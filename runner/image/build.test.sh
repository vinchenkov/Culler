#!/bin/sh
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/mc-build-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

fail() {
  printf 'build.test.sh: %s\n' "$*" >&2
  exit 1
}

mkdir -p "$TMP/bin"
cat >"$TMP/bin/mise" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >"${MC_TEST_MISE_LOG:?}"
exit 73
EOF
chmod 755 "$TMP/bin/mise"

run_variant() {
  name=$1
  shift
  if PATH="$TMP/bin:/usr/bin:/bin" MC_TEST_MISE_LOG="$TMP/$name.log" \
      "$HERE/build.sh" "$@" >"$TMP/$name.out" 2>&1; then
    fail "$name build unexpectedly completed with a stubbed compiler"
  fi
}

run_variant fake
run_variant real --real

grep -F -- '-tags test_fake_routing -ldflags -w -o ../runner/image/mc-fake-e2e-bin ./cmd/mc' "$TMP/fake.log" >/dev/null ||
  fail "fake build did not use its dedicated tagged artifact"
grep -F -- '-ldflags -w -o ../runner/image/mc-real-e2e-bin ./cmd/mc' "$TMP/real.log" >/dev/null ||
  fail "real build did not use its dedicated untagged artifact"
if grep -F -- 'test_fake_routing' "$TMP/real.log" >/dev/null; then
  fail "real build carried the fake routing tag"
fi
grep -F -- '--build-arg "MC_BIN=${mc_bin}"' "$HERE/build.sh" >/dev/null ||
  fail "Docker build does not receive the selected MC binary"

printf 'e2e image artifacts are variant-separated: ok\n'
