import { initializeLiveSpine, summarizeLiveDiagnostics, resolveLiveImage } from "./environment";
import { randomBytes } from "node:crypto";
import { createConnection } from "node:net";
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { isAbsolute, join, relative, resolve } from "node:path";
import { startForegroundProviderAccess } from "../../resident/src/provider-foreground";
import { acquireConnectionOwner } from "../../resident/src/connection-owner";
import { parseProviderAccess } from "../../runner/agent-runner/adapters/provider-access";
import { cheapestLiveModel, liveModelCost, liveModelControls } from "./models";
import { collectLiveUsage, snapshotNativeUsage, type LiveUsageEvidence } from "./usage";

const args = process.argv.slice(2);
const flags = new Set(["--confirm-live-spend", "--import-api-keys"]);
const names = new Set(["--home", "--mc-binary", "--image", "--connections", "--keys-file", "--evidence"]);
const supplied = new Map<string, string>();
for (let i = 0; i < args.length; i++) {
  const name = args[i]!;
  if (supplied.has(name) || !flags.has(name) && (!names.has(name) || !args[i + 1] || args[i + 1]!.startsWith("--"))) {
    throw new Error("The real model suite arguments are invalid.");
  }
  supplied.set(name, flags.has(name) ? "true" : args[++i]!);
}
const value = (name: string) => supplied.get(name) ?? "";
if (!supplied.has("--confirm-live-spend")) throw new Error("The real model suite requires explicit cost authorization.");
const home = resolve(value("--home"));
const binary = value("--mc-binary");
const imageSelector = value("--image");
let image = "";
const connections = value("--connections").split(",").filter(Boolean);
if (!isAbsolute(value("--home")) || !isAbsolute(binary) || !imageSelector || !connections.length || connections.length > 4 ||
    new Set(connections).size !== connections.length || connections.some(id => !/^[a-z0-9][a-z0-9_.-]{0,63}$/.test(id))) {
  throw new Error("Use an isolated test home, host binary, test image, and connection identifiers.");
}
const testRoot = await realpath(join(process.env.HOME!, ".config/culler-tests"));
const physicalHome = await realpath(home);
const inside = relative(testRoot, physicalHome);
const homeInfo = await lstat(home);
if (!inside || inside.startsWith("..") || isAbsolute(inside) || !homeInfo.isDirectory() || homeInfo.uid !== process.getuid?.() || (homeInfo.mode & 0o777) !== 0o700) {
  throw new Error("The test home must be a private directory below ~/.config/culler-tests/.");
}
try { await lstat(join(home, "health/provider-acceptance.sock")); throw new Error("The isolated home already has a proxy socket."); }
catch (error: any) { if (error.code !== "ENOENT") throw error; }
const replacementProbeLimit = supplied.has("--import-api-keys")
  ? connections.filter(id => ["proxy-test-codex-api", "proxy-test-claude-api"].includes(id)).length : 0;
const requestLimits = { model: connections.length * 3, catalog: connections.length * 5, key_replacement_probe: replacementProbeLimit };
console.log(`The real model suite permits at most ${connections.length * 8 + replacementProbeLimit} provider requests: ${requestLimits.model} model requests, ${requestLimits.catalog} catalog requests, and ${replacementProbeLimit} key replacement probes, with five minutes per connection.`);
const source = resolve(import.meta.dir, "../..");
const root = await mkdtemp("/tmp/culler-live-model-");
const spineRoot = await mkdtemp(join(home, ".live-spine-"));
const env = { ...process.env, MC_HOME: home };
for (const name of ["MC_HELPER", "MC_SPINE", "MC_RUN_JSON", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "CODEX_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CULLER_TEST_HOST_SECRET_DIR"]) delete env[name];
env.MC_SPINE = join(spineRoot, "spine.db");
const children = new Set<ReturnType<typeof Bun.spawn>>();
const containers = new Set<string>();
const secrets: string[] = [];
const evidence: Record<string, unknown>[] = [];
const imported: { connection_id: string; authorization_id: string }[] = [];
let stop: (() => Promise<void>) | undefined;
const safe = (body: string) => secrets.every(secret => !body.includes(secret));
async function command(argv: string[], input?: Uint8Array, timeout = 60_000, privateOutput = false) {
  const child = Bun.spawn(argv, { env, stdin: input ?? "ignore", stdout: "pipe", stderr: "pipe" });
  children.add(child);
  const timer = setTimeout(() => child.kill(), timeout);
  const collect = async (stream: ReadableStream<Uint8Array>) => {
    const parts: Uint8Array[] = []; let size = 0;
    for await (const part of stream) {
      size += part.length;
      if (size > 16 << 20) { child.kill(); throw new Error("The test output exceeded its limit."); }
      parts.push(part);
    }
    return Buffer.concat(parts).toString("utf8");
  };
  try {
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, collect(child.stdout), collect(child.stderr)]);
    if (!safe(stderr) || !privateOutput && !safe(stdout)) throw new Error("A provider credential escaped into command output.");
    return { exitCode, stdout, stderr };
  } finally { clearTimeout(timer); children.delete(child); }
}
const mc = async (argv: string[], input?: Uint8Array) => {
  const privateOutput = argv[0] === "agent-profile" && ["credential-read", "proxy-tls"].includes(argv[1]!);
  const result = await command([binary, ...argv], input, 60_000, privateOutput);
  if (privateOutput && result.exitCode === 0) {
    const data = JSON.parse(result.stdout);
    const values = argv[1] === "proxy-tls" ? [data.key] : [data.record?.api_key, data.record?.access_token, data.record?.id_token, data.record?.refresh_token];
    for (const value of values) if (typeof value === "string" && value && !secrets.includes(value)) secrets.push(value);
  }
  return result;
};
const json = (value: unknown) => new TextEncoder().encode(JSON.stringify(value));
async function product(argv: string[], input?: unknown) {
  const result = await mc(argv, input === undefined ? undefined : json(input));
  if (result.exitCode) throw new Error(`The product command ${argv.slice(0, 2).join(" ")} failed.`);
  return JSON.parse(result.stdout);
}
async function control(request: Record<string, unknown>) {
  const nonce = randomBytes(16).toString("hex");
  return new Promise<any>((accept, reject) => {
    const socket = createConnection(join(home, "health/provider-acceptance.sock")); let body = "";
    socket.setTimeout(30_000, () => socket.destroy(new Error("The proxy control request expired.")));
    socket.on("connect", () => socket.write(JSON.stringify({ version: 1, nonce, ...request }) + "\n"));
    socket.on("data", chunk => { body += chunk; if (body.length > 65_536) socket.destroy(new Error("The proxy control response exceeded its limit.")); });
    socket.on("error", reject);
    socket.on("end", () => { try {
      const result = JSON.parse(body);
      if (!safe(body) || result.status !== "ok" || result.nonce !== nonce) throw new Error("The proxy control response is invalid.");
      accept(result);
    } catch { reject(new Error("The proxy control response is invalid.")); } });
  });
}
async function scan(path: string, token: string): Promise<void> {
  for (const entry of await readdir(path, { withFileTypes: true })) {
    const child = join(path, entry.name);
    if (entry.isDirectory()) await scan(child, token);
    else if (entry.isFile()) {
      const body = await readFile(child, "utf8");
      if (!safe(body) || body.includes(token)) throw new Error("The native session or workspace contains a credential.");
    } else throw new Error("The native session contains an unexpected file type.");
  }
}
try {
  image = await resolveLiveImage(imageSelector, argv => command(argv));
  await initializeLiveSpine(mc);
  if (args.includes("--import-api-keys")) {
    const file = value("--keys-file") || join(process.env.HOME!, ".config/culler-tests/live-model.env");
    const info = await lstat(file);
    const parentInfo = await lstat(resolve(file, ".."));
    if (!parentInfo.isDirectory() || (parentInfo.mode & 0o777) !== 0o700) throw new Error("The key directory must have mode 0700.");
    if (!info.isFile() || (info.mode & 0o777) !== 0o600) throw new Error("The key file must have mode 0600.");
    const keys = new Map<string, string>();
    for (const line of (await readFile(file, "utf8")).split("\n")) {
      const match = line.match(/^\s*(OPENAI_API_KEY|ANTHROPIC_API_KEY)\s*=\s*(\S+)\s*$/);
      if (match) keys.set(match[1]!, match[2]!.replace(/^(['"])(.*)\1$/, "$2"));
    }
    for (const [id, template, name] of [["proxy-test-codex-api", "codex-api-key", "OPENAI_API_KEY"], ["proxy-test-claude-api", "claude-api-key", "ANTHROPIC_API_KEY"]]) {
      if (!connections.includes(id!)) continue;
      const key = keys.get(name!); if (!key) throw new Error(`The real model key file lacks ${name}.`); secrets.push(key);
      const catalog = await product(["agent-profile", "list"]);
      const existing = catalog.connections.find((row: any) => row.id === id);
      const saved = await product(["agent-profile", "connection-save"], { id, template_id: template, name: id, api_key: key,
        authorization_id: existing?.authorization_id ?? "", revision: catalog.revision, submission_id: `live-${randomBytes(12).toString("hex")}` });
      const authorization = saved.connections.find((row: any) => row.id === id)?.authorization_id;
      if (!authorization) throw new Error("The imported test key lacks a saved authorization identity.");
      imported.push({ connection_id: id!, authorization_id: authorization });
    }
  }
  const catalog = await product(["agent-profile", "runtime-catalog"]);
  const pins = JSON.parse(await readFile(join(source, "runner/agent-runner/package.json"), "utf8")).dependencies;
  const checked = await command(["docker", "run", "--rm", "--network", "none", "--user", "10002:10002", "--cap-drop", "ALL",
    "--entrypoint", "/opt/culler-runtime/bin/bun", image, "-e",
    `console.log(JSON.stringify(Object.fromEntries(${JSON.stringify(Object.keys(pins))}.map(name => [name, require('/app/node_modules/' + name + '/package.json').version]))))`]);
  if (checked.exitCode || JSON.stringify(JSON.parse(checked.stdout)) !== JSON.stringify(pins)) throw new Error("The image has different native harness versions.");
  const stage = join(root, "runner");
  if ((await command(["python3", join(source, "runner/image/stage-runtime.py"), stage])).exitCode) throw new Error("The exact runner release could not be staged.");
  const service = await startForegroundProviderAccess({ mcHome: home, socketHome: home, mcPath: binary }, {
    runMc: mc, acquireOwner: connection => acquireConnectionOwner(binary, home, connection),
  });
  stop = service.stop;
  for (const connection of connections) {
    const spec = catalog.connections[connection];
    if (!spec) throw new Error("A selected test connection is unavailable.");
    const listed = await product(["agent-profile", "models"], { connection_id: connection });
    const model = cheapestLiveModel(spec.harness, listed.models.map((row: any) => row.id));
    const authMode = spec.kind.endsWith("-api-key") ? "api-key" : "account";
    const cost = liveModelCost(spec.harness, model, authMode, 3);
    const controls = liveModelControls(spec.harness, model, "proxy");
    console.log(JSON.stringify({ connection, planned_cost: cost, controls }));
    const name = `culler-live-${randomBytes(12).toString("hex")}`;
    const authorized = await control({ op: "profile_authorize", credential: connection, profile: "live-model", run_id: name,
      launch_id: name, model, expires_at_ms: Date.now() + 300_000, max_requests: 3 });
    const access = parseProviderAccess(JSON.stringify(authorized.proxy_config), spec.harness);
    const work = join(root, name); await mkdir(work);
    for (const part of ["session", "workspace"]) { await mkdir(join(work, part)); await chmod(join(work, part), 0o777); }
    const path = join(work, "access.json"); await writeFile(path, JSON.stringify(access), { mode: 0o644 });
    let native: string | undefined;
    let requests = 0;
    const turnUsage: LiveUsageEvidence[] = [];
    try {
      for (let turn = 0; turn < 2; turn++) {
        const container = `${name}-${turn}`; containers.add(container);
        const argv = ["docker", "run", "--rm", "--interactive", "--init", "--name", container, "--user", "10002:10002", "--cap-drop", "ALL",
          ...(process.platform === "linux" ? ["--network", "host"] : []),
          "--mount", `type=bind,src=${stage},dst=/opt/culler-runtime/app/src,readonly`,
          "--mount", `type=bind,src=${path},dst=/mc/provider-access.json,readonly`,
          "--mount", `type=bind,src=${join(work, "session")},dst=/mc/session`,
          "--mount", `type=bind,src=${join(work, "workspace")},dst=/workspace/source`,
          "-e", "HOME=/home/mc-model", "-e", "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json",
          "-e", "MC_PRIVATE_BUN=/opt/culler-runtime/bin/bun", "-e", "MC_PRIVATE_NODE=/opt/culler-runtime/bin/node",
          "--entrypoint", "/opt/culler-runtime/bin/bun", image,
          `/opt/culler-runtime/app/src/agent-runner/adapters/${spec.harness === "codex" ? "codex" : "claude"}.ts`,
          "--session-dir", "/mc/session", "--workspace", "/workspace/source", "--model", model,
          ...(spec.harness === "codex" ? ["--mode", turn ? "native" : "fresh"] : ["--channel", "claude", "--max-turns", "2"]),
          ...controls.args, ...(native ? ["--native-ref", native] : [])];
        const prompt = turn ? "Without tools, repeat the exact final reply from the previous turn."
          : "Use the shell exactly once: printf CULLER_LIVE_TOOL_OK > result.txt\nThen reply exactly CULLER_LIVE_CONTEXT_OK.";
        const nativeBefore = await snapshotNativeUsage(join(work, "session"));
        const result = await command(argv, new TextEncoder().encode(prompt), 180_000);
        const usage = await collectLiveUsage({ harness: spec.harness, authMode: access.authMode,
          stdout: result.stdout, sessionDirectory: join(work, "session"), nativeBefore });
        turnUsage.push(usage);
        containers.delete(container);
        if (result.stdout.includes(access.token) || result.stderr.includes(access.token)) throw new Error("The adapter output contains its Culler token.");
        const events = result.stdout.split("\n").flatMap(line => { try { return [JSON.parse(line)]; } catch { return []; } });
        const complete = events.filter(event => event.event === "turn-complete");
        const started = events.find(event => event.event === "session-start");
        if (result.exitCode || complete.length !== 1 || complete[0].status !== "success" || complete[0].output.trim() !== "CULLER_LIVE_CONTEXT_OK" || !started?.session_id) {
          const codes = events.filter(event => event.event === "infrastructure-failure").map(event => ({ category: event.category, code: event.code, proxy_request_id: event.proxy_request_id }));
          evidence.push({ connection, model, turn, status: "failed", exit_code: result.exitCode, codes, usage });
          throw new Error("The native harness did not complete the bounded model turn.");
        }
        if (native && started.session_id !== native) throw new Error("The native resume changed its conversation identity.");
        native = started.session_id;
        await scan(join(work, "session"), access.token); await scan(join(work, "workspace"), access.token);
      }
      if ((await readFile(join(work, "workspace/result.txt"), "utf8")) !== "CULLER_LIVE_TOOL_OK") throw new Error("The model did not create the required tool artifact.");
      requests = (await control({ op: "profile_status", run_id: name, launch_id: name })).provider_requests;
      if (!Number.isInteger(requests) || requests < 3 || requests > 3) throw new Error("The host did not prove exactly three provider requests.");
      evidence.push({ connection, harness: spec.harness, method: access.authMode, model, status: "passed", provider_requests: requests,
        native_resume: true, tool_artifact: true, cost, controls, usage: turnUsage,
        diagnostics: summarizeLiveDiagnostics(await product(["system", "diagnostics", "--connection", connection])) });
      console.log(`PASS ${connection}: ${model}, ${requests} host requests, tool and native resume.`);
    } finally { await control({ op: "profile_revoke", run_id: name, launch_id: name }); await rm(path, { force: true }); }
  }
} finally {
  const cleanupErrors: string[] = [];
  for (const child of children) child.kill();
  const cleanup = async (name: string, action: () => Promise<unknown>) => {
    try { await action(); } catch { cleanupErrors.push(name); }
  };
  for (const name of containers) await cleanup("container removal", async () => {
    if ((await command(["docker", "rm", "--force", name])).exitCode) throw new Error();
  });
  await cleanup("proxy shutdown", async () => { await stop?.(); });
  for (const connection of imported) await cleanup("test key removal", () =>
    product(["agent-profile", "connection-disconnect"], { ...connection, confirm: true }));
  const evidencePath = value("--evidence") || join(home, "live-model-evidence.json");
  await cleanup("evidence write", () => writeFile(evidencePath,
    JSON.stringify({ at: new Date().toISOString(), image, image_selector: imageSelector, request_limits: requestLimits, results: evidence, cleanup_errors: cleanupErrors }, null, 2), { mode: 0o600 }));
  await cleanup("private file removal", () => rm(root, { recursive: true, force: true }));
  await cleanup("scratch spine removal", () => rm(spineRoot, { recursive: true, force: true }));
  if (cleanupErrors.length) throw new Error(`The real model test cleanup failed: ${cleanupErrors.join(", ")}.`);
}
