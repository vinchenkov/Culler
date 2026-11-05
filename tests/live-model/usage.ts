import { lstat, open, readdir } from "node:fs/promises";
import { join } from "node:path";

type Tokens = Partial<Record<"input_tokens" | "output_tokens" | "cached_input_tokens" |
  "cache_write_input_tokens" | "reasoning_output_tokens" | "total_tokens", number>>;
type UsageSource = "codex.turn.completed" | "codex.token_usage_record" | "claude.result.usage" | "claude.result.modelUsage";
export interface LiveUsageEvidence {
  billing: "api-charges" | "subscription-allowance";
  tokens: { status: "reported"; source: UsageSource; scope: "new_responses" | "conversation_total" | "reported_result"; records: number; values: Tokens } | { status: "unavailable" };
  harness_cost: { status: "reported"; source: "claude.result.total_cost_usd"; usd: number } | { status: "unavailable" };
  subscription_allowance_remaining?: { status: "unavailable" };
}
interface NativeFile { size: number; inode: number; }
export type NativeUsageCursor = Map<string, NativeFile>;
const LIMIT = 16 << 20;

function object(value: unknown): value is Record<string, any> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function numbers(value: unknown, fields: Record<string, keyof Tokens>): Tokens | undefined {
  if (!object(value)) return;
  const result: Tokens = {};
  for (const [from, to] of Object.entries(fields)) {
    if (!(from in value)) continue;
    const number = value[from];
    if (!Number.isSafeInteger(number) || number < 0) return;
    result[to] = number;
  }
  return result.input_tokens !== undefined && result.output_tokens !== undefined ? result : undefined;
}

const snake = { input_tokens: "input_tokens", output_tokens: "output_tokens",
  cached_input_tokens: "cached_input_tokens", cache_write_input_tokens: "cache_write_input_tokens",
  reasoning_output_tokens: "reasoning_output_tokens", total_tokens: "total_tokens" } as const;
const claude = { input_tokens: "input_tokens", output_tokens: "output_tokens",
  cache_read_input_tokens: "cached_input_tokens", cache_creation_input_tokens: "cache_write_input_tokens" } as const;
const camel = { inputTokens: "input_tokens", outputTokens: "output_tokens",
  cacheReadInputTokens: "cached_input_tokens", cacheCreationInputTokens: "cache_write_input_tokens" } as const;

function sum(records: Tokens[]): Tokens | undefined {
  if (!records.length) return;
  const result: Tokens = {};
  for (const key of Object.keys(snake) as (keyof Tokens)[]) {
    if (records.some(record => record[key] === undefined)) continue;
    const value = records.reduce((total, record) => total + record[key]!, 0);
    if (!Number.isSafeInteger(value)) return;
    result[key] = value;
  }
  return result;
}

function events(body: string): Record<string, any>[] {
  if (Buffer.byteLength(body) > LIMIT) return [];
  return body.split("\n").flatMap(line => {
    try { const value = JSON.parse(line); return object(value) ? [value] : []; }
    catch { return []; }
  });
}

export async function snapshotNativeUsage(directory: string): Promise<NativeUsageCursor> {
  const result: NativeUsageCursor = new Map();
  async function visit(relative: string, depth: number): Promise<void> {
    if (depth > 8) throw new Error("The native usage directory exceeds its depth limit.");
    for (const entry of await readdir(join(directory, relative), { withFileTypes: true })) {
      const path = join(relative, entry.name);
      if (entry.isSymbolicLink()) throw new Error("The native usage directory contains a symbolic link.");
      if (entry.isDirectory()) await visit(path, depth + 1);
      else if (entry.isFile() && entry.name.endsWith(".jsonl")) {
        if (result.size >= 256) throw new Error("The native usage directory exceeds its file limit.");
        const info = await lstat(join(directory, path));
        result.set(path, { size: info.size, inode: info.ino });
      }
    }
  }
  await visit("", 0);
  return result;
}

async function nativeRecords(directory: string, before: NativeUsageCursor): Promise<Record<string, any>[]> {
  const after = await snapshotNativeUsage(directory);
  let size = 0;
  const records: Record<string, any>[] = [];
  for (const [path, current] of after) {
    const prior = before.get(path);
    if (prior && (prior.inode !== current.inode || prior.size > current.size)) return [];
    const offset = prior?.size ?? 0;
    size += current.size - offset;
    if (size > LIMIT) return [];
    const file = await open(join(directory, path), "r");
    try {
      const bytes = Buffer.alloc(current.size - offset);
      const result = await file.read(bytes, 0, bytes.length, offset);
      if (result.bytesRead !== bytes.length) return [];
      records.push(...events(bytes.toString("utf8")));
    } finally { await file.close(); }
  }
  return records;
}

export async function collectLiveUsage(input: {
  harness: string; authMode: "api-key" | "account"; stdout: string;
  sessionDirectory?: string; nativeBefore?: NativeUsageCursor;
}): Promise<LiveUsageEvidence> {
  const evidence: LiveUsageEvidence = {
    billing: input.authMode === "api-key" ? "api-charges" : "subscription-allowance",
    tokens: { status: "unavailable" }, harness_cost: { status: "unavailable" },
    ...(input.authMode === "account" ? { subscription_allowance_remaining: { status: "unavailable" as const } } : {}),
  };
  const output = events(input.stdout);
  if (input.harness === "claude-sdk") {
    const results = output.filter(event => event.type === "result");
    if (results.length !== 1) return evidence;
    const result = results[0]!;
    if (typeof result.total_cost_usd === "number" && Number.isFinite(result.total_cost_usd) && result.total_cost_usd >= 0) {
      evidence.harness_cost = { status: "reported", source: "claude.result.total_cost_usd", usd: result.total_cost_usd };
    }
    const usage = numbers(result.usage, claude);
    if (usage) evidence.tokens = { status: "reported", source: "claude.result.usage", scope: "reported_result", records: 1, values: usage };
    else if (result.usage === undefined && object(result.modelUsage)) {
      const models = Object.values(result.modelUsage).map(value => numbers(value, camel));
      const totals = models.every(Boolean) ? sum(models as Tokens[]) : undefined;
      if (totals) evidence.tokens = { status: "reported", source: "claude.result.modelUsage", scope: "reported_result", records: models.length, values: totals };
    }
  } else if (input.harness === "codex") {
    const turns = output.filter(event => event.type === "turn.completed");
    if (turns.length === 1) {
      const usage = numbers(turns[0]!.usage, snake);
      if (usage) evidence.tokens = { status: "reported", source: "codex.turn.completed", scope: "conversation_total", records: 1, values: usage };
    }
    if (input.sessionDirectory && input.nativeBefore) {
      try {
        const entries = await nativeRecords(input.sessionDirectory, input.nativeBefore);
        const seen = new Set<string>();
        const usage: Tokens[] = [];
        for (const entry of entries) {
          if (entry.type !== "token_usage_record" || !object(entry.payload)) continue;
          const payload = entry.payload;
          if (typeof payload.response_id !== "string" || !payload.response_id || typeof payload.thread_id !== "string") continue;
          const key = payload.thread_id + "\0" + payload.response_id;
          if (seen.has(key)) continue;
          seen.add(key);
          const values = numbers(payload.usage, snake);
          if (!values) return evidence;
          usage.push(values);
        }
        const totals = sum(usage);
        if (totals) evidence.tokens = { status: "reported", source: "codex.token_usage_record", scope: "new_responses", records: usage.length, values: totals };
      } catch { /* Missing native evidence remains explicitly unavailable. */ }
    }
  }
  return evidence;
}
