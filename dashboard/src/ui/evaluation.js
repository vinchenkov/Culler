import { dialogHeader, dialogBody, dialogFooter, dialogFrame, showDialog } from "./components.js";
import { el } from "./dom.js";
import { reportDocument, FRAME_SANDBOX } from "./review.js";

const LABELS = {
  comprehension: "Comprehension", causal_explanation: "Causal explanation", decision_support: "Decision support",
  visual_explanation: "Visual explanation", clarity: "Clarity", packet_length: "Packet length",
};
export function blankAssessment(identity, rubricHash) {
  return { request_id: crypto.randomUUID(), identity: structuredClone(identity), scores: {}, notes: {}, reason: "",
    critical_failure: null, failures: [], familiar_project: false, source_inspected: null, evidence_inspected: null,
    uncertainty: "", supersedes: 0, rubric_sha256: rubricHash, wrapper: "", display: {} };
}
export function assessmentProblem(draft) {
  for (const key of Object.keys(LABELS)) if (!Number.isInteger(draft.scores[key]) || draft.scores[key] < 0 || draft.scores[key] > 4) return "Select all six scores.";
  if (typeof draft.critical_failure !== "boolean") return "Select the critical failure determination.";
  if (!draft.reason.trim()) return "Enter an overall reason.";
  if (!draft.familiar_project || typeof draft.source_inspected !== "boolean" || typeof draft.evidence_inspected !== "boolean") return "Complete the Project context fields.";
  if (draft.critical_failure && (!draft.failures.length || draft.failures.some(f => !f.category || !f.explanation.trim() || !f.evidence.trim()))) return "Complete each critical failure record.";
  return "";
}
const identityKey = row => `${row.project}/${row.task_id}/${row.packet_id}/${row.version_id}`;
const section = (title, ...children) => el("section", {}, el("h3", { text: title }), children);
const details = (title, ...children) => el("details", {}, el("summary", { text: title }), children);
const textList = lines => el("ul", {}, (lines ?? []).map(text => el("li", { text })));

// The draft belongs to one immutable packet, independent of reader updates.
export function createEvaluationControl({ host, api, report }) {
  const drafts = new Map();
  let current = null, status = null, lastRead = 0, serial = 0, dialog = null;
  const button = el("button", { type: "button", class: "btn", testid: "score-case", text: "Score case", onclick: () => open() });
  const message = el("p", { class: "system-helper", testid: "capture-status" });
  host.append(button, message);
  host.hidden = true;
  async function refresh(row, force = false) {
    if (!row?.packet_id) { host.hidden = true; return; }
    const changed = !current || identityKey(row) !== identityKey(current);
    current = structuredClone(row);
    if (!force && !changed && Date.now() - lastRead < 3000) return;
    lastRead = Date.now();
    const request = ++serial;
    try {
      const result = await api("/api/evaluation/status", { method: "POST", body: JSON.stringify({
        project: row.project, task_id: row.task_id, packet_id: row.packet_id, version_id: row.version_id,
      }) });
      if (request !== serial) return;
      status = result;
      host.hidden = !result.enabled;
      button.disabled = !result.available;
      message.textContent = result.available ? "" : result.error || "The source capture is incomplete. Culler retries the capture.";
    } catch (error) {
      if (request !== serial) return;
      button.disabled = true;
      message.textContent = String(error.message ?? error);
    }
  }
  async function open() {
    if (!status?.available || !status.enabled || !current) return;
    const row = structuredClone(current), capture = structuredClone(status), key = identityKey(row);
    let entry = drafts.get(key);
    if (!entry) {
      const rendered = report();
      if (!rendered || rendered.hash !== capture.identity.packet_sha256) { message.textContent = "The displayed report differs from the captured packet. Open the packet again."; return; }
      const draft = blankAssessment(capture.identity, capture.rubric_sha256);
      draft.wrapper = rendered.wrapper;
      draft.display = { viewport: { width: innerWidth, height: innerHeight }, theme: document.documentElement.dataset.theme ?? "default",
        source: { input_commit: row.input_commit, submitted_commit: row.submitted_commit },
        candidate: { candidate_commit: row.candidate_commit, destination_commit: row.destination_commit },
        verifier_evidence: row.verifier_evidence, packet_id: row.packet_id, version_id: row.version_id };
      entry = { draft, capture, row, receipt: null, busy: false, evidenceReady: false };
      drafts.set(key, entry);
    }
    if (entry.busy) { message.textContent = "The case save is in progress. Open the dialog after it completes."; return; }
    if (dialog) dialog.remove();
    dialog = el("dialog", { class: "evaluation-dialog", testid: "score-dialog", "aria-label": "Score case" });
    document.getElementById("packet-reader").append(dialog);
    const { draft } = entry;
    const error = el("p", { role: "alert", testid: "score-error" });
    const saved = el("div", { testid: "case-saved" });
    const form = el("form", { class: "evaluation-form" });
    const mutate = fn => { if (entry.busy || entry.receipt) return; fn(); draft.request_id = crypto.randomUUID(); };
    const select = (label, value, choices, change, testid) => {
      const input = el("select", { class: "field", "aria-label": label, testid,
        onchange: event => mutate(() => change(event.target.value)) },
        el("option", { value: "", text: "Select a value", selected: value == null }),
        choices.map(([v, text]) => el("option", { value: String(v), text, selected: value != null && String(value) === String(v) })));
      return el("label", {}, el("span", { text: label }), input);
    };
    const text = (label, value, change, testid) => el("label", {}, el("span", { text: label }), el("textarea", {
      class: "field", rows: 3, "aria-label": label, testid, value,
      oninput: event => mutate(() => change(event.target.value)),
    }, value));
    const yesNo = (label, value, change, testid) => select(label, value, [[true, "Yes"], [false, "No"]], v => change(v === "" ? null : v === "true"), testid);
    const opener = document.activeElement;
    const currentDialog = dialog;
    const close = () => currentDialog.close();
    currentDialog.addEventListener("cancel", event => { event.preventDefault(); close(); });
    currentDialog.addEventListener("close", () => {
      currentDialog.remove();
      if (dialog !== currentDialog) return;
      dialog = null;
      if (opener?.isConnected) opener.focus();
    });
    const header = dialogHeader({ title: "Score case", titleId: "score-dialog-title", onClose: close,
      closeProps: { title: "Close and keep draft" } });
    const body = dialogBody({ class: "evaluation-content" });
    body.append(el("p", { testid: "score-identity", text: `Task #${row.task_id} · Packet ${row.packet_id} · Version ${row.version_id}` }),
      el("p", { class: "evaluation-commit", text: `Reviewed commit: ${draft.identity.submitted_commit}` }),
      el("p", { text: `Rubric version ${entry.capture.rubric.version}. ${entry.capture.rubric.calibration_status}.` }),
      textList(entry.capture.rubric.assessment_guidance),
      el("p", { text: "Assess the packet explanation separately from source correctness. Generated Files changed data remains outside these scores." }));
    const evidence = details("Preserved source and Verifier evidence", el("p", { text: "Read the original reviewed diff below. A later merge candidate remains separate." }));
    body.append(details("Packet and criteria", el("iframe", { class: "evaluation-report", title: "Packet for this assessment", sandbox: FRAME_SANDBOX,
      referrerpolicy: "no-referrer", srcdoc: draft.wrapper }), el("pre", { text: row.criteria ?? "" })), evidence);
    api("/api/evaluation/inspect", { method: "POST", body: JSON.stringify({ case_id: capture.case_id, candidate_commit: row.candidate_commit ?? "", destination_commit: row.destination_commit ?? "" }) }).then(result => {
      draft.observation = result.observation ?? "";
      entry.evidenceReady = true;
      if (result.candidate_diff) evidence.append(details("Later merge candidate diff", el("pre", { text: result.candidate_diff })));
      evidence.append(details("Original reviewed diff", el("pre", { text: result["source/reviewed.diff"] ?? "The reviewed diff is unavailable." })),
        ...result.evidence.map(record => details(record.origin, el("pre", { text: record.text ?? `Media type: ${record.media_type}. Content hash: ${record.sha256}.` }))));
    }).catch(failure => evidence.append(el("p", { text: failure.message })));
    for (const [key, label] of Object.entries(LABELS)) {
      const rubric = entry.capture.rubric.dimensions[key];
      body.append(section(label, textList(rubric.guidance),
        select(`${label} score`, draft.scores[key], Object.entries(rubric.anchors).map(([score, anchor]) => [score, `${score} — ${anchor}`]),
          value => { draft.scores[key] = value === "" ? null : Number(value); }, `score-${key}`),
        details(`${label} anchors`, textList(Object.entries(rubric.anchors).map(([score, anchor]) => `${score}: ${anchor}`))),
        text(`${label} notes (optional)`, draft.notes[key] ?? "", value => { draft.notes[key] = value; })));
    }
    const failureRows = el("div");
    const redrawFailures = () => {
      failureRows.replaceChildren();
      failureRows.hidden = draft.critical_failure !== true;
      if (draft.critical_failure !== true) return;
      draft.failures.forEach((failure, index) => failureRows.append(section(`Critical failure ${index + 1}`,
        select("Failure category", failure.category || null, entry.capture.rubric.critical_failure_categories.map(c => [c, c.replaceAll("_", " ")]), v => { failure.category = v; }),
        text("Failure explanation", failure.explanation, v => { failure.explanation = v; }),
        text("Packet or source evidence reference", failure.evidence, v => { failure.evidence = v; }),
        el("button", { type: "button", class: "btn", text: "Remove failure", onclick: () => { mutate(() => draft.failures.splice(index, 1)); redrawFailures(); } }))));
      failureRows.append(el("button", { type: "button", class: "btn", text: "Add critical failure", onclick: () => {
        mutate(() => draft.failures.push({ category: "", explanation: "", evidence: "" })); redrawFailures();
      } }));
    };
    body.append(yesNo("Does the packet contain a critical failure?", draft.critical_failure, value => {
      draft.critical_failure = value;
      if (value && draft.failures.length === 0) draft.failures.push({ category: "", explanation: "", evidence: "" });
      if (value === false) draft.failures = [];
      redrawFailures();
    }, "score-critical"), failureRows,
      text("Overall reason", draft.reason, value => { draft.reason = value; }, "score-reason"),
      el("label", {}, el("input", { type: "checkbox", checked: draft.familiar_project, testid: "score-familiar",
        onchange: event => mutate(() => { draft.familiar_project = event.target.checked; }) }),
      "I know this Project through actual development and review."),
      yesNo("Did you inspect the relevant source changes?", draft.source_inspected, value => { draft.source_inspected = value; }, "score-source"),
      yesNo("Did you inspect the Verifier evidence?", draft.evidence_inspected, value => { draft.evidence_inspected = value; }, "score-evidence"),
      el("p", { text: "These inspection records describe your review. They do not claim that you executed a verification check." }),
      text("Uncertainty (optional)", draft.uncertainty, value => { draft.uncertainty = value; }), error, saved);
    redrawFailures();
    const submit = el("button", { type: "submit", class: "btn primary", testid: "score-submit", text: "Save case" });
    const freeze = disabled => { for (const input of form.querySelectorAll("select, textarea, input")) input.disabled = disabled; };
    const showReceipt = receipt => {
      freeze(true);
      entry.receipt = receipt;
      saved.replaceChildren(el("p", { text: `Case saved: ${receipt.case_id}. Assessment revision ${receipt.assessment_revision}.` }),
        el("button", { type: "button", class: "btn", text: "Download case", testid: "case-download", onclick: async () => {
          try {
            const archive = await api("/api/evaluation/download", { method: "POST", body: JSON.stringify({ case_id: receipt.case_id }) });
            const blob = new Blob([Uint8Array.from(atob(archive.archive_base64), c => c.charCodeAt(0))], { type: "application/gzip" });
            const url = URL.createObjectURL(blob), link = el("a", { href: url, download: `${receipt.case_id}.tar.gz` });
            link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
          } catch (failure) { error.textContent = failure.message; }
        } }), el("button", { type: "button", class: "btn", text: "Revise assessment", onclick: () => {
          entry.receipt = null; mutate(() => { draft.supersedes = receipt.assessment_revision; }); freeze(false); saved.replaceChildren(); submit.disabled = false; submit.textContent = "Save assessment revision";
        } }));
      submit.disabled = true;
    };
    const previous = entry.capture.receipts?.at(-1);
    if (previous && !entry.receipt && draft.supersedes === 0) {
      submit.disabled = true;
      saved.append(el("p", { text: `This case has assessment revision ${previous.assessment_revision}.` }),
        el("button", { type: "button", class: "btn", text: "Start assessment revision", onclick: () => {
          mutate(() => { draft.supersedes = previous.assessment_revision; }); submit.disabled = false; saved.replaceChildren();
        } }));
    }
    const footer = dialogFooter({}, submit);
    dialogFrame(dialog, { header, body, footer, shell: form });
    form.addEventListener("submit", async event => {
      event.preventDefault();
      if (entry.busy || entry.receipt) return;
      error.textContent = entry.evidenceReady ? assessmentProblem(draft) : "The preserved evidence requires a successful read before submission.";
      if (error.textContent) return;
      submit.disabled = true;
      entry.busy = true; freeze(true);
      try {
        const receipt = await api("/api/evaluation/submit", { method: "POST", body: JSON.stringify({ case_id: capture.case_id, assessment: draft }) });
        showReceipt(receipt);
      } catch (failure) { error.textContent = failure.message; submit.disabled = false; freeze(false); }
      finally { entry.busy = false; }
    });
    if (entry.receipt) showReceipt(entry.receipt);
    showDialog(dialog);
    dialog.focus();
  }
  setInterval(() => {
    if (current && document.getElementById("packet-reader")?.getClientRects().length) void refresh(current, true);
  }, 3000);
  return { refresh };
}
