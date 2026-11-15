import { join } from "node:path";
import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, mc, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => {
  smoke = await bootSmoke();
  await smoke.page.route("**/api/system/status", route => route.fulfill({
    json: { complete: true, conditions: [], connections: [] },
  }));
  for (let index = 1; index <= 24; index++) {
    const session = await mc(["collie", "start", "--from", `discord:scroll-${index}`]) as { session_id: string };
    await mc(["collie", "title", session.session_id, "--title", `Conversation ${index}`]);
  }
  await gotoReady(smoke);
}, 60000);
afterAll(async () => { await teardown(smoke); });

test("Collie keeps the conversation and session list inside separate scroll areas", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-collie").click();
  await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement).disabled);
  expect(await page.locator("#collie-controls").getByTestId("new-session").count()).toBe(1);
  expect(await page.getByText("Active and ended", { exact: true }).count()).toBe(0);
  await page.getByTestId("send-body").fill("Explain each step of the plan.");
  await Promise.all([
    page.waitForResponse(response => response.url().endsWith("/send") && response.request().method() === "POST" && response.ok()),
    page.getByTestId("send").click(),
  ]);
  await page.getByTestId("collie-message").filter({ hasText: "Explain each step" }).waitFor();
  const id = await page.locator(".session-row.selected").getAttribute("data-session");
  const identity = join(smoke.scratch, "scroll-agent.json");
  await Bun.write(identity, JSON.stringify({ run_id: id, tier: "collie", role: "collie" }));
  const claim = await mc(["collie", "claim", id!], { MC_RUN_JSON: identity }) as { message: { id: number } };
  const reply = Array.from({ length: 70 }, (_, index) => `### Step ${index + 1}\n\nReview the task and confirm its expected result. Record the result before the next step.`).join("\n\n");
  await mc(["collie", "reply", id!, "--to", String(claim.message.id), "--body", reply], { MC_RUN_JSON: identity });
  await page.getByTestId("collie-transcript").getByText("Step 70", { exact: true }).waitFor();

  for (const [width, height] of [[1440, 900], [1000, 700], [390, 844], [390, 667]]) {
    await page.setViewportSize({ width, height });
    await page.screenshot({ path: evidencePath(`collie-scroll-${width}-${height}.png`), fullPage: true });
    for (const name of ["collie-transcript", "session-list"]) {
      const area = page.getByTestId(name);
      await area.evaluate(node => { node.scrollTop = 0; });
      const bounds = await area.boundingBox();
      expect(bounds!.height, `${name} at ${width} by ${height}`).toBeGreaterThan(30);
      expect(await area.evaluate(node => node.scrollHeight > node.clientHeight)).toBe(true);
      await page.mouse.move(bounds!.x + bounds!.width / 2, bounds!.y + bounds!.height / 2);
      await page.mouse.wheel(0, 200);
      await page.waitForFunction(id => document.getElementById(id)!.scrollTop > 0, name);
      await area.evaluate(node => { node.scrollTop = node.scrollHeight; });
      await page.mouse.wheel(0, 800);
      await page.waitForTimeout(150);
      expect(await page.evaluate(() => window.scrollY)).toBe(0);
    }
    const dimensions = await page.evaluate(() => ({
      width: document.documentElement.scrollWidth,
      height: document.documentElement.scrollHeight,
      send: document.getElementById("send")!.getBoundingClientRect().bottom,
      newSession: document.getElementById("new-session")!.getBoundingClientRect().bottom,
      conversation: document.querySelector("#collie-pane .vision-conversation")!.getBoundingClientRect().bottom,
    }));
    expect(dimensions.width).toBeLessThanOrEqual(width);
    expect(dimensions.height).toBeLessThanOrEqual(height);
    expect(dimensions.send).toBeLessThanOrEqual(height);
    expect(dimensions.send).toBeLessThan(dimensions.conversation);
    expect(dimensions.newSession).toBeLessThanOrEqual(height);
    await page.screenshot({ path: evidencePath(`collie-scroll-${width}-${height}.png`), fullPage: true });
  }
  await page.getByTestId("collie-transcript").focus();
  const before = await page.getByTestId("collie-transcript").evaluate(node => node.scrollTop);
  await page.keyboard.press("PageUp");
  await page.waitForFunction(before => document.getElementById("collie-transcript")!.scrollTop < before, before);
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);
