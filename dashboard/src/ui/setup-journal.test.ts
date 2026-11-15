import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { setupJournalModel, setupJournalNode, setupProposalSummary, acceptSetupHealth, setupFailureDisplay, setupHistoryAvailable } from "./setup-journal.js";
import { markdownNodes, safeMarkdownHref } from "./dom.js";
import { parseMarkdown } from "./markdown.js";

const configuration = (content = "FROM retained-base\nRUN install-tool\n") => ({
  base_digest: "sha256:base", dockerfile: { path: ".culler/Dockerfile", content },
  build_files: [{ path: ".culler/setup.md", content: "# Exact input\n\n**Keep these bytes.**\n" }],
  build_args: { TOOL_VERSION: "1.2" }, setup: { command: "install-dependencies", directory: "." },
});
const operation = (id: string, extra = {}) => ({
  id, project_id: "garden", revision_id: "initial", kind: "setup", status: "running", attempt: 1,
  created_at: "2026-10-12 12:00:00", ended_at: "", metadata: {}, ...extra,
});
const revision = (id: string, extra = {}) => ({
  id, project_id: "garden", editor: "operator", configuration: configuration(), explanation: "Install the database headers. The Project needs them for its checks.", ...extra,
});
const base = (extra = {}) => ({
  environment: { project_id: "garden", project_title: "Garden", setup_status: "configured", setup_id: "setup", vision_revision: "vision-1",
    phase: "agent", draft_revision_id: "initial", active_operation_id: "one", selected_revision_id: "" },
  vision: { revision: "vision-1", content: "# Garden\n\nKeep plants healthy.", created_at: "2026-10-12 11:00:00" },
  revisions: [revision("initial")], operations: [operation("one")], results: [], approvals: [], ...extra,
});
const milestones = model => model.entries.filter(row => row.kind === "milestone");

test("the journal follows recorded operation states and stops activity after a failed read", () => {
  const view = base();
  const active = setupJournalModel(view);
  expect(active.state).toBe("Setup in progress");
  expect(active.operations[0].active).toBe(true);
  expect(active.operations[0].actor).toBe("Setup agent");
  expect(milestones(active).map(row => row.state)).toEqual(["complete", "started"]);
  const stale = setupJournalModel(view, { unavailable: true, lastReadAt: "2026-10-12T12:01:00Z" });
  expect(stale.operations[0].active).toBe(false);
  expect(stale.operations[0].outcome.text).toBe("Active");
  expect(stale.lastReadAt).toBe("2026-10-12T12:01:00Z");
  view.operations[0].status = "queued";
  expect(setupJournalModel(view).operations[0].active).toBe(false);
  expect(setupJournalModel(view).operations[0].summary).toBe("The setup agent awaits launch.");
  view.environment.active_operation_id = "other";
  view.operations[0].status = "running";
  expect(setupJournalModel(view).operations[0].active).toBe(false);
});

test("equal creation times retain record order and retries retain their own identity", () => {
  const view = base({ operations: [operation("retry", { attempt: 2, metadata: { previous_operation_id: "one" } }),
    operation("one", { status: "failed", error: "The image was unavailable." })] });
  view.environment.active_operation_id = "retry";
  const model = setupJournalModel(view);
  expect(model.operations.map(row => row.id)).toEqual(["one", "retry"]);
  expect(model.operations[0].outcome.text).toBe("Execution failed");
  expect(model.operations[1].operation.metadata.previous_operation_id).toBe("one");
  expect(model.operations[1].operation.attempt).toBe(2);
  expect(model.operations[0].current).toBe(false);
  expect(model.operations[1].current).toBe(true);
});

test("a successful agent process alone does not complete preparation", () => {
  const view = base({ operations: [operation("one", { status: "succeeded" })] });
  view.environment.active_operation_id = "";
  const model = setupJournalModel(view);
  expect(model.operations[0].outcome.text).toBe("Result unavailable");
  expect(milestones(model).filter(row => row.id === "prepare").map(row => row.state)).toEqual(["started"]);
  expect(model.completed).toBe(false);
});

test("execution failures remain separate from complete reports with limits", () => {
  const view = base({ operations: [
    operation("limited", { status: "succeeded" }),
    operation("canceled", { status: "canceled" }),
    operation("failed", { status: "failed" }),
  ], results: [{ id: "result", operation_id: "limited", revision_id: "initial", outcome: "complete",
    summary: "Preparation is complete with unavailable tools.", details: "Every repository test remains unverified." }] });
  const rows = setupJournalModel(view).operations;
  expect(rows.map(row => row.outcome.text)).toEqual(["Execution failed", "Canceled", "Preparation complete"]);
  expect(rows[2].summary).toBe(view.results[0].summary);
});

function proposalView() {
  const proposal = revision("proposal", { editor: "setup:one" });
  const view = base({ draft: proposal, revisions: [proposal, revision("initial")],
    operations: [operation("one", { status: "succeeded", metadata: { report_outcome: "proposal", proposed_revision_id: "proposal" } })] });
  view.environment.phase = "approval";
  view.environment.draft_revision_id = "proposal";
  view.environment.active_operation_id = "";
  return view;
}

test("approval targets the exact current revision and earlier approval remains visible", () => {
  const view = proposalView();
  const pending = setupJournalModel(view);
  expect(pending.approval?.revision.id).toBe("proposal");
  expect(pending.operations[0].currentProposal).toBe(true);
  expect(pending.operations[0].outcome.text).toBe("Image proposal");
  view.approvals = [{ revision_id: "proposal", created_at: "2026-10-12 12:01:00" }];
  view.operations.unshift(operation("build", { kind: "build", revision_id: "proposal" }));
  view.environment.phase = "build";
  view.environment.active_operation_id = "build";
  const approved = setupJournalModel(view);
  expect(approved.approval).toBeNull();
  expect(approved.operations[0].outcome.text).toBe("Image changes approved");
  expect(approved.operations[0].approval.created_at).toBe("2026-10-12 12:01:00");
  expect(approved.operations[1].actor).toBe("Culler");
  expect(approved.operations[0].currentProposal).toBe(false);
});

test("a stale or active draft exposes no approval action", () => {
  const view = proposalView();
  view.environment.draft_revision_id = "new-draft";
  expect(setupJournalModel(view).approval).toBeNull();
  view.environment.draft_revision_id = "proposal";
  view.environment.active_operation_id = "another";
  expect(setupJournalModel(view).approval).toBeNull();
});

test("a denied proposal retains its decision and current image", () => {
  const view = proposalView();
  view.decisions = [{ revision_id: "proposal", decision: "denied", current_image_digest: "sha256:current", created_at: "2026-10-12 12:01:00" }];
  view.environment.phase = "queued";
  const row = setupJournalModel(view).operations[0];
  expect(row.outcome.text).toBe("Current image retained");
  expect(row.decision.current_image_digest).toBe("sha256:current");
  expect(setupJournalModel(view).approval).toBeNull();
  expect(setupJournalModel(view).progressing).toBe(true);
});

function completedView() {
  const result = { id: "result", operation_id: "one", revision_id: "initial", outcome: "complete",
    summary: "The available preparation is complete.", details: "The test command returned exit 1." };
  const accepted = operation("accept", { kind: "accept", status: "succeeded", ended_at: "2026-10-12 12:02:00",
    metadata: { initial_setup: true, result_id: "result", source_commit: "accepted-commit", previous_operation_id: "one" } });
  const view = base({ operations: [accepted, operation("one", { status: "succeeded" })], results: [result] });
  view.environment.setup_status = "ready";
  view.environment.phase = "complete";
  view.environment.active_operation_id = "";
  view.environment.selected_revision_id = "initial";
  return view;
}

test("initial history ends at the first successful acceptance and retains its evidence", () => {
  const view = completedView();
  view.operations.unshift(operation("maintenance", { kind: "setup", revision_id: "later" }));
  view.results.unshift({ id: "later-result", operation_id: "maintenance", revision_id: "later", outcome: "proposal", summary: "The Project needs another tool." });
  view.revisions.unshift(revision("later"));
  view.environment.active_operation_id = "maintenance";
  view.environment.phase = "agent";
  view.environment.selected_revision_id = "later";
  const model = setupJournalModel(view);
  expect(model.operations.map(row => row.id)).toEqual(["one", "accept"]);
  expect(model.completed).toBe(true);
  expect(milestones(model).filter(row => row.state === "complete").map(row => row.id)).toEqual(["vision", "prepare", "accept", "ready"]);
  expect(model.completion.revision.id).toBe("initial");
  expect(model.completion.result.id).toBe("result");
  expect(model.completion.sourceCommit).toBe("accepted-commit");
  expect(model.completion.completedAt).toBe("2026-10-12 12:02:00");
  expect(model.operations.some(row => row.active)).toBe(false);
});

test("setup history opens on request after initial setup completes or selects an environment", () => {
  expect(setupHistoryAvailable(completedView())).toBe(true);
  expect(setupHistoryAvailable(base())).toBe(false);
  const selected = base();
  selected.environment.selected_revision_id = "initial";
  expect(setupHistoryAvailable(selected)).toBe(true);
  const withoutSetup = completedView();
  withoutSetup.environment.setup_id = "";
  expect(setupHistoryAvailable(withoutSetup)).toBe(false);
  expect(setupHistoryAvailable({ environment: { project_id: "garden" } })).toBe(false);
});

test("a complete report qualifies preparation while source acceptance remains incomplete", () => {
  const view = completedView();
  view.environment.setup_status = "configured";
  view.operations[0].status = "running";
  view.environment.active_operation_id = "accept";
  const model = setupJournalModel(view);
  expect(milestones(model).map(row => row.state)).toEqual(["complete", "complete", "started"]);
  expect(model.operations[0].result.details).toBe("The test command returned exit 1.");
  expect(model.completed).toBe(false);
});

test("a later retry does not reuse an earlier report to complete preparation", () => {
  const view = completedView();
  view.environment.setup_status = "configured";
  view.operations[0].status = "failed";
  view.operations.unshift(operation("retry"));
  view.environment.active_operation_id = "retry";
  expect(milestones(setupJournalModel(view)).filter(row => row.id === "prepare")).toMatchObject([{ id: "prepare", state: "started" }]);
});

test("milestones keep one identity through retries and completion", () => {
  const view = completedView();
  const original = milestones(setupJournalModel(view));
  view.environment.setup_status = "configured";
  view.operations[0].status = "failed";
  view.operations.unshift(operation("retry"));
  view.environment.active_operation_id = "retry";
  const retry = setupJournalModel(view);
  expect(milestones(retry).map(row => [row.id, row.state])).toEqual([
    ["vision", "complete"], ["prepare", "started"], ["accept", "started"],
  ]);
  view.operations[0].status = "succeeded";
  view.results.unshift({ id: "retry-result", operation_id: "retry", revision_id: "initial", outcome: "complete" });
  view.operations.unshift(operation("accept-retry", { kind: "accept", status: "succeeded",
    metadata: { initial_setup: true, result_id: "retry-result" } }));
  view.environment.setup_status = "ready";
  view.environment.active_operation_id = "";
  const complete = setupJournalModel(view);
  expect(milestones(complete)).toEqual(original);
  expect(complete.operations.map(row => row.id)).toEqual(["one", "accept", "retry", "accept-retry"]);
  const queued = setupJournalModel(base({ operations: [] }));
  expect(milestones(queued).map(row => row.key)).toEqual(milestones(setupJournalModel(base())).map(row => row.key));
});

test("ready environment state cannot substitute for absent initial acceptance", () => {
  const view = completedView();
  view.operations = [operation("maintenance", { status: "succeeded" })];
  const model = setupJournalModel(view);
  expect(model.completed).toBe(false);
  expect(model.historicalEvidenceMissing).toBe(true);
  expect(model.operations).toEqual([]);
  expect(model.completion).toBeNull();
  expect(model.state).toBe("Evidence unavailable");
});

test("completion does not invent missing result, revision, or Vision evidence", () => {
  const view = completedView();
  view.results = [];
  view.revisions = [];
  view.vision.revision = "new-vision";
  const model = setupJournalModel(view);
  expect(model.completion.revision).toBeNull();
  expect(model.completion.result).toBeNull();
  expect(milestones(model).filter(row => row.id === "prepare").map(row => row.state)).toEqual(["started"]);
  expect(model.vision).toBeNull();
});

test("a proposal compares against its preceding acceptance without later selected state", () => {
  const view = proposalView();
  const old = revision("previous", { configuration: configuration("FROM old-base\n") });
  view.revisions.push(old);
  view.operations.push(operation("previous-accept", { kind: "accept", revision_id: "previous", status: "succeeded", metadata: { initial_setup: false } }));
  const model = setupJournalModel(view);
  expect(model.operations[1].acceptedConfiguration.dockerfile.content).toBe("FROM old-base\n");
  expect(model.approval.acceptedConfiguration).toBe(old.configuration);
  const complete = completedView();
  const later = revision("later", { configuration: configuration("FROM future-base\n") });
  Object.assign(complete, { selected: later });
  complete.revisions.unshift(later);
  expect(setupJournalModel(complete).operations[0].acceptedConfiguration).toBeNull();
});

test("large explanations stay intact in proposal details", () => {
  expect(setupProposalSummary("Install `git`. The Project checks need it.")).toEqual({ text: "Install `git`. The Project checks need it.", details: "" });
  const long = "## Recorded observations\n\n" + "The agent recorded a diagnostic result. ".repeat(40);
  expect(setupProposalSummary(long).details).toBe(long);
  expect(setupProposalSummary(long).text).toContain("Image dependencies");
  expect(setupProposalSummary(long).text.length).toBeLessThan(180);
});

class FakeNode {
  readonly nodeType: number;
  readonly childNodes: FakeNode[] = [];
  readonly dataset: Record<string, string> = {};
  readonly attributes: Array<{ name: string; value: string }> = [];
  readonly listeners: Record<string, (event?: unknown) => void> = {};
  className = "";
  ownText = "";
  constructor(readonly nodeName: string, text = "") { this.nodeType = nodeName === "#text" ? 3 : 1; this.ownText = text; }
  set textContent(value: string) { this.ownText = value; }
  get textContent(): string { return this.ownText + this.childNodes.map(child => child.textContent).join(""); }
  setAttribute(name: string, value: string) { this.attributes.push({ name, value }); }
  getAttribute(name: string) { return this.attributes.find(attribute => attribute.name === name)?.value ?? null; }
  addEventListener(name: string, callback: (event?: unknown) => void) { this.listeners[name] = callback; }
  append(child: FakeNode | string) { this.childNodes.push(typeof child === "string" ? new FakeNode("#text", child) : child); }
  all(): FakeNode[] { return [this, ...this.childNodes.flatMap(child => child.all())]; }
}

describe("journal document", () => {
  const global = globalThis as { document?: unknown };
  beforeAll(() => { global.document = {
    createElement: (tag: string) => new FakeNode(tag.toUpperCase()),
    createTextNode: (text: string) => new FakeNode("#text", text),
  }; });
  afterAll(() => { delete global.document; });
  const render = (view: unknown, options = {}, actions = {}, viewers = {}) => setupJournalNode(setupJournalModel(view, options), actions, viewers) as unknown as FakeNode;
  const find = (node: FakeNode, testid: string) => node.all().find(child => child.dataset.testid === testid)!;

  test("a setup agent operation links its AgentsView trace beside its status", () => {
    const trace = { session: "setup-session", path: "/sessions/setup-session?msg=last" };
    const available = { viewers: { agentsview: { state: "available", url: "http://127.0.0.1:7340" } }, pending: false };
    expect(find(render(base(), {}, {}, available), "setup-trace-link")).toBeUndefined();
    const view = base({ operations: [operation("one", { trace })] });
    expect(setupJournalModel(view).operations[0].trace).toEqual(trace);
    const node = render(view, {}, {}, available);
    const link = find(node, "setup-trace-link");
    expect(link.nodeName).toBe("A");
    expect(link.getAttribute("href")).toBe("/agentsview/open?path=%2Fsessions%2Fsetup-session%3Fmsg%3Dlast");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    expect(link.getAttribute("aria-label")).toBe("Trace of setup operation one, opens a new tab");
    expect(link.textContent).toBe("Trace ↗");
    const meta = find(node, "setup-operation").all().find(child => child.className === "journal-message-meta")!;
    expect(meta.childNodes.at(-2)).toBe(link);
    expect(meta.childNodes.at(-1)?.textContent).toBe("See what the agent is up to");
    const finished = base({ operations: [operation("one", { status: "succeeded", trace })] });
    finished.environment.active_operation_id = "";
    expect(find(render(finished, {}, {}, available), "setup-trace-link").getAttribute("href")).toBe(link.getAttribute("href"));
    const unavailable = { viewers: { agentsview: { state: "unavailable", url: "http://127.0.0.1:7340", reason: "AgentsView is not running." } }, pending: false };
    const disabled = find(render(view, {}, {}, unavailable), "setup-trace-link");
    expect(disabled.getAttribute("href")).toBeNull();
    expect(disabled.getAttribute("aria-disabled")).toBe("true");
    expect(disabled.getAttribute("title")).toBe("AgentsView is unavailable. AgentsView is not running.");
    const loading = find(render(view, {}, {}, { viewers: null, pending: true }), "setup-trace-link");
    expect(loading.getAttribute("href")).toBeNull();
    expect(loading.getAttribute("title")).toBe("Loading AgentsView status…");
    expect(find(render(base({ operations: [operation("one", { kind: "build", trace })] }), {}, {}, available), "setup-trace-link")).toBeUndefined();
  });

  test("the header explains the setup steps until setup completes", () => {
    const lead = "The setup agent downloads and installs the Project's dependencies, then builds the Project and runs its tests.";
    expect(find(render(null), "setup-lead").textContent).toBe(lead);
    expect(find(render(base()), "setup-lead").textContent).toBe(lead);
    const failed = base({ operations: [operation("one", { status: "failed", error: "install failed" })] });
    failed.environment.active_operation_id = "";
    failed.environment.phase = "failed";
    expect(find(render(failed), "setup-lead").textContent).toBe(lead);
    expect(find(render(completedView()), "setup-lead")).toBeUndefined();
  });

  test("setup activity covers the first read, the queue, execution, and intervals between operations", () => {
    const pulses = (node: FakeNode) => node.all().filter(child => child.className === "journal-pulse");
    expect(pulses(render(null))).toHaveLength(1);
    expect(pulses(render(null, { unavailable: true }))).toHaveLength(0);
    const initial = base({ operations: [] });
    initial.environment.active_operation_id = "";
    expect(pulses(find(render(initial), "setup-phase"))).toHaveLength(1);
    expect(pulses(find(render(initial), "setup-transcript"))).toHaveLength(1);
    for (const kind of ["setup", "build", "validate", "accept"]) {
      for (const status of ["queued", "running"]) {
        const view = base({ operations: [operation("one", { kind, status })] });
        const node = render(view);
        expect(pulses(find(node, "setup-phase"))).toHaveLength(1);
        expect(pulses(find(node, "setup-operation"))).toHaveLength(1);
        expect(find(node, "setup-operation").textContent).toContain(status === "queued" ? "Queued" : "Active");
      }
    }
    const gap = base({ operations: [operation("one", { kind: "build", status: "succeeded" })] });
    gap.environment.active_operation_id = "";
    expect(find(render(gap), "setup-transition").textContent).toBe("Culler prepares the next setup operation.");
    expect(pulses(find(render(gap), "setup-transcript"))).toHaveLength(1);
    const accepted = completedView();
    accepted.environment.setup_status = "configured";
    expect(find(render(accepted), "setup-transition").textContent).toBe("Culler completes Project initialization.");
    expect(pulses(render(completedView()))).toHaveLength(0);
  });

  test("approval, errors, and unavailable status stop setup activity", () => {
    const pulses = (node: FakeNode) => node.all().filter(child => child.className === "journal-pulse");
    expect(pulses(render(proposalView()))).toHaveLength(0);
    for (const status of ["failed", "canceled", "succeeded"]) {
      const view = base({ operations: [operation("one", { status, error: "The service is unavailable." })] });
      view.environment.active_operation_id = "";
      expect(pulses(render(view))).toHaveLength(0);
      expect(setupJournalModel(view).state).toBe("Setup needs attention");
    }
    expect(pulses(render(base(), { unavailable: true }))).toHaveLength(0);
    const gap = base({ operations: [operation("one", { kind: "build", status: "succeeded" })] });
    gap.environment.active_operation_id = "";
    for (const request of [{ available: false }, { available: true, status: "failed" }]) {
      const health = { request, submissions: [] };
      expect(pulses(render(gap, { health }))).toHaveLength(0);
      expect(pulses(find(render(base(), { health }), "setup-operation"))).toHaveLength(1);
    }
    const health = { request: { available: true, status: "succeeded" }, submissions: [
      { project_id: "garden", available: true, status: "failed" },
    ] };
    expect(pulses(render(gap, { health }))).toHaveLength(0);
    health.submissions[0].project_id = "library";
    expect(pulses(render(base(), { health }))).toHaveLength(2);
  });

  test("a failed operation renders its Markdown cause once and keeps retry outside the transcript", () => {
    const message = "<script>literal error</script>\n**Source message**";
    const view = base({ operations: [operation("one", { status: "failed", error: message, metadata: { failure_details: "Diagnostic output." } })] });
    Object.assign(view.environment, { phase: "failed", cause: message, active_operation_id: "" });
    const node = render(view);
    expect(node.textContent.split("Source message")).toHaveLength(2);
    expect(find(node, "setup-operation-error").textContent).toContain("Source message");
    expect(find(node, "setup-operation-error").all().some(child => child.nodeName === "STRONG")).toBe(true);
    expect(find(node, "setup-transcript").textContent).not.toContain("Retry setup");
    expect(find(node, "setup-controls").textContent).toContain("Retry setup");
    expect(node.all().some(child => child.nodeName === "SCRIPT")).toBe(false);

  });

  test("the transcript reveals milestones only when setup reaches them", () => {
    const milestones = (node: FakeNode) => find(node, "setup-transcript").all()
      .filter(child => child.dataset.testid === "setup-milestone")
      .map(child => `${child.dataset.milestone}:${child.dataset.state}`);
    const active = render(base());
    expect(milestones(active)).toEqual(["vision:complete", "prepare:started"]);
    expect(active.textContent).not.toContain("Save Project files");
    expect(active.textContent).not.toContain("Ready for development");
    expect(active.all().some(child => child.nodeName === "ASIDE")).toBe(false);
    const view = completedView();
    view.environment.setup_status = "configured";
    view.environment.active_operation_id = "accept";
    view.operations[0].status = "running";
    expect(milestones(render(view))).toEqual(["vision:complete", "prepare:complete", "accept:started"]);
    const complete = render(completedView());
    expect(milestones(complete)).toEqual([
      "vision:complete", "prepare:complete", "accept:complete", "ready:complete",
    ]);
    const entries = find(complete, "setup-transcript").childNodes.map(child => child.dataset.milestone
      ? `${child.dataset.milestone}:${child.dataset.state}` : child.dataset.operation ? `operation:${child.dataset.operation}` : child.dataset.testid);
    expect(entries).toEqual([
      "vision:complete", "prepare:complete", "operation:one", "accept:complete",
      "operation:accept", "ready:complete", "setup-completion",
    ]);
  });

  test("a failed attempt and its retry remain visible without false milestone completion", () => {
    const view = base({ operations: [operation("retry", { metadata: { previous_operation_id: "one" } }),
      operation("one", { status: "failed", error: "The service is unavailable." })] });
    view.environment.active_operation_id = "retry";
    const transcript = find(render(view), "setup-transcript");
    expect(transcript.childNodes.filter(child => child.dataset.operation).map(child => child.dataset.operation)).toEqual(["one", "retry"]);
    expect(transcript.textContent).toContain("The service is unavailable.");
    expect(transcript.all().filter(child => child.dataset.milestone === "prepare").map(child => child.dataset.state)).toEqual(["started"]);
    const absent = completedView();
    absent.operations = [];
    const missing = find(render(absent), "setup-transcript");
    expect(missing.textContent).toContain("Evidence unavailable");
    expect(missing.all().filter(child => child.dataset.testid === "setup-milestone").map(child => child.dataset.milestone)).toEqual(["vision"]);
  });

  test("every setup state keeps action controls outside the transcript", () => {
    const proposal = proposalView();
    proposal.draft.explanation = "The Project needs [database tools](https://example.test).";
    const failed = base({ operations: [operation("one", { status: "failed", error: "The service is unavailable." })] });
    Object.assign(failed.environment, { phase: "failed", active_operation_id: "" });
    for (const view of [base(), proposal, failed, completedView()]) {
      const node = render(view);
      expect(node.all().some(child => child.dataset.testid === "setup-operation-evidence")).toBe(false);
      for (const label of ["Setup details", "View accepted Vision", "View result and evidence", "View accepted setup evidence"]) {
        expect(node.textContent).not.toContain(label);
      }
      const transcript = find(node, "setup-transcript");
      expect(transcript.all().filter(child => ["BUTTON", "INPUT", "TEXTAREA", "SELECT"].includes(child.nodeName))).toEqual([]);
    }
    expect(find(render(proposal), "setup-controls").all().some(child => child.dataset.testid === "setup-approve")).toBe(true);
  });

  test("stale shared evidence and submission failures preserve operation state and scope", () => {
    const health = { request: { available: false, status: "failed", message: "The image request failed.", observed_at: "2026-10-13T00:00:00Z" },
      submissions: [{ project_id: "garden", operation_id: "one", available: true, status: "failed", message: "The save failed.", result_error: "The harness stopped." },
        { project_id: "library", operation_id: "two", available: true, status: "failed", message: "Another Project error." }] };
    const node = render(base(), { health });
    expect(find(node, "setup-health").textContent).toContain("Last recorded setup error");
    expect(find(node, "setup-health").textContent).toContain("Setup status unavailable");
    expect(find(node, "setup-submission-error").textContent).toContain("The harness stopped.");
    expect(node.textContent).not.toContain("Another Project error.");
    expect(setupJournalModel(base(), { health }).operations[0].operation.status).toBe("running");
    const unavailable = render(null, { project: { id: "garden" }, unavailable: true, health });
    expect(find(unavailable, "setup-health").textContent).toContain("The image request failed.");
  });

  test("an absent failure message explains the lack of output", () => {
    const node = render(base({ operations: [operation("one", { status: "failed" })] }));
    expect(find(node, "setup-operation-error").textContent).toContain("The operation failed without an error message.");
  });

  test("the suggested setup states its purpose, places the summary beside the Dockerfile, and shows the setup script", () => {
    const node = render(proposalView());
    const card = find(node, "setup-proposal");
    expect(find(card, "setup-proposal-purpose").textContent).toBe("Each agent of this Project starts in a container with this setup. The setup lets the agent change the repository and run its tests.");
    const image = find(card, "setup-proposal-image");
    expect(image.childNodes[0].textContent).toBe("Project image");
    expect(find(image, "setup-proposal-summary").textContent).toBe("Install the database headers. The Project needs them for its checks.");
    expect(image.all().some(child => child.dataset.testid === "setup-dockerfile")).toBe(true);
    const operation = find(node, "setup-operation");
    expect(operation.textContent).toContain("The setup agent suggests a change to the Project image. Review the suggested setup below.");
    expect(operation.textContent).not.toContain("Install the database headers.");
    const part = find(card, "setup-proposal-script");
    expect(part.childNodes[0].textContent).toBe("Setup script");
    expect(part.childNodes[1].textContent).toBe("Culler runs this script in each agent container before the agent starts.");
    const script = find(part, "setup-script");
    expect(script.dataset).toMatchObject({ path: "Setup script", change: "Added" });
    expect(script.all().find(child => child.className === "file-view-head")?.textContent).toBe("BashAdded");

    const scriptless = proposalView();
    scriptless.draft.configuration.setup = { command: "", directory: "." };
    expect(render(scriptless).all().some(child => child.dataset.testid === "setup-proposal-script")).toBe(false);
    const nested = proposalView();
    nested.draft.configuration.setup = { command: "npm ci", directory: "web" };
    expect(find(render(nested), "setup-proposal-script").childNodes[1].textContent)
      .toBe("Culler runs this script in each agent container before the agent starts. The script starts in the web directory of the repository.");
  });

  test("current approval shows each file in a file viewer, hides legacy prose, and submits only its displayed revision", () => {
    const view = proposalView();
    view.draft.explanation = "## Diagnostic results\n\n" + "The tests require an additional image tool. ".repeat(40);
    let approved = "", edits = 0;
    const node = render(view, {}, { approve: (id: string) => { approved = id; }, editDockerfile: () => { edits++; } });
    expect(find(node, "setup-proposal").className).toBe("journal-proposal journal-approval");
    expect(find(node, "setup-proposal").childNodes[0].childNodes[0].textContent).toBe("Suggested setup");
    const edit = find(node, "setup-edit-dockerfile");
    expect(edit.textContent).toBe("Edit Dockerfile");
    edit.listeners.click();
    expect(edits).toBe(1);
    for (const text of ["Proposal revision:", "Base image", "Dependency configuration", "Setup directory"]) {
      expect(find(node, "setup-proposal").textContent).not.toContain(text);
    }
    const dockerfile = find(node, "setup-dockerfile");
    expect(dockerfile.dataset).toMatchObject({ path: ".culler/Dockerfile", change: "Added" });
    expect(dockerfile.all().find(child => child.className === "file-view-head")?.textContent).toBe(".culler/DockerfileAdded");
    const build = node.all().find(child => child.getAttribute("data-journal-key") === "proposal:file:.culler/setup.md")!;
    expect(build.getAttribute("open")).toBeNull();
    expect(build.childNodes[0].textContent).toBe("Added: .culler/setup.md");
    expect(build.all().find(child => child.dataset.path === ".culler/setup.md")?.dataset.change).toBe("Added");
    const details = node.all().find(child => child.getAttribute("data-journal-key") === "proposal:proposal-details")!;
    expect(details.getAttribute("open")).toBeNull();
    expect(details.textContent).toContain("Diagnostic results");
    const approve = find(node, "setup-approve");
    expect(approve.textContent).toBe("Approve changes and continue");
    approve.listeners.click();
    expect(approved).toBe("proposal");
    expect(node.all().some(child => child.nodeName === "TEXTAREA")).toBe(false);
  });

  test("a refresh keeps each file viewer, and a changed file gets a new viewer", () => {
    const view = proposalView();
    const first = find(render(view), "setup-dockerfile");
    expect(find(render(view), "setup-dockerfile")).toBe(first);
    view.draft.configuration = configuration("FROM retained-base\nRUN install-another-tool\n");
    const changed = find(render(view), "setup-dockerfile");
    expect(changed).not.toBe(first);
    expect(changed.dataset.key).not.toBe(first.dataset.key);
  });

  test("a base image proposal preserves its approval action without a Dockerfile", () => {
    const view = proposalView();
    view.draft.configuration.dockerfile = null;
    view.draft.configuration.build_files = [];
    const node = render(view);
    expect(node.textContent).toContain("This proposal uses the selected base image without a Dockerfile.");
    expect(node.textContent).not.toContain(view.draft.configuration.base_digest);
    expect(find(node, "setup-approve").getAttribute("disabled")).toBeNull();
  });

  test("failed reads preserve evidence and disable approval without active indicators", () => {
    const node = render(proposalView(), { unavailable: true, lastReadAt: "2026-10-12T12:01:00Z" });
    expect(find(node, "setup-read-error").textContent).toContain("Last successful read:");
    expect(find(node, "setup-approve").getAttribute("disabled")).toBe("true");
    expect(find(node, "setup-dockerfile")).toBeDefined();
    expect(node.all().some(child => child.className === "journal-pulse")).toBe(false);
    const first = render(null, { unavailable: true });
    expect(first.textContent).toContain("Setup status unavailable");
    expect(first.all().some(child => child.dataset.testid === "setup-retry")).toBe(false);
  });

  test("a long report keeps its short summary visible and its Markdown details collapsed", () => {
    const view = completedView();
    Object.assign(view.results[0], { summary: "Preparation is complete with platform limits.", details:
      "## Available tools\n\n- The compiler works.\n- The tests remain **unverified**.\n\n```text\n<script>exact output</script>\n```" });
    const node = render(view);
    const message = find(node, "setup-operation");
    expect(message.textContent).toContain(view.results[0].summary);
    const details = message.all().find(child => child.getAttribute("data-journal-key") === "one:report")!;
    expect(details.getAttribute("open")).toBeNull();
    expect(details.all().some(child => child.nodeName === "STRONG" && child.textContent === "unverified")).toBe(true);
    expect(details.all().some(child => child.nodeName === "LI")).toBe(true);
    expect(details.all().some(child => child.nodeName === "CODE" && child.textContent.includes("<script>"))).toBe(true);
    expect(node.all().some(child => child.nodeName === "SCRIPT")).toBe(false);
  });

  test("the shared Markdown renderer supports prose without executable links or embedded content", () => {
    const nodes = markdownNodes(parseMarkdown("## Tools\n\nUse **Git** and _Python_ with `setup`.\n\n- One\n- Two\n\n1. First\n2. Second\n\n> A note\n\n```sh\necho '<b>literal</b>'\n```\n\n[Safe](https://example.test) [Unsafe](javascript:alert) <img src='https://tracker.test'>")) as unknown as FakeNode[];
    const elements = nodes.flatMap(node => node.all());
    for (const name of ["H2", "STRONG", "EM", "CODE", "UL", "OL", "BLOCKQUOTE", "PRE"]) expect(elements.some(node => node.nodeName === name)).toBe(true);
    expect(elements.filter(node => node.nodeName === "A").map(node => node.getAttribute("href"))).toEqual(["https://example.test"]);
    expect(elements.some(node => ["IMG", "SCRIPT", "IFRAME", "BUTTON"].includes(node.nodeName))).toBe(false);
    expect(nodes.map(node => node.textContent).join("")).toContain("[Unsafe](javascript:alert)");
  });
});

test("Markdown links reject executable and application protocols", () => {
  for (const href of ["javascript:alert(1)", "JAVASCRIPT:alert", "java\nscript:alert", "data:text/html,<script>", "file:///tmp/tool", "vscode:command", "mailto:person@example.test", "\\evil.test", " https://safe.test"]) {
    expect(safeMarkdownHref(href)).toBeNull();
  }
  for (const href of ["https://example.test", "http://localhost:7333", "/docs/setup", "#section", "../README.md"]) {
    expect(safeMarkdownHref(href)).toBe(href);
  }
});


test("a short proposal preserves tool versions and file paths in its summary", () => {
  const text = "The image adds Go 1.25.12 and Bun 1.3.9. The Project needs them for ./check.sh.";
  expect(setupProposalSummary(text)).toEqual({ text, details: "" });
});

test("a failed health read retains the last observation until a complete response", () => {
  const prior = { request: { available: true, status: "failed", message: "The request failed.", observed_at: "2026-10-13T00:00:00Z" }, submissions: [] };
  const stale = acceptSetupHealth(prior, null);
  expect(stale.request).toEqual({ ...prior.request, available: false });
  expect(acceptSetupHealth(prior, { setup: { request: { available: false }, submissions: [] } }).request).toEqual(stale.request);
  const recovered = { request: { available: true, status: "succeeded", observed_at: "2026-10-13T00:01:00Z" }, submissions: [] };
  expect(acceptSetupHealth(stale, { setup: recovered })).toEqual(recovered);
});

test("retained structured logs remain evidence instead of an invented error", () => {
  const raw = 'The setup container could not start: {"level":"info","event":"runner.skill_installed","msg":"Installed four skills."}';
  const failure = setupFailureDisplay(raw);
  expect(failure.message).toBe("The operation failed without an error message.");
  expect(failure.details).toBe(raw);
  const structured = setupFailureDisplay('{"error":{"code":"environment","message":"The image is unavailable."}}');
  expect(structured.message).toBe("The image is unavailable.");
  expect(structured.details).toContain('"code":"environment"');
  expect(setupFailureDisplay("The path {source} is unavailable.").message).toBe("The path {source} is unavailable.");
});

test("approval of an edited proposal retains both exact revisions", () => {
  const view = proposalView();
  const edited = revision("edited", { configuration: configuration("FROM retained-base\nRUN install-another-tool\n") });
  view.draft = edited;
  view.revisions.push(edited);
  view.environment.draft_revision_id = edited.id;
  view.environment.phase = "build";
  view.environment.active_operation_id = "build";
  view.decisions = [{ revision_id: edited.id, operation_id: "one", decision: "approved" }];
  view.approvals = [{ revision_id: edited.id, created_at: "2026-10-12 12:01:00" }];
  const row = setupJournalModel(view).operations[0];
  expect(row.outcome.text).toBe("Image changes approved");
  expect(row.proposal.id).toBe("proposal");
  expect(row.decidedRevision.configuration).toEqual(edited.configuration);
  expect(row.decision.operation_id).toBe("one");
});
