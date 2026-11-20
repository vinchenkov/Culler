#!/bin/sh
set -eu

# Minimum behavior suite for the redeploy-dashboard service-manager branches.
# It pins the four rulings: the Linux branch calls systemctl with the right
# unit and arguments, never launchctl; a systemd failure fails the script
# without any launchd fallback; the override is fail-closed; and the default
# (no override) follows the real host OS. Darwin is exercised too so the
# branch shape is proven from both sides.

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/mc-redeploy-dashboard-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

fail() { printf 'redeploy-dashboard_test.sh: %s\n' "$*" >&2; exit 1; }

mkdir -p "$TMP/bin" "$TMP/home/release/host/dashboard/src"
# The installed release differs from HEAD so the script updates a .ts file and
# must restart the dashboard.
printf 'console.log("installed-v1");\n' >"$TMP/home/release/host/dashboard/src/server.ts"

repo="$TMP/repo"
mkdir -p "$repo/scripts" "$repo/dashboard/src" "$repo/mc/deployment"
cp "$HERE/redeploy-dashboard.sh" "$repo/"
cp "$HERE/scripts/source-update-guard.sh" "$repo/scripts/"
printf '\t"dashboard/src/server.ts",\n' >"$repo/mc/deployment/host_release.go"
printf 'console.log("head-v2");\n' >"$repo/dashboard/src/server.ts"
git -C "$repo" init -q
git -C "$repo" -c user.email=t@example.invalid -c user.name=test add -A
git -C "$repo" -c user.email=t@example.invalid -c user.name=test commit -qm head

cat >"$TMP/bin/systemctl" <<'EOF'
#!/bin/sh
printf 'systemctl %s\n' "$*" >>"$STUB_LOG"
case "$*" in
  *list-units*) [ "${STUB_SYSTEMD_NO_UNIT:-}" = 1 ] || printf 'com.culler.abc123456789.dashboard.service loaded active running Culler dashboard\n' ;;
  *restart*) [ "${STUB_SYSTEMD_FAIL:-}" = 1 ] && exit 5 ;;
esac
exit 0
EOF
cat >"$TMP/bin/launchctl" <<'EOF'
#!/bin/sh
printf 'launchctl %s\n' "$*" >>"$STUB_LOG"
case "$1" in
  list) printf -- '-\t0\tcom.culler.abc123456789.dashboard\n' ;;
esac
exit 0
EOF
chmod 755 "$TMP/bin/systemctl" "$TMP/bin/launchctl"

run_case() { # name expected-status extra-env...
  name=$1; want=$2; shift 2
  : >"$TMP/stub.log"; STUB_LOG="$TMP/stub.log"
  export STUB_LOG
  # Restore the "installed differs from HEAD" precondition so every case
  # actually reaches the restart branch (the script rewrites the file on success).
  printf 'console.log("installed-v1");\n' >"$TMP/home/release/host/dashboard/src/server.ts"
  status=0
  out=$(/usr/bin/env "PATH=$TMP/bin:/usr/bin:/bin" "HOME=$TMP/home" "MC_HOME=$TMP/home" \
    "STUB_LOG=$STUB_LOG" "$@" sh "$repo/redeploy-dashboard.sh" 2>&1) || status=$?
  [ "$status" -eq "$want" ] || fail "$name exited $status, want $want"
  LAST_OUT=$out
}

# --- Linux happy path (lowercase override proves normalization) -------------
run_case linux 0 MC_OS_OVERRIDE=linux
case "$LAST_OUT" in *"== restart: com.culler.abc123456789.dashboard.service"*) ;; *) fail "linux branch did not restart the systemd unit: $LAST_OUT" ;; esac
grep -F 'systemctl --user restart com.culler.abc123456789.dashboard.service' "$TMP/stub.log" >/dev/null || fail "restart argument wrong: $(cat "$TMP/stub.log")"
grep -F 'launchctl' "$TMP/stub.log" && fail "linux branch called launchctl"

# --- systemd failure must fail closed, never fall back ----------------------
run_case linux-fail 1 MC_OS_OVERRIDE=Linux STUB_SYSTEMD_FAIL=1
case "$LAST_OUT" in *"could not restart"*) ;; *) fail "systemd failure was not surfaced: $LAST_OUT" ;; esac
grep -F 'launchctl' "$TMP/stub.log" && fail "the failure path fell back to launchctl"

# --- missing unit is a refusal, not silence ---------------------------------
run_case linux-nounit 1 MC_OS_OVERRIDE=Linux STUB_SYSTEMD_NO_UNIT=1
case "$LAST_OUT" in *'unit is loaded; run redeploy.sh'*) ;; *) fail "missing unit not refused: $LAST_OUT" ;; esac

# --- override is fail-closed ------------------------------------------------
run_case bad-override 2 MC_OS_OVERRIDE=Plan9
case "$LAST_OUT" in *'must be Darwin or Linux'*) ;; *) fail "invalid override not refused: $LAST_OUT" ;; esac
[ -s "$TMP/stub.log" ] && fail "invalid override still touched a service manager"

# --- no override follows the real host, proven via a faked uname ------------
cat >"$TMP/bin/uname" <<'EOF'
#!/bin/sh
printf 'Linux\n'
EOF
chmod 755 "$TMP/bin/uname"
run_case default-uname 0 MC_OS_OVERRIDE=
case "$LAST_OUT" in *"== restart: com.culler.abc123456789.dashboard.service"*) ;; *) fail "default OS path did not follow the host: $LAST_OUT" ;; esac
rm -f "$TMP/bin/uname"

# --- Darwin branch still uses launchctl, not systemctl ----------------------
run_case darwin 0 MC_OS_OVERRIDE=Darwin
case "$LAST_OUT" in *"== restart: com.culler.abc123456789.dashboard"*) ;; *) fail "darwin branch lost its label: $LAST_OUT" ;; esac
grep -F 'launchctl kill TERM' "$TMP/stub.log" >/dev/null || fail "darwin branch did not signal via launchctl"
grep -F 'systemctl' "$TMP/stub.log" && fail "darwin branch called systemctl"

printf 'redeploy-dashboard_test.sh: ok\n'
