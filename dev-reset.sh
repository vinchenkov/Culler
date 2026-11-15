#!/usr/bin/env bash
#
# dev-reset.sh — nuke a production-tier deployment back to a fresh brain, fast.
#
# TARGET: the deployment home that MC_HOME names. The script refuses to run
# without MC_HOME, because the default home, ~/.culler, is the production
# deployment. It needs a production build of mc on PATH, because only that
# build has `mc __reset`. A development-tier home (install.sh --dev) is out of
# its reach.
#
# WHY THIS EXISTS: during bring-up a deployment keeps landing in sticky
# states (a wedged lease, a half-provisioned Project, a spine full of
# retired smoke generations), and waiting one out costs more than rebuilding
# it. This gets a deployment from "stuck" to "fresh and supervised" in a couple
# of minutes.
#
# WHAT IT DESTROYS: the spine volume and every durable artifact that references
# it — projects, tasks, packets, activity, run records, session traces,
# rendered outputs, seals, smoke workspaces, the AgentsView archive and trace
# copies, and the wiki exports.
#
# WHAT IT PRESERVES, deliberately: `refresh-grants/` (the runtime-auth refresh
# grants), `config.toml`, `routing.md`, and the published release. The
# credentials are the load-bearing one — Anthropic refresh tokens are
# single-use, so re-capturing one means going back through the operator's own
# host CLI. They are copied to a timestamped safe-hold before anything is
# removed, so even a bug in this script cannot cost them.
#
# NOT AN UNINSTALL. `mc __reset` re-initializes the deployment and removes no
# software. This script has the same intent, automated. It never removes
# MC_HOME itself.
#
# Usage:
#   MC_HOME=<home> ./dev-reset.sh --confirm                      nuke and re-provision
#   MC_HOME=<home> ./dev-reset.sh --confirm --rebuild --smoke    the whole dev loop
#   MC_HOME=<home> ./dev-reset.sh                                show the plan and exit
#
# --rebuild does the paired rebuild at HEAD. It is needed whenever the tree has
# moved, because activation is refused unless host `mc` and the `mc-prod` image
# carry the same commit as the recorded onboard state — so "I fixed something,
# now retest" is --confirm --rebuild --smoke. The host mc compiles into a
# scratch path before anything stops, so a compile error costs nothing. The
# image builds after supervision stops, because build-prod.sh refuses while a
# unit is loaded. The new host mc replaces the mc on PATH only after the image
# build succeeds, so the host mc and the image always match.
#
# Lease tunables are written as part of re-provisioning. The defaults are
# development values, NOT production ones: they exist so a dead run is reaped in
# minutes rather than the shipped 75. Override with the env vars below.

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Dev lease tunables. These only reap runs that have gone SILENT — a healthy run
# heartbeats every heartbeat_interval_s and is never touched — except
# HARD_DEADLINE_MINUTES, which is non-renewable and bounds a run that is alive
# but hung. Shipped production defaults are 60/15/240.
TIMEOUT_MINUTES="${MC_DEV_TIMEOUT_MINUTES:-5}"
GRACE_MINUTES="${MC_DEV_GRACE_MINUTES:-2}"
HARD_DEADLINE_MINUTES="${MC_DEV_HARD_DEADLINE_MINUTES:-20}"
HEARTBEAT_INTERVAL_S="${MC_DEV_HEARTBEAT_INTERVAL_S:-60}"
SPAWN_GRACE_S="${MC_DEV_SPAWN_GRACE_S:-60}"
COLLIE_IDLE_TIMEOUT_S="${MC_DEV_COLLIE_IDLE_TIMEOUT_S:-1800}"
REVIEW_WIP_CAP="${MC_DEV_REVIEW_WIP_CAP:-3}"

CONFIRM=0
RUN_SMOKE=0
REBUILD=0
PROJECT_ID=""
WORKSPACE_ROOT=""
while [ $# -gt 0 ]; do
	case "$1" in
		--confirm|-y) CONFIRM=1 ;;
		--smoke) RUN_SMOKE=1 ;;
		--rebuild) REBUILD=1 ;;
		--project) PROJECT_ID="${2:?--project needs an id}"; shift ;;
		--workspace-root) WORKSPACE_ROOT="${2:?--workspace-root needs a path}"; shift ;;
		-h|--help) awk 'NR == 1 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "$0"; exit 0 ;;
		*) echo "dev-reset: unknown argument $1" >&2; exit 2 ;;
	esac
	shift
done
if [ -n "$PROJECT_ID" ] && [ -z "$WORKSPACE_ROOT" ]; then
	echo "dev-reset: --project needs --workspace-root" >&2; exit 2
fi
if [ -z "${MC_HOME:-}" ]; then
	echo "dev-reset: set MC_HOME to the deployment home to reset. The production home is ~/.culler." >&2; exit 2
fi

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
die() { printf '\ndev-reset: %s\n' "$*" >&2; exit 1; }

# Artifact directories cleared wholesale. Every one of these is derived state
# that references the spine we are about to destroy, so keeping any of it would
# leave dangling references rather than useful history. Enumerated rather than
# globbed: this script must never be one typo away from removing MC_HOME.
# `onboard-intents/` is deliberately NOT here. It records the operator's own
# Project registration intents — workspace paths they supplied — which is
# input to onboarding, not output of the spine. Clearing it once cost exactly
# the paths needed to re-register after the wipe.
ARTIFACT_DIRS=(
	runs sessions outputs workflows seals smoke health attachments
	logs behaviors state/projects
)
# Derived views of the session folders and the Project repositories. They are
# removed, not recreated: supervision activation and the resident create them
# again. The AgentsView executable in bin/ stays.
VIEWER_DIRS=(agentsview wiki)

# ---------------------------------------------------------------- preflight --
[ -d "$MC_HOME" ] || die "MC_HOME $MC_HOME does not exist; nothing to reset"
[ -f "$MC_HOME/config.toml" ] || die "$MC_HOME has no config.toml — refusing to treat it as a Culler home"
MC_BIN="$(command -v mc 2>/dev/null)" || die "no mc on PATH; install Culler from this checkout first with sh scripts/install-source.sh"
docker info >/dev/null 2>&1 || die "container runtime unreachable; start Docker Desktop first"

SMOKE_WORKSPACES="$(dirname "$MC_HOME")/.culler-smoke-workspaces"

say "PLAN"
cat <<PLAN
  MC_HOME              $MC_HOME
  repo                 $REPO

  DESTROY  spine volume (snapshotted by mc __reset first)
           $MC_HOME/{$(IFS=,; echo "${ARTIFACT_DIRS[*]}")}
           $MC_HOME/{$(IFS=,; echo "${VIEWER_DIRS[*]}")}
           $MC_HOME/deployment.uuid   <- forces a NEW brain rather than a restore
           $MC_HOME/mount-allowlist   <- rewritten to deny-all
           $SMOKE_WORKSPACES

  PRESERVE $MC_HOME/refresh-grants    <- runtime-auth credentials
           $MC_HOME/runtime-auth-sources
           $MC_HOME/config.toml, routing.md
           $MC_HOME/release, supervision, backups

  THEN$( [ "$REBUILD" = 1 ] && echo "     paired rebuild at $(git -C "$REPO" rev-parse --short HEAD 2>/dev/null || echo HEAD) (host mc -> $MC_BIN + mc-prod image)" || echo "" )
           re-provision spine, write dev lease tunables, reactivate supervision
           lease: timeout ${TIMEOUT_MINUTES}m + grace ${GRACE_MINUTES}m => dead run reaped in ~$((TIMEOUT_MINUTES + GRACE_MINUTES))m
                  hard deadline ${HARD_DEADLINE_MINUTES}m (bounds a hung-but-live run)
$( [ "$RUN_SMOKE" = 1 ] && echo "           then start a fresh smoke generation" )

  NOTE     every Project is destroyed with the spine. Add real ones again with
           Add Project on the dashboard Board, or with --project and
           --workspace-root; --smoke creates the smoke Project.
PLAN

if [ "$CONFIRM" != 1 ]; then
	echo
	echo "dev-reset: dry run. Re-run with --confirm to execute."
	exit 0
fi

# --------------------------------------------------------------- compile mc --
# Before anything stops, so a compile error costs nothing. The new mc replaces
# the mc on PATH only after the image build succeeds.
if [ "$REBUILD" = 1 ]; then
	HEAD_SHA="$(git -C "$REPO" rev-parse HEAD)"
	VERSION="$(cat "$REPO/VERSION")"
	case "$VERSION" in *[!0-9.]*|'') die "the VERSION file must hold one version, for example 0.1.0" ;; esac
	printf '%s\n' "$VERSION" | grep -Eqx '(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)' || die "the VERSION file must hold one version, for example 0.1.0"
	NEW_MC_DIR="$(mktemp -d "${TMPDIR:-/tmp}/dev-reset-mc.XXXXXX")"
	trap 'rm -rf "$NEW_MC_DIR"' EXIT
	say "COMPILE host mc at $HEAD_SHA"
	( cd "$REPO/mc" && mise exec -- go build \
		-ldflags "-w -X main.releaseBuildID=$HEAD_SHA -X main.releaseVersion=$VERSION" \
		-o "$NEW_MC_DIR/mc" ./cmd/mc )
fi

# ------------------------------------------------------- credential safehold --
# Cheap insurance taken BEFORE anything is removed. The refresh grants are the
# one thing here that cannot be regenerated from the repo.
STAMP="$(date +%Y%m%d-%H%M%S)"
SAFEHOLD="$MC_HOME/backups/dev-reset-safehold-$STAMP"
say "SAFEHOLD credentials -> $SAFEHOLD"
mkdir -p "$SAFEHOLD"
for keep in refresh-grants runtime-auth-sources config.toml routing.md; do
	[ -e "$MC_HOME/$keep" ] && cp -R "$MC_HOME/$keep" "$SAFEHOLD/" || true
done
ls -la "$SAFEHOLD"

# ------------------------------------------------------------------ quiesce --
# mc __reset refuses while a unit is loaded, and a live resident would keep
# ticking into a spine we are removing. AgentsView holds its archive open.
say "STOP supervision"
for label in resident dashboard agentsview; do
	unit="$(launchctl list 2>/dev/null | awk -v l="$label" '$3 ~ ("com.culler..*\\." l "$") {print $3}' | head -1)"
	if [ -n "$unit" ]; then
		echo "  booting out $unit"
		launchctl bootout "gui/$(id -u)/$unit" 2>/dev/null || true
	else
		echo "  $label not loaded"
	fi
done

# `launchctl bootout` returns before the unit leaves `launchctl list`, and both
# build-prod.sh and `mc __reset` refuse while a com.culler.* label is listed.
# Wait, bounded to a few seconds, for the labels to clear.
culler_units_loaded() {
	case "$(launchctl list 2>/dev/null || true)" in
		*com.culler.*) return 0 ;;
		*) return 1 ;;
	esac
}
polls=0
while culler_units_loaded; do
	polls=$((polls + 1))
	[ "$polls" -lt 50 ] || die "a com.culler.* launchd unit is still loaded after the stop. Nothing was removed."
	sleep 0.1
done

# ------------------------------------------------------------ rebuild image --
# build-prod.sh refuses while a unit is loaded, so the image builds after the
# stop. A failed build leaves the host mc and the image as they were.
if [ "$REBUILD" = 1 ]; then
	say "REBUILD mc-prod image at $HEAD_SHA"
	MC_RELEASE_BUILD_ID="$HEAD_SHA" "$REPO/runner/image/build-prod.sh" ||
		die "the image build failed. Supervision is stopped and nothing was removed. Fix the build, then run this script again."
	say "INSTALL host mc -> $MC_BIN"
	install -m 0755 "$NEW_MC_DIR/mc" "$MC_BIN"
fi

say "REMOVE stray agent containers"
# Agent containers ONLY. Pipeline containers are named mc-<project>-<run_id>,
# so they are selected by the labels every one
# carries — mc-managed=true AND mc-tier=pipeline — never by `mc-managed` alone:
# helpers carry that label too, and more than one deployment can share a
# Docker daemon (this machine has two). A blanket sweep removed the other
# deployment's helper on first run — recoverable, since helpers are recreated
# lazily, but not this script's business to touch. `mc __reset` removes THIS
# deployment's helper itself, by exact name, which is the correct grain. The
# setup/probe containers keep the mc-setup-<run_id> name and are swept by it.
stray="$(
	{
		docker ps -a --filter label=mc-managed=true --filter label=mc-tier=pipeline --format '{{.Names}}' 2>/dev/null
		docker ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^mc-setup-'
	} | sort -u || true
)"
if [ -n "$stray" ]; then
	echo "$stray" | xargs docker rm -f >/dev/null 2>&1 || true
	echo "  removed $(echo "$stray" | wc -l | tr -d ' ') agent container(s)"
else
	echo "  none"
fi

# ------------------------------------------------------------------- destroy --
say "RESET spine (snapshots to $MC_HOME/backups first)"
# The reset backs up the spine first, and the backup refuses a spine whose schema
# differs from this mc. After a schema change, only redeploy.sh can replace it.
mc __reset --confirm ||
	die "mc __reset refused, and the spine and the files are unchanged. When the database schema differs from this checkout, run ./redeploy.sh --reset-database instead."

say "CLEAR derived artifacts"
for dir in "${ARTIFACT_DIRS[@]}"; do
	target="$MC_HOME/$dir"
	case "$target" in
		"$MC_HOME"/*) ;;                       # never escape MC_HOME
		*) die "refusing to remove $target" ;;
	esac
	if [ -d "$target" ]; then
		rm -rf "${target:?}"
		# 0700, NOT the umask default. Onboarding checks several of these for
		# `Perm() == 0o700` exactly, so a plain
		# mkdir leaves 0755 and the next `--smoke` refuses with "must be an
		# exact owner-only directory" — which is a confusing way to learn that
		# the reset, not the system, broke the deployment.
		(umask 077 && mkdir -p "$target")
		echo "  cleared $dir"
	fi
done

for dir in "${VIEWER_DIRS[@]}"; do
	target="$MC_HOME/$dir"
	case "$target" in
		"$MC_HOME"/*) ;;                       # never escape MC_HOME
		*) die "refusing to remove $target" ;;
	esac
	if [ -e "$target" ]; then
		rm -rf "${target:?}"
		echo "  removed $dir"
	fi
done

# A task store root is left mode 0555 on purpose, and a directory without its
# write bit will not give up its children — so `rm -rf` returns EPERM on every
# task dir, for the host operator AND for root inside a container (both
# measured). The operator owns them, though, so restoring the write bit first
# is all it takes. No container, no sudo.
if [ -d "$SMOKE_WORKSPACES" ]; then
	chmod -R u+rwX "$SMOKE_WORKSPACES" 2>/dev/null || true
	if rm -rf "${SMOKE_WORKSPACES:?}" 2>/dev/null; then
		echo "  cleared $(basename "$SMOKE_WORKSPACES")"
	else
		echo "  WARNING: residue left in $SMOKE_WORKSPACES; inert (new generations use new ids)"
	fi
fi

# The allowlist accumulates one artifact-plane grant per Project. With every
# Project gone those entries are stale, so it goes back to the deny-all
# scaffold onboarding would author. A re-added Project grants again.
printf 'version = 1\n' > "$MC_HOME/mount-allowlist"
echo "  reset mount-allowlist to deny-all"

# THE step that makes this a nuke rather than a reset. With the identity mirror
# still present, onboarding reads "MC_HOME says a spine should exist" and fails
# closed with "spine lost — restore from backup", which is correct for a reset and wrong for a deliberate rebuild.
rm -f "$MC_HOME/deployment.uuid"
echo "  removed deployment.uuid (fresh brain, not a restore)"

# ---------------------------------------------------------------- rebuild --
say "PROVISION fresh spine"
mc __onboard home --release-source "$REPO/runner" --host-release-source "$REPO"

# `mc __reset` removes this deployment's helper by exact name, and the sweep above
# removes its agent containers. The container section re-establishes the image /
# helper envelope and re-runs the kernel capability probe. Without it the first
# supervision activation rolled back with "resident produced no successful
# release-bound tick within 90 seconds" — the resident's attachment-confinement
# canary could not stand up its probe container.
say "RE-ESTABLISH container envelope"
mc __onboard container

say "WRITE dev lease tunables"
mc onboard tunables \
	--review-wip-cap "$REVIEW_WIP_CAP" \
	--timeout-minutes "$TIMEOUT_MINUTES" \
	--grace-minutes "$GRACE_MINUTES" \
	--heartbeat-interval-s "$HEARTBEAT_INTERVAL_S" \
	--spawn-grace-s "$SPAWN_GRACE_S" \
	--hard-deadline-minutes "$HARD_DEADLINE_MINUTES" \
	--collie-idle-timeout-s "$COLLIE_IDLE_TIMEOUT_S"

# A wiped spine has zero Projects, and activation validates the whole
# onboarding chain — the project section refuses with "the first Project
# needs operator input". So a Project must exist BEFORE
# supervision, which inverts the obvious order: the smoke registers its own
# transient Project, so starting it first satisfies the precondition.
#
# `mc __onboard --smoke` then watches for ten minutes, and the pipeline it is
# watching cannot move until the resident is up — so it runs in the background
# and we proceed as soon as its Project row lands.
SMOKE_PID=""
if [ "$RUN_SMOKE" = 1 ]; then
	say "REGISTER smoke Project"
	mc __onboard --smoke >/tmp/dev-reset-smoke.$$ 2>&1 &
	SMOKE_PID=$!
	for _ in $(seq 1 60); do
		if [ -n "$(mc project list 2>/dev/null | grep -o '"id":"smoke-[0-9a-f]*"' | head -1)" ]; then
			echo "  registered $(mc project list 2>/dev/null | grep -o 'smoke-[0-9a-f]*' | head -1)"
			break
		fi
		sleep 1
	done
elif [ -n "$PROJECT_ID" ]; then
	say "REGISTER Project $PROJECT_ID"
	mc onboard project --project "$PROJECT_ID" --workspace-root "$WORKSPACE_ROOT"
else
	say "STOPPING: a fresh spine has no Project"
	cat <<NEEDS
  Supervision cannot activate until one exists. Either:
    MC_HOME=$MC_HOME ./dev-reset.sh --confirm --smoke
    MC_HOME=$MC_HOME ./dev-reset.sh --confirm --project <id> --workspace-root </abs/path>
  The spine is provisioned and the tunables are written; re-run with one of
  those to finish. Nothing above needs repeating.
NEEDS
	exit 0
fi

say "ACTIVATE supervision"
mc __onboard supervision --activate --reconfigure

say "VERIFY"
mc doctor

if [ -n "$SMOKE_PID" ]; then
	say "SMOKE (watcher started before supervision; now has a resident to drive it)"
	wait "$SMOKE_PID" || echo "  watcher returned nonzero — the pipeline continues on the tick; follow it on the dashboard Board"
	cat "/tmp/dev-reset-smoke.$$" 2>/dev/null || true
	rm -f "/tmp/dev-reset-smoke.$$"
fi

say "DONE"
cat <<DONE
  credentials safehold: $SAFEHOLD
  lease: dead run reaped in ~$((TIMEOUT_MINUTES + GRACE_MINUTES))m, hung run at ${HARD_DEADLINE_MINUTES}m
  every Project was destroyed — add real ones again with Add Project on the dashboard Board
DONE
