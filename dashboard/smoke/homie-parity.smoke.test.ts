import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => { smoke = await bootSmoke(); await gotoReady(smoke); }, 60000);
afterAll(async () => { await teardown(smoke); });

const selectedID = () => smoke.page.locator('[data-testid="session-row"].selected').getAttribute("data-session");

async function capture(name: string, width: number, height: number) {
  const { page } = smoke;
  await page.setViewportSize({ width, height });
  await page.getByTestId("send").scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const send = await page.getByTestId("send").boundingBox();
  expect(send).not.toBeNull();
  expect(send!.x + send!.width).toBeLessThanOrEqual(width);
  await page.screenshot({ path: evidencePath(name), fullPage: true });
}

test("Homie shares the conversation controls and preserves its session history", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-homie").click();
  const pane = page.getByTestId("homie-pane");
  await pane.getByText("What can Homie help you with?").waitFor();
  await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement).disabled);
  expect(await page.getByTestId("session-row").count()).toBe(1);
  expect(await page.getByTestId("homie-uploads").isDisabled()).toBe(false);
  expect(await pane.locator(".vision-document").count()).toBe(0);
  expect(await pane.getByRole("button", { name: /Vision/ }).count()).toBe(0);
  await capture("homie-empty-desktop.png", 1440, 1000);

  const prompt = "Help me plan my next step.";
  await pane.getByRole("button", { name: prompt, exact: true }).click();
  await page.waitForFunction(text => (document.getElementById("send-body") as HTMLTextAreaElement)?.value === text, prompt);
  expect(await page.getByTestId("session-row").count()).toBe(1);
  expect(await page.getByTestId("homie-message").count()).toBe(0);
  await page.getByTestId("homie-uploads").setInputFiles({ name: "draft.txt", mimeType: "text/plain", buffer: Buffer.from("Keep this draft.") });
  await page.getByRole("button", { name: "Remove draft.txt", exact: true }).waitFor();
  const first = await selectedID();
  const model = page.getByTestId("homie-model");
  await model.waitFor();
  await model.click();
  const menu = page.getByTestId("homie-model-menu");
  await menu.locator('[data-binding="claude"]').click();
  await page.waitForFunction(id => document.querySelector('[data-testid="session-row"].selected')?.getAttribute("data-session") !== id, first);
  await menu.waitFor({ state: "hidden" });
  expect(await page.getByTestId("send-body").inputValue()).toBe(prompt);
  const second = await selectedID();
  expect(second).not.toBe(first);
  expect(await page.getByTestId("session-row").count()).toBe(1);
  expect(await page.getByRole("button", { name: "Remove draft.txt", exact: true }).isVisible()).toBe(true);
  await page.getByRole("button", { name: "Remove draft.txt", exact: true }).click();

  const input = page.getByTestId("send-body");
  await input.fill("A goal");
  await input.press("Shift+Enter");
  await input.press("x");
  expect(await input.inputValue()).toBe("A goal\nx");
  await input.press("Enter");
  await page.getByTestId("homie-message").filter({ hasText: "A goal" }).waitFor();
  await page.waitForFunction(() => !(document.getElementById("send") as HTMLButtonElement).disabled);
  const history = await mc(["homie", "history", second!]) as { messages: { body: string }[] };
  expect(history.messages.some(row => row.body === "A goal\nx")).toBe(true);

  await page.waitForFunction(() => (document.querySelector('[data-testid="homie-model"]') as HTMLButtonElement).disabled);
  const identity = join(smoke.scratch, "homie-parity-run.json");
  writeFileSync(identity, JSON.stringify({ run_id: second, tier: "homie", role: "homie" }));
  const claim = await mc(["homie", "claim", second!], { MC_RUN_JSON: identity }) as { message: { id: number } };
  await mc(["homie", "reply", second!, "--to", String(claim.message.id), "--body", "The goal is clear."], { MC_RUN_JSON: identity });
  await page.getByTestId("homie-message").filter({ hasText: "The goal is clear." }).waitFor();
  await model.click();
  expect(await menu.textContent()).toContain("Selecting another model starts a new conversation. This conversation stays in your list.");
  await capture("homie-model-desktop.png", 1440, 1000);
  await input.fill("Keep my next question.");
  await page.getByTestId("homie-uploads").setInputFiles({ name: "next.txt", mimeType: "text/plain", buffer: Buffer.from("Keep this attachment.") });
  await page.getByRole("button", { name: "Remove next.txt", exact: true }).waitFor();
  const routingBefore = await mc(["routing", "show"]);
  const changes: string[] = [];
  const recordChange = (request: import("playwright").Request) => {
    if (request.method() === "POST" && /\/api\/(sessions\/[^/]+\/model|routing)$/.test(request.url())) changes.push(request.url());
  };
  page.on("request", recordChange);
  const dialogs: string[] = [];
  const dismissBrowserDialog = async (dialog: import("playwright").Dialog) => { dialogs.push(dialog.message()); await dialog.dismiss(); };
  page.on("dialog", dismissBrowserDialog);
  await menu.locator('[data-binding="fake"]').click();
  const confirmation = page.getByTestId("homie-model-confirm");
  await confirmation.waitFor();
  expect(await confirmation.textContent()).toContain("Your current conversation stays in the session list.");
  expect(await confirmation.locator("p").first().textContent()).toContain("Homie will use");
  expect(await confirmation.getByRole("button", { name: "Cancel", exact: true }).evaluate(button => button === document.activeElement)).toBe(true);
  expect(changes).toEqual([]);
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    const bounds = await confirmation.boundingBox();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);
    await page.screenshot({ path: evidencePath(`homie-model-confirm-${width}.png`), fullPage: true });
  }
  await confirmation.getByRole("button", { name: "Cancel", exact: true }).click();
  await confirmation.waitFor({ state: "detached" });
  expect(await model.evaluate(button => button === document.activeElement)).toBe(true);
  expect(await selectedID()).toBe(second);
  expect(changes).toEqual([]);
  expect(await mc(["routing", "show"])).toEqual(routingBefore);
  expect(await page.getByTestId("session-row").count()).toBe(1);
  expect(await input.inputValue()).toBe("Keep my next question.");
  expect(await page.getByRole("button", { name: "Remove next.txt", exact: true }).isVisible()).toBe(true);
  await model.click();
  await menu.locator('[data-binding="fake"]').click();
  await confirmation.waitFor();
  await confirmation.press("Escape");
  await confirmation.waitFor({ state: "detached" });
  expect(changes).toEqual([]);
  expect(await selectedID()).toBe(second);
  await model.click();
  await menu.locator('[data-binding="claude"]').click();
  await menu.waitFor({ state: "hidden" });
  expect(changes).toEqual([]);
  expect(await confirmation.count()).toBe(0);
  await model.click();
  await menu.locator('[data-binding="fake"]').click();
  await confirmation.getByRole("button", { name: "Start new conversation", exact: true }).click();
  await menu.waitFor({ state: "hidden" });
  await page.waitForFunction(id => document.querySelector('[data-testid="session-row"].selected')?.getAttribute("data-session") !== id, second);
  await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement).disabled);
  expect(await selectedID()).not.toBe(second);
  expect(dialogs).toEqual([]);
  page.off("dialog", dismissBrowserDialog);
  expect(changes).toHaveLength(2);
  page.off("request", recordChange);
  expect(await page.getByTestId("session-row").count()).toBe(2);
  expect(await input.inputValue()).toBe("Keep my next question.");
  await page.getByRole("button", { name: "Remove next.txt", exact: true }).click();
  expect(await page.getByTestId("homie-message").count()).toBe(0);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator(`[data-testid="session-row"][data-session="${second}"]`).click();
  await page.getByTestId("homie-message").filter({ hasText: "A goal" }).waitFor();
  await capture("homie-conversation-desktop.png", 1440, 1000);
  await capture("homie-conversation-mobile.png", 390, 844);

  await page.getByTestId("homie-uploads").setInputFiles({ name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("A test attachment.") });
  // The composer reads the file asynchronously before it shows the preview.
  await pane.locator(".conversation-uploads").getByText("notes.txt", { exact: true }).waitFor();
  await input.fill("Please read the file.");
  // These responses isolate the composer from the resident file check.
  const uploadRequests: { request_id: string; body: string; files: { name: string; data: string }[] }[] = [];
  await page.route("**/api/sessions/*/upload", async route => {
    uploadRequests.push(route.request().postDataJSON());
    if (uploadRequests.length === 1) await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "The test interrupts this upload." } }) });
    else await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ session_id: second }) });
  });
  await page.getByTestId("send").click();
  await page.getByTestId("error").waitFor();
  expect(await input.inputValue()).toBe("Please read the file.");
  expect(await pane.locator(".conversation-uploads").textContent()).toContain("notes.txt");
  await page.getByTestId("send").click();
  await page.getByRole("button", { name: "Remove notes.txt", exact: true }).waitFor({ state: "hidden" });
  expect(await input.inputValue()).toBe("");
  expect(uploadRequests).toHaveLength(2);
  expect(uploadRequests[0]).toEqual(uploadRequests[1]);
  expect(uploadRequests[0].body).toBe("Please read the file.");
  expect(uploadRequests[0].files[0]).toMatchObject({ name: "notes.txt", data: Buffer.from("A test attachment.").toString("base64") });
  await capture("homie-attachment-mobile.png", 390, 844);
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 90000);


test("Vision retains its draft beside the shared conversation", async () => {
  const { page } = smoke;
  await seedProject(smoke, "shared-conversation", { title: "Shared conversation", vision: false });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.evaluate(() => { location.hash = "#vision/shared-conversation"; });
  await page.getByTestId("vision-message").waitFor();
  await page.getByTestId("vision-homie-model").waitFor();
  expect(await page.getByTestId("project-field-vision").isVisible()).toBe(true);
  await page.getByRole("button", { name: "Ask me questions about my goal.", exact: true }).click();
  expect(await page.getByTestId("vision-message").inputValue()).toBe("Ask me questions about my goal.");
  await page.screenshot({ path: evidencePath("vision-shared-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByTestId("vision-send").scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("vision-shared-mobile.png"), fullPage: true });
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 30000);
