// effects.ts — the resident's effectors: apply one `mc dispatch` effect
// JSON, in the pinned order:
//
//   spawn      → mkdir MC_HOME/sessions/<run_id>; write MC_HOME/runs/<run_id>.json
//                (the launch envelope lives outside the trace-only session
//                folder and is the only thing mounted RO at /mc/run.json);
//                write the host-only mount-plan sibling; `mc __mount-recheck`;
//                docker create; `mc __mount-recheck` again; docker start.
//                Any drift removes the unstarted container. Ordinary binds
//                come only from the effect's committed mount plan.
//   reap       → docker stop mc-<project>-<run_id> (exact name, strict
//                charset), then remove the run's launch envelope and plan
//                sibling.
//   idle       → nothing
//
// Every pipeline effect names its Project: the agent container of run R in
// Project W is `mc-<W>-<R>`, labelled mc-project=<W> beside mc-tier=pipeline
// and mc-run-id=<R>. The setup containers keep `mc-setup-<R>`.
//
// Fail-closed throughout: anything unrecognized or malformed is logged and
// skipped, never guessed at; a failed effect never crashes the loop (the
// liveness limits in the spine are the recovery path). An agent run holds the
// Project lease, or it is the Scribe run of a wiki update.

import { posix } from "node:path";
import { parseProviderAccess } from "../../runner/agent-runner/adapters/provider-access";
import type { Effect, MountPlan, PacketIdentity, RuntimeBinding, TickDeps } from "./types";
import { canonicalCollieDockerEnvelope } from "./collie-envelope";
import {
  composeRunSkills, ensureProjectSkillStore, readOperatorSkills, shippedSkillsFrom, skillStoreHostDir,
  type RunSkill, type StoreFs,
} from "./skill-store";
import { configuredGitEnvironment } from "./git-identity";
import { deploymentIdentity, deploymentArguments, CULLER_RUNTIME, projectExecutionArguments, runtimeArguments, openCodeArguments, projectCacheArguments, projectHomeArguments, projectSecretValues } from "./environment-lane";
import { prepareRepositoryRun, repositoryRunRoot, subjectFields } from "./repository";
import {
  inspectPipelineRunContainer, pipelineContainerName, PROJECT_ID, SAFE_ID,
} from "./pipeline-liveness";

export { pipelineContainerName, PROJECT_ID, SAFE_ID } from "./pipeline-liveness";

export async function applyEffect(effect: Effect, deps: TickDeps): Promise<void> {
  // `mc` emits `action: "refused"` for every dispatch refusal. `mc` applied the
  // consequence of the refusal inside its own transaction before the effect
  // reaches the resident. This branch commits nothing.
  //
  // Deliberately not a `case` and not a member of the `Effect` union: `Effect`
  // describes what the resident effects, and this is the one message that
  // asks it to effect nothing.
  //
  // Verbatim interpolation only. No mapping table, no severity, no prettifying
  // of codes: that is interpretation, and it stays out of the resident.
  const refusal = effect as unknown as { action?: unknown; code?: unknown; consequence?: unknown };
  if (refusal.action === "refused") {
    deps.log.warn("tick.dispatch_refused",
      `tick: dispatch refused (${String(refusal.code)}); consequence ${String(refusal.consequence)}: ${boundedEffect(effect)}`,
    );
    return;
  }
  switch (effect.action) {
    case "idle":
      deps.log.debug("tick.idle", `tick: idle${effect.reason ? ` (${effect.reason})` : ""}`);
      return;
    case "spawn":
      try { return await spawn(effect, deps); }
      catch (error) {
        deps.credentials?.revoke?.(effect.run_id, effect.run_id);
        await removeProviderAccessFile(effect.run_id, effect.run_id, deps);
        throw error;
      }
    case "reap":
      // An operator interrupt arrives here too, as an ordinary reap carrying
      // reason "interrupted".
      return reap(effect, deps);
    case "collie-wake": {
      let started = false;
      try { started = await collieWake(effect, deps) === true; }
      finally {
        if (!started) {
          deps.credentials?.revoke?.(effect.session, effect.launch);
          await removeProviderAccessFile(effect.session, effect.launch, deps);
        }
        if (!started) await deps.runMc(["collie", "launch-failure", effect.session, "--launch", effect.launch]);
      }
      return;
    }
    case "collie-adopt":
      return collieAdopt(effect, deps);
    case "collie-stop":
      return collieStop(effect, deps);
    case "collie-remove":
      return collieRemove(effect, deps);
    default: {
      // The action is unhandled, but the effect usually explains itself, so
      // the whole effect is logged. Bounded so a hostile or runaway effect
      // cannot flood the log through this path.
      deps.log.warn("tick.effect_unknown",
        `tick: unknown action ${JSON.stringify((effect as { action?: unknown }).action)}; ignored (fail-closed): ${boundedEffect(effect)}`,
      );
      return;
    }
  }
}

/** The effect rendered whole, capped so a runaway or hostile effect cannot
 * flood the log. Shared by the refusal branch and the fail-closed default so
 * the bound is one number in one place. */
function boundedEffect(effect: unknown): string {
  const detail = JSON.stringify(effect);
  return detail.length > 512 ? `${detail.slice(0, 512)}…` : detail;
}

/** Host dir for materialized launch envelopes: a sibling of sessions/, never
 * inside a session folder, which holds nothing but the trace. */
export function runsDir(mcHome: string): string {
  return `${mcHome}/runs`;
}

function providerAccessHostPath(mcHome: string, run: string, launch: string): string {
  if (!SAFE_ID.test(run) || !SAFE_ID.test(launch)) throw new Error("The provider launch identity is invalid.");
  return `${runsDir(mcHome)}/provider-access/${run}-${launch}.json`;
}

async function removeProviderAccessFiles(run: string, deps: TickDeps): Promise<void> {
  if (!SAFE_ID.test(run)) return;
  const root = `${runsDir(deps.config.mcHome)}/provider-access`;
  let files: string[];
  try { files = await deps.fs.readdir(root); } catch { return; }
  for (const file of files) {
    if (file.startsWith(`${run}-`) && /^[0-9a-f]{16}\.json$/.test(file.slice(run.length + 1))) {
      await deps.fs.rm(`${root}/${file}`);
    }
  }
}

async function removeProviderAccessFile(run: string, launch: string, deps: TickDeps): Promise<void> {
  if (!SAFE_ID.test(run) || !SAFE_ID.test(launch)) return;
  const path = providerAccessHostPath(deps.config.mcHome, run, launch);
  try { await deps.fs.readFile(path); } catch { return; }
  await discarder(deps, `provider access ${run}`)(path);
}

/** Host path of one run's launch envelope. */
function runJsonHostPath(mcHome: string, runId: string): string {
  return `${runsDir(mcHome)}/${runId}.json`;
}

/** Host path of one run's mount-plan sibling. It is host-only recheck input
 * for `mc __mount-recheck` and is NEVER mounted into the container — the
 * agent-visible envelope carries no host source paths. */
function mountPlanHostPath(mcHome: string, runId: string): string {
  return `${runsDir(mcHome)}/${runId}.mounts.json`;
}

/** The resident consumes only what it binds: source/destination/access must
 * be sound for docker -v grammar; everything deeper is mc's recheck. Returns
 * a refusal reason or null. */
function invalidMountPlanReason(plan: MountPlan | undefined): string | null {
  if (plan === null || typeof plan !== "object" || plan.version !== 1 || !Array.isArray(plan.entries)) {
    return "dispatch effect carries no explicit mount plan";
  }
  for (const entry of plan.entries) {
    if (entry === null || typeof entry !== "object") return "mount entry is not an object";
    if (typeof entry.source !== "string" || !entry.source.startsWith("/") || entry.source.includes(":")) {
      return `mount source ${JSON.stringify(entry.source)} is not an absolute colon-free host path`;
    }
    // The plan names only the artifact class namespace under /mc/artifacts/.
    // Every other destination is the resident's own bind. The normalization
    // refuses `/mc/artifacts/../run.json`, which Docker would clean onto the
    // run's read-only envelope.
    if (
      typeof entry.destination !== "string" ||
      entry.destination.includes(":") ||
      posix.normalize(entry.destination) !== entry.destination ||
      !entry.destination.startsWith("/mc/artifacts/")
    ) {
      return `mount destination ${JSON.stringify(entry.destination)} is outside the colon-free /mc/artifacts/ class namespace`;
    }
    if (entry.access !== "ro" && entry.access !== "rw") {
      return `mount access ${JSON.stringify(entry.access)} is not ro|rw`;
    }
  }
  if (plan.repository !== undefined) {
    try {
      subjectFields(plan.repository);
    } catch (err) {
      return err instanceof Error ? err.message : String(err);
    }
  }
  if (plan.tool_home !== undefined) {
    const home = plan.tool_home;
    if (home === null || typeof home !== "object" ||
        typeof home.canonical !== "string" || !home.canonical.startsWith("/") ||
        /[:,\0\r\n]/.test(home.canonical) ||
        posix.normalize(home.canonical) !== home.canonical ||
        !(/^(0|[1-9][0-9]*)$/).test(home.device) ||
        !(/^(0|[1-9][0-9]*)$/).test(home.inode) ||
        !Number.isSafeInteger(home.owner_uid) || home.owner_uid < 0) {
      return "synthetic home descriptor is malformed";
    }
  }
	if (plan.seeding_projections !== undefined) {
		if (!Array.isArray(plan.seeding_projections) || plan.seeding_projections.length === 0) {
			return "seeding projections are malformed";
		}
		let prior = "";
		for (const step of plan.seeding_projections) {
			const root = step?.workspace_root;
			if (step === null || typeof step !== "object" ||
				typeof step.project_id !== "string" || step.project_id === "" ||
				step.project_id === "." || step.project_id === ".." ||
				/[\\/:\x00-\x1f\x7f]/.test(step.project_id) || step.project_id <= prior ||
				step.projection_kind !== "seeding-committed-projection" ||
				step.git_cover_kind !== "seeding-source-git-cover" ||
				(step.object_format !== "sha1" && step.object_format !== "sha256") ||
				typeof step.target_ref !== "string" || step.target_ref === "" ||
				step.target_ref.includes("\0") || step.target_ref.includes("\n") || step.target_ref.includes("\r") ||
				root === null || typeof root !== "object" ||
				typeof root.canonical !== "string" || !root.canonical.startsWith("/") ||
				root.canonical.includes(":") || posix.normalize(root.canonical) !== root.canonical ||
				!(/^(0|[1-9][0-9]*)$/).test(root.device) || !(/^(0|[1-9][0-9]*)$/).test(root.inode) ||
				!Number.isSafeInteger(root.owner_uid) || root.owner_uid < 0) {
				return "seeding projection descriptor is malformed, unsorted, or duplicated";
			}
			prior = step.project_id;
		}
	}
  return null;
}

const RUNTIME_BINDING_TEXT_FIELDS = [
  "base_url", "channel", "credential", "harness", "id", "model",
] as const;

/** Structural guard for the carried runtime capability.
 *
 * The resident requires every key it consumes, present and typed, because
 * this carrier is the resident's only authority for what a launch may be. A
 * partial object would degrade into a launch built from whatever happened to
 * be there. Keys the resident does not consume are ignored. The fake family
 * is token-free and carries an empty credential; every other family must
 * name its credential source. */
function resolvedRuntime(value: unknown): RuntimeBinding | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  for (const field of RUNTIME_BINDING_TEXT_FIELDS) {
    if (typeof record[field] !== "string") return null;
  }
  if (record["id"] === "" || record["harness"] === "") return null;
  const isFakeFamily = record["harness"] === "fake" && record["id"] === "fake";
  if (!isFakeFamily && (record["credential"] === "" || record["base_url"] !== "" ||
      !((record.harness === "codex" && record.channel === "codex") ||
        (record.harness === "claude-sdk" && record.channel === "claude") ||
        (record.harness === "opencode" && record.channel === "opencode")))) return null;
  if (record.effort !== undefined && typeof record.effort !== "string") return null;
  const out: RuntimeBinding = {
    ...(record.effort ? { effort: record.effort as string } : {}),
    ...(typeof record.authorization_id === "string" ? { authorization_id: record.authorization_id } : {}),
    ...(typeof record.profile_revision === "string" ? { profile_revision: record.profile_revision } : {}),
    base_url: record["base_url"] as string,
    channel: record["channel"] as string,
    credential: record["credential"] as string,
    harness: record["harness"] as string,
    id: record["id"] as string,
    model: record["model"] as string,
  };
  return out;
}

/** The harness families this IMAGE ships an adapter for.
 *
 * This is deliberately not a route catalog — config.toml owns which bindings
 * exist and which are enabled, and the resident stopped second-guessing that.
 * It is the resident's own fact: a family with no adapter in the image cannot
 * be launched no matter what the operator configures, and refusing here beats
 * discovering it inside a started container. */
const ADAPTER_HARNESS_FAMILIES = new Set(["claude-sdk", "codex", "opencode", "fake"]);

/** The Collie container's memory cap, as Docker takes it and as it reports it. */
const COLLIE_MEMORY = "1024m";
const COLLIE_MEMORY_BYTES = 1024 * 1024 * 1024;
const COLLIE_PIDS_LIMIT = 128;
const COLLIE_USER = "10002:10002";
/** Where a Collie container reads each session folder. The Collie role directive
 * and the `culler` skill name this path. */
const COLLIE_TRACE_MOUNT = "/mc/traces";
/** The artifact root of a Project, relative to its folder. */
const PROJECT_ARTIFACT_DIR = ".mc-artifacts";
/** The container root of each recorded artifact path. */
const ARTIFACTS_MOUNT = "/mc/artifacts";

/** The empty folder that each Collie container sees at the artifact root of
 * each Project folder. */
function artifactMaskDir(mcHome: string): string {
  return `${runsDir(mcHome)}/artifact-mask`;
}

const PACKET_KEYS = JSON.stringify(["packet_id", "project_id", "task_id", "version_id"]);

/** The packet of a packet conversation, or null when the value does not hold
 * exactly three positive identifiers and a Project identifier. */
function packetIdentity(value: unknown): PacketIdentity | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  if (JSON.stringify(Object.keys(record).sort()) !== PACKET_KEYS) return null;
  const { packet_id, version_id, task_id, project_id } = record;
  for (const id of [packet_id, version_id, task_id]) {
    if (!Number.isSafeInteger(id) || (id as number) < 1) return null;
  }
  if (typeof project_id !== "string" || !SAFE_ID.test(project_id)) return null;
  return {
    packet_id: packet_id as number,
    version_id: version_id as number,
    task_id: task_id as number,
    project_id,
  };
}

/** The read-only bind of the artifact folder of one task. The host command
 * resolves the folder through the mount allowlist. `source` is null when the
 * folder is absent, for example after artifact expiry. */
async function taskArtifactMount(
  deps: TickDeps,
  log: TickDeps["log"],
  session: string,
  root: string,
  task: number,
): Promise<{ destination: string; source: string | null }> {
  const result = await deps.runMc(["__task-artifacts", root, String(task)]);
  if (result.exitCode !== 0) {
    log.warn("collie.wake_refused",
      `collie-wake ${session}: mc __task-artifacts exited ${result.exitCode}: ${result.stderr.trim()}`);
    throw new Error("The artifact files of the packet are unavailable. Restore the Project access and retry the message.");
  }
  let mount: { destination?: unknown; source?: unknown } = {};
  try {
    const parsed = JSON.parse(result.stdout) as unknown;
    if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) mount = parsed as typeof mount;
  } catch {
    mount = {};
  }
  const { destination, source } = mount;
  // Each bind path must be a normal absolute path that ends with the task
  // folder. Docker normalizes a bind path, so a path in another form can name
  // another folder.
  const taskFolder = (path: unknown): path is string =>
    typeof path === "string" && path.startsWith("/") && !path.includes(":") &&
    posix.normalize(path) === path && path.endsWith(`/${task}`);
  if (
    !taskFolder(destination) ||
    !destination.startsWith(`${ARTIFACTS_MOUNT}/`) ||
    (source !== null && !taskFolder(source))
  ) {
    throw new Error(`collie-wake ${session}: the artifact mount of the packet is malformed`);
  }
  return { destination, source };
}

/** The workspaces that get an artifact mask. A workspace gets no mask only
 * when its artifact root is absent or is not a folder. Each other read error
 * adds the mask, because the container can possibly read that folder. */
async function workspacesWithArtifactRoot(
  deps: TickDeps,
  workspaces: Array<{ id: string; root: string }>,
): Promise<Set<string>> {
  const found = new Set<string>();
  for (const workspace of workspaces) {
    try {
      await deps.fs.readdir(`${workspace.root}/${PROJECT_ARTIFACT_DIR}`);
      found.add(workspace.id);
    } catch (err) {
      const code = (err as { code?: unknown } | null)?.code;
      if (code !== "ENOENT" && code !== "ENOTDIR") found.add(workspace.id);
    }
  }
  return found;
}

/** The `skills` / `skill_problems` members of a run.json: the shipped skills
 * the effect carries, then every deliverable operator skill in the store
 * directory the effect names. A store directory the resident will not
 * resolve, or a shipped set it cannot read, is itself a problem — the effect
 * is `mc dispatch`'s, and a value this refuses means the two disagree about
 * the wire shape. */
async function runSkillSet(
  effect: { skills?: unknown; skill_store_dir?: string },
  mcHome: string,
  fs: StoreFs,
): Promise<{ skills?: RunSkill[]; skill_problems?: string[] }> {
  const problems: string[] = [];
  let shipped = shippedSkillsFrom(effect.skills);
  if (shipped === undefined) {
    problems.push("refused the effect's shipped skills: not a list of named, non-empty skills");
    shipped = [];
  }
  let operator: { skills: RunSkill[]; problems: string[] } = { skills: [], problems: [] };
  if (effect.skill_store_dir !== undefined) {
    const dir = skillStoreHostDir(mcHome, effect.skill_store_dir);
    operator = dir === undefined
      ? { skills: [], problems: [`refused skill store directory ${JSON.stringify(effect.skill_store_dir)}: not a clean path under skills/`] }
      : await readOperatorSkills(dir, fs);
  }
  const composed = composeRunSkills(shipped, { skills: operator.skills, problems: [...problems, ...operator.problems] });
  return {
    ...(composed.skills.length > 0 ? { skills: composed.skills } : {}),
    ...(composed.problems.length > 0 ? { skill_problems: composed.problems } : {}),
  };
}

/** The launch-envelope projection of the capability: the non-secret shape the
 * runner and its adapters need. The credential source id stays on the host;
 * the resident mints tokens, and a container never authenticates itself. */
function runtimeEnvelope(runtime: RuntimeBinding) {
  return {
    base_url: runtime.base_url,
    channel: runtime.channel,
    harness: runtime.harness,
    id: runtime.id,
    model: runtime.model,
    ...(runtime.effort ? { effort: runtime.effort } : {}),
  };
}

type SpawnEffect = Extract<Effect, { action: "spawn" }>;
type ReapEffect = Extract<Effect, { action: "reap" }>;
type CollieWakeEffect = Extract<Effect, { action: "collie-wake" }>;
type CollieAdoptEffect = Extract<Effect, { action: "collie-adopt" }>;
type CollieStopEffect = Extract<Effect, { action: "collie-stop" }>;
type CollieRemoveEffect = Extract<Effect, { action: "collie-remove" }>;

/** The lease-free Collie wake effector. The dispatch
 * tick has already minted and persisted the launch generation; the resident's
 * only authority here is the container: materialize the folder + a heartbeat-
 * free tier:"collie" run.json, create the container over a fixed operator-read-
 * scope mount set (the workspace is bound RO, its artifact root is masked, and
 * a packet conversation gets the artifact folder of its task RO), CAS the exact
 * docker id onto the launch (`mc collie launch-bind`) BEFORE start so a fenced
 * or ended session never gets a running container, then start it. The runner
 * reports `mc collie runner-started` from inside once it boots (never here).
 * There is no lease, no heartbeat, and no reservation: many Collie containers
 * run concurrently. */
async function collieWake(effect: CollieWakeEffect, deps: TickDeps): Promise<boolean | void> {
  const { config } = deps;
  const {
    session, launch, mode, binding, session_path, native_session_ref,
    trace_filename, container_name,
  } = effect;
  // Every record this function writes names its session and launch as FIELDS,
  // whether or not the sentence that produced it remembered to. A refusal that
  // says only "wake refused" is unattributable the moment two sessions overlap,
  // and Collie sessions overlap by design — there is no lease here.
  const log = deps.log.child({ session, launch });
  const availableWorkspaces = await deps.readWorkspaces();
  const workspaces = effect.vision_project_id === undefined
    ? availableWorkspaces
    : availableWorkspaces.filter((workspace) => workspace.id === effect.vision_project_id);
  if (effect.vision_project_id !== undefined &&
      (!SAFE_ID.test(effect.vision_project_id) || workspaces.length !== 1)) {
    throw new Error("The Vision Project folder is unavailable. Restore its access and retry the message.");
  }
  // A packet conversation keeps the ordinary workspace list. The Project of its
  // packet must be in that list.
  const packet = effect.packet === undefined ? undefined : packetIdentity(effect.packet);
  if (packet === null || (packet !== undefined && effect.vision_project_id !== undefined)) {
    throw new Error(`collie-wake ${session}: the packet identity is malformed`);
  }
  const packetWorkspace = packet && workspaces.find((workspace) => workspace.id === packet.project_id);
  if (packet !== undefined && packetWorkspace === undefined) {
    throw new Error("The packet Project folder is unavailable. Restore its access and retry the message.");
  }
  const behavior = config.roleBehaviors["collie"];

  if (typeof effect.directive !== "string" || effect.directive.trim() === "") {
    throw new Error(`collie-wake ${session}: the Collie directive is required`);
  }

  // The spine freezes all four resume locators. The session folder grammar is
  // deterministic, so compare it to the session id instead of normalizing or
  // guessing; native mode requires the complete immutable pair. Refuse before
  // the first filesystem or Docker effect.
  if (!SAFE_ID.test(session) || session_path !== `sessions/${session}`) {
    throw new Error(`collie-wake ${session}: session_path does not match the frozen session id`);
  }
  if (!/^[0-9a-f]{16}$/.test(launch) || container_name !== `mc-collie-${session}`) {
    throw new Error(`collie-wake ${session}: launch/container identity is malformed`);
  }
  if (mode !== "fresh" && mode !== "native" && mode !== "rows") {
    throw new Error(`collie-wake ${session}: unsupported mode ${JSON.stringify(mode)}`);
  }
  if (mode === "native" && (
    typeof native_session_ref !== "string" || native_session_ref === "" ||
    typeof trace_filename !== "string" || trace_filename === ""
  )) {
    throw new Error(`collie-wake ${session}: native mode requires the immutable locator pair`);
  }
  if (mode !== "native" && (native_session_ref !== undefined || trace_filename !== undefined)) {
    throw new Error(`collie-wake ${session}: native locators are forbidden in ${mode} mode`);
  }

  // Resolve the frozen historical route from the capability the dispatch tick
  // carried, never from today's routing.md and never from a catalog compiled
  // into the resident. config.toml is the only authority for what this
  // deployment can run, and the host already resolved the session's frozen
  // binding against it. Explicit agentRunnerRoutes remain test-only fake
  // stand-ins. Projection happens before the first launch file exists.
  const bindingParts = binding.split("/");
  if (bindingParts.length !== 2 || bindingParts.some((part) => part === "")) {
    log.warn("collie.wake_refused", `collie-wake ${session}: malformed historical binding ${JSON.stringify(binding)}; refusing`);
    return;
  }
  const [harness, modelBinding] = bindingParts as [string, string];
  const runtime = resolvedRuntime(effect.runtime_binding);
  if (runtime === null) {
    log.warn("collie.wake_refused", `collie-wake ${session}: carries no resolved runtime capability; refusing`);
    return;
  }
  if (runtime.harness !== harness || runtime.id !== modelBinding) {
    log.warn("collie.wake_refused",
      `collie-wake ${session}: resolved capability ${JSON.stringify(`${runtime.harness}/${runtime.id}`)} ` +
      `does not describe the frozen binding ${JSON.stringify(binding)}; refusing`,
    );
    return;
  }
  const isFakeFamily = runtime.harness === "fake" && runtime.id === "fake";
  const usesFakeAdapter = isFakeFamily || (config.agentRunnerRoutes?.includes(binding) ?? false);
  if (!usesFakeAdapter && !ADAPTER_HARNESS_FAMILIES.has(runtime.harness)) {
    log.warn("collie.wake_refused", `collie-wake ${session}: this image ships no ${JSON.stringify(runtime.harness)} adapter; refusing`);
    return;
  }
  if (usesFakeAdapter && behavior === undefined) {
    log.warn("collie.wake_refused", `collie-wake ${session}: fake adapter has no Collie behavior; refusing`);
    return;
  }
  if (!isFakeFamily) {
    const admission = await deps.runMc(["collie", "launch-check", session, "--launch", launch,
      "--connection", runtime.credential, "--authorization", runtime.authorization_id ?? "",
      "--profile", runtime.id, "--profile-revision", runtime.profile_revision ?? ""]);
    if (admission.exitCode !== 0) return;
  }
  deps.credentials?.revoke?.(session);
  const projection = isFakeFamily
    ? null
    : deps.credentials?.project(runtime, { runId: session, launchId: launch,
      expiresAt: Date.now() + 60 * 60_000, model: runtime.model, profileRevision: runtime.profile_revision }) ?? {
      refused: "runtime credential projector unavailable",
    };
  if (projection !== null && "refused" in projection) {
    log.warn("collie.wake_refused",
      `collie-wake ${session}: credential projection unavailable for ${binding}: ` +
      `${projection.refused}`,
    );
    return;
  }
  if (projection !== null) {
    try {
      if (Object.keys(projection.env).length || typeof projection.proxyConfig !== "string") throw new Error("The provider access configuration is absent.");
      parseProviderAccess(projection.proxyConfig, runtime.harness as "codex" | "claude-sdk" | "opencode");
    } catch {
      throw new Error("The provider access configuration is invalid.");
    }
  }
  const providerAccessPath = projection?.proxyConfig === undefined ? undefined
    : providerAccessHostPath(config.mcHome, session, launch);

  // The frozen operator-verb allowlist must be present and well-shaped BEFORE
  // any launch artifact exists, for the same reason the projection shape is
  // checked here: a Collie launched without it comes up able to read and nothing
  // else, and the model's only documentation says it owns twelve write verbs, so
  // it retries them against a gate that can never admit it. Refusing is the
  // recoverable outcome; a warm useless Collie is not.
  const verbAllowlist = effect.verb_allowlist;
  if (!Array.isArray(verbAllowlist) || verbAllowlist.some((v) => typeof v !== "string")) {
    log.warn("collie.wake_refused", `collie-wake ${session}: wake carries no frozen verb allowlist; refusing`);
    return;
  }

  // The runner's own exit deadline. An older `mc` sends no value; the runner
  // then falls back to its fixed default.
  const idleTimeoutS = Number.isSafeInteger(effect.idle_timeout_s) && effect.idle_timeout_s! > 0
    ? effect.idle_timeout_s
    : undefined;

  const taskArtifacts = packet === undefined
    ? undefined
    : await taskArtifactMount(deps, log, session, packetWorkspace!.root, packet.task_id);

  // The stable attachment tree is a sibling of sessions/, never trace content.
  // Provision/revalidate it before the first launch artifact so a failed
  // ownership/mode/symlink fence produces no warm Collie or partial envelope.
  deps.attachmentConfinement.assertHealthy();
  const attachmentTree = await deps.prepareAttachmentTree(session);

  // 1. folder (trace-only) + the sibling envelope dir + the per-launch
  // scratch that is this tier's only writable working directory (below).
  const sessionDir = `${config.mcHome}/${session_path}`;
  const scratchDir = `${runsDir(config.mcHome)}/scratch/${session}`;
  // Same bind, same reasoning, as the pipeline tier: stderr-class diagnostics
  // only, never the harness event stream, and a leaf the container can write.
  const runsLogDir = `${config.mcHome}/logs/runs/${session}`;
  await deps.fs.mkdir(sessionDir);
  await deps.fs.mkdir(`${config.mcHome}/agentsview/inspection`);
  await deps.fs.mkdir(runsDir(config.mcHome));
  await deps.fs.mkdir(scratchDir);
  await deps.fs.mkdir(runsLogDir, { mode: 0o777 });
  const maskDir = artifactMaskDir(config.mcHome);
  await deps.fs.mkdir(maskDir);
  if ((await deps.fs.readdir(maskDir)).length > 0) {
    throw new Error("The artifact mask folder is not empty.");
  }
  const maskedWorkspaces = await workspacesWithArtifactRoot(deps, workspaces);

  // 2. run.json — lease-free: no heartbeat interval, run_id == session. The
  // binding is the historical route key ("harness/model_binding"); the runner
  // gates on it symmetric with the agent-runner. harness_config.behavior is a
  // fake-adapter concept, carried only when the deployment supplies a collie
  // behavior fixture (a real harness needs none).
  // The session's skill set: the Collie tier's shipped skills from the wake,
  // then every operator skill in the store's `collie` directory. Read now, so the container holds the set that
  // existed when it started.
  const runSkills = await runSkillSet(effect, config.mcHome, deps.fs);
  const runJson = {
    runtime_binding: effect.runtime_binding,
    run_id: session,
    session_id: session,
    ...(effect.vision_project_id === undefined ? {} : { vision_project_id: effect.vision_project_id }),
    ...(packet === undefined ? {} : { packet }),
    tier: "collie",
    release_build_id: config.releaseBuildId ?? "development",
    directive: effect.directive,
    mode,
    binding,
    harness,
    model_binding: modelBinding,
    // Access uses the current profile; effort belongs to the conversation.
    runtime: runtimeEnvelope({ ...runtime, effort: effect.effort ?? runtime.effort }),
    launch,
    session_path,
    // Same carrier as the pipeline envelope: `skills` is the set the runner
    // installs before the first turn, `skill_problems` what it writes into
    // the run's log; absence reads as "none".
    ...runSkills,
    // NOT under the omit-whole-rather-than-half rule the skill uses: this key is
    // the Collie's entire write capability. `mc` in the container admits an
    // operator verb only by scanning run.json's verb_allowlist and then matching
    // it against the frozen registry value, so an absent key is indistinguishable
    // from "no verbs" and silently reduces the session to reads. It is carried
    // unconditionally and validated above.
    verb_allowlist: verbAllowlist,
    ...(idleTimeoutS === undefined ? {} : { idle_timeout_s: idleTimeoutS }),
    ...(mode === "native" ? { native_session_ref, trace_filename } : {}),
    ...(usesFakeAdapter ? { harness_config: { behavior: behavior! } } : {}),
    mounts: {
      session: "/mc/session",
      // The Project folders of a Collie are read-only. The per-launch scratch
      // is the writable cwd, and it dies with the --rm container.
      workspace: "/mc/scratch",
      workspaces: workspaces.map((w) => ({ id: w.id, path: `/workspaces/${w.id}` })),
      attachments: {
        inbound: "/mc/attachments/in",
        outbound: "/mc/private/attachments/out",
      },
    },
  };
  const runJsonPath = runJsonHostPath(config.mcHome, session);
  const runJsonBody = JSON.stringify(runJson, null, 2) + "\n";
  await deps.fs.writeFile(runJsonPath, runJsonBody);
  if (providerAccessPath !== undefined) {
    await deps.fs.mkdir(`${runsDir(config.mcHome)}/provider-access`, { mode: 0o700 });
    await deps.fs.writeFile(providerAccessPath, projection!.proxyConfig!, { mode: 0o644 });
  }

  // 3. container. The container name is session-fixed (mc-collie-<session>), so a
  // prior now-dead generation — an ended-then-resumed session whose old --rm
  // container has not yet idled out — may still hold it. The launch fence
  // guarantees at most one LIVE generation per session, so force-remove any
  // stale same-named container before create (a no-op if absent). Labelled
  // mc-tier=collie for the inventory/reap sweep; --rm so the container is gone
  // once the runner exits; mount set is fixed operator read-scope. The memory
  // cap has no swap headroom and no CPU cap: the Collie answers the operator
  // and must not be throttled while it does.
  await deps.docker(["rm", "-f", container_name]);
  deps.collieRuntime.revokeSession(session);
  const createPrefix = [
    "create", "--rm",
    ...(isFakeFamily ? ["--network", "none"] : []),
    // The pipeline profile: uid 10002 without capabilities reaches the
    // spine only through the setuid mc.
    "--user", COLLIE_USER,
    "--cap-drop", "ALL",
    "--memory", COLLIE_MEMORY,
    "--memory-swap", COLLIE_MEMORY,
    "--pids-limit", String(COLLIE_PIDS_LIMIT),
    "--name", container_name,
    "--label", "mc-managed=true", ...deploymentArguments(config.mcHome),
    "--label", "mc-tier=collie",
    "--label", `mc-session-id=${session}`,
    ...(effect.vision_project_id ? ["--label", `com.culler.project=${effect.vision_project_id}`] : []),
    ...(packet ? ["--label", `com.culler.project=${packet.project_id}`] : []),
    "--label", `mc-launch-id=${launch}`,
  ];
  const createPayload = [
    ...(config.runtimeVolume ? runtimeArguments(deps) : []),
    ...openCodeArguments(config.mcHome, runtime?.harness),
    ...(config.runtimeVolume ? ["--entrypoint", `${CULLER_RUNTIME}/bin/bun`] : []),
    "-v", `${sessionDir}:/mc/session`,
    // Collie can read the trace of each run and each Collie session.
    "-v", `${config.mcHome}/agentsview/inspection:${COLLIE_TRACE_MOUNT}:ro`,
    "-v", `${runJsonPath}:/mc/run.json:ro`,
    "-v", `${attachmentTree.inbound_published}:/mc/attachments/in:ro`,
    "-v", `${attachmentTree.outbound}:/mc/private/attachments/out`,
    "-v", `${scratchDir}:/mc/scratch`,
    "-v", `${runsLogDir}:/mc/logs`,
    "-v", `${config.behaviorsDir}:/mc/behaviors:ro`,
    ...(!config.runtimeVolume ? ["-v", `${config.runnerSrcDir}:/app/src:ro`] : []),
    // Collie can read the current Project folders.
    ...(workspaces.length ? ["-v", `${workspaces[0].root}:/workspace/source:ro`] : []),
    ...workspaces.flatMap((w) => ["-v", `${w.root}:/workspaces/${w.id}:ro`]),
    // The empty mask hides the artifact files of each task in each Project
    // folder. A packet conversation reads the artifact files of its own task
    // at the recorded artifact paths.
    ...(workspaces.length && maskedWorkspaces.has(workspaces[0].id)
      ? ["-v", `${maskDir}:/workspace/source/${PROJECT_ARTIFACT_DIR}:ro`]
      : []),
    ...workspaces.filter((w) => maskedWorkspaces.has(w.id))
      .flatMap((w) => ["-v", `${maskDir}:/workspaces/${w.id}/${PROJECT_ARTIFACT_DIR}:ro`]),
    ...(taskArtifacts?.source ? ["-v", `${taskArtifacts.source}:${taskArtifacts.destination}:ro`] : []),
    ...(providerAccessPath === undefined ? [] : [
      "-v", `${providerAccessPath}:/mc/provider-access.json:ro`,
      "-e", "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json",
    ]),
    "-v", `${config.spineVolume}:${posix.dirname(config.spineDbPath)}`,
    "-e", `MC_SPINE=${config.spineDbPath}`,
    // Stated, not inherited. The Collie tier has no synthetic home to bind — it
    // holds no lease and no Project identity. The runner derives the skill
    // root from HOME, and an unset one makes the install a silent no-op, so
    // pin it to the image home of uid 10002. The container is `--rm`, so this
    // home is per-launch by construction.
    "-e", "HOME=/home/mc-model",
    ...logLevelEnv(),
    ...zoneEnv(deps),
    ...(config.agentRunnerRoutes && config.agentRunnerRoutes.length > 0
      ? ["-e", `MC_AGENT_RUNNER_ROUTES=${config.agentRunnerRoutes.join(",")}`]
      : []),
    config.image,
    ...(config.runtimeVolume ? (config.collieCmd ?? config.agentCmd).slice(1) : config.collieCmd ?? config.agentCmd),
  ];
  const envelope_digest = deps.collieRuntime.digestEnvelope(JSON.stringify({
    provider_access: projection?.proxyConfig ?? null,
    create_argv: [...createPrefix, ...createPayload],
    run_json: runJsonBody,
  }));
  const runtime_generation = deps.collieRuntime.register({ session, launch, envelope_digest },
    projection?.proxyConfig === undefined ? undefined : () => deps.credentials?.has?.(session, launch) === true);
  const revokeRuntime = () => {
    deps.credentials?.revoke?.(session, launch);
    return deps.collieRuntime.revoke({ session, launch, envelope_digest, runtime_generation });
  };
  const cleanup = async (containerIdentity = container_name) => {
    await deps.docker(["rm", containerIdentity]);
    revokeRuntime();
    await deps.fs.rm(runJsonPath);
    if (providerAccessPath !== undefined) await deps.fs.rm(providerAccessPath);
  };
  const createArgs = [
    ...createPrefix,
    "--label", `mc-envelope-digest=${envelope_digest}`,
    "--label", `mc-runtime-generation=${runtime_generation}`,
    ...createPayload,
  ];
  const created = await deps.docker(createArgs);
  if (created.exitCode !== 0) {
    log.warn("collie.create_failed", `collie-wake ${session}: docker create failed (exit ${created.exitCode}): ${created.stderr.trim()}`);
    revokeRuntime();
    await deps.fs.rm(runJsonPath);
    if (providerAccessPath !== undefined) await deps.fs.rm(providerAccessPath);
    return;
  }
  const containerId = created.stdout.trim();
  if (!/^[0-9a-f]{64}$/.test(containerId)) {
    log.warn("collie.create_failed", `collie-wake ${session}: docker create returned a non-canonical id; abandoning container ${container_name}`);
    await cleanup();
    return;
  }
  const inspected = await deps.docker(["inspect", "--type", "container", containerId]);
  let dockerEnvelope: string;
  try {
    if (inspected.exitCode !== 0) {
      throw new Error(`inspect exited ${inspected.exitCode}: ${inspected.stderr.trim()}`);
    }
    const records = JSON.parse(inspected.stdout) as unknown;
    if (!Array.isArray(records) || records.length !== 1) {
      throw new Error("inspect did not return exactly one container");
    }
    const values = (flag: string) => createArgs.flatMap((arg, index) =>
      arg === flag && createArgs[index + 1] !== undefined ? [createArgs[index + 1]!] : []);
    dockerEnvelope = canonicalCollieDockerEnvelope(records[0], {
      binds: values("-v"),
      mounts: values("--mount"),
      tmpfs: values("--tmpfs"),
      cap_drop: ["ALL"],
      cmd: config.runtimeVolume ? (config.collieCmd ?? config.agentCmd).slice(1) : config.collieCmd ?? config.agentCmd,
      ...(config.runtimeVolume ? { entrypoint: [`${CULLER_RUNTIME}/bin/bun`] } : {}),
      container_id: containerId,
      env: values("-e"),
      image: config.image,
      mc_labels: {
        "mc-deployment": deploymentIdentity(config.mcHome),
        "mc-envelope-digest": envelope_digest,
        "mc-launch-id": launch,
        "mc-managed": "true",
        "mc-runtime-generation": runtime_generation,
        "mc-session-id": session,
        "mc-tier": "collie",
      },
      memory: COLLIE_MEMORY_BYTES,
      memory_swap: COLLIE_MEMORY_BYTES,
      name: container_name,
      network_modes: isFakeFamily ? ["none"] : ["default", "bridge"],
      pids_limit: COLLIE_PIDS_LIMIT,
      security_opt: [],
      state: "created",
      user: COLLIE_USER,
    });
  } catch (err) {
    log.warn("collie.envelope_refused", `collie-wake ${session}: post-create envelope inspection refused: ${
      err instanceof Error ? err.message : String(err)
    }`);
    await cleanup(containerId);
    return;
  }
  try {
    deps.collieRuntime.bind({
      container_id: containerId,
      docker_envelope: dockerEnvelope,
      envelope_digest,
      launch,
      runtime_generation,
      session,
    });
  } catch (err) {
    log.warn("collie.bind_refused", `collie-wake ${session}: runtime bind refused: ${err instanceof Error ? err.message : String(err)}`);
    await cleanup(containerId);
    return;
  }

  // 4. launch-bind: CAS the exact docker id onto the current launch BEFORE
  // start. A non-zero exit or fenced:false means this launch is stale (the
  // session ended, or a newer generation superseded it) — abandon the
  // container rather than start an orphan.
  const bind = await deps.runMc([
    "collie", "launch-bind", session, "--launch", launch, "--container-id", containerId,
  ]);
  if (bind.exitCode !== 0) {
    log.warn("collie.bind_refused", `collie-wake ${session}: launch-bind failed (exit ${bind.exitCode}): ${bind.stderr.trim()}`);
    await cleanup(containerId);
    return;
  }
  let bound: { fenced?: boolean } = {};
  try {
    bound = JSON.parse(bind.stdout) as { fenced?: boolean };
  } catch {
    log.warn("collie.bind_refused", `collie-wake ${session}: launch-bind returned unparseable receipt; abandoning`);
    await cleanup(containerId);
    return;
  }
  if (bound.fenced === false) {
    log.warn("collie.bind_refused", `collie-wake ${session}: launch ${launch} fenced at bind; abandoning container ${container_name}`);
    await cleanup(containerId);
    return;
  }

  // 4b. Republish run.json with the now-known container id BEFORE start. The
  // envelope is a RO file bind, so the container reads this updated content at
  // runtime; it is how the in-container runner learns its own 64-hex id to
  // report `mc collie runner-started` (the fence needs the exact bound id).
  const finalRunJsonBody =
    JSON.stringify({ ...runJson, container_id: containerId }, null, 2) + "\n";
  await deps.fs.writeFile(runJsonPath, finalRunJsonBody);
  try {
    deps.collieRuntime.sealArtifacts({
      artifact_envelope: JSON.stringify({
        provider_access: projection?.proxyConfig ?? null,
        run_json: finalRunJsonBody,
      }),
      container_id: containerId,
      envelope_digest,
      launch,
      runtime_generation,
      session,
    });
  } catch (err) {
    log.warn("collie.envelope_refused", `collie-wake ${session}: runtime artifact seal refused: ${
      err instanceof Error ? err.message : String(err)
    }`);
    await cleanup(containerId);
    return;
  }

  // 5. start — the runner reports runner-started from inside once it boots.
  const started = await deps.docker(["start", containerId]);
  if (started.exitCode !== 0) {
    log.warn("collie.start_failed", `collie-wake ${session}: docker start failed (exit ${started.exitCode}): ${started.stderr.trim()}`);
    return false;
  }
  deps.collieStarted?.(containerId);
  return true;
}

/** The Collie idle-end effector. The dispatch tick already flipped the session
 * to ended and deactivated its bindings; the resident's only remaining job is
 * to stop the container the launch was bound to, if any. A pre-bind idle end
 * (the session idled before its container ever started) carries no id and
 * stops nothing. A --rm container that already idled out from inside is gone;
 * a failed stop is logged, not retried (the inventory sweep is the backstop). */
async function collieStop(effect: CollieStopEffect, deps: TickDeps): Promise<void> {
  const log = deps.log.child({ session: effect.session });
  deps.credentials?.revoke?.(effect.session);
  await removeProviderAccessFiles(effect.session, deps);
  if (effect.container_id === undefined) {
    log.debug("collie.stop_skipped", `collie-stop ${effect.session}: no container bound; nothing to stop`);
    return;
  }
  const stopped = await deps.docker(["stop", effect.container_id]);
  if (stopped.exitCode !== 0) {
    log.warn("collie.stop_failed", `collie-stop ${effect.session}: docker stop exited ${stopped.exitCode}: ${stopped.stderr.trim()}`);
  } else {
    deps.collieRuntime.revokeContainer(effect.session, effect.container_id);
  }
}

/** Exact-created adoption is intentionally a start-only lane. The helper
 * echoes the current inventory proof; the resident re-inspects the exact id,
 * recomputes the immutable Docker envelope, rechecks its boot-local row, and
 * starts only if the proof is still byte-identical. */
async function collieAdopt(effect: CollieAdoptEffect, deps: TickDeps): Promise<void> {
  if (
    !/^h-[a-z0-9-]+$/.test(effect.session) ||
    !/^[0-9a-f]{16}$/.test(effect.launch) ||
    !/^[0-9a-f]{64}$/.test(effect.container_id) ||
    effect.container_name !== `mc-collie-${effect.session}` ||
    !/^[0-9a-f]{64}$/.test(effect.envelope_digest) ||
    !/^[0-9a-f]{64}$/.test(effect.runtime_generation)
  ) {
    deps.log.warn("collie.adopt_refused", "collie-adopt: malformed identity or proof; refusing");
    return;
  }
  const inspected = await deps.docker([
    "inspect", "--type", "container", effect.container_id,
  ]);
  try {
    if (inspected.exitCode !== 0) {
      throw new Error(`inspect exited ${inspected.exitCode}: ${inspected.stderr.trim()}`);
    }
    const records = JSON.parse(inspected.stdout) as unknown;
    if (!Array.isArray(records) || records.length !== 1) {
      throw new Error("inspect did not return exactly one container");
    }
    const record = records[0] as {
      Id?: unknown;
      Name?: unknown;
      Config?: { Labels?: unknown };
      HostConfig?: { Binds?: unknown };
      State?: { Status?: unknown };
    };
    const labels = record.Config?.Labels;
    if (labels === null || typeof labels !== "object" || Array.isArray(labels)) {
      throw new Error("container labels are absent");
    }
    const labelMap = labels as Record<string, unknown>;
    const mcLabels = Object.keys(labelMap).filter((key) => key.startsWith("mc-")).sort();
    if (
      record.Id !== effect.container_id ||
      record.Name !== `/${effect.container_name}` ||
      record.State?.Status !== "created" ||
      labelMap["mc-managed"] !== "true" ||
      labelMap["mc-deployment"] !== deploymentIdentity(deps.config.mcHome) ||
      labelMap["mc-tier"] !== "collie" ||
      labelMap["mc-session-id"] !== effect.session ||
      labelMap["mc-launch-id"] !== effect.launch ||
      labelMap["mc-runtime-generation"] !== effect.runtime_generation ||
      JSON.stringify(mcLabels) !== JSON.stringify([
        "mc-deployment", "mc-envelope-digest", "mc-launch-id", "mc-managed",
        "mc-runtime-generation", "mc-session-id", "mc-tier",
      ])
    ) {
      throw new Error("created container identity or state drifted");
    }
    const planDigest = labelMap["mc-envelope-digest"];
    if (typeof planDigest !== "string" || !/^[0-9a-f]{64}$/.test(planDigest)) {
      throw new Error("created container plan digest is malformed");
    }
    const binds = record.HostConfig?.Binds;
    if (!Array.isArray(binds) || binds.some((bind) => typeof bind !== "string")) {
      throw new Error("created container bind table is malformed");
    }
    const authSuffix = ":/mc/provider-access.json:ro";
    const authBinds = (binds as string[]).filter((bind) => bind.endsWith(authSuffix));
    if (authBinds.length > 1) {
      throw new Error("The container has duplicate provider access files.");
    }
    let providerAccess: string | null = null;
    if (authBinds.length === 1) {
      if (deps.credentials?.has?.(effect.session, effect.launch) !== true) {
        throw new Error("The provider access expired. The container requires a new launch.");
      }
      const expectedAuthPath =
        providerAccessHostPath(deps.config.mcHome, effect.session, effect.launch);
      if (authBinds[0] !== `${expectedAuthPath}${authSuffix}`) {
        throw new Error("The provider access file path changed.");
      }
      providerAccess = await deps.fs.readFile(expectedAuthPath);
    }
    const artifactEnvelope = JSON.stringify({
      provider_access: providerAccess,
      run_json: await deps.fs.readFile(runJsonHostPath(deps.config.mcHome, effect.session)),
    });
    const proof = deps.collieRuntime.attestEnvelope({
      artifact_envelope: artifactEnvelope,
      container_id: effect.container_id,
      docker_envelope: canonicalCollieDockerEnvelope(records[0]),
      envelope_digest: planDigest,
      launch: effect.launch,
      runtime_generation: effect.runtime_generation,
      session: effect.session,
    });
    if (proof === null ||
        proof.envelope_digest !== effect.envelope_digest ||
        proof.runtime_generation !== effect.runtime_generation) {
      throw new Error("current runtime envelope does not match the dispatched proof");
    }
  } catch (err) {
    deps.log.warn("collie.adopt_refused", `collie-adopt ${effect.session}/${effect.launch}: ${
      err instanceof Error ? err.message : String(err)
    }; refusing start`);
    return;
  }
  const started = await deps.docker(["start", effect.container_id]);
  if (started.exitCode !== 0) {
    deps.log.warn("collie.start_failed",
      `collie-adopt ${effect.session}/${effect.launch}: docker start failed ` +
      `(exit ${started.exitCode}): ${started.stderr.trim()}`,
    );
    return;
  }
  deps.collieStarted?.(effect.container_id);
}

/** Exact orphan/reconciliation cleanup selected by the helper from the
 * resident-owned inventory. The resident revalidates the closed identity and
 * reason grammar, then removes only the observed Docker id; a name is never
 * used as authority. */
async function collieRemove(effect: CollieRemoveEffect, deps: TickDeps): Promise<void> {
  if (
    !/^h-[a-z0-9-]+$/.test(effect.session) ||
    !/^[0-9a-f]{16}$/.test(effect.launch) ||
    !/^[0-9a-f]{64}$/.test(effect.container_id) ||
    effect.container_name !== `mc-collie-${effect.session}` ||
    !["orphan", "paused", "restarting", "removing", "exited", "dead", "starting-timeout"].includes(effect.reason)
  ) {
    deps.log.warn("collie.remove_refused", `collie-remove: malformed identity; refusing`);
    return;
  }
  deps.credentials?.revoke?.(effect.session, effect.launch);
  await deps.fs.rm(providerAccessHostPath(deps.config.mcHome, effect.session, effect.launch));
  const removed = await deps.docker(["rm", "-f", effect.container_id]);
  if (removed.exitCode !== 0) {
    deps.log.warn("collie.remove_failed",
      `collie-remove ${effect.session}/${effect.launch}: docker rm exited ${removed.exitCode}: ${removed.stderr.trim()}`,
    );
  } else {
    deps.collieRuntime.revokeObserved(effect.session, effect.launch, effect.container_id);
  }
}

/** A cause that the connection state records. It charges no task allowance. */
type SpawnDiagnosis = { cause: "credentials" | "connection"; evidence: string; operator_action: string };

/** The mc program attributes this observation through the run: to the task of
 * the run, or to its wiki update. */
async function reportInfrastructureFailure(
  effect: Pick<SpawnEffect, "run_id" | "subject_id">,
  condition: string,
  deps: TickDeps,
  diagnosis?: SpawnDiagnosis,
): Promise<void> {
  try {
    const res = await deps.runMc([
      "run", "infra-failure", effect.run_id,
      "--condition", Array.from(condition).slice(0, 2048).join(""),
      "--source", "resident", "--cause", diagnosis?.cause ?? "unknown",
      ...(diagnosis === undefined ? [] : [
        "--evidence", Array.from(diagnosis.evidence).slice(0, 8192).join(""),
        "--operator-action", Array.from(diagnosis.operator_action).slice(0, 2048).join(""),
      ]),
    ]);
    if (res.exitCode !== 0) {
      deps.log.warn("spawn.gate_failed",
        `run ${effect.run_id}: infrastructure report failed (exit ${res.exitCode}): ${res.stderr.trim()}`);
    }
  } catch (err) {
    deps.log.warn("spawn.gate_failed",
      `run ${effect.run_id}: infrastructure report failed: ${String(err)}`);
  }
}

/** Preparation completes its cleanup before the resident reports an execution failure. */
async function spawn(effect: SpawnEffect, deps: TickDeps): Promise<void> {
  let condition: string | undefined;
  let diagnosis: SpawnDiagnosis | undefined;
  try {
    await spawnAttempt(effect, deps, (observed, confirmed) => {
      condition = observed;
      diagnosis = confirmed;
    });
  } catch (err) {
    condition = `run preparation failed: ${err instanceof Error ? err.message : String(err)}`;
    diagnosis = undefined;
    throw err;
  } finally {
    if (condition !== undefined) await reportInfrastructureFailure(effect, condition, deps, diagnosis);
  }
}

async function spawnAttempt(
  effect: SpawnEffect,
  deps: TickDeps,
  observeFailure: (condition: string, diagnosis?: SpawnDiagnosis) => void,
): Promise<void> {
  const { config } = deps;
  const { run_id, role } = effect;
  const log = deps.log.child({ run_id, role });
  const refuse = (
    event: Parameters<TickDeps["log"]["warn"]>[0],
    condition: string,
    diagnosis?: SpawnDiagnosis,
  ) => {
    log.warn(event, condition);
    observeFailure(condition, diagnosis);
  };
	if (!SAFE_ID.test(run_id)) {
    refuse("spawn.refused", `spawn refused: run_id ${JSON.stringify(run_id)} fails the container-name charset`);
    return;
  }
  // Every run is bound to one Project:
  // the id names the container and the operator skill store, so a spawn that
  // names none, or one outside the id pattern, is not effected.
  if (typeof effect.project !== "string" || !PROJECT_ID.test(effect.project)) {
    refuse("spawn.refused", `spawn refused: project ${JSON.stringify(effect.project)} fails the Project id pattern (fail-closed)`);
    return;
  }
  if (typeof effect.skill_store_dir !== "string") {
    refuse("spawn.refused", "spawn refused: dispatch effect carries no operator skill store directory (fail-closed)");
    return;
  }
	if (typeof effect.brief !== "string" || effect.brief.length === 0) {
		refuse("spawn.refused", "spawn refused: dispatch effect carries no immutable brief (fail-closed)");
		return;
	}
  if (effect.git_identity_override === undefined) {
    refuse("spawn.refused", "spawn refused: dispatch effect carries no Git identity override decision (fail-closed)");
    return;
  }
  let gitEnvironment: string[];
  try {
    const identity = effect.git_identity_override;
    gitEnvironment = configuredGitEnvironment(
      identity === null ? config.gitAuthorName : identity.author_name,
      identity === null ? config.gitAuthorEmail : identity.author_email,
    );
  } catch (err) {
    refuse("spawn.refused", `spawn refused: ${err instanceof Error ? err.message : String(err)} (fail-closed)`);
    return;
  }
  const planRefusal = invalidMountPlanReason(effect.mount_plan);
  if (planRefusal !== null) {
    refuse("spawn.refused", `spawn refused: ${planRefusal} (fail-closed)`);
    return;
  }
  if (config.runtimeVolume && effect.mount_plan.tool_home === undefined) {
    refuse("spawn.refused", "The pipeline run requires its attested Project home.");
    return;
  }
	// Proposal runs read the Project tree without a task subject.
	const seedingRole = (role === "strategist(propose)" || role === "arbiter") && effect.subject_id === null;
	if (effect.mount_plan.seeding_projections !== undefined && !seedingRole) {
		refuse("spawn.refused", "spawn refused: seeding projections do not match a role that reads the committed tree (fail-closed)");
		return;
	}
  const behavior = config.roleBehaviors[role] ??
    config.roleBehaviors[role.replace(/\(.+\)$/, "")];
	const seedingProjectionBase = effect.mount_plan.seeding_projections === undefined
		? undefined
		: `${runsDir(config.mcHome)}/seeding/${run_id}`;
	const seedingProjectionRoots = (effect.mount_plan.seeding_projections ?? []).map((step) => ({
		step,
		root: `${seedingProjectionBase}/${step.project_id}`,
	}));
	let activeSeedingEnvelope: string | undefined;
	// config.toml owns the route catalog; the dispatch attest already resolved
	// this route against it and carried the result here. The resident keeps no
	// second opinion, only its own fact: which adapters the image ships.
	// agentRunnerRoutes is retained solely for explicit fake-adapter stand-ins
	// in boundary acceptance. Nothing is written before this settles.
	const routeKey = `${effect.harness}/${effect.model_binding}`;
	const runtime = resolvedRuntime(effect.runtime_binding);
	if (runtime === null) {
		refuse("spawn.refused", `spawn refused: no resolved runtime capability for ${JSON.stringify(routeKey)} (fail-closed)`);
		return;
	}
	if (runtime.harness !== effect.harness || runtime.id !== effect.model_binding) {
		refuse("spawn.refused",
			`spawn refused: resolved capability ${JSON.stringify(`${runtime.harness}/${runtime.id}`)} ` +
			`does not describe route ${JSON.stringify(routeKey)} (fail-closed)`,
		);
		return;
	}
	const isFakeFamily = runtime.harness === "fake" && runtime.id === "fake";
	const usesFakeAdapter = isFakeFamily || (config.agentRunnerRoutes?.includes(routeKey) ?? false);
	if (!usesFakeAdapter && !ADAPTER_HARNESS_FAMILIES.has(runtime.harness)) {
		refuse("spawn.refused", `spawn refused: this image ships no ${JSON.stringify(runtime.harness)} adapter (fail-closed)`);
		return;
	}
	if (usesFakeAdapter && behavior === undefined) {
		refuse("spawn.refused", `spawn refused: no behavior mapped for role ${JSON.stringify(role)} using fake adapter (fail-closed)`);
		return;
	}

	// Resolve the credential projection before any launch file exists. A
	// refused projection ends the run with a failure report, and mc decides
	// the next start; the run never launches without its credential. The fake
	// family is deterministic and token-free, so it keeps --network none as
	// spawn-side hygiene; every real agent route gets the open network by
	// requirement, with the projector as its only credential material.
    let expiresAt = Date.now() + 60_000;
    if (runtime.harness !== "fake") {
      const admission = await deps.runMc(["run", "launch-check", effect.run_id]);
      if (admission.exitCode !== 0) {
        await reportInfrastructureFailure(effect, "The connection changed or became unavailable before launch.", deps, {
          cause: "connection", evidence: admission.stderr.trim() || "The launch check refused the run.", operator_action: "",
        });
        return;
      }
      let deadline: unknown;
      try { deadline = JSON.parse(admission.stdout).expires_at; } catch {}
      const parsed = typeof deadline === "string" ? Date.parse(deadline) : NaN;
      if (Number.isFinite(parsed)) expiresAt = parsed;
      else if (!usesFakeAdapter) {
        refuse("spawn.refused", "The run admission has no deadline.");
        return;
      }
    }
	const projection = isFakeFamily
		? null
		: deps.credentials?.project(runtime, { runId: run_id, launchId: run_id, expiresAt,
      model: runtime.model, profileRevision: runtime.profile_revision }) ?? {
			refused: "runtime credential projector unavailable",
		};
	if (projection !== null && "refused" in projection) {
    refuse("spawn.refused", `spawn refused: credential projection unavailable for ${routeKey}: ${projection.refused}`,
      "cause" in projection && projection.cause === "credentials" ? projection : undefined);
		return;
	}
  if (projection !== null) {
    try {
      if (Object.keys(projection.env).length || typeof projection.proxyConfig !== "string") throw new Error("The provider access configuration is absent.");
      parseProviderAccess(projection.proxyConfig, runtime.harness as "codex" | "claude-sdk" | "opencode");
    } catch {
      throw new Error("The provider access configuration is invalid.");
    }
  }
  const providerAccessPath = projection?.proxyConfig === undefined ? undefined
    : providerAccessHostPath(config.mcHome, run_id, run_id);

  // 1. folder: the trace-only session folder plus the sibling envelope dir.
  // run.json must live outside the session folder so the RW session mount
  // never aliases the RO /mc/run.json mount.
  const sessionDir = `${config.mcHome}/sessions/${run_id}`;
  await deps.fs.mkdir(sessionDir);
  await deps.fs.mkdir(runsDir(config.mcHome));
  // A role without a repository plan reads no workspace, so nothing binds
  // /workspace/source, and the image ships no such directory. Both adapters
  // put the envelope's workspace straight into the harness `cwd`, and a spawn
  // against a missing cwd fails instantly. Those runs receive a real, empty,
  // run-scoped scratch directory as cwd instead.
  const repositoryInput = effect.mount_plan.repository
    ? await prepareRepositoryRun(effect.mount_plan.repository, run_id, deps) : undefined;
  let environment = effect.environment;
  let environmentSecrets: Record<string, string> = {};
  if (config.runtimeVolume) {
    const selection = await deps.runMc(["environment", "resolve"], new TextEncoder().encode(JSON.stringify({
      project_id: effect.project, ...(repositoryInput ? { run_id, source_commit: repositoryInput.input_commit } : {}),
      runtime_identity: config.releaseBuildId ?? "development",
    })));
    if (selection.exitCode !== 0) throw new Error("The Project environment is unavailable. Open its Environment page to repair it.");
    environment = JSON.parse(selection.stdout);
    if (!environment || !/^sha256:[0-9a-f]{64}$/.test(environment.image_digest) || !environment.configuration) {
      throw new Error("The Project environment has no checked image selection.");
    }
    const image = await deps.docker(["image", "inspect", environment.image_digest]);
    if (image.exitCode !== 0) throw new Error("The selected Project image is unavailable. Open its Environment page to repair it.");
    environmentSecrets = await projectSecretValues(environment.configuration, effect.project, deps, true);
  }
  const bindsRepository = repositoryInput !== undefined;
  const scratchDir = bindsRepository
    ? undefined
    : `${runsDir(config.mcHome)}/scratch/${run_id}`;
  if (scratchDir !== undefined) await deps.fs.mkdir(scratchDir);

  // Runner diagnostics, bound rather than captured. Every run container is
  // `--rm` and nothing reads its output; two correctness predicates read
  // container absence (the liveness observation of each active agent run, the
  // producer-absence fence).
  //
  // This is the stderr class only: a few KB of structured records per run.
  // Container STDOUT is the harness event stream and stays uncaptured;
  // sessions/<run_id>/native.jsonl already holds it.
  //
  // The leaf is 0777 and that is deliberate. Every run container runs as
  // `--user 10002:10002`, so the leaf must accept writes from that uid. It is
  // safe because the parent
  // `logs/` is 0700 operator-owned, so no other host uid can traverse to it,
  // and the container reaches the inode through the bind.
  const runsLogDir = `${config.mcHome}/logs/runs/${run_id}`;
  await deps.fs.mkdir(runsLogDir, { mode: 0o777 });

  // The envelope names each destination that the `-v` set below binds
  // writable. mc reads the list to tell a durable artifact plane from the
  // container's own filesystem. Deriving it here, from the same entries,
  // keeps the list and the binds in step. Read-only rows stay out.
  const writableDestinations = [
    ...effect.mount_plan.entries
      .filter((entry) => entry.access === "rw")
      .map((entry) => entry.destination),
    ...(scratchDir === undefined ? [] : ["/mc/scratch"]),
    "/mc/logs",
  ];

  // 2. run.json. Host source paths stay out of the agent-visible envelope;
  // they live only in the host-side plan sibling.
  // The run's skill set: the role's shipped skills from the effect, then
  // every operator skill in the store directory the effect names. Read now, so the container holds the set that
  // existed when it started. A Project registered from inside a Collie
  // container has no store directories yet; they are created here, once.
  await ensureProjectSkillStore(config.mcHome, effect.project, deps.fs);
  const runSkills = await runSkillSet(effect, config.mcHome, deps.fs);
  const runJson = {
    run_id,
    tier: "pipeline",
    role,
    subject_id: effect.subject_id ?? null,
    project: effect.project,
    harness: effect.harness,
    model_binding: effect.model_binding,
    runtime: runtimeEnvelope(runtime),
    mode: "fresh",
		brief: effect.brief,
    // Launch-envelope material, not brief content: the runner installs each
    // skill into the harness's skill roots before the harness starts, and the
    // harness discovers them there rather than being handed them as prompt
    // text. Omitted when there are none — the runner treats absence as "no
    // skills this run". `skill_problems` is what the store could not deliver;
    // the runner writes each into the run's log and proceeds.
    ...runSkills,
    pool_ids: effect.pool_ids ?? [],
    heartbeat_interval_s: effect.heartbeat_interval_s,
    ...(environment ? { environment } : {}),
    ...(behavior === undefined ? {} : { harness_config: { behavior } }),
    mounts: {
      session: "/mc/session",
      workspace: scratchDir === undefined ? "/workspace/source" : "/mc/scratch",
      allow_write: writableDestinations,
    },
  };
  const runJsonPath = runJsonHostPath(config.mcHome, run_id);
  if (repositoryInput !== undefined) {
    // The runner reads `repository.input_commit`: its first act in the
    // isolated repository is `git reset --hard <input_commit> && git clean -fdx`.
    // The host plan, its workspace root included, stays out of the envelope.
    Object.assign(runJson, { repository: { input_commit: repositoryInput.input_commit } });
    runJson.mounts.allow_write.push(...(role === "scribe" ? ["/workspace/source/openwiki", "/workspace/source/.git"] : ["/workspace/source"]));
  }
  await deps.fs.writeFile(runJsonPath, JSON.stringify(runJson, null, 2) + "\n");
  const planPath = mountPlanHostPath(config.mcHome, run_id);
  await deps.fs.writeFile(planPath, JSON.stringify(effect.mount_plan) + "\n", { mode: 0o600 });
  let launchImage = environment?.image_digest ?? config.image;
  if (role === "packager") {
    try {
      const capture = await deps.runMc(["evaluation", "start"], new TextEncoder().encode(JSON.stringify({
        run_id, image: launchImage, release: config.releaseBuildId ?? "",
      })));
      if (capture.exitCode !== 0) log.warn("evaluation.capture_failed", capture.stderr.trim());
      else {
        const result = JSON.parse(capture.stdout || "{}");
        if (result.status === "failed") log.warn("evaluation.capture_failed", String(result.error));
        const image = result.runner_image;
        if (typeof image === "string" && /^sha256:[0-9a-f]{64}$/.test(image)) launchImage = image;
      }
    } catch (error) { log.warn("evaluation.capture_failed", String(error)); }
  }

  if (providerAccessPath !== undefined) {
    await deps.fs.mkdir(`${runsDir(config.mcHome)}/provider-access`, { mode: 0o700 });
    await deps.fs.writeFile(providerAccessPath, projection!.proxyConfig!, { mode: 0o644 });
  }
  // Same discipline as `reap`, and for a sharper reason: every caller below is
  // an ABORT path. A removal that throws here would abort the abort, leaving
  // the spawn half-unwound and taking the tick with it.
  const discardLaunch = discarder(deps, `spawn ${run_id}`);
  const removeLaunchFiles = async () => {
    deps.credentials?.revoke?.(run_id, run_id);
    await discardLaunch(runJsonPath);
    await discardLaunch(planPath);
		if (providerAccessPath !== undefined) await discardLaunch(providerAccessPath);
		if (activeSeedingEnvelope !== undefined) await discardLaunch(activeSeedingEnvelope);
		if (seedingProjectionBase !== undefined) await discardLaunch(seedingProjectionBase, { recursive: true });
		if (scratchDir !== undefined) await discardLaunch(scratchDir, { recursive: true });
  };
  const recheck = async (leg: string): Promise<boolean> => {
    if (effect.mount_plan.entries.length === 0 &&
			effect.mount_plan.seeding_projections === undefined &&
        effect.mount_plan.tool_home === undefined) return true; // nothing can drift
    const res = await deps.runMc(["__mount-recheck", planPath]);
    if (res.exitCode !== 0) {
      refuse("spawn.recheck_refused", `spawn ${run_id}: mount recheck refused ${leg} (exit ${res.exitCode}): ${res.stderr.trim()}`);
      return false;
    }
    return true;
  };

  // 3. container: recheck → create → recheck → start. Ordinary binds come
  // only from the committed plan.
  if (!(await recheck("before create"))) {
    await removeLaunchFiles();
    return;
  }
	if (seedingProjectionBase !== undefined) {
		try {
			await deps.fs.mkdir(`${runsDir(config.mcHome)}/seeding`);
			await deps.fs.mkdir(seedingProjectionBase, { exclusive: true });
			for (let i = 0; i < seedingProjectionRoots.length; i += 1) {
				const { step, root } = seedingProjectionRoots[i]!;
				await deps.fs.mkdir(root, { exclusive: true });
				const envelopePath = `${runsDir(config.mcHome)}/${run_id}.seeding-${i}.json`;
				activeSeedingEnvelope = envelopePath;
				const envelope = {
					object_format: step.object_format,
					operation: "seeding-committed-tree",
					projection_root: "/repo/projection",
					schema_version: 1,
					source_root: "/repo/source",
					target_ref: step.target_ref,
				};
				await deps.fs.writeFile(envelopePath, JSON.stringify(envelope) + "\n", { mode: 0o644 });
				const setup = await deps.docker([
					"run", "--rm", "--name", `mc-setup-${run_id}`, "--network", "none",
					"--label", "mc-managed=true", ...deploymentArguments(config.mcHome), "--label", "mc-tier=pipeline", "--label", `mc-run-id=${run_id}`,
					"--label", `mc-project=${effect.project}`,
					"--user", "10002:10002", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
					"--cpus", "1", "--memory", "1024m", "--pids-limit", "128",
					"-v", `${step.workspace_root.canonical}:/repo/source:ro`,
					"-v", `${root}:/repo/projection`, "-v", `${envelopePath}:/mc/setup.json:ro`,
					...zoneEnv(deps),
					config.image, "mc", "__setup-seeding-projection", "/mc/setup.json",
				]);
				if (setup.exitCode !== 0) throw new Error(`projection ${step.project_id} setup exited ${setup.exitCode}`);
				await deps.fs.rm(envelopePath);
				activeSeedingEnvelope = undefined;
			}
		} catch (err) {
			refuse("spawn.projection_failed", `spawn ${run_id}: seeding projection setup failed: ${(err as Error).message} (fail-closed)`);
			await removeLaunchFiles();
			return;
		}
	}
  // The container identity is per Project.
  const name = pipelineContainerName(effect.project, run_id);
  const planBinds = effect.mount_plan.entries
    .flatMap((entry) => [
      "-v", `${entry.source}:${entry.destination}${entry.access === "ro" ? ":ro" : ""}`,
    ]);
  if (effect.mount_plan.repository !== undefined && repositoryInput !== undefined) {
    const root = `${repositoryRunRoot(deps, run_id, subjectFields(effect.mount_plan.repository))}/repo`;
    planBinds.push("-v", `${root}:/workspace/source${role === "scribe" ? ":ro" : ""}`);
    if (role === "scribe") {
      planBinds.push("-v", `${root}/openwiki:/workspace/source/openwiki`);
      planBinds.push("-v", `${root}/.git:/workspace/source/.git`);
    }
  }
	for (const { step, root } of seedingProjectionRoots) {
		const destination = `/workspace/seeding/${step.project_id}/source`;
		planBinds.push("-v", `${root}:${destination}:ro`);
		planBinds.push("-v", `${root}/.git:${destination}/.git:ro`);
	}
  const created = await deps.docker([
    "create", "--rm", ...(environment ? ["--interactive"] : []),
    ...projectExecutionArguments(isFakeFamily, deps.hostZone(), scratchDir === undefined ? "/workspace/source" : "/mc/scratch"),
    "--name", name,
    "--label", "mc-managed=true", ...deploymentArguments(config.mcHome),
    "--label", "mc-tier=pipeline",
		"--label", `mc-run-id=${run_id}`,
		"--label", `mc-project=${effect.project}`,
    ...(config.runtimeVolume ? runtimeArguments(deps) : []),
    ...openCodeArguments(config.mcHome, runtime?.harness),
    ...(config.runtimeVolume ? ["--entrypoint", `${CULLER_RUNTIME}/bin/bun`] : []),
    ...(environment ? projectCacheArguments(config.mcHome, effect.project, environment.image_digest) : []),
    "-v", `${sessionDir}:/mc/session`,
    "-v", `${runJsonPath}:/mc/run.json:ro`,
    ...(scratchDir === undefined ? [] : ["-v", `${scratchDir}:/mc/scratch`]),
    "-v", `${runsLogDir}:/mc/logs`,
    "-v", `${config.behaviorsDir}:/mc/behaviors:ro`,
    ...(!config.runtimeVolume ? ["-v", `${config.runnerSrcDir}:/app/src:ro`] : []),
    ...(effect.mount_plan.tool_home === undefined
      ? ["-e", "HOME=/home/mc-model"]
      : projectHomeArguments(effect.mount_plan.tool_home.canonical)),
    ...planBinds,
    ...(providerAccessPath === undefined ? [] : [
      "-v", `${providerAccessPath}:/mc/provider-access.json:ro`,
      "-e", "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json",
    ]),
    "-v", `${config.spineVolume}:${posix.dirname(config.spineDbPath)}`,
    "-e", `MC_SPINE=${config.spineDbPath}`,
    ...logLevelEnv(),
    // Identity is a dispatch decision, not an ambient credential or adapter
    // default. Emit it last so no earlier projected environment can replace it.
    ...gitEnvironment,
    // Authorize the in-container fake adapter to stand in for the same non-fake
    // routes the resident's own launch gate accepts (fail-closed: absent ⇒
    // fake-only inside the container too).
    ...(config.agentRunnerRoutes && config.agentRunnerRoutes.length > 0
      ? ["-e", `MC_AGENT_RUNNER_ROUTES=${config.agentRunnerRoutes.join(",")}`]
      : []),
    launchImage,
    ...(config.runtimeVolume ? config.agentCmd.slice(1) : config.agentCmd),
  ]);
  if (created.exitCode !== 0) {
    refuse("spawn.create_failed", `spawn ${run_id}: docker create failed (exit ${created.exitCode}): ${created.stderr.trim()}`);
    await removeLaunchFiles();
    return;
  }
  if (!(await recheck("after create"))) {
    // Drift inside the create window: remove the confirmed-unstarted
    // container before its launch inputs.
    const removed = await deps.docker(["rm", name]);
    if (removed.exitCode !== 0) {
      log.warn("cleanup.failed", `spawn ${run_id}: docker rm of the unstarted container exited ${removed.exitCode}: ${removed.stderr.trim()}`);
    }
    await removeLaunchFiles();
    return;
  }
  if (environment) {
    const attachment = deps.docker(["start", "--interactive", name], new TextEncoder().encode(JSON.stringify(environmentSecrets) + "\n"));
    void attachment.then(async (result) => {
      deps.credentials?.revoke?.(run_id, run_id);
      if (providerAccessPath !== undefined) await discardLaunch(providerAccessPath);
      if (result.exitCode !== 0) await reportInfrastructureFailure(effect, "The Project container input connection failed.", deps);
    }).catch(() => {
      deps.credentials?.revoke?.(run_id, run_id);
      if (providerAccessPath !== undefined) void discardLaunch(providerAccessPath);
      log.warn("spawn.start_failed", "The Project container input connection failed.", { run_id });
    });
    return;
  }
  const started = await deps.docker(["start", name]);
  if (started.exitCode !== 0) {
    refuse("spawn.start_failed", `spawn ${run_id}: docker start failed (exit ${started.exitCode}): ${started.stderr.trim()}`);
    try {
      const removed = await deps.docker(["rm", "-f", name]);
      if (removed.exitCode !== 0) {
        log.warn("cleanup.failed", `spawn ${run_id}: container removal failed (exit ${removed.exitCode}): ${removed.stderr.trim()}`);
        return;
      }
      await removeLaunchFiles();
    } catch (err) {
      log.warn("cleanup.failed", `spawn ${run_id}: container removal failed: ${String(err)}`);
    }
  }
}

/** Backoff between discard attempts, and by its length the retry budget: one
 * initial attempt plus one per entry. Short and bounded on purpose: this runs
 * inside a tick, and a tick that spends its time on residue is a tick that is
 * not dispatching.
 *
 * Every path the discarder deletes is derived from one charset-checked run id,
 * so a failed removal costs disk and nothing else, whereas a thrown error would
 * unwind the whole tick. The removal can reach a tree the VM has not finished
 * unmounting; that lands as EACCES, which is transient, and Node's own retry
 * does not cover it. The retry is not conditioned on the error code. */
export const DISCARD_RETRY_MS = [100, 400] as const;

// Deliberately narrower than TickDeps: the removal budget is reused by the
// run-log prune, which is a tick chore rather than an effector and holds
// no mount/spawn seams. Asking only for what it uses keeps that caller honest
// instead of casting a stub into the full bundle.
// logLevelEnv carries the operator's level into every container, so a
// deployment raised to debug is raised everywhere rather than only on the host.
//
// It is one more `-e` on both create argvs and deliberately NOT a run.json
// field: that would be an envelope change on both tiers. The Collie envelope
// digest already covers `-e` rows, so create and attest stay consistent for
// free. Absent means absent — the container's own parseLevel then chooses the
// default, rather than this side baking one in.
function logLevelEnv(): string[] {
  const level = process.env["MC_LOG_LEVEL"];
  return level === undefined || level === "" ? [] : ["-e", `MC_LOG_LEVEL=${level}`];
}

// zoneEnv carries the zone of the Mac into every container, so an agent and
// the operator agree on the hour and the date. Read at each creation, like
// the level. A host that names no zone adds nothing.
function zoneEnv(deps: Pick<TickDeps, "hostZone">): string[] {
  const zone = deps.hostZone();
  return zone === undefined || zone === "" ? [] : ["-e", `TZ=${zone}`];
}

export function discarder(
  deps: { fs: Pick<TickDeps["fs"], "rm">; sleep: TickDeps["sleep"]; log: TickDeps["log"] },
  label: string,
): (path: string, opts?: { recursive?: boolean }) => Promise<void> {
  return async (path, opts) => {
    for (let attempt = 0; ; attempt++) {
      try {
        await deps.fs.rm(path, opts);
        return;
      } catch (err) {
        if (attempt < DISCARD_RETRY_MS.length) {
          await deps.sleep(DISCARD_RETRY_MS[attempt]!);
          continue;
        }
        // Budget spent. One line, naming the run and the path, because this is
        // now residue an operator has to find some other way.
        deps.log.warn("cleanup.failed", `${label}: could not remove ${path}: ${err instanceof Error ? err.message : String(err)}`);
        return;
      }
    }
  };
}

/** The resident confirms that both run containers stopped. mc then releases
 * the Project lease of the run, or records the stop of the wiki update run. */
async function reap(effect: ReapEffect, deps: TickDeps): Promise<void> {
  const log = deps.log.child({ run_id: effect.run_id });
  if (!effect.stop_container) {
    log.debug("reap.skipped", `reap ${effect.run_id}: stop_container not set; nothing to do`);
    return;
  }
  if (!SAFE_ID.test(effect.run_id)) {
    log.warn("reap.refused", `reap refused: run_id ${JSON.stringify(effect.run_id)} fails the container-name charset`);
    return;
  }
  if (typeof effect.project !== "string" || !PROJECT_ID.test(effect.project)) {
    log.warn("reap.refused", `reap refused: project ${JSON.stringify(effect.project)} fails the Project id pattern`);
    return;
  }
  deps.credentials?.revoke?.(effect.run_id, effect.run_id);
  let stopped = true;
  for (const name of [`mc-setup-${effect.run_id}`, pipelineContainerName(effect.project, effect.run_id)]) {
    try {
      const res = await deps.docker(["stop", name]);
      if (res.exitCode === 0) continue;
      log.warn("reap.stop_failed", `reap ${effect.run_id}: docker stop ${name} exited ${res.exitCode}: ${res.stderr.trim()}`);
      const status = await inspectPipelineRunContainer(name, effect.run_id, deps, "run container");
      if (status !== null && !["created", "exited", "dead"].includes(status)) {
        stopped = false;
        log.warn("reap.stop_failed", `reap ${effect.run_id}: container ${name} remains ${status}`);
      }
    } catch (err) {
      stopped = false;
      log.warn("reap.stop_failed", `reap ${effect.run_id}: cannot confirm that ${name} stopped: ${String(err)}`);
    }
  }
  if (!stopped) return;
  try {
    const acknowledged = await deps.runMc(["run", "stopped", effect.run_id]);
    if (acknowledged.exitCode !== 0) {
      log.warn("reap.refused", `reap ${effect.run_id}: stop acknowledgement failed (exit ${acknowledged.exitCode}): ${acknowledged.stderr.trim()}`);
      return;
    }
  } catch (err) {
    log.warn("reap.refused", `reap ${effect.run_id}: stop acknowledgement failed: ${String(err)}`);
    return;
  }
  // Launch files remain available until mc accepts the stop acknowledgement.
  const discard = discarder(deps, `reap ${effect.run_id}`);
  const accessPath = providerAccessHostPath(deps.config.mcHome, effect.run_id, effect.run_id);
  try { await deps.fs.readFile(accessPath); await discard(accessPath); } catch {}
  // The envelope and its plan sibling are ephemeral launch input: removed
  // with the container. Missing files are fine (force semantics).
  await discard(runJsonHostPath(deps.config.mcHome, effect.run_id));
  await discard(mountPlanHostPath(deps.config.mcHome, effect.run_id));
	await discard(`${runsDir(deps.config.mcHome)}/seeding/${effect.run_id}`, { recursive: true });
	// Same disposability for a plan-less run's scratch cwd: derived from this
	// exact safe run id, holds no task-store input, removed with the container.
	await discard(`${runsDir(deps.config.mcHome)}/scratch/${effect.run_id}`, { recursive: true });
	// `logs/runs/<run_id>` is DELIBERATELY not in this list. A reap follows a
	// lease timeout, a hard deadline, an absent container, an operator
	// interrupt, and each end of a wiki update run. Removing a run's diagnostics
	// here would destroy them for exactly the runs an operator comes looking
	// for, which is the whole reason they are captured. The tree is bounded
	// instead by the run-log prune chore (run-log-prune.ts), and that chore is
	// the price of this exemption.
}
