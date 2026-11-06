import { el } from "./dom.js";

export function createDiscordPanel({ root, api, onAvailable }) {
  let snapshot, draft, stage = "intro", busy = false, error = "", notice = "", revision = "";
  let channels = [], guild = "", channel = "", reuse = false, replaceAccounts = false, disconnect = false;
  let restored = false;
  const storageKey = "culler-discord-draft";
  const paragraph = (text, props = {}) => el("p", { text, ...props });
  const button = (text, action, primary = false, props = {}) => el("button", {
    type: "button", class: `btn${primary ? " primary" : ""}`, text, disabled: busy, onclick: action, ...props,
  });
  const actions = (...children) => el("div", { class: "discord-actions" }, children);
  const field = (label, input, help) => el("label", { class: "discord-field" }, el("strong", { text: label }), input,
    help && el("small", { text: help }));
  const saveDraft = () => {
    try {
      if (draft) sessionStorage.setItem(storageKey, JSON.stringify({ id: draft.draft, revision, stage, guild, channel }));
      else sessionStorage.removeItem(storageKey);
    } catch { /* Setup can continue without browser storage. */ }
  };
  const post = (action, body = {}) => api(`/api/discord/${action}`, { method: "POST",
    body: JSON.stringify({ expected: revision || snapshot?.revision, draft: draft?.draft || "", ...body }) });

  async function perform(task) {
    if (busy) return;
    busy = true; error = ""; notice = "";
    for (const control of root.querySelectorAll("button, input, select")) control.disabled = true;
    const status = root.querySelector('[data-testid="discord-status"]');
    if (status) status.textContent = "Culler checks the Discord request…";
    try { await task(); } catch (failure) { error = failure.message || "The Discord request failed. Try again."; }
    finally { busy = false; saveDraft(); render(); }
  }

  async function chooseServer(value) {
    guild = value; channel = ""; channels = [];
    const result = await post("channels", { guild });
    draft = result; channels = result.channels ?? [];
    channel = channels.find(row => row.missing.length === 0)?.id ?? channels[0]?.id ?? "";
  }

  function start() {
    stage = "bot"; revision = snapshot.revision; reuse = snapshot.enabled;
    error = ""; notice = ""; disconnect = false; replaceAccounts = false; render();
  }

  async function cancel() {
    await perform(async () => { if (draft) await post("cancel"); draft = undefined; stage = "intro"; revision = ""; });
  }

  function setupBot() {
    const token = el("input", { class: "field", type: "password", autocomplete: "off", spellcheck: "false",
      testid: "discord-token", "aria-label": "Bot token", disabled: reuse });
    const keep = (snapshot.enabled || draft) && field("Stored bot token", el("input", { type: "checkbox", checked: reuse,
      "aria-label": "Keep current token", onchange: event => { reuse = event.target.checked; token.disabled = reuse; } }), "Keep the current token, or enter a replacement.");
    return [el("h4", { text: "Connect your Discord bot" }),
      paragraph("Create a bot in Discord, add it to your server, then enter its token here."), keep,
      field("Bot token", token, "Culler keeps the token on this Mac."),
      el("details", { class: "discord-help" }, el("summary", { text: "Create a bot" }),
        el("ol", {}, ["Create an application in the Discord Developer Portal.", "Open Bot and enable Message Content Intent.",
          "Add the bot to your server.", "Copy the bot token into Culler."].map(text => el("li", { text }))),
        paragraph("Give the bot these channel permissions:"),
        el("ul", {}, ["View Channel", "Send Messages", "Read Message History", "Attach Files", "Create Public Threads", "Send Messages in Threads"].map(text => el("li", { text }))),
        paragraph("Culler requires no Administrator permission."),
        el("a", { href: "https://discord.com/developers/applications", target: "_blank", rel: "noopener noreferrer", text: "Open Discord Developer Portal ↗" })),
      actions(button("Check bot", () => {
        const value = token.value.trim();
        void perform(async () => {
          draft = await post("check-bot", { token: reuse ? "" : value, reuse }); token.value = "";
          stage = "channel"; guild = snapshot.guild_id || draft.servers[0]?.id || "";
          if (guild && draft.servers.some(row => row.id === guild)) {
            await chooseServer(guild);
            if (channels.some(row => row.id === snapshot.channel_id)) channel = snapshot.channel_id;
          }
        });
      }, true), button("Cancel", cancel))];
  }

  function setupChannel() {
    const serverSelect = el("select", { class: "field", "aria-label": "Server", onchange: event => { void perform(() => chooseServer(event.target.value)); } },
      el("option", { value: "", text: "Select a server", selected: !guild }),
      (draft.servers ?? []).map(row => el("option", { value: row.id, selected: row.id === guild, text: `${row.name} (${row.id})` })));
    const channelSelect = el("select", { class: "field", "aria-label": "Channel", onchange: event => { channel = event.target.value; saveDraft(); render(); } },
      el("option", { value: "", text: "Select a channel", selected: !channel }),
      channels.map(row => el("option", { value: row.id, selected: row.id === channel,
        text: `#${row.name}${row.category ? ` · ${row.category}` : ""} (${row.id})` })));
    const missing = channels.find(row => row.id === channel)?.missing ?? [];
    return [el("span", { class: "tag tone-teal", text: `${draft.bot.name} verified` }),
      el("h4", { text: "Choose a channel" }), paragraph("Each message in this channel starts a Collie conversation in a new thread. Reply in that thread to continue."),
      field("Server", serverSelect), field("Channel", channelSelect, "Choose a channel where you can read and send messages."),
      missing.length > 0 && paragraph(`The channel requires these bot permissions: ${missing.join(", ")}.`, { class: "discord-warning" }),
      el("div", { class: "discord-refresh" },
        paragraph("If a server or channel is absent, add the bot or correct its permissions. Then refresh the list."),
        button("Refresh list", () => perform(async () => {
          const selected = channel;
          draft = await post("servers");
          if (guild && draft.servers.some(row => row.id === guild)) {
            await chooseServer(guild);
            if (channels.some(row => row.id === selected)) channel = selected;
          }
        }))),
      actions(button("Send test message", () => perform(async () => {
        draft = await post("test", { guild, channel }); stage = "account";
      }), true, { disabled: !guild || !channel || missing.length > 0 }),
      button("Back", () => { stage = "bot"; reuse = true; render(); }), button("Cancel", cancel))];
  }

  function setupAccount() {
    const account = draft.account;
    const previous = draft.allowed_accounts ?? [];
    const removes = previous.filter(row => row.id !== account?.id);
    const requiresReplacement = account && previous.length > 0 && !previous.some(row => row.id === account.id);
    const expired = draft.expired || Date.parse(draft.expires_at) <= Date.now();
    return [el("h4", { text: "Confirm your Discord account" }),
      paragraph(`Culler sent a test message to #${draft.channel.name}. Send this code in that channel from your account.`),
      draft.code && el("div", { class: "discord-code-row" },
        el("code", { class: "discord-code", testid: "discord-code", text: draft.code }),
        button("Copy code", async () => {
          try { await navigator.clipboard.writeText(draft.code); notice = "Code copied."; }
          catch { error = "Copy the code from the field."; }
          render();
        })),
      paragraph(expired ? "The code expired. Request a new code." : `The code expires at ${new Date(draft.expires_at).toLocaleTimeString()}. It remains valid for five minutes.`),
      account && paragraph(`Reply received from ${account.name} (${account.id}). Allow this account to talk to Collie?`, { class: "discord-notice" }),
      requiresReplacement && field("Replace the allowed accounts", el("input", { type: "checkbox", checked: replaceAccounts,
        "aria-label": "Replace the allowed accounts", onchange: event => { replaceAccounts = event.target.checked; render(); } }),
        `These accounts will lose access: ${removes.map(row => `${row.name} (${row.id})`).join(", ")}.`),
      actions(expired ? button("Get a new code", () => perform(async () => { draft = await post("renew"); }), true)
        : account ? button(`Allow ${account.name} and connect`, () => perform(async () => {
          snapshot = await post("connect", { replace_accounts: replaceAccounts });
          draft = undefined; revision = ""; stage = "intro"; notice = "Discord connected.";
        }), true, { disabled: requiresReplacement && !replaceAccounts })
        : button("Check for my reply", () => perform(async () => {
          draft = await post("reply"); if (!draft.account && !draft.expired) notice = "Culler has no reply yet. Send the code from your account, then check again.";
        }), true),
      button("Back", () => perform(async () => { await chooseServer(guild); stage = "channel"; })), button("Cancel", cancel))];
  }

  function connected() {
    const runtime = snapshot.runtime ?? {};
    const accountNames = runtime.accounts ?? snapshot.operator_ids.map(id => ({ id, name: `Name unavailable (${id})` }));
    const facts = [["Bot", runtime.bot?.name ?? "Name unavailable"], ["Server", runtime.server?.name ?? `Name unavailable (${snapshot.guild_id})`],
      ["Channel", runtime.channel?.name ? `#${runtime.channel.name}` : `Name unavailable (${snapshot.channel_id})`],
      ["Allowed account", accountNames.map(row => `${row.name} (${row.id})`).join(", ")]];
    const stale = runtime.checked_at && Date.now() - Date.parse(runtime.checked_at) > 120_000;
    const status = stale || runtime.status === "unavailable" ? "Status unavailable" : runtime.status === "connected" ? "Connected" : "Needs attention";
    const toggle = (key, label, help) => field(label, el("input", { type: "checkbox", checked: snapshot[key], "aria-label": label,
      onchange: event => { const value = event.target.checked; void perform(async () => {
        revision = snapshot.revision;
        snapshot = await post("alerts", { blocked: snapshot.blocked, health: snapshot.health, [key]: value }); revision = ""; notice = "Alerts saved.";
      }); } }), help);
    return [el("div", { class: "discord-card-head" }, el("h4", { text: "Discord connection" }),
      el("span", { class: `tag${status === "Connected" ? " tone-teal" : ""}`, testid: "discord-connection-status", text: status })),
      runtime.detail && paragraph(runtime.detail, { class: "discord-warning" }),
      stale && paragraph(`The last connection check was at ${new Date(runtime.checked_at).toLocaleString()}. Check the connection again.`),
      el("dl", { class: "discord-facts" }, facts.map(([label, value]) => el("div", {}, el("dt", { text: label }), el("dd", { text: value })))),
      paragraph("A channel message starts a new conversation. Continue in its thread or open the same conversation in the dashboard."),
      el("h5", { text: "Alerts" }), toggle("blocked", "Blocked tasks", "A task needs your attention before work can continue."),
      toggle("health", "System health", "Culler detects a problem that needs your attention."),
      actions(button("Send test message", () => perform(async () => {
        try { await post("test-current"); notice = "Test message delivered."; }
        finally { snapshot = await api("/api/discord"); }
      })),
        button("Check connection", () => perform(async () => {
          try { await post("check"); }
          finally { snapshot = await api("/api/discord"); }
          notice = snapshot.runtime?.status === "connected" ? "Discord connected. You can send a test message." : "";
        })),
        button("Edit connection", start), button("Disconnect", () => { disconnect = true; render(); })),
      disconnect && el("div", { class: "discord-warning" }, el("h5", { text: "Disconnect Discord?" }),
        paragraph(`Disconnect ${runtime.server?.name ?? snapshot.guild_id} / #${runtime.channel?.name ?? snapshot.channel_id}.`),
        paragraph("Culler will remove the bot token. You can continue conversations in the dashboard."),
        actions(button("Disconnect Discord", () => perform(async () => { snapshot = await post("disconnect", { confirm: true }); disconnect = false; })),
          button("Keep connected", () => { disconnect = false; render(); })))];
  }

  function render() {
    if (!snapshot) { root.replaceChildren(paragraph("Culler checks Discord availability…", { role: "status" })); return; }
    if (!snapshot.available) { root.replaceChildren(); return; }
    let content;
    if (stage === "bot") content = setupBot();
    else if (stage === "channel") content = setupChannel();
    else if (stage === "account") content = setupAccount();
    else if (snapshot.enabled) content = connected();
    else content = [el("div", { class: "discord-intro" }, el("div", {}, el("h4", { text: "Use Culler from Discord." }),
      paragraph("Use one channel for Collie conversations and alerts when Culler needs your attention."), button("Set up Discord", start, true)),
      el("div", { class: "discord-example", "aria-label": "Example Discord conversation" }, el("small", { text: "# culler · Example conversation" }),
        el("strong", { text: "Alex" }), paragraph("What needs my attention?"), el("strong", { text: "Collie" }),
        paragraph("The Website Project has one blocked task. Open Culler to review its question.")))];
    const steps = ["bot", "channel", "account"];
    root.replaceChildren(...[
      steps.includes(stage) && el("ol", { class: "discord-steps", "aria-label": "Discord setup steps" }, ["Connect bot", "Choose channel", "Confirm account"].map((text, index) =>
        el("li", { "aria-current": steps[index] === stage ? "step" : null, text: `${index + 1}. ${text}` }))),
      el("div", { class: "panelcard discord-card" }, content),
      el("p", { class: error ? "discord-warning" : "discord-notice", role: error ? "alert" : "status", testid: "discord-status", text: error || notice }),
      paragraph("One connection for this Culler installation. Culler must run on your Mac to use Discord.", { class: "system-helper" })].filter(Boolean));
  }

  async function refresh() {
    try {
      const next = await api("/api/discord");
      if (busy) return;
      const changed = JSON.stringify(snapshot) !== JSON.stringify(next);
      snapshot = next; onAvailable(Boolean(next.available));
      let first = false;
      if (!restored) {
        restored = true; first = true;
        let saved;
        try { saved = JSON.parse(sessionStorage.getItem(storageKey) || "null"); } catch { /* An absent draft starts normally. */ }
        if (saved?.id && snapshot.available) {
          revision = saved.revision;
          try {
            draft = await post("draft", { draft: saved.id }); stage = saved.stage;
            guild = saved.guild; channel = saved.channel;
            if (stage === "channel") { await chooseServer(guild); channel = saved.channel; }
          } catch { draft = undefined; stage = "intro"; revision = ""; notice = "The previous setup expired. Start Discord setup again."; saveDraft(); }
        }
      }
      if (first || (changed && stage === "intro") || !root.childElementCount) render();
      else if (draft && root.querySelector('[data-testid="discord-code"]') === null && stage === "account") render();
    } catch (failure) { if (!snapshot) { error = failure.message; root.replaceChildren(paragraph(error, { role: "alert" })); } }
  }
  render();
  return { refresh };
}
