// The role map shows task roles and current runs.
// The Project wiki section shows the Scribe apart from the task path.

import { projectName, shortAge } from "./format.js";
import { el } from "./dom.js";
import { stageLabel } from "./stages.js";

/** Each edge identifies the tasks that wait between its roles. */
const CHAIN = [
  { from: "strategist", status: "proposed" },
  { from: "arbiter", status: "seeded" },
  { from: "worker", status: "worked" },
  { from: "verifier", status: "verified" },
  { from: "packager", status: "packaged" },
];

const PURPOSES = {
  strategist: "Proposes two tasks",
  arbiter: "Chooses from proposed tasks",
  worker: "Implements a task",
  verifier: "Checks the deliverable",
  packager: "Prepares the review packet",
  scribe: "Updates the Project wiki",
};

export const NARROW_BELOW_PX = 1080;
export function layoutFor(width) {
  return width < NARROW_BELOW_PX ? "narrow" : "wide";
}

function joinProjects(held) {
  return held.map((h) => h.projectName).join(", ");
}

/** The words one node shows on the graph and in its disclosure. */
function nodeWords(node) {
  const outside = node.lane === "outside";
  let stateLabel = null;
  if (!outside) stateLabel = node.active ? (node.starting ? "Agent launch pending" : node.stale ? "Heartbeat stopped" : "Running now") : "Idle";

  let line;
  let lineTail = "";
  if (node.active) {
    const since = node.held.find((h) => h.since)?.since;
    lineTail = since ? ` · ${since}` : "";
    line = `on ${joinProjects(node.held)}${lineTail}`;
  } else if (node.lastRunLabel) {
    line = `last run ${node.lastRunLabel}`;
  } else {
    line = "no runs yet";
  }

  const lastRun = node.lastRunLabel
    ? `${node.lastRunLabel}${node.lastOutcome ? ` · ${node.lastOutcome}` : ""}`
    : "no runs yet";

  const stateline = [];
  if (stateLabel) stateline.push(stateLabel);
  if (node.active) {
    for (const h of node.held) {
      stateline.push(`on ${h.projectName}${h.since ? ` · ${h.since}` : ""}${h.subject !== null ? ` · task #${h.subject}` : ""}${h.starting && !node.starting ? " · Agent launch pending" : ""}`);
    }
  } else {
    stateline.push(node.lastRunLabel ? `last run ${lastRun}` : "no runs yet");
  }

  return {
    stateLabel,
    line,
    lineTail,
    detail: {
      title: node.label,
      stateline: stateline.join(" · "),
      description: node.blurb,
      harness: node.harness,
      model: node.model,
    },
  };
}

/** The flow map's words. `topology` is topologyView's result; `tasks` every
 * task row on hand (all Projects). Each chain edge carries the count of
 * tasks that hold its status. The edge lights when the role it leaves has a
 * run with a started agent and a current heartbeat. */
export function flowmapView(topology, tasks, catalog = [], options = {}) {
  const live = (tasks ?? []).filter(task => task && !task.archived);
  const byRole = new Map(topology.nodes.map((n) => [n.role, n]));

  const nodes = topology.nodes.map((node) => ({
    role: node.role,
    agent: node.agent,
    name: node.name,
    purpose: PURPOSES[node.role] ?? node.blurb,
    lane: node.lane,
    state: node.state,
    active: node.active,
    starting: node.starting,
    stale: node.stale,
    binding: node.binding ?? null,
    profileName: node.profileName ?? node.binding ?? null,
    ...nodeWords({ ...node, held: node.held.map(h => ({ ...h, projectName: projectName(h.project, catalog) })) }),
  }));

  const edges = CHAIN.map(({ from, status }) => ({
    from,
    status,
    count: live.filter((task) => task.status === status).length,
    live: (byRole.get(from)?.held ?? []).some((h) => !h.starting && !h.stale),
  }));

  const currentRuns = topology.nodes.filter(node => node.active && node.role !== "scribe" && node.role !== "homie")
    .flatMap(node => node.held.map(held => {
      const lease = (options.leases ?? []).find(row => row.project === held.project && row.owner === node.agent);
      const run = (options.runs ?? []).find(row => row.id === lease?.run_id);
      const task = live.find(row => row.id === held.subject && row.project === held.project);
      const heartbeat = shortAge(lease?.last_heartbeat_at, options.nowMs ?? Date.now());
      return {
        role: node.name,
        state: nodeWords(node).stateLabel,
        stale: held.stale,
        starting: held.starting,
        task: task?.id ?? held.subject,
        title: task?.title || (held.subject !== null ? `Task #${held.subject}` : PURPOSES[node.role]),
        project: projectName(held.project, catalog),
        age: shortAge(lease?.agent_started_at ?? lease?.acquired_at, options.nowMs ?? Date.now()) || held.since || "—",
        heartbeat: heartbeat ? `${heartbeat} ago` : "No heartbeat recorded",
        run: run ? { id: run.id, trace: run.agentsview ?? null } : null,
      };
    }));
  return { nodes, edges, currentRuns, loaded: options.loaded !== false, paused: options.paused === true,
    wikiState: options.wikiState };
}

function roleCard(node, index, edge, onSelect, selected) {
  return el("button", {
    type: "button", class: `node role-card ${node.state}${node.starting ? " starting" : ""}${node.stale ? " stale" : ""}${selected ? " selected" : ""}`,
    testid: `topology-node-${node.role}`, data: { role: node.role, state: node.state },
    "aria-haspopup": "dialog", "aria-expanded": String(selected), onclick: () => onSelect(node.role),
  },
  el("span", { class: "role-card-top", "aria-hidden": "true" },
    el("span", { class: "role-number", text: String(index + 1) }),
    el("span", { class: "role-egg" })),
  el("span", { class: "name", text: node.name }),
  el("span", { class: "role-purpose", text: node.purpose }),
  el("span", { class: "state", text: node.stateLabel }),
  el("span", { class: "line", text: node.line }),
  edge && el("span", { class: "role-count", testid: `topology-edge-${edge.status}` },
    el("span", { text: edge.status }), " ", el("b", { text: edge.count })),
  );
}

/** Each banner describes a recorded task run. Scribe runs stay in the wiki section. */
export function flowRunNode(view, traceNode) {
  return el("div", { class: "pipeline-current-runs" },
    view.currentRuns.length === 0
      ? el("section", { class: "pipeline-current idle", testid: "pipeline-current-run" },
        el("div", {}, el("p", { class: "run-eyebrow", text: view.paused ? "Project paused" : "Task runs" }),
          el("h3", { text: view.loaded ? "No active task run" : "Culler reads the pipeline state" }),
          el("p", { text: "The role cards show the latest recorded run for each role." })))
      : view.currentRuns.map(run => el("section", {
        class: `pipeline-current${run.stale ? " stale" : ""}`, testid: "pipeline-current-run",
      },
      el("div", { class: "current-subject" },
        el("p", { class: "run-eyebrow", text: `${run.role} · ${run.state}` }),
        el("h3", { text: run.title }),
        el("p", { text: [run.project, run.task !== null ? `Task #${run.task}` : null, view.paused ? "Project paused" : null].filter(Boolean).join(" · ") })),
      el("div", { class: "current-facts" },
        el("div", {}, el("strong", { text: run.age }), el("small", { text: run.starting ? "Launch wait" : "Run age" })),
        el("div", {}, el("strong", { class: "heartbeat", text: run.heartbeat }), el("small", { text: "Last heartbeat" }),
          run.run && traceNode?.(run.run))),
      )),
  );
}

/** Native buttons keep each role accessible at every viewport width. */
export function flowmapNode(view, layoutName, onSelect, selected = null) {
  const roles = view.nodes.filter(node => CHAIN.some(edge => edge.from === node.role));
  const packaged = view.edges.find(edge => edge.status === "packaged")?.count ?? 0;
  return el("div", { class: "graph role-map", testid: "topology-graph", data: { layout: layoutName } },
    el("section", { class: "role-path", "aria-label": "Task path" },
      el("div", { class: "role-map-heading" },
        el("div", {}, el("h3", { text: "Task path" }), el("p", { text: "Select a role for its details." })),
        el("span", { class: "tag", text: `${view.currentRuns.length} task ${view.currentRuns.length === 1 ? "run" : "runs"}` })),
      el("div", { class: "role-cards" }, roles.map((node, index) =>
        roleCard(node, index, view.edges.find(edge => edge.from === node.role), onSelect, node.role === selected))),
      el("div", { class: "role-map-outcome" }, el("span", { text: "The Packager prepares a packet for operator review." }),
        el("span", { text: `${packaged} ${packaged === 1 ? "task" : "tasks"} at ${stageLabel("packaged")}` }))),
  );
}

/** The Project wiki section stands beside the run banner, apart from the task
 * path. Its Scribe button opens the role detail of the Scribe. */
export function flowWikiNode(view, onSelect, selected = null) {
  const scribe = view.nodes.find(node => node.role === "scribe");
  if (!scribe) return null;
  return el("section", { class: "pipeline-wiki", testid: "pipeline-wiki", "aria-label": "Project wiki" },
    el("p", { class: "role-section-label", text: "Separate from task work" }),
    el("h3", { text: scribe.name }),
    el("p", { class: "wiki-state", testid: "wiki-state", text: view.wikiState === null ? "The dashboard cannot read the wiki state." : view.wikiState?.state_text ?? "The Scribe updates the Project wiki on a schedule." }),
    el("button", {
      type: "button", class: `node scribe-card ${scribe.state}${scribe.stale ? " stale" : ""}${selected === scribe.role ? " selected" : ""}`,
      testid: "topology-node-scribe", data: { role: scribe.role, state: scribe.state },
      "aria-haspopup": "dialog", "aria-expanded": String(selected === scribe.role), onclick: () => onSelect(scribe.role),
    },
    el("span", { class: "state", text: scribe.stateLabel }),
    el("span", { class: "scribe-details", text: "Details →" }),
    el("span", { class: "line", text: scribe.line })),
  );
}

/** The picker entries for one disclosure: every enabled binding, sorted by
 * id, with the label the option shows and the current binding marked. A
 * binding without a model shows an em dash in the model position. */
export function bindingOptions(bindings, current) {
  if (!Array.isArray(bindings)) return [];
  return bindings
    .filter((b) => typeof b?.id === "string" && b.id !== "")
    .map((b) => {
      const harness = b.harness ?? "—";
      const model = b.model ? b.model : "—";
      return { id: b.id, harness, model, label: `${b.name || b.id} · ${harness} · ${model}`, current: b.id === current };
    })
    .sort((a, b) => a.id.localeCompare(b.id));
}

/** The role detail offers each enabled agent profile. */
export function routePickerNode(node, routing) {
  if (!routing || !Array.isArray(routing.bindings)) return null;
  return el(
    "select",
    {
      class: "route-picker",
      testid: "topology-route-picker",
      "aria-label": `Agent profile for ${node.name}`,
      disabled: routing.busy ? "disabled" : null,
      onchange: (event) => {
        if (event.target.value === "__create__") {
          event.target.value = node.binding;
          routing.onCreate?.(node.agent, event.target);
        } else routing.onRoute(node.agent, event.target.value);
      },
    },
    bindingOptions(routing.bindings, node.binding).map((option) =>
      el("option", { value: option.id, selected: option.current ? "selected" : null, text: option.label }),
    ),
    routing.onCreate && el("option", { value: "__create__", text: "Create agent profile…" }),
  );
}

/** The role detail of the Configs page: the role's full name, its
 * description, and its route with the binding picker. Configs reads routes,
 * not runs, so the detail states no activity. `routing` carries the enabled
 * bindings and `onRoute(role, binding)`; the verb's rejection text, when
 * there is one, sits under the picker. */
export function flowSideNode(node, onClose, routing = null) {
  const d = node.detail;
  const fact = (label, value, testid) =>
    el("div", {}, el("dt", { text: label }), el("dd", { testid, text: value ?? "unknown" }));
  const picker = routePickerNode(node, routing);
  return el(
    "aside",
    { class: "flow-side", testid: "topology-side", "aria-live": "polite" },
    el(
      "div",
      { class: "top" },
      el("h3", { class: "title", id: "role-dialog-title", text: d.title }),
      el("button", { class: "close", type: "button", "aria-label": "Close", text: "✕", onclick: onClose }),
    ),
    el("p", { text: d.description }),
    el(
      "dl",
      {},
      fact("Harness", d.harness, "topology-side-harness"),
      fact("Model", d.model, "topology-side-model"),
      !picker && fact("Agent profile", node.profileName),
      picker && el("div", {}, el("dt", { text: "Agent profile" }), el("dd", { class: "plain" }, picker)),
    ),
    el("p", { class: "flow-side-note", text: "The Pipeline page shows what this role runs now." }),
    routing?.onManage && el("button", { type: "button", class: "btn", text: "Change agent profile", testid: "topology-change-profile", onclick: (event) => routing.onManage(node.agent, event.currentTarget) }),
    picker && routing.error
      ? el("div", { class: "route-picker-error", testid: "topology-route-picker-error", text: routing.error })
      : null,
  );
}

/** The role dialog of the Pipeline page.
 * It states the role and holds one row for each configuration. `actions`
 * carries `onClose`, `onProfile(agent, entry)`, `onInstructions(role, entry)`,
 * and `instructions`, the state of the custom instructions. */
export function flowPopoverNode(node, actions) {
  const d = node.detail;
  const row = (testid, label, scope, value, tone, onclick) =>
    el(
      "button",
      { type: "button", class: "flow-popover-row", testid, onclick: (event) => onclick(event.currentTarget) },
      el("span", { class: "flow-popover-label" }, el("b", { text: label }), el("small", { text: scope })),
      el("span", { class: `flow-popover-value ${tone}`.trim(), text: value }),
    );
  return el(
    "aside",
    {
      class: "flow-popover",
      testid: "topology-side",
      data: { role: node.role },
    },
    el(
      "div",
      { class: "top" },
      el(
        "div",
        {},
        el("h3", { class: "title", id: "role-dialog-title", text: d.title }),
        el("p", { class: `stateline${node.active ? " running" : ""}`, text: d.stateline }),
      ),
      el("button", { type: "button", class: "btn dialog-close", text: "Close", onclick: actions.onClose }),
    ),
    el("p", { class: "blurb", text: d.description }),
    el(
      "div",
      { class: "flow-popover-rows" },
      row(
        "topology-change-profile",
        "Agent profile",
        "All Projects",
        d.model || node.profileName || "unknown",
        d.model ? "model" : "",
        (entry) => actions.onProfile(node.agent, entry),
      ),
      row(
        "topology-custom-instructions",
        "Custom instructions",
        "This Project",
        actions.instructions?.text ?? "",
        actions.instructions?.blocked ? "attention" : "",
        (entry) => actions.onInstructions(node.role, entry),
      ),
    ),
  );
}
