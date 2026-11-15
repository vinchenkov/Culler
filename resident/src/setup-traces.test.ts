import { afterEach, expect, test } from "bun:test";
import { appendFile, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { makeTestLogger } from "../../runner/shared/log";
import { createTraceLane, SESSIONS_REFRESH_MS, traceCopyPaths } from "./trace-lane";
import { nativeTraceIdentity, recoverSetupTrace, SETUP_RECOVERY_PER_PASS, setupTraceCandidates, type SetupTraceCandidate } from "./setup-traces";

test("OpenCode trace recovery retains the native session identity", () => {
  expect(nativeTraceIdentity("codex", JSON.stringify({ type: "session_meta", payload: { id: "7e556d01-59fc-8ae6-b393-64609e645994", native_session_ref: "ses_fixture", originator: "OpenCode" } }) + "\n")).toBe("ses_fixture");
});

const homes: string[] = [];
afterEach(async () => { await Promise.all(homes.splice(0).map(home => rm(home, { recursive: true, force: true }))); });
async function fixture(format: "claude" | "codex" = "claude", id = "setup-one") {
  const home = await mkdtemp(join(tmpdir(), "setup-trace-")); homes.push(home);
  const row: SetupTraceCandidate = { kind: "setup", id, session_path: `environment-runs/${id}/session`, format,
    display_name: `Project · setup · ${id}`, ended: false };
  const filename = format === "claude" ? "native.jsonl" : "2026/10/12/rollout-native-one.jsonl";
  const source = join(home, row.session_path, filename);
  const line = format === "claude" ? '{"type":"user","sessionId":"native-one","message":{"role":"user","content":"Fixture"}}\n'
    : '{"type":"session_meta","payload":{"id":"native-one","cwd":"/workspace/source","originator":"codex_exec"}}\n';
  await mkdir(join(source, ".."), { recursive: true }); await writeFile(source, line);
  return { home, row, source, line, filename };
}

test("setup trace recovery supports both native formats without path inference from dashboard input", async () => {
  for (const format of ["claude", "codex"] as const) {
    const f = await fixture(format);
    expect(await recoverSetupTrace(f.home, f.row)).toEqual({ nativeRef: "native-one", filename: f.filename });
    expect(setupTraceCandidates([{ ...f.row, session_path: "../../private" }, { ...f.row, id: "../other" }, f.row])).toEqual([f.row]);
  }
});

test("setup trace recovery rejects ambiguous, incomplete, and invalid native identities", async () => {
  const f = await fixture();
  await appendFile(f.source, '{"type":"user","sessionId":"other"}\n');
  await expect(recoverSetupTrace(f.home, f.row)).rejects.toThrow("conflicting");
  await writeFile(f.source, '{"sessionId":"native-one"}');
  await expect(recoverSetupTrace(f.home, f.row)).rejects.toThrow("complete identity");
  await writeFile(f.source, 'invalid\n');
  await expect(recoverSetupTrace(f.home, f.row)).rejects.toThrow("invalid JSON");
  const c = await fixture("codex");
  await writeFile(join(c.home,c.row.session_path,"rollout-other.jsonl"), c.line);
  await expect(recoverSetupTrace(c.home,c.row)).rejects.toThrow("ambiguous");
});

test("setup trace recovery refuses symbolic links in operation folders and native files", async () => {
  const f = await fixture();
  const outside = join(f.home,"outside.jsonl");
  await writeFile(outside,f.line); await rm(f.source); await symlink(outside,f.source);
  await expect(recoverSetupTrace(f.home,f.row)).rejects.toThrow();
  await rm(join(f.home,f.row.session_path),{recursive:true});
  await mkdir(join(f.home,"outside")); await symlink(join(f.home,"outside"),join(f.home,f.row.session_path));
  await expect(recoverSetupTrace(f.home,f.row)).rejects.toThrow("regular folder");
});

test("the ordinary trace lane recovers finished setup traces and restores lost copies after restart", async () => {
  for (const format of ["claude", "codex"] as const) {
    const f = await fixture(format); f.row.ended = true;
    let registered: any;
    const log = makeTestLogger("resident");
    let now = Date.now();
    const runMc = async (argv: string[]) => {
      if (argv[0] === "__trace-register-setup") {
        registered = { ...f.row, native_session_ref: argv[2], trace_filename: argv[3],
          agentsview_session: format === "codex" ? `codex:${argv[2]}` : argv[2] };
        return { exitCode: 0, stdout: "{}", stderr: "" };
      }
      return { exitCode: 0, stdout: JSON.stringify({ sessions: registered ? [registered] : [], setup_pending: registered ? [] : [f.row] }), stderr: "" };
    };
    const create = () => createTraceLane({ runMc, log, mcHome:f.home, now:()=>now, schedule:setTimeout, cancel:h=>clearTimeout(h as ReturnType<typeof setTimeout>) });
    let lane=create();
    try {
      await lane.copyPass(); await lane.copyPass();
      const paths=traceCopyPaths(f.home);
      const dest=format==="claude" ? join(paths.claude,"native-one.jsonl") : join(paths.codex,f.filename);
      expect(await readFile(dest,"utf8")).toBe(f.line);
      await appendFile(f.source,'{"type":"progress"}\npartial');
      now+=SESSIONS_REFRESH_MS;
      await lane.copyPass();
      expect(await readFile(dest,"utf8")).toBe(f.line+'{"type":"progress"}\n');
      await lane.stop(); await rm(join(f.home,"agentsview"),{recursive:true}); lane=create();
      await lane.copyPass(); await lane.copyPass();
      expect(await readFile(dest,"utf8")).toBe(f.line+'{"type":"progress"}\n');
      expect(await readFile(f.source,"utf8")).toBe(f.line+'{"type":"progress"}\npartial');
    } finally { await lane.stop(); }
  }
});

test("setup recovery bounds each pass and retries temporary registration failures", async () => {
  const f=await fixture();
  const rows=Array.from({length:SETUP_RECOVERY_PER_PASS+1},(_,index)=>({...f.row,id:`setup-${index}`,session_path:`environment-runs/setup-${index}/session`}));
  for (const row of rows) {
    await mkdir(join(f.home,row.session_path),{recursive:true}); await writeFile(join(f.home,row.session_path,"native.jsonl"),f.line);
  }
  const calls:string[]=[]; let now=Date.now();
  const lane=createTraceLane({mcHome:f.home,log:makeTestLogger("resident"),now:()=>now,schedule:setTimeout,cancel:h=>clearTimeout(h as ReturnType<typeof setTimeout>),
    runMc:async argv=> {
      if(argv[0]==="__trace-register-setup") { calls.push(argv[1]!); return {exitCode:2,stdout:"",stderr:"temporary"}; }
      return {exitCode:0,stdout:JSON.stringify({sessions:[],setup_pending:rows}),stderr:""};
    }});
  try {
    await lane.copyPass(); expect(calls).toHaveLength(SETUP_RECOVERY_PER_PASS);
    now+=SESSIONS_REFRESH_MS; await lane.copyPass(); expect(new Set(calls).size).toBe(rows.length);
  } finally {await lane.stop();}
});

test("Codex setup recovery selects the main rollout while retaining its child files", async () => {
  const f=await fixture("codex");
  const child=join(f.home,f.row.session_path,"rollout-child.jsonl");
  await writeFile(child,JSON.stringify({type:"session_meta",payload:{id:"child-native",source:{subagent:{thread_spawn:{parent_thread_id:"native-one"}}}}})+"\n");
  expect(await recoverSetupTrace(f.home,f.row)).toEqual({nativeRef:"native-one",filename:f.filename});
  expect(await readFile(child,"utf8")).toContain("child-native");
});

import { FakeScheduler } from "./test-helpers";
import { SESSIONS_TIMEOUT_MS } from "./trace-lane";

test("a setup registration that ignores cancellation delays neither new ordinary traces nor shutdown", async () => {
  const f=await fixture();
  const source=join(f.home,"sessions","run-one","native.jsonl");
  await mkdir(join(source,".."),{recursive:true});await writeFile(source,f.line);
  const regular={kind:"run",id:"run-one",session_path:"sessions/run-one",format:"claude",native_session_ref:"ordinary-native",trace_filename:"native.jsonl",agentsview_session:"ordinary-native",display_name:"Project · worker · run-one",ended:false};
  const scheduler=new FakeScheduler(),log=makeTestLogger("resident");
  let signal:AbortSignal|undefined;
  const lane=createTraceLane({mcHome:f.home,log,now:scheduler.clock,schedule:scheduler.schedule,cancel:scheduler.cancel,
    runMc:async(argv,_input,options)=>{
      if(argv[0]==="__trace-register-setup") { signal=options?.signal;return new Promise(()=>{}); }
      return {exitCode:0,stdout:JSON.stringify({sessions:[regular],setup_pending:[f.row]}),stderr:""};
    }});
  const pass=lane.copyPass();
  const deadline=Date.now()+2000;
  while(!signal && Date.now()<deadline) await new Promise(resolve=>setTimeout(resolve,2));
  expect(signal).toBeDefined();
  await lane.copyPass();
  expect(await readFile(join(traceCopyPaths(f.home).claude,"ordinary-native.jsonl"),"utf8")).toBe(f.line);
  const firstSignal=signal!;
  await scheduler.advance(SESSIONS_TIMEOUT_MS);
  await pass;
  expect(firstSignal.aborted).toBe(true);
  expect(log.records.some(record=>record.msg.includes("recovery deadline"))).toBe(true);
  await lane.stop();
});
