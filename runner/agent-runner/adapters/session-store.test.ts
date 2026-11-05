import { afterEach, describe, expect, test } from "bun:test";
import { chmodSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { MAIN_NATIVE_FILE, NativeSessionStore } from "./session-store";

const roots: string[] = [];
afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

function root(): string {
  const value = mkdtempSync(join(tmpdir(), "mc-native-store-"));
  roots.push(value);
  return value;
}

const key = { projectKey: "workspace", sessionId: "11111111-1111-4111-8111-111111111111" };

describe("Claude native SessionStore", () => {
  test("eager append durably writes native JSONL and resume loads it", async () => {
    const dir = root();
    const store = new NativeSessionStore(dir);
    await store.append(key, [
      { type: "user", uuid: "u-1", body: "hello" },
      { type: "assistant", uuid: "a-1", body: "world" },
    ]);
    expect(await store.load(key)).toEqual([
      { type: "user", uuid: "u-1", body: "hello" },
      { type: "assistant", uuid: "a-1", body: "world" },
    ]);
    expect(Bun.file(join(dir, MAIN_NATIVE_FILE)).size).toBeGreaterThan(0);
  });

  test("uuid retries are idempotent while uuid-less native markers append", async () => {
    const store = new NativeSessionStore(root());
    await store.append(key, [{ type: "user", uuid: "same" }, { type: "marker", value: 1 }]);
    await store.append(key, [{ type: "user", uuid: "same" }, { type: "marker", value: 2 }]);
    expect(await store.load(key)).toEqual([
      { type: "user", uuid: "same" },
      { type: "marker", value: 1 },
      { type: "marker", value: 2 },
    ]);
  });

  test("a tool result loses its Culler token before durable native storage", async () => {
    const dir = root();
    const token = "synthetic-culler-token-000000000000000000000000";
    const store = new NativeSessionStore(dir, value => value.split(token).join("[redacted]"));
    await store.append(key, [{ type: "user", uuid: "u-1", body: `tool result: ${token}` }]);
    const restored = new NativeSessionStore(dir);
    expect(await restored.load(key)).toEqual([{ type: "user", uuid: "u-1", body: "tool result: [redacted]" }]);
    expect(await Bun.file(join(dir, MAIN_NATIVE_FILE)).text()).not.toContain(token);
  });

  test("subagent transcripts stay native and are discoverable for resume", async () => {
    const store = new NativeSessionStore(root());
    await store.append({ ...key, subpath: "subagents/agent-7" }, [{ type: "assistant", uuid: "sub-1" }]);
    expect(await store.listSubkeys(key)).toEqual(["subagents/agent-7"]);
    expect(await store.load({ ...key, subpath: "subagents/agent-7" })).toEqual([
      { type: "assistant", uuid: "sub-1" },
    ]);
  });

  test("path traversal, cross-session adoption, and widened transcript modes refuse", async () => {
    const dir = root();
    const store = new NativeSessionStore(dir);
    expect(() => store.append({ ...key, subpath: "../escape" }, [{ type: "user" }])).toThrow("invalid subpath");
    await store.append(key, [{ type: "user", uuid: "u-1" }]);
    expect(() => store.load({ ...key, sessionId: "22222222-2222-4222-8222-222222222222" })).toThrow("cannot adopt");
    chmodSync(join(dir, MAIN_NATIVE_FILE), 0o644);
    await expect(new NativeSessionStore(dir).load(key)).rejects.toThrow("mode-0600");
  });
});
