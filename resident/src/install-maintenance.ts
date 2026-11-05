import { constants } from "node:fs";
import { randomUUID } from "node:crypto";
import { open, rename, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

/** The request survives an interruption. Only its owner removes it. */
export async function readInstallMaintenance(home: string): Promise<string | null> {
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
