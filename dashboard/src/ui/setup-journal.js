import { el, markdownNodes } from "./dom.js";
import { parseMarkdown } from "./markdown.js";
import { createFileViewers, proposalFiles, setupScriptFile } from "./fileview.js";
import { traceLink, viewerLinkNode } from "./viewers.js";
import { diagnosticButton, setupDiagnostic } from "./diagnosticchat.js";

const AGENT_KINDS = new Set(["setup", "validate"]);
const TITLES = {
  setup: "Prepare the Project environment", build: "Prepare the Project image",
  validate: "Continue Project preparation", accept: "Save Project files",
  "runtime-check": "Check the Project runtime", "cache-clear": "Clear the Project cache",
};
const MILESTONES = {
  vision: "Vision accepted", prepare: "Prepare Project environment",
  accept: "Save Project files", ready: "Ready for development",
};
const SETUP_LEAD = "The setup agent downloads and installs the Project's dependencies, then builds the Project and runs its tests.";
const string = value => typeof value === "string" ? value : "";
const errorFallback = "The operation failed without an error message.";
const literal = (text, props = {}) => el("p", { class: "journal-error", text, ...props });
const qualifies = result => result?.outcome === "complete";
const prose = (text, props = {}) => el("div", { class: "md journal-prose", ...props }, markdownNodes(parseMarkdown(text)));
const detail = (key, title, children, open = false) => el("details", {
  class: "journal-details", "data-journal-key": key, open,
}, el("summary", { text: title }), ...children);
const fileViewers = createFileViewers();

export function setupFailureDisplay(message, details = "") {
  const source = string(message);
  const start = source.indexOf("{");
  if (start >= 0) {
    try {
      const record = JSON.parse(source.slice(start));
      const text = record.event === "infrastructure-failure" ? string(record.condition)
        : string(record.error?.message) || (record.level === "error" ? string(record.msg) : "");
      return { message: text || errorFallback, details: [source, string(details)].filter(Boolean).join("\n\n") };
    } catch {}
  }
  return { message: source.trim() ? source : errorFallback, details: string(details) };
}

export function setupProposalSummary(explanation) {
  const original = string(explanation);
  const value = original.trim();
  const blocks = parseMarkdown(value);
  const sentences = [...new Intl.Segmenter("en", { granularity: "sentence" }).segment(value)].map(row => row.segment);
  const concise = value.length > 0 && value.length <= 320 && blocks.length === 1
    && blocks[0].type === "paragraph" && sentences.length <= 2
    && sentences.every(sentence => sentence.trim().split(/\s+/).length <= 25);
  return {
    text: concise ? value : "The setup agent proposes Image dependencies for this Project. Review the exact Dockerfile and required build files below.",
    details: concise ? "" : original,
  };
}

function operationOutcome(operation, result, proposal, approval, decision) {
  if (operation.status === "queued") return { key: "queued", text: "Queued" };
  if (operation.status === "running") return { key: "active", text: "Active" };
  if (operation.status === "failed") return { key: "failed", text: "Execution failed" };
  if (operation.status === "canceled") return { key: "canceled", text: "Canceled" };
  if (operation.status !== "succeeded") return { key: "unknown", text: "Result unavailable" };
  if (operation.error) return { key: "failed", text: "Execution failed" };
  if (decision?.decision === "denied") return { key: "denied", text: "Current image retained" };
  if (proposal) return approval
    ? { key: "approved", text: "Image changes approved" }
    : { key: "proposal", text: "Image proposal" };
  if (result?.outcome === "complete") return { key: "complete", text: "Preparation complete" };
  if (operation.kind === "accept") return { key: "complete", text: "Files accepted" };
  if (operation.kind === "build") return { key: "complete", text: "Image prepared" };
  if (operation.kind === "runtime-check") return { key: "complete", text: "Runtime check complete" };
  if (operation.kind === "cache-clear") return { key: "complete", text: "Cache removed" };
  return { key: "unknown", text: "Result unavailable" };
}

function operationSummary(row) {
  // The suggested setup shows the summary of the current proposal beside its Dockerfile.
  if (row.currentProposal) return "The setup agent suggests a change to the Project image. Review the suggested setup below.";
  if (row.result?.summary) return row.result.summary;
  if (row.outcome.key === "queued" && row.operation.attempt > 1) return "Culler will retry this operation automatically. Setup continues after the retry succeeds.";
  if (row.outcome.key === "queued") return row.agent
    ? "The setup agent awaits launch." : "Culler will start this authorized operation.";
  if (row.outcome.key === "active") return {
    setup: "The setup agent prepares Workspace dependencies and examines the Project.",
    validate: "The setup agent continues preparation in the selected image.",
    build: "Culler prepares the Project image from the approved Dockerfile and build files.",
    accept: "Culler saves the checked Project files and their environment association.",
  }[row.operation.kind] ?? "Culler executes this operation.";
  if (row.proposal) return row.approval
    ? "The operator approved this proposal for Image dependencies."
    : "The setup agent proposed Image dependencies.";
  if (row.outcome.key === "failed") return "This operation stopped before completion. Setup requires a successful attempt to continue.";
  if (row.outcome.key === "canceled") return "Culler canceled this operation.";
  if (row.outcome.key === "unknown") return "The retained records do not establish this operation's result.";
  return {
    setup: "The setup agent finished the available preparation.",
    validate: "The setup agent finished preparation and reported its limits.",
    build: "Culler prepared the approved Project image and checked its runtime compatibility.",
    accept: "Culler saved the checked Project files.",
  }[row.operation.kind] ?? "Culler completed this operation.";
}

export function acceptSetupHealth(previous, response) {
  const historical = observation => ({ ...observation, available: false });
  const incoming = response?.setup;
  const complete = incoming?.request?.observed_at && ["succeeded", "failed"].includes(incoming.request.status);
  return {
    request: complete ? incoming.request : historical(previous?.request),
    submissions: Array.isArray(incoming?.submissions) && (complete || incoming.submissions.length)
      ? incoming.submissions : (previous?.submissions ?? []).map(historical),
  };
}

export function initialSetupComplete(view) {
  return view?.environment?.setup_status === "ready" && (view?.operations ?? []).some(op => op.kind === "accept"
    && op.status === "succeeded" && op.metadata?.initial_setup === true && !op.metadata?.candidate);
}

/** The journal opens on request after initial setup completes or selects an environment. */
export function setupHistoryAvailable(view) {
  const env = view?.environment;
  return Boolean(env?.setup_id) && (initialSetupComplete(view) || Boolean(env.selected_revision_id));
}

export function setupJournalModel(view, { project, unavailable = false, lastReadAt = "", health } = {}) {
  const env = view?.environment;
  const model = {
    exists: Boolean(env), projectID: env?.project_id || project?.id || "",
    title: env?.project_title || project?.title || "Project", unavailable, lastReadAt,
    completed: false, historicalEvidenceMissing: false, operations: [], entries: [], approval: null,
    progressing: !unavailable, state: unavailable ? "Setup status unavailable" : "Setup in progress",
  };
  model.health = health ? { request: health.request, submissions: (health.submissions ?? []).filter(row => row.project_id === model.projectID) } : null;
  const healthUnavailable = model.health && [model.health.request, ...model.health.submissions].some(row => !row?.available);
  const healthFailed = model.health && [model.health.request, ...model.health.submissions].some(row => row?.status === "failed");
  model.progressing &&= !healthUnavailable && !healthFailed;
  if (!env) {
    if (healthUnavailable) model.state = "Setup status unavailable";
    else if (healthFailed) model.state = "Setup needs attention";
    return model;
  }
  // The read response uses descending record order. Reversal preserves ties.
  const records = [...(view.operations ?? [])].reverse();
  const acceptanceIndex = records.findIndex(op => op.kind === "accept" && op.status === "succeeded"
    && op.metadata?.initial_setup === true && !op.metadata?.candidate);
  const acceptance = acceptanceIndex < 0 ? null : records[acceptanceIndex];
  const initialized = (env.setup_status ?? project?.setup_status) === "ready";
  model.completed = Boolean(acceptance && initialized);
  model.historicalEvidenceMissing = initialized && !acceptance;
  const operations = model.historicalEvidenceMissing ? [] : acceptance ? records.slice(0, acceptanceIndex + 1) : records;
  const revisions = new Map((view.revisions ?? []).map(row => [row.id, row]));
  for (const row of [view.draft, view.selected]) if (row && !revisions.has(row.id)) revisions.set(row.id, row);
  const decisions = new Map((view.decisions ?? []).map(row => [row.revision_id, row]));
  const approvals = new Map((view.approvals ?? []).map(row => [row.revision_id, row]));
  const results = new Map((view.results ?? []).map(row => [row.id, row]));
  const resultFor = operation => (view.results ?? []).find(row => row.operation_id === operation.id);
  const latest = operations.at(-1);
  const acceptanceResult = results.get(acceptance?.metadata?.result_id) ?? null;
  const acceptedRevision = acceptance ? revisions.get(acceptance.revision_id) ?? null : null;
  const draft = view.draft?.id === env.draft_revision_id ? view.draft : null;
  const needsApproval = !acceptance && !model.historicalEvidenceMissing && env.phase === "approval"
    && draft && !approvals.has(draft.id) && !decisions.has(draft.id) && !env.active_operation_id;
  const currentID = env.active_operation_id || (needsApproval
    ? operations.find(op => op.metadata?.proposed_revision_id === draft.id || draft.editor === `setup:${op.id}`)?.id
    : latest?.id);

  const acceptedBefore = operation => {
    const index = records.findIndex(row => row.id === operation.id);
    const accepted = records.slice(0, index).findLast(row => row.kind === "accept" && row.status === "succeeded" && !row.metadata?.candidate);
    return accepted ? revisions.get(accepted.revision_id)?.configuration ?? null : null;
  };
  model.operations = operations.map(operation => {
    const result = resultFor(operation) ?? null;
    const proposedID = operation.metadata?.proposed_revision_id;
    const reported = operation.metadata?.report_outcome;
    const authored = [...revisions.values()].find(revision => revision.editor === `setup:${operation.id}`);
    const proposal = reported === "proposal" ? revisions.get(proposedID) ?? null : null;
    const decision = proposal ? decisions.get(proposal.id) ?? (view.decisions ?? []).find(row => row.operation_id === operation.id) ?? null : null;
    const approval = proposal ? approvals.get(decision?.revision_id ?? proposal.id) ?? null : null;
    const decidedRevision = decision && decision.revision_id !== proposal?.id ? revisions.get(decision.revision_id) ?? null : null;
    const row = {
      id: operation.id, operation, result, proposal, approval, decidedRevision, decision, acceptedConfiguration: acceptedBefore(operation),
      revision: result ? revisions.get(result.revision_id) ?? null : revisions.get(operation.revision_id) ?? null,
      explanation: result && authored ? authored.explanation : "",
      title: TITLES[operation.kind] ?? "Environment operation",
      agent: AGENT_KINDS.has(operation.kind), actor: AGENT_KINDS.has(operation.kind) ? "Setup agent" : "Culler",
      // The AgentsView session of the agent trace, as `mc` computes it after the runner registers the session.
      trace: operation.trace ?? null,
      current: !acceptance && operation.id === currentID,
      currentProposal: Boolean(needsApproval && proposal?.id === draft.id),
      active: !unavailable && operation.id === env.active_operation_id && operation.status === "running" && !acceptance,
      outcome: operationOutcome(operation, result, proposal, approval, decision),
    };
    row.summary = operationSummary(row);
    return row;
  });
  model.approval = needsApproval ? {
    revision: draft, canDeny: model.operations.some(row => row.proposal), operationID: model.operations.find(row => row.currentProposal)?.id ?? null,
    acceptedConfiguration: model.operations.find(row => row.currentProposal)?.acceptedConfiguration ?? null,
  } : null;
  model.completion = acceptance ? {
    operation: acceptance, revision: acceptedRevision, result: acceptanceResult,
    sourceCommit: string(acceptance.metadata?.source_commit), completedAt: string(acceptance.ended_at),
  } : null;
  model.vision = view.vision?.revision === env.vision_revision ? view.vision : null;
  model.visionRevision = string(env.vision_revision);
  model.retry = !acceptance && !model.historicalEvidenceMissing && env.phase === "failed" && !env.active_operation_id;
  model.cause = !acceptance && !model.historicalEvidenceMissing ? string(env.cause) : "";
  model.phase = env.phase;
  const current = model.operations.find(row => row.current);
  const pending = current && ["queued", "running"].includes(current.operation.status);
  model.attention = ["approval", "failed"].includes(env.phase) || Boolean(!pending && healthFailed)
    || Boolean(!pending && current && ["blocked", "failed", "canceled", "unknown"].includes(current.outcome.key));
  model.progressing = !unavailable && Boolean(pending || !healthUnavailable && !healthFailed)
    && !model.completed && !model.historicalEvidenceMissing && !model.attention;
  model.state = model.historicalEvidenceMissing ? "Evidence unavailable" : model.completed ? "Ready for development"
    : unavailable || !pending && healthUnavailable ? "Setup status unavailable"
      : needsApproval ? "Approval required" : model.attention ? "Setup needs attention" : "Setup in progress";
  model.entries = journalEntries(model);
  return model;
}

function facts(rows) {
  return el("dl", { class: "environment-facts journal-facts" }, rows.filter(([, value]) => value !== "" && value != null)
    .flatMap(([label, value]) => [el("dt", { text: label }), el("dd", { text: value })]));
}

function dateText(value) {
  const text = string(value);
  if (!text) return "";
  const date = new Date(text.includes("T") ? text : `${text.replace(" ", "T")}Z`);
  return Number.isNaN(date.getTime()) ? text : date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

function statusNode(row, model) {
  return el("span", { class: `journal-status ${row.active ? "active" : row.outcome.key}` },
    model.progressing && row.current && ["queued", "running"].includes(row.operation.status)
      && el("span", { class: "journal-pulse", "aria-hidden": "true" }),
    el("span", { text: model.unavailable && row.current && row.operation.status === "running" ? "Last known: active" : row.outcome.text }));
}

function fileNode(file, key, primary = false) {
  const viewer = fileViewers.node(file, { key: `${key}:${primary ? "dockerfile" : "build"}:${file.path}`, testid: primary ? "setup-dockerfile" : null });
  return primary ? viewer : detail(`${key}:file:${file.path}`, `${file.change}: ${file.path}`, [viewer]);
}

function scriptLead(config) {
  const directory = string(config.setup?.directory);
  return "Culler runs this script in each agent container before the agent starts."
    + (directory && directory !== "." ? ` The script starts in the ${directory} directory of the repository.` : "");
}

function approvalNode(revision, { approved, decision, current = false, unavailable = false, busy, approve, deny, editDockerfile, actionError }, before = null) {
  const config = revision.configuration ?? {};
  const summary = setupProposalSummary(revision.explanation);
  const { dockerfile, buildFiles } = proposalFiles(before, config);
  const script = setupScriptFile(before, config);
  return el("section", { class: `journal-proposal${current ? " journal-approval" : ""}`, data: { revision: revision.id }, testid: "setup-proposal" },
    el("div", { class: "journal-proposal-head" },
      el("h3", { text: current ? "Suggested setup" : "Image dependencies" }),
      current && editDockerfile && config.dockerfile && el("button", { type: "button", class: "btn small", testid: "setup-edit-dockerfile",
        text: "Edit Dockerfile", onclick: editDockerfile })),
    el("p", { class: "journal-proposal-purpose", testid: "setup-proposal-purpose",
      text: "Each agent of this Project starts in a container with this setup. The setup lets the agent change the repository and run its tests." }),
    el("section", { class: "journal-proposal-part", testid: "setup-proposal-image" },
      el("h4", { text: "Project image" }),
      prose(summary.text, { testid: "setup-proposal-summary" }),
      dockerfile ? fileNode(dockerfile, revision.id, true)
        : prose(revision.configuration ? "This proposal uses the selected base image without a Dockerfile." : "Evidence unavailable. The proposal configuration is absent."),
      buildFiles.length > 0 && el("section", { class: "journal-build-files" },
        el("h4", { text: "Required build files" }), buildFiles.map(file => fileNode(file, revision.id))),
      Object.keys(config.build_args ?? {}).length > 0 && el("section", {},
        el("h4", { text: "Build arguments" }), facts(Object.entries(config.build_args)))),
    script && el("section", { class: "journal-proposal-part", testid: "setup-proposal-script" },
      el("h4", { text: "Setup script" }),
      el("p", { class: "journal-proposal-lead", text: scriptLead(config) }),
      fileViewers.node(script, { key: `${revision.id}:setup-script`, testid: "setup-script" })),
    summary.details && detail(`${revision.id}:proposal-details`, "Proposal details", [prose(summary.details)]),
    approved && facts([["Approval", "Approved"], ["Approved at", dateText(approved.created_at)]]),
    decision?.decision === "denied" && facts([["Decision", "Continue with current image"], ["Decided at", dateText(decision.created_at)], ["Current image", decision.current_image_digest]]),
    current && el("div", { class: "journal-approval-action" },
      prose("Culler continues setup with your image choice. The agent reports any limits, then Culler imports the Project."),
      el("button", { type: "button", class: "btn primary", testid: "setup-approve", text: "Approve changes and continue",
        disabled: busy || unavailable || !revision.configuration?.base_digest, onclick: () => approve?.(revision.id) }),
      deny && el("button", { type: "button", class: "btn", testid: "setup-deny", text: "Continue with current image",
        disabled: busy || unavailable, onclick: () => deny?.(revision.id) }),
      actionError && el("p", { class: "journal-action-error", role: "alert", testid: "setup-action-error", text: actionError })));
}

function technicalDetails(key, text) {
  return detail(`${key}:technical`, "Technical details", [text
    ? el("pre", { class: "journal-code journal-diagnostic", tabindex: "0", "aria-label": "Technical details", text })
    : el("p", { text: "Culler captured no diagnostic output." })]);
}

function retryNode(model, actions) {
  return el("div", { class: "journal-retry", testid: "setup-recovery" },
    el("p", { text: "Select Retry setup to try this operation again." }),
    el("button", { type: "button", class: "btn primary", text: "Retry setup", testid: "setup-retry",
      disabled: actions.busy || model.unavailable, onclick: actions.retry }),
    actions.actionError && el("p", { class: "journal-action-error", role: "alert", testid: "setup-action-error", text: actions.actionError }));
}

/** The Ask Collie action of the current setup failure. The action stays with
 * the setup controls because the stream contains no action controls. */
function diagnosticNode(model, ask) {
  const row = model.operations.find(row => row.current && (row.operation.error || row.operation.status === "failed"));
  if (row) {
    const failure = setupFailureDisplay(row.operation.error, row.operation.metadata?.failure_details);
    return diagnosticButton(setupDiagnostic(model.projectID, row.operation, row.title, failure.message), ask, "setup-ask-collie");
  }
  return model.retry
    ? diagnosticButton(setupDiagnostic(model.projectID, null, "Setup needs attention", model.cause || errorFallback), ask, "setup-ask-collie")
    : null;
}

function healthNode(observation, key, submission = false, operationHasError = false) {
  if (observation?.available && observation.status === "succeeded") return null;
  const stale = !observation?.available;
  return el("section", { class: "journal-health", testid: submission ? "setup-submission-error" : "setup-health", role: "status" },
    el("h2", { text: stale ? "Setup status unavailable" : submission ? "Culler could not save the operation result" : "Culler could not advance setup" }),
    stale && el("p", { text: "Culler cannot confirm the current setup status. The recorded operation states remain unchanged." }),
    observation?.message && el("div", {},
      stale && el("strong", { text: "Last recorded setup error" }),
      literal(observation.message)),
    observation?.observed_at && el("p", { class: "journal-muted", text: `Last observation: ${dateText(observation.observed_at)}` }),
    el("p", { text: submission ? "Culler retains the operation result and retries the save automatically."
      : "Culler repeats the setup request automatically." }),
    submission && !operationHasError && observation.result_error && el("div", {},
      el("h3", { text: "Operation error" }), literal(observation.result_error),
      technicalDetails(`${key}:result`, observation.result_details)),
    technicalDetails(key, observation?.details));
}

function journalEntries(model) {
  const entries = [];
  const milestones = new Map();
  const milestone = (id, state) => {
    const existing = milestones.get(id);
    if (existing) {
      existing.state = state;
      return;
    }
    const entry = { kind: "milestone", id, state, key: `milestone:${id}`, title: MILESTONES[id] };
    milestones.set(id, entry);
    entries.push(entry);
  };
  if (model.visionRevision) milestone("vision", "complete");
  if (model.historicalEvidenceMissing) return entries;
  for (const row of model.operations) {
    const acceptance = row.operation.kind === "accept" && row.operation.metadata?.initial_setup === true
      && !row.operation.metadata?.candidate;
    const next = acceptance ? "accept" : "prepare";
    milestone(next, "started");
    entries.push({ kind: "operation", row, key: row.id });
    if (next === "prepare" && row.outcome.key === "complete" && row.result && qualifies(row.result)) {
      milestone("prepare", "complete");
    }
    if (acceptance && row.operation.status === "succeeded") milestone("accept", "complete");
  }
  if (!model.operations.length) milestone("prepare", "started");
  if (model.completed) milestone("ready", "complete");
  return entries;
}

function milestoneNode(entry) {
  return el("div", { class: `journal-milestone ${entry.state}`, testid: "setup-milestone",
    data: { milestone: entry.id, state: entry.state, key: entry.key } },
    el("span", { class: "journal-milestone-mark", "aria-hidden": "true", text: entry.state === "complete" ? "✓" : "○" }),
    el("span", { class: "journal-milestone-title", text: entry.title }),
    el("span", { class: "journal-milestone-state", text: entry.state === "complete" ? "Complete" : "Started" }));
}

const messageProse = text => prose(text);

/** The AgentsView link of a setup agent operation, or null while the
 * operation has no registered trace. */
function traceNode(row, viewers, pending) {
  const link = row.agent ? traceLink(row.trace, viewers, pending) : null;
  if (link === null) return null;
  return viewerLinkNode({ text: "Trace", label: `Trace of setup operation ${row.id}`, testid: "setup-trace-link", link, className: "trace-link" });
}

function operationMessage(row, model, viewers, pending) {
  const operation = row.operation;
  const trace = traceNode(row, viewers, pending);
  const failure = setupFailureDisplay(operation.error, operation.metadata?.failure_details);
  return el("article", { class: `journal-message${row.current ? " current" : ""}`, id: `setup-operation-${row.id}`,
    testid: "setup-operation", data: { operation: row.id, key: row.id } },
    el("div", { class: "journal-message-meta" },
      el("span", { class: "journal-actor", text: row.actor }),
      statusNode(row, model),
      trace,
      trace && el("span", { class: "journal-muted", text: "See what the agent is up to" })),
    el("h3", { text: row.title }),
    messageProse(row.summary),
    row.result?.details && detail(`${row.id}:report`, "Report details", [prose(row.result.details)]),
    row.decision?.decision === "denied" && messageProse("The operator chose the current image. Culler continues import with that image."),
    (operation.error || operation.status === "failed") && el("div", { class: "journal-failure", testid: "setup-operation-error" },
      prose(failure.message)),
    operation.status === "queued" && operation.retry_at && el("p", { class: "journal-muted", text: `Automatic retry after ${dateText(operation.retry_at)}` }));
}

/** `viewers` holds the viewer facts of the status response and whether
 * the first status response is still pending. */
export function setupJournalNode(model, actions = {}, { viewers = null, pending = false } = {}) {
  const { environment, openBoard, retryRead, busy = false } = actions;
  fileViewers.begin();
  const header = el("header", { class: "journal-heading" },
    el("div", {}, el("p", { class: "project-eyebrow", text: model.title }),
      el("h1", { text: "Project setup", tabindex: "-1" }),
      !model.completed && !model.historicalEvidenceMissing && el("p", { class: "journal-lead", testid: "setup-lead", text: SETUP_LEAD })),
    el("div", { class: "journal-header-actions" },
      el("span", { class: `journal-state${model.completed ? " complete" : model.attention ? " attention" : ""}`,
        testid: "setup-phase", role: "status" },
        model.progressing && el("span", { class: "journal-pulse", "aria-hidden": "true" }),
        el("span", { text: model.state })),
      el("button", { type: "button", class: "btn small", text: "Open Environment", onclick: environment })));
  const notices = model.unavailable ? el("div", { class: "journal-read-state", role: "status", testid: "setup-read-error" },
    el("div", {}, el("strong", { text: model.exists ? "Last known setup data" : "Setup status unavailable" }),
      model.lastReadAt && el("p", { text: `Last successful read: ${dateText(model.lastReadAt)}` })),
    el("button", { type: "button", class: "btn small", text: "Retry", onclick: retryRead, disabled: busy })) : null;
  const health = model.health && [healthNode(model.health.request, "setup-request"),
    ...model.health.submissions.map(item => healthNode(item, `submission:${item.operation_id}`, true,
      Boolean(model.operations.find(row => row.id === item.operation_id)?.operation.error)))];
  if (!model.exists) return el("section", { class: "setup-journal", testid: "setup-panel", "aria-label": "Project setup" },
    header, el("div", { class: "journal-content" }, health, notices ?? el("p", { role: "status", text: "Culler reads the setup records…" })));
  const unexplainedFailure = model.retry && !model.operations.some(row => row.current && (row.operation.error || row.operation.status === "failed"));
  return el("section", { class: "setup-journal", testid: "setup-panel", "aria-label": "Project setup", data: { project: model.projectID } },
    header,
    el("div", { class: "journal-content", testid: "setup-journal" },
      notices, health,
      el("div", { class: "journal-entries", testid: "setup-transcript", role: "region", "aria-label": "Setup progress" },
        model.entries.map(entry => entry.kind === "milestone" ? milestoneNode(entry) : operationMessage(entry.row, model, viewers, pending)),
        model.historicalEvidenceMissing ? el("section", { class: "journal-message", testid: "setup-evidence-unavailable" },
          el("h2", { text: "Evidence unavailable" }),
          messageProse("Culler has no retained acceptance record for this Project's initial setup. The current environment does not establish that history."))
          : !model.operations.length && el("section", { class: "journal-message" },
            el("div", { class: "journal-message-meta" },
              el("p", { class: "journal-actor", text: "Culler" }),
              model.progressing && el("span", { class: "journal-status", role: "status" },
                el("span", { class: "journal-pulse", "aria-hidden": "true" }), el("span", { text: "Queued" }))),
            el("h2", { text: model.approval ? "Image changes require approval" : "Setup awaits an agent" }),
            messageProse(model.approval ? setupProposalSummary(model.approval.revision.explanation).text : "Culler will start the authorized setup operation.")),
        model.progressing && model.operations.length > 0
          && !model.operations.some(row => row.current && ["queued", "running"].includes(row.operation.status))
          && el("p", { class: "journal-status journal-transition", role: "status", testid: "setup-transition" },
            el("span", { class: "journal-pulse", "aria-hidden": "true" }),
            el("span", { text: model.completion ? "Culler completes Project initialization." : "Culler prepares the next setup operation." })),
        unexplainedFailure && el("section", { class: "journal-message" },
          el("h2", { text: "Setup needs attention" }), literal(model.cause || errorFallback)),
        model.completion && el("section", { class: "journal-message journal-completion", testid: "setup-completion" },
          messageProse(model.completed ? "Culler accepted the Project files. Initial setup is complete."
            : "Culler accepted the Project files. Project initialization remains incomplete."),
          model.completion.completedAt && el("p", { class: "journal-muted", text: `Completed at ${dateText(model.completion.completedAt)}` }))),
      el("div", { class: "journal-support", testid: "setup-controls" },
        model.approval && approvalNode(model.approval.revision, { ...actions, deny: model.approval.canDeny ? actions.deny : undefined, current: true, unavailable: model.unavailable }, model.approval.acceptedConfiguration),
        model.retry && retryNode(model, actions),
        diagnosticNode(model, actions.askDiagnostic),
        (model.completed || model.historicalEvidenceMissing) && el("button", { type: "button", class: "btn primary", testid: "setup-open-board", text: "Open Board", onclick: openBoard }))));
}
