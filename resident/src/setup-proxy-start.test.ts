import { afterAll, beforeAll, expect, test } from "bun:test";
import { chmod, mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createEnvironmentLane } from "./environment-lane";
import { startCredentialProjector } from "./credential-projector";
import { createProviderLifetimeCheck, providerLaunchIsCurrent } from "./provider-lifetime";
import { parseProviderAccess } from "../../runner/agent-runner/adapters/provider-access";
import { makeRig, ok } from "./test-helpers";
import type { Exec, ExecResult } from "./types";

const image = `sha256:${"a".repeat(64)}`;
const operation = "proxy-operation";
let root: string, control: string;
let tls: { ca: string; cert: string; key: string };

async function processResult(argv: string[], env = process.env, input?: Uint8Array): Promise<ExecResult> {
  const child = Bun.spawn(argv, { cwd: resolve(import.meta.dir, "../../mc"), env,
    stdin: input === undefined ? "ignore" : input, stdout: "pipe", stderr: "pipe" });
  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited, new Response(child.stdout).text(), new Response(child.stderr).text(),
  ]);
  return { exitCode, stdout, stderr };
}

beforeAll(async () => {
  root = await realpath(await mkdtemp(join(tmpdir(), "culler-setup-proxy-test-")));
  control = join(root, "control");
  const built = await processResult(["mise", "exec", "--", "go", "build", "-tags=test_scratch_spine",
    "-o", control, resolve(import.meta.dir, "../testdata/setup-proxy-control.go")]);
  if (built.exitCode !== 0) throw new Error(built.stderr || built.stdout);
  const file = (name: string) => join(root, name);
  const certificate = async (args: string[]) => {
    const result = await processResult(["openssl", ...args]);
    if (result.exitCode !== 0) throw new Error("The synthetic proxy certificate could not be created.");
  };
  await certificate(["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", file("ca.key"), "-out", file("ca.pem"), "-days", "1", "-subj", "/CN=Synthetic CA"]);
  await certificate(["req", "-newkey", "rsa:2048", "-nodes", "-keyout", file("key.pem"), "-out", file("leaf.csr"), "-subj", "/CN=localhost"]);
  await writeFile(file("leaf.ext"), "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1\n");
  await certificate(["x509", "-req", "-in", file("leaf.csr"), "-CA", file("ca.pem"), "-CAkey", file("ca.key"), "-CAcreateserial", "-out", file("cert.pem"), "-days", "1", "-extfile", file("leaf.ext")]);
  tls = { ca: await readFile(file("ca.pem"), "utf8"), cert: await readFile(file("cert.pem"), "utf8"), key: await readFile(file("key.pem"), "utf8") };
}, 300_000);

afterAll(async () => { if (root) await rm(root, { recursive: true, force: true }); });

test.each(["setup", "validate"])("%s proxy access survives a lifetime sweep before the first heartbeat", async kind => {
  const home = await mkdtemp(join(root, `${kind}-`));
  await chmod(home, 0o700);
  const env = { ...process.env, MC_HOME: home, MC_SPINE: join(home, "spine.db"), MC_RUN_JSON: join(home, "absent.json") };
  const runMc: Exec = (args, input) => processResult([control, ...args], env, input);
  const call = async (args: string[], body?: unknown) => {
    const result = await runMc(args, body === undefined ? undefined : new TextEncoder().encode(JSON.stringify(body)));
    if (result.exitCode !== 0) throw new Error(result.stderr || result.stdout);
    return JSON.parse(result.stdout);
  };
  await call(["init", kind]);
  const { deps, fakeFs, logs } = makeRig();
  deps.config = { ...deps.config, mcHome: home, runtimeVolume: "test-runtime", projectImage: "test-project", releaseBuildId: "fixture-release" };
  deps.runMc = (args, input, options) => args[0] === "environment" && args[1] === "image-check"
    ? Promise.resolve(ok("{}"))
    : args[0] === "config" && args[1] === "show" ? Promise.resolve(ok(JSON.stringify({ families: [] }))) : runMc(args, input, options);
  const events: string[] = [];
  let providerRequests = 0;
  const handle = startCredentialProjector({ tls,
    credentials: {
      credential: async () => ({ headers: { authorization: "Bearer synthetic-host-key" }, authorizationRevision: "fixture", credentialRef: "fixture" }),
      reject() {}, drain: async () => {},
    } as never,
    isLaunchCurrent: (run, launch, signal) => providerLaunchIsCurrent(runMc, run, launch, signal),
    fetch: (async (destination, options) => {
      expect(String(destination)).toBe("https://provider.invalid/v1/responses");
      expect(new Headers(options?.headers).get("authorization")).toBe("Bearer synthetic-host-key");
      providerRequests += 1;
      return Response.json({ output: "synthetic reply" });
    }) as typeof fetch,
  }, { "test-profile": { harness: "codex", credential: "test-connection", channel: "codex", kind: "codex-api-key", inference_url: "https://provider.invalid/v1" } });
  deps.credentials = handle.projector;
  const sweep = createProviderLifetimeCheck(handle, runMc);
  const accessPath = `${home}/environment-runs/${operation}/provider-access.json`;
  let access: ReturnType<typeof parseProviderAccess> | undefined;
  const request = async () => {
    const url = new URL(access!.url); url.hostname = "127.0.0.1";
    const response = await fetch(`${url.toString().replace(/\/$/, "")}/responses`, {
      method: "POST", headers: { authorization: `Bearer ${access!.token}`, "content-type": "application/json" },
      body: JSON.stringify({ model: "small-model", input: "synthetic" }), tls: { ca: tls.ca },
    });
    await response.text();
    return response.status;
  };
  deps.docker = async args => {
    if (args[0] === "image") return ok(JSON.stringify([{ Id: image }]));
    if (args[0] === "container") return { exitCode: 1, stdout: "", stderr: "No such container" };
    if (args[0] === "create") {
      events.push("create");
      expect(args).toContain(`type=bind,src=${accessPath},dst=/mc/provider-access.json,readonly`);
      access = parseProviderAccess(fakeFs.writes.get(accessPath)!, "codex");
      expect(await call(["state"])).toEqual({ status: "queued" });
      for (let index = 0; index < 3; index += 1) await sweep();
      events.push("queued-sweeps");
      expect(await request()).toBe(200);
      expect(handle.projector.has(operation, operation)).toBe(true);
      events.push("queued-request");
    }
    if (args[0] === "start") {
      events.push("start");
      expect(await call(["state"])).toEqual({ status: "queued" });
      await call(["environment", "heartbeat"], { operation_id: operation });
      expect(await call(["state"])).toEqual({ status: "running" });
      events.push("heartbeat");
      await sweep();
      expect(parseProviderAccess(fakeFs.writes.get(accessPath)!, "codex").token).toBe(access!.token);
      expect(await request()).toBe(200);
      events.push("running-request");
      await call(["environment", "operation-result"], { operation_id: operation, inputs_digest: `sha256:${"c".repeat(64)}`,
        status: "failed", error: "The synthetic process ended." });
      expect(await call(["state"])).toEqual({ status: "failed" });
      await sweep();
      expect(await request()).toBe(401);
      expect(providerRequests).toBe(2);
      events.push("terminal-refused");
    }
    return ok("");
  };
  const lane = createEnvironmentLane(deps);
  try {
    await lane.request();
    const deadline = Date.now() + 10_000;
    while (lane.activeCount() > 0 && Date.now() < deadline) await Bun.sleep(5);
    expect(lane.activeCount()).toBe(0);
    expect(logs).toEqual([]);
    expect(events).toEqual(["create", "queued-sweeps", "queued-request", "start", "heartbeat", "running-request", "terminal-refused"]);
    expect(providerRequests).toBe(2);
    expect(handle.projector.has(operation, operation)).toBe(false);
    expect(fakeFs.writes.has(accessPath)).toBe(false);
  } finally { await lane.stop(); handle.stop(); }
}, 20_000);
