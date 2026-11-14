#!/bin/sh
# fake-harness check — Docker-free, token-free (fast suite).
set -eu
cd "$(dirname "$0")"
exec mise exec -- bun test
