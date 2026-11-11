// Run with AGENTSVIEW_BIN=<pinned executable> mise exec -- bun acceptance/opencode.ts.
import { Database } from "bun:sqlite";
import { strict as assert } from "node:assert";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { nativeFixture } from "../testdata/opencode";
import { copyNativeDatabase } from "../src/opencode-copy";
import { agentsViewClient } from "../src/agentsview";
import { OPENCODE_DATABASE, openCodeTraceID } from "../../runner/shared/opencode-trace";

const binary = process.env.AGENTSVIEW_BIN;
if (!binary) throw new Error("AGENTSVIEW_BIN must name the pinned executable.");
const pin = JSON.parse(await readFile(join(import.meta.dir, "../../mc/deployment/agentsview.json"), "utf8"));
assert.equal(createHash("sha256").update(await readFile(binary)).digest("hex"), pin.executable.sha256);
const root = await mkdtemp(join(tmpdir(), "culler-native-acceptance-"));
let server: ReturnType<typeof Bun.spawn> | undefined;
let db: Database | undefined;
try {
  const source = join(root, "source");
  const copies = join(root, "copies");
  const archive = join(root, "archive");
  const home = join(root, "home");
  const codex = join(root, "codex");
  for (const folder of [copies, archive, home, codex]) await mkdir(folder, { recursive: true });
  const credential = "synthetic-provider-credential-acceptance";
  const temporary = "culler_proxy_" + "T".repeat(43);
  db = nativeFixture(join(source, OPENCODE_DATABASE), `Native answer ${credential} ${temporary}`);
  db.exec("INSERT INTO session_v2 SELECT 'ses_child',project_id,'ses_main','Child session',directory,time_created,time_updated,time_idle,agent,model FROM session_v2");
  db.exec("INSERT INTO session_message SELECT 'msg_child','ses_child',type,seq,time_created,time_updated,data FROM session_message WHERE id='msg_reply'");
  db.query("INSERT INTO credential VALUES (?,?)").run("account", credential);
  const destination = join(copies, "opencode-fixture.db");
  await copyNativeDatabase(source, destination, "ses_main", [credential]);
  const legacyID = openCodeTraceID("ses_main");
  await writeFile(join(codex, `rollout-${legacyID}.jsonl`), [
    { timestamp: "2023-11-14T22:13:20Z", type: "session_meta", payload: { id: legacyID, cwd: "/workspace/source", originator: "OpenCode", source: "exec", timestamp: "2023-11-14T22:13:20Z" } },
    { timestamp: "2023-11-14T22:13:21Z", type: "response_item", payload: { type: "message", role: "user", content: [{ type: "input_text", text: "Inspect the task." }] } },
  ].map(row => JSON.stringify(row)).join("\n") + "\n");
  const token = "a".repeat(64);
  await writeFile(join(archive, "config.toml"), `require_auth = true\nauth_token = "${token}"\ndisable_update_check = true\narchive_content = "full"\nresult_content_blocked_categories = []\ntool_result_images = "keep"\ndisabled_agents = ${JSON.stringify(pin.disabled_agents)}\n[agents.opencode]\ndirs = [${JSON.stringify(copies)}]\n[agents.codex]\ndirs = [${JSON.stringify(codex)}]\n[agents.claude]\ndirs = [${JSON.stringify(join(home, "absent"))}]\n`);
  const reservation = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch: () => new Response() });
  const port = reservation.port!;
  reservation.stop(true);
  const bind = `127.0.0.1:${port}`;
  server = Bun.spawn([binary, "serve", "--host", "127.0.0.1", "--port", String(port), "--no-browser", "--no-update-check"], {
    env: { ...process.env, HOME: home, AGENTSVIEW_DATA_DIR: archive, AGENTSVIEW_NO_DAEMON: "1", AGENTSVIEW_DISABLE_UPDATE_CHECK: "1", AGENTSVIEW_TELEMETRY_ENABLED: "0", OPENCODE_DIR: copies },
    stdout: "pipe", stderr: "pipe",
  });
  const output = new Response(server.stdout).text();
  const errors = new Response(server.stderr).text();
  const signal = AbortSignal.timeout(60_000);
  const client = agentsViewClient(bind, { token: async () => token, timeoutMs: 10_000 });
  let ready = false;
  for (let attempt = 0; attempt < 200; attempt++) {
    if (server.exitCode !== null) throw new Error(`AgentsView stopped: ${await output}\n${await errors}`);
    try { await client.ping(signal); ready = true; break; } catch { await Bun.sleep(100); }
  }
  assert(ready, "AgentsView must start");
  const request = async (path: string, method = "GET", body?: unknown) => {
    const response = await fetch(`http://${bind}/api/v1${path}`, { method, signal,
      headers: { authorization: `Bearer ${token}`, origin: `http://${bind}`, "content-type": "application/json" },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
    assert(response.ok, `${method} ${path}: ${response.status} ${response.ok ? "" : await response.text()}`);
    return response.status === 204 ? null : response.json();
  };
  await client.ensureProjectRules(signal);
  await client.syncNative("opencode:ses_main", signal);
  await client.syncNative("opencode:ses_child", signal);
  const session = await request("/sessions/opencode:ses_main");
  assert.equal(session.agent, "opencode");
  assert.equal(session.project, "Culler");
  const messages = await request("/sessions/opencode:ses_main/messages");
  const content = JSON.stringify(messages);
  assert(content.includes("Native answer"));
  assert(content.includes("native tool output"));
  assert(content.includes("fixture-model"));
  assert(!content.includes(credential) && !content.includes(temporary));
  const child = await request("/sessions/opencode:ses_child");
  assert.equal(child.parent_session_id, "opencode:ses_main");
  console.log("PASS native import, content, model, child identity, Project, and credential masks");
  db.query("UPDATE session_message SET data=replace(data,?,?) WHERE id='msg_reply'").run("Native answer", "Changed reply");
  await copyNativeDatabase(source, destination, "ses_main", ["rotated-synthetic-credential"]);
  await client.syncNative("opencode:ses_main", signal);
  const changed = JSON.stringify(await request("/sessions/opencode:ses_main/messages"));
  assert(changed.includes("Changed reply") && !changed.includes(credential));
  console.log("PASS native record update with unchanged timestamps and prior credential masks");
  await request(`/sessions/codex:${legacyID}/star`, "PUT");
  const legacyMessages = await request(`/sessions/codex:${legacyID}/messages`);
  assert(legacyMessages.messages.length > 0);
  await request(`/sessions/codex:${legacyID}/messages/${legacyMessages.messages[0].id}/pin`, "POST", { note: "Retain this operator note." });
  await client.retireExport(`codex:${legacyID}`, "opencode:ses_main", signal);
  assert(JSON.stringify(await request("/trash")).includes(legacyID));
  const pins = await request(`/sessions/codex:${legacyID}/pins`);
  assert(JSON.stringify(pins).includes("Retain this operator note."));
  assert(JSON.stringify(await request("/starred")).includes(legacyID));
  console.log("PASS export retirement with retained operator annotations");
  server.kill("SIGTERM");
  await server.exited;
  server = undefined;
} finally {
  db?.close();
  if (server) { server.kill("SIGTERM"); await server.exited; }
  await rm(root, { recursive: true, force: true });
}
