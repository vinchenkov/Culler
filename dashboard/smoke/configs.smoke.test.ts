import { afterAll, beforeAll, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { evidencePath, bootSmoke, gotoReady, mc, SMOKE_BEARER, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "config-library", { title: "Library" });
  await seedProject(smoke, "config-garden", { title: "Garden" });
  await gotoReady(smoke);
}, 60000);
afterAll(async () => { if (smoke) await teardown(smoke); });

async function openSettings() {
  await smoke.page.getByTestId("tab-configs").click();
  await smoke.page.getByLabel("Lease timeout (minutes)", { exact: true }).waitFor({ state: "attached" });
  await smoke.page.getByTestId("config-nav-advanced").click();
}

async function saved() {
  await smoke.page.waitForFunction(() => document.querySelector("#configs-save-status")?.textContent?.startsWith("Culler saved the changes."));
}

test("the dashboard persists supported settings and applies the dispatch values", async () => {
  const { page } = smoke;
  await openSettings();
  expect(await page.getByTestId("configs-stats").count()).toBe(0);
  const knobs = page.getByTestId("configs-knobs");
  expect(await knobs.getByTestId("config-knob").count()).toBe(12);
  for (const key of ["container.runtime", "container.base_image", "budgets.dispatch_retries", "git.author_name", "runtimes.bindings", "surfaces.dashboard.auth_token"]) {
    expect(await knobs.locator(`[data-key="${key}"]`).count()).toBe(0);
  }
  const wikiInterval = page.getByLabel("Wiki update interval (minutes)", { exact: true });
  expect(await wikiInterval.inputValue()).toBe("60");
  expect(await knobs.locator('[data-key="timing.wiki_update_interval_s"]').innerText()).not.toContain("seconds");
  await page.getByLabel("Lease timeout (minutes)", { exact: true }).fill("75");
  await wikiInterval.fill("120");
  await page.getByLabel("Backup retention", { exact: true }).fill("8");
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await saved();
  const settings = await mc(["settings", "get"]);
  expect(settings.timeout_minutes).toBe(75);
  expect(settings.wiki_update_interval_s).toBe(7200);
  expect(readFileSync(join(smoke.home, "config.toml"), "utf8")).toContain("backup_retain = 8");
  await page.reload();
  await openSettings();
  expect(await page.getByLabel("Lease timeout (minutes)", { exact: true }).inputValue()).toBe("75");
  expect(await page.getByLabel("Wiki update interval (minutes)", { exact: true }).inputValue()).toBe("120");
  expect(await page.getByLabel("Backup retention", { exact: true }).inputValue()).toBe("8");
}, 30000);

test("the policy editors preserve mandatory restrictions and persist supported rules", async () => {
  const { page } = smoke;
  await page.getByLabel("Additional forbidden environment variables", { exact: true }).fill("PRIVATE_TOKEN");
  await page.getByRole("button", { name: "Add rule", exact: true }).click();
  await page.getByLabel("Rule 1 type", { exact: true }).selectOption("basename-glob");
  await page.getByLabel("Rule 1 pattern", { exact: true }).fill("*.private");
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await saved();
  const cfg = await mc(["config", "show"]);
  const fields = (cfg.families as any[]).flatMap(family => family.knobs);
  const env = fields.find(field => field.key === "runtimes.forbidden_env");
  expect(env.value).toContain("PRIVATE_TOKEN");
  expect(env.value).toEqual(expect.arrayContaining(env.required_values));
  expect(fields.find(field => field.key === "container.additional_blocked_patterns").value).toEqual([{ kind: "basename-glob", pattern: "*.private" }]);
  await page.reload();
  await openSettings();
  expect(await page.getByLabel("Additional forbidden environment variables", { exact: true }).inputValue()).toBe("PRIVATE_TOKEN");
  expect(await page.getByLabel("Rule 1 type", { exact: true }).inputValue()).toBe("basename-glob");
  await page.getByRole("button", { name: "Remove rule 1", exact: true }).click();
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await saved();
}, 30000);

test("a stale save keeps edits and a discard restores the saved value", async () => {
  const { page, server } = smoke;
  await page.getByLabel("Backup retention", { exact: true }).fill("16");
  const result = await fetch(`${server.url}/api/config`, { method: "POST", headers: { ...SMOKE_BEARER, "content-type": "application/json" }, body: JSON.stringify({ changes: [{ key: "timing.backup_retain", expected: 8, value: 14 }] }) });
  expect(result.status).toBe(200);
  await page.getByTestId("configs-search").fill("timeout");
  await page.getByTestId("configs-search").fill("");
  expect(await page.getByLabel("Backup retention", { exact: true }).inputValue()).toBe("16");
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await page.waitForFunction(() => document.querySelector("#configs-save-status")?.textContent?.includes("changed elsewhere"));
  expect(await page.getByLabel("Backup retention", { exact: true }).inputValue()).toBe("16");
  await page.getByRole("button", { name: "Discard changes", exact: true }).click();
  await page.reload();
  await openSettings();
  expect(await page.getByLabel("Backup retention", { exact: true }).inputValue()).toBe("14");
}, 30000);

test("unsaved values survive a periodic read and the editor fits a narrow screen", async () => {
  const { page } = smoke;
  await page.getByLabel("Backup retention", { exact: true }).fill("18");
  await page.getByTestId("configs-search").focus();
  await page.waitForResponse(response => response.url().endsWith("/api/config") && response.request().method() === "GET", { timeout: 20000 });
  expect(await page.getByLabel("Backup retention", { exact: true }).inputValue()).toBe("18");
  await page.screenshot({ path: evidencePath("culler-config-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("culler-config-mobile.png"), fullPage: true });
  await page.getByRole("button", { name: "Discard changes", exact: true }).click();
  expect(await page.getByLabel("Backup retention", { exact: true }).inputValue()).toBe("14");
}, 30000);

test("the Config sidebar preserves drafts and supports keyboard, history, and direct links", async () => {
  const { page, server } = smoke;
  await page.setViewportSize({ width: 1440, height: 960 });
  await openSettings();
  const retention = page.getByLabel("Backup retention", { exact: true });
  const original = await retention.inputValue();
  await retention.fill(String(Number(original) + 1));
  await page.getByTestId("config-nav-agents").click();
  await page.getByTestId("config-section-agents").waitFor();
  expect(await page.getByTestId("config-section-advanced").isVisible()).toBe(false);
  expect(await page.getByTestId("config-nav-agents").getAttribute("aria-current")).toBe("page");
  await page.getByTestId("config-nav-agents").focus();
  await page.keyboard.press("Tab");
  expect(await page.getByTestId("config-nav-projects").evaluate(node => node === document.activeElement)).toBe(true);
  await page.keyboard.press("Enter");
  await page.getByTestId("config-section-projects").waitFor();
  expect(page.url()).toEndWith("#configs/projects");
  await page.getByTestId("config-nav-system").click();
  await page.getByTestId("config-section-system").waitFor();
  await page.goBack();
  await page.getByTestId("config-section-projects").waitFor();
  await page.goForward();
  await page.getByTestId("config-section-system").waitFor();
  await page.getByTestId("config-nav-advanced").click();
  await retention.waitFor();
  expect(await retention.inputValue()).toBe(String(Number(original) + 1));
  await page.getByRole("button", { name: "Discard changes", exact: true }).click();
  expect(await retention.inputValue()).toBe(original);
  await page.getByTestId("config-nav-system").click();
  await page.getByTestId("tab-board").click();
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-section-system").waitFor();
  expect(page.url()).toEndWith("#configs/system");
  await page.reload();
  await page.getByTestId("config-section-system").waitFor();
  expect(await page.getByTestId("config-nav-system").getAttribute("aria-current")).toBe("page");
  await page.goto(`${server.url}#configs`);
  await page.getByTestId("config-section-system").waitFor();
  expect(await page.getByTestId("config-nav-system").getAttribute("aria-current")).toBe("page");
  expect(await page.locator("[data-config-section]:visible").evaluateAll(nodes => nodes.map(node => node.getAttribute("data-config-section")))).toEqual(["system", "agents", "projects", "advanced"]);
}, 30000);

test("each Config section fits desktop and narrow screens in both themes", async () => {
  const { page } = smoke;
  for (const theme of ["light", "dark"]) {
    if (await page.locator("html").getAttribute("data-theme") !== theme) {
      await page.getByTestId("theme-button").click();
    }
    await page.waitForFunction(want => document.documentElement.dataset.theme === want, theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 960 });
      for (const section of ["system", "agents", "projects", "advanced"]) {
        await page.getByTestId(`config-nav-${section}`).click();
        const panel = page.getByTestId(`config-section-${section}`);
        await panel.waitFor();
        expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
        expect(await page.locator('[data-testid^="config-section-"]:visible').count()).toBe(1);
        for (const name of ["system", "agents", "projects", "advanced"]) {
          const link = page.getByTestId(`config-nav-${name}`);
          expect(await link.isVisible()).toBe(true);
          const box = (await link.boundingBox())!;
          expect(box.x).toBeGreaterThanOrEqual(0);
          expect(box.x + box.width).toBeLessThanOrEqual(width);
        }
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.screenshot({ path: evidencePath(`config-sidebar-${section}-${theme}-${width}.png`), fullPage: true });
      }
    }
  }
}, 30000);

test("an invalid draft remains visible and focused after a filtered save", async () => {
  const { page } = smoke;
  await openSettings();
  const retention = page.getByLabel("Backup retention", { exact: true });
  const original = await retention.inputValue();
  let saves = 0;
  await page.route("**/api/config", async route => {
    if (route.request().method() === "POST") saves += 1;
    await route.continue();
  });
  try {
    await retention.fill("1.5");
    await page.getByTestId("configs-search").fill("timeout");
    expect(await retention.count()).toBe(0);
    await page.getByRole("button", { name: "Save changes", exact: true }).click();
    await retention.waitFor();
    expect(await page.getByTestId("configs-search").inputValue()).toBe("");
    expect(await retention.inputValue()).toBe("1.5");
    expect(await retention.evaluate(node => node === document.activeElement)).toBe(true);
    expect(await retention.evaluate((node: HTMLInputElement) => node.validity.valid)).toBe(false);
    expect(saves).toBe(0);
    await page.getByRole("button", { name: "Discard changes", exact: true }).click();
    expect(await retention.inputValue()).toBe(original);
  } finally {
    await page.unroute("**/api/config");
    await page.getByTestId("configs-search").fill("");
    const discard = page.getByRole("button", { name: "Discard changes", exact: true });
    if (await discard.isEnabled()) await discard.click();
  }
}, 30000);

test("Projects and Advanced expose a failed Config read and recover through Retry", async () => {
  const { page } = smoke;
  let failed = true;
  await page.route("**/api/config", async route => {
    if (failed && route.request().method() === "GET") {
      await route.fulfill({ status: 503, json: { error: { message: "Configuration is unavailable." } } });
    } else {
      await route.continue();
    }
  });
  const notice = page.locator("#configs-content-request");
  try {
    for (const section of ["projects", "advanced"]) {
      failed = true;
      await page.getByTestId(`config-nav-${section}`).click();
      await page.getByTestId(`config-section-${section}`).waitFor();
      await notice.waitFor();
      expect(await notice.innerText()).toContain("Configuration is unavailable.");
      const retry = notice.getByRole("button", { name: "Retry", exact: true });
      expect(await retry.isVisible()).toBe(true);
      failed = false;
      await retry.click();
      await notice.waitFor({ state: "hidden" });
      expect(await page.getByTestId(`config-section-${section}`).isVisible()).toBe(true);
    }
  } finally {
    failed = false;
    await page.unroute("**/api/config");
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await notice.waitFor({ state: "hidden" });
  }
}, 30000);

test("System health opens System when the notice has no known conditions", async () => {
  const { page, server } = smoke;
  await page.route("**/api/system/status", route => route.fulfill({ json: {
    complete: false, observed_at: new Date().toISOString(), conditions: [], connections: [],
  } }));
  try {
    await page.goto(`${server.url}#board`);
    await page.getByTestId("system-notices").getByRole("button", { name: "System health", exact: true }).click();
    await page.getByTestId("config-section-system").waitFor();
    expect(page.url()).toEndWith("#configs/system");
    expect(await page.getByTestId("config-nav-system").getAttribute("aria-current")).toBe("page");
  } finally {
    await page.unroute("**/api/system/status");
    await page.reload();
    await page.getByTestId("config-section-system").waitFor();
  }
}, 30000);

test("keyboard focus stays above the Advanced save controls on narrow screens", async () => {
  const { page } = smoke;
  await openSettings();
  try {
    for (const viewport of [{ width: 390, height: 844 }, { width: 720, height: 480 }]) {
      await page.setViewportSize(viewport);
      await page.getByLabel("Backup retention", { exact: true }).fill("18");
      const controls = page.getByTestId("configs-knobs").locator("input, textarea, select, button");
      const count = await controls.count();
      await page.getByTestId("configs-search").focus();
      for (let index = 0; index < count; index += 1) {
        await page.keyboard.press("Tab");
        await page.waitForFunction(() => {
          const input = document.activeElement;
          const bar = document.querySelector(".config-savebar");
          if (!input || !bar || !document.getElementById("configs-knobs")?.contains(input)) return false;
          const bounds = input.getBoundingClientRect();
          return bounds.top >= 0 && bounds.bottom <= bar.getBoundingClientRect().top;
        });
      }
      await page.screenshot({ path: evidencePath(`config-focus-${viewport.width}.png`) });
      await page.getByRole("button", { name: "Discard changes", exact: true }).click();
    }
  } finally {
    const discard = page.getByRole("button", { name: "Discard changes", exact: true });
    if (await discard.isEnabled()) await discard.click();
    await page.setViewportSize({ width: 1440, height: 960 });
  }
}, 30000);

test("long profile and model names remain readable in Agents", async () => {
  const { page } = smoke;
  const profile = "Production research and document verification";
  const model = "research-reasoner-2026-10-extended-context-128000";
  await page.route("**/api/routing", async route => {
    const response = await route.fetch();
    const result = await response.json();
    result.routes = result.routes.map((row: Record<string, unknown>) => ({ ...row, name: profile, model }));
    await route.fulfill({ response, json: result });
  });
  try {
    await page.getByTestId("config-nav-agents").click();
    await page.getByTestId("configs-routing").getByText(profile, { exact: true }).first().waitFor();
    for (const theme of ["light", "dark"]) {
      if (await page.locator("html").getAttribute("data-theme") !== theme) await page.getByTestId("theme-button").click();
      await page.waitForFunction(want => document.documentElement.dataset.theme === want, theme);
      for (const width of [1440, 390]) {
        await page.setViewportSize({ width, height: 960 });
        expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
        expect(await page.getByTestId("configs-routing").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.screenshot({ path: evidencePath(`config-long-names-${theme}-${width}.png`), fullPage: true });
      }
    }
  } finally {
    await page.unroute("**/api/routing");
    await page.setViewportSize({ width: 1440, height: 960 });
    await page.reload();
    await page.getByTestId("config-section-agents").waitFor();
  }
}, 30000);
