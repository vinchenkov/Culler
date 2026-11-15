#!/bin/sh
cd "$(dirname "$0")" || exit 1
exec mise exec -- bun test
