import { afterEach, expect, test } from "bun:test";
import { Database } from "bun:sqlite";
import { mkdtemp, readFile, readdir, rm, stat, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { nativeFixture } from "../testdata/opencode";
import { copyNativeDatabase, readNativeSnapshot } from "./opencode-copy";
import { OPENCODE_DATABASE } from "../../runner/shared/opencode-trace";
import { nativeMask } from "./opencode-snapshot";

const roots: string[] = [];
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });
async function fixture(text?: string) {
  const root = await mkdtemp(join(tmpdir(), "culler-native-copy-")); roots.push(root);
  const source = join(root, "session", OPENCODE_DATABASE);
  const db = nativeFixture(source, text);
  return { root, source, db, session: join(root, "session"), destination: join(root, "copies/opencode-run.db") };
}

test("native copies include committed WAL records and exclude an incomplete transaction", async () => {
  const f = await fixture();
  try {
    f.db.exec("BEGIN; UPDATE session_message SET data='uncommitted' WHERE id='msg_reply'");
    const result = await copyNativeDatabase(f.session, f.destination, "ses_main", []);
    const copy = new Database(f.destination, { readonly: true });
    try {
      expect(copy.query("SELECT data FROM session_message WHERE id='msg_reply'").get()).toEqual({ data: expect.stringContaining("A native reply") });
      expect(copy.query("PRAGMA integrity_check").get()).toEqual({ integrity_check: "ok" });
      expect(result.sessions).toEqual(["ses_main"]);
    } finally { copy.close(); }
    f.db.exec("ROLLBACK");
    const size = (await stat(f.source)).size;
    f.db.query("UPDATE session_message SET data=replace(data,?,?) WHERE id='msg_reply'").run("A native reply", "A changed text");
    expect((await stat(f.source)).size).toBe(size);
    const updated = await copyNativeDatabase(f.session, f.destination, "ses_main", []);
    expect(updated.changed).toBe(true);
    expect(updated.digest).not.toBe(result.digest);
    expect(Buffer.from(updated.bytes).includes(Buffer.from("A changed text"))).toBe(true);
  } finally { f.db.close(); }
});

test("native copies mask credentials and omit account data without source changes", async () => {
  const secret = 'synthetic-provider-credential-123';
  const f = await fixture(`before ${secret} after`);
  f.db.query("INSERT INTO credential VALUES (?,?)").run("account", "account-secret-outside-transcript");
  f.db.close();
  const entries = await readdir(join(f.session, "opencode/data/opencode"));
  const original = await readFile(f.source);
  const result = await copyNativeDatabase(f.session, f.destination, "ses_main", [secret]);
  expect(await readFile(f.source)).toEqual(original);
  expect(await readdir(join(f.session, "opencode/data/opencode"))).toEqual(entries);
  expect(Buffer.from(result.bytes).includes(Buffer.from(secret))).toBe(false);
  expect(Buffer.from(result.bytes).includes(Buffer.from("account-secret-outside-transcript"))).toBe(false);
  const copy = new Database(f.destination, { readonly: true });
  try {
    const { data } = copy.query("SELECT data FROM session_message WHERE id='msg_reply'").get() as { data: string };
    expect(JSON.parse(data).content[0].text).toBe(`before ${"*".repeat(secret.length)} after`);
    expect(JSON.parse(data).content[1].id).toBe("tool_native");
    expect(copy.query("SELECT name FROM sqlite_schema WHERE name='credential'").get()).toBeNull();
  } finally { copy.close(); }
  const resumed = new Database(f.source);
  resumed.query("UPDATE session_message SET data=replace(data,?,?) WHERE id='msg_reply'").run("before ", "moved before ");
  resumed.close();
  const rotated = await copyNativeDatabase(f.session, f.destination, "ses_main", ["new-synthetic-credential"]);
  expect(Buffer.from(rotated.bytes).includes(Buffer.from(secret))).toBe(false);
  expect(await readFile(f.destination + ".masks.json", "utf8")).not.toContain(secret);
});

test("native copies preserve child identities and refuse a child as the main session", async () => {
  const f = await fixture();
  try {
    f.db.exec("INSERT INTO session_v2 SELECT 'ses_child',project_id,'ses_main',title,directory,time_created,time_updated,time_idle,agent,model FROM session_v2");
    expect((await readNativeSnapshot(f.session)).main).toBe("ses_main");
    const result = await copyNativeDatabase(f.session, f.destination, "ses_main", []);
    expect(result.sessions.sort()).toEqual(["ses_child", "ses_main"]);
    await expect(readNativeSnapshot(f.session, "ses_child")).rejects.toThrow();
  } finally { f.db.close(); }
});

test("native masks preserve JSON content and prior credentials with literal mask characters", () => {
  const secret = "synthetic*credential";
  const token = "culler_proxy_" + "a".repeat(43);
  const original = { [secret]: secret, stable: "synthetic and credential remain separate words",
    attachment: { mime: "text/plain", data: Buffer.from(`before ${secret} ${token} after`).toString("base64") } };
  const first = nativeMask([secret], []);
  const masked = JSON.parse(first.value(JSON.stringify(original)));
  expect(masked["*".repeat(secret.length)]).toBe("*".repeat(secret.length));
  expect(masked.stable).toBe(original.stable);
  expect(Buffer.from(masked.attachment.data, "base64").toString()).toBe(`before ${"*".repeat(secret.length)} ${"*".repeat(token.length)} after`);
  const restored = nativeMask([], JSON.parse(JSON.stringify(first.signatures())));
  expect(JSON.parse(restored.value(JSON.stringify(original)))).toEqual(masked);
});

test("an interrupted native refresh preserves the last complete copy", async () => {
  const f = await fixture();
  try {
    await copyNativeDatabase(f.session, f.destination, "ses_main", []);
    const before = await readFile(f.destination);
    f.db.query("UPDATE session_message SET data=replace(data,?,?) WHERE id='msg_reply'").run("A native reply", "A changed text");
    const abort = new AbortController(); abort.abort();
    await expect(copyNativeDatabase(f.session, f.destination, "ses_main", [], abort.signal)).rejects.toThrow("stopped");
    expect(await readFile(f.destination)).toEqual(before);
    await copyNativeDatabase(f.session, f.destination, "ses_main", []);
    expect((await readFile(f.destination)).includes(Buffer.from("A changed text"))).toBe(true);
  } finally { f.db.close(); }
});

test("an unavailable source preserves the last complete database and rejects links", async () => {
  const f = await fixture(); f.db.close();
  await copyNativeDatabase(f.session, f.destination, "ses_main", []);
  const before = await readFile(f.destination);
  await rm(f.source);
  await expect(copyNativeDatabase(f.session, f.destination, "ses_main", [])).rejects.toThrow();
  expect(await readFile(f.destination)).toEqual(before);
  await symlink(f.destination, f.source);
  await expect(copyNativeDatabase(f.session, f.destination, "ses_main", [])).rejects.toThrow();
  expect(await readFile(f.destination)).toEqual(before);
});

test("a missing WAL auxiliary file creates no file in the original store", async () => {
  const f = await fixture();
  const original = await readFile(f.source), wal = await readFile(f.source + "-wal");
  f.db.close();
  await rm(f.source + "-shm", { force: true });
  await writeFile(f.source, original); await writeFile(f.source + "-wal", wal);
  const directory = join(f.session, "opencode/data/opencode");
  const entries = await readdir(directory);
  await expect(copyNativeDatabase(f.session, f.destination, "ses_main", [])).rejects.toThrow("incomplete WAL auxiliary files");
  expect(await readdir(directory)).toEqual(entries);
  expect(await readFile(f.source)).toEqual(original);
  expect(await readFile(f.source + "-wal")).toEqual(wal);
});
