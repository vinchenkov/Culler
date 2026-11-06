#!/bin/sh
# Fast, Docker-free contract tests for install.sh's Level-0 failure boundary.
set -eu
TEST_HOST_OS=$(/usr/bin/uname -s)

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/mc-install-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

fail() {
  printf 'install_test.sh: %s\n' "$*" >&2
  exit 1
}

# Each mc build omits DWARF data. The release builds carry the release identity.
[ "$(grep -c -F -- '-ldflags "-w -X main.releaseBuildID=$RELEASE_BUILD_ID -X main.releaseVersion=$RELEASE_VERSION"' "$ROOT/scripts/install-source.sh")" = 2 ] ||
  fail "a release build of install-source.sh lacks -w, the release identity, or the release version"
grep -F -- '-tags test_fake_routing -ldflags "-w" -o "$BIN_DIR/mc" ./cmd/mc' "$ROOT/scripts/install-source.sh" >/dev/null ||
  fail "the development build of install-source.sh does not omit DWARF data"

for width in 80 40; do
  COLUMNS=$width sh "$ROOT/scripts/install-source.sh" --help >"$TMP/help-$width.out"
  awk -v width="$width" 'length($0) > width { exit 1 }' "$TMP/help-$width.out" || fail "The help exceeds $width columns."
  grep -Fx '  --accept-default-dashboard-bind' "$TMP/help-$width.out" >/dev/null || fail 'The help splits a long flag.'
  if grep -E -- '--static-(binding|credential|token-file)' "$TMP/help-$width.out" >/dev/null; then
    fail 'The help lists an unsupported credential option.'
  fi
done

make_mise() {
  bin=$1
  mkdir -p "$bin"
  # The cases search only this directory and the system paths.
  # A Linux image can keep Git and python3 outside the system paths.
  for tool in git python3; do
    if [ ! -x "/usr/bin/$tool" ] && [ ! -x "/bin/$tool" ]; then
      ln -s "$(command -v "$tool")" "$bin/$tool"
    fi
  done
  cat >"$bin/mise" <<'EOF'
#!/bin/sh
set -eu
if [ "${1:-}" = install ] || [ "${1:-}" = trust ]; then
  exit 0
fi
if [ "${1:-}" = exec ]; then
  shift
  [ "${1:-}" != -- ] || shift
fi
if [ "${1:-}" = go ] && [ "${2:-}" = version ]; then
  exit 0
fi
if [ "${1:-}" = bun ] && [ "${2:-}" = --version ]; then
  exit 0
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
if [ -n "${MC_INSTALL_TEST_LOG:-}" ]; then
  if [ -n "${MC_INSTALL_TEST_ENV_LOG:-}" ]; then
    printf '%s\n%s\n%s\n' "${MC_HOME:-}" "${MC_INSTALL_TEST_MISE:-}" "${CULLER_PARENT_PATH:-}" >"$MC_INSTALL_TEST_ENV_LOG"
  fi
  {
    printf 'CALL'
    for arg do
      printf '\t%s' "$arg"
    done
    printf '\n'
  } >>"$MC_INSTALL_TEST_LOG"
  if [ "${MC_INSTALL_TEST_FAIL_PREFLIGHT:-0}" = 1 ] && [ "${1:-}" = __onboard ] && [ "${2:-}" = preflight ]; then
    echo "The host requirements check failed." >&2
    exit 1
  fi
  if [ -n "${MC_INSTALL_TEST_FAIL_AGENTSVIEW:-}" ] && [ "${1:-}" = __onboard ] && [ "${2:-}" = agentsview ]; then
    echo "mc: download the AgentsView release archive: connection refused" >&2
    exit 1
  fi
  if [ "${1:-}" = __onboard ] && [ "${2:-}" = home ] && [ -n "${MC_BIN_DIR:-}" ]; then
    mkdir -p "$MC_BIN_DIR"
    cp "$0" "$MC_BIN_DIR/mc"
    chmod 755 "$MC_BIN_DIR/mc"
  fi
  exit 0
fi
exit 91
SCRIPT
  chmod 755 "$out"
  exit 0
fi
if [ -x "${1:-}" ]; then
  PATH="/fixture/mise/bin:$PATH" MC_INSTALL_TEST_MISE=1 exec "$@"
fi
exit 92
EOF
  chmod 755 "$bin/mise"
  # Every case gets a launchctl reporting NOTHING loaded. `install.sh` builds
  # the production image, and build-prod.sh refuses that while a supervised
  # deployment is running — correctly, since the rebuild re-stamps the image the
  # live helper is recreated from. Stubbing it is what keeps this suite's
  # verdict independent of whether the developer's own deployment happens to be
  # up; the real binary sits in /bin, which every case's PATH reaches.
  cat >"$bin/launchctl" <<'EOF'
#!/bin/sh
exit 0
EOF
  chmod 755 "$bin/launchctl"
}

run_must_fail() {
  name=$1
  expected=$2
  shift 2
  out="$TMP/$name.out"
  if "$@" >"$out" 2>&1; then
    fail "$name unexpectedly exited 0"
  fi
  # fail() folds long messages at spaces, so the check joins the lines.
  tr -d '\n' <"$out" | grep -F -- "$expected" >/dev/null || {
    sed -n '1,120p' "$out" >&2
    fail "$name did not report: $expected"
  }
}

run_terminal() {
  terminal_output=$1
  shift
  printf '%s\n' "$@" >"$TMP/terminal-args"
  cat >"$TMP/terminal-shell" <<'EOF'
#!/bin/sh
set --
while IFS= read -r arg; do
  set -- "$@" "$arg"
done <"$MC_INSTALL_TEST_TTY_ARGS"
"$@"
printf '%s\n' "$?" >"$MC_INSTALL_TEST_TTY_STATUS"
EOF
  chmod 755 "$TMP/terminal-shell"
  rm -f "$TMP/terminal-status"
  if [ "$TEST_HOST_OS" = Linux ]; then
    MC_INSTALL_TEST_TTY_ARGS="$TMP/terminal-args" MC_INSTALL_TEST_TTY_STATUS="$TMP/terminal-status" \
      script -q -c "$TMP/terminal-shell" "$terminal_output" </dev/null >/dev/null 2>&1 || true
  else
    SHELL="$TMP/terminal-shell" MC_INSTALL_TEST_TTY_ARGS="$TMP/terminal-args" \
      MC_INSTALL_TEST_TTY_STATUS="$TMP/terminal-status" \
      script -q "$terminal_output" </dev/null >/dev/null 2>&1 || true
  fi
  [ -f "$TMP/terminal-status" ] || fail "the terminal fixture did not complete"
  return "$(cat "$TMP/terminal-status")"
}

run_must_fail inline-vision "unknown flag --directive" \
  sh "$ROOT/scripts/install-source.sh" --directive "Improve the project."
run_must_fail codex-login-file "unknown flag --codex-auth-file" \
  sh "$ROOT/scripts/install-source.sh" --codex-auth-file "$TMP/auth.json"
run_must_fail claude-login-file "unknown flag --claude-credentials-file" \
  sh "$ROOT/scripts/install-source.sh" --claude-credentials-file "$TMP/.credentials.json"

case_root="$TMP/terminal-setup"
make_mise "$case_root/bin"
cat >"$case_root/bin/docker" <<'EOF'
#!/bin/sh
printf 'unexpected Docker call\n' >>"$MC_INSTALL_TEST_DOCKER_LOG"
exit 94
EOF
cat >"$case_root/bin/uname" <<'EOF'
#!/bin/sh
printf 'Darwin\n'
EOF
cat >"$case_root/bin/xcode-select" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 755 "$case_root/bin/docker" "$case_root/bin/uname" "$case_root/bin/xcode-select"
mkdir -p "$case_root/home" "$case_root/project root" "$case_root/install bin"
printf 'existing deployment executable\n' >"$case_root/install bin/mc"
run_terminal "$case_root/terminal.out" \
  env -u CULLER_PARENT_PATH PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_HOME= COLUMNS=60 MC_INSTALL_TEST_LOG="$case_root/forward.log" \
  MC_INSTALL_TEST_ENV_LOG="$case_root/environment.log" \
  MC_INSTALL_TEST_DOCKER_LOG="$case_root/docker.log" \
  sh "$ROOT/scripts/install-source.sh" --bin-dir "$case_root/install bin" \
    --workspace-root "$case_root/project root" --runtime-bindings chatgpt \
    --acquire-runtime-auth || \
  fail "terminal setup did not reach the interface"
expected=$(printf 'CALL\t__setup\t--bin-dir\t%s\t--workspace-root\t%s\t--runtime-bindings\tchatgpt\t--acquire-runtime-auth\t--release-source\t%s\t--host-release-source\t%s' \
  "$case_root/install bin" "$case_root/project root" "$ROOT/runner" "$ROOT")
[ "$(cat "$case_root/forward.log")" = "$expected" ] || fail "terminal setup changed the original arguments"
[ "$(sed -n '1p' "$case_root/environment.log")" = "$case_root/home/.culler" ] || \
  fail "terminal setup omitted the default production home"
[ "$(sed -n '2p' "$case_root/environment.log")" = 1 ] || \
  fail "terminal setup lost the pinned tool environment"
[ "$(sed -n '3p' "$case_root/environment.log")" = "$case_root/bin:/usr/bin:/bin" ] || \
  fail "The source installer lost the original shell search path."
[ ! -e "$case_root/docker.log" ] || fail "terminal setup checked Docker before the interface"
[ "$(cat "$case_root/install bin/mc")" = "existing deployment executable" ] || fail "terminal setup replaced the deployed executable before inspection"
grep -F 'Culler' "$case_root/terminal.out" >/dev/null || \
  fail "terminal setup omitted its identity"
grep -F 'The setup program is ready' "$case_root/terminal.out" >/dev/null || \
  fail "terminal setup omitted the completed preparation"
if grep -E '^\+[-]+\+' "$case_root/terminal.out" >/dev/null; then
  fail "terminal setup retains a preparation box"
fi
grep -F 'deployment home (MC_HOME)' "$case_root/terminal.out" >/dev/null && \
  fail "terminal setup used a shell prompt"

run_terminal "$case_root/parent-path.out" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_HOME= CULLER_PARENT_PATH="/original/shell/bin:/usr/bin:/bin" \
  MC_INSTALL_TEST_LOG="$case_root/parent-path.log" \
  MC_INSTALL_TEST_ENV_LOG="$case_root/parent-path-env.log" \
  sh "$ROOT/scripts/install-source.sh" --bin-dir "$case_root/install bin" || \
  fail "The source installer rejected the inherited shell search path."
[ "$(sed -n '3p' "$case_root/parent-path-env.log")" = "/original/shell/bin:/usr/bin:/bin" ] || \
  fail "The source installer replaced the inherited shell search path."

cat >"$case_root/bin/xcode-select" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$MC_INSTALL_TEST_APPLE_LOG"
[ "$1" = --install ]
EOF
run_terminal "$case_root/apple.out" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_HOME= MC_INSTALL_TEST_APPLE_LOG="$case_root/apple.log" \
  sh "$ROOT/scripts/install-source.sh" --bin-dir "$case_root/apple-bin" || \
  fail "the Apple installer handoff failed"
[ "$(cat "$case_root/apple.log")" = "$(printf '%s\n' -p --install)" ] || \
  fail "the Apple handoff did not use the official installer"
[ ! -e "$case_root/apple-bin/mc" ] || fail "the Apple handoff built before the host tools were ready"
tr '\r\n' '  ' <"$case_root/apple.out" | tr -s ' ' | grep -F 'run sh scripts/install-source.sh to resume setup' >/dev/null || \
  fail "the Apple handoff omitted the resume action"

case_root="$TMP/mise-bootstrap"
mkdir -p "$case_root/bin" "$case_root/home"
sed -n '/^install_mise() {$/,/^}$/p' "$ROOT/scripts/install-source.sh" >"$case_root/bootstrap.sh"
cat >"$case_root/bin/uname" <<'EOF'
#!/bin/sh
printf 'arm64\n'
EOF
cat >"$case_root/bin/curl" <<'EOF'
#!/bin/sh
set -eu
url=
output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    https://*) url=$1 ;;
    --output) output=$2; shift ;;
  esac
  shift
done
[ -n "$output" ] || exit 80
case "$url" in
  https://github.com/jdx/mise/releases/download/v2026.9.1/mise-v2026.9.1-macos-arm64)
    cp "$MC_INSTALL_TEST_MISE_FIXTURE" "$output" ;;
  https://github.com/jdx/mise/releases/download/v2026.9.1/SHASUMS256.txt)
    digest=$(shasum -a 256 "$MC_INSTALL_TEST_MISE_FIXTURE" | awk '{print $1}')
    [ "${MC_INSTALL_TEST_BAD_DIGEST:-0}" = 0 ] || digest=$(printf '%064d' 0)
    filename=${MC_INSTALL_TEST_MISE_FILENAME:-./mise-v2026.9.1-macos-arm64}
    printf '%s  %s\n' "$digest" "$filename" >"$output"
    if [ "${MC_INSTALL_TEST_DUPLICATE_DIGEST:-0}" = 1 ]; then
      printf '%s  %s\n' "$digest" "$filename" >>"$output"
    fi ;;
  *) exit 81 ;;
esac
EOF
chmod 755 "$case_root/bin/uname" "$case_root/bin/curl"
printf 'this file is a test fixture, not a program\n' >"$case_root/fixture"
cat >>"$case_root/bootstrap.sh" <<'EOF'
set -eu
say() { printf '%s\n' "$*"; }
fail() { printf '%s\n' "$*" >&2; exit 1; }
OS=Darwin
install_mise
EOF
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_MISE_FIXTURE="$case_root/fixture" \
  sh "$case_root/bootstrap.sh" >"$case_root/bootstrap.out" 2>&1 || \
  fail "the verified mise fixture did not install"
cmp "$case_root/fixture" "$case_root/home/.local/bin/mise" >/dev/null || \
  fail "the mise bootstrap changed the verified bytes"
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/bare-home" \
  MC_INSTALL_TEST_MISE_FIXTURE="$case_root/fixture" \
  MC_INSTALL_TEST_MISE_FILENAME=mise-v2026.9.1-macos-arm64 \
  sh "$case_root/bootstrap.sh" >"$case_root/bare.out" 2>&1 || \
  fail "The mise bootstrap rejected a bare filename."
cmp "$case_root/fixture" "$case_root/bare-home/.local/bin/mise" >/dev/null || \
  fail "The mise bootstrap changed the verified bytes."
for checksum_name in ./mise-v2026.9.1-macos-arm64.tar.gz ./other/mise-v2026.9.1-macos-arm64 ./mise-v2026.9.1-macos-x64; do
  run_must_fail mise-checksum-filename "checksum is missing or invalid" \
    env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/wrong-asset-home" \
    MC_INSTALL_TEST_MISE_FIXTURE="$case_root/fixture" \
    MC_INSTALL_TEST_MISE_FILENAME="$checksum_name" \
    sh "$case_root/bootstrap.sh"
  [ ! -e "$case_root/wrong-asset-home/.local/bin/mise" ] || \
    fail "The mise bootstrap installed an asset without its exact checksum."
done
run_must_fail mise-checksum-duplicate "checksum is missing or invalid" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/duplicate-home" \
  MC_INSTALL_TEST_MISE_FIXTURE="$case_root/fixture" MC_INSTALL_TEST_DUPLICATE_DIGEST=1 \
  sh "$case_root/bootstrap.sh"
[ ! -e "$case_root/duplicate-home/.local/bin/mise" ] || \
  fail "The mise bootstrap accepted duplicate checksums."
run_must_fail mise-checksum "failed its checksum check" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/rejected-home" \
  MC_INSTALL_TEST_MISE_FIXTURE="$case_root/fixture" MC_INSTALL_TEST_BAD_DIGEST=1 \
  sh "$case_root/bootstrap.sh"
[ ! -e "$case_root/rejected-home/.local/bin/mise" ] || fail "the mise bootstrap installed an invalid download"

# PATH deliberately omits Homebrew, where Docker normally lives on macOS.
case_root="$TMP/missing-cli"
make_mise "$case_root/bin"
run_must_fail missing-cli "docker CLI is required" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin"

case_root="$TMP/stopped-daemon"
make_mise "$case_root/bin"
cat >"$case_root/bin/docker" <<'EOF'
#!/bin/sh
[ "${1:-}" != info ] || exit 1
exit 93
EOF
cat >"$case_root/bin/uname" <<'EOF'
#!/bin/sh
printf 'Linux\n'
EOF
chmod 755 "$case_root/bin/docker" "$case_root/bin/uname"
run_must_fail stopped-daemon "docker daemon is not responding" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin"
[ ! -e "$case_root/install-bin/mc" ] || fail "stopped-daemon wrote mc before preflight passed"

case_root="$TMP/image-build-failed"
make_mise "$case_root/bin"
cat >"$case_root/bin/docker" <<'EOF'
#!/bin/sh
case "${1:-}" in
  info) exit 0 ;;
  build) exit 1 ;;
esac
exit 94
EOF
chmod 755 "$case_root/bin/docker"
mkdir -p "$case_root/install-bin"
printf 'existing executable\n' >"$case_root/install-bin/mc"
run_must_fail image-build-failed "production image build failed" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin"

[ "$(cat "$case_root/install-bin/mc")" = "existing executable" ] ||
  fail "The failed image build replaced the installed executable."

case_root="$TMP/production-forwarding"
make_mise "$case_root/bin"
cat >"$case_root/bin/docker" <<'EOF'
#!/bin/sh
case "${1:-}" in
  info|build|run) exit 0 ;;
esac
exit 94
EOF
cat >"$case_root/bin/uname" <<'EOF'
#!/bin/sh
printf 'Linux\n'
EOF
chmod 755 "$case_root/bin/docker" "$case_root/bin/uname"
mkdir -p "$case_root/home"
mkdir -p "$case_root/workspace" "$case_root/workspace root"
case_workspace=$(CDPATH= cd -- "$case_root/workspace" && pwd -P)
case_workspace_space=$(CDPATH= cd -- "$case_root/workspace root" && pwd -P)
forward_log="$case_root/forward.log"
mkdir -p "$case_root/preserved-bin"
printf 'existing executable\n' >"$case_root/preserved-bin/mc"
run_must_fail production-preflight-preserves-executable "The host requirements check failed." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" MC_INSTALL_TEST_FAIL_PREFLIGHT=1 \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/preserved-bin" \
    --mc-home "$case_root/mc-home" --runtime-bindings chatgpt \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind --discord-mode disabled
[ "$(cat "$case_root/preserved-bin/mc")" = "existing executable" ] || \
  fail "The failed host check replaced the installed executable."
: >"$forward_log"
run_must_fail production-missing-home "deployment home (MC_HOME) is required" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_HOME= MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin"
run_must_fail production-bad-vision-answer "--project-vision must be create|later" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin" \
    --mc-home "$case_root/mc-home" \
    --project primary --workspace-root "$case_root/workspace" \
    --accept-generated-sandbox --project-vision invalid \
    --runtime-bindings chatgpt,claude \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled
run_must_fail production-zero-bindings "Select at least one agent profile with --runtime-bindings." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin" \
    --mc-home "$case_root/mc-home" \
    --project primary --workspace-root "$case_root/workspace" \
    --accept-generated-sandbox \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled
: >"$forward_log"
run_must_fail production-undefined-binding \
  "--runtime-bindings names \"second-endpoint\", an undefined binding. The defined bindings are: chatgpt, claude. Use one of these ids." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/undefined-binding-bin" \
    --mc-home "$case_root/mc-home" \
    --runtime-bindings chatgpt,second-endpoint \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled
run_must_fail production-empty-binding \
  "--runtime-bindings contains an empty binding id. Remove the extra comma." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/undefined-binding-bin" \
    --mc-home "$case_root/mc-home" \
    --runtime-bindings chatgpt, \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled
run_must_fail production-repeated-binding \
  "--runtime-bindings names \"chatgpt\" twice. Remove the repeated id." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/undefined-binding-bin" \
    --mc-home "$case_root/mc-home" \
    --runtime-bindings "chatgpt, chatgpt" \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled
run_must_fail production-blank-binding \
  "--runtime-bindings names \"chatgpt claude\", an undefined binding. The defined bindings are: chatgpt, claude. Use one of these ids." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/undefined-binding-bin" \
    --mc-home "$case_root/mc-home" \
    --runtime-bindings "chatgpt,claude,chatgpt claude" \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled
run_must_fail development-undefined-binding \
  "--runtime-bindings names \"endpoint\", an undefined binding. The defined bindings are: chatgpt, claude, fake. Use one of these ids." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --dev --mc-home "$case_root/undefined-binding-home" \
    --runtime-bindings endpoint \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled
mkdir -p "$case_root/profile-home"
cat >"$case_root/profile-home/config.toml" <<'EOF'
[runtimes]
  enabled_bindings = ["chatgpt"]
  [runtimes.bindings]
    [runtimes.bindings.chatgpt]
      harness = "codex"
    [runtimes.bindings."operator.endpoint"]
      harness = "claude-sdk"
EOF
run_must_fail config-undefined-binding \
  "--runtime-bindings names \"claude\", an undefined binding. The defined bindings are: chatgpt, operator.endpoint. Use one of these ids." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/undefined-binding-bin" \
    --mc-home "$case_root/profile-home" \
    --runtime-bindings claude \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled
[ ! -s "$forward_log" ] && [ ! -e "$case_root/undefined-binding-bin/mc" ] && \
  [ ! -e "$case_root/undefined-binding-home" ] || \
  fail "an undefined runtime binding reached the build or the wizard"
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin" \
    --mc-home "$case_root/profile-home" \
    --project primary --workspace-root "$case_root/workspace" \
    --accept-generated-sandbox \
    --runtime-bindings operator.endpoint \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled \
    >/dev/null 2>&1 || fail "a binding of config.toml did not reach the whole wizard"
tail -n 1 "$forward_log" | grep -F -e "$(printf -- '--runtime-bindings\toperator.endpoint\t')" >/dev/null || \
  fail "the whole wizard did not receive the binding of config.toml"
: >"$forward_log"
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin" \
    --mc-home "$case_root/mc-home" \
    --runtime-bindings chatgpt,claude \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind --discord-mode disabled \
    >"$case_root/no-project-output" 2>&1 || fail "installation without a Project failed"
actual=$(tail -n 1 "$forward_log")
expected=$(printf 'CALL\t__onboard\t--runtime-bindings\tchatgpt,claude\t--accept-default-routing\t--accept-default-tunables\t--accept-default-git-identity\t--accept-default-dashboard-bind\t--discord-mode\tdisabled')
[ "$actual" = "$expected" ] || fail "installation without a Project changed its service choices"
expected=$(printf 'CALL\t__onboard\tpreflight\nCALL\t__onboard\thome\nCALL\t__onboard\tagentsview\nCALL\t__onboard\t--runtime-bindings')
[ "$(cut -f1-3 "$forward_log")" = "$expected" ] || {
  cut -f1-3 "$forward_log" >&2
  fail "the production hand-off did not install AgentsView between the home section and the whole wizard"
}
if grep -Ei 'first Project|workspace root|VISION.md|sandbox profile' "$case_root/no-project-output" >/dev/null; then
  fail "installation without a Project requested project inputs"
fi
: >"$forward_log"
run_must_fail agentsview-install-failure \
  "install.sh: The AgentsView install failed. Fix the error above, then run sh scripts/install-source.sh again." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" MC_INSTALL_TEST_FAIL_AGENTSVIEW=1 \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin" \
    --mc-home "$case_root/mc-home" \
    --runtime-bindings chatgpt,claude \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind --discord-mode disabled
[ "$(tail -n 1 "$forward_log" | cut -f1-3)" = "$(printf 'CALL\t__onboard\tagentsview')" ] || \
  fail "a failed AgentsView install reached the whole wizard"
: >"$forward_log"
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin" \
    --mc-home "$case_root/mc-home" \
    --project primary --workspace-root "$case_root/workspace" \
    --accept-generated-sandbox \
    --runtime-bindings chatgpt,claude \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled \
    >"$case_root/front-door.out" 2>&1 || fail "production defaults did not reach the whole wizard"
for stated in \
  "VISION.md describes your project's open-ended, high-level goal." \
  "Culler develops strategy and proposes high-leverage work to advance that vision as your project develops." \
  "This vision gives Culler the direction it requires before it can start work." \
  "Work cannot start: $case_workspace/VISION.md is required. Your agent can help you write it." \
  "Culler develops $case_workspace with git." \
  "An existing repository there is adopted." \
  "If there is no repository, Culler runs \`git init\` in $case_workspace."
do
  grep -F -- "$stated" "$case_root/front-door.out" >/dev/null || {
    cat "$case_root/front-door.out" >&2
    fail "the first-Project section did not state: $stated"
  }
done
expected=$(printf 'CALL\t__onboard\t--project\tprimary\t--workspace-root\t%s\t--accept-generated-sandbox\t--runtime-bindings\tchatgpt,claude\t--accept-default-routing\t--accept-default-tunables\t--accept-default-git-identity\t--accept-default-dashboard-bind\t--discord-mode\tdisabled' \
  "$case_workspace")
actual=$(tail -n 1 "$forward_log")
[ "$actual" = "$expected" ] || {
  printf 'expected: %s\nactual:   %s\n' "$expected" "$actual" >&2
  fail "production defaults changed whole-wizard arguments"
}
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin" \
    --mc-home "$case_root/mc-home" \
    --project primary --workspace-root "$case_root/workspace root" \
    --project-git-author-name "Primary Bot" \
    --project-git-author-email "primary@example.test" \
    --accept-generated-sandbox --project-vision later \
    --runtime-bindings chatgpt,claude --acquire-runtime-auth \
    --routing-file "$case_root/routing answers.md" \
    --review-wip-cap 5 --timeout-minutes 45 --grace-minutes 10 \
    --heartbeat-interval-s 30 --spawn-grace-s 40 --homie-idle-timeout-s 900 \
    --git-author-name "MC Roles" --git-author-email "roles@example.invalid" \
    --dashboard-bind 0.0.0.0:7443 \
    --dashboard-auth-token-file "$case_root/dashboard token" \
    --discord-mode disabled \
    --confirm-live-spend --restore-latest --activate \
    >/dev/null 2>&1 || fail "production-forwarding did not reach the whole wizard"
expected=$(printf 'CALL\t__onboard\t--project\tprimary\t--workspace-root\t%s\t--accept-generated-sandbox\t--project-vision\tlater\t--project-git-author-name\tPrimary Bot\t--project-git-author-email\tprimary@example.test\t--runtime-bindings\tchatgpt,claude\t--routing-file\t%s\t--review-wip-cap\t5\t--timeout-minutes\t45\t--grace-minutes\t10\t--heartbeat-interval-s\t30\t--spawn-grace-s\t40\t--homie-idle-timeout-s\t900\t--git-author-name\tMC Roles\t--git-author-email\troles@example.invalid\t--dashboard-bind\t0.0.0.0:7443\t--dashboard-auth-token-file\t%s\t--discord-mode\tdisabled\t--acquire\t--confirm-live-spend\t--restore-latest\t--activate' \
  "$case_workspace_space" "$case_root/routing answers.md" "$case_root/dashboard token")
actual=$(tail -n 1 "$forward_log")
[ "$actual" = "$expected" ] || {
  printf 'expected: %s\nactual:   %s\n' "$expected" "$actual" >&2
  fail "production-forwarding changed whole-wizard arguments"
}

: >"$forward_log"
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --dev --mc-home "$case_root/dev-home" \
    --project primary --workspace-root "$case_root/workspace" \
    --project-git-author-name "Development Bot" \
    --project-git-author-email "development@example.test" \
    --accept-generated-sandbox \
    --runtime-bindings chatgpt,claude \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled \
    >/dev/null 2>&1 || fail "development-forwarding did not reach the wizard"
expected=$(printf 'CALL\t__onboard\t--release-source\t%s\t--host-release-source\t%s\t--project\tprimary\t--workspace-root\t%s\t--accept-generated-sandbox\t--runtime-bindings\tchatgpt,claude\t--project-git-author-name\tDevelopment Bot\t--project-git-author-email\tdevelopment@example.test\t--accept-default-routing\t--accept-default-tunables\t--accept-default-git-identity\t--accept-default-dashboard-bind\t--discord-mode\tdisabled' \
  "$ROOT/runner" "$ROOT" "$case_workspace")
actual=$(tail -n 1 "$forward_log")
[ "$actual" = "$expected" ] || {
  printf 'expected: %s\nactual:   %s\n' "$expected" "$actual" >&2
  fail "development-forwarding changed wizard arguments"
}

run_must_fail project-git-identity-one-sided \
  "--project-git-author-name and --project-git-author-email are required together" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --dev --mc-home "$case_root/dev-home-pair-refusal" \
    --project primary --workspace-root "$case_root/workspace" \
    --project-git-author-name "One-sided Bot" \
    --accept-generated-sandbox \
    --runtime-bindings chatgpt,claude \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled

: >"$forward_log"
env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  MC_INSTALL_TEST_LOG="$forward_log" \
  sh "$ROOT/scripts/install-source.sh" --yes --bin-dir "$case_root/install-bin" \
    --mc-home "$case_root/mc-home" \
    --project primary --workspace-root "$case_root/workspace" \
    --accept-generated-sandbox \
    --runtime-bindings chatgpt,claude \
    --accept-default-routing --accept-default-tunables \
    --accept-default-git-identity --accept-default-dashboard-bind \
    --discord-mode disabled \
    --activate --smoke --smoke-approve-task 37 \
    >/dev/null 2>&1 || fail "production-smoke did not reach the ride-through"
actual=$(tail -n 2 "$forward_log")
expected=$(printf 'CALL\t__onboard\t--project\tprimary\t--workspace-root\t%s\t--accept-generated-sandbox\t--runtime-bindings\tchatgpt,claude\t--accept-default-routing\t--accept-default-tunables\t--accept-default-git-identity\t--accept-default-dashboard-bind\t--discord-mode\tdisabled\t--activate\nCALL\t__onboard\t--smoke\t--smoke-approve-task\t37' \
  "$case_workspace")
[ "$actual" = "$expected" ] || {
  printf 'expected: %s\nactual:   %s\n' "$expected" "$actual" >&2
  fail "production-smoke changed whole-wizard or smoke arguments"
}

run_must_fail development-smoke "--smoke is production-only" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  sh "$ROOT/scripts/install-source.sh" --yes --dev --mc-home "$case_root/dev-home" \
    --smoke --activate
run_must_fail development-static-binding "Static credentials are unsupported." \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  sh "$ROOT/scripts/install-source.sh" --yes --dev --mc-home "$case_root/dev-home" --static-binding fixture-secret
run_must_fail smoke-without-activation "--smoke requires --activate" \
  env PATH="$case_root/bin:/usr/bin:/bin" HOME="$case_root/home" \
  sh "$ROOT/scripts/install-source.sh" --yes --smoke

printf 'install.sh fail-closed preflight: ok\n'
