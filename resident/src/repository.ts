// repository.ts — the host work of one repository operation. The lane
// (repository-lane.ts) starts each execution that `mc delivery start` lists.
// This module runs one execution to its record or to its failure report.

import type { TickDeps } from "./types";
import { CULLER_RUNTIME, runtimeArguments, projectCacheArguments, projectSecretValues } from "./environment-lane";
import {
  confirmRunContainersAbsent, pipelineContainerName, PRODUCER_ABSENCE_ATTEMPTS, PROJECT_ID, SAFE_ID,
} from "./pipeline-liveness";

/** The subject of one repository operation: one task or one wiki update.
 * `wiki_update` is the run identifier of the wiki update run. */
export type RepositorySubject = { task_id: number } | { wiki_update: string };

/** The subject fields of a plan, an execution, or a result. */
export function subjectFields(x: { task_id?: unknown; wiki_update?: unknown }): RepositorySubject {
  if (x !== null && typeof x === "object") {
    const { task_id: task, wiki_update: wiki } = x;
    if (wiki === undefined && typeof task === "number" && Number.isSafeInteger(task) && task >= 1) {
      return { task_id: task };
    }
    if (task === undefined && typeof wiki === "string" && SAFE_ID.test(wiki)) {
      return { wiki_update: wiki };
    }
  }
  throw new Error("The repository subject is invalid.");
}

export interface RepositoryPlan {
  task_id?: number;
  wiki_update?: string;
  workspace_root: string;
  input_commit: string;
  destination_branch: string;
  version_id?: number;
  max_temporary_bytes: number;
}

/** The host dependencies of one repository operation. */
export type RepositoryDeps = Pick<TickDeps, "runMc" | "docker" | "log" | "fs" | "config" | "sleep">;

export type RepositoryOperationName =
  "capture" | "cleanup" | "observe" | "prepare" | "apply" | "reconcile";

/** One active execution as a start response lists it: the stored effect of
 * the execution, its remaining deadline, and the remaining container grace of
 * the target run. */
export interface StartedExecution {
  run_id: string;
  operation: RepositoryOperationName;
  task_id?: number;
  wiki_update?: string;
  project: string;
  workspace_root: string;
  destination_branch: string;
  input_commit?: string;
  start_commit?: string;
  submitted_commit?: string;
  result_commit?: string;
  submission_kind?: string;
  version_id?: number;
  application?: unknown;
  previous_run?: string;
  producer_run?: string;
  max_temporary_bytes?: number;
  deadline_ms: number;
  container_grace_ms?: number;
}

export interface ExecutionContext {
  /** A stop request or the deadline aborts it. */
  signal: AbortSignal;
  /** A batched heartbeat under the lane lock. True only when mc renewed
   * this execution. It throws when each attempt of the heartbeat fails. */
  checkBeforeContainer(): Promise<boolean>;
  /** The container grace of the target run, minus the elapsed time. */
  graceRemainingMs(): number;
}

interface RepositoryResult {
  task_id?: number;
  wiki_update?: string;
  input_commit: string;
  destination_commit: string;
  candidate?: unknown;
  applied?: unknown;
  [key: string]: unknown;
}

/** A failure report sends at most this number of calls, with this wait
 * between two calls. */
export const REPORT_ATTEMPTS = 3;
export const REPORT_RETRY_MS = 5_000;

class ExecutionStopped extends Error {
  constructor() { super("The repository execution received a stop request."); }
}

/** Each attempt of a failure report failed. The execution then ends through
 * the heartbeat limit of mc. */
class FailureReportFailed extends Error {}

let allocations: Promise<unknown> = Promise.resolve();

/** One allocation runs at a time in the resident, so two size checks of the
 * temporary repositories cannot pass together. */
function withAllocationLock<T>(fn: () => Promise<T>): Promise<T> {
  const allocation = allocations.then(fn);
  allocations = allocation.then(() => undefined, () => undefined);
  return allocation;
}

/** The host root of the temporary repository of one run. The temporary
 * repositories of wiki updates have their own folder, so each folder has its
 * own temporary disk limit. */
export function repositoryRunRoot(
  deps: { config: { mcHome: string } }, run: string, subject: RepositorySubject,
): string {
  if (!SAFE_ID.test(run)) throw new Error("The repository run identifier is invalid.");
  const folder = "wiki_update" in subject ? "wiki-repositories" : "repositories";
  return `${deps.config.mcHome}/runs/${folder}/${run}`;
}

async function native(
  envelope: Record<string, unknown>, deps: RepositoryDeps, key: string, signal?: AbortSignal,
): Promise<RepositoryResult> {
  const path = `${deps.config.mcHome}/runs/${key}.repository.json`;
  await deps.fs.mkdir(`${deps.config.mcHome}/runs`, { mode: 0o700 });
  await deps.fs.writeFile(path, JSON.stringify(envelope) + "\n", { mode: 0o600 });
  const result = await deps.runMc(["__repository", path], undefined, signal === undefined ? undefined : { signal });
  if (signal?.aborted) throw new ExecutionStopped();
  if (result.exitCode !== 0) throw new Error(result.stderr.trim() || "The repository operation failed.");
  return JSON.parse(result.stdout) as RepositoryResult;
}

async function record(operation: string, run: string, result: RepositoryResult, deps: RepositoryDeps): Promise<void> {
  const recorded = await deps.runMc(["delivery", "record", "--operation", operation, "--run", run, "--result", "-"], new TextEncoder().encode(JSON.stringify(result)));
  if (recorded.exitCode !== 0) throw new Error(recorded.stderr.trim() || "The repository result record failed.");
}

async function reportFailure(run: string, condition: string, deps: RepositoryDeps, containerPresent = false): Promise<void> {
  let failure = "";
  for (let attempt = 1; attempt <= REPORT_ATTEMPTS; attempt += 1) {
    if (attempt > 1) await deps.sleep(REPORT_RETRY_MS);
    try {
      const reported = await deps.runMc(["delivery", "fail", "--run", run, "--condition", condition,
        ...(containerPresent ? ["--container-present"] : [])]);
      if (reported.exitCode === 0) return;
      failure = reported.stderr.trim() || `exit ${reported.exitCode}`;
    } catch (error) {
      failure = error instanceof Error ? error.message : String(error);
    }
  }
  throw new FailureReportFailed(`The failure report of repository execution ${run} failed: ${failure}`);
}

export async function prepareRepositoryRun(plan: RepositoryPlan, run: string, deps: RepositoryDeps): Promise<RepositoryResult> {
  const subject = subjectFields(plan);
  const envelope = {
    operation: "allocate", ...subject, run_id: run, wiki_only: plan.wiki_update !== undefined,
    workspace_root: plan.workspace_root, run_root: repositoryRunRoot(deps, run, subject),
    input_commit: plan.input_commit, destination_branch: plan.destination_branch,
    version_id: plan.version_id,
    max_temporary_bytes: plan.max_temporary_bytes,
  };
  const allocated = await withAllocationLock(() => native(envelope, deps, run));
  await record("ready", run, allocated, deps);
  const result = await native({ ...envelope, operation: "ready", input_commit: allocated.input_commit,
    destination_commit: allocated.destination_commit }, deps, run);
  await record("ready", run, result, deps);
  return result;
}

/** Stops and removes both containers of a run. Returns each failure that
 * Docker reports for a present container. */
export async function removeRunContainers(project: string, run: string, deps: Pick<RepositoryDeps, "docker">): Promise<string[]> {
  const failures: string[] = [];
  for (const name of [pipelineContainerName(project, run), `mc-setup-${run}`]) {
    try {
      await deps.docker(["stop", name]);
      const removed = await deps.docker(["rm", "-f", name]);
      if (removed.exitCode !== 0 && !/No such container/i.test(removed.stderr)) {
        failures.push(`docker rm ${name} exited ${removed.exitCode}: ${removed.stderr.trim()}`);
      }
    } catch (error) {
      failures.push(`docker could not remove ${name}: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  return failures;
}

/** Runs one execution to its record, or reports its failure through
 * `mc delivery fail`. A stopped execution reports nothing, because the end
 * of the execution caused the stop. */
export async function runExecution(execution: StartedExecution, deps: RepositoryDeps, ctx: ExecutionContext): Promise<void> {
  const run = execution.run_id;
  if (!SAFE_ID.test(run) || typeof execution.project !== "string" || !PROJECT_ID.test(execution.project)) {
    throw new Error(`The repository execution ${JSON.stringify(run)} has an invalid identity.`);
  }
  try {
    await executionFlow(execution, deps, ctx);
  } catch (error) {
    if (ctx.signal.aborted || error instanceof ExecutionStopped) return;
    if (error instanceof FailureReportFailed) throw error;
    const condition = error instanceof Error ? error.message : String(error);
    await reportFailure(run, condition, deps);
    throw error;
  } finally {
    for (const suffix of [".repository.json", ".merge.json"]) {
      const path = `${deps.config.mcHome}/runs/${run}${suffix}`;
      try {
        await deps.fs.rm(path);
      } catch (error) {
        deps.log.warn("cleanup.failed", `repository execution ${run}: could not remove ${path}: ${String(error)}`);
      }
    }
  }
}

async function executionFlow(execution: StartedExecution, deps: RepositoryDeps, ctx: ExecutionContext): Promise<void> {
  const run = execution.run_id;
  const checkStop = () => { if (ctx.signal.aborted) throw new ExecutionStopped(); };
  // The native executor names the temporary repository by the run that owns
  // it: the producer run of a capture or a cleanup, and the apply run of
  // receipt recovery.
  const owner = execution.operation === "reconcile" ? execution.previous_run ?? ""
    : execution.operation === "capture" || execution.operation === "cleanup" ? execution.producer_run ?? ""
    : run;
  const subject = subjectFields(execution);
  const envelope: Record<string, unknown> = {
    operation: execution.operation, ...subject, run_id: owner,
    workspace_root: execution.workspace_root, run_root: repositoryRunRoot(deps, owner, subject),
    input_commit: execution.input_commit || execution.submitted_commit || "",
    start_commit: execution.start_commit,
    destination_branch: execution.destination_branch, result_commit: execution.result_commit,
    submission_kind: execution.submission_kind,
    application: execution.application, version_id: execution.version_id,
    max_temporary_bytes: execution.max_temporary_bytes,
    author_name: deps.config.gitAuthorName, author_email: deps.config.gitAuthorEmail,
  };
  const runNative = (body: Record<string, unknown>) =>
    native({ ...body, deadline_ms: execution.deadline_ms }, deps, run, ctx.signal);

  switch (execution.operation) {
    case "capture":
    case "cleanup": {
      if (!await targetContainersAbsent(execution, owner, deps, ctx)) return;
      checkStop();
      // An evaluation capture belongs to a task.
      if (execution.operation === "cleanup" && "task_id" in subject) {
        const capture = await deps.runMc(["evaluation", "finish"], new TextEncoder().encode(JSON.stringify({ run_id: owner })));
        const result = capture.exitCode === 0 ? JSON.parse(capture.stdout || "{}") : null;
        if (capture.exitCode !== 0 || result?.status === "pending" && !result.cleanup_ready) {
          await record("cleanup", run, { ...subject, input_commit: execution.input_commit ?? "",
            destination_commit: "", evaluation_pending: true, cleanup_error: capture.stderr.trim() || "The evaluation capture requires its original source.",
            temporary_bytes: 0 }, deps);
          return;
        }
      }
      await record(execution.operation, run, await runNative(envelope), deps);
      return;
    }
    case "observe":
      await record(execution.operation, run, await runNative(envelope), deps);
      return;
    case "reconcile":
      await requireMergeContainerAbsent(execution.project, owner, deps);
      checkStop();
      await sourceEnvironmentAssociation(execution, "source-accepting", deps);
      const recovered = await runNative(envelope);
      if (recovered.applied) await acceptSourceEnvironment(execution, recovered, deps);
      else if (recovered.status === "not-applied") await sourceEnvironmentAssociation(execution, "source-aborted", deps);
      await record("reconcile", run, recovered, deps);
      return;
    case "prepare": {
      if (execution.previous_run) {
        await requireMergeContainerAbsent(execution.project, execution.previous_run, deps);
        checkStop();
      }
      const allocated = await withAllocationLock(() => runNative({ ...envelope, operation: "allocate" }));
      checkStop();
      await record("ready", run, allocated, deps);
      checkStop();
      const prepared = await runNative({ ...envelope, operation: "prepare-repository",
        input_commit: allocated.input_commit, destination_commit: allocated.destination_commit });
      checkStop();
      await record("ready", run, prepared, deps);
      checkStop();
      envelope.destination_commit = prepared.destination_commit;
      envelope.input_commit = prepared.input_commit;
      const facts = await mergeContainer(execution, envelope, deps, ctx);
      if (facts === undefined) return;
      await checkCandidateEnvironment(execution, facts, deps, ctx);
      checkStop();
      const retained = await runNative({ ...envelope, operation: "retain-candidate", candidate: facts.candidate });
      checkStop();
      await record("prepare", run, retained, deps);
      return;
    }
    case "apply": {
      await sourceEnvironmentAssociation(execution, "source-accepting", deps);
      const facts = await mergeContainer(execution, envelope, deps, ctx);
      if (facts === undefined) return;
      // An exact apply receipt remains a fact after a stop request.
      if (facts.applied === undefined) checkStop();
      await acceptSourceEnvironment(execution, facts, deps);
      if (facts.status === "destination-changed") await sourceEnvironmentAssociation(execution, "source-aborted", deps);
      await record("apply", run, facts, deps);
      return;
    }
    default:
      throw new Error(`The repository operation ${JSON.stringify(execution.operation)} is unknown.`);
  }
}

async function sourceEnvironmentAssociation(execution: StartedExecution, action: "source-accepting" | "source-aborted", deps: RepositoryDeps): Promise<void> {
  if (!deps.config.runtimeVolume || execution.wiki_update || execution.destination_branch !== "main") return;
  const application = execution.application as { candidate?: { submitted_commit?: string; commit?: string } } | undefined;
  const candidate = application?.candidate;
  const result = await deps.runMc(["environment", action], new TextEncoder().encode(JSON.stringify({
    project_id: execution.project,
    candidate_source: candidate?.submitted_commit ?? execution.submitted_commit ?? execution.input_commit,
    ...(candidate?.commit ? { source_commit: candidate.commit } : {}),
  })));
  if (result.exitCode !== 0) throw new Error("The source environment association requires recovery.");
}

async function acceptSourceEnvironment(execution: StartedExecution, facts: RepositoryResult, deps: RepositoryDeps): Promise<void> {
  if (!deps.config.runtimeVolume || !facts.applied || execution.wiki_update || execution.destination_branch !== "main") return;
  const applied = facts.applied as { commit?: string };
  const application = execution.application as { candidate?: { submitted_commit?: string } } | undefined;
  const candidateSource = application?.candidate?.submitted_commit ?? execution.submitted_commit ?? execution.input_commit;
  if (!applied.commit || !candidateSource) throw new Error("The source result has no environment association.");
  const result = await deps.runMc(["environment", "source-accepted"], new TextEncoder().encode(JSON.stringify({
    project_id: execution.project, source_commit: applied.commit, candidate_source: candidateSource,
  })));
  if (result.exitCode !== 0) throw new Error("The source environment association requires recovery.");
}

async function checkCandidateEnvironment(execution: StartedExecution, facts: RepositoryResult, deps: RepositoryDeps, ctx: ExecutionContext): Promise<void> {
  if (!deps.config.runtimeVolume || execution.wiki_update) return;
  const candidate = facts.candidate as { status?: string; commit?: string; submitted_commit?: string } | undefined;
  if (candidate?.status !== "merged") return;
  const selected = await deps.runMc(["environment", "resolve"], new TextEncoder().encode(JSON.stringify({
    project_id: execution.project, run_id: execution.run_id, source_commit: candidate.commit,
    candidate_source: candidate.submitted_commit, runtime_identity: deps.config.releaseBuildId ?? "development",
  })));
  if (selected.exitCode !== 0) throw new Error("The merge candidate environment is unavailable.");
  const environment = JSON.parse(selected.stdout);
  if (!/^sha256:[0-9a-f]{64}$/.test(environment.image_digest)) throw new Error("The merge candidate image is invalid.");
  const path = `${deps.config.mcHome}/runs/${execution.run_id}.environment.json`;
  await deps.fs.writeFile(path, JSON.stringify({ configuration: environment.configuration }), { mode: 0o644 });
  const secrets = await projectSecretValues(environment.configuration, execution.project, deps);
  const name = pipelineContainerName(execution.project, execution.run_id);
  const stop = () => { void deps.docker(["stop", name]); };
  ctx.signal.addEventListener("abort", stop, { once: true });
  try {
    if (ctx.signal.aborted) throw new ExecutionStopped();
    const result = await deps.docker(["run", "--rm", "--interactive", "--name", name,
      "--label", "mc-managed=true", "--label", "mc-tier=pipeline", "--label", `mc-run-id=${execution.run_id}`,
      "--label", `mc-project=${execution.project}`,
      "--user", "10002:10002", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
      "--memory", "4g", "--cpus", "2", "--pids-limit", "512",
      "--entrypoint", `${CULLER_RUNTIME}/bin/bun`, ...runtimeArguments(deps),
      ...projectCacheArguments(deps.config.mcHome, execution.project, environment.image_digest),
      "--mount", `type=bind,src=${repositoryRunRoot(deps, execution.run_id, subjectFields(execution))}/repo,dst=/workspace/source`,
      "--mount", `type=bind,src=${path},dst=/mc/environment.json,readonly`,
      environment.image_digest, `${CULLER_RUNTIME}/app/src/shared/check-project.ts`],
    new TextEncoder().encode(JSON.stringify(secrets) + "\n"));
    if (result.stdout.trim().startsWith("{")) {
      const evidence = JSON.parse(result.stdout);
      if (!evidence.tool_versions || !Array.isArray(evidence.package_records)) throw new Error("The merge candidate environment evidence is incomplete.");
      const recorded = await deps.runMc(["environment", "execution-evidence"], new TextEncoder().encode(JSON.stringify({
        run_id: execution.run_id, tool_versions: evidence.tool_versions, package_records: evidence.package_records,
      })));
      if (recorded.exitCode !== 0) throw new Error("The merge candidate environment evidence could not be recorded.");
    } else if (result.exitCode === 0) throw new Error("The merge candidate environment evidence is unavailable.");
    if (result.exitCode !== 0) throw new Error(`The merge candidate environment preparation failed. ${result.stderr.slice(-2048)} ${result.stdout.slice(-2048)}`);
  } finally {
    ctx.signal.removeEventListener("abort", stop);
    await deps.fs.rm(path);
  }
}

/** Capture and cleanup start only when the containers of the ended run are
 * absent. Inside the grace, a present container ends the execution without a
 * charge. After the grace, the resident stops and removes it. */
async function targetContainersAbsent(
  execution: StartedExecution, target: string, deps: RepositoryDeps, ctx: ExecutionContext,
): Promise<boolean> {
  const presence = await confirmRunContainersAbsent(execution.project, target, deps, 1);
  if (presence === "absent") return true;
  if (presence === "unknown") throw new Error("Culler cannot confirm that the containers of the ended run are absent.");
  if (ctx.graceRemainingMs() > 0) {
    await reportFailure(execution.run_id, "A container of the ended run is still present.", deps, true);
    return false;
  }
  if (ctx.signal.aborted) throw new ExecutionStopped();
  await removeRunContainers(execution.project, target, deps);
  // The autoremove of Docker or a concurrent container stop of the lane can
  // still remove the container, so the check repeats.
  if (await confirmRunContainersAbsent(execution.project, target, deps, PRODUCER_ABSENCE_ATTEMPTS) !== "absent") {
    throw new Error("A container of the ended run remains present after the stop.");
  }
  return true;
}

async function requireMergeContainerAbsent(project: string, run: string, deps: RepositoryDeps): Promise<void> {
  if (await confirmRunContainersAbsent(project, run, deps, PRODUCER_ABSENCE_ATTEMPTS) !== "absent") {
    throw new Error("The container of the interrupted merge is present.");
  }
}

/** Runs the merge candidate step or the merge in its confined container.
 * Returns undefined when the pre-container check refuses the start. */
async function mergeContainer(
  execution: StartedExecution, envelope: Record<string, unknown>, deps: RepositoryDeps, ctx: ExecutionContext,
): Promise<RepositoryResult | undefined> {
  const run = execution.run_id;
  const containerRoot = `/mc/repositories/${run}`;
  const path = `${deps.config.mcHome}/runs/${run}.merge.json`;
  await deps.fs.writeFile(path, JSON.stringify({
    ...envelope, workspace_root: "/workspace/source", run_root: containerRoot,
  }) + "\n", { mode: 0o644 });
  if (ctx.signal.aborted) throw new ExecutionStopped();
  if (!await ctx.checkBeforeContainer()) return undefined;
  const name = pipelineContainerName(execution.project, run);
  const mounts = execution.operation === "prepare"
    ? ["-v", `${repositoryRunRoot(deps, run, subjectFields(execution))}:${containerRoot}`]
    : ["-v", `${execution.workspace_root}:/workspace/source`];
  // A stop request can arrive before Docker creates the container, so the
  // stop repeats until the container run ends.
  let settled = false;
  const stopContainer = async () => {
    while (!settled) {
      const stopped = await deps.docker(["stop", name]);
      if (stopped.exitCode === 0 || settled) return;
      await deps.sleep(1000);
    }
  };
  const onAbort = () => { void stopContainer().catch(() => {}); };
  ctx.signal.addEventListener("abort", onAbort, { once: true });
  let result: Awaited<ReturnType<RepositoryDeps["docker"]>>;
  try {
    if (ctx.signal.aborted) throw new ExecutionStopped();
    result = await deps.docker([
      "run", "--rm", "--network", "none", "--name", name,
      "--label", "mc-managed=true", "--label", "mc-tier=pipeline",
      "--label", `mc-run-id=${run}`, "--label", `mc-project=${execution.project}`,
      "--user", "10002:10002", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
      "--cpus", "1", "--memory", "1024m", "--memory-swap", "1024m", "--pids-limit", "128",
      ...(deps.config.runtimeVolume ? runtimeArguments(deps) : []),
      ...mounts, "-v", `${path}:/mc/repository.json:ro`, deps.config.image,
      ...deps.config.landCmd, execution.operation, "/mc/repository.json",
    ]);
  } finally {
    settled = true;
    ctx.signal.removeEventListener("abort", onAbort);
  }
  if (result.exitCode !== 0) {
    if (ctx.signal.aborted) throw new ExecutionStopped();
    throw new Error(result.stderr.trim() || `The merge operation exited ${result.exitCode}.`);
  }
  return JSON.parse(result.stdout) as RepositoryResult;
}
