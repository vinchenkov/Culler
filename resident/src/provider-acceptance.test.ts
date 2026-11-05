import { expect, test } from "bun:test";
import { handleProviderAcceptanceRequest, type ProviderAcceptanceOps } from "./provider-acceptance";

const release = "a".repeat(40);
const nonce = "b".repeat(32);
const config = { version: 1, url: "https://host.docker.internal:1234", token: "culler_proxy_" + "c".repeat(43),
  ca: "-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----", harness: "codex", authMode: "api-key" };
const authorize = () => ({ version: 1, op: "profile_authorize", credential: "test-connection", profile: "test-profile",
  profile_revision: "revision-1", run_id: "run-1", launch_id: "launch-1", model: "test-model", nonce,
  expires_at_ms: Date.now() + 60_000, max_requests: 2 });
function fixture() {
  const calls: unknown[] = [];
  const ops: ProviderAcceptanceOps = {
    authorize: async request => { calls.push(["authorize", request]); return config; },
    revoke: (run, launch) => { calls.push(["revoke", run, launch]); },
    revokeConnection: (connection, revision) => { calls.push(["revokeConnection", connection, revision]); },
    status: (run, launch) => { calls.push(["status", run, launch]); return { providerRequests: 2 }; },
    check: async connection => { calls.push(["check", connection]); },
  };
  return { calls, ops, request: async (body: unknown) => JSON.parse(await handleProviderAcceptanceRequest(JSON.stringify(body), release, ops)) };
}

test("the acceptance socket returns only scoped Culler access", async () => {
  const f = fixture();
  const request = authorize();
  expect(await f.request(request)).toEqual({ version: 1, status: "ok", credential: request.credential, nonce, proxy_config: config });
  expect(f.calls).toEqual([["authorize", request]]);
});

test("invalid authority and budgets stop before authorization", async () => {
  const f = fixture();
  for (const patch of [
    { version: 2 }, { nonce: "short" }, { credential: "../private" }, { profile: "" }, { model: "" },
    { run_id: "../run" }, { launch_id: "" }, { expires_at_ms: Date.now() - 1 },
    { expires_at_ms: Date.now() + 31 * 60_000 }, { max_requests: 0 }, { max_requests: 101 },
    { token_url: "https://attacker.invalid" }, { purpose: "unlimited" }, { profile_revision: "../revision" },
  ]) await expect(f.request({ ...authorize(), ...patch })).rejects.toThrow();
  expect(f.calls).toEqual([]);
});

test("model discovery receives a distinct purpose without a model", async () => {
  const f = fixture();
  const request = { ...authorize(), model: "", purpose: "model_list" };
  await f.request(request);
  expect(f.calls).toEqual([["authorize", request]]);
});

test("a malformed access response revokes the newly issued authority", async () => {
  const f = fixture();
  f.ops.authorize = async () => ({ ...config, access_token: "private-provider-token" });
  await expect(f.request(authorize())).rejects.toThrow("The provider access configuration is invalid.");
  expect(f.calls).toEqual([["revoke", "run-1", "launch-1"]]);
});

test("revocation and status use the exact run and launch", async () => {
  const f = fixture();
  const identity = { version: 1, run_id: "run-1", launch_id: "launch-1", nonce };
  expect(await f.request({ ...identity, op: "profile_status" })).toEqual({ version: 1, status: "ok", nonce, provider_requests: 2 });
  expect(await f.request({ ...identity, op: "profile_revoke" })).toEqual({ version: 1, status: "ok", nonce, revoked: true });
  expect(f.calls).toEqual([["status", "run-1", "launch-1"], ["revoke", "run-1", "launch-1"]]);
  f.ops.status = () => null;
  await expect(f.request({ ...identity, op: "profile_status" })).rejects.toThrow("evidence is unavailable");
});

test("the connection check exposes no credential and accepts no destination", async () => {
  const f = fixture();
  const request = { version: 1, op: "connection_check", credential: "test-connection", nonce };
  expect(await f.request(request)).toEqual({ version: 1, status: "ok", nonce });
  expect(f.calls).toEqual([["check", "test-connection"]]);
  await expect(f.request({ ...request, url: "https://attacker.invalid" })).rejects.toThrow();
});

test("legacy credential operations and excess input fail closed", async () => {
  const f = fixture();
  for (const op of ["mint", "profile_mint", "codex_arm", "codex_status"]) {
    await expect(f.request({ ...authorize(), op })).rejects.toThrow();
  }
  await expect(handleProviderAcceptanceRequest("x".repeat(8193), release, f.ops)).rejects.toThrow("too large");
  expect(f.calls).toEqual([]);
});


test("host connection revocation accepts one connection and returns no credentials", async () => {
  const f = fixture();
  const request = { version: 1, op: "connection_revoke", credential: "test-connection", authorization_id: "current-revision", nonce };
  expect(await f.request(request)).toEqual({ version: 1, status: "ok", nonce, revoked: true });
  expect(f.calls).toEqual([["revokeConnection", "test-connection", "current-revision"]]);
  await expect(f.request({ ...request, credential: "../private" })).rejects.toThrow();
  await expect(f.request({ ...request, token: "untrusted" })).rejects.toThrow();
});
