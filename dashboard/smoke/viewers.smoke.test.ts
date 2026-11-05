import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { Locator, Page } from "playwright";
import { WIKI_LIBRARIES } from "../src/wiki-server";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, SMOKE_TOKEN, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket } from "./delivery-fixture";

// The header, the Project menu, and the run lists link to the wiki viewer and
// to AgentsView. Each link opens a separate browser tab. The fixtures supply a
// wiki export, a stand-in for AgentsView, and runs with a main trace.

let smoke: Smoke;
let taskID = 0;
const WIDE = { width: 1280, height: 900 };
const NARROW = { width: 390, height: 844 };
const COMMIT = "0123456789abcdef0123456789abcdef01234567";
const CLAUDE_REF = "11111111-1111-4111-8111-111111111111";
const CODEX_REF = "22222222-2222-4222-8222-222222222222";
const RUN = { claude: "trace-claude-worker", fake: "trace-fake-arbiter", codex: "trace-codex-verifier" };
const externalRequests: string[] = [];
const evidence = evidencePath("viewers-evidence");

const tracesHref = () => `/agentsview/open?path=${encodeURIComponent("/sessions?include_one_shot=true&include_automated=true")}`;
const traceHref = (path: string) => `/agentsview/open?path=${encodeURIComponent(path)}`;
const CLAUDE_PATH = `/sessions/${CLAUDE_REF}?msg=last`;
const CODEX_PATH = `/sessions/codex/${CODEX_REF}?msg=last`;
const wikiHref = () => new URL(`/${smoke.wikiKey}/viewer-alpha/`, smoke.wiki.url).href;
const TRACES_TITLE = "All traces in AgentsView. The trace list opens in a new tab.";

// The page of an OpenWiki export: the libraries come from the CDN, and the
// Google Fonts links load a font.
function exportPage() {
  const scripts = WIKI_LIBRARIES.map((library) =>
    `    <script src="${library.source}" integrity="${library.integrity}" crossorigin="anonymous"></script>`).join("\n");
  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'self' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; img-src 'self' data:; font-src 'self' https://fonts.gstatic.com; connect-src 'self'; base-uri 'none'; form-action 'none'" />
    <title>OpenWiki</title>
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&display=swap" rel="stylesheet" />
${scripts}
    <link rel="stylesheet" href="./styles.css" />
    <script type="module" src="./client.js"></script>
  </head>
  <body data-static-export="true"><main id="wiki"></main></body>
</html>
`;
}

// The export command writes five files. The client imports its helper module.
const CLIENT = `import { nodeBody } from "./client-lib.js";
const graph = await (await fetch("./graph.json")).json();
mermaid.initialize({ startOnLoad: false });
new ForceGraph();
document.getElementById("wiki").innerHTML = DOMPurify.sanitize(marked.parse(nodeBody(graph.nodes[0])));
document.body.dataset.ready = "true";
`;
const CLIENT_LIB = `export function nodeBody(node) {
  return node.body;
}
`;

function writeExport(home: string) {
  const root = join(home, "wiki", "viewer-alpha");
  const folder = join(root, COMMIT);
  mkdirSync(folder, { recursive: true });
  writeFileSync(join(folder, "index.html"), exportPage());
  writeFileSync(join(folder, "client.js"), CLIENT);
  writeFileSync(join(folder, "client-lib.js"), CLIENT_LIB);
  writeFileSync(join(folder, "styles.css"), "main { font-family: system-ui, sans-serif; }\n");
  writeFileSync(join(folder, "graph.json"), JSON.stringify({
    nodes: [{ id: "index", title: "Architecture", body: "# Architecture\n\nThe accepted source defines this behavior.\n" }], edges: [],
  }));
  writeFileSync(join(root, "export.json"), JSON.stringify({
    project: "viewer-alpha", commit: COMMIT, tree: "89abcdef0123456789abcdef0123456789abcdef", directory: COMMIT,
    exported_at: new Date().toISOString(),
  }));
}

beforeAll(async () => {
  smoke = await bootSmoke();
  smoke.page.setDefaultTimeout(10000);
  await smoke.page.setViewportSize(WIDE);
  mkdirSync(evidence, { recursive: true });
  await seedProject(smoke, "viewer-alpha", { title: "Viewer Alpha", vision: false });
  await seedProject(smoke, "viewer-beta", { title: "Viewer Beta", vision: false });
  taskID = Number((await mc(["task", "add", "traced task", "--project", "viewer-alpha"])).task_id);
  const db = openSpine(smoke);
  try {
    db.run("UPDATE settings SET review_wip_cap=10 WHERE id=1");
    seedDeliveryPacket(db, { taskID, thesis: "The traced packet thesis", renderPath: "" });
    // The newest runs of the task: a Claude run, a run without a main trace,
    // and a Codex run.
    const run = db.prepare(`INSERT INTO runs(id,tier,role,project,subject,ended_at,outcome,binding,native_session_ref,trace_filename)
      VALUES (?,'pipeline',?,'viewer-alpha',?,datetime('now'),'completed',?,?,?)`);
    run.run(RUN.claude, "worker", taskID, "claude-sdk/claude", CLAUDE_REF, "native.jsonl");
    run.run(RUN.fake, "arbiter", taskID, "fake/fake", null, null);
    run.run(RUN.codex, "verifier", taskID, "codex/chatgpt", CODEX_REF, "rollout-2026-09-29T00-00-00-22222222-2222-4222-8222-222222222222.jsonl");
  } finally { db.close(); }
  writeExport(smoke.home);
  // A prepared AgentsView unit makes the status command ask AgentsView.
  mkdirSync(join(smoke.home, "supervision", "LaunchAgents"), { recursive: true });
  writeFileSync(join(smoke.home, "supervision", "LaunchAgents", "agentsview.plist"), "<plist></plist>\n");
  const local = new Set([new URL(smoke.server.url).origin, new URL(smoke.wiki.url).origin, smoke.agentsview.url]);
  await smoke.context.route("**/*", async (route) => {
    if (local.has(new URL(route.request().url()).origin) || new URL(route.request().url()).hostname === "127.0.0.1") await route.continue();
    else { externalRequests.push(route.request().url()); await route.abort(); }
  });
  await gotoReady(smoke);
  await selectProject(smoke, "viewer-alpha");
}, 90000);

afterAll(async () => { await teardown(smoke); });

async function hrefIs(testid: string, href: string | null) {
  await smoke.page.waitForFunction(([id, value]) =>
    document.querySelector(`[data-testid="${id}"]`)?.getAttribute("href") === value, [testid, href] as const, { timeout: 15000 });
}

async function linkState(link: Locator) {
  return link.evaluate((node) => ({
    href: node.getAttribute("href"),
    target: node.getAttribute("target"),
    rel: node.getAttribute("rel"),
    disabled: node.getAttribute("aria-disabled"),
    tabindex: node.getAttribute("tabindex"),
    title: node.getAttribute("title"),
    label: node.getAttribute("aria-label"),
  }));
}

/** Opens a link and returns the new browser tab after it loads. */
async function openTab(link: Locator): Promise<Page> {
  const [tab] = await Promise.all([smoke.context.waitForEvent("page"), link.click()]);
  await tab.waitForLoadState("domcontentloaded");
  return tab;
}

async function openTask() {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await page.locator(`[data-testid="board-card"][data-task="${taskID}"]`).getByTestId("board-card-open").click();
  await page.getByTestId(`task-run-diagnostics-${RUN.claude}`).waitFor();
}

async function closeTask() {
  await smoke.page.getByTestId("task-dialog-close").click();
}

async function openReaderRuns() {
  const { page } = smoke;
  await page.getByTestId("tab-review").click();
  await page.locator(`[data-testid="packet-row"][data-task="${taskID}"]`).click();
  await page.getByTestId("packet-reader").waitFor();
  await page.getByTestId("packet-task-open").click();
  await page.getByTestId("task-runs").waitFor();
}

/** Closes the task dialog. A reader that covers the window closes too. */
async function closeReader() {
  const { page } = smoke;
  await closeTask();
  if (!(await page.getByTestId("reader-close").isVisible())) return;
  await page.getByTestId("reader-close").click();
  await page.getByTestId("packet-reader").waitFor({ state: "hidden" });
}

async function noHorizontalScroll() {
  expect(await smoke.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
}

test("the header links open the wiki and the traces in a new tab", async () => {
  const { page } = smoke;
  await hrefIs("header-traces", tracesHref());
  await hrefIs("header-wiki", wikiHref());
  for (const [testid, name] of [["header-wiki", "Wiki"], ["header-traces", "Traces"]] as const) {
    const link = page.getByTestId(testid);
    expect(await link.evaluate((node) => node.tagName)).toBe("A");
    expect(await link.evaluate((node) => node.classList.contains("tab") || node.hasAttribute("data-page") || node.closest("nav") !== null)).toBe(false);
    const state = await linkState(link);
    expect(state).toMatchObject({ target: "_blank", rel: "noopener noreferrer", disabled: null, label: `${name}, opens a new tab` });
    expect(state.href).not.toContain(SMOKE_TOKEN);
    expect((await link.innerText()).trim()).toBe(`${name} ↗`);
  }
  expect(await page.getByTestId("header-wiki").getAttribute("title")).toBe("Wiki of Viewer Alpha at commit 0123456");
  expect(await page.getByTestId("header-traces").getAttribute("title")).toBe(TRACES_TITLE);
  expect(await page.getByTestId("header-traces").getAttribute("href")).not.toContain(smoke.agentsviewToken);

  const wiki = await openTab(page.getByTestId("header-wiki"));
  try {
    await wiki.waitForSelector("body[data-ready]", { timeout: 15000 });
    expect(wiki.url()).toBe(wikiHref());
    expect(await wiki.getByRole("heading", { name: "Architecture" }).isVisible()).toBe(true);
    expect(await wiki.evaluate(() => [window.opener, document.referrer])).toEqual([null, ""]);
    expect(await wiki.evaluate(() => [...document.scripts].map((script) => new URL(script.src).pathname)))
      .toEqual([...WIKI_LIBRARIES.map((library) => `/${smoke.wikiKey}/_openwiki/${library.file}`), `/${smoke.wikiKey}/viewer-alpha/client.js`]);
    await wiki.screenshot({ path: join(evidence, "wiki-tab.png") });
  } finally { await wiki.close(); }

  const traces = await openTab(page.getByTestId("header-traces"));
  try {
    await traces.waitForURL(url => url.pathname === "/sessions");
    expect(new URL(traces.url()).search).toBe("?include_one_shot=true&include_automated=true");
    expect(await traces.getByText("AgentsView fixture").isVisible()).toBe(true);
    expect(await traces.evaluate(() => [window.opener, document.referrer])).toEqual([null, ""]);
  } finally { await traces.close(); }
  expect(externalRequests).toEqual([]);
  await page.screenshot({ path: join(evidence, "header-wide.png") });
}, 45000);

test("the wiki address answers only below the wiki key", async () => {
  const wrong = smoke.wikiKey.replace(/^./, (first) => (first === "0" ? "1" : "0"));
  const statuses: Record<string, number> = {};
  for (const path of [
    "/viewer-alpha/", `/_openwiki/${WIKI_LIBRARIES[0]!.file}`,
    `/${wrong}/viewer-alpha/`, `/${wrong}/_openwiki/${WIKI_LIBRARIES[0]!.file}`,
    `/${smoke.wikiKey}/viewer-alpha/`, `/${smoke.wikiKey}/_openwiki/${WIKI_LIBRARIES[0]!.file}`,
  ]) {
    statuses[path.replace(smoke.wikiKey, "KEY").replace(wrong, "WRONG")] = (await fetch(new URL(path, smoke.wiki.url))).status;
  }
  expect(statuses).toEqual({
    "/viewer-alpha/": 404, [`/_openwiki/${WIKI_LIBRARIES[0]!.file}`]: 404,
    "/WRONG/viewer-alpha/": 404, [`/WRONG/_openwiki/${WIKI_LIBRARIES[0]!.file}`]: 404,
    "/KEY/viewer-alpha/": 200, [`/KEY/_openwiki/${WIKI_LIBRARIES[0]!.file}`]: 200,
  });
});

test("the Project menu gives each Project its wiki link", async () => {
  const { page } = smoke;
  await page.getByTestId("project-button").click();
  await page.getByTestId("project-listbox").waitFor({ state: "visible" });
  const alpha = page.getByTestId("project-wiki-viewer-alpha");
  const beta = page.getByTestId("project-wiki-viewer-beta");
  expect(await linkState(alpha)).toMatchObject({
    href: wikiHref(), target: "_blank", rel: "noopener noreferrer", disabled: null,
    title: "Wiki of Viewer Alpha at commit 0123456", label: "Wiki of Viewer Alpha, opens a new tab",
  });
  expect(await linkState(beta)).toMatchObject({
    href: null, disabled: "true", tabindex: "0",
    title: "Viewer Beta has no wiki export. Culler exports the wiki when the accepted commit holds an openwiki/ folder.",
  });
  // A wiki link is not a Project option: it has no selection state, and the
  // arrow keys move between the options only.
  for (const link of [alpha, beta]) {
    expect(await link.evaluate((node) => node.classList.contains("option") || node.hasAttribute("aria-current") || node.hasAttribute("aria-selected"))).toBe(false);
  }
  await page.getByTestId("project-viewer-alpha").focus();
  await page.keyboard.press("ArrowDown");
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("project-viewer-beta");
  await page.keyboard.press("Tab");
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("project-wiki-viewer-beta");
  await page.screenshot({ path: join(evidence, "project-menu-wide.png") });

  // The list closes after the link opens the wiki.
  const wiki = await openTab(alpha);
  try {
    expect(wiki.url()).toBe(wikiHref());
  } finally { await wiki.close(); }
  await page.getByTestId("project-listbox").waitFor({ state: "hidden" });
  await selectProject(smoke, "viewer-beta");
  await hrefIs("header-wiki", null);
  expect(await linkState(page.getByTestId("header-wiki"))).toMatchObject({
    disabled: "true", tabindex: "0",
    title: "Viewer Beta has no wiki export. Culler exports the wiki when the accepted commit holds an openwiki/ folder.",
  });
  await selectProject(smoke, "viewer-alpha");
  await hrefIs("header-wiki", wikiHref());
}, 30000);

test("each run with a main trace links to its AgentsView session", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-pipeline").click();
  await page.locator(`[data-testid="run-row"][data-key="${RUN.claude}"]`).waitFor();
  const row = (id: string) => page.locator(`[data-testid="run-row"][data-key="${id}"]`);
  expect(await row(RUN.claude).getByTestId("run-trace-link").getAttribute("href")).toBe(traceHref(CLAUDE_PATH));
  expect(await row(RUN.codex).getByTestId("run-trace-link").getAttribute("href")).toBe(traceHref(CODEX_PATH));
  expect(await row(RUN.fake).getByTestId("run-trace-link").count()).toBe(0);
  expect(await row(RUN.fake).getByTestId("run-log-link").count()).toBe(1);
  expect(await linkState(row(RUN.claude).getByTestId("run-trace-link"))).toMatchObject({
    target: "_blank", rel: "noopener noreferrer", title: "The trace opens at its last message in AgentsView.",
    label: `Trace of run ${RUN.claude}, opens a new tab`,
  });
  const trace = await openTab(row(RUN.claude).getByTestId("run-trace-link"));
  try {
    await trace.waitForURL(url => url.pathname + url.search === CLAUDE_PATH);
    expect(await trace.getByText("AgentsView fixture").isVisible()).toBe(true);
    expect(await trace.evaluate(() => [window.opener, document.referrer])).toEqual([null, ""]);
  } finally { await trace.close(); }
  await page.screenshot({ path: join(evidence, "pipeline-runs-wide.png"), fullPage: true });

  await openTask();
  expect(await page.getByTestId(`task-run-trace-${RUN.claude}`).getAttribute("href")).toBe(traceHref(CLAUDE_PATH));
  expect(await page.getByTestId(`task-run-trace-${RUN.codex}`).getAttribute("href")).toBe(traceHref(CODEX_PATH));
  expect(await page.getByTestId(`task-run-trace-${RUN.fake}`).count()).toBe(0);
  await page.screenshot({ path: join(evidence, "task-runs-wide.png") });
  await closeTask();

  await openReaderRuns();
  const history = page.getByTestId("task-runs");
  const claudeRow = history.getByTestId(`task-run-${RUN.claude}`);
  expect(await claudeRow.innerText()).toContain("worker");
  expect(await claudeRow.innerText()).toContain("Completed");
  expect(await history.getByTestId(`task-run-${RUN.codex}`).innerText()).toContain("verifier");
  expect(await history.getByTestId(`task-run-trace-${RUN.claude}`).getAttribute("href")).toBe(traceHref(CLAUDE_PATH));
  expect(await history.getByTestId(`task-run-trace-${RUN.codex}`).getAttribute("href")).toBe(traceHref(CODEX_PATH));
  expect(await history.getByTestId(`task-run-${RUN.fake}`).count()).toBe(1);
  expect(await history.getByTestId(`task-run-trace-${RUN.fake}`).count()).toBe(0);
  await page.screenshot({ path: join(evidence, "packet-runs-wide.png") });
  await closeReader();
  expect(externalRequests).toEqual([]);
}, 45000);

test("an unavailable AgentsView keeps each trace link in place and states the reason", async () => {
  const { page } = smoke;
  smoke.agentsview.answering = false;
  try {
    await hrefIs("header-traces", null);
    const header = await linkState(page.getByTestId("header-traces"));
    expect(header).toMatchObject({ disabled: "true", tabindex: "0" });
    expect(header.title).toStartWith("AgentsView is unavailable. ");
    await page.getByTestId("header-traces").focus();
    expect(await page.evaluate(() => document.activeElement?.getAttribute("data-testid"))).toBe("header-traces");
    // A click on an unavailable link opens no tab.
    let opened = 0;
    const count = () => { opened++; };
    smoke.context.on("page", count);
    await page.getByTestId("header-traces").click();
    await Bun.sleep(300);
    smoke.context.off("page", count);
    expect(opened).toBe(0);
    // The existing notice names the condition. A status without the resident
    // lists the condition among the known issues.
    const notice = page.getByTestId("system-notices");
    await page.waitForFunction(() => /AgentsView|Review issues/.test(document.querySelector('[data-testid="system-notices"]')?.textContent ?? ""));
    if (!(await notice.innerText()).includes("AgentsView")) {
      await notice.getByRole("button", { name: "Review issues" }).click();
      const dialog = page.locator("dialog.system-details[open]");
      await dialog.locator(".system-condition", { hasText: "AgentsView" }).waitFor();
      await page.screenshot({ path: join(evidence, "agentsview-condition.png") });
      await dialog.getByRole("button", { name: "Close", exact: true }).click();
    }

    await page.getByTestId("tab-pipeline").click();
    const link = page.locator(`[data-testid="run-row"][data-key="${RUN.claude}"]`).getByTestId("run-trace-link");
    expect(await linkState(link)).toMatchObject({ href: null, disabled: "true", tabindex: "0", title: header.title });
    await openTask();
    expect(await linkState(page.getByTestId(`task-run-trace-${RUN.claude}`))).toMatchObject({ href: null, disabled: "true", title: header.title });
    await closeTask();
    await openReaderRuns();
    expect(await linkState(page.getByTestId(`task-run-trace-${RUN.codex}`))).toMatchObject({ href: null, disabled: "true", title: header.title });
    await page.screenshot({ path: join(evidence, "packet-runs-unavailable.png") });
    await closeReader();
    // The wiki does not depend on AgentsView.
    expect(await page.getByTestId("header-wiki").getAttribute("href")).toBe(wikiHref());
    await page.screenshot({ path: join(evidence, "header-unavailable.png") });
  } finally {
    smoke.agentsview.answering = true;
  }
  await hrefIs("header-traces", tracesHref());
}, 45000);

test("a refused AgentsView token closes each trace link and states the reason", async () => {
  const { page } = smoke;
  const refused = `AgentsView is unavailable. AgentsView at ${smoke.agentsview.url} refuses the AgentsView token of this deployment.`;
  smoke.agentsview.token = "0".repeat(64);
  try {
    await page.getByTestId("tab-pipeline").click();
    await hrefIs("header-traces", null);
    expect(await linkState(page.getByTestId("header-traces"))).toMatchObject({ disabled: "true", title: refused });
    const link = page.locator(`[data-testid="run-row"][data-key="${RUN.claude}"]`).getByTestId("run-trace-link");
    expect(await linkState(link)).toMatchObject({ href: null, disabled: "true", title: refused });
    await page.screenshot({ path: join(evidence, "header-refused-token.png") });
  } finally {
    smoke.agentsview.token = smoke.agentsviewToken;
  }
  await hrefIs("header-traces", tracesHref());
}, 45000);

test("Configs omits the AgentsView access section", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-system").click();
  expect(await page.getByTestId("configs-traces").count()).toBe(0);
  expect(await page.getByTestId("agentsview-token-copy").count()).toBe(0);
  expect(await page.getByText("AgentsView asks for its token", { exact: false }).count()).toBe(0);
  await page.screenshot({ path: join(evidence, "configs-traces-absent.png") });
});

test("the viewer links fit a narrow window", async () => {
  const { page } = smoke;
  await page.setViewportSize(NARROW);
  try {
    await page.getByTestId("tab-board").click();
    await noHorizontalScroll();
    for (const testid of ["header-wiki", "header-traces"]) {
      const box = await page.getByTestId(testid).boundingBox();
      expect(box).not.toBeNull();
      expect(box!.x).toBeGreaterThanOrEqual(0);
      expect(box!.x + box!.width).toBeLessThanOrEqual(NARROW.width);
    }
    await page.screenshot({ path: join(evidence, "header-narrow.png") });

    await page.getByTestId("project-button").click();
    await page.getByTestId("project-listbox").waitFor({ state: "visible" });
    const panel = await page.getByTestId("project-listbox").boundingBox();
    for (const id of ["viewer-alpha", "viewer-beta"]) {
      const box = await page.getByTestId(`project-wiki-${id}`).boundingBox();
      expect(box!.x + box!.width).toBeLessThanOrEqual(panel!.x + panel!.width);
      expect(await page.getByTestId(`project-${id}`).evaluate((node) => node.getBoundingClientRect().width > 80)).toBe(true);
    }
    await noHorizontalScroll();
    await page.screenshot({ path: join(evidence, "project-menu-narrow.png") });
    await page.keyboard.press("Escape");

    await page.getByTestId("tab-pipeline").click();
    const runTrace = page.locator(`[data-testid="run-row"][data-key="${RUN.claude}"]`).getByTestId("run-trace-link");
    await runTrace.waitFor();
    await noHorizontalScroll();
    // The run table scrolls sideways inside its card. The trace link is in
    // the last column.
    await runTrace.scrollIntoViewIfNeeded();
    const runBox = await runTrace.boundingBox();
    expect(runBox!.x + runBox!.width).toBeLessThanOrEqual(NARROW.width);
    await page.screenshot({ path: join(evidence, "pipeline-runs-narrow.png"), fullPage: true });

    await openTask();
    const trace = await page.getByTestId(`task-run-trace-${RUN.claude}`).boundingBox();
    expect(trace!.x + trace!.width).toBeLessThanOrEqual(NARROW.width);
    await page.screenshot({ path: join(evidence, "task-runs-narrow.png") });
    await closeTask();

    await openReaderRuns();
    const reader = await page.getByTestId(`task-run-trace-${RUN.claude}`).boundingBox();
    expect(reader!.x + reader!.width).toBeLessThanOrEqual(NARROW.width);
    await page.screenshot({ path: join(evidence, "packet-runs-narrow.png") });
    await closeReader();
  } finally {
    await page.setViewportSize(WIDE);
  }
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 45000);
