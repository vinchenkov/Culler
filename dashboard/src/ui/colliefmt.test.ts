// colliefmt.test.ts — the Collie tab's view models. They are tested because
// the tab claims that a Discord-born conversation is listed and
// resumable from here, and because an echo row misattributed to the dashboard
// would tell the operator they said something they never said.

import { describe, expect, test } from "bun:test";

import {
  attachmentView,
  composeState,
  collieListed,
  collieModelView,
  pendingTurnBody,
  pollTight,
  replyOwed,
  tabOpenAction,
  conversationName,
  sessionLabel,
  sessionRows,
  sessionStats,
  transcriptRows,
} from "./colliefmt.js";
import { stampLabel } from "./format.js";

// 2026-08-17 14:52:00Z, the "now" every age below is measured against.
const NOW = Date.parse("2026-08-17T14:52:00Z");

const active = {
  id: "s-live",
  status: "active",
  last_activity_at: "2026-08-17 14:49:00",
  active_bindings: [{ surface: "dashboard", channel_ref: "web" }],
  known_bindings: [{ surface: "dashboard", channel_ref: "web" }],
};

// The tab's case: born on Discord, unbound since it ended, still resumable.
const discordEnded = {
  id: "s-discord",
  status: "ended",
  last_activity_at: "2026-08-17 12:52:00",
  active_bindings: [],
  known_bindings: [{ surface: "discord", channel_ref: "chan-77" }],
};

const reaped = {
  id: "s-reaped",
  status: "reaped",
  last_activity_at: "2026-08-15 14:52:00",
  active_bindings: [],
  known_bindings: [],
};

describe("sessionLabel", () => {
  test("names the session, its status and its relative age", () => {
    expect(sessionLabel(active, NOW)).toBe("New conversation (active · 3m)");
  });

  test("drops the age when the session never recorded activity", () => {
    expect(sessionLabel({ id: "s-new", status: "active" }, NOW)).toBe("New conversation (active)");
  });

  test("uses the fallback name with no clock and no row", () => {
    expect(sessionLabel(active)).toBe("New conversation (active)");
    expect(sessionLabel(null, NOW)).toBe("New conversation (unknown)");
  });
});

describe("sessionRows", () => {
  test("orders newest activity first", () => {
    const rows = sessionRows([reaped, discordEnded, active], NOW);
    expect(rows.map((r) => r.id)).toEqual(["s-live", "s-discord", "s-reaped"]);
  });

  test("sorts a session with no stamp last and keeps input order among ties", () => {
    const a = { id: "a", status: "ended" };
    const b = { id: "b", status: "ended" };
    const rows = sessionRows([a, b, active], NOW);
    expect(rows.map((r) => r.id)).toEqual(["s-live", "a", "b"]);
    expect(rows[1].activity).toBe("");
    expect(rows[1].activityAge).toBe("");
  });

  test("keeps spine order for two sessions active at the same instant", () => {
    const first = { id: "first", status: "active", last_activity_at: "2026-08-17 14:00:00" };
    const second = { id: "second", status: "active", last_activity_at: "2026-08-17 14:00:00" };
    expect(sessionRows([first, second], NOW).map((r) => r.id)).toEqual(["first", "second"]);
  });

  test("reports a Discord-born ended session as resumable from known_bindings alone", () => {
    const [row] = sessionRows([discordEnded], NOW);
    expect(row.discordBorn).toBe(true);
    expect(row.surfaces).toEqual(["discord"]);
    expect(row.bindings).toEqual([]); // unbound now — the history is what survives
    expect(row.resumable).toBe(true);
    expect(row.active).toBe(false);
    expect(row.tone).toBe("ended");
    expect(row.activity).toBe("2h ago");
    expect(row.activityAge).toBe("2h");
  });

  test("carries live bindings as surface/ref pairs and skips a surfaceless one", () => {
    const s = {
      id: "s-bound",
      status: "active",
      last_activity_at: "2026-08-17 14:52:00",
      active_bindings: [
        { surface: "discord", channel_ref: "chan-77" },
        { surface: "dashboard" },
        { channel_ref: "orphan" },
      ],
      known_bindings: [{ surface: "discord", channel_ref: "chan-77" }],
    };
    const [row] = sessionRows([s], NOW);
    expect(row.bindings).toEqual([
      { surface: "discord", ref: "chan-77" },
      { surface: "dashboard", ref: null },
    ]);
    expect(row.surfaces).toEqual(["dashboard", "discord"]);
    expect(row.active).toBe(true);
    expect(row.tone).toBe("live");
    expect(row.activity).toBe("just now");
  });

  test("tones a reaped session and an unknown status apart", () => {
    expect(sessionRows([reaped], NOW)[0].tone).toBe("reaped");
    const odd = sessionRows([{ id: "s-odd", status: "quarantined" }], NOW)[0];
    expect(odd.tone).toBe("stone");
    expect(odd.resumable).toBe(true);
  });

  test("marks only the selected session", () => {
    const rows = sessionRows([active, discordEnded], NOW, "s-discord");
    expect(rows.map((r) => r.selected)).toEqual([false, true]);
    expect(sessionRows([active], NOW).every((r) => r.selected === false)).toBe(true);
  });

  test("survives a row from an older spine that carries no binding columns", () => {
    const [row] = sessionRows([{ id: "s-old", status: "ended" }], NOW);
    expect(row.bindings).toEqual([]);
    expect(row.surfaces).toEqual([]);
    expect(row.discordBorn).toBe(false);
    expect(row.status).toBe("ended");
  });

  test("returns nothing for an empty or absent registry", () => {
    expect(sessionRows([], NOW)).toEqual([]);
    expect(sessionRows(undefined, NOW)).toEqual([]);
  });

  test("sessionRows carries the task number of a packet conversation", () => {
    const packet = { packet_id: 41, version_id: 88, task_id: 21, task_title: "Add the export command", project_id: "garden", current: true };
    const rows = sessionRows([{ ...active, id: "s-packet", packet }, { ...discordEnded, packet: null }], NOW);
    expect(rows.map((r) => [r.id, r.task])).toEqual([["s-packet", 21], ["s-discord", null]]);
  });
});

describe("collieListed", () => {
  const packet = { packet_id: 41, version_id: 88, task_id: 21, task_title: "Add the export command", project_id: "garden", current: true };

  test("collieListed hides a packet conversation without messages", () => {
    expect(collieListed({ id: "s-empty", status: "active", first_message: null, packet })).toBe(false);
    expect(collieListed({ id: "s-asked", status: "ended", first_message: "Why this test?", packet })).toBe(true);
    expect(collieListed({ id: "s-old", status: "ended", first_message: "Why this test?", packet: { ...packet, current: false } })).toBe(true);
  });

  test("lists each ordinary conversation and hides each Vision conversation", () => {
    expect(collieListed({ id: "s-new", status: "active", first_message: null, packet: null })).toBe(true);
    expect(collieListed({ id: "s-older-spine", status: "ended" })).toBe(true);
    expect(collieListed({ id: "s-vision", status: "active", first_message: "Help me", vision_project: "garden", packet: null })).toBe(false);
  });
});

describe("sessionStats", () => {
  test("counts each registry state and the Discord-born population", () => {
    expect(sessionStats([active, discordEnded, reaped])).toEqual({
      total: 3,
      active: 1,
      ended: 1,
      reaped: 1,
      discord: 1,
    });
  });

  test("zeroes out on an empty or absent registry", () => {
    expect(sessionStats([])).toEqual({ total: 0, active: 0, ended: 0, reaped: 0, discord: 0 });
    expect(sessionStats(null)).toEqual({ total: 0, active: 0, ended: 0, reaped: 0, discord: 0 });
  });
});

describe("transcriptRows", () => {
  const history = {
    session_id: "s-discord",
    status: "ended",
    messages: [
      {
        id: 1,
        seq: 1,
        direction: "send",
        surface: "discord",
        channel_ref: "chan-77",
        body: "ship it",
        attachments: [],
        created_at: "2026-08-17 14:47:00",
      },
      {
        id: 2,
        seq: 2,
        direction: "reply",
        surface: "discord",
        channel_ref: "chan-77",
        body: "on it",
        attachments: ["claim:packet/33"],
        created_at: "2026-08-17 14:52:00",
      },
      {
        id: 3,
        seq: 3,
        direction: "send",
        surface: "dashboard",
        channel_ref: null,
        body: "status?",
        attachments: [],
        created_at: "2026-08-17 14:52:00",
      },
    ],
  };

  test("attributes a non-dashboard send as the echo it is", () => {
    const [echo] = transcriptRows(history, NOW);
    expect(echo.who).toBe("Operator");
    expect(echo.viaLabel).toBe("via Discord");
    expect(echo.surface).toBe("discord");
    expect(echo.own).toBe(true);
    expect(echo.direction).toBe("send");
    expect(echo.age).toBe("5m ago");
    expect(echo.stamp).toBe(stampLabel("2026-08-17 14:47:00"));
    expect(echo.stamp).toMatch(/^Aug 1\d \d\d:\d\d$/);
  });

  test("attributes a dashboard send to the operator at this keyboard", () => {
    const row = transcriptRows(history, NOW)[2];
    expect(row.who).toBe("You");
    expect(row.viaLabel).toBe(null);
    expect(row.own).toBe(true);
  });

  test("attributes a reply to Collie and never claims it as the operator's", () => {
    const reply = transcriptRows(history, NOW)[1];
    expect(reply.who).toBe("Collie");
    expect(reply.own).toBe(false);
    expect(reply.viaLabel).toBe(null);
    expect(reply.direction).toBe("reply");
    expect(reply.age).toBe("just now");
  });

  test("passes attachment refs through untouched", () => {
    expect(transcriptRows(history, NOW)[1].attachments).toEqual(["claim:packet/33"]);
    expect(transcriptRows(history, NOW)[0].attachments).toEqual([]);
  });

  test("keeps ids and seq so the pane can key and order rows", () => {
    expect(transcriptRows(history, NOW).map((r) => r.seq)).toEqual([1, 2, 3]);
    expect(transcriptRows(history, NOW).map((r) => r.id)).toEqual([1, 2, 3]);
  });

  // A send we cannot place is the operator's own, not "via ?": naming a
  // surface we do not have would be a guess dressed as attribution.
  test("does not invent a surface for a send that carries none", () => {
    const [row] = transcriptRows({ messages: [{ id: 9, seq: 9, direction: "send" }] }, NOW);
    expect(row.who).toBe("You");
    expect(row.viaLabel).toBe(null);
    expect(row.surface).toBe(null);
    expect(row.body).toBe("");
    expect(row.attachments).toEqual([]);
    expect(row.age).toBe("");
    expect(row.stamp).toBe("");
  });

  test("returns nothing for an empty, absent or messageless history", () => {
    expect(transcriptRows({ messages: [] }, NOW)).toEqual([]);
    expect(transcriptRows(null, NOW)).toEqual([]);
    expect(transcriptRows({ session_id: "s-live" }, NOW)).toEqual([]);
  });
});

describe("replyOwed", () => {
  const inbound = (seq: number) => ({ id: seq, seq, direction: "inbound", body: "?" });
  const reply = (seq: number) => ({ id: seq, seq, direction: "reply", body: "!" });

  test("owes nothing on an empty, absent or messageless history", () => {
    expect(replyOwed({ session_id: "s-live", messages: [] })).toBe(false);
    expect(replyOwed(null)).toBe(false);
    expect(replyOwed({ session_id: "s-live" })).toBe(false);
  });

  test("owes a reply when the newest message is inbound", () => {
    expect(replyOwed({ messages: [inbound(1)] })).toBe(true);
  });

  test("owes nothing when the newest message is a reply", () => {
    expect(replyOwed({ messages: [inbound(1), reply(2)] })).toBe(false);
  });

  test("owes a reply again when a new inbound follows the last reply", () => {
    expect(replyOwed({ messages: [inbound(1), reply(2), inbound(3)] })).toBe(true);
  });
});

describe("pollTight", () => {
  const inbound = { id: 1, seq: 1, direction: "inbound", body: "?" };
  const reply = { id: 2, seq: 2, direction: "reply", body: "!" };

  test("runs tight while an active session owes a reply", () => {
    expect(pollTight({ session_id: "s-live", status: "active", messages: [inbound] })).toBe(true);
  });

  test("runs lazy on an ended session even when its newest message is inbound", () => {
    expect(pollTight({ session_id: "s-gone", status: "ended", messages: [inbound] })).toBe(false);
  });

  test("runs lazy when the active session owes nothing", () => {
    expect(pollTight({ session_id: "s-live", status: "active", messages: [inbound, reply] })).toBe(false);
    expect(pollTight({ session_id: "s-live", status: "active", messages: [] })).toBe(false);
    expect(pollTight(null)).toBe(false);
  });
});

describe("tabOpenAction", () => {
  const olderActive = { ...active, id: "s-older", last_activity_at: "2026-08-17 14:40:00" };
  // This reaped session has newer activity than the ended Discord session.
  const newerReaped = { ...reaped, last_activity_at: "2026-08-17 14:00:00" };

  test("selects the newest active session when none is open", () => {
    expect(tabOpenAction([olderActive, discordEnded, active], null, false, NOW)).toEqual({
      kind: "select",
      id: "s-live",
    });
    expect(tabOpenAction([olderActive, discordEnded, active], null, false, NOW, true)).toEqual({
      kind: "select",
      id: "s-live",
    });
    expect(tabOpenAction([active], null, false, NOW, true)).toEqual({ kind: "select", id: "s-live" });
  });

  test("keeps the open session when the list holds an active one", () => {
    expect(tabOpenAction([active], "s-live", false, NOW)).toEqual({ kind: "keep" });
    expect(tabOpenAction([active], "s-live", false, NOW, true)).toEqual({ kind: "keep" });
    expect(tabOpenAction([active, discordEnded], "s-discord", false, NOW)).toEqual({ kind: "keep" });
    expect(tabOpenAction([active, discordEnded], "s-discord", false, NOW, true)).toEqual({ kind: "resume", id: "s-discord" });
  });

  test("a tab open resumes the open session when that session ended", () => {
    expect(tabOpenAction([discordEnded, newerReaped], "s-discord", false, NOW, true)).toEqual({
      kind: "resume",
      id: "s-discord",
    });
  });

  test("a tab open with no open session resumes the session with the newest activity", () => {
    expect(tabOpenAction([discordEnded, newerReaped], null, false, NOW, true)).toEqual({
      kind: "resume",
      id: "s-reaped",
    });
    expect(tabOpenAction([reaped, discordEnded], null, false, NOW, true)).toEqual({
      kind: "resume",
      id: "s-discord",
    });
    // The open session left the list; the newest session is the target.
    expect(tabOpenAction([discordEnded], "s-gone", false, NOW, true)).toEqual({
      kind: "resume",
      id: "s-discord",
    });
  });

  test("a reaped session is a resume target", () => {
    expect(tabOpenAction([reaped], null, false, NOW, true)).toEqual({ kind: "resume", id: "s-reaped" });
    expect(tabOpenAction([reaped], "s-reaped", false, NOW, true)).toEqual({ kind: "resume", id: "s-reaped" });
  });

  test("a tab open with no sessions opens nothing", () => {
    expect(tabOpenAction([], null, false, NOW, true)).toEqual({ kind: "none" });
    expect(tabOpenAction([], "s-gone", false, NOW, true)).toEqual({ kind: "none" });
    expect(tabOpenAction(undefined, null, false, NOW, true)).toEqual({ kind: "none" });
  });

  test("a poll never starts and never resumes a session", () => {
    expect(tabOpenAction([], null, false, NOW)).toEqual({ kind: "none" });
    expect(tabOpenAction([discordEnded, reaped], null, false, NOW)).toEqual({ kind: "none" });
    expect(tabOpenAction([discordEnded], "s-discord", false, NOW)).toEqual({ kind: "keep" });
    expect(tabOpenAction([discordEnded], "s-discord", true, NOW)).toEqual({ kind: "keep" });
  });

  test("a tab open during a resume in flight does nothing more", () => {
    expect(tabOpenAction([discordEnded], "s-discord", true, NOW, true)).toEqual({ kind: "keep" });
    expect(tabOpenAction([discordEnded, reaped], null, true, NOW, true)).toEqual({ kind: "none" });
    expect(tabOpenAction([], null, true, NOW, true)).toEqual({ kind: "none" });
  });
});

describe("pendingTurnBody", () => {
  const turn = { id: 4, direction: "inbound", body: "board?", draft: "Four seeded" };

  test("shows the draft while the turn has no reply", () => {
    expect(pendingTurnBody(turn, undefined)).toEqual({ kind: "draft", body: "Four seeded" });
  });

  test("the reply replaces the draft, an attachment-only reply included", () => {
    expect(pendingTurnBody(turn, { id: 5, direction: "reply", reply_to: 4, body: "Four seeded, one packaged." }))
      .toEqual({ kind: "reply", body: "Four seeded, one packaged." });
    expect(pendingTurnBody(turn, { id: 5, direction: "reply", reply_to: 4, body: "", attachments: [{}] }).kind)
      .toBe("reply");
  });

  test("is pending, with no body, for a turn with no draft and no reply", () => {
    expect(pendingTurnBody({ ...turn, draft: null }, undefined)).toEqual({ kind: "pending", body: "" });
    expect(pendingTurnBody({ ...turn, draft: "" }, undefined).kind).toBe("pending");
    expect(pendingTurnBody(undefined, undefined).kind).toBe("pending");
  });
});

describe("the provisional draft row", () => {
  const pending = {
    status: "active",
    messages: [
      { id: 1, seq: 1, direction: "inbound", surface: "dashboard", body: "one", reply_to: null, draft: null },
      { id: 2, seq: 2, direction: "reply", surface: "collie", body: "1", reply_to: 1, draft: null },
      { id: 3, seq: 3, direction: "inbound", surface: "dashboard", body: "two?", reply_to: null,
        draft: "Two is **next**", created_at: "2026-08-17 14:51:00" },
    ],
  };

  test("sits right under the pending message, marked provisional, with no id", () => {
    const rows = transcriptRows(pending, NOW);
    expect(rows.map((r) => r.provisional)).toEqual([false, false, false, true]);
    const draft = rows[3];
    expect(draft.who).toBe("Collie");
    expect(draft.direction).toBe("reply");
    expect(draft.own).toBe(false);
    expect(draft.body).toBe("Two is **next**");
    expect(draft.id).toBe(null);
    expect(replyOwed(pending)).toBe(true);
  });

  test("leaves when the reply row lands, and the pane stops owing a reply", () => {
    const replied = {
      status: "active",
      messages: [
        ...pending.messages.map((m) => (m.id === 3 ? { ...m, draft: null } : m)),
        { id: 4, seq: 4, direction: "reply", surface: "collie", body: "Two is next.", reply_to: 3, draft: null },
      ],
    };
    const rows = transcriptRows(replied, NOW);
    expect(rows.map((r) => r.provisional)).toEqual([false, false, false, false]);
    expect(rows[3].body).toBe("Two is next.");
    expect(replyOwed(replied)).toBe(false);
  });

  test("the newest turn with no draft gets an empty provisional row at once", () => {
    const quiet = { status: "active", messages: [pending.messages[0]] };
    const rows = transcriptRows(quiet, NOW);
    expect(rows.map((r) => r.provisional)).toEqual([false, true]);
    expect(rows[1].who).toBe("Collie");
    expect(rows[1].body).toBe("");
    expect(rows[1].id).toBe(null);
    expect(replyOwed(quiet)).toBe(true);
    expect(replyOwed({ messages: [] })).toBe(false);
  });

  test("an older turn with no draft shows no row; only the newest one waits", () => {
    const two = {
      status: "active",
      messages: [
        { ...pending.messages[0] },
        { id: 3, seq: 3, direction: "inbound", surface: "dashboard", body: "two?", reply_to: null, draft: null },
      ],
    };
    expect(transcriptRows(two, NOW).map((r) => r.provisional)).toEqual([false, false, true]);
  });

  test("an empty provisional row leaves when the reply lands", () => {
    const replied = {
      status: "active",
      messages: [
        pending.messages[0],
        { id: 2, seq: 2, direction: "reply", surface: "collie", body: "1", reply_to: 1, draft: null },
      ],
    };
    expect(transcriptRows(replied, NOW).map((r) => r.provisional)).toEqual([false, false]);
  });

  test("an ended session shows no provisional row and polls lazy, draft or not", () => {
    const ended = { ...pending, status: "ended" };
    const rows = transcriptRows(ended, NOW);
    expect(rows.map((r) => r.provisional)).toEqual([false, false, false]);
    expect(pollTight(ended)).toBe(false);
    expect(pollTight(pending)).toBe(true);
  });
});

describe("attachmentView", () => {
  const sha = "b".repeat(64);
  const claim = {
    ordinal: 1,
    session_id: "h-42",
    direction: "in",
    sha256: sha,
    size: 20480,
    media_type: "image/png",
    display_name: "capture.png",
  };

  test("an allowlisted raster claim renders inline through the byte endpoint", () => {
    expect(attachmentView(claim)).toEqual({
      kind: "inline",
      url: `/api/sessions/h-42/attachments/in/${sha}/20480`,
      name: "capture.png",
      caption: "capture.png · image/png · 20.0 KB",
    });
  });

  test("parameters cannot buy an inline render; the bare type is judged", () => {
    const view = attachmentView({ ...claim, media_type: "Image/PNG; charset=binary" });
    expect(view.kind).toBe("inline");
    expect(view.caption).toContain("image/png");
  });

  test("everything off the allowlist downloads — image/svg+xml included", () => {
    expect(attachmentView({ ...claim, media_type: "image/svg+xml" }).kind).toBe("download");
    const pdf = attachmentView({ ...claim, direction: "out", media_type: "application/pdf", display_name: "notes.pdf" });
    expect(pdf).toEqual({
      kind: "download",
      url: `/api/sessions/h-42/attachments/out/${sha}/20480`,
      name: "notes.pdf",
      caption: "notes.pdf · application/pdf · 20.0 KB",
    });
  });

  test("a legacy string reference stays inert — it names no servable bytes", () => {
    expect(attachmentView("claim:packet/33")).toEqual({
      kind: "inert",
      label: "attachment claim:packet/33",
    });
  });

  test("a claim missing any address field stays inert rather than 404-linking", () => {
    expect(attachmentView({ ...claim, sha256: "not-hex" }).kind).toBe("inert");
    expect(attachmentView({ ...claim, direction: "up" }).kind).toBe("inert");
    expect(attachmentView({ ...claim, size: "20480" }).kind).toBe("inert");
    expect(attachmentView({ ...claim, session_id: undefined }).kind).toBe("inert");
  });
});

describe("composeState", () => {
  test("asks for a session before it offers a composer", () => {
    expect(composeState(null)).toEqual({
      canSend: false,
      canResume: false,
      hint: "Pick a session, or start a new one.",
    });
    expect(composeState(undefined).canSend).toBe(false);
  });

  test("lets an active session be typed into with no hint in the way", () => {
    expect(composeState(active)).toEqual({ canSend: true, canResume: false, hint: "" });
  });

  test("offers the resume instead of an error on an ended or reaped session", () => {
    const ended = {
      canSend: false,
      canResume: true,
      hint: "This conversation has ended — resume it to keep talking.",
    };
    expect(composeState(discordEnded)).toEqual(ended);
    expect(composeState(reaped)).toEqual(ended);
    expect(composeState({ id: "s-odd", status: "quarantined" })).toEqual(ended);
  });
});


test("Every failed reply remains visible after another inbound message", () => {
  const messages = [1,2].map(id => ({id,direction:"inbound",body:"Message",created_at:"2026-08-16T20:52:00Z",reply_state:"failed",attempt_id:String(id)}));
  const rows = transcriptRows({status:"ended",messages},NOW);
  const failures = rows.filter(row=>row.provisional);
  expect(failures).toHaveLength(2);
  expect(failures.map(row=>row.statusLabel)).toEqual(["Reply interrupted","Reply interrupted"]);
});

test("a retry uses its current state instead of the original message age", () => {
  for (const [reply_state, label] of [["authorized", "Reply queued"], ["active", "Reply in progress"], ["failed", "Reply interrupted"]]) {
    const history = { status: "active", messages: [{ id: 1, direction: "inbound", body: "Inspect the Project.", created_at: "2020-01-01T00:00:00Z", reply_state }] };
    expect(transcriptRows(history, NOW).find(row => row.provisional)?.statusLabel).toBe(label);
  }
});

describe("collieModelView", () => {
  const routing = {
    routes: [
      { role: "worker", harness: "claude-sdk", binding: "claude", name: "claude", model: "claude-opus-5" },
      { role: "collie", harness: "codex", binding: "chatgpt", name: "ChatGPT", model: "collie-model" },
    ],
    bindings: [
      { id: "chatgpt", name: "ChatGPT", harness: "codex", model: "collie-model" },
      { id: "claude", name: "claude", harness: "claude-sdk", model: "claude-opus-5" },
      { id: "local", name: "local", harness: "claude-sdk", model: "" },
    ],
  };

  test("a conversation without a session shows the Collie route", () => {
    const view = collieModelView(routing);
    expect(view.current).toEqual({ id: "chatgpt", name: "ChatGPT", model: "collie-model", harness: "codex", label: "collie-model" });
    expect(view.pending).toBe(false);
    expect(view.options.map((o) => [o.id, o.selected])).toEqual([["chatgpt", true], ["claude", false], ["local", false]]);
  });

  test("a session keeps its own model after the route changes", () => {
    const view = collieModelView(routing, "claude-sdk/claude");
    expect(view.current?.label).toBe("claude-opus-5");
    expect(view.next?.label).toBe("collie-model");
    expect(view.pending).toBe(true);
  });

  test("a profile without a model shows the profile name", () => {
    expect(collieModelView(routing, "claude-sdk/local").current?.label).toBe("local");
  });

  test("a session binding outside the enabled list keeps its identifier", () => {
    expect(collieModelView(routing, "codex/retired").current).toEqual({ id: "retired", name: "retired", model: "", harness: "codex", label: "retired" });
  });

  test("absent routing shows no model", () => {
    expect(collieModelView(null)).toEqual({ current: null, next: null, pending: false, options: [] });
  });
});


test("conversation names use a stored title, a bounded excerpt, or an empty state", () => {
  expect(conversationName({ title: "Choose a database", first_message: "Other text" })).toBe("Choose a database");
  expect(conversationName({ first_message: "  Compare\n database  options " })).toBe("Compare database options");
  expect(conversationName({ first_message: "界".repeat(90) })).toBe("界".repeat(39) + "…");
  expect(conversationName({ first_message: 'Yo investigate this error "Remove MiniMax and GPT-5.6 model names from tests and example configs"' })).toBe('Yo investigate this error "Remove…');
  expect(conversationName({ first_message: "One two three four five six seven" })).toBe("One two three four five six…");
  expect(conversationName({ first_message: "One two three four five six" })).toBe("One two three four five six");
  expect(conversationName({ first_message: "" })).toBe("Conversation with attachments");
  expect(conversationName({ id: "h-private", first_message: null })).toBe("New conversation");
  expect(sessionLabel({ title: "Database options", status: "active" })).toBe("Database options (active)");
});
