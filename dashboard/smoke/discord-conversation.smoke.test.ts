import { afterAll, beforeAll, expect, test } from "bun:test";
import { join } from "node:path";
import { DiscordGatewayState, drainDiscordOutbox, nativeDiscordRest, nativeDiscordThreads, type DiscordPolicy } from "../../resident/src/discord";
import { bootSmoke, evidencePath, gotoReady, mc, teardown, type Smoke } from "./harness";
import type { Exec } from "../../resident/src/types";

let smoke: Smoke;
const policy: DiscordPolicy = { enabled: true, bot_token: "test-only", guild_id: "1", channel_id: "2", operator_ids: ["3"] };
const threads = new Map<string, any>();
const sources = new Map<string, any>();
const failedThreads = new Set<string>();
let interruptStart = false;
let interruptSend = false;
const deliveries: Array<{ thread: string; content: string; nonce: string }> = [];
let refuseDelivery = false;
const request = (async (url: any, options?: RequestInit) => {
  const path = new URL(String(url)).pathname.replace("/api/v10", "");
  const create = /^\/channels\/2\/messages\/(\d+)\/threads$/.exec(path);
  if (create) {
    const thread = { id: create[1], guild_id: "1", parent_id: "2", type: 11, owner_id: "4" };
    threads.set(thread.id, thread);
    return Response.json(thread);
  }
  if (path === "/users/@me") return Response.json({ id: "4", bot: true });
  const source = /^\/channels\/2\/messages\/(\d+)$/.exec(path);
  if (source) return sources.has(source[1]) ? Response.json(sources.get(source[1])) : new Response(null, { status: 404 });
  const read = /^\/channels\/(\d+)$/.exec(path);
  if (read) return threads.has(read[1]) ? Response.json(threads.get(read[1])) : new Response(null, { status: 404 });
  const post = /^\/channels\/(\d+)\/messages$/.exec(path);
  if (post) {
    if (refuseDelivery || failedThreads.has(post[1])) return new Response(null, { status: 403 });
    const body = JSON.parse(String(options?.body));
    if (!deliveries.some(row => row.thread === post[1] && row.nonce === body.nonce)) deliveries.push({ thread: post[1], ...body });
    return Response.json({ id: String(deliveries.length) });
  }
  throw new Error(`Unexpected Discord request: ${path}`);
}) as typeof fetch;
const runMc: Exec = async argv => {
  if (interruptSend && argv[1] === "send") { interruptSend = false; return { exitCode: 1, stdout: "", stderr: "The test interrupted the initial message write." }; }
  if (interruptStart && argv[1] === "start") { interruptStart = false; return { exitCode: 1, stdout: "", stderr: "The test interrupted the session write." }; }
  try { return { exitCode: 0, stdout: JSON.stringify(await mc(argv)), stderr: "" }; }
  catch (error) { return { exitCode: 1, stdout: "", stderr: String(error) }; }
};
const warnings: string[] = [];
function receiver() {
  return new DiscordGatewayState(policy, runMc, { warn(_event: string, detail: string) { warnings.push(detail); } } as any,
    undefined, undefined, undefined, undefined, nativeDiscordThreads(policy, request));
}
let gateway = receiver();
let sequence = 0;
async function discord(id: string, channel: string, content: string, author = "3", bot = false) {
  if (channel === "2") sources.set(id, { id, channel_id: channel, content, author: { id: author, bot } });
  await gateway.receive({ send() {}, close() {} }, { op: 0, s: ++sequence, t: "MESSAGE_CREATE",
    d: { id, guild_id: "1", channel_id: channel, content, author: { id: author, bot } } });
}
const flush = () => drainDiscordOutbox(policy, runMc, nativeDiscordRest(policy, request));
async function sessionFor(thread: string) {
  const result = await mc(["homie", "list"]) as any;
  return result.sessions.find((row: any) => row.discord_thread?.thread_id === thread)?.id as string;
}
async function reply(session: string, body: string) {
  const identity = join(smoke.scratch, `${session}.json`);
  await Bun.write(identity, JSON.stringify({ run_id: session, tier: "homie", role: "homie" }));
  const env = { MC_RUN_JSON: identity };
  await mc(["run", "register-session", session, "--native-ref", `native-${session}`, "--file", "native.jsonl"], env);
  const claim = await mc(["homie", "claim", session], env) as any;
  expect(claim.message).toBeTruthy();
  await mc(["homie", "reply", session, "--to", String(claim.message.id), "--body", body], env);
}
async function sendDashboard(body: string) {
  await smoke.page.getByTestId("send-body").fill(body);
  await Promise.all([
    smoke.page.waitForResponse(response => response.url().endsWith("/send") && response.request().method() === "POST" && response.ok()),
    smoke.page.getByTestId("send").click(),
  ]);
}
beforeAll(async () => { smoke = await bootSmoke(); await gotoReady(smoke); }, 60000);
afterAll(async () => { if (smoke) await teardown(smoke); });

test("one conversation continues between Discord and the dashboard across idle periods and a connector restart", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-homie").click();
  await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement)?.disabled);
  const privateSession = await page.locator(".session-row.selected").getAttribute("data-session");
  await sendDashboard("This conversation starts in the dashboard.");
  await reply(privateSession!, "This reply stays in the dashboard.");
  expect(await flush()).toBe(0);

  await discord("100", "2", "Plan the release.");
  const session = await sessionFor("100");
  expect(session).toBeTruthy();
  expect(threads.size).toBe(1);
  await reply(session, "First, check the release tests.");
  await flush();
  expect(deliveries.map(row => row.thread)).toEqual(["100"]);
  await page.reload();
  await page.locator(`[data-session="${session}"]`).click();
  await page.getByTestId("homie-transcript").getByText("Plan the release.", { exact: true }).waitFor();
  await page.getByTestId("homie-transcript").getByText("First, check the release tests.", { exact: true }).waitFor();

  await sendDashboard("I checked the tests from the dashboard.");
  await reply(session, "Next, inspect the release files.");
  await flush();
  expect(deliveries.slice(-2).map(row => row.content)).toEqual(["I checked the tests from the dashboard.", "Next, inspect the release files."]);
  await mc(["homie", "end", session, "--reason", "The session is idle."]);
  gateway = receiver();
  await discord("101", "100", "The release files look correct.");
  await reply(session, "The same conversation can continue.");
  await flush();
  await page.getByTestId("homie-transcript").getByText("The release files look correct.", { exact: true }).waitFor();

  await mc(["homie", "end", session, "--reason", "The session is idle again."]);
  await page.reload();
  await page.locator(`[data-session="${session}"]`).click();
  if (await page.getByTestId("resume").isVisible()) await page.getByTestId("resume").click();
  await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement)?.disabled, undefined, { timeout: 5000 }).catch(async error => {
    throw new Error(`${error}\n${await page.locator("#homie-pane").innerText()}\n${await page.locator('[role="alert"]').allTextContents()}`);
  });
  await sendDashboard("I am back in the dashboard.");
  await reply(session, "The thread still receives this reply.");
  refuseDelivery = true;
  await expect(flush()).rejects.toThrow("403");
  await page.getByTestId("homie-transcript").getByText("The thread still receives this reply.", { exact: true }).waitFor();
  refuseDelivery = false;
  await flush();
  expect(deliveries.slice(-2).map(row => row.content)).toEqual(["I am back in the dashboard.", "The thread still receives this reply."]);

  gateway = receiver();
  await discord("101", "100", "The release files look correct.");
  await discord("102", "100", "An unauthorized message", "8");
  await discord("103", "100", "A bot echo", "3", true);
  await discord("104", "999", "An unrelated thread");
  const history = await mc(["homie", "history", session]) as any;
  expect(history.messages.map((row: any) => row.body)).toEqual([
    "Plan the release.", "First, check the release tests.",
    "I checked the tests from the dashboard.", "Next, inspect the release files.",
    "The release files look correct.", "The same conversation can continue.",
    "I am back in the dashboard.", "The thread still receives this reply.",
  ]);
  expect(deliveries.every(row => row.thread === "100")).toBe(true);
  expect(warnings).toEqual([]);
  await page.screenshot({ path: evidencePath("discord-shared-conversation.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("discord-shared-conversation-mobile.png"), fullPage: true });

  await discord("200", "2", "Start a different conversation.");
  const second = await sessionFor("200");
  expect(second).not.toBe(session);
  expect(threads.size).toBe(2);
  await reply(second, "This reply belongs to the second thread.");
  await flush();
  expect(deliveries.at(-1)?.thread).toBe("200");
  const secondHistory = await mc(["homie", "history", second]) as any;
  expect(secondHistory.messages.map((row: any) => row.body)).toEqual(["Start a different conversation.", "This reply belongs to the second thread."]);
  await Bun.write(evidencePath("discord-transport.json"), JSON.stringify({ deliveries, session, second }, null, 2));
}, 60000);


test("an interrupted thread start recovers after restart and a failed thread permits other deliveries", async () => {
  interruptStart = true;
  await discord("400", "2", "Keep this initial message after a restart.");
  expect(threads.has("400")).toBe(true);
  expect(await sessionFor("400")).toBeUndefined();
  expect(warnings.at(-1)).toContain("interrupted");
  gateway = receiver();
  await discord("401", "400", "Continue the interrupted conversation.");
  const session = await sessionFor("400");
  expect(session).toBeTruthy();
  await reply(session, "The initial message remains available.");
  await reply(session, "This is the reply to the next message.");
  await discord("500", "2", "This conversation uses another thread.");
  const other = await sessionFor("500");
  await reply(other, "This thread can receive its reply.");
  failedThreads.add("400");
  await expect(flush()).rejects.toThrow("403");
  expect(deliveries.some(row => row.thread === "500" && row.content === "This thread can receive its reply.")).toBe(true);
  expect(deliveries.some(row => row.thread === "400")).toBe(false);
  failedThreads.clear();
  await flush();
  expect(deliveries.filter(row => row.thread === "400").map(row => row.content)).toEqual([
    "The initial message remains available.", "This is the reply to the next message.",
  ]);
  gateway = receiver();
  await discord("400", "2", "Keep this initial message after a restart.");
  const history = await mc(["homie", "history", session]) as any;
  expect(history.messages.filter((row: any) => row.direction === "inbound").map((row: any) => row.body)).toEqual([
    "Keep this initial message after a restart.", "Continue the interrupted conversation.",
  ]);
  expect(await flush()).toBe(0);
}, 30000);


test("an interrupted initial message recovers inside its existing session", async () => {
  interruptSend = true;
  await discord("600", "2", "Preserve the first message in the existing session.");
  const session = await sessionFor("600");
  expect(session).toBeTruthy();
  expect((await mc(["homie", "history", session]) as any).messages).toHaveLength(0);
  gateway = receiver();
  await discord("601", "600", "Continue after the interruption.");
  expect(await sessionFor("600")).toBe(session);
  const history = await mc(["homie", "history", session]) as any;
  expect(history.messages.map((row: any) => row.body)).toEqual([
    "Preserve the first message in the existing session.", "Continue after the interruption.",
  ]);
}, 30000);
