import { expect, test } from "bun:test";
import { appendFile, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { makeClaudeResponseObserver } from "./claude";
import { codexResponseWatcher, codexTraceOffsets, makeCodexResponseObserver } from "./codex";

const stream = (event: unknown, parent: string | null = null) => ({ type: "stream_event", parent_tool_use_id: parent, event });
const start = (model = "fixture-model") => ({ type: "message_start", message: { id: "response", model } });
const end = { type: "message_delta", delta: { stop_reason: "tool_use" } };
const stop = { type: "message_stop" };

test("Claude confirms a complete tool request before a tool result", () => {
  const observe = makeClaudeResponseObserver("fixture-model");
  expect(observe(stream(start()))).toBe(false);
  expect(observe(stream({ type: "content_block_delta", delta: { type: "input_json_delta", partial_json: "{}" } }))).toBe(false);
  expect(observe(stream(end))).toBe(false);
  expect(observe(stream(stop))).toBe(true);
  expect(observe(stream(stop))).toBe(false);
});

test("Claude excludes partial output, restored messages, subagents, and another model", () => {
  for (const events of [
    [stream(start()), stream({ type: "content_block_delta", delta: { type: "text_delta", text: "partial" } }), stream(stop)],
    [{ type: "assistant", message: { model: "fixture-model", content: [{ type: "text", text: "restored" }] } }],
    [stream(start(), "subagent"), stream(end, "subagent"), stream(stop, "subagent")],
    [stream(start("other")), stream(end), stream(stop)],
    [stream(start()), stream(end), stream({ type: "error" }), stream(stop)],
  ]) {
    expect(events.some(makeClaudeResponseObserver("fixture-model"))).toBe(false);
  }
});

const context = { type: "turn_context", payload: { turn_id: "turn", root_turn_id: "turn", model: "fixture-model" } };
const tool = { type: "response_item", payload: { type: "function_call", arguments: "{}" } };
const usage = { type: "token_usage_record", payload: { thread_id: "thread", turn_id: "turn", root_turn_id: "turn", response_id: "response" } };
const line = (event: unknown) => JSON.stringify(event);

test("Codex requires a fresh complete response for the main thread and model", () => {
  const observe = makeCodexResponseObserver("thread", "fixture-model");
  for (const event of [context, tool, { type: "event_msg", payload: { type: "token_count" } }]) expect(observe(line(event))).toBe(false);
  expect(observe(line({ ...usage, payload: { ...usage.payload, thread_id: "child" } }))).toBe(false);
  expect(observe(line(usage))).toBe(true);
  expect(observe(line(usage))).toBe(false);
  const otherModel = makeCodexResponseObserver("thread", "other");
  expect([context, tool, usage].some(event => otherModel(line(event)))).toBe(false);
  const child = makeCodexResponseObserver("thread", "fixture-model");
  expect([{ ...context, payload: { ...context.payload, root_turn_id: "parent" } }, tool, usage].some(event => child(line(event)))).toBe(false);
});

test("Codex reads only fresh trace bytes and waits for a complete record", async () => {
  const root = await mkdtemp(join(tmpdir(), "response-evidence-"));
  try {
    const path = join(root, "trace.jsonl");
    await writeFile(path, [context, tool, usage].map(line).join("\n") + "\n");
    const offsets = await codexTraceOffsets(root);
    let count = 0;
    const watcher = codexResponseWatcher(path, offsets.get("trace.jsonl")!, makeCodexResponseObserver("thread", "fixture-model"), () => { count++; });
    await Bun.sleep(30);
    expect(count).toBe(0);
    await appendFile(path, [context, tool].map(line).join("\n") + "\n" + line(usage).slice(0, 20));
    await Bun.sleep(120);
    expect(count).toBe(0);
    await appendFile(path, line(usage).slice(20) + "\n");
    await watcher.stop();
    expect(count).toBe(1);
  } finally { await rm(root, { recursive: true, force: true }); }
});
