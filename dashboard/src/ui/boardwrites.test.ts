import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import {
  REVISION_WAIT, boardEditState, boardRecordControls, boardWritePayload, createBoardWrites, resolveAction, resolveEffect,
  revisionEffect, revisionSaveRule, revisionWaitTexts, submissionPayload,
} from "./boardwrites.js";

const task = { id: 12, scope: "task", project: "alpha", title: "Task", description: "Requirements", priority: 2, record_version: 7, status: "worked" };
const report = { ...task, blocked: 1, blocked_kind: "report", blocked_reason: "The two export criteria contradict each other." };
const OTHER_KINDS = ["customization", "confinement", "checkout", "operator"];

describe("board write requests", () => {
  test("creation carries only user fields and the selected destination", () => {
    expect(boardWritePayload({
      action: "create", scope: "task", project: "alpha", title: "A new task", requirements: "", priority: "-1",
      taskID: 99, expectedVersion: 22, status: "packaged", branch: "main", origin: "agent",
    })).toEqual({ action: "create", scope: "task", project: "alpha", title: "A new task", priority: -1, description: "" });
  });

  test("an edit carries the presented version and preserves protected fields", () => {
    expect(boardWritePayload({ ...task, action: "edit", taskID: task.id, expectedVersion: task.record_version, title: "Revised title", priority: "0" }))
      .toEqual({ action: "edit", task_id: task.id, expected_version: task.record_version, title: "Revised title", priority: 0 });
    expect(boardWritePayload({ action: "revise", taskID: task.id, expectedVersion: task.record_version, scope: "task", requirements: "Revised", reason: "A new constraint" }))
      .toEqual({ action: "revise", task_id: task.id, expected_version: task.record_version, description: "Revised", reason: "A new constraint" });
  });

  test("each reason action carries its reason and version", () => {
    for (const action of ["unblock", "cancel"]) {
      expect(boardWritePayload({ action, taskID: 12, expectedVersion: 7, reason: "A concrete reason", title: "Excluded", priority: 2 }))
        .toEqual({ action, task_id: 12, expected_version: 7, reason: "A concrete reason" });
    }
  });

  test("an exact retry preserves its operation after a lost response and reload", () => {
    const draft = { action: "create", scope: "task", title: "Unique task", project: "alpha", requirements: "", priority: 2 };
    const first = submissionPayload(draft, () => "operation-one");
    expect(submissionPayload(draft, () => "operation-two")).toEqual(first);
    const restored = JSON.parse(JSON.stringify(draft));
    expect(submissionPayload(restored, () => "operation-three")).toEqual(first);
    restored.title = "Another task";
    expect(submissionPayload(restored, () => "operation-four").operation_id).toBe("operation-four");
  });
});

describe("record controls", () => {
  test("a final decision prevents content edits and cancellation", () => {
    for (const decision of ["rejected", "cancelled"]) {
      expect(boardEditState({ ...task, decision })).toMatchObject({ canEdit: false, canRevise: false, canUnblock: false, canCancel: false });
    }
    expect(boardEditState({ ...task, archived: 1 })).toMatchObject({ canEdit: false, canRevise: false, canCancel: false });
  });

  test("approved work permits recovery and cancellation before application", () => {
    for (const failure of [{ blocked: 1 }, { stalled: 1 }]) {
      expect(boardEditState({ ...task, status: "packaged", decision: "approved", ...failure }))
        .toMatchObject({ canEdit: false, canRevise: false, canUnblock: Boolean(failure.blocked), canResume: Boolean(failure.stalled), canCancel: true });
      expect(boardEditState({ ...task, status: "packaged", decision: "approved", archived: 1, ...failure }).canUnblock).toBe(false);
      expect(boardEditState({ ...task, decision: "approved", delivery: { application_pending: 1 }, ...failure }).canCancel).toBe(false);
      for (const decision of ["rejected", "cancelled"]) {
        expect(boardEditState({ ...task, decision, ...failure }).canUnblock).toBe(false);
      }
    }
  });

  test("a task revision prevents a second requirements revision and preserves title edits", () => {
    expect(boardEditState({ ...task, pending_revision: { id: 4 } })).toMatchObject({ canEdit: true, canRevise: false });
    expect(boardEditState({ ...task, blocked: 1 })).toMatchObject({ canUnblock: true });
    expect(boardEditState({ ...task, status: "packaged" })).toMatchObject({ canEdit: false, canRevise: true });
  });

  test("a partial task read cannot offer unversioned edits", () => {
    expect(boardEditState({ id: 12, title: "Partial" }).canEdit).toBe(false);
  });
});

test("revision effects describe the current stage and the exact active run", () => {
  expect(revisionEffect({ ...task, status: "seeded" })).toContain("The task stays Seeded. The next Worker run uses the revised requirements.");
  for (const status of ["worked", "verified", "packaged"]) {
    const texts = revisionEffect({ ...task, status }, { run_id: "active-run" });
    expect(texts[0]).toContain("active-run");
    expect(texts[1]).toContain("returns to Seeded");
  }
});

test("a revision that waits for a repository operation names no current run", () => {
  const waitReason = "mc saved your revision request. A repository operation will finish before mc applies your changes.";
  const texts = revisionWaitTexts({ id: 4, state: "pending", wait_reason: waitReason });
  expect(texts).toEqual([waitReason]);
  expect(texts).not.toContain(REVISION_WAIT);
  expect(texts.some((text) => text.startsWith("Current run:"))).toBe(false);
  expect(revisionWaitTexts({ id: 4, state: "pending", active_run_id: "worker-7", wait_reason: REVISION_WAIT }))
    .toEqual([REVISION_WAIT, "Current run: worker-7"]);
});

test("Proposed tasks omit every operator action before and during proposal review", () => {
  for (const pool of [null, [10,11]]) {
    const view=boardEditState({...task,status:"proposed",pool_snapshot:pool,blocked:1,stalled:1});
    for (const action of ["canEdit","canRevise","canUnblock","canResume","canCancel"]) expect(view[action]).toBe(false);
  }
});

test("the Verifier allowance limit requires its explicit action", () => {
  const row = { ...task, blocked: 1, delivery: { allowance_condition: "verifier" } };
  expect(boardEditState(row)).toMatchObject({ canUnblock: false, canAuthorize: true, canCancel: true, canRevise: true });
  expect(boardWritePayload({ action: "allow-corrections", taskID: 12, expectedVersion: 7, reason: "Continue this correction." }))
    .toEqual({ action: "allow-corrections", task_id: 12, expected_version: 7, reason: "Continue this correction." });
});

describe("Resolve decision of an operator decision report", () => {
  test("the condition type selects the form", () => {
    expect(resolveAction(report)).toBe("resolve");
    for (const kind of [...OTHER_KINDS, "allowance", "proposal", undefined]) {
      expect(resolveAction({ ...report, blocked_kind: kind })).toBe("unblock");
    }
    expect(resolveAction({ ...report, blocked: 0 })).toBe("unblock");
  });

  test("Resolve decision requires the conditions of a requirements revision", () => {
    expect(boardEditState(report)).toMatchObject({ canUnblock: true, canRevise: true, canCancel: true });
    expect(boardEditState({ ...report, pending_revision: { id: 4 } })).toMatchObject({ canUnblock: false, canCancel: true });
    expect(boardEditState({ ...report, status: "packaged", decision: "approved" })).toMatchObject({ canUnblock: false, canCancel: true });
    for (const kind of OTHER_KINDS) {
      expect(boardEditState({ ...report, blocked_kind: kind, pending_revision: { id: 4 } }).canUnblock).toBe(true);
      expect(boardEditState({ ...report, blocked_kind: kind, status: "packaged", decision: "approved" }).canUnblock).toBe(true);
    }
  });

  test("a save requires a changed description and a reason", () => {
    const draft = { action: "resolve", record: report, requirements: report.description, reason: "" };
    expect(revisionSaveRule(draft)).toBe("Change the description and write a reason before you save.");
    expect(revisionSaveRule({ ...draft, requirements: `  ${report.description}\n`, reason: "The operator chose the CSV criterion." }))
      .toBe("Change the description before you save.");
    expect(revisionSaveRule({ ...draft, requirements: "Export CSV only.", reason: "  " })).toBe("Write a reason before you save.");
    expect(revisionSaveRule({ ...draft, requirements: "Export CSV only.", reason: "The operator chose the CSV criterion." })).toBe("");
    expect(revisionSaveRule({ ...draft, action: "revise" })).toBe("Change the description and write a reason before you save.");
    for (const action of ["unblock", "cancel", "edit"]) expect(revisionSaveRule({ ...draft, action })).toBe("");
  });

  test("the save is a requirements revision with the description and the reason", () => {
    const draft = { action: "resolve", taskID: 12, expectedVersion: 7, requirements: "Export CSV only.", reason: "The operator chose the CSV criterion.", record: report };
    expect(boardWritePayload(draft)).toEqual({
      action: "revise", task_id: 12, expected_version: 7, description: "Export CSV only.", reason: "The operator chose the CSV criterion.",
    });
    const first = submissionPayload(draft, () => "operation-one");
    expect(first).toMatchObject({ action: "revise", operation_id: "operation-one" });
    expect(submissionPayload(draft, () => "operation-two").operation_id).toBe("operation-one");
  });

  test("the effect states the continuation and the wait for an active run", () => {
    const texts = resolveEffect(report);
    expect(texts).toContain("The revision ends Decision Pending and returns the task to Seeded.");
    expect(texts.at(-1)).toBe("The Worker continues from the last commit of the task.");
    expect(texts.some((text) => text.startsWith("Run "))).toBe(false);
    const waiting = resolveEffect({ ...report, status: "seeded", stalled: 1 }, { run_id: "worker-7" });
    expect(waiting).toContain("Run worker-7 must finish before the revision takes effect. Decision Pending stays until then.");
    expect(waiting).toContain("The revision ends Decision Pending. The task stays Seeded.");
    expect(waiting).toContain("Retries Exhausted stays in effect.");
    expect(revisionEffect(report)).toContain("The revision ends Decision Pending.");
    expect(revisionEffect({ ...report, blocked_kind: "operator" })).not.toContain("The revision ends Decision Pending.");
  });
});

type Listener = (event: Record<string, unknown>) => unknown;

/** The small part of the DOM that dom.js and the board write dialog use. */
class FakeNode {
  readonly childNodes: FakeNode[] = [];
  readonly dataset: Record<string, string> = {};
  readonly listeners = new Map<string, Listener[]>();
  private readonly attrs = new Map<string, string>();
  private ownText = "";
  className = "";
  readonly classList = { add: (...names: string[]) => { this.className = [...new Set([...this.className.split(" "), ...names])].filter(Boolean).join(" "); } };
  hidden = false;
  disabled = false;
  value = "";
  open = false;
  constructor(readonly nodeName: string, readonly nodeType = 1) {}
  get textContent(): string { return this.ownText + this.childNodes.map((child) => child.textContent).join(""); }
  set textContent(value: string) { this.ownText = value; this.childNodes.length = 0; }
  get attributes() { return [...this.attrs].map(([name, value]) => ({ name, value })); }
  setAttribute(name: string, value: string) {
    this.attrs.set(name, String(value));
    if (name === "disabled") this.disabled = true;
  }
  getAttribute(name: string) { return this.attrs.get(name) ?? null; }
  addEventListener(type: string, listener: Listener) { this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]); }
  removeEventListener() {}
  dispatch(type: string) {
    return Promise.all((this.listeners.get(type) ?? []).map((listener) => listener({ preventDefault() {}, currentTarget: this, target: this })));
  }
  append(...children: Array<FakeNode | string>) {
    for (const child of children) this.childNodes.push(typeof child === "string" ? textNode(child) : child);
  }
  replaceChildren(...children: FakeNode[]) { this.childNodes.length = 0; this.append(...children); }
  all(): FakeNode[] { return [this, ...this.childNodes.flatMap((child) => child.all())]; }
  byTestid(testid: string) { return this.all().find((node) => node.dataset.testid === testid); }
  querySelector(selector: string) {
    const testid = selector.match(/^\[data-testid="(.+)"\]$/)?.[1];
    return testid ? this.byTestid(testid) ?? null : null;
  }
  querySelectorAll(selector: string) {
    const names = selector.split(",").map((part) => part.trim().split(/[:[]/)[0]!.toUpperCase());
    return this.all().slice(1).filter((node) => names.includes(node.nodeName));
  }
  focus() { focused = this; }
  getClientRects() { return [{}]; }
  showModal() { this.open = true; }
  close() { this.open = false; void this.dispatch("close"); }
}

const textNode = (text: string) => {
  const node = new FakeNode("#text", 3);
  node.textContent = text;
  return node;
};
let focused: FakeNode | null = null;

describe("the Resolve decision dialog", () => {
  const global = globalThis as { document?: unknown; sessionStorage?: unknown };
  const saved = { document: global.document, sessionStorage: global.sessionStorage };
  const storage = new Map<string, string>();
  beforeAll(() => {
    global.document = {
      createElement: (name: string) => new FakeNode(name.toUpperCase()),
      createTextNode: textNode,
      querySelector: () => null,
      get activeElement() { return focused; },
    };
    global.sessionStorage = {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => { storage.set(key, value); },
      removeItem: (key: string) => { storage.delete(key); },
    };
  });
  afterAll(() => { global.document = saved.document; global.sessionStorage = saved.sessionStorage; });
  beforeEach(() => { storage.clear(); focused = null; });

  function open(row: Record<string, unknown>, activeRun: unknown = null) {
    const dialog = new FakeNode("DIALOG");
    const requests: Array<{ path: string; body: unknown }> = [];
    const writes = createBoardWrites({
      dialog, getProjects: () => [{ id: "alpha", title: "Alpha" }], selectedProject: () => "alpha",
      api: async (path: string, options: { body: string }) => { requests.push({ path, body: JSON.parse(options.body) }); return { task_id: row.id }; },
      onSaved: async () => {}, onCollie: () => {},
    });
    writes.openAction("unblock", row, activeRun);
    const node = (testid: string) => dialog.byTestid(testid);
    const type = (testid: string, value: string) => { node(testid)!.value = value; return node(testid)!.dispatch("input"); };
    const form = () => dialog.childNodes[0]!;
    const heading = () => dialog.all().find((item) => item.nodeName === "H2")?.textContent;
    const label = (testid: string) => dialog.all().find((item) => item.nodeName === "LABEL" && item.childNodes.includes(node(testid)!))?.childNodes[0]?.textContent;
    return { dialog, writes, requests, node, type, form, heading, label };
  }

  test("an operator decision report opens the decision form", () => {
    const view = open(report);
    expect(view.dialog.open).toBe(true);
    expect(view.heading()).toBe("Resolve decision");
    expect(view.node("board-resolve-report")!.textContent).toContain(report.blocked_reason);
    expect(view.node("board-write-requirements")!.value).toBe(report.description);
    expect(view.label("board-write-requirements")).toBe("Description");
    expect(view.label("board-write-reason")).toBe("What resolved the decision?");
    expect(view.node("board-resolve-effect")!.textContent).toContain("The Worker continues from the last commit of the task.");
    expect(view.node("board-resolve-cancel")!.textContent).toBe("Cancel task");
    expect(view.node("board-action-explanation")).toBeUndefined();
    expect(view.node("board-write-submit")!.textContent).toBe("Save requirements");
  });

  test("each other condition type keeps the reason-only form", () => {
    for (const kind of [...OTHER_KINDS, undefined]) {
      const view = open({ ...report, blocked_kind: kind });
      expect(view.heading()).toBe("Resolve decision");
      expect(view.label("board-write-reason")).toBe("What resolved the decision?");
      expect(view.node("board-action-explanation")).toBeDefined();
      for (const testid of ["board-write-requirements", "board-resolve-report", "board-resolve-effect", "board-resolve-cancel"]) {
        expect(view.node(testid)).toBeUndefined();
      }
      expect(view.node("board-write-submit")!.disabled).toBe(false);
      view.writes.close();
    }
  });

  test("the decision form saves a changed description and a reason as one revision", async () => {
    const view = open(report);
    const submit = view.node("board-write-submit")!;
    const rule = view.node("board-write-rule")!;
    expect(submit.disabled).toBe(true);
    expect(rule.hidden).toBe(false);
    expect(rule.textContent).toBe("Change the description and write a reason before you save.");
    await view.form().dispatch("submit");
    expect(view.requests).toEqual([]);

    await view.type("board-write-reason", "The operator chose the CSV criterion.");
    expect(submit.disabled).toBe(true);
    expect(rule.textContent).toBe("Change the description before you save.");
    await view.type("board-write-requirements", `${report.description}\n`);
    expect(submit.disabled).toBe(true);

    await view.type("board-write-requirements", "Export CSV only. The export omits the JSON file.");
    expect(submit.disabled).toBe(false);
    expect(rule.hidden).toBe(true);
    await view.form().dispatch("submit");
    expect(view.requests).toHaveLength(1);
    expect(view.requests[0]!.path).toBe("/api/board/write");
    expect(view.requests[0]!.body).toEqual({
      action: "revise", task_id: report.id, expected_version: report.record_version,
      description: "Export CSV only. The export omits the JSON file.", reason: "The operator chose the CSV criterion.",
      operation_id: expect.any(String),
    });
    expect(view.dialog.open).toBe(false);
  });

  test("a pending revision refuses a second save", () => {
    const view = open(report);
    view.writes.updateRecord({ ...report, pending_revision: { id: 4, state: "pending" } });
    expect(view.node("board-write-restriction")!.textContent).toBe("The requested requirements must take effect before another revision.");
    expect(view.node("board-write-submit")!.disabled).toBe(true);
  });

  test("Cancel task opens the cancellation form in the same dialog", () => {
    const view = open(report, { run_id: "worker-7" });
    expect(view.node("board-resolve-effect")!.textContent).toContain("Run worker-7 must finish before the revision takes effect.");
    view.node("board-resolve-cancel")!.dispatch("click");
    expect(view.dialog.open).toBe(true);
    expect(view.heading()).toBe("Cancel task");
    expect(view.node("board-write-submit")!.className).toBe("btn danger");
    expect(view.node("board-write-requirements")).toBeUndefined();
    expect(focused).toBe(view.node("board-write-reason")!);
  });

  test("the record controls open the form that the condition type selects", () => {
    const actions: string[] = [];
    const controls = boardRecordControls(report, { onAction: (action: string) => actions.push(action) }) as unknown as FakeNode;
    const lead = controls.byTestid("board-unblock")!;
    expect(lead.textContent).toBe("Resolve decision");
    expect(lead.disabled).toBe(false);
    lead.dispatch("click");
    expect(actions).toEqual(["unblock"]);
    const waiting = boardRecordControls({ ...report, pending_revision: { id: 4, requirements: "Export CSV only.", reason: "A decision", state: "pending" } }) as unknown as FakeNode;
    expect(waiting.byTestid("board-unblock")!.disabled).toBe(true);
    expect(waiting.byTestid("board-revision-status")).toBeDefined();
  });
});
