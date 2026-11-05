import { parseRuntimeCatalog, resolveRuntimeBindings, type RuntimeBindingAuthorities, type CredentialProjectorHandle, type RuntimeBindingSpec } from "./credential-projector";
import type { Exec } from "./types";

export interface AgentProfileRuntimeDeps {
  runMc: Exec;
  authorities?: RuntimeBindingAuthorities;
}

export async function readAgentProfileRuntimeCatalog(runMc: Exec, authorities?: RuntimeBindingAuthorities): Promise<Record<string, RuntimeBindingSpec>> {
  if (authorities !== undefined) {
    const gate = await runMc(["agent-profile", "test-runtime-authorities"]);
    let enabled = false;
    try { enabled = gate.exitCode === 0 && JSON.parse(gate.stdout)?.enabled === true; }
    catch {}
    if (!enabled) throw new Error("Runtime authority overrides require a test build.");
  }
  const result = await runMc(["agent-profile", "runtime-catalog"]);
  if (result.exitCode !== 0) throw new Error("The agent profile configuration is unavailable. Check the operator configuration.");
  let body: unknown;
  try { body = JSON.parse(result.stdout); }
  catch { throw new Error("The agent profile configuration response is invalid."); }
  if (body === null || typeof body !== "object" || !("bindings" in body)) {
    throw new Error("The agent profile configuration response has no profiles.");
  }
  const catalog = parseRuntimeCatalog(body.bindings);
  if ("connections" in body) {
    const connections = parseRuntimeCatalog(body.connections);
    for (const [id, source] of Object.entries(connections)) {
      if (source.credential !== id) throw new Error("The connection configuration has an invalid identity.");
      catalog[`connection:${id}`] = source;
    }
  }
  return resolveRuntimeBindings(authorities, catalog);
}

export function createAgentProfileRuntimeRefresh(
  handle: CredentialProjectorHandle,
  deps: AgentProfileRuntimeDeps,
): () => Promise<void> {
  let pending: Promise<void> | undefined;
  return () => {
    pending ??= (async () => {
      const catalog = await readAgentProfileRuntimeCatalog(deps.runMc, deps.authorities);
      await handle.reload(catalog);
    })().finally(() => { pending = undefined; });
    return pending;
  };
}
