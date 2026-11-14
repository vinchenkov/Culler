import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke, openSpine } from "./harness";

let smoke: Smoke;
const tasks: number[] = [];
const run = "proposal-review-smoke";
let identity: string;

beforeAll(async () => {
  smoke = await bootSmoke();
  // The title is wider than a Pipeline graph node.
  await seedProject(smoke, "proposal-review", { title: "Proposal review with a long Project title", environmentReady: true });
  const db = openSpine(smoke);
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    for (let index = 1; index <= 3; index++) {
      const added = db.run("INSERT INTO tasks(title,status,project,description) VALUES (?,'proposed','proposal-review','The result meets the Project requirements.')", [`Proposal ${index}`]);
      tasks.push(Number(added.lastInsertRowid));
    }
  } finally { db.close(); }
  identity = join(smoke.scratch, "arbiter-identity.json");
  writeFileSync(identity, JSON.stringify({ run_id: run, tier: "pipeline", role: "arbiter", pool_ids: tasks.slice(0, 2) }));
  mkdirSync(join(smoke.home, "health"), { recursive: true });
  writeFileSync(join(smoke.home, "health", "resident-availability.json"), JSON.stringify({ observed_at: new Date().toISOString(), can_commit: true }));
  await gotoReady(smoke);
  await selectProject(smoke, "proposal-review");
}, 60000);

afterAll(async () => { await teardown(smoke); });

test("each assigned proposal shows one shared run and remains read-only", async () => {
  const { page } = smoke;
  const card = (id: number) => page.locator(`[data-testid="board-card"][data-task="${id}"]`);
  const open = async (id: number) => {
    await card(id).getByTestId("board-card-open").click();
    await page.getByTestId("board-record-controls").waitFor();
    for (const action of ["edit", "revise", "cancel", "block", "unblock", "resume"]) {
      expect(await page.getByTestId(`board-${action}`).count()).toBe(0);
    }
  };
  await open(tasks[0]);
  await page.getByTestId("task-dialog-close").click();
  const db = openSpine(smoke);
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.run("INSERT INTO runs(id,tier,role,project,subject,pool_snapshot,binding) VALUES (?,'pipeline','arbiter','proposal-review',NULL,?,'fake/fake')", [run, JSON.stringify(tasks.slice(0, 2))]);
    db.run("UPDATE leases SET run_id=?,subject=NULL,owner='arbiter',acquired_at=datetime('now'),last_heartbeat_at=datetime('now'),boot_id='smoke',acquired_uptime_ms=0,heartbeat_uptime_ms=0,deadline_uptime_ms=3600000 WHERE project='proposal-review'", [run]);
  } finally { db.close(); }
  await page.reload();
  await card(tasks[0]).getByText("Agent launch pending", { exact: true }).waitFor();
  expect(await page.getByTestId("lease-badge").innerText()).toContain("Arbiter agent launch pending");
  await page.getByTestId("tab-pipeline").click();
  const node = page.getByTestId("topology-node-arbiter");
  await node.getByText("Agent launch pending", { exact: false }).waitFor();
  expect(await node.locator(".line").innerText()).toContain("on Proposal review");
  expect(await node.evaluate((card) => {
    const box = card.getBoundingClientRect();
    const lines = [...card.querySelectorAll(".name, .role-purpose, .state, .line, .role-count")].map(text => text.getBoundingClientRect());
    return lines.every(line => line.left >= box.left && line.right <= box.right && line.top >= box.top && line.bottom <= box.bottom) &&
      lines.every((line, i) => lines.every((other, j) => i === j || line.bottom <= other.top || other.bottom <= line.top));
  })).toBe(true);
  await page.getByTestId("tab-collie").click();
  await page.getByTestId("lease-badge").getByText("1 agent launch pending", { exact: false }).waitFor();
  expect(await page.getByTestId("lease-badge").innerText()).toContain("1 agent launch pending");
  await mc(["run", "process-started", run], { MC_RUN_JSON: identity });
  await page.getByTestId("tab-board").click();
  await page.reload();
  await card(tasks[0]).getByText("Under Arbiter proposal review", { exact: false }).waitFor();
  expect(await page.getByTestId("lease-badge").innerText()).toContain("Arbiter reviews 2 proposals · 1 agent run");
  expect(await page.getByTestId("board-stats").getByTestId("stat-active").locator("b").innerText()).toBe("1");
  // Each stat leads with its number, in larger and heavier type than its caption.
  expect(await page.getByTestId("board-stats").getByTestId("stat-active").evaluate((node) => {
    const [value, caption] = [...node.children].map((child) => getComputedStyle(child));
    return node.firstElementChild?.tagName === "B" && parseFloat(value.fontSize) > parseFloat(caption.fontSize)
      && Number(value.fontWeight) > Number(caption.fontWeight);
  })).toBe(true);
  for (const id of tasks.slice(0, 2)) {
    expect(await card(id).innerText()).toContain("Under Arbiter proposal review");
    expect(await card(id).getByTestId("board-card-running").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
    await open(id);
    await page.getByTestId(`task-run-diagnostics-${run}`).waitFor();
    expect(await page.getByTestId("task-dialog").innerText()).toContain("Arbiter proposal review");
    await page.getByTestId("task-dialog-close").click();
  }
  expect(await card(tasks[2]).innerText()).not.toContain("Under Arbiter proposal review");
  await open(tasks[2]);
  expect(await page.getByTestId(`task-run-diagnostics-${run}`).count()).toBe(0);
  await page.getByTestId("task-dialog-close").click();
  await page.screenshot({ path: evidencePath("shared-proposal-review.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  for (const id of tasks.slice(0, 2)) {
    expect(await card(id).getByTestId("board-card-running").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  }
  const badge = page.getByTestId("lease-badge");
  expect(await badge.locator(".text").textContent()).toBe("Arbiter reviews 2 proposals · 1 agent run");
  expect(await badge.getAttribute("title")).toContain(run);
  expect(await badge.evaluate(node => {
    const text = node.querySelector(".text")!;
    const style = getComputedStyle(text);
    const outer = node.getBoundingClientRect();
    const inner = text.getBoundingClientRect();
    return style.overflow === "hidden" && style.textOverflow === "ellipsis" && style.whiteSpace === "nowrap" &&
      inner.left >= outer.left && inner.right <= outer.right && inner.top >= outer.top && inner.bottom <= outer.bottom;
  })).toBe(true);
  await page.screenshot({ path: evidencePath("shared-proposal-review-phone.png") });
  await page.setViewportSize({ width: 1280, height: 720 });
}, 60000);

test("one shared issue retains the proposal links and preserves an independent task block", async () => {
  const { page } = smoke;
  const question = "Which Project requirement applies to these proposals? Both proposals require the complete test suite.\n\nThe container cannot execute the host checks. The operator must decide whether those checks require a separate prerequisite.";
  const result = await mc(["complete", "--run", run, "--needs-operator", "--reason", question], { MC_RUN_JSON: identity });
  await mc(["run", "stopped", run]);
  mkdirSync(join(smoke.home, "health"), { recursive: true });
  writeFileSync(join(smoke.home, "health", "resident-availability.json"), JSON.stringify({ observed_at: new Date().toISOString(), can_commit: true }));
  await page.reload();
  const card = (id: number) => page.locator(`[data-testid="board-card"][data-task="${id}"]`);
  await card(tasks[0]).getByText("Awaits shared proposal decision", { exact: true }).waitFor();
  for (const id of tasks.slice(0, 2)) {
    expect(await card(id).innerText()).toContain("Awaits shared proposal decision");
    expect(await card(id).innerText()).not.toContain("Under Arbiter proposal review");
    expect(await mc(["task", "get", String(id)])).toMatchObject({ status: "proposed", blocked: 1, stalled: 0, dispatch_retries: 3 });
  }
  const notices = page.getByTestId("system-notices");
  expect(await notices.getByRole("button", { name: "Resolve proposal decision", exact: true }).count()).toBe(1);
  expect(await notices.getByRole("button", { name: "Review issues", exact: true }).count()).toBe(0);
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-system").click();
  expect(await page.locator("#system-health-summary").innerText()).toBe("1 issue needs attention");
  expect(await page.locator("#system-execution .system-condition").count()).toBe(1);
  await page.getByTestId("tab-board").click();
  for (const id of tasks.slice(0, 2)) {
    await notices.getByRole("button", { name: "View details", exact: true }).click();
    const detail = page.getByRole("dialog", { name: "Issue details", exact: true });
    expect(await detail.locator(".system-cause").textContent()).toBe(question);
    await detail.getByRole("button", { name: `Proposal #${id}`, exact: true }).click();
    await detail.waitFor({ state: "hidden" });
    await page.getByTestId("task-dialog").waitFor();
    expect(await page.getByTestId("task-dialog").innerText()).toContain(`Proposal ${tasks.indexOf(id) + 1}`);
    const sharedDecision = page.getByTestId("task-shared-proposal-decision");
    await sharedDecision.focus();
    const refreshed = page.waitForResponse("**/api/system/status");
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await refreshed;
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)));
    expect(await sharedDecision.evaluate(node => node === document.activeElement)).toBe(true);
    await sharedDecision.click();
    await page.getByRole("dialog", { name: "Issue details", exact: true }).waitFor();
    expect(await page.getByRole("dialog", { name: "Issue details", exact: true }).locator(".system-cause").textContent()).toBe(question);
    await page.keyboard.press("Escape");
  }
  const added = await mc(["task", "add", "Resolve the independent task requirement", "--project", "proposal-review"]);
  const independentTask = Number(added.task_id);
  await mc(["task", "block", String(independentTask), "--reason", "The operator must confirm this separate task requirement."]);
  await page.reload();
  await notices.getByText("2 issues need attention", { exact: true }).waitFor();
  expect(await page.getByTestId("stat-blocked").innerText()).toContain("Blocked tasks");
  await page.getByTestId("stat-blocked").locator("b").filter({ hasText: /^3$/ }).waitFor();
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-system").click();
  expect(await page.locator("#system-health-summary").innerText()).toBe("2 issues need attention");
  expect(await page.locator("#system-execution .system-condition").count()).toBe(2);
  await page.getByTestId("tab-board").click();
  await page.getByTestId("system-notices").getByRole("button", { name: "Review issues" }).click();
  const issues = page.getByRole("dialog", { name: "System issues", exact: true });
  expect(await issues.locator(".system-condition").count()).toBe(2);
  expect(await issues.locator(`[data-condition-id="task:${independentTask}"]`).count()).toBe(1);
  for (const id of tasks.slice(0, 2)) {
    expect(await issues.locator(`[data-condition-id="task:${id}"]`).count()).toBe(0);
    expect(await issues.getByRole("button", { name: `Proposal #${id}`, exact: true }).count()).toBe(1);
  }
  await page.screenshot({ path: evidencePath("shared-proposal-issues.png"), animations: "disabled" });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await issues.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("shared-proposal-issues-phone.png"), animations: "disabled" });
  await page.setViewportSize({ width: 1280, height: 720 });
  await issues.getByRole("button", { name: "Resolve proposal decision", exact: true }).click();
  const decision = page.getByRole("dialog", { name: "Resolve proposal decision", exact: true });
  expect(await decision.locator("form > p").first().textContent()).toBe(question);
  await decision.getByRole("textbox", { name: "Decision resolution" }).fill("Use the current Project requirements.");
  const response = page.waitForResponse("**/api/system/resolve-proposals");
  await decision.getByRole("button", { name: "Resolve decision", exact: true }).click();
  expect((await response).ok()).toBe(true);
  await decision.waitFor({ state: "hidden" });
  await page.keyboard.press("Escape");
  for (const id of tasks.slice(0, 2)) {
    const task = await mc(["task", "get", String(id)]);
    expect(task).toMatchObject({ status: "proposed", blocked: 0, stalled: 0, dispatch_retries: 3 });
    expect((task.run_history as Array<{ id: string }>).map(row => row.id)).toEqual([run]);
    await card(id).getByTestId("board-card-open").click();
    await page.getByTestId(`task-run-diagnostics-${run}`).waitFor();
    await page.getByTestId("task-dialog-close").click();
  }
  expect(await mc(["task", "get", String(independentTask)])).toMatchObject({ status: "seeded", blocked: 1 });
  expect(await notices.getByRole("button", { name: "Resolve proposal decision", exact: true }).count()).toBe(0);
  expect(await notices.getByRole("button", { name: "Open task", exact: true }).count()).toBe(1);
  expect(Number(result.proposal_decision_id)).toBeGreaterThan(0);
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

test("separate Project infrastructure resumes preserve independent task controls", async () => {
  const { page } = smoke;
  const added = await mc(["task", "add", "Retain the independent task decision", "--project", "proposal-review"]);
  const task = Number(added.task_id);
  const fixture = openSpine(smoke);
  try {
    fixture.run("UPDATE projects SET status='paused' WHERE id='proposal-review'");
    fixture.run("UPDATE tasks SET dispatch_retries=2,blocked=1,blocked_kind='operator',blocked_reason='The task requires a separate operator decision.' WHERE id=?", [task]);
  } finally { fixture.close(); }
  const requests: string[] = [];
  for (let count = 0; count < 2; count++) {
    const status = page.waitForResponse(async response => {
      if (!response.url().endsWith("/api/system/status") || !response.ok()) return false;
      const result = await response.json();
      return result.conditions?.some((condition: { id: string; action: string }) =>
        condition.id === "infrastructure:proposal-review" && condition.action === "Resume Project infrastructure");
    });
    const db = openSpine(smoke);
    try {
      db.run("UPDATE projects SET infrastructure_failure_count=3,infrastructure_next_retry_at=NULL,infrastructure_last_failed_run_id=NULL,infrastructure_condition='The runner image is unavailable.' WHERE id='proposal-review'");
    } finally { db.close(); }
    if (count === 0) await page.reload();
    await status;
    await page.getByTestId("system-notices").getByRole("button", { name: "Review issues" }).click();
    const resume = page.getByRole("button", { name: "Resume Project infrastructure", exact: true });
    await resume.waitFor();
    const response = page.waitForResponse("**/api/system/resume-infrastructure");
    await resume.click();
    const completed = await response;
    expect(completed.ok()).toBe(true);
    requests.push(completed.request().postDataJSON().request_id);
    const current = openSpine(smoke);
    try {
      expect(current.query("SELECT infrastructure_failure_count,status FROM projects WHERE id='proposal-review'").get())
        .toEqual({ infrastructure_failure_count: 0, status: "paused" });
    } finally { current.close(); }
    expect(await mc(["task", "get", String(task)]))
      .toMatchObject({ status: "seeded", blocked: 1, stalled: 0, dispatch_retries: 2 });
  }
  expect(requests[0]).not.toBe(requests[1]);
}, 60000);
