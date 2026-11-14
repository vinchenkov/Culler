// Unit tests for flowmap.js: the words the Pipeline page's flow map shows,
// computed from the topology view and the task list.

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { NARROW_BELOW_PX, bindingOptions, flowPopoverNode, flowRunNode, flowWikiNode, flowmapNode, flowmapView, layoutFor } from "./flowmap.js";
import { TOPOLOGY_NODES, topologyView } from "./topology.js";

const NOW = Date.parse("2026-08-17T12:00:00Z");

const leaseHeld = (over: Record<string, unknown> = {}) => ({
  project: "alpha",
  status: "held",
  run_id: "r-9",
  owner: "worker",
  subject: 212,
  acquired_at: "2026-08-17 11:56:00",
  last_heartbeat_at: "2026-08-17 11:59:50",
  hard_deadline_at: "2026-08-17 13:00:00",
  ...over,
});

const FREE = { project: "alpha", status: "free", run_id: null, owner: null, subject: null, acquired_at: null, last_heartbeat_at: null, hard_deadline_at: null };

const ROUTES = [
  { role: "worker", harness: "claude-sdk", binding: "claude", model: "opus" },
  { role: "arbiter", harness: "codex", binding: "chatgpt", model: "gpt" },
];

const RUNS = [
  { id: "r-1", role: "arbiter", created_at: "2026-08-17 11:48:00", ended_at: "2026-08-17 11:50:00", outcome: "completed" },
  { id: "r-2", role: "collie", created_at: "2026-08-17 11:54:00", ended_at: "2026-08-17 11:55:00", outcome: "completed" },
];

const task = (over: Record<string, unknown> = {}) => ({ id: 1, status: "seeded", blocked: 0, blocked_reason: null, project: "alpha", ...over });

const projects = [{ id: "alpha", title: "Alpha Site" }, { id: "beta", title: "Beta Site" }];

const byRole = (view: { nodes: any[] }) => Object.fromEntries(view.nodes.map((n) => [n.role, n]));

describe("flowmapView nodes", () => {
  test("the Strategist card and role detail use the same role name", () => {
    const view = flowmapView(topologyView([], [], ROUTES, NOW), [], projects);
    const strategist = byRole(view).strategist;
    expect(strategist.name).toBe("Strategist");
    expect(strategist.detail.title).toBe("Strategist");
  });

  test("a run claim waits for process start before the graph says Running now", () => {
    const claim = leaseHeld({ agent_started_at: null });
    const nodes = (leases: any[]) => byRole(flowmapView(topologyView(leases, [], ROUTES, NOW), [], projects));
    expect(nodes([claim]).worker.stateLabel).toBe("Agent launch pending");
    expect(nodes([{ ...claim, agent_started_at: "2026-08-17 11:59:00" }]).worker.stateLabel).toBe("Running now");
    const mixed = nodes([claim, leaseHeld({ project: "beta", run_id: "r-b", subject: 7 })]);
    expect(mixed.worker.detail.stateline).toContain("Agent launch pending");
  });

  test("a running node says where it runs and for how long; the disclosure names the task", () => {
    const topology = topologyView([leaseHeld()], RUNS, ROUTES, NOW, { heartbeat_interval_s: 60 });
    const worker = byRole(flowmapView(topology, [], projects))["worker"];
    expect(worker).toMatchObject({ name: "Worker", state: "running", stateLabel: "Running now", line: "on Alpha Site · 4m", lineTail: " · 4m" });
    expect(worker.detail).toMatchObject({
      title: "Worker",
      stateline: "Running now · on Alpha Site · 4m · task #212",
      harness: "claude-sdk",
      model: "opus",
    });
  });

  test("one node running for two Projects names both", () => {
    const leases = [leaseHeld(), leaseHeld({ project: "beta", run_id: "r-b", subject: 7 })];
    const worker = byRole(flowmapView(topologyView(leases, [], ROUTES, NOW), [], projects))["worker"];
    expect(worker.line).toBe("on Alpha Site, Beta Site · 4m");
  });

  test("a silent heartbeat reads as stale, not as running", () => {
    const silent = leaseHeld({ last_heartbeat_at: "2026-08-17 11:40:00" });
    const worker = byRole(flowmapView(topologyView([silent], [], ROUTES, NOW, { heartbeat_interval_s: 60 }), []))["worker"];
    expect(worker).toMatchObject({ stale: true, stateLabel: "Heartbeat stopped" });
  });

  test("an idle node says when it last ran, or that it never ran", () => {
    const nodes = byRole(flowmapView(topologyView([FREE], RUNS, ROUTES, NOW), []));
    expect(nodes["arbiter"]).toMatchObject({ stateLabel: "Idle", line: "last run 12m ago", lineTail: "", state: "recent" });
    expect(nodes["arbiter"].detail).toMatchObject({ stateline: "Idle · last run 12m ago · Completed" });
    expect(nodes["packager"]).toMatchObject({ stateLabel: "Idle", line: "no runs yet", state: "idle" });
    expect(nodes["packager"].detail.harness).toBeNull();
  });

  test("Collie carries no state word: it holds no lease", () => {
    const collie = byRole(flowmapView(topologyView([FREE], RUNS, ROUTES, NOW), []))["collie"];
    expect(collie).toMatchObject({ lane: "outside", stateLabel: null, line: "last run 6m ago" });
  });

});

describe("flowmapView edges", () => {
  test("the Scribe receives its normal route, and the verified edge counts each verified task", () => {
    const topology = topologyView([FREE], [], [
      { role: "scribe", harness: "codex", binding: "scribe-profile", model: "model-a" },
    ], NOW);
    const view = flowmapView(topology, [
      task({ id: 1, status: "verified", delivery: { report_path: null } }),
      task({ id: 2, status: "verified" }),
      task({ id: 3, status: "verified", project: "beta" }),
    ]);
    expect(byRole(view).scribe).toMatchObject({ agent: "scribe", active: false, binding: "scribe-profile", stateLabel: "Idle" });
    expect(view.edges.find((edge) => edge.status === "verified")).toMatchObject({ from: "verifier", count: 3 });
    expect(view.edges.some((edge) => edge.from === "scribe")).toBe(false);
  });

  test("each chain edge carries its status and the count across Projects", () => {
    const tasks = [
      task({ id: 1, status: "proposed" }),
      task({ id: 2, status: "proposed", project: "beta" }),
      task({ id: 3, status: "seeded" }),
      task({ id: 4, status: "packaged" }),
    ];
    const view = flowmapView(topologyView([FREE], [], ROUTES, NOW), tasks, projects);
    expect(view.edges.map((e) => [e.status, e.count])).toEqual([
      ["proposed", 2],
      ["seeded", 1],
      ["worked", 0],
      ["verified", 0],
      ["packaged", 1],
    ]);
  });

  test("an edge lights when the role it leaves is running", () => {
    const view = flowmapView(topologyView([leaseHeld({ owner: "verifier" })], [], ROUTES, NOW), []);
    expect(view.edges.filter((e) => e.live).map((e) => e.status)).toEqual(["verified"]);
    expect(flowmapView(topologyView([FREE], [], ROUTES, NOW), []).edges.some((e) => e.live)).toBe(false);
  });

  test("an edge stays unlit while its role has no run with a current heartbeat", () => {
    const settings = { heartbeat_interval_s: 60 };
    const silent = leaseHeld({ owner: "verifier", last_heartbeat_at: "2026-08-17 11:40:00" });
    const lit = (leases: any[]) => flowmapView(topologyView(leases, [], ROUTES, NOW, settings), []).edges.filter((e) => e.live).map((e) => e.status);
    expect(lit([silent])).toEqual([]);
    expect(lit([leaseHeld({ owner: "verifier", agent_started_at: null })])).toEqual([]);
    expect(lit([silent, leaseHeld({ owner: "verifier", project: "beta", run_id: "r-b" })])).toEqual(["verified"]);
  });
});

/** The small part of the DOM that dom.js uses. */
class FakeElement {
  readonly nodeType = 1;
  readonly childNodes: FakeElement[] = [];
  readonly dataset: Record<string, string> = {};
  readonly attributes: Array<{ name: string; value: string }> = [];
  className = "";
  textContent = "";
  constructor(readonly nodeName: string) {}
  setAttribute(name: string, value: string) { this.attributes.push({ name, value }); }
  readonly listeners: Record<string, (event?: unknown) => void> = {};
  addEventListener(name: string, fn: (event?: unknown) => void) { this.listeners[name] = fn; }
  append(child: FakeElement) { this.childNodes.push(child); }
  all(): FakeElement[] { return [this, ...this.childNodes.flatMap((child) => typeof child === "object" ? child.all() : [])]; }
}

describe("flowPopoverNode", () => {
  const global = globalThis as { document?: unknown };
  beforeAll(() => { global.document = { createElement: (tag: string) => new FakeElement(tag.toUpperCase()) }; });
  afterAll(() => { delete global.document; });

  const detail = (role: string) => {
    const node = byRole(flowmapView(topologyView([FREE], RUNS, ROUTES, NOW), [], projects))[role];
    const actions = { onClose() {}, onProfile() {}, onInstructions() {}, instructions: { text: "No custom instructions" } };
    return flowPopoverNode(node, actions) as unknown as FakeElement;
  };
  const parts = (node: FakeElement) => node.childNodes.map((child) => child.className);
  const rowHooks = (node: FakeElement) =>
    node.all().filter((child) => child.className === "flow-popover-row").map((child) => child.dataset.testid);

  test("a role card selects the existing popup role and preserves its action hooks", () => {
    const view = flowmapView(topologyView([FREE], RUNS, ROUTES, NOW), [], projects);
    let selected = "";
    const map = flowmapNode(view, "wide", (role: string) => { selected = role; }, "worker") as unknown as FakeElement;
    const card = map.all().find(node => node.dataset.testid === "topology-node-worker")!;
    expect(card.nodeName).toBe("BUTTON");
    expect(card.attributes).toContainEqual({ name: "aria-expanded", value: "true" });
    card.listeners.click!();
    expect(selected).toBe("worker");
    expect(rowHooks(detail(selected))).toEqual(["topology-change-profile", "topology-custom-instructions"]);
    expect(map.all().some(node => node.dataset.testid === "topology-edge-correction")).toBe(false);
    expect(map.all().some(node => node.dataset.testid === "topology-node-scribe")).toBe(false);
  });

  test("the Project wiki section selects the Scribe and states the wiki state", () => {
    const view = flowmapView(topologyView([FREE], RUNS, ROUTES, NOW), [], projects);
    const text = (wikiState: unknown) => (flowWikiNode({ ...view, wikiState }, () => {}) as unknown as FakeElement)
      .all().find(node => node.dataset.testid === "wiki-state")!.textContent;
    expect(text({ state_text: "The Project wiki is current." })).toBe("The Project wiki is current.");
    expect(text(null)).toBe("The dashboard cannot read the wiki state.");
    expect(text(undefined)).toBe("The Scribe updates the Project wiki on a schedule.");
    let selected = "";
    const section = flowWikiNode(view, (role: string) => { selected = role; }, "scribe") as unknown as FakeElement;
    const card = section.all().find(node => node.dataset.testid === "topology-node-scribe")!;
    expect(card.nodeName).toBe("BUTTON");
    expect(card.attributes).toContainEqual({ name: "aria-expanded", value: "true" });
    expect(card.childNodes.map(node => node.textContent)).toEqual(["Idle", "Details →", "no runs yet"]);
    card.listeners.click!();
    expect(selected).toBe("scribe");
  });

  test("an open correction preserves the quiet task path and its task count", () => {
    const view = flowmapView(topologyView([FREE], RUNS, ROUTES, NOW), [task({
      id: 212, status: "worked", delivery: { verifier_correction_open: 1, verifier_report: { problem: "The export omits the date." } },
    })], projects);
    const map = flowmapNode(view, "narrow", () => {}) as unknown as FakeElement;
    expect(map.all().some(node => node.dataset.testid === "pipeline-correction")).toBe(false);
    expect(map.all().some(node => node.textContent.includes("correction") || node.textContent.includes("export"))).toBe(false);
    const count = map.all().find(node => node.dataset.testid === "topology-edge-worked")!;
    expect(count.all().find(node => node.nodeName === "B")?.textContent).toBe("1");
  });

  test("the current banner links the held run trace only", () => {
    const lease = leaseHeld();
    const runs = [{ id: "r-9", role: "worker", agentsview: { state: "available" } }];
    const view = flowmapView(topologyView([lease], runs, ROUTES, NOW), [], projects, { leases: [lease], runs, nowMs: NOW });
    const linked: string[] = [];
    const banner = flowRunNode(view, (run: { id: string }) => { linked.push(run.id); return null; }) as unknown as FakeElement;
    expect(linked).toEqual(["r-9"]);
    expect(banner.all().find(node => node.dataset.testid === "pipeline-current-run")).toBeDefined();
    expect(banner.all().some(node => node.textContent === "No active task run")).toBe(false);
  });

  test("the role detail of each role, the Scribe included, holds the same parts", () => {
    for (const role of TOPOLOGY_NODES.map((entry) => entry.role)) {
      const node = detail(role);
      expect(node.dataset).toEqual({ testid: "topology-side", role });
      expect(parts(node)).toEqual(["top", "blurb", "flow-popover-rows"]);
      expect(rowHooks(node)).toEqual(["topology-change-profile", "topology-custom-instructions"]);
    }
  });
});

describe("layoutFor", () => {
  test("the rail folds below the narrow threshold", () => {
    expect(layoutFor(NARROW_BELOW_PX)).toBe("wide");
    expect(layoutFor(NARROW_BELOW_PX - 1)).toBe("narrow");
    expect(layoutFor(1400)).toBe("wide");
  });
});

describe("flowmapView routing facts", () => {
  const ROUTED = [
    ...ROUTES,
    { role: "strategist", harness: "codex", binding: "chatgpt", model: "gpt" },
    { role: "collie", harness: "claude-sdk", binding: "claude", model: "opus" },
  ];

  test("every node carries its agent and current binding for the picker", () => {
    const nodes = byRole(flowmapView(topologyView([FREE], RUNS, ROUTED, NOW, {}), []));
    expect(nodes["worker"].agent).toBe("worker");
    expect(nodes["worker"].binding).toBe("claude");
    expect(nodes["arbiter"].binding).toBe("chatgpt");
    expect(nodes["collie"].agent).toBe("collie");
    expect(nodes["collie"].binding).toBe("claude");
    expect(nodes["packager"].binding).toBeNull();
  });

});

describe("bindingOptions", () => {
  const BINDINGS = [
    { id: "fake", harness: "fake", model: "" },
    { id: "claude", harness: "claude-sdk", model: "claude-opus-4-8" },
    { id: "chatgpt", harness: "codex", model: "codex-model" },
  ];

  test("sorts by id, labels each entry with the harness and the model, and marks only the current one", () => {
    const options = bindingOptions(BINDINGS, "claude");
    expect(options.map((o) => o.id)).toEqual(["chatgpt", "claude", "fake"]);
    expect(options.map((o) => o.label)).toEqual([
      "chatgpt · codex · codex-model",
      "claude · claude-sdk · claude-opus-4-8",
      "fake · fake · —",
    ]);
    expect(options.map((o) => o.current)).toEqual([false, true, false]);
  });

  test("a role without a route marks no entry", () => {
    expect(bindingOptions(BINDINGS, null).some((o) => o.current)).toBe(false);
  });

  test("a missing or empty list gives no entries", () => {
    expect(bindingOptions(null, "claude")).toEqual([]);
    expect(bindingOptions(undefined, null)).toEqual([]);
    expect(bindingOptions([], null)).toEqual([]);
  });
});

test("agent profile choices show the display name and preserve the identifier", () => {
  const [option] = bindingOptions([{ id: "profile-123", name: "Careful", harness: "codex", model: "model-a" }], "profile-123");
  expect(option.id).toBe("profile-123");
  expect(option.label).toBe("Careful · codex · model-a");
  expect(option.current).toBe(true);
});

describe("role map records", () => {
  const view = (tasks: any[], leases: any[] = [], runs: any[] = []) =>
    flowmapView(topologyView(leases, runs, ROUTES, NOW), tasks, projects, { leases, runs, nowMs: NOW });

  test("the banner uses the held run and its task, heartbeat, and trace", () => {
    const lease = leaseHeld({ agent_started_at: "2026-08-17 11:57:00" });
    const trace = { native_session_ref: "session-a" };
    const runs = [{ id: "other", agentsview: { native_session_ref: "other" } }, { id: "r-9", agentsview: trace }];
    const result = view([task({ id: 212, title: "Preserve the pause state" })], [lease], runs);
    expect(result.currentRuns).toEqual([{
      role: "Worker", state: "Running now", stale: false, starting: false,
      task: 212, title: "Preserve the pause state", project: "Alpha Site", age: "3m", heartbeat: "10s ago",
      run: { id: "r-9", trace },
    }]);
  });

  test("a completed run with a retained lease produces no current banner", () => {
    const lease = leaseHeld({ run_outcome: "completed", run_ended_at: "2026-08-17 11:59:00" });
    expect(view([], [lease]).currentRuns).toEqual([]);
    expect(view([], [FREE]).currentRuns).toEqual([]);
  });

  test("a launch wait and a stopped heartbeat keep their distinct states", () => {
    const pending = view([], [leaseHeld({ agent_started_at: null, last_heartbeat_at: null })]).currentRuns[0];
    expect(pending).toMatchObject({ state: "Agent launch pending", starting: true, heartbeat: "No heartbeat recorded" });
    const stale = view([], [leaseHeld({ last_heartbeat_at: "2026-08-17 11:00:00" })]).currentRuns[0];
    expect(stale).toMatchObject({ state: "Heartbeat stopped", stale: true, heartbeat: "1h ago" });
  });

  test("the Scribe stays separate when its wiki update is active", () => {
    const wikiState = { project: "alpha", active: { step: "run", run_id: "scribe-1", started_at: "2026-08-17 11:56:00", agent_started_at: "2026-08-17 11:56:20" } };
    const result = flowmapView(topologyView([], [], ROUTES, NOW, {}, wikiState), [], projects, { wikiState });
    expect(result.nodes.find(node => node.role === "scribe").active).toBe(true);
    expect(result.currentRuns).toEqual([]);
    expect(result.wikiState).toBe(wikiState);
  });

  test("an archived task adds zero to the task count", () => {
    const result = view([task({ archived: 1, delivery: { verifier_correction_open: 1 } })]);
    expect(result.edges.every(edge => edge.count === 0)).toBe(true);
  });
});
