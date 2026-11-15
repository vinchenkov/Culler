import { chmod, lstat, mkdir, rm } from "node:fs/promises";
import type { Logger } from "../../runner/shared/log";
import { join } from "node:path";
import { parseProviderAccess } from "../../runner/agent-runner/adapters/provider-access";
const MAX_REQUEST_BYTES = 8192;

export interface ProviderAcceptanceRequest {
  version: 1;
  op: "profile_authorize";
  credential: string;
  profile: string;
  profile_revision?: string;
  run_id: string;
  launch_id: string;
  model: string;
  nonce: string;
  expires_at_ms: number;
  max_requests: number;
  purpose?: "model_list";
}

export interface ProviderAcceptanceOps {
  authorize(request: ProviderAcceptanceRequest): Promise<Record<string, unknown>>;
  revoke(run: string, launch: string): void;
  revokeConnection(connection: string): void;
  status(run: string, launch: string): { providerRequests: number } | null;
  check(connection: string): Promise<void>;
}

export interface ProviderAcceptanceHandle {
  path: string;
  stop(): Promise<void>;
}

export async function handleProviderAcceptanceRequest(body: string, _releaseBuildId: string,
  ops: ProviderAcceptanceOps): Promise<string> {
  if (Buffer.byteLength(body) > MAX_REQUEST_BYTES) throw new Error("The provider access request is too large.");
  const request = JSON.parse(body);
  const identifier = (value: unknown) => typeof value === "string" && /^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$/.test(value);
  if (!request || typeof request !== "object" || Array.isArray(request) || request.version !== 1 ||
      typeof request.nonce !== "string" || !/^[0-9a-f]{32}$/.test(request.nonce)) throw new Error("The provider access identity is invalid.");
  if (request.op === "connection_revoke") {
    const allowed = new Set(["version", "op", "credential", "nonce"]);
    if (!identifier(request.credential) || Object.keys(request).some(key => !allowed.has(key))) throw new Error("The connection revocation is invalid.");
    ops.revokeConnection(request.credential);
    return JSON.stringify({ version: 1, status: "ok", nonce: request.nonce, revoked: true }) + "\n";
  }
  if (request.op === "connection_check") {
    const allowed = new Set(["version", "op", "credential", "nonce"]);
    if (!identifier(request.credential) || Object.keys(request).some(key => !allowed.has(key))) throw new Error("The connection check is invalid.");
    await ops.check(request.credential);
    return JSON.stringify({ version: 1, status: "ok", nonce: request.nonce }) + "\n";
  }
  if (!identifier(request.run_id) || !identifier(request.launch_id)) throw new Error("The provider access identity is invalid.");
  if (request.op === "profile_status") {
    const allowed = new Set(["version", "op", "run_id", "launch_id", "nonce"]);
    if (Object.keys(request).some(key => !allowed.has(key))) throw new Error("The provider status request is invalid.");
    const status = ops.status(request.run_id, request.launch_id);
    if (!status) throw new Error("The provider request evidence is unavailable.");
    return JSON.stringify({ version: 1, status: "ok", nonce: request.nonce, provider_requests: status.providerRequests }) + "\n";
  }
  if (request.op === "profile_revoke") {
    const allowed = new Set(["version", "op", "run_id", "launch_id", "nonce"]);
    if (Object.keys(request).some(key => !allowed.has(key))) throw new Error("The provider access request is invalid.");
    ops.revoke(request.run_id, request.launch_id);
    return JSON.stringify({ version: 1, status: "ok", nonce: request.nonce, revoked: true }) + "\n";
  }
  const allowed = new Set(["version", "op", "credential", "profile", "profile_revision", "run_id", "launch_id", "model", "nonce", "expires_at_ms", "max_requests", "purpose"]);
  if (request.op !== "profile_authorize" || Object.keys(request).some(key => !allowed.has(key)) ||
      !identifier(request.credential) || !identifier(request.profile) ||
      (request.profile_revision !== undefined && !identifier(request.profile_revision)) ||
      (request.purpose !== undefined && request.purpose !== "model_list") ||
      typeof request.model !== "string" || request.model.length > 256 || (request.purpose !== "model_list" && !request.model) ||
      !Number.isSafeInteger(request.expires_at_ms) || request.expires_at_ms <= Date.now() || request.expires_at_ms > Date.now() + 30 * 60_000 ||
      !Number.isSafeInteger(request.max_requests) || request.max_requests < 1 || request.max_requests > 100) {
    throw new Error("The provider access limits are invalid.");
  }
  const config = await ops.authorize(request);
  try {
    if (config.harness !== "codex" && config.harness !== "claude-sdk" && config.harness !== "opencode") throw new Error("Invalid harness.");
    parseProviderAccess(JSON.stringify(config), config.harness);
  } catch {
    ops.revoke(request.run_id, request.launch_id);
    throw new Error("The provider access configuration is invalid.");
  }
  return JSON.stringify({ version: 1, status: "ok", credential: request.credential, nonce: request.nonce, proxy_config: config }) + "\n";
}

export async function startProviderAcceptanceServer(
  mcHome: string,
  releaseBuildId: string,
  ops: ProviderAcceptanceOps,
  log: Logger,
): Promise<ProviderAcceptanceHandle> {
  const health = join(mcHome, "health");
  await mkdir(health, { recursive: true, mode: 0o700 });
  const path = join(health, "provider-acceptance.sock");
  try {
    const stale = await lstat(path);
    if (!stale.isSocket() || stale.uid !== process.getuid?.()) {
      throw new Error("acceptance socket path is occupied by an unsafe inode");
    }
    await rm(path);
  } catch (err) {
    if (!(err !== null && typeof err === "object" && "code" in err && err.code === "ENOENT")) {
      throw err;
    }
  }
  const states = new WeakMap<object, { body: string; handled: boolean }>();
  const server = Bun.listen({
    unix: path,
    socket: {
      open(socket) {
        states.set(socket, { body: "", handled: false });
      },
      data(socket, data) {
        const state = states.get(socket);
        if (state === undefined || state.handled) {
          socket.end();
          return;
        }
        state.body += Buffer.from(data).toString("utf8");
        if (Buffer.byteLength(state.body, "utf8") > MAX_REQUEST_BYTES + 1) {
          state.handled = true;
          socket.end('{"version":1,"status":"error","error":"request exceeds its bound"}\n');
          return;
        }
        const newline = state.body.indexOf("\n");
        if (newline < 0) return;
        state.handled = true;
        const body = state.body.slice(0, newline);
        if (state.body.slice(newline + 1) !== "") {
          socket.end('{"version":1,"status":"error","error":"request carries trailing bytes"}\n');
          return;
        }
        void handleProviderAcceptanceRequest(body, releaseBuildId, ops).then(
          (response) => socket.end(response),
          (err) => {
            log.warn("credential.acceptance_refused", `The provider access request was refused.`);
            socket.end('{"version":1,"status":"error","error":"provider access refused"}\n');
          },
        );
      },
      error(_socket, err) {
        log.warn("credential.acceptance_refused", `provider acceptance socket error: ${String(err)}`);
      },
    },
  });
  try {
    await chmod(path, 0o600);
  } catch (err) {
    server.stop(true);
    await rm(path, { force: true });
    throw err;
  }
  return {
    path,
    async stop() {
      server.stop(true);
      await rm(path, { force: true });
    },
  };
}
