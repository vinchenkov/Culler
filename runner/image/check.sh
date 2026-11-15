#!/bin/sh
# Docker-free fast tests for the baked landing boundary.
set -eu
cd "$(dirname "$0")"
python3 stage-runtime.test.py
sh build.test.sh
sh build-prod.test.sh
sh browser-smoke.test.sh
sh toolchain.test.sh
sh toolchain-smoke.test.sh
sh ../../release/check.sh
sh ../openwiki/check.sh
exec mise exec -- bun test mc-land.test.ts
