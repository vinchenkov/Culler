import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, gotoReady, mc, seedProject, selectProject, teardown, type Smoke, openSpine } from "./harness";
import { completeMerge, seedDeliveryPacket } from "./delivery-fixture";

let smoke: Smoke;
const task: Record<string, number> = {};

const DATE = String.raw`\w{3} \d{1,2}, \d{4} \d{2}:\d{2}`;
const escape = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

const plane = () => join(smoke.scratch, "reports", "alpha");

// The alpha Project holds three packets that await a decision.
beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "alpha", { vision: false, environmentReady: true });
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane())}\ntarget = "alpha"\naccess = "ro"\n`, { mode: 0o600 });
  const names = ["merged", "first", "second"];
  for (const name of names) {
    task[name] = Number((await mc(["task", "add", `${name} task`, "--project", "alpha"]))["task_id"]);
  }
  const db = openSpine(smoke);
  try {
    db.run("UPDATE settings SET review_wip_cap=10 WHERE id=1");
    for (const [index, name] of names.entries()) {
      const body = `<!doctype html><h1>The ${name} report</h1>`;
      mkdirSync(join(plane(), String(task[name]), "packet"), { recursive: true });
      writeFileSync(join(plane(), String(task[name]), "packet", "report.html"), body);
      seedDeliveryPacket(db, {
        taskID: task[name]!, thesis: `The ${name} packet`,
        renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "alpha", String(task[name]), "packet", "report.html"),
        reportSHA256: new Bun.CryptoHasher("sha256").update(body).digest("hex"),
        createdAt: new Date(Date.now() - (10 - index) * 3600_000).toISOString().replace("T", " ").slice(0, 19),
      });
    }
  } finally { db.close(); }
  await gotoReady(smoke);
  await smoke.page.getByTestId("project-alpha").waitFor({ state: "attached" });
  await smoke.page.getByTestId("tab-review").click();
  await selectProject(smoke, "alpha");
}, 90000);
afterAll(async () => { await teardown(smoke); });

function rowOf(name: string) {
  return smoke.page.locator(`[data-testid="packet-row"][data-task="${task[name]}"]`);
}

async function waitForDecision(pattern: RegExp) {
  await smoke.page.waitForFunction((source) =>
    new RegExp(source).test(document.querySelector('[data-testid="packet-decision"]')?.textContent ?? ""),
  pattern.source, { timeout: 20000 });
}

async function waitForRowText(name: string, text: string) {
  await smoke.page.waitForFunction(([id, text]) =>
    document.querySelector(`[data-testid="packet-row"][data-task="${id}"] [data-testid="packet-merge-state"]`)?.textContent === text,
  [String(task[name]), text] as const, { timeout: 20000 });
}

/** The packet list row of one task, as `mc packet list` states it. */
async function packetRow(name: string) {
  const rows = (await mc(["packet", "list"]))["packets"] as Record<string, unknown>[];
  const row = rows.find((p) => p["task_id"] === task[name]);
  if (!row) throw new Error(`mc packet list has no packet for task ${task[name]}`);
  return row;
}

async function openRow(name: string) {
  await rowOf(name).click();
  await smoke.page.getByTestId("packet-reader").waitFor({ state: "visible" });
  await smoke.page.frameLocator('[data-testid="packet-frame"]').getByText(`The ${name} report`).waitFor();
}

/** Closes a reader that covers the window. A reader beside the queue column stays open. */
async function closeReader() {
  if (!(await smoke.page.getByTestId("reader-close").isVisible())) return;
  await smoke.page.getByTestId("reader-close").click();
  await smoke.page.getByTestId("packet-reader").waitFor({ state: "hidden" });
}

/** The operator approves one packet, then opens it to inspect the merge state. */
async function approve(name: string, state: string, relatedTask: number | null, expected: string) {
  const { page } = smoke;
  await openRow(name);
  await page.getByTestId("packet-decide").waitFor({ state: "visible" });
  await page.getByTestId("packet-approve").waitFor({ state: "visible" });
  const response = page.waitForResponse((res) => res.url().endsWith(`/api/packets/${task[name]}/decide`));
  await page.getByTestId("packet-approve").click();
  expect((await response).ok()).toBe(true);
  await waitForRowText(name, expected);
  await openRow(name);
  await waitForDecision(/^Approved on /);
  const row = await packetRow(name);
  expect(row["review_state"]).toBe("approved");
  expect(row["merge_state"]).toBe(state);
  expect(row["merge_task_id"]).toBe(relatedTask);
  expect(row["merge_state_text"]).toBe(expected);
  return expected;
}

// No execution is active, and no other approved task precedes this one.
const MERGE_NOW = "Culler merges this change now.";

test("an approved packet shows its merge state until the merge", async () => {
  const { page } = smoke;
  const text = await approve("merged", "merge", null, MERGE_NOW);
  await waitForRowText("merged", text);

  await page.getByTestId("tab-board").click();
  const card = page.locator(`[data-testid="board-card"][data-task="${task.merged}"]`);
  await card.getByTestId("board-card-approved").waitFor({ timeout: 15000 });
  expect(await card.getByTestId("board-card-approved").innerText()).toBe("Approved");
  expect(await card.innerText()).not.toContain("Review packet available");
  expect(await page.locator(`[data-testid="board-card"][data-task="${task.first}"]`).innerText()).toContain("Review packet available");
  await card.getByTestId("board-card-open").click();
  const dialog = page.locator('[data-testid="task-dialog"][open]');
  await dialog.getByTestId("task-merge-state").waitFor({ timeout: 15000 });
  expect(await dialog.getByTestId("task-merge-state").innerText()).toBe(text);
  await dialog.getByTestId("task-dialog-close").click();
  await page.getByTestId("tab-review").click();

  await openRow("merged");
  await waitForDecision(new RegExp(`^Approved on ${DATE}\\. ${escape(text)}$`));
  expect(await page.getByTestId("packet-decide").isVisible()).toBe(false);
  const db = openSpine(smoke);
  let commit: string;
  try {
    commit = completeMerge(db, task.merged!);
  } finally { db.close(); }
  const merged = await packetRow("merged");
  expect(merged["review_state"]).toBe("merged");
  expect(merged["merge_commit"]).toBe(commit);
  const branch = String(merged["destination_branch"]);
  await waitForDecision(new RegExp(`^Approved on ${DATE}\\. Merged into ${escape(branch)} at ${escape(commit)}\\.$`));
  await closeReader();
  expect(await rowOf("merged").count()).toBe(0);
}, 60000);

test("the reader follows the merge state of an approved packet", async () => {
  const { page } = smoke;
  await approve("first", "merge", null, MERGE_NOW);
  // The first task has the lower task number, so it merges first.
  const before = await approve("second", "after-task", task.first!, `Culler merges task #${task.first} first.`);
  await waitForRowText("second", before);
  await openRow("second");
  await waitForDecision(new RegExp(`^Approved on ${DATE}\\. ${escape(before)}$`));
  const db = openSpine(smoke);
  try {
    completeMerge(db, task.first!);
  } finally { db.close(); }
  const text = String((await packetRow("second"))["merge_state_text"]);
  expect(text).toBe(MERGE_NOW);
  await waitForDecision(new RegExp(`^Approved on ${DATE}\\. ${escape(text)}$`));
  await closeReader();
  await waitForRowText("second", text);
}, 60000);
