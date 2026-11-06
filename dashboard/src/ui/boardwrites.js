import { dialogHeader, dialogBody, dialogFooter, dialogFrame, showDialog } from "./components.js";
import { projectName } from "./format.js";
import { el, markdownNodes } from "./dom.js";
import { parseMarkdown } from "./markdown.js";
import { FRAME_SANDBOX, renderMode, reportDocument } from "./review.js";

export const REVISION_WAIT = "mc saved your revision request. The current run will finish before mc applies your changes.";
const DRAFT_KEY = "mc.board-write-form";
const priorityName = (value) => Number(value) === -1 ? "Expedited" : `Priority ${value}`;
const prose = (text) => el("div", { class: "md prose" }, markdownNodes(parseMarkdown(text ?? "")));
const paragraphs = (texts) => texts.map((text) => el("p", { text }));

/** The form that Resolve decision opens. A requirements revision resolves an
 * operator decision report; a reason resolves each other condition. */
export function resolveAction(row) {
  return Boolean(row?.blocked) && row.blocked_kind === "report" ? "resolve" : "unblock";
}

export function boardEditState(row) {
  const permanent = Boolean(row?.decision || row?.archived);
  const restriction = permanent ? row?.archived ? "This record is archived." : `This record is ${row.decision}.`
    : (row?.status === "proposed" ? "Culler permits operator changes after a task leaves Proposed." : "") || row?.edit_restriction || "";
  const proposed = row?.status === "proposed";
  const available = Boolean(row && Number.isInteger(row.record_version));
  const canRevise = available && !proposed && !restriction && !row.pending_revision;
  return {
    restriction,
    canEdit: available && !proposed && row.status !== "packaged" && !restriction,
    canRevise,
    canUnblock: !row?.delivery?.allowance_condition && available && !proposed && !row.proposal_decision_id && !row.archived && (!row.decision || row.decision === "approved") && Boolean(row.blocked)
      && (resolveAction(row) !== "resolve" || canRevise),
    canAuthorize: available && !row.archived && Boolean(row.delivery?.allowance_condition),
    canResume: available && !proposed && !row.archived && Boolean(row.stalled),
    canCancel: available && !proposed && !row.archived && (!row.decision || row.decision === "approved") && !row.delivery?.application_pending,
  };
}

export function revisionEffect(row, activeRun = null) {
  const texts = [];
  if (activeRun) texts.push(`Run ${activeRun.run_id} must finish before the revision takes effect.`);
  texts.push(row.status === "seeded"
    ? "The task stays Seeded. The next Worker run uses the revised requirements."
    : "The task returns to Seeded. The Worker repeats the work against the revised requirements.");
  if (resolveAction(row) === "resolve") texts.push("The revision ends Decision Pending.");
  texts.push("Earlier reports stay in history. Final approval requires a new review packet for the effective requirements.");
  texts.push("An independent block or infrastructure retry requirement stays in effect.");
  return texts;
}

export function resolveEffect(row, activeRun = null) {
  const texts = [
    "Record the decision in the description. Remove, rewrite, or add a criterion as the decision requires.",
    "The description holds every requirement. The Worker reads the reason only as an explanation.",
  ];
  if (activeRun) texts.push(`Run ${activeRun.run_id} must finish before the revision takes effect. Decision Pending stays until then.`);
  texts.push(row.status === "seeded"
    ? "The revision ends Decision Pending. The task stays Seeded."
    : "The revision ends Decision Pending and returns the task to Seeded.");
  if (row.stalled) texts.push("Retries Exhausted stays in effect.");
  texts.push("The Worker continues from the last commit of the task.");
  return texts;
}

/** The text that names what a requirements revision still needs before a
 * save, or "" when the form can save. */
export function revisionSaveRule(draft) {
  if (!["revise", "resolve"].includes(draft?.action)) return "";
  const changed = String(draft.requirements ?? "").trim() !== String(draft.record?.description ?? "").trim();
  const reason = String(draft.reason ?? "").trim() !== "";
  if (changed && reason) return "";
  if (!changed && !reason) return "Change the description and write a reason before you save.";
  return changed ? "Write a reason before you save." : "Change the description before you save.";
}

export function revisionWaitTexts(revision) {
  const texts = [];
  if (revision.active_run_id) texts.push(REVISION_WAIT, `Current run: ${revision.active_run_id}`);
  if (revision.wait_reason && revision.wait_reason !== REVISION_WAIT) texts.push(revision.wait_reason);
  if (revision.failure_reason) texts.push(revision.failure_reason);
  return texts;
}

export function boardWritePayload(draft) {
  // The resolution of an operator decision report is a requirements revision.
  const revise = draft.action === "revise" || draft.action === "resolve";
  const body = { action: revise ? "revise" : draft.action };
  if (draft.action === "create") {
    Object.assign(body, { scope: "task", project: draft.project, title: draft.title, priority: Number(draft.priority) });
    body.description = draft.requirements;
  } else {
    Object.assign(body, { task_id: draft.taskID, expected_version: draft.expectedVersion });
    if (draft.action === "edit") Object.assign(body, { title: draft.title, priority: Number(draft.priority) });
    else {
      body.reason = draft.reason;
      if (revise) body.description = draft.requirements;
    }
  }
  return body;
}

// Each exact request keeps its identity until the operator changes its values.
export function submissionPayload(draft, makeID = () => crypto.randomUUID()) {
  const body = boardWritePayload(draft);
  const key = JSON.stringify(body);
  if (draft.submission?.key !== key) draft.submission = { key, operationID: makeID() };
  return { ...body, operation_id: draft.submission.operationID };
}

export function boardRecordControls(row, { onAction } = {}) {
  const controls = boardEditState(row);
  // The action that answers the task's condition leads; the row holds the rest.
  const allowance = row.delivery?.allowance_condition;
  const lead = row.status === "proposed" ? null : allowance === "verifier" ? "allow-corrections"
    : row.blocked ? "unblock" : row.stalled ? "resume" : null;
  const button = (action, text, enabled) => el("button", {
    type: "button", class: action === lead ? "btn primary board-lead" : `btn small${action === "cancel" ? " cancel" : ""}`,
    testid: `board-${action}`, text, disabled: !enabled,
    onclick: () => onAction?.(action),
  });
  const leadButton = lead === "unblock" ? button("unblock", "Resolve decision", controls.canUnblock)
    : lead === "resume" ? button("resume", "Resume task", controls.canResume)
    : lead === "allow-corrections" ? button(lead, "Authorize three more corrections", controls.canAuthorize) : null;
  return el("section", { class: "board-record-controls", testid: "board-record-controls" },
    leadButton,
    el("div", { class: "board-action-row" },
      row.status !== "proposed" && row.status !== "packaged" && button("edit", "Edit title and priority", controls.canEdit),
      row.status !== "proposed" && button("revise", "Revise requirements", controls.canRevise),
      row.status !== "proposed" && Boolean(row.stalled) && lead !== "resume" && button("resume", "Resume task", controls.canResume),
      row.status !== "proposed" && button("cancel", "Cancel task", controls.canCancel),
    ),
    controls.restriction && el("p", { class: "board-notice", testid: "board-edit-restriction", text: controls.restriction }),
    boardRevisionNode(row),
  );
}

function boardRevisionNode(row) {
  const revision = row.pending_revision;
  if (!revision) return null;
  return el("section", { class: "board-notice revision-status", testid: "board-revision-status", "aria-label": "Requirements revision" },
    el("strong", { text: "Requirements revision" }),
    el("div", { testid: "board-requested-requirements" },
      el("p", { text: "Requested requirements" }), prose(revision.requirements),
      el("p", { text: `Reason: ${revision.reason}` }),
      el("p", { text: `Revision ${revision.id} · ${revision.state} · ${revision.requested_at}` }),
      ...paragraphs(revisionWaitTexts(revision)),
    ),
  );
}

function historyValues(values, label) {
  if (!values) return null;
  const fields = [
    ["title", "Title"], ["priority", "Priority"], ["requirements", "Requirements"],
    ["description", "Description"], ["status", "Stage"],
    ["blocked_reason", "Decision reason"], ["decision", "Decision"],
  ].filter(([key]) => values[key] !== undefined && values[key] !== null);
  if (!fields.length) return null;
  return el("div", { class: "board-history-values" }, el("h4", { text: label }),
    el("dl", {}, fields.map(([key, name]) => [
      el("dt", { text: name }),
      el("dd", {}, ["requirements", "description"].includes(key) ? prose(values[key])
        : String(key === "priority" ? priorityName(values[key]) : values[key])),
    ])),
  );
}

/** A "Read the report" control that reads the report at `path` and shows it
 * in place. */
export function reportNode(path, readReport, title = "Historical report") {
  if (!readReport) return null;
  const content = el("div", { class: "board-history-report" });
  const button = el("button", {
    type: "button", class: "btn small", text: "Read the report",
    onclick: async () => {
      button.disabled = true;
      try {
        const { body, type } = await readReport(path);
        content.replaceChildren(renderMode(type, body) === "html"
          ? el("iframe", { class: "render-frame", title, sandbox: FRAME_SANDBOX, srcdoc: reportDocument(body) })
          : prose(body));
      } catch (error) {
        content.replaceChildren(el("p", { role: "alert", text: error.message }));
      } finally { button.disabled = false; }
    },
  });
  return el("div", { data: { report: path } }, button, content);
}

/** The task's edit and revision history, or null when the task has none. */
export function boardHistoryNode(row, readReport) {
  const operations = row.board_history ?? [];
  const revisions = row.requirements_history ?? [];
  const packets = row.packet_history ?? [];
  const evidence = row.evidence_history ?? [];
  if (!operations.length && !revisions.length && !packets.length && !evidence.length) return null;
  const actionLabel = { create: "Created", edit: "Title and priority edit", revise: "Requirements revision", block: "Decision Pending", unblock: "Decision resolved", resume: "Task resume", cancel: "Cancelled" };
  return el("details", { class: "board-history", testid: "board-history", data: { disclosure: "history" } },
    el("summary", { text: "Edit and revision history" }),
    el("ol", {}, operations.map((operation) => el("li", {},
      el("strong", { text: operation.outcome?.automatic ? "Automatic decision resolution" : actionLabel[operation.action] ?? operation.action }),
      el("p", { text: `${operation.actor} · ${operation.created_at}` }),
      operation.reason && el("p", { text: `Reason: ${operation.reason}` }),
      el("p", { text: operation.outcome?.revision_state ? `Revision state at acceptance: ${operation.outcome.revision_state}` : "Result: accepted" }),
      el("details", { data: { disclosure: `operation-${operation.operation_id}` } }, el("summary", { text: "Previous and requested values" }),
        historyValues(operation.previous, "Previous values"), historyValues(operation.requested, "Requested values")),
    ))),
    el("ol", {}, revisions.map((revision) => el("li", {},
      el("strong", { text: `Revision ${revision.id} · ${revision.state}` }),
      el("p", { text: `${revision.actor} · ${revision.requested_at}` }),
      el("p", { text: `Reason: ${revision.reason}` }),
      revision.applied_at && el("p", { text: `Applied: ${revision.applied_at}` }),
      revision.failure_reason && el("p", { text: revision.failure_reason }),
      el("details", { data: { disclosure: `revision-${revision.id}` } }, el("summary", { text: "Previous and requested requirements" }),
        el("p", { text: "Previous requirements" }), prose(revision.previous_requirements),
        el("p", { text: "Requested requirements" }), prose(revision.requirements)),
    ))),
    packets.length > 0 && el("details", { data: { disclosure: "packets" } }, el("summary", { text: "Superseded review packets" }),
      el("p", { text: "These packets describe earlier requirements. Current approval requires a new packet." }),
      packets.map((packet) => el("section", { class: "board-history-item" },
        el("strong", { text: `Requirements version ${packet.requirements_version}` }),
        el("p", { text: `Created: ${packet.created_at}` }),
        prose(packet.thesis),
        packet.render_path && reportNode(`/api/tasks/${row.id}/history/packets/${packet.id}/render`, readReport),
      )),
    ),
    evidence.length > 0 && el("details", { data: { disclosure: "evidence" } }, el("summary", { text: "Earlier completion and verification evidence" }),
      evidence.map((entry) => el("section", { class: "board-history-item" },
        el("strong", { text: `Requirements version ${entry.requirements_version} · ${entry.status}` }),
        el("p", { text: `Recorded: ${entry.created_at}` }),
        entry.completion_sha && el("p", { text: `Submitted commit: ${entry.completion_sha}` }),
        entry.verified_sha && el("p", { text: `Verified commit: ${entry.verified_sha}` }),
        entry.delivery && reportNode(`/api/tasks/${row.id}/history/evidence/${entry.id}/render`, readReport),
      )),
    ),
  );
}

export function createBoardWrites({ dialog, api, getProjects, selectedProject, onSaved, onCollie }) {
  let draft = null;
  let busy = false;
  let previousFocus = null;
  let previousTestID = null;
  let currentRecord = null;
  let errorBox = null;
  let conflictBox = null;
  let statusBox = null;
  let ruleBox = null;

  function persist() {
    try {
      if (draft) sessionStorage.setItem(DRAFT_KEY, JSON.stringify(draft));
      else sessionStorage.removeItem(DRAFT_KEY);
    } catch {}
  }

  function close() {
    if (!draft || busy) return;
    draft = null;
    persist();
    if (dialog.open) dialog.close();
  }

  dialog.addEventListener("close", () => {
    const target = previousFocus?.isConnected ? previousFocus
      : previousTestID ? document.querySelector(`[data-testid="${previousTestID}"]`) : null;
    target?.focus();
  });
  dialog.addEventListener("keydown", (event) => {
    if (event.key !== "Tab") return;
    const controls = [...dialog.querySelectorAll("button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], summary")]
      .filter((node) => node.getClientRects().length > 0);
    const first = controls[0];
    const last = controls.at(-1);
    if (!first) event.preventDefault();
    else if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  });
  dialog.addEventListener("cancel", (event) => {
    if (busy) event.preventDefault();
    else { draft = null; persist(); }
  });
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog && !busy) close();
  });

  function field(label, name, { multiline = false, required = false, options = null } = {}) {
    const id = `board-write-${name}`;
    const input = options
      ? el("select", { id, name, testid: id, required }, options.map(([value, text]) => el("option", { value, text })))
      : el(multiline ? "textarea" : "input", { id, name, testid: id, required, placeholder: name === "reason" ? ({ cancel: "Say why this work should stop.", unblock: "Say what resolved the decision.", resolve: "Say why you changed the requirements.", resume: "Say what was repaired." }[draft.action] ?? "Say what changed and why.") : null, ...(multiline ? { rows: name === "reason" ? 3 : 7 } : { type: "text" }) });
    input.value = draft[name] ?? "";
    input.addEventListener("input", () => { draft[name] = input.value; persist(); updateStatus(); });
    return el("label", { class: "board-field", for: id }, el("span", { text: label }), input);
  }

  function updateStatus() {
    if (!draft || !statusBox || draft.action === "create") return;
    const record = currentRecord ?? draft.record;
    const revision = ["revise", "resolve"].includes(draft.action);
    const restricted = (["edit", "revise", "resolve"].includes(draft.action) && boardEditState(record).restriction) ||
      (revision && currentRecord?.pending_revision && "The requested requirements must take effect before another revision.") ||
      (draft.action === "resolve" && resolveAction(record) !== "resolve" && "The decision condition changed. Close this form and open Resolve decision again.");
    statusBox.textContent = restricted || "";
    statusBox.hidden = !restricted;
    const rule = restricted ? "" : revisionSaveRule(draft);
    if (ruleBox) {
      ruleBox.textContent = rule;
      ruleBox.hidden = !rule;
    }
    const button = dialog.querySelector('[data-testid="board-write-submit"]');
    if (button) button.disabled = busy || Boolean(restricted) || Boolean(rule);
    const cancel = dialog.querySelector('[data-testid="board-resolve-cancel"]');
    if (cancel) cancel.disabled = busy || !boardEditState(record).canCancel;
  }

  function showConflict(row) {
    currentRecord = row;
    if (!conflictBox || !draft) return;
    conflictBox.replaceChildren(
      el("strong", { text: "The record changed. Your unsaved values stay in this form." }),
      el("p", { text: `Current title: ${row.title}` }),
      el("p", { text: `Current priority: ${priorityName(row.priority)}` }),
      el("p", { text: `Current record version: ${row.record_version}` }),
      el("p", { text: "Current effective requirements" }), prose(row.description),
      el("button", {
        type: "button", class: "btn", testid: "board-write-use-current", text: "Use the current record version",
        onclick: () => {
          draft.expectedVersion = row.record_version;
          draft.record = row;
          draft.submission = null;
          persist();
          conflictBox.hidden = true;
          errorBox.hidden = true;
          updateStatus();
        },
      }),
    );
    conflictBox.hidden = false;
    updateStatus();
  }

  async function submit(event) {
    event.preventDefault();
    if (busy || !draft || revisionSaveRule(draft)) return;
    const submitted = draft;
    const body = submissionPayload(submitted);
    persist();
    busy = true;
    const form = event.currentTarget;
    for (const input of form.querySelectorAll("input, textarea, select, button")) input.disabled = true;
    errorBox.hidden = true;
    let result;
    try {
      result = await api("/api/board/write", { method: "POST", body: JSON.stringify(body) });
    } catch (error) {
      if (draft !== submitted) return;
      errorBox.textContent = error.message;
      errorBox.hidden = false;
      if (error.status === 409 && submitted.taskID) {
        try { showConflict(await api(`/api/tasks/${submitted.taskID}`)); }
        catch (readError) { errorBox.textContent += ` Current record: ${readError.message}`; }
      }
    } finally {
      busy = false;
      if (draft === submitted) {
        for (const input of form.querySelectorAll("input, textarea, select, button")) input.disabled = false;
        updateStatus();
      }
    }
    if (!result || draft !== submitted) return;
    // The write is done, so the refresh in onSaved must not hold busy: an
    // action opened during that refresh would otherwise be dropped silently.
    close();
    await onSaved(result, submitted);
  }

  function render() {
    const create = draft.action === "create";
    const revise = draft.action === "revise";
    const resolve = draft.action === "resolve";
    const labels = {
      "allow-corrections": "Authorize three more corrections",
      create: "Create task", edit: "Edit title and priority", revise: "Revise requirements",
      unblock: "Resolve decision", resolve: "Resolve decision", resume: "Resume task", cancel: "Cancel task",
    };
    const form = el("form", { class: "board-write-form", autocomplete: "off", onsubmit: submit });
    const header = dialogHeader({ title: labels[draft.action], titleId: "board-write-heading", onClose: close,
      closeProps: { testid: "board-write-close", title: "Close without a change" } });
    const body = dialogBody({ class: "board-write-content" });
    if (create) {
      body.append(el("p", { class: "board-notice", testid: "board-create-explanation" },
        "Create a new task here, or ask ",
        el("a", { href: "#collie", testid: "board-create-collie", text: "Collie", onclick: (event) => { event.preventDefault(); close(); onCollie(); } }),
        " to do it for you.",
      ));
      if (draft.fixedProject) {
        body.append(el("p", { class: "board-destination", testid: "board-write-destination", text: `Project: ${projectName(draft.project, getProjects())}` }));
      } else {
        const options = [["", "Select a Project"], ...getProjects().map((row) => [row.id, projectName(row.id, getProjects())])];
        body.append(field("Destination Project", "project", { required: true, options }));
      }
    } else {
      body.append(el("p", { class: "board-destination", testid: "board-write-destination", text: `Task #${draft.taskID} · Project: ${projectName(draft.project, getProjects())}` }));
    }
    if (create || draft.action === "edit") {
      body.append(field("Title", "title", { required: true }));
      body.append(field("Priority", "priority", { options: [-1, 0, 1, 2, 3].map((value) => [value, priorityName(value)]) }));
    }
    if (revise) {
      body.append(el("details", { class: "board-effective", open: true },
        el("summary", { text: "Effective requirements" }), prose(draft.record.description)));
      body.append(el("div", { class: "board-effect", testid: "board-revision-effect" }, ...paragraphs(revisionEffect(draft.record, draft.activeRun))));
    }
    if (resolve) {
      body.append(el("section", { class: "board-notice board-report", testid: "board-resolve-report", "aria-label": "Agent report" },
        el("strong", { text: "Agent report" }), prose(draft.record.blocked_reason)));
    }
    if (create || revise || resolve) body.append(field("Description", "requirements", { multiline: true }));
    if (resolve) body.append(el("div", { class: "board-effect", testid: "board-resolve-effect" }, ...paragraphs(resolveEffect(draft.record, draft.activeRun))));
    if (!create && draft.action !== "edit") {
      const effects = {
        "allow-corrections": ["This action permits three more unsuccessful Verifier corrections.",
          "The task returns to Seeded with the correction report. Independent conditions stay in effect."],
        unblock: [draft.record.decision === "approved"
          ? "Decision resolution records the answer and lets the approved merge resume when all other controls permit it."
          : "Decision resolution records the answer and restores dispatch eligibility when all other controls permit it.",
        "The pipeline stage stays the same. Retries Exhausted remains independent."],
        resume: ["Resume task restores three task run failure attempts.","Decision Pending remains independent."],
        cancel: ["Cancellation ends this task.", "Removal of accepted code requires a corrective task."],
      }[draft.action];
      if (effects) body.append(el("div", { class: "board-notice", testid: "board-action-explanation" }, ...paragraphs(effects)));
      body.append(field(draft.action === "unblock" || resolve ? "What resolved the decision?" : "Reason", "reason", { multiline: true, required: true }));
    }
    if (resolve) {
      body.append(el("div", { class: "board-resolve-alternative", testid: "board-resolve-alternative" },
        el("p", { text: "To stop this work instead, cancel the task." }),
        el("button", {
          type: "button", class: "btn small cancel", testid: "board-resolve-cancel", text: "Cancel task",
          onclick: () => switchAction("cancel"),
        }),
      ));
    }
    statusBox = el("p", { class: "board-notice", testid: "board-write-restriction", hidden: true });
    errorBox = el("p", { class: "board-write-error", testid: "board-write-error", role: "alert", hidden: true });
    conflictBox = el("div", { class: "board-notice board-conflict", testid: "board-write-conflict", hidden: true });
    ruleBox = el("p", { class: "board-write-rule", testid: "board-write-rule", hidden: true });
    body.append(statusBox, errorBox, conflictBox, ruleBox);
    const footer = dialogFooter({ class: "board-write-actions" },
        el("button", { type: "button", class: "btn", testid: "board-write-keep", text: "Close without a change", onclick: close }),
        el("button", {
          type: "submit", class: draft.action === "cancel" ? "btn danger" : "btn primary", testid: "board-write-submit",
          text: create ? "Create task"
            : draft.action === "edit" ? "Save title and priority" : revise || resolve ? "Save requirements" : labels[draft.action],
        }),
    );
    dialogFrame(dialog, { header, body, footer, shell: form });
    updateStatus();
  }

  function actionDraft(action, row, activeRun) {
    return { action, taskID: row.id, expectedVersion: row.record_version, scope: row.scope, project: row.project,
      title: row.title ?? "", requirements: row.description ?? "", priority: row.priority, reason: "", record: row, activeRun };
  }

  // The resolve form offers Cancel task in the same dialog. The dialog keeps
  // the focus target of the control that opened it.
  function switchAction(action) {
    if (busy || !draft) return;
    const latest = currentRecord ?? draft.record;
    show(actionDraft(action, draft.record, draft.activeRun), { keepFocus: true });
    currentRecord = latest;
    if (latest.record_version !== draft.expectedVersion) showConflict(latest);
    else updateStatus();
    dialog.querySelector('[data-testid="board-write-reason"]')?.focus();
  }

  function show(next, { restore = false, keepFocus = false } = {}) {
    if (busy) return;
    if (!keepFocus) {
      previousFocus = document.activeElement;
      previousTestID = previousFocus?.dataset?.testid ?? null;
    }
    draft = next;
    currentRecord = next.record ?? null;
    if (!restore) {
      try {
        const saved = JSON.parse(sessionStorage.getItem(DRAFT_KEY));
        if (saved?.action === next.action && saved?.taskID === next.taskID && saved?.project === next.project) draft = saved;
      } catch {}
    }
    persist();
    render();
    if (!dialog.open) showDialog(dialog);
    if (currentRecord && currentRecord.record_version !== draft.expectedVersion) showConflict(currentRecord);
  }

  return {
    close,
    openCreate() {
      const selected = getProjects().find((row) => row.id === selectedProject());
      show({ action: "create", scope: "task", project: selected?.id ?? "",
        fixedProject: Boolean(selected), title: "", requirements: "", priority: 2, reason: "" });
    },
    openAction(action, row, activeRun = null) {
      show(actionDraft(action === "unblock" ? resolveAction(row) : action, row, activeRun));
    },
    updateRecord(row) {
      if (!draft || draft.taskID !== row.id) return;
      currentRecord = row;
      if (row.record_version !== draft.expectedVersion) showConflict(row);
      else updateStatus();
    },
    restore() {
      if (draft || busy) return;
      try {
        const saved = JSON.parse(sessionStorage.getItem(DRAFT_KEY));
        if (saved?.submission && saved.project === selectedProject()) show(saved, { restore: true });
      } catch {}
    },
  };
}
