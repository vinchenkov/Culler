import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { join } from "node:path";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import type { Locator } from "playwright";
import { REVISION_WAIT } from "../src/ui/boardwrites.js";
import { evidencePath, bootSmoke, seedProject, gotoReady, mc, selectProject, SMOKE_BEARER, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket, seedWorkerSubmission } from "./delivery-fixture";

let smoke: Smoke;
let taskID = 0;

beforeAll(async () => {
  smoke = await bootSmoke();
  mkdirSync(evidencePath("collie-board-evidence"), { recursive: true });
  for (const id of ["board-alpha", "board-beta"]) {
    await seedProject(smoke, id, { title: id === "board-alpha" ? "Alpha Site" : "Beta Site", environmentReady: true });
  }
  await gotoReady(smoke);
  await selectProject(smoke, "board-alpha");
}, 60000);

afterAll(async () => { await teardown(smoke); });
afterEach(async () => {
  if (await smoke.page.getByTestId("board-write-dialog").isVisible()) await smoke.page.keyboard.press("Escape");
  await closeTask();
  await smoke.page.setViewportSize({ width: 1280, height: 900 });
});

async function write(body: Record<string, unknown>) {
  const response = await fetch(`${smoke.server.url}api/board/write`, {
    method: "POST", headers: { ...SMOKE_BEARER, "content-type": "application/json" },
    body: JSON.stringify({ operation_id: crypto.randomUUID(), ...body }),
  });
  if (!response.ok) throw new Error(await response.text());
  return await response.json() as any;
}

async function openTask(id: number) {
  const { page } = smoke;
  await closeTask();
  await page.getByTestId("tab-board").click();
  await page.locator(`[data-testid="board-card"][data-task="${id}"]`).getByTestId("board-card-open").click();
  await page.getByTestId("board-record-controls").waitFor();
}

async function submit() {
  const response = smoke.page.waitForResponse((response) => response.url().endsWith("/api/board/write"));
  await smoke.page.getByTestId("board-write-submit").click();
  const result = await response;
  if (!result.ok()) throw new Error(await result.text());
  await smoke.page.getByTestId("board-write-dialog").waitFor({ state: "hidden" });
  return await result.json();
}

/** A refresh of the open task keeps the loaded report document and its read position. */
async function refreshKeepsReport(frameNode: Locator, id: number) {
  const document = async () => {
    const frame = await (await frameNode.elementHandle())?.contentFrame();
    if (!frame) throw new Error("The report frame has no document.");
    return frame;
  };
  const position = await (await document()).evaluate(() => {
    window.document.documentElement.dataset.smokeRead = "1";
    window.scrollTo(0, 400);
    return window.scrollY;
  });
  const refreshed = smoke.page.waitForResponse((response) => response.url().endsWith(`/api/tasks/${id}`));
  await smoke.page.evaluate(() => window.document.dispatchEvent(new Event("visibilitychange")));
  await refreshed;
  await Bun.sleep(500);
  expect(await (await document()).evaluate(() => [window.document.documentElement.dataset.smokeRead ?? "", window.scrollY]))
    .toEqual(["1", position]);
}

async function closeTask() {
  const dialog = smoke.page.getByTestId("task-dialog");
  if (await dialog.isVisible()) await smoke.page.keyboard.press("Escape");
  await dialog.waitFor({ state: "hidden" });
}

test("the board creates tasks with the selected Project", async () => {
  const { page } = smoke;
  await page.getByTestId("board-create").click();
  expect(await page.getByTestId("board-create-explanation").innerText())
    .toBe("Create a new task here, or ask Collie to do it for you.");
  expect(await page.getByTestId("board-write-destination").innerText()).toBe("Project: Alpha Site");
  expect(await page.getByTestId("board-write-priority").inputValue()).toBe("2");
  await page.getByTestId("board-write-title").fill("Browser task");
  await page.getByTestId("board-write-priority").selectOption("-1");
  await page.screenshot({ animations: "disabled", path: evidencePath("collie-board-evidence/create-desktop.png"), fullPage: false });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ animations: "disabled", path: evidencePath("collie-board-evidence/create-narrow.png"), fullPage: false });
  expect(await page.getByTestId("board-write-dialog").evaluate((node) => node.scrollWidth <= window.innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 900 });
  taskID = Number((await submit()).task_id);
  await page.getByTestId("board-record-controls").waitFor();
  const task = await mc(["task", "get", String(taskID)]);
  expect(task).toMatchObject({ title: "Browser task", project: "board-alpha", priority: -1, origin: "user", status: "seeded" });
  await closeTask();
  await selectProject(smoke, "board-beta");
  expect(await page.getByTestId("board-card").count()).toBe(0);
  await page.getByTestId("board-create").click();
  expect(await page.getByTestId("board-write-destination").innerText()).toBe("Project: Beta Site");
  await page.getByTestId("board-write-keep").click();
  await selectProject(smoke, "board-alpha");
}, 60000);

test("the Collie creation link navigates without a message or new board record", async () => {
  const { page } = smoke;
  const before = (await mc(["task", "list"])).tasks as any[];
  let messages = 0;
  const observe = (request: any) => { if (/\/api\/sessions\/[^/]+\/send$/.test(request.url())) messages++; };
  page.on("request", observe);
  try {
    await page.getByTestId("board-create").click();
    await page.getByTestId("board-create-collie").click();
    await page.getByTestId("collie-pane").waitFor({ state: "visible" });
    expect(await page.getByTestId("board-write-dialog").isVisible()).toBe(false);
    expect(messages).toBe(0);
    expect(((await mc(["task", "list"])).tasks as any[]).length).toBe(before.length);
  } finally { page.off("request", observe); }
  await page.getByTestId("tab-board").click();
}, 30000);

test("a lost response and reload reuse the exact creation request", async () => {
  const { page } = smoke;
  const operationIDs: string[] = [];
  const pattern = "**/api/board/write";
  await page.route(pattern, async (route) => {
    operationIDs.push(route.request().postDataJSON().operation_id);
    if (operationIDs.length === 1) {
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      await route.abort("failed");
    } else await route.continue();
  });
  try {
    await page.getByTestId("board-create").click();
    await page.getByTestId("board-write-title").fill("One creation after a lost response");
    await page.getByTestId("board-write-submit").click();
    await page.getByTestId("board-write-error").waitFor();
    expect(await page.getByTestId("board-write-title").inputValue()).toBe("One creation after a lost response");
    await page.reload();
    await page.getByTestId("board-write-dialog").waitFor({ state: "visible" });
    expect(await page.getByTestId("board-write-title").inputValue()).toBe("One creation after a lost response");
    await submit();
    await page.getByTestId("board-record-controls").waitFor();
    expect(operationIDs).toHaveLength(2);
    expect(operationIDs[0]).toBe(operationIDs[1]);
    const tasks = (await mc(["task", "list"])).tasks as any[];
    expect(tasks.filter((row) => row.title === "One creation after a lost response")).toHaveLength(1);
  } finally { await page.unroute(pattern); }
  await closeTask();
}, 30000);

test("a concurrent edit preserves unsaved values and requires the current version", async () => {
  const { page } = smoke;
  await openTask(taskID);
  await page.getByTestId("board-edit").click();
  await page.getByTestId("board-write-title").fill("The operator's local title");
  await page.getByTestId("board-write-priority").selectOption("0");
  const row = await mc(["task", "get", String(taskID)]);
  await write({ action: "edit", task_id: taskID, expected_version: row.record_version, title: "A concurrent title" });
  const response = page.waitForResponse((response) => response.url().endsWith("/api/board/write"));
  await page.getByTestId("board-write-submit").click();
  expect((await response).status()).toBe(409);
  await page.getByTestId("board-write-conflict").waitFor();
  expect(await page.getByTestId("board-write-conflict").innerText()).toContain("A concurrent title");
  expect(await page.getByTestId("board-write-title").inputValue()).toBe("The operator's local title");
  expect(await page.getByTestId("board-write-priority").inputValue()).toBe("0");
  await page.getByTestId("board-write-use-current").click();
  await submit();
  expect(await mc(["task", "get", String(taskID)])).toMatchObject({ title: "The operator's local title", priority: 0, status: "seeded" });
  await page.getByTestId("board-edit").click();
  await page.getByTestId("board-write-keep").click();
  // The dialog restores focus in its close event, which fires after the click.
  await page.waitForFunction(() => document.activeElement?.getAttribute("data-testid") === "board-edit");
  await closeTask();
}, 30000);

test("Resolve decision preserves the reasons and the pipeline stage", async () => {
  const { page } = smoke;
  const current = await mc(["task", "get", String(taskID)]);
  await write({ action: "block", task_id: taskID, expected_version: current.record_version, reason: "The operator must resolve the requirement conflict." });
  await openTask(taskID);
  await page.getByTestId("board-unblock").waitFor();
  expect(await page.getByTestId("board-block").count()).toBe(0);
  const blocked = await mc(["task", "get", String(taskID)]);
  expect(blocked).toMatchObject({ blocked: 1, blocked_kind: "operator", status: "seeded" });
  expect(await page.getByTestId("board-unblock").innerText()).toBe("Resolve decision");
  await page.getByTestId("board-unblock").click();
  // An operator block has the form with the reason field only.
  const dialog = page.getByTestId("board-write-dialog");
  expect(await dialog.getByLabel("What resolved the decision?", { exact: true }).getAttribute("data-testid")).toBe("board-write-reason");
  for (const hook of ["board-write-requirements", "board-resolve-report", "board-resolve-effect", "board-resolve-cancel"]) {
    expect(await page.getByTestId(hook).count()).toBe(0);
  }
  expect(await page.getByTestId("board-write-submit").innerText()).toBe("Resolve decision");
  await page.getByTestId("board-write-reason").fill("The operator supplied the required decision.");
  const request = page.waitForRequest((request) => request.url().endsWith("/api/board/write"));
  await submit();
  expect((await request).postDataJSON()).toMatchObject({ action: "unblock", task_id: taskID, reason: "The operator supplied the required decision." });
  expect((await request).postDataJSON()).not.toHaveProperty("description");
  await page.waitForFunction(() => document.querySelector('[data-testid="board-history"]')?.textContent?.includes("The operator supplied the required decision."));
  await page.getByTestId("board-history").locator(":scope > summary").click();
  const history = await page.getByTestId("board-history").innerText();
  expect(history).toContain("The operator must resolve the requirement conflict.");
  expect(history).toContain("The operator supplied the required decision.");
  expect(await mc(["task", "get", String(taskID)])).toMatchObject({
    blocked: 0, blocked_reason: null, blocked_kind: null, status: "seeded",
    description: blocked.description, requirements_version: blocked.requirements_version,
  });
  await closeTask();
}, 30000);

test("Resolve decision ends an operator decision report through a task revision", async () => {
  const { page } = smoke;
  const description = "The export lists each order.\nThe export uses the date format of the host.";
  const created = await write({ action: "create", scope: "task", title: "Task with an operator decision report", project: "board-alpha", description });
  const id = Number(created.task_id);
  const report = "Decision needed: the host date format contradicts the required ISO dates. Select one date format.";
  const db = openSpine(smoke);
  let submission: { commit: string; input: string };
  try {
    submission = seedWorkerSubmission(db, { taskID: id });
    db.run("UPDATE tasks SET blocked=1,blocked_kind='report',blocked_reason=? WHERE id=?", [report, id]);
  } finally { db.close(); }
  const reported = await mc(["task", "get", String(id)]);
  expect(reported).toMatchObject({ status: "worked", blocked: 1, blocked_kind: "report", base_commit: submission.input });

  // Resolve decision with a reason cannot end an operator decision report.
  const refused = await fetch(`${smoke.server.url}api/board/write`, {
    method: "POST", headers: { ...SMOKE_BEARER, "content-type": "application/json" },
    body: JSON.stringify({ operation_id: crypto.randomUUID(), action: "unblock", task_id: id, expected_version: reported.record_version, reason: "The operator read the report." }),
  });
  expect(refused.status).toBe(400);
  expect(((await refused.json()) as any).error).toMatchObject({
    code: "illegal-transition", message: "Resolve this operator decision report with a task revision, or cancel the task.",
  });
  expect(await mc(["task", "get", String(id)])).toMatchObject({ record_version: reported.record_version, blocked: 1, blocked_kind: "report" });

  await page.reload();
  await page.getByTestId("board-card").first().waitFor();
  await openTask(id);
  expect(await page.getByTestId("board-unblock").innerText()).toBe("Resolve decision");
  await page.getByTestId("board-unblock").click();
  const dialog = page.getByTestId("board-write-dialog");
  expect(await page.getByTestId("board-resolve-report").getAttribute("open")).toBeNull();
  await page.getByTestId("board-resolve-report").getByText("Agent report", { exact: true }).click();
  expect(await page.getByTestId("board-resolve-report").innerText()).toContain(report);
  expect(await page.getByTestId("board-write-requirements").inputValue()).toBe(description);
  expect(await page.getByTestId("board-resolve-effect").innerText()).toContain("The Worker continues from the saved code");
  expect(await dialog.getByLabel("Revision instructions (optional after an edit)", { exact: true }).getAttribute("data-testid")).toBe("board-write-reason");
  expect(await page.getByTestId("board-resolve-alternative").innerText()).toContain("cancel the task");

  // Cancel task is the other resolution. A close of its form changes no record.
  await page.getByTestId("board-resolve-cancel").click();
  expect(await page.getByTestId("board-write-submit").innerText()).toBe("Cancel task");
  expect(await page.getByTestId("board-write-requirements").count()).toBe(0);
  await page.getByTestId("board-write-keep").click();
  await dialog.waitFor({ state: "hidden" });
  expect(await mc(["task", "get", String(id)])).toMatchObject({ record_version: reported.record_version, status: "worked", blocked: 1 });
  await page.getByTestId("board-unblock").click();

  // Either an edit or revision instructions permits a save.
  const save = page.getByTestId("board-write-submit");
  expect(await save.innerText()).toBe("Save and return to Worker");
  expect(await save.isDisabled()).toBe(true);
  expect(await page.getByTestId("board-write-rule").innerText()).toBe("Change the title or description, or add revision instructions.");
  const reason = "The operator selected ISO dates for every export.";
  await page.getByTestId("board-write-reason").fill(reason);
  expect(await save.isEnabled()).toBe(true);
  await page.getByTestId("board-write-reason").fill("");
  await page.getByTestId("board-write-requirements").fill(`  ${description}\n`);
  expect(await save.isDisabled()).toBe(true);
  const revised = "The export lists each order.\nThe export writes each date in the ISO 8601 format.";
  await page.getByTestId("board-write-requirements").fill(revised);
  expect(await save.isEnabled()).toBe(true);
  await page.getByTestId("board-write-reason").fill(reason);
  expect(await page.getByTestId("board-write-rule").isHidden()).toBe(true);
  await page.screenshot({ animations: "disabled", path: evidencePath("collie-board-evidence/resolve-report-desktop.png"), fullPage: false, animations: "disabled" });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await dialog.evaluate((node) => node.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ animations: "disabled", path: evidencePath("collie-board-evidence/resolve-report-narrow.png"), fullPage: false, animations: "disabled" });
  await page.setViewportSize({ width: 1280, height: 900 });

  const request = page.waitForRequest((request) => request.url().endsWith("/api/board/write"));
  const result = await submit();
  expect((await request).postDataJSON()).toMatchObject({ action: "revise", task_id: id, description: revised, instructions: reason });
  expect(result).toMatchObject({ revision_state: "applied" });
  const resolved = await mc(["task", "get", String(id)]);
  expect(resolved).toMatchObject({
    status: "seeded", blocked: 0, blocked_kind: null, blocked_reason: null, description: revised,
    requirements_version: Number(reported.requirements_version) + 1, base_commit: submission.input, revision_notes: null,
  });
  // The next Worker run starts at the last submitted commit.
  expect(resolved.delivery).toMatchObject({ input_commit: submission.commit, version_id: null, checkpoint_version: null });
  await page.getByTestId("board-unblock").waitFor({ state: "hidden" });
  await page.getByTestId("board-revise").waitFor();
  await page.waitForFunction((text) => document.querySelector('[data-testid="board-history"]')?.textContent?.includes(text), reason);
  await closeTask();
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

test("approved work permits Unblock before a merge and preserves its approval", async () => {
  const { page } = smoke;
  const created = await write({ action: "create", scope: "task", title: "Approved task with a failed merge", project: "board-alpha", description: "The approved requirements stay effective." });
  const recordID = Number(created.task_id);
  const db = openSpine(smoke);
  let packet: { packetID: number; versionID: number };
  try {
    packet = seedDeliveryPacket(db, {
      taskID: recordID, commit: "approved-commit", thesis: "The report meets the approved requirements.",
    });
  } finally { db.close(); }
  expect(await mc(["packet", "decide", String(recordID), "--approve", "--packet", String(packet.packetID), "--version", String(packet.versionID)])).toMatchObject({ decision: "approved", archived: false });
  const failures = openSpine(smoke);
  failures.run("UPDATE tasks SET blocked=1,blocked_kind='checkout',blocked_reason='The target branch needs repair.',dispatch_retries=0 WHERE id=?", [recordID]);
  failures.close();
  await page.reload();
  await page.getByTestId("board-card").first().waitFor();
  await openTask(recordID);
  // A packaged task has no title and priority edit.
  expect(await page.getByTestId("board-edit").count()).toBe(0);
  expect(await page.getByTestId("board-revise").isDisabled()).toBe(true);
  expect(await page.getByTestId("board-cancel").isEnabled()).toBe(true);
  expect(await page.getByTestId("board-unblock").isEnabled()).toBe(true);
  await page.getByTestId("board-unblock").click();
  expect(await page.getByTestId("board-action-explanation").innerText()).toContain("lets the approved merge resume");
  await page.getByTestId("board-write-reason").fill("The operator repaired the target branch and restored the service.");
  await submit();
  expect(await mc(["task", "get", String(recordID)])).toMatchObject({
    decision: "approved", archived: 0, blocked: 0, stalled: 1, dispatch_retries: 0, status: "packaged",
    description: "The approved requirements stay effective.", verified_sha: "approved-commit",
  });
  await page.getByTestId("board-unblock").waitFor({ state: "hidden" });
  await page.getByTestId("board-resume").waitFor();
  await page.getByTestId("board-resume").click();
  await page.getByTestId("board-write-reason").fill("The operator restored the task run allowance.");
  await submit();
  expect(await mc(["task", "get", String(recordID)])).toMatchObject({ blocked: 0, stalled: 0, dispatch_retries: 3 });
  await page.getByTestId("board-resume").waitFor({ state: "hidden" });
  expect(await page.getByTestId("board-edit").count()).toBe(0);
  expect(await page.getByTestId("board-revise").isDisabled()).toBe(true);
  expect(await page.getByTestId("board-cancel").isEnabled()).toBe(true);
  await page.getByTestId("board-history").locator(":scope > summary").click();
  expect(await page.getByTestId("board-history").innerText()).toContain("The operator repaired the target branch and restored the service.");
}, 30000);

test("a requirements revision preserves its superseded report as readable history", async () => {
  const { page } = smoke;
  const result = await write({ action: "create", scope: "task", title: "Task with a historical report", project: "board-alpha", description: "Original report requirements" });
  const reportTask = Number(result.task_id);
  const plane = join(smoke.scratch, "reports", "board-alpha");
  mkdirSync(join(plane, String(reportTask), "packet"), { recursive: true });
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "board-alpha"\naccess = "ro"\n`, { mode: 0o600 });
  writeFileSync(join(plane, String(reportTask), "packet", "report.html"), "<!doctype html><h1>The original review report</h1><p>The earlier requirements remain in history.</p>");
  const db = openSpine(smoke);
  seedDeliveryPacket(db, {
    taskID: reportTask,
    renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "board-alpha", String(reportTask), "packet", "report.html"),
    reportSHA256: new Bun.CryptoHasher("sha256").update(readFileSync(join(plane, String(reportTask), "packet", "report.html"))).digest("hex"),
    thesis: "A packet for the original requirements",
  });
  db.close();
  await page.reload();
  await page.getByTestId("board-card").first().waitFor();
  await openTask(reportTask);
  await page.getByTestId("board-revise").click();
  await page.getByTestId("board-write-requirements").fill("The revised report requires a concrete example.");
  await page.getByTestId("board-write-reason").fill("The operator requires an example.");
  await submit();
  await page.waitForFunction(() => document.querySelector('[data-testid="board-history"]')?.textContent?.includes("Superseded review packets"));
  await page.getByTestId("board-history").locator(":scope > summary").click();
  await page.getByText("Superseded review packets", { exact: true }).click();
  const reportResponse = page.waitForResponse((response) => response.url().includes(`/api/tasks/${reportTask}/history/packets/`));
  await page.getByTestId("board-history").getByRole("button", { name: "Read the report" }).click();
  expect((await reportResponse).ok()).toBe(true);
  const frame = page.getByTestId("board-history").locator("iframe");
  await frame.waitFor();
  expect(await frame.getAttribute("sandbox")).not.toContain("allow-scripts");
  expect(await frame.getAttribute("sandbox")).not.toContain("allow-same-origin");
  await page.frameLocator('[data-testid="board-history"] iframe').getByRole("heading", { name: "The original review report" }).waitFor();
  await refreshKeepsReport(frame, reportTask);
  // The revision replaced the packet, so the dialog offers no packet link.
  expect(await page.getByTestId("task-dialog-packet").count()).toBe(0);
  const current = await mc(["task", "get", String(reportTask)]);
  expect(current.status).toBe("seeded");
  const packets = (await mc(["packet", "list"])).packets as any[];
  expect(packets.some((packet) => packet.task_id === reportTask && !packet.superseded && !packet.archived)).toBe(false);
  expect(packets.some((packet) => packet.task_id === reportTask && packet.review_state === "replaced")).toBe(true);
  await page.screenshot({ animations: "disabled", path: evidencePath("collie-board-evidence/historical-report.png"), fullPage: false });
}, 30000);

test("the task dialog links the review packet", async () => {
  const { page } = smoke;
  const plane = join(smoke.scratch, "reports", "board-alpha");
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "board-alpha"\naccess = "ro"\n`, { mode: 0o600 });
  const report = (id: number, html: string) => {
    mkdirSync(join(plane, String(id), "packet"), { recursive: true });
    writeFileSync(join(plane, String(id), "packet", "report.html"), html);
    return {
      renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "board-alpha", String(id), "packet", "report.html"),
      reportSHA256: new Bun.CryptoHasher("sha256").update(html).digest("hex"),
    };
  };
  const packaged = Number((await write({ action: "create", scope: "task", title: "Task with a review packet", project: "board-alpha", description: "The packet holds the report." })).task_id);
  const db = openSpine(smoke);
  try {
    seedDeliveryPacket(db, { taskID: packaged, thesis: "The packet of a single task", ...report(packaged, "<!doctype html><h1>The single task report</h1>") });
  } finally { db.close(); }
  await page.reload();
  await page.getByTestId("board-card").first().waitFor();

  await openTask(packaged);
  await page.getByTestId("task-dialog-packet").waitFor();
  const main = await page.locator("#task-dialog-body").innerText();
  for (const text of ["Read the report", "Carried notes", "Accepted work", "Submitted commit", "Verified commit", "Verification and wiki", "temporary bytes"]) {
    expect(main).not.toContain(text);
  }
  expect(await page.locator("#task-dialog-body").innerText()).not.toMatch(/\b[0-9a-f]{40}\b/);
  await page.screenshot({ animations: "disabled", path: evidencePath("collie-board-evidence/task-dialog-packet.png"), fullPage: false });

}, 30000);

test("an active run keeps its requirements while a task revision waits", async () => {
  const { page } = smoke;
  const result = await write({ action: "create", scope: "task", title: "Task with an active run", project: "board-beta", description: "Original run requirements" });
  const activeTask = Number(result.task_id);
  const db = openSpine(smoke);
  db.run("INSERT INTO runs(id,tier,role,project,subject) VALUES ('board-active-run','pipeline','worker','board-beta',?)", [activeTask]);
  db.run("UPDATE leases SET run_id='board-active-run',subject=?,owner='worker',acquired_at=datetime('now'),last_heartbeat_at=datetime('now'),boot_id='smoke',acquired_uptime_ms=0,heartbeat_uptime_ms=0,deadline_uptime_ms=3600000 WHERE project='board-beta'", [activeTask]);
  db.close();
  await selectProject(smoke, "board-beta");
  await openTask(activeTask);
  await page.getByTestId("board-edit").click();
  await page.getByTestId("board-write-title").fill("Task title during an active run");
  await submit();
  expect(await mc(["task", "get", String(activeTask)])).toMatchObject({ status: "seeded" });
  await page.getByRole("heading", { name: "Task title during an active run", exact: true }).waitFor();
  await page.getByTestId("board-revise").click();
  expect(await page.getByTestId("board-revision-effect").innerText()).toBe("The active run finishes first. The Worker then continues from the saved code.");
  await page.getByTestId("board-write-requirements").fill("Requested requirements after the active run");
  await page.getByTestId("board-write-reason").fill("The operator added a precise output requirement.");
  await submit();
  await page.getByTestId("board-requested-requirements").waitFor();
  expect(await page.getByTestId("board-revision-status").innerText()).toContain(REVISION_WAIT);
  const row = await mc(["task", "get", String(activeTask)]);
  expect(row.description).toBe("Original run requirements");
  expect(row.pending_revision).toMatchObject({ requirements: "Requested requirements after the active run", active_run_id: "board-active-run" });
  await closeTask();
  await page.reload();
  await page.getByTestId("board-card").first().waitFor();
  await openTask(activeTask);
  expect(await page.getByTestId("board-revision-status").innerText()).toContain(REVISION_WAIT);
  expect(await page.getByTestId("board-revise").isDisabled()).toBe(true);
  await page.screenshot({ animations: "disabled", path: evidencePath("collie-board-evidence/revision-wait-desktop.png"), fullPage: false });
  await closeTask();
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

test("the Verifier allowance dialog authorizes the requested correction without a Project hold", async () => {
  const { page } = smoke;
  const available = () => {
    mkdirSync(join(smoke.home, "health"), { recursive: true });
    writeFileSync(join(smoke.home, "health", "resident-availability.json"), JSON.stringify({ observed_at: new Date().toISOString(), can_commit: true }));
  };
  available();
  await seedProject(smoke, "allowances", { title: "Allowance Project", environmentReady: true });
  await gotoReady(smoke);
  await selectProject(smoke, "allowances");
  const created = await write({ action: "create", scope: "task", project: "allowances", title: "verifier limit", description: "Preserve the required result." });
  const id = Number(created.task_id);
  const db = openSpine(smoke);
  seedDeliveryPacket(db, { taskID: id, thesis: "The task requires another correction." });
  const cause = "The Verifier rejected three corrections. The export omits the required date. Evidence: the export test failed. Proposed resolution: include the date.";
  db.run("UPDATE tasks SET correction_count=3,verifier_correction_open=1,correction_open=1,correction_reason='The export requires a date.',blocked_kind='allowance',allowance_condition='verifier',allowance_version=submitted_version,allowance_reason=?,blocked=1,blocked_reason=? WHERE id=?",
    [cause, cause, id]);
  db.close();
  available();
  await page.reload();
  await gotoReady(smoke);
  await selectProject(smoke, "allowances");
  await openTask(id);
  const task = page.getByTestId("task-dialog");
  expect(await task.innerText()).toContain(cause);
  expect(await task.innerText()).not.toContain("prevents new work");
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await task.evaluate(node => node.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ animations: "disabled", path: evidencePath(`collie-board-evidence/verifier-limit-${width}.png`), animations: "disabled" });
  }
  await task.getByText("Technical details and evidence", { exact: true }).click();
  expect(await task.evaluate(node => node.scrollWidth <= window.innerWidth)).toBe(true);
  await task.getByText("Technical details and evidence", { exact: true }).click();
  await page.setViewportSize({ width: 1280, height: 900 });
  await closeTask();
  await page.getByTestId("system-notices").getByRole("button", { name: /View details|Review issues/ }).click();
  const limitIssues = page.getByRole("dialog", { name: "System issues", exact: true });
  if (await limitIssues.isVisible()) await limitIssues.locator(`[data-condition-id="task:${id}"]`).getByRole("button", { name: /^View details for/ }).click();
  const limitDialog = page.getByRole("dialog", { name: "Issue details", exact: true });
  expect(await limitDialog.innerText()).toContain(cause);
  expect(await limitDialog.innerText()).not.toContain("prevents new work");
  await page.screenshot({ animations: "disabled", path: evidencePath("collie-board-evidence/verifier-notification.png"), animations: "disabled" });
  await limitDialog.getByRole("button", { name: "Open task", exact: true }).click();
  await page.getByTestId("board-allow-corrections").click();
  await page.getByTestId("board-write-reason").fill("Continue with the required date.");
  // The Board refresh after the write waits until the test closes the task dialog.
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  const refreshTasks = (url: URL) => url.pathname.endsWith("/api/tasks");
  await page.route(refreshTasks, async (route) => { await held; await route.continue(); });
  await submit();
  const updated = await mc(["task", "get", String(id)]);
  expect(updated).toMatchObject({ status: "seeded", blocked: 0 });
  expect(updated.delivery).toMatchObject({ correction_count: 0, allowance_condition: null, project_priority: 0 });
  await closeTask();
  const refreshed = page.waitForResponse((response) => refreshTasks(new URL(response.url())));
  release();
  await refreshed;
  await page.unroute(refreshTasks);
  await Bun.sleep(500);
  expect(await page.getByTestId("task-dialog").isVisible()).toBe(false);
  await write({ action: "block", task_id: id, expected_version: updated.record_version, reason: "The two changes require different date formats. Select the required format." });
  available();
  await page.reload();
  await gotoReady(smoke);
  await selectProject(smoke, "allowances");
  const notices = page.getByTestId("system-notices");
  await notices.getByRole("button", { name: /View details|Review issues/ }).click();
  const issues = page.getByRole("dialog", { name: "System issues", exact: true });
  if (await issues.isVisible()) await issues.locator(`[data-condition-id="task:${id}"]`).getByRole("button", { name: /^View details for/ }).click();
  const dialog = page.getByRole("dialog", { name: "Issue details", exact: true });
  expect(await dialog.innerText()).toContain("Select the required format.");
  expect(await dialog.innerText()).not.toContain("prevents new work");
  await dialog.getByRole("button", { name: "Open task", exact: true }).click();
  await page.getByTestId("board-record-controls").waitFor();
  const current = await mc(["task", "get", String(id)]);
  await write({ action: "cancel", task_id: id, expected_version: current.record_version, reason: "The browser check is complete." });
  await closeTask();
}, 120000);
