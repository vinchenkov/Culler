import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket } from "./delivery-fixture";

let smoke: Smoke;
const task: Record<string, number> = {};

const CRITERIA = "The export job must meet these criteria:\n\n- The job repeats a failed request.\n- The third failure stops the job.";
const RATIONALE = [
  "**Proposed change:** add a retry to the export job.",
  "Observed: the export job stops at the first network error.",
  "Assumed: the operator repeats a stopped export by hand.",
].join("\n\n");
const STATEMENT = "The export job now repeats a failed request three times.";
const SUMMARY = "The export job repeats a failed request.";

const reportOf = (heading: string, body: string) => `<!doctype html><html><head><meta charset="utf-8"><title>Report</title></head><body>
<h1>The export report</h1>
<nav aria-label="Contents"><ol><li><a href="#first">${heading}</a></li><li><a href="#risk">Risk and rollback</a></li></ol></nav>
<h2 id="first">${heading}</h2>
${body}
<h2 id="risk">Risk and rollback</h2>
<p>The record holds no risk.</p>
</body></html>`;
const STRATEGIST_REPORT = reportOf("Strategic rationale", `<p>${STATEMENT}</p>
<p>The Strategist wrote this rationale:</p>
<p><strong>Proposed change:</strong> add a retry to the export job.</p>
<p>Observed: the export job stops at the first network error.</p>
<p>Assumed: the operator repeats a stopped export by hand.</p>`);
const OPERATOR_REPORT = reportOf("Summary", `<p>${SUMMARY}</p>`);

// Each seed is one task with a packet. A seed without a report shows the
// opening text of its packet in the report area.
const SEEDS: { name: string; strategist: boolean; report: string | null }[] = [
  { name: "strategist-report", strategist: true, report: STRATEGIST_REPORT },
  { name: "strategist-absent", strategist: true, report: null },
  { name: "operator-report", strategist: false, report: OPERATOR_REPORT },
  { name: "operator-absent", strategist: false, report: null },
];

beforeAll(async () => {
  smoke = await bootSmoke();
  await smoke.page.setViewportSize({ width: 1440, height: 1000 });
  await seedProject(smoke, "rationale", { title: "Rationale", vision: false });
  const plane = join(smoke.scratch, "reports", "rationale");
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "rationale"\naccess = "ro"\n`, { mode: 0o600 });
  const add = async (title: string) =>
    Number((await mc(["task", "add", title, "--project", "rationale", "--description", CRITERIA]))["task_id"]);
  task.operator = await add("Operator export task");
  for (const { name, strategist } of SEEDS) if (!strategist) task[name] = await add(`${name} task`);
  const db = openSpine(smoke);
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.run("UPDATE settings SET review_wip_cap=10 WHERE id=1");
    const propose = (title: string) => Number(db.run(
      "INSERT INTO tasks(title,status,project,description,origin,strategic_rationale) VALUES (?,'proposed','rationale',?,'autonomous',?)",
      [title, CRITERIA, RATIONALE]).lastInsertRowid);
    task.strategist = propose("Strategist export task");
    for (const { name, strategist } of SEEDS) if (strategist) task[name] = propose(`${name} task`);
    for (const { name, strategist, report } of SEEDS) {
      const id = task[name]!;
      if (report !== null) {
        mkdirSync(join(plane, String(id), "packet"), { recursive: true });
        writeFileSync(join(plane, String(id), "packet", "report.html"), report);
      }
      seedDeliveryPacket(db, {
        taskID: id,
        // The opening text of a task that the Strategist proposed: the statement of the Packager, then the rationale.
        thesis: strategist ? `${STATEMENT}\n\n${RATIONALE}` : SUMMARY,
        renderPath: report === null ? "" : join(process.env["MC_ARTIFACTS_ROOT"]!, "rationale", String(id), "packet", "report.html"),
        ...(report === null ? {} : { reportSHA256: new Bun.CryptoHasher("sha256").update(report).digest("hex") }),
      });
    }
  } finally { db.close(); }
  await gotoReady(smoke);
  await smoke.page.getByTestId("project-rationale").waitFor({ state: "attached" });
  await selectProject(smoke, "rationale");
}, 90000);
afterAll(async () => { await teardown(smoke); });

async function openTask(id: number) {
  const { page } = smoke;
  if (await page.getByTestId("task-dialog").isVisible()) await page.getByTestId("task-dialog-close").click();
  await page.getByTestId("tab-board").click();
  await page.locator(`[data-testid="board-card"][data-task="${id}"]`).getByTestId("board-card-open").click();
  await page.locator('[data-testid="task-dialog"][open]').waitFor();
}

/** The label of each content section of the open task dialog, in page order. */
const sectionLabels = () => smoke.page.locator("#task-dialog-body section.sec > .sec-head > .sec-label")
  .evaluateAll(nodes => nodes.map(node => node.textContent ?? ""));

async function openPacket(name: string) {
  const { page, server } = smoke;
  await page.goto(`${server.url}?rationale-${name}#review/${task[name]}`);
  await page.waitForSelector("body[data-ready]");
  await page.waitForFunction((id) =>
    (document.querySelector('[data-testid="reader-title"]')?.textContent ?? "").startsWith(`Task #${id} · `), task[name], { timeout: 15000 });
}

const report = () => smoke.page.frameLocator('[data-testid="packet-frame"]');

/** The heading and the text of the opening section in the report area. */
async function opening() {
  const section = smoke.page.locator('[data-testid="packet-render"] .report-section').first();
  return { heading: await section.locator("h3").textContent(), text: await section.getByTestId("packet-opening").innerText() };
}

test("a task that the Strategist proposed shows Strategic rationale as the first content section, above Description", async () => {
  const { page } = smoke;
  await openTask(task.strategist!);
  const rationale = page.getByTestId("task-dialog-rationale");
  // The complete task read supplies the rationale and the description.
  await rationale.getByText("Observed: the export job stops at the first network error.").waitFor();
  const labels = await sectionLabels();
  expect(labels.slice(0, 2)).toEqual(["Strategic rationale", "Description"]);
  const text = await rationale.innerText();
  expect(text).toContain("Proposed change: add a retry to the export job.");
  expect(text).toContain("Observed: the export job stops at the first network error.");
  expect(text).toContain("Assumed: the operator repeats a stopped export by hand.");
  expect(await rationale.locator("strong").first().textContent()).toBe("Proposed change:");
  // The description holds the criteria and no part of the rationale.
  const description = page.locator("#task-dialog-body section.sec").nth(1);
  expect(await description.innerText()).toContain("The third failure stops the job.");
  expect(await description.innerText()).not.toContain("Observed:");
  const boxes = await Promise.all([rationale.boundingBox(), description.boundingBox()]);
  expect(boxes[0]!.y + boxes[0]!.height).toBeLessThanOrEqual(boxes[1]!.y + 0.5);
  await page.screenshot({ animations: "disabled", path: evidencePath("strategic-rationale/task-dialog-strategist.png") });
  await page.getByTestId("task-dialog-close").click();
}, 30000);

test("a task that the operator defined shows no Strategic rationale section", async () => {
  const { page } = smoke;
  await openTask(task.operator!);
  await page.locator("#task-dialog-body").getByText("The third failure stops the job.").waitFor();
  const labels = await sectionLabels();
  expect(labels[0]).toBe("Description");
  expect(labels).not.toContain("Strategic rationale");
  expect(await page.getByTestId("task-dialog-rationale").count()).toBe(0);
  expect(await page.locator("#task-dialog-body").innerText()).not.toMatch(/strategic rationale/i);
  await page.screenshot({ animations: "disabled", path: evidencePath("strategic-rationale/task-dialog-operator.png") });
  await page.getByTestId("task-dialog-close").click();
}, 30000);

test("the Review Packet of a task that the Strategist proposed starts with Strategic rationale", async () => {
  const { page } = smoke;
  await openPacket("strategist-report");
  await report().getByRole("heading", { name: "Strategic rationale", exact: true }).waitFor();
  const sections = await report().locator("h2").allTextContents();
  expect(sections).toEqual(["Strategic rationale", "Risk and rollback"]);
  const body = await report().locator("body").innerText();
  expect(body).toContain(STATEMENT);
  expect(body).toContain("Observed: the export job stops at the first network error.");
  expect(body.indexOf(STATEMENT)).toBeLessThan(body.indexOf("Proposed change:"));
  await page.screenshot({ animations: "disabled", path: evidencePath("strategic-rationale/packet-strategist.png") });

  // Without a report, the report area shows the stored opening text under the same heading.
  await openPacket("strategist-absent");
  await page.getByTestId("packet-render").getByText("No report exists yet for this packet.").waitFor();
  const shown = await opening();
  expect(shown.heading).toBe("Strategic rationale");
  expect(shown.text.startsWith(STATEMENT)).toBe(true);
  expect(shown.text).toContain("Proposed change: add a retry to the export job.");
  expect(shown.text).toContain("Assumed: the operator repeats a stopped export by hand.");
  expect(await page.getByTestId("packet-opening").locator("strong").first().textContent()).toBe("Proposed change:");
  await page.screenshot({ animations: "disabled", path: evidencePath("strategic-rationale/packet-strategist-absent.png") });
}, 30000);

test("the Review Packet of a task that the operator defined starts with Summary", async () => {
  const { page } = smoke;
  await openPacket("operator-report");
  await report().getByRole("heading", { name: "Summary", exact: true }).waitFor();
  expect(await report().locator("h2").allTextContents()).toEqual(["Summary", "Risk and rollback"]);
  expect(await report().locator("body").innerText()).not.toContain("Strategic rationale");

  await openPacket("operator-absent");
  await page.getByTestId("packet-render").getByText("No report exists yet for this packet.").waitFor();
  const shown = await opening();
  expect(shown.heading).toBe("Thesis");
  expect(shown.text).toBe(SUMMARY);
  expect(await page.getByTestId("packet-render").innerText()).not.toMatch(/strategic rationale/i);
}, 30000);
