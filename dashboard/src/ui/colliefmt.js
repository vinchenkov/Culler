// colliefmt.js — view models for the Collie tab. The tab makes the
// conversation rows the single rendering source: the same transcript is what
// Discord sees, so a row that arrived on another surface must read here as an
// echo of that surface and not as something the dashboard typed.

import { ageLabel, relativeAge, stampLabel, utcMs } from "./format.js";

export function conversationName(session) {
  if (typeof session?.title === "string" && session.title.trim()) return session.title;
  if (session?.first_message == null) return "New conversation";
  const text = String(session.first_message).replace(/\s+/gu, " ").trim();
  if (!text) return "Conversation with attachments";
  let excerpt = text.split(" ").slice(0, 6).join(" ");
  const chars = Array.from(excerpt);
  if (chars.length > 39) {
    excerpt = chars.slice(0, 39).join("");
    const boundary = excerpt.lastIndexOf(" ");
    if (boundary > 0) excerpt = excerpt.slice(0, boundary);
  }
  return excerpt === text ? text : excerpt + "…";
}

export function sessionLabel(s, nowMs) {
  const name = conversationName(s);
  const status = s?.status ?? "unknown";
  const age = typeof nowMs === "number" ? relativeAge(s?.last_activity_at, nowMs) : "";
  return age === "" ? `${name} (${status})` : `${name} (${status} · ${age})`;
}

/** Tone per the three registry states. An unknown status is `stone`: a
 * status this build does not know is still a fact, not a crash. */
function sessionTone(status) {
  if (status === "active") return "live";
  if (status === "ended") return "ended";
  if (status === "reaped") return "reaped";
  return "stone";
}

/** Every surface a session has ever been bound to. Both binding lists count:
 * a Discord-born session that has since been unbound is still Discord-born,
 * and resuming that conversation from here is the tab's headline feature. */
function surfacesOf(session) {
  const seen = new Set();
  for (const list of [session?.active_bindings, session?.known_bindings]) {
    if (!Array.isArray(list)) continue;
    for (const b of list) {
      if (typeof b?.surface === "string" && b.surface !== "") seen.add(b.surface);
    }
  }
  return [...seen].sort();
}

/** Whether the Collie page lists a conversation. A Vision conversation stays on
 * its Vision page. A packet conversation shows after its first message. */
export function collieListed(row) {
  return !row?.vision_project && (row?.packet == null || row.first_message != null);
}

/** The session picker's rows, newest activity first. A packet conversation
 * carries the task number of its packet. */
export function sessionRows(sessions, nowMs, selectedID = null) {
  const rows = Array.isArray(sessions) ? sessions : [];
  return rows
    .map((s, index) => {
      const surfaces = surfacesOf(s);
      const bindings = (Array.isArray(s?.active_bindings) ? s.active_bindings : [])
        .filter((b) => typeof b?.surface === "string" && b.surface !== "")
        .map((b) => ({ surface: b.surface, ref: b.channel_ref ?? null }));
      return {
        row: {
          id: s?.id ?? null,
          name: conversationName(s),
          status: s?.status ?? "unknown",
          tone: sessionTone(s?.status),
          activity: ageLabel(s?.last_activity_at, nowMs),
          activityAge: relativeAge(s?.last_activity_at, nowMs),
          task: s?.packet?.task_id ?? null,
          bindings,
          surfaces,
          discordBorn: surfaces.includes("discord"),
          resumable: s?.status !== "active",
          active: s?.status === "active",
          selected: s?.id === selectedID,
        },
        index,
        ms: utcMs(s?.last_activity_at),
      };
    })
    .sort((a, b) => {
      // A session with no stamp sorts last rather than as epoch-old: absent is
      // unknown, not ancient. Ties keep the spine's own order.
      const aMissing = Number.isNaN(a.ms);
      const bMissing = Number.isNaN(b.ms);
      if (aMissing !== bMissing) return aMissing ? 1 : -1;
      if (!aMissing && a.ms !== b.ms) return b.ms - a.ms;
      return a.index - b.index;
    })
    .map((entry) => entry.row);
}

/** Counts for the pane header. `discord` counts sessions Discord ever bound,
 * because that is the population the resume affordance serves. */
export function sessionStats(sessions) {
  const rows = Array.isArray(sessions) ? sessions : [];
  const stats = { total: rows.length, active: 0, ended: 0, reaped: 0, discord: 0 };
  for (const s of rows) {
    if (s?.status === "active") stats.active += 1;
    else if (s?.status === "ended") stats.ended += 1;
    else if (s?.status === "reaped") stats.reaped += 1;
    if (surfacesOf(s).includes("discord")) stats.discord += 1;
  }
  return stats;
}

function capitalise(surface) {
  return surface.charAt(0).toUpperCase() + surface.slice(1);
}

/** Which body a turn shows under the operator's message: the reply once it
 * exists, else the runner's provisional draft, else nothing yet (`pending`).
 * The reply wins even when it is attachment-only, because it is the turn's
 * completed answer. */
export function pendingTurnBody(inbound, reply) {
  if (reply !== null && reply !== undefined && typeof reply === "object") {
    return { kind: "reply", body: typeof reply.body === "string" ? reply.body : "" };
  }
  const draft = typeof inbound?.draft === "string" ? inbound.draft : "";
  if (draft !== "") return { kind: "draft", body: draft };
  return { kind: "pending", body: "" };
}

/** The provisional Collie row under a turn that has no reply yet. A `pending`
 * row has no text: the pane shows it as Collie at work on the turn. */
export function provisionalRow(body) {
  return {
    id: null,
    seq: null,
    direction: "reply",
    who: "Collie",
    viaLabel: null,
    surface: null,
    body,
    attachments: [],
    age: "",
    stamp: "",
    own: false,
    provisional: true,
  };
}

function historyMessages(history) {
  const messages = Array.isArray(history) ? history : (history?.messages ?? []);
  return Array.isArray(messages) ? messages : [];
}

/** The reply row of each inbound turn, keyed by the turn's id. */
function repliesByTurn(messages) {
  const replies = new Map();
  for (const m of messages) {
    if (m?.direction === "reply" && m?.reply_to !== null && m?.reply_to !== undefined) {
      replies.set(m.reply_to, m);
    }
  }
  return replies;
}

/** The transcript's rows. Conversation rows carry no `kind`: a `send` whose
 * surface is not `dashboard` IS the echo of a message that arrived elsewhere,
 * and the transcript attributes it ("via Discord"). A send with no surface at
 * all is attributed to the operator here rather than to a surface we cannot
 * name — "via ?" would be a guess dressed as a fact.
 *
 * In an active session, an inbound turn with a draft and no reply yet gets one
 * provisional Collie row right under it. The newest message, when it is an
 * inbound turn with no reply and no draft, gets an empty provisional row: the
 * operator sees at once that Collie holds the turn. A provisional row has no
 * id: it is not a conversation row, and it leaves when the reply row arrives.
 * An ended session owes no reply, so its transcript shows no provisional row. */
export function transcriptRows(history, nowMs) {
  const messages = historyMessages(history);
  const replies = repliesByTurn(messages);
  const drafting = history?.status === "active";
  const newest = messages[messages.length - 1];
  const rows = [];
  for (const m of messages) {
    const direction = m?.direction === "reply" ? "reply" : "send";
    const surface = typeof m?.surface === "string" && m.surface !== "" ? m.surface : null;
    const echoed = direction === "send" && surface !== null && surface !== "dashboard";
    rows.push({
      id: m?.id ?? null,
      seq: m?.seq ?? null,
      direction,
      who: direction === "reply" ? "Collie" : echoed ? "Operator" : "You",
      viaLabel: echoed ? `via ${capitalise(surface)}` : null,
      surface,
      body: typeof m?.body === "string" ? m.body : "",
      attachments: Array.isArray(m?.attachments) ? m.attachments : [],
      age: ageLabel(m?.created_at, nowMs),
      stamp: stampLabel(m?.created_at),
      own: direction !== "reply",
      provisional: false,
    });
    if (direction !== "send" || (!drafting && m?.reply_state !== "failed")) continue;
    const pending = pendingTurnBody(m, replies.get(m?.id));
    if (pending.kind === "draft" || (pending.kind === "pending" && (m === newest || m?.reply_state === "failed"))) {
      const row = provisionalRow(pending.body);
      row.replyState = m?.reply_state ?? "unknown";
      row.messageID = m?.id;
      row.attemptID = m?.attempt_id;
      const age = nowMs - utcMs(m?.created_at);
      row.statusLabel = row.replyState === "failed" ? "Reply interrupted"
        : row.replyState === "authorized" ? "Reply queued"
        : row.replyState === "active" ? "Reply in progress"
        : age >= 120_000 ? "Reply delayed" : "No reply yet";
      rows.push(row);
    }
  }
  return rows;
}

/** True when the newest message is an inbound turn: it has no reply after it,
 * so Collie owes one. The history is in send order, newest last. */
export function replyOwed(history) {
  const messages = Array.isArray(history) ? history : (history?.messages ?? []);
  if (!Array.isArray(messages) || messages.length === 0) return false;
  return messages[messages.length - 1]?.direction !== "reply";
}

/** True while the poll loop must run tight: the open session is active and
 * owes a reply. An ended session owes nothing the loop can wait for. */
export function pollTight(history) {
  return history?.status === "active" && replyOwed(history);
}

/** A page open prepares the selected or newest conversation.
 * A periodic refresh preserves the selection without a resume. */
export function tabOpenAction(sessions, selectedID, resumeInFlight, nowMs, opened = false) {
  const rows = sessionRows(sessions, nowMs).filter(row => row.id !== null);
  const selected = rows.find(row => row.id === selectedID);
  if (!opened || resumeInFlight) {
    if (selected) return { kind: "keep" };
    const active = rows.find(row => row.active);
    return active ? { kind: "select", id: active.id } : { kind: "none" };
  }
  const last = selected ?? rows[0];
  if (!last) return { kind: "none" };
  if (!last.active) return { kind: "resume", id: last.id };
  return selected ? { kind: "keep" } : { kind: "select", id: last.id };
}

/** The media types the byte endpoint will serve inline, mirroring
 * `mc __attachment-bytes`'s allowlist: safe raster images only, keyed on the
 * bare lowercased type so parameters cannot buy an inline render, and
 * image/svg+xml (scriptable) deliberately absent. The verb stays the
 * authority on the response; this only picks the chip's shape. */
const INLINE_MEDIA_TYPES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);

function byteLabel(size) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

/** One attachment reference's render decision. A typed claim carries its
 * own complete byte address — session_id, direction, sha256,
 * size, exactly the fields `mc collie history` returned — which becomes the
 * byte endpoint's URL verbatim; nothing is derived. A legacy string reference
 * (pre-typed transcripts), or a claim missing any address field, names no
 * servable bytes and stays the inert reference it always was. */
export function attachmentView(ref) {
  if (ref === null || typeof ref !== "object") {
    return { kind: "inert", label: `attachment ${ref}` };
  }
  const sessionID = typeof ref.session_id === "string" ? ref.session_id : "";
  const direction = ref.direction === "in" || ref.direction === "out" ? ref.direction : "";
  const sha256 = typeof ref.sha256 === "string" && /^[0-9a-f]{64}$/.test(ref.sha256) ? ref.sha256 : "";
  const size = typeof ref.size === "number" && Number.isInteger(ref.size) && ref.size >= 0 ? ref.size : null;
  if (sessionID === "" || direction === "" || sha256 === "" || size === null) {
    return { kind: "inert", label: `attachment ${ref.display_name ?? ref.sha256 ?? "(malformed)"}` };
  }
  const mediaType = typeof ref.media_type === "string" ? ref.media_type : "";
  const bare = mediaType.split(";")[0].trim().toLowerCase();
  const name =
    typeof ref.display_name === "string" && ref.display_name !== "" ? ref.display_name : "attachment";
  return {
    kind: INLINE_MEDIA_TYPES.has(bare) ? "inline" : "download",
    url: `/api/sessions/${encodeURIComponent(sessionID)}/attachments/${direction}/${sha256}/${size}`,
    name,
    caption: `${name} · ${bare === "" ? "unknown type" : bare} · ${byteLabel(size)}`,
  };
}

/** What the composer may do against the selected session. Sending into an
 * ended session must offer the resume, not an error: the conversation is
 * recoverable and the tab makes that recovery the point. */
export function composeState(session) {
  if (!session) {
    return { canSend: false, canResume: false, hint: "Pick a session, or start a new one." };
  }
  if (session.status === "active") {
    return { canSend: true, canResume: false, hint: "" };
  }
  return {
    canSend: false,
    canResume: true,
    hint: "This conversation has ended — resume it to keep talking.",
  };
}

/** The model behind one Collie conversation. A session keeps the agent
 * profile it started with, so `current` follows the session and `next`
 * follows the Collie route that each new conversation uses. A session records
 * its binding as `<harness>/<binding>`. */
export function collieModelView(routing, sessionBinding = null) {
  const bindings = (Array.isArray(routing?.bindings) ? routing.bindings : [])
    .filter((b) => typeof b?.id === "string" && b.id !== "");
  const route = (routing?.routes ?? []).find((r) => r?.role === "collie") ?? null;
  const profile = (id) => {
    const b = bindings.find((row) => row.id === id) ?? (route?.binding === id ? route : {});
    const name = b.name || id;
    return { id, name, model: b.model || "", harness: b.harness || "", label: b.model || name };
  };
  const next = route?.binding ? profile(route.binding) : null;
  const slash = sessionBinding ? sessionBinding.indexOf("/") : -1;
  const session = sessionBinding
    ? { ...profile(sessionBinding.slice(slash + 1)), ...(slash > 0 ? { harness: sessionBinding.slice(0, slash) } : {}) }
    : null;
  const current = session ?? next;
  return {
    current,
    next,
    pending: Boolean(current && next && current.id !== next.id),
    options: bindings.map((b) => ({ ...profile(b.id), selected: b.id === next?.id })),
  };
}
