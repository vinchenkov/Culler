import { expect, test } from "bun:test";
import { createAgentProfileRuntimeRefresh, readAgentProfileRuntimeCatalog } from "./agent-profile-runtime";

const bindings = {
  local: { harness: "claude-sdk", credential: "local-account", channel: "claude", kind: "claude-oauth",
    inference_url: "https://provider.invalid", token_url: "https://provider.invalid/token", client_id: "fixture-client" },
};
const ok = (value: unknown) => ({ exitCode: 0, stdout: JSON.stringify(value), stderr: "" });

test("the host supplies profiles and connections without credentials", async () => {
  const calls: string[][] = [];
  const catalog = await readAgentProfileRuntimeCatalog(async argv => {
    calls.push(argv);
    return ok({ bindings, connections: { "local-account": bindings.local } });
  });
  expect(calls).toEqual([["agent-profile", "runtime-catalog"]]);
  expect(catalog.local).toEqual(bindings.local);
  expect(catalog["connection:local-account"]).toEqual(bindings.local);
});

test("a test build applies one renewal authority to all profiles of its connection", async () => {
  const calls: string[][] = [];
  const authority = { token_url: "http://127.0.0.1:1234/token", client_id: "test-client" };
  const catalog = await readAgentProfileRuntimeCatalog(async argv => {
    calls.push(argv);
    return ok(argv[1] === "test-runtime-authorities" ? { enabled: true } : { bindings,
      connections: { "local-account": bindings.local } });
  }, { local: authority });
  expect(calls).toEqual([["agent-profile", "test-runtime-authorities"], ["agent-profile", "runtime-catalog"]]);
  expect(catalog.local).toMatchObject(authority);
  expect(catalog["connection:local-account"]).toMatchObject(authority);
});

test.each([
  { exitCode: 0, stdout: '{"enabled":false}', stderr: "" },
  { exitCode: 1, stdout: '{"enabled":true}', stderr: "private-value" },
  { exitCode: 0, stdout: "private-value", stderr: "" },
  { exitCode: 0, stdout: '{"enabled":"true"}', stderr: "" },
])("a rejected authority gate stops before catalog access", async gate => {
  const calls: string[][] = [];
  const refresh = createAgentProfileRuntimeRefresh({ reload: async () => { throw new Error("Unexpected reload"); } } as never, {
    runMc: async argv => { calls.push(argv); return gate; },
    authorities: { local: { token_url: "http://127.0.0.1:1234/token", client_id: "fixture-client" } },
  });
  await expect(refresh()).rejects.toThrow("Runtime authority overrides require a test build.");
  expect(calls).toEqual([["agent-profile", "test-runtime-authorities"]]);
});

test("concurrent refreshes share one catalog read and the next refresh reads again", async () => {
  let calls = 0;
  const catalogs: unknown[] = [];
  const refresh = createAgentProfileRuntimeRefresh({ reload: async catalog => { catalogs.push(catalog); } } as never, {
    runMc: async () => { calls++; return ok({ bindings }); },
  });
  await Promise.all([refresh(), refresh(), refresh()]);
  expect(calls).toBe(1);
  expect(catalogs).toEqual([bindings]);
  await refresh();
  expect(calls).toBe(2);
});

test("a failed refresh releases its pending request and hides host output", async () => {
  let fail = true;
  let reloads = 0;
  const refresh = createAgentProfileRuntimeRefresh({ reload: async () => { reloads++; } } as never, {
    runMc: async () => fail ? { exitCode: 1, stdout: "private-secret", stderr: "private-secret" } : ok({ bindings }),
  });
  await expect(refresh()).rejects.toThrow("The agent profile configuration is unavailable.");
  expect(reloads).toBe(0);
  fail = false;
  await refresh();
  expect(reloads).toBe(1);
});

test("a connection retains its identity and rejects secrets in the catalog", async () => {
  for (const body of [{ connections: { wrong: bindings.local }, bindings: {} },
    { bindings: { local: { ...bindings.local, api_key: "private-secret" } } },
    { bindings: { local: { ...bindings.local, inference_url: "https://user:secret@provider.invalid" } } }]) {
    await expect(readAgentProfileRuntimeCatalog(async () => ok(body))).rejects.toThrow();
  }
});
