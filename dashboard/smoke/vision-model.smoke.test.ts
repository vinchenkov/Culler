import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, gotoReady, mc, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "model-project", { vision: false });
  await gotoReady(smoke);
}, 60000);

afterAll(async () => { await teardown(smoke); });

const homieRoute = async () => ((await mc(["routing", "show"])) as { routes: { role: string; binding: string }[] }).routes.find((r) => r.role === "homie")?.binding;

test("the Vision conversation shows the Homie model and changes the Homie route", async () => {
  const { page } = smoke;
  await page.evaluate(() => { location.hash = "#vision/model-project"; });
  const control = page.getByTestId("vision-homie-model");
  await control.waitFor({ timeout: 20000 });
  const routing = await mc(["routing", "show"]) as { bindings: { id: string; model: string; name: string }[] };
  const label = (id: string) => { const b = routing.bindings.find((row) => row.id === id)!; return b.model || b.name; };
  await page.waitForFunction(() => !(document.querySelector('[data-testid="vision-homie-model"]') as HTMLButtonElement)?.disabled);
  expect(await control.innerText()).toContain(label("fake"));

  await control.click();
  const menu = page.getByTestId("vision-homie-model-menu");
  await menu.waitFor({ state: "visible" });
  const options = menu.getByTestId("vision-homie-model-option");
  expect(await options.count()).toBe(routing.bindings.length);
  expect(await menu.locator('[aria-checked="true"]').innerText()).toContain(label("fake"));
  await page.keyboard.press("Escape");
  await menu.waitFor({ state: "hidden" });

  await control.click();
  await menu.locator('[data-binding="claude"]').click();
  await menu.waitFor({ state: "hidden" });
  expect(await homieRoute()).toBe("claude");
  await page.waitForFunction((text) => document.querySelector('[data-testid="vision-homie-model"]')?.textContent?.includes(text), label("claude"));

  await page.getByTestId("vision-message").fill("Help me write a Vision for this Project.");
  await page.getByTestId("vision-send").click();
  await page.getByTestId("vision-clear").waitFor({ timeout: 20000 });
  await control.click();
  await menu.locator('[data-binding="fake"]').click();
  const confirmation = page.getByTestId("vision-homie-model-confirm");
  await confirmation.waitFor();
  expect(await homieRoute()).toBe("claude");
  expect(await control.innerText()).toContain(label("claude"));
  await confirmation.getByRole("button", { name: "Cancel", exact: true }).click();
  await confirmation.waitFor({ state: "detached" });
  expect(await homieRoute()).toBe("claude");
  expect(await page.getByTestId("vision-transcript").textContent()).toContain("Help me write a Vision");
  await control.click();
  await menu.locator('[data-binding="fake"]').click();
  await confirmation.press("Escape");
  await confirmation.waitFor({ state: "detached" });
  expect(await homieRoute()).toBe("claude");
  await control.click();
  await menu.locator('[data-binding="claude"]').click();
  await menu.waitFor({ state: "hidden" });
  expect(await confirmation.count()).toBe(0);
  await page.getByTestId("vision-message").fill("Keep this draft.");
  await control.click();
  await menu.locator('[data-binding="fake"]').click();
  let failReset = true;
  await page.route("**/api/sessions", async route => {
    if (route.request().method() === "POST" && route.request().postDataJSON()?.clear && failReset) {
      failReset = false;
      await route.fulfill({ status: 503, json: { error: { message: "The conversation reset failed." } } });
    } else await route.continue();
  });
  await confirmation.getByRole("button", { name: "Start new conversation", exact: true }).click();
  await menu.getByRole("button", { name: "Retry", exact: true }).waitFor();
  expect(await control.innerText()).toContain(label("claude"));
  expect(await page.getByTestId("vision-transcript").textContent()).toContain("Help me write a Vision");
  await menu.getByRole("button", { name: "Retry", exact: true }).click();
  await menu.waitFor({ state: "hidden" });
  await page.waitForFunction((text) => document.querySelector('[data-testid="vision-homie-model"]')?.textContent?.includes(text), label("fake"));
  expect(await control.getAttribute("data-pending")).toBe("false");
  expect(await homieRoute()).toBe("fake");
  expect(await page.getByTestId("vision-message").inputValue()).toBe("Keep this draft.");
  expect(await page.getByTestId("vision-transcript").getByTestId("homie-message").count()).toBe(0);
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);
