import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, MC_BIN, seedProject, teardown, type Smoke } from "../../dashboard/smoke/harness";
import { createFailureRepairFixture, type ProviderFault } from "./failure-repair-fixture";
import { runNativeHarness } from "./native-harness";
import { dashboardState, installFixtureRunner, recordFixtureAvailability, refreshDashboard, retainDashboardEvidence, testAndSaveProfileInDashboard } from "./dashboard-assertions";

const image = process.env.CULLER_PROXY_TEST_IMAGE ?? process.env.MC_BOUNDARY_IMAGE;
if (!image) throw new Error("Set CULLER_PROXY_TEST_IMAGE to the image with the pinned release harnesses.");
const connections = [
  { id: "synthetic-codex-origin", harness: "codex" as const, template: "codex-api-key" },
  { id: "synthetic-claude-origin", harness: "claude-sdk" as const, template: "claude-api-key" },
];
let smoke: Smoke;
let fixture: Awaited<ReturnType<typeof createFailureRepairFixture>>;
const profiles = new Map<string, { id: string; name: string; revision: string }>();
const previousStore = process.env.CULLER_TEST_HOST_SECRET_DIR;
const previousImage = process.env.CULLER_TEST_PROVIDER_IMAGE;

async function modelPanel() {
  await smoke.page.getByTestId("tab-pipeline").click();
  await smoke.page.getByRole("button", { name: "Model configuration", exact: true }).click();
}

async function retrySelectedModel() {
  const { page } = smoke;
  await page.getByTestId("profile-test").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="profile-save"]') as HTMLButtonElement)?.disabled,
    undefined, { timeout: 90000 });
  const saved = page.waitForResponse(response => response.url().endsWith("/api/agent-profiles/save") && response.request().method() === "POST");
  await page.getByTestId("profile-save").click();
  expect((await saved).ok()).toBe(true);
  await page.getByTestId("profile-create").waitFor();
  await page.keyboard.press("Escape");
}

beforeAll(async () => {
  process.env.CULLER_TEST_PROVIDER_IMAGE = image;
  smoke = await bootSmoke();
  await installFixtureRunner(smoke);
  process.env.CULLER_TEST_HOST_SECRET_DIR = join(smoke.stateScratch, "host-secrets");
  await seedProject(smoke, "provider-origin", { title: "Provider origin" });
  for (const connection of connections) {
    const catalog = await mc(["agent-profile", "list"]);
    await mc(["agent-profile", "connection-save"], {}, { id: connection.id, name: connection.id,
      template_id: connection.template, api_key: `synthetic-${connection.harness}-origin-key`,
      revision: catalog.revision, submission_id: crypto.randomUUID() });
  }
  fixture = await createFailureRepairFixture({ home: smoke.home, mcBinary: MC_BIN, runMc: (argv, input) => mc(argv, {}, input), connections });
  recordFixtureAvailability(smoke);
  await gotoReady(smoke);
  for (const connection of connections) {
    await modelPanel();
    await smoke.page.getByTestId("profile-create").click();
    await smoke.page.getByTestId("profile-use-existing").click();
    await smoke.page.getByTestId("profile-connection_id").selectOption(connection.id);
    const name = `${connection.harness} origin profile`;
    const saved = await testAndSaveProfileInDashboard(smoke, name);
    await fixture.flush();
    const observation = fixture.observations().findLast(row => row.profile_id === saved.profile.id && row.origin === "provider");
    expect(observation?.profile_revision).toBeString();
    profiles.set(connection.id, { id: saved.profile.id, name, revision: String(observation!.profile_revision) });
  }
}, 240000);

afterAll(async () => {
  await fixture?.stop();
  if (previousStore === undefined) delete process.env.CULLER_TEST_HOST_SECRET_DIR;
  else process.env.CULLER_TEST_HOST_SECRET_DIR = previousStore;
  if (previousImage === undefined) delete process.env.CULLER_TEST_PROVIDER_IMAGE;
  else process.env.CULLER_TEST_PROVIDER_IMAGE = previousImage;
  await teardown(smoke);
}, 30000);

afterEach(async () => { await smoke.page.keyboard.press("Escape"); await smoke.page.keyboard.press("Escape"); });

const cases: { fault: ProviderFault; origin: string; status: number; providerRequests: number }[] = [
  { fault: "rate_limit", origin: "provider", status: 429, providerRequests: 1 },
  { fault: "overload", origin: "provider", status: 503, providerRequests: 1 },
  { fault: "store", origin: "credential_store", status: 503, providerRequests: 0 },
  { fault: "proxy", origin: "proxy", status: 503, providerRequests: 0 },
];

for (const connection of connections) for (const fault of cases) test(`${connection.harness} ${fault.fault} preserves account health and recovers through the dashboard`, async () => {
  const profile = profiles.get(connection.id)!;
  const before = await dashboardState(smoke);
  const account = before.status.connections.find((row: any) => row.connection_id === connection.id && !row.profile_id);
  expect(account.state).toBe("ready");
  const start = fixture.requests(connection.id);
  const observationsStart = fixture.observations().length;
  fixture.setFault(connection.id, fault.fault);
  const failed = await runNativeHarness({ image, access: await fixture.authorize(connection.id, {
    model: "fixture-model", profile: profile.id, profileRevision: profile.revision,
  }), model: "fixture-model", prompt: "Return the controlled provider response without tools." });
  await fixture.flush();
  expect(failed.exitCode).not.toBe(0);
  expect(fixture.requests(connection.id) - start).toBe(fault.providerRequests);
  const evidence = fixture.observations().slice(observationsStart);
  expect(evidence.some(row => row.origin === fault.origin && row.http_status === fault.status && row.provider_sent === (fault.providerRequests > 0))).toBe(true);
  const state = await dashboardState(smoke);
  const currentAccount = state.status.connections.find((row: any) => row.connection_id === connection.id && !row.profile_id);
  expect(currentAccount.state).toBe("ready");
  expect(currentAccount.failure_count).toBe(account.failure_count);
  expect(currentAccount.verified_at).toBe(account.verified_at);
  expect(state.status.conditions.filter((row: any) => row.connection_id === connection.id).some((row: any) => ["Reconnect", "Replace API key", "Review account allowance"].includes(row.action))).toBe(false);
  if (fault.origin === "provider") {
    const condition = state.status.conditions.find((row: any) => row.profile_id === profile.id);
    expect(condition?.state).toBe("retry_wait");
    expect(condition?.http_status).toBe(fault.status);
    expect(evidence.filter(row => row.origin === "proxy_limit").every(row => row.provider_sent === false)).toBe(true);
  } else {
    expect(state.status.conditions.some((row: any) => row.origin === fault.origin && row.connection_id === connection.id)).toBe(true);
  }
  await refreshDashboard(smoke);
  await smoke.page.getByTestId("system-notices").getByRole("button", { name: /^(Review issues|View details)$/ }).click();
  const issue = smoke.page.getByRole("dialog", { name: "Issue details", exact: true });
  await issue.waitFor();
  const visible = await issue.innerText();
  expect(visible).toMatch(fault.origin === "provider" ? /Connection interrupted|Retry pending/
    : fault.origin === "credential_store" ? /Unlock the login Keychain/ : /repair host provider access/);
  await smoke.page.screenshot({ path: evidencePath(`${connection.id}-${fault.fault}-failure.png`) });
  retainDashboardEvidence(`${connection.id}-${fault.fault}-failure`, { state, observations: evidence, failure: failed.failure });
  await smoke.page.keyboard.press("Escape");
  await modelPanel();
  await smoke.page.getByTestId(`profile-edit-${profile.id}`).click();
  await smoke.page.getByTestId("profile-test").click();
  await smoke.page.waitForFunction(() => document.querySelector('[data-testid="profile-operation"]')?.textContent?.includes("The model test failed"),
    undefined, { timeout: 90000 });
  await fixture.flush();
  const failedModelTest = await dashboardState(smoke);
  expect(failedModelTest.status.connections.find((row: any) => row.connection_id === connection.id && !row.profile_id)?.state).toBe("ready");
  expect(failedModelTest.status.connections.find((row: any) => row.profile_id === profile.id)?.state).toBe(fault.origin === "provider" ? "retry_wait" : "ready");
  retainDashboardEvidence(`${connection.id}-${fault.fault}-failed-model-test`, { state: failedModelTest });
  await fixture.restore(connection.id);
  await retrySelectedModel();
  await fixture.flush();
  const recovered = await dashboardState(smoke);
  expect(recovered.status.conditions.filter((row: any) => row.connection_id === connection.id)).toHaveLength(0);
  expect(recovered.status.connections.find((row: any) => row.profile_id === profile.id)?.state).toBe("ready");
  await refreshDashboard(smoke);
  await smoke.page.screenshot({ path: evidencePath(`${connection.id}-${fault.fault}-recovered.png`) });
  retainDashboardEvidence(`${connection.id}-${fault.fault}-recovered`, { state: recovered, provider_requests: fixture.requests(connection.id) - start });
}, 180000);
