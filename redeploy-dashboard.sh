#!/bin/sh
# redeploy-dashboard.sh — move the live dashboard, and only the dashboard, to
# the files committed at HEAD.
#
#   ./redeploy-dashboard.sh
#
# A development shortcut for dashboard work. redeploy.sh moves the whole
# release to a new commit: it stops the supervised units, rebuilds the production image,
# and reinstalls mc. A dashboard change needs none of that. The dashboard is a
# Bun process that runs the release copy of its source. Browser assets are read
# from disk on every request, so a copy alone makes a UI change visible on the
# next page load. A server-side change (dashboard/src/*.ts) needs a new
# process; the unit has KeepAlive, so the script signals the process and
# launchd starts it again.
#
# Files come from HEAD, not from the working tree: only committed changes reach
# the dashboard. The file set is the dashboard part of the host release
# manifest (mc/deployment/host_release.go) at HEAD. The resident and
# runner/shared are untouched.
#
# Two cases need redeploy.sh instead: a change that depends on a new mc verb or
# field (the installed mc is still the old release), and a new manifest file
# (the installed mc's host release validation does not know it).
#
# Environment: MC_HOME (defaults to ~/.culler).
set -eu

REPO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
MC_HOME=${MC_HOME:-$HOME/.culler}
HOST_ROOT="$MC_HOME/release/host"

say() { printf '%s\n' "$*"; }
fail() { printf 'redeploy-dashboard.sh: %s\n' "$*" >&2; exit 1; }

command -v git >/dev/null 2>&1 || fail "git is required"
[ -d "$HOST_ROOT/dashboard" ] || fail "no installed host release under $HOST_ROOT; run install.sh or redeploy.sh first"

HEAD_ID=$(git -C "$REPO_DIR" rev-parse --short 'HEAD^{commit}') || fail "cannot resolve HEAD"
say "== dashboard files at $HEAD_ID"
if [ -n "$(git -C "$REPO_DIR" status --porcelain -- dashboard/src)" ]; then
  say "   note: uncommitted changes under dashboard/src are not deployed"
fi

# The dashboard part of the host release manifest, as committed at HEAD. The
# same Go file also lists directories; the extension keeps those out.
FILES=$(git -C "$REPO_DIR" show HEAD:mc/deployment/host_release.go |
  sed -n 's/^[[:space:]]*"\(dashboard\/[^"]*\.[a-z]*\)",$/\1/p')
[ -n "$FILES" ] || fail "no dashboard files in the host release manifest at HEAD"

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/mc-redeploy-dashboard.XXXXXX")
PENDING=""
cleanup() { rm -rf "$SCRATCH"; [ -z "$PENDING" ] || rm -f "$PENDING"; }
trap cleanup EXIT

changed=0
restart=0
for rel in $FILES; do
  target="$HOST_ROOT/$rel"
  [ -f "$target" ] || fail "$rel is not in the installed release; a new manifest file needs redeploy.sh"
  staged="$SCRATCH/$(printf '%s' "$rel" | tr '/' '_')"
  git -C "$REPO_DIR" show "HEAD:$rel" > "$staged" 2>/dev/null || fail "$rel is in the manifest but not at HEAD"
  [ -s "$staged" ] || fail "$rel is empty at HEAD"
  if cmp -s "$staged" "$target"; then
    continue
  fi
  case "$rel" in
    *.ts) restart=1 ;;
  esac
  # Stage next to the target so the swap is one rename; the release directory
  # must never hold a partial file, because the server reads it per request.
  PENDING="$target.redeploy"
  (umask 077 && cp "$staged" "$PENDING") && mv -f "$PENDING" "$target"
  PENDING=""
  chmod 600 "$target"
  say "   updated $rel"
  changed=1
done

if [ "$changed" -eq 0 ]; then
  say "== nothing to do: the installed dashboard already matches HEAD"
  exit 0
fi
if [ "$restart" -eq 0 ]; then
  say "== done: browser assets only; reload the page"
  exit 0
fi

row=$(launchctl list 2>/dev/null | grep 'com\.culler\.[0-9a-f]*\.dashboard$' | head -n 1) || true
[ -n "$row" ] || fail "server-side files changed but no com.culler.*.dashboard unit is loaded; run redeploy.sh"
label=${row##*[[:space:]]}
pid=${row%%[[:space:]]*}
say "== restart: $label"
launchctl kill TERM "gui/$(id -u)/$label" || fail "could not signal $label"

polls=0
while [ "$pid" != "-" ] && kill -0 "$pid" 2>/dev/null; do
  polls=$((polls + 1))
  [ "$polls" -lt 100 ] || fail "dashboard process $pid did not exit"
  sleep 0.1
done

bind=$(sed -n 's/.*"bind": *"\([^"]*\)".*/\1/p' "$MC_HOME/supervision/dashboard.json" 2>/dev/null | head -n 1)
if [ -z "$bind" ] || ! command -v curl >/dev/null 2>&1; then
  say "== done: launchd restarts the dashboard within its throttle interval"
  exit 0
fi
polls=0
until [ "$(curl -s -o /dev/null -w '%{http_code}' "http://$bind/")" != "000" ]; do
  polls=$((polls + 1))
  [ "$polls" -lt 300 ] || fail "dashboard did not come back on $bind; see $MC_HOME/logs/dashboard.stderr.log"
  sleep 0.1
done
say "== done: dashboard is serving on $bind"
