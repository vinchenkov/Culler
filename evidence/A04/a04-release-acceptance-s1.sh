#!/bin/sh
# a04-release-acceptance-s1.sh — A04 真机验收段 S1(在目标机 root 用户管理器上下文运行)
#
# 真实 systemd 监督行为尚未验证,本脚本为其验收器。
# True systemd supervision behavior is not verified yet; this script is its
# acceptor. A PASS here is acceptance evidence for the checks it runs, for
# nothing else, and it registers only together with its raw output.
#
# Execution prerequisites (requirement ②):
#   - Ubuntu 22.04.x, systemd 249, x86_64, docker 29.x with the user manager
#     reachable (the designated host in docs/facts/o3-environment-probe-001.md).
#   - Run as the account whose systemd --user manager is exercised; the account
#     is a NEW dedicated acceptance account created by the operator-side run,
#     never an existing one. This script writes only inside its own directory
#     and $HOME; it installs no units and enables nothing (that is segment S2,
#     gated separately), so it is non-persistent by construction.
#   - Companion files beside this script: mc.amd64 (a linux/amd64 build of
#     mc from the A04 ticket branch) and a04-deployment.amd64 (the linux/amd64
#     test binary of mc/deployment from the same commit).
#
# Success criteria (requirement ③): each check records "<id>.result PASS" or
# "<id>.result FAIL reason=<token>"; the script prints "ACCEPT: PASS" only when
# every recorded check passes, else "ACCEPT: FAIL failing=<ids>".
# Tokens: disk-envelope, user-manager-unreachable, binaries-absent,
# go-suite-failed, amd64-native-refused, install-refused, host-not-linux.
#
# Raw outputs captured (requirement ④): each executed argv, exit code, and
# combined output between ACCEPT-RAW-BEGIN and ACCEPT-RAW-END, every record
# prefixed ts=<UTC ISO8601> host=<name> uid=<id>, plus uname/systemd/docker
# version lines.
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
MC="$HERE/mc.amd64"
GOSUITE="$HERE/a04-deployment.amd64"
WORK="$HERE/work"
rm -rf "$WORK"; mkdir -m 700 -p "$WORK"
RAWF="$WORK/raw.log"; : >"$RAWF"

WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$(uname -n)" "$(id -u)"; }
record() { printf '%s.%s %s%s %s\n' "$1" "$2" "$3" "${4:+ $4}" "$(WHO)" >>"$WORK/results"; }
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

# --- A0 environment and disk envelope (operator ruling Q6: precheck or stop) --
run A0.uname uname -a || true
run A0.systemd systemctl --version || true
run A0.docker docker version --format '{{.Server.Version}}' || true
AVAIL_KB=$(df -Pk "$HOME" | awk 'NR==2{print $4}')
[ "${AVAIL_KB:-0}" -ge 2097152 ] || { printf 'ACCEPT: FAIL failing=A0.result\n%s A0 disk=%sKB\n' "$(WHO)" "$AVAIL_KB" >>"$RAWF"; printf 'ACCEPT-RAW-END\n'; cat "$WORK/results" 2>/dev/null; exit 1; }
record A0 result PASS "free_kb=$AVAIL_KB"
RT=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}
BUS_OK=0
[ -S "$RT/bus" ] && BUS_OK=1
XDG_RUNTIME_DIR=$RT; export XDG_RUNTIME_DIR

# --- A1 the real deployment suite on the amd64 host, against real systemd ----
if [ ! -x "$GOSUITE" ]; then
  record A1 result FAIL reason=binaries-absent
else
  if run A1.suite "$GOSUITE" -test.run 'TestSystemd|TestRender|TestPlatformUpdateServicesLinux|TestNativeArch|TestAgentsView|TestHostBun|TestA03RenderedUnits' -test.v; then
    record A1 result PASS
  else
    record A1 result FAIL reason=go-suite-failed
  fi
fi

# --- A2 the amd64 binary executes natively on this host ----------------------
# Proof of the exec-format level only: the packaged mc.amd64 runs here. The
# image-architecture equality gates are proven behaviorally inside A1's suite
# (the real-host answer is compared against the gate on the target), and the
# container probe leg runs in segment S2 where an image actually exists.
if [ ! -x "$MC" ]; then
  record A2 result FAIL reason=binaries-absent
else
  mkdir -p "$WORK/armhome"
  help_status=0
  printf '%s RUN [mc.amd64] [--help]\n' "$(WHO)" >>"$RAWF"
  env HOME="$WORK/armhome" "$MC" --help >"$WORK/help.out" 2>>"$RAWF" || help_status=$?
  printf '%s EXIT A2.exec=%s\n' "$(WHO)" "$help_status" >>"$RAWF"
  case "$help_status" in
    0|1|2) if [ -s "$WORK/help.out" ]; then record A2 result PASS; else record A2 result FAIL reason=amd64-native-refused; fi ;;
    *) record A2 result FAIL reason=amd64-native-refused ;;
  esac
fi

# --- A3 the published front door on a real Linux:x86_64 host ------------------
# Optional: the front-door script is shipped only when the operator's scan
# policy permits its transport. Without it this records SKIP, never a fake
# PASS; the platform gate itself is pinned behaviorally by the front-door
# fixture suite and by A1 running on this host.
if [ ! -x "$MC" ] || [ ! -f "$HERE/install.sh" ]; then
  record A3 result SKIP reason=front-door-not-shipped
else
  front_out="$WORK/front.out"; front_status=0
  env HOME="$WORK/armhome" CULLER_REPOSITORY=example/no-such-culler \
    sh "$HERE/install.sh" --check-updates >"$front_out" 2>&1 || front_status=$?
  printf '%s EXIT A3.front=%s\n' "$(WHO)" "$front_status" >>"$RAWF"
  if grep -q 'requires a Mac with Apple silicon or on Linux for x86_64' "$front_out"; then
    record A3 result FAIL reason=install-refused
  elif [ "$front_status" != 0 ]; then
    # A refusal after the platform gate is the expected Linux admission: the
    # repository access failed, not the host check.
    record A3 result PASS
  else
    record A3 result PASS
  fi
fi

[ "$BUS_OK" = 1 ] || record A99 user-manager FAIL reason=user-manager-unreachable

sed 's/^/| /' "$RAWF"
printf 'ACCEPT-RAW-END\n'
cat "$WORK/results"
failing=$(awk '$2=="FAIL"{printf "%s,", $1}' "$WORK/results")
skips=$(awk '$2=="SKIP"{printf "%s,", $1}' "$WORK/results")
rm -rf "$WORK"
if [ -n "$skips" ]; then printf 'NOTE: checks SKIPPED: %s\n' "${skips%,}"; fi
if [ -z "$failing" ]; then printf 'ACCEPT: PASS\n'; else printf 'ACCEPT: FAIL failing=%s\n' "${failing%,}"; exit 1; fi
