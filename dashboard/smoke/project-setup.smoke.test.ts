import { afterAll, beforeAll, expect, test } from "bun:test";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { evidencePath, bootSmoke, gotoReady, mc, teardown, seedProject, MC_BIN, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => { smoke = await bootSmoke(); await gotoReady(smoke); }, 60000);
afterAll(async () => { await teardown(smoke); });

async function post(action: string, body: object) {
  return smoke.page.evaluate(async ({ action, body }) => {
    const response = await fetch(`/api/projects/${action}`, { method: "POST", headers: { authorization: "Bearer smoke-token", "content-type": "application/json" }, body: JSON.stringify(body) });
    const result = await response.json();
    if (!response.ok) throw new Error(JSON.stringify(result));
    return result;
  }, { action, body });
}
async function apply(id: string, submission_id: string) {
  const proc = Bun.spawn([MC_BIN, "project", "vision-apply"], { env: process.env, stdin: new TextEncoder().encode(JSON.stringify({ id, submission_id })), stdout: "pipe", stderr: "pipe" });
  const [code, out, error] = await Promise.all([proc.exited, new Response(proc.stdout).text(), new Response(proc.stderr).text()]);
  expect(code, error).toBe(0);
  return JSON.parse(out);
}
async function register(path: string, title: string) {
  await smoke.page.getByTestId("tab-board").click();
  await smoke.page.getByTestId("project-add").evaluate((node: HTMLElement) => node.click());
  await smoke.page.getByTestId("project-field-path").fill(path);
  await smoke.page.getByTestId("project-field-title").fill(title);
  await smoke.page.waitForFunction(() => !document.querySelector<HTMLButtonElement>('[data-testid="project-next"]')?.disabled);
  await smoke.page.getByTestId("project-next").click();
  await visionReady();
  return smoke.page.url().split("#vision/")[1];
}
async function visionReady() {
  await smoke.page.waitForFunction(() => location.hash.startsWith("#vision/") && document.querySelector('#vision-pane:not([hidden])[aria-busy="false"] [data-testid="project-field-vision"]'));
}
async function savedDraft() {
  await smoke.page.waitForFunction(() => document.querySelector('[data-testid="vision-draft-status"]')?.textContent?.startsWith("Draft saved"));
}


test("folder selection registers a paused Project before the persistent Vision page", async () => {
  const { page } = smoke;
  const path = join(smoke.scratch, "new-project");
  await page.getByTestId("board-add-project").click();
  expect(await page.getByTestId("project-check").count()).toBe(0);
  expect(await page.getByTestId("project-field-vision").count()).toBe(0);
  expect(await page.getByTestId("project-next").isDisabled()).toBe(true);
  await page.route("**/api/projects/choose-folder", route => route.fulfill({ json: { path: `${path}/` } }));
  await page.getByTestId("project-choose-folder").click();
  await page.waitForFunction(() => !document.querySelector<HTMLButtonElement>('[data-testid="project-next"]')?.disabled);
  expect(await page.getByTestId("project-field-path").inputValue()).toBe(path);
  await page.getByTestId("project-field-title").fill("Garden");
  await page.getByTestId("project-next").click();
  await visionReady();
  expect(await page.getByTestId("vision-pane").getByTestId("project-field-title").count()).toBe(0);
  expect(await page.getByTestId("vision-pane").getByTestId("project-field-path").count()).toBe(0);
  const id = page.url().split("#vision/")[1];
  const rows = (await mc(["project", "list"])).projects as any[];
  expect(rows).toHaveLength(1);
  expect(rows[0].status).toBe("paused");
  expect(rows[0].setup_status).toBe("vision");
  expect(existsSync(join(path, ".git"))).toBe(true);
  expect(existsSync(join(path, "VISION.md"))).toBe(false);
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe("");
  expect(await page.getByTestId("project-start").isDisabled()).toBe(true);
  await page.getByTestId("project-field-vision").fill("Help families grow vegetables.");
  await savedDraft();
  expect(existsSync(join(path, "VISION.md"))).toBe(false);
  await page.reload();
  await visionReady();
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe("Help families grow vegetables.");
  await page.getByTestId("project-save").click();
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("awaits confirmation"));
  const { draft } = await post("vision-draft", { id });
  expect(await page.getByTestId("project-start").isDisabled()).toBe(true);
  await apply(id, draft.submission.submission_id);
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("Initialization is incomplete"));
  expect(await page.getByTestId("project-result").innerText()).toContain(`Vision saved to ${path}/VISION.md.`);
  expect(await page.getByTestId("project-result").innerText()).toContain("Initialize project permits setup and development from this Vision.");
  expect(readFileSync(join(path, "VISION.md"), "utf8")).toBe("Help families grow vegetables.");
  expect(((await mc(["project", "list"])).projects as any[])[0].status).toBe("paused");
  await page.waitForFunction(() => !document.querySelector<HTMLButtonElement>('[data-testid="project-start"]')?.disabled);
  const evidence = evidencePath("culler-vision-browser");
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: join(evidence, "vision-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: join(evidence, "vision-narrow.png"), fullPage: true });
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.getByTestId("tab-board").click();
  await page.getByTestId("board-project").getByText("Initialization is incomplete", { exact: true }).waitFor();
  await page.getByTestId("board-project").getByRole("button", { name: "Open Vision" }).click();
  await page.getByTestId("project-start").waitFor();
  await page.route("**/api/projects/start", (route) => route.fulfill({
    status: 400, json: { error: { message: "Start test refusal." } },
  }));
  try {
    await page.getByTestId("project-start").click();
    await page.getByTestId("vision-start-error").getByText("Start test refusal.").waitFor();
  } finally { await page.unroute("**/api/projects/start"); }
  expect((await post("vision-draft", { id })).draft.setup_status).toBe("configured");
  await page.getByTestId("project-field-vision").fill("Help families grow food near their homes.");
  await savedDraft();
  expect(await page.getByTestId("project-start").isDisabled()).toBe(true);
  await page.getByTestId("project-save").click();
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("awaits confirmation"));
  const updated = (await post("vision-draft", { id })).draft;
  await apply(id, updated.submission.submission_id);
  await page.waitForFunction(() => !document.querySelector<HTMLButtonElement>('[data-testid="project-start"]')?.disabled);
  await mc(["onboard", "surfaces"]);
  expect(await page.getByTestId("vision-start-error").isVisible()).toBe(true);
  await post("start", {
    id, identity: updated.workspace_identity, revision: updated.revision,
    submission_id: updated.submission.submission_id,
  });
  await page.getByTestId("project-start").getByText("View setup", { exact: true }).waitFor();
  await page.getByTestId("vision-start-error").waitFor({ state: "hidden" });
  expect(await page.getByTestId("project-result").innerText()).toBe(`Vision saved to ${path}/VISION.md.`);
  const started = (await post("vision-draft", { id })).draft;
  expect(started.setup_status).toBe("configured");
  expect(started.project_status).toBe("paused");
  expect(started.setup_id).toBeTruthy();
  expect(await page.getByTestId("project-start").innerText()).toBe("View setup");
  await page.reload();
  await visionReady();
  expect(await page.getByTestId("project-start").innerText()).toBe("View setup");
  expect(await page.getByRole("button", { name: "Project details", exact: true }).count()).toBe(0);
}, 60000);

test("an imported Vision survives registration and an external file requires comparison", async () => {
  const { page } = smoke;
  const path = join(smoke.scratch, "imported"); mkdirSync(path);
  const original = "# Vision\n\nHelp people reduce household energy use.\n";
  writeFileSync(join(path, "VISION.md"), original);
  const id = await register(path, "Energy");
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe(original);
  expect(await page.getByTestId("project-start").isDisabled()).toBe(true);
  expect(readFileSync(join(path, "VISION.md"), "utf8")).toBe(original);
  writeFileSync(join(path, "VISION.md"), "An external goal.\n");
  await page.getByTestId("project-save").click();
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("awaits confirmation"));
  const { draft } = await post("vision-draft", { id });
  await apply(id, draft.submission.submission_id);
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("draft remains"));
  expect(readFileSync(join(path, "VISION.md"), "utf8")).toBe("An external goal.\n");
  await page.getByTestId("vision-workspace-copy").waitFor();
  expect(await page.getByTestId("vision-workspace-copy").innerText()).toContain("An external goal.");
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe(original);
  await page.getByTestId("vision-rebase").click();
  await page.getByTestId("vision-rebase").waitFor({ state: "detached" });
  await page.getByTestId("project-save").click();
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("awaits confirmation"));
  const next = await post("vision-draft", { id });
  await apply(id, next.draft.submission.submission_id);
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("Initialization is incomplete"));
  expect(readFileSync(join(path, "VISION.md"), "utf8")).toBe(original);
  await page.waitForFunction(() => !document.querySelector<HTMLButtonElement>('[data-testid="project-start"]')?.disabled);
  await mc(["onboard", "surfaces"]);
  await page.getByTestId("project-start").click();
  await page.waitForFunction(() => location.hash === "#board");
  expect(await page.getByTestId("tab-board").getAttribute("aria-current")).toBe("page");
  expect(await page.getByTestId("vision-pane").isVisible()).toBe(false);
  expect(await page.getByTestId("project-button").innerText()).toContain("Energy");
  await page.waitForFunction(() => document.querySelector<HTMLDivElement>('[data-testid="board-columns"]')?.hidden);
  await page.getByTestId("setup-panel").waitFor();
  expect(await page.getByTestId("board-project").innerText()).not.toContain("Initialization is incomplete");
  // The scratch instance has no resident to advance setup.
  await page.waitForFunction(() => document.querySelector('[data-testid="setup-phase"]')?.textContent === "Setup status unavailable");
  expect(await page.getByTestId("setup-phase").locator(".journal-pulse").count()).toBe(0);
  expect(await page.getByTestId("setup-panel").getByRole("button", { name: /pause/i }).count()).toBe(0);
  const started = (await post("vision-draft", { id })).draft;
  expect(started.setup_status).toBe("configured");
  expect(started.project_status).toBe("paused");
  expect(started.setup_id).toBeTruthy();
  await page.evaluate(id => { location.hash = `#vision/${id}`; }, id);
  await visionReady();
  expect(await page.getByTestId("project-start").innerText()).toBe("View setup");
}, 40000);

test("a stale edit preserves both drafts and Collie failure leaves the editor available", async () => {
  const { page } = smoke;
  const id = page.url().split("#vision/")[1];
  const initial = await post("vision-draft", { id });
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/api/projects/vision-edit", async route => { await held; await route.continue(); });
  await page.getByTestId("project-field-vision").fill("My local goal.");
  await page.waitForFunction(() => document.querySelector('[data-testid="vision-draft-status"]')?.textContent?.includes("saves the draft"));
  const runner = Bun.spawn([MC_BIN, "project", "vision-edit"], { env: process.env, stdin: new TextEncoder().encode(JSON.stringify({ id, revision: initial.draft.revision, content: "A goal from another editor." })), stdout: "pipe", stderr: "pipe" });
  expect(await runner.exited).toBe(0);
  release();
  await page.getByTestId("vision-remote-copy").waitFor();
  expect(await page.getByTestId("vision-remote-copy").innerText()).toBe("A goal from another editor.");
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe("My local goal.");
  expect(await page.getByTestId("project-save").isDisabled()).toBe(true);
  await page.unroute("**/api/projects/vision-edit");
  await page.getByTestId("vision-keep-local").click(); await savedDraft();
  await page.route("**/api/sessions", route => route.fulfill({ status: 503, json: { error: { message: "Collie is unavailable." } } }));
  await page.reload(); await visionReady();
  expect(await page.getByTestId("project-field-vision").isEnabled()).toBe(true);
  expect(await page.getByTestId("project-save").isEnabled()).toBe(true);
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe("My local goal.");
  await page.unroute("**/api/sessions");
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror"))).toEqual([]);
}, 40000);

test("Vision conversations retain their Project and the shared upload control preserves text during Send", async () => {
  const { page } = smoke;
  const firstID = page.url().split("#vision/")[1];
  await page.getByTestId("vision-message").fill("Help me state the goal.");
  await page.getByTestId("vision-send").click();
  await page.getByTestId("vision-transcript").getByText("Help me state the goal.", { exact: true }).waitFor();
  let sessions = (await mc(["collie", "list"])).sessions as any[];
  const first = sessions.find(row => row.vision_project === firstID);
  expect(first).toBeDefined();
  await page.reload(); await visionReady();
  await page.getByTestId("vision-transcript").getByText("Help me state the goal.", { exact: true }).waitFor();
  const secondID = await register(join(smoke.scratch, "third-project"), "Other Project");
  await page.getByTestId("vision-message").fill("Discuss this other Project.");
  await page.getByTestId("vision-send").click();
  await page.getByTestId("vision-transcript").getByText("Discuss this other Project.", { exact: true }).waitFor();
  sessions = (await mc(["collie", "list"])).sessions as any[];
  const second = sessions.find(row => row.vision_project === secondID);
  expect(second.id).not.toBe(first.id);
  expect(await page.getByTestId("vision-transcript").getByText("Help me state the goal.", { exact: true }).count()).toBe(0);
  await page.getByTestId("tab-collie").click();
  expect(await page.getByTestId("session-row").filter({ hasText: first.id }).count()).toBe(0);
  expect(await page.getByTestId("collie-uploads").count()).toBe(1);
  await page.evaluate(id => { location.hash = `#vision/${id}`; }, firstID);
  await page.getByTestId("vision-transcript").getByText("Help me state the goal.", { exact: true }).waitFor();
  await page.getByTestId("vision-uploads").setInputFiles({ name: "goal.txt", mimeType: "text/plain", buffer: Buffer.from("A file goal.") });
  let payload: any;
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  await page.route(`**/api/sessions/${first.id}/upload`, async route => {
    payload = route.request().postDataJSON();
    await held;
    await route.fulfill({ json: { session_id: first.id } });
  });
  await page.getByTestId("vision-message").fill("Use the attached goal.");
  await page.getByTestId("vision-send").click();
  for (let attempt = 0; !payload && attempt < 100; attempt++) await new Promise(resolve => setTimeout(resolve, 20));
  expect(payload.files).toEqual([{ name: "goal.txt", media_type: "text/plain", data: Buffer.from("A file goal.").toString("base64") }]);
  expect(payload.request_id).toBeTruthy();
  await page.getByTestId("vision-message").fill("This is my next message.");
  release();
  await page.waitForFunction(() => !document.querySelector<HTMLButtonElement>('[data-testid="vision-send"]')?.disabled);
  expect(await page.getByTestId("vision-message").inputValue()).toBe("This is my next message.");
  await page.unroute(`**/api/sessions/${first.id}/upload`);
}, 40000);

test("a delayed submission response cannot replace a newer draft revision", async () => {
  const { page } = smoke;
  const id = page.url().split("#vision/")[1];
  await page.getByTestId("project-field-vision").fill("The submitted goal."); await savedDraft();
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  let accepted = false;
  await page.route("**/api/projects/vision-submit", async route => {
    const response = await route.fetch(); accepted = true; await held; await route.fulfill({ response });
  });
  await page.getByTestId("project-save").click();
  for (let attempt = 0; !accepted && attempt < 100; attempt++) await new Promise(resolve => setTimeout(resolve, 20));
  expect(accepted).toBe(true);
  await page.getByTestId("project-field-vision").fill("A later goal remains in the draft."); await savedDraft();
  const newer = await post("vision-draft", { id });
  release();
  await page.unroute("**/api/projects/vision-submit");
  await apply(id, newer.draft.submission.submission_id);
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("changes beyond"));
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe("A later goal remains in the draft.");
  const latest = await post("vision-draft", { id });
  expect(latest.draft.revision).toBe(newer.draft.revision);
  expect(readFileSync(join(latest.draft.workspace_path, "VISION.md"), "utf8")).toBe("The submitted goal.");
}, 40000);

test("an invalid workspace Vision preserves the persistent draft editor", async () => {
  const { page } = smoke;
  const id = page.url().split("#vision/")[1];
  const { draft } = await post("vision-draft", { id });
  writeFileSync(join(draft.workspace_path, "VISION.md"), Buffer.from([0xff, 0xfe, 0x00]));
  await page.reload(); await visionReady();
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe(draft.content);
  expect(await page.getByTestId("project-field-vision").isEnabled()).toBe(true);
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe(draft.content);
  writeFileSync(join(draft.workspace_path, "VISION.md"), "The submitted goal.");
}, 20000);


test("an existing ready Project opens its first draft and preserves its active state", async () => {
  const { page } = smoke;
  const path = await seedProject(smoke, "legacy-project");
  await mc(["project", "resume", "legacy-project"]);
  await page.evaluate(() => { location.hash = "#vision/legacy-project"; });
  await page.waitForFunction(expected => document.querySelector('[data-testid="vision-destination"]')?.textContent === expected, `${path}/VISION.md`);
  expect(await page.getByTestId("project-field-vision").inputValue()).toBe(readFileSync(join(path, "VISION.md"), "utf8"));
  await page.getByTestId("project-save").click();
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("awaits confirmation"));
  const { draft } = await post("vision-draft", { id: "legacy-project" });
  await apply("legacy-project", draft.submission.submission_id);
  await page.waitForFunction(() => document.querySelector('[data-testid="project-result"]')?.textContent?.includes("Vision saved to"));
  expect(((await mc(["project", "list"])).projects as any[]).find(row => row.id === "legacy-project").status).toBe("active");
}, 20000);

test("an archived Project permits inspection and disables draft and conversation writes", async () => {
  const { page } = smoke;
  const path = await seedProject(smoke, "archived-project");
  await post("vision-open", { id: "archived-project" });
  await mc(["project", "archive", "archived-project"]);
  await page.evaluate(() => { location.hash = "#vision/archived-project"; });
  await page.waitForFunction(expected => document.querySelector('[data-testid="vision-destination"]')?.textContent === expected, `${path}/VISION.md`);
  await page.getByTestId("vision-pane").waitFor();
  expect(await page.getByTestId("project-field-vision").getAttribute("readonly")).not.toBeNull();
  expect(await page.getByTestId("project-save").isDisabled()).toBe(true);
  expect(await page.getByTestId("project-start").isVisible()).toBe(false);
  expect(await page.getByTestId("vision-message").isDisabled()).toBe(true);
  expect(await page.getByTestId("vision-send").isDisabled()).toBe(true);
  expect(await page.getByTestId("vision-uploads").isDisabled()).toBe(true);
  expect(await page.getByTestId("project-result").innerText()).toContain("archived");
  await seedProject(smoke, "archived-without-draft");
  await mc(["project", "archive", "archived-without-draft"]);
  await page.evaluate(() => { location.hash = "#vision/archived-without-draft"; });
  await page.getByTestId("vision-pane").getByText("This Project is archived. Its Vision draft is unavailable.", { exact: true }).waitFor();
}, 20000);

test("reply retry shows immediate progress across controls and survives a history refresh", async () => {
  const { page } = smoke;
  const id = await register(join(smoke.scratch, "retry-project"), "Retry Project");
  await page.getByTestId("vision-message").fill("Inspect this Project.");
  await page.getByTestId("vision-send").click();
  await page.getByTestId("vision-transcript").getByText("Inspect this Project.", { exact: true }).waitFor();
  const session = (await mc(["collie", "list"])).sessions.find((row: any) => row.vision_project === id);
  let replyState = "failed", calls = 0;
  let release!: () => void;
  let held = new Promise<void>(resolve => { release = resolve; });
  const historyPath = `**/api/sessions/${session.id}/history`;
  const condition = { id: "reply:1", name: "Collie", group: "execution", state: "failed", action: "Retry reply", session_id: session.id, message_id: 1, attempt_id: "old-attempt" };
  await page.route(historyPath, route => route.fulfill({ json: {
    session_id: session.id, status: "active", messages: [{ id: 1, direction: "inbound", body: "Inspect this Project.", created_at: "2020-01-01T00:00:00Z", reply_state: replyState, attempt_id: replyState === "failed" ? "old-attempt" : "new-attempt" }],
  } }));
  await page.route("**/api/system/status", route => route.fulfill({ json: {
    complete: true, observed_at: new Date().toISOString(), connections: [], conditions: replyState === "failed" ? [condition] : [],
  } }));
  await page.route("**/api/collie/reply-retry", async route => {
    calls++;
    await held;
    if (calls === 1) await route.fulfill({ status: 503, json: { error: { message: "Retry request failed." } } });
    else {
      replyState = "authorized";
      await route.fulfill({ json: { session_id: session.id, attempt_id: "new-attempt" } });
    }
  });
  try {
    await page.reload();
    const transcript = page.getByTestId("vision-transcript");
    const notices = page.getByTestId("system-notices");
    await transcript.getByRole("button", { name: "Retry reply", exact: true }).waitFor();
    await notices.getByRole("button", { name: "Retry reply", exact: true }).waitFor();
    await transcript.getByRole("button", { name: "Retry reply", exact: true }).click();
    expect(await transcript.getByRole("button", { name: "Retry requested…", exact: true }).isDisabled()).toBe(true);
    expect(await notices.getByRole("button", { name: "Retry requested…", exact: true }).isDisabled()).toBe(true);
    await notices.getByRole("button", { name: "Retry requested…", exact: true }).evaluate(node => node.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    await page.waitForResponse(response => response.url().endsWith(`/api/sessions/${session.id}/history`));
    expect(await transcript.getByRole("button", { name: "Retry requested…", exact: true }).isDisabled()).toBe(true);
    expect(calls).toBe(1);
    expect(await page.getByTestId("project-field-vision").isEnabled()).toBe(true);
    await page.screenshot({ path: evidencePath("culler-reply-retry-progress.png"), fullPage: true });
    release();
    await transcript.getByRole("button", { name: "Retry reply", exact: true }).waitFor();
    expect(await transcript.getByRole("button", { name: "Retry reply", exact: true }).isEnabled()).toBe(true);
    expect(await page.getByTestId("error").innerText()).toContain("Retry request failed.");
    held = new Promise<void>(resolve => { release = resolve; });
    await notices.getByRole("button", { name: "Retry reply", exact: true }).click();
    expect(await transcript.getByRole("button", { name: "Retry requested…", exact: true }).isDisabled()).toBe(true);
    release();
    await transcript.getByText("Reply queued", { exact: true }).waitFor();
    expect(await transcript.locator(".thinking").count()).toBe(1);
    replyState = "active";
    await transcript.getByText("Reply in progress", { exact: true }).waitFor();
    expect(await transcript.getByText("Reply delayed", { exact: true }).count()).toBe(0);
    expect(calls).toBe(2);
  } finally {
    release();
    await page.unroute(historyPath);
    await page.unroute("**/api/system/status");
    await page.unroute("**/api/collie/reply-retry");
    await page.getByTestId("tab-board").click();
  }
}, 40000);

test("Clear ends the Vision conversation and a reload shows the empty conversation", async () => {
  const { page } = smoke;
  const id = await register(join(smoke.scratch, "clear-project"), "Clear Project");
  expect(await page.getByTestId("vision-clear").isVisible()).toBe(false);
  await page.getByTestId("vision-message").fill("An old goal.");
  await page.getByTestId("vision-send").click();
  await page.getByTestId("vision-transcript").getByText("An old goal.", { exact: true }).waitFor();
  await page.getByTestId("vision-clear").waitFor({ state: "visible" });
  page.once("dialog", dialog => dialog.dismiss());
  await page.getByTestId("vision-clear").click();
  expect(await page.getByTestId("vision-transcript").getByText("An old goal.", { exact: true }).count()).toBe(1);
  page.once("dialog", dialog => dialog.accept());
  await page.getByTestId("vision-clear").click();
  await page.getByTestId("vision-clear").waitFor({ state: "hidden" });
  expect(await page.getByTestId("vision-transcript").getByText("An old goal.", { exact: true }).count()).toBe(0);
  const sessions = ((await mc(["collie", "list"])).sessions as any[]).filter(row => row.vision_project === id);
  expect(sessions.map(row => row.status)).toEqual(["ended", "active"]);
  await page.reload(); await visionReady();
  await page.getByTestId("vision-transcript").getByText("Start with the goal").waitFor();
  expect(await page.getByTestId("vision-transcript").getByText("An old goal.", { exact: true }).count()).toBe(0);
  await page.getByTestId("vision-message").fill("A new goal.");
  await page.getByTestId("vision-send").click();
  await page.getByTestId("vision-transcript").getByText("A new goal.", { exact: true }).waitFor();
  const after = ((await mc(["collie", "list"])).sessions as any[]).filter(row => row.vision_project === id);
  expect(after.map(row => row.id)).toEqual(sessions.map(row => row.id));
}, 40000);

test("a folder response preserves the Project name draft, focus, selection, and body position", async () => {
  const { page } = smoke;
  let release!: () => void;
  let requested!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  const seen = new Promise<void>(resolve => { requested = resolve; });
  await page.setViewportSize({ width: 390, height: 667 });
  await page.evaluate(() => sessionStorage.removeItem("mc-project-draft"));
  await page.getByTestId("tab-board").click();
  await page.getByTestId("project-add").evaluate((node: HTMLElement) => node.click());
  await page.route("**/api/projects/setup-preview", async route => { requested(); await gate; await route.continue(); });
  try {
    await page.getByTestId("project-field-path").fill(join(smoke.scratch, "selection-project"));
    await seen;
    const name = page.getByTestId("project-field-title");
    await name.fill("Selection Project");
    await name.evaluate((node: HTMLInputElement) => node.setSelectionRange(2, 8, "backward"));
    const body = page.getByTestId("project-dialog").locator(".ui-dialog-body");
    const scrollTop = await body.evaluate(node => node.scrollTop);
    release();
    await page.waitForFunction(() => !document.querySelector<HTMLButtonElement>('[data-testid="project-next"]')?.disabled);
    expect(await name.inputValue()).toBe("Selection Project");
    expect(await name.evaluate((node: HTMLInputElement) => [node === document.activeElement, node.selectionStart, node.selectionEnd, node.selectionDirection]))
      .toEqual([true, 2, 8, "backward"]);
    expect(await body.evaluate(node => node.scrollTop)).toBe(scrollTop);
  } finally {
    release();
    await page.unroute("**/api/projects/setup-preview");
    await page.getByTestId("project-close").click();
    await page.evaluate(() => sessionStorage.removeItem("mc-project-draft"));
    await page.setViewportSize({ width: 1280, height: 720 });
  }
}, 30000);
