import { randomUUID } from "node:crypto";
import { constants } from "node:fs";
import { copyFile, link, lstat, readdir, unlink, mkdir, rm } from "node:fs/promises";
import { join } from "node:path";

async function directoryEntry(path: string) {
  return lstat(path).catch(error => { if (error.code === "ENOENT") return undefined; throw error; });
}

// Previous deployments retained npm archives in /cache without a persistent home.
// Copy absent archives atomically so concurrent runs preserve the same home cache.
export async function importLegacyNpmCache(root: string, home: string): Promise<void> {
  const source = join(root, "npm", "_cacache", "content-v2", "sha512");
  if (!(await directoryEntry(source))?.isDirectory()) return;
  const destination = join(home, ".npm", "_cacache", "content-v2", "sha512");
  const copy = async (from: string, to: string) => {
    await mkdir(to, { recursive: true });
    for (const entry of await readdir(from, { withFileTypes: true })) {
      const sourcePath = join(from, entry.name), destinationPath = join(to, entry.name);
      if (entry.isDirectory()) {
        await copy(sourcePath, destinationPath);
      } else if (entry.isFile() && !await directoryEntry(destinationPath)) {
        const temporary = join(to, `.culler-import-${randomUUID()}`);
        try {
          await copyFile(sourcePath, temporary, constants.COPYFILE_EXCL);
          await link(temporary, destinationPath).catch(error => { if (error.code !== "EEXIST") throw error; });
        } finally { await rm(temporary, { force: true }); }
      }
    }
  };
  await copy(source, destination);
}

export async function projectCacheUsage(root = "/cache"): Promise<{ bytes: number; files: Array<{ path: string; bytes: number; modified: number }> }> {
  const entry = await directoryEntry(root);
  if (entry && !entry.isDirectory()) throw new Error("The Project cache root must be a directory.");
  const files: Array<{ path: string; bytes: number; modified: number }> = [];
  const walk = async (directory: string) => {
    for (const name of await readdir(directory).catch((error) => { if (error.code === "ENOENT") return []; throw error; })) {
      const path = join(directory, name);
      const entry = await directoryEntry(path);
      if (entry?.isDirectory()) await walk(path);
      else if (entry?.isFile()) files.push({ path, bytes: entry.size, modified: entry.mtimeMs });
    }
  };
  await walk(root);
  return { bytes: files.reduce((total, file) => total + file.bytes, 0), files };
}

export async function projectHomeUsage(root = process.env.HOME ?? "/home/agent"): Promise<{ bytes: number }> {
  return { bytes: (await projectCacheUsage(root)).bytes };
}

export async function clearProjectCache(root = "/cache"): Promise<number> {
  const entry = await directoryEntry(root);
  if (!entry) return 0;
  if (!entry.isDirectory()) throw new Error("The Project cache root must be a directory.");
  for (const name of await readdir(root)) await rm(join(root, name), { recursive: true, force: true });
  return (await projectCacheUsage(root)).bytes;
}

export async function limitProjectCache(root = "/cache", limit = 2 * 1024 ** 3): Promise<number> {
  if (!Number.isSafeInteger(limit) || limit < 0) throw new Error("The Project cache limit is invalid.");
  const usage = await projectCacheUsage(root);
  let bytes = usage.bytes;
  for (const file of usage.files.sort((a, b) => a.modified - b.modified || a.path.localeCompare(b.path))) {
    if (bytes <= limit) break;
    if (file.path.endsWith(".lock")) continue;
    try { await unlink(file.path); bytes -= file.bytes; }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error; bytes -= file.bytes; }
  }
  if (bytes > limit) throw new Error("The Project cache exceeds its size limit. Clear unused cache files in the Environment page.");
  return bytes;
}

if (import.meta.main) {
  try {
    const bytes = process.argv[2] === "clear" ? await clearProjectCache() : (await projectCacheUsage()).bytes;
    console.log(JSON.stringify({ cache_bytes: bytes }));
  } catch (error) { console.error(String(error)); process.exit(1); }
}
