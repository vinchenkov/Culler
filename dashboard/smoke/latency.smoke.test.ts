import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, seedProject, gotoReady, mc, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => {
  smoke = await bootSmoke();
  await smoke.page.clock.install();
  smoke.page.on("pageerror", error => console.error("Browser error:", error.message));
  await seedProject(smoke, "latency-alpha", { title: "Alpha" });
  await seedProject(smoke, "latency-beta", { title: "Beta" });
  await mc(["task", "add", "Alpha task", "--project", "latency-alpha"]);
  await mc(["task", "add", "Beta task", "--project", "latency-beta"]);
}, 60000);
afterAll(async () => { await teardown(smoke); });

test("The first Board read shows the structure and shares requests", async () => {
  const { page } = smoke;
  const counts = new Map<string, number>();
  let release;
  const pause = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "GET") counts.set(path, (counts.get(path) ?? 0) + 1);
    if (path === "/api/pause") await pause;
    await route.continue();
  });
  try {
    const tasks = page.waitForResponse(response => response.url().endsWith("/api/tasks"));
    await gotoReady(smoke);
    await tasks;
    expect(counts.get("/api/packets")).toBe(1);
    expect(counts.get("/api/health") ?? 0).toBe(0);
    expect(await page.getByTestId("board-columns").getByTestId("board-skeleton-card").count()).toBeGreaterThan(0);
    expect(await page.getByTestId("board-pane").getAttribute("aria-busy")).toBe("true");
    expect(await page.getByTestId("board-pending").isVisible()).toBe(true);
    expect(await page.locator("#page-status").count()).toBe(0);
    expect(await page.getByTestId("board-stats").isVisible()).toBe(true);
    expect(await page.getByTestId("board-columns").isVisible()).toBe(true);
    expect(await page.getByTestId("board-project").isVisible()).toBe(false);
    await page.screenshot({ path: evidencePath("homie-latency-initial-board.png"), fullPage: true });
    release();
    await page.getByTestId("board-card").first().waitFor();
    await page.waitForFunction(() => !document.querySelector('.panel:not([hidden]) [aria-busy="true"]'));
    expect(counts.get("/api/health")).toBe(1);
    expect(await page.getByTestId("board-columns").innerText()).toContain("Alpha task");
    expect(await page.getByTestId("board-columns").innerText()).not.toContain("Beta task");
    await page.screenshot({ path: evidencePath("homie-latency-ready-board.png"), fullPage: true });
    await page.getByTestId("board-card-open").first().click();
    await page.getByTestId("task-dialog").waitFor();
    await page.getByTestId("task-dialog-close").click();
    const reads = new Map(counts);
    await page.getByTestId("tab-pipeline").click();
    await page.locator(".flowmap").waitFor();
    for (const path of ["/api/tasks", "/api/packets", "/api/leases", "/api/settings", "/api/projects"]) expect(counts.get(path)).toBe(reads.get(path));
    await page.getByTestId("tab-review").click();
    await page.waitForFunction(() => document.getElementById("packets")?.dataset.requestState === "ready");
    for (const path of ["/api/packets", "/api/leases", "/api/settings"]) expect(counts.get(path)).toBe(reads.get(path));
  } finally {
    release();
    await page.unrouteAll({ behavior: "wait" });
  }
}, 30000);

test("The first page visit shows its structure during supplementary reads", async () => {
  const { page, server } = smoke;
  let release;
  let held = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/api/runs", async route => { await held; await route.continue(); });
  await page.route("**/api/logs/runs", async route => { await held; await route.continue(); });
  try {
    await page.goto(`${server.url}?latency=${crypto.randomUUID()}#logs/resident/events`);
    await page.waitForFunction(() => document.getElementById("logs-lines")?.textContent !== "The logs load here.");
    expect(await page.getByTestId("logs-pane").isVisible()).toBe(true);
    expect(await page.locator("#page-status").count()).toBe(0);
  } finally {
    const completed = Promise.all([page.waitForResponse(response => response.url().endsWith("/api/runs")), page.waitForResponse(response => response.url().endsWith("/api/logs/runs"))]);
    release();
    await completed;
    await page.unroute("**/api/runs");
    await page.unroute("**/api/logs/runs");
  }
  await page.getByTestId("logs-pane").waitFor();
  held = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/api/outbox/lag", async route => { await held; await route.continue(); });
  try {
    await page.goto(`${server.url}?latency=${crypto.randomUUID()}#configs/agents`);
    await page.getByTestId("routing-row").first().waitFor({ state: "attached" });
    expect(await page.getByTestId("configs-pane").isVisible()).toBe(true);
    expect(await page.locator("#page-status").count()).toBe(0);
    expect(await page.getByTestId("pipeline-outbox").getAttribute("aria-busy")).toBe("true");
  } finally {
    const completed = page.waitForResponse(response => response.url().endsWith("/api/outbox/lag"));
    release(); await completed; await page.unroute("**/api/outbox/lag");
  }
  await page.getByTestId("configs-pane").waitFor();
}, 30000);

test("Every direct page shares initial reads and supports a narrow viewport", async () => {
  const { page, server } = smoke;
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.setViewportSize({ width: 390, height: 844 });
  try {
    for (const tab of ["board", "homie", "review", "pipeline", "configs", "logs"]) {
      const counts = new Map<string, number>();
      const registry = page.waitForResponse(response => response.url().endsWith("/api/pause"));
      await page.route("**/api/**", async route => {
        const path = new URL(route.request().url()).pathname;
        if (route.request().method() === "GET") {
          counts.set(path, (counts.get(path) ?? 0) + 1);
        }
        if (path === "/api/health") await registry;
        await route.continue();
      });
      try {
        await page.goto(`${server.url}?latency=${crypto.randomUUID()}#${tab}`);
        await registry;
        await page.waitForFunction(() => document.body.dataset.ready && !document.querySelector('.panel:not([hidden]) [aria-busy="true"]'));
        expect([...counts].filter(([, count]) => count !== 1)).toEqual([]);
        expect(counts.has("/api/doctor")).toBe(tab === "configs");
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await page.getByTestId(`tab-${tab}`).focus();
        expect(await page.getByTestId(`tab-${tab}`).evaluate(node => node === document.activeElement)).toBe(true);
      } finally {
        await page.unrouteAll({ behavior: "wait" });
      }
    }
    await page.screenshot({ path: evidencePath("homie-latency-narrow.png"), fullPage: true });
  } finally {
    await page.setViewportSize({ width: 1280, height: 720 });
  }
}, 30000);

test("A refresh preserves element identity, focus, and text selection", async () => {
  const { page, server } = smoke;
  await page.goto(`${server.url}?latency=${crypto.randomUUID()}#board`);
  await page.getByTestId("board-card-open").first().waitFor();
  await page.getByTestId("board-card-open").first().focus();
  await page.evaluate(() => {
    const node = document.querySelector('[data-testid="board-card-open"]')!;
    (window as any).latencyCard = node;
    const range = document.createRange();
    range.selectNodeContents(node.querySelector(".title")!);
    getSelection()!.removeAllRanges(); getSelection()!.addRange(range);
  });
  const selected = await page.evaluate(() => getSelection()!.toString());
  const tasks = page.waitForResponse(response => response.url().endsWith("/api/tasks"));
  await page.clock.fastForward(11000);
  // A page visit shares the periodic read when that read is still active.
  await page.getByTestId("tab-board").dispatchEvent("click");
  await tasks;
  await page.waitForFunction(() => document.getElementById("board-columns")?.getAttribute("aria-busy") === "false");
  expect(await page.evaluate(() => (window as any).latencyCard === document.querySelector('[data-testid="board-card-open"]'))).toBe(true);
  expect(await page.evaluate(() => getSelection()!.toString())).toBe(selected);
  expect(await page.evaluate(() => (window as any).latencyCard === document.activeElement)).toBe(true);
  await page.clock.resume();
}, 30000);

test("A hidden document stops periodic reads and prefetch", async () => {
  const { page, server } = smoke;
  await page.goto(`${server.url}?latency=${crypto.randomUUID()}#board`);
  await page.getByTestId("board-card-open").first().waitFor();
  await page.waitForFunction(() => !document.querySelector('[aria-busy="true"]'));
  let reads = 0;
  await page.route("**/api/**", async route => { reads++; await route.continue(); });
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.clock.fastForward(30000);
  await page.getByTestId("tab-configs").dispatchEvent("pointerenter");
  expect(reads).toBe(0);
  await page.evaluate(() => {
    delete (document as any).hidden;
    delete (document as any).visibilityState;
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.waitForResponse(response => response.url().endsWith("/api/tasks"));
  expect(reads).toBeGreaterThan(0);
  await page.unroute("**/api/**");
}, 30000);

test("An authorization failure hides retained records and keeps the repair message visible", async () => {
  const { page, server } = smoke;
  await page.goto(`${server.url}?latency=${crypto.randomUUID()}#board`);
  await page.getByTestId("board-card").first().waitFor();
  await page.route("**/api/config", route => route.fulfill({ status: 401, json: { error: { message: "Denied" } } }));
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("error").waitFor();
  expect(await page.getByTestId("error").innerText()).toContain("Open the dashboard link from setup");
  expect(await page.getByTestId("board-pane").isVisible()).toBe(false);
  expect(await page.getByTestId("configs-pane").isVisible()).toBe(false);
  await page.unroute("**/api/config");
}, 30000);

test("A failed registry clears project records and provides a retry on each project page", async () => {
  const { page, server } = smoke;
  await page.route("**/api/pause", route => route.fulfill({ status: 503, json: { error: { message: "The registry is unavailable." } } }));
  try {
    for (const tab of ["board", "review", "pipeline"]) {
      await page.goto(`${server.url}?latency=${crypto.randomUUID()}#${tab}`);
      await page.getByTestId(`${tab}-pane`).getByRole("button", { name: "Retry", exact: true }).first().waitFor();
      await page.waitForFunction(() => {
        const button = document.querySelector<HTMLButtonElement>('[data-testid="project-button"]');
        return button?.disabled && button.title.includes("The registry is unavailable.");
      });
      expect(await page.getByTestId("board-card").count()).toBe(0);
      expect(await page.getByTestId("packet-row").count()).toBe(0);
      expect(await page.getByTestId("project-button").isDisabled()).toBe(true);
      expect(await page.getByTestId("project-button").getAttribute("title")).toContain("The registry is unavailable.");
    }
  } finally { await page.unroute("**/api/pause"); }
}, 30000);

test("An initial read reaches its deadline and permits a retry", async () => {
  const { page, server } = smoke;
  let release;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let reads = 0;
  await page.route("**/api/tasks", async route => {
    if (++reads === 1) await gate;
    await route.continue();
  });
  try {
    const supplementaryReads = Promise.all([
      "/api/pause", "/api/leases", "/api/settings", "/api/projects", "/api/packets",
      "/api/projects/readiness", "/api/health", "/api/environment/get", "/api/system/status",
    ].map(path => page.waitForResponse(response => new URL(response.url()).pathname === path)
      .then(response => response.finished())));
    await page.goto(`${server.url}?deadline#board`);
    await supplementaryReads;
    await page.waitForFunction(() => document.getElementById("board-project")?.getAttribute("aria-busy") === "false"
      && document.getElementById("lease-badge")?.getAttribute("aria-busy") === "false");
    await page.clock.fastForward(16000);
    const taskError = page.locator("#board-columns-request");
    await taskError.getByText("The request exceeded its deadline. Retry the read.", { exact: true }).waitFor();
    expect(reads).toBe(1);
    release();
    await taskError.getByRole("button", { name: "Retry", exact: true }).click();
    await page.getByTestId("board-card").first().waitFor();
    expect(reads).toBe(2);
  } finally { release(); await page.unrouteAll({ behavior: "wait" }); }
}, 30000);
