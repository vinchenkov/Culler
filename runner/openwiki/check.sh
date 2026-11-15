#!/bin/sh
set -eu
cd "$(dirname "$0")"
exec mise exec -- bun test
