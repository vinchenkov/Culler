import { afterAll, beforeAll, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { DiscordConfiguration, serveDiscordConfiguration } from "../../resident/src/discord-config";
import type { DiscordInboundMessage } from "../../resident/src/discord";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke, service: DiscordConfiguration, socket: Awaited<ReturnType<typeof serveDiscordConfiguration>>;
let observe: ((message: DiscordInboundMessage) => void) | undefined;
let failDelivery = false;
let deliveryDetail = "";
const posts: unknown[] = [];

beforeAll(async () => {
  smoke = await bootSmoke();
  const api = async (token: string, path: string, body?: unknown) => {
    if (token !== "smoke-discord-secret") throw new Error("The bot token is invalid. Copy a current token from Discord.");
    if (path === "/users/@me") return { id: "1", username: "Culler", bot: true };
    if (path.startsWith("/users/@me/guilds")) return [{ id: "2", name: "Personal workspace" }];
    if (path === "/guilds/2") return { id: "2", name: "Personal workspace" };
    if (path === "/guilds/2/members/1") return { roles: [] };
    if (path === "/guilds/2/roles") return [{ id: "2", permissions: "8" }];
    const channel = { id: "3", name: "culler", guild_id: "2", type: 0, permission_overwrites: [] };
    if (path === "/guilds/2/channels") return [channel];
    if (path === "/channels/3") return channel;
    if (path === "/users/4") return { id: "4", username: "Alex" };
    if (path === "/channels/3/messages") {
      if (failDelivery) throw new Error("Discord did not confirm delivery. Try again.");
      posts.push(body); return { id: "5" };
    }
    throw new Error("The test received an unexpected Discord request.");
  };
  service = new DiscordConfiguration(api, async (_policy, options) => {
    if (options?.observe) observe = options.observe;
    let stopped = false;
    return { async stop() { stopped = true; }, async ready() {}, activate() {},
      health() { return { connected: !stopped, detail: deliveryDetail, checked_at: new Date().toISOString() }; } };
  });
  socket = await serveDiscordConfiguration(smoke.home, service);
  await gotoReady(smoke);
}, 60000);
afterAll(async () => { await socket?.stop(); await service?.stop(); if (smoke) await teardown(smoke); });

test("Discord appears only after complete Project setup", async () => {
  const { page, server } = smoke;
  await page.goto(`${server.url}#configs/discord`);
  await page.getByTestId("config-section-system").waitFor();
  expect(await page.getByTestId("config-nav-discord").isVisible()).toBe(false);
  expect((await mc(["discord", "show"])).available).toBe(false);
  await seedProject(smoke, "discord-ready", { environmentReady: true });
  await page.reload();
  await page.getByTestId("config-nav-discord").waitFor();
  expect(await page.getByTestId("config-section-system").isVisible()).toBe(true);
  await page.getByTestId("config-nav-discord").click();
  await page.getByRole("button", { name: "Set up Discord", exact: true }).waitFor();
  expect(await page.locator(".config-nav a:visible svg").count()).toBe(5);
}, 30000);

test("the real host commands validate, confirm, activate, and disconnect Discord", async () => {
  const { page } = smoke;
  await page.getByRole("button", { name: "Set up Discord", exact: true }).click();
  await page.getByTestId("discord-token").fill("invalid");
  await page.getByRole("button", { name: "Check bot", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "bot token is invalid" }).waitFor().catch(async () => {
    throw new Error(`The token check showed: ${await page.getByTestId("discord-content").innerText()}`);
  });
  await page.getByTestId("discord-token").fill("smoke-discord-secret");
  await page.getByRole("button", { name: "Check bot", exact: true }).click();
  await page.getByLabel("Channel", { exact: true }).waitFor();
  expect(await page.getByRole("button", { name: "Refresh list", exact: true }).count()).toBe(1);
  await page.screenshot({ path: evidencePath("discord-channel-step.png"), fullPage: true });
  expect(posts).toHaveLength(0);
  failDelivery = true;
  await page.getByRole("button", { name: "Send test message", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "did not confirm delivery" }).waitFor();
  failDelivery = false;
  await page.getByRole("button", { name: "Send test message", exact: true }).click();
  const code = await page.getByTestId("discord-code").textContent();
  expect(posts).toHaveLength(1);
  expect((await mc(["discord", "show"])).enabled).toBe(false);
  await page.reload();
  await page.getByTestId("discord-code").waitFor();
  expect(await page.getByTestId("discord-code").textContent()).toBe(code);
  expect(code).toMatch(/^[0-9A-F]{16}$/);
  await page.screenshot({ path: evidencePath("discord-account-step.png"), fullPage: true });
  observe?.({ message_id: "8", author_id: "4", author_name: "Alex", guild_id: "2", channel_id: "3", content: code!, author_is_bot: false });
  await page.getByRole("button", { name: "Check for my reply", exact: true }).click();
  await page.getByRole("button", { name: "Allow Alex and connect", exact: true }).click();
  await page.getByTestId("discord-connection-status").filter({ hasText: "Connected" }).waitFor();
  expect(await page.getByTestId("discord-content").getByText("The Discord connection is inactive.", { exact: false }).count()).toBe(0);
  deliveryDetail = "A queued Discord message failed.";
  await page.getByRole("button", { name: "Check connection", exact: true }).click();
  await page.getByTestId("discord-connection-status").filter({ hasText: "Needs attention" }).waitFor();
  await page.getByRole("button", { name: "Send test message", exact: true }).click();
  await page.getByTestId("discord-status").filter({ hasText: "Test message delivered." }).waitFor();
  expect(posts).toHaveLength(2);
  deliveryDetail = "";
  await page.getByRole("button", { name: "Check connection", exact: true }).click();
  await page.getByTestId("discord-status").filter({ hasText: "Discord connected. You can send a test message." }).waitFor();
  const view = await mc(["discord", "show"]);
  expect(JSON.stringify(view)).not.toContain("smoke-discord-secret");
  expect(readFileSync(join(smoke.home, "config.toml"), "utf8")).toContain("smoke-discord-secret");
  await page.getByRole("checkbox", { name: "System health", exact: true }).uncheck();
  await page.getByTestId("discord-status").filter({ hasText: "Alerts saved." }).waitFor();
  expect((await mc(["discord", "show"])).health).toBe(false);
  for (const theme of ["light", "dark"]) {
    await page.evaluate(value => { localStorage.setItem("mc.theme", value); document.documentElement.dataset.theme = value; }, theme);
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ path: evidencePath(`discord-${theme}-${width}.png`), fullPage: true });
    }
  }
  await page.getByRole("button", { name: "Edit connection", exact: true }).click();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  expect((await mc(["discord", "show"])).enabled).toBe(true);
  await page.getByRole("button", { name: "Disconnect", exact: true }).click();
  await page.getByRole("button", { name: "Disconnect Discord", exact: true }).click();
  await page.getByRole("button", { name: "Set up Discord", exact: true }).waitFor();
  const disconnected = await mc(["discord", "show"]);
  expect(disconnected.enabled).toBe(false);
  expect(disconnected.available).toBe(true);
  expect(readFileSync(join(smoke.home, "config.toml"), "utf8")).not.toContain("smoke-discord-secret");
  expect(await page.getByTestId("config-nav-discord").isVisible()).toBe(true);
}, 60000);
