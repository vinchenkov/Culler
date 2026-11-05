// dialog.verify.ts — verify-behavior evidence for the Board task dialog.
// Run from dashboard/ with the fake-routing mc:
//   MC_SMOKE_BIN=<mc built with -tags test_fake_routing> \
//     mise exec -- bun ../.tracking/task-dialog/evidence/dialog.verify.ts
// Screenshots and the video land beside this file. Not a suite member.
import { Database } from "bun:sqlite";
import { mkdirSync, readdirSync, renameSync } from "node:fs";
import { dirname, join } from "node:path";

import { SMOKE_TOKEN, bootSmoke, mc, teardown, type Smoke } from "../../../dashboard/smoke/harness";

const OUT = dirname(new URL(import.meta.url).pathname);
mkdirSync(OUT, { recursive: true });

const REQUIREMENTS = `The Packager renders an HTML diff explainer for every packet.

Done when:
- every packaged task carries \`packet/diff.html\`
- the Review page opens it inline`;

const DESCRIPTION = `The composer is a fixed one-line input. A long message hides its own start.

Make it grow with its text:
1. one line at rest
2. grows to ten lines
3. scrolls past ten

Keep \`Enter\` as send and \`Shift+Enter\` as newline.`;

const NOTES = `Second pass. The Verifier asked for a test that proves the ten-line cap, not the growth.`;

async function seed(smoke: Smoke) {
  await mc(["project", "add", "homie", "--title", "Homie", "--kind", "repo"]);
  await mc(["project", "add", "llm-council", "--title", "Council", "--kind", "repo"]);
  await mc(["project", "add", "docs", "--title", "Docs", "--kind", "repo"]);
  const packetTask = await mc([
    "task", "add", "Wire the Packager's diff explainer end to end",
    "--project", "homie", "--description", REQUIREMENTS,
  ]);
  const packetTaskID = Number(packetTask["task_id"]);
  const a = await mc([
    "task", "add", "The Homie composer grows with its text",
    "--project", "llm-council", "--priority", "-1", "--description", DESCRIPTION,
  ]);
  const b = await mc([
    "task", "add", "The resident projector serves a stale grant after rotation",
    "--project", "llm-council", "--description", "New launches 401 until the resident restarts.",
  ]);
  const d = await mc([
    "task", "add", "The Board columns drop the owed-work subtitle",
    "--project", "llm-council", "--description", "Each column header keeps its name and its count.",
  ]);
  const e = await mc([
    "task", "add", "The onboard skill states the acceptance procedure",
    "--project", "docs",
  ]);
  const aID = Number(a["task_id"]);
  const bID = Number(b["task_id"]);
  const dID = Number(d["task_id"]);
  const eID = Number(e["task_id"]);

  let db = new Database(join(smoke.home, "spine.db"));
  db.run(`UPDATE tasks SET status = 'seeded' WHERE id IN (?, ?, ?, ?)`, [packetTaskID, aID, dID, eID]);
  db.run(`UPDATE tasks SET revision_notes = ?, correction_count = 1, stage_entered_at = datetime('now', '-40 minutes') WHERE id = ?`, [NOTES, aID]);
  db.close();
  await mc([
    "task", "block", String(bID),
    "--reason", "Needs an operator decision: restart the resident on rotation, or re-read the grant per launch?",
  ]);

  db = new Database(join(smoke.home, "spine.db"));
  db.run(
    `INSERT INTO tasks (title, description, scope, status, project) VALUES
     ('Render explain-diff-html into the packet directory', 'Call the skill from the Packager brief and write packet/diff.html.', 'task', 'seeded', 'homie'),
     ('Open the diff explainer from the Review page', NULL, 'task', 'seeded', 'homie')`,
  );
  const c1 = (db.query(`SELECT id FROM tasks WHERE title LIKE 'Render explain%'`).get() as { id: number }).id;
  const c2 = (db.query(`SELECT id FROM tasks WHERE title LIKE 'Open the diff%'`).get() as { id: number }).id;
  db.run(`UPDATE tasks SET status = 'worked' WHERE id IN (?, ?)`, [c1, c2]);
  db.run(`UPDATE tasks SET stage_entered_at = datetime('now', '-2 hours') WHERE id = ?`, [c1]);
  db.run(`UPDATE tasks SET correction_count = 2 WHERE id = ?`, [c2]);
  // Eight runs on c2: seven ended, one live — the history folds the oldest.
  const ended = [
    ["arbiter", "completed", "-9 hours", "-8 hours"],
    ["worker", "completed", "-7 hours", "-6 hours"],
    ["verifier", "blocked", "-6 hours", "-5 hours"],
    ["worker", "infra-failed", "-5 hours", "-4 hours"],
    ["worker", "reaped", "-4 hours", "-3 hours"],
    ["worker", "completed", "-3 hours", "-2 hours"],
    ["verifier", "completed", "-2 hours", "-1 hours"],
  ];
  ended.forEach(([role, outcome, from, to], i) => {
    db.run(
      `INSERT INTO runs (id, tier, role, project, subject, created_at, ended_at, outcome)
       VALUES (?, 'pipeline', ?, 'homie', ?, datetime('now', ?), datetime('now', ?), ?)`,
      [`run-c2-${i}`, role, c2, from, to, outcome],
    );
  });
  db.run(
    `INSERT INTO runs (id, tier, role, project, subject, created_at, ended_at, outcome)
     VALUES ('run-worker-live', 'pipeline', 'worker', 'homie', ?, datetime('now', '-12 minutes'), NULL, NULL)`,
    [c2],
  );
  db.run(
    `UPDATE leases SET run_id = 'run-worker-live', subject = ?, owner = 'worker', acquired_at = datetime('now', '-12 minutes'),
       last_heartbeat_at = datetime('now', '-5 seconds'), hard_deadline_at = datetime('now', '+1 hour') WHERE project = 'homie'`,
    [c2],
  );
  // The stale run in docs: a worker whose heartbeat stopped twenty minutes ago.
  db.run(`UPDATE tasks SET status = 'worked' WHERE id = ?`, [eID]);
  db.run(
    `INSERT INTO runs (id, tier, role, project, subject, created_at, ended_at, outcome)
     VALUES ('run-docs-stale', 'pipeline', 'worker', 'docs', ?, datetime('now', '-50 minutes'), NULL, NULL)`,
    [eID],
  );
  db.run(
    `UPDATE leases SET run_id = 'run-docs-stale', subject = ?, owner = 'worker', acquired_at = datetime('now', '-50 minutes'),
       last_heartbeat_at = datetime('now', '-20 minutes'), hard_deadline_at = datetime('now', '+1 hour') WHERE project = 'docs'`,
    [eID],
  );
  // The packaged task with a packet and an approval.
  for (const status of ["worked", "verified", "packaged"]) {
    db.run(`UPDATE tasks SET status = ? WHERE id = ?`, [status, dID]);
  }
  db.run(`UPDATE tasks SET stage_entered_at = datetime('now', '-25 minutes') WHERE id = ?`, [dID]);
  db.run(
    `INSERT INTO review_packets (task_id, render_path, thesis, created_at)
     VALUES (?, NULL, 'The subtitle repeated the column name in other words.', datetime('now', '-25 minutes'))`,
    [dID],
  );
  db.run(
    `INSERT INTO runs (id, tier, role, project, subject, created_at, ended_at, outcome) VALUES
     ('run-d-worker', 'pipeline', 'worker', 'llm-council', ?, datetime('now', '-2 hours'), datetime('now', '-90 minutes'), 'completed'),
     ('run-d-verifier', 'pipeline', 'verifier', 'llm-council', ?, datetime('now', '-80 minutes'), datetime('now', '-60 minutes'), 'completed'),
     ('run-d-packager', 'pipeline', 'packager', 'llm-council', ?, datetime('now', '-50 minutes'), datetime('now', '-25 minutes'), 'completed')`,
    [dID, dID, dID],
  );
  db.run(`UPDATE tasks SET decision = 'approved', decided_at = datetime('now', '-15 minutes') WHERE id = ?`, [dID]);
  db.close();
  return { packetTaskID, aID, bID, dID, eID, c1, c2 };
}

const problems: string[] = [];
const check = (cond: boolean, what: string) => {
  if (!cond) problems.push(what);
};

const smoke = await bootSmoke();
try {
  const ids = await seed(smoke);
  // A recorded context beside the harness's own; one Chromium, two contexts.
  const browser = smoke.context.browser()!;
  const context = await browser.newContext({
    recordVideo: { dir: OUT, size: { width: 1440, height: 1000 } },
    viewport: { width: 1440, height: 1000 },
  });
  const page = await context.newPage();
  const log: string[] = [];
  page.on("pageerror", (err) => log.push(`pageerror: ${err.message}`));
  page.on("console", (msg) => {
    if (msg.type() === "error") log.push(`console.error: ${msg.text()}`);
  });
  await page.goto(`${smoke.server.url}#token=${SMOKE_TOKEN}`);
  await page.waitForSelector("body[data-ready]", { timeout: 15000 });
  await page.locator('[data-testid="board-card"]').first().waitFor({ timeout: 15000 });
  await page.waitForTimeout(600);
  await page.screenshot({ path: join(OUT, "01-board.png") });

  const dialog = page.locator('[data-testid="task-dialog"]');
  const cardOf = (id: number) => page.locator(`[data-testid="board-card"][data-task="${id}"]`);
  const isOpen = () => page.evaluate(() => document.querySelector('[data-testid="task-dialog"]')!.hasAttribute("open"));
  const ring = (id: number) => cardOf(id).evaluate((node) => node.classList.contains("open"));
  const open = async (id: number) => {
    await cardOf(id).locator('[data-testid="board-card-open"]').click();
    await page.locator('[data-testid="task-dialog"][open]').waitFor({ timeout: 15000 });
    await page.waitForFunction(
      () => {
        const d = document.querySelector('[data-testid="task-dialog"]');
        return d !== null && d.textContent !== null && !d.textContent.includes("Reading the task");
      },
      undefined,
      { timeout: 15000 },
    );
    await page.waitForTimeout(400);
  };
  const closeWithEscape = async () => {
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => !document.querySelector('[data-testid="task-dialog"]')!.hasAttribute("open"));
    await page.waitForTimeout(200);
  };

  // Story 1: the dialog opens over the page on the task's prose, notes and facts.
  await open(ids.aID);
  await page.screenshot({ path: join(OUT, "02-dialog-prose-notes-facts.png") });
  const box = await dialog.boundingBox();
  check(box !== null && box.width >= 1440 * 0.7 && box.height >= 1000 * 0.55, `story 1: the dialog covers most of the window (${JSON.stringify(box)})`);
  check(((await dialog.locator("#task-dialog-title").textContent()) ?? "") === "The Homie composer grows with its text", "story 1: the head names the task");
  check((await dialog.locator(".td-head .tag.stage").count()) === 1, "story 1: the head carries the stage pill");
  check((await dialog.locator(".td-head .tag.priority").count()) === 1, "story 1: the head carries the expedited pill");
  check((await dialog.locator(".td-main .md ol li").count()) === 3, "story 1: the description's ordered list renders as three items");
  check((await dialog.locator(".td-main .md code").count()) >= 2, "story 1: inline code renders");
  check((await dialog.locator(".td-main .callout.tone-stone").count()) === 1, "story 1: the carried notes callout");
  const facts = (await dialog.locator('[data-testid="task-dialog-facts"]').textContent()) ?? "";
  check(facts.includes(`#${ids.aID}`), "story 1: the facts grid names the task id");
  check(facts.includes("user"), "story 1: the facts grid names the origin");
  check(/Seededfor 40m/.test(facts), `story 1: the facts grid names the stage and its age (${facts})`);
  check(await ring(ids.aID), "story 1: the open card wears the ring");
  // The poll keeps it open.
  await page.waitForTimeout(2500);
  check(await isOpen(), "story 1: the dialog survives the poll");

  // Story 2: Escape closes it; the blocked reason is a rust callout.
  await closeWithEscape();
  check(!(await ring(ids.aID)), "story 2: the closed card drops the ring");
  await open(ids.bID);
  await page.screenshot({ path: join(OUT, "03-blocked-reason-callout.png") });
  const blocked = dialog.locator(".td-main .callout.tone-rust");
  check((await blocked.count()) === 1, "story 2: one rust callout");
  check(((await blocked.textContent()) ?? "").includes("restart the resident"), "story 2: the callout holds the full reason");
  check((await dialog.locator(".td-head .chip.attention").count()) === 1, "story 2: the head carries the attention chip");

  // Story 3: the live run panel and the folded timeline.
  await closeWithEscape();
  await open(ids.c2);
  await page.screenshot({ path: join(OUT, "04-live-run-and-timeline.png") });
  const live = dialog.locator('[data-testid="task-dialog-live"]');
  check((await live.count()) === 1, "story 3: the live panel");
  const liveText = (await live.textContent()) ?? "";
  check(liveText.includes("Running now"), "story 3: the live panel says Running now");
  check(liveText.includes("started 12m ago"), `story 3: the live panel dates the run (${liveText})`);
  check(liveText.includes("run-worker-live"), "story 3: the live panel names the run id");
  check(/heartbeat \d+s ago/.test(liveText), `story 3: the live panel dates the heartbeat (${liveText})`);
  check((await dialog.locator(".td-side .timeline .tl").count()) === 6, "story 3: the timeline lists six runs");
  check(((await dialog.locator(".td-side .sec-more").textContent()) ?? "") === "2 earlier runs", "story 3: the fold names the two older runs");
  check((await dialog.locator('.td-head [data-testid="task-dialog-running"]').count()) === 1, "story 3: the head carries the running chip");

  // Story 5: the decision callout and the packet button.
  await closeWithEscape();
  await open(ids.dID);
  await page.screenshot({ path: join(OUT, "06-decision-and-packet-button.png") });
  const decision = dialog.locator(".td-main .callout.tone-leaf");
  check((await decision.count()) === 1, "story 5: the approval callout is leaf");
  check(((await decision.textContent()) ?? "").includes("approved"), "story 5: the callout names the decision");
  check(((await decision.textContent()) ?? "").includes("15m ago"), "story 5: the callout dates the decision");
  check((await dialog.locator('[data-testid="task-dialog-packet"]').count()) === 1, "story 5: the packet button");

  // Story 6: the stale heartbeat.
  await closeWithEscape();
  await open(ids.eID);
  await page.screenshot({ path: join(OUT, "07-stale-heartbeat.png") });
  check((await dialog.locator('[data-testid="task-dialog-live"].stale').count()) === 1, "story 6: the live panel is stale");
  check(((await dialog.locator('[data-testid="task-dialog-live"]').textContent()) ?? "").includes("Heartbeat stale"), "story 6: it says so");

  // Story 7: the packet button closes the dialog and lands on Review with the packet selected.
  await closeWithEscape();
  await open(ids.dID);
  await dialog.locator('[data-testid="task-dialog-packet"]').click();
  await page.locator(".packet-row.selected").waitFor({ timeout: 15000 });
  await page.waitForTimeout(400);
  await page.screenshot({ path: join(OUT, "08-review-packet-selected.png") });
  check(!(await isOpen()), "story 7: the dialog closed");
  check(((await page.locator(".packet-row.selected").textContent()) ?? "").includes(`packet #${ids.dID}`), "story 7: the selected packet is the task's");

  // Story 8: back on the Board, a click on the backdrop closes the dialog.
  await page.locator("nav.seg .tab", { hasText: "Board" }).click();
  await cardOf(ids.aID).waitFor({ timeout: 15000 });
  await open(ids.aID);
  await page.mouse.click(20, 500);
  await page.waitForFunction(() => !document.querySelector('[data-testid="task-dialog"]')!.hasAttribute("open"));
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(OUT, "09-board-after-backdrop-close.png") });
  check(!(await ring(ids.aID)), "story 8: the card drops the ring after the backdrop click");

  // Story 9: the close button.
  await open(ids.bID);
  await dialog.locator('[data-testid="task-dialog-close"]').click();
  await page.waitForFunction(() => !document.querySelector('[data-testid="task-dialog"]')!.hasAttribute("open"));
  check(!(await isOpen()), "story 9: the close button closes the dialog");

  // Story 10: a narrow window stacks the two columns.
  await page.setViewportSize({ width: 720, height: 900 });
  await open(ids.c2);
  await page.screenshot({ path: join(OUT, "10-narrow-window-stacked.png") });
  const main = await dialog.locator(".td-main").boundingBox();
  const side = await dialog.locator(".td-side").boundingBox();
  check(main !== null && side !== null && side.y >= main.y + main.height - 1, "story 10: the side column sits under the main column");
  await closeWithEscape();

  check(log.length === 0, `browser problems: ${log.join(" | ")}`);
  await page.close();
  await context.close();
  for (const f of readdirSync(OUT)) {
    if (f.endsWith(".webm") && f !== "dialog.webm") renameSync(join(OUT, f), join(OUT, "dialog.webm"));
  }
} finally {
  await teardown(smoke);
}
if (problems.length > 0) {
  console.log("NOT VERIFIED");
  for (const p of problems) console.log(` - ${p}`);
  process.exit(1);
}
console.log("VERIFIED");
// The harness leaves its Chromium for process exit to reap; a plain script
// must exit on purpose.
process.exit(0);
