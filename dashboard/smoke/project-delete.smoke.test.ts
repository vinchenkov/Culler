import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let workspace: string;
let previousPath: string | undefined;
beforeAll(async () => {
  smoke = await bootSmoke();
  const bin = join(smoke.scratch, "bin");
  mkdirSync(bin);
  writeFileSync(join(bin, "docker"), '#!/bin/sh\n[ "$1" = ps ] || exit 1\n', { mode: 0o700 });
  previousPath = process.env.PATH;
  process.env.PATH = `${bin}:${previousPath}`;
  workspace = await seedProject(smoke, "garden", { title: "Garden", environmentReady: true });
  const library = join(smoke.scratch, "library");
  mkdirSync(library);
  const preview = await mc(["project", "setup-preview"], {}, { path: library });
  await mc(["project", "setup-save"], {}, {
    id: "library", title: "Library", path: library, identity: (preview.workspace as any).identity, confirm_access: true,
  });
  await mc(["task", "add", "Keep a useful task", "--project", "garden"]);
  await gotoReady(smoke);
  await selectProject(smoke, "garden");
  await smoke.page.getByTestId("tab-configs").click();
  await smoke.page.getByTestId("config-nav-projects").click();
  await smoke.page.getByTestId("delete-project-garden").waitFor();
}, 60000);
afterEach(async () => { await smoke.page.unrouteAll({ behavior: "wait" }); });
afterAll(async () => { process.env.PATH = previousPath; await teardown(smoke); });

const dialog = () => smoke.page.getByTestId("project-delete-dialog");
const confirm = () => smoke.page.getByTestId("project-delete-confirm");
const details = () => smoke.page.getByTestId("project-delete-details");

test("the active Project opens a confirmation that explains immediate container removal", async () => {
  const { page } = smoke;
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.screenshot({ path: evidencePath("project-delete-row.png"), animations: "disabled" });
  await page.getByTestId("delete-project-garden").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="project-delete-confirm"]') as HTMLButtonElement)?.disabled);
  expect(await dialog().innerText()).toContain("immediately stops this Project’s containers");
  expect(await dialog().getByRole("heading").innerText()).toBe("Delete Garden?");
  expect(await details().innerText()).toContain("1 task");
  expect(await details().innerText()).toContain("0 reviews");
  expect(await details().innerText()).toContain("2 setup operations");
  await page.screenshot({ path: evidencePath("project-delete-active.png"), animations: "disabled" });
  await dialog().getByRole("button", { name: "Cancel", exact: true }).click();
  expect(((await mc(["project", "list"])).projects as any[]).map(row => row.id)).toEqual(["garden", "library"]);
  expect(await page.getByTestId("delete-project-garden").evaluate(node => node === document.activeElement)).toBe(true);
}, 30000);

test("a delayed or failed preview permits cancellation and an explicit retry", async () => {
  const { page } = smoke;
  const address = "**/api/projects/library/delete";
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let arrived!: () => void;
  const received = new Promise<void>(resolve => { arrived = resolve; });
  await page.route(address, async route => {
    arrived();
    await gate;
    await route.continue();
  });
  await page.getByTestId("delete-project-library").click();
  await received;
  expect(await details().innerText()).toBe("Culler reads the Project records…");
  expect(await confirm().isDisabled()).toBe(true);
  await dialog().getByRole("button", { name: "Cancel", exact: true }).click();
  release();
  await page.unrouteAll({ behavior: "wait" });
  await dialog().waitFor({ state: "detached" });

  await page.route(address, route => route.fulfill({ status: 503, json: { error: { message: "The Project records are unavailable." } } }));
  await page.getByTestId("delete-project-library").click();
  await page.getByTestId("project-delete-error").waitFor({ state: "visible" });
  expect(await page.getByTestId("project-delete-error").innerText()).toBe("The Project records are unavailable.");
  expect(await confirm().isDisabled()).toBe(true);
  await page.unrouteAll({ behavior: "wait" });
  await dialog().getByRole("button", { name: "Check again", exact: true }).click();
  await confirm().waitFor();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="project-delete-confirm"]') as HTMLButtonElement)?.disabled);
  await dialog().getByRole("button", { name: "Cancel", exact: true }).click();
}, 30000);

test("a Docker failure preserves the Project and permits another attempt", async () => {
  const { page } = smoke;
  await page.getByTestId("delete-project-garden").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="project-delete-confirm"]') as HTMLButtonElement)?.disabled);
  const docker = join(smoke.scratch, "bin", "docker");
  writeFileSync(docker, "#!/bin/sh\nexit 1\n", { mode: 0o700 });
  await confirm().click();
  await page.getByTestId("project-delete-error").waitFor({ state: "visible" });
  expect(await page.getByTestId("project-delete-error").innerText()).toContain("The Project records remain intact.");
  expect(await confirm().isDisabled()).toBe(false);
  expect(((await mc(["task", "list", "--all", "--project", "garden"])).tasks as any[]).length).toBe(1);
  writeFileSync(docker, '#!/bin/sh\n[ "$1" = ps ] || exit 1\n', { mode: 0o700 });
  await dialog().getByRole("button", { name: "Cancel", exact: true }).click();
}, 30000);

test("deletion clears the selected Project and setup records while it preserves the other Project", async () => {
  const { page } = smoke;
  await page.getByTestId("delete-project-garden").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="project-delete-confirm"]') as HTMLButtonElement)?.disabled);
  await page.screenshot({ path: evidencePath("project-delete-confirm.png"), animations: "disabled" });
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let arrived!: () => void;
  const received = new Promise<void>(resolve => { arrived = resolve; });
  let requests = 0;
  await page.route("**/api/projects/garden/delete", async route => {
    if (route.request().method() !== "POST") return route.continue();
    requests++;
    arrived();
    await gate;
    await route.continue();
  });
  await confirm().click();
  await received;
  expect(await confirm().isDisabled()).toBe(true);
  expect(await dialog().getByRole("button", { name: "Cancel", exact: true }).isDisabled()).toBe(true);
  expect(await dialog().getByRole("button", { name: "Close", exact: true }).isDisabled()).toBe(true);
  expect(await details().innerText()).toBe("Culler stops the Project containers. Culler then deletes its records…");
  await page.keyboard.press("Escape");
  expect(await dialog().isVisible()).toBe(true);
  release();
  await dialog().waitFor({ state: "detached" });
  await page.getByTestId("delete-project-garden").waitFor({ state: "detached" });
  expect(requests).toBe(1);
  expect(((await mc(["project", "list"])).projects as any[]).map(row => row.id)).toEqual(["library"]);
  await page.waitForFunction(() => localStorage.getItem("mc.project") === "library");
  expect(existsSync(join(workspace, "VISION.md"))).toBe(true);
  expect(existsSync(join(workspace, ".git"))).toBe(true);
  await expect(mc(["environment", "get"], {}, { project_id: "garden" })).rejects.toThrow();
  expect(((await mc(["task", "list", "--all", "--project", "library"])).tasks as any[]).length).toBe(0);
  await page.screenshot({ path: evidencePath("project-delete-result.png"), animations: "disabled" });
}, 30000);

test("deletion supports the last Project with incomplete setup and leaves a stable empty state", async () => {
  const { page } = smoke;
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByTestId("delete-project-library").scrollIntoViewIfNeeded();
  await page.screenshot({ path: evidencePath("project-delete-mobile-row.png"), animations: "disabled" });
  await page.getByTestId("delete-project-library").click();
  await page.waitForFunction(() => !(document.querySelector('[data-testid="project-delete-confirm"]') as HTMLButtonElement)?.disabled);
  await page.screenshot({ path: evidencePath("project-delete-mobile-dialog.png"), animations: "disabled" });
  await confirm().click();
  await page.getByTestId("configs-projects").getByText("No projects registered.").waitFor();
  await page.waitForFunction(() => localStorage.getItem("mc.project") === null);
  await page.waitForFunction(() => document.querySelector('[data-testid="configs-add-project"]') === document.activeElement);
  expect(await page.getByTestId("configs-add-project").evaluate(node => node === document.activeElement)).toBe(true);
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  await page.getByTestId("configs-projects").getByText("No projects registered.").waitFor();
  expect(((await mc(["project", "list"])).projects as any[]).length).toBe(0);
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
  await page.screenshot({ path: evidencePath("project-delete-empty.png"), animations: "disabled" });
}, 30000);
