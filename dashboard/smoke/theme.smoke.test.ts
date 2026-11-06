import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
let taskID: number;

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "theme", { title: "Theme" });
  taskID = Number((await mc(["task", "add", "Show each page in the selected theme", "--project", "theme",
    "--description", "Each page keeps readable text in the light theme and in the dark theme."]))["task_id"]);
  const blocked = await mc(["task", "add", "Confirm the colors of the dark theme", "--project", "theme"]);
  await mc(["task", "block", String(blocked["task_id"]), "--reason", "The operator must confirm the colors."]);
  mkdirSync(join(smoke.home, "logs"), { recursive: true });
  writeFileSync(join(smoke.home, "logs", "resident.ndjson"),
    JSON.stringify({ ts: "2026-10-06T12:00:00.000Z", level: "info", comp: "resident",
      event: "tick.completed", msg: "The Project state is current." }) + "\n");
  await smoke.page.setViewportSize({ width: 1440, height: 960 });
  await smoke.page.emulateMedia({ colorScheme: "light" });
  await gotoReady(smoke);
  await smoke.page.getByTestId("board-card").first().waitFor();
}, 60000);

afterAll(async () => { await teardown(smoke); }, 30000);

const shown = () => smoke.page.evaluate(() => {
  const root = document.documentElement;
  const button = document.getElementById("theme-button")!;
  return {
    theme: root.dataset.theme,
    stored: localStorage.getItem("mc.theme"),
    scheme: getComputedStyle(root).colorScheme,
    paper: getComputedStyle(document.body).backgroundColor,
    picture: getComputedStyle(document.body, "::before").backgroundImage,
    scene: root.dataset.scene,
    landscape: getComputedStyle(root).getPropertyValue("--landscape"),
    pressed: button.getAttribute("aria-pressed"),
    name: button.getAttribute("aria-label"),
    title: button.title,
    frame: document.querySelector('meta[name="theme-color"]')!.getAttribute("content"),
    mast: getComputedStyle(root).getPropertyValue("--mast").trim(),
  };
});

const until = (theme: string) => smoke.page.waitForFunction((want) => document.documentElement.dataset.theme === want, theme);

/** The contrast ratio of each text of the page against its own ground. The
 * ground is each background color from the element up to the first solid
 * one, over the paper color. */
const contrasts = async () => {
  // A measure during a color transition reads a color between the two themes.
  await smoke.page.waitForFunction(() => document.getAnimations().every((animation) =>
    animation.playState === "finished" || animation.effect?.getComputedTiming().iterations === Infinity));
  return smoke.page.evaluate(() => {
    const parse = (value: string) => {
      const parts = (value.match(/[\d.]+/g) ?? []).map(Number);
      const scale = value.startsWith("color(") ? 255 : 1;
      return { rgb: parts.slice(0, 3).map((part) => part * scale), alpha: parts.length > 3 ? parts[3]! : 1 };
    };
    const over = (top: number[], alpha: number, under: number[]) => top.map((part, at) => part * alpha + under[at]! * (1 - alpha));
    const luminance = (rgb: number[]) => {
      const [red, green, blue] = rgb.map((part) => {
        const channel = part / 255;
        return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * red! + 0.7152 * green! + 0.0722 * blue!;
    };
    const paper = parse(getComputedStyle(document.body).backgroundColor).rgb;
    const found: [string, number][] = [];
    const seen = new Set<Element>();
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const element = node.parentElement;
      const words = (node.nodeValue ?? "").trim();
      if (!element || seen.has(element) || words === "") continue;
      seen.add(element);
      // The flow graph draws its text as shapes with a fill.
      if (element instanceof SVGElement) continue;
      if (!element.checkVisibility({ opacityProperty: true, visibilityProperty: true })) continue;
      const layers: { rgb: number[]; alpha: number }[] = [];
      let opacity = 1;
      for (let at: Element | null = element; at; at = at.parentElement) {
        const style = getComputedStyle(at);
        opacity *= Number(style.opacity);
        const layer = parse(style.backgroundColor);
        if (layers.at(-1)?.alpha !== 1 && layer.alpha > 0) layers.push(layer);
      }
      let ground = paper;
      for (const layer of layers.reverse()) ground = over(layer.rgb, layer.alpha, ground);
      const ink = parse(getComputedStyle(element).color);
      const text = over(ink.rgb, ink.alpha * opacity, ground);
      const [high, low] = [luminance(text), luminance(ground)].sort((a, b) => b - a);
      const place = `${element.tagName.toLowerCase()}${element.id ? `#${element.id}` : ""}.${[...element.classList].join(".")}`;
      found.push([`${place} "${words.slice(0, 40)}"`, (high! + 0.05) / (low! + 0.05)]);
    }
    return found;
  });
};

/** The text that has less contrast in the dark theme than in the light
 * theme. A contrast ratio of 4.5:1 is sufficient in each theme. */
async function lostContrast() {
  const { page } = smoke;
  await page.emulateMedia({ colorScheme: "light" });
  await until("light");
  const light = new Map(await contrasts());
  await page.emulateMedia({ colorScheme: "dark" });
  await until("dark");
  const lost: string[] = [];
  for (const [text, ratio] of await contrasts()) {
    const before = light.get(text);
    if (before !== undefined && ratio < Math.min(before, 4.5) - 0.05) lost.push(`${text}: ${before.toFixed(2)} in light, ${ratio.toFixed(2)} in dark`);
  }
  return lost;
}

test("Without a selection, the dashboard uses the theme of the system", async () => {
  const { page } = smoke;
  const light = await shown();
  expect(light).toMatchObject({ theme: "light", stored: null, scheme: "light", pressed: "false", name: "Dark theme", title: "Use the dark theme" });
  // Without a selection, only the tile lines of the picture show.
  expect(light.scene).toBe("hidden");
  expect(light.picture).toContain("data:image/svg+xml");
  expect(light.landscape).toContain("/landscape.png");
  expect(light.frame).toBe(light.mast);

  await page.emulateMedia({ colorScheme: "dark" });
  await until("dark");
  const dark = await shown();
  expect(dark).toMatchObject({ theme: "dark", stored: null, scheme: "dark", pressed: "true", name: "Dark theme", title: "Use the light theme" });
  expect(dark.picture).toContain("data:image/svg+xml");
  expect(dark.picture).not.toBe(light.picture);
  expect(dark.landscape).toContain("/landscape-dark.png");
  expect(dark.paper).not.toBe(light.paper);
  expect(dark.frame).toBe(dark.mast);
  expect(dark.mast).not.toBe(light.mast);

  // Each background picture is a file of the dashboard.
  for (const name of ["/landscape.png", "/landscape-dark.png"]) {
    const picture = await fetch(`${smoke.server.url}${name}`);
    expect(picture.status).toBe(200);
    expect(picture.headers.get("content-type")).toBe("image/png");
  }
  await page.emulateMedia({ colorScheme: "light" });
  await until("light");
}, 30000);

test("The logo button shows and hides the background picture, and the browser keeps the selection", async () => {
  const { page } = smoke;
  const button = page.getByTestId("scene-button");
  const state = () => page.evaluate(() => {
    const control = document.getElementById("scene-button")!;
    return {
      scene: document.documentElement.dataset.scene,
      stored: localStorage.getItem("mc.scene"),
      picture: getComputedStyle(document.body, "::before").backgroundImage.includes("/landscape.png") ? "picture"
        : getComputedStyle(document.body, "::before").backgroundImage.includes("data:image/svg+xml") ? "lines" : "none",
      veil: getComputedStyle(document.body, "::after").display,
      pressed: control.getAttribute("aria-pressed"),
      name: control.getAttribute("aria-label"),
      title: control.title,
    };
  });
  expect(await state()).toMatchObject({ scene: "hidden", stored: null, picture: "lines", veil: "block", pressed: "false", name: "Background picture", title: "Show the background picture" });
  const bounds = await button.boundingBox();
  expect(bounds!.width).toBeGreaterThanOrEqual(24);
  expect(bounds!.height).toBeGreaterThanOrEqual(24);

  await button.click();
  expect(await state()).toMatchObject({ scene: "shown", stored: "shown", picture: "picture", veil: "block", pressed: "true", title: "Hide the background picture" });
  await page.screenshot({ path: evidencePath("theme/scene-shown.png"), animations: "disabled" });

  // A reload keeps the selection.
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  expect(await state()).toMatchObject({ scene: "shown", picture: "picture", pressed: "true" });

  await button.click();
  expect(await state()).toMatchObject({ scene: "hidden", stored: "hidden", picture: "lines", pressed: "false" });
  await page.screenshot({ path: evidencePath("theme/scene-hidden.png"), animations: "disabled" });

  // Away from a hover, the chick moves on a timer of 10 seconds.
  await page.mouse.move(700, 600);
  const motion = await page.locator(".brand-mark .chick").evaluate((node) => {
    const style = getComputedStyle(node);
    return { name: style.animationName, duration: style.animationDuration, count: style.animationIterationCount };
  });
  expect(motion).toEqual({ name: "chick-peek", duration: "10s", count: "infinite" });
  await page.evaluate(() => localStorage.removeItem("mc.scene"));
}, 30000);

test("The theme button selects a theme, and the browser keeps the selection", async () => {
  const { page } = smoke;
  const button = page.getByTestId("theme-button");
  const bounds = await button.boundingBox();
  expect(bounds!.width).toBeGreaterThanOrEqual(24);
  expect(bounds!.height).toBeGreaterThanOrEqual(24);

  await button.click();
  await until("dark");
  expect(await shown()).toMatchObject({ theme: "dark", stored: "dark", pressed: "true" });
  expect(await button.locator(".sun").isVisible()).toBe(true);
  expect(await button.locator(".moon").isVisible()).toBe(false);

  // The selection wins over the theme of the system, and a reload keeps it.
  await page.emulateMedia({ colorScheme: "light" });
  await gotoReady(smoke);
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  expect(await shown()).toMatchObject({ theme: "dark", stored: "dark", pressed: "true", title: "Use the light theme" });

  // The keyboard reaches the button and shows its focus.
  await button.focus();
  await page.keyboard.press("Tab");
  await page.keyboard.press("Shift+Tab");
  const focus = await button.evaluate((node) => {
    const style = getComputedStyle(node);
    return { active: node === document.activeElement, visible: node.matches(":focus-visible"), width: parseFloat(style.outlineWidth), style: style.outlineStyle };
  });
  expect(focus).toMatchObject({ active: true, visible: true });
  expect(focus.width).toBeGreaterThanOrEqual(2);
  expect(focus.style).not.toBe("none");
  await page.keyboard.press("Enter");
  await until("light");
  expect(await shown()).toMatchObject({ theme: "light", stored: "light", pressed: "false" });
  expect(await button.locator(".moon").isVisible()).toBe(true);

  // A stored selection of light holds in a dark system.
  await page.emulateMedia({ colorScheme: "dark" });
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  expect((await shown()).theme).toBe("light");
  await page.evaluate(() => localStorage.removeItem("mc.theme"));
  await page.emulateMedia({ colorScheme: "light" });
}, 30000);

test("Each main page keeps in the dark theme the text contrast of the light theme", async () => {
  const { page } = smoke;
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  for (const name of ["board", "collie", "review", "pipeline", "configs", "logs"]) {
    await page.emulateMedia({ colorScheme: "light" });
    await until("light");
    await page.getByTestId(`tab-${name}`).click();
    await page.getByTestId(`${name}-pane`).waitFor();
    if (name === "board") await page.getByTestId("board-card").first().waitFor();
    if (name === "collie") await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement).disabled);
    if (name === "review") await page.waitForFunction(() => !document.getElementById("review-empty")?.hidden);
    if (name === "pipeline") await page.getByTestId("topology-node-worker").waitFor();
    if (name === "configs") await page.getByTestId("config-section-system").waitFor();
    if (name === "logs") await page.waitForFunction(() => document.getElementById("logs-lines")?.textContent?.includes("The Project state is current."));
    await page.screenshot({ path: evidencePath(`theme/${name}-light.png`), fullPage: true, animations: "disabled" });
    expect(await lostContrast(), `the ${name} page`).toEqual([]);
    await page.screenshot({ path: evidencePath(`theme/${name}-dark.png`), fullPage: true, animations: "disabled" });
  }
  await page.emulateMedia({ colorScheme: "light" });
  await until("light");
  await page.getByTestId("tab-board").click();
  await page.locator(`[data-testid="board-card"][data-task="${taskID}"]`).getByTestId("board-card-open").click();
  await page.getByTestId("board-record-controls").waitFor();
  expect(await lostContrast(), "the task dialog").toEqual([]);
  await page.screenshot({ path: evidencePath("theme/task-dark.png"), animations: "disabled" });
  await page.keyboard.press("Escape");
  await page.getByTestId("task-dialog").waitFor({ state: "hidden" });
  await page.emulateMedia({ colorScheme: "light" });
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 120000);
