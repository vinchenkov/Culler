// trace-lane.test.ts — the trace lane in real temporary folders with
// synthetic Claude and Codex traces, a scripted mc, a fake AgentsView
// server, and a manual clock.

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { appendFile, mkdir, mkdtemp, readdir, readFile, rename, rm, stat, symlink, utimes, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { makeLogger, type LogRecord } from "../../runner/shared/log";
import {
  agentsViewClient, agentsViewTokenPath, parseAgentsViewBind, readAgentsViewToken, type AgentsViewClient, type Fetch,
} from "./agentsview";
import {
  COPY_INTERVAL_MS, copyMark, createTraceLane, OVERDUE_MS, parseTraceSessions, printable, RECHECK_INTERVAL_MS,
  RECONCILE_INTERVAL_MS, SESSIONS_MIN_INTERVAL_MS, SESSIONS_REFRESH_MS, SESSIONS_TIMEOUT_MS, SETTLE_MS, traceCopyPaths,
  type TraceLane, type TraceLaneDeps, type TraceSession,
} from "./trace-lane";
import { FakeScheduler, ok } from "./test-helpers";
import type { ExecResult } from "./types";

const CLAUDE_REF = "11111111-1111-4111-8111-111111111111";
const CODEX_REF = "22222222-2222-4222-8222-222222222222";
const CODEX_CHILD_REF = "33333333-3333-4333-8333-333333333333";
const CODEX_FILE = `2026/09/29/rollout-2026-09-29T10-00-00-${CODEX_REF}.jsonl`;
const CODEX_CHILD_FILE = `2026/09/29/rollout-2026-09-29T10-05-00-${CODEX_CHILD_REF}.jsonl`;
const TOKEN = "a1".repeat(32);
const NEXT_TOKEN = "b2".repeat(32);

function claudeLine(n: number, sessionId = CLAUDE_REF): string {
  return JSON.stringify({
    type: n % 2 === 0 ? "assistant" : "user", sessionId, uuid: `claude-${n}`, cwd: "/workspace/source",
    message: { role: n % 2 === 0 ? "assistant" : "user", content: `Synthetic Claude line ${n}.` },
  }) + "\n";
}

/** A Claude line with a time, as the Claude harness writes it. */
function timedLine(n: number, at: string, sessionId = CLAUDE_REF): string {
  return JSON.stringify({
    type: "assistant", sessionId, uuid: `claude-${n}`, cwd: "/workspace/source", timestamp: at,
    message: { role: "assistant", content: `Synthetic Claude line ${n}.` },
  }) + "\n";
}

/** The time of each synthetic Codex line. */
const CODEX_TIME = "2026-09-29T10:00:00.000Z";

function codexLine(n: number, id = CODEX_REF): string {
  const payload = n === 0
    ? { type: "session_meta", payload: { id, cwd: "/workspace/source", originator: "codex_exec" } }
    : { type: "response_item", payload: { type: "message", role: "assistant", content: `Synthetic Codex line ${n}.` } };
  return JSON.stringify({ timestamp: CODEX_TIME, ...payload }) + "\n";
}

function claudeRow(id: string, fields: Partial<TraceSession> = {}): TraceSession {
  return {
    kind: "run", id, session_path: `sessions/${id}`, format: "claude", native_session_ref: CLAUDE_REF,
    trace_filename: "native.jsonl", agentsview_session: CLAUDE_REF,
    display_name: `Culler · worker · Add the trace lane · ${id}`, ended: false, ...fields,
  };
}

function codexRow(id: string, fields: Partial<TraceSession> = {}): TraceSession {
  return {
    kind: "collie", id, session_path: `sessions/${id}`, format: "codex", native_session_ref: CODEX_REF,
    trace_filename: CODEX_FILE, agentsview_session: `codex:${CODEX_REF}`,
    display_name: `Collie · Culler · ${id}`, ended: false, ...fields,
  };
}

interface FakeSession {
  display_name: string | null;
  /** The time of the last imported line. */
  ended_at?: string | null;
  /** A child session, which the default session list leaves out. */
  child?: boolean;
}

/** A fake AgentsView server. Each API request needs the bearer token, and
 * mutating requests need the Host and Origin of the bind, as in AgentsView.
 * The session list gives one-shot and automated sessions only with both
 * include flags, as AgentsView does. Each Culler session is both. */
class FakeAgentsView {
  pid = 4242;
  token = TOKEN;
  mappings: Array<{ id: number; path_prefix: string; project: string; enabled: boolean }> = [];
  sessions = new Map<string, FakeSession>();
  requests: Array<{
    method: string; path: string; host: string | null; origin: string | null; authorization: string | null; body: unknown;
  }> = [];
  lists: URLSearchParams[] = [];
  refuseCursors = false;
  down = false;
  private nextMapping = 1;
  server = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch: (request) => this.handle(request) });

  get bind(): string {
    return `127.0.0.1:${this.server.port}`;
  }

  client(): AgentsViewClient {
    const fetcher: Fetch = (url, init) => this.down
      ? Promise.reject(new Error("Unable to connect"))
      : fetch(url, init);
    return agentsViewClient(this.bind, { token: () => readAgentsViewToken(agentsViewTokenPath(home)), fetcher });
  }

  requestsOf(method: string, prefix: string) {
    return this.requests.filter((request) => request.method === method && request.path.startsWith(prefix));
  }

  /** A new process that lost its archive. */
  restartWithoutArchive(): void {
    this.pid += 1;
    this.mappings = [];
    for (const session of this.sessions.values()) session.display_name = null;
  }

  private list(params: URLSearchParams): Response {
    this.lists.push(params);
    const rows = params.get("include_one_shot") !== "true" || params.get("include_automated") !== "true" ? [] :
      [...this.sessions.entries()].filter(([, session]) => !session.child).map(([id, session]) => ({
        id, ...(session.display_name === null ? {} : { display_name: session.display_name }), ended_at: session.ended_at ?? null,
      }));
    if (params.get("order_by") === "id:asc") rows.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    else rows.sort((a, b) => (b.ended_at ?? "").localeCompare(a.ended_at ?? "") || (a.id < b.id ? -1 : 1));
    const limit = Number(params.get("limit") ?? 200);
    let offset = 0;
    const cursor = params.get("cursor");
    if (cursor !== null) {
      const match = /^([0-9]+)@([0-9]+)$/.exec(cursor);
      if (this.refuseCursors || match === null || Number(match[2]) !== this.pid) {
        return Response.json({ error: "invalid cursor" }, { status: 400 });
      }
      offset = Number(match[1]);
    }
    const next = offset + limit < rows.length ? { next_cursor: `${offset + limit}@${this.pid}` } : {};
    return Response.json({ sessions: rows.slice(offset, offset + limit), total: rows.length, ...next });
  }

  private async handle(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const body = request.method === "GET" ? undefined : await request.json().catch(() => undefined);
    this.requests.push({
      method: request.method, path: url.pathname, host: request.headers.get("host"),
      origin: request.headers.get("origin"), authorization: request.headers.get("authorization"), body,
    });
    if (request.headers.get("authorization") !== `Bearer ${this.token}`) {
      return new Response("Unauthorized", { status: 401 });
    }
    if (request.method !== "GET" &&
        (request.headers.get("host") !== this.bind || request.headers.get("origin") !== `http://${this.bind}`)) {
      return Response.json({ error: "forbidden" }, { status: 403 });
    }
    if (url.pathname === "/api/ping") return Response.json({ ok: true, service: "agentsview", pid: this.pid });
    if (url.pathname === "/api/v1/settings/worktree-mappings") {
      if (request.method === "GET") return Response.json({ machine: "local", mappings: this.mappings });
      const rule = body as { path_prefix: string; project: string; enabled: boolean };
      if (this.mappings.some((mapping) => mapping.path_prefix === rule.path_prefix)) {
        return Response.json({ error: "worktree mapping already exists" }, { status: 409 });
      }
      const created = { id: this.nextMapping++, ...rule };
      this.mappings.push(created);
      return Response.json(created, { status: 201 });
    }
    const mapping = /^\/api\/v1\/settings\/worktree-mappings\/([0-9]+)$/.exec(url.pathname);
    if (mapping !== null && request.method === "PUT") {
      const index = this.mappings.findIndex((item) => item.id === Number(mapping[1]));
      this.mappings[index] = { id: Number(mapping[1]), ...(body as { path_prefix: string; project: string; enabled: boolean }) };
      return Response.json(this.mappings[index]);
    }
    if (url.pathname === "/api/v1/settings/worktree-mappings/apply") {
      return Response.json({ machine: "local", matched_sessions: 0, updated_sessions: 0 });
    }
    if (url.pathname === "/api/v1/sessions" && request.method === "GET") return this.list(url.searchParams);
    const session = /^\/api\/v1\/sessions\/([^/]+)(\/rename)?$/.exec(url.pathname);
    if (session !== null) {
      const id = decodeURIComponent(session[1]!);
      const found = this.sessions.get(id);
      if (found === undefined) return Response.json({ error: "session not found" }, { status: 404 });
      if (session[2] === undefined) {
        return Response.json({ id, display_name: found.display_name ?? undefined, ended_at: found.ended_at ?? null });
      }
      found.display_name = (body as { display_name: string }).display_name;
      return Response.json({ id, display_name: found.display_name });
    }
    return Response.json({ error: "not found" }, { status: 404 });
  }
}

class TraceRig {
  scheduler = new FakeScheduler();
  rows: TraceSession[] = [];
  listCalls = 0;
  list: () => ExecResult | Promise<ExecResult> = () => ok(JSON.stringify({ sessions: this.rows }));
  records: LogRecord[] = [];
  lane: TraceLane;
  paths: ReturnType<typeof traceCopyPaths>;

  constructor(readonly home: string, extra: Partial<TraceLaneDeps> = {}) {
    this.scheduler.now = Date.now();
    this.paths = traceCopyPaths(home);
    this.lane = this.build(extra);
  }

  build(extra: Partial<TraceLaneDeps> = {}): TraceLane {
    return createTraceLane({
      runMc: async (argv) => {
        if (argv[0] !== "__trace-sessions") throw new Error(`unexpected mc ${argv.join(" ")}`);
        this.listCalls += 1;
        return this.list();
      },
      log: makeLogger({
        comp: "resident", level: "debug",
        sink: { write: (line) => { this.records.push(JSON.parse(line) as LogRecord); } },
      }),
      mcHome: this.home,
      schedule: this.scheduler.schedule,
      cancel: this.scheduler.cancel,
      now: this.scheduler.clock,
      ...extra,
    });
  }

  events(name: string): LogRecord[] {
    return this.records.filter((record) => record.event === name);
  }

  session(id: string): string {
    return join(this.home, "sessions", id);
  }

  async write(id: string, relative: string, text: string): Promise<string> {
    const path = join(this.session(id), relative);
    await mkdir(join(path, ".."), { recursive: true });
    await writeFile(path, text);
    return path;
  }

  async health(): Promise<{ observed_at: string; overdue: unknown[] }> {
    return JSON.parse(await readFile(this.paths.health, "utf8"));
  }

  async at(ms: number): Promise<void> {
    this.scheduler.now += ms;
  }
}

async function until(condition: () => boolean, limit = 2_000): Promise<void> {
  const deadline = Date.now() + limit;
  while (!condition()) {
    if (Date.now() > deadline) throw new Error("the condition did not become true");
    await new Promise((resolve) => setTimeout(resolve, 2));
  }
}

let home: string;
const lanes: TraceLane[] = [];
const servers: FakeAgentsView[] = [];

function rig(extra: Partial<TraceLaneDeps> = {}): TraceRig {
  const made = new TraceRig(home, extra);
  lanes.push(made.lane);
  return made;
}

function agentsView(): FakeAgentsView {
  const server = new FakeAgentsView();
  servers.push(server);
  return server;
}

async function writeToken(token: string): Promise<void> {
  await mkdir(join(home, "agentsview"), { recursive: true, mode: 0o700 });
  await writeFile(agentsViewTokenPath(home), `${token}\n`, { mode: 0o600 });
}

beforeEach(async () => {
  home = await mkdtemp(join(tmpdir(), "trace-lane-"));
  await mkdir(join(home, "sessions"), { recursive: true });
  await writeToken(TOKEN);
});

afterEach(async () => {
  await Promise.all(lanes.splice(0).map((lane) => lane.stop()));
  for (const server of servers.splice(0)) server.server.stop(true);
  await rm(home, { recursive: true, force: true });
});

const read = (path: string) => readFile(path, "utf8");

describe("trace copies", () => {
  test("Claude and Codex traces go into the stock AgentsView layouts", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude"), codexRow("h-codex")];
    const main = claudeLine(1) + claudeLine(2);
    await r.write("r-claude", "native.jsonl", main + '{"partial":');
    await r.write("r-claude", "subagents/agent-a1.jsonl", claudeLine(3, "agent-a1"));
    await r.write("r-claude", "subagents/workflows/w1/agent-b2.jsonl", claudeLine(4, "agent-b2"));
    await r.write("r-claude", "subagents/notes.txt", "not a trace\n");
    await r.write("r-claude", "other.jsonl", claudeLine(5));
    await r.write("h-codex", CODEX_FILE, codexLine(0) + codexLine(1));
    await r.write("h-codex", CODEX_CHILD_FILE, codexLine(0, CODEX_CHILD_REF));
    await r.write("h-codex", "native.jsonl", codexLine(2));
    await r.write("r-fake", "native.jsonl", '{"fake":true}\n');

    await r.lane.copyPass();

    const claude = r.paths.claude;
    expect(await read(join(claude, `${CLAUDE_REF}.jsonl`))).toBe(main);
    expect(await read(join(claude, CLAUDE_REF, "subagents", "agent-a1.jsonl"))).toBe(claudeLine(3, "agent-a1"));
    expect(await read(join(claude, CLAUDE_REF, "subagents", "workflows", "w1", "agent-b2.jsonl")))
      .toBe(claudeLine(4, "agent-b2"));
    expect(await Bun.file(join(claude, CLAUDE_REF, "subagents", "notes.txt")).exists()).toBe(false);
    expect(await Bun.file(join(claude, "other.jsonl")).exists()).toBe(false);
    expect(await Bun.file(join(claude, "native.jsonl")).exists()).toBe(false);
    expect(await read(join(r.paths.codex, CODEX_FILE))).toBe(codexLine(0) + codexLine(1));
    expect(await read(join(r.paths.codex, CODEX_CHILD_FILE))).toBe(codexLine(0, CODEX_CHILD_REF));
    expect(await Bun.file(join(r.paths.codex, "native.jsonl")).exists()).toBe(false);
    expect((await stat(join(claude, `${CLAUDE_REF}.jsonl`))).mode & 0o777).toBe(0o644);
    expect((await stat(join(r.paths.codex, CODEX_FILE))).mode & 0o777).toBe(0o644);
    expect((await stat(join(r.paths.codex, "2026", "09", "29"))).mode & 0o777).toBe(0o700);
    expect((await stat(join(home, "agentsview"))).mode & 0o777).toBe(0o700);
    expect(r.records.filter((record) => record.level !== "info")).toEqual([]);
  });

  test("a new line of an active trace reaches the copy at the next pass", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude")];
    const source = await r.write("r-claude", "native.jsonl", claudeLine(1));
    await r.lane.copyPass();
    await appendFile(source, claudeLine(2));
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await read(join(r.paths.claude, `${CLAUDE_REF}.jsonl`))).toBe(claudeLine(1) + claudeLine(2));
  });

  test("a restart of the resident copies each new line once, with no gap", async () => {
    const first = rig();
    first.rows = [claudeRow("r-claude"), codexRow("h-codex")];
    const claude = await first.write("r-claude", "native.jsonl", claudeLine(1) + claudeLine(2).slice(0, 7));
    const child = await first.write("r-claude", "subagents/agent-a1.jsonl", claudeLine(3, "agent-a1"));
    const codex = await first.write("h-codex", CODEX_FILE, codexLine(0));
    await first.lane.copyPass();
    await first.lane.stop();

    await appendFile(claude, claudeLine(2).slice(7) + claudeLine(4));
    await appendFile(child, claudeLine(5, "agent-a1"));
    await appendFile(codex, codexLine(1));
    const second = rig();
    second.rows = first.rows;
    await second.lane.copyPass();

    expect(await read(join(second.paths.claude, `${CLAUDE_REF}.jsonl`))).toBe(claudeLine(1) + claudeLine(2) + claudeLine(4));
    expect(await read(join(second.paths.claude, CLAUDE_REF, "subagents", "agent-a1.jsonl")))
      .toBe(claudeLine(3, "agent-a1") + claudeLine(5, "agent-a1"));
    expect(await read(join(second.paths.codex, CODEX_FILE))).toBe(codexLine(0) + codexLine(1));
  });

  test("a shorter or replaced source gives a complete new copy", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude")];
    const source = await r.write("r-claude", "native.jsonl", claudeLine(1) + claudeLine(2) + claudeLine(3));
    await r.lane.copyPass();
    await writeFile(source, claudeLine(1));
    await r.lane.copyPass();
    expect(await read(join(r.paths.claude, `${CLAUDE_REF}.jsonl`))).toBe(claudeLine(1));
    await writeFile(join(r.session("r-claude"), "next.jsonl"), claudeLine(7) + claudeLine(8));
    await rename(join(r.session("r-claude"), "next.jsonl"), source);
    await r.lane.copyPass();
    expect(await read(join(r.paths.claude, `${CLAUDE_REF}.jsonl`))).toBe(claudeLine(7) + claudeLine(8));
  });

  test("a file that fails stops no other file, and the log names it once", async () => {
    const r = rig();
    const blocked = claudeRow("r-blocked", { native_session_ref: "44444444-4444-4444-8444-444444444444",
      agentsview_session: "44444444-4444-4444-8444-444444444444" });
    r.rows = [blocked, claudeRow("r-claude"), codexRow("h-codex")];
    await r.write("r-blocked", "native.jsonl", claudeLine(1));
    await r.write("r-claude", "native.jsonl", claudeLine(1));
    const codex = await r.write("h-codex", CODEX_FILE, codexLine(0));
    await mkdir(join(r.paths.claude, "44444444-4444-4444-8444-444444444444.jsonl"), { recursive: true });
    await r.lane.copyPass();
    await appendFile(codex, codexLine(1));
    await r.lane.copyPass();
    await r.lane.copyPass();

    expect(await read(join(r.paths.claude, `${CLAUDE_REF}.jsonl`))).toBe(claudeLine(1));
    expect(await read(join(r.paths.codex, CODEX_FILE))).toBe(codexLine(0) + codexLine(1));
    const failed = r.events("trace.copy_failed");
    expect(failed).toHaveLength(1);
    expect(failed[0]).toMatchObject({
      level: "warn", kind: "run", id: "r-blocked", file: "native.jsonl",
      msg: "The resident cannot copy the trace file native.jsonl of run r-blocked: the trace copy is not a regular file",
    });

    await rm(join(r.paths.claude, "44444444-4444-4444-8444-444444444444.jsonl"), { recursive: true });
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await read(join(r.paths.claude, "44444444-4444-4444-8444-444444444444.jsonl"))).toBe(claudeLine(1));
  });

  test("a file that keeps failing waits longer before each new attempt", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude")];
    await r.write("r-claude", "native.jsonl", claudeLine(1));
    const copy = join(r.paths.claude, `${CLAUDE_REF}.jsonl`);
    await mkdir(copy, { recursive: true });
    await r.lane.copyPass();
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    await rm(copy, { recursive: true });
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await Bun.file(copy).exists()).toBe(false);
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await read(copy)).toBe(claudeLine(1));
  });

  test("an ended session settles although one trace file keeps failing", async () => {
    const r = rig({ limits: { chunkBytes: 16, maxLineBytes: 64 } });
    r.rows = [claudeRow("r-claude", { ended: true })];
    await r.write("r-claude", "native.jsonl", claudeLine(1).slice(0, 60) + "\n");
    await r.write("r-claude", "subagents/agent-a1.jsonl", `${"x".repeat(200)}\n`);
    for (const wait of [0, COPY_INTERVAL_MS, COPY_INTERVAL_MS * 2, SETTLE_MS]) {
      await r.at(wait);
      await r.lane.copyPass();
    }
    await r.write("r-claude", "subagents/agent-b2.jsonl", claudeLine(2, "agent-b2"));
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await Bun.file(join(r.paths.claude, CLAUDE_REF, "subagents", "agent-b2.jsonl")).exists()).toBe(false);
    expect(r.events("trace.copy_failed").map((record) => record.msg)).toEqual([
      "The resident cannot copy the trace file subagents/agent-a1.jsonl of run r-claude: a trace line is longer than 64 bytes",
    ]);
  });

  test("a settled session gets a complete new copy when its copy disappears", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude", { ended: true })];
    await r.write("r-claude", "native.jsonl", claudeLine(1) + claudeLine(2));
    await r.lane.copyPass();
    await r.at(SETTLE_MS);
    await r.lane.copyPass();
    const copy = join(r.paths.claude, `${CLAUDE_REF}.jsonl`);
    await rm(copy);
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await read(copy)).toBe(claudeLine(1) + claudeLine(2));
  });

  test("the lane copies at most the file limit of one session and logs the limit once", async () => {
    const r = rig({ maxSessionFiles: 3 });
    r.rows = [claudeRow("r-claude")];
    await r.write("r-claude", "native.jsonl", claudeLine(1));
    for (const name of ["a", "b", "c", "d"]) {
      await r.write("r-claude", `subagents/agent-${name}.jsonl`, claudeLine(2, `agent-${name}`));
    }
    await r.lane.copyPass();
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    const children = join(r.paths.claude, CLAUDE_REF, "subagents");
    expect((await readdir(children)).filter(name => name.endsWith(".jsonl")).sort()).toEqual(["agent-a.jsonl", "agent-b.jsonl"]);
    expect(r.events("trace.copy_failed").map((record) => record.msg)).toEqual([
      "The session folder of run r-claude holds more than 3 trace files or 256 folders. " +
        "The resident copies at most 3 trace files of one session.",
    ]);
  });

  test("the lane forgets a trace file that disappears", async () => {
    const r = rig({ limits: { chunkBytes: 16, maxLineBytes: 64 } });
    r.rows = [claudeRow("r-claude")];
    await r.write("r-claude", "native.jsonl", "{}\n");
    const child = await r.write("r-claude", "subagents/agent-a1.jsonl", `${"x".repeat(200)}\n`);
    await r.lane.copyPass();
    await rm(child);
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    await r.write("r-claude", "subagents/agent-a1.jsonl", `${"x".repeat(200)}\n`);
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(r.events("trace.copy_failed")).toHaveLength(2);
  });

  test("a log record escapes each control character in a folder name of an agent", async () => {
    const r = rig({ limits: { chunkBytes: 16, maxLineBytes: 64 } });
    r.rows = [claudeRow("r-claude")];
    await r.write("r-claude", "native.jsonl", "{}\n");
    const folder = "x\n2026-09-29T12:00:00Z INFO  resident tick.completed";
    await r.write("r-claude", `subagents/${folder}/agent-2.jsonl`, `${"x".repeat(200)}\n`);
    await r.lane.copyPass();
    const [record] = r.events("trace.copy_failed");
    const escaped = "subagents/x\\u000a2026-09-29T12:00:00Z INFO  resident tick.completed/agent-2.jsonl";
    expect(record!.msg).toBe(`The resident cannot copy the trace file ${escaped} of run r-claude: a trace line is longer than 64 bytes`);
    expect(record!.file).toBe(escaped);
    expect(printable("a\\b\u2028c\u0085")).toBe("a\\\\b\\u2028c\\u0085");
  });

  test("the lane follows no symbolic link in a session folder", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude")];
    const outside = join(home, "outside");
    await mkdir(outside);
    await writeFile(join(outside, "agent-x.jsonl"), "outside\n");
    await writeFile(join(outside, "native.jsonl"), "outside\n");
    await mkdir(join(r.session("r-claude"), "subagents"), { recursive: true });
    await symlink(join(outside, "agent-x.jsonl"), join(r.session("r-claude"), "subagents", "agent-x.jsonl"));
    await symlink(outside, join(r.session("r-claude"), "subagents", "linked"));
    await symlink(join(outside, "native.jsonl"), join(r.session("r-claude"), "native.jsonl"));
    await r.lane.copyPass();
    expect(await Bun.file(join(r.paths.claude, `${CLAUDE_REF}.jsonl`)).exists()).toBe(false);
    expect(await Bun.file(join(r.paths.claude, CLAUDE_REF)).exists()).toBe(false);
    expect(r.events("trace.copy_failed").map((record) => record.msg))
      .toEqual(["The resident cannot copy the trace file native.jsonl of run r-claude: the trace file is a symbolic link"]);
  });

  test("a folder that an agent swaps for a symbolic link stops the copy of each trace below it", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude", { trace_filename: "nested/native.jsonl" })];
    const outside = join(home, "outside");
    await mkdir(outside);
    await r.write("r-claude", "nested/native.jsonl", claudeLine(1));
    await r.lane.copyPass();
    const copy = join(r.paths.claude, `${CLAUDE_REF}.jsonl`);
    expect(await read(copy)).toBe(claudeLine(1));

    await writeFile(join(outside, "native.jsonl"), claudeLine(1) + '{"secret":"outside the session folder"}\n');
    await rename(join(r.session("r-claude"), "nested"), join(home, "nested-real"));
    await symlink(outside, join(r.session("r-claude"), "nested"));
    await r.lane.copyPass();
    await r.lane.copyPass();
    expect(await read(copy)).toBe(claudeLine(1));
    expect(r.events("trace.copy_failed").map((record) => record.msg)).toEqual([
      "The resident cannot copy the trace file nested/native.jsonl of run r-claude: " +
        "the trace file resolves outside its session folder",
    ]);

    await rm(join(r.session("r-claude"), "nested"));
    await rename(join(home, "nested-real"), join(r.session("r-claude"), "nested"));
    await appendFile(join(r.session("r-claude"), "nested", "native.jsonl"), claudeLine(2));
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await read(copy)).toBe(claudeLine(1) + claudeLine(2));
  });

  test("an ended session gets passes until its traces stop changing for a minute", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude", { ended: true })];
    const source = await r.write("r-claude", "native.jsonl", claudeLine(1));
    await r.lane.copyPass();
    await appendFile(source, claudeLine(2));
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await read(join(r.paths.claude, `${CLAUDE_REF}.jsonl`))).toBe(claudeLine(1) + claudeLine(2));
    await r.at(SETTLE_MS);
    await r.lane.copyPass();
    await appendFile(source, claudeLine(3));
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await read(join(r.paths.claude, `${CLAUDE_REF}.jsonl`))).toBe(claudeLine(1) + claudeLine(2));
  });

  test("an ended session without its trace file logs the absent file once", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude", { ended: true }), claudeRow("r-active", {
      native_session_ref: "55555555-5555-4555-8555-555555555555", agentsview_session: "55555555-5555-4555-8555-555555555555",
    })];
    await mkdir(r.session("r-claude"));
    await mkdir(r.session("r-active"));
    await r.lane.copyPass();
    await r.lane.copyPass();
    expect(r.events("trace.copy_failed").map((record) => record.msg))
      .toEqual(["The resident cannot copy the trace file native.jsonl of run r-claude: the trace file is absent"]);
  });
});

describe("the session list", () => {
  test("the lane lists sessions at start, for a new folder, and at least each minute, at most each 5 seconds", async () => {
    const r = rig();
    const old = join(home, "sessions", "r-old");
    await mkdir(old);
    const hourAgo = new Date(Date.now() - 3_600_000);
    await utimes(old, hourAgo, hourAgo);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(1);
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(1);

    await mkdir(r.session("r-new"));
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(1);
    await r.at(SESSIONS_MIN_INTERVAL_MS - COPY_INTERVAL_MS * 2);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(2);

    // The new folder has no native session ID yet.
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(2);
    await r.at(SESSIONS_MIN_INTERVAL_MS - COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(3);

    r.rows = [claudeRow("r-new")];
    await r.write("r-new", "native.jsonl", claudeLine(1));
    await r.at(SESSIONS_MIN_INTERVAL_MS);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(4);
    expect(await read(join(r.paths.claude, `${CLAUDE_REF}.jsonl`))).toBe(claudeLine(1));

    await r.at(SESSIONS_REFRESH_MS - COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(4);
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(5);
  });

  test("a slow session list does not delay the copies of the known sessions", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude")];
    const source = await r.write("r-claude", "native.jsonl", claudeLine(1));
    await r.lane.copyPass();
    let answer: ((result: ExecResult) => void) | undefined;
    r.list = () => new Promise((resolve) => { answer = resolve; });
    await appendFile(source, claudeLine(2));
    await r.at(SESSIONS_REFRESH_MS);
    const pass = r.lane.copyPass();
    const copy = join(r.paths.claude, `${CLAUDE_REF}.jsonl`);
    let text = "";
    await until(() => {
      void read(copy).then((value) => { text = value; });
      return answer !== undefined && text === claudeLine(1) + claudeLine(2);
    });
    answer!(ok(JSON.stringify({ sessions: r.rows })));
    await pass;
  });

  test("a failed list logs once and repeats after 5 seconds", async () => {
    const r = rig();
    r.list = () => ({ exitCode: 2, stdout: "", stderr: "mc: helper unavailable" });
    await r.lane.copyPass();
    await r.at(SESSIONS_MIN_INTERVAL_MS);
    await r.lane.copyPass();
    expect(r.listCalls).toBe(2);
    expect(r.events("trace.sessions_failed").map((record) => record.msg))
      .toEqual(["The resident cannot read the trace sessions: mc __trace-sessions exited 2: mc: helper unavailable"]);
    r.rows = [claudeRow("r-claude")];
    r.list = () => ok(JSON.stringify({ sessions: r.rows }));
    await r.write("r-claude", "native.jsonl", claudeLine(1));
    await r.at(SESSIONS_MIN_INTERVAL_MS);
    await r.lane.copyPass();
    expect(await read(join(r.paths.claude, `${CLAUDE_REF}.jsonl`))).toBe(claudeLine(1));
  });

  test("the lane ignores each row with an unsafe path and copies the others", async () => {
    const parsed = parseTraceSessions(JSON.stringify({ sessions: [
      claudeRow("r-claude"),
      claudeRow("r-up", { trace_filename: "../../outside.jsonl" }),
      claudeRow("r-ref", { native_session_ref: "../escape" }),
      claudeRow("r-path", { session_path: "sessions/other" }),
      codexRow("h-codex", { trace_filename: "native.jsonl" }),
      { ...claudeRow("r-format"), format: "fake" },
    ] }));
    expect(parsed.sessions.map((row) => row.id)).toEqual(["r-claude"]);
    expect(parsed.invalid).toEqual([
      { id: "r-up", cause: "the trace file name is not valid" },
      { id: "r-ref", cause: "the native session ID is not valid" },
      { id: "r-path", cause: "the session path does not match its operation" },
      { id: "h-codex", cause: "the trace file name is not a Codex rollout" },
      { id: "r-format", cause: "the format is not claude or codex" },
    ]);
    expect(() => parseTraceSessions("{}")).toThrow("the output holds no session list");
  });
});

describe("the lane timers", () => {
  test("start runs a copy pass at once and the next pass 2 seconds after it ends", async () => {
    const r = rig();
    r.lane.start();
    await r.scheduler.advance(0);
    await until(() => r.listCalls === 1 && r.scheduler.pending().includes(COPY_INTERVAL_MS));
    await r.scheduler.advance(COPY_INTERVAL_MS - 1);
    expect(r.scheduler.pending()).toEqual([1]);
  });

  test("a session list that does not answer stops after 30 seconds, and the copies continue each 2 seconds", async () => {
    const r = rig();
    r.rows = [claudeRow("r-claude")];
    const source = await r.write("r-claude", "native.jsonl", claudeLine(1));
    await r.lane.copyPass();
    r.list = () => new Promise(() => undefined);
    await r.at(SESSIONS_REFRESH_MS);
    r.lane.start();
    await r.scheduler.advance(0);
    await until(() => r.listCalls === 2 && r.scheduler.pending().includes(COPY_INTERVAL_MS));
    const copy = join(r.paths.claude, `${CLAUDE_REF}.jsonl`);
    let expected = claudeLine(1);
    for (let line = 2; line <= 4; line += 1) {
      await appendFile(source, claudeLine(line));
      expected += claudeLine(line);
      await r.scheduler.advance(COPY_INTERVAL_MS);
      let text = "";
      await until(() => {
        void read(copy).then((value) => { text = value; });
        return text === expected && r.scheduler.pending().includes(COPY_INTERVAL_MS);
      });
    }
    expect(r.events("trace.sessions_failed")).toEqual([]);

    r.list = () => ok(JSON.stringify({ sessions: r.rows }));
    await r.scheduler.advance(SESSIONS_TIMEOUT_MS - 3 * COPY_INTERVAL_MS);
    await until(() => r.events("trace.sessions_failed").length === 1);
    expect(r.events("trace.sessions_failed")[0]!.msg)
      .toBe("The resident cannot read the trace sessions: mc __trace-sessions did not answer within 30 seconds");
    await r.scheduler.advance(SESSIONS_MIN_INTERVAL_MS);
    await until(() => r.listCalls === 3);
  });

  test("stop ends the session list call and waits for the pass", async () => {
    const r = rig();
    let signal: AbortSignal | undefined;
    const lane = r.build({
      runMc: (_argv, _stdin, options) => {
        signal = options?.signal;
        return new Promise((resolve) => {
          options?.signal?.addEventListener("abort", () => resolve({ exitCode: 143, stdout: "", stderr: "" }));
        });
      },
    });
    const pass = lane.copyPass();
    await until(() => signal !== undefined);
    await lane.stop();
    expect(signal!.aborted).toBe(true);
    await pass;
    expect(r.records.filter((record) => record.level === "warn")).toEqual([]);
  });
});

describe("AgentsView", () => {
  const CLAUDE_NAME = "Culler · worker · Add the trace lane · r-claude";
  const CODEX_NAME = "Collie · Culler · h-codex";

  async function copied(r: TraceRig): Promise<void> {
    r.rows = [claudeRow("r-claude"), codexRow("h-codex")];
    await r.write("r-claude", "native.jsonl", claudeLine(1));
    await r.write("r-claude", "subagents/agent-a1.jsonl", claudeLine(2, "agent-a1"));
    await r.write("h-codex", CODEX_FILE, codexLine(0));
    await r.lane.copyPass();
  }

  /** The requests of one pass after the ping and the project rules. */
  function sessionRequests(server: FakeAgentsView): string[] {
    return server.requests
      .filter((request) => request.path !== "/api/ping" && !request.path.startsWith("/api/v1/settings/"))
      .map((request) => `${request.method} ${request.path}`);
  }

  test("the first answer sets the project rules, then each display name", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    await copied(r);
    server.sessions.set(CLAUDE_REF, { display_name: "Synthetic Claude line 1." });
    server.sessions.set("agent-a1", { display_name: "child description", child: true });

    await r.lane.reconcilePass();
    expect(server.requests.map((request) => `${request.method} ${request.path}`)).toEqual([
      "GET /api/ping",
      "GET /api/v1/settings/worktree-mappings",
      "POST /api/v1/settings/worktree-mappings",
      "POST /api/v1/settings/worktree-mappings",
      "POST /api/v1/settings/worktree-mappings",
      "POST /api/v1/settings/worktree-mappings/apply",
      "GET /api/v1/sessions",
      "GET /api/v1/sessions",
      `PATCH /api/v1/sessions/${CLAUDE_REF}/rename`,
    ]);
    expect(server.lists.map((query) => [query.get("include_one_shot"), query.get("include_automated"), query.get("order_by")]))
      .toEqual([["true", "true", null], ["true", "true", "id:asc"]]);
    expect(server.mappings.map(({ path_prefix, project, enabled }) => ({ path_prefix, project, enabled }))).toEqual([
      { path_prefix: "/workspace", project: "Culler", enabled: true },
      { path_prefix: "/workspaces", project: "Culler", enabled: true },
      { path_prefix: "/mc", project: "Culler", enabled: true },
    ]);
    for (const request of server.requests.filter((item) => item.method !== "GET")) {
      expect(request.host).toBe(server.bind);
      expect(request.origin).toBe(`http://${server.bind}`);
    }
    expect(server.requests.map((request) => request.authorization)).toEqual(server.requests.map(() => `Bearer ${TOKEN}`));
    expect(server.sessions.get(CLAUDE_REF)!.display_name).toBe(CLAUDE_NAME);
    expect(server.sessions.get("agent-a1")!.display_name).toBe("child description");

    server.sessions.set(`codex:${CODEX_REF}`, { display_name: CODEX_NAME, ended_at: CODEX_TIME });
    server.requests = [];
    await r.at(RECONCILE_INTERVAL_MS);
    await r.lane.reconcilePass();
    expect(server.requests.map((request) => `${request.method} ${request.path}`)).toEqual([
      "GET /api/ping",
      "GET /api/v1/sessions",
    ]);
    expect(r.events("agentsview.request_failed")).toEqual([]);
  });

  test("a new AgentsView process gets the rules and each display name again", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    await copied(r);
    server.sessions.set(CLAUDE_REF, { display_name: null });
    server.sessions.set(`codex:${CODEX_REF}`, { display_name: null, ended_at: CODEX_TIME });
    await r.lane.reconcilePass();
    server.restartWithoutArchive();
    server.requests = [];
    await r.at(RECONCILE_INTERVAL_MS);
    await r.lane.reconcilePass();
    expect(server.requestsOf("POST", "/api/v1/settings/worktree-mappings")).toHaveLength(4);
    expect(server.requestsOf("PATCH", "/api/v1/sessions/")).toHaveLength(2);
    expect(server.sessions.get(CLAUDE_REF)!.display_name).toBe(CLAUDE_NAME);
    expect(server.sessions.get(`codex:${CODEX_REF}`)!.display_name).toBe(CODEX_NAME);
    expect(r.events("agentsview.available").map((record) => record.pid)).toEqual([4242, 4243]);
  });

  test("a changed rule goes back to the Culler project", async () => {
    const server = agentsView();
    server.mappings = [{ id: 7, path_prefix: "/mc", project: "mc", enabled: false }];
    const r = rig({ agentsView: server.client() });
    await r.lane.reconcilePass();
    expect(server.requestsOf("PUT", "/api/v1/settings/worktree-mappings/7")).toHaveLength(1);
    expect(server.mappings.find((mapping) => mapping.id === 7)).toEqual(
      { id: 7, path_prefix: "/mc", project: "Culler", enabled: true });
  });

  test("the complete session list comes again every 10 minutes and restores a changed display name", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client(), recentPageSize: 1 });
    await copied(r);
    server.sessions.set(CLAUDE_REF, { display_name: null, ended_at: "2026-09-29T09:00:00.000Z" });
    server.sessions.set(`codex:${CODEX_REF}`, { display_name: null, ended_at: CODEX_TIME });
    await r.lane.reconcilePass();
    server.sessions.get(CLAUDE_REF)!.display_name = "renamed in AgentsView";
    server.lists = [];
    server.requests = [];
    await r.at(RECHECK_INTERVAL_MS - 1);
    await r.lane.reconcilePass();
    expect(server.lists.map((query) => query.get("order_by"))).toEqual([null]);
    expect(server.requestsOf("PATCH", "/api/v1/sessions/")).toHaveLength(0);
    await r.at(1);
    await r.lane.reconcilePass();
    expect(server.lists.map((query) => query.get("order_by"))).toEqual([null, null, "id:asc"]);
    expect(server.sessions.get(CLAUDE_REF)!.display_name).toBe(CLAUDE_NAME);
  });

  test("one pass sends a bounded number of requests, and every session gets its display name, the oldest included", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client(), requestsPerPass: 2, recentPageSize: 1, sweepPageSize: 10, sweepPagesPerPass: 1 });
    const count = 100;
    const refs = Array.from({ length: count }, (_, index) => `${String(index).padStart(8, "0")}-0000-4000-8000-000000000000`);
    r.rows = refs.map((ref, index) => claudeRow(`r-${String(index).padStart(3, "0")}`, {
      native_session_ref: ref, agentsview_session: ref, display_name: `Culler · worker · task ${index}`,
    }));
    for (const [index, ref] of refs.entries()) {
      await r.write(`r-${String(index).padStart(3, "0")}`, "native.jsonl", claudeLine(1, ref));
      server.sessions.set(ref, { display_name: null, ended_at: new Date(Date.UTC(2026, 8, 29, 0, index)).toISOString() });
    }
    await r.lane.copyPass();
    const named = () => refs.filter((ref, index) => server.sessions.get(ref)!.display_name === `Culler · worker · task ${index}`).length;

    async function passes(limit: number): Promise<number> {
      let used = 0;
      while (named() < count && used < limit) {
        server.requests = [];
        await r.lane.reconcilePass();
        expect(sessionRequests(server).length).toBeLessThanOrEqual(2);
        await r.at(RECONCILE_INTERVAL_MS);
        used += 1;
      }
      return used;
    }
    // One request for a name in each pass: more passes than one 10-minute cycle of 40 passes.
    expect(await passes(400)).toBeLessThan(400);
    expect(server.sessions.get(refs[0]!)!.display_name).toBe("Culler · worker · task 0");

    server.restartWithoutArchive();
    expect(named()).toBe(0);
    expect(await passes(400)).toBeLessThan(400);
    expect(named()).toBe(count);
  });

  test("a refused cursor starts the complete session list again", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client(), recentPageSize: 1, sweepPageSize: 1, sweepPagesPerPass: 1 });
    await copied(r);
    server.sessions.set(CLAUDE_REF, { display_name: null });
    server.sessions.set(`codex:${CODEX_REF}`, { display_name: null, ended_at: CODEX_TIME });
    await r.lane.reconcilePass();
    server.refuseCursors = true;
    server.lists = [];
    await r.at(RECONCILE_INTERVAL_MS);
    await r.lane.reconcilePass();
    server.refuseCursors = false;
    await r.at(RECONCILE_INTERVAL_MS);
    await r.lane.reconcilePass();
    expect(server.lists.map((query) => [query.get("order_by"), query.get("cursor")]))
      .toEqual([[null, null], ["id:asc", "1@4242"], [null, null], ["id:asc", null]]);
    expect(r.events("agentsview.request_failed")).toEqual([]);
  });

  test("the health file lists each main copy that AgentsView did not import within 5 minutes", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    await copied(r);
    const changedAt = new Date(r.scheduler.now).toISOString();
    server.sessions.set(CLAUDE_REF, { display_name: null });
    await r.lane.reconcilePass();
    expect(await r.health()).toEqual({ observed_at: changedAt, overdue: [] });
    await r.at(OVERDUE_MS - 1);
    await r.lane.reconcilePass();
    expect((await r.health()).overdue).toEqual([]);
    await r.at(1);
    await r.lane.reconcilePass();
    expect(await r.health()).toEqual({
      observed_at: new Date(r.scheduler.now).toISOString(),
      overdue: [{ kind: "collie", id: "h-codex", agentsview_session: `codex:${CODEX_REF}`, changed_at: changedAt }],
    });
    expect((await stat(r.paths.health)).mode & 0o777).toBe(0o600);

    server.sessions.set(`codex:${CODEX_REF}`, { display_name: null, ended_at: CODEX_TIME });
    await r.at(RECONCILE_INTERVAL_MS);
    await r.lane.reconcilePass();
    expect((await r.health()).overdue).toEqual([]);
  });

  test("a copy that grows after AgentsView stopped its import is overdue 5 minutes after the change", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    r.rows = [claudeRow("r-claude")];
    const source = await r.write("r-claude", "native.jsonl", timedLine(1, "2026-09-29T10:00:00.000Z"));
    await r.lane.copyPass();
    server.sessions.set(CLAUDE_REF, { display_name: CLAUDE_NAME, ended_at: "2026-09-29T10:00:00Z" });
    await r.lane.reconcilePass();
    expect((await r.health()).overdue).toEqual([]);

    await appendFile(source, timedLine(2, "2026-09-29T10:04:00.250Z"));
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    const changedAt = new Date(r.scheduler.now).toISOString();
    for (let waited = 0; waited + RECONCILE_INTERVAL_MS < OVERDUE_MS; waited += RECONCILE_INTERVAL_MS) {
      await r.at(RECONCILE_INTERVAL_MS);
      await r.lane.reconcilePass();
      expect((await r.health()).overdue).toEqual([]);
    }
    await r.at(RECONCILE_INTERVAL_MS);
    await r.lane.reconcilePass();
    expect((await r.health()).overdue).toEqual([
      { kind: "run", id: "r-claude", agentsview_session: CLAUDE_REF, changed_at: changedAt },
    ]);

    server.sessions.get(CLAUDE_REF)!.ended_at = "2026-09-29T10:04:00.250123Z";
    await r.at(RECONCILE_INTERVAL_MS);
    await r.lane.reconcilePass();
    expect((await r.health()).overdue).toEqual([]);
  });

  test("an active copy is not overdue while AgentsView imports each change", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    r.rows = [claudeRow("r-claude")];
    const at = (minute: number) => new Date(Date.UTC(2026, 8, 29, 10, minute)).toISOString();
    const source = await r.write("r-claude", "native.jsonl", timedLine(0, at(0)));
    await r.lane.copyPass();
    server.sessions.set(CLAUDE_REF, { display_name: CLAUDE_NAME, ended_at: at(0) });
    for (let minute = 1; minute <= 12; minute += 1) {
      await appendFile(source, timedLine(minute, at(minute)));
      await r.at(COPY_INTERVAL_MS);
      await r.lane.copyPass();
      server.sessions.get(CLAUDE_REF)!.ended_at = at(minute - 1);
      await r.at(60_000 - COPY_INTERVAL_MS);
      await r.lane.reconcilePass();
      expect((await r.health()).overdue).toEqual([]);
      server.sessions.get(CLAUDE_REF)!.ended_at = at(minute);
    }
  });

  test("a session that the operator removes in AgentsView is not overdue", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    await copied(r);
    server.sessions.set(CLAUDE_REF, { display_name: null });
    server.sessions.set(`codex:${CODEX_REF}`, { display_name: null, ended_at: CODEX_TIME });
    await r.lane.reconcilePass();
    server.sessions.delete(CLAUDE_REF);
    await appendFile(join(r.session("r-claude"), "native.jsonl"), claudeLine(3));
    await r.at(COPY_INTERVAL_MS);
    await r.lane.copyPass();
    await r.at(OVERDUE_MS);
    await r.lane.reconcilePass();
    expect(server.requestsOf("GET", `/api/v1/sessions/${CLAUDE_REF}`)).toHaveLength(1);
    expect((await r.health()).overdue).toEqual([]);
  });

  test("a new AgentsView process gets 5 minutes to import again", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    await copied(r);
    server.sessions.set(`codex:${CODEX_REF}`, { display_name: null, ended_at: CODEX_TIME });
    server.sessions.set(CLAUDE_REF, { display_name: null });
    await r.lane.reconcilePass();
    await r.at(OVERDUE_MS * 2);
    server.restartWithoutArchive();
    server.sessions.clear();
    await r.lane.reconcilePass();
    expect((await r.health()).overdue).toEqual([]);
    await r.at(OVERDUE_MS);
    await r.lane.reconcilePass();
    expect((await r.health()).overdue).toHaveLength(2);
  });

  test("the health file holds the time of its write", async () => {
    const server = agentsView();
    const client = server.client();
    const r = rig();
    const lane = r.build({
      agentsView: {
        ...client,
        ensureProjectRules: async (signal) => {
          r.scheduler.now += 100_000;
          await client.ensureProjectRules(signal);
        },
      },
    });
    lanes.push(lane);
    const before = r.scheduler.now;
    await lane.reconcilePass();
    expect((await r.health()).observed_at).toBe(new Date(before + 100_000).toISOString());
  });

  test("an unavailable AgentsView logs once per change and lists no overdue copy", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    await copied(r);
    server.down = true;
    for (let pass = 0; pass < 3; pass += 1) {
      await r.lane.reconcilePass();
      await r.at(OVERDUE_MS);
    }
    const down = r.events("agentsview.unavailable");
    expect(down).toHaveLength(1);
    expect(down[0]!.msg).toBe(`AgentsView at ${server.bind} is unavailable, so trace links do not open: Unable to connect`);
    expect((await r.health()).overdue).toEqual([]);
    server.down = false;
    await r.lane.reconcilePass();
    await r.lane.reconcilePass();
    expect(r.events("agentsview.available")).toHaveLength(1);
    server.down = true;
    await r.lane.reconcilePass();
    expect(r.events("agentsview.unavailable")).toHaveLength(2);
  });

  test("a refused token makes AgentsView unavailable, and a new token file applies at the next pass", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    await copied(r);
    server.token = NEXT_TOKEN;
    await r.lane.reconcilePass();
    expect(server.requests.map((request) => `${request.method} ${request.path}`)).toEqual(["GET /api/ping"]);
    expect(r.events("agentsview.unavailable").map((record) => record.msg)).toEqual([
      `AgentsView at ${server.bind} is unavailable, so trace links do not open: ` +
        "AgentsView refuses the AgentsView token of this deployment",
    ]);
    expect(JSON.stringify(r.records)).not.toContain(NEXT_TOKEN);
    expect((await r.health()).overdue).toEqual([]);

    await writeToken(NEXT_TOKEN);
    server.requests = [];
    await r.at(RECONCILE_INTERVAL_MS);
    await r.lane.reconcilePass();
    expect(r.events("agentsview.available")).toHaveLength(1);
    expect(server.requestsOf("POST", "/api/v1/settings/worktree-mappings/apply")).toHaveLength(1);
    expect(new Set(server.requests.map((request) => request.authorization))).toEqual(new Set([`Bearer ${NEXT_TOKEN}`]));
  });

  test("a lane without a readable token file sends no request", async () => {
    const server = agentsView();
    const r = rig({ agentsView: server.client() });
    await copied(r);
    await rm(agentsViewTokenPath(home));
    await r.lane.reconcilePass();
    await writeToken("not a token");
    await r.lane.reconcilePass();
    expect(server.requests).toEqual([]);
    expect(r.events("agentsview.unavailable").map((record) => record.msg)).toEqual([
      `AgentsView at ${server.bind} is unavailable, so trace links do not open: ` +
        `the AgentsView token file ${agentsViewTokenPath(home)} cannot be read: ` +
        `ENOENT: no such file or directory, open '${agentsViewTokenPath(home)}'`,
    ]);
    await expect(readAgentsViewToken(agentsViewTokenPath(home)))
      .rejects.toThrow("does not hold 64 lowercase hexadecimal characters");
  });

  test("a lane without an AgentsView address sends no request", async () => {
    const r = rig();
    await copied(r);
    await r.lane.reconcilePass();
    expect(await Bun.file(r.paths.health).exists()).toBe(false);
  });
});

describe("copyMark", () => {
  test("gives the time of the last complete line with a time, also behind a long line", async () => {
    const path = join(home, "copy.jsonl");
    const long = JSON.stringify({ type: "user", message: { content: "x".repeat(20_000) } }) + "\n";
    const text = timedLine(1, "2026-09-29T10:00:00.123456Z") + long + '{"summary":"no time"}\n';
    await writeFile(path, text);
    expect(await copyMark(path, text.length)).toBe(Date.parse("2026-09-29T10:00:00.123Z"));
    expect(await copyMark(path, timedLine(1, "2026-09-29T10:00:00.123456Z").length)).toBe(Date.parse("2026-09-29T10:00:00.123Z"));
    await writeFile(path, long);
    expect(await copyMark(path, long.length)).toBeNull();
    expect(await copyMark(join(home, "absent.jsonl"), 10)).toBeNull();
  });
});

describe("parseAgentsViewBind", () => {
  test("accepts only a loopback host:port", () => {
    for (const bind of ["127.0.0.1:7334", "localhost:1", "[::1]:65535"]) expect(parseAgentsViewBind(bind)).toBe(bind);
    for (const bind of ["0.0.0.0:7334", "10.0.0.1:7334", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:65536", "::1:7334",
      "127.0.0.2:7334", 7334, undefined]) {
      expect(() => parseAgentsViewBind(bind)).toThrow("agentsViewBind must be a loopback host:port");
    }
  });
});


test("inspection exposes masked copies and availability without original access", async () => {
  const secret = "exact-private-credential";
  const r = rig({ credentials: () => [secret] });
  r.rows = [claudeRow("r-copy"), codexRow("h-copy")];
  const token = "culler_proxy_" + "c".repeat(43);
  const original = JSON.stringify({ type: "assistant", signature: "sk-" + "a".repeat(80), text: `${secret} ${token}` }) + "\n";
  const native = await r.write("r-copy", "native.jsonl", original);
  const child = await r.write("r-copy", "subagents/agent-a.jsonl", original);
  await r.write("h-copy", CODEX_FILE, "{}");
  await r.lane.copyPass();
  const inspection = join(r.paths.inspection, "r-copy/native.jsonl");
  const copy = join(r.paths.claude, `${CLAUDE_REF}.jsonl`);
  expect((await stat(inspection)).ino).toBe((await stat(copy)).ino);
  expect((await stat(inspection)).ino).not.toBe((await stat(native)).ino);
  for (const path of [inspection, join(r.paths.inspection, "r-copy/subagents/agent-a.jsonl")]) {
    expect(await read(path)).not.toContain(secret);
    expect(await read(path)).not.toContain(token);
    expect(await read(path)).toContain("sk-" + "a".repeat(80));
  }
  expect(JSON.parse(await read(join(r.paths.inspection, "h-copy/status.json"))).files[CODEX_FILE]).toBe("pending");
  expect(await Bun.file(join(r.paths.inspection, "h-copy", CODEX_FILE)).exists()).toBe(false);
  await rm(copy);
  await r.lane.copyPass();
  expect(await read(inspection)).toBe(await read(copy));
  await rm(native);
  await r.lane.copyPass();
  expect(JSON.parse(await read(join(r.paths.inspection, "r-copy/status.json"))).files["native.jsonl"]).toBe("unavailable");
  expect(await Bun.file(inspection).exists()).toBe(false);
  expect(await read(child)).toBe(original);
});
