import { proposalAssignment } from "./board.js";
import { projectName, relativeAge, secondsSince, stampLabel } from "./format.js";
import { leaseHeld } from "./board.js";

/** The spine's own outcome vocabulary (`endRun`): a run that finished the way
 * it was supposed to, and a run that did not. An outcome in neither set is a
 * fact this page has no reading for — it is shown plainly rather than painted
 * as a failure it may not be. */
const GOOD_OUTCOMES = new Set(["completed", "setup-complete"]);
const BAD_OUTCOMES = new Set(["blocked", "infra-failed", "reaped"]);

/** A heartbeat interval to fall back on when the settings row has not been
 * read. Shorter than the spine's own default, so an unknown cadence errs
 * toward flagging a silent lease rather than toward calling it live. */
const HEARTBEAT_INTERVAL_FALLBACK_S = 30;

/** Whether a heartbeat is silent: three missed beats against the settings
 * row's interval is silence, not jitter. A run with no heartbeat is not
 * silent. */
export function heartbeatStopped(lastHeartbeatAt, nowMs, settings) {
  const interval =
    typeof settings?.heartbeat_interval_s === "number"
      ? settings.heartbeat_interval_s
      : HEARTBEAT_INTERVAL_FALLBACK_S;
  const secs = secondsSince(lastHeartbeatAt, nowMs);
  return secs !== null && secs > 3 * interval;
}

/** Whether a held lease's heartbeat is silent. A free lease is never stale.
 * The topology graph and the degraded deep-links both read this one rule. */
export function leaseStale(lease, nowMs, settings) {
  return leaseHeld(lease) && heartbeatStopped(lease.last_heartbeat_at, nowMs, settings);
}

/** Per-surface undelivered outbox counts. A capped count is a FLOOR — the poll
 * limit was reached, so the real backlog is at least this big — and both the
 * row and the total carry that flag so the page can never print it as exact. */
export function outboxLagView(surfaces) {
  const rows = (surfaces ?? []).map((s) => {
    const undelivered = typeof s.undelivered === "number" ? s.undelivered : 0;
    const capped = s.capped === true;
    return {
      surface: s.surface ?? "(unknown surface)",
      undelivered,
      capped,
      oldest: s.oldest_created_at ?? null,
      tone: capped ? "rust" : undelivered === 0 ? "leaf" : undelivered < 10 ? "sky" : "rust",
    };
  });

  return {
    total: rows.reduce((n, r) => n + r.undelivered, 0),
    capped: rows.some((r) => r.capped),
    rows,
  };
}

/** Why a failed run ended, in the operator's words. The reason codes appear
 * only in the run diagnostics. */
const END_REASONS = {
  agent: "The agent reported a failure",
  harness: "The agent program stopped",
  launch: "The run did not start",
  repository: "The repository step failed",
  network: "Network error",
  provider: "Provider error",
  connection: "The connection was unavailable",
  cancelled: "Cancelled",
  interrupted: "Interrupted",
  "spawn-watchdog": "The run did not start in time",
  "lease-timeout": "The heartbeat stopped",
  "hard-deadline": "The run reached its time limit",
  "container-gone": "The container stopped",
  "vm-restart": "Docker restarted",
};

const OUTCOMES = {
  running: "Running",
  completed: "Completed",
  "setup-complete": "Setup completed",
  blocked: "Decision Pending",
  obsolete: "No longer needed",
  "infra-failed": "Failed",
  reaped: "Stopped",
  cancelled: "Cancelled",
  interrupted: "Interrupted",
};

/** Whether the run is a Verifier run that sent the task back to the Worker. */
export function requestedCorrection(run) {
  return run?.verdict_outcome === "correct";
}

/** The wiki update of a run row, when it states a result text. Null for each
 * other row: `mc run list` states no result text while the Scribe run is
 * active. */
function statedWikiUpdate(run) {
  const update = run?.wiki_update;
  return typeof update?.result_text === "string" && update.result_text !== "" ? update : null;
}

/** The end of one run in plain words: the result text of its wiki update,
 * else the end reason of a failed run, else the correction request, else the
 * outcome. */
export function runResult(run) {
  const update = statedWikiUpdate(run);
  if (update) return update.result_text;
  const running = (run?.outcome === null || run?.outcome === undefined) && (run?.ended_at === null || run?.ended_at === undefined);
  if (running) return run.agent_started_at === null ? "Agent launch pending" : OUTCOMES.running;
  if (END_REASONS[run.end_reason]) return END_REASONS[run.end_reason];
  if (requestedCorrection(run)) return "Requested a correction";
  return OUTCOMES[run.outcome] ?? "Ended";
}

/** Whether a run is a repository operation. A repository operation uses no
 * agent, so no page shows it as a run. */
export function mergeStep(run) {
  return run?.binding === "mechanical";
}

/** The tone of a wiki update: an active wiki update has no result. */
function wikiUpdateTone(update) {
  return update.result === "failed" ? "rust" : update.result === "succeeded" ? "leaf" : "sky";
}

/** The most recent runs, NEWEST FIRST. `mc run list` orders by creation, so the
 * newest tail is taken and then reversed — a stream reads top-down. A row of
 * a wiki update that states its result takes its result, its tone, and the
 * cause of a failure (`detail`) from the wiki update. */
export function runsView(runs, nowMs, limit = 12) {
  const rows = (runs ?? []).filter((r) => !mergeStep(r)).slice(-limit).reverse();
  return rows.map((r) => {
    // Nothing is ever "in progress" in the records; a run is in flight
    // exactly when it has neither an outcome nor an end.
    const running =
      (r.outcome === null || r.outcome === undefined) &&
      (r.ended_at === null || r.ended_at === undefined);
    const outcome = r.outcome ?? (running ? "running" : "unknown");
    const update = statedWikiUpdate(r);
    return {
      id: r.id ?? null,
      tier: r.tier ?? null,
      role: proposalAssignment(r).length ? "Arbiter proposal review" : r.role ?? null,
      proposalIDs: proposalAssignment(r),
      // The AgentsView session of the main trace, as `mc run list` computes it.
      trace: r.agentsview ?? null,
      subject: typeof r.subject === "number" ? r.subject : null,
      outcome,
      result: runResult(r),
      detail: update?.cause_text ?? null,
      wikiUpdate: update !== null,
      running,
      age: relativeAge(r.created_at, nowMs) || null,
      stamp: stampLabel(r.created_at) || null,
      tone: update
        ? wikiUpdateTone(update)
        : running
          ? "sky"
          : requestedCorrection(r) && !BAD_OUTCOMES.has(outcome)
            ? "stone"
            : GOOD_OUTCOMES.has(outcome)
              ? "leaf"
              : BAD_OUTCOMES.has(outcome)
                ? "rust"
                : "stone",
    };
  });
}

/** One verb's findings as view rows. `mc doctor` and `mc health` return the
 * same three keys with the same three-valued status, so they share this — a
 * second copy would be a second place for `deferred` to be mistaken for a
 * failure, which is the bug this shape already had once. */
function mapFindings(raw) {
  return (raw ?? []).map((f) => {
    const status = f.status === "ok" || f.status === "deferred" ? f.status : "fail";
    return {
      check: f.check ?? "(unnamed check)",
      status,
      ok: status === "ok",
      failing: status === "fail",
      label: status === "ok" ? "ok" : status === "deferred" ? "deferred" : "FAIL",
      detail: f.detail ?? null,
      tone: status === "ok" ? "leaf" : status === "deferred" ? "stone" : "rust",
    };
  });
}

/** The host plane, from `mc health`. The verb opens no spine and spawns no
 * container, so it keeps answering on the days the doctor beside it cannot.
 *
 * `attention` is the verb's own rendering of the error and warning records it
 * counted — text, already rendered server-side, and NOT parsed here. The rule
 * the System Logs page follows applies just as hard on this pane: the browser
 * does not interpret a log line. AgentsView shows the traces. */
export function healthView(health) {
  if (!health) {
    return { state: "unchecked", summary: "not read yet", findings: [], attention: [] };
  }
  const findings = mapFindings(health.findings);
  const failing = findings.filter((f) => f.failing).length;
  const deferred = findings.filter((f) => f.status === "deferred").length;
  const noun = findings.length === 1 ? "check" : "checks";
  const passed = findings.filter(f => f.status === "ok").length;
  const tail = `${passed} passed, ${failing} failed`;
  const attention = (health.attention ?? []).filter((line) => typeof line === "string");
  return {
    state: health.availability ? (!health.availability.available ? "unavailable" : health.availability.can_commit ? "ok" : "failing") : health.ok === true ? "ok" : "failing",
    summary: health.availability ? (health.availability.available ? (health.availability.can_commit ? "The resident can save system state." : "The resident cannot save system state.") : "No recent response from the resident.") : `${findings.length} ${noun}, ${tail}${deferred === 0 ? "" : `, ${deferred} deferred`}`,
    findings,
    attention,
  };
}

/** Deployment findings. Null means no inspection result yet; an unchecked
 * deployment is not a healthy one. */
export function doctorView(doctor) {
  if (!doctor) {
    return { state: "unchecked", summary: "not checked this session", findings: [] };
  }
  const findings = mapFindings(doctor.findings).filter(finding => doctor.deployment_ok === undefined || finding.check !== "runtime-auth");
  const failing = findings.filter((f) => f.failing).length;
  const deferred = findings.filter((f) => f.status === "deferred").length;
  const noun = findings.length === 1 ? "check" : "checks";
  const passed = findings.filter(f => f.status === "ok").length;
  const tail = `${passed} passed, ${failing} failed`;
  return {
    state: doctor.unavailable || doctor.deployment_complete === false ? "unavailable" : doctor.deployment_ok === true || doctor.ok === true ? "ok" : "failing",
    checkedAt: doctor.completed_at ?? null,
    summary: `${findings.length} ${noun}, ${tail}${deferred === 0 ? "" : `, ${deferred} deferred`}`,
    findings,
  };
}

/** The System Logs tails worth opening when something is degraded, from the
 * doctor VIEW (not its raw input) and the lease rows. Pipeline and Activity
 * are in-band — they can only show what something successfully wrote to the
 * spine; when a unit is failing before it can write, its stderr is the only
 * record, which is what these deep-links reach. */
export function degradedUnits(doctorVM, leases, nowMs, settings, projects = []) {
  const found = [];
  const push = (unit, stream, reason) => {
    if (found.some((r) => r.unit === unit && r.stream === stream)) return;
    found.push({ unit, stream, reason });
  };

  const stale = (leases ?? [])
    .filter((l) => leaseStale(l, nowMs, settings))
    .map((l) => projectName(l.project, projects));
  if (stale.length > 0) {
    push(
      "resident",
      "stderr",
      `the lease heartbeat of ${stale.join(", ")} is stale — the runner may have disappeared`,
    );
  }
  for (const f of doctorVM?.findings ?? []) {
    // Only a real `fail` earns a log deep-link. A deferred check has written
    // nothing to any log, so pointing the operator at a tail is a dead end.
    if (!f.failing) continue;
    // AgentsView writes its own logs, and the dashboard process serves the
    // wiki address.
    if (f.check === "agentsview" || f.check === "wiki") {
      push(f.check === "agentsview" ? "agentsview" : "dashboard", "stderr", `doctor check "${f.check}" is failing`);
      continue;
    }
    push("resident", "stderr", `doctor check "${f.check}" is failing`);
    const text = `${f.check ?? ""} ${f.detail ?? ""}`.toLowerCase();
    if (text.includes("dashboard")) {
      push("dashboard", "stderr", `doctor check "${f.check}" is failing`);
    }
  }
  return found;
}
