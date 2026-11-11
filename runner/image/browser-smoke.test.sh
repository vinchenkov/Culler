#!/bin/sh
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/mc-browser-smoke-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

fail() {
  printf 'browser-smoke.test.sh: %s\n' "$*" >&2
  exit 1
}

mkdir -p "$TMP/bin"
if "$HERE/browser-smoke.sh" >/dev/null 2>&1; then
  fail "missing image was accepted"
fi
if "$HERE/browser-smoke.sh" --runtime >/dev/null 2>&1; then
  fail "the runtime option without an image was accepted"
fi
if "$HERE/browser-smoke.sh" --privileged >/dev/null 2>&1; then
  fail "option-shaped image was accepted"
fi
if "$HERE/browser-smoke.sh" mc-prod mc-project >/dev/null 2>&1; then
  fail "two images were accepted"
fi

printf '#!/bin/sh\nprintf "%%s\\n" "$@" >"${MC_TEST_DOCKER_LOG:?}"\n' >"$TMP/bin/docker"
chmod 755 "$TMP/bin/docker"

# Both modes open a page, read the page content, and take a screenshot as the agent account.
for mode in project runtime; do
  log="$TMP/docker-$mode.log"
  if [ "$mode" = runtime ]; then
    PATH="$TMP/bin:/usr/bin:/bin" MC_TEST_DOCKER_LOG="$log" "$HERE/browser-smoke.sh" mc-prod --runtime >"$TMP/output-$mode"
  else
    PATH="$TMP/bin:/usr/bin:/bin" MC_TEST_DOCKER_LOG="$log" "$HERE/browser-smoke.sh" mc-prod >"$TMP/output-$mode"
  fi
  for arg in run --rm --network none --user 10002:10002 --cap-drop ALL HOME=/tmp/mc-browser-smoke mc-prod; do
    grep -Fx -- "$arg" "$log" >/dev/null || fail "the $mode smoke omitted $arg"
  done
  grep -F -- 'test "$(command -v playwright-cli)" = "$1"' "$log" >/dev/null ||
    fail "the $mode smoke never checks which browser tool it starts"
  grep -F -- 'playwright-cli -s="$session" --browser=chromium open' "$log" >/dev/null ||
    fail "the $mode smoke never opens the real browser with the only installed browser"
  grep -F -- 'grep -F "Culler browser ready" "$HOME/page.yml"' "$log" >/dev/null ||
    fail "the $mode smoke never reads the page content"
  grep -F -- 'screenshot --full-page' "$log" >/dev/null ||
    fail "the $mode smoke never captures visual evidence"
  grep -F -- 'delete-data' "$log" >/dev/null ||
    fail "the $mode smoke leaves browser profile state behind"
done

# The Project mode keeps the PATH of the image and starts the Project tool.
if grep -F -- 'PATH=' "$TMP/docker-project.log" >/dev/null; then
  fail "the Project smoke replaces the PATH of the image"
fi
grep -Fx -- '/usr/local/bin/playwright-cli' "$TMP/docker-project.log" >/dev/null ||
  fail "the Project smoke does not name the Project browser tool"

# The runtime mode has the PATH of an agent container and starts the runtime browser tool.
grep -Fx -- 'PATH=/opt/culler-runtime/commands:/usr/local/bin:/usr/bin:/bin' "$TMP/docker-runtime.log" >/dev/null ||
  fail "the runtime smoke does not use the PATH of an agent container"
grep -Fx -- '/opt/culler-runtime/commands/playwright-cli' "$TMP/docker-runtime.log" >/dev/null ||
  fail "the runtime smoke does not name the runtime browser tool"

# Each role surface must select the installed browser.
for surface in \
  ../../mc/verbs/skills/verifier/verify-behavior/SKILL.md \
  ../../mc/verbs/skills/packager/packager-browser/SKILL.md; do
  path="$HERE/$surface"
  test -f "$path" || fail "role surface missing: $surface"
  grep -F -- '--browser=chromium' "$path" >/dev/null ||
    fail "role surface never names --browser=chromium: $surface"
done

printf 'production image browser smoke envelope: ok\n'
