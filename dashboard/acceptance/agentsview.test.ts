import { afterAll, beforeAll, expect, test } from "bun:test";
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import type { Page } from "playwright";
import { bootSmoke, evidencePath, gotoReady, selectProject, SMOKE_TOKEN, teardown, type Smoke } from "../smoke/harness";

let smoke: Smoke;
let processHandle: ReturnType<typeof Bun.spawn> | undefined;
let launch: () => void;
let file: string;
let configFile: string;
const ID = "33333333-3333-4333-8333-333333333333";
const PATH = `/sessions/${ID}?msg=last`;
const SECOND_ID = "44444444-4444-4444-8444-444444444444";
const SECOND_PATH = `/sessions/${SECOND_ID}?msg=last`;
const observedAuthPages: string[] = [];
const unexpectedRequests: string[] = [];
const viewerRequests: string[] = [];
const viewerErrors: string[] = [];
let upstreamOrigin = "";

function line(n: number, content: string) {
  return JSON.stringify({ type: n % 2 ? "user" : "assistant", uuid: `message-${n}`, parentUuid: n > 1 ? `message-${n-1}` : null,
    sessionId: ID, cwd: "/workspace/source", timestamp: new Date().toISOString(),
    message: { role: n % 2 ? "user" : "assistant", content, ...(n % 2 ? {} : { model: "fixture", usage: { input_tokens: 10, output_tokens: 10 } }) } }) + "\n";
}

async function ready(token = smoke.agentsviewToken) {
  for (let count = 0; count < 100; count++) {
    try {
      const result = await fetch(`${upstreamOrigin}/api/v1/sessions/${ID}`, { headers: { authorization: `Bearer ${token}` } });
      if (result.ok) return;
    } catch {}
    if (processHandle?.exitCode !== null) throw new Error("The isolated AgentsView process exited.");
    await Bun.sleep(100);
  }
  throw new Error("The isolated AgentsView trace did not appear.");
}

beforeAll(async () => {
  const executable = process.env.MC_SMOKE_AGENTSVIEW_BIN;
  if (!executable) throw new Error("MC_SMOKE_AGENTSVIEW_BIN must name the pinned AgentsView executable.");
  const pin = JSON.parse(readFileSync(join(import.meta.dir, "../../mc/deployment/agentsview.json"), "utf8"));
  const actual = createHash("sha256").update(readFileSync(executable)).digest("hex");
  expect(actual).toBe(pin.executable.sha256);
  smoke = await bootSmoke();
  upstreamOrigin = smoke.agentsview.url;
  const port = smoke.agentsview.port;
  smoke.agentsview.stop();
  const root = join(smoke.scratch, "real-agentsview");
  const traces = join(root, "traces", "projects");
  mkdirSync(join(traces, "culler"), { recursive: true });
  file = join(traces, "culler", `${ID}.jsonl`);
  writeFileSync(file, line(1, "Culler authentication trace.") + line(2, "The authenticated trace is visible."));
  writeFileSync(join(traces, "culler", `${SECOND_ID}.jsonl`),
    (line(1, "A second Culler trace.") + line(2, "The authenticated trace is visible.")).replaceAll(ID, SECOND_ID));
  configFile = join(root, "config.toml");
  writeFileSync(configFile, `require_auth = true\nauth_token = "${smoke.agentsviewToken}"\ndisable_update_check = true\n` +
    `disabled_agents = ${JSON.stringify(pin.disabled_agents)}\n[agents.claude]\ndirs = [${JSON.stringify(traces)}]\n`, { mode: 0o600 });
  launch = () => {
    processHandle = Bun.spawn([executable, "serve", "--host", "127.0.0.1", "--port", String(port), "--no-browser", "--no-update-check"], {
      env: { ...process.env, HOME: root, AGENTSVIEW_DATA_DIR: root, AGENTSVIEW_TELEMETRY_ENABLED: "0",
        HTTP_PROXY: "http://127.0.0.1:9", HTTPS_PROXY: "http://127.0.0.1:9" }, stdout: "ignore", stderr: "ignore",
    });
  };
  launch();
  await ready();
  mkdirSync(join(smoke.home, "supervision", "LaunchAgents"), { recursive: true });
  writeFileSync(join(smoke.home, "supervision", "LaunchAgents", "agentsview.plist"), "<plist/>\n");
  await smoke.context.exposeBinding("recordAuthenticationPage", (_source, text) => { observedAuthPages.push(text); });
  await smoke.context.addInitScript(() => {
    try {
      localStorage.setItem("agentsview-auth-token", "incorrect-browser-token");
      localStorage.setItem("agentsview-server-url", "http://127.0.0.1:9");
    } catch {}
    const observer = new MutationObserver(() => {
      if (document.body?.innerText.includes("Authentication Required")) window.recordAuthenticationPage(location.href);
    });
    observer.observe(document, { childList: true, subtree: true, characterData: true });
  });
  smoke.context.on("request", req => {
    const url = new URL(req.url());
    if (url.pathname.startsWith("/api/v1/") || req.isNavigationRequest()) viewerRequests.push(url.pathname + url.search);
    if (url.origin === upstreamOrigin || url.hostname !== "127.0.0.1" || req.url().includes(smoke.agentsviewToken)) unexpectedRequests.push(req.url());
  });
  smoke.context.on("page", page => {
    page.on("pageerror", error => viewerErrors.push(error.message));
    page.on("console", message => { if (["error", "warning"].includes(message.type())) viewerErrors.push(message.text()); });
  });
  await gotoReady(smoke);
  await smoke.page.locator('[data-testid="header-traces"][href]').waitFor();
  writeFileSync(evidencePath("agentsview-version.json"), JSON.stringify({ agentsview: pin.version, executable_sha256: actual,
    browser: smoke.context.browser()!.version(), platform: process.platform }, null, 2) + "\n");
}, 90000);

afterAll(async () => {
  processHandle?.kill();
  await processHandle?.exited;
  await teardown(smoke);
});

async function open(path = PATH): Promise<Page> {
  const page = await smoke.context.newPage();
  await page.goto(`${smoke.server.url}agentsview/open?path=${encodeURIComponent(path)}`);
  await page.waitForURL(url => url.pathname + url.search === path);
  return page;
}

async function traceVisible(page: Page) {
  await page.getByText("The authenticated trace is visible.", { exact: false }).first().waitFor({ timeout: 20000 });
  expect(await page.getByText("Authentication Required", { exact: true }).count()).toBe(0);
  expect(await page.evaluate(() => localStorage.getItem("mc-dashboard-token"))).toBeNull();
  expect(await page.content()).not.toContain(smoke.agentsviewToken);
}

test("the header opens the real authenticated list without clipboard access", async () => {
  const [page] = await Promise.all([smoke.context.waitForEvent("page"), smoke.page.getByTestId("header-traces").click()]);
  try {
    await page.waitForURL(url => url.pathname === "/sessions");
    await page.getByText("Culler authentication trace.", { exact: false }).first().waitFor({ timeout: 20000 });
    expect(viewerRequests).toContain("/sessions?include_one_shot=true&include_automated=true");
    // AgentsView omits the default one-shot filter from its final address.
    expect(viewerRequests.some(path => path.startsWith("/api/v1/analytics/sessions?") &&
      new URLSearchParams(path.split("?")[1]).get("include_one_shot") === "true" &&
      new URLSearchParams(path.split("?")[1]).get("automated_scope") === "all")).toBe(true);
    expect(await page.evaluate(() => [window.opener, document.referrer])).toEqual([null, ""]);
    expect(observedAuthPages).toEqual([]);
    await page.screenshot({ path: evidencePath("agentsview-authenticated-list.png") });
  } finally { await page.close(); }
}, 45000);

test("trace access survives reload, concurrent tabs, and new trace content", async () => {
  const first = await open();
  const second = await open(SECOND_PATH);
  try {
    await traceVisible(first); await traceVisible(second);
    expect(new URL(first.url()).pathname).toBe(`/sessions/${ID}`);
    expect(new URL(second.url()).pathname).toBe(`/sessions/${SECOND_ID}`);
    await first.reload(); await traceVisible(first);
    await smoke.context.clearCookies();
    await first.reload(); await traceVisible(first);
    await first.goto(new URL("/sessions?include_one_shot=true&include_automated=true", first.url()).href);
    await first.getByText("Culler authentication trace.", { exact: false }).first().waitFor();
    await first.goBack(); await traceVisible(first);
    await first.goForward();
    await first.getByText("Culler authentication trace.", { exact: false }).first().waitFor();
    await first.goBack(); await traceVisible(first);
    const settings = await first.evaluate(async () => (await fetch("/api/v1/settings")).json());
    expect(settings.auth_token).toBeUndefined();
    expect(settings.require_auth).toBe(true);
    await first.reload(); await traceVisible(first);
    await first.bringToFront();
    const updated = first.waitForResponse(async response => {
      if (new URL(response.url()).pathname !== `/api/v1/sessions/${ID}/messages` || !response.ok()) return false;
      const body = await response.json();
      return body.messages.some(message => message.content === "The active trace has new content.");
    }, { timeout: 25000 });
    appendFileSync(file, line(3, "Continue the trace.") + line(4, "The active trace has new content."));
    try {
      await updated;
      // The pinned viewer receives live data but retains its displayed transcript until reload.
      await first.reload();
      await first.getByText("The active trace has new content.", { exact: false }).first().waitFor({ timeout: 25000 });
    } catch (error) {
      writeFileSync(evidencePath("agentsview-live-requests.json"), JSON.stringify(viewerRequests, null, 2));
      const result = await fetch(`${upstreamOrigin}/api/v1/sessions/${ID}`, { headers: { authorization: `Bearer ${smoke.agentsviewToken}` } });
      writeFileSync(evidencePath("agentsview-live-session.json"), JSON.stringify(await result.json(), null, 2));
      writeFileSync(evidencePath("agentsview-live-errors.json"), JSON.stringify(viewerErrors, null, 2));
      const messages = await first.evaluate(async id => (await fetch(`/api/v1/sessions/${id}/messages?from=0&limit=1000&direction=asc`)).json(), ID);
      writeFileSync(evidencePath("agentsview-live-messages.json"), JSON.stringify(messages, null, 2));
      await first.screenshot({ path: evidencePath("agentsview-live-failure.png") });
      throw error;
    }
    await first.screenshot({ path: evidencePath("agentsview-authenticated-trace.png") });
    expect(observedAuthPages).toEqual([]);
    expect(unexpectedRequests).toEqual([]);
  } finally { await first.close(); await second.close(); }
}, 60000);

test("the active and retained setup journal open the real trace", async () => {
  smoke.page.setDefaultTimeout(10000);
  smoke.page.setDefaultNavigationTimeout(15000);
  const project = "authenticated-setup";
  const revision = { id: "trace-revision", configuration: { base_digest: `sha256:${"a".repeat(64)}` }, explanation: "The Project uses the default image." };
  let complete = false;
  const projectRow = () => ({ id: project, title: "Trace acceptance", status: complete ? "active" : "paused", setup_status: complete ? "ready" : "configured" });
  await smoke.page.route("**/api/projects", route => route.fulfill({ json: { projects: [projectRow()] } }));
  await smoke.page.route("**/api/pause", async route => {
    const response = await route.fetch();
    const state = await response.json();
    state.projects = [projectRow()];
    await route.fulfill({ json: state });
  });
  await smoke.page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== project) return route.continue();
    await route.fulfill({ json: {
      environment: { project_id: project, project_title: "Trace acceptance", project_status: complete ? "active" : "paused",
        setup_status: complete ? "ready" : "configured", setup_id: "trace-setup", vision_revision: "trace-vision",
        selected_revision_id: complete ? revision.id : "", draft_revision_id: revision.id,
        phase: complete ? "complete" : "setup", readiness: complete ? "ready" : "setup_required",
        active_operation_id: complete ? "" : "trace-operation", cause: "" },
      draft: revision, selected: complete ? revision : null, revisions: [revision], approvals: [],
      vision: { revision: "trace-vision", content: "# Trace acceptance\n\nVerify direct access to the trace.", created_at: "2026-10-18 12:00:00" },
      operations: [{ id: "trace-operation", project_id: project, revision_id: revision.id, kind: "setup",
        status: complete ? "succeeded" : "running", attempt: 1, created_at: "2026-10-18 12:00:00",
        ended_at: complete ? "2026-10-18 12:01:00" : "", metadata: complete ? { report_outcome: "complete" } : {},
        trace: { session: ID, path: PATH } }, ...(complete ? [{
          id: "trace-accept", project_id: project, revision_id: revision.id, kind: "accept", status: "succeeded", attempt: 1,
          created_at: "2026-10-18 12:01:00", ended_at: "2026-10-18 12:02:00",
          metadata: { initial_setup: true, result_id: "trace-result", source_commit: "c".repeat(40), previous_operation_id: "trace-operation" },
        }] : [])].reverse(),
      results: complete ? [{ id: "trace-result", operation_id: "trace-operation", revision_id: revision.id, outcome: "complete",
        summary: "The Project is ready.", details: "", image_digest: revision.configuration.base_digest }] : [],
    } });
  });
  try {
    await smoke.page.reload();
    await smoke.page.getByTestId("tab-board").click();
    await selectProject(smoke, project);
    await smoke.page.reload();
    const operation = smoke.page.locator('[data-operation="trace-operation"]');
    for (const state of ["Active", "Preparation complete"]) {
      await operation.locator(".journal-status").getByText(state, { exact: true }).waitFor();
      const [page] = await Promise.all([smoke.context.waitForEvent("page"), operation.getByTestId("setup-trace-link").click()]);
      try {
        await traceVisible(page);
        expect(new URL(page.url()).pathname + new URL(page.url()).search).toBe(PATH);
      } finally { await page.close(); }
      complete = true;
      await smoke.page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
    }
    expect(observedAuthPages).toEqual([]);
  } catch (error) {
    await smoke.page.screenshot({ path: evidencePath("agentsview-setup-failure.png") });
    writeFileSync(evidencePath("agentsview-setup-failure.txt"), await smoke.page.locator("body").innerText());
    throw error;
  } finally { await smoke.page.unrouteAll({ behavior: "wait" }); }
}, 60000);

test("keyboard and browser tab actions keep automatic authentication", async () => {
  const link = smoke.page.getByTestId("header-traces");
  for (const activate of [
    async () => { await link.focus(); await link.press("Enter"); },
    () => link.click({ button: "middle" }),
    () => link.click({ modifiers: [process.platform === "darwin" ? "Meta" : "Control"] }),
  ]) {
    const [page] = await Promise.all([smoke.context.waitForEvent("page"), activate()]);
    try {
      await page.waitForURL(url => url.pathname === "/sessions");
      await page.getByText("Culler authentication trace.", { exact: false }).first().waitFor();
      expect(await page.evaluate(() => window.opener)).toBeNull();
    } finally { await page.close(); }
  }
  expect(observedAuthPages).toEqual([]);
}, 60000);

test("cookie and clipboard restrictions preserve trace access", async () => {
  await smoke.context.clearCookies();
  const page = await smoke.context.newPage();
  const protocol = await smoke.context.newCDPSession(page);
  try {
    await protocol.send("Network.enable");
    await protocol.send("Network.setCookieControls", {
      enableThirdPartyCookieRestriction: true,
      disableThirdPartyCookieMetadata: true,
      disableThirdPartyCookieHeuristics: true,
    });
    await page.addInitScript(() => {
      Object.defineProperty(navigator, "clipboard", { get() { throw new Error("Clipboard access is blocked."); } });
    });
    await page.goto(`${smoke.server.url}agentsview/open?path=${encodeURIComponent(PATH)}`);
    await traceVisible(page);
    expect(observedAuthPages).toEqual([]);
  } finally { await protocol.detach(); await page.close(); }
}, 45000);

test("an agent container receives no trace data through the host addresses", async () => {
  const page = await open();
  try {
    await traceVisible(page);
    const image = process.env.MC_BOUNDARY_IMAGE ?? "mc-prod";
    async function status(origin: string, path: string) {
      const address = new URL(origin);
      const child = Bun.spawn(["docker", "run", "--rm", "--pull=never", "--read-only", "--user", "10002:10002",
        "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--entrypoint", "/usr/bin/curl", image,
        "--silent", "--show-error", "--noproxy", "*", "--connect-timeout", "5", "--max-time", "10", "--output", "/dev/null",
        "--write-out", "%{http_code}", "--header", `Host: ${address.host}`, `http://host.docker.internal:${address.port}${path}`],
      { stdout: "pipe", stderr: "pipe" });
      const [code, output, error] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]);
      if (code !== 0) throw new Error(`The container access check failed: ${error}`);
      return Number(output);
    }
    expect(await status(smoke.server.url, "/")).toBe(200);
    expect(await status(smoke.server.url, "/api/agentsview/access")).toBe(401);
    expect(await status(upstreamOrigin, "/api/v1/settings")).toBe(401);
    expect(await status(new URL(page.url()).origin, "/api/v1/settings")).toBe(503);
  } finally { await page.close(); }
}, 60000);

test("a service failure gives a Culler error and retry keeps the destination", async () => {
  processHandle!.kill(); await processHandle!.exited;
  const page = await smoke.context.newPage();
  try {
    await page.goto(`${smoke.server.url}agentsview/open?path=${encodeURIComponent(PATH)}`);
    await page.getByRole("button", { name: "Retry" }).waitFor();
    expect(await page.locator("#destination").innerText()).toBe(PATH);
    expect(await page.locator("#status").innerText()).toContain("unavailable");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: evidencePath("agentsview-error-narrow.png") });
    launch(); await ready();
    await page.getByRole("button", { name: "Retry" }).click();
    await traceVisible(page);
    expect(new URL(page.url()).pathname + new URL(page.url()).search).toBe(PATH);
    expect(observedAuthPages).toEqual([]);
  } finally { await page.close(); }
}, 45000);

test("dashboard authorization resumes the original trace destination", async () => {
  const page = await smoke.context.newPage();
  try {
    await smoke.page.evaluate(() => localStorage.removeItem("mc-dashboard-token"));
    await page.goto(`${smoke.server.url}agentsview/open?path=${encodeURIComponent(PATH)}`);
    await page.getByRole("button", { name: "Retry" }).waitFor();
    expect(await page.locator("#status").innerText()).toContain("dashboard link from setup");
    await smoke.page.evaluate(token => localStorage.setItem("mc-dashboard-token", token), SMOKE_TOKEN);
    await traceVisible(page);
    expect(new URL(page.url()).pathname + new URL(page.url()).search).toBe(PATH);
    expect(observedAuthPages).toEqual([]);
  } finally { await page.close(); }
}, 45000);

test("credential replacement recovers through Culler without a token form", async () => {
  const replacement = "b".repeat(64);
  processHandle!.kill(); await processHandle!.exited;
  writeFileSync(configFile, readFileSync(configFile, "utf8").replace(smoke.agentsviewToken, replacement));
  launch(); await ready(replacement);
  const page = await smoke.context.newPage();
  try {
    await page.goto(`${smoke.server.url}agentsview/open?path=${encodeURIComponent(PATH)}`);
    await page.getByRole("button", { name: "Retry" }).waitFor();
    expect(await page.locator("#status").innerText()).toContain("refused the deployment credential");
    expect(await page.content()).not.toContain(replacement);
    writeFileSync(join(smoke.home, "agentsview", "token"), replacement, { mode: 0o600 });
    smoke.agentsviewToken = replacement;
    await page.getByRole("button", { name: "Retry" }).click();
    await traceVisible(page);
    expect(observedAuthPages).toEqual([]);
  } finally { await page.close(); }
}, 45000);
