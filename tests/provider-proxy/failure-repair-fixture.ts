import { mkdir, readFile, realpath, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { readAgentProfileRuntimeCatalog } from "../../resident/src/agent-profile-runtime";
import { acquireConnectionOwner } from "../../resident/src/connection-owner";
import { startCredentialProjector } from "../../resident/src/credential-projector";
import { startProviderAcceptanceServer } from "../../resident/src/provider-acceptance";
import { createProviderCredentials } from "../../resident/src/provider-credentials";
import { ProviderCredentialError } from "../../resident/src/provider-proxy";
import { makeLogger } from "../../runner/shared/log";
import { parseProviderAccess } from "../../runner/agent-runner/adapters/provider-access";
import type { Exec } from "../../resident/src/types";
import { syntheticFinalResponse, syntheticModelTestResponse } from "./native-harness";

export type ProviderFault = "none" | "credential" | "rate_limit" | "quota" | "overload" | "store" | "proxy";
export interface FailureRepairOptions {
  home: string;
  mcBinary: string;
  runMc(argv: string[], input?: unknown): Promise<any>;
  connections: { id: string; harness: "codex" | "claude-sdk" }[];
  skipInitialCheck?: boolean;
}

export async function saveSyntheticAccount(options: Pick<FailureRepairOptions, "home" | "runMc">,
  connection: { id: string; harness: "codex" | "claude-sdk"; template: string }) {
  const home = await realpath(options.home);
  if (basename(home) !== "home" || !basename(dirname(home)).startsWith("culler-smoke-state-")) {
    throw new Error("The synthetic account requires a dashboard smoke home.");
  }
  const catalog = await options.runMc(["agent-profile", "list"]);
  await options.runMc(["agent-profile", "connection-save"], { id: connection.id, name: connection.id,
    template_id: connection.template, revision: catalog.revision, submission_id: crypto.randomUUID() });
  const runtime = await options.runMc(["agent-profile", "runtime-catalog"]);
  const source = runtime.connections[connection.id];
  if (!source?.kind.endsWith("-oauth") || source.harness !== connection.harness) throw new Error("The synthetic account type differs.");
  const access = `synthetic-account-access:${connection.id}`;
  const refresh = `synthetic-account-refresh:${connection.id}`;
  const directory = join(home, "refresh-grants");
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const path = join(directory, `${connection.id}.json`);
  await writeFile(path, JSON.stringify({ version: 1, type: "account", credential: connection.id, channel: source.channel,
    token_url: source.token_url, client_id: source.client_id, access_token: access, refresh_token: refresh,
    expires_at_ms: 1, account_id: `synthetic-account:${connection.id}`, scope: "user:inference" }), { mode: 0o600 });
  await options.runMc(["agent-profile", "credential-migrate"]);
  const metadata = await readFile(path, "utf8");
  if (metadata.includes(access) || metadata.includes(refresh) || !JSON.parse(metadata).credential_ref) {
    throw new Error("The actual migration did not protect the synthetic account credentials.");
  }
}

/** The production proxy receives a controlled transport. All state changes use the actual host command. */
export async function createFailureRepairFixture(options: FailureRepairOptions) {
  const home = await realpath(options.home);
  if (basename(home) !== "home" || !basename(dirname(home)).startsWith("culler-smoke-state-")) {
    throw new Error("The fault fixture requires a dashboard smoke home.");
  }
  const connections = new Map(options.connections.map(connection => [connection.id, connection.harness]));
  const faults = new Map<string, ProviderFault>();
  const counts = new Map<string, number>();
  const modelCounts = new Map<string, number>();
  const renewalCounts = new Map<string, number>();
  const refreshTokens = new Map(options.connections.map(connection => [`synthetic-account-refresh:${connection.id}`, connection.id]));
  const headers = new Map<string, string>();
  const observations: Record<string, unknown>[] = [];
  const writes = new Set<Promise<void>>();
  const failures: unknown[] = [];
  const held = new Map<string, { release(): void; ready: Promise<void> }>();
  const releases = new Set<() => void>();
  const runMc: Exec = async (argv, input) => {
    try {
      const value = await options.runMc(argv, input ? JSON.parse(new TextDecoder().decode(input)) : undefined);
      return { exitCode: 0, stdout: JSON.stringify(value), stderr: "" };
    } catch { return { exitCode: 1, stdout: "", stderr: "The synthetic host command failed." }; }
  };
  const observe = (value: Record<string, unknown>): Promise<void> => {
    observations.push(structuredClone(value));
    const pending = options.runMc(["connection", "proxy-observe"], value).then(() => {}, error => { failures.push(error); throw error; });
    writes.add(pending);
    void pending.finally(() => writes.delete(pending)).catch(() => {});
    return pending;
  };
  const credentials = createProviderCredentials({ runMc, observe,
    acquireOwner: connection => acquireConnectionOwner(options.mcBinary, home, connection),
    fetch: (async (_url, request) => {
      const body = JSON.parse(String(request?.body));
      const connection = refreshTokens.get(body.refresh_token);
      if (!connection || body.grant_type !== "refresh_token") throw new Error("The fixture cannot renew an unknown credential.");
      const count = (renewalCounts.get(connection) ?? 0) + 1;
      renewalCounts.set(connection, count);
      const next = `synthetic-account-refresh:${connection}:${count}`;
      refreshTokens.set(next, connection);
      return Response.json({ access_token: `synthetic-account-access:${connection}:${count}`,
        refresh_token: next, expires_in: 3600, scope: "user:inference" });
    }) as typeof fetch,
  });
  const credential = credentials.credential;
  credentials.credential = async (connection, signal) => {
    const fault = faults.get(connection);
    if (fault === "store" || fault === "proxy") {
      if (fault === "proxy") await observe({ request_id: crypto.randomUUID(), connection, operation: "service", origin: "proxy",
        outcome: "failed", provider_sent: false, observed_at: new Date().toISOString(), duration_ms: 0 });
      throw new ProviderCredentialError(fault === "store" ? "credential_store" : "proxy", "synthetic_unavailable");
    }
    const current = await credential(connection, signal);
    for (const [name, value] of Object.entries(current.headers)) headers.set(`${name.toLowerCase()}:${value}`, connection);
    return current;
  };
  const catalog = await readAgentProfileRuntimeCatalog(runMc);
  for (const [id, harness] of connections) {
    const row = catalog[`connection:${id}`];
    if (!row || row.harness !== harness || !["-api-key", "-oauth"].some(kind => row.kind.endsWith(kind))) {
      throw new Error("The fixture requires the selected provider connection.");
    }
  }
  const tls = await options.runMc(["agent-profile", "proxy-tls"]);
  const check = async (connection: string) => {
    const checkId = crypto.randomUUID().replaceAll("-", "");
    const reservation = await options.runMc(["connection", "check-start"], { connection_id: connection, check_id: checkId });
    if (!reservation.allowed) throw new Error("The fixture connection check cannot start.");
    try {
      await credentials.renew(connection);
      await options.runMc(["connection", "observe"], { connection_id: connection, check_id: checkId,
        authorization_id: reservation.authorization_id, operation: reservation.operation, event_id: crypto.randomUUID().replaceAll("-", ""),
        outcome: "ready", observed_at: new Date().toISOString() });
    } catch (error) {
      await options.runMc(["connection", "check-cancel"], { connection_id: connection, check_id: checkId,
        authorization_id: reservation.authorization_id, operation: reservation.operation });
      throw error;
    }
  };
  const handle = startCredentialProjector({ tls, credentials, observe, refreshConnection: check,
    fetch: (async (_url, request) => {
      let connection: string | undefined;
      for (const [name, value] of new Headers(request?.headers)) connection ??= headers.get(`${name}:${value}`);
      if (!connection || !connections.has(connection)) throw new Error("The synthetic provider received an unknown host credential.");
      counts.set(connection, (counts.get(connection) ?? 0) + 1);
      if (request?.method === "GET") return Response.json({ data: [{ id: "fixture-model", display_name: "Fixture model" }],
        models: [{ slug: "fixture-model", display_name: "Fixture model", visibility: "list", priority: 0 }], has_more: false });
      modelCounts.set(connection, (modelCounts.get(connection) ?? 0) + 1);
      const fault = faults.get(connection) ?? "none";
      const error = { credential: [401, "invalid_api_key"], rate_limit: [429, "rate_limit_error"],
        quota: [429, "insufficient_quota"], overload: [503, "overloaded_error"] }[fault];
      if (error) return Response.json({ type: "error", error: { type: error[1], code: error[1], message: "The synthetic provider refused this request." } },
        { status: Number(error[0]), headers: { "retry-after": "1" } });
      const body = JSON.parse(Buffer.from(request?.body as Uint8Array).toString());
      const response = JSON.stringify(body).includes("mc-live-provider-tool.txt")
        ? syntheticModelTestResponse(connections.get(connection)!, body)
        : syntheticFinalResponse(connections.get(connection)!, body, "CULLER_REPAIR_OK", held.has(connection) ? "commentary" : undefined);
      const wait = held.get(connection);
      if (!wait) return response;
      held.delete(connection);
      const text = await response.text();
      const marker = connections.get(connection) === "codex" ? "event: response.completed" : "event: message_stop";
      const end = text.lastIndexOf(marker);
      if (end < 0) throw new Error("The held response has no terminal provider event.");
      return new Response(new ReadableStream<Uint8Array>({ async start(controller) {
        controller.enqueue(new TextEncoder().encode(text.slice(0, end)));
        await wait.ready;
        try { controller.enqueue(new TextEncoder().encode(text.slice(end))); controller.close(); } catch {}
      } }), { headers: response.headers });
    }) as typeof fetch,
  }, catalog);
  let control: Awaited<ReturnType<typeof startProviderAcceptanceServer>> | undefined;
  try {
    control = await startProviderAcceptanceServer(home, "", {
      authorize: async request => {
        await handle.reload(await readAgentProfileRuntimeCatalog(runMc));
        return JSON.parse(handle.authorizeConnection(request.credential, request.profile, {
          runId: request.run_id, launchId: request.launch_id, model: request.model, expiresAt: request.expires_at_ms,
          profileRevision: request.profile_revision,
        }, request.max_requests, request.purpose).proxyConfig!);
      }, revoke: (run, launch) => handle.projector.revoke(run, launch),
      revokeConnection: (connection, revision) => handle.revokeConnection(connection, revision),
      status: (run, launch) => handle.status(run, launch), check,
    }, makeLogger({ comp: "resident", level: "error", sink: { write() {} } }));
    if (!options.skipInitialCheck) for (const id of connections.keys()) await check(id);
  } catch (error) { handle.stop(); await control?.stop(); await credentials.drain(); throw error; }
  return {
    setFault(connection: string, fault: ProviderFault) {
      if (!connections.has(connection)) throw new Error("The fixture connection is unknown.");
      faults.set(connection, fault);
    },
    hold(connection: string) {
      if (!connections.has(connection) || held.has(connection)) throw new Error("The held response connection is unavailable.");
      let release!: () => void;
      const ready = new Promise<void>(resolve => { release = () => { releases.delete(release); resolve(); }; });
      releases.add(release);
      held.set(connection, { release, ready });
      return { release };
    },
    async restore(connection: string) {
      faults.set(connection, "none");
      await credentials.read(connection);
      await observe({ request_id: crypto.randomUUID(), connection, operation: "service", origin: "proxy",
        outcome: "ready", provider_sent: false, observed_at: new Date().toISOString(), duration_ms: 0 });
    },
    async authorize(connection: string, options: { profile?: string; profileRevision?: string; model: string; runId?: string; launchId?: string }) {
      const harness = connections.get(connection);
      if (!harness) throw new Error("The fixture connection is unknown.");
      await handle.reload(await readAgentProfileRuntimeCatalog(runMc));
      const projection = handle.authorizeConnection(connection, options.profile ?? "failure-repair", {
        runId: options.runId ?? crypto.randomUUID(), launchId: options.launchId ?? crypto.randomUUID(), model: options.model,
        expiresAt: Date.now() + 120_000, profileRevision: options.profileRevision,
      }, 1);
      return parseProviderAccess(projection.proxyConfig!, harness);
    },
    async flush() {
      while (writes.size) await Promise.allSettled([...writes]);
      if (failures.length) throw new Error("The actual host rejected a proxy observation.");
    },
    requests: (connection: string) => modelCounts.get(connection) ?? 0,
    totalRequests: (connection: string) => counts.get(connection) ?? 0,
    renewals: (connection: string) => renewalCounts.get(connection) ?? 0,
    observations: () => structuredClone(observations),
    async stop() { handle.stop(); for (const release of releases) release(); await control?.stop(); await credentials.drain(); headers.clear(); },
  };
}
