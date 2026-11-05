import type { CredentialProjectorHandle } from "./credential-projector";
import type { Exec } from "./types";

/** Control state can extend a live launch; the launch token cannot. */
export function createProviderLifetimeCheck(handle: CredentialProjectorHandle, runMc: Exec): () => Promise<void> {
  let active: Promise<void> | undefined;
  return () => {
    active ??= (async () => {
      const launches = handle.launches();
      for (let offset = 0; offset < launches.length; offset += 100) {
        const group = launches.slice(offset, offset + 100);
        const requested = group.map(row => ({ run_id: row.runId, launch_id: row.launchId }));
        let rows: { run_id: string; launch_id: string; allowed: boolean; expires_at?: string }[] = [];
        try {
          const result = await runMc(["connection", "proxy-lifetimes"], new TextEncoder().encode(JSON.stringify({ launches: requested })));
          const body = JSON.parse(result.stdout);
          if (result.exitCode === 0 && Array.isArray(body.launches)) rows = body.launches;
        } catch {}
        for (const launch of group) {
          const matches = rows.filter(row => row && typeof row === "object" && row.run_id === launch.runId && row.launch_id === launch.launchId);
          const row = matches.length === 1 ? matches[0] : undefined;
          const expires = row?.expires_at ? Date.parse(row.expires_at) : NaN;
          if (row?.allowed === true && Number.isFinite(expires) && expires > Date.now()) {
            handle.projector.extend(launch.runId, launch.launchId, expires);
          } else {
            handle.projector.revoke(launch.runId, launch.launchId);
          }
        }
      }
    })().finally(() => { active = undefined; });
    return active;
  };
}

export async function providerLaunchIsCurrent(runMc: Exec, runId: string, launchId: string, signal: AbortSignal): Promise<boolean> {
  signal.throwIfAborted();
  const result = await runMc(["connection", "proxy-lifetimes"], new TextEncoder().encode(JSON.stringify({
    launches: [{ run_id: runId, launch_id: launchId }],
  })), { signal });
  signal.throwIfAborted();
  if (result.exitCode !== 0) throw new Error("The provider launch control state is unavailable.");
  const rows = JSON.parse(result.stdout).launches;
  if (!Array.isArray(rows) || rows.length !== 1) throw new Error("The provider launch control state is invalid.");
  const row = rows[0];
  return row?.run_id === runId && row?.launch_id === launchId && row.allowed === true && Date.parse(row.expires_at) > Date.now();
}
