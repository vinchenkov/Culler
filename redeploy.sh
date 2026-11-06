#!/bin/sh
# redeploy.sh moves the live Culler deployment to this checkout.
#
#   ./redeploy.sh                   Keep the deployed database.
#   ./redeploy.sh --reset-database  Replace the deployed database with an
#                                   empty database.
#
# The script builds the new release, checks the deployed database, installs the
# pinned AgentsView executable, stops the running units, deploys the release,
# and runs mc doctor. A rerun is safe: it rebuilds the same release, finds the
# units stopped, and each onboard section without a change reports `ok`.
#
# Order: the production image build refuses (exit 3) while a com.culler.* unit
# is loaded, because the helper comes from that image. A rebuilt image under a
# running deployment is a release identity mismatch at the next tick. Thus the
# script builds the host mc and the Linux mc into a scratch path first, so a
# compile error leaves the deployment running. The database check and the
# AgentsView install also run before the stop. Then the units stop, the image
# builds, and the release installs.
#
# Database: the deploy step keeps the deployed database. The board reaps and
# retries a run that was in flight. Before any change, the scratch host mc
# starts a container of the deployed image. In that container, the Linux mc of
# this release opens the database read-only through a read-only mount. When the
# database schema differs from this release, or the database is absent, the
# script stops, and nothing changed. When the check cannot run, the script
# continues, and the deploy step makes the same check after the units stop.
# That later stop keeps the database and names the commit of the previously
# deployed release. The supervision record in the deployment home names that
# commit. The org.opencontainers.image.revision label of the mc-prod image is
# the second record, and the script reads it before the image build replaces
# the image.
#
# --reset-database replaces the deployed database with an empty database with
# the current schema. The script omits the database check. Before the first
# build step, the script names the deployment home and asks the operator to
# type a word at a terminal. A run without a terminal or without that word
# changes nothing. The reset removes the agent and Homie containers that use
# the database. The reset keeps the Project repositories and worktrees, the
# credentials, config.toml, and the deployment identity. The tunables section
# then copies the config.toml settings into the database. The empty database
# has the default alert routes, so the surfaces section then copies the
# config.toml alert routes into the database.
#
# Production tier only. The development tier is install.sh --dev.
#
# Environment: MC_HOME (the deployment home; mc's default ~/.culler when unset)
# and MC_BIN_DIR (where mc is installed; defaults to the installed mc's
# directory, then ~/.local/bin, the install.sh convention). COLUMNS sets the
# width of the help and the database messages, from 40 to 80 columns.
set -eu

REPO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

say() { printf '%s\n' "$*"; }
fail() { printf 'redeploy.sh: %s\n' "$*" >&2; exit 1; }

message_width() {
  width=${COLUMNS:-}
  if [ -z "$width" ]; then
    width=$( { stty size </dev/tty; } 2>/dev/null | cut -d ' ' -f 2) || width=
  fi
  case "$width" in ''|*[!0-9]*|????*) width=80 ;; esac
  [ "$width" -ge 40 ] || width=40
  [ "$width" -le 80 ] || width=80
  printf '%s' "$width"
}
paragraph() { printf '%s\n' "$*" | fold -s -w "$WIDTH" | sed 's/ *$//'; }
item() {
  printf '%s\n' "$*" | fold -s -w $((WIDTH - 2)) | sed -e 's/ *$//' -e '1s/^/- /' -e '1!s/^/  /'
}
command_line() { printf '%s\n' "$*" | sed 's/^/    /'; }

DEPLOYMENT_HOME=${MC_HOME:-$HOME/.culler}

# display_home prints the deployment home, with ~ for the home directory.
display_home() {
  case "$DEPLOYMENT_HOME" in
    "$HOME") printf '~' ;;
    "$HOME"/*) printf '~/%s' "${DEPLOYMENT_HOME#"$HOME"/}" ;;
    *) printf '%s' "$DEPLOYMENT_HOME" ;;
  esac
}

# previous_release prints the commit of the previously deployed release, or
# nothing when no record names a commit other than this release.
previous_release() {
  supervision="$DEPLOYMENT_HOME/supervision/resident.json"
  commit=$(sed -n 's/^[[:space:]]*"releaseBuildId":[[:space:]]*"\([0-9a-f]*\)".*/\1/p' "$supervision" 2>/dev/null | head -n 1) || commit=
  case "$commit" in ''|"$RELEASE_BUILD_ID") commit=$IMAGE_RELEASE ;; esac
  case "$commit" in ''|*[!0-9a-f]*|"$RELEASE_BUILD_ID") commit= ;; esac
  printf '%s' "$commit"
}

# keep_records_action sets KEEP_ACTION and KEEP_COMMANDS to the action and the
# commands that deploy the previously deployed release again. A full commit
# that does not fit the message width shows as its first 12 characters.
keep_records_action() {
  commit=$(previous_release)
  if [ -z "$commit" ]; then
    KEEP_ACTION="Keep the database records: redeploy.sh found no record of the previously deployed release. Check out the commit that you deployed last, then run:"
    KEEP_COMMANDS="./redeploy.sh"
    return
  fi
  if [ $((${#commit} + 17)) -gt "$(message_width)" ]; then
    commit=$(printf '%.12s' "$commit")
  fi
  KEEP_ACTION="Keep the database records: deploy the previously deployed release again. Run these commands:"
  KEEP_COMMANDS=$(printf 'git checkout %s\n./redeploy.sh' "$commit")
}

# database_stop explains a stop that kept the database, then exits. Its
# arguments are the cause, the deployment state, and pairs of an action and its
# commands, one command on each line. An action without a command has an empty
# command.
database_stop() {
  WIDTH=$(message_width)
  {
    paragraph "redeploy.sh: Stopped. $1"
    say ""
    paragraph "$2"
    shift 2
    say ""
    if [ "$#" -gt 2 ]; then
      paragraph "Choose one action:"
    else
      paragraph "Next action:"
    fi
    while [ "$#" -ge 2 ]; do
      item "$1"
      [ -z "$2" ] || command_line "$2"
      shift 2
    done
  } >&2
  exit 1
}

service_control_stop() {
  WIDTH=$(message_width)
  {
    paragraph "redeploy.sh: Stopped. $1"
    say ""
    paragraph "The service state is unverified. This attempt kept the container image, Culler executable, and database unchanged."
    say ""
    paragraph "Details: $2"
    say ""
    paragraph "Open Terminal in this checkout and run ./redeploy.sh again."
    paragraph "If the error continues, give this output to your coding harness."
  } >&2
  exit 1
}

read_units() {
  unit_status=0
  UNITS=$(launchctl list 2>"$SCRATCH/launchctl.err") || unit_status=$?
  if [ "$unit_status" -ne 0 ]; then
    service_control_stop "The macOS service check failed." \
      "launchctl list exited with $unit_status. $(cat "$SCRATCH/launchctl.err")"
  fi
}
units_loaded() {
  read_units
  printf '%s\n' "$UNITS" | grep -q 'com\.culler\.'
}

# early_database_stop explains a database check that stopped the script before
# any change, then exits. Its argument is the state that the check found.
early_database_stop() {
  if units_loaded; then
    services="The Culler services still run the deployed release."
    [ "$1" = absent ] || services="The Culler services still run the deployed release with its database."
    keep_action="Keep the database records: no action is necessary. The deployed release continues to run."
    keep_commands=
  else
    services="The Culler services are stopped."
    [ "$1" = absent ] || services="The Culler services are stopped. The deployment keeps its database."
    keep_records_action
    keep_action=$KEEP_ACTION
    keep_commands=$KEEP_COMMANDS
  fi
  if [ "$1" = differs ]; then
    database_stop "The deployed database has a different schema from this release. Nothing changed." \
      "$services" \
      "Replace the database with an empty database:" "./redeploy.sh --reset-database" \
      "$keep_action" "$keep_commands"
  fi
  database_stop "The deployed database is absent. Nothing changed." \
    "$services" \
    "Create an empty database:" "./redeploy.sh --reset-database"
}

RESET_DATABASE=0
for argument do
  case "$argument" in
    --reset-database) RESET_DATABASE=1 ;;
    -h|--help)
      WIDTH=$(message_width)
      paragraph "Usage: ./redeploy.sh [--reset-database]"
      say ""
      paragraph "redeploy.sh moves the live Culler deployment to this checkout. The deployment keeps its database. A schema difference stops redeploy.sh and keeps the database."
      say ""
      paragraph "Option:"
      item "--reset-database: Replace the database with an empty database. You confirm the reset at a terminal."
      exit 0 ;;
    *) fail "unknown option $argument; the only option is --reset-database" ;;
  esac
done

command -v git >/dev/null 2>&1 || fail "git is required"
command -v mise >/dev/null 2>&1 || fail "mise is required (it pins the toolchain this repo builds with)"
command -v docker >/dev/null 2>&1 || fail "docker CLI is required"

if [ "$RESET_DATABASE" -eq 1 ]; then
  WIDTH=$(message_width)
  if ! tty -s; then
    {
      paragraph "redeploy.sh: The database reset needs your confirmation at a terminal. Nothing changed."
      say ""
      paragraph "Run this command in a terminal, then type reset when it asks:"
      command_line "./redeploy.sh --reset-database"
    } >&2
    exit 1
  fi
  paragraph "The database reset replaces the database of the deployment at $(display_home) with an empty database."
  say ""
  paragraph "The reset deletes all database records, for example these records:"
  item "Projects, tasks, runs, and review packets."
  item "Homie conversations."
  item "Project setup records and Project role customizations."
  item "The settings outside config.toml."
  say ""
  paragraph "The reset stops the agent and Homie containers that use the database."
  say ""
  paragraph "The reset keeps these items:"
  item "The Project repositories and worktrees."
  item "The credentials and config.toml."
  item "The deployment identity."
  say ""
  paragraph "After the reset, redeploy.sh copies the config.toml settings into the new database, including the Discord alert destinations. Then add each Project again in the dashboard."
  say ""
  paragraph "Type reset to replace the database. Press Return to cancel."
  printf 'Type reset: '
  answer=
  IFS= read -r answer || answer=
  case "$answer" in
    reset) ;;
    *[![:space:]]*)
      typed=$(printf '%s' "$answer" | tr -d '[:cntrl:]' | cut -c 1-20)
      {
        say ""
        paragraph "You typed $typed, not reset. Nothing changed."
        say ""
        paragraph "To replace the database, run this command again and type reset:"
        command_line "./redeploy.sh --reset-database"
      } >&2
      exit 1 ;;
    *)
      say ""
      paragraph "The reset is cancelled. Nothing changed." >&2
      exit 1 ;;
  esac
  say ""
fi

RELEASE_BUILD_ID=$(git -C "$REPO_DIR" rev-parse --verify 'HEAD^{commit}') || \
  fail "cannot resolve the release commit"
case "$RELEASE_BUILD_ID" in
  *[!0-9a-f]*|'') fail "release commit is not lowercase hexadecimal" ;;
esac
RELEASE_VERSION=$(cat "$REPO_DIR/VERSION" 2>/dev/null) || fail "the VERSION file is absent"
case "$RELEASE_VERSION" in
  *[!0-9.]*|'') fail "the VERSION file must hold one version, for example 0.1.0" ;;
esac
printf '%s\n' "$RELEASE_VERSION" | grep -Eqx '(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)' || \
  fail "the VERSION file must hold one version, for example 0.1.0"

if [ -n "${MC_BIN_DIR:-}" ]; then
  BIN_DIR=$MC_BIN_DIR
elif installed=$(command -v mc 2>/dev/null) && [ -n "$installed" ]; then
  BIN_DIR=$(dirname -- "$installed")
else
  BIN_DIR="$HOME/.local/bin"
fi
MC_BIN_DIR=$BIN_DIR
export MC_BIN_DIR

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/mc-redeploy.XXXXXX")
trap 'rm -rf "$SCRATCH"' EXIT

read_units

say "== release $RELEASE_BUILD_ID"

say "== build: host mc"
( cd "$REPO_DIR/mc" && mise exec -- go build \
    -ldflags "-w -X main.releaseBuildID=$RELEASE_BUILD_ID -X main.releaseVersion=$RELEASE_VERSION" \
    -o "$SCRATCH/mc" ./cmd/mc ) || fail "host mc build failed; the deployment was not touched"

IMAGE_RELEASE=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' mc-prod 2>/dev/null) || \
  IMAGE_RELEASE=

if [ "$RESET_DATABASE" -eq 0 ]; then
  say "== build: Linux mc (the database check)"
  ( cd "$REPO_DIR/mc" && GOFLAGS= GOENV=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 mise exec -- go build \
      -ldflags "-w -X main.releaseBuildID=$RELEASE_BUILD_ID -X main.releaseVersion=$RELEASE_VERSION" \
      -o "$SCRATCH/mc-linux" ./cmd/mc ) || fail "Linux mc build failed; the deployment was not touched"

  say "== check: mc __redeploy-check (the deployed database, read-only)"
  check_status=0
  "$SCRATCH/mc" __redeploy-check "$SCRATCH/mc-linux" >"$SCRATCH/check.out" 2>"$SCRATCH/check.err" || check_status=$?
  reason=$(sed -n '1s/^mc: //p' "$SCRATCH/check.err")
  state=$(sed -n 's/.*"state":"\([a-z]*\)".*/\1/p' "$SCRATCH/check.out")
  case "$check_status:$state" in
    0:current) say "   the deployed database has the schema of this release" ;;
    0:new) say "   the deployment has no database; the deploy step creates an empty database" ;;
    0:differs|0:absent) early_database_stop "$state" ;;
    0:*|2:*)
      say "   the database check could not run: ${reason:-it returned no result}"
      say "   the deploy step checks the database after the units stop" ;;
    *)
      WIDTH=$(message_width)
      if units_loaded; then
        services="The Culler services still run the deployed release with its database."
      else
        services="The Culler services are stopped. The deployment keeps its database."
      fi
      {
        paragraph "redeploy.sh: Stopped. The database check refused the deployment. Nothing changed."
        [ -z "$reason" ] || { say ""; paragraph "$reason"; }
        say ""
        paragraph "$services"
        say ""
        paragraph "Next action:"
        item "Give this message to your coding harness. Ask it to repair the deployment with the onboard skill or install.sh."
      } >&2
      exit 1 ;;
  esac
fi

say "== install: mc __onboard agentsview (the pinned AgentsView executable)"
"$SCRATCH/mc" __onboard agentsview || \
  fail "the AgentsView install stopped and left the deployment running; rerun after fixing what it names"

say "== stop: supervised units"
stopped=0
for plist in "$HOME/Library/LaunchAgents"/com.culler.*.plist; do
  [ -e "$plist" ] || continue
  label=$(basename "$plist" .plist)
  if launchctl bootout "gui/$(id -u)/$label" >"$SCRATCH/bootout.out" 2>&1; then
    WIDTH=$(message_width)
    paragraph "   Stop requested: $label"
  else
    bootout_status=$?
    # An empty service list can describe another domain. Check the target service.
    inspect_status=0
    inspection=$(launchctl print "gui/$(id -u)/$label" 2>&1) || inspect_status=$?
    if [ "$inspect_status" -eq 113 ] && \
       printf '%s\n' "$inspection" | grep -Eiq 'could not find service|service not found'; then
      say "   $label already stopped"
    else
      service_control_stop "A Culler service could not stop." \
        "launchctl bootout exited with $bootout_status for $label. $(cat "$SCRATCH/bootout.out") Service inspection exited with $inspect_status. $inspection"
    fi
  fi
  stopped=1
done
[ "$stopped" -eq 1 ] || say "   no supervised units found"

# `launchctl bootout` returns before the unit leaves `launchctl list`, and the
# image build below refuses while any com.culler.* label is listed.
# Wait, bounded to a few seconds, for the labels to clear.
polls=0
while units_loaded; do
  polls=$((polls + 1))
  [ "$polls" -lt 50 ] || fail "a supervised Culler deployment is loaded."
  sleep 0.1
done

say "== build: production image"
if ! ( cd "$REPO_DIR" && MC_RELEASE_BUILD_ID="$RELEASE_BUILD_ID" ./runner/image/build-prod.sh ); then
  WIDTH=$(message_width)
  {
    paragraph "redeploy.sh: Stopped. The production image build failed."
    say ""
    paragraph "This attempt kept the Culler executable and database unchanged. Service activation did not run."
    say ""
    paragraph "Give this output to your coding harness. Ask it to resolve the error before you run ./redeploy.sh again."
  } >&2
  exit 1
fi

say "== install: $BIN_DIR/mc"
mkdir -p "$BIN_DIR"
cp "$SCRATCH/mc" "$BIN_DIR/mc.redeploy" && mv -f "$BIN_DIR/mc.redeploy" "$BIN_DIR/mc"

set -- __onboard home
if [ "$RESET_DATABASE" -eq 1 ]; then
  say "== deploy: mc __onboard home (runner and host release; an empty database)"
  set -- "$@" --allow-database-reset
else
  say "== deploy: mc __onboard home (runner and host release; the deployed database)"
fi
set -- "$@" --release-source "$REPO_DIR/runner" --host-release-source "$REPO_DIR"
LATE_STATE="The Culler services are stopped. This release's command-line tool and container image are installed. The deployment home keeps the other files of the previous release."
if ! "$BIN_DIR/mc" "$@" >"$SCRATCH/home.out" 2>"$SCRATCH/home.err"; then
  if grep -q '"code":"database-schema-differs"' "$SCRATCH/home.out"; then
    keep_records_action
    database_stop "The deployed database has a different schema from this release." \
      "The database is unchanged. $LATE_STATE" \
      "Replace the database with an empty database:" "./redeploy.sh --reset-database" \
      "$KEEP_ACTION" "$KEEP_COMMANDS"
  fi
  if grep -q '"code":"database-absent"' "$SCRATCH/home.out"; then
    database_stop "The deployed database is absent." \
      "The deployment still has no database. $LATE_STATE" \
      "Create an empty database:" "./redeploy.sh --reset-database"
  fi
  cat "$SCRATCH/home.err" >&2
  cat "$SCRATCH/home.out"
  fail "mc __onboard home refused; rerun after fixing what it names"
fi
cat "$SCRATCH/home.err" >&2
cat "$SCRATCH/home.out"
[ "$RESET_DATABASE" -eq 0 ] || say "   the deployment database is a new empty database"

say "== deploy: mc __onboard tunables (the config.toml settings in the database)"
"$BIN_DIR/mc" __onboard tunables || \
  fail "the tunables section refused; rerun after fixing what it names"

if [ "$RESET_DATABASE" -eq 1 ]; then
  say "== deploy: mc __onboard surfaces (the config.toml alert routes in the database)"
  "$BIN_DIR/mc" __onboard surfaces || \
    fail "the surfaces section refused; rerun after fixing what it names"
fi

say "== deploy: mc __onboard supervision --activate --reconfigure"
"$BIN_DIR/mc" __onboard supervision --activate --reconfigure || \
  fail "supervision activation refused; rerun after fixing what it names"

say "== verify: mc doctor"
"$BIN_DIR/mc" doctor
