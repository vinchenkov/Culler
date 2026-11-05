import { el, reconcileChildren } from "./dom.js";
import { createComposer } from "./conversation.js";
import { pollTight, provisionalRow, transcriptRows } from "./homiefmt.js";

const CONTEXT = "Dashboard Board context:\n";
const REQUEST = "\n\nOperator request:\n";
const SUGGESTIONS = ["What needs my attention?", "Explain the blocked tasks.", "What changed recently?"];

export function boardMessage(text, project) {
  const context = { project_id: project?.id ?? null, project_title: project?.title ?? null };
  return `${CONTEXT}${JSON.stringify(context)}\n\nFor a new state change, describe the proposed action and ask for confirmation first.\nExplicit confirmation of your proposal permits only that change.${REQUEST}${text}`;
}

export function boardMessageText(text) {
  if (!text.startsWith(CONTEXT)) return text;
  const end = text.indexOf(REQUEST);
  return end === -1 ? text : text.slice(end + REQUEST.length);
}

export function createCommandBar({ api, renderMessage, openConversation, changed }) {
  const node = el("div", { class: "board-command-host", testid: "board-command" });
  const conversations = new Map();
  let current = null, visible = true, contextReady = false, sequence = 0;

  function conversation(project) {
    const storageKey = `mc.board-homie.${project?.id ?? "global"}`;
    let sessionID = null, busy = false, reading = false, owed = false;
    let contextAvailable = false;
    let active = false, expanded = false, timer = null, error = "", rows = [], available = false;
    let version = 0, requestState = "idle";
    try { sessionID = localStorage.getItem(storageKey); } catch {}
    if (sessionID && !/^h-[a-z0-9-]+$/.test(sessionID)) sessionID = null;
    const composer = createComposer({ prefix: `board-homie-${++sequence}` });
    composer.input.dataset.testid = "board-homie-message";
    composer.send.dataset.testid = "board-homie-send";
    let suggestionIndex = 0, suggestionTimer = null, suggestionAnimations = [];
    composer.input.placeholder = SUGGESTIONS[0];
    const suggestion = el("div", { class: "board-command-suggestion", "aria-hidden": "true", testid: "board-homie-suggestion" },
      el("span", { text: SUGGESTIONS[0] }));
    const inputWrap = el("div", { class: "board-command-input" }, composer.input, suggestion);
    function settleSuggestion() {
      for (const animation of suggestionAnimations) animation.cancel();
      suggestionAnimations = [];
      suggestion.replaceChildren(el("span", { text: SUGGESTIONS[suggestionIndex] }));
    }
    function rotateSuggestion() {
      if (document.hidden || composer.input.disabled || composer.input.value || document.activeElement === composer.input) return;
      const previous = SUGGESTIONS[suggestionIndex];
      suggestionIndex = (suggestionIndex + 1) % SUGGESTIONS.length;
      composer.input.placeholder = SUGGESTIONS[suggestionIndex];
      if (matchMedia("(prefers-reduced-motion: reduce)").matches) { settleSuggestion(); return; }
      const outgoing = el("span", { text: previous });
      const incoming = el("span", { text: SUGGESTIONS[suggestionIndex] });
      suggestion.replaceChildren(outgoing, incoming);
      const timing = { duration: 420, easing: "cubic-bezier(0.22, 1, 0.36, 1)", fill: "both" };
      suggestionAnimations = [
        outgoing.animate([{ opacity: 1, transform: "translateY(0)" }, { opacity: 0, transform: "translateY(-65%)" }], timing),
        incoming.animate([{ opacity: 0, transform: "translateY(65%)" }, { opacity: 1, transform: "translateY(0)" }], timing),
      ];
      Promise.all(suggestionAnimations.map(animation => animation.finished)).then(settleSuggestion).catch(() => {});
    }
    composer.input.addEventListener("focus", settleSuggestion);
    composer.input.addEventListener("input", settleSuggestion);
    const attach = composer.form.querySelector(".conversation-attach");
    const hint = composer.form.querySelector(".conversation-hint");
    hint.id = `board-homie-hint-${sequence}`;
    composer.input.setAttribute("aria-describedby", hint.id);
    composer.form.replaceChildren(attach, inputWrap, composer.send, composer.uploads.node, hint);
    const transcript = el("div", { class: "transcript", id: `board-homie-transcript-${sequence}`, testid: "board-homie-transcript",
      role: "log", "aria-label": "Board conversation with Homie", "aria-live": "polite", hidden: true });
    const status = el("p", { class: "conversation-status", testid: "board-homie-status", role: "status" });
    const notice = el("p", { class: "board-command-error", testid: "board-homie-error", role: "alert", hidden: true });
    const toggle = el("button", { class: "board-command-toggle", type: "button", testid: "board-homie-toggle",
      "aria-controls": transcript.id, "aria-expanded": "false", text: "Conversation", hidden: !sessionID,
      onclick: () => {
        expanded = !expanded; paint();
        if (expanded) { transcript.scrollTop = transcript.scrollHeight; void refresh(); composer.input.focus(); }
      } });
    const full = el("button", { class: "btn small", type: "button", testid: "board-homie-open", text: "Open in Homie",
      onclick: () => { if (sessionID && !busy) { menu.open = false; openConversation(sessionID); } } });
    const fresh = el("button", { class: "board-command-toggle", type: "button", testid: "board-homie-new", text: "New conversation",
      onclick: async () => { await startNewConversation(); if (active) composer.input.focus(); } });
    const retry = el("button", { class: "btn small", type: "button", testid: "board-homie-refresh", text: "Retry connection",
      hidden: true, onclick: () => {
        if (!sessionID) { void startNewConversation(); return; }
        error = ""; paint(); void refresh();
      } });
    const menu = el("details", { class: "board-command-menu" },
      el("summary", { "aria-label": "Conversation actions", title: "Conversation actions", text: "···" }),
      el("div", { class: "board-command-menu-items" }, full));
    menu.addEventListener("keydown", event => {
      if (event.key === "Escape") { menu.open = false; menu.querySelector("summary").focus(); }
    });
    menu.addEventListener("focusout", event => { if (!menu.contains(event.relatedTarget)) menu.open = false; });
    const statusRow = el("div", { class: "board-command-status" }, status, retry);
    const view = el("section", { class: "board-command conversation-page", "aria-label": "Homie command bar" },
      el("header", { class: "board-command-header" },
        el("div", { class: "board-command-title" }, el("h3", { text: "Homie" })),
        el("div", { class: "board-command-actions" }, fresh, toggle, menu)),
      transcript, statusRow, notice, composer.form);

    function persist() {
      try { if (sessionID) localStorage.setItem(storageKey, sessionID); else localStorage.removeItem(storageKey); } catch {}
    }
    function paint() {
      const disabled = !contextAvailable || !available || busy || owed;
      composer.input.disabled = disabled;
      composer.send.disabled = disabled;
      composer.uploads.setDisabled(disabled);
      toggle.hidden = !sessionID && !rows.length && !busy;
      toggle.textContent = expanded ? "Hide conversation" : "Conversation";
      toggle.setAttribute("aria-expanded", String(expanded));
      transcript.hidden = !expanded;
      full.hidden = !sessionID; full.disabled = busy;
      fresh.disabled = busy || !contextAvailable;
      retry.hidden = !error || busy;
      notice.hidden = !error; notice.textContent = error;
      menu.hidden = !sessionID;
      status.textContent = requestState === "send" ? "Your message goes to Homie…"
        : requestState === "start" ? "Culler starts a new conversation…"
        : error ? "The last request failed."
        : owed ? "Homie prepares a reply…"
        : rows.some(row => row.replyState === "failed") ? "A reply failed. Open the conversation to retry the reply."
        : "";
      statusRow.hidden = !status.textContent && retry.hidden;
    }
    const post = (path, body = {}) => api(path, { method: "POST", body: JSON.stringify(body) });
    async function startNewConversation() {
      if (busy) return;
      menu.open = false;
      clearTimeout(timer);
      version++; sessionID = null; rows = []; owed = false; error = ""; expanded = false;
      busy = true; available = false; requestState = "start"; persist(); transcript.replaceChildren(); paint();
      try {
        const result = await post("/api/sessions");
        sessionID = result.session_id; persist();
      } catch (err) { error = `A new conversation could not start. ${err.message} Your draft remains available.`; }
      finally { busy = false; available = true; requestState = "idle"; paint(); schedule(); }
    }
    function draw(follow = false) {
      const bottom = transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight < 40;
      reconcileChildren(transcript, rows.map(row => renderMessage(row.direction === "send"
        ? { ...row, body: boardMessageText(row.body) } : row, sessionID)));
      if (bottom || follow) transcript.scrollTop = transcript.scrollHeight;
    }
    function schedule() {
      clearTimeout(timer);
      if (active && !document.hidden && sessionID) timer = setTimeout(refresh, owed ? 1000 : 10000);
    }
    async function refresh() {
      if (!active || document.hidden || busy || reading || !sessionID) { schedule(); return; }
      reading = true;
      const requested = sessionID, revision = version;
      try {
        const history = await api(`/api/sessions/${requested}/history`, { maxAge: 0 });
        if (revision !== version || requested !== sessionID || busy || history.session_id !== requested) return;
        const wasOwed = owed;
        owed = pollTight(history);
        rows = transcriptRows(history, Date.now());
        available = true;
        if (error.startsWith("The conversation could not refresh.")) error = "";
        draw();
        if (wasOwed && !owed) changed();
      } catch (err) {
        if (revision !== version || requested !== sessionID) return;
        if (err.status === 400 && err.message === `unknown Homie session "${requested}"`) await startNewConversation();
        else error = `The conversation could not refresh. ${err.message}`;
      } finally { reading = false; paint(); schedule(); }
    }
    composer.form.addEventListener("submit", async event => {
      event.preventDefault();
      const text = composer.input.value.trim();
      if (composer.input.disabled || (!text && !composer.uploads.count())) return;
      busy = true; version++; error = ""; expanded = true; requestState = "send"; paint();
      const body = boardMessage(text, project);
      try {
        const files = await composer.uploads.files();
        if (!sessionID) {
          const result = await post("/api/sessions");
          sessionID = result.session_id; persist();
        }
        await post(`/api/sessions/${sessionID}/${files.length ? "upload" : "send"}`,
          files.length ? { body, files, request_id: composer.uploads.requestID(body) } : { body });
        composer.input.value = ""; composer.resize(); composer.uploads.clear();
        rows.push({ ...provisionalRow(text), id: "local-send", direction: "send", own: true, who: "You", provisional: false }, provisionalRow(""));
        owed = true; draw(true);
      } catch (err) { error = `${err.message} Your message remains available. Retry Send.`; }
      finally { busy = false; requestState = "idle"; paint(); void refresh(); }
    });
    available = !sessionID;
    paint();
    return {
      node: view,
      setContext(value, ready) { project = value; contextAvailable = ready; paint(); },
      activate(value) {
        active = value;
        if (active && !suggestionTimer) suggestionTimer = setInterval(rotateSuggestion, 4000);
        if (!active) { clearInterval(suggestionTimer); suggestionTimer = null; settleSuggestion(); }
        clearTimeout(timer);
        if (active) { composer.resize(); void refresh(); }
      },
    };
  }

  return {
    node,
    setContext(project, ready) {
      const wasUnavailable = !contextReady;
      contextReady = ready;
      node.hidden = !visible;
      const key = project?.id ?? "global";
      let next = conversations.get(key);
      if (!next) { next = conversation(project); conversations.set(key, next); }
      if (next !== current) {
        current?.activate(false);
        current = next;
        node.replaceChildren(current.node);
        current.setContext(project, ready);
        current.activate(ready && visible && !document.hidden);
      } else {
        current.setContext(project, ready);
        if (wasUnavailable || !ready) current.activate(ready && visible && !document.hidden);
      }
    },
    setVisible(value) { visible = value; node.hidden = !value; current?.activate(contextReady && !node.hidden && !document.hidden); },
    refresh() { current?.activate(contextReady && visible && !node.hidden && !document.hidden); },
  };
}
