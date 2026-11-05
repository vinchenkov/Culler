import { el } from "./dom.js";

export function dialogHeader({ title, titleId, onClose, closeProps = {}, className = "", context = [] }) {
  const heading = typeof title === "string"
    ? el("h2", { id: titleId, class: "ui-dialog-title", text: title })
    : title;
  return el("header", { class: `ui-dialog-head ${className}`.trim() },
    el("div", { class: "ui-dialog-heading" }, heading, context),
    el("button", {
      type: "button", text: "Close", "aria-label": "Close", title: "Close (Esc)", onclick: onClose,
      ...closeProps, class: `btn dialog-close ui-dialog-close ${closeProps.class || ""}`.trim(),
    }));
}

export function dialogBody(props = {}, ...children) {
  return el("div", { ...props, class: `ui-dialog-body ${props.class || ""}`.trim() }, ...children);
}

export function dialogFooter(props = {}, ...children) {
  return el("footer", { ...props, class: `ui-dialog-foot ${props.class || ""}`.trim() }, ...children);
}

export function dialogFrame(dialog, { header, body, footer, shell = el("div") }) {
  dialog.classList.add("ui-dialog");
  shell.classList.add("ui-dialog-shell");
  shell.replaceChildren(...[header, body, footer].filter(Boolean));
  dialog.replaceChildren(shell);
  return shell;
}

export function showDialog(dialog) {
  if (!dialog.open) dialog.dataset.dialogStacked = String(Boolean(document.querySelector("dialog:modal")));
  dialog.showModal();
}
