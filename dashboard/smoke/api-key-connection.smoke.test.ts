import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => { smoke = await bootSmoke(); await seedProject(smoke, "api-key-form", { title: "API key form" }); }, 60000);
afterEach(async () => {
  const close = smoke.page.getByTestId("profile-close");
  if (await close.isVisible()) await close.click();
  await smoke.page.unrouteAll({ behavior: "wait" });
});
afterAll(async () => { await teardown(smoke); }, 30000);

test("one masked key creates a connection and survives a failed model test without another key entry", async () => {
  const { page } = smoke;
  const secret = "synthetic-api-key-canary";
  const data = { revision: "revision-1", connections: [] as any[], profiles: [] as any[], integrations: [
    { id: "codex-api-key", kind: "codex-api-key", harness: "codex", name: "First key", authorization_available: true },
    { id: "claude-api-key", kind: "claude-api-key", harness: "claude-sdk", name: "Second key", authorization_available: true },
    { id: "claude", kind: "claude-oauth", harness: "claude-sdk", name: "Second sign-in", authorization_available: true },
  ], providers: [
    { id: "codex-api-key", kind: "codex-api-key", harness: "codex", name: "First provider" },
    { id: "claude-api-key", kind: "claude-api-key", harness: "claude-sdk", name: "Second provider" },
  ] };
  let keySaves = 0;
  let tests = 0;
  let active: any;
  await page.route("**/api/agent-profiles", route => route.fulfill({ json: data }));
  await page.route("**/api/agent-profiles/connection-save", async route => {
    const request = route.request().postDataJSON();
    expect(request.api_key).toBe(secret);
    expect(request.template_id).toBe("claude-api-key");
    expect(request.name).toBe("Second provider API key");
    keySaves++;
    data.connections = [{ id: "key-connection", name: request.name, kind: "claude-api-key", access_status: "connected", authorization_id: "authorization-1", models: [] }];
    await route.fulfill({ json: { ...data, connection_id: "key-connection" } });
  });
  await page.route("**/api/agent-profiles/models", route => route.fulfill({ json: { connection_id: "key-connection", models: [{ id: "fixture-model", name: "Fixture model" }] } }));
  await page.route("**/api/agent-profiles/operation-start", async route => {
    active = route.request().postDataJSON();
    expect(active.kind).toBe("model_test");
    expect(JSON.stringify(active)).not.toContain(secret);
    tests++;
    await route.fulfill({ json: { ...active, status: "pending", started_at: new Date().toISOString() } });
  });
  await page.route("**/api/agent-profiles/operation-status", route => route.fulfill({ json: { ...active, status: tests === 1 ? "failed" : "succeeded", message: tests === 1 ? "The provider is temporarily unavailable." : "The model test succeeded.", error_code: tests === 1 ? "model_test" : undefined } }));
  await page.route("**/api/agent-profiles/save", route => route.fulfill({ json: { ...data, profile_id: "api-profile" } }));
  await gotoReady(smoke);
  await page.getByTestId("tab-pipeline").click();
  await page.getByRole("button", { name: "Model configuration", exact: true }).click();
  await page.getByTestId("profile-create").click();
  expect(await page.locator('[data-testid^="profile-use-"]').evaluateAll(nodes => nodes.map(node => node.textContent)))
    .toEqual(["Use First provider", "Use Second provider", "Use another provider", "Use an existing connection"]);
  await page.getByTestId("profile-use-claude-sdk").click();
  expect(await page.locator('[data-testid^="profile-access-"]').evaluateAll(nodes => nodes.map(node => (node as HTMLElement).dataset.testid)))
    .toEqual(["profile-access-subscription", "profile-access-api-key"]);
  expect(await page.getByTestId("profile-connection_name").count()).toBe(0);
  await page.screenshot({ path: evidencePath("provider-access-methods.png") });
  const input = page.getByTestId("profile-api_key");
  expect(await input.getAttribute("type")).toBe("password");
  await input.fill(secret);
  await page.getByTestId("profile-key-connect").click();
  const model = page.getByTestId("profile-model");
  await model.locator('option[value="fixture-model"]').waitFor({ state: "attached" });
  await model.selectOption("fixture-model");
  await page.getByTestId("profile-name").fill("API profile");
  await page.getByTestId("profile-configure-next").click();
  await page.getByTestId("profile-test").click();
  await page.getByText("The provider is temporarily unavailable.", { exact: true }).waitFor();
  expect(await page.getByTestId("profile-review").innerText()).toContain("API profile");
  expect(await page.getByTestId("profile-api_key").count()).toBe(0);
  const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
  expect(storage).not.toContain(secret);
  await page.getByTestId("profile-test").click();
  await page.getByTestId("profile-save").waitFor({ state: "visible" });
  await page.waitForFunction(() => !(document.querySelector('[data-testid="profile-save"]') as HTMLButtonElement)?.disabled);
  expect(keySaves).toBe(1);
  expect(tests).toBe(2);
  await page.screenshot({ path: evidencePath("api-key-model-test-desktop.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.getByTestId("agent-profiles-panel").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("api-key-model-test-narrow.png") });
  await page.getByTestId("profile-save").click();
  await page.getByTestId("profile-create").waitFor();
}, 30000);

test("OpenCode search, catalog models, download retry, and model test retain the selected effort", async () => {
  const { page } = smoke;
  await page.setViewportSize({ width: 1280, height: 900 });
  const providers = [
    { id: "codex-api-key", name: "Release provider", harness: "codex", kind: "codex-api-key", catalog_entry: "release", address: "https://release.invalid/v1", website: "https://release.invalid/docs" },
    { id: "opencode:fixture", name: "Fixture provider", harness: "opencode", kind: "opencode-api-key", catalog_entry: "fixture", address: "https://api.fixture.invalid/v1", website: "https://fixture.invalid/docs", web_search: true },
    { id: "opencode:fixture-region", name: "Fixture provider region", harness: "opencode", kind: "opencode-api-key", catalog_entry: "fixture-region", address: "https://regional.fixture.invalid/v1", website: "https://regional.fixture.invalid/docs", web_search: true },
  ];
  const data: any = { revision: "open-revision", providers, integrations: [], connections: [], profiles: [], opencode: { operation_id: "opencode-download", kind: "opencode_download", status: "running", message: "Download OpenCode and its web search plugin." } };
  let downloadState = "running";
  let testRequest: any;
  await page.route("**/api/agent-profiles", route => route.fulfill({ json: data }));
  await page.route("**/api/agent-profiles/connection-save", async route => {
    const request = route.request().postDataJSON();
    expect(request.template_id).toBe("opencode:fixture");
    expect(request.name).toBe("Fixture provider API key");
    data.connections = [{ ...providers[1], id: "fixture-connection", access_status: "connected", authorization_id: "fixture-authorization", models: [] }];
    await route.fulfill({ json: { ...data, connection_id: "fixture-connection" } });
  });
  await page.route("**/api/agent-profiles/models", route => route.fulfill({ json: { connection_id: "fixture-connection", web_search: true, models: [
    { id: "fixture-k3", name: "Fixture K3", context: 262144, input_price: 0.5, output_price: 1.8, effort_levels: ["low", "high", "max"], effort_support_known: true },
    { id: "fixture-toggle", name: "Fixture toggle", context: 131072, input_price: 0.2, output_price: 0.8, effort_levels: ["none", "thinking"], effort_support_known: true },
  ] } }));
  await page.route("**/api/agent-profiles/operation-status", async route => {
    const request = route.request().postDataJSON();
    if (request.operation_id === "opencode-download") await route.fulfill({ json: { ...data.opencode, status: downloadState, message: downloadState === "failed" ? "The OpenCode download failed. Retry the download." : downloadState === "succeeded" ? "OpenCode is ready." : data.opencode.message } });
    else await route.fulfill({ json: { ...testRequest, status: "succeeded", message: "The model response and tool call succeeded." } });
  });
  await page.route("**/api/agent-profiles/operation-start", async route => {
    const request = route.request().postDataJSON();
    if (request.kind === "opencode_download") { downloadState = "succeeded"; data.opencode.status = "succeeded"; data.opencode.message = "OpenCode is ready."; await route.fulfill({ json: data.opencode }); }
    else { expect(request.profile.model).toBe("fixture-k3"); expect(request.profile.effort).toBe("high"); testRequest = request; await route.fulfill({ json: { ...request, status: "running", started_at: new Date().toISOString() } }); }
  });
  await page.route("**/api/agent-profiles/save", async route => { const request = route.request().postDataJSON(); expect(request.effort).toBe("high"); await route.fulfill({ json: { ...data, profile_id: "open-profile" } }); });
  await gotoReady(smoke);
  await page.getByTestId("tab-pipeline").click();
  await page.getByRole("button", { name: "Model configuration", exact: true }).click();
  await page.getByTestId("profile-create").click();
  await page.getByTestId("profile-use-provider").click();
  expect(await page.getByTestId("profile-provider-result").count()).toBe(2);
  expect(await page.getByTestId("profile-selected-provider").innerText()).toContain("None");
  const search = page.getByTestId("profile-key-provider");
  await search.fill("no-provider-987"); expect(await page.getByTestId("profile-provider-result").count()).toBe(0);
  await search.fill("fixture"); expect(await page.getByTestId("profile-provider-result").count()).toBe(2);
  expect(await page.getByTestId("profile-provider-results").innerText()).toContain("https://regional.fixture.invalid/v1");
  await search.fill("https://api.fixture.invalid/v1"); expect(await page.getByTestId("profile-provider-result").count()).toBe(1);
  await page.getByTestId("profile-provider-opencode:fixture").click();
  expect(await page.getByTestId("profile-provider-opencode:fixture").isChecked()).toBe(true);
  expect(await page.locator(".profile-provider.selected").innerText()).toContain("Selected");
  await search.fill("region");
  expect(await page.locator(".profile-provider.selected").count()).toBe(0);
  expect(await page.getByTestId("profile-selected-provider").innerText()).toContain("Fixture provider\nhttps://api.fixture.invalid/v1");
  await search.fill("fixture");
  await page.screenshot({ path: evidencePath("provider-selected.png") });
  await page.getByTestId("profile-api_key").fill("synthetic-open-key");
  await page.getByTestId("profile-key-connect").click();
  const model = page.getByTestId("profile-model");
  await model.locator('option[value="fixture-k3"]').waitFor({ state: "attached" });
  expect(await model.innerText()).toContain("262,144"); expect(await model.innerText()).toContain("$0.5 input");
  await model.selectOption("fixture-toggle");
  expect(await page.getByTestId("profile-effort").innerText()).toContain("Thinking");
  await model.selectOption("fixture-k3");
  expect(await page.getByTestId("profile-effort").locator("option").allTextContents()).toEqual(["Default", "Low", "High", "Max"]);
  await page.getByTestId("profile-effort").selectOption("high");
  await page.getByTestId("profile-name").fill("OpenCode profile");
  await page.getByText("The agents of this connection can search the web.", { exact: true }).waitFor();
  await page.getByTestId("profile-configure-next").click();
  expect(await page.getByTestId("profile-test").isDisabled()).toBe(true);
  downloadState = "failed";
  await page.getByTestId("profile-opencode-retry").click();
  await page.getByTestId("profile-test").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="profile-save"]') as HTMLButtonElement)?.disabled);
  await page.screenshot({ path: evidencePath("opencode-model-test.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.getByTestId("agent-profiles-panel").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("opencode-model-test-narrow.png") });
  await page.getByTestId("profile-save").click();
  await page.getByTestId("profile-create").waitFor();
}, 40000);
