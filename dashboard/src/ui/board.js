// Board facts come from the task records and the Project leases.

import { projectName, ageLabel, relativeAge, secondsSince, shortAge, utcMs } from "./format.js";
import { STAGES, STAGE_META, stageLabel, stageTone } from "./stages.js";

export function taskBlocked(task) {
  return task?.blocked === 1 || task?.blocked === true;
}

export function taskStalled(task) {
  return task?.stalled === true || task?.stalled === 1;
}

export function infrastructureFailureView(task) {
  const count = task?.infrastructure_failure_count ?? 0;
  const failure = task?.latest_infrastructure_failure;
  if (!failure) return { count, latest: null };
  const evidence = typeof failure.evidence === "string" && failure.evidence.trim() ? failure.evidence : null;
  const cause = evidence && ["credentials", "quota"].includes(failure.cause) ? failure.cause : "unknown";
  const causeLabel = { unknown: "Unknown cause", credentials: "Credential failure", quota: "Quota failure" }[cause];
  return {
    count,
    latest: {
      taskID: failure.task_id ?? task.id,
      runID: failure.run_id,
      condition: failure.condition,
      source: failure.source,
      failedAt: failure.failed_at,
      cause,
      diagnosis: failure.source === "agent" && cause !== "unknown" ? `Agent diagnosis: ${causeLabel}` : causeLabel,
      evidence,
      operatorAction: cause !== "unknown" ? failure.operator_action || null : null,
      quotaResetAt: cause === "quota" ? failure.quota_reset_at || null : null,
    },
  };
}

// Each Project has its own attention count, including on a filtered Board.
export function boardAttention(tasks) {
  const distinct = new Map();
  for (const task of tasks ?? []) {
    if (!task || task.archived === 1 || task.archived === true || distinct.has(task.id)) continue;
    if (!taskBlocked(task) && !taskStalled(task)) continue;
    distinct.set(task.id, {
      id: task.id,
      title: task.title || "(untitled)",
      project: task.project,
      blocked: taskBlocked(task),
      stalled: taskStalled(task),
      reason: taskBlocked(task) ? task.blocked_reason || "(no reason recorded)" : null,
      failure: infrastructureFailureView(task),
    });
  }
  const rows = [...distinct.values()];
  const counts = new Map();
  for (const row of rows) counts.set(row.project, (counts.get(row.project) ?? 0) + 1);
  const projects = [...counts].sort(([a], [b]) => String(a).localeCompare(String(b)))
    .map(([project, count]) => ({ project, count, paused: count >= 3 }));
  return { count: rows.length, limit: 3, paused: projects.some((project) => project.paused), projects, tasks: rows };
}

/** Whether a lease row is held. `mc lease list` says so with `status`; the
 * null-ness of `run_id` is the same fact from the row's own columns. */
export function leaseHeld(lease) {
  return Boolean(lease && lease.run_id !== null && lease.run_id !== undefined);
}

/** Whether a held lease belongs to an agent run. */
export function agentLease(lease) {
  return leaseHeld(lease);
}

/** The lease rows that agent runs hold, in the verb's (Project id) order. */
export function heldLeases(leases) {
  return (leases ?? []).filter(agentLease);
}

function stopPending(lease) {
  return leaseHeld(lease) && (lease.run_outcome != null || lease.run_ended_at != null);
}

/** The task id holding one Project's lease, or null. A free lease has a
 * null run_id and a subjectless lease (a propose run) has a null
 * subject — neither marks a card. */
export function activeTaskID(lease) {
  return agentLease(lease) && !stopPending(lease) && typeof lease.subject === "number" ? lease.subject : null;
}

function projectInfrastructureHold(task, nowMs) {
  const condition = task.project_infrastructure;
  if (!condition) return null;
  return condition.remaining === 0 || Date.parse(condition.retry_at) > nowMs ? condition : null;
}

export function proposalAssignment(run) {
  if (run?.subject != null || (run?.run_role ?? run?.role ?? run?.owner) !== "arbiter") return [];
  try { const ids = typeof run.pool_snapshot === "string" ? JSON.parse(run.pool_snapshot) : run.pool_snapshot; return Array.isArray(ids) ? ids : []; }
  catch { return []; }
}

export function taskRuns(runs, task) {
  return (runs ?? []).filter(run => run.project === task.project &&
    (run.subject === task.id || proposalAssignment(run).includes(task.id)));
}

function leaseOfTask(leases, taskID, project) {
  return heldLeases(leases).find(lease => lease.project === project &&
    (lease.subject === taskID || proposalAssignment(lease).includes(taskID))) ?? null;
}

// A heartbeat older than three intervals is the one fact that separates a dead
// lease from a busy one; the interval is the settings row's, and defaults to
// the compiled 30s when the row has not been read yet.
const DEFAULT_HEARTBEAT_INTERVAL_S = 30;
const STALE_INTERVALS = 3;

function heartbeatInterval(settings) {
  return typeof settings?.heartbeat_interval_s === "number"
    ? settings.heartbeat_interval_s
    : DEFAULT_HEARTBEAT_INTERVAL_S;
}

/** One lease row as the badge reads it. A free lease reports nothing rather
 * than zeros — a "0s" heartbeat on an unheld lease is a lie about liveness.
 * `settings` is the settings row (its heartbeat interval sets the staleness
 * threshold). */
export function leaseView(lease, nowMs, settings) {
  const project = typeof lease?.project === "string" ? lease.project : null;
  if (!agentLease(lease)) {
    return {
      held: false,
      project,
      owner: null,
      runID: null,
      subject: null,
      heartbeat: "",
      heartbeatSeconds: null,
      stale: false,
      stopPending: false,
      acquiredAge: "",
      runAge: "",
    };
  }
  const interval = heartbeatInterval(settings);
  const heartbeatSeconds = secondsSince(lease.last_heartbeat_at, nowMs);
  return {
    held: true,
    project,
    owner: lease.owner ?? null,
    runID: lease.run_id ?? null,
    proposalIDs: proposalAssignment(lease),
    starting: lease.agent_started_at === null,
    subject: typeof lease.subject === "number" ? lease.subject : null,
    heartbeat: shortAge(lease.last_heartbeat_at, nowMs),
    heartbeatSeconds,
    stale: !stopPending(lease) && heartbeatSeconds !== null && heartbeatSeconds > interval * STALE_INTERVALS,
    stopPending: stopPending(lease),
    acquiredAge: relativeAge(lease.acquired_at, nowMs),
    runAge: shortAge(lease.acquired_at, nowMs),
  };
}

/** Every lease row as the badge reads it, one per Project in the verb's
 * order. */
export function leasesView(leases, nowMs, settings) {
  return (leases ?? []).map((lease) => leaseView(lease, nowMs, settings));
}

function roleLabel(owner) {
  return typeof owner === "string" && owner !== "" ? owner[0].toUpperCase() + owner.slice(1) : "Agent";
}

/** One run in the operator's words: the role, its task, and its state. A
 * problem comes first, so a truncated badge still shows it. A run in a
 * paused Project is finishing: no new run follows it. */
function runText(view, finishing = false) {
  const role = roleLabel(view.owner);
  if (!view.stopPending && view.starting) return `${role} agent launch pending`;
  if (!view.stopPending && view.proposalIDs?.length) return `Arbiter reviews ${view.proposalIDs.length} proposals · 1 agent run`;
  const task = view.subject === null ? "" : ` on task #${view.subject}`;
  if (view.stopPending) return `Stopping ${role}${task}`;
  if (view.stale) return `Heartbeat stopped ${view.heartbeat} ago · ${role}${task}`;
  const what = `${role} ${finishing ? "finishing run" : "running"}${task}`;
  return view.runAge ? `${what} · ${view.runAge}` : what;
}

function plural(count, word) {
  return `${count} ${word}${count === 1 ? "" : "s"}`;
}

/** The header badge over the leases: how many are held, whether any is
 * stale, and the copy. The copy has a fixed form for each page type, so a
 * second run never lengthens it. A Project page describes the one run of its
 * Project. A global page counts the runs and names each run in the tooltip.
 * `paused` holds the ids of the paused Projects. `titles` maps a Project id
 * to its title. */
export function leaseBadge(leases, nowMs, settings, { allProjects = false, paused = null, titles = null } = {}) {
  const held = leasesView(leases, nowMs, settings).filter((v) => v.held);
  const stale = held.some((v) => v.stale);
  const finishing = (v) => Boolean(paused?.has(v.project));
  if (held.length === 0) {
    return {
      held: 0,
      stale: false,
      text: "No agents are running",
      title: allProjects ? "No pipeline agents are active in any Project." : "No pipeline agent is active in this Project.",
    };
  }
  if (held.length === 1 && !allProjects) {
    const [v] = held;
    const title = v.stopPending
      ? "The resident must confirm that the container stopped before another run can use this lease."
      : v.stale
        ? `The last heartbeat arrived ${v.heartbeat} ago. The runner may have disappeared.`
        : `${v.starting ? "Culler claimed run" : "Run"} ${v.runID ?? "?"} ${v.starting ? "and awaits the agent process start" : `started ${v.runAge || "0s"} ago`}. ${v.heartbeat ? `The last heartbeat arrived ${v.heartbeat} ago.` : "The run sent no heartbeat yet."}` +
          (finishing(v) ? " The Project is paused, so no new run starts after this run." : "");
    return { held: 1, stale, text: runText(v, finishing(v)), title, proposalReview: v.proposalIDs?.length > 0 };
  }
  const live = held.filter((v) => !v.stopPending);
  const pendingCount = live.filter(v => v.starting).length;
  const started = live.filter(v => !v.starting);
  const finishingCount = started.filter(finishing).length;
  const running = started.length - finishingCount;
  const staleCount = held.filter((v) => v.stale).length;
  const parts = [];
  if (staleCount > 0) parts.push(`${plural(staleCount, "heartbeat")} stopped`);
  if (pendingCount > 0) parts.push(`${pendingCount} agent ${pendingCount === 1 ? "launch" : "launches"} pending`);
  if (running > 0) parts.push(`${plural(running, "agent")} running`);
  if (finishingCount > 0) parts.push(`${plural(finishingCount, "agent")} finishing ${finishingCount === 1 ? "run" : "runs"}`);
  if (held.length > live.length) parts.push(`${plural(held.length - live.length, "agent")} stopping`);
  const leaseProjectName = (id) => titles?.get(id) || "Project name unavailable";
  return {
    held: held.length,
    stale,
    text: parts.join(" · "),
    title: held.map((v) => `${leaseProjectName(v.project)}: ${runText(v, finishing(v))}`).join("\n"),
  };
}

/** The project filter's choices, "all" first. Built from the rows on hand
 * so a filter can never offer a project the board cannot show. */
export function projectOptions(tasks) {
  const seen = new Set();
  for (const task of tasks ?? []) {
    if (task && typeof task.project === "string" && task.project !== "") seen.add(task.project);
  }
  return ["all", ...[...seen].sort()];
}

/** True when the Review reader can open the packet of task `id`. The Review
 * page lists a packet only after merge preparation, and a requirements
 * revision replaces the packet. */
function readerPacket(packets, id) {
  return (packets ?? []).some((p) => p.task_id === id && !p.archived && p.review_state !== "replaced");
}

export function taskTags(task, { showProject = true, projects = [] } = {}) {
  const tags = [];
  if (task?.priority === -1) tags.push({ label: "expedited", kind: "priority" });
  if (showProject && task?.project) tags.push({ label: projectName(task.project, projects), kind: "project" });
  return tags;
}

/** The running chip's text. A stale heartbeat must not read as running: a
 * dead runner that says "Running now" is a lie about liveness. */
export function runningLabel(view) {
  if (!view.stopPending && view.starting) return "Agent launch pending";
  if (view.stopPending) return "Stopping";
  if (view.proposalIDs?.length) return "Under Arbiter proposal review";
  if (view.stale) return "Heartbeat stopped";
  return view.acquiredAge && view.acquiredAge !== "0m" ? `Running now · ${view.acquiredAge}` : "Running now";
}

function matchesProject(task, project) {
  return project === "all" || task.project === project;
}

function stageMs(task) {
  const ms = utcMs(task.stage_entered_at ?? task.created_at);
  // A row with no usable stamp sorts last: an unknown wait cannot claim to be
  // the longest one.
  return Number.isNaN(ms) ? Infinity : ms;
}

// Active cards precede expedited cards, attention cards, and older stage entries.
// Several proposal cards can refer to the same Project run.
function cardOrder(a, b) {
  if (a.active !== b.active) return a.active ? -1 : 1;
  if (a.expedited !== b.expedited) return a.expedited ? -1 : 1;
  if (a.attention !== b.attention) return a.attention ? -1 : 1;
  return a.sortMs - b.sortMs;
}

/** One column per task status, in pipeline order, each with its filtered and
 * ordered cards. Columns are always all five: a stage with no work is a fact
 * about the pipeline, not a reason to hide the stage. `leases` is every lease
 * row; active cards match the subject or the assigned proposal set. */
export function boardColumns(tasks, leases, { project = "all", nowMs, settings, packets = [], projects = [] } = {}) {
  const all = (tasks ?? []).filter((t) => t && t.archived !== 1 && t.archived !== true);
  const rows = all.filter((t) => matchesProject(t, project));
  // The Project pill repeats the filter when the filter names one, and
  // repeats itself when the whole board has one.
  const showProject = project === "all" && projectOptions(all).length > 2;

  return STAGES.map((status) => {
    const meta = STAGE_META[status];
    const cards = rows
      .filter((task) => task.status === status)
      .map((task) => {
        const lease = leaseOfTask(leases, task.id, task.project);
        const view = lease !== null ? leaseView(lease, nowMs, settings) : null;
        const active = view !== null && !view.stopPending;
        const blocked = taskBlocked(task);
        const stalled = taskStalled(task);
        return {
          id: task.id,
          title: task.title ? task.title : "(untitled)",
          tags: taskTags(task, { showProject, projects }),
          active,
          stopPending: view?.stopPending ?? false,
          blocked,
          proposalDecisionID: task.blocked_kind === "proposal" ? task.proposal_decision_id : null,
          stalled,
          attention: blocked || stalled,
          connectionWait: task.connection_wait ?? null,
          projectInfrastructure: projectInfrastructureHold(task,nowMs),
          packet: readerPacket(packets, task.id) ? task.id : null,
          approved: task.decision === "approved",
          reason: blocked ? (task.blocked_reason ? task.blocked_reason : "(no reason recorded)") : null,
          age: relativeAge(task.created_at, nowMs),
          stageAge: relativeAge(task.stage_entered_at ?? task.created_at, nowMs),
          expedited: task.priority === -1,
          scope: task.scope ?? null,
          project: task.project ?? null,
          // A card that does not hold its Project's lease has no run:
          // showing a lease's run id on every card would claim five runs
          // where one Project allows one.
          runID: active ? view.runID : null,
          heartbeat: active ? view.heartbeat : null,
          stale: active ? view.stale : false,
          running: view ? runningLabel(view) : null,
          sortMs: stageMs(task),
        };
      })
      .sort(cardOrder)
      .map(({ sortMs, ...card }) => card);

    return {
      status,
      label: meta.label,
      tone: meta.tone,
      count: cards.length,
      blockedCount: cards.filter((c) => c.blocked).length,
      stalledCount: cards.filter((c) => c.stalled).length,
      attentionCount: cards.filter((c) => c.attention).length,
      cards,
    };
  });
}

/** The task dialog: the one `mc task get` row plus what the live list and
 * the leases say about it. Everything a glance cannot use lives here — the
 * prose, the run, and the decision. `row` is the get verb's row; the
 * list row stands in for it while the get is in flight. */
export function taskDetail(row, { tasks, leases, nowMs, settings, packets = [], projects = [] } = {}) {
  const lease = leaseOfTask(leases, row.id, row.project);
  const view = lease === null ? null : leaseView(lease, nowMs, settings);
  const text = (s) => (typeof s === "string" && s.trim() !== "" ? s : null);
  return {
    id: row.id,
    archived: Boolean(row.archived),
    scope: row.scope ?? "task",
    title: row.title ? row.title : "(untitled)",
    tags: taskTags(row, { showProject: true, projects }),
    blocked: taskBlocked(row),
    stalled: taskStalled(row),
    connectionWait: row.connection_wait ?? null,
    projectInfrastructure: projectInfrastructureHold(row,nowMs),
    failure: infrastructureFailureView(row),
    allowance: row.delivery ? {
      correction_count: row.delivery.correction_count,
      verifier_correction_open: row.delivery.verifier_correction_open,
      allowance_condition: row.delivery.allowance_condition, allowance_reason: row.delivery.allowance_reason,
      latest_conflict: row.delivery.latest_conflict,
      verifier_report: row.delivery.verifier_report,
    } : null,
    projectHold: Boolean(row.delivery?.project_priority && (taskBlocked(row) || taskStalled(row))),
    projectName: projectName(row.project, projects),
    projectPaused: projects.some(project => project.id === row.project && project.status === "paused"),
    runAllowance: { remaining: row.dispatch_retries ?? 3, retryAt: row.run_retry_at ?? null },
    running: view === null ? null : runningLabel(view),
    stopPending: view?.stopPending ?? false,
    stale: view === null ? false : view.stale,
    stageTone: stageTone(row.status),
    description: text(row.description),
    requirementsPending: Boolean(row.pending_revision),
    reason: row.blocked === 1 || row.blocked === true ? (text(row.blocked_reason) ?? "(no reason recorded)") : null,
    run:
      view === null
        ? null
        : { role: view.owner, runID: view.runID, starting: view.starting, proposalIDs: view.proposalIDs, heartbeat: view.heartbeat, stale: view.stale, age: view.acquiredAge, stopPending: view.stopPending },
    decision: text(row.decision),
    decidedAge: ageLabel(row.decided_at, nowMs) || null,
    mergeStateText: mergeStateText(row, packets),
    packet: readerPacket(packets, row.id) ? row.id : null,
    origin: text(row.origin),
    createdAge: ageLabel(row.created_at, nowMs) || null,
    // A wait under a minute is not a wait worth reading.
    stageAge: (() => {
      const age = relativeAge(row.stage_entered_at ?? row.created_at, nowMs);
      return age === "" || age === "0m" ? null : age;
    })(),
    stage: stageLabel(row.status),
  };
}

/** The merge state text of an approved task, from its packet row. */
function mergeStateText(row, packets) {
  if (row.decision !== "approved" || row.archived === 1 || row.archived === true) return null;
  const packet = (packets ?? []).find((p) => p.task_id === row.id && p.review_state === "approved");
  return typeof packet?.merge_state_text === "string" && packet.merge_state_text !== "" ? packet.merge_state_text : null;
}

/** The board's header counters, over the same filtered set the columns show —
 * a total that ignored the filter would not describe what is on screen.
 * `activeIDs` are the subject tasks of the held leases inside the filter, in
 * Project order. */
export function boardStats(tasks, leases, { project = "all" } = {}) {
  const rows = (tasks ?? []).filter((t) => t && t.archived !== 1 && t.archived !== true && matchesProject(t, project));
  const activeIDs = heldLeases(leases)
    .filter((l) => project === "all" || l.project === project)
    .map(activeTaskID)
    .filter((id) => id !== null);
  return {
    total: rows.length,
    blocked: rows.filter(taskBlocked).length,
    stalled: rows.filter(taskStalled).length,
    expedited: rows.filter((t) => t.priority === -1).length,
    activeIDs,
    activeRuns: heldLeases(leases).filter(l => !stopPending(l) && (project === "all" || l.project === project)).length,
  };
}

/** The stats bar items in display order. Blocked and active work lead the
 * row with more weight; a stat with a zero count recedes. Each item has a
 * stable `key`; the label is sentence-case display text. */
export function boardStatItems(stats) {
  const active = stats.activeIDs ?? [];
  const runs = stats.activeRuns ?? active.length;
  return [
    { key: "blocked", label: "Blocked tasks", title: "Tasks that await an operator decision. Tasks can share one decision.", value: stats.blocked, lead: true, zero: stats.blocked === 0, alert: stats.blocked > 0 },
    { key: "active", label: "Agent runs", value: runs, lead: true, zero: runs === 0 },
    { key: "total", label: "Tasks", value: stats.total, zero: stats.total === 0 },
    { key: "stalled", label: "Stalled", value: stats.stalled, zero: stats.stalled === 0 },
    // An expedited count shows only when some task is expedited.
    ...(stats.expedited > 0 ? [{ key: "expedited", label: "Expedited", value: stats.expedited }] : []),
  ].map((item) => ({ lead: false, zero: false, alert: false, ...item }));
}

/** The host supplies the current file status. The browser selects the notices. */
export function visionNotices(projects) {
  return (projects ?? [])
    .filter((w) => w && typeof w.id === "string" &&
      w.status !== "archived" && w.vision_status?.ready === false)
    .map((w) => ({
      id: w.id,
      title: w.title || "Project name unavailable",
      state: w.vision_status.state,
      path: w.vision_status.path,
      reason: w.vision_status.reason,
    }))
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}
