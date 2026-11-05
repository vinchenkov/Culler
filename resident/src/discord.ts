// Discord is a native resident surface: it never opens the spine and it
// never interprets natural language. This module deliberately separates the
// deterministic `mc`/REST boundary from Gateway ownership, so the security
// rules can be pinned without a bot account.

import type { Exec, ExecResult } from "./types";
import type { Logger } from "../../runner/shared/log";
import { createHash } from "node:crypto";
import { constants } from "node:fs";
import { lstat, open, realpath } from "node:fs/promises";
import { join, relative, resolve } from "node:path";

export interface DiscordPolicy {
  enabled: boolean;
  bot_token: string;
  guild_id: string;
  channel_id: string;
  operator_ids: string[];
}

export interface DiscordInboundMessage {
  message_id: string;
  author_id: string;
  guild_id: string | null;
  channel_id: string;
  /** For a Discord thread, the parent channel. */
  parent_channel_id?: string | null;
  content: string;
  author_is_bot?: boolean;
  author_name?: string;
}

export interface DiscordRest {
  postMessage(channelID: string, body: DiscordPost): Promise<void>;
  /** Show the channel's typing indicator; it lasts about ten seconds. */
  triggerTyping(channelID: string): Promise<void>;
}

/** Injectable clock and timers, so the typing loop is testable in an instant. */
export interface DiscordTypingTimers {
  now(): number;
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
}

/** The typing indicator lasts about ten seconds; eight keeps it continuous. */
export const TYPING_INTERVAL_MS = 8_000;
/** A reply that has not arrived after this long gets no more typing. */
export const TYPING_DEADLINE_MS = 10 * 60_000;

const realTypingTimers: DiscordTypingTimers = {
  now: () => Date.now(),
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

// DiscordTypingLoop shows the typing indicator in a channel from the moment an
// inbound message is sent to Homie until the outbox delivers a reply for that
// session, or the deadline passes. One loop per session; a second inbound
// message restarts its deadline. A failed call is logged and the loop goes on.
export class DiscordTypingLoop {
  private loops = new Map<string, { channel: string; startedAt: number; timer: unknown }>();

  constructor(
    private readonly rest: Pick<DiscordRest, "triggerTyping">,
    private readonly log: Pick<Logger, "warn">,
    private readonly timers: DiscordTypingTimers = realTypingTimers,
  ) {}

  start(channelID: string, sessionID: string): void {
    const existing = this.loops.get(sessionID);
    if (existing !== undefined) this.timers.clearTimeout(existing.timer);
    this.loops.set(sessionID, { channel: channelID, startedAt: this.timers.now(), timer: undefined });
    this.tick(sessionID);
  }

  replied(sessionID: string): void {
    const loop = this.loops.get(sessionID);
    if (loop === undefined) return;
    this.timers.clearTimeout(loop.timer);
    this.loops.delete(sessionID);
  }

  stop(): void {
    for (const sessionID of [...this.loops.keys()]) this.replied(sessionID);
  }

  private tick(sessionID: string): void {
    const loop = this.loops.get(sessionID);
    if (loop === undefined) return;
    if (this.timers.now() - loop.startedAt >= TYPING_DEADLINE_MS) {
      this.loops.delete(sessionID);
      return;
    }
    this.rest.triggerTyping(loop.channel).catch((err: unknown) => {
      this.log.warn("discord.typing_failed", `Discord typing indicator failed: ${err instanceof Error ? err.message : String(err)}`);
    });
    loop.timer = this.timers.setTimeout(() => this.tick(sessionID), TYPING_INTERVAL_MS);
  }
}

export interface DiscordUpload {
  name: string;
  contentType: string;
  bytes: Uint8Array;
}

export interface DiscordPost {
  content: string;
  nonce: string;
  files?: DiscordUpload[];
}

// The native surface owns file reads.  Its narrow reader seam keeps an outbox
// reference from becoming a general host-path capability and lets delivery be
// tested without a bot token or real file plane.
export interface DiscordFilePlane {
  readAttachment(reference: DiscordAttachmentReference): Promise<DiscordUpload>;
}

export interface DiscordAttachmentReference {
  ordinal: number;
  session_id: string;
  direction: "in" | "out";
  sha256: string;
  size: number;
  media_type: string;
  display_name: string;
}

export interface DiscordSurfaceHandle {
  stop(): Promise<void>;
  ready(): Promise<void>;
  activate(): void;
  health(): { connected: boolean; detail: string; checked_at: string };
}

export interface DiscordOutboxRow {
  id: number;
  kind: string;
  session_id: string | null;
  surface: "discord";
  channel_ref: string | null;
  payload: Record<string, unknown>;
}

const decimalID = /^[0-9]{1,20}$/;
export const discordConfirmationCode = /^[0-9A-F]{16}$/i;
const homieSessionID = /^h-[0-9a-f]{16}$/;
const attachmentDigest = /^[0-9a-f]{64}$/;
const maxAttachmentObjects = 32;
const maxAttachmentObjectBytes = 256 * 1024 * 1024;
const maxAttachmentBatchBytes = 512 * 1024 * 1024;
const maxDiscordAttachmentBytes = 10 * 1024 * 1024;
const maxDiscordMultipartBytes = 24 * 1024 * 1024;

function exactString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function mcFailure(action: string, result: ExecResult): Error {
  return new Error(`${action} failed (exit ${result.exitCode}): ${result.stderr.trim()}`);
}

export function validateDiscordPolicy(policy: DiscordPolicy | undefined): DiscordPolicy | undefined {
  if (policy === undefined) return undefined;
  if (typeof policy !== "object" || policy === null || typeof policy.enabled !== "boolean" ||
      typeof policy.bot_token !== "string" || typeof policy.guild_id !== "string" ||
      typeof policy.channel_id !== "string" || !Array.isArray(policy.operator_ids)) {
    throw new Error("resident: Discord policy is malformed");
  }
  const allowed = new Set<string>();
  for (const id of policy.operator_ids) {
    if (typeof id !== "string" || !decimalID.test(id) || allowed.has(id)) {
      throw new Error("resident: Discord operator allowlist is malformed");
    }
    allowed.add(id);
  }
  if (policy.enabled) {
    if (policy.bot_token.length === 0 || !decimalID.test(policy.guild_id) ||
        !decimalID.test(policy.channel_id) || allowed.size === 0) {
      throw new Error("resident: enabled Discord policy is incomplete");
    }
  } else if (policy.bot_token !== "" || policy.guild_id !== "" || policy.channel_id !== "" || allowed.size !== 0) {
    throw new Error("resident: disabled Discord policy retains authority");
  }
  return policy;
}

// authorizedDiscordMessage is intentionally a pure no-op gate. In particular,
// it is evaluated before `homie list`, so an attacker cannot use a rejected
// event to observe timing or cause a host-side action.
export function authorizedDiscordMessage(policy: DiscordPolicy, message: DiscordInboundMessage): boolean {
  return policy.enabled && message.author_is_bot !== true &&
    decimalID.test(message.message_id) &&
    message.guild_id === policy.guild_id &&
    policy.operator_ids.includes(message.author_id) &&
    (message.channel_id === policy.channel_id || message.parent_channel_id === policy.channel_id) &&
    message.content.length > 0 && message.content.length <= 2_000 && !discordConfirmationCode.test(message.content.trim());
}

interface HomieBinding {
  binding_id: string;
  surface: string;
  channel_ref: string;
}

interface HomieSession {
  discord_thread?: { guild_id: string; channel_id: string; thread_id: string; retired: boolean; initial_received?: boolean };
  id: string;
  status: string;
  active_bindings?: HomieBinding[];
  known_bindings?: HomieBinding[];
}

function parseSessions(result: ExecResult): HomieSession[] {
  if (result.exitCode !== 0) throw mcFailure("mc homie list", result);
  let value: unknown;
  try {
    value = JSON.parse(result.stdout);
  } catch {
    throw new Error("mc homie list returned invalid JSON");
  }
  if (value === null || typeof value !== "object" || !Array.isArray((value as { sessions?: unknown }).sessions)) {
    throw new Error("mc homie list returned an invalid session list");
  }
  const sessions = (value as { sessions: unknown[] }).sessions;
  if (!sessions.every((s) => s !== null && typeof s === "object" &&
      typeof (s as HomieSession).id === "string" && typeof (s as HomieSession).status === "string")) {
    throw new Error("mc homie list returned a malformed session");
  }
  for (const session of sessions as HomieSession[]) {
    for (const bindings of [session.active_bindings, session.known_bindings]) {
      if (bindings === undefined) continue;
      if (!Array.isArray(bindings) || !bindings.every((binding) =>
        binding !== null && typeof binding === "object" &&
        canonicalBindingID(binding.binding_id) &&
        typeof binding.surface === "string" &&
        typeof binding.channel_ref === "string")) {
        throw new Error("mc homie list returned a malformed session binding");
      }
    }
  }
  return sessions as HomieSession[];
}

function canonicalBindingID(value: unknown): value is string {
  return typeof value === "string" && /^[1-9][0-9]{0,18}$/.test(value) &&
    BigInt(value) <= 9_223_372_036_854_775_807n;
}

async function requireMc(runMc: Exec, argv: string[], label: string): Promise<unknown> {
  const result = await runMc(argv);
  if (result.exitCode !== 0) throw mcFailure(label, result);
  try {
    return JSON.parse(result.stdout);
  } catch {
    throw new Error(`${label} returned invalid JSON`);
  }
}

/** Wakes the resident's round for one Homie session after a message write. */
export type HomieNudge = (sessionId: string) => void;

const noNudge: HomieNudge = () => {};

// routeDiscordFreeText maps only the surface reference. Its body is opaque
// operator text and travels straight to Homie; no command grammar belongs on
// the Discord side of this boundary. A successful send nudges the round.
export async function routeDiscordFreeText(
  policy: DiscordPolicy,
  message: DiscordInboundMessage,
  runMc: Exec,
  nudge: HomieNudge = noNudge,
  typing?: Pick<DiscordTypingLoop, "start">,
  threads: DiscordThreads = nativeDiscordThreads(policy),
): Promise<"ignored" | "sent" | "started" | "resumed"> {
  if (!authorizedDiscordMessage(policy, { ...message, parent_channel_id: policy.channel_id })) return "ignored";
  const root = message.channel_id === policy.channel_id;
  const channelRef = root ? message.message_id : message.channel_id;
  const from = `discord:${channelRef}`;
  const sessions = parseSessions(await runMc(["homie", "list"]));
  let session = sessions.find(candidate => {
    const thread = candidate.discord_thread;
    return thread && !thread.retired && thread.guild_id === policy.guild_id &&
      thread.channel_id === policy.channel_id && thread.thread_id === channelRef;
  });
  if (session && !root && session.discord_thread?.initial_received === false) {
    const source = await threads.recover?.(channelRef);
    if (!source || source.message_id !== channelRef || source.channel_id !== policy.channel_id || !authorizedDiscordMessage(policy, source)) {
      throw new Error("Culler cannot recover the initial Discord message. Check access to the channel and the initial message.");
    }
    await routeDiscordFreeText(policy, source, runMc, nudge, typing, threads);
  }
  let disposition: "sent" | "started" | "resumed" = "sent";
  if (!session) {
    if (!root) {
      const source = await threads.recover?.(channelRef);
      if (!source || source.message_id !== channelRef || source.channel_id !== policy.channel_id || !authorizedDiscordMessage(policy, source)) return "ignored";
      await routeDiscordFreeText(policy, source, runMc, nudge, typing, threads);
      return routeDiscordFreeText(policy, message, runMc, nudge, typing, threads);
    }
    await threads.ensure(message);
    const started = await requireMc(runMc, ["homie", "start", "--from", from,
      "--discord-guild", policy.guild_id, "--discord-parent", policy.channel_id], "mc homie start") as { session_id?: string };
    if (typeof started?.session_id !== "string") throw new Error("mc homie start returned no session id");
    session = { id: started.session_id, status: "active" };
    disposition = "started";
  } else if (session.status !== "active") {
    disposition = "resumed";
  }
  const sent = await requireMc(runMc, ["homie", "send", session.id, "--from", from, "--body", message.content,
    "--source-message", message.message_id], "mc homie send") as { duplicate?: boolean };
  if (!sent.duplicate) {
    nudge(session.id);
    typing?.start(channelRef, session.id);
  }
  return disposition;
}

function parseOutboxRows(result: ExecResult): DiscordOutboxRow[] {
  if (result.exitCode !== 0) throw mcFailure("mc outbox poll", result);
  let value: unknown;
  try {
    value = JSON.parse(result.stdout);
  } catch {
    throw new Error("mc outbox poll returned invalid JSON");
  }
  if (value === null || typeof value !== "object" || (value as { surface?: unknown }).surface !== "discord" ||
      !Array.isArray((value as { rows?: unknown }).rows)) {
    throw new Error("mc outbox poll returned an invalid Discord cursor");
  }
  const rows = (value as { rows: unknown[] }).rows;
  if (!rows.every((row) => row !== null && typeof row === "object" &&
      Number.isSafeInteger((row as DiscordOutboxRow).id) && (row as DiscordOutboxRow).id > 0 &&
      (row as DiscordOutboxRow).surface === "discord" &&
      typeof (row as DiscordOutboxRow).kind === "string" &&
      (row as DiscordOutboxRow).payload !== null && typeof (row as DiscordOutboxRow).payload === "object")) {
    throw new Error("mc outbox poll returned a malformed Discord row");
  }
  return rows as DiscordOutboxRow[];
}

function discordText(row: DiscordOutboxRow): string {
  const body = exactString(row.payload.body);
  if (body === undefined || body.length === 0) {
    throw new Error("Discord outbox payload has invalid text content");
  }
  return body;
}

function closedKeys(value: Record<string, unknown>, expected: string[]): boolean {
  return JSON.stringify(Object.keys(value).sort()) === JSON.stringify([...expected].sort());
}

function safeAttachmentMediaType(value: unknown): value is string {
  return typeof value === "string" && value.length >= 1 && value.length <= 255 &&
    /^[\x20-\x7e]+$/.test(value);
}

function safeAttachmentDisplayName(value: unknown): value is string {
  if (typeof value !== "string" || value.includes("/") || value.includes("\\")) return false;
  const length = new TextEncoder().encode(value).length;
  if (length < 1 || length > 255) return false;
  for (const character of value) {
    const code = character.codePointAt(0)!;
    if (code < 0x20 || code === 0x7f) return false;
  }
  return true;
}

function discordAttachmentReferences(row: DiscordOutboxRow): DiscordAttachmentReference[] {
  const value = row.payload.attachments;
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > maxAttachmentObjects ||
      (value.length > 0 &&
        (typeof row.session_id !== "string" || !homieSessionID.test(row.session_id)))) {
    throw new Error("Discord Homie attachment payload is malformed");
  }
  const expectedDirection = row.kind === "homie_echo" ? "in" :
    row.kind === "homie_reply" ? "out" : undefined;
  const references: DiscordAttachmentReference[] = [];
  let total = 0;
  for (const raw of value) {
    const reference = raw !== null && typeof raw === "object" && !Array.isArray(raw)
      ? raw as Record<string, unknown>
      : undefined;
    if (reference === undefined || expectedDirection === undefined ||
        !closedKeys(reference, [
          "direction", "display_name", "media_type", "ordinal",
          "session_id", "sha256", "size",
        ]) ||
        reference.ordinal !== references.length + 1 ||
        reference.session_id !== row.session_id ||
        reference.direction !== expectedDirection ||
        typeof reference.sha256 !== "string" || !attachmentDigest.test(reference.sha256) ||
        !Number.isSafeInteger(reference.size) || (reference.size as number) < 0 ||
        (reference.size as number) > maxAttachmentObjectBytes ||
        !safeAttachmentMediaType(reference.media_type) ||
        !safeAttachmentDisplayName(reference.display_name)) {
      throw new Error("Discord Homie attachment payload is malformed");
    }
    total += reference.size as number;
    if (total > maxAttachmentBatchBytes) {
      throw new Error("Discord Homie attachment payload exceeds its sealed batch");
    }
    references.push(reference as unknown as DiscordAttachmentReference);
  }
  return references;
}

async function discordPostFor(row: DiscordOutboxRow, filePlane: DiscordFilePlane | undefined): Promise<Omit<DiscordPost, "nonce">> {
  if (row.kind === "homie_echo" || row.kind === "homie_reply") {
    const references = discordAttachmentReferences(row);
    if (references.length === 0) return { content: discordText(row) };
    if (filePlane === undefined) {
      throw new Error("Discord Homie attachment delivery has no file-plane reader");
    }
    const files: DiscordUpload[] = [];
    let total = 0;
    for (const reference of references) {
      if (reference.size > maxDiscordAttachmentBytes) {
        throw new Error("Discord Homie attachment exceeds the provider file limit");
      }
      const file = await filePlane.readAttachment(reference);
      total += file.bytes.byteLength;
      if (file.bytes.byteLength !== reference.size ||
          createHash("sha256").update(file.bytes).digest("hex") !== reference.sha256 ||
          total > maxDiscordMultipartBytes) {
        throw new Error("Discord Homie attachments exceed the bounded multipart request");
      }
      files.push({
        name: reference.display_name,
        contentType: reference.media_type,
        bytes: file.bytes,
      });
    }
    return { content: discordText(row), files };
  }
  if (row.kind === "blocked_alert" || row.kind === "health") {
    return { content: discordText(row) };
  }
  throw new Error(`Discord cannot deliver unsupported outbox kind ${JSON.stringify(row.kind)}`);
}

export async function drainDiscordOutbox(
  policy: DiscordPolicy,
  runMc: Exec,
  rest: Pick<DiscordRest, "postMessage">,
  filePlane?: DiscordFilePlane,
  typing?: Pick<DiscordTypingLoop, "replied">,
  canSend: () => boolean = () => true,
): Promise<number> {
  if (!policy.enabled) return 0;
  let rows = parseOutboxRows(await runMc(["outbox", "poll", "--surface", "discord"]));
  let delivered = 0;
  const failed = new Set<string>();
  let failure: unknown;
  while (canSend()) {
    for (const row of rows) {
      if (!canSend()) break;
      const channel = row.channel_ref ?? policy.channel_id;
      if (failed.has(channel)) continue;
      try {
        if (!decimalID.test(channel)) throw new Error("Discord outbox row has an invalid channel reference");
        const post = await discordPostFor(row, filePlane);
        const parts: string[] = [];
        let part = "";
        for (const character of post.content) {
          if (part.length + character.length > 2_000) { parts.push(part); part = ""; }
          part += character;
        }
        parts.push(part);
        for (const [index, content] of parts.entries()) {
          if (!canSend()) return delivered;
          await rest.postMessage(channel, {
            content,
            ...(index === 0 && post.files ? { files: post.files } : {}),
            nonce: `mc-${row.id.toString(36)}${index === 0 ? "" : `-${index.toString(36)}`}`,
          });
        }
        if (row.kind === "homie_reply" && typeof row.session_id === "string") typing?.replied(row.session_id);
        await requireMc(runMc, ["outbox", "ack", String(row.id), "--surface", "discord"], "mc outbox ack");
        delivered++;
      } catch (error) { failed.add(channel); failure ??= error; }
    }
    if (rows.length < 100 || !canSend()) break;
    const after = rows.at(-1)!.id;
    rows = parseOutboxRows(await runMc(["outbox", "poll", "--surface", "discord", "--after", String(after)]));
    if (rows.some(row => row.id <= after)) throw new Error("The Discord outbox cursor did not advance.");
  }
  if (failure !== undefined) throw failure;
  return delivered;
}

const discordAPI = "https://discord.com/api/v10";
const gatewayIntents = (1 << 0) | (1 << 9) | (1 << 15); // GUILDS, GUILD_MESSAGES, MESSAGE_CONTENT

function discordAuthorization(policy: DiscordPolicy): Record<string, string> {
  return { authorization: `Bot ${policy.bot_token}`, "content-type": "application/json" };
}

function checkedDiscordURL(value: unknown): string {
  if (typeof value !== "string") throw new Error("Discord gateway endpoint returned no URL");
  const parsed = new URL(value);
  if (parsed.protocol !== "wss:" || !parsed.hostname.endsWith(".discord.gg")) {
    throw new Error("Discord gateway endpoint returned an unsafe URL");
  }
  parsed.searchParams.set("v", "10");
  parsed.searchParams.set("encoding", "json");
  return parsed.toString();
}

export interface DiscordThreads {
  ensure(message: DiscordInboundMessage): Promise<void>;
  recover?(threadID: string): Promise<DiscordInboundMessage | null>;
}

export function nativeDiscordThreads(policy: DiscordPolicy, request: typeof fetch = fetch): DiscordThreads {
  const validThread = (value: any, message: DiscordInboundMessage) => value?.id === message.message_id &&
    value?.parent_id === policy.channel_id && value?.guild_id === policy.guild_id && value?.type === 11;
  const lookup = async (message: DiscordInboundMessage) => {
    const result = await request(`${discordAPI}/channels/${message.message_id}`, {
      headers: discordAuthorization(policy), signal: AbortSignal.timeout(10_000),
    });
    if (result.status === 404) return false;
    if (!result.ok) throw new Error(`Discord thread lookup failed with HTTP ${result.status}`);
    if (!validThread(await result.json(), message)) throw new Error("The Discord thread belongs to another destination.");
    return true;
  };
  return { async ensure(message) {
    if (await lookup(message)) return;
    let result: Response;
    try { result = await request(`${discordAPI}/channels/${policy.channel_id}/messages/${message.message_id}/threads`, {
      method: "POST", headers: discordAuthorization(policy), signal: AbortSignal.timeout(10_000),
      body: JSON.stringify({ name: "Homie conversation", auto_archive_duration: 1440 }),
    }); } catch (error) {
      if (await lookup(message)) return;
      throw error;
    }
    if (!result.ok) {
      if (await lookup(message)) return;
      throw new Error(`Discord could not create the conversation thread. Check Create Public Threads permission. HTTP ${result.status}`);
    }
    if (!validThread(await result.json(), message)) throw new Error("Discord returned an invalid conversation thread.");
  }, async recover(threadID) {
    const read = async (path: string) => {
      const response = await request(`${discordAPI}${path}`, { headers: discordAuthorization(policy), signal: AbortSignal.timeout(10_000) });
      if (response.status === 404) return null;
      if (!response.ok) throw new Error(`Discord thread recovery failed with HTTP ${response.status}`);
      return response.json() as Promise<any>;
    };
    const thread = await read(`/channels/${threadID}`);
    if (!validThread(thread, { message_id: threadID } as DiscordInboundMessage)) return null;
    const bot = await read("/users/@me");
    if (!bot?.bot || thread.owner_id !== bot.id) return null;
    const source = await read(`/channels/${policy.channel_id}/messages/${threadID}`);
    if (source?.id !== threadID || source.channel_id !== policy.channel_id) return null;
    return { message_id: source.id, guild_id: policy.guild_id, channel_id: source.channel_id,
      author_id: source.author?.id ?? "", author_is_bot: source.author?.bot === true, content: source.content ?? "" };
  } };
}

export function nativeDiscordRest(policy: DiscordPolicy, request: typeof fetch = fetch): DiscordRest {
  return {
    async postMessage(channelID, body) {
      const hasFiles = body.files !== undefined && body.files.length > 0;
      const headers = discordAuthorization(policy);
      let requestBody: BodyInit;
      if (hasFiles) {
        // Let fetch set the multipart boundary.  The nonce stays in the
        // JSON payload so Discord's durable idempotency correlation survives
        // an at-least-once retry with the same file reference.
        delete headers["content-type"];
        const form = new FormData();
        form.set("payload_json", JSON.stringify({
          content: body.content, nonce: body.nonce, enforce_nonce: true,
        }));
        body.files!.forEach((file, index) => {
          form.set(`files[${index}]`, new Blob([file.bytes], { type: file.contentType }), file.name);
        });
        requestBody = form;
      } else {
        requestBody = JSON.stringify({
          content: body.content, nonce: body.nonce, enforce_nonce: true,
        });
      }
      const response = await request(`${discordAPI}/channels/${channelID}/messages`, {
        method: "POST", headers, body: requestBody, signal: AbortSignal.timeout(10_000),
      });
      if (!response.ok) throw new Error(`Discord post failed with HTTP ${response.status}`);
    },
    async triggerTyping(channelID) {
      const response = await request(`${discordAPI}/channels/${channelID}/typing`, {
        method: "POST", headers: discordAuthorization(policy), signal: AbortSignal.timeout(10_000),
      });
      if (!response.ok) throw new Error(`Discord typing failed with HTTP ${response.status}`);
    },
  };
}

// nativeDiscordFilePlane resolves one attachment reference beneath exactly
// MC_HOME/attachments.  It rejects symlinks, hard-linked files, escapes,
// non-regular objects and oversized payloads before Discord sees a byte.
export function nativeDiscordFilePlane(mcHome: string): DiscordFilePlane {
  return {
    async readAttachment(reference) {
      if (!homieSessionID.test(reference.session_id) ||
          (reference.direction !== "in" && reference.direction !== "out") ||
          !attachmentDigest.test(reference.sha256) ||
          !Number.isSafeInteger(reference.size) || reference.size < 0 ||
          reference.size > maxDiscordAttachmentBytes ||
          !safeAttachmentMediaType(reference.media_type) ||
          !safeAttachmentDisplayName(reference.display_name)) {
        throw new Error("Discord attachment reference is malformed or oversized");
      }
      const home = resolve(mcHome);
      if (await realpath(home) !== home) {
        throw new Error("Discord attachment home is not canonical");
      }
      const root = resolve(
        home, "attachments", reference.session_id, reference.direction,
        "published", "sha256",
      );
      const shard = join(root, reference.sha256.slice(0, 2));
      const candidate = join(shard, reference.sha256);
      let cursor = home;
      for (const part of relative(home, shard).split("/")) {
        cursor = join(cursor, part);
        const info = await lstat(cursor);
        if (!info.isDirectory() || info.isSymbolicLink()) {
          throw new Error("Discord attachment ancestor is not a real directory");
        }
      }
      const leaf = await lstat(candidate);
      if (!leaf.isFile() || leaf.isSymbolicLink() || leaf.nlink !== 1 ||
          leaf.size !== reference.size || await realpath(candidate) !== candidate) {
        throw new Error("Discord attachment is not its exact regular file");
      }
      const handle = await open(candidate, constants.O_RDONLY | constants.O_NOFOLLOW);
      try {
        const before = await handle.stat({ bigint: true });
        if (!before.isFile() || before.nlink !== 1n ||
            before.size !== BigInt(reference.size)) {
          throw new Error("Discord attachment changed before verification");
        }
        const bytes = new Uint8Array(await handle.readFile());
        const after = await handle.stat({ bigint: true });
        if (before.dev !== after.dev || before.ino !== after.ino ||
            before.size !== after.size || before.mtimeNs !== after.mtimeNs ||
            bytes.byteLength !== reference.size ||
            createHash("sha256").update(bytes).digest("hex") !== reference.sha256) {
          throw new Error("Discord attachment content or identity drifted");
        }
        return {
          name: reference.display_name,
          contentType: reference.media_type,
          bytes,
        };
      } finally {
        await handle.close();
      }
    },
  };
}

interface GatewayPayload {
  op: number;
  d: unknown;
  s?: number | null;
  t?: string | null;
}

interface GatewaySocket {
  send(data: string): unknown;
  close(code?: number, reason?: string): unknown;
}

function gatewayObject(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

// DiscordGatewayState owns only short-lived transport state. No sequence,
// token, or message is made durable here: the durable record is the Homie
// send/outbox transaction that follows the strict inbound filter.
export class DiscordGatewayState {
  private sequence: number | null = null;
  private sessionID: string | undefined;
  private resumeURL: string | undefined;
  private awaitingHeartbeatAck = false;
  private threadParents = new Map<string, string>();
  private seenMessageIDs = new Set<string>();
  private messages: Promise<void> = Promise.resolve();

  constructor(
    private readonly policy: DiscordPolicy,
    private readonly runMc: Exec,
    private readonly log: Logger,
    private readonly nudge: HomieNudge = noNudge,
    private readonly typing?: Pick<DiscordTypingLoop, "start">,
    private readonly observe?: (message: DiscordInboundMessage) => void,
    private readonly accept: () => boolean = () => true,
    private readonly threads: DiscordThreads = nativeDiscordThreads(policy),
  ) {}

  canResume(): boolean {
    return this.sequence !== null && this.sessionID !== undefined && this.resumeURL !== undefined;
  }

  gatewayURL(initialURL: string): string {
    return this.canResume() ? this.resumeURL! : initialURL;
  }

  identify(socket: GatewaySocket): void {
    socket.send(JSON.stringify({ op: 2, d: {
      token: this.policy.bot_token, intents: gatewayIntents,
      properties: { os: process.platform, browser: "culler", device: "culler" },
    } }));
  }

  resume(socket: GatewaySocket): void {
    if (!this.canResume()) {
      this.identify(socket);
      return;
    }
    socket.send(JSON.stringify({ op: 6, d: {
      token: this.policy.bot_token, session_id: this.sessionID, seq: this.sequence,
    } }));
  }

  heartbeat(socket: GatewaySocket): boolean {
    if (this.awaitingHeartbeatAck) return false;
    this.awaitingHeartbeatAck = true;
    socket.send(JSON.stringify({ op: 1, d: this.sequence }));
    return true;
  }

  async receive(socket: GatewaySocket, payload: GatewayPayload): Promise<"hello" | "reconnect" | "continue"> {
    if (!Number.isInteger(payload.op)) throw new Error("Discord Gateway payload has invalid opcode");
    if (payload.s !== undefined && payload.s !== null) {
      if (!Number.isSafeInteger(payload.s) || payload.s < 0) throw new Error("Discord Gateway payload has invalid sequence");
      this.sequence = payload.s;
    }
    switch (payload.op) {
      case 10: { // HELLO
        const hello = gatewayObject(payload.d);
        if (hello === undefined || !Number.isSafeInteger(hello.heartbeat_interval) || (hello.heartbeat_interval as number) <= 0) {
          throw new Error("Discord Gateway HELLO has invalid heartbeat interval");
        }
        this.resume(socket);
        return "hello";
      }
      case 11: // HEARTBEAT_ACK
        this.awaitingHeartbeatAck = false;
        return "continue";
      case 1: // server heartbeat request
        this.awaitingHeartbeatAck = false;
        this.heartbeat(socket);
        return "continue";
      case 7: // RECONNECT
        return "reconnect";
      case 9: // INVALID_SESSION
        if (payload.d !== true) {
          this.sequence = null;
          this.sessionID = undefined;
          this.resumeURL = undefined;
        }
        return "reconnect";
      case 0:
        const operation = this.messages.catch(() => {}).then(() => this.dispatch(payload));
        this.messages = operation;
        await operation;
        return "continue";
      default:
        return "continue";
    }
  }

  private rememberThread(data: Record<string, unknown>): void {
    const id = exactString(data.id);
    const parent = exactString(data.parent_id);
    if (id !== undefined && parent !== undefined && decimalID.test(id) && decimalID.test(parent)) {
      this.threadParents.set(id, parent);
      if (this.threadParents.size > 1_024) this.threadParents.delete(this.threadParents.keys().next().value!);
    }
  }

  private async dispatch(payload: GatewayPayload): Promise<void> {
    const data = gatewayObject(payload.d);
    if (data === undefined) return;
    if (payload.t === "READY") {
      const sessionID = exactString(data.session_id);
      const resumeURL = exactString(data.resume_gateway_url);
      if (sessionID === undefined || resumeURL === undefined) throw new Error("Discord READY has no resumable session");
      this.sessionID = sessionID;
      this.resumeURL = checkedDiscordURL(resumeURL);
      return;
    }
    if (payload.t === "THREAD_CREATE") {
      this.rememberThread(data);
      return;
    }
    if (payload.t !== "MESSAGE_CREATE") return;
    const author = gatewayObject(data.author);
    const id = exactString(data.id);
    const channelID = exactString(data.channel_id);
    const guildID = exactString(data.guild_id) ?? null;
    if (author === undefined || id === undefined || channelID === undefined) return;
    if (this.seenMessageIDs.has(id)) return;
    try {
      const message: DiscordInboundMessage = {
        message_id: id,
        author_id: exactString(author.id) ?? "", author_is_bot: author.bot === true,
        guild_id: guildID, channel_id: channelID, parent_channel_id: this.threadParents.get(channelID) ?? null,
        content: exactString(data.content) ?? "",
        author_name: exactString(author.username) ?? "",
      };
      this.observe?.(message);
      if (this.accept()) await routeDiscordFreeText(this.policy, message, this.runMc, this.nudge, this.typing, this.threads);
      this.seenMessageIDs.add(id);
      if (this.seenMessageIDs.size > 4_096) this.seenMessageIDs.delete(this.seenMessageIDs.values().next().value!);
    } catch (err) {
      this.log.warn("discord.delivery_failed", `Discord inbound delivery refused: ${err instanceof Error ? err.message : String(err)}`);
    }
  }
}

// startDiscordSurface owns the production network loops. Each transport fault
// leaves durable outbox work unacked and retries with capped backoff; it never
// terminates the dispatch resident merely because Discord is unavailable.
export async function startDiscordSurface(
  policy: DiscordPolicy, runMc: Exec, log: Logger, mcHome: string, nudge: HomieNudge = noNudge,
  options: { preview?: boolean; observe?: (message: DiscordInboundMessage) => void } = {},
): Promise<DiscordSurfaceHandle> {
  const rest = nativeDiscordRest(policy);
  const filePlane = nativeDiscordFilePlane(mcHome);
  const typing = new DiscordTypingLoop(rest, log);
  const gatewayResponse = await fetch(`${discordAPI}/gateway/bot`, { headers: discordAuthorization(policy), signal: AbortSignal.timeout(10_000) });
  if (!gatewayResponse.ok) throw new Error(`Discord gateway discovery failed with HTTP ${gatewayResponse.status}`);
  const discovered = checkedDiscordURL((await gatewayResponse.json() as { url?: unknown }).url);
  let stopped = false;
  let active = !options.preview;
  const state = new DiscordGatewayState(policy, runMc, log, nudge, typing, options.observe, () => !stopped && active);
  let connected = false;
  let detail = "Culler checks the Discord connection.";
  let checkedAt = new Date().toISOString();
  let pump: Promise<void> = Promise.resolve();
  const inbound = new Set<Promise<void>>();
  let socket: WebSocket | undefined;
  let heartbeat: ReturnType<typeof setInterval> | undefined;
  let reconnect: ReturnType<typeof setTimeout> | undefined;
  let delay = 1_000;
  let outboxTimer: ReturnType<typeof setTimeout> | undefined;

  const clearSocketTimers = () => {
    if (heartbeat !== undefined) clearInterval(heartbeat);
    heartbeat = undefined;
  };
  const scheduleConnect = () => {
    if (stopped || reconnect !== undefined) return;
    const wait = delay;
    delay = Math.min(delay * 2, 60_000);
    reconnect = setTimeout(() => { reconnect = undefined; connect(); }, wait);
  };
  const connect = () => {
    if (stopped) return;
    try {
      socket = new WebSocket(state.gatewayURL(discovered));
      socket.onmessage = (event) => {
        const operation = (async () => {
          let payload: GatewayPayload;
          try {
            payload = JSON.parse(String(event.data)) as GatewayPayload;
            const action = await state.receive(socket!, payload);
            checkedAt = new Date().toISOString();
            if (payload.t === "READY" || payload.t === "RESUMED") { connected = true; detail = ""; }
            if (payload.op === 10) {
              const interval = (payload.d as { heartbeat_interval: number }).heartbeat_interval;
              heartbeat = setInterval(() => {
                if (!state.heartbeat(socket!)) socket!.close(4_000, "heartbeat unacknowledged");
              }, interval);
              delay = 1_000;
            }
            if (action === "reconnect") socket!.close(4_000, "Discord requested reconnect");
          } catch (err) {
            log.warn("discord.inbound_ignored", `Discord Gateway message refused: ${err instanceof Error ? err.message : String(err)}`);
            socket?.close(4_000, "invalid Gateway payload");
          }
        })();
        inbound.add(operation);
        void operation.finally(() => inbound.delete(operation));
      };
      socket.onclose = (event) => {
        connected = false;
        detail = event.code === 4014 ? "Enable Message Content Intent for the bot in Discord." : "The Discord receiver is unavailable. Check the connection again.";
        checkedAt = new Date().toISOString();
        clearSocketTimers(); scheduleConnect();
      };
      socket.onerror = () => { socket?.close(4_000, "Gateway error"); };
    } catch (err) {
      log.warn("discord.disconnected", `Discord Gateway connect failed: ${err instanceof Error ? err.message : String(err)}`);
      scheduleConnect();
    }
  };
  const pumpOutbox = async (): Promise<void> => {
    if (stopped) return;
    if (!connected) {
      outboxTimer = setTimeout(() => { pump = pumpOutbox(); }, 1_000);
      return;
    }
    let wait = 5_000;
    try {
      const delivered = await drainDiscordOutbox(policy, runMc, rest, filePlane, typing, () => !stopped);
      if (delivered > 0) wait = 250;
      if (connected) detail = "";
    } catch (err) {
      log.warn("outbox.drain_failed", `Discord outbox retry: ${err instanceof Error ? err.message : String(err)}`);
      detail = "Culler could not deliver a queued Discord message. Culler will retry. You can send a test message.";
      wait = 15_000;
    }
    if (!stopped) outboxTimer = setTimeout(() => { pump = pumpOutbox(); }, wait);
  };
  connect();
  if (active) pump = pumpOutbox();
  return {
    activate() { if (!active && !stopped) { active = true; pump = pumpOutbox(); } },
    async stop() {
      stopped = true;
      typing.stop();
      clearSocketTimers();
      if (reconnect !== undefined) clearTimeout(reconnect);
      if (outboxTimer !== undefined) clearTimeout(outboxTimer);
      socket?.close(1_000, "resident shutdown");
      await Promise.allSettled([pump, ...inbound]);
    },
    async ready() {
      const deadline = Date.now() + 12_000;
      while (!connected && !stopped && Date.now() < deadline) await Bun.sleep(50);
      if (!connected) throw new Error(detail);
    },
    health() { return { connected: connected && !stopped, detail, checked_at: checkedAt }; },
  };
}
