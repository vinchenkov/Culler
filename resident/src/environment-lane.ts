import { createEnvironmentHealth } from "./environment-health";
import { diagnosticRedactor } from "../../runner/agent-runner/diagnostics";
import { parseProviderAccess } from "../../runner/agent-runner/adapters/provider-access";
import { SetupError, setupError, setupProcessError, setupCommandError } from "../../runner/shared/setup-error";
import { createHash } from "node:crypto";
import { posix } from "node:path";
import type { TickDeps, RuntimeBinding, ExecResult } from "./types";
import type { ProjectEnvironment } from "../../runner/shared/project-environment";
import { configuredGitEnvironment } from "./git-identity";
import { composeRunSkills, ensureProjectSkillStore, readOperatorSkills } from "./skill-store";

export const CULLER_RUNTIME = "/opt/culler-runtime";
export const PROJECT_HOME = "/home/agent";
const DIGEST = /^sha256:[0-9a-f]{64}$/;
const REGISTRY_DIGEST = /^[a-zA-Z0-9._:/-]+@sha256:[0-9a-f]{64}$/;
const ID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;
const PROJECT_ID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/;
const encoder = new TextEncoder();
const ENVIRONMENT_ATTEMPT_TIMEOUT_MS = 30 * 60_000;
const runtimeChecks = new WeakMap<object, Map<string, Promise<void>>>();

export interface EnvironmentOperation {
  id: string;
  project_id: string;
  kind: "setup" | "build" | "validate" | "accept" | "cache-clear" | "runtime-check";
  image_digest: string;
  workspace_root: string;
  configuration: ProjectEnvironment;
  inputs_digest: string;
  revision_id: string;
  brief: string;
  skills: Array<{ name: string; body: string }>;
  runtime_binding?: RuntimeBinding;
  created_at?: string;
  metadata?: { setup_id?: string; proposal_operation_id?: string; denied_build_files?: Array<{ path: string; content: string }>; files_digest?: string; source_commit?: string; initial_setup?: boolean; result_id?: string;
    previous_operation_id?: string; validation_operation_id?: string; candidate?: boolean;
    candidate_source?: string; source_run_id?: string; removed_files?: Array<{ path: string; content: string }>;
    base_source?: { registry_reference: string; release_identity: string } };
}

type Dependencies = Pick<TickDeps, "runMc" | "docker" | "config" | "fs" | "log" | "credentials" | "hostZone"> & { redact?: (text: string) => string };

// Setup probes and pipeline commands require the same container policy.
export function projectExecutionArguments(fake: boolean, zone?: string, workspace = "/workspace/source"): string[] {
  return [
    "--init",
    "--user", "10002:10002", "--cap-drop", "ALL",
    "--pids-limit", "512", "--memory", "4g", "--cpus", "2",
    "--workdir", workspace,
    ...(fake ? ["--network", "none"] : []),
    ...(zone ? ["-e", `TZ=${zone}`] : []),
  ];
}

export function runtimeArguments(deps: Pick<Dependencies, "config">): string[] {
  const volume = deps.config.runtimeVolume;
  if (!volume || !/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(volume)) {
    throw new Error("The Culler runtime volume is unavailable.");
  }
  return ["-v", `${volume}:${CULLER_RUNTIME}:ro`,
    "-e", `PATH=${CULLER_RUNTIME}/commands:/usr/local/bin:/usr/bin:/bin`,
    "-e", `MC_PRIVATE_BUN=${CULLER_RUNTIME}/bin/bun`,
    "-e", `MC_PRIVATE_NODE=${CULLER_RUNTIME}/bin/node`,
    "-e", "LD_PRELOAD=", "-e", "LD_LIBRARY_PATH=", "-e", "NODE_OPTIONS=", "-e", "BUN_OPTIONS=",
    "-e", `MC_CODEX_ADAPTER=${CULLER_RUNTIME}/app/src/agent-runner/adapters/codex.ts`,
    "-e", `MC_CLAUDE_ADAPTER=${CULLER_RUNTIME}/app/src/agent-runner/adapters/claude.ts`,
    "-e", `MC_HARNESS_CLI=${CULLER_RUNTIME}/app/src/fake-harness/cli.ts`];
}

export function deploymentIdentity(home: string): string {
  const suffix = createHash("sha256").update("mc-deployment-v1\0" + home).digest("hex").slice(0, 12);
  return suffix;
}

export function deploymentArguments(home: string): string[] {
  return ["--label", `mc-deployment=${deploymentIdentity(home)}`];
}

export function openCodeVolume(home: string): string {
  const suffix = createHash("sha256").update("mc-deployment-v1\0" + home).digest("hex").slice(0, 12);
  return `mc-opencode-${suffix}`;
}

export function openCodeArguments(home: string, harness?: string): string[] {
  return harness === "opencode" ? ["--mount", `type=volume,src=${openCodeVolume(home)},dst=/opt/culler-opencode,readonly,volume-nocopy`,
    "--tmpfs", "/etc/opencode:ro,nosuid,nodev,noexec,mode=0555"] : [];
}

function namespace(home: string): string { return createHash("sha256").update(home).digest("hex").slice(0, 16); }

export function environmentContainerName(home: string, operation: string): string {
  if (!ID.test(operation)) throw new Error("The environment operation identity is invalid.");
  return `mc-environment-${namespace(home)}-${operation}`;
}

export function projectBaseReference(digest: string): string {
  if (!DIGEST.test(digest)) throw new Error("The Project base image has no exact digest.");
  return `culler-project-base:${digest.slice(7)}`;
}

export function projectCacheArguments(home: string, project: string, image: string): string[] {
  if (!PROJECT_ID.test(project) || !DIGEST.test(image)) throw new Error("The Project cache identity is invalid.");
  const key = createHash("sha256").update(`${project}\nlinux/arm64\nnpm-content-v2-sha512`).digest("hex").slice(0, 24);
  const volume = `mc-cache-${namespace(home)}-${key}`;
  return ["--mount", `type=volume,src=${volume},dst=/cache`,
    "-e", "CULLER_CACHE_LIMIT_BYTES=2147483648"];
}

export function projectHomeArguments(source: string, runState = true): string[] {
  if (!source.startsWith("/") || posix.normalize(source) !== source || /[:,\0\r\n]/.test(source)) {
    throw new Error("The Project home path is invalid.");
  }
  return ["--mount", `type=bind,src=${source},dst=${PROJECT_HOME}`, "-e", `HOME=${PROJECT_HOME}`,
    ...(runState ? [".agents", ".claude", ".codex"].flatMap(directory => [
      "--tmpfs", `${PROJECT_HOME}/${directory}:rw,uid=10002,gid=10002,mode=0700`,
    ]) : [])];
}

async function prepareProjectHome(project: string, deps: Dependencies): Promise<string> {
  if (!PROJECT_ID.test(project)) throw new Error("The Project home identity is invalid.");
  const result = await succeeded(deps.runMc(["__project-home", project]), "The Project home is unavailable");
  const home = JSON.parse(result.stdout);
  if (home.canonical !== posix.join(deps.config.mcHome, "state", "projects", project, "home")) {
    throw new Error("The Project home differs from its assigned path.");
  }
  return home.canonical;
}

export function safeBuildPath(path: string): boolean {
  return path !== "" && !path.startsWith("/") && !path.includes("\\") && !/[\0\r\n]/.test(path) &&
    path.split("/").every((part) => part !== "" && part !== "." && part !== ".." && part !== ".git") && path !== ".dockerignore";
}

async function command(deps: Dependencies, action: string, body: unknown): Promise<any> {
  const result = await deps.runMc(["environment", action], encoder.encode(JSON.stringify(body)));
  if (result.exitCode !== 0) throw setupCommandError(`The environment ${action} request failed.`, result);
  return JSON.parse(result.stdout);
}

function assertOperation(operation: EnvironmentOperation): void {
  if (!ID.test(operation.id) || !PROJECT_ID.test(operation.project_id) || !DIGEST.test(operation.image_digest) ||
      !["setup", "build", "validate", "accept", "cache-clear", "runtime-check"].includes(operation.kind) || !operation.inputs_digest ||
      !operation.workspace_root.startsWith("/") || /[:,\0\r\n]/.test(operation.workspace_root)) {
    throw new Error("The environment operation contains invalid launch inputs.");
  }
}

async function inspect(deps: Dependencies, kind: "container" | "image", name: string): Promise<any | undefined> {
  const result = await deps.docker([kind, "inspect", name]);
  if (result.exitCode === 0) {
    const rows = JSON.parse(result.stdout);
    if (!Array.isArray(rows) || rows.length !== 1) throw new Error("Docker returned an invalid inspection result.");
    return rows[0];
  }
  if (/No such (object|container|image)/i.test(result.stderr)) return undefined;
  throw setupCommandError(`Docker could not inspect the environment ${kind}.`, result);
}

async function succeeded(result: Promise<ExecResult>, message: string): Promise<ExecResult> {
  const value = await result;
  if (value.exitCode !== 0) throw setupCommandError(message, value);
  return value;
}

async function restoreBase(operation: EnvironmentOperation, deps: Dependencies): Promise<void> {
  const digest = operation.configuration.base_digest;
  if ((await inspect(deps, "image", digest))?.Id === digest) return;
  const source = operation.metadata?.base_source ??
    (await command(deps, "get", { project_id: operation.project_id })).base_source;
  const reference = source?.registry_reference;
  if (typeof reference !== "string" || !REGISTRY_DIGEST.test(reference)) {
    throw new Error("The recorded Project base is unavailable. Restore its saved Docker image, then retry in the Environment page.");
  }
  await succeeded(deps.docker(["pull", "--platform", "linux/arm64", reference]), "The recorded Project base could not be restored");
  if ((await inspect(deps, "image", reference))?.Id !== digest || (await inspect(deps, "image", digest))?.Id !== digest) {
    throw new Error("The restored image differs from the recorded Project base.");
  }
}

async function requireImages(image: string, operation: EnvironmentOperation, deps: Dependencies): Promise<void> {
  if (!DIGEST.test(image)) throw new Error("The candidate image has no exact digest.");
  await restoreBase(operation, deps);
  if ((await inspect(deps, "image", image))?.Id !== image) throw new Error("The selected Project image is unavailable.");
}

function runtimeCheckCache(deps: Dependencies): Map<string, Promise<void>> {
  let checks = runtimeChecks.get(deps);
  if (!checks) { checks = new Map(); runtimeChecks.set(deps, checks); }
  return checks;
}

function runtimeCheckKey(image: string, operation: EnvironmentOperation, deps: Dependencies): string {
  return `${operation.configuration.base_digest}:${image}:${deps.config.releaseBuildId}:${deps.config.runtimeVolume}`;
}

export async function checkRuntimeCompatibility(image: string, operation: EnvironmentOperation, deps: Dependencies): Promise<void> {
  await requireImages(image, operation, deps);
  const checks = runtimeCheckCache(deps);
  const openCode = await deps.docker(["volume", "inspect", openCodeVolume(deps.config.mcHome)]);
  const key = runtimeCheckKey(image, operation, deps) + (openCode.exitCode === 0 ? ":opencode" : "");
  const existing = checks.get(key);
  if (existing) return existing;
  const check = performRuntimeCompatibility(image, operation, deps, openCode.exitCode === 0);
  checks.set(key, check);
  try { await check; }
  catch (error) { checks.delete(key); throw error; }
}

/** The spine records a passed check of an image with a Culler runtime. The
 * same image and runtime need no second check after a resident restart. */
export async function confirmRecordedCompatibility(image: string, operation: EnvironmentOperation, deps: Dependencies): Promise<void> {
  await requireImages(image, operation, deps);
  const openCode = await deps.docker(["volume", "inspect", openCodeVolume(deps.config.mcHome)]);
  if (openCode.exitCode === 0) return checkRuntimeCompatibility(image, operation, deps);
  runtimeCheckCache(deps).set(runtimeCheckKey(image, operation, deps), Promise.resolve());
}

async function performRuntimeCompatibility(image: string, operation: EnvironmentOperation, deps: Dependencies, openCode: boolean): Promise<void> {
  await command(deps, "image-check", { base_digest: operation.configuration.base_digest, image_digest: image });
  const name = `${environmentContainerName(deps.config.mcHome, operation.id)}-check`;
  const prior = await inspect(deps, "container", name);
  if (prior) await succeeded(deps.docker(["rm", "-f", name]), "The prior compatibility container could not close");
  const openCodeMount = openCode ? [...openCodeArguments(deps.config.mcHome, "opencode"), "--tmpfs", "/tmp:rw,nosuid,nodev,mode=1777"] : [];
  const script = `
    const fs = await import('node:fs/promises');
    const { constants } = await import('node:fs');
    if (process.getuid() !== 10002 || process.getgid() !== 10002) throw new Error('Invalid runtime user.');
    await fs.access('${CULLER_RUNTIME}/commands/mc', constants.X_OK);
    let writable = false;
    try { await fs.access('${CULLER_RUNTIME}/commands/mc', constants.W_OK); writable = true; } catch {}
    if (writable) throw new Error('The runtime permits file changes.');
    const node = Bun.spawnSync(['${CULLER_RUNTIME}/bin/node', '--version']);
    if (node.exitCode !== 0) throw new Error('The private Node runtime failed.');
    await import('${CULLER_RUNTIME}/app/src/agent-runner/adapters/claude.ts');
    await import('${CULLER_RUNTIME}/app/src/agent-runner/adapters/codex.ts');
    const wiki = Bun.spawnSync(['${CULLER_RUNTIME}/bin/node', '--input-type=module', '-e',
      'await import("${CULLER_RUNTIME}/openwiki/node_modules/openwiki/dist/integrations/core/session-manager.js")']);
    if (wiki.exitCode !== 0) throw new Error('The private OpenWiki runtime failed.');
    for (const path of ['/workspace', '/mc']) {
      const entry = await fs.lstat(path);
      if (!entry.isDirectory() || entry.isSymbolicLink()) throw new Error('Invalid reserved directory.');
    }
    if (${openCodeMount.length > 0}) {
      const result = Bun.spawnSync(['/opt/culler-opencode/node_modules/@opencode/cli-linux-arm64/bin/opencode', '--version'], {
        env: { ...process.env, XDG_DATA_HOME: '/tmp/data', XDG_CONFIG_HOME: '/tmp/config', XDG_CACHE_HOME: '/tmp/cache', XDG_STATE_HOME: '/tmp/state' },
      });
      if (result.exitCode !== 0) throw new Error('The OpenCode program cannot use this image.');
    }
    console.log(JSON.stringify({bun:Bun.version,node:node.stdout.toString().trim()}));
  `;
  await succeeded(deps.docker(["run", "--rm", "--name", name, "--network", "none", "--read-only",
    "--label", "mc-managed=true", ...deploymentArguments(deps.config.mcHome), "--label", `mc-project=${operation.project_id}`,
    "--user", "10002:10002", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
    "--pids-limit", "128", "--memory", "512m", "--cpus", "1", "--entrypoint", `${CULLER_RUNTIME}/bin/bun`,
    ...runtimeArguments(deps), ...openCodeMount, image, "-e", script]), "The image cannot use the Culler runtime");
}

async function measureEnvironment(project: string, image: string, deps: Dependencies, clear = false): Promise<void> {
  const inspected = await inspect(deps, "image", image);
  if (!inspected) throw new Error("The selected Project image is unavailable.");
  const home = await prepareProjectHome(project, deps);
  const result = await succeeded(deps.docker(["run", "--rm", "--network", "none", "--user", "10002:10002",
    "--label", "mc-managed=true", ...deploymentArguments(deps.config.mcHome), "--label", `mc-project=${project}`,
    "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--memory", "512m", "--pids-limit", "128",
    "--entrypoint", `${CULLER_RUNTIME}/bin/bun`, ...runtimeArguments(deps),
    ...projectCacheArguments(deps.config.mcHome, project, image), ...projectHomeArguments(home, false), image, "-e",
    `const {projectHomeUsage,${clear ? "clearProjectCache" : "projectCacheUsage"}}=await import('${CULLER_RUNTIME}/app/src/shared/project-cache.ts');` +
      `const cache=${clear ? "{bytes:await clearProjectCache('/cache')}" : "await projectCacheUsage()"};` +
      "const home=await projectHomeUsage();" +
      `const {collectProjectImageInventory}=await import('${CULLER_RUNTIME}/app/src/shared/project-evidence.ts');` +
      "const inventory=await collectProjectImageInventory();" +
      "console.log(JSON.stringify({cache_bytes:cache.bytes,home_bytes:home.bytes,inventory}));"]), "Project disk use is unavailable");
  const measured = JSON.parse(result.stdout);
  await command(deps, "storage-report", { project_id: project, image_digest: image,
    metadata: { cache_bytes: measured.cache_bytes, home_bytes: measured.home_bytes, image_bytes: inspected.Size ?? 0, inventory: measured.inventory } });
}

async function buildImage(operation: EnvironmentOperation, deps: Dependencies, signal: AbortSignal): Promise<void> {
  const configuration = operation.configuration;
  const dockerfile = configuration.dockerfile;
  if (!dockerfile || !safeBuildPath(dockerfile.path)) throw new Error("The proposed Dockerfile is unavailable.");
  const tag = `culler-project-${namespace(deps.config.mcHome)}-${operation.project_id}:${operation.id}`.toLowerCase();
  const trusted = (image: any) => image?.Config?.Labels?.["com.culler.environment.operation"] === operation.id &&
    image?.Config?.Labels?.["com.culler.environment.inputs"] === operation.inputs_digest && DIGEST.test(image?.Id);
  let image = await inspect(deps, "image", tag);
  if (image && !trusted(image)) throw new Error("The build image does not match the recorded attempt.");
  if (!image) {
    await restoreBase(operation, deps);
    const base = await inspect(deps, "image", configuration.base_digest);
    if (!base || base.Id !== configuration.base_digest) throw new Error("The selected Project base image is unavailable.");
    const reference = projectBaseReference(configuration.base_digest);
    await succeeded(deps.docker(["tag", configuration.base_digest, reference]), "The selected Project base reference could not be prepared");
    if ((await inspect(deps, "image", reference))?.Id !== configuration.base_digest) {
      throw new Error("The Project base reference differs from its selected image.");
    }
    const context = `${deps.config.mcHome}/environment-builds/${operation.id}`;
    await deps.fs.rm(context, { recursive: true });
    await deps.fs.mkdir(context, { mode: 0o700 });
    const files = [dockerfile, ...(configuration.build_files ?? [])];
    const seen = new Set<string>();
    for (const file of files) {
      if (!safeBuildPath(file.path) || seen.has(file.path)) throw new Error("The approved build contains an invalid file path.");
      seen.add(file.path);
      await deps.fs.mkdir(posix.dirname(`${context}/${file.path}`), { mode: 0o700 });
      await deps.fs.writeFile(`${context}/${file.path}`, file.content, { mode: 0o600 });
    }
    const args = Object.entries(configuration.build_args ?? {}).sort(([a], [b]) => a.localeCompare(b));
    if (args.some(([key]) => !/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(key))) throw new Error("The Docker build argument name is invalid.");
    await command(deps, "heartbeat", { operation_id: operation.id });
    await succeeded(deps.docker(["build", "--pull=false", "--platform", "linux/arm64", "--file", `${context}/${dockerfile.path}`,
      "--tag", tag, "--label", `mc-project=${operation.project_id}`, "--label", `com.culler.environment.operation=${operation.id}`,
      "--label", `com.culler.environment.inputs=${operation.inputs_digest}`,
      ...args.flatMap(([key, value]) => ["--build-arg", `${key}=${value}`]), context], undefined, { signal }),
    "The Project image build failed");
    image = await inspect(deps, "image", tag);
    if (!trusted(image)) throw new Error("The build result has no matching trusted identity.");
  }
  await checkRuntimeCompatibility(image.Id, operation, deps);
  await command(deps, "operation-result", { operation_id: operation.id, inputs_digest: operation.inputs_digest,
    status: "succeeded", image_digest: image.Id, runtime_compatible: true,
    metadata: { tag, platform: "linux/arm64", runtime_identity: deps.config.releaseBuildId } });
}

async function originalProjectInput(operation: EnvironmentOperation, deps: Dependencies): Promise<string | undefined> {

  if (operation.metadata?.proposal_operation_id) {
    const prior = operation.metadata.proposal_operation_id;
    if (!ID.test(prior)) throw new Error("The proposal operation identity is invalid.");
    const inputTree = (await deps.fs.readFile(`${deps.config.mcHome}/environment-runs/${prior}/session/project-input-tree`)).trim();
    if (!/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(inputTree)) throw new Error("The original Project files are unavailable.");
    return inputTree;
  }
}

async function acceptFiles(operation: EnvironmentOperation, deps: Dependencies): Promise<void> {
  await checkRuntimeCompatibility(operation.image_digest, operation, deps);
  const digest = operation.metadata?.files_digest;
  if (!digest || !DIGEST.test(digest)) throw new Error("Source acceptance requires the checked file digest.");
  const source = operation.metadata?.initial_setup ? undefined : operation.metadata?.source_commit;
  if (!operation.metadata?.initial_setup && !/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(source ?? "")) {
    throw new Error("Source acceptance requires the checked source commit.");
  }
  const name = environmentContainerName(deps.config.mcHome, operation.id);
  let workspace = operation.workspace_root;
  if (operation.metadata?.candidate) {
    const validation = operation.metadata.validation_operation_id;
    if (!validation || !ID.test(validation)) throw new Error("Candidate acceptance requires its retained validation workspace.");
    workspace = `${deps.config.mcHome}/environment-runs/${validation}/source`;
  }
  let filesPath: string | undefined;
  if (!operation.metadata?.candidate) {
    const root = `${deps.config.mcHome}/environment-runs/${operation.id}`;
    await deps.fs.mkdir(root, { mode: 0o700 });
    filesPath = `${root}/build-files.json`;
    const tree = await originalProjectInput(operation, deps);
    await deps.fs.writeFile(filesPath, JSON.stringify({ files: [
      ...(operation.configuration.dockerfile ? [operation.configuration.dockerfile] : []),
      ...(operation.configuration.build_files ?? []),
    ], removed_files: operation.metadata?.removed_files ?? [],
      denied: tree ? { tree, files: operation.metadata?.denied_build_files ?? [] } : undefined }), { mode: 0o644 });
  }
  let container = await inspect(deps, "container", name);
  if (container && (container.Config?.Labels?.["com.culler.environment.inputs"] !== operation.inputs_digest ||
      container.Config?.Labels?.["com.culler.environment.operation"] !== operation.id)) {
    throw new Error("The source acceptance container has another identity.");
  }
  if (!container) {
    await succeeded(deps.docker(["create", "--name", name, "--network", "none", "--user", "10002:10002",
      "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--pids-limit", "128", "--memory", "1g",
      "--label", "mc-managed=true", ...deploymentArguments(deps.config.mcHome), "--label", "mc-tier=setup", "--label", `mc-project=${operation.project_id}`,
      "--label", `com.culler.environment.operation=${operation.id}`, "--label", `com.culler.environment.inputs=${operation.inputs_digest}`,
      "--entrypoint", `${CULLER_RUNTIME}/bin/bun`, ...runtimeArguments(deps),
      ...configuredGitEnvironment(deps.config.gitAuthorName, deps.config.gitAuthorEmail),
      ...(filesPath ? ["--mount", `type=bind,src=${filesPath},dst=/mc/build-files.json,readonly`] : []),
      "--mount", `type=bind,src=${workspace},dst=/workspace/source`, operation.configuration.base_digest,
      `${CULLER_RUNTIME}/app/src/shared/project-files.ts`, "accept", "/workspace/source", digest,
      ...(source || filesPath ? [source ?? ""] : []), ...(filesPath ? ["/mc/build-files.json", operation.id] : [])]),
    "The source acceptance container could not be created");
    await succeeded(deps.docker(["start", name]), "Source acceptance could not start");
    await command(deps, "heartbeat", { operation_id: operation.id });
    container = await inspect(deps, "container", name);
  }
  if (container?.State?.Status === "created") await succeeded(deps.docker(["start", name]), "Source acceptance could not start");
  const wait = await succeeded(deps.docker(["wait", name]), "Source acceptance did not finish");
  const logs = await succeeded(deps.docker(["logs", name]), "Source acceptance result is unavailable");
  if (wait.stdout.trim() !== "0") throw new Error(`Source acceptance failed: ${logs.stderr.slice(-3000)}`);
  const result = JSON.parse(logs.stdout.trim());
  if (result.files_digest !== digest || !/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(result.source_commit)) {
    throw new Error("The source acceptance result differs from the checked files.");
  }
  await command(deps, "operation-result", { operation_id: operation.id, inputs_digest: operation.inputs_digest,
    status: "succeeded", files_digest: digest, source_commit: result.source_commit,
    ...(source ? { metadata: { checked_source_commit: source } } : {}) });
}

export async function projectSecretValues(configuration: ProjectEnvironment, project: string, deps: Pick<Dependencies, "config" | "fs">, allowMissing = false): Promise<Record<string, string>> {
  if (!PROJECT_ID.test(project)) throw new Error("The Project secret identity is invalid.");
  const values: Record<string, string> = {};
  for (const secret of configuration.secrets ?? []) {
    if (!ID.test(secret.reference) || !/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(secret.name)) throw new Error("The Project secret reference is invalid.");
    try { values[secret.name] = await deps.fs.readFile(`${deps.config.mcHome}/project-secrets/${project}/${secret.reference}`); }
    catch { if (!allowMissing) throw new Error(`The secret reference for ${secret.name} is unavailable.`); }
  }
  return values;
}

async function startSetupContainer(operation: EnvironmentOperation, deps: Dependencies, secrets: Record<string, string>): Promise<void> {
  const name = environmentContainerName(deps.config.mcHome, operation.id);
  const result = await deps.docker(["start", "--interactive", name], encoder.encode(JSON.stringify(secrets) + "\n"));
  deps.credentials?.revoke?.(operation.id, operation.id);
  await deps.fs.rm(`${deps.config.mcHome}/environment-runs/${operation.id}/provider-access.json`);
  if (result.exitCode !== 0) {
    const container = await inspect(deps, "container", name).catch(() => undefined);
    if (container?.State?.Status === "created") {
      throw setupCommandError(container.State.Error || "The setup container could not start.", result);
    }
    throw setupProcessError(result, deps.redact ?? diagnosticRedactor(process.env, Object.values(secrets)));
  }
}

async function agentOperation(operation: EnvironmentOperation, deps: Dependencies): Promise<void> {
  const name = environmentContainerName(deps.config.mcHome, operation.id);
  const previous = await inspect(deps, "container", name);
  if (previous) {
    if (previous.Config?.Labels?.["com.culler.environment.inputs"] !== operation.inputs_digest ||
        previous.Config?.Labels?.["com.culler.environment.operation"] !== operation.id || previous.Image !== operation.image_digest) {
      throw new Error("The setup container does not match the recorded operation.");
    }
    if (operation.runtime_binding?.harness !== "fake" &&
        !deps.credentials?.has?.(operation.id, operation.id)) {
      deps.credentials?.revoke?.(operation.id, operation.id);
      await succeeded(deps.docker(["rm", "--force", name]), "The prior setup container could not stop");
      await deps.fs.rm(`${deps.config.mcHome}/environment-runs/${operation.id}/provider-access.json`);
      throw new SetupError("The setup agent stopped after the resident restarted.",
        "The prior provider access expired. Culler retains the setup files and reports this attempt as incomplete.");
    }
    if (previous.State?.Running) return;
    if (previous.State?.Status === "created") {
      const secrets = await projectSecretValues(operation.configuration, operation.project_id, deps, true);
      await startSetupContainer(operation, deps, secrets);
      return;
    }
    const logs = await deps.docker(["logs", name]);
    if (logs.exitCode !== 0) throw new SetupError("The setup output is unavailable.", logs.stderr);
    throw setupProcessError({ ...logs, exitCode: previous.State?.ExitCode ?? 1 }, deps.redact);
  }
  const runtime = operation.runtime_binding;
  if (!runtime) throw new Error("The setup agent has no configured model route.");
  const fake = runtime.harness === "fake" && runtime.id === "fake";
  if (!fake && (runtime.base_url !== "" ||
      !((runtime.harness === "codex" && runtime.channel === "codex") ||
        (runtime.harness === "claude-sdk" && runtime.channel === "claude") ||
        (runtime.harness === "opencode" && runtime.channel === "opencode")))) {
    throw new Error("The setup agent requires a supported connection.");
  }
  await checkRuntimeCompatibility(operation.image_digest, operation, deps);
  const began = Date.parse((operation.created_at ?? "").replace(" ", "T").replace(/Z?$/, "Z"));
  const expiresAt = (Number.isFinite(began) ? began : Date.now()) + ENVIRONMENT_ATTEMPT_TIMEOUT_MS;
  const projection = runtime.harness === "fake" ? null : deps.credentials?.project(runtime, {
    runId: operation.id, launchId: operation.id, model: runtime.model, expiresAt,
    profileRevision: runtime.profile_revision, attemptId: operation.id,
  });
  if (projection === undefined || (projection !== null && "refused" in projection)) {
    throw new Error("The setup agent requires an available connection. Open Configs to repair the connection.");
  }
  if (projection) {
    if (Object.keys(projection.env).length || typeof projection.proxyConfig !== "string") {
      throw new Error("The setup provider access configuration is invalid.");
    }
    const access = parseProviderAccess(projection.proxyConfig, runtime.harness as "codex" | "claude-sdk" | "opencode");
    const previousRedactor = deps.redact ?? diagnosticRedactor();
    deps.redact = value => previousRedactor(value.split(access.token).join("[redacted]"));
  }
  const root = `${deps.config.mcHome}/environment-runs/${operation.id}`;
  const home = await prepareProjectHome(operation.project_id, deps);
  await deps.fs.mkdir(root, { mode: 0o700 });
  await deps.fs.mkdir(`${root}/session`, { mode: 0o777 });
  let workspace = operation.workspace_root;
  if (operation.kind === "validate" && !operation.metadata?.initial_setup && operation.configuration.source_commit) {
    const commit = operation.configuration.source_commit;
    if (!/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(commit)) throw new Error("The validation source commit is invalid.");
    workspace = `${root}/source`;
    let source = operation.workspace_root;
    if (operation.metadata?.source_run_id) {
      if (!ID.test(operation.metadata.source_run_id)) throw new Error("The candidate source identity is invalid.");
      source = `${deps.config.mcHome}/runs/repositories/${operation.metadata.source_run_id}/repo`;
    }
    await deps.fs.mkdir(workspace, { mode: 0o777 });
    await succeeded(deps.docker(["run", "--rm", "--network", "none", "--user", "10002:10002",
      "--label", "mc-managed=true", ...deploymentArguments(deps.config.mcHome), "--label", `mc-project=${operation.project_id}`,
      "--label", `com.culler.environment.operation=${operation.id}`,
      "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--entrypoint", "/bin/bash",
      "--mount", `type=bind,src=${source},dst=/source,readonly`,
      "--mount", `type=bind,src=${workspace},dst=/workspace/source`, operation.configuration.base_digest,
      "-e", "-c", "if [ ! -d /workspace/source/.git ]; then git clone --no-hardlinks /source /workspace/source; fi\ngit -C /workspace/source checkout --detach \"$1\"\ngit -C /workspace/source reset --hard \"$1\"\ngit -C /workspace/source clean -fdx", "setup", commit]),
    "The validation source could not be prepared");
  }
  const secrets = await projectSecretValues(operation.configuration, operation.project_id, deps, true);
  await ensureProjectSkillStore(deps.config.mcHome, operation.project_id, deps.fs);
  const skills = composeRunSkills(operation.skills,
    await readOperatorSkills(`${deps.config.mcHome}/skills/projects/${operation.project_id}/setup`, deps.fs));
  const inputTree = await originalProjectInput(operation, deps);
  const envelope = {
    tier: "setup", role: "setup", run_id: operation.id, project: operation.project_id,
    subject_id: null, harness: runtime.harness, model_binding: runtime.id,
    runtime: { harness: runtime.harness, id: runtime.id, channel: runtime.channel, model: runtime.model, base_url: runtime.base_url, ...(runtime.effort ? { effort: runtime.effort } : {}) },
    brief: operation.brief, skills: skills.skills, skill_problems: skills.problems, heartbeat_interval_s: 20,
    environment: { configuration: operation.configuration, revision_id: operation.revision_id, inputs_digest: operation.inputs_digest,
      kind: operation.kind, removed_files: operation.metadata?.removed_files ?? [],
      initial_setup: operation.metadata?.initial_setup === true, setup_id: operation.metadata?.setup_id, input_tree: inputTree, denied_build_files: operation.metadata?.denied_build_files ?? [] },
    mounts: { workspace: "/workspace/source", session: "/mc/session", allow_write: ["/workspace/source", "/mc/session"] },
    ...(deps.config.roleBehaviors.setup ? { harness_config: { behavior: deps.config.roleBehaviors.setup } } : {}),
  };
  await deps.fs.writeFile(`${root}/run.json`, JSON.stringify(envelope), { mode: 0o644 });
  const access: string[] = [];
  if (projection?.proxyConfig) {
    await deps.fs.writeFile(`${root}/provider-access.json`, projection.proxyConfig, { mode: 0o644 });
    access.push("--mount", `type=bind,src=${root}/provider-access.json,dst=/mc/provider-access.json,readonly`,
      "-e", "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json");
  }
  await succeeded(deps.docker(["create", "--interactive", "--name", name, "--label", "mc-managed=true", ...deploymentArguments(deps.config.mcHome), "--label", "mc-tier=setup",
    "--label", `mc-project=${operation.project_id}`, "--label", `com.culler.environment.operation=${operation.id}`,
    "--label", `com.culler.environment.inputs=${operation.inputs_digest}`,
    ...projectExecutionArguments(runtime.harness === "fake", deps.hostZone()),
    "--entrypoint", `${CULLER_RUNTIME}/bin/bun`,
    ...runtimeArguments(deps), ...openCodeArguments(deps.config.mcHome, runtime?.harness), ...projectCacheArguments(deps.config.mcHome, operation.project_id, operation.image_digest),
    ...projectHomeArguments(home),
    "--mount", `type=bind,src=${workspace},dst=/workspace/source`,
    "--mount", `type=bind,src=${root}/run.json,dst=/mc/run.json,readonly`,
    "--mount", `type=bind,src=${root}/session,dst=/mc/session`,
    "--mount", `type=bind,src=${deps.config.behaviorsDir},dst=/mc/behaviors,readonly`,
    "--mount", `type=volume,src=${deps.config.spineVolume},dst=${posix.dirname(deps.config.spineDbPath)}`,
    "-e", `MC_SPINE=${deps.config.spineDbPath}`,
    ...access,
    ...configuredGitEnvironment(deps.config.gitAuthorName, deps.config.gitAuthorEmail),
    operation.image_digest, `${CULLER_RUNTIME}/app/src/setup-runner/main.ts`]), "The setup container could not be created");
  await startSetupContainer(operation, deps, secrets);
}

export function createEnvironmentLane(deps: Dependencies, {
  instance = crypto.randomUUID(), now = () => new Date(), maintenance = async () => false,
} = {}) {
  const health = createEnvironmentHealth(deps, instance, now);
  const active = new Map<string, { done: Promise<void>; controller: AbortController }>();
  let busy = false;
  let stopped = false;
  let timer: ReturnType<typeof setInterval> | undefined;
  let defaultImage: string | undefined;
  let selectionsCheckedAt = 0;
  let prepared = false;
  const checkSelections = async () => {
    if (!deps.config.runtimeVolume || !deps.config.projectImage) { prepared = true; return; }
    const result = await command(deps, "selections", {});
    const runtime = deps.config.releaseBuildId ?? "development";
    for (const selected of result.selections ?? []) {
      const key = `${selected.project_id}:${selected.image_digest}:${deps.config.releaseBuildId}`;
      const recorded = selected.checked_status === "succeeded" && selected.checked_image_digest === selected.image_digest &&
        selected.checked_runtime_identity === runtime;
      try {
        const operation = {
          id: `runtime-${createHash("sha256").update(key).digest("hex").slice(0, 24)}`,
          project_id: selected.project_id,
          configuration: { base_digest: selected.base_digest },
        } as EnvironmentOperation;
        if (recorded) {
          await confirmRecordedCompatibility(selected.image_digest, operation, deps);
        } else {
          await checkRuntimeCompatibility(selected.image_digest, operation, deps);
          await command(deps, "runtime-check", { project_id: selected.project_id, image_digest: selected.image_digest,
            runtime_identity: runtime, status: "succeeded" });
        }
      } catch (error) {
        const imageMissing = !(await inspect(deps, "image", selected.image_digest).catch(() => ({ unavailable: true })));
        await command(deps, "runtime-check", { project_id: selected.project_id, image_digest: selected.image_digest,
          runtime_identity: runtime, status: "failed", error: setupError(error).message,
          metadata: { image_missing: imageMissing } });
        continue;
      }
      await measureEnvironment(selected.project_id, selected.image_digest, deps).catch((error) => {
        deps.log.warn("spawn.gate_failed", `Project disk use could not be measured: ${String(error)}`);
      });
    }
    selectionsCheckedAt = Date.now();
    prepared = true;
  };
  const request = async () => {
    if (busy || stopped || !deps.config.projectImage || !deps.config.runtimeVolume) return;
    busy = true;
    try {
      if (await maintenance()) return;
      await health.recover();
      if (!defaultImage) {
        let image = await inspect(deps, "image", deps.config.projectImage);
        if (!image && REGISTRY_DIGEST.test(deps.config.projectImage)) {
          await succeeded(deps.docker(["pull", "--platform", "linux/arm64", deps.config.projectImage]), "The default Project image could not be restored");
          image = await inspect(deps, "image", deps.config.projectImage);
        }
        if (!image || !DIGEST.test(image.Id)) throw new Error("The default Project image is unavailable.");
        await command(deps, "base-record", { base_digest: image.Id,
          metadata: { ...(REGISTRY_DIGEST.test(deps.config.projectImage) ? { registry_reference: deps.config.projectImage } : {}),
            release_identity: deps.config.releaseBuildId ?? "development" } });
        defaultImage = image.Id;
      }
      if (Date.now() - selectionsCheckedAt > 10 * 60_000) await checkSelections();
      const response = await command(deps, "work", { default_image_digest: defaultImage, runtime_identity: deps.config.releaseBuildId ?? "development" });
      if (!Array.isArray(response.operations)) throw new Error("The environment work response contains no operations.");
      await health.recordRequest();
      for (const operation of response.operations as EnvironmentOperation[]) {
        let operationDeps = deps;
        let redact = diagnosticRedactor();
        const reportFailure = async (error: unknown) => {
          const failure = setupError(error, operationDeps.redact ?? redact);
          deps.log.warn("spawn.gate_failed", failure.message, { run_id: operation.id });
          await health.submit(operation.project_id, { operation_id: operation.id, inputs_digest: operation.inputs_digest,
            status: "failed", error: failure.message, metadata: { failure_details: failure.details } }, operationDeps.redact ?? redact);
        };
        try {
          assertOperation(operation);
          if (active.has(operation.id) || health.has(operation.id)) continue;
          const secretValues = ["setup", "validate"].includes(operation.kind)
            ? await projectSecretValues(operation.configuration, operation.project_id, deps, true) : {};
          const credentialValues: string[] = Object.values(secretValues);
          const accessPath = `${deps.config.mcHome}/environment-runs/${operation.id}/provider-access.json`;
          try {
            const access = JSON.parse(await deps.fs.readFile(accessPath));
            if (typeof access.token === "string") credentialValues.push(access.token);
          } catch {}
          redact = diagnosticRedactor(process.env, credentialValues);
          operationDeps = { ...deps, redact,
            runMc: async (argv, input, options) => {
              if (argv[0] === "environment" && argv[1] === "operation-result") {
                return health.submit(operation.project_id, JSON.parse(new TextDecoder().decode(input)), operationDeps.redact ?? redact);
              }
              return deps.runMc(argv, input, options);
            },
          };
          const previousID = operation.metadata?.previous_operation_id;
          if (previousID) {
            deps.credentials?.revoke?.(previousID, previousID);
            await deps.fs.rm(`${deps.config.mcHome}/environment-runs/${previousID}/provider-access.json`);
            const previous = await inspect(deps, "container", environmentContainerName(deps.config.mcHome, previousID));
            if (previous?.State?.Running) {
              if (previous.Config?.Labels?.["com.culler.environment.operation"] !== previousID) {
                throw new Error("The previous setup container has another identity.");
              }
              await succeeded(deps.docker(["stop", "--time", "10", environmentContainerName(deps.config.mcHome, previousID)]),
                "The completed setup agent could not stop");
            }
          }
          const controller = new AbortController();
          const began = Date.parse((operation.created_at ?? "").replace(" ", "T").replace(/Z?$/, "Z"));
          if (Number.isFinite(began) && Date.now() - began >= ENVIRONMENT_ATTEMPT_TIMEOUT_MS) {
            deps.credentials?.revoke?.(operation.id, operation.id);
            await deps.fs.rm(`${deps.config.mcHome}/environment-runs/${operation.id}/provider-access.json`);
            const name = environmentContainerName(deps.config.mcHome, operation.id);
            const container = await inspect(deps, "container", name);
            if (container?.Config?.Labels?.["com.culler.environment.operation"] === operation.id) {
              await succeeded(deps.docker(["stop", name]), "The expired setup container could not stop");
            }
            await command(operationDeps, "operation-result", { operation_id: operation.id, inputs_digest: operation.inputs_digest,
              status: "failed", error: "The environment execution reached its 30-minute limit. Culler retains its files." });
            continue;
          }
          const timeout = setTimeout(() => {
            controller.abort();
            deps.credentials?.revoke?.(operation.id, operation.id);
            void deps.fs.rm(`${deps.config.mcHome}/environment-runs/${operation.id}/provider-access.json`);
            void deps.docker(["stop", environmentContainerName(deps.config.mcHome, operation.id)]);
          },
            Math.max(1000, ENVIRONMENT_ATTEMPT_TIMEOUT_MS - (Number.isFinite(began) ? Date.now() - began : 0)));
          const heartbeat = setInterval(() => {
            if (["build", "accept", "runtime-check"].includes(operation.kind)) void command(deps, "heartbeat", { operation_id: operation.id }).catch(() => {});
          }, 20_000);
          const done = (async () => {
            try {
              if (operation.kind === "build") await buildImage(operation, operationDeps, controller.signal);
              else if (operation.kind === "accept") await acceptFiles(operation, operationDeps);
              else if (operation.kind === "runtime-check") {
                await checkRuntimeCompatibility(operation.image_digest, operation, operationDeps);
                await command(operationDeps, "operation-result", { operation_id: operation.id, inputs_digest: operation.inputs_digest,
                  status: "succeeded", runtime_compatible: true });
              }
              else if (operation.kind === "cache-clear") {
                await command(deps, "heartbeat", { operation_id: operation.id });
                await measureEnvironment(operation.project_id, operation.image_digest, operationDeps, true);
                await command(operationDeps, "operation-result", { operation_id: operation.id, inputs_digest: operation.inputs_digest, status: "succeeded" });
              }
              else await agentOperation(operation, operationDeps);
            } catch (error) {
              deps.credentials?.revoke?.(operation.id, operation.id);
              await deps.fs.rm(`${deps.config.mcHome}/environment-runs/${operation.id}/provider-access.json`);
              if (stopped) return;
              await reportFailure(error);
            }
          })().finally(() => { clearTimeout(timeout); clearInterval(heartbeat); active.delete(operation.id); });
          active.set(operation.id, { done, controller });
        } catch (error) {
          await reportFailure(error);
        }
      }
    } catch (error) {
      const failure = setupError(error);
      deps.log.warn("spawn.gate_failed", `Project setup could not advance: ${failure.message}`);
      await health.recordRequest(failure);
    } finally { busy = false; }
  };
  return {
    request,
    checkSelections,
    assertPrepared() {
      if (!prepared) throw new Error("Culler cannot dispatch work until the Project runtime checks complete.");
    },
    async prepare() {
      try { await checkSelections(); }
      catch (error) {
        const failure = setupError(error);
        deps.log.warn("spawn.gate_failed", `Project setup could not advance: ${failure.message}`);
        await health.recordRequest(failure);
      }
    },
    start() { if (!timer) timer = setInterval(() => { void request(); }, 5000); void request(); },
    activeCount: () => active.size + (busy ? 1 : 0),
    async stop() {
      stopped = true;
      if (timer) clearInterval(timer);
      for (const [id, operation] of active) {
        operation.controller.abort();
        deps.credentials?.revoke?.(id, id);
        await deps.fs.rm(`${deps.config.mcHome}/environment-runs/${id}/provider-access.json`);
        await deps.docker(["stop", "--time", "10", environmentContainerName(deps.config.mcHome, id)]).catch(() => {});
      }
      await Promise.all([...active.values()].map((operation) => operation.done));
    },
  };
}
