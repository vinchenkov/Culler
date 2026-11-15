import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, seedProject, gotoReady, mc, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let visionPath: string;

beforeAll(async () => {
  smoke = await bootSmoke();
  for (const id of ["vision-required", "vision-ready"]) {
    const workspace = join(smoke.scratch, id);
    mkdirSync(workspace);
    const path = join(workspace, "VISION.md");
    if (id === "vision-ready") writeFileSync(path, "Help people understand their local environment.\n");
    await seedProject(smoke, id, { workspace, vision: false });
    if (id === "vision-required") visionPath = path;
  }
  await gotoReady(smoke);
}, 60000);

afterAll(async () => { await teardown(smoke); });

test("current files control notices across the Board and selected Project views", async () => {
  const { page } = smoke;
  const notice = page.getByTestId("board-vision-vision-required");
  await notice.waitFor({ timeout: 20000 });
  await notice.locator("summary").click();
  expect(await notice.innerText()).toContain(visionPath);
  expect(await notice.getByRole("button", { name: "Open Vision" }).isVisible()).toBe(true);
  expect(await page.getByTestId("board-vision-vision-ready").count()).toBe(0);

  await selectProject(smoke, "vision-required");
  await page.getByTestId("tab-review").click();
  const selected = page.getByTestId("project-vision-vision-required");
  await selected.waitFor({ timeout: 20000 });
  await selected.locator("summary").click();
  expect(await selected.innerText()).toContain(visionPath);
  await selectProject(smoke, "vision-ready");
  await selected.waitFor({ state: "detached", timeout: 20000 });

  await page.getByTestId("tab-board").click();
  await notice.waitFor({ timeout: 20000 });
  writeFileSync(visionPath, "Help people understand their local environment.\n");
  await notice.waitFor({ state: "detached", timeout: 20000 });
  expect(await page.getByTestId("board-vision-notice").count()).toBe(0);

  unlinkSync(visionPath);
  await notice.waitFor({ timeout: 20000 });
  expect(await page.getByTestId("board-vision-vision-ready").count()).toBe(0);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);
