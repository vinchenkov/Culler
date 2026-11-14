import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { bootSmoke, gotoReady, mc, teardown, type Smoke } from "../smoke/harness";

let smoke: Smoke;
const observations: any[] = [];
const requests: any[] = [];
const commands: any[] = [];
let failTasks = false;
let marks: Record<string, number> = {};
let phase = "";
const count = 12;
const delay = 100;

beforeAll(async () => {
  smoke = await bootSmoke({ observedCommand: sample => commands.push({ phase, ...sample }) });
  await smoke.page.clock.install();
  await smoke.page.exposeFunction("latencyMark", name => { marks[name] ??= performance.now(); });
  for (const project of ["measure-alpha", "measure-beta"]) {
    await mc(["project", "add", project, "--title", project, "--kind", "repo"]);
    for (let i = 0; i < 12; i++) await mc(["task", "add", `Example task ${i}`, "--project", project]);
  }
  await smoke.page.addInitScript(() => {
    const evidence = (window as any).latencyEvidence = { start: performance.now(), moves: [] };
    new PerformanceObserver(list => {
      for (const entry of list.getEntries()) evidence.moves.push({ at: performance.now(), value: (entry as any).value });
    }).observe({ type: "layout-shift", buffered: true });
    const observe = () => {
      if (document.querySelector('#board-pane:not([hidden]) [data-testid="board-card"]') && !evidence.board) {
        evidence.board = performance.now();
        (window as any).latencyMark("board").then(() => { evidence.markedBoard = true; });
      }
      if (document.querySelector('#pipeline-pane:not([hidden]) .flowmap') && !evidence.pipeline) evidence.pipeline = performance.now();
    };
    new MutationObserver(observe).observe(document, { subtree: true, childList: true, attributes: true });
  });
  await smoke.page.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() !== "GET") { await route.continue(); return; }
    const item = { phase, path, start: performance.now(), server: 0, end: 0 };
    requests.push(item);
    await new Promise(resolve => setTimeout(resolve, delay));
    if (path === "/api/tasks" && failTasks) {
      await route.fulfill({ status: 503, json: { error: { message: "The task read is unavailable." } } });
      item.end = performance.now();
      return;
    }
    const start = performance.now();
    const response = await route.fetch();
    item.server = performance.now() - start;
    if (path === "/api/runs" || path === "/api/packets") {
      const body = await response.json();
      const history = Array.from({ length: 500 }, (_, i) => ({
        id: `example-run-${i}`, task_id: 1000 + i, subject: i % 24 + 1,
        project: i % 2 ? "measure-alpha" : "measure-beta", role: "worker", outcome: "completed",
        created_at: "2026-09-01 12:00:00", started_at: "2026-09-01 12:00:00", ended_at: "2026-09-01 12:01:00",
        archived: 1, thesis: "Example archived report.", pool_snapshot: JSON.stringify({ tasks: Array(12).fill({ status: "seeded" }) }),
      }));
      body[path === "/api/runs" ? "runs" : "packets"] = history;
      await route.fulfill({ response, json: body });
    } else if (path === "/api/projects") {
      const body = await response.json();
      body.projects = body.projects.map(row => ({ ...row, vision_status: { ready: true, state: "ready" } }));
      await route.fulfill({ response, json: body });
    } else if (path === "/api/projects/readiness") {
      await route.fulfill({ response, json: { ready: true, issues: [] } });
    } else if (path === "/api/system/status") {
      await route.fulfill({ response, json: { complete: true, conditions: [], connections: [], observed_at: new Date().toISOString() } });
    } else await route.fulfill({ response });
    item.end = performance.now();
  });
  await gotoReady(smoke);
}, 60000);

afterAll(async () => { await teardown(smoke); });

async function finish(name: string, started: number, contentAt: number) {
  // Settle response callbacks without adding a display delay to the product.
  await smoke.page.waitForTimeout(200);
  const rows = requests.filter(item => item.phase === phase);
  const moves = await smoke.page.evaluate(() => (window as any).latencyEvidence.moves);
  observations.push({ scenario: name, phase, content_ms: contentAt - started,
    layout_shift: moves.filter(item => item.at >= started && item.at <= contentAt + 200).reduce((sum, item) => sum + item.value, 0),
    reads: rows.length, overlap: rows.reduce((peak, row) => Math.max(peak, rows.filter(other => other.start <= row.start && other.end > row.start).length), 0),
    server_ms: rows.map(item => item.server), command_ms: commands.filter(item => item.phase === phase).map(item => item.duration_ms), requests: rows.map(({ phase, ...rest }) => rest),
  });
}

test("Repeated scratch observations separate browser latency from command time", async () => {
  const { page, server } = smoke;
  for (let sample = 0; sample < count; sample++) {
    failTasks = false;
    phase = `cold-${sample}`;
    marks = {};
    const coldStarted = performance.now();
    await page.goto(`${server.url}?measure=${sample}#board`);
    await page.waitForFunction(() => (window as any).latencyEvidence.markedBoard);
    const first = await page.evaluate(() => (window as any).latencyEvidence);
    await finish("cold Board", first.start, first.board);
    observations.at(-1).content_ms = marks.board - coldStarted;

    phase = `next-${sample}`;
    const next = await page.evaluate(() => {
      const start = performance.now();
      document.querySelector<HTMLButtonElement>('[data-testid="tab-pipeline"]')!.click();
      return start;
    });
    await page.waitForFunction(() => (window as any).latencyEvidence.pipeline);
    const pipeline = await page.evaluate(() => (window as any).latencyEvidence.pipeline);
    await finish("first Pipeline after Board", next, pipeline);

    phase = `return-${sample}`;
    const revisit = await page.evaluate(() => {
      const start = performance.now();
      document.querySelector<HTMLButtonElement>('[data-testid="tab-board"]')!.click();
      return { start, end: performance.now(), present: Boolean(document.querySelector('#board-pane:not([hidden]) [data-testid="board-card"]')) };
    });
    expect(revisit.present).toBe(true);
    await finish("fresh Board return", revisit.start, revisit.end);

    for (const failed of [false, true]) {
      phase = `${failed ? "failure" : "periodic"}-${sample}`;
      failTasks = failed;
      const response = page.waitForResponse(result => result.url().endsWith("/api/tasks"));
      const started = performance.now();
      await page.clock.fastForward(11000);
      const browserStart = await page.evaluate(() => performance.now());
      await (await response).finished();
      await page.waitForFunction(() => !document.querySelector('#board-pane [aria-busy="true"]'));
      const completed = performance.now() - started;
      await finish(failed ? "failed task refresh" : "periodic Board refresh", browserStart, browserStart);
      observations.at(-1).response_ms = completed;
      observations.at(-1).retains_records = await page.getByTestId("board-card").count() > 0;
    }
  }
  const percentile = (values, p) => [...values].sort((a, b) => a - b)[Math.ceil(values.length * p) - 1];
  const summary = [...new Set(observations.map(item => item.scenario))].map(scenario => {
    const rows = observations.filter(item => item.scenario === scenario);
    return { scenario, samples: rows.length,
      median_ms: percentile(rows.map(item => item.content_ms), .5), p95_ms: percentile(rows.map(item => item.content_ms), .95),
      median_reads: percentile(rows.map(item => item.reads), .5), median_overlap: percentile(rows.map(item => item.overlap), .5),
      median_layout_shift: percentile(rows.map(item => item.layout_shift), .5), p95_layout_shift: percentile(rows.map(item => item.layout_shift), .95),
      median_command_ms: percentile(rows.flatMap(item => item.command_ms), .5),
      median_server_ms: percentile(rows.flatMap(item => item.server_ms), .5),
      median_response_ms: rows[0].response_ms === undefined ? undefined : percentile(rows.map(item => item.response_ms), .5),
      p95_response_ms: rows[0].response_ms === undefined ? undefined : percentile(rows.map(item => item.response_ms), .95),
      retained_samples: rows[0].retains_records === undefined ? undefined : rows.filter(item => item.retains_records).length,
    };
  });
  const directory = process.env.LATENCY_EVIDENCE ?? "/tmp/collie-latency-performance";
  mkdirSync(directory, { recursive: true });
  writeFileSync(`${directory}/results.json`, JSON.stringify({ delay_ms: delay, projects: 2, tasks: 24, synthetic_runs: 500, synthetic_packets: 500, summary, observations, commands }, null, 2));
  console.log(JSON.stringify(summary));
}, 90000);
