import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, gotoReady, mc, teardown, type Smoke, MC_BIN } from "./harness";

let smoke: Smoke;
let connection: string;
let other: string;
const identity = () => crypto.randomUUID().replaceAll("-", "");
async function state(action: string, value: Record<string, unknown>) {
  const proc = Bun.spawn([MC_BIN, "__system-state", action], {
    env: process.env, stdin: new TextEncoder().encode(JSON.stringify(value)), stdout: "pipe", stderr: "pipe",
  });
  const [out, error, code] = await Promise.all([new Response(proc.stdout).text(), new Response(proc.stderr).text(), proc.exited]);
  if (code) throw new Error(error);
  return JSON.parse(out);
}
function available() {
  mkdirSync(join(smoke.home, "health"), { recursive: true });
  writeFileSync(join(smoke.home, "health", "resident-availability.json"), JSON.stringify({observed_at:new Date().toISOString(),can_commit:true}));
}
async function failure(id: string) {
  const check = {connection_id:id,authorization_id:"unavailable",operation:"token_refresh",check_id:identity()};
  await state("check-start",check);
  await state("observe",{...check,event_id:identity(),outcome:"failed",http_status:400,provider_error_code:"invalid_grant",observed_at:new Date().toISOString(),request_sent:true});
}
beforeAll(async()=>{
  smoke=await bootSmoke();
  const catalog=await mc(["agent-profile","list"]);
  const connections=(catalog.connections as Array<{id:string;kind:string}>).filter(value=>value.kind.endsWith("oauth"));
  connection=connections[0].id;other=connections[1].id;
  await failure(connection);await failure(other);available();
  await gotoReady(smoke);
});
afterAll(async()=>{await teardown(smoke);});

test("multiple failures use a compact notice and keep independent repair actions",async()=>{
  const {page}=smoke;
  for(const tab of ["board","pipeline","configs","homie","logs"]){
    await page.getByTestId(`tab-${tab}`).click();
    const notice=page.getByTestId("system-notices");
    expect(await notice.innerText()).toContain("2 issues need attention");
    expect(await notice.innerText()).not.toContain("invalid_grant");
    expect((await notice.boundingBox())!.height).toBeLessThan(150);
  }
  await page.getByTestId("system-notices").getByRole("button",{name:"Review issues"}).click();
  const dialog=page.getByRole("dialog",{name:"System issues",exact:true});
  for (const id of [connection,other]) {
    const item=dialog.locator(`[data-condition-id="connection:${id}:token_refresh:"]`);
    expect(await item.getByRole("button",{name:"Reconnect",exact:true}).count()).toBe(1);
  }
  await page.keyboard.press("Escape");
  expect(await page.getByRole("button",{name:"Review issues",exact:true}).evaluate(node=>node===document.activeElement)).toBe(true);
  for (const width of [390,320]) {
    await page.setViewportSize({width,height:844});
    expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    expect((await page.getByTestId("system-notices").boundingBox())!.height).toBeLessThan(190);
  }
  await page.setViewportSize({width:1440,height:1000});
},30000);

async function capture(name: string) {
  const root = process.env.MC_UX_EVIDENCE;
  if (!root) return;
  mkdirSync(root, {recursive:true});
  await smoke.page.screenshot({path:join(root, `${name}.png`)});
}

test("status polls preserve keyboard focus on the notice", async () => {
  const {page} = smoke;
  const control = page.getByTestId("system-notices").getByRole("button", {name:"Review issues"});
  await control.focus();
  const response = page.waitForResponse("**/api/system/status");
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await (await response).finished();
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)));
  expect(await control.evaluate(node => node === document.activeElement)).toBe(true);
});

test("an open diagnostic dialog reflects recovery and unavailable status",async()=>{
  const {page} = smoke;
  await page.getByTestId("system-notices").getByRole("button", {name:"Review issues"}).click();
  await page.getByRole("dialog",{name:"System issues",exact:true})
    .locator(`[data-condition-id="connection:${connection}:token_refresh:"]`).getByRole("button", {name:/View details/}).click();
  const dialog = page.getByRole("dialog", {name:"Issue details"});
  expect(await dialog.locator(".system-history-list").count()).toBe(0);
  expect(await dialog.getByText("Refresh activity", {exact:true}).count()).toBe(0);
  expect(await dialog.innerText()).toContain("The provider rejected the credential refresh.");
  expect(await dialog.innerText()).toContain("Automatic retries stopped.");
  await capture("notice-default");
  await dialog.getByText("Technical details", {exact:true}).click();
  expect(await dialog.innerText()).not.toContain("Consecutive failures");
  expect(await dialog.innerText()).not.toContain("Recorded when this view opened");
  expect((await dialog.innerText()).match(/invalid_grant/g)?.length).toBe(1);
  await page.setViewportSize({width:320,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  expect(await dialog.evaluate(node=>node.scrollWidth <= node.clientWidth)).toBe(true);
  await capture("dialog-320");
  await page.setViewportSize({width:1440,height:1000});
  available();
  await state("verified",{connection_id:connection,authorization_id:"unavailable",operation:"token_refresh",check_id:identity(),event_id:identity(),outcome:"ready",observed_at:new Date().toISOString()});
  await page.evaluate(()=>window.dispatchEvent(new Event("focus")));
  await page.waitForFunction(()=>document.querySelector(".system-detail-current")?.textContent?.includes("Connected"));
  expect(await dialog.getByRole("button",{name:"Reconnect",exact:true}).count()).toBe(0);
  expect(await dialog.locator(".system-facts").textContent()).toContain("Last verified");
  expect(await dialog.locator(".system-facts").textContent()).not.toContain("invalid_grant");
  expect(await dialog.locator(".system-detail-current").textContent()).not.toContain("rejected");
  expect(await dialog.locator(".system-detail-current").textContent()).not.toContain("retries stopped");
  await capture("dialog-recovered");
  await page.route("**/api/system/status",route=>route.fulfill({contentType:"application/json",body:JSON.stringify({complete:false,conditions:[]})}));
  await page.evaluate(()=>window.dispatchEvent(new Event("focus")));
  await page.waitForFunction(()=>document.querySelector(".system-detail-current")?.textContent?.includes("Status unavailable"));
  expect(await dialog.locator(".system-detail-current").textContent()).toContain("Last known: Connected");
  await capture("dialog-unavailable");
  await page.unroute("**/api/system/status");
  await dialog.getByRole("button", {name:"View connection history"}).click();
  const historyDialog = page.getByRole("dialog", {name:"Diagnostic history"});
  await page.waitForFunction(() => document.querySelector(".system-history-list")?.textContent?.includes("invalid_grant"));
  expect(await historyDialog.getByText("Started", {exact:true}).count()).toBe(0);
  expect((await historyDialog.innerText()).match(/invalid_grant/g)?.length).toBe(1);
  expect(await historyDialog.getByRole("button", {name:"Reconnect",exact:true}).count()).toBe(0);
  await historyDialog.getByText("Refresh activity", {exact:true}).click();
  expect(await historyDialog.locator(".system-refresh-summary").innerText()).toContain("1 failure from 1 refresh request");
  await historyDialog.getByRole("combobox", {name:"Refresh period"}).selectOption("week");
  expect(await historyDialog.locator(".system-refresh-summary").count()).toBe(1);
  await capture("notice-history");
  await page.keyboard.press("Escape");
  await page.evaluate(()=>window.dispatchEvent(new Event("focus")));
  await page.waitForFunction(id=>document.querySelector(`#system-notices [data-condition-id="connection:${id}:token_refresh:"]`) && !document.querySelector("#system-notices")?.textContent?.includes("Status unavailable"),other);
  const history=await mc(["system","diagnostics","--connection",connection]);
  expect((history.events as Array<{outcome:string}>).some(event=>event.outcome==="failed")).toBe(true);
},30000);

test("an unavailable status preserves the known failure",async()=>{
  let observed=0;
  await smoke.page.route("**/api/system/status",route=>{observed++;return route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({complete:false,conditions:[]})});});
  await smoke.page.evaluate(()=>window.dispatchEvent(new Event("focus")));
  try { await smoke.page.waitForFunction(()=>document.querySelector("#system-notices")?.textContent?.includes("Status unavailable"),null,{timeout:7000}); } catch(error) { throw new Error(`Status responses: ${observed}; content: ${await smoke.page.locator("#system-notices").textContent()}; log: ${smoke.browserLog.join(" | ")}`); }
  await smoke.page.getByTestId("system-notices").getByRole("button", {name:"Review issues"}).click();
  expect(await smoke.page.getByRole("dialog", {name:"System issues", exact:true}).locator(`[data-condition-id="connection:${other}:token_refresh:"]`).count()).toBe(1);
  await smoke.page.keyboard.press("Escape");
  await smoke.page.unroute("**/api/system/status");
},30000);

test("System health separates current state from collapsed evidence", async () => {
  const {page} = smoke;
  await page.route("**/api/doctor", route => route.fulfill({contentType:"application/json",body:JSON.stringify({
    ok:false,deployment_ok:true,deployment_complete:true,completed_at:"2026-09-20T06:28:42.438307Z",
    findings:[{check:"spine",status:"ok",detail:"Database available."},{check:"runtime-auth",status:"fail",detail:"Connection requires attention."}],
  })}));
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-system").click();
  await page.getByTestId("config-section-system").waitFor();
  const deployment = (await page.getByTestId("pipeline-doctor").boundingBox())!;
  const connections = (await page.locator("#system-connections").boundingBox())!;
  expect(deployment.y).toBeLessThan(connections.y);
  expect(await page.locator("#system-connection-more").getAttribute("open")).toBeNull();
  await page.getByTestId("doctor-run").click();
  await page.waitForFunction(()=>document.querySelector("#deployment-status")?.textContent?.includes("Deployment checks passed"));
  expect(await page.locator("#deployment-status").textContent()).toContain("Checked Sep");
  expect(await page.locator("#deployment-findings").getAttribute("open")).toBeNull();
  expect(await page.locator("#resident-history").getAttribute("open")).toBeNull();
  expect(await page.locator("#system-execution-section").isVisible()).toBe(false);
  expect(await page.getByTestId("config-section-advanced").isVisible()).toBe(false);
  await page.locator("#deployment-findings > summary").click();
  expect(await page.locator("#deployment-checks").innerText()).toContain("1 check, 1 passed, 0 failed");
  for(const width of [390,320]) {
    await page.setViewportSize({width,height:844});
    expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.locator("#resident-history > summary").click();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.locator("#resident-history > summary").click();
  }
  await page.unroute("**/api/doctor");
},30000);

test("Reconnect from the notice shows the connection and starts authorization only on request", async () => {
  const {page} = smoke;
  await page.setViewportSize({width:1440,height:1000});
  let starts = 0;
  let authorized = false;
  let verified = false;
  const pattern = "**/api/agent-profiles/operation-*";
  await page.route(pattern, route => {
    const request = route.request().postDataJSON();
    const action = route.request().url().split("/").pop();
    if (action === "operation-start" && ++starts === 1) {
      return route.fulfill({status:400,contentType:"application/json",body:JSON.stringify({error:{message:"The host lacks the subscription authorization executable."}})});
    }
    // The host refuses a cancellation after it saves the credential.
    const saved = authorized && action !== "operation-start";
    return route.fulfill({contentType:"application/json",body:JSON.stringify({
      operation_id:request.operation_id,kind:"authorization",connection_id:other,
      status:saved ? verified ? "succeeded" : "running" : action === "operation-cancel" ? "cancelled" : "action_required",
      ...(saved ? {message:verified ? "Authorization succeeded. Culler verified access to this connection." : "Authorization succeeded. The access check is in progress."}
        : {authorization_url:"https://authorization.example/device"}),
    })});
  });
  // The saved credential stays present while the provider rejects its refresh.
  await page.route("**/api/agent-profiles", async route => {
    const response = await route.fetch();
    const catalog = await response.json();
    for (const row of [...catalog.connections, ...catalog.profiles]) row.access_status = "connected";
    await route.fulfill({response, json:catalog});
  });
  try {
    await page.getByTestId("system-notices").getByRole("button",{name:"Reconnect",exact:true}).click();
    const panel = page.getByTestId("agent-profiles-panel");
    const repair = panel.getByTestId("profile-repair");
    await repair.waitFor();
    expect(await repair.innerText()).toContain("Authorization required");
    expect(await repair.innerText()).toContain("The provider rejected the credential refresh.");
    expect(await repair.innerText()).toContain("start the authorization for the connection");
    expect(await panel.getByTestId("profile-operation").count()).toBe(0);
    expect(await panel.getByTestId("profile-model").count()).toBe(0);
    expect(starts).toBe(0);
    await capture("reconnect-opened");
    const reconnect = repair.getByTestId("profile-reconnect");
    await reconnect.click();
    await panel.getByTestId("profile-error").waitFor();
    expect(await panel.getByTestId("profile-error").textContent()).toContain("authorization executable");
    expect(await reconnect.isEnabled()).toBe(true);
    await capture("reconnect-rejected");
    await reconnect.click();
    await panel.getByRole("link",{name:"Open authorization page"}).waitFor();
    expect(starts).toBe(2);
    expect(await panel.getByTestId("profile-error").count()).toBe(0);
    expect(await repair.count()).toBe(0);
    await capture("reconnect-started");
    await panel.getByRole("button",{name:"Cancel authorization"}).click();
    await reconnect.waitFor();
    expect(await reconnect.isEnabled()).toBe(true);
    authorized = true;
    await reconnect.click();
    await panel.getByText("The access check is in progress.").waitFor();
    await panel.getByRole("button",{name:"Cancel authorization"}).click();
    await page.waitForTimeout(300);
    expect(await panel.getByTestId("profile-operation").innerText()).toContain("The authorization is in progress");
    await page.route("**/api/system/status", route => verified ? route.fulfill({contentType:"application/json",body:JSON.stringify({
      complete:true,observed_at:new Date().toISOString(),revision:"",conditions:[],
      connections:[{id:`connection:${other}:token_refresh:`,group:"connections",state:"ready",connection_id:other,name:other,condition:"Connection access is verified.",action:"Reconnect",failure_count:0,revision:1}],
    })}) : route.continue());
    verified = true;
    await panel.getByText("Culler verified access to this connection.").waitFor();
    // The periodic status request needs up to 5 seconds. The panel requests the status at once.
    await page.waitForFunction(() => !document.querySelector("#system-notices")?.textContent?.includes("Reconnect required"), null, {timeout:2000});
    expect(starts).toBe(3);
    expect(await repair.count()).toBe(0);
    await capture("reconnect-succeeded");
    await panel.getByTestId("profile-close").click();
    // A region without a notice takes no space between the header and the page.
    expect(await page.getByTestId("system-notices").isVisible()).toBe(false);
    const gap = await page.evaluate(() => document.querySelector("main")!.getBoundingClientRect().top - document.querySelector("header.topbar")!.getBoundingClientRect().bottom);
    expect(gap).toBeLessThanOrEqual(20);
  } finally { await page.unroute(pattern); await page.unroute("**/api/agent-profiles"); await page.unroute("**/api/system/status"); }
},30000);
