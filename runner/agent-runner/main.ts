#!/usr/bin/env bun
// The runner prepares the repository of the run and starts the selected adapter.
// It reports process failures through mc. The mc program preserves accepted results.
// Heartbeat and report failures leave the resident watchdog as the recovery path.

import { modelResponseReporter } from "../shared/model-response";
import { makeLogger, parseLevel } from "../shared/log";
import { runLogSink } from "../shared/log-sink";
import { installEnvelopeSkills } from "./skills";
import { adapterFailure, readAdapterFailure, type AdapterFailure } from "./diagnostics";
import { prepareProjectEnvironment, prepareAvailableProjectEnvironment, readEnvironmentSecrets, type ProjectEnvironment } from "../shared/project-environment";
import { collectProjectEvidence } from "../shared/project-evidence";

// Fixed in-container paths; the env overrides exist ONLY for the test tier
// (runner-process.test.ts runs main() as a real subprocess against a temp
// envelope + a stub mc) — the agent container never sets either.
const RUN_JSON = process.env["MC_RUN_JSON"] || "/mc/run.json";
const HARNESS_CLI = process.env["MC_HARNESS_CLI"] || "/app/src/fake-harness/cli.ts";
const CODEX_ADAPTER = process.env["MC_CODEX_ADAPTER"] || "/app/src/agent-runner/adapters/codex.ts";
const OPENCODE_ADAPTER = process.env["MC_OPENCODE_ADAPTER"] || "/opt/culler-runtime/app/src/agent-runner/adapters/opencode.ts";
const CLAUDE_ADAPTER = process.env["MC_CLAUDE_ADAPTER"] || "/app/src/agent-runner/adapters/claude.ts";
const FAKE_NATIVE_FILENAME = "native.jsonl";

/** The non-secret runtime capability config.toml resolved for this run,
 * carried through the launch envelope. The runner selects its adapter from
 * the harness family and gives the adapter the model and the credential
 * channel. The runner holds no per-provider knowledge. No
 * field here is a credential. */
export interface RunEnvelopeRuntime {
  base_url: string;
  channel: string;
  harness: string;
  id: string;
  model: string;
  effort?: string;
}

export interface RunEnvelope {
  run_id: string;
  role: string;
	harness: string;
	model_binding: string;
  subject_id: number | null;
  pool_ids?: number[];
  heartbeat_interval_s: number;
  brief: string;
  /** This run's skill set: the role's command surface skill first, then
   * the operator's skills for the role and Project. Optional: an envelope
   * from an older `mc` carries none, and a run without one is degraded, not
   * broken — the frozen directive still names the command skill and its
   * path. */
  skills?: { name: string; body: string }[];
  /** What the host skill store could not deliver; written into the run's log. */
  skill_problems?: string[];
  mode?: "fresh" | "native";
  native_session_ref?: string;
  harness_config?: { behavior: string };
  runtime?: RunEnvelopeRuntime;
  /** The isolated repository of the run. `input_commit` is the commit the
   * preamble pins the repository to before the model sees the tree.
   * Proposal runs and Homie omit this field and skip the preamble. */
  repository?: { input_commit: string };
  mounts: { session: string; workspace?: string };
  environment?: { configuration: ProjectEnvironment; configuration_revision: string; image_digest: string };
}

export interface AdapterInvocation {
  argv: string[];
  defaultNativeFile?: string;
}

/** Select exactly one adapter.
 *
 * A real route is selected by the HARNESS FAMILY of the resolved capability the
 * envelope carries, not by a route key compiled in here: config.toml owns which
 * bindings exist, and an operator-defined binding on a shipped family must
 * launch without a code change. What stays compiled is the runner's own fact:
 * which adapters this image contains. MC_AGENT_RUNNER_ROUTES remains a
 * test/development fake stand-in list; production leaves it empty. */
export function adapterInvocation(
  run: RunEnvelope,
  fakeRoutes = process.env["MC_AGENT_RUNNER_ROUTES"] ?? "",
): AdapterInvocation {
  const privateBun = process.env.MC_PRIVATE_BUN || "bun";
  const routeKey = `${run.harness}/${run.model_binding}`;
  const standIns = fakeRoutes.split(",").filter((route) => route !== "");
  if (routeKey === "fake/fake" || standIns.includes(routeKey)) {
    const behavior = run.harness_config?.behavior;
    if (!behavior) throw new Error(`fake adapter route ${JSON.stringify(routeKey)} requires harness_config.behavior`);
    return {
      argv: [privateBun, HARNESS_CLI, "--behavior", behavior, "--session-dir", run.mounts.session],
      defaultNativeFile: FAKE_NATIVE_FILENAME,
    };
  }

  const mode = run.mode ?? "fresh";
  if (mode !== "fresh" && mode !== "native") throw new Error(`unsupported adapter mode ${JSON.stringify(mode)}`);
  if (mode === "native" && !run.native_session_ref) throw new Error("native adapter mode requires native_session_ref");
  const runtime = run.runtime;
  if (runtime === undefined || runtime === null || typeof runtime !== "object") {
    throw new Error(`route ${JSON.stringify(routeKey)} carries no resolved runtime capability`);
  }
  for (const field of ["base_url", "channel", "harness", "id", "model"] as const) {
    if (typeof runtime[field] !== "string") {
      throw new Error(`route ${JSON.stringify(routeKey)} carries an incomplete runtime capability`);
    }
  }
  if (runtime.harness !== run.harness || runtime.id !== run.model_binding) {
    throw new Error(`runtime capability does not describe route ${JSON.stringify(routeKey)}`);
  }
  if (runtime.harness !== "codex" && runtime.harness !== "claude-sdk" && runtime.harness !== "opencode") {
    throw new Error(`this image ships no ${JSON.stringify(runtime.harness)} adapter`);
  }
  if (runtime.base_url !== "" ||
      !((runtime.harness === "codex" && runtime.channel === "codex") ||
        (runtime.harness === "claude-sdk" && runtime.channel === "claude") ||
        (runtime.harness === "opencode" && runtime.channel === "opencode"))) {
    throw new Error("The runner requires a supported sign-in connection without a custom endpoint.");
  }
  const wikiInput = run.repository?.input_commit;
  if (runtime.effort !== undefined && typeof runtime.effort !== "string") {
    throw new Error("The runtime effort is invalid.");
  }
  const common = [
    ...(runtime.effort ? ["--effort", runtime.effort] : []),
    "--session-dir", run.mounts.session,
    "--workspace", run.mounts.workspace ?? "/workspace/source",
    ...(run.role === "scribe" ? ["--openwiki"] : []),
    ...(run.role === "scribe" && wikiInput !== undefined ? ["--openwiki-input-commit", wikiInput] : []),
  ];
  const resume = run.native_session_ref ? ["--native-ref", run.native_session_ref] : [];
  // An empty configured value means "the provider default", so it is omitted
  // rather than passed through as an empty flag value.
  const model = runtime.model === "" ? [] : ["--model", runtime.model];
  if (runtime.harness === "opencode") {
    return { argv: [privateBun, OPENCODE_ADAPTER, ...common, ...model, ...resume] };
  }
  if (runtime.harness === "codex") {
    return { argv: [privateBun, CODEX_ADAPTER, ...common, "--mode", mode, ...model, ...resume] };
  }
  if (runtime.harness === "claude-sdk") {
    return {
      argv: [
        privateBun, CLAUDE_ADAPTER, ...common,
        "--channel", runtime.channel, ...model, ...resume,
      ],
    };
  }
  throw new Error(`this image ships no ${JSON.stringify(runtime.harness)} adapter`);
}

export function nativeLocator(value: unknown, fallback?: string): string {
  const locator = typeof value === "string" && value !== "" ? value : fallback;
  if (!locator || locator.startsWith("/") || locator.split("/").some((part) => part === "" || part === "." || part === "..")) {
    throw new Error("adapter session-start omitted a safe native trace locator");
  }
  return locator;
}

/** A failed Git command returns its observed exit code and error text. */
export type PreambleExec = (argv: string[]) => Promise<{ exitCode: number; stderr: string }>;

const spawnPreambleProcess: PreambleExec = async (argv) => {
  try {
    const proc = Bun.spawn(argv, { stdin: "ignore", stdout: "ignore", stderr: "pipe" });
    const [exitCode, stderr] = await Promise.all([
      proc.exited,
      new Response(proc.stderr).text(),
    ]);
    return { exitCode, stderr };
  } catch (err) {
    return { exitCode: 127, stderr: err instanceof Error ? err.message : String(err) };
  }
};

/** The repository preamble: pin the isolated repository to the input commit
 * before the model sees the tree. `git clean -fdx` ignores info/exclude, so
 * crashed-run debris under `.claude/` goes too.
 *
 * Returns null to proceed, or the refusal reason. Keyed on the envelope
 * carrying a repository, never on the role: a run without one (propose,
 * Homie) launches untouched. */
export async function runStorePreamble(
  run: RunEnvelope,
  exec: PreambleExec = spawnPreambleProcess,
): Promise<string | null> {
  if (run.repository === undefined) return null;
  const expected = run.repository.input_commit;
  if (typeof expected !== "string" || !/^[0-9a-f]{40}$|^[0-9a-f]{64}$/.test(expected)) {
    return `repository.input_commit ${JSON.stringify(expected)} is not a canonical git object name`;
  }
  const workspace = run.mounts.workspace ?? "/workspace/source";
  const inside = await exec(["git", "-C", workspace, "rev-parse", "--is-inside-work-tree"]);
  if (inside.exitCode !== 0) {
    return `the repository at ${workspace} is missing or is not a git work tree: ${inside.stderr.trim()}`;
  }
  const resolvable = await exec(["git", "-C", workspace, "cat-file", "-e", `${expected}^{commit}`]);
  if (resolvable.exitCode !== 0) {
    return `input commit ${expected} does not resolve to a commit in the repository: ${resolvable.stderr.trim()}`;
  }
  if (run.role === "scribe") {
    const unchanged = await exec(["git", "-C", workspace, "diff", "--quiet", expected, "--"]);
    return unchanged.exitCode === 0 ? null : "The Scribe repository differs from its start commit.";
  }
  const reset = await exec(["git", "-C", workspace, "reset", "--hard", expected]);
  if (reset.exitCode !== 0) {
    return `git reset --hard ${expected} failed in the repository: ${reset.stderr.trim()}`;
  }
  const clean = await exec(["git", "-C", workspace, "clean", "-fdx"]);
  if (clean.exitCode !== 0) {
    return `git clean -fdx failed in the repository: ${clean.stderr.trim()}`;
  }
  return null;
}

/** The env-interpolation channel: how a scripted behavior "comprehends its
 * brief". MC_SPINE rides in via `base` (container env). */
export function harnessEnv(
  run: RunEnvelope,
  base: Record<string, string | undefined>,
): Record<string, string | undefined> {
  return {
    ...base,
    MC_RUN_ID: run.run_id,
    MC_ROLE: run.role,
    MC_SUBJECT_ID: run.subject_id == null ? "" : String(run.subject_id),
    MC_POOL_IDS: (run.pool_ids ?? []).join(","),
  };
}

/** Incremental line splitter for the stdout event stream: chunks arrive at
 * arbitrary boundaries; onLine fires once per complete line. */
export function makeLineSplitter(onLine: (line: string) => void): (chunk: string) => void {
  let buf = "";
  return (chunk: string) => {
    buf += chunk;
    for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
      onLine(buf.slice(0, i));
      buf = buf.slice(i + 1);
    }
  };
}

// The resident binds a per-run host directory at /mc/logs, so these records
// outlive the container — every run container is `--rm` and nothing reads its
// output, so before this a runner's account of what it did was destroyed the
// moment it finished.
//
// stderr when the bind is absent, which is every container the resident did not
// compose: the e2e lanes, a hand-run image, a deployment mid-upgrade. Same
// format either way, so `docker logs` and the host file read alike.
const log = makeLogger({
  comp: "agent-runner",
  level: parseLevel(process.env["MC_LOG_LEVEL"]),
  sink: runLogSink(),
});

async function mc(argv: string[], input?: unknown): Promise<number> {
  try {
    const proc = Bun.spawn(["mc", ...argv], {
      stdin: input === undefined ? "ignore" : "pipe",
      stdout: "ignore",
      stderr: "pipe",
    });
    if (input !== undefined && proc.stdin) { proc.stdin.write(JSON.stringify(input)); proc.stdin.end(); }
    const [code, stderr] = await Promise.all([
      proc.exited,
      new Response(proc.stderr).text(),
    ]);
    if (code !== 0) {
      log.warn("runner.mc_failed", `mc ${argv[0]} exited ${code}: ${stderr.trim()}`,
        { verb: argv[0] ?? "", exit: code });
    }
    return code;
  } catch (err) {
    log.warn("runner.mc_failed", `mc ${argv[0]} failed to start: ${String(err)}`,
      { verb: argv[0] ?? "" });
    return 2;
  }
}

async function reportInfrastructureFailure(run: RunEnvelope, condition: string, evidence = "", provider?: AdapterFailure): Promise<void> {
  const report = adapterFailure(condition, evidence);
  await mc([
    "run", "infra-failure", run.run_id,
    "--condition", report.condition,
    "--source", "runner", "--cause", "unknown",
    ...(report.evidence ? ["--evidence", report.evidence] : []),
    ...(provider?.http_status ? ["--http-status", String(provider.http_status)] : []),
    ...(provider?.provider_error_code ? ["--provider-error-code", provider.provider_error_code] : []),
    ...(provider?.network_error ? ["--network-error", provider.network_error] : []),
    ...(provider?.proxy_request_id ? ["--proxy-request-id", provider.proxy_request_id] : []),
  ]);
}

/** Materialize run.json's skill set at the container's private skill root.
 * The claude adapter installs a second copy into its own config dir, because
 * the SDK resolves user skills from CLAUDE_CONFIG_DIR rather than from HOME. */
async function installRunSkills(run: RunEnvelope): Promise<void> {
  await installEnvelopeSkills(run.skills, run.skill_problems, log.child({ run_id: run.run_id }));
}

async function executeRun(run: RunEnvelope): Promise<number> {
  let workflow: Awaited<ReturnType<typeof prepareProjectEnvironment>> | undefined;
  let preparationHeartbeat: ReturnType<typeof setInterval> | undefined;
  if (run.environment && run.repository && run.role !== "scribe") {
    await mc(["heartbeat", run.run_id]);
    preparationHeartbeat = setInterval(() => { void mc(["heartbeat", run.run_id]); }, run.heartbeat_interval_s * 1000);
  }
  try {
    return await executePreparedRun(run, async () => {
      if (!run.environment || !run.repository || run.role === "scribe") return process.env;
      const secrets = await readEnvironmentSecrets();
      const prepared = await prepareAvailableProjectEnvironment(run.environment.configuration,
        run.mounts.workspace ?? "/workspace/source", process.env, secrets);
      workflow = prepared.workflow;
      run.brief += prepared.evidence;
      const evidence = await collectProjectEvidence(run.environment.configuration,
        run.mounts.workspace ?? "/workspace/source", prepared.agentEnvironment);
      if (await mc(["environment", "run-evidence"], { run_id: run.run_id, ...evidence }) !== 0) {
        throw new Error("The Project tool and package evidence could not be recorded.");
      }
      return prepared.agentEnvironment;
    });
  } finally {
    if (preparationHeartbeat !== undefined) clearInterval(preparationHeartbeat);
    await workflow?.stop();
  }
}

async function executePreparedRun(run: RunEnvelope, prepare: () => Promise<Record<string, string | undefined>>): Promise<number> {
  const invocation = adapterInvocation(run);
  log.info("runner.started", `run ${run.run_id} role=${run.role} subject=${run.subject_id ?? "none"}`,
    { run_id: run.run_id, role: run.role, subject: run.subject_id ?? null });

  const preambleRefusal = await runStorePreamble(run);
  if (preambleRefusal !== null) {
    log.error("runner.store_preamble_refused", preambleRefusal, { run_id: run.run_id });
    await reportInfrastructureFailure(run, `repository preamble refused: ${preambleRefusal}`);
    return 2;
  }

  // The skill directory belongs to this container, even when its Project home is shared.
  await installRunSkills(run);

  const preparedEnvironment = await prepare();

  const proc = Bun.spawn(invocation.argv, {
      stdin: "pipe",
      stdout: "pipe",
      stderr: "inherit",
      env: harnessEnv(run, preparedEnvironment),
  });

  const recovery = modelResponseReporter(
    async () => await mc(["run", "model-response", run.run_id]) === 0,
    () => log.warn("runner.recovery_unconfirmed", "The connection recovery report failed."),
  );
  let heartbeatTimer: ReturnType<typeof setInterval> | undefined;
  let sessionSeen = false;
  let protocolError = false;
  let registration: Promise<number> | undefined;
  let diagnosis: AdapterFailure | undefined;
  let stopped = false;
  const stop = () => { stopped = true; proc.kill("SIGTERM"); };
  process.on("SIGTERM", stop);
  process.on("SIGINT", stop);

  const onEvent = (line: string) => {
    console.log(line);
    let ev: { event?: string; session_id?: string; native_file?: string };
    try {
      ev = JSON.parse(line) as typeof ev;
    } catch {
      return; // not an event line; ignore
    }
    recovery.observe(ev);
    const failure = readAdapterFailure(ev);
    if (failure) {
      diagnosis ??= failure;
      return;
    }
    if (sessionSeen) return;
    if (ev.event !== "session-start" || typeof ev.session_id !== "string") return;
    let file: string;
    try {
      file = nativeLocator(ev.native_file, invocation.defaultNativeFile);
    } catch (err) {
      protocolError = true;
      log.error("adapter.stream_error", err instanceof Error ? err.message : String(err));
      return;
    }
    sessionSeen = true;
    // Register the session independently of the OpenWiki connection.
    registration = mc([
      "run",
      "register-session",
      run.run_id,
      "--native-ref",
      ev.session_id,
      "--file",
      file,
    ]).catch((err) => {
      log.error("runner.session_lost", `mc run register-session failed to start: ${String(err)}`);
      return 2;
    });
    // The session receives an immediate heartbeat and the normal heartbeat interval.
    void mc(["heartbeat", run.run_id]);
    if (!run.environment || !run.repository || run.role === "scribe") {
      heartbeatTimer = setInterval(() => {
        void mc(["heartbeat", run.run_id]);
      }, run.heartbeat_interval_s * 1000);
    }
  };

  // The agent receives its brief after mc records the process start.
  let startFailed = false;
  const startRegistration = mc(["run", "process-started", run.run_id]).then((result) => {
    if (result !== 0) {
      startFailed = true;
      diagnosis = adapterFailure("The runner could not record the agent process start.", "");
      proc.kill("SIGTERM");
      return;
    }
    proc.stdin.write(run.brief);
    proc.stdin.end();
  });

  const split = makeLineSplitter(onEvent);
  const decoder = new TextDecoder();
  let code: number;
  try {
    for await (const chunk of proc.stdout) {
      split(decoder.decode(chunk, { stream: true }));
    }
    code = await proc.exited;
  } catch (err) {
    proc.kill();
    await proc.exited;
    diagnosis ??= adapterFailure("The adapter event stream failed", err instanceof Error ? err.message : String(err));
    code = 2;
  } finally {
    if (heartbeatTimer !== undefined) clearInterval(heartbeatTimer);
    process.off("SIGTERM", stop);
    process.off("SIGINT", stop);
  }
  await recovery.flush();
  await startRegistration;
  if (startFailed) code = 2;
  // A fast adapter can exit before the session record reaches durable storage.
  if (registration !== undefined) await registration;
  log.info("runner.exited", `harness exited ${code}${sessionSeen ? "" : " (no session-start seen)"}`,
    { exit: code, session_seen: sessionSeen });
  if (!stopped) await reportInfrastructureFailure(run,
    diagnosis?.condition ?? (protocolError ? `adapter protocol failed; harness exited ${code}` : `harness exited ${code}`),
    diagnosis ? `Adapter process exit: ${code}\n${diagnosis.evidence}` : "", diagnosis);
  return protocolError && code === 0 ? 2 : code;
}

async function main(): Promise<number> {
  const run = (await Bun.file(RUN_JSON).json()) as RunEnvelope;
  try {
    return await executeRun(run);
  } catch (err) {
    const condition = `runner execution failed: ${err instanceof Error ? err.message : String(err)}`;
    log.error("adapter.launch_failed", condition);
    await reportInfrastructureFailure(run, condition);
    return 2;
  }
}

if (import.meta.main) {
  process.exit(await main());
}
