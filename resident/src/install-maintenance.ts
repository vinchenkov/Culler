import { constants } from "node:fs";
import { createHash, randomUUID } from "node:crypto";
import { open, rename, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

/** The request survives an interruption. Only its owner removes it. */
export async function readInstallMaintenance(home: string): Promise<string | null> {
  const update = await readUpdateState(home);
  if (update && !["committed", "complete", "restored"].includes(update.phase)) return update.id;
  const requestPath = join(home, "state", "install-maintenance.json");
  let file;
  try {
    file = await open(requestPath, constants.O_RDONLY | constants.O_NOFOLLOW);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    return null;
  }
  let id: string;
  try {
    const stat = await file.stat();
    if (!stat.isFile() || stat.nlink !== 1 || stat.uid !== process.getuid!() ||
        (stat.mode & 0o777) !== 0o600 || stat.size > 4096) {
      throw new Error("The installer pause request requires an owner-only regular file.");
    }
    const request = JSON.parse(await file.readFile("utf8"));
    if (typeof request.id !== "string" || !/^[a-zA-Z0-9-]{16,128}$/.test(request.id)) {
      throw new Error("The installer pause request has an invalid identity.");
    }
    id = request.id;
  } finally {
    await file.close();
  }
  return id;
}

export function createInstallMaintenance(home: string, release: string) {
  const acknowledgement = join(home, "state", "install-maintenance-ack.json");
  return async (activeOperations: number): Promise<boolean> => {
    const id = await readInstallMaintenance(home);
    if (id === null) {
      await rm(acknowledgement, { force: true });
      return false;
    }
    const stage = acknowledgement + `.${process.pid}.${randomUUID()}.next`;
    try {
      await writeFile(stage, JSON.stringify({
        id, release, pid: process.pid, ready: activeOperations === 0,
        observed_at: new Date().toISOString(),
      }) + "\n", { mode: 0o600, flag: "wx" });
      await rename(stage, acknowledgement);
    }
    finally { await rm(stage, { force: true }); }
    return true;
  };
}


interface UpdateState {
  version: 1;
  id: string;
  phase: string;
  deployment: string;
  candidate: { commit: string };
  previous?: { commit: string };
}

export async function readUpdateState(home: string): Promise<UpdateState | null> {
  let file;
  try { file = await open(join(home, "updates", "transaction.json"), constants.O_RDONLY | constants.O_NOFOLLOW); }
  catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
    throw error;
  }
  try {
    const stat = await file.stat();
    if (!stat.isFile() || stat.nlink !== 1 || stat.uid !== process.getuid!() ||
        (stat.mode & 0o777) !== 0o600 || stat.size > 4 * 1024 * 1024) {
      throw new Error("The update record requires an owner-only regular file.");
    }
    const state = JSON.parse(await file.readFile("utf8"));
    const deployment = createHash("sha256").update("mc-deployment-v1\0" + home).digest("hex").slice(0, 12);
    const phases = ["stop", "prepare", "checkpoint", "replace", "verify", "committed", "complete", "restore", "restored"];
    if (state.version !== 1 || !/^[a-f0-9]{16,64}$/.test(state.id ?? "") || state.deployment !== deployment ||
        !phases.includes(state.phase) || !/^[a-f0-9]{40}$/.test(state.candidate?.commit ?? "") ||
        (state.previous !== undefined && !/^[a-f0-9]{40}$/.test(state.previous?.commit ?? ""))) {
      throw new Error("The update record has an invalid identity or phase. Keep Culler stopped.");
    }
    return state;
  } finally { await file.close(); }
}

export async function waitForUpdateActivation(
  home: string,
  release: string,
  publish: () => Promise<void>,
  wait: () => Promise<void> = () => Bun.sleep(250),
  preflight: () => Promise<void> = async () => {},
): Promise<void> {
  let checked = false;
  for (;;) {
    const state = await readUpdateState(home);
    if (!state) return;
    const terminal = ["committed", "complete", "restored"].includes(state.phase);
    if (terminal && await readInstallMaintenance(home) !== state.id) return;
    const permitted = ["verify", "committed", "complete"].includes(state.phase) ? state.candidate.commit
      : ["restore", "restored"].includes(state.phase) ? state.previous?.commit : undefined;
    if (!permitted || permitted !== release) {
      throw new Error("The update keeps this release stopped. Use culler update to recover.");
    }
    if (!checked) { await preflight(); checked = true; }
    await publish();
    await wait();
  }
}
