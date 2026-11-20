#!/bin/sh
# a03-systemd-acceptance.sh — A03 真机验收脚本（在目标机上运行）
#
# 真实 systemd 监督行为尚未验证，本脚本为其验收器。
# True systemd supervision behavior is not verified yet; this script is its
# acceptor. A PASS here is acceptance evidence for the checks it runs, for
# nothing else, and it is registered only together with its raw output.
#
# Execution prerequisites (requirement ②):
#   - Ubuntu 22.04.x with systemd 249 (the designated host recorded in
#     docs/facts/o3-environment-probe-001.md §3.5); another host may run it,
#     but its captured version lines decide what the result is about.
#   - Run as the user whose systemd --user manager is exercised. The manager
#     must be reachable: the runtime directory holds its bus socket.
#   - The companion binary a03-deployment.amd64 (linux/amd64 build of
#     go test -c ./deployment from the A03 ticket branch) sits next to this
#     script; the script executes nothing else it did not receive.
#   - Non-persistent modality, by operator ruling: no unit file is installed
#     into any unit directory, nothing is enabled, linger is untouched, and no
#     pre-existing service or file is modified. Every unit the script starts
#     is transient (--collect) and named a03-transient-*; every file it writes
#     lives inside the work directory under this script's own directory or in
#     the shared ${TMPDIR:-/tmp}/a03-render-fixture the test binary builds,
#     and both are removed when the script exits.
#
# Success criteria (requirement ③): every check records "<id>.result PASS" or
# "<id>.result FAIL reason=<token>" with a timestamp, and the script prints
# "ACCEPT: PASS" only when all checks pass, else
# "ACCEPT: FAIL failing=<ids>". Reason tokens: test-binary-failed,
# unit-extract-count, no-dump, user-manager-unreachable, verify-nonzero,
# transient-not-active, disable-now-left-it-running, is-active-map, residue.
#
# Raw outputs captured (requirement ④): each executed argv, its exit code and
# combined output appear between ACCEPT-RAW-BEGIN and ACCEPT-RAW-END, every
# record prefixed ts=<UTC ISO8601> host=<name> uid=<id>.
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BIN="$HERE/a03-deployment.amd64"
WORK="$HERE/work"
rm -rf "$WORK"
mkdir -m 700 -p "$WORK"
RAWF="$WORK/raw.log"
: >"$RAWF"
printf '0\n' >"$WORK/extracted.count"

WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$(uname -n)" "$(id -u)"; }
record() { # id slot status [reason]
  printf '%s.%s %s%s %s\n' "$1" "$2" "$3" "${4:+ $4}" "$(WHO)" >>"$WORK/results"
}
# run <label> <argv...>: execute with an argument vector (never a rebuilt
# shell string) and append the transcript to the raw log.
run() {
  label=$1; shift
  printf '%s RUN' "$(WHO)" >>"$RAWF"
  for arg in "$@"; do printf ' [%s]' "$arg" >>"$RAWF"; done
  printf '\n' >>"$RAWF"
  status=0
  "$@" >>"$RAWF" 2>&1 || status=$?
  printf '%s EXIT %s=%s\n' "$(WHO)" "$label" "$status" >>"$RAWF"
  return "$status"
}

printf 'ACCEPT-RAW-BEGIN\n'

# --- C0 environment identity -------------------------------------------------
echo "== C0 env" >>"$RAWF"
run C0.systemctl systemctl --version || true
run C0.uname uname -a || true
run C0.target systemctl get-default || true
printf '%s RUNTIME_DIR_ENV=[%s]\n' "$(WHO)" "${XDG_RUNTIME_DIR:-}" >>"$RAWF"
BUS_OK=0
RT=${XDG_RUNTIME_DIR:-}
[ -n "$RT" ] || RT=/run/user/$(id -u)
if [ -S "$RT/bus" ]; then
  XDG_RUNTIME_DIR=$RT
  export XDG_RUNTIME_DIR
  BUS_OK=1
fi
printf '%s C0.bus ok=%s\n' "$(WHO)" "$BUS_OK" >>"$RAWF"

# --- C1 the real test binary on this host -------------------------------------
if [ ! -x "$BIN" ]; then
  record C1 result FAIL reason=test-binary-failed
  record C1b result FAIL reason=test-binary-failed
else
  if run C1.tests "$BIN" -test.run 'TestSystemd|TestRender|TestPlatformUpdateServicesLinux|TestA03RenderedUnits' -test.v; then
    record C1 result PASS
  else
    record C1 result FAIL reason=test-binary-failed
  fi
  if run C1b.dump env A03_DUMP=1 "$BIN" -test.run 'TestA03RenderedUnitsExport' -test.v; then
    n=0
    for name in $(grep '^A03-UNIT-BEGIN ' "$RAWF" | awk '{print $2}' | sort -u); do
      # Whitelist before any file write: only the flat unit names the Go dump
      # is allowed to print. Anything else is dropped, never used as a path.
      case "$name" in
        com.culler.[0-9a-f]*.resident.service|\
        com.culler.[0-9a-f]*.dashboard.service|\
        com.culler.[0-9a-f]*.agentsview.service) ;;
        *) continue ;;
      esac
      awk -v want="$name" '
        $1 == "A03-UNIT-BEGIN" { collecting = ($2 == want); next }
        $1 == "A03-UNIT-END"   { collecting = 0; next }
        collecting             { print }
      ' "$RAWF" >"$WORK/$name"
      [ -s "$WORK/$name" ] && grep -q '^\[Service\]' "$WORK/$name" && n=$((n + 1))
    done
    printf '%s\n' "$n" >"$WORK/extracted.count"
    if [ "$n" -eq 3 ]; then
      record C1b result PASS
    else
      record C1b result FAIL reason=unit-extract-count
    fi
  else
    record C1b result FAIL reason=no-dump
  fi
fi

# --- C2 systemd-analyze verify on the extracted bytes --------------------------
if [ "$BUS_OK" = 1 ] && [ "$(cat "$WORK/extracted.count")" = 3 ]; then
  c2=PASS
  for unit in "$WORK"/com.culler.*.service; do
    run "C2.verify.$(basename "$unit" .service)" systemd-analyze --user verify "$unit" || c2=FAIL
  done
  if [ "$c2" = PASS ]; then record C2 result PASS; else record C2 result FAIL reason=verify-nonzero; fi
elif [ "$BUS_OK" != 1 ]; then
  record C2 result FAIL reason=user-manager-unreachable
else
  record C2 result FAIL reason=unit-extract-count
fi

# --- C3..C6 transient lifecycle, the stage-4 verbs against a real unit ---------
UNIT="a03-transient-$$"
cleanup() {
  systemctl --user stop "$UNIT" >/dev/null 2>&1 || true
  rm -rf "$WORK" "${TMPDIR:-/tmp}/a03-render-fixture" 2>/dev/null || true
}
trap cleanup EXIT
if [ "$BUS_OK" = 1 ]; then
  started=0
  run C3.start systemd-run --user --collect --unit "$UNIT" /bin/sleep 30 && started=1
  if [ "$started" = 1 ]; then
    polls=0; active=0
    while [ "$polls" -lt 20 ]; do
      systemctl --user is-active "$UNIT" >/dev/null 2>&1 && { active=1; break; }
      polls=$((polls + 1)); sleep 0.1
    done
    if [ "$active" = 1 ]; then
      record C3 result PASS
      run C4.stop systemctl --user disable --now "$UNIT" || true
      polls=0; stopped=0
      while [ "$polls" -lt 50 ]; do
        out=$(systemctl --user is-active "$UNIT" 2>&1 || true)
        case "$out" in
          active) ;;
          *) stopped=1; break ;;
        esac
        polls=$((polls + 1)); sleep 0.1
      done
      if [ "$stopped" = 1 ]; then
        record C4 result PASS
        printf '%s C4.final_state=[%s]\n' "$(WHO)" "$out" >>"$RAWF"
      else
        record C4 result FAIL reason=disable-now-left-it-running
      fi
    else
      record C3 result FAIL reason=transient-not-active
    fi
  else
    record C3 result FAIL reason=user-manager-unreachable
  fi
  # C5 the confirm mapping of stage ④: a never-existing unit must answer with
  # a nonzero exit and a token the mapping reads as stopped. Which token this
  # systemd chooses is a captured fact, not a criterion; the contract is only
  # "nonzero AND {inactive, failed, unknown}".
  status=0
  out=$(systemctl --user is-active a03-no-such-unit.service 2>&1) || status=$?
  printf '%s RUN is-active-absent EXIT=%s out=[%s]\n' "$(WHO)" "$status" "$out" >>"$RAWF"
  c5=FAIL
  if [ "$status" -ne 0 ]; then
    case "$out" in
      inactive|failed|unknown) c5=PASS ;;
    esac
  fi
  if [ "$c5" = PASS ]; then
    record C5 result PASS
  else
    record C5 result FAIL reason=is-active-map
  fi
  # C6 residue: no a03-* unit remains in any state.
  left=$(systemctl --user list-units --no-legend --all 'a03-*' 2>/dev/null || true)
  if [ -z "$left" ]; then
    record C6 result PASS
  else
    printf '%s RESIDUE=[%s]\n' "$(WHO)" "$left" >>"$RAWF"
    record C6 result FAIL reason=residue
  fi
else
  for id in C3 C4 C5 C6; do record "$id" result FAIL reason=user-manager-unreachable; done
fi

sed 's/^/| /' "$RAWF"
printf 'ACCEPT-RAW-END\n'
cat "$WORK/results"
failing=$(awk '$2=="FAIL"{printf "%s,", $1}' "$WORK/results")
if [ -z "$failing" ]; then
  printf 'ACCEPT: PASS\n'
else
  printf 'ACCEPT: FAIL failing=%s\n' "${failing%,}"
  exit 1
fi
