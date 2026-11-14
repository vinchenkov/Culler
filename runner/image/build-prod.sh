#!/usr/bin/env bash
# Build the production image with the real harnesses and the pinned browser.
# The build requires native arm64 execution.
set -euo pipefail
cd "$(dirname "$0")"

MC_RELEASE_BUILD_ID="${MC_RELEASE_BUILD_ID:-$(git -C ../.. rev-parse --verify 'HEAD^{commit}')}"
if [[ ! "$MC_RELEASE_BUILD_ID" =~ ^[0-9a-f]{40,64}$ ]]; then
  echo "build-prod.sh: MC_RELEASE_BUILD_ID must be a 40..64 lowercase-hex commit" >&2
  exit 2
fi
MC_RELEASE_VERSION="$(cat ../../VERSION)"
if [[ ! "$MC_RELEASE_VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "build-prod.sh: the VERSION file must hold one version, for example 0.1.0" >&2
  exit 2
fi

# A loaded deployment uses the shared image tag. Its image identity must stay unchanged.
units=
if command -v launchctl >/dev/null 2>&1; then
  if units=$(launchctl list 2>&1); then
    :
  else
    status=$?
    width=${COLUMNS:-80}
    case "$width" in ''|*[!0-9]*|????*) width=80 ;; esac
    [ "$width" -ge 40 ] || width=40
    [ "$width" -le 80 ] || width=80
    {
      printf '%s\n\n' "build-prod.sh: Stopped. The macOS service check failed." \
        "This attempt changed no build files or container images." \
        "Details: launchctl list exited with $status. $units" \
        "Open Terminal in the repository and run ./redeploy.sh." \
        "If the error continues, give this output to your coding harness."
    } | fold -s -w "$width" | sed 's/ *$//' >&2
    exit 3
  fi
fi
if printf '%s\n' "$units" | grep -q 'com\.culler\.'; then
  echo "build-prod.sh: a supervised Culler deployment is loaded." >&2
  echo "  Rebuilding mc-prod would re-stamp the image its helper is recreated from," >&2
  echo "  and every tick would then fail closed with a release identity mismatch that" >&2
  echo "  appears only in logs/resident.stderr.log." >&2
  echo "  Boot the units out first:" >&2
  printf '%s\n' "$units" | awk '/com\.culler\./ {print "    launchctl bootout gui/$(id -u)/" $3}' >&2
  exit 3
fi

echo "build-prod.sh: compiling linux/arm64 mc WITHOUT test_fake_routing..." >&2
printf 'setup-progress: compile-linux\n' >&2
(cd ../../mc && GOFLAGS= GOENV=off GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=arm64 mise exec -- go build -trimpath -mod=readonly \
  -ldflags "-w -X main.releaseBuildID=$MC_RELEASE_BUILD_ID -X main.releaseVersion=$MC_RELEASE_VERSION" \
  -o ../runner/image/mc-prod-bin ./cmd/mc)
printf 'setup-progress: compile-dispatcher\n' >&2
(cd ../.. && GOFLAGS= GOENV=off GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=arm64 mise exec -- go build -trimpath -ldflags "-w" -o runner/image/mc-dispatch ./runner/image/mc_dispatch.go)

echo "build-prod.sh: docker build -t mc-prod ..." >&2
printf 'setup-progress: build-container\n' >&2
python3 stage-runtime.py ../runtime-source
DOCKER_BUILDKIT=1 docker build --target project --progress=plain -f Dockerfile \
  --label "org.opencontainers.image.revision=$MC_RELEASE_BUILD_ID" \
  --build-arg BROWSER_DEPS=1 -t mc-project ../..
DOCKER_BUILDKIT=1 docker build --target product --progress=plain -f runtime.Dockerfile \
  --label "org.opencontainers.image.revision=$MC_RELEASE_BUILD_ID" \
  --build-arg MC_BIN=mc-prod-bin \
  --build-arg PROJECT_BASE=mc-project \
  --build-arg "CULLER_RELEASE_ID=$MC_RELEASE_BUILD_ID" \
  --build-arg RUNTIME_DEPS=1 \
  -t mc-prod ../..

printf 'setup-progress: check-browser\n' >&2
"$PWD/browser-smoke.sh" mc-prod
"$PWD/browser-smoke.sh" mc-prod --runtime
