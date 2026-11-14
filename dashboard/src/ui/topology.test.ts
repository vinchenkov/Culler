// Unit tests for topology.js: the Scribe node takes its run from the wiki
// state row of one Project. With no row, it takes its run from the run rows.

import { describe, expect, test } from "bun:test";
import { flowmapView } from "./flowmap.js";
import { topologyView } from "./topology.js";

const NOW = Date.parse("2026-08-17T12:00:00Z");
const SETTINGS = { heartbeat_interval_s: 60 };
const PROJECTS = [{ id: "alpha", title: "Alpha Site" }];
const CAUSE = "The wiki commit conflicts with the culler branch. Culler discarded the wiki commit.";

// The `active` object of one `mc wiki-state list` row.
const active = (over: Record<string, unknown> = {}) => ({
  run_id: "9a0b1c2d3e4f5a6b",
  started_at: "2026-08-17 11:56:00",
  agent_started_at: "2026-08-17 11:56:20",
  last_heartbeat_at: "2026-08-17 11:59:50",
  run_ended_at: null,
  step: "run",
  step_text: "The Scribe updates the Project wiki.",
  repair_text: null,
  ...over,
});

const updateActive = (over: Record<string, unknown> = {}) => ({
  project: "alpha",
  state: "update-active",
  state_text: "A wiki update is active.",
  accepted_commit: "a".repeat(40),
  next_start_at: null,
  next_start_text: null,
  active: active(over),
  last: null,
});

const CURRENT = { ...updateActive(), state: "current", state_text: "The Project wiki is current.", active: null };

// One `mc run list` row of the Scribe run of an active wiki update.
const scribeRunRow = (over: Record<string, unknown> = {}) => ({
  id: "9a0b1c2d3e4f5a6b", role: "scribe", project: "alpha", subject: null, binding: "fake/fake",
  created_at: "2026-08-17 11:56:00", agent_started_at: "2026-08-17 11:56:20", ended_at: null, outcome: null,
  wiki_update: { result: null, result_text: null, cause_text: null },
  ...over,
});

/** The Scribe node of the topology, and the words that the graph shows for it. */
function scribe(wikiState: unknown, leases: unknown[] = [], runs: unknown[] = []) {
  const topology = topologyView(leases, runs, [], NOW, SETTINGS, wikiState);
  const role = (nodes: any[]) => nodes.find((node) => node.role === "scribe");
  return { node: role(topology.nodes), words: role(flowmapView(topology, [], PROJECTS).nodes), topology };
}

describe("the Scribe node", () => {
  test("a Scribe run with a current heartbeat reads Running now, on its Project, with no task", () => {
    const { node, words } = scribe(updateActive());
    expect(node).toMatchObject({ active: true, state: "running", starting: false, stale: false, projects: ["alpha"] });
    expect(node.held).toEqual([{ project: "alpha", subject: null, since: "4m", stale: false, starting: false }]);
    expect(words).toMatchObject({ stateLabel: "Running now", line: "on Alpha Site · 4m" });
    expect(words.detail.stateline).toBe("Running now · on Alpha Site · 4m");
  });

  test("a Scribe run with no started agent reads Agent launch pending", () => {
    const { node, words } = scribe(updateActive({ agent_started_at: null, last_heartbeat_at: null }));
    expect(node).toMatchObject({ active: true, starting: true, stale: false });
    expect(words.stateLabel).toBe("Agent launch pending");
  });

  test("a Scribe run with three missed beats reads Heartbeat stopped", () => {
    const { node, words } = scribe(updateActive({ last_heartbeat_at: "2026-08-17 11:56:30" }));
    expect(node).toMatchObject({ active: true, starting: false, stale: true });
    expect(words.stateLabel).toBe("Heartbeat stopped");
  });

  test("the node reads Idle while the wiki state row holds no Scribe run", () => {
    const rows = [
      null,
      CURRENT,
      updateActive({ step: "merge", step_text: "Culler merges the wiki commit.", run_ended_at: "2026-08-17 11:58:00" }),
      updateActive({ step: "receipt", step_text: "Culler checks the result of an interrupted merge of the wiki commit. Each other merge step of the Project waits for this result.", run_ended_at: "2026-08-17 11:58:00" }),
      updateActive({ step: "paused", step_text: "The Project is paused. Culler merges the wiki commit after the Project is active.", run_ended_at: "2026-08-17 11:58:00" }),
    ];
    for (const row of rows) {
      const { node, words } = scribe(row);
      expect(node).toMatchObject({ active: false, state: "idle", held: [], projects: [] });
      expect(words).toMatchObject({ stateLabel: "Idle", line: "no runs yet" });
    }
  });

  test("with no wiki state row, the node takes its run from the newest Scribe run row", () => {
    // The row is null after a failed read. The read holds no row of an archived Project.
    for (const row of [null, undefined]) {
      const { node, words } = scribe(row, [], [scribeRunRow()]);
      expect(node).toMatchObject({ active: true, state: "running", starting: false, stale: false, projects: ["alpha"] });
      expect(node.held).toEqual([{ project: "alpha", subject: null, since: "4m", stale: false, starting: false }]);
      expect(words.detail.stateline).toBe("Running now · on Alpha Site · 4m");
      expect(scribe(row, [], [scribeRunRow({ agent_started_at: null })]).words.stateLabel).toBe("Agent launch pending");
      const ended = scribe(row, [], [scribeRunRow({ ended_at: "2026-08-17 11:58:00", outcome: "completed" })]);
      expect(ended.node.active).toBe(false);
      expect(ended.words.stateLabel).toBe("Idle");
    }
  });

  test("a wiki state row with no Scribe run reads Idle beside an active Scribe run row", () => {
    const { node, words } = scribe(CURRENT, [], [scribeRunRow()]);
    expect(node.active).toBe(false);
    expect(words.stateLabel).toBe("Idle");
  });

  test("a Scribe run lights no edge and no other node", () => {
    const { topology } = scribe(updateActive());
    expect(topology.nodes.filter((node: any) => node.active).map((node: any) => node.role)).toEqual(["scribe"]);
    expect(flowmapView(topology, [], PROJECTS).edges.some((edge: any) => edge.live)).toBe(false);
  });

  test("the node states the result text of a failed wiki update as its last run", () => {
    const runs = [
      { id: "9a0b1c2d3e4f5a6b", role: "scribe", subject: null, created_at: "2026-08-17 11:40:00", ended_at: "2026-08-17 11:44:00", outcome: "completed",
        wiki_update: { result: "failed", result_text: "The wiki update failed.", cause_text: CAUSE } },
    ];
    const { node, words } = scribe({ ...CURRENT, state: "behind", state_text: "The Project wiki is behind the accepted commit." }, [], runs);
    expect(node).toMatchObject({ active: false, state: "recent", lastRun: "9a0b1c2d3e4f5a6b", lastOutcome: "The wiki update failed.", runCount: 1 });
    expect(words).toMatchObject({ stateLabel: "Idle", line: "last run 20m ago" });
    expect(words.detail.stateline).toBe("Idle · last run 20m ago · The wiki update failed.");
  });
});
