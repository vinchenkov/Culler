#!/bin/sh
# Resident fast check. Docker-free by construction: unit tests use fake
# timer/exec/fs dependencies, while split-brain acceptance uses temp host
# state plus the real test-tagged mc binary through the same injected seams.
set -eu
cd "$(cd "$(dirname "$0")" && pwd)"
# Four test files build mc in a hook with a time limit.
# These builds fill the Go build cache before the first hook starts.
tags=test_fake_routing
if [ "$(uname -s)" = Linux ]; then
  tags=test_fake_routing,test_scratch_spine
fi
(cd ../mc/cmd/mc && mise exec -- go build -tags "$tags" -o /dev/null .)
(cd ../mc/cmd/mc && CGO_ENABLED=0 mise exec -- go build -tags "$tags" -o /dev/null .)
exec mise exec -- bun test
