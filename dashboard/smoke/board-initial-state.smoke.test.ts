import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, mc, seedProject, SMOKE_TOKEN, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => {
  smoke = await bootSmoke();
  await smoke.page.clock.install();
}, 60000);
afterAll(async () => { await teardown(smoke); });

function gate() {
  let release!: () => void;
  const promise = new Promise<void>(resolve => { release = resolve; });
  return { promise, release };
}

// The Board shows its column structure before the first response.
async function expectSkeleton(region: string) {
  const host = smoke.page.getByTestId(region);
  expect(await host.locator(".col h3").count()).toBe(5);
  const cols = host.locator(".col");
  for (let i = 0; i < 5; i++) {
    const cards = cols.nth(i).getByTestId("board-skeleton-card");
    expect(await cards.count()).toBeGreaterThan(0);
    expect(await cards.first().isVisible()).toBe(true);
    expect(await cards.first().getAttribute("aria-hidden")).toBe("true");
  }
  expect(await host.locator('[role="status"]').count()).toBe(0);
  expect(await smoke.page.getByTestId("board-pane").getAttribute("aria-busy")).toBe("true");
}

async function settled() {
  await smoke.page.waitForFunction(() => document.body.dataset.ready
    && !document.querySelector('#board-pane[aria-busy="true"], #board-pane [aria-busy="true"], #lease-badge[aria-busy="true"]'));
  await smoke.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}

test("The Board shows its structure until the registry identifies an empty Project list", async () => {
  const { page, server } = smoke;
  const script = gate(), registry = gate(), records = gate(), readiness = gate();
  const registryStarted = gate();
  const recordPaths = ["/api/tasks", "/api/leases", "/api/settings", "/api/projects", "/api/packets"];
  await page.route("**/app.js", async route => { await script.promise; await route.continue(); });
  await page.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/system/status") {
      await route.fulfill({ json: { complete: true, conditions: [], connections: [] } });
      return;
    }
    if (path === "/api/pause") { registryStarted.release(); await registry.promise; }
    if (recordPaths.includes(path)) await records.promise;
    if (path === "/api/projects/readiness") await readiness.promise;
    if (path === "/api/tasks") {
      await route.fulfill({ status: 503, json: { error: { message: "Task data is unavailable." } } });
    } else await route.continue();
  });
  try {
    await page.goto(`${server.url}#token=${SMOKE_TOKEN}`, { waitUntil: "commit" });
    await page.getByTestId("board-columns").waitFor();
    await expectSkeleton("board-columns");
    expect(await page.locator("#board-heading").isVisible()).toBe(true);
    expect(await page.getByTestId("board-columns").isVisible()).toBe(true);
    expect(await page.getByTestId("board-stats").isVisible()).toBe(true);
    await page.evaluate(() => {
      (window as any).boardColumnsAppeared = false;
      new MutationObserver(() => {
        const columns = document.getElementById("board-columns")!;
        if (columns.getBoundingClientRect().height > 0) (window as any).boardColumnsAppeared = true;
      }).observe(document.getElementById("board-pane")!, { subtree: true, attributes: true, childList: true });
    });
    script.release();
    await page.waitForSelector("body[data-ready]");
    await registryStarted.promise;
    expect(await page.getByTestId("board-pending").isVisible()).toBe(true);
    expect(await page.locator("#page-status").count()).toBe(0);
    await expectSkeleton("board-columns");
    expect(await page.getByTestId("board-add-project").count()).toBe(0);
    const before = await page.locator("header.topbar").boundingBox();
    await page.screenshot({ path: evidencePath("collie-board-initial-pending.png"), fullPage: true });
    registry.release();
    await page.getByTestId("board-add-project").waitFor({ state: "attached" });
    expect(await page.getByTestId("board-add-project").isVisible()).toBe(true);
    expect(await page.locator("#page-status").count()).toBe(0);
    const responses = Promise.all([...recordPaths, "/api/projects/readiness"].map(path =>
      page.waitForResponse(response => new URL(response.url()).pathname === path)));
    records.release();
    readiness.release();
    await responses;
    await page.getByTestId("board-add-project").waitFor();
    expect(await page.getByTestId("board-pending").isVisible()).toBe(false);
    expect(await page.getByTestId("board-columns").isVisible()).toBe(false);
    expect(await page.getByTestId("board-pane").getAttribute("aria-busy")).toBe("false");
    expect(await page.getByTestId("board-project").getByRole("heading", { name: "Add your first Project" }).isVisible()).toBe(true);
    expect(await page.getByTestId("board-project").getAttribute("aria-busy")).toBe("false");
    const after = await page.locator("header.topbar").boundingBox();
    await page.screenshot({ path: evidencePath("collie-board-initial-empty.png"), fullPage: true });
    for (const dimension of ["x", "y", "width", "height"] as const) {
      expect(Math.abs(before![dimension] - after![dimension])).toBeLessThanOrEqual(1);
    }
    expect((await page.getByTestId("board-pane").boundingBox())!.height).toBeGreaterThanOrEqual(440);
    for (const title of ["Describe your vision", "Agents work", "Stay in the loop"]) {
      expect(await page.getByText(title, { exact: true }).isVisible()).toBe(true);
    }
    for (const viewport of [{ width: 1280, height: 720 }, { width: 1440, height: 800 }, { width: 1728, height: 960 }]) {
      await page.setViewportSize(viewport);
      const card = (await page.locator(".project-welcome").boundingBox())!;
      const chat = (await page.getByTestId("board-command").boundingBox())!;
      expect(chat.y).toBeGreaterThanOrEqual(card.y + card.height + 16);
      expect(card.height).toBeCloseTo(Math.min(660, chat.y - card.y - 16), 0);
      expect(chat.y + chat.height).toBeLessThanOrEqual(viewport.height);
      expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeLessThanOrEqual(viewport.height);
      await page.screenshot({ path: evidencePath(`collie-welcome-desktop-${viewport.width}.png`), fullPage: true });
    }
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await page.screenshot({ path: evidencePath("collie-board-initial-empty-narrow.png"), fullPage: true });
    await settled();
    expect(await page.getByTestId("board-add-project").isVisible()).toBe(true);
    expect(await page.getByTestId("error").isVisible()).toBe(false);
    expect(await page.locator("#board-columns-request").isVisible()).toBe(false);
    expect(await page.evaluate(() => (window as any).boardColumnsAppeared)).toBe(true);
  } finally {
    script.release(); registry.release(); records.release(); readiness.release();
    await page.unrouteAll({ behavior: "wait" });
    await page.setViewportSize({ width: 1280, height: 720 });
  }
}, 30000);

test("An empty Board omits task reads on refresh and prefetch", async () => {
  const { page } = smoke;
  const counts = new Map<string, number>();
  await page.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname;
    counts.set(path, (counts.get(path) ?? 0) + 1);
    await route.continue();
  });
  try {
    const initial = Promise.all(["/api/tasks", "/api/packets", "/api/projects", "/api/projects/readiness"].map(path =>
      page.waitForResponse(response => new URL(response.url()).pathname === path)));
    await page.reload();
    await initial;
    await page.getByTestId("board-add-project").waitFor();
    await settled();
    const before = new Map(counts);
    const refreshed = page.waitForResponse(response => response.url().endsWith("/api/pause"));
    await page.clock.fastForward(11000);
    await refreshed;
    await settled();
    expect(counts.get("/api/projects/readiness")).toBeGreaterThan(before.get("/api/projects/readiness")!);
    for (const path of ["/api/tasks", "/api/packets", "/api/projects"]) expect(counts.get(path)).toBe(before.get(path));
    await page.getByTestId("tab-board").dispatchEvent("pointerenter");
    await page.getByTestId("tab-board").focus();
    for (const path of ["/api/tasks", "/api/packets", "/api/projects"]) expect(counts.get(path)).toBe(before.get(path));
    expect(await page.getByTestId("board-add-project").isVisible()).toBe(true);
  } finally { await page.unrouteAll({ behavior: "wait" }); }
}, 30000);

test("A registry failure shows Retry and preserves the distinction from an empty Board", async () => {
  const { page } = smoke;
  await page.route("**/api/pause", route => route.fulfill({ status: 503, json: { error: { message: "The Project registry is unavailable." } } }));
  try {
    await page.reload();
    await page.getByTestId("board-project").getByRole("button", { name: "Retry" }).waitFor();
    await settled();
    expect(await page.getByTestId("board-pending").isVisible()).toBe(false);
    expect(await page.getByTestId("board-columns").isVisible()).toBe(false);
    expect(await page.getByTestId("board-add-project").count()).toBe(0);
  } finally { await page.unroute("**/api/pause"); }
  await page.getByTestId("board-project").getByRole("button", { name: "Retry" }).click();
  await page.getByTestId("board-add-project").waitFor();
  expect(await page.getByTestId("board-columns").isVisible()).toBe(false);
  expect(await page.getByTestId("board-pending").isVisible()).toBe(false);
  await settled();
}, 30000);

test("The Board resumes task reads when the first Project appears", async () => {
  const { page } = smoke;
  await seedProject(smoke, "first-project", { title: "First project" });
  await mc(["task", "add", "The first task", "--project", "first-project"]);
  const tasks = gate();
  const started = gate();
  await page.route("**/api/tasks", async route => { started.release(); await tasks.promise; await route.continue(); });
  try {
    await page.clock.fastForward(11000);
    await started.promise;
    await page.getByTestId("board-columns").waitFor();
    expect(await page.getByTestId("board-pending").isVisible()).toBe(false);
    expect(await page.getByTestId("board-add-project").count()).toBe(0);
    await expectSkeleton("board-columns");
    expect(await page.getByTestId("board-stats").innerText()).toContain("—");
    tasks.release();
    await page.getByTestId("board-card").first().waitFor();
    expect(await page.getByTestId("board-columns").innerText()).toContain("The first task");
    expect(await page.getByTestId("board-pane").getAttribute("aria-busy")).toBe("false");
    expect(await page.getByTestId("board-columns").getByTestId("board-skeleton-card").count()).toBe(0);
    await settled();
    const refreshed = page.waitForResponse(response => response.url().endsWith("/api/tasks"));
    await page.clock.fastForward(11000);
    await refreshed;
    await settled();
    const projectButton = page.getByTestId("project-button");
    expect(await projectButton.innerText()).toBe("First project");
    expect(await page.getByRole("button", { name: "Controls", exact: true }).count()).toBe(0);
    expect(await page.getByRole("button", { name: "Project details", exact: true }).count()).toBe(0);
    await projectButton.click();
    expect(await page.getByRole("dialog", { name: "Projects" }).isVisible()).toBe(true);
    expect(await page.getByTestId("project-listbox").innerText()).toContain("First project");
    expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
  } finally { tasks.release(); await page.unrouteAll({ behavior: "wait" }); }
}, 30000);
