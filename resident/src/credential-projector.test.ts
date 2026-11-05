import { afterEach, beforeAll, expect, test } from "bun:test";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { parseRuntimeCatalog, resolveRuntimeBindings, startCredentialProjector, type RuntimeBindingSpec } from "./credential-projector";
import { ProviderCredentialError } from "./provider-proxy";
import { parseProviderAccess } from "../../runner/agent-runner/adapters/provider-access";

let tls: { ca: string; cert: string; key: string };
const cleanup: Array<() => void> = [];
beforeAll(async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-projection-tls-"));
  const file = (name: string) => join(root, name);
  const run = (args: string[]) => {
    const result = Bun.spawnSync(["openssl", ...args], { stdout: "pipe", stderr: "pipe" });
    if (result.exitCode) throw new Error("The test certificate could not be created.");
  };
  try {
    run(["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", file("ca.key"), "-out", file("ca.pem"), "-days", "1", "-subj", "/CN=Synthetic CA"]);
    run(["req", "-newkey", "rsa:2048", "-nodes", "-keyout", file("key.pem"), "-out", file("leaf.csr"), "-subj", "/CN=localhost"]);
    await writeFile(file("leaf.ext"), "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1\n");
    run(["x509", "-req", "-in", file("leaf.csr"), "-CA", file("ca.pem"), "-CAkey", file("ca.key"), "-CAcreateserial", "-out", file("cert.pem"), "-days", "1", "-extfile", file("leaf.ext")]);
    tls = { ca: await readFile(file("ca.pem"), "utf8"), cert: await readFile(file("cert.pem"), "utf8"), key: await readFile(file("key.pem"), "utf8") };
  } finally { await rm(root, { recursive: true, force: true }); }
});
afterEach(() => { for (const close of cleanup.splice(0).reverse()) close(); });

const row: RuntimeBindingSpec = { harness: "codex", credential: "test-connection", channel: "codex", kind: "codex-api-key", inference_url: "https://provider.invalid/v1" };
const runtime = { harness: "codex", id: "test-profile", credential: row.credential, channel: "codex", model: "small-model", base_url: "" };
const launch = (runId = "run-1", launchId = "launch-1") => ({ runId, launchId, model: "small-model", expiresAt: Date.now() + 60_000 });
function fixture(catalog: Record<string, RuntimeBindingSpec> = { "test-profile": row }) {
  const reads: string[] = [];
  const renewals: string[] = [];
  const credentials = {
    credential: async (connection: string) => { reads.push(connection); throw new ProviderCredentialError("credential_store", "synthetic_unavailable"); },
    renew: async (connection: string) => { renewals.push(connection); }, reject: () => {},
    read: async (_connection: string) => ({ authorization_id: "current-revision" }),
  };
  const handle = startCredentialProjector({ tls, isLaunchCurrent: async () => true, credentials: credentials as never }, catalog);
  cleanup.push(() => handle.stop());
  return { ...handle, reads, renewals, credentials };
}
function access(value: unknown) {
  expect(value).not.toHaveProperty("refused");
  return parseProviderAccess((value as { proxyConfig: string }).proxyConfig, "codex");
}
async function request(config: ReturnType<typeof access>, model = "small-model") {
  const url = new URL(config.url); url.hostname = "127.0.0.1";
  const response = await fetch(`${url.toString().replace(/\/$/, "")}/responses`, { method: "POST",
    headers: { authorization: `Bearer ${config.token}`, "content-type": "application/json" },
    body: JSON.stringify({ model, input: "synthetic" }), tls: { ca: tls.ca } });
  return { status: response.status, body: await response.json() as any };
}

test("the private projection contains a Culler token and a public certificate only", () => {
  const f = fixture();
  const projection = f.projector.project(runtime, launch())!;
  const config = access(projection);
  expect(projection).toEqual({ env: {}, proxyConfig: JSON.stringify(config) });
  expect(config.authMode).toBe("api-key");
  expect(config.ca).toBe(tls.ca);
  expect(config.token).toHaveLength(56);
  expect(JSON.stringify(projection)).not.toContain("PRIVATE KEY");
  expect(f.reads).toEqual([]);
  expect(f.launches()).toEqual([{ runId: "run-1", launchId: "launch-1" }]);
});

test("account and key connections share the proxy contract", () => {
  const f = fixture({ "test-profile": { ...row, kind: "codex-oauth" } });
  expect(access(f.projector.project(runtime, launch())).authMode).toBe("account");
});

test("a runtime cannot replace its connection or harness", () => {
  const f = fixture();
  for (const patch of [{ credential: "another" }, { id: "missing" }, { harness: "claude-sdk" }, { channel: "claude" }]) {
    expect(f.projector.project({ ...runtime, ...patch }, launch())).toHaveProperty("refused");
  }
  expect(f.projector.project({ ...runtime, harness: "fake", id: "fake" }, launch())).toBeNull();
  expect(f.launches()).toEqual([]);
});

test("invalid or repeated launches receive no second authority", () => {
  const f = fixture();
  for (const patch of [{ model: "" }, { expiresAt: 0 }, { expiresAt: Number.POSITIVE_INFINITY }, { runId: "../run" }]) {
    expect(f.projector.project(runtime, { ...launch(), ...patch })).toHaveProperty("refused");
  }
  const first = access(f.projector.project(runtime, launch()));
  expect(f.projector.project(runtime, launch())).toHaveProperty("refused");
  const second = access(f.projector.project(runtime, launch("run-1", "launch-2")));
  expect(first.token).not.toBe(second.token);
});

test("the proxy checks the selected model before host credential access", async () => {
  const f = fixture();
  const config = access(f.projector.project(runtime, launch()));
  expect((await request(config, "unselected-model")).status).toBe(403);
  expect(f.reads).toEqual([]);
  expect((await request(config)).status).toBe(503);
  expect(f.reads).toEqual([row.credential]);
});

test("one launch revocation preserves another launch, and run revocation closes both", async () => {
  const f = fixture();
  const first = access(f.projector.project(runtime, launch()));
  const second = access(f.projector.project(runtime, launch("run-1", "launch-2")));
  f.projector.revoke("run-1", "launch-1");
  expect((await request(first)).status).toBe(401);
  expect((await request(second)).status).toBe(503);
  expect(f.status("run-1", "launch-1")).toBeNull();
  f.projector.revoke("run-1");
  expect((await request(second)).status).toBe(401);
  expect(f.launches()).toEqual([]);
});

test("acceptance grants use the same proxy and remain outside the resident run list", async () => {
  const f = fixture({ "connection:test-connection": row });
  const config = access(f.authorizeConnection(row.credential, "acceptance-profile", launch(), 2));
  expect((await request(config)).status).toBe(503);
  expect(f.launches()).toEqual([]);
  expect(f.status("run-1", "launch-1")).not.toBeNull();
  expect(() => f.authorizeConnection("missing", "profile", launch())).toThrow("unavailable");
  await f.refreshConnection(row.credential);
  expect(f.renewals).toEqual([row.credential]);
});

test("catalog reload refuses old profile bindings and conflicting destinations", async () => {
  const f = fixture();
  await f.reload({ "new-profile": row });
  expect(f.projector.project(runtime, launch())).toHaveProperty("refused");
  expect(() => fixture({ first: row, second: { ...row, inference_url: "https://another.invalid" } })).toThrow("conflicting provider routes");
});

test("the catalog rejects provider secrets and untrusted destinations", () => {
  for (const patch of [{ api_key: "synthetic-secret" }, { inference_url: "http://provider.invalid" },
    { inference_url: "https://name:secret@provider.invalid" }, { channel: "unknown" }, { kind: "unknown" }]) {
    expect(() => parseRuntimeCatalog({ test: { ...row, ...patch } })).toThrow();
  }
  expect(resolveRuntimeBindings({ first: { token_url: "https://renewal.invalid", client_id: "test-client" } }, { first: row, second: row }).second)
    .toMatchObject({ token_url: "https://renewal.invalid", client_id: "test-client" });
});


test("connection revocation removes only that connection's launch authority", async () => {
  const other = { ...row, credential: "other-connection" };
  const f = fixture({ "test-profile": row, "other-profile": other });
  const first = access(f.projector.project(runtime, launch("first", "first")));
  f.projector.project({ ...runtime, id: "other-profile", credential: other.credential }, launch("other", "other"));
  f.revokeConnection(row.credential, "disconnected-revision");
  expect(f.projector.has("first", "first")).toBe(false);
  expect(f.projector.has("other", "other")).toBe(true);
  expect((await request(first)).status).toBe(401);
  expect(f.reads).toEqual([]);
});

test("a trusted test transport receives host credentials after the actual proxy validates the launch", async () => {
  const destinations: string[] = [];
  const handle = startCredentialProjector({ tls, credentials: {
    credential: async () => ({ headers: { authorization: "Bearer synthetic-host-key" }, authorizationRevision: "revision", credentialRef: "ref" }),
    reject() {}, drain: async () => {},
  } as never, fetch: (async (destination, options) => {
    destinations.push(String(destination));
    expect(new Headers(options?.headers).get("authorization")).toBe("Bearer synthetic-host-key");
    return Response.json({ accepted: true });
  }) as typeof fetch }, { "connection:test-connection": row });
  cleanup.push(handle.stop);
  const config = access(handle.authorizeConnection(row.credential, "profile", launch(), 1));
  expect((await request(config, "other-model")).status).toBe(403);
  expect(destinations).toEqual([]);
  expect((await request(config)).body).toEqual({ accepted: true });
  expect(destinations).toEqual(["https://provider.invalid/v1/responses"]);
  expect(handle.status("run-1", "launch-1")).toEqual({ providerRequests: 1 });
});


test("disconnection blocks new grants until a different saved authorization appears", async () => {
  const f = fixture();
  f.revokeConnection(row.credential, "current-revision");
  expect(f.projector.project(runtime, launch())).toHaveProperty("refused");
  await f.reload({ "test-profile": row });
  expect(f.projector.project(runtime, launch())).toHaveProperty("refused");
  f.credentials.read = async () => { throw new Error("missing"); };
  await f.reload({ "test-profile": row });
  expect(f.projector.project(runtime, launch())).toHaveProperty("refused");
  f.credentials.read = async () => ({ authorization_id: "new-revision" });
  await f.reload({ "test-profile": row });
  expect(f.projector.project(runtime, launch())).toHaveProperty("proxyConfig");
});

test("a late recovery read cannot clear a newer disconnection", async () => {
  const f = fixture();
  f.revokeConnection(row.credential, "first-revision");
  let resolve!: (value: { authorization_id: string }) => void;
  f.credentials.read = () => new Promise(yes => { resolve = yes; });
  const reload = f.reload({ "test-profile": row });
  f.revokeConnection(row.credential, "second-revision");
  resolve({ authorization_id: "second-revision" });
  await reload;
  expect(f.projector.project(runtime, launch())).toHaveProperty("refused");
  f.credentials.read = async () => ({ authorization_id: "third-revision" });
  await f.reload({ "test-profile": row });
  expect(f.projector.project(runtime, launch())).toHaveProperty("proxyConfig");
});
