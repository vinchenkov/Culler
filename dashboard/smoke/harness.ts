// harness.ts — the shared smoke bootstrap: a scratch spine and
// MC_HOME provisioned through the production onboarding authority, a real
// dashboard server with COMPLETE deps (readRender included — the ApiDeps
// interface requires it, and a smoke that omits it crashes on a render
// instead of returning an envelope), and a real Chromium page with its
// console captured for failure messages.
//
// MC_SPINE/MC_HOME are process-global; smoke files run sequentially in one
// bun process, so each file's beforeAll must call bootSmoke (re-pointing the
// env) and its afterAll must call teardown (stopping the server) before the
// next file boots.

import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { randomBytes } from "node:crypto";
import { dirname, isAbsolute, join } from "node:path";
import { tmpdir } from "node:os";
import { Database } from "bun:sqlite";
import type { Browser, BrowserContext, Page } from "playwright";

import { makeGenRef } from "../src/api";
import { openAttachmentFile } from "../src/hostfile";
import { makeMcRunner } from "../src/mc";
import { nudgeResident, residentSocketPath } from "../src/resident-nudge";
import { startDashboardServer, type DashboardServerHandle } from "../src/server";
import { startWikiServer, type WikiServerHandle } from "../src/wiki-server";
import { makeLogger, parseLevel } from "../../runner/shared/log";
import { openFileSink } from "../../runner/shared/log-sink";

export const MC_BIN = process.env["MC_SMOKE_BIN"] ?? "";
const { chromium } = await import(process.env["MC_SMOKE_PLAYWRIGHT"] ?? "playwright");
// macOS Unix sockets require a shorter path than the default user temporary directory.
const scratchRoot = process.env["MC_SMOKE_ROOT"] ?? (process.platform === "darwin" ? "/tmp" : tmpdir());
const evidenceRoot = process.env["MC_SMOKE_EVIDENCE_DIR"] ?? join(scratchRoot, "culler-smoke-evidence");

export function evidencePath(name: string): string {
  const path = join(evidenceRoot, name);
  mkdirSync(dirname(path), { recursive: true });
  return path;
}

/** Opens the scratch spine of a smoke. A direct write waits for the lock of
 * the dashboard's mc instead of a failure with SQLITE_BUSY. */
export function openSpine(smoke: { home: string }): Database {
  const db = new Database(join(smoke.home, "spine.db"));
  db.run("PRAGMA busy_timeout = 10000");
  return db;
}

/** Every bind is token-gated; the smokes present this one, browser-side via
 * the URL fragment and fetch-side via the bearer header. */
export const SMOKE_TOKEN = "smoke-token";
export const SMOKE_BEARER = { authorization: `Bearer ${SMOKE_TOKEN}` };

export const ROUTING = `# Culler routing

| role | harness | binding |
| --- | --- | --- |
| strategist | fake | fake |
| arbiter | fake | fake |
| worker | fake | fake |
| verifier | fake | fake |
| scribe | fake | fake |
| packager | fake | fake |
| homie | fake | fake |
`;

/** A loopback stand-in for AgentsView. Each API address requires the bearer
 * `token` and gives 401 without it. The health answer comes while
 * `answering` is true, and the answer of another service after that. Each
 * other address gives a plain page, so a trace link opens in a browser tab. */
export interface FakeAgentsView {
  url: string;
  port: number;
  answering: boolean;
  token: string;
  stop(): void;
}

function startFakeAgentsView(token: string): FakeAgentsView {
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(req) {
      const path = new URL(req.url).pathname;
      if (path.startsWith("/api/") && req.headers.get("authorization") !== `Bearer ${fake.token}`) {
        return Response.json({ error: "unauthorized" }, { status: 401 });
      }
      if (path === "/api/ping") {
        return fake.answering
          ? Response.json({ ok: true, service: "agentsview", pid: process.pid })
          : new Response("unavailable", { status: 503 });
      }
      if (path.startsWith("/api/v1/sessions/")) return Response.json({ id: decodeURIComponent(path.slice("/api/v1/sessions/".length)), messages: [] });
      return new Response("<!doctype html><title>AgentsView fixture</title><p>AgentsView fixture</p>",
        { headers: { "content-type": "text/html; charset=utf-8" } });
    },
  });
  const fake: FakeAgentsView = {
    url: `http://127.0.0.1:${server.port}`,
    port: server.port as number,
    answering: true,
    token,
    stop() { server.stop(true); },
  };
  return fake;
}

/** Writes a viewer secret the way the deployment creates it: 64 lowercase
 * hexadecimal characters in a file with mode 0600. */
function writeViewerSecret(folder: string, name: string): string {
  mkdirSync(folder, { recursive: true, mode: 0o700 });
  chmodSync(folder, 0o700);
  const secret = randomBytes(32).toString("hex");
  writeFileSync(join(folder, name), `${secret}\n`, { mode: 0o600 });
  chmodSync(join(folder, name), 0o600);
  return secret;
}

/** Points the viewer addresses of the scratch config.toml at the servers of
 * the smoke. No smoke reaches a viewer of the operator deployment. */
function pointViewers(home: string, ports: { agentsview: number; wiki: number }) {
  const path = join(home, "config.toml");
  const before = readFileSync(path, "utf8");
  let after = before;
  for (const [surface, port] of Object.entries(ports)) {
    const section = new RegExp(`(\\[surfaces\\.${surface}\\][^\\[]*?\\bbind\\s*=\\s*)"[^"]*"`);
    if (!section.test(after)) throw new Error(`The scratch config.toml has no [surfaces.${surface}] bind.`);
    after = after.replace(section, `$1"127.0.0.1:${port}"`);
  }
  writeFileSync(path, after);
}

export interface Smoke {
  scratch: string;
  stateScratch: string;
  home: string;
  server: DashboardServerHandle;
  wiki: WikiServerHandle;
  wikiKey: string;
  agentsview: FakeAgentsView;
  agentsviewToken: string;
  context: BrowserContext;
  page: Page;
  browserLog: string[];
}

// One Chromium per bun process, shared by every smoke file. A second
// chromium.launch() in the same process wedges the Playwright transport under
// bun — launch #2 hangs outright, or its browser stalls mid-session with
// every protocol call (clicks, waits, even browser.close()) never resolving.
// Observed 2026-08-17 across repeated `bun test smoke/` runs; single-file
// runs (one launch) never failed. Files instead get a fresh context each.
// The browser is left to bun's process-exit reaping — closing it would just
// recreate the second-launch case for the next file.
let sharedBrowser: Browser | null = null;

async function launchOnce(): Promise<Browser> {
  if (sharedBrowser === null || !sharedBrowser.isConnected()) {
    const cache = join(scratchRoot, "browser-cache");
    mkdirSync(cache, { recursive: true });
    // Linux Chromium reserves a gutter for a classic scrollbar. Overlay
    // scrollbars give each platform the page width of macOS.
    const endpoint = process.env.MC_SMOKE_BROWSER_WS_ENDPOINT;
    sharedBrowser = endpoint ? await chromium.connect(endpoint, {
      exposeNetwork: "<loopback>", timeout: 10000,
    }) : await chromium.launch({
      args: ["--enable-features=OverlayScrollbar"],
      env: { ...process.env, TMPDIR: scratchRoot, XDG_CACHE_HOME: cache },
    });
  }
  return sharedBrowser;
}

/** Shells the smoke's real mc binary and returns its parsed stdout. A verb
 * that outlives the deadline is killed and named — a silent hang here
 * otherwise consumes the whole test timeout with no diagnosis. */
export async function mc(argv: string[], extraEnv: Record<string, string> = {}, input?: unknown) {
  const proc = Bun.spawn([MC_BIN, ...argv], {
    env: { ...process.env, ...extraEnv },
    stdin: input === undefined ? "ignore" : "pipe",
    stdout: "pipe",
    stderr: "pipe",
  });
  if (input !== undefined) {
    proc.stdin.write(JSON.stringify(input));
    proc.stdin.end();
  }
  const deadline = setTimeout(() => proc.kill(9), 20000);
  const [stdout, stderr, code] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ]);
  clearTimeout(deadline);
  if (code !== 0) {
    throw new Error(`mc ${argv.join(" ")} exited ${code}: ${stderr}`);
  }
  return JSON.parse(stdout) as Record<string, unknown>;
}

/** Registers a scratch repository through the current Project setup command. */
export async function seedProject(smoke: Smoke, id: string, options: { title?: string; workspace?: string; vision?: boolean; environmentReady?: boolean } = {}) {
  const workspace = options.workspace ?? join(smoke.scratch, id);
  mkdirSync(workspace, { recursive: true });
  const vision = join(workspace, "VISION.md");
  if (options.vision !== false && !existsSync(vision)) {
    writeFileSync(vision, `The ${id} Project proves the dashboard behavior.\n`);
  }
  await mc(["onboard", "project", "--project", id, "--title", options.title ?? id,
    "--workspace-root", workspace, "--accept-generated-sandbox", "--project-vision", "later"]);
  if (options.environmentReady) await seedAcceptedEnvironment(smoke, id);
  return workspace;
}

/** Records the prerequisite environment of a dashboard fixture through mc. No container runs in a dashboard smoke. */
async function seedAcceptedEnvironment(smoke: Smoke, project: string): Promise<void> {
  const base = `sha256:${"a".repeat(64)}`;
  const files = `sha256:${"b".repeat(64)}`;
  const configuration = { base_digest: base };
  await mc(["environment", "save"], {}, { project_id: project, configuration });
  await mc(["environment", "assist"], {}, { project_id: project, instruction: "Prepare the Project dependencies." });
  const request = { default_image_digest: base, runtime_identity: "dashboard-smoke" };
  const work = await mc(["environment", "work"], {}, request);
  const operation = (work.operations as Array<{ id: string; project_id: string }>).find(entry => entry.project_id === project);
  if (!operation) throw new Error("The dashboard fixture received no environment operation.");
  const identity = join(smoke.scratch, `${project}-environment.json`);
  writeFileSync(identity, JSON.stringify({ tier: "setup", role: "setup", run_id: operation.id }));
  await mc(["environment", "report"], { MC_RUN_JSON: identity }, {
    operation_id: operation.id, outcome: "complete", files_digest: files,
    summary: "The Project preparation is complete.",
  });
  const acceptance = await mc(["environment", "work"], {}, request);
  const accept = (acceptance.operations as Array<{ id: string; project_id: string; kind: string; inputs_digest: string }>).find(entry => entry.project_id === project);
  if (accept?.kind !== "accept") throw new Error("The dashboard fixture received no source acceptance.");
  await mc(["environment", "operation-result"], {}, {
    operation_id: accept.id, inputs_digest: accept.inputs_digest, status: "succeeded",
    source_commit: "c".repeat(40), files_digest: files,
  });
}

export async function bootSmoke(options: { releaseID?: string; observedCommand?: (sample: { command: string; duration_ms: number }) => void } = {}): Promise<Smoke> {
  if (MC_BIN === "" || !isAbsolute(MC_BIN)) {
    throw new Error("MC_SMOKE_BIN must name an absolute mc binary. Use dashboard/smoke.sh.");
  }
  // Fixture repositories must stay outside the Git root of the checkout.
  const scratch = realpathSync(mkdtempSync("/tmp/culler-smoke-"));
  const stateScratch = realpathSync(mkdtempSync(join(scratchRoot, "culler-smoke-state-")));
  const home = join(stateScratch, "home");
  // A Unix socket path holds at most 103 bytes on macOS and 107 bytes on Linux.
  if (Buffer.byteLength(join(home, "discord.socket")) > 103) {
    throw new Error(`The Discord socket path under ${home} is too long. Select a shorter MC_SMOKE_ROOT.`);
  }
  mkdirSync(home);
  chmodSync(home, 0o700);

  // Bootstrap through the production onboarding authority. A hand-written
  // routing.md without config.toml is not a deployment and must fail closed.
  // Tests must not inherit the run identity or the deployment paths of an agent container.
  for (const name of Object.keys(process.env)) {
    if (name.startsWith("MC_") && !name.startsWith("MC_SMOKE_")) delete process.env[name];
  }
  process.env["MC_RUN_JSON"] = join(stateScratch, "absent-run.json");
  process.env["MC_SPINE"] = join(home, "spine.db");
  process.env["MC_HOME"] = home;
  process.env["MC_TASK_STORE_ROOT"] = join(stateScratch, "tasks");
  process.env["MC_ARTIFACTS_ROOT"] = join(stateScratch, "artifacts");
  mkdirSync(join(stateScratch, "tasks"));
  mkdirSync(join(stateScratch, "artifacts"));
  await mc(["onboard", "home"]);
  const requestedRouting = join(scratch, "routing.request.md");
  writeFileSync(requestedRouting, ROUTING, { mode: 0o600 });
  // Two enabled bindings: every role routes to the fake harness, and the
  // claude reference binding stays enabled so the Pipeline page's picker has
  // a second binding to move a role to. Routing needs no grant.
  await mc(["onboard", "routing", "--runtime-bindings", "fake,claude", "--routing-file", requestedRouting]);
  await mc(["environment", "base-record"], {}, {
    base_digest: `sha256:${"a".repeat(64)}`,
    metadata: { release_identity: "dashboard-smoke" },
  });

  // The dashboard process serves the wiki exports at a second address, below
  // the wiki key. AgentsView requires the AgentsView token.
  const wikiKey = writeViewerSecret(join(home, "state"), "wiki-key");
  const agentsviewToken = writeViewerSecret(join(home, "agentsview"), "token");
  const wiki = startWikiServer({ bind: "127.0.0.1:0", home, key: wikiKey });
  const agentsview = startFakeAgentsView(agentsviewToken);
  pointViewers(home, { agentsview: agentsview.port, wiki: wiki.port });

  const runner = makeMcRunner(MC_BIN);
  const server = startDashboardServer(
    { bind: "127.0.0.1:0", authToken: SMOKE_TOKEN, releaseID: options.releaseID },
    {
      mc: { async run(args, input) {
        const started = performance.now();
        try { return await runner.run(args, input); }
        finally { options.observedCommand?.({ command: args.slice(0, 2).join(" "), duration_ms: performance.now() - started }); }
      } },
      genRef: makeGenRef(),
      // The real sink against the scratch MC_HOME, exactly as main.ts wires it:
      // a stub here would let the file-opening half of the seam rot untested,
      // and it is the half that touches the filesystem.
      log: makeLogger({
        comp: "dashboard",
        level: parseLevel(process.env["MC_LOG_LEVEL"]),
        sink: openFileSink({ dir: join(home, "logs"), name: "dashboard.ndjson" }),
      }),
      readRender: (hostPath) => Bun.file(hostPath).text(),
      readArtifact: (hostPath) => Bun.file(hostPath).bytes(),
      openAttachment: openAttachmentFile,
      // No resident runs in the smoke; the nudge fails and is logged.
      nudgeResident: (sessionId) => nudgeResident(residentSocketPath(home), sessionId),
    },
  );

  const browser = await launchOnce();
  // MC_SMOKE_COLOR_SCHEME=dark runs each smoke in the dark theme: without a
  // stored selection, the dashboard uses the theme of the system.
  const colorScheme = process.env["MC_SMOKE_COLOR_SCHEME"] === "dark" ? "dark" : "light";
  const context = await browser.newContext({ colorScheme });
  const page = await context.newPage();
  const browserLog: string[] = [];
  page.on("console", (msg) => browserLog.push(`console.${msg.type()}: ${msg.text()}`));
  page.on("pageerror", (err) => browserLog.push(`pageerror: ${err.message}`));
  page.on("requestfailed", (req) => browserLog.push(`requestfailed: ${req.url()} ${req.failure()?.errorText}`));
  page.on("crash", () => browserLog.push("PAGE CRASHED"));
  page.on("framenavigated", (frame) => browserLog.push(`framenavigated: ${frame.url()}`));

  return { scratch, stateScratch, home, server, wiki, wikiKey, agentsview, agentsviewToken, context, page, browserLog };
}

/** Loads the shell and waits until app.js has attached its listeners — a tab
 * click delivered before that is silently lost. The token rides the URL
 * fragment, exactly as the operator's setup link presents it; the browser
 * keeps it after that. */
export async function gotoReady(smoke: Smoke) {
  await smoke.page.goto(`${smoke.server.url}#token=${SMOKE_TOKEN}`);
  await smoke.page.waitForSelector("body[data-ready]", { timeout: 15000 });
}

/** Waits for a complete page read that starts after this call. The page starts
 * a page read only after the previous page read ends, so the third read of the
 * region starts after the second page read ends. */
export async function completePageRead(page: Page, region: string) {
  await page.evaluate(region => {
    const record = { ends: 0, observer: null as MutationObserver | null };
    record.observer = new MutationObserver(changes => { record.ends += changes.filter(change => change.oldValue === "true").length; });
    record.observer.observe(document.getElementById(region)!, { attributes: true, attributeFilter: ["aria-busy"], attributeOldValue: true });
    (window as any).smokePageReads = record;
  }, region);
  await page.waitForFunction(() => {
    document.dispatchEvent(new Event("visibilitychange"));
    return (window as any).smokePageReads.ends >= 3;
  }, undefined, { polling: 100 });
  await page.evaluate(() => (window as any).smokePageReads.observer.disconnect());
}

/** Selects one Project through the title switcher. The options sit inside
 * the panel, so a direct click on an option waits for a hidden element. */
export async function selectProject(smoke: Smoke, id: string) {
  const { page } = smoke;
  await page.getByTestId("project-button").click();
  await page.getByTestId("project-listbox").waitFor({ state: "visible" });
  await page.getByTestId(`project-${id}`).click();
  await page.getByTestId("project-listbox").waitFor({ state: "hidden" });
  await page.waitForFunction(() =>
    document.querySelector('[data-testid="project-button"]')?.getAttribute("aria-expanded") === "false");
}

/** Opens the setup journal of one Project through its Environment page. */
export async function openSetupHistory(smoke: Smoke, id: string) {
  const { page } = smoke;
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-projects").click();
  await page.getByTestId(`environment-project-${id}`).click();
  await page.getByTestId("environment-setup-history").click();
}

export async function teardown(smoke: Smoke | undefined) {
  if (!smoke) return;
  // Server first: the page's poll loop then fails fast instead of holding
  // requests open under a closing browser. The context closes, never the
  // shared browser (see launchOnce).
  smoke.server?.stop();
  smoke.wiki?.stop();
  smoke.agentsview?.stop();
  await smoke.context?.close();
  if (smoke.scratch) {
    rmSync(smoke.scratch, { recursive: true, force: true });
  }
  if (smoke.stateScratch) {
    rmSync(smoke.stateScratch, { recursive: true, force: true });
  }
}
