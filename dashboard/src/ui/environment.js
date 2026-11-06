import { el, markdownNodes } from "./dom.js";
import { stampLabel } from "./format.js";
import { parseMarkdown } from "./markdown.js";
import { diagnosticButton, setupDiagnostic } from "./diagnosticchat.js";
import { setupHistoryAvailable } from "./setup-journal.js";

const prose = value => el("div", { class: "md" }, markdownNodes(parseMarkdown(value)));

const READINESS = {
  setup_required: "Setup required", ready: "Ready for development",
};
const PHASES = {
  idle: "Setup awaits initialization", queued: "Setup awaits an agent",
  agent: "The agent prepares the Project", approval: "Additional tools need your approval",
  build: "Culler prepares the Project image", validate: "The agent continues Project preparation",
  complete: "Project preparation is complete", failed: "Setup needs attention",
};
const INSTRUCTION = /^(\s*)(ADD|ARG|CMD|COPY|ENTRYPOINT|ENV|EXPOSE|FROM|HEALTHCHECK|LABEL|MAINTAINER|ONBUILD|RUN|SHELL|STOPSIGNAL|USER|VOLUME|WORKDIR)(?=\s|$)/i;
const APPLY_EFFECT = "Apply prepares the selected image. The setup report and source acceptance put the draft in use.";
const clone = value => JSON.parse(JSON.stringify(value));
const text = value => typeof value === "string" ? value : "";
const count = (number, noun) => `${number} ${noun}${number === 1 ? "" : "s"}`;
const post = (api, action, body) => api(`/api/environment/${action}`, { method: "POST", body: JSON.stringify(body) });
const replace = (node, ...children) => node.replaceChildren(...children.flat(2).filter(child => child !== null && child !== undefined && child !== false));

export function environmentBaseReference(digest) {
  return /^sha256:[0-9a-f]{64}$/.test(digest) ? `culler-project-base:${digest.slice(7)}` : "";
}

export function environmentBytes(value) {
  if (!Number.isFinite(value) || value < 0) return "Not measured";
  const units = ["KB", "MB", "GB", "TB"];
  let size = value, unit = -1;
  while (size >= 1000 && unit < units.length - 1) { size /= 1000; unit++; }
  if (unit < 0) return value === 1 ? "1 byte" : `${value} bytes`;
  return `${size < 100 ? size.toFixed(1) : Math.round(size)} ${units[unit]}`;
}

export function environmentConfiguration(value = {}) {
  return {
    ...clone(value), base_digest: value.base_digest ?? "",
    dockerfile: value.dockerfile ? clone(value.dockerfile) : null,
    build_files: clone(value.build_files ?? []), build_args: clone(value.build_args ?? {}),
    setup: { command: "", directory: ".", ...value.setup },
    services: clone(value.services ?? []),
    variables: clone(value.variables ?? []), secrets: clone(value.secrets ?? []),
    declarations: clone(value.declarations ?? []),
  };
}

/** Writes name and value pairs as lines of NAME=value. Quotes keep a value
 * that the plain form cannot hold. */
export function environmentVariableText(entries) {
  return entries.map(({ name, value = "" }) => {
    const plain = !/^[\s"']|\s$|[\n\r]/.test(value);
    const quoted = `"${value.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n").replace(/\r/g, "\\r")}"`;
    return `${name}=${plain ? value : quoted}`;
  }).join("\n");
}

/** Reads lines of NAME=value. A name keeps its earlier scope, and a new name
 * applies to the setup script and the agent. */
export function environmentVariables(source, scopes = {}) {
  return source.split("\n").flatMap(line => {
    if (!line.trim() || line.trimStart().startsWith("#")) return [];
    const at = line.indexOf("=");
    const name = (at < 0 ? line : line.slice(0, at)).trim().replace(/^export\s+/, "");
    let value = at < 0 ? "" : line.slice(at + 1).trim();
    const mark = value[0];
    if (value.length > 1 && (mark === '"' || mark === "'") && value.endsWith(mark)) {
      value = value.slice(1, -1);
      if (mark === '"') value = value.replace(/\\([\\"nr])/g, (_, code) => code === "n" ? "\n" : code === "r" ? "\r" : code);
    }
    return [{ name, value, scope: scopes[name] ?? "both" }];
  });
}

/** The configuration that a save sends. An operator draft omits the source
 * commit, and Culler then selects the accepted commit. */
export function environmentPayload(config, { keepSource = false } = {}) {
  const payload = clone(config);
  if (!keepSource) delete payload.source_commit;
  return payload;
}

const buildInputs = config => JSON.stringify([config?.base_digest ?? "", config?.dockerfile ?? null, config?.build_files ?? [], config?.build_args ?? {}]);
/** An explanation describes one set of image inputs. */
export function environmentExplanation(before, after, explanation) {
  return buildInputs(before) === buildInputs(after) ? explanation : "";
}

export function environmentAwaitsApproval(view) {
  const draft = view?.draft;
  return Boolean(draft) && !(view.approvals ?? []).some(row => row.revision_id === draft.id);
}

export function environmentProgress(view) {
  const env = view?.environment;
  if (!env) return null;
  const operation = (view.operations ?? []).find(row => row.id === env.active_operation_id);
  const active = operation?.status === "running";
  const pending = operation?.status === "queued";
  const approval = env.phase === "approval" && Boolean(view.draft);
  const runtimeRepair = Boolean(text(env.runtime_condition));
  const repair = !active && !pending && (env.phase === "failed" || runtimeRepair);
  const ready = env.readiness === "ready" && env.phase === "complete";
  return {
    title: operation?.kind === "runtime-check" && (active || pending)
      ? active ? "Culler checks the Project runtime" : "The runtime check awaits execution"
      : operation?.kind === "cache-clear" && (active || pending)
      ? active ? "Culler clears the Project cache" : "Cache removal awaits execution"
      : operation?.kind === "accept" && (active || pending)
        ? active ? "Culler saves the checked Project files" : "The checked Project files await acceptance"
        : runtimeRepair && !active && !pending ? "The Project environment needs repair"
        : ready
      ? READINESS.ready : pending && ["agent", "validate"].includes(env.phase)
        ? "The setup agent awaits launch" : pending && env.phase === "build"
          ? "The image build awaits execution" : PHASES[env.phase] ?? "Environment status unavailable",
    readiness: runtimeRepair ? "Runtime repair required" : READINESS[env.readiness] ?? "Readiness unavailable",
    active, pending, approval,
    tone: active || pending ? "active" : repair ? "failed" : approval ? "attention"
      : ["queued", "agent", "build", "validate"].includes(env.phase) ? "active"
      : ready ? "ready" : "idle",
    progress: active ? "The current operation is active." : pending ? "Culler will start the authorized operation." : "",
    cause: text(env.runtime_condition) || text(env.cause) || text(operation?.error),
    explanation: approval ? text(view.draft.explanation) : "",
    repair,
  };
}

/** The report belongs to the current draft or the selected environment. */
export function environmentReportSummary(view) {
  const env = view?.environment ?? {}, results = view?.results ?? [];
  return results.find(row => row.revision_id === env.draft_revision_id)
    ?? results.find(row => row.revision_id === env.selected_revision_id) ?? null;
}

/** Colors one source text for its editor. A color never changes a character. */
function paint(kind, source) {
  let continued = false;
  return source.split("\n").flatMap((line, index) => {
    const nodes = index ? ["\n"] : [];
    const comment = /^\s*#/.test(line);
    const instruction = kind === "dockerfile" && !continued && !comment && line.match(INSTRUCTION);
    if (comment) nodes.push(el("span", { class: "tok-comment", text: line }));
    else if (instruction) nodes.push(instruction[1], el("span", { class: "tok-keyword", text: instruction[2] }), line.slice(instruction[0].length));
    else if (kind === "env" && /^\s*(export\s+)?[A-Za-z_][A-Za-z0-9_]*=/.test(line)) {
      const at = line.indexOf("=");
      nodes.push(el("span", { class: "tok-key", text: line.slice(0, at) }), line.slice(at));
    } else nodes.push(line);
    if (kind === "dockerfile" && !comment) continued = /\\\s*$/.test(line);
    return nodes;
  });
}

export function createEnvironment({ api, host, openConfigs, onChanged, askDiagnostic, openSetupHistory }) {
  let current = null;
  const key = id => `culler-environment-draft-${id}`;
  function retain(ctx) {
    try { localStorage.setItem(key(ctx.id), JSON.stringify({ revision: ctx.revision, configuration: ctx.config })); } catch {}
  }
  function forget(ctx) { try { localStorage.removeItem(key(ctx.id)); } catch {} }
  /** The draft in Culler becomes the reference for the next save. */
  function rebase(ctx) {
    const env = ctx.view.environment, draft = ctx.view.draft;
    ctx.revision = draft?.id ?? "";
    ctx.base = environmentConfiguration(draft?.configuration ?? ctx.view.selected?.configuration ?? { base_digest: env.selected_base_digest });
    ctx.explanation = draft?.explanation ?? "";
    ctx.pinned = Boolean(draft) && draft.id !== env.selected_revision_id && text(draft.editor).startsWith("pipeline:");
  }
  function note(ctx, message = "", error = false) {
    const env = ctx.view.environment;
    ctx.status.textContent = message || (env.project_status === "archived" ? "This Project is archived. Its environment is read only."
      : ctx.conflict ? "" : ctx.dirty ? "Unsaved edits remain in this browser."
      : !ctx.initialRead && environmentAwaitsApproval(ctx.view) ? "This draft awaits your approval." : "");
    ctx.status.setAttribute("role", error ? "alert" : "status");
    ctx.status.dataset.tone = error ? "error" : "";
    ctx.plainNote = !message;
  }
  function changed(ctx) {
    ctx.dirty = true; retain(ctx); controls(ctx); note(ctx);
    ctx.advancedNote.textContent = advancedNote(ctx.config);
  }
  function controls(ctx) {
    const env = ctx.view.environment, archived = env.project_status === "archived";
    const operating = Boolean(env.active_operation_id), awaits = environmentAwaitsApproval(ctx.view);
    const locked = archived || ctx.busy;
    ctx.apply.textContent = ctx.dirty || !awaits ? "Save and apply" : "Apply";
    ctx.apply.disabled = locked || ctx.conflict || operating || !(ctx.dirty || awaits);
    ctx.apply.title = operating ? "Culler finishes the current operation first." : ctx.conflict ? "Select a draft version first." : "";
    ctx.discard.disabled = locked || !ctx.dirty;
    ctx.assist.disabled = locked || ctx.dirty || operating;
    ctx.assist.title = ctx.dirty ? "Apply or discard your edits first." : operating ? "Culler finishes the current operation first." : "";
    ctx.form.disabled = locked;
    ctx.local.hidden = !ctx.conflict;
  }
  function field(ctx, title, value, set, { label = title, multiline = false, options, hint = "", testid, placeholder, wide = false } = {}) {
    const input = options ? el("select", { class: "field", "aria-label": title, testid },
      options.map(option => el("option", { value: option, text: option })))
      : el(multiline ? "textarea" : "input", { class: `field${multiline ? " environment-code" : ""}`, "aria-label": title,
        testid, placeholder, type: multiline ? null : "text", spellcheck: "false", rows: multiline ? 2 : null });
    input.value = value ?? "";
    input.addEventListener(options ? "change" : "input", () => { set(input.value); changed(ctx); });
    return el("label", { class: `environment-field${wide ? " wide" : ""}` }, el("span", { text: label }), input,
      hint && el("small", { class: "environment-help", text: hint }));
  }
  /** A text area with line numbers. The colored copy lies under transparent
   * text, so both hold the same characters at the same positions. */
  function editor(ctx, title, value, set, { kind = "plain", testid, placeholder, lines = 4 } = {}) {
    const gutter = el("div", { class: "code-gutter", "aria-hidden": "true" });
    const painted = el("pre", { class: "code-paint", "aria-hidden": "true" });
    const input = el("textarea", { class: "code-input", "aria-label": title, testid, placeholder, wrap: "off",
      spellcheck: "false", autocapitalize: "off", autocomplete: "off", autocorrect: "off" });
    input.value = value ?? "";
    function draw() {
      const source = input.value;
      // A final space gives the last empty line its height.
      const copy = document.createDocumentFragment();
      for (const node of paint(kind, source)) copy.append(node);
      if (source === "" || source.endsWith("\n")) copy.append(" ");
      painted.replaceChildren(copy);
      gutter.textContent = source.split("\n").map((_, index) => index + 1).join("\n");
    }
    input.addEventListener("input", () => { draw(); set(input.value); changed(ctx); });
    // The frame scrolls. The text area keeps its origin under the colored copy.
    input.addEventListener("scroll", () => { input.scrollTop = 0; input.scrollLeft = 0; });
    draw();
    return el("div", { class: "code-editor", style: `--code-lines: ${lines}` }, gutter, el("div", { class: "code-canvas" }, painted, input));
  }
  function section(title, body, { hint = "", aside, testid } = {}) {
    return el("section", { class: "environment-section", testid },
      el("div", { class: "environment-section-head" }, el("h3", { text: title }), aside), body,
      hint && el("p", { class: "environment-help", text: hint }));
  }
  function collection(ctx, name, { title, noun, columns, empty, make, hint = "" }) {
    const rows = el("div", { class: "environment-rows" });
    function render() {
      rows.replaceChildren(...ctx.config[name].map((row, index) => el("div", { class: "environment-row", testid: `environment-${name}-row` },
        el("div", { class: "environment-row-head" }, el("span", { text: `${noun} ${index + 1}` }),
          el("button", { type: "button", class: "btn ghost small", text: "Remove", "aria-label": `Remove ${noun.toLowerCase()} ${index + 1}`,
            onclick: () => { ctx.config[name].splice(index, 1); changed(ctx); render(); } })),
        columns.map(column => column.key === "required"
          ? el("label", { class: "environment-checkbox" }, el("input", { type: "checkbox", checked: row.required, "aria-label": `${noun} ${index + 1} required`,
            onchange: event => { row.required = event.target.checked; changed(ctx); } }), "Required for readiness")
          : field(ctx, `${column.title} ${index + 1}`, row[column.key], value => { row[column.key] = value; }, column)))));
      if (!ctx.config[name].length) rows.append(el("p", { class: "environment-empty", text: empty }));
    }
    render();
    return el("section", { class: "environment-group" }, el("h4", { text: title }),
      hint && el("p", { class: "environment-help", text: hint }), rows,
      el("button", { type: "button", class: "btn small", text: `Add ${noun.toLowerCase()}`, testid: `environment-add-${name}`,
        onclick: () => { ctx.config[name].push(make()); changed(ctx); render(); } }));
  }
  function advancedNote(config) {
    const parts = [[config.services.length, "service"], [config.secrets.length, "secret"],
      [config.build_files.length, "build file"], [Object.keys(config.build_args).length, "build argument"]];
    return parts.filter(([number]) => number).map(([number, noun]) => count(number, noun)).join(" · ");
  }
  function renderStatus(ctx) {
    ctx.history.hidden = !setupHistoryAvailable(ctx.view);
    if (ctx.initialRead) {
      replace(ctx.state, el("div", { class: "environment-state", data: { tone: "idle" } },
        el("span", { class: "environment-dot", "aria-hidden": "true" }),
        el("div", { class: "environment-state-text" }, el("strong", { role: "status", text: "Culler reads the Project environment…" }))),
        el("div", { class: "environment-results-none" }, el("span", { text: "Setup report" })));
      return;
    }
    const view = ctx.view, env = view.environment, archived = env.project_status === "archived";
    const operating = Boolean(env.active_operation_id);
    const progress = environmentProgress(view), summary = environmentReportSummary(view);
    // A ready Project keeps its accepted environment while another draft is in work.
    const drafted = !env.runtime_condition && env.readiness === "ready" && Boolean(env.selected_revision_id)
      && Boolean(env.draft_revision_id) && env.draft_revision_id !== env.selected_revision_id;
    const detail = [progress.progress, progress.cause, progress.explanation,
      drafted && "Agent runs use the accepted environment until Culler accepts this draft."].filter(Boolean);
    // An equal status keeps its nodes, so a refresh preserves focus and typed text.
    const signature = JSON.stringify([progress, summary, view.disk_use, archived, operating, env.selected_revision_id, ctx.busy]);
    if (signature === ctx.stateSignature) return;
    ctx.stateSignature = signature;
    replace(ctx.state,
      el("div", { class: "environment-state", data: { tone: progress.tone } },
        el("span", { class: `environment-dot${progress.active ? " live" : ""}`, "aria-hidden": "true" }),
        el("div", { class: "environment-state-text" },
          el("strong", { text: progress.title }),
          progress.readiness !== progress.title && !drafted && el("span", { class: "tag", text: progress.readiness }),
          detail.map(line => el("p", { role: "status", text: line }))),
        progress.repair && el("button", { type: "button", class: "btn small", text: "Retry", testid: "environment-retry",
          disabled: archived || ctx.busy, onclick: () => mutate(ctx, "retry", { project_id: ctx.id }) }),
        progress.repair && diagnosticButton(setupDiagnostic(ctx.id,
          view.operations?.find(row => row.id === env.active_operation_id) || view.operations?.[0],
          progress.title, progress.cause || progress.explanation), askDiagnostic, "environment-ask-collie")),
      !summary && el("div", { class: "environment-results-none", testid: "environment-results" }, el("span", { text: "Setup report" }),
        el("span", { class: "environment-results-tally", text: "No report yet" })),
      summary && el("section", { class: "environment-results", testid: "environment-results" },
        prose(summary.summary),
        el("time", { datetime: summary.created_at, text: stampLabel(summary.created_at) }),
        summary.details && el("details", { open: Boolean(ctx.reportOpen), ontoggle: event => { ctx.reportOpen = event.target.open; } },
          el("summary", { text: "Report details" }), prose(summary.details))));
    replace(ctx.storage,
      el("dl", {},
        el("div", {}, el("dt", { text: "Project image" }), el("dd", { text: environmentBytes(view.disk_use?.image_bytes) })),
        el("div", {}, el("dt", { text: "Project home" }), el("dd", { text: environmentBytes(view.disk_use?.home_bytes), testid: "environment-home-size" })),
        el("div", {}, el("dt", { text: "Disposable cache (/cache)" }), el("dd", { text: environmentBytes(view.disk_use?.cache_bytes), testid: "environment-cache-size" }))),
      el("button", { type: "button", class: "btn small", text: "Clear /cache", testid: "environment-cache-clear",
        title: "This action removes files from /cache. It preserves the Project home and source files.",
        disabled: ctx.busy || archived || !env.selected_revision_id || operating,
        onclick: () => mutate(ctx, "cache-clear", { project_id: ctx.id }) }));
  }
  const RECORDED = {
    approve: "Culler recorded the approval and queued Project preparation.", "cache-clear": "Culler queued cache removal.",
    assist: "Culler queued the request for the setup agent.", retry: "Culler queued another attempt.",
  };
  /** Sends one request and gives true after Culler records it. */
  async function mutate(ctx, action, body) {
    if (ctx.busy) return false;
    ctx.busy = true; controls(ctx); note(ctx, "Culler records the request…");
    try {
      const view = await post(api, action, body);
      if (current !== ctx) return false;
      if (view.environment?.project_id !== ctx.id) throw new Error("The response identifies another Project.");
      ctx.view = view;
      note(ctx, RECORDED[action]);
      await onChanged?.(ctx.id);
      return true;
    } catch (error) {
      if (current === ctx) note(ctx, error.message, true);
      return false;
    } finally { ctx.busy = false; if (current === ctx) { controls(ctx); renderStatus(ctx); } }
  }
  /** Saves unsaved edits, then approves the exact saved draft. */
  async function apply(ctx) {
    if (ctx.busy) return;
    ctx.busy = true; controls(ctx);
    let message = "", error = false;
    try {
      if (ctx.dirty) {
        note(ctx, "Culler saves the draft…");
        const configuration = environmentPayload(ctx.config, { keepSource: ctx.pinned });
        const view = await post(api, "save", { project_id: ctx.id, expected_revision_id: ctx.revision || "", configuration,
          explanation: environmentExplanation(ctx.base, configuration, ctx.explanation) });
        if (view.environment?.project_id !== ctx.id) throw new Error("The response identifies another Project.");
        ctx.view = view; rebase(ctx); ctx.config = clone(ctx.base);
        ctx.dirty = false; ctx.conflict = false; forget(ctx);
      }
      message = "Culler already has this draft.";
      if (environmentAwaitsApproval(ctx.view)) {
        if (current === ctx) note(ctx, "Culler records the approval…");
        const view = await post(api, "approve", { project_id: ctx.id, revision_id: ctx.view.draft.id });
        if (view.environment?.project_id !== ctx.id) throw new Error("The response identifies another Project.");
        ctx.view = view; message = RECORDED.approve;
      }
      await onChanged?.(ctx.id);
    } catch (failure) {
      error = true;
      message = ctx.dirty ? `${failure.message} Your edits remain in this browser.` : failure.message;
      if (ctx.dirty) retain(ctx);
    } finally {
      ctx.busy = false;
      if (current === ctx) { if (ctx.dirty) controls(ctx); else render(ctx); note(ctx, message, error); }
    }
  }
  function discard(ctx) {
    forget(ctx); rebase(ctx); ctx.config = clone(ctx.base);
    ctx.dirty = false; ctx.conflict = false; render(ctx);
  }
  function render(ctx) {
    const env = ctx.view.environment, config = ctx.config;
    ctx.stateSignature = null;
    ctx.status = el("p", { class: "environment-note", role: "status", testid: "environment-status" });
    ctx.state = el("section", { class: "environment-status", testid: "environment-phase", "aria-label": "Environment status" });
    ctx.storage = el("section", { class: "environment-storage", "aria-label": "Project storage" });
    ctx.apply = el("button", { type: "button", class: "btn primary", text: "Save and apply", testid: "environment-apply", onclick: () => apply(ctx) });
    ctx.discard = el("button", { type: "button", class: "btn", text: "Discard", testid: "environment-discard", onclick: () => discard(ctx) });
    ctx.assist = el("button", { type: "button", class: "btn", text: "Ask agent to configure", testid: "environment-assist",
      "aria-expanded": String(Boolean(ctx.assistOpen)), onclick: () => { ctx.assistOpen = !ctx.assistOpen; assistPanel(); } });
    ctx.history = el("button", { type: "button", class: "btn", text: "Setup history", testid: "environment-setup-history",
      hidden: true, onclick: () => openSetupHistory(ctx.id) });
    const assistHost = el("div", {});
    function assistPanel() {
      ctx.assist.setAttribute("aria-expanded", String(Boolean(ctx.assistOpen)));
      replace(assistHost, ctx.assistOpen && el("form", { class: "environment-inline environment-assist", testid: "environment-assist-panel", onsubmit: async event => {
        event.preventDefault();
        const instruction = ctx.assistText.trim();
        if (!instruction || !await mutate(ctx, "assist", { project_id: ctx.id, instruction })) return;
        ctx.assistOpen = false; ctx.assistText = "";
        if (current === ctx) assistPanel();
      } },
        el("label", { class: "environment-field" }, el("span", { text: "Describe the tools or workflow that this Project needs." }),
          el("textarea", { class: "field", rows: 3, maxlength: 8192, required: true, testid: "environment-assist-instruction",
            oninput: event => { ctx.assistText = event.target.value; } }, ctx.assistText),
          el("small", { class: "environment-help", text: "The setup agent writes a draft. The draft takes effect after your approval." })),
        el("div", { class: "environment-inline-actions" },
          el("button", { type: "button", class: "btn small", text: "Cancel", onclick: () => { ctx.assistOpen = false; assistPanel(); } }),
          el("button", { type: "submit", class: "btn small primary", text: "Send to agent", testid: "environment-assist-send" }))));
      const area = assistHost.querySelector("textarea");
      if (area) { area.focus(); area.setSelectionRange(area.value.length, area.value.length); }
    }
    assistPanel();
    ctx.local = el("div", { class: "environment-conflict", hidden: !ctx.conflict },
      el("div", {}, el("strong", { text: "The saved draft changed elsewhere." }),
        el("p", { text: "Your edits remain in this editor. Select the version to keep." })),
      el("div", { class: "environment-inline-actions" },
        el("button", { type: "button", class: "btn small", text: "Keep my edits", onclick: () => {
          rebase(ctx); ctx.conflict = false; changed(ctx);
        } }),
        el("button", { type: "button", class: "btn small", text: "Use saved draft", onclick: () => discard(ctx) })),
      el("details", {}, el("summary", { text: "Saved draft contents" }), el("pre", { tabindex: 0, text: JSON.stringify(ctx.view.draft?.configuration, null, 2) })));
    const dockerHost = el("div", {});
    function dockerEditor() {
      const dockerfile = config.dockerfile;
      replace(dockerHost, dockerfile
        ? section("Dockerfile",
          editor(ctx, "Dockerfile", dockerfile.content, value => { dockerfile.content = value; }, { kind: "dockerfile", testid: "environment-dockerfile", lines: 6 }),
          { hint: "Each agent container starts from the image of this Dockerfile. Culler builds the image after you apply the draft. The first instruction extends the Culler base image.",
            aside: el("div", { class: "environment-section-aside" },
              el("input", { class: "environment-path", type: "text", "aria-label": "Dockerfile path", testid: "environment-dockerfile-path", spellcheck: "false", value: dockerfile.path,
                oninput: event => { dockerfile.path = event.target.value; changed(ctx); } }),
              el("button", { type: "button", class: "btn ghost small", text: "Remove", "aria-label": "Remove Dockerfile", onclick: () => { config.dockerfile = null; changed(ctx); dockerEditor(); } })) })
        : section("Dockerfile",
          el("div", { class: "environment-blank" },
            el("p", { text: "This Project uses the Culler base image without additions. The base image includes common development tools." }),
            el("button", { type: "button", class: "btn small", text: "Add Dockerfile", onclick: () => {
              config.dockerfile = { path: ".culler/Dockerfile", content: `FROM ${environmentBaseReference(config.base_digest)}\n` }; changed(ctx); dockerEditor();
            } })),
          { hint: "Add a Dockerfile when a dependency needs system packages." }));
    }
    dockerEditor();
    const scopes = Object.fromEntries(config.variables.map(entry => [entry.name, entry.scope]));
    const scope = { title: "Secret scope", label: "Available to", key: "scope", options: ["setup", "agent", "both"] };
    ctx.advancedNote = el("span", { class: "environment-advanced-note", text: advancedNote(config) });
    ctx.form = el("fieldset", { class: "environment-form" },
      el("legend", { class: "sr-only", text: "Environment configuration" }),
      dockerHost,
      section("Setup script",
        editor(ctx, "Setup script", config.setup.command, value => { config.setup.command = value; }, { kind: "shell", testid: "environment-setup-script", placeholder: "npm ci", lines: 5 }),
        { hint: "Culler runs this script with Bash in each agent container before the agent starts. Enter commands or the path of a repository script." }),
      section("Environment variables",
        editor(ctx, "Environment variables", environmentVariableText(config.variables), value => { config.variables = environmentVariables(value, scopes); }, { kind: "env", testid: "environment-variables", placeholder: "NAME=value", lines: 4 }),
        { hint: "One NAME=value per line. The setup script and each agent run receive these values. Enter no credentials here." }),
      el("details", { class: "environment-advanced", testid: "environment-advanced", open: Boolean(ctx.advancedOpen),
        ontoggle: event => { ctx.advancedOpen = event.target.open; } },
        el("summary", {}, el("span", { text: "Advanced" }), ctx.advancedNote),
        el("div", { class: "environment-advanced-body" },
          el("section", { class: "environment-group" }, el("h4", { text: "Base image" }),
            field(ctx, "Base image digest", config.base_digest, value => { config.base_digest = value; },
              { label: "Digest", hint: "A base change applies to this Project after preparation and source acceptance.", testid: "environment-base", placeholder: "sha256:" })),
          collection(ctx, "services", { title: "Services", noun: "Service", columns: [
            { key: "name", title: "Service name", label: "Name" }, { key: "directory", title: "Service directory", label: "Directory" },
            { key: "command", title: "Service command", label: "Start command", wide: true },
            { key: "ready_command", title: "Service readiness command", label: "Readiness command", wide: true },
          ], empty: "The workflow requires no service.", make: () => ({ name: "", command: "", directory: ".", ready_command: "" }) }),
          collection(ctx, "secrets", { title: "Secrets", noun: "Secret", hint: "A secret names a stored reference. Secret values stay outside this page.", columns: [
            { key: "name", title: "Secret variable name", label: "Variable name" }, { key: "reference", title: "Secret reference", label: "Reference" }, scope,
          ], empty: "No secret reference is set.", make: () => ({ name: "", reference: "", scope: "setup" }) }),
          collection(ctx, "build_files", { title: "Build files", noun: "Build file", hint: "The Dockerfile can copy these repository files into the image.", columns: [
            { key: "path", title: "Repository file path", label: "Repository path", wide: true },
            { key: "content", title: "File contents", label: "Contents", multiline: true, wide: true },
          ], empty: "The Dockerfile has no additional context files.", make: () => ({ path: "", content: "" }) }),
          el("section", { class: "environment-group" }, el("h4", { text: "Build arguments" }),
            field(ctx, "Build arguments", environmentVariableText(Object.entries(config.build_args).map(([name, value]) => ({ name, value }))),
              value => { config.build_args = Object.fromEntries(environmentVariables(value).map(entry => [entry.name, entry.value])); },
              { label: "Arguments", multiline: true, placeholder: "NAME=value", hint: "One NAME=value per line. Use nonsecret values.", testid: "environment-build-args" })),
          el("section", { class: "environment-group" }, el("h4", { text: "Setup directory" }),
            field(ctx, "Setup directory", config.setup.directory, value => { config.setup.directory = value; },
              { label: "Directory", hint: "The setup script starts in this repository directory." })),
          el("section", { class: "environment-group" }, el("h4", { text: "Dependency declarations" }),
            field(ctx, "Declaration paths", config.declarations.join("\n"), value => { config.declarations = value.split("\n").map(line => line.trim()).filter(Boolean); },
              { label: "Repository paths", multiline: true, hint: "One repository path per line. Package manifests and lockfiles record Project dependencies." })))));
    host.replaceChildren(el("div", { class: "environment-page" },
      el("header", { class: "environment-heading" },
        el("button", { type: "button", class: "btn ghost small environment-back", text: "Back to Configs", onclick: openConfigs }),
        el("div", { class: "environment-title" },
          el("div", {}, el("p", { class: "project-eyebrow", text: "Project environment" }), el("h2", { text: env.project_title || ctx.id })),
          el("div", { class: "environment-title-actions" }, ctx.history, ctx.assist)),
        el("p", { class: "environment-lede", text: "Each agent run of this Project starts from this configuration. Setup prepares it automatically." })),
      assistHost, ctx.local, ctx.state, ctx.form, ctx.storage,
      el("footer", { class: "environment-savebar" },
        el("div", { class: "environment-savebar-text" }, ctx.status, el("p", { class: "environment-help", text: APPLY_EFFECT })),
        el("div", { class: "environment-inline-actions" }, ctx.discard, ctx.apply))));
    controls(ctx); renderStatus(ctx); note(ctx);
  }
  async function refresh() {
    const ctx = current;
    if (!ctx || ctx.busy || ctx.refreshBusy || ctx.initialRead) return;
    ctx.refreshBusy = true;
    try {
      const view = await post(api, "get", { project_id: ctx.id });
      if (current !== ctx || ctx.busy) return;
      if (view.environment?.project_id !== ctx.id) throw new Error("The response identifies another Project.");
      const priorDraft = ctx.view.draft?.id;
      ctx.view = view;
      if ((view.draft?.id ?? "") !== ctx.revision) {
        if (ctx.dirty) {
          if (!ctx.conflict || priorDraft !== view.draft?.id) { ctx.conflict = true; render(ctx); }
          else renderStatus(ctx);
        } else { rebase(ctx); ctx.config = clone(ctx.base); render(ctx); }
      } else { renderStatus(ctx); controls(ctx); if (ctx.plainNote) note(ctx); }
    } catch (error) { if (current === ctx) note(ctx, `${error.message} The draft remains available.`, true); }
    finally { ctx.refreshBusy = false; }
  }
  async function open(id) {
    if (current?.id === id) { await refresh(); return; }
    leave();
    const ctx = { id, dirty: false, busy: true, initialRead: true, view: { environment: { project_id: id } },
      config: environmentConfiguration(), base: environmentConfiguration(), explanation: "", revision: "", conflict: false,
      assistOpen: false, assistText: "", reportOpen: false, advancedOpen: false };
    current = ctx;
    render(ctx);
    try {
      const view = await post(api, "get", { project_id: id });
      if (current !== ctx) return;
      if (view.environment?.project_id !== id) throw new Error("The response identifies another Project.");
      ctx.initialRead = false; ctx.busy = false;
      ctx.view = view; rebase(ctx); ctx.config = clone(ctx.base);
      let local;
      try { local = JSON.parse(localStorage.getItem(key(id))); } catch {}
      if (local?.configuration) {
        ctx.config = environmentConfiguration(local.configuration);
        ctx.dirty = true; ctx.conflict = local.revision !== ctx.revision;
      }
      render(ctx);
    } catch (error) {
      if (current === ctx) {
        replace(ctx.state, el("div", { class: "environment-state", data: { tone: "failed" } },
          el("span", { class: "environment-dot", "aria-hidden": "true" }),
          el("div", { class: "environment-state-text" }, el("strong", { text: "Environment status unavailable" }), el("p", { role: "alert", text: error.message })),
          el("button", { type: "button", class: "btn small", text: "Retry", onclick: () => { current = null; void open(id); } })));
      }
    }
  }
  function leave() { if (current?.dirty) retain(current); current = null; }
  /** Shows the Dockerfile editor of the open page and moves the focus into it. */
  function focusDockerfile() {
    const input = current && !host.hidden && host.querySelector('[data-testid="environment-dockerfile"]');
    if (!input) return;
    input.closest(".environment-section").scrollIntoView({ block: "center" });
    input.focus({ preventScroll: true });
  }
  return { open, refresh, leave, focusDockerfile };
}
