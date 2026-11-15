import { afterEach, expect, test } from "bun:test";
import { mkdtemp, readFile, readdir, realpath, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startForegroundProviderAccess } from "./provider-foreground";
import type { Exec } from "./types";

const cleanup: (() => Promise<void>)[] = [];
afterEach(async () => { for (const stop of cleanup.splice(0).reverse()) await stop(); });

async function control(path: string, body: Record<string, unknown>): Promise<Record<string, any>> {
  return new Promise((resolve, reject) => {
    let output = "";
    void Bun.connect({ unix: path, socket: {
      open(socket) { socket.write(JSON.stringify({ version: 1, nonce: "a".repeat(32), ...body }) + "\n"); },
      data(_socket, data) { output += Buffer.from(data).toString(); },
      close() { try { resolve(JSON.parse(output)); } catch (error) { reject(error); } },
      error(_socket, error) { reject(error); },
    } }).catch(reject);
  });
}

test("a temporary host provides isolated Culler authority and checks a saved key", async () => {
  const root = await mkdtemp(join(tmpdir(), "foreground-"));
  cleanup.push(() => rm(root, { recursive: true, force: true }));
  const certificate = join(root, "cert.pem");
  const key = join(root, "key.pem");
  const made = Bun.spawnSync(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key,
    "-out", certificate, "-days", "1", "-subj", "/CN=host.docker.internal"], { stdout: "ignore", stderr: "ignore" });
  expect(made.exitCode).toBe(0);
  const cert = await readFile(certificate, "utf8");
  const tls = { ca: cert, cert, key: await readFile(key, "utf8") };
  const calls: string[][] = [];
  const observations: Record<string, unknown>[] = [];
  let ownerReleased = 0;
  const runMc: Exec = async (args, input) => {
    calls.push(args);
    let value: unknown = {};
    switch (args.join(" ")) {
      case "agent-profile runtime-catalog": value = { bindings: {}, connections: { account: {
        credential: "account", kind: "codex-api-key", harness: "codex", channel: "codex", inference_url: "https://example.invalid/v1",
      } } }; break;
      case "agent-profile proxy-tls": value = tls; break;
      case "agent-profile credential-read": value = { record: { version: 1, type: "api_key", credential: "account", channel: "codex", api_key: "synthetic-provider-secret" }, authorization_id: "auth", credential_ref: "ref" }; break;
      case "connection check-start": value = { allowed: true, authorization_id: "auth", operation: "connection_access" }; break;
      case "connection observe":
      case "connection proxy-observe": observations.push(JSON.parse(new TextDecoder().decode(input))); break;
      default: throw new Error("Unexpected host command.");
    }
    return { exitCode: 0, stdout: JSON.stringify(value), stderr: "" };
  };
  const handle = await startForegroundProviderAccess({ mcHome: root, socketHome: root, mcPath: "fixture" }, {
    runMc, acquireOwner: async () => async () => { ownerReleased++; },
  });
  cleanup.push(handle.stop);
  const path = join(root, "health", "provider-acceptance.sock");
  const response = await control(path, { op: "profile_authorize", credential: "account", profile: "profile", profile_revision: "revision",
    run_id: "run", launch_id: "launch", model: "small-model", max_requests: 1, expires_at_ms: Date.now() + 60_000 });
  expect(response.status).toBe("ok");
  expect(response.proxy_config.token).toMatch(/^culler_proxy_[A-Za-z0-9_-]{43}$/);
  expect(JSON.stringify(response)).not.toContain("synthetic-provider-secret");
  expect(JSON.stringify(response)).not.toContain("PRIVATE KEY");
  const status = await control(path, { op: "profile_status", run_id: "run", launch_id: "launch" });
  expect(status.provider_requests).toBe(0);
  const checked = await control(path, { op: "connection_check", credential: "account" });
  expect(checked.status).toBe("ok");
  expect(ownerReleased).toBe(2);
  expect(observations.some(value => value.operation === "connection_access" && value.outcome === "ready")).toBe(true);
  expect(calls.every(args => args[0] === "connection" || args[0] === "agent-profile")).toBe(true);
  const revoked = await control(path, { op: "profile_revoke", run_id: "run", launch_id: "launch" });
  expect(revoked.revoked).toBe(true);
  expect((await control(path, { op: "profile_status", run_id: "run", launch_id: "launch" })).status).toBe("error");
  expect(JSON.stringify(observations)).not.toContain("synthetic-provider-secret");
  const registry = join(root, "health", "provider-proxies");
  const entries = await readdir(registry);
  expect(entries).toHaveLength(1);
  const entry = join(registry, entries[0]!);
  expect((await stat(entry)).mode & 0o777).toBe(0o600);
  expect(JSON.parse(await readFile(entry, "utf8"))).toEqual({ socket_home: await realpath(root) });
  await handle.stop();
  expect(await readdir(registry)).toEqual([]);
});
