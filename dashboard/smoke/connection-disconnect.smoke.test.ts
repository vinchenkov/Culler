import { afterAll, beforeAll, expect, test } from "bun:test";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let catalog: any;
const connection = "synthetic-disconnect";
const previousStore = process.env.CULLER_TEST_HOST_SECRET_DIR;

beforeAll(async () => {
  smoke = await bootSmoke();
  process.env.CULLER_TEST_HOST_SECRET_DIR = join(smoke.stateScratch, "host-secrets");
  await seedProject(smoke, "disconnect-form", { title: "Connection disconnection" });
  const before = await mc(["agent-profile", "list"]);
  catalog = await mc(["agent-profile", "connection-save"], {}, {
    id: connection, name: "Synthetic connection", template_id: "claude-api-key", api_key: "synthetic-disconnect-old-key",
    revision: before.revision, submission_id: crypto.randomUUID(),
  });
  await gotoReady(smoke);
}, 60000);

afterAll(async () => {
  if (previousStore === undefined) delete process.env.CULLER_TEST_HOST_SECRET_DIR;
  else process.env.CULLER_TEST_HOST_SECRET_DIR = previousStore;
  await teardown(smoke);
}, 30000);

test("disconnection requires confirmation, refuses stale consent, and preserves agent profiles", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-pipeline").click();
  await page.getByRole("button", { name: "Model configuration", exact: true }).click();
  await page.getByTestId("profile-connections").locator("summary").click();
  await page.getByTestId(`profile-disconnect-${connection}`).click();
  const confirm = page.getByTestId("profile-disconnect-confirm");
  expect(await confirm.innerText()).toContain("Provider revocation is unconfirmed.");
  expect(await confirm.innerText()).toContain("Agent profiles and assignments remain saved.");
  await page.getByTestId("profile-disconnect-cancel").click();
  expect((await mc(["agent-profile", "list"])).connections).toEqual(catalog.connections);
  await page.getByTestId("profile-connections").locator("summary").click();
  await page.getByTestId(`profile-disconnect-${connection}`).click();
  const current = catalog.connections.find((row: any) => row.id === connection);
  const replaced = await mc(["agent-profile", "connection-save"], {}, {
    id: connection, name: "Synthetic connection", template_id: "claude-api-key", api_key: "synthetic-disconnect-new-key",
    revision: catalog.revision, authorization_id: current.authorization_id, submission_id: crypto.randomUUID(),
  });
  await page.getByTestId("profile-disconnect-confirmed").click();
  await page.getByText("The connection changed. Refresh its status before disconnection.", { exact: true }).waitFor();
  expect((await mc(["agent-profile", "list"])).connections).toEqual(replaced.connections);
  await page.getByTestId("profile-close").click();
  await page.getByRole("button", { name: "Model configuration", exact: true }).click();
  await page.getByTestId("profile-connections").locator("summary").click();
  await page.getByTestId(`profile-disconnect-${connection}`).click();
  await page.screenshot({ path: evidencePath("connection-disconnect-confirm.png") });
  await page.getByTestId("profile-disconnect-confirmed").click();
  await page.getByTestId("profile-notice").filter({ hasText: "The connection is disconnected from Culler." }).waitFor();
  const after = await mc(["agent-profile", "list"]);
  expect(after.profiles).toEqual(catalog.profiles);
  expect((after.connections as any[]).find(row => row.id === connection).access_status).toBe("disconnected");
  expect(await page.getByTestId(`profile-disconnect-${connection}`).count()).toBe(0);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.getByTestId("agent-profiles-panel").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("connection-disconnect-complete.png") });
}, 30000);
