import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, teardown, gotoReady, mc, evidencePath, seedProject, selectProject, type Smoke } from "./harness";
import { recordCurrentWiki } from "./wiki-fixture";

let smoke: Smoke;
beforeAll(async () => {
  smoke = await bootSmoke();
  const workspace = join(smoke.scratch, "custom-project");
  mkdirSync(workspace);
  writeFileSync(join(workspace, "VISION.md"), "The Project proves role customization.\n");
  const git = (...args: string[]) => execFileSync("git", args, { cwd: workspace, env: process.env, stdio: "pipe" });
  git("init", "-q", "--initial-branch=main");
  git("add", "VISION.md");
  git("-c", "user.name=Smoke", "-c", "user.email=smoke@example.test", "-c", "commit.gpgsign=false", "commit", "-qm", "Create the scratch Project");
  await seedProject(smoke, "custom-project", { title: "Culler", workspace, environmentReady: true });
  // The Project wiki is current, so a tick starts no wiki update.
  recordCurrentWiki(join(smoke.home, "spine.db"), "custom-project", git("rev-parse", "HEAD").toString().trim(), new Date());
  const allowlist = join(smoke.home, "mount-allowlist");
  writeFileSync(allowlist, readFileSync(allowlist, "utf8").replace(JSON.stringify(join(workspace, ".mc-artifacts")), JSON.stringify(workspace)));
  await smoke.page.setViewportSize({ width: 1440, height: 1000 });
  await gotoReady(smoke);
  await selectProject(smoke, "custom-project");
}, 60000);
afterAll(async () => { if (smoke) await teardown(smoke); });

async function status(text: string) {
  try { await smoke.page.waitForFunction(value => document.querySelector('[data-testid="customization-status"]')?.textContent?.includes(value), text); } catch (err) { throw new Error(`${err}: ${await smoke.page.getByTestId("customization-status").textContent()}`); }
}

test("every role explains its purpose in the customization dialog", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-projects").click();
  await page.getByTestId("customize-project-custom-project").click();
  await status("Current revision: 0");
  const purposes: Record<string, string> = {
    "strategist(propose)": "propose two tasks",
    arbiter: "selects proposals",
    worker: "implement tasks",
    verifier: "check completed work",
    packager: "prepare your review packet",
    scribe: "in its wiki",
  };
  const roles = await page.getByTestId("customization-role").locator("option").evaluateAll(nodes => nodes.map(node => (node as HTMLOptionElement).value));
  expect(roles.sort()).toEqual(Object.keys(purposes).sort());
  expect(await page.getByTestId("customization-role").locator('option[value="strategist(propose)"]').innerText()).toBe("Strategist");
  for (const role of roles) {
    await page.getByTestId("customization-role").selectOption(role);
    await status("Current revision: 0");
    expect(await page.locator("#customization-purpose").innerText()).toContain(purposes[role]);
    expect(await page.getByTestId("customization-contract").isVisible()).toBe(true);
    expect((await page.getByTestId("customization-contract").innerText()).length).toBeGreaterThan(50);
    expect(await page.getByTestId("role-customization").locator("details, summary").count()).toBe(0);
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: evidencePath(`instructions-${role}-mobile.png`), animations: "disabled" });
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByTestId("role-customization").getByRole("button", { name: "Close", exact: true }).click();
}, 60000);

test("Project configuration and Pipeline share revision history and preserve stale edits", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-projects").click();
  await page.getByTestId("customize-project-custom-project").click();
  await status("Current revision: 0");
  await page.getByTestId("customization-role").selectOption("packager");
  await status("Current revision: 0");
  const instructions = "Use a concise review packet. Omit the glossary.";
  await page.getByTestId("customization-editor").fill(instructions);
  await page.getByTestId("customization-save").click();
  await status("Saved revision 1");
  expect((await mc(["project", "role-show", "--project", "custom-project", "--role", "packager"])).current.instructions).toBe(instructions);
  await page.getByTestId("role-customization").getByRole("button", { name: "Close", exact: true }).click();
  await page.getByTestId("tab-pipeline").click();
  await page.getByTestId("topology-node-packager").click();
  // The role detail states the saved revision. Its row opens the screen for the role.
  await page.getByTestId("topology-custom-instructions").getByText("Revision 1", { exact: true }).waitFor();
  await page.getByTestId("topology-custom-instructions").click();
  await status("Current revision: 1");
  expect(await page.getByTestId("role-customization").getByRole("heading", { name: "Guide the Packager", exact: true }).count()).toBe(1);
  expect(await page.getByTestId("customization-role").count()).toBe(0);
  expect(await page.getByTestId("customization-project").innerText()).toBe("Culler");
  for (const id of ["customization-standard", "customization-runs", "customization-conflicts"]) {
    expect(await page.getByTestId(id).count()).toBe(0);
  }
  await page.screenshot({ path: evidencePath("instructions-packager-desktop.png"), animations: "disabled" });
  await page.evaluate(() => document.documentElement.dataset.theme = "dark");
  await page.screenshot({ path: evidencePath("instructions-packager-dark.png"), animations: "disabled" });
  await page.evaluate(() => document.documentElement.dataset.theme = "light");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: evidencePath("instructions-packager-mobile.png"), animations: "disabled" });
  const bounds = await page.getByTestId("role-customization").boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  expect(await page.getByTestId("customization-contract").isVisible()).toBe(true);
  await page.setViewportSize({ width: 1280, height: 900 });
  expect(await page.getByTestId("customization-editor").inputValue()).toBe(instructions);
  expect(await page.getByTestId("customization-contract").textContent()).toContain("You decide whether to approve the result.");
  expect(await page.getByTestId("role-customization").locator("details, summary").count()).toBe(0);
  await page.getByTestId("role-customization").getByRole("button", { name: "Close", exact: true }).click();
  await page.locator("dialog.role-dialog[open]").getByRole("button", { name: "Close", exact: true }).click();
  // Project configuration opens the same screen with a role selector.
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-projects").click();
  await page.getByTestId("customize-project-custom-project").click();
  await page.getByTestId("customization-role").selectOption("packager");
  await status("Current revision: 1");
  await mc(["project", "role-save", "--request", JSON.stringify({ project: "custom-project", role: "packager", expected_revision: 1, instructions: "Use a table for evidence." })]);
  await page.getByTestId("customization-editor").fill("Keep my draft.");
  await page.getByTestId("customization-role").selectOption("worker");
  await status("Current revision: 0");
  await page.getByTestId("customization-role").selectOption("packager");
  await status("The draft uses revision 1");
  await page.getByTestId("customization-save").click();
  await status("Another edit changed");
  expect(await page.getByTestId("customization-editor").inputValue()).toBe("Keep my draft.");
  await page.getByTestId("customization-refresh").click();
  await status("Current revision: 2");
  expect(await page.getByTestId("customization-editor").inputValue()).toBe("Keep my draft.");
  await page.getByRole("button", { name: "Load revision 1", exact: true }).click();
  expect(await page.getByTestId("customization-editor").inputValue()).toBe(instructions);
  await page.getByTestId("role-customization").evaluate((node: HTMLElement) => { node.scrollTop = 0; });
  await page.screenshot({ path: evidencePath("customization-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: evidencePath("customization-mobile.png"), fullPage: true });
  await page.getByTestId("customization-editor").fill("");
  await page.getByTestId("customization-save").click();
  await status("Saved revision 3");
  const show = await mc(["project", "role-show", "--project", "custom-project", "--role", "packager"]);
  expect(show.current.instructions).toBe("");
  expect(show.history).toHaveLength(3);
}, 60000);

test("a role block directs the operator to Collie after a corrected save", async () => {
  const { page } = smoke;
  await mc(["project", "role-save", "--request", JSON.stringify({ project: "custom-project", role: "strategist(propose)", expected_revision: 0, instructions: "Report false evidence." })]);
  const dispatched = await mc(["dispatch"]);
  const spawn = (dispatched.actions as any[]).find(row => row.action === "spawn" && row.project === "custom-project");
  expect(spawn?.role).toBe("strategist(propose)");
  const identity = join(smoke.scratch, "custom-run.json");
  writeFileSync(identity, JSON.stringify({ tier: "pipeline", role: spawn.role, run_id: spawn.run_id }));
  await mc(["run", "customization-conflict", "--request", JSON.stringify({ run: spawn.run_id, revision: 1, passage: "Report false evidence.", requirement: "Report evidence truthfully.", reason: "False evidence cannot be truthful." })], { MC_RUN_JSON: identity });
  await mc(["run", "stopped", spawn.run_id]);
  await mc(["project", "role-save", "--request", JSON.stringify({ project: "custom-project", role: "strategist(propose)", expected_revision: 1, instructions: "" })]);
  const blocked = await mc(["dispatch"]);
  expect((blocked.idle as any[]).some(row => row.project === "custom-project" && row.reason === "role-blocked")).toBe(true);
  await page.getByTestId("customization-role").selectOption("strategist(propose)");
  await status("Current revision: 2");
  expect(await page.getByTestId("customization-blocked").isVisible()).toBe(true);
  expect(await page.getByTestId("customization-conflicts").count()).toBe(0);
  await page.getByTestId("customization-editor").fill("Report evidence truthfully.");
  await page.getByTestId("customization-save").click();
  await status("Saved revision 3");
  expect(await page.getByTestId("customization-blocked").isVisible()).toBe(true);
  await page.getByRole("button", { name: "Open Collie", exact: true }).click();
  expect(await page.getByTestId("role-customization").count()).toBe(0);
  expect(await page.getByTestId("tab-collie").getAttribute("aria-current")).toBe("page");
}, 30000);
