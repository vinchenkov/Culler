import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => { smoke = await bootSmoke(); await gotoReady(smoke); }, 60000);
afterAll(async () => { await teardown(smoke); });
const selectedID = () => smoke.page.locator('[data-testid="session-row"].selected').getAttribute("data-session");

async function paste(prefix: string, text = "", copies = 1) {
  await smoke.page.getByTestId(prefix === "collie" ? "send-body" : "vision-message").evaluate((input, args) => {
    const clipboard = new DataTransfer();
    clipboard.setData("text/plain", args.text);
    // A valid one-pixel PNG supplies the same file data as a screenshot paste.
    const bytes = Uint8Array.from(atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="), value => value.charCodeAt(0));
    for (let i = 0; i < args.copies; i++) clipboard.items.add(new File([bytes], "image.png", { type: "image/png" }));
    input.dispatchEvent(new ClipboardEvent("paste", { clipboardData: clipboard, bubbles: true, cancelable: true }));
  }, { text, copies });
}

test("empty model changes preserve the draft and reuse a request after a lost response", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-collie").click();
  const input = page.getByTestId("send-body");
  await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement).disabled);
  await input.fill("Keep this draft");
  await paste("collie");
  await page.getByRole("button", { name: "Remove Screenshot-1.png", exact: true }).waitFor();
  const initial = await selectedID();
  const model = page.getByTestId("collie-model");
  const menu = page.getByTestId("collie-model-menu");
  const requests: unknown[] = [];
  let fail = true;
  await page.route("**/api/sessions/*/model", async route => {
    requests.push(route.request().postDataJSON());
    const response = await route.fetch();
    if (fail) {
      fail = false;
      const created = await response.json();
      await mc(["collie", "end", created.session_id, "--reason", "The test ends an idle session."]);
      await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "The response was interrupted." } }) });
    } else await route.fulfill({ response });
  });
  await model.click();
  await menu.locator('[data-binding="claude"]').click();
  await menu.getByRole("button", { name: "Retry", exact: true }).waitFor();
  expect(await input.inputValue()).toBe("Keep this draft");
  await menu.getByRole("button", { name: "Retry", exact: true }).click();
  await menu.waitFor({ state: "hidden" });
  expect(requests).toHaveLength(2);
  expect(requests[0]).toEqual(requests[1]);
  expect(await selectedID()).not.toBe(initial);
  expect(await page.getByTestId("session-row").count()).toBe(1);
  for (const binding of ["fake", "claude", "fake"]) {
    await model.click();
    await menu.locator(`[data-binding="${binding}"]`).click();
    await menu.waitFor({ state: "hidden" });
    expect(await page.getByTestId("session-row").count()).toBe(1);
    expect(await input.inputValue()).toBe("Keep this draft");
    expect(await page.locator(".conversation-draft-file img").count()).toBe(1);
  }
  const current = await selectedID(), count = requests.length;
  await model.click();
  await menu.locator('[data-binding="fake"]').click();
  await menu.waitFor({ state: "hidden" });
  expect(await selectedID()).toBe(current);
  expect(requests.length).toBe(count);
  const list = await mc(["collie", "list"]) as { sessions: { id: string }[] };
  expect(list.sessions.map(row => row.id)).toEqual([current!]);
  await page.unroute("**/api/sessions/*/model");
}, 60000);

test("a default model failure reports the completed conversation change and retries only the default", async () => {
  const { page } = smoke;
  const model = page.getByTestId("collie-model");
  const menu = page.getByTestId("collie-model-menu");
  let fail = true, changes = 0;
  await page.route("**/api/sessions/*/model", async route => { changes++; await route.continue(); });
  await page.route("**/api/routing", async route => {
    if (route.request().method() === "POST" && fail) {
      fail = false;
      await route.fulfill({ status: 503, json: { error: { message: "The default model update failed." } } });
    } else await route.continue();
  });
  await model.click();
  await menu.locator('[data-binding="claude"]').click();
  await menu.getByText(/The conversation uses the selected model/).waitFor();
  const selected = await selectedID();
  await menu.getByRole("button", { name: "Retry", exact: true }).click();
  await menu.waitFor({ state: "hidden" });
  expect(await selectedID()).toBe(selected);
  expect(changes).toBe(1);
  expect(await page.getByTestId("session-row").count()).toBe(1);
  await page.unroute("**/api/sessions/*/model");
  await page.unroute("**/api/routing");
  await model.click();
  await menu.locator('[data-binding="fake"]').click();
  await menu.waitFor({ state: "hidden" });
}, 30000);

test("a concurrent message preserves the previous conversation and explains the result", async () => {
  const { page } = smoke;
  const previous = await selectedID();
  const list = await mc(["collie", "list"]) as { sessions: { id: string; active_bindings: { surface: string; channel_ref: string }[] }[] };
  const surface = list.sessions.find(row => row.id === previous)!.active_bindings.find(row => row.surface === "dashboard")!;
  await page.route("**/api/sessions/*/model", async route => {
    await mc(["collie", "send", previous!, "--from", `dashboard:${surface.channel_ref}`, "--body", "A concurrent message"]);
    await route.fulfill({ response: await route.fetch() });
  });
  await page.getByTestId("collie-model").click();
  const menu = page.getByTestId("collie-model-menu");
  await menu.locator('[data-binding="claude"]').click();
  await menu.getByText("Another message reached the previous conversation. Culler kept that conversation and opened a new conversation.", { exact: true }).waitFor();
  expect(await selectedID()).not.toBe(previous);
  expect(await page.getByTestId("session-row").count()).toBe(2);
  expect(await page.getByTestId("send-body").inputValue()).toBe("Keep this draft");
  const history = await mc(["collie", "history", previous!]) as { messages: { body: string }[] };
  expect(history.messages[0].body).toBe("A concurrent message");
  await menu.press("Escape");
  await page.unroute("**/api/sessions/*/model");
}, 30000);

test("Collie and Vision combine image paste, text, and files with previews and retries", async () => {
  const { page } = smoke;
  for (const prefix of ["collie", "vision"]) {
    if (prefix === "vision") {
      await seedProject(smoke, "paste-project", { title: "Paste Project", vision: false });
      await page.evaluate(() => { location.hash = "#vision/paste-project"; });
      await page.getByTestId("vision-message").waitFor();
      expect(await page.getByTestId("project-field-vision").isVisible()).toBe(true);
    }
    const input = page.getByTestId(prefix === "collie" ? "send-body" : "vision-message");
    const form = input.locator("..");
    const previews = form.locator(".conversation-draft-file");
    if (prefix === "collie") await form.getByRole("button", { name: /Remove Screenshot/ }).click();
    await input.fill("Text: ");
    await paste(prefix, "<b>plain text</b>", 2);
    await page.waitForFunction(id => document.querySelector(`[data-testid="${id}"]`)?.closest("form")?.querySelectorAll(".conversation-draft-file img").length === 1, prefix === "collie" ? "send-body" : "vision-message");
    expect(await input.inputValue()).toBe("Text: <b>plain text</b>");
    expect(await previews.count()).toBe(1);
    expect(await form.locator("b").count()).toBe(0);
    const imageName = await previews.locator("img").getAttribute("alt");
    expect(await previews.locator("img").evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0)).toBe(true);
    await page.getByTestId(`${prefix}-uploads`).setInputFiles({ name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("A file note.") });
    await form.getByRole("button", { name: "Remove notes.txt", exact: true }).waitFor();
    expect(await previews.count()).toBe(2);
    await input.evaluate(input => {
      const clipboard = new DataTransfer();
      clipboard.items.add(new File([new Uint8Array(16 * 1024 * 1024)], "large.png", { type: "image/png" }));
      input.dispatchEvent(new ClipboardEvent("paste", { clipboardData: clipboard, cancelable: true }));
    });
    await form.getByText("Attach at most 16 MiB per message.", { exact: true }).waitFor();
    expect(await previews.count()).toBe(2);
    expect(await input.inputValue()).toBe("Text: <b>plain text</b>");
    await input.evaluate(input => {
      const event = new ClipboardEvent("paste", { cancelable: true });
      Object.defineProperty(event, "clipboardData", { value: {
        items: [{ kind: "file", type: "image/png", getAsFile: () => null }],
        files: [], getData: () => "",
      } });
      input.dispatchEvent(event);
    });
    await form.getByText("Culler could not read the image. Paste the image again.", { exact: true }).waitFor();
    expect(await previews.count()).toBe(2);
    expect(await input.inputValue()).toBe("Text: <b>plain text</b>");
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      await input.scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      await page.screenshot({ path: evidencePath(`${prefix}-paste-${width}.png`), fullPage: true });
    }
    const sent: any[] = [];
    await page.route("**/api/sessions/*/upload", async route => {
      sent.push(route.request().postDataJSON());
      await route.fulfill({ status: sent.length === 1 ? 503 : 200, contentType: "application/json",
        body: JSON.stringify(sent.length === 1 ? { error: { message: "The upload was interrupted." } } : { session_id: "accepted" }) });
    });
    const send = page.getByTestId(prefix === "collie" ? "send" : "vision-send");
    await send.click();
    const error = prefix === "collie" ? page.getByTestId("error") : page.getByTestId("vision-collie-status");
    await error.filter({ hasText: "The upload was interrupted." }).waitFor();
    expect(await previews.count()).toBe(2);
    expect(await input.inputValue()).toBe("Text: <b>plain text</b>");
    await send.click();
    await previews.first().waitFor({ state: "hidden" });
    expect(sent).toHaveLength(2);
    expect(sent[0]).toEqual(sent[1]);
    expect(sent[0].files.map((file: any) => file.name)).toEqual([imageName, "notes.txt"]);
    expect(sent[0].files[0].media_type).toBe("image/png");
    expect(await input.inputValue()).toBe("");
    await page.unroute("**/api/sessions/*/upload");
  }
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

test("a screenshot paste uses the clipboard and an image-only send", async () => {
  const { page, context } = smoke;
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const input = page.getByTestId("vision-message");
  const form = input.locator("..");
  await input.fill("");
  await page.evaluate(async () => { await navigator.clipboard.writeText("Ordinary pasted text"); });
  await input.focus();
  await input.press("ControlOrMeta+v");
  await page.waitForFunction(() => (document.querySelector('[data-testid="vision-message"]') as HTMLTextAreaElement).value === "Ordinary pasted text");
  expect(await form.locator(".conversation-draft-file").count()).toBe(0);
  const screenshot = await page.screenshot();
  await page.evaluate(async data => {
    const bytes = Uint8Array.from(atob(data), value => value.charCodeAt(0));
    await navigator.clipboard.write([new ClipboardItem({ "image/png": new Blob([bytes], { type: "image/png" }) })]);
  }, screenshot.toString("base64"));
  await input.fill("");
  await input.press("ControlOrMeta+v");
  await form.locator(".conversation-draft-file img").waitFor();
  expect(await input.inputValue()).toBe("");
  let payload: any;
  await page.route("**/api/sessions/*/upload", async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({ json: { session_id: "accepted" } });
  });
  await page.getByTestId("vision-send").click();
  await form.locator(".conversation-draft-file").waitFor({ state: "hidden" });
  expect(payload.body).toBe("");
  expect(payload.files).toHaveLength(1);
  expect(payload.files[0].media_type).toBe("image/png");
  expect(Buffer.from(payload.files[0].data, "base64").subarray(0, 8).equals(Buffer.from([137,80,78,71,13,10,26,10]))).toBe(true);
  await page.unroute("**/api/sessions/*/upload");
}, 30000);
