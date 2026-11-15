#!/bin/sh
# The dashboard smoke tests use a scratch mc, a scratch database, and a real browser.
# MC_SMOKE_REPORT names the JUnit report path. The default report is scratch.
set -eu
cd "$(dirname "$0")"
case "$(uname -s)" in
  Darwin) smoke_base=${MC_SMOKE_ROOT:-/tmp} ;;
  *) smoke_base=${MC_SMOKE_ROOT:-${TMPDIR:-/tmp}} ;;
esac
case "$smoke_base" in /*) ;; *) exit 2 ;; esac
mkdir -p "$smoke_base"
smoke_scratch=$(mktemp -d "$smoke_base/culler-smoke.XXXXXX")
trap 'rm -rf "$smoke_scratch"' EXIT HUP INT TERM
export MC_SMOKE_ROOT="$smoke_scratch" TMPDIR="$smoke_scratch"
export MC_SMOKE_EVIDENCE_DIR=${MC_SMOKE_EVIDENCE_DIR:-"$smoke_base/culler-smoke-evidence"}

# The default Project image supplies a library and a browser of the same version.
image_playwright=/opt/project-tools/node_modules/playwright/index.mjs
if [ -z "${MC_SMOKE_PLAYWRIGHT:-}" ] && [ -r "$image_playwright" ]; then
  export MC_SMOKE_PLAYWRIGHT="$image_playwright"
fi
if [ -z "${MC_SMOKE_PLAYWRIGHT:-}" ]; then
  mise exec -- bun install --frozen-lockfile
  mise exec -- bun x playwright install chromium
fi
if [ -z "${MC_SMOKE_BIN:-}" ]; then
  MC_SMOKE_BIN="$smoke_scratch/mc"
  # A Linux build with test_scratch_spine opens the scratch database on any filesystem.
  (cd ../mc && GOTOOLCHAIN=local mise exec -- go build -mod=readonly -tags test_fake_routing,test_scratch_spine -o "$MC_SMOKE_BIN" ./cmd/mc)
fi
case "$MC_SMOKE_BIN" in /*) ;; *) exit 2 ;; esac
[ -x "$MC_SMOKE_BIN" ]
export MC_SMOKE_BIN
# The gate requires at least one executed test and no failure or skip.
# The default run also requires one executed test file for each smoke file.
suite=
if [ "$#" -eq 0 ]; then
  set -- smoke/
  suite="--suite smoke"
fi
report=${MC_SMOKE_REPORT:-"$smoke_scratch/report.xml"}
case "$report" in /*) ;; *) exit 2 ;; esac
mkdir -p "$(dirname "$report")"
rm -f "$report"
status=0
mise exec -- bun test --reporter=junit --reporter-outfile="$report" "$@" || status=$?
# shellcheck disable=SC2086
mise exec -- bun smoke-gate.ts "$report" $suite || status=1
exit "$status"
