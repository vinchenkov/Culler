import { describe, expect, test } from "bun:test";
import { channelPermissions, DiscordConfiguration } from "./discord-config";
import type { DiscordInboundMessage } from "./discord";

function fixture() {
  let now = Date.now(), allowed = true, failDelivery = false, failReady = false, detail = "";
  const receivers: Array<{ preview: boolean; stopped: boolean; active: boolean; observe?: (message: DiscordInboundMessage) => void }> = [];
  const posts: any[] = [];
  const rights = [10n, 11n, 15n, 16n, 35n, 38n].reduce((bits, bit) => bits | (1n << bit), 0n).toString();
  const api = async (token: string, path: string, body?: any) => {
    if (token !== "test-secret") throw new Error("The bot token is invalid.");
    if (path === "/users/@me") return { id: "1", username: "Culler", bot: true };
    if (path.startsWith("/users/@me/guilds")) return [{ id: "2", name: "Workspace" }];
    if (path === "/guilds/2") return { id: "2", name: "Workspace" };
    if (path === "/guilds/2/members/1") return { roles: [] };
    if (path === "/guilds/2/roles") return [{ id: "2", permissions: allowed ? rights : "1024" }];
    const channel = { id: "3", guild_id: "2", name: "culler", type: 0, permission_overwrites: [] };
    if (path === "/guilds/2/channels") return [channel];
    if (path === "/channels/3") return channel;
    if (path === "/users/4") return { id: "4", username: "Alex" };
    if (path === "/channels/3/messages") {
      if (failDelivery) throw new Error("Discord did not confirm delivery.");
      posts.push(body); return { id: "5" };
    }
    throw new Error(`Unexpected test path: ${path}`);
  };
  const service = new DiscordConfiguration(api, async (_policy, options) => {
    const receiver = { preview: Boolean(options?.preview), stopped: false, active: !options?.preview, observe: options?.observe };
    receivers.push(receiver);
    return { async stop() { receiver.stopped = true; }, async ready() { if (failReady) throw new Error("The Gateway is unavailable."); }, activate() { receiver.active = true; },
      health() { return { connected: !receiver.stopped, detail, checked_at: new Date(now).toISOString() }; } };
  }, () => now);
  async function setup() {
    const draft = await service.request("check-bot", { token: "test-secret", expected: "revision" });
    const request = { draft: draft.draft, expected: "revision", guild: "2", channel: "3" };
    await service.request("channels", request);
    const result = await service.request("test", request);
    return { request, result };
  }
  function reply(code: string, extra: Partial<DiscordInboundMessage> = {}) {
    receivers.at(-1)!.observe?.({ message_id: "6", author_id: "4", author_name: "Alex", author_is_bot: false,
      guild_id: "2", channel_id: "3", content: code, ...extra });
  }
  return { service, setup, reply, posts, receivers, advance() { now += 6 * 60_000; }, revoke() { allowed = false; },
    problem(value: string) { detail = value; }, fail() { failDelivery = true; }, failReady() { failReady = true; } };
}

describe("Discord configuration", () => {
  test("the setup proves delivery and a human account before it permits activation", async () => {
    const f = fixture();
    const { request, result } = await f.setup();
    expect(JSON.stringify(result)).not.toContain("test-secret");
    expect(f.posts).toHaveLength(1);
    expect(JSON.stringify(f.posts)).not.toContain(result.code);
    await expect(f.service.request("candidate", request)).rejects.toThrow("Confirm your account");
    for (const extra of [{ author_is_bot: true }, { channel_id: "9" }, { guild_id: "9" }]) {
      f.reply(result.code, extra);
      expect((await f.service.request("reply", request)).account).toBeUndefined();
    }
    f.reply(result.code);
    const candidate = await f.service.request("candidate", request);
    expect(candidate.policy.operator_ids).toEqual(["4"]);
    await f.service.request("apply", candidate);
    expect((await f.service.request("status")).status).toBe("connected");
    expect((await f.service.request("status")).detail).toBe("");
    await f.service.request("cancel", request);
    await expect(f.service.request("candidate", request)).rejects.toThrow("expired");
    await f.service.stop();
    expect(f.receivers.every(receiver => receiver.stopped)).toBe(true);
  });

  test("a delivery failure reports attention and permits an independent test", async () => {
    const f = fixture();
    const policy = { enabled: true, bot_token: "test-secret", guild_id: "2", channel_id: "3", operator_ids: ["4"] };
    await f.service.request("apply", { policy });
    f.problem("A queued Discord message failed.");
    expect((await f.service.request("check")).status).toBe("attention");
    expect((await f.service.request("test-current")).delivered).toBe(true);
    expect(f.posts).toHaveLength(1);
    expect(f.receivers).toHaveLength(1);
    f.fail();
    await expect(f.service.request("test-current")).rejects.toThrow("confirm delivery");
    await f.service.stop();
  });

  test("a connection check restores an inactive receiver", async () => {
    const f = fixture();
    const policy = { enabled: true, bot_token: "test-secret", guild_id: "2", channel_id: "3", operator_ids: ["4"] };
    await f.service.request("apply", { policy });
    await f.service.request("quiesce");
    expect((await f.service.request("check")).status).toBe("connected");
    expect((await f.service.request("test-current")).delivered).toBe(true);
    expect(f.receivers).toHaveLength(2);
    await f.service.stop();
  });

  test("expiry, a changed destination, and a changed revision invalidate confirmation", async () => {
    const f = fixture();
    const { request, result } = await f.setup();
    f.reply(result.code); f.advance();
    expect((await f.service.request("reply", request)).expired).toBe(true);
    await expect(f.service.request("candidate", request)).rejects.toThrow("Confirm your account");
    const renewed = await f.service.request("renew", request);
    expect(renewed.code).not.toBe(result.code);
    f.reply(result.code);
    expect((await f.service.request("reply", request)).account).toBeUndefined();
    f.reply(renewed.code);
    await f.service.request("channels", request);
    await expect(f.service.request("candidate", request)).rejects.toThrow("Confirm your account");
    await expect(f.service.request("reply", { ...request, expected: "other" })).rejects.toThrow("changed");
    await f.service.stop();
  });

  test("permission and delivery failures leave activation unavailable", async () => {
    const f = fixture();
    const { request: initialRequest } = await f.setup();
    f.revoke();
    await expect(f.service.request("test", initialRequest)).rejects.toThrow("permissions");
    await f.service.stop();
    const failed = fixture(); failed.fail();
    const draft = await failed.service.request("check-bot", { token: "test-secret", expected: "revision" });
    const request = { draft: draft.draft, expected: "revision", guild: "2", channel: "3" };
    await expect(failed.service.request("test", request)).rejects.toThrow("confirm delivery");
    const state = await failed.service.request("draft", request);
    failed.reply(state.code);
    await expect(failed.service.request("candidate", request)).rejects.toThrow("Confirm your account");
    await failed.service.stop();
  });

  test("a failed replacement keeps the active connection and closes its preview", async () => {
    const f = fixture();
    const policy = { enabled: true, bot_token: "test-secret", guild_id: "2", channel_id: "3", operator_ids: ["4"] };
    await f.service.request("apply", { policy });
    f.failReady();
    await expect(f.service.request("stage", { policy: { ...policy, operator_ids: ["7"] } })).rejects.toThrow("Gateway");
    expect(f.receivers[0].stopped).toBe(false);
    expect(f.receivers[1].stopped).toBe(true);
    expect((await f.service.request("status")).accounts).toEqual([{ id: "4", name: "Alex" }]);
    expect((await f.service.request("status")).status).toBe("connected");
    await f.service.stop();
  });

  test("a staged connection stays inactive until promotion and expires without promotion", async () => {
    const f = fixture();
    const policy = { enabled: true, bot_token: "test-secret", guild_id: "2", channel_id: "3", operator_ids: ["4"] };
    const first = await f.service.request("stage", { policy });
    expect(f.receivers[0].active).toBe(false);
    f.advance();
    await f.service.request("expire");
    expect(f.receivers[0].stopped).toBe(true);
    await f.service.request("stage", { policy });
    await f.service.request("discard-stage", first);
    expect(f.receivers[1].stopped).toBe(false);
    await f.service.request("apply", { policy });
    expect(f.receivers[1].active).toBe(true);
    expect(f.receivers[1].stopped).toBe(false);
    await f.service.stop();
  });

  test("a draft cannot change an active account list without explicit replacement", async () => {
    const f = fixture();
    await f.service.request("apply", { policy: { enabled: true, bot_token: "test-secret", guild_id: "2", channel_id: "3", operator_ids: ["4"] } });
    const { request, result } = await f.setup();
    f.reply(result.code, { author_id: "7" });
    await expect(f.service.request("candidate", request)).rejects.toThrow("replacement");
    expect((await f.service.request("candidate", { ...request, replace_accounts: true })).policy.operator_ids).toEqual(["7"]);
    expect((await f.service.request("status")).status).toBe("connected");
    await f.service.stop();
  });

  test("member overwrites follow combined role overwrites", () => {
    const channel = { permission_overwrites: [{ id: "2", type: 0, deny: "2048", allow: "0" },
      { id: "5", type: 0, deny: "0", allow: "2048" }, { id: "1", type: 1, deny: "2048", allow: "0" }] };
    expect(channelPermissions("2", "1", { roles: ["5"] }, [{ id: "2", permissions: "3072" }], channel) & 2048n).toBe(0n);
  });
});
