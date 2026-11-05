import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let complete = true;
const failure = {
  id: "connection:team:model_access:review", group: "connections", name: "Team", connection_id: "team",
  profile_id: "review", state: "retry_wait", retry_phase: "delay", roles: ["verifier", "homie"], action: "",
  diagnostic_id: 1, network_error: "reset", latest_failure_at: "2026-10-02T18:58:12Z", next_retry_at: "2099-10-02T18:58:42Z",
};
let current: any = { ...failure };
const attempt = { run_id: "replacement", role: "verifier", project_id: "culler", project_name: "Culler", task_id: 2, state: "active", started_at: "2026-10-02T19:22:30Z" };

beforeAll(async () => {
  smoke = await bootSmoke();
  smoke.page.setDefaultTimeout(7000);
  await smoke.page.route("**/api/system/status", route => route.fulfill({ json: {
    complete, observed_at: new Date().toISOString(), connections: complete ? [current] : [],
    conditions: complete && current.state !== "ready" ? [current] : [],
  } }));
  await gotoReady(smoke);
}, 60000);
afterAll(async () => { await teardown(smoke); });

async function refresh() {
  const response = smoke.page.waitForResponse("**/api/system/status");
  await smoke.page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await response;
}

test("the notice and System health separate the previous failure, retry, and recovery", async () => {
  const { page } = smoke;
  const notice = page.getByTestId("system-notices");
  await notice.getByText("Team / review · Connection interrupted", { exact: true }).waitFor();
  current = { ...failure, retry_phase: "pending", next_retry_at: "2026-10-02T18:58:42Z" };
  await refresh();
  await notice.getByText("Team / review · Retry pending", { exact: true }).waitFor();
  current = { ...current, retry_phase: "active", attempts: [attempt] };
  await refresh();
  await notice.getByText("Team / review · Verifier retry in progress", { exact: true }).waitFor();
  expect(await notice.innerText()).not.toContain("cannot start");
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 950 });
    await notice.getByRole("button", { name: "View details" }).click();
    const dialog = page.getByRole("dialog", { name: "Issue details", exact: true });
    expect(await dialog.innerText()).toContain("The response stream disconnected");
    expect(await dialog.innerText()).toContain("Roles that use this profile");
    expect(await dialog.innerText()).toContain("task #2");
    await dialog.getByText("Technical details", { exact: true }).click();
    expect(await dialog.innerText()).toContain("replacement");
    expect(await dialog.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
    await page.screenshot({ path: evidencePath(`connection-recovery-active-${width}.png`), fullPage: true });
    await dialog.getByRole("button", { name: "Close", exact: true }).click();
    await page.getByTestId("tab-configs").click();
    await page.getByTestId("configs-pane").waitFor();
    await page.getByTestId("config-nav-system").click();
    expect(await page.locator("#system-connections").innerText()).toContain("Verifier retry in progress");
    await page.screenshot({ path: evidencePath(`connection-recovery-health-${width}.png`), fullPage: true });
  }
  await notice.getByRole("button", { name: "View details" }).click();
  const dialog = page.getByRole("dialog", { name: "Issue details", exact: true });
  const close = dialog.getByRole("button", { name: "Close", exact: true });
  await close.focus();
  await refresh();
  expect(await close.evaluate(node => node === document.activeElement)).toBe(true);
  complete = false;
  await refresh();
  await dialog.getByText("Status unavailable", { exact: true }).waitFor();
  expect(await dialog.innerText()).not.toContain("The model responded");
  complete = true;
  current = { ...current, state: "ready", diagnostic_id: 2, resolved_diagnostic_id: 1, attempts: [], recovery: attempt, verified_at: "2026-10-02T19:22:31Z" };
  await refresh();
  await dialog.getByText("The model responded. The Verifier continues on task #2.", { exact: true }).waitFor();
  expect(await notice.locator("[data-recovery-id]").count()).toBe(1);
  expect(await close.evaluate(node => node === document.activeElement)).toBe(true);
  const recovery = notice.locator("[data-recovery-id]");
  await recovery.evaluate(node => { (node as HTMLElement).dataset.retained = "yes"; });
  await refresh();
  expect(await recovery.getAttribute("data-retained")).toBe("yes");
  current = { ...current, recovery: { ...attempt, state: "completed" } };
  await refresh();
  await dialog.getByText("The model responded. The Verifier attempt ended.", { exact: true }).waitFor();
  await page.screenshot({ path: evidencePath("connection-recovery-confirmed.png"), fullPage: true });
  await close.click();
}, 60000);
