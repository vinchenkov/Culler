import { createHash } from "node:crypto";
import protocols from "./provider-protocols.json";
import type { CredentialProjection, CredentialProjector, CredentialRefusal, ProviderLaunch, RuntimeCarrier } from "./types";
import { startProviderProxy, type ProviderProxyHandle, type ProviderRoute, type ProviderRequestObservation } from "./provider-proxy";
import type { createProviderCredentials } from "./provider-credentials";

export interface OpenCodeModel {
  id: string;
  address: string;
  package: string;
  key_placement: "bearer" | "x-api-key" | "x-goog-api-key";
  model_location: "body" | "path";
  variants: string[];
  /** The model record that OpenCode reads for a live model. */
  record?: string;
}
export interface OpenCodeProvider {
  id: string;
  key_variable: string;
  web_search: boolean;
  models: OpenCodeModel[];
}
export interface RuntimeBindingSpec {
  opencode?: OpenCodeProvider;
  harness: string;
  credential: string;
  channel: "claude" | "codex" | "opencode";
  kind: string;
  inference_url: string;
  token_url?: string;
  client_id?: string;
  model?: string;
  required?: boolean;
}

export type RuntimeBindingAuthorities = Record<string, { token_url: string; client_id: string }>;

export function parseRuntimeCatalog(value: unknown): Record<string, RuntimeBindingSpec> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("The runtime catalog is invalid.");
  const out: Record<string, RuntimeBindingSpec> = {};
  const fields = new Set(["harness", "credential", "channel", "kind", "inference_url", "token_url", "client_id", "model", "required", "opencode"]);
  for (const [id, raw] of Object.entries(value)) {
    if (!raw || typeof raw !== "object" || Array.isArray(raw) || Object.keys(raw).some(key => !fields.has(key))) throw new Error("The runtime catalog entry is invalid.");
    const row = raw as RuntimeBindingSpec;
    if (!/^(?:connection:)?[a-z0-9][a-z0-9_.-]{0,63}$/.test(id) || !/^[a-z0-9][a-z0-9_.-]{0,63}$/.test(row.credential) ||
        !["codex", "claude", "opencode"].includes(row.channel) || row.harness !== (row.channel === "claude" ? "claude-sdk" : row.channel) ||
        ![`${row.channel}-oauth`, `${row.channel}-api-key`].includes(row.kind)) throw new Error("The runtime catalog connection is invalid.");
    if (row.channel === "opencode") {
      if (row.kind !== "opencode-api-key" || row.inference_url !== "") throw new Error("The OpenCode connection is invalid.");
      out[id] = structuredClone(row);
      continue;
    }
    const target = new URL(row.inference_url);
    if (target.protocol !== "https:" || target.username || target.password || target.search || target.hash) throw new Error("The runtime catalog destination is invalid.");
    out[id] = { ...row };
  }
  return out;
}

export function resolveRuntimeBindings(overrides: RuntimeBindingAuthorities | undefined, catalog: Record<string, RuntimeBindingSpec>): Record<string, RuntimeBindingSpec> {
  const out = structuredClone(catalog);
  for (const [binding, authority] of Object.entries(overrides ?? {})) {
    const selected = out[binding];
    if (!selected) continue;
    for (const row of Object.values(out)) if (row.credential === selected.credential) Object.assign(row, authority);
  }
  return out;
}

const openCodeRefusals = ["The release catalog lacks this provider. Select another provider.", "The connection does not offer this model. Select another model."];

// One route serves the models of a connection that share an origin, a key placement, and a model location.
// A change of the model list preserves the routes of the other models and the access of their active runs.
function openCodeRoute(credential: string, model: OpenCodeModel): { key: string; route: ProviderRoute } {
  const route: ProviderRoute = { baseURL: new URL(model.address).origin,
    openCode: { keyPlacement: model.key_placement, modelLocation: model.model_location },
    operations: [{ id: "model", method: "POST", path: "/" }] };
  return { key: `${credential}:${createHash("sha256").update(JSON.stringify(route)).digest("hex").slice(0, 32)}`, route };
}

function routes(catalog: Record<string, RuntimeBindingSpec>): Record<string, ProviderRoute> {
  const out: Record<string, ProviderRoute> = {};
  for (const row of Object.values(catalog)) {
    if (row.channel === "opencode" && !row.opencode) continue;
    if (row.opencode) {
      for (const model of row.opencode.models) {
        const { key, route } = openCodeRoute(row.credential, model);
        out[key] = route;
      }
      continue;
    }
    const route: ProviderRoute = {
      baseURL: row.inference_url,
      operations: structuredClone(protocols[row.channel].operations) as ProviderRoute["operations"],
      requestHeaders: protocols[row.channel].requestHeaders,
      responseHeaders: protocols[row.channel].responseHeaders,
    };
    if (out[row.credential] && JSON.stringify(out[row.credential]) !== JSON.stringify(route)) throw new Error("The connection has conflicting provider routes.");
    out[row.credential] = route;
  }
  return out;
}

export interface CredentialProjectorHandle {
  projector: CredentialProjector;
  proxy: ProviderProxyHandle;
  ready: Promise<void>;
  traceSecrets(): string[];
  refreshConnection(connection: string): Promise<void>;
  revokeConnection(connection: string): void;
  replaceTLS(tls: { ca: string; cert: string; key: string }): void;
  reload(catalog: Record<string, RuntimeBindingSpec>): Promise<void>;
  authorizeConnection(connection: string, profile: string, launch: ProviderLaunch, maxRequests?: number, purpose?: "model_list"): CredentialProjection;
  launches(): { runId: string; launchId: string }[];
  status(run: string, launch: string): { providerRequests: number } | null;
  stop(): void;
  drain(): Promise<void>;
}

export interface CredentialProjectorDeps {
  credentials: ReturnType<typeof createProviderCredentials>;
  tls: { ca: string; cert: string; key: string };
  observe?(observation: ProviderRequestObservation): Promise<void>;
  refreshConnection?(connection: string): Promise<void>;
  isLaunchCurrent?(runId: string, launchId: string, signal: AbortSignal): Promise<boolean | { allowed: boolean; attemptId?: string }>;
  fetch?: typeof fetch;
}

export function startCredentialProjector(deps: CredentialProjectorDeps, configured: Record<string, RuntimeBindingSpec>): CredentialProjectorHandle {
  let catalog = parseRuntimeCatalog(configured);
  let tls = deps.tls;
  const grants = new Map<string, { runId: string; launchId: string; authorizationId: string; managed: boolean }>();
  const proxy = startProviderProxy({ tls: { cert: deps.tls.cert, key: deps.tls.key }, routes: routes(catalog) }, {
    credential: deps.credentials.credential,
    fetch: deps.fetch,
    isLaunchCurrent: (spec, signal) => deps.isLaunchCurrent ? deps.isLaunchCurrent(spec.runId, spec.launchId, signal) : Promise.resolve(false),
    observe: async observation => {
      if (observation.origin === "provider" && observation.http_status === 401 && observation.credential_ref) deps.credentials.reject(observation.credential_ref);
      await deps.observe?.(observation);
    },
  });

  function authorize(row: RuntimeBindingSpec, profile: string, launch: ProviderLaunch, maxRequests?: number, purpose?: "model_list", managed = false): CredentialProjection {
    if (row.channel === "opencode" && !row.opencode) throw new Error(openCodeRefusals[0]);
    const key = `${launch.runId}/${launch.launchId}`;
    if (grants.has(key)) throw new Error("The container launch already has provider access.");
    const openCodeModel = row.opencode?.models.find(model => model.id === launch.model);
    if (row.opencode && !openCodeModel) throw new Error(openCodeRefusals[1]);
    const authorization = proxy.issue({
      requireLiveness: managed, runId: launch.runId, launchId: launch.launchId, connection: row.credential, route: openCodeModel ? openCodeRoute(row.credential, openCodeModel).key : row.credential,
      models: purpose === "model_list" ? [] : [launch.model],
      operations: row.opencode ? ["model"] : purpose === "model_list" ? ["models"] : row.channel === "codex" ? ["model", "compact", "models"] : ["model", "count", "models"],
      expiresAt: launch.expiresAt, ...(purpose === "model_list" || !launch.profileRevision ? {} : { profileId: profile, profileRevision: launch.profileRevision }), attemptId: launch.attemptId,
      limits: { maxBodyBytes: 16 * 1024 * 1024, maxDurationMs: 15 * 60_000, maxConcurrent: 8, requestsPerMinute: 120,
        ...(maxRequests === undefined ? {} : { maxRequests }) },
    });
    grants.set(key, { runId: launch.runId, launchId: launch.launchId, authorizationId: authorization.authorizationId, managed });
    return { env: {}, proxyConfig: JSON.stringify({ version: 1, url: proxy.url + (openCodeModel ? new URL(openCodeModel.address).pathname.replace(/\/$/, "") : ""),
      ...(openCodeModel ? { opencode: { provider: row.opencode!.id, keyVariable: row.opencode!.key_variable, webSearch: row.opencode!.web_search, model: openCodeModel } } : {}), token: authorization.token,
      ca: tls.ca, harness: row.harness, authMode: row.kind.endsWith("-api-key") ? "api-key" : "account" }) };
  }

  const projector: CredentialProjector = {
    project(runtime: RuntimeCarrier, launch: ProviderLaunch): CredentialProjection | CredentialRefusal | null {
      if (runtime.harness === "fake" && runtime.id === "fake") return null;
      const row = catalog[runtime.id];
      if (!row || row.credential !== runtime.credential || row.harness !== runtime.harness || row.channel !== runtime.channel) return { refused: "The provider connection changed. Refresh the agent profile." };
      if (!launch || !launch.model) return { refused: "The provider request requires its run and model." };
      try { return authorize(row, runtime.id, launch, undefined, undefined, true); }
      catch (error) { return { refused: error instanceof Error && openCodeRefusals.includes(error.message) ? error.message : "The provider proxy cannot authorize this container launch." }; }
    },
    has(runId, launchId) {
      const grant = grants.get(`${runId}/${launchId}`);
      return !!grant && proxy.status(grant.authorizationId) !== null;
    },
    revoke(runId, launchId) {
      for (const [key, grant] of grants) if (grant.runId === runId && (launchId === undefined || grant.launchId === launchId)) {
        proxy.revoke(grant.authorizationId);
        grants.delete(key);
      }
    },
    extend(runId, launchId, expiresAt) {
      const grant = grants.get(`${runId}/${launchId}`);
      if (grant) proxy.extend(grant.authorizationId, expiresAt);
    },
  };

  return {
    projector, proxy, ready: Promise.resolve(),
    traceSecrets: () => deps.credentials.traceSecrets(),
    revokeConnection(connection) {
      proxy.revokeConnection(connection);
      for (const [key, grant] of grants) if (proxy.status(grant.authorizationId) === null) grants.delete(key);
    },
    refreshConnection: async connection => {
      if (deps.refreshConnection) await deps.refreshConnection(connection);
      else await deps.credentials.renew(connection);
    },
    replaceTLS(next) {
      proxy.replaceTLS({ cert: next.cert, key: next.key });
      if (next.ca !== tls.ca) {
        for (const grant of grants.values()) proxy.revoke(grant.authorizationId);
        grants.clear();
      }
      tls = next;
    },
    async reload(next) {
      const checked = parseRuntimeCatalog(next);
      proxy.setRoutes(routes(checked));
      catalog = checked;
    },
    authorizeConnection(connection, profile, launch, maxRequests, purpose) {
      const row = catalog[profile] ?? catalog[`connection:${connection}`] ?? Object.values(catalog).find(row => row.credential === connection);
      if (!row) throw new Error("The provider connection is unavailable.");
      return authorize(row, profile, launch, maxRequests, purpose);
    },
    status(run, launch) {
      const grant = grants.get(`${run}/${launch}`);
      const status = grant ? proxy.status(grant.authorizationId) : null;
      return status ? { providerRequests: status.requests } : null;
    },
    launches: () => [...grants.values()].filter(grant => grant.managed).map(({ runId, launchId }) => ({ runId, launchId })),
    stop() { proxy.stop(); grants.clear(); },
    drain: () => deps.credentials.drain(),
  };
}
