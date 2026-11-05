import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, MC_BIN, seedProject, teardown, type Smoke } from "../../dashboard/smoke/harness";
import { createFailureRepairFixture } from "./failure-repair-fixture";
import { runNativeHarness } from "./native-harness";
import { dashboardState, installFixtureRunner, recordFixtureAvailability, refreshDashboard, replaceKeyInDashboard, retainDashboardEvidence, testAndSaveProfileInDashboard } from "./dashboard-assertions";

const image = process.env.CULLER_PROXY_TEST_IMAGE ?? process.env.MC_BOUNDARY_IMAGE;
if (!image) throw new Error("Set CULLER_PROXY_TEST_IMAGE to the image with the pinned release harnesses.");
const connections = [
  { id: "synthetic-codex-repair", harness: "codex" as const, template: "codex-api-key" },
  { id: "synthetic-claude-repair", harness: "claude-sdk" as const, template: "claude-api-key" },
];
let smoke: Smoke;
let fixture: Awaited<ReturnType<typeof createFailureRepairFixture>>;
const commands: string[] = [];
const previousStore = process.env.CULLER_TEST_HOST_SECRET_DIR;
const previousImage = process.env.CULLER_TEST_PROVIDER_IMAGE;
const previousValidation = process.env.CULLER_TEST_API_KEY_VALIDATION_STATUS;

beforeAll(async () => {
  smoke = await bootSmoke({ observedCommand: value => commands.push(value.command) });
  process.env.CULLER_TEST_HOST_SECRET_DIR = join(smoke.stateScratch, "host-secrets");
  process.env.CULLER_TEST_PROVIDER_IMAGE = image;
  await installFixtureRunner(smoke);
  await seedProject(smoke, "provider-repair", { title: "Provider repair" });
  for (const connection of connections) {
    const catalog = await mc(["agent-profile", "list"]);
    await mc(["agent-profile", "connection-save"], {}, {
      id: connection.id, name: connection.id, template_id: connection.template,
      api_key: `synthetic-${connection.harness}-before-repair`, revision: catalog.revision, submission_id: crypto.randomUUID(),
    });
  }
  fixture = await createFailureRepairFixture({ home: smoke.home, mcBinary: MC_BIN,
    runMc: (argv, input) => mc(argv, {}, input), connections, skipInitialCheck: true });
  recordFixtureAvailability(smoke);
  await gotoReady(smoke);
}, 60000);

afterEach(async () => {
  while (await smoke.page.locator("dialog[open]").count()) await smoke.page.keyboard.press("Escape");
});

afterAll(async () => {
  await fixture?.stop();
  if (previousValidation === undefined) delete process.env.CULLER_TEST_API_KEY_VALIDATION_STATUS;
  else process.env.CULLER_TEST_API_KEY_VALIDATION_STATUS = previousValidation;
  if (previousStore === undefined) delete process.env.CULLER_TEST_HOST_SECRET_DIR;
  else process.env.CULLER_TEST_HOST_SECRET_DIR = previousStore;
  if (previousImage === undefined) delete process.env.CULLER_TEST_PROVIDER_IMAGE;
  else process.env.CULLER_TEST_PROVIDER_IMAGE = previousImage;
  await teardown(smoke);
}, 30000);

for (const connection of connections) test(`${connection.harness} provider rejection reaches the real dashboard and key repair restores the request`, async () => {
  const before = await dashboardState(smoke);
  const authorization = before.profiles.connections.find((row: any) => row.id === connection.id).authorization_id;
  expect(before.status.connections.find((row: any) => row.connection_id === connection.id && !row.profile_id)?.state).toBe("unknown");
  const previousRequests = fixture.requests(connection.id);
  fixture.setFault(connection.id, "credential");
  const failed = await runNativeHarness({ image, access: await fixture.authorize(connection.id, { model: "fixture-model" }),
    model: "fixture-model", prompt: "Return the controlled provider response without tools." });
  await fixture.flush();
  expect(failed.exitCode).not.toBe(0);
  expect(failed.failure?.proxy_request_id).toBeString();
  expect(fixture.requests(connection.id) - previousRequests).toBe(1);
  const failedState = await dashboardState(smoke);
  const condition = failedState.status.conditions.find((row: any) => row.connection_id === connection.id && row.action === "Replace API key");
  expect(condition).toBeDefined();
  expect(condition.state).toBe("stopped");
  const diagnostic = await mc(["system", "diagnostics", "--connection", connection.id]);
  const recorded = (diagnostic.events as any[]).find(row => row.request_id === failed.failure?.proxy_request_id);
  expect(recorded?.origin).toBe("provider");
  expect(recorded?.http_status).toBe(401);
  retainDashboardEvidence(`${connection.id}-failure`, { state: failedState, diagnostics: diagnostic, failure: failed.failure });
  const configBefore = await readFile(join(smoke.home, "config.toml"), "utf8");
  const referencePath = join(smoke.home, "refresh-grants", `${connection.id}.json`);
  const referenceBefore = await readFile(referencePath, "utf8");
  process.env.CULLER_TEST_API_KEY_VALIDATION_STATUS = "401";
  await replaceKeyInDashboard(smoke, connection.id, `synthetic-${connection.harness}-rejected-replacement`, false);
  expect(await readFile(join(smoke.home, "config.toml"), "utf8")).toBe(configBefore);
  expect(await readFile(referencePath, "utf8")).toBe(referenceBefore);
  const preserved = await dashboardState(smoke);
  expect(preserved.profiles.connections.find((row: any) => row.id === connection.id).authorization_id).toBe(authorization);
  fixture.setFault(connection.id, "none");
  const oldKeyRequest = await runNativeHarness({ image, access: await fixture.authorize(connection.id, { model: "fixture-model" }),
    model: "fixture-model", prompt: "Return the controlled provider response without tools." });
  expect(oldKeyRequest.exitCode).toBe(0);
  expect(oldKeyRequest.output).toBe("CULLER_REPAIR_OK");
  await fixture.flush();
  await smoke.page.keyboard.press("Escape");
  process.env.CULLER_TEST_API_KEY_VALIDATION_STATUS = "200";
  const commandStart = commands.length;
  const replacement = await replaceKeyInDashboard(smoke, connection.id, `synthetic-${connection.harness}-after-repair`);
  expect(replacement.connections.find((row: any) => row.id === connection.id).authorization_id).not.toBe(authorization);
  expect(commands.slice(commandStart)).toContain("agent-profile connection-save");
  expect(commands.slice(commandStart)).not.toContain("agent-profile operation-start");
  fixture.setFault(connection.id, "none");
  const saved = await testAndSaveProfileInDashboard(smoke, `${connection.harness} repaired profile`);
  await fixture.flush();
  expect(saved.profile.connection_id).toBe(connection.id);
  const testRequests = fixture.requests(connection.id) - previousRequests;
  expect(testRequests).toBe(4);
  const recovered = await runNativeHarness({ image, access: await fixture.authorize(connection.id, { model: "fixture-model" }),
    model: "fixture-model", prompt: "Return the controlled provider response without tools." });
  await fixture.flush();
  expect(recovered.exitCode).toBe(0);
  expect(recovered.output).toBe("CULLER_REPAIR_OK");
  expect(fixture.requests(connection.id) - previousRequests).toBe(5);
  const ready = await dashboardState(smoke);
  expect(ready.status.conditions.filter((row: any) => row.connection_id === connection.id)).toHaveLength(0);
  expect(ready.status.connections.some((row: any) => row.connection_id === connection.id && row.state === "ready")).toBe(true);
  await refreshDashboard(smoke);
  await smoke.page.screenshot({ path: evidencePath(`${connection.id}-recovered.png`) });
  retainDashboardEvidence(`${connection.id}-recovered`, { state: ready, observations: fixture.observations(), commands: commands.slice(commandStart), requests: fixture.requests(connection.id) - previousRequests });
}, 180000);
