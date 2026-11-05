import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  authorizedDiscordMessage,
  DiscordGatewayState,
  DiscordTypingLoop,
  drainDiscordOutbox,
  nativeDiscordFilePlane,
  routeDiscordFreeText,
  startDiscordSurface,
  nativeDiscordThreads,
  TYPING_DEADLINE_MS,
  TYPING_INTERVAL_MS,
  type DiscordInboundMessage,
  type DiscordPolicy,
  type DiscordTypingTimers,
} from "./discord";
import type { Exec } from "./types";

const policy: DiscordPolicy = {
  enabled: true, bot_token: "secret", guild_id: "1", channel_id: "2", operator_ids: ["3"],
};
const message: DiscordInboundMessage = {
  message_id: "100", author_id: "3", guild_id: "1", channel_id: "20", parent_channel_id: "2", content: "hello",
};

function queued(results: Array<{ exitCode?: number; stdout?: string; stderr?: string }>, calls: string[][]): Exec {
  return async (argv) => {
    calls.push(argv);
    const next = results.shift();
    if (next === undefined) throw new Error(`unexpected mc invocation ${argv.join(" ")}`);
    return { exitCode: next.exitCode ?? 0, stdout: next.stdout ?? "{}", stderr: next.stderr ?? "" };
  };
}

/** Timers the test advances by hand; due callbacks run in order. */
function fakeTimers(): DiscordTypingTimers & { advance: (ms: number) => void } {
  let t = 0;
  let next = 1;
  const pending: { at: number; id: number; fn: () => void }[] = [];
  return {
    now: () => t,
    setTimeout: (fn, ms) => {
      const id = next++;
      pending.push({ at: t + ms, id, fn });
      return id;
    },
    clearTimeout: (handle) => {
      const index = pending.findIndex((p) => p.id === handle);
      if (index >= 0) pending.splice(index, 1);
    },
    advance: (ms) => {
      const until = t + ms;
      for (;;) {
        pending.sort((a, b) => a.at - b.at || a.id - b.id);
        const due = pending[0];
        if (due === undefined || due.at > until) break;
        pending.shift();
        t = due.at;
        due.fn();
      }
      t = until;
    },
  };
}

describe("Discord typing indicator", () => {
  const loopFor = (fail = false) => {
    const typed: string[] = [];
    const warnings: string[] = [];
    const timers = fakeTimers();
    const loop = new DiscordTypingLoop(
      { triggerTyping: async (channel) => { typed.push(channel); if (fail) throw new Error("HTTP 429"); } },
      { warn: (_event, msg) => { warnings.push(msg); } },
      timers,
    );
    return { loop, typed, warnings, timers };
  };

  test("starts at once after the send and repeats every eight seconds", async () => {
    const { loop, typed, timers } = loopFor();
    const calls: string[][] = [];
    const runMc = queued([
      { stdout: JSON.stringify({ sessions: [{ id: "h-active", status: "active", discord_thread: { guild_id: "1", channel_id: "2", thread_id: "20", retired: false }, active_bindings: [{ binding_id: "12", surface: "discord", channel_ref: "20" }] }] }) },
      { stdout: JSON.stringify({ message_id: 1 }) },
    ], calls);
    expect(await routeDiscordFreeText(policy, message, runMc, undefined, loop)).toBe("sent");
    expect(typed).toEqual(["20"]);
    timers.advance(TYPING_INTERVAL_MS - 1);
    expect(typed.length).toBe(1);
    timers.advance(1);
    expect(typed.length).toBe(2);
    timers.advance(TYPING_INTERVAL_MS * 3);
    expect(typed).toEqual(["20", "20", "20", "20", "20"]);
    loop.stop();
  });

  test("a rejected message starts no typing", async () => {
    const { loop, typed } = loopFor();
    expect(await routeDiscordFreeText(policy, { ...message, author_id: "4" }, queued([], []), undefined, loop)).toBe("ignored");
    expect(typed).toEqual([]);
  });

  test("stops when the outbox delivers a reply for that session", async () => {
    const { loop, typed, timers } = loopFor();
    loop.start("2", "h");
    loop.start("2", "h-other");
    timers.advance(TYPING_INTERVAL_MS);
    expect(typed.length).toBe(4);
    const runMc = queued([
      { stdout: JSON.stringify({ surface: "discord", rows: [{ id: 42, kind: "homie_reply", session_id: "h", surface: "discord", channel_ref: "2", payload: { body: "reply" } }] }) },
      { stdout: JSON.stringify({ id: 42, acked: true }) },
    ], []);
    await drainDiscordOutbox(policy, runMc, { triggerTyping: async () => {}, postMessage: async () => {} }, undefined, loop);
    timers.advance(TYPING_INTERVAL_MS * 10);
    // Only the other session's loop is still typing.
    expect(typed.length).toBe(14);
    loop.replied("h-other");
    timers.advance(TYPING_INTERVAL_MS * 10);
    expect(typed.length).toBe(14);
  });

  test("stops at the deadline when no reply arrives", () => {
    const { loop, typed, timers } = loopFor();
    loop.start("2", "h");
    timers.advance(TYPING_DEADLINE_MS - 1);
    const beforeDeadline = typed.length;
    expect(beforeDeadline).toBe(Math.ceil(TYPING_DEADLINE_MS / TYPING_INTERVAL_MS));
    timers.advance(TYPING_INTERVAL_MS * 10);
    expect(typed.length).toBe(beforeDeadline);
  });

  test("a second message for the session restarts its deadline", () => {
    const { loop, typed, timers } = loopFor();
    loop.start("2", "h");
    timers.advance(TYPING_DEADLINE_MS / 2);
    loop.start("2", "h");
    timers.advance(TYPING_DEADLINE_MS - 1);
    expect(typed.length).toBeGreaterThan(Math.ceil(TYPING_DEADLINE_MS / TYPING_INTERVAL_MS));
    loop.stop();
    timers.advance(TYPING_INTERVAL_MS * 10);
  });

  test("a failed typing call is logged and the loop goes on", async () => {
    const { loop, typed, warnings, timers } = loopFor(true);
    loop.start("2", "h");
    await Bun.sleep(0);
    timers.advance(TYPING_INTERVAL_MS);
    await Bun.sleep(0);
    expect(typed.length).toBe(2);
    expect(warnings).toEqual([
      "Discord typing indicator failed: HTTP 429",
      "Discord typing indicator failed: HTTP 429",
    ]);
    loop.stop();
  });
});

describe("Discord native surface", () => {
  test("rejects wrong author, location, bot, and empty message before mc", async () => {
    for (const rejected of [
      { ...message, author_id: "4" }, { ...message, guild_id: "5" },
      { ...message, channel_id: "6", parent_channel_id: null }, { ...message, author_is_bot: true }, { ...message, content: "" },
      { ...message, message_id: "not-a-snowflake" },
      { ...message, content: "4E966824D2FE6091" }, { ...message, content: "  4e966824d2fe6091  " },
    ]) {
      expect(authorizedDiscordMessage(policy, rejected)).toBe(false);
      const calls: string[][] = [];
      expect(await routeDiscordFreeText(policy, rejected, queued([{ stdout: JSON.stringify({ sessions: [] }) }], calls), undefined, undefined, { async ensure() {} })).toBe("ignored");
      expect(calls.every(call => call.join(" ") === "homie list")).toBe(true);
    }
  });

  test("each channel message creates its own thread and session", async () => {
    const calls: string[][] = [], threads: string[] = [];
    const runMc = queued([
      { stdout: JSON.stringify({ sessions: [] }) },
      { stdout: JSON.stringify({ session_id: "h-new" }) },
      { stdout: JSON.stringify({ message_id: 1 }) },
    ], calls);
    const root = { ...message, channel_id: "2" };
    expect(await routeDiscordFreeText(policy, root, runMc, undefined, undefined,
      { async ensure(value) { threads.push(value.message_id); } })).toBe("started");
    expect(threads).toEqual(["100"]);
    expect(calls).toEqual([
      ["homie", "list"],
      ["homie", "start", "--from", "discord:100", "--discord-guild", "1", "--discord-parent", "2"],
      ["homie", "send", "h-new", "--from", "discord:100", "--body", "hello", "--source-message", "100"],
    ]);
  });

  test("a retained thread continues after restart without a thread event", async () => {
    for (const status of ["active", "ended"]) {
      const calls: string[][] = [], nudges: string[] = [];
      const runMc = queued([
        { stdout: JSON.stringify({ sessions: [{ id: "h-existing", status, discord_thread: {
          guild_id: "1", channel_id: "2", thread_id: "20", retired: false,
        } }] }) },
        { stdout: JSON.stringify({ message_id: 1 }) },
      ], calls);
      expect(await routeDiscordFreeText(policy, { ...message, parent_channel_id: null }, runMc, id => nudges.push(id))).toBe(status === "active" ? "sent" : "resumed");
      expect(calls.at(-1)).toEqual(["homie", "send", "h-existing", "--from", "discord:20", "--body", "hello", "--source-message", "100"]);
      expect(nudges).toEqual(["h-existing"]);
    }
  });

  test("unknown and retired threads create no session", async () => {
    for (const thread of [undefined, { guild_id: "1", channel_id: "2", thread_id: "20", retired: true },
      { guild_id: "1", channel_id: "9", thread_id: "20", retired: false }]) {
      const calls: string[][] = [];
      expect(await routeDiscordFreeText(policy, message, queued([
        { stdout: JSON.stringify({ sessions: [{ id: "h", status: "active", discord_thread: thread }] }) },
      ], calls), undefined, undefined, { async ensure() {} })).toBe("ignored");
      expect(calls).toEqual([["homie", "list"]]);
    }
  });

  test("a failed thread creation starts no session", async () => {
    const calls: string[][] = [];
    await expect(routeDiscordFreeText(policy, { ...message, channel_id: "2" }, queued([
      { stdout: JSON.stringify({ sessions: [] }) },
    ], calls), undefined, undefined, { async ensure() { throw new Error("Permission denied"); } })).rejects.toThrow("Permission denied");
    expect(calls).toEqual([["homie", "list"]]);
  });

  test("thread creation recovers an existing thread and refuses another parent", async () => {
    const root = { ...message, channel_id: "2" };
    const thread = { id: "100", guild_id: "1", parent_id: "2", type: 11 };
    const paths: string[] = [];
    const responses = [new Response(null, { status: 404 }), new Response(null, { status: 400 }), Response.json(thread)];
    const transport = nativeDiscordThreads(policy, (async (url: any) => { paths.push(String(url)); return responses.shift()!; }) as typeof fetch);
    await transport.ensure(root);
    expect(paths).toEqual(["https://discord.com/api/v10/channels/100", "https://discord.com/api/v10/channels/2/messages/100/threads", "https://discord.com/api/v10/channels/100"]);
    await expect(nativeDiscordThreads(policy, (async () => Response.json({ ...thread, parent_id: "9" })) as typeof fetch).ensure(root)).rejects.toThrow("another destination");
  });

  test("a lost thread response recovers the thread that Discord created", async () => {
    let requests = 0;
    const transport = nativeDiscordThreads(policy, (async () => {
      requests++;
      if (requests === 1) return new Response(null, { status: 404 });
      if (requests === 2) throw new Error("The response was lost.");
      return Response.json({ id: "100", guild_id: "1", parent_id: "2", type: 11 });
    }) as typeof fetch);
    await transport.ensure({ ...message, channel_id: "2" });
    expect(requests).toBe(3);
  });

  test("a replay starts no extra reply or typing indicator", async () => {
    const calls: string[][] = [], nudges: string[] = [];
    const runMc = queued([
      { stdout: JSON.stringify({ sessions: [{ id: "h-existing", status: "active", discord_thread: {
        guild_id: "1", channel_id: "2", thread_id: "20", retired: false,
      } }] }) },
      { stdout: JSON.stringify({ message_id: 1, duplicate: true }) },
    ], calls);
    await routeDiscordFreeText(policy, message, runMc, id => nudges.push(id), { start() { throw new Error("Unexpected typing indicator"); } });
    expect(nudges).toEqual([]);
  });

  test("delivery reaches another thread after a full page of failed rows", async () => {
    const calls: string[][] = [];
    const rows = Array.from({ length: 100 }, (_, index) => ({ id: index + 1, kind: "health", surface: "discord", channel_ref: "20", payload: { body: "Alert" } }));
    const runMc = queued([
      { stdout: JSON.stringify({ surface: "discord", rows }) },
      { stdout: JSON.stringify({ surface: "discord", rows: [{ ...rows[0], id: 101, channel_ref: "30" }] }) },
      { stdout: "{}" },
    ], calls);
    const sent: string[] = [];
    await expect(drainDiscordOutbox(policy, runMc, { async postMessage(channel) {
      if (channel === "20") throw new Error("The first thread is unavailable.");
      sent.push(channel);
    } })).rejects.toThrow("first thread");
    expect(sent).toEqual(["30"]);
    expect(calls.slice(1)).toEqual([["outbox", "poll", "--surface", "discord", "--after", "100"], ["outbox", "ack", "101", "--surface", "discord"]]);
  });

  test("a queue failure preserves the active Gateway connection", async () => {
    const originalFetch = globalThis.fetch;
    const originalSocket = globalThis.WebSocket;
    let surface: Awaited<ReturnType<typeof startDiscordSurface>> | undefined;
    let failed!: () => void;
    const failure = new Promise<void>(resolve => { failed = resolve; });
    class Socket {
      onmessage?: (event: { data: string }) => void;
      onclose?: (event: { code: number }) => void;
      constructor() {
        queueMicrotask(() => this.onmessage?.({ data: JSON.stringify({ op: 0, t: "READY", s: 1,
          d: { session_id: "test-session", resume_gateway_url: "wss://gateway.discord.gg" } }) }));
      }
      close(code: number) { this.onclose?.({ code }); }
    }
    try {
      globalThis.fetch = (async () => Response.json({ url: "wss://gateway.discord.gg" })) as typeof fetch;
      globalThis.WebSocket = Socket as unknown as typeof WebSocket;
      surface = await startDiscordSurface(policy, async () => { throw new Error("The queue is unavailable."); },
        { warn() { failed(); } } as any, "/unused");
      await surface.ready();
      await failure;
      expect(surface.health().connected).toBe(true);
      expect(surface.health().detail).toContain("queued Discord message");
    } finally {
      await surface?.stop();
      globalThis.fetch = originalFetch;
      globalThis.WebSocket = originalSocket;
    }
  });

  test("posts a durable outbox row before its ack", async () => {
    const calls: string[][] = [];
    const runMc = queued([
      { stdout: JSON.stringify({ surface: "discord", rows: [{ id: 42, kind: "homie_reply", session_id: "h", surface: "discord", channel_ref: "2", payload: { body: "reply" } }] }) },
      { stdout: JSON.stringify({ id: 42, acked: true }) },
    ], calls);
    const posts: unknown[] = [];
    expect(await drainDiscordOutbox(policy, runMc, { triggerTyping: async () => {}, postMessage: async (channel, body) => { posts.push({ channel, ...body }); } })).toBe(1);
    expect(posts).toEqual([{ channel: "2", content: "reply", nonce: "mc-16" }]);
    expect(calls).toEqual([["outbox", "poll", "--surface", "discord"], ["outbox", "ack", "42", "--surface", "discord"]]);
  });

  test("long replies preserve text and retry each part before the row acknowledgement", async () => {
    const body = "a".repeat(1999) + "🥚" + "b".repeat(2200);
    const calls: string[][] = [];
    const posts: Array<{ content: string; nonce: string }> = [];
    let fail = true;
    const runMc: Exec = async argv => {
      calls.push(argv);
      return { exitCode: 0, stderr: "", stdout: JSON.stringify(argv[1] === "poll"
        ? { surface: "discord", rows: [{ id: 42, kind: "homie_reply", surface: "discord", payload: { body } }] }
        : { acked: true }) };
    };
    const rest = { async postMessage(_channel: string, post: { content: string; nonce: string }) {
      if (fail && posts.length === 1) throw new Error("The second part failed.");
      posts.push(post);
    } };
    await expect(drainDiscordOutbox(policy, runMc, rest)).rejects.toThrow("second part");
    expect(calls).toEqual([["outbox", "poll", "--surface", "discord"]]);
    fail = false;
    expect(await drainDiscordOutbox(policy, runMc, rest)).toBe(1);
    expect(posts[0]).toEqual(posts[1]);
    expect(posts.slice(1).map(post => post.content).join("")).toBe(body);
    expect(posts.slice(1).map(post => post.nonce)).toEqual(["mc-16", "mc-16-1", "mc-16-2"]);
    expect(posts.every(post => post.content.length <= 2000 && post.content.isWellFormed())).toBe(true);
    expect(calls.at(-1)).toEqual(["outbox", "ack", "42", "--surface", "discord"]);
  });

  test("a stopped connection acknowledges its completed delivery and starts no next delivery", async () => {
    const calls: string[][] = [];
    const runMc = queued([
      { stdout: JSON.stringify({ surface: "discord", rows: [42, 43].map(id => ({ id, kind: "health", surface: "discord", channel_ref: "2", payload: { body: "Test alert" } })) }) },
      { stdout: JSON.stringify({ id: 42, acked: true }) },
    ], calls);
    let active = true;
    const posts: string[] = [];
    const delivered = await drainDiscordOutbox(policy, runMc, {
      postMessage: async channel => { posts.push(channel); active = false; },
    }, undefined, undefined, () => active);
    expect(delivered).toBe(1);
    expect(posts).toEqual(["2"]);
    expect(calls).toEqual([["outbox", "poll", "--surface", "discord"], ["outbox", "ack", "42", "--surface", "discord"]]);
  });

  test("posts and acknowledges a terminal attachment failure receipt", async () => {
    const calls: string[][] = [];
    const runMc = queued([
      { stdout: JSON.stringify({ surface: "discord", rows: [{
        id: 43, kind: "homie_reply", session_id: "h", surface: "discord", channel_ref: "2",
        payload: {
          code: "attachment_publish_failed", attachment_ingress_id: 7,
          attempt_id: "0123456789abcdef", attempt: 3, outcome: "failed",
          reason: "hard_expired", message: "Attachment publication failed after three attempts.",
          body: "Attachment publication failed after three attempts.", retryable: false,
        },
      }] }) },
      { stdout: JSON.stringify({ id: 43, acked: true }) },
    ], calls);
    const posts: unknown[] = [];
    expect(await drainDiscordOutbox(policy, runMc, {
      postMessage: async (channel, body) => { posts.push({ channel, ...body }); },
    })).toBe(1);
    expect(posts).toEqual([{
      channel: "2", content: "Attachment publication failed after three attempts.",
      nonce: "mc-17",
    }]);
    expect(calls).toEqual([
      ["outbox", "poll", "--surface", "discord"],
      ["outbox", "ack", "43", "--surface", "discord"],
    ]);
  });

  test("posts configured blocked and health alert rows before acknowledging them", async () => {
    const calls: string[][] = [];
    const runMc = queued([
      { stdout: JSON.stringify({ surface: "discord", rows: [
        {
          id: 46, kind: "blocked_alert", session_id: null, surface: "discord",
          channel_ref: "22", payload: {
            task_id: 7, reason: "operator input",
            body: "Task #7 blocked: operator input",
          },
        },
        {
          id: 47, kind: "health", session_id: null, surface: "discord",
          channel_ref: "22", payload: {
            activity_id: 9, body: "Culler health: routing unavailable",
          },
        },
      ] }) },
      { stdout: JSON.stringify({ id: 46, acked: true }) },
      { stdout: JSON.stringify({ id: 47, acked: true }) },
    ], calls);
    const posts: unknown[] = [];
    await expect(drainDiscordOutbox(policy, runMc, {
      postMessage: async (channel, body) => { posts.push({ channel, ...body }); },
    })).resolves.toBe(2);
    expect(posts).toEqual([
      {
        channel: "22", content: "Task #7 blocked: operator input",
        nonce: "mc-1a",
      },
      {
        channel: "22", content: "Culler health: routing unavailable",
        nonce: "mc-1b",
      },
    ]);
    expect(calls).toEqual([
      ["outbox", "poll", "--surface", "discord"],
      ["outbox", "ack", "46", "--surface", "discord"],
      ["outbox", "ack", "47", "--surface", "discord"],
    ]);
  });

  test("does not ack when Discord post fails or a row carries an unsupported kind", async () => {
    const poll = { stdout: JSON.stringify({ surface: "discord", rows: [{ id: 42, kind: "homie_reply", session_id: "h", surface: "discord", channel_ref: "2", payload: { body: "reply" } }] }) };
    const calls: string[][] = [];
    await expect(drainDiscordOutbox(policy, queued([poll], calls), { triggerTyping: async () => {}, postMessage: async () => { throw new Error("network down"); } })).rejects.toThrow("network down");
    expect(calls).toEqual([["outbox", "poll", "--surface", "discord"]]);
    await expect(drainDiscordOutbox(policy, queued([{ stdout: JSON.stringify({ surface: "discord", rows: [{ id: 9, kind: "banner", session_id: null, surface: "discord", channel_ref: null, payload: { body: "brief" } }] }) }], []), { triggerTyping: async () => {}, postMessage: async () => {} })).rejects.toThrow("unsupported outbox kind");
  });

  test("verifies every typed Homie attachment before one post and its ack", async () => {
    const calls: string[][] = [];
    const posts: unknown[] = [];
    const reads: unknown[] = [];
    const session = "h-0123456789abcdef";
    const first = new TextEncoder().encode("seven");
    const second = new TextEncoder().encode("eight");
    const firstDigest = createHash("sha256").update(first).digest("hex");
    const secondDigest = createHash("sha256").update(second).digest("hex");
    const poll = { stdout: JSON.stringify({
      surface: "discord",
      rows: [{
        id: 44, kind: "homie_echo", session_id: session, surface: "discord",
        channel_ref: "2", payload: {
          body: "attachment",
          attachments: [
            {
              ordinal: 1, session_id: session, direction: "in",
              sha256: firstDigest, size: first.byteLength,
              media_type: "text/plain", display_name: "seven.txt",
            },
            {
              ordinal: 2, session_id: session, direction: "in",
              sha256: secondDigest, size: second.byteLength,
              media_type: "application/octet-stream", display_name: "eight.bin",
            },
          ],
        },
      }],
    }) };
    await expect(drainDiscordOutbox(
      policy,
      queued([poll, { stdout: JSON.stringify({ id: 44, acked: true }) }], calls),
      { triggerTyping: async () => {}, postMessage: async (channel, body) => { posts.push({ channel, ...body }); } },
      {
        readAttachment: async (reference) => {
          reads.push(reference);
          const bytes = reference.ordinal === 1 ? first : second;
          return { name: "ignored", contentType: "ignored", bytes };
        },
      },
    )).resolves.toBe(1);
    expect(reads).toEqual([
      {
        ordinal: 1, session_id: session, direction: "in",
        sha256: firstDigest, size: first.byteLength,
        media_type: "text/plain", display_name: "seven.txt",
      },
      {
        ordinal: 2, session_id: session, direction: "in",
        sha256: secondDigest, size: second.byteLength,
        media_type: "application/octet-stream", display_name: "eight.bin",
      },
    ]);
    expect(posts).toEqual([{
      channel: "2", content: "attachment", nonce: "mc-18",
      files: [
        { name: "seven.txt", contentType: "text/plain", bytes: first },
        { name: "eight.bin", contentType: "application/octet-stream", bytes: second },
      ],
    }]);
    expect(calls).toEqual([
      ["outbox", "poll", "--surface", "discord"],
      ["outbox", "ack", "44", "--surface", "discord"],
    ]);
  });

  test("leaves typed attachment rows unacked when any reference or object fails", async () => {
    const session = "h-0123456789abcdef";
    const bytes = new TextEncoder().encode("sealed");
    const digest = createHash("sha256").update(bytes).digest("hex");
    const reference = {
      ordinal: 1, session_id: session, direction: "in",
      sha256: digest, size: bytes.byteLength,
      media_type: "text/plain", display_name: "sealed.txt",
    };
    const row = {
      id: 45, kind: "homie_echo", session_id: session, surface: "discord",
      channel_ref: "2", payload: { body: "attachment", attachments: [reference] },
    };
    for (const attachments of [
      [{ ...reference, session_id: "h-fedcba9876543210" }],
      [{ ...reference, direction: "out" }],
      [{ ...reference, ordinal: 2 }],
      [{ ...reference, path: "/tmp/sealed" }],
    ]) {
      const calls: string[][] = [];
      const posts: unknown[] = [];
      let reads = 0;
      await expect(drainDiscordOutbox(
        policy,
        queued([{ stdout: JSON.stringify({
          surface: "discord", rows: [{ ...row, payload: { ...row.payload, attachments } }],
        }) }], calls),
        { triggerTyping: async () => {}, postMessage: async (...args) => { posts.push(args); } },
        {
          readAttachment: async () => {
            reads++;
            return { name: "sealed.txt", contentType: "text/plain", bytes };
          },
        },
      )).rejects.toThrow("attachment payload is malformed");
      expect({ reads, posts, calls }).toEqual({
        reads: 0, posts: [], calls: [["outbox", "poll", "--surface", "discord"]],
      });
    }

    const second = { ...reference, ordinal: 2, display_name: "second.txt" };
    const calls: string[][] = [];
    const posts: unknown[] = [];
    let reads = 0;
    await expect(drainDiscordOutbox(
      policy,
      queued([{ stdout: JSON.stringify({
        surface: "discord",
        rows: [{ ...row, payload: { ...row.payload, attachments: [reference, second] } }],
      }) }], calls),
      { triggerTyping: async () => {}, postMessage: async (...args) => { posts.push(args); } },
      {
        readAttachment: async () => {
          reads++;
          if (reads === 2) throw new Error("object unavailable");
          return { name: "sealed.txt", contentType: "text/plain", bytes };
        },
      },
    )).rejects.toThrow("object unavailable");
    expect({ reads, posts, calls }).toEqual({
      reads: 2, posts: [], calls: [["outbox", "poll", "--surface", "discord"]],
    });

    await expect(drainDiscordOutbox(
      policy,
      queued([{ stdout: JSON.stringify({ surface: "discord", rows: [row] }) }], []),
      { triggerTyping: async () => {}, postMessage: async () => { throw new Error("post must not run"); } },
      {
        readAttachment: async () => ({
          name: "sealed.txt", contentType: "text/plain",
          bytes: new TextEncoder().encode("forged"),
        }),
      },
    )).rejects.toThrow("bounded multipart request");
  });

  test("reads a typed attachment only from its digest-derived published root", async () => {
    const home = await mkdtemp(join(await realpath(tmpdir()), "mc-discord-attachment-"));
    const session = "h-0123456789abcdef";
    const bytes = new TextEncoder().encode("immutable attachment");
    const digest = createHash("sha256").update(bytes).digest("hex");
    const root = join(
      home, "attachments", session, "in", "published", "sha256", digest.slice(0, 2),
    );
    await mkdir(root, { recursive: true });
    const object = join(root, digest);
    await writeFile(object, bytes);
    const plane = nativeDiscordFilePlane(home);
    const reference = {
      ordinal: 1, session_id: session, direction: "in" as const,
      sha256: digest, size: bytes.byteLength,
      media_type: "text/plain", display_name: "attachment.txt",
    };
    try {
      await expect(plane.readAttachment!(reference)).resolves.toEqual({
        name: "attachment.txt", contentType: "text/plain", bytes,
      });
      await writeFile(object, new TextEncoder().encode("mutable attachment!!"));
      await expect(plane.readAttachment!(reference)).rejects.toThrow(
        "content or identity drifted",
      );

      const linkedBytes = new TextEncoder().encode("linked");
      const linkedDigest = createHash("sha256").update(linkedBytes).digest("hex");
      const linkedRoot = join(
        home, "attachments", session, "in", "published", "sha256", linkedDigest.slice(0, 2),
      );
      await mkdir(linkedRoot, { recursive: true });
      await symlink(object, join(linkedRoot, linkedDigest));
      await expect(plane.readAttachment!({
        ...reference, sha256: linkedDigest, size: linkedBytes.byteLength,
      })).rejects.toThrow("exact regular file");
      await expect(plane.readAttachment!({
        ...reference, size: 10 * 1024 * 1024 + 1,
      })).rejects.toThrow("malformed or oversized");
    } finally {
      await rm(home, { recursive: true, force: true });
    }
  });

  test("Gateway identifies, records resumable state, and routes only a filtered MESSAGE_CREATE", async () => {
    const calls: string[][] = [];
    const runMc = queued([
      { stdout: JSON.stringify({ sessions: [{ id: "h-new", status: "active", discord_thread: { guild_id: "1", channel_id: "2", thread_id: "44", retired: false } }] }) },
      { stdout: JSON.stringify({ message_id: 1 }) },
    ], calls);
    const sent: unknown[] = [];
    const socket = { send: (frame: string) => { sent.push(JSON.parse(frame)); }, close: () => {} };
    const state = new DiscordGatewayState(policy, runMc, () => {});
    await expect(state.receive(socket, { op: 10, d: { heartbeat_interval: 45_000 } })).resolves.toBe("hello");
    expect(sent).toEqual([{ op: 2, d: { token: "secret", intents: 33_281, properties: { os: process.platform, browser: "culler", device: "culler" } } }]);
    await state.receive(socket, { op: 0, s: 9, t: "READY", d: { session_id: "gateway-session", resume_gateway_url: "wss://gateway.discord.gg" } });
    await state.receive(socket, { op: 0, s: 10, t: "THREAD_CREATE", d: { id: "44", parent_id: "2" } });
    await state.receive(socket, { op: 0, s: 11, t: "MESSAGE_CREATE", d: {
      id: "101", guild_id: "1", channel_id: "44", content: "thread text", author: { id: "3", bot: false },
    } });
    await state.receive(socket, { op: 0, s: 12, t: "MESSAGE_CREATE", d: {
      id: "102", guild_id: "1", channel_id: "44", content: "attacker", author: { id: "4", bot: false },
    } });
    expect(calls).toEqual([
      ["homie", "list"],
      ["homie", "send", "h-new", "--from", "discord:44", "--body", "thread text", "--source-message", "101"],
    ]);
    const resume: unknown[] = [];
    state.resume({ send: (frame: string) => { resume.push(JSON.parse(frame)); }, close: () => {} });
    expect(resume).toEqual([{ op: 6, d: { token: "secret", session_id: "gateway-session", seq: 12 } }]);
  });
});
