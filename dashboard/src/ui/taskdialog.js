import { dialogHeader, infoButton } from "./components.js";
// taskdialog.js — the task dialog. A Board card and the Task button of the
// Review reader open one task's full row (board.js taskDetail) in a modal that
// covers most of the window: a head with the title and the pills, a main column
// with the prose and the callouts that need a decision, and a side column with
// the live run, the facts and the run history.

import { reportNode } from "./boardwrites.js";
import { el, markdownNodes, tag } from "./dom.js";
import { connectionName, agoLabel } from "./format.js";
import { parseMarkdown } from "./markdown.js";
import { traceLinkNode } from "./viewers.js";

/** Runs the history panel lists before it folds the rest into one line. */
export const HISTORY_LIMIT = 6;

/** The history panel's rows: the newest `limit` runs, plus the count of the
 * older ones the panel names instead of listing. */
export function historyView(runs, limit = HISTORY_LIMIT) {
  const rows = (runs ?? []).filter((r) => r);
  return { shown: rows.slice(0, limit), older: rows.slice(limit) };
}

/** The decision panel's tone, from the task row's decision: approved is the
 * good outcome, rejected sends the task back, cancelled ends it. */
export function decisionTone(decision) {
  if (decision === "approved") return "leaf";
  if (decision === "rejected") return "rust";
  return "stone";
}

/** The live run panel's copy, from the detail's run. */
export function liveRunView(run) {
  if (!run) return null;
  const meta = [run.role ?? "a run"];
  if (run.heartbeat) meta.push(`heartbeat ${run.heartbeat} ago`);
  return {
    label: run.stopPending ? "Stopping" : run.stale ? "Heartbeat stopped" : run.starting ? "Agent launch pending" : run.proposalIDs?.length ? "Under Arbiter proposal review" : "Running now",
    started: run.age ? `started ${agoLabel(run.age)}` : "",
    meta: meta.join(" · "),
    runID: run.runID ?? "no run id",
    stale: Boolean(run.stale),
  };
}

const head = (label, count = null) =>
  el(
    "div",
    { class: "sec-head" },
    el("span", { class: "sec-label", text: label }),
    count !== null && el("span", { class: "sec-count", text: String(count) }),
  );

const section = (cls, label, count, ...body) => el("section", { class: `sec ${cls}`.trim() }, head(label, count), ...body);

const callout = (tone, label, ...body) =>
  el("div", { class: `callout tone-${tone}` }, el("div", { class: "sec-label", text: label }), ...body);

const empty = (text) => el("div", { class: "sec-empty", text });

/** The Strategic rationale of a task that the Strategist proposed: the first
 * content section of the dialog. A task that the operator defined has none. */
function rationaleSection(d) {
  if (d.origin !== "autonomous") return null;
  const node = section("prose", "Strategic rationale", null,
    d.strategicRationale ? prose(d.strategicRationale) : empty("No Strategic rationale recorded."));
  node.dataset.testid = "task-dialog-rationale";
  return node;
}

const prose = (text) => el("div", { class: "md prose" }, markdownNodes(parseMarkdown(text)));

function liveNode(run) {
  const v = liveRunView(run);
  return el(
    "div",
    { class: `live${v.stale ? " stale" : ""}`, testid: "task-dialog-live" },
    el(
      "div",
      { class: "live-head" },
      !run.stopPending && el("i", { class: "dot" }),
      el("b", { text: v.label }),
      v.started && el("span", { class: "when", text: v.started }),
    ),
    el("div", { class: "live-meta", text: v.meta }),
    el("div", { class: "mono", text: v.runID }),
  );
}

function diagnosticsButton(runID, onRun, testid) {
  return runID && onRun && el("button", {
    type: "button", class: "btn small ghost run-diagnostics", testid,
    text: "View run diagnostics", onclick: () => onRun(runID),
  });
}

/** The run history of one task: the newest runs with their role and result,
 * on a rail that ends in an origin mark below the first run,
 * the diagnostics control when `onRun` is set, and the trace link of each run
 * with a main trace. A disclosure holds the older runs with the same row. */
export function runHistoryNode(runs, { onRun = null, viewers = null, pending = false } = {}) {
  const { shown, older } = historyView(runs);
  const row = (r) =>
    el(
      "li",
      { class: `tl tl-${r.tone}`, testid: `task-run-${r.id}`, data: { key: r.id ?? "" } },
      el("i", { class: "tl-dot" }),
      tag(r.result, `tone-${r.tone}`),
      el("span", { class: "who", text: r.role ?? "run" }),
      r.proposalIDs?.length > 0 && el("span", { text: `Assigned proposals: ${r.proposalIDs.map(id=>`#${id}`).join(", ")}` }),
      el("span", { class: "when", title: r.stamp ?? "", text: agoLabel(r.age) }),
      diagnosticsButton(r.id, onRun, `task-run-diagnostics-${r.id}`),
      traceLinkNode(r, viewers, pending, `task-run-trace-${r.id}`),
    );
  return el(
    "section",
    { class: "sec history", testid: "task-runs" },
    head("Runs", (runs ?? []).length),
    shown.length === 0 ? empty("No run yet.") : el("ol", { class: older.length > 0 ? "timeline" : "timeline timeline-origin" }, shown.map(row)),
    older.length > 0 && el(
      "details",
      { class: "history-older", testid: "task-runs-earlier", data: { disclosure: "earlier-runs" } },
      el("summary", { class: "sec-more", text: `${older.length} earlier ${older.length === 1 ? "run" : "runs"}` }),
      el("ol", { class: "timeline timeline-origin" }, older.map(row)),
    ),
  );
}

export const RUN_FAILURES_TIP = "A run that stops because of a technical problem, such as a lost connection, counts as one failure. "
  + "Culler then starts the run again. After 3 failures, the task stops until you resume it. "
  + "When the task moves to its next stage, the count goes back to 0.";
export const CORRECTIONS_TIP = "When the Verifier rejects the work, the Worker makes a correction. "
  + "Each correction that the Verifier rejects again counts as one. "
  + "After 3, the task waits for your decision. "
  + "A Verifier pass, a requirements revision, or your approval of more corrections sets the count to 0.";

const tipID = (label) => `fact-tip-${label.toLowerCase().replaceAll(" ", "-")}`;
const tipButton = (label) =>
  infoButton(`About ${label.toLowerCase()}`, tipID(label));

/** A fact of the facts box. A `tip` explains the fact in a tooltip that a
 * pointer or the keyboard opens. */
const fact = (label, value, sub = null, mono = false, tip = null) =>
  el(
    "div",
    { class: "kv" },
    el("div", { class: "k" }, label, tip && tipButton(label)),
    el("div", { class: `v${mono ? " mono" : ""}`, text: value }),
    sub && el("div", { class: "sub", text: sub }),
    tip && el("span", { class: "fact-tip-text", id: tipID(label), role: "tooltip", text: tip }),
  );

/** The alert for a stalled task: three task run failures stopped the
 * automatic retries, so the operator must act. An earlier failure appears only
 * in its run row and in its run diagnostics. */
function infrastructureNode(d, { onRun }) {
  if (!d.stalled) return null;
  const failure = d.failure?.latest;
  return section(
    "failure-details", "Task run failure", null,
    el("div", { class: "callout tone-rust", testid: "task-dialog-retry" },
      el("p", { text: "Three task run failures used the retry allowance of this task. Automatic retries stopped." }),
      el("p", { text: "Resume the task to authorize three more attempts." })),
    el("div", { class: "failure-condition", testid: "task-dialog-failure" },
      fact("Observed condition", failure?.condition || "No failure details are available."),
      failure && fact("Cause", failure.cause === "unknown" ? "The cause is unknown." : failure.diagnosis),
      failure?.operatorAction && fact("Operator action", failure.operatorAction),
      diagnosticsButton(failure?.runID, onRun, "task-failure-diagnostics")),
    failure && el("details", { data: { disclosure: "infrastructure-evidence" } },
      el("summary", { text: "Failure evidence and report source" }),
      el("div", { class: "facts" },
        fact("Task", `#${failure.taskID}`),
        fact("Run", failure.runID ?? "unknown", null, true),
        fact("Report source", failure.source ?? "unknown"),
        fact("Failure time", failure.failedAt ?? "unknown"),
        failure.evidence && fact("Evidence", failure.evidence),
        failure.quotaResetAt && fact("Quota reset time", failure.quotaResetAt),
    )),
  );
}

/** The alert for a task whose next run waits for a connection. */
function connectionWaitNode(d, connections) {
  if (!d.connectionWait) return null;
  return callout("rust", "Waits for a connection",
    el("p", { testid: "task-dialog-wait", text: `A connection check stopped the next run of this task. Culler starts the run when ${connectionName(d.connectionWait, connections) ?? "the connection"} is available.` }),
    el("p", { text: "Review System health in Configs." }));
}

export function taskDialogHead(d, { onClose = null } = {}) {
  const chips = [
    tag(d.stage, `stage tone-${d.stageTone}`),
    d.running &&
      el(
        "span",
        { class: `chip ${d.stopPending ? "tone-stone" : d.stale ? "stale" : "running"}`, testid: "task-dialog-running" },
        !d.stopPending && el("i", { class: "dot" }),
        document.createTextNode(d.running),
      ),
    d.blocked && el("span", { class: "chip attention", text: "Decision Pending" }),
    d.stalled && el("span", { class: "chip attention", text: "Retries Exhausted" }),
    d.connectionWait && el("span", { class: "chip attention", text: "Waits for a connection" }),
    ...(d.tags ?? []).map((t) => tag(t.label, t.kind)),
  ].filter(Boolean);
  return [...dialogHeader({
    title: el("h2", { class: "td-title", id: "task-dialog-title", text: d.title }),
    context: el("div", { class: "metaline" }, chips),
    onClose,
    closeProps: { testid: "task-dialog-close", title: "Close (Esc)" },
  }).childNodes];
}

/** The dialog's body. `loading` is true until the task's full row is read;
 * `error` is the read's failure text; `runs` are the task's run rows from
 * pipeline.js runsView, newest first; `controls` is the record controls node
 * of boardwrites.js; `history` is the history node of boardwrites.js, last
 * in the main column; `onPacket` opens the review packet; `readReport` reads
 * a report; `viewers`
 * and `viewersPending` come from the status response. */
export function taskDialogBody(d, { loading = false, error = null, runs = [], controls = null, history = null, discussion = null, onPacket = null, onRun = null, readReport = null, connections = [], viewers = null, viewersPending = false } = {}) {
  const proseLabel = "Description";
  return [
    el(
      "div",
      { class: "td-main" },
      loading && el("div", { class: "loading", text: "Loading task…" }),
      error && el("div", { class: "note", text: error }),
      d.projectPaused && callout("stone", "Project paused",
        el("p", { text: "The explicit Project pause remains effective after task resolution." })),
      d.projectHold && callout("rust", "Project requires your action",
        el("p", { text: `Task #${d.id} prevents new work in Project ${d.projectName}.` }),
        el("p", { text: "You must address this task before unrelated Project work can continue." }),
        el("p", { text: "This task holds Project priority independently of an explicit pause or the three-task attention threshold." })),
      d.reason && callout("rust", d.allowance?.allowance_condition === "verifier" ? "Verifier correction limit reached" : "Decision Pending",
        el("p", { text: "This task requires an operator decision or repair before work can continue." }),
        el("div", { class: "reason full", text: d.reason }),
        d.allowance?.allowance_reason && d.allowance.allowance_reason !== d.reason && el("p", { text: d.allowance.allowance_reason }),
        d.allowance?.verifier_report?.report_path && d.allowance.allowance_condition === "verifier" && readReport &&
          reportNode(`/api/tasks/${d.id}/correction-report`, readReport, "Verifier correction report"),
        d.allowance?.allowance_condition && el("details", {},
          el("summary", { text: "Technical details and evidence" }),
          el("pre", { class: "allowance-details", text: JSON.stringify({ conflict: d.allowance.latest_conflict, report: d.allowance.verifier_report }, null, 2) }))),
      d.projectInfrastructure && callout("rust", "infra failed",
        [el("p",{text:d.projectInfrastructure.condition}),el("p",{text:"Open System issues to inspect the Project condition."})]),
      infrastructureNode(d, { onRun }),
      connectionWaitNode(d, connections),
      discussion,
      d.packet !== null &&
        onPacket &&
        el("button", {
          type: "button",
          class: "btn primary wide",
          testid: "task-dialog-packet",
          text: "Open the review packet",
          onclick: onPacket,
        }),
      controls,
      rationaleSection(d),
      section("prose", d.requirementsPending ? `Effective ${proseLabel.toLowerCase()}` : proseLabel, null, d.description ? prose(d.description) : empty("No description recorded.")),
      d.decision &&
        callout(
          decisionTone(d.decision),
          "Decision",
          el(
            "div",
            { class: "decision" },
            el("b", { text: d.decision }),
            d.decidedAge && el("span", { class: "when", text: d.decidedAge }),
          ),
          d.mergeStateText && el("p", { testid: "task-merge-state", text: d.mergeStateText }),
        ),
      history,
    ),
    el(
      "div",
      { class: "td-side" },
      d.run && liveNode(d.run),
      el(
        "div",
        { class: "facts", testid: "task-dialog-facts" },
        fact("Task", `#${d.id}`, null, true),
        fact("Origin", d.origin ?? "unknown"),
        fact("Created", d.createdAge ?? "unknown"),
        fact("Stage", d.stage, d.stageAge ? `for ${d.stageAge}` : null, false, d.stageDescription),
        fact("Task run failures", `${d.failure?.count ?? 0} of 3`,
          d.runAllowance?.retryAt ? `Next retry: ${d.runAllowance.retryAt}` : null, false, RUN_FAILURES_TIP),
        d.allowance && fact("Unsuccessful Verifier corrections", `${d.allowance.correction_count ?? 0} of 3`, null, false, CORRECTIONS_TIP),
      ),
      !loading && runHistoryNode(runs, { onRun, viewers, pending: viewersPending }),
    ),
  ];
}
