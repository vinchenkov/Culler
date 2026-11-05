import { describe, expect, test } from "bun:test";
import { configGroups, configInputValue, configStoredValue, knobView, routingRows, projectRows } from "./configs.js";

const FAMILIES = [
  { family: "budgets", knobs: [{ key: "budgets.review_wip_cap", value: 3, layer: "operator", type: "integer", min: 1, max: 1000000 }] },
  { family: "timing", knobs: [{ key: "timing.backup_retain", value: 48, layer: "default", type: "integer" }] },
  { family: "runtimes", knobs: [{ key: "runtimes.forbidden_env", value: ["PRIVATE_TOKEN"], type: "lines" }, { key: "runtimes.bindings", value: [] }] },
  { family: "container", knobs: [{ key: "container.runtime", value: "docker" }] },
];

describe("configuration editors", () => {
  test("the editor retains typed values and server constraints", () => {
    const integer = knobView(FAMILIES[0].knobs[0]);
    expect(integer.rawValue).toBe(3);
    expect(integer.min).toBe(1);
    expect(integer.max).toBe(1000000);
    expect(integer.operatorSet).toBe(true);
    const patterns = [{ kind: "component", pattern: "private" }];
    expect(knobView({ key: "container.additional_blocked_patterns", value: patterns, type: "patterns" }).rawValue).toEqual(patterns);
  });

  test("the wiki interval editor shows minutes and preserves stored values", () => {
    const knob = knobView({ key: "timing.wiki_update_interval_s", label: "Wiki update interval (seconds)", value: 3600, type: "integer", min: 1, max: 31536000 });
    expect(knob.label).toBe("Wiki update interval (minutes)");
    expect(knob.showKey).toBe(false);
    expect(knob.inputMax).toBe(525600);
    expect(configInputValue(knob, knob.rawValue)).toBe(60);
    expect(configStoredValue(knob, 120)).toBe(7200);
    expect(configStoredValue(knob, configInputValue(knob, 5))).toBe(5);
  });

  test("only supported editors appear in groups", () => {
    expect(configGroups(FAMILIES).map(group => group.family)).toEqual(["budgets", "timing", "runtimes"]);
    expect(configGroups([{ family: "secret", knobs: [{ key: "secret.token", type: "lines", secret: true }] }])).toEqual([]);
  });

  test("the filter matches keys and preserves typed drafts", () => {
    const groups = configGroups(FAMILIES, "FORBIDDEN");
    expect(groups).toHaveLength(1);
    expect(groups[0].knobs[0].rawValue).toEqual(["PRIVATE_TOKEN"]);
    expect(configGroups(FAMILIES, "PRIVATE_TOKEN")).toEqual([]);
  });

  test("empty and unsupported responses expose no editor", () => {
    expect(configGroups(undefined)).toEqual([]);
    expect(configGroups([{ family: "future", knobs: [{ key: "future.setting", type: "unsupported" }] }])).toEqual([]);
  });
});

describe("routingRows", () => {
  const ROUTES = [
    { role: "homie", harness: "claude-sdk", binding: "resident", model: "opus" },
    { role: "worker", harness: "codex", binding: "cli", model: "gpt" },
    { role: "auditor", harness: "codex", binding: "cli", model: "gpt" },
    { role: "strategist", harness: "claude-sdk", binding: "cli", model: "opus" },
    { role: "arbiter", harness: "claude-sdk", binding: "cli", model: "opus" },
    { role: "verifier", harness: "codex", binding: "cli", model: "gpt" },
    { role: "scribe", harness: "codex", binding: "cli", model: "gpt" },
    { role: "packager", harness: "claude-sdk", binding: "cli", model: "opus" },
  ];

  test("the task roles in pipeline order, then the Scribe, then Homie; an unknown role follows in input order", () => {
    expect(routingRows(ROUTES).map((r) => r.role)).toEqual([
      "strategist",
      "arbiter",
      "worker",
      "verifier",
      "packager",
      "scribe",
      "homie",
      "auditor",
    ]);
  });

  test("labels are capitalised and the homie row is marked, not hidden", () => {
    const rows = routingRows(ROUTES);
    expect(rows.map((r) => r.label)).toEqual([
      "Strategist",
      "Arbiter",
      "Worker",
      "Verifier",
      "Packager",
      "Scribe",
      "Homie",
      "Auditor",
    ]);
    expect(rows.filter((r) => r.homie).map((r) => r.role)).toEqual(["homie"]);
  });

  test("family carries the harness; a missing model falls back to empty", () => {
    const [row] = routingRows([{ role: "worker", harness: "claude-sdk", binding: "cli" }]);
    expect(row).toEqual({
      role: "worker",
      label: "Worker",
      harness: "claude-sdk",
      binding: "cli",
      profileName: "cli",
      model: "",
      family: "claude-sdk",
      homie: false,
    });
  });

  test("empty input", () => {
    expect(routingRows([])).toEqual([]);
    expect(routingRows(undefined)).toEqual([]);
  });
});

describe("projectRows", () => {
  test("preserves the verb's order and marks the live rows", () => {
    const rows = projectRows([
      {
        id: "homie",
        title: "Culler",
        jurisdiction: "self",
        sandbox_profile: "trusted",
        status: "active",
      },
      { id: "later", title: "Later", status: "retired" },
    ]);
    expect(rows.map((r) => r.id)).toEqual(["homie", "later"]);
    expect(rows.map((r) => r.live)).toEqual([true, false]);
    expect(rows[0].profile).toBe("trusted");
  });

  test("absent columns fall back to an explicit dash", () => {
    const [row] = projectRows([{ id: "bare" }]);
    expect(row).toEqual({
      id: "bare",
      title: "",
      status: "—",
      profile: "—",
      jurisdiction: "—",
      live: false,
    });
  });

  test("empty input", () => {
    expect(projectRows([])).toEqual([]);
    expect(projectRows(undefined)).toEqual([]);
  });
});
