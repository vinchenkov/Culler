// board.test.ts — the Board's view models. These are tested because the board
// is the operator's claim about what the system is doing: a wrong active card
// or a missing blocked reason looks exactly as correct as a right one.

import { describe, expect, test } from "bun:test";

import {
  activeTaskID,
  boardAttention,
  infrastructureFailureView,
  boardColumns,
  boardStatItems,
  boardStats,
  heldLeases,
  leaseBadge,
  leaseHeld,
  leaseView,
  leasesView,
  runningLabel,
  taskDetail,
  taskRuns,
  taskTags,
  visionNotices,
  projectOptions,
} from "./board.js";

const NOW = Date.parse("2026-08-17T12:00:00Z");

function task(over: Record<string, unknown> = {}) {
  return {
    id: 1,
    title: "a task",
    scope: "task",
    priority: 0,
    created_at: "2026-08-17 11:00:00",
    status: "seeded",
    stage_rank: 1,
    stage_entered_at: "2026-08-17 11:30:00",
    correction_count: 0,
    blocked: 0,
    blocked_reason: null,
    decision: null,
    decided_at: null,
    archived: 0,
    origin: null,
    project: "llm-council",
    branch: null,
    target_ref: null,
    ...over,
  };
}

// One `mc lease list` row: a held lease of one Project.
function lease(over: Record<string, unknown> = {}) {
  return {
    project: "llm-council",
    status: "held",
    run_id: "run-7",
    subject: 1,
    owner: "worker",
    agent_started_at: "2026-08-17 11:50:00",
    acquired_at: "2026-08-17 11:50:00",
    last_heartbeat_at: "2026-08-17 11:59:48",
    hard_deadline_at: "2026-08-17 13:00:00",
    ...over,
  };
}

// A free lease row of another Project, as the verb returns it.
const FREE = {
  project: "other",
  status: "free",
  run_id: null,
  subject: null,
  owner: null,
  acquired_at: null,
  last_heartbeat_at: null,
  hard_deadline_at: null,
};

// The `mc settings get` row, deployment-wide.
const SETTINGS = {
  timeout_minutes: 30,
  grace_minutes: 5,
  heartbeat_interval_s: 30,
  spawn_grace_s: 60,
  hard_deadline_minutes: 60,
  review_wip_cap: 3,
  collie_idle_timeout_s: 1800,
  wiki_update_interval_s: 3600,
};

describe("leaseHeld and heldLeases", () => {
  test("held is the run_id being set, free is it being null", () => {
    expect(leaseHeld(lease())).toBe(true);
    expect(leaseHeld(FREE)).toBe(false);
    expect(leaseHeld(null)).toBe(false);
  });

  test("held leases keep the verb's Project order", () => {
    const rows = [lease({ project: "alpha", run_id: "r-a" }), FREE, lease({ project: "zeta", run_id: "r-z" })];
    expect(heldLeases(rows).map((l) => l.project)).toEqual(["alpha", "zeta"]);
    expect(heldLeases(null)).toEqual([]);
  });
});

describe("activeTaskID", () => {
  test("returns the leased subject", () => {
    expect(activeTaskID(lease())).toBe(1);
  });

  test("a free lease marks nothing", () => {
    expect(activeTaskID({ run_id: null, subject: 4 })).toBeNull();
    expect(activeTaskID(FREE)).toBeNull();
    expect(activeTaskID(null)).toBeNull();
    expect(activeTaskID(undefined)).toBeNull();
  });

  // Propose runs hold their Project's lease without owning a task.
  test("a subjectless lease marks nothing", () => {
    expect(activeTaskID(lease({ subject: null }))).toBeNull();
  });
});

describe("leaseView", () => {
  test("reads the held lease off the row", () => {
    const view = leaseView(lease(), NOW, SETTINGS);
    expect(view.held).toBe(true);
    expect(view.owner).toBe("worker");
    expect(view.runID).toBe("run-7");
    expect(view.subject).toBe(1);
    expect(view.project).toBe("llm-council");
    expect(view.heartbeat).toBe("12s");
    expect(view.heartbeatSeconds).toBe(12);
    expect(view.stale).toBe(false);
    expect(view.acquiredAge).toBe("10m");
  });

  // The one fact that separates a dead lease from a busy one.
  test("flags a heartbeat older than three intervals as stale", () => {
    const view = leaseView(lease({ last_heartbeat_at: "2026-08-17 11:57:00" }), NOW, SETTINGS);
    expect(view.heartbeatSeconds).toBe(180);
    expect(view.stale).toBe(true);
  });

  test("stays inside the window at exactly three intervals", () => {
    const view = leaseView(lease({ last_heartbeat_at: "2026-08-17 11:58:30" }), NOW, SETTINGS);
    expect(view.heartbeatSeconds).toBe(90);
    expect(view.stale).toBe(false);
  });

  test("the interval is the settings row's, not the lease row's", () => {
    const row = lease({ last_heartbeat_at: "2026-08-17 11:58:00" });
    expect(leaseView(row, NOW, { ...SETTINGS, heartbeat_interval_s: 60 }).stale).toBe(false);
    expect(leaseView(row, NOW, { ...SETTINGS, heartbeat_interval_s: 30 }).stale).toBe(true);
  });

  test("defaults the interval to 30s when the settings row has not been read", () => {
    const row = lease({ last_heartbeat_at: "2026-08-17 11:58:00" });
    expect(leaseView(row, NOW, null).stale).toBe(true);
    expect(leaseView(row, NOW, undefined).stale).toBe(true);
  });

  test("a free lease reports nothing rather than zeros, but keeps its Project", () => {
    const view = leaseView(FREE, NOW, SETTINGS);
    expect(view).toEqual({
      held: false,
      project: "other",
      owner: null,
      runID: null,
      subject: null,
      heartbeat: "",
      heartbeatSeconds: null,
      stale: false,
      stopPending: false,
      acquiredAge: "",
      runAge: "",
    });
    expect(leaseView(null, NOW, SETTINGS).held).toBe(false);
    expect(leaseView(null, NOW, SETTINGS).project).toBeNull();
  });

  test("a held lease with no heartbeat yet is unknown, not stale", () => {
    const view = leaseView(lease({ last_heartbeat_at: null, acquired_at: null }), NOW, SETTINGS);
    expect(view.held).toBe(true);
    expect(view.heartbeat).toBe("");
    expect(view.heartbeatSeconds).toBeNull();
    expect(view.stale).toBe(false);
    expect(view.acquiredAge).toBe("");
  });

  test("a subjectless held lease still reports its run", () => {
    const view = leaseView(lease({ subject: null }), NOW, SETTINGS);
    expect(view.held).toBe(true);
    expect(view.subject).toBeNull();
    expect(view.runID).toBe("run-7");
  });

  test("leasesView reads every row in the verb's order", () => {
    const views = leasesView([lease({ project: "alpha" }), FREE], NOW, SETTINGS);
    expect(views.map((v) => [v.project, v.held])).toEqual([
      ["alpha", true],
      ["other", false],
    ]);
    expect(leasesView(null, NOW, SETTINGS)).toEqual([]);
  });
});

describe("leaseBadge", () => {
  test("the idle badge states that no agents are active", () => {
    const badge = leaseBadge([FREE, { ...FREE, project: "zeta" }], NOW, SETTINGS, { allProjects: true });
    expect(badge.held).toBe(0);
    expect(badge.stale).toBe(false);
    expect(badge.text).toBe("No agents are running");
    expect(badge.title).toBe("No pipeline agents are active in any Project.");
    expect(leaseBadge([FREE], NOW, SETTINGS).title).toBe("No pipeline agent is active in this Project.");
    expect(leaseBadge([], NOW, SETTINGS).held).toBe(0);
    expect(leaseBadge(null, NOW, SETTINGS).held).toBe(0);
  });

  test("a Project page names the role, the task, and the run time", () => {
    const badge = leaseBadge([lease(), FREE], NOW, SETTINGS);
    expect(badge.held).toBe(1);
    expect(badge.stale).toBe(false);
    expect(badge.text).toBe("Worker running on task #1 · 10m");
    expect(badge.title).toBe("Run run-7 started 10m ago. The last heartbeat arrived 12s ago.");
  });

  test("a run without a heartbeat says so in the tooltip", () => {
    const badge = leaseBadge([lease({ acquired_at: "2026-08-17 11:59:40", last_heartbeat_at: null })], NOW, SETTINGS);
    expect(badge.text).toBe("Worker running on task #1 · 20s");
    expect(badge.title).toBe("Run run-7 started 20s ago. The run sent no heartbeat yet.");
  });

  test("a run without a task names only the role", () => {
    const badge = leaseBadge([lease({ owner: "strategist", subject: null })], NOW, SETTINGS);
    expect(badge.text).toBe("Strategist running · 10m");
  });

  test("a run in a paused Project is finishing", () => {
    const paused = new Set(["alpha"]);
    const badge = leaseBadge([lease({ project: "alpha" })], NOW, SETTINGS, { paused });
    expect(badge.text).toBe("Worker finishing run on task #1 · 10m");
    expect(badge.title).toBe("Run run-7 started 10m ago. The last heartbeat arrived 12s ago. The Project is paused, so no new run starts after this run.");
    const strategist = leaseBadge([lease({ project: "alpha", owner: "strategist", subject: null })], NOW, SETTINGS, { paused });
    expect(strategist.text).toBe("Strategist finishing run · 10m");
    expect(leaseBadge([lease({ project: "alpha" })], NOW, SETTINGS, { paused: new Set(["beta"]) }).text).toBe("Worker running on task #1 · 10m");
  });

  test("a stale heartbeat comes first, so a truncated badge still shows it", () => {
    const badge = leaseBadge([lease({ last_heartbeat_at: "2026-08-17 11:50:00" })], NOW, SETTINGS);
    expect(badge.stale).toBe(true);
    expect(badge.text).toBe("Heartbeat stopped 10m ago · Worker on task #1");
    expect(badge.title).toBe("The last heartbeat arrived 10m ago. The runner may have disappeared.");
  });

  test("a global page counts the runs and names each Project by title in the tooltip", () => {
    const rows = [
      lease({ project: "alpha", run_id: "r-a", owner: "worker", subject: 4 }),
      FREE,
      lease({ project: "zeta", run_id: "r-z", owner: "strategist", subject: null }),
    ];
    const titles = new Map([["alpha", "Alpha Site"]]);
    const badge = leaseBadge(rows, NOW, SETTINGS, { allProjects: true, titles });
    expect(badge.held).toBe(2);
    expect(badge.text).toBe("2 agents running");
    expect(badge.title).toBe("Alpha Site: Worker running on task #4 · 10m\nProject name unavailable: Strategist running · 10m");
    const single = leaseBadge([rows[0]], NOW, SETTINGS, { allProjects: true, titles });
    expect(single.text).toBe("1 agent running");
    expect(single.title).toBe("Alpha Site: Worker running on task #4 · 10m");
  });

  test("a global page counts the runs of paused Projects as finishing", () => {
    const rows = [
      lease({ project: "alpha", run_id: "r-a", owner: "worker", subject: 4 }),
      lease({ project: "beta", run_id: "r-b", owner: "strategist", subject: null }),
      lease({ project: "zeta", run_id: "r-z" }),
    ];
    const one = leaseBadge(rows, NOW, SETTINGS, { allProjects: true, paused: new Set(["alpha"]) });
    expect(one.text).toBe("2 agents running · 1 agent finishing run");
    expect(one.title).toBe("Project name unavailable: Worker finishing run on task #4 · 10m\nProject name unavailable: Strategist running · 10m\nProject name unavailable: Worker running on task #1 · 10m");
    const all = leaseBadge(rows, NOW, SETTINGS, { allProjects: true, paused: new Set(["alpha", "beta", "zeta"]) });
    expect(all.text).toBe("3 agents finishing runs");
  });

  test("the count text keeps one form for any number of runs", () => {
    const rows = Array.from({ length: 6 }, (_, index) => lease({ project: `project-${index}`, run_id: `r-${index}` }));
    expect(leaseBadge(rows, NOW, SETTINGS, { allProjects: true }).text).toBe("6 agents running");
  });

  test("the count text states stale heartbeats first and pending stops last", () => {
    const rows = [
      lease({ project: "alpha", run_id: "r-a" }),
      lease({ project: "beta", run_id: "r-b", run_outcome: "done", run_ended_at: "2026-08-17 11:59:00" }),
      lease({ project: "zeta", run_id: "r-z", last_heartbeat_at: "2026-08-17 11:50:00" }),
    ];
    const badge = leaseBadge(rows, NOW, SETTINGS, { allProjects: true });
    expect(badge.stale).toBe(true);
    expect(badge.text).toBe("1 heartbeat stopped · 2 agents running · 1 agent stopping");
    expect(badge.title).toContain("Project name unavailable: Heartbeat stopped 10m ago · Worker on task #1");
    expect(badge.title).toContain("Project name unavailable: Stopping Worker on task #1");
  });
});

describe("projectOptions", () => {
  test("all first, then unique projects sorted", () => {
    const tasks = [
      task({ id: 1, project: "zeta" }),
      task({ id: 2, project: "alpha" }),
      task({ id: 3, project: "zeta" }),
    ];
    expect(projectOptions(tasks)).toEqual(["all", "alpha", "zeta"]);
  });

  test("skips absent and empty projects", () => {
    expect(projectOptions([task({ project: null }), task({ id: 2, project: "" })])).toEqual(["all"]);
    expect(projectOptions([])).toEqual(["all"]);
    expect(projectOptions(null)).toEqual(["all"]);
  });
});

describe("taskTags", () => {

  // A correction appears in the task dialog run history, not as a pill.
  test("expedited priority gets a pill; corrections and origin never do", () => {
    const tags = taskTags(task({ project: null, origin: "user", priority: -1, correction_count: 2 }));
    expect(tags).toEqual([{ label: "expedited", kind: "priority" }]);
  });

  test("the project pill rides last, and only when asked for", () => {
    expect(taskTags(task({ priority: -1 })).map((t) => t.kind)).toEqual(["priority", "project"]);
    expect(taskTags(task(), { showProject: false })).toEqual([]);
  });

  test("an older row with no optional columns produces no pills", () => {
    expect(taskTags({ id: 9, status: "seeded" })).toEqual([]);
  });
});

describe("runningLabel", () => {
  test("names the run age once it is a minute old", () => {
    expect(runningLabel(leaseView(lease(), NOW, SETTINGS))).toBe("Running now · 10m");
    expect(runningLabel(leaseView(lease({ acquired_at: "2026-08-17 11:59:40" }), NOW, SETTINGS))).toBe("Running now");
  });

  test("a stale heartbeat does not read as running", () => {
    const view = leaseView(lease({ last_heartbeat_at: "2026-08-17 11:50:00" }), NOW, SETTINGS);
    expect(runningLabel(view)).toBe("Heartbeat stopped");
  });
});

describe("boardColumns", () => {
  test("renders every stage in pipeline order with its counts", () => {
    const tasks = [
      task({ id: 1, status: "proposed" }),
      task({ id: 2, status: "packaged" }),
      task({ id: 3, status: "packaged", blocked: 1, blocked_reason: "needs input" }),
    ];
    const cols = boardColumns(tasks, null, { nowMs: NOW });
    expect(cols.map((c) => c.status)).toEqual(["proposed", "seeded", "worked", "verified", "packaged"]);
    expect(cols.map((c) => c.count)).toEqual([1, 0, 0, 0, 2]);
    expect(cols[4].blockedCount).toBe(1);
    expect(cols[0].label).toBe("Proposed");
    expect(cols[4].label).toBe("Hatched");
    expect(cols[0].tone).toBe("sky");
  });

  test("an empty task list still yields five empty columns", () => {
    const cols = boardColumns([], null, { nowMs: NOW });
    expect(cols).toHaveLength(5);
    expect(cols.every((c) => c.count === 0 && c.blockedCount === 0 && c.cards.length === 0)).toBe(true);
  });

  test("the project filter keeps only exact matches", () => {
    const tasks = [
      task({ id: 1, project: "alpha" }),
      task({ id: 2, project: "beta" }),
      task({ id: 3, project: null }),
    ];
    const all = boardColumns(tasks, null, { nowMs: NOW });
    expect(all[1].count).toBe(3);
    const alpha = boardColumns(tasks, null, { project: "alpha", nowMs: NOW });
    expect(alpha[1].cards.map((c) => c.id)).toEqual([1]);
  });

  test("orders expedited, then blocked, then the longest wait", () => {
    const tasks = [
      task({ id: 1, stage_entered_at: "2026-08-17 11:50:00" }),
      task({ id: 2, stage_entered_at: "2026-08-17 10:00:00" }),
      task({ id: 3, blocked: 1, blocked_reason: "stuck", stage_entered_at: "2026-08-17 11:55:00" }),
      task({ id: 4, priority: -1, stage_entered_at: "2026-08-17 11:59:00" }),
    ];
    const cols = boardColumns(tasks, null, { nowMs: NOW });
    expect(cols[1].cards.map((c) => c.id)).toEqual([4, 3, 2, 1]);
  });

  test("the running card leads its column, ahead of expedited and blocked", () => {
    const tasks = [
      task({ id: 1, stage_entered_at: "2026-08-17 10:00:00" }),
      task({ id: 2, stage_entered_at: "2026-08-17 11:50:00" }),
      task({ id: 3, blocked: 1, blocked_reason: "stuck", stage_entered_at: "2026-08-17 11:55:00" }),
      task({ id: 4, priority: -1, stage_entered_at: "2026-08-17 11:59:00" }),
    ];
    const cols = boardColumns(tasks, [lease({ subject: 2 })], { nowMs: NOW, settings: SETTINGS });
    expect(cols[1].cards.map((c) => c.id)).toEqual([2, 4, 3, 1]);
  });

  test("ties keep spine order", () => {
    const tasks = [
      task({ id: 7, stage_entered_at: "2026-08-17 11:00:00" }),
      task({ id: 5, stage_entered_at: "2026-08-17 11:00:00" }),
      task({ id: 6, stage_entered_at: "2026-08-17 11:00:00" }),
    ];
    expect(boardColumns(tasks, null, { nowMs: NOW })[1].cards.map((c) => c.id)).toEqual([7, 5, 6]);
  });

  test("falls back to created_at when the stage stamp is absent", () => {
    const tasks = [
      task({ id: 1, stage_entered_at: null, created_at: "2026-08-17 11:55:00" }),
      task({ id: 2, stage_entered_at: null, created_at: "2026-08-17 09:00:00" }),
    ];
    const cards = boardColumns(tasks, null, { nowMs: NOW })[1].cards;
    expect(cards.map((c) => c.id)).toEqual([2, 1]);
    expect(cards[0].stageAge).toBe("3h");
    expect(cards[0].age).toBe("3h");
  });

  test("one held lease makes exactly one card active and only it carries the run", () => {
    const tasks = [task({ id: 1 }), task({ id: 2 }), task({ id: 3, status: "worked" })];
    const cols = boardColumns(tasks, [lease({ subject: 2 }), FREE], { nowMs: NOW, settings: SETTINGS });
    const cards = [...cols[1].cards, ...cols[2].cards];
    expect(cards.filter((c) => c.active).map((c) => c.id)).toEqual([2]);
    const active = cards.find((c) => c.active)!;
    expect(active.runID).toBe("run-7");
    expect(active.heartbeat).toBe("12s");
    for (const idle of cards.filter((c) => !c.active)) {
      expect(idle.runID).toBeNull();
      expect(idle.heartbeat).toBeNull();
    }
  });

  // Two active Projects run two leased runs at once, one each — so the
  // board shows one active card per Project, each carrying its own lease's
  // run.
  test("two held leases make one active card per Project, each with its own run", () => {
    const tasks = [
      task({ id: 1, project: "alpha" }),
      task({ id: 2, project: "alpha", status: "worked" }),
      task({ id: 3, project: "beta" }),
      task({ id: 4, project: "beta", status: "worked" }),
    ];
    const leases = [
      lease({ project: "alpha", run_id: "r-a", subject: 2, last_heartbeat_at: "2026-08-17 11:59:55" }),
      lease({ project: "beta", run_id: "r-b", subject: 3, last_heartbeat_at: "2026-08-17 11:59:30" }),
    ];
    const cols = boardColumns(tasks, leases, { nowMs: NOW, settings: SETTINGS });
    const cards = cols.flatMap((c) => c.cards);
    expect(cards.filter((c) => c.active).map((c) => c.id).sort()).toEqual([2, 3]);
    expect(cards.find((c) => c.id === 2)).toMatchObject({ runID: "r-a", heartbeat: "5s" });
    expect(cards.find((c) => c.id === 3)).toMatchObject({ runID: "r-b", heartbeat: "30s" });
    expect(cards.find((c) => c.id === 1)!.runID).toBeNull();
    expect(cards.find((c) => c.id === 4)!.runID).toBeNull();
    // The filter keeps only that Project's active card.
    const beta = boardColumns(tasks, leases, { project: "beta", nowMs: NOW, settings: SETTINGS });
    expect(beta.flatMap((c) => c.cards).filter((c) => c.active).map((c) => c.id)).toEqual([3]);
  });

  test("a subjectless lease marks no card at all", () => {
    const cols = boardColumns([task({ id: 1 }), task({ id: 2 })], [lease({ subject: null })], { nowMs: NOW });
    expect(cols[1].cards.some((c) => c.active)).toBe(false);
    expect(cols[1].cards.every((c) => c.runID === null)).toBe(true);
  });

  test("a blocked card with no recorded reason still says so", () => {
    const cols = boardColumns([task({ blocked: 1, blocked_reason: null })], null, { nowMs: NOW });
    expect(cols[1].cards[0].blocked).toBe(true);
    expect(cols[1].cards[0].reason).toBe("(no reason recorded)");
  });

  test("keeps a recorded reason and leaves unblocked cards without one", () => {
    const tasks = [task({ id: 1, blocked: 1, blocked_reason: "needs input" }), task({ id: 2 })];
    const cards = boardColumns(tasks, null, { nowMs: NOW })[1].cards;
    expect(cards.find((c) => c.id === 1)!.reason).toBe("needs input");
    expect(cards.find((c) => c.id === 2)!.reason).toBeNull();
  });

  test("carries the task facts and an untitled fallback", () => {
    const tasks = [
      task({ id: 8, title: "", priority: -1 }),
    ];
    const cards = boardColumns(tasks, null, { nowMs: NOW })[1].cards;
    const card = cards.find((c) => c.id === 8)!;
    expect(card.title).toBe("(untitled)");
    expect(card.expedited).toBe(true);
    expect(card.scope).toBe("task");
    expect(card.project).toBe("llm-council");
    expect(card.tags.map((t) => t.kind)).toEqual(["priority"]);
    expect(card).not.toHaveProperty("sortMs");
  });

  // The pill repeats the filter when the filter names one Project, and
  // repeats itself when the whole board has one.
  test("the project pill shows only on an unfiltered board of several Projects", () => {
    expect(boardColumns([task({ id: 1 })], null, { nowMs: NOW })[1].cards[0].tags).toEqual([]);
    const several = [task({ id: 1, project: "alpha" }), task({ id: 2, project: "beta" })];
    const labels = (cols: ReturnType<typeof boardColumns>) => cols[1].cards.map((c) => c.tags.map((t) => t.label));
    expect(labels(boardColumns(several, null, { nowMs: NOW, projects: [{ id: "alpha", title: "Alpha Site" }, { id: "beta", title: "Beta Site" }] }))).toEqual([["Alpha Site"], ["Beta Site"]]);
    expect(labels(boardColumns(several, null, { project: "alpha", nowMs: NOW }))).toEqual([[]]);
  });

  test("the running card carries the chip text and the stale flag", () => {
    const live = boardColumns([task()], [lease()], { nowMs: NOW, settings: SETTINGS })[1].cards[0];
    expect(live).toMatchObject({ running: "Running now · 10m", stale: false });
    const dead = lease({ last_heartbeat_at: "2026-08-17 11:50:00" });
    const stale = boardColumns([task()], [dead], { nowMs: NOW, settings: SETTINGS })[1].cards[0];
    expect(stale).toMatchObject({ running: "Heartbeat stopped", stale: true });
    expect(boardColumns([task()], null, { nowMs: NOW })[1].cards[0].running).toBeNull();
  });

  test("tolerates absent tasks and an absent options object", () => {
    expect(boardColumns(null, null, { nowMs: NOW })[0].count).toBe(0);
    expect(boardColumns([task()], null)).toHaveLength(5);
  });
});

describe("taskDetail", () => {
  // One `mc task get` row: the list row plus the prose columns.
  const row = (over: Record<string, unknown> = {}) =>
    task({ description: "Do the thing well.", dispatch_retries: 3, ...over });

  test("reads the prose, the origin and the ages off the get row", () => {
    const d = taskDetail(row({ origin: "user" }), { tasks: [], leases: null, nowMs: NOW });
    expect(d).toMatchObject({
      id: 1,
      scope: "task",
      description: "Do the thing well.",
      reason: null,
      run: null,
      decision: null,
      decidedAge: null,
      packet: null,
      origin: "user",
      createdAge: "1h ago",
      stageAge: "30m",
      stage: "Seeded",
    });
  });

  test("reads the Strategic rationale of a task that the Strategist proposed, and none of a task that the operator defined", () => {
    const strategist = taskDetail(row({ origin: "autonomous", strategic_rationale: "Observed: the export omits the date." }), { nowMs: NOW });
    expect(strategist).toMatchObject({ origin: "autonomous", strategicRationale: "Observed: the export omits the date." });
    expect(taskDetail(row({ origin: "user" }), { nowMs: NOW }).strategicRationale).toBeNull();
  });

  test("a wait under a minute, or with no stamp, is no stage age", () => {
    expect(taskDetail(row({ stage_entered_at: "2026-08-17 11:59:40" }), { nowMs: NOW }).stageAge).toBeNull();
    expect(taskDetail(row({ stage_entered_at: null, created_at: null }), { nowMs: NOW }).stageAge).toBeNull();
    expect(taskDetail(row({ stage_entered_at: "2026-08-17 11:59:00" }), { nowMs: NOW }).stageAge).toBe("1m");
  });

  test("blank prose is absent, and a blocked row without a reason still says so", () => {
    const d = taskDetail(row({ description: "  ", blocked: 1, blocked_reason: null }), { nowMs: NOW });
    expect(d.description).toBeNull();
    expect(d.reason).toBe("(no reason recorded)");
  });

  test("the dialog carries no correction notes, commits, or delivery records", () => {
    const d = taskDetail(row({
      status: "packaged", revision_notes: "{\"conflict\":true}", verified_sha: "a".repeat(40), completion_sha: "b".repeat(40),
      delivery: { report_path: "/mc/artifacts/p/1/report.html", source_verified_commit: "a".repeat(40), temporary_repositories: [{ run_id: "run-1", bytes: 9 }] },
    }), { nowMs: NOW });
    expect(JSON.stringify(d)).not.toMatch(/conflict|a{40}|b{40}|report\.html|run-1/);
  });

  test("the packet link needs a packet that the Review reader opens", () => {
    expect(taskDetail(row({ status: "packaged" }), { packets: [], nowMs: NOW }).packet).toBeNull();
    const listed = [{ task_id: 1, archived: 0, review_state: "awaits_decision" }];
    expect(taskDetail(row({ status: "packaged" }), { packets: listed, nowMs: NOW }).packet).toBe(1);
    const replaced = [{ task_id: 1, archived: 0, review_state: "replaced" }];
    expect(taskDetail(row(), { packets: replaced, nowMs: NOW }).packet).toBeNull();
    expect(boardColumns([task()], [], { packets: replaced })[1].cards[0].packet).toBeNull();
  });

  test("the held lease is the current run, with its liveness", () => {
    const d = taskDetail(row(), { leases: [lease()], nowMs: NOW, settings: SETTINGS });
    expect(d.run).toMatchObject({ role: "worker", runID: "run-7", heartbeat: "12s", stale: false, age: "10m", stopPending: false });
    expect(taskDetail(row(), { leases: [lease({ subject: 2 })], nowMs: NOW }).run).toBeNull();
  });

  test("a packaged row names its packet; a decided row names the decision and its age", () => {
    const packaged = taskDetail(row({ id: 5, status: "packaged" }), { nowMs: NOW, packets: [{ task_id: 5, archived: 0 }] });
    expect(packaged.packet).toBe(5);
    expect(packaged.stage).toBe("Hatched");
    const decided = taskDetail(row({ status: "packaged", decision: "approved", decided_at: "2026-08-17 11:45:00" }), {
      nowMs: NOW,
    });
    expect(decided).toMatchObject({ decision: "approved", decidedAge: "15m ago" });
  });

  test("an approved task states the merge state text of its packet row", () => {
    const text = "Culler merges task #3 first.";
    const packets = [{ task_id: 5, archived: 0, review_state: "approved", merge_state: "after-task", merge_state_text: text, merge_task_id: 3 }];
    const approved = row({ id: 5, status: "packaged", decision: "approved", decided_at: "2026-08-17 11:45:00" });
    expect(taskDetail(approved, { nowMs: NOW, packets }).mergeStateText).toBe(text);
    expect(taskDetail(row({ id: 5, status: "packaged" }), { nowMs: NOW, packets }).mergeStateText).toBeNull();
    expect(taskDetail({ ...approved, archived: 1 }, { nowMs: NOW, packets }).mergeStateText).toBeNull();
    expect(taskDetail(approved, { nowMs: NOW, packets: [{ ...packets[0], review_state: "merged" }] }).mergeStateText).toBeNull();
  });
});

describe("boardStatItems", () => {
  test("leads with blocked and active work, and marks zero counts", () => {
    const items = boardStatItems({ total: 4, blocked: 2, stalled: 0, expedited: 0, activeIDs: [3, 7] });
    expect(items.map((item) => item.key)).toEqual(["blocked", "active", "total", "stalled"]);
    expect(items.map((item) => item.value)).toEqual([2, 2, 4, 0]);
    expect(items.every((item) => typeof item.label === "string" && item.label !== "")).toBe(true);
    expect(items.map((item) => item.lead)).toEqual([true, true, false, false]);
    expect(items.map((item) => item.zero)).toEqual([false, false, false, true]);
    expect(items[0].alert).toBe(true);
    expect(items[1].value).toBe(2);
  });

  test("keeps blocked and active first when both are zero, with less weight", () => {
    const items = boardStatItems({ total: 2, blocked: 0, stalled: 1, expedited: 1, activeIDs: [] });
    expect(items.map((item) => item.key)).toEqual(["blocked", "active", "total", "stalled", "expedited"]);
    expect(items.map((item) => item.value)).toEqual([0, 0, 2, 1, 1]);
    expect(items.slice(0, 2).every((item) => item.lead && item.zero && !item.alert)).toBe(true);
    expect(items[1].value).toBe(0);
  });
});

describe("boardStats", () => {
  const tasks = [
    task({ id: 1, project: "alpha", priority: -1 }),
    task({ id: 2, project: "beta", blocked: 1, blocked_reason: "stuck" }),
    task({ id: 3, project: "alpha", blocked: 1, blocked_reason: "stuck" }),
  ];

  test("counts the whole board by default, with every active subject", () => {
    const leases = [
      lease({ project: "alpha", run_id: "r-a", subject: 3 }),
      lease({ project: "beta", run_id: "r-b", subject: 2 }),
    ];
    expect(boardStats(tasks, leases)).toEqual({
      total: 3,
      blocked: 2,
      stalled: 0,
      expedited: 1,
      activeIDs: [3, 2],
      activeRuns: 2,
    });
  });

  test("counts only the filtered set, active subjects included", () => {
    const leases = [
      lease({ project: "alpha", run_id: "r-a", subject: 3 }),
      lease({ project: "beta", run_id: "r-b", subject: 2 }),
    ];
    expect(boardStats(tasks, leases, { project: "alpha" })).toEqual({
      total: 2,
      blocked: 1,
      stalled: 0,
      expedited: 1,
      activeIDs: [3],
      activeRuns: 1,
    });
  });

  test("a subjectless or free lease contributes no active subject", () => {
    expect(boardStats(tasks, [lease({ subject: null }), FREE]).activeIDs).toEqual([]);
  });

  test("an empty board counts zero", () => {
    expect(boardStats([], null)).toEqual({ total: 0, blocked: 0, stalled: 0, expedited: 0, activeIDs: [], activeRuns: 0 });
    expect(boardStats(null, null).total).toBe(0);
  });
});

describe("visionNotices", () => {
  const ws = (over: Record<string, unknown> = {}) => ({
    id: "project",
    title: "The project",
    status: "active",
    vision: "present",
    vision_status: { state: "present", ready: true, path: "/workspace/project/VISION.md", reason: "" },
    ...over,
  });
  const blocked = (state: string) => ({
    state, ready: false, path: "/workspace/project/VISION.md", reason: `The file is ${state}.`,
  });

  test("current valid files produce no notice", () => {
    expect(visionNotices([ws({ vision: "declined" })])).toEqual([]);
    expect(visionNotices([])).toEqual([]);
    expect(visionNotices(undefined)).toEqual([]);
  });

  test("current file failures override the setup record and preserve the host facts", () => {
    const rows = visionNotices([
      ws({ id: "zeta", title: "", vision_status: blocked("absent") }),
      ws({ id: "alpha", vision_status: blocked("unreadable") }),
    ]);
    expect(rows).toEqual([
      { id: "alpha", title: "The project", state: "unreadable", path: "/workspace/project/VISION.md", reason: "The file is unreadable." },
      { id: "zeta", title: "Project name unavailable", state: "absent", path: "/workspace/project/VISION.md", reason: "The file is absent." },
    ]);
  });

  test("every blocked file state appears for active and paused repositories", () => {
    for (const state of ["absent", "unreadable", "empty", "template", "unknown"]) {
      expect(visionNotices([ws({ vision_status: blocked(state) })])[0].state).toBe(state);
      expect(visionNotices([ws({ status: "paused", vision_status: blocked(state) })])).toHaveLength(1);
    }
  });

  test("archived Projects need no notice; absent host facts produce no invented status", () => {
    expect(visionNotices([
      ws({ status: "archived", vision_status: blocked("absent") }),
      ws({ vision_status: undefined }),
      null,
    ])).toEqual([]);
  });
});

describe("task failure facts", () => {
  test("the attention threshold applies to each Project", () => {
    const rows = [
      task({ id: 1, blocked: 1, blocked_reason: "The operator must select the scope.", project: "alpha" }),
      task({ id: 2, stalled: true, infrastructure_failure_count: 3, project: "beta" }),
      task({ id: 3, stalled: 1, blocked: 1, infrastructure_failure_count: 3, project: "gamma" }),
      task({ id: 3, stalled: 1 }),
      task({ id: 4, archived: 1, stalled: true }),
    ];
    const view = boardAttention(rows);
    expect(view.count).toBe(3);
    expect(view.paused).toBe(false);
    expect(view.tasks.map((t) => t.id)).toEqual([1, 2, 3]);
    expect(boardStats(rows, [], { project: "alpha" }).total).toBe(1);
    expect(boardAttention(rows.slice(0, 2)).paused).toBe(false);
    const sameProject = rows.slice(0, 3).map((row) => ({ ...row, project: "alpha" }));
    expect(boardAttention(sameProject).projects).toEqual([{ project: "alpha", count: 3, paused: true }]);
    expect(boardAttention(sameProject).paused).toBe(true);
  });

  test("three tasks with one failure each preserve the allowance", () => {
    const rows = [1, 2, 3].map((id) => task({ id, infrastructure_failure_count: 1, stalled: false }));
    expect(boardAttention(rows)).toEqual({ count: 0, limit: 3, paused: false, projects: [], tasks: [] });
  });

  test("approved card shows Approved", () => {
    const packets = [{ task_id: 5, archived: 0, review_state: "approved" }, { task_id: 6, archived: 0, review_state: "awaits_decision" }];
    const rows = [
      task({ id: 5, status: "packaged", decision: "approved", decided_at: "2026-08-17 11:45:00" }),
      task({ id: 6, status: "packaged" }),
    ];
    const cards = boardColumns(rows, [], { packets }).find((column) => column.status === "packaged")!.cards;
    expect(cards.map((card) => [card.id, card.approved, card.packet])).toEqual([[5, true, 5], [6, false, 6]]);
  });

  test("a stalled task retains its stage and earlier packet", () => {
    const row = task({ id: 7, status: "seeded", stalled: true, infrastructure_failure_count: 3, verified_sha: "a".repeat(40), completion_sha: "b".repeat(40) });
    const packets = [{ task_id: 7, archived: 0 }];
    const columns = boardColumns([row], [], { packets });
    expect(columns[1].cards[0]).toMatchObject({ id: 7, stalled: true, blocked: false, packet: 7 });
    expect(columns[1].stalledCount).toBe(1);
    expect(columns[4].count).toBe(0);
    expect(taskDetail(row, { packets })).toMatchObject({
      stage: "Seeded", stalled: true, packet: 7, failure: { count: 3 },
    });
    expect(taskDetail(row, { packets: [{ task_id: 7, archived: 1 }] }).packet).toBeNull();
  });

  test("a card names the connection that its next run waits for and carries no failure record", () => {
    const row = task({ id: 8, infrastructure_failure_count: 1, connection_wait: "connection-7" });
    const card = boardColumns([row], [])[1].cards[0];
    expect(card).toMatchObject({ id: 8, connectionWait: "connection-7", attention: false });
    expect(card).not.toHaveProperty("failure");
    expect(taskDetail(row)).toMatchObject({ connectionWait: "connection-7", failure: { count: 1 } });
    expect(boardColumns([task({ id: 9 })], [])[1].cards[0].connectionWait).toBeNull();
  });

  test("a run exit does not establish a credential or quota diagnosis", () => {
    const failure = {
      run_id: "run-1", task_id: 1, condition: "runner-exited", source: "runner", cause: "credentials",
      evidence: "", operator_action: "Renew credentials.", quota_reset_at: "2026-08-18T12:00:00Z",
      failed_at: "2026-08-17T12:00:00Z",
    };
    const view = infrastructureFailureView(task({ infrastructure_failure_count: 1, latest_infrastructure_failure: failure }));
    expect(view.latest).toMatchObject({
      taskID: 1, runID: "run-1", condition: "runner-exited", source: "runner", cause: "unknown",
      diagnosis: "Unknown cause", evidence: null, operatorAction: null, quotaResetAt: null,
    });
  });

  test("a supported agent diagnosis retains its source and supplied quota facts", () => {
    const failure = {
      run_id: "run-2", task_id: 1, condition: "request failed", source: "agent", cause: "quota",
      evidence: "The service reports quota exhaustion.", operator_action: "Wait for the service quota reset.",
      quota_reset_at: "2026-08-18T12:00:00Z", failed_at: "2026-08-17T12:00:00Z",
    };
    const view = infrastructureFailureView(task({ infrastructure_failure_count: 3, latest_infrastructure_failure: failure }));
    expect(view.latest).toMatchObject({
      diagnosis: "Agent diagnosis: Quota failure", evidence: failure.evidence,
      operatorAction: failure.operator_action, quotaResetAt: failure.quota_reset_at, failedAt: failure.failed_at,
    });
    expect(infrastructureFailureView(task()).latest).toBeNull();
  });
});

test("a final run retains its lease without a false active task", () => {
  const row = task({ stalled: true, infrastructure_failure_count: 3 });
  const held = lease({ run_outcome: "infra-failed", run_ended_at: "2026-08-17 11:59:00" });
  expect(activeTaskID(held)).toBeNull();
  expect(boardStats([row], [held]).activeIDs).toEqual([]);
  expect(boardColumns([row], [held], { nowMs: NOW })[1].cards[0]).toMatchObject({
    active: false, stalled: true, stopPending: true, running: "Stopping",
  });
  expect(taskDetail(row, { leases: [held], nowMs: NOW })).toMatchObject({
    running: "Stopping", stopPending: true, run: { runID: "run-7", stopPending: true },
  });
  expect(leaseBadge([held], NOW).text).toBe("Stopping Worker on task #1");
});

describe("shared Arbiter proposal review", () => {
  const tasks = [10,11,12].map(id => task({ id, status: "proposed" }));
  const review = lease({ subject: null, owner: "arbiter", run_id: "review", pool_snapshot: "[10,11]" });
  test("two separate cards share one run and the same review indicator", () => {
    const cards = boardColumns(tasks,[review],{nowMs:NOW,settings:SETTINGS})[0].cards;
    for(const id of [10,11]) {
      expect(cards.find(card => card.id === id)).toMatchObject({active:true,runID:"review",running:"Under Arbiter proposal review"});
    }
    expect(cards.find(card => card.id === 12)).toMatchObject({active:false,runID:null});
    expect(boardStats(tasks,[review]).activeRuns).toBe(1);
    expect(boardStatItems(boardStats(tasks,[review]))[1]).toMatchObject({key:"active",value:1});
  });
  test("an ended review clears all active indicators and preserves the canonical history", () => {
    const ended={...review,run_outcome:"completed",run_ended_at:"2026-08-17 12:00:00"};
    const cards=boardColumns(tasks,[ended],{nowMs:NOW})[0].cards;
    expect(cards.every(card => !card.active && card.runID === null)).toBe(true);
    const run={id:"review",project:review.project,subject:null,role:"arbiter",pool_snapshot:review.pool_snapshot,outcome:"completed"};
    for(const member of tasks.slice(0,2)) expect(taskRuns([run],{...member,status:"seeded"})).toEqual([run]);
    expect(taskRuns([run],tasks[2])).toEqual([]);
    expect(taskRuns([{...run,project:"other"}],tasks[0])).toEqual([]);
  });
  test("a claim without process start identifies the pending agent launch", () => {
    const claimed={...review,agent_started_at:null};
    expect(leaseBadge([claimed],NOW,SETTINGS).text).toBe("Arbiter agent launch pending");
    expect(boardColumns(tasks,[claimed],{nowMs:NOW})[0].cards[0].running).toBe("Agent launch pending");
    expect(leaseBadge([claimed],NOW,SETTINGS,{allProjects:true}).text).toBe("1 agent launch pending");
    expect(leaseBadge([claimed,lease({project:"other"})],NOW,SETTINGS,{allProjects:true}).text).toBe("1 agent launch pending · 1 agent running");
  });
});
