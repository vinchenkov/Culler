import { describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, realpath, rm, stat, symlink } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import {
  canonicalResidentHome,
  readWorkspacesViaMc,
  canonicalResidentConfig,
  createBackupChore,
  expireArtifacts,
  followLines,
  prepareAttachmentTreeViaMc,
  proveAttachmentConfinement,
  publishTickReceipt,
  residentControlIdentity,
  routeNudge,
  refreshConnectionCheck,
  refreshConnectionsBeforeTick,
  observeProviderAccess,
} from "./main";
import { ProviderCredentialError } from "./provider-proxy";
import { createProviderCredentials, type HostCredentialSnapshot } from "./provider-credentials";
import type { StartEvent } from "./repository-lane";
import {
  AttachmentConfinementHealth,
} from "./attachment-health";
import { greenAttachmentCanaryReceipt, makeRig } from "./test-helpers";

function errno(code: string): Error & { code: string } {
  return Object.assign(new Error(`store failed: ${code}`), { code });
}

test("resident canonicalizes a symlink-spelled MC_HOME once at startup", async () => {
  const parent = await mkdtemp(join(tmpdir(), "mc-resident-home-"));
  const physical = join(parent, "physical");
  const alias = join(parent, "alias");
  await mkdir(physical);
  await symlink(physical, alias);
  const canonical = await realpath(physical);
  expect(await canonicalResidentHome(alias)).toBe(canonical);
  const config = await canonicalResidentConfig({
    mcHome: alias,
    releaseBuildId: "release",
    configSchemaVersion: 1,
  });
  expect(residentControlIdentity(config).mcHome).toBe(canonical);
  const calls: string[][] = [];
  const receipt = await prepareAttachmentTreeViaMc(async (argv) => {
    calls.push(argv);
    return {
      exitCode: 0,
      stdout: JSON.stringify({
        inbound_published: `${canonical}/attachments/h-tree/in/published`,
        outbound: `${canonical}/attachments/h-tree/out`,
        session: "h-tree",
      }),
      stderr: "",
    };
  }, config.mcHome, "h-tree");
  expect(calls).toEqual([["__attachment-tree-prepare", "h-tree"]]);
  expect(receipt.outbound).toBe(`${canonical}/attachments/h-tree/out`);
  await rm(parent, { recursive: true });
});

test("resident mints attachment health only from this boot's command result", async () => {
  const health = new AttachmentConfinementHealth();
  const calls: string[][] = [];
  await proveAttachmentConfinement(async (argv) => {
    calls.push(argv);
    return {
      exitCode: 0,
      stdout: JSON.stringify(greenAttachmentCanaryReceipt()),
      stderr: "",
    };
  }, health);
  expect(calls).toEqual([["__attachment-canary"]]);
  expect(() => health.assertHealthy()).not.toThrow();

  const red = new AttachmentConfinementHealth();
  await expect(proveAttachmentConfinement(async () => ({
    exitCode: 1, stdout: "", stderr: "kernel denial missing",
  }), red)).rejects.toThrow("kernel denial missing");
  expect(() => red.assertHealthy()).toThrow("unhealthy");
});

test("resident tick receipt is atomic, owner-only, and release-bound", async () => {
  const home = await mkdtemp(join(tmpdir(), "mc-resident-receipt-"));
  const when = new Date("2026-07-22T12:34:56.000Z");
  const previous = new Date("2026-07-22T12:33:56.000Z");
  await publishTickReceipt(home, "a".repeat(40), 1, "resident-instance", 7, when, previous);
  const path = join(home, "health", "resident-tick.json");
  expect(JSON.parse(await readFile(path, "utf8"))).toEqual({
    version: 1,
    release_build_id: "a".repeat(40),
    config_schema_version: 1,
    resident_instance_id: "resident-instance",
    sequence: 7,
    previous_completed_at: previous.toISOString(),
    completed_at: when.toISOString(),
  });
  expect((await stat(path)).mode & 0o777).toBe(0o600);
  await rm(home, { recursive: true });
});

describe("resident backup chore", () => {
  test("snapshots at startup and only when the serialized interval is due", async () => {
    let clock = 0;
    const calls: string[][] = [];
    const chore = createBackupChore(async (argv) => {
      calls.push(argv);
      return { exitCode: 0, stdout: JSON.stringify({ snapshot: `/mc/backups/${calls.length}.db`, bytes: 4096 }), stderr: "" };
    }, 1000, () => clock);
    await chore.startup();
    clock = 999;
    await chore.beforeTick();
    expect(calls).toEqual([["backup"]]);
    clock = 1000;
    await chore.beforeTick();
    expect(calls).toEqual([["backup"], ["backup"]]);
  });

  test("failed and malformed backups do not advance the due clock", async () => {
    let clock = 0;
    let attempt = 0;
    const chore = createBackupChore(async () => {
      attempt += 1;
      if (attempt === 1) return { exitCode: 0, stdout: JSON.stringify({ snapshot: "/mc/backups/ok.db", bytes: 1 }), stderr: "" };
      if (attempt === 2) return { exitCode: 1, stdout: "{}", stderr: "snapshot refused" };
      if (attempt === 3) return { exitCode: 0, stdout: "{}", stderr: "" };
      return { exitCode: 0, stdout: JSON.stringify({ snapshot: "/mc/backups/retry.db", bytes: 2 }), stderr: "" };
    }, 1000, () => clock);
    await chore.startup();
    clock = 1000;
    await expect(chore.beforeTick()).rejects.toThrow("snapshot refused");
    await expect(chore.beforeTick()).rejects.toThrow("invalid receipt");
    await chore.beforeTick();
    expect(attempt).toBe(4);
  });
});

test("Collie reads the current Project catalog and rejects a failed read", async () => {
  const roots = [{ id: "second", root: "/host/second" }];
  const calls: string[][] = [];
  let current: unknown[] = [];
  const read = () => readWorkspacesViaMc(async (args) => {
    calls.push(args);
    return { exitCode: 0, stdout: JSON.stringify({ workspaces: current }), stderr: "" };
  });
  expect(await read()).toEqual([]);
  current = roots;
  expect(await read()).toEqual(roots);
  expect(calls).toEqual([["project", "workspaces"], ["project", "workspaces"]]);
  await expect(readWorkspacesViaMc(async () => ({ exitCode: 1, stdout: "{}", stderr: "unavailable" }))).rejects.toThrow("unavailable");
  current = [{ id: "invalid", root: "relative" }];
  await expect(read()).rejects.toThrow("invalid");
});

describe("repository lane wiring", () => {
  test("a packet decision goes to the lane and each other nudge wakes the tick", () => {
    const ticks: number[] = [];
    const events: StartEvent[] = [];
    routeNudge("packet-decide", () => ticks.push(1), (event) => events.push(event));
    routeNudge("wake", () => ticks.push(1), (event) => events.push(event));
    expect(events).toEqual(["packet-decision"]);
    expect(ticks).toHaveLength(1);
  });

  test("expiry end sends a start request", async () => {
    const rig = makeRig();
    let requests = 0;
    rig.mc.enqueue({ exitCode: 0, stdout: "{}", stderr: "" }, { exitCode: 1, stdout: "", stderr: "The expiry failed." });
    await expireArtifacts(rig.deps.runMc, rig.deps.log, () => { requests += 1; });
    expect(requests).toBe(1);
    await expireArtifacts(rig.deps.runMc, rig.deps.log, () => { requests += 1; });
    expect(requests).toBe(2);
    await expireArtifacts(async () => { throw new Error("helper unavailable"); }, rig.deps.log, () => { requests += 1; });
    expect(requests).toBe(3);
    expect(rig.mc.calls).toEqual([["__prune-artifacts"], ["__prune-artifacts"]]);
    expect(rig.logRecords.filter((record) => record.event === "artifacts.expiry_failed")).toHaveLength(2);
  });

  test("a follower delivers each output line and reports the exit code", async () => {
    const lines: string[] = [];
    const follower = followLines(["/bin/sh", "-c", "printf 'first\\nsecond\\nlast'; exit 3"], (line) => lines.push(line));
    expect(await follower.done).toBe(3);
    expect(lines).toEqual(["first", "second", "last"]);
  });

  test("a stopped follower ends", async () => {
    const lines: string[] = [];
    const follower = followLines(["/bin/sh", "-c", "echo ready; exec sleep 30"], (line) => lines.push(line));
    for (let i = 0; i < 100 && lines.length === 0; i++) await new Promise((resolve) => setTimeout(resolve, 20));
    expect(lines).toEqual(["ready"]);
    follower.stop();
    expect(await follower.done).not.toBe(0);
  });
});


test("host failures close connection checks without provider failure evidence", async () => {
  for (const error of [new ProviderCredentialError("credential_store", "credential_save"), new ProviderCredentialError("proxy", "diagnostic_unavailable"), new Error("host command failed")]) {
    const calls: { action: string; body: any }[] = [];
    const runMc = async (args: string[], input?: Uint8Array) => {
      calls.push({ action: args[1]!, body: JSON.parse(new TextDecoder().decode(input)) });
      return { exitCode: 0, stderr: "", stdout: JSON.stringify({ allowed: true, authorization_id: "current", operation: "token_refresh" }) };
    };
    await expect(refreshConnectionCheck(runMc, async () => { throw error; }, "test")).rejects.toThrow();
    expect(calls.map(call => call.action)).toEqual(["check-start", "check-cancel"]);
    expect(calls[1]!.body).toEqual({ connection_id: "test", authorization_id: "current", operation: "token_refresh", check_id: calls[0]!.body.check_id });
  }
});

test("a provider renewal failure closes its check without another failure observation", async () => {
  const calls: { action: string; body: any }[] = [];
  const runMc = async (args: string[], input?: Uint8Array) => {
    calls.push({ action: args[1]!, body: JSON.parse(new TextDecoder().decode(input)) });
    return { exitCode: 0, stderr: "", stdout: JSON.stringify({ allowed: true, authorization_id: "current", operation: "token_refresh" }) };
  };
  const failure = new ProviderCredentialError("provider_renewal", "invalid_grant");
  await expect(refreshConnectionCheck(runMc, async () => { throw failure; }, "test")).rejects.toBe(failure);
  expect(calls.map(call => call.action)).toEqual(["check-start", "check-cancel"]);
  expect(calls[1]!.body).toEqual({ connection_id: "test", authorization_id: "current", operation: "token_refresh", check_id: calls[0]!.body.check_id });
});

test("the resident selects account retries and leaves model retries to agent runs", async () => {
  const rig = makeRig();
  const conditions = [
    { connection_id: "unknown", state: "unknown", id: "connection:unknown:token_refresh:" },
    { connection_id: "due", state: "retry_wait", id: "connection:due:token_refresh:" },
    { connection_id: "delayed", state: "retry_wait", id: "connection:delayed:token_refresh:" },
    { connection_id: "ready", state: "ready", id: "connection:ready:token_refresh:" },
    { connection_id: "stopped", state: "stopped", id: "connection:stopped:token_refresh:" },
    { connection_id: "key", state: "unknown", id: "connection:key:connection_access:" },
    { connection_id: "model", state: "retry_wait", id: "connection:model:model_access:profile" },
  ];
  rig.mc.enqueue({ exitCode: 0, stdout: JSON.stringify({ conditions }), stderr: "" });
  const selected: string[] = [];
  await refreshConnectionsBeforeTick(rig.deps.runMc, async connection => { selected.push(connection); }, rig.deps.log);
  expect(selected).toEqual(["unknown", "due", "delayed"]);
  expect(rig.mc.calls).toEqual([["system", "status"]]);
});

test("the resident can dispatch while account renewal awaits a response", async () => {
  const rig = makeRig();
  rig.mc.enqueue({ exitCode: 0, stdout: JSON.stringify({ conditions: [
    { connection_id: "account", state: "retry_wait", id: "connection:account:token_refresh:" },
  ] }), stderr: "" });
  const response = Promise.withResolvers<void>();
  let started = false;
  await refreshConnectionsBeforeTick(rig.deps.runMc, async () => { started = true; await response.promise; }, rig.deps.log);
  expect(started).toBe(true);
  expect(rig.logRecords).toHaveLength(0);
  response.reject(new Error("private provider response"));
  await new Promise(resolve => setTimeout(resolve, 0));
  expect(rig.logRecords.filter(record => record.event === "credential.refresh_failed")).toHaveLength(1);
  expect(JSON.stringify(rig.logRecords)).not.toContain("private provider response");
});

test("a failed status read starts no account renewal", async () => {
  const rig = makeRig();
  rig.mc.enqueue({ exitCode: 1, stdout: "{}", stderr: "status unavailable" });
  let started = false;
  await refreshConnectionsBeforeTick(rig.deps.runMc, async () => { started = true; }, rig.deps.log);
  expect(started).toBe(false);
});

test("the canonical observer exposes read and renewal diagnostic failures without repeating provider access", async () => {
  for (const failedOperation of ["service", "token_refresh"]) {
    const rig = makeRig();
    let snapshot: HostCredentialSnapshot = {
      authorization_id: "authorization-one", credential_ref: "credential-one",
      record: { version: 1, type: "account", credential: "one", channel: "codex", access_token: "synthetic-old-access",
        refresh_token: "synthetic-old-refresh", token_url: "https://provider.invalid/token", client_id: "native-client", expires_at_ms: 1 },
    };
    let providerRequests = 0;
    let saves = 0;
    const runMc = async (args: string[], input?: Uint8Array) => {
      if (args[1] === "credential-read") return { exitCode: 0, stderr: "", stdout: JSON.stringify(snapshot) };
      const body = JSON.parse(new TextDecoder().decode(input));
      if (args[1] === "credential-renew") {
        saves++;
        snapshot = { record: body.record, authorization_id: snapshot.authorization_id, credential_ref: "credential-two" };
        return { exitCode: 0, stderr: "", stdout: "{}" };
      }
      if (args[1] === "proxy-observe") return { exitCode: body.operation === failedOperation ? 1 : 0,
        stderr: "synthetic-private-command-output", stdout: "{}" };
      throw new Error("The test received an unexpected host command.");
    };
    const service = createProviderCredentials({ runMc, observe: value => observeProviderAccess(runMc, rig.deps.log, value),
      fetch: (async () => { providerRequests++; return Response.json({ access_token: "synthetic-new-access", refresh_token: "synthetic-new-refresh", expires_in: 3600 }); }) as typeof fetch });
    await expect(service.credential("one", new AbortController().signal)).rejects.toMatchObject({ origin: "proxy", code: "diagnostic_unavailable" });
    expect(providerRequests).toBe(failedOperation === "service" ? 0 : 1);
    expect(saves).toBe(failedOperation === "service" ? 0 : 1);
    expect(rig.logRecords.filter(record => record.event === "credential.refresh_failed")).toHaveLength(1);
    expect(JSON.stringify(rig.logRecords)).not.toContain("synthetic-private-command-output");
    if (failedOperation === "token_refresh") {
      expect((await service.credential("one", new AbortController().signal)).headers.authorization).toBe("Bearer synthetic-new-access");
      expect(providerRequests).toBe(1);
      expect(saves).toBe(1);
    }
    await service.drain();
  }
});

test("resident update checks refuse incomplete runtime and profile data before listener checks", async () => {
  const { verifyResidentUpdate } = await import("./main");
  const checks = ["mc-home", "spine", "deployment-identity", "container-runtime"];
  const config = { mcHome: "/fixture" };
  let listeners = 0;
  const calls: string[][] = [];
  const runMc = async (argv: string[]) => {
    calls.push(argv);
    return { exitCode: 0, stderr: "", stdout: argv[0] === "doctor"
      ? JSON.stringify({ findings: checks.map(check => ({ check, status: "ok" })) })
      : JSON.stringify({ bindings: {} }) };
  };
  await verifyResidentUpdate(config, runMc, async () => { listeners++; });
  expect(listeners).toBe(1);
  expect(calls).toEqual([["doctor"], ["agent-profile", "runtime-catalog"]]);
  await expect(verifyResidentUpdate(config, async () => ({ exitCode: 0, stderr: "", stdout: JSON.stringify({ findings: [] }) }), async () => { listeners++; })).rejects.toThrow("check failed");
  expect(listeners).toBe(1);
  await expect(verifyResidentUpdate(config, runMc, async () => { throw new Error("socket occupied"); })).rejects.toThrow("socket occupied");
});

test("resident update checks preserve an unsafe socket file", async () => {
  const { checkUpdateListeners } = await import("./main");
  const { writeFile } = await import("node:fs/promises");
  const home = await mkdtemp(join(tmpdir(), "culler-update-listener-"));
  try {
    const path = join(home, "resident.socket");
    await writeFile(path, "preserve this file");
    await expect(checkUpdateListeners(home)).rejects.toThrow("unsafe file");
    expect(await readFile(path, "utf8")).toBe("preserve this file");
    await rm(path);
    await checkUpdateListeners(home);
  } finally { await rm(home, { recursive: true, force: true }); }
});


test("resident verification records a current response with ordinary work disabled", async () => {
  const { publishResidentAvailability } = await import("./main");
  const home = await mkdtemp(join(tmpdir(), "culler-update-availability-"));
  const now = new Date("2026-11-05T12:00:00Z");
  try {
    await publishResidentAvailability(home, "verification-instance", false, now);
    expect(JSON.parse(await readFile(join(home, "health", "resident-availability.json"), "utf8"))).toEqual({
      resident_instance_id: "verification-instance", observed_at: now.toISOString(), can_commit: false,
    });
  } finally { await rm(home, { recursive: true, force: true }); }
});
