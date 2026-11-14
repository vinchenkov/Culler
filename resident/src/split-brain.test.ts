// Split-brain convergence tests. The real mc binary commits every record,
// every lease decision, and every repository execution. A failure enters only
// through the resident, runner, or native-surface seams. A restart uses the
// ordinary tick loop, the ordinary repository lane, or the ordinary outbox
// poll, send, and ack client. The mc binary has no fault hook and no test
// clock, so the test ages a committed lease or heartbeat in SQLite. The last
// group runs a wiki update beside a Worker run through the same seams, with
// no failure.

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { Database } from "bun:sqlite";
import {
  appendFileSync,
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { REVISION, VERSION, wikiDigest, wikiFiles } from "../../runner/openwiki/integration.mjs";
import { recordCurrentWiki } from "../testdata/wiki-update";
import { startTickLoop } from "./tick-loop";
import { createRepositoryLane, type RepositoryLane } from "./repository-lane";
import { fail, FakeFollow, FakeScheduler, makeRig, ok, testConfig } from "./test-helpers";
import type { Effect, Exec, ExecResult, TickDeps } from "./types";

type JsonObject = Record<string, unknown>;

const repoRoot = resolve(import.meta.dir, "..", "..");
const mcSourceDir = join(repoRoot, "mc", "cmd", "mc");
const agentRunnerMain = join(repoRoot, "runner", "agent-runner", "main.ts");
const fakeHarnessCli = join(repoRoot, "runner", "fake-harness", "cli.ts");
const mcLandScript = join(repoRoot, "runner", "image", "mc-land");
const suiteRoot = realpathSync(mkdtempSync(join(tmpdir(), "mc-split-brain-suite-")));
const mcBin = join(suiteRoot, "mc");

const fakeRouting = `# Culler routing

| role | harness | binding |
| --- | --- | --- |
| strategist | fake | fake |
| arbiter | fake | fake |
| worker | fake | fake |
| verifier | fake | fake |
| scribe | fake | fake |
| packager | fake | fake |
| collie | fake | fake |
`;

interface ProcessOptions {
  cwd?: string;
  env?: Record<string, string>;
  stdin?: string | Uint8Array;
}

async function runProcess(argv: string[], options: ProcessOptions = {}): Promise<ExecResult> {
  const proc = Bun.spawn(argv, {
    cwd: options.cwd,
    env: options.env,
    stdin: options.stdin === undefined ? "ignore" : Buffer.from(options.stdin),
    stdout: "pipe",
    stderr: "pipe",
  });
  const [exitCode, stdout, stderr] = await Promise.all([
    proc.exited,
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
  ]);
  return { exitCode, stdout, stderr };
}

beforeAll(async () => {
  const tags = process.platform === "linux" ? "test_fake_routing,test_scratch_spine" : "test_fake_routing";
  const built = await runProcess(
    ["mise", "exec", "--", "go", "build", "-tags", tags, "-o", mcBin, "."],
    { cwd: mcSourceDir },
  );
  if (built.exitCode !== 0) {
    throw new Error(`build real mc test binary: ${built.stderr || built.stdout}`);
  }
}, 300_000);

afterAll(() => {
  rmSync(suiteRoot, { recursive: true, force: true });
});

function mcEnv(home: string, spine?: string): Record<string, string> {
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (value === undefined) continue;
    if (["MC_HOME", "MC_SPINE", "MC_HELPER", "MC_RUN_JSON", "MC_TASK_STORE_ROOT"].includes(key)) continue;
    env[key] = value;
  }
  env.MC_HOME = home;
  env.MC_RUN_JSON = join(home, "absent-run.json");
  if (spine !== undefined) env.MC_SPINE = spine;
  return env;
}

async function runMc(
  home: string,
  spine: string | undefined,
  argv: string[],
  stdin?: Uint8Array,
): Promise<ExecResult> {
  return runProcess([mcBin, ...argv], { env: mcEnv(home, spine), stdin });
}

async function mcJson(
  home: string,
  spine: string | undefined,
  argv: string[],
): Promise<JsonObject> {
  const result = await runMc(home, spine, argv);
  if (result.exitCode !== 0) {
    throw new Error(
      `mc ${argv.join(" ")} exited ${result.exitCode}: ${result.stderr || result.stdout}`,
    );
  }
  return JSON.parse(result.stdout) as JsonObject;
}

/** The one pipeline effect of a tick. The fixture has exactly one Project
 * with a current wiki and no Collie session, so a tick holds at most one
 * action. A round that committed nothing reports as the idle effect with its
 * reason. */
function tickEffect(tick: JsonObject): JsonObject {
  const actions = tick.actions;
  const idle = tick.idle;
  if (tick.action !== "tick" || !Array.isArray(actions) || !Array.isArray(idle)) {
    throw new Error(`mc dispatch did not return a tick object: ${JSON.stringify(tick)}`);
  }
  if (actions.length === 1 && idle.length === 0) return actions[0] as JsonObject;
  if (actions.length === 0 && idle.length === 1) {
    return { action: "idle", reason: (idle[0] as JsonObject).reason };
  }
  throw new Error(`tick over one Project held ${actions.length} actions and ${idle.length} idle rounds: ${JSON.stringify(tick)}`);
}

async function dispatchOnce(fixture: Fixture): Promise<JsonObject> {
  return tickEffect(await mcJson(fixture.home, fixture.spine, ["dispatch"]));
}

interface Fixture {
  root: string;
  home: string;
  spine: string;
  /** The Project repository. Its `culler` branch is the destination. */
  workspace: string;
  /** The one commit on `culler` before any task work. */
  baseSHA: string;
  taskId: number;
  /** The run of the wiki update that makes the wiki current, or null for a
   * fixture whose wiki is behind. */
  wikiRun: string | null;
  config: TickDeps["config"];
}

async function gitAt(path: string, ...argv: string[]): Promise<string> {
  const result = await runProcess(["git", "-C", path, ...argv]);
  if (result.exitCode !== 0) {
    throw new Error(`git ${argv.join(" ")} exited ${result.exitCode}: ${result.stderr}`);
  }
  return result.stdout.trim();
}

async function git(fixture: Fixture, ...argv: string[]): Promise<string> {
  return gitAt(fixture.workspace, ...argv);
}

/** The Project repository: one commit on `culler` and an untracked vision
 * file. The commit carries a complete Project wiki, so the Scribe run of a
 * wiki update can end with no wiki change. */
async function initializeGitWorkspace(workspace: string): Promise<string> {
  await gitAt(workspace, "init", "-q", "-b", "culler");
  await gitAt(workspace, "config", "user.name", "split-brain fixture");
  await gitAt(workspace, "config", "user.email", "fixture@mc.invalid");
  mkdirSync(join(workspace, "openwiki"));
  writeFileSync(join(workspace, "README.md"), "split-brain fixture\n");
  writeFileSync(join(workspace, "openwiki", "index.md"), "---\ntitle: Fixture\ntype: Index\n---\n# Fixture\n");
  writeFileSync(join(workspace, "openwiki", ".last-update.json"), `{"status":"complete"}`);
  await gitAt(workspace, "add", "-A");
  await gitAt(workspace, "commit", "-q", "-m", "fixture base");
  writeFileSync(join(workspace, "VISION.md"), "Make project work reliable and easy to verify.\n");
  return gitAt(workspace, "rev-parse", "culler");
}

/** The fixture starts after Project setup accepts its environment. */
function recordReadyEnvironment(spine: string, sourceCommit: string): void {
  const image = `sha256:${"a".repeat(64)}`;
  const configuration = {
    base_digest: image,
    dockerfile: null,
    build_files: [],
    build_args: {},
    setup: { command: "", directory: "." },
    checks: [],
    services: [],
    variables: [],
    secrets: [],
    declarations: [],
    source_commit: sourceCommit,
  };
  const db = new Database(spine);
  try {
    db.transaction(() => {
      db.run(`INSERT INTO environment_revisions
        (id,project_id,editor,configuration,inputs_digest)
        VALUES ('fixture-environment','ws-test','fixture',?,?)`,
      [JSON.stringify(configuration), `sha256:${"b".repeat(64)}`]);
      db.run(`INSERT INTO project_environments
        (project_id,setup_id,selected_base_digest,selected_revision_id,
         selected_image_digest,draft_revision_id,readiness,phase,authorization)
        VALUES ('ws-test','fixture-setup',?,'fixture-environment',?,
                'fixture-environment','ready','complete','change')`, [image, image]);
    })();
  } finally {
    db.close();
  }
}

/** One Project with one seeded task. The wiki of the Project is current, so
 * no tick starts a wiki update. With `wikiBehind` the wiki is behind, and the
 * first tick starts a wiki update. */
async function makeFixture(options: { wikiBehind?: boolean } = {}): Promise<Fixture> {
  const root = mkdtempSync(join(suiteRoot, "case-"));
  const home = join(root, "home");
  const spine = join(root, "spine.db");
  const workspace = join(root, "workspace");
  mkdirSync(home);
  chmodSync(home, 0o700); // TrustHomeDir: operator-only MC_HOME
  mkdirSync(workspace);
  const baseSHA = await initializeGitWorkspace(workspace);

  const initialized = await mcJson(home, undefined, [
    "__init",
    "--spine", spine,
    "--project", "ws-test",
    "--workspace-root", workspace,
    // The grace keeps a retry lease live while the test reads the spine.
    // The fixture ages only the original claim.
    "--spawn-grace-s", "300",
  ]);
  const deploymentUUID = initialized.deployment_uuid;
  if (typeof deploymentUUID !== "string" || deploymentUUID === "") {
    throw new Error(`mc __init returned no deployment_uuid: ${JSON.stringify(initialized)}`);
  }
  await mcJson(home, spine, ["onboard", "home"]);
  recordReadyEnvironment(spine, baseSHA);
  writeFileSync(join(home, "deployment.uuid"), `${deploymentUUID}\n`, { mode: 0o600 });
  writeFileSync(join(home, "routing.md"), fakeRouting, { mode: 0o600 });
  // The mount plan carries the repository bind. The allowlist grants the
  // Project artifact root, where the merge candidate step stores the change
  // data. An earlier onboarding named the target, so it differs from the
  // Project id.
  const artifactRoot = join(workspace, ".mc-artifacts");
  mkdirSync(artifactRoot);
  appendFileSync(join(workspace, ".git", "info", "exclude"), ".mc-artifacts/\n");
  writeFileSync(join(home, "mount-allowlist"),
    `version = 1\n\n[[allow]]\npath = ${JSON.stringify(artifactRoot)}\ntarget = "earlier-project"\naccess = "ro"\n`, { mode: 0o600 });
  const added = await mcJson(home, spine, [
    "task", "add", "split-brain subject", "--project", "ws-test",
  ]);
  const taskId = added.task_id;
  if (typeof taskId !== "number") throw new Error(`task add returned no numeric id: ${JSON.stringify(added)}`);
  let wikiRun: string | null = null;
  if (!options.wikiBehind) {
    wikiRun = recordCurrentWiki(spine, "ws-test", baseSHA, new Date());
    if (wikiRun === null) throw new Error("the fixture recorded no current wiki");
  }

  return {
    root,
    home,
    spine,
    workspace,
    baseSHA,
    taskId,
    wikiRun,
    config: { ...testConfig, mcHome: home, workspaceRoot: workspace },
  };
}

/** The host root of the isolated repository of one task run. */
function runRoot(fixture: Fixture, run: string): string {
  return join(fixture.home, "runs", "repositories", run);
}

/** The isolated repository the resident binds at /workspace/source. */
function isolatedRepo(fixture: Fixture, run: string): string {
  return join(runRoot(fixture, run), "repo");
}

/** The host root of the isolated repository of the Scribe run of a wiki
 * update. */
function wikiRunRoot(fixture: Fixture, run: string): string {
  return join(fixture.home, "runs", "wiki-repositories", run);
}

/** The host stand-in for the container's fixed /workspace root: `source`
 * names the isolated repository of the run, as the bind does inside the
 * container. */
function storeRoot(fixture: Fixture, run: string, repository = isolatedRepo(fixture, run)): string {
  const store = join(fixture.root, `store-${run}`);
  if (!existsSync(store)) {
    mkdirSync(store);
    symlinkSync(repository, join(store, "source"));
  }
  return store;
}

interface RecordingFs {
  events: string[];
  fs: TickDeps["fs"];
}

function recordingFs(failBeforeFirstWrite = false): RecordingFs {
  const events: string[] = [];
  let shouldFail = failBeforeFirstWrite;
  return {
    events,
    fs: {
      mkdir: async (path) => {
        events.push(`mkdir:${path}`);
        await mkdir(path, { recursive: true });
      },
      writeFile: async (path, data, opts) => {
        events.push(`write:${path}`);
        if (shouldFail) {
          shouldFail = false;
          throw new Error("simulated resident death at the first launch write");
        }
        if (opts?.mode !== undefined) {
          await writeFile(path, data, { mode: opts.mode });
        } else {
          await writeFile(path, data);
        }
      },
      readFile: (path) => readFile(path, "utf8"),
      readdir: (path) => readdir(path),
      rm: async (path, opts) => {
        events.push(`rm:${path}`);
        await rm(path, { force: true, recursive: opts?.recursive });
      },
    },
  };
}

function recordingMc(
  fixture: Fixture,
  effects: Effect[],
  killAfterFirstDispatch: boolean,
): Exec {
  let shouldKill = killAfterFirstDispatch;
  return async (argv, stdin) => {
    const result = await runMc(fixture.home, fixture.spine, argv, stdin);
    if (argv.length === 1 && argv[0] === "dispatch" && result.exitCode === 0) {
      effects.push(tickEffect(JSON.parse(result.stdout) as JsonObject) as unknown as Effect);
      if (shouldKill) {
        shouldKill = false;
        throw new Error("simulated resident death after committed dispatch");
      }
    }
    return result;
  };
}

type FirstRunMode = "ordinary" | "die-before-start" | "die-after-start";

/** The Docker seam that resident restarts share. The two injected modes
 * select the side of the container start on which the resident dies. Every
 * later command uses the ordinary behavior. */
class RecordingDocker {
  calls: string[][] = [];
  live = new Set<string>();
  private firstStart = true;

  constructor(private readonly firstRunMode: FirstRunMode) {}

  exec: Exec = async (argv) => {
    this.calls.push([...argv]);
    if (argv[0] === "create") {
      const nameAt = argv.indexOf("--name");
      const name = nameAt >= 0 ? argv[nameAt + 1] : undefined;
      if (name === undefined) throw new Error("test Docker received a create without --name");
      return ok(`${name}-container-id\n`);
    }
    if (argv[0] === "start") {
      const name = argv[1];
      if (name === undefined) throw new Error("test Docker received start without a name");
      const isFirst = this.firstStart;
      this.firstStart = false;
      if (isFirst && this.firstRunMode === "die-before-start") {
        throw new Error("simulated resident death before container start");
      }
      this.live.add(name);
      if (isFirst && this.firstRunMode === "die-after-start") {
        throw new Error("simulated resident death after container start");
      }
      return ok("");
    }

    if (argv[0] === "inspect") return fail(1, `Error: No such container: ${argv.at(-1)}`);
    if (argv[0] === "stop") {
      const name = argv[1];
      if (name === undefined) throw new Error("test Docker received stop without a name");
      if (!this.live.delete(name)) return fail(1, "No such container");
      return ok("");
    }
    return ok("");
  };
}

/** ws-test's lease row (`mc lease list`, one row per Project). */
async function lock(fixture: Fixture): Promise<JsonObject> {
  const listed = await mcJson(fixture.home, fixture.spine, ["lease", "list"]);
  const rows = listed.leases as JsonObject[];
  const row = rows.find((lease) => lease.project === "ws-test");
  if (row === undefined) throw new Error(`mc lease list holds no ws-test row: ${JSON.stringify(listed)}`);
  return row;
}

const releasedLease = {
  run_id: null,
  project: "ws-test",
  subject: null,
  owner: null,
  acquired_at: null,
  last_heartbeat_at: null,
  binding: null,
};

async function task(fixture: Fixture): Promise<JsonObject> {
  return mcJson(fixture.home, fixture.spine, ["task", "get", String(fixture.taskId)]);
}

/** The runs of the spine, without the run that made the wiki of the fixture
 * current. */
async function runs(fixture: Fixture): Promise<JsonObject[]> {
  const listed = await mcJson(fixture.home, fixture.spine, ["run", "list"]);
  return (listed.runs as JsonObject[]).filter((row) => row.id !== fixture.wikiRun);
}

/** The wiki state of ws-test (`mc wiki-state list`, one row per Project). */
async function wikiState(fixture: Fixture): Promise<JsonObject> {
  const listed = await mcJson(fixture.home, fixture.spine, ["wiki-state", "list"]);
  const row = (listed.wiki_states as JsonObject[]).find((state) => state.project === "ws-test");
  if (row === undefined) throw new Error(`mc wiki-state list holds no ws-test row: ${JSON.stringify(listed)}`);
  return row;
}

async function packets(fixture: Fixture): Promise<JsonObject[]> {
  const listed = await mcJson(fixture.home, fixture.spine, ["packet", "list"]);
  return listed.packets as JsonObject[];
}

/** The merge operations of the fixture task, read from the spine. */
function mergeOperations(fixture: Fixture): JsonObject[] {
  const db = new Database(fixture.spine, { readonly: true });
  try {
    return db.query(`
      SELECT run_id, operation, finished, result
      FROM merge_operations WHERE task_id = ? ORDER BY rowid`).all(fixture.taskId) as JsonObject[];
  } finally {
    db.close();
  }
}

/** The temporary repositories of the task that the resident has not cleaned. */
function temporaryRepositories(taskRow: JsonObject): JsonObject[] {
  return (taskRow.delivery as JsonObject).temporary_repositories as JsonObject[];
}

/** The repository execution of one run, read from the spine. */
function executionRow(fixture: Fixture, run: string): JsonObject {
  const db = new Database(fixture.spine);
  try {
    const row = db.query(`
      SELECT run_id, operation, owner, ended_at, end_reason, retry_after, charged, stopped_at
      FROM repository_executions WHERE run_id = ?`).get(run) as JsonObject | null;
    if (row === null) throw new Error(`the spine holds no repository execution of run ${run}`);
    return row;
  } finally {
    db.close();
  }
}

function activeExecutionCount(fixture: Fixture): number {
  const db = new Database(fixture.spine);
  try {
    const row = db.query("SELECT COUNT(*) AS count FROM repository_executions WHERE ended_at IS NULL").get() as { count: number };
    return row.count;
  } finally {
    db.close();
  }
}

/** Moves the last heartbeat of an active execution 90 seconds back, so mc
 * accepts the stop confirmation of a later owner. */
function ageExecutionHeartbeat(spine: string, run: string): void {
  const db = new Database(spine);
  try {
    const changed = db.run(
      "UPDATE repository_executions SET heartbeat_uptime_ms = heartbeat_uptime_ms - 90000 WHERE run_id = ? AND ended_at IS NULL",
      [run],
    );
    if (changed.changes !== 1) throw new Error(`aged ${changed.changes} execution rows, want 1`);
  } finally {
    db.close();
  }
}

/** The repository lane of one resident over the fixture spine. The lane
 * timers use a fake scheduler, so a heartbeat, a retry, or a due-time timer
 * runs only when the test advances it. The test sends each start event. */
class FixtureLane {
  readonly scheduler = new FakeScheduler();
  readonly followers = new FakeFollow();
  /** The body of each start request. */
  readonly requests: JsonObject[] = [];
  /** Each execution that a start response listed, once, in order. */
  readonly listed: JsonObject[] = [];
  readonly exits: number[] = [];
  readonly lane: RepositoryLane;
  private inFlight = 0;

  constructor(deps: TickDeps) {
    const runMc = deps.runMc;
    this.lane = createRepositoryLane({
      runMc: async (argv, stdin, options) => {
        this.inFlight += 1;
        try {
          const start = argv[0] === "delivery" && argv[1] === "start";
          if (start && stdin !== undefined) this.requests.push(JSON.parse(new TextDecoder().decode(stdin)) as JsonObject);
          const result = await runMc(argv, stdin, options);
          if (start && result.exitCode === 0) {
            const response = JSON.parse(result.stdout) as JsonObject;
            for (const execution of response.executions as JsonObject[]) {
              if (!this.listed.some((seen) => seen.run_id === execution.run_id)) this.listed.push(execution);
            }
          }
          return result;
        } finally {
          this.inFlight -= 1;
        }
      },
      // The fixture has no Collie container and no heartbeat container list.
      docker: (argv, stdin, options) => argv[0] === "ps" ? Promise.resolve(ok("")) : deps.docker(argv, stdin, options),
      log: deps.log,
      fs: deps.fs,
      config: deps.config,
      sleep: deps.sleep,
      schedule: this.scheduler.schedule,
      cancel: this.scheduler.cancel,
      follow: this.followers.follow,
      now: this.scheduler.clock,
      maintenance: async () => false,
      exit: (code) => { this.exits.push(code); },
    });
  }

  /** Waits until the lane has no mc call in flight and no active flow. */
  async quiesce(): Promise<void> {
    const deadline = Date.now() + 30_000;
    let quiet = 0;
    while (quiet < 3) {
      if (Date.now() > deadline) throw new Error("the repository lane did not finish its work within 30 seconds");
      await new Promise((resolveQuiet) => setTimeout(resolveQuiet, 20));
      quiet = this.inFlight === 0 && this.lane.activeCount() === 0 ? quiet + 1 : 0;
    }
  }
}

function setRetryTime(fixture: Fixture, owner: "task" | "project", time: string): void {
  const db = new Database(fixture.spine);
  try {
    if (owner === "task") db.run("UPDATE tasks SET run_retry_at=? WHERE id=?", [time, fixture.taskId]);
    else db.run("UPDATE projects SET infrastructure_next_retry_at=? WHERE id='ws-test'", [time]);
  } finally {
    db.close();
  }
}

function ageLeasePastSpawnWatchdog(spine: string): void {
  const db = new Database(spine);
  try {
    const changed = db.run(
      "UPDATE leases SET acquired_uptime_ms = acquired_uptime_ms - 600000 WHERE project = 'ws-test' AND run_id IS NOT NULL",
    );
    if (changed.changes !== 1) throw new Error(`aged ${changed.changes} lease rows, want 1`);
  } finally {
    db.close();
  }
}

function ageLeasePastDeadline(spine: string): void {
  const db = new Database(spine);
  try {
    const changed = db.run("UPDATE leases SET acquired_uptime_ms=acquired_uptime_ms-600000, deadline_uptime_ms=acquired_uptime_ms-1 WHERE project='ws-test' AND run_id IS NOT NULL");
    if (changed.changes !== 1) throw new Error(`aged ${changed.changes} lease rows, want 1`);
  } finally {
    db.close();
  }
}

/** Runs one agent-facing mc verb with the identity the container would hold.
 * The isolated repository of the run stands in for /workspace/source. The
 * Scribe role has the repository of its wiki update run. */
async function runScopedMc(
  fixture: Fixture,
  runId: string,
  role: string,
  argv: string[],
  extraEnv: Record<string, string> = {},
  allowWrite: string[] = [],
): Promise<JsonObject> {
  const identity = join(fixture.root, `setup-${role}-${runId}.json`);
  writeFileSync(identity, JSON.stringify({
    run_id: runId,
    tier: "pipeline",
    role,
    pool_ids: [],
    // The writable set of the run. A terminal that records an artifact path
    // requires the plane of that path in this set.
    mounts: { allow_write: allowWrite },
  }));
  const env = mcEnv(fixture.home, fixture.spine);
  env.MC_RUN_JSON = identity;
  env.MC_TASK_STORE_ROOT = role === "scribe"
    ? storeRoot(fixture, runId, join(wikiRunRoot(fixture, runId), "repo"))
    : storeRoot(fixture, runId);
  Object.assign(env, extraEnv);
  const result = await runProcess([mcBin, ...argv], { env });
  if (result.exitCode !== 0) {
    throw new Error(
      `scoped ${role} mc ${argv.join(" ")} exited ${result.exitCode}: ${result.stderr || result.stdout}`,
    );
  }
  return result.stdout.trim() === "" ? {} : JSON.parse(result.stdout) as JsonObject;
}

/** The durable artifact plane of the fixture. Every verdict and report path
 * a terminal records lives inside it, because a terminal opens the path it
 * records. */
function artifactPlane(fixture: Fixture): { artifacts: string; plane: string } {
  const artifacts = join(fixture.root, "artifacts");
  const plane = join(artifacts, "ws-fixture");
  mkdirSync(plane, { recursive: true });
  return { artifacts, plane };
}

function planeFile(fixture: Fixture, relative: string, body: string): string {
  const { plane } = artifactPlane(fixture);
  const path = join(plane, String(fixture.taskId), relative);
  mkdirSync(join(path, ".."), { recursive: true });
  writeFileSync(path, body);
  return path;
}

/** The Worker's hand-in: one commit in the isolated repository, made on top
 * of the assigned input commit, then `mc complete --commit`. */
async function playWorker(fixture: Fixture, run: string): Promise<string> {
  const repo = isolatedRepo(fixture, run);
  expect(await gitAt(repo, "rev-parse", "HEAD")).toBe(fixture.baseSHA);
  writeFileSync(join(repo, "landing.txt"), `reviewed landing for task ${fixture.taskId}\n`);
  await gitAt(repo, "add", "landing.txt");
  await gitAt(
    repo,
    "-c", "user.name=mc worker",
    "-c", "user.email=worker@mc.invalid",
    "commit", "-q", "-m", `task ${fixture.taskId}: reviewed landing`,
  );
  const sha = await gitAt(repo, "rev-parse", "HEAD");
  await runScopedMc(fixture, run, "worker", [
    "complete", String(fixture.taskId),
    "--run", run,
    "--status", "worked",
    "--commit", sha,
  ]);
  return sha;
}

async function playVerifier(fixture: Fixture, run: string, sha: string): Promise<void> {
  expect(await gitAt(isolatedRepo(fixture, run), "rev-parse", "HEAD")).toBe(sha);
  const { artifacts, plane } = artifactPlane(fixture);
  const evidence = planeFile(fixture, "verification/gate-ladder.md", "Criterion 1: the landing commit is present. PASS.\n");
  await runScopedMc(fixture, run, "verifier", [
    "verifier", "verdict", String(fixture.taskId),
    "--run", run,
    "--outcome", "pass",
    "--evidence", evidence,
    "--sha", sha,
  ], { MC_ARTIFACTS_ROOT: artifacts }, [plane]);
}

/** The hand-in of the Scribe run of a wiki update with no wiki change. The
 * fixture wiki is complete, so the receipt names the start commit as both
 * its input and its source. */
async function playScribe(fixture: Fixture, run: string): Promise<JsonObject> {
  const repo = join(wikiRunRoot(fixture, run), "repo");
  expect(await gitAt(repo, "rev-parse", "HEAD")).toBe(fixture.baseSHA);
  const receipt = join(fixture.root, `wiki-receipt-${run}.json`);
  writeFileSync(receipt, JSON.stringify({
    format: 1,
    status: "noop",
    input_commit: fixture.baseSHA,
    source_commit: fixture.baseSHA,
    wiki_digest: wikiDigest(await wikiFiles(repo)),
    openwiki_version: VERSION,
    openwiki_revision: REVISION,
  }));
  return runScopedMc(fixture, run, "scribe", [
    "complete",
    "--run", run,
    "--commit", fixture.baseSHA,
    "--wiki-evidence", receipt,
  ]);
}

async function playPackager(fixture: Fixture, run: string, sha: string): Promise<void> {
  expect(await gitAt(isolatedRepo(fixture, run), "rev-parse", "HEAD")).toBe(sha);
  const { artifacts, plane } = artifactPlane(fixture);
  // The Packager terminal opens the render before it publishes the packet.
  // The render must live in a writable plane.
  const render = planeFile(fixture, "packet/render.html", "<h1>review packet</h1>\n");
  await runScopedMc(fixture, run, "packager", [
    "complete", String(fixture.taskId),
    "--run", run,
    "--status", "packaged",
    "--outputs", render,
    "--thesis", "the packet's own claim",
  ], { MC_ARTIFACTS_ROOT: artifacts }, [plane]);
}

function mountSource(argv: string[], target: string, readOnly = false): string {
  const suffix = `:${target}${readOnly ? ":ro" : ""}`;
  for (let i = 0; i < argv.length - 1; i++) {
    if (argv[i] === "-v" && argv[i + 1]!.endsWith(suffix)) {
      return argv[i + 1]!.slice(0, -suffix.length);
    }
  }
  throw new Error(`test Docker argv carries no ${target} mount: ${JSON.stringify(argv)}`);
}

/** Every bind of a Docker argv, keyed by its container destination. */
function mountTable(argv: string[]): Map<string, string> {
  const binds = new Map<string, string>();
  for (let i = 0; i < argv.length - 1; i++) {
    if (argv[i] !== "-v") continue;
    const [source, destination] = argv[i + 1]!.split(":");
    if (source !== undefined && destination !== undefined) binds.set(destination, source);
  }
  return binds;
}

/** The real mc-land script behind the resident's Docker seam. The container
 * binds become host paths in the envelope; the script then runs the same
 * `mc __repository` the merge container runs. Every other Docker command is
 * recorded like the spawn seam records it. */
class LocalLandDocker {
  calls: string[][] = [];
  results: ExecResult[] = [];
  readonly recording = new RecordingDocker("ordinary");

  constructor(private readonly fixture: Fixture) {}

  exec: Exec = async (argv) => {
    if (argv[0] !== "run") return this.recording.exec(argv);
    this.calls.push([...argv]);
    for (const flag of ["--rm", "--network", "none", "--user", "10002:10002", "--cap-drop", "ALL"]) {
      if (!argv.includes(flag)) {
        throw new Error(`local land Docker did not receive the confined one-shot shape (${flag}): ${JSON.stringify(argv)}`);
      }
    }
    const imageAt = argv.indexOf(this.fixture.config.image);
    const command = imageAt < 0 ? [] : argv.slice(imageAt + 1);
    if (command.length !== 3 || command[0] !== "mc-land" || command[2] !== "/mc/repository.json") {
      throw new Error(`local land Docker received the wrong command: ${JSON.stringify(argv)}`);
    }
    const operation = command[1]!;
    const envelope = JSON.parse(readFileSync(mountSource(argv, "/mc/repository.json", true), "utf8")) as JsonObject;
    const binds = mountTable(argv);
    const translated: JsonObject = { ...envelope };
    for (const key of ["workspace_root", "run_root"]) {
      const value = envelope[key];
      if (typeof value === "string" && binds.has(value)) translated[key] = binds.get(value);
    }
    if (operation === "apply") {
      if (realpathSync(translated.workspace_root as string) !== realpathSync(this.fixture.workspace)) {
        throw new Error(`local land Docker received the wrong Project mount: ${JSON.stringify(argv)}`);
      }
    } else if (translated.run_root !== runRoot(this.fixture, envelope.run_id as string)) {
      throw new Error(`local land Docker received the wrong repository mount: ${JSON.stringify(argv)}`);
    }
    const path = join(this.fixture.root, `land-${operation}-${envelope.run_id}.json`);
    writeFileSync(path, JSON.stringify(translated));
    const env = mcEnv(this.fixture.home, this.fixture.spine);
    env.PATH = `${suiteRoot}:${env.PATH ?? ""}`;
    const result = await runProcess(["sh", mcLandScript, operation, path], { cwd: this.fixture.root, env });
    this.results.push(result);
    return result;
  };
}

/** Commits real start requests and records while injecting only resident
 * death. A dead resident makes no further call. */
class LandingMc {
  calls: string[][] = [];
  /** Each start response that mc returned. */
  starts: JsonObject[] = [];
  private dead = false;

  constructor(
    private readonly fixture: Fixture,
    private readonly options: { killAfterApplyStart?: boolean; dieBeforeApplyRecord?: boolean } = {},
  ) {}

  exec: Exec = async (argv, stdin) => {
    this.calls.push([...argv]);
    if (this.dead) throw new Error("simulated resident death: the process is gone");
    if (
      this.options.dieBeforeApplyRecord && argv[0] === "delivery" && argv[1] === "record" &&
      argv[2] === "--operation" && argv[3] === "apply"
    ) {
      this.dead = true;
      throw new Error("simulated resident death after the merge, before mc delivery record");
    }
    const result = await runMc(this.fixture.home, this.fixture.spine, argv, stdin);
    if (argv[0] === "delivery" && argv[1] === "start" && result.exitCode === 0) {
      const response = JSON.parse(result.stdout) as JsonObject;
      this.starts.push(response);
      if (this.options.killAfterApplyStart &&
          (response.executions as JsonObject[]).some((execution) => execution.operation === "apply")) {
        this.dead = true;
        throw new Error("simulated resident death after the apply start, before the host work");
      }
    }
    return result;
  };

  /** Each execution that a start response listed, once, in order. */
  listed(): JsonObject[] {
    const listed: JsonObject[] = [];
    for (const response of this.starts) {
      for (const execution of response.executions as JsonObject[]) {
        if (!listed.some((seen) => seen.run_id === execution.run_id)) listed.push(execution);
      }
    }
    return listed;
  }
}

function effectStep(effect: Effect): string {
  switch (effect.action) {
    case "spawn":
      return `spawn:${effect.role}`;
    case "reap":
      return `reap:${effect.reason}`;
    case "idle":
      return `idle:${effect.reason}`;
    default:
      return effect.action;
  }
}

/** The steps from a seeded task to its published Review Packet. Each spawn
 * is one tick. The repository lane runs the repository operations between
 * two spawns. One start request can start several operations, so the steps
 * of each interval are sorted. The prepare record lets the next start request
 * publish the packet. */
const packagingSteps = [
  "spawn:worker",
  "repository:capture",
  "repository:cleanup",
  "spawn:verifier",
  "repository:cleanup",
  "spawn:packager",
  "repository:cleanup",
  "repository:cleanup",
  "repository:prepare",
];

/** Drives the fixture task through the ordinary resident to a published
 * Review Packet. The tick spawns each role. The repository lane captures
 * every hand-in, cleans every isolated repository, and prepares the merge
 * candidate through the real mc-land script. Returns the Worker's commit,
 * which the Verifier passed. */
async function packageTask(fixture: Fixture): Promise<string> {
  const effects: Effect[] = [];
  const docker = new LocalLandDocker(fixture);
  const runMc = recordingMc(fixture, effects, false);
  const rig = makeRig({
    runMc: async (args, stdin) => {
      const result = await runMc(args, stdin);
      if (result.exitCode !== 0) throw new Error(`${args.join(" ")}: ${result.stderr || result.stdout}`);
      return result;
    },
    docker: docker.exec,
    fs: recordingFs().fs,
    config: fixture.config,
  });
  const lane = new FixtureLane(rig.deps);
  const steps: string[] = [];
  let recorded = 0;
  const closeInterval = () => {
    const interval = lane.listed.slice(recorded).map((execution) => `repository:${String(execution.operation)}`);
    recorded = lane.listed.length;
    steps.push(...interval.sort());
  };
  await lane.lane.start();
  const loop = startTickLoop(rig.deps);
  let sha = "";
  try {
    for (const role of ["worker", "verifier", "packager"]) {
      await lane.quiesce();
      closeInterval();
      const before = effects.length;
      await rig.timer.fire();
      const effect = effects[before];
      if (effect === undefined) throw new Error("the tick committed no effect");
      steps.push(effectStep(effect));
      if (effect.action !== "spawn" || effect.role !== role) {
        throw new Error(`the tick committed ${effectStep(effect)}, not the ${role} spawn`);
      }
      switch (effect.role) {
        case "worker":
          sha = await playWorker(fixture, effect.run_id);
          break;
        case "verifier":
          await playVerifier(fixture, effect.run_id, sha);
          break;
        case "packager":
          await playPackager(fixture, effect.run_id, sha);
          break;
      }
      // The exit of the run container causes a start request.
      lane.lane.request("container-exit");
    }
    await lane.quiesce();
    closeInterval();
  } catch (error) {
    throw new Error(`${error instanceof Error ? error.message : String(error)}\nsteps: ${steps.join(", ")}\nresident log:\n${rig.logs.join("\n")}`);
  } finally {
    await loop.stop();
    await lane.lane.stop();
  }
  expect(steps, rig.logs.join("\n")).toEqual(packagingSteps);
  expect(lane.exits).toEqual([]);
  expect(docker.results).toHaveLength(1);
  expect(docker.results[0]!.exitCode).toBe(0);
  return sha;
}

type WorkspaceKillBoundary = "workspace-bytes" | "git-commit";

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

/** The scripted Worker. The first attempt leaves partial state in its
 * isolated repository and crashes. The retry receives a fresh isolated
 * repository at the input commit, so it checks that nothing of the first
 * attempt is visible, then makes and submits its own commit. */
function workerBehavior(
  fixture: Fixture,
  boundary: WorkspaceKillBoundary,
  attempt: number,
): JsonObject {
  const content = `partial workspace bytes for task ${fixture.taskId}`;
  const atInput = [
    "set -eu",
    `test "$(git rev-parse HEAD)" = ${shellQuote(fixture.baseSHA)}`,
    `test "$MC_SUBJECT_ID" = ${shellQuote(String(fixture.taskId))}`,
  ];
  const write = [
    `printf '%s\\n' ${shellQuote(content)} > split-brain.txt`,
    "git add split-brain.txt",
  ];
  const commit = (message: string) =>
    "git -c user.name='mc worker' -c user.email='worker@mc.invalid' " +
    `commit -q -m "task ${fixture.taskId}: split-brain work (${message})"`;

  if (attempt === 1) {
    const command = [
      ...atInput,
      ...write,
      ...(boundary === "git-commit" ? [commit("first attempt")] : []),
    ].join("; ");
    return {
      steps: [
        // The first pause lets the runner's first heartbeat land before the
        // crash. The second pause lets the Git boundary settle.
        { do: "sleep", seconds: 0.2 },
        { do: "exec", command },
        { do: "sleep", seconds: 0.2 },
        { do: "crash", code: 17 },
      ],
    };
  }

  const command = [
    ...atInput,
    "test ! -e split-brain.txt",
    "test -z \"$(git status --porcelain)\"",
    ...write,
    commit("retry $MC_RUN_ID"),
    'mc complete "$MC_SUBJECT_ID" --run "$MC_RUN_ID" --status worked --commit "$(git rev-parse HEAD)"',
  ].join("; ");
  return {
    steps: [
      { do: "exec", command },
      { do: "succeed", output: "submitted the retry commit" },
    ],
  };
}

interface RunnerResult extends ExecResult {
  runId: string;
  events: JsonObject[];
}

/** Translate one resident Docker spawn into the real host runner process,
 * with the isolated repository of the run as its working directory. */
class LocalWorkerDocker {
  calls: string[][] = [];
  runnerPromises: Promise<RunnerResult>[] = [];
  private runnerKills: Array<() => void> = [];
  private workerAttempt = 0;

  constructor(
    private readonly fixture: Fixture,
    private readonly boundary: WorkspaceKillBoundary,
  ) {}

  waitForRunner(index: number): Promise<RunnerResult> {
    const promise = this.runnerPromises[index];
    if (promise === undefined) throw new Error(`runner ${index} was never launched`);
    return new Promise((resolveRunner, rejectRunner) => {
      const timeout = setTimeout(() => {
        this.runnerKills[index]?.();
        rejectRunner(new Error(`runner ${index} did not exit within 10 seconds`));
      }, 10_000);
      promise.then(
        (result) => {
          clearTimeout(timeout);
          resolveRunner(result);
        },
        (error) => {
          clearTimeout(timeout);
          rejectRunner(error);
        },
      );
    });
  }

  killAll(): void {
    for (const kill of this.runnerKills) kill();
  }

  private pendingCreates = new Map<string, string[]>();

  exec: Exec = async (argv) => {
    this.calls.push([...argv]);
    if (argv[0] === "inspect") return fail(1, `Error: No such container: ${argv.at(-1)}`);
    if (argv[0] === "stop") return fail(1, "No such container");
    if (argv[0] === "create") {
      const nameAt = argv.indexOf("--name");
      const name = nameAt >= 0 ? argv[nameAt + 1] : undefined;
      if (name === undefined) throw new Error("local Worker Docker received a create without --name");
      this.pendingCreates.set(name, [...argv]);
      return ok(`${name}-container-id\n`);
    }
    if (argv[0] === "rm") {
      if (!this.pendingCreates.delete(argv[1] ?? "")) {
        throw new Error(`local Worker Docker received rm for an unknown container: ${JSON.stringify(argv)}`);
      }
      return ok("");
    }
    if (argv[0] !== "start") {
      throw new Error(`local Worker Docker received an unexpected command: ${JSON.stringify(argv)}`);
    }
    const createArgv = this.pendingCreates.get(argv[1] ?? "");
    if (createArgv === undefined) {
      throw new Error(`local Worker Docker received start for an uncreated container: ${JSON.stringify(argv)}`);
    }

    this.workerAttempt++;
    if (this.workerAttempt > 2) throw new Error("local Worker Docker received a third spawn");
    const hostEnvelope = mountSource(createArgv, "/mc/run.json", true);
    const hostSession = mountSource(createArgv, "/mc/session");
    const hostRepository = mountSource(createArgv, "/workspace/source");
    const envelope = JSON.parse(readFileSync(hostEnvelope, "utf8")) as JsonObject;
    if (envelope.role !== "worker" || typeof envelope.run_id !== "string") {
      throw new Error(`local Worker Docker received a non-Worker envelope: ${JSON.stringify(envelope)}`);
    }
    const runId = envelope.run_id;
    if (hostRepository !== isolatedRepo(this.fixture, runId)) {
      throw new Error(`local Worker Docker received a foreign repository bind: ${hostRepository}`);
    }
    const behaviorPath = join(this.fixture.root, `worker-${this.workerAttempt}-${runId}.json`);
    const localEnvelope = join(this.fixture.root, `local-run-${runId}.json`);
    writeFileSync(
      behaviorPath,
      JSON.stringify(workerBehavior(this.fixture, this.boundary, this.workerAttempt)),
    );
    const harnessConfig = envelope.harness_config as JsonObject;
    const mounts = envelope.mounts as JsonObject;
    harnessConfig.behavior = behaviorPath;
    mounts.session = hostSession;
    mounts.workspace = hostRepository;
    writeFileSync(localEnvelope, JSON.stringify(envelope));

    const env = mcEnv(this.fixture.home, this.fixture.spine);
    env.MC_RUN_JSON = localEnvelope;
    env.MC_TASK_STORE_ROOT = storeRoot(this.fixture, runId);
    env.MC_HARNESS_CLI = fakeHarnessCli;
    env.PATH = `${suiteRoot}:${env.PATH ?? ""}`;
    // The runner deletes $HOME/.agents/skills before it installs the skills
    // of the run. This runner is a host process, so it receives a private
    // home. An inherited home is the developer's own home.
    const agentHome = join(this.fixture.root, `agent-home-${runId}`);
    mkdirSync(agentHome, { recursive: true });
    env.HOME = agentHome;
    const proc = Bun.spawn(["bun", agentRunnerMain], {
      cwd: hostRepository,
      env,
      stdin: "ignore",
      stdout: "pipe",
      stderr: "pipe",
    });
    this.runnerKills.push(() => {
      try {
        proc.kill("SIGKILL");
      } catch {
        // Already-exited subprocesses need no cleanup.
      }
    });
    const completion = Promise.all([
      proc.exited,
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
    ]).then(([exitCode, stdout, stderr]) => {
      const result = { exitCode, stdout, stderr };
      const events = result.stdout
        .split("\n")
        .filter((line) => line.startsWith("{"))
        .map((line) => JSON.parse(line) as JsonObject);
      return { ...result, runId, events };
    });
    this.runnerPromises.push(completion);

    // `docker start` reports the start, not the eventual runner exit. The
    // test observes exit separately through this injected seam.
    return ok("");
  };
}

interface SurfaceOutboxRow extends JsonObject {
  id: number;
  kind: string;
  session_id: string | null;
  surface: string;
  channel_ref: string | null;
  payload: JsonObject;
  created_at: string;
}

function requireSurfaceRows(result: ExecResult, surface: string): SurfaceOutboxRow[] {
  if (result.exitCode !== 0) {
    throw new Error(`outbox poll ${surface} exited ${result.exitCode}: ${result.stderr}`);
  }
  const parsed = JSON.parse(result.stdout) as JsonObject;
  if (parsed.surface !== surface || !Array.isArray(parsed.rows)) {
    throw new Error(`malformed ${surface} outbox poll: ${result.stdout}`);
  }
  return parsed.rows.map((value) => {
    if (
      value === null || typeof value !== "object" || Array.isArray(value) ||
      typeof (value as JsonObject).id !== "number" ||
      (value as JsonObject).surface !== surface
    ) {
      throw new Error(`malformed ${surface} outbox row: ${JSON.stringify(value)}`);
    }
    return value as SurfaceOutboxRow;
  });
}

/** One native-surface pass: read durable cursor, push, then ack confirmation. */
async function drainSurfaceOnce(
  surface: string,
  runSurfaceMc: Exec,
  deliver: (row: SurfaceOutboxRow) => Promise<void>,
): Promise<SurfaceOutboxRow[]> {
  const rows = requireSurfaceRows(
    await runSurfaceMc(["outbox", "poll", "--surface", surface]),
    surface,
  );
  for (const row of rows) {
    await deliver(row);
    const ack = await runSurfaceMc([
      "outbox", "ack", String(row.id), "--surface", surface,
    ]);
    if (ack.exitCode !== 0) {
      throw new Error(`outbox ack ${row.id} exited ${ack.exitCode}: ${ack.stderr}`);
    }
  }
  return rows;
}

/** External API model: attempts are at-least-once; durable outbox id dedups. */
class DeduplicatingSurface {
  attempts: number[] = [];
  logicalPosts = new Map<number, SurfaceOutboxRow>();

  deliver = async (row: SurfaceOutboxRow): Promise<void> => {
    this.attempts.push(row.id);
    if (!this.logicalPosts.has(row.id)) this.logicalPosts.set(row.id, row);
  };
}

const boundaries = [
  {
    name: "action selected / before effect",
    killAfterDispatch: true,
    failBeforeWrite: false,
    oldFolderExists: false,
    oldEnvelopeExists: false,
    oldRepositoryExists: false,
    oldContainerExists: false,
    firstRunMode: "ordinary",
  },
  {
    name: "session folder / before repository allocation",
    killAfterDispatch: false,
    failBeforeWrite: true,
    oldFolderExists: true,
    oldEnvelopeExists: false,
    oldRepositoryExists: false,
    oldContainerExists: false,
    firstRunMode: "ordinary",
  },
  {
    name: "run.json / before container",
    killAfterDispatch: false,
    failBeforeWrite: false,
    oldFolderExists: true,
    oldEnvelopeExists: true,
    oldRepositoryExists: true,
    oldContainerExists: false,
    firstRunMode: "die-before-start",
  },
  {
    name: "container start / before first heartbeat",
    killAfterDispatch: false,
    failBeforeWrite: false,
    oldFolderExists: true,
    oldEnvelopeExists: true,
    oldRepositoryExists: true,
    oldContainerExists: true,
    firstRunMode: "die-after-start",
  },
] as const;

describe("split-brain convergence: committed spawn before first heartbeat", () => {
  for (const boundary of boundaries) {
    test(boundary.name, async () => {
      const fixture = await makeFixture();
      const effects: Effect[] = [];
      try {
        // The first resident commits the decision and dies at the selected
        // boundary. Every failure is outside mc.
        const firstFs = recordingFs(boundary.failBeforeWrite);
        const docker = new RecordingDocker(boundary.firstRunMode);
        const first = makeRig({
          runMc: recordingMc(fixture, effects, boundary.killAfterDispatch),
          docker: docker.exec,
          fs: firstFs.fs,
          config: fixture.config,
        });
        const firstLoop = startTickLoop(first.deps);
        await first.timer.fire();
        await firstLoop.stop();

        expect(effects).toHaveLength(1);
        const selected = effects[0]!;
        expect(selected).toMatchObject({
          action: "spawn",
          role: "worker",
          subject_id: fixture.taskId,
        });
        if (selected.action !== "spawn") throw new Error("first dispatch did not spawn");
        const oldRun = selected.run_id;
        const oldSession = join(fixture.home, "sessions", oldRun);
        const oldEnvelope = join(fixture.home, "runs", `${oldRun}.json`);
        const oldRepository = isolatedRepo(fixture, oldRun);
        const oldContainer = `mc-ws-test-${oldRun}`;

        const initialDockerCalls = boundary.oldEnvelopeExists ? 2 : 0;
        expect(docker.calls).toHaveLength(initialDockerCalls);
        if (initialDockerCalls === 2) {
          expect(docker.calls[0]![0]).toBe("create");
          expect(docker.calls[0]).toContain(oldContainer);
          expect(docker.calls[0]).toContain(`${oldSession}:/mc/session`);
          expect(docker.calls[0]).toContain(`${oldEnvelope}:/mc/run.json:ro`);
          // The Worker binds the isolated repository of its run, never the
          // Project repository, and runs hardened.
          expect(docker.calls[0]).toContain(`${oldRepository}:/workspace/source`);
          expect(docker.calls[0]).not.toContain(`${fixture.workspace}:/workspace/source`);
          expect(docker.calls[0]).toContain("10002:10002");
          expect(docker.calls[0]).toContain("--cap-drop");
          expect(docker.calls[1]).toEqual(["start", oldContainer]);
        }
        expect(docker.live.has(oldContainer)).toBe(boundary.oldContainerExists);
        expect(first.logs.some((line) => line.includes("simulated resident death"))).toBe(true);
        expect(existsSync(oldEnvelope)).toBe(boundary.oldEnvelopeExists);
        if (boundary.oldEnvelopeExists) {
          expect(JSON.parse(readFileSync(oldEnvelope, "utf8"))).toMatchObject({
            run_id: oldRun,
            subject_id: fixture.taskId,
            role: "worker",
            repository: { input_commit: fixture.baseSHA },
            mounts: { workspace: "/workspace/source" },
          });
        }
        expect(existsSync(oldSession)).toBe(boundary.oldFolderExists);
        if (boundary.oldFolderExists) expect(readdirSync(oldSession)).toEqual([]);
        if (boundary.killAfterDispatch) expect(firstFs.events).toEqual([]);
        // The allocation is durable on both sides: the isolated repository
        // on disk at the input commit, and the retained input reference in
        // the Project repository.
        expect(existsSync(oldRepository)).toBe(boundary.oldRepositoryExists);
        const retainedInput = await runProcess([
          "git", "-C", fixture.workspace, "rev-parse", "--verify", "--quiet",
          `refs/mc/tasks/${fixture.taskId}/inputs/${oldRun}`,
        ]);
        expect(retainedInput.exitCode === 0).toBe(boundary.oldRepositoryExists);
        if (boundary.oldRepositoryExists) {
          expect(await gitAt(oldRepository, "rev-parse", "HEAD")).toBe(fixture.baseSHA);
          expect(retainedInput.stdout.trim()).toBe(fixture.baseSHA);
        }
        expect(await git(fixture, "rev-parse", "culler")).toBe(fixture.baseSHA);

        // A reported failure retains the lease until the resident stops the container.
        expect(await lock(fixture)).toMatchObject({
          run_id: oldRun,
          project: "ws-test",
          subject: fixture.taskId,
          owner: "worker",
          last_heartbeat_at: null,
        });
        const beforeRuns = await runs(fixture);
        expect(beforeRuns).toHaveLength(1);
        expect(beforeRuns[0]).toMatchObject({
          id: oldRun,
          role: "worker",
          subject: fixture.taskId,
          outcome: boundary.killAfterDispatch ? null : "infra-failed",
          ended_at: boundary.killAfterDispatch ? null : expect.any(String),
          session_path: `sessions/${oldRun}`,
          binding: "fake/fake",
        });
        const checkpointTask = await task(fixture);
        expect(checkpointTask).toMatchObject({
          id: fixture.taskId,
          status: "seeded",
          dispatch_retries: 3,
          blocked: 0,
          stalled: 0,
        });
        expect(temporaryRepositories(checkpointTask)).toEqual(
          boundary.oldRepositoryExists ? [expect.objectContaining({ run_id: oldRun })] : [],
        );

        ageLeasePastSpawnWatchdog(fixture.spine);

        // A new resident with ordinary dependencies runs the normal loop and
        // the repository lane. The reap is one committed action. The lane
        // cleans an allocated repository. The reselection takes a later
        // tick. An absent container cannot prevent the reap.
        const recoveredFs = recordingFs();
        const recovered = makeRig({
          runMc: recordingMc(fixture, effects, false),
          docker: docker.exec,
          fs: recoveredFs.fs,
          config: fixture.config,
        });
        const recoveredLoop = startTickLoop(recovered.deps);
        const recoveredLane = new FixtureLane(recovered.deps);

        await recovered.timer.fire();
        expect(effects).toHaveLength(2);
        expect(effects[1]).toEqual({
          action: "reap",
          project: "ws-test",
          run_id: oldRun,
          reason: boundary.killAfterDispatch ? "spawn-watchdog" : "reported-outcome",
          stop_container: true,
        });
        const stopCalls = [
          ["stop", `mc-setup-${oldRun}`],
          ["inspect", "--type", "container", `mc-setup-${oldRun}`],
          ["stop", oldContainer],
          ...(boundary.oldContainerExists ? [] : [["inspect", "--type", "container", oldContainer]]),
        ];
        let dockerCalls = initialDockerCalls + stopCalls.length;
        expect(docker.calls).toHaveLength(dockerCalls);
        expect(docker.calls.slice(initialDockerCalls)).toEqual(stopCalls);
        expect(docker.live.has(oldContainer)).toBe(false);
        expect(recovered.logs.some((line) => line.includes(`mc-ws-test-${oldRun}`) && line.includes("No such container"))).toBe(
          !boundary.oldContainerExists,
        );
        expect(existsSync(oldEnvelope)).toBe(false);
        expect(existsSync(oldSession)).toBe(boundary.oldFolderExists);
        if (boundary.oldFolderExists) expect(readdirSync(oldSession)).toEqual([]);
        // Reap cannot touch the repository.
        expect(existsSync(oldRepository)).toBe(boundary.oldRepositoryExists);

        expect(await lock(fixture)).toMatchObject(releasedLease);
        const reapedRuns = await runs(fixture);
        expect(reapedRuns).toHaveLength(1);
        expect(reapedRuns[0]).toMatchObject({ id: oldRun, outcome: boundary.killAfterDispatch ? "reaped" : "infra-failed" });
        expect(reapedRuns[0]!.ended_at).not.toBeNull();
        expect(await task(fixture)).toMatchObject({
          status: "seeded",
          dispatch_retries: 3,
          blocked: 0,
        });

        // The lane of the new resident starts. It removes the isolated
        // repository of the ended run before the test selects a new Worker.
        // The cleanup confirms both run containers absent. It claims no
        // lease and runs as its own mechanical run.
        await recoveredLane.lane.start();
        await recoveredLane.quiesce();
        expect(recoveredLane.exits).toEqual([]);
        const cleanupRuns = boundary.oldRepositoryExists ? 1 : 0;
        if (boundary.oldRepositoryExists) {
          expect(recoveredLane.listed).toEqual([expect.objectContaining({
            operation: "cleanup",
            project: "ws-test",
            task_id: fixture.taskId,
            producer_run: oldRun,
          })]);
          const cleanupRun = recoveredLane.listed[0]!.run_id as string;
          expect(cleanupRun).not.toBe(oldRun);
          const cleanupCalls = [
            ["inspect", "--type", "container", oldContainer],
            ["inspect", "--type", "container", `mc-setup-${oldRun}`],
          ];
          expect(docker.calls.slice(dockerCalls)).toEqual(cleanupCalls);
          dockerCalls += cleanupCalls.length;
          expect(existsSync(runRoot(fixture, oldRun))).toBe(false);
          expect(temporaryRepositories(await task(fixture))).toEqual([]);
          expect(await lock(fixture)).toMatchObject(releasedLease);
          const cleanedRuns = await runs(fixture);
          expect(cleanedRuns).toHaveLength(2);
          expect(cleanedRuns.find((row) => row.id === cleanupRun)).toMatchObject({
            binding: "mechanical",
            outcome: "completed",
          });
          expect(executionRow(fixture, cleanupRun)).toMatchObject({ operation: "cleanup", end_reason: "result", charged: 0 });
          // The retained input reference outlives its repository.
          expect(await git(fixture, "rev-parse", `refs/mc/tasks/${fixture.taskId}/inputs/${oldRun}`)).toBe(fixture.baseSHA);
        } else {
          expect(recoveredLane.listed).toEqual([]);
          expect(docker.calls).toHaveLength(dockerCalls);
        }

        expect(await task(fixture)).toMatchObject({ project_infrastructure: { failure_count: 1, remaining: 2 } });
        setRetryTime(fixture, "project", "2999-01-01T00:00:00Z");
        await recovered.timer.fire();
        expect(effects[2]).toMatchObject({ action: "idle", reason: "project-infrastructure-retry-wait" });
        setRetryTime(fixture, "project", "2000-01-01T00:00:00Z");

        let effectCount = 3;
        await recovered.timer.fire();
        await recoveredLane.quiesce();
        effectCount += 1;
        expect(effects).toHaveLength(effectCount);
        const retry = effects[effectCount - 1]!;
        expect(retry).toMatchObject({
          action: "spawn",
          role: "worker",
          subject_id: fixture.taskId,
        });
        if (retry.action !== "spawn") throw new Error("retry dispatch did not spawn");
        const newRun = retry.run_id;
        expect(newRun).not.toBe(oldRun);

        const newSession = join(fixture.home, "sessions", newRun);
        const newEnvelope = join(fixture.home, "runs", `${newRun}.json`);
        const newRepository = isolatedRepo(fixture, newRun);
        expect(existsSync(newSession)).toBe(true);
        expect(existsSync(newEnvelope)).toBe(true);
        expect(JSON.parse(readFileSync(newEnvelope, "utf8"))).toMatchObject({
          run_id: newRun,
          subject_id: fixture.taskId,
          role: "worker",
          repository: { input_commit: fixture.baseSHA },
        });
        expect(existsSync(oldEnvelope)).toBe(false);
        expect(existsSync(oldSession)).toBe(boundary.oldFolderExists);
        if (boundary.oldFolderExists) expect(readdirSync(oldSession)).toEqual([]);
        expect(existsSync(runRoot(fixture, oldRun))).toBe(false);
        expect(await gitAt(newRepository, "rev-parse", "HEAD")).toBe(fixture.baseSHA);

        dockerCalls += 2;
        expect(docker.calls).toHaveLength(dockerCalls);
        const retryDocker = docker.calls.at(-2)!;
        expect(retryDocker[0]).toBe("create");
        expect(retryDocker).toContain(`mc-ws-test-${newRun}`);
        expect(retryDocker).toContain(`${newSession}:/mc/session`);
        expect(retryDocker).toContain(`${newEnvelope}:/mc/run.json:ro`);
        expect(retryDocker).toContain(`${newRepository}:/workspace/source`);
        expect(docker.calls.at(-1)).toEqual(["start", `mc-ws-test-${newRun}`]);
        expect([...docker.live]).toEqual([`mc-ws-test-${newRun}`]);
        expect(await lock(fixture)).toMatchObject({
          run_id: newRun,
          project: "ws-test",
          subject: fixture.taskId,
          owner: "worker",
          last_heartbeat_at: null,
        });
        const convergedRuns = await runs(fixture);
        expect(convergedRuns).toHaveLength(2 + cleanupRuns);
        expect(convergedRuns.find((row) => row.id === oldRun)).toMatchObject({
          outcome: boundary.killAfterDispatch ? "reaped" : "infra-failed",
        });
        expect(convergedRuns.find((row) => row.id === newRun)).toMatchObject({
          role: "worker",
          subject: fixture.taskId,
          project: "ws-test",
          outcome: null,
          ended_at: null,
          session_path: `sessions/${newRun}`,
          binding: "fake/fake",
        });
        const convergedTask = await task(fixture);
        expect(convergedTask).toMatchObject({
          status: "seeded",
          dispatch_retries: 3,
          blocked: 0,
        });
        expect(temporaryRepositories(convergedTask)).toEqual([expect.objectContaining({ run_id: newRun })]);
        expect(await git(fixture, "rev-parse", "culler")).toBe(fixture.baseSHA);

        // Another ordinary tick and start request see the live retry lease.
        // They cannot open a duplicate Run, charge a second retry, or repeat
        // any host effect.
        await recovered.timer.fire();
        recoveredLane.lane.request("tick");
        await recoveredLane.quiesce();
        effectCount += 1;
        expect(effects).toHaveLength(effectCount);
        expect(effects[effectCount - 1]).toEqual({ action: "idle", reason: "lease-held" });
        expect(docker.calls).toHaveLength(dockerCalls);
        expect(recoveredLane.listed).toHaveLength(cleanupRuns);
        expect(await runs(fixture)).toHaveLength(2 + cleanupRuns);
        expect(await task(fixture)).toMatchObject({ dispatch_retries: 3 });

        await recoveredLoop.stop();
        await recoveredLane.lane.stop();
      } finally {
        rmSync(fixture.root, { recursive: true, force: true });
      }
    }, 60_000);
  }
});

const workspaceBoundaries = [
  {
    name: "workspace bytes / before git commit",
    boundary: "workspace-bytes",
    checkpointCommits: 0,
    checkpointStatus: "A  split-brain.txt",
  },
  {
    name: "git commit / before complete",
    boundary: "git-commit",
    checkpointCommits: 1,
    checkpointStatus: "",
  },
] as const;

describe("split-brain convergence: scripted Worker Git boundary", () => {
  for (const boundary of workspaceBoundaries) {
    test(boundary.name, async () => {
      const fixture = await makeFixture();
      const effects: Effect[] = [];
      let docker: LocalWorkerDocker | undefined;
      try {
        const baseSHA = fixture.baseSHA;
        const seededTask = await task(fixture);
        expect(seededTask).toMatchObject({
          status: "seeded",
          completion_sha: null,
          dispatch_retries: 3,
          correction_count: 0,
        });
        expect(temporaryRepositories(seededTask)).toEqual([]);
        expect(await lock(fixture)).toMatchObject({ run_id: null, owner: null });

        const localDocker = new LocalWorkerDocker(fixture, boundary.boundary);
        docker = localDocker;
        const firstFs = recordingFs();
        const first = makeRig({
          runMc: recordingMc(fixture, effects, false),
          docker: localDocker.exec,
          fs: firstFs.fs,
          config: fixture.config,
        });
        const firstLoop = startTickLoop(first.deps);
        await first.timer.fire();

        expect(effects).toHaveLength(1);
        const selected = effects[0]!;
        expect(selected).toMatchObject({
          action: "spawn",
          role: "worker",
          subject_id: fixture.taskId,
          project: "ws-test",
        });
        if (selected.action !== "spawn") throw new Error("Worker checkpoint did not spawn");
        const oldRun = selected.run_id;
        const oldSession = join(fixture.home, "sessions", oldRun);
        const oldEnvelope = join(fixture.home, "runs", `${oldRun}.json`);
        const oldRepository = isolatedRepo(fixture, oldRun);
        expect(localDocker.calls).toHaveLength(2);
        expect(localDocker.calls[0]![0]).toBe("create");
        expect(localDocker.calls[1]).toEqual(["start", `mc-ws-test-${oldRun}`]);
        expect(localDocker.calls[0]).toContain(`mc-ws-test-${oldRun}`);
        expect(localDocker.calls[0]).toContain(`${oldSession}:/mc/session`);
        expect(localDocker.calls[0]).toContain(`${oldEnvelope}:/mc/run.json:ro`);
        expect(localDocker.calls[0]).toContain(`${oldRepository}:/workspace/source`);
        expect(localDocker.calls[0]).toContain("10002:10002");

        const firstRunner = await localDocker.waitForRunner(0);
        await firstLoop.stop();
        expect(firstRunner.runId).toBe(oldRun);
        expect(firstRunner.exitCode).toBe(17);
        const firstTools = firstRunner.events.filter((event) => event.event === "tool-use");
        expect(firstTools).toHaveLength(1);
        expect(firstTools[0]).toMatchObject({ exit_code: 0 });
        expect(firstRunner.events.some((event) => event.event === "turn-complete")).toBe(false);

        // The envelope the resident wrote is unchanged. The path translation
        // exists only in the local Docker copy.
        expect(JSON.parse(readFileSync(oldEnvelope, "utf8"))).toMatchObject({
          run_id: oldRun,
          role: "worker",
          harness_config: { behavior: "/mc/behaviors/worker.json" },
          mounts: { session: "/mc/session", workspace: "/workspace/source" },
          repository: { input_commit: baseSHA },
        });
        const oldTrace = join(oldSession, "native.jsonl");
        expect(existsSync(oldTrace)).toBe(true);
        expect(readFileSync(oldTrace, "utf8")).toContain('"event":"session-start"');
        expect(readFileSync(oldTrace, "utf8")).not.toContain('"event":"turn-complete"');

        const held = await lock(fixture);
        expect(held).toMatchObject({
          run_id: oldRun,
          project: "ws-test",
          subject: fixture.taskId,
          owner: "worker",
        });
        expect(typeof held.last_heartbeat_at).toBe("string");
        const checkpointTask = await task(fixture);
        expect(checkpointTask).toMatchObject({
          status: "seeded",
          completion_sha: null,
          dispatch_retries: 2,
          correction_count: 0,
          delivery: { input_commit: baseSHA, submitted_commit: null },
        });
        expect(temporaryRepositories(checkpointTask)).toEqual([expect.objectContaining({ run_id: oldRun })]);
        const checkpointRuns = await runs(fixture);
        const oldRow = checkpointRuns.find((row) => row.id === oldRun);
        expect(oldRow).toMatchObject({
          role: "worker",
          subject: fixture.taskId,
          project: "ws-test",
          outcome: "infra-failed",
          ended_at: expect.any(String),
          native_session_ref: "fake-session",
          trace_filename: "native.jsonl",
        });

        // The Project repository is unchanged. All partial state lives in
        // the isolated repository of the run.
        expect(await git(fixture, "rev-parse", "culler")).toBe(baseSHA);
        expect(await git(fixture, "status", "--porcelain")).toBe("?? VISION.md");
        expect(await git(fixture, "rev-parse", `refs/mc/tasks/${fixture.taskId}/inputs/${oldRun}`)).toBe(baseSHA);
        expect(await gitAt(oldRepository, "branch", "--show-current")).toBe(`mc/task-${fixture.taskId}`);
        expect(await gitAt(oldRepository, "status", "--porcelain")).toBe(boundary.checkpointStatus);
        expect(readFileSync(join(oldRepository, "split-brain.txt"), "utf8")).toBe(
          `partial workspace bytes for task ${fixture.taskId}\n`,
        );
        expect(Number(await gitAt(oldRepository, "rev-list", "--count", `${baseSHA}..HEAD`))).toBe(
          boundary.checkpointCommits,
        );
        const checkpointSHA = await gitAt(oldRepository, "rev-parse", "HEAD");
        expect(checkpointSHA === baseSHA).toBe(boundary.checkpointCommits === 0);
        // An unsubmitted commit exists only in the isolated repository.
        const checkpointInProject = await runProcess(["git", "-C", fixture.workspace, "cat-file", "-e", `${checkpointSHA}^{commit}`]);
        expect(checkpointInProject.exitCode === 0).toBe(boundary.checkpointCommits === 0);

        // The reported failure needs no aging: the lease holder has a final outcome.
        const recoveredFs = recordingFs();
        const recovered = makeRig({
          runMc: recordingMc(fixture, effects, false),
          docker: localDocker.exec,
          fs: recoveredFs.fs,
          config: fixture.config,
        });
        const recoveredLoop = startTickLoop(recovered.deps);
        const recoveredLane = new FixtureLane(recovered.deps);
        await recovered.timer.fire();
        expect(effects).toHaveLength(2);
        expect(effects[1]).toEqual({
          action: "reap",
          project: "ws-test",
          run_id: oldRun,
          reason: "reported-outcome",
          stop_container: true,
        });
        expect(localDocker.calls.filter((argv) => argv[0] === "stop").at(-1)).toEqual(["stop", `mc-ws-test-${oldRun}`]);
        expect(existsSync(oldEnvelope)).toBe(false);
        expect(existsSync(oldSession)).toBe(true);
        expect(await lock(fixture)).toMatchObject(releasedLease);
        expect(await task(fixture)).toMatchObject({
          status: "seeded",
          completion_sha: null,
          dispatch_retries: 2,
          correction_count: 0,
        });
        const afterReapRuns = await runs(fixture);
        const reapedRow = afterReapRuns.find((row) => row.id === oldRun)!;
        expect(reapedRow).toMatchObject({
          outcome: "infra-failed",
        });
        expect(reapedRow.ended_at).not.toBeNull();

        // Reap cannot touch Git. The checkpoint state remains in the
        // isolated repository until the cleanup operation.
        expect(await git(fixture, "rev-parse", "culler")).toBe(baseSHA);
        expect(await git(fixture, "status", "--porcelain")).toBe("?? VISION.md");
        expect(await gitAt(oldRepository, "rev-parse", "HEAD")).toBe(checkpointSHA);
        expect(await gitAt(oldRepository, "status", "--porcelain")).toBe(boundary.checkpointStatus);

        // The lane of the new resident starts. Its cleanup discards the ended
        // run's repository with its partial state. Only a submitted commit is
        // retained, and this run submitted none.
        await recoveredLane.lane.start();
        await recoveredLane.quiesce();
        expect(recoveredLane.exits).toEqual([]);
        expect(recoveredLane.listed).toEqual([expect.objectContaining({
          operation: "cleanup",
          task_id: fixture.taskId,
          producer_run: oldRun,
        })]);
        expect(existsSync(runRoot(fixture, oldRun))).toBe(false);
        expect(temporaryRepositories(await task(fixture))).toEqual([]);
        expect(await git(fixture, "rev-parse", "culler")).toBe(baseSHA);
        expect(await git(fixture, "status", "--porcelain")).toBe("?? VISION.md");
        expect(
          (await git(fixture, "for-each-ref", "--format=%(refname)", `refs/mc/tasks/${fixture.taskId}/versions/`))
            .split("\n")
            .filter((line) => line !== ""),
        ).toEqual([]);
        expect(await lock(fixture)).toMatchObject(releasedLease);

        expect(await task(fixture)).toMatchObject({ run_retry_at: expect.any(String) });
        setRetryTime(fixture, "task", "2000-01-01T00:00:00Z");
        await recovered.timer.fire();
        expect(effects).toHaveLength(3);
        const retry = effects[2]!;
        expect(retry).toMatchObject({
          action: "spawn",
          role: "worker",
          subject_id: fixture.taskId,
          project: "ws-test",
        });
        if (retry.action !== "spawn") throw new Error("Worker recovery did not respawn");
        const newRun = retry.run_id;
        expect(newRun).not.toBe(oldRun);
        const newSession = join(fixture.home, "sessions", newRun);
        const newEnvelope = join(fixture.home, "runs", `${newRun}.json`);
        const newRepository = isolatedRepo(fixture, newRun);
        const retryDocker = localDocker.calls.at(-2)!;
        expect(retryDocker[0]).toBe("create");
        expect(retryDocker).toContain(`mc-ws-test-${newRun}`);
        expect(retryDocker).toContain(`${newSession}:/mc/session`);
        expect(retryDocker).toContain(`${newEnvelope}:/mc/run.json:ro`);
        expect(retryDocker).toContain(`${newRepository}:/workspace/source`);
        expect(localDocker.calls.at(-1)).toEqual(["start", `mc-ws-test-${newRun}`]);

        const retryRunner = await localDocker.waitForRunner(1);
        expect(retryRunner.runId).toBe(newRun);
        expect(retryRunner.exitCode).toBe(0);
        const retryTools = retryRunner.events.filter((event) => event.event === "tool-use");
        expect(retryTools).toHaveLength(1);
        expect(retryTools[0]).toMatchObject({ exit_code: 0 });
        expect(retryRunner.events.some((event) => event.event === "turn-complete")).toBe(true);
        expect(readFileSync(join(newSession, "native.jsonl"), "utf8")).toContain(
          '"event":"turn-complete"',
        );

        // The hand-in released the lease and recorded a submission. The task
        // advances only once the resident captures the submitted commit.
        expect(await lock(fixture)).toMatchObject(releasedLease);
        const finalSHA = await gitAt(newRepository, "rev-parse", "HEAD");
        expect(finalSHA).not.toBe(baseSHA);
        expect(finalSHA).not.toBe(checkpointSHA);
        expect(await gitAt(newRepository, "status", "--porcelain")).toBe("");
        expect(Number(await gitAt(newRepository, "rev-list", "--count", `${baseSHA}..HEAD`))).toBe(1);
        expect(readFileSync(join(newRepository, "split-brain.txt"), "utf8")).toBe(
          `partial workspace bytes for task ${fixture.taskId}\n`,
        );
        expect(await task(fixture)).toMatchObject({
          status: "seeded",
          completion_sha: null,
          delivery: { submitted_commit: null },
        });

        // The exit of the retry container causes a start request. The lane
        // captures the submitted commit.
        recoveredLane.lane.request("container-exit");
        await recoveredLane.quiesce();
        const capture = recoveredLane.listed.find((execution) => execution.operation === "capture");
        expect(capture).toMatchObject({
          operation: "capture",
          task_id: fixture.taskId,
          producer_run: newRun,
          input_commit: baseSHA,
          result_commit: finalSHA,
          submission_kind: "submission",
        });
        expect(capture!.run_id).not.toBe(newRun);
        expect(effects).toHaveLength(3);
        await recoveredLoop.stop();
        await recoveredLane.lane.stop();

        expect(await lock(fixture)).toMatchObject(releasedLease);
        const convergedTask = await task(fixture);
        expect(convergedTask).toMatchObject({
          status: "worked",
          completion_sha: finalSHA,
          correction_count: 0,
          delivery: { input_commit: baseSHA, submitted_commit: finalSHA },
        });
        const convergedRuns = await runs(fixture);
        const workerRows = convergedRuns.filter((row) => row.binding === "fake/fake");
        expect(workerRows).toHaveLength(2);
        const finalOldRow = workerRows.find((row) => row.id === oldRun)!;
        const finalNewRow = workerRows.find((row) => row.id === newRun)!;
        expect(finalOldRow).toMatchObject({ outcome: "infra-failed" });
        expect(finalOldRow.ended_at).not.toBeNull();
        expect(finalNewRow).toMatchObject({
          subject: fixture.taskId,
          project: "ws-test",
          outcome: "completed",
          session_path: `sessions/${newRun}`,
          binding: "fake/fake",
          native_session_ref: "fake-session",
          trace_filename: "native.jsonl",
        });
        expect(finalNewRow.ended_at).not.toBeNull();
        expect(convergedRuns.find((row) => row.id === capture!.run_id)).toMatchObject({
          binding: "mechanical",
          outcome: "completed",
        });

        // The submitted commit is retained in the Project repository under
        // the run's own reference. `culler` and the checkout never moved, and
        // the first attempt's commit never reached the Project repository.
        expect(await git(fixture, "rev-parse", "culler")).toBe(baseSHA);
        expect(await git(fixture, "status", "--porcelain")).toBe("?? VISION.md");
        expect(await git(fixture, "rev-parse", `refs/mc/tasks/${fixture.taskId}/versions/${newRun}`)).toBe(finalSHA);
        expect(
          (await git(fixture, "for-each-ref", "--format=%(refname)", `refs/mc/tasks/${fixture.taskId}/versions/`))
            .split("\n")
            .filter((line) => line !== ""),
        ).toEqual([`refs/mc/tasks/${fixture.taskId}/versions/${newRun}`]);
        expect(await git(fixture, "show", `${finalSHA}:split-brain.txt`)).toBe(
          `partial workspace bytes for task ${fixture.taskId}`,
        );
        const firstAttemptInProject = await runProcess(["git", "-C", fixture.workspace, "cat-file", "-e", `${checkpointSHA}^{commit}`]);
        expect(firstAttemptInProject.exitCode === 0).toBe(boundary.checkpointCommits === 0);
      } finally {
        docker?.killAll();
        rmSync(fixture.root, { recursive: true, force: true });
      }
    }, 60_000);
  }
});

const landingBoundaries = [
  {
    name: "operator approve / before apply",
    killAfterApplyStart: true,
    dieBeforeApplyRecord: false,
    appliedAtCheckpoint: false,
  },
  {
    name: "apply success / before record",
    killAfterApplyStart: false,
    dieBeforeApplyRecord: true,
    appliedAtCheckpoint: true,
  },
] as const;

describe("split-brain convergence: approval and merge boundary", () => {
  for (const boundary of landingBoundaries) {
    test(boundary.name, async () => {
      const fixture = await makeFixture();
      try {
        const sha = await packageTask(fixture);
        const beforeApproval = await task(fixture);
        expect(beforeApproval).toMatchObject({
          status: "packaged",
          verified_sha: sha,
          decision: null,
          decided_at: null,
          archived: 0,
          blocked: 0,
          delivery: { submitted_commit: sha, destination_branch: "culler" },
        });
        expect(temporaryRepositories(beforeApproval)).toEqual([]);
        expect(await lock(fixture)).toMatchObject(releasedLease);
        expect(await git(fixture, "rev-parse", "culler")).toBe(fixture.baseSHA);
        expect(await git(fixture, "status", "--porcelain")).toBe("?? VISION.md");
        const livePackets = await packets(fixture);
        expect(livePackets).toEqual([expect.objectContaining({ task_id: fixture.taskId, archived: 0 })]);
        const packet = livePackets[0]!;
        const beforeLandingRuns = await runs(fixture);

        // The operator commits only the durable approval. It neither moves
        // Git nor creates an execution, Run, or lease. A later start request
        // starts the merge.
        const approved = await mcJson(fixture.home, fixture.spine, [
          "packet", "decide", String(fixture.taskId), "--approve",
          "--packet", String(packet.packet_id),
          "--version", String(packet.version_id),
        ]);
        expect(approved).toMatchObject({ decision: "approved", task_id: fixture.taskId });
        const approvedTask = await task(fixture);
        expect(approvedTask).toMatchObject({
          status: "packaged",
          verified_sha: sha,
          decision: "approved",
          archived: 0,
          blocked: 0,
        });
        expect(typeof approvedTask.decided_at).toBe("string");
        const decidedAt = approvedTask.decided_at;
        expect(await runs(fixture)).toEqual(beforeLandingRuns);
        expect(await git(fixture, "rev-parse", "culler")).toBe(fixture.baseSHA);
        expect(mergeOperations(fixture).filter((row) => row.operation === "apply")).toEqual([]);

        // The first resident starts the merge in its repository lane and
        // dies at the selected boundary.
        const localDocker = new LocalLandDocker(fixture);
        const firstMc = new LandingMc(fixture, {
          killAfterApplyStart: boundary.killAfterApplyStart,
          dieBeforeApplyRecord: boundary.dieBeforeApplyRecord,
        });
        const first = makeRig({
          runMc: firstMc.exec,
          docker: localDocker.exec,
          fs: recordingFs().fs,
          config: fixture.config,
        });
        const firstLane = new FixtureLane(first.deps);
        await firstLane.lane.start();
        await firstLane.quiesce();
        await firstLane.lane.stop();
        expect(firstLane.exits).toEqual([]);
        const firstOwner = firstLane.requests[0]!.owner as number;

        const listed = firstMc.listed();
        expect(listed).toHaveLength(1);
        const applyExecution = listed[0]!;
        expect(applyExecution).toMatchObject({
          operation: "apply",
          task_id: fixture.taskId,
          project: "ws-test",
          destination_branch: "culler",
          submitted_commit: sha,
        });
        expect(applyExecution.previous_run).toBeUndefined();
        const oldRun = applyExecution.run_id as string;
        expect(realpathSync(applyExecution.workspace_root as string)).toBe(realpathSync(fixture.workspace));
        // The authorization is the run that first selected the merge; the
        // merged candidate is the verified commit, because `culler` did not
        // move since the input commit.
        expect(applyExecution.application).toMatchObject({
          authorization: oldRun,
          candidate: {
            status: "merged",
            submitted_commit: sha,
            destination_branch: "culler",
            destination_commit: fixture.baseSHA,
            commit: sha,
          },
        });
        expect(first.logs.some((line) => line.includes("simulated resident death"))).toBe(true);
        expect(localDocker.calls).toHaveLength(boundary.appliedAtCheckpoint ? 1 : 0);

        // The approved packet remains current until mc records the merge.
        // The merge claims no lease. Its execution stays active until a
        // heartbeat limit or a new owner ends it.
        expect(await task(fixture)).toMatchObject({
          status: "packaged",
          verified_sha: sha,
          decision: "approved",
          decided_at: decidedAt,
          archived: 0,
          blocked: 0,
        });
        expect(await packets(fixture)).toEqual([
          expect.objectContaining({ task_id: fixture.taskId, archived: 0 }),
        ]);
        expect(await lock(fixture)).toMatchObject(releasedLease);
        const checkpointRuns = await runs(fixture);
        expect(checkpointRuns).toHaveLength(beforeLandingRuns.length + 1);
        expect(checkpointRuns.find((row) => row.id === oldRun)).toMatchObject({
          binding: "mechanical",
          outcome: null,
          ended_at: null,
        });
        expect(executionRow(fixture, oldRun)).toMatchObject({
          operation: "apply",
          owner: firstOwner,
          ended_at: null,
          stopped_at: null,
        });
        expect(mergeOperations(fixture).filter((row) => row.operation === "apply")).toEqual([
          expect.objectContaining({ run_id: oldRun, finished: 0 }),
        ]);

        const receiptRef = `refs/mc/applications/${oldRun}`;
        const receiptAtCheckpoint = await runProcess([
          "git", "-C", fixture.workspace, "rev-parse", "--verify", "--quiet", receiptRef,
        ]);
        expect(receiptAtCheckpoint.exitCode === 0).toBe(boundary.appliedAtCheckpoint);
        const destinationAtCheckpoint = boundary.appliedAtCheckpoint ? sha : fixture.baseSHA;
        if (boundary.appliedAtCheckpoint) {
          // The merge container updated `culler` and wrote its receipt; only
          // the record is missing.
          expect(localDocker.results[0]!.exitCode).toBe(0);
          expect(JSON.parse(localDocker.results[0]!.stdout)).toMatchObject({
            task_id: fixture.taskId,
            applied: { authorization: oldRun, commit: sha, recovered: false },
          });
          expect(readFileSync(join(fixture.workspace, "landing.txt"), "utf8")).toBe(
            `reviewed landing for task ${fixture.taskId}\n`,
          );
        } else {
          expect(existsSync(join(fixture.workspace, "landing.txt"))).toBe(false);
        }
        expect(await git(fixture, "rev-parse", "culler")).toBe(destinationAtCheckpoint);
        expect(await git(fixture, "status", "--porcelain")).toBe("?? VISION.md");

        ageExecutionHeartbeat(fixture.spine, oldRun);

        // A second resident registers a new owner. Its first start request
        // ends the merge of the earlier owner. Its lane confirms that the
        // merge container is absent, and mc accepts the confirmation after
        // 90 seconds without a heartbeat. Receipt recovery then waits for
        // the retry time of the merge.
        const recoveredMc = new LandingMc(fixture);
        const recovered = makeRig({
          runMc: recoveredMc.exec,
          docker: localDocker.exec,
          fs: recordingFs().fs,
          config: fixture.config,
        });
        const recoveredLane = new FixtureLane(recovered.deps);
        await recoveredLane.lane.start();
        await recoveredLane.quiesce();
        expect(recoveredLane.exits).toEqual([]);
        const recoveredOwner = recoveredLane.requests[0]!.owner as number;
        expect(recoveredOwner).toBeGreaterThan(firstOwner);
        expect(recovered.logs.some((line) => line.includes("simulated resident death"))).toBe(false);

        const stop = (recoveredMc.starts[0]!.stops as JsonObject[]).find((entry) => entry.run_id === oldRun);
        expect(stop).toMatchObject({ project: "ws-test", operation: "apply", owner: firstOwner });
        expect(stop!.wait_ms).toBeGreaterThan(0);
        expect(localDocker.recording.calls).toContainEqual(["inspect", "--type", "container", `mc-ws-test-${oldRun}`]);
        expect(recoveredLane.requests.some((body) => (body.stopped as string[]).includes(oldRun))).toBe(true);
        expect(recoveredMc.starts.some((response) => (response.confirmed as string[]).includes(oldRun))).toBe(true);

        const endedExecution = executionRow(fixture, oldRun);
        expect(endedExecution).toMatchObject({
          operation: "apply",
          owner: firstOwner,
          end_reason: "owner-change",
          charged: 0,
        });
        expect(endedExecution.ended_at).not.toBeNull();
        expect(endedExecution.stopped_at).not.toBeNull();
        expect(endedExecution.retry_after).not.toBeNull();
        expect((await runs(fixture)).find((row) => row.id === oldRun)).toMatchObject({
          outcome: "interrupted",
          ended_at: expect.any(String),
        });

        // The apply row stays unfinished. Receipt recovery waits, so no
        // execution is active and the packet states the recovery.
        expect(recoveredMc.listed()).toEqual([]);
        expect(activeExecutionCount(fixture)).toBe(0);
        expect(mergeOperations(fixture).filter((row) => row.operation === "apply")).toEqual([
          expect.objectContaining({ run_id: oldRun, finished: 0 }),
        ]);
        const lastResponse = recoveredMc.starts.at(-1)!;
        expect(typeof lastResponse.due_in_ms).toBe("number");
        expect(lastResponse.drain_pending).toBe(true);
        expect(await packets(fixture)).toEqual([
          expect.objectContaining({
            task_id: fixture.taskId,
            archived: 0,
            merge_state: "recovery",
            merge_state_text: "Culler checks the result of an interrupted merge.",
            merge_task_id: null,
          }),
        ]);
        expect(await task(fixture)).toMatchObject({
          status: "packaged",
          decision: "approved",
          decided_at: decidedAt,
          archived: 0,
          blocked: 0,
        });
        expect(await lock(fixture)).toMatchObject(releasedLease);
        expect(localDocker.calls).toHaveLength(boundary.appliedAtCheckpoint ? 1 : 0);

        // Git keeps the state of the checkpoint until receipt recovery.
        expect(await git(fixture, "rev-parse", "culler")).toBe(destinationAtCheckpoint);
        const receiptAfterStop = await runProcess([
          "git", "-C", fixture.workspace, "rev-parse", "--verify", "--quiet", receiptRef,
        ]);
        expect(receiptAfterStop.exitCode === 0).toBe(boundary.appliedAtCheckpoint);
        expect(await git(fixture, "status", "--porcelain")).toBe("?? VISION.md");

        // mc refuses each request of the earlier owner.
        const lateHeartbeat = await runMc(fixture.home, fixture.spine, ["delivery", "heartbeat", "--request", "-"],
          new TextEncoder().encode(JSON.stringify({ owner: firstOwner, running: [oldRun], stopped: [], containers: [] })));
        expect(lateHeartbeat.exitCode).toBe(1);
        expect(JSON.parse(lateHeartbeat.stdout)).toMatchObject({ error: { code: "stale-owner" } });

        const after = await dispatchOnce(fixture);
        expect(after.action).not.toBe("spawn");
        await recoveredLane.lane.stop();
      } finally {
        rmSync(fixture.root, { recursive: true, force: true });
      }
    }, 90_000);
  }
});

describe("split-brain convergence: message and outbox delivery boundary", () => {
  test("message/outbox insert / delivery", async () => {
    const fixture = await makeFixture();
    try {
      const started = await mcJson(fixture.home, fixture.spine, [
        "collie", "start", "--from", "dashboard:operator",
      ]);
      const session = started.session_id;
      if (typeof session !== "string") {
        throw new Error(`collie start returned no session: ${JSON.stringify(started)}`);
      }
      await mcJson(fixture.home, fixture.spine, [
        "collie", "bind", session, "--from", "discord:ops",
      ]);
      const baselineLock = await lock(fixture);
      const baselineTask = await task(fixture);
      const baselineRuns = await runs(fixture);

      // One real CLI transaction appends the source conversation row and
      // resolves its one non-origin destination into the outbox.
      const sent = await mcJson(fixture.home, fixture.spine, [
        "collie", "send", session,
        "--from", "dashboard:operator",
        "--body", "deliver this durable turn",
      ]);
      expect(sent).toMatchObject({ session_id: session, seq: 1, echoes: 1 });
      const messageID = sent.message_id;
      expect(typeof messageID).toBe("number");

      const realSurfaceMc: Exec = (argv) => runMc(fixture.home, fixture.spine, argv);
      const initialRows = requireSurfaceRows(
        await realSurfaceMc(["outbox", "poll", "--surface", "discord"]),
        "discord",
      );
      expect(initialRows).toHaveLength(1);
      const row = initialRows[0]!;
      expect(row).toMatchObject({
        kind: "collie_echo",
        session_id: session,
        surface: "discord",
        channel_ref: "ops",
      });
      expect(row.payload).toMatchObject({
        message_id: messageID,
        seq: 1,
        body: "deliver this durable turn",
      });

      const committed = new Database(fixture.spine);
      try {
        const message = committed.query(`
          SELECT id, session_id, seq, direction, surface, channel_ref, body
          FROM conversation_messages WHERE session_id = ?`).get(session) as JsonObject;
        expect(message).toEqual({
          id: messageID,
          session_id: session,
          seq: 1,
          direction: "inbound",
          surface: "dashboard",
          channel_ref: "operator",
          body: "deliver this durable turn",
        });
        const outbox = committed.query(`
          SELECT id, session_id, surface, channel_ref, delivered_at
          FROM outbox WHERE session_id = ?`).get(session) as JsonObject;
        expect(outbox).toEqual({
          id: row.id,
          session_id: session,
          surface: "discord",
          channel_ref: "ops",
          delivered_at: null,
        });
      } finally {
        committed.close();
      }

      const external = new DeduplicatingSurface();
      let diedBeforeAck = false;
      const firstSurfaceMc: Exec = async (argv) => {
        if (argv[0] === "outbox" && argv[1] === "ack" && !diedBeforeAck) {
          diedBeforeAck = true;
          throw new Error("simulated native-surface death after delivery, before ack");
        }
        return realSurfaceMc(argv);
      };
      await expect(
        drainSurfaceOnce("discord", firstSurfaceMc, external.deliver),
      ).rejects.toThrow("after delivery, before ack");

      // Physical delivery succeeded, but no canonical cursor advanced. A
      // restart sees the byte-identical durable row with the same dedup id.
      expect(external.attempts).toEqual([row.id]);
      expect([...external.logicalPosts.keys()]).toEqual([row.id]);
      const checkpointRows = requireSurfaceRows(
        await realSurfaceMc(["outbox", "poll", "--surface", "discord"]),
        "discord",
      );
      expect(checkpointRows).toEqual(initialRows);
      const checkpoint = new Database(fixture.spine);
      try {
        const delivery = checkpoint.query(
          "SELECT delivered_at FROM outbox WHERE id = ?",
        ).get(row.id) as JsonObject;
        expect(delivery.delivered_at).toBeNull();
        expect((checkpoint.query(
          "SELECT COUNT(*) AS n FROM conversation_messages WHERE session_id = ?",
        ).get(session) as JsonObject).n).toBe(1);
      } finally {
        checkpoint.close();
      }
      expect(await lock(fixture)).toEqual(baselineLock);
      expect(await task(fixture)).toEqual(baselineTask);
      expect(await runs(fixture)).toEqual(baselineRuns);

      // The restarted surface re-polls and retries the same id. Its external
      // API suppresses a second logical post. This time ack commits, but its
      // response is lost as the native process dies again.
      let lostAckResponse = false;
      const secondSurfaceMc: Exec = async (argv) => {
        const result = await realSurfaceMc(argv);
        if (
          argv[0] === "outbox" && argv[1] === "ack" &&
          result.exitCode === 0 && !lostAckResponse
        ) {
          lostAckResponse = true;
          throw new Error("simulated native-surface death after ack commit");
        }
        return result;
      };
      await expect(
        drainSurfaceOnce("discord", secondSurfaceMc, external.deliver),
      ).rejects.toThrow("after ack commit");
      expect(external.attempts).toEqual([row.id, row.id]);
      expect([...external.logicalPosts.keys()]).toEqual([row.id]);

      const afterAck = new Database(fixture.spine);
      let deliveredAt: string;
      try {
        const state = afterAck.query(`
          SELECT delivered_at,
                 (SELECT COUNT(*) FROM conversation_messages WHERE session_id = ?) AS messages,
                 (SELECT COUNT(*) FROM outbox WHERE session_id = ?) AS outbox_rows
          FROM outbox WHERE id = ?`).get(session, session, row.id) as JsonObject;
        expect(typeof state.delivered_at).toBe("string");
        deliveredAt = state.delivered_at as string;
        expect(state.messages).toBe(1);
        expect(state.outbox_rows).toBe(1);
      } finally {
        afterAck.close();
      }

      // A second restart sees no pending delivery. Direct ack replay is an
      // inert success with the first timestamp, and neither bookkeeping path
      // deletes the source message or outbox history.
      expect(await drainSurfaceOnce("discord", realSurfaceMc, external.deliver)).toEqual([]);
      const ackReplay = await mcJson(fixture.home, fixture.spine, [
        "outbox", "ack", String(row.id), "--surface", "discord",
      ]);
      expect(ackReplay).toEqual({
        id: row.id,
        surface: "discord",
        acked: false,
        delivered_at: deliveredAt,
      });
      expect(external.attempts).toEqual([row.id, row.id]);
      const history = await mcJson(fixture.home, fixture.spine, [
        "collie", "history", session,
      ]);
      expect(history.messages).toEqual([
        expect.objectContaining({
          id: messageID,
          seq: 1,
          direction: "inbound",
          body: "deliver this durable turn",
        }),
      ]);
      expect(await lock(fixture)).toEqual(baselineLock);
      expect(await task(fixture)).toEqual(baselineTask);
      expect(await runs(fixture)).toEqual(baselineRuns);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }, 30_000);
});

describe("a wiki update beside an agent run", () => {
  test("a wiki update and a Worker run end with their own results", async () => {
    const fixture = await makeFixture({ wikiBehind: true });
    try {
      // The complete tick objects. For one Project a tick can hold two
      // actions: one of the agent run with the Project lease and one of the
      // wiki update run.
      const ticks: JsonObject[] = [];
      const mcCalls: string[][] = [];
      const docker = new RecordingDocker("ordinary");
      const rig = makeRig({
        runMc: async (argv, stdin) => {
          mcCalls.push([...argv]);
          const result = await runMc(fixture.home, fixture.spine, argv, stdin);
          if (argv.length === 1 && argv[0] === "dispatch" && result.exitCode === 0) {
            ticks.push(JSON.parse(result.stdout) as JsonObject);
          }
          return result;
        },
        docker: docker.exec,
        fs: recordingFs().fs,
        config: fixture.config,
      });
      const lane = new FixtureLane(rig.deps);
      await lane.lane.start();
      const loop = startTickLoop(rig.deps);
      try {
        // The first tick starts the Worker run in its pipeline round and the
        // wiki update in its wiki round, at the commit of `culler`.
        await rig.timer.fire();
        expect(ticks, rig.logs.join("\n")).toHaveLength(1);
        const started = ticks[0]!.actions as JsonObject[];
        expect(started.map((action) => `${action.action}:${action.role}`)).toEqual(["spawn:worker", "spawn:scribe"]);
        expect(ticks[0]!.idle).toEqual([]);
        const workerSpawn = started.find((action) => action.action === "spawn" && action.role === "worker")!;
        const wikiSpawn = started.find((action) => action.action === "spawn" && action.role === "scribe")!;
        const workerRun = workerSpawn.run_id as string;
        const wikiRun = wikiSpawn.run_id as string;
        expect(workerSpawn).toMatchObject({ project: "ws-test", subject_id: fixture.taskId });
        expect(wikiSpawn).toMatchObject({
          project: "ws-test",
          subject_id: null,
          pool_ids: [],
          session_path: `sessions/${wikiRun}`,
          mount_plan: {
            entries: [],
            repository: { wiki_update: wikiRun, input_commit: fixture.baseSHA, destination_branch: "culler" },
            version: 1,
          },
        });
        expect((wikiSpawn.mount_plan as JsonObject).repository).not.toHaveProperty("task_id");
        expect((wikiSpawn.mount_plan as JsonObject).tool_home).toEqual((workerSpawn.mount_plan as JsonObject).tool_home);

        // The resident started one container for each run. The Worker run has
        // its repository below the task folder. The Scribe run has its
        // repository below the wiki folder, reads the Project content, and
        // writes only the wiki and the Git folder.
        const workerContainer = `mc-ws-test-${workerRun}`;
        const wikiContainer = `mc-ws-test-${wikiRun}`;
        const workerRepository = isolatedRepo(fixture, workerRun);
        const wikiRepository = join(wikiRunRoot(fixture, wikiRun), "repo");
        expect(docker.calls.map((argv) => argv[0]), rig.logs.join("\n")).toEqual(["create", "start", "create", "start"]);
        expect(docker.calls[0]).toContain(workerContainer);
        expect(docker.calls[0]).toContain(`${workerRepository}:/workspace/source`);
        expect(docker.calls[1]).toEqual(["start", workerContainer]);
        expect(docker.calls[2]).toContain(wikiContainer);
        expect(docker.calls[2]).toContain(`${wikiRepository}:/workspace/source:ro`);
        expect(docker.calls[2]).toContain(`${wikiRepository}/openwiki:/workspace/source/openwiki`);
        expect(docker.calls[2]).toContain(`${wikiRepository}/.git:/workspace/source/.git`);
        for (const create of [docker.calls[0]!, docker.calls[2]!]) {
          expect(create).toContain(`type=bind,src=${fixture.home}/state/projects/ws-test/home,dst=/home/agent`);
          expect(create).toContain("/home/agent/.agents:rw,uid=10002,gid=10002,mode=0700");
        }
        expect(docker.calls[3]).toEqual(["start", wikiContainer]);
        expect([...docker.live].sort()).toEqual([workerContainer, wikiContainer].sort());
        expect(await gitAt(workerRepository, "rev-parse", "HEAD")).toBe(fixture.baseSHA);
        expect(await gitAt(wikiRepository, "rev-parse", "HEAD")).toBe(fixture.baseSHA);
        expect(await gitAt(wikiRepository, "branch", "--show-current")).toBe("mc/wiki-update");
        expect(existsSync(runRoot(fixture, wikiRun))).toBe(false);
        expect(await git(fixture, "rev-parse", `refs/mc/wiki/inputs/${wikiRun}`)).toBe(fixture.baseSHA);
        const workerEnvelope = join(fixture.home, "runs", `${workerRun}.json`);
        const wikiEnvelope = join(fixture.home, "runs", `${wikiRun}.json`);
        expect(JSON.parse(readFileSync(workerEnvelope, "utf8"))).toMatchObject({
          run_id: workerRun,
          role: "worker",
          subject_id: fixture.taskId,
        });
        expect(JSON.parse(readFileSync(wikiEnvelope, "utf8"))).toMatchObject({
          run_id: wikiRun,
          role: "scribe",
          subject_id: null,
          project: "ws-test",
          pool_ids: [],
          repository: { input_commit: fixture.baseSHA },
          mounts: { workspace: "/workspace/source" },
        });

        // The Worker run holds the Project lease. The wiki update run holds
        // no lease.
        const workerLease = await lock(fixture);
        expect(workerLease).toMatchObject({
          run_id: workerRun,
          project: "ws-test",
          subject: fixture.taskId,
          owner: "worker",
        });
        expect(await wikiState(fixture)).toMatchObject({
          state: "update-active",
          accepted_commit: fixture.baseSHA,
          active: { run_id: wikiRun, run_ended_at: null, step: "run" },
          last: null,
        });
        const activeRuns = await runs(fixture);
        expect(activeRuns).toHaveLength(2);
        expect(activeRuns.find((row) => row.id === workerRun)).toMatchObject({
          role: "worker",
          subject: fixture.taskId,
          outcome: null,
          ended_at: null,
          wiki_update: null,
        });
        expect(activeRuns.find((row) => row.id === wikiRun)).toMatchObject({
          role: "scribe",
          subject: null,
          project: "ws-test",
          outcome: null,
          ended_at: null,
          binding: "fake/fake",
          wiki_update: { result: null, result_text: null },
        });
        const taskBesideWikiUpdate = await task(fixture);
        expect(taskBesideWikiUpdate).toMatchObject({ status: "seeded", blocked: 0, stalled: 0 });

        // The Scribe ends its run with no wiki change while the Worker run is
        // active. The wiki update succeeds with no merge. The task, its
        // allowances, and the Project lease stay equal.
        expect(await playScribe(fixture, wikiRun)).toEqual({
          run_id: wikiRun,
          wiki_commit: fixture.baseSHA,
          wiki_change: false,
        });
        expect(await task(fixture)).toEqual(taskBesideWikiUpdate);
        expect(await lock(fixture)).toEqual(workerLease);
        expect(await wikiState(fixture)).toMatchObject({
          state: "current",
          state_text: "The Project wiki is current.",
          accepted_commit: fixture.baseSHA,
          next_start_at: null,
          next_start_text: null,
          active: null,
          last: {
            run_id: wikiRun,
            result: "succeeded",
            result_text: "The Project wiki needed no change.",
            cause_text: null,
            start_commit: fixture.baseSHA,
            wiki_commit: fixture.baseSHA,
            merged_commit: null,
          },
        });
        const runsAfterWikiUpdate = await runs(fixture);
        expect(runsAfterWikiUpdate).toHaveLength(2);
        expect(runsAfterWikiUpdate.find((row) => row.id === wikiRun)).toMatchObject({
          outcome: "completed",
          ended_at: expect.any(String),
          wiki_update: { result: "succeeded", result_text: "The Project wiki needed no change.", cause_text: null },
        });
        expect(runsAfterWikiUpdate.find((row) => row.id === workerRun)).toMatchObject({ outcome: null, ended_at: null });
        expect(await git(fixture, "rev-parse", "culler")).toBe(fixture.baseSHA);

        // The next tick returns the stop of the ended wiki update run. The
        // resident stops only the containers of that run and removes only
        // its launch file. The Worker run keeps the lease.
        await rig.timer.fire();
        expect(ticks).toHaveLength(2);
        expect(ticks[1]).toEqual({
          action: "tick",
          actions: [{
            action: "reap",
            project: "ws-test",
            run_id: wikiRun,
            reason: "reported-outcome",
            stop_container: true,
          }],
          idle: [{ project: "ws-test", reason: "lease-held" }],
        });
        expect(docker.calls.slice(4)).toEqual([
          ["stop", `mc-setup-${wikiRun}`],
          ["inspect", "--type", "container", `mc-setup-${wikiRun}`],
          ["stop", wikiContainer],
        ]);
        expect([...docker.live]).toEqual([workerContainer]);
        expect(mcCalls).toContainEqual(["run", "stopped", wikiRun]);
        expect(existsSync(wikiEnvelope)).toBe(false);
        expect(existsSync(workerEnvelope)).toBe(true);
        expect(await lock(fixture)).toEqual(workerLease);

        // mc holds the stop confirmation, so the next tick repeats no stop.
        // The wiki is current, so the tick starts no wiki update.
        await rig.timer.fire();
        expect(ticks).toHaveLength(3);
        expect(ticks[2]).toEqual({
          action: "tick",
          actions: [],
          idle: [{ project: "ws-test", reason: "lease-held" }],
        });
        expect(docker.calls).toHaveLength(7);

        // The exit of the Scribe container causes a start request. The lane
        // removes the repository of the wiki update run and sends no
        // evaluation call. The repository of the Worker run stays.
        lane.lane.request("container-exit");
        await lane.quiesce();
        expect(lane.exits).toEqual([]);
        expect(lane.listed, rig.logs.join("\n")).toEqual([expect.objectContaining({
          operation: "cleanup",
          project: "ws-test",
          wiki_update: wikiRun,
          producer_run: wikiRun,
        })]);
        expect(lane.listed[0]).not.toHaveProperty("task_id");
        expect(existsSync(wikiRunRoot(fixture, wikiRun))).toBe(false);
        expect(existsSync(workerRepository)).toBe(true);
        expect(mcCalls.filter((argv) => argv[0] === "evaluation")).toEqual([]);
        expect(await task(fixture)).toEqual(taskBesideWikiUpdate);

        // The Worker hands in its commit after the end of the wiki update.
        // The lane captures the commit and removes the repository of the
        // Worker run.
        const sha = await playWorker(fixture, workerRun);
        lane.lane.request("container-exit");
        await lane.quiesce();
        expect(lane.exits).toEqual([]);
        expect(lane.listed.slice(1).map((execution) => `${execution.operation}:${execution.task_id}:${execution.producer_run}`).sort())
          .toEqual([`capture:${fixture.taskId}:${workerRun}`, `cleanup:${fixture.taskId}:${workerRun}`]);
        expect(existsSync(runRoot(fixture, workerRun))).toBe(false);
        expect(await task(fixture)).toMatchObject({
          status: "worked",
          completion_sha: sha,
          blocked: 0,
          stalled: 0,
          delivery: { input_commit: fixture.baseSHA, submitted_commit: sha },
        });
        expect(await lock(fixture)).toMatchObject(releasedLease);
        const endedRuns = await runs(fixture);
        expect(endedRuns.find((row) => row.id === workerRun)).toMatchObject({
          role: "worker",
          subject: fixture.taskId,
          outcome: "completed",
          wiki_update: null,
        });
        expect(endedRuns.filter((row) => row.role === "scribe")).toEqual([expect.objectContaining({
          id: wikiRun,
          subject: null,
          outcome: "completed",
          wiki_update: { result: "succeeded", result_text: "The Project wiki needed no change.", cause_text: null },
        })]);

        // The commit of the Worker waits for its review outside `culler`, so
        // the wiki stays current.
        expect(await git(fixture, "rev-parse", "culler")).toBe(fixture.baseSHA);
        expect(await git(fixture, "rev-parse", `refs/mc/tasks/${fixture.taskId}/versions/${workerRun}`)).toBe(sha);
        expect(await wikiState(fixture)).toMatchObject({
          state: "current",
          active: null,
          last: { run_id: wikiRun, result: "succeeded", merged_commit: null },
        });
      } finally {
        await loop.stop();
        await lane.lane.stop();
      }
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }, 90_000);
});
