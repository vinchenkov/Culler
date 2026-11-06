#!/bin/sh
# Culler setup
#
# Usage: sh scripts/install-source.sh [options]
#
# Run sh scripts/install-source.sh for terminal setup.
# Use --yes with explicit setup options.
# Agent connections use Codex or Claude Code sign-in.
#
# The development tier uses a scratch deployment home.
# Shared host tools remain after either uninstall mode.
# Culler removes its private libraries and images.
#
# Options:
#   --dev  Use the development tier with the fake harness.
#   --mc-home <path>  Select the deployment home. The development tier requires a scratch path.
#   --bin-dir <path>  Select the executable directory. Production defaults to $HOME/.local/bin.
#   --project <id>  Name the first Project.
#   --workspace-root <path>  Select the first Project workspace.
#   --project-git-author-name <name>  Set the Project commit author name.
#   --project-git-author-email <email>  Set the Project commit author email.
#   --accept-generated-sandbox  Accept the displayed Project access profile.
#   --project-vision <create|later>  Select the action when the workspace has no VISION.md.
#   --runtime-bindings <ids>  Select agent profiles, separated by commas. One profile is enough.
#     Each profile must exist in MC_HOME/config.toml.
#     An absent configuration offers chatgpt and claude.
#   --acquire-runtime-auth  Start a separate Culler sign-in for each selected account type.
#   --confirm-live-spend  Run live acceptance for each selected agent profile after final verification.
#   --accept-default-routing  Accept the recommended role assignments.
#   --routing-file <path>  Select an explicit file for role assignments.
#   --accept-default-tunables  Accept the recommended service intervals and review limit.
#   --review-wip-cap <n>  Set the maximum live review packets per Project. The default is 3.
#   --homie-idle-timeout-s <n>  Set the Homie conversation idle limit in seconds. The default is 1800.
#   --timeout-minutes <n>  Set the agent lease in minutes.
#   --grace-minutes <n>  Set the agent lease grace in minutes.
#   --heartbeat-interval-s <n>  Set the agent heartbeat interval in seconds.
#   --spawn-grace-s <n>  Set the first heartbeat grace in seconds.
#   --hard-deadline-minutes <n>  Set the maximum agent run in minutes.
#   --accept-default-git-identity  Accept Culler <mc@localhost> as the commit author.
#   --git-author-name <name>  Set the default commit author name.
#   --git-author-email <email>  Set the default commit author email.
#   --accept-default-dashboard-bind  Accept the dashboard address 127.0.0.1:7333.
#   --dashboard-bind <host:port>  Select the dashboard address.
#   --dashboard-auth-token-file <path>  Supply the dashboard token from a private file.
#   --discord-mode <disabled|enabled>  Select the Discord mode. The default is disabled.
#   --restore-latest  Restore the newest compatible snapshot when the spine is absent.
#   --activate  Authorize service activation while the operator is present.
#   --reconfigure  Stop the services and apply the current configuration while the operator is present.
#   --smoke  Run the full pipeline acceptance.
#   --smoke-approve-task <id>  Approve this exact task during pipeline acceptance.
#   --yes  Use explicit options without prompts.
#   --help  Show this help.

set -eu
set +x
umask 077
CULLER_PARENT_PATH=${CULLER_PARENT_PATH-$PATH}
export CULLER_PARENT_PATH

say() { printf '%s\n' "$*"; }
fail() {
  _fail_width=${COLUMNS:-$(tput cols 2>/dev/null || :)}
  case "$_fail_width" in ''|*[!0-9]*|????*) _fail_width=80 ;; esac
  [ "$_fail_width" -ge 40 ] || _fail_width=40
  [ "$_fail_width" -le 80 ] || _fail_width=80
  printf 'install.sh: %s\n' "$*" | fold -s -w "$_fail_width" >&2
  exit 1
}

show_help() {
  help_width=${COLUMNS:-$(tput cols 2>/dev/null || :)}
  case "$help_width" in ''|*[!0-9]*|????*) help_width=80 ;; esac
  [ "$help_width" -ge 40 ] || help_width=40
  [ "$help_width" -le 80 ] || help_width=80
  sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//' | awk -v width="$help_width" '
    function emit(text, indent, words, count, word_index, line) {
      count = split(text, words, /[[:space:]]+/)
      line = indent
      for (word_index = 1; word_index <= count; word_index++) {
        if (words[word_index] == "") continue
        if (length(line) > length(indent) && length(line) + 1 + length(words[word_index]) > width) {
          print line
          line = indent
        }
        line = line (length(line) > length(indent) ? " " : "") words[word_index]
      }
      print line
    }
    {
      if ($0 == "") { print ""; next }
      text = $0
      match(text, /^[[:space:]]*/)
      padding = RLENGTH
      sub(/^[[:space:]]+/, "", text)
      if (text ~ /^--/ && match(text, /  +/)) {
        emit(substr(text, 1, RSTART - 1), "  ")
        emit(substr(text, RSTART + RLENGTH), "    ")
      } else {
        emit(text, padding > 4 ? "    " : substr("    ", 1, padding))
      }
    }
  '
}

REPO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CULLER_SOURCE_INSTALLER="$REPO_DIR/scripts/install-source.sh"
export CULLER_SOURCE_INSTALLER

DEV=0
ASSUME_YES=0

MC_HOME_IN="${MC_HOME:-}"
BIN_DIR=""
PROJECT=""
WORKSPACE_ROOT=""
PROJECT_GIT_AUTHOR_NAME=""
PROJECT_GIT_AUTHOR_EMAIL=""
ACCEPT_GENERATED_SANDBOX=0
PROJECT_VISION=""
RUNTIME_BINDINGS=""
ACCEPT_DEFAULT_ROUTING=0
ROUTING_FILE=""
ACCEPT_DEFAULT_TUNABLES=0
REVIEW_WIP_CAP=""
TIMEOUT_MINUTES=""
GRACE_MINUTES=""
HEARTBEAT_INTERVAL_S=""
SPAWN_GRACE_S=""
HARD_DEADLINE_MINUTES=""
HOMIE_IDLE_TIMEOUT_S=""
ACCEPT_DEFAULT_GIT_IDENTITY=0
GIT_AUTHOR_NAME=""
GIT_AUTHOR_EMAIL=""
ACCEPT_DEFAULT_DASHBOARD_BIND=0
DASHBOARD_BIND=""
DASHBOARD_AUTH_TOKEN_FILE=""
DISCORD_MODE=""
ACQUIRE_RUNTIME_AUTH=0
CONFIRM_LIVE_SPEND=0
RESTORE_LATEST=0
ACTIVATE=0
RECONFIGURE=0
SMOKE=0
SMOKE_APPROVE_TASK=""

parse_args() {
while [ $# -gt 0 ]; do
  case "$1" in
    --dev) DEV=1 ;;
    --yes) ASSUME_YES=1 ;;
    --mc-home) MC_HOME_IN=${2:?--mc-home needs a path}; shift ;;
    --bin-dir) BIN_DIR=${2:?--bin-dir needs a path}; shift ;;
    --project) PROJECT=${2:?--project needs an id}; shift ;;
    --workspace-root) WORKSPACE_ROOT=${2:?--workspace-root needs a path}; shift ;;
    --project-git-author-name) PROJECT_GIT_AUTHOR_NAME=${2:?--project-git-author-name needs a value}; shift ;;
    --project-git-author-email) PROJECT_GIT_AUTHOR_EMAIL=${2:?--project-git-author-email needs a value}; shift ;;
    --accept-generated-sandbox) ACCEPT_GENERATED_SANDBOX=1 ;;
    --project-vision) PROJECT_VISION=${2:?--project-vision needs create|later}; shift ;;
    --runtime-bindings) RUNTIME_BINDINGS=${2:?}; shift ;;
    --accept-default-routing) ACCEPT_DEFAULT_ROUTING=1 ;;
    --routing-file) ROUTING_FILE=${2:?--routing-file needs a path}; shift ;;
    --accept-default-tunables) ACCEPT_DEFAULT_TUNABLES=1 ;;
    --review-wip-cap) REVIEW_WIP_CAP=${2:?--review-wip-cap needs a value}; shift ;;
    --timeout-minutes) TIMEOUT_MINUTES=${2:?--timeout-minutes needs a value}; shift ;;
    --grace-minutes) GRACE_MINUTES=${2:?--grace-minutes needs a value}; shift ;;
    --heartbeat-interval-s) HEARTBEAT_INTERVAL_S=${2:?--heartbeat-interval-s needs a value}; shift ;;
    --spawn-grace-s) SPAWN_GRACE_S=${2:?--spawn-grace-s needs a value}; shift ;;
    --hard-deadline-minutes) HARD_DEADLINE_MINUTES=${2:?--hard-deadline-minutes needs a value}; shift ;;
    --homie-idle-timeout-s) HOMIE_IDLE_TIMEOUT_S=${2:?--homie-idle-timeout-s needs a value}; shift ;;
    --accept-default-git-identity) ACCEPT_DEFAULT_GIT_IDENTITY=1 ;;
    --git-author-name) GIT_AUTHOR_NAME=${2:?--git-author-name needs a value}; shift ;;
    --git-author-email) GIT_AUTHOR_EMAIL=${2:?--git-author-email needs a value}; shift ;;
    --accept-default-dashboard-bind) ACCEPT_DEFAULT_DASHBOARD_BIND=1 ;;
    --dashboard-bind) DASHBOARD_BIND=${2:?--dashboard-bind needs a value}; shift ;;
    --dashboard-auth-token-file) DASHBOARD_AUTH_TOKEN_FILE=${2:?--dashboard-auth-token-file needs a path}; shift ;;
    --discord-mode) DISCORD_MODE=${2:?--discord-mode needs disabled|enabled}; shift ;;
    --acquire-runtime-auth) ACQUIRE_RUNTIME_AUTH=1 ;;
    --static-binding|--static-binding=*|--static-credential|--static-credential=*|--static-token-file|--static-token-file=*)
      fail "Static credentials are unsupported. Use the Codex or Claude Code sign-in." ;;
    --confirm-live-spend) CONFIRM_LIVE_SPEND=1 ;;
    --restore-latest) RESTORE_LATEST=1 ;;
    --activate) ACTIVATE=1 ;;
    --reconfigure) RECONFIGURE=1 ;;
    --smoke) SMOKE=1 ;;
    --smoke-approve-task) SMOKE_APPROVE_TASK=${2:?--smoke-approve-task needs an id}; shift ;;
    -h|--help) show_help; exit 0 ;;
    *) fail "unknown flag $1 (see --help)" ;;
  esac
  shift
done
}
parse_args "$@"

. "$REPO_DIR/scripts/source-update-guard.sh"
culler_require_source_update_idle "${MC_HOME_IN:-$HOME/.culler}" || exit 1

[ "$SMOKE" -eq 0 ] || [ "$DEV" -eq 0 ] || \
  fail "--smoke is production-only; development never exercises real provider or supervision routes"
[ -z "$SMOKE_APPROVE_TASK" ] || [ "$SMOKE" -eq 1 ] || \
  fail "--smoke-approve-task requires --smoke"
case "$SMOKE_APPROVE_TASK" in
  "") ;;
  *[!0-9]*|0) fail "--smoke-approve-task must be a positive task id" ;;
esac
[ "$SMOKE" -eq 0 ] || [ "$ACTIVATE" -eq 1 ] || \
  fail "--smoke requires --activate so the operator-present production resident is running"
[ "$ACCEPT_DEFAULT_ROUTING" -eq 0 ] || [ -z "$ROUTING_FILE" ] || \
  fail "--accept-default-routing cannot be combined with --routing-file"
HAS_TUNABLE_OVERRIDE=0
for value in "$REVIEW_WIP_CAP" "$TIMEOUT_MINUTES" "$GRACE_MINUTES" \
  "$HEARTBEAT_INTERVAL_S" "$SPAWN_GRACE_S" "$HARD_DEADLINE_MINUTES" "$HOMIE_IDLE_TIMEOUT_S"
do
  [ -z "$value" ] || HAS_TUNABLE_OVERRIDE=1
done
[ "$ACCEPT_DEFAULT_TUNABLES" -eq 0 ] || [ "$HAS_TUNABLE_OVERRIDE" -eq 0 ] || \
  fail "--accept-default-tunables cannot be combined with tunable overrides"
[ "$ACCEPT_DEFAULT_GIT_IDENTITY" -eq 0 ] || \
  { [ -z "$GIT_AUTHOR_NAME" ] && [ -z "$GIT_AUTHOR_EMAIL" ]; } || \
  fail "--accept-default-git-identity cannot be combined with a custom Git identity"
[ -z "$GIT_AUTHOR_NAME" ] && [ -z "$GIT_AUTHOR_EMAIL" ] || \
  { [ -n "$GIT_AUTHOR_NAME" ] && [ -n "$GIT_AUTHOR_EMAIL" ]; } || \
  fail "--git-author-name and --git-author-email are required together"
[ "$ACCEPT_DEFAULT_DASHBOARD_BIND" -eq 0 ] || [ -z "$DASHBOARD_BIND" ] || \
  fail "--accept-default-dashboard-bind cannot be combined with --dashboard-bind"
case "$DISCORD_MODE" in
  ""|disabled|enabled) ;;
  *) fail "--discord-mode must be disabled|enabled" ;;
esac

# binding_ids prints the binding ids of one TOML file on one line. Each id
# ends with a space. mc writes indented tables and quotes an id with a dot.
binding_ids() {
  sed -n \
    -e 's/^[[:space:]]*\[runtimes\.bindings\.\([a-z0-9_-]*\)\][[:space:]]*$/\1/p' \
    -e 's/^[[:space:]]*\[runtimes\.bindings\."\([a-z0-9._-]*\)"\][[:space:]]*$/\1/p' \
    "$1" 2>/dev/null | tr '\n' ' '
}

# check_runtime_bindings checks agent profiles in MC_HOME/config.toml. The
# reference bindings of the configuration template stand in for an absent
# config.toml. The development build also defines the fake binding.
check_runtime_bindings() {
  [ -n "$RUNTIME_BINDINGS" ] || return 0
  case ",$RUNTIME_BINDINGS," in
    *,,*) fail "--runtime-bindings contains an empty binding id. Remove the extra comma." ;;
  esac
  if [ -n "$MC_HOME_IN" ] && [ -f "$MC_HOME_IN/config.toml" ]; then
    _defined=$(binding_ids "$MC_HOME_IN/config.toml")
    # mc reports a config.toml without bindings.
    [ -n "$_defined" ] || return 0
  else
    _defined=$(binding_ids "$REPO_DIR/mc/config/config.example.toml")
    [ -n "$_defined" ] || \
      fail "The configuration template is unavailable. Run sh scripts/install-source.sh from a complete repository checkout."
  fi
  [ "$DEV" -eq 0 ] || _defined="${_defined}fake "
  _old_ifs=$IFS
  set -f
  # mc splits the list at commas only and trims the blanks around each id.
  IFS=,
  _seen=' '
  for _id in $RUNTIME_BINDINGS; do
    _id=${_id#"${_id%%[![:space:]]*}"}
    _id=${_id%"${_id##*[![:space:]]}"}
    [ -n "$_id" ] || fail "--runtime-bindings contains an empty binding id. Remove the extra comma."
    case "$_id" in *[[:space:]]*) _id_known=no ;; *) _id_known=yes ;; esac
    case "$_id_known: $_defined" in
      yes:*" $_id "*) ;;
      *)
        _list=$(printf '%s' "$_defined" | tr ' ' '\n' | LC_ALL=C sort -u | sed '/^$/d' | \
          tr '\n' ' ' | sed 's/ $//; s/ /, /g')
        fail "--runtime-bindings names \"$_id\", an undefined binding. The defined bindings are: $_list. Use one of these ids."
        ;;
    esac
    case "$_seen" in
      *" $_id "*) fail "--runtime-bindings names \"$_id\" twice. Remove the repeated id." ;;
    esac
    _seen="$_seen$_id "
  done
  set +f
  IFS=$_old_ifs
}
# A home that setup asks for later gets the check after the answer.
[ -z "$MC_HOME_IN" ] || check_runtime_bindings

interactive() { [ "$ASSUME_YES" -eq 0 ] && [ -t 0 ]; }
terminal_setup() { [ "$DEV" -eq 0 ] && [ "$SMOKE" -eq 0 ] && interactive; }

bootstrap_note() {
  _bootstrap_title=$1
  _bootstrap_body=$2
  _bootstrap_width=${COLUMNS:-80}
  case "$_bootstrap_width" in ''|*[!0-9]*) _bootstrap_width=80 ;; esac
  [ "$_bootstrap_width" -le 80 ] || _bootstrap_width=80
  [ "$_bootstrap_width" -ge 1 ] || _bootstrap_width=80
  printf '\n'
  if [ -t 1 ] && [ "${TERM:-dumb}" != dumb ] && [ "${NO_COLOR+x}" != x ]; then
    printf '\033[1;36m'
  fi
  printf '%s\n' "$_bootstrap_title" | fold -s -w "$_bootstrap_width"
  if [ -t 1 ] && [ "${TERM:-dumb}" != dumb ] && [ "${NO_COLOR+x}" != x ]; then
    printf '\033[0m'
  fi
  printf '%s\n\n' "$_bootstrap_body" | fold -s -w "$_bootstrap_width"
}

install_mise() {
  _mise_version=2026.9.1
  case "$OS:$(uname -m)" in
    Darwin:arm64) _mise_platform=macos-arm64 ;;
    Darwin:x86_64) _mise_platform=macos-x64 ;;
    Linux:aarch64|Linux:arm64) _mise_platform=linux-arm64 ;;
    Linux:x86_64) _mise_platform=linux-x64 ;;
    *) fail "This computer requires a manual mise installation. Install mise, then run sh scripts/install-source.sh again." ;;
  esac
  command -v curl >/dev/null 2>&1 && command -v shasum >/dev/null 2>&1 || \
    fail "The download verification tools are unavailable. Repair curl and shasum, then run sh scripts/install-source.sh again."
  say "› Download and verify mise"
  _mise_temp=$(mktemp -d "${TMPDIR:-/tmp}/mc-mise.XXXXXX")
  trap 'rm -rf "$_mise_temp"' EXIT HUP INT TERM
  _mise_asset="mise-v$_mise_version-$_mise_platform"
  _mise_release="https://github.com/jdx/mise/releases/download/v$_mise_version"
  curl --fail --silent --show-error --location --connect-timeout 15 --max-time 120 \
    "$_mise_release/$_mise_asset" --output "$_mise_temp/mise" >/dev/null 2>&1 && \
    curl --fail --silent --show-error --location --connect-timeout 15 --max-time 120 \
      "$_mise_release/SHASUMS256.txt" --output "$_mise_temp/checksums" >/dev/null 2>&1 || \
    fail "The mise download failed. Check your network connection, then run sh scripts/install-source.sh again."
  _mise_checksum=$(awk -v asset="$_mise_asset" '
    NF == 2 && ($2 == asset || $2 == "./" asset) {print $1}
  ' "$_mise_temp/checksums")
  case "$_mise_checksum" in
    ''|*[!0-9a-f]*) fail "The mise release checksum is missing or invalid. Ask your coding harness to inspect the release files." ;;
  esac
  [ "${#_mise_checksum}" -eq 64 ] || \
    fail "The mise release checksum is missing or invalid. Ask your coding harness to inspect the release files."
  printf '%s  mise\n' "$_mise_checksum" >"$_mise_temp/selected-checksum"
  ( cd "$_mise_temp" && shasum -a 256 -c selected-checksum >/dev/null 2>&1 ) || \
    fail "The mise download failed its checksum check. Run sh scripts/install-source.sh again to retry the download."
  mkdir -p "$HOME/.local/bin"
  install -m 755 "$_mise_temp/mise" "$HOME/.local/bin/mise" || \
    fail "The mise installation failed. Check your home directory permissions, then run sh scripts/install-source.sh again."
  rm -rf "$_mise_temp"
  trap - EXIT HUP INT TERM
  PATH="$HOME/.local/bin:$PATH"
  export PATH
  say "✓ mise is ready"
}

ask() {
  # ask <var-name> <prompt> [default]
  _var=$1; _prompt=$2; _default=${3:-}
  if interactive; then
    if [ -n "$_default" ]; then
      printf '%s [%s]: ' "$_prompt" "$_default"
    else
      printf '%s: ' "$_prompt"
    fi
    read -r _answer
    [ -n "$_answer" ] || _answer=$_default
  else
    _answer=$_default
  fi
  [ -n "$_answer" ] || fail "$_prompt is required (pass the matching flag for non-interactive runs)"
  eval "$_var=\$_answer"
}

ask_again() {
  # ask_again <var-name> <prompt>: ask until the answer is not empty.
  _var=$1; _prompt=$2; _answer=""
  while [ -z "$_answer" ]; do
    printf '%s: ' "$_prompt"
    read -r _answer || [ -n "$_answer" ] || {
      printf '\n'
      fail "The input ended before you answered the endpoint questions. Run sh scripts/install-source.sh again."
    }
  done
  eval "$_var=\$_answer"
}

ask_explicit() {
  # ask_explicit <var-name> <prompt> <interactive-default>
  _var=$1; _prompt=$2; _default=$3
  eval "_current=\${$_var}"
  if [ -n "$_current" ]; then
    return
  fi
  if interactive; then
    ask "$_var" "$_prompt" "$_default"
    return
  fi
  fail "$_prompt is required (pass the matching flag for non-interactive runs)"
}

ask_yes_no() {
  # ask_yes_no <prompt>: exit status 0 for yes.
  printf '%s [yes/no]: ' "$1"
  read -r _answer
  case "$_answer" in
    y|Y|yes|Yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

add_runtime_binding() {
  case ",$RUNTIME_BINDINGS," in
    *",$1,"*) ;;
    *) RUNTIME_BINDINGS="${RUNTIME_BINDINGS}${RUNTIME_BINDINGS:+,}$1" ;;
  esac
}

# collect_runtime_inputs asks for the supported sign-in connections.
collect_runtime_inputs() {
  say "Culler supports Codex sign-in and Claude Code sign-in."
  say "One account is enough. You can add more accounts later."
  if [ -n "$RUNTIME_BINDINGS" ]; then
    return
  fi
  interactive || fail "Select at least one agent profile with --runtime-bindings."
  if ask_yes_no "Do you have an OpenAI subscription?"; then
    add_runtime_binding chatgpt
    ACQUIRE_RUNTIME_AUTH=1
  fi
  if ask_yes_no "Do you have an Anthropic subscription?"; then
    add_runtime_binding claude
    ACQUIRE_RUNTIME_AUTH=1
  fi
  [ -n "$RUNTIME_BINDINGS" ] || fail "Select at least one agent profile with --runtime-bindings."
}

state_workspace_git() {
  _git_root=$(CDPATH= cd -- "$WORKSPACE_ROOT" 2>/dev/null && pwd -P) ||
    fail "first Project workspace root must be an available directory"
  WORKSPACE_ROOT=$_git_root
  say "Culler develops $WORKSPACE_ROOT with git."
  say "An existing repository there is adopted."
  say "If there is no repository, Culler runs \`git init\` in $WORKSPACE_ROOT."
}

# The workspace supplies the vision.
ask_project_vision() {
  case "$PROJECT_VISION" in
    ""|create|later) ;;
    *) fail "--project-vision must be create|later" ;;
  esac
  if [ -f "$WORKSPACE_ROOT/VISION.md" ]; then
    return 0
  fi
  say "VISION.md describes your project's open-ended, high-level goal. Culler develops strategy and proposes high-leverage work to advance that vision as your project develops. This vision gives Culler the direction it requires before it can start work."
  say "Work cannot start: $WORKSPACE_ROOT/VISION.md is required. Your agent can help you write it."
  [ -z "$PROJECT_VISION" ] || return 0
  if ! interactive; then
    return 0
  fi
  VISION_CHOICE=""
  ask VISION_CHOICE "create VISION.md at $WORKSPACE_ROOT now with your agent, or write it after setup (create/later)" "create"
  case "$VISION_CHOICE" in
    create|later) PROJECT_VISION=$VISION_CHOICE ;;
    *) fail "answer create or later" ;;
  esac
}

review_generated_sandbox() {
  _review_root=$(CDPATH= cd -- "$WORKSPACE_ROOT" 2>/dev/null && pwd -P) ||
    fail "first Project workspace root must be an available directory"
  WORKSPACE_ROOT=$_review_root
  say "generated sandbox profile v3 (acceptance is bound to these inputs):"
  say "   id: project-$PROJECT"
  say "   workspace_root: $WORKSPACE_ROOT"
  say "   workspace access: task-local Git stores"
  say "   artifact_roots: [$WORKSPACE_ROOT/.mc-artifacts]"
  say "   denied_paths: $HOME/{.ssh,.aws,.azure,.config,.docker,.gnupg,.kube,.codex,.claude,.netrc,.npmrc,.pypirc,.git-credentials} and $HOME/Library/Keychains"
  say "   tool_home_dir: $MC_HOME_IN/state/projects/$PROJECT/home"
  say "   runtime_control_dir: empty; harness_env_policy: {}; tool_env_policy: {}"
  if [ -n "$PROJECT_GIT_AUTHOR_NAME" ]; then
    say "   git_identity_override: $PROJECT_GIT_AUTHOR_NAME <$PROJECT_GIT_AUTHOR_EMAIL>"
  else
    say "   git_identity_override: null (inherit the deployment identity)"
  fi
  say "   tool_allowlist: []; runtime_auth_delivery: projection"
}

collect_project_git_identity() {
  if { [ -n "$PROJECT_GIT_AUTHOR_NAME" ] && [ -z "$PROJECT_GIT_AUTHOR_EMAIL" ]; } ||
     { [ -z "$PROJECT_GIT_AUTHOR_NAME" ] && [ -n "$PROJECT_GIT_AUTHOR_EMAIL" ]; }; then
    fail "--project-git-author-name and --project-git-author-email are required together"
  fi
  if [ -n "$PROJECT_GIT_AUTHOR_NAME" ] || ! interactive; then
    return
  fi
  _override=""
  ask _override "override the deployment Git identity for this Project (yes/no)" "no"
  if [ "$_override" = yes ]; then
    ask_explicit PROJECT_GIT_AUTHOR_NAME "Project Git author name" ""
    ask_explicit PROJECT_GIT_AUTHOR_EMAIL "Project Git author email" ""
  elif [ "$_override" != no ]; then
    fail "Project Git identity override answer must be yes or no"
  fi
}

collect_git_identity() {
  if [ "$ACCEPT_DEFAULT_GIT_IDENTITY" -eq 0 ] &&
    [ -z "$GIT_AUTHOR_NAME" ] && [ -z "$GIT_AUTHOR_EMAIL" ]; then
    if interactive; then
      ask GIT_AUTHOR_NAME "role Git author name (non-personal recommended)" "Culler"
      ask GIT_AUTHOR_EMAIL "role Git author email" "mc@localhost"
    else
      fail "Git identity choice is required (pass --accept-default-git-identity or both custom author flags)"
    fi
  fi
}

collect_surface_inputs() {
  if [ "$ACCEPT_DEFAULT_DASHBOARD_BIND" -eq 0 ] && [ -z "$DASHBOARD_BIND" ]; then
    if interactive; then
      ask DASHBOARD_BIND "dashboard bind address" "127.0.0.1:7333"
    else
      fail "dashboard bind choice is required (pass --accept-default-dashboard-bind or --dashboard-bind)"
    fi
  fi
  if [ -z "$DISCORD_MODE" ]; then
    if interactive; then
      ask DISCORD_MODE "Discord surface (disabled or enabled)" "disabled"
    else
      fail "Discord choice is required (pass --discord-mode disabled or enabled)"
    fi
  fi
  [ "$DISCORD_MODE" != enabled ] ||
    fail "Discord enablement awaits the resident connector and posted test-message gate; use --discord-mode disabled for this release"
  case "${DASHBOARD_BIND:-127.0.0.1:7333}" in
    127.0.0.1:*|localhost:*|'[::1]:'*) ;;
    *) [ -n "$DASHBOARD_AUTH_TOKEN_FILE" ] ||
      fail "a non-loopback dashboard bind requires --dashboard-auth-token-file" ;;
  esac
}

# --- 1. Prerequisites -------------------------------------------------------

if terminal_setup; then
  bootstrap_note "Culler" \
    "Welcome. First, let's prepare the setup tools."
else
  say "== prerequisites"
fi
OS=$(uname -s)
case "$OS" in
  Darwin|Linux) ;;
  *) fail "unsupported operating system $OS (expected macOS/launchd or Linux/systemd)" ;;
esac

if terminal_setup; then
  if [ "$OS" = Darwin ] && ! xcode-select -p >/dev/null 2>&1; then
    bootstrap_note "! Install Apple's developer tools" \
      "Complete the Apple installer, then run sh scripts/install-source.sh to resume setup."
    xcode-select --install >/dev/null 2>&1 || \
      fail "The Apple installer is unavailable. Install the developer tools from https://developer.apple.com/download/all/, then run sh scripts/install-source.sh again."
    exit 0
  fi
  command -v git >/dev/null 2>&1 && git --version >/dev/null 2>&1 || \
    fail "Git is unavailable. Repair the Apple developer tools, then run sh scripts/install-source.sh again."
  if ! command -v mise >/dev/null 2>&1; then
    for _mise_dir in "$HOME/.local/bin" "$HOME/.local/share/mise/bin" /opt/homebrew/bin /usr/local/bin; do
      if [ -x "$_mise_dir/mise" ]; then
        PATH="$_mise_dir:$PATH"
        export PATH
        break
      fi
    done
  fi
  command -v mise >/dev/null 2>&1 || install_mise
  say "› Prepare Go and Bun"
  ( cd "$REPO_DIR" && mise trust "$REPO_DIR/.mise.toml" >/dev/null 2>&1 ) || \
    fail "The repository tool configuration is unavailable. Resume setup from a complete repository checkout."
  ( cd "$REPO_DIR" && MISE_YES=1 mise install >/dev/null 2>&1 ) || \
    fail "The host tool installation failed. Check your network connection, then run sh scripts/install-source.sh again."
  ( cd "$REPO_DIR" && mise exec -- go version >/dev/null 2>&1 ) || \
    fail "The required Go tool is unavailable. Run sh scripts/install-source.sh again after you repair the mise installation."
  ( cd "$REPO_DIR" && mise exec -- bun --version >/dev/null 2>&1 ) || \
    fail "The required Bun tool is unavailable. Run sh scripts/install-source.sh again after you repair the mise installation."
  say "✓ Go and Bun are ready"
  RELEASE_BUILD_ID=$(git -C "$REPO_DIR" rev-parse --verify 'HEAD^{commit}') || \
    fail "The release identity is unavailable. Run sh scripts/install-source.sh from a complete repository checkout."
  case "$RELEASE_BUILD_ID" in
    *[!0-9a-f]*|'') fail "The release identity is invalid. Run sh scripts/install-source.sh from a complete repository checkout." ;;
  esac
  [ "${#RELEASE_BUILD_ID}" -ge 40 ] && [ "${#RELEASE_BUILD_ID}" -le 64 ] || \
    fail "The release identity is invalid. Run sh scripts/install-source.sh from a complete repository checkout."
  RELEASE_VERSION=$(cat "$REPO_DIR/VERSION" 2>/dev/null) || \
    fail "The release version is unavailable. Run sh scripts/install-source.sh from a complete repository checkout."
  case "$RELEASE_VERSION" in
    *[!0-9.]*|'') fail "The VERSION file must hold one version, for example 0.1.0. Repair the VERSION file, then run sh scripts/install-source.sh again." ;;
  esac
  printf '%s\n' "$RELEASE_VERSION" | grep -Eqx '(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)' || \
    fail "The VERSION file must hold one version, for example 0.1.0. Repair the VERSION file, then run sh scripts/install-source.sh again."
  [ -n "$BIN_DIR" ] || BIN_DIR="${MC_BIN_DIR:-$HOME/.local/bin}"
  SETUP_SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/mc-setup.XXXXXX")
  trap 'rm -rf "$SETUP_SCRATCH"' EXIT HUP INT TERM
  say "› Build the setup program"
  ( cd "$REPO_DIR/mc" && mise exec -- go build \
      -ldflags "-w -X main.releaseBuildID=$RELEASE_BUILD_ID -X main.releaseVersion=$RELEASE_VERSION" \
      -o "$SETUP_SCRATCH/mc" ./cmd/mc >/dev/null 2>&1 ) || \
    fail "The setup program build failed. Repair the host tools, then run sh scripts/install-source.sh again."
  say "✓ The setup program is ready"
  [ -n "$MC_HOME_IN" ] || MC_HOME_IN="$HOME/.culler"
  check_runtime_bindings
  export MC_HOME="$MC_HOME_IN"
  cd "$REPO_DIR"
  export MC_BIN_DIR="$BIN_DIR"
  mise exec -- "$SETUP_SCRATCH/mc" __setup "$@" --release-source "$REPO_DIR/runner" \
    --host-release-source "$REPO_DIR"
  exit $?
fi

command -v git >/dev/null 2>&1 || fail "git is required and was not found on PATH"
command -v docker >/dev/null 2>&1 || \
  fail "docker CLI is required — install Docker Desktop, then re-run the front door"
if ! docker info >/dev/null 2>&1; then
  if [ "$OS" = Darwin ] && command -v open >/dev/null 2>&1; then
    say "   docker daemon is not responding — starting Docker Desktop"
    open -a Docker >/dev/null 2>&1 || true
    docker_ready=0
    attempt=0
    while [ "$attempt" -lt 60 ]; do
      if docker info >/dev/null 2>&1; then
        docker_ready=1
        break
      fi
      sleep 1
      attempt=$((attempt + 1))
    done
    [ "$docker_ready" -eq 1 ] || \
      fail "docker daemon is not responding after 60s — check Docker Desktop health, then re-run the front door"
  else
    fail "docker daemon is not responding — start the container runtime, then re-run the front door"
  fi
fi
command -v mise >/dev/null 2>&1 || fail "mise is required (https://mise.jdx.dev) — it pins the toolchain this repo builds with"
( cd "$REPO_DIR" && mise install >/dev/null 2>&1 ) || true
( cd "$REPO_DIR" && mise exec -- go version >/dev/null 2>&1 ) || fail "the pinned Go toolchain is unavailable (try: mise install)"
say "   git, toolchain, container runtime: ok"

RELEASE_BUILD_ID=$(git -C "$REPO_DIR" rev-parse --verify 'HEAD^{commit}') || \
  fail "cannot resolve the immutable release commit"
case "$RELEASE_BUILD_ID" in
  *[!0-9a-f]*|'') fail "release commit is not lowercase hexadecimal" ;;
esac
[ "${#RELEASE_BUILD_ID}" -ge 40 ] && [ "${#RELEASE_BUILD_ID}" -le 64 ] || \
  fail "release commit has an unsupported length"
RELEASE_VERSION=$(cat "$REPO_DIR/VERSION" 2>/dev/null) || fail "the VERSION file is absent"
case "$RELEASE_VERSION" in
  *[!0-9.]*|'') fail "the VERSION file must hold one version, for example 0.1.0" ;;
esac
printf '%s\n' "$RELEASE_VERSION" | grep -Eqx '(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)' || \
  fail "the VERSION file must hold one version, for example 0.1.0"

# --- 2. Build / install mc --------------------------------------------------

if [ "$DEV" -eq 1 ]; then
  ask MC_HOME_IN "deployment home (MC_HOME, a scratch path outside any git tree)" "$MC_HOME_IN"
  check_runtime_bindings
  [ -n "$BIN_DIR" ] || BIN_DIR="$MC_HOME_IN/bin"
  mkdir -p "$MC_HOME_IN" "$BIN_DIR"
  chmod 700 "$MC_HOME_IN"
  say "== build (development tier, direct spine)"
  ( cd "$REPO_DIR/mc" && mise exec -- go build -tags test_fake_routing -ldflags "-w" -o "$BIN_DIR/mc" ./cmd/mc )
else
  [ -n "$BIN_DIR" ] || BIN_DIR="${MC_BIN_DIR:-$HOME/.local/bin}"
  export MC_BIN_DIR="$BIN_DIR"
  SETUP_SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/mc-setup.XXXXXX")
  trap 'rm -rf "$SETUP_SCRATCH"' EXIT HUP INT TERM
  say "== build (production)"
  ( cd "$REPO_DIR/mc" && mise exec -- go build \
      -ldflags "-w -X main.releaseBuildID=$RELEASE_BUILD_ID -X main.releaseVersion=$RELEASE_VERSION" \
      -o "$SETUP_SCRATCH/mc" ./cmd/mc )
  say "== build (production image, native arm64)"
  ( cd "$REPO_DIR" && MC_RELEASE_BUILD_ID="$RELEASE_BUILD_ID" ./runner/image/build-prod.sh ) || \
    fail "production image build failed — fix the reported build/runtime error, then re-run the front door"
fi
[ "$DEV" -eq 0 ] || say "   installed $BIN_DIR/mc"

# --- 3. Hand off to the wizard ---------------------------------------------

if [ "$DEV" -eq 0 ]; then
  ask_explicit MC_HOME_IN "deployment home (MC_HOME)" "$HOME/.culler"
  check_runtime_bindings
fi
collect_runtime_inputs
if [ "$ACCEPT_DEFAULT_ROUTING" -eq 0 ] && [ -z "$ROUTING_FILE" ]; then
  if interactive; then
    ROUTING_CHOICE=""
    ask ROUTING_CHOICE \
      "role routing (type default, or an absolute routing.md path)" "default"
    if [ "$ROUTING_CHOICE" = default ]; then
      ACCEPT_DEFAULT_ROUTING=1
    else
      ROUTING_FILE=$ROUTING_CHOICE
    fi
  else
    fail "role routing choice is required (pass --accept-default-routing or --routing-file)"
  fi
fi
if [ "$ACCEPT_DEFAULT_TUNABLES" -eq 0 ] && [ "$HAS_TUNABLE_OVERRIDE" -eq 0 ]; then
  if interactive; then
    TUNABLES_CHOICE=""
    ask TUNABLES_CHOICE \
      "tunables (type defaults, or customize REVIEW_WIP_CAP and timing)" "defaults"
    if [ "$TUNABLES_CHOICE" = defaults ]; then
      ACCEPT_DEFAULT_TUNABLES=1
    else
      ask REVIEW_WIP_CAP \
        "REVIEW_WIP_CAP (daily review load; positive integer)" "3"
      ask TIMEOUT_MINUTES "lease timeout minutes" "60"
      ask GRACE_MINUTES "lease grace minutes" "15"
      ask HEARTBEAT_INTERVAL_S "heartbeat interval seconds" "60"
      ask SPAWN_GRACE_S "first-heartbeat grace seconds" "60"
    fi
  else
    fail "tunables choice is required (pass --accept-default-tunables or explicit tunable flags)"
  fi
fi
collect_git_identity

if [ "$DEV" -eq 0 ]; then
  export MC_HOME="$MC_HOME_IN"
  say "== hand-off: mc __onboard preflight"
  "$SETUP_SCRATCH/mc" __onboard preflight
  say "== hand-off: mc __onboard home (through the managed helper)"
  set -- "$SETUP_SCRATCH/mc" __onboard home --release-source "$REPO_DIR/runner" \
    --host-release-source "$REPO_DIR"
  [ "$RESTORE_LATEST" -eq 0 ] || set -- "$@" --restore-latest
  "$@"
  rm -rf "$SETUP_SCRATCH"
  trap - EXIT HUP INT TERM
  say "   installed $BIN_DIR/mc"
  say "== hand-off: mc __onboard agentsview (the pinned AgentsView executable)"
  "$BIN_DIR/mc" __onboard agentsview || \
    fail "The AgentsView install failed. Fix the error above, then run sh scripts/install-source.sh again."
  say "== hand-off: remaining mc onboard sections"
  if [ -n "$PROJECT$WORKSPACE_ROOT$PROJECT_GIT_AUTHOR_NAME$PROJECT_VISION" ] || [ "$ACCEPT_GENERATED_SANDBOX" -eq 1 ] || [ "$SMOKE" -eq 1 ]; then
    ask PROJECT "first Project id" "$PROJECT"
    ask WORKSPACE_ROOT "first Project workspace root (absolute path)" "$WORKSPACE_ROOT"
    state_workspace_git
    ask_project_vision
    collect_project_git_identity
    review_generated_sandbox
    if [ "$ACCEPT_GENERATED_SANDBOX" -eq 0 ]; then
      if interactive; then
        SANDBOX_REVIEW=""
        ask SANDBOX_REVIEW "accept this generated deny-by-default sandbox profile (yes/no)" "no"
        [ "$SANDBOX_REVIEW" = yes ] || fail "the first Project sandbox profile was not accepted"
        ACCEPT_GENERATED_SANDBOX=1
      else
        fail "sandbox profile review is required (pass --accept-generated-sandbox after reviewing it)"
      fi
    fi
  fi
  collect_surface_inputs
  set -- "$BIN_DIR/mc" __onboard
  if [ -n "$PROJECT" ]; then
    set -- "$@" --project "$PROJECT" --workspace-root "$WORKSPACE_ROOT" --accept-generated-sandbox
  fi
  [ -z "$PROJECT_VISION" ] || set -- "$@" --project-vision "$PROJECT_VISION"
  [ -z "$PROJECT_GIT_AUTHOR_NAME" ] || set -- "$@" \
    --project-git-author-name "$PROJECT_GIT_AUTHOR_NAME" \
    --project-git-author-email "$PROJECT_GIT_AUTHOR_EMAIL"
  set -- "$@" --runtime-bindings "$RUNTIME_BINDINGS"
  [ "$ACCEPT_DEFAULT_ROUTING" -eq 0 ] || set -- "$@" --accept-default-routing
  [ -z "$ROUTING_FILE" ] || set -- "$@" --routing-file "$ROUTING_FILE"
  [ "$ACCEPT_DEFAULT_TUNABLES" -eq 0 ] || set -- "$@" --accept-default-tunables
  [ -z "$REVIEW_WIP_CAP" ] || set -- "$@" --review-wip-cap "$REVIEW_WIP_CAP"
  [ -z "$TIMEOUT_MINUTES" ] || set -- "$@" --timeout-minutes "$TIMEOUT_MINUTES"
  [ -z "$GRACE_MINUTES" ] || set -- "$@" --grace-minutes "$GRACE_MINUTES"
  [ -z "$HEARTBEAT_INTERVAL_S" ] || set -- "$@" --heartbeat-interval-s "$HEARTBEAT_INTERVAL_S"
  [ -z "$SPAWN_GRACE_S" ] || set -- "$@" --spawn-grace-s "$SPAWN_GRACE_S"
  [ -z "$HARD_DEADLINE_MINUTES" ] || set -- "$@" --hard-deadline-minutes "$HARD_DEADLINE_MINUTES"
  [ -z "$HOMIE_IDLE_TIMEOUT_S" ] || set -- "$@" --homie-idle-timeout-s "$HOMIE_IDLE_TIMEOUT_S"
  [ "$ACCEPT_DEFAULT_GIT_IDENTITY" -eq 0 ] || set -- "$@" --accept-default-git-identity
  [ -z "$GIT_AUTHOR_NAME" ] || set -- "$@" --git-author-name "$GIT_AUTHOR_NAME" --git-author-email "$GIT_AUTHOR_EMAIL"
  [ "$ACCEPT_DEFAULT_DASHBOARD_BIND" -eq 0 ] || set -- "$@" --accept-default-dashboard-bind
  [ -z "$DASHBOARD_BIND" ] || set -- "$@" --dashboard-bind "$DASHBOARD_BIND"
  [ -z "$DASHBOARD_AUTH_TOKEN_FILE" ] || set -- "$@" --dashboard-auth-token-file "$DASHBOARD_AUTH_TOKEN_FILE"
  set -- "$@" --discord-mode "$DISCORD_MODE"
  [ "$ACQUIRE_RUNTIME_AUTH" -eq 0 ] || set -- "$@" --acquire
  [ "$CONFIRM_LIVE_SPEND" -eq 0 ] || set -- "$@" --confirm-live-spend
  [ "$RESTORE_LATEST" -eq 0 ] || set -- "$@" --restore-latest
  [ "$ACTIVATE" -eq 0 ] || set -- "$@" --activate
  [ "$RECONFIGURE" -eq 0 ] || set -- "$@" --reconfigure
  if [ "$SMOKE" -eq 0 ]; then
    if [ "$ASSUME_YES" -eq 1 ]; then
      exec "$@" </dev/null
    fi
    exec "$@"
  fi
  "$@"
  say "== acceptance: real full-pipeline smoke"
  set -- "$BIN_DIR/mc" __onboard --smoke
  [ -z "$SMOKE_APPROVE_TASK" ] || set -- "$@" --smoke-approve-task "$SMOKE_APPROVE_TASK"
  if [ "$ASSUME_YES" -eq 1 ]; then
    exec "$@" </dev/null
  fi
  exec "$@"
fi

ask PROJECT "first Project id" "$PROJECT"
ask WORKSPACE_ROOT "first Project workspace root (absolute path)" "$WORKSPACE_ROOT"
state_workspace_git
ask_project_vision
collect_project_git_identity
review_generated_sandbox
if [ "$ACCEPT_GENERATED_SANDBOX" -eq 0 ]; then
  if interactive; then
    SANDBOX_REVIEW=""
    ask SANDBOX_REVIEW "accept this generated deny-by-default sandbox profile (yes/no)" "no"
    [ "$SANDBOX_REVIEW" = yes ] || fail "the first Project sandbox profile was not accepted"
    ACCEPT_GENERATED_SANDBOX=1
  else
    fail "sandbox profile review is required (pass --accept-generated-sandbox after reviewing it)"
  fi
fi
collect_surface_inputs

say "== hand-off: mc __onboard (development tier)"
set -- "$BIN_DIR/mc" __onboard \
  --release-source "$REPO_DIR/runner" \
  --host-release-source "$REPO_DIR" \
  --project "$PROJECT" --workspace-root "$WORKSPACE_ROOT" \
  --accept-generated-sandbox
[ -z "$PROJECT_VISION" ] || set -- "$@" --project-vision "$PROJECT_VISION"
set -- "$@" --runtime-bindings "$RUNTIME_BINDINGS"
[ -z "$PROJECT_GIT_AUTHOR_NAME" ] || set -- "$@" \
  --project-git-author-name "$PROJECT_GIT_AUTHOR_NAME" \
  --project-git-author-email "$PROJECT_GIT_AUTHOR_EMAIL"
[ "$ACCEPT_DEFAULT_ROUTING" -eq 0 ] || set -- "$@" --accept-default-routing
[ -z "$ROUTING_FILE" ] || set -- "$@" --routing-file "$ROUTING_FILE"
[ "$ACCEPT_DEFAULT_TUNABLES" -eq 0 ] || set -- "$@" --accept-default-tunables
[ -z "$REVIEW_WIP_CAP" ] || set -- "$@" --review-wip-cap "$REVIEW_WIP_CAP"
[ -z "$TIMEOUT_MINUTES" ] || set -- "$@" --timeout-minutes "$TIMEOUT_MINUTES"
[ -z "$GRACE_MINUTES" ] || set -- "$@" --grace-minutes "$GRACE_MINUTES"
[ -z "$HEARTBEAT_INTERVAL_S" ] || set -- "$@" --heartbeat-interval-s "$HEARTBEAT_INTERVAL_S"
[ -z "$SPAWN_GRACE_S" ] || set -- "$@" --spawn-grace-s "$SPAWN_GRACE_S"
[ -z "$HARD_DEADLINE_MINUTES" ] || set -- "$@" --hard-deadline-minutes "$HARD_DEADLINE_MINUTES"
[ -z "$HOMIE_IDLE_TIMEOUT_S" ] || set -- "$@" --homie-idle-timeout-s "$HOMIE_IDLE_TIMEOUT_S"
[ "$ACCEPT_DEFAULT_GIT_IDENTITY" -eq 0 ] || set -- "$@" --accept-default-git-identity
[ -z "$GIT_AUTHOR_NAME" ] || set -- "$@" --git-author-name "$GIT_AUTHOR_NAME" --git-author-email "$GIT_AUTHOR_EMAIL"
[ "$ACCEPT_DEFAULT_DASHBOARD_BIND" -eq 0 ] || set -- "$@" --accept-default-dashboard-bind
[ -z "$DASHBOARD_BIND" ] || set -- "$@" --dashboard-bind "$DASHBOARD_BIND"
[ -z "$DASHBOARD_AUTH_TOKEN_FILE" ] || set -- "$@" --dashboard-auth-token-file "$DASHBOARD_AUTH_TOKEN_FILE"
set -- "$@" --discord-mode "$DISCORD_MODE"
MC_HOME="$MC_HOME_IN" MC_SPINE="$MC_HOME_IN/spine.db" exec "$@"
