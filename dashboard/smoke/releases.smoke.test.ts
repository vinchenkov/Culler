import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let status: Record<string, unknown>;
let opened = 0;
beforeAll(async () => {
  smoke = await bootSmoke();
  status = {
    state: "current", installed: "a".repeat(40), available: "a".repeat(40),
    detail: "The installed release matches the available release. Service health appears separately.",
    workflow: { state: "preparing", commit: "b".repeat(40), url: "https://github.com/owner/repo/actions/runs/123" },
  };
  await smoke.page.route("**/api/releases/status", route => route.fulfill({ json: status }));
  await smoke.page.route("**/api/releases/open", async route => {
    expect(route.request().postDataJSON()).toEqual({ confirm: true });
    opened += 1;
    await route.fulfill({ json: { opened: true, detail: "Continue in Terminal. The installer asks before service activation." } });
  });
  await gotoReady(smoke);
  await smoke.page.getByTestId("tab-configs").click();
  await smoke.page.getByTestId("config-nav-system").click();
});
afterAll(async () => { await teardown(smoke); });

test("release controls distinguish publication from service health and preserve narrow layouts", async () => {
  const { page } = smoke;
  const panel = page.getByTestId("release-controls");
  await panel.getByRole("button", { name: "Check for updates" }).click();
  await page.waitForFunction(() => document.querySelector("#release-status")?.textContent?.includes("Culler is current"));
  expect(await panel.innerText()).toContain("GitHub is preparing the latest main commit.");
  expect(await panel.getByRole("link", { name: "View release workflow" }).getAttribute("href")).toEndWith("/123");
  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({ width, height: 1000 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
    await page.screenshot({ path: evidencePath(`releases-${width}.png`) });
  }
  status = { state: "unknown", installed: "a".repeat(40), detail: "GitHub refused access. Repair the GitHub credential, then run install.sh again." };
  await panel.getByRole("button", { name: "Check for updates" }).click();
  await page.waitForFunction(() => document.querySelector("#release-status")?.textContent?.includes("Release availability is unknown"));
  expect(await panel.innerText()).not.toContain("Culler is current");
  await panel.getByRole("button", { name: "Open installer" }).click();
  await page.waitForFunction(() => document.querySelector("#release-action-status")?.textContent?.includes("Continue in Terminal"));
  expect(opened).toBe(1);
  expect(await panel.innerText()).not.toContain("Update complete");
}, 30_000);
