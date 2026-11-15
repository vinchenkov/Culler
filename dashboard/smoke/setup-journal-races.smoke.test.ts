import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { bootSmoke, gotoReady, openSetupHistory, seedProject, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
const garden = "journal-race-garden";
const library = "journal-race-library";
const digest = `sha256:${"a".repeat(64)}`;
const dockerfile = "FROM retained-base\nCOPY .culler/tools.md /tmp/tools.md\nRUN install-tools\n";
const buildFile = "# Image input\n\n**Keep these bytes.**\n<script>literal input</script>\n";

function gate() {
  let release!: () => void;
  const promise = new Promise<void>(resolve => { release = resolve; });
  return { promise, release };
}

function fixture(phase: "active" | "approval" | "build" = "active", project = garden): any {
  const prefix = project === garden ? "race" : "library";
  const configuration = { base_digest: digest, dockerfile: { path: ".culler/Dockerfile", content: dockerfile },
    build_files: [{ path: ".culler/tools.md", content: buildFile }], build_args: {},
    setup: { command: "install-dependencies", directory: "." } };
  const revision = { id: `${prefix}-proposal`, editor: `setup:${prefix}-agent`, configuration,
    explanation: "Install the Project tools in the image. The Project checks need these tools." };
  const operation = { id: `${prefix}-agent`, project_id: project, revision_id: `${prefix}-base`, kind: "setup", attempt: 1,
    status: phase === "active" ? "running" : "succeeded", created_at: "2026-10-12 12:00:00",
    ended_at: phase === "active" ? "" : "2026-10-12 12:01:00",
    metadata: phase === "active" ? {} : { report_outcome: "proposal", proposed_revision_id: revision.id } };
  const build = { id: `${prefix}-build`, project_id: project, revision_id: revision.id, kind: "build", attempt: 1,
    status: "queued", created_at: "2026-10-12 12:01:00", metadata: { previous_operation_id: operation.id } };
  return { environment: { project_id: project, project_title: project === garden ? "Race Garden" : "Race Library", project_status: "paused", setup_status: "configured",
    setup_id: `${prefix}-setup`, vision_revision: `${prefix}-vision`, selected_revision_id: "", draft_revision_id: revision.id,
    phase: phase === "active" ? "agent" : phase, readiness: "setup_required",
    active_operation_id: phase === "active" ? operation.id : phase === "build" ? build.id : "", cause: "" },
    draft: revision, selected: null, revisions: [revision, { id: `${prefix}-base`, configuration: { base_digest: digest }, explanation: "" }],
    operations: phase === "build" ? [build, operation] : [operation],
    results: [], approvals: phase === "build" ? [{ revision_id: revision.id, created_at: "2026-10-12 12:01:00" }] : [],
    vision: { revision: `${prefix}-vision`, content: "# Project Vision\n\nRecord Project setup progress." } };
}

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, garden, { title: "Race Garden" });
  await seedProject(smoke, library, { title: "Race Library", environmentReady: true });
  await gotoReady(smoke);
}, 60000);
afterEach(async () => {
  await smoke.page.unrouteAll({ behavior: "wait" });
});
afterAll(async () => { if (smoke) await teardown(smoke); });

async function refresh() {
  await smoke.page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
}

function environmentRead(project: string) {
  return smoke.page.waitForResponse(response => response.url().endsWith("/api/environment/get")
    && response.request().postDataJSON()?.project_id === project);
}

async function nextFrame() {
  await smoke.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}

async function showGarden(read: () => any) {
  let served = 0;
  await smoke.page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== garden) return route.continue();
    served++;
    await route.fulfill({ json: read() });
  });
  await smoke.page.getByTestId("tab-board").click();
  await selectProject(smoke, garden);
  await smoke.page.reload();
  try { await smoke.page.getByTestId("setup-journal").waitFor({ timeout: 10000 }); }
  catch (error) {
    throw new Error(`${error}\nFixture reads: ${served}\nSelected: ${await smoke.page.getByTestId("project-button").innerText()}\nBoard: ${await smoke.page.locator("#board-pane").innerText()}\nBrowser: ${smoke.browserLog.join("\n")}`);
  }
}

test("a late setup response cannot replace the newly selected Project", async () => {
  const { page } = smoke;
  await showGarden(() => fixture());
  const held = gate(), started = gate();
  await page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== garden) return route.fallback();
    started.release();
    await held.promise;
    const stale = fixture("approval");
    stale.environment.project_title = "Stale Garden response";
    await route.fulfill({ json: stale });
  });
  try {
    await refresh();
    await started.promise;
    const libraryRead = environmentRead(library);
    await selectProject(smoke, library);
    await libraryRead;
    await nextFrame();
    expect(await page.getByTestId("setup-journal").count()).toBe(0);
    const response = page.waitForResponse(response => response.url().endsWith("/api/environment/get")
      && response.request().postDataJSON()?.project_id === garden);
    held.release();
    await response;
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    expect(await page.getByTestId("project-button").innerText()).toContain("Race Library");
    expect(await page.getByTestId("setup-journal").count()).toBe(0);
    expect(await page.locator("#board-heading").isVisible()).toBe(true);
    expect(await page.locator("#board-setup").innerText()).not.toContain("Stale Garden response");
  } finally { held.release(); }
}, 30000);

test("an environment read failure leaves a completed Project's Board available", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await selectProject(smoke, library);
  const libraryRead = environmentRead(library);
  await page.reload();
  await libraryRead;
  await page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== library) return route.continue();
    await route.fulfill({ status: 503, json: { error: { code: "environment", message: "The fixture environment read failed." } } });
  });
  await refresh();
  await page.getByText("Setup history is unavailable. The fixture environment read failed.", { exact: true }).waitFor();
  expect(await page.locator("#board-heading").isVisible()).toBe(true);
  expect(await page.locator("#board-columns").isVisible()).toBe(true);
  expect(await page.getByTestId("setup-journal").count()).toBe(0);
  expect(await page.locator(".board-command-host:visible").count()).toBe(1);
  expect(await page.getByText("Setup status unavailable", { exact: true }).count()).toBe(0);
}, 30000);

test("two approval clicks submit one request while that request remains pending", async () => {
  const { page } = smoke;
  let view = fixture("approval");
  await showGarden(() => view);
  const held = gate(), started = gate();
  const requests: unknown[] = [];
  await page.route("**/api/environment/approve", async route => {
    requests.push(route.request().postDataJSON());
    started.release();
    await held.promise;
    view = fixture("build");
    await route.fulfill({ json: view });
  });
  try {
    await page.getByTestId("setup-approve").evaluate(button => {
      (button as HTMLButtonElement).click();
      (button as HTMLButtonElement).click();
    });
    await started.promise;
    expect(await page.getByTestId("setup-approve").isDisabled()).toBe(true);
    expect(requests).toEqual([{ project_id: garden, revision_id: "race-proposal" }]);
    const response = page.waitForResponse("**/api/environment/approve");
    held.release();
    await response;
    await page.getByTestId("setup-approve").waitFor({ state: "detached" });
    expect(requests).toHaveLength(1);
    expect(await page.getByTestId("setup-journal").innerText()).toContain("Image changes approved");
  } finally { held.release(); }
}, 30000);

test("a delayed approval response preserves a newer read for the same Project", async () => {
  const { page } = smoke;
  let view = fixture("approval");
  await showGarden(() => view);
  const held = gate(), started = gate();
  const earlier = fixture("build");
  await page.route("**/api/environment/approve", async route => {
    expect(route.request().postDataJSON()).toEqual({ project_id: garden, revision_id: "race-proposal" });
    started.release();
    await held.promise;
    await route.fulfill({ json: earlier });
  });
  try {
    await page.getByTestId("setup-approve").click();
    await started.promise;
    view = fixture("build");
    view.operations[0].status = "running";
    await refresh();
    const build = page.locator('[data-operation="race-build"]');
    await build.getByText("Active", { exact: true }).waitFor();
    const response = page.waitForResponse("**/api/environment/approve");
    const pause = page.waitForResponse("**/api/pause");
    held.release();
    await response;
    await pause;
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    expect(await build.locator(".journal-status").innerText()).toBe("Active");
    expect(await build.locator(".journal-pulse").count()).toBe(1);
    expect(await page.getByTestId("setup-approve").count()).toBe(0);
  } finally { held.release(); }
}, 30000);

test("another Project can approve its proposal while the first Project's approval waits", async () => {
  const { page } = smoke;
  let gardenView = fixture("approval");
  let libraryView = fixture("approval", library);
  await showGarden(() => gardenView);
  await page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== library) return route.fallback();
    await route.fulfill({ json: libraryView });
  });
  const held = gate(), started = gate();
  const requests: unknown[] = [];
  await page.route("**/api/environment/approve", async route => {
    const body = route.request().postDataJSON();
    requests.push(body);
    if (body.project_id === garden) {
      started.release();
      await held.promise;
      gardenView = fixture("build");
      await route.fulfill({ json: gardenView });
      return;
    }
    libraryView = fixture("build", library);
    await route.fulfill({ json: libraryView });
  });
  try {
    await page.getByTestId("setup-approve").click();
    await started.promise;
    await selectProject(smoke, library);
    await page.locator('[data-testid="setup-proposal"][data-revision="library-proposal"]').waitFor();
    expect(await page.getByTestId("setup-approve").isEnabled()).toBe(true);
    await page.getByTestId("setup-approve").click();
    await page.locator('[data-operation="library-build"]').waitFor();
    expect(requests).toEqual([
      { project_id: garden, revision_id: "race-proposal" },
      { project_id: library, revision_id: "library-proposal" },
    ]);
    const response = page.waitForResponse(response => response.url().endsWith("/api/environment/approve")
      && response.request().postDataJSON()?.project_id === garden);
    held.release();
    await response;
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    expect(await page.getByTestId("setup-panel").getAttribute("data-project")).toBe(library);
    expect(await page.locator('[data-operation="library-build"]').count()).toBe(1);
    expect(await page.locator('[data-operation="race-build"]').count()).toBe(0);
  } finally { held.release(); }
}, 30000);

test("completed history without acceptance evidence does not claim setup is active", async () => {
  const { page } = smoke;
  const view = fixture("approval", library);
  Object.assign(view.environment, { setup_status: "ready", phase: "complete", project_status: "active",
    selected_revision_id: view.draft.id, active_operation_id: "" });
  view.selected = view.draft;
  view.operations = [];
  await page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== library) return route.continue();
    await route.fulfill({ json: view });
  });
  await page.getByTestId("tab-board").click();
  await selectProject(smoke, library);
  await page.reload();
  await openSetupHistory(smoke, library);
  await page.getByTestId("setup-evidence-unavailable").waitFor();
  expect(await page.getByTestId("setup-phase").innerText()).toBe("Evidence unavailable");
  expect(await page.locator("#lease-badge .text").innerText()).toBe("Setup evidence unavailable");
  expect(await page.getByTestId("setup-journal").locator(".journal-pulse").count()).toBe(0);
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 30000);

test("the file comparison preserves exact changed Dockerfile and build file contents", async () => {
  const { page } = smoke;
  const before = { base_digest: digest,
    dockerfile: { path: ".culler/Dockerfile", content: "FROM retained-base\nRUN old-tools\n" },
    build_files: [{ path: ".culler/tools.md", content: "# Old image input\n\n  Keep indentation.\n" }] };
  // An earlier acceptance supplies the accepted version of each file.
  const view = fixture("approval");
  view.revisions[1].configuration = before;
  view.operations.push({ id: "race-accept", project_id: garden, revision_id: "race-base", kind: "accept", attempt: 1,
    status: "succeeded", created_at: "2026-10-12 11:59:00", ended_at: "2026-10-12 11:59:30", metadata: {} });
  await showGarden(() => view);
  const rows = (viewer: ReturnType<typeof page.locator>) => viewer.evaluate(node => {
    const root = node.querySelector("diffs-container")!.shadowRoot!;
    // An empty row holds one line break.
    return { lines: [...root.querySelectorAll("[data-line]")].map(row => [row.getAttribute("data-line-type"), row.textContent === "\n" ? "" : row.textContent]),
      markup: root.querySelectorAll("[data-line] h1, [data-line] strong, [data-line] script").length };
  });
  const ready = (selector: string) => page.waitForFunction(selector => document.querySelector(selector)?.getAttribute("data-state") === "ready"
    && document.querySelector(selector)?.querySelector("diffs-container")?.shadowRoot?.querySelector("[data-line]") != null, selector);
  const complete = async (viewer: ReturnType<typeof page.locator>) => {
    await viewer.getByTestId("setup-file-complete").click();
    await viewer.evaluate(node => new Promise(resolve => {
      const wait = () => node.querySelector("diffs-container")?.shadowRoot?.querySelector("pre[data-file] [data-line]") ? resolve(null) : requestAnimationFrame(wait);
      wait();
    }));
    return (await rows(viewer)).lines.map(([, text]) => text).join("\n") + "\n";
  };

  const dockerfileView = page.getByTestId("setup-dockerfile");
  expect(await dockerfileView.locator(".file-view-change").innerText()).toBe("Changed");
  await ready('[data-testid="setup-dockerfile"]');
  const lines = (await rows(dockerfileView)).lines;
  const ofType = (type: string) => lines.filter(([kind]) => kind === type).map(([, text]) => text);
  expect(ofType("context")).toEqual(["FROM retained-base"]);
  expect(ofType("change-deletion")).toEqual(["RUN old-tools"]);
  expect(ofType("change-addition")).toEqual(["COPY .culler/tools.md /tmp/tools.md", "RUN install-tools"]);
  expect(await complete(dockerfileView)).toBe(dockerfile);

  await page.locator("summary").filter({ hasText: "Changed: .culler/tools.md" }).click();
  const buildSelector = '[data-journal-key="race-proposal:file:.culler/tools.md"] .file-view';
  await ready(buildSelector);
  const buildView = page.locator(buildSelector);
  const build = await rows(buildView);
  // The empty second line is common to both versions.
  expect(build.lines.filter(([type]) => type === "context").map(([, text]) => text)).toEqual([""]);
  expect(build.lines.filter(([type]) => type === "change-deletion").map(([, text]) => text)).toEqual(["# Old image input", "  Keep indentation."]);
  expect(build.lines.filter(([type]) => type === "change-addition").map(([, text]) => text))
    .toEqual(["# Image input", "**Keep these bytes.**", "<script>literal input</script>"]);
  expect(build.markup).toBe(0);
  expect(await complete(buildView)).toBe(buildFile);
  expect((await rows(buildView)).markup).toBe(0);
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 30000);

test("a late health response cannot replace the next Project's setup status", async () => {
  const { page } = smoke;
  const healthy = { setup: { request: { available: true, status: "succeeded", observed_at: "2026-10-13T00:00:00Z" }, submissions: [] } };
  await page.route("**/api/health", route => route.fulfill({ json: healthy }));
  await showGarden(() => fixture());
  const held = gate(), started = gate();
  let first = true;
  await page.route("**/api/health", async route => {
    if (!first) return route.fallback();
    first = false;
    started.release();
    await held.promise;
    await route.fulfill({ json: { setup: { request: { available: true, status: "failed", observed_at: "2026-10-13T00:00:01Z", message: "The prior request failed." },
      submissions: [{ available: true, status: "failed", project_id: garden, operation_id: "race-agent", message: "The Garden result could not be saved." }] } } });
  });
  try {
    await refresh();
    await started.promise;
    await selectProject(smoke, library);
    await openSetupHistory(smoke, library);
    await page.getByTestId("setup-panel").waitFor();
    const response = page.waitForResponse(response => response.url().endsWith("/api/health") && response.status() === 200);
    held.release();
    await response;
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    expect(await page.getByTestId("setup-panel").innerText()).not.toContain("The prior request failed.");
    expect(await page.getByTestId("setup-panel").innerText()).not.toContain("The Garden result could not be saved.");
  } finally { held.release(); }
}, 30000);
