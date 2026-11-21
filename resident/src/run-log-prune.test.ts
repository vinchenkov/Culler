import { describe, expect, test } from "bun:test";
import { msgLogger } from "./test-helpers";
import {
  createRunLogPruneChore,
  KEEP_RUN_LOGS,
  pruneRunLogs,
  RUN_LOG_PRUNE_PER_TICK,
  type RunLogPruneDeps,
} from "./run-log-prune";
import type { ExecResult } from "./types";

const MC_HOME = "/tmp/mc-home";
const ROOT = `${MC_HOME}/logs/runs`;

interface Rig {
  deps: RunLogPruneDeps;
  discarded: string[];
  logs: string[];
  mcCalls: string[][];
}

function ok(stdout: string): ExecResult {
  return { exitCode: 0, stdout, stderr: "" };
}

function runList(rows: Array<{ id: string; ended_at: string | null }>): ExecResult {
  return ok(JSON.stringify({ runs: rows }));
}

function configWithBudget(megabytes: number): ExecResult {
  return ok(JSON.stringify({ families: [{ family: "container", knobs: [
    { key: "container.run_tree_max_mb", value: megabytes },
  ] }] }));
}

function rig(opts: {
  entries?: string[] | Error;
  runs?: ExecResult;
  containers?: ExecResult;
  budgetMB?: number;
  sizes?: Record<string, number> | Error;
}): Rig {
  const discarded: string[] = [];
  const logs: string[] = [];
  const mcCalls: string[][] = [];
  const deps: RunLogPruneDeps = {
    mcHome: MC_HOME,
    runMc: async (argv) => {
      mcCalls.push(argv);
      if (argv[0] === "config") return configWithBudget(opts.budgetMB ?? 0);
      return opts.runs ?? runList([]);
    },
    docker: async () => opts.containers ?? ok(""),
    readdir: async (path) => {
      if (path !== ROOT) throw new Error(`unexpected readdir ${path}`);
      if (opts.entries instanceof Error) throw opts.entries;
      return opts.entries ?? [];
    },
    du: async (path) => {
      const id = path.slice(`${ROOT}/`.length);
      if (opts.sizes instanceof Error) throw opts.sizes;
      const size = opts.sizes?.[id];
      if (size === undefined) throw new Error(`no size for ${id}`);
      return size;
    },
    discard: async (path) => {
      discarded.push(path);
    },
    log: msgLogger(logs),
  };
  return { deps, discarded, logs, mcCalls };
}

/** `count` ended runs, oldest first by their `ended_at` second. */
function endedRuns(count: number, offset = 0): Array<{ id: string; ended_at: string }> {
  return Array.from({ length: count }, (_, i) => ({
    id: `run-${String(offset + i).padStart(4, "0")}`,
    ended_at: `2026-08-${String(1 + ((offset + i) % 28)).padStart(2, "0")} ${String((offset + i) % 24).padStart(2, "0")}:00:00`,
  }));
}

describe("the run log prune", () => {
  test("keeps everything while the tree is within its bound, paying only the budget read", async () => {
    const runs = endedRuns(KEEP_RUN_LOGS);
    const r = rig({ entries: runs.map((x) => x.id), runs: runList(runs) });
    await pruneRunLogs(r.deps);
    expect(r.discarded).toEqual([]);
    // With no byte budget readable (the rig's default knob value 0 means
    // unusable) the prune returns before the whole `mc run list` and the
    // container inventory. The zero board pays nothing at all.
    expect(r.mcCalls).toEqual([["config", "show"]]);
    const empty = rig({ entries: [], runs: runList([]) });
    await pruneRunLogs(empty.deps);
    expect(empty.mcCalls).toEqual([]);
  });

  test("the byte budget prunes the oldest ended runs under the count bound", async () => {
    const runs = endedRuns(3);
    const sizes: Record<string, number> = {
      [runs[0]!.id]: 600 * 1048576,
      [runs[1]!.id]: 500 * 1048576,
      [runs[2]!.id]: 10 * 1048576,
    };
    const r = rig({ entries: runs.map((x) => x.id), runs: runList(runs), budgetMB: 1024, sizes });
    await pruneRunLogs(r.deps);
    // endedRuns is oldest first for a handful of runs; freeing the single
    // oldest 600 MB brings the ended tree (1110 total) under the budget.
    expect(r.discarded).toEqual([`${ROOT}/${runs[0]!.id}`]);
    expect(r.logs.some((l) => l.includes("over the 1024 MB ended-run tree budget"))).toBe(true);
  });

  test("a failed size read skips the byte arm and removes nothing", async () => {
    const runs = endedRuns(3);
    const r = rig({ entries: runs.map((x) => x.id), runs: runList(runs),
      budgetMB: 1, sizes: new Error("stat: EPERM") });
    await pruneRunLogs(r.deps);
    expect(r.discarded).toEqual([]);
    expect(r.logs.some((l) => l.includes("the byte budget was not applied this tick"))).toBe(true);
  });

  test("the count arm's doomed runs already count against the budget", async () => {
    const runs = endedRuns(KEEP_RUN_LOGS + 3);
    const sizes: Record<string, number> = Object.fromEntries(
      runs.map((run) => [run.id, 100 * 1048576]));
    const r = rig({ entries: runs.map((x) => x.id), runs: runList(runs),
      budgetMB: 1048576, sizes });
    await pruneRunLogs(r.deps);
    // 3 over the count bound, well inside the 1 TiB budget: only the count
    // arm fires, and the message names only the count. The doomed three are
    // the tail of the newest-first order, removed oldest-first.
    const byDate = [...runs].sort((a, b) =>
      a.ended_at === b.ended_at ? a.id.localeCompare(b.id) : (a.ended_at < b.ended_at ? 1 : -1));
    expect(r.discarded).toEqual(byDate.slice(KEEP_RUN_LOGS).reverse().map((run) => `${ROOT}/${run.id}`));
    expect(r.logs.some((l) => l.includes("beyond the newest") && !l.includes("ended-run tree budget"))).toBe(true);
  });

  test("drops the oldest ended runs once the tree is over its bound", async () => {
    const runs = endedRuns(KEEP_RUN_LOGS + 3);
    const r = rig({ entries: runs.map((x) => x.id), runs: runList(runs) });
    await pruneRunLogs(r.deps);
    expect(r.discarded).toHaveLength(3);
    // Ordered by the ROW's ended_at, not by name and not by any clock this
    // process read: three of the runs above are the oldest by timestamp.
    const oldest = [...runs].sort((a, b) => (a.ended_at < b.ended_at ? -1 : 1)).slice(0, 3);
    expect(r.discarded.sort()).toEqual(oldest.map((x) => `${ROOT}/${x.id}`).sort());
    expect(r.logs.some((l) => l.includes("reclaimed 3"))).toBe(true);
  });

  test("a run that has not ended is never a candidate, however old the tree is", async () => {
    const runs: Array<{ id: string; ended_at: string | null }> = endedRuns(KEEP_RUN_LOGS + 2);
    runs[0]!.ended_at = null;
    const r = rig({ entries: runs.map((x) => x.id), runs: runList(runs) });
    await pruneRunLogs(r.deps);
    expect(r.discarded).not.toContain(`${ROOT}/${runs[0]!.id}`);
  });

  test("a run whose container is still present is left alone", async () => {
    // The row goes ended from INSIDE the container, before that container
    // exits, so ended-ness alone would race the unmount.
    const runs = endedRuns(KEEP_RUN_LOGS + 1);
    const oldest = [...runs].sort((a, b) => (a.ended_at < b.ended_at ? -1 : 1))[0]!;
    const r = rig({
      entries: runs.map((x) => x.id),
      runs: runList(runs),
      containers: ok(`${oldest.id} <no value>\n`),
    });
    await pruneRunLogs(r.deps);
    expect(r.discarded).toEqual([]);
  });

  test("a Collie session's container is recognised by its own label", async () => {
    const runs: Array<{ id: string; ended_at: string | null }> = endedRuns(KEEP_RUN_LOGS);
    runs.unshift({ id: "h-abc", ended_at: "2026-08-01 00:00:00" });
    const r = rig({
      entries: runs.map((x) => x.id),
      runs: runList(runs),
      containers: ok("<no value> h-abc\n"),
    });
    await pruneRunLogs(r.deps);
    expect(r.discarded).toEqual([]);
  });

  test("a directory naming no run row is left and named in the log", async () => {
    const runs = endedRuns(KEEP_RUN_LOGS + 1);
    const r = rig({ entries: [...runs.map((x) => x.id), "orphan"], runs: runList(runs) });
    await pruneRunLogs(r.deps);
    expect(r.discarded).not.toContain(`${ROOT}/orphan`);
    expect(r.logs.some((l) => l.includes("name no run row") && l.includes("orphan"))).toBe(true);
  });

  test("an entry outside the run-id charset is never used in a path", async () => {
    const runs = endedRuns(KEEP_RUN_LOGS + 1);
    const r = rig({ entries: [...runs.map((x) => x.id), "..", "../../sessions"], runs: runList(runs) });
    await pruneRunLogs(r.deps);
    for (const path of r.discarded) {
      expect(path.startsWith(`${ROOT}/`)).toBe(true);
      expect(path).not.toContain("..");
    }
    expect(r.logs.some((l) => l.includes("fail the run-id charset"))).toBe(true);
  });

  test("the per-tick cap bounds the work and says what is left", async () => {
    const runs = endedRuns(KEEP_RUN_LOGS + RUN_LOG_PRUNE_PER_TICK + 5);
    const r = rig({ entries: runs.map((x) => x.id), runs: runList(runs) });
    await pruneRunLogs(r.deps);
    expect(r.discarded).toHaveLength(RUN_LOG_PRUNE_PER_TICK);
    expect(r.logs.some((l) => l.includes("5 left for the next tick"))).toBe(true);
  });

  test("an unreadable run list removes nothing", async () => {
    const runs = endedRuns(KEEP_RUN_LOGS + 3);
    const r = rig({
      entries: runs.map((x) => x.id),
      runs: { exitCode: 1, stdout: "", stderr: "spine unreachable" },
    });
    await pruneRunLogs(r.deps);
    expect(r.discarded).toEqual([]);
    expect(r.logs.some((l) => l.includes("mc run list failed"))).toBe(true);
  });

  test("an unreadable container inventory removes nothing", async () => {
    const runs = endedRuns(KEEP_RUN_LOGS + 3);
    const r = rig({
      entries: runs.map((x) => x.id),
      runs: runList(runs),
      containers: { exitCode: 1, stdout: "", stderr: "docker down" },
    });
    await pruneRunLogs(r.deps);
    expect(r.discarded).toEqual([]);
  });

  test("a missing root is nothing to do, not an error", async () => {
    const r = rig({ entries: new Error("ENOENT") });
    await pruneRunLogs(r.deps);
    expect(r.discarded).toEqual([]);
    expect(r.logs).toEqual([]);
  });

  // beforeTick failure skips dispatch for the tick, so this chore's contract is
  // that it cannot fail — a tick must never lose its dispatch to disk hygiene.
  test("the chore swallows what the prune throws", async () => {
    const r = rig({ entries: [] });
    r.deps.readdir = async () => { throw new Error("ENOENT"); };
    const chore = createRunLogPruneChore(r.deps);
    await chore.beforeTick();
    r.deps.readdir = async () => Array.from({ length: KEEP_RUN_LOGS + 1 }, (_, i) => `run-${i}`);
    r.deps.runMc = async () => { throw new Error("spawn failed"); };
    await chore.beforeTick();
    expect(r.logs.some((l) => l.includes("skipped this tick"))).toBe(true);
  });
});
