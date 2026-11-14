import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import type { Locator } from "playwright";
import { selectTheme, bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let taskID: number;
const sizes = [
  { width: 1440, height: 900 }, { width: 1000, height: 700 },
  { width: 390, height: 844 }, { width: 390, height: 667 }, { width: 320, height: 667 },
];
const themes = ["light", "dark"] as const;

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "conventions", { title: "Culler conventions", environmentReady: true });
  await seedProject(smoke, "other", { title: "Other Project", environmentReady: true });
  taskID = Number((await mc(["task", "add", "A task with a long title that wraps within the shared blue header", "--project", "conventions",
    "--description", Array.from({ length: 40 }, (_, at) => `Paragraph ${at + 1} describes the task and its required evidence.`).join("\n\n")])).task_id);
  await mc(["project", "role-save", "--request", JSON.stringify({ project: "conventions", role: "worker", instructions: "Report the exact test results." })]);
  await smoke.page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await gotoReady(smoke);
  await selectProject(smoke, "conventions");
}, 60000);

afterAll(async () => { await teardown(smoke); }, 30000);
afterEach(async () => {
  await smoke.page.evaluate(() => { document.documentElement.style.zoom = ""; });
  for (let count = 0; count < 4 && await smoke.page.locator("dialog[open]").count(); count++) await smoke.page.keyboard.press("Escape");
});

async function theme(value: "light" | "dark") {
  await selectTheme(smoke, value);
  await smoke.page.waitForFunction(want => document.documentElement.dataset.theme === want, value);
}

async function colors(node: Locator) {
  return node.evaluate(element => {
    const style = getComputedStyle(element);
    return { background: style.backgroundColor, color: style.color };
  });
}

async function surface(dialog: Locator, name: string, selected: "light" | "dark") {
  const header = dialog.locator(".ui-dialog-head");
  const body = dialog.locator(".ui-dialog-body");
  expect((await colors(header)).background, `${name} header`).toBe(selected === "light" ? "rgb(31, 80, 120)" : "rgb(35, 78, 115)");
  expect((await colors(header.locator("h2").first())).color, `${name} title`).toBe("rgb(255, 255, 255)");
  expect((await colors(body)).background, `${name} body`).toBe(selected === "light" ? "rgb(253, 253, 254)" : "rgb(25, 44, 62)");
  expect(await dialog.getByRole("button", { name: "Close", exact: true }).count()).toBe(1);
  await controlContrast(dialog);
}

async function controlContrast(dialog: Locator) {
  const samples = await dialog.evaluate(element => {
    const rgb = (value: string) => (value.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
    const light = (value: string) => {
      const [r, g, b] = rgb(value).map(part => { const channel = part / 255; return channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4; });
      return .2126 * r! + .7152 * g! + .0722 * b!;
    };
    const ratio = (a: string, b: string) => { const high = Math.max(light(a), light(b)); const low = Math.min(light(a), light(b)); return (high + .05) / (low + .05); };
    const ground = (node: Element) => {
      for (let at: Element | null = node; at; at = at.parentElement) {
        const color = getComputedStyle(at).backgroundColor;
        if (color !== "rgba(0, 0, 0, 0)" && color !== "transparent") return color;
      }
      return getComputedStyle(document.body).backgroundColor;
    };
    const output: { name: string; ratio: number; minimum: number }[] = [];
    const headers = element.querySelectorAll(".ui-dialog-head, .vision-panel-bar, .reader-bar, .packet-chat-header,.environment-heading, .flow-popover > .top, .flow-side > .top, .system-health-head");
    for (const header of headers) {
      for (const node of header.querySelectorAll("h2, h3, h4, button, select, .system-health-status")) {
        if (!(node as HTMLElement).checkVisibility()) continue;
        const style = getComputedStyle(node);
        output.push({ name: `${node.tagName} ${node.textContent?.trim().slice(0, 30)} header text (${style.color} on ${ground(node)})`, ratio: ratio(style.color, ground(node)), minimum: 4.5 });
        if (node.tagName !== "BUTTON") continue;
        output.push({ name: `${node.tagName} inner boundary`, ratio: ratio(style.borderTopColor, ground(node)), minimum: 3 });
        const outer = parseFloat(style.outlineWidth) > 0 && style.outlineStyle !== "none" ? style.outlineColor : style.borderTopColor;
        output.push({ name: `${node.tagName} header boundary`, ratio: ratio(outer, ground(header)), minimum: 3 });
      }
    }
    for (const node of element.querySelectorAll("input:not([type=checkbox]):not([type=radio]), textarea, select")) {
      if (!(node as HTMLElement).checkVisibility() || (node as HTMLInputElement).disabled) continue;
      const frame = node.closest(".code-editor") ?? node;
      const style = getComputedStyle(frame);
      output.push({ name: `${node.tagName}.${node.className} field boundary (${style.borderTopColor} on ${ground(frame)})`, ratio: ratio(style.borderTopColor, ground(frame)), minimum: 3 });
      output.push({ name: `${node.tagName} outer boundary`, ratio: ratio(parseFloat(style.outlineWidth) > 0 && style.outlineStyle !== "none" ? style.outlineColor : style.borderTopColor, ground(frame.parentElement!)), minimum: 3 });
    }
    for (const node of element.querySelectorAll(":focus-visible")) {
      const style = getComputedStyle(node);
      if (parseFloat(style.outlineWidth) > 0 && style.outlineStyle !== "none") {
        output.push({ name: `${node.tagName} focus indicator`, ratio: ratio(style.outlineColor, ground(node.parentElement!)), minimum: 3 });
      }
    }
    return output;
  });
  expect(samples.length).toBeGreaterThan(0);
  for (const sample of samples) expect(sample.ratio, sample.name).toBeGreaterThanOrEqual(sample.minimum);
}

async function fits(dialog: Locator) {
  const bounds = await dialog.boundingBox();
  const viewport = smoke.page.viewportSize()!;
  expect(bounds!.x).toBeGreaterThanOrEqual(-1);
  expect(bounds!.y).toBeGreaterThanOrEqual(-1);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(viewport.width + 1);
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height + 1);
  expect(await dialog.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
  expect(await smoke.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

test("equivalent dialog headers share colors and theme changes preserve the role draft", async () => {
  const { page } = smoke;
  await page.setViewportSize(sizes[0]!);
  await page.getByTestId("tab-configs").click();
  await page.getByTestId("config-nav-projects").click();
  await page.getByTestId("customize-project-conventions").click();
  const dialog = page.getByTestId("role-customization");
  const editor = page.getByTestId("customization-editor");
  await page.waitForFunction(() => document.querySelector('[data-testid="role-customization"]')?.getAttribute("aria-busy") === "false");
  await editor.fill("Keep this unsaved draft across both themes.");
  await editor.focus();
  await editor.evaluate((node: HTMLTextAreaElement) => node.setSelectionRange(5, 12));
  for (const selected of themes) {
    await theme(selected);
    await surface(dialog, "role instructions", selected);
    expect(await editor.inputValue()).toBe("Keep this unsaved draft across both themes.");
    expect(await editor.evaluate((node: HTMLTextAreaElement) => [node === document.activeElement, node.selectionStart, node.selectionEnd])).toEqual([true, 5, 12]);
    await page.screenshot({ path: evidencePath(`conventions/role-instructions-${selected}.png`), animations: "disabled" });
  }
  expect(await editor.inputValue()).toBe("Keep this unsaved draft across both themes.");
  await page.keyboard.press("Escape");
  expect(await page.getByTestId("customize-project-conventions").evaluate(node => node === document.activeElement)).toBe(true);
  await page.getByRole("button", { name: "Add Project", exact: true }).click();
  for (const selected of themes) {
    await theme(selected);
    await surface(page.getByTestId("project-dialog"), "Add Project", selected);
    await page.screenshot({ path: evidencePath(`conventions/add-project-${selected}.png`), animations: "disabled" });
  }
  await page.keyboard.press("Escape");
}, 60000);

test("long task details and stacked action forms preserve the header, actions, and focus", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await page.locator(`[data-testid="board-card"][data-task="${taskID}"]`).getByTestId("board-card-open").click();
  const task = page.getByTestId("task-dialog");
  await page.getByTestId("board-record-controls").waitFor();
  for (const selected of themes) {
    await theme(selected);
    for (const size of sizes) {
      await page.setViewportSize(size);
      await fits(task);
      expect((await colors(task.locator(".ui-dialog-head"))).background).toBe(selected === "light" ? "rgb(31, 80, 120)" : "rgb(35, 78, 115)");
      await task.locator(".ui-dialog-body").evaluate(node => { node.scrollTop = node.scrollHeight; });
      const close = await task.getByRole("button", { name: "Close", exact: true }).boundingBox();
      expect(close!.y).toBeGreaterThanOrEqual(0);
      expect(close!.y + close!.height).toBeLessThanOrEqual(size.height);
      await page.screenshot({ path: evidencePath(`conventions/task-${selected}-${size.width}-${size.height}.png`), animations: "disabled" });
      await task.locator(".ui-dialog-body").evaluate(node => { node.scrollTop = 0; });
    }
  }
  // Browser zoom at 200 percent halves the desktop layout viewport.
  await page.setViewportSize({ width: 720, height: 450 });
  await fits(task);
  await page.screenshot({ path: evidencePath("conventions/task-200-percent-layout.png"), animations: "disabled" });
  expect(await task.getByRole("button", { name: "Close", exact: true }).isVisible()).toBe(true);
  await page.setViewportSize(sizes[0]!);
  const opener = page.getByRole("button", { name: "Edit title and priority", exact: true });
  await opener.click();
  const form = page.getByTestId("board-write-dialog");
  for (const selected of themes) {
    await theme(selected);
    await surface(form, "task action", selected);
    expect(await form.evaluate(node => node.contains(document.activeElement))).toBe(true);
    const scrims = await page.evaluate(() => [...document.querySelectorAll("dialog[open]")].map(node => getComputedStyle(node, "::backdrop").backgroundColor));
    expect(scrims).toHaveLength(2);
    expect(scrims[0]).not.toBe(scrims[1]);
    await page.screenshot({ path: evidencePath(`conventions/stacked-task-${selected}.png`), animations: "disabled" });
  }
  await form.getByRole("button", { name: "Close", exact: true }).focus();
  await page.keyboard.press("Shift+Tab");
  expect(await form.evaluate(node => node.contains(document.activeElement))).toBe(true);
  await page.keyboard.press("Escape");
  expect(await task.isVisible()).toBe(true);
  expect(await opener.evaluate(node => node === document.activeElement)).toBe(true);
  await page.keyboard.press("Escape");
}, 60000);

test("role details and Environment headers share blue colors and keep their controls within narrow layouts", async () => {
  const { page } = smoke;
  for (const selected of themes) {
    await theme(selected);
    for (const size of [sizes[0]!, sizes[2]!, sizes[4]!]) {
      await page.setViewportSize(size);
      await page.getByTestId("tab-pipeline").click();
      await page.getByTestId("topology-node-worker").click();
      const role = page.getByTestId("topology-side");
      await role.waitFor();
      const head = role.locator(":scope > .top");
      expect((await colors(head)).background).toBe(selected === "light" ? "rgb(31, 80, 120)" : "rgb(35, 78, 115)");
      expect((await colors(head.locator("h3"))).color).toBe("rgb(255, 255, 255)");
      await controlContrast(role);
      const close = await head.getByRole("button", { name: "Close", exact: true }).boundingBox();
      const bounds = await head.boundingBox();
      expect(close!.x).toBeGreaterThanOrEqual(bounds!.x);
      expect(close!.x + close!.width).toBeLessThanOrEqual(bounds!.x + bounds!.width + 1);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ path: evidencePath(`conventions/role-detail-${selected}-${size.width}.png`), animations: "disabled" });
      await head.getByRole("button", { name: "Close", exact: true }).click();
      await page.evaluate(() => { location.hash = "#environment/conventions"; });
      const environment = page.locator(".environment-page");
      await page.waitForFunction(() => document.querySelector('#environment-pane:not([hidden])')?.getAttribute("aria-busy") === "false");
      const heading = environment.locator(":scope > .environment-heading");
      await heading.waitFor();
      expect((await colors(heading)).background).toBe(selected === "light" ? "rgb(31, 80, 120)" : "rgb(35, 78, 115)");
      await controlContrast(environment);
      const sheet = await environment.boundingBox();
      const bar = await heading.boundingBox();
      expect(Math.abs(bar!.x - sheet!.x)).toBeLessThanOrEqual(2);
      expect(Math.abs(bar!.width - sheet!.width)).toBeLessThanOrEqual(2);
      for (const button of await heading.getByRole("button").all()) {
        const box = await button.boundingBox();
        if (!box) continue;
        expect(box.x).toBeGreaterThanOrEqual(bar!.x);
        expect(box.x + box.width).toBeLessThanOrEqual(bar!.x + bar!.width + 1);
        if (await button.isEnabled()) await button.click({ trial: true });
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ path: evidencePath(`conventions/environment-${selected}-${size.width}.png`), animations: "disabled" });
    }
  }
}, 60000);

test("System health uses the blue structural header in both themes", async () => {
  const { page } = smoke;
  for (const selected of themes) {
    await theme(selected);
    for (const size of [sizes[0]!, sizes[4]!]) {
      await page.setViewportSize(size);
      await page.getByTestId("tab-configs").click();
      await page.getByTestId("config-nav-system").click();
      const panel = page.locator(".system-health-card");
      const header = panel.locator(".system-health-head");
      await header.waitFor();
      expect((await colors(header)).background).toBe(selected === "light" ? "rgb(31, 80, 120)" : "rgb(35, 78, 115)");
      expect((await colors(header.locator("h4"))).color).toBe("rgb(255, 255, 255)");
      await controlContrast(panel);
      const sheet = await panel.boundingBox();
      const bar = await header.boundingBox();
      expect(Math.abs(bar!.x - sheet!.x)).toBeLessThanOrEqual(2);
      expect(Math.abs(bar!.width - sheet!.width)).toBeLessThanOrEqual(2);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await header.scrollIntoViewIfNeeded();
      await page.screenshot({ path: evidencePath(`conventions/system-health-${selected}-${size.width}.png`), animations: "disabled" });
    }
  }
}, 30000);

test("trace access and failure pages retain blue headers and readable actions in both themes", async () => {
  const { page } = smoke;
  let release = () => {};
  let gate = Promise.resolve();
  await page.route("**/api/agentsview/access", async route => {
    await gate;
    await route.fulfill({ status: 503, json: { error: { message: "AgentsView is unavailable. Retry after the service recovers." } } });
  });
  try {
    for (const selected of themes) {
      await gotoReady(smoke);
      await selectTheme(smoke, selected);
      await page.setViewportSize(sizes[0]!);
      gate = new Promise<void>(resolve => { release = resolve; });
      await page.goto(`${smoke.server.url}agentsview/open?path=%2Fsessions%2Ftrace`);
      await page.getByRole("heading", { name: "Open AgentsView", exact: true }).waitFor();
      await page.screenshot({ path: evidencePath(`conventions/trace-access-${selected}.png`), animations: "disabled" });
      release();
      await page.getByRole("heading", { name: "Trace access unavailable", exact: true }).waitFor();
      expect((await colors(page.locator("header"))).background).toBe(selected === "light" ? "rgb(31, 80, 120)" : "rgb(35, 78, 115)");
      expect((await colors(page.locator("main"))).background).toBe(selected === "light" ? "rgb(253, 253, 254)" : "rgb(25, 44, 62)");
      expect(await page.getByRole("button", { name: "Retry", exact: true }).isVisible()).toBe(true);
      for (const size of [sizes[0]!, sizes[4]!]) {
        await page.setViewportSize(size);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await page.screenshot({ path: evidencePath(`conventions/trace-failure-${selected}-${size.width}.png`), animations: "disabled" });
      }
    }
  } finally {
    release();
    await page.unrouteAll({ behavior: "wait" });
    await gotoReady(smoke);
  }
}, 30000);
