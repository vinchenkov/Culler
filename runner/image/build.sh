#!/usr/bin/env bash
# Build the private runtime and Project image for isolated container tests.
set -euo pipefail
cd "$(dirname "$0")"

tags=(-tags test_fake_routing)
image=mc-fake-e2e
mc_bin=mc-fake-e2e-bin
if [ "${1:-}" = "--real" ]; then
  tags=()
  image=mc-real-e2e
  mc_bin=mc-real-e2e-bin
fi

image=${MC_TEST_IMAGE:-$image}
[[ "$image" =~ ^[a-z0-9][a-z0-9_.-]*(:[a-zA-Z0-9][a-zA-Z0-9_.-]*)?$ ]] || exit 2
[[ "${image%%:*}" != mc-prod && "${image%%:*}" != mc-project ]] || exit 2

# Native only, like build-prod.sh: the image is built for this machine.
case "${MC_TARGET_ARCH:-}" in
  '') case "$(uname -m)" in
        aarch64|arm64) TARGET_ARCH=arm64 ;;
        x86_64) TARGET_ARCH=amd64 ;;
        *) echo "build.sh: unsupported host architecture $(uname -m)" >&2; exit 2 ;;
      esac ;;
  arm64|amd64) TARGET_ARCH=$MC_TARGET_ARCH ;;
  *) echo "build.sh: MC_TARGET_ARCH must be arm64 or amd64, or unset." >&2; exit 2 ;;
esac

echo "build.sh: compiling linux/${TARGET_ARCH} mc for ${image}..." >&2
(cd ../../mc && GOFLAGS= GOENV=off CGO_ENABLED=0 GOOS=linux GOARCH="$TARGET_ARCH" mise exec -- go build ${tags[@]+"${tags[@]}"} -ldflags "-w" -o "../runner/image/${mc_bin}" ./cmd/mc)
(cd ../.. && GOFLAGS= GOENV=off CGO_ENABLED=0 GOOS=linux GOARCH="$TARGET_ARCH" mise exec -- go build -ldflags "-w" -o runner/image/mc-dispatch ./runner/image/mc_dispatch.go)

echo "build.sh: docker build -t ${image} ..." >&2
if [ "${1:-}" = "--real" ]; then
  python3 stage-runtime.py ../runtime-source
else
  python3 stage-runtime.py ../runtime-source --include-fake
fi
DOCKER_BUILDKIT=1 docker build --platform "linux/${TARGET_ARCH}" --target project -f Dockerfile --build-arg BROWSER_DEPS=0 -t "${image}-project" ../..
DOCKER_BUILDKIT=1 docker build --platform "linux/${TARGET_ARCH}" --target product -f runtime.Dockerfile --build-arg BROWSER_DEPS=0 --build-arg RUNTIME_DEPS=1 --build-arg "PROJECT_BASE=${image}-project" --build-arg "MC_BIN=${mc_bin}" -t "${image}" ../..
