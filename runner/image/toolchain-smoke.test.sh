#!/bin/sh
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT HUP INT TERM
cat >"$root/docker" <<'STUB'
#!/bin/sh
set -eu
[ "$1" = run ]
shift
while [ "$#" -gt 0 ]; do
  case "$1" in
    --user) [ "$2" = 10002:10002 ]; agent=yes; shift ;;
    --network) exit 1 ;;
    --cap-drop) [ "$2" = ALL ]; capabilities=yes; shift ;;
    --security-opt) [ "$2" = no-new-privileges ]; no_privileges=yes; shift ;;
    --env)
      case "$2" in
        GOTOOLCHAIN=local) local_toolchain=yes ;;
        GOFLAGS=-mod=readonly) readonly_modules=yes ;;
        EXPECTED_GO_VERSION=?*) expected_version=yes ;;
      esac
      shift ;;
    --mount) case "$2" in *target=/source,readonly) source=yes ;; *) exit 1 ;; esac; shift ;;
    /bin/sh) shift; [ "$1" = -ec ]; shift; body=$1; break ;;
  esac
  shift
done
[ "${agent:-}" = yes ]
[ "${local_toolchain:-}" = yes ]
[ "${readonly_modules:-}" = yes ]
[ "${expected_version:-}" = yes ]
[ "${source:-}" = yes ]
[ "${capabilities:-}" = yes ]
[ "${no_privileges:-}" = yes ]
printf '%s' "$body" | grep -F 'test "$(mise exec -- go env GOVERSION)" = "go$EXPECTED_GO_VERSION"' >/dev/null
printf '%s' "$body" | grep -F 'mise exec -- go build -o /tmp/mc ./cmd/mc' >/dev/null
printf '%s' "$body" | grep -F 'sha256sum -c /tmp/mc-module-checksums' >/dev/null
exit "${TEST_DOCKER_EXIT:-0}"
STUB
chmod 755 "$root/docker"
PATH="$root:$PATH" "$HERE/toolchain-smoke.sh" test-image
if PATH="$root:$PATH" TEST_DOCKER_EXIT=19 "$HERE/toolchain-smoke.sh" test-image; then
  exit 1
else
  [ "$?" -eq 19 ]
fi
