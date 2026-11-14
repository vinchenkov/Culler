import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, gotoReady, mc, seedProject, selectProject, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket } from "./delivery-fixture";

let smoke: Smoke;
const task: Record<string, number> = {};
const packet: Record<string, { packetID: number; versionID: number }> = {};

function reportPath(ws: string, id: number) {
  return join(process.env["MC_ARTIFACTS_ROOT"]!, ws, String(id), "packet", "report.html");
}

function stamp(hoursAgo: number) {
  return new Date(Date.now() - hoursAgo * 3600_000).toISOString().replace("T", " ").slice(0, 19);
}

// Each review state has one packet in the alpha Project. The beta Project
// holds packets that alpha must never show, and the empty Project holds none.
beforeAll(async () => {
  smoke = await bootSmoke();
  for (const ws of ["alpha", "beta", "empty"]) await seedProject(smoke, ws, { vision: false, environmentReady: true });
  const plane = join(smoke.scratch, "reports", "alpha");
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "alpha"\naccess = "ro"\n`, { mode: 0o600 });
  const seeds: [string, string, number][] = [
    ["blocked", "alpha", 6], ["awaiting", "alpha", 3], ["approved", "alpha", 5], ["returned", "alpha", 4],
    ["merged-old", "alpha", 90], ["merged-new", "alpha", 80], ["review-cancel", "alpha", 70], ["board-cancel", "alpha", 60],
    ["replaced", "alpha", 2], ["revised", "alpha", 2], ["beta-awaiting", "beta", 1], ["beta-merged", "beta", 50],
  ];
  for (const [name, ws] of seeds) {
    task[name] = Number((await mc(["task", "add", `${name} task`, "--project", ws]))["task_id"]);
  }
  const db = openSpine(smoke);
  try {
    db.run("UPDATE settings SET review_wip_cap=10 WHERE id=1");
    for (const [name, , hoursAgo] of seeds) {
      const renderPath = reportPath(name.startsWith("beta") ? "beta" : "alpha", task[name]!);
      const body = `<!doctype html><h1>The ${name} report</h1>`;
      if (!name.startsWith("beta")) {
        mkdirSync(join(plane, String(task[name]), "packet"), { recursive: true });
        writeFileSync(join(plane, String(task[name]), "packet", "report.html"), body);
      }
      packet[name] = seedDeliveryPacket(db, {
        taskID: task[name]!, thesis: `The ${name} packet`, renderPath, createdAt: stamp(hoursAgo),
        reportSHA256: new Bun.CryptoHasher("sha256").update(body).digest("hex"),
      });
    }
    // A merge repair returns the task and retains its review slot.
    db.run(`UPDATE tasks SET status='seeded',correction_open=1,returned_kind='merge',source_version=NULL,
      candidate_id=NULL,report_path=NULL,thesis=NULL WHERE id=?`, [task.returned!]);
  } finally { db.close(); }
  const decide = (name: string, ...flags: string[]) => mc(["packet", "decide", String(task[name]), ...flags,
    "--packet", String(packet[name]!.packetID), "--version", String(packet[name]!.versionID)]);
  await mc(["task", "block", String(task.blocked), "--reason", "The operator must confirm the license."]);
  await decide("approved", "--approve");
  await decide("revised", "--revise", "--reason", "Rename the exported function.");
  for (const name of ["merged-old", "merged-new", "beta-merged"]) await decide(name, "--approve");
  await decide("review-cancel", "--cancel", "--reason", "The Review page cancel reason.");
  const board = (request: Record<string, unknown>) => mc(["board", "write", "--request", JSON.stringify(request)]);
  const version = async (name: string) => Number((await mc(["task", "get", String(task[name])]))["record_version"]);
  await board({ operation_id: "ledger-board-cancel", action: "cancel", task_id: task["board-cancel"],
    expected_version: await version("board-cancel"), reason: "The Board cancel reason." });
  await board({ operation_id: "ledger-replace", action: "revise", task_id: task.replaced,
    expected_version: await version("replaced"), description: "The revised requirements.", reason: "The operator changed the scope." });
  // The merge application archives an approved task. The expired report
  // keeps only its record in the spine.
  const merges = openSpine(smoke);
  try {
    for (const [name, decided] of [["merged-old", stamp(288)], ["merged-new", stamp(240)], ["beta-merged", stamp(260)]]) {
      merges.run("UPDATE tasks SET decided_at=? WHERE id=?", [decided, task[name!]]);
      merges.run("UPDATE tasks SET archived=1 WHERE id=?", [task[name!]]);
    }
    const expired = task["merged-old"];
    merges.run("INSERT INTO artifact_retention(task_id,expires_at,expired_at) VALUES (?,?,?)", [expired, "2026-09-20 09:14:00", "2026-09-20 09:14:00"]);
    merges.run("INSERT INTO task_artifacts(task_id,path,expired_at) VALUES (?,?,?)", [expired, reportPath("alpha", expired!), "2026-09-20 09:14:00"]);
  } finally { merges.close(); }
  await gotoReady(smoke);
  await smoke.page.getByTestId("project-alpha").waitFor({ state: "attached" });
}, 90000);
afterAll(async () => { await teardown(smoke); });

async function rows() {
  return smoke.page.getByTestId("packet-row").evaluateAll((nodes) => nodes.map((node) => Number((node as HTMLElement).dataset["task"])));
}
async function waitForRows(expected: number[]) {
  await smoke.page.waitForFunction((expected) =>
    JSON.stringify([...document.querySelectorAll('[data-testid="packet-row"]')].map((node) => Number((node as HTMLElement).dataset["task"]))) === JSON.stringify(expected),
  expected, { timeout: 15000 });
}
async function row(name: string) {
  return smoke.page.locator(`[data-testid="packet-row"][data-task="${task[name]}"]`).innerText();
}
/** Closes a reader that covers the window. A reader beside the queue column stays open. */
async function closeReader() {
  if (!(await smoke.page.getByTestId("reader-close").isVisible())) return;
  await smoke.page.getByTestId("reader-close").click();
  await smoke.page.getByTestId("packet-reader").waitFor({ state: "hidden" });
}
async function openReview(ws: string) {
  await closeReader();
  await smoke.page.getByTestId("tab-review").click();
  await selectProject(smoke, ws);
}
/** Opens the reader for one packet from the packet list. */
async function openRow(name: string) {
  await closeReader();
  await smoke.page.locator(`[data-testid="packet-row"][data-task="${task[name]}"]`).click();
  await smoke.page.waitForFunction((id) =>
    !(document.querySelector('[data-testid="packet-reader"]') as HTMLElement).hidden &&
    (document.querySelector('[data-testid="reader-title"]')?.textContent ?? "").startsWith(`Task #${id} · `), task[name]);
}
/** Waits until the reader place beside the queue column states one text. */
async function placeShows(text: string) {
  await smoke.page.waitForSelector(`[data-testid="reader-empty"]:text-is("${text}")`, { state: "visible" });
}
async function pressed(testid: string) {
  return smoke.page.getByTestId(testid).getAttribute("aria-pressed");
}

test("the Live queue shows the decisions owed first, then the approved and returned packets", async () => {
  const { page } = smoke;
  await openReview("alpha");
  await waitForRows([task.blocked!, task.awaiting!, task.approved!, task.returned!]);
  expect(await page.getByTestId("review-count").innerText()).toBe("2");
  const stat = (key: string) => page.getByTestId("review-stats").getByTestId(`stat-${key}`).locator("b").innerText();
  expect(await stat("awaiting")).toBe("2");
  expect(await stat("slots")).toBe("4/10");
  expect(await page.getByTestId("review-stats").getByTestId("stat-oldest").count()).toBe(0);
  expect(await row("blocked")).toContain("Decision Pending: The operator must confirm the license.");
  expect(await row("approved")).toContain("Approved");
  // The approved task can merge while another task has an open correction.
  expect(await page.locator(`[data-testid="packet-row"][data-task="${task.approved}"] [data-testid="packet-merge-state"]`).innerText())
    .toBe("Culler merges this change now.");
  expect(await row("returned")).toContain("Returned to the pipeline");
  const text = await page.getByTestId("packets").innerText();
  for (const absent of ["replaced", "revised", "merged-old", "merged-new", "review-cancel", "board-cancel", "beta-awaiting", "beta-merged"]) {
    expect(text).not.toContain(`${absent} task`);
  }
}, 30000);

test("decision controls appear only for a packet that awaits a decision", async () => {
  const { page } = smoke;
  await openReview("alpha");
  await openRow("awaiting");
  await page.frameLocator('[data-testid="packet-frame"]').getByText("The awaiting report").waitFor();
  await page.getByTestId("packet-decide").waitFor({ state: "visible" });
  expect(await page.getByTestId("approval-scope").count()).toBe(0);
  for (const name of ["approved", "returned", "blocked"]) {
    await openRow(name);
    await page.frameLocator('[data-testid="packet-frame"]').getByText(`The ${name} report`).waitFor();
    expect(await page.getByTestId("packet-decide").isVisible()).toBe(false);
  }
  expect(await page.getByTestId("reader-block-reason").innerText()).toBe("Decision Pending: The operator must confirm the license.");
  for (const name of ["approved", "returned"]) {
    await openRow(name);
    await page.frameLocator('[data-testid="packet-frame"]').getByText(`The ${name} report`).waitFor();
    expect(await page.getByTestId("approval-scope").isVisible()).toBe(false);
  }
  expect(await page.getByTestId("packet-decision").innerText()).toBe("Returned to the pipeline.");
  await openRow("approved");
  await page.getByTestId("packet-decision").waitFor();
  expect(await page.getByTestId("packet-decision").innerText())
    .toMatch(/^Approved on \w{3} \d{1,2}, \d{4} \d{2}:\d{2}\. Culler merges this change now\.$/);
  await closeReader();
}, 30000);

test("the ledger shows approved packets by default, newest decision first", async () => {
  const { page } = smoke;
  await openReview("alpha");
  await page.getByTestId("packet-view-ledger").click();
  expect(await pressed("ledger-filter-approved")).toBe("true");
  await waitForRows([task["merged-new"]!, task["merged-old"]!]);
  expect(await row("merged-new")).toContain("Merged into culler.");
  expect(await row("merged-new")).not.toContain("in queue");
  expect(await page.getByTestId("ledger-filter-approved").innerText()).toBe("Approved 2");
  expect(await page.getByTestId("ledger-filter-cancelled").innerText()).toBe("Cancelled 2");
  expect(await page.getByTestId("ledger-filter-all").innerText()).toBe("All 4");
  expect(await page.getByTestId("review-stats").isVisible()).toBe(false);
  expect(await page.getByTestId("queue-note").innerText()).toBe("newest decision first");

  await page.getByTestId("ledger-filter-cancelled").click();
  await waitForRows([task["board-cancel"]!, task["review-cancel"]!]);
  expect(await row("board-cancel")).toContain("Reason: The Board cancel reason.");
  expect(await row("review-cancel")).toContain("Reason: The Review page cancel reason.");

  await page.getByTestId("ledger-filter-all").click();
  await waitForRows([task["board-cancel"]!, task["review-cancel"]!, task["merged-new"]!, task["merged-old"]!]);
  const text = await page.getByTestId("packets").innerText();
  for (const absent of ["replaced", "awaiting", "approved", "returned", "beta-awaiting", "beta-merged"]) {
    expect(text).not.toContain(`${absent} task`);
  }
}, 30000);

test("a ledger packet with an expired report shows its record without an error", async () => {
  const { page } = smoke;
  await openReview("alpha");
  await page.getByTestId("packet-view-ledger").click();
  await page.getByTestId("ledger-filter-approved").click();
  await waitForRows([task["merged-new"]!, task["merged-old"]!]);
  await openRow("merged-old");
  await page.getByTestId("packet-expired").waitFor();
  expect(await page.getByTestId("packet-expired").innerText()).toMatch(/The packet report expired on Sep \d{1,2}, 2026 \d{2}:\d{2}\./);
  expect(await page.getByTestId("packet-decision").innerText()).toMatch(/Approved on \w{3} \d{1,2}, \d{4} \d{2}:\d{2}\. Merged into culler at [0-9a-f]{40}\./);
  expect(await page.getByTestId("packet-render").innerText()).toContain("The merged-old packet");
  expect(await page.getByTestId("approval-scope").isVisible()).toBe(false);
  expect(await page.getByTestId("packet-frame").count()).toBe(0);
  expect(await page.getByTestId("packet-decide").isVisible()).toBe(false);
  // The Files changed view keeps the commits and states the expiry of the change data.
  await page.getByTestId("reader-view-changes").click();
  await page.getByTestId("changes-unavailable").waitFor();
  expect(await page.getByTestId("changes-unavailable").innerText()).toMatch(/^The change data expired on Sep \d{1,2}, 2026 \d{2}:\d{2}\. The repository keeps the commits\.$/);
  expect(await page.getByTestId("changes-facts").locator('[data-key="worker"] button').getAttribute("title")).toMatch(/^[0-9a-f]{40}$/);
  expect(await page.getByTestId("error").isVisible()).toBe(false);
  expect(await page.getByTestId("reader-error").isVisible()).toBe(false);

  await openRow("merged-new");
  await page.frameLocator('[data-testid="packet-frame"]').getByText("The merged-new report").waitFor();
  expect(await page.getByTestId("packet-decide").isVisible()).toBe(false);
  await closeReader();
}, 30000);

test("the ledger filter keeps its selection until the page loads again", async () => {
  const { page } = smoke;
  await openReview("alpha");
  await page.getByTestId("packet-view-ledger").click();
  await page.getByTestId("ledger-filter-cancelled").click();
  await waitForRows([task["board-cancel"]!, task["review-cancel"]!]);
  await page.getByTestId("tab-board").click();
  await page.getByTestId("tab-review").click();
  await waitForRows([task["board-cancel"]!, task["review-cancel"]!]);
  await page.getByTestId("packet-view-live").click();
  expect(await page.getByTestId("ledger-filters").isVisible()).toBe(false);
  await page.getByTestId("packet-view-ledger").click();
  expect(await pressed("ledger-filter-cancelled")).toBe("true");
  await selectProject(smoke, "beta");
  await placeShows("No cancelled packets yet.");
  expect(await pressed("ledger-filter-cancelled")).toBe("true");
  await page.getByTestId("ledger-filter-approved").click();
  await waitForRows([task["beta-merged"]!]);

  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await page.getByTestId("tab-review").click();
  await page.getByTestId("packet-view-ledger").click();
  expect(await pressed("ledger-filter-approved")).toBe("true");
}, 30000);

test("each view names its empty state", async () => {
  const { page } = smoke;
  await openReview("empty");
  await page.getByTestId("packet-view-live").click();
  // Beside the queue column, the reader place states the empty list, and the
  // queue column shows no empty box.
  await placeShows("Nothing awaiting decision.");
  expect(await page.getByTestId("review-empty").isVisible()).toBe(false);
  expect(await page.getByTestId("review-count").isVisible()).toBe(false);
  await page.getByTestId("packet-view-ledger").click();
  for (const [filter, text] of [["approved", "No approved packets yet."], ["cancelled", "No cancelled packets yet."], ["all", "No decided packets yet."]]) {
    await page.getByTestId(`ledger-filter-${filter}`).click();
    await placeShows(text!);
    expect(await page.getByTestId("packets").getByText(text!).isVisible()).toBe(false);
  }
  // In a narrow window, the queue column states the empty list.
  const wide = page.viewportSize()!;
  await page.setViewportSize({ width: 390, height: 844 });
  try {
    await page.getByTestId("packets").getByText("No decided packets yet.").waitFor();
    await page.getByTestId("packet-view-live").click();
    await page.getByTestId("review-empty").waitFor({ state: "visible" });
    expect(await page.getByTestId("review-empty").innerText()).toBe("Nothing awaiting decision.");
    await page.getByTestId("packet-view-ledger").click();
  } finally { await page.setViewportSize(wide); }
  await page.getByTestId("ledger-filter-approved").click();
  await page.getByTestId("packet-view-live").click();
  await selectProject(smoke, "alpha");
}, 30000);

test("a failed read and a delayed read never show another state as data", async () => {
  const { page, server } = smoke;
  await page.route("**/api/packets", (route) => route.fulfill({ status: 503, json: { error: { message: "The packet read is unavailable." } } }));
  try {
    await page.goto(`${server.url}?ledger-failure=${crypto.randomUUID()}#review`);
    await page.waitForSelector("body[data-ready]");
    await page.getByTestId("review-pane").getByText("The packet read is unavailable.").first().waitFor();
    expect(await page.getByTestId("review-pane").getByRole("button", { name: "Retry", exact: true }).isVisible()).toBe(true);
    expect(await rows()).toEqual([]);
  } finally { await page.unroute("**/api/packets"); }

  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/packets", async (route) => { await gate; await route.continue(); });
  try {
    await page.goto(`${server.url}?ledger-delay=${crypto.randomUUID()}#review`);
    await page.waitForSelector("body[data-ready]");
    await placeShows("Loading packets…");
    expect(await page.getByTestId("review-empty").isVisible()).toBe(false);
    release();
    await page.getByTestId("packet-row").first().waitFor();
  } finally {
    release();
    await page.unrouteAll({ behavior: "wait" });
  }
}, 30000);
