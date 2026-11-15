import { constants, lstat, mkdir, open, readFile, rename, rm, type FileHandle } from "node:fs/promises";
import { dirname, join } from "node:path";
import { createHash, randomUUID } from "node:crypto";
import { checkInside, openFolderInside } from "./trace-copy";
import { OPENCODE_DATABASE } from "../../runner/shared/opencode-trace";
import type { MaskSignature, NativeSnapshot } from "./opencode-snapshot";

export const NATIVE_COPY_TIMEOUT_MS = 5_000;
const SOURCE_MAX_BYTES = 512 << 20;
const flags = constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK;

async function sourceHandles(root: string, filename: string): Promise<Array<{ handle: FileHandle; path: string }>> {
  const handles: Array<{ handle: FileHandle; path: string }> = [];
  try {
    for (const suffix of ["", "-wal", "-shm", "-journal"]) {
      const path = join(root, filename + suffix);
      const handle = await open(path, flags).catch(error => {
        if (suffix && error.code === "ENOENT") return undefined;
        throw error;
      });
      if (!handle) continue;
      handles.push({ handle, path });
      const info = await handle.stat();
      if (!info.isFile() || info.size > SOURCE_MAX_BYTES) throw new Error("The native source is not a bounded regular file.");
      await checkInside(handle, root, path, "native database");
    }
    return handles;
  } catch (error) {
    await Promise.all(handles.map(({ handle }) => handle.close()));
    throw error;
  }
}

export async function readNativeSnapshot(root: string, main?: string, credentials: readonly string[] = [], masks: MaskSignature[] = [], signal?: AbortSignal, filename = OPENCODE_DATABASE): Promise<NativeSnapshot> {
  const handles = await sourceHandles(root, filename);
  try {
    const wal = handles.some(file => file.path.endsWith("-wal"));
    const shared = handles.some(file => file.path.endsWith("-shm"));
    if (wal !== shared) throw new Error("The native database has incomplete WAL auxiliary files. A later pass will retry.");
    const standalone = handles.length === 1;
    const before = await handles[0]!.handle.stat({ bigint: true });
    const snapshot = await new Promise<NativeSnapshot>((resolve, reject) => {
      const worker = new Worker(new URL("./opencode-copy-worker.ts", import.meta.url).href);
      const finish = (error?: Error, result?: NativeSnapshot) => {
        clearTimeout(timer);
        signal?.removeEventListener("abort", abort);
        worker.terminate();
        if (error) reject(error); else resolve(result!);
      };
      const abort = () => finish(new Error("The native trace copy stopped."));
      const timer = setTimeout(() => finish(new Error("The native trace copy exceeded its time limit.")), NATIVE_COPY_TIMEOUT_MS);
      worker.onmessage = event => event.data.error ? finish(new Error(event.data.error)) : finish(undefined, event.data.snapshot);
      worker.onerror = () => finish(new Error("The native trace worker failed."));
      signal?.addEventListener("abort", abort, { once: true });
      if (signal?.aborted) { abort(); return; }
      worker.postMessage({ source: join(root, filename), main, credentials, masks, standalone });
    });
    for (const { handle, path } of handles) await checkInside(handle, root, path, "native database");
    const after = await sourceHandles(root, filename);
    try {
      if (standalone) {
        const latest = await handles[0]!.handle.stat({ bigint: true });
        if (after.length !== 1 || latest.size !== before.size || latest.mtimeNs !== before.mtimeNs || latest.ctimeNs !== before.ctimeNs) {
          throw new Error("The native database changed during the read. A later pass will retry.");
        }
      }
    } finally { await Promise.all(after.map(({ handle }) => handle.close())); }
    return snapshot;
  } finally { await Promise.all(handles.map(({ handle }) => handle.close())); }
}

/** The host replaces each complete file with one atomic rename. */
export async function publishNativeFile(root: string, path: string, bytes: Uint8Array | string, mode = 0o600): Promise<void> {
  await mkdir(dirname(path), { recursive: true, mode: 0o700 });
  const folder = await openFolderInside(root, dirname(path), "native copy folder");
  const temporary = join(dirname(path), `.native-${randomUUID()}.next`);
  try {
    const handle = await open(temporary, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, mode);
    try { await handle.writeFile(bytes); await handle.sync(); } finally { await handle.close(); }
    await checkInside(folder, root, dirname(path), "native copy folder");
    await rename(temporary, path);
  } finally { await folder.close(); await rm(temporary, { force: true }); }
}

export async function copyNativeDatabase(root: string, destination: string, main: string, credentials: readonly string[], signal?: AbortSignal, filename = OPENCODE_DATABASE): Promise<NativeSnapshot & { changed: boolean }> {
  const directory = dirname(destination);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const folder = await openFolderInside(directory, directory, "native copy folder");
  await folder.close();
  const historyPath = destination + ".masks.json";
  let prior: MaskSignature[] = [];
  const history = await open(historyPath, flags).catch(error => { if (error.code !== "ENOENT") throw error; });
  if (history) {
    try {
      if (!(await history.stat()).isFile() || (await history.stat()).size > 4 << 20) throw new Error("The native mask record is invalid.");
      prior = JSON.parse(await history.readFile("utf8"));
      if (!Array.isArray(prior)) throw new Error("The native mask record is invalid.");
    } finally { await history.close(); }
  }
  const result = await readNativeSnapshot(root, main, credentials, prior, signal, filename);
  const current = await lstat(destination).catch(error => { if (error.code !== "ENOENT") throw error; });
  if (current && !current.isFile()) throw new Error("The native trace copy is not a regular file.");
  const changed = !current || createHash("sha256").update(await readFile(destination)).digest("hex") !== result.digest;
  // Commit the hashes first. A failed publication can retry with every prior mask intact.
  await publishNativeFile(directory, historyPath, JSON.stringify(result.masks) + "\n");
  if (changed) await publishNativeFile(directory, destination, result.bytes, 0o644);
  return { ...result, changed };
}
