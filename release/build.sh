#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
output=${1:?The output directory is required.}
case "$output" in /*) ;; *) exit 2 ;; esac
commit=${CULLER_COMMIT:?The release commit is required.}
[ "$(git -C "$ROOT" rev-parse HEAD)" = "$commit" ]
test -z "$(git -C "$ROOT" status --porcelain --untracked-files=all)"
# One release leg builds one platform's package. The default is the historical
# darwin leg; the Linux leg is selected explicitly, never by accident.
platform=${CULLER_RELEASE_PLATFORM:-darwin-arm64}
case "$platform" in
  darwin-arm64)
    host_goos=darwin; host_goarch=arm64; image_arch=arm64; host_bun_key=darwin-arm64
    agentsview_pin_file=agentsview.json; opencode_platform=linux-arm64
    package_meta=package.json; opencode_meta=opencode.json ;;
  linux-amd64)
    host_goos=linux; host_goarch=amd64; image_arch=amd64; host_bun_key=linux-x64
    agentsview_pin_file=agentsview.linux-amd64.json; opencode_platform=linux-x64
    package_meta=package.linux-amd64.json; opencode_meta=opencode.linux-amd64.json ;;
  *) echo "release/build.sh: unsupported CULLER_RELEASE_PLATFORM $platform" >&2; exit 2 ;;
esac
package_asset=culler-$platform.tar.gz
opencode_asset=culler-opencode-$opencode_platform.tar.gz
mkdir -p "$output"
output=$(CDPATH= cd -- "$output" && pwd -P)
case "$output/" in "$ROOT/"*) echo 'The release output must stay outside the checkout.' >&2; exit 2 ;; esac
mkdir -p "$output/assets" "$output/image/runner/image" "$output/image/runner/openwiki" "$output/image/runner/agent-runner"
version=$(python3 "$ROOT/release/version.py" show)
python3 "$ROOT/release/version.py" notes >"$output/release-notes.md"
printf '%s\n' "$version" >"$output/version"
# The AgentsView release archive stays outside the output, so the release inputs hold only its packaged executable.
agentsview_scratch=$(mktemp -d "${TMPDIR:-/tmp}/culler-agentsview.XXXXXX")
hostbun_scratch=$(mktemp -d "${TMPDIR:-/tmp}/culler-hostbun.XXXXXX")
opencode_scratch=$(mktemp -d "${TMPDIR:-/tmp}/culler-opencode-check.XXXXXX")
trap 'rm -rf "$agentsview_scratch" "$hostbun_scratch" "$opencode_scratch"' EXIT
"$ROOT/release/opencode/build.sh" "$output/assets/$opencode_asset" "$opencode_platform" >"$output/opencode-build.log"
python3 "$ROOT/release/opencode/check_archive.py" "$output/assets/$opencode_asset" --platform "$opencode_platform" >"$output/$opencode_meta"
"$ROOT/release/opencode/check.sh" "$opencode_scratch"
agentsview_pin() {
  python3 -c 'import json, sys
value = json.load(open(sys.argv[1]))
for key in sys.argv[2].split("."):
    value = value[key]
print(value)' "$ROOT/mc/deployment/$agentsview_pin_file" "$1"
}
agentsview_archive=$agentsview_scratch/agentsview.tar.gz
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
  --connect-timeout 15 --max-time 900 --output "$agentsview_archive" "$(agentsview_pin archive.url)"
agentsview_expected=$(agentsview_pin archive.sha256)
agentsview_actual=$(shasum -a 256 "$agentsview_archive" | awk '{print $1}')
if [ "$agentsview_actual" != "$agentsview_expected" ] || \
  [ "$(wc -c <"$agentsview_archive" | tr -d ' ')" != "$(agentsview_pin archive.size)" ]; then
  echo "The AgentsView release archive differs from its pin: expected SHA-256 $agentsview_expected, found SHA-256 $agentsview_actual." >&2
  exit 1
fi
# The host Bun archive stays outside the output. The installer downloads it from the pinned address.
hostbun_pin() {
  python3 -c 'import json, sys
value = json.load(open(sys.argv[1]))
for key in sys.argv[2].split("."):
    value = value[key]
print(value)' "$ROOT/mc/deployment/hostbun.json" "$1"
}
hostbun_archive=$hostbun_scratch/$(hostbun_pin "archives.$host_bun_key.name")
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
  --connect-timeout 15 --max-time 900 --output "$hostbun_archive" "$(hostbun_pin "archives.$host_bun_key.url")"
hostbun_expected=$(hostbun_pin "archives.$host_bun_key.sha256")
hostbun_actual=$(shasum -a 256 "$hostbun_archive" | awk '{print $1}')
if [ "$hostbun_actual" != "$hostbun_expected" ] || \
  [ "$(wc -c <"$hostbun_archive" | tr -d ' ')" != "$(hostbun_pin "archives.$host_bun_key.size")" ]; then
  echo "The host Bun archive differs from its pin: expected SHA-256 $hostbun_expected, found SHA-256 $hostbun_actual." >&2
  exit 1
fi
export GOFLAGS= GOENV=off GOTOOLCHAIN=local CGO_ENABLED=0
cd "$ROOT/mc"
GOOS=$host_goos GOARCH=$host_goarch mise exec -- go build -trimpath -mod=readonly \
  -ldflags "-w -X main.releaseBuildID=$commit -X main.releaseVersion=$version" -o "$output/mc-$platform" ./cmd/mc
GOOS=linux GOARCH=$image_arch mise exec -- go build -trimpath -mod=readonly \
  -ldflags "-w -X main.releaseBuildID=$commit -X main.releaseVersion=$version" -o "$output/image/runner/image/mc-prod-bin" ./cmd/mc
mise exec -- go run ./cmd/culler-package --verify-binary "$output/image/runner/image/mc-prod-bin" \
  --platform "linux-$host_goarch" --commit "$commit"
mise exec -- go run ./cmd/culler-package --root "$ROOT" --binary "$output/mc-$platform" \
  --agentsview-archive "$agentsview_archive" \
  --commit "$commit" --platform "$platform" --output "$output/assets/$package_asset" >"$output/$package_meta"
cd "$ROOT"
GOOS=linux GOARCH=$image_arch mise exec -- go build -trimpath -ldflags "-w" -o "$output/image/runner/image/mc-dispatch" ./runner/image/mc_dispatch.go
python3 runner/image/stage-runtime.py "$output/image/runner/runtime-source"
mkdir -p "$output/image/runner/image/project-tools" "$output/image/runner/image/runtime-tools"
for name in runner/image/Dockerfile runner/image/runtime.Dockerfile runner/image/mc-land \
  runner/image/project-manifest.sh runner/image/runtime-manifest.sh runner/image/runtime-check.sh runner/image/runtime-playwright \
  runner/image/project-tools/package.json runner/image/project-tools/package-lock.json \
  runner/image/runtime-tools/package.json runner/image/runtime-tools/package-lock.json \
  runner/openwiki/package.json runner/openwiki/package-lock.json \
  runner/openwiki/patch-upstream.mjs runner/openwiki/project-documents.mjs \
  runner/openwiki/recovery-patches.mjs runner/openwiki/recovery.mjs \
  runner/agent-runner/package.json runner/agent-runner/bun.lock \
  runner/agent-runner/package.linux-x64.json runner/agent-runner/bun.linux-x64.lock; do
  [ -f "$name" ] && [ ! -L "$name" ]
  cp "$name" "$output/image/$name"
done
cp install.sh "$output/assets/install.sh"
cp release/installer.sh "$output/assets/installer.sh"
git rev-list --count --first-parent "$commit" >"$output/sequence"
[ "$(git rev-parse HEAD)" = "$commit" ]
test -z "$(git status --porcelain --untracked-files=all)"
