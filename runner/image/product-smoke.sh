#!/bin/sh
set -eu
image=${1:?The image identifier is required.}
case "$image" in -*) exit 2 ;; esac
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

docker run --rm --network none --user 10002:10002 --cap-drop ALL \
  --env HOME=/home/mc-model "$image" /bin/sh -ec '
    test "$(id -u)" = 10002
    test ! -e /workspace/source
    test -s /opt/culler-runtime/app/src/agent-runner/main.ts
    test -z "${GOFLAGS:-}${GOCACHE:-}${GOMODCACHE:-}${GOTOOLCHAIN:-}"
    go version
    mise --version
    bun --version
    node --version
    uv --version
    git --version
    test -x /usr/local/bin/mc
    test -x /usr/local/bin/mc-land
    test "$(stat -c %u:%g:%a /opt/culler-runtime/libexec/mc-real)" = 10001:10001:6755
    test -x /opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-arm64/claude
    test ! -e /opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-arm64-musl
    test -x /opt/culler-runtime/node/bin/node
    test ! -e /opt/culler-runtime/node/bin/npm
    test ! -e /opt/culler-runtime/node/lib/node_modules
    test -n "$(find /opt/culler-runtime/ms-playwright -maxdepth 1 -name "chromium-*")"
    test -n "$(find /opt/culler-runtime/ms-playwright -maxdepth 1 -name "ffmpeg-*")"
    test -z "$(find /opt/culler-runtime/ms-playwright -maxdepth 1 -name "chromium_headless_shell*")"
    test -z "$(find /opt/culler-runtime/ms-playwright -name "*.pak.info")"
    mkdir -p "$HOME/.agents/skills/product-probe"
    test ! -r /mc/spine
    test ! -r /mc/private
    node --input-type=module -e '\''await import("/opt/openwiki/node_modules/openwiki/dist/integrations/core/session-manager.js"); await import("/opt/openwiki/node_modules/openwiki/dist/integrations/mcp/server.js")'\''
  '

# The real binary must refuse a database on a temporary filesystem.
docker run --rm --network none --tmpfs /mc/spine:rw,mode=1777 \
  --env MC_HOME=/tmp --env MC_RUN_JSON=/tmp/absent.json "$image" /bin/sh -ec '
    if /opt/culler-runtime/libexec/mc-real project list >/tmp/result 2>&1; then
      exit 1
    fi
    grep -F "not a block-device-backed local filesystem" /tmp/result
    test ! -e /mc/spine/spine.db
  '

# A model identity cannot erase the fixed run identity through its environment.
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
chmod 0755 "$fixture"
printf '%s\n' '{"tier":"pipeline","role":"worker","run_id":"product-probe"}' >"$fixture/run.json"
chmod 0644 "$fixture/run.json"
docker run --rm --network none --user 10002:10002 --cap-drop ALL \
  --mount "type=bind,source=$fixture/run.json,target=/mc/run.json,readonly" \
  --env MC_RUN_JSON=/tmp/absent.json "$image" /bin/sh -ec '
    if mc dispatch >/tmp/refusal 2>&1; then
      exit 1
    fi
    grep -F "host scope only; run.json callers are refused" /tmp/refusal
  '
"$HERE/browser-smoke.sh" "$image"
"$HERE/browser-smoke.sh" "$image" --runtime
