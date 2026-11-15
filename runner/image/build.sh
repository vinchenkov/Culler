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

echo "build.sh: compiling linux/arm64 mc for ${image}..." >&2
(cd ../../mc && GOFLAGS= GOENV=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 mise exec -- go build ${tags[@]+"${tags[@]}"} -ldflags "-w" -o "../runner/image/${mc_bin}" ./cmd/mc)
(cd ../.. && GOFLAGS= GOENV=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 mise exec -- go build -ldflags "-w" -o runner/image/mc-dispatch ./runner/image/mc_dispatch.go)

echo "build.sh: docker build -t ${image} ..." >&2
if [ "${1:-}" = "--real" ]; then
  python3 stage-runtime.py ../runtime-source
else
  python3 stage-runtime.py ../runtime-source --include-fake
fi
DOCKER_BUILDKIT=1 docker build --target project -f Dockerfile --build-arg BROWSER_DEPS=0 -t "${image}-project" ../..
DOCKER_BUILDKIT=1 docker build --target product -f runtime.Dockerfile --build-arg BROWSER_DEPS=0 --build-arg RUNTIME_DEPS=1 --build-arg "PROJECT_BASE=${image}-project" --build-arg "MC_BIN=${mc_bin}" -t "${image}" ../..
