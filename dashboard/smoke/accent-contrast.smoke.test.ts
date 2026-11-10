import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { selectTheme, bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket } from "./delivery-fixture";

let smoke: Smoke;

beforeAll(async () => {
  smoke = await bootSmoke();
  await smoke.page.setViewportSize({ width: 1440, height: 1000 });
  await seedProject(smoke, "accent", { title: "Accent review", vision: false });
  const task = Number((await mc(["task", "add", "Separate controls from the page", "--project", "accent"])).task_id);
  const plane = join(smoke.scratch, "reports", "accent");
  const folder = join(plane, String(task), "packet");
  mkdirSync(folder, { recursive: true });
  const body = "<!doctype html><h1>Separate controls from the page</h1><p>The report stays readable beside the task runs.</p>";
  writeFileSync(join(folder, "report.html"), body);
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "accent"\naccess = "ro"\n`);
  const db = openSpine(smoke);
  try {
    seedDeliveryPacket(db, { taskID: task, thesis: "Separate controls from the page.",
      renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "accent", String(task), "packet", "report.html"),
      reportSHA256: new Bun.CryptoHasher("sha256").update(body).digest("hex") });
  } finally { db.close(); }
  await gotoReady(smoke);
  await selectProject(smoke, "accent");
}, 60000);

afterAll(async () => { if (smoke) await teardown(smoke); });

function contrast(first: string, second: string) {
  const ground = second.match(/[\d.]+/g)!.slice(0, 3).map(Number);
  const top = first.match(/[\d.]+/g)!.map(Number);
  const alpha = top[3] ?? 1;
  const luminance = (color: number[]) => {
    const [r, g, b] = color.map(value => {
      const channel = value / 255;
      return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
    });
    return r! * 0.2126 + g! * 0.7152 + b! * 0.0722;
  };
  const values = [luminance(top.slice(0, 3).map((value, index) => value * alpha + ground[index]! * (1 - alpha))), luminance(ground)].sort((a, b) => b - a);
  return (values[0]! + 0.05) / (values[1]! + 0.05);
}

async function colors(selector: string) {
  return smoke.page.locator(selector).evaluate(node => {
    const style = getComputedStyle(node);
    return { fill: style.backgroundColor, ink: style.color, line: style.borderTopColor, bottomLine: style.borderBottomColor, shadow: style.boxShadow };
  });
}

async function settled() {
  await smoke.page.waitForFunction(() => document.getAnimations().every(animation =>
    animation.playState === "finished" || animation.effect?.getComputedTiming().iterations === Infinity));
}

test("fields and task panels retain contrast in both themes and at narrow widths", async () => {
  const { page } = smoke;
  const measurements: Record<string, number> = {};
  for (const theme of ["light", "dark"] as const) {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await selectTheme(smoke, theme);
    await page.waitForFunction(theme => document.documentElement.dataset.theme === theme, theme);
    await page.getByTestId("tab-board").click();
    await page.getByTestId("project-add").evaluate((node: HTMLElement) => node.click());
    const field = page.getByTestId("project-field-title");
    await settled();
    const empty = await colors('[data-testid="project-field-title"]');
    await page.getByTestId("project-field-path").fill(join(smoke.scratch, "new-project"));
    await page.waitForFunction(() => !(document.querySelector('[data-testid="project-next"]') as HTMLButtonElement).disabled);
    await field.fill("The accent Project");
    await field.focus();
    await settled();
    const filled = await colors('[data-testid="project-field-title"]');
    expect(filled.fill).toBe(empty.fill);
    expect(filled.shadow).not.toBe("none");
    measurements[`${theme} active field boundary`] = contrast(filled.line, filled.fill);
    measurements[`${theme} active field text`] = contrast(filled.ink, filled.fill);
    const head = await colors(".project-dialog-head");
    const title = await colors(".project-dialog-head h2");
    measurements[`${theme} Project title text`] = contrast(title.ink, head.fill);
    await page.screenshot({ path: evidencePath(`accent/project-filled-${theme}.png`), animations: "disabled" });

    const session = await page.context().newCDPSession(page);
    try {
      await session.send("DOM.enable");
      await session.send("CSS.enable");
      const { root } = await session.send("DOM.getDocument");
      const { nodeId } = await session.send("DOM.querySelector", { nodeId: root.nodeId, selector: '[data-testid="project-field-title"]' });
      await session.send("CSS.forcePseudoState", { nodeId, forcedPseudoClasses: ["autofill"] });
      expect(await field.evaluate(node => node.matches(":autofill"))).toBe(true);
      const autofill = await colors('[data-testid="project-field-title"]');
      expect(autofill.shadow).toContain(filled.fill);
      await page.screenshot({ path: evidencePath(`accent/project-autofill-${theme}.png`), animations: "disabled" });
      await session.send("CSS.forcePseudoState", { nodeId, forcedPseudoClasses: [] });
    } finally { await session.detach(); }

    for (const action of ["choose-folder", "setup-save"]) {
      let release!: () => void;
      const gate = new Promise<void>(resolve => { release = resolve; });
      const request = `**/api/projects/${action}`;
      await page.route(request, async route => {
        await gate;
        await route.fulfill(action === "choose-folder" ? { json: { path: "" } } : { status: 503, json: { error: "The test ends the request." } });
      });
      try {
        await page.getByTestId(action === "choose-folder" ? "project-choose-folder" : "project-next").click();
        await page.waitForFunction(() => (document.querySelector('[data-testid="project-field-title"]') as HTMLInputElement).disabled);
        await settled();
        const disabled = await colors('[data-testid="project-field-title"]');
        expect(disabled.fill).toBe(theme === "light" ? "rgb(238, 241, 244)" : "rgb(32, 53, 72)");
        expect((await colors(".project-dialog-foot")).fill).toBe(theme === "light" ? "rgb(238, 241, 244)" : "rgb(32, 53, 72)");
        expect(await field.inputValue()).toBe("The accent Project");
        measurements[`${theme} ${action} field boundary`] = contrast(disabled.line, disabled.fill);
        measurements[`${theme} ${action} field text`] = contrast(disabled.ink, disabled.fill);
        await page.screenshot({ path: evidencePath(`accent/project-${action}-${theme}.png`), animations: "disabled" });
      } finally {
        release();
        await page.waitForFunction(() => !(document.querySelector('[data-testid="project-field-title"]') as HTMLInputElement).disabled);
        await page.unroute(request);
      }
    }
    await page.getByTestId("project-close").click();
    await page.getByTestId("tab-review").click();
    await page.getByTestId("packet-task-open").click();
    await page.getByTestId("task-runs").waitFor({ state: "visible" });
    await settled();
    const bar = await colors('[data-testid="task-dialog"] .td-head');
    const heading = await colors('[data-testid="task-dialog"] h2');
    const panel = await colors('[data-testid="task-dialog"] .td-body');
    if (theme === "light") {
      expect(panel.fill).toBe("rgb(253, 253, 254)");
      expect((await colors(".reader-decision")).fill).toBe("rgb(238, 241, 244)");
      const selected = await colors('.packet-row[aria-current="true"]');
      expect(selected.fill).toBe("rgb(230, 237, 244)");
      expect(selected.shadow).toContain("inset");
    }
    measurements[`${theme} panel header text`] = contrast(heading.ink, bar.fill);
    measurements[`${theme} panel header separation`] = contrast(bar.bottomLine, panel.fill);
    await page.getByTestId("task-dialog-close").focus();
    await page.keyboard.press("Tab");
    await page.keyboard.press("Shift+Tab");
    const focus = await page.getByTestId("task-dialog-close").evaluate(node => getComputedStyle(node).outlineColor);
    measurements[`${theme} header focus boundary`] = contrast(focus, bar.fill);
    await page.screenshot({ path: evidencePath(`accent/task-${theme}.png`), animations: "disabled" });
    await page.getByTestId("task-dialog-close").click();

    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByTestId("packet-task-open").click();
    await page.getByTestId("task-runs").waitFor({ state: "visible" });
    await page.screenshot({ path: evidencePath(`accent/task-narrow-${theme}.png`), animations: "disabled" });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await page.getByTestId("task-dialog-close").click();
    await page.setViewportSize({ width: 1440, height: 1000 });
    for (const tab of ["collie", "pipeline", "configs"]) {
      await page.getByTestId(`tab-${tab}`).click();
      await page.getByTestId(`${tab}-pane`).waitFor();
      await settled();
      await page.screenshot({ path: evidencePath(`accent/${tab}-${theme}.png`), animations: "disabled" });
    }
    await page.evaluate(() => { location.hash = "#environment/accent"; });
    await page.getByTestId("environment-setup-script").waitFor();
    await settled();
    await page.screenshot({ path: evidencePath(`accent/environment-${theme}.png`), animations: "disabled" });
  }
  writeFileSync(evidencePath("accent/contrast.json"), JSON.stringify(measurements, null, 2));
  for (const [name, value] of Object.entries(measurements)) {
    expect(value, name).toBeGreaterThanOrEqual(name.endsWith("text") ? 4.5 : 3);
  }
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 120000);
