#!/bin/sh
# Release downloads use isolated fixtures and never contact a real service.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CALLER_PATH=$PATH CALLER_HOME=${HOME:-}
TMP=$(mktemp -d "${TMPDIR:-/tmp}/culler-launcher-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
fail() { printf 'install_test.sh: %s\n' "$*" >&2; exit 1; }
contains() { tr '\r\n' ' ' <"$1" | tr -s ' ' | grep -F -- "$2" >/dev/null; }
mkdir -p "$TMP/bin" "$TMP/home" "$TMP/fixtures" "$TMP/package/bin" "$TMP/package/host" "$TMP/package/runner" "$TMP/outside"
cp "$ROOT/install.sh" "$TMP/outside/install.sh"
sh "$TMP/outside/install.sh" --help >"$TMP/help.out" 2>&1 || fail 'local help failed'
grep -F -- --check-updates "$TMP/help.out" >/dev/null || fail 'local help omitted the update check'
for entry in "$TMP/outside/install.sh" "$ROOT/scripts/install-source.sh"; do
  for option in --static-binding --static-credential --static-token-file; do
    if sh "$entry" "$option" fixture-secret >"$TMP/refused.out" 2>&1; then
      fail "The installer accepted $option."
    fi
    contains "$TMP/refused.out" 'Static credentials are unsupported.' || fail 'The installer omitted the refusal.'
    if grep -F fixture-secret "$TMP/refused.out" >/dev/null; then fail 'The installer disclosed the supplied value.'; fi
  done
done
cp "$ROOT/install.sh" "$TMP/package/bin/install.sh"
cat >"$TMP/package/bin/mc" <<'MC'
#!/bin/sh
printf '%s\n' "$@" >"$CULLER_FIXTURE_LOG"
printf '%s\n' "${MC_HOME:-}" "${CULLER_RELEASE_RECORD:-}" "${CULLER_PACKAGE_ROOT:-}" "${MC_BIN_DIR:-}" "${CULLER_INSTALL_STARTED_AT:-}" "${PATH%%:*}" >"$CULLER_FIXTURE_ENV"
if [ -x "${CULLER_PACKAGE_ROOT:-}/bin/agentsview" ]; then printf 'agentsview\n' >>"$CULLER_FIXTURE_ENV"; fi
exit "${CULLER_FIXTURE_EXIT:-0}"
MC
chmod 755 "$TMP/package/bin/mc"
printf '#!/bin/sh\nexit 0\n' >"$TMP/package/bin/agentsview"
chmod 755 "$TMP/package/bin/agentsview"
printf 'host file\n' >"$TMP/package/host/file"
printf 'runner file\n' >"$TMP/package/runner/file"
cat >"$TMP/bin/uname" <<'CMD'
#!/bin/sh
case "$1" in -s) printf '%s\n' "${CULLER_FIXTURE_OS:-Darwin}" ;; -m) printf 'arm64\n' ;; esac
CMD
cat >"$TMP/bin/xcode-select" <<'CMD'
#!/bin/sh
exit 0
CMD
cat >"$TMP/bin/git" <<'CMD'
#!/bin/sh
[ "$1" = --version ] || exit 91
printf 'git fixture\n'
CMD
cat >"$TMP/bin/gh" <<'CMD'
#!/bin/sh
exit 1
CMD
# The published installer never calls mise. Any call fails the install.
cat >"$TMP/bin/mise" <<'CMD'
#!/bin/sh
printf '%s\n' "$*" >>"$CULLER_FIXTURE_MISE"
exit 97
CMD
cat >"$TMP/bin/curl" <<'CMD'
#!/bin/sh
output=
url=
config=
printf '%s\n' "${CULLER_INSTALL_STARTED_AT:-}" >>"$CULLER_FIXTURE_STARTED_AT"
for argument do printf '%s\n' "$argument" >>"$CULLER_FIXTURE_CURL"; done
while [ "$#" -gt 0 ]; do
 case "$1" in --output) output=$2; shift ;; --config) config=$2; shift ;; https://*) url=$1 ;; esac
 shift
done
if [ -n "${CULLER_FIXTURE_HTTP:-}" ]; then
  case "$url" in
    */repos/vinchenkov/Culler) printf '{}' >"$output"; printf '%s' "${CULLER_FIXTURE_REPO_STATUS:-200}" ;;
    *) printf '%s' "$CULLER_FIXTURE_HTTP" ;;
  esac
  exit 0
fi
if [ "${CULLER_FIXTURE_PRIVATE:-0}" = 1 ]; then
 grep -F 'Authorization: Bearer fixture_private_token' "$config" >/dev/null || { printf 401; exit 0; }
fi
case "$url" in
 */releases/latest) source=github-release.json ;;
 */releases/assets/1) source=release.json ;;
 */releases/assets/2) source=installer.sh ;;
 */releases/assets/3) source=package.tar.gz ;;
 */releases/download/bun-v1.3.9/bun-darwin-aarch64.zip) source=bun.zip ;;
 *) exit 93 ;;
esac
cp "$CULLER_FIXTURE_DIR/$source" "$output"
if [ "$source" = installer.sh ] && [ "${CULLER_FIXTURE_CORRUPT:-0}" = 1 ]; then printf '# bad bytes\n' >>"$output"; fi
if [ "$source" = bun.zip ] && [ "${CULLER_FIXTURE_BUN_CORRUPT:-0}" = 1 ]; then printf 'bad bytes\n' >>"$output"; fi
printf 200
CMD
if [ "$(uname -s)" = Linux ]; then
  cat >"$TMP/bin/stat" <<'CMD'
#!/bin/sh
shift 2
exec /usr/bin/stat -c '%u %a %h' "$@"
CMD
  cat >"$TMP/bin/plutil" <<'CMD'
#!/usr/bin/env python3
import json,sys
try:
    value=json.load(open(sys.argv[-1]))
    for key in sys.argv[2].split('.'):
        value=value[int(key)] if isinstance(value,list) else value[key]
    print(str(value).lower() if isinstance(value,bool) else value)
except (OSError,ValueError,KeyError,IndexError):
    sys.exit(1)
CMD
fi
chmod 755 "$TMP/bin/"*
# A Linux image can keep python3 outside the system paths.
if [ ! -x /usr/bin/python3 ] && [ ! -x /bin/python3 ]; then
  ln -s "$(command -v python3)" "$TMP/bin/python3"
fi
export PATH="$TMP/bin:/usr/bin:/bin"
export HOME="$TMP/home"
export CULLER_FIXTURE_DIR="$TMP/fixtures"
export CULLER_FIXTURE_LOG="$TMP/args"
export CULLER_FIXTURE_ENV="$TMP/env"
export CULLER_FIXTURE_CURL="$TMP/curl-args"
export CULLER_FIXTURE_STARTED_AT="$TMP/download-starts"
export CULLER_FIXTURE_MISE="$TMP/mise-args"
# The Bun archive fixture holds a program that reports the pinned version.
mkdir -p "$TMP/bun-fixture/bun-darwin-aarch64"
cat >"$TMP/bun-fixture/bun-darwin-aarch64/bun" <<'CMD'
#!/bin/sh
[ "${1:-}" = --version ] || exit 91
printf '1.3.9\n'
CMD
chmod 755 "$TMP/bun-fixture/bun-darwin-aarch64/bun"
(cd "$TMP/bun-fixture" && zip -q -r "$TMP/fixtures/bun.zip" bun-darwin-aarch64)
unset MC_HOME CULLER_GITHUB_TOKEN_FILE CULLER_BOOTSTRAP_DIR CULLER_PACKAGE_ROOT CULLER_RELEASE_RECORD
cp "$ROOT/release/installer.sh" "$TMP/fixtures/installer.sh"
tar -czf "$TMP/fixtures/package.tar.gz" -C "$TMP/package" bin/mc bin/install.sh bin/agentsview host/file runner/file
# macOS supplies plutil. The JSON fixture writer remains a test dependency.
python3 - "$TMP/fixtures" <<'PY'
import hashlib,json,sys
from pathlib import Path
root=Path(sys.argv[1]);commit='a'*40
assets=[]
def asset(filename,id,name=None):
 p=root/filename
 a={'name':name or filename,'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'size':p.stat().st_size}
 assets.append({'id':id,'name':a['name'],'size':a['size']})
 return a
bun=root/'bun.zip'
host_bun={'version':'1.3.9','archives':{'darwin-arm64':{'name':'bun-darwin-aarch64.zip','url':'https://github.com/oven-sh/bun/releases/download/bun-v1.3.9/bun-darwin-aarch64.zip','size':bun.stat().st_size,'sha256':hashlib.sha256(bun.read_bytes()).hexdigest()}}}
record={'format':1,'version':'0.1.0','commit':commit,'release_id':commit,'tag':'v0.1.0','sequence':7,'published_at':'2026-09-26T00:00:00Z','platform':'darwin-arm64','schema':'f'*64,'runtimes':{'go':'1.25.12','bun':'1.3.9'},'host_bun':host_bun,'image':'ghcr.io/owner/culler@sha256:'+'b'*64,'checks':[{'name':n,'passed':True} for n in ['fast-suite-macos','deployment-linux','application-package','product-image','release-downloads']],'installer':asset('installer.sh',2),'package':asset('package.tar.gz',3,'culler-darwin-arm64.tar.gz')}
(root/'release.json').write_text(json.dumps(record));assets.append({'id':1,'name':'release.json','size':(root/'release.json').stat().st_size})
(root/'github-release.json').write_text(json.dumps({'tag_name':record['tag'],'draft':False,'prerelease':False,'assets':assets}))
PY
invocation_started=$(date +%s)
mkdir "$TMP/download temp"
ln -s "$TMP/download temp" "$TMP/download-alias"
download_root=$(CDPATH= cd -- "$TMP/download temp" && pwd -P)
TMPDIR="$TMP/download-alias/" CULLER_INSTALL_STARTED_AT=1 sh "$TMP/outside/install.sh" --mc-home "$TMP/deployment home" --yes --activate >"$TMP/install.out" 2>&1 || { cat "$TMP/install.out" >&2; fail 'saved launcher failed'; }
package_root=$(sed -n '3p' "$TMP/env")
case "$package_root" in
  "$download_root"/culler-install.*/package) ;;
  *) fail 'setup received a package path with an alias or repeated separator' ;;
esac
grep -Fx -- "$package_root/host" "$TMP/args" >/dev/null || fail 'setup received a different host path'
grep -Fx -- "$package_root/runner" "$TMP/args" >/dev/null || fail 'setup received a different runner path'
[ "$(sed -n '7p' "$TMP/env")" = agentsview ] || fail 'setup received a package without the AgentsView executable'
bun_home="$TMP/deployment home/bun/1.3.9"
[ -x "$bun_home/bin/bun" ] || fail 'installer did not install the host Bun'
[ "$("$bun_home/bin/bun" --version)" = 1.3.9 ] || fail 'installed host Bun is a different program'
[ "$(sed -n '6p' "$TMP/env")" = "$bun_home/bin" ] || fail 'setup PATH did not start with the host Bun directory'
[ ! -s "$TMP/mise-args" ] || fail 'installer called mise'
[ -z "$(find "$TMP/deployment home/bun" -mindepth 1 -maxdepth 1 ! -name 1.3.9)" ] || fail 'installer left files beside the host Bun'
contains "$TMP/install.out" 'Download and verify Bun 1.3.9' || fail 'installer omitted the Bun download step'
contains "$TMP/install.out" 'Bun 1.3.9 is installed.' || fail 'installer omitted the Bun download result'
[ "$(grep -c -- 'bun-v1.3.9/bun-darwin-aarch64.zip' "$TMP/curl-args")" = 1 ] || fail 'installer downloaded the Bun archive more than once'
installation_started=$(sed -n '5p' "$TMP/env")
case "$installation_started" in ''|*[!0-9]*) fail 'launcher omitted its timestamp' ;; esac
[ "$installation_started" -ge "$invocation_started" ] && [ "$installation_started" -le "$(date +%s)" ] || fail 'launcher accepted an inherited timestamp'
[ "$(sed -n '1p' "$TMP/download-starts")" = "$installation_started" ] || fail 'launcher started its timer after the first download'
[ "$(sed -n '1p' "$TMP/args")" = __setup ] || fail 'launcher did not start setup'
grep -Fx -- "$TMP/deployment home" "$TMP/args" >/dev/null || fail 'launcher lost an argument with spaces'
grep -Fx -- --activate "$TMP/args" >/dev/null || fail 'launcher lost the activation choice'
grep -Fx -- --host-release-source "$TMP/args" >/dev/null || fail 'launcher lost the package host files'
[ ! -e "$TMP/home/.culler" ] || fail 'fixture changed deployment state'
# A second install of the same version reuses the installed host Bun.
sh "$TMP/outside/install.sh" --mc-home "$TMP/deployment home" --yes --activate >"$TMP/reuse.out" 2>&1 || { cat "$TMP/reuse.out" >&2; fail 'second install failed'; }
[ "$(grep -c -- 'bun-v1.3.9/bun-darwin-aarch64.zip' "$TMP/curl-args")" = 1 ] || fail 'second install downloaded the Bun archive again'
contains "$TMP/reuse.out" 'Bun 1.3.9 is already installed.' || fail 'second install did not report the installed host Bun'
if contains "$TMP/reuse.out" 'Download and verify Bun'; then fail 'second install reported a Bun download'; fi
[ "$(sed -n '6p' "$TMP/env")" = "$bun_home/bin" ] || fail 'second install lost the host Bun PATH'
# Test releases use the normal installer and one exact commit tag.
cp "$TMP/fixtures/release.json" "$TMP/public-release.json"
cp "$TMP/fixtures/github-release.json" "$TMP/public-github-release.json"
python3 - "$TMP/fixtures" <<'PYTEST'
import json, sys
from pathlib import Path
root = Path(sys.argv[1])
record = json.loads((root/'release.json').read_text())
record['test_release'] = True
record['tag'] = 'test-' + record['commit']
(root/'release.json').write_text(json.dumps(record))
release = json.loads((root/'github-release.json').read_text())
release['tag_name'] = record['tag']
(root/'github-release.json').write_text(json.dumps(release))
PYTEST
sh "$TMP/outside/install.sh" --mc-home "$TMP/deployment home" --yes --activate >"$TMP/test-release.out" 2>&1 || { cat "$TMP/test-release.out" >&2; fail 'test release install failed'; }
python3 - "$TMP/fixtures/release.json" <<'PYTEST'
import json, sys
from pathlib import Path
path = Path(sys.argv[1]); record = json.loads(path.read_text())
record['tag'] = 'test-' + 'b' * 40
path.write_text(json.dumps(record))
PYTEST
rm -f "$TMP/args"
if sh "$TMP/outside/install.sh" --mc-home "$TMP/deployment home" --yes --activate >"$TMP/test-tag.out" 2>&1; then fail 'test release accepted a different commit tag'; fi
[ ! -e "$TMP/args" ] || fail 'invalid test tag started setup'
mv "$TMP/public-release.json" "$TMP/fixtures/release.json"
mv "$TMP/public-github-release.json" "$TMP/fixtures/github-release.json"
# A Bun archive with another checksum stops the install before setup and leaves no Bun.
rm -f "$TMP/args"
if CULLER_FIXTURE_BUN_CORRUPT=1 sh "$TMP/outside/install.sh" --mc-home "$TMP/corrupt home" --yes --activate >"$TMP/bun-hash.out" 2>&1; then fail 'corrupt Bun archive succeeded'; fi
contains "$TMP/bun-hash.out" 'Bun 1.3.9 failed the download checks.' || fail 'Bun checksum refusal was absent'
[ ! -e "$TMP/args" ] || fail 'corrupt Bun archive started setup'
[ -z "$(ls -A "$TMP/corrupt home/bun" 2>/dev/null)" ] || fail 'corrupt Bun download left files'
if [ -n "${CULLER_TEST_ARTIFACT_DIR:-}" ]; then
  mkdir -p "$CULLER_TEST_ARTIFACT_DIR"
  cat >"$TMP/preview-launcher" <<'PREVIEW'
#!/bin/sh
stty cols "$COLUMNS"
exec sh "$CULLER_PREVIEW_LAUNCHER" "$@"
PREVIEW
  export CULLER_PREVIEW_LAUNCHER="$TMP/outside/install.sh"
  for width in 80 40; do
    for mode in color plain; do
      no_color=
      [ "$mode" = color ] || no_color=1
      COLUMNS="$width" TERM=xterm-256color NO_COLOR="$no_color" \
        script -q "$CULLER_TEST_ARTIFACT_DIR/launcher-$width-$mode.txt" \
        sh "$TMP/preview-launcher" --check-updates </dev/null >/dev/null 2>&1
      for scenario in auth no-release checksum bun-checksum bun-install bun-reuse; do
        http_status=
        corrupt=0
        bun_corrupt=0
        set --
        case "$scenario" in
          auth) http_status=403 ;;
          no-release) http_status=404 ;;
          checksum) corrupt=1 ;;
          bun-checksum) bun_corrupt=1; set -- --mc-home "$TMP/preview $scenario $width $mode" --yes --activate ;;
          bun-install) set -- --mc-home "$TMP/preview bun $width $mode" --yes --activate ;;
          bun-reuse) set -- --mc-home "$TMP/preview bun $width $mode" --yes --activate ;;
        esac
        COLUMNS="$width" TERM=xterm-256color NO_COLOR="$no_color" \
          CULLER_FIXTURE_HTTP="$http_status" CULLER_FIXTURE_CORRUPT="$corrupt" CULLER_FIXTURE_BUN_CORRUPT="$bun_corrupt" \
          script -q "$CULLER_TEST_ARTIFACT_DIR/launcher-$scenario-$width-$mode.txt" \
          sh "$TMP/preview-launcher" "$@" </dev/null >/dev/null 2>&1 || true
      done
    done
  done
fi
: >"$TMP/mise-args"
: >"$TMP/curl-args"
cat "$TMP/outside/install.sh" | sh -s -- --check-updates --mc-home "$TMP/check home" >"$TMP/check.out" 2>&1 || fail 'pipe invocation failed'
[ "$(cat "$TMP/args")" = "$(printf '%s\n' __release status-terminal)" ] || fail 'update check did not use the status command'
[ "$(sed -n '1p' "$TMP/env")" = "$TMP/check home" ] || fail 'update check lost the deployment home'
[ ! -s "$TMP/mise-args" ] || fail 'update check installed host tools'
if grep -F 'bun-darwin-aarch64.zip' "$TMP/curl-args" >/dev/null; then fail 'update check downloaded the Bun archive'; fi
[ ! -e "$TMP/check home/bun" ] || fail 'update check installed the host Bun'
mkdir -p "$TMP/saved home/bin" "$TMP/saved home/state"
cp "$ROOT/install.sh" "$TMP/saved home/bin/install.sh"
printf '{}\n' >"$TMP/saved home/state/published-release.json"
invocation_started=$(date +%s)
CULLER_INSTALL_STARTED_AT=2 sh "$TMP/saved home/bin/install.sh" --check-updates >"$TMP/saved.out" 2>&1 || fail 'installed launcher failed'
[ "$(sed -n '5p' "$TMP/env")" -ge "$invocation_started" ] || fail 'installed launcher reused an inherited timestamp'
[ "$(sed -n '1p' "$TMP/env")" = "$(CDPATH= cd -- "$TMP/saved home" && pwd)" ] || fail 'installed launcher lost its deployment home'
mkdir -p "$TMP/custom home/supervision"
printf '{"mcPath":"%s/custom bin/mc"}\n' "$TMP" >"$TMP/custom home/supervision/resident.json"
sh "$TMP/outside/install.sh" --mc-home "$TMP/custom home" --yes --activate >"$TMP/custom.out" 2>&1 || fail 'custom executable directory failed'
[ "$(sed -n '4p' "$TMP/env")" = "$TMP/custom bin" ] || fail 'installer lost the custom executable directory'
if CULLER_FIXTURE_EXIT=17 sh "$TMP/outside/install.sh" --check-updates >"$TMP/exit.out" 2>&1; then fail 'launcher lost the installer failure'; else [ "$?" = 17 ] || fail 'launcher changed the installer exit code'; fi
mkdir -p "$HOME/.config/culler"
printf 'fixture_private_token\n' >"$HOME/.config/culler/github-token"
chmod 600 "$HOME/.config/culler/github-token"
CULLER_FIXTURE_PRIVATE=1 sh "$TMP/outside/install.sh" --check-updates >"$TMP/private.out" 2>&1 || fail 'private download failed'
if grep -F fixture_private_token "$TMP/curl-args" "$TMP/private.out" >/dev/null; then fail 'credential entered arguments or terminal output'; fi
chmod 644 "$HOME/.config/culler/github-token"
if sh "$TMP/outside/install.sh" --check-updates >"$TMP/permissions.out" 2>&1; then fail 'public credential file was accepted'; fi
rm "$HOME/.config/culler/github-token"
if CULLER_FIXTURE_HTTP=403 sh "$TMP/outside/install.sh" >"$TMP/access.out" 2>&1; then fail 'refused access succeeded'; fi
contains "$TMP/access.out" 'Renew the repository and package credentials' || fail 'access error omitted repair action'
if CULLER_FIXTURE_HTTP=404 sh "$TMP/outside/install.sh" >"$TMP/no-release.out" 2>&1; then fail 'absent release succeeded'; fi
contains "$TMP/no-release.out" 'No complete release is available' || fail 'release absence was unclear'
if CULLER_FIXTURE_HTTP=404 CULLER_FIXTURE_REPO_STATUS=404 sh "$TMP/outside/install.sh" >"$TMP/private-absence.out" 2>&1; then fail 'private absence succeeded'; fi
contains "$TMP/private-absence.out" 'repository access is absent' || fail 'private repository absence was unclear'
# The same saved launcher must select a later published installer.
for file in installer.sh release.json github-release.json; do cp "$TMP/fixtures/$file" "$TMP/$file.original"; done
cat >"$TMP/fixtures/installer.sh" <<'NEWINSTALLER'
#!/bin/sh
printf 'new installer\n' >"$CULLER_FIXTURE_NEW"
NEWINSTALLER
python3 - "$TMP/fixtures" <<'PYFIXTURE'
import hashlib,json,sys
from pathlib import Path
root=Path(sys.argv[1]);record=json.loads((root/'release.json').read_text());installer=root/'installer.sh'
record.update(version='0.2.0',commit='b'*40,release_id='b'*40,tag='v0.2.0',sequence=8)
record['installer']['sha256']=hashlib.sha256(installer.read_bytes()).hexdigest();record['installer']['size']=installer.stat().st_size
(root/'release.json').write_text(json.dumps(record))
github=json.loads((root/'github-release.json').read_text());github['tag_name']=record['tag']
for asset in github['assets']:
    if asset['name']=='installer.sh': asset['size']=record['installer']['size']
(root/'github-release.json').write_text(json.dumps(github))
PYFIXTURE
CULLER_FIXTURE_NEW="$TMP/new-installer" sh "$TMP/outside/install.sh" --check-updates >"$TMP/new.out" 2>&1 || fail 'saved launcher rejected the newer release'
[ -f "$TMP/new-installer" ] || fail 'saved launcher reused its old installer'
for file in installer.sh release.json github-release.json; do cp "$TMP/$file.original" "$TMP/fixtures/$file"; done
for field in tag version; do
  python3 - "$TMP/fixtures" "$field" <<'PYFIXTURE'
import json,sys
from pathlib import Path
root=Path(sys.argv[1]);record=json.loads((root/'release.json').read_text())
if sys.argv[2]=='tag': record['tag']='culler-'+record['commit']
else: del record['version']
(root/'release.json').write_text(json.dumps(record))
PYFIXTURE
  rm -f "$TMP/args"
  if sh "$TMP/outside/install.sh" >"$TMP/identity-$field.out" 2>&1; then fail "a release record with a wrong $field succeeded"; fi
  [ ! -e "$TMP/args" ] || fail "a release record with a wrong $field executed code"
  grep -Eq 'release identities differ|release record is incomplete' "$TMP/identity-$field.out" || fail "a release record with a wrong $field gave another failure"
  cp "$TMP/release.json.original" "$TMP/fixtures/release.json"
done
cp "$TMP/fixtures/installer.sh" "$TMP/valid-installer"
printf '\n# corruption\n' >>"$TMP/fixtures/installer.sh"
rm -f "$TMP/args"
if sh "$TMP/outside/install.sh" >"$TMP/hash.out" 2>&1; then fail 'corrupt installer succeeded'; fi
[ ! -e "$TMP/args" ] || fail 'corrupt installer executed code'
cp "$TMP/valid-installer" "$TMP/fixtures/installer.sh"
printf 'corrupt archive\n' >"$TMP/fixtures/package.tar.gz"
if sh "$TMP/outside/install.sh" >"$TMP/package-hash.out" 2>&1; then fail 'corrupt package succeeded'; fi
[ ! -e "$TMP/args" ] || fail 'corrupt package executed code'
# A trusted checksum does not permit archive paths or symbolic links.
printf 'extra\n' >"$TMP/package/bin/extra"
tar -czf "$TMP/fixtures/package.tar.gz" -C "$TMP/package" bin/mc bin/install.sh bin/agentsview bin/extra host/file runner/file
python3 - "$TMP/fixtures" <<'PYFIXTURE'
import hashlib,json,sys
from pathlib import Path
root=Path(sys.argv[1]);record=json.loads((root/'release.json').read_text());archive=root/'package.tar.gz'
record['package']['sha256']=hashlib.sha256(archive.read_bytes()).hexdigest();record['package']['size']=archive.stat().st_size
(root/'release.json').write_text(json.dumps(record))
PYFIXTURE
if sh "$TMP/outside/install.sh" >"$TMP/archive-extra.out" 2>&1; then fail 'an unexpected package executable succeeded'; fi
contains "$TMP/archive-extra.out" 'The package contains an invalid path.' || fail 'the unexpected executable refusal was absent'
[ ! -e "$TMP/args" ] || fail 'an unexpected package executable executed code'
rm "$TMP/package/runner/file"
ln -s "$TMP/outside" "$TMP/package/runner/file"
tar -czf "$TMP/fixtures/package.tar.gz" -C "$TMP/package" bin/mc bin/install.sh bin/agentsview host/file runner/file
python3 - "$TMP/fixtures" <<'PYFIXTURE'
import hashlib,json,sys
from pathlib import Path
root=Path(sys.argv[1]);record=json.loads((root/'release.json').read_text());archive=root/'package.tar.gz'
record['package']['sha256']=hashlib.sha256(archive.read_bytes()).hexdigest();record['package']['size']=archive.stat().st_size
(root/'release.json').write_text(json.dumps(record))
PYFIXTURE
if sh "$TMP/outside/install.sh" >"$TMP/archive-link.out" 2>&1; then fail 'package symbolic link succeeded'; fi
contains "$TMP/archive-link.out" 'file link or special file' || fail 'archive link refusal was absent'
[ ! -e "$TMP/args" ] || fail 'archive symbolic link executed code'
if CULLER_FIXTURE_OS=Linux sh "$TMP/outside/install.sh" >"$TMP/platform.out" 2>&1; then fail 'unsupported platform succeeded'; fi
# An interrupted update uses the retained candidate without release discovery.
recovery_home=$TMP/recovery
recovery_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
recovery_root=$recovery_home/updates/$recovery_id/candidate
mkdir -p "$recovery_root/bin"
recovery_home=$(CDPATH= cd -- "$recovery_home" && pwd -P)
recovery_root=$recovery_home/updates/$recovery_id/candidate
cp "$TMP/package/bin/mc" "$recovery_root/bin/mc"
python3 - "$recovery_home" "$recovery_root" <<'PYRECOVERY'
import hashlib,json,sys
from pathlib import Path
home,root=map(Path,sys.argv[1:])
record={'version':1,'id':'a'*32,'phase':'replace','candidate_root':str(root),'candidate_sha256':hashlib.sha256((root/'bin/mc').read_bytes()).hexdigest(),'bin_dir':str(home/'commands')}
path=home/'updates/transaction.json'
path.write_text(json.dumps(record));path.chmod(0o600)
PYRECOVERY
rm -f "$TMP/curl-args" "$TMP/args"
CULLER_FIXTURE_HTTP=503 sh "$TMP/outside/install.sh" --mc-home "$recovery_home" --restore >"$TMP/recovery.out" 2>&1 || { cat "$TMP/recovery.out" >&2; fail 'offline retained recovery failed'; }
[ ! -e "$TMP/curl-args" ] || fail 'recovery queried a newer release'
grep -Fx -- --restore "$TMP/args" >/dev/null || fail 'recovery lost the restore choice'
[ "$(sed -n '3p' "$TMP/env")" = "$recovery_root" ] || fail 'recovery selected another package'
printf '# changed\n' >>"$recovery_root/bin/mc"
rm "$TMP/args"
if sh "$TMP/outside/install.sh" --mc-home "$recovery_home" >"$TMP/recovery-corrupt.out" 2>&1; then fail 'corrupt recovery executable succeeded'; fi
[ ! -e "$TMP/args" ] && [ ! -e "$TMP/curl-args" ] || fail 'corrupt recovery invoked code or network'
contains "$TMP/recovery-corrupt.out" 'failed verification' || fail 'corrupt recovery refusal was unclear'
if sh "$TMP/outside/install.sh" --mc-home "$TMP/absent-recovery" --restore >"$TMP/recovery-absent.out" 2>&1; then fail 'absent recovery succeeded'; fi
[ ! -e "$TMP/curl-args" ] || fail 'absent recovery queried a newer release'
# Preserve the source and development contracts through their separate entry point.
# That test receives the tools of the caller, not the stubs of this test.
env PATH="$CALLER_PATH" HOME="$CALLER_HOME" sh "$ROOT/scripts/install-source_test.sh"
printf 'Published installer contracts: ok\n'
