import { afterEach, expect, test } from "bun:test";
import { Database } from "bun:sqlite";
import { mkdtemp, rm, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { readNativeTrace } from "./trace-read";

const roots: string[] = [];
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });

test("the native trace reader preserves record identifiers and bounds long messages", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-trace-read-")); roots.push(root);
  const path = join(root, "opencode.db");
  const db = new Database(path);
  db.exec(`CREATE TABLE session_v2(id TEXT,parent_id TEXT,title TEXT,time_created INTEGER,time_updated INTEGER);
    CREATE TABLE session_message(id TEXT,session_id TEXT,type TEXT,seq INTEGER,time_created INTEGER,time_updated INTEGER,data TEXT);
    INSERT INTO session_v2 VALUES ('ses_main',NULL,'Main session',1,2)`);
  const data = JSON.stringify({ content: [{ type: "tool", id: "tool_native", text: "x".repeat(25_000) }] });
  db.query("INSERT INTO session_message VALUES (?,?,?,?,?,?,?)").run("msg_native", "ses_main", "assistant", 1, 1, 2, data);
  db.query("INSERT INTO session_message VALUES (?,?,?,?,?,?,?)").run("msg_first", "ses_main", "user", 0, 1, 1, '{"text":"First message"}');
  db.close();
  expect(await readNativeTrace(path, [], root)).toMatchObject({ sessions: [{ id: "ses_main", parent_id: null }] });
  expect((await readNativeTrace(path, ["ses_main"], root) as any).messages.map((row: any) => row.id)).toEqual(["msg_first", "msg_native"]);
  const result = await readNativeTrace(path, ["ses_main", "0"], root) as any;
  expect(result.messages[0]).toMatchObject({ id: "msg_native", session_id: "ses_main", seq: 1, characters: data.length });
  expect(result.messages[0].data).toHaveLength(16_000);
  const next = await readNativeTrace(path, ["ses_main", "0", "msg_native", "16000"], root) as any;
  expect(result.messages[0].data + next.data).toBe(data);
  expect((await readNativeTrace(path, ["ses_main", "1"], root) as any).messages).toEqual([]);
  await expect(readNativeTrace(path, ["ses_main; DROP TABLE session_message"], root)).rejects.toThrow();
  const link = join(root, "link.db");
  await symlink(path, link);
  await expect(readNativeTrace(link, [], root)).rejects.toThrow();
  await expect(readNativeTrace(path, [], join(root, "other"))).rejects.toThrow();
});
