import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let sessionID: string;
let identity: string;
beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "board-alpha", { title: "Alpha" });
  await seedProject(smoke, "board-beta", { title: "Beta" });
  await gotoReady(smoke);
  await selectProject(smoke, "board-alpha");
}, 60000);
afterAll(async () => { await teardown(smoke); }, 30000);

async function readyToSend() {
  await smoke.page.waitForFunction(() => {
    const input = document.querySelector('[data-testid="board-homie-message"]') as HTMLTextAreaElement;
    return input && !input.disabled;
  });
}

async function claim() {
  const result = await mc(["homie", "claim", sessionID], { MC_RUN_JSON: identity }) as { message: { id: number; body: string } };
  expect(result.message).toBeTruthy();
  return result.message;
}

async function reply(id: number, text: string) {
  await mc(["homie", "reply", sessionID, "--to", String(id), "--body", text], { MC_RUN_JSON: identity });
  await smoke.page.getByTestId("board-homie-transcript").getByTestId("homie-message").filter({ hasText: text }).waitFor();
  await readyToSend();
}

test("the empty input cycles through suggestions and preserves the draft", async () => {
  const { page } = smoke;
  const input = page.getByTestId("board-homie-message");
  await readyToSend();
  expect(await input.getAttribute("placeholder")).toBe("What needs my attention?");
  for (const text of ["Explain the blocked tasks.", "What changed recently?", "What needs my attention?"]) {
    await page.waitForFunction(text => document.querySelector<HTMLTextAreaElement>('[data-testid="board-homie-message"]')?.placeholder === text, text);
    if (text === "Explain the blocked tasks.") {
      const suggestion = page.getByTestId("board-homie-suggestion");
      expect(await suggestion.evaluate(node => node.getAnimations({ subtree: true }).length)).toBe(2);
      for (const time of [0, 80, 419]) {
        await suggestion.evaluate((node, time) => {
          for (const animation of node.getAnimations({ subtree: true })) { animation.pause(); animation.currentTime = time; }
        }, time);
        await page.getByTestId("board-command").screenshot({ path: evidencePath(`commandbar-transition-${time}.png`) });
      }
      await suggestion.evaluate(node => { for (const animation of node.getAnimations({ subtree: true })) animation.play(); });
    }
  }
  await input.focus();
  await page.waitForTimeout(4200);
  expect(await input.getAttribute("placeholder")).toBe("What needs my attention?");
  await input.fill("Keep this draft.");
  await input.blur();
  await page.waitForTimeout(4200);
  expect(await input.inputValue()).toBe("Keep this draft.");
  expect(await input.getAttribute("placeholder")).toBe("What needs my attention?");
  expect(await page.getByTestId("board-homie-suggestion").isVisible()).toBe(false);
  await input.fill("");
  await page.emulateMedia({ reducedMotion: "reduce" });
  await input.blur();
  await page.waitForFunction(() => document.querySelector<HTMLTextAreaElement>('[data-testid="board-homie-message"]')?.placeholder === "Explain the blocked tasks.");
  expect(await page.getByTestId("board-homie-suggestion").evaluate(node => node.getAnimations({ subtree: true }).length)).toBe(0);
  await page.emulateMedia({ reducedMotion: "no-preference" });
  expect((await mc(["homie", "list"]) as { sessions: unknown[] }).sessions).toHaveLength(0);
}, 30000);

test("the Board sends real messages, retains follow-ups, and opens the same Homie session", async () => {
  const { page, scratch } = smoke;
  await readyToSend();
  expect((await mc(["homie", "list"]) as { sessions: unknown[] }).sessions).toHaveLength(0);
  await page.getByTestId("board-homie-message").fill("What needs my attention?");
  expect(await page.getByTestId("board-homie-message").inputValue()).toBe("What needs my attention?");
  await page.getByTestId("board-homie-message").press("Enter");
  await page.getByTestId("board-homie-transcript").getByText("What needs my attention?", { exact: true }).waitFor();
  expect(await page.getByTestId("board-pane").isVisible()).toBe(true);
  expect(await page.getByTestId("homie-pane").isVisible()).toBe(false);
  const sessions = (await mc(["homie", "list"])) as { sessions: { id: string; verb_allowlist: string[] }[] };
  expect(sessions.sessions).toHaveLength(1);
  sessionID = sessions.sessions[0]!.id;
  identity = join(scratch, "commandbar-run.json");
  writeFileSync(identity, JSON.stringify({ run_id: sessionID, tier: "homie", role: "homie", verb_allowlist: sessions.sessions[0]!.verb_allowlist }));
  const message = await claim();
  expect(message.body).toContain('"project_id":"board-alpha"');
  expect(message.body).toContain('"project_title":"Alpha"');
  expect(message.body).toContain("ask for confirmation first");
  expect(message.body.endsWith("What needs my attention?")).toBe(true);
  expect(await page.getByTestId("board-homie-message").isDisabled()).toBe(true);
  await mc(["homie", "draft", sessionID, "--to", String(message.id), "--body", "I will check Alpha's tasks."], { MC_RUN_JSON: identity });
  await page.getByTestId("board-homie-transcript").getByTestId("homie-draft").filter({ hasText: "I will check Alpha's tasks." }).waitFor();
  await reply(message.id, "Alpha has no tasks that need a decision.");

  await page.getByTestId("board-homie-message").fill("What is the next step?");
  await page.getByTestId("board-homie-send").click();
  await page.getByTestId("board-homie-transcript").getByText("What is the next step?", { exact: true }).waitFor();
  const second = await claim();
  await reply(second.id, "Review the Project Vision before you start the pipeline.");
  expect((await mc(["homie", "list"]) as { sessions: unknown[] }).sessions).toHaveLength(1);
  expect(await page.getByTestId("board-homie-transcript").textContent()).toContain("Alpha has no tasks");
  await page.getByTestId("board-homie-message").fill("Keep this draft.");
  await page.getByTestId("board-homie-toggle").click();
  expect(await page.getByTestId("board-homie-transcript").isVisible()).toBe(false);
  expect(await page.getByTestId("board-homie-message").inputValue()).toBe("Keep this draft.");
  await page.getByLabel("Conversation actions").click();
  await page.getByTestId("board-homie-open").click();
  await page.getByTestId("homie-pane").waitFor({ state: "visible" });
  await page.getByTestId("homie-transcript").getByText("Alpha has no tasks that need a decision.", { exact: true }).waitFor();
  expect(await page.getByTestId("homie-transcript").textContent()).not.toContain("Dashboard Board context:");
  expect(await page.getByTestId("board-command").isVisible()).toBe(false);
  expect(await page.locator('[data-testid="session-row"].selected').getAttribute("data-session")).toBe(sessionID);
  await page.getByTestId("tab-board").click();
  await readyToSend();
  expect(await page.getByTestId("board-homie-message").inputValue()).toBe("Keep this draft.");
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await readyToSend();
  await page.getByTestId("board-homie-toggle").click();
  await page.getByTestId("board-homie-transcript").getByText("Alpha has no tasks that need a decision.", { exact: true }).waitFor();
}, 60000);

test("a failed send retains text and a Project change isolates drafts and late replies", async () => {
  const { page } = smoke;
  const input = page.getByTestId("board-homie-message");
  let attempts = 0;
  await page.route("**/api/sessions/*/send", async route => {
    attempts++;
    await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "The connection was interrupted." } }) });
  });
  await input.fill("Keep my question after a failed send.");
  await page.getByTestId("board-homie-send").click();
  await page.getByTestId("board-homie-error").filter({ hasText: "The connection was interrupted." }).waitFor();
  expect(await page.getByTestId("board-homie-status").textContent()).toBe("The last request failed.");
  expect(await input.inputValue()).toBe("Keep my question after a failed send.");
  expect(attempts).toBe(1);
  await page.unroute("**/api/sessions/*/send");
  await page.getByTestId("board-homie-refresh").click();
  await page.getByTestId("board-homie-error").waitFor({ state: "hidden" });
  expect(await input.inputValue()).toBe("Keep my question after a failed send.");
  await page.getByTestId("board-homie-send").click();
  await page.getByTestId("board-homie-transcript").getByText("Keep my question after a failed send.", { exact: true }).waitFor();
  const message = await claim();
  expect(message.body).toContain('"project_title":"Alpha"');
  await selectProject(smoke, "board-beta");
  await readyToSend();
  expect(await page.getByTestId("board-homie-transcript").textContent()).not.toContain("Alpha has no tasks");
  await input.fill("Beta draft");
  await mc(["homie", "reply", sessionID, "--to", String(message.id), "--body", "This reply belongs to Alpha."], { MC_RUN_JSON: identity });
  expect(await page.getByTestId("board-homie-transcript").textContent()).not.toContain("This reply belongs to Alpha.");
  await selectProject(smoke, "board-alpha");
  await readyToSend();
  await page.getByTestId("board-homie-transcript").getByText("This reply belongs to Alpha.", { exact: true }).waitFor();
  await selectProject(smoke, "board-beta");
  expect(await input.inputValue()).toBe("Beta draft");
  await selectProject(smoke, "board-alpha");
}, 60000);

test("Send resumes an ended conversation and the command bar fits both themes and narrow screens", async () => {
  const { page } = smoke;
  await mc(["run", "register-session", sessionID, "--native-ref", "board-native", "--file", "native.jsonl"], { MC_RUN_JSON: identity });
  await mc(["homie", "end", sessionID, "--reason", "idle timeout"]);
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await readyToSend();
  expect(await page.getByTestId("board-homie-resume").count()).toBe(0);
  expect(await page.getByTestId("board-homie-status").isVisible()).toBe(false);
  expect((await mc(["homie", "history", sessionID]) as { status: string }).status).toBe("ended");

  const input = page.getByTestId("board-homie-message");
  await input.fill("Continue our conversation.");
  await page.route("**/api/sessions/*/send", route => route.fulfill({
    status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "The server is unavailable." } }),
  }));
  await page.getByTestId("board-homie-send").click();
  await page.getByTestId("board-homie-error").waitFor();
  await readyToSend();
  expect(await input.inputValue()).toBe("Continue our conversation.");
  expect((await mc(["homie", "history", sessionID]) as { status: string }).status).toBe("ended");
  await page.unroute("**/api/sessions/*/send");
  await page.getByTestId("board-homie-send").click();
  await page.getByTestId("board-homie-transcript").getByText("Continue our conversation.", { exact: true }).waitFor();
  expect(await savedSession()).toBe(sessionID);
  expect((await mc(["homie", "history", sessionID]) as { status: string }).status).toBe("active");
  await reply((await claim()).id, "The same conversation continues.");
  expect(await page.getByTestId("board-homie-transcript").textContent()).toContain("Alpha has no tasks");

  await mc(["homie", "end", sessionID, "--reason", "idle timeout"]);
  await page.locator('[data-testid="board-command"] input[type="file"]').setInputFiles({
    name: "question.txt", mimeType: "text/plain", buffer: Buffer.from("Continue with this attachment."),
  });
  await page.route("**/api/sessions/*/upload", route => route.fulfill({
    status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "The upload was interrupted." } }),
  }));
  await page.getByTestId("board-homie-send").click();
  await page.getByTestId("board-homie-error").filter({ hasText: "The upload was interrupted." }).waitFor();
  await readyToSend();
  expect(await page.locator(".board-command .conversation-draft-file").textContent()).toContain("question.txt");
  await page.unroute("**/api/sessions/*/upload");
  await page.getByRole("button", { name: "Remove question.txt", exact: true }).click();
  await input.fill("Continue after the upload failure.");
  await page.getByTestId("board-homie-send").click();
  await page.getByTestId("board-homie-transcript").getByText("Continue after the upload failure.", { exact: true }).waitFor();
  await reply((await claim()).id, "The conversation continues after the upload failure.");
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.getByTestId("board-homie-message").scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      const bounds = await page.getByTestId("board-command").boundingBox();
      expect(bounds!.width).toBeLessThanOrEqual(width);
      await page.screenshot({ path: evidencePath(`commandbar-${scheme}-${width}.png`), fullPage: true });
      await page.getByTestId("board-homie-toggle").click();
      expect(await page.getByTestId("board-homie-open").isVisible()).toBe(false);
      expect(await page.getByTestId("board-homie-status").isVisible()).toBe(false);
      expect((await page.getByTestId("board-command").boundingBox())!.height).toBeLessThan(130);
      await page.screenshot({ path: evidencePath(`commandbar-collapsed-${scheme}-${width}.png`), fullPage: true });
      await page.getByLabel("Conversation actions").click();
      await page.getByTestId("board-homie-new").waitFor();
      await page.screenshot({ path: evidencePath(`commandbar-menu-${scheme}-${width}.png`) });
      await page.getByLabel("Conversation actions").press("Escape");
      expect(await page.getByTestId("board-homie-new").isVisible()).toBe(true);
      await page.getByTestId("board-homie-toggle").click();
    }
  }
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

test("a failed first session preserves the draft and starts no conversation", async () => {
  const { page } = smoke;
  const before = (await mc(["homie", "list"]) as { sessions: unknown[] }).sessions.length;
  await readyToSend();
  await page.route("**/api/sessions", async route => {
    if (route.request().method() !== "POST") { await route.continue(); return; }
    await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "Homie is unavailable." } }) });
  });
  await page.getByTestId("board-homie-new").click();
  await readyToSend();
  await page.getByTestId("board-homie-message").fill("Keep this first message.");
  await page.getByTestId("board-homie-send").click();
  await page.getByTestId("board-homie-error").filter({ hasText: "Homie is unavailable." }).waitFor();
  expect(await page.getByTestId("board-homie-message").inputValue()).toBe("Keep this first message.");
  expect(await page.getByTestId("board-homie-refresh").isVisible()).toBe(true);
  expect((await mc(["homie", "list"]) as { sessions: unknown[] }).sessions).toHaveLength(before);
  await page.unroute("**/api/sessions");
}, 30000);

async function savedSession() {
  return await smoke.page.evaluate(() => localStorage.getItem("mc.board-homie.board-alpha"));
}

test("a reload replaces an absent saved conversation and retains the replacement", async () => {
  const { page } = smoke;
  await page.evaluate(() => localStorage.setItem("mc.board-homie.board-alpha", "h-missing"));
  const before = (await mc(["homie", "list"]) as { sessions: unknown[] }).sessions.length;
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await readyToSend();
  const replacement = await savedSession();
  expect(replacement).toMatch(/^h-/);
  expect(replacement).not.toBe("h-missing");
  expect((await mc(["homie", "list"]) as { sessions: unknown[] }).sessions).toHaveLength(before + 1);
  expect(await page.getByTestId("board-homie-error").isVisible()).toBe(false);
  expect(await page.getByTestId("board-homie-new").isVisible()).toBe(true);
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await readyToSend();
  expect(await savedSession()).toBe(replacement);
  expect((await mc(["homie", "list"]) as { sessions: unknown[] }).sessions).toHaveLength(before + 1);
}, 30000);

test("a connection failure preserves the saved conversation until a successful retry", async () => {
  const { page } = smoke;
  const saved = await savedSession();
  const before = (await mc(["homie", "list"]) as { sessions: unknown[] }).sessions.length;
  await page.route(`**/api/sessions/${saved}/history`, route => route.fulfill({
    status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "The server is unavailable." } }),
  }));
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await page.getByTestId("board-homie-error").waitFor();
  expect(await savedSession()).toBe(saved);
  expect((await mc(["homie", "list"]) as { sessions: unknown[] }).sessions).toHaveLength(before);
  expect(await page.getByTestId("board-homie-new").isEnabled()).toBe(true);
  await page.unroute(`**/api/sessions/${saved}/history`);
  await page.getByTestId("board-homie-refresh").click();
  await readyToSend();
  expect(await savedSession()).toBe(saved);
  expect(await page.getByTestId("board-homie-error").isVisible()).toBe(false);
}, 30000);

test("a deleted conversation recovers and preserves its draft and attachments after a failed start", async () => {
  const { page } = smoke;
  const deleted = await savedSession();
  await page.getByTestId("board-homie-message").fill("Keep my draft and attachment.");
  await page.locator('[data-testid="board-command"] input[type="file"]').setInputFiles({
    name: "draft.txt", mimeType: "text/plain", buffer: Buffer.from("Keep this attachment."),
  });
  await page.locator(".board-command .conversation-draft-file").waitFor();
  await page.route("**/api/sessions", async route => {
    if (route.request().method() !== "POST") { await route.continue(); return; }
    await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "Homie is unavailable." } }) });
  });
  await mc(["homie", "delete", deleted!]);
  await page.getByTestId("board-homie-toggle").click();
  await page.getByTestId("board-homie-error").filter({ hasText: "A new conversation could not start." }).waitFor();
  expect(await savedSession()).toBeNull();
  await readyToSend();
  expect(await page.getByTestId("board-homie-message").inputValue()).toBe("Keep my draft and attachment.");
  expect(await page.locator(".board-command .conversation-draft-file").textContent()).toContain("draft.txt");
  await page.unroute("**/api/sessions");
  await page.getByTestId("board-homie-refresh").click();
  await readyToSend();
  expect(await savedSession()).toMatch(/^h-/);
  expect(await savedSession()).not.toBe(deleted);
  expect(await page.getByTestId("board-homie-error").isVisible()).toBe(false);
  expect(await page.getByTestId("board-homie-message").inputValue()).toBe("Keep my draft and attachment.");
  expect(await page.locator(".board-command .conversation-draft-file").textContent()).toContain("draft.txt");
  await page.getByRole("button", { name: "Remove draft.txt", exact: true }).click();
  const sent = page.waitForResponse("**/api/sessions/*/send");
  await page.getByTestId("board-homie-send").click();
  const accepted = await sent;
  expect({ status: accepted.status(), body: await accepted.json() }).toMatchObject({ status: 200 });
  await page.getByTestId("board-homie-transcript").getByText("Keep my draft and attachment.", { exact: true }).waitFor();
  const previous = await savedSession();
  expect(await page.getByTestId("board-homie-message").isDisabled()).toBe(true);
  expect(await page.getByTestId("board-homie-new").isEnabled()).toBe(true);
  await page.getByTestId("board-homie-new").click();
  await readyToSend();
  expect(await savedSession()).not.toBe(previous);
  const sessions = (await mc(["homie", "list"]) as { sessions: { id: string }[] }).sessions;
  expect(sessions.some(session => session.id === previous)).toBe(true);
  expect(await page.getByTestId("board-homie-transcript").textContent()).not.toContain("Keep my draft and attachment.");
}, 30000);

test("a late error from the previous conversation preserves the selected new conversation", async () => {
  const { page } = smoke;
  const previous = await savedSession();
  let release!: () => void;
  let requested!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  const started = new Promise<void>(resolve => { requested = resolve; });
  await page.route(`**/api/sessions/${previous}/history`, async route => {
    requested(); await held;
    await route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: { message: `unknown Homie session "${previous}"` } }) });
  });
  await page.getByTestId("board-homie-toggle").click();
  await started;
  await page.getByTestId("board-homie-message").fill("Keep this draft in the new conversation.");
  await page.getByTestId("board-homie-new").click();
  await readyToSend();
  const selected = await savedSession();
  const response = page.waitForResponse(`**/api/sessions/${previous}/history`);
  release(); await response;
  await page.unroute(`**/api/sessions/${previous}/history`);
  expect(await savedSession()).toBe(selected);
  expect(await page.getByTestId("board-homie-message").inputValue()).toBe("Keep this draft in the new conversation.");
  expect(await page.getByTestId("board-homie-error").isVisible()).toBe(false);
}, 30000);
