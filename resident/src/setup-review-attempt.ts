#!/usr/bin/env bun
// setup-review-attempt.ts — the Docker acceptance check of the attempt limit,
// the history of a failed setup agent attempt, and the review attempt. It uses
// the real resident lanes and the real setup runner with a fake harness.
import assert from "node:assert/strict";
import { randomBytes, randomUUID } from "node:crypto";
import { chmod, mkdir, mkdtemp, readFile, readdir, realpath, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { createEnvironmentLane, ENVIRONMENT_ATTEMPT_TIMEOUT_MS, environmentContainerName, type EnvironmentOperation } from "./environment-lane";
import { completeTraceCopy, createTraceLane } from "./trace-lane";
import { createTraceMask } from "./trace-mask";
import { makeRig, ok, runtimeBinding } from "./test-helpers";
import { makeTestLogger } from "../../runner/shared/log";
import type { ExecResult, TickDeps } from "./types";
import type { ProjectEnvironment } from "../../runner/shared/project-environment";

const repository = resolve(import.meta.dir, "../..");
const token = randomBytes(6).toString("hex");
const runtimeVolume = `mc-review-runtime-${token}`;
const spineVolume = `mc-review-spine-${token}`;
const root = await realpath(await mkdtemp(join(tmpdir(), "mc-setup-review-")));
const home = join(root, "home"), source = join(root, "source"), fixtures = join(root, "fixtures");
const containers = new Set<string>(), volumes = new Set([runtimeVolume, spineVolume]);
const mcBinary = join(root, "mc");
const mcEnvironment = { ...process.env, MC_HOME: home, MC_RUN_JSON: join(root, "absent.json"), MC_SPINE: undefined, MC_HELPER: undefined };
const fixtureRuntime = process.env.CULLER_ACCEPT_RUNTIME_IMAGE ?? "mc-prod";
const fixtureImage = process.env.CULLER_ACCEPT_PROJECT_IMAGE ?? "mc-project";
const project = "review", first = "setup-first", review = "setup-review";
const traceReference = randomUUID();
const results: any[] = [];
let operation: EnvironmentOperation | undefined;
let traced = false;
let checks = 0;

async function execute(argv: string[], stdin?: Uint8Array, timeout = 120_000, env = process.env): Promise<ExecResult> {
  const child = Bun.spawn(argv, { stdin: stdin ?? "ignore", stdout: "pipe", stderr: "pipe", env });
  const timer = setTimeout(() => child.kill("SIGKILL"), timeout);
  try {
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]);
    return { exitCode, stdout, stderr };
  } finally { clearTimeout(timer); }
}
async function successful(argv: string[]): Promise<string> {
  const result = await execute(argv);
  assert.equal(result.exitCode, 0, `${argv.slice(0, 4).join(" ")}: ${result.stderr}\n${result.stdout}`);
  return result.stdout;
}
const docker = (argv: string[], stdin?: Uint8Array) => execute(["docker", ...argv], stdin);
const git = (...args: string[]) => successful(["git", "-C", source, ...args]);
async function put(path: string, content: string, mode = 0o644) {
  await mkdir(dirname(path), { recursive: true, mode: 0o777 });
  await writeFile(path, content, { mode });
}
function passed(message: string) { checks += 1; console.log(`PASS ${message}`); }
async function waitFor(test: () => Promise<boolean>, message: string, timeout = 180_000) {
  const until = Date.now() + timeout;
  while (!await test()) {
    assert(Date.now() < until, message);
    await Bun.sleep(200);
  }
}
async function behavior(command: string) {
  await put(join(root, "behaviors", "setup.json"), JSON.stringify({ steps: [
    { do: "exec", command }, { do: "succeed", output: "The setup check completed." },
  ] }));
}

const rig = makeRig();
const deps: TickDeps = {
  ...rig.deps,
  config: { ...rig.deps.config, mcHome: home, runtimeVolume, projectImage: fixtureImage, spineVolume,
    behaviorsDir: join(root, "behaviors"), image: fixtureRuntime, roleBehaviors: { setup: "/mc/behaviors/setup.json" } },
  fs: {
    mkdir: async (path, opts) => {
      await mkdir(path, { recursive: !opts?.exclusive, mode: opts?.mode ?? 0o777 });
      if (opts?.mode !== undefined) await chmod(path, opts.mode);
    },
    writeFile: async (path, body, opts) => { await writeFile(path, body, { mode: opts?.mode ?? 0o644 }); },
    readFile: path => readFile(path, "utf8"), readdir, rename,
    rm: async (path, opts) => { await rm(path, { recursive: opts?.recursive, force: true }); },
  },
  docker: async (argv, stdin) => {
    if (argv[0] === "create") containers.add(argv[argv.indexOf("--name") + 1]!);
    return docker(argv, stdin);
  },
  runMc: async (argv, input) => {
    if (argv[0] === "__project-home") return execute([mcBinary, ...argv], undefined, 30_000, mcEnvironment);
    if (argv[0] === "__trace-sessions") {
      return ok(JSON.stringify({ setup_pending: [], sessions: traced ? [{
        kind: "setup", id: first, session_path: `environment-runs/${first}/session`, format: "claude",
        native_session_ref: traceReference, trace_filename: "native.jsonl", agentsview_session: traceReference,
        display_name: `Review · setup · ${first}`, ended: true,
      }] : [] }));
    }
    if (argv[0] === "environment") {
      if (argv[1] === "work") return ok(JSON.stringify({ operations: operation ? [operation] : [] }));
      if (argv[1] === "selections") return ok('{"selections":[]}');
      if (argv[1] === "operation-result") results.push(JSON.parse(new TextDecoder().decode(input)));
    }
    return ok("{}");
  },
};
const traceLane = createTraceLane({
  runMc: deps.runMc, log: makeTestLogger("resident"), mcHome: home, now: Date.now, schedule: setTimeout,
  cancel: handle => clearTimeout(handle as ReturnType<typeof setTimeout>),
});
const history = {
  completeTraceCopy: (id: string) => completeTraceCopy(traceLane, home, id),
  mask: (text: string) => createTraceMask([]).apply(Buffer.from(text)).toString(),
};

/** Runs one setup agent attempt through the environment lane. */
async function attempt(next: EnvironmentOperation) {
  operation = next;
  const lane = createEnvironmentLane(deps, { history });
  try {
    await lane.request();
    await waitFor(async () => lane.activeCount() === 0, `The attempt ${next.id} exceeded its deadline.\n${rig.logs.join("\n")}`);
  } finally { operation = undefined; await lane.stop(); }
}

// The review harness reads only the history. It reports through the same command as a setup agent.
const reviewHarness = `import { projectFilesDigest } from "/opt/culler-runtime/app/src/shared/project-files.ts";
import { readFile, writeFile } from "node:fs/promises";
const envelope = await Bun.file("/mc/run.json").json();
const record = JSON.parse(await readFile("/mc/failed-attempt/limit-record.json", "utf8"));
const traceFolder = "/mc/traces/" + record.operation_id;
const status = JSON.parse(await readFile(traceFolder + "/status.json", "utf8"));
const trace = (await readFile(traceFolder + "/native.jsonl", "utf8")).trim().split("\\n").map(line => JSON.parse(line));
const log = await readFile("/mc/failed-attempt/logs/unit-tests.log", "utf8");
for (const path of ["/mc/failed-attempt/logs/changed.log", traceFolder + "/changed.jsonl", "/mc/failed-attempt/limit-record.json"]) {
  if (await writeFile(path, "changed").then(() => true, () => false)) throw new Error("The history path " + path + " permits a write.");
}
const stopped = record.process_list.find(entry => /^sh run-tests\\.sh/.test(entry.command));
const file = record.command_logs.find(entry => entry.path === "unit-tests.log");
const silent = Math.round((Date.parse(record.limit_at) - Date.parse(file.modified_at)) / 1000);
const details = [
  "The first attempt reached the attempt limit.",
  "Stopped at the limit: " + stopped.command,
  "Completed work: " + log.split("\\n").filter(line => line.startsWith("PASS ")).join(", "),
  "Time between the last output and the limit: " + silent + " s",
  "Processor time " + stopped.processor_time + " in the elapsed time " + stopped.elapsed,
  "Trace copy: " + status.files["native.jsonl"] + ", " + trace.length + " records, command result " + (trace.some(event => event.event === "tool-use") ? "present" : "absent"),
].join("\\n");
const report = { operation_id: envelope.run_id, outcome: "complete", configuration: envelope.environment.configuration,
  files_digest: await projectFilesDigest(process.cwd()), summary: "The first attempt reached the attempt limit. The assigned image shows no gap.", details };
const child = Bun.spawn(["mc", "environment", "report"], { stdin: "pipe", stdout: "inherit", stderr: "inherit" });
child.stdin.write(JSON.stringify(report));
child.stdin.end();
process.exit(await child.exited);
`;

try {
  await chmod(root, 0o777);
  await mkdir(home, { mode: 0o700 });
  await successful(["mise", "exec", "--", "go", "-C", join(repository, "mc"), "build", "-o", mcBinary, "./cmd/mc"]);
  await successful(["docker", "info", "--format", "{{.ServerVersion}}"]);
  const image = (await successful(["docker", "image", "inspect", "--format", "{{.Id}}", fixtureImage])).trim();
  // The fake control command keeps the report of the agent for this check.
  await put(join(fixtures, "mc"), "#!/bin/sh\nif [ \"$1 $2\" = \"environment report\" ]; then cat > /mc/session/report.json; fi\nprintf '{}\\n'\n", 0o755);
  for (const volume of volumes) await successful(["docker", "volume", "create", volume]);
  await successful(["docker", "run", "--rm", "--network", "none", "--user", "0:0",
    "--entrypoint", "/bin/sh", "-v", `${runtimeVolume}:/runtime-output`, "-v", `${repository}/runner:/current-runner:ro`,
    "-v", `${fixtures}:/fixtures:ro`, fixtureRuntime, "-ec",
    "cp -a /opt/culler-runtime/. /runtime-output/; cp -a /current-runner/. /runtime-output/app/src/; rm /runtime-output/commands/mc; cp /fixtures/mc /runtime-output/commands/mc; chmod 755 /runtime-output/commands/mc"]);
  await mkdir(source, { recursive: true, mode: 0o777 });
  await put(join(source, "run-tests.sh"), "#!/bin/sh\necho 'PASS test_one'\necho 'PASS test_two'\necho 'RUN test_three'\nsleep 900\necho 'PASS test_three'\n");
  await git("init", "--quiet", "-b", "culler");
  await git("config", "user.name", "Review fixture");
  await git("config", "user.email", "fixture@example.invalid");
  await git("add", "."); await git("-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "Add the review fixture");
  const configuration: ProjectEnvironment = { base_digest: image, build_files: [], build_args: {}, setup: { command: "", directory: "." },
    services: [], variables: [], secrets: [], declarations: [] };
  const base: EnvironmentOperation = { id: first, project_id: project, kind: "setup", image_digest: image, workspace_root: source,
    configuration, inputs_digest: `sha256:${"c".repeat(64)}`, revision_id: "fixture", brief: "Build and test the Project.", skills: [],
    runtime_binding: runtimeBinding("fake", "fake") };

  // The first attempt has 45 seconds until its attempt limit. Its test command does not end.
  const remaining = 45_000;
  await behavior("sh run-tests.sh > /mc/logs/unit-tests.log 2>&1");
  const started = Date.now();
  await attempt({ ...base, created_at: new Date(started - ENVIRONMENT_ATTEMPT_TIMEOUT_MS + remaining).toISOString() });
  assert(Date.now() - started >= remaining - 1000, "The first attempt ended before its attempt limit.");
  assert.equal(results.length, 1, JSON.stringify(results));
  assert.equal(results[0].status, "failed");
  assert.equal(results[0].error, "The environment execution reached its 45-minute limit. Culler retains its files.");
  assert.equal(results[0].metadata.attempt_limit, true);
  const state = JSON.parse(await successful(["docker", "inspect", environmentContainerName(home, first)]))[0].State;
  assert.equal(state.Running, false);
  passed("The first attempt reaches the attempt limit during a test command, and Culler stops its container.");

  const failedRoot = join(home, "environment-runs", first);
  const record = JSON.parse(await readFile(join(failedRoot, "limit-record.json"), "utf8"));
  const limitAt = Date.parse(record.limit_at);
  assert(Math.abs(limitAt - (started + remaining)) < 15_000, `The limit time is ${record.limit_at}.`);
  assert.equal(record.process_list_cause, undefined, JSON.stringify(record));
  const test = record.process_list.find((entry: any) => /^sh run-tests\.sh/.test(entry.command));
  assert(test, `The process list omits the test command: ${JSON.stringify(record.process_list)}`);
  assert(record.process_list.some((entry: any) => entry.command === "sleep 900"), JSON.stringify(record.process_list));
  for (const field of ["elapsed", "processor_time"]) assert.match(test[field], /^(?:\d+-)?(?:\d+:)?\d+:\d\d$/, field);
  assert.match(test.resident_memory, /^\d+[a-z]?$/);
  assert.deepEqual(record.command_logs.map((file: any) => file.path), ["unit-tests.log"]);
  const log = await readFile(join(failedRoot, "logs", "unit-tests.log"), "utf8");
  assert.equal(log, "PASS test_one\nPASS test_two\nRUN test_three\n");
  assert.equal(record.command_logs[0].bytes, Buffer.byteLength(log));
  assert(limitAt - Date.parse(record.command_logs[0].modified_at) >= 3000, JSON.stringify(record.command_logs));
  passed("The limit record states the processes of the container and the last write time of the command log, and the log folder stays on the host.");

  // The review attempt receives the history through read-only mounts.
  traced = true;
  await writeFile(join(root, "behaviors", "review.ts"), reviewHarness);
  await behavior("bun /mc/behaviors/review.ts");
  await attempt({ ...base, id: review, created_at: new Date().toISOString(), metadata: { replaces_operation_id: first, review_attempt: true } });
  assert.equal(results.length, 1, JSON.stringify(results));
  const events = (await readFile(join(home, "environment-runs", review, "session", "native.jsonl"), "utf8")).trim().split("\n").map(line => JSON.parse(line));
  for (const event of events.filter(event => event.event === "tool-use")) assert.equal(event.exit_code, 0, event.stderr + event.stdout);
  assert(events.some(event => event.event === "turn-complete"), "The review attempt did not complete.");
  const mounts = JSON.parse(await successful(["docker", "inspect", environmentContainerName(home, review)]))[0].Mounts;
  const mount = (destination: string) => mounts.find((entry: any) => entry.Destination === destination);
  // Docker Desktop can add a prefix to the host path.
  assert(mount(`/mc/traces/${first}`).Source.endsWith(join(home, "agentsview", "inspection", first)), mount(`/mc/traces/${first}`).Source);
  for (const destination of [`/mc/traces/${first}`, "/mc/failed-attempt/logs", "/mc/failed-attempt/limit-record.json"]) {
    assert.equal(mount(destination).RW, false, destination);
  }
  assert.equal(mount("/mc/logs").RW, true);
  passed("The second attempt reads the trace copy, the command log, and the limit record of the first attempt through read-only mounts.");

  const report = JSON.parse(await readFile(join(home, "environment-runs", review, "session", "report.json"), "utf8"));
  assert.equal(report.operation_id, review);
  assert.equal(report.outcome, "complete");
  assert.match(report.files_digest, /^sha256:[0-9a-f]{64}$/);
  assert.match(report.details, /^Stopped at the limit: sh run-tests\.sh/m);
  assert.match(report.details, /^Completed work: PASS test_one, PASS test_two$/m);
  const silent = Number(report.details.match(/^Time between the last output and the limit: (\d+) s$/m)?.[1]);
  assert(silent >= 3 && silent <= remaining / 1000, report.details);
  assert.match(report.details, /^Trace copy: ready, \d+ records, command result absent$/m);
  passed("The second attempt submits a report that states the stopped command, its completed work, and the time between its last output and the limit.");
  console.log(`The setup review attempt checks passed: ${checks}.`);
} finally {
  await traceLane.stop();
  for (const name of containers) await docker(["rm", "-f", name]);
  for (const volume of volumes) await docker(["volume", "rm", volume]);
  await rm(root, { recursive: true, force: true });
}
