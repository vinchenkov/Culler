import { afterAll, beforeAll, expect, test } from "bun:test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, MC_BIN, seedProject, selectProject, teardown, type Smoke } from "../../dashboard/smoke/harness";
import { makeDraftWriter, textFromEvent } from "../../runner/collie-runner/main";
import { makeLogger } from "../../runner/shared/log";
import { createFailureRepairFixture } from "./failure-repair-fixture";
import { runNativeHarness } from "./native-harness";
import { recordFixtureAvailability, retainDashboardEvidence } from "./dashboard-assertions";

const image = process.env.CULLER_PROXY_TEST_IMAGE ?? process.env.MC_BOUNDARY_IMAGE;
if (!image) throw new Error("Set CULLER_PROXY_TEST_IMAGE to the image with the pinned release harnesses.");
const connections = [
  { id: "synthetic-codex-draft", harness: "codex" as const, template: "codex-api-key" },
  { id: "synthetic-claude-draft", harness: "claude-sdk" as const, template: "claude-api-key" },
];
let smoke: Smoke;
let fixture: Awaited<ReturnType<typeof createFailureRepairFixture>>;
let session: string;
let identity: string;
const previousStore = process.env.CULLER_TEST_HOST_SECRET_DIR;

beforeAll(async () => {
  smoke = await bootSmoke();
  process.env.CULLER_TEST_HOST_SECRET_DIR = join(smoke.stateScratch, "host-secrets");
  await seedProject(smoke, "provider-draft", { title: "Provider draft" });
  for (const connection of connections) {
    const catalog = await mc(["agent-profile", "list"]);
    await mc(["agent-profile", "connection-save"], {}, { id: connection.id, name: connection.id,
      template_id: connection.template, api_key: `synthetic-${connection.harness}-draft-key`,
      revision: catalog.revision, submission_id: crypto.randomUUID() });
  }
  fixture = await createFailureRepairFixture({ home: smoke.home, mcBinary: MC_BIN, runMc: (argv, input) => mc(argv, {}, input), connections });
  recordFixtureAvailability(smoke);
  await gotoReady(smoke);
  await selectProject(smoke, "provider-draft");
}, 60000);

afterAll(async () => {
  await fixture?.stop();
  if (previousStore === undefined) delete process.env.CULLER_TEST_HOST_SECRET_DIR;
  else process.env.CULLER_TEST_HOST_SECRET_DIR = previousStore;
  await teardown(smoke);
}, 30000);

for (const connection of connections) test(`${connection.harness} text reaches the Collie draft before the provider ends its response`, async () => {
  const { page } = smoke;
  recordFixtureAvailability(smoke);
  const input = page.getByTestId("board-collie-message");
  await page.waitForFunction(() => !(document.querySelector('[data-testid="board-collie-message"]') as HTMLTextAreaElement)?.disabled);
  await input.fill(`Show the ${connection.harness} controlled reply.`);
  await input.press("Enter");
  await page.getByTestId("board-collie-transcript").getByText(`Show the ${connection.harness} controlled reply.`, { exact: true }).waitFor();
  const sessions = await mc(["collie", "list"]);
  session = sessions.sessions[0].id;
  identity = join(smoke.scratch, "native-draft-run.json");
  writeFileSync(identity, JSON.stringify({ run_id: session, tier: "collie", role: "collie", verb_allowlist: sessions.sessions[0].verb_allowlist }));
  const claimed = await mc(["collie", "claim", session], { MC_RUN_JSON: identity });
  expect(claimed.message).toBeTruthy();
  const commands: string[][] = [];
  const commandErrors: string[] = [];
  const events: Record<string, unknown>[] = [];
  const drafts = makeDraftWriter({ mcJSON: async argv => { commands.push(argv); try { return await mc(argv, { MC_RUN_JSON: identity }); }
    catch (error) { commandErrors.push(String(error)); throw error; } },
    sleep: milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds)), now: Date.now,
    log: makeLogger({ comp: "collie-runner", level: "error", sink: { write() {} } }),
  }, session, String(claimed.message.id), claimed.message.attempt_id);
  const gate = fixture.hold(connection.id);
  let complete = false;
  const pending = runNativeHarness({ image, access: await fixture.authorize(connection.id, { model: "fixture-model", runId: session }),
    model: "fixture-model", prompt: "Return the controlled provider response without tools.", onEvent: event => {
      events.push(event);
      const text = textFromEvent(JSON.stringify(event));
      if (text !== undefined) drafts.push(text);
    } }).finally(() => { complete = true; });
  try {
    const draft = page.getByTestId("board-collie-transcript").getByTestId("collie-draft").filter({ hasText: "CULLERREPAIROK" });
    await draft.waitFor({ timeout: 45000 });
    expect(complete).toBe(false);
    expect(commands.some(argv => argv[0] === "collie" && argv[1] === "draft")).toBe(true);
    await page.screenshot({ path: evidencePath(`${connection.id}-before-completion.png`) });
    gate.release();
    const result = await pending;
    await drafts.stop();
    await fixture.flush();
    expect(result.exitCode).toBe(0);
    expect(result.output).toBe("CULLER_REPAIR_OK");
    await mc(["collie", "reply", session, "--to", String(claimed.message.id), "--body", result.output!,
      ...(claimed.message.attempt_id ? ["--attempt", claimed.message.attempt_id] : [])], { MC_RUN_JSON: identity });
    await page.getByTestId("board-collie-transcript").getByTestId("collie-draft").waitFor({ state: "hidden" });
    await page.getByTestId("board-collie-transcript").getByTestId("collie-message").filter({ hasText: "CULLERREPAIROK" }).last().waitFor();
    expect(await page.getByTestId("board-collie-transcript").getByTestId("collie-draft").count()).toBe(0);
    retainDashboardEvidence(`${connection.id}-draft-path`, { events: result.events, commands, provider_requests: fixture.requests(connection.id),
      scope: "The actual native adapter, host proxy, draft writer, host verb, and dashboard complete this path." });
  } catch (error) {
    retainDashboardEvidence(`${connection.id}-draft-failed`, { events, commands, commandErrors, complete,
      observations: fixture.observations(), page: await page.getByTestId("board-collie-transcript").innerText() });
    throw error;
  } finally { gate.release(); await pending.catch(() => {}); await drafts.stop(); }
}, 90000);
