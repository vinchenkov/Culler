import { el } from "./dom.js";
import { dialogHeader, dialogBody, dialogFooter, dialogFrame, showDialog } from "./components.js";

export function createProjectDeletion({ api, deleted }) {
  function open(project, opener) {
    let busy = false;
    let removed = false;
    let read = 0;
    const restoreFocus = () => {
      const target = opener.isConnected ? opener
        : document.querySelector(`[data-testid="${CSS.escape(opener.dataset.testid)}"]`)
          ?? document.querySelector('[data-testid="configs-add-project"]');
      target?.focus();
    };
    const details = el("div", { testid: "project-delete-details", "aria-live": "polite" });
    const notice = el("p", { class: "form-error", role: "alert", hidden: true, testid: "project-delete-error" });
    const cancel = el("button", { type: "button", class: "btn", text: "Cancel", onclick: () => dialog.close() });
    const retry = el("button", { type: "button", class: "btn", text: "Check again", hidden: true, onclick: load });
    const remove = el("button", { type: "button", class: "btn danger", text: "Delete Project", disabled: true, testid: "project-delete-confirm", onclick: submit });
    const close = () => { if (!busy) dialog.close(); };
    const dialog = el("dialog", { class: "session-delete-dialog", "aria-labelledby": "project-delete-title", testid: "project-delete-dialog" });
    const header = dialogHeader({ title: `Delete ${project.title}?`, titleId: "project-delete-title", onClose: close });
    dialogFrame(dialog, { header, body: dialogBody({},
      el("p", { text: "Culler immediately stops this Project’s containers. Culler permanently deletes the Project and all its database records. This includes setup records and setup conversations." }),
      el("p", { text: "Your source repository and other Projects remain intact." }),
      el("p", { class: "muted", text: "Culler retains diagnostic files on disk." }),
      details, notice), footer: dialogFooter({ class: "session-delete-actions" }, cancel, retry, remove) });

    async function load() {
      const serial = ++read;
      remove.disabled = true;
      retry.hidden = true;
      notice.hidden = true;
      details.replaceChildren(el("p", { text: "Culler reads the Project records…" }));
      try {
        const view = await api(`/api/projects/${encodeURIComponent(project.id)}/delete`, { maxAge: 0 });
        if (!dialog.open || serial !== read) return;
        details.replaceChildren(
          el("ul", {},
            el("li", { text: `${view.tasks} ${view.tasks === 1 ? "task" : "tasks"}` }),
            el("li", { text: `${view.reviews} ${view.reviews === 1 ? "review" : "reviews"}` }),
            el("li", { text: `${view.setup_operations} setup ${view.setup_operations === 1 ? "operation" : "operations"}` })),
          ...(view.blockers.length ? [el("ul", {}, ...view.blockers.map(text => el("li", { text })))] : []));
        remove.disabled = view.blockers.length > 0;
        retry.hidden = view.blockers.length === 0;
      } catch (error) {
        if (!dialog.open || serial !== read) return;
        details.replaceChildren();
        notice.textContent = error.message;
        notice.hidden = false;
        retry.hidden = false;
      }
    }

    async function submit() {
      if (busy || remove.disabled) return;
      busy = true;
      remove.disabled = true;
      cancel.disabled = true;
      header.querySelector("button").disabled = true;
      retry.disabled = true;
      notice.hidden = true;
      details.replaceChildren(el("p", { text: "Culler stops the Project containers. Culler then deletes its records…" }));
      try {
        await api(`/api/projects/${encodeURIComponent(project.id)}/delete`, { method: "POST", body: JSON.stringify({ confirm: true }) });
      } catch (error) {
        busy = false;
        cancel.disabled = false;
        header.querySelector("button").disabled = false;
        retry.disabled = false;
        await load();
        if (!dialog.open) return;
        notice.textContent = error.message;
        notice.hidden = false;
        retry.hidden = false;
        return;
      }
      busy = false;
      removed = true;
      dialog.close();
      await deleted(project.id);
      restoreFocus();
    }

    dialog.addEventListener("cancel", event => { if (busy) event.preventDefault(); });
    dialog.addEventListener("close", () => {
      ++read;
      dialog.remove();
      if (!removed) restoreFocus();
    });
    document.body.append(dialog);
    showDialog(dialog);
    cancel.focus();
    void load();
  }

  return {
    button(project) {
      const button = el("button", {
        type: "button", class: "btn small rust", text: "Delete Project",
        "aria-label": `Delete ${project.title}`, testid: `delete-project-${project.id}`,
        onclick: () => open(project, button),
      });
      return button;
    },
  };
}
