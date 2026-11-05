import { createConnection } from "node:net";
import { randomBytes, randomUUID } from "node:crypto";
import { lstat, readFile, realpath } from "node:fs/promises";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

export async function residentProjection(home: string, binding: string, spec: any, source: string) {
  const root = await realpath(home);
  async function validateBinding() {
    const catalog = Bun.TOML.parse(await readFile(join(root, "config.toml"), "utf8")) as any;
    const configured = catalog.runtimes?.bindings?.[binding];
    const credential = catalog.runtimes?.credentials?.[spec.credential];
    const harness = configured?.harness === "claude-sdk" ? "claude" : configured?.harness;
    const channel = typeof credential?.kind === "string" ? credential.kind.split("-")[0] : null;
    if (!catalog.runtimes?.enabled_bindings?.includes(binding) || configured?.credential !== spec.credential ||
        harness !== spec.harness || channel !== spec.channel || ![`${channel}-oauth`, `${channel}-api-key`].includes(credential.kind) ||
        credential.kind !== spec.kind || credential.inference_url !== spec.inference_url ||
        (credential.token_url ?? "") !== (spec.token_url ?? "") || (credential.client_id ?? "") !== (spec.client_id ?? "")) {
      throw new Error("The resident binding differs from the evaluation connection profile.");
    }
    return { harness, channel };
  }
  const { harness, channel } = await validateBinding();
  const file = join(root, "health", "provider-acceptance.sock");
  const access = await import(pathToFileURL(join(source, "runner/agent-runner/adapters/provider-access.ts")).href);
  async function request(fields: Record<string, unknown>) {
    const socket = await lstat(file);
    if (!socket.isSocket() || socket.isSymbolicLink() || (socket.mode & 0o777) !== 0o600 || socket.nlink !== 1 ||
        (process.getuid && socket.uid !== process.getuid())) throw new Error("The resident socket requires owner-only access.");
    const nonce = randomBytes(16).toString("hex");
    const body = await new Promise<string>((accept, reject) => {
      const connection = createConnection(file);
      let result = "";
      connection.setTimeout(fields.op === "profile_authorize" ? 45_000 : 5_000, () => connection.destroy(new Error("The resident request exceeded its time limit.")));
      connection.on("connect", () => connection.write(JSON.stringify({ version: 1, ...fields, nonce }) + "\n"));
      connection.on("data", chunk => {
        result += chunk.toString("utf8");
        if (Buffer.byteLength(result) > 64 << 10) connection.destroy(new Error("The resident response exceeds its limit."));
        else if (result.includes("\n")) { connection.end(); accept(result); }
      });
      connection.on("error", () => reject(new Error("The resident request failed.")));
      connection.on("end", () => { if (!result.endsWith("\n")) reject(new Error("The resident response is incomplete.")); });
    });
    const value = JSON.parse(body);
    const extra = fields.op === "profile_authorize" ? ["credential", "proxy_config"] : fields.op === "profile_status" ? ["provider_requests"] : ["revoked"];
    if (!value || Array.isArray(value) || Object.keys(value).some(key => !["version", "status", "nonce", ...extra].includes(key)) ||
        value.version !== 1 || value.status !== "ok" || value.nonce !== nonce) throw new Error("The resident response is invalid.");
    return value;
  }
  const grants = new Map<string, { run_id: string; launch_id: string }>();
  async function release(id: string) {
    const grant = grants.get(id);
    if (!grant) throw new Error("The evaluation grant is unavailable.");
    let count: number | undefined;
    try {
      const value = await request({ op: "profile_status", ...grant });
      if (!Number.isSafeInteger(value.provider_requests) || value.provider_requests < 0 || value.provider_requests > 100) {
        throw new Error("The provider request count is invalid.");
      }
      count = value.provider_requests;
    } finally {
      const value = await request({ op: "profile_revoke", ...grant });
      if (value.revoked !== true) throw new Error("The evaluation grant revocation failed.");
      grants.delete(id);
    }
    return count;
  }
  return {
    async stop() {
      const results = await Promise.allSettled([...grants.keys()].map(release));
      if (results.some(value => value.status === "rejected")) throw new Error("The evaluation grants could not close.");
    },
    release,
    async current(model: string, duration: number) {
      await validateBinding();
      if (typeof model !== "string" || !model || model.length > 256 || !Number.isInteger(duration) || duration < 1 || duration > 1800) {
        throw new Error("The evaluation requires an exact model and a duration of at most 1800 seconds.");
      }
      const id = randomUUID();
      const grant = { run_id: `eval-${id}`, launch_id: randomUUID() };
      grants.set(id, grant);
      try {
        const value = await request({ op: "profile_authorize", credential: spec.credential, profile: binding, ...grant,
          model, expires_at_ms: Date.now() + duration * 1000, max_requests: 100 });
        if (value.credential !== spec.credential) throw new Error("The resident connection differs.");
        const config = access.parseProviderAccess(JSON.stringify(value.proxy_config), harness === "claude" ? "claude-sdk" : "codex");
        if (config.authMode !== (spec.kind.endsWith("-api-key") ? "api-key" : "account")) throw new Error("The resident authentication method differs.");
        return { harness, channel, base_url: "", provider_access: config, grant_id: id };
      } catch (error) {
        try { await release(id); } catch {}
        throw error;
      }
    },
  };
}
