// pages.smoke.test.ts — the Board, Pipeline, Configs and System Logs smokes:
// each page proves it renders real spine (or host-file) state end-to-end
// through its own mc read verb, in a real browser, with the polished DOM.
// Boots its own scratch deployment (harness.ts re-points the process env;
// files run sequentially).

import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import { evidencePath, SMOKE_BEARER, bootSmoke, seedProject, gotoReady, mc, selectProject, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket } from "./delivery-fixture";

let smoke: Smoke;
let packetTask = 0;
let packetRecord = 0;
let packetVersion = 0;

// The panel shows Close and a maximum of one other navigation control.
const panelNavigation = () => smoke.page.getByTestId("agent-profiles-panel")
  .locator('[data-testid="profile-close"], [data-testid="profile-back"], [data-testid="profile-list"]').allTextContents();


async function profileHealth() {
  await smoke.page.route("**/api/system/status", route => route.fulfill({ json: {
    complete: true, observed_at: new Date().toISOString(), connections: [], conditions: [],
  } }));
  const response = smoke.page.waitForResponse("**/api/system/status");
  await smoke.page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await (await response).finished();
}

beforeAll(async () => {
  smoke = await bootSmoke();
  // Seed two projects and two seeded tasks so the Board has rows to show
  // AND something for its project filter to filter, plus one supervision
  // log with a known tail.
  await seedProject(smoke, "ws-smoke", { title: "Smoke", vision: false });
  await seedProject(smoke, "ws-other", { title: "Other", vision: false });
  await mc(["task", "add", "smoke task", "--project", "ws-smoke", "--description", "Smoke description canary"]);
  await mc(["task", "add", "other task", "--project", "ws-other"]);
  mkdirSync(join(smoke.home, "logs"));
  writeFileSync(
    join(smoke.home, "logs", "resident.stderr.log"),
    "tick one\ntick two\nresident stderr canary line 3\n",
  );
  // The structured channel the pane now opens on. Written as records, because
  // the verb renders them server-side and the point of the assertion is that
  // the browser receives text it did not have to parse.
  writeFileSync(
    join(smoke.home, "logs", "resident.ndjson"),
    `{"ts":"2026-08-18T20:00:00.000Z","level":"info","comp":"resident","event":"tick.completed","msg":"tick done","run_id":"smokerun1"}\n` +
      `{"ts":"2026-08-18T20:00:01.000Z","level":"warn","comp":"resident","event":"reap.stop_failed","msg":"resident events canary","run_id":"ab12"}\n`,
  );
  writeFileSync(join(smoke.home, "logs", "dashboard.ndjson"),
    JSON.stringify({ ts: "2026-08-18T20:00:00.000Z", level: "info", comp: "dashboard",
      event: "cli.invoked", msg: "dashboard canary", run_id: "dashboard-run" }) + "\n");
  // One run's captured diagnostics: the per-run file a
  // container's runner writes through its /mc/logs bind.
  mkdirSync(join(smoke.home, "logs", "runs", "smokerun1"), { recursive: true });
  writeFileSync(
    join(smoke.home, "logs", "runs", "smokerun1", "runner.ndjson"),
    `{"ts":"2026-08-18T20:02:00.000Z","level":"error","comp":"runner","event":"adapter.launch_failed","msg":"run diagnostics canary"}\n`,
  );

  // The retained packet contains an external image that the reader must reject.
  const added = await mc(["task", "add", "packet smoke task", "--project", "ws-smoke"]);
  packetTask = Number(added["task_id"]);
  const plane = join(smoke.scratch, "artifacts", "ws-smoke");
  mkdirSync(join(plane, String(packetTask), "packet"), { recursive: true });
  writeFileSync(
    join(smoke.home, "mount-allowlist"),
    `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "ws-smoke"\naccess = "ro"\n`,
    { mode: 0o600 },
  );
  writeFileSync(
    join(plane, String(packetTask), "packet", "packet.html"),
    `<!doctype html><html><head><title>packet</title></head><body>` +
      `<h1>Packet smoke canary</h1>` +
      `<img src="http://127.0.0.1:1/leak.png" alt="an external fetch the frame must refuse">` +
      `<p>media rides inline or not at all</p></body></html>`,
  );
  const db = openSpine(smoke);
  const packet = seedDeliveryPacket(db, {
    taskID: packetTask,
    renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "ws-smoke", String(packetTask), "packet", "packet.html"),
    reportSHA256: new Bun.CryptoHasher("sha256").update(readFileSync(join(plane, String(packetTask), "packet", "packet.html"))).digest("hex"),
    thesis: "Smoke: the packet renders as sandboxed HTML",
    createdAt: new Date(Date.now() - 3600000).toISOString().slice(0, 19).replace("T", " "),
  });
  packetRecord = packet.packetID;
  packetVersion = packet.versionID;
  db.close();
  await gotoReady(smoke);
}, 60000);

afterAll(async () => {
  await teardown(smoke);
}, 30000);

test("a Docker outage explains the recovery action and clears after the daemon returns", async () => {
  const { page, server } = smoke;
  const message = "ensure warm helper: docker image inspect mc-prod: exit status 1: failed to connect to the docker API at unix:///Users/operator/.docker/run/docker.sock; check if the path is correct and if the daemon is running: dial unix /Users/operator/.docker/run/docker.sock: connect: no such file or directory";
  const pattern = "**/api/**";
  await page.route(pattern, async (route) => {
    if (new URL(route.request().url()).pathname.startsWith("/api/logs/resident")) {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 400,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "usage", message } }),
    });
  });
  try {
    await page.goto(`${server.url}?docker-outage#board`);
    const notice = page.getByTestId("docker-error");
    await notice.waitFor({ timeout: 15000 });
    expect(await notice.innerText()).toContain("Open Docker Desktop and wait for Docker to start.");
    expect(await notice.innerText()).toContain("This dashboard reconnects automatically.");
    expect(await notice.innerText()).not.toContain("docker.sock");
    expect(await page.getByTestId("error").isVisible()).toBe(false);
    expect(await page.getByTestId("lease-badge").innerText()).toContain("Docker is unavailable");
    expect(await page.getByTestId("board-columns").isVisible()).toBe(false);
    expect(await page.getByTestId("board-pending").isVisible()).toBe(false);
    expect(await page.getByTestId("board-project").getByRole("button", { name: "Retry" }).isVisible()).toBe(true);

    await notice.getByText("Technical details").click();
    expect(await notice.locator("pre").innerText()).toBe(message);
    await page.setViewportSize({ width: 390, height: 844 });
    const bounds = await notice.boundingBox();
    expect(bounds).not.toBeNull();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);

    await page.getByTestId("tab-logs").click();
    await page.waitForFunction(() =>
      document.querySelector('[data-testid="logs-lines"]')?.textContent?.includes("resident events canary"),
    );
    expect(await notice.isVisible()).toBe(true);
    expect(await page.getByTestId("lease-badge").innerText()).toContain("Docker is unavailable");

    const refused = page.waitForResponse((response) => response.url().endsWith("/api/tasks") && response.status() === 400);
    await page.getByTestId("tab-board").click();
    await refused;
    await page.unroute(pattern);
    await notice.waitFor({ state: "hidden", timeout: 20000 });
    expect(await notice.isVisible()).toBe(false);
    expect(await page.getByTestId("lease-badge").innerText()).toContain("No agents are running");
  } finally {
    await page.unroute(pattern);
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.goto(`${server.url}#board`);
    await page.getByTestId("board-column-seeded").waitFor({ timeout: 15000 });
  }
}, 60000);

test("the seeded Board renders cards in their stage column", async () => {
  const { page } = smoke;
  // Board is the default tab; the seeded tasks render as cards in the
  // `seeded` column with the facts their rows carry.
  await selectProject(smoke, "ws-smoke");
  const column = page.locator('[data-testid="board-column-seeded"]');
  await column.locator('[data-testid="board-card"]').first().waitFor({ timeout: 15000 });
  const text = (await column.textContent()) ?? "";
  expect(text).toContain("smoke task");
  // Two Projects on an unfiltered board, so each card names its own.
  expect(text).not.toContain("other task");
  const cards = column.getByTestId("board-card");
  const firstCard = cards.first();
  expect(await firstCard.locator(".card-id").innerText()).toBe(`#${await firstCard.getAttribute("data-task")}`);
  expect(await column.locator(".column-count").innerText()).toBe(String(await cards.count()));
  // The task dialog holds the origin and description.
  expect(text).not.toContain("origin");
  expect(text).not.toContain("Smoke description canary");
  // Every stage gets a column, empty ones included — a stage with no work
  // is a fact about the pipeline, not a reason to hide it.
  for (const status of ["proposed", "seeded", "worked", "verified", "packaged"]) {
    expect(await page.isVisible(`[data-testid="board-column-${status}"]`)).toBe(true);
  }
  // Nothing holds the lease on a fresh deployment, so no card is active.
  expect(await page.locator(".card.active").count()).toBe(0);
  expect(await page.textContent('[data-testid="lease-badge"]')).toContain("No agents are running");
}, 30000);

test("a Board card opens the task dialog on the task's full row", async () => {
  const { page } = smoke;
  const card = page.locator('[data-testid="board-column-seeded"] [data-testid="board-card"]', {
    hasText: "smoke task",
  });
  await card.locator('[data-testid="board-card-open"]').click();
  const dialog = page.locator('[data-testid="task-dialog"][open]');
  await dialog.waitFor({ timeout: 15000 });
  expect(await dialog.locator("#task-dialog-title").textContent()).toContain("smoke task");
  // The get verb's prose lands once its read does; the run history and the
  // facts grid come with it.
  await dialog.locator("text=Smoke description canary").waitFor({ timeout: 15000 });
  const text = (await dialog.textContent()) ?? "";
  expect(text).toContain("No run yet.");
  const facts = (await dialog.locator('[data-testid="task-dialog-facts"]').textContent()) ?? "";
  expect(facts).toContain("user");
  expect(facts).toMatch(/#\d+/);
  expect(await card.evaluate((node) => node.classList.contains("open"))).toBe(true);
  // The close button shuts it, and the card drops its ring.
  await dialog.locator('[data-testid="task-dialog-close"]').click();
  await page.waitForFunction(() => !document.querySelector('[data-testid="task-dialog"]')!.hasAttribute("open"));
  await page.waitForFunction(() => !document.querySelector('[data-testid="board-card"].open'));
}, 60000);

test("the Board names every Project that requires VISION.md", async () => {
  const { page } = smoke;
  const notice = page.getByTestId("board-vision-notice");
  await notice.waitFor({ timeout: 15000 });
  expect(await notice.innerText()).toContain("needs a Project Vision");
  expect(await notice.innerText()).toContain("Describe the Project goal so Culler can plan work.");
  expect(await notice.innerText()).not.toContain("mc onboard");
  expect(await page.getByTestId("board-vision-ws-smoke").isVisible()).toBe(true);
  expect(await page.getByTestId("board-vision-ws-other").isVisible()).toBe(true);
  expect(await page.locator("#board-project .project-action").count()).toBe(0);
  expect(await page.getByTestId("board-vision-ws-smoke").locator("summary").innerText()).toBe("Why Vision is required");
  await page.getByTestId("board-vision-ws-smoke").locator("summary").click();
  expect(await page.getByTestId("board-vision-ws-smoke").innerText()).toContain("Required file:");
  expect(await notice.locator(".attention, .tone-rust").count()).toBe(0);
  expect(await notice.locator("h3").first().isVisible()).toBe(true);
}, 30000);

test("the Project switcher changes the Board", async () => {
  const { page } = smoke;
  await selectProject(smoke, "ws-other");
  const seeded = page.getByTestId("board-column-seeded");
  await page.waitForFunction(() => document.querySelector('[data-testid="board-column-seeded"]')?.textContent?.includes("other task"));
  expect(await seeded.innerText()).not.toContain("smoke task");
  await selectProject(smoke, "ws-smoke");
  await page.waitForFunction(() => document.querySelector('[data-testid="board-column-seeded"]')?.textContent?.includes("smoke task"));
  expect(await seeded.innerText()).not.toContain("other task");
}, 30000);

test("the Pipeline tab shows the topology and recent runs; Configs checks the deployment on demand", async () => {
  const { page, browserLog, server } = smoke;
  await page.click('[data-testid="tab-pipeline"]');
  await page.waitForSelector('[data-testid="pipeline-pane"]', { state: "visible" });

  try {
    await page.waitForSelector('[data-testid="topology-node-worker"]', { timeout: 15000 });
  } catch {
    throw new Error(`the topology never rendered (browser log: ${browserLog.join(" | ")})`);
  }
  for (const role of ["strategist", "arbiter", "worker", "verifier", "packager", "scribe"]) {
    expect(await page.isVisible(`[data-testid="topology-node-${role}"]`)).toBe(true);
  }
  await page.getByTestId("pipeline-current-run").getByText("No active task run", { exact: true }).waitFor();
  const viewport = page.viewportSize()!;
  try {
    for (const [layout, width] of [["wide", 1600], ["narrow", 1000]] as const) {
      await page.setViewportSize({ width, height: 900 });
      await page.waitForFunction((layout) =>
        document.querySelector('[data-testid="topology-graph"]')?.getAttribute("data-layout") === layout, layout);
      expect(await page.getByTestId("topology-graph").isVisible()).toBe(true);
      expect(await page.getByTestId("topology-edge-correction").count()).toBe(0);
      expect(await page.getByTestId("pipeline-correction").count()).toBe(0);
      expect(await page.getByTestId("pipeline-current-run").innerText()).toContain("No active task run");
      expect(await page.evaluate(() => {
        const cards = [...document.querySelectorAll(".role-cards .role-card")].map(node => node.getBoundingClientRect());
        return cards.length === 5 && cards.every((card, i) => card.width > 0 && card.left >= 0 && card.right <= innerWidth &&
          cards.every((other, j) => i === j || card.right <= other.left || other.right <= card.left || card.bottom <= other.top || other.bottom <= card.top));
      })).toBe(true);
      await page.screenshot({ path: evidencePath(`pipeline-role-map-${layout}.png`), fullPage: false, animations: "disabled" });
    }
  } finally {
    await page.setViewportSize(viewport);
  }
  // The completed runs leave each role idle.
  expect(await page.locator(".node.running").count()).toBe(0);
  expect(await page.textContent('[data-testid="topology-node-worker"]')).toContain("Idle");
  expect(await page.textContent('[data-testid="topology-node-worker"]')).toContain("last run");
  // The role opens a centered dialog.
  const worker = page.getByTestId("topology-node-worker");
  const detail = page.getByTestId("topology-side");
  await page.getByTestId("topology-node-strategist").click();
  await detail.getByRole("heading", { name: "Strategist", exact: true }).waitFor();
  await detail.getByTestId("topology-custom-instructions").click();
  await page.getByTestId("role-customization").getByRole("heading", { name: "Guide the Strategist", exact: true }).waitFor();
  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await worker.click();
  await detail.getByTestId("topology-custom-instructions").getByText("None", { exact: true }).waitFor();
  expect(await page.locator("dialog[open]").count()).toBe(1);
  expect(await worker.getAttribute("aria-expanded")).toBe("true");
  expect(await detail.textContent()).toContain("The Worker works one task in one worktree.");
  expect(await detail.getByTestId("topology-change-profile").textContent()).toContain("All Projects");
  expect(await detail.getByTestId("topology-custom-instructions").textContent()).toContain("This Project");
  expect(await page.evaluate(() => {
    const box = (id: string) => document.querySelector(`[data-testid="${id}"]`)!.getBoundingClientRect();
    const side = box("topology-side");
    return Math.abs(side.left + side.width / 2 - innerWidth / 2) <= 1 && side.top >= 0 && side.bottom <= innerHeight;
  })).toBe(true);
  await page.screenshot({ path: evidencePath("pipeline-role-detail.png"), fullPage: false, animations: "disabled" });
  // Each row opens one dialog. The role detail stays open behind the dialog.
  await detail.getByTestId("topology-custom-instructions").click();
  await page.getByTestId("role-customization").getByRole("heading", { name: "Guide the Worker", exact: true }).waitFor();
  expect(await page.getByTestId("customization-role").count()).toBe(0);
  await page.screenshot({ path: evidencePath("pipeline-custom-instructions.png"), fullPage: false, animations: "disabled" });
  await page.keyboard.press("Escape");
  expect(await page.getByTestId("role-customization").count()).toBe(0);
  expect(await detail.getByTestId("topology-custom-instructions").evaluate((node) => node === document.activeElement)).toBe(true);
  await detail.getByTestId("topology-change-profile").click();
  await page.getByTestId("profile-assignment").waitFor();
  expect(await page.locator("#agent-profiles-title").textContent()).toBe("Worker agent profile");
  expect(await page.getByTestId("agent-profiles-panel").textContent()).toContain("This assignment applies to all Projects.");
  // The assignment view is a dialog in the middle of the page.
  expect(await page.getByTestId("agent-profiles-panel").evaluate((node) => {
    const box = node.getBoundingClientRect();
    return box.left > 0 && box.right < innerWidth && box.top > 0 && box.bottom < innerHeight;
  })).toBe(true);
  await page.screenshot({ path: evidencePath("pipeline-agent-profile.png"), fullPage: false, animations: "disabled" });
  await page.getByTestId("profile-close").click();
  await detail.waitFor();
  // Escape closes the role detail and returns the focus to the role.
  await detail.getByTestId("topology-change-profile").focus();
  await page.keyboard.press("Escape");
  await page.getByTestId("topology-side").waitFor({ state: "detached" });
  expect(await page.locator('[data-testid="topology-side"]').count()).toBe(0);
  expect(await worker.evaluate((node) => node === document.activeElement)).toBe(true);
  // A press outside the role detail closes it.
  await worker.click();
  await detail.waitFor();
  await page.mouse.click(2, 2);
  await page.getByTestId("topology-side").waitFor({ state: "detached" });
  expect(await page.locator('[data-testid="topology-side"]').count()).toBe(0);

  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-agents").click();
  await page.getByTestId("config-route-worker").click();
  await page.waitForSelector('[data-testid="topology-route-picker"]');
  expect(await page.inputValue('[data-testid="topology-route-picker"]')).toBe("fake");
  expect(await page.$$eval('[data-testid="topology-route-picker"] option', (opts) => opts.map((o) => (o as HTMLOptionElement).value))).toEqual([
    "claude",
    "fake",
    "__create__",
  ]);
  expect(await page.locator('[data-testid="topology-route-picker"] option').allTextContents()).toEqual([
    "claude · claude-sdk · claude-opus-4-8",
    "fake · fake · —",
    "Create agent profile…",
  ]);
  await page.selectOption('[data-testid="topology-route-picker"]', "claude");
  await page.waitForFunction(
    () => document.querySelector('[data-testid="topology-side-harness"]')?.textContent === "claude-sdk",
  );
  expect(await page.textContent('[data-testid="topology-side-model"]')).toBe("claude-opus-4-8");
  expect(await page.inputValue('[data-testid="topology-route-picker"]')).toBe("claude");
  expect(await page.locator('[data-testid="topology-route-picker-error"]').count()).toBe(0);
  const moved = (await mc(["routing", "show"])).routes as Array<Record<string, unknown>>;
  expect(moved.find((row) => row.role === "worker")).toMatchObject({
    binding: "claude", harness: "claude-sdk", model: "claude-opus-4-8",
  });
  expect(moved.find((row) => row.role === "verifier")).toMatchObject({ binding: "fake" });

  // The rejection path through the real verb. The picker lists only enabled
  // bindings, so the disabled id goes straight to the route; the table stays
  // as it was.
  const refused = await fetch(`${server.url}api/routing`, {
    method: "POST",
    headers: { ...SMOKE_BEARER, "content-type": "application/json" },
    body: JSON.stringify({ role: "worker", binding: "not-enabled" }),
  });
  expect(refused.status).toBe(400);
  const refusal = (await refused.json()) as { error: { message: string } };
  expect(refusal.error.message).toContain("not enabled");
  const unchanged = (await mc(["routing", "show"])).routes as Array<Record<string, unknown>>;
  expect(unchanged.find((row) => row.role === "worker")).toMatchObject({ binding: "claude" });

  // The same rejection through the picker: the disclosure shows the verb's
  // error text under the picker and keeps the role's route. The picker
  // lists only enabled bindings, so the disabled entry is added to the open
  // picker the way a stale page would still hold it.
  await page.$eval('[data-testid="topology-route-picker"]', (select) => {
    (select as HTMLSelectElement).add(new Option("not-enabled", "not-enabled"));
  });
  await page.selectOption('[data-testid="topology-route-picker"]', "not-enabled");
  await page.waitForSelector('[data-testid="topology-route-picker-error"]');
  expect(await page.textContent('[data-testid="topology-route-picker-error"]')).toContain("not enabled");
  expect(await page.textContent('[data-testid="topology-side-harness"]')).toBe("claude-sdk");
  expect(await page.inputValue('[data-testid="topology-route-picker"]')).toBe("claude");
  const kept = (await mc(["routing", "show"])).routes as Array<Record<string, unknown>>;
  expect(kept.find((row) => row.role === "worker")).toMatchObject({ binding: "claude" });

  await page.selectOption('[data-testid="topology-route-picker"]', "fake");
  await page.waitForFunction(
    () => document.querySelector('[data-testid="topology-route-picker-error"]') === null,
  );
  await page.waitForFunction(
    () => document.querySelector('[data-testid="topology-side-harness"]')?.textContent === "fake",
  );
  const restored = (await mc(["routing", "show"])).routes as Array<Record<string, unknown>>;
  expect(restored.find((row) => row.role === "worker")).toMatchObject({ binding: "fake", harness: "fake" });
  await page.keyboard.press("Escape");
  await page.getByTestId("topology-side").waitFor({ state: "detached" });
  expect(await page.locator('[data-testid="topology-side"]').count()).toBe(0);

  await page.getByTestId("tab-pipeline").click();
  // The selected Project has one seeded task.
  await page.waitForFunction(
    () => document.querySelector('[data-testid="topology-edge-seeded"]')?.textContent === "seeded 1",
  );

  // The runs of the packet task hold no Scribe run.
  const roles = await page.locator('[data-testid="run-row"] td:nth-child(2)').allTextContents();
  expect(roles).toContain("packager");
  expect(roles).not.toContain("scribe");
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-system").click();
  expect(await page.textContent('[data-testid="pipeline-outbox"]')).toContain("undelivered");

  // Configs checks the deployment when it opens; the button repeats the check.
  await page.waitForFunction(() =>
    document.querySelectorAll('[data-testid="doctor-finding"]').length > 0,
  );
  expect(await page.textContent('[data-testid="pipeline-doctor"]')).not.toContain("Deployment not checked");
  await page.click('[data-testid="doctor-run"]');
  await page.waitForFunction(() => document.querySelector('[data-testid="doctor-run"]')?.textContent === "Re-check deployment");
  expect(await page.locator('[data-testid="doctor-finding"]').count()).toBeGreaterThan(0);

  // Host plane, read on the page poll, so it is already there. It answers from files alone, which is why it can carry a verdict on
  // a scratch home with no supervision and no running resident.
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="pipeline-health"]')?.textContent ?? "").includes("Resident status unavailable"),
  );
  const health = (await page.textContent('[data-testid="pipeline-health"]')) ?? "";
  expect(health).toContain("resident-tick");
  expect(health).toContain("log-resident");
  // No supervision here, so the tick receipt is deferred rather than failing:
  // a deployment that has never ticked is not a broken one.
  expect(health).toContain("deferred");
  expect(await page.locator('[data-testid="health-finding"]').count()).toBeGreaterThan(0);

  // The seeded warn record reaches attention, rendered by the verb — the
  // browser prints the line and never parses it.
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="pipeline-health-attention"]')?.textContent ?? "")
      .includes("resident events canary"),
  );
  const attention = (await page.textContent('[data-testid="pipeline-health-attention"]')) ?? "";
  expect(attention).toContain("WARN");
  // Info records are not attention: this one is in the same seeded file.
  expect(attention).not.toContain("tick done");
}, 40000);

test("the Configs tab renders knobs with layers, the routing table, and projects", async () => {
  const { page } = smoke;
  await page.click('[data-testid="tab-configs"]');
  await page.waitForSelector('[data-testid="configs-pane"]', { state: "visible" });

  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="configs-knobs"]')?.textContent ?? "").includes(
      "timing.timeout_minutes",
    ),
  );
  const knobs = (await page.textContent('[data-testid="configs-knobs"]')) ?? "";
  expect(knobs).toContain("default");
  expect(knobs).not.toContain("surfaces.dashboard.auth_token");
  expect(knobs).not.toContain("container.runtime");
  expect(knobs).not.toContain("runtimes.bindings");
  expect(await page.locator('[data-testid="config-knob"] input').count()).toBeGreaterThan(0);

  // The filter is browser-side and matches keys only.
  await page.getByTestId("config-nav-advanced").click();
  await page.fill('[data-testid="configs-search"]', "timeout");
  await page.waitForFunction(
    () => document.querySelectorAll('[data-testid="config-knob"]').length > 0,
  );
  const filtered = (await page.textContent('[data-testid="configs-knobs"]')) ?? "";
  expect(filtered).toContain("timeout");
  expect(filtered).not.toContain("runtimes.");
  await page.fill('[data-testid="configs-search"]', "");

  await page.getByTestId("config-nav-agents").click();
  const routing = (await page.textContent('[data-testid="configs-routing"]')) ?? "";
  expect(routing).toContain("Collie");
  expect(routing).toContain("fake");
  expect(await page.locator('[data-testid="routing-row"]').count()).toBe(7);
  expect(routing).toContain("Scribe");

  await page.getByTestId("config-nav-projects").click();
  expect(await page.textContent('[data-testid="configs-projects"]')).toContain("ws-smoke");
}, 30000);

test("the Logs tab tails a unit, greps it, and reports a never-written stream as a state", async () => {
  const { page } = smoke;
  await page.click('[data-testid="tab-logs"]');
  await page.waitForSelector('[data-testid="logs-pane"]', { state: "visible" });

  // Default picker: resident/events — the structured channel, rendered by the
  // verb. The level, the component, the event name and the run id all reach
  // the browser as text it never parsed.
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="logs-lines"]')?.textContent ?? "").includes(
      "resident events canary",
    ),
  );
  const rendered = (await page.textContent('[data-testid="logs-lines"]')) ?? "";
  expect(rendered).toContain("WARN");
  expect(rendered).toContain("reap.stop_failed");
  expect(rendered).toContain("run_id=ab12");
  // The stamp shows in the browser zone with a numeric offset; the UTC
  // marker the file holds never reaches the page.
  expect(rendered).toMatch(/2026-08-1\dT\d\d:\d\d:01\.000[+-]\d\d:\d\d WARN/);
  expect(rendered).not.toContain("20:00:01.000Z");
  expect(await page.locator('[data-testid="log-line"]').count()).toBe(2);

  expect(await page.inputValue('[data-testid="logs-limit"]')).toBe("500");
  expect(await page.textContent('[data-testid="logs-copy"]')).toBe("Copy visible");
  expect(await page.locator('[data-testid="logs-older"]').count()).toBe(0);
  const limitResponse = page.waitForResponse((response) => response.url().includes("/api/logs/resident?") && response.url().includes("tail=1000"));
  await page.selectOption('[data-testid="logs-limit"]', "1000");
  await limitResponse;
  await page.check('[data-testid="logs-wrap"]');
  expect(await page.locator('[data-testid="logs-lines"]').evaluate((node) => node.classList.contains("wrap"))).toBe(true);
  await page.uncheck('[data-testid="logs-wrap"]');
  await page.screenshot({ path: evidencePath("logs-layout-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 640, height: 900 });
  await page.screenshot({ path: evidencePath("logs-layout-mobile.png"), fullPage: true });
  await page.setViewportSize({ width: 1280, height: 900 });

  expect(await page.locator('[data-testid="logs-recent-smokerun1"]').count()).toBe(1);
  await page.click('[data-testid="logs-process-dashboard"]');
  await page.waitForSelector('[data-testid="logs-recent-dashboard-run"]');
  expect(await page.locator('[data-testid="logs-recent-smokerun1"]').count()).toBe(0);
  expect(await page.textContent('#logs-run-count')).toContain("dashboard logs");
  await page.click('[data-testid="logs-process-resident"]');
  await page.waitForSelector('[data-testid="logs-recent-smokerun1"]');
  expect(await page.locator('[data-testid="logs-recent-dashboard-run"]').count()).toBe(0);

  // The crash channel is still one pick away, and still free prose.
  await page.evaluate(() => { location.hash = "#logs/resident/stderr"; });
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="logs-lines"]')?.textContent ?? "").includes(
      "resident stderr canary line 3",
    ),
  );
  expect(await page.locator('[data-testid="log-line"]').count()).toBe(3);

  // The grep is browser-side over the window already fetched.
  await page.fill('[data-testid="logs-grep"]', "canary");
  await page.waitForFunction(
    () => document.querySelectorAll('[data-testid="log-line"]').length === 1,
  );
  expect(await page.textContent('[data-testid="logs-note"]')).toContain("1/3 lines match");
  await page.fill('[data-testid="logs-grep"]', "");
  await page.waitForFunction(
    () => document.querySelectorAll('[data-testid="log-line"]').length === 3,
  );

  // A stream no unit ever wrote is a state, not an error.
  await page.evaluate(() => { location.hash = "#logs/resident/stdout"; });
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="logs-lines"]')?.textContent ?? "").includes(
      "no resident stdout log yet",
    ),
  );
  expect(await page.isVisible('[data-testid="error"]')).toBe(false);
  // The picker keeps the address bar honest, so the tail is linkable.
  expect(page.url()).toContain("#logs/resident/stdout");
}, 30000);

test("a new tab opens the plain address signed in, with no token in the URL", async () => {
  const { context, server } = smoke;
  // The setup link ran once in this browser (gotoReady). A second tab is how
  // the operator returns: the bare address, no fragment.
  const tab = await context.newPage();
  try {
    await tab.goto(server.url);
    await tab.waitForSelector("body[data-ready]", { timeout: 15000 });
    await tab.waitForSelector('[data-testid="board-pane"]', { state: "visible" });
    await tab.waitForFunction(() =>
      (document.querySelector('[data-testid="board-pane"]')?.textContent ?? "").includes("smoke task"),
    );
    expect(await tab.isVisible('[data-testid="error"]')).toBe(false);
    expect(tab.url()).not.toContain("token");
  } finally {
    await tab.close();
  }
}, 30000);

test("a #logs deep link lands on the named tail after a reload", async () => {
  const { page, server } = smoke;
  await page.goto(`${server.url}#logs/resident/stderr`);
  await page.waitForSelector("body[data-ready]", { timeout: 15000 });
  await page.waitForSelector('[data-testid="logs-pane"]', { state: "visible" });
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="logs-lines"]')?.textContent ?? "").includes(
      "resident stderr canary line 3",
    ),
  );
  expect(page.url()).toContain("#logs/resident/stderr");
  // Only the visible tab polls, so the Board pane is not even rendered here.
  expect(await page.isVisible('[data-testid="board-pane"]')).toBe(false);
}, 30000);

test("the level facet narrows a structured tail without a fetch", async () => {
  const { page } = smoke;
  await page.evaluate(() => { location.hash = "#logs/resident/events"; });
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="logs-lines"]')?.textContent ?? "").includes(
      "resident events canary",
    ),
  );
  // The chips carry counts from the verb's ALIGNED records: one info, one warn.
  expect(await page.textContent('[data-testid="logs-level-warn"]')).toBe("Warn 1");
  expect(await page.textContent('[data-testid="logs-level-info"]')).toBe("Info 1");
  await page.click('[data-testid="logs-level-warn"]');
  await page.waitForFunction(
    () => document.querySelectorAll('[data-testid="log-line"]').length === 1,
  );
  expect(await page.textContent('[data-testid="logs-lines"]')).toContain("tick done");
  expect(await page.textContent('[data-testid="logs-lines"]')).not.toContain("resident events canary");
  await page.click('[data-testid="logs-level-warn"]');
  await page.waitForFunction(
    () => document.querySelectorAll('[data-testid="log-line"]').length === 2,
  );
}, 30000);

test("the run-diagnostics lane tails one run's captured log", async () => {
  const { page } = smoke;
  await page.click('[data-testid="logs-recent-smokerun1"]');
  // The lane lands on the newest run from the verb's own list, rendered
  // server-side: level, component, event and message reach the browser as
  // text it never parsed.
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="logs-lines"]')?.textContent ?? "").includes(
      "run diagnostics canary",
    ),
  );
  const rendered = (await page.textContent('[data-testid="logs-lines"]')) ?? "";
  expect(rendered).toContain("ERROR");
  expect(rendered).toContain("adapter.launch_failed");
  expect(await page.inputValue('[data-testid="logs-run"]')).toBe("smokerun1");
  // The address bar stays honest, so the tail is linkable from Pipeline.
  expect(page.url()).toContain("#logs/run/smokerun1");
  // The stream picker yields to the run picker: the lane has one stream.
  expect(await page.locator('[data-testid="logs-stream"]').count()).toBe(0);
}, 30000);

test("an HTML packet renders inside a sandboxed, network-dead frame", async () => {
  const { page, server } = smoke;
  const leakAttempts: string[] = [];
  page.on("requestfailed", (req) => {
    if (req.url().includes("leak.png")) leakAttempts.push(req.failure()?.errorText ?? "unknown");
  });
  page.on("requestfinished", (req) => {
    if (req.url().includes("leak.png")) leakAttempts.push("FINISHED");
  });
  await page.goto(`${server.url}#review`);
  await page.waitForSelector("body[data-ready]", { timeout: 15000 });
  await page.waitForSelector('[data-testid="review-pane"]', { state: "visible" });
  await page.locator('[data-testid="packet-row"]').first().click();
  const frame = page.frameLocator('[data-testid="packet-frame"]');
  await frame.getByText("Packet smoke canary").waitFor({ timeout: 15000 });
  // The reader read the recorded filename: html renders as a document, not as
  // its own source in a <pre>.
  expect(await page.locator('[data-testid="packet-frame"]').count()).toBe(1);
  const sandbox = await page.getAttribute('[data-testid="packet-frame"]', "sandbox");
  expect(sandbox).not.toContain("allow-scripts");
  expect(sandbox).not.toContain("allow-same-origin");
  // The packet's external <img> must never leave the frame: the injected CSP
  // (default-src 'none') kills the fetch client-side. The failure reason
  // matters — a connection error would mean the request actually went out and
  // only its target happened to be dead.
  expect(leakAttempts.length).toBeGreaterThan(0);
  // Chromium reports a CSP kill as the literal failure text "csp".
  expect(leakAttempts.every((reason) => reason.toLowerCase().includes("csp"))).toBe(true);
  // A live packet is a decision owed: the gate's own buttons sit under the
  // render the operator just read.
  expect(await page.isVisible('[data-testid="packet-decide"]')).toBe(true);
}, 30000);

test("a versioned packet requires its selected report before approval", async () => {
  const { page, server } = smoke;
  const packets = "**/api/packets";
  const render = "**/api/packets/*/render?*";
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  let requested!: () => void;
  const seen = new Promise<void>((resolve) => { requested = resolve; });
  let refuse = false;
  await page.route(packets, async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.packets = body.packets.map((packet: any) => packet.task_id !== packetTask ? packet : {
      ...packet, ready: 1,
      input_commit: "a".repeat(40), submitted_commit: "b".repeat(40),
      destination_branch: "main", destination_commit: "c".repeat(40), candidate_commit: "d".repeat(40),
      criteria: "Both settings and all accepted files remain correct.",
      verifier_evidence: "/mc/artifacts/ws-smoke/verification.html",
    });
    await route.fulfill({ response, json: body });
  });
  await page.route(render, async (route) => {
    const url = new URL(route.request().url());
    expect(url.searchParams.get("packet")).toBe(String(packetRecord));
    expect(url.searchParams.get("version")).toBe(String(packetVersion));
    requested();
    await gate;
    if (refuse) await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { message: "The selected report changed." } }) });
    else await route.continue();
  });
  const decisionsDisabled = async () => {
    for (const decision of ["approve", "revise", "cancel"]) {
      expect(await page.getByTestId(`packet-${decision}`).isDisabled()).toBe(true);
    }
  };
  try {
    await page.goto(`${server.url}?version-review#review`, { waitUntil: "domcontentloaded" });
    await page.locator('[data-testid="packet-row"]').first().click();
    await seen;
    await decisionsDisabled();
    release();
    await page.getByTestId("packet-decide").waitFor({ state: "visible" });
    await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
    await page.getByTestId("reader-view-changes").click();
    const facts = page.getByTestId("changes-facts");
    expect(await facts.locator('[data-key="candidate"] button').getAttribute("title")).toBe("d".repeat(40));
    expect(await facts.locator('[data-key="candidate"] button').innerText()).toBe("d".repeat(7));
    // The verified Worker commit is the submitted commit of the packet.
    expect(await facts.locator('[data-key="worker"] button').getAttribute("title")).toBe("b".repeat(40));
    await page.getByTestId("packet-task-open").click();
    await page.getByTestId("task-dialog-facts").waitFor();
    expect(await page.getByTestId("packet-evidence").count()).toBe(0);
    await page.screenshot({ path: evidencePath("task-delivery-review.png"), fullPage: true });
    await page.getByTestId("task-dialog-close").click();
    refuse = true;
    await page.goBack();
    await page.getByTestId("packet-reader").waitFor({ state: "hidden" });
    await page.locator('[data-testid="packet-row"]').first().click();
    await page.getByTestId("packet-reader").getByText("The selected report changed.", { exact: true }).first().waitFor();
    await decisionsDisabled();
  } finally {
    release();
    await page.unroute(packets);
    await page.unroute(render);
  }
}, 30000);

test("agent profiles walk back through each step, preserve form values after a failed model test, and keep the connection", async () => {
  const { page } = smoke;
  page.setDefaultTimeout(5000);
  const catalog = {
    revision: "profile-flow", profiles: [],
    connections: [],
    integrations: [{ id: "claude", name: "Claude Code", kind: "claude-oauth", authorization_available: true }],
  };
  const profileRoute = "**/api/agent-profiles";
  const saveRoute = "**/api/agent-profiles/connection-save";
  const modelRoute = "**/api/agent-profiles/models";
  await profileHealth();
  await page.route(profileRoute, route => route.fulfill({ json: catalog }));
  await page.route(saveRoute, async route => {
    const body = route.request().postDataJSON();
    expect(body.template_id).toBe("claude");
    for (const field of ["kind", "token", "base_url"]) expect(body).not.toHaveProperty(field);
    catalog.connections = [{ id: "smoke-account", name: body.name, kind: "claude-oauth", access_status: "connected", models: [] }];
    await route.fulfill({ json: { ...catalog, connection_id: "smoke-account" } });
  });
  await page.route(modelRoute, route => route.fulfill({ status: 502, json: { error: { message: "The model list is unavailable." } } }));
  await page.getByTestId("tab-pipeline").click();
  await page.getByTestId("pipeline-profiles").click();
  await page.getByTestId("profile-create").waitFor({ state: "visible" });
  expect(await page.getByTestId("agent-profiles-panel").textContent()).toContain("apply to all Projects");
  expect(await panelNavigation()).toEqual(["Close"]);
  await page.getByTestId("profile-create").click();
  expect(await panelNavigation()).toEqual(["Close", "Back"]);
  await page.getByTestId("agent-profiles-panel").screenshot({ path: evidencePath("agent-profile-connection-choice.png") });
  await page.getByTestId("profile-use-subscription").click();
  expect(await page.getByTestId("profile-connection_name").inputValue()).toMatch(/^Subscription account/);
  expect(await page.getByTestId("profile-use-endpoint").count()).toBe(0);
  expect(await page.getByTestId("profile-token").count()).toBe(0);
  expect(await page.getByTestId("profile-base_url").count()).toBe(0);
  await page.getByTestId("profile-connection_name").fill("Smoke account");
  await page.getByTestId("profile-sign-in-claude").click();
  await page.getByTestId("profile-model").waitFor({ state: "visible" });
  await page.getByTestId("profile-model").fill("manual/model:v1");
  await page.getByTestId("profile-name").fill("Smoke draft");
  await page.getByTestId("profile-configure-next").click();
  expect(await page.getByTestId("profile-review").textContent()).toContain("Smoke account");
  expect(await page.getByTestId("profile-save").isDisabled()).toBe(true);
  expect(await page.getByTestId("profile-test").evaluate((node) => node.classList.contains("profile-primary"))).toBe(true);
  let tests = 0;
  let finished = false;
  const operationRoute = "**/api/agent-profiles/operation-*";
  await page.route(operationRoute, async (route) => {
    const body = route.request().postDataJSON();
    if (route.request().url().endsWith("operation-start")) tests++;
    await route.fulfill({ json: finished ? {
      operation_id: body.operation_id, kind: "model_test", status: "failed",
      message: "The provider is unavailable. Retry the model test.",
    } : { operation_id: body.operation_id, kind: "model_test", status: "running", started_at: new Date(Date.now() - 5000).toISOString() } });
  });
  try {
    await page.getByTestId("profile-test").click();
    const review = page.getByTestId("profile-review");
    await review.getByText("The model test is in progress").waitFor();
    expect(await review.getByTestId("profile-operation-time").textContent()).toMatch(/^\d+ s$/);
    expect(await review.getByRole("button", { name: "Cancel test" }).isVisible()).toBe(true);
    expect(await review.getByTestId("profile-operation-status").count()).toBe(0);
    expect(await page.getByTestId("profile-test").isDisabled()).toBe(true);
    expect(await page.getByTestId("profile-save").isDisabled()).toBe(true);
    await page.getByTestId("agent-profiles-panel").screenshot({ path: evidencePath("agent-profile-test-running.png") });
    finished = true;
    await page.waitForFunction(() => document.querySelector('[data-testid="profile-review"] [data-testid="profile-operation"]')?.textContent?.includes("The model test failed"));
    await page.getByTestId("agent-profiles-panel").screenshot({ path: evidencePath("agent-profile-test-failed.png") });
    expect(tests).toBe(1);
    await page.getByTestId("profile-back").click();
    expect(await page.getByTestId("profile-model").inputValue()).toBe("manual/model:v1");
    expect(await page.getByTestId("profile-name").inputValue()).toBe("Smoke draft");
    await page.getByTestId("profile-configure-next").click();
    expect(await page.getByTestId("profile-save").isDisabled()).toBe(true);
    expect(tests).toBe(1);
  } finally { await page.unroute(operationRoute); }
  await page.getByTestId("profile-back").click();
  await page.getByTestId("profile-model").waitFor();
  expect(await page.locator('.profile-steps [aria-current="step"]').textContent()).toContain("Configure");
  await page.getByTestId("profile-back").click();
  expect(await page.getByTestId("profile-connection_name").inputValue()).toBe("Smoke account");
  await page.getByTestId("profile-back").click();
  await page.getByTestId("profile-use-subscription").waitFor();
  await page.getByTestId("profile-back").click();
  await page.getByTestId("profile-create").waitFor();
  expect(await page.getByTestId("profile-card").filter({ hasText: "Smoke draft" }).count()).toBe(0);
  await page.getByTestId("profile-close").click();
  expect(await page.getByTestId("pipeline-profiles").evaluate((node) => node === document.activeElement)).toBe(true);
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-agents").click();
  await page.getByTestId("configs-profiles").click();
  await page.getByTestId("profile-create").click();
  await page.getByTestId("profile-use-existing").click();
  const saved = await page.getByTestId("profile-connection_id").locator("option", { hasText: "Smoke account" }).getAttribute("value");
  await page.getByTestId("profile-connection_id").selectOption(saved!);
  expect(await page.getByTestId("profile-review").textContent()).toContain("Smoke account");
  expect(await page.getByTestId("profile-save").isDisabled()).toBe(true);
  expect(await page.getByTestId("agent-profiles-panel").textContent()).not.toContain("null");
  expect(await page.locator("#agent-profiles-title").evaluate((node) => node.getBoundingClientRect().top)).toBeGreaterThanOrEqual(0);
  await page.screenshot({ path: evidencePath("agent-profiles-desktop.png"), fullPage: false });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: evidencePath("agent-profiles-narrow.png"), fullPage: false });
  expect(await page.getByTestId("agent-profiles-panel").evaluate((node) => node.scrollWidth <= window.innerWidth)).toBe(true);
  await page.keyboard.press("Escape");
  expect(await page.getByTestId("configs-profiles").evaluate((node) => node === document.activeElement)).toBe(true);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByTestId("config-route-worker").click();
  const confirmedProfile = await page.getByTestId("topology-route-picker").inputValue();
  await page.getByTestId("topology-route-picker").selectOption("__create__");
  await page.getByTestId("profile-use-existing").waitFor();
  await page.keyboard.press("Escape");
  expect(await page.getByTestId("topology-route-picker").inputValue()).toBe(confirmedProfile);
  expect(await page.getByTestId("topology-route-picker").evaluate((node) => node === document.activeElement)).toBe(true);
  await page.keyboard.press("Escape");
  await page.getByTestId("topology-side").waitFor({ state: "detached" });
  await page.unroute(profileRoute);
  await page.unroute(saveRoute);
  await page.unroute(modelRoute);
  await page.unroute("**/api/system/status");
}, 30000);

test("profile creation from a role returns to the role selector after the save", async () => {
  const { page } = smoke;
  const catalog = {
    revision: "test-revision", integrations: [],
    connections: [{ id: "team", name: "Team", kind: "claude-oauth", access_status: "connected", models: [] }],
    profiles: [{ id: "old", name: "Old profile", connection_id: "team", model: "old-model", enabled: true, access_status: "connected", roles: ["worker"] }],
  };
  let saves = 0;
  let assignments = 0;
  let operationID = "";
  let modelTests = 0;
  let testedProfile: Record<string, unknown> | undefined;
  const profileRoute = /\/api\/agent-profiles(?:\/[^/?]+)?$/;
  const assignmentRoute = "**/api/routing";
  await profileHealth();
  await page.route(profileRoute, async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/models")) {
      await route.fulfill({ status: 502, json: { error: { message: "The model list is unavailable." } } });
    } else if (path.endsWith("operation-start")) {
      operationID = route.request().postDataJSON().operation_id;
      testedProfile = route.request().postDataJSON().profile;
      modelTests++;
      await route.abort("failed");
    } else if (path.endsWith("operation-status")) {
      await route.fulfill({ json: { operation_id: operationID, kind: "model_test", status: "succeeded", profile: testedProfile } });
    } else if (path.endsWith("/save")) {
      saves++;
      const body = route.request().postDataJSON();
      expect(body.test_id).toBe(operationID);
      catalog.profiles.push({ id: "new", name: body.name, connection_id: "team", model: body.model, enabled: true, access_status: "connected", roles: [] });
      await route.fulfill({ json: { ...catalog, profile_id: "new" } });
    } else await route.fulfill({ json: catalog });
  });
  await page.route(assignmentRoute, async (route) => {
    if (route.request().method() === "POST") {
      assignments++;
      expect(route.request().postDataJSON()).toEqual({ role: "worker", binding: "new" });
      if (assignments === 1) {
        await route.fulfill({ status: 409, json: { error: "The assignment changed. Retry the assignment." } });
        return;
      }
    }
    await route.fulfill({ json: { routes: [{ role: "worker", binding: assignments > 1 ? "new" : "old", harness: "fake", model: "test" }], bindings: [] } });
  });
  try {
    await page.getByTestId("tab-configs").click();
    await page.getByTestId("config-nav-agents").click();
    await page.getByTestId("config-route-worker").click();
    await page.getByTestId("topology-side").getByTestId("topology-change-profile").click();
    await page.getByTestId("profile-assignment").waitFor();
    expect(await panelNavigation()).toEqual(["Close", "All agent profiles"]);
    await page.getByTestId("profile-assignment-create").click();
    await page.getByTestId("profile-use-existing").click();
    expect(await panelNavigation()).toEqual(["Close", "Back"]);
    await page.getByTestId("profile-connection_id").selectOption("team");
    await page.getByTestId("profile-model").fill("new-model");
    await page.getByTestId("profile-name").fill("New profile");
    expect(await page.getByTestId("profile-save").textContent()).toBe("Save");
    expect(await page.getByTestId("profile-save").isDisabled()).toBe(true);
    await page.getByTestId("profile-test").click();
    await page.waitForFunction(() => !(document.querySelector('[data-testid="profile-save"]') as HTMLButtonElement)?.disabled);
    await page.reload();
    await page.waitForFunction(() => document.body.dataset.ready === "1");
    await page.getByTestId("configs-profiles").click();
    await page.getByTestId("profile-save").waitFor();
    expect(await page.getByTestId("profile-model").inputValue()).toBe("new-model");
    await page.waitForFunction(() => {
      const save = document.querySelector<HTMLButtonElement>('[data-testid="profile-save"]');
      return save !== null && !save.disabled;
    });
    expect(await page.getByTestId("profile-save").evaluate((node) => node.classList.contains("profile-primary"))).toBe(true);
    expect(await page.getByTestId("profile-review").textContent()).toContain("The model test succeeded");
    await page.getByTestId("agent-profiles-panel").screenshot({ path: evidencePath("agent-profile-test-succeeded.png") });
    expect(modelTests).toBe(1);
    await page.getByTestId("profile-save").click();
    await page.getByTestId("profile-assignment").waitFor();
    expect(saves).toBe(1);
    expect(assignments).toBe(0);
    expect(await page.getByTestId("profile-notice").textContent()).toBe("The agent profile New profile is saved. Select it to assign it to the Worker role.");
    const assigned = () => page.getByTestId("profile-assignment").locator("input:checked").getAttribute("value");
    expect(await assigned()).toBe("old");
    await page.getByTestId("profile-assignment-new").click();
    await page.getByTestId("profile-error").waitFor();
    expect(await assigned()).toBe("old");
    await page.getByTestId("profile-assignment-new").click();
    await page.getByTestId("profile-notice").getByText("The Worker role now uses this agent profile across all Projects.", { exact: true }).waitFor();
    expect(await assigned()).toBe("new");
    expect(saves).toBe(1);
    expect(assignments).toBe(2);
    await page.getByTestId("profile-close").click();
  } finally {
    if (await page.getByTestId("agent-profiles-panel").isVisible()) {
      await page.getByTestId("profile-close").click();
    }
    await page.unroute(profileRoute);
    await page.unroute(assignmentRoute);
    await page.unroute("**/api/system/status");
  }
}, 30000);

for (const kind of ["codex-oauth", "claude-oauth"]) test(`${kind} authorization resumes the form and permits reuse and reconnect`, async () => {
  const { page } = smoke;
  const integration = { id: "subscription", name: "Team subscription", kind, models: ["model-a"], authorization_available: true };
  const catalog: any = { revision: "subscription-revision", profiles: [], connections: [], integrations: [integration] };
  let authorized = false;
  let authorizations = 0;
  await profileHealth();
  const pattern = /\/api\/agent-profiles(?:\/[^/?]+)?$/;
  await page.route(pattern, async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/models")) {
      expect(route.request().postDataJSON()).toEqual({ connection_id: "team-account" });
      await route.fulfill({ json: { connection_id: "team-account", models: [{ id: "model-a", name: "Model A" }, { id: "model-b", name: "Model B" }] } });
    } else if (path.endsWith("connection-save")) {
      const body = route.request().postDataJSON();
      catalog.connections = [{ ...integration, id: "team-account", name: body.name, access_status: "disconnected" }];
      await route.fulfill({ json: { ...catalog, connection_id: "team-account" } });
    } else if (path.endsWith("operation-start") || path.endsWith("operation-status")) {
      if (path.endsWith("operation-start")) authorizations++;
      if (authorized) catalog.connections[0].access_status = "connected";
      await route.fulfill({ json: {
        operation_id: route.request().postDataJSON().operation_id, kind: "authorization",
        connection_id: "team-account", status: authorized ? "succeeded" : "action_required",
        authorization_url: "https://authorization.example/device", user_code: "TEST-CODE",
      } });
    } else await route.fulfill({ json: catalog });
  });
  try {
    await page.getByTestId("configs-profiles").click();
    await page.getByTestId("profile-create").click();
    await page.getByTestId("profile-use-subscription").click();
    await page.getByTestId("profile-connection_name").fill("Team account");
    await page.getByTestId("profile-sign-in-subscription").click();
    await page.getByRole("link", { name: "Open authorization page" }).waitFor();
    expect(await page.getByTestId("profile-operation").textContent()).toContain("TEST-CODE");
    expect(await page.getByTestId("profile-operation").textContent()).toContain("The authorization requires your action");
    expect(await page.getByTestId("profile-operation-status").count()).toBe(0);
    authorized = true;
    await page.getByTestId("profile-model").waitFor();
    expect(await page.getByTestId("profile-name").inputValue()).toBe("Team account");
    expect(await page.getByTestId("profile-notice").textContent()).toBe("The connection is saved. Other agent profiles can also use this connection.");
    await page.getByTestId("profile-back").click();
    await page.getByTestId("profile-sign-in-subscription").waitFor();
    expect(await page.getByTestId("profile-connection_name").inputValue()).toBe("Team account");
    await page.getByTestId("profile-back").click();
    await page.getByTestId("profile-back").click();
    await page.getByTestId("profile-create").click();
    await page.getByTestId("profile-use-existing").click();
    await page.getByTestId("profile-connection_id").selectOption("team-account");
    await page.getByTestId("profile-model").selectOption("model-b");
    expect(await page.getByTestId("profile-model").inputValue()).toBe("model-b");
    expect(await page.getByTestId("profile-reconnect").count()).toBe(0);
    expect(authorizations).toBe(1);
    await page.getByTestId("profile-close").click();
    catalog.connections[0].access_status = "disconnected";
    catalog.profiles = [{ id: "saved-profile", name: "Saved profile", connection_id: "team-account", model: "model-b", enabled: true, access_status: "disconnected", roles: [] }];
    authorized = false;
    await page.getByTestId("configs-profiles").click();
    await page.getByRole("button", { name: "Edit Saved profile", exact: true }).click();
    await page.getByTestId("profile-reconnect").click();
    await page.getByRole("link", { name: "Open authorization page" }).waitFor();
    authorized = true;
    await page.getByTestId("profile-model").locator('option[value="model-b"]').waitFor({ state: "attached" });
    expect(authorizations).toBe(2);
    expect(await page.getByTestId("profile-reconnect").count()).toBe(0);
    expect(await page.getByTestId("profile-save").isDisabled()).toBe(true);
    await page.getByTestId("profile-close").click();
  } finally { await page.unroute(pattern); await page.unroute("**/api/system/status"); }
}, 30000);

test("a dense log window keeps the controls compact and scrolls inside the log area", async () => {
  const { page } = smoke;
  const records = Array.from({ length: 500 }, (_, index) => ({
    ts: "2026-09-15T20:00:00.000Z", level: index % 3 ? "info" : "warn", comp: "resident",
    event: "spawn.started", msg: "The resident started the container.", run_id: `run-${String(index % 43).padStart(16, "0")}`,
  }));
  const rendered = records.map((record) => `${record.ts} ${record.level.toUpperCase()} resident ${record.event} run_id=${record.run_id} — ${record.msg}`);
  const pattern = "**/api/logs/resident?*";
  await page.route(pattern, (route) => route.fulfill({
    status: 200, contentType: "application/json",
    body: JSON.stringify({ unit: "resident", stream: "events", exists: true, truncated: true,
      records, rendered, lines: records.map((record) => JSON.stringify(record)) }),
  }));
  try {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`${smoke.server.url}#logs/resident/events`);
    await page.waitForFunction(() => document.querySelectorAll('[data-testid="log-line"]').length === 500);
    const toolbar = await page.locator('.logs-toolbar').boundingBox();
    const log = await page.getByTestId('logs-lines').boundingBox();
    const process = await page.getByTestId('logs-process-resident').boundingBox();
    expect(process!.height).toBeLessThanOrEqual(28);
    expect(log!.y - toolbar!.y).toBeLessThan(90);
    expect(log!.x + log!.width).toBeLessThanOrEqual(1280);
    expect(await page.getByTestId('logs-lines').evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
    expect(await page.locator('#logs-recent').evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
    await page.screenshot({ path: evidencePath("compact-logs-dense.png"), fullPage: true });
  } finally {
    await page.unroute(pattern);
  }
}, 30000);


test("the top of the log loads older records and preserves the visible record", async () => {
  const { page } = smoke;
  const records = Array.from({ length: 1200 }, (_, index) => ({
    ts: "2026-09-15T20:00:00.000Z", level: "info", comp: "resident",
    event: "tick.completed", msg: `Record ${index}`,
  }));
  writeFileSync(join(smoke.home, "logs", "resident.ndjson"), records.map((record) => JSON.stringify(record)).join("\n") + "\n");
  await page.goto(`${smoke.server.url}?scroll-history#logs/resident/events`);
  const pane = page.getByTestId("logs-lines");
  await page.waitForFunction(() => document.querySelectorAll('[data-testid="log-line"]').length === 500);
  expect(await page.locator(".logs-source-head").count()).toBe(0);
  expect(await page.locator('[data-testid="logs-older"]').count()).toBe(0);
  expect(await pane.evaluate((node) => node.scrollTop > 0)).toBe(true);
  await pane.evaluate((node) => { node.scrollTop = 0; });
  await page.waitForFunction(() => document.querySelectorAll('[data-testid="log-line"]').length === 1000);
  const anchorOffset = await pane.evaluate((node) => {
    const anchor = [...node.querySelectorAll(".logline")].find((row) => row.querySelector(".t")?.textContent?.endsWith("Record 700"));
    return anchor!.getBoundingClientRect().top - node.getBoundingClientRect().top;
  });
  expect(Math.abs(anchorOffset - 12)).toBeLessThanOrEqual(2);
  expect(await page.getByTestId("logs-limit").inputValue()).toBe("1000");
  await pane.evaluate((node) => { node.scrollTop = 0; });
  await page.waitForFunction(() => document.querySelectorAll('[data-testid="log-line"]').length === 1200);
  expect(await pane.locator(".t").first().textContent()).toContain("Record 0");
  await pane.evaluate((node) => { node.scrollTop = 0; });
  await page.waitForFunction(() => !document.querySelector('[data-testid="logs-lines"]')?.hasAttribute("aria-busy"));
  expect(await page.getByTestId("logs-limit").inputValue()).toBe("1500");
  await page.screenshot({ path: evidencePath("logs-scroll-history.png"), fullPage: true });
}, 30000);


test("an older-record request can retry and cannot replace another process", async () => {
  const { page } = smoke;
  const pattern = "**/api/logs/resident?*";
  let attempts = 0;
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route(pattern, async (route) => {
    if (new URL(route.request().url()).searchParams.get("tail") !== "1000") return route.continue();
    attempts += 1;
    if (attempts === 1) return route.fulfill({ status: 500, contentType: "application/json",
      body: JSON.stringify({ error: { code: "usage", message: "The log read failed." } }) });
    await held;
    await route.continue();
  });
  try {
    await page.goto(`${smoke.server.url}?scroll-retry#logs/resident/events`);
    const pane = page.getByTestId("logs-lines");
    await page.waitForFunction(() => document.querySelectorAll('[data-testid="log-line"]').length === 500);
    await pane.evaluate((node) => { node.scrollTop = 0; });
    await page.getByTestId("error").waitFor({ state: "visible" });
    expect(await page.getByTestId("logs-limit").inputValue()).toBe("500");
    expect(await pane.locator(".logline").count()).toBe(500);
    const retry = page.waitForRequest((request) => request.url().includes("/api/logs/resident?") && request.url().includes("tail=1000"));
    await pane.evaluate((node) => node.dispatchEvent(new WheelEvent("wheel", { deltaY: -100 })));
    await retry;
    await pane.evaluate((node) => {
      for (let index = 0; index < 5; index++) node.dispatchEvent(new Event("scroll"));
    });
    expect(attempts).toBe(2);
    await page.getByTestId("logs-process-dashboard").click();
    await page.waitForFunction(() => document.querySelector('[data-testid="logs-lines"]')?.textContent?.includes("dashboard canary"));
    const oldResponse = page.waitForResponse((response) => response.url().includes("/api/logs/resident?") && response.url().includes("tail=1000"));
    release();
    await oldResponse;
    await page.waitForFunction(() => !document.querySelector('[data-testid="logs-lines"]')?.hasAttribute("aria-busy"));
    expect(await pane.textContent()).toContain("dashboard canary");
    expect(await pane.textContent()).not.toContain("Record 700");
    expect(await page.getByTestId("logs-limit").inputValue()).toBe("500");
  } finally {
    release();
    await page.unroute(pattern);
  }
}, 30000);

test("the header keeps one height for any number of runs", async () => {
  const { page } = smoke;
  const stamp = (msAgo: number) => new Date(Date.now() - msAgo).toISOString().replace("T", " ").slice(0, 19);
  const run = (project: string, owner: string, subject: number | null, heartbeatAgo = 5_000) => ({
    project, status: "held", run_id: `run-${project}`, owner, subject,
    acquired_at: stamp(240_000), last_heartbeat_at: stamp(heartbeatAgo), binding: "fake",
  });
  const cases = [
    { leases: [], board: /^No agents are running$/, collie: /^No agents are running$/ },
    { leases: [run("ws-smoke", "verifier", 1)], board: /^Verifier running on task #1 · \dm$/, collie: /^1 agent running$/ },
    { leases: [run("ws-smoke", "verifier", 1), run("ws-other", "worker", 2)], board: /^Verifier running on task #1 · \dm$/, collie: /^2 agents running$/ },
    { leases: [run("ws-smoke", "verifier", 1, 600_000), run("ws-other", "strategist", null)],
      board: /^Heartbeat stopped 1\dm ago · Verifier on task #1$/, collie: /^1 heartbeat stopped · 2 agents running$/ },
  ];
  let leases: unknown[] = [];
  await page.route("**/api/leases", (route) => route.fulfill({ json: { leases } }));
  const heights = new Map<string, number>();
  try {
    await gotoReady(smoke);
    await selectProject(smoke, "ws-smoke");
    for (const entry of cases) {
      leases = entry.leases;
      await page.reload();
      await page.waitForSelector("body[data-ready]", { timeout: 15000 });
      for (const tab of ["board", "collie"] as const) {
        await page.getByTestId(`tab-${tab}`).click();
        const pattern = entry[tab];
        await page.waitForFunction((source) =>
          new RegExp(source).test(document.querySelector('[data-testid="lease-badge"] .text')?.textContent ?? ""), pattern.source);
        for (const width of [1440, 1100, 800, 390]) {
          await page.setViewportSize({ width, height: 720 });
          const box = await page.locator("header.topbar").boundingBox();
          const key = `${tab}:${width}`;
          heights.set(key, heights.get(key) ?? box!.height);
          expect(`${key} ${box!.height}`).toBe(`${key} ${heights.get(key)}`);
        }
      }
    }
    expect(await page.getByTestId("lease-badge").getAttribute("title"))
      .toMatch(/^Smoke: Heartbeat stopped 1\dm ago · Verifier on task #1\nOther: Strategist running · \dm$/);
  } finally {
    await page.unroute("**/api/leases");
    await page.setViewportSize({ width: 1280, height: 720 });
  }
}, 90000);
