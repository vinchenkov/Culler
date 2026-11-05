import { el } from "./dom.js";
import { conditionExplanation, conditionImpact, conditionRetry, conditionSummary } from "./systemhealth.js";

const REFERENCES = ["condition_id", "project_id", "task_id", "run_id", "diagnostic_id", "connection_id", "profile_id",
  "session_id", "message_id", "attempt_id", "operation_id", "proposal_decision_id", "revision_id"];

function excerpt(value, limit = 1200) {
  if (typeof value !== "string") return "";
  const text = value.trim();
  return text.length > limit ? `${text.slice(0, limit)}… [Excerpt; read the referenced record for details.]` : text;
}

export function diagnosticMessage(context) {
  const references = Object.fromEntries(REFERENCES.filter(key =>
    (typeof context[key] === "string" && context[key] !== "") || Number.isFinite(context[key]))
    .map(key => [key, typeof context[key] === "string" ? context[key].slice(0, 200) : context[key]]));
  for (const key of ["project_ids", "run_ids", "session_ids"]) {
    if (Array.isArray(context[key]) && context[key].length) references[key] = context[key]
      .filter(value => typeof value === "string").slice(0, 6).map(value => value.slice(0, 200));
  }
  const evidence = Object.fromEntries(Object.entries({
    state: excerpt(context.state, 120),
    summary: excerpt(context.summary),
    impact: excerpt(context.impact, 400),
    recorded_action: excerpt(context.recorded_action, 400),
    recorded_at: excerpt(context.recorded_at, 80),
    references,
  }).filter(([, value]) => value !== ""));
  return `Explain this issue: ${excerpt(context.title, 200) || "Diagnostic issue"}\n` +
    "Propose the next action.\n\nDashboard diagnostic context (recorded evidence):\n" + JSON.stringify(evidence, null, 2);
}

export function conditionDiagnostic(condition, unavailable = false) {
  return {
    ...Object.fromEntries(REFERENCES.map(key => [key, condition[key]])),
    condition_id: condition.id,
    title: condition.name,
    state: unavailable ? `Last known: ${conditionSummary(condition)}. Current status is unavailable.` : conditionSummary(condition),
    summary: [conditionExplanation(condition), condition.model && `Model: ${condition.model}`,
      condition.http_status && `Response status: ${condition.http_status}`,
      condition.provider_error_code && `Error code: ${condition.provider_error_code}`].filter(Boolean).join("\n"),
    impact: conditionImpact(condition),
    recorded_at: condition.latest_failure_at || condition.verified_at,
    recorded_action: [condition.action, conditionRetry(condition)].filter(Boolean).join("\n"),
    project_ids: condition.projects,
    run_ids: condition.attempts?.map(attempt => attempt.run_id).filter(Boolean),
    session_ids: condition.attempts?.map(attempt => attempt.session_id).filter(Boolean),
  };
}

export function taskDiagnostic(row, detail) {
  const failure = detail.stalled ? detail.failure?.latest : null;
  return {
    title: detail.title, task_id: row.id, project_id: row.project,
    state: [detail.blocked && "Decision Pending", detail.stalled && "Retries Exhausted",
      detail.stale && "Heartbeat stopped", detail.connectionWait && "Waits for a connection",
      detail.projectInfrastructure && "Project infrastructure requires attention"].filter(Boolean).join("; "),
    summary: [detail.reason, failure?.condition, detail.projectInfrastructure?.condition].filter(Boolean).join("\n"),
    run_id: detail.run?.runID || failure?.runID,
    connection_id: detail.connectionWait?.connection_id,
    recorded_at: failure?.failedAt,
  };
}

export function setupDiagnostic(projectID, operation, title, summary) {
  return { title, summary, project_id: projectID, operation_id: operation?.id,
    revision_id: operation?.revision_id, state: operation?.status || "Setup requires attention",
    recorded_at: operation?.ended_at };
}

// A retry checks the history because a failed response can follow a successful send.
export function createDiagnosticChat({ api, openConversation }) {
  const conversations = new Map();
  const post = (path, body) => api(path, { method: "POST", body: JSON.stringify(body) });
  return async context => {
    const body = diagnosticMessage(context);
    let entry = conversations.get(body);
    if (!entry) { entry = {}; conversations.set(body, entry); }
    if (!entry.pending) {
      entry.pending = (async () => {
        if (!entry.session) entry.session = await post("/api/sessions", {});
        const id = entry.session.session_id;
        if (entry.uncertain) {
          const history = await api(`/api/sessions/${id}/history`, { maxAge: 0 });
          if (history.session_id !== id || !Array.isArray(history.messages)) throw new Error("The conversation history is unavailable.");
          entry.sent = history.messages.some(message => message.direction === "inbound" && message.body === body);
          entry.uncertain = false;
        }
        if (!entry.sent) {
          entry.uncertain = true;
          await post(`/api/sessions/${id}/send`, { body });
          entry.sent = true;
          entry.uncertain = false;
        }
        await openConversation(entry.session);
        conversations.delete(body);
      })().finally(() => { entry.pending = null; });
    }
    return entry.pending;
  };
}

const actionStates = new WeakMap();
let actionSequence = 0;

export function diagnosticButton(context, ask, testid = "diagnostic-homie") {
  if (!ask) return null;
  if (!actionStates.has(ask)) actionStates.set(ask, new Map());
  const states = actionStates.get(ask), key = diagnosticMessage(context);
  if (!states.has(key)) states.set(key, { id: `diagnostic-action-${++actionSequence}`, pending: false, error: "" });
  const state = states.get(key);
  const label = () => state.pending ? "Culler prepares the conversation…" : "Ask Homie";
  function paint() {
    for (const node of document.querySelectorAll(`[data-diagnostic-action="${state.id}"]`)) {
      const button = node.querySelector("button"), error = node.querySelector('[role="alert"]');
      button.disabled = state.pending;
      button.textContent = label();
      button.setAttribute("aria-busy", String(state.pending));
      error.textContent = state.error;
      error.hidden = !state.error;
    }
  }
  const error = el("span", { class: "system-helper", role: "alert", hidden: !state.error, text: state.error });
  const button = el("button", { type: "button", class: "btn small", testid, text: label(),
    disabled: state.pending, "aria-busy": String(state.pending),
    title: "Open a Homie conversation with this issue and its references.",
    onclick: async () => {
      if (state.pending) return;
      state.pending = true;
      state.error = "";
      paint();
      try { await ask(context); }
      catch (err) {
        state.error = `${err.message} Select Ask Homie to retry.`;
      } finally {
        state.pending = false;
        paint();
      }
    } });
  return el("span", { class: "diagnostic-homie-action", data: { diagnosticAction: state.id } }, button, error);
}
