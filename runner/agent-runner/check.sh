#!/bin/sh
# Fast-suite entrypoint (repo convention, cf. mc/substrate/check.sh):
# Docker-free, token-free bun tests for the agent runner's helpers.
cd "$(dirname "$0")" || exit 1
exec mise exec -- bun test
