import { expect, test } from "bun:test";
import { createProviderCredentials, type HostCredentialSnapshot } from "./provider-credentials";

function fixture(type: "api_key" | "account" = "account", observe?: (value: Record<string, unknown>) => Promise<void>) {
  let stored: HostCredentialSnapshot = {
    record: type === "api_key" ? { version: 1, type, credential: "one", channel: "codex", api_key: "synthetic-key-one" } : {
      version: 1, type, credential: "one", channel: "codex", access_token: "synthetic-access-one",
      refresh_token: "synthetic-refresh-one", id_token: "synthetic-identity-one", account_id: "account-one",
      token_url: "https://provider.example/token", client_id: "native-client", expires_at_ms: 1,
    }, authorization_id: "authorization-one", credential_ref: "credential-one",
  };
  let fetches = 0;
  let saves = 0;
  let saveFails = false;
  let readsFail = false;
  const requests: unknown[] = [];
  const now = 1_800_000_000_000;
  const service = createProviderCredentials({ now: () => now, observe,
    runMc: async (argv, stdin) => {
      if (argv[1] === "credential-read") return { exitCode: readsFail ? 1 : 0, stdout: JSON.stringify(stored), stderr: "" };
      if (argv[1] !== "credential-renew") throw new Error("Unexpected host command.");
      const request = JSON.parse(new TextDecoder().decode(stdin));
      expect(request.authorization_id).toBe(stored.authorization_id);
      expect(request.credential_ref).toBe(stored.credential_ref);
      saves++;
      if (saveFails) return { exitCode: 1, stdout: "", stderr: "private failure" };
      stored = { record: request.record, authorization_id: stored.authorization_id, credential_ref: "credential-two" };
      return { exitCode: 0, stdout: '{"updated":true}', stderr: "" };
    },
    fetch: (async (url, options) => {
      fetches++;
      expect(url).toBe("https://provider.example/token");
      expect(options?.redirect).toBe("error");
      requests.push(JSON.parse(String(options?.body)));
      return Response.json({ access_token: "synthetic-access-two", refresh_token: "synthetic-refresh-two",
        id_token: "synthetic-identity-two", expires_in: 3600 });
    }) as typeof fetch,
  });
  return { service, now, requests, get stored() { return stored; }, set stored(value) { stored = value; },
    get fetches() { return fetches; }, get saves() { return saves; },
    set saveFails(value: boolean) { saveFails = value; }, set readsFail(value: boolean) { readsFail = value; } };
}

test("an API key remains on the host and performs no token renewal", async () => {
  const f = fixture("api_key");
  const result = await f.service.credential("one", new AbortController().signal);
  expect(result.headers.authorization).toBe("Bearer synthetic-key-one");
  expect(result.authorizationRevision).toBe("authorization-one");
  expect(result.credentialRef).toBe("credential-one");
  expect(f.fetches).toBe(0);
  expect(f.saves).toBe(0);
});

test("a diagnostic transport failure keeps its proxy origin after a successful credential read", async () => {
  const observations: Record<string, unknown>[] = [];
  const f = fixture("api_key", async value => { observations.push(value); throw new Error("synthetic transport failure"); });
  await expect(f.service.credential("one", new AbortController().signal)).rejects.toMatchObject({ origin: "proxy", code: "diagnostic_unavailable" });
  expect(observations).toHaveLength(1);
  expect(observations[0]).toMatchObject({ origin: "credential_store", outcome: "ready" });
  expect(f.fetches).toBe(0);
  expect(f.saves).toBe(0);
});

test("a failed renewal diagnostic preserves the durable rotation without a second provider request", async () => {
  let fail = true;
  const observations: Record<string, unknown>[] = [];
  const f = fixture("account", async value => {
    observations.push(value);
    if (fail && value.origin === "provider_renewal" && value.outcome === "ready") throw new Error("synthetic transport failure");
  });
  await expect(f.service.renew("one")).rejects.toMatchObject({ origin: "proxy", code: "diagnostic_unavailable" });
  expect(f.stored.record.refresh_token).toBe("synthetic-refresh-two");
  expect(f.saves).toBe(1);
  expect(observations.some(value => value.origin === "credential_store" && value.outcome === "failed")).toBe(false);
  fail = false;
  expect((await f.service.renew("one")).credential_ref).toBe("credential-two");
  expect(f.fetches).toBe(1);
  expect(f.saves).toBe(1);
});

test("concurrent requests share one durable renewal and preserve the authorization revision", async () => {
  const f = fixture();
  const results = await Promise.all(Array.from({ length: 8 }, () => f.service.credential("one", new AbortController().signal)));
  expect(f.fetches).toBe(1);
  expect(f.saves).toBe(1);
  expect(f.stored.record.refresh_token).toBe("synthetic-refresh-two");
  expect(f.stored.record.id_token).toBe("synthetic-identity-two");
  expect(f.stored.record.expires_at_ms).toBe(f.now + 3_600_000);
  expect(results.every(result => result.headers.authorization === "Bearer synthetic-access-two" && result.authorizationRevision === "authorization-one")).toBe(true);
  expect(f.requests).toEqual([{ grant_type: "refresh_token", client_id: "native-client", refresh_token: "synthetic-refresh-one" }]);
});

test("a failed durable save never supplies the new access token", async () => {
  const f = fixture();
  f.saveFails = true;
  await expect(f.service.credential("one", new AbortController().signal)).rejects.toMatchObject({ origin: "credential_store", code: "credential_save" });
  expect(f.stored.credential_ref).toBe("credential-one");
  f.saveFails = false;
  const recovered = await f.service.credential("one", new AbortController().signal);
  expect(recovered.headers.authorization).toBe("Bearer synthetic-access-two");
  expect(f.stored.record.refresh_token).toBe("synthetic-refresh-two");
  expect(f.fetches).toBe(1);
});

test("a locked credential store performs no provider request", async () => {
  const f = fixture();
  f.readsFail = true;
  await expect(f.service.credential("one", new AbortController().signal)).rejects.toMatchObject({ origin: "credential_store" });
  expect(f.fetches).toBe(0);
});

test("a delayed rejected token leaves a renewed session usable", async () => {
  const f = fixture();
  await f.service.renew("one");
  f.service.reject("credential-one");
  const result = await f.service.credential("one", new AbortController().signal);
  expect(result.credentialRef).toBe("credential-two");
  expect(f.fetches).toBe(1);
});

test("operator replacement supplies the next request without a stale token cache", async () => {
  const f = fixture("api_key");
  await f.service.credential("one", new AbortController().signal);
  f.stored = { ...f.stored, authorization_id: "authorization-two", credential_ref: "credential-two",
    record: { ...f.stored.record, api_key: "replacement-key" } };
  const result = await f.service.credential("one", new AbortController().signal);
  expect(result.headers.authorization).toBe("Bearer replacement-key");
  expect(result.authorizationRevision).toBe("authorization-two");
  expect(f.fetches).toBe(0);
});


test("the native API key header comes from the selected protocol", async () => {
  const f = fixture("api_key");
  f.stored = { ...f.stored, record: { ...f.stored.record, channel: "claude" } };
  const credential = await f.service.credential("one", new AbortController().signal);
  expect(credential.headers).toEqual({ "x-api-key": "synthetic-key-one" });
  expect(f.fetches).toBe(0);
});

function renewalScenario(answer: (attempt: number, signal: AbortSignal) => Promise<Response>, options: {
  pause?: (delay: number, signal: AbortSignal) => Promise<void>;
  owner?: () => Promise<() => Promise<void>>;
} = {}) {
  let time = 1_800_000_000_000;
  let stored: HostCredentialSnapshot = { record: { version: 1, type: "account", credential: "one", channel: "codex",
    token_url: "https://provider.example/token", client_id: "native-client", refresh_token: "old-refresh", access_token: "old-access", expires_at_ms: 1 },
    authorization_id: "authorization", credential_ref: "reference-one" };
  const observations: Record<string, unknown>[] = [];
  const delays: number[] = [];
  let fetches = 0;
  let saves = 0;
  let releases = 0;
  let saveFails = false;
  const service = createProviderCredentials({
    now: () => time, observe: async value => { observations.push(value); },
    acquireOwner: options.owner ?? (async () => async () => { releases++; }),
    sleep: async (delay, signal) => { delays.push(delay); if (options.pause) await options.pause(delay, signal); time += delay; },
    runMc: async (args, input) => {
      if (args[1] === "credential-read") return { exitCode: 0, stdout: JSON.stringify(stored), stderr: "" };
      if (args[1] !== "credential-renew") throw new Error("Unexpected host command.");
      saves++;
      if (saveFails) return { exitCode: 1, stdout: "", stderr: "private store failure" };
      const renewal = JSON.parse(new TextDecoder().decode(input));
      expect(renewal.authorization_id).toBe(stored.authorization_id);
      expect(renewal.credential_ref).toBe(stored.credential_ref);
      stored = { record: renewal.record, authorization_id: stored.authorization_id, credential_ref: `reference-${saves + 1}` };
      return { exitCode: 0, stdout: '{"updated":true}', stderr: "" };
    },
    fetch: ((_url, init) => answer(++fetches, init!.signal!)) as typeof fetch,
  });
  return { service, observations, delays, get now() { return time; }, get stored() { return stored; }, set stored(value) { stored = value; },
    get fetches() { return fetches; }, get saves() { return saves; }, get releases() { return releases; }, set saveFails(value: boolean) { saveFails = value; } };
}

const renewedResponse = () => Response.json({ access_token: "new-access", refresh_token: "new-refresh", expires_in: 3600,
  refresh_token_expires_in: 7200, scope: "" });

test("temporary renewal responses respect retry information and keep a finite attempt cap", async () => {
  const f = renewalScenario(async attempt => attempt === 1
    ? Response.json({ error: "rate_limit_exceeded" }, { status: 429, headers: { "retry-after": "1" } })
    : attempt === 2 ? new Response("temporarily unavailable", { status: 503 }) : renewedResponse());
  const credential = await f.service.credential("one", new AbortController().signal);
  expect(credential.headers.authorization).toBe("Bearer new-access");
  expect(f.delays).toEqual([1000, 500]);
  expect(f.fetches).toBe(3); expect(f.saves).toBe(1); expect(f.releases).toBe(1);
  expect(f.stored.record.scope).toBe("");
  expect(f.stored.record.refresh_expires_at_ms).toBe(f.now + 7_200_000);
  const renewal = f.observations.filter(value => value.origin === "provider_renewal");
  expect(renewal.map(value => value.outcome)).toEqual(["failed", "failed", "ready"]);
  expect(new Set(renewal.map(value => value.request_id)).size).toBe(3);
  expect(renewal[0]!.retry_after).toBeDefined();
  expect(JSON.stringify(f.observations)).not.toContain("new-refresh");
});

test("persistent renewal refusal stops after three attempts without changing the saved token", async () => {
  const f = renewalScenario(async () => Response.json({ error: "server_error" }, { status: 503 }));
  await expect(f.service.renew("one")).rejects.toMatchObject({ origin: "provider_renewal", code: "server_error" });
  expect(f.fetches).toBe(3); expect(f.saves).toBe(0); expect(f.delays).toEqual([250, 500]);
  expect(f.stored.record.refresh_token).toBe("old-refresh");
});

test("a permanent rejection, uncertain transport, and excessive retry delay never repeat renewal", async () => {
  for (const answer of [
    async () => Response.json({ error: "invalid_grant" }, { status: 400 }),
    async () => { throw new Error("private provider response"); },
    async () => Response.json({ error: "rate_limit_exceeded" }, { status: 429, headers: { "retry-after": "120" } }),
  ]) {
    const f = renewalScenario(answer);
    await expect(f.service.renew("one")).rejects.toBeDefined();
    expect(f.fetches).toBe(1); expect(f.saves).toBe(0); expect(f.delays).toHaveLength(0);
    expect(JSON.stringify(f.observations)).not.toContain("private provider response");
  }
});

test("one canceled caller preserves a shared renewal for the other caller", async () => {
  const response = Promise.withResolvers<Response>();
  const started = Promise.withResolvers<void>();
  const f = renewalScenario(async () => { started.resolve(); return response.promise; });
  const first = new AbortController();
  const one = f.service.credential("one", first.signal);
  const two = f.service.credential("one", new AbortController().signal);
  await started.promise;
  first.abort("request-ended");
  await expect(one).rejects.toBe("request-ended");
  response.resolve(renewedResponse());
  expect((await two).headers.authorization).toBe("Bearer new-access");
  expect(f.fetches).toBe(1); expect(f.saves).toBe(1); expect(f.releases).toBe(1);
});

test("a received rotation becomes durable after all requesting runs end", async () => {
  const response = Promise.withResolvers<Response>();
  const started = Promise.withResolvers<void>();
  const f = renewalScenario(async () => { started.resolve(); return response.promise; });
  const request = new AbortController();
  const credential = f.service.credential("one", request.signal);
  await started.promise;
  request.abort("run-ended");
  await expect(credential).rejects.toBe("run-ended");
  response.resolve(renewedResponse());
  const saved = await f.service.renew("one");
  expect(saved.record.refresh_token).toBe("new-refresh");
  expect(f.fetches).toBe(1); expect(f.saves).toBe(1);
});

test("cancellation during retry delay prevents another renewal request", async () => {
  const paused = Promise.withResolvers<void>();
  const f = renewalScenario(async () => Response.json({ error: "server_error" }, { status: 503 }), {
    pause: (_delay, signal) => new Promise((_resolve, reject) => { paused.resolve(); signal.addEventListener("abort", () => reject(signal.reason), { once: true }); }),
  });
  const request = new AbortController();
  const credential = f.service.credential("one", request.signal);
  await paused.promise;
  request.abort("run-ended");
  await expect(credential).rejects.toBe("run-ended");
  await new Promise(resolve => setTimeout(resolve, 0));
  expect(f.fetches).toBe(1); expect(f.saves).toBe(0); expect(f.releases).toBe(1);
});

test("cancellation before connection ownership prevents credential and provider access", async () => {
  const owner = Promise.withResolvers<() => Promise<void>>();
  let released = false;
  const f = renewalScenario(async () => renewedResponse(), { owner: () => owner.promise });
  const request = new AbortController();
  const credential = f.service.credential("one", request.signal);
  request.abort("run-ended");
  await expect(credential).rejects.toBe("run-ended");
  owner.resolve(async () => { released = true; });
  await new Promise(resolve => setTimeout(resolve, 0));
  expect(f.fetches).toBe(0); expect(f.observations).toHaveLength(0); expect(released).toBe(true);
});

test("saved access expiry and refresh expiry control renewal without sending an expired refresh token", async () => {
  const f = renewalScenario(async () => renewedResponse());
  f.stored = { ...f.stored, record: { ...f.stored.record, expires_at_ms: f.now + 120_001 } };
  expect((await f.service.credential("one", new AbortController().signal)).headers.authorization).toBe("Bearer old-access");
  expect(f.fetches).toBe(0);
  f.stored = { ...f.stored, record: { ...f.stored.record, expires_at_ms: f.now, refresh_expires_at_ms: f.now - 1 } };
  await expect(f.service.renew("one")).rejects.toMatchObject({ origin: "provider_renewal", code: "invalid_grant" });
  expect(f.fetches).toBe(0);
  expect(f.observations.at(-1)).toMatchObject({ origin: "provider_renewal", provider_sent: false, provider_error_code: "invalid_grant" });
});

test("an invalid or oversized successful renewal response never replaces the saved credential", async () => {
  for (const response of [Response.json({ access_token: "new-access", expires_in: -1 }), Response.json({ access_token: "x".repeat(70_000), expires_in: 3600 })]) {
    const f = renewalScenario(async () => response);
    await expect(f.service.renew("one")).rejects.toMatchObject({ origin: "provider_renewal", code: "invalid_response" });
    expect(f.fetches).toBe(1); expect(f.saves).toBe(0);
    expect(f.stored.record.access_token).toBe("old-access");
  }
});

test("a failed save preserves the received rotation and never overwrites operator replacement", async () => {
  const f = renewalScenario(async () => renewedResponse());
  f.saveFails = true;
  await expect(f.service.renew("one")).rejects.toMatchObject({ origin: "credential_store", code: "credential_save" });
  expect(f.observations.at(-1)).toMatchObject({ origin: "credential_store", outcome: "failed" });
  f.saveFails = false;
  f.stored = { authorization_id: "replacement", credential_ref: "replacement-ref", record: { version: 1, type: "api_key", credential: "one", channel: "codex", api_key: "replacement-key" } };
  expect((await f.service.credential("one", new AbortController().signal)).headers.authorization).toBe("Bearer replacement-key");
  expect(f.fetches).toBe(1); expect(f.saves).toBe(1);
});

test("a malformed expiry preserves a replacement refresh token without supplying access", async () => {
  const f = renewalScenario(async () => Response.json({ access_token: "new-access", refresh_token: "only-valid-refresh", expires_in: -1 }));
  await expect(f.service.renew("one")).rejects.toMatchObject({ origin: "provider_renewal", code: "invalid_response" });
  expect(f.fetches).toBe(1); expect(f.saves).toBe(1);
  expect(f.stored.record.refresh_token).toBe("only-valid-refresh");
  expect(f.stored.record.expires_at_ms).toBe(0);
  expect(f.observations.at(-1)).toMatchObject({ origin: "provider_renewal", outcome: "failed", credential_ref: f.stored.credential_ref });
});

test("a token claim supplies expiry when the successful reply omits it", async () => {
  const expires = 1_800_003_600;
  const token = `header.${Buffer.from(JSON.stringify({ exp: expires })).toString("base64url")}.signature`;
  const f = renewalScenario(async () => Response.json({ access_token: token, refresh_token: "new-refresh" }));
  expect((await f.service.renew("one")).record.expires_at_ms).toBe(expires * 1000);
  expect(f.saves).toBe(1);
});

test("shutdown rejects new renewal and waits for a received rotation to become durable", async () => {
  const response = Promise.withResolvers<Response>();
  const started = Promise.withResolvers<void>();
  const f = renewalScenario(async () => { started.resolve(); return response.promise; });
  const renewal = f.service.renew("one");
  await started.promise;
  let completed = false;
  const drain = f.service.drain().then(() => { completed = true; });
  await expect(f.service.renew("one")).rejects.toMatchObject({ code: "host_stopping" });
  await new Promise(resolve => setTimeout(resolve, 0));
  expect(completed).toBe(false);
  response.resolve(renewedResponse());
  await renewal;
  await drain;
  expect(f.stored.record.refresh_token).toBe("new-refresh");
  expect(f.saves).toBe(1); expect(f.releases).toBe(1);
});

test("the shutdown wait has a finite bound", async () => {
  const response = Promise.withResolvers<Response>();
  const started = Promise.withResolvers<void>();
  const f = renewalScenario(async () => { started.resolve(); return response.promise; });
  const renewal = f.service.renew("one");
  await started.promise;
  await expect(f.service.drain(5)).rejects.toMatchObject({ origin: "credential_store", code: "credential_save" });
  expect(f.saves).toBe(0);
  response.resolve(renewedResponse());
  await renewal;
  expect(f.stored.record.refresh_token).toBe("new-refresh");
});

test("shutdown retries a pending rotated save and reports persistent store failure", async () => {
  for (const restored of [false, true]) {
    const f = renewalScenario(async () => renewedResponse());
    f.saveFails = true;
    await expect(f.service.renew("one")).rejects.toMatchObject({ code: "credential_save" });
    f.saveFails = !restored;
    if (restored) {
      await f.service.drain();
      expect(f.stored.record.refresh_token).toBe("new-refresh");
      expect(f.saves).toBe(2);
    } else {
      await expect(f.service.drain()).rejects.toMatchObject({ origin: "credential_store", code: "credential_save" });
      expect(f.saves).toBe(4);
      expect(f.observations.at(-1)).toMatchObject({ origin: "credential_store", outcome: "failed" });
    }
    expect(f.fetches).toBe(1);
  }
});

test("malformed credentials and failed owner access record a host failure without provider traffic", async () => {
  for (const scenario of ["json", "shape", "missing token", "owner", "authority"]) {
    const observations: Record<string, unknown>[] = [];
    let fetches = 0;
    const record = scenario === "missing token" ? { version: 1, credential: "one", channel: "codex", type: "api_key" }
      : { version: 1, credential: "one", channel: "codex", type: "account", token_url: "http://private.invalid", client_id: "client", refresh_token: "private-refresh" };
    const service = createProviderCredentials({
      observe: async value => { observations.push(value); },
      acquireOwner: async () => { if (scenario === "owner") throw new Error("private owner error"); return async () => {}; },
      runMc: async () => ({ exitCode: 0, stdout: scenario === "json" ? "private invalid json" : scenario === "shape" ? "{}" : JSON.stringify({ record, authorization_id: "auth", credential_ref: "ref" }), stderr: "" }),
      fetch: (async () => { fetches++; return renewedResponse(); }) as typeof fetch,
    });
    await expect(service.credential("one", new AbortController().signal)).rejects.toMatchObject({ origin: "credential_store" });
    expect(fetches).toBe(0);
    expect(observations.at(-1)).toMatchObject({ origin: "credential_store", outcome: "failed" });
    expect(JSON.stringify(observations)).not.toContain("private");
  }
});
