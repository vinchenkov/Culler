import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let ready = false;
let complete = true;
let releaseStatus: Promise<void> | null = null;
const failed = {
  id: "connection:team:model_access:review", group: "connections", name: "Team account",
  connection_id: "team", profile_id: "review", profile_name: "Review", model: "review-model:v2", state: "stopped",
  roles: ["verifier", "packager"], action: "Review connection", http_status: 403,
  latest_failure_at: "2026-10-03T01:42:10Z", diagnostic_id: 1,
};
const catalog = {
  revision: "board-connection-test", integrations: [],
  connections: [{ id: "team", name: "Team account", kind: "claude-oauth", access_status: "connected", models: [] }],
  profiles: [
    { id: "daily", name: "Daily", connection_id: "team", connection_name: "Team account", model: "general-model:v3", enabled: true, access_status: "connected", roles: ["worker"] },
    { id: "review", name: "Review", connection_id: "team", connection_name: "Team account", model: "review-model:v2", enabled: true, access_status: "connected", roles: failed.roles },
  ],
};

beforeAll(async () => {
  smoke = await bootSmoke();
  smoke.page.setDefaultTimeout(7000);
  await smoke.page.route("**/api/agent-profiles", route => route.fulfill({ json: catalog }));
  await smoke.page.route("**/api/projects/readiness", route => route.fulfill({ json: { ready, profiles: catalog.profiles, issues: ["Complete access for Review in Model configuration."] } }));
  await smoke.page.route("**/api/system/status", async route => {
    if (releaseStatus) await releaseStatus;
    await route.fulfill({ json: {
      complete, observed_at: new Date().toISOString(),
      connections: complete ? [{ ...failed, state: ready ? "ready" : "stopped" }] : [],
      conditions: complete && !ready ? [failed] : [],
    } });
  });
  await gotoReady(smoke);
}, 60000);
afterAll(async () => { await teardown(smoke); });

async function refreshStatus() {
  const response = smoke.page.waitForResponse("**/api/system/status");
  await smoke.page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await (await response).finished();
}

async function capture(name: string) {
  await smoke.page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
  await smoke.page.screenshot({ path: evidencePath(`board-connection-${name}.png`), fullPage: true });
}

test("the empty Board limits agent setup to one message and action", async () => {
  const { page } = smoke;
  await page.getByTestId("board-add-project").waitFor();
  const setup = page.getByTestId("project-agents");
  await setup.waitFor();
  expect(await setup.innerText()).toBe("Agent configuration requires attention before agents can start.\n\nModel configuration");
  expect(await setup.locator("ul").count()).toBe(0);
  await setup.getByRole("button", { name: "Model configuration", exact: true }).click();
  await page.getByTestId("profile-card").last().waitFor();
  expect(await page.getByTestId("profile-card").count()).toBe(2);
  await page.getByTestId("profile-close").click();
  await capture("empty-attention");
  ready = true;
  await page.reload();
  await page.getByTestId("board-add-project").waitFor();
  await page.waitForFunction(() => !document.querySelector('#board-project[aria-busy="true"]'));
  expect(await setup.count()).toBe(0);
  expect(await page.getByTestId("system-notices").isVisible()).toBe(false);
  await capture("empty-ready");
}, 30000);

test("the Board keeps a shared failure notice and only the selected Project's tasks", async () => {
  const { page } = smoke;
  ready = false;
  for (const id of ["alpha", "beta"]) {
    await seedProject(smoke, id);
    await mc(["task", "add", `${id} task`, "--project", id]);
  }
  await page.reload();
  await page.getByTestId("board-card").first().waitFor();
  for (const id of ["alpha", "beta"]) {
    await selectProject(smoke, id);
    await page.getByTestId("board-card").filter({ hasText: `${id} task` }).waitFor();
    expect(await page.getByTestId("board-card").count()).toBe(1);
    expect(await page.getByTestId("project-agents").count()).toBe(0);
    expect(await page.getByTestId("system-notices").innerText()).toContain("2 pipeline roles cannot start.");
  }
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    expect(await page.getByTestId("board-columns").isVisible()).toBe(true);
    expect(await page.getByTestId("system-notices").getByRole("button", { name: "View details" }).isVisible()).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await capture(`board-${width}`);
  }
}, 30000);

test("issue details identify the model and open the correct profile for repair", async () => {
  const { page } = smoke;
  const notice = page.getByTestId("system-notices");
  expect(await notice.innerText()).toContain("Team account / Review");
  await notice.getByRole("button", { name: "View details" }).click();
  const dialog = page.getByRole("dialog", { name: "Issue details", exact: true });
  expect(await dialog.locator(".system-context").innerText()).toContain("Agent profile\nReview");
  expect(await dialog.locator(".system-context").innerText()).toContain("review-model:v2");
  expect(await dialog.locator(".system-context").innerText()).toContain("verifier, packager");
  expect(await dialog.innerText()).toContain("The provider rejected access to this model.");
  expect(await dialog.innerText()).not.toContain("general-model:v3");
  expect(await dialog.locator("details").getAttribute("open")).toBeNull();
  for (const width of [1440, 320]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await dialog.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
    await capture(`details-${width}`);
  }
  await dialog.getByText("Technical details", { exact: true }).click();
  expect(await dialog.locator(".system-facts").innerText()).toContain("Profile identifier\nreview");
  expect(await dialog.locator(".system-facts").innerText()).toContain("403");
  expect(await dialog.locator(".system-facts").innerText()).toContain("Oct");
  expect(await dialog.getByText("Roles that use this profile", { exact: true }).count()).toBe(1);
  await dialog.getByRole("button", { name: "Review connection", exact: true }).click();
  await page.getByTestId("profile-model").waitFor();
  expect(await page.getByTestId("profile-model").inputValue()).toBe("review-model:v2");
  expect(await page.getByTestId("profile-connection_id").inputValue()).toBe("team");
  await page.getByTestId("profile-close").click();
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-agents").click();
  await page.getByTestId("configs-profiles").click();
  await page.getByTestId("profile-card").last().waitFor();
  const facts = async (name: string) => {
    await page.getByRole("button", { name: `Details for ${name}`, exact: true }).click();
    const list = page.getByRole("article", { name, exact: true }).locator(".profile-details");
    return { terms: await list.locator("dt").allInnerTexts(), values: await list.locator("dd").allInnerTexts() };
  };
  const daily = await facts("Daily");
  expect(daily.terms).toEqual(["Connection", "Connection access", "Effort"]);
  expect(daily.values[0]).toBe("Team account");
  const review = await facts("Review");
  expect(review.terms).toEqual(["Connection", "Connection access", "Effort"]);
  expect(review.values).toEqual(["Team account", "Model unavailable", "Default"]);
  await capture("profiles");
  await page.getByTestId("profile-close").click();
  await page.getByTestId("tab-board").click();
}, 30000);

test("a delayed or incomplete status preserves details until confirmed recovery", async () => {
  const { page } = smoke;
  const notice = page.getByTestId("system-notices");
  let release!: () => void;
  releaseStatus = new Promise<void>(resolve => { release = resolve; });
  const request = page.waitForRequest("**/api/system/status");
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await request;
  try {
    await notice.getByRole("button", { name: "View details" }).click();
    const dialog = page.getByRole("dialog", { name: "Issue details", exact: true });
    expect(await dialog.locator(".system-context").innerText()).toContain("Agent profile\nReview");
    expect(await dialog.locator(".system-context").innerText()).toContain("review-model:v2");
    complete = false;
    releaseStatus = null;
    release();
    await dialog.getByText("Status unavailable", { exact: true }).waitFor();
    expect(await dialog.locator(".system-context").innerText()).toContain("verifier, packager");
    await capture("unavailable");
    complete = true;
    ready = true;
    await refreshStatus();
    await dialog.getByText("Connected", { exact: true }).waitFor();
    expect(await notice.isVisible()).toBe(false);
    expect(await dialog.getByRole("button", { name: "Review connection", exact: true }).count()).toBe(0);
    expect(await dialog.locator(".system-detail-current").innerText()).not.toContain("rejected");
    await capture("recovered");
    await page.keyboard.press("Escape");
    expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
  } finally { releaseStatus = null; release(); }
}, 30000);
