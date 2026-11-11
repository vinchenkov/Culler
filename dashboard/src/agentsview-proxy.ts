import { randomBytes } from "node:crypto";
import { openCodeTraceID } from "../../runner/shared/opencode-trace";
import type { McRunner } from "./mc";
import { accessPage, ACCESS_MESSAGES, AGENTSVIEW_BRIDGE, type AccessFailure } from "./agentsview-pages";

const GRANT_MS = 30_000;
const SESSION_MS = 12 * 60 * 60 * 1000;
const TIMEOUT_MS = 10_000;
const MAX_ACCESS = 256;
const secret = () => randomBytes(32).toString("hex");
const noStore = { "cache-control": "no-store", "referrer-policy": "no-referrer" };

export function agentsViewDestination(value: unknown): string | null {
  if (typeof value !== "string" || value.length > 2048) return null;
  const [path, query = ""] = value.split("?");
  if (!/^\/sessions(?:\/(?:(?:codex|opencode)\/)?[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?$/.test(path!)) return null;
  const params = new URLSearchParams(query);
  for (const [key, value] of params) {
    if (params.getAll(key).length !== 1) return null;
    if (path === "/sessions") {
      if (!["include_one_shot", "include_automated"].includes(key) || value !== "true") return null;
    } else if (key !== "msg" || !/^(last|[0-9]+)$/.test(value)) return null;
  }
  return value.includes("#") || value.split("?").length > 2 ? null : value;
}

class AccessError extends Error {
  constructor(readonly code: AccessFailure) { super(ACCESS_MESSAGES[code]); }
}

export function accessError(error: unknown): Response {
  const code = error instanceof AccessError ? error.code : "failed";
  return Response.json({ error: { message: ACCESS_MESSAGES[code] } }, { status: 503, headers: noStore });
}

async function deadline<T>(promise: Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout>;
  try {
    return await Promise.race([promise, new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new AccessError("unavailable")), TIMEOUT_MS);
    })]);
  } finally { clearTimeout(timer!); }
}

export async function accessBody(request: Request): Promise<string> {
  const reader = request.body?.getReader();
  if (!reader) throw new AccessError("invalid");
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    await deadline((async () => {
      while (true) {
        const { value, done } = await reader.read();
        if (done) return;
        size += value.byteLength;
        if (size > 4096) throw new AccessError("invalid");
        chunks.push(value);
      }
    })());
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    return new TextDecoder().decode(bytes);
  } finally { void reader.cancel().catch(() => {}); }
}

interface Credentials { url: string; token: string }
interface Grant { path: string; origin: string; until: number }

export function createAgentsViewProxy(mc: McRunner, now = Date.now) {
  let server: ReturnType<typeof Bun.serve> | null = null;
  let dashboardOrigin = "";
  let stopped = false;
  const grants = new Map<string, Grant>();
  const sessions = new Map<string, { origin: string; until: number }>();
  const controllers = new WeakMap<Response, AbortController>();
  const cookieName = `culler_agentsview_${secret().slice(0, 16)}`;
  let credentialsInFlight: Promise<Credentials> | null = null;

  function credentials(): Promise<Credentials> {
    if (credentialsInFlight) return credentialsInFlight;
    credentialsInFlight = deadline(mc.run(["__agentsview-access"])).then(result => {
      const token = result.json?.token;
      if (result.code !== 0 || typeof token !== "string" || !/^[0-9a-f]{64}$/.test(token)) throw new AccessError("credential");
      let url: URL;
      try { url = new URL(String(result.json?.url)); } catch { throw new AccessError("unavailable"); }
      if (url.protocol !== "http:" || !["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)
        || url.username || url.password || url.pathname !== "/" || url.search || url.hash) throw new AccessError("unavailable");
      if (server && url.port === String(server.port)) throw new AccessError("unavailable");
      return { url: url.origin, token };
    }).finally(() => { credentialsInFlight = null; });
    return credentialsInFlight;
  }

  function prune() {
    for (const map of [grants, sessions]) {
      for (const [id, record] of map) if (record.until <= now()) map.delete(id);
    }
  }

  async function upstream(path: string, init: RequestInit = {}) {
    const config = await credentials();
    const headers = new Headers(init.headers);
    headers.set("authorization", `Bearer ${config.token}`);
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
    const signal = init.signal ? AbortSignal.any([init.signal, controller.signal]) : controller.signal;
    let response: Response;
    try { response = await fetch(config.url + path, { ...init, headers, signal, redirect: "manual" }); }
    catch { throw new AccessError("unavailable"); }
    finally { clearTimeout(timer); }
    if (response.status === 401 || response.status === 403) {
      await response.body?.cancel();
      throw new AccessError("refused");
    }
    if (response.status >= 300 && response.status < 400 || response.status >= 500) {
      await response.body?.cancel();
      throw new AccessError("unavailable");
    }
    controllers.set(response, controller);
    return response;
  }

  async function readBody(response: Response, format: "json" | "text" | "arrayBuffer") {
    try { return await deadline(response[format]()); }
    finally { controllers.get(response)?.abort(); controllers.delete(response); }
  }

  async function check(path: string) {
    const ping = await upstream("/api/ping");
    const health = await readBody(ping, "json").catch(() => { throw new AccessError("unavailable"); });
    if (!ping.ok || health?.service !== "agentsview") throw new AccessError("unavailable");
    if (path.split("?")[0] !== "/sessions") {
      const id = path.split("?")[0]!.slice("/sessions/".length).replace(/^(codex|opencode)\//, "$1:");
      const trace = await upstream(`/api/v1/sessions/${encodeURIComponent(id)}`);
      if (trace.status === 404 && id.startsWith("opencode:")) {
        await trace.body?.cancel();
        const legacy = openCodeTraceID(id.slice("opencode:".length));
        const exported = await upstream(`/api/v1/sessions/codex:${legacy}`);
        if (!exported.ok) { await exported.body?.cancel(); throw new AccessError("trace"); }
        await readBody(exported, "json").catch(() => { throw new AccessError("trace"); });
        return `/sessions/codex/${legacy}?${path.split("?")[1] ?? "msg=last"}`;
      }
      if (!trace.ok) { await trace.body?.cancel(); throw new AccessError("trace"); }
      await readBody(trace, "json").catch(() => { throw new AccessError("trace"); });
    }
    return path;
  }

  function broker(origin: string, path: string) {
    return `${origin}/agentsview/open?path=${encodeURIComponent(agentsViewDestination(path) ?? "/sessions")}`;
  }

  function start() {
    if (server) return;
    server = Bun.serve({
      hostname: "127.0.0.1", port: 0,
      async fetch(req, active) {
        active.timeout(req, 35);
        const url = new URL(req.url);
        const ownOrigin = `http://127.0.0.1:${active.port}`;
        if (url.origin !== ownOrigin || req.headers.get("host") !== new URL(ownOrigin).host) return new Response("Forbidden", { status: 403 });
        prune();
        const origin = req.headers.get("origin");
        if (url.pathname === "/__culler/access" && req.method === "POST") {
          const body = await accessBody(req).catch(() => "");
          const key = new URLSearchParams(body).get("grant") ?? "";
          const grant = grants.get(key);
          // A form with no referrer sends a null origin. The single-use grant authorizes this request.
          const foreignOrigin = origin !== null && origin !== "null" && origin !== grant?.origin;
          if (!grant || grant.until <= now() || foreignOrigin) return new Response("Access expired. Open the trace from Culler again.", { status: 403, headers: noStore });
          grants.delete(key);
          const session = secret();
          if (sessions.size >= MAX_ACCESS) sessions.delete(sessions.keys().next().value!);
          sessions.set(session, { origin: grant.origin, until: now() + SESSION_MS });
          // A document on this origin ends the cross-origin form request before navigation.
          return new Response(`<!doctype html><meta name="referrer" content="no-referrer"><title>Open AgentsView</title>
<p role="status">Culler opens the trace…</p><script>location.replace(${JSON.stringify(grant.path)});</script>`, {
            headers: { ...noStore, "content-type": "text/html; charset=utf-8",
              "content-security-policy": "default-src 'none'; script-src 'unsafe-inline'; frame-ancestors 'none'",
              "set-cookie": `${cookieName}=${session}; HttpOnly; SameSite=Strict; Path=/; Max-Age=${SESSION_MS / 1000}` },
          });
        }
        if (origin !== null && origin !== ownOrigin || ["cross-site", "same-site"].includes(req.headers.get("sec-fetch-site") ?? "")) return new Response("Forbidden", { status: 403 });
        const cookie = req.headers.get("cookie")?.split(";").map(v => v.trim()).find(v => v.startsWith(cookieName + "="))?.slice(cookieName.length + 1);
        const session = sessions.get(cookie ?? "");
        if (!session) {
          const path = agentsViewDestination(url.pathname === "/__culler/error" ? url.searchParams.get("path") : url.pathname + url.search) ?? "/sessions";
          if (req.headers.get("accept")?.includes("text/html")) {
            return new Response(null, { status: 303, headers: { ...noStore, location: broker(dashboardOrigin, path) } });
          }
          return Response.json({ error: "Open the trace from Culler again." }, {
            status: 503, headers: { ...noStore, "x-culler-access-error": "dashboard" },
          });
        }
        if (url.pathname === "/__culler/error") {
          const path = agentsViewDestination(url.searchParams.get("path")) ?? "/sessions";
          const code = url.searchParams.get("code") as AccessFailure;
          return accessPage(path, { failure: Object.hasOwn(ACCESS_MESSAGES, code) ? code : "failed", retry: broker(session.origin, path) });
        }
        if (url.pathname === "/__culler/bridge.js") return new Response(AGENTSVIEW_BRIDGE, { headers: { ...noStore, "content-type": "text/javascript" } });
        if (url.pathname.startsWith("/__culler/")) return new Response("Not found", { status: 404 });
        let pathname: string;
        try { pathname = decodeURIComponent(url.pathname); } catch { return new Response("Invalid path", { status: 400 }); }
        if (/[\\%\x00-\x20]/.test(pathname) || pathname.includes("//") || pathname.split("/").some(part => part === "." || part === "..")) return new Response("Invalid path", { status: 400 });
        // Settings can disclose or replace the service credential.
        if (/^\/api\/v1\/(?:config|raw-sync|remote-sync|sync\/remotes|push)(?:\/|$)/.test(pathname) || pathname.startsWith("/_agentsview/")) {
          return Response.json({ error: "Culler manages the AgentsView service configuration." }, { status: 403, headers: noStore });
        }
        if (pathname === "/api/v1/settings" && req.method !== "GET") return Response.json({ error: "Culler manages the AgentsView service configuration." }, { status: 403 });
        const path = pathname + url.search;
        try {
          const headers = new Headers();
          for (const name of ["accept", "content-type", "range"]) {
            const value = req.headers.get(name);
            if (value) headers.set(name, value);
          }
          const response = await upstream(path, { method: req.method, headers, signal: req.signal,
            ...(req.method === "GET" || req.method === "HEAD" ? {} : { body: await req.arrayBuffer() }) });
          const type = response.headers.get("content-type") ?? "application/octet-stream";
          const outHeaders = new Headers({ ...noStore, "content-type": type, "x-content-type-options": "nosniff",
            "content-security-policy": "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'" });
          if (pathname === "/api/v1/settings") {
            const body = await readBody(response, "json");
            delete body.auth_token;
            body.require_auth = true;
            return Response.json(body, { status: response.status, headers: outHeaders });
          }
          if (type.includes("text/html")) {
            const html = await readBody(response, "text");
            return new Response(html.replace("<head>", '<head><script src="/__culler/bridge.js"></script>')
              .replace(/<link\b[^>]*href="https:\/\/fonts\.[^>]*>/g, ""), { status: response.status, headers: outHeaders });
          }
          if (type.includes("text/event-stream")) {
            active.timeout(req, 0);
            return new Response(response.body, { status: response.status, headers: outHeaders });
          }
          for (const name of ["content-range", "accept-ranges", "content-disposition"]) {
            const value = response.headers.get(name);
            if (value) outHeaders.set(name, value);
          }
          const body = await readBody(response, "arrayBuffer");
          return new Response(req.method === "HEAD" || [204, 205].includes(response.status) ? null : body,
            { status: response.status, headers: outHeaders });
        } catch (error) {
          const code = error instanceof AccessError ? error.code : "failed";
          if (req.headers.get("accept")?.includes("text/html")) return accessPage(path, { failure: code, retry: broker(session.origin, path) });
          return Response.json({ error: ACCESS_MESSAGES[code] }, { status: 503, headers: { ...noStore, "x-culler-access-error": code } });
        }
      },
    });
  }

  return {
    async grant(path: unknown, origin: string) {
      if (stopped) throw new AccessError("unavailable");
      const destination = agentsViewDestination(path);
      if (!destination) throw new AccessError("invalid");
      const resolved = await deadline(check(destination));
      if (stopped) throw new AccessError("unavailable");
      dashboardOrigin = origin;
      start();
      prune();
      if (grants.size >= MAX_ACCESS) grants.delete(grants.keys().next().value!);
      const grant = secret();
      grants.set(grant, { path: resolved, origin, until: now() + GRANT_MS });
      return { url: `http://127.0.0.1:${server!.port}/__culler/access`, grant };
    },
    stop() { stopped = true; server?.stop(true); server = null; grants.clear(); sessions.clear(); },
  };
}
