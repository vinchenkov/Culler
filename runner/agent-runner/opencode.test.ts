import { afterEach, expect, test } from "bun:test";
import { Database } from "bun:sqlite";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { openCodeConfiguration, openCodeEnvironment, openCodeEvent, redactOpenCodeSession } from "./adapters/opencode";
import { parseProviderAccess, type ProviderAccess } from "./adapters/provider-access";
import control, { cleanShellEnvironment, waitForOpenWiki } from "../opencode/control/server.mjs";

const roots: string[] = [];
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });
const token = "culler_proxy_" + "a".repeat(43);
const access: ProviderAccess = { version: 1, url: "https://host.docker.internal:4000/v1", token, ca: "-----BEGIN CERTIFICATE-----\nfixture", harness: "opencode", authMode: "api-key",
  opencode: { provider: "fixture", keyVariable: "FIXTURE_API_KEY", webSearch: true, model: { id: "fixture-model", address: "https://provider.invalid/v1", package: "@opencode/ai/providers/openai-compatible", variants: ["low", "high", "max"], key_placement: "bearer", model_location: "body" } } };

test("OpenCode receives the model variant, local plugins, role skills, and OpenWiki", () => {
  const config = openCodeConfiguration(access, "fixture-model", "high", "/home/agent/.agents/skills", "/opt/culler-opencode", { workspace: "/workspace/source" });
  expect(config.model).toBe("fixture/fixture-model#high");
  expect(config.agents.build.model).toBe(config.model);
  expect(config.agents.compaction.model).toBe(config.model);
  expect(config.agents.title.disabled).toBe(true);
  expect(config.agents.summary.disabled).toBe(true);
  expect(config.plugins.every(path => path.startsWith("file:///opt/culler-opencode/"))).toBe(true);
  expect(config.skills).toEqual(["/home/agent/.agents/skills"]);
  expect(config.mcp!.servers.openwiki.type).toBe("local");
  expect(openCodeConfiguration(access, "fixture-model", undefined, "/skills").model).toBe("fixture/fixture-model");
  expect(() => openCodeConfiguration(access, "fixture-model", "thinking", "/skills")).toThrow("invalid");
  const unavailable = structuredClone(access); unavailable.opencode!.webSearch = false;
  expect(openCodeConfiguration(unavailable, "fixture-model", undefined, "/skills").websearch).toBe(false);
});

test("the shell environment removes every token and the native configuration", () => {
  const env = openCodeEnvironment({ HOME: "/home/agent", OPENCODE_CONFIG: "/project/opencode.json", OPENCODE_CONFIG_CONTENT: "malicious", HTTP_PROXY: "http://outside.invalid", OTHER_API_KEY: "real-secret", MC_PROVIDER_ACCESS_FILE: "/mc/access.json", PATH: "/bin" }, { token }, "/mc/session/opencode", "/mc/ca");
  expect(env.OPENCODE_CONFIG).toBeUndefined();
  expect(env.OPENCODE_DISABLE_PROJECT_CONFIG).toBe("1");
  expect(env.OPENCODE_DISABLE_MODELS_FETCH).toBe("1");
  expect(env.XDG_DATA_HOME).toBe("/mc/session/opencode/data");
  expect(env.HOME).toBe("/home/agent");
  const shell = cleanShellEnvironment({ ...env, FIXTURE_API_KEY: token, ARBITRARY: `prefix ${token}`, MC_PROVIDER_ACCESS_FILE: "/mc/access.json" });
  expect(JSON.stringify(shell)).not.toContain(token);
  expect(shell.OPENCODE_CONFIG_CONTENT).toBeUndefined();
  expect(shell.MC_PROVIDER_ACCESS_FILE).toBeUndefined();
  expect(shell.PATH).toBe("/bin");
});

test("OpenWiki waits for its tools and rejects a failed or incomplete server", async () => {
  let polls = 0;
  const ctx = {
    mcp: { list: async () => ({ data: [{ name: "openwiki", status: { status: "connected" } }] }) },
    tool: { list: async () => ++polls < 3 ? [] : [{ name: "openwiki_begin", options: { namespace: "openwiki" } }] },
  };
  await waitForOpenWiki(ctx, ["openwiki_begin"], 100, 1);
  expect(polls).toBe(3);
  await expect(waitForOpenWiki(ctx, ["openwiki_finish"], 10, 1)).rejects.toThrow("startup deadline");
  ctx.mcp.list = async () => ({ data: [{ name: "openwiki", status: { status: "failed" } }] });
  await expect(waitForOpenWiki(ctx, ["openwiki_begin"], 100, 1)).rejects.toThrow("OpenWiki failed");
});

test("the control plugin removes questions and unavailable web search", async () => {
  const previous = process.env.CULLER_OPENCODE_WEBSEARCH;
  try {
    for (const available of ["0", "1"]) {
      process.env.CULLER_OPENCODE_WEBSEARCH = available;
      const removed: string[] = [];
      await control.setup({ shell: { hook: async () => {} }, tool: { transform: async change => change({ remove: name => removed.push(name) }) } });
      expect(removed).toEqual(available === "1" ? ["question"] : ["question", "websearch"]);
    }
  } finally {
    if (previous === undefined) delete process.env.CULLER_OPENCODE_WEBSEARCH;
    else process.env.CULLER_OPENCODE_WEBSEARCH = previous;
  }
});

test("OpenCode access rejects an account credential or another base path", () => {
  expect(parseProviderAccess(JSON.stringify(access), "opencode").opencode!.provider).toBe("fixture");
  expect(() => parseProviderAccess(JSON.stringify({ ...access, authMode: "account" }), "opencode")).toThrow();
  expect(() => parseProviderAccess(JSON.stringify({ ...access, url: "https://host.docker.internal:4000/other" }), "opencode")).toThrow();
  expect(openCodeEvent({ type: "error", sessionID: "ses_1", error: { data: { message: "refused" } } })).toMatchObject({ failure: "refused", session: "ses_1" });
});

test("the prompt hook retains the main session identity before a model request", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-opencode-session-")); roots.push(root);
  const file = join(root, "native-session");
  const previous = process.env.CULLER_OPENCODE_SESSION_FILE;
  process.env.CULLER_OPENCODE_SESSION_FILE = file;
  try {
    let prompt: (input: { sessionID: string }) => Promise<void>;
    await control.setup({
      shell: { hook: async () => {} }, tool: { transform: async () => {} },
      session: { hook: async (name, callback) => { expect(name).toBe("prompt"); prompt = callback; } },
    });
    await prompt!({ sessionID: "ses_main" });
    expect(await readFile(file, "utf8")).toBe("ses_main");
    await prompt!({ sessionID: "ses_child" });
    expect(await readFile(file, "utf8")).toBe("ses_main");
  } finally {
    if (previous === undefined) delete process.env.CULLER_OPENCODE_SESSION_FILE;
    else process.env.CULLER_OPENCODE_SESSION_FILE = previous;
  }
});

test("the adapter registers a session while the first model response remains incomplete", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-opencode-start-")); roots.push(root);
  await mkdir(join(root, "session")); await mkdir(join(root, "workspace"));
  await writeFile(join(root, "access.json"), JSON.stringify(access));
  const native = join(root, "native"), release = join(root, "release");
  await writeFile(native, `#!${process.execPath}\nawait Bun.stdin.text();
await Bun.write(process.env.CULLER_OPENCODE_SESSION_FILE, "ses_delayed");
while (!await Bun.file(process.env.FIXTURE_RELEASE).exists()) await Bun.sleep(10);
console.log(JSON.stringify({type:"text",sessionID:"ses_delayed",part:{text:"Complete"}}));
console.log(JSON.stringify({type:"step_finish",sessionID:"ses_delayed",part:{reason:"stop"}}));\n`, { mode: 0o755 });
  const child = Bun.spawn([process.execPath, join(import.meta.dir, "adapters/opencode.ts"), "--model", "fixture-model", "--workspace", join(root, "workspace"), "--session-dir", join(root, "session")], {
    env: { ...process.env, MC_PROVIDER_ACCESS_FILE: join(root, "access.json"), MC_OPENCODE_BIN: native, FIXTURE_RELEASE: release },
    stdin: new TextEncoder().encode("Role directive and run brief"), stdout: "pipe", stderr: "pipe",
  });
  const timer = setTimeout(() => child.kill(), 3000);
  const events: any[] = [];
  try {
    let buffer = "";
    for await (const chunk of child.stdout) {
      buffer += new TextDecoder().decode(chunk);
      for (let at = buffer.indexOf("\n"); at >= 0; at = buffer.indexOf("\n")) {
        const event = JSON.parse(buffer.slice(0, at)); buffer = buffer.slice(at + 1);
        events.push(event);
        if (event.event === "session-start") {
          expect(await Bun.file(release).exists()).toBe(false);
          expect(child.exitCode).toBeNull();
          await writeFile(release, "continue");
        }
      }
    }
    expect(await child.exited, await new Response(child.stderr).text()).toBe(0);
    expect(events[0]).toMatchObject({ event: "session-start", session_id: "ses_delayed" });
    expect(events.filter(event => event.event === "session-start")).toHaveLength(1);
    expect(events.at(-1)).toMatchObject({ event: "turn-complete", status: "success" });
  } finally {
    clearTimeout(timer);
    if (child.exitCode === null) { child.kill(); await child.exited; }
  }
});

test("the native database keeps session rows and removes retained tokens", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-opencode-test-")); roots.push(root);
  const path = join(root, "native.db");
  const db = new Database(path); db.exec("PRAGMA journal_mode=WAL; CREATE TABLE session(id TEXT PRIMARY KEY, message TEXT)");
  db.query("INSERT INTO session VALUES (?,?)").run("session-1", JSON.stringify({ text: token })); db.close();
  await writeFile(join(root, "native.log"), token);
  await redactOpenCodeSession(root);
  const checked = new Database(path);
  expect(checked.query("PRAGMA integrity_check").get()).toEqual({ integrity_check: "ok" });
  expect(checked.query("SELECT id FROM session").get()).toEqual({ id: "session-1" }); checked.close();
  expect((await readFile(path)).includes(Buffer.from(token))).toBe(false);
  expect(await readFile(join(root, "native.log"), "utf8")).not.toContain(token);
});

test("the adapter preserves JSON events, durable input, completion, and session continuation", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-opencode-adapter-")); roots.push(root);
  await mkdir(join(root, "session")); await mkdir(join(root, "workspace"));
  await writeFile(join(root, "access.json"), JSON.stringify(access));
  const native = join(root, "native");
  await writeFile(native, `#!${process.execPath}\nconst prompt=await Bun.stdin.text(); await Bun.write(process.env.FIXTURE_OUTPUT,JSON.stringify({prompt,args:process.argv,config:JSON.parse(process.env.OPENCODE_CONFIG_CONTENT)}));\nfor(const event of [{type:'step_start',sessionID:'ses_fixture',part:{}},{type:'text',sessionID:'ses_fixture',part:{text:'A reply with tokens: 4'}},{type:'step_finish',sessionID:'ses_fixture',part:{reason:'stop'}}]) console.log(JSON.stringify(event));\n`, { mode: 0o755 });
  for (const resume of [false, true]) {
    const child = Bun.spawn([process.execPath, join(import.meta.dir, "adapters/opencode.ts"), "--model", "fixture-model", "--effort", "high", "--workspace", join(root,"workspace"), "--session-dir", join(root,"session"), ...(resume ? ["--native-ref", "ses_fixture"] : [])], {
      env: { ...process.env, MC_PROVIDER_ACCESS_FILE: join(root,"access.json"), MC_OPENCODE_BIN: native, FIXTURE_OUTPUT: join(root,"invocation.json") }, stdin: new TextEncoder().encode(resume ? "Second input" : "Role directive and run brief"), stdout: "pipe", stderr: "pipe" });
    const output = await new Response(child.stdout).text(); const error = await new Response(child.stderr).text();
    expect(await child.exited, error+output).toBe(0);
    const events = output.trim().split("\n").map(line => JSON.parse(line));
    expect(events.at(-1)).toMatchObject({ event: "turn-complete", status: "success" });
    expect(output).not.toContain(token);
    const invocation = JSON.parse(await readFile(join(root,"invocation.json"),"utf8"));
    expect(invocation.prompt).toBe(resume ? "Second input" : "Role directive and run brief");
    expect(invocation.args.includes("--session")).toBe(resume);
  }
  const trace = (await readFile(join(root,"session/rollout-7e556d01-59fc-8ae6-b393-64609e645994.jsonl"),"utf8")).trim().split("\n").map(line => JSON.parse(line));
  expect(trace.filter(line => line.type === "session_meta")).toHaveLength(1);
  expect(trace.filter(line => line.payload.role === "user")).toHaveLength(2);
});
