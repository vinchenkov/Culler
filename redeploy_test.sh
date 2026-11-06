#!/bin/sh
# redeploy_test.sh tests the redeploy front door without Docker: a fake
# toolchain and container runtime, the real script, and the real order. The
# test proves these results:
#   - the sequence build, database check, AgentsView, stop, image, install,
#     onboard home, tunables, supervision, doctor, and a rerun with the same
#     sequence;
#   - the kept database on a current schema;
#   - the early stop on a database with a different schema or an absent
#     database: no unit stop, no image build, no install, and the database
#     unchanged;
#   - the later stop of the deploy step when the database check cannot run,
#     and stop messages that name no mc command;
#   - the commit of the previously deployed release in a stop with stopped
#     units, from the supervision record or from the image label that the
#     script reads before the image build, and a statement when no record
#     names it;
#   - the confirmed database reset with the named deployment home and the
#     config.toml alert routes copied again, and a refused reset without a
#     terminal, with a wrong answer, with an empty answer, or without an
#     answer;
#   - a refused database check that states the service state;
#   - help text inside the message width;
#   - an image build failure that leaves the installed release untouched;
#   - an AgentsView install failure that leaves the units running and the
#     installed release untouched.
# The fake mc keeps a one-line fake database in MC_REDEPLOY_TEST_DB. The line
# "schema=current" is the schema of this release. MC_REDEPLOY_TEST_CHECK_EXIT
# selects a database check that refuses (1) or cannot run (2). The fake docker
# reports the mc-prod revision label from MC_REDEPLOY_TEST_IMAGE_RELEASE, and reports an
# absent image when that variable is empty.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/mc-redeploy-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

# Fake builds write only into the temporary repository.
python3 "$ROOT/scripts/test-source.py" "$ROOT" "$TMP/source"
ROOT=$(CDPATH= cd -- "$TMP/source" && pwd)

fail() { printf 'redeploy_test.sh: %s\n' "$*" >&2; exit 1; }

# Each mc build omits DWARF data and carries the release identity.
[ "$(grep -c -F -- '-ldflags "-w -X main.releaseBuildID=$RELEASE_BUILD_ID -X main.releaseVersion=$RELEASE_VERSION"' "$ROOT/redeploy.sh")" = 2 ] ||
  fail "a go build of redeploy.sh does not omit DWARF data"

make_fakes() {
  bin=$1
  mkdir -p "$bin"
  # mise exec -- go build -o <out>: writes a fake mc that records its argv.
  cat >"$bin/mise" <<'FAKE'
#!/bin/sh
set -eu
if [ "${1:-}" = exec ]; then
  shift
  [ "${1:-}" != -- ] || shift
fi
if [ "${1:-}" = go ] && [ "${2:-}" = build ]; then
  out=
  while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then
      out=${2:?missing build output}
      break
    fi
    shift
  done
  [ -n "$out" ] || exit 90
  cat >"$out" <<'SCRIPT'
#!/bin/sh
{
  printf 'CALL'
  for arg do
    case "$arg" in */mc-linux) arg=mc-linux ;; esac
    printf '\t%s' "$arg"
  done
  printf '\n'
} >>"${MC_REDEPLOY_TEST_LOG:?}"
if [ "${1:-}" = __onboard ] && [ "${2:-}" = agentsview ] && [ -n "${MC_REDEPLOY_TEST_AGENTSVIEW_EXIT:-}" ]; then
  exit "$MC_REDEPLOY_TEST_AGENTSVIEW_EXIT"
fi
if [ "${1:-}" = __onboard ] && [ "${2:-}" = home ]; then
  [ "${MC_BIN_DIR:-}" = "$(dirname -- "$0")" ] || exit 96
fi
if [ "${1:-}" = __redeploy-check ]; then
  [ -x "${2:-}" ] || exit 93
  if [ -n "${MC_REDEPLOY_TEST_CHECK_EXIT:-}" ]; then
    printf 'mc: The fake database check stopped.\n' >&2
    exit "$MC_REDEPLOY_TEST_CHECK_EXIT"
  fi
  if [ -z "${MC_REDEPLOY_TEST_DB:-}" ] || grep -q '^schema=current ' "$MC_REDEPLOY_TEST_DB"; then
    printf '{"state":"current"}\n'
  elif [ ! -e "$MC_REDEPLOY_TEST_DB" ]; then
    printf '{"state":"absent"}\n'
  else
    printf '{"state":"differs"}\n'
  fi
  exit 0
fi
if [ "${1:-}" = __onboard ] && [ "${2:-}" = home ] && [ -n "${MC_REDEPLOY_TEST_DB:-}" ]; then
  reset=0
  for arg do
    [ "$arg" != --allow-database-reset ] || reset=1
  done
  if [ "$reset" -eq 1 ]; then
    printf 'schema=current records=0\n' >"$MC_REDEPLOY_TEST_DB"
  elif [ ! -e "$MC_REDEPLOY_TEST_DB" ]; then
    printf 'mc: spine lost: the deployment database is absent\n' >&2
    printf '{"error":{"code":"database-absent","message":"spine lost: the deployment database is absent"}}\n'
    exit 1
  elif ! grep -q '^schema=current ' "$MC_REDEPLOY_TEST_DB"; then
    printf 'mc: the database schema differs from this release\n' >&2
    printf '{"error":{"code":"database-schema-differs","message":"the database schema differs from this release"}}\n'
    exit 1
  fi
  printf '{"ok":true,"sections":[{"section":"home","status":"ok"}]}\n'
fi
exit 0
SCRIPT
  chmod 755 "$out"
  exit 0
fi
exit 92
FAKE
  # `launchctl list` shows a com.culler. label while the file
  # MC_REDEPLOY_TEST_LOADED_FILE exists, and bootout removes that file. The list
  # also shows the label while the counter in MC_REDEPLOY_TEST_LIST_BUSY_FILE is
  # above zero (one decrement per list call). With both files unset, nothing is
  # loaded.
  cat >"$bin/launchctl" <<'FAKE'
#!/bin/sh
{ printf 'CALL\tlaunchctl'; for arg do printf '\t%s' "$arg"; done; printf '\n'; } >>"${MC_REDEPLOY_TEST_LOG:?}"
if [ "${1:-}" = list ]; then
  calls=$(grep -c '^CALL	launchctl	list$' "$MC_REDEPLOY_TEST_LOG")
  if [ "$calls" = "${MC_REDEPLOY_TEST_LIST_FAIL_AT:-0}" ]; then
    printf 'Could not print domain: 141: Reentrancy avoided\n' >&2
    exit 141
  fi
fi
if [ "${1:-}" = print ] && [ -n "${MC_REDEPLOY_TEST_PRINT_EXIT:-}" ]; then
  if [ "$MC_REDEPLOY_TEST_PRINT_EXIT" = 113 ]; then
    printf 'Could not find service in domain.\n' >&2
  else
    printf 'Could not print domain: 141: Reentrancy avoided\n' >&2
  fi
  exit "$MC_REDEPLOY_TEST_PRINT_EXIT"
fi
if [ "${1:-}" = bootout ] && [ -n "${MC_REDEPLOY_TEST_BOOTOUT_EXIT:-}" ]; then
  printf 'Boot-out failed: service control refused the request.\n' >&2
  exit "$MC_REDEPLOY_TEST_BOOTOUT_EXIT"
fi
if [ "${1:-}" = bootout ] && [ -n "${MC_REDEPLOY_TEST_LOADED_FILE:-}" ]; then
  rm -f "$MC_REDEPLOY_TEST_LOADED_FILE"
fi
if [ "${1:-}" = list ] && [ -n "${MC_REDEPLOY_TEST_LOADED_FILE:-}" ] && [ -e "$MC_REDEPLOY_TEST_LOADED_FILE" ]; then
  printf -- '-\t0\tcom.culler.abc.resident\n'
fi
if [ "${1:-}" = list ] && [ -n "${MC_REDEPLOY_TEST_LIST_BUSY_FILE:-}" ]; then
  left=$(cat "$MC_REDEPLOY_TEST_LIST_BUSY_FILE" 2>/dev/null || printf '0')
  if [ "$left" -gt 0 ]; then
    printf '%s\n' "$((left - 1))" >"$MC_REDEPLOY_TEST_LIST_BUSY_FILE"
    printf -- '-\t0\tcom.culler.abc.resident\n'
  fi
fi
exit 0
FAKE
  cat >"$bin/docker" <<'FAKE'
#!/bin/sh
{ printf 'CALL\tdocker\t%s\n' "${1:-}"; } >>"${MC_REDEPLOY_TEST_LOG:?}"
case "${1:-}" in
  info|build|run) exit "${MC_REDEPLOY_TEST_DOCKER_EXIT:-0}" ;;
  image)
    [ "${2:-}" = inspect ] || exit 94
    if [ -z "${MC_REDEPLOY_TEST_IMAGE_RELEASE:-}" ]; then
      printf 'Error: No such image: mc-prod\n' >&2
      exit 1
    fi
    printf '%s\n' "$MC_REDEPLOY_TEST_IMAGE_RELEASE"
    exit 0 ;;
esac
exit 94
FAKE
  cat >"$bin/uname" <<'FAKE'
#!/bin/sh
printf 'Linux\n'
FAKE
  # `tty -s` reports a terminal on stdin only when MC_REDEPLOY_TEST_TTY is 1.
  cat >"$bin/tty" <<'FAKE'
#!/bin/sh
[ "${1:-}" = -s ] || exit 95
[ "${MC_REDEPLOY_TEST_TTY:-0}" = 1 ]
FAKE
  chmod 755 "$bin/mise" "$bin/launchctl" "$bin/docker" "$bin/uname" "$bin/tty"
  # The case PATH holds only the stubs, /usr/bin, and /bin. An image can keep
  # git or python3 elsewhere, for example under /usr/local/bin.
  for tool in git python3; do
    found=$(command -v "$tool" 2>/dev/null) || continue
    case "$found" in
      /usr/bin/*|/bin/*) ;;
      /*) ln -s "$found" "$bin/$tool" ;;
    esac
  done
}

release=$(git -C "$ROOT" rev-parse --verify 'HEAD^{commit}')

# --- happy path: the whole sequence, in order -------------------------------
case_root="$TMP/happy"
make_fakes "$case_root/bin"
mkdir -p "$case_root/home/Library/LaunchAgents" "$case_root/mc-bin"
touch "$case_root/home/Library/LaunchAgents/com.culler.abc.resident.plist" \
      "$case_root/home/Library/LaunchAgents/com.culler.abc.dashboard.plist"
printf 'old release\n' >"$case_root/mc-bin/mc"
printf 'schema=current records=5\n' >"$case_root/spine.db"
log="$case_root/calls.log"
: >"$log"
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_BIN_DIR="$case_root/mc-bin" MC_REDEPLOY_TEST_LOG="$log" MC_REDEPLOY_TEST_DB="$case_root/spine.db" \
  sh "$ROOT/redeploy.sh" >"$case_root/out" 2>&1 || { cat "$case_root/out" >&2; fail "happy path did not complete"; }
[ "$(cat "$case_root/spine.db")" = "schema=current records=5" ] || fail "a redeploy changed a database with the current schema"
grep -F -- "== release $release" "$case_root/out" >/dev/null || fail "the release commit was not stated"
expected=$(printf 'CALL\tlaunchctl\tlist\nCALL\tdocker\timage\nCALL\t__redeploy-check\tmc-linux\nCALL\t__onboard\tagentsview\nCALL\tlaunchctl\tbootout\tgui/%s/com.culler.abc.dashboard\nCALL\tlaunchctl\tbootout\tgui/%s/com.culler.abc.resident\nCALL\tlaunchctl\tlist\nCALL\tlaunchctl\tlist\nCALL\tdocker\tbuild\nCALL\tdocker\tbuild\nCALL\tdocker\trun\nCALL\tdocker\trun\nCALL\t__onboard\thome\t--release-source\t%s/runner\t--host-release-source\t%s\nCALL\t__onboard\ttunables\nCALL\t__onboard\tsupervision\t--activate\t--reconfigure\nCALL\tdoctor' \
  "$(id -u)" "$(id -u)" "$ROOT" "$ROOT")
actual=$(grep -v '^CALL	docker	info$' "$log")
[ "$actual" = "$expected" ] || {
  printf 'expected:\n%s\nactual:\n%s\n' "$expected" "$actual" >&2
  fail "the redeploy sequence is not build -> image label -> database check -> agentsview -> stop -> Project image -> runtime image -> Project browser -> runtime browser -> home -> tunables -> supervision -> doctor"
}
grep -q 'MC_REDEPLOY_TEST_LOG' "$case_root/mc-bin/mc" || fail "the new mc was not installed over the old one"

# --- rerun: the same sequence again, nothing refuses --------------------------
: >"$log"
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_BIN_DIR="$case_root/mc-bin" MC_REDEPLOY_TEST_LOG="$log" \
  sh "$ROOT/redeploy.sh" >"$case_root/out2" 2>&1 || { cat "$case_root/out2" >&2; fail "rerun did not complete"; }
[ "$(grep -v '^CALL	docker	info$' "$log")" = "$expected" ] || fail "rerun changed the sequence"

# The default executable directory reaches the private home operation.
case_root="$TMP/default-bin"
make_fakes "$case_root/bin"
mkdir -p "$case_root/home"
log="$case_root/calls.log"
: >"$log"
env -u MC_BIN_DIR PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_REDEPLOY_TEST_LOG="$log" \
  sh "$ROOT/redeploy.sh" >"$case_root/out" 2>&1 || { cat "$case_root/out" >&2; fail "the default executable directory did not reach the home operation"; }
[ -x "$case_root/home/.local/bin/mc" ] || fail "the default executable directory was not used"

# --- bootout race: the labels linger after bootout; the script waits ----------
# `launchctl list` stays busy for the first two polls. The transcript pins that
# the script polls until the label clears and only then reaches the image
# build: two busy polls, one clear poll, then build-prod's own check.
case_root="$TMP/bootout-race"
make_fakes "$case_root/bin"
mkdir -p "$case_root/home/Library/LaunchAgents" "$case_root/mc-bin"
touch "$case_root/home/Library/LaunchAgents/com.culler.abc.resident.plist" \
      "$case_root/home/Library/LaunchAgents/com.culler.abc.dashboard.plist"
printf 'old release\n' >"$case_root/mc-bin/mc"
log="$case_root/calls.log"
: >"$log"
printf '3\n' >"$case_root/busy"
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_BIN_DIR="$case_root/mc-bin" MC_REDEPLOY_TEST_LOG="$log" \
  MC_REDEPLOY_TEST_LIST_BUSY_FILE="$case_root/busy" \
  sh "$ROOT/redeploy.sh" >"$case_root/out" 2>&1 || { cat "$case_root/out" >&2; fail "a lingering bootout was not waited out"; }
expected_race=$(printf 'CALL\tlaunchctl\tlist\nCALL\tdocker\timage\nCALL\t__redeploy-check\tmc-linux\nCALL\t__onboard\tagentsview\nCALL\tlaunchctl\tbootout\tgui/%s/com.culler.abc.dashboard\nCALL\tlaunchctl\tbootout\tgui/%s/com.culler.abc.resident\nCALL\tlaunchctl\tlist\nCALL\tlaunchctl\tlist\nCALL\tlaunchctl\tlist\nCALL\tlaunchctl\tlist\nCALL\tdocker\tbuild\nCALL\tdocker\tbuild\nCALL\tdocker\trun\nCALL\tdocker\trun\nCALL\t__onboard\thome\t--release-source\t%s/runner\t--host-release-source\t%s\nCALL\t__onboard\ttunables\nCALL\t__onboard\tsupervision\t--activate\t--reconfigure\nCALL\tdoctor' \
  "$(id -u)" "$(id -u)" "$ROOT" "$ROOT")
actual=$(grep -v '^CALL	docker	info$' "$log")
[ "$actual" = "$expected_race" ] || {
  printf 'expected:\n%s\nactual:\n%s\n' "$expected_race" "$actual" >&2
  fail "the image build did not wait for the bootout to complete"
}

# --- bootout never completes: the wait expires with the existing message ------
case_root="$TMP/bootout-stuck"
make_fakes "$case_root/bin"
mkdir -p "$case_root/home/Library/LaunchAgents" "$case_root/mc-bin"
touch "$case_root/home/Library/LaunchAgents/com.culler.abc.resident.plist"
printf 'old release\n' >"$case_root/mc-bin/mc"
log="$case_root/calls.log"
: >"$log"
printf '999\n' >"$case_root/busy"
if env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_BIN_DIR="$case_root/mc-bin" MC_REDEPLOY_TEST_LOG="$log" \
  MC_REDEPLOY_TEST_LIST_BUSY_FILE="$case_root/busy" \
  sh "$ROOT/redeploy.sh" >"$case_root/out" 2>&1; then
  fail "a deployment that never unloaded was reported as a deploy"
fi
grep -F "redeploy.sh: a supervised Culler deployment is loaded." "$case_root/out" >/dev/null || \
  { cat "$case_root/out" >&2; fail "the expired wait did not fail with the existing message"; }
grep -q 'CALL	docker	build' "$log" && fail "the expired wait still built the image"
[ "$(cat "$case_root/mc-bin/mc")" = "old release" ] || fail "the expired wait replaced the installed mc"

# --- image build fails: units stopped, old release untouched, clear message ---
case_root="$TMP/image-fails"
make_fakes "$case_root/bin"
mkdir -p "$case_root/home" "$case_root/mc-bin"
printf 'old release\n' >"$case_root/mc-bin/mc"
log="$case_root/calls.log"
: >"$log"
if env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_BIN_DIR="$case_root/mc-bin" MC_REDEPLOY_TEST_LOG="$log" MC_REDEPLOY_TEST_DOCKER_EXIT=1 \
  sh "$ROOT/redeploy.sh" >"$case_root/out" 2>&1; then
  fail "a failed image build was reported as a deploy"
fi
grep -F "production image build failed" "$case_root/out" >/dev/null || { cat "$case_root/out" >&2; fail "image failure was not named"; }
[ "$(cat "$case_root/mc-bin/mc")" = "old release" ] || fail "a failed image build replaced the installed mc"
grep -q 'CALL	__onboard	home' "$log" && fail "a failed image build still ran onboarding"
grep -q 'supervision' "$log" && fail "a failed image build still activated supervision"

# --- AgentsView install fails: the units keep running, clear message ---------
case_root="$TMP/agentsview-fails"
make_fakes "$case_root/bin"
mkdir -p "$case_root/home/Library/LaunchAgents" "$case_root/mc-bin"
touch "$case_root/home/Library/LaunchAgents/com.culler.abc.resident.plist" \
      "$case_root/home/Library/LaunchAgents/com.culler.abc.dashboard.plist"
printf 'old release\n' >"$case_root/mc-bin/mc"
log="$case_root/calls.log"
: >"$log"
if env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_BIN_DIR="$case_root/mc-bin" MC_REDEPLOY_TEST_LOG="$log" MC_REDEPLOY_TEST_AGENTSVIEW_EXIT=1 \
  sh "$ROOT/redeploy.sh" >"$case_root/out" 2>&1; then
  fail "a failed AgentsView install was reported as a deploy"
fi
grep -F "redeploy.sh: the AgentsView install stopped and left the deployment running; rerun after fixing what it names" "$case_root/out" >/dev/null || \
  { cat "$case_root/out" >&2; fail "the AgentsView failure was not named"; }
[ "$(cat "$log")" = "$(printf 'CALL\tlaunchctl\tlist\nCALL\tdocker\timage\nCALL\t__redeploy-check\tmc-linux\nCALL\t__onboard\tagentsview')" ] || \
  { cat "$log" >&2; fail "a failed AgentsView install still stopped, built, or deployed"; }
[ "$(cat "$case_root/mc-bin/mc")" = "old release" ] || fail "a failed AgentsView install replaced the installed mc"

# Service errors stop before the image build or executable install.
for width in 80 40; do
  for color in color plain; do
    for scenario in preflight database poll image bootout bootout-check absent; do
      case_root="$TMP/service-$scenario-$width-$color"
      make_fakes "$case_root/bin"
      mkdir -p "$case_root/home/Library/LaunchAgents" "$case_root/mc-bin"
      touch "$case_root/home/Library/LaunchAgents/com.culler.abc.resident.plist"
      printf 'old release\n' >"$case_root/mc-bin/mc"
      database='schema=current records=5'
      [ "$scenario" != database ] || database='schema=previous records=5'
      printf '%s\n' "$database" >"$case_root/spine.db"
      log="$case_root/calls.log"
      : >"$log"
      list_fail=0 bootout_exit= print_exit= loaded= no_color=1 force_color=0
      [ "$color" = plain ] || { no_color= force_color=1; }
      case "$scenario" in
        preflight) list_fail=1 ;;
        database|poll) list_fail=2 ;;
        image) list_fail=3 ;;
        bootout)
          bootout_exit=5 loaded="$case_root/loaded"
          touch "$loaded" ;;
        bootout-check) bootout_exit=5 print_exit=141 ;;
        absent) bootout_exit=3 print_exit=113 ;;
      esac
      status=0
      env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
        MC_BIN_DIR="$case_root/mc-bin" MC_REDEPLOY_TEST_LOG="$log" \
        MC_REDEPLOY_TEST_DB="$case_root/spine.db" \
        MC_REDEPLOY_TEST_LOADED_FILE="$loaded" \
        MC_REDEPLOY_TEST_LIST_FAIL_AT="$list_fail" \
        MC_REDEPLOY_TEST_BOOTOUT_EXIT="$bootout_exit" MC_REDEPLOY_TEST_PRINT_EXIT="$print_exit" \
        COLUMNS="$width" TERM=xterm-256color NO_COLOR="$no_color" FORCE_COLOR="$force_color" \
        sh "$ROOT/redeploy.sh" >"$case_root/out" 2>&1 || status=$?
      if [ "$scenario" = absent ]; then
        [ "$status" -eq 0 ] || { cat "$case_root/out" >&2; fail "an absent service blocked the redeploy"; }
        grep -q 'already stopped' "$case_root/out" || fail "the verified absent service was not reported"
        continue
      fi
      [ "$status" -ne 0 ] || fail "the $scenario error permitted the redeploy"
      [ "$(cat "$case_root/mc-bin/mc")" = "old release" ] || fail "the $scenario error replaced the executable"
      [ "$(cat "$case_root/spine.db")" = "$database" ] || fail "the $scenario error changed the database"
      grep -Eq '^CALL	(docker	build|__onboard	(home|tunables|supervision)|doctor)' "$log" && \
        fail "the $scenario error permitted a deployment change"
      grep -q 'already stopped' "$case_root/out" && fail "the $scenario error reported a stopped service"
      if [ "$scenario" = preflight ]; then
        [ "$(cat "$log")" = "$(printf 'CALL\tlaunchctl\tlist')" ] || fail "the preflight error ran a later step"
        grep -q '^== build:' "$case_root/out" && fail "the preflight error started a build"
      fi
      if [ "$scenario" = bootout ]; then
        [ -e "$loaded" ] || fail "the failed stop removed the service"
        tr '\n' ' ' <"$case_root/out" | tr -s ' ' | grep -q 'Boot-out failed:' || fail "the stop error lost its diagnostic"
      else
        grep -q 'Reentrancy avoided' "$case_root/out" || fail "the service check lost its diagnostic"
      fi
      sed -n '/^redeploy.sh: Stopped./,$p' "$case_root/out" >"$case_root/message"
      long=$(awk -v w="$width" 'length($0) > w' "$case_root/message")
      [ -z "$long" ] || fail "the $scenario message exceeds $width columns: $long"
      action='Open Terminal in this checkout and run ./redeploy.sh again.'
      if [ "$scenario" = image ]; then
        action='Ask it to resolve the error before you run ./redeploy.sh again.'
        grep -q 'units are stopped' "$case_root/out" && fail "the image guard error claimed stopped services"
      fi
      tr '\n' ' ' <"$case_root/message" | tr -s ' ' | grep -F "$action" >/dev/null || \
        fail "the $scenario message omits the next action"
    done
  done
done

# --- database cases: one fake deployment per case -----------------------------
setup_case() {
  case_root="$TMP/$1"
  make_fakes "$case_root/bin"
  mkdir -p "$case_root/home/Library/LaunchAgents" "$case_root/mc-bin"
  touch "$case_root/home/Library/LaunchAgents/com.culler.abc.resident.plist"
  printf 'old release\n' >"$case_root/mc-bin/mc"
  log="$case_root/calls.log"
  : >"$log"
}
# run_redeploy reads its standard input from the caller. TTY and WIDTH select
# the fake terminal state and the message width. LOADED=1 starts the case with
# a loaded unit. CHECK_EXIT selects a database check that refuses or cannot run.
# CASE_MC_HOME sets MC_HOME; an empty value selects the default home.
# The cases assign these variables before the call, because an assignment that
# prefixes a function call can persist after the call.
LOADED=0 CHECK_EXIT= CASE_MC_HOME=
run_redeploy() {
  loaded=
  if [ "${LOADED:-0}" = 1 ]; then
    loaded="$case_root/loaded"
    : >"$loaded"
  fi
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
    MC_BIN_DIR="$case_root/mc-bin" MC_REDEPLOY_TEST_LOG="$log" \
    MC_REDEPLOY_TEST_DB="$case_root/spine.db" MC_REDEPLOY_TEST_TTY="${TTY:-0}" \
    MC_REDEPLOY_TEST_IMAGE_RELEASE="${IMAGE_RELEASE:-}" \
    MC_REDEPLOY_TEST_LOADED_FILE="$loaded" \
    MC_REDEPLOY_TEST_CHECK_EXIT="${CHECK_EXIT:-}" MC_HOME="${CASE_MC_HOME:-}" \
    COLUMNS="${WIDTH:-80}" sh "$ROOT/redeploy.sh" "$@" >"$case_root/out" 2>&1
}
contains() { tr '\n' ' ' <"$1" | tr -s ' ' | grep -F -- "$2" >/dev/null; }
stop_message() { sed -n '/^redeploy\.sh: Stopped\./,$p' "$case_root/out"; }
# fits_width fails when a line of the case output is longer than WIDTH.
fits_width() {
  long=$(awk -v w="$WIDTH" 'length($0) > w' "$case_root/out")
  [ -z "$long" ] || fail "$1 exceeds $WIDTH columns: $long"
}
names_mc_command() { stop_message | grep -Eq '(^|[^[:alnum:]_./-])mc([[:space:]]|$)'; }
release_call=$(printf 'CALL\t__onboard\thome\t--release-source\t%s/runner\t--host-release-source\t%s' "$ROOT" "$ROOT")
reset_call=$(printf 'CALL\t__onboard\thome\t--allow-database-reset\t--release-source\t%s/runner\t--host-release-source\t%s' "$ROOT" "$ROOT")
# changed_nothing proves an early stop: no AgentsView install, no unit stop, no
# image build, no install, no deploy step, and the database unchanged.
changed_nothing() {
  [ "$(cat "$case_root/spine.db" 2>/dev/null || printf absent)" = "$1" ] || fail "the early stop ($2) changed the database"
  [ "$(cat "$case_root/mc-bin/mc")" = "old release" ] || fail "the early stop ($2) replaced the installed mc"
  actual=$(grep -v '^CALL	launchctl	list$' "$log")
  [ "$actual" = "$(printf 'CALL\tdocker\timage\nCALL\t__redeploy-check\tmc-linux')" ] || \
    { cat "$log" >&2; fail "the early stop ($2) still installed, stopped, built, or deployed"; }
  [ -z "$loaded" ] || [ -e "$loaded" ] || fail "the early stop ($2) stopped a loaded unit"
}

# --- schema difference with loaded units: the early stop changes nothing -----
for WIDTH in 80 40; do
  setup_case "early-differs-loaded-$WIDTH"
  printf 'schema=previous records=5\n' >"$case_root/spine.db"
  LOADED=1
  if run_redeploy </dev/null; then
    fail "a database with a different schema was reported as a deploy"
  fi
  LOADED=0
  changed_nothing "schema=previous records=5" "loaded units"
  for text in "redeploy.sh: Stopped. The deployed database has a different schema from this release. Nothing changed." \
              "The Culler services still run the deployed release with its database." \
              "Choose one action: - Replace the database with an empty database: ./redeploy.sh --reset-database" \
              "- Keep the database records: no action is necessary. The deployed release continues to run."; do
    contains "$case_root/out" "$text" || { cat "$case_root/out" >&2; fail "the early schema stop omits: $text"; }
  done
  contains "$case_root/out" "git checkout" && fail "the early stop with loaded units names a checkout"
  contains "$case_root/out" "are installed" && fail "the early stop states an install"
  names_mc_command && { stop_message >&2; fail "the early stop names an mc command"; }
  long=$(stop_message | awk -v w="$WIDTH" 'length($0) > w')
  [ -z "$long" ] || fail "the early stop exceeds $WIDTH columns: $long"
done

# --- schema difference after a stop: the early stop or the deploy step -------
# Each record case names where the previous commit comes from:
#   supervision: the supervision record names it; the image label names this
#     release, as after an earlier stop of this release;
#   image: no supervision record; the image label names it;
#   none: the supervision record names this release; no image exists.
# The early stage runs the database check. The late stage has a database
# check that cannot run, so the deploy step stops after the units stop.
previous=0123456789abcdef0123456789abcdef01234567
for WIDTH in 80 40; do
  for record in supervision image none; do
    for stage in early late; do
      setup_case "schema-differs-$WIDTH-$record-$stage"
      printf 'schema=previous records=5\n' >"$case_root/spine.db"
      supervision="$case_root/home/.culler/supervision"
      case "$record" in
        supervision) recorded=$previous IMAGE_RELEASE=$release ;;
        image) recorded= IMAGE_RELEASE=$previous ;;
        none) recorded=$release IMAGE_RELEASE= ;;
      esac
      if [ -n "$recorded" ]; then
        mkdir -p "$supervision"
        printf '{\n  "mcPath": "/fake/mc",\n  "releaseBuildId": "%s"\n}\n' "$recorded" >"$supervision/resident.json"
      fi
      CHECK_EXIT=
      [ "$stage" = early ] || CHECK_EXIT=2
      if run_redeploy </dev/null; then
        fail "a database with a different schema was reported as a deploy"
      fi
      IMAGE_RELEASE= CHECK_EXIT=
      [ "$(cat "$case_root/spine.db")" = "schema=previous records=5" ] || fail "the schema stop changed the database"
      grep -q -- '--allow-database-reset' "$log" && fail "a redeploy without the reset option sent the reset authorization"
      grep -Eq 'CALL	__onboard	(tunables|supervision)|CALL	doctor' "$log" && fail "the schema stop still ran a later step"
      [ "$(grep -E 'CALL	docker	(image|build)' "$log" | head -n 1)" = "$(printf 'CALL\tdocker\timage')" ] || \
        { cat "$log" >&2; fail "the script did not read the image label before the image build"; }
      case "$record:$WIDTH" in
        none:*) keep="Keep the database records: redeploy.sh found no record of the previously deployed release. Check out the commit that you deployed last, then run: ./redeploy.sh" ;;
        *:80) keep="Keep the database records: deploy the previously deployed release again. Run these commands: git checkout $previous ./redeploy.sh" ;;
        *:40) keep="Keep the database records: deploy the previously deployed release again. Run these commands: git checkout 0123456789ab ./redeploy.sh" ;;
      esac
      if [ "$stage" = early ]; then
        changed_nothing "schema=previous records=5" "$record"
        cause="redeploy.sh: Stopped. The deployed database has a different schema from this release. Nothing changed."
        state="The Culler services are stopped. The deployment keeps its database."
      else
        grep -Fx -- "$release_call" "$log" >/dev/null || { cat "$log" >&2; fail "the late schema stop did not come from the kept-database deploy step"; }
        grep -q 'MC_REDEPLOY_TEST_LOG' "$case_root/mc-bin/mc" || fail "the stop message states an installed release that was not installed"
        contains "$case_root/out" "the database check could not run: The fake database check stopped." || \
          { cat "$case_root/out" >&2; fail "the script did not state the database check that could not run"; }
        cause="redeploy.sh: Stopped. The deployed database has a different schema from this release."
        state="The database is unchanged. The Culler services are stopped. This release's command-line tool and container image are installed."
      fi
      for text in "$cause" "$state" \
                  "Choose one action: - Replace the database with an empty database: ./redeploy.sh --reset-database" \
                  "$keep"; do
        contains "$case_root/out" "$text" || { cat "$case_root/out" >&2; fail "the $stage schema stop ($record) omits: $text"; }
      done
      contains "$case_root/out" "git checkout $release" && fail "the schema stop ($record) names this release as the previous release"
      names_mc_command && { stop_message >&2; fail "the stop message names an mc command"; }
      grep -F 'mc: the database schema differs' "$case_root/out" >/dev/null && fail "the stop repeats the raw mc refusal"
      long=$(stop_message | awk -v w="$WIDTH" 'length($0) > w')
      [ -z "$long" ] || fail "the stop message exceeds $WIDTH columns: $long"
    done
  done
done
WIDTH=80

# --- absent database: the early stop or the deploy step creates no database --
for stage in early late; do
  setup_case "database-absent-$stage"
  CHECK_EXIT=
  [ "$stage" = early ] || CHECK_EXIT=2
  LOADED=1
  if run_redeploy </dev/null; then
    fail "an absent database was reported as a deploy"
  fi
  LOADED=0 CHECK_EXIT=
  [ ! -e "$case_root/spine.db" ] || fail "the absent-database stop created a database"
  if [ "$stage" = early ]; then
    changed_nothing absent "absent database"
    contains "$case_root/out" "redeploy.sh: Stopped. The deployed database is absent. Nothing changed." || { cat "$case_root/out" >&2; fail "the absent database was not named"; }
    contains "$case_root/out" "The Culler services still run the deployed release. Next action:" || \
      { cat "$case_root/out" >&2; fail "the early absent-database stop omits the deployment state"; }
  else
    contains "$case_root/out" "redeploy.sh: Stopped. The deployed database is absent." || { cat "$case_root/out" >&2; fail "the absent database was not named"; }
    contains "$case_root/out" "The deployment still has no database. The Culler services are stopped." || fail "the absent-database stop omits the deployment state"
  fi
  contains "$case_root/out" "Next action: - Create an empty database: ./redeploy.sh --reset-database" || fail "the absent-database stop omits the reset option"
  names_mc_command && fail "the absent-database stop names an mc command"
done

# --- refused database check: nothing changes, the service state is named -----
for WIDTH in 80 40; do
  for units in loaded stopped; do
    setup_case "check-refused-$units-$WIDTH"
    printf 'schema=current records=5\n' >"$case_root/spine.db"
    CHECK_EXIT=1 LOADED=0
    [ "$units" = stopped ] || LOADED=1
    if run_redeploy </dev/null; then
      fail "a refused database check was reported as a deploy"
    fi
    CHECK_EXIT= LOADED=0
    changed_nothing "schema=current records=5" "refused check"
    if [ "$units" = loaded ]; then
      services="The Culler services still run the deployed release with its database."
    else
      services="The Culler services are stopped. The deployment keeps its database."
    fi
    for text in "redeploy.sh: Stopped. The database check refused the deployment. Nothing changed." \
                "The fake database check stopped." "$services" \
                "Next action: - Give this message to your coding harness. Ask it to repair the deployment with the onboard skill or install.sh."; do
      contains "$case_root/out" "$text" || { cat "$case_root/out" >&2; fail "the refused check ($units) omits: $text"; }
    done
    contains "$case_root/out" "run this command again" && fail "the refused check ($units) asks for a rerun"
    names_mc_command && { stop_message >&2; fail "the refused check names an mc command"; }
    long=$(stop_message | awk -v w="$WIDTH" 'length($0) > w')
    [ -z "$long" ] || fail "the refused check ($units) exceeds $WIDTH columns: $long"
  done
done
WIDTH=80

# --- confirmed reset: an empty database, then the configured settings --------
# The default case names the default deployment home. The named case sets
# MC_HOME outside the home directory.
for target in default named; do
  for WIDTH in 80 40; do
    setup_case "reset-confirmed-$target-$WIDTH"
    printf 'schema=previous records=5\n' >"$case_root/spine.db"
    CASE_MC_HOME= shown="~/.culler"
    if [ "$target" = named ]; then
      CASE_MC_HOME=/srv/culler shown=/srv/culler
    fi
    printf 'reset\n' | TTY=1 run_redeploy --reset-database || { cat "$case_root/out" >&2; fail "a confirmed reset did not complete"; }
    CASE_MC_HOME=
    [ "$(cat "$case_root/spine.db")" = "schema=current records=0" ] || fail "a confirmed reset did not replace the database"
    expected_reset=$(printf 'CALL\tlaunchctl\tlist\nCALL\tdocker\timage\nCALL\t__onboard\tagentsview\nCALL\tlaunchctl\tbootout\tgui/%s/com.culler.abc.resident\nCALL\tlaunchctl\tlist\nCALL\tlaunchctl\tlist\nCALL\tdocker\tbuild\nCALL\tdocker\tbuild\nCALL\tdocker\trun\nCALL\tdocker\trun\n%s\nCALL\t__onboard\ttunables\nCALL\t__onboard\tsurfaces\nCALL\t__onboard\tsupervision\t--activate\t--reconfigure\nCALL\tdoctor' \
      "$(id -u)" "$reset_call")
    actual=$(grep -v '^CALL	docker	info$' "$log")
    [ "$actual" = "$expected_reset" ] || {
      printf 'expected:\n%s\nactual:\n%s\n' "$expected_reset" "$actual" >&2
      fail "a confirmed reset did not run the redeploy sequence with the reset authorization and the alert routes"
    }
    for text in "The database reset replaces the database of the deployment at $shown with an empty database." \
                "The reset deletes all database records, for example these records:" \
                "- Homie conversations." "- The settings outside config.toml." \
                "The reset keeps these items:" "- The Project repositories and worktrees." \
                "- The credentials and config.toml." "- The deployment identity." \
                "After the reset, redeploy.sh copies the config.toml settings into the new database, including the Discord alert destinations. Then add each Project again in the dashboard." \
                "Type reset to replace the database. Press Return to cancel. Type reset:" \
                "the deployment database is a new empty database"; do
      contains "$case_root/out" "$text" || { cat "$case_root/out" >&2; fail "the confirmed reset ($target) omits: $text"; }
    done
    contains "$case_root/out" "Homie bindings" && fail "the reset confirmation names Homie bindings"
    contains "$case_root/out" "Alert routes" && fail "the reset confirmation names alert routes"
    prompt_line=$(grep -n '^Type reset: ' "$case_root/out" | cut -d: -f1)
    release_line=$(grep -n '^== release ' "$case_root/out" | cut -d: -f1)
    [ "$prompt_line" -lt "$release_line" ] || fail "the reset confirmation did not precede the first build step"
    sed -n '1,/^Type reset: /p' "$case_root/out" >"$case_root/confirmation"
    long=$(awk -v w="$WIDTH" 'length($0) > w' "$case_root/confirmation")
    [ -z "$long" ] || fail "the reset confirmation exceeds $WIDTH columns: $long"
  done
done
WIDTH=80

# --- kept database: no alert route copy -----------------------------------------
grep -q 'CALL	__onboard	surfaces' "$TMP/happy/calls.log" && fail "a redeploy that kept the database copied the alert routes"

# --- refused reset: no terminal, a wrong word, or no answer change nothing ---
for refusal in no-terminal wrong-word empty-answer no-answer; do
  setup_case "reset-$refusal"
  printf 'schema=previous records=5\n' >"$case_root/spine.db"
  case "$refusal" in
    no-terminal) printf 'reset\n' | TTY=0 run_redeploy --reset-database && fail "a reset without a terminal was accepted" ;;
    wrong-word) printf 'yes\n' | TTY=1 run_redeploy --reset-database && fail "a reset with a wrong word was accepted" ;;
    empty-answer) printf '\n' | TTY=1 run_redeploy --reset-database && fail "a reset with an empty answer was accepted" ;;
    no-answer) TTY=1 run_redeploy --reset-database </dev/null && fail "a reset without an answer was accepted" ;;
  esac
  [ ! -s "$log" ] || { cat "$log" >&2; fail "a refused reset ($refusal) still ran a step"; }
  [ "$(cat "$case_root/spine.db")" = "schema=previous records=5" ] || fail "a refused reset ($refusal) changed the database"
  [ "$(cat "$case_root/mc-bin/mc")" = "old release" ] || fail "a refused reset ($refusal) replaced the installed mc"
  grep -q '^== release ' "$case_root/out" && fail "a refused reset ($refusal) started the redeploy"
  contains "$case_root/out" "Nothing changed." || { cat "$case_root/out" >&2; fail "a refused reset ($refusal) did not state the unchanged deployment"; }
done
contains "$TMP/reset-no-terminal/out" "needs your confirmation at a terminal" || fail "the terminal requirement was not named"
for refusal in empty-answer no-answer; do
  contains "$TMP/reset-$refusal/out" "Type reset: The reset is cancelled. Nothing changed." || \
    { cat "$TMP/reset-$refusal/out" >&2; fail "a cancelled reset ($refusal) was not stated as a cancel"; }
  grep -q '^redeploy\.sh:' "$TMP/reset-$refusal/out" && fail "a cancelled reset ($refusal) reads as an error"
done
contains "$TMP/reset-wrong-word/out" "You typed yes, not reset. Nothing changed. To replace the database, run this command again and type reset: ./redeploy.sh --reset-database" || \
  { cat "$TMP/reset-wrong-word/out" >&2; fail "the wrong word was not named"; }
grep -q 'Type reset' "$TMP/reset-no-terminal/out" && fail "a run without a terminal asked for the confirmation"

# --- help: the text fits the message width ---------------------------------------
for WIDTH in 80 40; do
  setup_case "help-$WIDTH"
  run_redeploy --help </dev/null || { cat "$case_root/out" >&2; fail "the help did not complete"; }
  [ ! -s "$log" ] || fail "the help ran a step"
  for text in "Usage: ./redeploy.sh [--reset-database]" \
              "redeploy.sh moves the live Culler deployment to this checkout. The deployment keeps its database. A schema difference stops redeploy.sh and keeps the database." \
              "Option: - --reset-database: Replace the database with an empty database. You confirm the reset at a terminal."; do
    contains "$case_root/out" "$text" || { cat "$case_root/out" >&2; fail "the help omits: $text"; }
  done
  fits_width "the help"
done
WIDTH=80

# --- unknown option: nothing runs ---------------------------------------------
setup_case unknown-option
printf 'schema=current records=5\n' >"$case_root/spine.db"
run_redeploy --reset </dev/null && fail "an unknown option was accepted"
[ ! -s "$log" ] || fail "an unknown option still ran a step"
contains "$case_root/out" "redeploy.sh: unknown option --reset" || fail "the unknown option was not named"

printf 'redeploy.sh front door: ok\n'
