import { createHash, randomBytes, randomUUID } from "node:crypto";
import { networkErrorFromFetch, type NetworkErrorClass } from "../../runner/shared/network-error";

export interface ProviderOperation {
  id: string;
  method: "GET" | "POST";
  path: string;
  queryKeys?: string[];
  modelRequired?: boolean;
  /** Additional model fields use object paths, such as ["config", "model"]. */
  modelFields?: string[][];
}

export interface ProviderRoute {
  baseURL: string;
  operations: ProviderOperation[];
  requestHeaders?: string[];
  responseHeaders?: string[];
}

export interface ProviderCredential {
  headers: Record<string, string>;
  authorizationRevision: string;
  credentialRef: string;
  /** Include secrets that can appear in provider errors but are absent from the headers. */
  secrets?: string[];
}

export type ProviderFailureOrigin = "proxy_authorization" | "proxy_limit" | "proxy" | "credential_store" |
  "provider_renewal" | "provider_network" | "provider" | "client";

export class ProviderCredentialError extends Error {
  constructor(public readonly origin: "credential_store" | "provider_renewal" | "proxy", public readonly code: string) {
    super("The host cannot supply the connection credential.");
  }
}

export interface ProviderRequestObservation {
  request_id: string;
  authorization_id?: string;
  run_id?: string;
  launch_id?: string;
  connection?: string;
  authorization_revision?: string;
  credential_ref?: string;
  profile_id?: string;
  profile_revision?: string;
  attempt_id?: string;
  operation?: string;
  origin: ProviderFailureOrigin;
  /** Response completion describes transport, not a completed agent turn. */
  outcome: "rejected" | "response_complete" | "interrupted";
  provider_sent: boolean;
  http_status?: number;
  provider_error_code?: string;
  provider_request_id?: string;
  retry_after?: string;
  network_error?: NetworkErrorClass;
  observed_at: string;
  duration_ms: number;
}

export interface ProviderRequestLimits {
  maxBodyBytes: number;
  maxDurationMs: number;
  maxConcurrent: number;
  requestsPerMinute: number;
  maxRequests?: number;
}

export interface ProviderRunAuthorization {
  runId: string;
  launchId: string;
  connection: string;
  route: string;
  models: string[];
  operations: string[];
  expiresAt: number;
  limits: ProviderRequestLimits;
  profileId?: string;
  profileRevision?: string;
  attemptId?: string;
  requireLiveness?: boolean;
}

export interface ProviderProxyOptions {
  tls: { cert: string; key: string };
  port?: number;
  /** Routes come from trusted host configuration, never the request or run envelope. */
  routes: Record<string, ProviderRoute>;
}

export interface ProviderProxyDependencies {
  credential(connection: string, signal: AbortSignal): Promise<ProviderCredential>;
  isLaunchCurrent?(spec: ProviderRunAuthorization, signal: AbortSignal): Promise<boolean>;
  observe?(observation: ProviderRequestObservation): Promise<void> | void;
  now?(): number;
  /** Tests can replace transport without an address override in runtime configuration. */
  fetch?: typeof fetch;
}

export interface ProviderProxyHandle {
  url: string;
  setRoutes(routes: Record<string, ProviderRoute>): void;
  replaceTLS(tls: { cert: string; key: string }): void;
  status(authorizationId: string): { requests: number; modelRequests: number; active: number } | null;
  issue(authorization: ProviderRunAuthorization): { token: string; authorizationId: string };
  extend(authorizationId: string, expiresAt: number): boolean;
  revoke(authorizationId: string): void;
  revokeConnection(connection: string): void;
  stop(): void;
}

interface Authorization {
  id: string;
  digest: string;
  spec: ProviderRunAuthorization;
  active: Set<AbortController>;
  arrivals: number[];
  requests: number;
  modelRequests: number;
  timer?: ReturnType<typeof setTimeout>;
}

const forbiddenHeaders = /^(?:authorization|proxy-authorization|host|cookie|set-cookie|connection|keep-alive|transfer-encoding|upgrade|trailer|te|content-length|content-encoding|location|forwarded|x-forwarded-.*|x-culler-.*|.*(?:api-key|api_key|auth-token|access-token|account|organization|workspace|base-url|endpoint).*)$/i;
const defaultRequestHeaders = ["accept", "content-type", "user-agent"];
const defaultResponseHeaders = ["content-type", "retry-after", "request-id", "x-request-id"];
const maxErrorBytes = 65_536;
const maxSecretBytes = 16_384;

function digest(token: string): string {
  return createHash("sha256").update(token).digest("hex");
}

function positiveInteger(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}

function identifier(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,255}$/.test(value);
}

function canonicalPath(value: string): boolean {
  return /^\/(?:[A-Za-z0-9_-]+\/?)*$/.test(value) && !value.includes("//");
}

function validateRoutes(input: Record<string, ProviderRoute>): Record<string, ProviderRoute> {
  const routes: Record<string, ProviderRoute> = Object.create(null);
  for (const [id, candidate] of Object.entries(input)) {
    const route = structuredClone(candidate);
    const url = new URL(route.baseURL);
    if (!identifier(id) || url.protocol !== "https:" || url.username || url.password || url.search || url.hash ||
        !canonicalPath(url.pathname) || !route.operations.length) {
      throw new Error("The provider route is invalid.");
    }
    const operations = new Set<string>();
    const paths = new Set<string>();
    for (const operation of route.operations) {
      const path = `${operation.method} ${operation.path}`;
      if (!identifier(operation.id) || operations.has(operation.id) || paths.has(path) ||
          !["GET", "POST"].includes(operation.method) || !canonicalPath(operation.path) ||
          operation.queryKeys?.some(key => !/^[a-zA-Z][a-zA-Z0-9_]*$/.test(key)) ||
          operation.modelFields?.some(fields => !fields.length || fields.some(field => !identifier(field)))) {
        throw new Error("The provider operation is invalid.");
      }
      operations.add(operation.id);
      paths.add(path);
    }
    for (const name of [...(route.requestHeaders ?? []), ...(route.responseHeaders ?? [])]) {
      if (!/^[a-zA-Z0-9-]+$/.test(name) || forbiddenHeaders.test(name)) {
        throw new Error("The provider header rule is invalid.");
      }
    }
    route.baseURL = url.href.replace(/\/$/, "");
    routes[id] = route;
  }
  return routes;
}

function safeCode(value: unknown, secrets: string[] = []): string | undefined {
  return typeof value === "string" && /^[a-zA-Z][a-zA-Z0-9_.-]{0,63}$/.test(value) &&
    !secrets.some(secret => value.includes(secret)) ? value : undefined;
}

function providerErrorCode(body: unknown, secrets: string[]): string | undefined {
  if (!body || typeof body !== "object") return undefined;
  const value = body as Record<string, unknown>;
  const error = value.error;
  if (error && typeof error === "object") {
    const fields = error as Record<string, unknown>;
    return knownProviderError(fields.code, secrets) ?? knownProviderError(fields.type, secrets);
  }
  return knownProviderError(error, secrets) ?? knownProviderError(value.code, secrets);
}

const providerErrorCodes = new Set([
  "invalid_api_key", "authentication_error", "authentication_failed", "invalid_grant", "access_denied",
  "insufficient_quota", "billing_error", "credit_balance_exhausted", "usage_limit_reached",
  "model_not_found", "permission_denied", "not_found_error", "permission_error", "oauth_org_not_allowed",
  "rate_limit_exceeded", "rate_limit_error", "rate_limit", "overloaded", "overloaded_error",
  "server_error", "api_error", "temporarily_unavailable", "invalid_request", "invalid_request_error",
]);

function knownProviderError(value: unknown, secrets: string[]): string | undefined {
  const code = safeCode(value, secrets);
  return code && providerErrorCodes.has(code) ? code : undefined;
}

function secretValues(credential: ProviderCredential, token: string): string[] {
  const values = [token, ...(credential.secrets ?? [])];
  for (const [name, value] of Object.entries(credential.headers)) {
    if (/authorization|api.key|token/i.test(name)) {
      values.push(value, value.replace(/^Bearer\s+/i, ""));
    }
  }
  if (values.some(value => typeof value !== "string" || !value || Buffer.byteLength(value) > maxSecretBytes)) {
    throw new Error("The host credential is invalid.");
  }
  return [...new Set(values.flatMap(value => {
    const bytes = Buffer.from(value);
    const unicode = (upper: boolean) => value.split("").map(char => {
      const hex = char.charCodeAt(0).toString(16).padStart(4, "0");
      return `\\u${upper ? hex.toUpperCase() : hex}`;
    }).join("");
    const percent = (upper: boolean) => [...bytes].map(byte => {
      const hex = byte.toString(16).padStart(2, "0");
      return `%${upper ? hex.toUpperCase() : hex}`;
    }).join("");
    return [value, JSON.stringify(value).slice(1, -1), encodeURIComponent(value),
      bytes.toString("base64"), bytes.toString("base64").replace(/=+$/, ""), bytes.toString("base64url"),
      unicode(false), unicode(true), percent(false), percent(true)];
  }))];
}

function redactJSON(value: unknown, secrets: string[], depth = 0): unknown {
  if (depth > 128) throw new Error("The provider error exceeded its nesting bound.");
  if (typeof value === "string") {
    try {
      const decoded = decodeURIComponent(value);
      const safe = redact(decoded, secrets);
      if (safe !== decoded) return safe;
    } catch { /* An error string can contain a literal percent sign. */ }
    return redact(value, secrets);
  }
  if (Array.isArray(value)) return value.map(item => redactJSON(item, secrets, depth + 1));
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([key, item]) =>
    [redact(key, secrets), redactJSON(item, secrets, depth + 1)]));
  return value;
}

class JSONEscapeNormalizer {
  private pending = "";
  private decoder = new TextDecoder();
  push(bytes: Uint8Array, final = false): Uint8Array {
    const text = this.pending + this.decoder.decode(bytes, { stream: !final });
    this.pending = "";
    let output = "";
    for (let at = 0; at < text.length; at++) {
      if (text[at] !== "\\") { output += text[at]; continue; }
      if (!final && (at + 1 === text.length || text[at + 1] === "u" && text.length - at < 6)) {
        this.pending = text.slice(at); break;
      }
      if (text[at + 1] === "u" && /^[0-9a-f]{4}$/i.test(text.slice(at + 2, at + 6))) {
        const code = parseInt(text.slice(at + 2, at + 6), 16);
        if (code >= 0xd800 && code <= 0xdbff) {
          if (!final && text.length - at < 12) { this.pending = text.slice(at); break; }
          const following = /^\\u([0-9a-f]{4})/i.exec(text.slice(at + 6));
          const low = following ? parseInt(following[1]!, 16) : 0;
          if (low >= 0xdc00 && low <= 0xdfff) {
            output += String.fromCodePoint(0x10000 + ((code - 0xd800) << 10) + low - 0xdc00);
            at += 11; continue;
          }
        }
        output += JSON.stringify(String.fromCharCode(code)).slice(1, -1);
        at += 5;
      } else if (text[at + 1] === "/") { output += "/"; at++;
      } else { output += text[at]; if (at + 1 < text.length) output += text[++at]; }
    }
    return Buffer.from(output);
  }
}

class ProviderEventEvidence {
  private line = "";
  private linePresent = false;
  private event = "";
  private data = "";
  private overflow = false;
  private decoder = new TextDecoder();
  failed = false;
  code: string | undefined;
  constructor(private secrets: string[]) {}
  push(bytes: Uint8Array, final = false): void {
    const text = this.decoder.decode(bytes, { stream: !final });
    for (let start = 0; start < text.length;) {
      const end = text.indexOf("\n", start);
      const part = text.slice(start, end < 0 ? text.length : end);
      this.linePresent ||= part.replace(/\r/g, "").length > 0;
      if (this.line.length + this.data.length + part.length > maxErrorBytes) this.overflow = true;
      this.line += part.slice(0, Math.max(0, maxErrorBytes - this.line.length - this.data.length));
      if (end < 0) break;
      if (this.line || !this.linePresent) this.consumeLine(this.line.replace(/\r$/, ""));
      this.line = "";
      this.linePresent = false;
      start = end + 1;
    }
    if (final) { if (this.line) this.consumeLine(this.line); this.consumeLine(""); this.line = ""; }
  }
  private consumeLine(line: string): void {
    if (line === "") {
      let failed = this.event === "error" || this.event === "response.failed";
      let payload: unknown;
      if (!this.overflow && this.data) {
        try {
          payload = JSON.parse(this.data);
          if (payload && typeof payload === "object") {
            const value = payload as Record<string, unknown>;
            failed ||= value.type === "error" || value.type === "response.failed";
            if (value.type === "response.failed") payload = value.response;
          }
        } catch { /* Event names still identify malformed provider errors. */ }
      }
      if (failed) { this.failed = true; this.code ??= providerErrorCode(payload, this.secrets) ?? "provider_stream_error"; }
      this.event = ""; this.data = ""; this.overflow = false;
    } else if (line.startsWith("event:")) this.event = line.slice(6).trim();
    else if (line.startsWith("data:") && !this.overflow) this.data += line.slice(5).replace(/^ /, "") + "\n";
  }
}

class SecretFilter {
  private pending = Buffer.alloc(0);
  private readonly values: Buffer[];
  constructor(values: string[]) {
    this.values = values.map(value => Buffer.from(value)).sort((a, b) => b.length - a.length);
  }
  push(value: Uint8Array, final = false): Uint8Array {
    const body = Buffer.concat([this.pending, value]);
    const result: Buffer[] = [];
    let cursor = 0;
    while (cursor < body.length) {
      let at = -1;
      let length = 0;
      for (const secret of this.values) {
        const next = body.indexOf(secret, cursor);
        if (next >= 0 && (at < 0 || next < at)) { at = next; length = secret.length; }
      }
      if (at < 0) break;
      result.push(body.subarray(cursor, at), Buffer.from("[REDACTED]"));
      cursor = at + length;
    }
    let retain = 0;
    if (!final) {
      for (const secret of this.values) {
        for (let length = Math.min(secret.length - 1, body.length - cursor); length > retain; length--) {
          if (body.subarray(body.length - length).equals(secret.subarray(0, length))) { retain = length; break; }
        }
      }
    }
    result.push(body.subarray(cursor, body.length - retain));
    this.pending = Buffer.from(body.subarray(body.length - retain));
    return Buffer.concat(result);
  }
}

function redact(value: string, secrets: string[]): string {
  return Buffer.from(new SecretFilter(secrets).push(Buffer.from(value), true)).toString();
}

function abortable<T>(operation: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) {
    void operation.catch(() => {});
    return Promise.reject(signal.reason);
  }
  return new Promise<T>((resolve, reject) => {
    const abort = () => reject(signal.reason);
    signal.addEventListener("abort", abort, { once: true });
    operation.then(resolve, reject).finally(() => signal.removeEventListener("abort", abort));
  });
}

class BodyLimit extends Error {}

async function readBounded(body: ReadableStream<Uint8Array> | null, limit: number, signal: AbortSignal): Promise<Uint8Array> {
  if (!body) return new Uint8Array();
  const reader = body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const next = await abortable(reader.read(), signal);
      if (next.done) return Buffer.concat(chunks, size);
      size += next.value.byteLength;
      if (size > limit) throw new BodyLimit();
      chunks.push(next.value);
    }
  } finally {
    void reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

function requestModels(body: Record<string, unknown>, operation: ProviderOperation): unknown[] {
  const models: unknown[] = [];
  if (body.model !== undefined || operation.modelRequired) models.push(body.model);
  for (const fields of operation.modelFields ?? []) {
    let value: unknown = body;
    for (const field of fields) {
      value = value && typeof value === "object" && !Array.isArray(value)
        ? (value as Record<string, unknown>)[field] : undefined;
    }
    if (value !== undefined) models.push(value);
  }
  return models;
}

export function startProviderProxy(options: ProviderProxyOptions, dependencies: ProviderProxyDependencies): ProviderProxyHandle {
  if (!options.tls?.cert || !options.tls?.key) throw new Error("The provider proxy requires a TLS certificate and private key.");
  let routes = validateRoutes(options.routes);
  const now = dependencies.now ?? Date.now;
  const requestProvider = dependencies.fetch ?? fetch;
  const byDigest = new Map<string, Authorization>();
  const byID = new Map<string, Authorization>();
  const byLaunch = new Map<string, string>();
  let stopped = false;

  function revoke(id: string): void {
    const held = byID.get(id);
    if (!held) return;
    byID.delete(id);
    byDigest.delete(held.digest);
    byLaunch.delete(held.spec.launchId);
    clearTimeout(held.timer);
    for (const controller of held.active) controller.abort("authorization_revoked");
  }

  function armExpiry(held: Authorization): void {
    clearTimeout(held.timer);
    const remaining = held.spec.expiresAt - now();
    if (remaining <= 0) { revoke(held.id); return; }
    held.timer = setTimeout(() => armExpiry(held), Math.min(remaining, 2_147_483_647));
    held.timer.unref();
  }

  const serverOptions = {
    hostname: "127.0.0.1",
    port: options.port ?? 0,
    tls: options.tls,
    idleTimeout: 0,
    maxRequestBodySize: 64 * 1024 * 1024,
    async fetch(request: Request): Promise<Response> {
      const requestId = randomUUID();
      const started = now();
      let held: Authorization | undefined;
      let operation: ProviderOperation | undefined;
      let credential: ProviderCredential | undefined;
      let sent = false;
      let observed = false;
      let released = false;
      let timeout: ReturnType<typeof setTimeout> | undefined;
      const controller = new AbortController();
      const abortClient = () => controller.abort("client_disconnected");
      request.signal.addEventListener("abort", abortClient, { once: true });
      if (request.signal.aborted) abortClient();

      function release(): void {
        if (released) return;
        released = true;
        clearTimeout(timeout);
        request.signal.removeEventListener("abort", abortClient);
        held?.active.delete(controller);
      }

      async function observe(origin: ProviderFailureOrigin, outcome: ProviderRequestObservation["outcome"],
        facts: Partial<ProviderRequestObservation> = {}): Promise<void> {
        if (observed) return;
        observed = true;
        const observation: ProviderRequestObservation = {
          request_id: requestId,
          ...(held ? {
            authorization_id: held.id, run_id: held.spec.runId, launch_id: held.spec.launchId,
            connection: held.spec.connection, profile_id: held.spec.profileId,
            profile_revision: held.spec.profileRevision, attempt_id: held.spec.attemptId,
          } : {}),
          ...(credential ? { authorization_revision: credential.authorizationRevision, credential_ref: credential.credentialRef } : {}),
          operation: operation?.id, origin, outcome, provider_sent: sent,
          ...facts, observed_at: new Date(now()).toISOString(), duration_ms: Math.max(0, now() - started),
        };
        try { await dependencies.observe?.(observation); }
        catch { /* A diagnostic failure cannot repeat provider execution. */ }
      }

      async function refuse(status: number, origin: ProviderFailureOrigin, code: string): Promise<Response> {
        release();
        await observe(origin, "rejected", { http_status: status });
        return Response.json({ error: { type: "culler_proxy_error", code, message: "The host could not authorize or complete this request." }, request_id: requestId }, {
          status, headers: { "x-culler-request-id": requestId, "x-request-id": requestId, "request-id": requestId, "x-culler-origin": origin, "cache-control": "no-store" },
        });
      }

      try {
        const bearer = request.headers.get("authorization");
        const key = request.headers.get("x-api-key");
        const token = bearer?.match(/^Bearer (culler_proxy_[A-Za-z0-9_-]{43})$/)?.[1] ?? (!bearer ? key : null);
        if (stopped || !token || !/^culler_proxy_[A-Za-z0-9_-]{43}$/.test(token) || (key && bearer)) {
          return await refuse(401, "proxy_authorization", "invalid_token");
        }
        held = byDigest.get(digest(token));
        if (!held || held.spec.expiresAt <= now()) {
          if (held) revoke(held.id);
          return await refuse(401, "proxy_authorization", "invalid_token");
        }
        const route = routes[held.spec.route]!;
        const url = new URL(request.url);
        if (!["127.0.0.1", "localhost", "host.docker.internal"].includes(url.hostname) ||
            !canonicalPath(url.pathname) || url.hash || url.username || url.password) {
          return await refuse(403, "proxy_authorization", "destination_not_permitted");
        }
        operation = route.operations.find(candidate => candidate.method === request.method && candidate.path === url.pathname);
        if (!operation || !held.spec.operations.includes(operation.id)) {
          return await refuse(403, "proxy_authorization", "operation_not_permitted");
        }
        const queryKeys = [...url.searchParams.keys()];
        if (queryKeys.some(key => !operation!.queryKeys?.includes(key)) || new Set(queryKeys).size !== queryKeys.length) {
          return await refuse(403, "proxy_authorization", "query_not_permitted");
        }
        const limits = held.spec.limits;
        if (held.active.size >= limits.maxConcurrent) return await refuse(429, "proxy_limit", "concurrency_limit");
        held.arrivals = held.arrivals.filter(arrival => arrival > now() - 60_000);
        if (held.arrivals.length >= limits.requestsPerMinute) return await refuse(429, "proxy_limit", "rate_limit");
        if (limits.maxRequests !== undefined && held.requests >= limits.maxRequests) {
          return await refuse(429, "proxy_limit", "request_limit");
        }
        held.active.add(controller);
        held.arrivals.push(now());
        timeout = setTimeout(() => controller.abort("request_timeout"), limits.maxDurationMs);
        timeout.unref();
        const length = request.headers.get("content-length");
        if (request.headers.has("content-encoding") || (length !== null && (!/^\d+$/.test(length) || Number(length) > limits.maxBodyBytes))) {
          return await refuse(413, "proxy_limit", "request_size_limit");
        }
        let body: Uint8Array;
        try { body = await readBounded(request.body, limits.maxBodyBytes, controller.signal); }
        catch (error) {
          if (error instanceof BodyLimit) return await refuse(413, "proxy_limit", "request_size_limit");
          throw error;
        }
        if (request.method === "POST") {
          if (request.headers.get("content-type")?.split(";")[0]?.trim() !== "application/json") {
            return await refuse(415, "proxy_authorization", "json_required");
          }
          let value: unknown;
          try { value = JSON.parse(Buffer.from(body).toString("utf8")); }
          catch { return await refuse(400, "proxy_authorization", "invalid_json"); }
          if (!value || typeof value !== "object" || Array.isArray(value)) return await refuse(400, "proxy_authorization", "invalid_json");
          if (requestModels(value as Record<string, unknown>, operation).some(model => typeof model !== "string" || !held!.spec.models.includes(model))) {
            return await refuse(403, "proxy_authorization", "model_not_permitted");
          }
          // The provider must receive the same object that passed the model check.
          body = Buffer.from(JSON.stringify(value));
        } else if (body.byteLength || operation.modelRequired) {
          return await refuse(400, "proxy_authorization", "invalid_request");
        }
        const checkLiveness = async (): Promise<Response | null> => {
          if (!held!.spec.requireLiveness) return null;
          if (!dependencies.isLaunchCurrent) return refuse(503, "proxy", "launch_control_unavailable");
          let current: boolean;
          try { current = await abortable(dependencies.isLaunchCurrent(structuredClone(held!.spec), controller.signal), controller.signal); }
          catch { return refuse(503, "proxy", "launch_control_unavailable"); }
          if (!current) {
            const response = await refuse(401, "proxy_authorization", "launch_ended");
            revoke(held!.id);
            return response;
          }
          return null;
        };
        const unavailable = await checkLiveness();
        if (unavailable) return unavailable;
        try {
          credential = structuredClone(await abortable(dependencies.credential(held.spec.connection, controller.signal), controller.signal));
          if (!identifier(credential.authorizationRevision) || !identifier(credential.credentialRef)) throw new Error("Invalid credential metadata.");
        } catch (error) {
          if (controller.signal.aborted) throw error;
          const origin = error instanceof ProviderCredentialError ? error.origin : "credential_store";
          return await refuse(503, origin, "credential_unavailable");
        }
        let secrets: string[];
        try { secrets = secretValues(credential, token); }
        catch { return await refuse(503, "credential_store", "invalid_credential"); }
        const headers = new Headers();
        for (const name of [...defaultRequestHeaders, ...(route.requestHeaders ?? [])]) {
          if (forbiddenHeaders.test(name)) continue;
          const value = request.headers.get(name);
          if (value !== null && !secrets.some(secret => value.includes(secret))) headers.set(name, value);
        }
        for (const [name, value] of Object.entries(credential.headers)) {
          if (/^(?:host|cookie|connection|content-length|transfer-encoding|content-encoding|forwarded|x-forwarded-.*|x-culler-.*)$/i.test(name) || value.includes(token)) {
            return await refuse(503, "credential_store", "invalid_credential");
          }
          headers.set(name, value);
        }
        const ended = await checkLiveness();
        if (ended) return ended;
        if (controller.signal.aborted || !byID.has(held.id)) throw controller.signal.reason;
        if (limits.maxRequests !== undefined && held.requests >= limits.maxRequests) {
          return await refuse(429, "proxy_limit", "request_limit");
        }
        held.requests++;
        if (operation.modelRequired) held.modelRequests++;
        sent = true;
        let upstream: Response;
        try {
          upstream = await abortable(requestProvider(`${route.baseURL}${operation.path}${url.search}`, {
            method: request.method, headers, body: request.method === "POST" ? body : undefined,
            redirect: "manual", signal: controller.signal,
          }), controller.signal);
        } catch (error) {
          if (controller.signal.aborted) throw error;
          release();
          await observe("provider_network", "interrupted", { network_error: networkErrorFromFetch(error) });
          return Response.json({ error: { type: "culler_proxy_error", code: "provider_unreachable" }, request_id: requestId }, {
            status: 502, headers: { "x-culler-request-id": requestId, "x-request-id": requestId, "request-id": requestId, "x-culler-origin": "provider_network" },
          });
        }
        if (upstream.status >= 300 && upstream.status < 400) {
          void upstream.body?.cancel().catch(() => {});
          return await refuse(502, "provider", "provider_redirect_rejected");
        }
        const responseHeaders = new Headers({ "cache-control": "no-store", "x-culler-request-id": requestId, "x-culler-origin": "provider" });
        for (const name of [...defaultResponseHeaders, ...(route.responseHeaders ?? [])]) {
          if (forbiddenHeaders.test(name)) continue;
          const value = upstream.headers.get(name);
          if (value !== null) responseHeaders.set(name, redact(value, secrets));
        }
        responseHeaders.set("x-request-id", requestId);
        responseHeaders.set("request-id", requestId);
        const retry = upstream.headers.get("retry-after");
        const retryTime = retry === null ? NaN : /^\d+$/.test(retry) ? now() + Number(retry) * 1000 : Date.parse(retry);
        const facts: Partial<ProviderRequestObservation> = {
          http_status: upstream.status,
          provider_request_id: safeCode(upstream.headers.get("request-id") ?? upstream.headers.get("x-request-id"), secrets),
          ...(Number.isFinite(new Date(retryTime).getTime()) ? { retry_after: new Date(retryTime).toISOString() } : {}),
        };
        if (!upstream.ok) {
          let responseBody: Uint8Array;
          try { responseBody = await readBounded(upstream.body, maxErrorBytes, controller.signal); }
          catch (error) {
            if (!(error instanceof BodyLimit)) throw error;
            responseBody = Buffer.from('{"error":{"type":"provider_error","message":"The provider rejected the request."}}');
            responseHeaders.set("content-type", "application/json");
          }
          let decoded = false;
          try {
            const parsed = JSON.parse(Buffer.from(responseBody).toString());
            decoded = true;
            facts.provider_error_code = providerErrorCode(parsed, secrets);
            responseBody = Buffer.from(JSON.stringify(redactJSON(parsed, secrets)));
          }
          catch {
            if (decoded) responseBody = Buffer.from('{"error":{"type":"provider_error","message":"The provider rejected the request."}}');
          }
          const safeBody = new SecretFilter(secrets).push(responseBody, true);
          release();
          await observe("provider", "response_complete", facts);
          return new Response(safeBody, { status: upstream.status, headers: responseHeaders });
        }
        if (!upstream.body) {
          release();
          await observe("provider", "response_complete", facts);
          return new Response(null, { status: upstream.status, headers: responseHeaders });
        }
        const reader = upstream.body.getReader();
        const filter = new SecretFilter(secrets);
        const contentType = upstream.headers.get("content-type")?.split(";")[0]?.trim().toLowerCase();
        const evidence = contentType === "text/event-stream"
          ? new ProviderEventEvidence(secrets) : undefined;
        const normalize = evidence || contentType === "application/json" || contentType?.endsWith("+json")
          ? new JSONEscapeNormalizer() : undefined;
        const stream = new ReadableStream<Uint8Array>({
          async pull(output) {
            try {
              while (true) {
                const next = await abortable(reader.read(), controller.signal);
                evidence?.push(next.value ?? new Uint8Array(), next.done);
                if (evidence?.failed) facts.provider_error_code = evidence.code;
                const bytes = next.value ?? new Uint8Array();
                const safe = filter.push(normalize?.push(bytes, next.done) ?? bytes, next.done);
                if (safe.byteLength) output.enqueue(safe);
                if (evidence?.failed) void observe("provider", "interrupted", facts);
                if (next.done) {
                  output.close();
                  reader.releaseLock();
                  release();
                  await observe("provider", evidence?.failed ? "interrupted" : "response_complete", facts);
                }
                if (next.done || safe.byteLength) return;
              }
            } catch (error) {
              output.error(new Error("The provider response ended before transport completion."));
              controller.abort(controller.signal.reason ?? "provider_stream_failed");
              void reader.cancel().catch(() => {});
              release();
              const origin = evidence?.failed ? "provider" : controller.signal.reason === "client_disconnected" ? "client"
                : ["authorization_revoked", "request_timeout"].includes(controller.signal.reason) ? "proxy" : "provider_network";
              await observe(origin, "interrupted", { ...facts, ...(origin === "provider_network" ? { network_error: networkErrorFromFetch(error) } : {}) });
            }
          },
          async cancel() {
            controller.abort("client_disconnected");
            void reader.cancel().catch(() => {});
            release();
            await observe(evidence?.failed ? "provider" : "client", "interrupted", facts);
          },
        }, { highWaterMark: 0 });
        return new Response(stream, { status: upstream.status, headers: responseHeaders });
      } catch (error) {
        const reason = controller.signal.reason;
        if (sent && !controller.signal.aborted) {
          release();
          await observe("provider_network", "interrupted", { network_error: networkErrorFromFetch(error) });
          return Response.json({ error: { type: "culler_proxy_error", code: "provider_response_interrupted" }, request_id: requestId }, {
            status: 502, headers: { "x-culler-request-id": requestId, "x-request-id": requestId, "request-id": requestId, "x-culler-origin": "provider_network" },
          });
        }
        return await refuse(reason === "request_timeout" ? 504 : 503,
          reason === "client_disconnected" ? "client" : "proxy", reason === "request_timeout" ? "request_timeout" : "request_interrupted");
      } finally {
        if (!sent) release();
      }
    },
  };
  let server = Bun.serve(serverOptions);
  const retired = new Set<typeof server>();

  return {
    url: `https://host.docker.internal:${server.port}`,
    replaceTLS(tls) {
      if (stopped) throw new Error("The provider proxy is stopped.");
      if (tls.cert === serverOptions.tls.cert && tls.key === serverOptions.tls.key) return;
      // Validate the key pair before the current listener closes.
      const validation = Bun.serve({ hostname: "127.0.0.1", port: 0, tls, fetch: () => new Response(null, { status: 503 }) });
      validation.stop(true);
      const previous = server;
      const port = previous.port!;
      retired.add(previous);
      void previous.stop(false).finally(() => retired.delete(previous));
      try {
        server = Bun.serve({ ...serverOptions, port, tls });
        serverOptions.tls = tls;
      } catch (error) {
        server = Bun.serve({ ...serverOptions, port });
        throw error;
      }
    },
    setRoutes(next) {
      if (stopped) throw new Error("The provider proxy is stopped.");
      const validated = validateRoutes(next);
      for (const held of byID.values()) {
        if (JSON.stringify(routes[held.spec.route]) !== JSON.stringify(validated[held.spec.route])) revoke(held.id);
      }
      routes = validated;
    },
    status(authorizationId) {
      const held = byID.get(authorizationId);
      if (!held || held.spec.expiresAt <= now()) return null;
      return { requests: held.requests, modelRequests: held.modelRequests, active: held.active.size };
    },
    issue(specification) {
      const spec = structuredClone(specification);
      const route = routes[spec.route];
      const required = [spec.runId, spec.launchId, spec.connection, spec.route, ...spec.operations];
      if (stopped || required.some(value => !identifier(value)) || !route || !spec.operations.length ||
          (spec.requireLiveness !== undefined && typeof spec.requireLiveness !== "boolean") ||
          spec.operations.some(operation => !route.operations.some(allowed => allowed.id === operation)) ||
          spec.models.some(model => typeof model !== "string" || !model || model.length > 256) ||
          !Number.isSafeInteger(spec.expiresAt) || spec.expiresAt <= now() ||
          Object.values(spec.limits).some(value => !positiveInteger(value)) ||
          !positiveInteger(spec.limits.maxBodyBytes) || spec.limits.maxBodyBytes > 64 * 1024 * 1024 ||
          !positiveInteger(spec.limits.maxDurationMs) || spec.limits.maxDurationMs > 2_147_483_647 ||
          !positiveInteger(spec.limits.maxConcurrent) || !positiveInteger(spec.limits.requestsPerMinute)) {
        throw new Error("The run authorization is invalid.");
      }
      for (const value of [spec.profileId, spec.profileRevision, spec.attemptId]) {
        if (value !== undefined && !identifier(value)) throw new Error("The run authorization metadata is invalid.");
      }
      const prior = byLaunch.get(spec.launchId);
      if (prior) revoke(prior);
      const token = `culler_proxy_${randomBytes(32).toString("base64url")}`;
      const authorizationId = randomUUID();
      const held: Authorization = { id: authorizationId, digest: digest(token), spec, active: new Set(), arrivals: [], requests: 0, modelRequests: 0 };
      byDigest.set(held.digest, held);
      byID.set(held.id, held);
      byLaunch.set(held.spec.launchId, held.id);
      armExpiry(held);
      return { token, authorizationId };
    },
    extend(authorizationId, expiresAt) {
      const held = byID.get(authorizationId);
      if (!held || held.spec.expiresAt <= now() || !Number.isSafeInteger(expiresAt) || expiresAt <= now()) return false;
      held.spec.expiresAt = expiresAt;
      armExpiry(held);
      return true;
    },
    revoke,
    revokeConnection(connection) {
      for (const held of byID.values()) if (held.spec.connection === connection) revoke(held.id);
    },
    stop() {
      stopped = true;
      for (const id of byID.keys()) revoke(id);
      server.stop(true);
      for (const old of retired) old.stop(true);
      retired.clear();
    },
  };
}
