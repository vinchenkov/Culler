import { expect } from "bun:test";
import { chmodSync, mkdirSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { evidencePath, SMOKE_BEARER, type Smoke } from "../../dashboard/smoke/harness";

export async function installFixtureRunner(smoke: Smoke) {
  const script = join(import.meta.dir, "../../runner/image/stage-runtime.py");
  const result = Bun.spawn(["python3", script, join(smoke.home, "release/runner")], { stdout: "pipe", stderr: "pipe" });
  const [output, error, code] = await Promise.all([new Response(result.stdout).text(), new Response(result.stderr).text(), result.exited]);
  if (code !== 0) throw new Error(`The fixture runner could not install: ${output}${error}`);
  const protect = (path: string) => {
    chmodSync(path, 0o700);
    for (const entry of readdirSync(path, { withFileTypes: true })) {
      const child = join(path, entry.name);
      if (entry.isDirectory()) protect(child);
      else chmodSync(child, 0o600);
    }
  };
  protect(join(smoke.home, "release/runner"));
}

export async function dashboardState(smoke: Smoke) {
  const status = await smoke.page.request.get(`${smoke.server.url}/api/system/status`, { headers: SMOKE_BEARER });
  const profiles = await smoke.page.request.get(`${smoke.server.url}/api/agent-profiles`, { headers: SMOKE_BEARER });
  expect(status.ok()).toBe(true);
  expect(profiles.ok()).toBe(true);
  return { status: await status.json(), profiles: await profiles.json() };
}

export function retainDashboardEvidence(name: string, value: unknown) {
  writeFileSync(evidencePath(`${name}.json`), JSON.stringify(value, null, 2), { mode: 0o600 });
}

export function recordFixtureAvailability(smoke: Smoke) {
  mkdirSync(join(smoke.home, "health"), { recursive: true, mode: 0o700 });
  writeFileSync(join(smoke.home, "health/resident-availability.json"), JSON.stringify({ observed_at: new Date().toISOString(), can_commit: true }), { mode: 0o600 });
}

export async function refreshDashboard(smoke: Smoke) {
  recordFixtureAvailability(smoke);
  const response = smoke.page.waitForResponse(response => response.url().endsWith("/api/system/status") && response.status() === 200);
  await smoke.page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await (await response).finished();
}

export async function replaceKeyInDashboard(smoke: Smoke, connection: string, key: string, accepted = true) {
  const { page } = smoke;
  await refreshDashboard(smoke);
  const notice = page.getByTestId("system-notices");
  const conditionID = `connection:${connection}:connection_access:`;
  await page.waitForFunction(id => {
    const notice = document.querySelector('[data-testid="system-notices"]');
    return notice?.querySelector(`[data-condition-id="${id}"]`) || Array.from(notice?.querySelectorAll("button") ?? []).some(button => button.textContent === "Review issues");
  }, conditionID);
  const direct = notice.locator(`[data-condition-id="${conditionID}"]`).getByRole("button", { name: "Replace API key", exact: true });
  if (await direct.count()) await direct.click();
  else {
    await notice.getByRole("button", { name: "Review issues", exact: true }).click();
    await page.locator(`article[data-condition-id="${conditionID}"]:visible`).getByRole("button", { name: "Replace API key", exact: true }).click();
  }
  const repair = page.getByTestId("profile-repair");
  await repair.waitFor();
  expect(await repair.getByRole("button", { name: "Authorize", exact: true }).count()).toBe(0);
  const field = repair.getByTestId("profile-api_key");
  expect(await field.getAttribute("type")).toBe("password");
  await field.fill(key);
  const saved = page.waitForResponse(response => response.url().endsWith("/api/agent-profiles/connection-save") && response.request().method() === "POST");
  await repair.getByTestId("profile-save-key").click();
  const response = await saved;
  if (!accepted) {
    expect(response.ok()).toBe(false);
    const failure = await response.json();
    expect(JSON.stringify(failure)).toContain("The previous key remains saved.");
    expect(JSON.stringify(failure)).not.toContain(key);
    await page.getByRole("alert").filter({ hasText: "The previous key remains saved." }).waitFor();
    expect(await field.getAttribute("type")).toBe("password");
    const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
    expect(storage).not.toContain(key);
    await page.screenshot({ path: evidencePath(`${connection}-rejected-key.png`) });
    return failure;
  }
  expect(response.ok()).toBe(true);
  const catalog = await response.json();
  expect(catalog.connection_id).toBe(connection);
  await page.getByText("The API key is saved. Test the selected model to verify access.", { exact: true }).waitFor();
  expect(await page.getByTestId("profile-api_key").count()).toBe(0);
  const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
  expect(storage).not.toContain(key);
  await page.screenshot({ path: evidencePath(`${connection}-key-repair.png`) });
  return catalog;
}

export async function testAndSaveProfileInDashboard(smoke: Smoke, name: string) {
  const { page } = smoke;
  const model = page.getByTestId("profile-model");
  await model.locator('option[value="fixture-model"]').waitFor({ state: "attached" });
  await model.selectOption("fixture-model");
  await page.getByTestId("profile-name").fill(name);
  if (await page.getByTestId("profile-configure-next").count()) await page.getByTestId("profile-configure-next").click();
  await page.getByTestId("profile-test").click();
  await page.waitForFunction(() => {
    const save = document.querySelector('[data-testid="profile-save"]') as HTMLButtonElement | null;
    const error = document.querySelector('[data-testid="profile-operation"]');
    return save && !save.disabled || error?.textContent?.includes("failed");
  }, undefined, { timeout: 90000 });
  if (!(await page.getByTestId("profile-save").isEnabled())) {
    const detail = await page.getByTestId("profile-operation").innerText();
    retainDashboardEvidence(`${name.replaceAll(" ", "-")}-model-test-failed`, { detail, state: await dashboardState(smoke) });
    throw new Error(`The actual model test failed: ${detail}`);
  }
  const saved = page.waitForResponse(response => response.url().endsWith("/api/agent-profiles/save") && response.request().method() === "POST");
  await page.getByTestId("profile-save").click();
  const response = await saved;
  expect(response.ok()).toBe(true);
  const catalog = await response.json();
  const profile = catalog.profiles.find((row: any) => row.id === catalog.profile_id);
  expect(profile).toBeDefined();
  await page.getByTestId("profile-create").waitFor();
  await page.keyboard.press("Escape");
  return { catalog, profile };
}
