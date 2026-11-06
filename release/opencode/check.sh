#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd -P)
scratch=${1:?The scratch directory is required.}
mkdir -p "$scratch"
# npm requires a physical path when the scratch directory uses a symbolic link.
scratch=$(CDPATH= cd -- "$scratch" && pwd -P)
platform=$(uname -s)
case "$platform:$(uname -m)" in
  Darwin:arm64) package=cli-darwin-arm64 ;;
  Linux:aarch64|Linux:arm64) package=cli-linux-arm64 ;;
  *) echo 'The catalog check requires an arm64 Mac or Linux host.' >&2; exit 1 ;;
esac
version=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["opencode"])' "$root/release/opencode/pins.json")
cp "$root/runner/opencode/package.json" "$root/runner/opencode/package-lock.json" "$scratch/"
npm ci --prefix "$scratch" --ignore-scripts --omit=optional --force >/dev/null
if [ "$package" != cli-linux-arm64 ]; then
  npm install --prefix "$scratch" --ignore-scripts --no-save --package-lock=false --omit=optional --force "@opencode/$package@$version" >/dev/null
fi
python3 "$root/release/opencode/generate.py" --binary "$scratch/node_modules/@opencode/$package/bin/opencode" \
  --plugin "$scratch/node_modules/opencode-websearch" --output "$scratch/catalog.json"
cmp "$root/mc/opencodecatalog/catalog.json" "$scratch/catalog.json"
mkdir -p "$scratch/control"
cp -R "$root/runner/opencode/control/." "$scratch/control/"
python3 "$root/release/opencode/plugin_check.py" "$scratch/node_modules/@opencode/$package/bin/opencode" "$scratch"
