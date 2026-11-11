import { afterAll, afterEach, beforeAll, beforeEach, expect, test } from "bun:test";
import { evidencePath, bootSmoke, seedProject, gotoReady, mc, teardown, type Smoke } from "./harness";

let smoke: Smoke;
const profileRoute = "**/api/agent-profiles";
let connections: Array<Record<string,unknown>> = [];

function catalog() {
  return {
    revision: "model-configuration-test", integrations: [],
    connections: [
      { id: "team", name: "Team account", kind: "codex-oauth", access_status: "connected", models: [] },
      { id: "local", name: "Second account", kind: "claude-oauth", access_status: "disconnected", models: [] },
    ],
    profiles: [
      { id: "careful", name: "Careful", connection_id: "team", connection_name: "Team account", model: "review-model:v2", access_status: "connected", enabled: true, roles: ["verifier", "packager"] },
      { id: "daily", name: "Daily", connection_id: "team", connection_name: "Team account", model: "general-model:v3", access_status: "connected", enabled: true, roles: ["strategist", "arbiter", "worker", "scribe", "collie"] },
      { id: "spare", name: "Spare", connection_id: "local", connection_name: "Second account", model: "local/model:v1", access_status: "disconnected", enabled: true, roles: [] },
      { id: "draft", name: "Draft", connection_id: "local", connection_name: "Second account", model: "draft/model:v1", access_status: "disconnected", enabled: false, roles: [] },
    ],
  };
}

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "models-one", { title: "First project" });
  await seedProject(smoke, "models-two", { title: "Second project" });
}, 60000);

beforeEach(async () => {
  await smoke.page.setViewportSize({ width: 1440, height: 900 });
  smoke.page.setDefaultTimeout(5000);
  connections = [
    {id:"connection:team:token_refresh:",group:"connections",name:"Team account",connection_id:"team",state:"ready"},
    {id:"connection:local:connection_access:",group:"connections",name:"Second account",connection_id:"local",state:"ready"},
  ];
  await smoke.page.route("**/api/system/status", route=>route.fulfill({json:{complete:true,observed_at:new Date().toISOString(),connections,conditions:connections.filter(item=>item.state!=="ready")}}));
  await gotoReady(smoke);
});

afterEach(async () => {
  if (await smoke.page.getByTestId("agent-profiles-panel").isVisible()) await smoke.page.getByTestId("profile-close").click();
  if (await smoke.page.locator("dialog.role-dialog[open]").isVisible()) await smoke.page.keyboard.press("Escape");
  await smoke.page.unrouteAll({ behavior: "wait" });
});

afterAll(async () => { await teardown(smoke); }, 30000);

test("model configuration lists only enabled profiles, keeps three profiles compact, and reveals details through the keyboard", async () => {
  const { page } = smoke;
  await page.route(profileRoute, (route) => route.fulfill({ json: catalog() }));
  await page.getByTestId("tab-pipeline").click();
  await page.getByRole("button", { name: "Model configuration", exact: true }).click();
  const panel = page.getByTestId("agent-profiles-panel");
  await page.getByTestId("profile-card").last().waitFor();
  expect(await panel.getByRole("heading", { name: "Model configuration", exact: true }).isVisible()).toBe(true);
  expect(await page.getByTestId("profile-card").count()).toBe(3);
  expect(await panel.locator(".profile-count").innerText()).toBe("3 profiles");
  expect(await page.getByRole("article", { name: "Draft", exact: true }).count()).toBe(0);
  expect(await panel.getByText("Disabled", { exact: true }).count()).toBe(0);
  expect(await page.getByTestId("profile-attention-count").count()).toBe(0);
  expect(await page.getByTestId("profile-card").last().evaluate((node) => node.getBoundingClientRect().bottom <= innerHeight)).toBe(true);
  expect(await panel.getByText("https://endpoint.example/models", { exact: true }).isVisible()).toBe(false);
  expect(await page.getByRole("article", { name: "Daily", exact: true }).getByText("Scribe", { exact: true }).isVisible()).toBe(true);
  await page.screenshot({ path: evidencePath("model-configuration-desktop.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await panel.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("model-configuration-narrow.png") });

  const details = page.getByRole("button", { name: "Details for Careful", exact: true });
  await details.focus();
  await page.keyboard.press("Enter");
  expect(await details.getAttribute("aria-expanded")).toBe("true");
  const facts = page.getByRole("article", { name: "Careful", exact: true }).locator(".profile-details");
  expect(await facts.locator("dt").allInnerTexts()).toEqual(["Connection", "Connection access", "Effort"]);
  expect(await facts.locator("dd").allInnerTexts()).toEqual(["Team account", "Connected", "Default"]);
  expect(await page.getByRole("article", { name: "Careful", exact: true }).getByTestId("profile-status").innerText()).toBe("Connected");
  expect(await details.evaluate((node) => node === document.activeElement)).toBe(true);
  await page.keyboard.press("Enter");
  expect(await details.getAttribute("aria-expanded")).toBe("false");
  expect(await facts.isVisible()).toBe(false);

  await page.getByRole("button", { name: "Edit Careful", exact: true }).click();
  expect(await page.getByTestId("profile-model").inputValue()).toBe("review-model:v2");
  expect(await page.getByTestId("profile-connection_id").inputValue()).toBe("team");
  await page.keyboard.press("Escape");
  expect(await page.getByTestId("pipeline-profiles").evaluate((node) => node === document.activeElement)).toBe(true);
}, 30000);

test("each assignment choice offers Edit and preserves the role assignment", async () => {
  const { page } = smoke;
  const data: any = catalog();
  data.profiles[0].effort = "high";
  data.profiles[1].effort = "medium";
  let binding = "daily";
  let operation: any;
  let saved: any;
  const assignments: any[] = [];
  await page.route(profileRoute, route => route.fulfill({ json: data }));
  await page.route("**/api/routing", async route => {
    if (route.request().method() === "POST") {
      const assignment = route.request().postDataJSON();
      assignments.push(assignment);
      binding = assignment.binding;
    }
    await route.fulfill({ json: { routes: [{ role: "worker", binding, harness: "codex", model: data.profiles.find((profile: any) => profile.id === binding).model }], bindings: [] } });
  });
  await page.route("**/api/agent-profiles/models", route => route.fulfill({ json: { connection_id: "team", models: [
    { id: "review-model:v2", effort_levels: ["low", "medium", "high"], effort_support_known: true },
    { id: "general-model:v3", effort_levels: ["low", "medium", "high"], effort_support_known: true },
  ] } }));
  await page.route("**/api/agent-profiles/operation-start", async route => {
    operation = route.request().postDataJSON();
    await route.fulfill({ json: { ...operation, status: "pending" } });
  });
  await page.route("**/api/agent-profiles/operation-status", route => route.fulfill({ json: { ...operation, status: "succeeded" } }));
  await page.route("**/api/agent-profiles/save", async route => {
    saved = route.request().postDataJSON();
    expect(saved.id).toBe("daily");
    expect(saved.test_id).toBe(operation.operation_id);
    data.profiles[1] = { ...data.profiles[1], ...saved };
    await route.fulfill({ json: { ...data, profile_id: saved.id } });
  });
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-agents").click();
  await page.getByTestId("config-route-worker").click();
  await page.getByTestId("topology-side").getByTestId("topology-change-profile").click();
  const panel = page.getByTestId("agent-profiles-panel");
  const assignment = page.getByTestId("profile-assignment");
  await assignment.waitFor();
  expect(await assignment.getByRole("button", { name: /^Edit / }).count()).toBe(3);
  expect(await page.getByTestId("profile-assignment-daily").isChecked()).toBe(true);
  await page.screenshot({ path: evidencePath("profile-assignment-edit-desktop.png"), animations: "disabled" });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await panel.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  expect(await assignment.getByRole("button", { name: /^Edit / }).evaluateAll(nodes => nodes.every(node => {
    const box = node.getBoundingClientRect();
    return box.left >= 0 && box.right <= innerWidth;
  }))).toBe(true);
  await page.screenshot({ path: evidencePath("profile-assignment-edit-narrow.png"), animations: "disabled" });
  await page.getByRole("button", { name: "Edit Careful", exact: true }).focus();
  await page.keyboard.press("Enter");
  await page.getByTestId("profile-effort").locator('option[value="high"]').waitFor({ state: "attached" });
  expect(await page.getByTestId("profile-name").inputValue()).toBe("Careful");
  expect(await page.getByTestId("profile-model").inputValue()).toBe("review-model:v2");
  expect(await page.getByTestId("profile-effort").inputValue()).toBe("high");
  expect(assignments).toEqual([]);
  await page.getByTestId("profile-back").click();
  expect(await panel.getByRole("heading", { name: "Worker agent profile", exact: true }).isVisible()).toBe(true);
  expect(await page.getByTestId("profile-assignment-daily").isChecked()).toBe(true);
  await page.getByRole("button", { name: "Edit Daily", exact: true }).click();
  expect(await page.getByTestId("profile-effort").inputValue()).toBe("medium");
  await page.getByTestId("profile-effort").selectOption("high");
  await page.getByTestId("profile-test").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="profile-save"]') as HTMLButtonElement)?.disabled);
  await page.getByTestId("profile-save").click();
  await assignment.waitFor();
  expect(saved.effort).toBe("high");
  expect(await panel.getByRole("heading", { name: "Worker agent profile", exact: true }).isVisible()).toBe(true);
  expect(await page.getByTestId("profile-assignment-daily").isChecked()).toBe(true);
  expect(await page.getByTestId("profile-notice").innerText()).toBe("The agent profile Daily is saved. The Worker role uses this agent profile.");
  expect(assignments).toEqual([]);
  await assignment.getByText("review-model:v2", { exact: true }).click();
  await page.getByTestId("profile-notice").getByText("The Worker role now uses this agent profile across all Projects.", { exact: true }).waitFor();
  expect(assignments).toEqual([{ role: "worker", binding: "careful", expected: "daily" }]);
  expect(await page.getByTestId("profile-assignment-careful").isChecked()).toBe(true);
}, 30000);

test("a subscription connection offers its models and keeps a manual entry after a failed list", async () => {
  const { page } = smoke;
  let lists = 0;
  await page.route(profileRoute, (route) => route.fulfill({ json: catalog() }));
  await page.route("**/api/agent-profiles/models", async (route) => {
    lists++;
    expect(route.request().postDataJSON()).toEqual({ connection_id: "team" });
    if (lists === 1) await route.fulfill({ status: 400, json: { error: { code: "domain-rejection", message: "The provider model list returned HTTP 502. Retry later." } } });
    else await route.fulfill({ json: { connection_id: "team", models: [{ id: "gpt-6-sol", name: "GPT-6-Sol" }, { id: "general-model:v3", name: "general-model:v3" }] } });
  });
  await page.getByTestId("tab-pipeline").click();
  await page.getByRole("button", { name: "Model configuration", exact: true }).click();
  await page.getByTestId("profile-create").click();
  await page.getByTestId("profile-use-existing").click();
  await page.getByTestId("profile-connection_id").selectOption("team");
  await page.getByTestId("profile-model-list-error").waitFor();
  expect(await page.getByTestId("profile-model-list-error").innerText()).toBe("The provider model list returned HTTP 502. Retry later.");
  await page.getByTestId("profile-model").fill("manual-model");
  await page.getByTestId("profile-model-retry").click();
  const model = page.getByTestId("profile-model");
  await model.locator('option[value="gpt-6-sol"]').waitFor({ state: "attached" });
  expect(await model.evaluate((node) => node.tagName)).toBe("SELECT");
  expect(await model.locator("option").allInnerTexts()).toEqual(["Select a model", "manual-model (not offered)", "GPT-6-Sol (gpt-6-sol)", "general-model:v3"]);
  expect(await page.getByRole("alert").filter({ hasText: "does not offer the model manual-model" }).isVisible()).toBe(true);
  await model.selectOption("gpt-6-sol");
  expect(await model.inputValue()).toBe("gpt-6-sol");
  expect(await page.getByText("does not offer the model").count()).toBe(0);
  expect(await page.getByTestId("profile-review").textContent()).toContain("gpt-6-sol");
  await page.getByTestId("agent-profiles-panel").screenshot({ path: evidencePath("model-configuration-model-list.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.getByTestId("agent-profiles-panel").evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
  await page.getByTestId("agent-profiles-panel").screenshot({ path: evidencePath("model-configuration-model-list-narrow.png") });
  expect(lists).toBe(2);
}, 30000);

test("enabled profiles with failures appear first and retain an action for the correct connection", async () => {
  const { page } = smoke;
  const data = catalog();
  connections[1] = {...connections[1],state:"stopped",condition:"The endpoint is unavailable.",action:"Review connection"};
  connections.push({id:"connection:team:model_access:daily",group:"connections",name:"Team account",connection_id:"team",profile_id:"daily",roles:data.profiles[1].roles,state:"stopped",condition:"The model test failed.",action:"Review connection"});
  await page.evaluate(()=>window.dispatchEvent(new Event("focus")));
  // The failed connection of the unused Spare profile is not an issue, so the notice shows the one remaining issue.
  const notice = page.getByTestId("system-notices");
  const issue = notice.locator('[data-condition-id="connection:team:model_access:daily"]');
  await issue.waitFor();
  expect(await issue.getByRole("button", {name:"Review connection",exact:true}).isVisible()).toBe(true);
  expect(await notice.innerText()).not.toContain("Second account");
  if (process.env.MC_UX_EVIDENCE) await page.screenshot({path:`${process.env.MC_UX_EVIDENCE}/unused-failure.png`});
  await page.route(profileRoute, (route) => route.fulfill({ json: data }));
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-agents").click();
  await page.getByRole("button", { name: "Model configuration", exact: true }).click();
  await page.getByTestId("profile-attention-count").waitFor();
  expect(await page.getByTestId("profile-attention-count").innerText()).toBe("2 need attention");
  expect(await page.getByTestId("profile-card").evaluateAll((nodes) => nodes.map((node) => node.getAttribute("aria-label")))).toEqual(["Daily", "Spare", "Careful"]);
  expect(await page.getByRole("article", { name: "Daily", exact: true }).getByTestId("profile-status").innerText()).toContain("Model unavailable");
  await page.screenshot({ path: evidencePath("model-configuration-attention.png") });
  await page.getByRole("button", { name: "Review connection Spare", exact: true }).click();
  expect(await page.getByTestId("profile-connection_id").inputValue()).toBe("local");
  expect(await page.getByTestId("profile-base_url").count()).toBe(0);
}, 30000);

test("an incomplete or failed read stays distinct from an empty profile list", async () => {
  const { page } = smoke;
  let mode = "delay";
  let release!: () => void;
  let requested!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const seen = new Promise<void>((resolve) => { requested = resolve; });
  await page.route(profileRoute, async (route) => {
    if (mode === "delay") { requested(); await gate; }
    if (mode === "error") await route.fulfill({ status: 500, json: { error: { message: "The model configuration is unavailable." } } });
    else await route.fulfill({ json: mode === "ready" ? catalog() : { ...catalog(), profiles: [] } });
  });
  try {
    await page.getByTestId("tab-pipeline").click();
    await page.getByTestId("pipeline-profiles").click();
    await seen;
    expect(await page.getByText("Loading model configuration…", { exact: true }).isVisible()).toBe(true);
    expect(await page.getByTestId("profile-empty").count()).toBe(0);
    release();
    await page.getByTestId("profile-empty").waitFor();
    expect(await page.getByTestId("profile-create").isEnabled()).toBe(true);
    await page.getByTestId("profile-close").click();
    mode = "error";
    await page.getByTestId("pipeline-profiles").click();
    await page.getByTestId("profile-error").waitFor();
    expect(await page.getByTestId("profile-empty").count()).toBe(0);
    expect(await page.getByTestId("profile-create").count()).toBe(0);
    mode = "ready";
    await page.getByTestId("profile-refresh").click();
    await page.getByTestId("profile-card").last().waitFor();
    expect(await page.getByTestId("profile-card").count()).toBe(3);
    expect(await page.getByTestId("profile-error").count()).toBe(0);
  } finally { release(); }
}, 30000);

test("long model identifiers fit a narrow panel and the close control stays available", async () => {
  const { page } = smoke;
  const data = catalog();
  data.profiles.push(...Array.from({ length: 5 }, (_, index) => ({
    ...data.profiles[0]!, id: `extra-${index}`, name: `Extra profile ${index + 1}`,
    model: `team/long-model-identifier-without-spaces-${index + 1}-extended-context`,
  })));
  await page.route(profileRoute, (route) => route.fulfill({ json: data }));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-agents").click();
  await page.getByTestId("configs-profiles").click();
  const panel = page.getByTestId("agent-profiles-panel");
  await page.getByTestId("profile-card").last().waitFor();
  await page.getByRole("button", { name: "Details for Spare", exact: true }).click();
  await page.getByRole("article", { name: "Spare", exact: true }).locator(".profile-details").scrollIntoViewIfNeeded();
  expect(await panel.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
  const close = await page.getByTestId("profile-close").boundingBox();
  expect(close).not.toBeNull();
  expect(close!.y).toBeGreaterThanOrEqual(0);
  expect(close!.y + close!.height).toBeLessThanOrEqual(844);
  await page.getByTestId("profile-close").click();
  expect(await page.getByTestId("configs-profiles").evaluate((node) => node === document.activeElement)).toBe(true);
}, 30000);

test("a delayed response from a closed panel preserves the current model configuration", async () => {
  const { page } = smoke;
  let requests = 0;
  let release!: () => void;
  let requested!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const seen = new Promise<void>((resolve) => { requested = resolve; });
  await page.route(profileRoute, async (route) => {
    requests++;
    if (requests === 1) {
      requested();
      await gate;
      await route.fulfill({ headers: { "x-profile-read": "delayed" }, json: { ...catalog(), profiles: [] } });
    } else await route.fulfill({ json: catalog() });
  });
  try {
    await page.getByTestId("tab-pipeline").click();
    await page.getByTestId("pipeline-profiles").click();
    await seen;
    const cancelled = page.waitForEvent("requestfailed", request => new URL(request.url()).pathname === "/api/agent-profiles");
    await page.getByTestId("profile-close").click();
    expect((await cancelled).failure()?.errorText).toContain("ABORTED");
    await page.getByTestId("pipeline-profiles").click();
    await page.getByTestId("profile-card").last().waitFor();
    release();
    await page.getByRole("button", { name: "Details for Careful", exact: true }).click();
    expect(await page.getByTestId("profile-card").count()).toBe(3);
    expect(await page.getByTestId("profile-empty").count()).toBe(0);
  } finally { release(); }
}, 30000);

test("a model list response preserves the profile name draft, focus, selection, and body position", async () => {
  const { page } = smoke;
  let release!: () => void;
  let requested!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  const seen = new Promise<void>(resolve => { requested = resolve; });
  await page.route(profileRoute, route => route.fulfill({ json: catalog() }));
  await page.route("**/api/agent-profiles/models", async route => {
    requested();
    await gate;
    await route.fulfill({ json: { connection_id: "team", models: [{ id: "general-model:v3", name: "General model" }] } });
  });
  try {
    await page.setViewportSize({ width: 390, height: 667 });
    await page.getByTestId("tab-pipeline").click();
    await page.getByTestId("pipeline-profiles").click();
    await page.getByTestId("profile-create").click();
    await page.getByTestId("profile-use-existing").click();
    await page.getByTestId("profile-connection_id").selectOption("team");
    await seen;
    const name = page.getByTestId("profile-name");
    await name.fill("Selection profile");
    await name.evaluate((node: HTMLInputElement) => node.setSelectionRange(2, 8, "backward"));
    const body = page.getByTestId("agent-profiles-panel").locator(".ui-dialog-body");
    const scrollTop = await body.evaluate(node => node.scrollTop);
    release();
    await page.getByTestId("profile-model").locator('option[value="general-model:v3"]').waitFor({ state: "attached" });
    expect(await name.inputValue()).toBe("Selection profile");
    expect(await name.evaluate((node: HTMLInputElement) => [node === document.activeElement, node.selectionStart, node.selectionEnd, node.selectionDirection]))
      .toEqual([true, 2, 8, "backward"]);
    expect(await body.evaluate(node => node.scrollTop)).toBe(scrollTop);
  } finally { release(); }
}, 30000);

test("effort survives edit and save and each change requires a new model test", async () => {
  const { page } = smoke;
  const data: any = catalog();
  data.profiles[0].effort = "high";
  let active: any;
  const tests: any[] = [];
  let saved: any;
  await page.route(profileRoute, route => route.fulfill({ json: data }));
  await page.route("**/api/agent-profiles/models", route => route.fulfill({ json: { connection_id: "team", models: [
    { id: "review-model:v2", name: "Review model", effort_levels: ["low", "high", "max"], effort_support_known: true },
    { id: "general-model:v3", name: "General model", effort_levels: ["low", "medium", "high"], effort_support_known: false },
  ] } }));
  await page.route("**/api/agent-profiles/operation-start", async route => {
    active = route.request().postDataJSON();
    tests.push(active.profile);
    await route.fulfill({ json: { ...active, status: "pending", started_at: new Date().toISOString() } });
  });
  await page.route("**/api/agent-profiles/operation-status", route => route.fulfill({ json: { ...active, status: "succeeded", message: "The model test succeeded." } }));
  await page.route("**/api/agent-profiles/save", async route => {
    saved = route.request().postDataJSON();
    data.profiles[0] = { ...data.profiles[0], ...saved };
    await route.fulfill({ json: { ...data, profile_id: "careful" } });
  });
  await page.getByTestId("tab-pipeline").click();
  await page.getByTestId("pipeline-profiles").click();
  await page.getByRole("button", { name: "Edit Careful", exact: true }).click();
  const effort = page.getByTestId("profile-effort");
  await effort.locator('option[value="max"]').waitFor({ state: "attached" });
  expect(await effort.inputValue()).toBe("high");
  expect(await effort.locator("option").allInnerTexts()).toEqual(["Default", "Low", "High", "Max"]);
  await page.getByTestId("profile-test").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="profile-save"]') as HTMLButtonElement)?.disabled);
  expect(tests[0].effort).toBe("high");
  await effort.selectOption("max");
  expect(await page.getByTestId("profile-save").isDisabled()).toBe(true);
  await page.getByTestId("profile-model").selectOption("general-model:v3");
  expect(await effort.inputValue()).toBe("");
  expect(await effort.locator('option[value="max"]').count()).toBe(0);
  expect(await page.getByText("The model lists no effort levels.", { exact: false }).isVisible()).toBe(true);
  await effort.selectOption("low");
  await page.getByTestId("profile-model").selectOption("review-model:v2");
  expect(await effort.inputValue()).toBe("");
  await effort.selectOption("max");
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.getByTestId("agent-profiles-panel").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  await effort.scrollIntoViewIfNeeded();
  await page.screenshot({ path: evidencePath("model-configuration-effort-narrow.png") });
  await page.getByTestId("profile-test").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="profile-save"]') as HTMLButtonElement)?.disabled);
  expect(tests[1].effort).toBe("max");
  await page.getByTestId("profile-save").click();
  await page.getByRole("button", { name: "Edit Careful", exact: true }).waitFor();
  expect(saved.effort).toBe("max");
  expect(saved.test_id).toBe(active.operation_id);
  await page.getByRole("button", { name: "Edit Careful", exact: true }).click();
  expect(await effort.inputValue()).toBe("max");
  await effort.selectOption("");
  expect(await page.getByTestId("profile-save").isDisabled()).toBe(true);
  await page.getByTestId("profile-test").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="profile-save"]') as HTMLButtonElement)?.disabled);
  expect(tests[2]).not.toHaveProperty("effort");
}, 30000);
