import { afterAll, afterEach, beforeAll, describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import {
  ProviderCredentialError,
  startProviderProxy,
  type ProviderCredential,
  type ProviderProxyDependencies,
  type ProviderProxyHandle,
  type ProviderRequestObservation,
  type ProviderRoute,
  type ProviderRunAuthorization,
} from "./provider-proxy";

let certificates: { ca: string; cert: string; key: string; mismatch: string; expired: string; renewed: string };
const cleanup: Array<() => void> = [];

beforeAll(async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-proxy-tls-"));
  try {
    const source = join(root, "main.go");
    await writeFile(source, `package main
import("crypto/ecdsa";"crypto/elliptic";"crypto/rand";"crypto/x509";"crypto/x509/pkix";"encoding/json";"encoding/pem";"math/big";"net";"os";"time")
func main(){
 key,_:=ecdsa.GenerateKey(elliptic.P256(),rand.Reader)
 caKey,_:=ecdsa.GenerateKey(elliptic.P256(),rand.Reader)
 now:=time.Now()
 ca:=&x509.Certificate{SerialNumber:big.NewInt(1),Subject:pkix.Name{CommonName:"Culler isolated test CA"},NotBefore:now.Add(-24*time.Hour),NotAfter:now.Add(24*time.Hour),IsCA:true,BasicConstraintsValid:true,KeyUsage:x509.KeyUsageCertSign}
 caBytes,_:=x509.CreateCertificate(rand.Reader,ca,ca,&caKey.PublicKey,caKey)
 cert:=func(serial int64,name string,expired bool)string{
  c:=&x509.Certificate{SerialNumber:big.NewInt(serial),Subject:pkix.Name{CommonName:name},DNSNames:[]string{name,"host.docker.internal"},NotBefore:now.Add(-2*time.Hour),NotAfter:now.Add(2*time.Hour),KeyUsage:x509.KeyUsageDigitalSignature,ExtKeyUsage:[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
  if name=="localhost"{c.IPAddresses=[]net.IP{net.ParseIP("127.0.0.1")}}
  if expired{c.NotAfter=now.Add(-time.Hour)}
  b,_:=x509.CreateCertificate(rand.Reader,c,ca,&key.PublicKey,caKey)
  return string(pem.EncodeToMemory(&pem.Block{Type:"CERTIFICATE",Bytes:b}))
 }
 raw,_:=x509.MarshalPKCS8PrivateKey(key)
 json.NewEncoder(os.Stdout).Encode(map[string]string{"ca":string(pem.EncodeToMemory(&pem.Block{Type:"CERTIFICATE",Bytes:caBytes})),"key":string(pem.EncodeToMemory(&pem.Block{Type:"PRIVATE KEY",Bytes:raw})),"cert":cert(2,"localhost",false),"mismatch":cert(3,"another.invalid",false),"expired":cert(4,"localhost",true),"renewed":cert(5,"localhost",false)})
}
`, { mode: 0o600 });
    const result = spawnSync("mise", ["exec", "--", "go", "run", source], { encoding: "utf8", timeout: 30_000 });
    if (result.status !== 0) throw new Error(`The isolated test certificates could not be created: ${result.stderr}`);
    certificates = JSON.parse(result.stdout);
  } finally { await rm(root, { recursive: true, force: true }); }
}, 40_000);

afterEach(() => { for (const close of cleanup.splice(0).reverse()) close(); });
afterAll(() => { certificates = undefined as any; });

const providerSecret = "synthetic-provider-access-2EDdGCnXgRjTJV87";
const renewalSecret = "synthetic-provider-refresh-BqcuWXaRYqW83ZG2";
const route: ProviderRoute = {
  baseURL: "https://provider.invalid/v1",
  operations: [
    { id: "model", method: "POST", path: "/responses", modelRequired: true, modelFields: [["secondary", "model"]] },
    { id: "models", method: "GET", path: "/models", queryKeys: ["limit"] },
  ],
  requestHeaders: ["protocol-beta", "protocol-version"],
  responseHeaders: ["x-rate-limit-remaining"],
};

function credential(overrides: Partial<ProviderCredential> = {}): ProviderCredential {
  return {
    headers: { authorization: `Bearer ${providerSecret}`, "account-id": "host-account" },
    authorizationRevision: "revision-1", credentialRef: "credential-1", secrets: [providerSecret, renewalSecret],
    ...overrides,
  };
}

function authorization(overrides: Partial<ProviderRunAuthorization> = {}): ProviderRunAuthorization {
  return {
    runId: "run-1", launchId: "launch-1", connection: "connection-1", route: "official",
    models: ["small-model"], operations: ["model", "models"], expiresAt: Date.now() + 60_000,
    profileId: "profile-1", profileRevision: "profile-revision-1", attemptId: "attempt-1",
    limits: { maxBodyBytes: 4096, maxDurationMs: 10_000, maxConcurrent: 2, requestsPerMinute: 30, maxRequests: 20 },
    ...overrides,
  };
}

function service(handler: (request: Request) => Response | Promise<Response>, cert = certificates.cert) {
  const server = Bun.serve({
    hostname: "127.0.0.1", port: 0, tls: { cert, key: certificates.key }, idleTimeout: 0, fetch: handler,
  });
  cleanup.push(() => server.stop(true));
  return `https://localhost:${server.port}`;
}

function proxy(overrides: Partial<ProviderProxyDependencies> = {}, routes: Record<string, ProviderRoute> = { official: route }, cert = certificates.cert) {
  const observations: ProviderRequestObservation[] = [];
  const sent: Array<{ url: string; init: RequestInit }> = [];
  const handle = startProviderProxy({ tls: { cert, key: certificates.key }, routes }, {
    credential: async () => credential(),
    observe: observation => { observations.push(observation); },
    fetch: (async (url: string, init: RequestInit) => {
      sent.push({ url, init });
      return Response.json({ output: "ok" });
    }) as typeof fetch,
    ...overrides,
  });
  cleanup.push(() => handle.stop());
  return { handle, observations, sent };
}

function localURL(handle: ProviderProxyHandle, path = "/responses") {
  return `${handle.url.replace("host.docker.internal", "localhost")}${path}`;
}

function call(handle: ProviderProxyHandle, token: string, init: RequestInit = {}, path = "/responses"): Promise<Response> {
  return fetch(localURL(handle, path), {
    method: "POST", headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
    body: JSON.stringify({ model: "small-model", input: "one word" }),
    tls: { ca: certificates.ca }, ...init,
  } as any);
}

function trustedTransport(destination: string, observe?: (url: string, init: RequestInit) => void): typeof fetch {
  return (async (url: string, init: RequestInit) => {
    observe?.(url, init);
    const path = new URL(url).pathname;
    return fetch(destination + path, { ...init, tls: { ca: certificates.ca } } as any);
  }) as typeof fetch;
}

function deferred<T = void>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

async function waitFor(check: () => boolean, timeout = 2000): Promise<void> {
  const end = Date.now() + timeout;
  while (!check()) {
    if (Date.now() > end) throw new Error("The expected proxy event did not arrive.");
    await Bun.sleep(5);
  }
}

describe("provider proxy authorization", () => {
  test("managed launches require current host control state before credential and provider access", async () => {
    for (const scenario of ["ended", "unavailable", "missing", "ended during renewal", "current"]) {
      let checks = 0;
      let reads = 0;
      const deps: Partial<ProviderProxyDependencies> = { credential: async () => { reads++; return credential(); } };
      if (scenario !== "missing") deps.isLaunchCurrent = async spec => {
        expect(spec.runId).toBe("run-1"); expect(spec.launchId).toBe("launch-1");
        checks++;
        if (scenario === "unavailable") throw new Error("private control error");
        return scenario === "current" || scenario === "ended during renewal" && checks === 1;
      };
      const { handle, sent, observations } = proxy(deps);
      const { token } = handle.issue(authorization({ requireLiveness: true }));
      const response = await call(handle, token);
      await response.text();
      if (scenario === "current") {
        expect(response.status).toBe(200); expect(checks).toBe(2); expect(sent).toHaveLength(1);
      } else {
        expect(response.status).toBe(["missing", "unavailable"].includes(scenario) ? 503 : 401);
        expect(sent).toHaveLength(0);
        expect(reads).toBe(scenario === "ended during renewal" ? 1 : 0);
        expect(observations[0]!.origin).toBe(["missing", "unavailable"].includes(scenario) ? "proxy" : "proxy_authorization");
        expect(JSON.stringify(observations)).not.toContain("private control error");
      }
    }
  });

  test("uses an HTTPS loopback listener and opaque authority for one launch", async () => {
    const { handle, sent, observations } = proxy();
    const issued = handle.issue(authorization());
    expect(handle.url).toMatch(/^https:\/\/host\.docker\.internal:/);
    expect(issued.token).toMatch(/^culler_proxy_[A-Za-z0-9_-]{43}$/);
    expect(Buffer.from(issued.token.slice("culler_proxy_".length), "base64url").byteLength).toBe(32);
    const response = await call(handle, issued.token);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ output: "ok" });
    await waitFor(() => observations.length === 1);
    expect(sent).toHaveLength(1);
    expect(observations[0]).toMatchObject({
      run_id: "run-1", launch_id: "launch-1", connection: "connection-1", profile_id: "profile-1",
      profile_revision: "profile-revision-1", attempt_id: "attempt-1", authorization_revision: "revision-1",
      credential_ref: "credential-1", operation: "model", provider_sent: true, origin: "provider", outcome: "response_complete",
    });
    expect(observations[0]!.request_id).toBe(response.headers.get("x-culler-request-id"));
    const evidence = JSON.stringify(observations);
    for (const secret of [issued.token, providerSecret, renewalSecret, "one word"]) expect(evidence).not.toContain(secret);
  });

  test("rejects missing, incorrect, and ambiguous authority before credential access", async () => {
    let reads = 0;
    const { handle, sent } = proxy({ credential: async () => { reads++; return credential(); } });
    const { token } = handle.issue(authorization());
    for (const headers of [
      { "content-type": "application/json" },
      { authorization: "Bearer wrong-token", "content-type": "application/json" },
      { authorization: `Bearer ${token}`, "x-api-key": token, "content-type": "application/json" },
    ]) expect((await call(handle, token, { headers })).status).toBe(401);
    expect(reads).toBe(0);
    expect(sent).toHaveLength(0);
  });

  test("accepts a launch token through the API key header and replaces it", async () => {
    const { handle, sent } = proxy();
    const { token } = handle.issue(authorization());
    const response = await call(handle, token, { headers: { "x-api-key": token, "content-type": "application/json" } });
    expect(response.status).toBe(200);
    await response.text();
    expect(new Headers(sent[0]!.init.headers).get("authorization")).toBe(`Bearer ${providerSecret}`);
    expect(new Headers(sent[0]!.init.headers).has("x-api-key")).toBe(false);
  });

  test("refuses a forbidden model, nested model, operation, method, and query", async () => {
    const { handle, sent } = proxy();
    const { token } = handle.issue(authorization());
    for (const body of [{ model: "large-model" }, { input: "missing model" }, { model: "small-model", secondary: { model: "large-model" } }]) {
      expect((await call(handle, token, { body: JSON.stringify(body) })).status).toBe(403);
    }
    expect((await call(handle, token, {}, "/oauth/token")).status).toBe(403);
    expect((await call(handle, token, {}, "/responses?base_url=https://attacker.invalid")).status).toBe(403);
    expect((await call(handle, token, { method: "DELETE", body: undefined })).status).toBe(403);
    expect((await call(handle, token, { method: "GET", body: undefined }, "/models?limit=1&limit=2")).status).toBe(403);
    const models = await call(handle, token, { method: "GET", body: undefined }, "/models?limit=1");
    expect(models.status).toBe(200);
    await models.text();
    expect(sent).toHaveLength(1);
  });

  test("strips client identity and destination headers and preserves declared protocol headers", async () => {
    let received: Headers | undefined;
    let destination: string | undefined;
    const upstream = service(request => { received = request.headers; return Response.json({ accepted: true }); });
    const { handle } = proxy({ fetch: trustedTransport(upstream, url => { destination = url; }) });
    const { token } = handle.issue(authorization());
    const response = await call(handle, token, { headers: {
      authorization: `Bearer ${token}`, "content-type": "application/json", "account-id": "attacker-account",
      "provider-organization": "attacker-org", "provider-workspace": "attacker-workspace",
      "x-forwarded-host": "attacker.invalid", forwarded: "host=attacker.invalid", "base-url": "https://attacker.invalid",
      "protocol-beta": "test-extension", "protocol-version": "1", cookie: `session=${token}`,
      "x-culler-request-id": "attacker-request", "x-secret": providerSecret,
    } });
    expect(response.status).toBe(200);
    await response.text();
    expect(destination).toBe("https://provider.invalid/v1/responses");
    expect(received!.get("authorization")).toBe(`Bearer ${providerSecret}`);
    expect(received!.get("account-id")).toBe("host-account");
    for (const header of ["provider-organization", "provider-workspace", "x-forwarded-host", "forwarded", "base-url", "cookie", "x-culler-request-id", "x-secret"]) {
      expect(received!.has(header)).toBe(false);
    }
    expect(received!.get("protocol-beta")).toBe("test-extension");
    expect(received!.get("protocol-version")).toBe("1");
    expect(JSON.stringify([...received!])).not.toContain(token);
  });

  test("duplicate model keys cannot change the object that reaches the provider", async () => {
    const { handle, sent } = proxy();
    const { token } = handle.issue(authorization());
    const response = await call(handle, token, { body: '{"model":"large-model","model":"small-model","secondary":{"model":"large-model","model":"small-model"}}' });
    expect(response.status).toBe(200);
    await response.text();
    expect(sent).toHaveLength(1);
    const forwarded = Buffer.from(sent[0]!.init.body as Uint8Array).toString();
    expect(forwarded).toBe('{"model":"small-model","secondary":{"model":"small-model"}}');
    expect(forwarded).not.toContain("large-model");
    expect((await call(handle, token, { body: '{"model":"small-model","model":"large-model"}' })).status).toBe(403);
    expect((await call(handle, token, { body: '{"model":"small-model","secondary":{"model":"small-model","model":"large-model"}}' })).status).toBe(403);
    expect(sent).toHaveLength(1);
  });

  test("scope snapshots resist later mutation and each launch receives fresh authority", async () => {
    const { handle } = proxy();
    const spec = authorization();
    const first = handle.issue(spec);
    spec.models.push("large-model");
    spec.limits.maxRequests = 1000;
    expect((await call(handle, first.token, { body: '{"model":"large-model"}' })).status).toBe(403);
    const second = handle.issue(authorization());
    expect(second.token).not.toBe(first.token);
    expect((await call(handle, first.token)).status).toBe(401);
    expect((await call(handle, second.token)).status).toBe(200);
  });

  test("expiry and connection revocation preserve unrelated authorizations", async () => {
    let clock = Date.now();
    const { handle } = proxy({ now: () => clock });
    const first = handle.issue(authorization({ expiresAt: clock + 1000 }));
    const second = handle.issue(authorization({ connection: "connection-2", launchId: "launch-2", expiresAt: clock + 5000 }));
    clock += 1001;
    expect((await call(handle, first.token)).status).toBe(401);
    expect(handle.extend(first.authorizationId, clock + 1000)).toBe(false);
    expect(handle.extend(second.authorizationId, clock + 6000)).toBe(true);
    handle.revokeConnection("connection-1");
    expect((await call(handle, second.token)).status).toBe(200);
    handle.revokeConnection("connection-2");
    expect((await call(handle, second.token)).status).toBe(401);
  });

  test("restart rejects old authority and exposes no credential or authorization endpoint", async () => {
    const first = proxy();
    const { token } = first.handle.issue(authorization());
    first.handle.stop();
    const second = proxy();
    expect((await call(second.handle, token)).status).toBe(401);
    const current = second.handle.issue(authorization());
    for (const path of ["/oauth/token", "/credentials", "/authorize", "/extend"]) {
      const response = await call(second.handle, current.token, {}, path);
      expect(response.status).toBe(403);
      const body = await response.text();
      for (const secret of [token, current.token, providerSecret, renewalSecret]) expect(body).not.toContain(secret);
    }
  });

  test("a route reload preserves unchanged launches and revokes changed routes atomically", async () => {
    const { handle } = proxy({}, { official: route, other: { ...route, baseURL: "https://second.invalid" } });
    const first = handle.issue(authorization());
    const second = handle.issue(authorization({ launchId: "launch-2", route: "other" }));
    expect(() => handle.setRoutes({ official: { ...route, baseURL: "http://invalid" } })).toThrow();
    await (await call(handle, first.token)).text();
    handle.setRoutes({ official: structuredClone(route), other: { ...route, baseURL: "https://third.invalid" } });
    expect((await call(handle, second.token)).status).toBe(401);
    expect((await call(handle, first.token)).status).toBe(200);
    handle.setRoutes({});
    expect((await call(handle, first.token)).status).toBe(401);
  });
});

describe("provider proxy limits", () => {
  test("enforces declared and streamed body size before provider access", async () => {
    let reads = 0;
    const { handle, sent } = proxy({ credential: async () => { reads++; return credential(); } });
    const spec = authorization();
    spec.limits.maxBodyBytes = 64;
    const { token } = handle.issue(spec);
    const body = JSON.stringify({ model: "small-model", input: "x".repeat(100) });
    expect((await call(handle, token, { body })).status).toBe(413);
    expect((await call(handle, token, {
      body: new ReadableStream({ start(output) { output.enqueue(new TextEncoder().encode(body)); output.close(); } }),
    } as any)).status).toBe(413);
    expect(sent).toHaveLength(0);
    expect(reads).toBe(0);
  });

  test("counts each provider request and distinguishes a local limit from provider quota", async () => {
    const { handle, sent, observations } = proxy();
    const spec = authorization();
    spec.limits.maxRequests = 1;
    const { token, authorizationId } = handle.issue(spec);
    const first = await call(handle, token);
    await first.text();
    const denied = await call(handle, token);
    expect(denied.status).toBe(429);
    expect(denied.headers.get("x-culler-origin")).toBe("proxy_limit");
    expect(sent).toHaveLength(1);
    expect(observations.at(-1)).toMatchObject({ origin: "proxy_limit", http_status: 429, provider_sent: false });
    expect(handle.status(authorizationId)).toEqual({ requests: 1, modelRequests: 1, active: 0 });
    handle.revoke(authorizationId);
    expect(handle.status(authorizationId)).toBeNull();
  });

  test("holds the concurrency slot until response completion", async () => {
    const finish = deferred();
    const started = deferred();
    let sent = 0;
    const { handle } = proxy({ fetch: (async () => {
      sent++;
      return new Response(new ReadableStream({
        async start(output) {
          output.enqueue(new TextEncoder().encode("first\n")); started.resolve();
          await finish.promise;
          try { output.close(); } catch {}
        },
      }));
    }) as typeof fetch });
    const spec = authorization();
    spec.limits.maxConcurrent = 1;
    const { token } = handle.issue(spec);
    const first = await call(handle, token);
    await started.promise;
    expect((await call(handle, token)).status).toBe(429);
    expect(sent).toBe(1);
    finish.resolve();
    await first.text();
    const after = await call(handle, token);
    expect(after.status).toBe(200);
    await after.text();
  });

  test("rate limits use the authorization clock and recover after the window", async () => {
    let clock = Date.now();
    const { handle, sent } = proxy({ now: () => clock });
    const spec = authorization({ expiresAt: clock + 120_000 });
    spec.limits.requestsPerMinute = 1;
    const { token } = handle.issue(spec);
    await (await call(handle, token)).text();
    expect((await call(handle, token)).status).toBe(429);
    clock += 60_001;
    await (await call(handle, token)).text();
    expect(sent).toHaveLength(2);
  });

  test("the request deadline bounds a credential resolver that never returns", async () => {
    const { handle, observations } = proxy({ credential: () => new Promise(() => {}) });
    const spec = authorization();
    spec.limits.maxDurationMs = 20;
    const { token } = handle.issue(spec);
    const response = await call(handle, token);
    expect(response.status).toBe(504);
    expect(observations[0]).toMatchObject({ provider_sent: false, origin: "proxy" });
  });

  test("rejects invalid limits, unsafe routes, and identity header rules", () => {
    const { handle } = proxy();
    for (const value of [0, -1, Infinity, NaN, 1.5]) {
      const spec = authorization();
      spec.limits.maxRequests = value;
      expect(() => handle.issue(spec)).toThrow("run authorization");
    }
    for (const baseURL of ["http://provider.invalid", "https://user:secret@provider.invalid", "https://provider.invalid?target=x"]) {
      expect(() => proxy({}, { official: { ...route, baseURL } })).toThrow("provider route");
    }
    for (const header of ["authorization", "x-api-key", "x-forwarded-host", "account-id", "set-cookie"]) {
      expect(() => proxy({}, { official: { ...route, requestHeaders: [header] } })).toThrow("header rule");
    }
  });
});

describe("provider proxy response delivery", () => {
  test("records bounded stream error evidence from HTTP 200 before the client closes", async () => {
    for (const [event, payload, code] of [
      ["error", { type: "error", error: { type: "overloaded_error", message: providerSecret } }, "overloaded_error"],
      ["error", { type: "error", code: "rate_limit_exceeded", message: providerSecret }, "rate_limit_exceeded"],
      ["response.failed", { type: "response.failed", response: { error: { code: "invalid_api_key", message: providerSecret } } }, "invalid_api_key"],
      ["error", { type: "error", error: { type: "invalid_request_error", code: "invalid_encrypted_content", message: providerSecret } }, "invalid_encrypted_content"],
      ["error", { type: "error", error: { type: "invalid_request_error", message: providerSecret } }, "invalid_request_error"],
      ["error", { type: "error", code: "arbitrary_private_value", message: providerSecret }, "provider_stream_error"],
    ] as const) {
      const finish = deferred();
      const wire = `event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`;
      const { handle, observations } = proxy({ fetch: (async () => new Response(new ReadableStream({
        async start(output) {
          for (let at = 0; at < wire.length; at += 7) output.enqueue(Buffer.from(wire.slice(at, at + 7)));
          await finish.promise;
          try { output.close(); } catch {}
        },
      }), { headers: { "content-type": "text/event-stream" } })) as typeof fetch });
      const { token } = handle.issue(authorization());
      const response = await call(handle, token);
      expect(response.status).toBe(200);
      const reader = response.body!.getReader();
      let delivered = "";
      while (!delivered.includes("\n\n")) { const next = await reader.read(); delivered += Buffer.from(next.value!).toString(); }
      await waitFor(() => observations.length === 1);
      expect(observations[0]).toMatchObject({ origin: "provider", outcome: "interrupted", http_status: 200, provider_error_code: code });
      expect(delivered).toContain("[REDACTED]");
      expect(JSON.stringify(observations)).not.toContain(providerSecret);
      expect(JSON.stringify(observations)).not.toContain("arbitrary_private_value");
      finish.resolve();
      await reader.cancel();
    }
  });

  test("handles an oversized stream error with bounded generic evidence", async () => {
    const wire = `event: error\ndata: ${JSON.stringify({ type: "error", error: { message: "x".repeat(130_000) } })}\n\n`;
    const { handle, observations } = proxy({ fetch: (async () => new Response(wire, { headers: { "content-type": "text/event-stream" } })) as typeof fetch });
    await (await call(handle, handle.issue(authorization()).token)).text();
    await waitFor(() => observations.length === 1);
    expect(observations[0]).toMatchObject({ origin: "provider", outcome: "interrupted", provider_error_code: "provider_stream_error" });
    expect(JSON.stringify(observations[0]).length).toBeLessThan(2048);
  });

  test("redacts split encoded credentials and mixed JSON Unicode escapes", async () => {
    const variants = [
      Buffer.from(providerSecret).toString("base64"), Buffer.from(renewalSecret).toString("base64url"),
      [...providerSecret].map(char => `%${char.charCodeAt(0).toString(16).padStart(2, "0").toUpperCase()}`).join(""),
      [...providerSecret].map((char, index) => index % 2 ? `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}` : char).join(""),
    ];
    const wire = variants.map(value => `event: error\ndata: {"type":"error","error":{"type":"authentication_error","message":"${value}"}}\n\n`).join("");
    const { handle } = proxy({ fetch: (async () => new Response(new ReadableStream({ start(output) {
      for (let at = 0; at < wire.length; at += 11) output.enqueue(Buffer.from(wire.slice(at, at + 11)));
      output.close();
    } }), { headers: { "content-type": "text/event-stream" } })) as typeof fetch });
    const delivered = await (await call(handle, handle.issue(authorization()).token)).text();
    for (const variant of [...variants, providerSecret, renewalSecret]) expect(delivered).not.toContain(variant);
    expect(delivered.match(/\[REDACTED\]/g)).toHaveLength(4);
    for (const frame of delivered.trim().split("\n\n")) expect(JSON.parse(frame.split("data: ")[1]!).error.message).toBe("[REDACTED]");
  });

  test("decodes bounded JSON errors before secret redaction", async () => {
    const mixed = [...providerSecret].map((char, index) => index % 2 ? `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}` : char).join("");
    const { handle } = proxy({ fetch: (async () => new Response(`{"error":{"code":"invalid_api_key","message":"${mixed}"}}`, { status: 401 })) as typeof fetch });
    const response = await call(handle, handle.issue(authorization()).token);
    expect((await response.json()).error.message).toBe("[REDACTED]");
  });

  test("redacts mixed Unicode credentials in successful JSON without changing clean values", async () => {
    const mixed = [...providerSecret].map((char, index) => index % 2 ? `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}` : char).join("");
    const clean = { text: 'A quoted "value", a newline\n, a literal \\u0074, and 🌱.', count: 2, enabled: true, nested: [null, "clean"] };
    const wire = `{"output":"${mixed}","clean":${JSON.stringify(clean)}}`;
    for (const contentType of ["application/json", "application/json; charset=utf-8", "application/result+json"]) {
      const { handle } = proxy({ fetch: (async () => new Response(new ReadableStream({ start(output) {
        const bytes = Buffer.from(wire);
        for (let at = 0; at < bytes.length; at += 3) output.enqueue(bytes.subarray(at, at + 3));
        output.close();
      } }), { headers: { "content-type": contentType } })) as typeof fetch });
      const response = await call(handle, handle.issue(authorization()).token);
      expect(response.status).toBe(200);
      const decoded = await response.json();
      expect(decoded.output).toBe("[REDACTED]");
      expect(decoded.clean).toEqual(clean);
      expect(JSON.stringify(decoded)).not.toContain(providerSecret);
    }
    const unchanged = JSON.stringify(clean);
    const { handle } = proxy({ fetch: (async () => new Response(unchanged, { headers: { "content-type": "application/json" } })) as typeof fetch });
    expect(await (await call(handle, handle.issue(authorization()).token)).text()).toBe(unchanged);
  });

  test("a diagnostic write failure never repeats an inference request", async () => {
    let requests = 0;
    let observations = 0;
    const { handle } = proxy({
      fetch: (async () => { requests++; return Response.json({ output: "one completion" }); }) as typeof fetch,
      observe: () => { observations++; throw new Error("The diagnostic store is unavailable."); },
    });
    expect(await (await call(handle, handle.issue(authorization()).token)).json()).toEqual({ output: "one completion" });
    expect(requests).toBe(1);
    expect(observations).toBe(1);
  });

  test("delivers an initial event before provider completion with exact event order", async () => {
    const finish = deferred();
    const first = "event: delta\ndata: {\"text\":\"hello\"}\n\n";
    const last = "event: completed\ndata: {\"usage\":{\"output_tokens\":1}}\n\n";
    const upstream = service(() => new Response(new ReadableStream({
      async start(output) {
        output.enqueue(new TextEncoder().encode(first));
        await finish.promise;
        try { output.enqueue(new TextEncoder().encode(last)); output.close(); } catch {}
      },
    }), { headers: { "content-type": "text/event-stream" } }));
    const { handle, observations } = proxy({ fetch: trustedTransport(upstream) });
    const { token } = handle.issue(authorization());
    const response = await call(handle, token);
    const reader = response.body!.getReader();
    const initial = await reader.read();
    expect(new TextDecoder().decode(initial.value)).toBe(first);
    expect(observations).toHaveLength(0);
    finish.resolve();
    let remainder = "";
    while (true) { const next = await reader.read(); if (next.done) break; remainder += new TextDecoder().decode(next.value); }
    expect(remainder).toBe(last);
    await waitFor(() => observations.length === 1);
    expect(observations[0]!.outcome).toBe("response_complete");
  });

  test("redacts split secrets without delaying an unrelated complete event", async () => {
    const next = deferred();
    const { handle } = proxy({ fetch: (async () => new Response(new ReadableStream({
      async start(output) {
        output.enqueue(new TextEncoder().encode("data: first\n\n"));
        await next.promise;
        for (const part of [providerSecret.slice(0, 15), providerSecret.slice(15), " ", renewalSecret, " end\n\n"]) {
          output.enqueue(new TextEncoder().encode(part));
        }
        output.close();
      },
    }))) as typeof fetch });
    const { token } = handle.issue(authorization());
    const response = await call(handle, token);
    const reader = response.body!.getReader();
    expect(new TextDecoder().decode((await reader.read()).value)).toBe("data: first\n\n");
    next.resolve();
    let body = "";
    while (true) { const chunk = await reader.read(); if (chunk.done) break; body += new TextDecoder().decode(chunk.value); }
    expect(body).toBe("[REDACTED] [REDACTED] end\n\n");
  });

  test("forwards provider errors and retry evidence without secret reflection", async () => {
    const { handle, observations } = proxy({ fetch: (async () => Response.json({
      error: { type: "rate_limit_error", message: `${providerSecret} ${renewalSecret}` },
    }, { status: 429, headers: { "retry-after": "3", "x-rate-limit-remaining": "0", "set-cookie": `key=${providerSecret}`, "x-culler-origin": "proxy_limit" } })) as typeof fetch });
    const { token } = handle.issue(authorization());
    const response = await call(handle, token);
    expect(response.status).toBe(429);
    expect(response.headers.get("x-culler-origin")).toBe("provider");
    expect(response.headers.get("retry-after")).toBe("3");
    expect(response.headers.get("x-rate-limit-remaining")).toBe("0");
    expect(response.headers.has("set-cookie")).toBe(false);
    expect(await response.json()).toEqual({ error: { type: "rate_limit_error", message: "[REDACTED] [REDACTED]" } });
    expect(observations[0]).toMatchObject({ origin: "provider", http_status: 429, provider_sent: true, provider_error_code: "rate_limit_error" });
    expect(observations[0]!.retry_after).toBeDefined();
    const evidence = JSON.stringify(observations);
    for (const secret of [token, providerSecret, renewalSecret]) expect(evidence).not.toContain(secret);
  });

  test("bounds an oversized error and preserves its provider status", async () => {
    const { handle } = proxy({ fetch: (async () => new Response("x".repeat(100_000) + providerSecret, { status: 503 })) as typeof fetch });
    const { token } = handle.issue(authorization());
    const response = await call(handle, token);
    expect(response.status).toBe(503);
    const body = await response.text();
    expect(body.length).toBeLessThan(200);
    expect(body).not.toContain(providerSecret);
  });

  test("never follows a provider redirect or repeats a provider request", async () => {
    let count = 0;
    const destination = service(() => { count++; return new Response(null, { status: 307, headers: { location: "https://attacker.invalid" } }); });
    const { handle, observations } = proxy({ fetch: trustedTransport(destination) });
    const { token } = handle.issue(authorization());
    const response = await call(handle, token);
    expect(response.status).toBe(502);
    expect(response.headers.has("location")).toBe(false);
    expect(count).toBe(1);
    expect(observations[0]).toMatchObject({ origin: "provider", provider_sent: true });
  });

  test("records the credential revision used before a replacement completes", async () => {
    const finish = deferred();
    let current = credential();
    const { handle, observations } = proxy({
      credential: async () => ({ ...current }),
      fetch: (async (_url: string, init: RequestInit) => {
        const auth = new Headers(init.headers).get("authorization");
        if (auth === `Bearer ${providerSecret}`) { await finish.promise; return Response.json({ error: { type: "authentication_error" } }, { status: 401 }); }
        return Response.json({ output: "ok" });
      }) as typeof fetch,
    });
    const { token } = handle.issue(authorization());
    const old = call(handle, token);
    await Bun.sleep(20);
    current = credential({ headers: { authorization: "Bearer replacement-provider-secret" }, authorizationRevision: "revision-2", credentialRef: "credential-2" });
    await (await call(handle, token)).text();
    finish.resolve();
    expect((await old).status).toBe(401);
    expect(observations.find(row => row.http_status === 401)).toMatchObject({ authorization_revision: "revision-1", credential_ref: "credential-1" });
    expect(observations.find(row => row.http_status === 200)).toMatchObject({ authorization_revision: "revision-2", credential_ref: "credential-2" });
  });

  test("keeps host credential failure distinct from provider rejection", async () => {
    for (const origin of ["credential_store", "provider_renewal"] as const) {
      const { handle, sent, observations } = proxy({ credential: async () => { throw new ProviderCredentialError(origin, "unavailable"); } });
      const { token } = handle.issue(authorization());
      const response = await call(handle, token);
      expect(response.status).toBe(503);
      expect(response.headers.get("x-culler-origin")).toBe(origin);
      expect(observations[0]).toMatchObject({ origin, provider_sent: false });
      expect(sent).toHaveLength(0);
    }
  });

  for (const action of ["revoke", "expiry", "cancel", "deadline", "stop"] as const) {
    test(`${action} cancels an active provider request and keeps the response incomplete`, async () => {
      const cancelled = deferred();
      const destination = service(request => {
        request.signal.addEventListener("abort", () => cancelled.resolve(), { once: true });
        return new Response(new ReadableStream({ start(output) { output.enqueue(new TextEncoder().encode("first\n")); } }));
      });
      const { handle, observations } = proxy({ fetch: trustedTransport(destination) });
      const spec = authorization();
      if (action === "expiry") spec.expiresAt = Date.now() + 100;
      if (action === "deadline") spec.limits.maxDurationMs = 100;
      const issued = handle.issue(spec);
      const signal = new AbortController();
      const response = await call(handle, issued.token, { signal: signal.signal });
      const reader = response.body!.getReader();
      expect(new TextDecoder().decode((await reader.read()).value)).toBe("first\n");
      const rest = reader.read().then(next => next.done ? "no_completion_event" : new TextDecoder().decode(next.value), () => "no_completion_event");
      if (action === "revoke") handle.revoke(issued.authorizationId);
      if (action === "cancel") signal.abort();
      if (action === "stop") handle.stop();
      expect(await rest).toBe("no_completion_event");
      await Promise.race([cancelled.promise, Bun.sleep(2000).then(() => { throw new Error("The provider request remained active."); })]);
      await waitFor(() => observations.length === 1);
      expect(observations[0]!.outcome).toBe("interrupted");
    });
  }

  test("a slow response leaves another authorization available", async () => {
    let calls = 0;
    const finish = deferred();
    const { handle } = proxy({ fetch: (async () => {
      calls++;
      if (calls > 1) return Response.json({ output: "independent" });
      return new Response(new ReadableStream({ async start(output) {
        output.enqueue(new TextEncoder().encode("slow\n")); await finish.promise;
        try { output.close(); } catch {}
      } }));
    }) as typeof fetch });
    const first = handle.issue(authorization());
    const second = handle.issue(authorization({ launchId: "launch-2" }));
    const slow = await call(handle, first.token);
    const fast = await call(handle, second.token);
    expect(await fast.json()).toEqual({ output: "independent" });
    finish.resolve();
    await slow.text();
  });
});

describe("provider proxy TLS", () => {
  test("rejects an unknown certificate authority before HTTP access", async () => {
    const { handle, sent } = proxy();
    const { token } = handle.issue(authorization());
    await expect(fetch(localURL(handle), { headers: { authorization: `Bearer ${token}` } })).rejects.toThrow();
    expect(sent).toHaveLength(0);
  });

  test("rejects a wrong host name and an expired server certificate", async () => {
    for (const cert of [certificates.mismatch, certificates.expired]) {
      const { handle, sent } = proxy({}, { official: route }, cert);
      const { token } = handle.issue(authorization());
      await expect(call(handle, token)).rejects.toThrow();
      expect(sent).toHaveLength(0);
    }
  });

  test("validates the upstream certificate independently", async () => {
    for (const cert of [certificates.mismatch, certificates.expired]) {
      let requests = 0;
      const destination = service(() => { requests++; return Response.json({ output: "unexpected" }); }, cert);
      const { handle, observations } = proxy({ fetch: trustedTransport(destination) });
      const { token } = handle.issue(authorization());
      const response = await call(handle, token);
      expect(response.status).toBe(502);
      expect(requests).toBe(0);
      expect(observations[0]).toMatchObject({ origin: "provider_network", outcome: "interrupted" });
    }
  });
});


test("a certificate renewal keeps the listener address, active stream, and launch authority", async () => {
  const finish = deferred();
  const f = proxy({ fetch: (async () => new Response(new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode("before"));
      void finish.promise.then(() => { controller.enqueue(new TextEncoder().encode("after")); controller.close(); });
    },
  }))) as typeof fetch });
  const issued = f.handle.issue(authorization());
  const response = await call(f.handle, issued.token);
  const reader = response.body!.getReader();
  expect(new TextDecoder().decode((await reader.read()).value)).toBe("before");
  const address = f.handle.url;
  f.handle.replaceTLS({ cert: certificates.renewed, key: certificates.key });
  expect(f.handle.url).toBe(address);
  finish.resolve();
  expect(new TextDecoder().decode((await reader.read()).value)).toBe("after");
  expect((await reader.read()).done).toBe(true);
  const next = await call(f.handle, issued.token, { keepalive: false });
  expect(next.status).toBe(200);
  expect(await next.text()).toBe("beforeafter");
  expect(f.handle.status(issued.authorizationId)?.requests).toBe(2);
});

test("an invalid replacement certificate preserves the current listener", async () => {
  const f = proxy();
  const issued = f.handle.issue(authorization());
  expect(() => f.handle.replaceTLS({ cert: certificates.renewed, key: "invalid key" })).toThrow();
  const response = await call(f.handle, issued.token);
  expect(response.status).toBe(200);
  await response.text();
});

describe("OpenCode provider routes", () => {
  for (const placement of ["bearer", "x-api-key", "x-goog-api-key"] as const) {
    test(`the ${placement} route permits same-host JSON endpoints and replaces only its key header`, async () => {
      const selected: ProviderRoute = { baseURL: "https://provider.invalid", openCode: { keyPlacement: placement, modelLocation: "body" }, operations: [{ id: "model", method: "POST", path: "/" }] };
      const { handle, sent } = proxy({}, { official: selected });
      const grant = handle.issue(authorization({ operations: ["model"] }));
      const headers = { [placement === "bearer" ? "authorization" : placement]: placement === "bearer" ? `Bearer ${grant.token}` : grant.token,
        "content-type": "application/json", "x-provider-option": "on", "organization": "account-choice", "cookie": "private", "proxy-extra": "private" };
      for (const path of ["/v1/chat/completions", "/tools/search", "/outside/base/path"]) {
        const response = await call(handle, grant.token, { headers, body: JSON.stringify({ model: "small-model" }) }, path);
        expect(response.status).toBe(200);
        expect(sent.at(-1)!.url).toBe(`https://provider.invalid${path}`);
      }
      expect((await call(handle, grant.token, { headers, method: "GET", body: undefined }, "/anything")).status).toBe(200);
      const forwarded = new Headers(sent[0]!.init.headers);
      expect(forwarded.get(placement === "bearer" ? "authorization" : placement)).toBe(placement === "bearer" ? `Bearer ${providerSecret}` : providerSecret);
      expect(forwarded.get("x-provider-option")).toBe("on");
      expect(forwarded.get("organization")).toBe("account-choice");
      expect(forwarded.has("cookie")).toBe(false);
      expect(forwarded.has("proxy-extra")).toBe(false);
      const count = sent.length;
      for (const wrong of ["authorization", "x-api-key", "x-goog-api-key"].filter(name => name !== (placement === "bearer" ? "authorization" : placement))) {
        expect((await call(handle, grant.token, { headers: { [wrong]: wrong === "authorization" ? `Bearer ${grant.token}` : grant.token, "content-type": "application/json" } })).status).toBe(401);
      }
      expect((await call(handle, grant.token, { headers, body: JSON.stringify({ model: "another-model" }) })).status).toBe(403);
      expect((await call(handle, grant.token, { headers, body: "[]" })).status).toBe(400);
      expect((await call(handle, grant.token, { headers, method: "DELETE", body: undefined })).status).toBe(403);
      expect(sent).toHaveLength(count);
    });
  }
  test("a model in the path must match the profile model", async () => {
    const selected: ProviderRoute = { baseURL: "https://provider.invalid", openCode: { keyPlacement: "x-goog-api-key", modelLocation: "path" }, operations: [{ id: "model", method: "POST", path: "/" }] };
    const { handle, sent } = proxy({}, { official: selected });
    const { token } = handle.issue(authorization({ operations: ["model"] }));
    const headers = { "x-goog-api-key": token, "content-type": "application/json" };
    expect((await call(handle, token, { headers, body: "{}" }, "/v1beta/models/small-model:streamGenerateContent?alt=sse")).status).toBe(200);
    expect((await call(handle, token, { headers, body: "{}" }, "/v1beta/models/other:streamGenerateContent")).status).toBe(403);
    expect((await call(handle, token, { headers, body: "{}" }, "/v1beta/models/small-model:streamGenerateContent?key=leak")).status).toBe(403);
    expect(sent).toHaveLength(1);
  });
});

test("each warm-session request retains the current reply attempt", async () => {
  let attempt = "a".repeat(32);
  const { handle, observations } = proxy({
    isLaunchCurrent: async () => ({ allowed: true, attemptId: attempt }),
    fetch: (async () => Response.json({ error: { type: "invalid_request_error", code: "invalid_encrypted_content" } }, { status: 400 })) as typeof fetch,
  });
  const { token } = handle.issue(authorization({ requireLiveness: true, attemptId: undefined }));
  for (const next of ["a".repeat(32), "b".repeat(32)]) {
    attempt = next;
    const response = await call(handle, token);
    await response.text();
    expect(response.status).toBe(400);
    expect(observations.at(-1)).toMatchObject({ attempt_id: next, provider_error_code: "invalid_encrypted_content" });
  }
});

test("a reply change during credential renewal refuses the earlier request", async () => {
  let checks = 0;
  const { handle, sent, observations } = proxy({ isLaunchCurrent: async () => ({ allowed: true, attemptId: (++checks === 1 ? "a" : "b").repeat(32) }) });
  const { token } = handle.issue(authorization({ requireLiveness: true, attemptId: undefined }));
  const response = await call(handle, token);
  await response.text();
  expect(response.status).toBe(409);
  expect(sent).toHaveLength(0);
  expect(observations.at(-1)).toMatchObject({ attempt_id: "a".repeat(32), origin: "proxy_authorization" });
});
