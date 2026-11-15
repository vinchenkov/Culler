#!/bin/sh
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/mc-build-prod-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

# Runtime source preparation writes only into the temporary repository.
python3 "$HERE/../../scripts/test-source.py" "$HERE/../.." "$TMP/source"
HERE="$TMP/source/runner/image"

fail() {
  printf 'build-prod.test.sh: %s\n' "$*" >&2
  exit 1
}

mkdir -p "$TMP/bin"
# The PATH of each run below names only the stubs and the system directories.
# The build script needs python3, which some hosts keep in another directory.
python=$(command -v python3 || :)
case "$python" in
  ''|/usr/bin/python3|/bin/python3) ;;
  *) ln -s "$python" "$TMP/bin/python3" ;;
esac
case "$(MC_RELEASE_BUILD_ID=not-a-commit "$HERE/build-prod.sh" 2>&1 || :)" in
  *"MC_RELEASE_BUILD_ID must be a 40..64 lowercase-hex commit"*) ;;
  *) fail "invalid release identity was not refused" ;;
esac

cat >"$TMP/bin/mise" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >"${MC_TEST_MISE_LOG:?}"
exit 73
EOF
chmod 755 "$TMP/bin/mise"

release_id=0123456789abcdef0123456789abcdef01234567

# The supervision guard, both ways. `launchctl` is STUBBED rather than read from
# the machine on purpose: this suite must give the same verdict whether or not
# the developer's own deployment happens to be running, and the real binary sits
# in /bin, which the PATH below would otherwise reach.
cat >"$TMP/bin/launchctl" <<'EOF'
#!/bin/sh
if [ -n "${MC_TEST_LAUNCHCTL_EXIT:-}" ]; then
  printf 'Could not print domain: 141: Reentrancy avoided\n' >&2
  exit "$MC_TEST_LAUNCHCTL_EXIT"
fi
[ -n "${MC_TEST_SUPERVISED:-}" ] && printf '8156\t0\tcom.culler.deadbeef.resident\n'
exit 0
EOF
chmod 755 "$TMP/bin/launchctl"

for width in 80 40; do
  for color in color plain; do
    no_color=1 force_color=0
    [ "$color" = plain ] || { no_color= force_color=1; }
    output="$TMP/service-$width-$color.out"
    status=0
    PATH="$TMP/bin:/usr/bin:/bin" MC_TEST_LAUNCHCTL_EXIT=141 \
      MC_TEST_MISE_LOG="$TMP/service.log" MC_RELEASE_BUILD_ID="$release_id" \
      COLUMNS="$width" TERM=xterm-256color NO_COLOR="$no_color" FORCE_COLOR="$force_color" \
      "$HERE/build-prod.sh" >"$output" 2>&1 || status=$?
    [ "$status" -eq 3 ] || fail "the service check error permitted the image build"
    [ ! -e "$TMP/service.log" ] || fail "the service check error started the compiler"
    grep -q 'Reentrancy avoided' "$output" || fail "the service check lost its diagnostic"
    grep -q '^setup-progress:' "$output" && fail "the service check error announced a build"
    long=$(awk -v w="$width" 'length($0) > w' "$output")
    [ -z "$long" ] || fail "the service check message exceeds $width columns"
    tr '\n' ' ' <"$output" | tr -s ' ' | grep -F 'Open Terminal in the repository and run ./redeploy.sh.' >/dev/null || \
      fail "the service check message omits the next action"
  done
done

guard_output=$(PATH="$TMP/bin:/usr/bin:/bin" MC_TEST_SUPERVISED=1 MC_TEST_MISE_LOG="$TMP/guard.log" \
    MC_RELEASE_BUILD_ID="$release_id" "$HERE/build-prod.sh" 2>&1 || :)
case "$guard_output" in
  *"a supervised Culler deployment is loaded"*) ;;
  *) fail "rebuilding mc-prod under a live deployment was not refused" ;;
esac
# It must refuse BEFORE compiling anything: a refusal that still re-stamped the
# binary would leave the same mismatch it exists to prevent.
[ -e "$TMP/guard.log" ] && fail "the supervision guard ran the build before refusing"
if printf '%s\n' "$guard_output" | grep '^setup-progress:' >/dev/null; then
  fail "The supervision refusal announced a build operation."
fi
if PATH="$TMP/bin:/usr/bin:/bin" MC_TEST_MISE_LOG="$TMP/mise.log" \
    MC_RELEASE_BUILD_ID="$release_id" "$HERE/build-prod.sh" \
    >"$TMP/output" 2>&1; then
  fail "stubbed compiler unexpectedly completed"
fi

grep -F -- "-trimpath -mod=readonly -ldflags -w -X main.releaseBuildID=$release_id" "$TMP/mise.log" >/dev/null ||
  fail "the production helper build does not use the release build flags: -trimpath, -mod=readonly, -w, and the release identity"
[ "$(grep '^setup-progress:' "$TMP/output")" = 'setup-progress: compile-linux' ] ||
  fail "The failed compiler announced a later build operation."

cat >"$TMP/bin/mise" <<'EOF'
#!/bin/sh
set -eu
[ "${CGO_ENABLED:-}" = 0 ] && [ "${GOOS:-}" = linux ] && [ "${GOARCH:-}" = arm64 ] || exit 80
[ "${GOFLAGS-unset}" = "" ] && [ "${GOENV:-}" = off ] || exit 86
case "$*" in
  'exec -- go build '*./cmd/mc)
    printf 'test-operation: compile-linux\n' >&2 ;;
  'exec -- go build '*./runner/image/mc_dispatch.go)
    printf 'test-operation: compile-dispatcher\n' >&2 ;;
  *) exit 81 ;;
esac
EOF
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
set -eu
case "${1:-}" in
  build)
    [ "${DOCKER_BUILDKIT:-}" = 1 ] || exit 82
    case " $* " in *' --progress=plain '*) ;; *) exit 83 ;; esac
    case " $* " in
      *' -f runtime.Dockerfile '*) case " $* " in *' --target product '*) ;; *) exit 87 ;; esac ;;
    esac
    printf 'test-operation: build-container\n' >&2
    [ "${MC_TEST_BUILD_FAIL:-0}" = 0 ] || exit 84 ;;
  run)
    printf 'test-operation: check-browser\n' >&2 ;;
  *) exit 85 ;;
esac
EOF
chmod 755 "$TMP/bin/mise" "$TMP/bin/docker"

if ! PATH="$TMP/bin:/usr/bin:/bin" DOCKER_BUILDKIT=0 BUILDKIT_PROGRESS=tty GOFLAGS=-tags=test_scratch_spine \
    MC_RELEASE_BUILD_ID="$release_id" "$HERE/build-prod.sh" \
    >"$TMP/progress.stdout" 2>"$TMP/progress.stderr"; then
  cat "$TMP/progress.stderr" >&2
  fail "The fake tools did not complete the build sequence."
fi
cat >"$TMP/progress.expected" <<'EOF'
setup-progress: compile-linux
test-operation: compile-linux
setup-progress: compile-dispatcher
test-operation: compile-dispatcher
setup-progress: build-container
test-operation: build-container
test-operation: build-container
setup-progress: check-browser
test-operation: check-browser
test-operation: check-browser
EOF
grep -E '^(setup-progress|test-operation):' "$TMP/progress.stderr" >"$TMP/progress.actual"
cmp "$TMP/progress.expected" "$TMP/progress.actual" >/dev/null || \
  fail "A progress record did not precede its build operation."
if grep '^setup-progress:' "$TMP/progress.stdout" >/dev/null; then
  fail "A progress record used standard output."
fi

if PATH="$TMP/bin:/usr/bin:/bin" MC_TEST_BUILD_FAIL=1 \
    MC_RELEASE_BUILD_ID="$release_id" "$HERE/build-prod.sh" \
    >"$TMP/build-fail.stdout" 2>"$TMP/build-fail.stderr"; then
  fail "The failed container build completed its sequence."
else
  build_status=$?
fi
[ "$build_status" -eq 84 ] || fail "The build script changed the container failure status."
head -n 6 "$TMP/progress.expected" >"$TMP/build-fail.expected"
grep -E '^(setup-progress|test-operation):' "$TMP/build-fail.stderr" >"$TMP/build-fail.actual"
cmp "$TMP/build-fail.expected" "$TMP/build-fail.actual" >/dev/null || \
  fail "The failed container build announced a later operation."

grep -F -- 'ARG NODE_VERSION=24.18.0' "$HERE/Dockerfile" >/dev/null ||
  fail "production image does not pin the Node LTS used by Playwright"
grep -F -- '"@playwright/cli": "0.1.17"' "$HERE/project-tools/package.json" >/dev/null ||
  fail "production image does not pin the Playwright agent CLI"
grep -F -- 'PLAYWRIGHT_BROWSERS_PATH=/ms-playwright' "$HERE/Dockerfile" >/dev/null ||
  fail "Playwright browser does not have one image-owned shared path"
# Named explicitly, because `--browser=chromium` resolves to chrome-for-testing
# and nothing else installs it. The `--only-shell` form this replaced installed
# a headless shell the CLI's selector cannot reach, so the image shipped a
# browser layer no role could launch.
grep -F -- 'playwright-cli install-browser chrome-for-testing --with-deps' "$HERE/Dockerfile" >/dev/null ||
  fail "production image does not install the selectable Chromium browser by name"
# Comments are excluded deliberately: the Dockerfile explains the flag it no
# longer uses, and that prose is why the defect will not be reintroduced.
if grep -v '^[[:space:]]*#' "$HERE/Dockerfile" | grep -F -- '--only-shell' >/dev/null; then
  fail "production image installs the shell-only browser, which --browser=chromium cannot select"
fi
# The resident binds auth.json and sessions beneath the projected CODEX_HOME.
# Docker creates a missing bind parent root-owned, and codex initializes its
# in-process app-server inside CODEX_HOME, so an unowned /mc/codex fails every
# codex-routed run with `Permission denied` before a session starts.
grep -F -- 'install -d -o 10002 -g 10002 -m 0700 /mc/attachments /mc/attachments/in /mc/codex' \
  "$HERE/Dockerfile" >/dev/null ||
  fail "production image does not own the projected CODEX_HOME as the agent uid"
grep -F -- '--build-arg BROWSER_DEPS=1' "$HERE/build-prod.sh" >/dev/null ||
  fail "production build does not enable the browser layer"
grep -F -- '"$PWD/browser-smoke.sh" mc-prod' "$HERE/build-prod.sh" >/dev/null ||
  fail "production build does not launch the real browser acceptance smoke"
grep -F -- '"$PWD/browser-smoke.sh" mc-prod --runtime' "$HERE/build-prod.sh" >/dev/null ||
  fail "production build does not launch the runtime browser smoke"

# The runtime image installs its parts in a build stage and copies the finished parts.
grep -F -- 'FROM ${PROJECT_BASE} AS tools' "$HERE/runtime.Dockerfile" >/dev/null ||
  fail "runtime image has no build stage"
grep -F -- 'FROM ${PROJECT_BASE} AS runtime' "$HERE/runtime.Dockerfile" >/dev/null ||
  fail "runtime image has no runtime stage"
# The runtime holds the full browser build and no headless shell.
grep -F -- 'install chromium --no-shell' "$HERE/runtime.Dockerfile" >/dev/null ||
  fail "runtime image does not install the full browser build without the headless shell"
if grep -v '^[[:space:]]*#' "$HERE/runtime.Dockerfile" | grep -F -- '--only-shell' >/dev/null; then
  fail "runtime image installs the shell-only browser"
fi
# The harness install omits the optional packages and the unused peer packages.
grep -F -- 'bun install --production --frozen-lockfile --omit=optional --omit=peer' "$HERE/runtime.Dockerfile" >/dev/null ||
  fail "runtime image installs optional or peer harness packages"
# mc-real has its owner and mode in the layer that adds it.
grep -F -- 'COPY --chown=10001:10001 --chmod=6755 runner/image/${MC_BIN} /opt/culler-runtime/libexec/mc-real' "$HERE/runtime.Dockerfile" >/dev/null ||
  fail "runtime image does not add mc-real with its final owner and mode"
if grep -E -- 'ch(own|mod) [^ ]* /opt/culler-runtime/libexec/mc-real' "$HERE/runtime.Dockerfile" >/dev/null; then
  fail "runtime image changes mc-real after the layer that adds it"
fi

printf 'production image release identity: ok\n'
