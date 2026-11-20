#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd -P)
output=${1:?The archive path is required.}
# The platform is the machine this build runs on, or the explicit second
# argument. A release leg that builds another platform's archive passes the
# name; the archive and the manifest pair must agree, not the host's mood.
platform=${2:-}
if [ -z "$platform" ]; then
  case "$(uname -m)" in
    aarch64|arm64) platform=linux-arm64 ;;
    x86_64) platform=linux-x64 ;;
    *) printf 'The OpenCode archive cannot be built for machine %s.\n' "$(uname -m)" >&2; exit 1 ;;
  esac
fi
case "$platform" in
  linux-arm64) manifest=package.json lock=package-lock.json ;;
  linux-x64) manifest=package.linux-x64.json lock=package-lock.linux-x64.json ;;
  *) printf 'The OpenCode platform %s is unsupported.\n' "$platform" >&2; exit 1 ;;
esac
stage=$(mktemp -d "${TMPDIR:-/tmp}/culler-opencode.XXXXXX")
trap 'rm -rf "$stage"' EXIT
# npm requires a physical path when the temporary directory uses a symbolic link.
stage=$(CDPATH= cd -- "$stage" && pwd -P)
cp "$root/runner/opencode/$manifest" "$stage/package.json"
cp "$root/runner/opencode/$lock" "$stage/package-lock.json"
# The explicit Linux dependency permits an archive build on the Mac.
npm ci --prefix "$stage" --ignore-scripts --omit=optional --force
cp -R "$root/runner/opencode/control" "$stage/control"
python3 "$root/release/opencode/check_archive.py" --folder "$stage" --platform "$platform"
tar -czf "$output" -C "$stage" .
python3 "$root/release/opencode/check_archive.py" "$output" --platform "$platform"
