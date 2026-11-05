#!/bin/bash
# Remove installed Culler software, with an explicit choice about its data.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./uninstall.sh [options]

  --keep-data    Remove software;
                 keep data.
  --full-wipe    Default. Delete data,
                 task branches,
                 credentials and
                 review files.
                 Project repos stay.
  --dry-run      Preview removal.
  --yes, -y      Skip confirmation.
  --home PATH    Another deployment.
  --workspace PATH
                 Another Project.
  --binary PATH  A custom installed
                 program named mc.
  --help, -h     Show this help.

Paths may be repeated. Culler and old
Mission Control installs are included.
With --keep-data, reinstall at the same
path to reuse configuration and history.
Project Git settings stay in both modes.
Both modes remove the Culler Bun.
Docker, Git, Go, and the Apple
developer tools stay.
EOF
}

DRY_RUN=0
ASSUME_YES=0
MODE=
EXTRA=(--full-wipe)
while [ $# -gt 0 ]; do
  case "$1" in
    --keep-data|--full-wipe)
      if [ -n "$MODE" ] && [ "$MODE" != "$1" ]; then
        echo 'Choose --keep-data or --full-wipe.' >&2
        exit 2
      fi
      MODE=$1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --home|--workspace|--binary)
      if [ $# -lt 2 ] || [ -z "$2" ] || [[ "$2" == --* ]]; then
        printf '%s needs an absolute path.\n' "$1" >&2
        exit 2
      fi
      EXTRA+=("$1" "$2"); shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) printf 'Unknown option: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done
MODE=${MODE:---full-wipe}
EXTRA[0]=$MODE
REPO=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

TEMP=$(mktemp -d "${TMPDIR:-/tmp}/culler-uninstall.XXXXXX")
trap 'rm -rf "$TEMP"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
# Build this checkout's removal code even if the installed version predates it.
# A temporary executable also permits retries after the installed one is gone.
if [ -f "$REPO/mc/go.mod" ]; then
  echo 'Preparing the uninstaller...'
  MC="$TEMP/uninstaller"
  (cd "$REPO/mc" && mise exec -- go build -o "$MC" ./cmd/mc)
elif [ -x "$HOME/.local/bin/mc" ]; then
  MC="$HOME/.local/bin/mc"
else
  echo 'Run uninstall.sh from a Culler checkout.' >&2
  exit 1
fi

echo 'Finding installed resources...'
if ! "$MC" __uninstall --dry-run "${EXTRA[@]}" >"$TEMP/plan.json"; then
  echo 'Preview failed. Nothing was removed.' >&2
  echo 'Resolve the error, then try again.' >&2
  exit 1
fi
# The plan report lists each Culler Bun directory under host_buns.
REMOVES_BUN=0
if grep -q '"host_buns":\["' "$TEMP/plan.json"; then REMOVES_BUN=1; fi
if [ "$DRY_RUN" -eq 1 ]; then
  echo
  echo 'Preview complete. Nothing was removed.'
  exit 0
fi

if [ "$ASSUME_YES" -eq 0 ]; then
  echo
  if [ "$MODE" = --full-wipe ]; then
    echo 'Permanently delete the listed data?'
    CONFIRM=wipe
  else
    echo 'Remove the listed software?'
    CONFIRM=uninstall
  fi
  printf 'Type "%s" to confirm: ' "$CONFIRM"
  REPLY=
  read -r REPLY || true
  if [ "$REPLY" != "$CONFIRM" ]; then
    echo 'Cancelled. Nothing was removed.'
    exit 1
  fi
fi

if ! "$MC" __uninstall --confirm "${EXTRA[@]}" >/dev/null; then
  echo 'Uninstall did not finish.' >&2
  echo 'Retry with the same options.' >&2
  exit 1
fi
echo
if [ "$MODE" = --full-wipe ]; then
  echo 'Culler software and data removed.'
  [ "$REMOVES_BUN" -eq 0 ] || echo 'Culler Bun removed.'
  echo 'Project repositories kept.'
else
  echo 'Culler software removed. Data kept.'
  [ "$REMOVES_BUN" -eq 0 ] || echo 'Culler Bun removed.'
fi
