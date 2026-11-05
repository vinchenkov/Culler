import { expect, test } from "bun:test";
import { appendFile, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { collectLiveUsage, snapshotNativeUsage } from "../../tests/live-model/usage";

const line = (value: unknown) => JSON.stringify(value) + "\n";
test("Codex records reported tokens and separates subscription allowance from charges", async () => {
  const usage = await collectLiveUsage({ harness: "codex", authMode: "account", stdout: line({ type: "turn.completed",
    usage: { input_tokens: 31, output_tokens: 7, cached_input_tokens: 10, private_field: "synthetic-secret" } }) });
  expect(usage).toEqual({ billing: "subscription-allowance", tokens: { status: "reported", source: "codex.turn.completed",
    scope: "conversation_total", records: 1, values: { input_tokens: 31, output_tokens: 7, cached_input_tokens: 10 } },
    harness_cost: { status: "unavailable" }, subscription_allowance_remaining: { status: "unavailable" } });
  expect(JSON.stringify(usage)).not.toContain("synthetic-secret");
});

test("Claude records reported usage and harness cost without treating them as an invoice", async () => {
  const usage = await collectLiveUsage({ harness: "claude-sdk", authMode: "api-key", stdout: line({ type: "result",
    usage: { input_tokens: 10, output_tokens: 4, cache_read_input_tokens: 2, cache_creation_input_tokens: 6 },
    modelUsage: { ignored: { inputTokens: 999, outputTokens: 999 } }, total_cost_usd: 0.0012, result: "synthetic-secret" }) });
  expect(usage).toEqual({ billing: "api-charges", tokens: { status: "reported", source: "claude.result.usage", scope: "reported_result", records: 1,
    values: { input_tokens: 10, output_tokens: 4, cached_input_tokens: 2, cache_write_input_tokens: 6 } },
    harness_cost: { status: "reported", source: "claude.result.total_cost_usd", usd: 0.0012 } });
  expect(JSON.stringify(usage)).not.toContain("synthetic-secret");
});

test("Claude model totals provide a fallback without retained model names", async () => {
  const usage = await collectLiveUsage({ harness: "claude-sdk", authMode: "account", stdout: line({ type: "result",
    modelUsage: { "untrusted-name": { inputTokens: 3, outputTokens: 1, cacheReadInputTokens: 2 },
      other: { inputTokens: 5, outputTokens: 2 } } }) });
  expect(usage.tokens).toEqual({ status: "reported", source: "claude.result.modelUsage", scope: "reported_result", records: 2,
    values: { input_tokens: 8, output_tokens: 3 } });
  expect(JSON.stringify(usage)).not.toContain("untrusted-name");
});

test("invalid, incomplete, duplicate, and absent usage stays unavailable", async () => {
  for (const input of ["31", -1, 1.5, null, Number.MAX_SAFE_INTEGER + 1]) {
    const usage = await collectLiveUsage({ harness: "codex", authMode: "api-key", stdout: line({ type: "turn.completed",
      usage: { input_tokens: input, output_tokens: 3 } }) });
    expect(usage.tokens).toEqual({ status: "unavailable" });
  }
  for (const stdout of ["", "not json", line({ type: "turn.completed", usage: { input_tokens: 2 } }),
    line({ type: "turn.completed", usage: { input_tokens: 2, output_tokens: 1 } }).repeat(2)]) {
    expect((await collectLiveUsage({ harness: "codex", authMode: "api-key", stdout })).tokens).toEqual({ status: "unavailable" });
  }
  const overflow = await collectLiveUsage({ harness: "claude-sdk", authMode: "api-key", stdout: line({ type: "result",
    modelUsage: { a: { inputTokens: Number.MAX_SAFE_INTEGER, outputTokens: 1 }, b: { inputTokens: 1, outputTokens: 1 } },
    total_cost_usd: "0.3" }) });
  expect(overflow.tokens).toEqual({ status: "unavailable" });
  expect(overflow.harness_cost).toEqual({ status: "unavailable" });
});

test("native resume counts only new per-response usage and ignores cumulative records", async () => {
  const directory = await mkdtemp(join(tmpdir(), "culler-usage-"));
  const path = join(directory, "native.jsonl");
  const record = (id: string, input: number, output: number) => line({ type: "token_usage_record", payload: {
    thread_id: "thread", response_id: id, usage: { input_tokens: input, output_tokens: output },
    turn_token_usage: { input_tokens: 999, output_tokens: 999 }, thread_token_usage: { input_tokens: 9999, output_tokens: 9999 } } });
  try {
    await writeFile(path, record("previous", 100, 20));
    const nativeBefore = await snapshotNativeUsage(directory);
    await appendFile(path, record("new-1", 10, 2) + record("new-1", 10, 2) + record("new-2", 12, 3));
    const usage = await collectLiveUsage({ harness: "codex", authMode: "api-key", stdout: "", sessionDirectory: directory, nativeBefore });
    expect(usage.tokens).toEqual({ status: "reported", source: "codex.token_usage_record", scope: "new_responses", records: 2,
      values: { input_tokens: 22, output_tokens: 5 } });
    const preferred = await collectLiveUsage({ harness: "codex", authMode: "api-key",
      stdout: line({ type: "turn.completed", usage: { input_tokens: 7, output_tokens: 1 } }), sessionDirectory: directory, nativeBefore });
    expect(preferred.tokens).toEqual({ status: "reported", source: "codex.token_usage_record", scope: "new_responses", records: 2,
      values: { input_tokens: 22, output_tokens: 5 } });
    await rm(path);
    await writeFile(path, record("replaced", 200, 20));
    expect((await collectLiveUsage({ harness: "codex", authMode: "api-key", stdout: "", sessionDirectory: directory, nativeBefore })).tokens)
      .toEqual({ status: "unavailable" });
  } finally { await rm(directory, { recursive: true, force: true }); }
});
