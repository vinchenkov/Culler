// trace-lane.ts — the trace lane of the resident. The lane copies the traces
// of each pipeline run, Collie session, and setup operation into the copies that AgentsView
// reads. It gives each main AgentsView session its display name, and it
// reports each main trace copy that AgentsView did not import. The lane runs
// on its own timers, so the tick never waits for it.
//
// Each pass has a limit on its work. A trace file that keeps failing waits
// longer before each new attempt. A complete AgentsView session list, read in
// pages over several passes, reaches every main session, the oldest included.

import { constants, chmod, link, lstat, mkdir, open, readdir, readFile, rename, rm, stat, writeFile, type FileHandle } from "node:fs/promises";
import { basename, dirname, join, relative } from "node:path";
import type { Ctx, Event, Logger } from "../../runner/shared/log";
import type { Exec, ExecResult } from "./types";
import { checkInside, copyTrace, openFolderInside, type CopyLimits, type CopyState } from "./trace-copy";
import { AgentsViewAnswer, type AgentsViewClient, type AgentsViewPage, type AgentsViewSession } from "./agentsview";
import { recoverSetupTrace, setupTraceCandidates, setupTraceFolder, SETUP_RECOVERY_PER_PASS } from "./setup-traces";
import { SAFE_ID } from "./pipeline-liveness";
import { createTraceMask } from "./trace-mask";
import { openCodeTraceID, OPENCODE_DATABASE } from "../../runner/shared/opencode-trace";
import { copyNativeDatabase, publishNativeFile } from "./opencode-copy";

export const COPY_INTERVAL_MS = 2_000;
/** The longest time between two session lists. */
export const SESSIONS_REFRESH_MS = 60_000;
/** The shortest time between two session lists. */
export const SESSIONS_MIN_INTERVAL_MS = 5_000;
/** The longest time that one session list can take. */
export const SESSIONS_TIMEOUT_MS = 30_000;
/** A session folder without a native session ID causes a session list for
 * this time after the folder changed. */
export const NEW_FOLDER_WINDOW_MS = 120_000;
/** An ended session gets no more passes when its traces did not change for
 * this time. */
export const SETTLE_MS = 60_000;
/** The longest wait before the lane tries a failed trace file again. */
export const RETRY_MAX_MS = 300_000;
/** A trace file that failed this number of times in a row does not keep its
 * ended session from settling. */
export const SETTLE_FAILURES = 3;
/** The most trace files that the lane copies for one session. */
export const MAX_SESSION_FILES = 256;
/** The most folders that one walk of a session folder enters. */
export const MAX_WALK_FOLDERS = 256;
/** The settled sessions that one copy pass checks for a missing copy. */
export const AUDIT_PER_PASS = 16;
export const RECONCILE_INTERVAL_MS = 15_000;
/** The longest time between two complete AgentsView session lists. */
export const RECHECK_INTERVAL_MS = 600_000;
/** The time between two complete session lists while a copy is overdue. */
export const OVERDUE_RECHECK_MS = 60_000;
export const OVERDUE_MS = 300_000;
export const REQUESTS_PER_PASS = 60;
/** The rows of the list of the most recent AgentsView sessions. */
export const RECENT_PAGE_SIZE = 100;
/** The rows of one page of the complete AgentsView session list. */
export const SWEEP_PAGE_SIZE = 500;
export const SWEEP_PAGES_PER_PASS = 4;
/** AgentsView and a trace line can write one time with a different precision. */
const TIME_TOLERANCE_MS = 1_000;
/** The last bytes of a main copy that the lane reads to find its last time. */
const MARK_WINDOWS = [4 << 10, 64 << 10, 1 << 20];

export interface TraceSession {
  kind: "run" | "collie" | "setup";
  id: string;
  session_path: string;
  format: "claude" | "codex" | "opencode";
  native_session_ref: string;
  trace_filename: string;
  legacy_filename?: string;
  agentsview_session: string;
  display_name: string;
  ended: boolean;
}

export interface OverdueCopy {
  kind: "run" | "collie" | "setup";
  id: string;
  agentsview_session: string;
  changed_at: string;
}

export interface TraceLaneDeps {
  runMc: Exec;
  log: Logger;
  mcHome: string;
  /** Absent when the deployment has no AgentsView address. The lane then
   * copies the traces and sends no request. */
  agentsView?: AgentsViewClient;
  /** One-shot timer. */
  schedule(fn: () => void, ms: number): unknown;
  cancel(handle: unknown): void;
  /** Epoch milliseconds. */
  now(): number;
  limits?: CopyLimits;
  credentials?(): readonly string[];
  requestsPerPass?: number;
  recentPageSize?: number;
  sweepPageSize?: number;
  sweepPagesPerPass?: number;
  maxSessionFiles?: number;
}

export interface TraceLane {
  /** Starts the copy timer and the AgentsView timer. */
  start(): void;
  /** One copy pass. A pass waits for the pass before it. This call also
   * waits for the session list that the pass started, and then copies the
   * sessions that the list added. */
  copyPass(): Promise<void>;
  /** One AgentsView pass. A pass waits for the pass before it. */
  reconcilePass(): Promise<void>;
  /** Stops both timers and waits for the passes in flight. */
  stop(): Promise<void>;
}

export interface TraceCopyPaths {
  sessions: string;
  claude: string;
  codex: string;
  opencode: string;
  health: string;
  inspection: string;
}

export function traceCopyPaths(mcHome: string): TraceCopyPaths {
  const traces = join(mcHome, "agentsview", "traces");
  return {
    sessions: join(mcHome, "sessions"),
    inspection: join(mcHome, "agentsview", "inspection"),
    claude: join(traces, "claude", "projects", "culler"),
    codex: join(traces, "codex", "sessions"),
    opencode: join(traces, "opencode"),
    health: join(mcHome, "health", "agentsview.json"),
  };
}

/** Runs copy passes until the trace copy of one ended session holds each
 * complete record. The result is false when the session has no trace copy or
 * the copy stays incomplete. */
export async function completeTraceCopy(lane: Pick<TraceLane, "copyPass">, mcHome: string, id: string,
  { passes = 8, pause = (ms: number) => Bun.sleep(ms) } = {}): Promise<boolean> {
  for (let pass = 0; pass < passes; pass += 1) {
    if (pass) await pause(1000);
    await lane.copyPass();
    let states: unknown[];
    try {
      states = Object.values(JSON.parse(await readFile(join(traceCopyPaths(mcHome).inspection, id, "status.json"), "utf8")).files ?? {});
    } catch { return false; }
    if (states.length && states.every(state => state === "ready")) return true;
    if (!states.includes("pending")) return false;
  }
  return false;
}

const NATIVE_REF = /^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$/;
const SEGMENT = "[A-Za-z0-9_][A-Za-z0-9._-]*";
const CLAUDE_FILE = new RegExp(`^${SEGMENT}(/${SEGMENT})*\\.jsonl$`);
const CODEX_FILE = /^([0-9]{4}\/[0-9]{2}\/[0-9]{2}\/)?rollout-[A-Za-z0-9._-]+\.jsonl$/;
const CLAUDE_CHILD = /^agent-[A-Za-z0-9._-]*\.jsonl$/;
const CODEX_ROLLOUT = /^rollout-[A-Za-z0-9._-]+\.jsonl$/;
const CODEX_DATE_LEVELS = [/^[0-9]{4}$/, /^[0-9]{2}$/, /^[0-9]{2}$/];
const CHILD_DEPTH = 8;

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function code(error: unknown): string | undefined {
  return error !== null && typeof error === "object" && "code" in error ? String(error.code) : undefined;
}

function detail(result: ExecResult): string {
  return (result.stderr.trim() || result.stdout.trim()).slice(0, 500);
}

/** The text with each backslash and control character escaped. An agent
 * names the folders in its session folder, and such a name must not start a
 * new line in a log. */
export function printable(text: string): string {
  return text.replace(/[\\\u0000-\u001f\u007f-\u009f\u2028\u2029]/g,
    (char) => (char === "\\" ? "\\\\" : `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`));
}

/** Epoch milliseconds of an RFC 3339 time. The time keeps at most
 * millisecond precision. */
export function parseTime(text: string): number {
  return Date.parse(text.replace(/(\.[0-9]{3})[0-9]+/, "$1"));
}

function lineTime(line: string): number | null {
  if (line.trim() === "") return null;
  let value: unknown;
  try {
    value = JSON.parse(line);
  } catch {
    return null;
  }
  const text = value !== null && typeof value === "object" ? (value as { timestamp?: unknown }).timestamp : undefined;
  if (typeof text !== "string") return null;
  const time = parseTime(text);
  return Number.isFinite(time) ? time : null;
}

/** The time of the last complete line before `end` of a trace copy that has
 * a `timestamp`. The value is null when the last bytes hold no such line. */
export async function copyMark(path: string, end: number): Promise<number | null> {
  let handle: FileHandle;
  try {
    handle = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  } catch {
    return null;
  }
  try {
    for (const window of MARK_WINDOWS) {
      const start = Math.max(0, end - window);
      const buffer = Buffer.alloc(end - start);
      const { bytesRead } = await handle.read(buffer, 0, buffer.length, start);
      const lines = buffer.subarray(0, bytesRead).toString("utf8").split("\n");
      // The first piece is part of a line, except at the start of the file.
      for (let index = lines.length - 1; index >= (start === 0 ? 0 : 1); index -= 1) {
        const time = lineTime(lines[index]!);
        if (time !== null) return time;
      }
      if (start === 0) return null;
    }
    return null;
  } catch {
    return null;
  } finally {
    await handle.close();
  }
}

function invalidRow(row: Record<string, unknown>): string | undefined {
  if (row.kind !== "run" && row.kind !== "collie" && row.kind !== "setup") return "the kind is not run, collie, or setup";
  if (typeof row.id !== "string" || !SAFE_ID.test(row.id)) return "the ID is not valid";
  if (row.session_path !== (row.kind === "setup" ? `environment-runs/${row.id}/session` : `sessions/${row.id}`)) return "the session path does not match its operation";
  if (typeof row.native_session_ref !== "string" || !NATIVE_REF.test(row.native_session_ref)) {
    return "the native session ID is not valid";
  }
  if (typeof row.trace_filename !== "string") return "the trace file name is missing";
  if (row.format === "claude") {
    if (!CLAUDE_FILE.test(row.trace_filename)) return "the trace file name is not valid";
  } else if (row.format === "codex") {
    if (!CODEX_FILE.test(row.trace_filename)) return "the trace file name is not a Codex rollout";
  } else if (row.format === "opencode") {
    if (row.trace_filename !== OPENCODE_DATABASE) return "the native database path is invalid";
    if (row.legacy_filename !== undefined && (typeof row.legacy_filename !== "string" || !CODEX_FILE.test(row.legacy_filename))) return "the retained export path is invalid";
  } else {
    return "the trace format is invalid";
  }
  if (typeof row.agentsview_session !== "string" || row.agentsview_session === "") {
    return "the AgentsView session is missing";
  }
  if (typeof row.display_name !== "string") return "the display name is missing";
  if (typeof row.ended !== "boolean") return "the ended value is missing";
  return undefined;
}

/** Parses the output of `mc __trace-sessions`. Each row that is not valid
 * appears in `invalid` with its cause. */
export function parseTraceSessions(stdout: string): {
  sessions: TraceSession[];
  invalid: Array<{ id: string; cause: string }>;
} {
  let body: unknown;
  try {
    body = JSON.parse(stdout);
  } catch {
    throw new Error("the output is not JSON");
  }
  const rows = body !== null && typeof body === "object" ? (body as { sessions?: unknown }).sessions : undefined;
  if (!Array.isArray(rows)) throw new Error("the output holds no session list");
  const sessions: TraceSession[] = [];
  const invalid: Array<{ id: string; cause: string }> = [];
  for (const row of rows) {
    if (row === null || typeof row !== "object") {
      invalid.push({ id: "", cause: "the row is not an object" });
      continue;
    }
    const cause = invalidRow(row as Record<string, unknown>);
    if (cause !== undefined) {
      const id = (row as { id?: unknown }).id;
      invalid.push({ id: typeof id === "string" ? id.slice(0, 128) : "", cause });
      continue;
    }
    const valid = row as TraceSession;
    sessions.push({
      kind: valid.kind, id: valid.id, session_path: valid.session_path, format: valid.format,
      native_session_ref: valid.native_session_ref, trace_filename: valid.trace_filename,
      agentsview_session: valid.agentsview_session, display_name: valid.display_name, ended: valid.ended,
      ...(valid.legacy_filename ? { legacy_filename: valid.legacy_filename } : {}),
    });
  }
  return { sessions, invalid };
}

interface FileState extends CopyState {
  /** The path of the trace file below the session folder. */
  label: string;
  /** The failures of this file since its last copy. */
  failures?: number;
  /** The lane tries the file again at this time. */
  retryAt?: number;
  availability?: "ready" | "pending" | "delayed" | "unavailable";
}

/** A change of a main copy that AgentsView did not show yet. */
interface Pending {
  /** The time when the lane saw the change. */
  since: number;
  /** The size of the main copy after the change. */
  position: number;
  /** The time of the last line of the copy with a time, or null when no line
   * has a time. The lane reads it when it first needs it. */
  mark?: number | null;
}

interface SessionState {
  row: TraceSession;
  dir: string;
  main: FileState;
  children: Map<string, FileState>;
  settled: boolean;
  inspectionStatus?: string;
  native?: { digest: string; members: string[]; queue: string[]; imported?: string; checkedAt: number };
  nativeCause?: string;
  nativeImportCause?: string;
  exportRetired?: boolean;
  legacy?: FileState;
  /** The first change of the main copy that AgentsView did not show yet. */
  pending?: Pending;
  /** The last time that the lane learned the state of the main session in
   * AgentsView. */
  checkedAt?: number;
  /** The last time that a session list of AgentsView held the main session. */
  listedAt?: number;
  /** The display name that AgentsView showed at the last check. */
  shownName?: string | null;
  /** The AgentsView process that last showed the main session. A later
   * absence in the same process means that the operator removed it. */
  shownBy?: number;
  /** A check after the deadline of the pending change found AgentsView
   * without that change at this time. */
  lateAt?: number;
}

interface Walk {
  folders: number;
  files: number;
  /** A limit stopped the walk. */
  truncated: boolean;
  /** A folder of the walk failed. */
  failed: boolean;
}

interface Sweep {
  /** The cursor of the next page, or null for the first page. */
  cursor: string | null;
  startedAt: number;
}

export function createTraceLane(deps: TraceLaneDeps): TraceLane {
  const paths = traceCopyPaths(deps.mcHome);
  const maxFiles = deps.maxSessionFiles ?? MAX_SESSION_FILES;
  let mask = createTraceMask(deps.credentials?.());
  const controller = new AbortController();
  const stopped = new Promise<"stopped">((resolve) => {
    controller.signal.addEventListener("abort", () => resolve("stopped"), { once: true });
  });
  const sessions = new Map<string, SessionState>();
  // Each session folder, with the time of its last change when the lane
  // first saw it.
  const folders = new Map<string, number>();
  // Each logged failure cause, by the item that failed. A cause is logged
  // once until the item succeeds.
  const failures = new Map<string, Set<string>>();
  let lastRefreshAt: number | undefined;
  let recoveryCursor = 0;
  let nativeCursor = 0;
  let nativeImportCursor = 0;
  let lastRefreshFailed = false;
  let refreshing: Promise<void> | undefined;
  let audit: Iterator<SessionState> | undefined;
  let closed = false;
  let copyTimer: unknown;
  let reconcileTimer: unknown;
  let copyChain: Promise<unknown> = Promise.resolve();
  let reconcileChain: Promise<void> = Promise.resolve();
  let availability: "unknown" | "available" | "unavailable" = "unknown";
  let agentsViewPid: number | undefined;
  let availableSince = 0;
  let rulesReady = false;
  // The complete AgentsView session list in progress, the process of the
  // last complete list, and the start time of that list.
  let sweep: Sweep | undefined;
  let sweptPid: number | undefined;
  let sweptAt = 0;
  // The Culler IDs of the sessions that need a display name or a direct request.
  const renames = new Set<string>();
  const probes = new Set<string>();
  let started = false;

  function reportOnce(key: string, cause: string, event: Event, text: string, ctx?: Ctx): void {
    let causes = failures.get(key);
    if (causes === undefined) failures.set(key, causes = new Set());
    if (causes.has(cause)) return;
    causes.add(cause);
    deps.log.warn(event, text, ctx);
  }

  const copyKey = (id: string, label: string) => `copy:${id}:${label}`;

  /** Logs a failure of one trace file, or of one folder when `folder` is
   * true. An empty label names the session folder. */
  function reportCopy(session: SessionState, label: string, cause: string, folder = false): void {
    const { kind, id } = session.row;
    const name = printable(label);
    const item = !folder ? `the trace file ${name}` : label === "" ? "the trace folder" : `the trace folder ${name}`;
    reportOnce(copyKey(id, label), cause, "trace.copy_failed",
      `The resident cannot copy ${item} of ${kind} ${id}: ${printable(cause)}`, { kind, id, file: name });
  }

  /** Removes the logged causes and the requests of one session that is gone. */
  function forget(id: string): void {
    for (const key of failures.keys()) {
      if (key.startsWith(`copy:${id}:`) || key === `limit:${id}` || key === `name:${id}` || key === `probe:${id}`) {
        failures.delete(key);
      }
    }
    renames.delete(id);
    probes.delete(id);
  }

  function mainState(row: TraceSession, dir: string): FileState {
    const dest = row.format === "claude"
      ? join(paths.claude, `${row.native_session_ref}.jsonl`)
      : row.format === "opencode" ? join(paths.opencode, `opencode-${row.id}.db`) : join(paths.codex, row.trace_filename);
    return { root: dir, source: join(dir, row.trace_filename), dest, label: row.trace_filename };
  }

  function applyRows(rows: TraceSession[]): void {
    const listed = new Set<string>();
    const dests = new Set<string>();
    for (const row of rows) {
      const dir = join(deps.mcHome, row.session_path);
      const main = mainState(row, dir);
      if (listed.has(row.id) || dests.has(main.dest)) {
        reportOnce(`row:${row.id}`, "duplicate", "trace.sessions_failed",
          `The session list holds ${row.kind} ${row.id} or its trace copy twice. The resident copies the first row only.`,
          { kind: row.kind, id: row.id });
        continue;
      }
      listed.add(row.id);
      dests.add(main.dest);
      const current = sessions.get(row.id);
      if (current === undefined || current.main.dest !== main.dest || current.main.source !== main.source) {
        if (current !== undefined) forget(row.id);
        sessions.set(row.id, { row, dir, main, children: new Map(), settled: false });
        continue;
      }
      if (current.row.agentsview_session !== row.agentsview_session) {
        current.shownName = undefined;
        current.shownBy = undefined;
        current.lateAt = undefined;
      }
      if (!row.ended) current.settled = false;
      current.row = row;
    }
    for (const id of [...sessions.keys()]) {
      if (listed.has(id)) continue;
      sessions.delete(id);
      forget(id);
    }
  }

  async function listFolders(): Promise<string[] | undefined> {
    try {
      const entries = await readdir(paths.sessions, { withFileTypes: true }).catch(error => {
        if (code(error) === "ENOENT") return [];
        throw error;
      });
      const setup = await readdir(join(deps.mcHome, "environment-runs"), { withFileTypes: true }).catch(error => {
        if (code(error) === "ENOENT") return [];
        throw error;
      });
      failures.delete("folders");
      return [...entries, ...setup].filter((entry) => entry.isDirectory() && SAFE_ID.test(entry.name)).map((entry) => entry.name);
    } catch (error) {
      if (code(error) === "ENOENT") return [];
      reportOnce("folders", message(error), "trace.sessions_failed",
        `The resident cannot read the session folders: ${message(error)}`);
      return undefined;
    }
  }

  function refreshWanted(names: string[] | undefined, now: number): boolean {
    if (lastRefreshAt === undefined) return true;
    const since = now - lastRefreshAt;
    if (since >= SESSIONS_REFRESH_MS) return true;
    if (since < SESSIONS_MIN_INTERVAL_MS) return false;
    if (lastRefreshFailed) return true;
    if (names === undefined) return false;
    return names.some((name) => {
      const seenAt = folders.get(name);
      return seenAt === undefined || (!sessions.has(name) && now - seenAt < NEW_FOLDER_WINDOW_MS);
    });
  }

  async function recordFolders(names: string[], now: number): Promise<void> {
    const present = new Set(names);
    for (const name of [...folders.keys()]) if (!present.has(name)) folders.delete(name);
    for (const name of names) {
      if (folders.has(name)) continue;
      let seenAt = now;
      try {
        seenAt = Math.min(now, (await stat(join(paths.sessions, name)).catch(() => stat(join(deps.mcHome, "environment-runs", name, "session")))).mtimeMs);
      } catch {
        // A folder that disappears gets the current time.
      }
      folders.set(name, seenAt);
    }
  }

  /** A trace command has a deadline independent of process cancellation. */
  async function runTraceCommand(argv: string[], timeoutMs = SESSIONS_TIMEOUT_MS): Promise<ExecResult | "timeout" | "stopped"> {
    const stopper = new AbortController();
    let timer: unknown;
    const timeout = new Promise<"timeout">((resolve) => {
      timer = deps.schedule(() => resolve("timeout"), timeoutMs);
    });
    try {
      const call = deps.runMc(argv, undefined, { signal: AbortSignal.any([controller.signal, stopper.signal]) });
      const result = await Promise.race([call, timeout, stopped]);
      if (result === "timeout" || result === "stopped") {
        stopper.abort();
        void call.catch(() => undefined);
      }
      return result;
    } finally {
      deps.cancel(timer);
    }
  }

  async function refreshSessions(names: string[] | undefined, now: number): Promise<void> {
    lastRefreshAt = now;
    lastRefreshFailed = true;
    if (names !== undefined) await recordFolders(names, now);
    const failed = (cause: string) => reportOnce("sessions", cause, "trace.sessions_failed",
      `The resident cannot read the trace sessions: ${cause}`);
    let result: ExecResult | "timeout" | "stopped";
    try {
      result = await runTraceCommand(["__trace-sessions"]);
    } catch (error) {
      if (!closed) failed(`mc __trace-sessions failed: ${message(error)}`);
      return;
    }
    if (closed || result === "stopped") return;
    if (result === "timeout") {
      failed(`mc __trace-sessions did not answer within ${SESSIONS_TIMEOUT_MS / 1_000} seconds`);
      return;
    }
    if (result.exitCode !== 0) {
      failed(`mc __trace-sessions exited ${result.exitCode}: ${detail(result)}`);
      return;
    }
    let parsed: ReturnType<typeof parseTraceSessions>;
    try {
      parsed = parseTraceSessions(result.stdout);
    } catch (error) {
      failed(`mc __trace-sessions returned an invalid response: ${message(error)}`);
      return;
    }
    failures.delete("sessions");
    lastRefreshFailed = false;
    const rowKeys = new Set<string>();
    for (const { id, cause } of parsed.invalid) {
      rowKeys.add(`row:${id}`);
      reportOnce(`row:${id}`, cause, "trace.sessions_failed",
        `The resident ignores the trace session ${JSON.stringify(id)}: ${cause}`, { id });
    }
    for (const row of parsed.sessions) rowKeys.add(`row:${row.id}`);
    for (const key of failures.keys()) if (key.startsWith("row:") && !rowKeys.has(key)) failures.delete(key);
    applyRows(parsed.sessions);
    const candidates = setupTraceCandidates(JSON.parse(result.stdout).setup_pending);
    const recoveryDeadline = deps.now() + SESSIONS_TIMEOUT_MS;
    let attempts = 0;
    while (attempts < Math.min(SETUP_RECOVERY_PER_PASS, candidates.length) && !closed && deps.now() < recoveryDeadline) {
      const row = candidates[(recoveryCursor + attempts++) % candidates.length]!;
      try {
        const found = await recoverSetupTrace(deps.mcHome, row);
        if (closed) break;
        const registered = await runTraceCommand(["__trace-register-setup", row.id, found.nativeRef, found.filename],
          Math.max(1, recoveryDeadline - deps.now()));
        if (registered === "stopped") break;
        if (registered === "timeout") throw new Error("setup trace registration exceeded the recovery deadline");
        if (registered.exitCode !== 0) throw new Error(detail(registered));
        parsed.sessions.push({ ...row, native_session_ref: found.nativeRef,
          trace_filename: row.format === "opencode" ? OPENCODE_DATABASE : found.filename,
          ...(row.format === "opencode" && found.filename !== OPENCODE_DATABASE ? { legacy_filename: found.filename } : {}),
          agentsview_session: row.format === "opencode" ? `opencode:${found.nativeRef}` : row.format === "codex" ? `codex:${found.nativeRef}` : found.nativeRef });
        failures.delete(`recovery:${row.id}`);
      } catch (error) {
        reportOnce(`recovery:${row.id}`, message(error), "trace.sessions_failed",
          `The resident cannot recover the setup trace ${row.id}: ${printable(message(error))}`, { kind: "setup", id: row.id });
      }
    }
    recoveryCursor = candidates.length ? (recoveryCursor + attempts) % candidates.length : 0;
    applyRows(parsed.sessions);
  }

  /** The regular files of one folder tree whose names match. The walk
   * follows no symbolic link. Each folder that the walk enters must be the
   * folder at its real path inside the session folder, before and after the
   * walk reads it. The walk enters a limited number of folders and finds a
   * limited number of files. */
  async function walk(
    session: SessionState, root: string, prefix: string, depth: number,
    folder: (name: string, level: number) => boolean, file: (name: string, level: number) => boolean,
    found: string[], state: Walk, level = 0,
  ): Promise<void> {
    if (state.truncated) return;
    if (state.folders <= 0) {
      state.truncated = true;
      return;
    }
    state.folders -= 1;
    let entries;
    try {
      const handle = await openFolderInside(session.dir, root, "trace folder");
      try {
        entries = await readdir(root, { withFileTypes: true });
        await checkInside(handle, session.dir, root, "trace folder");
      } finally {
        await handle.close();
      }
    } catch (error) {
      if (code(error) !== "ENOENT") {
        state.failed = true;
        reportCopy(session, prefix, message(error), true);
      }
      return;
    }
    failures.delete(copyKey(session.row.id, prefix));
    entries.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
    for (const entry of entries) {
      if (state.truncated) return;
      const label = prefix === "" ? entry.name : `${prefix}/${entry.name}`;
      if (entry.isFile() && file(entry.name, level)) {
        if (found.length >= state.files) {
          state.truncated = true;
          return;
        }
        found.push(label);
      } else if (entry.isDirectory() && level < depth && folder(entry.name, level)) {
        await walk(session, join(root, entry.name), label, depth, folder, file, found, state, level + 1);
      }
    }
  }

  async function traceFiles(session: SessionState): Promise<FileState[]> {
    const labels: string[] = [];
    const { row, dir } = session;
    const state: Walk = { folders: MAX_WALK_FOLDERS, files: Math.max(0, maxFiles - 1), truncated: false, failed: false };
    if (row.format === "claude") {
      const root = join(dir, "subagents");
      let isFolder = false;
      try {
        isFolder = (await lstat(root)).isDirectory();
        if (!isFolder) {
          state.failed = true;
          reportCopy(session, "subagents", "the subagents entry is not a folder", true);
        } else {
          failures.delete(copyKey(row.id, "subagents"));
        }
      } catch (error) {
        if (code(error) !== "ENOENT") {
          state.failed = true;
          reportCopy(session, "subagents", message(error), true);
        }
      }
      if (isFolder) {
        await walk(session, root, "subagents", CHILD_DEPTH, () => true, (name) => CLAUDE_CHILD.test(name), labels, state);
      }
    } else {
      await walk(session, dir, "", CODEX_DATE_LEVELS.length,
        (name, level) => CODEX_DATE_LEVELS[level]!.test(name),
        (name, level) => (level === 0 || level === CODEX_DATE_LEVELS.length) && CODEX_ROLLOUT.test(name), labels, state);
    }
    if (state.truncated) {
      reportOnce(`limit:${row.id}`, "limit", "trace.copy_failed",
        `The session folder of ${row.kind} ${row.id} holds more than ${maxFiles} trace files or ${MAX_WALK_FOLDERS} folders. ` +
          `The resident copies at most ${maxFiles} trace files of one session.`,
        { kind: row.kind, id: row.id });
    }
    const files: FileState[] = [session.main];
    const present = new Set<string>();
    for (const label of labels) {
      if (label === row.trace_filename) continue;
      present.add(label);
      let child = session.children.get(label);
      if (child === undefined) {
        const dest = row.format === "claude"
          ? join(paths.claude, row.native_session_ref, label)
          : join(paths.codex, label);
        child = { root: dir, source: join(dir, label), dest, label };
        session.children.set(label, child);
      }
      files.push(child);
    }
    if (!state.truncated && !state.failed) {
      for (const label of [...session.children.keys()]) {
        if (present.has(label)) continue;
        await rm(join(paths.inspection, row.id, label), { force: true });
        session.children.delete(label);
        failures.delete(copyKey(row.id, label));
      }
    }
    return files;
  }

  /** A change of the main copy starts a pending change when none waits. A
   * complete new copy or a shorter copy starts a new pending change. */
  function noteChange(session: SessionState, before: number | undefined, replaced: boolean, now: number): void {
    const position = session.main.position;
    if (position === undefined || position === 0 || (position === before && !replaced)) return;
    if (session.pending === undefined || replaced || position < session.pending.position) {
      session.pending = { since: now, position };
      session.lateAt = undefined;
    }
  }

  async function inspectionDirectory(directory: string): Promise<void> {
    await mkdir(directory, { recursive: true, mode: 0o755 });
    const folder = await openFolderInside(paths.inspection, directory, "trace inspection folder");
    await folder.close();
    // The private deployment home contains this mount. The container user needs traversal inside it.
    let path = paths.inspection;
    await chmod(path, 0o755);
    for (const part of relative(paths.inspection, directory).split("/").filter(Boolean)) {
      path = join(path, part);
      await chmod(path, 0o755);
    }
  }

  async function publishInspection(session: SessionState, file: FileState): Promise<void> {
    const target = join(paths.inspection, session.row.id, file.label);
    if (file.recopy || file.availability === "unavailable" || !file.position) {
      await rm(target, { force: true });
      return;
    }
    const source = await lstat(file.dest);
    if (!source.isFile()) throw new Error("The trace copy is not a regular file.");
    const current = await lstat(target).catch(error => {
      if (code(error) === "ENOENT") return undefined;
      throw error;
    });
    if (current?.isFile() && current.dev === source.dev && current.ino === source.ino) return;
    await inspectionDirectory(dirname(target));
    await chmod(file.dest, 0o644);
    const stage = `${target}.next`;
    await rm(stage, { force: true });
    await link(file.dest, stage);
    await rename(stage, target);
  }

  async function publishAvailability(session: SessionState, files: FileState[]): Promise<void> {
    const body = JSON.stringify({ files: Object.fromEntries(files.map(file => [file.label, file.availability ?? "pending"])),
      ...(session.row.format === "opencode" ? { format: "opencode", native_session_ref: session.row.native_session_ref,
        copy: session.main.availability ?? "pending", cause: session.nativeCause ?? null,
        import: session.native?.imported === session.native?.digest && session.native ? "current" : "pending",
        import_cause: session.nativeImportCause ?? null,
        ...(session.legacy ? { export: { filename: session.legacy.label, format: "OpenCode export" } } : {}) } : {}) }) + "\n";
    if (body === session.inspectionStatus && (await lstat(join(paths.inspection, session.row.id, "status.json")).catch(() => undefined))?.isFile()) return;
    const directory = join(paths.inspection, session.row.id);
    await inspectionDirectory(directory);
    const stage = join(directory, ".status.next");
    await writeFile(stage, body, { mode: 0o644 });
    await rename(stage, join(directory, "status.json"));
    session.inspectionStatus = body;
  }

  async function copySession(session: SessionState, now: number): Promise<void> {
    if (session.row.format === "opencode") {
      await copyNative(session, now);
      return;
    }
    if (session.row.kind === "setup") await setupTraceFolder(deps.mcHome, session.row.id);
    const files = await traceFiles(session);
    let complete = true;
    let newest = 0;
    for (const file of files) {
      if (closed) return;
      if ((file.retryAt ?? 0) > now) {
        if ((file.failures ?? 0) < SETTLE_FAILURES) complete = false;
        newest = Math.max(newest, file.sourceMtimeMs ?? 0);
        continue;
      }
      const before = file.position;
      try {
        const result = await copyTrace(file, now, deps.limits, mask);
        file.availability = result.absent ? "unavailable" : result.caughtUp && !file.scan && file.position ? "ready" : "pending";
        await publishInspection(session, file);
        file.failures = 0;
        file.retryAt = undefined;
        if (!result.caughtUp) complete = false;
        if (result.absent && file === session.main && session.row.ended) {
          reportCopy(session, file.label, "the trace file is absent");
        } else {
          failures.delete(copyKey(session.row.id, file.label));
        }
        if (file === session.main) noteChange(session, before, result.replaced, now);
      } catch (error) {
        file.availability = "unavailable";
        await publishInspection(session, file).catch(() => {});
        file.failures = (file.failures ?? 0) + 1;
        file.retryAt = now + Math.min(COPY_INTERVAL_MS * 2 ** (file.failures - 1), RETRY_MAX_MS);
        if (file.failures < SETTLE_FAILURES) complete = false;
        reportCopy(session, file.label, message(error));
      }
      newest = Math.max(newest, file.sourceMtimeMs ?? 0);
    }
    await publishAvailability(session, files);
    if (session.row.ended && complete && now - newest >= SETTLE_MS) session.settled = true;
  }

  async function copyNative(session: SessionState, now: number): Promise<void> {
    const file = session.main;
    if ((file.retryAt ?? 0) > now) return;
    session.exportRetired ??= !!(await lstat(file.dest + ".retired").catch(() => undefined))?.isFile();
    const accept = async (result: Awaited<ReturnType<typeof copyNativeDatabase>>) => {
      file.position = result.bytes.length;
      if (session.native?.digest !== result.digest || result.changed) {
        const queue = [...new Set([...(session.native?.queue ?? []).filter(id => result.sessions.includes(id)), ...result.sessions])];
        session.native = { digest: result.digest, members: result.sessions, queue, checkedAt: now };
        session.pending ??= { since: now, position: file.position };
        file.changedAt = now;
      }
      session.native!.checkedAt = now;
      await publishInspection(session, file);
    };
    try {
      const source = await openFolderInside(deps.mcHome, session.dir, "native session folder");
      await source.close();
      await mkdir(paths.opencode, { recursive: true, mode: 0o700 });
      const destination = await openFolderInside(deps.mcHome, paths.opencode, "native trace folder");
      await destination.close();
      const result = await copyNativeDatabase(session.dir, file.dest, session.row.native_session_ref, deps.credentials?.() ?? [], controller.signal);
      file.availability = "ready";
      file.failures = 0;
      file.retryAt = undefined;
      session.nativeCause = undefined;
      await accept(result);
      failures.delete(copyKey(session.row.id, file.label));
    } catch (error) {
      // An older complete database remains available after a failed refresh.
      const previous = await lstat(file.dest).catch(() => undefined);
      file.availability = previous?.isFile() ? "delayed" : "unavailable";
      file.position = previous?.isFile() ? previous.size : 0;
      if (previous?.isFile() && !controller.signal.aborted) {
        try {
          // The last copy can need new masks after the original becomes unavailable.
          await accept(await copyNativeDatabase(paths.opencode, file.dest, session.row.native_session_ref,
            deps.credentials?.() ?? [], controller.signal, basename(file.dest)));
        } catch { /* The last complete copy remains available. */ }
      }
      await publishInspection(session, file);
      session.nativeCause = message(error);
      file.failures = (file.failures ?? 0) + 1;
      file.retryAt = now + Math.min(COPY_INTERVAL_MS * 2 ** (file.failures - 1), RETRY_MAX_MS);
      reportCopy(session, file.label, message(error));
    }
    if (session.row.legacy_filename && !session.exportRetired) {
      session.legacy ??= { root: session.dir, source: join(session.dir, session.row.legacy_filename),
        dest: join(paths.codex, session.row.legacy_filename), label: session.row.legacy_filename };
      try {
        const copied = await copyTrace(session.legacy, now, deps.limits, mask);
        session.legacy.availability = copied.absent ? "unavailable" : copied.caughtUp ? "ready" : "pending";
        await publishInspection(session, session.legacy);
      } catch (error) { reportCopy(session, session.legacy.label, message(error)); }
    }
    await publishAvailability(session, [file, ...(session.legacy ? [session.legacy] : [])]);
  }

  async function copySessions(now: number): Promise<void> {
    const native = [...sessions.values()].filter(session => session.row.format === "opencode");
    const selected = new Set(Array.from({ length: Math.min(4, native.length) }, (_, index) => native[(nativeCursor + index) % native.length]));
    nativeCursor = native.length ? (nativeCursor + selected.size) % native.length : 0;
    for (const session of [...sessions.values()]) {
      if (closed) return;
      if (session.settled) continue;
      if (session.row.format === "opencode" && (!selected.has(session) ||
          (session.row.ended && session.native && now - session.native.checkedAt < SESSIONS_REFRESH_MS && session.main.maskRevision === mask.revision))) continue;
      try {
        await copySession(session, now);
        session.main.maskRevision = mask.revision;
      } catch (error) {
        session.main.availability = "unavailable";
        await publishAvailability(session, [session.main, ...session.children.values()]).catch(() => {});
        reportCopy(session, "", message(error), true);
      }
    }
  }

  /** Checks a limited number of settled sessions in turn. A settled session
   * whose copy is absent or has another size than its copy position gets
   * passes again, and the next pass makes a complete new copy. */
  async function auditSettled(): Promise<void> {
    let checked = 0;
    while (checked < AUDIT_PER_PASS && !closed) {
      audit ??= sessions.values();
      const next = audit.next();
      if (next.done) {
        audit = undefined;
        return;
      }
      const session = next.value;
      if (!session.settled || sessions.get(session.row.id) !== session) continue;
      checked += 1;
      if (!(await lstat(join(paths.inspection, session.row.id, "status.json")).catch(() => undefined))?.isFile()) session.settled = false;
      for (const file of [session.main, ...session.children.values()]) {
        if (file.position === undefined || (file.failures ?? 0) > 0) continue;
        const copy = await lstat(file.dest).catch(() => undefined);
        const inspection = await lstat(join(paths.inspection, session.row.id, file.label)).catch(() => undefined);
        if (copy === undefined || !copy.isFile() || copy.size !== file.position || inspection?.ino !== copy.ino || inspection.dev !== copy.dev) {
          session.settled = false;
          break;
        }
      }
    }
  }

  /** One copy pass. It starts a session list when one is due and none runs,
   * and it copies the known sessions without a wait for the list. The result
   * holds the session list that this pass started. The result is an object,
   * because a promise that a promise resolves takes the state of that promise. */
  async function copyPassNow(): Promise<{ refresh?: Promise<void> }> {
    const nextMask = createTraceMask(deps.credentials?.());
    if (nextMask.revision !== mask.revision) {
      mask = nextMask;
      for (const session of sessions.values()) session.settled = false;
    }
    const now = deps.now();
    const names = await listFolders();
    let refresh: Promise<void> | undefined;
    if (refreshing === undefined && refreshWanted(names, now)) {
      refresh = refreshing = refreshSessions(names, now).finally(() => {
        refreshing = undefined;
      });
    }
    await copySessions(now);
    await auditSettled();
    return refresh === undefined ? {} : { refresh };
  }

  function runCopyPass(): Promise<{ refresh?: Promise<void> }> {
    const pass = copyChain.then(() => (closed ? {} : copyPassNow()));
    copyChain = pass.catch(() => undefined);
    return pass;
  }

  async function copyPass(): Promise<void> {
    const { refresh } = await runCopyPass();
    if (refresh === undefined) return;
    await refresh;
    await runCopyPass();
  }

  function deadline(pending: Pending): number {
    return Math.max(pending.since, availableSince) + OVERDUE_MS;
  }

  function overdue(): OverdueCopy[] {
    if (availability !== "available") return [];
    const late: Array<{ session: SessionState; changed: number }> = [];
    for (const session of sessions.values()) {
      const { pending, lateAt } = session;
      if (pending === undefined || lateAt === undefined || lateAt < deadline(pending)) continue;
      if ((session.main.position ?? 0) === 0) continue;
      late.push({ session, changed: session.main.changedAt ?? pending.since });
    }
    late.sort((a, b) => a.changed - b.changed || a.session.row.id.localeCompare(b.session.row.id));
    return late.map(({ session, changed }) => ({
      kind: session.row.kind,
      id: session.row.id,
      agentsview_session: session.row.agentsview_session,
      changed_at: new Date(changed).toISOString(),
    }));
  }

  /** Writes the health file with the time of the write. */
  async function publishHealth(list: OverdueCopy[]): Promise<void> {
    const directory = join(deps.mcHome, "health");
    try {
      await mkdir(directory, { recursive: true, mode: 0o700 });
      const stage = join(directory, `.agentsview.${process.pid}.next`);
      const observedAt = new Date(deps.now()).toISOString();
      const native = [...sessions.values()].filter(session => session.row.format === "opencode").map(session => ({
          kind: session.row.kind, id: session.row.id, copy: session.main.availability ?? "pending",
          import: session.native && session.native.imported === session.native.digest ? "current" : "pending",
          cause: session.nativeCause ?? null,
          import_cause: session.nativeImportCause ?? null,
        }));
      await writeFile(stage, JSON.stringify({ observed_at: observedAt, overdue: list, ...(native.length ? { native } : {}) }) + "\n", { mode: 0o600 });
      await rename(stage, paths.health);
      failures.delete("health");
    } catch (error) {
      reportOnce("health", message(error), "agentsview.health_failed",
        `The resident cannot write ${paths.health}: ${message(error)}`);
    }
  }

  /** True when AgentsView shows the pending change: its last line has a time
   * at or after the time of the last line of the copy at that change. */
  async function imported(session: SessionState, pending: Pending, endedAt: string | null): Promise<boolean> {
    if (session.row.format === "opencode") return !!session.native && session.native.imported === session.native.digest;
    if (pending.mark === undefined) pending.mark = await copyMark(session.main.dest, pending.position);
    if (pending.mark === null) return true;
    const shown = endedAt === null ? Number.NaN : parseTime(endedAt);
    return Number.isFinite(shown) && shown + TIME_TOLERANCE_MS >= pending.mark;
  }

  /** Records one answer of AgentsView for one main session. `shown` is null
   * when AgentsView holds no such session. */
  async function observe(session: SessionState, shown: AgentsViewSession | null, at: number): Promise<void> {
    session.checkedAt = at;
    const pending = session.pending;
    if (shown === null) {
      if (session.shownBy === agentsViewPid) session.lateAt = undefined;
      else if (pending !== undefined && at >= deadline(pending)) session.lateAt = at;
      return;
    }
    session.shownBy = agentsViewPid;
    session.shownName = shown.displayName;
    if (shown.displayName !== session.row.display_name) renames.add(session.row.id);
    else renames.delete(session.row.id);
    if (pending === undefined) return;
    if (await imported(session, pending, shown.endedAt)) {
      const position = session.main.position ?? 0;
      session.pending = position > pending.position ? { since: at, position } : undefined;
      session.lateAt = undefined;
    } else if (at >= deadline(pending)) {
      session.lateAt = at;
    }
  }

  /** A complete session list is due for a new AgentsView process, every 10
   * minutes, for a pending change whose deadline passed without a check, and
   * each minute while a copy is overdue. */
  function sweepWanted(now: number): boolean {
    if (sweptPid !== agentsViewPid || now - sweptAt >= RECHECK_INTERVAL_MS) return true;
    for (const session of sessions.values()) {
      const pending = session.pending;
      if (pending === undefined || (session.main.position ?? 0) === 0) continue;
      if (session.lateAt !== undefined) {
        if (now - sweptAt >= OVERDUE_RECHECK_MS) return true;
      } else if (now >= deadline(pending) && (session.checkedAt ?? -Infinity) < deadline(pending)) {
        return true;
      }
    }
    return false;
  }

  /** A complete list ended. AgentsView can hold a session outside the list,
   * so each session that the list did not hold and that can be late gets a
   * direct request. */
  function finishSweep(done: Sweep, index: Map<string, SessionState>): void {
    sweep = undefined;
    sweptPid = agentsViewPid;
    sweptAt = done.startedAt;
    const now = deps.now();
    for (const session of index.values()) {
      if ((session.listedAt ?? -Infinity) >= done.startedAt) continue;
      const pending = session.pending;
      if (session.lateAt !== undefined || (pending !== undefined && now >= deadline(pending))) probes.add(session.row.id);
    }
  }

  function requestFailed(error: unknown): void {
    reportOnce("requests", message(error), "agentsview.request_failed", `An AgentsView request failed: ${message(error)}`);
  }

  /** Reads the most recent sessions, then pages of the complete session
   * list, then sets display names and sends direct requests, within the
   * request limit of one pass. The result is false after a network failure. */
  async function checkSessions(client: AgentsViewClient): Promise<boolean> {
    let budget = deps.requestsPerPass ?? REQUESTS_PER_PASS;
    const native = [...sessions.values()].filter(session => session.row.format === "opencode");
    for (let i = 0; i < native.length && budget > 8 && !closed; i++) {
      const session = native[(nativeImportCursor + i) % native.length]!;
      const state = session.native;
      if (!state) {
        if (session.legacy?.position) {
          budget--;
          await client.rename(`codex:${openCodeTraceID(session.row.native_session_ref)}`, `OpenCode export · ${session.row.display_name}`, controller.signal).catch(requestFailed);
        }
        continue;
      }
      try {
        while (state.queue.length && budget > 8 && !closed && session.native === state) {
          const id = state.queue[0]!;
          budget--;
          await client.syncNative(`opencode:${id}`, controller.signal);
          if (session.native !== state) break;
          state.queue.shift();
          if (state.queue.length === 0) state.imported = state.digest;
        }
        if (session.native !== state) continue;
        session.nativeImportCause = undefined;
        if (state.imported === state.digest && !session.exportRetired) {
          const legacy = `codex:${openCodeTraceID(session.row.native_session_ref)}`;
          budget -= 3;
          await client.retireExport(legacy, session.row.agentsview_session, controller.signal);
          if (session.legacy) {
            await rm(session.legacy.dest, { force: true });
            await rm(join(paths.inspection, session.row.id, session.legacy.label), { force: true });
            session.legacy = undefined;
          }
          await publishNativeFile(paths.opencode, session.main.dest + ".retired", session.row.agentsview_session + "\n");
          session.exportRetired = true;
        }
        await publishAvailability(session, [session.main, ...(session.legacy ? [session.legacy] : [])]);
      } catch (error) {
        session.nativeImportCause = message(error);
        await publishAvailability(session, [session.main, ...(session.legacy ? [session.legacy] : [])]);
        if (session.pending && deps.now() >= deadline(session.pending)) session.lateAt = deps.now();
        requestFailed(error);
      }
    }
    nativeImportCursor = native.length ? (nativeImportCursor + 1) % native.length : 0;
    const index = new Map<string, SessionState>();
    for (const session of sessions.values()) {
      if ((session.main.position ?? 0) > 0) index.set(session.row.agentsview_session, session);
    }
    const record = async (page: AgentsViewPage) => {
      const at = deps.now();
      for (const row of page.sessions) {
        const session = index.get(row.id);
        if (session === undefined) continue;
        session.listedAt = at;
        await observe(session, row, at);
      }
    };
    try {
      budget -= 1;
      await record(await client.listSessions({ limit: deps.recentPageSize ?? RECENT_PAGE_SIZE }, controller.signal));
      const now = deps.now();
      if (sweep === undefined && sweepWanted(now)) sweep = { cursor: null, startedAt: now };
      for (let pages = 0; sweep !== undefined && pages < (deps.sweepPagesPerPass ?? SWEEP_PAGES_PER_PASS) && budget > 0; pages += 1) {
        const current: Sweep = sweep;
        budget -= 1;
        let page: AgentsViewPage;
        try {
          page = await client.listSessions({
            limit: deps.sweepPageSize ?? SWEEP_PAGE_SIZE, order: "id",
            ...(current.cursor === null ? {} : { cursor: current.cursor }),
          }, controller.signal);
        } catch (error) {
          // AgentsView refuses a cursor that it did not make. The list starts again.
          if (!(error instanceof AgentsViewAnswer) || error.status !== 400 || current.cursor === null) throw error;
          sweep = undefined;
          break;
        }
        await record(page);
        if (page.nextCursor === null) finishSweep(current, index);
        else current.cursor = page.nextCursor;
      }
      failures.delete("list");
    } catch (error) {
      if (closed) return false;
      if (!(error instanceof AgentsViewAnswer)) {
        requestFailed(error);
        return false;
      }
      reportOnce("list", message(error), "agentsview.request_failed",
        `The resident cannot read the AgentsView session list: ${message(error)}`);
    }
    for (const id of [...renames]) {
      if (closed || budget <= 0) break;
      renames.delete(id);
      const session = sessions.get(id);
      if (session === undefined || session.shownName === session.row.display_name) continue;
      const { kind, agentsview_session: target, display_name: name } = session.row;
      budget -= 1;
      try {
        await client.rename(target, name, controller.signal);
        session.shownName = name;
        failures.delete(`name:${id}`);
      } catch (error) {
        if (closed) return false;
        if (!(error instanceof AgentsViewAnswer)) {
          renames.add(id);
          requestFailed(error);
          return false;
        }
        reportOnce(`name:${id}`, message(error), "agentsview.request_failed",
          `The resident cannot set the display name of the AgentsView session ${target} of ${kind} ${id}: ${message(error)}`,
          { kind, id });
      }
    }
    for (const id of [...probes]) {
      if (closed || budget <= 0) break;
      probes.delete(id);
      const session = sessions.get(id);
      if (session === undefined) continue;
      const { kind, agentsview_session: target } = session.row;
      budget -= 1;
      try {
        await observe(session, await client.session(target, controller.signal), deps.now());
        failures.delete(`probe:${id}`);
      } catch (error) {
        if (closed) return false;
        if (!(error instanceof AgentsViewAnswer)) {
          probes.add(id);
          requestFailed(error);
          return false;
        }
        reportOnce(`probe:${id}`, message(error), "agentsview.request_failed",
          `The resident cannot read the AgentsView session ${target} of ${kind} ${id}: ${message(error)}`, { kind, id });
      }
    }
    return true;
  }

  async function reconcilePassNow(): Promise<void> {
    const client = deps.agentsView;
    if (client === undefined) return;
    const now = deps.now();
    let pid: number;
    try {
      pid = await client.ping(controller.signal);
    } catch (error) {
      if (closed) return;
      if (availability !== "unavailable") {
        deps.log.warn("agentsview.unavailable",
          `AgentsView at ${client.bind} is unavailable, so trace links do not open: ${message(error)}`,
          { bind: client.bind });
      }
      availability = "unavailable";
      await publishHealth([]);
      return;
    }
    if (availability !== "available" || pid !== agentsViewPid) {
      deps.log.info("agentsview.available", `AgentsView answers at ${client.bind} with process ${pid}.`,
        { bind: client.bind, pid });
    }
    availability = "available";
    if (pid !== agentsViewPid) {
      // A new process can have a new archive. The rules go in again, and the
      // lane checks each copy and each display name again.
      agentsViewPid = pid;
      availableSince = now;
      rulesReady = false;
      sweep = undefined;
      renames.clear();
      probes.clear();
      for (const session of sessions.values()) {
        const position = session.main.position ?? 0;
        if (position > 0) session.pending ??= { since: now, position };
        if (session.native) {
          session.native.imported = undefined;
          session.native.queue = [...session.native.members];
        }
      }
    }
    let reachable = true;
    if (!rulesReady) {
      try {
        await client.ensureProjectRules(controller.signal);
        rulesReady = true;
        failures.delete("rules");
      } catch (error) {
        if (closed) return;
        reachable = error instanceof AgentsViewAnswer;
        reportOnce("rules", message(error), "agentsview.request_failed",
          `The resident cannot set the AgentsView project rules: ${message(error)}`);
      }
    }
    if (reachable && await checkSessions(client)) failures.delete("requests");
    if (closed) return;
    await publishHealth(overdue());
  }

  function reconcilePass(): Promise<void> {
    const pass = reconcileChain.then(() => (closed ? undefined : reconcilePassNow()));
    reconcileChain = pass.catch(() => undefined);
    return pass;
  }

  // The next copy pass follows the end of the copies of this pass. A session
  // list that runs longer does not delay it.
  function scheduleCopy(ms: number): void {
    if (closed) return;
    copyTimer = deps.schedule(() => {
      copyTimer = undefined;
      void runCopyPass()
        .catch((error) => reportOnce("copy-pass", message(error), "trace.copy_failed",
          `The trace copy pass failed: ${message(error)}`))
        .finally(() => scheduleCopy(COPY_INTERVAL_MS));
    }, ms);
  }

  function scheduleReconcile(ms: number): void {
    if (closed || deps.agentsView === undefined) return;
    reconcileTimer = deps.schedule(() => {
      reconcileTimer = undefined;
      void reconcilePass()
        .catch((error) => reportOnce("reconcile-pass", message(error), "agentsview.request_failed",
          `The AgentsView pass failed: ${message(error)}`))
        .finally(() => scheduleReconcile(RECONCILE_INTERVAL_MS));
    }, ms);
  }

  return {
    start() {
      if (closed || started) return;
      started = true;
      scheduleCopy(0);
      scheduleReconcile(0);
    },
    copyPass,
    reconcilePass,
    async stop() {
      closed = true;
      if (copyTimer !== undefined) deps.cancel(copyTimer);
      if (reconcileTimer !== undefined) deps.cancel(reconcileTimer);
      copyTimer = undefined;
      reconcileTimer = undefined;
      controller.abort();
      await Promise.all([copyChain, reconcileChain, refreshing?.catch(() => undefined)]);
    },
  };
}
