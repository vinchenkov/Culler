import { afterAll, beforeAll, expect, test } from "bun:test";
import { join } from "node:path";
import { evidencePath, bootSmoke, seedProject, gotoReady, mc, selectProject, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket } from "./delivery-fixture";

let smoke: Smoke;
const tasks: Record<string, number> = {};
beforeAll(async () => {
  smoke = await bootSmoke();
  for (const ws of ["alpha", "beta", "empty", "archived"]) {
    await seedProject(smoke, ws, { vision: false });
  }
  await mc(["project", "archive", "archived"]);
  for (const ws of ["alpha", "beta"]) {
    const task = await mc(["task", "add", `${ws} task`, "--project", ws]);
    tasks[ws] = Number(task.task_id);
  }
  const db = openSpine(smoke);
  for (const ws of ["alpha", "beta"]) {
    seedDeliveryPacket(db, { taskID: tasks[ws]!, thesis: `${ws} packet` });
    db.run("INSERT INTO activity(actor,kind,subject,detail) VALUES ('operator','task.test',?,?)", [String(tasks[ws]), `${ws} event`]);
    db.run("INSERT INTO activity(actor,kind,subject,detail) VALUES ('dispatch','dispatch.health',?,?)", [String(tasks[ws]), `${ws} error`]);
    db.run("INSERT INTO runs(id,tier,role,project,subject) VALUES (?,'pipeline','worker',?,?)", [`${ws}-run`,ws,tasks[ws]]);
  }
  db.close();
  await gotoReady(smoke);
  await smoke.page.getByTestId("project-alpha").waitFor({ state: "attached" });
}, 60000);
afterAll(async () => { await teardown(smoke); });

async function select(ws: string) {
  await selectProject(smoke, ws);
  await contains("project-button", ws);
  expect(await smoke.page.getByTestId(`project-${ws}`).getAttribute("aria-current")).toBe("true");
}
async function selectedProject() {
  return smoke.page.evaluate(() =>
    document.querySelector('[data-testid="project-listbox"] [aria-current="true"]')?.getAttribute("title"));
}
async function contains(testid: string, text: string) {
  await smoke.page.waitForFunction(({ testid, text }) =>
    document.querySelector(`[data-testid="${testid}"]`)?.textContent?.includes(text), { testid, text }, { timeout: 15000 });
}

async function selectActivity(stream: "events" | "errors") {
  const { page } = smoke;
  await page.getByTestId(`activity-tab-${stream}`).click();
  await page.getByTestId(`activity-${stream}`).waitFor({ state: "visible" });
  expect(await page.getByTestId(`activity-tab-${stream}`).getAttribute("aria-selected")).toBe("true");
  expect(await page.getByTestId(`activity-${stream}`).isVisible()).toBe(true);
  expect(await page.getByTestId(`activity-${stream === "events" ? "errors" : "events"}`).isVisible()).toBe(false);
}

test("One Project scopes each project page", async () => {
  const { page } = smoke;
  for (const ws of ["alpha", "beta"]) {
    const other = ws === "alpha" ? "beta" : "alpha";
    await page.getByTestId("tab-board").click();
    await select(ws);
    await contains("board-columns", `${ws} task`);
    expect(await page.getByTestId("board-columns").innerText()).not.toContain(`${other} task`);
    expect(await page.getByTestId(`board-vision-${other}`).isVisible()).toBe(true);
    expect(await page.getByTestId(`board-vision-${ws}`).isVisible()).toBe(true);
    await page.getByTestId("tab-review").click();
    await contains("packets", `${ws} task`);
    expect(await page.getByTestId(`project-vision-${ws}`).isVisible()).toBe(true);
    expect(await page.getByTestId(`project-vision-${other}`).count()).toBe(0);
    expect(await page.getByTestId("packets").innerText()).not.toContain(`${other} task`);
    expect(await page.getByTestId("review-count").innerText()).toBe("1");
    await page.getByTestId("tab-pipeline").click();
    await page.getByTestId("pipeline-pane").getByTestId("activity-pane").waitFor();
    expect(await page.getByTestId("pipeline-pane").getByTestId("activity-pane").isVisible()).toBe(true);
    await selectActivity("events");
    await contains("activity-events", `${ws} event`);
    expect(await page.getByTestId("activity-events").innerText()).not.toContain(`${other} event`);
    await selectActivity("errors");
    await contains("activity-errors", `${ws} error`);
    expect(await page.getByTestId("activity-errors").innerText()).not.toContain(`${other} error`);
    await page.getByTestId("pipeline-runs").locator(`[title="${ws}-run"]`).waitFor();
    expect(await page.getByTestId("pipeline-runs").locator(`[title="${other}-run"]`).count()).toBe(0);
    expect(await page.getByTestId("topology-node-collie").count()).toBe(0);
  }
  await page.reload();
  await page.getByTestId("topology-node-worker").waitFor();
  await contains("project-button", "beta");
  expect(await page.getByTestId("project-beta").getAttribute("aria-current")).toBe("true");
  await page.getByTestId("pipeline-runs").locator('[title="beta-run"]').waitFor();
}, 60000);

test("empty and archived Projects remain selectable; global pages mute the selector", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await select("empty");
  expect(await page.getByTestId("board-card").count()).toBe(0);
  await select("archived");
  await page.getByTestId("project-button").click();
  await page.getByTestId("project-listbox").waitFor({ state: "visible" });
  expect(await page.getByTestId("project-row-archived").getByRole("button", { name: /^(Pause|Resume) / }).count()).toBe(0);
  // A page change closes the Project list and disables its button.
  await page.evaluate(() => { location.hash = "#collie"; });
  await page.getByTestId("project-listbox").waitFor({ state: "hidden" });
  expect(await page.getByTestId("project-button").getAttribute("aria-expanded")).toBe("false");
  expect(await page.getByTestId("project-context").isVisible()).toBe(true);
  for (const tab of ["collie", "configs", "logs"]) {
    await page.getByTestId(`tab-${tab}`).click();
    expect(await page.getByTestId("project-context").isVisible()).toBe(true);
    expect(await page.getByTestId("project-button").isDisabled()).toBe(true);
  }
  await page.getByTestId("tab-board").click();
  expect(await page.getByTestId("project-button").isEnabled()).toBe(true);
  await contains("project-button", "archived");
  expect(await page.getByTestId("project-archived").getAttribute("aria-current")).toBe("true");
  expect(await page.getByTestId("project-archived").innerText()).toContain("Archived");
  await select("archived");
}, 30000);

test("page changes preserve the navigation positions", async () => {
  const { page } = smoke;
  const viewport = page.viewportSize()!;
  const controls = ".scope-switcher, nav.seg .tab, .nav-divider, .viewer-links";
  try {
    for (const width of [1440, 900, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.getByTestId("tab-board").click();
      await select("alpha");
      await page.waitForFunction(() => document.querySelector('[data-testid="review-count"]')?.textContent === "1");
      const spacing = await page.evaluate(() => {
        const label = (name: string) => {
          const range = document.createRange();
          range.selectNodeContents(document.querySelector(`nav.seg .tab[data-page="${name}"]`)!.firstChild!);
          return range.getBoundingClientRect();
        };
        const [board, review, pipeline] = ["board", "review", "pipeline"].map(label);
        const count = document.querySelector('[data-testid="review-count"]')!.getBoundingClientRect();
        return {
          gaps: [Math.round(review!.left - board!.right), Math.round(pipeline!.left - review!.right)],
          countClear: count.right < pipeline!.left,
        };
      });
      expect(spacing.gaps[1]).toBe(spacing.gaps[0]);
      expect(spacing.countClear).toBe(true);
      const before = await page.locator(controls).evaluateAll((nodes) => nodes.map((node) => {
        const { x, y, width, height } = node.getBoundingClientRect();
        return { x: x + (node.closest("nav.seg")?.scrollLeft ?? 0), y, width, height };
      }));
      for (const tab of ["logs", "collie", "configs", "review", "pipeline", "board"]) {
        await page.evaluate((tab) => { location.hash = `#${tab}`; }, tab);
        await page.waitForFunction((tab) => document.querySelector(`.tab[data-page="${tab}"]`)?.getAttribute("aria-current") === "page", tab);
        expect(await page.locator(controls).evaluateAll((nodes) => nodes.map((node) => {
          const { x, y, width, height } = node.getBoundingClientRect();
          return { x: x + (node.closest("nav.seg")?.scrollLeft ?? 0), y, width, height };
        }))).toEqual(before);
        expect(await selectedProject()).toBe("alpha");
        if (tab === "logs" || tab === "board") {
          await page.screenshot({ path: evidencePath(`navigation-${width}-${tab}.png`) });
        }
      }
    }
  } finally { await page.setViewportSize(viewport); }
}, 60000);

test("refresh preserves navigation before modules and Project data arrive", async () => {
  const { page } = smoke;
  const viewport = page.viewportSize()!;
  const controls = ".scope-switcher, #project-button, nav.seg .tab, .nav-divider, .viewer-links";
  const bounds = () => page.locator(controls).evaluateAll((nodes) => nodes.map((node) => {
    const { x, y, width, height } = node.getBoundingClientRect();
    return { x: x + (node.closest("nav.seg")?.scrollLeft ?? 0), y, width, height };
  }));
  await page.getByTestId("tab-board").click();
  await select("alpha");
  try {
    for (const width of [1440, 900, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      for (const tab of ["board", "logs", "review"]) {
        let releaseModule!: () => void;
        let releaseData!: () => void;
        const moduleGate = new Promise<void>((resolve) => { releaseModule = resolve; });
        const dataGate = new Promise<void>((resolve) => { releaseData = resolve; });
        await page.route("**/app.js", async (route) => { await moduleGate; await route.continue(); });
        await page.route("**/api/pause", async (route) => { await dataGate; await route.continue(); });
        await page.route("**/fonts/*.woff2", async (route) => { await dataGate; await route.continue(); });
        try {
          await page.goto(`${smoke.server.url}?refresh=${width}-${tab}#${tab}`, { waitUntil: "commit" });
          await page.getByTestId(`tab-${tab}`).waitFor();
          await page.waitForFunction(() => document.querySelector("nav.seg .tab[aria-current='page']"));
          expect(await page.getByTestId("project-context").isVisible()).toBe(true);
          expect(await page.getByTestId("project-button").textContent()).toContain("alpha");
          expect(await page.getByTestId("project-button").isDisabled()).toBe(true);
          expect(await page.getByTestId(`tab-${tab}`).getAttribute("aria-current")).toBe("page");
          expect(await page.getByTestId("project-context").getAttribute("class")).toBe(tab === "logs" ? "project-context muted" : "project-context");
          const before = await bounds();
          releaseModule();
          await page.waitForFunction(() => document.body.dataset.ready === "1");
          expect(await bounds()).toEqual(before);
          releaseData();
          await page.getByTestId("project-alpha").waitFor({ state: "attached" });
          await page.evaluate(() => document.fonts.ready);
          await page.waitForFunction(() => document.querySelector('[data-testid="review-count"]')?.textContent === "1");
          expect(await bounds()).toEqual(before);
          expect(await page.getByTestId("project-button").isDisabled()).toBe(tab === "logs");
          await page.screenshot({ path: evidencePath(`refresh-${width}-${tab}.png`) });
        } finally {
          releaseModule();
          releaseData();
          await page.unrouteAll({ behavior: "wait" });
        }
      }
    }
  } finally { await page.setViewportSize(viewport); }
}, 60000);

test("saved Activity links open Pipeline and preserve the Project", async () => {
  const { page } = smoke;
  await select("beta");
  await page.goto(`${smoke.server.url}#activity`);
  await page.getByTestId("topology-node-worker").waitFor();
  await contains("activity-events", "beta event");
  expect(new URL(page.url()).hash).toBe("#pipeline");
  expect(await page.getByRole("button", { name: "Activity", exact: true }).count()).toBe(0);
  expect(await page.getByTestId("tab-pipeline").getAttribute("aria-current")).toBe("page");
  await page.evaluate(() => { location.hash = "#/activity"; });
  await page.waitForFunction(() => location.hash === "#pipeline");
  await page.reload();
  await contains("activity-events", "beta event");
  await contains("project-button", "beta");
}, 30000);

test("the Activity filter applies to both streams within Pipeline", async () => {
  const { page } = smoke;
  await select("alpha");
  await page.getByTestId("tab-pipeline").click();
  const filter = page.getByTestId("activity-kind");
  try {
    await selectActivity("errors");
    await filter.fill("dispatch.health");
    await filter.press("Enter");
    await contains("activity-errors", "alpha error");
    expect(await page.getByTestId("activity-events").getByTestId("activity-row").count()).toBe(0);
    await selectActivity("events");
    await filter.fill("task.test");
    await filter.press("Enter");
    await contains("activity-events", "alpha event");
    expect(await page.getByTestId("activity-errors").getByTestId("activity-row").count()).toBe(0);
    expect(await page.getByTestId("activity-events").innerText()).not.toContain("beta event");
    expect(await page.getByTestId("topology-node-worker").isVisible()).toBe(true);
  } finally {
    await filter.fill("");
    await filter.press("Enter");
  }
  await contains("activity-errors", "alpha error");
}, 30000);

function addActivity(project: string, kind: string, detail: string) {
  const db = openSpine(smoke);
  try {
    return Number(db.run("INSERT INTO activity(actor,kind,subject,detail) VALUES ('dispatch',?,?,?)",
      [kind, String(tasks[project]), detail]).lastInsertRowid);
  } finally { db.close(); }
}

function healthDelivered(id: number) {
  const db = openSpine(smoke);
  try {
    const row = db.query("SELECT delivered_at FROM outbox WHERE kind='health' AND surface='dashboard' AND json_extract(payload,'$.activity_id')=?")
      .get(id) as { delivered_at: string | null };
    expect(row).toBeTruthy();
    return row.delivered_at !== null;
  } finally { db.close(); }
}

test("Activity tabs support the keyboard and acknowledge only visible errors", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await select("alpha");
  const errorID = addActivity("alpha", "dispatch.health", "The hidden error requires inspection.");
  await page.getByTestId("tab-pipeline").click();
  await contains("activity-errors", "The hidden error requires inspection.");
  expect(await page.getByTestId("activity-events").isVisible()).toBe(true);
  expect(await page.getByTestId("activity-errors").isVisible()).toBe(false);
  expect(healthDelivered(errorID)).toBe(false);
  const events = page.getByTestId("activity-tab-events");
  const errors = page.getByTestId("activity-tab-errors");
  const ack = page.waitForResponse((response) => response.url().endsWith("/api/outbox/health/ack") &&
    response.request().postDataJSON().ids.includes(errorID));
  await events.focus();
  await events.press("ArrowRight");
  await ack;
  expect(healthDelivered(errorID)).toBe(true);
  expect(await errors.getAttribute("aria-selected")).toBe("true");
  expect(await page.getByTestId("activity-errors").isVisible()).toBe(true);
  expect(await page.getByTestId("activity-events").isVisible()).toBe(false);
  expect(await errors.evaluate((node) => node === document.activeElement)).toBe(true);
  expect(await events.getAttribute("tabindex")).toBe("-1");
  await errors.press("Home");
  expect(await events.getAttribute("aria-selected")).toBe("true");
  await events.press("End");
  expect(await errors.getAttribute("aria-selected")).toBe("true");
  await errors.press("ArrowLeft");
  expect(await events.getAttribute("aria-selected")).toBe("true");
  await selectActivity("errors");
  await select("beta");
  await contains("activity-errors", "beta error");
  expect(await errors.getAttribute("aria-selected")).toBe("true");
  await page.getByTestId("tab-board").click();
  await page.getByTestId("tab-pipeline").click();
  expect(await errors.getAttribute("aria-selected")).toBe("true");
  await selectActivity("events");
}, 30000);

test("a paused live tail preserves records while the Pipeline overview refreshes", async () => {
  const { page } = smoke;
  await select("alpha");
  await page.getByTestId("tab-pipeline").click();
  await selectActivity("events");
  await contains("activity-events", "alpha event");
  const tail = page.getByTestId("activity-tail");
  await tail.uncheck();
  const alphaError = addActivity("alpha", "dispatch.health", "Alpha error after pause");
  const betaError = addActivity("beta", "dispatch.health", "Beta error after pause");
  addActivity("alpha", "task.test", "Alpha event after pause");
  const db = openSpine(smoke);
  db.run("INSERT INTO runs(id,tier,role,project,subject) VALUES ('alpha-paused-run','pipeline','worker','alpha',?)", [tasks.alpha]);
  db.close();
  try {
    await page.getByTestId("tab-board").click();
    await page.getByTestId("tab-pipeline").click();
    await page.getByTestId("pipeline-runs").locator('[title="alpha-paused-run"]').waitFor();
    await contains("activity-events", "alpha event");
    expect(await page.getByTestId("activity-events").innerText()).not.toContain("Alpha event after pause");
    expect(await page.getByTestId("activity-errors").innerText()).not.toContain("Alpha error after pause");
    expect(healthDelivered(alphaError)).toBe(false);
    expect(healthDelivered(betaError)).toBe(false);
    await select("beta");
    await contains("activity-events", "The live tail is paused.");
    expect(await page.getByTestId("activity-events").getByTestId("activity-row").count()).toBe(0);
    expect(await page.getByTestId("activity-errors").getByTestId("activity-row").count()).toBe(0);
    const ack = page.waitForResponse((response) => response.url().endsWith("/api/outbox/health/ack") &&
      response.request().postDataJSON().ids.includes(betaError));
    await tail.check();
    await contains("activity-errors", "Beta error after pause");
    expect(healthDelivered(betaError)).toBe(false);
    await tail.uncheck();
    await selectActivity("errors");
    expect(healthDelivered(betaError)).toBe(false);
    await tail.check();
    await ack;
    expect(healthDelivered(betaError)).toBe(true);
    expect(healthDelivered(alphaError)).toBe(false);
  } finally { await tail.check(); }
}, 30000);

test("a failed activity read clears the streams and preserves the Pipeline overview", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-pipeline").click();
  await select("alpha");
  await selectActivity("events");
  await contains("activity-events", "alpha event");
  await page.route("**/api/activity?**", (route) => route.fulfill({ status: 500,
    json: { error: { message: "The activity read failed." } } }));
  try {
    await page.getByTestId("tab-pipeline").click();
    await contains("activity-events", "Events are unavailable.");
    await selectActivity("errors");
    await contains("activity-errors", "Dispatch errors are unavailable.");
    await contains("error", "The activity read failed.");
    expect(await page.getByTestId("topology-node-worker").isVisible()).toBe(true);
    expect(await page.getByTestId("pipeline-runs").locator('[title="alpha-run"]').isVisible()).toBe(true);
  } finally { await page.unroute("**/api/activity?**"); }
  await page.getByTestId("tab-pipeline").click();
  await selectActivity("events");
  await contains("activity-events", "alpha event");
  await page.getByTestId("error").waitFor({ state: "hidden" });
  await select("empty");
  await contains("activity-events", "No events match this selection.");
  await selectActivity("errors");
  await contains("activity-errors", "No dispatch errors match this selection.");
  expect(await page.getByTestId("activity-tab-errors").textContent()).toContain("Dispatch errors");
  expect(await page.getByTestId("activity-help").textContent()).toContain("See Recent runs for run failures.");
  await contains("pipeline-runs", "No pipeline runs yet.");
}, 30000);

test("Pipeline activity fits desktop and phone displays with bounded streams", async () => {
  const { page } = smoke;
  for (let index = 0; index < 35; index++) addActivity("alpha", "task.test", `Activity record ${index}: The task requires verification.`);
  await select("alpha");
  await page.getByTestId("tab-pipeline").click();
  await page.reload();
  await contains("activity-events", "Activity record 34");
  expect(await page.getByTestId("events-count").innerText()).toMatch(/newest (just now|\d+[smh])/);
  try {
    for (const [width, height, name] of [[1440, 1000, "desktop"], [390, 844, "phone"]] as const) {
      await page.setViewportSize({ width, height });
      await page.waitForFunction(() => document.documentElement.scrollWidth <= window.innerWidth, null, { timeout: 5000 });
      const stream = page.getByTestId("activity-events");
      expect(await stream.evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
      for (const testid of ["activity-kind", "activity-tail", "activity-tab-events", "activity-tab-errors", "activity-events", "pipeline-runs"]) {
        const box = await page.getByTestId(testid).boundingBox();
        expect(box!.x).toBeGreaterThanOrEqual(0);
        expect(box!.x + box!.width).toBeLessThanOrEqual(width);
      }
      await page.getByTestId("activity-pane").screenshot({ path: evidencePath(`pipeline-tabs-events-${name}.png`) });
      await selectActivity("errors");
      const errors = await page.getByTestId("activity-errors").boundingBox();
      const events = await stream.boundingBox();
      expect(events).toBeNull();
      expect(errors!.width).toBeGreaterThan(width * 0.75);
      await page.getByTestId("activity-pane").screenshot({ path: evidencePath(`pipeline-tabs-errors-${name}.png`) });
      await selectActivity("events");
    }
  } finally { await page.setViewportSize({ width: 1280, height: 720 }); }
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 30000);

test("the keyboard opens the list, moves the selection, and closes it", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await select("alpha");
  const ids = await page.evaluate(() =>
    [...document.querySelectorAll('[data-testid="project-listbox"] [aria-current]')].map((option) => option.getAttribute("title")));
  const next = ids[(ids.indexOf("alpha") + 1) % ids.length];
  await page.getByTestId("project-button").focus();
  await page.keyboard.press("ArrowDown");
  await page.getByTestId("project-listbox").waitFor({ state: "visible" });
  expect(await page.getByTestId("project-button").getAttribute("aria-expanded")).toBe("true");
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("project-alpha");
  await page.keyboard.press("ArrowDown");
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe(`project-${next}`);
  await page.keyboard.press("Enter");
  await contains("project-button", next);
  await page.getByTestId("project-listbox").waitFor({ state: "hidden" });
  expect(await selectedProject()).toBe(next);
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("project-button");
  await page.keyboard.press("ArrowUp");
  await page.getByTestId("project-listbox").waitFor({ state: "visible" });
  await page.keyboard.press("Escape");
  await page.getByTestId("project-listbox").waitFor({ state: "hidden" });
  expect(await page.getByTestId("project-button").getAttribute("aria-expanded")).toBe("false");
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("project-button");
  expect(await selectedProject()).toBe(next);
}, 30000);

test("a click outside the list closes it and keeps the selection", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await select("alpha");
  await page.getByTestId("project-button").click();
  await page.getByTestId("project-listbox").waitFor({ state: "visible" });
  await page.getByTestId("lease-badge").click();
  await page.getByTestId("project-listbox").waitFor({ state: "hidden" });
  expect(await page.getByTestId("project-button").getAttribute("aria-expanded")).toBe("false");
  expect(await selectedProject()).toBe("alpha");
  await contains("project-button", "alpha");
}, 30000);

test("a delayed Activity response cannot enter another Project's stream", async () => {
  const { page } = smoke;
  await select("alpha");
  const errorID = addActivity("alpha", "dispatch.health", "Alpha error from a delayed response");
  let release!: () => void;
  let received!: () => void;
  const arrived = new Promise<void>((resolve) => { received = resolve; });
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/activity?**", async (route) => {
    if (new URL(route.request().url()).searchParams.get("project") === "alpha") {
      const response = await route.fetch();
      received();
      await gate;
      await route.fulfill({ response });
    } else await route.continue();
  });
  try {
    await page.getByTestId("tab-pipeline").click();
    await arrived;
    await select("beta");
    await contains("activity-events", "beta event");
    const completed = page.waitForResponse((response) => response.url().includes("project=alpha") && response.url().includes("/api/activity"));
    release();
    await completed;
    await page.evaluate(() => new Promise(requestAnimationFrame));
    expect(await page.getByTestId("activity-events").innerText()).not.toContain("alpha event");
    expect(await page.getByTestId("activity-errors").innerText()).not.toContain("alpha error");
    expect(healthDelivered(errorID)).toBe(false);
  } finally { release(); await page.unroute("**/api/activity?**"); }
}, 30000);

test("after a Project switch, the address of a packet of the other Project shows the packet list", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await select("alpha");
  await page.getByTestId("board-card-open").click();
  await page.getByTestId("task-dialog").waitFor({ state: "visible" });
  await page.getByTestId("task-dialog-close").click();
  await page.getByTestId("tab-review").click();
  await contains("packets", "alpha task");
  await page.getByTestId("packet-row").click();
  await page.getByTestId("packet-decide").waitFor({ state: "visible" });
  // The Project switcher stays available beside the reader.
  await select("beta");
  await contains("packets", "beta task");
  await page.waitForFunction((id) => location.hash === `#review/${id}`, tasks.beta);
  expect(await page.getByTestId("packets").innerText()).not.toContain("alpha task");
  // An address of the other Project's packet shows the packet list.
  await page.evaluate((id) => { location.hash = `#review/${id}`; }, tasks.alpha);
  await page.waitForFunction(() => location.hash === "#review");
  expect(await page.getByTestId("packet-reader").isVisible()).toBe(false);
}, 30000);

test("the switcher and its list fit a phone viewport", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await select("beta");
  const inside = (testid: string) => page.waitForFunction((testid) => {
    const box = document.querySelector(`[data-testid="${testid}"]`)!.getBoundingClientRect();
    return document.documentElement.scrollWidth <= 390 && box.width > 0 && box.left >= 0 && box.right <= 390;
  }, testid);
  try {
    await page.setViewportSize({ width: 390, height: 844 });
    await inside("project-button");
    await page.getByTestId("project-button").click();
    await page.getByTestId("project-listbox").waitFor({ state: "visible" });
    await inside("project-listbox");
    await page.keyboard.press("Escape");
    await page.getByTestId("project-listbox").waitFor({ state: "hidden" });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  } finally { await page.setViewportSize({ width: 1280, height: 720 }); }
}, 30000);

test("a delayed packet report after the reader closes paints no report", async () => {
  const { page } = smoke;
  let release!: () => void;
  let received!: () => void;
  const arrived = new Promise<void>((resolve) => { received = resolve; });
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/packets", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.packets = body.packets.map((row: any) => row.project === "alpha" ? { ...row, render_path: "/scratch/alpha.html" } : row);
    await route.fulfill({ response, json: body });
  });
  const pattern = `**/api/packets/${tasks.alpha}/render?*`;
  await page.route(pattern, async (route) => {
    received();
    await gate;
    await route.fulfill({ contentType: "text/html", body: "<h1>Alpha delayed report</h1>" });
  });
  try {
    await page.getByTestId("tab-board").click();
    await select("alpha");
    await page.reload();
    await page.waitForSelector("body[data-ready]");
    // The Review page opens the alpha packet, and the report read waits.
    await page.getByTestId("tab-review").click();
    await arrived;
    // Browser Back closes the reader before the report arrives.
    await page.goBack();
    await page.getByTestId("packet-reader").waitFor({ state: "hidden" });
    await select("beta");
    await contains("packets", "beta task");
    await page.waitForFunction((id) => location.hash === `#review/${id}`, tasks.beta);
    const completed = page.waitForResponse((response) => new URL(response.url()).pathname === `/api/packets/${tasks.alpha}/render`);
    release();
    await completed;
    await page.evaluate(() => new Promise(requestAnimationFrame));
    // The reader keeps the beta packet, and the alpha report paints nowhere.
    expect(await page.getByTestId("reader-title").innerText()).toStartWith(`Task #${tasks.beta} · `);
    expect(await page.getByTestId("packet-frame").count()).toBe(0);
    expect(await page.getByTestId("packet-render").innerText()).not.toContain("Alpha delayed report");
  } finally {
    release();
    await page.unrouteAll({ behavior: "wait" });
  }
}, 30000);

// A registry without the selected Project makes the page select another
// Project while the reader is open.
test("an automatic Project change closes the reader and shows the packet list", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-review").click();
  await select("alpha");
  await contains("packets", "alpha task");
  await page.getByTestId("packet-row").click();
  await page.getByTestId("packet-reader").waitFor({ state: "visible" });
  await page.route("**/api/pause", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.projects = body.projects.filter((row: any) => row.id !== "alpha");
    await route.fulfill({ response, json: body });
  });
  try {
    await page.getByTestId("packet-reader").waitFor({ state: "hidden", timeout: 30000 });
    await page.waitForFunction(() => location.hash === "#review");
    await contains("packets", "beta task");
    expect(await page.getByTestId("packets").innerText()).not.toContain("alpha task");
    expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("packets");
  } finally { await page.unroute("**/api/pause"); }
}, 45000);
