import { el } from "./dom.js";
import { dialogHeader, dialogBody, dialogFooter, dialogFrame, showDialog } from "./components.js";

export function customizationRole(role) {
  return role === "strategist" ? "strategist(propose)" : role;
}

const roleDescriptions = {
  "strategist(propose)": "Tell the Strategist where to look for useful work. Set the priorities it uses to propose two tasks for this Project.",
  arbiter: "Tell the Arbiter how to assess proposed work. Set the priorities and tradeoffs it considers when it selects proposals for this Project.",
  worker: "Tell the Worker how to implement tasks for this Project. Set preferences for code structure, development methods, and tests.",
  verifier: "Tell the Verifier how to check completed work. Set the risks, test methods, and evidence you want it to examine.",
  packager: "Tell the Packager how to prepare your review packet. Set the format, detail, and evidence you want for this Project.",
  scribe: "Tell the Scribe how to explain this Project in its wiki. Set the audience, level of detail, and documentation style.",
};

const roleResponsibilities = {
  "strategist(propose)": "The Strategist proposes two tasks that support your Project’s goals. It leaves the choice of what proceeds to the Arbiter.",
  arbiter: "The Arbiter selects from the two proposed tasks for your Project. It checks that each proposal is clear and fits the available capacity.",
  worker: "The Worker completes the assigned task and tests its changes. It explains the result and reports any limits honestly.",
  verifier: "The Verifier checks the result against the task’s requirements. It uses evidence to decide whether the work is ready or needs changes.",
  packager: "The Packager gives you a clear, honest summary of the work and its evidence. You decide whether to approve the result.",
  scribe: "The Scribe keeps the Project wiki accurate as the code changes. It updates the documentation and leaves the code intact.",
};

export function createRoleCustomization({ api, openCollie }) {
  let dialog = null;
  let origin = null;
  let closed = null;
  // The focus returns to the control that opened the dialog. A page poll can replace that control.
  const close = () => {
    if (!dialog) return;
    dialog.close();
    dialog.remove();
    dialog = null;
    const target = origin?.isConnected ? origin : document.querySelector(`[data-testid="${CSS.escape(origin?.dataset?.testid || "")}"]`);
    target?.focus();
    const done = closed;
    origin = closed = null;
    done?.();
  };
  const panel = (title, options = {}) => {
    close();
    origin = options.origin ?? document.activeElement;
    closed = options.onClose ?? null;
    dialog = el("dialog", { class: "role-customization", testid: "role-customization", "aria-labelledby": "role-customization-title" });
    const current = dialog;
    dialogFrame(dialog, {
      header: dialogHeader({ title, titleId: "role-customization-title", onClose: close }),
      body: dialogBody(),
    });
    dialog.addEventListener("cancel", event => { event.preventDefault(); close(); });
    document.body.append(dialog);
    showDialog(dialog);
    return current;
  };
  const pre = (text, testid) => el("pre", { text, testid });
  // A role detail opens the dialog for its role. Project configuration opens the dialog with a role selector.
  const open = async (project, fixedRole = null, options = {}) => {
    const current = panel("Custom instructions", options);
    let role = customizationRole(fixedRole ?? "worker");
    let record = null;
    let generation = 0;
    const drafts = new Map();
    const heading = current.querySelector("h2");
    const title = el("p", { testid: "customization-project", text: "Project" });
    const select = el("select", { testid: "customization-role", "aria-label": "Role" });
    const editor = el("textarea", { rows: 7, testid: "customization-editor" });
    const contract = el("div", { testid: "customization-contract" });
    const blocked = el("aside", { class: "customization-blocked", testid: "customization-blocked", hidden: true },
      el("p", { text: "This role has a block. After you correct its instructions, ask Collie to clear the block." }),
      el("button", { type: "button", class: "btn small", text: "Open Collie", onclick: () => { close(); openCollie(); } }));
    const description = el("p", { id: "customization-purpose", class: "customization-purpose" });
    current.setAttribute("aria-describedby", "customization-purpose");
    const status = el("p", { role: "status", testid: "customization-status" });
    const save = el("button", { type: "button", class: "btn primary", text: "Save instructions", testid: "customization-save" });
    const busy = value => {
      current.setAttribute("aria-busy", String(value));
      save.disabled = select.disabled = editor.disabled = value;
    };
    const load = async (keepDraft = false) => {
      const token = ++generation;
      busy(true);
      status.textContent = "Loading role customization…";
      try {
        const data = await api(`/api/projects/role-show?project=${encodeURIComponent(project)}&role=${encodeURIComponent(role)}`, { maxAge: 0 });
        if (token !== generation || dialog !== current) return;
        blocked.hidden = !data.conflicts.some(conflict => conflict.blocked && conflict.task == null);
        record = data.current;
        title.textContent = data.project_title;
        const roleTitle = data.roles.find(row => row.role === role)?.title ?? role;
        heading.textContent = `Guide the ${roleTitle}`;
        description.textContent = roleDescriptions[role];
        select.replaceChildren(...data.roles.map(row => el("option", { value: row.role, text: row.title, selected: row.role === role })));
        contract.textContent = roleResponsibilities[role];
        if (!keepDraft) {
          editor.value = drafts.has(role) ? drafts.get(role) : record.instructions;
        }
        status.textContent = "";
      } catch (err) { status.textContent = err.message; }
      finally { if (token === generation && dialog === current) { busy(false); save.disabled = !record; } }
    };
    select.addEventListener("change", () => { if (record) drafts.set(role, editor.value); role = select.value; record = null; void load(); });
    save.addEventListener("click", async () => {
      const text = editor.value;
      busy(true);
      status.textContent = "Culler saves the customization.";
      try {
        await api("/api/projects/role-save", { method: "POST", body: JSON.stringify({ project, role, instructions: text }) });
        if (dialog !== current) return;
        drafts.delete(role);
        await load(true);
        status.textContent = text.trim() ? "Saved. New runs will use these instructions." : "Saved. New runs will use the usual instructions.";
      } catch (err) { status.textContent = err.message; }
      finally { busy(false); }
    });
    current.querySelector(".ui-dialog-heading").append(el("div", { class: "customization-context" }, title, !fixedRole && el("label", {}, "Role ", select)));
    current.querySelector(".ui-dialog-body").append(
      description, blocked,
      el("div", { class: "customization-editor-heading" },
        el("label", { for: "customization-text", text: "Your instructions" }),
        el("span", { text: "Applies to new runs" })),
      editor,
      el("p", { id: "customization-help", class: "customization-note",
        text: "Leave this empty to use the usual instructions for this agent." }),
      el("aside", { class: "customization-contract-card" },
        el("h3", { text: "What you can expect" }),
        contract,
        el("p", { class: "customization-contract-note", text: "Your instructions guide its approach. These responsibilities stay the same." })));
    current.querySelector(".ui-dialog-shell").append(
      dialogFooter({ class: "customization-footer" }, status, el("div", { class: "customization-actions" }, save)));
    editor.id = "customization-text";
    editor.setAttribute("aria-describedby", "customization-help");
    editor.placeholder = "Describe what matters to you…";
    await load();
  };
  const showRun = async (run, options = {}) => {
    const current = panel(`Run ${run}`, options);
    const status = el("p", { role: "status", text: "Loading run instructions…" });
    const body = current.querySelector(".ui-dialog-body");
    body.append(status);
    try {
      const data = await api(`/api/runs/${encodeURIComponent(run)}/customization`, { maxAge: 0 });
      if (dialog !== current) return;
      status.textContent = `Project: ${data.project}. Role: ${data.role}.`;
      body.append(el("h3", { text: "Standard Culler directive" }), pre(data.standard_directive),
        el("h3", { text: "Operator instructions" }), pre(data.instructions || "No custom instructions."));
    } catch (err) { status.textContent = err.message; }
  };
  return { open, showRun };
}
