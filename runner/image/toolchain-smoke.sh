#!/bin/sh
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$HERE/../.." && pwd)
image=${1:?The image identifier is required.}
case "$image" in -*) exit 2 ;; esac
version=$(sed -n 's/^toolchain go//p' "$ROOT/mc/go.mod")
[ -n "$version" ]

# The agent account builds mc from a copy of the checkout with the image toolchain.
docker run --rm --user 10002:10002 \
  --cap-drop ALL --security-opt no-new-privileges \
  --env GOTOOLCHAIN=local --env GOFLAGS=-mod=readonly --env EXPECTED_GO_VERSION="$version" \
  --env GOCACHE=/tmp/go-build-cache --env GOMODCACHE=/tmp/go-mod-cache \
  --tmpfs /workspace:rw,exec,mode=1777 \
  --mount "type=bind,source=$ROOT,target=/source,readonly" \
  "$image" /bin/sh -ec '
    test "$(id -u)" = 10002
    mkdir -p /workspace/source
    cp -R /source/mc /workspace/source/mc
    cp /source/.mise.toml /workspace/source/
    cd /workspace/source/mc
    mise trust /workspace/source/.mise.toml
    test "$(mise exec -- go env GOVERSION)" = "go$EXPECTED_GO_VERSION"
    test "$(mise exec -- go env GOTOOLCHAIN)" = local
    mise exec -- go version
    sha256sum go.mod go.sum >/tmp/mc-module-checksums
    mise exec -- go build -o /tmp/mc ./cmd/mc
    sha256sum -c /tmp/mc-module-checksums
    test -x /tmp/mc
  '
