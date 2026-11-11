import { afterEach, expect, test } from "bun:test";
import { Database } from "bun:sqlite";
import { mkdtemp, readFile, rename, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { nativeFixture } from "../testdata/opencode";
import { makeTestLogger } from "../../runner/shared/log";
import { OPENCODE_DATABASE, openCodeTraceID } from "../../runner/shared/opencode-trace";
import { createTraceLane, OVERDUE_MS, SESSIONS_REFRESH_MS, traceCopyPaths, type TraceSession } from "./trace-lane";
import type { AgentsViewClient, AgentsViewSession } from "./agentsview";
import { recoverSetupTrace } from "./setup-traces";

const roots: string[] = [];
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });

async function fixture(kind: "run" | "collie" | "setup" = "run") {
  const home = await mkdtemp(join(tmpdir(), "culler-native-lane-")); roots.push(home);
  const row: TraceSession = { kind, id: "execution", session_path: kind === "setup" ? "environment-runs/execution/session" : "sessions/execution",
    format: "opencode", native_session_ref: "ses_main", trace_filename: OPENCODE_DATABASE,
    agentsview_session: "opencode:ses_main", display_name: "Project · packager · execution", ended: false };
  const source = join(home, row.session_path, OPENCODE_DATABASE);
  const writer = nativeFixture(source);
  const paths = traceCopyPaths(home);
  const destination = join(paths.opencode, "opencode-execution.db");
  const inspection = join(paths.inspection, row.id, OPENCODE_DATABASE);
  let now = Date.now(), refuse = false, pid = 1;
  let secrets: string[] = [];
  const archive = new Map<string, AgentsViewSession>();
  const imports: string[] = [], retired: string[] = [], commands: string[][] = [];
  const client: AgentsViewClient = {
    bind: "127.0.0.1:1", ping: async () => pid, ensureProjectRules: async () => {},
    listSessions: async () => ({ sessions: [...archive.values()], nextCursor: null }),
    session: async id => archive.get(id) ?? null,
    rename: async (id, name) => { if (archive.has(id)) archive.get(id)!.displayName = name; },
    syncNative: async id => {
      if (refuse) throw new Error("The synthetic import failed.");
      imports.push(id);
      archive.set(id, { id, displayName: null, endedAt: null });
    },
    retireExport: async (legacy, native) => {
      expect(archive.has(native)).toBe(true);
      retired.push(legacy);
      archive.delete(legacy);
    },
  };
  const create = () => createTraceLane({ mcHome: home, log: makeTestLogger("resident"), agentsView: client,
    schedule: setTimeout, cancel: handle => clearTimeout(handle as Timer), now: () => now, credentials: () => secrets,
    runMc: async argv => { commands.push(argv); return { exitCode: 0, stdout: JSON.stringify({ sessions: [row] }), stderr: "" }; },
  });
  const status = async () => JSON.parse(await readFile(join(paths.inspection, row.id, "status.json"), "utf8"));
  return { home, row, source, writer, destination, inspection, create, status, imports, retired, archive, paths, commands,
    restartViewer: () => { pid++; archive.clear(); },
    advance: (ms: number) => { now += ms; }, setSecrets: (values: string[]) => { secrets = values; }, failImport: () => { refuse = true; } };
}

test("pipeline, Collie, and setup executions publish native copies and confirm imports separately", async () => {
  for (const kind of ["run", "collie", "setup"] as const) {
    const f = await fixture(kind);
    const lane = f.create();
    try {
      await lane.copyPass();
      expect(await f.status()).toMatchObject({ format: "opencode", native_session_ref: "ses_main", copy: "ready", import: "pending" });
      expect((await stat(f.inspection)).ino).toBe((await stat(f.destination)).ino);
      await lane.reconcilePass();
      expect(await f.status()).toMatchObject({ copy: "ready", import: "current" });
      expect(f.archive.get("opencode:ses_main")?.displayName).toBe(f.row.display_name);
      expect(f.imports).toEqual(["opencode:ses_main"]);
      f.restartViewer();
      await lane.reconcilePass();
      expect(f.archive.has("opencode:ses_main")).toBe(true);
      expect(f.imports).toHaveLength(2);
      expect(f.commands.every(args => args[0] === "__trace-sessions")).toBe(true);
    } finally { await lane.stop(); f.writer.close(); }
  }
});

test("native record updates and credential changes require a new confirmed import", async () => {
  const f = await fixture(); const lane = f.create();
  try {
    await lane.copyPass(); await lane.reconcilePass();
    f.writer.query("UPDATE session_message SET data=replace(data,?,?) WHERE id='msg_reply'").run("A native reply", "native-key-abc");
    await lane.copyPass();
    expect((await f.status()).import).toBe("pending");
    await lane.reconcilePass();
    f.setSecrets(["native-key-abc"]);
    await lane.copyPass();
    expect((await f.status()).import).toBe("pending");
    await lane.reconcilePass();
    expect(f.imports).toHaveLength(3);
    expect((await readFile(f.inspection)).includes(Buffer.from("native-key-abc"))).toBe(false);
  } finally { await lane.stop(); f.writer.close(); }
});

test("active native sessions confirm child imports within the request budget", async () => {
  const f = await fixture(); const lane = f.create();
  try {
    f.writer.exec("INSERT INTO session_v2 SELECT 'ses_child',project_id,'ses_main',title,directory,time_created,time_updated,time_idle,agent,model FROM session_v2");
    for (let pass = 0; pass < 3; pass++) {
      f.writer.query("UPDATE session_v2 SET time_updated=?").run(pass);
      await lane.copyPass(); await lane.reconcilePass();
      expect(await f.status()).toMatchObject({ copy: "ready", import: "current" });
    }
    expect(f.imports.filter(id => id === "opencode:ses_child")).toHaveLength(3);
    expect(f.imports.filter(id => id === "opencode:ses_main")).toHaveLength(3);
  } finally { await lane.stop(); f.writer.close(); }
});

test("a failed native refresh retains the last copy and a later pass restores current records", async () => {
  const f = await fixture(); let lane = f.create();
  try {
    await lane.copyPass(); await lane.reconcilePass();
    f.writer.close();
    const before = await readFile(f.inspection);
    await rename(f.source, f.source + ".held");
    await lane.copyPass();
    expect(await f.status()).toMatchObject({ copy: "delayed", import: "current", cause: expect.any(String) });
    expect(await readFile(f.inspection)).toEqual(before);
    await rename(f.source + ".held", f.source);
    f.advance(SESSIONS_REFRESH_MS);
    await lane.copyPass();
    expect((await f.status()).copy).toBe("ready");
    await lane.stop();
    await rm(f.destination);
    lane = f.create();
    await lane.copyPass(); await lane.reconcilePass();
    expect(await f.status()).toMatchObject({ copy: "ready", import: "current" });
    expect(await readFile(f.inspection)).toEqual(before);
  } finally { await lane.stop(); }
});

test("retained exports retire after native import and remain retired after a restart", async () => {
  const f = await fixture(); let lane = f.create();
  const legacy = openCodeTraceID("ses_main");
  f.row.legacy_filename = `rollout-${legacy}.jsonl`;
  await writeFile(join(f.home, f.row.session_path, f.row.legacy_filename), JSON.stringify({ type: "session_meta", payload: { id: legacy } }) + "\n");
  f.archive.set(`codex:${legacy}`, { id: `codex:${legacy}`, displayName: "Retained export", endedAt: null });
  try {
    await lane.copyPass();
    expect(f.retired).toHaveLength(0);
    expect(await Bun.file(join(f.paths.codex, f.row.legacy_filename)).exists()).toBe(true);
    await lane.reconcilePass();
    expect(f.retired).toEqual([`codex:${legacy}`]);
    expect(await Bun.file(join(f.paths.codex, f.row.legacy_filename)).exists()).toBe(false);
    await lane.stop(); lane = f.create();
    await lane.copyPass(); await lane.reconcilePass();
    expect(f.retired).toHaveLength(1);
    expect(await Bun.file(join(f.paths.codex, f.row.legacy_filename)).exists()).toBe(false);
    await lane.stop(); f.writer.close();
    await rm(f.source);
    f.restartViewer(); lane = f.create();
    await lane.copyPass(); await lane.reconcilePass();
    expect(await f.status()).toMatchObject({ copy: "delayed", import: "current" });
    expect(f.archive.has("opencode:ses_main")).toBe(true);
    expect(f.retired).toHaveLength(1);
    expect(await Bun.file(join(f.paths.codex, f.row.legacy_filename)).exists()).toBe(false);
  } finally { await lane.stop(); f.writer.close(); }
});

test("native import failures appear in overdue health without execution commands", async () => {
  const f = await fixture(); const lane = f.create(); f.failImport();
  try {
    await lane.copyPass(); await lane.reconcilePass();
    f.advance(OVERDUE_MS + 1);
    await lane.reconcilePass();
    const health = JSON.parse(await readFile(f.paths.health, "utf8"));
    expect(health.overdue).toEqual([expect.objectContaining({ id: f.row.id, agentsview_session: "opencode:ses_main" })]);
    expect(await f.status()).toMatchObject({ import: "pending", import_cause: "The synthetic import failed." });
    expect(f.commands.every(args => args[0] === "__trace-sessions")).toBe(true);
  } finally { await lane.stop(); f.writer.close(); }
});

test("setup recovery uses its native marker and rejects an ambiguous or child identity", async () => {
  const f = await fixture("setup");
  try {
    f.writer.exec("INSERT INTO session_v2 SELECT 'ses_child',project_id,'ses_main',title,directory,time_created,time_updated,time_idle,agent,model FROM session_v2");
    expect(await recoverSetupTrace(f.home, f.row as any)).toEqual({ nativeRef: "ses_main", filename: OPENCODE_DATABASE });
    f.writer.exec("INSERT INTO session_v2 SELECT 'ses_other',project_id,NULL,title,directory,time_created,time_updated,time_idle,agent,model FROM session_v2 WHERE id='ses_main'");
    await expect(recoverSetupTrace(f.home, f.row as any)).rejects.toThrow();
    const marker = join(f.home, f.row.session_path, "opencode/native-session");
    await writeFile(marker, "ses_main");
    expect((await recoverSetupTrace(f.home, f.row as any)).nativeRef).toBe("ses_main");
    await writeFile(marker, "ses_child");
    await expect(recoverSetupTrace(f.home, f.row as any)).rejects.toThrow();
  } finally { f.writer.close(); }
});

test("an absent native setup database retains its converted evidence and reports its format", async () => {
  const f = await fixture("setup"); f.writer.close();
  await rm(f.source);
  const legacy = openCodeTraceID("ses_main");
  f.row.legacy_filename = `rollout-${legacy}.jsonl`;
  await writeFile(join(f.home, f.row.session_path, f.row.legacy_filename), JSON.stringify({ type: "session_meta",
    payload: { id: legacy, originator: "OpenCode", native_session_ref: "ses_main" } }) + "\n");
  expect(await recoverSetupTrace(f.home, f.row as any)).toEqual({ nativeRef: "ses_main", filename: f.row.legacy_filename });
  await writeFile(join(f.home, f.row.session_path, "opencode/native-session"), "ses_main");
  expect(await recoverSetupTrace(f.home, f.row as any)).toEqual({ nativeRef: "ses_main", filename: f.row.legacy_filename });
  f.archive.set(`codex:${legacy}`, { id: `codex:${legacy}`, displayName: "Retained export", endedAt: null });
  const lane = f.create();
  try {
    await lane.copyPass(); await lane.reconcilePass();
    expect(await f.status()).toMatchObject({ copy: "unavailable", import: "pending", cause: expect.any(String) });
    expect(f.archive.get(`codex:${legacy}`)?.displayName).toBe(`OpenCode export · ${f.row.display_name}`);
    expect(await Bun.file(join(f.paths.codex, f.row.legacy_filename)).exists()).toBe(true);
    expect(f.retired).toHaveLength(0);
    expect(f.imports).toHaveLength(0);
  } finally { await lane.stop(); }
});
