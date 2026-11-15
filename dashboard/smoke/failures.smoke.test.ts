import { afterAll, beforeAll, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import { evidencePath, bootSmoke, seedProject, gotoReady, mc, selectProject, teardown, type Smoke, openSpine } from "./harness";
import { recordCurrentWiki } from "./wiki-fixture";

let smoke: Smoke;
const taskIDs: number[] = [];

beforeAll(async () => {
  smoke = await bootSmoke();
  for (const project of ["failure-alpha", "failure-beta"]) {
    const workspace = join(smoke.scratch, project);
    mkdirSync(workspace);
    writeFileSync(join(workspace, "VISION.md"), "# Test project\n\nThe project proves task failure recovery.\n");
    const git = (...args: string[]) => execFileSync("git", args, { cwd: workspace, env: process.env, stdio: "pipe" });
    git("init", "-q", "--initial-branch=main");
    git("add", "VISION.md");
    git("-c", "user.name=Smoke", "-c", "user.email=smoke@example.test", "-c", "commit.gpgsign=false", "commit", "-qm", "Create the scratch project");
    await seedProject(smoke, project, { workspace, environmentReady: true });
    // The Project wiki is current, so a tick starts no wiki update.
    recordCurrentWiki(join(smoke.home, "spine.db"), project, git("rev-parse", "HEAD").toString().trim(), new Date());
    // The fake harness mounts the scratch workspace directly.
    const allowlist = join(smoke.home, "mount-allowlist");
    const source = readFileSync(allowlist, "utf8");
    const artifactPath = JSON.stringify(join(workspace, ".mc-artifacts"));
    expect(source).toContain(artifactPath);
    writeFileSync(allowlist, source.replace(artifactPath, JSON.stringify(workspace)));
  }
  for (const [index, project] of ["failure-alpha", "failure-beta", "failure-beta"].entries()) {
    const row = await mc(["task", "add", `Failure task ${index + 1}`, "--project", project]);
    const id = Number(row.task_id);
    taskIDs.push(id);
    if (index === 0) {
      await mc(["task", "block", String(id), "--reason", "The operator must select the scope for task 1."]);
    } else {
      for (let count = 1; count <= 3; count++) {
        const retry = openSpine(smoke);
        try { retry.run("UPDATE tasks SET run_retry_at='2000-01-01T00:00:00Z' WHERE id=?", [id]); } finally { retry.close(); }
        let runID: string | undefined;
        const observations: unknown[] = [];
        for (let tick = 0; tick < 3 && runID === undefined; tick++) {
          const result = await mc(["dispatch"]);
          const effects = result.actions as Array<Record<string, unknown>>;
          observations.push({ idle: result.idle, error: result.error, effects: effects.map(({ action, role, subject_id, code, consequence }) => ({ action, role, subject_id, code, consequence })) });
          for (const effect of effects) {
            if (effect.action === "reap") await mc(["run", "stopped", String(effect.run_id)]);
          }
          const spawn = effects.find((effect) => effect.action === "spawn" && effect.subject_id === id);
          if (spawn) runID = String(spawn.run_id);
        }
        if (runID === undefined) throw new Error(`The smoke task ${id} received no assigned run for attempt ${count}: ${JSON.stringify(observations)}`);
        await processStarted(runID, "worker");
        const report = ["run", "infra-failure", runID, "--condition", "runner-exited", "--source", "runner"];
        if (index === 1 && count === 3) {
          report.splice(0, report.length,
            "run", "infra-failure", runID, "--condition", "The service refused the request.", "--source", "agent",
            "--cause", "quota", "--evidence", "The service reports quota exhaustion.",
            "--operator-action", "Wait for the service quota reset.", "--quota-reset-at", "2026-09-08T12:00:00Z");
        }
        await mc(report);
        const task = await mc(["task", "get", String(id)]);
        expect(task.infrastructure_failure_count).toBe(count);
      }
    }
  }
  await gotoReady(smoke);
}, 60000);

async function processStarted(runID: string, role: string) {
  const identity = join(smoke.scratch, `${runID}-identity.json`);
  writeFileSync(identity, JSON.stringify({ run_id: runID, tier: "pipeline", role }));
  await mc(["run", "process-started", runID], { MC_RUN_JSON: identity });
}

afterAll(async () => {
  await teardown(smoke);
}, 30000);

test("the board shows task failures and recovery through task cards", async () => {
  const { page } = smoke;
  const card = (id: number) => page.locator(`[data-testid="board-card"][data-task="${id}"]`);
  await card(taskIDs[0]).waitFor({ timeout: 15000 });
  expect(await page.locator('[data-testid="board-attention"]').count()).toBe(0);
  expect(await card(taskIDs[0]).textContent()).toContain("Decision Pending");
  await card(taskIDs[0]).getByTestId("board-card-open").click();
  await page.getByText("This task requires an operator decision or repair before work can continue.").waitFor();
  expect(await page.getByTestId("task-dialog").innerText()).toContain("The operator must select the scope for task 1.");
  await page.getByTestId("task-dialog-close").click();

  await selectProject(smoke, "failure-alpha");
  await card(taskIDs[0]).waitFor();
  expect(await page.locator('[data-testid="board-card"]').count()).toBe(1);
  await selectProject(smoke, "failure-beta");
  await card(taskIDs[1]).waitFor();
  expect(await page.locator('[data-testid="board-card"]').count()).toBe(2);
  expect(await card(taskIDs[2]).textContent()).toContain("Retries Exhausted");
  expect(await card(taskIDs[2]).textContent()).not.toContain("infra-failure");
  expect(await card(taskIDs[2]).textContent()).not.toContain("3/3");
  await card(taskIDs[2]).locator('[data-testid="board-card-open"]').click();

  const dialog = page.locator('[data-testid="task-dialog"][open]');
  await dialog.locator('[data-testid="task-dialog-failure"]').waitFor({ timeout: 15000 });
  expect(await dialog.textContent()).toContain("Retries Exhausted");
  expect(await dialog.textContent()).toContain("Stopping");
  expect(await dialog.textContent()).not.toContain("Running now");
  expect(await page.getByTestId("board-stats").getByTestId("stat-active").locator("b").textContent()).toBe("0");
  expect(await dialog.textContent()).toContain("The cause is unknown.");
  expect(await dialog.getByTestId("task-dialog-retry").innerText()).toContain("Automatic retries stopped.");
  expect(await dialog.textContent()).toContain("runner-exited");
  const runFailures = dialog.locator('[data-testid="task-dialog-facts"] .kv').filter({ hasText: "Task run failures" });
  expect(await runFailures.locator(".v").innerText()).toBe("3 of 3");
  // The tooltip opens from the keyboard and explains the count.
  await runFailures.locator(".fact-tip").focus();
  await runFailures.getByRole("tooltip").waitFor({ state: "visible" });
  expect(await runFailures.getByRole("tooltip").innerText()).toContain("After 3 failures, the task stops until you resume it.");
  await Bun.sleep(400);
  await page.screenshot({ path: evidencePath("task-dialog-run-failures-tip.png") });
  expect(await dialog.locator('[data-testid="task-dialog-facts"]').textContent()).toContain("Seeded");
  await dialog.locator('[data-testid="task-dialog-close"]').click();

  await card(taskIDs[1]).locator('[data-testid="board-card-open"]').click();
  await dialog.getByText("The cause is unknown.", { exact: true }).waitFor({ timeout: 15000 });
  await dialog.getByText("Failure evidence and report source", { exact: true }).click();
  expect(await dialog.textContent()).toContain("The service reports quota exhaustion.");
  expect(await dialog.textContent()).not.toContain("Wait for the service quota reset.");
  expect(await dialog.textContent()).not.toContain("2026-09-08T12:00:00Z");
  await dialog.locator('[data-testid="task-dialog-close"]').click();

  await mc(["task", "resume", String(taskIDs[2]), "--reason", "The operator restored the service."]);
  await page.reload();
  await card(taskIDs[2]).waitFor({ timeout: 15000 });
  expect(await card(taskIDs[2]).textContent()).not.toContain("Retries Exhausted");
  expect(await card(taskIDs[1]).textContent()).toContain("Retries Exhausted");
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

test("a resolved failure appears only in its run row and opens the exact run diagnostics", async () => {
  const { page } = smoke;
  const id = taskIDs[2];
  const task = await mc(["task", "get", String(id)]);
  if (task.stalled) await mc(["task", "resume", String(id), "--reason", "The service is available for another attempt."]);
  const previous = task.latest_infrastructure_failure as { run_id: string };
  await mc(["run", "stopped", previous.run_id]);
  const dispatched = await mc(["dispatch"]);
  const spawn = (dispatched.actions as Array<Record<string, unknown>>).find(effect => effect.action === "spawn" && effect.subject_id === id);
  expect(spawn).toBeDefined();
  const runID = String(spawn!.run_id);
  await processStarted(runID, "worker");
  await mc(["run", "infra-failure", runID, "--condition", "The verification tool could not start.", "--source", "runner"]);
  await mc(["run", "stopped", runID]);
  const logs = join(smoke.home, "logs", "runs", runID);
  mkdirSync(logs, { recursive: true });
  writeFileSync(join(logs, "runner.ndjson"), JSON.stringify({
    ts: new Date().toISOString(), level: "error", comp: "runner", event: "adapter.launch_failed", msg: "The retry diagnostics identify this run.",
  }) + "\n");
  if (await page.getByTestId("task-dialog").isVisible()) await page.getByTestId("task-dialog-close").click();
  await selectProject(smoke, "failure-beta");
  const card = page.locator(`[data-testid="board-card"][data-task="${id}"]`);
  const dialog = page.getByTestId("task-dialog");
  const history = page.getByTestId(`task-run-diagnostics-${runID}`);
  try {
    await page.reload();
    await card.getByTestId("board-card-open").click();
    await history.waitFor({ timeout: 15000 });
    expect(await history.locator("xpath=..").innerText()).toContain("The agent program stopped");
    expect(await dialog.getByTestId("task-dialog-retry").count()).toBe(0);
    expect(await dialog.getByTestId("task-dialog-failure").count()).toBe(0);
    expect(await dialog.innerText()).not.toContain("infra-failed");
    expect(await dialog.innerText()).not.toContain("The verification tool could not start.");
    expect(await card.innerText()).not.toContain("infra-failure");

    for (const [width, height, name] of [[1440, 1000, "desktop"], [390, 844, "phone"]] as const) {
      await page.setViewportSize({ width, height });
      expect(await dialog.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
      await page.screenshot({ path: evidencePath(`resolved-failure-${name}.png`) });
    }
    await page.setViewportSize({ width: 1280, height: 900 });
    await history.click();
    await page.getByTestId("logs-lines").getByText("The retry diagnostics identify this run.", { exact: false }).waitFor();
    expect(new URL(page.url()).hash).toContain(runID);
    expect(await dialog.isVisible()).toBe(false);
    expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
  } finally {
    await page.setViewportSize({ width: 1280, height: 720 });
  }
}, 90000);

test("a connection wait names the connection on the card, in the dialog, and in the dispatch errors", async () => {
  const { page } = smoke;
  const id = taskIDs[2];
  const connection = "connection-smoke";
  const spine = () => openSpine(smoke);
  const db = spine();
  try {
    db.run("UPDATE projects SET connection_wait=?, connection_wait_task=? WHERE id='failure-beta'", [connection, id]);
    db.run("INSERT INTO activity(actor,kind,subject,detail,project) VALUES ('mc','dispatch.wait_started',?,?,'failure-beta')",
      [connection, `The Worker run for task #${id} waits for this connection. The connection requires attention. Open System health in the dashboard.`]);
  } finally { db.close(); }
  await page.route("**/api/system/status", route => route.fulfill({ json: {
    complete: true, conditions: [],
    connections: [{ id: `connection:${connection}`, group: "connections", state: "stopped", connection_id: connection, name: "Test service" }],
  } }));
  const card = page.locator(`[data-testid="board-card"][data-task="${id}"]`);
  try {
    await page.getByTestId("tab-board").click();
    await selectProject(smoke, "failure-beta");
    await page.reload();
    await card.getByTestId("board-card-wait").getByText("Waits for Test service", { exact: true }).waitFor({ timeout: 15000 });
    await card.getByTestId("board-card-open").click();
    await page.getByTestId("task-dialog-wait")
      .getByText("A connection check stopped the next run of this task. Culler starts the run when Test service is available.", { exact: true })
      .waitFor({ timeout: 15000 });
    await page.getByTestId("task-dialog-close").click();

    await page.getByTestId("tab-pipeline").click();
    await page.getByTestId("activity-tab-errors").click();
    await page.getByTestId("activity-errors").getByText("connection Test service", { exact: false }).waitFor({ timeout: 15000 });
    expect(await page.getByTestId("activity-errors").innerText()).toContain(`The Worker run for task #${id} waits for this connection.`);
    expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
  } finally {
    await page.unroute("**/api/system/status");
    const clean = spine();
    try { clean.run("UPDATE projects SET connection_wait=NULL, connection_wait_task=NULL WHERE id='failure-beta'"); } finally { clean.close(); }
  }
}, 60000);
