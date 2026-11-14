import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, seedProject, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
const base = `sha256:${"a".repeat(64)}`;

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "environment-garden", { title: "Garden" });
  await seedProject(smoke, "environment-library", { title: "Library" });
  await gotoReady(smoke);
}, 60000);
afterAll(async () => { if (smoke) await teardown(smoke); });

async function environmentReady() {
  await smoke.page.waitForFunction(() => location.hash.startsWith("#environment/") && document.querySelector('#environment-pane:not([hidden])[aria-busy="false"] [data-testid="environment-base"]'));
}

async function environment(action: string, body: object) {
  return smoke.page.evaluate(async ({ action, body }) => {
    const response = await fetch(`/api/environment/${action}`, { method: "POST", headers: {
      authorization: "Bearer smoke-token", "content-type": "application/json",
    }, body: JSON.stringify(body) });
    const result = await response.json();
    if (!response.ok) throw new Error(JSON.stringify(result));
    return result;
  }, { action, body });
}

const APPLIED = "Culler recorded the approval and queued Project preparation.";

test("the Environment page preserves direct edits and applies the exact saved draft", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-projects").click();
  await page.getByTestId("environment-project-environment-garden").click();
  await environmentReady();
  expect(page.url()).toEndWith("#environment/environment-garden");
  expect(await page.getByTestId("environment-apply").isDisabled()).toBe(true);
  expect(await page.getByTestId("environment-discard").isDisabled()).toBe(true);
  expect(await page.getByTestId("environment-cache-clear").isDisabled()).toBe(true);
  expect(await page.getByTestId("environment-cache-size").innerText()).toBe("Not measured");
  expect(await page.getByTestId("environment-home-size").innerText()).toBe("Not measured");
  expect(await page.getByTestId("environment-base").isVisible()).toBe(false);
  await page.getByTestId("environment-advanced").locator("summary").click();
  await page.getByTestId("environment-base").fill(base);
  await page.getByTestId("environment-setup-script").fill("npm ci");
  await page.getByTestId("environment-variables").fill('# The tests read these values.\nNODE_ENV=test\nGREETING="hello world "');
  await page.getByTestId("environment-add-services").click();
  expect(await page.getByLabel("Service readiness command 1", { exact: true }).inputValue()).toBe("");
  await page.getByLabel("Service name 1", { exact: true }).fill("local-server");
  await page.getByLabel("Service command 1", { exact: true }).fill("node server.js");
  await page.getByLabel("Service readiness command 1", { exact: true }).fill("curl --fail http://localhost:3000");
  expect(await page.getByTestId("environment-advanced").locator("summary").innerText()).toContain("1 service");
  expect(await page.getByTestId("environment-apply").innerText()).toBe("Save and apply");
  expect(await page.getByTestId("environment-assist").isDisabled()).toBe(true);
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-projects").click();
  await page.getByTestId("environment-project-environment-library").click();
  await environmentReady();
  expect(await page.getByTestId("environment-base").inputValue()).toBe("");
  expect(await page.getByTestId("environment-setup-script").inputValue()).toBe("");
  await page.evaluate(() => { location.hash = "#environment/environment-garden"; });
  await page.getByTestId("environment-setup-script").waitFor();
  await page.waitForFunction(() => document.querySelector<HTMLTextAreaElement>('[data-testid="environment-setup-script"]')?.value === "npm ci");
  await page.reload();
  await environmentReady();
  expect(await page.getByTestId("environment-base").inputValue()).toBe(base);
  expect(await page.getByLabel("Service command 1", { exact: true }).inputValue()).toBe("node server.js");
  expect(await page.getByTestId("environment-variables").inputValue()).toBe('NODE_ENV=test\nGREETING="hello world "');
  await page.getByTestId("environment-apply").click();
  await page.getByTestId("environment-status").getByText(APPLIED, { exact: true }).waitFor();
  const applied = await environment("get", { project_id: "environment-garden" });
  expect(applied.draft.configuration.setup.command).toBe("npm ci");
  expect(applied.draft.configuration.variables).toEqual([
    { name: "NODE_ENV", value: "test", scope: "both" }, { name: "GREETING", value: "hello world ", scope: "both" }]);
  expect(applied.draft.configuration.services).toEqual([
    { name: "local-server", command: "node server.js", directory: ".", ready_command: "curl --fail http://localhost:3000" }]);
  expect(applied.draft.configuration).not.toHaveProperty("checks");
  expect(applied.approvals.map((approval: { revision_id: string }) => approval.revision_id)).toEqual([applied.draft.id]);
  expect(applied.environment.phase).toBe("queued");
  expect(applied.environment.readiness).toBe("setup_required");
  expect(applied.environment.selected_revision_id).toBe("");
  expect(await page.getByTestId("environment-apply").isDisabled()).toBe(true);
  expect(await page.getByTestId("environment-phase").innerText()).not.toContain("Ready for development");
  expect(await page.getByTestId("environment-pane").innerText()).not.toMatch(/^false$/m);
  await page.getByTestId("environment-setup-script").fill("npm ci --offline");
  await page.getByTestId("environment-discard").click();
  expect(await page.getByTestId("environment-setup-script").inputValue()).toBe("npm ci");
  expect(await page.getByTestId("environment-apply").isDisabled()).toBe(true);
  await page.getByTestId("environment-assist").click();
  await page.getByTestId("environment-assist-instruction").fill("Add the PostgreSQL client library.");
  await page.getByTestId("environment-assist-send").click();
  await page.getByTestId("environment-status").getByText("Culler queued the request for the setup agent.", { exact: true }).waitFor();
  await page.getByTestId("environment-assist-panel").waitFor({ state: "detached" });
  expect((await environment("get", { project_id: "environment-garden" })).environment.instruction).toBe("Add the PostgreSQL client library.");
  await page.screenshot({ path: evidencePath("culler-environment-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: evidencePath("culler-environment-mobile.png"), fullPage: true });
  await page.setViewportSize({ width: 1280, height: 720 });
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 40000);

test("the Dockerfile editor records build contents and a stale draft preserves local edits", async () => {
  const { page } = smoke;
  await page.evaluate(() => { location.hash = "#environment/environment-library"; });
  await page.waitForFunction(() => document.querySelector("#environment-pane h2")?.textContent?.startsWith("Library"));
  await page.getByTestId("environment-advanced").locator("summary").click();
  await page.getByTestId("environment-base").fill(base);
  await page.getByRole("button", { name: "Add Dockerfile", exact: true }).click();
  expect(await page.getByTestId("environment-dockerfile").inputValue()).toBe(`FROM culler-project-base:${base.slice(7)}\n`);
  expect(await page.getByTestId("environment-dockerfile-path").inputValue()).toBe(".culler/Dockerfile");
  const dockerfile = `FROM culler-project-base:${base.slice(7)}\nRUN apt-get update && apt-get install -y libpq-dev=16.4-1\n`;
  await page.getByTestId("environment-dockerfile").fill(dockerfile);
  // The colored copy holds the same text as the text area, and the line numbers follow it.
  const editor = page.locator(".code-editor", { has: page.getByTestId("environment-dockerfile") });
  expect(await editor.locator(".code-paint").evaluate(node => node.textContent)).toBe(`${dockerfile} `);
  expect(await editor.locator(".code-gutter").evaluate(node => node.textContent)).toBe("1\n2\n3");
  expect(await editor.locator(".tok-keyword").allInnerTexts()).toEqual(["FROM", "RUN"]);
  await page.getByTestId("environment-add-build_files").click();
  await page.getByLabel("Repository file path 1", { exact: true }).fill(".culler/install.sh");
  await page.getByLabel("File contents 1", { exact: true }).fill("#!/bin/bash\nset -eu\n");
  await page.getByTestId("environment-apply").click();
  await page.getByTestId("environment-status").getByText(APPLIED, { exact: true }).waitFor();
  const original = await environment("get", { project_id: "environment-library" });
  expect(original.draft.configuration.dockerfile.content).toBe(dockerfile);
  expect(original.draft.configuration.build_files[0].content).toBe("#!/bin/bash\nset -eu\n");
  await page.getByTestId("environment-setup-script").fill("npm ci --offline");
  await environment("save", { project_id: "environment-library", expected_revision_id: original.draft.id,
    configuration: { ...original.draft.configuration, setup: { directory: ".", command: "npm ci --ignore-scripts" } }, explanation: original.draft.explanation });
  await page.reload();
  await page.getByRole("button", { name: "Keep my edits", exact: true }).waitFor();
  expect(await page.getByTestId("environment-setup-script").inputValue()).toBe("npm ci --offline");
  expect(await page.getByTestId("environment-apply").isDisabled()).toBe(true);
  await page.getByRole("button", { name: "Keep my edits", exact: true }).click();
  await page.getByTestId("environment-apply").click();
  await page.getByTestId("environment-status").getByText(APPLIED, { exact: true }).waitFor();
  const kept = await environment("get", { project_id: "environment-library" });
  expect(kept.draft.configuration.setup.command).toBe("npm ci --offline");
  expect(kept.draft.configuration.dockerfile.content).toBe(dockerfile);
}, 40000);

test("the Board approves a proposal for Image dependencies once and restores its wait after a reload", async () => {
  const { page } = smoke;
  const applied = await environment("get", { project_id: "environment-library" });
  const proposed = await environment("save", { project_id: "environment-library", expected_revision_id: applied.draft.id,
    configuration: { ...applied.draft.configuration, setup: { directory: ".", command: "npm ci" } },
    explanation: "The database package requires PostgreSQL development files." });
  let approved = false, approvals = 0;
  // The smoke has no resident. This test supplies its successful setup observation.
  await page.route("**/api/health", route => route.fulfill({ json: { setup: {
    request: { resident_instance_id: "environment-smoke", available: true, status: "succeeded", observed_at: new Date().toISOString() },
    submissions: [],
  } } }));
  await page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== "environment-library") return route.continue();
    const current = approved ? await (await route.fetch()).json() : proposed;
    await route.fulfill({ json: { ...current, environment: { ...current.environment, setup_status: "configured", ...(!approved ? { phase: "approval" } : {}) } } });
  });
  await page.route("**/api/environment/approve", async route => {
    approvals++;
    expect(route.request().postDataJSON()).toEqual({ project_id: "environment-library", revision_id: proposed.draft.id });
    const response = await route.fetch(); approved = true;
    const current = await response.json();
    await route.fulfill({ json: { ...current, environment: { ...current.environment, setup_status: "configured" } } });
  });
  try {
    await page.getByTestId("tab-board").click();
    await selectProject(smoke, "environment-library");
    await page.getByTestId("setup-approve").waitFor();
    expect(await page.getByTestId("setup-panel").innerText()).toContain(proposed.draft.explanation);
    expect(await page.getByTestId("setup-panel").getByRole("button", { name: /pause|view details/i }).count()).toBe(0);
    expect(await page.getByTestId("setup-ask-collie").count()).toBe(0);
    await page.waitForFunction(() => document.querySelector('[data-testid="setup-dockerfile"]')?.getAttribute("data-state") === "ready");
    expect(await page.getByTestId("setup-dockerfile").locator(".file-view-change").innerText()).toBe("Added");
    expect(await page.getByTestId("setup-dockerfile").locator("diffs-container").evaluate(node =>
      [...node.shadowRoot!.querySelectorAll("[data-line]")].map(row => row.textContent).join("\n")))
      .toBe(proposed.draft.configuration.dockerfile.content.trimEnd());
    await page.getByTestId("setup-approve").click();
    await page.getByTestId("setup-approve").waitFor({ state: "detached" });
    expect(approvals).toBe(1);
    await page.reload();
    await page.getByTestId("setup-journal").waitFor();
    expect(await page.getByTestId("setup-phase").innerText()).toBe("Setup in progress");
    expect(await page.getByTestId("setup-completion").count()).toBe(0);
    expect(await page.getByTestId("setup-approve").count()).toBe(0);
  } finally {
    await page.unrouteAll({ behavior: "wait" });
  }
}, 30000);


test("a later report retains readable details while the accepted environment stays ready", async () => {
  const { page } = smoke;
  const saved = await environment("get", { project_id: "environment-library" });
  const result = { id: "current-report", revision_id: saved.draft.id, outcome: "complete", created_at: "2026-10-10 09:12:07", image_digest: base,
    summary: "The preparation is complete. The container cannot execute the repository tests.",
    details: "## Repository tests\n\n- The command `npm test` requires an unavailable service.\n- The tests remain **unverified**." };
  let active = false;
  const view = () => ({ ...saved,
    environment: { ...saved.environment, selected_revision_id: "accepted-environment", readiness: "ready", phase: "queued", active_operation_id: active ? "acceptance" : "" },
    results: [result], operations: [], disk_use: { image_bytes: 2_184_000_000, home_bytes: 824_000_000, cache_bytes: 348_200_000 },
  });
  await page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== "environment-library") return route.continue();
    await route.fulfill({ json: view() });
  });
  try {
    await page.evaluate(() => { location.hash = "#environment/environment-library"; });
    const report = page.getByTestId("environment-results");
    await report.getByText(result.summary, { exact: true }).waitFor();
    expect(await report.locator("details").evaluate(node => node.open)).toBe(false);
    expect(await page.getByTestId("environment-phase").innerText()).toContain("Agent runs use the accepted environment until Culler accepts this draft.");
    expect(await page.getByTestId("environment-cache-size").innerText()).toBe("348 MB");
    expect(await page.getByTestId("environment-home-size").innerText()).toBe("824 MB");
    expect(await page.getByTestId("environment-cache-clear").innerText()).toBe("Clear /cache");
    expect(await page.getByTestId("environment-cache-clear").getAttribute("title")).toContain("preserves the Project home");
    expect(await page.getByTestId("environment-cache-clear").isDisabled()).toBe(false);
    await report.locator("summary").click();
    await report.locator("summary").focus();
    expect(await report.locator("h2").innerText()).toBe("Repository tests");
    expect(await report.locator("strong").innerText()).toBe("unverified");
    await report.locator("details").evaluate(node => { node.dataset.kept = "yes"; });
    await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
    await page.waitForFunction(() => document.querySelector("#environment-pane")?.getAttribute("aria-busy") === "false");
    expect(await report.locator("details").getAttribute("data-kept")).toBe("yes");
    expect(await report.locator("summary").evaluate(node => node === document.activeElement)).toBe(true);
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await page.screenshot({ path: evidencePath("culler-environment-report-mobile.png"), fullPage: true });
    await page.setViewportSize({ width: 1280, height: 720 });
    active = true;
    await page.reload();
    await environmentReady();
    expect(await page.getByTestId("environment-cache-clear").isDisabled()).toBe(true);
  } finally { await page.unroute("**/api/environment/get"); }
}, 30000);
