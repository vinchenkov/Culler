#!/bin/bash
# build-image.sh — bootstrap the mc-prod image on the target so the A16
# acceptor's C0 precondition is met. This is the A04-proven release-leg +
# build-prod sequence, run as root with the user bus named so build-prod's
# (correctly fail-closed) systemd precheck can connect. It performs NO A16
# assertion; it only produces the image the acceptor needs to be present.
set -eu
R=${A16_ROOT:?}
REPO=$R/repo
LEG=$R/leg
COMMIT=$(git -C "$REPO" rev-parse HEAD)
# Root's user manager socket, so systemctl --user works for the guard.
RT=$(id -u)
if [ -S "/run/user/$RT/bus" ]; then
  export XDG_RUNTIME_DIR="/run/user/$RT"
  export DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$RT/bus"
fi
export PATH="$HOME/.local/bin:$PATH"
export MISE_TRUSTED_CONFIG_PATHS="$REPO/.mise.toml"
if [ ! -x "$HOME/.local/bin/mise" ]; then
  install -d -m 700 "$HOME/.local/bin"
  curl -fsSL https://github.com/jdx/mise/releases/download/v2026.10.0/mise-v2026.10.0-linux-x64 -o "$HOME/.local/bin/mise"
  printf '%s  %s\n' '57ced973f968b8fbab07aa8e32bd7077d4a357e200a22356d98963c723c6de0a' "$HOME/.local/bin/mise" | sha256sum -c -
  chmod 755 "$HOME/.local/bin/mise"
fi
( cd "$REPO" && mise install go@1.25.12 && mise install bun@1.3.12 )
( cd "$REPO" && CULLER_COMMIT=$COMMIT CULLER_RELEASE_PLATFORM=linux-amd64 ./release/build.sh "$LEG" )
( cd "$REPO" && MC_RELEASE_BUILD_ID=$COMMIT ./runner/image/build-prod.sh )
docker image inspect mc-prod --format 'mc-prod arch={{.Architecture}} rev={{index .Config.Labels "org.opencontainers.image.revision"}}'
[ "$(docker image inspect mc-prod --format '{{.Architecture}}')" = amd64 ]
[ "$(docker image inspect mc-prod --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$COMMIT" ]
echo "BOOTSTRAP-OK commit=${COMMIT:0:12}"
