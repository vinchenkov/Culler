import { afterEach, beforeEach, describe, expect, jest, test } from "bun:test";
import { createAgentProfiles, defaultConnectionName, downloadView, effortChoices, connectionChoices, currentProfileStatus, elapsedLabel, harnessName, listedProfiles, listsModels, modelChoices, modelSourceHint, needsAuthorization, operationSummary, plannedConnection, profileNameHasSpace, profilePayload, profileStatus, providerChoices, releaseProviders } from "./agentprofiles.js";
import { installFakeDocument, type FakeDocument, type FakeNode } from "./fakedom";

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
  const profile = { id: "profile", connection_id: "connection", access_status: "connected", problem: "The connection does not offer this model. Select another model." };
  expect(currentProfileStatus(profile, { snapshot: { connections: [{ connection_id: "connection", state: "ready" }] } })).toMatchObject({ label: "Model unavailable", action: "Edit profile", attention: true, reconnect: false });
});

test("the saved connections are only supported connections with a saved sign-in", () => {
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

test("the provider search lists name matches before identifier and address matches", () => {
  const providers = [
    { id: "claude-api-key", name: "Anthropic", harness: "claude-sdk", catalog_entry: "anthropic", address: "https://api.anthropic.com/v1" },
    { id: "opencode:blueclaw", name: "Blue Claw", harness: "opencode", catalog_entry: "blueclaw", address: "https://openai.blueclaw.network/v1" },
    { id: "opencode:kimi-code-plan-global", name: "Kimi For Coding (kimi.ai)", harness: "opencode", catalog_entry: "kimi-code-plan-global", address: "https://api.kimi.ai/coding/v1" },
    { id: "opencode:moonshotai", name: "Moonshot AI", harness: "opencode", catalog_entry: "moonshotai", address: "https://api.moonshot.ai/v1" },
    { id: "codex-api-key", name: "OpenAI", harness: "codex", catalog_entry: "openai", address: "https://api.openai.com/v1" },
    { id: "opencode:opencode-go", name: "OpenCode Go", harness: "opencode", catalog_entry: "opencode-go", address: "https://opencode.ai/inference/go/openai/v1" },
  ];
  const names = (query: string) => providerChoices(providers, query).map(provider => provider.name);
  expect(names("")).toEqual(["Blue Claw", "Kimi For Coding (kimi.ai)", "Moonshot AI", "OpenCode Go"]);
  expect(names("kim")).toEqual(["Kimi For Coding (kimi.ai)"]);
  expect(names("claude")).toEqual([]);
  expect(names("opencode")).toEqual(["OpenCode Go"]);
  expect(names("openai")).toEqual(["Blue Claw", "OpenCode Go"]);
  expect(names("ai")).toEqual(["Kimi For Coding (kimi.ai)", "Moonshot AI", "Blue Claw", "OpenCode Go"]);
  expect(names("API.KIMI.AI")).toEqual(["Kimi For Coding (kimi.ai)"]);
});

test("the release providers list the first harness family first, each with its sign-in and its API key", () => {
  const catalog = {
    providers: [
      { id: "claude-api-key", name: "Second provider", harness: "claude-sdk", kind: "claude-api-key" },
      { id: "opencode:fixture", name: "Fixture", harness: "opencode", kind: "opencode-api-key" },
      { id: "codex-api-key", name: "First provider", harness: "codex", kind: "codex-api-key" },
    ],
    integrations: [
      { id: "claude", harness: "claude-sdk", kind: "claude-oauth" }, { id: "claude-api-key", harness: "claude-sdk", kind: "claude-api-key" },
      { id: "chatgpt", harness: "codex", kind: "codex-oauth" }, { id: "codex-api-key", harness: "codex", kind: "codex-api-key" },
    ],
  };
  expect(releaseProviders(catalog).map(provider => [provider.name, provider.subscription.id, provider.api_key.id]))
    .toEqual([["First provider", "chatgpt", "codex-api-key"], ["Second provider", "claude", "claude-api-key"]]);
  expect(releaseProviders({ providers: [], integrations: [] })).toEqual([]);
});

test("a connection reuses an idle match and avoids names that already exist", () => {
  const template = { id: "codex-api-key", kind: "codex-api-key", harness: "codex", catalog_entry: "fixture" };
  const idle = { id: "codex-api-key", name: "fixture_connection", kind: "codex-api-key", access_status: "disconnected" };
  expect(plannedConnection({ connections: [], profiles: [] }, template, "fixture_connection")).toEqual({ id: null, name: "fixture_connection" });
  // A connection with the name and no credential receives the credential.
  expect(plannedConnection({ connections: [idle], profiles: [{ connection_id: idle.id, enabled: false }] }, template, "fixture_connection")).toEqual({ id: "codex-api-key", name: "fixture_connection" });
  // A used name gives the next free name.
  for (const catalog of [
    { connections: [{ ...idle, access_status: "connected" }], profiles: [] },
    { connections: [idle], profiles: [{ connection_id: idle.id, enabled: true }] },
    { connections: [{ ...idle, kind: "claude-api-key" }], profiles: [] },
  ]) expect(plannedConnection(catalog, template, "fixture_connection")).toEqual({ id: null, name: "fixture_2_connection" });
  const open = { id: "opencode:fixture", kind: "opencode-api-key", harness: "opencode", catalog_entry: "fixture" };
  const other = { id: "connection-1", name: "fixture_connection", kind: "opencode-api-key", catalog_entry: "other", access_status: "disconnected" };
  expect(plannedConnection({ connections: [other], profiles: [] }, open, "fixture_connection")).toEqual({ id: null, name: "fixture_2_connection" });
  expect(plannedConnection({ connections: [{ ...other, catalog_entry: "fixture" }], profiles: [] }, open, "FIXTURE_connection")).toEqual({ id: "connection-1", name: "fixture_connection" });
});

test("the model list hint states the source of the model list", () => {
  expect(modelSourceHint("release_catalog")).toBe("The release catalog supplies these models. Prices apply to each million input and output tokens.");
  expect(modelSourceHint("release_catalog_and_live")).toBe("The release catalog and the live model list of OpenCode supply these models. Prices apply to each million input and output tokens.");
  expect(modelSourceHint(undefined)).toBe(modelSourceHint("release_catalog"));
});

test("only a pending or active download shows the progress indicator, and only a stopped download offers Retry", () => {
  const view = (status?: string, message?: string) => downloadView(status ? { status, message } : undefined);
  expect(view("pending", "OpenCode waits for the download.")).toEqual({ tone: "active", active: true, retry: false, stage: "OpenCode waits for the download." });
  expect(view("running", "Unpack the OpenCode archive.")).toEqual({ tone: "active", active: true, retry: false, stage: "Unpack the OpenCode archive." });
  expect(view("succeeded", "OpenCode is ready.")).toEqual({ tone: "ready", active: false, retry: false, stage: "OpenCode is ready." });
  expect(view("failed", "The network is unavailable.")).toEqual({ tone: "failed", active: false, retry: true, stage: "The network is unavailable." });
  expect(view()).toEqual({ tone: "required", active: false, retry: true, stage: "OpenCode requires a download." });
});

describe("the Configure step of an OpenCode connection", () => {
  let page: FakeDocument;
  let restore: () => void;
  beforeEach(() => { ({ document: page, restore } = installFakeDocument()); jest.useFakeTimers(); });
  afterEach(() => { jest.useRealTimers(); restore(); });

  const MODELS = [
    { id: "fixture-k3", name: "Fixture K3", context: 262144, input_price: 0.5, output_price: 1.8, effort_levels: ["low", "high"], effort_support_known: true },
    { id: "fixture-k2", name: "Fixture K2", context: 131072, input_price: 0.2, output_price: 0.8, effort_levels: [], effort_support_known: true },
  ];
  const LIVE = { id: "fixture-k4", name: "Fixture K4", context: 524288, input_price: 1.25, output_price: 6.5, effort_levels: ["low", "high"], effort_support_known: true };
  const CATALOG_HINT = "The release catalog supplies these models. Prices apply to each million input and output tokens.";
  const LIVE_HINT = "The release catalog and the live model list of OpenCode supply these models. Prices apply to each million input and output tokens.";
  // The responses resolve in microtasks, so some turns complete each request and its render.
  const settle = async () => { for (let turn = 0; turn < 30; turn++) await Promise.resolve(); };
  const after = async (milliseconds: number) => { jest.advanceTimersByTime(milliseconds); await settle(); };

  /** Saves a key for the fixture provider and stops in the Configure step, with the model fixture-k3 and the focus in the Model control. */
  async function configureStep() {
    const provider = { id: "opencode:fixture", name: "Fixture provider", harness: "opencode", kind: "opencode-api-key", catalog_entry: "fixture", address: "https://api.fixture.invalid/v1" };
    const host = {
      download: { operation_id: "opencode-download", kind: "opencode_download", status: "running", message: "Download OpenCode and its web search plugin." },
      models: { connection_id: "fixture-connection", web_search: true, models: MODELS, source: "release_catalog" } as Record<string, unknown>,
      modelRequests: 0,
      starts: 0,
    };
    const data = { revision: "revision-1", providers: [provider], integrations: [], connections: [] as unknown[], profiles: [] };
    const api = async (path: string, options?: { body: string }) => {
      if (path === "/api/routing") return { routes: [] };
      if (path === "/api/agent-profiles") return structuredClone({ ...data, opencode: host.download });
      if (path.endsWith("/connection-save")) {
        data.connections = [{ ...provider, id: "fixture-connection", name: JSON.parse(options!.body).name, access_status: "connected", authorization_id: "authorization-1" }];
        return structuredClone({ ...data, opencode: host.download, connection_id: "fixture-connection" });
      }
      if (path.endsWith("/models")) { host.modelRequests++; return structuredClone(host.models); }
      if (path.endsWith("/operation-status")) return structuredClone(host.download);
      if (path.endsWith("/operation-start")) {
        host.starts++;
        host.download = { ...host.download, status: "pending", message: "OpenCode waits for the download." };
        return structuredClone(host.download);
      }
      throw new Error(`The test has no response for ${path}.`);
    };
    const panel = createAgentProfiles({ api, onAssigned() {}, systemState: () => undefined });
    await panel.open();
    const dialog = page.body.byTestid("agent-profiles-panel")!;
    const node = (testid: string) => dialog.byTestid(testid);
    const click = (testid: string) => node(testid)!.dispatch("click");
    await click("profile-create");
    await click("profile-use-provider");
    node("profile-provider-opencode:fixture")!.checked = true;
    await node("profile-provider-opencode:fixture")!.dispatch("change");
    node("profile-api_key")!.value = "synthetic-key";
    await node("profile-api_key")!.dispatch("input");
    await click("profile-key-connect");
    await settle();
    expect(data.connections[0]).toMatchObject({ name: "fixture_provider_connection" });
    expect(node("profile-name")!.value).toBe("");
    const model = node("profile-model")!;
    model.value = "fixture-k3";
    await model.dispatch("change");
    model.focus();
    const hint = () => dialog.all().find((item) => item.getAttribute("id") === "profile-model-hint")!.textContent;
    const choices = () => model.options.map((option) => option.value);
    return { panel, dialog, host, node, click, model, hint, choices };
  }

  test("a download status change preserves the Model control element, its focus, and its selection", async () => {
    const view = await configureStep();
    const options = [...view.model.options];
    expect(view.node("profile-opencode-progress")!.getAttribute("role")).toBe("progressbar");
    expect(view.node("profile-opencode-stage")!.textContent).toBe("Download OpenCode and its web search plugin.");
    expect(view.node("profile-opencode-stage")!.getAttribute("role")).toBe("status");
    expect(view.node("profile-opencode-download")!.textContent).toContain("The model test waits for the download.");
    expect(view.node("profile-effort")!.hasAttribute("disabled")).toBe(false);
    expect(view.node("profile-name")!.hasAttribute("disabled")).toBe(false);

    view.host.download = { ...view.host.download, message: "Unpack the OpenCode archive." };
    page.changes = [];
    await after(1000);
    const stage = view.node("profile-opencode-stage")!;
    expect(stage.textContent).toBe("Unpack the OpenCode archive.");
    // The text of the stage is the one change of the document.
    expect(page.changes.length).toBe(1);
    expect(page.changes[0]).toBe(stage.childNodes[0]!);
    expect(view.node("profile-model")).toBe(view.model);
    expect(view.model.options.length).toBe(options.length);
    view.model.options.forEach((option, index) => expect(option).toBe(options[index]!));
    expect(page.activeElement).toBe(view.model);
    expect(view.model.value).toBe("fixture-k3");
    view.panel.close();
  });

  test("the profile name starts empty and refuses spaces", async () => {
    const view = await configureStep();
    const name = view.node("profile-name")!;
    name.value = "fixture profile";
    await name.dispatch("input");
    await view.click("profile-configure-next");
    expect(view.node("profile-error")!.textContent).toBe("Enter an agent profile name without spaces.");
    expect(view.node("profile-model")).toBeTruthy();
    view.panel.close();
  });

  test("a successful download removes the progress indicator, and the next refresh adds its live models", async () => {
    const view = await configureStep();
    expect(view.hint()).toBe(CATALOG_HINT);
    expect(view.host.modelRequests).toBe(1);
    await after(3000);
    expect(view.host.modelRequests).toBe(1);

    view.host.download = { ...view.host.download, status: "succeeded", message: "OpenCode is ready." };
    view.host.models = { ...view.host.models, refreshing: true };
    await after(1000);
    expect(view.node("profile-opencode-progress")).toBeNull();
    expect(view.node("profile-opencode-retry")).toBeNull();
    expect(view.node("profile-opencode-download")!.textContent).toBe("OpenCode is ready.");
    expect(view.host.modelRequests).toBe(2);
    expect(page.activeElement).toBe(view.model);

    view.host.models = { connection_id: "fixture-connection", web_search: true, models: [LIVE, ...MODELS], source: "release_catalog_and_live" };
    await after(3000);
    expect(view.host.modelRequests).toBe(3);
    // The Model control has the focus, so its list keeps its choices.
    expect(view.choices()).toEqual(["", "fixture-k3", "fixture-k2"]);
    expect(view.hint()).toBe(CATALOG_HINT);

    view.model.blur();
    await settle();
    expect(view.node("profile-model")).toBe(view.model);
    expect(view.choices()).toEqual(["", "fixture-k4", "fixture-k3", "fixture-k2"]);
    expect(view.model.options[1]!.textContent).toBe("Fixture K4 (fixture-k4) · 524,288 tokens · $1.25 input / $6.5 output per million tokens");
    expect(view.model.value).toBe("fixture-k3");
    expect(view.hint()).toBe(LIVE_HINT);
    await after(10000);
    expect(view.host.modelRequests).toBe(3);
    view.panel.close();
  });

  test("a model selection shows the live models that waited for the focus", async () => {
    const view = await configureStep();
    view.host.models = { connection_id: "fixture-connection", web_search: true, models: [LIVE, ...MODELS], source: "release_catalog_and_live" };
    view.host.download = { ...view.host.download, status: "succeeded", message: "OpenCode is ready." };
    await after(1000);
    expect(view.choices()).toEqual(["", "fixture-k3", "fixture-k2"]);
    view.model.value = "fixture-k2";
    await view.model.dispatch("change");
    expect(view.choices()).toEqual(["", "fixture-k4", "fixture-k3", "fixture-k2"]);
    expect(view.model.value).toBe("fixture-k2");
    expect(page.activeElement).toBe(view.model);
    view.panel.close();
  });

  test("a failed model list request in the background keeps the available model list", async () => {
    const view = await configureStep();
    view.model.blur();
    view.host.download = { ...view.host.download, status: "succeeded", message: "OpenCode is ready." };
    view.host.models = { models: [] };
    await after(1000);
    expect(view.host.modelRequests).toBe(2);
    expect(view.node("profile-model")).toBe(view.model);
    expect(view.node("profile-model-list-error")).toBeNull();
    expect(view.choices()).toEqual(["", "fixture-k3", "fixture-k2"]);
    expect(view.model.value).toBe("fixture-k3");
    view.panel.close();
  });

  test("a failed download shows the cause and Retry, and Retry starts the download again", async () => {
    const view = await configureStep();
    view.host.download = { ...view.host.download, status: "failed", message: "The OpenCode download failed. Retry the download." };
    await after(1000);
    expect(view.node("profile-opencode-progress")).toBeNull();
    expect(view.node("profile-opencode-stage")!.textContent).toBe("The OpenCode download failed. Retry the download.");
    expect(view.node("profile-opencode-download")!.textContent).not.toContain("The model test waits for the download.");
    expect(view.node("profile-model")).toBe(view.model);
    expect(view.model.value).toBe("fixture-k3");
    await view.click("profile-opencode-retry");
    expect(view.host.starts).toBe(1);
    expect(view.node("profile-opencode-retry")).toBeNull();
    expect(view.node("profile-opencode-progress")).not.toBeNull();
    expect(view.node("profile-opencode-stage")!.textContent).toBe("OpenCode waits for the download.");
    expect(view.node("profile-model")).toBe(view.model);
    view.panel.close();
  });
});

test("each harness family has a display name", () => {
  expect(["codex", "claude-sdk", "opencode"].map(harnessName)).toEqual(["Codex", "Claude Agent SDK", "OpenCode"]);
  expect(harnessName("future-harness")).toBe("future-harness");
  expect(harnessName(undefined)).toBe("");
});

describe("the first screen of the creation form", () => {
  let page: FakeDocument;
  let restore: () => void;
  beforeEach(() => { ({ document: page, restore } = installFakeDocument()); });
  afterEach(() => restore());
  const settle = async () => { for (let turn = 0; turn < 30; turn++) await Promise.resolve(); };

  async function choiceScreen(connections: unknown[]) {
    const data = {
      revision: "revision-1", profiles: [], connections,
      providers: [
        { id: "codex-api-key", name: "First provider", harness: "codex", kind: "codex-api-key" },
        { id: "claude-api-key", name: "Second provider", harness: "claude-sdk", kind: "claude-api-key" },
        { id: "opencode:fixture", name: "Fixture", harness: "opencode", kind: "opencode-api-key" },
      ],
      integrations: [{ id: "chatgpt", harness: "codex", kind: "codex-oauth" }],
    };
    const api = async (path: string) => {
      if (path === "/api/routing") return { routes: [] };
      if (path === "/api/agent-profiles") return structuredClone(data);
      if (path.endsWith("/models")) return { models: [{ id: "fixture-model" }] };
      throw new Error(`The test has no response for ${path}.`);
    };
    const panel = createAgentProfiles({ api, onAssigned() {}, systemState: () => undefined });
    await panel.open();
    const dialog = page.body.byTestid("agent-profiles-panel")!;
    const node = (testid: string) => dialog.byTestid(testid);
    await node("profile-create")!.dispatch("click");
    return { panel, dialog, node };
  }
  const options = (root: FakeNode) => root.querySelectorAll(".profile-option").map((item) => [item.dataset.testid, ...item.querySelector(".profile-option-text")!.childNodes.map((part) => part.textContent)]);

  test("the provider cards name each harness, and the catalog choice comes last", async () => {
    const view = await choiceScreen([{ id: "idle", name: "idle_connection", kind: "codex-oauth", harness: "codex", access_status: "disconnected" }]);
    expect(view.node("profile-saved-connections")).toBeNull();
    expect(view.dialog.textContent).toContain("Connect a provider for the agent profile.");
    expect(options(view.node("profile-new-connections")!)).toEqual([
      ["profile-use-codex", "First provider", "Sign in or use an API key · Harness: Codex"],
      ["profile-use-claude-sdk", "Second provider", "Use an API key · Harness: Claude Agent SDK"],
      ["profile-use-provider", "Another provider", "API key for a provider in the catalog · Harness: OpenCode"],
    ]);
    expect(view.node("profile-use-codex")!.querySelector(".profile-mark-generic")).toBeNull();
    expect(view.node("profile-use-provider")!.querySelector(".profile-mark-generic")).not.toBeNull();
    await view.node("profile-use-claude-sdk")!.dispatch("click");
    expect(view.node("profile-harness")!.textContent).toBe("Culler runs the agents of this connection with the Claude Agent SDK harness.");
    view.panel.close();
  });

  test("a saved connection opens the Configure step with that connection selected", async () => {
    const view = await choiceScreen([
      { id: "team", name: "team_connection", kind: "codex-oauth", harness: "codex", access_status: "connected" },
      { id: "fixture", name: "fixture_connection", kind: "opencode-api-key", harness: "opencode", access_status: "connected" },
      { id: "idle", name: "idle_connection", kind: "claude-oauth", harness: "claude-sdk", access_status: "disconnected" },
    ]);
    expect(view.dialog.textContent).toContain("Reuse a saved connection, or connect a provider.");
    expect(options(view.node("profile-saved-connections")!)).toEqual([
      ["profile-saved-team", "team_connection", "Harness: Codex · Credential saved"],
      ["profile-saved-fixture", "fixture_connection", "Harness: OpenCode · Credential saved"],
    ]);
    expect(view.node("profile-saved-fixture")!.querySelector(".profile-mark-generic")).not.toBeNull();
    await view.node("profile-saved-team")!.dispatch("click");
    await settle();
    expect(view.node("profile-flow-title")!.textContent).toBe("Use a saved connection");
    expect(view.node("profile-connection_id")!.value).toBe("team");
    expect(view.node("profile-review-harness")!.textContent).toBe("Codex");
    await view.node("profile-back")!.dispatch("click");
    expect(view.node("profile-saved-connections")).not.toBeNull();
    view.panel.close();
  });
});

test("a default connection name uses snake case and a unique number", () => {
  for (const [provider, name] of [["OpenAI", "openai_connection"], ["Moonshot AI", "moonshot_ai_connection"], ["Z.AI  Coding", "z_ai_coding_connection"]]) {
    expect(defaultConnectionName(provider)).toBe(name);
  }
  const template = { kind: "codex-api-key" };
  expect(plannedConnection({ connections: [] }, template, "fixture_connection").name).toBe("fixture_connection");
  const used = [{ name: "fixture_connection", access_status: "connected" }, { name: "fixture_2_connection", access_status: "connected" }];
  expect(plannedConnection({ connections: used }, template, "fixture_connection").name).toBe("fixture_3_connection");
});

test("an agent profile name accepts each character other than a space", () => {
  for (const name of ["fixture_profile", "Fixture-Profile.2", "fixture/profile:ä"]) expect(profileNameHasSpace(name)).toBe(false);
  for (const name of ["fixture profile", "fixture\tprofile", "fixture\u00a0profile"]) expect(profileNameHasSpace(name)).toBe(true);
});

describe("connection deletion", () => {
  let page: FakeDocument;
  let restore: () => void;
  beforeEach(() => { ({ document: page, restore } = installFakeDocument()); });
  afterEach(() => restore());
  const settle = async () => { for (let turn = 0; turn < 30; turn++) await Promise.resolve(); };

  async function confirmation(roles: string[]) {
    const posts: unknown[] = [];
    const data = {
      revision: "revision-1", providers: [], integrations: [],
      connections: [{ id: "team", name: "team_connection", kind: "codex-api-key", harness: "codex", access_status: "connected", authorization_id: "authorization-1" }],
      profiles: [
        { id: "first", name: "first_profile", connection_id: "team", model: "model-a", enabled: true, access_status: "connected", roles },
        { id: "hidden", name: "hidden_profile", connection_id: "team", model: "model-b", enabled: false, access_status: "connected", roles: [] },
      ],
    };
    const api = async (path: string, options?: { body: string }) => {
      if (path === "/api/routing") return { routes: [] };
      if (path === "/api/agent-profiles") return structuredClone(data);
      if (path === "/api/agent-profiles/connection-delete") {
        posts.push(JSON.parse(options!.body));
        return { ...structuredClone(data), connections: [], profiles: [], message: "The connection is deleted." };
      }
      throw new Error(`The test has no response for ${path}.`);
    };
    const panel = createAgentProfiles({ api, onAssigned() {}, systemState: () => undefined });
    await panel.open();
    const node = (testid: string) => page.body.byTestid("agent-profiles-panel")!.byTestid(testid);
    await node("profile-delete-team")!.dispatch("click");
    return { panel, node, posts };
  }

  test("the confirmation names the listed agent profiles and sends the current authorization", async () => {
    const view = await confirmation([]);
    const text = view.node("profile-delete-confirm")!.textContent;
    expect(text).toContain("Delete team_connection?");
    expect(text).toContain("Agent profiles to delete: first_profile.");
    await view.node("profile-delete-confirmed")!.dispatch("click");
    await settle();
    expect(view.posts).toEqual([{ connection_id: "team", authorization_id: "authorization-1", confirm: true }]);
    expect(view.node("profile-delete-confirm")).toBeNull();
    expect(view.node("profile-delete-team")).toBeNull();
    view.panel.close();
  });

  test("a connection that a role uses offers no deletion and names the roles", async () => {
    const view = await confirmation(["worker", "collie"]);
    expect(view.node("profile-delete-confirm")!.textContent).toContain("Roles use this connection: Collie, Worker.");
    expect(view.node("profile-delete-confirmed")).toBeNull();
    await view.node("profile-delete-cancel")!.dispatch("click");
    expect(view.node("profile-delete-confirm")).toBeNull();
    expect(view.posts).toEqual([]);
    view.panel.close();
  });
});
