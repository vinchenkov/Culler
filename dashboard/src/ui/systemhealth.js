import { el } from "./dom.js";
import { projectName, stampLabel } from "./format.js";

const replyRetries = new Set();

// A release label shows the version and the short commit, for example "0.1.0 (1a2b3c4d5e6f)".
// An unknown version gives the short commit only.
export function releaseLabel(version, commit) {
  const short = String(commit ?? "unknown").slice(0, 12);
  return typeof version === "string" && /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version) ? `${version} (${short})` : short;
}

export function releaseView(status) {
  const labels = {
    current: "Culler is current",
    available: "Update available",
    installed_newer: "The installed release is newer",
    none: "No complete release is available",
    unknown: "Release availability is unknown",
  };
  const workflow = status?.workflow;
  const version = workflow?.version;
  const previous = status?.available ? " The previous release remains available." : "";
  const preparation = workflow?.commit && version && workflow.commit !== status.available
    ? ({
      preparing: `Version ${version} is not available yet. Its workflow is in progress.`,
      failed: `The workflow for version ${version} failed.${previous}`,
      cancelled: `The workflow for version ${version} stopped before it completed.${previous}`,
      published: `GitHub completed the workflow for version ${version}. Check for updates again to refresh the available release.`,
    }[workflow.state]
      ?? `Version ${version} has no complete release. Its workflow state is unknown.`) : "";
  return {
    title: labels[status?.state] ?? labels.unknown,
    detail: status?.detail ?? "Check the network and GitHub access, then retry.",
    installed: status?.installed ?? "unknown",
    available: status?.available ?? "unknown",
    installedLabel: releaseLabel(status?.installed_version, status?.installed ?? "unknown"),
    availableLabel: releaseLabel(status?.available_version, status?.available ?? "unknown"),
    preparation,
    workflowURL: preparation && typeof workflow?.url === "string" && /^https:\/\/github\.com\/[^/]+\/[^/]+\/actions\/runs\/\d+$/.test(workflow.url) ? workflow.url : null,
  };
}

export function systemActionButton(condition, action, className = "btn rust") {
  if (condition.action !== "Retry reply") {
    return el("button", { class: className, type: "button", text: connectionTestAvailable(condition) ? "Test model" : condition.action, onclick: () => action(connectionTestAvailable(condition) ? { ...condition, action: "Test model" } : condition) });
  }
  const key = `${condition.session_id}:${condition.message_id}`;
  function updateButtons() {
    for (const button of document.querySelectorAll("[data-reply-retry]")) {
      const pending = replyRetries.has(button.dataset.replyRetry);
      button.disabled = pending;
      button.textContent = pending ? "Retry requested…" : "Retry reply";
      button.setAttribute("aria-busy", String(pending));
    }
  }
  return el("button", {
    class: className, type: "button", data: { replyRetry: key },
    text: replyRetries.has(key) ? "Retry requested…" : "Retry reply",
    disabled: replyRetries.has(key), "aria-busy": String(replyRetries.has(key)), "aria-live": "polite",
    onclick: async () => {
      if (replyRetries.has(key)) return;
      replyRetries.add(key);
      updateButtons();
      try { await action(condition); }
      finally { replyRetries.delete(key); updateButtons(); }
    },
  });
}

/** The viewer facts of a status response: the addresses and states of
 * AgentsView and of the wiki exports. The host part of the command supplies
 * them, so an incomplete response can still carry current facts. */
function viewerFacts(response) {
  const viewers = response?.viewers;
  return viewers !== null && typeof viewers === "object" && !Array.isArray(viewers) ? viewers : null;
}

export function acceptSystemStatus(previous, response, now) {
  if (response?.complete !== true || !Array.isArray(response.conditions)) {
    const known = new Map((previous.snapshot?.conditions ?? []).map(item => [item.id, item]));
    for (const item of response?.conditions ?? []) known.set(item.id, item);
    const connections = new Map((previous.snapshot?.connections ?? []).map(item => [item.id,item]));
    for (const item of response?.conditions ?? []) if (item.group === "connections") connections.set(item.id,item);
    const snapshot = response?.conditions?.length ? { ...previous.snapshot, conditions: [...known.values()], connections:[...connections.values()] } : previous.snapshot;
    return { ...previous, snapshot, viewers: viewerFacts(response) ?? previous.viewers ?? null, unavailable: true, pending: false };
  }
  const ready = new Map((response.connections ?? []).filter(item => item.state === "ready").map(item => [item.id, item]));
  const fresh = (previous.snapshot?.conditions ?? []).flatMap(item => {
    const current = ready.get(item.id);
    return current?.resolved_diagnostic_id === item.diagnostic_id && current?.recovery ? [current] : [];
  });
  const recoveredUntil = fresh.length ? now + 10_000 : previous.recoveredUntil ?? 0;
  const recovered = fresh.length ? fresh : now < recoveredUntil
    ? (previous.recovered ?? []).flatMap(item => ready.get(item.id)?.diagnostic_id === item.diagnostic_id ? [ready.get(item.id)] : []) : [];
  return { snapshot: response, viewers: viewerFacts(response), receivedAt: now, lastCompleteAt: response.observed_at, recovered, recoveredUntil, unavailable: false, pending: false };
}

export function createSystemStatus({ request, changed, now = Date.now, schedule = setTimeout, cancel = clearTimeout,
  hidden = () => typeof document !== "undefined" && document.visibilityState === "hidden" }) {
  let state = { snapshot: null, viewers: null, receivedAt: 0, unavailable: false, pending: true };
  let active = null;
  let timer = null;
  let serial = 0;
  let stopped = false;
  function publish() {
    if (!hidden()) changed({ ...state });
  }
  async function refresh() {
    if (stopped || active) return active;
    const generation = ++serial;
    const controller = new AbortController();
    let rejectDeadline;
    const timeout = new Promise((_, reject) => { rejectDeadline = reject; });
    const deadline = schedule(() => { controller.abort(); rejectDeadline(new Error("Status request expired.")); }, 10_000);
    active = (async () => {
      try {
        const response = await Promise.race([request(controller.signal), timeout]);
        if (generation !== serial || controller.signal.aborted || stopped) return;
        state = acceptSystemStatus(state, response, now());
      } catch {
        if (generation === serial && !stopped) state = { ...state, unavailable: true, pending: false };
      } finally {
        cancel(deadline);
        if (generation === serial) { active = null; publish(); }
      }
    })();
    return active;
  }
  function tick() {
    if (!hidden()) void refresh();
    timer = schedule(tick, 5_000);
  }
  publish();
  tick();
  return { refresh, stop() { stopped = true; serial++; cancel(timer); } };
}

// Deployment state changes only at an install or a redeploy. The status poll
// already crosses into the helper container every 5 seconds.
export const DEPLOYMENT_CHECK_MS = 60_000;

/** An incomplete inspection keeps the previous evidence and marks it unavailable. */
export function acceptDeploymentCheck(previous, inspection) {
  return inspection?.deployment_complete === false && previous
    ? { ...previous, unavailable: true, attempted_at: inspection.completed_at } : inspection;
}

export function createDeploymentCheck({ request, changed, hidden = () => false, now = Date.now, schedule = setTimeout, cancel = clearTimeout }) {
  let doctor = null;
  let active = null;
  let startedAt = -Infinity;
  let timer = null;
  function plan() { cancel(timer); timer = schedule(tick, DEPLOYMENT_CHECK_MS); }
  function tick() {
    if (hidden()) plan();
    else refresh().catch(() => {});
  }
  function refresh() {
    if (active) return active;
    plan();
    startedAt = now();
    changed(doctor, true);
    active = Promise.resolve().then(request).then(
      inspection => { doctor = acceptDeploymentCheck(doctor, inspection); },
      error => { doctor = { ...doctor, unavailable: true }; throw error; },
    ).finally(() => { active = null; changed(doctor, false); });
    return active;
  }
  tick();
  return {
    refresh,
    /** A page that becomes visible repeats a check older than the interval. */
    wake() {
      if (!active && !hidden() && now() - startedAt >= DEPLOYMENT_CHECK_MS) tick();
      return active;
    },
  };
}

/** Connections that require attention stay open. Connected and unused connections share one closed disclosure. */
export function connectionGroups(connections, conditions) {
  const active = new Set(conditions.map(item => item.id));
  const used = connections.filter(item => item.roles?.length || active.has(item.id));
  return {
    attention: used.filter(item => item.state !== "ready"),
    connected: used.filter(item => item.state === "ready"),
    unused: connections.filter(item => !item.roles?.length && !active.has(item.id)),
  };
}

export function connectionDisclosureLabel({ connected, unused }) {
  return [connected.length && `${connected.length} connected`, unused.length && `${unused.length} unused`].filter(Boolean).join(", ");
}

function roleName(role) { return role ? role[0].toUpperCase() + role.slice(1) : "Agent"; }

export function recoveryText(condition) {
  const attempt = condition.recovery;
  if (!attempt) return "The model responded.";
  const role = roleName(attempt.role);
  const task = attempt.task_id ? ` on task #${attempt.task_id}` : "";
  return attempt.state === "active" ? `The model responded. The ${role} continues${task}.`
    : ["starting", "stale", "stopping"].includes(attempt.state) ? `The model responded. Check the ${role} execution status.`
    : `The model responded. The ${role} attempt ended.`;
}

function attemptText(attempt) {
  const context = [attempt.project_name || attempt.project_id, attempt.task_id && `task #${attempt.task_id}`, attempt.session_id && `conversation ${attempt.session_id}`].filter(Boolean).join(" · ");
  const status = { active: "The attempt is active.", starting: "The first heartbeat is pending.", stale: "The heartbeat is stale." }[attempt.state] || "Check the execution status.";
  return `${roleName(attempt.role)}${context ? " · " + context : ""}. ${status}`;
}

export function conditionSummary(condition) {
  if (condition.group === "connections") {
    if (condition.state === "ready") return "Connected";
    if (condition.state === "retry_wait") {
      if (condition.attempts?.length) return condition.attempts.length === 1
        ? `${roleName(condition.attempts[0].role)} retry in progress` : "Connection retries in progress";
      return condition.retry_phase === "pending" ? "Retry pending" : "Connection interrupted";
    }
    if (condition.state === "unknown") return "Connection not verified";
    if (condition.action === "Reconnect") return "Reconnect required";
    if (condition.action === "Update credential") return "Credential requires attention";
    return condition.profile_id ? "Model access requires attention" : "Connection requires attention";
  }
  if (condition.id?.startsWith("infrastructure:")) {
    return "infra failed";
  }
  if (condition.proposal_decision_id) return "Decision Pending";
  if (condition.attempt_id) return "Collie could not reply";
  if (condition.allowance_condition === "verifier") return "Verifier correction limit reached";
  if (condition.task_id) return "Task requires attention";
  return condition.name;
}

/** The actions that a dashboard control carries out. Each other action is an
 * instruction for the operator, and the page shows it as text. */
const CONTROL_ACTIONS = new Set(["Retry connection", "Reconnect", "Update credential", "Replace API key", "Review connection", "Resume Project infrastructure", "Resolve proposal decision", "Retry reply", "Open task"]);

export function connectionTestAvailable(condition) {
  return condition?.group === "connections" && Boolean(condition.connection_id && condition.profile_id) &&
    condition.state === "retry_wait";
}

export function actionControl(condition) {
  if (connectionTestAvailable(condition)) return true;
  return Boolean(condition?.action) && condition.action !== "View details" &&
    CONTROL_ACTIONS.has(condition.action);
}

/** The repair instruction of a condition without a dashboard control, or "". */
export function repairText(condition) {
  if (!condition?.action || condition.action === "View details" || actionControl(condition)) return "";
  return ({
    "Restore credential access": "Unlock the login Keychain. Culler retries access to the saved credentials.",
    "Restore provider access": "Ask your coding harness to repair host provider access. Your credentials remain saved.",
    "Review request limits": "Wait for the provider request limit to reset, then retry.",
    "Review account allowance": "Review your provider quota or billing, then retry.",
  })[condition.action] ?? condition.action;
}

/** The heading of one condition: its name and its state, without a repeat. */
export function conditionTitle(condition) {
  const summary = conditionSummary(condition);
  const name = condition.profile_id ? `${condition.name} / ${condition.profile_id}` : condition.name;
  return summary === name ? name : `${name} · ${summary}`;
}

export function conditionImpact(condition) {
  if (condition.group === "connections") {
    if (condition.state === "ready" && condition.recovery) return recoveryText(condition);
    if (condition.state === "retry_wait") {
      if (condition.attempts?.length) return `${condition.attempts.map(attemptText).join(" ")} Culler awaits a complete model response.`;
      if (condition.retry_phase === "pending") return "The retry delay ended. The next attempt depends on the other execution requirements.";
      return `New attempts that use this ${condition.profile_id ? "profile" : "connection"} wait for the retry time.`;
    }
    const roles = condition.roles ?? [];
    if (!roles.length) return "No roles use this connection.";
    if (condition.state === "ready") return `${roles.length} ${roles.length === 1 ? "role uses" : "roles use"} this connection.`;
    const pipeline = roles.filter(role => role !== "collie").length;
    if (roles.includes("collie")) {
      return pipeline ? `Collie and ${pipeline} ${pipeline === 1 ? "pipeline role" : "pipeline roles"} cannot start.` : "Collie cannot start a reply.";
    }
    return `${pipeline} ${pipeline === 1 ? "pipeline role cannot" : "pipeline roles cannot"} start.`;
  }
  if (condition.proposal_decision_id) return "Project proposal work waits for this decision. Assigned task work follows the other Project controls.";
  if (condition.id?.startsWith("infrastructure:")) {
    return `${condition.failure_count} consecutive failures.${condition.state === "stopped" ? " New agent runs require your attention." : " New agent runs wait for the retry time."}`;
  }
  if (condition.attempt_id) return "Your message remains in the conversation.";
  if (condition.task_id && condition.project_priority) return `Task #${condition.task_id} prevents new work in Project ${condition.project_id}. You must address this task before unrelated Project work can continue. This priority hold is independent of an explicit pause or the three-task attention threshold.`;
  if (condition.task_id) return "This task needs your review before work can continue.";
  return condition.condition || "Open the details to review this issue.";
}

/** A shared decision represents only the task blocks that its resolution clears. */
export function systemIssues(conditions = []) {
  const owned = new Set();
  for (const condition of conditions) {
    if (!condition.proposal_decision_id || condition.task_id) continue;
    for (const task of condition.affected_tasks ?? []) {
      if (task.owns_block === true) owned.add(JSON.stringify([condition.project_id, task.task_id, condition.proposal_decision_id]));
    }
  }
  return conditions.filter(condition => !condition.task_id || condition.blocked_kind !== "proposal" || condition.stalled || condition.allowance_condition ||
    !owned.has(JSON.stringify([condition.project_id, condition.task_id, condition.block_decision_id])));
}

export function affectedProposalNodes(condition, action) {
  if (!condition.proposal_decision_id || !condition.affected_tasks?.length) return null;
  return el("div", { class: "system-affected-proposals" },
    el("p", { text: "Affected proposals:" }),
    el("ul", {}, condition.affected_tasks.map(task => el("li", {},
      el("button", { type: "button", class: "system-history-link", text: `Proposal #${task.task_id}`, title: task.title,
        onclick: () => action({ action: "Open task", id: `task:${task.task_id}`, task_id: task.task_id, project_id: condition.project_id }) })))));
}

export function systemConditionNode(condition, action, details, discuss = null) {
  const ready = condition.state === "ready";
  const unused = condition.group === "connections" && condition.state === "unknown" && !condition.roles?.length && !condition.diagnostic_id;
  return el("article", { class: `system-condition${ready ? " is-ready" : unused ? " is-unused" : " needs-attention"}`, "data-condition-id": condition.id },
    el("div", { class: "system-condition-copy" },
      el("div", { class: "system-condition-heading" },
        el("strong", { text: condition.name }),
        (unused || conditionSummary(condition) !== condition.name) &&
          el("span", { class: "system-state", text: unused ? "Not in use" : conditionSummary(condition) })),
      condition.profile_id && el("span", { class: "system-profile", text: `Agent profile: ${condition.profile_id}` }),
      el("p", { text: conditionImpact(condition) }),
      affectedProposalNodes(condition, action),
      !ready && repairText(condition) && el("p", { class: "system-repair-text", text: repairText(condition) })),
    el("div", { class: "system-actions" },
      !ready && !unused && actionControl(condition) && systemActionButton(condition, action),
      !ready && !unused && discuss?.(condition),
      el("button", { class: "btn ghost", type: "button", text: "View details", "aria-label": `View details for ${condition.name}${condition.profile_id ? " / " + condition.profile_id : ""}`, onclick: () => details(condition) })));
}

export function systemConditionNodes(conditions, action, details, discuss = null) {
  return [...conditions].sort((a, b) => Number(a.state === "ready") - Number(b.state === "ready"))
    .map(condition => systemConditionNode(condition, action, details, discuss));
}

export function systemNoticeNode(view, action, review, discuss = null) {
  const notice = systemFailureNoticeNode(view, action, review, discuss);
  const recoveries = view.unavailable ? [] : view.recovered ?? [];
  if (!recoveries.length) return notice;
  return el("div", {}, ...recoveries.map(condition => el("div", { class: "system-notice-row", role: "status", "data-recovery-id": condition.diagnostic_id },
    el("div", { class: "system-notice-copy" }, el("strong", { text: `${condition.name} / ${condition.profile_id}` }), el("p", { text: recoveryText(condition) })))), notice);
}

function systemFailureNoticeNode(view, action, review, discuss) {
  if (view.pending) return el("div", { class: "system-notice-pending", role: "status", "aria-busy": "true", text: "Loading system status…" });
  const conditions = systemIssues(view.snapshot?.conditions);
  if (!conditions.length && !view.unavailable) return null;
  const single = conditions.length === 1 ? conditions[0] : null;
  const title = view.unavailable ? "Status unavailable" : single
    ? conditionTitle(single) : `${conditions.length} issues need attention`;
  const connectionCount = new Set(conditions.filter(item => item.group === "connections").map(item => item.connection_id)).size;
  const impact = view.unavailable
    ? `${conditions.length ? `${conditions.length} known ${conditions.length === 1 ? "issue remains" : "issues remain"}. ` : ""}Culler cannot confirm the current status.`
    : single ? conditionImpact(single)
    : [connectionCount ? `${connectionCount} ${connectionCount === 1 ? "connection needs" : "connections need"} attention.` : "",
      "Review the current attempts and any required actions."].filter(Boolean).join(" ");
  return el("div", { class: `system-notice-row${view.unavailable ? " is-unavailable" : ""}`, "data-condition-id": single?.id },
    el("span", { class: "system-notice-mark", "aria-hidden": "true", text: "!" }),
    el("div", { class: "system-notice-copy", role: "status" },
      el("strong", { text: title }), el("p", { text: impact }),
      single && !view.unavailable && repairText(single) && el("p", { class: "system-repair-text", text: repairText(single) })),
    el("div", { class: "system-actions" },
      single && !view.unavailable && actionControl(single) && systemActionButton(single, action),
      single && discuss?.(single),
      el("button", { type: "button", class: `btn ${single && !view.unavailable ? "ghost" : "rust"}`, text: single && !view.unavailable ? "View details" : conditions.length ? "Review issues" : "System health", onclick: review })));
}

const NETWORK_ERRORS = {
  connect: "Could not connect",
  reset: "The response stream disconnected",
  timeout: "Request timed out",
  tls: "Secure connection failed",
  other: "Request failed",
};

export function conditionExplanation(condition) {
  if (condition.state === "ready") return "";
  if (condition.provider_error_code === "invalid_grant") return "The provider rejected the credential refresh.";
  if (condition.network_error) return `${NETWORK_ERRORS[condition.network_error] || "The request failed"}. ${condition.profile_id ? "The earlier model request did not complete." : "The connection check did not complete."}`;
  if (condition.group === "connections") {
    if (condition.http_status >= 400) return condition.profile_id
      ? "The provider rejected access to this model." : "The provider rejected the connection check.";
    return "Culler cannot confirm the cause of this access issue.";
  }
  return condition.condition || "";
}

export function conditionRetry(condition) {
  if (condition.state === "retry_wait" && condition.next_retry_at) {
    if (condition.attempts?.length) return "";
    return `${condition.retry_phase === "pending" || Date.parse(condition.next_retry_at) <= Date.now() ? "Retry delay ended" : "Earliest retry"}: ${stampLabel(condition.next_retry_at)}.`;
  }
  if (condition.state === "stopped" && (condition.group === "connections" || condition.id?.startsWith("infrastructure:"))) return "Automatic retries stopped.";
  return "";
}

export function conditionFacts(condition, projects = []) {
  const rows = [
    [condition.latest_failure_at ? "Failure recorded" : "Last verified", stampLabel(condition.latest_failure_at || condition.verified_at)],
    ["Recovery confirmed", condition.recovery && stampLabel(condition.verified_at)],
    ...((condition.attempts ?? []).flatMap(attempt => [
      ["Current attempt", attempt.run_id || attempt.attempt_id],
      ["Attempt started", stampLabel(attempt.started_at)],
      ["Execution status", attemptText(attempt)],
    ])),
    ["Provider response", [condition.http_status, condition.provider_error_code].filter(Boolean).join(" · ")],
    ["Network error", NETWORK_ERRORS[condition.network_error]],
    ["Affected roles", condition.group !== "connections" && condition.roles?.join(", ")],
    ["Project", (condition.projects ?? (condition.project_id ? [condition.project_id] : [])).map(id => projectName(id, projects)).join(", ")],
  ];
  return el("dl", { class: "system-facts" }, rows.filter(([,value]) => value).map(([label,value]) => [el("dt", { text: label }), el("dd", { text: value })]));
}

export function diagnosticResults(events) {
  return events.filter(event => event.outcome !== "started");
}

export function refreshSummary(total = {}) {
  const failures = total.failures ?? 0;
  const requests = total.requests ?? 0;
  return `${failures} ${failures === 1 ? "failure" : "failures"} from ${requests} refresh ${requests === 1 ? "request" : "requests"}`;
}
