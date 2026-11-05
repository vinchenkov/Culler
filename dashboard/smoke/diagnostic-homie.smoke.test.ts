import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let taskID: number;
let conditions: Record<string, unknown>[] = [];
const project = "diagnostic-project";
const reason = "The container cannot execute the required Docker boundary test.";

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, project, { environmentReady: true });
  await mc(["task", "add", "Verify container access", "--project", project]);
  const tasks = (await mc(["task", "list"])).tasks as { id: number }[];
  taskID = tasks[0].id;
  await mc(["task", "block", String(taskID), "--reason", reason]);
  await smoke.page.route("**/api/system/status", route => route.fulfill({ json: {
    complete: true, observed_at: "2026-10-20T10:00:00Z", conditions, connections: conditions.filter(item => item.group === "connections"),
  } }));
  await gotoReady(smoke);
  await selectProject(smoke, project);
}, 60000);
afterAll(async () => { await teardown(smoke); });

async function selectedConversation() {
  await smoke.page.waitForFunction(() => location.hash === "#homie" && document.querySelector('[data-testid="session-row"].selected'));
  return (await smoke.page.locator('[data-testid="session-row"].selected').getAttribute("data-session"))!;
}

test("a task issue opens ordinary Homie with evidence and preserves the prior draft", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-homie").click();
  const prior = await selectedConversation();
  await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement).disabled);
  await page.getByTestId("send-body").fill("Keep this draft.");
  await page.getByTestId("tab-board").click();
  await page.locator(`[data-testid="board-card"][data-task="${taskID}"]`).getByTestId("board-card-open").click();
  await page.getByTestId("task-ask-homie").waitFor();
  for (const [width, theme] of [[1440, "light"], [390, "dark"]] as const) {
    await page.setViewportSize({ width, height: 1000 });
    await page.evaluate(value => document.documentElement.dataset.theme = value, theme);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.screenshot({ path: evidencePath(`diagnostic-task-${width}-${theme}.png`), animations: "disabled" });
  }
  let releaseSend!: () => void;
  const held = new Promise<void>(resolve => { releaseSend = resolve; });
  await page.route("**/api/sessions/*/send", async route => { await held; await route.continue(); });
  const send = page.waitForRequest("**/api/sessions/*/send");
  await page.getByTestId("task-ask-homie").click();
  await send;
  const refreshed = page.waitForResponse("**/api/system/status");
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await (await refreshed).finished();
  expect(await page.getByTestId("task-ask-homie").isDisabled()).toBe(true);
  releaseSend();
  const id = await selectedConversation();
  await page.unroute("**/api/sessions/*/send");
  expect(id).not.toBe(prior);
  expect(await page.getByTestId("send-body").inputValue()).toBe("Keep this draft.");
  expect(await page.locator("dialog[open]").count()).toBe(0);
  const history = await mc(["homie", "history", id]);
  const messages = history.messages as { direction: string; body: string }[];
  expect(messages).toHaveLength(1);
  expect(messages[0].body).toContain(reason);
  expect(messages[0].body).toContain(`"task_id": ${taskID}`);
  expect(messages[0].body).toContain(`"project_id": "${project}"`);
  const sessions = (await mc(["homie", "list"])).sessions as { id: string; packet?: unknown }[];
  expect(sessions.find(row => row.id === id)?.packet).toBeNull();
  expect((await mc(["homie", "history", prior])).messages).toEqual([]);
  expect((await mc(["task", "get", String(taskID)])).blocked).toBe(1);
  await page.screenshot({ path: evidencePath("diagnostic-homie-conversation.png"), animations: "disabled" });
}, 60000);

test("a connection issue retains its dialog and reuses a message after a lost response", async () => {
  const { page } = smoke;
  await page.setViewportSize({ width: 1440, height: 1000 });
  conditions = [{ id: "connection:work:model_access:work", group: "connections", name: "Work connection", state: "stopped",
    condition: "Model access failed.", connection_id: "work", profile_id: "work", diagnostic_id: 37,
    roles: ["worker"], http_status: 403, action: "Review connection", latest_failure_at: "2026-10-20T09:00:00Z" }];
  await page.getByTestId("tab-board").click();
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await page.getByTestId("system-notices").getByRole("button", { name: "View details" }).click();
  const dialog = page.getByRole("dialog", { name: "Issue details" });
  let starts = 0, sends = 0;
  await page.route("**/api/sessions", async route => {
    if (route.request().method() === "POST") starts++;
    await route.continue();
  });
  await page.route("**/api/sessions/*/send", async route => {
    sends++;
    await route.fetch();
    await route.fulfill({ status: 503, json: { error: { message: "The response was lost." } } });
  });
  await dialog.getByTestId("diagnostic-homie").click();
  await dialog.getByRole("alert").getByText(/The response was lost/).waitFor();
  expect(page.url()).toEndWith("#board");
  await dialog.getByTestId("diagnostic-homie").click();
  const id = await selectedConversation();
  expect([starts, sends]).toEqual([1, 1]);
  const messages = (await mc(["homie", "history", id])).messages as { body: string }[];
  expect(messages).toHaveLength(1);
  expect(messages[0].body).toContain('"diagnostic_id": 37');
  expect(messages[0].body).toContain('"connection_id": "work"');
  expect(messages[0].body).not.toContain('"task_id"');
  await page.unroute("**/api/sessions");
  await page.unroute("**/api/sessions/*/send");
}, 60000);

test("an environment failure submits the Project and operation references", async () => {
  const { page } = smoke;
  await page.route("**/api/environment/get", async route => {
    const response = await route.fetch();
    const view = await response.json();
    view.environment.phase = "failed";
    view.environment.active_operation_id = null;
    view.environment.runtime_condition = "The selected image lacks a required library.";
    view.operations.unshift({ id: "environment-failure", revision_id: 12, status: "failed", kind: "runtime-check" });
    await route.fulfill({ json: view });
  });
  await page.evaluate(id => { location.hash = `#environment/${id}`; }, project);
  await page.getByTestId("environment-ask-homie").click();
  const id = await selectedConversation();
  const messages = (await mc(["homie", "history", id])).messages as { body: string }[];
  expect(messages[0].body).toContain('"operation_id": "environment-failure"');
  expect(messages[0].body).toContain(`"project_id": "${project}"`);
  expect(messages[0].body).toContain("The selected image lacks a required library.");
  await page.unroute("**/api/environment/get");
}, 60000);

test("a setup failure opens Homie from the Setup Journal", async () => {
  const { page } = smoke;
  await page.route("**/api/environment/get", async route => {
    const response = await route.fetch();
    const view = await response.json();
    Object.assign(view.environment, { phase: "failed", setup_status: "failed", selected_revision_id: null, active_operation_id: null });
    view.results = [];
    view.operations = [{ id: "setup-failure", revision_id: 12, status: "failed", kind: "setup", metadata: {},
      error: "The setup agent could not install the required tool." }];
    await route.fulfill({ json: view });
  });
  await page.getByTestId("tab-board").click();
  await page.getByTestId("setup-ask-homie").click();
  const id = await selectedConversation();
  const messages = (await mc(["homie", "history", id])).messages as { body: string }[];
  expect(messages[0].body).toContain('"operation_id": "setup-failure"');
  expect(messages[0].body).toContain("The setup agent could not install the required tool.");
  await page.unroute("**/api/environment/get");
}, 60000);

test("run diagnostics submit a short failure and retain the run reference", async () => {
  const { page } = smoke;
  await page.route("**/api/logs/runs", route => route.fulfill({ json: { runs: ["failed-run"] } }));
  await page.route("**/api/logs/run/failed-run?*", route => route.fulfill({ json: {
    run_id: "failed-run", exists: true, lines: ["Full output remains in the diagnostic view."],
    records: [{ level: "error", msg: "The agent process exited before completion." }],
  } }));
  await page.evaluate(() => { location.hash = "#logs/run/failed-run"; });
  await page.getByTestId("run-ask-homie").click();
  const id = await selectedConversation();
  const messages = (await mc(["homie", "history", id])).messages as { body: string }[];
  expect(messages[0].body).toContain('"run_id": "failed-run"');
  expect(messages[0].body).toContain("The agent process exited before completion.");
  expect(messages[0].body).not.toContain("Full output remains");
  await page.unroute("**/api/logs/runs");
  await page.unroute("**/api/logs/run/failed-run?*");
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror"))).toEqual([]);
}, 60000);
