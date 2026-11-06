// packet-chat.smoke.test.ts — the quick chat of the Review page against a real
// mc, a scratch spine, and a real Chromium. No resident runs, so no container
// starts. The test plays the runner of each packet conversation: it claims
// the sent turn and records the reply through mc.

import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { Frame } from "playwright";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, SMOKE_BEARER, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket, type ChangeFixtureFile } from "./delivery-fixture";

let smoke: Smoke;
const task: Record<string, number> = {};
const packet: Record<string, { packetID: number; versionID: number }> = {};
const WIDE = { width: 1920, height: 1080 };

// Each paragraph has a number, so a check can name the paragraph at the top
// of the report frame.
const paragraphs = Array.from({ length: 120 }, (_, i) =>
  `<p id="p${i + 1}">Paragraph ${i + 1}. The report states one fact about the change, and the quick chat keeps this text in view.</p>`).join("");
const LONG_REPORT = `<!doctype html><html><head><meta charset="utf-8"><title>Chat report</title></head><body>
<h1>The chat report</h1>${paragraphs}</body></html>`;

const patch = (...rows: string[]) => `${rows.join("\n")}\n`;
const FILES: ChangeFixtureFile[] = [
  { status: "modified", path: "src/limit.js", old_mode: "100644", new_mode: "100644", patch: patch(
    "diff --git a/src/limit.js b/src/limit.js",
    "index 1111111..2222222 100644",
    "--- a/src/limit.js",
    "+++ b/src/limit.js",
    "@@ -1,2 +1,2 @@",
    "-export const LIMIT = 10;",
    "+export const LIMIT = 20;",
    " export const NAME = \"limit\";") },
  { status: "added", path: "docs/limit.md", new_mode: "100644", patch: patch(
    "diff --git a/docs/limit.md b/docs/limit.md",
    "new file mode 100644",
    "index 0000000..3333333",
    "--- /dev/null",
    "+++ b/docs/limit.md",
    "@@ -0,0 +1,2 @@",
    "+The limit is 20.",
    "+The name is limit.") },
];

// The Live queue order is oldest first.
const SEEDS = [
  { name: "long", hours: 9 },
  { name: "second", hours: 8 },
  { name: "third", hours: 7 },
  { name: "fourth", hours: 6 },
  { name: "decide", hours: 5 },
  { name: "model", hours: 4 },
];

function stamp(hoursAgo: number) {
  return new Date(Date.now() - hoursAgo * 3600_000).toISOString().replace("T", " ").slice(0, 19);
}

beforeAll(async () => {
  smoke = await bootSmoke();
  await smoke.page.setViewportSize(WIDE);
  await seedProject(smoke, "chat", { title: "Chat", vision: false });
  const plane = join(smoke.scratch, "reports", "chat");
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "chat"\naccess = "ro"\n`, { mode: 0o600 });
  for (const { name } of SEEDS) {
    task[name] = Number((await mc(["task", "add", `${name} task`, "--project", "chat", "--description", "The change must keep the limit."]))["task_id"]);
  }
  const db = openSpine(smoke);
  try {
    db.run("UPDATE settings SET review_wip_cap=10 WHERE id=1");
    for (const { name, hours } of SEEDS) {
      const id = task[name]!;
      const body = name === "long" ? LONG_REPORT : `<!doctype html><h1>The ${name} report</h1><p>The ${name} packet explains its change.</p>`;
      mkdirSync(join(plane, String(id), "packet"), { recursive: true });
      writeFileSync(join(plane, String(id), "packet", "report.html"), body);
      packet[name] = seedDeliveryPacket(db, {
        taskID: id, thesis: `The ${name} packet thesis`, createdAt: stamp(hours),
        renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "chat", String(id), "packet", "report.html"),
        reportSHA256: new Bun.CryptoHasher("sha256").update(body).digest("hex"),
        changes: { plane, files: FILES },
      });
    }
  } finally { db.close(); }
  await gotoReady(smoke);
  await smoke.page.getByTestId("project-chat").waitFor({ state: "attached" });
  await selectProject(smoke, "chat");
}, 90000);
afterAll(async () => { await teardown(smoke); });

const report = () => smoke.page.frameLocator('[data-testid="packet-frame"]');
const transcript = () => smoke.page.getByTestId("packet-chat-transcript");
const composer = () => smoke.page.getByTestId("packet-chat-message");

type Box = { x: number; y: number; width: number; height: number };

async function box(testid: string): Promise<Box> {
  const found = await smoke.page.getByTestId(testid).boundingBox();
  if (found === null) throw new Error(`${testid} has no box.`);
  return found;
}

async function composerBox(): Promise<Box> {
  const found = await smoke.page.getByTestId("packet-chat").locator("form").boundingBox();
  if (found === null) throw new Error("The composer has no box.");
  return found;
}

async function until(check: () => Promise<boolean>, what: string, timeout = 10000) {
  const end = Date.now() + timeout;
  while (!(await check())) {
    if (Date.now() > end) throw new Error(`Timed out: ${what}. Browser log: ${smoke.browserLog.slice(-8).join(" | ")}`);
    await Bun.sleep(100);
  }
}

/** Sets the stored open state of the quick chat. The next page load uses it. */
async function storeChat(state: "open" | "closed") {
  await smoke.page.evaluate((value) => localStorage.setItem("mc.packet-collie", value), state);
}

/** Waits until the reader shows the packet of one task and the address names it. */
async function readerShows(name: string) {
  await smoke.page.waitForFunction((id) => {
    const reader = document.querySelector('[data-testid="packet-reader"]') as HTMLElement | null;
    return reader !== null && !reader.hidden && location.hash === `#review/${id}` &&
      (document.querySelector('[data-testid="reader-title"]')?.textContent ?? "").startsWith(`Task #${id} · `);
  }, task[name], { timeout: 15000 });
}

/** Loads the Review page at the packet of one task. */
async function load(name = "long") {
  await smoke.page.goto(`${smoke.server.url}?chat=${crypto.randomUUID()}#review/${task[name]}`);
  await smoke.page.waitForSelector("body[data-ready]");
  await readerShows(name);
}

/** Waits until the quick chat shows the packet of one task. */
async function chatShows(name: string) {
  await smoke.page.waitForFunction((id) =>
    (document.querySelector('[data-testid="packet-chat-title"]')?.textContent ?? "").startsWith(`Task #${id}`),
  task[name], { timeout: 10000 });
}

async function packetConversations(): Promise<Record<string, any>[]> {
  const list = await mc(["collie", "list"]);
  return (list["sessions"] as Record<string, any>[]).filter((row) => row["packet"] != null);
}

async function currentConversation(name: string) {
  return (await packetConversations()).find((row) => row.packet.packet_id === packet[name]!.packetID && row.packet.current === true) ?? null;
}

async function untilConversation(name: string): Promise<string> {
  let id = "";
  await until(async () => { id = (await currentConversation(name))?.id ?? ""; return id !== ""; }, `the ${name} packet has a conversation`);
  return id;
}

async function historyBodies(sessionID: string): Promise<string[]> {
  const history = await mc(["collie", "history", sessionID]);
  return (history["messages"] as Record<string, any>[]).map((message) => String(message["body"] ?? ""));
}

/** Plays the runner of one conversation: claims the sent turn, shows a
 * partial reply when one is given, and records the reply. */
async function answer(sessionID: string, expected: string, reply: string, partial = "") {
  const runJSON = join(smoke.scratch, `${sessionID}.run.json`);
  const listed = (await packetConversations()).find((row) => row.id === sessionID);
  writeFileSync(runJSON, JSON.stringify({ run_id: sessionID, tier: "collie", role: "collie", verb_allowlist: listed?.verb_allowlist }));
  let claimed: Record<string, unknown> | null = null;
  for (let i = 0; i < 40 && claimed === null; i++) {
    const res = await mc(["collie", "claim", sessionID], { MC_RUN_JSON: runJSON });
    claimed = (res["message"] as Record<string, unknown> | null) ?? null;
    if (claimed === null) await Bun.sleep(250);
  }
  if (claimed === null) throw new Error("The sent turn never became claimable.");
  expect(claimed["body"]).toBe(expected);
  if (partial !== "") {
    await mc(["collie", "draft", sessionID, "--to", String(claimed["id"]), "--body", partial], { MC_RUN_JSON: runJSON });
    await transcript().getByTestId("collie-draft").filter({ hasText: partial }).waitFor({ timeout: 15000 });
  }
  await mc(["collie", "reply", sessionID, "--to", String(claimed["id"]), "--body", reply], { MC_RUN_JSON: runJSON });
  await transcript().getByTestId("collie-message").filter({ hasText: reply }).waitFor({ timeout: 15000 });
}

/** Sends one message through the composer and waits until the transcript shows it. */
async function send(text: string) {
  await composer().fill(text);
  await composer().press("Enter");
  await transcript().getByTestId("collie-message").filter({ hasText: text }).first().waitFor({ timeout: 5000 });
  expect(await composer().inputValue()).toBe("");
}

async function reportFrame(): Promise<Frame> {
  const handle = await smoke.page.getByTestId("packet-frame").elementHandle();
  const frame = await handle?.contentFrame();
  if (!frame) throw new Error("The report frame has no document.");
  return frame;
}

/** The paragraph at the top of the report frame, or 0. */
async function topParagraph(frame: Frame): Promise<number> {
  return frame.evaluate(() => {
    for (const node of document.querySelectorAll("p[id^='p']")) {
      const rect = node.getBoundingClientRect();
      if (rect.bottom > 0) return Number(node.id.slice(1));
    }
    return 0;
  });
}

/** Whether one paragraph is inside the viewport of the report frame. */
async function paragraphInView(frame: Frame, number: number): Promise<boolean> {
  return frame.evaluate((id) => {
    const rect = document.getElementById(id)!.getBoundingClientRect();
    return rect.bottom > 0 && rect.top < window.innerHeight;
  }, `p${number}`);
}

/** Each visible element of the reader stays inside the horizontal bounds of
 * the reader, and the page has no horizontal scroll. */
async function readerFits(width: number) {
  const fit = await smoke.page.evaluate(() => {
    const reader = document.querySelector('[data-testid="packet-reader"]')!;
    const bounds = reader.getBoundingClientRect();
    const outside = [...reader.querySelectorAll("*")].filter((node) => {
      if (!node.checkVisibility({ visibilityProperty: true })) return false;
      const rect = node.getBoundingClientRect();
      return rect.width > 0 && (rect.right > bounds.right + 0.5 || rect.left < bounds.left - 0.5);
    }).map((node) => node.id || (node as HTMLElement).dataset["testid"] || node.className || node.tagName);
    return { outside, scroll: document.documentElement.scrollWidth };
  });
  expect(fit.outside).toEqual([]);
  expect(fit.scroll).toBeLessThanOrEqual(width);
}

const inside = (inner: Box, outer: Box) =>
  inner.x >= outer.x - 0.5 && inner.y >= outer.y - 0.5 &&
  inner.x + inner.width <= outer.x + outer.width + 0.5 && inner.y + inner.height <= outer.y + outer.height + 0.5;

test("the Ask Collie control opens the chat column and a sent message gets a reply", async () => {
  const { page } = smoke;
  await storeChat("closed");
  await load("long");
  await report().getByText("The chat report", { exact: true }).waitFor();
  expect(await page.getByTestId("packet-chat-column").isVisible()).toBe(false);
  const control = page.getByTestId("packet-collie-open");
  expect(await control.getAttribute("aria-expanded")).toBe("false");
  expect(await control.getAttribute("aria-controls")).toBe("packet-chat-column");
  expect(inside(await box("packet-task-open"), await box("reader-bar"))).toBe(true);
  // "Ask Collie" starts the row of the decision buttons.
  expect(await control.innerText()).toBe("Ask Collie");
  const decisionBar = await box("packet-decision-bar");
  const ask = await box("packet-collie-open");
  const approve = await box("packet-approve");
  expect(inside(ask, decisionBar)).toBe(true);
  expect(ask.x + ask.width).toBeLessThan(approve.x);
  expect(ask.y).toBeCloseTo(approve.y, 0);

  await control.click();
  await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
  expect(await control.getAttribute("aria-expanded")).toBe("true");
  expect(await page.evaluate(() => localStorage.getItem("mc.packet-collie"))).toBe("open");
  // The open moves the focus to the composer, and the focus prepares the
  // conversation of the open packet.
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("packet-chat-message");
  expect(await page.getByTestId("packet-chat-title").innerText()).toBe(`Task #${task.long} · long task`);
  const sessionID = await untilConversation("long");
  await page.getByTestId("packet-chat-empty").waitFor();
  expect(await page.getByTestId("packet-chat-empty").innerText())
    .toContain("Ask Collie about this packet. Collie receives its thesis, its criteria, and its report text.");
  // The prepared conversation holds no message before the first send.
  expect((await currentConversation("long"))?.first_message ?? null).toBeNull();

  // The message shows at once, the composer clears, and the reply follows.
  await send("What does the report change?");
  await answer(sessionID, "What does the report change?", "The report raises the limit to 20.", "The report raises");
  expect(await historyBodies(sessionID)).toEqual(["What does the report change?", "The report raises the limit to 20."]);
  expect(await page.getByTestId("packet-chat-error").isVisible()).toBe(false);
  await page.screenshot({ path: evidencePath("packet-chat-1920.png"), animations: "disabled" });

  // "Open in Collie" selects the same conversation on the Collie page, with
  // the task number of its packet.
  await page.getByTestId("packet-chat-menu-button").click();
  await page.getByTestId("packet-chat-open-collie").click();
  await page.getByTestId("collie-pane").waitFor({ state: "visible" });
  const row = page.locator(`[data-testid="session-row"][data-session="${sessionID}"]`);
  await row.waitFor();
  expect(await row.getAttribute("aria-pressed")).toBe("true");
  expect(await row.getByTestId("session-task").innerText()).toBe(`Task #${task.long}`);
  await page.locator('[data-testid="collie-transcript"] [data-testid="collie-message"]').filter({ hasText: "The report raises the limit to 20." }).waitFor();
  // The model control is unavailable for a packet conversation.
  await until(async () => await page.getByTestId("collie-model").isDisabled(), "the model control is unavailable");
}, 60000);

test("each part of the reader stays visible with the chat column open at 960, 1440, and 1920 px", async () => {
  const { page } = smoke;
  await storeChat("open");
  for (const width of [960, 1440, 1920]) {
    await page.setViewportSize({ width, height: 900 });
    await load("second");
    await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
    await chatShows("second");
    await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
    // A restored open state moves no focus.
    expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).not.toBe("packet-chat-message");
    const reader = await box("packet-reader");
    const column = await box("packet-chat-column");
    expect(column.width).toBe(400);
    expect(column.x).toBeCloseTo(reader.x + reader.width + 12, 0);
    // The chat column has the full height of the Review panel.
    expect(column.y).toBeCloseTo(reader.y, 0);
    expect(column.y + column.height).toBeCloseTo(reader.y + reader.height, 0);
    // The queue column narrows, and hides only when the reader beside it
    // would be narrower than 640 px.
    expect(await page.getByTestId("review-list").isVisible()).toBe(width !== 960);
    expect(await page.getByTestId("review-pane").getAttribute("data-queue")).toBe(width === 960 ? "hidden" : "compact");
    if (width !== 960) {
      expect(reader.width).toBeGreaterThanOrEqual(640);
      expect((await box("review-list")).width).toBe(200);
    }
    // Each part of the reader stays visible inside the reader.
    for (const testid of ["reader-bar", "reader-views", "packet-frame", "packet-decision-bar", "packet-task-open",
      "packet-collie-open", "reader-previous", "reader-next", "packet-approve", "packet-revise", "packet-cancel"]) {
      expect(await page.getByTestId(testid).isVisible()).toBe(true);
      expect(inside(await box(testid), reader)).toBe(true);
    }
    expect((await box("packet-frame")).height).toBeGreaterThan(120);
    await readerFits(width);
    // The composer is level with the decision bar: with an empty draft, the
    // two have the same top edge and the same bottom edge.
    const decision = await box("packet-decision-bar");
    const form = await composerBox();
    expect(Math.abs(form.y + form.height - (decision.y + decision.height))).toBeLessThanOrEqual(1);
    expect(Math.abs(form.y - decision.y)).toBeLessThanOrEqual(1);
    expect(inside(form, column)).toBe(true);
    await page.screenshot({ path: evidencePath(`packet-chat-reader-${width}.png`), animations: "disabled" });
  }
  await page.setViewportSize(WIDE);
}, 90000);

test("the divider resizes Collie immediately and preserves the packet and the draft", async () => {
  const { page } = smoke;
  await page.setViewportSize(WIDE);
  await storeChat("open");
  await load("long");
  await chatShows("long");
  await report().getByText("The chat report", { exact: true }).waitFor();
  await composer().fill("Keep this draft after the resize.");
  const frame = await reportFrame();
  await frame.evaluate(() => { document.body.dataset.resizeCheck = "kept"; });
  const divider = page.getByRole("separator", { name: "Collie width" });
  const readerBefore = await box("packet-reader");
  const handle = await box("packet-chat-resize");
  try {
    await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
    await page.mouse.down();
    await page.mouse.move(handle.x + handle.width / 2 - 220, handle.y + handle.height / 2, { steps: 8 });
    expect((await box("packet-chat-column")).width).toBe(620);
    expect((await box("packet-reader")).width).toBeCloseTo(readerBefore.width - 220, 0);
    await page.mouse.up();
    expect(await divider.getAttribute("aria-valuenow")).toBe("620");
    expect(await page.evaluate(() => localStorage.getItem("mc.packet-collie-width"))).toBe("620");
    expect(await frame.evaluate(() => document.body.dataset.resizeCheck)).toBe("kept");
    expect(await composer().inputValue()).toBe("Keep this draft after the resize.");
    await readerFits(WIDE.width);
    await page.screenshot({ path: evidencePath("packet-chat-resize-wide.png"), animations: "disabled" });

    await page.getByTestId("reader-next").click();
    await readerShows("second");
    expect((await box("packet-chat-column")).width).toBe(620);
    await page.getByTestId("reader-previous").click();
    await readerShows("long");
    expect(await composer().inputValue()).toBe("Keep this draft after the resize.");
    await page.getByTestId("packet-chat-close").click();
    expect(await divider.isVisible()).toBe(false);
    await page.getByTestId("packet-collie-open").click();
    expect((await box("packet-chat-column")).width).toBe(620);
    await page.reload();
    await readerShows("long");
    await divider.waitFor();
    expect((await box("packet-chat-column")).width).toBe(620);
    await divider.dblclick();
    expect((await box("packet-chat-column")).width).toBe(400);
  } finally {
    await page.mouse.up();
    await page.evaluate(() => localStorage.removeItem("mc.packet-collie-width"));
  }
}, 60000);

test("the divider supports the keyboard and keeps both columns inside the window", async () => {
  const { page } = smoke;
  await storeChat("open");
  await page.setViewportSize({ width: 1440, height: 900 });
  await load("long");
  await chatShows("long");
  const divider = page.getByTestId("packet-chat-resize");
  try {
    await divider.focus();
    await page.keyboard.press("ArrowLeft");
    expect((await box("packet-chat-column")).width).toBe(424);
    await page.keyboard.press("ArrowRight");
    expect((await box("packet-chat-column")).width).toBe(400);
    await page.keyboard.press("End");
    expect((await box("packet-reader")).width).toBe(400);
    expect(await page.getByTestId("review-list").isVisible()).toBe(false);
    expect(await divider.getAttribute("aria-valuenow")).toBe(await divider.getAttribute("aria-valuemax"));
    const selectedWidth = (await box("packet-chat-column")).width;
    await readerFits(1440);
    await page.screenshot({ path: evidencePath("packet-chat-resize-maximum.png"), animations: "disabled" });

    await page.setViewportSize({ width: 960, height: 900 });
    await until(async () => (await box("packet-chat-column")).width < selectedWidth, "the chat width fits the smaller window");
    expect((await box("packet-reader")).width).toBe(400);
    await readerFits(960);
    await page.setViewportSize({ width: 390, height: 844 });
    await divider.waitFor({ state: "hidden" });
    await page.getByTestId("reader-collie-view").getByTestId("packet-chat").waitFor();
    expect(await page.getByTestId("reader-collie-view").getByTestId("packet-chat").isVisible()).toBe(true);
    await readerFits(390);
    await page.setViewportSize({ width: 1440, height: 900 });
    await divider.waitFor();
    await until(async () => (await box("packet-chat-column")).width === selectedWidth, "the selected width returns");

    await divider.press("Home");
    expect((await box("packet-chat-column")).width).toBe(320);
    expect(await page.getByTestId("review-list").isVisible()).toBe(true);
    await divider.press("ArrowRight");
    expect((await box("packet-chat-column")).width).toBe(320);
    expect(inside(await composerBox(), await box("packet-chat-column"))).toBe(true);
    await page.screenshot({ path: evidencePath("packet-chat-resize-minimum.png"), animations: "disabled" });
    expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
  } finally {
    await page.setViewportSize(WIDE);
    await divider.waitFor();
    await divider.dblclick();
    await page.evaluate(() => localStorage.removeItem("mc.packet-collie-width"));
  }
}, 60000);

test("at 1280 px the queue column hides while the chat column is open and returns after a close", async () => {
  const { page } = smoke;
  await storeChat("closed");
  await page.setViewportSize({ width: 1280, height: 900 });
  try {
    await load("second");
    expect(await page.getByTestId("review-list").isVisible()).toBe(true);
    await page.getByTestId("packet-collie-open").click();
    await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
    await page.getByTestId("review-list").waitFor({ state: "hidden" });
    expect(await page.getByTestId("review-pane").getAttribute("data-queue")).toBe("hidden");
    // The queue column stays in the document, and the reader takes its width.
    expect(await page.getByTestId("review-list").count()).toBe(1);
    expect((await box("packet-reader")).width).toBeCloseTo(796, 0);
    // The previous and next controls stay available.
    for (const testid of ["reader-previous", "reader-next"]) {
      expect(await page.getByTestId(testid).isVisible()).toBe(true);
      expect(await page.getByTestId(testid).isEnabled()).toBe(true);
    }
    await page.getByTestId("reader-next").click();
    await readerShows("third");
    await chatShows("third");
    expect(await page.getByTestId("review-list").isVisible()).toBe(false);
    await page.screenshot({ path: evidencePath("packet-chat-1280.png"), animations: "disabled" });

    await page.getByTestId("packet-chat-close").click();
    await page.getByTestId("packet-chat-column").waitFor({ state: "hidden" });
    await page.getByTestId("review-list").waitFor({ state: "visible" });
    expect(await page.getByTestId("review-pane").getAttribute("data-queue")).toBeNull();
    expect((await box("review-list")).width).toBe(264);
    // The close moves the focus to the "Ask Collie" control.
    expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("packet-collie-open");
    expect(await page.evaluate(() => localStorage.getItem("mc.packet-collie"))).toBe("closed");

    // A wider window gives the queue column space again.
    await page.getByTestId("packet-collie-open").click();
    await page.getByTestId("review-list").waitFor({ state: "hidden" });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.getByTestId("review-list").waitFor({ state: "visible" });
  } finally {
    await page.setViewportSize(WIDE);
  }
}, 60000);

test("at 1440 px the queue column narrows to 200 px with the chat column open", async () => {
  const { page } = smoke;
  await storeChat("open");
  await page.setViewportSize({ width: 1440, height: 900 });
  try {
    await load("second");
    await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
    expect(await page.getByTestId("review-list").isVisible()).toBe(true);
    expect(await page.getByTestId("review-pane").getAttribute("data-queue")).toBe("compact");
    expect((await box("review-list")).width).toBe(200);
    // The reader receives the width that the queue column gives.
    expect((await box("packet-reader")).width).toBeCloseTo(744, 0);
    expect((await box("packet-chat-column")).width).toBe(400);
    // Each packet row fits the queue column.
    const rows = await page.getByTestId("packet-row").evaluateAll(nodes => nodes.map(node => node.scrollWidth <= node.clientWidth));
    expect(rows.every(Boolean)).toBe(true);
    await page.screenshot({ path: evidencePath("packet-chat-1440.png"), animations: "disabled" });
  } finally {
    await page.setViewportSize(WIDE);
  }
}, 30000);

test("at 390 px the Collie view shows the quick chat with the reader bar and the decision bar", async () => {
  const { page } = smoke;
  const narrow = { width: 390, height: 844 };
  await storeChat("closed");
  await page.setViewportSize(narrow);
  try {
    await load("third");
    expect(await box("packet-reader")).toEqual({ x: 0, y: 0, ...narrow });
    expect(await page.getByTestId("packet-collie-open").isVisible()).toBe(false);
    await page.getByTestId("reader-view-collie").click();
    await page.getByTestId("reader-collie-view").waitFor({ state: "visible" });
    expect(await page.getByTestId("reader-view-collie").getAttribute("aria-selected")).toBe("true");
    expect(await page.getByTestId("reader-collie-view").getByTestId("packet-chat").count()).toBe(1);
    expect(await page.getByTestId("packet-chat-column").isVisible()).toBe(false);
    expect(await page.getByTestId("packet-chat-close").isVisible()).toBe(false);
    expect(await page.getByTestId("packet-render").isVisible()).toBe(false);
    // A pointer selection of the Collie view moves the focus to the composer.
    expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("packet-chat-message");
    // The reader bar, the view row, and the decision bar stay visible.
    await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
    for (const testid of ["reader-bar", "reader-views", "packet-decision-bar", "reader-close", "packet-approve"]) {
      const part = await box(testid);
      expect(await page.getByTestId(testid).isVisible()).toBe(true);
      expect(part.y).toBeGreaterThanOrEqual(0);
      expect(part.y + part.height).toBeLessThanOrEqual(narrow.height + 0.5);
    }
    const decision = await box("packet-decision-bar");
    expect(decision.y + decision.height).toBeCloseTo(narrow.height, 0);
    const form = await composerBox();
    expect(form.y + form.height).toBeLessThanOrEqual(decision.y + 0.5);
    expect(form.height).toBeGreaterThan(40);
    await readerFits(narrow.width);
    await page.screenshot({ path: evidencePath("packet-chat-390.png"), animations: "disabled" });

    // A change between the layouts keeps the quick chat and its draft text.
    await composer().fill("A draft in the narrow layout");
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
    expect(await page.getByTestId("packet-chat-column").getByTestId("packet-chat").count()).toBe(1);
    expect(await composer().inputValue()).toBe("A draft in the narrow layout");
    await page.setViewportSize(narrow);
    await page.getByTestId("reader-collie-view").waitFor({ state: "visible" });
    expect(await composer().inputValue()).toBe("A draft in the narrow layout");

    // "Packet" closes the quick chat and shows the Packet view.
    await page.getByTestId("reader-view-packet").click();
    await page.getByTestId("packet-render").waitFor({ state: "visible" });
    expect(await page.getByTestId("reader-collie-view").isVisible()).toBe(false);
    expect(await page.evaluate(() => localStorage.getItem("mc.packet-collie"))).toBe("closed");
  } finally {
    await page.setViewportSize(WIDE);
  }
}, 60000);

test("the view, the scroll position, the open panel, and the reason text survive an open and a close", async () => {
  const { page } = smoke;
  await storeChat("closed");
  await load("long");
  await report().getByText("The chat report", { exact: true }).waitFor();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-revise"]') as HTMLButtonElement).disabled);
  const frame = await reportFrame();
  await frame.evaluate(() => document.getElementById("p60")!.scrollIntoView({ block: "start" }));
  const top = await topParagraph(frame);
  expect(top).toBeGreaterThanOrEqual(59);
  await page.getByTestId("packet-revise").click();
  await page.getByTestId("packet-reason").fill("Keep this reason.");
  const title = await page.getByTestId("reader-title").innerText();

  const unchanged = async () => {
    expect(await page.getByTestId("reader-title").innerText()).toBe(title);
    expect(await page.getByTestId("reader-view-packet").getAttribute("aria-selected")).toBe("true");
    expect(await page.getByTestId("packet-reason").inputValue()).toBe("Keep this reason.");
    expect(await paragraphInView(frame, top)).toBe(true);
  };
  await page.getByTestId("packet-collie-open").click();
  await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
  await until(async () => await paragraphInView(frame, top), "the top paragraph stays in view after the open");
  await unchanged();
  await page.getByTestId("packet-chat-close").click();
  await page.getByTestId("packet-chat-column").waitFor({ state: "hidden" });
  await until(async () => await paragraphInView(frame, top), "the top paragraph stays in view after the close");
  await unchanged();
  await page.getByTestId("decide-abandon").click();

  // The Files changed view keeps its selected file.
  await page.getByTestId("reader-view-changes").click();
  await page.getByTestId("changes-path").waitFor();
  await page.getByTestId("changes-next").click();
  await page.waitForFunction(() => document.querySelector('[data-testid="changes-path"]')?.textContent === "src/limit.js");
  await page.getByTestId("packet-collie-open").click();
  await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
  expect(await page.getByTestId("reader-view-changes").getAttribute("aria-selected")).toBe("true");
  expect(await page.getByTestId("changes-path").innerText()).toBe("src/limit.js");
  await page.getByTestId("packet-chat-close").click();
  await page.getByTestId("packet-chat-column").waitFor({ state: "hidden" });
  expect(await page.getByTestId("reader-view-changes").getAttribute("aria-selected")).toBe("true");
  expect(await page.getByTestId("changes-path").innerText()).toBe("src/limit.js");
  await page.getByTestId("reader-view-packet").click();
}, 60000);

test("a move through several packets with the chat column open creates no conversation", async () => {
  const { page } = smoke;
  await storeChat("open");
  await load("second");
  await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
  const before = (await packetConversations()).map((row) => row.id).sort();
  for (const name of ["third", "fourth", "decide"]) {
    await page.getByTestId("reader-next").click();
    await readerShows(name);
    await chatShows(name);
    await page.getByTestId("packet-chat-empty").waitFor();
  }
  for (const name of ["fourth", "third", "second"]) {
    await page.getByTestId("reader-previous").click();
    await readerShows(name);
    await chatShows(name);
  }
  await Bun.sleep(500);
  expect((await packetConversations()).map((row) => row.id).sort()).toEqual(before);
}, 60000);

test("after a move to another packet no message reaches the conversation of the earlier packet", async () => {
  const { page } = smoke;
  await storeChat("open");
  await load("long");
  await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
  await chatShows("long");
  await composer().focus();
  const earlier = await untilConversation("long");
  const before = await historyBodies(earlier);
  await page.getByTestId("reader-next").click();
  await readerShows("second");
  await chatShows("second");
  await send("Which test covers the limit?");
  const later = await untilConversation("second");
  expect(later).not.toBe(earlier);
  await until(async () => (await historyBodies(later)).includes("Which test covers the limit?"), "the message reaches the second packet");
  expect(await historyBodies(earlier)).toEqual(before);
  await answer(later, "Which test covers the limit?", "The limit test covers it.");
}, 60000);

test("draft text stays with its packet after a move and a return", async () => {
  const { page } = smoke;
  await storeChat("open");
  await load("third");
  await chatShows("third");
  await composer().fill("A draft for the third packet");
  await page.getByTestId("reader-next").click();
  await readerShows("fourth");
  await chatShows("fourth");
  expect(await composer().inputValue()).toBe("");
  await composer().fill("A draft for the fourth packet");
  await page.getByTestId("reader-previous").click();
  await readerShows("third");
  await chatShows("third");
  expect(await composer().inputValue()).toBe("A draft for the third packet");
  await page.getByTestId("reader-next").click();
  await readerShows("fourth");
  expect(await composer().inputValue()).toBe("A draft for the fourth packet");
}, 30000);

test("a return to a packet and a reload show the same conversation with its history", async () => {
  const { page } = smoke;
  await storeChat("open");
  await load("long");
  const sessionID = (await currentConversation("long"))?.id;
  expect(sessionID).toBeTruthy();
  const shows = async () => {
    await chatShows("long");
    await transcript().getByTestId("collie-message").filter({ hasText: "The report raises the limit to 20." }).waitFor();
    const keys = await transcript().getByTestId("collie-message").evaluateAll((nodes) => nodes.map((node) => (node as HTMLElement).dataset["key"] ?? ""));
    expect(keys.length).toBe(2);
    for (const key of keys) expect(key.startsWith(`${sessionID}:`)).toBe(true);
  };
  await shows();
  await page.getByTestId("reader-next").click();
  await readerShows("second");
  await transcript().getByTestId("collie-message").filter({ hasText: "The limit test covers it." }).waitFor();
  await page.getByTestId("reader-previous").click();
  await readerShows("long");
  await shows();
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await readerShows("long");
  await shows();
  expect((await currentConversation("long"))?.id).toBe(sessionID);
}, 60000);

test("a reply retry in the quick chat keeps the Review page", async () => {
  const { page } = smoke;
  await storeChat("open");
  const sessionID = (await currentConversation("long"))?.id;
  expect(sessionID).toBeTruthy();
  let replyState = "failed", retries = 0;
  const historyPath = `**/api/sessions/${sessionID}/history`;
  await page.route(historyPath, (route) => route.fulfill({ json: {
    session_id: sessionID, status: "active",
    messages: [{ id: 1, direction: "inbound", body: "Why did the reply stop?", created_at: "2020-01-01T00:00:00Z", reply_state: replyState, attempt_id: `${replyState}-attempt` }],
  } }));
  await page.route("**/api/collie/reply-retry", async (route) => {
    retries++;
    replyState = "authorized";
    await route.fulfill({ json: { session_id: sessionID, attempt_id: "authorized-attempt" } });
  });
  try {
    await load("long");
    await chatShows("long");
    // The quick chat then shows the retried reply without a "Retry reply" control.
    await transcript().getByRole("button", { name: "Retry reply", exact: true }).click();
    await until(async () => await transcript().getByText("Why did the reply stop?", { exact: true }).isVisible() &&
      (await transcript().getByRole("button", { name: /^Retry/ }).count()) === 0, "the quick chat shows the retried reply", 3000);
    expect(retries).toBe(1);
    expect(await page.evaluate(() => location.hash)).toBe(`#review/${task.long}`);
    expect(await page.getByTestId("packet-reader").isVisible()).toBe(true);
    expect(await page.getByTestId("packet-chat-column").isVisible()).toBe(true);
  } finally {
    await page.unroute(historyPath);
    await page.unroute("**/api/collie/reply-retry");
  }
}, 30000);

test("the decision controls work while the quick chat is open", async () => {
  const { page } = smoke;
  await storeChat("open");
  await load("decide");
  await chatShows("decide");
  await composer().fill("A draft beside the decision");
  await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-revise"]') as HTMLButtonElement).disabled);
  await page.getByTestId("packet-revise").click();
  await page.getByTestId("packet-reason").fill("Use a smaller limit.");
  await page.getByTestId("packet-decide-submit").click();
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="packet-decision"]')?.textContent ?? "").startsWith("Returned to the pipeline."), undefined, { timeout: 15000 });
  const list = await mc(["packet", "list"]);
  const decided = (list["packets"] as Record<string, any>[]).find((row) => row["task_id"] === task.decide);
  expect(decided?.["review_state"]).toBe("returned");
  // The quick chat stays open with its draft.
  expect(await page.getByTestId("packet-chat-column").isVisible()).toBe(true);
  expect(await composer().inputValue()).toBe("A draft beside the decision");
  await page.screenshot({ path: evidencePath("packet-chat-decision.png"), animations: "disabled" });
}, 30000);

test("a send while a newer packet replaces the open packet keeps the text and states the cause", async () => {
  const { page } = smoke;
  await storeChat("open");
  await load("decide");
  await chatShows("decide");
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="packet-decision"]')?.textContent ?? "").startsWith("Returned to the pipeline."), undefined, { timeout: 15000 });
  // The test holds the preparation until a newer packet of the task exists.
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  let prepared = false;
  await page.route("**/api/sessions", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    prepared = true;
    await held;
    await route.continue();
  });
  try {
    await composer().fill("Does the smaller limit pass?");
    await composer().press("Enter");
    await until(async () => prepared, "the quick chat prepares the conversation");
    const plane = join(smoke.scratch, "reports", "chat");
    const body = "<!doctype html><h1>The newer decide report</h1><p>The newer packet uses a smaller limit.</p>";
    writeFileSync(join(plane, String(task.decide), "packet", "report-2.html"), body);
    const db = openSpine(smoke);
    let newer = 0;
    try {
      newer = seedDeliveryPacket(db, {
        taskID: task.decide!, thesis: "The newer decide packet thesis",
        renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "chat", String(task.decide), "packet", "report-2.html"),
        reportSHA256: new Bun.CryptoHasher("sha256").update(body).digest("hex"),
        changes: { plane, files: FILES },
      }).packetID;
    } finally { db.close(); }
    release();
    // The refusal of the replaced packet refreshes the page, and the reader
    // shows the newer packet of the task.
    await report().getByText("The newer decide report", { exact: true }).waitFor({ timeout: 12000 });
    await chatShows("decide");
    await page.getByTestId("packet-chat-error").waitFor({ state: "visible", timeout: 5000 });
    expect(await page.getByTestId("packet-chat-error").innerText())
      .toBe("The review packet changed. Open the current packet. Your message remains available. Retry Send.");
    expect(await composer().inputValue()).toBe("Does the smaller limit pass?");
    // mc refused the send, so no conversation of the task holds the message.
    const conversations = (await packetConversations()).filter((row) => row.packet.task_id === task.decide);
    expect(conversations.some((row) => row.packet.packet_id === newer)).toBe(false);
    expect(conversations.map((row) => row.first_message ?? null).filter((first) => first !== null)).toEqual([]);
  } finally {
    release();
    await page.unroute("**/api/sessions");
  }
}, 30000);

test("after a return the empty state of the newer packet links to the conversation of the earlier packet", async () => {
  const { page } = smoke;
  await storeChat("open");
  await load("fourth");
  await chatShows("fourth");
  // The first packet of a task has no earlier packet.
  await page.getByTestId("packet-chat-empty").waitFor();
  expect(await page.getByTestId("packet-chat-earlier").count()).toBe(0);
  await send("Is the limit documented?");
  const earlier = await untilConversation("fourth");
  await until(async () => (await historyBodies(earlier)).includes("Is the limit documented?"), "the message reaches the fourth packet");
  await mc(["packet", "decide", String(task.fourth), "--revise", "--reason", "Document the limit.",
    "--packet", String(packet.fourth!.packetID), "--version", String(packet.fourth!.versionID)]);
  const plane = join(smoke.scratch, "reports", "chat");
  const body = "<!doctype html><h1>The newer fourth report</h1><p>The newer packet documents the limit.</p>";
  writeFileSync(join(plane, String(task.fourth), "packet", "report-2.html"), body);
  const db = openSpine(smoke);
  try {
    seedDeliveryPacket(db, {
      taskID: task.fourth!, thesis: "The newer fourth packet thesis",
      renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "chat", String(task.fourth), "packet", "report-2.html"),
      reportSHA256: new Bun.CryptoHasher("sha256").update(body).digest("hex"),
      changes: { plane, files: FILES },
    });
  } finally { db.close(); }
  await load("fourth");
  await report().getByText("The newer fourth report", { exact: true }).waitFor({ timeout: 12000 });
  await chatShows("fourth");
  const link = page.getByTestId("packet-chat-earlier");
  await link.waitFor({ timeout: 10000 });
  expect(await link.innerText()).toBe("Continue the conversation about the earlier packet");
  await link.click();
  await page.getByTestId("collie-pane").waitFor({ state: "visible" });
  const row = page.locator(`[data-testid="session-row"][data-session="${earlier}"]`);
  await row.waitFor();
  expect(await row.getAttribute("aria-pressed")).toBe("true");
}, 60000);

// This test archives the Project, so it runs last.
test("the model control of the quick chat changes the Collie route and keeps the earlier conversation", async () => {
  const { page } = smoke;
  const routing = await mc(["routing", "show"]) as { routes: { role: string; binding: string }[]; bindings: { id: string; model: string; name: string }[] };
  const collieRoute = async () => ((await mc(["routing", "show"])) as typeof routing).routes.find((r) => r.role === "collie")?.binding;
  const label = (id: string) => { const b = routing.bindings.find((row) => row.id === id)!; return b.model || b.name; };
  const first = (await collieRoute())!;
  const other = routing.bindings.map((b) => b.id).find((id) => id !== first)!;
  const enabled = () => page.waitForFunction(() => !(document.querySelector('[data-testid="packet-chat-model"]') as HTMLButtonElement | null)?.disabled);
  await storeChat("open");
  await load("model");
  await chatShows("model");
  const control = page.getByTestId("packet-chat-model");
  await enabled();
  expect(await control.innerText()).toContain(label(first));
  expect(inside(await box("packet-chat-model"), await box("packet-chat-column"))).toBe(true);

  // A packet without a conversation takes the selection at once.
  await control.click();
  const menu = page.getByTestId("packet-chat-model-menu");
  await menu.waitFor({ state: "visible" });
  expect(await menu.getByTestId("packet-chat-model-option").count()).toBe(routing.bindings.length);
  await page.screenshot({ path: evidencePath("packet-chat-model-menu.png"), animations: "disabled" });
  await menu.locator(`[data-binding="${other}"]`).click();
  await menu.waitFor({ state: "hidden" });
  expect(await collieRoute()).toBe(other);
  await page.waitForFunction((text) => document.querySelector('[data-testid="packet-chat-model"]')?.textContent?.includes(text), label(other));
  expect(await currentConversation("model")).toBeNull();

  // The first message starts a conversation with the selected model.
  await send("Which model answers?");
  const earlier = await untilConversation("model");
  expect(String((await currentConversation("model"))!.binding).split("/").at(-1)).toBe(other);
  await answer(earlier, "Which model answers?", "The selected model answers.");

  // A conversation with messages asks first, then gives its place to a new
  // conversation. The earlier conversation stays in the Collie list.
  await enabled();
  await control.click();
  await menu.locator(`[data-binding="${first}"]`).click();
  const confirmation = page.getByTestId("packet-chat-model-confirm");
  await confirmation.waitFor();
  expect(await confirmation.innerText()).toContain("Your current conversation stays in the session list.");
  await confirmation.getByRole("button", { name: "Start new conversation", exact: true }).click();
  await until(async () => (await collieRoute()) === first, "the Collie route changes");
  let later = "";
  await until(async () => { later = (await currentConversation("model"))?.id ?? ""; return later !== "" && later !== earlier; }, "a new conversation of the packet");
  expect(String((await currentConversation("model"))!.binding).split("/").at(-1)).toBe(first);
  const kept = (await packetConversations()).find((row) => row.id === earlier);
  expect(kept?.packet.current).toBe(false);
  expect(kept?.first_message).not.toBeNull();
  await page.getByTestId("packet-chat-empty").waitFor();
  await page.waitForFunction((text) => document.querySelector('[data-testid="packet-chat-model"]')?.textContent?.includes(text), label(first));
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

test("the quick chat of a packet of an archived Project accepts no message and states the cause", async () => {
  const { page } = smoke;
  const ARCHIVED = "The Project of this packet is archived. Conversations about its packets are unavailable.";
  await storeChat("open");
  await mc(["project", "archive", "chat"]);
  // mc refuses a conversation about a packet of the archived Project.
  const refused = await fetch(`${smoke.server.url}api/sessions`, {
    method: "POST", headers: { ...SMOKE_BEARER, "content-type": "application/json" },
    body: JSON.stringify({ packet_id: packet.third!.packetID, version_id: packet.third!.versionID }),
  });
  expect(refused.status).not.toBe(200);
  expect(((await refused.json()) as { error?: { message?: string } }).error?.message).toBe(ARCHIVED);

  // A packet without messages states the cause in place of the invitation.
  await load("third");
  await chatShows("third");
  await until(async () => (await page.getByTestId("packet-chat-empty").innerText().catch(() => "")) === ARCHIVED,
    "the empty state states the archived Project");
  expect(await page.getByTestId("packet-chat-earlier").count()).toBe(0);
  expect(await composer().isDisabled()).toBe(true);
  expect(await page.getByTestId("packet-chat-send").isDisabled()).toBe(true);
  expect(await page.getByTestId("packet-chat-model").isDisabled()).toBe(true);
  await page.getByTestId("packet-chat-menu-button").click();
  expect(await page.getByTestId("packet-chat-new").isDisabled()).toBe(true);

  // A packet with a conversation keeps its history and states the cause.
  await load("long");
  await chatShows("long");
  await transcript().getByTestId("collie-message").filter({ hasText: "The report raises the limit to 20." }).waitFor();
  await until(async () => (await page.getByTestId("packet-chat-status").innerText().catch(() => "")) === ARCHIVED,
    "the status line states the archived Project");
  expect(await composer().isDisabled()).toBe(true);
  await page.screenshot({ path: evidencePath("packet-chat-archived.png"), animations: "disabled" });
}, 60000);
