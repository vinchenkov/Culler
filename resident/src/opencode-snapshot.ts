import { Database } from "bun:sqlite";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { createTraceMask } from "./trace-mask";

export interface MaskSignature { length: number; rolling: number; digest: string }
export interface NativeSnapshot {
  bytes: Uint8Array;
  digest: string;
  masks: MaskSignature[];
  sessions: string[];
  main: string;
}

export const NATIVE_MAX_BYTES = 128 << 20;
const MAX_ROWS = 200_000;
const MAX_SESSIONS = 256;
const digest = (value: Uint8Array) => createHash("sha256").update(value).digest("hex");
const rolling = (value: Uint8Array) => value.reduce((sum, byte) => (Math.imul(sum, 31) + byte) >>> 0, 0);

/** Hashes retain prior masks without a retained credential value. */
export function nativeMask(credentials: readonly string[], prior: MaskSignature[]) {
  const current = createTraceMask(credentials);
  const signatures = new Map(prior.map(item => [item.digest, item]));
  const groups = new Map<number, Map<number, Set<string>>>();
  for (const item of prior) {
    if (!Number.isInteger(item.length) || item.length < 1 || item.length > NATIVE_MAX_BYTES ||
        !/^[a-f0-9]{64}$/.test(item.digest)) throw new Error("The native mask record is invalid.");
    let hashes = groups.get(item.length);
    if (!hashes) groups.set(item.length, hashes = new Map());
    let digests = hashes.get(item.rolling);
    if (!digests) hashes.set(item.rolling, digests = new Set());
    digests.add(item.digest);
  }
  function maskBytes(value: Buffer): Buffer {
    const result = current.apply(value, (start, length) => {
      const original = value.subarray(start, start + length);
      const hash = digest(original);
      signatures.set(hash, { length: original.length, rolling: rolling(original), digest: hash });
    });
    for (const [length, hashes] of groups) {
      if (length > value.length) continue;
      let power = 1;
      for (let i = 1; i < length; i++) power = Math.imul(power, 31) >>> 0;
      let hash = rolling(value.subarray(0, length));
      for (let at = 0; at + length <= value.length; at++) {
        const candidates = hashes.get(hash);
        if (candidates?.has(digest(value.subarray(at, at + length)))) result.fill(42, at, at + length);
        hash = (Math.imul((hash - Math.imul(value[at]!, power)) >>> 0, 31) + (value[at + length] ?? 0)) >>> 0;
      }
    }
    return result;
  }
  function json(value: any): any {
    if (typeof value === "string") return maskBytes(Buffer.from(value)).toString("utf8");
    if (Array.isArray(value)) return value.map(json);
    if (value && typeof value === "object") {
      const out = Object.fromEntries(Object.entries(value).map(([key, item]) => [json(key), json(item)]));
      if (typeof value.mime === "string" && typeof value.data === "string" && /^[A-Za-z0-9+/]*={0,2}$/.test(value.data)) {
        const bytes = Buffer.from(value.data, "base64");
        const masked = maskBytes(bytes);
        if (!bytes.equals(masked)) out.data = masked.toString("base64");
      }
      return out;
    }
    return value;
  }
  return {
    signatures: () => [...signatures.values()].sort((a, b) => a.digest.localeCompare(b.digest)),
    value(value: unknown): any {
      if (value instanceof Uint8Array) return maskBytes(Buffer.from(value));
      if (typeof value !== "string") return value;
      try {
        const parsed = JSON.parse(value);
        const masked = json(parsed);
        return JSON.stringify(parsed) === JSON.stringify(masked) ? value : JSON.stringify(masked);
      } catch { return json(value); }
    },
  };
}

const columns: Record<string, string[]> = {
  project: ["id", "worktree", "time_created", "time_updated"],
  session_v2: ["id", "project_id", "parent_id", "title", "directory", "time_created", "time_updated", "time_idle",
    "agent", "model", "cost", "tokens_input", "tokens_output", "tokens_reasoning", "tokens_cache_read", "tokens_cache_write"],
  session_message: ["id", "session_id", "type", "seq", "time_created", "time_updated", "data"],
};
const quote = (name: string) => `"${name.replaceAll('"', '""')}"`;

/** One read transaction includes committed WAL records. The new database contains transcript tables only. */
export function nativeSnapshot(source: string, main: string | undefined, credentials: readonly string[], prior: MaskSignature[], standalone = false): NativeSnapshot {
  let input: Database;
  if (standalone) {
    // The host validates an unchanged main file and absent journals before and after this read.
    const bytes = readFileSync(source);
    if (bytes.length < 100 || bytes.subarray(0, 16).toString() !== "SQLite format 3\0") throw new Error("The native database header is invalid.");
    // An in-memory database has no WAL. Only this private memory copy changes its journal flags.
    bytes[18] = 1;
    bytes[19] = 1;
    input = Database.deserialize(bytes, { readonly: true });
  } else {
    input = new Database(source, { readonly: true });
  }
  const output = new Database(":memory:");
  try {
    input.exec("PRAGMA trusted_schema=OFF; PRAGMA query_only=ON; PRAGMA busy_timeout=100; BEGIN");
    const schema = input.query("SELECT name FROM sqlite_schema WHERE type='table'").all() as { name: string }[];
    const names = new Set(schema.map(row => row.name));
    const sessionTable = names.has("session_v2") ? "session_v2" : "session";
    if (!names.has(sessionTable) || !names.has("session_message") || !names.has("project")) {
      throw new Error("The native database has no supported OpenCode transcript schema.");
    }
    const sessions = input.query(`SELECT id, parent_id FROM ${quote(sessionTable)} LIMIT ?`).all(MAX_SESSIONS + 1) as { id: string; parent_id: string | null }[];
    if (sessions.length > MAX_SESSIONS) throw new Error("The native database exceeds the session limit.");
    if (sessions.some(row => !/^ses_[A-Za-z0-9_-]{1,124}$/.test(row.id))) throw new Error("The native database has an invalid session identity.");
    const roots = sessions.filter(row => !row.parent_id);
    main ??= roots.length === 1 ? roots[0]!.id : undefined;
    if (!main || !roots.some(row => row.id === main)) throw new Error("The native database has no unambiguous main session.");
    const mask = nativeMask(credentials, prior);
    let rows = 0, bytes = 0;
    output.exec("BEGIN");
    for (const [kind, allowed] of Object.entries(columns)) {
      const table = kind === "session_v2" ? sessionTable : kind;
      const fields = input.query(`PRAGMA table_info(${quote(table)})`).all() as { name: string; type: string }[];
      const selected = allowed.filter(name => fields.some(field => field.name === name));
      const definitions = selected.map(name => {
        const type = fields.find(field => field.name === name)!.type.toUpperCase();
        return `${quote(name)} ${["TEXT", "INTEGER", "REAL", "BLOB"].includes(type) ? type : "BLOB"}`;
      });
      output.exec(`CREATE TABLE ${quote(table)} (${definitions.join(",")})`);
      const insert = output.prepare(`INSERT INTO ${quote(table)} VALUES (${selected.map(() => "?").join(",")})`);
      for (const row of input.query(`SELECT ${selected.map(quote).join(",")} FROM ${quote(table)} ORDER BY id`).iterate() as Iterable<Record<string, any>>) {
        if (++rows > MAX_ROWS) throw new Error("The native database exceeds the record limit.");
        const values = selected.map(name => {
          const value = row[name];
          bytes += typeof value === "string" ? Buffer.byteLength(value) : value instanceof Uint8Array ? value.byteLength : 8;
          if (bytes > NATIVE_MAX_BYTES) throw new Error("The native database exceeds the transcript byte limit.");
          return mask.value(value);
        });
        insert.run(...values);
      }
    }
    output.exec(`CREATE UNIQUE INDEX session_identity ON ${quote(sessionTable)}(id);
      CREATE INDEX message_session ON session_message(session_id);
      CREATE UNIQUE INDEX message_identity ON session_message(id); COMMIT`);
    input.exec("COMMIT");
    const result = output.serialize();
    return { bytes: result, digest: digest(result), masks: mask.signatures(), sessions: sessions.map(row => row.id), main };
  } finally { input.close(); output.close(); }
}
