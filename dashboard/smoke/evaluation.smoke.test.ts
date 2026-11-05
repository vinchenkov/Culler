import { afterAll, beforeAll, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, SMOKE_BEARER, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket } from "./delivery-fixture";

let smoke: Smoke;
let task: number, packet: {packetID:number;versionID:number};
const body = "<!doctype html><html><body><h1>Original packet</h1><p>The packet explains the change.</p></body></html>";
const hash = (value: string | Buffer) => createHash("sha256").update(value).digest("hex");
const caseID = "case-browser-fixture";

beforeAll(async () => {
 smoke = await bootSmoke();
 await seedProject(smoke,"evaluation",{title:"Evaluation",vision:false});
 task = Number((await mc(["task","add","Assess the original packet","--project","evaluation","--description","The source preserves the original result."])).task_id);
 const plane=join(smoke.scratch,"reports","evaluation");mkdirSync(plane,{recursive:true});
 const report=join(plane,String(task),"packet","report.html");mkdirSync(join(plane,String(task),"packet"),{recursive:true});writeFileSync(report,body);
 writeFileSync(join(smoke.home,"mount-allowlist"),`version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "evaluation"\naccess = "ro"\n`,{mode:0o600});
 const db = openSpine(smoke);
 try {
  const renderPath=join(process.env.MC_ARTIFACTS_ROOT!,"evaluation",String(task),"packet","report.html");
  packet=seedDeliveryPacket(db,{taskID:task,renderPath,thesis:"The original explanation",reportSHA256:hash(body)});
  db.run("UPDATE tasks SET candidate_id=NULL WHERE id=?",[task]);
  const run=db.query("SELECT id FROM runs WHERE subject=? AND role='packager'").get(task) as {id:string};
  const row=db.query("SELECT v.result_commit FROM task_versions v WHERE v.id=?").get(packet.versionID) as {result_commit:string};
  db.run("UPDATE runs SET output_path=? WHERE id=?",[renderPath,run.id]);
  db.run("INSERT INTO evaluation_captures(run_id,project,task_id,version_id,enabled,case_id,status,packet_id,report_sha256) VALUES (?,?,?, ?,1,?,'complete',?,?)",[run.id,"evaluation",task,packet.versionID,caseID,packet.packetID,hash(body)]);
  const identity={deployment:"browser-fixture",project:"evaluation",task_id:task,requirements_version:1,packet_id:packet.packetID,version_id:packet.versionID,run_id:run.id,packet_sha256:hash(body),submitted_commit:row.result_commit};
  const root=join(smoke.home,"evals","cases",caseID);mkdirSync(root,{recursive:true,mode:0o700});
  const artifacts=[];
  // The browser fixture proves the interface and command receipt. The Go source test proves the real bundle.
  for(const [path,value] of Object.entries({"source/source.bundle":"browser fixture","source/reviewed.diff":"diff --git a/source b/source\n+Original source\n","input/effective.json":"{\"brief\":\"Original input\"}","input/records.json":"{}","input/access.json":"{}","input/inventory.json":"[]","input/runtime.json":"{}","source/identities.json":"{}","packet/report":body,"packet/records.json":JSON.stringify({packet:packet.packetID}),"evidence/verifier.txt":"The Verifier reported a pass."})) {
   const file=join(root,path);mkdirSync(join(file,".."),{recursive:true,mode:0o700});writeFileSync(file,value,{mode:0o600});
   artifacts.push({path,size:Buffer.byteLength(value),sha256:hash(value),origin:"browser fixture",captured_at:new Date().toISOString(),media_type:"text/plain"});
  }
  writeFileSync(join(root,"manifest.json"),JSON.stringify({format_version:1,case_id:caseID,case_group:"browser-fixture/evaluation/1",identity,capture_evaluation_cases:true,artifacts,absences:{},replay_complete:true,reference_status:"Source facts require review."}),{mode:0o600});
 } finally {db.close();}
 await gotoReady(smoke);await selectProject(smoke,"evaluation");
 await smoke.page.getByTestId("tab-review").click();
 await smoke.page.getByTestId("packet-row").first().click();
},30000);
afterAll(async()=>{await teardown(smoke);});

async function flag(enabled:boolean){
 const config=await mc(["config","show"]);
 const row=(config.families as any[]).flatMap(f=>f.knobs).find(r=>r.key==="development.capture_evaluation_cases");
 const response = await fetch(`${smoke.server.url}/api/config`, {method:"POST",headers:{...SMOKE_BEARER,"content-type":"application/json"},body:JSON.stringify({changes:[{key:row.key,expected:row.value,value:enabled}]})});
 expect(response.status).toBe(200);
}

test("the default hides the control and authentication protects all case operations",async()=>{
 expect(await smoke.page.getByTestId("score-case").isHidden()).toBe(true);
 for(const action of ["status","submit","download","inspect"]){
  const response=await fetch(`${smoke.server.url}/api/evaluation/${action}`,{method:"POST",headers:{"content-type":"application/json"},body:"{}"});expect(response.status).toBe(401);
 }
});

test("the exact packet form preserves a draft and saves an immutable revision",async()=>{
 await flag(true);
 const capture = await fetch(`${smoke.server.url}/api/evaluation/status`,{method:"POST",headers:{...SMOKE_BEARER,"content-type":"application/json"},body:JSON.stringify({project:"evaluation",task_id:task,packet_id:packet.packetID,version_id:packet.versionID})}).then(r=>r.json());
 expect(capture.available).toBe(true);
 expect(capture.enabled).toBe(true);

 await smoke.page.waitForFunction(() => {const button=document.querySelector<HTMLButtonElement>('[data-testid="score-case"]');return button && !button.disabled && button.getClientRects().length>0;},null,{timeout:10000}).catch(async error => {await smoke.page.screenshot({animations:"disabled",path:evidencePath("evaluation-failure.png")});throw new Error(`${error.message} ${await smoke.page.locator("#evaluation-control").evaluate(node=>node.outerHTML)} ${await smoke.page.locator("#packet-reader").innerText()} ${smoke.browserLog.join("\n")}`);});
 await smoke.page.getByTestId("score-case").click();
 const dialog=smoke.page.getByTestId("score-dialog");
 expect(await dialog.isVisible()).toBe(true);
 expect(await dialog.getByTestId("score-identity").textContent()).toContain(`Version ${packet.versionID}`);
 for(const key of ["comprehension","causal_explanation","decision_support","visual_explanation","clarity","packet_length"]){
  expect(await dialog.getByTestId(`score-${key}`).inputValue()).toBe("");await dialog.getByTestId(`score-${key}`).selectOption("3");
 }
 await dialog.getByTestId("score-critical").selectOption("false");
 await dialog.getByTestId("score-reason").fill("The packet explains the source and its evidence limits.");
 await dialog.getByTestId("score-familiar").check();
 await dialog.getByTestId("score-source").selectOption("true");
 await dialog.getByTestId("score-evidence").selectOption("false");
 expect(await dialog.getByRole("button",{name:"Close",exact:true}).getAttribute("title")).toBe("Close and keep draft");
 await dialog.getByRole("button",{name:"Close",exact:true}).click();
 await dialog.waitFor({state:"detached"});
 expect(await smoke.page.getByTestId("score-case").evaluate(node=>document.activeElement===node)).toBe(true);
 await smoke.page.getByTestId("score-case").click();
 expect(await dialog.getByTestId("score-reason").inputValue()).toBe("The packet explains the source and its evidence limits.");
 expect(await dialog.getByTestId("score-comprehension").inputValue()).toBe("3");
 await flag(false);
 await dialog.getByTestId("score-submit").click();
 await smoke.page.waitForFunction(() => document.querySelector('[data-testid="score-error"]')?.textContent?.includes("disabled"));
 expect(await dialog.getByTestId("score-reason").inputValue()).toBe("The packet explains the source and its evidence limits.");
 await flag(true);
 await dialog.getByTestId("score-submit").click();
 await smoke.page.waitForFunction(() => document.querySelector('[data-testid="case-saved"]')?.textContent?.includes("Case saved:"));
 expect(await dialog.getByTestId("case-download").isVisible()).toBe(true);
 const receipts=readFileSync(join(smoke.home,"evals","cases",caseID,"assessments","000001","receipt.json"),"utf8");
 expect(JSON.parse(receipts).packet_sha256).toBe(hash(body));
 await dialog.getByRole("button",{name:"Revise assessment",exact:true}).click();
 await dialog.getByTestId("score-reason").fill("The revised assessment preserves the earlier assessment.");
 await dialog.getByTestId("score-submit").click();
 await smoke.page.waitForFunction(() => document.querySelector('[data-testid="case-saved"]')?.textContent?.includes("Assessment revision 2"));
 expect(readFileSync(join(smoke.home,"evals","cases",caseID,"assessments","000001","receipt.json"),"utf8")).toBe(receipts);
 await smoke.page.screenshot({animations:"disabled",path:evidencePath("evaluation-wide.png")});
 await smoke.page.setViewportSize({width:390,height:844});
 expect(await dialog.isVisible()).toBe(true);
 const box=await dialog.boundingBox();expect(box!.x).toBeGreaterThanOrEqual(0);expect(box!.x+box!.width).toBeLessThanOrEqual(390);
 await smoke.page.screenshot({animations:"disabled",path:evidencePath("evaluation-narrow.png")});
 await dialog.getByRole("button",{name:"Close",exact:true}).click();
 await smoke.page.getByTestId("score-case").waitFor({state:"visible"});
 expect(await smoke.page.getByTestId("score-case").isVisible()).toBe(true);
},30000);

test("the Ledger retains the score control and scoring preserves the packet decision",async()=>{
 const db = openSpine(smoke);
 try {
  const row=db.query("SELECT status,decision,archived FROM tasks WHERE id=?").get(task) as any;
  expect(row).toEqual({status:"packaged",decision:null,archived:0});
  expect((db.query("SELECT count(*) AS count FROM review_decisions WHERE packet_id=?").get(packet.packetID) as any).count).toBe(0);
 } finally {db.close();}
 const response=await fetch(`${smoke.server.url}/api/packets/${task}/decide`,{method:"POST",headers:{...SMOKE_BEARER,"content-type":"application/json"},body:JSON.stringify({decision:"cancel",reason:"The fixture enters the Ledger.",packet_id:packet.packetID,version_id:packet.versionID})});
 expect(response.status).toBe(200);
 await smoke.page.setViewportSize({width:1440,height:1000});
 await smoke.page.getByTestId("packet-view-ledger").click();
 await smoke.page.getByTestId("ledger-filter-cancelled").click();
 await smoke.page.getByTestId("packet-row").first().click();
 await smoke.page.getByTestId("score-case").waitFor({state:"visible"});
 expect(await smoke.page.getByTestId("score-case").isEnabled()).toBe(true);
 await smoke.page.setViewportSize({width:390,height:844});
 expect(await smoke.page.getByTestId("score-case").isVisible()).toBe(true);
 const frame=smoke.page.getByTestId("packet-frame");
 expect(await frame.getAttribute("sandbox")).toBe("allow-popups allow-popups-to-escape-sandbox");
 await flag(false);
 const saved=await fetch(`${smoke.server.url}/api/evaluation/cases`,{headers:SMOKE_BEARER}).then(r=>r.json());
 expect(saved.cases[0].manifest.case_id).toBe(caseID);
},20000);
