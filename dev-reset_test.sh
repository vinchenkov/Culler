#!/bin/sh
# dev-reset.sh destroys a deployment. These Docker-free probes check that it
# names its target explicitly and keeps the host mc and the image paired.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/culler-dev-reset-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
fail() { printf 'dev-reset_test.sh: %s\n' "$*" >&2; exit 1; }

REPO="$TMP/repo"
mkdir -p "$REPO/mc" "$REPO/runner/image" "$TMP/tools" "$TMP/bin" "$TMP/deploy/home"
cp "$ROOT/dev-reset.sh" "$REPO/dev-reset.sh"
printf '0.1.0\n' >"$REPO/VERSION"
: >"$TMP/deploy/home/config.toml"
LOG="$TMP/calls"
export DEV_RESET_TEST_LOG="$LOG"

# Each stub records its call. The mc stub records its generation, so the log
# shows which mc ran each step.
write_mc() {
  cat >"$1" <<EOF
#!/bin/sh
printf 'mc-$2 %s\n' "\$*" >>"\$DEV_RESET_TEST_LOG"
[ "\$1 \${DEV_RESET_TEST_FAIL:-}" != "__reset reset" ]
EOF
  chmod +x "$1"
}
write_mc "$TMP/bin/mc" old
write_mc "$TMP/new-mc" new
cat >"$TMP/tools/mise" <<EOF
#!/bin/sh
printf 'mise %s\n' "\$*" >>"\$DEV_RESET_TEST_LOG"
while [ "\$#" -gt 0 ]; do
  if [ "\$1" = -o ]; then cp "$TMP/new-mc" "\$2"; exit 0; fi
  shift
done
EOF
# The units stay listed until a bootout, or for good when the test sets STUCK.
cat >"$TMP/tools/launchctl" <<'EOF'
#!/bin/sh
printf 'launchctl %s\n' "$*" >>"$DEV_RESET_TEST_LOG"
case "$1" in
  bootout) : >"$DEV_RESET_TEST_LOG.booted" ;;
  list)
    if [ -n "${DEV_RESET_TEST_STUCK:-}" ] || [ ! -e "$DEV_RESET_TEST_LOG.booted" ]; then
      printf '1\t0\tcom.culler.abc.resident\n2\t0\tcom.culler.abc.dashboard\n'
    fi ;;
esac
exit 0
EOF
cat >"$TMP/tools/docker" <<'EOF'
#!/bin/sh
printf 'docker %s\n' "$*" >>"$DEV_RESET_TEST_LOG"
EOF
cat >"$TMP/tools/git" <<'EOF'
#!/bin/sh
echo 0123456789abcdef0123456789abcdef01234567
EOF
cat >"$REPO/runner/image/build-prod.sh" <<'EOF'
#!/bin/sh
printf 'build-prod\n' >>"$DEV_RESET_TEST_LOG"
[ "${DEV_RESET_TEST_FAIL:-}" != image ]
EOF
chmod +x "$TMP/tools/"* "$REPO/runner/image/build-prod.sh"
mkdir -p "$TMP/home"
export PATH="$TMP/tools:$TMP/bin:$PATH" HOME="$TMP/home"

# An absent MC_HOME never falls back to the production home.
: >"$LOG"
status=0
env -u MC_HOME "$REPO/dev-reset.sh" --confirm >"$TMP/out" 2>&1 || status=$?
[ "$status" -eq 2 ] || fail "a run without MC_HOME exited $status, not 2"
grep -F 'set MC_HOME' "$TMP/out" >/dev/null || fail 'the refusal does not name MC_HOME'
[ ! -s "$LOG" ] || fail 'a run without MC_HOME called a tool'

env -u MC_HOME "$REPO/dev-reset.sh" --help >"$TMP/out" 2>&1 || fail 'help failed without MC_HOME'
grep -F 'TARGET: the deployment home that MC_HOME names' "$TMP/out" >/dev/null || fail 'help omits the target'
grep -F 'set -euo pipefail' "$TMP/out" >/dev/null && fail 'help prints script code'

# The plan names no mc command for the operator.
MC_HOME="$TMP/deploy/home" "$REPO/dev-reset.sh" --rebuild >"$TMP/out" 2>&1 || fail 'the plan failed'
grep -F 'Add Project' "$TMP/out" >/dev/null || fail 'the plan omits Add Project'
grep -E 'mc (onboard|task) ' "$TMP/out" >/dev/null && fail 'the plan tells the operator to run mc'

# A unit that stays loaded after the stop blocks the image build and the reset.
: >"$LOG"; rm -f "$LOG.booted"
status=0
DEV_RESET_TEST_STUCK=1 MC_HOME="$TMP/deploy/home" "$REPO/dev-reset.sh" --confirm --rebuild >"$TMP/out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail 'a stuck unit returned success'
grep -F 'still loaded after the stop' "$TMP/out" >/dev/null || fail 'the stuck unit failure omits the cause'
grep -E '^build-prod$|__reset' "$LOG" >/dev/null && fail 'a stuck unit did not stop the build and the reset'
cmp -s "$TMP/bin/mc" "$TMP/new-mc" && fail 'a stuck unit replaced the mc on PATH'

# The rebuild compiles first, builds the image after the stop, and installs the
# new mc before the reset.
: >"$LOG"; rm -f "$LOG.booted"
MC_HOME="$TMP/deploy/home" "$REPO/dev-reset.sh" --confirm --rebuild >"$TMP/out" 2>&1 || { cat "$TMP/out" >&2; fail 'the rebuild failed'; }
order=$(awk '
  /^mise .*go build/ && !c { c = NR }
  /^launchctl bootout/ && !s { s = NR }
  /^build-prod$/ && !b { b = NR }
  /^mc-new __reset/ && !r { r = NR }
  END { print (c && s && b && r && c < s && s < b && b < r) ? "ok" : "bad" }
' "$LOG")
[ "$order" = ok ] || { cat "$LOG" >&2; fail 'the rebuild ran out of order'; }
grep -F 'mc-old __reset' "$LOG" >/dev/null && fail 'the reset used the old mc'
cmp -s "$TMP/bin/mc" "$TMP/new-mc" || fail 'the new mc did not replace the mc on PATH'

# A refused reset names the database replacement and removes no files.
: >"$LOG"; rm -f "$LOG.booted"
mkdir -p "$TMP/deploy/home/runs/run-1"
status=0
DEV_RESET_TEST_FAIL=reset MC_HOME="$TMP/deploy/home" "$REPO/dev-reset.sh" --confirm >"$TMP/out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail 'a refused reset returned success'
grep -F './redeploy.sh --reset-database' "$TMP/out" >/dev/null || fail 'the refused reset omits the database replacement'
[ -d "$TMP/deploy/home/runs/run-1" ] || fail 'a refused reset removed run files'

# A failed image build keeps the old mc and destroys nothing.
write_mc "$TMP/bin/mc" old
: >"$LOG"; rm -f "$LOG.booted"
status=0
DEV_RESET_TEST_FAIL=image MC_HOME="$TMP/deploy/home" "$REPO/dev-reset.sh" --confirm --rebuild >"$TMP/out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail 'a failed image build returned success'
grep -F 'nothing was removed' "$TMP/out" >/dev/null || fail 'the image failure omits the state'
grep -F '__reset' "$LOG" >/dev/null && fail 'a failed image build reset the spine'
cmp -s "$TMP/bin/mc" "$TMP/new-mc" && fail 'a failed image build replaced the mc on PATH'
echo 'dev-reset_test.sh: ok'
