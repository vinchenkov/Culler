#!/bin/sh
# The fast-suite entrypoint for the Go side:
# substrate + dispatch + verb/CLI tests against temp spine files, no Docker.
# The Docker e2e is compiled only under -tags docker_e2e and never runs here.
# A clean gofmt and a clean vet are part of the definition of done.
set -eu
cd "$(dirname "$0")"
fmt="$(mise exec -- gofmt -l .)"
if [ -n "$fmt" ]; then
    echo "gofmt needed on:" >&2
    echo "$fmt" >&2
    exit 1
fi
mise exec -- go vet ./...

# The tagged suites do not RUN here (nightly is slow; docker_e2e needs Docker),
# but they must still COMPILE on every commit. Without this, a signature change
# rots a tagged suite invisibly while the fast lane stays green — which is
# exactly how a changed domain signature can break a tagged suite and stay
# unnoticed until a review finds it.
# vet is cheap and needs neither Docker nor a nightly budget.
for tag in nightly docker_e2e docker_boundary update_docker test_fake_routing test_scratch_spine; do
    mise exec -- go vet -tags "$tag" ./...
done

# Same argument, one axis over: the fast lane runs on darwin, but the mc that
# opens the spine for real runs on linux. The lock-domain guard
# (substrate/lockdomain_linux.go) therefore has its ONLY production
# implementation on a platform this suite never compiles by default — the
# invisible-rot shape the tagged vets above exist to prevent.
mise exec -- env GOOS=linux GOARCH=arm64 go vet ./...
mise exec -- env GOOS=linux GOARCH=arm64 go vet -tags test_scratch_spine ./...

# These tests read files outside this Go module. Run them without the test cache.
./test.sh -count=1 ./deployment/ ./testenv/

# install.sh is the production front door. Its Level-0 failure boundary is a
# shell contract, so keep the Docker-free probe in this fast lane rather than
# hiding it behind the phase-completion acceptance walk.
sh ../install_test.sh
# redeploy.sh is the release-move front door. The same Docker-free probe checks its sequence.
sh ../redeploy_test.sh
sh ../scripts/source-update-guard_test.sh
sh ../uninstall_test.sh

# The default limit of go test is ten minutes for one package. The verbs package can need that much time.
./test.sh -timeout 20m ./...

# A Linux test build uses test_scratch_spine. These tests prove the production filesystem guard without it.
if [ "$(uname -s)" = Linux ]; then
    ./test.sh -count=1 -tags= ./substrate/ -run 'Test(LockDomain|OpenRefuses|OpenReadOnlyRefuses|GuardResolves|LinuxRequires)'
fi
