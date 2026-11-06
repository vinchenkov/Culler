// The graph uses a node key for each role and an agent name for spine records.

import { ageLabel, relativeAge, utcMs } from "./format.js";
import { heartbeatStopped, leaseStale, mergeStep, runResult } from "./pipeline.js";

/** Runs that ended less than this ago still read as warm on the graph. */
const RECENT_MS = 30 * 60 * 1000;

/** One node per agent role. `name` is the word the graph shows; `label` is
 * the full name the disclosure shows. `from`/`to` are the statuses the role
 * consumes and produces — null where the role moves no task at all. */
export const TOPOLOGY_NODES = [
  {
    role: "strategist",
    agent: "strategist",
    name: "Strategist",
    label: "Strategist",
    lane: "pipeline",
    from: null,
    to: "proposed",
    blurb: "The Strategist proposes two tasks. It runs when no other role has work to do.",
  },
  {
    role: "arbiter",
    agent: "arbiter",
    name: "Arbiter",
    label: "Arbiter",
    lane: "pipeline",
    from: "proposed",
    to: "seeded",
    blurb: "The Arbiter selects from two proposed tasks. It promotes the selected tasks to Seeded and rejects the remaining tasks.",
  },
  {
    role: "worker",
    agent: "worker",
    name: "Worker",
    label: "Worker",
    lane: "pipeline",
    from: "seeded",
    to: "worked",
    blurb: "The Worker works one task in one worktree.",
  },
  {
    role: "verifier",
    agent: "verifier",
    name: "Verifier",
    label: "Verifier",
    lane: "pipeline",
    from: "worked",
    to: "verified",
    blurb: "The Verifier checks the Worker deliverable against the task requirements. Task corrections return to the Worker.",
  },
  {
    role: "packager",
    agent: "packager",
    name: "Packager",
    label: "Packager",
    lane: "pipeline",
    from: "verified",
    to: "packaged",
    blurb: "The Packager prepares the review packet from the verified deliverables. The report covers the source change for operator review.",
  },
  {
    role: "scribe",
    agent: "scribe",
    name: "Scribe",
    label: "Scribe",
    lane: "pipeline",
    from: null,
    to: null,
    blurb: "The Scribe updates the Project wiki on a schedule. It has no task.",
  },
  {
    role: "collie",
    agent: "collie",
    name: "Collie",
    label: "Collie",
    lane: "outside",
    from: null,
    to: null,
    blurb: "Collie talks with the operator. It is outside the pipeline and holds no lease.",
  },
];

/** The graph connects the task roles and their correction paths. */
export const TOPOLOGY_EDGES = [
  { from: "strategist", to: "arbiter", kind: "chain" },
  { from: "arbiter", to: "worker", kind: "chain" },
  { from: "worker", to: "verifier", kind: "chain" },
  { from: "verifier", to: "packager", kind: "chain" },
  { from: "verifier", to: "worker", kind: "correction" },
];

function held(lease) {
  return Boolean(lease && lease.run_id !== null && lease.run_id !== undefined);
}

/** The active node comes from the role of each held lease. */
function litRole(lease) {
  if (!held(lease) || mergeStep(lease) || lease.run_outcome != null || lease.run_ended_at != null || typeof lease.owner !== "string") return null;
  const agent = lease.owner.toLowerCase();
  return TOPOLOGY_NODES.some((n) => n.agent === agent) ? agent : null;
}

/** The Scribe run of an active wiki update, in the shape of a held lease.
 * Null when the wiki state row holds no Scribe run. With no wiki state row,
 * the newest Scribe run row gives the run while that row has no end. A run
 * row holds no heartbeat time, so that run never reads as stale. */
function scribeRun(wikiState, runs, nowMs, settings) {
  if (wikiState == null) {
    const { latest } = newestRun(runs, "scribe");
    if (!latest || latest.outcome != null || latest.ended_at != null) return null;
    return {
      project: typeof latest.project === "string" ? latest.project : "?",
      subject: null,
      since: relativeAge(latest.created_at, nowMs) || null,
      stale: false,
      starting: latest.agent_started_at === null,
    };
  }
  const active = wikiState.active;
  if (active?.step !== "run") return null;
  return {
    project: typeof wikiState.project === "string" ? wikiState.project : "?",
    subject: null,
    since: relativeAge(active.started_at, nowMs) || null,
    stale: heartbeatStopped(active.last_heartbeat_at, nowMs, settings),
    starting: active.agent_started_at === null,
  };
}

function routeFor(routes, agent) {
  const row = (routes ?? []).find(
    (r) => typeof r?.role === "string" && r.role.toLowerCase() === agent,
  );
  return {
    harness: row?.harness ?? null,
    binding: row?.binding ?? null,
    profileName: row?.name ?? row?.binding ?? null,
    model: row?.model ?? null,
  };
}

/** The newest agent run for a flat role. `mc run list` returns oldest first,
 * so the newest is the last match, not the first. */
function newestRun(runs, agent) {
  let latest = null;
  let count = 0;
  for (const run of runs ?? []) {
    if (typeof run?.role !== "string" || run.role.toLowerCase() !== agent || mergeStep(run)) continue;
    count += 1;
    latest = run;
  }
  return { latest, count };
}

/** The graph with the live overlay. `leases` is every lease row (one per
 * Project); `edges` is the frozen table verbatim; each node gains lease,
 * routing and run-history facts. Several Projects can hold a lease at
 * once, so several nodes can be active; each active node lists the leases
 * that run it (`held`: the Project, the subject, the age of the lease, and
 * whether its heartbeat is stale against `settings`). `wikiState` is the
 * wiki state row of one Project, or null. The Scribe node is active while
 * that row holds a Scribe run. With no row, the node is active while the
 * newest Scribe run row has no end. */
export function topologyView(leases, runs, routes, nowMs, settings, wikiState = null) {
  // Each held lease activates one role node.
  const lit = new Map();
  for (const lease of leases ?? []) {
    const role = litRole(lease);
    if (role === null) continue;
    if (!lit.has(role)) lit.set(role, []);
    lit.get(role).push({
      project: typeof lease.project === "string" ? lease.project : "?",
      subject: typeof lease.subject === "number" ? lease.subject : null,
      since: relativeAge(lease.acquired_at, nowMs) || null,
      stale: leaseStale(lease, nowMs, settings),
      starting: lease.agent_started_at === null,
    });
  }
  const scribe = scribeRun(wikiState, runs, nowMs, settings);
  if (scribe) lit.set("scribe", [scribe]);

  const nodes = TOPOLOGY_NODES.map((node) => {
    const heldBy = lit.get(node.role) ?? [];
    const active = heldBy.length > 0;

    const { latest, count } = newestRun(runs, node.agent);
    const endedMs = latest ? utcMs(latest.ended_at ?? latest.created_at) : NaN;
    const recent = !Number.isNaN(endedMs) && nowMs - endedMs < RECENT_MS;

    const lastOutcome = latest ? runResult(latest) : null;

    return {
      ...node,
      active,
      starting: active && heldBy.every(h => h.starting),
      held: heldBy,
      projects: heldBy.map((h) => h.project),
      stale: heldBy.some((h) => h.stale),
      state: active ? "running" : recent ? "recent" : "idle",
      ...routeFor(routes, node.agent),
      lastRun: latest?.id ?? null,
      lastRunAge: (latest && relativeAge(latest.created_at, nowMs)) || null,
      lastRunLabel: (latest && ageLabel(latest.created_at, nowMs)) || null,
      lastOutcome,
      runCount: count,
    };
  });

  return { nodes, edges: TOPOLOGY_EDGES };
}
