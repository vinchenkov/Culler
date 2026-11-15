// Run with NATIVE_TEST_IMAGE=<runtime image> mise exec -- bun acceptance/opencode-container.ts.
import { strict as assert } from "node:assert";
import { Database } from "bun:sqlite";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { nativeFixture } from "../testdata/opencode";
import { copyNativeDatabase } from "../src/opencode-copy";
import { OPENCODE_DATABASE } from "../../runner/shared/opencode-trace";

const image = process.env.NATIVE_TEST_IMAGE;
if (!image) throw new Error("NATIVE_TEST_IMAGE must name an available Culler runtime image.");
const root = await mkdtemp(join(tmpdir(), "culler-native-container-"));
const container = `culler-native-trace-${process.pid}`;
let processHandle: ReturnType<typeof Bun.spawn> | undefined;
try {
  const session = join(root, "session");
  nativeFixture(join(session, OPENCODE_DATABASE)).close();
  await writeFile(join(root, "writer.ts"), `import { Database } from "bun:sqlite";
const db = new Database('/fixture/session/${OPENCODE_DATABASE}');
db.exec('PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0');
db.query("UPDATE session_message SET data=replace(data,?,?) WHERE id='msg_reply'").run('A native reply','Committed text');
await Bun.write('/fixture/ready','ready');
db.exec('BEGIN');
db.query("UPDATE session_message SET data=replace(data,?,?) WHERE id='msg_reply'").run('Committed text','Incomplete text');
while (!await Bun.file('/fixture/finish').exists()) await Bun.sleep(20);
db.exec('COMMIT');
db.close();
`);
  processHandle = Bun.spawn(["docker", "run", "--rm", "--name", container, "--entrypoint", "/opt/culler-runtime/bin/bun",
    "--mount", `type=bind,src=${root},dst=/fixture`, image, "/fixture/writer.ts"], { stdout: "pipe", stderr: "pipe" });
  const output = new Response(processHandle.stdout).text();
  const errors = new Response(processHandle.stderr).text();
  let ready = false;
  for (let attempt = 0; attempt < 500; attempt++) {
    if (processHandle.exitCode !== null) throw new Error(`Container stopped: ${await output}\n${await errors}`);
    if (await Bun.file(join(root, "ready")).exists()) { ready = true; break; }
    await Bun.sleep(20);
  }
  assert(ready, "The container must expose committed WAL records.");
  const destination = join(root, "copies/opencode-fixture.db");
  for (let pass = 0; pass < 4; pass++) {
    await copyNativeDatabase(session, destination, "ses_main", []);
    const copy = new Database(destination, { readonly: true });
    const { data } = copy.query("SELECT data FROM session_message WHERE id='msg_reply'").get() as { data: string };
    assert(data.includes("Committed text") && !data.includes("Incomplete text"));
    assert.deepEqual(copy.query("PRAGMA integrity_check").get(), { integrity_check: "ok" });
    copy.close();
    assert.equal(processHandle.exitCode, null);
  }
  console.log("PASS host copies committed container WAL records while an incomplete transaction remains active");
  await writeFile(join(root, "finish"), "finish");
  assert.equal(await processHandle.exited, 0, await errors);
  processHandle = undefined;
  await copyNativeDatabase(session, destination, "ses_main", []);
  assert((await readFile(destination)).includes(Buffer.from("Incomplete text")));
  console.log("PASS final committed container records and consistent database copies");
} finally {
  if (processHandle) {
    const stop = Bun.spawn(["docker", "rm", "-f", container], { stdout: "ignore", stderr: "ignore" });
    await stop.exited;
    await processHandle.exited;
  }
  await rm(root, { recursive: true, force: true });
}
