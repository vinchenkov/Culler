// collie-runner/main.ts — the lease-free Collie conversation runner. It runs
// inside a tier:"collie" container the resident woke and bound. Unlike the
// pipeline agent-runner it:
//   - never heartbeats and holds no lease;
//   - lives for the session: it loops claim → run one harness turn → post the
//     reply, and polls for the next turn until the resident stops the
//     container on the session idle timeout;
//   - reports `mc collie runner-started` once at boot with its own launch and
//     64-hex container id (both carried in the republished run.json). A fenced
//     report means this launch was superseded: exit immediately.
//
// The loop's spine is two host verbs: `mc collie claim` (atomically take the
// oldest pending inbound turn — durable, so a fresh runner resumes a held
// turn) and `mc collie reply --to <id>` (append the reply, complete the claim,
// fan the outbox). While the harness turn runs, `mc collie draft --to <id>`
// carries the assistant text so far as a provisional reply, at most once per
// second; the reply supersedes it. The pipeline runner's adapter selection,
// by the harness family in the envelope, owns the fresh/native harness
// mechanics; rows fallback remains a separate arm.
//
// In a packet conversation, the first turn of each native session also
// carries the packet context that `mc collie packet-context` builds.

import { modelResponseReporter } from "../shared/model-response";
import { openCodeTraceID, OPENCODE_DATABASE } from "../shared/opencode-trace";
import { makeLogger, parseLevel, type Logger } from "../shared/log";
import { runLogSink } from "../shared/log-sink";
import {
  adapterInvocation,
  type AdapterInvocation,
  type RunEnvelope,
  type RunEnvelopeRuntime,
} from "../agent-runner/main";
import { readAdapterFailure, type AdapterFailure } from "../agent-runner/diagnostics";
import { installEnvelopeSkills } from "../agent-runner/skills";
import { createHash } from "node:crypto";
import { constants } from "node:fs";
import { lstat, open, realpath } from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";

const RUN_JSON = process.env["MC_RUN_JSON"] || "/mc/run.json";
const INBOUND_ATTACHMENT_ROOT = "/mc/attachments/in";
const DECIMAL_ID = /^[1-9][0-9]{0,18}$/;
const HEX_64 = /^[0-9a-f]{64}$/;
const SQLITE_TIME = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/;
const MAX_INT64 = 9_223_372_036_854_775_807n;
const MAX_ATTACHMENT_BYTES = 256 * 1024 * 1024;
const MAX_ATTACHMENT_BATCH = 512 * 1024 * 1024;

/** Delay between empty polls. */
const DEFAULT_POLL_MS = 1000;
/** The session idle timeout when the envelope carries none (an older `mc`). */
const DEFAULT_IDLE_TIMEOUT_S = 1800;
/** Time past the idle timeout before the runner exits on its own. The resident
 * normally stops the container first; this bounds a container whose stop never
 * arrives. Two tick intervals. */
export const IDLE_EXIT_MARGIN_S = 120;
/** Warnings for a failing claim are at least this far apart. */
export const CLAIM_WARN_INTERVAL_MS = 60_000;
/** The refusal code `mc collie claim` returns on a session that is not active.
 * Mirrors mc/domain CodeSessionInactive. */
export const SESSION_INACTIVE_CODE = "session-inactive";
/** The host rejects this launch when its profile or connection changes. */
export const RUNTIME_CHANGED_CODE = "runtime-changed";
/** The line that marks the end of each harness turn on standard output. */
export const TURN_ENDED_LINE = "collie-turn-ended";

/** The least time between two draft writes for one turn. */
export const DRAFT_INTERVAL_MS = 1000;

/** The collie launch envelope (a subset of run.json). Written by the resident's
 * wake effector and republished with container_id before start. */
export interface CollieEnvelope {
  run_id: string;
  session_id: string;
  vision_project_id?: string;
  /** The packet of a packet conversation. Each other conversation has no packet. */
  packet?: { packet_id: number; version_id: number; task_id: number; project_id: string };
  tier: "collie";
  directive: string;
  launch: string;
  container_id: string;
  /** Historical route key "harness/model_binding" (e.g. "fake/fake"). */
  binding: string;
  harness: string;
  model_binding: string;
  mode: "fresh" | "native" | "rows";
  session_path: string;
  native_session_ref?: string;
  trace_filename?: string;
  /** The Collie tier's skill set: its command surface first, then the
   * operator's Collie skills. Optional for the same reason the pipeline
   * envelope's is: an older `mc` carries none. */
  skills?: { name: string; body: string }[];
  /** What the host skill store could not deliver; written into the run's log. */
  skill_problems?: string[];
  harness_config?: { behavior: string };
  runtime?: RunEnvelopeRuntime;
  /** The session idle timeout in seconds. Absent from an older `mc`. */
  idle_timeout_s?: number;
  mounts: {
    session: string;
    /** The harness's working directory. The Project folders of a Collie are
     * read-only, so the resident hands it a writable per-launch scratch. */
    workspace?: string;
    attachments: {
      inbound: "/mc/attachments/in";
      outbound: "/mc/private/attachments/out";
    };
  };
}

/** One claimed inbound turn (the `message` field of `mc collie claim`). */
export interface ClaimedAttachment {
  ordinal: number;
  session_id: string;
  direction: "in";
  sha256: string;
  size: number;
  media_type: string;
  display_name: string;
}

export interface VisionContext {
  project_id: string;
  title: string;
  workspace_has_files: boolean;
  workspace_vision_status: string;
  draft: { id: string; content: string; revision: string };
}

export interface ClaimedMessage {
  vision_context?: VisionContext;
  attempt_id?: string;
  id: string;
  seq: string;
  surface: "discord" | "dashboard" | "cli";
  channel_ref: string | null;
  body: string;
  attachments: ClaimedAttachment[];
  created_at: string;
  claimed_at: string;
  reclaimed: boolean;
}

/** One harness turn's outcome. `reply` is null when the harness produced no
 * completed turn (crash/hang/failure). `nativeSessionId` is the harness's own
 * session id from its session-start event, present the first time it is seen —
 * the loop registers it once as the session's native locator. */
export interface TurnResult {
  failure?: AdapterFailure;
  reply: string | null;
  nativeSessionId?: string;
  nativeFile?: string;
}

/** One `mc` call's outcome: the stdout object, or the refusal's error
 * envelope. `code` is empty when the refusal carried no envelope. */
export type McOutcome =
  | { ok: true; value: Record<string, unknown> }
  | { ok: false; exit: number; code: string; message: string };

/** Injectable seams so the loop is testable without real subprocesses. */
export interface RunnerDeps {
  /** Run `mc <argv>` and parse its stdout as JSON (null when not JSON). */
  mcJSON: (argv: string[]) => Promise<Record<string, unknown> | null>;
  /** Run `mc <argv>` and keep the refusal's code and message. */
  mcCall: (argv: string[]) => Promise<McOutcome>;
  /** Run ONE harness turn for `turn`; resolves the reply + observed native id.
   * `onText` receives each piece of assistant text as the stream carries it. */
  runHarnessTurn: (
    run: CollieEnvelope,
    turn: string,
    onText: (text: string) => void,
    onModelResponse: () => void,
    onNativeSession: (session: string, file: string) => Promise<void>,
  ) => Promise<TurnResult>;
  /** Resolve and verify the claim's fixed file-plane references. */
  resolveTurn: (message: ClaimedMessage) => Promise<string>;
  /** Marks the end of one harness turn on standard output. The resident
   * follows the container output and starts each repository operation that
   * the turn made due. */
  turnEnded: () => void;
  sleep: (ms: number) => Promise<void>;
  /** Wall clock in milliseconds. */
  now: () => number;
  log: Logger;
  pollMs: number;
}

/** Milliseconds without a message after which the runner exits on its own. */
export function idleExitMs(run: CollieEnvelope): number {
  const timeout = Number.isSafeInteger(run.idle_timeout_s) && run.idle_timeout_s! > 0
    ? run.idle_timeout_s!
    : DEFAULT_IDLE_TIMEOUT_S;
  return (timeout + IDLE_EXIT_MARGIN_S) * 1000;
}

/** The throttled draft writer for one turn. `push` appends assistant text and
 * never blocks the stream: a write starts at once when a second has passed
 * since the last one, else one trailing write is queued for the remainder.
 * The first failed write stops drafts for the turn; the reply still follows.
 * `stop` waits for the write in flight so it cannot race the reply. */
export function makeDraftWriter(
  deps: Pick<RunnerDeps, "mcJSON" | "sleep" | "now" | "log">,
  sessionID: string,
  turnID: string,
  attemptID?: string,
): { push: (text: string) => void; stop: () => Promise<void> } {
  let text = "";
  let lastWrite = Number.NEGATIVE_INFINITY;
  let queued = false;
  let stopped = false;
  let refused = false;
  let chain: Promise<void> = Promise.resolve();
  const write = async (): Promise<void> => {
    const wait = lastWrite + DRAFT_INTERVAL_MS - deps.now();
    if (wait > 0) await deps.sleep(wait);
    queued = false;
    if (stopped) return;
    lastWrite = deps.now();
    let posted: Record<string, unknown> | null = null;
    try {
      posted = await deps.mcJSON(["collie", "draft", sessionID, "--to", turnID, "--body", text, ...(attemptID ? ["--attempt", attemptID] : [])]);
    } catch {
      posted = null;
    }
    if (posted === null && !refused) {
      refused = true;
      stopped = true;
      deps.log.info("collie.draft_stopped", `turn ${turnID}: draft write refused; drafts stop for this turn`,
        { turn: turnID });
    }
  };
  return {
    push(piece) {
      if (stopped || piece === "") return;
      text += piece;
      if (queued) return;
      queued = true;
      chain = chain.then(write);
    },
    async stop() {
      stopped = true;
      await chain;
    },
  };
}

/** The trace file every harness writes into the session folder. */
export const NATIVE_FILENAME = "native.jsonl";

/** Read the refusal out of a non-zero `mc` exit. The envelope on stdout is
 * `{error:{code,message}}`; without one, stderr is the message. */
export function parseRefusal(
  exit: number,
  stdout: string,
  stderr: string,
): { exit: number; code: string; message: string } {
  try {
    const error = record(record(JSON.parse(stdout))?.["error"]);
    if (error !== undefined && typeof error.code === "string" &&
        typeof error.message === "string") {
      return { exit, code: error.code, message: error.message };
    }
  } catch {
    // Not an envelope: fall through to stderr.
  }
  return { exit, code: "", message: stderr.trim() };
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined;
}

function closedKeys(value: Record<string, unknown>, expected: string[]): boolean {
  return JSON.stringify(Object.keys(value).sort()) === JSON.stringify([...expected].sort());
}

function canonicalDecimalID(value: unknown): value is string {
  return typeof value === "string" && DECIMAL_ID.test(value) && BigInt(value) <= MAX_INT64;
}

function safeMediaType(value: unknown): value is string {
  return typeof value === "string" && value.length >= 1 && value.length <= 255 &&
    /^[\x20-\x7e]+$/.test(value);
}

function safeDisplayName(value: unknown): value is string {
  if (typeof value !== "string" || value.includes("/") || value.includes("\\") ||
      new TextEncoder().encode(value).length < 1 ||
      new TextEncoder().encode(value).length > 255) {
    return false;
  }
  for (const character of value) {
    const code = character.codePointAt(0)!;
    if (code < 0x20 || code === 0x7f) return false;
  }
  return true;
}

/** Parse the closed private claim frame before any value reaches a harness. */
export function parseClaimedMessage(value: unknown, sessionID: string): ClaimedMessage | null {
  if (value === null) return null;
  const message = record(value);
  if (message === undefined || !closedKeys(message, [
    "attachments", "body", "channel_ref", "claimed_at", "created_at",
    "id", "reclaimed", "seq", "surface", ...(message.vision_context === undefined ? [] : ["vision_context"]), ...(message.attempt_id === undefined ? [] : ["attempt_id"]),
  ]) ||
      !canonicalDecimalID(message.id) || !canonicalDecimalID(message.seq) ||
      (message.attempt_id !== undefined && (typeof message.attempt_id !== "string" || !/^[0-9a-f]{32}$/.test(message.attempt_id))) ||
      (message.surface !== "discord" && message.surface !== "dashboard" &&
        message.surface !== "cli") ||
      (message.channel_ref !== null &&
        (typeof message.channel_ref !== "string" || message.channel_ref === "")) ||
      typeof message.body !== "string" ||
      !SQLITE_TIME.test(String(message.created_at)) ||
      !SQLITE_TIME.test(String(message.claimed_at)) ||
      typeof message.reclaimed !== "boolean" ||
      !Array.isArray(message.attachments) || message.attachments.length > 32) {
    throw new Error("Collie claim returned a malformed message");
  }
  if (message.vision_context !== undefined) {
    const vision = record(message.vision_context);
    const draft = record(vision?.draft);
    if (vision === undefined || draft === undefined ||
        typeof vision.project_id !== "string" || !/^[A-Za-z0-9_-]+$/.test(vision.project_id) ||
        typeof vision.title !== "string" || typeof vision.workspace_has_files !== "boolean" ||
        typeof vision.workspace_vision_status !== "string" || draft.id !== vision.project_id ||
        typeof draft.content !== "string" || typeof draft.revision !== "string" || draft.revision === "") {
      throw new Error("The Vision context is invalid.");
    }
  }
  const attachments: ClaimedAttachment[] = [];
  let total = 0;
  for (const value of message.attachments) {
    const attachment = record(value);
    if (attachment === undefined || !closedKeys(attachment, [
      "direction", "display_name", "media_type", "ordinal",
      "session_id", "sha256", "size",
    ]) ||
        attachment.ordinal !== attachments.length + 1 ||
        attachment.session_id !== sessionID || attachment.direction !== "in" ||
        typeof attachment.sha256 !== "string" || !HEX_64.test(attachment.sha256) ||
        !Number.isSafeInteger(attachment.size) ||
        (attachment.size as number) < 0 ||
        (attachment.size as number) > MAX_ATTACHMENT_BYTES ||
        !safeMediaType(attachment.media_type) ||
        !safeDisplayName(attachment.display_name)) {
      throw new Error("Collie claim returned a malformed attachment identity");
    }
    total += attachment.size as number;
    if (total > MAX_ATTACHMENT_BATCH) {
      throw new Error("Collie claim returned an over-budget attachment set");
    }
    attachments.push(attachment as unknown as ClaimedAttachment);
  }
  return { ...message, attachments } as unknown as ClaimedMessage;
}

export function visionTurnContext(vision: VisionContext): string {
  return "# Project Vision context\n\n" +
    "This conversation develops the selected Project draft. The context describes the current draft before this turn.\n" +
    "The workspace is read-only at /workspaces/" + vision.project_id + ".\n" +
    "Use the current draft revision for each edit. An edit changes Culler storage only.\n" +
    "The operator uses Save Vision to write VISION.md. Repository evidence does not establish the operator’s goals.\n\n" +
    JSON.stringify(vision, null, 2);
}

/** This function builds the model input of one turn. `packetContext` is present
 * only at the first turn of each native session of a packet conversation. */
export function collieTurnPrompt(
  run: CollieEnvelope,
  message: ClaimedMessage,
  turn: string,
  packetContext?: string,
): string {
  return `${run.directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability. The ready state identifies a current copy. The delayed state identifies an older valid copy. The pending state requires a later read. The unavailable state identifies an absent valid copy.\n\nOpenCode copies use SQLite databases and native session references. The mc-collie skill describes the runtime reader. Cite each native message or tool identifier with its database copy and session. The status reports AgentsView import separately.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\n` +
    `Conversation identifier: ${run.session_id}\nOperator message sequence: ${message.seq}\n\n` +
    (packetContext === undefined ? "" : packetContext + "\n\n") +
    (message.vision_context === undefined ? "" : visionTurnContext(message.vision_context) + "\n\n") +
    `# Operator message\n\n${turn}`;
}

/** Read the packet context of a packet conversation. The result must name this
 * session and its packet exactly. A refusal or another result gives undefined.
 * The log keeps the reason of a refusal. */
async function readPacketContext(
  run: CollieEnvelope,
  packet: NonNullable<CollieEnvelope["packet"]>,
  deps: Pick<RunnerDeps, "mcCall" | "log">,
): Promise<string | undefined> {
  const argv = ["collie", "packet-context", run.session_id];
  const outcome = await deps.mcCall(argv);
  if (!outcome.ok) {
    deps.log.warn("runner.mc_failed", `mc ${argv.join(" ")} exited ${outcome.exit}: ${outcome.message}`,
      { verb: "collie", exit: outcome.exit, code: outcome.code });
    return undefined;
  }
  const value = record(outcome.value);
  if (value === undefined ||
      !closedKeys(value, ["context", "packet_id", "session_id", "version_id"]) ||
      value.session_id !== run.session_id || value.packet_id !== packet.packet_id ||
      value.version_id !== packet.version_id ||
      typeof value.context !== "string" || value.context === "") {
    return undefined;
  }
  return value.context;
}

export function inboundAttachmentPath(root: string, digest: string): string {
  if (!isAbsolute(root) || !HEX_64.test(digest)) {
    throw new Error("attachment claim cannot derive its fixed inbound path");
  }
  return join(root, "sha256", digest.slice(0, 2), digest);
}

async function requireDirectory(path: string): Promise<void> {
  const stat = await lstat(path);
  if (!stat.isDirectory() || stat.isSymbolicLink()) {
    throw new Error(`attachment ancestor is not a real directory: ${path}`);
  }
}

async function requireAbsoluteDirectoryChain(path: string): Promise<void> {
  let cursor = "/";
  await requireDirectory(cursor);
  for (const part of path.split("/").filter((value) => value !== "")) {
    cursor = join(cursor, part);
    await requireDirectory(cursor);
  }
}

async function verifyClaimedAttachment(
  claim: ClaimedAttachment,
  root: string,
): Promise<string> {
  const path = inboundAttachmentPath(root, claim.sha256);
  const normalizedRoot = resolve(root);
  if (resolve(path) !== path || !path.startsWith(normalizedRoot + "/")) {
    throw new Error("attachment claim escaped its fixed inbound root");
  }
  await requireAbsoluteDirectoryChain(normalizedRoot);
  await requireDirectory(join(normalizedRoot, "sha256"));
  await requireDirectory(join(normalizedRoot, "sha256", claim.sha256.slice(0, 2)));
  const leaf = await lstat(path);
  if (!leaf.isFile() || leaf.isSymbolicLink() || leaf.size !== claim.size ||
      await realpath(path) !== path) {
    throw new Error(`attachment claim does not name its exact regular file: ${claim.ordinal}`);
  }

  const handle = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const before = await handle.stat({ bigint: true });
    if (!before.isFile() || before.size !== BigInt(claim.size)) {
      throw new Error(`attachment claim changed before verification: ${claim.ordinal}`);
    }
    const digest = createHash("sha256");
    const buffer = Buffer.allocUnsafe(64 * 1024);
    let offset = 0;
    while (offset < claim.size) {
      const length = Math.min(buffer.length, claim.size - offset);
      const { bytesRead } = await handle.read(buffer, 0, length, offset);
      if (bytesRead <= 0) {
        throw new Error(`attachment claim ended before its declared size: ${claim.ordinal}`);
      }
      digest.update(buffer.subarray(0, bytesRead));
      offset += bytesRead;
    }
    const after = await handle.stat({ bigint: true });
    if (before.dev !== after.dev || before.ino !== after.ino ||
        before.size !== after.size || before.mtimeNs !== after.mtimeNs ||
        digest.digest("hex") !== claim.sha256) {
      throw new Error(`attachment claim content or identity drifted: ${claim.ordinal}`);
    }
  } finally {
    await handle.close();
  }
  return path;
}

/** Verify every typed identity, then expose only derived read-only paths. */
export async function resolveClaimedTurn(
  message: ClaimedMessage,
  root = INBOUND_ATTACHMENT_ROOT,
): Promise<string> {
  if (message.attachments.length === 0) return message.body;
  const attachments = [];
  for (const claim of message.attachments) {
    attachments.push({
      ...claim,
      path: await verifyClaimedAttachment(claim, root),
    });
  }
  return [
    "Culler supplied this complete turn as canonical JSON.",
    "Attachment paths are immutable, read-only file-plane references.",
    JSON.stringify({ version: 1, body: message.body, attachments }),
  ].join("\n");
}

/** Incremental line splitter for the harness stdout event stream. */
export function makeLineSplitter(onLine: (line: string) => void): (chunk: string) => void {
  let buf = "";
  return (chunk: string) => {
    buf += chunk;
    for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
      onLine(buf.slice(0, i));
      buf = buf.slice(i + 1);
    }
  };
}

/** Scan a harness event line for a successful turn-complete's output. Returns
 * the output string, or undefined for any other line. A failed/absent
 * completion (crash/hang) simply never yields one. */
export function replyFromEvent(line: string): string | undefined {
  let ev: { event?: string; status?: string; output?: unknown };
  try {
    ev = JSON.parse(line) as typeof ev;
  } catch {
    return undefined;
  }
  if (ev.event === "turn-complete" && ev.status === "success" && typeof ev.output === "string") {
    return ev.output;
  }
  return undefined;
}

/** Scan a harness event line for a piece of assistant text. Returns the
 * text, or undefined for any other line. */
export function textFromEvent(line: string): string | undefined {
  let ev: { event?: string; text?: unknown };
  try {
    ev = JSON.parse(line) as typeof ev;
  } catch {
    return undefined;
  }
  if (ev.event === "turn-text" && typeof ev.text === "string" && ev.text !== "") {
    return ev.text;
  }
  return undefined;
}

/** Scan a harness event line for the session-start's native session id. */
export function nativeIdFromEvent(line: string): string | undefined {
  let ev: { event?: string; session_id?: unknown };
  try {
    ev = JSON.parse(line) as typeof ev;
  } catch {
    return undefined;
  }
  if (ev.event === "session-start" && typeof ev.session_id === "string") {
    return ev.session_id;
  }
  return undefined;
}

/** Extract and validate the exact relative native trace locator normalized by
 * an adapter. Non-session events return undefined; a malformed session-start
 * is a protocol error, never guessed. */
export function nativeLocatorFromEvent(
  line: string,
  fallback?: string,
): { sessionId: string; nativeFile: string } | undefined {
  let ev: { event?: string; session_id?: unknown; native_file?: unknown };
  try {
    ev = JSON.parse(line) as typeof ev;
  } catch {
    return undefined;
  }
  if (ev.event !== "session-start") return undefined;
  if (typeof ev.session_id !== "string" || ev.session_id === "") {
    throw new Error("adapter session-start omitted its native session id");
  }
  const file = typeof ev.native_file === "string" && ev.native_file !== ""
    ? ev.native_file
    : fallback;
  if (!file || file.startsWith("/") ||
    file.split("/").some((part) => part === "" || part === "." || part === "..")) {
    throw new Error("adapter session-start omitted a safe native trace locator");
  }
  return { sessionId: ev.session_id, nativeFile: file };
}

/** The lease-free claim→reply loop. Returns the process exit code. */
export async function runCollieLoop(run: CollieEnvelope, deps: RunnerDeps): Promise<number> {
  if (typeof run.directive !== "string" || run.directive.trim() === "") {
    deps.log.error("runner.started", "The Collie directive is required");
    return 2;
  }
  if (run.mode === "native" && (!run.native_session_ref || !run.trace_filename)) {
    deps.log.error("runner.started", "native Collie launch omitted its immutable locator pair");
    return 2;
  }
  // Boot fence: report runner-started once. A fenced report means a newer
  // launch superseded this one (or the session ended) — this runner is a
  // zombie; exit cleanly and let the --rm container disappear.
  const started = await deps.mcJSON([
    "collie", "runner-started", run.session_id,
    "--launch", run.launch, "--container-id", run.container_id,
  ]);
  if (started !== null && started["fenced"] === false) {
    deps.log.info("runner.exited", `runner-started fenced (launch ${run.launch} superseded); exiting`,
      { launch: run.launch, fenced: true });
    return 0;
  }

  const exitAfterMs = idleExitMs(run);
  let lastMessageAt = deps.now();
  let claimWarnedAt: number | undefined;
  let claimFailures = 0;
  let registered = false;
  let turnRun = { ...run };
  let packetContext: string | undefined;
  for (;;) {
    const outcome = await deps.mcCall(["collie", "claim", run.session_id]);
    let claimed: Record<string, unknown> | null = null;
    if (outcome.ok) {
      claimed = outcome.value;
    } else if (outcome.code === SESSION_INACTIVE_CODE) {
      // The session ended under this runner. There is nothing left to claim.
      deps.log.info("runner.exited", `session ${run.session_id} is not active (${outcome.message}); exiting`,
        { session: run.session_id, code: outcome.code });
      return 0;
    } else if (outcome.code === RUNTIME_CHANGED_CODE) {
      deps.log.info("runner.exited", "The agent profile or connection changed. This container will exit so the resident can start a replacement.",
        { session: run.session_id, launch: run.launch, code: outcome.code });
      return 0;
    } else {
      // Any other refusal is treated as transient: keep polling, but do not
      // write one warning per poll.
      claimFailures += 1;
      const at = deps.now();
      if (claimWarnedAt === undefined || at - claimWarnedAt >= CLAIM_WARN_INTERVAL_MS) {
        deps.log.warn("runner.mc_failed", `mc collie claim exited ${outcome.exit}: ${outcome.message}`,
          { verb: "collie", exit: outcome.exit, code: outcome.code, failures: claimFailures });
        claimWarnedAt = at;
        claimFailures = 0;
      }
    }
    let message: ClaimedMessage | null;
    try {
      message = parseClaimedMessage(claimed?.["message"] ?? null, run.session_id);
    } catch (err) {
      deps.log.error("runner.turn_started", err instanceof Error ? err.message : String(err));
      return 1;
    }
    if (message === null) {
      const idleMs = deps.now() - lastMessageAt;
      if (idleMs >= exitAfterMs) {
        deps.log.info("collie.idle_exit", `no message for ${Math.round(idleMs / 1000)}s; exiting`,
          { idle_s: Math.round(idleMs / 1000), limit_s: exitAfterMs / 1000 });
        return 0;
      }
      await deps.sleep(deps.pollMs);
      continue;
    }
    lastMessageAt = deps.now();

    // A harness turn without the native reference starts a new native session.
    // That session has no earlier packet context.
    const withPacketContext = run.packet !== undefined && turnRun.native_session_ref === undefined;
    let turn: string;
    try {
      turn = await deps.resolveTurn(message);
      if (run.vision_project_id !== message.vision_context?.project_id) {
        throw new Error("The Vision context does not match this conversation.");
      }
      if (withPacketContext && packetContext === undefined) {
        packetContext = await readPacketContext(run, run.packet!, deps);
        if (packetContext === undefined) {
          throw new Error("The packet context does not match this conversation");
        }
      }
    } catch (err) {
      deps.log.error("runner.turn_started", `turn ${message.id}: ${
        err instanceof Error ? err.message : String(err)
      }; leaving it claimed`, { turn: message.id });
      return 1;
    }
    const drafts = makeDraftWriter(deps, run.session_id, message.id, message.attempt_id);
    const recovery = modelResponseReporter(
      async () => message.attempt_id !== undefined &&
        (await deps.mcCall(["collie", "model-response", message.attempt_id])).ok,
      () => deps.log.warn("runner.recovery_unconfirmed", "The connection recovery report failed."),
    );
    const registerNative = async (nativeSessionId: string, nativeFile: string) => {
      if ((turnRun.native_session_ref !== undefined && turnRun.native_session_ref !== nativeSessionId) ||
          (turnRun.trace_filename !== undefined && turnRun.trace_filename !== nativeFile &&
            !(turnRun.harness === "opencode" && nativeFile === OPENCODE_DATABASE &&
              turnRun.trace_filename === `rollout-${openCodeTraceID(nativeSessionId)}.jsonl`))) {
        throw new Error("The harness changed the native session identity.");
      }
      if (!registered) {
        const result = await deps.mcJSON([
          "run", "register-session", run.session_id, "--native-ref", nativeSessionId, "--file", nativeFile,
        ]);
        if (result === null) throw new Error("The native session reference could not become durable.");
        registered = true;
      }
      turnRun = { ...turnRun, mode: "native", native_session_ref: nativeSessionId, trace_filename: nativeFile };
    };
    const result = await deps.runHarnessTurn(
      turnRun, collieTurnPrompt(run, message, turn, withPacketContext ? packetContext : undefined), drafts.push,
      () => recovery.observe({ event: "model-response" }), registerNative,
    );
    await recovery.flush();
    deps.turnEnded();
    await drafts.stop();
    if (!registered && result.nativeSessionId !== undefined) {
      try { await registerNative(result.nativeSessionId, result.nativeFile ?? NATIVE_FILENAME); }
      catch (error) {
        deps.log.error("runner.session_lost", error instanceof Error ? error.message : String(error), { turn: message.id });
        return 1;
      }
    }
    if (result.reply === null) {
      if (message.attempt_id) await deps.mcJSON(["collie", "reply-failure", message.attempt_id,
        "--condition", "The harness ended without a reply.",
        ...(result.failure?.proxy_request_id ? ["--proxy-request-id", result.failure.proxy_request_id] : []),
        ...(result.failure?.http_status ? ["--http-status", String(result.failure.http_status)] : []),
        ...(result.failure?.provider_error_code ? ["--provider-error-code", result.failure.provider_error_code] : []),
        ...(result.failure?.network_error ? ["--network-error", result.failure.network_error] : [])]);
      // The turn stays claimed (durable): a fresh runner will reclaim it. A
      // broken harness must not spin the loop, so exit non-zero as evidence.
      deps.log.error("runner.harness_failed", `turn ${message.id}: harness produced no reply; leaving it claimed`,
        { turn: message.id });
      return 1;
    }
    const posted = await deps.mcJSON([
      "collie", "reply", run.session_id, "--to", String(message.id), "--body", result.reply, ...(message.attempt_id ? ["--attempt", message.attempt_id] : []),
    ]);
    if (posted === null) {
      if (message.attempt_id) await deps.mcJSON(["collie", "reply-failure", message.attempt_id,
        "--condition", "The reply could not become durable."]);
      deps.log.error("runner.turn_completed", `turn ${message.id}: reply post failed; leaving the turn claimed`,
        { turn: message.id });
      return 1;
    }
    lastMessageAt = deps.now();
  }
}

// The resident binds a per-run host directory at /mc/logs, so these records
// outlive the container — every run container is `--rm` and nothing reads its
// output, so before this a runner's account of what it did was destroyed the
// moment it finished.
//
// stderr when the bind is absent, which is every container the resident did not
// compose: the e2e lanes, a hand-run image, a deployment mid-upgrade. Same
// format either way, so `docker logs` and the host file read alike.
const log = makeLogger({
  comp: "collie-runner",
  level: parseLevel(process.env["MC_LOG_LEVEL"]),
  sink: runLogSink(),
});

/** Run `mc` and keep its outcome. The verbs return a JSON object on success
 * and an error envelope on a refusal. Nothing is logged here. */
async function mcCall(argv: string[]): Promise<McOutcome> {
  const proc = Bun.spawn(["mc", ...argv], { stdin: "ignore", stdout: "pipe", stderr: "pipe" });
  const [code, out, err] = await Promise.all([
    proc.exited,
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
  ]);
  if (code !== 0) {
    return { ok: false, ...parseRefusal(code, out, err) };
  }
  try {
    return { ok: true, value: JSON.parse(out) as Record<string, unknown> };
  } catch {
    return out.trim() === ""
      ? { ok: true, value: {} }
      : { ok: false, exit: 0, code: "", message: "mc wrote no JSON object" };
  }
}

/** Run `mc` and parse stdout JSON; a non-zero exit or non-JSON stdout is null.
 * A non-zero exit is logged. */
async function mcJSON(argv: string[]): Promise<Record<string, unknown> | null> {
  const outcome = await mcCall(argv);
  if (outcome.ok) return outcome.value;
  if (outcome.exit !== 0) {
    log.warn("runner.mc_failed", `mc ${argv.join(" ")} exited ${outcome.exit}: ${outcome.message}`,
      { verb: argv[0] ?? "", exit: outcome.exit, code: outcome.code });
  }
  return null;
}

/** Convert the Collie envelope to the pipeline runner's closed adapter input.
 * The historical binding is restated and must agree with its split fields. */
export function collieAdapterInvocation(run: CollieEnvelope): AdapterInvocation {
  if (run.binding !== `${run.harness}/${run.model_binding}`) {
    throw new Error("Collie historical binding disagrees with its adapter fields");
  }
  if (run.mounts.attachments?.inbound !== "/mc/attachments/in" ||
      run.mounts.attachments?.outbound !== "/mc/private/attachments/out") {
    throw new Error("Collie attachment mount contract is absent or malformed");
  }
  if (run.mode === "rows") {
    throw new Error("rows-mode Collie fallback is a separate unimplemented arm");
  }
  // Refuse rather than inherit the shared adapter's default of
  // `/workspace/source`. That default is the PIPELINE's, where the Worker binds
  // that path RW; on this tier every workspace is read-only, so the Collie
  // would have no writable working directory.
  if (run.mounts.workspace === undefined) {
    throw new Error("a Collie launch envelope must name its own writable workspace cwd");
  }
  const adapterRun: RunEnvelope = {
    run_id: run.run_id,
    role: "collie",
    harness: run.harness,
    model_binding: run.model_binding,
    subject_id: null,
    heartbeat_interval_s: 0,
    brief: "",
    mode: run.mode,
    native_session_ref: run.native_session_ref,
    harness_config: run.harness_config,
    runtime: run.runtime,
    mounts: {
      session: run.mounts.session,
      workspace: run.mounts.workspace,
    },
  };
  return adapterInvocation(adapterRun);
}

/** Production harness turn: select one fake or real adapter through the shared
 * closed catalog, feed the operator turn, and consume only its normalized
 * session-start / turn-text / turn-complete events. */
async function runHarnessTurn(
  run: CollieEnvelope,
  turn: string,
  onText: (text: string) => void,
  onModelResponse: () => void,
  onNativeSession: (session: string, file: string) => Promise<void>,
): Promise<TurnResult> {
  let invocation: AdapterInvocation;
  try {
    invocation = collieAdapterInvocation(run);
  } catch (err) {
    log.error("adapter.launch_failed", err instanceof Error ? err.message : String(err));
    return { reply: null };
  }
  const proc = Bun.spawn(
    invocation.argv,
    { stdin: new TextEncoder().encode(turn), stdout: "pipe", stderr: "inherit" },
  );
  let reply: string | undefined;
  let nativeSessionId: string | undefined;
  let nativeFile: string | undefined;
  let protocolError = false;
  let failure: AdapterFailure | undefined;
  let nativeRegistration = Promise.resolve();
  const split = makeLineSplitter((line) => {
    try {
      const event = JSON.parse(line);
      failure = readAdapterFailure(event) ?? failure;
      if (event.event === "model-response") onModelResponse();
    } catch {}
    console.log(line); // mirror to docker logs
    const text = textFromEvent(line);
    if (text !== undefined) onText(text);
    const out = replyFromEvent(line);
    if (out !== undefined) reply = out;
    try {
      const locator = nativeLocatorFromEvent(line, invocation.defaultNativeFile);
      if (locator !== undefined) {
        nativeSessionId = locator.sessionId;
        nativeFile = locator.nativeFile;
        nativeRegistration = nativeRegistration.then(() => onNativeSession(locator.sessionId, locator.nativeFile))
          .catch(error => {
            protocolError = true;
            proc.kill();
            log.error("runner.session_lost", error instanceof Error ? error.message : String(error));
          });
      }
    } catch (err) {
      protocolError = true;
      log.error("adapter.stream_error", err instanceof Error ? err.message : String(err));
    }
  });
  const decoder = new TextDecoder();
  for await (const chunk of proc.stdout) {
    split(decoder.decode(chunk, { stream: true }));
  }
  const code = await proc.exited;
  await nativeRegistration;
  if (code !== 0 || protocolError || reply === undefined) {
    log.info("runner.turn_completed", `harness turn exited ${code}${reply === undefined ? " (no turn-complete)" : ""}`,
      { exit: code, replied: reply !== undefined });
    return { reply: null, nativeSessionId, nativeFile, failure };
  }
  return { reply, nativeSessionId, nativeFile };
}

async function main(): Promise<number> {
  const run = (await Bun.file(RUN_JSON).json()) as CollieEnvelope;
  log.info("runner.started", `collie run ${run.session_id} binding=${run.binding} mode=${run.mode}`,
    { session: run.session_id, binding: run.binding, mode: run.mode });
  // Before the first turn, so the harness discovers it in its own startup scan.
  // The container is rebuilt on every wake, so installing once here covers it.
  await installEnvelopeSkills(run.skills, run.skill_problems, log.child({ session: run.session_id }));
  try {
    collieAdapterInvocation(run);
  } catch (err) {
    log.error("adapter.launch_failed", err instanceof Error ? err.message : String(err));
    return 2;
  }
  return runCollieLoop(run, {
    mcJSON,
    mcCall,
    runHarnessTurn,
    resolveTurn: resolveClaimedTurn,
    turnEnded: () => { process.stdout.write(`${TURN_ENDED_LINE}\n`); },
    sleep: (ms) => Bun.sleep(ms),
    now: () => Date.now(),
    log,
    pollMs: Number(process.env["MC_COLLIE_POLL_MS"] ?? DEFAULT_POLL_MS),
  });
}

if (import.meta.main) {
  process.exit(await main());
}
