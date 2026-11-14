import { afterAll, beforeAll, expect, test } from "bun:test";
import type { Database } from "bun:sqlite";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke, openSpine } from "./harness";

let smoke: Smoke;
let task: number;
const run = "role-map-worker";
const correctionProblem = "The change clears the Project pause.";

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "role-map", { title: "Role map Project", environmentReady: true });
  task = Number((await mc(["task", "add", "Preserve the Project pause state", "--project", "role-map"])).task_id);
  await gotoReady(smoke);
  await selectProject(smoke, "role-map");
}, 60000);

afterAll(async () => { await teardown(smoke); });

function records(change: (db: Database) => void) {
  const db = openSpine(smoke);
  try { db.exec("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000"); change(db); }
  finally { db.close(); }
}

function recordCorrection(db: Database) {
  const worker = "role-map-first-worker";
  const verifier = "role-map-verifier";
  const input = "a".repeat(40);
  const commit = "b".repeat(40);
  db.run(`INSERT INTO runs(id,tier,role,project,subject,binding,ended_at,outcome)
    VALUES (?,'pipeline','worker','role-map',?,'fake/fake',datetime('now','-10 minutes'),'completed')`, [worker, task]);
  db.run(`INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,temporary_bytes,captured,cleaned)
    VALUES (?,?,?,?,0,1,1)`, [worker, task, input, input]);
  db.run("INSERT INTO version_submissions(run_id,task_id,input_commit,result_commit) VALUES (?,?,?,?)", [worker, task, input, commit]);
  const version = db.run(`INSERT INTO task_versions(task_id,run_id,input_commit,result_commit,destination_branch,retained_ref)
    VALUES (?,?,?,?,'culler',?)`, [task, worker, input, commit, `refs/mc/tasks/${task}/versions/${worker}`]);
  const versionID = Number(version.lastInsertRowid);
  db.run(`INSERT INTO runs(id,tier,role,project,subject,binding,ended_at,outcome,verdict_outcome)
    VALUES (?,'pipeline','verifier','role-map',?,'fake/fake',datetime('now','-6 minutes'),'completed','correct')`, [verifier, task]);
  db.run(`INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,version_id,temporary_bytes,captured,cleaned)
    VALUES (?,?,?,?,?,0,1,1)`, [verifier, task, commit, input, versionID]);
  db.run(`INSERT INTO version_verdicts(version_id,run_id,outcome,evidence_path,problem,explanation_evidence,resolution)
    VALUES (?,?,'correct','/mc/artifacts/correction.md',?,'The pause test failed.','Preserve the Project pause.')`,
  [versionID, verifier, correctionProblem]);
  db.run("UPDATE tasks SET verifier_correction_open=1,correction_open=1 WHERE id=?", [task]);
}

async function openTask() {
  await smoke.page.getByTestId("tab-board").click();
  await smoke.page.locator(`[data-testid="board-card"][data-task="${task}"]`).getByTestId("board-card-open").click();
  await smoke.page.getByTestId("board-record-controls").waitFor();
}

/** The role dialog stays centered inside the viewport. */
function centeredRoleDialog() {
  return smoke.page.evaluate(() => {
    const dialog = document.querySelector("dialog.role-dialog:modal")!;
    const box = dialog.getBoundingClientRect();
    return document.documentElement.scrollWidth <= innerWidth && box.left >= 0 && box.right <= innerWidth &&
      box.top >= 0 && box.bottom <= innerHeight && Math.abs(box.left + box.width / 2 - innerWidth / 2) <= 1 &&
      Math.abs(box.top + box.height / 2 - innerHeight / 2) <= 1 && dialog.contains(document.activeElement);
  });
}

/** The contrast ratio of two computed rgb() colors. */
function contrast([ink, surface]: string[]) {
  const luminance = (value: string) => value.match(/[\d.]+/g)!.slice(0, 3).map(Number).map(channel => {
    const c = channel / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  }).reduce((sum, channel, index) => sum + channel * [0.2126, 0.7152, 0.0722][index]!, 0);
  const [a, b] = [luminance(ink!), luminance(surface!)];
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

async function reloadPipeline() {
  await smoke.page.reload();
  await smoke.page.waitForSelector("body[data-ready]");
  await smoke.page.getByTestId("tab-pipeline").click();
  await smoke.page.getByTestId("topology-node-worker").waitFor();
}

test("the role map shows actual run states and retains the role popup", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-pipeline").click();
  await page.getByTestId("pipeline-current-run").getByText("No active task run", { exact: true }).waitFor();
  expect(await page.getByTestId("pipeline-correction").count()).toBe(0);
  records(db => {
    db.run("INSERT INTO runs(id,tier,role,project,subject,binding) VALUES (?,'pipeline','worker','role-map',?,'fake/fake')", [run, task]);
    db.run(`UPDATE leases SET run_id=?,subject=?,owner='worker',acquired_at=datetime('now'),last_heartbeat_at=datetime('now'),
      boot_id='smoke',acquired_uptime_ms=0,heartbeat_uptime_ms=0,deadline_uptime_ms=3600000 WHERE project='role-map'`, [run, task]);
  });
  await reloadPipeline();
  const current = page.getByTestId("pipeline-current-run");
  await current.getByText("Worker · Agent launch pending", { exact: true }).waitFor();
  await current.getByRole("heading", { name: "Preserve the Project pause state", exact: true }).waitFor();
  expect(await current.innerText()).toContain("Preserve the Project pause state");
  records(db => {
    db.run("UPDATE runs SET agent_started_at=datetime('now') WHERE id=?", [run]);
    recordCorrection(db);
  });
  await reloadPipeline();
  await current.getByText("Worker · Running now", { exact: true }).waitFor();
  expect(await page.getByTestId("pipeline-correction").count()).toBe(0);
  expect(await page.getByTestId("topology-graph").innerText()).not.toContain(correctionProblem);
  await page.getByTestId("pipeline-runs").getByText("Requested a correction", { exact: true }).waitFor();
  expect(await page.getByTestId("pipeline-runs").getByText("Requested a correction", { exact: true }).getAttribute("class")).toContain("tone-stone");
  await openTask();
  const dialog = page.getByTestId("task-dialog");
  await dialog.getByText("Requested a correction", { exact: true }).waitFor();
  expect(await dialog.getByText(correctionProblem).count()).toBe(0);
  expect(await dialog.getByTestId("task-ask-collie").count()).toBe(0);
  expect(await dialog.locator(".callout").count()).toBe(0);
  const running = dialog.getByTestId("task-dialog-running");
  expect(await running.innerText()).toContain("Running now");
  for (const theme of ["light", "dark"]) {
    await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
    expect(contrast(await running.evaluate(node => [getComputedStyle(node).color, getComputedStyle(node).backgroundColor])))
      .toBeGreaterThanOrEqual(4.5);
    await page.screenshot({ path: evidencePath(`verifier-correction-task-details-${theme}.png`), animations: "disabled" });
  }
  await page.keyboard.press("Escape");
  await page.getByTestId("tab-pipeline").click();
  for (const theme of ["light", "dark"]) {
    await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
    for (const width of [1440, 1000, 390]) {
      await page.setViewportSize({ width, height: 900 });
      expect(contrast(await current.locator("h3").evaluate(node =>
        [getComputedStyle(node).color, getComputedStyle(node.closest(".pipeline-current")!).backgroundColor])))
        .toBeGreaterThanOrEqual(4.5);
      const activityGap = () => page.evaluate(() =>
        document.querySelector('[data-testid="activity-pane"]')!.getBoundingClientRect().top -
        document.querySelector('[data-testid="topology-graph"]')!.getBoundingClientRect().bottom);
      const closedGap = await activityGap();
      await page.getByTestId("topology-graph").screenshot({ path: evidencePath(`role-eggs-${theme}-${width}.png`), animations: "disabled" });
      const worker = page.getByTestId("topology-node-worker");
      await worker.focus();
      await page.keyboard.press("Enter");
      const popup = page.getByTestId("topology-side");
      await popup.getByTestId("topology-custom-instructions").getByText("None", { exact: true }).waitFor();
      expect(await popup.getByTestId("topology-change-profile").innerText()).toContain("All Projects");
      expect(await popup.getByTestId("topology-custom-instructions").innerText()).toContain("This Project");
      expect(await centeredRoleDialog()).toBe(true);
      expect(await activityGap()).toBe(closedGap);
      await page.screenshot({ path: evidencePath(`role-map-${theme}-${width}.png`), fullPage: true });
      await page.keyboard.press("Escape");
      await popup.waitFor({ state: "detached" });
      expect(await worker.evaluate(node => node === document.activeElement)).toBe(true);
      // The dialog preserves the position of the Activity section.
      expect(await activityGap()).toBe(closedGap);
      // Each role opens a centered dialog.
      for (const role of ["verifier", "packager", "scribe"]) {
        await page.getByTestId(`topology-node-${role}`).click();
        await page.getByTestId("topology-side").waitFor();
        expect(await centeredRoleDialog()).toBe(true);
        await page.screenshot({ path: evidencePath(`role-map-${role}-${theme}-${width}.png`), fullPage: true });
        await page.keyboard.press("Escape");
        await popup.waitFor({ state: "detached" });
        expect(await activityGap()).toBe(closedGap);
      }
    }
  }
  records(db => db.run("UPDATE leases SET last_heartbeat_at=datetime('now','-10 minutes') WHERE project='role-map'"));
  await reloadPipeline();
  await current.getByText("Worker · Heartbeat stopped", { exact: true }).waitFor();
  records(db => {
    db.run("UPDATE tasks SET verifier_correction_open=0 WHERE id=?", [task]);
    db.run("UPDATE runs SET ended_at=datetime('now'),outcome='completed' WHERE id=?", [run]);
  });
  await reloadPipeline();
  await current.getByText("No active task run", { exact: true }).waitFor();
  expect(await page.getByTestId("pipeline-correction").count()).toBe(0);
  await page.getByTestId("pipeline-runs").getByText("Requested a correction", { exact: true }).waitFor();
  await openTask();
  expect(await dialog.getByText(correctionProblem).count()).toBe(0);
  await dialog.getByText("Requested a correction", { exact: true }).waitFor();
  await page.keyboard.press("Escape");
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);


test("Configs role dialogs preserve the table position and return focus", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-agents").click();
  for (const theme of ["light", "dark"]) {
    await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
    for (const width of [1440, 1000, 390]) {
      await page.setViewportSize({ width, height: 900 });
      const button = page.getByTestId("config-route-worker");
      await button.scrollIntoViewIfNeeded();
      const before = await page.locator("#configs-routing").boundingBox();
      await button.click();
      await page.getByTestId("topology-route-picker").waitFor();
      expect(await centeredRoleDialog()).toBe(true);
      expect(await page.locator("#configs-routing").boundingBox()).toEqual(before);
      await page.screenshot({ path: evidencePath(`configs-role-${theme}-${width}.png`), animations: "disabled" });
      await page.keyboard.press("Escape");
      await page.getByTestId("topology-side").waitFor({ state: "detached" });
      expect(await button.evaluate(node => node === document.activeElement)).toBe(true);
      await button.click();
      await page.getByTestId("topology-side").getByRole("button", { name: "Close", exact: true }).click();
      await page.getByTestId("topology-side").waitFor({ state: "detached" });
      expect(await button.evaluate(node => node === document.activeElement)).toBe(true);
    }
  }
}, 60000);
