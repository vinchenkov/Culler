import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { Frame } from "playwright";
import { bootSmoke, completePageRead, evidencePath, gotoReady, mc, seedProject, selectProject, systemNoticeShown, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket, type ChangeFixtureFile } from "./delivery-fixture";

let smoke: Smoke;
const task: Record<string, number> = {};
const packet: Record<string, { packetID: number; versionID: number }> = {};
const WIDE = { width: 1920, height: 1080 };

const CRITERIA = "The reader must meet these criteria:\n\n- The report fills the window.\n- The view row stays visible.\n- The Files changed view shows each changed file.";

const filler = (topic: string, count: number) => Array.from({ length: count }, (_, i) =>
  `<p>${topic} paragraph ${i + 1}. The reader keeps this text inside the report frame, and the frame scrolls.</p>`).join("");

// The report has a table of contents, a glossary link, a link to another
// address, and a script link. It is longer than the window.
const HTML_REPORT = `<!doctype html><html><head><meta charset="utf-8"><title>Reader report</title></head><body>
<h1>The reader report</h1>
<nav aria-label="Contents"><ol><li><a href="#summary">Summary</a></li><li><a href="#details">Details</a></li><li><a href="#glossary">Glossary</a></li></ol></nav>
<h2 id="summary">Summary</h2>
<p>The change adds the <a href="#term-candidate">merge candidate</a> check.
Read the <a href="https://example.com/culler-guide">external guide</a>.
<a href="javascript:alert(1)">A script link</a>.</p>
<h2 id="details">Details</h2>
${filler("Detail", 50)}
<h2 id="glossary">Glossary</h2>
<dl><dt id="term-candidate">Merge candidate</dt><dd>The Git merge of the submitted commit with the destination commit.</dd></dl>
${filler("Appendix", 40)}
</body></html>`;

const addedLines = Array.from({ length: 80 }, (_, i) => `+The reader guide line ${i + 1}.`);
const patch = (...rows: string[]) => `${rows.join("\n")}\n`;
const FILES: ChangeFixtureFile[] = [
  { status: "modified", path: "dashboard/src/ui/review.js", old_mode: "100644", new_mode: "100644", patch: patch(
    "diff --git a/dashboard/src/ui/review.js b/dashboard/src/ui/review.js",
    "index 1111111..2222222 100644",
    "--- a/dashboard/src/ui/review.js",
    "+++ b/dashboard/src/ui/review.js",
    "@@ -1,4 +1,5 @@",
    " import { relativeAge } from \"./format.js\";",
    "-const LIMIT = 10;",
    "+const LIMIT = 20;",
    "+const LABEL = \"new\";",
    " export function view() {",
    "   return LIMIT;") },
  { status: "added", path: "docs/reader.md", new_mode: "100644", patch: patch(
    "diff --git a/docs/reader.md b/docs/reader.md",
    "new file mode 100644",
    "index 0000000..3333333",
    "--- /dev/null",
    "+++ b/docs/reader.md",
    `@@ -0,0 +1,${addedLines.length} @@`,
    ...addedLines) },
  { status: "deleted", path: "old/notes.txt", old_mode: "100644", patch: patch(
    "diff --git a/old/notes.txt b/old/notes.txt",
    "deleted file mode 100644",
    "index 4444444..0000000",
    "--- a/old/notes.txt",
    "+++ /dev/null",
    "@@ -1,2 +0,0 @@",
    "-The first note.",
    "-The second note.") },
];

function stamp(hoursAgo: number) {
  return new Date(Date.now() - hoursAgo * 3600_000).toISOString().replace("T", " ").slice(0, 19);
}

// The Live queue order: the awaiting packets oldest first, then the approved
// packet. The ledger holds the merged packets.
const SEEDS: { name: string; hours: number; report: "html" | "short" | "none" | "changed" }[] = [
  { name: "html", hours: 9, report: "html" },
  { name: "second", hours: 8, report: "short" },
  { name: "blocked", hours: 7, report: "short" },
  { name: "missing", hours: 6, report: "none" },
  { name: "broken", hours: 5, report: "changed" },
  { name: "decide", hours: 4, report: "short" },
  { name: "approved", hours: 3, report: "short" },
  { name: "merged", hours: 80, report: "short" },
  { name: "expired", hours: 90, report: "short" },
];
const LIVE = ["html", "second", "blocked", "missing", "broken", "decide", "approved"];

beforeAll(async () => {
  smoke = await bootSmoke();
  await smoke.page.setViewportSize(WIDE);
  await seedProject(smoke, "reader", { title: "Reader", vision: false });
  const plane = join(smoke.scratch, "reports", "reader");
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "reader"\naccess = "ro"\n`, { mode: 0o600 });
  for (const { name } of SEEDS) {
    task[name] = Number((await mc(["task", "add", `${name} task`, "--project", "reader", "--description", CRITERIA]))["task_id"]);
  }
  const db = openSpine(smoke);
  try {
    db.run("UPDATE settings SET review_wip_cap=10 WHERE id=1");
    for (const { name, hours, report } of SEEDS) {
      const id = task[name]!;
      const body = report === "html" ? HTML_REPORT : `<!doctype html><h1>The ${name} report</h1><p>The ${name} packet explains its change.</p>`;
      const renderPath = report === "none" ? "" : join(process.env["MC_ARTIFACTS_ROOT"]!, "reader", String(id), "packet", "report.html");
      if (report !== "none") {
        mkdirSync(join(plane, String(id), "packet"), { recursive: true });
        // The changed report differs from the digest that the spine records.
        writeFileSync(join(plane, String(id), "packet", "report.html"), report === "changed" ? `${body}<p>A later edit.</p>` : body);
      }
      packet[name] = seedDeliveryPacket(db, {
        taskID: id, thesis: `The ${name} packet thesis`, renderPath, createdAt: stamp(hours),
        reportSHA256: new Bun.CryptoHasher("sha256").update(body).digest("hex"),
        changes: { plane, files: FILES },
      });
    }
  } finally { db.close(); }
  const decide = (name: string) => mc(["packet", "decide", String(task[name]), "--approve",
    "--packet", String(packet[name]!.packetID), "--version", String(packet[name]!.versionID)]);
  await mc(["task", "block", String(task.blocked), "--reason", "The operator must confirm the license."]);
  for (const name of ["approved", "merged", "expired"]) await decide(name);
  // The merge application archives an approved task. The expired report
  // keeps only its record in the spine.
  const merges = openSpine(smoke);
  try {
    merges.run("UPDATE tasks SET decided_at=?,archived=1 WHERE id=?", [stamp(70), task.merged!]);
    merges.run("UPDATE tasks SET decided_at=?,archived=1 WHERE id=?", [stamp(85), task.expired!]);
    const renderPath = join(process.env["MC_ARTIFACTS_ROOT"]!, "reader", String(task.expired), "packet", "report.html");
    merges.run("INSERT INTO artifact_retention(task_id,expires_at,expired_at) VALUES (?,?,?)", [task.expired!, "2026-09-20 09:14:00", "2026-09-20 09:14:00"]);
    merges.run("INSERT INTO task_artifacts(task_id,path,expired_at) VALUES (?,?,?)", [task.expired!, renderPath, "2026-09-20 09:14:00"]);
  } finally { merges.close(); }
  await gotoReady(smoke);
  await smoke.page.getByTestId("project-reader").waitFor({ state: "attached" });
  await selectProject(smoke, "reader");
}, 90000);
afterAll(async () => { await teardown(smoke); });

const row = (name: string) => smoke.page.locator(`[data-testid="packet-row"][data-task="${task[name]}"]`);
const report = () => smoke.page.frameLocator('[data-testid="packet-frame"]');

async function box(testid: string) {
  const found = await smoke.page.getByTestId(testid).boundingBox();
  if (found === null) throw new Error(`${testid} has no box.`);
  return found;
}

async function until(check: () => Promise<boolean>, what: string, timeout = 10000) {
  const end = Date.now() + timeout;
  while (!(await check())) {
    if (Date.now() > end) throw new Error(`Timed out: ${what}`);
    await Bun.sleep(50);
  }
}

/** Loads the Review page from its address. The page opens the first packet of
 * the Live queue beside the queue column. */
async function fresh(first = "html", text = "The reader report") {
  await smoke.page.goto(`${smoke.server.url}?review=${crypto.randomUUID()}#review`);
  await smoke.page.waitForSelector("body[data-ready]");
  await readerShows(first);
  if (text !== "") await report().getByText(text, { exact: true }).first().waitFor();
}

/** Waits until the reader shows the packet of one task and the address names it. */
async function readerShows(name: string) {
  await smoke.page.waitForFunction((id) => {
    const reader = document.querySelector('[data-testid="packet-reader"]') as HTMLElement | null;
    return reader !== null && !reader.hidden && location.hash === `#review/${id}` &&
      (document.querySelector('[data-testid="reader-title"]')?.textContent ?? "").startsWith(`Task #${id} · `);
  }, task[name], { timeout: 15000 });
}

/** Waits until the Review page shows the list entry: no open packet. */
async function listShows() {
  await smoke.page.waitForFunction(() =>
    (document.querySelector('[data-testid="packet-reader"]') as HTMLElement | null)?.hidden === true &&
    location.hash === "#review" &&
    document.querySelector('[data-testid="packets"]')?.checkVisibility() === true, undefined, { timeout: 15000 });
}

async function open(name: string, text = `The ${name} report`) {
  await row(name).click();
  await readerShows(name);
  if (text !== "") await report().getByText(text, { exact: true }).first().waitFor();
}

async function reportFrame(): Promise<Frame> {
  const handle = await smoke.page.getByTestId("packet-frame").elementHandle();
  const frame = await handle?.contentFrame();
  if (!frame) throw new Error("The report frame has no document.");
  return frame;
}

async function noErrorNotice() {
  const { page } = smoke;
  expect(await page.getByTestId("reader-error").isVisible()).toBe(false);
  expect(await page.getByTestId("error").isVisible()).toBe(false);
}

const inside = (inner: { x: number; y: number; width: number; height: number }, outer: { x: number; y: number; width: number; height: number }) =>
  inner.x >= outer.x - 0.5 && inner.y >= outer.y - 0.5 &&
  inner.x + inner.width <= outer.x + outer.width + 0.5 && inner.y + inner.height <= outer.y + outer.height + 0.5;

test("the Review page opens the first packet beside the queue column", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  const entries = await page.evaluate(() => history.length);
  await page.getByTestId("tab-review").click();
  await readerShows("html");
  await report().getByText("The reader report", { exact: true }).first().waitFor();
  // The first packet adds one history entry over the list entry.
  expect(await page.evaluate(() => history.length)).toBe(entries + 1);
  const order = await page.getByTestId("packet-row").evaluateAll((nodes) => nodes.map((node) => Number((node as HTMLElement).dataset["task"])));
  expect(order).toEqual(LIVE.map((name) => task[name]!));
  // The card of the open packet shows the current state.
  const current = await page.getByTestId("packet-row").evaluateAll((nodes) => nodes
    .filter((node) => node.getAttribute("aria-current") === "true").map((node) => Number((node as HTMLElement).dataset["task"])));
  expect(current).toEqual([task.html!]);
  // Each card shows its task title under the task number, and no thesis.
  const cards = await page.getByTestId("packet-row").evaluateAll((nodes) => nodes.map((node) => {
    const meta = node.querySelector(".metaline")!.getBoundingClientRect();
    const title = node.querySelector('[data-testid="packet-title"]');
    return {
      title: title?.textContent ?? "",
      below: title !== null && meta.bottom <= title.getBoundingClientRect().top,
      thesis: (node as HTMLElement).innerText.includes("packet thesis"),
    };
  }));
  expect(cards).toEqual(LIVE.map((name) => ({ title: `${name} task`, below: true, thesis: false })));

  // The page column is 1920 px wide with 24 px side space. The header, the
  // tabs, and the notice area stay in view and stay available.
  const header = await page.locator("header.topbar").boundingBox();
  expect(header!.x).toBe(24);
  expect(header!.width).toBe(WIDE.width - 48);
  for (const testid of ["tab-board", "system-notices", "packets"]) {
    expect(await page.getByTestId(testid).evaluate((node) => node.closest("[inert]") === null)).toBe(true);
  }
  expect(await page.getByTestId("tab-board").isVisible()).toBe(true);
  // The first packet keeps the focus on the tab.
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("tab-review");

  // The queue column is 264 px wide. The reader fills the remaining width to
  // the window height, and the page itself does not scroll.
  const pane = await box("review-pane");
  const column = await box("review-list");
  const reader = await box("packet-reader");
  expect(column.width).toBe(264);
  expect(column.x).toBe(pane.x + 12);
  expect(reader.x).toBe(column.x + column.width + 12);
  expect(reader.x + reader.width).toBe(pane.x + pane.width - 12);
  expect(reader.width).toBeGreaterThan(1550);
  expect(reader.y + reader.height).toBe(pane.y + pane.height - 12);
  expect(pane.y + pane.height).toBe(WIDE.height - 16);
  expect(await page.evaluate(() => document.scrollingElement!.scrollHeight)).toBe(WIDE.height);
  expect(await page.getByTestId("reader-empty").isVisible()).toBe(false);

  // The four parts sit from top to bottom and fill the reader.
  const bar = await box("reader-bar");
  const views = await box("reader-views");
  const frame = await box("packet-frame");
  const decision = await box("packet-decision-bar");
  expect(bar.y).toBe(reader.y);
  expect(views.y).toBeCloseTo(bar.y + bar.height, 0);
  expect(frame.y).toBeCloseTo(views.y + views.height, 0);
  expect(frame.width).toBe(reader.width);
  expect(decision.y).toBeCloseTo(frame.y + frame.height, 0);
  expect(decision.y + decision.height).toBeCloseTo(reader.y + reader.height, 0);
  // The reader bar is one row. The Task button sits in it.
  expect(bar.height).toBeLessThan(56);
  for (const testid of ["packet-task-open", "reader-previous", "reader-next"]) {
    expect(inside(await box(testid), bar)).toBe(true);
  }
  expect(await page.getByTestId("reader-close").isVisible()).toBe(false);
  // The view row is one row. It holds the two views and no Git records.
  expect(views.height).toBeLessThan(48);
  expect(await page.getByTestId("reader-views").getByRole("tab").allInnerTexts()).toEqual(["Packet", "Files changed · 3"]);
  expect(await page.getByTestId("reader-view-packet").getAttribute("aria-selected")).toBe("true");
  expect(await page.getByTestId("reader-views").locator(".fact").count()).toBe(0);
  // The reader bar names the position and the task. A packet that awaits a
  // decision has no tags; the queue row keeps the age.
  expect(await page.getByTestId("reader-position").innerText()).toBe("1 of 7");
  expect(await page.getByTestId("reader-title").innerText()).toBe(`Task #${task.html} · html task`);
  expect(await page.getByTestId("reader-tags").innerText()).toBe("");
  expect(await row("html").getByTestId("packet-age").innerText()).toContain("in queue");
  await page.getByTestId("packet-approve").waitFor({ state: "visible" });
  await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
  await page.screenshot({ path: evidencePath("reader-1920.png") });
}, 30000);

test("the report frame and the packet list are the only scroll areas of the page", async () => {
  const { page } = smoke;
  await readerShows("html");
  const frame = await reportFrame();
  const layout = await page.evaluate(() => {
    const reader = document.querySelector('[data-testid="packet-reader"]')!;
    const scrollers = [...reader.querySelectorAll("*")].filter((node) => {
      const style = getComputedStyle(node);
      return node.checkVisibility({ visibilityProperty: true }) && /(auto|scroll)/.test(style.overflowY + style.overflowX) &&
        (node.scrollHeight > node.clientHeight + 1 || node.scrollWidth > node.clientWidth + 1);
    }).map((node) => node.id || node.className);
    const list = document.querySelector('[data-testid="packets"]')!;
    return { scrollers, list: getComputedStyle(list).overflowY, page: document.scrollingElement!.scrollHeight - window.innerHeight };
  });
  expect(layout.scrollers).toEqual([]);
  expect(layout.list).toBe("auto");
  expect(layout.page).toBe(0);
  const inner = await frame.evaluate(() => ({ height: document.documentElement.scrollHeight, view: window.innerHeight }));
  expect(inner.height).toBeGreaterThan(inner.view * 2);

  // A wheel over the report scrolls the report. A wheel over the reader bar
  // scrolls nothing.
  const area = await box("packet-frame");
  await page.mouse.move(area.x + area.width / 2, area.y + area.height / 2);
  await page.mouse.wheel(0, 900);
  await until(async () => (await frame.evaluate(() => window.scrollY)) > 0, "the report scrolls");
  const bar = await box("reader-bar");
  await page.mouse.move(bar.x + bar.width / 2, bar.y + bar.height / 2);
  await page.mouse.wheel(0, 900);
  await Bun.sleep(200);
  expect(await page.evaluate(() => [window.scrollY, document.scrollingElement!.scrollTop])).toEqual([0, 0]);
  await frame.evaluate(() => window.scrollTo(0, 0));
}, 30000);

test("an in-page link moves the frame to its target and keeps the report", async () => {
  const { page } = smoke;
  await readerShows("html");
  const frame = await reportFrame();
  const inView = (id: string) => frame.evaluate((id) => {
    const top = document.getElementById(id)!.getBoundingClientRect().top;
    return top >= 0 && top < window.innerHeight && window.scrollY > 0;
  }, id);

  await report().getByRole("link", { name: "merge candidate", exact: true }).click();
  await until(async () => frame.url() === "about:srcdoc#term-candidate", "the glossary link moves the frame");
  await until(() => inView("term-candidate"), "the glossary term is in view");

  await frame.evaluate(() => window.scrollTo(0, 0));
  await report().getByRole("navigation", { name: "Contents" }).getByRole("link", { name: "Glossary" }).click();
  await until(async () => frame.url() === "about:srcdoc#glossary", "the contents link moves the frame");
  await until(() => inView("glossary"), "the glossary heading is in view");
  // The frame keeps the report: the page did not load inside it.
  expect(await report().getByRole("heading", { name: "The reader report" }).count()).toBe(1);
  expect(await report().locator("[data-testid]").count()).toBe(0);
  expect(await page.getByTestId("packet-frame").count()).toBe(1);
  await readerShows("html");

  // A link to another address opens a new tab. A script link has no address
  // and shows as plain text.
  const external = report().getByRole("link", { name: "external guide" });
  expect(await external.getAttribute("target")).toBe("_blank");
  expect(await external.getAttribute("rel")).toBe("noopener noreferrer");
  const script = await report().getByText("A script link").evaluate((node) => ({
    href: node.hasAttribute("href"),
    plain: getComputedStyle(node).color === getComputedStyle(node.parentElement!).color,
  }));
  expect(script).toEqual({ href: false, plain: true });
  expect(await report().getByRole("link", { name: "Details" }).getAttribute("target")).toBe("_self");

  // The next control removes the frame steps, so one Browser Back shows the list entry.
  await page.getByTestId("reader-next").click();
  await readerShows("second");
  await report().getByText("The second report", { exact: true }).waitFor();
  await page.goBack();
  await listShows();
  expect(await page.getByTestId("reader-empty").innerText()).toBe("Select a packet to read its report.");
  expect(await page.getByTestId("reader-empty").isVisible()).toBe(true);

  // A click on the card of another packet removes the frame steps too.
  await open("html", "The reader report");
  await report().getByRole("navigation", { name: "Contents" }).getByRole("link", { name: "Details" }).click();
  const moved = await reportFrame();
  await until(async () => moved.url() === "about:srcdoc#details", "the contents link moves the frame");
  await open("second");
  await page.goBack();
  await listShows();
  // The focus stays on the card that the operator clicked.
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-task"))).toBe(String(task.second));
}, 30000);

test("the previous control, the next control, Browser Back, and a reload show the correct packet", async () => {
  const { page, server } = smoke;
  await fresh();
  const entries = await page.evaluate(() => history.length);
  expect(await page.getByTestId("reader-previous").isDisabled()).toBe(true);

  await page.getByTestId("reader-next").click();
  await readerShows("second");
  await report().getByText("The second report", { exact: true }).waitFor();
  expect(await page.getByTestId("reader-position").innerText()).toBe("2 of 7");
  expect(await page.getByTestId("reader-previous").isEnabled()).toBe(true);
  // The focus stays on the next control.
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("reader-next");

  await page.getByTestId("reader-next").click();
  await readerShows("blocked");
  await report().getByText("The blocked report", { exact: true }).waitFor();
  expect(await page.getByTestId("reader-position").innerText()).toBe("3 of 7");

  await page.getByTestId("reader-previous").click();
  await readerShows("second");
  await report().getByText("The second report", { exact: true }).waitFor();
  // A move to another packet replaces the reader entry.
  expect(await page.evaluate(() => history.length)).toBe(entries);

  await page.goBack();
  await listShows();
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-task"))).toBe(String(task.second));

  // The next control is unavailable at the last packet. The new reader entry
  // replaces the entry that Back left, so the history does not grow.
  await open("approved");
  expect(await page.evaluate(() => history.length)).toBe(entries);
  expect(await page.getByTestId("reader-position").innerText()).toBe("7 of 7");
  expect(await page.getByTestId("reader-next").isDisabled()).toBe(true);
  expect(await row("approved").getAttribute("aria-current")).toBe("true");
  expect(await row("html").getAttribute("aria-current")).toBeNull();

  // A reload opens the reader for the same packet, and Back still shows the list entry.
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await readerShows("approved");
  await report().getByText("The approved report", { exact: true }).waitFor();
  await page.goBack();
  await listShows();

  // A link opens the reader directly and adds no history entry.
  await page.goto(`${server.url}?reader-link#review/${task.second}`);
  await page.waitForSelector("body[data-ready]");
  await readerShows("second");
  await report().getByText("The second report", { exact: true }).waitFor();
  expect(await page.getByTestId("reader-position").innerText()).toBe("2 of 7");

  // An address that names a task without a packet shows the list entry.
  await page.goto(`${server.url}?reader-absent#review/999999`);
  await page.waitForSelector("body[data-ready]");
  await listShows();

  // The Board task dialog opens the reader directly.
  await page.getByTestId("tab-board").click();
  await page.locator(`[data-testid="board-card"][data-task="${task.second}"]`).getByTestId("board-card-open").click();
  await page.getByTestId("task-dialog-packet").click();
  await readerShows("second");
  await report().getByText("The second report", { exact: true }).waitFor();
  await page.goBack();
  await listShows();
}, 60000);

test("a view change and a Ledger filter change open the first packet of the new list", async () => {
  const { page } = smoke;
  await fresh();
  const entries = await page.evaluate(() => history.length);
  await page.getByTestId("packet-view-ledger").click();
  await readerShows("merged");
  expect(await row("merged").getAttribute("aria-current")).toBe("true");
  expect(await page.evaluate(() => history.length)).toBe(entries);

  // The Cancelled filter holds no packet, so the reader shows no packet.
  await page.getByTestId("ledger-filter-cancelled").click();
  await listShows();
  expect(await page.getByTestId("reader-empty").innerText()).toBe("No cancelled packets yet.");

  // "All" holds packets. The first one opens.
  await page.getByTestId("ledger-filter-all").click();
  await readerShows("merged");
  // A list that holds the open packet keeps it.
  await open("expired", "");
  await page.getByTestId("ledger-filter-approved").click();
  await readerShows("expired");
  await Bun.sleep(300);
  await readerShows("expired");

  await page.getByTestId("packet-view-live").click();
  await readerShows("html");
  await page.screenshot({ path: evidencePath("reader-live-after-ledger-1920.png") });
}, 30000);

test("the Ledger filter fits the narrow queue column beside the chat column", async () => {
  const { page } = smoke;
  await fresh();
  await page.getByTestId("packet-view-ledger").click();
  await page.getByTestId("ledger-filter-all").click();
  await readerShows("merged");
  await page.getByTestId("packet-collie-open").click();
  await page.getByTestId("packet-chat-column").waitFor({ state: "visible" });
  try {
    expect(await page.getByTestId("review-pane").getAttribute("data-queue")).toBe("compact");
    const list = await box("review-list");
    expect(list.width).toBe(200);
    for (const testid of ["ledger-filter-approved", "ledger-filter-cancelled", "ledger-filter-all"]) {
      expect(inside(await box(testid), list)).toBe(true);
    }
    expect(await page.getByTestId("ledger-filters").evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
    // A decided packet keeps its task title on its card in the narrow column.
    const title = row("merged").getByTestId("packet-title");
    expect(await title.innerText()).toBe("merged task");
    expect(inside((await title.boundingBox())!, (await row("merged").boundingBox())!)).toBe(true);
    // A decided packet keeps "Ask Collie" in the decision bar.
    expect(inside(await box("packet-collie-open"), await box("packet-decision-bar"))).toBe(true);
    await page.screenshot({ path: evidencePath("reader-ledger-chat-1920.png") });
  } finally {
    await page.getByTestId("packet-chat-close").click();
    await page.getByTestId("packet-chat-column").waitFor({ state: "hidden" });
  }
}, 30000);

test("the Task button opens the task dialog over each view", async () => {
  const { page } = smoke;
  await fresh();
  const dialog = page.getByTestId("task-dialog");
  const control = page.getByTestId("packet-task-open");
  expect(await control.innerText()).toBe("Task");

  // This page load read no Board data, so the dialog stands on its own read.
  await control.click();
  await dialog.waitFor({ state: "visible" });
  expect(await page.locator("#task-dialog-title").innerText()).toBe("html task");
  // The task description holds the criteria of the packet.
  const items = await dialog.locator(".sec.prose li").allInnerTexts();
  expect(items).toEqual(["The report fills the window.", "The view row stays visible.", "The Files changed view shows each changed file."]);
  await page.getByTestId("task-runs").waitFor();
  // The run rows use the timeline format of the task dialog, not a numbered list.
  expect(await page.getByTestId("task-runs").locator("ol.timeline").first().evaluate((node) => getComputedStyle(node).listStyleType)).toBe("none");
  expect(await page.getByTestId("packet-evidence").count()).toBe(0);
  // The dialog sits over the packet, so it has no link to that packet.
  expect(await page.getByTestId("task-dialog-packet").count()).toBe(0);
  await page.screenshot({ path: evidencePath("reader-task-dialog-1920.png") });

  // The Review page keeps the open dialog in step with the task.
  await page.route(`**/api/tasks/${task.html}`, async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...await response.json(), title: "The refreshed title" } });
  });
  try {
    await page.locator("#task-dialog-title").getByText("The refreshed title").waitFor();
  } finally { await page.unroute(`**/api/tasks/${task.html}`); }

  await page.getByTestId("task-dialog-close").click();
  await dialog.waitFor({ state: "hidden" });
  expect(await page.evaluate(() => document.activeElement?.id)).toBe("task-open");
  await control.click();
  await dialog.waitFor({ state: "visible" });
  await page.keyboard.press("Escape");
  await dialog.waitFor({ state: "hidden" });
  // Escape closes the dialog and keeps the reader open.
  await readerShows("html");

  // The dialog opens over the Files changed view too and keeps that view.
  await page.getByTestId("reader-view-changes").click();
  await page.getByTestId("changes-path").waitFor();
  await control.click();
  await dialog.waitFor({ state: "visible" });
  await page.keyboard.press("Escape");
  await dialog.waitFor({ state: "hidden" });
  expect(await page.getByTestId("reader-view-changes").getAttribute("aria-selected")).toBe("true");
  await readerShows("html");
  // Escape without a dialog or a reason field keeps the reader beside the queue column.
  await page.getByTestId("reader-view-packet").click();
  await page.getByTestId("packet-render").click({ position: { x: 4, y: 4 } }).catch(() => {});
  await page.keyboard.press("Escape");
  await Bun.sleep(200);
  await readerShows("html");
}, 30000);

test("a click on a commit or on the Verifier evidence copies its full value", async () => {
  const { page, context, server } = smoke;
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: new URL(server.url).origin });
  const evidence = `/mc/artifacts/reader/${task.second}/evidence/verification.html`;
  await page.route("**/api/packets", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.packets = body.packets.map((packet: any) => packet.task_id === task.second ? { ...packet, verifier_evidence: evidence } : packet);
    await route.fulfill({ response, json: body });
  });
  const clipboard = () => page.evaluate(() => navigator.clipboard.readText());
  try {
    await page.goto(`${server.url}?reader-copy#review/${task.second}`);
    await page.waitForSelector("body[data-ready]");
    await readerShows("second");
    await page.getByTestId("reader-view-changes").click();
    const submitted = page.getByTestId("changes-facts").locator('[data-key="worker"] button');
    await submitted.waitFor();
    const full = (await submitted.getAttribute("title"))!;
    expect(full).toMatch(/^[0-9a-f]{40}$/);
    expect(await submitted.innerText()).toBe(full.slice(0, 7));
    await submitted.click();
    await until(async () => (await clipboard()) === full, "the full commit is copied");
    await page.getByTestId("changes-facts").getByText("Copied").waitFor();
  } finally { await page.unroute("**/api/packets"); }
}, 30000);

test("decision controls appear only for a packet that awaits a decision, after its report loads", async () => {
  const { page } = smoke;
  await fresh();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  let requested!: () => void;
  const seen = new Promise<void>((resolve) => { requested = resolve; });
  const pattern = `**/api/packets/${task.second}/render?*`;
  await page.route(pattern, async (route) => { requested(); await gate; await route.continue(); });
  try {
    await row("second").click();
    await readerShows("second");
    await seen;
    await page.getByTestId("packet-decide").waitFor({ state: "visible" });
    for (const decision of ["approve", "revise", "cancel"]) {
      expect(await page.getByTestId(`packet-${decision}`).isDisabled()).toBe(true);
    }
    expect(await page.getByTestId("packet-render").innerText()).toContain("Loading packet");
    // The loading state shows a placeholder report, not the thesis.
    expect(await page.getByTestId("packet-loading").isVisible()).toBe(true);
    expect(await page.getByTestId("packet-render").getAttribute("aria-busy")).toBe("true");
    expect(await page.getByTestId("packet-render").innerText()).not.toContain("The second packet thesis");
    release();
    await report().getByText("The second report", { exact: true }).waitFor();
    await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
    for (const decision of ["approve", "revise", "cancel"]) {
      expect(await page.getByTestId(`packet-${decision}`).isEnabled()).toBe(true);
    }
  } finally {
    release();
    await page.unroute(pattern);
  }
  expect(await page.getByTestId("approval-scope").count()).toBe(0);
  expect(await page.locator("#decide-note").isVisible()).toBe(false);
  expect(await page.getByTestId("packet-decision").isVisible()).toBe(false);

  // Cancel opens a reason field in the decision bar. "Never mind" closes it.
  await page.getByTestId("packet-cancel").click();
  await page.getByTestId("packet-reason").waitFor({ state: "visible" });
  expect(await page.getByTestId("packet-decide-submit").isVisible()).toBe(true);
  expect(await page.getByTestId("decide-abandon").isVisible()).toBe(true);
  const reasonBox = await box("packet-reason");
  const decisionBar = await box("packet-decision-bar");
  expect(reasonBox.y).toBeGreaterThanOrEqual(decisionBar.y);
  await page.getByTestId("decide-abandon").click();
  await page.getByTestId("packet-reason").waitFor({ state: "hidden" });

  // Escape keeps a reason field that holds text, and closes an empty one.
  await page.getByTestId("packet-cancel").click();
  await page.getByTestId("packet-reason").fill("Rename the exported function.");
  await page.keyboard.press("Escape");
  expect(await page.getByTestId("packet-reason").isVisible()).toBe(true);
  expect(await page.getByTestId("packet-reason").inputValue()).toBe("Rename the exported function.");
  await page.getByTestId("packet-reason").fill("");
  await page.keyboard.press("Escape");
  await page.getByTestId("packet-reason").waitFor({ state: "hidden" });
  await readerShows("second");

  // The blocked packet states the block reason and has no decision controls.
  await page.getByTestId("reader-next").click();
  await readerShows("blocked");
  await report().getByText("The blocked report", { exact: true }).waitFor();
  expect(await page.getByTestId("packet-decide").isVisible()).toBe(false);
  expect(await page.getByTestId("reader-block-reason").innerText()).toBe("Decision Pending: The operator must confirm the license.");
  expect(await page.getByTestId("reader-tags").innerText()).toContain("Decision Pending");

  // The approved packet shows the approval time and the merge state text.
  await open("approved");
  expect(await page.getByTestId("packet-decide").isVisible()).toBe(false);
  const rows = (await mc(["packet", "list"]))["packets"] as Record<string, unknown>[];
  const mergeState = String(rows.find((p) => p["task_id"] === task.approved)?.["merge_state_text"]).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  expect(await page.getByTestId("packet-decision").innerText()).toMatch(new RegExp(`^Approved on \\w{3} \\d{1,2}, \\d{4} \\d{2}:\\d{2}\\. ${mergeState}$`));

  // The merged packet shows the approval time, the branch, and the merge commit.
  await page.getByTestId("packet-view-ledger").click();
  await readerShows("merged");
  expect(await page.getByTestId("reader-position").innerText()).toBe("1 of 2");
  expect(await page.getByTestId("packet-decide").isVisible()).toBe(false);
  expect(await page.getByTestId("packet-decision").innerText()).toMatch(/^Approved on \w{3} \d{1,2}, \d{4} \d{2}:\d{2}\. Merged into culler at [0-9a-f]{40}\.$/);
  await page.getByTestId("packet-view-live").click();
  await readerShows("html");
}, 30000);

test("a packet with an expired report and a packet without a report show their states without an error notice", async () => {
  const { page } = smoke;
  await fresh();
  await page.getByTestId("packet-view-ledger").click();
  await readerShows("merged");
  await row("expired").click();
  await readerShows("expired");
  await page.getByTestId("packet-expired").waitFor();
  expect(await page.getByTestId("packet-expired").innerText()).toMatch(/^The packet report expired on Sep \d{1,2}, 2026 \d{2}:\d{2}\.$/);
  const expired = await page.getByTestId("packet-render").innerText();
  expect(expired).toContain("The expired packet thesis");
  expect(expired).toContain("The report fills the window.");
  expect(expired).not.toContain("docs/reader.md");
  expect(await page.getByTestId("packet-frame").count()).toBe(0);
  expect(await page.getByTestId("packet-decide").isVisible()).toBe(false);
  // The Files changed view states the expiry of the change data.
  await page.getByTestId("reader-view-changes").click();
  await page.getByTestId("changes-unavailable").waitFor();
  expect(await page.getByTestId("changes-unavailable").innerText())
    .toMatch(/^The change data expired on Sep \d{1,2}, 2026 \d{2}:\d{2}\. The repository keeps the commits\.$/);
  expect(await page.getByTestId("changes-body").isVisible()).toBe(false);
  await noErrorNotice();

  await page.getByTestId("packet-view-live").click();
  await readerShows("html");
  await row("missing").click();
  await readerShows("missing");
  await page.getByTestId("packet-render").getByText("No report exists yet for this packet.").waitFor();
  expect(await page.getByTestId("packet-render").innerText()).toContain("The missing packet thesis");
  expect(await page.getByTestId("packet-frame").count()).toBe(0);
  // A decision requires the report, and this packet has none.
  expect(await page.getByTestId("packet-approve").isDisabled()).toBe(true);
  expect(await page.getByTestId("decide-note").innerText()).toBe("A decision requires the report of the packet. This packet has no report.");
  await noErrorNotice();
  // A report that loads shows no thesis in the report area.
  await page.getByTestId("reader-previous").click();
  await readerShows("blocked");
  await report().getByText("The blocked report", { exact: true }).waitFor();
  expect(await page.getByTestId("packet-render").innerText()).not.toContain("The blocked packet thesis");
}, 30000);

test("a failed report read shows its error inside the reader", async () => {
  const { page } = smoke;
  await fresh();
  await row("broken").click();
  await readerShows("broken");
  const error = page.getByTestId("reader-error");
  await error.waitFor({ state: "visible" });
  expect(await error.innerText()).toContain("The report differs from its recorded version.");
  expect(await page.getByTestId("packet-reader").getByTestId("reader-error").count()).toBe(1);
  expect(await page.getByTestId("error").isVisible()).toBe(false);
  expect(await page.getByTestId("packet-render").innerText()).toContain("The report is unavailable.");
  expect(await page.getByTestId("packet-frame").count()).toBe(0);
  expect(await page.getByTestId("packet-approve").isDisabled()).toBe(true);
  // The error sits between the view row and the report area.
  const views = await box("reader-views");
  const shown = await box("reader-error");
  expect(shown.y).toBeCloseTo(views.y + views.height, 0);
  expect(shown.width).toBe((await box("packet-reader")).width);
}, 30000);

test("a page error shows in the page error area beside the queue column and inside a reader that covers the page", async () => {
  const { page } = smoke;
  await fresh();
  await open("second");
  await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
  let arrived!: () => void;
  let release!: () => void;
  const seen = new Promise<void>((resolve) => { arrived = resolve; });
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const refused = "The selected report changed.";
  const pattern = `**/api/packets/${task.second}/decide`;
  await page.route(pattern, async (route) => {
    expect(route.request().postDataJSON()).toMatchObject({ decision: "approve", packet_id: packet.second.packetID, version_id: packet.second.versionID });
    arrived();
    await gate;
    await route.fulfill({ status: 409, json: { error: { message: refused } } });
  });
  try {
    await page.getByTestId("packet-approve").click();
    await seen;
    await listShows();
    const approvalRow = row("second");
    expect(await approvalRow.getAttribute("data-state")).toBe("awaits_decision");
    expect(await approvalRow.getByTestId("packet-progress").innerText()).toBe("Approval in progress");
    expect(await page.getByTestId("reader-empty").innerText()).toContain(`Task #${task.second}`);
    release();
    // A failure after the reader closes shows a page error that names the task.
    const message = `The decision for Task #${task.second} failed. ${refused}`;
    await page.getByTestId("error").getByText(message).waitFor();
    expect(await approvalRow.getByTestId("packet-progress").count()).toBe(0);
    expect(await approvalRow.getAttribute("data-state")).toBe("awaits_decision");

    // Beside the queue column, the page error stays in the page error area.
    await open("html", "The reader report");
    expect(await page.getByTestId("error").innerText()).toContain(message);
    expect(await page.getByTestId("reader-error").isVisible()).toBe(false);

    // A reader that covers the page shows the page error, and each other part
    // of the page is inert.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByTestId("reader-error").getByText(message).waitFor();
    expect(await page.getByTestId("error").isVisible()).toBe(false);
    for (const testid of ["tab-board", "packets", "error", "footer", "system-notices"]) {
      expect(await page.getByTestId(testid).evaluate((node) => node.closest("[inert]") !== null)).toBe(true);
    }
    expect(await page.getByTestId("packet-reader").evaluate((node) => node.closest("[inert]") === null)).toBe(true);
    // The page error returns to the page when the reader stops covering it.
    await page.setViewportSize(WIDE);
    await page.getByTestId("error").getByText(message).waitFor();
    expect(await page.getByTestId("reader-error").isVisible()).toBe(false);
    expect(await page.getByTestId("tab-board").evaluate((node) => node.closest("[inert]") === null)).toBe(true);
  } finally {
    release();
    await page.setViewportSize(WIDE);
    await page.unroute(pattern);
  }
  // A later page read keeps the error of the failed decision until the operator dismisses it.
  await completePageRead(page, "packets");
  expect(await page.getByTestId("error").innerText()).toContain(`The decision for Task #${task.second} failed.`);
  await page.getByTestId("error").getByRole("button", { name: "Dismiss" }).click();
  await page.getByTestId("error").waitFor({ state: "hidden" });
}, 30000);

test("while the page sends a decision, the reader keeps the packet and the reason", async () => {
  const { page } = smoke;
  await fresh();
  await open("second");
  await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-revise"]') as HTMLButtonElement).disabled);
  let pending: { arrived: () => void; gate: Promise<void> } | null = null;
  const expectSend = () => {
    let arrived!: () => void;
    let release!: () => void;
    const seen = new Promise<void>((resolve) => { arrived = resolve; });
    const gate = new Promise<void>((resolve) => { release = resolve; });
    pending = { arrived, gate };
    return { seen, release };
  };
  const refused = "The selected report changed.";
  const pattern = `**/api/packets/${task.second}/decide`;
  await page.route(pattern, async (route) => {
    const send = pending!;
    send.arrived();
    await send.gate;
    await route.fulfill({ status: 409, json: { error: { message: refused } } });
  });
  const reason = "Rename the exported function.";
  const first = expectSend();
  let second = first;
  try {
    await page.getByTestId("packet-cancel").click();
    await page.getByTestId("packet-reason").fill(reason);
    await page.getByTestId("packet-decide-submit").click();
    await first.seen;
    await until(async () => (await page.getByTestId("decide-note").innerText()) === "The page sends the decision.", "the note states the send");
    for (const testid of ["reader-close", "reader-previous", "reader-next", "packet-approve", "packet-revise", "packet-cancel",
      "packet-decide-submit", "decide-abandon"]) {
      expect(await page.getByTestId(testid).isDisabled()).toBe(true);
    }
    expect(await page.getByTestId("packet-reason").isVisible()).toBe(true);
    expect(await page.getByTestId("packet-reason").isEditable()).toBe(false);
    expect(await page.getByTestId("packet-reason").inputValue()).toBe(reason);
    // Escape, a click on a card, and a view change have no effect while the page sends.
    await page.keyboard.press("Escape");
    await row("html").click();
    await page.getByTestId("packet-view-ledger").click();
    await Bun.sleep(300);
    await readerShows("second");
    await page.getByTestId("packet-view-live").click();
    expect(await page.getByTestId("packet-reason").isVisible()).toBe(true);

    // A failure keeps the reason field and its text, and shows the error in the reader.
    first.release();
    await page.getByTestId("reader-error").getByText(refused).waitFor();
    expect(await page.getByTestId("packet-reason").inputValue()).toBe(reason);
    expect(await page.getByTestId("packet-reason").isEditable()).toBe(true);
    expect(await page.getByTestId("packet-decide-submit").isEnabled()).toBe(true);
    expect(await page.getByTestId("reader-next").isEnabled()).toBe(true);
    await page.getByTestId("reader-error").getByRole("button", { name: "Dismiss" }).click();

    // A failure after the reader moves to another packet names the task of the decision.
    second = expectSend();
    await page.getByTestId("packet-decide-submit").click();
    await second.seen;
    await page.goBack();
    await listShows();
    await open("html", "The reader report");
    expect(await page.getByTestId("packet-approve").isDisabled()).toBe(true);
    second.release();
    await page.getByTestId("error").getByText(`The decision for Task #${task.second} failed. ${refused}`).waitFor();
    await readerShows("html");
    await page.getByTestId("error").getByRole("button", { name: "Dismiss" }).click();
  } finally {
    first.release();
    second.release();
    await page.unroute(pattern);
  }
}, 30000);

test("with two notices, the Review page fits a laptop window and keeps the decision bar in view", async () => {
  const { page, server } = smoke;
  try {
    for (const size of [{ width: 1280, height: 800 }, { width: 1024, height: 768 }]) {
      await page.setViewportSize(size);
      await page.goto(`${server.url}?reader-fit-${size.width}#review/${task.second}`);
      await page.waitForSelector("body[data-ready]");
      await readerShows("second");
      await report().getByText("The second report", { exact: true }).waitFor();
      await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
      // The fixture shows a system notice and a Project action above the panel.
      await systemNoticeShown(page);
      expect(await page.locator("main .project-action").count()).toBeGreaterThan(0);
      // The page itself does not scroll, and each part of the reader is in view.
      expect(await page.evaluate(() => document.scrollingElement!.scrollHeight)).toBe(size.height);
      const reader = await box("packet-reader");
      expect(reader.y + reader.height).toBeLessThanOrEqual(size.height);
      for (const testid of ["reader-next", "reader-view-changes", "packet-approve", "packet-revise", "packet-cancel"]) {
        const control = await box(testid);
        expect(control.y + control.height).toBeLessThanOrEqual(reader.y + reader.height);
      }
      // The Task button and the previous and next controls stay together at the right.
      const taskControl = await box("packet-task-open");
      const previous = await box("reader-previous");
      expect(previous.y).toBeCloseTo(taskControl.y + (taskControl.height - previous.height) / 2, 0);
      expect(previous.x).toBeGreaterThan(taskControl.x);
      const next = await box("reader-next");
      expect(next.x + next.width).toBeGreaterThan(reader.x + reader.width - 40);
      // The report area keeps a readable height between the fixed parts of the reader.
      expect((await box("packet-frame")).height).toBeGreaterThan(80);
      await page.screenshot({ path: evidencePath(`reader-fit-${size.width}.png`) });
      // The row of the open packet stays in view when the reader moves.
      for (const name of ["blocked", "missing", "broken", "decide", "approved"]) {
        await page.getByTestId("reader-next").click();
        await readerShows(name);
      }
      const list = await box("packets");
      const open = await row("approved").boundingBox();
      expect(inside(open!, list)).toBe(true);
    }
  } finally {
    await page.setViewportSize(WIDE);
  }
}, 30000);

test("at 1024 px a task packet keeps its controls and commit facts visible", async () => {
  const { page, server } = smoke;
  const size = { width: 1024, height: 768 };
  const evidence = `/mc/artifacts/reader/${task.second}/evidence/${"verification-".repeat(8)}evidence.html`;
  await page.route("**/api/packets", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.packets = body.packets.map((row: any) => row.task_id === task.second
      ? { ...row, verifier_evidence: evidence } : row);
    await route.fulfill({ response, json: body });
  });
  try {
    await page.setViewportSize(size);
    await page.goto(`${server.url}?reader-task#review/${task.second}`);
    await page.waitForSelector("body[data-ready]");
    await readerShows("second");
    // The queue column stays beside the reader at 1024 px.
    expect((await box("review-list")).width).toBe(264);
    expect((await box("reader-views")).height).toBeLessThan(48);
    const bar = await box("reader-bar");
    expect(inside(await box("packet-task-open"), bar)).toBe(true);
    // The page fits the window, and the controls fit the reader.
    const reader = await box("packet-reader");
    for (const testid of ["reader-next", "packet-task-open", "packet-cancel"]) {
      const control = await box(testid);
      expect(control.x + control.width).toBeLessThanOrEqual(reader.x + reader.width);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(size.width);
    await page.screenshot({ path: evidencePath("reader-1024.png") });

    // The Files changed view names each commit in the comparison.
    await page.getByTestId("reader-view-changes").click();
    await page.getByTestId("changes-path").waitFor();
    const labels = await page.getByTestId("changes-facts").locator(".fact-label").allInnerTexts();
    expect(labels).toEqual(["Destination commit", "Merge candidate commit", "Verified Worker commit"]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(size.width);
    await page.screenshot({ path: evidencePath("reader-1024-changes.png") });

    await page.getByTestId("packet-task-open").click();
    await page.getByTestId("task-dialog-facts").waitFor();
    await page.screenshot({ path: evidencePath("reader-1024-task-dialog.png") });
    await page.getByTestId("task-dialog-close").click();
  } finally {
    await page.setViewportSize(WIDE);
    await page.unroute("**/api/packets");
  }
}, 30000);

test("a Markdown report keeps a readable line width in the report area", async () => {
  const { page } = smoke;
  await fresh();
  const pattern = `**/api/packets/${task.second}/render?*`;
  const prose = Array.from({ length: 12 }, (_, i) => `Sentence ${i + 1} of the paragraph states one fact about the change.`).join(" ");
  await page.route(pattern, (route) => route.fulfill({ contentType: "text/markdown; charset=utf-8", body: `# The second report\n\n${prose}\n` }));
  try {
    await row("second").click();
    await readerShows("second");
    await page.getByTestId("packet-markdown").waitFor();
    const reader = await box("packet-reader");
    expect((await box("packet-render")).width).toBe(reader.width);
    const text = await box("packet-markdown");
    expect(text.width).toBeGreaterThan(600);
    expect(text.width).toBeLessThan(reader.width / 1.5);
  } finally { await page.unroute(pattern); }
}, 30000);

test("at 390 px the reader covers the window and fits its width", async () => {
  const { page, server } = smoke;
  const narrow = { width: 390, height: 844 };
  try {
    await page.setViewportSize(narrow);
    await page.goto(`${server.url}?reader-narrow#review`);
    await page.waitForSelector("body[data-ready]");
    await row("html").waitFor();
    // The narrow layout opens a packet only when the operator selects one.
    await Bun.sleep(300);
    await listShows();
    expect(await page.getByTestId("reader-empty").isVisible()).toBe(false);
    await open("html", "The reader report");
    expect(await box("packet-reader")).toEqual({ x: 0, y: 0, ...narrow });
    // The focus moves into the reader, and each other part of the page is inert.
    expect(await page.evaluate(() => document.querySelector('[data-testid="packet-reader"]')!.contains(document.activeElement))).toBe(true);
    for (const testid of ["tab-board", "packets", "error", "footer"]) {
      expect(await page.getByTestId(testid).evaluate((node) => node.closest("[inert]") !== null)).toBe(true);
    }
    expect(await page.getByTestId("reader-close").innerText()).toMatch(/^Review Queue\s*6$/);
    const fit = await page.evaluate((width) => {
      const reader = document.querySelector('[data-testid="packet-reader"]')!;
      const outside = [...reader.querySelectorAll("*")].filter((node) => {
        if (!node.checkVisibility({ visibilityProperty: true })) return false;
        const rect = node.getBoundingClientRect();
        return rect.right > width + 0.5 || rect.left < -0.5;
      }).map((node) => node.id || node.className || node.tagName);
      return { outside, scroll: document.documentElement.scrollWidth };
    }, narrow.width);
    expect(fit.outside).toEqual([]);
    expect(fit.scroll).toBeLessThanOrEqual(narrow.width);
    // The view row is one row: the three views and the Task button.
    const views = await box("reader-views");
    expect(views.height).toBeLessThan(64);
    expect(await page.getByTestId("reader-views").getByRole("tab").allInnerTexts()).toEqual(["Packet", "Files changed · 3", "Collie"]);
    for (const testid of ["reader-view-packet", "reader-view-changes", "reader-view-collie", "packet-task-open"]) {
      expect(inside(await box(testid), views)).toBe(true);
    }
    expect(await page.getByTestId("packet-collie-open").isVisible()).toBe(false);
    expect(await page.getByTestId("reader-view-changes").evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
    // Each reader control is at least 40 px tall.
    for (const testid of ["reader-close", "reader-previous", "reader-next", "reader-view-packet", "reader-view-changes", "reader-view-collie",
      "packet-task-open", "packet-approve", "packet-revise", "packet-cancel"]) {
      expect((await box(testid)).height).toBeGreaterThanOrEqual(40);
    }
    // The decision controls stay visible at the bottom of the window.
    await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
    for (const decision of ["approve", "revise", "cancel"]) {
      const control = await box(`packet-${decision}`);
      expect(control.x).toBeGreaterThanOrEqual(0);
      expect(control.x + control.width).toBeLessThanOrEqual(narrow.width);
      expect(control.y + control.height).toBeLessThanOrEqual(narrow.height);
      expect(await page.getByTestId(`packet-${decision}`).isEnabled()).toBe(true);
    }
    const bar = await box("packet-decision-bar");
    expect(bar.y + bar.height).toBeCloseTo(narrow.height, 0);
    expect((await box("packet-frame")).height).toBeGreaterThan(narrow.height / 3);
    await page.screenshot({ path: evidencePath("reader-390.png") });

    await page.getByTestId("packet-task-open").click();
    const dialog = page.getByTestId("task-dialog");
    await dialog.waitFor({ state: "visible" });
    // The dialog takes input over a reader that covers the page.
    expect(await dialog.evaluate((node) => node.closest("[inert]") === null)).toBe(true);
    await page.screenshot({ path: evidencePath("reader-390-task-dialog.png") });
    await page.keyboard.press("Escape");
    await dialog.waitFor({ state: "hidden" });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(narrow.width);

    // Escape closes a reader that covers the page, and the close control does too.
    await page.keyboard.press("Escape");
    await listShows();
    expect(await page.evaluate(() => document.activeElement?.getAttribute("data-task"))).toBe(String(task.html));
    expect(await page.getByTestId("tab-board").evaluate((node) => node.closest("[inert]") === null)).toBe(true);
    await open("second");
    await page.getByTestId("reader-close").click();
    await listShows();
    expect(await page.evaluate(() => document.activeElement?.getAttribute("data-task"))).toBe(String(task.second));
  } finally {
    await page.setViewportSize(WIDE);
  }
}, 30000);

test("approval advances the reader before a slow packet refresh", async () => {
  const { page } = smoke;
  await fresh();
  await open("decide");
  await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
  let hold = false;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/packets", async (route) => {
    if (hold) {
      await gate;
      await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "Packet read failed." }) });
    } else await route.continue();
  });
  try {
    hold = true;
    const decided = page.waitForResponse((response) => new URL(response.url()).pathname === `/api/packets/${task.decide}/decide`);
    await page.getByTestId("packet-approve").click();
    expect((await decided).ok()).toBe(true);
    await readerShows("html");
    const row = page.locator(`[data-testid="packet-row"][data-task="${task.decide}"]`);
    expect(await row.getAttribute("data-state")).toBe("approved");
    expect(await row.innerText()).toContain("Approval saved. The change awaits a merge.");
    expect(await row.getAttribute("aria-current")).toBeNull();
    expect(await row.getByTestId("packet-progress").innerText()).toBe("Merging...");
    expect(await row.locator(".packet-spinner").evaluate((node) => getComputedStyle(node).animationName)).toBe("profile-spin");
    await page.emulateMedia({ reducedMotion: "reduce" });
    expect(await row.locator(".packet-spinner").evaluate((node) => getComputedStyle(node).animationName)).toBe("none");
    await page.emulateMedia({ reducedMotion: "no-preference" });
    expect(await page.evaluate(() => document.activeElement?.id)).toBe("packet-reader");
    await page.screenshot({ path: evidencePath("approval-1920.png") });
    release();
    await page.waitForFunction(() => document.getElementById("error-message")?.textContent?.includes("The decision was saved."));
    expect(await row.getAttribute("data-state")).toBe("approved");
    expect(await mc(["task", "get", String(task.decide)])).toMatchObject({ decision: "approved" });
  } finally {
    release();
    await page.unroute("**/api/packets");
  }
}, 30000);

test("approval closes the narrow reader and retains the merge state in the list", async () => {
  const { page } = smoke;
  await fresh();
  await page.setViewportSize({ width: 390, height: 844 });
  try {
    await page.getByTestId("reader-close").click();
    await listShows();
    await open("second");
    await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
    await page.getByTestId("packet-approve").click();
    await listShows();
    await page.waitForFunction((id) => document.querySelector(`[data-testid="packet-row"][data-task="${id}"]`)?.getAttribute("data-state") === "approved", task.second);
    const row = page.locator(`[data-testid="packet-row"][data-task="${task.second}"]`);
    expect(await row.getAttribute("data-state")).toBe("approved");
    expect(await row.innerText()).toContain("Approved");
    expect(await page.evaluate(() => document.activeElement?.id)).toBe("packets");
    await page.screenshot({ path: evidencePath("approval-390.png") });
  } finally {
    await page.setViewportSize(WIDE);
  }
}, 30000);
