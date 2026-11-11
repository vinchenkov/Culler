import { describe, expect, test } from "bun:test";
import { accessBody, agentsViewDestination, createAgentsViewProxy } from "./agentsview-proxy";
import { startDashboardServer } from "./server";
import { deps, ok } from "./test-helpers";
import { openCodeTraceID } from "../../runner/shared/opencode-trace";

const TOKEN = "a".repeat(64);
const AUTH = { authorization: "Bearer dashboard-secret" };

function fixture() {
  let token = TOKEN;
  let supplied = TOKEN;
  let reachable = true;
  let nativeAvailable = false;
  let calls = 0;
  const requests: Request[] = [];
  const upstream = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch(req) {
    requests.push(req);
    if (!reachable) return new Response("Unavailable", { status: 503 });
    if (req.headers.get("authorization") !== `Bearer ${token}`) return new Response("Unauthorized", { status: 401 });
    const path = new URL(req.url).pathname;
    if (path === "/api/ping") return Response.json({ service: "agentsview", pid: 1 });
    if (path === "/api/v1/settings" || path === "/api/v1/%73ettings") return Response.json({ auth_token: token, require_auth: true });
    if (path === "/api/v1/sessions/absent") return new Response("Not found", { status: 404 });
    if (decodeURIComponent(path) === "/api/v1/sessions/opencode:ses_retained" && !nativeAvailable) return new Response("Not found", { status: 404 });
    if (path === "/api/v1/redirect") return new Response(null, { status: 302, headers: { location: "https://example.com" } });
    if (path.startsWith("/api/")) return Response.json({ id: "trace", messages: ["Trace content"] });
    return new Response('<!doctype html><html><head></head><body>Trace content</body></html>', { headers: { "content-type": "text/html" } });
  } });
  const mc = { async run() { calls++; return ok({ token: supplied, url: `http://127.0.0.1:${upstream.port}` }); } };
  const dashboard = startDashboardServer({ bind: "127.0.0.1:0", authToken: "dashboard-secret" }, deps({ mc }));
  const origin = new URL(dashboard.url).origin;
  return { dashboard, origin, mc, requests, get calls() { return calls; },
    set supplied(value: string) { supplied = value; }, set token(value: string) { token = value; }, set reachable(value: boolean) { reachable = value; },
    set nativeAvailable(value: boolean) { nativeAvailable = value; },
    async grant(path = "/sessions/trace?msg=last", headers = AUTH) {
      return fetch(`${origin}/api/agentsview/access`, { method: "POST", headers: { ...headers, "content-type": "application/json" }, body: JSON.stringify({ path }) });
    },
    stop() { dashboard.stop(); upstream.stop(true); },
  };
}

async function consume(grant: { url: string; grant: string }, origin: string) {
  const response = await fetch(grant.url, { method: "POST", headers: { origin, "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ grant: grant.grant }), redirect: "manual" });
  return { response, cookie: response.headers.get("set-cookie")?.split(";")[0] ?? "", url: new URL(grant.url).origin };
}

describe("AgentsView destinations", () => {
  test("access requests limit the body size without a content length", async () => {
    expect(await accessBody(new Request("http://localhost", { method: "POST", body: '{"path":"/sessions"}' }))).toBe('{"path":"/sessions"}');
    await expect(accessBody(new Request("http://localhost", { method: "POST", body: "x".repeat(4097) }))).rejects.toThrow("invalid");
  });
  test("retains the requested session and filters", () => {
    for (const path of ["/sessions", "/sessions?include_one_shot=true&include_automated=true", "/sessions/codex/uuid?msg=last", "/sessions/opencode/ses_native?msg=last", "/sessions/uuid?msg=4", "/sessions/trace.1?msg=last"]) {
      expect(agentsViewDestination(path)).toBe(path);
    }
    for (const path of ["https://example.com", "//example.com/sessions", "/sessions/../api", "/sessions/%2fsettings", "/sessions/a?token=secret",
      "/sessions?a=b", "/sessions/a?msg=last&msg=1", "/sessions/a#token=secret", "/sessions/a?msg=last?x=y", "/sessions/a\\b"]) {
      expect(agentsViewDestination(path)).toBeNull();
    }
  });
});

describe("AgentsView access boundary", () => {
  test("a native link selects retained evidence until the native import becomes available", async () => {
    const f = fixture();
    try {
      const path = "/sessions/opencode/ses_retained?msg=last";
      const fallback = await consume(await (await f.grant(path)).json(), f.origin);
      expect(await fallback.response.text()).toContain(`/sessions/codex/${openCodeTraceID("ses_retained")}?msg=last`);
      f.nativeAvailable = true;
      const grant = await (await f.grant(path)).json();
      expect(JSON.stringify(grant)).not.toContain(TOKEN);
      const native = await consume(grant, f.origin);
      expect(await native.response.text()).toContain(path);
    } finally { f.stop(); }
  });

  test("requires dashboard authorization and the exact origin before any host command", async () => {
    const f = fixture();
    try {
      expect((await f.grant("/sessions", {})).status).toBe(401);
      expect((await f.grant("/sessions", { ...AUTH, origin: "http://127.0.0.1:11111" })).status).toBe(403);
      expect((await f.grant("/sessions", { ...AUTH, origin: "https://example.com" })).status).toBe(403);
      expect((await f.grant("/sessions", { ...AUTH, host: "host.docker.internal" })).status).toBe(403);
      expect(f.calls).toBe(0);
      expect((await f.grant("https://example.com" )).status).toBe(503);
      expect(f.calls).toBe(0);
      expect((await fetch(`${f.origin}/api/agentsview/token`, { headers: AUTH })).status).toBe(404);
    } finally { f.stop(); }
  });

  test("grants one use and keeps credentials off the browser and upstream request headers", async () => {
    const f = fixture();
    try {
      const answer = await f.grant();
      expect(answer.headers.get("cache-control")).toBe("no-store");
      const grant = await answer.json();
      expect(JSON.stringify(grant)).not.toContain(TOKEN);
      expect(JSON.stringify(grant)).not.toContain("dashboard-secret");
      expect((await consume(grant, "https://example.com")).response.status).toBe(403);
      const session = await consume(grant, "null");
      expect(session.response.status).toBe(200);
      expect(await session.response.text()).toContain('/sessions/trace?msg=last');
      expect(session.response.headers.get("set-cookie")).toContain("HttpOnly; SameSite=Strict");
      expect((await consume(grant, f.origin)).response.status).toBe(403);
      const response = await fetch(session.url + "/sessions/trace?msg=last", { headers: { cookie: session.cookie, authorization: "Bearer browser-stale", referer: f.origin } });
      expect(await response.text()).toContain('/__culler/bridge.js');
      const last = f.requests.at(-1)!;
      expect(last.headers.get("authorization")).toBe(`Bearer ${TOKEN}`);
      for (const name of ["cookie", "referer", "origin"]) expect(last.headers.get(name)).toBeNull();
      expect((await fetch(session.url + "/api/v1/sessions")).status).toBe(503);
      expect((await fetch(session.url + "/api/v1/sessions", { headers: { cookie: session.cookie, origin: "https://example.com" } })).status).toBe(403);
      expect((await fetch(session.url + "/api/v1/sessions", { headers: { cookie: session.cookie, host: "host.docker.internal" } })).status).toBe(403);
      expect((await fetch(session.url + "/api/v1/sessions", { headers: { cookie: session.cookie, "sec-fetch-site": "cross-site" } })).status).toBe(403);
      for (const path of ["/api/v1/settings", "/api/v1/%73ettings"]) {
        const settings = await fetch(session.url + path, { headers: { cookie: session.cookie } });
        expect(await settings.json()).toEqual({ require_auth: true });
      }
      expect((await fetch(session.url + "/api/v1/settings", { method: "PUT", headers: { cookie: session.cookie }, body: '{"require_auth":false}' })).status).toBe(403);
      for (const path of ["/api/v1/config", "/api/v1/raw-sync/tokens", "/api/v1/remote-sync/targets", "/_agentsview/startup", "/api/v1/embeddings/status"]) {
        expect((await fetch(session.url + path, { headers: { cookie: session.cookie } })).status).toBe(403);
      }
      const sent = f.requests.length;
      const setup = await fetch(session.url + "/api/v1/embeddings/setup", { method: "POST", headers: { cookie: session.cookie, "content-type": "application/json" }, body: '{"approved_bytes":0}' });
      expect(setup.status).toBe(403);
      expect(await setup.json()).toEqual({ error: "Culler manages the AgentsView service configuration." });
      expect(f.requests.length).toBe(sent);
      const redirect = await fetch(session.url + "/api/v1/redirect", { headers: { cookie: session.cookie } });
      expect(redirect.status).toBe(503);
      expect(redirect.headers.get("location")).toBeNull();
    } finally { f.stop(); }
  });

  test("expired grants and sessions require new authorization", async () => {
    const f = fixture();
    let time = 0;
    const proxy = createAgentsViewProxy(f.mc, () => time);
    try {
      const expired = await proxy.grant("/sessions", f.origin);
      time = 30_001;
      expect((await consume(expired, f.origin)).response.status).toBe(403);
      const session = await consume(await proxy.grant("/sessions/trace?msg=last", f.origin), f.origin);
      time += 12 * 60 * 60 * 1000 + 1;
      const response = await fetch(session.url + "/sessions/trace?msg=last", { headers: { cookie: session.cookie, accept: "text/html" }, redirect: "manual" });
      expect(response.status).toBe(303);
      expect(response.headers.get("location")).toBe(`${f.origin}/agentsview/open?path=%2Fsessions%2Ftrace%3Fmsg%3Dlast`);
    } finally { proxy.stop(); f.stop(); }
  });

  test("deployment sessions stay separate", async () => {
    const first = fixture(); const second = fixture();
    try {
      const one = await consume(await (await first.grant()).json(), first.origin);
      const two = await consume(await (await second.grant()).json(), second.origin);
      expect((await fetch(two.url + "/api/v1/sessions", { headers: { cookie: one.cookie } })).status).toBe(503);
      expect((await fetch(one.url + "/api/v1/sessions", { headers: { cookie: two.cookie } })).status).toBe(503);
    } finally { first.stop(); second.stop(); }
  });

  test("service failures and credential replacement preserve truthful recovery", async () => {
    const f = fixture();
    try {
      const session = await consume(await (await f.grant()).json(), f.origin);
      const read = () => fetch(session.url + "/api/v1/sessions", { headers: { cookie: session.cookie } });
      f.reachable = false;
      expect((await read()).headers.get("x-culler-access-error")).toBe("unavailable");
      f.reachable = true;
      f.token = "b".repeat(64);
      const refused = await read();
      expect(refused.status).toBe(503);
      expect(refused.headers.get("x-culler-access-error")).toBe("refused");
      expect(await refused.text()).not.toContain(TOKEN);
      f.supplied = "b".repeat(64);
      expect((await read()).status).toBe(200);
      f.supplied = "invalid";
      expect((await read()).headers.get("x-culler-access-error")).toBe("credential");
      f.supplied = "b".repeat(64);
      const missing = await f.grant("/sessions/absent");
      expect(missing.status).toBe(503);
      expect((await missing.json()).error.message).toContain("trace is unavailable");
    } finally { f.stop(); }
  });
});
