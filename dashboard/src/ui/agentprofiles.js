import { dialogHeader, dialogBody, dialogFooter, showDialog } from "./components.js";
import { el, reconcileChildren, svg } from "./dom.js";
import { conditionExplanation, conditionImpact } from "./systemhealth.js";

const OPERATION_KEY = "mc-agent-profile-operation";
const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);
const STEPS = ["Connect", "Configure", "Test and save"];
// The panel requests an active download status and a model list with an active refresh again after these milliseconds.
const DOWNLOAD_INTERVAL = 1000;
const MODEL_LIST_INTERVAL = 3000;
const PRIMARY = { class: "profile-primary" };
const MODEL_SOURCES = {
  release_catalog: "The release catalog supplies these models.",
  release_catalog_and_live: "The release catalog and the live model list of OpenCode supply these models.",
};
// Each of these harness families has a provider with a subscription integration and an API key integration.
const RELEASE_HARNESSES = ["codex", "claude-sdk"];
const CONNECTION_KINDS = ["codex-oauth", "claude-oauth", "codex-api-key", "claude-api-key", "opencode-api-key"];
const apiKeyConnection = (source) => ["codex-api-key", "claude-api-key", "opencode-api-key"].includes(source?.kind);
const accessLabel = (value) => ({ connected: "Credential saved", disconnected: "Disconnected", unavailable: "Host credential store unavailable" }[value] || value);
// A connected account has a saved credential.
const usable = (value) => value === "connected";
const title = (value) => value ? value[0].toUpperCase() + value.slice(1) : "";
const uuid = () => crypto.randomUUID();

export function profilePayload(draft, revision, enabled) {
  return {
    submission_id: draft.submission_id, revision, ...(draft.id ? { id: draft.id } : {}),
    name: draft.name.trim(), connection_id: draft.connection_id,
    model: draft.model, ...(draft.effort ? { effort: draft.effort } : {}), enabled,
    ...(enabled ? { test_id: draft.test_id } : {}),
  };
}

// The panel lists only enabled agent profiles.
export function listedProfiles(profiles) {
  return (profiles ?? []).filter((profile) => profile.enabled);
}

// The Edit form keeps the current connection of its agent profile after that connection loses its access.
export function connectionChoices(connections, current, keepCurrent = false, system) {
  return (connections ?? []).filter((row) => {
    if (!CONNECTION_KINDS.includes(row.kind)) return false;
    if (keepCurrent && current && row.id === current) return true;
    const conditions = (system?.snapshot?.connections ?? []).filter((item) => item.connection_id === row.id && !item.profile_id);
    return usable(row.access_status) && !system?.unavailable && conditions.every((item) => item.state === "ready");
  }).map((row) => ({
    value: row.id, text: `${row.name || row.id} · ${accessLabel(row.access_status)}`, selected: row.id === current,
  }));
}

// The provider lists the models of a connection with a saved credential.
export function listsModels(connection) {
  return CONNECTION_KINDS.includes(connection?.kind) && connection.access_status === "connected";
}

// The choices keep a configured model that the provider does not offer.
export function modelChoices(models, current) {
  const choices = (models ?? []).map((model) => ({
    value: model.id, text: (model.name && model.name !== model.id ? `${model.name} (${model.id})` : model.id) + (model.context ? ` · ${model.context.toLocaleString()} tokens · $${model.input_price} input / $${model.output_price} output per million tokens` : ""), selected: model.id === current,
  }));
  if (current && !choices.some((choice) => choice.selected)) choices.unshift({ value: current, text: `${current} (not offered)`, selected: true });
  return choices;
}

// The hint states the source of the model list of an OpenCode connection.
export function modelSourceHint(source) {
  return `${MODEL_SOURCES[source] || MODEL_SOURCES.release_catalog} Prices apply to each million input and output tokens.`;
}

// A pending or active download shows the progress indicator. A failed download and an absent download offer Retry.
export function downloadView(status) {
  const active = Boolean(status) && !TERMINAL.has(status.status);
  const ready = status?.status === "succeeded";
  return {
    tone: active ? "active" : ready ? "ready" : status ? "failed" : "required",
    active, retry: !active && !ready, stage: status?.message || "OpenCode requires a download.",
  };
}

export const effortLabel = (effort) => ({ "": "Default", none: "None", minimal: "Minimal", low: "Low", medium: "Medium", high: "High", xhigh: "Extra high", max: "Max", ultra: "Ultra", thinking: "Thinking" }[effort || ""] || effort);

// The rank is -1 for a provider that does not match the search.
// Each catalog identifier starts with "opencode:", so the search ignores that prefix.
function providerRank(provider, search) {
  const name = String(provider.name || "").toLowerCase();
  if (name.startsWith(search)) return 0;
  if (name.split(/[^\p{L}\p{N}]+/u).some(word => word && word.startsWith(search))) return 1;
  if (name.includes(search)) return 2;
  if ([provider.catalog_entry, String(provider.id || "").replace(/^opencode:/, "")].some(value => String(value || "").toLowerCase().includes(search))) return 3;
  if (String(provider.address || "").toLowerCase().includes(search)) return 4;
  return -1;
}

// The matches list name matches before identifier matches and address matches.
// The list holds the providers that OpenCode runs. The first screen offers the other providers.
export function providerChoices(providers, query = "") {
  const search = query.trim().toLowerCase();
  return (providers || []).filter(provider => provider.harness === "opencode").map(provider => ({ provider, rank: providerRank(provider, search) }))
    .filter(match => match.rank >= 0).sort((a, b) => a.rank - b.rank).map(match => match.provider);
}

// A release provider offers a sign-in and an API key.
export function releaseProviders(catalog) {
  return RELEASE_HARNESSES.map((harness) => {
    const api_key = (catalog?.providers || []).find(row => row.harness === harness);
    const subscription = (catalog?.integrations || []).find(row => row.harness === harness && !apiKeyConnection(row));
    return api_key && { harness, name: api_key.name, api_key, subscription };
  }).filter(Boolean);
}

const sameName = (row, name) => (row.name || row.id).toLowerCase() === name.trim().toLowerCase();

// The name states the provider and the access method.
// A connection with that name and no credential receives the new credential, unless an agent profile uses it.
export function plannedConnection(catalog, template, name) {
  const connections = catalog?.connections || [];
  const idle = connections.find(row => sameName(row, name) && row.kind === template.kind
    && (row.catalog_entry || "") === (template.harness === "opencode" ? template.catalog_entry : "")
    && row.access_status === "disconnected" && !(catalog.profiles || []).some(profile => profile.enabled && profile.connection_id === row.id));
  if (idle) return { id: idle.id, name: idle.name || idle.id };
  let unused = name;
  for (let suffix = 2; connections.some(row => sameName(row, unused)); suffix++) unused = `${name} ${suffix}`;
  return { id: null, name: unused };
}

export function effortChoices(model, current = "") {
  const levels = ["", ...(model?.effort_levels || [])];
  const choices = levels.map(value => ({ value, text: effortLabel(value), selected: value === current }));
  if (current && !levels.includes(current)) choices.push({ value: current, text: `${effortLabel(current)} (${model ? "unavailable" : "saved"})`, selected: true, disabled: true });
  return choices;
}

export function profileStatus(profile) {
  const reconnect = !usable(profile.access_status) || !!profile.problem;
  const label = accessLabel(profile.access_status) || "Access unknown";
  return { reconnect, attention: reconnect, label, tone: reconnect ? "attention" : profile.access_status === "connected" ? "connected" : "quiet" };
}

export function operationSummary(operation) {
  const noun = operation.kind === "model_test" ? "model test" : "authorization";
  if (operation.status === "action_required") return { tone: "action", active: true, title: "The authorization requires your action" };
  if (!TERMINAL.has(operation.status)) return { tone: "active", active: true, title: `The ${noun} is in progress` };
  if (operation.status === "succeeded") return { tone: "success", active: false, title: `The ${noun} succeeded` };
  if (operation.status === "failed") return { tone: "failure", active: false, title: `The ${noun} failed` };
  return { tone: "quiet", active: false, title: `The ${noun} is cancelled` };
}

export function elapsedLabel(start, now = Date.now()) {
  const milliseconds = now - Date.parse(start);
  if (!Number.isFinite(milliseconds)) return "";
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  return seconds < 60 ? `${seconds} s` : `${Math.floor(seconds / 60)} min ${seconds % 60} s`;
}

export function currentProfileStatus(profile, system) {
  if (profile.problem) return { label: "Model unavailable", action: "Edit profile", reconnect: false, attention: true, tone: "attention" };
  if (!system) return profileStatus(profile);
  const rows = (system.snapshot?.connections ?? []).filter(item => item.connection_id === profile.connection_id && (!item.profile_id || item.profile_id === profile.id));
  const failure = rows.find(item => item.state === "stopped" || item.state === "retry_wait");
  const unknown = system.unavailable || !rows.length || rows.some(item => item.state === "unknown");
  const action = failure?.action ?? "Review connection";
  const label = failure ? failure.profile_id ? "Model unavailable" : failure.state === "retry_wait" ? "Retry scheduled" : "Connection unavailable" : unknown ? "Status unavailable" : "Connected";
  // An automatic retry or an operator retry needs no authorization.
  const reconnect = failure?.state === "stopped" && ["Reconnect", "Replace API key", "Review connection"].includes(failure.action);
  return { label, action, reconnect, attention: Boolean(failure || unknown), tone: failure ? "attention" : unknown ? "quiet" : "connected" };
}

// The Edit form requests authorization only when the current connection state requires Reconnect.
export function needsAuthorization(profile, system) {
  return (system?.snapshot?.connections ?? []).some(item =>
    item.connection_id === profile?.connection_id && !item.profile_id && item.state === "stopped" && ["Reconnect", "Replace API key"].includes(item.action));
}

export function createAgentProfiles({ api, onAssigned, systemState, onConnectionChanged }) {
  let catalog = { profiles: [], connections: [], integrations: [], revision: "" };
  let routes = [];
  let view = "list";
  let draft = null;
  let origin = null;
  let role = null;
  let busy = false;
  let error = "";
  let notice = "";
  let operation = null;
  let timer = null;
  let clock = null;
  let stalled = false;
  let generation = 0;
  let loaded = false;
  let choosing = null;
  let disconnectID = null;
  let reads = new AbortController();
  let modelLists = new Map();
  let downloadTimer;
  let modelListTimer;
  const expandedProfiles = new Set();
  const dialog = el("dialog", { class: "agent-profiles", testid: "agent-profiles-panel", "aria-labelledby": "agent-profiles-title" });
  document.body.append(dialog);
  const post = (action, body) => api(`/api/agent-profiles/${action}`, { method: "POST", body: JSON.stringify(body) });
  // A render retains each element and compares the properties from its creation, so each property goes in here.
  const button = (label, action, id, disabled = false, props = {}) => el("button", {
    type: "button", text: label, testid: id, disabled: disabled || busy || operationActive(), onclick: action,
    ...props, class: `btn ${props.class || ""}`.trim(),
  });
  const note = (text) => el("p", { class: "note", text });
  const connection = () => catalog.connections.find((row) => row.id === draft?.connection_id);
  const invalidate = () => { draft.test_id = null; draft.save_request = null; draft.submission_id = uuid(); operation = null; };
  // A suggested value follows its source until the operator enters another value.
  const suggest = (key, value) => {
    if (draft[key] && draft[key] !== draft.suggestions[key]) return;
    draft[key] = draft.suggestions[key] = value;
  };
  const update = (key, value) => {
    draft[key] = value;
    if (key === "model" || key === "connection_id") draft.effort = "";
    invalidate();
  };
  const operationActive = () => operation && !TERMINAL.has(operation.status);
  const field = (label, key, options = {}) => {
    const { hint, ...attributes } = options;
    const hintID = hint && `profile-${key}-hint`;
    const input = el("input", {
      class: "field", id: `profile-${key}`, testid: `profile-${key}`, name: key, "aria-describedby": hintID,
      value: draft[key] || "", required: true, autocomplete: "off", disabled: busy || operationActive(), ...attributes,
      oninput: (event) => {
        update(key, event.target.value);
        render();
      },
    });
    return el("label", { class: "profile-field" }, el("span", { text: label }), input,
      hint && el("span", { class: "profile-hint", id: hintID, text: hint }));
  };
  const actionsNode = (back, ...next) => el("div", { class: "profile-step-actions" }, back,
    next.some(Boolean) && el("div", { class: "profile-next-actions" }, next));
  const duplicate = (rows, name, id) => rows.some((row) => row.id !== id && sameName(row, name));
  const validateProfile = () => {
    if (!draft.name.trim() || !draft.model || !draft.connection_id) throw new Error("Enter an agent profile name, a model, and a connection.");
    const model = modelLists.get(draft.connection_id)?.models?.find(model => model.id === draft.model);
    if (draft.effort && !model?.effort_levels?.includes(draft.effort)) throw new Error("Select an available effort level.");
    if (duplicate(catalog.profiles, draft.name, draft.id)) throw new Error("This agent profile name already exists. Enter another name.");
  };
  async function requestModels(source) {
    const result = await post("models", { connection_id: source.id });
    if (!Array.isArray(result?.models) || !result.models.length) throw new Error("The provider model list is invalid. Retry later.");
    return { models: result.models, web_search: result.web_search, source: result.source, refreshing: result.refreshing === true };
  }
  async function loadModels(source) {
    const entry = { status: "loading" };
    modelLists.set(source.id, entry);
    try {
      Object.assign(entry, await requestModels(source), { status: "ready" });
    } catch (err) {
      Object.assign(entry, { status: "failed", error: err.message || "The provider model list is unavailable. Retry later." });
    }
    if (modelLists.get(source.id) === entry && dialog.open && view === "create") render();
  }
  // A background request keeps the available model list on the display. A failed request leaves that list in place.
  async function reloadModels(source) {
    const entry = modelLists.get(source.id);
    if (entry?.status !== "ready" || entry.requesting) return;
    entry.requesting = true;
    let next = null;
    try { next = await requestModels(source); } catch {}
    entry.requesting = false;
    if (modelLists.get(source.id) !== entry) return;
    if (next) {
      entry.refreshing = next.refreshing;
      const shown = (list) => JSON.stringify([list.models, list.web_search, list.source]);
      entry.changed = shown(next) === shown(entry) ? null : next;
    }
    if (dialog.open && view === "create") render();
  }
  // A changed model list waits while the Model control or the Effort control has the focus.
  // An open list belongs to the control with the focus, so that list stays open and keeps its choices.
  const listFocused = () => ["profile-model", "profile-effort"].includes(document.activeElement?.dataset?.testid);
  function showChangedModels(entry) {
    if (!entry?.changed) return false;
    const { models, web_search, source } = entry.changed;
    Object.assign(entry, { models, web_search, source, changed: null });
    return true;
  }
  function watchModels(source) {
    if (modelListTimer) return;
    const serial = generation;
    modelListTimer = setTimeout(() => {
      modelListTimer = null;
      if (serial === generation && dialog.open) reloadModels(source);
    }, MODEL_LIST_INTERVAL);
  }
  function stopWatches() {
    clearTimeout(downloadTimer);
    clearTimeout(modelListTimer);
    downloadTimer = modelListTimer = null;
  }
  async function refresh() {
    const serial = generation;
    const [next, assignments] = await Promise.all([api("/api/agent-profiles", { signal: reads.signal }), api("/api/routing", { signal: reads.signal })]);
    if (serial !== generation) return;
    catalog = next;
    routes = assignments.routes || [];
    loaded = true;
  }
  async function perform(action) {
    if (busy) return;
    const serial = generation;
    busy = true;
    error = "";
    render();
    try { await action(); } catch (err) { if (serial === generation) error = err.message; }
    finally { if (serial === generation) { busy = false; if (dialog.open) render(); } }
  }
  function close() {
    generation++;
    reads.abort();
    reads = new AbortController();
    clearTimeout(timer);
    stopWatches();
    clearInterval(clock);
    clock = null;
    if (operation && !TERMINAL.has(operation.status)) {
      post("operation-cancel", { operation_id: operation.operation_id }).catch(() => {});
    }
    dialog.close();
    dialog.replaceChildren();
    draft = null;
    operation = null;
    const returnTarget = origin?.isConnected ? origin : document.querySelector(`[data-testid="${CSS.escape(origin?.dataset?.testid || "")}"]`);
    returnTarget?.focus();
  }
  dialog.addEventListener("cancel", (event) => { event.preventDefault(); close(); });
  function newDraft(profile = null, recover = false) {
    if (!recover) {
      generation++;
      stopWatches();
      try { sessionStorage.removeItem(OPERATION_KEY); } catch {}
    }
    clearTimeout(timer);
    busy = false;
    draft = {
      name: profile?.name || "", model: profile?.model || "", effort: profile?.effort || "",
      connection_id: profile?.connection_id || "", id: profile?.id,
      submission_id: uuid(), connection_submission_id: uuid(), mode: profile ? "existing" : "",
      connection_name: "", integration: "", harness: "", provider: "", provider_query: "", api_key: "", step: 1, test_id: null, auth_failed: needsAuthorization(profile, systemState?.()),
      heading: profile?.id ? "Edit agent profile" : "Create agent profile",
      chosen: false, previous: null, suggestions: {},
    };
    operation = null;
    error = "";
    notice = "";
    view = "create";
    render();
    dialog.querySelector(".ui-dialog-body")?.scrollTo(0, 0);
  }
  async function assign(profileID) {
    const serial = generation;
    const assignedRole = role;
    const result = await api("/api/routing", { method: "POST", body: JSON.stringify({ role: assignedRole, binding: profileID }) });
    routes = result.routes || [];
    onAssigned(result);
    if (serial !== generation) return;
    notice = `The ${title(role)} role now uses this agent profile across all Projects.`;
    draft = null;
    view = "assign";
    try { sessionStorage.removeItem(OPERATION_KEY); } catch {}
  }
  async function save() {
    const serial = generation;
    if (!draft.save_request) validateProfile();
    draft.save_request ||= profilePayload(draft, catalog.revision, true);
    const result = await post("save", draft.save_request);
    if (serial !== generation) return;
    catalog = result;
    const assigned = routes.some((row) => row.role === role && row.binding === result.profile_id);
    notice = role
      ? `The agent profile ${draft.save_request.name} is saved. ${assigned ? `The ${title(role)} role uses this agent profile.` : `Select it to assign it to the ${title(role)} role.`}`
      : "The agent profile is saved. Select a role on the Pipeline page to assign it.";
    view = role ? "assign" : "list";
    draft = null;
    try { sessionStorage.removeItem(OPERATION_KEY); } catch {}
  }
  async function pollOperation(id, serial, recover = false) {
    if (serial !== generation || !dialog.open) return;
    if (document.hidden) {
      timer = setTimeout(() => pollOperation(id, serial, recover), 1000);
      return;
    }
    try {
      const result = await post("operation-status", { operation_id: id });
      if (serial !== generation || !dialog.open) return;
      if (operation?.operation_id === id && TERMINAL.has(operation.status) && !TERMINAL.has(result.status)) return;
      if (recover && !draft && result.kind === "model_test" && result.profile) {
        newDraft(result.profile, true);
        draft.submission_id = result.profile.submission_id || uuid();
        const saved = catalog.profiles.find((profile) => profile.enabled && profile.name === draft.name && profile.model === draft.model && (profile.effort || "") === (draft.effort || "") && profile.connection_id === draft.connection_id);
        if (saved && result.status === "succeeded") {
          draft.id = saved.id;
          view = role ? "assign" : "list";
          notice = "The agent profile is saved.";
        }
      }
      operation = result;
      error = "";
      stalled = false;
      if (draft && result.error_code === "authorization") draft.auth_failed = true;
      if (result.status === "succeeded") {
        await refresh();
        if (serial !== generation) return;
        if (result.kind === "authorization") {
          modelLists.delete(result.connection_id);
          if (view === "create") notice = "The connection is saved. Other agent profiles can also use this connection.";
          if (draft) { draft.step = 2; draft.auth_failed = false; }
          onConnectionChanged?.();
        } else if (draft) draft.test_id = id;
      }
      if (TERMINAL.has(result.status)) {
        if (result.kind === "model_test") onConnectionChanged?.();
        busy = false;
        if (result.kind === "authorization") try { sessionStorage.removeItem(OPERATION_KEY); } catch {}
      }
      render();
      if (!TERMINAL.has(result.status)) timer = setTimeout(() => pollOperation(id, serial, recover), 1000);
    } catch {
      if (serial !== generation) return;
      busy = false;
      stalled = true;
      error = "The operation status is unavailable. Check the status to recover the result.";
      render();
    }
  }
  async function startOperation(kind, connectionID) {
    const serial = generation;
    const operationID = uuid();
    try { sessionStorage.setItem(OPERATION_KEY, JSON.stringify({ operation_id: operationID, role })); } catch {}
    operation = { operation_id: operationID, kind, status: "pending", started_at: new Date().toISOString() };
    stalled = false;
    const body = { operation_id: operationID, kind, connection_id: connectionID };
    if (kind === "model_test") {
      try { if (view !== "test") validateProfile(); } catch (err) { operation = null; try { sessionStorage.removeItem(OPERATION_KEY); } catch {} throw err; }
      body.profile = profilePayload(draft, catalog.revision, false);
      body.confirm_spend = true;
    }
    try {
      const result = await post("operation-start", body);
      if (serial !== generation) return;
      operation = result;
    }
    catch (err) {
      if (serial !== generation) return;
      if (err.status >= 400 && err.status < 500) {
        operation = null;
        try { sessionStorage.removeItem(OPERATION_KEY); } catch {}
        throw err;
      }
      error = "The operation response is unavailable. Check the status before another request.";
    }
    if (operation) {
      busy = !TERMINAL.has(operation.status);
      pollOperation(operationID, serial);
    }
  }
  function tick() {
    const node = dialog.querySelector('[data-testid="profile-operation-time"]');
    if (!node || !operationActive()) { clearInterval(clock); clock = null; return; }
    node.textContent = elapsedLabel(operation.started_at);
  }
  function operationNode() {
    if (!operation) return null;
    const test = operation.kind === "model_test";
    const summary = operationSummary(operation);
    const actionURL = operation.authorization_url;
    let safeURL = false;
    try { safeURL = ["https:", "http:"].includes(new URL(actionURL).protocol); } catch {}
    const timed = summary.tone === "active";
    const mark = el("span", { class: "profile-progress-mark", "aria-hidden": "true", text: { success: "✓", failure: "!" }[summary.tone] });
    if (timed && !clock) clock = setInterval(tick, 500);
    const link = safeURL && el("a", { href: actionURL, target: "_blank", rel: "noopener noreferrer", class: "btn profile-primary", text: "Open authorization page" });
    return el("section", { class: `profile-operation profile-progress-${summary.tone}`, testid: "profile-operation", role: "status" },
      el("div", { class: "profile-progress-head" }, mark,
        el("p", { class: "profile-progress-title", text: summary.title }),
        timed && el("span", { class: "profile-progress-time", testid: "profile-operation-time", "aria-hidden": "true", text: elapsedLabel(operation.started_at) })),
      test && timed && note("This might take a moment"),
      operation.message && note(operation.message),
      operation.detail && note(`Details: ${operation.detail}`),
      test && view !== "test" && operation.status === "succeeded" && draft?.test_id && note("Select Save to keep the agent profile."),
      operation.user_code && note(`Authorization code: ${operation.user_code}`),
      (link || summary.active) && el("div", { class: "profile-progress-actions" }, link,
        summary.active && stalled && el("button", { type: "button", class: "btn", text: "Check status", testid: "profile-operation-status", onclick: () => pollOperation(operation.operation_id, generation) }),
        summary.active && el("button", { type: "button", class: "btn", text: test ? "Cancel test" : "Cancel authorization", testid: "profile-operation-cancel", onclick: () => {
          const serial = generation;
          post("operation-cancel", { operation_id: operation.operation_id }).then((result) => {
            if (serial !== generation) return;
            operation = result;
            // The host keeps an authorization active after it saves the credential.
            if (TERMINAL.has(result.status)) { busy = false; clearTimeout(timer); }
            render();
          }).catch(() => {
            if (serial !== generation) return;
            error = "The cancellation result is unavailable. Check the operation status.";
            render();
          });
        } })),
    );
  }
  function listNode() {
    if (!loaded) return el("p", { class: "profile-placeholder", role: "status", text: error ? "Model configuration is unavailable." : "Loading model configuration…" });
    const profiles = listedProfiles(catalog.profiles).map((profile) => ({ profile, status: currentProfileStatus(profile, systemState?.()) }));
    profiles.sort((a, b) => Number(b.status.attention) - Number(a.status.attention));
    const attention = profiles.filter((row) => row.status.attention).length;
    const create = button("Create agent profile", () => { role = null; newDraft(); }, "profile-create", false, { class: "profile-create" });
    return el("div", { class: "profile-list" },
      el("div", { class: "profile-toolbar" },
        el("div", { class: "profile-overview", role: "status" },
          el("span", { class: "profile-count", text: `${profiles.length} ${profiles.length === 1 ? "profile" : "profiles"}` }),
          attention > 0 && el("span", { class: "profile-attention-count", testid: "profile-attention-count", text: `${attention} ${attention === 1 ? "needs" : "need"} attention` }),
        ), create,
      ),
      !profiles.length && el("div", { class: "profile-empty", testid: "profile-empty" },
        el("h3", { text: "Connect your first model" }),
        note("Create an agent profile to choose a provider connection and a model.")),
      profiles.map(({ profile, status }) => profileNode(profile, status)),
      catalog.connections.some(source => source.authorization_id) && el("details", { testid: "profile-connections" },
        el("summary", { text: "Connections" }),
        catalog.connections.filter(source => source.authorization_id).map(source => el("section", { class: "profile-card", testid: `profile-connection-${source.id}` },
          el("h3", { text: source.name || source.id }),
          note(accessLabel(source.access_status)),
          button("Disconnect", () => { disconnectID = source.id; view = "disconnect"; notice = ""; error = ""; render(); }, `profile-disconnect-${source.id}`)))),
    );
  }
  function disconnectNode() {
    const source = catalog.connections.find(row => row.id === disconnectID);
    if (!source) return button("All agent profiles", showList, "profile-list");
    return el("section", { testid: "profile-disconnect-confirm" },
      el("h3", { text: `Disconnect ${source.name || source.id}?` }),
      note("Culler stops active requests for this connection and removes its saved credentials. Agent profiles and assignments remain saved."),
      note("Provider revocation is unconfirmed. Your separate native sessions remain available."),
      actionsNode(button("Keep connected", showList, "profile-disconnect-cancel"),
        button("Disconnect connection", () => perform(async () => {
          const result = await post("connection-disconnect", { connection_id: source.id, authorization_id: source.authorization_id, confirm: true });
          catalog = result;
          modelLists.delete(source.id);
          disconnectID = null;
          view = "list";
          notice = result.message;
          onConnectionChanged?.();
        }), "profile-disconnect-confirmed", false, PRIMARY)));
  }
  function profileNode(profile, status) {
    const expanded = expandedProfiles.has(profile.id);
    const detailsID = `profile-details-${profile.id}`;
    const profileName = profile.name || profile.id;
    const connectionName = profile.connection_name || profile.connection_id;
    const separateConnection = connectionName?.toLowerCase() !== profileName.toLowerCase();
    const edit = button("Edit", () => newDraft(profile), `profile-edit-${profile.id}`, false, { "aria-label": `Edit ${profile.name || profile.id}` });
    const source = catalog.connections.find((row) => row.id === profile.connection_id);
    const action = apiKeyConnection(source) && status.reconnect ? "Replace API key" : !status.action || status.action === "Reconnect" ? "Authorize" : status.action;
    const reconnectButton = status.reconnect && button(action, () => {
      if (status.action === "Review connection") newDraft(profile);
      else openRepair(profile.connection_id, profile, true);
    }, `profile-reconnect-${profile.id}`, false, { "aria-label": `${action} ${profile.name || profile.id}`, class: status.attention ? "rust" : "" });
    return el("article", {
      class: `profile-card profile-model-card${status.attention ? " profile-needs-attention" : ""}`,
      testid: "profile-card", "aria-label": profile.name || profile.id,
    },
      el("div", { class: "profile-model-head" },
        el("div", { class: "profile-model-identity" },
          el("h3", { text: profile.model }),
          profile.problem && el("p", { class: "profile-error", text: profile.problem }),
          el("p", { class: "profile-hint", text: `Effort: ${effortLabel(profile.effort)}` }),
          el("p", { class: "profile-connection" },
            el("span", { class: "profile-name", text: profileName }),
            separateConnection && el("span", { class: "profile-separator", "aria-hidden": "true", text: "·" }),
            separateConnection && el("span", { text: `Connection: ${connectionName}` }))),
        el("span", { class: `profile-status profile-status-${status.tone}`, testid: "profile-status" },
          el("span", { class: "profile-status-mark", "aria-hidden": "true", text: status.attention ? "!" : "" }), status.label),
      ),
      el("div", { class: "profile-roles" },
        profile.roles?.length
          ? [el("span", { class: "profile-roles-label", text: "Used by" }), ...profile.roles.map((role) => el("span", { class: "profile-role", text: title(role) }))]
          : el("span", { class: "profile-unused", text: "No roles assigned" })),
      el("div", { class: "profile-card-actions" },
        el("button", {
          type: "button", class: "profile-details-toggle", testid: `profile-details-toggle-${profile.id}`,
          "aria-expanded": String(expanded), "aria-controls": detailsID,
          "aria-label": `Details for ${profile.name || profile.id}`,
          onclick: () => { if (expanded) expandedProfiles.delete(profile.id); else expandedProfiles.add(profile.id); render(); },
        }, "Details", svg("svg", { viewBox: "0 0 16 16", "aria-hidden": "true", width: 14, height: 14 },
          svg("path", { d: "m4 6 4 4 4-4", fill: "none", stroke: "currentColor", "stroke-width": 1.5, "stroke-linecap": "round", "stroke-linejoin": "round" }))),
        el("div", { class: "profile-edit-actions" }, edit, reconnectButton)),
      el("dl", { class: "profile-details", id: detailsID, hidden: !expanded },
        el("dt", { text: "Connection" }), el("dd", { text: connectionName }),
        el("dt", { text: "Connection access" }), el("dd", { text: status.label }),
        el("dt", { text: "Effort" }), el("dd", { text: effortLabel(profile.effort) }),
      ));
  }
  // The assignment view lists each enabled agent profile as one choice. A selection assigns it at once.
  function assignmentNode() {
    if (!loaded) return el("p", { class: "profile-placeholder", role: "status", text: error ? "Model configuration is unavailable." : "Loading model configuration…" });
    // The selected choice stays in view while Culler saves the assignment.
    const current = choosing ?? routes.find((row) => row.role === role)?.binding;
    const profiles = listedProfiles(catalog.profiles);
    return el("section", { class: "profile-assignment" },
      note("The next run uses this agent profile. Active runs keep their original configuration."),
      el("div", { class: "profile-choices", role: "radiogroup", "aria-label": `Agent profile for ${title(role)}`, testid: "profile-assignment" },
        !profiles.length && note("No agent profile exists. Create an agent profile."),
        profiles.map((profile) => choiceNode(profile, profile.id === current))),
      el("div", { class: "profile-assignment-actions" },
        button("Create agent profile…", () => newDraft(), "profile-assignment-create"),
        button("All agent profiles", showList, "profile-list")));
  }
  function choiceNode(profile, checked) {
    const status = currentProfileStatus(profile, systemState?.());
    const profileName = profile.name || profile.id;
    const connectionName = profile.connection_name || profile.connection_id;
    const separateConnection = connectionName?.toLowerCase() !== profileName.toLowerCase();
    const edit = button("Edit", () => newDraft(profile), `profile-edit-${profile.id}`, false, { "aria-label": `Edit ${profileName}` });
    return el("div", { class: `profile-choice${checked ? " selected" : ""}` },
      el("label", { class: "profile-choice-select" },
        el("input", {
          type: "radio", name: "profile-assignment", value: profile.id, checked, testid: `profile-assignment-${profile.id}`,
          onchange: () => {
            if (busy) { render(); return; }
            choosing = profile.id;
            perform(async () => { try { await assign(profile.id); } finally { choosing = null; } });
          },
        }),
        el("span", { class: "profile-choice-text" },
          el("b", { text: profile.model }),
          el("span", { text: `Effort: ${effortLabel(profile.effort)}` }),
          el("span", { text: separateConnection ? `${profileName} · Connection: ${connectionName}` : profileName }))),
      el("div", { class: "profile-choice-actions" },
        el("span", { class: `profile-status profile-status-${status.tone}` },
          el("span", { class: "profile-status-mark", "aria-hidden": "true", text: status.attention ? "!" : "" }), status.label),
        edit));
  }
  function showList() {
    role = null;
    draft = null;
    operation = null;
    view = "list";
    disconnectID = null;
    render();
  }
  function suggestedName(rows, base, id) {
    let name = base;
    for (let suffix = 2; duplicate(rows, name, id); suffix++) name = `${base} ${suffix}`;
    return name;
  }
  function choicesNode(back) {
    const choose = (mode, heading, harness = "") => {
      Object.assign(draft, { mode, heading, harness, chosen: true });
      render();
    };
    return [
      el("div", { class: "profile-actions" },
        releaseProviders(catalog).map(provider => button(`Use ${provider.name}`, () => choose("release", `Use ${provider.name}`, provider.harness), `profile-use-${provider.harness}`)),
        button("Use another provider", () => choose("catalog", "Use another provider"), "profile-use-provider"),
        button("Use an existing connection", () => choose("existing", "Use an existing connection"), "profile-use-existing")),
      actionsNode(back),
    ];
  }
  // The first refresh of the live models follows a successful download.
  // Each model list of an OpenCode connection then waits for that refresh.
  function setDownload(next) {
    const succeeded = next?.status === "succeeded" && catalog.opencode?.status !== "succeeded";
    catalog.opencode = next;
    if (!succeeded) return;
    for (const [id, entry] of modelLists) {
      if (entry.status === "ready" && catalog.connections.find(row => row.id === id)?.harness === "opencode") entry.refreshing = true;
    }
    const source = draft && connection();
    if (source?.harness === "opencode") reloadModels(source);
  }
  function watchDownload() {
    const status = catalog.opencode;
    if (!status || TERMINAL.has(status.status) || downloadTimer) return;
    const serial = generation;
    downloadTimer = setTimeout(async () => {
      downloadTimer = null;
      try {
        const next = await post("operation-status", { operation_id: status.operation_id });
        if (generation !== serial || !dialog.open) return;
        setDownload(next);
      } catch (error) {
        if (generation === serial) catalog.opencode = { ...status, status: "failed", message: error.message };
      }
      if (generation === serial && dialog.open) render();
    }, DOWNLOAD_INTERVAL);
  }
  // The connection save starts an absent download, so the form shows that download as pending during the save.
  function downloadNode(source) {
    if (source?.harness !== "opencode") return null;
    const shown = downloadView(draft?.saving && !catalog.opencode ? { status: "pending", message: "OpenCode waits for the download." } : catalog.opencode);
    watchDownload();
    return el("section", { class: `profile-download profile-download-${shown.tone}`, testid: "profile-opencode-download" },
      el("p", { class: "profile-download-stage", role: "status", testid: "profile-opencode-stage", text: shown.stage }),
      shown.active && el("div", { class: "profile-download-bar", role: "progressbar", "aria-label": "OpenCode download", testid: "profile-opencode-progress" }),
      shown.active && note("The model test waits for the download."),
      shown.retry && button("Retry", () => perform(async () => {
        setDownload(await post("operation-start", { operation_id: uuid(), kind: "opencode_download" }));
      }), "profile-opencode-retry"));
  }
  function modelNode(source) {
    if (!draft.saving && !listsModels(source)) return field("Model", "model");
    if (!draft.saving && !modelLists.has(source.id)) loadModels(source);
    const list = draft.saving ? { status: "loading" } : modelLists.get(source.id);
    if (list.status === "failed") return [
      el("p", { class: "profile-error", role: "alert", testid: "profile-model-list-error", text: list.error }),
      field("Model", "model", { hint: "Enter the model identifier, or retry the model list." }),
      button("Retry model list", () => { loadModels(source); render(); }, "profile-model-retry"),
    ];
    const loading = list.status === "loading";
    if (!listFocused()) showChangedModels(list);
    if (list.refreshing) watchModels(source);
    const live = source?.harness === "opencode";
    const offered = loading || !draft.model || list.models.some((model) => model.id === draft.model);
    const placeholder = el("option", { value: "", disabled: true, selected: !draft.model, text: loading ? "Loading models…" : "Select a model" });
    const choices = loading ? (draft.model ? [{ value: draft.model, text: draft.model, selected: true }] : []) : modelChoices(list.models, draft.model);
    // The key keeps the element of each model, so a new live model adds one element and changes no other element.
    const options = choices.map((choice) => el("option", { ...choice, data: { key: choice.value } }));
    return [
      el("label", { class: "profile-field" }, el("span", { text: "Model" }),
        el("select", {
          class: "field", id: "profile-model", testid: "profile-model", name: "model", "aria-describedby": "profile-model-hint",
          disabled: loading || busy || operationActive(),
          onchange: (event) => { update("model", event.target.value); showChangedModels(list); render(); },
          onblur: () => { if (showChangedModels(list)) render(); },
        }, placeholder, options),
        el("span", { class: "profile-hint", id: "profile-model-hint", role: loading || live ? "status" : null,
          text: loading ? "Loading models for this connection…" : live ? modelSourceHint(list.source) : "The provider lists the models that this connection can use." })),
      !offered && el("p", { class: "profile-error", role: "alert", data: { key: "model-not-offered" }, text: `This connection does not offer the model ${draft.model}. Select a model from the list.` }),
    ];
  }
  function effortNode(source) {
    const list = modelLists.get(source?.id);
    const model = list?.models?.find(model => model.id === draft.model);
    const unavailable = draft.effort && model && !model.effort_levels?.includes(draft.effort);
    const hint = !model ? "Select a model from the model list to see its effort levels."
      : model.effort_support_known ? "The model lists these effort levels. Default uses the provider default."
      : "The model lists no effort levels. These are harness levels; Test model checks your selection.";
    return el("label", { class: "profile-field" },
      el("span", { text: "Effort" }),
      el("select", { class: "field", testid: "profile-effort", name: "effort", "aria-describedby": "profile-effort-hint",
        disabled: busy || operationActive(),
        onchange: event => { update("effort", event.target.value); showChangedModels(list); render(); },
        onblur: () => { if (showChangedModels(list)) render(); } },
        effortChoices(model, draft.effort).map(choice => el("option", { ...choice, data: { key: choice.value } }))),
      el("span", { class: "profile-hint", id: "profile-effort-hint", text: hint }),
      el("span", { class: "profile-hint", text: "Higher effort can use more time and tokens. New runs use the selected effort. Existing conversations keep their effort." }),
      unavailable && el("span", { class: "profile-error", role: "alert", text: "The selected effort is unavailable. Select another effort level." }));
  }
  function configureNode(back) {
    const source = connection();
    const choices = connectionChoices(catalog.connections, draft.connection_id, Boolean(draft.id), systemState?.());
    return el("div", {},
      draft.mode === "existing" && el("label", { class: "profile-field" }, el("span", { text: "Connection" }),
        el("select", { class: "field", testid: "profile-connection_id", "aria-label": "Connection", disabled: busy || operationActive(), onchange: (event) => {
          update("connection_id", event.target.value);
          suggest("name", connection() ? suggestedName(catalog.profiles, connection().name || connection().id, draft.id) : "");
          render();
        } }, el("option", { value: "", selected: !choices.some((choice) => choice.selected), text: "Select a connection" }),
        choices.map((choice) => el("option", choice)))),
      draft.mode === "existing" && !choices.length && note("No connection has usable access. Select Back to sign in to an account."),
      source && note(`Connection access: ${accessLabel(source.access_status)}`),
      source?.authorization_message && note(source.authorization_message),
      downloadNode(draft.saving ? { harness: "opencode" } : source),
      modelNode(source),
      source?.harness === "opencode" && note(modelLists.get(source.id)?.web_search ? "The agents of this connection can search the web." : "The agents of this connection cannot search the web."),
      effortNode(source),
      field("Agent profile name", "name"),
      duplicate(catalog.profiles, draft.name, draft.id) && el("p", { class: "profile-error", role: "alert", data: { key: "name-duplicate" }, text: "This agent profile name already exists. Enter another name." }),
      draft.mode !== "existing" && actionsNode(back,
        button("Continue", () => { try { validateProfile(); draft.step = 3; error = ""; } catch (err) { error = err.message; } render(); }, "profile-configure-next", false, PRIMARY)),
    );
  }
  function reconnect() {
    const source = connection();
    if (!source) return;
    draft.auth_failed = true;
    if (!apiKeyConnection(source)) perform(() => startOperation("authorization", source.id));
  }

  // The repair view shows one connection. The operator starts its authorization there.
  function openRepair(connectionID, profile, start = false) {
    newDraft(profile);
    draft.connection_id = connectionID;
    draft.mode = "existing";
    draft.auth_failed = true;
    view = "repair";
    render();
    if (start) reconnect();
  }
  function repairNode() {
    if (view !== "repair" && (view !== "create" || !(draft.mode === "existing" || draft.step === 2))) return null;
    if (operationActive() && operation.kind === "authorization") return null;
    const source = connection();
    if (!source || (source.access_status === "connected" && !draft.auth_failed)) return null;
    const name = source.name || source.id;
    if (apiKeyConnection(source)) return el("section", { class: "profile-repair", testid: "profile-repair", "aria-labelledby": "profile-repair-title" },
      el("h3", { id: "profile-repair-title", text: `${name} · API key required` }),
      note("Enter a replacement API key. Culler keeps the previous key until the replacement is saved."),
      keyField(),
      button("Save key", () => perform(async () => {
        if (!draft.api_key) throw new Error("Enter the API key.");
        const integration = source.harness === "opencode"
          ? (catalog.providers || []).find(item => item.catalog_entry === source.catalog_entry && item.harness === "opencode")
          : (catalog.integrations || []).find((item) => item.kind === source.kind);
        if (!integration) throw new Error("The connection type is unavailable.");
        const serial = generation;
        const result = await post("connection-save", { submission_id: uuid(), revision: catalog.revision, id: source.id,
          name: source.name || source.id, template_id: integration.id, api_key: draft.api_key, authorization_id: source.authorization_id || "" });
        if (serial !== generation) return;
        draft.api_key = "";
        draft.auth_failed = false;
        draft.test_id = null;
        draft.step = 2;
        catalog = result;
        modelLists.delete(source.id);
        notice = "The API key is saved. Test the selected model to verify access.";
        view = "create";
        draft.mode = "existing";
        onConnectionChanged?.();
      }), "profile-save-key", false, PRIMARY));
    const action = button("Authorize", reconnect, "profile-reconnect", false, { class: "rust" });
    const condition = view === "repair" && (systemState?.()?.snapshot?.connections ?? [])
      .find(item => item.connection_id === source.id && !item.profile_id && item.state !== "ready");
    return el("section", { class: "profile-repair", testid: "profile-repair", "aria-labelledby": "profile-repair-title" },
      el("h3", { id: "profile-repair-title", text: view === "repair" ? `${name} · Authorization required` : "Authorization required" }),
      condition && conditionExplanation(condition) && el("p", { text: conditionExplanation(condition) }),
      condition && el("p", { text: conditionImpact(condition) }),
      note(`Select Authorize to start the authorization for the connection ${name}.`),
      action);
  }
  // The form names each new connection. A rejected save releases the name and the revision for the next attempt.
  async function connect(template, name, apiKey) {
    const serial = generation;
    if (apiKey && !draft.api_key) throw new Error("Enter the API key.");
    if (draft.integration !== template.id) {
      const planned = plannedConnection(catalog, template, name);
      Object.assign(draft, { integration: template.id, connection_name: planned.name, connection_edit_id: planned.id, connection_submission_id: uuid(), connection_revision: null });
    }
    draft.connection_revision ||= catalog.revision;
    const payload = { submission_id: draft.connection_submission_id, revision: draft.connection_revision, name: draft.connection_name, ...(draft.connection_edit_id ? { id: draft.connection_edit_id } : {}), template_id: template.id };
    if (apiKey) {
      payload.api_key = draft.api_key;
      payload.authorization_id = catalog.connections.find(row => row.id === draft.connection_edit_id)?.authorization_id || "";
    }
    let result;
    try { result = await post("connection-save", payload); }
    catch (err) {
      if (serial === generation && err.status >= 400 && err.status < 500) draft.integration = "";
      throw err;
    }
    if (serial !== generation) return;
    catalog = result;
    draft.api_key = "";
    draft.connection_id = result.connection_id;
    draft.connection_edit_id = result.connection_id;
    draft.connection_submission_id = uuid();
    draft.connection_revision = null;
    suggest("name", suggestedName(catalog.profiles, draft.connection_name, draft.id));
    notice = "The connection is saved. Other agent profiles can also use this connection.";
    if (!apiKey && connection()?.access_status !== "connected") await startOperation("authorization", draft.connection_id);
    else draft.step = 2;
  }
  // The Configure step opens before the save completes. A rejected save returns to the Connect step and keeps the key.
  async function connectDuringConfigure(template) {
    const serial = generation;
    Object.assign(draft, { step: 2, saving: true });
    notice = "Culler saves the connection.";
    render();
    try { await connect(template, `${template.name} API key`, true); }
    catch (err) {
      if (serial === generation) { draft.step = 1; notice = ""; }
      throw err;
    } finally {
      if (serial === generation && draft) draft.saving = false;
    }
  }
  const keyField = () => field("API key", "api_key", { type: "password", autocomplete: "new-password", spellcheck: false });
  function providerNode(provider, checked) {
    return el("label", { class: `profile-choice profile-provider${checked ? " selected" : ""}`, testid: "profile-provider-result" },
      el("input", {
        type: "radio", name: "profile-provider", value: provider.id, checked, testid: `profile-provider-${provider.id}`, disabled: busy,
        onchange: () => { draft.provider = provider.id; render(); },
      }),
      el("span", { class: "profile-choice-text" },
        el("b", { text: provider.name }),
        el("span", { text: provider.address || "" }),
        provider.website && el("a", { href: provider.website, target: "_blank", rel: "noopener noreferrer", text: provider.website })),
      checked && el("span", { class: "profile-provider-mark", text: "Selected" }));
  }
  function connectNode(back) {
    const saved = connection();
    if (saved?.access_status === "connected" && !draft.auth_failed) return el("div", {},
      el("section", { class: "profile-card", testid: "profile-connection-saved" },
        el("h4", { text: saved.name || saved.id }),
        note("The credential is saved. Continue to select the model.")),
      actionsNode(back, button("Continue", () => { draft.step = 2; render(); }, "profile-connect-next", false, PRIMARY)));
    if (draft.mode === "catalog") {
      const providers = providerChoices(catalog.providers, draft.provider_query);
      const selected = (catalog.providers || []).find(row => row.id === draft.provider);
      return el("div", {},
        el("label", { class: "profile-field" }, el("span", { text: "Provider" }),
          el("input", { class: "field", type: "search", testid: "profile-key-provider", "aria-label": "Provider search", value: draft.provider_query, disabled: busy,
            placeholder: "Search by name, identifier, or address", oninput: event => { draft.provider_query = event.target.value; render(); } })),
        note("Culler sends keys only to the providers in its catalog."),
        el("div", { class: "profile-choices profile-provider-results", role: "radiogroup", "aria-label": "Provider", testid: "profile-provider-results" },
          !providers.length && note("No provider matches the search. Enter another name, identifier, or address."),
          providers.map(provider => providerNode(provider, provider.id === draft.provider))),
        el("p", { class: `profile-selected-provider${selected ? " selected" : ""}`, testid: "profile-selected-provider", role: "status" },
          el("span", { text: "Selected provider" }),
          el("b", { text: selected ? selected.name : "None. Select a provider from the list." }),
          selected?.address && el("span", { text: selected.address })),
        keyField(),
        actionsNode(back, button("Continue", () => perform(() => {
          if (!selected) throw new Error("Select the provider.");
          if (!draft.api_key) throw new Error("Enter the API key.");
          return connectDuringConfigure(selected);
        }), "profile-key-connect", false, PRIMARY)));
    }
    const provider = releaseProviders(catalog).find(row => row.harness === draft.harness);
    if (!provider) return el("div", {}, note("This provider is unavailable. Select Back to select another provider."), actionsNode(back));
    // The key action is the main action after a key entry, and for a provider without a sign-in.
    const keyFirst = Boolean(draft.api_key) || !provider.subscription;
    const signIn = provider.subscription && button("Sign in", () => perform(() => connect(provider.subscription, `${provider.name} subscription`, false)),
      `profile-sign-in-${provider.subscription.id}`, provider.subscription.authorization_available === false, keyFirst ? {} : PRIMARY);
    const saveKey = button("Save key", () => perform(() => connect(provider.api_key, `${provider.name} API key`, true)), "profile-key-connect", false, keyFirst ? PRIMARY : {});
    return el("div", {},
      signIn && el("section", { class: "profile-card", testid: "profile-access-subscription" },
        el("h4", { text: "Sign in with your subscription" }),
        provider.subscription.authorization_message && note(provider.subscription.authorization_message),
        signIn),
      el("section", { class: "profile-card", testid: "profile-access-api-key" },
        el("h4", { text: "Use an API key" }),
        note("API charges apply."),
        keyField(),
        saveKey),
      actionsNode(back));
  }

  function reviewShown() {
    return view === "create" && Boolean(draft?.mode) && (draft.mode === "existing" || draft.step === 3);
  }
  function errorNodes() {
    return error ? [
      el("p", { class: "profile-error", testid: "profile-error", role: "alert", text: error }),
      button("Refresh confirmed results", () => perform(refresh), "profile-refresh"),
    ] : [];
  }
  function reviewNode(back) {
    const source = connection();
    const testing = operationActive() && operation.kind === "model_test";
    const test = button("Test model", () => perform(() => startOperation("model_test", draft.connection_id)), "profile-test",
      !draft.connection_id || !draft.model || !draft.name.trim() || source?.harness === "opencode" && catalog.opencode?.status !== "succeeded", draft.test_id ? {} : PRIMARY);
    const keep = button("Save", () => perform(save), "profile-save", !draft.test_id, draft.test_id ? PRIMARY : {});
    return [
      // The form for an existing connection shows the download status with its model list.
      draft.mode !== "existing" && downloadNode(source),
      el("section", { class: "profile-review", testid: "profile-review" },
        draft.mode === "existing" && el("h4", { text: "Test and save" }),
        el("dl", {},
          el("dt", { text: "Agent profile name" }), el("dd", { text: draft.name || "Enter an agent profile name." }),
          el("dt", { text: "Connection" }), el("dd", { text: source?.name || "Select a connection." }),
          el("dt", { text: "Model" }), el("dd", { text: draft.model || "Enter a model identifier." }),
          el("dt", { text: "Effort" }), el("dd", { text: effortLabel(draft.effort) }),
        ),
        operation?.kind === "model_test" && operationNode(),
        !draft.test_id && !testing && note("Save requires a successful model test."),
        operation?.kind !== "authorization" && errorNodes(),
      ),
      actionsNode(back, test, keep),
    ];
  }
  function leave() {
    generation++;
    stopWatches();
    try { sessionStorage.removeItem(OPERATION_KEY); } catch {}
    clearTimeout(timer);
    draft = null;
    operation = null;
    notice = "";
    view = role ? "assign" : "list";
  }
  // Back returns to the previous step, then to the screen that opened the step, then out of the form.
  function back() {
    error = "";
    if (draft.mode && draft.mode !== "existing" && draft.step > 1) draft.step -= 1;
    else if (draft.previous) {
      ({ view, mode: draft.mode, heading: draft.heading } = draft.previous);
      draft.previous = null;
      draft.connection_edit_id = null;
    } else if (draft.chosen) {
      for (const [key, value] of Object.entries(draft.suggestions)) if (draft[key] === value) draft[key] = "";
      Object.assign(draft, {
        mode: "", chosen: false, heading: "Create agent profile", step: 1, suggestions: {},
        connection_id: "", connection_edit_id: null, connection_submission_id: uuid(), connection_revision: null,
        connection_name: "", integration: "", harness: "", provider: "", provider_query: "", api_key: "",
      });
      invalidate();
    } else leave();
    render();
  }
  function stepsNode() {
    return el("ol", { class: "profile-steps", "aria-label": "Agent profile steps" }, STEPS.map((label, index) => {
      const state = index + 1 < draft.step ? "done" : index + 1 === draft.step ? "current" : "next";
      return el("li", { class: `profile-step profile-step-${state}`, "aria-current": state === "current" && "step" },
        el("span", { class: "profile-step-mark", "aria-hidden": "true", text: state === "done" ? "✓" : index + 1 }),
        label, state === "done" && el("span", { class: "sr-only", text: " (complete)" }));
    }));
  }
  function headNode() {
    return el("div", { class: "profile-flow-head" },
      el("h3", { class: "profile-flow-title", testid: "profile-flow-title", text: draft.heading }),
      !draft.mode && note("Select the provider or the connection that the agent profile uses."),
      draft.mode && draft.mode !== "existing" && stepsNode());
  }
  function createNode() {
    const previous = button("Back", back, "profile-back");
    if (!draft.mode) return choicesNode(previous);
    if (draft.mode === "existing") return [configureNode(previous), reviewNode(previous)];
    return [connectNode, configureNode, reviewNode][draft.step - 1](previous);
  }
  // A render changes only the elements whose content changed. Each other element stays in the document,
  // so a control keeps its focus, its text, its selection, its scroll position, and its open list.
  function render() {
    const assignment = view === "assign";
    dialog.classList.toggle("profile-assign", assignment);
    const header = dialogHeader({
      title: assignment ? `${title(role)} agent profile` : "Model configuration", titleId: "agent-profiles-title",
      onClose: close, className: "profile-header", closeProps: { testid: "profile-close" },
      context: el("p", { class: "profile-scope", text: assignment ? "This assignment applies to all Projects." : "Providers and models apply to all Projects." }),
    });
    const body = dialogBody({ class: "profile-body" },
      view === "create" && draft && headNode(),
      !(reviewShown() && operation?.kind !== "authorization") && errorNodes(),
      notice && el("p", { role: "status", testid: "profile-notice", text: notice }),
      (view === "test" || operation?.kind !== "model_test" || (view === "create" && !reviewShown())) && operationNode(),
      repairNode(),
      view === "test" ? el("section", {}, el("h3", { text: draft.name }),
        actionsNode(button("Test model", () => perform(() => startOperation("model_test", draft.connection_id)), "profile-test", operationActive())))
        : view === "repair" ? null : view === "disconnect" ? disconnectNode() : view === "create" ? createNode() : view === "assign" ? assignmentNode() : listNode(),
      view === "repair" ? button("All agent profiles", showList, "profile-list") : null,
    );
    const actions = body.querySelector(".profile-step-actions");
    dialog.classList.add("ui-dialog");
    reconcileChildren(dialog, [el("div", { class: "ui-dialog-shell" }, header, body, actions && dialogFooter({}, actions))], true);
  }
  return {
    async open(selectedRole = null, create = false, entry = null) {
      origin = entry || document.activeElement;
      generation++;
      stopWatches();
      busy = false;
      choosing = null;
      operation = null;
      loaded = false;
      modelLists = new Map();
      expandedProfiles.clear();
      role = selectedRole;
      view = role ? "assign" : "list";
      draft = null;
      error = "";
      notice = "";
      render();
      showDialog(dialog);
      dialog.querySelector(".ui-dialog-body")?.scrollTo(0, 0);
      const serial = generation;
      await perform(async () => {
        await refresh();
        if (serial !== generation) return;
        if (create) newDraft();
        else if (!selectedRole) {
          let recovered;
          try {
            const value = sessionStorage.getItem(OPERATION_KEY);
            recovered = value?.startsWith("{") ? JSON.parse(value) : value ? { operation_id: value } : null;
          } catch {}
          if (recovered?.operation_id) {
            role = recovered.role || null;
            await pollOperation(recovered.operation_id, generation, true);
          }
        }
      });
    },
    async repair(connectionID, action = "Reconnect", profileID = "", code = "") {
      await this.open();
      const profile = catalog.profiles.find(profile => profile.id === profileID) ?? catalog.profiles.find(profile => profile.connection_id === connectionID);
      if (action === "Test model") {
        const exact = catalog.profiles.find(item => item.id === profileID && item.connection_id === connectionID);
        if (!exact) { error = "The agent profile is unavailable. Refresh System health."; render(); return; }
        newDraft(exact);
        view = "test";
        await perform(() => startOperation("model_test", connectionID));
        return;
      }
      if (action === "Review connection") {
        newDraft(profile);
        draft.connection_id = connectionID;
        notice = ["invalid_client", "invalid_request", "invalid_scope"].includes(code) ? "The authorization service rejected the connection configuration. Use your coding harness to repair the connection configuration." : "Review this connection and agent profile. A model test verifies access.";
        render();
        return;
      }
      // A recovered authorization for this connection continues without another request.
      const active = operationActive() && operation.kind === "authorization" && operation.connection_id === connectionID ? operation : null;
      const source = catalog.connections.find(row => row.id === connectionID);
      if (!source) return;
      openRepair(connectionID, profile);
      if (active) {
        try { sessionStorage.setItem(OPERATION_KEY, JSON.stringify({ operation_id: active.operation_id, role: null })); } catch {}
        operation = active;
        busy = true;
        render();
        pollOperation(active.operation_id, generation);
      }
    },
    refreshHealth() { if (dialog.open && view === "list") render(); },
    close,
  };
}
