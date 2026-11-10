import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { selectTheme, bootSmoke, evidencePath, gotoReady, mc, seedProject, teardown, type Smoke } from "./harness";

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
    picture: getComputedStyle(document.querySelector(".scene-art")!).backgroundImage,
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
  await selectTheme(smoke, "light");
  await until("light");
  const light = new Map(await contrasts());
  await selectTheme(smoke, "dark");
  await until("dark");
  const lost: string[] = [];
  for (const [text, ratio] of await contrasts()) {
    const before = light.get(text);
    if (before !== undefined && ratio < Math.min(before, 4.5) - 0.05) lost.push(`${text}: ${before.toFixed(2)} in light, ${ratio.toFixed(2)} in dark`);
  }
  return lost;
}

test("Without a selection, the dashboard uses the light theme on either system theme", async () => {
  const { page } = smoke;
  for (const colorScheme of ["dark", "light"] as const) {
    await page.emulateMedia({ colorScheme });
    await page.reload();
    await page.waitForSelector("body[data-ready]");
    expect(await shown()).toMatchObject({ theme: "light", stored: null, scheme: "light", pressed: "false",
      name: "Dark theme", title: "Use the dark theme", scene: "hidden" });
  }
  const light = await shown();
  expect(light.picture).toContain("data:image/svg+xml");
  expect(light.landscape).toContain("/landscape.png");
  expect(light.frame).toBe(light.mast);
  for (const [name, type] of [["/landscape.png", "image/png"], ["/landscape-dark.png", "image/png"],
    ["/scene-parts.webp", "image/webp"], ["/scene-parts-dark.webp", "image/webp"]]) {
    const picture = await fetch(`${smoke.server.url}${name}`);
    expect(picture.status).toBe(200);
    expect(picture.headers.get("content-type")).toBe(type);
  }
}, 30000);

/** The picture state of the page: the picture, or only the tile lines. */
const scene = () => smoke.page.evaluate(() => {
  const control = document.getElementById("scene-button")!;
  const picture = getComputedStyle(document.querySelector(".scene-picture")!);
  const lines = getComputedStyle(document.querySelector(".scene-art")!).backgroundImage.includes("data:image/svg+xml");
  return {
    scene: document.documentElement.dataset.scene,
    stored: localStorage.getItem("mc.scene"),
    picture: picture.backgroundImage.includes("/landscape.png") && picture.opacity === "1" ? "picture"
      : lines && (picture.backgroundImage === "none" || picture.opacity === "0") ? "lines" : "none",
    veil: getComputedStyle(document.body, "::after").display,
    pressed: control.getAttribute("aria-pressed"),
    name: control.getAttribute("aria-label"),
    title: control.title,
    motion: document.documentElement.dataset.sceneMotion ?? null,
    parts: [...document.querySelectorAll<HTMLElement>(".scene-cast .scene-part")].map((part) => part.dataset.part),
  };
});

/** Waits until the motion of the picture ends. */
const atRest = () => smoke.page.waitForFunction(() => !document.documentElement.dataset.sceneMotion
  && !document.getElementById("scene")!.dataset.hold && !document.querySelector(".scene-cast"));

test("The logo button shows and hides the background picture, and the browser keeps the selection", async () => {
  const { page } = smoke;
  const button = page.getByTestId("scene-button");
  expect(await scene()).toMatchObject({ scene: "hidden", stored: null, picture: "lines", veil: "block", pressed: "false", name: "Background picture", title: "Show the background picture" });
  const bounds = await button.boundingBox();
  expect(bounds!.width).toBeGreaterThanOrEqual(24);
  expect(bounds!.height).toBeGreaterThanOrEqual(24);

  await button.click();
  expect(await scene()).toMatchObject({ scene: "shown", stored: "shown", veil: "block", pressed: "true", title: "Hide the background picture" });
  await atRest();
  expect(await scene()).toMatchObject({ scene: "shown", picture: "picture", motion: null, parts: [] });
  await page.screenshot({ path: evidencePath("theme/scene-shown.png"), animations: "disabled" });

  // A reload keeps the selection and shows the picture without motion.
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  expect(await scene()).toMatchObject({ scene: "shown", picture: "picture", pressed: "true", motion: null, parts: [] });

  await button.click();
  expect(await scene()).toMatchObject({ scene: "hidden", stored: "hidden", pressed: "false" });
  await atRest();
  expect(await scene()).toMatchObject({ scene: "hidden", picture: "lines", motion: null, parts: [] });
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

test("A click on the logo button moves each part of the background picture by itself", async () => {
  const { page } = smoke;
  const button = page.getByTestId("scene-button");
  const names = ["shadow_egg", "shadow_nest", "shadow_shell", "shadow_chick", "egg", "egg_glow", "nest_back",
    "egg_top", "egg_left", "egg_right", "egg_shade", "nest_front", "shell_back", "chick", "shell_front", "cap", "crack_a", "crack_b"];
  /** The place of each part on the page, and the moment of the motion. */
  const places = () => page.evaluate(() => ({
    time: Number(document.querySelector<HTMLElement>(".scene-part")?.getAnimations()[0]?.currentTime ?? -1),
    rate: document.querySelector<HTMLElement>(".scene-part")?.getAnimations()[0]?.playbackRate ?? 0,
    sheet: getComputedStyle(document.querySelector(".scene-part")!).backgroundImage,
    boxes: Object.fromEntries([...document.querySelectorAll<HTMLElement>(".scene-part")].map((part) => {
      const box = part.getBoundingClientRect();
      return [part.dataset.part!, { top: box.top, bottom: box.bottom, left: box.left, right: box.right, opacity: Number(getComputedStyle(part).opacity) }];
    })),
  }));
  // The motion stops at a moment for a look, then goes on.
  const moment = (time: number) => page.evaluate((at) => {
    for (const animation of document.getElementById("scene")!.getAnimations({ subtree: true })) { animation.pause(); animation.currentTime = at; }
  }, time);
  const goOn = () => page.evaluate(() => { for (const animation of document.getElementById("scene")!.getAnimations({ subtree: true })) animation.play(); });
  await page.reload();
  await page.waitForSelector("body[data-ready]");
  expect(await scene()).toMatchObject({ scene: "hidden", picture: "lines" });

  // The show: the parts come one after the other, and the picture stays hidden until they are at rest.
  await button.click();
  await page.waitForFunction(() => document.documentElement.dataset.sceneMotion === "enter");
  expect(await scene()).toMatchObject({ scene: "shown", motion: "enter", parts: names });
  await moment(500);
  const early = await places();
  expect(early.sheet).toContain("/scene-parts.webp");
  expect(early.boxes.egg!.bottom).toBeGreaterThan(0);
  expect(early.boxes.nest_front!.bottom).toBeGreaterThan(0);
  for (const name of ["egg_top", "egg_left", "egg_right"]) expect(early.boxes[name]!.bottom, name).toBeLessThan(0);
  // The whole egg is to the left of the window.
  expect(early.boxes.cap!.right).toBeLessThan(0);
  expect(early.boxes.chick!.opacity).toBe(0);
  await page.screenshot({ path: evidencePath("theme/scene-motion-egg.png") });
  await moment(1700);
  const middle = await places();
  for (const name of ["egg_top", "egg_left", "egg_right"]) expect(middle.boxes[name]!.top, name).toBeGreaterThan(0);
  // The whole egg is at its place: the cap closes the shell, the crack is open, and the chick does not show.
  expect(middle.boxes.shell_front!.left).toBeGreaterThan(0);
  for (const name of ["shell_front", "cap", "crack_a", "crack_b"]) expect(middle.boxes[name]!.opacity, name).toBe(1);
  expect(middle.boxes.shell_back!.opacity).toBe(0);
  expect(middle.boxes.chick!.opacity).toBe(0);
  await page.screenshot({ path: evidencePath("theme/scene-motion-nest.png") });
  await moment(1960);
  const open = await places();
  // The cap is off the shell, and the chick is in the shell.
  expect(open.boxes.cap!.bottom).toBeLessThan(open.boxes.shell_front!.top);
  for (const name of ["crack_a", "crack_b"]) expect(open.boxes[name]!.opacity, name).toBe(0);
  for (const name of ["shell_back", "chick"]) expect(open.boxes[name]!.opacity, name).toBe(1);
  expect(open.boxes.chick!.left).toBeLessThan(open.boxes.nest_front!.left + 80);
  await page.screenshot({ path: evidencePath("theme/scene-motion-hatch.png") });
  await moment(2420);
  const late = await places();
  expect(late.boxes.chick!.opacity).toBe(1);
  expect(late.boxes.cap!.right).toBeLessThan(late.boxes.egg!.left);
  // The chick is in its jump over the nest.
  expect(late.boxes.chick!.bottom).toBeLessThan(late.boxes.nest_front!.top + 40);
  expect((await scene()).picture).toBe("lines");
  await page.screenshot({ path: evidencePath("theme/scene-motion-chick.png") });

  // A second click during the motion plays the motion back.
  await goOn();
  await button.click();
  expect(await scene()).toMatchObject({ scene: "hidden", stored: "hidden", motion: "exit", parts: names, pressed: "false" });
  expect((await places()).rate).toBeLessThan(0);
  await atRest();
  expect(await scene()).toMatchObject({ scene: "hidden", picture: "lines", parts: [] });

  // The full show ends on the picture, at the place of the parts.
  await button.click();
  await page.waitForFunction(() => document.documentElement.dataset.sceneMotion === "enter");
  await moment(1e6);
  const rest = await places();
  const art = await page.locator(".scene-art").boundingBox();
  const unit = art!.width / 1672;
  expect(rest.boxes.chick!.left).toBeCloseTo(art!.x + 1240 * unit, 0);
  expect(rest.boxes.chick!.top).toBeCloseTo(art!.y + 470 * unit, 0);
  expect(rest.boxes.egg!.left).toBeCloseTo(art!.x + 311 * unit, 0);
  await goOn();
  await atRest();
  expect(await scene()).toMatchObject({ scene: "shown", picture: "picture", parts: [] });

  // The hide: each part leaves, and the page shows only the tile lines.
  await button.click();
  await page.waitForFunction(() => document.documentElement.dataset.sceneMotion === "exit");
  expect(await scene()).toMatchObject({ scene: "hidden", motion: "exit", parts: names });
  await moment(1000);
  const leaving = await places();
  // The cap closes the shell over the chick.
  expect(leaving.boxes.chick!.opacity).toBe(0);
  expect(leaving.boxes.shell_back!.opacity).toBe(0);
  for (const name of ["cap", "crack_a", "crack_b"]) expect(leaving.boxes[name]!.opacity, name).toBe(1);
  for (const name of ["egg_top", "egg_left", "egg_right"]) expect(leaving.boxes[name]!.bottom, name).toBeLessThan(0);
  expect(leaving.boxes.egg!.bottom).toBeGreaterThan(0);
  // The parts stand in for the picture.
  expect(await page.locator(".scene-picture").evaluate((node) => getComputedStyle(node).opacity)).toBe("0");
  await page.screenshot({ path: evidencePath("theme/scene-motion-hide.png") });
  await goOn();
  await atRest();
  expect(await scene()).toMatchObject({ scene: "hidden", picture: "lines", parts: [] });

  // With reduced motion, a click changes the picture at once.
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await button.click();
  expect(await scene()).toMatchObject({ scene: "shown", picture: "picture", motion: null, parts: [] });
  await button.click();
  expect(await scene()).toMatchObject({ scene: "hidden", picture: "lines", motion: null, parts: [] });
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "no-preference" });
  await page.evaluate(() => localStorage.removeItem("mc.scene"));
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

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
    await selectTheme(smoke, "light");
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
  await selectTheme(smoke, "light");
  await until("light");
  await page.getByTestId("tab-board").click();
  await page.locator(`[data-testid="board-card"][data-task="${taskID}"]`).getByTestId("board-card-open").click();
  await page.getByTestId("board-record-controls").waitFor();
  expect(await lostContrast(), "the task dialog").toEqual([]);
  await page.screenshot({ path: evidencePath("theme/task-dark.png"), animations: "disabled" });
  await page.keyboard.press("Escape");
  await page.getByTestId("task-dialog").waitFor({ state: "hidden" });
  await selectTheme(smoke, "light");
  expect(smoke.browserLog.filter((line) => line.startsWith("pageerror:"))).toEqual([]);
}, 120000);
