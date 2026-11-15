// main.ts — real-world wiring for the resident.
//
// Usage:   bun src/main.ts --config <path-to-json>
// Env:     MC_TICK_INTERVAL_MS overrides the config's tickIntervalMs
//          (default tick_interval_s 60 → 60000 ms).
//
// The config file is ResidentConfig plus:
//   mcPath          — host mc binary (on macOS it runs spine verbs in the
//                     helper container of the deployment)
//   tickIntervalMs  — optional, default 60000
//
// The e2e spawns this process, points it at its test config, and only ever
// observes. The resident runs its first round at start and each later round
// on its timer.

import { projectContainers } from "./project-containers";
import { acquireConnectionOwner } from "./connection-owner";
import { chmod, lstat, mkdir, open, readFile, readdir, realpath, rename, rm, stat, writeFile } from "node:fs/promises";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { makeLogger, parseLevel, type Logger } from "../../runner/shared/log";
import { openFileSink } from "../../runner/shared/log-sink";
import { startTickLoop } from "./tick-loop";
import { createInstallMaintenance, readInstallMaintenance, waitForUpdateActivation } from "./install-maintenance";
import { createRepositoryLane, type StartEvent } from "./repository-lane";
import { createEnvironmentLane } from "./environment-lane";
import { createOperatorLane } from "./operator-lane";
import { createAgentProfileRuntimeRefresh, readAgentProfileRuntimeCatalog } from "./agent-profile-runtime";
import { hostZone } from "./host-zone";
import { discarder, PROJECT_ID } from "./effects";
import type { Exec, ResidentConfig, TickDeps } from "./types";
import {
  CONFIG_SCHEMA_VERSION,
  execMcVia,
  type ResidentControlIdentity,
} from "./resident-control";
import { CollieRuntimeRegistry } from "./collie-runtime";
import { collectPipelineLiveness } from "./pipeline-liveness";
import { AttachmentConfinementHealth } from "./attachment-health";
import { startAttachmentHealthGuard } from "./attachment-health-guard";
import { createAttachmentTerminalChore } from "./attachment-ingress";
import { createRunLogPruneChore } from "./run-log-prune";
import { applyPendingVisions } from "./vision-save";
import { configuredGitEnvironment } from "./git-identity";
import { startDiscordConfiguration } from "./discord-config";
import { startProviderAcceptanceServer } from "./provider-acceptance";
import { startNudgeSocket } from "./nudge-socket";
import { agentsViewClient, agentsViewTokenPath, parseAgentsViewBind, readAgentsViewToken } from "./agentsview";
import { completeTraceCopy, createTraceLane } from "./trace-lane";
import { createTraceMask } from "./trace-mask";
import { createWikiLane } from "./wiki-lane";
import {
  startCredentialProjector,
  type RuntimeBindingSpec,
  type CredentialProjectorHandle,
} from "./credential-projector";
import { createProviderLifetimeCheck, providerLaunchState } from "./provider-lifetime";
import { createProviderCredentials } from "./provider-credentials";

interface MainConfig extends ResidentConfig {
  mcPath: string;
  dockerPath?: string;
  tickIntervalMs?: number;
  backupIntervalMs?: number;
  releaseBuildId: string;
  configSchemaVersion: number;
}

export async function canonicalResidentHome(
  home: string,
  resolve: (path: string) => Promise<string> = realpath,
): Promise<string> {
  if (typeof home !== "string" || !home.startsWith("/")) {
    throw new Error("MC_HOME must be an absolute path");
  }
  const canonical = await resolve(home);
  if (!canonical.startsWith("/")) {
    throw new Error("MC_HOME canonicalization returned a non-absolute path");
  }
  return canonical;
}

export async function canonicalResidentConfig<T extends { mcHome: string }>(
  config: T,
  resolve: (path: string) => Promise<string> = realpath,
): Promise<T> {
  return { ...config, mcHome: await canonicalResidentHome(config.mcHome, resolve) };
}

export function residentControlIdentity(
  config: Pick<MainConfig, "mcHome" | "releaseBuildId" | "configSchemaVersion">,
): ResidentControlIdentity {
  return {
    mcHome: config.mcHome,
    releaseBuildId: config.releaseBuildId,
    configSchemaVersion: config.configSchemaVersion,
  };
}

export async function readWorkspacesViaMc(runMc: Exec): Promise<Array<{ id: string; root: string }>> {
  const result = await runMc(["project", "workspaces"]);
  if (result.exitCode !== 0) throw new Error("The Project catalog is unavailable.");
  const { workspaces } = JSON.parse(result.stdout);
  if (!Array.isArray(workspaces) || !workspaces.every((row) =>
    row && typeof row.id === "string" && PROJECT_ID.test(row.id) && typeof row.root === "string" && row.root.startsWith("/"))) {
    throw new Error("The Project catalog is invalid.");
  }
  return workspaces;
}

export async function prepareAttachmentTreeViaMc(
  runMc: Exec,
  mcHome: string,
  session: string,
): Promise<import("./types").AttachmentTreeReceipt> {
  const result = await runMc(["__attachment-tree-prepare", session]);
  if (result.exitCode !== 0) {
    throw new Error(
      `attachment tree preparation refused (exit ${result.exitCode}): ${result.stderr.trim()}`,
    );
  }
  let receipt: import("./types").AttachmentTreeReceipt;
  try {
    receipt = JSON.parse(result.stdout) as import("./types").AttachmentTreeReceipt;
  } catch (err) {
    throw new Error(`attachment tree preparation returned invalid JSON: ${String(err)}`);
  }
  const root = `${mcHome}/attachments/${session}`;
  if (
    receipt.session !== session ||
    receipt.inbound_published !== `${root}/in/published` ||
    receipt.outbound !== `${root}/out`
  ) {
    throw new Error("attachment tree preparation returned different stable paths");
  }
  return receipt;
}

export async function proveAttachmentConfinement(
  runMc: Exec,
  health: AttachmentConfinementHealth,
): Promise<string> {
  const result = await runMc(["__attachment-canary"]);
  if (result.exitCode !== 0) {
    throw new Error(
      `attachment confinement canary refused (exit ${result.exitCode}): ${result.stderr.trim()}`,
    );
  }
  let receipt: unknown;
  try {
    receipt = JSON.parse(result.stdout);
  } catch (err) {
    throw new Error(`attachment confinement canary returned invalid JSON: ${String(err)}`);
  }
  return health.markHealthy(receipt).generation;
}

export interface BackupChore {
  startup(): Promise<void>;
  beforeTick(): Promise<void>;
}

export function createBackupChore(
  runMc: Exec,
  intervalMs: number,
  now: () => number = Date.now,
): BackupChore {
  if (!Number.isSafeInteger(intervalMs) || intervalMs <= 0) {
    throw new Error(`resident: invalid backup interval ${intervalMs}`);
  }
  let lastCompletedAt: number | undefined;
  const snapshot = async (): Promise<void> => {
    const result = await runMc(["backup"]);
    if (result.exitCode !== 0) {
      throw new Error(`mc backup failed (exit ${result.exitCode}): ${result.stderr.trim()}`);
    }
    let receipt: unknown;
    try {
      receipt = JSON.parse(result.stdout);
    } catch {
      throw new Error("mc backup returned an invalid receipt");
    }
    if (receipt === null || typeof receipt !== "object" || Array.isArray(receipt) ||
        typeof (receipt as Record<string, unknown>).snapshot !== "string" ||
        !(receipt as Record<string, unknown>).snapshot!.toString().startsWith("/") ||
        !Number.isSafeInteger((receipt as Record<string, unknown>).bytes) ||
        ((receipt as Record<string, unknown>).bytes as number) <= 0) {
      throw new Error("mc backup returned an invalid receipt");
    }
    lastCompletedAt = now();
  };
  return {
    startup: snapshot,
    async beforeTick() {
      if (lastCompletedAt === undefined || now() - lastCompletedAt >= intervalMs) {
        await snapshot();
      }
    },
  };
}

export interface TickReceipt {
  version: 1;
  release_build_id: string;
  config_schema_version: number;
  resident_instance_id: string;
  sequence: number;
  previous_completed_at?: string;
  completed_at: string;
}

export async function publishTickReceipt(
  mcHome: string,
  releaseBuildId: string,
  configVersion: number,
  residentInstanceId: string,
  sequence: number,
  now = new Date(),
  previous?: Date,
): Promise<void> {
  const health = join(mcHome, "health");
  await mkdir(health, { recursive: true, mode: 0o700 });
  const final = join(health, "resident-tick.json");
  const stage = join(health, `.resident-tick.${process.pid}.next`);
  const receipt: TickReceipt = {
    version: 1,
    release_build_id: releaseBuildId,
    config_schema_version: configVersion,
    resident_instance_id: residentInstanceId,
    sequence,
    ...(previous === undefined ? {} : { previous_completed_at: previous.toISOString() }),
    completed_at: now.toISOString(),
  };
  await writeFile(stage, JSON.stringify(receipt) + "\n", { mode: 0o600 });
  await rename(stage, final);
}

export async function publishResidentAvailability(home: string, instance: string, canCommit: boolean, now = new Date()): Promise<void> {
  const directory = join(home, "health");
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const stage = join(directory, `.resident-availability.${process.pid}.next`);
  await writeFile(stage, JSON.stringify({ resident_instance_id: instance, observed_at: now.toISOString(), can_commit: canCommit }), { mode: 0o600 });
  await rename(stage, join(directory, "resident-availability.json"));
}

/** Records the time at which the operator lane last worked. */
export async function publishOperatorLaneHealth(home: string, instance: string, now = new Date()): Promise<void> {
  const directory = join(home, "health");
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const stage = join(directory, `.operator-lane.${process.pid}.next`);
  await writeFile(stage, JSON.stringify({ resident_instance_id: instance, observed_at: now.toISOString() }), { mode: 0o600 });
  await rename(stage, join(directory, "operator-lane.json"));
}

export async function refreshConnectionCheck(runMc: Exec, renew: (connection: string) => Promise<{ record: { expires_at_ms?: number } }>, connection: string) {
  const check = crypto.randomUUID().replaceAll("-", "");
  const started = await runMc(["connection", "check-start"], new TextEncoder().encode(JSON.stringify({ connection_id: connection, check_id: check })));
  if (started.exitCode !== 0) throw new Error("The connection status is unavailable.");
  const reservation = JSON.parse(started.stdout);
  if (!reservation.allowed) return;
  let observation: Record<string, unknown>;
  try {
    const snapshot = await renew(connection);
    observation = { outcome: "ready", ...(snapshot.record.expires_at_ms ? { expires_at: new Date(snapshot.record.expires_at_ms).toISOString() } : {}) };
  } catch (error) {
    // The credential service records the failure with its original scope and evidence.
    const cancelled = await runMc(["connection", "check-cancel"], new TextEncoder().encode(JSON.stringify({
      connection_id: connection, check_id: check, authorization_id: reservation.authorization_id, operation: reservation.operation,
    })));
    if (cancelled.exitCode !== 0) throw new Error("The connection check could not close.");
    throw error;
  }
  const result = await runMc(["connection", "observe"], new TextEncoder().encode(JSON.stringify({
    connection_id: connection, check_id: check, authorization_id: reservation.authorization_id,
    event_id: crypto.randomUUID().replaceAll("-", ""), operation: reservation.operation,
    observed_at: new Date().toISOString(), ...observation,
  })));
  if (result.exitCode !== 0) throw new Error("The connection status could not become durable.");
}

export async function refreshConnectionsBeforeTick(runMc: Exec, refreshConnection: (connection: string) => Promise<void>, log: Logger): Promise<void> {
  const status = await runMc(["system", "status"]);
  if (status.exitCode !== 0) return;
  const current = JSON.parse(status.stdout);
  for (const condition of current.conditions ?? []) {
    if ((condition.state === "unknown" || condition.state === "retry_wait") &&
        condition.id === `connection:${condition.connection_id}:token_refresh:`) {
      void refreshConnection(condition.connection_id).catch(() => log.warn("credential.refresh_failed", "The connection refresh could not complete."));
    }
  }
}

export async function observeProviderAccess(runMc: Exec, log: Logger, observation: Record<string, unknown>): Promise<void> {
  const result = await runMc(["connection", "proxy-observe"], new TextEncoder().encode(JSON.stringify(observation)));
  if (result.exitCode !== 0) {
    log.warn("credential.refresh_failed", "The provider request diagnostic could not become durable.");
    throw new Error("The provider request diagnostic could not become durable.");
  }
}

async function startProjector(mcPath: string, mcHome: string, log: Logger,
  catalog: Record<string, RuntimeBindingSpec>, runMc: Exec): Promise<CredentialProjectorHandle> {
  if (!(await Bun.file(join(mcHome, "state", "published-release.json")).exists())) {
    const migration = await runMc(["agent-profile", "credential-migrate"]);
    if (migration.exitCode !== 0) throw new Error("The host credential store requires recovery.");
  }
  const certificate = await runMc(["agent-profile", "proxy-tls"]);
  if (certificate.exitCode !== 0) throw new Error("The host proxy certificate is unavailable.");
  const tls = JSON.parse(certificate.stdout);
  const observe = (observation: Record<string, unknown>) => observeProviderAccess(runMc, log, observation);
  const credentials = createProviderCredentials({
    observe, runMc, acquireOwner: connection => acquireConnectionOwner(mcPath, mcHome, connection),
  });
  const refreshConnection = (connection: string) => refreshConnectionCheck(runMc, credentials.renew, connection);
  const handle = startCredentialProjector({ credentials, tls, refreshConnection, observe,
    isLaunchCurrent: (run, launch, signal) => providerLaunchState(runMc, run, launch, signal) }, catalog);
  try {
    await observe({ request_id: crypto.randomUUID(), operation: "service", origin: "proxy", outcome: "ready",
      provider_sent: false, observed_at: new Date().toISOString(), duration_ms: 0 });
  } catch (error) {
    handle.stop();
    throw error;
  }
  let probing = false;
  let certificateCheckAt = Date.now() + 60 * 60_000;
  const probe = async () => {
    if (probing) return;
    probing = true;
    try {
      if (Date.now() >= certificateCheckAt) {
        try {
          const result = await runMc(["agent-profile", "proxy-tls"]);
          if (result.exitCode !== 0) throw new Error("The proxy certificate is unavailable.");
          handle.replaceTLS(JSON.parse(result.stdout));
          certificateCheckAt = Date.now() + 60 * 60_000;
          await observe({ request_id: crypto.randomUUID(), operation: "service", origin: "proxy", outcome: "ready",
            provider_sent: false, observed_at: new Date().toISOString(), duration_ms: 0 });
        } catch {
          await observe({ request_id: crypto.randomUUID(), operation: "service", origin: "proxy", outcome: "failed",
            provider_sent: false, observed_at: new Date().toISOString(), duration_ms: 0 });
        }
      }
      const listed = await runMc(["agent-profile", "list"]);
      if (listed.exitCode === 0) {
        const connections = JSON.parse(listed.stdout).connections;
        if (Array.isArray(connections)) for (const connection of connections) {
          if (connection?.authorization_id && typeof connection.id === "string") await credentials.read(connection.id).catch(() => {});
        }
      }
    } finally { probing = false; }
  };
  const timer = setInterval(() => void probe().catch(() => {}), 30_000);
  await probe().catch(() => {});
  const stop = handle.stop;
  handle.stop = () => { clearInterval(timer); stop(); };
  return handle;
}

/** Routes one nudge line. A packet decision causes a start request of the
 * repository lane. Each other line wakes the tick. */
export function routeNudge(line: string, tick: () => void, lane: (event: StartEvent) => void): void {
  if (line === "packet-decide") lane("packet-decision");
  else tick();
}

/** One artifact expiry. The expiry blocks repository operations of its
 * Project, so its end causes a start request. */
export async function expireArtifacts(runMc: Exec, log: Logger, afterExpiry: () => void): Promise<void> {
  try {
    const expiry = await runMc(["__prune-artifacts"]);
    if (expiry.exitCode !== 0) log.warn("artifacts.expiry_failed", expiry.stderr.trim());
  } catch (error) {
    log.warn("artifacts.expiry_failed", `Task artifact expiry failed: ${String(error)}`);
  } finally {
    afterExpiry();
  }
}

/** Runs one long-lived command and delivers each line of its standard
 * output. `done` resolves with the exit code. */
export function followLines(
  command: string[], onLine: (line: string) => void,
): { stop(): void; done: Promise<number> } {
  const proc = Bun.spawn(command, { stdin: "ignore", stdout: "pipe", stderr: "ignore" });
  const done = (async () => {
    const decoder = new TextDecoder();
    let buffered = "";
    for await (const chunk of proc.stdout) {
      buffered += decoder.decode(chunk, { stream: true });
      let newline = buffered.indexOf("\n");
      while (newline >= 0) {
        onLine(buffered.slice(0, newline));
        buffered = buffered.slice(newline + 1);
        newline = buffered.indexOf("\n");
      }
    }
    if (buffered !== "") onLine(buffered);
    return proc.exited;
  })();
  return {
    stop() {
      try {
        proc.kill("SIGTERM");
      } catch {
        // An exited follower needs no stop.
      }
    },
    done,
  };
}

function execVia(binary: string): Exec {
  return async (argv, input, options) => {
    const proc = Bun.spawn([binary, ...argv], {
      stdin: input ?? "ignore",
      stdout: "pipe",
      stderr: "pipe",
      ...(options?.signal === undefined ? {} : { signal: options.signal }),
    });
    const [stdout, stderr, exitCode] = await Promise.all([
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
      proc.exited,
    ]);
    return { exitCode, stdout, stderr };
  };
}

export async function checkUpdateListeners(home: string): Promise<void> {
  const paths = [join(home, "resident.socket"), join(home, "discord.socket"), join(home, "health", "provider-acceptance.sock")];
  await mkdir(join(home, "health"), { recursive: true, mode: 0o700 });
  for (const path of paths) {
    try {
      const existing = await lstat(path);
      if (!existing.isSocket() || existing.uid !== process.getuid?.()) throw new Error("A resident socket path has an unsafe file.");
      await rm(path);
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    const listener = Bun.listen({ unix: path, socket: { open(socket) { socket.end(); }, data(socket) { socket.end(); } } });
    listener.stop(true);
    await rm(path, { force: true });
  }
  const server = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch: () => new Response(null, { status: 503 }) });
  server.stop(true);
}

export async function verifyResidentUpdate(
  config: Pick<MainConfig, "mcHome" | "runtimeBindingAuthorities">,
  runMc: Exec,
  listeners: (home: string) => Promise<void> = checkUpdateListeners,
): Promise<void> {
  const result = await runMc(["doctor"]);
  if (result.exitCode !== 0) throw new Error("The update cannot verify the resident runtime.");
  const report = JSON.parse(result.stdout);
  for (const check of ["mc-home", "spine", "deployment-identity", "container-runtime"]) {
    if (!Array.isArray(report.findings) || report.findings.filter((finding: any) => finding?.check === check && finding.status === "ok").length !== 1) {
      throw new Error(`The resident update check failed: ${check}.`);
    }
  }
  await readAgentProfileRuntimeCatalog(runMc, config.runtimeBindingAuthorities);
  await listeners(config.mcHome);
}

async function main(): Promise<void> {
  const idx = process.argv.indexOf("--config");
  const configPath = idx >= 0 ? process.argv[idx + 1] : undefined;
  if (!configPath) {
    console.error("usage: bun src/main.ts --config <path>");
    process.exit(2);
  }
  const config = await canonicalResidentConfig(
    (await Bun.file(configPath).json()) as MainConfig,
  );

  if (config.configSchemaVersion !== CONFIG_SCHEMA_VERSION ||
      typeof config.releaseBuildId !== "string" ||
      !/^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$/.test(config.releaseBuildId)) {
    console.error("resident: config release/schema identity is missing or unsupported");
    process.exit(2);
  }
  try {
    configuredGitEnvironment(config.gitAuthorName, config.gitAuthorEmail);
  } catch (err) {
    console.error(`resident: ${err instanceof Error ? err.message : String(err)}`);
    process.exit(2);
  }

  // A malformed adapter allowlist must never silently degrade to "launch
  // anything" or crash mid-tick: reject it up front, fail-closed.
  if (config.agentRunnerRoutes !== undefined &&
      (!Array.isArray(config.agentRunnerRoutes) ||
        !config.agentRunnerRoutes.every((r) => typeof r === "string" && /^[^/]+\/[^/]+$/.test(r)))) {
    console.error("resident: agentRunnerRoutes must be an array of harness/model_binding strings");
    process.exit(2);
  }
  if (config.dockerPath !== undefined &&
      (typeof config.dockerPath !== "string" || !config.dockerPath.startsWith("/"))) {
    console.error("resident: dockerPath must be an absolute path");
    process.exit(2);
  }
  if (config.workspaceRoots !== undefined &&
      (!Array.isArray(config.workspaceRoots) ||
        !config.workspaceRoots.every((w) => w !== null && typeof w === "object" &&
          PROJECT_ID.test(w.id) &&
          typeof w.root === "string" && w.root.startsWith("/")))) {
    console.error("resident: workspaceRoots must be a valid Project catalog");
    process.exit(2);
  }
  if (config.agentsViewBind !== undefined) {
    try {
      parseAgentsViewBind(config.agentsViewBind);
    } catch (err) {
      console.error(`resident: ${err instanceof Error ? err.message : String(err)}`);
      process.exit(2);
    }
  }

  const envInterval = process.env["MC_TICK_INTERVAL_MS"];
  const intervalMs = envInterval
    ? Number.parseInt(envInterval, 10)
    : (config.tickIntervalMs ?? 60_000);
  if (!Number.isInteger(intervalMs) || intervalMs <= 0) {
    console.error(`resident: invalid tick interval ${JSON.stringify(envInterval)}`);
    process.exit(2);
  }
  const verificationInstance = crypto.randomUUID();
  let verificationSequence = 0;
  let verificationCompleted: Date | undefined;
  await waitForUpdateActivation(config.mcHome, config.releaseBuildId, async () => {
    const now = new Date();
    await publishTickReceipt(config.mcHome, config.releaseBuildId, config.configSchemaVersion,
      verificationInstance, ++verificationSequence, now, verificationCompleted);
    await publishResidentAvailability(config.mcHome, verificationInstance, false, now);
    verificationCompleted = now;
  }, undefined, () => verifyResidentUpdate(config, execVia(config.mcPath)));
  // The structured channel. It is a file this process OPENS AND OWNS, not the
  // launchd redirect: supervision.go pins StandardErrorPath and launchd holds
  // that descriptor for the process lifetime, so a rotation there would leave
  // launchd appending into an unlinked inode. The two channels divide by
  // failure mode — everything the logger can reach goes here, and the launchd
  // stderr file keeps only what the logger cannot: the boot validation above,
  // uncaught exceptions, and runtime aborts.
  //
  // Level is env-only, beside the tick interval and for the same reason: a
  // deployment knob nobody should have to re-onboard to turn. An unreadable
  // value resolves to `info` rather than failing the boot — refusing to start
  // over a log setting trades a verbose log for no system at all.
  //
  // The directory is created HERE rather than left to onboarding. The sink
  // treats a missing directory as a state and writes nothing — correct inside a
  // container that was never given one, and silently wrong for the process that
  // OWNS this home: a resident started against a home whose logs/ does not yet
  // exist would run for days recording nothing, anywhere. 0700 is the mode
  // supervision already gives it, and the per-run log leaves under it are only
  // reachable through their own binds.
  const logsDir = join(config.mcHome, "logs");
  mkdirSync(logsDir, { recursive: true, mode: 0o700 });
  const log = makeLogger({
    comp: "resident",
    level: parseLevel(process.env["MC_LOG_LEVEL"]),
    sink: openFileSink({ dir: logsDir, name: "resident.ndjson" }),
  });
  const rawDocker = execVia(config.dockerPath ?? "docker");
  const docker: Exec = projectContainers(rawDocker, (...args) => runMc(...args));
  const collieRuntime = new CollieRuntimeRegistry();
	const runMc = execMcVia(
		[config.mcPath],
		residentControlIdentity(config),
		docker,
		collieRuntime,
		() => collectPipelineLiveness({
			docker,
			runMc,
			mcHome: config.mcHome,
			now: () => Date.now(),
			stat: async (path) => ({ mtimeMs: (await stat(path)).mtimeMs }),
			readFile: (path) => readFile(path, "utf8"),
			log: (msg) => log.warn("liveness.probe_failed", msg),
		}),
	);
  const attachmentConfinement = new AttachmentConfinementHealth();
  let attachmentGeneration: string;
  try {
    attachmentGeneration = await proveAttachmentConfinement(runMc, attachmentConfinement);
    log.info("boot.canary_passed", "attachment confinement canary passed for this resident boot");
  } catch (err) {
    console.error(
      `resident: attachment confinement unhealthy: ${
        err instanceof Error ? err.message : String(err)
      }`,
    );
    process.exit(2);
  }
  const attachmentHealthGuard = await (async () => {
    try {
      const guard = await startAttachmentHealthGuard(
        [config.mcPath], config.mcHome, attachmentGeneration,
      );
      log.info("boot.guard_started", "attachment confinement health guard live for this resident boot");
      return guard;
    } catch (err) {
      console.error(
        `resident: attachment health guard unavailable: ${
          err instanceof Error ? err.message : String(err)
        }`,
      );
      process.exit(2);
    }
  })();
  const backupIntervalMs = config.backupIntervalMs ?? 3_600_000;
  let backupChore: BackupChore;
  try {
    backupChore = createBackupChore(runMc, backupIntervalMs);
    await backupChore.startup();
    log.info("boot.snapshot_committed", "startup spine snapshot committed");
  } catch (err) {
    console.error(`resident: startup backup failed: ${err instanceof Error ? err.message : String(err)}`);
    process.exit(2);
  }
  // The other half of the /mc/logs bind. Reap deliberately does NOT remove a
  // run's log directory — deleting a failed run's account of itself at reap
  // makes it worthless for exactly the runs an operator comes looking for — so
  // this is what bounds the tree. Best-effort by construction: it cannot
  // throw, so it can never cost the tick its dispatch.
  const runLogPruneChore = createRunLogPruneChore({
    mcHome: config.mcHome,
    runMc,
    docker,
    readdir: (path) => readdir(path),
    discard: (path) =>
      discarder({
        fs: { rm: (p, opts) => rm(p, { force: true, recursive: opts?.recursive ?? false }) },
        sleep: (ms) => Bun.sleep(ms),
        log,
      }, "run log prune")(path, { recursive: true }),
    log,
  });
  const attachmentTerminalChore = createAttachmentTerminalChore(
    runMc, attachmentConfinement,
  );
  try {
    await attachmentTerminalChore.startup();
    log.info("boot.started", "startup attachment hard-expiry sweep completed");
  } catch (err) {
    console.error(
      `resident: startup attachment terminal sweep failed: ${
        err instanceof Error ? err.message : String(err)
      }`,
    );
    process.exit(2);
  }

  let runtimeCatalog: Record<string, RuntimeBindingSpec>;
  try {
    runtimeCatalog = await readAgentProfileRuntimeCatalog(runMc, config.runtimeBindingAuthorities);
  } catch (err) {
    console.error(`resident: agent profile configuration is unavailable: ${err instanceof Error ? err.message : String(err)}`);
    process.exit(2);
  }
  let captureRecoveryActive = false;
  const recoverEvaluationCases = async () => {
    if (captureRecoveryActive) return;
    captureRecoveryActive = true;
    try {
      const result = await runMc(["evaluation", "recover"]);
      if (result.exitCode !== 0) log.warn("evaluation.capture_failed", "The evaluation capture requires a retry.");
    } catch { log.warn("evaluation.capture_failed", "The evaluation capture requires a retry."); }
    finally { captureRecoveryActive = false; }
  };
  setInterval(() => void recoverEvaluationCases(), 60_000);
  void recoverEvaluationCases();
  const residentInstanceId = crypto.randomUUID();
  const writeAvailability = (canCommit: boolean) => publishResidentAvailability(config.mcHome, residentInstanceId, canCommit);
  await writeAvailability(false).catch(() => log.warn("tick.failed", "The resident cannot publish its availability."));
  let availabilityActive = false;
  const publishAvailability = async () => {
    if (availabilityActive) return;
    availabilityActive = true;
    try {
      let canCommit = false;
      try { const result = await runMc(["system", "probe"]); canCommit = result.exitCode === 0; } catch {}
      await writeAvailability(canCommit);
    } catch { log.warn("tick.failed", "The resident cannot publish its availability."); }
    finally { availabilityActive = false; }
  };
  setInterval(() => void publishAvailability(), 10_000);
  void publishAvailability();
  const credentialHandle = await startProjector(config.mcPath, config.mcHome, log, runtimeCatalog, runMc);
  await credentialHandle.ready;
  const checkProviderLifetimes = createProviderLifetimeCheck(credentialHandle, runMc);
  const providerLifetimeTimer = setInterval(() => void checkProviderLifetimes(), 1000);
  const refreshAgentProfiles = createAgentProfileRuntimeRefresh(credentialHandle, {
    runMc, authorities: config.runtimeBindingAuthorities,
  });
  log.info("boot.projector_live", "The host provider proxy is available.");
  const providerAcceptance = await startProviderAcceptanceServer(config.mcHome, config.releaseBuildId, {
    authorize: async request => {
      await refreshAgentProfiles();
      const projection = credentialHandle.authorizeConnection(request.credential, request.profile, {
        runId: request.run_id, launchId: request.launch_id, expiresAt: request.expires_at_ms,
        model: request.model, profileRevision: request.profile_revision,
      }, request.max_requests, request.purpose);
      return JSON.parse(projection.proxyConfig!);
    },
    revoke: (run, launch) => credentialHandle.projector.revoke(run, launch),
    revokeConnection: connection => credentialHandle.revokeConnection(connection),
    status: (run, launch) => credentialHandle.status(run, launch),
    check: connection => credentialHandle.refreshConnection(connection),
  }, log);
  log.info("credential.acceptance_armed", "The provider acceptance control socket is available.");

  let tickSequence = 0;
  let previousTickCompletedAt: Date | undefined;
  const fs: TickDeps["fs"] = {
    mkdir: async (path, opts) => {
      await mkdir(path, {
        recursive: opts?.exclusive !== true,
        ...(opts?.mode === undefined ? {} : { mode: opts.mode }),
      });
      // node's mkdir applies the process umask to `mode`, and the resident's
      // is 022 — so a requested 0777 lands as 0755 and the sealed-run uid
      // cannot write. chmod is not masked, so state the mode twice.
      if (opts?.mode !== undefined) await chmod(path, opts.mode);
    },
    writeFile: (path, data, opts) =>
      opts?.mode !== undefined ? writeFile(path, data, { mode: opts.mode }) : writeFile(path, data),
    rename,
    readdir: (path) => readdir(path),
    readFile: (path) => readFile(path, "utf8"),
    rm: (path, opts) => rm(path, { force: true, recursive: opts?.recursive ?? false }),
  };
  let shutdown: (reason: string, code?: number) => void = (_reason, code = 0) => process.exit(code);
  let environmentOperations = () => 0;
  let operatorOperations = () => 0;
  const maintenance = createInstallMaintenance(config.mcHome, config.releaseBuildId);
  const lane = createRepositoryLane({
    runMc,
    docker,
    log,
    fs,
    config,
    sleep: (ms) => Bun.sleep(ms),
    schedule: (fn, ms) => setTimeout(fn, ms),
    cancel: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
    follow: (argv, onLine) => followLines([config.dockerPath ?? "docker", ...argv], onLine),
    now: () => performance.now(),
    maintenance: (active) => maintenance(active + environmentOperations() + operatorOperations()),
    exit: (code) => shutdown("owner-refusal", code),
  });
  const schedule = (fn: () => void, ms: number) => setTimeout(fn, ms);
  const cancel = (handle: unknown) => clearTimeout(handle as ReturnType<typeof setTimeout>);
  const traceLane = createTraceLane({
    credentials: () => credentialHandle.traceSecrets(),
    runMc,
    log,
    mcHome: config.mcHome,
    ...(config.agentsViewBind === undefined ? {} : {
      agentsView: agentsViewClient(config.agentsViewBind, {
        token: () => readAgentsViewToken(agentsViewTokenPath(config.mcHome)),
      }),
    }),
    schedule,
    cancel,
    now: () => Date.now(),
  });
  const wikiLane = createWikiLane({ runMc, log, image: config.image, schedule, cancel });
  const deps: TickDeps = {
    intervalMs,
    setTimer: (fn, ms) => setInterval(fn, ms),
    clearTimer: (h) => clearInterval(h as ReturnType<typeof setInterval>),
		runMc,
    docker,
    sleep: (ms) => Bun.sleep(ms),
    hostZone: () => hostZone(),
    log,
    maintenance: () => lane.pauseCheck(),
    afterTick: () => lane.request("tick"),
    collieStarted: (containerId) => lane.followCollie(containerId),
    beforeTick: async () => {
      environmentLane.assertPrepared();
      await applyPendingVisions(runMc, log);
      await refreshAgentProfiles();
      await backupChore.beforeTick();
      await refreshConnectionsBeforeTick(runMc, credentialHandle.refreshConnection, log);
      await attachmentTerminalChore.beforeTick();
      await runLogPruneChore.beforeTick();
      await expireArtifacts(runMc, log, () => lane.request("expiry-end"));
    },
    tickComplete: async () => {
      const completedAt = new Date();
      const nextSequence = tickSequence + 1;
      await publishTickReceipt(
        config.mcHome, config.releaseBuildId, config.configSchemaVersion,
        residentInstanceId, nextSequence, completedAt, previousTickCompletedAt,
      );
      tickSequence = nextSequence;
      previousTickCompletedAt = completedAt;
    },
    fs,
    attachmentConfinement,
    readWorkspaces: () => readWorkspacesViaMc(runMc),
    prepareAttachmentTree: (session) =>
      prepareAttachmentTreeViaMc(runMc, config.mcHome, session),
    credentials: credentialHandle.projector,
    collieRuntime,
    config,
  };

  const environmentLane = createEnvironmentLane(deps, {
    instance: residentInstanceId,
    maintenance: async () => (await readInstallMaintenance(config.mcHome)) !== null,
    history: {
      completeTraceCopy: (operationID) => completeTraceCopy(traceLane, config.mcHome, operationID),
      mask: (text) => createTraceMask(credentialHandle.traceSecrets()).apply(Buffer.from(text)).toString(),
    },
  });
  environmentOperations = environmentLane.activeCount;
  await environmentLane.prepare();
  const operatorLane = createOperatorLane(runMc, log, 1000, async () => (await readInstallMaintenance(config.mcHome)) !== null,
    { publish: (now) => publishOperatorLaneHealth(config.mcHome, residentInstanceId, now) });
  operatorOperations = operatorLane.activeCount;
  const handle = startTickLoop(deps);
  let nudgeSocket: Awaited<ReturnType<typeof startNudgeSocket>>;
  try {
    nudgeSocket = await startNudgeSocket(config.mcHome,
      (line) => routeNudge(line, () => handle.nudge(), (event) => lane.request(event)), log);
  } catch (err) {
    console.error(`resident: nudge socket refused: ${err instanceof Error ? err.message : String(err)}`);
    await handle.stop();
    await lane.stop();
    clearInterval(providerLifetimeTimer);
    credentialHandle.stop();
    await Promise.all([providerAcceptance.stop(), credentialHandle.drain()]);
    process.exit(2);
  }
  const discord = await startDiscordConfiguration(config.mcHome, runMc, log, () => handle.nudge());

  let shuttingDown = false;
  shutdown = (reason: string, code = 0) => {
    if (shuttingDown) return;
    shuttingDown = true;
    deps.log.info("boot.stopping",
      `${reason}: stopping after any in-flight tick, repository operation, trace copy, and wiki export`, { signal: reason });
    clearInterval(providerLifetimeTimer);
    credentialHandle.stop();
    const credentialDrain = credentialHandle.drain();
    const services = discord.stop().then(() => nudgeSocket.stop())
      .then(() => providerAcceptance.stop())
      .then(() => handle.stop())
      .then(() => Promise.all([lane.stop(), traceLane.stop(), wikiLane.stop(), environmentLane.stop(), operatorLane.stop()]))
      .then(() => attachmentHealthGuard.stop());
    void Promise.allSettled([credentialDrain, services]).then(results => {
      if (results[0]?.status === "rejected") log.error("credential.refresh_failed", "The host could not save renewed credentials before shutdown.");
      if (results[1]?.status === "rejected") log.error("boot.stop_failed", "The resident could not stop all services.");
      process.exit(results.some(result => result.status === "rejected") ? 2 : code);
    });
  };
  process.on("SIGINT", () => shutdown("SIGINT"));
  process.on("SIGTERM", () => shutdown("SIGTERM"));
  void lane.start().catch((err) => {
    log.error("repository.start_failed", `the repository lane did not start: ${err instanceof Error ? err.message : String(err)}`);
  });
  traceLane.start();
  wikiLane.start();
  environmentLane.start();
  await operatorLane.start();
  deps.log.info("boot.started",
    `started: tick every ${intervalMs} ms, nudges on ${nudgeSocket.path}, mc=${config.mcPath}`,
    { interval_ms: intervalMs });
  // The first round runs at start. The release activation waits for its receipt.
  handle.nudge();
}

if (import.meta.main) {
  void main();
}
