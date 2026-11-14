// server.test.ts — bind/auth are fail-closed and the server
// serves the static shell plus the API mirror.

import { describe, expect, test } from "bun:test";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";

import { PAGE_POLICY, parseBind, startDashboardServer, updateBlocksMutations } from "./server";
import { FakeMc, deps, ok } from "./test-helpers";

const ephemeral = { bind: "127.0.0.1:0", authToken: "s3cret" };
const bearer = { authorization: "Bearer s3cret" };

test("the dashboard refuses mutations from an earlier release", async () => {
  const mc = new FakeMc().on(["collie", "start"], ok({ session_id: "h-1" }));
  const server = startDashboardServer({ ...ephemeral, releaseID: "release-two" }, deps({ mc }));
  try {
    const page = await fetch(server.url);
    expect(page.headers.get("cache-control")).toBe("no-store");
    expect(await page.text()).toContain('<meta name="culler-release" content="release-two">');
    for (const release of [undefined, "release-one"]) {
      const response = await fetch(`${server.url}api/sessions`, {
        method: "POST", headers: { ...bearer, ...(release ? { "x-culler-release": release } : {}) },
      });
      expect(response.status).toBe(409);
      expect(await response.text()).toContain("Reload this page");
    }
    expect(mc.calls).toEqual([]);
    expect((await fetch(`${server.url}api/sessions`, {
      method: "POST", headers: { ...bearer, "x-culler-release": "release-two" },
    })).status).toBe(200);
    expect(mc.calls).toEqual([["collie", "start", "--from", "dashboard:dash-feedf00d"]]);
  } finally { server.stop(); }
});

test("an incomplete update permits reads and refuses mutations", async () => {
  const mc = new FakeMc().on(["collie", "list"], ok({ sessions: [] }));
  const server = startDashboardServer({ ...ephemeral, maintenance: () => true }, deps({ mc }));
  try {
    expect((await fetch(`${server.url}api/sessions`, { headers: bearer })).status).toBe(200);
    expect((await fetch(`${server.url}api/sessions`, { method: "POST", headers: bearer })).status).toBe(503);
    expect(mc.calls).toEqual([["collie", "list"]]);
  } finally { server.stop(); }
});

test("the authenticated health response identifies the dashboard release", async () => {
  const mc = new FakeMc().on(["health"], ok({ ok: true, findings: [] }));
  const server = startDashboardServer({ ...ephemeral, releaseID: "release-two" }, deps({ mc }));
  try {
    expect((await fetch(`${server.url}api/health`, { headers: bearer })).headers.get("x-culler-release")).toBe("release-two");
    expect((await fetch(`${server.url}api/health`)).headers.get("x-culler-release")).toBeNull();
  } finally { server.stop(); }
});

test("the update journal permits mutations only after commit or restoration", () => {
  const home = mkdtempSync(join(tmpdir(), "culler-update-gate-"));
  const path = join(home, "updates", "transaction.json");
  const state = { version: 1, id: "a".repeat(32), candidate: { commit: "b".repeat(40) },
    deployment: createHash("sha256").update("mc-deployment-v1\0" + home).digest("hex").slice(0, 12) };
  try {
    expect(updateBlocksMutations(home)).toBe(false);
    mkdirSync(join(home, "updates"));
    for (const phase of ["stop", "prepare", "checkpoint", "replace", "verify", "restore", "unknown", "committed", "complete", "restored"]) {
      writeFileSync(path, JSON.stringify({ ...state, phase }), { mode: 0o600 });
      expect(updateBlocksMutations(home)).toBe(!["committed", "complete", "restored"].includes(phase));
    }
    mkdirSync(join(home, "state"));
    const maintenance = join(home, "state", "install-maintenance.json");
    for (const phase of ["committed", "complete", "restored"]) {
      writeFileSync(path, JSON.stringify({ ...state, phase }));
      writeFileSync(maintenance, JSON.stringify({ id: state.id }), { mode: 0o600 });
      expect(updateBlocksMutations(home)).toBe(true);
      writeFileSync(maintenance, JSON.stringify({ id: "ordinary-restart-1234" }));
      expect(updateBlocksMutations(home)).toBe(false);
      writeFileSync(maintenance, "unreadable request");
      expect(updateBlocksMutations(home)).toBe(true);
      rmSync(maintenance);
      expect(updateBlocksMutations(home)).toBe(false);
    }
    symlinkSync(join(home, "absent"), maintenance);
    expect(updateBlocksMutations(home)).toBe(true);
    rmSync(maintenance);
    writeFileSync(path, JSON.stringify({ ...state, version: 2, phase: "complete" }));
    expect(updateBlocksMutations(home)).toBe(true);
    writeFileSync(path, JSON.stringify({ ...state, deployment: "foreign", phase: "complete" }));
    expect(updateBlocksMutations(home)).toBe(true);
    writeFileSync(path, "{");
    expect(updateBlocksMutations(home)).toBe(true);
    writeFileSync(path, JSON.stringify({ ...state, phase: "complete" }));
    chmodSync(path, 0o644);
    expect(updateBlocksMutations(home)).toBe(true);
    rmSync(path);
    symlinkSync(join(home, "absent"), path);
    expect(updateBlocksMutations(home)).toBe(true);
  } finally { rmSync(home, { recursive: true, force: true }); }
});

describe("bind grammar", () => {
  test("parses host:port and bracketed IPv6", () => {
    expect(parseBind("127.0.0.1:7333")).toEqual({ hostname: "127.0.0.1", port: 7333 });
    expect(parseBind("[::1]:7333")).toEqual({ hostname: "::1", port: 7333 });
  });

  test("refuses a portless or hostless bind", () => {
    expect(() => parseBind("127.0.0.1")).toThrow("dashboard bind must be host:port");
    expect(() => parseBind("127.0.0.1", "wiki bind")).toThrow("wiki bind must be host:port");
    expect(() => parseBind(":7333")).toThrow("host");
    expect(() => parseBind("127.0.0.1:notaport")).toThrow("host:port");
  });
});

describe("fail-closed bind/auth", () => {
  test("any bind without a token is refused at startup, loopback included", () => {
    expect(() => startDashboardServer({ bind: "0.0.0.0:0", authToken: "" }, deps())).toThrow(
      "auth token is required on every bind",
    );
    expect(() => startDashboardServer({ bind: "127.0.0.1:0", authToken: "" }, deps())).toThrow(
      "auth token is required on every bind",
    );
  });

  test("the token gates every /api request before any mc spawn", async () => {
    const mc = new FakeMc().on(["collie", "list"], ok({ sessions: [] }));
    const server = startDashboardServer(ephemeral, deps({ mc }));
    try {
      const bare = await fetch(`${server.url}api/sessions`);
      expect(bare.status).toBe(401);
      expect(mc.calls).toEqual([]);
      const authed = await fetch(`${server.url}api/sessions`, {
        headers: { authorization: "Bearer s3cret" },
      });
      expect(authed.status).toBe(200);
      expect(mc.calls).toEqual([["collie", "list"]]);
      // The data-free static shell stays reachable so a browser can load it
      // and then present the token.
      expect((await fetch(server.url)).status).toBe(200);
    } finally {
      server.stop();
    }
  });

  test("Project deletion requires the dashboard token and the local origin", async () => {
    const mc = new FakeMc().on(["project", "delete", "garden", "--confirm"], ok({ project_id: "garden" }));
    const server = startDashboardServer(ephemeral, deps({ mc }));
    try {
      const address = `${server.url}api/projects/garden/delete`;
      const body = JSON.stringify({ confirm: true });
      expect((await fetch(address, { method: "POST", body })).status).toBe(401);
      expect((await fetch(address, { method: "POST", body,
        headers: { authorization: "Bearer s3cret", "content-type": "application/json", origin: "https://foreign.example" } })).status).toBe(403);
      expect(mc.calls).toEqual([]);
      expect((await fetch(address, { method: "POST", body,
        headers: { authorization: "Bearer s3cret", "content-type": "application/json", origin: server.url.replace(/\/$/, "") } })).status).toBe(200);
      expect(mc.calls).toEqual([["project", "delete", "garden", "--confirm"]]);
    } finally { server.stop(); }
  });

  test("a foreign Host header is refused — DNS rebinding boundary", async () => {
    const mc = new FakeMc().on(["collie", "list"], ok({ sessions: [] }));
    const server = startDashboardServer(ephemeral, deps({ mc }));
    try {
      const rebound = await fetch(`${server.url}api/sessions`, {
        headers: { host: "attacker.example:7333" },
      });
      expect(rebound.status).toBe(403);
      expect(mc.calls).toEqual([]);
    } finally {
      server.stop();
    }
  });

  test("a cross-origin request is refused, same-origin passes — CSRF boundary", async () => {
    const mc = new FakeMc().on(["collie", "start"], ok({ session_id: "h-1" }));
    const server = startDashboardServer(ephemeral, deps({ mc }));
    try {
      const forged = await fetch(`${server.url}api/sessions`, {
        method: "POST",
        headers: { origin: "https://evil.example" },
      });
      expect(forged.status).toBe(403);
      const sandboxed = await fetch(`${server.url}api/sessions`, {
        method: "POST",
        headers: { origin: "null" },
      });
      expect(sandboxed.status).toBe(403);
      expect(mc.calls).toEqual([]);
      const same = await fetch(`${server.url}api/sessions`, {
        method: "POST",
        headers: { origin: `http://127.0.0.1:${server.port}`, ...bearer },
      });
      expect(same.status).toBe(200);
    } finally {
      server.stop();
    }
  });
});

describe("static shell", () => {
  test("the dashboard address serves no wiki export or library copy", async () => {
    const mc = new FakeMc();
    const server = startDashboardServer(ephemeral, deps({ mc }));
    try {
      for (const path of ["openwiki/index.html", "openwiki/marked.min.js", "_openwiki/marked.min.js", "openwiki/provenance.json"]) {
        const response = await fetch(`${server.url}${path}`);
        expect(response.status).toBe(404);
        expect(response.headers.get("access-control-allow-origin")).toBeNull();
      }
      const sandboxed = await fetch(`${server.url}app.js`, { headers: { origin: "null" } });
      expect(sandboxed.status).toBe(403);
      expect(mc.calls).toEqual([]);
    } finally { server.stop(); }
  });

  test("serves the shell page at / and the app module", async () => {
    const server = startDashboardServer(ephemeral, deps());
    try {
      const page = await fetch(server.url);
      expect(page.status).toBe(200);
      expect(page.headers.get("content-type")).toContain("text/html");
      const html = await page.text();
      expect(html).toContain("Collie");
      const js = await fetch(`${server.url}app.js`);
      expect(js.status).toBe(200);
    } finally {
      server.stop();
    }
  });

  test("the shell page frames only its own origin", async () => {
    const server = startDashboardServer(ephemeral, deps());
    try {
      expect(PAGE_POLICY).toBe("frame-src 'self'");
      expect((await fetch(server.url)).headers.get("content-security-policy")).toBe(PAGE_POLICY);
      expect((await fetch(`${server.url}app.js`)).headers.get("content-security-policy")).toBeNull();
    } finally {
      server.stop();
    }
  });

  test("anything else is 404, never a directory listing", async () => {
    const server = startDashboardServer(ephemeral, deps());
    try {
      expect((await fetch(`${server.url}ui/`)).status).toBe(404);
      expect((await fetch(`${server.url}..%2fserver.ts`)).status).toBe(404);
    } finally {
      server.stop();
    }
  });
});

describe("native Project folder chooser", () => {
  test("requires authorization, a local origin, and POST before it opens", async () => {
    let calls = 0;
    const server = startDashboardServer({ ...ephemeral, chooseFolder: async () => { calls++; return "/Users/test/My Project/"; } }, deps());
    const url = `${server.url}api/projects/choose-folder`;
    try {
      expect((await fetch(url, { method: "POST" })).status).toBe(401);
      expect((await fetch(url, { method: "POST", headers: { ...bearer, origin: "https://example.com" } })).status).toBe(403);
      expect((await fetch(url, { headers: bearer })).status).toBe(405);
      expect(calls).toBe(0);
      const response = await fetch(url, { method: "POST", headers: bearer });
      expect(await response.json()).toEqual({ path: "/Users/test/My Project/" });
      expect(calls).toBe(1);
    } finally { server.stop(); }
  });

  test("returns cancellation and recovers after a chooser failure", async () => {
    let fail = true;
    const server = startDashboardServer({ ...ephemeral, chooseFolder: async () => {
      if (fail) throw new Error("Enter the folder path.");
      return null;
    } }, deps());
    try {
      const request = () => fetch(`${server.url}api/projects/choose-folder`, { method: "POST", headers: bearer });
      expect((await request()).status).toBe(503);
      fail = false;
      expect(await (await request()).json()).toEqual({ path: null });
    } finally { server.stop(); }
  });
});
