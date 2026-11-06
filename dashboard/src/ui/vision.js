import { el } from "./dom.js";

const shortPath = path => { const parts = path.split("/").filter(Boolean); return parts.length > 2 ? `…/${parts.slice(-2).join("/")}` : path; };

export function createVision({ api, host, createConversation, onSaved, onStarted }) {
  let current = null;
  const post = (action, body) => api(`/api/projects/${action}`, { method: "POST", body: JSON.stringify(body) });
  const localKey = id => `mc-vision-local-${id}`;
  function retain(ctx) {
    try { localStorage.setItem(localKey(ctx.id), JSON.stringify({ content: ctx.editor.value, revision: ctx.base.revision })); } catch {}
  }
  function forget(ctx) { try { localStorage.removeItem(localKey(ctx.id)); } catch {} }
  const SAVED = "Draft saved in Culler";
  const SAVED_TITLE = "Culler keeps the Vision draft. Save Vision writes VISION.md in the workspace.";
  function submitted(ctx) {
    const draft = ctx.base;
    return draft?.submission?.status === "saved" && draft.submission.revision === draft.revision &&
      draft.submission.saved_revision === draft.workspace_revision;
  }
  function status(ctx, label, tone, detail = "") {
    ctx.status.textContent = label; ctx.status.dataset.tone = tone;
    ctx.status.title = detail || (tone === "saved" ? SAVED_TITLE : label);
    ctx.note.textContent = detail; ctx.note.dataset.tone = tone; ctx.note.hidden = !detail;
  }
  function controls(ctx) {
    const archived = ctx.base?.project_status === "archived";
    ctx.editor.readOnly = archived || ctx.starting;
    ctx.outline.hidden = archived || ctx.editor.value !== "";
    ctx.conversation?.setReadOnly(archived);
    if (ctx.retry) ctx.retry.hidden = archived || !ctx.saveError || ctx.writing || ctx.conflict;
    ctx.save.disabled = archived || !ctx.base || ctx.dirty || ctx.writing || ctx.conflict || !ctx.editor.value.trim() || ctx.submitting || ctx.starting || ctx.base.submission?.status === "pending";
    ctx.start.hidden = archived || ctx.base?.setup_status === "ready";
    if (ctx.base?.setup_status === "ready" || ctx.base?.setup_id) ctx.startError.hidden = true;
    ctx.start.disabled = ctx.base?.setup_id ? archived || ctx.starting : ctx.save.disabled || !submitted(ctx);
    ctx.start.textContent = ctx.starting ? "Culler initializes the Project…" : ctx.base?.setup_id ? "View setup" : "Initialize project";
    ctx.startNote.classList.toggle("inactive", ctx.start.hidden);
    ctx.startNote.textContent = ctx.base?.setup_id ? "The Board shows the setup phase and any required action." : submitted(ctx) && !ctx.dirty && !ctx.conflict
      ? "Culler prepares the Project, then starts development from its Vision. Setup and development use agents and can make model requests."
      : "Use Save Vision before you initialize the Project.";
  }
  function result(ctx) {
    if (ctx.base.project_status === "archived") {
      ctx.result.textContent = "This Project is archived. You can read its Vision and conversation.";
      return;
    }
    const submission = ctx.base.submission;
    if (ctx.starting) {
      ctx.result.textContent = "Culler checks the Project before the pipeline starts.";
      return;
    }
    if (submission?.status === "saved") {
      ctx.result.textContent = `Vision saved to ${ctx.path}/VISION.md.`;
      if (ctx.base.setup_status !== "ready" && !ctx.base.setup_id) {
        ctx.result.textContent += " Initialization is incomplete. Initialize project permits setup and development from this Vision.";
      }
      if (ctx.base.revision !== submission.revision) ctx.result.textContent += " The draft has changes beyond the saved Vision.";
    } else if (submission?.status === "pending") {
      ctx.result.textContent = "Culler accepted the save. The workspace file awaits confirmation.";
    } else if (submission?.status === "failed") {
      ctx.result.textContent = `${submission.error || "Culler could not save the workspace file."} The draft remains available.`;
      if (ctx.shownFailure !== submission.submission_id) { ctx.shownFailure = submission.submission_id; void workspace(ctx); }
    } else ctx.result.textContent = ctx.base.setup_status === "ready"
      ? ""
      : "Initialization is incomplete. Submit the Project goal with Save Vision.";
  }
  function compare(ctx, remote) {
    ctx.conflict = true;
    ctx.remote = remote;
    status(ctx, "Draft changed elsewhere", "warn", "The Vision draft changed elsewhere. Compare both versions.");
    ctx.comparison.replaceChildren(
      el("div", { class: "vision-compare-head" },
        el("h3", { text: "Vision draft in Culler" }),
        el("p", { text: "Another editor changed the draft. Your text stays in the editor until you select a version." })),
      el("pre", { class: "project-vision-copy", testid: "vision-remote-copy", text: remote.content }),
      el("div", { class: "vision-compare-actions" },
        el("button", { class: "btn", type: "button", text: "Use this version", onclick: () => {
          ctx.base = remote; ctx.editor.value = remote.content; ctx.dirty = false; ctx.conflict = false;
          ctx.comparison.replaceChildren(); forget(ctx); status(ctx, SAVED, "saved"); controls(ctx);
        } }),
        el("button", { class: "btn", type: "button", testid: "vision-keep-local", disabled: ctx.base.project_status === "archived", text: "Keep my text", onclick: () => {
          ctx.base = remote; ctx.conflict = false; ctx.dirty = true; ctx.comparison.replaceChildren(); void persist(ctx);
        } })),
    );
    controls(ctx);
  }
  async function persist(ctx) {
    clearTimeout(ctx.timer);
    if (!ctx.base || ctx.base.project_status === "archived" || !ctx.dirty || ctx.writing || ctx.conflict) return;
    ctx.writing = true; ctx.saveError = false; controls(ctx); status(ctx, "Culler saves the draft…", "busy");
    const content = ctx.editor.value;
    const revision = ctx.base.revision;
    try {
      const response = await post("vision-edit", { id: ctx.id, revision, content });
      if (response.draft.id !== ctx.id || ctx.base.revision !== revision) return;
      ctx.base = response.draft;
      ctx.dirty = ctx.editor.value !== content;
      if (ctx.dirty) retain(ctx); else forget(ctx);
      status(ctx, ctx.dirty ? "Edits await a draft save" : SAVED, ctx.dirty ? "edited" : "saved");
      result(ctx);
    } catch (err) {
      retain(ctx); ctx.saveError = true;
      status(ctx, "Draft not saved", "warn", `${err.message} Your edits remain in this browser. Retry the draft save.`);
      try {
        const response = await post("vision-draft", { id: ctx.id });
        if (response.draft.id === ctx.id && response.draft.revision !== revision) compare(ctx, response.draft);
      } catch {}
    } finally {
      ctx.writing = false; controls(ctx);
      if (ctx.dirty && !ctx.conflict && ctx.base.revision !== revision) ctx.timer = setTimeout(() => persist(ctx), 400);
    }
  }
  async function workspace(ctx) {
    try {
      const response = await post("vision-workspace", { id: ctx.id });
      const preview = response.workspace;
      ctx.workspaceComparison.replaceChildren(
        el("div", { class: "vision-compare-head" },
          el("h3", { text: "VISION.md in the workspace" }),
          el("p", { text: "The workspace file changed outside Culler. Use it as the save base, then Save Vision replaces it with your draft." })),
        el("pre", { class: "project-vision-copy", testid: "vision-workspace-copy", text: preview.vision.content || "The workspace file is absent." }),
        el("div", { class: "vision-compare-actions" },
        el("button", { class: "btn", type: "button", text: "Close", onclick: () => ctx.workspaceComparison.replaceChildren() }),
        el("button", { class: "btn", type: "button", text: "Use this version as the save base", testid: "vision-rebase", disabled: ctx.base.project_status === "archived", onclick: async () => {
          try {
            const requestedRevision = ctx.base.revision;
            const changed = await post("vision-rebase", { id: ctx.id, revision: ctx.base.revision, workspace_revision: preview.vision.revision, workspace_identity: preview.identity });
            if (ctx.base.revision !== requestedRevision) return;
            ctx.base = changed.draft; ctx.workspaceComparison.replaceChildren(); result(ctx); controls(ctx);
            status(ctx, "Workspace version recorded", "info", "Culler recorded the workspace version. Save Vision submits your draft.");
          } catch (err) { status(ctx, "Workspace version not recorded", "warn", err.message); }
        } })),
      );
    } catch (err) { status(ctx, "Workspace file not read", "warn", `${err.message} Your draft remains available.`); }
  }
  async function submit(ctx) {
    if (ctx.save.disabled) return;
    ctx.submitting = true; controls(ctx);
    const requestedRevision = ctx.base.revision;
    try {
      const response = await post("vision-submit", {
        id: ctx.id, revision: ctx.base.revision,
        workspace_revision: ctx.base.workspace_revision, workspace_identity: ctx.base.workspace_identity,
      });
      if (response.draft.id === ctx.id && ctx.base.revision === requestedRevision) { ctx.base = response.draft; result(ctx); }
    } catch (err) { status(ctx, "Vision not saved", "warn", `${err.message} Your draft remains available.`); }
    finally { ctx.submitting = false; controls(ctx); }
  }
  async function start(ctx) {
    if (ctx.start.disabled || ctx.start.hidden) return;
    if (ctx.base.setup_id) { onStarted(ctx.id); return; }
    ctx.startVersion = (ctx.startVersion || 0) + 1;
    ctx.starting = true; ctx.startError.hidden = true; controls(ctx); result(ctx);
    let started = false;
    try {
      const response = await post("start", {
        id: ctx.id, identity: ctx.base.workspace_identity, revision: ctx.base.revision,
        submission_id: ctx.base.submission.submission_id,
      });
      if (current !== ctx || response.draft.id !== ctx.id) return;
      ctx.base = response.draft; started = true;
    } catch (err) {
      if (current === ctx) {
        ctx.startError.textContent = `The Project could not initialize. ${err.message}`;
        ctx.startError.hidden = false;
      }
    } finally {
      ctx.starting = false;
      if (current === ctx) { controls(ctx); result(ctx); }
    }
    if (started) onStarted(ctx.id);
  }
  async function refresh() {
    const ctx = current;
    if (!ctx?.base || !ctx.conversation || ctx.refreshing) return;
    ctx.refreshing = true;
    try {
      await Promise.all([ctx.conversation.refresh(), (async () => {
        const requestedRevision = ctx.base.revision;
        const startVersion = ctx.startVersion;
        const response = await post("vision-draft", { id: ctx.id });
        if (current !== ctx || response.draft.id !== ctx.id || ctx.writing || ctx.starting || startVersion !== ctx.startVersion) return;
        const remote = response.draft;
        if (ctx.base.revision !== requestedRevision) return;
        if (remote.revision !== ctx.base.revision && ctx.dirty) { compare(ctx, remote); return; }
        if (!ctx.dirty && !ctx.conflict && remote.revision !== ctx.base.revision) {
          ctx.editor.value = remote.content;
          status(ctx, "Draft updated", "info", "Collie or another editor updated the draft.");
        }
        const completed = ctx.base.submission?.status !== "saved" && remote.submission?.status === "saved";
        ctx.base = remote; result(ctx); controls(ctx);
        if (completed) await onSaved(ctx.id);
      })()]);
    } catch (err) { if (current === ctx) status(ctx, "Draft not refreshed", "warn", `${err.message} The draft remains in the editor.`); }
    finally { ctx.refreshing = false; }
  }
  async function open(id) {
    if (current?.id === id) { await refresh(); return; }
    leave();
    const ctx = { id, dirty: false, writing: false, conflict: false, submitting: false, base: null };
    current = ctx;
    try {
      ctx.editor = el("textarea", { class: "field vision-editor", testid: "project-field-vision", "aria-label": "Vision draft", spellcheck: "true", placeholder: "" });
      ctx.editor.readOnly = true;
      ctx.outline = el("div", { class: "vision-editor-empty" },
        el("p", { class: "vision-empty-title", text: "Describe what this Project must achieve." }),
        el("p", { text: "A short goal is enough. Collie can help you write the Vision draft." }));
      ctx.status = el("p", { class: "vision-status", role: "status", testid: "vision-draft-status", data: { tone: "info" }, text: "Loading Vision draft…" });
      ctx.note = el("p", { class: "vision-note", role: "alert", hidden: true });
      ctx.status.title = "Culler reads the Vision draft.";
      ctx.result = el("p", { class: "vision-result", role: "status", testid: "project-result" });
      ctx.comparison = el("div", { class: "vision-comparison", testid: "vision-comparison" });
      ctx.workspaceComparison = el("div", { class: "vision-comparison", testid: "vision-workspace-comparison" });
      ctx.save = el("button", { class: "btn vision-save", type: "button", text: "Save Vision", testid: "project-save", title: "Write the Vision draft to VISION.md in the workspace", onclick: () => submit(ctx) });
      ctx.start = el("button", { class: "btn primary", type: "button", text: "Initialize project", testid: "project-start", "aria-describedby": "vision-start-note", onclick: () => start(ctx) });
      ctx.startNote = el("p", { class: "vision-start-note", id: "vision-start-note", testid: "vision-start-note" });
      ctx.startError = el("p", { class: "vision-note", role: "alert", hidden: true, testid: "vision-start-error" });
      ctx.retry = el("button", { class: "btn small", type: "button", hidden: true, text: "Retry draft save", onclick: () => persist(ctx) });
      ctx.conversation = createConversation(id);
      ctx.conversation.setPending(true);
      ctx.save.disabled = true; ctx.start.disabled = true;
      ctx.editor.addEventListener("input", () => {
        ctx.dirty = ctx.editor.value !== ctx.base.content;
        if (ctx.dirty) retain(ctx); else forget(ctx);
        controls(ctx);
        status(ctx, ctx.dirty ? "Edits await a draft save" : SAVED, ctx.dirty ? "edited" : "saved");
        clearTimeout(ctx.timer); ctx.timer = setTimeout(() => persist(ctx), 400);
      });
      const destination = "";
      host.replaceChildren(
        el("header", { class: "vision-bar" },
          el("h2", { class: "vision-title" },
            el("span", { class: "vision-title-project", text: id }),
            el("span", { class: "vision-title-sep", "aria-hidden": "true", text: "/" }),
            el("span", { text: "Vision" })),
          el("div", { class: "vision-actions" },
            ctx.status,
            ctx.save,
            ctx.start),
          ctx.startNote,
          ctx.startError),
        el("div", { class: "vision-grid" }, ctx.conversation.node,
          el("section", { class: "vision-panel vision-document", "aria-label": "Vision draft" },
            el("div", { class: "vision-panel-bar" },
              el("h3", { text: "VISION.md" }),
              el("p", { class: "vision-destination", title: destination },
                el("span", { "aria-hidden": "true", text: shortPath(destination) }),
                el("bdi", { class: "sr-only", testid: "vision-destination", text: destination })),
              ctx.retry),
            el("div", { class: "vision-editor-wrap" }, ctx.editor, ctx.outline),
            el("div", { class: "vision-compare-area" }, ctx.comparison, ctx.workspaceComparison),
            el("div", { class: "vision-panel-foot" }, ctx.note, ctx.result))),
      );
      ctx.startNote.textContent = "Use Save Vision before you initialize the Project.";
      const response = await post("vision-open", { id });
      if (current !== ctx || response.draft.id !== id) return;
      ctx.base = response.draft; ctx.path = response.draft.workspace_path;
      ctx.conversation.setPending(false);
      ctx.editor.value = ctx.base.content;
      ctx.editor.placeholder = ctx.base.project_status === "archived" ? "The Vision draft is empty." : "";
      host.querySelector(".vision-title-project").textContent = ctx.base.title || "Project name unavailable";
      const path = `${ctx.path}/VISION.md`;
      const destinationNode = host.querySelector(".vision-destination");
      destinationNode.title = path;
      destinationNode.querySelector("span").textContent = shortPath(path);
      destinationNode.querySelector("bdi").textContent = path;
      status(ctx, ctx.base.project_status === "archived" ? "Read only" : SAVED, ctx.base.project_status === "archived" ? "info" : "saved");
      let local;
      try { local = JSON.parse(localStorage.getItem(localKey(id))); } catch {}
      if (local && local.content !== ctx.base.content) {
        ctx.editor.value = local.content; ctx.dirty = true;
        if (local.revision !== ctx.base.revision) compare(ctx, ctx.base);
        else { status(ctx, "Edits await a draft save", "edited"); ctx.timer = setTimeout(() => persist(ctx), 400); }
      }
      controls(ctx); result(ctx); await ctx.conversation.refresh();
    } catch (err) {
      let message = err.message;
      try {
        const catalog = await api("/api/projects", { maxAge: 0 });
        if (catalog.projects?.some(row => row.id === id && row.status === "archived")) {
          message = "This Project is archived. Its Vision draft is unavailable.";
        }
      } catch {}
      if (current === ctx) {
        status(ctx, "Vision draft unavailable", "warn", message);
        ctx.result.replaceChildren(el("button", { class: "btn", text: "Retry", onclick: () => { leave(); open(id); } }));
      }
    }
  }
  function leave() {
    if (current) {
      clearTimeout(current.timer);
      if (current.dirty && current.base) { retain(current); void persist(current); }
      current.conversation?.dispose();
    }
    current = null;
  }
  function setRouting(result) { current?.conversation?.setRouting(result); }
  return { open, refresh, leave, setRouting };
}
