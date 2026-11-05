import { randomBytes } from "node:crypto";
import { chmod, lstat, rm } from "node:fs/promises";
import { join } from "node:path";
import type { Logger } from "../../runner/shared/log";
import type { Exec } from "./types";
import { startDiscordSurface, validateDiscordPolicy, type DiscordPolicy, type DiscordSurfaceHandle, type DiscordInboundMessage } from "./discord";

type Named = { id: string; name: string };
type Request = { expected?: string; draft?: string; token?: string; reuse?: boolean; guild?: string; channel?: string; replace_accounts?: boolean };
type API = (token: string, path: string, body?: unknown) => Promise<any>;
type Open = (policy: DiscordPolicy, options?: { preview?: boolean; observe?: (message: DiscordInboundMessage) => void }) => Promise<DiscordSurfaceHandle>;
type Draft = {
  id: string; token: string; bot: Named; servers: Named[]; expires: number;
  revision?: string;
  guild?: Named; channel?: Named; code?: string; codeExpires?: number;
  account?: Named; receiver?: DiscordSurfaceHandle; nonce?: string; delivered?: boolean;
};

const emptyPolicy = (): DiscordPolicy => ({ enabled: false, bot_token: "", guild_id: "", channel_id: "", operator_ids: [] });
const validID = (value: unknown): value is string => typeof value === "string" && /^[0-9]{1,20}$/.test(value);
const named = (value: any): Named => ({ id: value.id, name: value.name || value.username || `Name unavailable (${value.id})` });
const permissionNames = new Map<bigint, string>([
  [1n << 10n, "View Channel"], [1n << 11n, "Send Messages"], [1n << 15n, "Attach Files"],
  [1n << 16n, "Read Message History"], [1n << 35n, "Create Public Threads"], [1n << 38n, "Send Messages in Threads"],
]);

export async function discordAPI(token: string, path: string, body?: unknown): Promise<any> {
  let response: Response;
  try {
    response = await fetch(`https://discord.com/api/v10${path}`, {
      method: body === undefined ? "GET" : "POST",
      headers: { authorization: `Bot ${token}`, "content-type": "application/json" },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(10_000),
    });
  } catch { throw new Error("Discord did not respond. Check the network and try again."); }
  if (response.status === 401) throw new Error("The bot token is invalid. Copy a current token from Discord.");
  if (response.status === 403) throw new Error("Discord refused access. Check the bot's channel permissions.");
  if (response.status === 404) throw new Error("Discord could not find the destination. Add the bot to the server and refresh.");
  if (response.status === 429) throw new Error("Discord limited the request rate. Wait before you try again.");
  if (!response.ok) throw new Error("Discord could not complete the request. Try again.");
  try { return await response.json(); } catch { throw new Error("Discord returned an invalid result. Try again."); }
}

export function channelPermissions(guild: string, bot: string, member: any, roles: any[], channel: any): bigint {
  const roleIDs = new Set<string>([guild, ...(member.roles ?? [])]);
  let permissions = roles.filter(role => roleIDs.has(role.id)).reduce((bits, role) => bits | BigInt(role.permissions), 0n);
  if (permissions & 8n) return ~0n;
  const overwrites = channel.permission_overwrites ?? [];
  const apply = (items: any[]) => {
    let allow = 0n, deny = 0n;
    for (const item of items) { allow |= BigInt(item.allow); deny |= BigInt(item.deny); }
    permissions = (permissions & ~deny) | allow;
  };
  apply(overwrites.filter((row: any) => row.id === guild));
  apply(overwrites.filter((row: any) => row.type === 0 && row.id !== guild && roleIDs.has(row.id)));
  apply(overwrites.filter((row: any) => row.type === 1 && row.id === bot));
  return permissions;
}

export class DiscordConfiguration {
  private policy = emptyPolicy();
  private active?: DiscordSurfaceHandle;
  private drafts = new Map<string, Draft>();
  private facts: { bot?: Named; server?: Named; channel?: Named; accounts?: Named[] } = {};
  private problem = "";
  private staged?: { id: string; policy: DiscordPolicy; surface: DiscordSurfaceHandle; facts: typeof this.facts; expires: number };
  private serial: Promise<unknown> = Promise.resolve();

  constructor(private readonly api: API, private readonly open: Open, private readonly now = () => Date.now()) {}

  request(action: string, request: any = {}): Promise<any> {
    const operation = this.serial.catch(() => {}).then(() => this.handle(action, request ?? {}));
    this.serial = operation;
    return operation;
  }

  private async remove(draft: Draft) {
    this.drafts.delete(draft.id);
    draft.token = "";
    draft.code = undefined;
    await draft.receiver?.stop();
  }

  private async expire() {
    if (this.staged && this.staged.expires <= this.now()) {
      await this.staged.surface.stop(); this.staged = undefined;
    }
    for (const draft of this.drafts.values()) {
      if (draft.expires <= this.now()) await this.remove(draft);
      else if (draft.codeExpires && draft.codeExpires <= this.now()) {
        draft.code = undefined; draft.account = undefined;
        await draft.receiver?.stop(); draft.receiver = undefined;
      }
    }
  }

  private draft(request: Request): Draft {
    const draft = this.drafts.get(request.draft ?? "");
    if (!draft) throw new Error("The setup draft expired. Start Discord setup again.");
    return draft;
  }

  private view(draft: Draft) {
    return {
      draft: draft.id, bot: draft.bot, servers: draft.servers, server: draft.guild, channel: draft.channel,
      code: draft.code, expires_at: draft.codeExpires ? new Date(draft.codeExpires).toISOString() : null,
      account: draft.account, expired: Boolean(draft.codeExpires && draft.codeExpires <= this.now()),
      allowed_accounts: this.facts.accounts ?? this.policy.operator_ids.map(id => ({ id, name: id })),
    };
  }

  private async channels(draft: Draft, guild: string) {
    if (!validID(guild) || !draft.servers.some(server => server.id === guild)) throw new Error("Select a server that contains the bot.");
    const [channels, roles, member] = await Promise.all([
      this.api(draft.token, `/guilds/${guild}/channels`), this.api(draft.token, `/guilds/${guild}/roles`),
      this.api(draft.token, `/guilds/${guild}/members/${draft.bot.id}`),
    ]);
    return channels.filter((channel: any) => channel.type === 0).map((channel: any) => {
      const bits = channelPermissions(guild, draft.bot.id, member, roles, channel);
      const missing = [...permissionNames].filter(([bit]) => (bits & bit) === 0n).map(([, name]) => name);
      return { ...named(channel), missing, category: channels.find((parent: any) => parent.id === channel.parent_id)?.name ?? "" };
    }).filter((channel: any) => !channel.missing.includes("View Channel"));
  }

  private observe(draft: Draft, message: DiscordInboundMessage) {
    if (this.drafts.get(draft.id) !== draft || !draft.code || draft.account || (draft.codeExpires ?? 0) <= this.now()) return;
    if (message.guild_id !== draft.guild?.id || message.channel_id !== draft.channel?.id || message.author_is_bot) return;
    if (!validID(message.author_id) || message.content.trim().toUpperCase() !== draft.code) return;
    draft.account = { id: message.author_id, name: message.author_name || message.author_id };
  }

  private async receiver(draft: Draft) {
    await draft.receiver?.stop();
    draft.account = undefined;
    draft.code = randomBytes(8).toString("hex").toUpperCase();
    draft.codeExpires = this.now() + 5 * 60_000;
    draft.receiver = await this.open({ enabled: true, bot_token: draft.token, guild_id: draft.guild!.id,
      channel_id: draft.channel!.id, operator_ids: [] }, { preview: true, observe: message => this.observe(draft, message) });
    try { await draft.receiver.ready(); }
    catch { await draft.receiver.stop(); draft.receiver = undefined; throw new Error("The bot cannot read messages. Enable Message Content Intent and check the channel permissions."); }
  }

  private async send(token: string, channel: string, nonce: string) {
    await this.api(token, `/channels/${channel}/messages`, { content: "Culler test message. The Discord channel can receive Culler messages.",
      allowed_mentions: { parse: [] }, nonce, enforce_nonce: true });
  }

  private async details(policy: DiscordPolicy) {
    const [bot, guild, channel, ...accounts] = await Promise.all([
      this.api(policy.bot_token, "/users/@me"), this.api(policy.bot_token, `/guilds/${policy.guild_id}`),
      this.api(policy.bot_token, `/channels/${policy.channel_id}`),
      ...policy.operator_ids.map(id => this.api(policy.bot_token, `/users/${id}`).catch(() => ({ id }))),
    ]);
    if (channel.guild_id !== policy.guild_id || channel.type !== 0) throw new Error("Select a text channel in the configured server.");
    const draft: Draft = { id: "", token: policy.bot_token, bot: named(bot), servers: [named(guild)], expires: 0 };
    const choices = await this.channels(draft, policy.guild_id);
    const choice = choices.find((item: any) => item.id === policy.channel_id);
    if (!choice) throw new Error("Give the bot permission to view the configured channel.");
    if (choice.missing.length) throw new Error(`The channel requires these bot permissions: ${choice.missing.join(", ")}.`);
    this.facts = { bot: named(bot), server: named(guild), channel: named(channel), accounts: accounts.map(named) };
  }

  private async handle(action: string, request: any): Promise<any> {
    await this.expire();
    if (action === "expire") return {};
    if (action === "status") {
      const health = this.active?.health();
      return { ...this.facts, status: !this.policy.enabled ? "disconnected" : health?.connected && !health.detail && !this.problem ? "connected" : "attention",
        detail: this.problem || health?.detail || (this.policy.enabled && !health?.connected ? "The Discord connection is inactive. Check the connection again." : ""),
        checked_at: health?.checked_at ?? null };
    }
    if (action === "quiesce") { await this.active?.stop(); this.active = undefined; return {}; }
    if (action === "discard-stage") {
      if (this.staged && this.staged.id === request.stage) { await this.staged.surface.stop(); this.staged = undefined; }
      return {};
    }
    if (action === "stage") {
      await this.staged?.surface.stop(); this.staged = undefined;
      const policy = validateDiscordPolicy(request.policy);
      if (!policy) throw new Error("The Discord policy is required.");
      const previousFacts = this.facts;
      let surface: DiscordSurfaceHandle | undefined;
      try {
        await this.details(policy);
        surface = await this.open(policy, { preview: true });
        await surface.ready();
        this.staged = { id: randomBytes(24).toString("hex"), policy, surface, facts: this.facts, expires: this.now() + 60_000 };
      } catch (error) { await surface?.stop(); throw error; }
      finally { this.facts = previousFacts; }
      return { stage: this.staged.id };
    }
    if (action === "apply") {
      const policy = validateDiscordPolicy(request.policy);
      if (!policy) throw new Error("The Discord policy is required.");
      if (this.staged && JSON.stringify(policy) === JSON.stringify(this.staged.policy)) {
        await this.active?.stop();
        this.active = this.staged.surface; this.facts = this.staged.facts;
        this.policy = structuredClone(policy); this.problem = ""; this.staged = undefined;
        this.active.activate();
        return {};
      }
      if (JSON.stringify(policy) === JSON.stringify(this.policy) && this.active?.health().connected) return {};
      await this.active?.stop(); this.active = undefined;
      this.policy = structuredClone(policy); this.problem = "";
      if (!policy.enabled) {
        this.facts = {};
        for (const draft of [...this.drafts.values()]) await this.remove(draft);
        return {};
      }
      try {
        await this.details(policy);
        this.active = await this.open(policy);
        await this.active.ready();
      } catch (error) {
        await this.active?.stop(); this.active = undefined;
        this.problem = error instanceof Error ? error.message : "The Discord connection failed.";
        throw new Error(this.problem);
      }
      return {};
    }
    if (action === "check") {
      if (this.policy.enabled) {
        if (!this.active?.health().connected) await this.handle("apply", { policy: this.policy });
        else await this.details(this.policy);
      }
      return this.handle("status", {});
    }
    if (action === "test-current") {
      if (!this.policy.enabled || !this.active?.health().connected) throw new Error(this.problem || this.active?.health().detail || "The Discord connection is inactive. Check the connection again.");
      await this.send(this.policy.bot_token, this.policy.channel_id, randomBytes(12).toString("hex"));
      return { delivered: true };
    }
    if (action === "check-bot") {
      if (this.drafts.size >= 8) throw new Error("Cancel another Discord setup draft before you start a new one.");
      const token = request.reuse ? (this.drafts.get(request.draft)?.token ?? this.policy.bot_token) : request.token;
      if (typeof token !== "string" || !token.trim() || token.length > 512 || /\s/.test(token)) throw new Error("Enter a valid bot token.");
      const bot = await this.api(token, "/users/@me");
      if (!bot.bot || !validID(bot.id)) throw new Error("The token must belong to a Discord bot.");
      const servers: Named[] = [];
      let after = "";
      for (let page = 0; page < 5; page++) {
        const rows = await this.api(token, `/users/@me/guilds?limit=200${after ? `&after=${after}` : ""}`);
        if (!Array.isArray(rows)) throw new Error("Discord returned an invalid server list.");
        servers.push(...rows.filter(row => validID(row.id)).map(named));
        if (rows.length < 200) break;
        after = rows.at(-1).id;
      }
      const draft: Draft = { id: randomBytes(24).toString("hex"), token, bot: named(bot), servers, expires: this.now() + 15 * 60_000, revision: request.expected };
      if (request.draft && this.drafts.has(request.draft)) await this.remove(this.draft(request));
      this.drafts.set(draft.id, draft);
      return this.view(draft);
    }
    if (action === "cancel") {
      if (this.drafts.has(request.draft)) await this.remove(this.draft(request));
      return {};
    }
    const draft = this.draft(request);
    if (draft.revision !== request.expected) throw new Error("The Discord configuration changed. Start setup again.");
    if (action === "draft" || action === "reply") return this.view(draft);
    if (action === "servers") {
      const rows = await this.api(draft.token, "/users/@me/guilds?limit=200");
      draft.servers = rows.filter((row: any) => validID(row.id)).map(named);
      return this.view(draft);
    }
    if (action === "channels") {
      await draft.receiver?.stop(); draft.receiver = undefined;
      draft.code = undefined; draft.codeExpires = undefined; draft.account = undefined; draft.channel = undefined; draft.delivered = false;
      const channels = await this.channels(draft, request.guild);
      draft.guild = draft.servers.find(server => server.id === request.guild);
      return { ...this.view(draft), channels };
    }
    if (action === "test") {
      const channels = await this.channels(draft, request.guild);
      const channel = channels.find((row: any) => row.id === request.channel);
      if (!channel) throw new Error("Select a channel that the bot can view.");
      if (channel.missing.length) throw new Error(`The channel requires these bot permissions: ${channel.missing.join(", ")}.`);
      const repeated = draft.channel?.id === channel.id && draft.code && draft.receiver?.health().connected;
      draft.guild = draft.servers.find(server => server.id === request.guild);
      draft.channel = { id: channel.id, name: channel.name };
      if (!repeated) {
        draft.delivered = false;
        draft.nonce = randomBytes(12).toString("hex");
        await this.receiver(draft);
      }
      await this.send(draft.token, channel.id, draft.nonce!);
      draft.delivered = true;
      return { ...this.view(draft), delivered: true };
    }
    if (action === "renew") {
      if (!draft.channel || !draft.guild || !draft.delivered) throw new Error("Select a channel and send a test message first.");
      await this.receiver(draft);
      return this.view(draft);
    }
    if (action === "candidate") {
      if (!draft.delivered || !draft.account || !draft.code || (draft.codeExpires ?? 0) <= this.now() || !draft.receiver?.health().connected) {
        throw new Error("Confirm your account with a current code before you connect.");
      }
      const accounts = this.policy.enabled && !request.replace_accounts ? this.policy.operator_ids : [draft.account.id];
      if (!accounts.includes(draft.account.id)) throw new Error("Confirm the account replacement before you connect.");
      return { policy: { enabled: true, bot_token: draft.token, guild_id: draft.guild!.id,
        channel_id: draft.channel!.id, operator_ids: accounts } };
    }
    throw new Error("The Discord action is unsupported.");
  }

  async stop() {
    await this.serial.catch(() => {});
    await this.staged?.surface.stop(); this.staged = undefined;
    await this.active?.stop(); this.active = undefined;
    for (const draft of [...this.drafts.values()]) await this.remove(draft);
  }
}

export async function startDiscordConfiguration(home: string, runMc: Exec, log: Logger, nudge: () => void) {
  const configuration = new DiscordConfiguration(discordAPI,
    (policy, options) => startDiscordSurface(policy, runMc, log, home, nudge, options));
  try {
    const result = await runMc(["__discord-config"]);
    if (result.exitCode !== 0) throw new Error("The Discord configuration is unavailable.");
    await configuration.request("apply", { policy: JSON.parse(result.stdout) });
  } catch {
    log.warn("discord.disconnected", "The Discord connection needs attention. Open Configs to check it.");
  }
  const server = await serveDiscordConfiguration(home, configuration);
  const timer = setInterval(() => { void configuration.request("expire").catch(() => {}); }, 30_000);
  return { async stop() { clearInterval(timer); await server.stop(); await configuration.stop(); } };
}

export async function serveDiscordConfiguration(home: string, configuration: Pick<DiscordConfiguration, "request">) {
  const path = join(home, "discord.socket");
  try {
    const existing = await lstat(path);
    if (!existing.isSocket() || existing.uid !== process.getuid?.()) throw new Error("The Discord socket path is unsafe.");
    await rm(path);
  } catch (error: any) { if (error.code !== "ENOENT") throw error; }
  const states = new WeakMap<object, { body: string; done: boolean }>();
  const server = Bun.listen({ unix: path, socket: {
    open(socket) { states.set(socket, { body: "", done: false }); socket.timeout(75); },
    data(socket, data) {
      const state = states.get(socket);
      if (!state || state.done) return;
      state.body += Buffer.from(data).toString("utf8");
      if (Buffer.byteLength(state.body) > 64 * 1024) { state.done = true; socket.end(); return; }
      if (!state.body.includes("\n")) return;
      state.done = true;
      void (async () => {
        let result;
        try {
          const { action, request } = JSON.parse(state.body);
          result = await configuration.request(action, request);
        } catch (error) { result = { error: error instanceof Error ? error.message : "The Discord operation failed." }; }
        socket.end(JSON.stringify(result) + "\n");
      })();
    },
    error(socket) { socket.end(); },
    timeout(socket) { socket.end(); },
  } });
  await chmod(path, 0o600);
  return { async stop() { server.stop(true); await rm(path, { force: true }); } };
}
