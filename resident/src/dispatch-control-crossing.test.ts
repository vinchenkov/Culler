// A real mc binary checks the resident control frame and changes the lease state.
// The tests reject a frame with a different key order.

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { Database } from "bun:sqlite";
import { mkdtempSync, readFileSync, realpathSync, rmSync, utimesSync } from "node:fs";
import { mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { execMcVia } from "./resident-control";
import { CollieRuntimeRegistry } from "./collie-runtime";
import { collectPipelineLiveness, type PipelineLivenessEntry } from "./pipeline-liveness";
import type { Effect, Exec, ExecResult } from "./types";
import { applyEffect } from "./effects";
import { makeRig, testConfig } from "./test-helpers";

const repoRoot = resolve(import.meta.dir, "..", "..");
const mcSourceDir = join(repoRoot, "mc", "cmd", "mc");
const suiteRoot = realpathSync(mkdtempSync(join(tmpdir(), "mc-control-crossing-")));
const mcBin = join(suiteRoot, "mc");
const helperName = "mc-helper-crossing";
const heldRunId = "0123456789abcdef";

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

// Captured from Docker 29.4.0. The resident's own gatherer treats exactly this
// answer — and nothing weaker — as proof of absence.
const dockerNotFound: ExecResult = {
  exitCode: 1,
  stdout: "[]\n",
  stderr: `Error response from daemon: No such container: mc-ws-test-${heldRunId}\n`,
};

async function runProcess(argv: string[], env?: Record<string, string>): Promise<ExecResult> {
  const proc = Bun.spawn(argv, { cwd: mcSourceDir, env, stdout: "pipe", stderr: "pipe" });
  const [exitCode, stdout, stderr] = await Promise.all([
    proc.exited,
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
  ]);
  return { exitCode, stdout, stderr };
}

beforeAll(async () => {
  // The test_fake_routing build is what makes a host-side crossing runnable off
  // Docker: it resolves the warm helper from MC_HELPER instead of creating one,
  // so the broker reaches its control exchange exactly as production does.
  const tags = process.platform === "linux" ? "test_fake_routing,test_scratch_spine" : "test_fake_routing";
  const built = await runProcess(
    ["mise", "exec", "--", "go", "build", "-tags", tags, "-o", mcBin, "."],
    { ...(process.env as Record<string, string>), CGO_ENABLED: "0" },
  );
  if (built.exitCode !== 0) {
    throw new Error(`build real mc test binary: ${built.stderr || built.stdout}`);
  }
}, 300_000);

afterAll(() => {
  rmSync(suiteRoot, { recursive: true, force: true });
});

/** A VM uptime in milliseconds that no test reaches: no lease limit fires. */
const farFuture = 2 ** 50;

/** The boot id that mc reads for its VM clock. */
function bootId(): string {
  if (process.platform === "linux") return readFileSync("/proc/sys/kernel/random/boot_id", "utf8").trim();
  const read = Bun.spawnSync(["sysctl", "-n", "kern.bootsessionuuid"], { env: process.env });
  if (read.exitCode !== 0) throw new Error(`sysctl kern.bootsessionuuid: ${read.stderr.toString()}`);
  return read.stdout.toString().trim();
}

/** A real MC_HOME with a real spine, a real launch record for the holder of
 * ws-test's lease, and a real held-but-container-less lease. Returns the home. */
async function deployment(launchAgeS: number): Promise<string> {
  const home = realpathSync(mkdtempSync(join(suiteRoot, "home-")));
  const workspace = join(home, "workspace");
  await mkdir(workspace, { recursive: true });
  const spine = join(home, "spine.db");

  const init = await runProcess([
    mcBin, "__init", "--spine", spine, "--project", "ws-test",
    "--workspace-root", workspace,
  ], mcEnvironment(home));
  if (init.exitCode !== 0) throw new Error(`mc __init: ${init.stderr || init.stdout}`);
  const uuid = (JSON.parse(init.stdout) as { deployment_uuid?: string }).deployment_uuid;
  if (typeof uuid !== "string" || uuid === "") throw new Error("mc __init returned no deployment uuid");

  await writeFile(join(home, "deployment.uuid"), `${uuid}\n`, { mode: 0o600 });
  await writeFile(join(home, "routing.md"), fakeRouting, { mode: 0o600 });
  await writeFile(join(home, "mount-allowlist"), "version = 1\n", { mode: 0o600 });

  // The launch record the gatherer reads. Its mtime IS the age — the same host
  // clock on both sides of the subtraction, never the spine's — and its
  // Project names the container (`mc-<project>-<run_id>`).
  await mkdir(join(home, "runs"), { recursive: true });
  const envelope = join(home, "runs", `${heldRunId}.json`);
  await writeFile(envelope, JSON.stringify({ run_id: heldRunId, project: "ws-test" }), { mode: 0o600 });
  const stamp = new Date(Date.now() - launchAgeS * 1000);
  utimesSync(envelope, stamp, stamp);

  // Fixture-only surgery, the same tier as split-brain.test.ts: a lease that is
  // FRESH by every spine-side threshold. Nothing but a host observation can
  // reap it, which is precisely the wedge this feature closes.
  const db = new Database(spine);
  try {
    db.run(
      `INSERT INTO runs (id, tier, role, project, subject) VALUES (?, 'pipeline', 'worker', 'ws-test', NULL)`,
      [heldRunId],
    );
    db.run(
      `UPDATE leases SET run_id=?, owner='worker',
         acquired_at=datetime('now'), last_heartbeat_at=datetime('now'), boot_id=?,
         acquired_uptime_ms=?, heartbeat_uptime_ms=?, deadline_uptime_ms=? WHERE project='ws-test'`,
      [heldRunId, bootId(), farFuture, farFuture, farFuture],
    );
  } finally {
    db.close();
  }
  return home;
}

function mcEnvironment(home: string): Record<string, string> {
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (value === undefined) continue;
    if (["MC_HOME", "MC_SPINE", "MC_HELPER", "MC_RUN_JSON"].includes(key)) continue;
    env[key] = value;
  }
  env.MC_HOME = home;
  env.MC_RUN_JSON = join(home, "absent-run.json");
  return env;
}

/** A `docker` on PATH that turns the broker's helper exec into a local
 * invocation of the same real binary against the same real spine — the stand-in
 * for the lock domain, and the only stubbed hop in the chain. */
async function helperStubDir(home: string): Promise<string> {
  const dir = join(home, "stub");
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, "docker"), [
    "#!/bin/sh",
    `test "$1/$2/$3/$4" = "exec/-i/${helperName}/mc" || exit 91`,
    "shift 4",
    `exec env MC_SPINE="${join(home, "spine.db")}" MC_HOME= MC_HELPER= "${mcBin}" "$@"`,
    "",
  ].join("\n"), { mode: 0o755 });
  return dir;
}

async function dispatchThroughRealBinary(
  home: string,
  liveness: () => Promise<PipelineLivenessEntry[]>,
): Promise<ExecResult> {
  const stubDir = await helperStubDir(home);
  const previousPath = process.env.PATH;
  const previousHelper = process.env.MC_HELPER;
  const previousSpine = process.env.MC_SPINE;
  // execMcVia spawns with the resident's own environment plus MC_HOME, so the
  // helper name and the stubbed docker are handed over the same way the
  // supervision unit hands the real ones over.
  process.env.PATH = `${stubDir}:${previousPath ?? ""}`;
  process.env.MC_HELPER = helperName;
  delete process.env.MC_SPINE;
  try {
    const exec = execMcVia(
      [mcBin],
      { mcHome: home, releaseBuildId: "development", configSchemaVersion: 1 },
      noCollieContainers,
      new CollieRuntimeRegistry(Buffer.alloc(32, 5)),
      liveness,
    );
    return await exec(["dispatch"]);
  } finally {
    process.env.PATH = previousPath;
    if (previousHelper === undefined) delete process.env.MC_HELPER;
    else process.env.MC_HELPER = previousHelper;
    if (previousSpine !== undefined) process.env.MC_SPINE = previousSpine;
  }
}

const noCollieContainers: Exec = async (argv) => {
  if (argv[0] === "ps") return { exitCode: 0, stdout: "", stderr: "" };
  throw new Error(`unexpected resident docker call: ${argv.join(" ")}`);
};

describe("the dispatch control crossing, through the real mc binary", () => {
  test("an old active agent run remains visible after thirty-two completed runs", async () => {
    const home = await deployment(600);
    for (let index = 0; index < 32; index++) {
      const run = index.toString(16).padStart(16, "0");
      await writeFile(join(home, "runs", `${run}.json`), JSON.stringify({ run_id: run, project: "ws-test" }));
    }
    const result = await dispatchThroughRealBinary(home, () => collectPipelineLiveness({
      // The REAL gatherer, over the real runs directory and the real
      // not-found answer — not a hand-written observation.
      docker: async (argv) => {
        expect(argv).toEqual(["inspect", "--type", "container", `mc-ws-test-${heldRunId}`]);
        return dockerNotFound;
      },
      mcHome: home,
      now: () => Date.now(),
      runMc: (argv) => runProcess([mcBin, ...argv], {
        ...mcEnvironment(home), MC_SPINE: join(home, "spine.db"),
      }),
      stat: async (path) => ({
        mtimeMs: (await import("node:fs/promises").then((fs) => fs.stat(path))).mtimeMs,
      }),
      readFile: (path) => import("node:fs/promises").then((fs) => fs.readFile(path, "utf8")),
    }));

    expect(result.exitCode).toBe(0);
    // One tick object: the reap is ws-test's round's action, and it names the
    // Project whose container the resident stops.
    expect(JSON.parse(result.stdout)).toEqual({
      action: "tick",
      actions: [{
        action: "reap",
        project: "ws-test",
        run_id: heldRunId,
        reason: "container-gone",
        stop_container: true,
      }],
      idle: [],
    });
    const db = new Database(join(home, "spine.db"));
    try {
      const leaseBeforeStop = db.query(`SELECT run_id FROM leases WHERE project='ws-test'`).get() as { run_id: string | null };
      expect(leaseBeforeStop.run_id).toBe(heldRunId);
      const rig = makeRig({
        config: { ...testConfig, mcHome: home },
        docker: async (argv) => ({
          exitCode: 1, stdout: "[]\n", stderr: `Error: No such container: ${argv.at(-1)}`,
        }),
        runMc: (argv) => runProcess([mcBin, ...argv], {
          ...mcEnvironment(home), MC_SPINE: join(home, "spine.db"),
        }),
      });
      const effect = (JSON.parse(result.stdout) as { actions: Effect[] }).actions[0]!;
      await applyEffect(effect, rig.deps);
      const leaseAfterStop = db.query(`SELECT run_id FROM leases WHERE project='ws-test'`).get() as { run_id: string | null };
      expect(leaseAfterStop.run_id).toBeNull();
    } finally {
      db.close();
    }
  }, 120_000);

  test("a launch record inside the spawn grace leaves the lease alone", async () => {
    const home = await deployment(5);
    const result = await dispatchThroughRealBinary(home, () => collectPipelineLiveness({
      docker: async () => dockerNotFound,
      mcHome: home,
      now: () => Date.now(),
      runMc: (argv) => runProcess([mcBin, ...argv], {
        ...mcEnvironment(home), MC_SPINE: join(home, "spine.db"),
      }),
      stat: async (path) => ({
        mtimeMs: (await import("node:fs/promises").then((fs) => fs.stat(path))).mtimeMs,
      }),
      readFile: (path) => import("node:fs/promises").then((fs) => fs.readFile(path, "utf8")),
    }));

    expect(result.exitCode).toBe(0);
    expect(JSON.parse(result.stdout)).toEqual({
      action: "tick",
      actions: [],
      idle: [{ project: "ws-test", reason: "lease-held" }],
    });
  }, 120_000);

  test("the real binary refuses an observation whose keys are out of wire order", async () => {
    const home = await deployment(600);
    const result = await dispatchThroughRealBinary(home, async () => [
      // Same three facts, same values — only the key order differs. Both hops
      // re-marshal and require bytes.Equal, so this must refuse rather than
      // quietly reap on a frame the Go side would have encoded differently.
      { run_id: heldRunId, present: false, launch_age_s: 600 } as unknown as PipelineLivenessEntry,
    ]);

    expect(result.exitCode).toBe(1);
    const refusal = JSON.parse(result.stdout) as { error?: { code?: string; message?: string } };
    expect(refusal.error?.code).toBe("preflight.frame_invalid");
    expect(refusal.error?.message).toContain("canonical");
    const db = new Database(join(home, "spine.db"));
    try {
      const lease = db.query(`SELECT run_id FROM leases WHERE project='ws-test'`).get() as { run_id: string | null };
      expect(lease.run_id).toBe(heldRunId);
    } finally {
      db.close();
    }
  }, 120_000);
});
