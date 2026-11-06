#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd -P)
output=${1:?The archive path is required.}
stage=$(mktemp -d "${TMPDIR:-/tmp}/culler-opencode.XXXXXX")
trap 'rm -rf "$stage"' EXIT
# npm requires a physical path when the temporary directory uses a symbolic link.
stage=$(CDPATH= cd -- "$stage" && pwd -P)
cp "$root/runner/opencode/package.json" "$root/runner/opencode/package-lock.json" "$stage/"
# The explicit Linux dependency permits an archive build on the Mac.
npm ci --prefix "$stage" --ignore-scripts --omit=optional --force
cp -R "$root/runner/opencode/control" "$stage/control"
python3 "$root/release/opencode/check_archive.py" --folder "$stage"
tar -czf "$output" -C "$stage" .
python3 "$root/release/opencode/check_archive.py" "$output"
