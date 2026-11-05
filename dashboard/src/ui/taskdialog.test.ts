import { afterAll, beforeAll, describe, expect, test } from "bun:test";

import { CORRECTIONS_TIP, HISTORY_LIMIT, RUN_FAILURES_TIP, decisionTone, historyView, liveRunView, runHistoryNode, taskDialogBody } from "./taskdialog.js";
import { taskDetail } from "./board.js";

describe("historyView", () => {
  test("lists the newest runs up to the limit and keeps the older runs", () => {
    const runs = Array.from({ length: HISTORY_LIMIT + 3 }, (_, i) => ({ id: `run-${i}` }));
    const v = historyView(runs);
    expect(v.shown.length).toBe(HISTORY_LIMIT);
    expect(v.shown[0]!.id).toBe("run-0");
    expect(v.older.map((run: { id: string }) => run.id)).toEqual(["run-6", "run-7", "run-8"]);
  });

  test("keeps no older run when the history fits", () => {
    const v = historyView([{ id: "a" }, { id: "b" }]);
    expect(v.shown.length).toBe(2);
    expect(v.older).toEqual([]);
  });

  test("tolerates an absent list", () => {
    expect(historyView(undefined)).toEqual({ shown: [], older: [] });
  });
});

/** The small part of the DOM that dom.js uses. */
class FakeElement {
  readonly nodeType = 1;
  readonly childNodes: Array<FakeElement | { nodeType: 3; data: string }> = [];
  readonly dataset: Record<string, string> = {};
  className = "";
  textContent = "";
  hidden = false;
  title = "";
  private readonly attrs = new Map<string, string>();
  constructor(readonly nodeName: string) {}
  setAttribute(name: string, value: string) { this.attrs.set(name, String(value)); }
  getAttribute(name: string) { return this.attrs.get(name) ?? null; }
  get attributes() { return [...this.attrs].map(([name, value]) => ({ name, value })); }
  addEventListener() {}
  append(...children: Array<FakeElement | string>) {
    for (const child of children) this.childNodes.push(typeof child === "string" ? { nodeType: 3, data: child } : child);
  }
  get text(): string {
    return this.textContent + this.childNodes.map((child) => (child instanceof FakeElement ? child.text : child.data)).join("");
  }
  all(): FakeElement[] {
    return [this, ...this.childNodes.flatMap((child) => (child instanceof FakeElement ? child.all() : []))];
  }
  byTestid(testid: string): FakeElement | undefined {
    return this.all().find((node) => node.dataset.testid === testid);
  }
}

describe("runHistoryNode", () => {
  const global = globalThis as { document?: unknown };
  beforeAll(() => { global.document = { createElement: (tag: string) => new FakeElement(tag.toUpperCase()) }; });
  afterAll(() => { delete global.document; });

  const viewers = { agentsview: { url: "http://127.0.0.1:7334", state: "available", reason: "" } };
  const runs = (count: number) => Array.from({ length: count }, (_, i) => ({
    id: `run-${i}`, tone: "leaf", result: "done", role: "worker", trace: { path: `/sessions/s-${i}` },
  }));

  test("lists the older runs in a disclosure, each with its trace link", () => {
    const list = runs(HISTORY_LIMIT + 2);
    list[HISTORY_LIMIT + 1]!.trace = null as unknown as { path: string };
    const node = runHistoryNode(list, { viewers }) as unknown as FakeElement;
    const older = node.byTestid("task-runs-earlier")!;
    expect(older.nodeName).toBe("DETAILS");
    expect(older.dataset.disclosure).toBe("earlier-runs");
    expect(older.childNodes[0]).toMatchObject({ nodeName: "SUMMARY", textContent: "2 earlier runs" });
    expect(older.all().filter((item) => item.nodeName === "LI").map((item) => item.dataset.testid))
      .toEqual(["task-run-run-6", "task-run-run-7"]);
    const link = older.byTestid("task-run-trace-run-6")!;
    expect(link.nodeName).toBe("A");
    expect(Object.fromEntries(link.attributes.map(({ name, value }) => [name, value]))).toEqual({
      href: "/agentsview/open?path=%2Fsessions%2Fs-6", target: "_blank", rel: "noopener noreferrer",
      "aria-label": "Trace of run run-6, opens a new tab", title: "The trace opens at its last message in AgentsView.",
    });
    expect(older.byTestid("task-run-trace-run-7")).toBeUndefined();
    expect(node.all().filter((item) => item.dataset.testid?.startsWith("task-run-trace-")))
      .toHaveLength(HISTORY_LIMIT + 1);
  });

  test("the origin mark ends the list that holds the first run", () => {
    const origins = (count: number) => (runHistoryNode(runs(count), { viewers }) as unknown as FakeElement).all()
      .filter((item) => item.nodeName === "OL").map((item) => item.className);
    expect(origins(HISTORY_LIMIT)).toEqual(["timeline timeline-origin"]);
    expect(origins(HISTORY_LIMIT + 2)).toEqual(["timeline", "timeline timeline-origin"]);
  });

  test("has no disclosure when the history fits", () => {
    const node = runHistoryNode(runs(HISTORY_LIMIT), { viewers }) as unknown as FakeElement;
    expect(node.byTestid("task-runs-earlier")).toBeUndefined();
    expect(node.all().filter((item) => item.dataset.testid?.startsWith("task-run-trace-"))).toHaveLength(HISTORY_LIMIT);
  });
});

describe("task dialog reports", () => {
  const global = globalThis as { document?: unknown };
  beforeAll(() => { global.document = { createElement: (tag: string) => new FakeElement(tag.toUpperCase()) }; });
  afterAll(() => { delete global.document; });

  const readReport = async () => ({ body: "", type: "text/plain" });
  const detail = (over: Record<string, unknown> = {}) => ({
    id: 3, scope: "task", title: "A task", description: null, stage: "Packaged", packet: 3,
    failure: { count: 0 }, ...over,
  });
  const body = (d: Record<string, unknown>, options: Record<string, unknown> = {}) => {
    const root = new FakeElement("ROOT");
    root.append(...(taskDialogBody(d, { onPacket: () => {}, readReport, ...options }) as unknown as FakeElement[]));
    return root;
  };
  test("an open correction adds no section, report, or alert to the task dialog", () => {
    const row = { id: 3, status: "worked", delivery: {
      verifier_correction_open: 1, correction_count: 1,
      verifier_report: { problem: "The export omits the date.", report_path: "/mc/artifacts/correction.html" },
    } };
    const root = body(taskDetail(row));
    expect(root.text).not.toContain(row.delivery.verifier_report.problem);
    expect(root.all().some(node => node.dataset.report === "/api/tasks/3/correction-report")).toBe(false);
    expect(root.all().some(node => node.className.includes("callout"))).toBe(false);
    const kv = root.all().find(node => node.className === "kv" && node.text.includes("Unsuccessful Verifier corrections"))!;
    expect(kv.all().find(node => node.className === "v")!.text).toBe("1 of 3");
  });

  test("a priority task shows the cause and Project hold outside technical details", () => {
    const root = body(detail({ projectHold: true, projectName: "Alpha", reason: "The Verifier rejected three corrections.",
      allowance: { allowance_condition: "verifier", correction_count: 3, verifier_correction_open: 1,
        verifier_report: { report_path: "/mc/artifacts/correction.html" },
        latest_conflict: { submitted_commit: "technical-commit" } } }));
    expect(root.text).toContain("Task #3 prevents new work in Project Alpha.");
    expect(root.text).toContain("You must address this task before unrelated Project work can continue.");
    expect(root.text).toContain("Verifier correction limit reached");
    expect(root.text).not.toContain("merge repair");
    const details = root.all().find(node => node.nodeName === "DETAILS" && node.text.includes("technical-commit"));
    expect(details).toBeDefined();
    expect(details?.text).not.toContain("You must address this task");
    expect(root.all().filter(node => node.dataset.report === "/api/tasks/3/correction-report")).toHaveLength(1);
  });

  test("a task opens its report through the review packet only", () => {
    const root = body(detail());
    expect(root.byTestid("task-dialog-packet")?.textContent).toBe("Open the review packet");
  });


  test("the record controls come first in the main column", () => {
    const controls = new FakeElement("SECTION");
    controls.dataset.testid = "board-record-controls";
    const [main] = taskDialogBody(detail(), { controls }) as unknown as FakeElement[];
    expect(main!.childNodes[0]).toBe(controls);
  });

  test("the allowances are facts with tooltips, not sections before the description", () => {
    const root = body(detail({ allowance: { correction_count: 1 }, runAllowance: { remaining: 2, retryAt: "in 4m" }, failure: { count: 1 } }));
    expect(root.all().some((node) => /correction-allowances|run-allowance/.test(node.className))).toBe(false);
    const facts = root.byTestid("task-dialog-facts")!;
    const kv = (label: string) => facts.all().find((node) => node.className === "kv" && node.text.startsWith(label))!;
    expect(kv("Task run failures").all().find((node) => node.className === "v")!.text).toBe("1 of 3");
    expect(kv("Task run failures").text).toContain("Next retry: in 4m");
    expect(kv("Unsuccessful Verifier corrections").all().find((node) => node.className === "v")!.text).toBe("1 of 3");
    for (const [label, tip] of [["Task run failures", RUN_FAILURES_TIP], ["Unsuccessful Verifier corrections", CORRECTIONS_TIP]] as const) {
      const button = kv(label).all().find((node) => node.className === "fact-tip")!;
      const tooltip = kv(label).all().find((node) => node.getAttribute("role") === "tooltip")!;
      expect(tooltip.text).toBe(tip);
      expect(button.getAttribute("aria-describedby")).toBe(tooltip.getAttribute("id"));
    }
  });

  test("a task without a Verifier record shows no correction fact", () => {
    expect(body(detail()).text).not.toContain("Unsuccessful Verifier corrections");
  });

  test("the history comes last in the main column", () => {
    const history = new FakeElement("DETAILS");
    history.dataset.testid = "board-history";
    const [main] = taskDialogBody(detail(), { history }) as unknown as FakeElement[];
    expect(main!.childNodes.at(-1)).toBe(history);
  });
});

describe("decisionTone", () => {
  test("approved is leaf, rejected is rust, anything else is stone", () => {
    expect(decisionTone("approved")).toBe("leaf");
    expect(decisionTone("rejected")).toBe("rust");
    expect(decisionTone("cancelled")).toBe("stone");
    expect(decisionTone(undefined)).toBe("stone");
  });
});

describe("liveRunView", () => {
  test("a fresh heartbeat reads as running now", () => {
    const v = liveRunView({ role: "worker", runID: "run-1", heartbeat: "5s", stale: false, age: "12m" });
    expect(v).toEqual({
      label: "Running now",
      started: "started 12m ago",
      meta: "worker · heartbeat 5s ago",
      runID: "run-1",
      stale: false,
    });
  });

  test("a stale heartbeat names itself", () => {
    const v = liveRunView({ role: "worker", runID: null, heartbeat: "9m", stale: true, age: "" });
    expect(v!.label).toBe("Heartbeat stopped");
    expect(v!.started).toBe("");
    expect(v!.runID).toBe("no run id");
    expect(v!.stale).toBe(true);
  });

  test("no run is null", () => {
    expect(liveRunView(null)).toBeNull();
  });
});
