#!/bin/sh
# Fast-suite entrypoint (repo convention, cf. runner/agent-runner/check.sh):
# Docker-free, token-free bun tests for the homie runner's helpers + loop.
cd "$(dirname "$0")" || exit 1
exec mise exec -- bun test
