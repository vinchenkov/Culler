import { lstat, mkdir, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { acquireConnectionOwner } from "./connection-owner";
import { readAgentProfileRuntimeCatalog } from "./agent-profile-runtime";
import { startCredentialProjector } from "./credential-projector";
import { startProviderAcceptanceServer } from "./provider-acceptance";
import { createProviderCredentials } from "./provider-credentials";
import { ProviderCredentialError } from "./provider-proxy";
import { makeLogger } from "../../runner/shared/log";
import type { Exec } from "./types";

interface ForegroundConfig { mcHome: string; socketHome: string; mcPath: string }
interface ForegroundDependencies {
  runMc: Exec;
  acquireOwner(connection: string): Promise<() => Promise<void>>;
}

export async function startForegroundProviderAccess(config: ForegroundConfig, deps: ForegroundDependencies) {
  const catalog = await readAgentProfileRuntimeCatalog(deps.runMc);
  const certificate = await deps.runMc(["agent-profile", "proxy-tls"]);
  if (certificate.exitCode !== 0) throw new Error("The host proxy certificate is unavailable.");
  const tls = JSON.parse(certificate.stdout);
  const observe = async (value: Record<string, unknown>) => {
    const saved = await deps.runMc(["connection", "proxy-observe"], new TextEncoder().encode(JSON.stringify(value)));
    if (saved.exitCode !== 0) throw new Error("The host could not retain the connection diagnostic.");
  };
  const credentials = createProviderCredentials({ runMc: deps.runMc, acquireOwner: deps.acquireOwner, observe });
  const check = async (connection: string) => {
    const checkId = crypto.randomUUID().replaceAll("-", "");
    const started = await deps.runMc(["connection", "check-start"], new TextEncoder().encode(JSON.stringify({ connection_id: connection, check_id: checkId })));
    if (started.exitCode !== 0) throw new Error("The connection status is unavailable.");
    const reservation = JSON.parse(started.stdout);
    if (!reservation.allowed) throw new Error("The connection cannot start an access check.");
    let result: Record<string, unknown>;
    let failure: unknown;
    try {
      const snapshot = await credentials.renew(connection);
      result = { outcome: "ready", ...(snapshot.record.expires_at_ms ? { expires_at: new Date(snapshot.record.expires_at_ms).toISOString() } : {}) };
    } catch (error) {
      failure = error;
      if (!(error instanceof ProviderCredentialError) || error.origin !== "provider_renewal") {
        const cancelled = await deps.runMc(["connection", "check-cancel"], new TextEncoder().encode(JSON.stringify({
          connection_id: connection, check_id: checkId, authorization_id: reservation.authorization_id, operation: reservation.operation,
        })));
        if (cancelled.exitCode !== 0) throw new Error("The connection check could not close its reservation. Restore the host service, then retry.");
        throw new Error("The host credential store is unavailable.");
      }
      result = { outcome: "failed", provider_error_code: error instanceof ProviderCredentialError ? error.code : "credential_store" };
    }
    const saved = await deps.runMc(["connection", "observe"], new TextEncoder().encode(JSON.stringify({
      connection_id: connection, check_id: checkId, authorization_id: reservation.authorization_id,
      event_id: crypto.randomUUID().replaceAll("-", ""), operation: reservation.operation,
      observed_at: new Date().toISOString(), ...result,
    })));
    if (saved.exitCode !== 0 || failure) throw new Error("The connection access check failed.");
  };
  const handle = startCredentialProjector({ credentials, tls, observe, refreshConnection: check }, catalog);
  let registration: string | undefined;
  try {
    const health = join(config.mcHome, "health");
    await mkdir(health, { recursive: true, mode: 0o700 });
    const parent = await lstat(health);
    if (!parent.isDirectory() || parent.uid !== process.getuid?.() || (parent.mode & 0o777) !== 0o700) {
      throw new Error("The temporary proxy registry is unavailable.");
    }
    const registry = join(health, "provider-proxies");
    await mkdir(registry, { recursive: true, mode: 0o700 });
    const directory = await lstat(registry);
    if (!directory.isDirectory() || directory.uid !== process.getuid?.() || (directory.mode & 0o777) !== 0o700) {
      throw new Error("The temporary proxy registry is unavailable.");
    }
    registration = join(registry, `${crypto.randomUUID()}.json`);
    await writeFile(registration, JSON.stringify({ socket_home: await realpath(config.socketHome) }), { mode: 0o600, flag: "wx" });
    const control = await startProviderAcceptanceServer(config.socketHome, "", {
      authorize: async request => {
        const release = await deps.acquireOwner(request.credential);
        try {
          await credentials.read(request.credential);
          await handle.reload(await readAgentProfileRuntimeCatalog(deps.runMc));
          const projection = handle.authorizeConnection(request.credential, request.profile, {
            runId: request.run_id, launchId: request.launch_id, expiresAt: request.expires_at_ms,
            model: request.model, profileRevision: request.profile_revision,
          }, request.max_requests, request.purpose);
          return JSON.parse(projection.proxyConfig!);
        } finally { await release(); }
      },
      revoke: (run, launch) => handle.projector.revoke(run, launch),
      revokeConnection: connection => handle.revokeConnection(connection),
      status: (run, launch) => handle.status(run, launch),
      check,
    }, makeLogger({ comp: "resident", level: "error", sink: { write() {} } }));
    return { async stop() {
      handle.stop();
      const drain = credentials.drain();
      await Promise.all([control.stop(), rm(registration!, { force: true }), drain]);
    } };
  } catch (error) {
    handle.stop();
    if (registration) await rm(registration, { force: true });
    throw error;
  }
}

async function boundedText(stream: ReadableStream<Uint8Array>): Promise<string> {
  const reader = stream.getReader();
  const parts: Uint8Array[] = [];
  let bytes = 0;
  for (;;) {
    const next = await reader.read();
    if (next.done) break;
    bytes += next.value.byteLength;
    if (bytes > 262_144) { await reader.cancel(); throw new Error("The host response exceeded its bound."); }
    parts.push(next.value);
  }
  return Buffer.concat(parts).toString("utf8");
}

async function main() {
  const args = process.argv.slice(2);
  if (args.length !== 6 || args[0] !== "--home" || args[2] !== "--socket-home" || args[4] !== "--mc") throw new Error("The host proxy arguments are invalid.");
  const config = { mcHome: args[1]!, socketHome: args[3]!, mcPath: args[5]! };
  const scratch = await realpath(config.socketHome);
  const info = await lstat(scratch);
  if (dirname(scratch) !== await realpath("/tmp") || !/^culler-proxy-[A-Za-z0-9]+$/.test(basename(scratch)) ||
      !info.isDirectory() || info.uid !== process.getuid?.() || (info.mode & 0o777) !== 0o700 ||
      (await readdir(scratch)).length !== 0 || scratch === await realpath(config.mcHome)) throw new Error("The temporary host directory is invalid.");
  const env = { ...process.env, MC_HOME: config.mcHome };
  delete env.MC_SPINE;
  delete env.MC_RUN_JSON;
  const abort = new AbortController();
  const runMc: Exec = async (argv, input, options) => {
    const child = Bun.spawn([config.mcPath, ...argv], { env, stdin: input ?? "ignore", stdout: "pipe", stderr: "pipe",
      signal: AbortSignal.any([abort.signal, AbortSignal.timeout(45_000), ...(options?.signal ? [options.signal] : [])]) });
    try {
      const [stdout, stderr, exitCode] = await Promise.all([boundedText(child.stdout), boundedText(child.stderr), child.exited]);
      return { stdout, stderr, exitCode };
    } catch { child.kill(); await child.exited; throw new Error("The host command failed."); }
  };
  let stop: (() => Promise<void>) | undefined;
  let finished = false;
  const finish = async () => {
    if (finished) return;
    finished = true;
    let status = 0;
    try { await stop?.(); }
    catch { status = 1; process.stderr.write("The host could not save renewed credentials before shutdown. Restore credential access, then retry.\n"); }
    abort.abort();
    await rm(scratch, { recursive: true, force: true });
    process.exit(status);
  };
  const deadline = setTimeout(() => void finish(), 20 * 60_000);
  process.on("SIGTERM", () => void finish());
  process.on("SIGINT", () => void finish());
  void (async () => { for await (const _ of Bun.stdin.stream()) {} await finish(); })();
  try {
    const handle = await startForegroundProviderAccess(config, {
      runMc, acquireOwner: connection => acquireConnectionOwner(config.mcPath, config.mcHome, connection),
    });
    stop = handle.stop;
    if (finished) { await handle.stop(); return; }
    process.stdout.write('{"ready":true}\n');
  } catch {
    clearTimeout(deadline);
    abort.abort();
    await stop?.();
    await rm(scratch, { recursive: true, force: true });
    process.stderr.write("The temporary host provider proxy could not start.\n");
    process.exit(1);
  }
}

if (import.meta.main) await main();
