#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
exec mise exec -- bun resident/src/dependency-reuse.ts "$@"
