import { afterAll, beforeAll, expect, test } from "bun:test";
import { chmod, mkdtemp, realpath, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { observeProviderAccess, refreshConnectionCheck, refreshConnectionsBeforeTick } from "./main";
import { createProviderCredentials, type HostCredentialSnapshot } from "./provider-credentials";
import { makeRig, ok } from "./test-helpers";
import { tick as residentTick } from "./tick-loop";
import type { Exec, ExecResult } from "./types";

const connection = "test-connection";
const conditionID = `connection:${connection}:token_refresh:`;
let root: string, control: string;

async function processResult(argv: string[], env = process.env, input?: Uint8Array): Promise<ExecResult> {
  const child = Bun.spawn(argv, { cwd: resolve(import.meta.dir, "../../mc"), env,
    stdin: input === undefined ? "ignore" : input, stdout: "pipe", stderr: "pipe" });
  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited, new Response(child.stdout).text(), new Response(child.stderr).text(),
  ]);
  return { exitCode, stdout, stderr };
}

beforeAll(async () => {
  root = await realpath(await mkdtemp(join(tmpdir(), "culler-connection-recovery-")));
  control = join(root, "control");
  const built = await processResult(["mise", "exec", "--", "go", "build", "-tags=test_scratch_spine",
    "-o", control, resolve(import.meta.dir, "../testdata/connection-recovery-control.go")]);
  if (built.exitCode !== 0) throw new Error(built.stderr || built.stdout);
}, 300_000);

afterAll(async () => { if (root) await rm(root, { recursive: true, force: true }); });

async function fixture(initial = "ready") {
  const home = await mkdtemp(join(root, "home-"));
  await chmod(home, 0o700);
  const env = { ...process.env, MC_HOME: home, MC_SPINE: join(home, "spine.db"), MC_RUN_JSON: join(home, "absent.json") };
  let snapshot: HostCredentialSnapshot = {
    authorization_id: "synthetic-authorization", credential_ref: "synthetic-reference-1",
    record: { version: 1, type: "account", credential: connection, channel: "codex",
      access_token: "synthetic-old-access", refresh_token: "synthetic-old-refresh", expires_at_ms: 1,
      token_url: "https://provider.invalid/token", client_id: "synthetic-client" },
  };
  let saves = 0;
  const commands: string[][] = [];
  const runMc: Exec = async (args, input) => {
    commands.push(args);
    if (args[0] === "dispatch") return ok(JSON.stringify({ action: "tick", actions: [] }));
    if (args[0] === "agent-profile") {
      const body = JSON.parse(new TextDecoder().decode(input));
      expect(body.connection_id).toBe(connection);
      if (args[1] === "credential-read") return ok(JSON.stringify(snapshot));
      if (args[1] === "credential-renew") {
        expect(body.authorization_id).toBe(snapshot.authorization_id);
        expect(body.credential_ref).toBe(snapshot.credential_ref);
        snapshot = { ...snapshot, record: body.record, credential_ref: `synthetic-reference-${++saves + 1}` };
        return ok("{}");
      }
      throw new Error("The synthetic credential command is unavailable.");
    }
    if (args[0] === "connection" && args[1] === "proxy-observe") {
      const body = JSON.parse(new TextDecoder().decode(input));
      input = new TextEncoder().encode(JSON.stringify({ ...body,
        current_authorization_revision: snapshot.authorization_id, current_credential_ref: snapshot.credential_ref,
        current_credential_type: snapshot.record.type,
      }));
    }
    return processResult([control, ...args], env, input);
  };
  const call = async (args: string[]) => {
    const result = await runMc(args);
    if (result.exitCode !== 0) throw new Error(result.stderr || result.stdout);
    return JSON.parse(result.stdout);
  };
  await call(["init", initial]);
  const { deps, logRecords } = makeRig();
  const services: ReturnType<typeof createProviderCredentials>[] = [];
  const jobs: Promise<void>[] = [];
  const makeService = (fetch: typeof globalThis.fetch) => {
    const service = createProviderCredentials({ runMc, fetch,
      observe: value => observeProviderAccess(runMc, deps.log, value) });
    services.push(service);
    return service;
  };
  const tick = async (service: ReturnType<typeof createProviderCredentials>) => {
    await residentTick({ ...deps, runMc, beforeTick: () => refreshConnectionsBeforeTick(runMc, key => {
      const job = refreshConnectionCheck(runMc, service.renew, key);
      jobs.push(job);
      return job;
    }, deps.log) });
  };
  const settle = async () => { await Promise.allSettled(jobs); };
  const close = async () => { await settle(); for (const service of services) await service.drain(); };
  const replaceCredential = () => {
    snapshot = { ...snapshot, credential_ref: "synthetic-replacement",
      record: { ...snapshot.record, access_token: "synthetic-replacement-access", refresh_token: "synthetic-replacement-refresh", expires_at_ms: Date.now() + 3_600_000 } };
  };
  return { runMc, call, commands, logRecords, makeService, tick, settle, close, jobs, replaceCredential, saved: () => saves };
}

const renewalResponse = () => Response.json({ access_token: "synthetic-new-access", refresh_token: "synthetic-new-refresh", expires_in: 3600 });

test("a due tick restores request-driven renewal failure after host service recreation", async () => {
  const f = await fixture();
  let requests = 0, failing = true;
  const transport = (async (url: string | URL | Request) => {
    expect(String(url)).toBe("https://provider.invalid/token");
    requests += 1;
    if (failing) throw new Error("Synthetic transport failure");
    return renewalResponse();
  }) as typeof fetch;
  let service = f.makeService(transport);
  try {
    const firstStart = Date.now();
    await expect(service.credential(connection, new AbortController().signal)).rejects.toMatchObject({ origin: "provider_renewal" });
    const firstEnd = Date.now();
    let state = await f.call(["state"]);
    expect(state).toMatchObject({ state: "retry_wait", failure_count: 1, request_deadline: "", network_error: "other" });
    expect(Date.parse(state.next_retry_at)).toBeGreaterThanOrEqual(firstStart + 30_000);
    expect(Date.parse(state.next_retry_at)).toBeLessThanOrEqual(firstEnd + 30_000);
    expect(await f.call(["admission"])).toEqual({ allowed: false });
    const failedStatus = await f.call(["system", "status"]);
    expect(failedStatus.conditions.find((row: any) => row.id === conditionID)).toMatchObject({ state: "retry_wait", action: "", failure_count: 1 });
    await f.tick(service); await f.settle();
    expect(requests).toBe(1);
    expect(await f.call(["state"])).toEqual(state);

    await service.drain();
    service = f.makeService(transport);
    await f.call(["due"]);
    expect(await f.call(["admission"])).toEqual({ allowed: false });
    const secondStart = Date.now();
    await f.tick(service); await f.settle();
    const secondEnd = Date.now();
    expect(requests).toBe(2);
    state = await f.call(["state"]);
    expect(state).toMatchObject({ state: "retry_wait", failure_count: 2, request_deadline: "", network_error: "other" });
    expect(Date.parse(state.next_retry_at)).toBeGreaterThanOrEqual(secondStart + 120_000);
    expect(Date.parse(state.next_retry_at)).toBeLessThanOrEqual(secondEnd + 120_000);
    await f.tick(service); await f.settle();
    expect(requests).toBe(2);
    expect(await f.call(["state"])).toEqual(state);

    failing = false;
    await f.call(["due"]);
    await f.tick(service); await f.settle();
    expect(requests).toBe(3);
    expect(f.saved()).toBe(1);
    expect(await f.call(["state"])).toMatchObject({ state: "ready", failure_count: 0, next_retry_at: "", request_deadline: "", first_failure_at: "" });
    expect(await f.call(["admission"])).toEqual({ allowed: true });
    const readyStatus = await f.call(["system", "status"]);
    expect(readyStatus.conditions.find((row: any) => row.id === conditionID)).toBeUndefined();
    expect(readyStatus.connections.find((row: any) => row.id === conditionID).state).toBe("ready");
    await f.tick(service); await f.settle();
    expect(requests).toBe(3);
    expect(f.commands.some(args => args[0] === "connection" && args[1] === "retry")).toBe(false);
    expect(f.commands.filter(args => args[0] === "dispatch")).toHaveLength(5);
  } finally { await f.close(); }
}, 20_000);

test("a tick renews an expired ready connection through its unknown status", async () => {
  const f = await fixture("expired");
  let requests = 0;
  const service = f.makeService((async () => { requests += 1; return renewalResponse(); }) as typeof fetch);
  try {
    expect((await f.call(["state"])).state).toBe("ready");
    expect(await f.call(["admission"])).toEqual({ allowed: false });
    const before = await f.call(["system", "status"]);
    expect(before.conditions.find((row: any) => row.id === conditionID)).toMatchObject({ state: "unknown" });
    await f.tick(service); await f.settle();
    expect(requests).toBe(1);
    expect(await f.call(["admission"])).toEqual({ allowed: true });
    expect((await f.call(["system", "status"])).conditions.find((row: any) => row.id === conditionID)).toBeUndefined();
    expect(f.commands.some(args => args[0] === "connection" && args[1] === "retry")).toBe(false);
  } finally { await f.close(); }
}, 20_000);

test("an obsolete renewal failure cannot stop a replacement credential", async () => {
  const f = await fixture("unknown");
  const entered = Promise.withResolvers<void>();
  const response = Promise.withResolvers<Response>();
  let requests = 0;
  const service = f.makeService((async () => { requests += 1; entered.resolve(); return response.promise; }) as typeof fetch);
  try {
    await f.tick(service);
    await entered.promise;
    expect((await f.call(["state"])).request_deadline).not.toBe("");
    f.replaceCredential();
    response.resolve(Response.json({ error: "invalid_grant" }, { status: 400 }));
    await f.settle();
    expect(await f.call(["state"])).toMatchObject({ state: "unknown", failure_count: 0, request_deadline: "", next_retry_at: "" });
    expect(f.commands.filter(args => args[0] === "connection" && args[1] === "check-cancel")).toHaveLength(1);
    expect(f.commands.filter(args => args[0] === "connection" && args[1] === "observe")).toHaveLength(0);
    await f.tick(service); await f.settle();
    expect(requests).toBe(1);
    expect(await f.call(["admission"])).toEqual({ allowed: true });
    expect((await f.call(["state"])).state).toBe("ready");
  } finally { response.resolve(renewalResponse()); await f.close(); }
}, 20_000);

test("overlapping ticks reserve one renewal while its provider response remains pending", async () => {
  const f = await fixture("unknown");
  const entered = Promise.withResolvers<void>();
  const response = Promise.withResolvers<Response>();
  let requests = 0;
  const service = f.makeService((async () => { requests += 1; entered.resolve(); return response.promise; }) as typeof fetch);
  try {
    const initial = await f.call(["state"]);
    await f.tick(service);
    await entered.promise;
    const active = await f.call(["state"]);
    expect(active.check_sequence).toBe(initial.check_sequence + 1);
    expect(active.request_deadline).not.toBe("");
    await Promise.all([f.tick(service), f.tick(service)]);
    await Promise.allSettled(f.jobs.slice(1));
    expect(requests).toBe(1);
    expect(await f.call(["state"])).toEqual(active);
    response.resolve(renewalResponse());
    await f.settle();
    expect(await f.call(["state"])).toMatchObject({ state: "ready", failure_count: 0, request_deadline: "" });
    expect(requests).toBe(1);
  } finally { response.resolve(renewalResponse()); await f.close(); }
}, 20_000);

test("a stopped authorization refuses automatic renewal", async () => {
  const f = await fixture();
  let requests = 0;
  const service = f.makeService((async () => { requests += 1; return Response.json({ error: "invalid_grant" }, { status: 400 }); }) as typeof fetch);
  try {
    await expect(service.credential(connection, new AbortController().signal)).rejects.toMatchObject({ code: "invalid_grant" });
    const stopped = await f.call(["state"]);
    expect(stopped).toMatchObject({ state: "stopped", failure_count: 1, next_retry_at: "", request_deadline: "" });
    await f.tick(service); await f.settle();
    await refreshConnectionCheck(f.runMc, service.renew, connection);
    expect(requests).toBe(1);
    expect(await f.call(["state"])).toEqual(stopped);
    expect(await f.call(["admission"])).toEqual({ allowed: false });
  } finally { await f.close(); }
}, 20_000);
