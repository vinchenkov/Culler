#!/bin/sh
# Start the browser of an image as the agent account: open a page, read the page content,
# and take a screenshot.
# Without --runtime, the check uses the Project tool `playwright-cli` on PATH.
# With --runtime, the container has the PATH of an agent container, and `playwright-cli`
# is the runtime browser tool at /opt/culler-runtime/commands/playwright-cli.
#
# `--browser=chromium` is mandatory: the default `chrome` channel has no Linux arm64 build.
# The Verifier skill and both role directives name this flag.
set -eu

usage() {
  echo "usage: browser-smoke.sh <image> [--runtime]" >&2
  exit 2
}

image=
mode=project
for argument in "$@"; do
  case "$argument" in
    --runtime) mode=runtime ;;
    -*) usage ;;
    *) [ -z "$image" ] || usage; image=$argument ;;
  esac
done
[ -n "$image" ] || usage

set -- --rm --network none --user 10002:10002 --cap-drop ALL --env HOME=/tmp/mc-browser-smoke
if [ "$mode" = runtime ]; then
  set -- "$@" --env PATH=/opt/culler-runtime/commands:/usr/local/bin:/usr/bin:/bin
  program=/opt/culler-runtime/commands/playwright-cli
  label='runtime browser'
else
  program=/usr/local/bin/playwright-cli
  label='Project browser'
fi

docker run "$@" "$image" /bin/sh -ec '
    test "$(command -v playwright-cli)" = "$1"
    mkdir -p "$HOME"
    cd "$HOME"
    session=mc-image-smoke
    trap '\''playwright-cli -s="$session" delete-data >/dev/null 2>&1 || :'\'' EXIT HUP INT TERM
    playwright-cli -s="$session" --browser=chromium open "data:text/html,<title>Culler Browser Smoke</title><h1>Culler browser ready</h1>"
    playwright-cli -s="$session" snapshot --filename="$HOME/page.yml"
    grep -F "Culler browser ready" "$HOME/page.yml" >/dev/null
    playwright-cli -s="$session" screenshot --full-page --filename="$HOME/evidence.png"
    test -s "$HOME/evidence.png"
  ' browser-smoke "$program"

printf '%s %s: ok\n' "$image" "$label"
