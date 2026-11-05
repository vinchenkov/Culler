import { constants, lstat, open, opendir } from "node:fs/promises";
import { join } from "node:path";
import { checkInside, openFolderInside } from "./trace-copy";

const NATIVE_REF = /^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$/;
const ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
const ROLLOUT = /^rollout-[A-Za-z0-9._-]+\.jsonl$/;
const DATE = [/^[0-9]{4}$/, /^[0-9]{2}$/, /^[0-9]{2}$/];
export const SETUP_RECOVERY_PER_PASS = 16;
export const SETUP_RECOVERY_BYTES = 1 << 20;
const MAX_ENTRIES = 256;

export interface SetupTraceCandidate {
  kind: "setup";
  id: string;
  session_path: string;
  format: "claude" | "codex";
  display_name: string;
  ended: boolean;
}

export function setupTraceCandidates(value: unknown): SetupTraceCandidate[] {
  if (!Array.isArray(value)) return [];
  return value.filter((row): row is SetupTraceCandidate => row && row.kind === "setup" &&
    typeof row.id === "string" && ID.test(row.id) && row.session_path === `environment-runs/${row.id}/session` &&
    (row.format === "claude" || row.format === "codex") && typeof row.display_name === "string" && typeof row.ended === "boolean");
}

/** The operation controls no path outside this fixed folder. */
export async function setupTraceFolder(home: string, id: string): Promise<string> {
  if (!ID.test(id)) throw new Error("the setup operation ID is invalid");
  let path = home;
  for (const part of ["environment-runs", id, "session"]) {
    path = join(path, part);
    const entry = await lstat(path);
    if (!entry.isDirectory() || entry.isSymbolicLink()) throw new Error("the setup trace folder is not a regular folder");
  }
  const handle = await openFolderInside(home, path, "setup trace folder");
  await handle.close();
  return path;
}

async function prefix(root: string, file: string, limit = SETUP_RECOVERY_BYTES): Promise<{ body: string; bytesRead: number }> {
  const path = join(root, file);
  let parent = root;
  for (const part of file.split("/").slice(0, -1)) {
    parent = join(parent, part);
    const info = await lstat(parent);
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error("the trace path contains a symbolic link or non-folder");
  }
  const handle = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    if (!(await handle.stat()).isFile()) throw new Error("the setup trace is not a regular file");
    await checkInside(handle, root, path, "setup trace");
    const buffer = Buffer.alloc(limit);
    const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
    const complete = buffer.subarray(0, bytesRead).lastIndexOf(10);
    if (complete < 0) throw new Error("the setup trace has no complete identity record");
    return { body: buffer.subarray(0, complete + 1).toString("utf8"), bytesRead };
  } finally { await handle.close(); }
}

/** Native identity records occur at the start of each supported main trace. */
export function nativeTraceIdentity(format: "claude" | "codex", body: string): string {
  const ids = new Set<string>();
  for (const line of body.split("\n")) {
    if (line === "") continue;
    let row;
    try { row = JSON.parse(line); } catch { throw new Error("the setup trace contains invalid JSON"); }
    if (!row || typeof row !== "object") throw new Error("the setup trace contains an invalid record");
    const id = format === "codex" ? (row.type === "session_meta" ? row.payload?.id : undefined) : row.sessionId;
    if (id === undefined) continue;
    if (typeof id !== "string" || !NATIVE_REF.test(id)) throw new Error("the setup trace has an invalid native identity");
    if (format === "claude" && row.isSidechain === true) throw new Error("the setup main trace identifies a child session");
    ids.add(id);
  }
  if (ids.size !== 1) throw new Error(ids.size ? "the setup trace has conflicting native identities" : "the setup trace has no native identity record");
  return [...ids][0]!;
}

/** The walk reads at most MAX_ENTRIES entries and accepts one main rollout. */
async function codexMain(root: string): Promise<string> {
  let entries = 0;
  const files: string[] = [];
  async function walk(relative: string, level: number): Promise<void> {
    const path = join(root, relative);
    const handle = await openFolderInside(root, path, "setup trace folder");
    try {
      const directory = await opendir(path);
      for await (const entry of directory) {
        if (++entries > MAX_ENTRIES) throw new Error("the setup trace folder exceeds the recovery entry limit");
        if (entry.isSymbolicLink()) throw new Error("the setup trace folder contains a symbolic link");
        const next = relative ? `${relative}/${entry.name}` : entry.name;
        if (entry.isFile() && ROLLOUT.test(entry.name) && (level === 0 || level === 3)) files.push(next);
        else if (entry.isDirectory() && DATE[level]?.test(entry.name)) await walk(next, level + 1);
      }
      await checkInside(handle, root, path, "setup trace folder");
    } finally { await handle.close(); }
  }
  await walk("", 0);
  const main: string[] = [];
  let budget = SETUP_RECOVERY_BYTES;
  for (const file of files) {
    if (budget <= 0) throw new Error("the setup trace headers exceed the recovery byte limit");
    const { body, bytesRead } = await prefix(root, file, Math.min(64 << 10, budget));
    budget -= bytesRead;
    let first;
    try { first = JSON.parse(body.split("\n")[0]!); } catch { throw new Error("the setup rollout has invalid metadata"); }
    if (first?.type !== "session_meta") throw new Error("the setup rollout has no initial session metadata");
    const source = first.payload?.source;
    if (source && typeof source === "object" && source.subagent) continue;
    main.push(file);
  }
  if (main.length !== 1) throw new Error(main.length ? "the setup trace has ambiguous main rollouts" : "the setup trace has no main rollout");
  return main[0]!;
}

export async function recoverSetupTrace(home: string, row: SetupTraceCandidate): Promise<{ nativeRef: string; filename: string }> {
  const root = await setupTraceFolder(home, row.id);
  const filename = row.format === "claude" ? "native.jsonl" : await codexMain(root);
  const nativeRef = nativeTraceIdentity(row.format, (await prefix(root, filename)).body);
  return { nativeRef, filename };
}
