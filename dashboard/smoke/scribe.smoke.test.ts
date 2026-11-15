import { afterAll, beforeAll, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke } from "./harness";
import { recordCurrentWiki, recordMainTrace, seedActiveWikiUpdate, seedFailedWikiUpdate } from "./wiki-fixture";

// The Scribe updates the Project wiki on a schedule and has no task. The
// Pipeline page shows the Scribe apart from the task path. The wiki section
// shows the wiki state of the selected Project. The role detail of the Scribe
// holds the same parts as the role detail of each task role.

let smoke: Smoke;
let spine: string;
let git: (...args: string[]) => string;
const run: { current: string | null; failed: string; active: string } = { current: null, failed: "", active: "" };
const evidence = evidencePath("scribe-evidence");
const CAUSE = "The wiki commit conflicts with the culler branch. Culler discarded the wiki commit.";
// The native session identifier of the main trace of the first wiki update run.
const TRACE_REF = "33333333-3333-4333-8333-333333333333";
// The parts of each role detail.
const PARTS = ["top", "blurb", "flow-popover-rows"];

beforeAll(async () => {
  smoke = await bootSmoke();
  smoke.page.setDefaultTimeout(10000);
  mkdirSync(evidence, { recursive: true });
  spine = join(smoke.home, "spine.db");
  const root = join(smoke.scratch, "scribe-alpha");
  mkdirSync(join(root, "openwiki"), { recursive: true });
  writeFileSync(join(root, "VISION.md"), "The scribe-alpha Project keeps its wiki current.\n");
  writeFileSync(join(root, "openwiki", "index.md"), "# Architecture\n\nThe accepted source defines this behavior.\n");
  git = (...args) => execFileSync("git", ["-c", "user.name=Scribe Test", "-c", "user.email=scribe@example.invalid", "-c", "commit.gpgsign=false", ...args],
    { cwd: root, env: process.env, stdio: ["ignore", "pipe", "pipe"] }).toString().trim();
  git("init", "-b", "main");
  git("add", ".");
  git("commit", "-m", "Create the accepted Project documents");
  await seedProject(smoke, "scribe-alpha", { workspace: root });
  run.current = recordCurrentWiki(spine, "scribe-alpha", git("rev-parse", "HEAD"), new Date());
  // The first wiki update run has a main trace.
  recordMainTrace(spine, run.current!, TRACE_REF);
  // A prepared AgentsView unit makes the status command ask AgentsView.
  mkdirSync(join(smoke.home, "supervision", "LaunchAgents"), { recursive: true });
  writeFileSync(join(smoke.home, "supervision", "LaunchAgents", "agentsview.plist"), "<plist></plist>\n");
  // The second Project has its own wiki update.
  await seedProject(smoke, "scribe-beta");
  recordCurrentWiki(spine, "scribe-beta", "b".repeat(40), new Date());
  await gotoReady(smoke);
  await selectProject(smoke, "scribe-alpha");
}, 60000);

afterAll(async () => { await teardown(smoke); });

/** The wiki state row that `mc` reads for one Project. */
async function wikiState(project: string) {
  return ((await mc(["wiki-state", "list"])).wiki_states as Record<string, any>[]).find((row) => row.project === project);
}

/** Reads the page again and opens the role detail of the Scribe for one Project. */
async function scribeDetail(project: string) {
  const { page } = smoke;
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await selectProject(smoke, project);
  await page.getByTestId("tab-pipeline").click();
  await page.getByTestId("topology-node-scribe").click();
  const detail = page.getByTestId("topology-side");
  await detail.getByTestId("topology-change-profile").waitFor();
  return detail;
}

/** The parts of the open role detail. */
function detailParts() {
  return smoke.page.evaluate(() => [...document.querySelector('[data-testid="topology-side"]')!.children].map((child) => child.className));
}

/** The Project wiki section holds the Scribe above the task path. It stands beside the run banner, or below it in a narrow viewport. */
function scribePlace() {
  return smoke.page.evaluate(() => {
    const graph = document.querySelector('[data-testid="topology-graph"]') as HTMLElement;
    const rect = (testid: string) => document.querySelector(`[data-testid="${testid}"]`)!.getBoundingClientRect();
    const wiki = rect("pipeline-wiki");
    const banner = rect("pipeline-current-run");
    const scribe = rect("topology-node-scribe");
    return {
      layout: graph.dataset.layout,
      inside: scribe.left >= wiki.left && scribe.top >= wiki.top && scribe.right <= wiki.right && scribe.bottom <= wiki.bottom,
      apart: wiki.bottom <= graph.getBoundingClientRect().top,
      banner: wiki.left >= banner.right && Math.abs(wiki.top - banner.top) < 1 ? "beside" : wiki.top >= banner.bottom ? "below" : "overlap",
    };
  });
}

/** The run row of one run in the recent runs. */
const runRow = (id: string | null) => smoke.page.locator(`[data-testid="run-row"][data-key="${id}"]`);

test("the Scribe stands apart from the task path, and its role detail fits a narrow viewport", async () => {
  const { page } = smoke;
  expect(run.current).toMatch(/^[0-9a-f]{16}$/);
  await page.setViewportSize({ width: 390, height: 844 });
  try {
    await page.getByTestId("tab-pipeline").click();
    await page.getByTestId("topology-node-scribe").click();
    const detail = page.getByTestId("topology-side");
    await detail.getByTestId("topology-change-profile").waitFor();
    expect(await detail.innerText()).toContain("The Scribe updates the Project wiki on a schedule. It has no task.");
    // The role detail of the Scribe holds the same parts as each other role detail. The wiki section states the wiki state.
    expect(await detailParts()).toEqual(PARTS);
    await page.getByTestId("wiki-state").filter({ hasText: "The Project wiki is current." }).waitFor();
    expect(await page.getByTestId("wiki-state").innerText()).toBe("The Project wiki is current.");
    expect(await scribePlace()).toEqual({ layout: "narrow", inside: true, apart: true, banner: "below" });
    expect(await page.getByTestId("topology-edge-verification").count()).toBe(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    const shownModel = () => detail.getByTestId("topology-change-profile").locator(".flow-popover-value").textContent();
    const previousModel = await shownModel();
    await page.getByTestId("topology-change-profile").click();
    await page.getByTestId("profile-assignment").waitFor();
    expect(await page.getByTestId("profile-assignment").getAttribute("aria-label")).toBe("Agent profile for Scribe");
    const choice = page.getByTestId("profile-assignment").locator("input:not(:checked)").first();
    const binding = (await choice.getAttribute("value"))!;
    const model = await choice.evaluate((input) => input.closest("label")!.querySelector("b")!.textContent!);
    expect(model).not.toBe(previousModel);
    const assignment = page.waitForResponse((response) => response.url().endsWith("/api/routing") && response.request().method() === "POST");
    await choice.click();
    expect((await assignment).ok()).toBe(true);
    expect(((await mc(["routing", "show"])).routes as { role: string; binding: string }[]).find((route) => route.role === "scribe")?.binding).toBe(binding);
    await page.screenshot({ path: join(evidence, "scribe-narrow.png"), fullPage: true });
    // The role dialog shows the new agent profile when the agent profile dialog closes. The check
    // runs after the close event and before the next poll.
    await page.evaluate(() => {
      const panel = document.querySelector('[data-testid="agent-profiles-panel"]')!;
      (window as any).profilesClosed = new Promise((resolve) => panel.addEventListener("close", () => setTimeout(resolve, 0), { once: true }));
    });
    await page.getByTestId("profile-close").click();
    await page.evaluate(() => (window as any).profilesClosed);
    expect(await shownModel()).toBe(model);
    expect(await detail.getByTestId("topology-change-profile").evaluate((node) => node === document.activeElement)).toBe(true);
  } finally {
    if (await page.getByTestId("agent-profiles-panel").isVisible()) await page.getByTestId("profile-close").click();
    await page.setViewportSize({ width: 1440, height: 900 });
  }
  // Escape closes the role dialog.
  await page.keyboard.press("Escape");
  await page.getByTestId("topology-side").waitFor({ state: "detached" });
  expect(await page.getByTestId("topology-side").count()).toBe(0);
  // The Packager follows the Verifier, and no edge names the Scribe.
  await page.waitForFunction(() => (document.querySelector('[data-testid="topology-graph"]') as HTMLElement | null)?.dataset.layout === "wide");
  expect(await page.getByTestId("topology-edge-verification").count()).toBe(0);
  expect(await page.getByTestId("topology-edge-wiki-complete").count()).toBe(0);
  expect(await page.getByTestId("topology-edge-verified").textContent()).toBe("Verified 0");
  expect(await scribePlace()).toEqual({ layout: "wide", inside: true, apart: true, banner: "beside" });
  expect(await page.getByTestId("topology-node-scribe").textContent()).toContain("Idle");
  // The wiki update run is a Scribe run with no task. Its row states the result in plain words.
  const current = runRow(run.current);
  expect(await current.locator("td").nth(1).innerText()).toBe("scribe");
  expect(await current.locator("td").nth(2).innerText()).toBe("—");
  expect(await current.locator("td").nth(3).innerText()).toBe("The Project wiki needed no change.");
  expect(await current.getByTestId("run-log-link").count()).toBe(1);
  // The run has a main trace, so its row links to the AgentsView session of the run.
  await current.locator(`[data-testid="run-trace-link"][href="/agentsview/open?path=${encodeURIComponent(`/sessions/${TRACE_REF}?msg=last`)}"]`).waitFor();
  expect(await current.getByTestId("run-trace-link").count()).toBe(1);
  await page.screenshot({ path: join(evidence, "scribe-wide.png"), fullPage: true });
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 30000);

test("a failed wiki update shows its cause in the recent runs", async () => {
  const { page } = smoke;
  // The culler branch moves, and the wiki update at the new commit fails.
  writeFileSync(join(smoke.scratch, "scribe-alpha", "NOTES.md"), "The accepted source changed.\n");
  git("add", ".");
  git("commit", "-m", "Change the accepted source");
  run.failed = seedFailedWikiUpdate(spine, "scribe-alpha", git("rev-parse", "HEAD"), CAUSE);
  expect(await wikiState("scribe-alpha")).toMatchObject({
    state: "behind", next_start_text: null, active: null,
    last: { run_id: run.failed, result: "failed", result_text: "The wiki update failed.", cause_text: CAUSE },
  });
  await scribeDetail("scribe-alpha");
  expect(await detailParts()).toEqual(PARTS);
  expect(await page.getByTestId("wiki-state").innerText()).toBe("The Project wiki is behind the accepted commit.");
  const failed = runRow(run.failed);
  expect(await failed.locator("td").nth(1).innerText()).toBe("scribe");
  expect(await failed.locator("td").nth(2).innerText()).toBe("—");
  expect(await failed.locator("td").nth(3).innerText()).toContain("The wiki update failed.");
  expect(await failed.getByTestId("run-wiki-cause").innerText()).toBe(CAUSE);
  expect(await failed.getByTestId("run-log-link").count()).toBe(1);
  // The failed wiki update run has no main trace, so its row has no trace link.
  expect(await failed.getByTestId("run-trace-link").count()).toBe(0);
  expect(await runRow(run.current).getByTestId("run-wiki-cause").count()).toBe(0);
  await page.screenshot({ path: join(evidence, "scribe-wiki-failed.png"), fullPage: true });
}, 30000);

test("each Project shows its own wiki state", async () => {
  const { page } = smoke;
  await scribeDetail("scribe-beta");
  expect(await page.getByTestId("wiki-state").innerText()).toBe("The Project wiki is current.");
  expect(await runRow(run.failed).count()).toBe(0);
  await scribeDetail("scribe-alpha");
  expect(await page.getByTestId("wiki-state").innerText()).toBe("The Project wiki is behind the accepted commit.");
  expect(await runRow(run.failed).count()).toBe(1);
}, 30000);

test("a failed wiki state read shows in the wiki section, and each other part of the page stays", async () => {
  const { page } = smoke;
  const read = "**/api/wiki-states";
  await page.route(read, (route) => route.fulfill({ status: 503, json: { error: { code: "environment", message: "The spine is unavailable." } } }));
  try {
    const detail = await scribeDetail("scribe-alpha");
    expect(await page.getByTestId("wiki-state").innerText()).toBe("The dashboard cannot read the wiki state.");
    expect(await detailParts()).toEqual(PARTS);
    expect(await detail.innerText()).toContain("The Scribe updates the Project wiki on a schedule. It has no task.");
    expect(await detail.getByTestId("topology-custom-instructions").count()).toBe(1);
    expect(await page.getByTestId("topology-node-worker").isVisible()).toBe(true);
    expect(await runRow(run.failed).getByTestId("run-wiki-cause").innerText()).toBe(CAUSE);
    expect(await page.getByTestId("error").isVisible()).toBe(false);
    await page.screenshot({ path: join(evidence, "scribe-wiki-unavailable.png"), fullPage: true });
  } finally {
    await page.unroute(read);
  }
}, 30000);

test("an active wiki update shows on the Scribe node and leaves the lease badge free", async () => {
  const { page } = smoke;
  run.active = seedActiveWikiUpdate(spine, "scribe-alpha", git("rev-parse", "HEAD"));
  expect(await wikiState("scribe-alpha")).toMatchObject({
    state: "update-active", next_start_at: null, next_start_text: null,
    active: { run_id: run.active, run_ended_at: null, step: "run", repair_text: null },
    last: { run_id: run.failed },
  });
  await scribeDetail("scribe-alpha");
  expect(await page.getByTestId("wiki-state").innerText()).toBe("A wiki update is active.");
  expect(await detailParts()).toEqual(PARTS);
  expect(await page.getByTestId("topology-node-scribe").textContent()).toContain("Running now");
  expect(await page.locator(".node.running").count()).toBe(1);
  // The wiki update run holds no Project lease.
  expect(await page.getByTestId("lease-badge").innerText()).toContain("No agents are running");
  expect(((await mc(["lease", "list"])).leases as Record<string, any>[]).filter((lease) => lease.project === "scribe-alpha" && lease.run_id !== null)).toEqual([]);
  // The row of the active Scribe run has no task and states no result of the wiki update.
  const active = runRow(run.active);
  expect(await active.locator("td").nth(1).innerText()).toBe("scribe");
  expect(await active.locator("td").nth(2).innerText()).toBe("—");
  expect(await active.locator("td").nth(3).innerText()).toBe("Running");
  expect(await active.getByTestId("run-wiki-cause").count()).toBe(0);
  await page.screenshot({ path: join(evidence, "scribe-wiki-active.png"), fullPage: true });
}, 30000);

test("an archived Project shows no wiki state", async () => {
  const { page } = smoke;
  await mc(["project", "archive", "scribe-beta"]);
  expect(await wikiState("scribe-beta")).toBeUndefined();
  const detail = await scribeDetail("scribe-beta");
  expect(await page.getByTestId("wiki-state").innerText()).toBe("The Scribe updates the Project wiki on a schedule.");
  expect(await detailParts()).toEqual(PARTS);
  expect(await detail.innerText()).toContain("The Scribe updates the Project wiki on a schedule. It has no task.");
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 30000);
