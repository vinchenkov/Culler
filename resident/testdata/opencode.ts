import { Database } from "bun:sqlite";
import { mkdirSync } from "node:fs";
import { dirname } from "node:path";

/** OpenCode 2.2.5 uses these transcript columns. Account data stays outside the transcript. */
export function nativeFixture(path: string, text = "A native reply", main = "ses_main") {
  mkdirSync(dirname(path), { recursive: true });
  const db = new Database(path);
  db.exec(`PRAGMA journal_mode=WAL;
    CREATE TABLE project(id TEXT PRIMARY KEY, worktree TEXT NOT NULL, time_created INTEGER, time_updated INTEGER);
    CREATE TABLE session_v2(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, title TEXT,
      directory TEXT, time_created INTEGER, time_updated INTEGER, time_idle INTEGER, agent TEXT, model TEXT);
    CREATE TABLE session_message(id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL,
      seq INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
    CREATE UNIQUE INDEX session_message_session_seq_idx ON session_message(session_id,seq);
    CREATE TABLE credential(id TEXT PRIMARY KEY, secret TEXT);`);
  db.query("INSERT INTO project VALUES (?,?,?,?)").run("project", "/workspace/source", 1_700_000_000_000, 1_700_000_001_000);
  db.query("INSERT INTO session_v2 VALUES (?,?,?,?,?,?,?,?,?,?)").run(main, "project", null, "Native session", "/workspace/source", 1_700_000_000_000, 1_700_000_001_000, 1_700_000_001_000, "build", '{"id":"fixture-model","providerID":"fixture"}');
  db.query("INSERT INTO session_message VALUES (?,?,?,?,?,?,?)").run("msg_user", main, "user", 1, 1_700_000_000_000, 1_700_000_000_000, JSON.stringify({ text: "Inspect the task.", time: { created: 1_700_000_000_000 } }));
  db.query("INSERT INTO session_message VALUES (?,?,?,?,?,?,?)").run("msg_reply", main, "assistant", 2, 1_700_000_001_000, 1_700_000_001_000, JSON.stringify({
    agent: "build", model: { id: "fixture-model", providerID: "fixture" },
    time: { created: 1_700_000_001_000, completed: 1_700_000_001_000 },
    tokens: { input: 11, output: 7, reasoning: 2, cache: { read: 3, write: 0 } },
    content: [{ type: "text", text }, { type: "tool", id: "tool_native", name: "shell",
      state: { status: "completed", input: { command: "echo native" }, content: [{ type: "text", text: "native tool output" }] } }],
  }));
  return db;
}
