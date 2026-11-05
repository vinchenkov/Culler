import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let taskID: number;

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "polish", { title: "Culler" });
  taskID = Number((await mc(["task", "add",
    "Trim oversized comments in the command logs and remove stale source references",
    "--project", "polish", "--description", "The command logs explain each failure and the next action."]))["task_id"]);
  await mc(["task", "add", "Keep the Project controls clear at narrow widths", "--project", "polish"]);
  const blocked = await mc(["task", "add", "Confirm the release acceptance requirements", "--project", "polish"]);
  await mc(["task", "block", String(blocked["task_id"]), "--reason", "The operator must confirm the required checks."]);
  mkdirSync(join(smoke.home, "logs"), { recursive: true });
  writeFileSync(join(smoke.home, "logs", "resident.ndjson"),
    JSON.stringify({ ts: "2026-10-03T12:00:00.000Z", level: "info", comp: "resident",
      event: "tick.completed", msg: "The Project state is current." }) + "\n");
  await gotoReady(smoke);
  await smoke.page.getByTestId("board-card").first().waitFor();
}, 60000);

afterAll(async () => { await teardown(smoke); }, 30000);

test("The Board provides readable cards and visible keyboard focus", async () => {
  const { page } = smoke;
  await page.setViewportSize({ width: 1440, height: 960 });
  const card = page.locator(`[data-testid="board-card"][data-task="${taskID}"]`);
  const bounds = await card.boundingBox();
  expect(bounds!.width).toBeGreaterThanOrEqual(200);
  expect(bounds!.height).toBeGreaterThanOrEqual(100);
  expect(await card.locator(".card-meta").isVisible()).toBe(true);
  expect(await card.locator(".card-id").innerText()).toContain(String(taskID));
  expect(await card.locator(".card-age").innerText()).not.toBe("");

  const open = card.getByTestId("board-card-open");
  const buttonBounds = await open.boundingBox();
  expect(bounds!.width - buttonBounds!.width).toBeLessThanOrEqual(4);
  expect(bounds!.height - buttonBounds!.height).toBeLessThanOrEqual(4);
  await open.focus();
  await page.keyboard.press("Tab");
  await page.keyboard.press("Shift+Tab");
  expect(await open.evaluate(node => node === document.activeElement)).toBe(true);
  const focus = await open.evaluate(node => {
    const style = getComputedStyle(node);
    return { visible: node.matches(":focus-visible"), width: parseFloat(style.outlineWidth), style: style.outlineStyle };
  });
  expect(focus.visible).toBe(true);
  expect(focus.width).toBeGreaterThanOrEqual(2);
  expect(focus.style).not.toBe("none");
  await page.screenshot({ path: evidencePath("dashboard-polish/board-keyboard.png"), fullPage: true });
  await page.keyboard.press("Enter");
  await page.getByTestId("board-record-controls").waitFor();
  expect(await page.locator(".board-action-row").evaluate(node =>
    [...node.childNodes].filter(child => child.nodeType === Node.TEXT_NODE).map(child => child.textContent?.trim()).filter(Boolean),
  )).toEqual([]);
  await page.screenshot({ path: evidencePath("dashboard-polish/task-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  const dialog = await page.getByTestId("task-dialog").boundingBox();
  expect(dialog!.x).toBeGreaterThanOrEqual(0);
  expect(dialog!.x + dialog!.width).toBeLessThanOrEqual(391);
  await page.screenshot({ path: evidencePath("dashboard-polish/task-narrow.png"), fullPage: true });
  await page.keyboard.press("Escape");
  await page.getByTestId("task-dialog").waitFor({ state: "hidden" });
}, 30000);

test("Every main page fits desktop, tablet, and narrow viewports", async () => {
  const { page } = smoke;
  for (const width of [1440, 900, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 960 });
    for (const name of ["board", "homie", "review", "pipeline", "configs", "logs"]) {
      await page.getByTestId(`tab-${name}`).click();
      await page.getByTestId(`${name}-pane`).waitFor();
      if (name === "board") await page.getByTestId("board-card").first().waitFor();
      if (name === "board" && width === 900) {
        const project = page.getByTestId("project-button");
        expect(await project.innerText()).toBe("Culler");
        expect(await project.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
        expect(await project.locator(".scope-name").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
      }
      if (name === "homie") await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement).disabled);
      if (name === "review") await page.waitForFunction(() => !document.getElementById("review-empty")?.hidden);
      if (name === "pipeline") await page.getByTestId("topology-node-worker").waitFor();
      if (name === "pipeline" && width === 390) {
        const role = page.getByTestId("topology-node-worker");
        expect((await role.boundingBox())!.width).toBeGreaterThanOrEqual(200);
        expect(await role.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
      }
      if (name === "configs") await page.getByTestId("config-section-system").waitFor();
      if (name === "logs") await page.waitForFunction(() => document.getElementById("logs-lines")?.textContent?.includes("The Project state is current."));
      await page.screenshot({ path: evidencePath(`dashboard-polish/${name}-${width}.png`), fullPage: true, animations: "disabled" });
      const overflow = await page.evaluate(() => ({ width: document.documentElement.scrollWidth, viewport: window.innerWidth }));
      expect(overflow.width, `${name} exceeds the ${width}-pixel viewport`).toBeLessThanOrEqual(overflow.viewport + 1);
    }
  }
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 120000);
