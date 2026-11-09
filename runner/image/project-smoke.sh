#!/bin/sh
set -eu
project=${1:?The Project image is required.}
runtime=${2:?The Culler runtime image is required.}
case "$project:$runtime" in -*|*:-*) exit 2 ;; esac
volume="culler-project-runtime-check-$$"
spine="culler-project-spine-check-$$"
fixture=$(mktemp -d)
docker volume create "$volume" >/dev/null
docker volume create "$spine" >/dev/null
trap 'docker volume rm "$volume" "$spine" >/dev/null; rm -rf "$fixture"' EXIT HUP INT TERM
docker run --rm --network none --user 0:0 \
  --mount "type=volume,src=$volume,dst=/runtime-output,volume-nocopy" \
  "$runtime" /bin/sh -ec 'cp -a /opt/culler-runtime/. /runtime-output/'
docker run --rm --network none --user 10002:10002 --cap-drop ALL \
  --env HOME=/home/mc-model "$project" /bin/sh -ec '
    test ! -e /opt/culler-runtime/bin/bun
    test ! -e /usr/local/bin/mc
    test ! -e /opt/openwiki
    test -s /usr/share/culler-project/manifest.json
    node --version
    bun --version
    python3 --version
    go version
    uv --version
    mise --version
    pnpm --version
    cmake --version
    ninja --version
    cc --version
    c++ --version
    make --version
    pkg-config --version
    git --version
    rg --version
    jq --version
    mkdir -p /tmp/mise-check
    cd /tmp/mise-check
    printf "[tools]\ngo = '\''1.25.12'\''\nbun = '\''1.3.12'\''\n" >mise.toml
    MISE_TRUSTED_CONFIG_PATHS=/tmp/mise-check mise exec -- go version
    MISE_TRUSTED_CONFIG_PATHS=/tmp/mise-check mise exec -- bun --version
  '
docker run --rm --network none --user 10002:10002 --cap-drop ALL \
  --mount "type=volume,src=$volume,dst=/opt/culler-runtime,readonly,volume-nocopy" \
  "$project" /bin/sh -ec '
    /opt/culler-runtime/bin/check
    test ! -e /opt/culler-runtime/node/bin/npm
    test ! -e /opt/culler-runtime/node/lib/node_modules
    test -z "$(find /opt/culler-runtime/ms-playwright -maxdepth 1 -name "chromium_headless_shell*")"
  '

# An update runs this check before it accepts the Project image.
release_id=$(docker run --rm --network none --entrypoint /bin/cat "$runtime" /opt/culler-runtime/release-id)
docker run --rm --network none --cap-drop ALL --user 10002:10002 --read-only \
  --security-opt no-new-privileges=true --pids-limit 128 --memory 512m --cpus 1 \
  --tmpfs /tmp:rw,nosuid,nodev,mode=1777 \
  --mount "type=volume,src=$volume,dst=/opt/culler-runtime,readonly,volume-nocopy" \
  --entrypoint /bin/sh "$project" \
  -ec "$(cat "$(dirname "$0")/../../mc/cmd/mc/candidate-runtime-check.sh")" check "$release_id"

# Initialize the scratch database with its owner account.
docker run --rm --network none --user 10001:10001 --cap-drop ALL \
  --env MC_HOME=/mc/spine --env MC_SPINE=/mc/spine/spine.db \
  --mount "type=volume,src=$volume,dst=/opt/culler-runtime,readonly,volume-nocopy" \
  --mount "type=volume,src=$spine,dst=/mc/spine" \
  "$project" /opt/culler-runtime/commands/mc __init \
    --spine /mc/spine/spine.db --project runtime-probe --workspace-root /workspace

# The scratch helper proves the setuid gate on the mounted volume.
docker run --rm --network none --user 10002:10002 --cap-drop ALL \
  --env MC_HOME=/mc/spine --env MC_SPINE=/mc/spine/spine.db \
  --mount "type=volume,src=$volume,dst=/opt/culler-runtime,readonly,volume-nocopy" \
  --mount "type=volume,src=$spine,dst=/mc/spine" \
  "$project" /bin/sh -ec '
    /opt/culler-runtime/commands/mc project list
    test ! -r /mc/spine/spine.db
    if cat /mc/spine/spine.db >/dev/null 2>&1; then exit 1; fi
  '
chmod 0755 "$fixture"
printf '%s\n' '{"tier":"pipeline","role":"worker","run_id":"runtime-probe"}' >"$fixture/run.json"
chmod 0644 "$fixture/run.json"
docker run --rm --network none --user 10002:10002 --cap-drop ALL \
  --mount "type=volume,src=$volume,dst=/opt/culler-runtime,readonly,volume-nocopy" \
  --mount "type=volume,src=$spine,dst=/mc/spine" \
  --mount "type=bind,src=$fixture/run.json,dst=/mc/run.json,readonly" \
  --env MC_RUN_JSON=/tmp/absent.json "$project" /bin/sh -ec '
    if /opt/culler-runtime/commands/mc dispatch >/tmp/refusal 2>&1; then exit 1; fi
    grep -F "host scope only; run.json callers are refused" /tmp/refusal
  '
