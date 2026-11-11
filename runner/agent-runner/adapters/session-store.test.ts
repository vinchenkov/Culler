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

  test("signed records and credentials survive append, load, and resume intact", async () => {
    const dir = root();
    const token = "culler_proxy_" + "c".repeat(43);
    const signature = Buffer.from("sk-" + "a".repeat(80)).toString("base64");
    const entries = [
      { type: "user", uuid: "u-1", message: { content: `tool result: ${token}` } },
      { type: "assistant", uuid: "a-1", parentUuid: "u-1", message: {
        id: "msg-1", content: [
          { type: "thinking", thinking: `context ${token}`, signature },
          { type: "redacted_thinking", data: signature },
          { type: "text", text: "sk-" + "b".repeat(80) },
        ],
      } },
    ];
    for (const subpath of [undefined, "subagents/agent-7"]) {
      const sessionKey = { ...key, subpath };
      await new NativeSessionStore(dir).append(sessionKey, entries);
      const path = join(dir, subpath ? `${subpath}.jsonl` : MAIN_NATIVE_FILE);
      const before = await Bun.file(path).text();
      expect(before).toBe(entries.map(entry => JSON.stringify(entry) + "\n").join(""));
      const restored = new NativeSessionStore(dir);
      expect(await restored.load(sessionKey)).toEqual(entries);
      await restored.append(sessionKey, entries);
      expect(await Bun.file(path).text()).toBe(before);
    }
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
