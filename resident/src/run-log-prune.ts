// run-log-prune.ts — the tick chore that bounds $MC_HOME/logs/runs.
//
// Every run container binds a per-run host directory at /mc/logs and writes
// its diagnostics there. The reap arm leaves that directory, because deleting
// a failed run's account of itself destroys exactly what an operator comes
// looking for. This chore bounds the tree instead.
//
// It keeps the newest KEEP_RUN_LOGS ended runs, ordered by the spine's
// `ended_at`. That is a record fact, not a clock reading, so the tick stays
// time-invariant.
//
// Three fences bound what it removes:
//   1. The root is the constant `logs/runs`. Entries are joined, never
//      globbed, and every entry passes the run-id charset before it enters a
//      path. `sessions/<run_id>/` is kept forever and is unreachable here.
//   2. A directory whose id has no run row stays, and the log names it. A log
//      that nothing recorded is a question, not garbage.
//   3. A run that has not ended, or whose container is still present, stays.
//      The row goes ended from inside the container before that container
//      exits, so ended-ness alone races the unmount and fails with EACCES.
//
// It never throws. A `beforeTick` failure skips dispatch for the tick, and a
// tick that stops dispatching over one undeleted directory trades the
// metronome for disk.

import type { Exec } from "./types";
import type { Logger } from "../../runner/shared/log";

/** Ended runs whose diagnostics are kept: about three days of history at this
 * deployment's rate. The sink's 4 MiB per-run cap bounds the worst case. */
export const KEEP_RUN_LOGS = 500;

/** Removals per tick. The backlog drains over several ticks on purpose: a tick
 * that spends its seconds on residue is a tick not dispatching, and reclaiming
 * disk carries no deadline. */
export const RUN_LOG_PRUNE_PER_TICK = 8;

export interface RunLogPruneDeps {
  mcHome: string;
  /** Invokes the host mc binary. The ended-set is decided in the lock domain
   * — the resident never opens the spine. */
  runMc: Exec;
  docker: Exec;
  /** Directory listing. Rejecting (no such root) means nothing to reclaim. */
  readdir(path: string): Promise<string[]>;
  /** Best-effort recursive removal — the reap arm's `discarder`, which retries
   * briefly and then logs rather than throwing. */
  discard(path: string): Promise<void>;
  log: Logger;
}

export interface RunLogPruneChore {
  beforeTick(): Promise<void>;
}

// Duplicated from effects.ts rather than imported: this module must not pull
// in the effector graph.
const SAFE_ID = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/;

export function createRunLogPruneChore(deps: RunLogPruneDeps): RunLogPruneChore {
  return {
    async beforeTick() {
      try {
        await pruneRunLogs(deps);
      } catch (err) {
        deps.log.warn("sweep.skipped", `run log prune: skipped this tick: ${err instanceof Error ? err.message : String(err)}`);
      }
    },
  };
}

/** One prune. Exported for tests; production reaches it through the chore,
 * which is the arm that guarantees it cannot cancel a tick. */
export async function pruneRunLogs(deps: RunLogPruneDeps): Promise<void> {
  const root = `${deps.mcHome}/logs/runs`;
  let entries: string[];
  try {
    entries = await deps.readdir(root);
  } catch {
    // No run-log root: nothing has run under the bind yet. Not an error.
    return;
  }
  if (entries.length <= KEEP_RUN_LOGS) return;

  const rows = await endedAtByRunID(deps);
  if (rows === null) return;
  const live = await liveRunIDs(deps);
  if (live === null) return;

  const malformed: string[] = [];
  const unrecorded: string[] = [];
  const candidates: { id: string; endedAt: string }[] = [];
  for (const id of entries) {
    if (!SAFE_ID.test(id)) {
      malformed.push(id);
      continue;
    }
    const endedAt = rows.get(id);
    if (endedAt === undefined) {
      unrecorded.push(id);
      continue;
    }
    if (endedAt === "" || live.has(id)) continue;
    candidates.push({ id, endedAt });
  }

  // Newest first by the row's own timestamp, so the oldest ended runs are the
  // ones that fall off the end. Ties break on the id so the order is total and
  // two ticks cannot disagree about which of two same-second runs to keep.
  candidates.sort((a, b) =>
    a.endedAt === b.endedAt ? a.id.localeCompare(b.id) : (a.endedAt < b.endedAt ? 1 : -1));

  const doomed = candidates.slice(KEEP_RUN_LOGS);
  let removed = 0;
  let remaining = 0;
  for (const { id } of doomed) {
    if (removed >= RUN_LOG_PRUNE_PER_TICK) {
      remaining++;
      continue;
    }
    await deps.discard(`${root}/${id}`);
    removed++;
  }

  if (removed > 0) {
    deps.log.info("sweep.reclaimed",
      `run log prune: reclaimed ${removed} run log(s) beyond the newest ${KEEP_RUN_LOGS}` +
      (remaining > 0 ? `; ${remaining} left for the next tick (per-tick cap ${RUN_LOG_PRUNE_PER_TICK})` : ""),
      { removed, remaining, keep: KEEP_RUN_LOGS, cap: RUN_LOG_PRUNE_PER_TICK },
    );
  }
  if (malformed.length > 0) {
    deps.log.warn("sweep.skipped", `run log prune: ${malformed.length} entr(ies) fail the run-id charset and were left: ${malformed.slice(0, 5).join(", ")}`,
      { malformed: malformed.length });
  }
  if (unrecorded.length > 0) {
    deps.log.warn("sweep.skipped", `run log prune: ${unrecorded.length} log(s) name no run row and were left: ${unrecorded.slice(0, 5).join(", ")}`,
      { unrecorded: unrecorded.length });
  }
}

/** run id → its row's `ended_at` ("" when still running). Null when the reading
 * cannot be trusted, which removes nothing: a run list that failed to parse
 * must not read as "every run has ended".
 *
 * The read is whole on purpose. The container inventory puts a whole
 * `docker ps` on this same timer, and the backup chore a full-database
 * `VACUUM INTO`. Only a tick observed to miss its interval justifies a
 * narrower read. The read is paid only on ticks where the run-log tree is
 * already over its bound. */
async function endedAtByRunID(deps: RunLogPruneDeps): Promise<Map<string, string> | null> {
  const res = await deps.runMc(["run", "list"]);
  if (res.exitCode !== 0) {
    deps.log.warn("sweep.failed", `run log prune: mc run list failed (exit ${res.exitCode}): ${res.stderr.trim()}`);
    return null;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(res.stdout);
  } catch {
    deps.log.warn("sweep.failed", "run log prune: mc run list returned unparseable JSON");
    return null;
  }
  const runs = (parsed as { runs?: unknown } | null)?.runs;
  if (!Array.isArray(runs)) {
    deps.log.warn("sweep.failed", "run log prune: mc run list returned no runs array");
    return null;
  }
  const ended = new Map<string, string>();
  for (const row of runs) {
    const id = (row as { id?: unknown } | null)?.id;
    if (typeof id !== "string" || id === "") continue;
    const at = (row as { ended_at?: unknown }).ended_at;
    ended.set(id, typeof at === "string" ? at : "");
  }
  return ended;
}

/** The run and session ids Docker still carries, from the `mc-managed` label
 * scope the container inventory already uses. Null when the inventory cannot
 * be trusted: an unreadable container list must not read as "every container
 * is gone". Both labels, because both tiers bind a log directory. */
async function liveRunIDs(deps: RunLogPruneDeps): Promise<Set<string> | null> {
  const res = await deps.docker([
    "ps", "-a", "--no-trunc",
    "--filter", "label=mc-managed=true",
    "--format", '{{.Label "mc-run-id"}} {{.Label "mc-session-id"}}',
  ]);
  if (res.exitCode !== 0) {
    deps.log.warn("sweep.failed", `run log prune: container inventory failed (exit ${res.exitCode}): ${res.stderr.trim()}`);
    return null;
  }
  const live = new Set<string>();
  for (const line of res.stdout.split("\n")) {
    for (const id of line.trim().split(/\s+/)) {
      if (id !== "" && id !== "<no value>") live.add(id);
    }
  }
  return live;
}
