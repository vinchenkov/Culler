import { afterAll, beforeAll, expect, test } from "bun:test";
import { evidencePath, bootSmoke, seedProject, gotoReady, mc, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => {
  smoke = await bootSmoke();
  for (const id of ["pause-one", "pause-empty", "pause-archived"]) {
    await seedProject(smoke, id, { title: `Project ${id.replace("pause-", "")}`, environmentReady: true });
  }
  await mc(["project", "archive", "pause-archived"]);
  await mc(["task", "add", "Pause test task", "--project", "pause-one"]);
  await gotoReady(smoke);
}, 60000);
afterAll(async () => { await teardown(smoke); });

async function waitProjects(statuses: string[]) {
  await smoke.page.waitForFunction((values) =>
    ["pause-empty", "pause-one"].every((id, index) =>
      document.querySelector(`[data-testid="project-row-${id}"] .state`)?.textContent === values[index]), statuses);
}

/** Opens the Project list. The pause controls live inside it. */
async function openList() {
  const { page } = smoke;
  if (await page.getByTestId("project-button").getAttribute("aria-expanded") !== "true") {
    await page.getByTestId("project-button").click();
  }
  await page.getByTestId("project-listbox").waitFor({ state: "visible" });
}

const row = (ws: string) => smoke.page.getByTestId(`project-row-${ws}`);
const action = (ws: string) => row(ws).getByRole("button", { name: new RegExp(`^(Pause|Resume) Project ${ws.replace("pause-", "")}$`) });

/** The Project ids whose pipeline is paused, in id order. */
async function pausedIDs() {
  const status = await mc(["pause", "status"]) as any;
  return (status.projects as any[]).filter((row) => row.status === "paused").map((row) => row.id);
}

test("Pause all pauses every Project and Resume all resumes each paused Project", async () => {
  const page = smoke.page;
  await waitProjects(["Pipeline active", "Pipeline active"]);
  await openList();
  expect(await action("pause-archived").count()).toBe(0);
  expect(await row("pause-empty").innerText()).not.toContain("pause-empty");
  expect(await action("pause-empty").innerText()).toBe("Pause");
  await page.screenshot({ path: evidencePath("project-names-menu.png") });
  await row("pause-empty").getByRole("button", { name: "Pause Project empty", exact: true }).click();
  await waitProjects(["Project paused", "Pipeline active"]);
  await row("pause-empty").getByRole("button", { name: "Resume Project empty", exact: true }).waitFor();
  expect(await page.getByTestId("project-listbox").isVisible()).toBe(true);
  const status = await mc(["pause", "status"]) as any;
  expect(Object.keys(status)).toEqual(["projects"]);
  expect(await pausedIDs()).toEqual(["pause-empty"]);
  await page.reload();
  await waitProjects(["Project paused", "Pipeline active"]);
  await selectProject(smoke, "pause-empty");
  await page.getByTestId("project-pause-dot").waitFor({ state: "visible" });
  expect(await page.getByTestId("lease-badge").innerText()).toBe("Project paused · No agents are running");
  await openList();
  expect(await page.getByTestId("pipeline-pause").innerText()).toBe("Pause all");
  await page.getByTestId("pipeline-pause").click();
  await waitProjects(["Project paused", "Project paused"]);
  expect(await page.getByTestId("pipeline-pause").innerText()).toBe("Resume all");
  expect(await row("pause-one").innerText()).toContain("Project paused");
  expect(await row("pause-empty").innerText()).toContain("Project paused");
  expect(await pausedIDs()).toEqual(["pause-empty", "pause-one"]);
  // A Project resume after Pause all resumes only that Project.
  await row("pause-one").getByRole("button", { name: "Resume Project one", exact: true }).click();
  await waitProjects(["Project paused", "Pipeline active"]);
  expect(await page.getByTestId("pipeline-pause").innerText()).toBe("Pause all");
  expect(await pausedIDs()).toEqual(["pause-empty"]);
  await row("pause-one").getByRole("button", { name: "Pause Project one", exact: true }).click();
  await waitProjects(["Project paused", "Project paused"]);
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-projects").click();
  await page.getByTestId("project-listbox").waitFor({ state: "hidden" });
  const configs = page.getByTestId("configs-projects");
  await configs.getByRole("button", { name: "Resume Project empty", exact: true }).waitFor();
  expect(await configs.locator("tbody tr td:first-child").allTextContents()).toEqual([
    "Project archivedEnvironmentRole customizationarchived",
    "Project emptyEnvironmentRole customizationpaused",
    "Project oneEnvironmentRole customizationpaused",
  ]);
  expect((await configs.innerText()).match(/Project empty/g)).toHaveLength(1);
  await configs.getByRole("button", { name: "Resume Project empty", exact: true }).click();
  await configs.getByRole("button", { name: "Pause Project empty", exact: true }).waitFor();
  await waitProjects(["Pipeline active", "Project paused"]);
  await configs.getByRole("button", { name: "Pause Project empty", exact: true }).click();
  await configs.getByRole("button", { name: "Resume Project empty", exact: true }).waitFor();
  await waitProjects(["Project paused", "Project paused"]);
  // Homie prepares a conversation while every Project is paused.
  await page.getByTestId("tab-homie").click();
  await page.getByTestId("project-listbox").waitFor({ state: "hidden" });
  await page.waitForFunction(() => document.querySelectorAll('[data-testid="session-row"]').length > 0);
  expect(((await mc(["homie", "list"])).sessions as any[]).length).toBe(1);
  expect(await pausedIDs()).toEqual(["pause-empty", "pause-one"]);
  await page.getByTestId("tab-board").click();
  await openList();
  expect(await page.getByTestId("pipeline-pause").innerText()).toBe("Resume all");
  await page.getByTestId("pipeline-pause").click();
  await waitProjects(["Pipeline active", "Pipeline active"]);
  await page.getByTestId("project-pause-dot").waitFor({ state: "hidden" });
  expect(await pausedIDs()).toEqual([]);
  expect(((await mc(["pause", "status"])).projects as any[]).find((row) => row.id === "pause-archived").status).toBe("archived");
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-projects").click();
  await configs.getByRole("button", { name: "Pause Project empty", exact: true }).click();
  await waitProjects(["Project paused", "Pipeline active"]);
  await configs.getByRole("button", { name: "Resume Project empty", exact: true }).click();
  await waitProjects(["Pipeline active", "Pipeline active"]);
}, 60000);

test("a failed pause keeps the confirmed state and displays the error", async () => {
  const page = smoke.page;
  await page.route("**/api/pause", async (route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({ status: 400, json: { error: { message: "Pause test refusal." } } });
    } else await route.continue();
  });
  try {
    await page.getByTestId("tab-board").click();
    await openList();
    await page.getByTestId("pipeline-pause").click();
    await page.getByTestId("pipeline-pause-error").waitFor({ state: "visible" });
    expect(await page.getByTestId("pipeline-pause-error").innerText()).toBe("Pause test refusal.");
    expect(await pausedIDs()).toEqual([]);
    await waitProjects(["Pipeline active", "Pipeline active"]);
  } finally { await page.unroute("**/api/pause"); }
}, 30000);

test("pause controls recover after a failed state read", async () => {
  const page = smoke.page;
  await page.route("**/api/pause", (route) => route.fulfill({
    status: 500, json: { error: { message: "Pause state test failure." } },
  }));
  try {
    await page.reload();
    await page.waitForFunction(() => document.querySelector('[data-testid="project-button"]')?.getAttribute("title") === "Pause state test failure.");
    expect(await page.getByTestId("project-button").isDisabled()).toBe(true);
    expect(await page.getByTestId("project-button").getAttribute("title")).toBe("Pause state test failure.");
    expect(await page.getByTestId("project-listbox").isVisible()).toBe(false);
    expect(await page.getByTestId("pipeline-pause").isDisabled()).toBe(true);
    expect(await page.getByTestId("pipeline-pause-error").innerText()).toBe("Pause state test failure.");
  } finally { await page.unroute("**/api/pause"); }
  await page.getByTestId("board-pane").getByRole("button", { name: "Retry", exact: true }).first().click();
  await waitProjects(["Pipeline active", "Pipeline active"]);
  await openList();
  expect(await page.getByTestId("pipeline-pause-error").isVisible()).toBe(false);
  expect(await page.getByTestId("pipeline-pause").isEnabled()).toBe(true);
  await page.getByTestId("project-row-pause-one").waitFor();
}, 30000);

test("the header names a run in a paused Project as finishing", async () => {
  const page = smoke.page;
  const stamp = (msAgo: number) => new Date(Date.now() - msAgo).toISOString().replace("T", " ").slice(0, 19);
  const leases = [{
    project: "pause-one", status: "held", run_id: "run-pause-one", owner: "worker", subject: 1,
    acquired_at: stamp(240_000), last_heartbeat_at: stamp(5_000), hard_deadline_at: stamp(-3_600_000),
  }];
  const badge = (pattern: RegExp) => page.waitForFunction((source) =>
    new RegExp(source).test(document.querySelector('[data-testid="lease-badge"] .text')?.textContent ?? ""), pattern.source);
  await page.route("**/api/leases", (route) => route.fulfill({ json: { leases } }));
  try {
    await mc(["pause"]);
    await page.reload();
    await page.waitForSelector("body[data-ready]", { timeout: 15000 });
    await page.getByTestId("tab-board").click();
    await selectProject(smoke, "pause-one");
    await badge(/^Project paused · Worker finishing run on task #1 · \dm$/);
    await page.getByTestId("tab-homie").click();
    await badge(/^1 agent finishing run$/);
    await mc(["resume"]);
    await page.reload();
    await page.waitForSelector("body[data-ready]", { timeout: 15000 });
    await badge(/^1 agent running$/);
    await page.getByTestId("tab-board").click();
    await badge(/^Worker running on task #1 · \dm$/);
  } finally {
    await page.unroute("**/api/leases");
    await mc(["resume"]);
  }
}, 60000);


test("the selector hides duplicate status and unnecessary bulk controls", async () => {
  const { page } = smoke;
  const cases = [
    [{ id: "pause-one", title: "Project one", status: "paused", setup_status: "vision" }],
    [{ id: "pause-one", title: "Project one", status: "active", setup_status: "ready" }],
    [
      { id: "pause-one", title: "Project one", status: "active", setup_status: "ready" },
      { id: "incomplete", title: "Incomplete", status: "paused", setup_status: "configured" },
      { id: "archived", title: "Archived", status: "archived", setup_status: "ready" },
    ],
  ];
  for (const projects of cases) {
    await page.route("**/api/pause", (route) => route.fulfill({ json: { projects } }));
    try {
      await page.reload();
      await page.getByTestId("project-button").waitFor();
      await openList();
      expect(await page.locator("#pipeline-controls").isVisible()).toBe(false);
      expect(await page.getByTestId("pipeline-pause").isVisible()).toBe(false);
      expect(await page.locator(".scope-foot-status").count()).toBe(0);
      await page.screenshot({ path: evidencePath(`selector-${projects.length}-${projects[0].setup_status}.png`), animations: "disabled" });
      if (projects.length === 1 && projects[0].setup_status === "ready") {
        await page.route("**/api/projects/pause-one/pause", (route) => route.fulfill({
          status: 400, json: { error: { message: "Project pause refusal." } },
        }));
        try {
          await action("pause-one").click();
          await page.getByTestId("pipeline-pause-error").waitFor({ state: "visible" });
          expect(await page.getByTestId("pipeline-pause-error").innerText()).toBe("Project pause refusal.");
          expect(await page.getByTestId("pipeline-pause").isVisible()).toBe(false);
        } finally { await page.unroute("**/api/projects/pause-one/pause"); }
      }
    } finally { await page.unroute("**/api/pause"); }
  }
});
