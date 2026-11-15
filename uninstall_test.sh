#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/culler-uninstall-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
mkdir -p "$TMP/tools" "$TMP/home"
cat >"$TMP/tools/mise" <<'EOF'
#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then
    cp "$UNINSTALL_TEST_BACKEND" "$2"
    chmod +x "$2"
    exit 0
  fi
  shift
done
exit 99
EOF
cat >"$TMP/backend" <<'EOF'
#!/bin/sh
set -eu
printf 'CALL\n' >>"$UNINSTALL_TEST_LOG"
printf '<%s>\n' "$@" >>"$UNINSTALL_TEST_LOG"
case "$*" in
  *--dry-run*)
    [ "${UNINSTALL_TEST_FAIL:-}" != preview ] || exit 1
    printf '{"dry_run":true,"host_buns":%s,"uninstalled":false}\n' "${UNINSTALL_TEST_HOST_BUNS:-[\"$HOME/.culler/bun\"]}" ;;
  *--confirm*) [ "${UNINSTALL_TEST_FAIL:-}" != remove ] || exit 1 ;;
esac
EOF
chmod +x "$TMP/tools/mise" "$TMP/backend"
export UNINSTALL_TEST_BACKEND="$TMP/backend" UNINSTALL_TEST_LOG="$TMP/calls"
export PATH="$TMP/tools:$PATH" HOME="$TMP/home"
fail() { printf 'uninstall_test.sh: %s\n' "$*" >&2; exit 1; }

"$ROOT/uninstall.sh" --yes --keep-data >"$TMP/output" 2>&1
[ "$(grep -c '^<--keep-data>$' "$TMP/calls")" -eq 2 ] || fail 'explicit data-preserving mode lost'
grep -F 'Culler software removed. Data kept.' "$TMP/output" >/dev/null || fail 'missing data-preserving completion'
grep -F 'Culler Bun removed.' "$TMP/output" >/dev/null || fail 'missing Bun removal line'
UNINSTALL_TEST_HOST_BUNS='[]' "$ROOT/uninstall.sh" --yes --keep-data >"$TMP/output" 2>&1
grep -F 'Culler software removed. Data kept.' "$TMP/output" >/dev/null || fail 'missing data-preserving completion without a Bun'
if grep -F 'Culler Bun removed.' "$TMP/output" >/dev/null; then fail 'Bun removal line printed without a Culler Bun'; fi
"$ROOT/uninstall.sh" --help >"$TMP/help" 2>&1 || fail 'help failed'
grep -F 'Both modes remove the Culler Bun.' "$TMP/help" >/dev/null || fail 'help omits the Bun removal'
if grep -F 'mise' "$TMP/help" >/dev/null; then fail 'help still names mise as a shared tool'; fi
: >"$TMP/calls"
"$ROOT/uninstall.sh" --full-wipe --dry-run --home "$TMP/home with spaces" \
  --workspace "$TMP/Project [one]" --workspace "$TMP/Project two" >"$TMP/output" 2>&1
[ "$(grep -c '^CALL$' "$TMP/calls")" -eq 1 ] || fail 'dry run performed removal'
grep -Fx "<$TMP/home with spaces>" "$TMP/calls" >/dev/null || fail 'home split'
grep -Fx "<$TMP/Project [one]>" "$TMP/calls" >/dev/null || fail 'workspace glob expanded'
grep -Fx "<$TMP/Project two>" "$TMP/calls" >/dev/null || fail 'repeat workspace lost'
for failure in preview remove; do
  if UNINSTALL_TEST_FAIL=$failure "$ROOT/uninstall.sh" --yes --full-wipe >"$TMP/output" 2>&1; then fail "$failure failure returned success"; fi
  if grep -F 'Culler software and data removed.' "$TMP/output" >/dev/null; then fail 'false completion'; fi
done
if "$ROOT/uninstall.sh" --keep-data --full-wipe >"$TMP/output" 2>&1; then fail 'conflicting modes accepted'; fi
if "$ROOT/uninstall.sh" --home --yes >"$TMP/output" 2>&1; then fail 'missing path accepted'; fi
: >"$TMP/calls"
if printf 'no\n' | "$ROOT/uninstall.sh" >"$TMP/output" 2>&1; then fail 'cancellation succeeded'; fi
[ "$(grep -c '^CALL$' "$TMP/calls")" -eq 1 ] || fail 'cancelled uninstall removed resources'
printf 'wipe\n' | "$ROOT/uninstall.sh" >"$TMP/output" 2>&1
grep -F 'Culler software and data removed.' "$TMP/output" >/dev/null || fail 'missing full-wipe completion'
grep -F 'Culler Bun removed.' "$TMP/output" >/dev/null || fail 'missing Bun removal line after the full wipe'
[ "$(grep -c '^<--full-wipe>$' "$TMP/calls")" -eq 3 ] || fail 'default did not select full wipe'
echo 'uninstall_test.sh: all contracts passed'
