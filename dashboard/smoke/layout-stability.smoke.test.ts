import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, systemNoticeShown, teardown, type Smoke } from "./harness";

let smoke: Smoke;
const pages = ["board", "collie", "review", "pipeline", "configs", "logs", "vision/layout", "environment/layout"];
beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "layout", { title: "Layout Project" });
  await mc(["task", "add", "Keep the dashboard stable", "--project", "layout"]);
  await smoke.page.addInitScript(() => {
    (window as any).layoutShifts = [];
    new PerformanceObserver(list => {
      for (const entry of list.getEntries() as any) {
        (window as any).layoutShifts.push({ value: entry.value, sources: entry.sources.map((source: any) =>
          ({ node: source.node?.id || source.node?.nodeName, before: source.previousRect, after: source.currentRect })) });
      }
    }).observe({ type: "layout-shift", buffered: true });
  });
  await gotoReady(smoke);
  await settled();
  await smoke.page.clock.install();
  await smoke.page.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname;
    await new Promise(resolve => setTimeout(resolve, ["/api/doctor", "/api/logs/runs"].includes(path) ? 360 : 180));
    await route.continue();
  });
}, 60000);
afterAll(async () => { await teardown(smoke); });

async function settled() {
  await smoke.page.waitForFunction(() => document.body.dataset.ready
    && !document.querySelector('.panel:not([hidden])[aria-busy="true"], .panel:not([hidden]) [aria-busy="true"], #lease-badge[aria-busy="true"]'));
  await smoke.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  expect(await smoke.page.locator("body.access-denied").count()).toBe(0);
}

async function expectNoShifts(name = "page") {
  await smoke.page.waitForTimeout(100);
  const shifts = await smoke.page.evaluate(() => (window as any).layoutShifts);
  expect(shifts, `${name}: ${await smoke.page.locator("#system-notices, .request-status:not([hidden])").allTextContents()}`).toEqual([]);
}

test("Review retains its layout after a minute in another window", async () => {
  const { page, server } = smoke;
  await page.route("**/api/system/status", route => route.fulfill({ json: { complete: true, conditions: [], connections: [] } }));
  let release!: () => void;
  try {
    await page.goto(`${server.url}?focus-after-minute#review`);
    await settled();
    await page.waitForFunction(() => !document.getElementById("system-notices")!.textContent);
    const before = await page.getByTestId("review-pane").boundingBox();
    await page.evaluate(() => {
      Object.defineProperty(document, "hidden", { configurable: true, value: true });
      Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await page.clock.fastForward(60_000);
    const held = new Promise<void>(resolve => { release = resolve; });
    await page.route("**/api/system/status", async route => { await held; await route.fulfill({ json: { complete: true, conditions: [], connections: [] } }); });
    const request = page.waitForRequest("**/api/system/status");
    await page.evaluate(() => {
      delete (document as any).hidden;
      delete (document as any).visibilityState;
      document.dispatchEvent(new Event("visibilitychange"));
      window.dispatchEvent(new Event("focus"));
    });
    await request;
    expect(await page.getByTestId("system-notices").textContent()).toBe("");
    expect(await page.getByTestId("review-pane").boundingBox()).toEqual(before);
    release();
    await settled();
    expect(await page.getByTestId("review-pane").boundingBox()).toEqual(before);
  } finally { release?.(); await page.clock.resume(); await page.unroute("**/api/system/status"); }
}, 30000);

const structure = {
  board: ["#board-heading", "#board-stats", "#board-columns", ".board-command-host"],
  collie: ["#collie-pane > .vision-bar", "#collie-pane .collie-grid", "#send-form"],
  review: ["#review-list", "#reader-empty"],
  pipeline: ["#pipeline-pane > .panel-head", "#pipeline-topology", "#activity-pane"],
  configs: [".config-nav", "#configs-content", ".config-section:not([hidden]) > .config-section-head"],
  logs: ["#logs-pane > .panel-head", ".logs-layout", ".logs-sidebar", ".logs-content"],
  vision: ["#vision-pane > .vision-bar", ".vision-grid", ".vision-document", "#vision-pane .vision-conversation"],
  environment: [".environment-heading", ".environment-status", ".environment-form", ".environment-savebar"],
};
async function positions(name: string) {
  return smoke.page.evaluate(selectors => Object.fromEntries(selectors.map(selector => {
    const rect = document.querySelector(selector)!.getBoundingClientRect();
    return [selector, { x: Math.round(rect.x), y: Math.round(rect.y), width: Math.round(rect.width) }];
  })), structure[name.split("/")[0]].filter(selector => selector !== "#reader-empty" || smoke.page.viewportSize()!.width >= 960));
}

test("The address selects the visible page before the browser modules arrive", async () => {
  const { page, server } = smoke;
  for (const name of pages.slice(0, -2)) {
    let release!: () => void;
    const held = new Promise<void>(resolve => { release = resolve; });
    await page.route("**/app.js", async route => { await held; await route.continue(); });
    try {
      await page.goto(`${server.url}?initial=${name}#${name}`, { waitUntil: "commit" });
      await page.locator(`#${name}-pane`).waitFor();
      expect(await page.locator("main > .panel:visible").count()).toBe(1);
      expect(await page.locator(`#${name}-pane`).isVisible()).toBe(true);
      expect(await page.locator("body[data-ready]").count()).toBe(0);
    } finally { release(); }
    await settled();
    await page.unroute("**/app.js");
  }
});

test("Direct page visits show the structure before data arrives and retain its positions", async () => {
  const { page, server } = smoke;
  const differences = [];
  for (const width of [1440, 900, 390]) {
    await page.setViewportSize({ width, height: 900 });
    for (const name of pages) {
      let release!: () => void;
      const held = new Promise<void>(resolve => { release = resolve; });
      await page.route("**/api/**", async route => {
        if (new URL(route.request().url()).pathname === "/api/system/status") {
          await route.fulfill({ json: { complete: true, conditions: [], connections: [] } });
        } else { await held; await route.continue(); }
      });
      let before;
      try {
        await page.goto(`${server.url}?layout=${crypto.randomUUID()}#${name}`);
        await page.waitForSelector("body[data-ready]");
        await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        expect(await page.locator(".panel:not([hidden])").isVisible()).toBe(true);
        for (const selector of structure[name.split("/")[0]].filter(selector => selector !== "#reader-empty" || width >= 960)) expect(await page.locator(selector).isVisible(), `${name} at ${width}: ${selector}`).toBe(true);
        expect(await page.locator("#page-status").count()).toBe(0);
        before = await positions(name);
        if (width !== 900) await page.screenshot({ path: evidencePath(`dashboard-layout/${name.replace("/", "-")}-${width}-pending.png`), fullPage: true });
      } finally { release(); }
      await settled();
      if (name === "board") expect(await page.getByTestId("board-columns").innerText()).toContain("Keep the dashboard stable");
      if (name.startsWith("vision/")) expect(await page.getByTestId("project-field-vision").inputValue()).toContain("layout Project");
      if (name.startsWith("environment/")) expect(await page.getByTestId("environment-base").isEnabled()).toBe(true);
      const after = await positions(name);
      if (JSON.stringify(before) !== JSON.stringify(after)) differences.push({ name, width, before, after });
      expect(await page.locator(".panel:not([hidden])").evaluate(node => getComputedStyle(node).animationName)).toBe("none");
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      if (width !== 900) await page.screenshot({ path: evidencePath(`dashboard-layout/${name.replace("/", "-")}-${width}-ready.png`), fullPage: true });
      await page.unroute("**/api/**");
    }
  }
  expect(differences).toEqual([]);
}, 120000);

test("First page visits show the structure and repeat visits retain the layout", async () => {
  const { page, server } = smoke;
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.route("**/api/system/status", route => route.fulfill({ json: { complete: true, conditions: [], connections: [] } }));
  await page.goto(`${server.url}?navigation#board`);
  await settled();
  for (const name of pages.slice(1, -2)) {
    const pending = await page.getByTestId(`tab-${name}`).evaluate((button, name) => {
      (button as HTMLButtonElement).click();
      return { status: Boolean(document.getElementById("page-status")),
        visibility: getComputedStyle(document.getElementById(`${name}-pane`)!).visibility };
    }, name);
    expect(pending).toEqual({ status: false, visibility: "visible" });
    await settled();
    expect(await page.getByTestId(`${name}-pane`).isVisible()).toBe(true);
  }
  for (const name of pages.slice(0, -2)) {
    await page.getByTestId(`tab-${name}`).click();
    expect(await page.locator("#page-status").count()).toBe(0);
    expect(await page.getByTestId(`${name}-pane`).isVisible()).toBe(true);
    await settled();
    await page.evaluate(() => {
      Object.defineProperty(document, "hidden", { configurable: true, value: true });
      Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await page.clock.fastForward(11000);
    await page.clock.resume();
    await page.evaluate(() => { (window as any).layoutShifts = []; });
    const refreshed = page.waitForResponse(response => response.url().includes("/api/") && response.request().method() === "GET");
    await page.evaluate(() => {
      delete (document as any).hidden;
      delete (document as any).visibilityState;
      document.dispatchEvent(new Event("visibilitychange"));
      window.dispatchEvent(new Event("focus"));
    });
    await refreshed;
    await settled();
    await expectNoShifts(name);
  }
  await page.unroute("**/api/system/status");
}, 60000);

test("A hidden document retains the Board command bar, draft, and scroll position", async () => {
  const { page, server } = smoke;
  await page.setViewportSize({ width: 390, height: 900 });
  await page.goto(`${server.url}?visibility#board`);
  await systemNoticeShown(page);
  await settled();
  const input = page.getByTestId("board-collie-message");
  await input.fill("Keep this draft.\nKeep this second line.");
  await page.evaluate(() => {
    window.scrollTo(0, document.documentElement.scrollHeight);
    (window as any).retainedComposer = document.querySelector('[data-testid="board-collie-message"]');
  });
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const before = await page.getByTestId("board-command").boundingBox();
  const scroll = await page.evaluate(() => scrollY);
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(await page.getByTestId("board-command").isVisible()).toBe(true);
  expect(await page.getByTestId("board-command").boundingBox()).toEqual(before);
  expect(await page.evaluate(() => scrollY)).toBe(scroll);
  await page.clock.fastForward(11000);
  await page.clock.resume();
  await page.evaluate(() => { (window as any).layoutShifts = []; });
  const refreshed = page.waitForResponse("**/api/tasks");
  await page.evaluate(() => {
    delete (document as any).hidden;
    delete (document as any).visibilityState;
    document.dispatchEvent(new Event("visibilitychange"));
    window.dispatchEvent(new Event("focus"));
  });
  await refreshed;
  await settled();
  await expectNoShifts();
  expect(await page.evaluate(() => scrollY)).toBe(scroll);
  expect(await page.getByTestId("board-command").boundingBox()).toEqual(before);
  expect(await input.inputValue()).toBe("Keep this draft.\nKeep this second line.");
  expect(await input.evaluate(node => node === (window as any).retainedComposer)).toBe(true);
  await page.screenshot({ path: evidencePath("dashboard-layout/board-after-focus.png"), fullPage: true });
}, 30000);

test("A slow previous page preserves the current page", async () => {
  const { page, server } = smoke;
  await page.goto(`${server.url}?navigation-race#board`);
  await settled();
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/api/config", async route => { await held; await route.continue(); });
  try {
    const started = page.waitForRequest("**/api/config");
    await page.getByTestId("tab-configs").click();
    await started;
    await page.getByTestId("tab-logs").click();
    await settled();
    expect(await page.getByTestId("logs-pane").isVisible()).toBe(true);
    const finished = page.waitForResponse("**/api/config");
    release();
    await finished;
    await settled();
    expect(await page.getByTestId("logs-pane").isVisible()).toBe(true);
    expect(await page.locator("#page-status").count()).toBe(0);
    expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
  } finally { release(); await page.unroute("**/api/config"); }
}, 30000);
