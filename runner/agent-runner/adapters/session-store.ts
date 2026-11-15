import { lstat, mkdir, open, readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import type {
  SessionKey,
  SessionStore,
  SessionStoreEntry,
} from "@anthropic-ai/claude-agent-sdk";

const MAIN_NATIVE_FILE = "native.jsonl";
const SAFE_COMPONENT = /^[A-Za-z0-9._-]{1,160}$/;

function validateKey(key: SessionKey): void {
  if (!SAFE_COMPONENT.test(key.sessionId)) {
    throw new Error("session store: invalid session id");
  }
  if (key.projectKey.length === 0 || key.projectKey.length > 512) {
    throw new Error("session store: invalid project key");
  }
  if (key.subpath !== undefined) {
    const components = key.subpath.split("/");
    if (components.length === 0 || components.some((part) =>
      part === "." || part === ".." || !SAFE_COMPONENT.test(part))) {
      throw new Error("session store: invalid subpath");
    }
  }
}

function pathFor(root: string, key: SessionKey): string {
  validateKey(key);
  return key.subpath === undefined
    ? join(root, MAIN_NATIVE_FILE)
    : join(root, `${key.subpath}.jsonl`);
}

async function loadEntries(path: string): Promise<SessionStoreEntry[] | null> {
  let body: string;
  try {
    const info = await lstat(path);
    if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o600) {
      throw new Error("native transcript must be a regular mode-0600 file");
    }
    body = await readFile(path, "utf8");
  } catch (err) {
    if (err !== null && typeof err === "object" && "code" in err && err.code === "ENOENT") {
      return null;
    }
    throw err;
  }
  const entries: SessionStoreEntry[] = [];
  for (const line of body.split("\n")) {
    if (line === "") continue;
    const value = JSON.parse(line) as unknown;
    if (value === null || typeof value !== "object" || Array.isArray(value) ||
        typeof (value as { type?: unknown }).type !== "string") {
      throw new Error("native transcript contains a malformed entry");
    }
    entries.push(value as SessionStoreEntry);
  }
  return entries;
}

export class NativeSessionStore implements SessionStore {
  private tail: Promise<void> = Promise.resolve();
  private identity: { projectKey: string; sessionId: string } | undefined;

  constructor(private readonly root: string) {}

  private bind(key: SessionKey): void {
    validateKey(key);
    const next = { projectKey: key.projectKey, sessionId: key.sessionId };
    if (this.identity === undefined) {
      this.identity = next;
      return;
    }
    if (this.identity.projectKey !== next.projectKey || this.identity.sessionId !== next.sessionId) {
      throw new Error("session store: one run folder cannot adopt another session");
    }
  }

  append(key: SessionKey, entries: SessionStoreEntry[]): Promise<void> {
    this.bind(key);
    const work = this.tail.then(async () => {
      if (entries.length === 0) return;
      const path = pathFor(this.root, key);
      await mkdir(dirname(path), { recursive: true, mode: 0o700 });
      const existing = await loadEntries(path);
      const seen = new Set((existing ?? []).flatMap((entry) =>
        typeof entry.uuid === "string" ? [entry.uuid] : []));
      const fresh = entries.filter((entry) => {
        if (entry === null || typeof entry !== "object" || typeof entry.type !== "string") {
          throw new Error("session store: append received a malformed entry");
        }
        if (typeof entry.uuid !== "string") return true;
        if (seen.has(entry.uuid)) return false;
        seen.add(entry.uuid);
        return true;
      });
      if (fresh.length === 0) return;
      const handle = await open(path, "a", 0o600);
      try {
        await handle.writeFile(fresh.map((entry) => JSON.stringify(entry) + "\n").join(""));
        await handle.sync();
      } finally {
        await handle.close();
      }
    });
    this.tail = work.catch(() => {});
    return work;
  }

  async load(key: SessionKey): Promise<SessionStoreEntry[] | null> {
    this.bind(key);
    await this.tail;
    return loadEntries(pathFor(this.root, key));
  }

  async listSubkeys(key: { projectKey: string; sessionId: string }): Promise<string[]> {
    this.bind(key);
    await this.tail;
    const subagents = join(this.root, "subagents");
    let names: string[];
    try {
      names = await readdir(subagents);
    } catch (err) {
      if (err !== null && typeof err === "object" && "code" in err && err.code === "ENOENT") return [];
      throw err;
    }
    return names.filter((name) => name.endsWith(".jsonl") && SAFE_COMPONENT.test(name.slice(0, -6)))
      .sort()
      .map((name) => `subagents/${name.slice(0, -6)}`);
  }
}

export { MAIN_NATIVE_FILE };
