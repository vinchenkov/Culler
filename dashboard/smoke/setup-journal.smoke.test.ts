import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, openSetupHistory, seedProject, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
const project = "journal-garden";
const digest = `sha256:${"a".repeat(64)}`;
const dockerfile = `FROM culler-project-base:${"a".repeat(64)}\nCOPY .culler/tools.sh /tmp/tools.sh\nRUN sh /tmp/tools.sh\n`;
const buildFile = "#!/bin/sh\n# **Literal build contents**\nprintf '%s\\n' '<script>literal</script>'\n";
const summary = "The image supplies **database tools**. The Project needs them for its checks.";
const longExplanation = `${summary}\n\n## Check evidence\n\n- The workspace uses the declared tool versions.\n- The application check needs the database client.\n\n${"The earlier agent recorded diagnostic details for inspection. ".repeat(45)}\n\n[Unsafe](javascript:alert) <button>Literal</button>`;

function fixture(phase: "active" | "approval" | "failed" | "complete" = "active"): any {
  const configuration = { base_digest: digest, dockerfile: { path: ".culler/Dockerfile", content: dockerfile },
    build_files: [{ path: ".culler/tools.sh", content: buildFile }], build_args: { TOOL_VERSION: "16" },
    setup: { directory: ".", command: "npm ci" } };
  const revision = { id: "journal-revision", configuration, explanation: summary, editor: "setup:journal-first" };
  const first = { id: "journal-first", project_id: project, revision_id: "journal-base", kind: "setup", status: "succeeded", attempt: 1,
    created_at: "2026-10-10 12:00:00", ended_at: "2026-10-10 12:01:00",
    metadata: { report_outcome: "proposal", proposed_revision_id: revision.id }, trace: { path: "/sessions/claude:journal-first" } };
  const build = { id: "journal-build", project_id: project, revision_id: revision.id, kind: "build", status: "succeeded", attempt: 1,
    created_at: "2026-10-10 12:01:00", ended_at: "2026-10-10 12:02:00", metadata: { previous_operation_id: first.id } };
  const validation = { id: "journal-validation", project_id: project, revision_id: revision.id, kind: "validate",
    status: phase === "active" ? "running" : phase === "failed" ? "failed" : "succeeded", attempt: 1,
    created_at: "2026-10-10 12:02:00", ended_at: phase === "active" ? "" : "2026-10-10 12:03:00",
    error: phase === "failed" ? "The package service is unavailable. Retry setup when the service returns." : "",
    metadata: { previous_operation_id: build.id, ...(phase === "complete" ? { report_outcome: "complete" } : {}) } };
  const acceptance = { id: "journal-accept", project_id: project, revision_id: revision.id, kind: "accept", status: "succeeded", attempt: 1,
    created_at: "2026-10-10 12:03:00", ended_at: "2026-10-10 12:04:00",
    metadata: { initial_setup: true, result_id: "journal-result", source_commit: "b".repeat(40), previous_operation_id: validation.id } };
  const operations = phase === "approval" ? [first] : phase === "complete" ? [acceptance, validation, build, first] : [validation, build, first];
  return { environment: { project_id: project, project_title: "Garden", project_status: phase === "complete" ? "active" : "paused",
    setup_status: phase === "complete" ? "ready" : "configured", setup_id: "journal-setup", vision_revision: "journal-vision",
    selected_revision_id: phase === "complete" ? revision.id : "", draft_revision_id: revision.id,
    phase: phase === "active" ? "validate" : phase, readiness: phase === "complete" ? "ready" : "setup_required",
    active_operation_id: phase === "active" ? validation.id : "", cause: phase === "failed" ? validation.error : "" },
    draft: revision, selected: phase === "complete" ? revision : null,
    revisions: [revision, { id: "journal-base", configuration: { base_digest: digest }, explanation: "The default image supplies the initial tools." }],
    operations, approvals: phase === "approval" ? [] : [{ revision_id: revision.id, created_at: "2026-10-10 12:01:00" }],
    vision: { revision: "journal-vision", content: "# Garden\n\nBuild a useful **garden planner**.\n\n1. Record plants.\n2. Plan each season.", created_at: "2026-10-10 11:59:00" },
    results: [{ id: "proposal-report", operation_id: first.id, revision_id: revision.id, outcome: "proposal", summary, details: longExplanation },
      ...(phase === "complete" ? [{ id: "journal-result", operation_id: validation.id, revision_id: revision.id, outcome: "complete", image_digest: digest,
        summary: "Preparation is complete with platform limits.", details: "The database client works. The macOS tests remain **unverified**.", created_at: "2026-10-10 12:03:00" }] : [])] };
}

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, project, { title: "Garden" });
  await seedProject(smoke, "journal-library", { title: "Library", environmentReady: true });
  // A prepared AgentsView unit makes the status command ask AgentsView.
  mkdirSync(join(smoke.home, "supervision", "LaunchAgents"), { recursive: true });
  writeFileSync(join(smoke.home, "supervision", "LaunchAgents", "agentsview.plist"), "<plist></plist>\n");
  await gotoReady(smoke);
}, 60000);
afterEach(async () => {
  await smoke.page.unrouteAll({ behavior: "wait" });
  await smoke.page.evaluate(() => { document.documentElement.style.zoom = ""; });
  await smoke.page.setViewportSize({ width: 1280, height: 720 });
  await smoke.page.emulateMedia({ reducedMotion: "no-preference" });
});
afterAll(async () => { if (smoke) await teardown(smoke); });

async function installFixture(read: () => any) {
  await smoke.page.route("**/api/pause", async route => {
    const response = await route.fetch();
    const state = await response.json();
    const view = read();
    state.projects = state.projects.map((row: any) => row.id === project
      ? { ...row, setup_status: view instanceof Error ? "configured" : view.environment.setup_status } : row);
    await route.fulfill({ json: state });
  });
  await smoke.page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== project) return route.continue();
    const value = read();
    await route.fulfill(value instanceof Error
      ? { status: 503, json: { error: { message: value.message, code: "environment" } } }
      : { json: value });
  });
  await smoke.page.getByTestId("tab-board").click();
  await selectProject(smoke, project);
  await smoke.page.reload();
}

async function refresh() {
  await smoke.page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
}

test("the journal retains progress, Markdown, approval files, and completion history", async () => {
  const { page } = smoke;
  let view = fixture("active");
  await installFixture(() => view);
  await page.getByTestId("setup-journal").waitFor();
  expect(await page.locator("#board-columns").isVisible()).toBe(false);
  expect(await page.locator("#board-heading").isVisible()).toBe(false);
  expect(await page.locator(".board-command-host:visible").count()).toBe(0);
  expect(await page.getByTestId("setup-panel").innerText()).toContain("Setup in progress");
  expect(await page.locator("#lease-badge").innerText()).not.toContain("Initialization incomplete");
  const transcript = page.getByTestId("setup-transcript");
  expect(await transcript.getByTestId("setup-milestone").allTextContents()).toEqual([
    "✓Vision acceptedComplete", "○Prepare Project environmentStarted",
  ]);
  expect(await page.getByTestId("setup-panel").innerText()).not.toContain("Save Project files");
  expect(await page.getByTestId("setup-panel").innerText()).not.toContain("Ready for development");
  expect(await page.getByTestId("setup-panel").locator("aside").count()).toBe(0);
  expect(await transcript.locator("button, input, textarea, select").count()).toBe(0);
  view = fixture("approval");
  await refresh();
  await page.getByTestId("setup-approve").waitFor();
  expect(await page.getByTestId("setup-proposal").getByRole("heading", { level: 3 }).innerText()).toBe("Suggested setup");
  const proposalText = await page.getByTestId("setup-proposal").innerText();
  for (const text of ["Proposal revision:", "Base image", "Dependency configuration", "Setup directory"]) {
    expect(proposalText).not.toContain(text);
  }
  expect(await page.getByTestId("setup-proposal-image").getByTestId("setup-proposal-summary").innerText()).toBe("The image supplies database tools. The Project needs them for its checks.");
  expect(await transcript.innerText()).toContain("The setup agent suggests a change to the Project image. Review the suggested setup below.");
  expect(await transcript.innerText()).not.toContain("The image supplies database tools.");
  await page.waitForFunction(() => document.querySelector('[data-testid="setup-script"]')?.getAttribute("data-state") === "ready");
  expect(await page.getByTestId("setup-proposal-script").locator("h4").innerText()).toBe("SETUP SCRIPT");
  expect(await page.getByTestId("setup-script").locator("diffs-container").evaluate(node =>
    [...node.shadowRoot!.querySelectorAll("[data-line]")].map(row => row.textContent).join("\n"))).toBe("npm ci");
  expect(await transcript.locator("button, input, textarea, select").count()).toBe(0);
  expect(await page.getByTestId("setup-controls").getByTestId("setup-approve").count()).toBe(1);
  expect(await page.getByTestId("setup-approve").innerText()).toBe("Approve changes and continue");
  await page.waitForFunction(() => document.querySelector('[data-testid="setup-dockerfile"]')?.getAttribute("data-state") === "ready");
  expect(await page.getByTestId("setup-dockerfile").locator("diffs-container").evaluate(node =>
    [...node.shadowRoot!.querySelectorAll("[data-line]")].map(row => row.textContent).join("\n"))).toBe(dockerfile.trim());
  const details = page.locator('[data-journal-key="journal-first:report"]');
  expect(await details.evaluate(node => (node as HTMLDetailsElement).open)).toBe(false);
  await details.locator("summary").click();
  expect(await details.locator("strong").first().innerText()).toBe("database tools");
  expect(await details.locator("a[href^='javascript:']").count()).toBe(0);
  expect(await details.locator("button").count()).toBe(0);
  await details.locator("summary").focus();
  view = { ...view, inventory: { tools: { node: "24" } } };
  await refresh();
  await page.waitForTimeout(300);
  expect(await details.evaluate(node => (node as HTMLDetailsElement).open)).toBe(true);
  expect(await details.locator("summary").evaluate(node => document.activeElement === node)).toBe(true);
  await details.locator("summary").click();
  view = fixture("complete");
  Object.assign(view.environment, { setup_status: "configured", phase: "accept", active_operation_id: "journal-accept" });
  Object.assign(view.operations[0], { status: "running", ended_at: "" });
  await refresh();
  await transcript.locator('[data-milestone="accept"][data-state="started"]').waitFor();
  expect(await transcript.locator('[data-milestone="prepare"][data-state="complete"]').count()).toBe(1);
  expect(await transcript.getByTestId("setup-milestone").allTextContents()).toEqual([
    "✓Vision acceptedComplete", "✓Prepare Project environmentComplete", "○Save Project filesStarted",
  ]);
  expect(await transcript.locator('[data-milestone="ready"]').count()).toBe(0);
  view = fixture("complete");
  await refresh();
  await page.getByRole("button", { name: "Open Board", exact: true }).waitFor();
  expect(await page.getByTestId("setup-journal").innerText()).toContain("Ready for development");
  expect(await transcript.locator('[data-milestone="accept"][data-state="complete"]').count()).toBe(1);
  expect(await transcript.locator('[data-milestone="ready"][data-state="complete"]').count()).toBe(1);
  const completeMilestones = [
    "✓Vision acceptedComplete", "✓Prepare Project environmentComplete", "✓Save Project filesComplete", "✓Ready for developmentComplete",
  ];
  expect(await transcript.getByTestId("setup-milestone").allTextContents()).toEqual(completeMilestones);
  expect(await transcript.locator("button, input, textarea, select").count()).toBe(0);
  await page.getByRole("button", { name: "Open Board", exact: true }).click();
  expect(await page.getByTestId("setup-journal").count()).toBe(0);
  expect(await page.locator("#board-heading").getByRole("button", { name: "Setup history" }).count()).toBe(0);
  await openSetupHistory(smoke, project);
  await page.getByTestId("setup-journal").waitFor();
  expect(page.url()).toEndWith("#board");
  expect(await transcript.getByTestId("setup-milestone").allTextContents()).toEqual(completeMilestones);
  await page.getByRole("button", { name: "Open Environment", exact: true }).click();
  await page.getByTestId("environment-setup-history").waitFor();
  expect(page.url()).toEndWith(`#environment/${project}`);
  await page.getByTestId("environment-setup-history").click();
  await page.getByTestId("setup-journal").waitFor();
  const read = page.waitForResponse(response => response.url().endsWith("/api/environment/get")
    && response.request().postDataJSON()?.project_id === project);
  await page.reload();
  await read;
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  expect(await page.getByTestId("setup-journal").count()).toBe(0);
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 40000);

test("setup activity remains visible through the queue and operation transitions", async () => {
  const { page } = smoke;
  let view = fixture("active");
  let request: any = { available: true, status: "succeeded", observed_at: "2026-10-13T00:00:00Z" };
  await page.route("**/api/health", route => route.fulfill({ json: { setup: { request, submissions: [] } } }));
  view.operations = [];
  Object.assign(view.environment, { phase: "queued", active_operation_id: "" });
  await installFixture(() => view);
  const header = page.getByTestId("setup-phase");
  const badge = page.locator("#lease-badge");
  await header.locator(".journal-pulse").waitFor();
  expect(await badge.evaluate(node => node.classList.contains("setup-progress"))).toBe(true);
  expect(await page.getByTestId("setup-transcript").locator(".journal-pulse").count()).toBe(1);
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  const holdRead = async route => {
    if (route.request().postDataJSON()?.project_id === project) await held;
    await route.fallback();
  };
  await page.route("**/api/environment/get", holdRead);
  try {
    await page.reload();
    await header.locator(".journal-pulse").waitFor();
    expect(await page.getByTestId("setup-panel").innerText()).toContain("Culler reads the setup records");
    expect(await badge.evaluate(node => node.classList.contains("setup-progress"))).toBe(true);
  } finally {
    release();
    await page.unroute("**/api/environment/get", holdRead);
  }
  await page.getByTestId("setup-journal").waitFor();

  for (const kind of ["setup", "build", "validate", "accept"]) {
    view = fixture("active");
    Object.assign(view.operations[0], { kind, status: "queued", retry_at: "2026-10-13 00:01:00" });
    await refresh();
    const operation = page.locator('[data-operation="journal-validation"]');
    await page.waitForFunction(() => document.querySelector('[data-operation="journal-validation"] .journal-status')?.textContent === "Queued");
    expect(await operation.locator(".journal-pulse").count()).toBe(1);
    expect(await header.locator(".journal-pulse").count()).toBe(1);
    expect(await badge.evaluate(node => node.classList.contains("setup-progress"))).toBe(true);
    if (kind === "setup") await page.screenshot({ path: evidencePath("setup-agent-queued.png"), fullPage: true });
    Object.assign(view.operations[0], { status: "running", retry_at: "" });
    await refresh();
    await page.waitForFunction(() => document.querySelector('[data-operation="journal-validation"] .journal-status')?.textContent === "Active");
    expect(await operation.locator(".journal-pulse").count()).toBe(1);
  }

  view.operations = [view.operations[1]];
  view.environment.active_operation_id = "";
  await refresh();
  await page.getByTestId("setup-transition").waitFor();
  expect(await header.locator(".journal-pulse").count()).toBe(1);
  expect(await page.getByTestId("setup-transition").innerText()).toBe("Culler prepares the next setup operation.");
  const animation = await header.locator(".journal-pulse").evaluate(node => getComputedStyle(node).animationName);
  expect(animation).toBe("journal-spin");
  const transforms = await header.locator(".journal-pulse").evaluate(async node => {
    const first = getComputedStyle(node).transform;
    await new Promise(resolve => setTimeout(resolve, 150));
    return [first, getComputedStyle(node).transform];
  });
  expect(transforms[0]).not.toBe(transforms[1]);
  for (const theme of ["light", "dark"]) {
    await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      await page.screenshot({ path: evidencePath(`setup-activity-${theme}-${width}.png`), fullPage: true });
    }
  }
  await page.emulateMedia({ reducedMotion: "reduce" });
  expect(await header.locator(".journal-pulse").evaluate(node => getComputedStyle(node).animationName)).toBe("none");
  expect(await header.locator(".journal-pulse").isVisible()).toBe(true);
  expect(await badge.locator(".dot").evaluate(node => getComputedStyle(node).animationName)).toBe("none");

  request = { available: true, status: "failed", message: "The image is unavailable.", observed_at: "2026-10-13T00:01:00Z" };
  await refresh();
  await page.getByTestId("setup-health").waitFor();
  await page.waitForFunction(() => !document.querySelector("#lease-badge")?.classList.contains("setup-progress"));
  expect(await page.getByTestId("setup-panel").locator(".journal-pulse").count()).toBe(0);
  request = { available: true, status: "succeeded", observed_at: "2026-10-13T00:02:00Z" };
  for (const phase of ["approval", "failed", "complete"] as const) {
    view = fixture(phase);
    await refresh();
    const marker = phase === "approval" ? "setup-approve" : phase === "failed" ? "setup-retry" : "setup-open-board";
    await page.getByTestId(marker).waitFor();
    expect(await page.getByTestId("setup-panel").locator(".journal-pulse").count()).toBe(0);
    expect(await badge.evaluate(node => node.classList.contains("setup-progress"))).toBe(false);
  }
}, 30000);

test("failed reads retain the journal and stop activity; action failures remain beside the action", async () => {
  const { page } = smoke;
  let view: any = fixture("active");
  await installFixture(() => view);
  await page.getByTestId("setup-journal").waitFor();
  view = new Error("The fixture read failed.");
  await refresh();
  await page.getByText(/Last known/).first().waitFor();
  expect(await page.getByTestId("setup-panel").textContent()).toContain("Garden");
  expect(await page.getByTestId("setup-journal").locator(".journal-pulse").count()).toBe(0);
  await page.reload();
  await page.getByText("Setup status unavailable", { exact: true }).first().waitFor();
  view = fixture("approval");
  await refresh();
  await page.getByTestId("setup-approve").waitFor();
  await page.route("**/api/environment/approve", async route => {
    expect(route.request().postDataJSON()).toEqual({ project_id: project, revision_id: "journal-revision" });
    await route.fulfill({ status: 409, json: { error: { code: "domain", message: "The proposal changed. Read the current proposal." } } });
  });
  await page.getByTestId("setup-approve").click();
  await page.getByTestId("setup-journal").getByText("The proposal changed. Read the current proposal.", { exact: true }).waitFor();
  expect(await page.getByTestId("setup-approve").isEnabled()).toBe(true);
}, 30000);

test("the journal supports all states, themes, narrow windows, and CSS zoom at 200 percent", async () => {
  const { page } = smoke;
  let view = fixture("active");
  await page.route("**/api/health", async route => {
    const response = await route.fetch();
    const health = await response.json();
    health.setup = { request: { available: true, status: "succeeded", observed_at: "2026-10-13T00:00:00Z" }, submissions: [] };
    await route.fulfill({ json: health });
  });
  await installFixture(() => view);
  await page.getByTestId("setup-journal").waitFor();
  for (const phase of ["active", "approval", "failed", "complete"] as const) {
    view = fixture(phase);
    await refresh();
    await page.waitForTimeout(300);
    for (const theme of ["light", "dark"]) {
      await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 900 });
        expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
        await page.screenshot({ path: evidencePath(`setup-journal-${phase}-${theme}-${width}.png`), fullPage: true });
      }
      await page.setViewportSize({ width: 1280, height: 900 });
      await page.evaluate(() => { document.documentElement.style.zoom = "2"; });
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(1280);
      await page.screenshot({ path: evidencePath(`setup-journal-${phase}-${theme}-css-zoom200.png`), fullPage: true });
      await page.evaluate(() => { document.documentElement.style.zoom = ""; });
    }
  }
}, 60000);

test("setup errors use safe Markdown and remain accessible across health refreshes", async () => {
  const { page } = smoke;
  const view = fixture("failed");
  const message = "Could not resolve api.example.test\n<error>**literal output**</error>";
  view.operations[0].error = message;
  view.operations[0].metadata.failure_details = '{"event":"infrastructure-failure","evidence":"ENOTFOUND api.example.test"}\n{"level":"info","event":"runner.skill_installed"}';
  view.environment.cause = message;
  let health: any = { request: { available: true, status: "succeeded", observed_at: "2026-10-13T00:00:00Z" }, submissions: [] };
  let readFails = false;
  await page.route("**/api/health", route => route.fulfill(readFails
    ? { status: 503, json: { error: { code: "environment", message: "The health request failed." } } }
    : { json: { setup: health } }));
  await installFixture(() => view);
  const failure = page.getByTestId("setup-operation-error");
  await failure.waitFor();
  expect((await page.getByTestId("setup-panel").innerText()).split("Could not resolve api.example.test")).toHaveLength(2);
  expect(await failure.locator("error").count()).toBe(0);
  expect(await failure.locator("strong").count()).toBe(1);
  expect(await page.getByTestId("setup-controls").getByTestId("setup-retry").isVisible()).toBe(true);
  expect(await page.getByTestId("setup-controls").getByTestId("setup-ask-homie").isVisible()).toBe(true);
  expect(await page.getByTestId("setup-transcript").locator("button, input, textarea, select").count()).toBe(0);
  expect(await page.locator('[data-journal-key="setup:evidence"]').count()).toBe(0);
  expect(await page.getByTestId("setup-operation-evidence").count()).toBe(0);
  health = { request: { available: true, status: "failed", observed_at: "2026-10-13T00:01:00Z", message: "The default image is unavailable.", details: "Docker could not inspect the image." }, submissions: [] };
  await refresh();
  await page.getByTestId("setup-health").waitFor();
  expect(await page.getByTestId("setup-health").innerText()).toContain("Culler could not advance setup");
  const details = page.locator('[data-journal-key="setup-request:technical"]');
  await details.locator("summary").click();
  await details.locator("summary").focus();
  health.request.observed_at = "2026-10-13T00:01:01Z";
  await refresh();
  expect(await details.evaluate(node => (node as HTMLDetailsElement).open)).toBe(true);
  expect(await details.locator("summary").evaluate(node => document.activeElement === node)).toBe(true);
  await details.locator("summary").click();
  for (const theme of ["light", "dark"]) {
    await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      const bounds = await page.evaluate(() => ({ content: document.documentElement.scrollWidth, viewport: innerWidth }));
      expect(bounds.content).toBeLessThanOrEqual(bounds.viewport + 1);
      await page.screenshot({ path: evidencePath(`setup-errors-${theme}-${width}.png`), fullPage: true });
    }
  }
  readFails = true;
  await refresh();
  await page.waitForFunction(() => document.querySelector('[data-testid="setup-health"]')?.textContent?.includes("Last recorded setup error"));
  expect(await page.getByTestId("setup-health").innerText()).toContain("The default image is unavailable.");
  for (const theme of ["light", "dark"]) {
    await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
    await page.screenshot({ path: evidencePath(`setup-errors-stale-${theme}-390.png`), fullPage: true });
  }
  readFails = false;
  health = { request: { available: true, status: "succeeded", observed_at: "2026-10-13T00:02:00Z" }, submissions: [] };
  await refresh();
  await page.getByTestId("setup-health").waitFor({ state: "detached" });
  expect(await failure.isVisible()).toBe(true);
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.evaluate(() => { document.documentElement.style.zoom = "2"; });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
}, 60000);

test("host setup health remains visible while a spine read prevents the journal", async () => {
  const { page } = smoke;
  await page.route("**/api/health", route => route.fulfill({ json: { setup: { request: {
    available: true, status: "failed", observed_at: "2026-10-13T00:00:00Z", message: "Docker is unavailable.", details: "The helper container could not start.",
  }, submissions: [] } } }));
  await installFixture(() => new Error("The spine is unavailable."));
  await page.getByTestId("setup-health").waitFor();
  expect(await page.getByTestId("setup-health").innerText()).toContain("Docker is unavailable.");
  expect(await page.getByTestId("setup-read-error").innerText()).toContain("Setup status unavailable");
  expect(await page.getByTestId("setup-panel").getByRole("button", { name: "Open Environment", exact: true }).isVisible()).toBe(true);
}, 30000);


test("denial retains the proposal and shows activity through import", async () => {
  const { page } = smoke;
  let view = fixture("approval");
  await page.route("**/api/health", route => route.fulfill({ json: { setup: { request: {
    available: true, status: "succeeded", observed_at: "2026-10-13T00:00:00Z",
  }, submissions: [] } } }));
  await installFixture(() => view);
  const decisions: unknown[] = [];
  await page.route("**/api/environment/deny", async route => {
    decisions.push(route.request().postDataJSON());
    const first = view.operations[0];
    view = fixture("active");
    view.operations = [{ ...view.operations[0], id: "denial-continuation", status: "queued", image_digest: digest, revision_id: "journal-base" }, first];
    view.environment.active_operation_id = "denial-continuation";
    view.environment.draft_revision_id = "journal-base";
    view.approvals = [];
    view.decisions = [{ operation_id: "journal-first", revision_id: "journal-revision", decision: "denied", current_revision_id: "journal-base", current_image_digest: digest, created_at: "2026-10-13 00:01:00" }];
    await route.fulfill({ json: view });
  });
  await page.getByTestId("setup-deny").click();
  await page.getByText("Current image retained", { exact: true }).first().waitFor();
  expect(decisions).toEqual([{ project_id: project, revision_id: "journal-revision" }]);
  expect(await page.getByTestId("setup-deny").count()).toBe(0);
  expect(await page.getByTestId("setup-phase").locator(".journal-pulse").count()).toBe(1);
  await page.reload();
  await page.getByText("Current image retained", { exact: true }).first().waitFor();
  expect(await page.getByTestId("setup-phase").locator(".journal-pulse").count()).toBe(1);
  const retainedDecisions = view.decisions;
  view = fixture("complete");
  view.decisions = retainedDecisions;
  view.approvals = [];
  const current = view.revisions.find(row => row.id === "journal-base");
  view.draft = current;
  view.selected = current;
  view.environment.draft_revision_id = current.id;
  view.environment.selected_revision_id = current.id;
  view.operations = view.operations.filter(row => row.kind !== "build").map(row => row.kind === "setup" ? row : {
    ...row, revision_id: current.id, image_digest: digest,
    metadata: { ...row.metadata, previous_operation_id: row.kind === "validate" ? "journal-first" : "journal-validation" },
  });
  view.results[1].revision_id = current.id;
  view.results[1].summary = "Setup used the current image. The platform tests remain unverified.";
  await refresh();
  await page.getByTestId("setup-open-board").waitFor();
  expect(await page.getByTestId("setup-transcript").innerText()).toContain(view.results[1].summary);
  expect(await page.getByTestId("setup-phase").locator(".journal-pulse").count()).toBe(0);
}, 30000);

test("the journal shows the operator decision without historical proposal sections", async () => {
  const { page } = smoke;
  const view = fixture("active");
  const original = view.draft;
  const edited = { ...original, id: "operator-revision", editor: "operator",
    configuration: { ...original.configuration, build_args: { TOOL_VERSION: "17" } },
    explanation: "The image uses the selected database version." };
  view.draft = edited;
  view.revisions.push(edited);
  view.environment.draft_revision_id = edited.id;
  view.decisions = [{ operation_id: "journal-first", revision_id: edited.id, decision: "approved" }];
  view.approvals = [{ revision_id: edited.id, created_at: "2026-10-10 12:01:00" }];
  view.operations = view.operations.map(row => row.kind === "setup" ? row : { ...row, revision_id: edited.id });
  await installFixture(() => view);
  await page.getByText("Image changes approved", { exact: true }).first().waitFor();
  expect(await page.getByTestId("setup-proposal").count()).toBe(0);
  expect(await page.getByTestId("setup-operation-evidence").count()).toBe(0);
  await page.screenshot({ path: evidencePath("setup-approved-operator-revision.png"), fullPage: true });
  view.decisions[0] = { ...view.decisions[0], decision: "denied", current_revision_id: "journal-base", current_image_digest: digest, created_at: "2026-10-10 12:01:00" };
  view.approvals = [];
  view.draft = view.revisions.find(row => row.id === "journal-base");
  view.environment.draft_revision_id = "journal-base";
  view.operations = view.operations.filter(row => row.kind !== "build").map(row => row.kind === "setup" ? { ...row, image_digest: digest } : {
    ...row, revision_id: "journal-base", image_digest: digest, metadata: { previous_operation_id: "journal-first" },
  });
  await refresh();
  await page.getByText("Current image retained", { exact: true }).first().waitFor();
  expect(await page.getByTestId("setup-proposal").count()).toBe(0);
  expect(await page.getByTestId("setup-transcript").innerText()).toContain("The operator chose the current image.");
  expect(await page.getByTestId("setup-journal").innerText()).not.toContain("Image prepared");
  await page.screenshot({ path: evidencePath("setup-denied-operator-revision.png"), fullPage: true });
}, 30000);

test("a setup agent operation links its AgentsView trace while the agent runs", async () => {
  const { page } = smoke;
  let view = fixture("active");
  await installFixture(() => view);
  const operation = page.locator('[data-operation="journal-validation"]');
  await page.waitForFunction(() => document.querySelector('[data-operation="journal-validation"] .journal-status')?.textContent === "Active");
  expect(await operation.getByTestId("setup-trace-link").count()).toBe(0);
  view.operations[0].trace = { session: "journal-validation-session", path: "/sessions/journal-validation-session?msg=last" };
  await refresh();
  const href = `/agentsview/open?path=${encodeURIComponent("/sessions/journal-validation-session?msg=last")}`;
  await operation.locator(`[data-testid="setup-trace-link"][href="${href}"]`).waitFor();
  const link = operation.getByTestId("setup-trace-link");
  expect(await link.getAttribute("target")).toBe("_blank");
  expect(await link.getAttribute("aria-label")).toBe("Trace of setup operation journal-validation, opens a new tab");
  expect(await link.innerText()).toBe("Trace ↗");
  expect(await page.locator('[data-operation="journal-build"]').getByTestId("setup-trace-link").count()).toBe(0);
  expect(await page.getByTestId("setup-transcript").locator("button, input, textarea, select").count()).toBe(0);
  await page.screenshot({ path: evidencePath("setup-agent-trace-link.png"), fullPage: true });
  const complete = fixture("complete");
  complete.operations.find(row => row.id === "journal-validation").trace = view.operations[0].trace;
  view = complete;
  await refresh();
  await page.getByTestId("setup-open-board").waitFor();
  expect(await operation.locator(`[data-testid="setup-trace-link"][href="${href}"]`).count()).toBe(1);
}, 30000);
