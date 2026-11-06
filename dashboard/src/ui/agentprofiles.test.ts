import { expect, test } from "bun:test";
import { effortChoices, connectionChoices, currentProfileStatus, elapsedLabel, listedProfiles, listsModels, modelChoices, needsAuthorization, operationSummary, profilePayload, profileStatus } from "./agentprofiles.js";

const ROLES = ["strategist", "arbiter", "worker", "verifier", "scribe", "packager", "collie"];

test("the panel lists only the enabled agent profiles of the catalog", () => {
  const profiles = [
    { id: "chatgpt", model: "codex-model", connection_id: "chatgpt", enabled: true, roles: ROLES },
    { id: "claude", model: "claude-opus-4-8", connection_id: "claude", enabled: false, roles: [] },
    { id: "endpoint", model: "endpoint-model", connection_id: "endpoint", base_url: "https://endpoint.example/anthropic", enabled: false, roles: [] },
  ];
  expect(listedProfiles(profiles).map((profile) => profile.id)).toEqual(["chatgpt"]);
  expect(listedProfiles(undefined)).toEqual([]);
});

test("a listed profile with a ready connection reads Connected without an attention alert", () => {
  const profile = { id: "chatgpt", connection_id: "chatgpt", enabled: true, access_status: "connected", roles: ROLES };
  const status = currentProfileStatus(profile, { unavailable: false, snapshot: { connections: [{ connection_id: "chatgpt", state: "ready" }] } });
  expect(status).toMatchObject({ label: "Connected", attention: false, reconnect: false, tone: "connected" });
});

test("a catalog problem stops a profile with a ready connection", () => {
  const profile = { id: "profile", connection_id: "connection", access_status: "connected", problem: "The release catalog lacks this model. Select another model." };
  expect(currentProfileStatus(profile, { snapshot: { connections: [{ connection_id: "connection", state: "ready" }] } })).toMatchObject({ label: "Model unavailable", action: "Edit profile", attention: true, reconnect: false });
});

test("Use an existing connection lists only supported connections with a saved sign-in", () => {
  const connections = [
    { id: "chatgpt", kind: "codex-oauth", name: "Codex account", access_status: "connected" },
    { id: "claude", kind: "claude-oauth", access_status: "disconnected" },
    { id: "endpoint", name: "Endpoint", access_status: "no_token" },
    { id: "second-endpoint", access_status: "unknown" },
  ];
  expect(connectionChoices(connections, "")).toEqual([
    { value: "chatgpt", text: "Codex account · Credential saved", selected: false },
  ]);
  expect(connectionChoices(connections, "chatgpt").map((choice) => choice.selected)).toEqual([true]);
  expect(connectionChoices([], "")).toEqual([]);
});

test("only the Edit form keeps the current connection of an agent profile that lost its access", () => {
  const connections = [{ id: "chatgpt", kind: "codex-oauth", access_status: "connected" }, { id: "claude", kind: "claude-oauth", access_status: "disconnected" }];
  expect(connectionChoices(connections, "claude", true)).toEqual([
    { value: "chatgpt", text: "chatgpt · Credential saved", selected: false },
    { value: "claude", text: "claude · Disconnected", selected: true },
  ]);
  expect(connectionChoices(connections, "claude")).toEqual([
    { value: "chatgpt", text: "chatgpt · Credential saved", selected: false },
  ]);
});

test("the profile status states the connection access without a successful test claim", () => {
  expect(profileStatus({ access_status: "disconnected" })).toMatchObject({ attention: true, reconnect: true, label: "Disconnected" });
  expect(profileStatus({ access_status: "connected" })).toMatchObject({ attention: false, reconnect: false, label: "Credential saved" });
  const unknown = profileStatus({});
  expect(unknown.label).toBe("Access unknown");
  expect(unknown.attention).toBe(true);
});

test("a profile save excludes credentials and retains the exact model identifier", () => {
  const draft = {
    submission_id: "request-1", id: "profile-1", name: " Careful ", connection_id: "team",
    model: "custom/model:v2", base_url: "https://endpoint.example/api", test_id: "test-1",
    token: "secret-canary", connection_name: "Team", integration: "provider",
  };
  const payload = profilePayload(draft, "revision-1", true);
  expect(payload).toEqual({
    submission_id: "request-1", revision: "revision-1", id: "profile-1", name: "Careful",
    connection_id: "team", model: "custom/model:v2",
    enabled: true, test_id: "test-1",
  });
  expect(JSON.stringify(payload)).not.toContain("secret-canary");
});

test("a model test request carries no earlier test receipt", () => {
  const payload = profilePayload({ submission_id: "request-1", name: "Draft", connection_id: "team", model: "model", test_id: "old-test" }, "revision-1", false);
  expect(payload.enabled).toBe(false);
  expect(payload).not.toHaveProperty("test_id");
});


test("Current connection state overrides a saved credential", () => {
  const profile = { id:"profile",connection_id:"connection",enabled:true,access_status:"connected" };
  const failed = currentProfileStatus(profile,{unavailable:false,snapshot:{connections:[{connection_id:"connection",state:"stopped",action:"Reconnect"}]}});
  expect(failed.label).toBe("Connection unavailable");
  expect(failed.attention).toBe(true);
  const recovered = currentProfileStatus(profile,{unavailable:false,snapshot:{connections:[{connection_id:"connection",state:"ready"}]}});
  expect(recovered.label).toBe("Connected");
  const unavailable = currentProfileStatus(profile,{unavailable:true,snapshot:{connections:[{connection_id:"connection",state:"ready"}]}});
  expect(unavailable.label).toBe("Status unavailable");
});

test("the Edit form requests authorization from the current connection state only", () => {
  const profile = { id:"profile",connection_id:"connection" };
  const system = (row: Record<string, unknown>) => ({unavailable:false,snapshot:{connections:[{connection_id:"connection",...row}]}});
  expect(needsAuthorization(profile, system({state:"ready"}))).toBe(false);
  expect(needsAuthorization(profile, undefined)).toBe(false);
  expect(needsAuthorization(profile, system({state:"stopped",action:"Reconnect"}))).toBe(true);
  expect(needsAuthorization(profile, system({state:"stopped",action:"Retry connection"}))).toBe(false);
  expect(needsAuthorization(profile, system({state:"stopped",action:"Reconnect",profile_id:"profile"}))).toBe(false);
  expect(needsAuthorization({ ...profile, connection_id:"other" }, system({state:"stopped",action:"Reconnect"}))).toBe(false);
  expect(needsAuthorization(null, system({state:"stopped",action:"Reconnect"}))).toBe(false);
});

test("Only a stopped connection that needs authorization offers Reconnect", () => {
  const profile = { id:"profile",connection_id:"connection",enabled:true,access_status:"connected" };
  const status = (row: Record<string, unknown>) => currentProfileStatus(profile,{unavailable:false,snapshot:{connections:[{connection_id:"connection",...row}]}});
  expect(status({state:"stopped",action:"Reconnect"}).reconnect).toBe(true);
  expect(status({state:"retry_wait",action:"",network_error:"connect"})).toMatchObject({label:"Retry scheduled",reconnect:false,attention:true});
  expect(status({state:"stopped",action:"Retry connection"}).reconnect).toBe(false);
});

test("an operation summary states whether the operation runs, waits for the operator, or ended", () => {
  expect(operationSummary({ kind: "model_test", status: "pending" })).toEqual({ tone: "active", active: true, title: "The model test is in progress" });
  expect(operationSummary({ kind: "model_test", status: "running" }).tone).toBe("active");
  expect(operationSummary({ kind: "authorization", status: "action_required" })).toEqual({ tone: "action", active: true, title: "The authorization requires your action" });
  expect(operationSummary({ kind: "model_test", status: "succeeded" })).toEqual({ tone: "success", active: false, title: "The model test succeeded" });
  expect(operationSummary({ kind: "authorization", status: "failed" })).toEqual({ tone: "failure", active: false, title: "The authorization failed" });
  expect(operationSummary({ kind: "model_test", status: "cancelled" })).toEqual({ tone: "quiet", active: false, title: "The model test is cancelled" });
});

test("the elapsed time counts whole seconds from the operation start", () => {
  const start = "2026-09-23T10:00:00.000Z";
  expect(elapsedLabel(start, Date.parse("2026-09-23T10:00:12.900Z"))).toBe("12 s");
  expect(elapsedLabel(start, Date.parse("2026-09-23T10:01:05.000Z"))).toBe("1 min 5 s");
  expect(elapsedLabel(start, Date.parse("2026-09-23T09:59:59.000Z"))).toBe("0 s");
  expect(elapsedLabel(undefined, Date.now())).toBe("");
});

test("only a connected subscription lists its models", () => {
  expect(listsModels({ kind: "codex-oauth", access_status: "connected" })).toBe(true);
  expect(listsModels({ kind: "claude-oauth", access_status: "connected" })).toBe(true);
  expect(listsModels({ kind: "codex-oauth", access_status: "disconnected" })).toBe(false);
  expect(listsModels({ kind: "static-env", access_status: "connected" })).toBe(false);
  expect(listsModels(undefined)).toBe(false);
});

test("model choices show the provider name and keep a model that the provider does not offer", () => {
  const models = [{ id: "gpt-6-sol", name: "GPT-6-Sol" }, { id: "plain-model", name: "plain-model" }];
  expect(modelChoices(models, "plain-model")).toEqual([
    { value: "gpt-6-sol", text: "GPT-6-Sol (gpt-6-sol)", selected: false },
    { value: "plain-model", text: "plain-model", selected: true },
  ]);
  expect(modelChoices(models, "retired-model")[0]).toEqual({ value: "retired-model", text: "retired-model (not offered)", selected: true });
  expect(modelChoices(models, "").some((choice) => choice.selected)).toBe(false);
});


test("reuse choices exclude failed accounts and keep the current account for repair", () => {
  const connections = [
    { id: "ready", kind: "codex-oauth", access_status: "connected" },
    { id: "failed", kind: "claude-oauth", access_status: "connected" },
    { id: "unsupported", kind: "static-env", access_status: "connected" },
  ];
  const system = { unavailable: false, snapshot: { connections: [
    { connection_id: "ready", state: "ready" },
    { connection_id: "failed", state: "stopped" },
  ] } };
  expect(connectionChoices(connections, "", false, system).map((row) => row.value)).toEqual(["ready"]);
  expect(connectionChoices(connections, "failed", true, system).map((row) => row.value)).toEqual(["ready", "failed"]);
  expect(connectionChoices(connections, "", false, { ...system, unavailable: true })).toEqual([]);
});

test("API key connections offer model choices and preserve the saved credential distinction", () => {
  for (const kind of ["codex-api-key", "claude-api-key"]) {
    const connection = { id: kind, kind, name: "API connection", access_status: "connected" };
    expect(connectionChoices([connection], kind)).toEqual([{ value: kind, text: "API connection · Credential saved", selected: true }]);
    expect(listsModels(connection)).toBe(true);
    expect(listsModels({ ...connection, access_status: "unavailable" })).toBe(false);
  }
});

test("profile payloads exclude an API key and its replacement revision", () => {
  const payload = profilePayload({ submission_id: "profile", name: "API profile", connection_id: "key", model: "fixture-model", api_key: "fixture-secret", authorization_id: "credential-revision" }, "config-revision", false);
  expect(JSON.stringify(payload)).not.toContain("fixture-secret");
  expect(payload).not.toHaveProperty("api_key");
  expect(payload).not.toHaveProperty("authorization_id");
});

test("a rejected API key offers its specific repair action", () => {
  const profile = { id: "profile", connection_id: "key", access_status: "connected" };
  const system = { unavailable: false, snapshot: { connections: [{ connection_id: "key", state: "stopped", action: "Replace API key" }] } };
  expect(currentProfileStatus(profile, system)).toMatchObject({ reconnect: true, action: "Replace API key" });
  expect(needsAuthorization(profile, system)).toBe(true);
});

test("profile requests retain effort and omit the default override", () => {
  const draft = { name: "Effort", model: "selected", effort: "high" };
  expect(profilePayload(draft, "revision", false).effort).toBe("high");
  expect(profilePayload({ ...draft, effort: "" }, "revision", false)).not.toHaveProperty("effort");
});

test("effort choices retain an unavailable selection without a silent replacement", () => {
  expect(effortChoices({ effort_levels: ["low", "high"] }, "max")).toEqual([
    { value: "", text: "Default", selected: false },
    { value: "low", text: "Low", selected: false },
    { value: "high", text: "High", selected: false },
    { value: "max", text: "Max (unavailable)", selected: true, disabled: true },
  ]);
  expect(effortChoices(undefined)).toEqual([{ value: "", text: "Default", selected: true }]);
});
