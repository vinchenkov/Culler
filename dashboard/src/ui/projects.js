import { el } from "./dom.js";
import { dialogHeader, dialogBody, dialogFooter, dialogFrame, showDialog } from "./components.js";

const DRAFT_KEY = "mc-project-draft";
export const visionHelp = "The vision states your project's open-ended, high-level goal. Culler strategizes and submits work that advances the vision.";
const note = (text) => el("p", { class: "note", text });

export function projectState({ available, projects, selected }) {
  if (!available) return "unavailable";
  const row = projects?.find((item) => item.id === selected);
  if (!row && !projects?.some((item) => item.status !== "archived")) return "empty";
  if (row?.status === "archived") return "archived";
  if (row?.vision_status?.ready === false) return "vision";
  if (row?.status === "paused") return "paused";
  return "ready";
}

export function systemExplanation() {
  return el("ol", { class: "project-model", "aria-label": "How Culler develops your project" },
    ...[
      ["Describe your vision", visionHelp],
      ["Agents work", "Agents propose work, then develop and check changes."],
      ["Stay in the loop", "Review the results before Culler merges changes. Each review explains what changed and why, so you stay familiar with your Project."],
    ].map(([title, text], index) => el("li", {},
      el("span", { class: "project-model-number", text: index + 1, "aria-hidden": "true" }),
      el("strong", { text: title }), el("p", { text }),
    )),
  );
}

export function agentReadinessNode(openModels) {
  return el("div", { class: "project-agents", testid: "project-agents" },
    note("Agent configuration requires attention before agents can start."),
    el("button", { type: "button", class: "btn small", text: "Model configuration", onclick: openModels }),
  );
}

export function createProjectControls({ api, onSaved, openVision }) {
  const dialog = el("dialog", { class: "project-dialog", testid: "project-dialog", "aria-labelledby": "project-form-title" });
  document.body.append(dialog);
  let draft, preview, error = "", busy = false, checking = false, serial = 0, timer, origin, suggestion = "";
  const post = (action, body) => api(`/api/projects/${action}`, { method: "POST", body: JSON.stringify(body) });
  function persist() { try { sessionStorage.setItem(DRAFT_KEY, JSON.stringify(draft)); } catch {} }
  function close() {
    if (busy) return;
    ++serial;
    clearTimeout(timer);
    dialog.close();
    const target = origin?.isConnected ? origin
      : document.querySelector(`[data-testid="${CSS.escape(origin?.dataset?.testid || "")}"]`);
    target?.focus();
  }
  dialog.addEventListener("cancel", event => { event.preventDefault(); close(); });
  const next = () => dialog.querySelector('[data-testid="project-next"]');
  function controls() {
    next().disabled = busy || checking || !preview || !draft.title.trim();
    const status = dialog.querySelector('[data-testid="project-folder-status"]');
    status.hidden = !checking;
  }
  async function checkFolder() {
    clearTimeout(timer);
    const request = ++serial;
    checking = Boolean(draft.path.trim()); preview = null; error = "";
    render();
    if (!checking) return;
    try {
      const result = await post("setup-preview", { path: draft.path.replace(/\/+$/, "") || draft.path });
      if (request !== serial) return;
      preview = result.workspace;
      draft.path = preview.path;
      if (!draft.title.trim() || draft.title === suggestion) draft.title = preview.path.split("/").pop();
      suggestion = preview.path.split("/").pop();
      persist();
    } catch (err) { if (request === serial) error = err.message; }
    finally { if (request === serial) { checking = false; render(); } }
  }
  async function chooseFolder() {
    clearTimeout(timer); ++serial; checking = false; busy = true; error = ""; render();
    let selected = false;
    try {
      const result = await post("choose-folder", {});
      if (result.path) { draft.path = result.path; preview = null; persist(); selected = true; }
    } catch (err) { error = err.message; }
    finally { busy = false; render(); }
    if (selected) await checkFolder();
  }
  async function register() {
    if (busy || checking || !preview || !draft.title.trim()) return;
    busy = true; error = ""; render();
    try {
      await post("setup-save", {
        id: draft.id, title: draft.title.trim(), path: preview.path, identity: preview.identity,
        create: !preview.exists, confirm_access: true, revision: preview.vision.revision,
      });
      try { sessionStorage.removeItem(DRAFT_KEY); } catch {}
      const id = draft.id;
      busy = false; close();
      openVision(id);
      await onSaved(id);
    } catch (err) { error = err.message; }
    finally { busy = false; render(); }
  }
  function field(label, key, options = {}) {
    return el("label", { class: "project-field" }, el("span", { text: label }), el("input", {
      class: "field", type: "text", value: draft[key], testid: `project-field-${key}`, autocomplete: "off", disabled: busy, ...options,
      oninput: event => {
        draft[key] = event.target.value; persist();
        if (key === "path") {
          clearTimeout(timer); ++serial; preview = null; checking = Boolean(draft.path.trim()); error = "";
          dialog.querySelector('[data-testid="project-error"]')?.remove();
          if (checking) timer = setTimeout(checkFolder, 400);
        }
        controls();
      },
    }));
  }
  function render() {
    if (!draft) return;
    const focused = document.activeElement;
    const focus = focused?.dataset.testid;
    const selection = focused?.selectionStart == null ? null
      : [focused.selectionStart, focused.selectionEnd, focused.selectionDirection];
    const scrollTop = dialog.querySelector(".ui-dialog-body")?.scrollTop || 0;
    dialogFrame(dialog, {
      header: dialogHeader({ title: "Add Project", titleId: "project-form-title", onClose: close,
        className: "project-dialog-head", context: el("p", { class: "project-eyebrow", text: "Folder and name" }),
        closeProps: { testid: "project-close", disabled: busy } }),
      body: dialogBody({ class: "project-dialog-body" },
        note("Choose the folder Culler will use for this Project."),
        error ? el("p", { class: "project-error", role: "alert", testid: "project-error", text: error }) : null,
        field("Project folder on this Mac", "path", { placeholder: "/Users/you/Projects/my-project" }),
        el("button", { class: "btn", type: "button", text: "Choose folder…", testid: "project-choose-folder", disabled: busy, onclick: chooseFolder }),
        note("Choose an existing folder, or enter a new folder path inside a folder that exists."),
        el("p", { role: "status", testid: "project-folder-status", hidden: !checking, text: "Culler checks the folder…" }),
        field("Project name", "title", { maxlength: 200 })),
      footer: dialogFooter({ class: "project-dialog-foot" }, note("Next: develop the Vision with Collie."),
        el("button", { class: "btn primary", type: "button", text: busy ? "Please wait…" : "Continue", testid: "project-next", onclick: register })),
    });
    controls();
    if (focus) {
      const node = dialog.querySelector(`[data-testid="${CSS.escape(focus)}"]`);
      node?.focus({ preventScroll: true });
      if (selection && node?.setSelectionRange) node.setSelectionRange(...selection);
    }
    dialog.querySelector(".ui-dialog-body").scrollTop = scrollTop;
  }
  async function open(id = null) {
    if (id) { openVision(id); return; }
    ++serial; clearTimeout(timer); origin = document.activeElement;
    draft = { id: `project-${crypto.randomUUID().slice(0, 12)}`, path: "", title: "" };
    try { const saved = JSON.parse(sessionStorage.getItem(DRAFT_KEY)); if (saved?.id && !saved.registered) draft = saved; } catch {}
    preview = null; error = ""; busy = false; checking = false; suggestion = "";
    render(); if (!dialog.open) showDialog(dialog);
    dialog.querySelector('[data-testid="project-field-path"]').focus();
    if (draft.path.trim()) await checkFolder();
  }
  return { open };
}
