import protocols from "./provider-protocols.json";
import { randomBytes } from "node:crypto";
import type { Exec } from "./types";
import { ProviderCredentialError, type ProviderCredential } from "./provider-proxy";

export interface HostCredentialRecord {
  version: 1;
  type: "api_key" | "account";
  credential: string;
  channel: "codex" | "claude" | "opencode";
  api_key?: string;
  token_url?: string;
  client_id?: string;
  access_token?: string;
  id_token?: string;
  refresh_token?: string;
  account_id?: string;
  scope?: string;
  expires_at_ms?: number;
  refresh_expires_at_ms?: number;
}

export interface HostCredentialSnapshot {
  record: HostCredentialRecord;
  authorization_id: string;
  credential_ref: string;
}

export interface ProviderCredentialDependencies {
  runMc: Exec;
  acquireOwner?(connection: string): Promise<() => Promise<void>>;
  fetch?: typeof fetch;
  now?: () => number;
  sleep?(milliseconds: number, signal: AbortSignal): Promise<void>;
  observe?(value: Record<string, unknown>): Promise<void>;
}

function cancellable<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) { void promise.catch(() => {}); return Promise.reject(signal.reason); }
  return new Promise((resolve, reject) => {
    const abort = () => reject(signal.reason);
    signal.addEventListener("abort", abort, { once: true });
    promise.then(value => { signal.removeEventListener("abort", abort); resolve(value); }, error => { signal.removeEventListener("abort", abort); reject(error); });
  });
}

/** Only the host resolves this interface. No transport returns its values to a container. */
export function createProviderCredentials(deps: ProviderCredentialDependencies) {
  const now = deps.now ?? Date.now;
  const request = deps.fetch ?? fetch;
  const active = new Map<string, { promise: Promise<HostCredentialSnapshot>; waiters: Set<AbortSignal>; demand: AbortController }>();
  const pause = deps.sleep ?? ((milliseconds: number, signal: AbortSignal) => new Promise<void>((resolve, reject) => {
    const finish = () => { clearTimeout(timer); signal.removeEventListener("abort", cancel); resolve(); };
    const cancel = () => { clearTimeout(timer); signal.removeEventListener("abort", cancel); reject(signal.reason); };
    const timer = setTimeout(finish, milliseconds);
    signal.addEventListener("abort", cancel, { once: true });
    if (signal.aborted) cancel();
  }));
  let draining = false;
  const rejected = new Set<string>();
  const pending = new Map<string, { snapshot: HostCredentialSnapshot; record: HostCredentialRecord }>();

  async function observe(value: Record<string, unknown>): Promise<void> {
    try { await deps.observe?.(value); }
    catch { throw new ProviderCredentialError("proxy", "diagnostic_unavailable"); }
  }

  async function storeFailure(connection: string): Promise<void> {
    try { await observe({ request_id: crypto.randomUUID(), connection, operation: "service", origin: "credential_store",
      outcome: "failed", provider_sent: false, observed_at: new Date(now()).toISOString(), duration_ms: 0 }); } catch {}
  }

  async function read(connection: string): Promise<HostCredentialSnapshot> {
    try {
      const result = await deps.runMc(["agent-profile", "credential-read"], new TextEncoder().encode(JSON.stringify({ connection_id: connection })));
      if (result.exitCode !== 0) throw new Error("read");
      const snapshot = JSON.parse(result.stdout) as HostCredentialSnapshot;
      if (!snapshot.record || snapshot.record.version !== 1 || snapshot.record.credential !== connection ||
          !["api_key", "account"].includes(snapshot.record.type) || !["codex", "claude", "opencode"].includes(snapshot.record.channel) ||
          !snapshot.authorization_id || !snapshot.credential_ref ||
          snapshot.record.type === "api_key" && !snapshot.record.api_key) throw new Error("shape");
      await observe({ request_id: crypto.randomUUID(), connection, operation: "service", origin: "credential_store",
        outcome: "ready", provider_sent: false, authorization_revision: snapshot.authorization_id, credential_ref: snapshot.credential_ref,
        observed_at: new Date(now()).toISOString(), duration_ms: 0 });
      return snapshot;
    } catch (error) {
      if (error instanceof ProviderCredentialError && error.origin === "proxy") throw error;
      await storeFailure(connection);
      throw new ProviderCredentialError("credential_store", "invalid_credential");
    }
  }

  async function save(snapshot: HostCredentialSnapshot, next: HostCredentialRecord): Promise<HostCredentialSnapshot> {
    const connection = snapshot.record.credential;
    pending.set(connection, { snapshot, record: next });
    const saved = await deps.runMc(["agent-profile", "credential-renew"], new TextEncoder().encode(JSON.stringify({
      connection_id: connection, authorization_id: snapshot.authorization_id,
      credential_ref: snapshot.credential_ref, record: next,
    })));
    if (saved.exitCode !== 0) {
      await storeFailure(connection);
      throw new ProviderCredentialError("credential_store", "credential_save");
    }
    const current = await read(connection);
    if (current.authorization_id !== snapshot.authorization_id || current.record.access_token !== next.access_token || current.record.refresh_token !== next.refresh_token) {
      throw new ProviderCredentialError("credential_store", "credential_changed");
    }
    pending.delete(connection);
    rejected.delete(snapshot.credential_ref);
    return current;
  }

  async function renew(connection: string, caller?: AbortSignal): Promise<HostCredentialSnapshot> {
    if (draining) throw new ProviderCredentialError("credential_store", "host_stopping");
    const signal = caller ?? new AbortController().signal;
    signal.throwIfAborted();
    let held = active.get(connection);
    if (!held) {
      held = { promise: undefined as unknown as Promise<HostCredentialSnapshot>, waiters: new Set([signal]), demand: new AbortController() };
      active.set(connection, held);
      const state = held;
      state.promise = performRenewal(connection, state.demand.signal).finally(() => {
        if (active.get(connection) === state) active.delete(connection);
      });
      void state.promise.catch(() => {});
    } else held.waiters.add(signal);
    try { return await cancellable(held.promise, signal); }
    finally {
      held.waiters.delete(signal);
      if (![...held.waiters].some(value => !value.aborted)) held.demand.abort("renewal_no_callers");
    }
  }

  async function performRenewal(connection: string, demand: AbortSignal): Promise<HostCredentialSnapshot> {
    let release: (() => Promise<void>) | undefined;
    try {
      release = await deps.acquireOwner?.(connection);
      demand.throwIfAborted();
      let snapshot = await read(connection);
      demand.throwIfAborted();
      const interrupted = pending.get(connection);
      if (interrupted) {
        if (snapshot.authorization_id === interrupted.snapshot.authorization_id && snapshot.credential_ref === interrupted.snapshot.credential_ref) {
          snapshot = await save(snapshot, interrupted.record);
          await renewalReady(snapshot);
        } else pending.delete(connection);
      }
      const record = snapshot.record;
      if (record.type === "api_key") return snapshot;
      if (record.access_token && Number.isFinite(record.expires_at_ms) && record.expires_at_ms! > now() + 120_000 && !rejected.has(snapshot.credential_ref)) return snapshot;
      if (!record.refresh_token || !record.token_url || !record.client_id) throw new ProviderCredentialError("credential_store", "invalid_credential");
      if (record.refresh_expires_at_ms && record.refresh_expires_at_ms <= now()) {
        await observe({ connection, authorization_revision: snapshot.authorization_id, credential_ref: snapshot.credential_ref,
          request_id: randomBytes(16).toString("hex"), duration_ms: 0, operation: "token_refresh", observed_at: new Date(now()).toISOString(),
          origin: "provider_renewal", outcome: "failed", provider_sent: false, provider_error_code: "invalid_grant" });
        throw new ProviderCredentialError("provider_renewal", "invalid_grant");
      }
      const target = new URL(record.token_url);
      if (target.protocol !== "https:" || target.username || target.password || target.hash || target.search) throw new ProviderCredentialError("credential_store", "invalid_authority");
      const deadline = now() + 30_000;
      for (let attempt = 0; attempt < 3; attempt++) {
        demand.throwIfAborted();
        const remaining = deadline - now();
        if (remaining <= 0) throw new ProviderCredentialError("provider_renewal", "renewal_unavailable");
        const started = now();
        const timeout = AbortSignal.timeout(Math.max(1, Math.ceil(remaining)));
        const identity = { connection, authorization_revision: snapshot.authorization_id, credential_ref: snapshot.credential_ref,
          request_id: randomBytes(16).toString("hex"), operation: "token_refresh" };
        const observeFailure = async (fields: Record<string, unknown>) => observe({ ...identity, origin: "provider_renewal", outcome: "failed", provider_sent: true,
          observed_at: new Date(now()).toISOString(), duration_ms: Math.max(0, now() - started), ...fields });
        let response: Response;
        try {
          response = await cancellable(request(target.href, { method: "POST", headers: { "content-type": "application/json" },
            body: JSON.stringify({ grant_type: "refresh_token", client_id: record.client_id, refresh_token: record.refresh_token,
              ...(record.scope ? { scope: record.scope } : {}) }), signal: timeout, redirect: "error" }), timeout);
        } catch {
          await observeFailure({ network_error: "other" });
          // The provider can rotate a token before a transport failure. A repeated request cannot prove safety.
          throw new ProviderCredentialError("provider_renewal", "renewal_unavailable");
        }
        const reader = response.body?.getReader();
        let data = "";
        let bytes = 0;
        const decoder = new TextDecoder();
        try {
          if (reader) for (;;) {
            const chunk = await cancellable(reader.read(), timeout);
            if (chunk.done) break;
            bytes += chunk.value.byteLength;
            if (bytes > 65_536) throw new Error("size");
            data += decoder.decode(chunk.value, { stream: true });
          }
          data += decoder.decode();
        } catch {
          void reader?.cancel().catch(() => {});
          await observeFailure({ provider_error_code: "invalid_response" });
          throw new ProviderCredentialError("provider_renewal", "invalid_response");
        }
        let body: Record<string, unknown>;
        try { body = JSON.parse(data); if (!body || typeof body !== "object" || Array.isArray(body)) throw new Error("shape"); }
        catch {
          if (response.ok) {
            await observeFailure({ http_status: response.status, provider_error_code: "invalid_response" });
            throw new ProviderCredentialError("provider_renewal", "invalid_response");
          }
          body = {};
        }
        if (!response.ok) {
          const nested = body.error && typeof body.error === "object" ? body.error as Record<string, unknown> : undefined;
          const raw = typeof body.error === "string" ? body.error : nested?.code ?? nested?.type;
          const code = ["invalid_grant", "invalid_client", "refresh_token_reused", "temporarily_unavailable", "server_error", "rate_limit_exceeded", "rate_limit_error", "overloaded_error"].includes(String(raw)) ? String(raw) : "renewal_rejected";
          const header = response.headers.get("retry-after");
          const retryAt = header && /^\d+$/.test(header) ? now() + Number(header) * 1000 : header ? Date.parse(header) : NaN;
          await observeFailure({ http_status: response.status, provider_error_code: code,
            ...(Number.isFinite(new Date(retryAt).getTime()) ? { retry_after: new Date(retryAt).toISOString() } : {}) });
          const temporary = (response.status === 429 || response.status >= 500) && !["invalid_grant", "invalid_client", "refresh_token_reused"].includes(code);
          const delay = Math.max(250 * 2 ** attempt, Number.isFinite(retryAt) ? retryAt - now() : 0);
          if (!temporary || attempt === 2 || now() + delay >= deadline) throw new ProviderCredentialError("provider_renewal", code);
          await pause(delay, demand);
          continue;
        }
        let expires = typeof body.expires_in === "number" ? now() + body.expires_in * 1000 : NaN;
        if (body.expires_in === undefined && typeof body.access_token === "string") {
          try {
            const parts = body.access_token.split(".");
            if (parts.length === 3) {
              const claims = JSON.parse(Buffer.from(parts[1]!, "base64url").toString("utf8"));
              if (typeof claims.exp === "number") expires = claims.exp * 1000;
            }
          } catch { /* An opaque token requires explicit expiry metadata. */ }
        }
        const validToken = typeof body.access_token === "string" && body.access_token.length > 0;
        const validExpiry = Number.isFinite(expires) && expires > now() && Number.isFinite(new Date(expires).getTime());
        const next: HostCredentialRecord = { ...record, ...(validToken ? { access_token: body.access_token as string } : {}), expires_at_ms: validExpiry ? expires : 0 };
        const rotated = typeof body.refresh_token === "string" && body.refresh_token.length > 0 && body.refresh_token !== record.refresh_token;
        if (typeof body.refresh_token === "string" && body.refresh_token) next.refresh_token = body.refresh_token;
        if (typeof body.id_token === "string" && body.id_token) next.id_token = body.id_token;
        if (typeof body.scope === "string") next.scope = body.scope;
        let validRefreshExpiry = true;
        if (body.refresh_token_expires_in !== undefined) {
          const expiry = typeof body.refresh_token_expires_in === "number" ? now() + body.refresh_token_expires_in * 1000 : NaN;
          validRefreshExpiry = Number.isFinite(expiry) && expiry > now() && Number.isFinite(new Date(expiry).getTime());
          next.refresh_expires_at_ms = validRefreshExpiry ? expiry : 0;
        }
        if (!validToken || !validExpiry || !validRefreshExpiry) {
          if (rotated) { const saved = await save(snapshot, next); identity.credential_ref = saved.credential_ref; }
          await observeFailure({ provider_error_code: "invalid_response" });
          throw new ProviderCredentialError("provider_renewal", "invalid_response");
        }
        // Finish a received rotation even when the requesting run ends before the durable save.
        const current = await save(snapshot, next);
        await renewalReady(current, identity.request_id, Math.max(0, now() - started));
        return current;
      }
      throw new ProviderCredentialError("provider_renewal", "renewal_unavailable");
    } catch (error) {
      if (!demand.aborted && (!(error instanceof ProviderCredentialError) || error.origin === "credential_store")) {
        await storeFailure(connection);
        throw error instanceof ProviderCredentialError ? error : new ProviderCredentialError("credential_store", "credential_store");
      }
      throw error;
    } finally { await release?.(); }
  }

  async function flushPending(signal: AbortSignal): Promise<void> {
    for (let attempt = 0; pending.size && attempt < 3; attempt++) {
      for (const [connection, interrupted] of pending) {
        signal.throwIfAborted();
        let release: (() => Promise<void>) | undefined;
        try {
          release = await deps.acquireOwner?.(connection);
          signal.throwIfAborted();
          const current = await read(connection);
          if (current.authorization_id === interrupted.snapshot.authorization_id && current.credential_ref === interrupted.snapshot.credential_ref) {
            const saved = await save(current, interrupted.record);
            await renewalReady(saved);
          } else pending.delete(connection);
        } catch { await storeFailure(connection); }
        finally { await release?.(); }
      }
      if (pending.size && attempt < 2) await pause(250 * 2 ** attempt, signal);
    }
    if (pending.size) throw new ProviderCredentialError("credential_store", "credential_save");
  }

  async function renewalReady(snapshot: HostCredentialSnapshot, requestId = randomBytes(16).toString("hex"), duration = 0) {
    await observe({ connection: snapshot.record.credential, authorization_revision: snapshot.authorization_id,
      credential_ref: snapshot.credential_ref, request_id: requestId, duration_ms: duration, operation: "token_refresh",
      observed_at: new Date(now()).toISOString(), origin: "provider_renewal", outcome: "ready", provider_sent: true,
      ...(snapshot.record.expires_at_ms ? { expires_at: new Date(snapshot.record.expires_at_ms).toISOString() } : {}) });
  }

  return {
    read,
    renew,
    async drain(maximumMilliseconds = 30_000): Promise<void> {
      if (!Number.isFinite(maximumMilliseconds) || maximumMilliseconds < 0 || maximumMilliseconds > 30_000) throw new Error("The credential drain deadline is invalid.");
      draining = true;
      for (const state of active.values()) state.demand.abort("host_stopping");
      const deadline = AbortSignal.timeout(Math.ceil(maximumMilliseconds));
      try {
        await cancellable((async () => {
          await Promise.allSettled([...active.values()].map(state => state.promise));
          await flushPending(deadline);
        })(), deadline);
      } catch { throw new ProviderCredentialError("credential_store", "credential_save"); }
    },
    reject(credentialRef: string) { rejected.add(credentialRef); },
    async credential(connection: string, signal: AbortSignal): Promise<ProviderCredential> {
      signal.throwIfAborted();
      const snapshot = await renew(connection, signal);
      signal.throwIfAborted();
      const record = snapshot.record;
      const token = record.type === "api_key" ? record.api_key : record.access_token;
      if (!token) { await storeFailure(connection); throw new ProviderCredentialError("credential_store", "credential_missing"); }
      const apiKeyHeader = record.channel === "opencode" ? undefined : protocols[record.channel].apiKeyHeader;
      const headers: Record<string, string> = record.type === "api_key" && apiKeyHeader
        ? { [apiKeyHeader]: token } : { authorization: `Bearer ${token}` };
      const identityHeader = record.channel === "opencode" ? undefined : protocols[record.channel].identityHeader;
      if (record.type === "account" && record.account_id && identityHeader) {
        headers[identityHeader] = record.account_id;
      }
      return { headers, authorizationRevision: snapshot.authorization_id, credentialRef: snapshot.credential_ref,
        secrets: [record.api_key, record.access_token, record.id_token, record.refresh_token].filter((v): v is string => !!v) };
    },
  };
}
