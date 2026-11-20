#!/bin/sh
# Culler installs the latest complete release from GitHub.
# Run this script again to check for updates.
# Production requires macOS on Apple silicon, or Linux on x86_64.
# --dev uses the local source checkout and a scratch deployment home.
# All other arguments pass unchanged to the selected release installer.
set -eu
set +x
umask 077
CULLER_INSTALL_STARTED_AT=$(date +%s)
export CULLER_INSTALL_STARTED_AT
CULLER_PARENT_PATH=${CULLER_PARENT_PATH-$PATH}
export CULLER_PARENT_PATH
say() { printf '%s\n' "$*"; }
fail() {
  message_width=${COLUMNS:-80}
  case "$message_width" in ''|*[!0-9]*|????*) message_width=80 ;; esac
  [ "$message_width" -ge 40 ] || message_width=40
  [ "$message_width" -le 80 ] || message_width=80
  printf 'install.sh: %s\n' "$*" | fold -s -w "$message_width" >&2
  exit 1
}

for argument do
  case "$argument" in
    --static-binding|--static-binding=*|--static-credential|--static-credential=*|--static-token-file|--static-token-file=*)
      fail "Static credentials are unsupported. Use the Codex or Claude Code sign-in." ;;
    -h|--help)
      cat <<'HELP'
Culler installation and updates

Usage: sh install.sh [options]

Run install.sh to install or update Culler.
Each invocation selects a complete release.
Production requires macOS on Apple silicon or Linux on x86_64.

  --mc-home <path>   Select the deployment home.
  --bin-dir <path>   Select the executable directory.
  --check-updates    Report release availability.
  --yes             Use explicit setup options.
  --interrupt-runs  Authorize interruption during an update.
  --restore         Restore an incomplete update.
  --activate        Authorize service activation.
  --dev             Use a local source checkout.
  --help            Show this help without a download.

Other setup options pass to the release installer.
See the install guide for setup instructions:
https://github.com/vinchenkov/Culler/blob/main/INSTALL.md
HELP
      exit 0 ;;
  esac
done
if [ -z "${MC_HOME:-}" ] && [ -f "$0" ]; then
  launcher_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
  case "$launcher_directory" in
    */bin)
      launcher_home=${launcher_directory%/bin}
      if [ -f "$launcher_home/state/published-release.json" ] || [ -f "$launcher_home/supervision/resident.json" ]; then
        MC_HOME=$launcher_home
        export MC_HOME
      fi ;;
  esac
fi
for argument do
  case "$argument" in
    --dev)
      source_script=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/scripts/install-source.sh
      [ -f "$source_script" ] || fail "Development setup requires a Culler checkout."
      exec sh "$source_script" "$@" ;;
  esac
done
case "$(uname -s):$(uname -m)" in
  Darwin:arm64) CULLER_PLATFORM=darwin-arm64 ;;
  Linux:x86_64) CULLER_PLATFORM=linux-amd64 ;;
  *) fail "Culler runs on a Mac with Apple silicon or on Linux for x86_64." ;;
esac
for tool in curl tar unzip; do
  command -v "$tool" >/dev/null 2>&1 || fail "The required tool $tool is absent from the PATH. Add /usr/bin to the PATH, then run install.sh again."
done
case "$CULLER_PLATFORM" in
  darwin-arm64)
    for tool in plutil shasum; do
      command -v "$tool" >/dev/null 2>&1 || fail "The required tool $tool is absent from the PATH. Add /usr/bin to the PATH, then run install.sh again."
    done
    json() { plutil -extract "$2" raw -o - "$1" 2>/dev/null; }
    sha256_of() { shasum -a 256 "$1" | awk '{print $1}'; }
    stat_identity() { stat -f '%u %Lp %l' "$1"; }
    record_asset=release.json ;;
  linux-amd64)
    for tool in python3 sha256sum stat; do
      command -v "$tool" >/dev/null 2>&1 || fail "The required tool $tool is absent from the PATH. Install it, then run install.sh again."
    done
    # The Linux path parses the same JSON with python3, printing the scalar
    # the way plutil -extract raw prints it: true and false in lowercase.
    json() {
      python3 -I -c '
import json, sys
try:
    doc = json.load(open(sys.argv[1]))
    for part in sys.argv[2].split("."):
        if isinstance(doc, list):
            if not part.isdigit() or int(part) >= len(doc):
                sys.exit(1)
            doc = doc[int(part)]
        elif isinstance(doc, dict):
            if part not in doc:
                sys.exit(1)
            doc = doc[part]
        else:
            sys.exit(1)
except (OSError, ValueError):
    sys.exit(1)
if isinstance(doc, bool):
    print("true" if doc else "false")
elif doc is None:
    print("null")
elif isinstance(doc, (int, float, str)):
    print(doc)
else:
    sys.exit(1)
' "$1" "$2"
    }
    sha256_of() { sha256sum "$1" | cut -d ' ' -f 1; }
    stat_identity() { stat -c '%u %a %h' "$1"; }
    record_asset=release-linux-amd64.json ;;
esac
# Recover the exact retained release before any release discovery.
recovery_home=${MC_HOME:-$HOME/.culler}
recovery_home_next=false
recovery_restore=false
recovery_check=false
for argument do
  if [ "$recovery_home_next" = true ]; then
    recovery_home=$argument
    recovery_home_next=false
    continue
  fi
  case "$argument" in
    --mc-home) recovery_home_next=true ;;
    --restore) recovery_restore=true ;;
    --check-updates) recovery_check=true ;;
  esac
done
[ "$recovery_home_next" = false ] || fail "The --mc-home option requires a path."
case "$recovery_home" in /*) ;; *) fail "The deployment home requires an absolute path." ;; esac
if [ -d "$recovery_home" ]; then
  recovery_home=$(CDPATH= cd -- "$recovery_home" && pwd -P)
fi
recovery_record=$recovery_home/updates/transaction.json
if [ -e "$recovery_record" ] || [ -L "$recovery_record" ]; then
  [ -f "$recovery_record" ] && [ ! -L "$recovery_record" ] || fail "The update record requires a private regular file."
  [ "$(stat_identity "$recovery_record")" = "$(id -u) 600 1" ] || fail "The update record requires owner-only access."
  recovery_phase=$(json "$recovery_record" phase) || fail "The update record is unreadable. Keep Culler stopped and repair it."
  case "$recovery_phase" in
    complete|restored) ;;
    stop|prepare|checkpoint|replace|verify|committed|restore)
      [ "$recovery_check" = false ] || fail "An incomplete update requires recovery. Run culler update."
      recovery_id=$(json "$recovery_record" id)
      case "$recovery_id" in ''|*[!0-9a-f]*) fail "The update identity is invalid." ;; esac
      [ "${#recovery_id}" -ge 16 ] && [ "${#recovery_id}" -le 64 ] || fail "The update identity is invalid."
      recovery_root=$(json "$recovery_record" candidate_root)
      [ "$recovery_root" = "$recovery_home/updates/$recovery_id/candidate" ] || fail "The retained update path is invalid."
      recovery_program=$recovery_root/bin/mc
      [ -f "$recovery_program" ] && [ ! -L "$recovery_program" ] && [ -x "$recovery_program" ] || fail "The retained update command is absent. Keep Culler stopped and repair the recovery files."
      recovery_checksum=$(json "$recovery_record" candidate_sha256)
      [ "$(sha256_of "$recovery_program")" = "$recovery_checksum" ] || fail "The retained update command failed verification. Keep Culler stopped and repair the recovery files."
      MC_HOME=$recovery_home
      MC_BIN_DIR=$(json "$recovery_record" bin_dir)
      CULLER_PACKAGE_ROOT=$recovery_root
      CULLER_RELEASE_RECORD=$recovery_root/release.json
      export MC_HOME MC_BIN_DIR CULLER_PACKAGE_ROOT CULLER_RELEASE_RECORD
      if [ ! -t 0 ] && [ -t 1 ] && [ -r /dev/tty ]; then
        exec "$recovery_program" __setup "$@" --release-source "$recovery_root/runner" --host-release-source "$recovery_root/host" </dev/tty
      fi
      exec "$recovery_program" __setup "$@" --release-source "$recovery_root/runner" --host-release-source "$recovery_root/host" ;;
    *) fail "The update phase is invalid. Keep Culler stopped and repair the update record." ;;
  esac
fi
[ "$recovery_restore" = false ] || fail "This deployment has no incomplete update to restore."
CULLER_REPOSITORY=${CULLER_REPOSITORY:-vinchenkov/Culler}
case "$CULLER_REPOSITORY" in
  *[!a-zA-Z0-9._/-]*|/*|*/|*/*/*|*..*) fail "The GitHub repository must use owner/name." ;;
  */*) ;;
  *) fail "The GitHub repository must use owner/name." ;;
esac
CULLER_BOOTSTRAP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/culler-install.XXXXXX")
trap 'rm -rf "$CULLER_BOOTSTRAP_DIR"' EXIT HUP INT TERM
export CULLER_BOOTSTRAP_DIR CULLER_REPOSITORY
credential=${CULLER_GITHUB_TOKEN_FILE:-$HOME/.config/culler/github-token}
if [ -e "$credential" ] || [ -n "${CULLER_GITHUB_TOKEN_FILE:-}" ]; then
  [ -f "$credential" ] && [ ! -L "$credential" ] || fail "The GitHub token file must be a private regular file."
  [ "$(stat_identity "$credential")" = "$(id -u) 600 1" ] || \
    fail "Set the GitHub token file owner to your account and its permissions to 600."
  cp "$credential" "$CULLER_BOOTSTRAP_DIR/token"
elif command -v gh >/dev/null 2>&1; then
  gh auth token --hostname github.com >"$CULLER_BOOTSTRAP_DIR/token" 2>/dev/null || rm -f "$CULLER_BOOTSTRAP_DIR/token"
fi
: >"$CULLER_BOOTSTRAP_DIR/curl.conf"
if [ -s "$CULLER_BOOTSTRAP_DIR/token" ]; then
  token=$(cat "$CULLER_BOOTSTRAP_DIR/token")
  case "$token" in *[!a-zA-Z0-9_]*|'') fail "The GitHub credential is invalid. Renew access, then run install.sh again." ;; esac
  printf 'header = "Authorization: Bearer %s"\n' "$token" >"$CULLER_BOOTSTRAP_DIR/curl.conf"
  unset token
  CULLER_GITHUB_TOKEN_FILE=$CULLER_BOOTSTRAP_DIR/token
  export CULLER_GITHUB_TOKEN_FILE
else
  unset CULLER_GITHUB_TOKEN_FILE
fi
required_json() { json "$@" || fail "The release record is incomplete. Run install.sh again after publication completes."; }
fetch() {
  status=$(curl -q --config "$CULLER_BOOTSTRAP_DIR/curl.conf" --silent --location \
    --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 300 \
    --header "Accept: $3" --header 'X-GitHub-Api-Version: 2022-11-28' --header 'Cache-Control: no-cache' \
    --output "$2" --write-out '%{http_code}' "$1" 2>/dev/null) || \
    fail "The download failed. Check the connection, then run install.sh again."
  case "$status" in
    200) ;;
    401|403) fail "GitHub refused access. Renew the repository and package credentials, then run install.sh again." ;;
    404)
      if [ "$1" = "https://api.github.com/repos/$CULLER_REPOSITORY/releases/latest" ]; then
        fetch "https://api.github.com/repos/$CULLER_REPOSITORY" "$CULLER_BOOTSTRAP_DIR/repository.json" application/vnd.github+json
        fail "No complete release is available. Wait for the release workflow, then run install.sh again."
      fi
      fail "GitHub repository access is absent. Renew access, then run install.sh again." ;;
    *) fail "GitHub returned HTTP $status. Run install.sh again to retry." ;;
  esac
}
asset_id() {
  index=0
  found=
  while name=$(json "$CULLER_BOOTSTRAP_DIR/github-release.json" "assets.$index.name"); do
    if [ "$name" = "$1" ]; then
      [ -z "$found" ] || fail "The release contains duplicate files. Ask your coding harness to inspect the release."
      found=$(json "$CULLER_BOOTSTRAP_DIR/github-release.json" "assets.$index.id")
    fi
    index=$((index+1))
  done
  case "$found" in ''|*[!0-9]*) fail "The release is incomplete. Run install.sh again after publication completes." ;; esac
  printf '%s\n' "$found"
}
say 'Culler'
say 'Check the published release...'
api="https://api.github.com/repos/$CULLER_REPOSITORY/releases"
fetch "$api/latest" "$CULLER_BOOTSTRAP_DIR/github-release.json" application/vnd.github+json
[ "$(json "$CULLER_BOOTSTRAP_DIR/github-release.json" draft)" = false ] && \
  [ "$(json "$CULLER_BOOTSTRAP_DIR/github-release.json" prerelease)" = false ] || fail "No complete production release is available."
record_id=$(asset_id "$record_asset")
fetch "$api/assets/$record_id" "$CULLER_BOOTSTRAP_DIR/release.json" application/octet-stream
record=$CULLER_BOOTSTRAP_DIR/release.json
[ "$(json "$record" format)" = 1 ] || fail "The release format is unsupported. Download install.sh again."
[ "$(json "$record" platform)" = "$CULLER_PLATFORM" ] || fail "The release record is not for this platform."
commit=$(required_json "$record" commit)
case "$commit" in ''|*[!0-9a-f]*) fail "The release identity is invalid." ;; esac
version=$(required_json "$record" version)
printf '%s\n' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail "The release identity is invalid."
tag=v$version
test_release=$(json "$record" test_release) || test_release=false
case "$test_release" in
  true) tag=test-$commit ;;
  false|'') ;;
  *) fail "The release identities differ. Run install.sh again." ;;
esac
[ "${#commit}" = 40 ] && [ "$(json "$record" release_id)" = "$commit" ] && \
  [ "$(json "$record" tag)" = "$tag" ] && \
  [ "$(json "$CULLER_BOOTSTRAP_DIR/github-release.json" tag_name)" = "$tag" ] || fail "The release identities differ. Run install.sh again."
for required in fast-suite-macos deployment-linux application-package product-image release-downloads; do
  index=0
  found=0
  while name=$(json "$record" "checks.$index.name"); do
    [ "$(json "$record" "checks.$index.passed")" = true ] || fail "The release checks are incomplete."
    if [ "$name" = "$required" ]; then found=$((found+1)); fi
    index=$((index+1))
  done
  [ "$found" = 1 ] || fail "The release checks are incomplete."
done
[ "$(json "$record" installer.name)" = installer.sh ] || fail "The installer name is invalid."
installer_id=$(asset_id installer.sh)
# Resolve every required file before any downloaded code starts.
asset_id "culler-$CULLER_PLATFORM.tar.gz" >/dev/null
say 'Download and verify the installer...'
fetch "$api/assets/$installer_id" "$CULLER_BOOTSTRAP_DIR/installer.sh" application/octet-stream
checksum=$(required_json "$record" installer.sha256)
case "$checksum" in ''|*[!0-9a-f]*) fail "The installer checksum is invalid." ;; esac
[ "${#checksum}" = 64 ] || fail "The installer checksum is invalid."
[ "$(sha256_of "$CULLER_BOOTSTRAP_DIR/installer.sh")" = "$checksum" ] && \
  [ "$(wc -c <"$CULLER_BOOTSTRAP_DIR/installer.sh" | tr -d ' ')" = "$(json "$record" installer.size)" ] || \
  fail "The installer failed verification. Run install.sh again to retry."
# A pipe carries the launcher, while the terminal carries operator input.
if [ ! -t 0 ] && [ -t 1 ] && [ -r /dev/tty ]; then
  sh "$CULLER_BOOTSTRAP_DIR/installer.sh" "$@" </dev/tty
else
  sh "$CULLER_BOOTSTRAP_DIR/installer.sh" "$@"
fi
