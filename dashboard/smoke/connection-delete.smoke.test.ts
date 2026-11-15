import { afterAll, beforeAll, expect, test } from "bun:test";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let catalog: any;
const connection = "synthetic-delete";
const profile = "synthetic_delete_profile";
const previousStore = process.env.CULLER_TEST_HOST_SECRET_DIR;

beforeAll(async () => {
  smoke = await bootSmoke();
  process.env.CULLER_TEST_HOST_SECRET_DIR = join(smoke.stateScratch, "host-secrets");
  await seedProject(smoke, "delete-form", { title: "Connection deletion" });
  const before = await mc(["agent-profile", "list"]);
  const saved = await mc(["agent-profile", "connection-save"], {}, {
    id: connection, name: "Synthetic connection", template_id: "claude-api-key", api_key: "synthetic-delete-old-key",
    revision: before.revision, submission_id: crypto.randomUUID(),
  });
  catalog = await mc(["agent-profile", "save"], {}, {
    name: profile, connection_id: connection, model: "claude-opus-4-8", enabled: false,
    revision: saved.revision, submission_id: crypto.randomUUID(),
  });
  await gotoReady(smoke);
}, 60000);

afterAll(async () => {
  if (previousStore === undefined) delete process.env.CULLER_TEST_HOST_SECRET_DIR;
  else process.env.CULLER_TEST_HOST_SECRET_DIR = previousStore;
  await teardown(smoke);
}, 30000);

test("deletion requires confirmation, refuses stale consent, and removes the connection with its agent profiles", async () => {
  const { page } = smoke;
  expect((catalog.profiles as any[]).some(row => row.connection_id === connection)).toBe(true);
  await page.getByTestId("tab-pipeline").click();
  await page.getByRole("button", { name: "Model configuration", exact: true }).click();
  await page.getByTestId("profile-connections").locator("summary").click();
  await page.getByTestId(`profile-delete-${connection}`).click();
  const confirm = page.getByTestId("profile-delete-confirm");
  expect(await confirm.innerText()).toContain("deletes its saved credential and its agent profiles");
  expect(await confirm.innerText()).toContain("Provider revocation is unconfirmed.");
  await page.getByTestId("profile-delete-cancel").click();
  expect((await mc(["agent-profile", "list"])).connections).toEqual(catalog.connections);
  await page.getByTestId("profile-connections").locator("summary").click();
  await page.getByTestId(`profile-delete-${connection}`).click();
  const current = catalog.connections.find((row: any) => row.id === connection);
  const replaced = await mc(["agent-profile", "connection-save"], {}, {
    id: connection, name: "Synthetic connection", template_id: "claude-api-key", api_key: "synthetic-delete-new-key",
    revision: catalog.revision, authorization_id: current.authorization_id, submission_id: crypto.randomUUID(),
  });
  await page.getByTestId("profile-delete-confirmed").click();
  await page.getByText("The connection changed. Refresh the connections, then retry.", { exact: true }).waitFor();
  expect((await mc(["agent-profile", "list"])).connections).toEqual(replaced.connections);
  await page.getByTestId("profile-close").click();
  await page.getByRole("button", { name: "Model configuration", exact: true }).click();
  await page.getByTestId("profile-connections").locator("summary").click();
  await page.getByTestId(`profile-delete-${connection}`).click();
  await page.screenshot({ path: evidencePath("connection-delete-confirm.png") });
  await page.getByTestId("profile-delete-confirmed").click();
  await page.getByTestId("profile-notice").filter({ hasText: "The connection is deleted." }).waitFor();
  const after = await mc(["agent-profile", "list"]);
  expect((after.connections as any[]).some(row => row.id === connection)).toBe(false);
  expect((after.profiles as any[]).some(row => row.connection_id === connection)).toBe(false);
  expect(await page.getByTestId(`profile-delete-${connection}`).count()).toBe(0);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.getByTestId("agent-profiles-panel").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("connection-delete-complete.png") });
}, 30000);
