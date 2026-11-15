import { initializeLiveSpine, summarizeLiveDiagnostics } from "./environment";
import { randomBytes } from "node:crypto";
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { startForegroundProviderAccess } from "../../resident/src/provider-foreground";
import { acquireConnectionOwner } from "../../resident/src/connection-owner";
import { cheapestLiveModel, liveModelCost } from "./models";
import { parseScribeAcceptanceArgs, runScribeAcceptance, scribeHostEnv, validateScribeHome } from "./scribe";

const args = process.argv.slice(2);
const reuseIndex = args.indexOf("--reuse-profile-tests");
const reusedTests = reuseIndex < 0 ? [] : args[reuseIndex + 1]?.split(",") ?? [];
if (reuseIndex >= 0) {
  if (reusedTests.length !== 2 || reusedTests.some(id => id !== "-" && !/^scribe-model-[a-f0-9]{16}-[01]$/.test(id))) {
    throw new Error("Select the two successful isolated profile test identifiers.");
  }
  args.splice(reuseIndex, 2);
}
const reusedCount = reusedTests.filter(id => id !== "-").length;
const selected = args.indexOf("--connections");
const connections = selected < 0 ? [] : args[selected + 1]?.split(",") ?? [];
if (connections.length !== 2 || connections.some(id => !/^[a-z0-9][a-z0-9_.-]{0,63}$/.test(id))) {
  throw new Error("Select one dedicated Codex connection and one dedicated Claude connection.");
}
args.splice(selected, 2);
const suffix = randomBytes(8).toString("hex");
const profileIDs = [`scribe-codex-${suffix}`, `scribe-claude-${suffix}`];
const options = parseScribeAcceptanceArgs([...args, "--codex-binding", profileIDs[0]!, "--claude-binding", profileIDs[1]!]);
await validateScribeHome(options.home);
if (await lstat(join(options.home, "health/provider-acceptance.sock")).then(() => true, error => { if (error.code === "ENOENT") return false; throw error; })) {
  throw new Error("The isolated test home already has a provider proxy socket.");
}
if (await lstat(options.output).then(() => true, error => { if (error.code === "ENOENT") return false; throw error; })) {
  throw new Error("The acceptance output directory must be new.");
}
const env = scribeHostEnv(options.home);
delete env.CULLER_TEST_HOST_SECRET_DIR;
env.CULLER_TEST_PROVIDER_IMAGE = options.image;
const source = resolve(import.meta.dir, "../..");
const privateRoot = await mkdtemp(join(options.home, ".scribe-profile-"));
const spine = join(privateRoot, "spine.db");
env.MC_SPINE = spine;
const configPath = join(options.home, "config.toml");
const originalConfig = await readFile(configPath);
await writeFile(join(privateRoot, "original-config.toml"), originalConfig, { mode: 0o600 });
let expectedConfig = originalConfig;
const runner = join(options.home, "release/runner");
const backupRunner = join(privateRoot, "original-runner");
let hadRunner = false, installedRunner = false;
let service: Awaited<ReturnType<typeof startForegroundProviderAccess>> | undefined;
const operations: string[] = [];
const evidence: Record<string, unknown>[] = [];
const cleanupErrors: string[] = [];
let completed = false;
async function command(argv: string[], input?: Uint8Array, signal?: AbortSignal) {
  const child = Bun.spawn(argv, { env, stdin: input ?? "ignore", stdout: "pipe", stderr: "pipe",
    signal: AbortSignal.any([AbortSignal.timeout(60_000), ...(signal ? [signal] : [])]) });
  async function collect(stream: ReadableStream<Uint8Array>) {
    let total = 0; const parts: Uint8Array[] = [];
    for await (const part of stream) {
      total += part.byteLength;
      if (total > 1 << 20) { child.kill(); throw new Error("The host test response exceeded its limit."); }
      parts.push(part);
    }
    return Buffer.concat(parts).toString("utf8");
  }
  const [exitCode, stdout, stderr] = await Promise.all([child.exited, collect(child.stdout), collect(child.stderr)]);
  return { exitCode, stdout, stderr };
}
const runMc = async (argv: string[], input?: Uint8Array, settings?: { signal?: AbortSignal }) => {
  const result = await command([options.mcBinary, ...argv], input, settings?.signal);
  if (result.exitCode && argv[0] === "connection" && argv[1] === "proxy-observe") {
    const message = JSON.parse(result.stdout)?.error?.message;
    if (typeof message === "string" && message.length < 500 && !/[\r\n]/.test(message)) console.error(`The isolated diagnostic write failed: ${message}`);
  }
  return result;
};
async function product(argv: string[], input?: unknown) {
  const result = await runMc(argv, input === undefined ? undefined : new TextEncoder().encode(JSON.stringify(input)));
  if (result.exitCode) {
    if (argv[0] === "agent-profile" && argv[1] === "models") {
      const response = JSON.parse(result.stdout);
      const message = response?.error?.message;
      if (typeof message === "string" && message.length < 500 && !/[\r\n]/.test(message)) {
        throw new Error(`The model catalog failed: ${message}`);
      }
    }
    throw new Error(`The isolated ${argv.slice(0, 2).join(" ")} command failed.`);
  }
  return JSON.parse(result.stdout);
}
async function protect(path: string): Promise<void> {
  const info = await lstat(path);
  if (info.isSymbolicLink()) throw new Error("The runner fixture contains a symbolic link.");
  await chmod(path, info.isDirectory() ? 0o700 : 0o600);
  if (info.isDirectory()) for (const name of await readdir(path)) await protect(join(path, name));
}
async function cleanup(name: string, action: () => Promise<unknown>) {
  try { await action(); } catch { cleanupErrors.push(name); }
}
const profileCount = options.harness ? 1 : 2;
const selectedReuseCount = options.harness ? Number(reusedTests[options.harness === "codex" ? 0 : 1] !== undefined && reusedTests[options.harness === "codex" ? 0 : 1] !== "-") : reusedCount;
const newProfileRequests = (profileCount - selectedReuseCount) * 3;
console.log(`The isolated Scribe acceptance permits at most ${profileCount * 34 + newProfileRequests} provider requests: ${newProfileRequests} new profile model requests, ${profileCount * 24} Scribe requests, and ${profileCount * 10} model catalog requests.`);
try {
  await mkdir(options.output, { mode: 0o700 });
  const release = join(options.home, "release");
  await mkdir(release, { recursive: true, mode: 0o700 });
  try {
    const info = await lstat(runner);
    if (!info.isDirectory()) throw new Error("The isolated runner path is invalid.");
    await rename(runner, backupRunner); hadRunner = true;
  } catch (error: any) { if (error.code !== "ENOENT") throw error; }
  installedRunner = true;
  const stage = await command(["python3", join(source, "runner/image/stage-runtime.py"), runner]);
  if (stage.exitCode) throw new Error("The exact host runner could not be staged.");
  await protect(runner);
  await initializeLiveSpine(runMc);
  service = await startForegroundProviderAccess({ mcHome: options.home, socketHome: options.home, mcPath: options.mcBinary }, {
    runMc, acquireOwner: connection => acquireConnectionOwner(options.mcBinary, options.home, connection),
  });
  for (let index = 0; index < connections.length; index++) {
    const harness = index === 0 ? "codex" : "claude-sdk";
    if (options.harness && options.harness !== harness) continue;
    const catalog = await product(["agent-profile", "list"]);
    const connection = catalog.connections.find((row: any) => row.id === connections[index]);
    if (!connection || connection.harness !== harness || connection.access_status !== "connected") {
      throw new Error("The selected connection requires dedicated provider access.");
    }
    const listed = await product(["agent-profile", "models"], { connection_id: connection.id });
    const model = cheapestLiveModel(harness, listed.models.map((row: any) => row.id), Date.now(), "scribe");
    const authMode = connection.kind.endsWith("-api-key") ? "api-key" : "account";
    const cost = liveModelCost(harness, model, authMode, 3);
    const reused = reusedTests[index] && reusedTests[index] !== "-" ? await product(["agent-profile", "operation-status"], { operation_id: reusedTests[index] }) : undefined;
    if (reused && (reused.status !== "succeeded" || reused.profile?.connection_id !== connection.id || reused.profile?.model !== model)) {
      throw new Error("The retained profile test does not match this connection and economical model.");
    }
    if (reused) profileIDs[index] = reused.profile.id;
    console.log(JSON.stringify(reused ? { profile: profileIDs[index], reused_model_test: reused.operation_id, provider_request_limit: 0 }
      : { profile: profileIDs[index], planned_cost: cost,
          controls: "The product model test verifies the selected model with its normal native defaults." }));
    const profile = { ...(reused?.profile ?? {}), id: profileIDs[index], name: reused?.profile.name ?? `Scribe acceptance ${harness} ${suffix}`,
      connection_id: connection.id, model, enabled: true, revision: catalog.revision, submission_id: `scribe-${suffix}-${index}` };
    const operation = reused?.operation_id ?? `scribe-model-${suffix}-${index}`;
    let result: any = reused;
    if (!reused) {
      operations.push(operation);
      await product(["agent-profile", "operation-start"], { operation_id: operation, kind: "model_test", profile, confirm_spend: true });
      const deadline = Date.now() + 300_000;
      for (;;) {
        result = await product(["agent-profile", "operation-status"], { operation_id: operation });
        if (["succeeded", "failed", "cancelled"].includes(result.status)) break;
        if (Date.now() >= deadline) throw new Error("The profile model test exceeded five minutes.");
        await Bun.sleep(500);
      }
    }
    evidence.push({ profile: profileIDs[index], operation_id: operation, executed_this_attempt: !reused, connection: connection.id, model, cost, status: result.status,
      error_code: result.error_code ?? null, provider_requests: result.provider_requests ?? null,
      usage: { billing: authMode === "api-key" ? "api-charges" : "subscription-allowance", tokens: { status: "unavailable" },
        limitation: "The product model test returns request counts and removes its private native session." } });
    if (result.status !== "succeeded") throw new Error(`The profile model test failed: ${result.error_code ?? "unavailable"}.`);
    const saved = await product(["agent-profile", "save"], { ...profile, test_id: operation });
    expectedConfig = await readFile(configPath);
    if (!saved.profiles.some((row: any) => row.id === profile.id && row.enabled && row.model === model)) {
      throw new Error("The tested profile was not saved as enabled.");
    }
  }
  await service.stop(); service = undefined;
  await runScribeAcceptance([...args,
    "--codex-binding", profileIDs[0]!, "--claude-binding", profileIDs[1]!,
  ].map((arg, index, all) => index > 0 && all[index - 1] === "--output" ? join(options.output, "scribe") : arg), spine);
  completed = true;
} finally {
  const diagnostics: Record<string, unknown>[] = [];
  for (const connection of connections) await cleanup("diagnostic evidence", async () => {
    const result = await product(["system", "diagnostics", "--connection", connection]);
    diagnostics.push({ connection, events: summarizeLiveDiagnostics(result) });
  });
  await cleanup("diagnostic evidence write", () => writeFile(join(options.output, "diagnostics.json"), JSON.stringify(diagnostics, null, 2), { mode: 0o600 }));
  for (const operation of operations) await cleanup("model test cancellation", () =>
    product(["agent-profile", "operation-cancel"], { operation_id: operation }));
  await cleanup("proxy shutdown", async () => { await service?.stop(); });
  await cleanup("profile configuration restoration", async () => {
    if (!(await readFile(configPath)).equals(expectedConfig)) throw new Error("The test configuration changed concurrently.");
    const temporary = join(options.home, `.scribe-config-${suffix}`);
    await writeFile(temporary, originalConfig, { mode: 0o600, flag: "wx" });
    await rename(temporary, configPath);
  });
  await cleanup("runner restoration", async () => {
    if (installedRunner) await rm(runner, { recursive: true, force: true });
    if (hadRunner) await rename(backupRunner, runner);
  });
  await cleanup("evidence write", () => writeFile(join(options.output, "profiles.json"), JSON.stringify({
    status: completed ? "passed" : "failed", image: options.image, profiles: evidence, cleanup_errors: cleanupErrors,
  }, null, 2), { mode: 0o600 }));
  if (!cleanupErrors.length) await rm(privateRoot, { recursive: true, force: true });
  if (cleanupErrors.length) throw new Error(`The isolated acceptance cleanup failed: ${cleanupErrors.join(", ")}. Private recovery files remain at ${privateRoot}.`);
}
