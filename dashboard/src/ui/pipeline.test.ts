import { describe, expect, test } from "bun:test";
import {
  heartbeatStopped,
  leaseStale,
  outboxLagView,
  runsView,
  doctorView,
  healthView,
  degradedUnits,
} from "./pipeline.js";
import { TOPOLOGY_NODES, TOPOLOGY_EDGES, topologyView } from "./topology.js";
import { stampLabel } from "./format.js";

const NOW = Date.parse("2026-08-17T12:00:00Z");

const task = (over: Record<string, unknown> = {}) => ({
  id: 1,
  title: "a task",
  status: "seeded",
  blocked: 0,
  blocked_reason: null,
  project: "alpha",
  ...over,
});

// One held `mc lease list` row.
const leaseHeld = (over: Record<string, unknown> = {}) => ({
  project: "alpha",
  status: "held",
  run_id: "r-9",
  owner: "worker",
  subject: 1,
  acquired_at: "2026-08-17 11:50:00",
  last_heartbeat_at: "2026-08-17 11:59:50",
  hard_deadline_at: "2026-08-17 13:00:00",
  ...over,
});

const FREE_LEASE = {
  project: "alpha",
  status: "free",
  run_id: null,
  owner: null,
  subject: null,
  acquired_at: null,
  last_heartbeat_at: null,
  hard_deadline_at: null,
};

// The `mc settings get` row.
const SETTINGS = { heartbeat_interval_s: 60 };

describe("leaseStale", () => {
  test("a free lease is never stale", () => {
    expect(leaseStale(FREE_LEASE, NOW, SETTINGS)).toBe(false);
    expect(leaseStale(null, NOW, SETTINGS)).toBe(false);
  });

  test("three missed beats against the settings row's interval is stale", () => {
    const lease = leaseHeld({ last_heartbeat_at: "2026-08-17 11:56:00" });
    expect(leaseStale(lease, NOW, { heartbeat_interval_s: 60 })).toBe(true);
    expect(leaseStale(lease, NOW, { heartbeat_interval_s: 120 })).toBe(false);
  });

  test("an unread settings row falls back to a short interval", () => {
    expect(leaseStale(leaseHeld({ last_heartbeat_at: "2026-08-17 11:59:40" }), NOW, null)).toBe(false);
    expect(leaseStale(leaseHeld({ last_heartbeat_at: "2026-08-17 11:58:00" }), NOW, null)).toBe(true);
  });
});

describe("heartbeatStopped", () => {
  test("three missed beats stop the heartbeat, and a run with no heartbeat is not stopped", () => {
    expect(heartbeatStopped("2026-08-17 11:56:00", NOW, SETTINGS)).toBe(true);
    expect(heartbeatStopped("2026-08-17 11:57:00", NOW, SETTINGS)).toBe(false);
    expect(heartbeatStopped(null, NOW, SETTINGS)).toBe(false);
  });
});

describe("outboxLagView", () => {
  test("tones step from leaf to sky to rust with the backlog", () => {
    const view = outboxLagView([
      { surface: "dashboard", undelivered: 0, oldest_created_at: null, capped: false },
      { surface: "discord", undelivered: 4, oldest_created_at: "2026-08-17 11:30:00", capped: false },
      { surface: "email", undelivered: 12, oldest_created_at: "2026-08-17 09:00:00", capped: false },
    ]);
    expect(view.rows.map((r) => r.tone)).toEqual(["leaf", "sky", "rust"]);
    expect(view.total).toBe(16);
    expect(view.capped).toBe(false);
    expect(view.rows[1].oldest).toBe("2026-08-17 11:30:00");
    expect(view.rows[0].oldest).toBeNull();
  });

  test("a capped count is a floor: the row and the total both say so", () => {
    const view = outboxLagView([
      { surface: "discord", undelivered: 5, oldest_created_at: null, capped: true },
    ]);
    expect(view.rows[0].capped).toBe(true);
    expect(view.rows[0].tone).toBe("rust");
    expect(view.capped).toBe(true);
    expect(view.total).toBe(5);
  });

  test("no surfaces at all", () => {
    expect(outboxLagView(null)).toEqual({ total: 0, capped: false, rows: [] });
  });
});

describe("runsView", () => {
  const run = (over: Record<string, unknown> = {}) => ({
    id: "r-1",
    tier: "pipeline",
    role: "worker",
    subject: 1,
    created_at: "2026-08-17 11:00:00",
    ended_at: "2026-08-17 11:20:00",
    outcome: "completed",
    ...over,
  });

  test("newest first, capped at the limit", () => {
    const runs = Array.from({ length: 20 }, (_, i) =>
      run({ id: `r-${i}`, created_at: `2026-08-17 10:${String(i).padStart(2, "0")}:00` }),
    );
    const view = runsView(runs, NOW);
    expect(view).toHaveLength(12);
    expect(view[0].id).toBe("r-19");
    expect(view[11].id).toBe("r-8");
    expect(runsView(runs, NOW, 3).map((r) => r.id)).toEqual(["r-19", "r-18", "r-17"]);
  });

  test("a running row has no outcome and no end, and reads as sky", () => {
    const view = runsView([run({ id: "r-live", outcome: null, ended_at: null })], NOW);
    expect(view[0]).toMatchObject({
      id: "r-live",
      running: true,
      outcome: "running",
      tone: "sky",
      age: "1h",
      stamp: stampLabel("2026-08-17 11:00:00"),
    });
  });

  // The vocabulary is the spine's own (`endRun`): "completed" is the success
  // the pipeline writes, and no run is ever ended as "ok" or "success".
  test("the spine's finished outcomes are leaf, its failures rust", () => {
    const rows = runsView(
      [
        run({ id: "a", outcome: "completed" }),
        run({ id: "b", outcome: "setup-complete" }),
        run({ id: "d", outcome: "infra-failed" }),
        run({ id: "e", outcome: "blocked" }),
        run({ id: "f", outcome: "reaped" }),
      ],
      NOW,
    );
    expect(rows.map((r) => `${r.id}:${r.tone}`)).toEqual([
      "f:rust",
      "e:rust",
      "d:rust",
      "b:leaf",
      "a:leaf",
    ]);
  });

  // An outcome this page has no reading for is shown, not judged: painting an
  // unknown word red would invent a failure the spine never recorded.
  test("an outcome outside the vocabulary is stone, not rust", () => {
    expect(runsView([run({ outcome: "cancelled-by-hand" })], NOW)[0]).toMatchObject({
      outcome: "cancelled-by-hand",
      tone: "stone",
    });
  });

  test("an ended run with no outcome is explicitly unknown, never a lie", () => {
    const view = runsView([run({ outcome: null })], NOW);
    expect(view[0]).toMatchObject({ running: false, outcome: "unknown", tone: "stone" });
  });

  test("a subjectless run and an empty list", () => {
    expect(runsView([run({ subject: null })], NOW)[0].subject).toBeNull();
    expect(runsView(null, NOW)).toEqual([]);
  });

  test("the result states the end in plain words, and a failed run names its reason", () => {
    const rows = runsView(
      [
        run({ id: "a", outcome: "completed" }),
        run({ id: "b", outcome: "infra-failed", end_reason: "network" }),
        run({ id: "c", outcome: "reaped", end_reason: "lease-timeout" }),
        run({ id: "d", outcome: null, ended_at: null }),
        run({ id: "e", outcome: null }),
      ],
      NOW,
    );
    expect(rows.map((r) => `${r.id}:${r.result}`)).toEqual([
      "e:Ended",
      "d:Running",
      "c:The heartbeat stopped",
      "b:Network error",
      "a:Completed",
    ]);
  });

  test("a routine correction uses a neutral tone; a run failure retains its alert tone", () => {
    const rows = runsView(
      [
        run({ id: "a", role: "verifier", verdict_outcome: "correct" }),
        run({ id: "b", role: "worker" }),
        run({ id: "c", role: "verifier", verdict_outcome: "pass" }),
        run({ id: "d", role: "verifier", verdict_outcome: "correct", outcome: "infra-failed", end_reason: "network" }),
      ],
      NOW,
    );
    expect(rows.map((r) => `${r.id}:${r.result}:${r.tone}`)).toEqual([
      "d:Network error:rust",
      "c:Completed:leaf",
      "b:Completed:leaf",
      "a:Requested a correction:stone",
    ]);
  });

  test("a merge step is not a run row, and the limit counts only agent runs", () => {
    const runs = [
      run({ id: "agent-1", binding: "endpoint" }),
      run({ id: "merge-1", binding: "mechanical" }),
      run({ id: "agent-2", binding: "endpoint" }),
      run({ id: "merge-2", binding: "mechanical" }),
    ];
    expect(runsView(runs, NOW, 2).map((r) => r.id)).toEqual(["agent-2", "agent-1"]);
  });

  test("a wiki update row states the result text, the cause, and the tone of its wiki update", () => {
    const cause = "The wiki commit conflicts with the main branch. Culler discarded the wiki commit.";
    const wiki = (id: string, wiki_update: Record<string, unknown>, over: Record<string, unknown> = {}) =>
      run({ id, role: "scribe", subject: null, ...over, wiki_update });
    const rows = runsView(
      [
        wiki("failed", { result: "failed", result_text: "The wiki update failed.", cause_text: cause }),
        wiki("stopped", { result: "failed", result_text: "The wiki update failed.", cause_text: cause }, { outcome: "infra-failed", end_reason: "network" }),
        wiki("merged", { result: "succeeded", result_text: "Culler merged the wiki commit.", cause_text: null }),
        wiki("merging", { result: null, result_text: "Culler merges the wiki commit.", cause_text: null }),
        wiki("running", { result: null, result_text: null, cause_text: null }, { outcome: null, ended_at: null }),
        run({ id: "task", wiki_update: null }),
      ],
      NOW,
    );
    expect(rows.map((r) => [r.id, r.role, r.subject, r.result, r.detail, r.tone, r.wikiUpdate])).toEqual([
      ["task", "worker", 1, "Completed", null, "leaf", false],
      ["running", "scribe", null, "Running", null, "sky", false],
      ["merging", "scribe", null, "Culler merges the wiki commit.", null, "sky", true],
      ["merged", "scribe", null, "Culler merged the wiki commit.", null, "leaf", true],
      ["stopped", "scribe", null, "The wiki update failed.", cause, "rust", true],
      ["failed", "scribe", null, "The wiki update failed.", cause, "rust", true],
    ]);
  });

  test("a run row carries the AgentsView session that the command computed", () => {
    const session = { session: "codex:019a", path: "/sessions/codex/019a?msg=last" };
    const rows = runsView([run({ id: "traced", agentsview: session }), run({ id: "untraced", agentsview: null }), run({ id: "older" })], NOW);
    expect(rows.map((r) => [r.id, r.trace])).toEqual([["older", null], ["untraced", null], ["traced", session]]);
  });

  test("the run row of a wiki update carries its AgentsView session", () => {
    const session = { session: "0d3f", path: "/sessions/0d3f?msg=last" };
    const [row] = runsView(
      [run({ id: "wiki", role: "scribe", subject: null, agentsview: session, wiki_update: { result: "succeeded", result_text: "Culler merged the wiki commit.", cause_text: null } })],
      NOW,
    );
    expect([row.subject, row.wikiUpdate, row.trace]).toEqual([null, true, session]);
  });
});

describe("doctorView", () => {
  test("deployment results exclude connection checks from the composed report", () => {
    const view = doctorView({ ok: false, deployment_ok: true, deployment_complete: true,
      findings: [{check:"spine",status:"ok"},{check:"runtime-auth",status:"fail"}] });
    expect(view.state).toBe("ok");
    expect(view.summary).toBe("1 check, 1 passed, 0 failed");
    expect(view.findings.map(item => item.check)).toEqual(["spine"]);
  });

  test("unchecked: the probe has not been run this session", () => {
    const view = doctorView(null);
    expect(view.state).toBe("unchecked");
    expect(view.findings).toEqual([]);
    expect(view.summary).toBe("not checked this session");
  });

  test("ok: every finding passes", () => {
    const view = doctorView({
      ok: true,
      findings: [
        { check: "spine", status: "ok", detail: "reachable" },
        { check: "container runtime", status: "ok", detail: null },
      ],
    });
    expect(view.state).toBe("ok");
    expect(view.summary).toBe("2 checks, 2 passed, 0 failed");
    expect(view.findings.map((f) => f.tone)).toEqual(["leaf", "leaf"]);
    expect(view.findings[1].detail).toBeNull();
  });

  test("failing: counts the failures and tones them rust", () => {
    const view = doctorView({
      ok: false,
      findings: [
        { check: "spine", status: "ok", detail: "reachable" },
        { check: "routing", status: "fail", detail: "unresolved model reference" },
      ],
    });
    expect(view.state).toBe("failing");
    expect(view.summary).toBe("2 checks, 1 passed, 1 failed");
    expect(view.findings[1]).toEqual({
      check: "routing",
      status: "fail",
      ok: false,
      failing: true,
      label: "FAIL",
      detail: "unresolved model reference",
      tone: "rust",
    });
  });

  // runtime-auth is a permanent `deferred` on every deployment, so this is the
  // shape the operator actually sees. Rendering it as FAIL under `ok: true`
  // taught the Pipeline page to cry wolf on every single load.
  test("deferred is reported, not alarmed", () => {
    const view = doctorView({
      ok: true,
      findings: [
        { check: "spine", status: "ok", detail: "reachable" },
        { check: "runtime-auth", status: "deferred", detail: "runs in the live acceptance" },
      ],
    });
    expect(view.state).toBe("ok");
    expect(view.summary).toBe("2 checks, 1 passed, 0 failed, 1 deferred");
    expect(view.findings[1]).toEqual({
      check: "runtime-auth",
      status: "deferred",
      ok: false,
      failing: false,
      label: "deferred",
      detail: "runs in the live acceptance",
      tone: "stone",
    });
  });

  test("an unrecognised status is treated as a failure, not as passing", () => {
    const view = doctorView({ ok: false, findings: [{ check: "spine", status: "weird" }] });
    expect(view.findings[0].failing).toBe(true);
    expect(view.findings[0].tone).toBe("rust");
    expect(view.summary).toBe("1 check, 0 passed, 1 failed");
  });

  test("a doctor payload with no findings array", () => {
    expect(doctorView({ ok: true }).summary).toBe("0 checks, 0 passed, 0 failed");
  });
});

describe("degradedUnits", () => {
  test("nothing degraded means nothing to open", () => {
    const leases = [leaseHeld()];
    expect(degradedUnits(doctorView({ ok: true, findings: [] }), leases, NOW, SETTINGS)).toEqual([]);
    expect(degradedUnits(doctorView(null), leases, NOW, SETTINGS, [{ id: "alpha", title: "Alpha Site" }])).toEqual([]);
    expect(degradedUnits(null, null, NOW, null)).toEqual([]);
  });

  // The live deployment carries a permanent deferred check, so before this the
  // Pipeline page offered a "go read the logs" link on every load, about a
  // check that has never written a log line because it is not built.
  test("a deferred check earns no log deep-link", () => {
    const doctor = doctorView({
      ok: true,
      findings: [{ check: "runtime-auth", status: "deferred", detail: "runs in the live acceptance" }],
    });
    expect(degradedUnits(doctor, [FREE_LEASE], NOW, SETTINGS)).toEqual([]);
  });

  test("a stale lease points at the resident's stderr and names its Project", () => {
    const leases = [
      leaseHeld({ project: "alpha", last_heartbeat_at: "2026-08-17 11:50:00" }),
      leaseHeld({ project: "beta", run_id: "r-b", subject: null }),
    ];
    const units = degradedUnits(doctorView(null), leases, NOW, SETTINGS, [{ id: "alpha", title: "Alpha Site" }]);
    expect(units).toHaveLength(1);
    expect(units[0].unit).toBe("resident");
    expect(units[0].stream).toBe("stderr");
    expect(units[0].reason).toContain("stale");
    expect(units[0].reason).toContain("Alpha Site");
    expect(units[0].reason).not.toContain("beta");
  });

  test("a failing doctor check adds the resident, and a dashboard one adds the dashboard", () => {
    const doctor = doctorView({
      ok: false,
      findings: [
        { check: "spine", status: "ok", detail: "reachable" },
        { check: "dashboard unit", status: "fail", detail: "not listening" },
      ],
    });
    const units = degradedUnits(doctor, [FREE_LEASE], NOW, SETTINGS);
    expect(units.map((u) => u.unit)).toEqual(["resident", "dashboard"]);
    expect(units.every((u) => u.stream === "stderr")).toBe(true);
    expect(units[1].reason).toContain("dashboard unit");
  });

  test("a failing AgentsView check opens the AgentsView log, and a failing wiki check the dashboard log", () => {
    const doctor = doctorView({
      ok: false,
      findings: [
        { check: "agentsview", status: "fail", detail: "no answer" },
        { check: "wiki", status: "fail", detail: "no answer" },
      ],
    });
    expect(degradedUnits(doctor, [FREE_LEASE], NOW, SETTINGS).map((u) => [u.unit, u.stream])).toEqual([
      ["agentsview", "stderr"],
      ["dashboard", "stderr"],
    ]);
  });

  test("several failing checks never duplicate a tail", () => {
    const leases = [leaseHeld({ last_heartbeat_at: "2026-08-17 11:00:00" })];
    const doctor = doctorView({
      ok: false,
      findings: [
        { check: "routing", status: "fail", detail: "unresolved" },
        { check: "spine", status: "fail", detail: "locked" },
      ],
    });
    const units = degradedUnits(doctor, leases, NOW, SETTINGS);
    expect(units).toHaveLength(1);
    expect(units[0].unit).toBe("resident");
  });
});

describe("topology tables", () => {
  test("one node per agent role, in the frozen order and lanes", () => {
    expect(TOPOLOGY_NODES.map((n) => n.role)).toEqual([
      "strategist",
      "arbiter",
      "worker",
      "verifier",
      "packager",
      "scribe",
      "collie",
    ]);
    const lanes = Object.fromEntries(TOPOLOGY_NODES.map((n) => [n.role, n.lane]));
    expect(lanes["worker"]).toBe("pipeline");
    expect(lanes["collie"]).toBe("outside");
    expect(TOPOLOGY_NODES.every((n) => typeof n.blurb === "string" && n.blurb.length > 0)).toBe(true);
    expect(TOPOLOGY_NODES.every((n) => typeof n.name === "string" && n.name.length > 0)).toBe(true);
    expect(TOPOLOGY_NODES.find((n) => n.role === "strategist")).toMatchObject({
      name: "Strategist", label: "Strategist",
    });
  });

  test("from/to are the statuses each role consumes and produces", () => {
    const io = Object.fromEntries(TOPOLOGY_NODES.map((n) => [n.role, [n.from, n.to]]));
    expect(io["strategist"]).toEqual([null, "proposed"]);
    expect(io["arbiter"]).toEqual(["proposed", "seeded"]);
    expect(io["worker"]).toEqual(["seeded", "worked"]);
    expect(io["verifier"]).toEqual(["worked", "verified"]);
    expect(io["packager"]).toEqual(["verified", "packaged"]);
    expect(io["scribe"]).toEqual([null, null]);
    expect(io["collie"]).toEqual([null, null]);
  });

  test("edges connect the task roles and task correction paths, and no edge names the Scribe", () => {
    const kinds = TOPOLOGY_EDGES.filter((e) => e.kind === "chain").map((e) => `${e.from}>${e.to}`);
    expect(kinds).toEqual([
      "strategist>arbiter",
      "arbiter>worker",
      "worker>verifier",
      "verifier>packager",
    ]);
    expect(TOPOLOGY_EDGES.some((e) => e.from === "scribe" || e.to === "scribe")).toBe(false);
    expect(TOPOLOGY_EDGES.filter((e) => e.kind === "correction")).toEqual([
      { from: "verifier", to: "worker", kind: "correction" },
    ]);
    expect(TOPOLOGY_EDGES.filter((e) => e.kind === "verification")).toEqual([]);
    expect(TOPOLOGY_EDGES.some((e) => e.from === "collie" || e.to === "collie")).toBe(false);
    const roles = new Set(TOPOLOGY_NODES.map((n) => n.role));
    expect(TOPOLOGY_EDGES.every((e) => roles.has(e.from) && roles.has(e.to))).toBe(true);
  });
});

describe("topologyView", () => {
  const ROUTES = [
    { role: "strategist", harness: "claude-sdk", binding: "claude", model: "opus" },
    { role: "arbiter", harness: "codex", binding: "chatgpt", model: "gpt" },
    { role: "worker", harness: "claude-sdk", binding: "endpoint", model: "endpoint-model" },
    { role: "verifier", harness: "codex", binding: "chatgpt", model: "gpt" },
    { role: "packager", harness: "claude-sdk", binding: "endpoint", model: "endpoint-model" },
    { role: "collie", harness: "claude-sdk", binding: "claude", model: "opus" },
  ];

  const byRole = (view: { nodes: any[] }) => Object.fromEntries(view.nodes.map((n) => [n.role, n]));

  test("edges come through verbatim", () => {
    expect(topologyView([FREE_LEASE], [], ROUTES, NOW).edges).toBe(TOPOLOGY_EDGES);
  });

  test("a held lease lights exactly one node, and it is the owner's", () => {
    const view = topologyView([leaseHeld({ owner: "Worker" })], [], ROUTES, NOW);
    const active = view.nodes.filter((n) => n.active);
    expect(active).toHaveLength(1);
    expect(active[0].role).toBe("worker");
    expect(active[0].state).toBe("running");
    expect(active[0].projects).toEqual(["alpha"]);
    expect(active[0].held).toEqual([{ project: "alpha", subject: 1, since: "10m", stale: false, starting: false }]);
    expect(active[0].stale).toBe(false);
    expect(byRole(view)["verifier"].state).toBe("idle");
    expect(byRole(view)["verifier"].held).toEqual([]);
    expect(byRole(view)["verifier"].projects).toEqual([]);
  });

  // Several Projects hold leases at once, so
  // several nodes can run, and one node can run for several Projects.
  test("several held leases light one node each, naming their Projects", () => {
    const leases = [
      leaseHeld({ project: "alpha", run_id: "r-a", owner: "worker" }),
      leaseHeld({ project: "beta", run_id: "r-b", owner: "verifier" }),
      leaseHeld({ project: "gamma", run_id: "r-g", owner: "worker" }),
      { ...FREE_LEASE, project: "delta" },
    ];
    const nodes = byRole(topologyView(leases, [], ROUTES, NOW));
    expect(nodes["worker"]).toMatchObject({ active: true, state: "running", projects: ["alpha", "gamma"] });
    expect(nodes["verifier"]).toMatchObject({ active: true, state: "running", projects: ["beta"] });
    expect(nodes["packager"]).toMatchObject({ active: false, projects: [] });
  });

  test("a Strategist lease activates its proposal node", () => {
    const propose = topologyView(
      [leaseHeld({ owner: "strategist", subject: null })],
      [],
      ROUTES,
      NOW,
    );
    expect(propose.nodes.filter((n) => n.active).map((n) => n.role)).toEqual([
      "strategist",
    ]);
  });

  test("a held lease with a silent heartbeat marks its node stale", () => {
    const silent = leaseHeld({ owner: "worker", last_heartbeat_at: "2026-08-17 11:50:00" });
    const nodes = byRole(topologyView([silent], [], ROUTES, NOW, { heartbeat_interval_s: 60 }));
    expect(nodes["worker"]).toMatchObject({ active: true, stale: true });
    expect(nodes["worker"].held[0].stale).toBe(true);
    const fresh = byRole(topologyView([leaseHeld({ owner: "worker" })], [], ROUTES, NOW, { heartbeat_interval_s: 60 }));
    expect(fresh["worker"].stale).toBe(false);
  });

  test("free leases leave every node inactive", () => {
    const view = topologyView([FREE_LEASE], [], ROUTES, NOW);
    expect(view.nodes.some((n) => n.active)).toBe(false);
    expect(view.nodes.every((n) => n.state === "idle")).toBe(true);
    expect(topologyView(null, null, null, NOW).nodes.some((n) => n.active)).toBe(false);
  });

  test("the role nodes use their configured profiles", () => {
    const nodes = byRole(topologyView([FREE_LEASE], [], ROUTES, NOW));
    expect(nodes["worker"]).toMatchObject({ harness: "claude-sdk", binding: "endpoint", model: "endpoint-model" });
    expect(nodes["strategist"]).toMatchObject({ harness: "claude-sdk", binding: "claude" });
    expect(nodes["collie"].harness).toBe("claude-sdk");
  });

  test("a routing table with no row for a role reports unknown, never a guess", () => {
    const nodes = byRole(topologyView([FREE_LEASE], [], [ROUTES[2]], NOW));
    expect(nodes["worker"].harness).toBe("claude-sdk");
    expect(nodes["verifier"]).toMatchObject({ harness: null, binding: null, model: null });
    const none = byRole(topologyView([FREE_LEASE], [], undefined, NOW));
    expect(none["arbiter"].model).toBeNull();
  });

  test("the newest run per role drives recent vs idle, reading oldest-first input", () => {
    const runs = [
      { id: "r-1", role: "worker", created_at: "2026-08-17 05:00:00", ended_at: "2026-08-17 05:10:00", outcome: "completed" },
      { id: "r-2", role: "worker", created_at: "2026-08-17 11:40:00", ended_at: "2026-08-17 11:50:00", outcome: "completed" },
      { id: "r-3", role: "verifier", created_at: "2026-08-17 06:00:00", ended_at: "2026-08-17 06:10:00", outcome: "infra-failed", end_reason: "network" },
    ];
    const nodes = byRole(topologyView([FREE_LEASE], runs, ROUTES, NOW));
    expect(nodes["worker"]).toMatchObject({
      state: "recent",
      lastRun: "r-2",
      lastRunAge: "20m",
      lastRunLabel: "20m ago",
      lastOutcome: "Completed",
      runCount: 2,
    });
    expect(nodes["verifier"]).toMatchObject({
      state: "idle",
      lastRun: "r-3",
      lastOutcome: "Network error",
      runCount: 1,
    });
    expect(nodes["packager"]).toMatchObject({ lastRun: null, lastRunAge: null, lastRunLabel: null, lastOutcome: null, runCount: 0 });
  });

  test("an in-flight run reads Running; an ended run with no outcome reads Ended", () => {
    const runs = [
      { id: "r-a", role: "worker", created_at: "2026-08-17 11:55:00", ended_at: null, outcome: null },
      { id: "r-b", role: "arbiter", created_at: "2026-08-17 11:55:00", ended_at: "2026-08-17 11:56:00", outcome: null },
    ];
    const nodes = byRole(topologyView([leaseHeld({ owner: "worker" })], runs, ROUTES, NOW));
    expect(nodes["worker"]).toMatchObject({ lastOutcome: "Running", state: "running", active: true });
    expect(nodes["arbiter"].lastOutcome).toBe("Ended");
  });

  test("a merge step lights no node and is not the last run of a role", () => {
    const runs = [
      { id: "r-agent", role: "worker", binding: "endpoint", created_at: "2026-08-17 11:00:00", ended_at: "2026-08-17 11:10:00", outcome: "completed" },
      { id: "r-merge", role: "worker", binding: "mechanical", created_at: "2026-08-17 11:55:00", ended_at: null, outcome: null },
    ];
    const nodes = byRole(topologyView([leaseHeld({ run_id: "r-merge", binding: "mechanical" })], runs, ROUTES, NOW));
    expect(nodes["worker"]).toMatchObject({ active: false, lastRun: "r-agent", lastOutcome: "Completed", runCount: 1 });
  });

  test("the frozen tables are never mutated by a view", () => {
    topologyView([leaseHeld()], [], ROUTES, NOW);
    expect(TOPOLOGY_NODES[2]).not.toHaveProperty("active");
  });
});

describe("healthView", () => {
  test("current availability remains separate from historical log errors", () => {
    const view = healthView({ ok:false, availability:{available:true,can_commit:true},
      findings:[{check:"log-resident",status:"fail",detail:"An old error"}] });
    expect(view.state).toBe("ok");
    expect(view.findings[0].failing).toBe(true);
  });

  const HEALTH = {
    ok: false,
    tail: 200,
    findings: [
      { check: "resident-tick", status: "ok", detail: "tick 41 completed 12s ago" },
      { check: "log-resident", status: "fail", detail: "1 error(s), 3 warning(s)" },
      { check: "log-mc", status: "deferred", detail: "no structured log yet" },
    ],
    attention: [
      "2026-08-18T20:00:00.000Z WARN  resident spawn.refused — provider quota exhausted",
      "2026-08-18T20:00:01.000Z ERROR resident spawn.create_failed — docker create refused",
    ],
  };

  test("an unread plane is unchecked, not failing", () => {
    const v = healthView(null);
    expect(v.state).toBe("unchecked");
    expect(v.findings).toEqual([]);
    expect(v.attention).toEqual([]);
  });

  test("the verb's three-valued status survives, exactly as the doctor's does", () => {
    const v = healthView(HEALTH);
    expect(v.state).toBe("failing");
    expect(v.summary).toBe("3 checks, 1 passed, 1 failed, 1 deferred");
    expect(v.findings.map((f) => [f.check, f.label, f.failing])).toEqual([
      ["resident-tick", "ok", false],
      ["log-resident", "FAIL", true],
      // Deferred is a state the operator already knows about, not an alarm.
      ["log-mc", "deferred", false],
    ]);
  });

  test("attention lines are carried through untouched", () => {
    // The verb rendered them. The browser prints them and does not parse them —
    // the moment it wants to understand a line it has become the Trace Viewer.
    expect(healthView(HEALTH).attention).toEqual(HEALTH.attention);
  });

  test("a non-string in attention is dropped rather than rendered as one", () => {
    const v = healthView({ ...HEALTH, attention: ["ok", null, 42, { a: 1 }] });
    expect(v.attention).toEqual(["ok"]);
  });

  test("a passing plane says so", () => {
    const v = healthView({ ok: true, findings: [{ check: "run-logs", status: "ok", detail: "4 runs" }], attention: [] });
    expect(v.state).toBe("ok");
    expect(v.summary).toBe("1 check, 1 passed, 0 failed");
  });
});


test("a final run does not mark a Pipeline role as active", () => {
  const view = topologyView([{ run_id: "finished", owner: "worker", subject: 1, run_outcome: "infra-failed" }], [], [], NOW);
  expect(view.nodes.some((node) => node.active)).toBe(false);
});
