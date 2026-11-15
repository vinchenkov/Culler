import { Database } from "bun:sqlite";
import { existsSync } from "node:fs";
import { lstat, realpath } from "node:fs/promises";
import { relative, resolve, sep } from "node:path";

const CHUNK = 16_000;
const identifier = /^[A-Za-z0-9_][A-Za-z0-9_-]{0,127}$/;

/** The reader returns bounded records from a native trace copy. */
export function nativeTraceRoot(env: Record<string, string | undefined>, container = existsSync("/mc/run.json")): string {
  return container ? "/mc/traces" : env["MC_TRACE_ROOT"] || "/mc/traces";
}

export async function readNativeTrace(path: string, args: string[], root = "/mc/traces") {
  const absolute = resolve(path);
  const base = await realpath(root);
  const resolved = await realpath(absolute);
  if (!resolved.startsWith(base + sep) || !(await lstat(absolute)).isFile()) throw new Error("The database must be a regular trace copy.");
  let current = root;
  for (const part of relative(root, absolute).split(sep)) {
    current = resolve(current, part);
    if ((await lstat(current)).isSymbolicLink()) throw new Error("The trace path contains a symbolic link.");
  }
  const db = new Database(absolute, { readonly: true });
  try {
    db.exec("PRAGMA query_only=ON; PRAGMA trusted_schema=OFF; PRAGMA busy_timeout=100");
    const table = db.query("SELECT name FROM sqlite_schema WHERE type='table' AND name='session_v2'").get() ? "session_v2" : "session";
    if (!args.length) return { sessions: db.query(`SELECT id,parent_id,title,time_created,time_updated FROM ${table} ORDER BY id LIMIT 256`).all() };
    const [session, after = "-1", message, offset = "0"] = args;
    if (!identifier.test(session!) || !/^(?:-1|\d{1,10})$/.test(after) || !/^\d{1,10}$/.test(offset) || (message && !identifier.test(message))) {
      throw new Error("Use a native session, a sequence number, and optional message and character offset.");
    }
    if (message) {
      return db.query(`SELECT id,session_id,type,seq,time_created,time_updated,length(data) AS characters,
        substr(data,?,?) AS data FROM session_message WHERE session_id=? AND id=?`).get(Number(offset) + 1, CHUNK, session!, message);
    }
    return { messages: db.query(`SELECT id,session_id,type,seq,time_created,time_updated,length(data) AS characters,
      substr(data,1,?) AS data FROM session_message WHERE session_id=? AND seq>? ORDER BY seq LIMIT 20`).all(CHUNK, session!, Number(after)) };
  } finally { db.close(); }
}

if (import.meta.main) {
  try {
    const [path, ...args] = process.argv.slice(2);
    if (!path) throw new Error("Supply the native database path.");
    const root = nativeTraceRoot(process.env);
    console.log(JSON.stringify(await readNativeTrace(path, args, root)));
  } catch { console.error("The native trace read failed. Check the path, arguments, and status.json."); process.exitCode = 1; }
}
