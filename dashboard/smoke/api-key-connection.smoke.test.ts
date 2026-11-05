import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => { smoke = await bootSmoke(); await seedProject(smoke, "api-key-form", { title: "API key form" }); }, 60000);
afterEach(async () => { await smoke.page.unrouteAll({ behavior: "wait" }); });
afterAll(async () => { await teardown(smoke); }, 30000);

test("one masked key creates a connection and survives a failed model test without another key entry", async () => {
  const { page } = smoke;
  const secret = "synthetic-api-key-canary";
  const data = { revision: "revision-1", connections: [] as any[], profiles: [] as any[], integrations: [
    { id: "codex-api-key", kind: "codex-api-key", name: "OpenAI API key", authorization_available: true },
    { id: "claude-api-key", kind: "claude-api-key", name: "Anthropic API key", authorization_available: true },
  ] };
  let keySaves = 0;
  let tests = 0;
  let active: any;
  await page.route("**/api/agent-profiles", route => route.fulfill({ json: data }));
  await page.route("**/api/agent-profiles/connection-save", async route => {
    const request = route.request().postDataJSON();
    expect(request.api_key).toBe(secret);
    expect(request.template_id).toBe("claude-api-key");
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
  await page.getByTestId("profile-use-api-key").click();
  await page.getByTestId("profile-key-provider").selectOption("claude-api-key");
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
