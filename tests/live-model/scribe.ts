#!/usr/bin/env bun
import { initializeLiveSpine } from "./environment";
import { cheapestLiveModel, liveModelCost, liveModelControls } from "./models";
import { collectLiveUsage, snapshotNativeUsage, type LiveUsageEvidence } from "./usage";
import { randomBytes, createHash } from "node:crypto";
import { createConnection } from "node:net";
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { adapterInvocation, type RunEnvelope } from "../../runner/agent-runner/main";
import { installRunSkills } from "../../runner/agent-runner/skills";
import { VERSION, REVISION, git, wikiDigest, wikiFiles } from "../../runner/openwiki/integration.mjs";
import { parseRuntimeCatalog, type RuntimeBindingSpec } from "../../resident/src/credential-projector";
import { startForegroundProviderAccess } from "../../resident/src/provider-foreground";
import { acquireConnectionOwner } from "../../resident/src/connection-owner";
import { parseProviderAccess, type ProviderAccess } from "../../runner/agent-runner/adapters/provider-access";

const LIMIT_MS = 300_000;
const OUTPUT_LIMIT = 16 << 20;
const RUNTIME = "/opt/culler-runtime";

export function scribeAdapterInvocation(run: RunEnvelope): string[] {
  const args = adapterInvocation(run, "").argv;
  const adapter = run.harness === "codex" ? "codex" : "claude";
  const source = `/app/src/agent-runner/adapters/${adapter}.ts`;
  if (args[1] !== source && args[1] !== RUNTIME + source) {
    throw new Error("The live test requires the normal installed adapter.");
  }
  return [`${RUNTIME}/bin/bun`, RUNTIME + source, ...args.slice(2)];
}
export const SCRIBE_TOOLS = [
  "openwiki_begin", "openwiki_submit_plan", "openwiki_next_page",
  "openwiki_inspect_page_claims", "openwiki_submit_page", "openwiki_finish",
];
const SOURCE = "The service returns one complete project report.\n";
export const ACCEPTANCE_REPORT = "---\ntitle: Complete accepted report\ntype: Research\ndocument: true\ntask_id: 1\ntask_status: verified\ntags: [research, evidence]\n---\n\n# Complete accepted report\n\n" +
  Array.from({ length: 80 }, (_, i) => `## Observation ${i + 1}\n\nThe experiment preserves record ${i + 1} in the complete report.\n\nThe evidence covers one service response.\n\n`).join("") +
  "## Evidence\n\nThe report cites [the source](../../source.txt).\n\n## Limitations\n\nThe experiment covers one service.\n";

export interface ScribeAcceptanceOptions {
  home: string;
  mcBinary: string;
  image: string;
  codexBinding: string;
  claudeBinding: string;
  output: string;
  harness?: "codex" | "claude-sdk";
}

export function parseScribeAcceptanceArgs(args: string[]): ScribeAcceptanceOptions {
  if (!args.includes("--confirm-live-spend")) throw new Error("The live Scribe test requires --confirm-live-spend.");
  const values = new Map<string, string>();
  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--confirm-live-spend") continue;
    const name = args[i]!;
    if (!["--mc-home", "--mc-binary", "--image", "--codex-binding", "--claude-binding", "--output", "--harness"].includes(name) ||
        values.has(name) || !args[i + 1] || args[i + 1]!.startsWith("--")) {
      throw new Error("The live Scribe test arguments are invalid.");
    }
    values.set(name, args[++i]!);
  }
  const path = (name: string): string => {
    const value = values.get(name);
    if (!value || !isAbsolute(value) || /[:\r\n\0]/.test(value)) throw new Error(`${name} requires an absolute local path.`);
    return resolve(value);
  };
  const binding = (name: string): string => {
    const value = values.get(name);
    if (!value || !/^[a-z0-9][a-z0-9_.-]{0,63}$/.test(value)) throw new Error(`${name} requires a configured profile identifier.`);
    return value;
  };
  const image = values.get("--image") ?? "";
  if (!/^sha256:[a-f0-9]{64}$/.test(image)) throw new Error("--image requires an immutable Docker image ID.");
  const harness = values.get("--harness");
  if (harness !== undefined && harness !== "codex" && harness !== "claude-sdk") throw new Error("Select one supported Scribe harness.");
  return {
    harness, home: path("--mc-home"), mcBinary: path("--mc-binary"), image,
    codexBinding: binding("--codex-binding"), claudeBinding: binding("--claude-binding"), output: path("--output"),
  };
}

export function successfulScribeTools(stdout: string, harness: string): string[] {
  const success = new Set<string>();
  const calls = new Map<string, string>();
  for (const line of stdout.split("\n")) {
    let event;
    try { event = JSON.parse(line); } catch { continue; }
    if (harness === "codex") {
      const item = event?.item;
      if (event?.type === "item.completed" && item?.type === "mcp_tool_call" && item.server === "openwiki" &&
          item.status === "completed" && !item.error && item.result) success.add(item.tool);
    } else if (harness === "claude-sdk") {
      const blocks = event?.message?.content;
      if (!Array.isArray(blocks)) continue;
      for (const block of blocks) {
        if (event.type === "assistant" && block?.type === "tool_use" &&
            typeof block.name === "string" && block.name.startsWith("mcp__openwiki__")) {
          calls.set(block.id, block.name.slice("mcp__openwiki__".length));
        }
        if (event.type === "user" && block?.type === "tool_result" && block.is_error !== true) {
          const name = calls.get(block.tool_use_id);
          if (name) success.add(name);
        }
      }
    }
  }
  return SCRIBE_TOOLS.filter((tool) => success.has(tool));
}

export function hasScribeSourceClaim(claims: any, statement = SOURCE.trim()): boolean {
  return Array.isArray(claims?.claims) && claims.claims.some((claim: any) => claim.statement === statement &&
    Array.isArray(claim.evidence) && claim.evidence.some((evidence: any) =>
      /^repo:\/\/source\.txt#L1(?:-L1)?$/.test(evidence.resource)));
}

export function parseProfileAccess(body: string, credential: string, nonce: string, harness: ProviderAccess["harness"]): ProviderAccess {
  if (Buffer.byteLength(body) > 64 << 10 || !body.endsWith("\n") || body.trim().includes("\n")) {
    throw new Error("The resident access response is invalid.");
  }
  let value;
  try { value = JSON.parse(body); }
  catch { throw new Error("The resident access response is invalid."); }
  const allowed = ["version", "status", "credential", "nonce", "proxy_config"];
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      Object.keys(value).some(key => !allowed.includes(key)) || value.version !== 1 || value.status !== "ok" ||
      value.credential !== credential || value.nonce !== nonce) {
    throw new Error("The resident access response has another identity.");
  }
  return parseProviderAccess(JSON.stringify(value.proxy_config), harness);
}

async function accessRequest(home: string, request: Record<string, unknown>): Promise<string> {
  return new Promise<string>((accept, reject) => {
    const socket = createConnection(join(home, "health", "provider-acceptance.sock"));
    let result = "";
    socket.setTimeout(30_000, () => socket.destroy(new Error("The resident access request exceeded its time limit.")));
    socket.on("connect", () => socket.write(JSON.stringify({ version: 1, ...request }) + "\n"));
    socket.on("data", chunk => {
      result += chunk.toString("utf8");
      if (Buffer.byteLength(result) > 64 << 10) socket.destroy(new Error("The resident access response exceeds its limit."));
      else if (result.includes("\n")) { socket.end(); accept(result); }
    });
    socket.on("error", () => reject(new Error("The resident access request failed.")));
    socket.on("end", () => { if (!result.endsWith("\n")) reject(new Error("The resident access response is incomplete.")); });
  });
}

async function execute(argv: string[], stdin = "", env = process.env, timeout = 30_000, signal?: AbortSignal) {
  const child = Bun.spawn(argv, { stdin: new TextEncoder().encode(stdin), stdout: "pipe", stderr: "pipe", env, signal });
  let expired = false;
  const timer = setTimeout(() => { expired = true; child.kill(); }, timeout);
  async function collect(stream: ReadableStream<Uint8Array>) {
    const parts: Uint8Array[] = [];
    let bytes = 0;
    for await (const part of stream) {
      bytes += part.length;
      if (bytes > OUTPUT_LIMIT) { child.kill(); throw new Error("The acceptance process output exceeds its limit."); }
      parts.push(part);
    }
    return Buffer.concat(parts).toString("utf8");
  }
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, collect(child.stdout), collect(child.stderr)]);
    if (expired) throw new Error("The acceptance process exceeded its time limit.");
    return { code, stdout, stderr };
  } finally { clearTimeout(timer); }
}

interface SelectedProfile { id: string; model: string; spec: RuntimeBindingSpec }
async function selectProfiles(options: ScribeAcceptanceOptions, env = scribeHostEnv(options.home)): Promise<SelectedProfile[]> {
  const results = await Promise.all([
    execute([options.mcBinary, "agent-profile", "runtime-catalog"], "", env),
    execute([options.mcBinary, "agent-profile", "list"], "", env),
  ]);
  if (results.some((result) => result.code !== 0)) throw new Error("The configured agent profiles are unavailable.");
  const catalog = parseRuntimeCatalog(JSON.parse(results[0]!.stdout).bindings);
  const profiles = JSON.parse(results[1]!.stdout).profiles;
  return Promise.all([[options.codexBinding, "codex"], [options.claudeBinding, "claude-sdk"]].filter(([, harness]) => !options.harness || harness === options.harness).map(async ([id, harness]) => {
    const spec = catalog[id!];
    const profile = Array.isArray(profiles) ? profiles.find((item) => item.id === id) : undefined;
    if (!spec || spec.harness !== harness || profile?.enabled !== true || profile.harness !== harness ||
        profile.connection_id !== spec.credential || typeof profile.model !== "string") {
      throw new Error("Each selected profile must enable the specified harness and an authenticated connection.");
    }
    const listed = await execute([options.mcBinary, "agent-profile", "models"], JSON.stringify({ connection_id: spec.credential }), env);
    if (listed.code !== 0) throw new Error("The low-cost model catalog is unavailable.");
    const cheapest = cheapestLiveModel(spec.harness, JSON.parse(listed.stdout).models.map((row: any) => row.id), Date.now(), "scribe");
    if (profile.model !== cheapest) throw new Error(`The Scribe test requires the lowest-cost offered model: ${cheapest}.`);
    return { id: id!, model: profile.model, spec };
  }));
}

async function makeReadable(root: string): Promise<void> {
  const info = await lstat(root);
  await chmod(root, info.isDirectory() ? 0o777 : 0o644);
  if (info.isDirectory()) for (const name of await readdir(root)) await makeReadable(join(root, name));
}

export async function preflight(options: ScribeAcceptanceOptions, runner: string) {
  const expected = JSON.parse(await readFile(new URL("../../runner/agent-runner/package.json", import.meta.url), "utf8")).dependencies;
  const script = `import { readFile } from "node:fs/promises";
import { loadUpstream } from "${RUNTIME}/app/src/openwiki/integration.mjs";
await loadUpstream();
const expected = ${JSON.stringify(expected)};
for (const [name, version] of Object.entries(expected)) {
  const actual = JSON.parse(await readFile("${RUNTIME}/app/node_modules/" + name + "/package.json", "utf8")).version;
  if (actual !== version) throw new Error("The harness package differs from its fixed version.");
}
console.log("MC_SCRIBE_IMAGE_OK");`;
  const result = await execute(["docker", "run", "--rm", "--network", "none", "--user", "10002:10002",
    "-v", `${runner}:${RUNTIME}/app/src:ro`, "--entrypoint", `${RUNTIME}/bin/node`,
    options.image, "--input-type=module", "-e", script]);
  if (result.code !== 0 || result.stdout.trim() !== "MC_SCRIBE_IMAGE_OK") throw new Error("The fixed runner image failed its package check.");
}

async function runProfile(options: ScribeAcceptanceOptions, profile: SelectedProfile, runner: string, worktree: string) {
  const privateRoot = await mkdtemp(join(tmpdir(), "mc-scribe-acceptance-"));
  const name = `mc-scribe-acceptance-${randomBytes(12).toString("hex")}`;
  const root = join(privateRoot, "source");
  const session = join(privateRoot, "session");
  const home = join(privateRoot, "home");
  const temporary = join(privateRoot, "tmp");
  const sensitive: string[] = [];
  const nonce = randomBytes(16).toString("hex");
  let authorized = false;
  let usage: LiveUsageEvidence | undefined;
  let failure: Record<string, unknown> | undefined;
  let nativeEvents: Record<string, unknown>[] = [];
  let cost: ReturnType<typeof liveModelCost> | undefined;
  let controls: ReturnType<typeof liveModelControls> | undefined;
  try {
    for (const path of [root, session, home, temporary]) await mkdir(path, { mode: 0o777 });
    await mkdir(join(root, "openwiki", "reports"), { recursive: true });
    await writeFile(join(root, "source.txt"), SOURCE);
    await writeFile(join(root, "AGENTS.md"), "Preserve complete accepted project reports.\n");
    await writeFile(join(root, "openwiki/reports/accepted.md"), ACCEPTANCE_REPORT);
    await git(root, "init", "--quiet", "--template=", "-b", "main");
    await git(root, "config", "user.name", "Acceptance fixture");
    await git(root, "config", "user.email", "fixture@example.invalid");
    await git(root, "add", ".");
    await git(root, "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "Preserve the acceptance source");
    const input = (await git(root, "rev-parse", "HEAD")).trim();
    const skillPaths = ["scribe/openwiki", "shared/writing", "shared/project-wiki"];
    await installRunSkills(join(home, ".agents", "skills"), await Promise.all(skillPaths.map(async (path) => ({
      name: path.split("/")[1]!, body: await readFile(join(worktree, "mc/verbs/skills", path, "SKILL.md"), "utf8"),
    }))));
    for (const path of [root, session, home, temporary]) await makeReadable(path);
    const args = ["docker", "run", "-i", "--name", name, "--user", "10002:10002",
      "--memory", "4g", "--cpus", "2", "--cap-drop", "ALL", "--pids-limit", "512",
      "--workdir", "/workspace/source",
      "--label", "mc-managed=true", "--label", "mc-tier=scribe-acceptance",
      "-v", `${runner}:${RUNTIME}/app/src:ro`, "-v", `${root}:/workspace/source`, "-v", `${session}:/mc/session`,
      "-v", `${home}:/home/agent`, "-v", `${temporary}:/tmp`, "-e", "HOME=/home/agent",
      "-e", `MC_PRIVATE_BUN=${RUNTIME}/bin/bun`, "-e", `MC_PRIVATE_NODE=${RUNTIME}/bin/node`,
      "-e", `PATH=${RUNTIME}/commands:/usr/local/bin:/usr/bin:/bin`,
      "-e", "LD_PRELOAD=", "-e", "LD_LIBRARY_PATH=", "-e", "NODE_OPTIONS=", "-e", "BUN_OPTIONS="];
    const spec = profile.spec;
    cost = liveModelCost(spec.harness, profile.model, spec.kind.endsWith("-api-key") ? "api-key" : "account", 24);
    controls = liveModelControls(spec.harness, profile.model, "scribe");
    console.log(JSON.stringify({ profile: profile.id, planned_cost: cost, controls }));
    const body = await accessRequest(options.home, { op: "profile_authorize", credential: spec.credential,
      profile: profile.id, run_id: name, launch_id: name, model: profile.model, nonce,
      expires_at_ms: Date.now() + LIMIT_MS + 30_000, max_requests: 24 });
    const access = parseProfileAccess(body, spec.credential, nonce, spec.harness as ProviderAccess["harness"]);
    authorized = true;
    sensitive.push(access.token);
    const accessPath = join(privateRoot, "provider-access.json");
    await writeFile(accessPath, JSON.stringify(access), { mode: 0o644 });
    args.push("-v", `${accessPath}:/mc/provider-access.json:ro`, "-e", "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json");
    const adapter = scribeAdapterInvocation({
      run_id: name, role: "scribe", harness: spec.harness, model_binding: profile.id, subject_id: null,
      heartbeat_interval_s: 30, brief: "", mode: "fresh", repository: { input_commit: input },
      runtime: { harness: spec.harness, id: profile.id, model: profile.model, channel: spec.channel,
        base_url: "" },
      mounts: { session: "/mc/session", workspace: "/workspace/source" },
    });
    adapter.push(...controls.args);
    if (spec.harness === "claude-sdk") adapter.push("--max-turns", "30");
    args.push("--entrypoint", adapter[0]!, options.image, ...adapter.slice(1));
    const prompt = `Complete this isolated wiki update in /workspace/source.
Read the OpenWiki skill at /home/agent/.agents/skills/openwiki/SKILL.md.
The repository contains a complete accepted report at openwiki/reports/accepted.md.
Preserve every byte of that report and source.txt.
Use the configured OpenWiki MCP tools for every host operation.
Invoke each named tool through the native MCP interface.
In Codex, these tools use the mcp__openwiki namespace.
Call openwiki_begin in init mode.
Plan only /openwiki/quickstart.md with source.txt as its seed path.
Call openwiki_next_page and openwiki_inspect_page_claims for its returned job.
Read source.txt and write the generated quickstart page with valid frontmatter.
Add the statement "The service returns one complete project report." with evidence repo://source.txt#L1.
Call openwiki_submit_page with that Claim.
Consume the complete page queue, then call openwiki_finish.
Call all six named tools, including openwiki_inspect_page_claims, even for an empty Claim set.
A successful finish writes /tmp/openwiki-completion.json.
This acceptance container has no Culler terminal action.
Do not call mc or edit OpenWiki metadata files directly.
Do not inspect credentials or session files.
Reply with MC_SCRIBE_ACCEPTANCE_OK after the finish succeeds.`;
    const nativeBefore = await snapshotNativeUsage(session);
    const result = await execute(args, prompt, process.env, LIMIT_MS);
    usage = await collectLiveUsage({ harness: spec.harness, authMode: access.authMode,
      stdout: result.stdout, sessionDirectory: session, nativeBefore });
    if (sensitive.some((value) => value && (result.stdout.includes(value) || result.stderr.includes(value)))) {
      throw new Error("The adapter exposed a projected credential in its output.");
    }
    const events = result.stdout.split("\n").flatMap((line) => { try { return [JSON.parse(line)]; } catch { return []; } });
    const label = (value: unknown) => typeof value === "string" && /^[a-zA-Z0-9_.:-]{1,128}$/.test(value) ? value : undefined;
    nativeEvents = events.slice(0, 512).map(event => ({ type: label(event.type), event: label(event.event),
      error_message: event.item?.type === "error" && typeof event.item.message === "string" ? event.item.message.slice(0, 2048) : undefined,
      item_type: label(event.item?.type), server: label(event.item?.server), tool: label(event.item?.tool),
      status: label(event.item?.status ?? event.status),
      exit_code: Number.isSafeInteger(event.item?.exit_code) ? event.item.exit_code : undefined,
      content_types: Array.isArray(event.message?.content) ? event.message.content.slice(0, 32).map((block: any) => label(block.type)) : undefined,
      native_tools: Array.isArray(event.message?.content) ? event.message.content.filter((block: any) => block.type === "tool_use")
        .slice(0, 32).map((block: any) => label(block.name)) : undefined,
    }));
    const reported = events.find(event => event.event === "infrastructure-failure");
    if (reported) failure = Object.fromEntries(["category", "code", "proxy_request_id", "condition", "evidence"].flatMap(field =>
      typeof reported[field] === "string" ? [[field, reported[field].slice(0, 4096)]] : []));
    if (result.code !== 0) throw new Error("The Scribe adapter failed its live turn.");
    if (!events.some((event) => event.event === "session-start") ||
        !events.some((event) => event.event === "turn-complete" && event.status === "success")) {
      throw new Error("The live turn lacks a durable session or successful completion.");
    }
    const completed = successfulScribeTools(result.stdout, spec.harness);
    if (completed.length !== SCRIBE_TOOLS.length) throw new Error(`The live turn lacks successful tools: ${SCRIBE_TOOLS.filter((tool) => !completed.includes(tool)).join(", ")}.`);
    const files = await wikiFiles(root);
    const digest = wikiDigest(files);
    if (files.get("openwiki/reports/accepted.md")?.toString("utf8") !== ACCEPTANCE_REPORT ||
        await readFile(join(root, "source.txt"), "utf8") !== SOURCE) throw new Error("The live turn changed the accepted report or source.");
    const receipt = JSON.parse(await readFile(join(temporary, "openwiki-completion.json"), "utf8"));
    const receiptKeys = ["format", "status", "input_commit", "source_commit", "wiki_digest", "openwiki_version", "openwiki_revision"];
    if (Object.keys(receipt).length !== receiptKeys.length || Object.keys(receipt).some((key) => !receiptKeys.includes(key)) ||
        receipt.format !== 1 || receipt.status !== "complete" || receipt.input_commit !== input || receipt.source_commit !== input ||
        receipt.openwiki_version !== VERSION || receipt.openwiki_revision !== REVISION || receipt.wiki_digest !== digest ||
        files.has("openwiki/.run.json") || files.has("openwiki/.culler-run.json")) {
      throw new Error("The live turn lacks a valid completion receipt.");
    }
    const claims = JSON.parse(files.get("openwiki/.claims/quickstart.json")?.toString("utf8") ?? "{}");
    if (!hasScribeSourceClaim(claims)) {
      throw new Error("The generated page lacks its source Claim.");
    }
    if (!files.get("openwiki/reports/index.md")?.toString("utf8").includes("Complete accepted report")) {
      throw new Error("The wiki navigation lacks the complete report.");
    }
    const output = join(options.output, profile.id);
    for (const [path, bytes] of files) {
      if (sensitive.some((value) => value && bytes.includes(Buffer.from(value)))) throw new Error("The wiki contains a projected credential.");
      await mkdir(dirname(join(output, path)), { recursive: true });
      await writeFile(join(output, path), bytes);
    }
    await writeFile(join(output, "source.txt"), SOURCE);
    const status = JSON.parse(await accessRequest(options.home, { op: "profile_status", run_id: name, launch_id: name, nonce }));
    if (status.status !== "ok" || status.nonce !== nonce || !Number.isInteger(status.provider_requests) || status.provider_requests < 1 || status.provider_requests > 24) {
      throw new Error("The Scribe test lacks bounded host request evidence.");
    }
    const evidence = { status: "passed", binding: profile.id, harness: spec.harness, model: profile.model,
      cost, controls, provider_requests: status.provider_requests,
      image: options.image, native_events: nativeEvents, tool_calls: completed, report_sha256: createHash("sha256").update(ACCEPTANCE_REPORT).digest("hex"), receipt, usage };
    await writeFile(join(output, "result.json"), JSON.stringify(evidence, null, 2) + "\n");
    return evidence;
  } catch (error) {
    let requests: number | null = null;
    if (authorized) {
      try {
        const status = JSON.parse(await accessRequest(options.home, { op: "profile_status", run_id: name, launch_id: name, nonce }));
        if (status.status === "ok" && status.nonce === nonce && Number.isSafeInteger(status.provider_requests)) requests = status.provider_requests;
      } catch {}
    }
    const output = join(options.output, profile.id);
    await mkdir(output, { recursive: true, mode: 0o700 });
    const artifactHashes: Record<string, string> = {};
    try {
      for (const [path, bytes] of await wikiFiles(root)) {
        if (sensitive.some(value => bytes.includes(Buffer.from(value)))) throw new Error("The wiki contains its Culler token.");
        await mkdir(dirname(join(output, "failure-artifacts", path)), { recursive: true });
        await writeFile(join(output, "failure-artifacts", path), bytes);
        artifactHashes[path] = createHash("sha256").update(bytes).digest("hex");
      }
      const receiptPath = join(temporary, "openwiki-completion.json");
      const info = await lstat(receiptPath).catch(() => undefined);
      if (info?.isFile() && info.size <= 65536) {
        const receipt = await readFile(receiptPath);
        if (!sensitive.some(value => receipt.includes(Buffer.from(value)))) await writeFile(join(output, "failure-artifacts", "completion.json"), receipt);
      }
    } catch { artifactHashes.capture = "unavailable"; }
    await writeFile(join(output, "result.json"), JSON.stringify({ status: "failed", binding: profile.id,
      harness: profile.spec.harness, model: profile.model, cost, controls, provider_requests: requests,
      failure, native_events: nativeEvents, artifact_sha256: artifactHashes, usage: usage ?? { tokens: { status: "unavailable" } } }, null, 2) + "\n", { mode: 0o600 });
    throw error;
  } finally {
    try {
      if (authorized) await accessRequest(options.home, { op: "profile_revoke",
        run_id: name, launch_id: name, nonce });
    } finally {
      const cleanup = await execute(["docker", "rm", "--force", name]);
      if (cleanup.code !== 0 && !cleanup.stderr.includes("No such container")) {
        throw new Error(`The acceptance container cleanup failed: ${name}. Its private files remain at ${privateRoot}.`);
      }
      await rm(privateRoot, { recursive: true, force: true });
    }
  }
}

export function scribeHostEnv(home: string, spine?: string): Record<string, string | undefined> {
  const env = { ...process.env, MC_HOME: home };
  for (const name of ["MC_HELPER", "MC_SPINE", "MC_RUN_JSON", "OPENAI_API_KEY", "ANTHROPIC_API_KEY",
    "CODEX_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"]) delete env[name];
  if (spine) env.MC_SPINE = spine;
  return env;
}

export async function validateScribeHome(home: string, testRoot = join(process.env.HOME!, ".config/culler-tests")) {
  const root = await realpath(testRoot);
  const physical = await realpath(home);
  const inside = relative(root, physical);
  const info = await lstat(home);
  if (!inside || inside.startsWith("..") || isAbsolute(inside) || !info.isDirectory() ||
      info.uid !== process.getuid?.() || (info.mode & 0o777) !== 0o700) {
    throw new Error("The test home must be a private directory below ~/.config/culler-tests/.");
  }
}

export async function runScribeAcceptance(args: string[], existingSpine?: string) {
  const options = parseScribeAcceptanceArgs(args);
  if (existsSync("/mc/run.json")) throw new Error("The live acceptance command requires a host session.");
  if (existsSync(options.output)) throw new Error("The acceptance output directory must be new.");
  await validateScribeHome(options.home);
  if (existsSync(join(options.home, "health/provider-acceptance.sock"))) {
    throw new Error("The isolated test home already has a provider proxy socket.");
  }
  const worktree = resolve(import.meta.dir, "../..");
  const spineRoot = existingSpine ? undefined : await mkdtemp(join(options.home, ".scribe-spine-"));
  const spine = existingSpine ?? join(spineRoot!, "spine.db");
  const env = scribeHostEnv(options.home, spine);
  const runMc = async (argv: string[], input?: Uint8Array, settings?: { signal?: AbortSignal }) => {
    const result = await execute([options.mcBinary, ...argv], input ? new TextDecoder().decode(input) : "", env, 45_000, settings?.signal);
    return { exitCode: result.code, stdout: result.stdout, stderr: result.stderr };
  };
  let service: Awaited<ReturnType<typeof startForegroundProviderAccess>>;
  try {
    if (!existingSpine) await initializeLiveSpine(runMc);
    else if ((await runMc(["__system-state", "probe"])).exitCode) throw new Error("The isolated diagnostic spine is unavailable.");
    service = await startForegroundProviderAccess({ mcHome: options.home, socketHome: options.home, mcPath: options.mcBinary }, {
    runMc,
    acquireOwner: connection => acquireConnectionOwner(options.mcBinary, options.home, connection),
    });
  } catch (error) {
    if (spineRoot) await rm(spineRoot, { recursive: true, force: true });
    throw error;
  }
  let staged: string | undefined;
  try {
    const profiles = await selectProfiles(options, env);
    console.log(`The live Scribe suite permits at most ${profiles.length * 29} provider requests: ${profiles.length * 24} model requests and ${profiles.length * 5} catalog requests, with five minutes per profile.`);
    staged = await mkdtemp(join(tmpdir(), "culler-scribe-runtime-"));
    const runner = join(staged, "runner");
    const stage = await execute(["python3", join(worktree, "runner/image/stage-runtime.py"), runner]);
    if (stage.code) throw new Error("The live test could not prepare the runner release.");
    await preflight(options, runner);
    await mkdir(options.output, { mode: 0o700 });
    const results = [];
    for (const profile of profiles) {
      console.log(`The live Scribe test starts for ${profile.id}.`);
      results.push(await runProfile(options, profile, runner, worktree));
      console.log(`The live Scribe test passed for ${profile.id}.`);
    }
    await writeFile(join(options.output, "result.json"), JSON.stringify({ status: "passed", results }, null, 2) + "\n");
  } finally {
    try { await service.stop(); }
    finally {
      if (staged) await rm(staged, { recursive: true, force: true });
      if (spineRoot) await rm(spineRoot, { recursive: true, force: true });
    }
  }
}

if (import.meta.main) {
  try { await runScribeAcceptance(process.argv.slice(2)); }
  catch (error) { console.error(error instanceof Error ? error.message : "The live Scribe test failed."); process.exitCode = 1; }
}
