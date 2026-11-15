// packetchat.js — the quick chat of the Review page: one Collie conversation
// about the open packet. The spine records the packet of each conversation,
// so `mc collie list` names the current conversation of each packet. The page
// keeps only the unsent draft of each packet, in memory.

import { el, reconcileChildren } from "./dom.js";
import { createComposer, createModelPicker } from "./conversation.js";
import { collieModelView, pollTight, provisionalRow, transcriptRows } from "./colliefmt.js";

/** The text of an empty conversation. It names the opening text of the
 * packet by the heading that the origin of its task selects. */
export function emptyText(packet) {
  const opening = packet?.origin === "autonomous" ? "Strategic rationale" : "thesis";
  return `Ask Collie about this packet. Collie receives its ${opening}, its criteria, and its report text.`;
}
// mc gives the same text when it refuses a conversation of an archived Project.
const ARCHIVED_TEXT = "The Project of this packet is archived. Conversations about its packets are unavailable.";

// The widths of the wide layout while the chat column shows, in px.
// style.css uses the same values.
const QUEUE_WIDTH = 200;
const CHAT_WIDTH = 400;
const CHAT_MIN = 320;
const PANE_GAP = 12;
const READER_MIN = 640;
const READER_RESIZE_MIN = 400;
const CHAT_WIDTH_KEY = "mc.packet-collie-width";

/** The current conversation of one packet from the Collie list: the newest
 * conversation of the packet, when the operator did not delete it. */
export function currentConversation(sessions, packetID) {
  return (Array.isArray(sessions) ? sessions : [])
    .find(row => row?.packet?.packet_id === packetID && row.packet.current === true) ?? null;
}

/** The current conversation of the earlier packet, when it holds a message.
 * The earlier packet is the packet of the same task that came directly before
 * the open packet. The packet row names it. */
export function earlierConversation(sessions, previousPacketID) {
  if (previousPacketID == null) return null;
  const found = currentConversation(sessions, previousPacketID);
  return found?.first_message != null ? found : null;
}

/** Whether the queue column hides while the chat column shows. `width` is
 * the content width of the Review panel. The queue column hides when the
 * reader beside it would be narrower than 640 px. */
export function queueHidden(width, chatWidth = CHAT_WIDTH) {
  return width - QUEUE_WIDTH - chatWidth - 2 * PANE_GAP < READER_MIN;
}

/** The divider changes the chat width and preserves the selection across page loads. */
export function createPacketChatResize({ pane, column, handle }) {
  let preferredWidth = CHAT_WIDTH;
  try {
    const stored = Number(localStorage.getItem(CHAT_WIDTH_KEY));
    if (Number.isFinite(stored) && stored >= CHAT_MIN) preferredWidth = stored;
  } catch { /* The default width applies when browser storage is unavailable. */ }
  let width = CHAT_WIDTH, maximum = CHAT_WIDTH, drag = null;

  function save() {
    try { localStorage.setItem(CHAT_WIDTH_KEY, String(preferredWidth)); } catch { /* The width lasts until the page loads again. */ }
  }

  function finish() {
    if (!drag) return;
    const { pointerID } = drag;
    drag = null;
    document.documentElement.classList.remove("packet-chat-resize-active");
    if (handle.hasPointerCapture(pointerID)) handle.releasePointerCapture(pointerID);
    save();
  }

  function update() {
    if (handle.hidden || pane.clientWidth === 0) {
      finish();
      delete pane.dataset.queue;
      return;
    }
    const style = getComputedStyle(pane);
    const available = pane.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
    maximum = Math.max(CHAT_MIN, available - PANE_GAP - READER_RESIZE_MIN);
    width = Math.max(CHAT_MIN, Math.min(preferredWidth, maximum));
    column.style.setProperty("--packet-chat-width", `${width}px`);
    pane.dataset.queue = queueHidden(available, width) ? "hidden" : "compact";
    handle.setAttribute("aria-valuemin", String(CHAT_MIN));
    handle.setAttribute("aria-valuemax", String(Math.round(maximum)));
    handle.setAttribute("aria-valuenow", String(Math.round(width)));
    handle.setAttribute("aria-valuetext", `${Math.round(width)} pixels`);
  }

  function select(value) {
    preferredWidth = Math.max(CHAT_MIN, Math.min(value, maximum));
    update();
  }

  handle.addEventListener("pointerdown", event => {
    if (event.button !== 0 || !event.isPrimary || handle.hidden) return;
    event.preventDefault();
    handle.focus({ preventScroll: true });
    drag = { pointerID: event.pointerId, x: event.clientX, width };
    handle.setPointerCapture(event.pointerId);
    document.documentElement.classList.add("packet-chat-resize-active");
  });
  handle.addEventListener("pointermove", event => {
    if (drag?.pointerID !== event.pointerId) return;
    select(drag.width + drag.x - event.clientX);
  });
  for (const type of ["pointerup", "pointercancel", "lostpointercapture"]) {
    handle.addEventListener(type, event => { if (drag?.pointerID === event.pointerId) finish(); });
  }
  window.addEventListener("blur", finish);
  handle.addEventListener("keydown", event => {
    let value;
    switch (event.key) {
      case "ArrowLeft": value = width + 24; break;
      case "ArrowRight": value = width - 24; break;
      case "Home": value = CHAT_MIN; break;
      case "End": value = maximum; break;
      default: return;
    }
    event.preventDefault();
    select(value);
    save();
  });
  handle.addEventListener("dblclick", () => { select(CHAT_WIDTH); save(); });
  new ResizeObserver(update).observe(pane);
  return { update };
}

/** The header text of the quick chat, in the format of the reader title. */
export function chatTitle(taskID, title) {
  return title ? `Task #${taskID} · ${title}` : `Task #${taskID}`;
}

export function createPacketChat({ api, renderMessage, openConversation, projectArchived, changed, close, onRouting, openProfiles }) {
  const drafts = new Map();
  const failures = new Map();
  // The sent rows of each packet stay until the history holds the message.
  const sent = new Map();
  const sending = new Set();
  let packet = null, open = false, version = 0, timer = null, reading = false, again = false;
  let current = null, earlier = null, history = null, rows = [], read = false, owed = false;
  let readError = "", preparing = null, clearing = false;

  const composer = createComposer({ prefix: "packet-chat", attachments: false });
  composer.input.placeholder = "Ask about this packet";
  const title = el("span", { class: "packet-chat-title", testid: "packet-chat-title" });
  const openCollie = el("button", { class: "btn small", type: "button", testid: "packet-chat-open-collie", text: "Open in Collie",
    onclick: () => { if (hasMessages()) { menu.open = false; openConversation(current.id); } } });
  const fresh = el("button", { class: "btn small", type: "button", testid: "packet-chat-new", text: "New conversation",
    onclick: () => startNewConversation() });
  const menu = el("details", { class: "board-command-menu packet-chat-menu", testid: "packet-chat-menu" },
    el("summary", { testid: "packet-chat-menu-button", "aria-label": "Conversation actions", title: "Conversation actions", text: "···" }),
    el("div", { class: "board-command-menu-items" }, openCollie, fresh));
  menu.addEventListener("focusout", event => { if (!menu.contains(event.relatedTarget)) menu.open = false; });
  // A model selection sets the Collie route. A current conversation with
  // another model then gives its place to a new conversation of the packet.
  const model = createModelPicker({
    api, prefix: "packet-chat", onRouting, openProfiles,
    session: () => ({ id: current?.id ?? null, binding: current?.binding ?? null, messages: hasMessages() || sending.has(packet?.packet_id) }),
    switchModel: async (binding, retry, expected) => {
      let routing;
      try { routing = await post("/api/routing", { role: "collie", binding, expected }); }
      catch (err) { throw new Error(`${err.message} Collie keeps its current model.`); }
      if (current !== null && collieModelView(routing, current.binding).current?.id !== binding && !await startNewConversation()) {
        throw new Error("New conversations use the selected model. Culler could not start a new conversation for this packet. Select Retry.");
      }
      return { routing, notice: "" };
    },
  });
  const closer = el("button", { class: "packet-chat-close", type: "button", testid: "packet-chat-close",
    "aria-label": "Close Collie", text: "Close", onclick: () => close() });
  const transcript = el("div", { class: "transcript", testid: "packet-chat-transcript", role: "log", "aria-live": "polite",
    "aria-label": "Conversation with Collie about this packet" });
  const status = el("p", { class: "conversation-status packet-chat-status", testid: "packet-chat-status", role: "status", hidden: true });
  const error = el("p", { class: "packet-chat-error", testid: "packet-chat-error", role: "alert", hidden: true });
  const node = el("section", { class: "packet-chat conversation-page", testid: "packet-chat" },
    el("header", { class: "packet-chat-header" }, el("h3", { text: "Collie" }), ...model.nodes, menu, closer, title),
    transcript, status, error, composer.form);

  const post = (path, body) => api(path, { method: "POST", body: JSON.stringify(body) });
  const hasMessages = () => current !== null && current.first_message != null;
  // The resident mounts no archived Project, so its packets accept no message.
  const archived = () => packet !== null && projectArchived(packet.project) === true;

  function emptyState() {
    if (archived()) return el("div", { class: "conversation-empty packet-chat-empty", testid: "packet-chat-empty" }, el("p", { text: ARCHIVED_TEXT }));
    return el("div", { class: "conversation-empty packet-chat-empty", testid: "packet-chat-empty" },
      el("p", { text: emptyText(packet) }),
      earlier && el("a", { class: "packet-chat-earlier", testid: "packet-chat-earlier", href: "#collie",
        text: "Continue the conversation about the earlier packet",
        onclick: event => { event.preventDefault(); openConversation(earlier.id); } }));
  }

  function paint() {
    const id = packet?.packet_id;
    const heading = packet ? chatTitle(packet.task_id, packet.title || current?.packet?.task_title || "") : "";
    title.textContent = heading;
    title.title = heading;
    const local = sent.get(id)?.rows ?? [];
    const shown = local.length ? [...(read ? rows : []), ...local] : rows;
    const follow = transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight < 40;
    if (!packet) transcript.replaceChildren();
    else if (!read && local.length === 0) transcript.replaceChildren(el("div", { class: "loading", role: "status", text: "Culler reads the conversation…" }));
    else reconcileChildren(transcript, shown.length ? shown.map(row => renderMessage(row, current?.id ?? "local")) : [emptyState()]);
    if (follow || local.length) transcript.scrollTop = transcript.scrollHeight;
    const closed = archived();
    openCollie.disabled = !hasMessages();
    fresh.disabled = !hasMessages() || clearing || sending.has(id) || closed;
    model.setDisabled(!packet || !read || closed || clearing || sending.has(id) || owed || preparing?.packetID === id);
    composer.input.disabled = closed;
    composer.send.disabled = !packet || sending.has(id) || clearing || closed;
    status.textContent = sending.has(id) ? "Your message goes to Collie…"
      : clearing ? "Culler starts a new conversation…"
        : preparing?.packetID === id ? "Culler prepares the conversation…"
          : owed ? "Collie prepares a reply…"
            : closed && shown.length ? ARCHIVED_TEXT : "";
    status.hidden = status.textContent === "";
    error.textContent = failures.get(id) ?? readError;
    error.hidden = error.textContent === "";
  }

  function schedule() {
    clearTimeout(timer);
    if (open && packet && !document.hidden) timer = setTimeout(refresh, owed ? 1000 : 10000);
  }

  /** Reads the current conversation of the open packet and its history. A
   * closed quick chat reads nothing. */
  async function refresh() {
    clearTimeout(timer);
    if (!open || !packet || document.hidden) return;
    if (reading) { again = true; return; }
    reading = true;
    const revision = version, target = packet;
    try {
      const list = await api("/api/sessions", { maxAge: 0 });
      if (revision !== version) return;
      const sessions = list.sessions ?? [];
      const found = currentConversation(sessions, target.packet_id);
      earlier = earlierConversation(sessions, target.previous_packet_id);
      let next = null;
      if (found) {
        next = await api(`/api/sessions/${found.id}/history`, { maxAge: 0 });
        if (revision !== version || next.session_id !== found.id) return;
      }
      current = found;
      history = next;
      read = true;
      readError = "";
      const local = sent.get(target.packet_id);
      if (local?.messageID != null && (next?.messages ?? []).some(message => message?.id === local.messageID)) {
        sent.delete(target.packet_id);
      }
      void model.load();
      const wasOwed = owed;
      owed = pollTight(history);
      rows = transcriptRows(history, Date.now());
      if (wasOwed && !owed) changed();
    } catch (err) {
      if (revision === version) readError = `The conversation could not refresh. ${err.message}`;
    } finally {
      reading = false;
      if (revision === version) paint();
      if (again) { again = false; void refresh(); } else schedule();
    }
  }

  /** Prepares the current conversation of a packet: the active conversation,
   * the ended conversation resumed with its history, or a new conversation.
   * A preparation in flight for the same packet serves each later request. */
  function prepare(target) {
    if (preparing?.packetID === target.packet_id) return preparing.promise;
    const entry = { packetID: target.packet_id, promise: null };
    entry.promise = post("/api/sessions", { packet_id: target.packet_id, version_id: target.version_id })
      .then(result => {
        if (packet?.packet_id === target.packet_id) noteSession(result);
        return result.session_id;
      })
      .finally(() => { if (preparing === entry) preparing = null; });
    preparing = entry;
    return entry.promise;
  }

  function noteSession(result) {
    if (current?.id !== result.session_id) {
      current = { id: result.session_id, first_message: null, packet: { ...packet, current: true } };
      history = null;
      rows = [];
    }
    current = { ...current, status: result.status ?? "active", binding: result.binding ?? current.binding };
  }

  /** The packet that holds the draft text and the failure of a packet. The
   * reader shows one packet of each task, so a newer packet of the same task
   * takes them from the packet that it replaces. */
  const holder = target => (packet !== null && packet.task_id === target.task_id ? packet : target);

  function fail(target, err, suffix = "") {
    failures.set(holder(target).packet_id, `${err.message}${suffix}`);
    if (err.status === 409) changed();
  }

  composer.input.addEventListener("focusin", () => {
    const target = packet;
    if (!target || archived() || preparing?.packetID === target.packet_id || current?.status === "active") return;
    failures.delete(target.packet_id);
    prepare(target).then(() => { failures.delete(target.packet_id); }, err => fail(target, err))
      .finally(() => { if (holder(target) === packet) paint(); });
    paint();
  });

  composer.form.addEventListener("submit", async event => {
    event.preventDefault();
    const target = packet;
    const text = composer.input.value.trim();
    if (!target || !text || archived() || sending.has(target.packet_id) || clearing) return;
    composer.input.value = "";
    composer.resize();
    failures.delete(target.packet_id);
    sending.add(target.packet_id);
    const local = { messageID: null, rows: [
      { ...provisionalRow(text), id: "local-send", direction: "send", who: "You", own: true, provisional: false, age: "just now" },
      provisionalRow(""),
    ] };
    sent.set(target.packet_id, local);
    paint();
    try {
      const sessionID = await prepare(target);
      const result = await post(`/api/sessions/${sessionID}/send`, { body: text });
      local.messageID = result.message_id ?? null;
      if (local.messageID === null) sent.delete(target.packet_id);
      if (packet?.packet_id === target.packet_id) owed = true;
    } catch (err) {
      sent.delete(target.packet_id);
      restoreDraft(target, text);
      fail(target, err, " Your message remains available. Retry Send.");
    } finally {
      sending.delete(target.packet_id);
      if (holder(target) === packet) paint();
      void refresh();
    }
  });

  /** Puts the text of a failed send back into the draft of its packet, before
   * any text that the operator typed after the send. */
  function restoreDraft(target, text) {
    const join = (later) => (later ? `${text}\n${later}` : text);
    const home = holder(target);
    if (home === packet) {
      composer.input.value = join(composer.input.value);
      composer.resize();
    } else drafts.set(home.packet_id, join(drafts.get(home.packet_id) ?? ""));
  }

  /** Ends the current conversation of the open packet and starts an empty
   * conversation with the current Collie route. Returns whether it started. */
  async function startNewConversation() {
    const target = packet;
    if (!target || archived() || clearing || sending.has(target.packet_id)) return false;
    menu.open = false;
    clearing = true;
    failures.delete(target.packet_id);
    paint();
    try {
      const result = await post("/api/sessions", { packet_id: target.packet_id, version_id: target.version_id, clear: true });
      sent.delete(target.packet_id);
      if (packet?.packet_id === target.packet_id) noteSession(result);
      return true;
    } catch (err) {
      fail(target, err);
      return false;
    } finally {
      clearing = false;
      if (packet?.packet_id === target.packet_id) {
        paint();
        composer.input.focus();
      }
      void refresh();
    }
  }

  document.addEventListener("visibilitychange", () => {
    if (document.hidden) clearTimeout(timer);
    else void refresh();
  });

  return {
    node,
    /** Shows the conversation of another packet, or repaints the same packet.
     * A move to another packet keeps the draft of each packet and prepares
     * nothing. A newer packet of the same task keeps the composer text and
     * the failure of the packet that it replaces. */
    setPacket(row) {
      const next = row?.packet_id ? {
        packet_id: row.packet_id, version_id: row.version_id, task_id: row.task_id, title: row.title ?? "",
        previous_packet_id: row.previous_packet_id ?? null, project: row.project,
      } : null;
      if (next?.packet_id === packet?.packet_id) {
        packet = next;
        paint();
        return;
      }
      const replaced = packet !== null && next !== null && next.task_id === packet.task_id;
      if (replaced) {
        const failure = failures.get(packet.packet_id);
        failures.delete(packet.packet_id);
        if (failure !== undefined) failures.set(next.packet_id, failure);
      } else if (packet) {
        if (composer.input.value) drafts.set(packet.packet_id, composer.input.value);
        else drafts.delete(packet.packet_id);
      }
      packet = next;
      version++;
      current = null; earlier = null; history = null; rows = []; read = false; owed = false; readError = "";
      if (!replaced) composer.input.value = next ? drafts.get(next.packet_id) ?? "" : "";
      composer.resize();
      paint();
      void refresh();
    },
    /** Starts or stops the reads of a visible quick chat. */
    setOpen(value) {
      if (open === value) return;
      open = value;
      if (open) {
        composer.resize();
        void refresh();
      } else clearTimeout(timer);
    },
    /** Moves the keyboard focus to the composer. */
    focus() { composer.input.focus(); },
    /** Whether the open quick chat shows one conversation. */
    shows(sessionID) { return open && current !== null && current.id === sessionID; },
    /** Shows a Collie route that another control changed. */
    setRouting(result) { model.set(result); },
    /** Reads the conversation again, for example after a reply retry. */
    refresh() { void refresh(); },
    /** Restores the transcript position after a move to another host. */
    moved() {
      transcript.scrollTop = transcript.scrollHeight;
      composer.resize();
    },
  };
}
