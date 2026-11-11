// Supply NATIVE_TEST_IMAGE and NATIVE_OPENCODE_ROOT, then run through mise exec -- bun.
import { strict as assert } from "node:assert";
import { Database } from "bun:sqlite";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { copyNativeDatabase } from "../src/opencode-copy";
import { OPENCODE_DATABASE } from "../../runner/shared/opencode-trace";

const image = process.env.NATIVE_TEST_IMAGE, runtime = process.env.NATIVE_OPENCODE_ROOT;
if (!image || !runtime) throw new Error("Supply a Culler runtime image and the pinned OpenCode package directory.");
const root = await mkdtemp(join(tmpdir(), "culler-native-resume-"));
const container = `culler-native-resume-${process.pid}`;
let child: ReturnType<typeof Bun.spawn> | undefined;
try {
  await writeFile(join(root, "driver.ts"), `import { mkdir } from 'node:fs/promises';
import { Database } from 'bun:sqlite';
const binary='/opencode/node_modules/@opencode/cli-linux-arm64/bin/opencode';
const version=Bun.spawn([binary,'--version'],{stdout:'pipe',stderr:'pipe'});
if ((await new Response(version.stdout).text()).trim() !== 'opencode v2.2.5' || await version.exited !== 0) throw new Error('The OpenCode version differs from the pin.');
let requests=0;
const server=Bun.serve({hostname:'127.0.0.1',port:0,async fetch(request){
 const body=await request.json();
 if (request.headers.get('authorization') !== 'Bearer synthetic-native-key' || body.model !== 'fixture-model') return new Response('Invalid fixture request',{status:400});
 const text='NATIVE_FIXTURE_REPLY_'+(++requests);
 const base={id:'fixture-'+requests,object:'chat.completion.chunk',created:1700000000,model:'fixture-model'};
 if (!body.stream) return Response.json({...base,object:'chat.completion',choices:[{index:0,message:{role:'assistant',content:text},finish_reason:'stop'}],usage:{prompt_tokens:11,completion_tokens:7,total_tokens:18}});
 const rows=[{...base,choices:[{index:0,delta:{role:'assistant',content:text},finish_reason:null}]},{...base,choices:[{index:0,delta:{},finish_reason:'stop'}],usage:{prompt_tokens:11,completion_tokens:7,total_tokens:18}}];
 return new Response(rows.map(row=>'data: '+JSON.stringify(row)+'\\n\\n').join('')+'data: [DONE]\\n\\n',{headers:{'content-type':'text/event-stream'}});
}});
const model='fixture/fixture-model';
const settings={baseURL:'http://127.0.0.1:'+server.port+'/v1',apiKey:'synthetic-native-key',maxRetries:0};
const config={model,update:'disable',share:'disabled',warming:false,snapshots:false,lsp:false,formatter:false,websearch:false,
 providers:{fixture:{settings,models:{'fixture-model':{package:'@opencode/ai/providers/openai-compatible',settings}}}},
 agents:{build:{model},compaction:{model},title:{model,disabled:true},summary:{model,disabled:true}},
 permissions:[{action:'*',resource:'*',effect:'allow'}],policies:[{action:'provider.use',resource:'*',effect:'deny'},{action:'provider.use',resource:'fixture',effect:'allow'}]};
await mkdir('/fixture/workspace',{recursive:true});
const env={...process.env,HOME:'/fixture/home',XDG_DATA_HOME:'/fixture/session/opencode/data',XDG_CONFIG_HOME:'/fixture/session/opencode/config',XDG_STATE_HOME:'/fixture/session/opencode/state',XDG_CACHE_HOME:'/fixture/session/opencode/cache',
 OPENCODE_CONFIG_CONTENT:JSON.stringify(config),OPENCODE_DISABLE_PROJECT_CONFIG:'1',OPENCODE_DISABLE_MODELS_FETCH:'1',OPENCODE_DISABLE_FREE_MODELS:'1',OPENCODE_DISABLE_AUTOUPDATE:'1',OPENCODE_DISABLE_FILEWATCHER:'1'};
let native;
try {for(let turn=1;turn<=2;turn++){
 const proc=Bun.spawn([binary,'run','--standalone','--auto','--format','json',...(native?['--session',native]:[])],{cwd:'/fixture/workspace',env,stdin:new TextEncoder().encode('Synthetic native turn '+turn),stdout:'pipe',stderr:'pipe'});
 const [stdout,stderr,code]=await Promise.all([new Response(proc.stdout).text(),new Response(proc.stderr).text(),proc.exited]);
 if(code) throw new Error('Native turn failed: '+stdout+' '+stderr);
 const events=stdout.trim().split('\\n').flatMap(line=>{try{return [JSON.parse(line)]}catch{return []}});
 const id=events.find(row=>typeof row.sessionID==='string')?.sessionID;
 if(!id || native && id!==native || !stdout.includes('NATIVE_FIXTURE_REPLY_'+turn)) throw new Error('Invalid native turn: '+stdout+' '+stderr);
 native=id;
 await Bun.write('/fixture/turn-'+turn,JSON.stringify({native,requests}));
 if(turn===1) while(!await Bun.file('/fixture/resume').exists()) await Bun.sleep(20);
}} finally {server.stop(true);}
`);
  child = Bun.spawn(["docker", "run", "--rm", "--network", "none", "--name", container,
    "--mount", `type=bind,src=${root},dst=/fixture`, "--mount", `type=bind,src=${runtime},dst=/opencode,readonly`,
    "--entrypoint", "/opt/culler-runtime/bin/bun", image, "/fixture/driver.ts"], { stdout: "pipe", stderr: "pipe" });
  const output = new Response(child.stdout).text(), errors = new Response(child.stderr).text();
  const wait = async (file: string) => {
    for (let attempt = 0; attempt < 1500; attempt++) {
      if (await Bun.file(join(root, file)).exists()) return JSON.parse(await readFile(join(root, file), "utf8"));
      if (child!.exitCode !== null) throw new Error(`Native fixture failed: ${await output}\n${await errors}`);
      await Bun.sleep(20);
    }
    throw new Error("The native fixture exceeded its time limit.");
  };
  const first = await wait("turn-1");
  const session = join(root, "session"), destination = join(root, "copies/opencode-fixture.db");
  const original = await readFile(join(session, OPENCODE_DATABASE));
  await copyNativeDatabase(session, destination, first.native, ["synthetic-native-key"]);
  assert.deepEqual(await readFile(join(session, OPENCODE_DATABASE)), original);
  await writeFile(join(root, "resume"), "resume");
  const second = await wait("turn-2");
  assert.equal(first.native, second.native);
  assert.equal(await child.exited, 0, await errors);
  child = undefined;
  await copyNativeDatabase(session, destination, first.native, ["synthetic-native-key"]);
  const copy = new Database(destination, { readonly: true });
  try {
    const messages = JSON.stringify(copy.query("SELECT id,session_id,data FROM session_message WHERE session_id=?").all(first.native));
    assert(messages.includes("NATIVE_FIXTURE_REPLY_1") && messages.includes("NATIVE_FIXTURE_REPLY_2"));
    assert(!messages.includes("synthetic-native-key"));
  } finally { copy.close(); }
  console.log("PASS OpenCode 2.2.5 native resume after a host snapshot, stable identity, and both turns in the final copy");
} finally {
  if (child) {
    await Bun.spawn(["docker", "rm", "-f", container], { stdout: "ignore", stderr: "ignore" }).exited;
    await child.exited;
  }
  await rm(root, { recursive: true, force: true });
}
