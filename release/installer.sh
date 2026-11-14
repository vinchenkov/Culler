#!/bin/sh
# The permanent launcher supplies one selected release and private download state.
set -eu
set +x
umask 077
say() { printf '%s\n' "$*"; }
fail() {
  message_width=${COLUMNS:-80}
  case "$message_width" in ''|*[!0-9]*|????*) message_width=80 ;; esac
  [ "$message_width" -ge 40 ] || message_width=40
  [ "$message_width" -le 80 ] || message_width=80
  printf 'install.sh: %s\n' "$*" | fold -s -w "$message_width" >&2
  exit 1
}
[ -n "${CULLER_BOOTSTRAP_DIR:-}" ] && [ -n "${CULLER_REPOSITORY:-}" ] || fail "Run install.sh to select a complete release."
CULLER_BOOTSTRAP_DIR=$(CDPATH= cd -- "$CULLER_BOOTSTRAP_DIR" 2>/dev/null && pwd -P) || fail "Run install.sh to select a complete release."
[ "$(uname -s):$(uname -m)" = Darwin:arm64 ] || fail "Culler requires a Mac with Apple silicon."
record=$CULLER_BOOTSTRAP_DIR/release.json
json() { plutil -extract "$2" raw -o - "$1" 2>/dev/null; }
required_json() { json "$@" || fail "The release record is incomplete. Run install.sh again after publication completes."; }
fetch() {
  status=$(curl -q --config "$CULLER_BOOTSTRAP_DIR/curl.conf" --silent --location \
    --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 900 \
    --header 'Accept: application/octet-stream' --header 'X-GitHub-Api-Version: 2022-11-28' --header 'Cache-Control: no-cache' \
    --output "$2" --write-out '%{http_code}' "$1" 2>/dev/null) || \
    fail "The package download failed. Check the connection, then run install.sh again."
  case "$status" in
    200) ;;
    401|403|404) fail "GitHub package access failed. Renew repository access, then run install.sh again." ;;
    *) fail "GitHub returned HTTP $status. Run install.sh again to retry." ;;
  esac
}
[ "$(json "$record" package.name)" = culler-darwin-arm64.tar.gz ] || fail "The package name is invalid."
index=0
package_id=
while name=$(json "$CULLER_BOOTSTRAP_DIR/github-release.json" "assets.$index.name"); do
  if [ "$name" = culler-darwin-arm64.tar.gz ]; then
    [ -z "$package_id" ] || fail "The release contains duplicate packages."
    package_id=$(required_json "$CULLER_BOOTSTRAP_DIR/github-release.json" "assets.$index.id")
  fi
  index=$((index+1))
done
case "$package_id" in ''|*[!0-9]*) fail "The release package is absent." ;; esac
say 'Download and verify Culler...'
archive=$CULLER_BOOTSTRAP_DIR/package.tar.gz
fetch "https://api.github.com/repos/$CULLER_REPOSITORY/releases/assets/$package_id" "$archive"
checksum=$(required_json "$record" package.sha256)
case "$checksum" in ''|*[!0-9a-f]*) fail "The package checksum is invalid." ;; esac
[ "${#checksum}" = 64 ] && \
  [ "$(shasum -a 256 "$archive" | awk '{print $1}')" = "$checksum" ] && \
  [ "$(wc -c <"$archive" | tr -d ' ')" = "$(json "$record" package.size)" ] || \
  fail "The package failed verification. Run install.sh again to retry."
# Reject links and paths outside the package before extraction.
tar -tzf "$archive" >"$CULLER_BOOTSTRAP_DIR/paths" || fail "The package archive is invalid."
awk '
  /^bin\/(mc|install.sh|agentsview)$/ || /^host\/[A-Za-z0-9_.\/-]+$/ || /^runner\/[A-Za-z0-9_.\/-]+$/ { if ($0 ~ /(^|\/)\.\.(\/|$)/ || $0 ~ /\/\//) exit 1; next }
  { exit 1 }
' "$CULLER_BOOTSTRAP_DIR/paths" || fail "The package contains an invalid path."
tar -tvzf "$archive" >"$CULLER_BOOTSTRAP_DIR/entries" || fail "The package archive is invalid."
awk 'substr($0,1,1) != "-" {exit 1}' "$CULLER_BOOTSTRAP_DIR/entries" || fail "The package contains a file link or special file."
CULLER_PACKAGE_ROOT=$CULLER_BOOTSTRAP_DIR/package
mkdir "$CULLER_PACKAGE_ROOT"
tar -xzf "$archive" -C "$CULLER_PACKAGE_ROOT" || fail "The installer could not unpack the package. Run install.sh again."
[ -x "$CULLER_PACKAGE_ROOT/bin/mc" ] || fail "The package has no executable setup program."
CULLER_RELEASE_RECORD=$record
export CULLER_PACKAGE_ROOT CULLER_RELEASE_RECORD
say 'Culler passed the download checks.'

check_updates=0
selected_home=${MC_HOME:-$HOME/.culler}
selected_bin=${MC_BIN_DIR:-}
expect_home=0
expect_bin=0
for argument do
  if [ "$expect_home" = 1 ]; then selected_home=$argument; expect_home=0; continue; fi
  if [ "$expect_bin" = 1 ]; then selected_bin=$argument; expect_bin=0; continue; fi
  case "$argument" in
    --check-updates) check_updates=1 ;;
    --mc-home) expect_home=1 ;;
    --bin-dir) expect_bin=1 ;;
  esac
done
[ "$expect_home" = 0 ] || fail "The --mc-home flag requires a path."
[ "$expect_bin" = 0 ] || fail "The --bin-dir flag requires a path."
if [ -n "$selected_bin" ]; then
  case "$selected_bin" in /*) MC_BIN_DIR=$selected_bin; export MC_BIN_DIR ;; *) fail "The executable directory requires an absolute path." ;; esac
fi
if [ "$check_updates" = 1 ]; then
  MC_HOME="$selected_home" "$CULLER_PACKAGE_ROOT/bin/mc" __release status-terminal
  exit $?
fi

if ! xcode-select -p >/dev/null 2>&1; then
  say 'Install the Apple developer tools.'
  say 'Complete the Apple prompts, then run install.sh again.'
  xcode-select --install >/dev/null 2>&1 || fail "Open the Apple developer downloads page to install the command tools."
  exit 0
fi
command -v git >/dev/null 2>&1 && git --version >/dev/null 2>&1 || fail "Repair the Apple developer tools, then run install.sh again."
for tool in curl shasum unzip; do
  command -v "$tool" >/dev/null 2>&1 || fail "The required tool $tool is absent from the PATH. Add /usr/bin to the PATH, then run install.sh again."
done
export MC_HOME="$selected_home"
# The host Bun comes from the pinned Bun release archive, never from mise.
bun_version=$(required_json "$record" host_bun.version)
case "$bun_version" in ''|*[!0-9.]*) fail "The release record has an invalid host Bun version. Wait for the next release, then run install.sh again." ;; esac
bun_platform=darwin-arm64
bun_name=$(required_json "$record" "host_bun.archives.$bun_platform.name")
bun_url=$(required_json "$record" "host_bun.archives.$bun_platform.url")
bun_checksum=$(required_json "$record" "host_bun.archives.$bun_platform.sha256")
bun_size=$(required_json "$record" "host_bun.archives.$bun_platform.size")
case "$bun_name" in */*|.*|'') fail "The release record has an invalid Bun archive name. Wait for the next release, then run install.sh again." ;; *.zip) ;; *) fail "The release record has an invalid Bun archive name. Wait for the next release, then run install.sh again." ;; esac
case "$bun_url" in https://*) ;; *) fail "The release record has an invalid Bun archive address. Wait for the next release, then run install.sh again." ;; esac
case "$bun_checksum" in ''|*[!0-9a-f]*) fail "The release record has an invalid Bun archive checksum. Wait for the next release, then run install.sh again." ;; esac
[ "${#bun_checksum}" = 64 ] || fail "The release record has an invalid Bun archive checksum. Wait for the next release, then run install.sh again."
case "$bun_size" in ''|*[!0-9]*) fail "The release record has an invalid Bun archive size. Wait for the next release, then run install.sh again." ;; esac
bun_root=$MC_HOME/bun
bun_home=$bun_root/$bun_version
bun_program=$bun_home/bin/bun
if [ -x "$bun_program" ] && [ "$("$bun_program" --version 2>/dev/null)" = "$bun_version" ]; then
  say "Bun $bun_version is already installed."
else
  say "Download and verify Bun $bun_version..."
  mkdir -p "$bun_root" 2>/dev/null && bun_temp=$(mktemp -d "$bun_root/.download.XXXXXX") || \
    fail "The Culler home $MC_HOME is not writable. Check its permissions, then run install.sh again."
  bun_archive=$bun_temp/$bun_name
  if ! curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 15 --max-time 900 --output "$bun_archive" "$bun_url" >/dev/null 2>&1; then
    rm -rf "$bun_temp"
    fail "The Bun download failed. Check the connection, then run install.sh again."
  fi
  bun_actual=$(shasum -a 256 "$bun_archive" | awk '{print $1}')
  if [ "$bun_actual" != "$bun_checksum" ] || [ "$(wc -c <"$bun_archive" | tr -d ' ')" != "$bun_size" ]; then
    rm -rf "$bun_temp"
    fail "Bun $bun_version failed the download checks. Run install.sh again to retry the download."
  fi
  bun_member=${bun_name%.zip}/bun
  if ! unzip -q "$bun_archive" "$bun_member" -d "$bun_temp" >/dev/null 2>&1 || [ ! -f "$bun_temp/$bun_member" ]; then
    rm -rf "$bun_temp"
    fail "The installer could not unpack the Bun archive. Run install.sh again."
  fi
  chmod 755 "$bun_temp/$bun_member"
  if [ "$("$bun_temp/$bun_member" --version 2>/dev/null)" != "$bun_version" ]; then
    rm -rf "$bun_temp"
    fail "The Bun program of the release does not report version $bun_version. Wait for the next release, then run install.sh again."
  fi
  if ! mkdir -p "$bun_home/bin" || ! mv -f "$bun_temp/$bun_member" "$bun_program"; then
    rm -rf "$bun_temp"
    fail "The installer could not move the Bun program into the Culler home $MC_HOME. Check its permissions, then run install.sh again."
  fi
  rm -rf "$bun_temp"
  say "Bun $bun_version is installed."
fi
if [ -z "${MC_BIN_DIR:-}" ]; then
  installed_program=$(json "$MC_HOME/supervision/resident.json" mcPath || true)
  case "$installed_program" in
    /*) MC_BIN_DIR=$(dirname -- "$installed_program") ;;
    *) MC_BIN_DIR=$HOME/.local/bin ;;
  esac
  export MC_BIN_DIR
fi
say 'Open Culler setup.'
PATH="$bun_home/bin:$PATH"
export PATH
"$CULLER_PACKAGE_ROOT/bin/mc" __setup "$@" \
  --release-source "$CULLER_PACKAGE_ROOT/runner" --host-release-source "$CULLER_PACKAGE_ROOT/host"
