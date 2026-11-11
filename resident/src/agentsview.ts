// agentsview.ts — the requests of the resident to the local AgentsView
// server: the ping, the project rules, the session list, and the display
// names. AgentsView requires the AgentsView token of the deployment in each
// request.

import { constants, open } from "node:fs/promises";
import { join } from "node:path";

export const REQUEST_TIMEOUT_MS = 3_000;
/** The AgentsView project that holds every Culler trace. */
export const AGENTSVIEW_PROJECT = "Culler";
/** The current folders of the harnesses in each Culler container. */
export const PROJECT_RULE_PREFIXES = ["/workspace", "/workspaces", "/mc"] as const;

export type Fetch = (url: string, init: RequestInit) => Promise<Response>;

/** An error for an AgentsView answer with an unexpected status. A network
 * failure is a plain error. */
export class AgentsViewAnswer extends Error {
  constructor(message: string, readonly status = 0) {
    super(message);
  }
}

const BIND = /^(127\.0\.0\.1|localhost|\[::1\]):([0-9]{1,5})$/;

/** Returns the bind when it is a loopback `host:port`. An IPv6 host uses
 * brackets, as in `[::1]:7334`. */
export function parseAgentsViewBind(value: unknown): string {
  const match = typeof value === "string" ? BIND.exec(value) : null;
  const port = match === null ? 0 : Number(match[2]);
  if (match === null || port < 1 || port > 65535) {
    throw new Error("agentsViewBind must be a loopback host:port (127.0.0.1, [::1], or localhost)");
  }
  return value as string;
}

/** The file that holds the AgentsView token of the deployment. */
export function agentsViewTokenPath(mcHome: string): string {
  return join(mcHome, "agentsview", "token");
}

const TOKEN = /^[0-9a-f]{64}\n?$/;

/** Reads the AgentsView token: 64 lowercase hexadecimal characters. */
export async function readAgentsViewToken(path: string): Promise<string> {
  let text: string;
  try {
    // O_NONBLOCK keeps a FIFO at the token path from blocking the open.
    const handle = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    try {
      if (!(await handle.stat()).isFile()) throw new Error("it is not a regular file");
      const buffer = Buffer.alloc(128);
      const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
      text = buffer.subarray(0, bytesRead).toString("utf8");
    } finally {
      await handle.close();
    }
  } catch (error) {
    throw new Error(`the AgentsView token file ${path} cannot be read: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (!TOKEN.test(text)) {
    throw new Error(`the AgentsView token file ${path} does not hold 64 lowercase hexadecimal characters`);
  }
  return text.trimEnd();
}

export interface AgentsViewClientOptions {
  /** Reads the AgentsView token. Each ping reads it again, so a changed
   * token applies at the next pass. */
  token(): Promise<string>;
  fetcher?: Fetch;
  timeoutMs?: number;
}

/** The facts of one AgentsView session that the resident compares with the
 * trace copy. `endedAt` is the time of the last line that AgentsView
 * imported. */
export interface AgentsViewSession {
  id: string;
  displayName: string | null;
  endedAt: string | null;
}

export interface AgentsViewPage {
  sessions: AgentsViewSession[];
  /** The cursor of the next page, or null after the last page. */
  nextCursor: string | null;
}

export interface SessionListQuery {
  limit: number;
  /** "id" lists the sessions in the order of their IDs. Without it,
   * AgentsView lists the most recent sessions first. */
  order?: "id";
  cursor?: string;
}

export interface AgentsViewClient {
  readonly bind: string;
  /** The process ID of AgentsView. The ping reads the token again. */
  ping(signal: AbortSignal): Promise<number>;
  /** Makes the project rules put each Culler trace into the Culler project. */
  ensureProjectRules(signal: AbortSignal): Promise<void>;
  /** One page of the AgentsView sessions, one-shot and automated sessions
   * included. */
  listSessions(query: SessionListQuery, signal: AbortSignal): Promise<AgentsViewPage>;
  /** One imported session, or null when AgentsView did not import it. */
  session(id: string, signal: AbortSignal): Promise<AgentsViewSession | null>;
  rename(id: string, displayName: string, signal: AbortSignal): Promise<void>;
  syncNative(id: string, signal: AbortSignal): Promise<void>;
  retireExport(legacy: string, native: string, signal: AbortSignal): Promise<void>;
}

function sessionPath(id: string): string {
  return `/api/v1/sessions/${encodeURIComponent(id).replaceAll("%3A", ":")}`;
}

function sessionFacts(row: unknown): AgentsViewSession | null {
  if (row === null || typeof row !== "object") return null;
  const { id, display_name: displayName, ended_at: endedAt } = row as Record<string, unknown>;
  if (typeof id !== "string" || id === "") return null;
  return {
    id,
    displayName: typeof displayName === "string" ? displayName : null,
    endedAt: typeof endedAt === "string" ? endedAt : null,
  };
}

export function agentsViewClient(bind: string, options: AgentsViewClientOptions): AgentsViewClient {
  const origin = `http://${bind}`;
  const fetcher = options.fetcher ?? fetch;
  const timeoutMs = options.timeoutMs ?? REQUEST_TIMEOUT_MS;
  let token: string | undefined;

  async function request(
    method: string, path: string, signal: AbortSignal, body?: unknown,
  ): Promise<Response> {
    token ??= await options.token();
    const headers: Record<string, string> = { accept: "application/json", authorization: `Bearer ${token}` };
    if (method !== "GET") {
      headers.host = bind;
      headers.origin = origin;
      headers["content-type"] = "application/json";
    }
    return fetcher(`${origin}${path}`, {
      method,
      headers,
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.any([signal, AbortSignal.timeout(timeoutMs)]),
    });
  }

  /** A request whose answer must have a success status. `label` names the
   * request in an error when the path holds a query. */
  async function success(
    method: string, path: string, signal: AbortSignal, body?: unknown, label = `${method} ${path}`,
  ): Promise<Response> {
    const response = await request(method, path, signal, body);
    if (!response.ok) {
      await response.body?.cancel();
      throw new AgentsViewAnswer(`${label} answered ${response.status}`, response.status);
    }
    return response;
  }

  return {
    bind,
    async ping(signal) {
      token = await options.token();
      const response = await request("GET", "/api/ping", signal);
      if (response.status === 401) {
        await response.body?.cancel();
        throw new Error("AgentsView refuses the AgentsView token of this deployment");
      }
      if (!response.ok) {
        await response.body?.cancel();
        throw new Error(`the ping answered ${response.status}`);
      }
      const body = await response.json().catch(() => null) as { service?: unknown; pid?: unknown } | null;
      if (body?.service !== "agentsview" || typeof body.pid !== "number") {
        throw new Error("the address answers, but not as AgentsView");
      }
      return body.pid;
    },
    async ensureProjectRules(signal) {
      const listed = await success("GET", "/api/v1/settings/worktree-mappings", signal);
      const body = await listed.json().catch(() => null) as { mappings?: unknown } | null;
      const mappings = Array.isArray(body?.mappings)
        ? (body.mappings as unknown[]).filter((item): item is Record<string, unknown> => item !== null && typeof item === "object")
        : [];
      for (const prefix of PROJECT_RULE_PREFIXES) {
        const rule = { path_prefix: prefix, project: AGENTSVIEW_PROJECT, enabled: true };
        const current = mappings.find((mapping) => mapping.path_prefix === prefix);
        if (current === undefined) {
          const created = await request("POST", "/api/v1/settings/worktree-mappings", signal, rule);
          await created.body?.cancel();
          if (!created.ok && created.status !== 409) {
            throw new AgentsViewAnswer(`POST /api/v1/settings/worktree-mappings answered ${created.status}`);
          }
        } else if (current.project !== AGENTSVIEW_PROJECT || current.enabled !== true) {
          await (await success("PUT", `/api/v1/settings/worktree-mappings/${Number(current.id)}`, signal, rule)).body?.cancel();
        }
      }
      await (await success("POST", "/api/v1/settings/worktree-mappings/apply", signal, {})).body?.cancel();
    },
    async listSessions(query, signal) {
      const params = new URLSearchParams({ include_one_shot: "true", include_automated: "true", limit: String(query.limit) });
      if (query.order === "id") params.set("order_by", "id:asc");
      if (query.cursor !== undefined) params.set("cursor", query.cursor);
      const label = "GET /api/v1/sessions";
      const response = await success("GET", `/api/v1/sessions?${params}`, signal, undefined, label);
      const body = await response.json().catch(() => {
        throw new AgentsViewAnswer(`${label} answered without JSON`);
      }) as { sessions?: unknown; next_cursor?: unknown } | null;
      if (!Array.isArray(body?.sessions)) throw new AgentsViewAnswer(`${label} answered without a session list`);
      return {
        sessions: body.sessions.map(sessionFacts).filter((row): row is AgentsViewSession => row !== null),
        nextCursor: typeof body.next_cursor === "string" && body.next_cursor !== "" ? body.next_cursor : null,
      };
    },
    async session(id, signal) {
      const response = await request("GET", sessionPath(id), signal);
      if (response.status === 404) {
        await response.body?.cancel();
        return null;
      }
      if (!response.ok) {
        await response.body?.cancel();
        throw new AgentsViewAnswer(`GET ${sessionPath(id)} answered ${response.status}`, response.status);
      }
      const body = await response.json().catch(() => {
        throw new AgentsViewAnswer(`GET ${sessionPath(id)} answered without JSON`);
      });
      const facts = sessionFacts(body);
      if (facts === null) throw new AgentsViewAnswer(`GET ${sessionPath(id)} answered without a session`);
      return facts;
    },
    async rename(id, displayName, signal) {
      await (await success("PATCH", `${sessionPath(id)}/rename`, signal, { display_name: displayName })).body?.cancel();
    },
    async syncNative(id, signal) {
      // The explicit session sync forces a parse, including edits with unchanged native timestamps.
      const response = await success("POST", "/api/v1/sessions/sync", signal, { id, subagents: true });
      const row = await response.json() as { id?: unknown; agent?: unknown };
      if (row.id !== id || row.agent !== "opencode") throw new AgentsViewAnswer("AgentsView did not confirm the native OpenCode import.");
    },
    async retireExport(legacy, native, signal) {
      if (!legacy.startsWith("codex:") || !native.startsWith("opencode:")) throw new Error("The trace retirement identities are invalid.");
      const exists = await request("GET", sessionPath(legacy), signal);
      if (exists.status === 404) { await exists.body?.cancel(); return; }
      if (!exists.ok) throw new AgentsViewAnswer("AgentsView could not read the retained export.", exists.status);
      const row = await exists.json() as { display_name?: string; deleted_at?: unknown };
      if (row.deleted_at) return;
      // Soft deletion preserves the original pins, notes, and stars in the AgentsView trash.
      await (await success("PATCH", `${sessionPath(legacy)}/rename`, signal,
        { display_name: `OpenCode export · ${row.display_name ?? legacy}` })).body?.cancel();
      await (await success("DELETE", sessionPath(legacy), signal)).body?.cancel();
    },
  };
}
