import { afterAll, beforeAll, expect, test } from "bun:test";
import { chmodSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { evidencePath, bootSmoke, gotoReady, mc, teardown, type Smoke } from "./harness";

const ROLES = ["Strategist", "Arbiter", "Worker", "Verifier", "Scribe", "Packager", "Homie"];
let smoke: Smoke;

// The state after setup with one Codex subscription: the chatgpt reference binding serves each role,
// and its connection holds a saved credential. The claude reference binding stays disabled.
beforeAll(async () => {
  smoke = await bootSmoke();
  const configPath = join(smoke.home, "config.toml");
  const before = readFileSync(configPath, "utf8");
  const modelSetting = /(\[runtimes\.bindings\.chatgpt\][^\[]*?\bmodel\s*=\s*)"[^"]*"/;
  expect(modelSetting.test(before)).toBe(true);
  writeFileSync(configPath, before.replace(modelSetting, '$1"fixture-model"'));
  const routing = join(smoke.scratch, "routing.codex.md");
  const rows = ROLES.map((role) => `| ${role.toLowerCase()} | codex | chatgpt |`).join("\n");
  writeFileSync(routing, `# Culler routing\n\n| role | harness | binding |\n| --- | --- | --- |\n${rows}\n`, { mode: 0o600 });
  await mc(["onboard", "routing", "--runtime-bindings", "chatgpt", "--routing-file", routing]);
  const config = Bun.TOML.parse(readFileSync(configPath, "utf8")) as any;
  const source = config.runtimes.credentials.chatgpt;
  const grants = join(smoke.home, "refresh-grants");
  mkdirSync(grants, { recursive: true });
  chmodSync(grants, 0o700);
  const grant = join(grants, "chatgpt.json");
  writeFileSync(grant, JSON.stringify({ credential: "chatgpt", channel: "codex", token_url: source.token_url, client_id: source.client_id, refresh_token: "smoke-refresh" }));
  chmodSync(grant, 0o600);
  // A smoke runs no resident, so the connection check comes from this status response.
  await smoke.page.route("**/api/system/status", (route) => route.fulfill({ json: {
    complete: true, observed_at: new Date().toISOString(), conditions: [],
    connections: [{ id: "connection:chatgpt:token_refresh:", group: "connections", name: "chatgpt", connection_id: "chatgpt", state: "ready" }],
  } }));
  await gotoReady(smoke);
}, 60000);

afterAll(async () => { await teardown(smoke); }, 30000);

test("after setup with one Codex subscription the panel lists one connected profile for every role", async () => {
  const { page } = smoke;
  const catalog = await mc(["agent-profile", "list"]) as any;
  expect(catalog.profiles.map((profile: any) => `${profile.id}:${profile.enabled}`).sort()).toEqual(["chatgpt:true", "claude:false"]);
  const reference = catalog.profiles.find((profile: any) => profile.id === "chatgpt");
  await page.getByTestId("tab-pipeline").click();
  await page.getByTestId("pipeline-profiles").click();
  const panel = page.getByTestId("agent-profiles-panel");
  const row = page.getByTestId("profile-card");
  await row.getByTestId("profile-status").filter({ hasText: "Connected" }).waitFor();
  expect(await panel.locator(".profile-count").innerText()).toBe("1 profile");
  expect(await row.count()).toBe(1);
  expect(await row.getByRole("heading", { level: 3 }).innerText()).toBe(reference.model);
  expect(await row.locator(".profile-connection").innerText()).toContain(reference.connection_name);
  expect(await row.getByTestId("profile-status").innerText()).toBe("Connected");
  expect(await row.locator(".profile-roles-label").innerText()).toBe("Used by");
  expect(await row.locator(".profile-role").allInnerTexts()).toEqual(ROLES);
  expect(await row.getByRole("button", { name: `Edit ${reference.name}`, exact: true }).isVisible()).toBe(true);
  expect(await panel.getByText("Disabled", { exact: true }).count()).toBe(0);
  await page.getByRole("button", { name: `Details for ${reference.name}`, exact: true }).click();
  expect(await row.locator(".profile-details").innerText()).not.toContain("Enabled");
  await panel.screenshot({ path: evidencePath("model-configuration-after-setup.png") });
  await page.getByTestId("profile-close").click();
}, 30000);

test("the create form offers only usable connections, both subscription integrations, and no endpoint values", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-pipeline").click();
  await page.getByTestId("pipeline-profiles").click();
  await page.getByTestId("profile-create").click();
  await page.getByTestId("profile-use-existing").click();
  expect(await page.getByTestId("profile-connection_id").locator("option").allInnerTexts()).toEqual(["Select a connection", "chatgpt · Credential saved"]);
  await page.getByTestId("profile-back").click();
  await page.getByTestId("profile-use-subscription").click();
  expect(await page.locator('[data-testid^="profile-sign-in-"]').evaluateAll((nodes) => nodes.map((node) => (node as HTMLElement).dataset.testid)))
    .toEqual(["profile-sign-in-chatgpt", "profile-sign-in-claude"]);
  await page.getByTestId("profile-back").click();
  for (const id of ["profile-use-endpoint", "profile-endpoint", "profile-token", "profile-no-token", "profile-base_url"]) {
    expect(await page.getByTestId(id).count()).toBe(0);
  }
  await page.getByTestId("profile-close").click();
}, 30000);

test("the create form states the next action when no connection has usable access", async () => {
  const { page } = smoke;
  // The removal of each saved credential leaves no connection with usable access.
  rmSync(join(smoke.home, "refresh-grants"), { recursive: true, force: true });
  const catalog = await mc(["agent-profile", "list"]) as any;
  expect(catalog.connections.length).toBeGreaterThan(0);
  expect(catalog.connections.filter((connection: any) => connection.access_status === "connected")).toEqual([]);
  await page.getByTestId("tab-pipeline").click();
  await page.getByTestId("pipeline-profiles").click();
  await page.getByTestId("profile-create").click();
  await page.getByTestId("profile-use-existing").click();
  const panel = page.getByTestId("agent-profiles-panel");
  await panel.getByText("No connection has usable access. Select Back to sign in to an account.", { exact: true }).waitFor();
  expect(await page.getByTestId("profile-connection_id").locator("option").allInnerTexts()).toEqual(["Select a connection"]);
  await page.getByTestId("profile-close").click();
}, 30000);
