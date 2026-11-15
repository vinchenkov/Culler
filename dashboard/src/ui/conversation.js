import { dialogHeader, dialogBody, dialogFooter, dialogFrame, showDialog } from "./components.js";
import { el, markdownNodes, reconcileChildren, svg } from "./dom.js";
import { parseMarkdown } from "./markdown.js";
import { attachmentView, collieModelView, transcriptRows } from "./colliefmt.js";
import { systemActionButton } from "./systemhealth.js";

const PLACEHOLDER = "Tell Collie your goal or ask a question";

export function createMessageRenderer({ token, unavailable, retry, showError }) {
  // Attachment downloads use authenticated requests.
  const attachmentBlobs = new Map();
  function attachmentBlobUrl(path) {
    let promise = attachmentBlobs.get(path);
    if (!promise) {
      const headers = {};
      if (token()) headers["authorization"] = `Bearer ${token()}`;
      promise = fetch(path, { headers })
        .then(async (res) => {
          if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            throw new Error(body?.error?.message ?? `request failed (${res.status})`);
          }
          return URL.createObjectURL(await res.blob());
        })
        .catch((err) => {
          attachmentBlobs.delete(path);
          throw err;
        });
      attachmentBlobs.set(path, promise);
    }
    return promise;
  }

  function attachmentNode(ref) {
    const view = attachmentView(ref);
    if (view.kind === "inert") {
      return el("span", { class: "tag tone-stone", testid: "attachment-ref", text: view.label, title: view.label });
    }
    if (view.kind === "inline") {
      const img = el("img", { alt: view.name, loading: "lazy" });
      const node = el(
        "a",
        {
          class: "attachment inline",
          testid: "attachment-inline",
          "data-url": view.url,
          target: "_blank",
          rel: "noopener",
          title: view.caption,
        },
        img,
        el("span", { class: "tag tone-stone caption", text: view.caption }),
      );
      attachmentBlobUrl(view.url).then((blobUrl) => {
        img.src = blobUrl;
        node.href = blobUrl;
      }, () => {});
      return node;
    }
    const node = el("a", {
      class: "tag tone-stone attachment download",
      testid: "attachment-download",
      "data-url": view.url,
      href: "#",
      download: view.name,
      title: view.caption,
      text: view.caption,
    });
    node.addEventListener("click", (event) => {
      if (node.getAttribute("href") !== "#") return;
      event.preventDefault();
      attachmentBlobUrl(view.url).then((blobUrl) => {
        node.href = blobUrl;
        node.click();
      }, showError);
    });
    return node;
  }

  // The shared transcript distinguishes active, completed, and failed replies.
  function messageNode(row, sessionID) {
    const pending = row.provisional && row.body === "";
    const statusLabel = row.provisional && unavailable() ? "Reply status unavailable" : row.statusLabel;
    const active = !unavailable() && ["active", "authorized"].includes(row.replyState);
    const bubble = pending && active
      ? el("div", { class: "bubble thinking", "aria-label": statusLabel },
        el("span", { class: "dot" }), el("span", { class: "dot" }), el("span", { class: "dot" }))
      : row.direction === "reply" && row.body !== ""
        ? el("div", { class: "bubble md" }, markdownNodes(parseMarkdown(row.body)))
        : el("div", { class: "bubble", text: row.body === "" ? (statusLabel ?? "No reply yet") : row.body });
    return el(
      "div",
      {
        class: `msg${row.own ? " own" : ""}${row.provisional ? " draft" : ""}`,
        testid: pending ? "collie-pending" : row.provisional ? "collie-draft" : "collie-message",
        data: { key: `${sessionID}:${row.id ?? "draft"}` },
      },
      el(
        "div",
        { class: "who" },
        document.createTextNode(row.who),
        row.viaLabel && el("span", { class: "tag tone-teal", testid: "echo-attribution", text: row.viaLabel }),
        row.provisional && el("span", { class: "tag tone-stone", role: "status", text: statusLabel ?? "No reply yet" }),
        row.age && el("span", { text: row.age, title: row.stamp }),
      ),
      bubble,
      row.replyState === "failed" && systemActionButton({ action: "Retry reply", attempt_id: row.attemptID, session_id: sessionID, message_id: row.messageID }, retry, "btn small"),
      row.attachments.length > 0 &&
        el("div", { class: "attachments" }, row.attachments.map(attachmentNode)),
    );
  }

  return messageNode;
}

export function createUploadInput(testid) {
  let requestID = crypto.randomUUID(), submittedBody = null, disabled = false;
  let entries = [], pending = Promise.resolve(), pendingCount = 0, generation = 0, sequence = 0;
  const input = el("input", { type: "file", multiple: true, testid, "aria-label": "Attach files" });
  const status = el("span", { class: "hint", role: "status" });
  const previews = el("div", { class: "conversation-draft-files" });
  function paint() {
    previews.replaceChildren(...entries.map(entry => el("div", { class: "conversation-draft-file" },
      entry.url && el("img", { src: entry.url, alt: entry.file.name }),
      el("span", { text: entry.file.name }),
      el("button", { type: "button", class: "btn small", text: "Remove", disabled,
        "aria-label": `Remove ${entry.file.name}`, onclick: () => {
          if (disabled) return;
          if (entry.url) URL.revokeObjectURL(entry.url);
          entries = entries.filter(item => item !== entry);
          requestID = crypto.randomUUID(); paint();
        } }))));
  }
  function add(files, paste = false) {
    if (disabled || !files.length) return;
    const current = generation;
    requestID = crypto.randomUUID();
    pendingCount++;
    status.textContent = "Loading attachments…";
    pending = pending.then(async () => {
      if (current !== generation) return;
      const added = [];
      let total = entries.reduce((size, entry) => size + entry.file.size, 0);
      for (let file of files) {
        if (file.size > 16 * 1024 * 1024) throw new Error("Attach at most 16 MiB per message.");
        let bytes;
        try { bytes = new Uint8Array(await file.arrayBuffer()); }
        catch { throw new Error("Culler could not read the attachment. Paste or attach the file again."); }
        let binary = "";
        for (let offset = 0; offset < bytes.length; offset += 8192) binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
        const data = btoa(binary);
        if (paste && added.some(entry => entry.data === data && entry.file.type === file.type)) continue;
        if (paste && (!file.name || file.name === "image.png")) {
          const extension = file.type.split("/")[1]?.replace(/[^a-z0-9]/gi, "") || "png";
          file = new File([file], `Screenshot-${++sequence}.${extension}`, { type: file.type });
        }
        total += file.size;
        if (total > 16 * 1024 * 1024) throw new Error("Attach at most 16 MiB per message.");
        added.push({ file, data });
      }
      if (current !== generation) return;
      for (const entry of added) {
        if (entry.file.type.startsWith("image/")) entry.url = URL.createObjectURL(entry.file);
      }
      entries.push(...added);
      requestID = crypto.randomUUID(); status.textContent = ""; paint();
    }).catch(err => { if (current === generation) status.textContent = err.message; })
      .finally(() => { pendingCount--; });
  }
  input.addEventListener("change", () => { add([...input.files]); input.value = ""; });
  return {
    node: el("div", { class: "conversation-uploads" }, input, status, previews),
    add,
    reportError() { status.textContent = "Culler could not read the image. Paste the image again."; },
    async files() {
      await pending;
      return entries.map(({ file, data }) => ({ name: file.name, media_type: file.type || "application/octet-stream", data }));
    },
    clear() {
      generation++;
      for (const entry of entries) if (entry.url) URL.revokeObjectURL(entry.url);
      entries = []; input.value = ""; status.textContent = "";
      requestID = crypto.randomUUID(); submittedBody = null; paint();
    },
    count() { return entries.length + pendingCount; },
    setDisabled(value) { if (disabled === value) return; disabled = value; input.disabled = value; paint(); },
    requestID(body) {
      if (body !== undefined && submittedBody !== null && submittedBody !== body) requestID = crypto.randomUUID();
      if (body !== undefined) submittedBody = body;
      return requestID;
    },
  };
}

const EXAMPLE_PROMPTS = [
  "Help me write a Vision for this Project.",
  "Read the Project files and suggest a goal.",
  "Ask me questions about my goal.",
];

const chevron = () => svg("svg", { viewBox: "0 0 16 16", width: 12, height: 12, "aria-hidden": "true", class: "collie-model-chevron" },
  svg("path", { d: "m4 6 4 4 4-4", fill: "none", stroke: "currentColor", "stroke-width": 1.6, "stroke-linecap": "round", "stroke-linejoin": "round" }));
const check = () => svg("svg", { viewBox: "0 0 16 16", width: 14, height: 14, "aria-hidden": "true", class: "collie-model-check" },
  svg("path", { d: "m3.5 8.5 3 3 6-7", fill: "none", stroke: "currentColor", "stroke-width": 1.8, "stroke-linecap": "round", "stroke-linejoin": "round" }));

export function createModelPicker({ api, session, startOver, switchModel, onRouting, openProfiles, prefix = "vision-collie" }) {
  let routing = null, loading = null, failedAt = 0, busy = false, error = "", disabled = false, retryBinding = null;
  let confirmation = null;
  const name = el("span", { class: "collie-model-name", text: "Loading model…" });
  const meta = el("span", { class: "collie-model-meta" });
  const button = el("button", {
    class: "collie-model", type: "button", testid: `${prefix}-model`, disabled: true,
    "aria-haspopup": "dialog", "aria-expanded": "false",
    onclick: () => (menu.hidden ? show() : hide()),
    onkeydown: event => { if (event.key === "ArrowDown" && menu.hidden) { event.preventDefault(); show(); } },
  }, name, meta, chevron());
  const menu = el("div", { class: "collie-model-menu", role: "dialog", "aria-label": "Collie model", testid: `${prefix}-model-menu`, hidden: true });
  const items = () => [...menu.querySelectorAll("button:not([disabled])")];
  menu.addEventListener("keydown", event => {
    const list = items(), at = list.indexOf(document.activeElement);
    if (event.key === "Escape") { event.preventDefault(); hide(); button.focus(); }
    else if (event.key === "Tab") hide();
    else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      list[(at + (event.key === "ArrowDown" ? 1 : list.length - 1)) % list.length]?.focus();
    } else if (event.key === "Home" || event.key === "End") {
      event.preventDefault();
      (event.key === "Home" ? list[0] : list.at(-1))?.focus();
    }
  });
  const outside = event => { if (!menu.contains(event.target) && !button.contains(event.target)) hide(); };

  function paint() {
    const view = collieModelView(routing, session().binding);
    if (!view.current) {
      name.textContent = routing || failedAt ? "Model unavailable" : "Loading model…";
      meta.hidden = true; button.disabled = true;
      hide(); return;
    }
    name.textContent = view.current.label;
    meta.textContent = view.current.harness;
    meta.hidden = !view.current.harness;
    button.disabled = disabled || busy;
    button.dataset.pending = view.pending ? "true" : "false";
    button.title = view.pending
      ? `This conversation uses ${view.current.label}. New conversations use ${view.next.label}.`
      : `Collie uses ${view.current.label} through the ${view.current.name} agent profile.`;
    if (menu.hidden) return;
    const focused = document.activeElement?.dataset?.binding;
    menu.replaceChildren(...[
      el("div", { class: "collie-model-head" },
        el("p", { class: "collie-model-title", text: "Collie model" }),
        el("p", { text: session().messages
          ? (switchModel ? "Selecting another model starts a new conversation. This conversation stays in your list."
            : "Select another model to start a new conversation. Collie forgets this conversation.")
          : "Select a model for this conversation." })),
      el("div", { class: "collie-model-options", role: "radiogroup", "aria-label": "Agent profiles" },
        view.options.map(option => el("button", {
          class: "collie-model-option", type: "button", role: "radio", testid: `${prefix}-model-option`,
          "aria-checked": (option.id === view.current.id) ? "true" : "false", disabled: busy || disabled, data: { binding: option.id },
          onclick: () => choose(option.id),
        },
        el("span", { class: "collie-model-option-text" },
          el("span", { class: "collie-model-option-name", text: option.label }),
          el("span", { class: "collie-model-option-meta", text: [option.model ? option.name : "", option.harness].filter(Boolean).join(" · ") })),
        check()))),
      view.options.length === 0 && el("p", { class: "collie-model-empty", text: "No agent profile is enabled." }),
      busy && el("p", { role: "status", text: "Prepare the selected model…" }),
      error && el("p", { class: "collie-model-error", role: "alert", text: error }),
      error && retryBinding && el("button", { class: "btn small", type: "button", text: "Retry", disabled: busy || disabled,
        onclick: () => choose(retryBinding, true) }),
      el("div", { class: "collie-model-foot" },
        el("button", { class: "collie-model-manage", type: "button", testid: `${prefix}-model-manage`,
          text: "Manage agent profiles", onclick: () => { hide(); openProfiles(button); } })),
    ].filter(Boolean));
    menu.querySelector(`[data-binding="${CSS.escape(focused ?? "")}"]`)?.focus();
  }
  async function load(force = false) {
    if (loading) return loading;
    if (!force && (routing || Date.now() - failedAt < 30000)) return;
    loading = api("/api/routing", { maxAge: 0 })
      .then(result => { routing = result; }, err => { failedAt = Date.now(); if (routing) error = err.message; })
      .finally(() => { loading = null; paint(); });
    return loading;
  }
  async function choose(binding, retry = false) {
    if (busy || disabled || confirmation) return;
    const currentID = collieModelView(routing, session().binding).current?.id;
    if (!retry && binding === currentID) { hide(); button.focus(); return; }
    if (!retry && session().messages) {
      const current = session();
      if (!await confirmModelChange(binding)) return;
      if (busy || disabled || session().id !== current.id || session().binding !== current.binding) return;
      void show();
    }
    busy = true; error = ""; retryBinding = binding; paint();
    try {
      if (switchModel) {
        const result = await switchModel(binding, retry, routing?.routes?.find(row => row.role === "collie")?.binding);
        routing = result.routing;
        error = result.notice ?? "";
        retryBinding = null;
        onRouting?.(routing);
      } else {
        routing = await api("/api/routing", { method: "POST", body: JSON.stringify({ role: "collie", binding, expected: routing?.routes?.find(row => row.role === "collie")?.binding }) });
        onRouting?.(routing);
        const current = session();
        if (current.binding && current.binding !== binding && !await startOver(false)) {
          throw new Error("Culler could not start the new conversation. Select Retry.");
        }
        retryBinding = null;
      }
    } catch (err) { error = switchModel ? err.message : `${err.message} Collie keeps its current model.`; }
    finally { busy = false; paint(); }
    if (!error && !collieModelView(routing, session().binding).pending) { hide(); button.focus(); }
  }
  function confirmModelChange(binding) {
    hide();
    const label = collieModelView(routing, session().binding).options.find(option => option.id === binding)?.label ?? binding;
    return new Promise(resolve => {
      const cancel = el("button", { class: "btn", type: "button", text: "Cancel", onclick: () => dialog.close() });
      const dialog = el("dialog", { class: "collie-model-dialog", testid: `${prefix}-model-confirm`,
        "aria-labelledby": `${prefix}-model-confirm-title`, "aria-describedby": `${prefix}-model-confirm-description` });
      dialogFrame(dialog, { header: dialogHeader({ title: "Start a new conversation?", titleId: `${prefix}-model-confirm-title`, onClose: () => dialog.close() }),
      body: dialogBody({}, el("p", { text: `Collie will use ${label}.` }),
      el("p", { id: `${prefix}-model-confirm-description`, text: switchModel ? "Your current conversation stays in the session list."
        : "Collie will forget the current conversation. You cannot restore it." })),
      footer: dialogFooter({ class: "collie-model-dialog-actions" }, cancel,
        el("button", { class: "btn primary", type: "button", text: "Start new conversation", onclick: () => dialog.close("confirm") })) });
      confirmation = dialog;
      dialog.addEventListener("click", event => { if (event.target === dialog) dialog.close(); });
      dialog.addEventListener("close", () => {
        confirmation = null;
        dialog.remove();
        if (button.isConnected) button.focus();
        resolve(dialog.returnValue === "confirm");
      }, { once: true });
      document.body.append(dialog);
      showDialog(dialog);
      cancel.focus();
    });
  }
  async function show() {
    if (disabled) return;
    menu.hidden = false; button.setAttribute("aria-expanded", "true");
    document.addEventListener("pointerdown", outside, true);
    paint();
    (menu.querySelector("[aria-checked=true]") ?? items()[0])?.focus();
    await load(true);
    if (!menu.hidden && !menu.contains(document.activeElement)) (menu.querySelector("[aria-checked=true]") ?? items()[0])?.focus();
  }
  function hide() {
    if (menu.hidden) return;
    menu.hidden = true; button.setAttribute("aria-expanded", "false");
    document.removeEventListener("pointerdown", outside, true);
  }
  return {
    nodes: [button, menu], load, paint,
    set(result) { routing = result; paint(); },
    setDisabled(value) { disabled = value; if (value) confirmation?.close(); if (value && !busy) hide(); paint(); },
    dispose() { confirmation?.close(); hide(); },
  };
}

/** The message input of a conversation. A composer without attachments has
 * no attach control and sends text only; a pasted image adds nothing. */
export function createComposer({ prefix, inputID = `${prefix}-message`, sendID = `${prefix}-send`, formID, attachments = true }) {
  const input = el("textarea", { rows: 1, testid: inputID, id: inputID, "aria-label": "Message Collie", placeholder: PLACEHOLDER });
  let measuredWidth = 0, measuredValue = null;
  function resize() {
    const width = input.getBoundingClientRect().width;
    if (!width || (width === measuredWidth && input.value === measuredValue)) return;
    measuredWidth = width; measuredValue = input.value;
    input.style.height = "auto";
    input.style.height = `${Math.min(input.scrollHeight, 180)}px`;
  }
  const uploads = attachments ? createUploadInput(`${prefix}-uploads`) : null;
  const fileInput = uploads?.node.querySelector("input");
  if (fileInput) {
    fileInput.id = `${prefix}-files`;
    fileInput.classList.add("conversation-file");
  }
  const attach = uploads && el("label", { class: "conversation-attach", for: fileInput.id, title: "Attach files" },
    svg("svg", { viewBox: "0 0 16 16", width: 14, height: 14, "aria-hidden": "true" },
      svg("path", { d: "M10.5 4.5 5.8 9.2a1.4 1.4 0 0 0 2 2l5-5a2.8 2.8 0 0 0-4-4l-5 5a4.2 4.2 0 0 0 6 6l4.2-4.2", fill: "none", stroke: "currentColor", "stroke-width": 1.4, "stroke-linecap": "round", "stroke-linejoin": "round" })),
    el("span", { text: "Attach" }));
  const send = el("button", { class: "btn primary", type: "submit", testid: sendID, id: sendID },
    el("span", { text: "Send" }),
    svg("svg", { viewBox: "0 0 16 16", width: 14, height: 14, "aria-hidden": "true" },
      svg("path", { d: "M3 8h9.5M8.5 3.5 13 8l-4.5 4.5", fill: "none", stroke: "currentColor", "stroke-width": 1.6, "stroke-linecap": "round", "stroke-linejoin": "round" })));
  const hint = el("span", { class: "conversation-hint" },
    el("kbd", { text: "Enter" }), " sends. ", el("kbd", { text: "Shift" }), "+", el("kbd", { text: "Enter" }), " adds a line.");
  const form = el("form", { class: "composer conversation-composer", id: formID, autocomplete: "off" }, input,
    el("div", { class: "conversation-composer-row" }, attach, uploads?.node, hint, send));
  input.addEventListener("paste", event => {
    if (input.disabled || !uploads) return;
    const clipboard = event.clipboardData;
    if (!clipboard) return;
    const items = [...clipboard.items].filter(item => item.kind === "file" && item.type.startsWith("image/"));
    const files = items.length ? items.map(item => {
      const file = item.getAsFile();
      return file && !file.type ? new File([file], file.name, { type: item.type }) : file;
    }) : [...clipboard.files].filter(file => file.type.startsWith("image/"));
    if (!items.length && !files.length) return;
    event.preventDefault();
    const text = clipboard.getData("text/plain");
    if (text) { input.setRangeText(text, input.selectionStart, input.selectionEnd, "end"); resize(); }
    if (files.some(file => !file)) { uploads.reportError(); return; }
    uploads.add(files, true);
  });
  input.addEventListener("input", resize);
  input.addEventListener("keydown", event => {
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing) { event.preventDefault(); form.requestSubmit(); }
  });
  return { input, uploads, send, form, resize };
}

export function conversationEmpty({ title, description, prompts, choose }) {
  return el("div", { class: "conversation-empty" },
    el("p", { class: "conversation-empty-title", text: title }),
    el("p", { text: description }),
    el("div", { class: "conversation-prompts", role: "group", "aria-label": "Example messages" },
      prompts.map(text => el("button", { class: "conversation-prompt", type: "button", text, onclick: () => choose(text) }))));
}

export function createConversation({ api, renderMessage, projectID, onRouting, openProfiles }) {
  let sessionID = null, sessionBinding = null, hasMessages = false, active = true, busy = false, refreshBusy = false, readOnly = false, pending = false;
  // A failed send keeps its status until the next send, so a refresh cannot hide it.
  let sendFailure = null;
  const { input, uploads, send, form, resize } = createComposer({ prefix: "vision" });
  function emptyState() {
    if (readOnly) return el("div", { class: "conversation-empty" }, el("p", { text: "This conversation has no messages." }));
    return conversationEmpty({
      title: "Start with the goal",
      description: "Tell Collie what this Project must achieve. Collie can read the Project files and help you write the Vision draft.",
      prompts: EXAMPLE_PROMPTS,
      choose: text => { if (!readOnly && !pending) { input.value = text; resize(); input.focus(); } },
    });
  }
  const transcript = el("div", { class: "transcript", testid: "vision-transcript", "aria-label": "Conversation with Collie" }, emptyState());
  const status = el("p", { class: "conversation-status", role: "status", testid: "vision-collie-status", text: "Collie connects to this Project when you send a message." });
  const post = (path, body = {}) => api(path, { method: "POST", body: JSON.stringify(body) });
  async function connect() {
    const result = await post("/api/sessions", { vision_project: projectID });
    if (!active) return null;
    sessionID = result.session_id;
    if (result.binding) sessionBinding = result.binding;
    model.paint();
    return sessionID;
  }
  async function refresh() {
    if (!active || refreshBusy || busy) return;
    refreshBusy = true;
    const modelRead = model.load();
    try {
      if (!sessionID) {
        const result = await api("/api/sessions", { maxAge: 0 });
        const row = result.sessions?.filter(row => row.vision_project === projectID || row.vision_project_id === projectID).at(-1);
        sessionID = row?.id ?? null;
        sessionBinding = row?.binding ?? null;
        model.paint();
      }
      if (!sessionID || !active) return;
      const requested = sessionID;
      const history = await api(`/api/sessions/${requested}/history`, { maxAge: 0 });
      if (!active || requested !== sessionID || history.session_id !== requested) return;
      const rows = transcriptRows(history, Date.now());
      const bottom = transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight < 40;
      hasMessages = rows.length > 0;
      reconcileChildren(transcript, rows.length ? rows.map(row => renderMessage(row, sessionID)) : [emptyState()]);
      clear.hidden = readOnly || !rows.length;
      if (bottom) transcript.scrollTop = transcript.scrollHeight;
      status.textContent = readOnly ? "This Project is archived. You can read its conversation." : sendFailure ?? "Collie can read this Project and the Vision draft.";
    } catch (err) { if (active) status.textContent = `${err.message} You can continue to edit and save the Vision.`; }
    finally { await modelRead; refreshBusy = false; }
  }
  form.addEventListener("submit", async event => {
    event.preventDefault();
    const originalText = input.value;
    let uploadID;
    const body = originalText.trim();
    if (readOnly || busy || (!body && !uploads.count())) return;
    busy = true; send.disabled = true; sendFailure = null; status.textContent = "Culler prepares the conversation…";
    try {
      const files = await uploads.files();
      uploadID = uploads.requestID(body);
      const id = await connect();
      if (!id) return;
      await post(`/api/sessions/${id}/${files.length ? "upload" : "send"}`, files.length ? { body, files, request_id: uploadID } : { body });
      if (input.value === originalText) { input.value = ""; resize(); }
      if (uploads.requestID() === uploadID) uploads.clear();
      status.textContent = "Collie prepares a reply…";
    } catch (err) { if (active) status.textContent = sendFailure = `${err.message} Your message remains available. Retry Send.`; }
    finally { busy = false; send.disabled = readOnly; await refresh(); }
  });
  // Clear ends the session and starts an empty one with the current Collie route.
  async function startOver(ask) {
    if (readOnly || busy) return false;
    if (ask && hasMessages && !confirm("Clear this conversation? Collie forgets it, and you cannot restore it.")) return false;
    busy = true; clear.disabled = true;
    try {
      const result = await post("/api/sessions", { vision_project: projectID, clear: true });
      if (!active) return false;
      sessionID = result.session_id; sessionBinding = result.binding ?? null; hasMessages = false; sendFailure = null;
      transcript.replaceChildren(emptyState()); clear.hidden = true;
      status.textContent = "Collie connects to this Project when you send a message.";
      return true;
    } catch (err) { if (active) status.textContent = `${err.message} The conversation remains.`; return false; }
    finally { busy = false; clear.disabled = false; model.paint(); }
  }
  const clear = el("button", { class: "btn small conversation-clear", type: "button", testid: "vision-clear", text: "Clear", hidden: true,
    title: "Start an empty conversation. Collie forgets this conversation.", onclick: () => startOver(true) });
  const model = createModelPicker({
    api, session: () => ({ id: sessionID, binding: sessionBinding, messages: hasMessages }), startOver, onRouting,
    openProfiles: entry => openProfiles?.(entry),
  });
  return {
    node: el("section", { class: "vision-panel vision-conversation", "aria-label": "Collie" },
      el("div", { class: "vision-panel-bar" }, el("h3", { text: "Collie" }), ...model.nodes, status, clear), transcript, form),
    refresh, dispose() { active = false; model.dispose(); uploads.clear(); },
    setRouting: model.set,
    setPending(value) {
      pending = value; input.disabled = value || readOnly; send.disabled = value || readOnly || busy;
      uploads.setDisabled(value || readOnly); model.setDisabled(value || readOnly);
      for (const prompt of transcript.querySelectorAll(".conversation-prompt")) prompt.disabled = value;
    },
    setReadOnly(value) {
      readOnly = value; input.disabled = value; send.disabled = value || busy; uploads.setDisabled(value); model.setDisabled(value);
      if (value) clear.hidden = true;
      if (transcript.querySelector(".conversation-empty")) transcript.replaceChildren(emptyState());
      input.placeholder = value ? "This Project is archived." : PLACEHOLDER;
      if (value) status.textContent = "This Project is archived. You can read its conversation.";
    },
  };
}
