#!/bin/sh
# Dashboard fast lane: unit tests only — Docker-free, browser-free, no
# install step (the unit lane imports nothing from node_modules). The
# Playwright browser suite is dashboard/smoke.sh, which the dashboard-browser
# job of the release workflow requires.
set -eu
cd "$(dirname "$0")"
exec mise exec -- bun test src/
