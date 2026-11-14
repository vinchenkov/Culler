#!/bin/sh
# The source entry points refuse incomplete public updates before effects.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SYSTEM_PATH=$PATH
TMP=$(mktemp -d "${TMPDIR:-/tmp}/culler-source-update-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT
fail() { printf 'source-update-guard_test.sh: %s\n' "$*" >&2; exit 1; }
mkdir -p "$TMP/home/updates" "$TMP/bin"
GUARD_HOME="$TMP/home"
GUARD_TRACE="$TMP/effects"
export GUARD_HOME GUARD_TRACE

# Python supplies the portable test double. The source guard uses system plutil.
cat >"$TMP/bin/plutil" <<'FAKE'
#!/usr/bin/env python3
import json, sys
try:
    with open(sys.argv[-1]) as stream:
        record = json.load(stream)
    if sys.argv[1] == '-convert':
        sys.exit(0)
    value = record[sys.argv[2]]
    if isinstance(value, (dict, list, bool)) or value is None:
        sys.exit(1)
    print(value)
except (OSError, ValueError, KeyError, TypeError):
    sys.exit(1)
FAKE
chmod 755 "$TMP/bin/plutil"
PATH="$TMP/bin:$PATH"
export PATH
record="$GUARD_HOME/updates/transaction.json"
write_record() {
  rm -f "$record"
  printf '{"version":1,"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","phase":"%s"}\n' "$1" >"$record"
  chmod 600 "$record"
}
run_guard() {
  sh -eu -c '. "$1/scripts/source-update-guard.sh"; culler_require_source_update_idle "$GUARD_HOME"' guard "$ROOT"
}
expect_refusal() {
  if run_guard >"$TMP/output" 2>&1; then fail "$1 permitted the source operation"; fi
  grep -q 'Source deployment remains unchanged.' "$TMP/output" || fail "$1 omitted the unchanged state"
  grep -q 'Use culler update to recover.' "$TMP/output" || fail "$1 omitted the recovery action"
}
run_guard || fail 'an absent journal refused the source operation'
for phase in complete restored; do
  write_record "$phase"
  run_guard || fail "$phase refused the source operation"
done
mkdir "$GUARD_HOME/state"
request="$GUARD_HOME/state/install-maintenance.json"
for phase in complete restored; do
  write_record "$phase"
  printf '{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}' >"$request"
  chmod 600 "$request"
  expect_refusal "$phase with its activation request"
  printf '{"id":"ordinary-restart-1234"}' >"$request"
  run_guard || fail "$phase refused another lifecycle request"
  printf '{' >"$request"
  expect_refusal "$phase with an unreadable activation request"
  rm "$request"
done
write_record complete
ln -s "$TMP/absent" "$request"
expect_refusal 'an activation request symlink'
rm "$request"
for phase in stop prepare checkpoint replace verify committed restore unknown; do
  write_record "$phase"
  expect_refusal "$phase"
done
printf '{' >"$record"
expect_refusal 'a malformed journal'
printf '{"version":2,"phase":"complete"}' >"$record"
expect_refusal 'an unknown journal version'
write_record complete
chmod 644 "$record"
expect_refusal 'an unsafe journal mode'
write_record complete
ln "$record" "$TMP/linked-record"
expect_refusal 'a journal with another link'
rm "$TMP/linked-record"
rm "$record"
ln -s "$TMP/absent" "$record"
expect_refusal 'a journal symlink'
rm "$record"
mkdir "$record"
expect_refusal 'a journal directory'
rmdir "$record"

# A command call proves that the guard ran too late.
for command in git mise docker curl launchctl; do
  cat >"$TMP/bin/$command" <<'FAKE'
#!/bin/sh
printf '%s\n' "$0 $*" >>"$GUARD_TRACE"
exit 99
FAKE
  chmod 755 "$TMP/bin/$command"
done
write_record replace
for script in redeploy.sh redeploy-dashboard.sh scripts/install-source.sh; do
  if MC_HOME="$GUARD_HOME" sh "$ROOT/$script" >"$TMP/output" 2>&1; then
    fail "$script permitted an incomplete update"
  fi
  grep -q 'Culler has an incomplete update.' "$TMP/output" || fail "$script missed the update guard"
  [ ! -e "$GUARD_TRACE" ] || fail "$script called a tool before the guard"
  [ "$(find "$GUARD_HOME" -type f | wc -l | tr -d ' ')" = 1 ] || fail "$script wrote deployment files"
done
if MC_HOME="$GUARD_HOME" sh "$ROOT/install.sh" --dev --mc-home "$GUARD_HOME" >"$TMP/output" 2>&1; then
  fail 'install.sh --dev permitted an incomplete update'
fi
grep -q 'Culler has an incomplete update.' "$TMP/output" || fail 'install.sh --dev missed the update guard'
[ ! -e "$GUARD_TRACE" ] || fail 'install.sh --dev called a tool before the guard'
if [ "$(uname -s)" = Darwin ]; then
  PATH=$SYSTEM_PATH
  for phase in complete restored; do
    write_record "$phase"
    run_guard || fail "system plutil refused $phase"
  done
  write_record replace
  expect_refusal 'system plutil with an incomplete update'
  grep -q 'Culler has an incomplete update.' "$TMP/output" || fail 'system plutil did not read JSON'
  printf '{' >"$record"
  expect_refusal 'system plutil with a malformed journal'
fi
printf 'source-update-guard_test.sh: passed\n'
