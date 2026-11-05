import { describe, expect, test } from "bun:test";

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { SCENE_KEY, THEME_KEY, currentTheme, mountScene, mountTheme, onThemeChange, resolveScene, resolveTheme, sceneControl, themeControl } from "./theme.js";

const read = (name: string) => readFileSync(join(import.meta.dir, name), "utf8");

describe("resolveTheme", () => {
  test("uses the stored selection", () => {
    expect(resolveTheme("dark", false)).toBe("dark");
    expect(resolveTheme("light", true)).toBe("light");
  });

  test("uses the theme of the system without a valid selection", () => {
    expect(resolveTheme(null, true)).toBe("dark");
    expect(resolveTheme(null, false)).toBe("light");
    expect(resolveTheme("sepia", true)).toBe("dark");
    expect(resolveTheme("", false)).toBe("light");
  });
});

describe("themeControl", () => {
  test("the tooltip names the theme that a click selects", () => {
    expect(themeControl("light")).toEqual({ pressed: false, title: "Use the dark theme" });
    expect(themeControl("dark")).toEqual({ pressed: true, title: "Use the light theme" });
  });
});

describe("the page head", () => {
  // The head script runs before the modules load, so it repeats the rule of
  // resolveTheme. The two must select the same theme.
  const script = /<script>([\s\S]*?)<\/script>/.exec(read("index.html"))![1]!;
  const head = (stored: Record<string, string>, systemDark: boolean, blocked = false) => {
    const root = { dataset: {} as Record<string, string> };
    const storage = { getItem: (key: string) => { if (blocked) throw new Error("blocked"); return stored[key] ?? null; } };
    new Function("localStorage", "matchMedia", "document", script)(
      storage, () => ({ matches: systemDark }), { documentElement: root });
    return root.dataset;
  };
  const run = (stored: string | null, systemDark: boolean, blocked = false) =>
    head(stored === null ? {} : { [THEME_KEY]: stored }, systemDark, blocked).theme;

  test("sets the theme that resolveTheme selects", () => {
    for (const stored of ["light", "dark", "sepia", null]) {
      for (const systemDark of [true, false]) {
        expect(run(stored, systemDark)).toBe(resolveTheme(stored, systemDark));
      }
    }
  });

  test("uses the theme of the system when the browser blocks storage", () => {
    expect(run("light", true, true)).toBe("dark");
  });

  test("shows the background picture only after a stored selection", () => {
    expect(head({}, false).scene).toBe("hidden");
    expect(head({ [SCENE_KEY]: "shown" }, false).scene).toBe("shown");
    expect(head({ [SCENE_KEY]: "hidden" }, false).scene).toBe("hidden");
    expect(head({ [SCENE_KEY]: "shown" }, false, true).scene).toBe("hidden");
  });

  test("runs before the stylesheet loads", () => {
    const html = read("index.html");
    expect(html.indexOf("<script>")).toBeGreaterThan(-1);
    expect(html.indexOf("<script>")).toBeLessThan(html.indexOf('<link rel="stylesheet"'));
  });
});

describe("mountTheme", () => {
  function page({ stored = null as string | null, systemDark = false } = {}) {
    const values = new Map<string, string>(stored === null ? [] : [[THEME_KEY, stored]]);
    const attributes = new Map<string, string>();
    const handlers: Record<string, (event?: unknown) => void> = {};
    const root = { dataset: {} as Record<string, string> };
    const button = {
      title: "",
      setAttribute: (name: string, value: string) => void attributes.set(name, value),
      addEventListener: (_: string, handler: () => void) => { handlers.click = handler; },
    };
    const media = { matches: systemDark, addEventListener: (_: string, handler: () => void) => { handlers.system = handler; } };
    const meta = { content: "", setAttribute(_: string, value: string) { this.content = value; } };
    const seen: string[] = [];
    const stop = onThemeChange((theme: string) => seen.push(theme));
    mountTheme({
      button, root, media, meta,
      storage: { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => void values.set(key, value) },
      page: { addEventListener: (_: string, handler: (event: unknown) => void) => { handlers.storage = handler as (event?: unknown) => void; } },
      mast: (node: typeof root) => (node.dataset.theme === "dark" ? "#14304a" : "#1f5078"),
    });
    return { root, button, media, meta, values, attributes, handlers, seen, stop };
  }

  test("shows the theme of the system and the state of the button", () => {
    const dark = page({ systemDark: true });
    expect(dark.root.dataset.theme).toBe("dark");
    expect(currentTheme(dark.root)).toBe("dark");
    expect(dark.attributes.get("aria-pressed")).toBe("true");
    expect(dark.button.title).toBe("Use the light theme");
    expect(dark.meta.content).toBe("#14304a");
    dark.stop();
    const light = page();
    expect(light.root.dataset.theme).toBe("light");
    expect(light.attributes.get("aria-pressed")).toBe("false");
    expect(light.meta.content).toBe("#1f5078");
    light.stop();
  });

  test("a click selects the other theme and stores the selection", () => {
    const view = page();
    view.handlers.click!();
    expect(view.root.dataset.theme).toBe("dark");
    expect(view.values.get(THEME_KEY)).toBe("dark");
    expect(view.attributes.get("aria-pressed")).toBe("true");
    expect(view.meta.content).toBe("#14304a");
    view.handlers.click!();
    expect(view.root.dataset.theme).toBe("light");
    expect(view.values.get(THEME_KEY)).toBe("light");
    expect(view.seen).toEqual(["light", "dark", "light"]);
    view.stop();
  });

  test("a change of the system theme applies only without a selection", () => {
    const free = page();
    free.media.matches = true;
    free.handlers.system!();
    expect(free.root.dataset.theme).toBe("dark");
    expect(free.values.has(THEME_KEY)).toBe(false);
    free.stop();
    const selected = page({ stored: "light" });
    selected.media.matches = true;
    selected.handlers.system!();
    expect(selected.root.dataset.theme).toBe("light");
    selected.stop();
  });

  test("a selection in another tab applies in this tab", () => {
    const view = page();
    view.values.set(THEME_KEY, "dark");
    view.handlers.storage!({ key: "mc.project" });
    expect(view.root.dataset.theme).toBe("light");
    view.handlers.storage!({ key: THEME_KEY });
    expect(view.root.dataset.theme).toBe("dark");
    view.stop();
  });

  test("a listener hears a change and no repeat of the same theme", () => {
    const view = page({ stored: "dark" });
    view.handlers.system!();
    view.handlers.storage!({ key: THEME_KEY });
    expect(view.seen).toEqual(["dark"]);
    view.stop();
  });
});

describe("the background picture", () => {
  test("is hidden without a valid selection", () => {
    expect(resolveScene(null)).toBe("hidden");
    expect(resolveScene("on")).toBe("hidden");
    expect(resolveScene("shown")).toBe("shown");
  });

  test("the tooltip names the action of a click", () => {
    expect(sceneControl("hidden")).toEqual({ pressed: false, title: "Show the background picture" });
    expect(sceneControl("shown")).toEqual({ pressed: true, title: "Hide the background picture" });
  });

  function page(stored: string | null = null) {
    const values = new Map<string, string>(stored === null ? [] : [[SCENE_KEY, stored]]);
    const attributes = new Map<string, string>();
    const handlers: Record<string, (event?: unknown) => void> = {};
    const root = { dataset: {} as Record<string, string> };
    const button = {
      title: "",
      setAttribute: (name: string, value: string) => void attributes.set(name, value),
      addEventListener: (_: string, handler: () => void) => { handlers.click = handler; },
    };
    mountScene({
      button, root,
      storage: { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => void values.set(key, value) },
      page: { addEventListener: (_: string, handler: (event: unknown) => void) => { handlers.storage = handler as (event?: unknown) => void; } },
    });
    return { root, button, values, attributes, handlers };
  }

  test("a click shows the picture, and a second click hides it", () => {
    const view = page();
    expect(view.root.dataset.scene).toBe("hidden");
    expect(view.attributes.get("aria-pressed")).toBe("false");
    view.handlers.click!();
    expect(view.root.dataset.scene).toBe("shown");
    expect(view.values.get(SCENE_KEY)).toBe("shown");
    expect(view.attributes.get("aria-pressed")).toBe("true");
    expect(view.button.title).toBe("Hide the background picture");
    view.handlers.click!();
    expect(view.root.dataset.scene).toBe("hidden");
    expect(view.values.get(SCENE_KEY)).toBe("hidden");
  });

  test("a stored selection and a selection in another tab apply", () => {
    const view = page("shown");
    expect(view.root.dataset.scene).toBe("shown");
    view.values.set(SCENE_KEY, "hidden");
    view.handlers.storage!({ key: THEME_KEY });
    expect(view.root.dataset.scene).toBe("shown");
    view.handlers.storage!({ key: SCENE_KEY });
    expect(view.root.dataset.scene).toBe("hidden");
  });
});

// The color tokens of each theme, read from the stylesheet. The first block of
// a theme can share its selector list with other selectors.
function tokens(css: string, selector: string) {
  const starts = [`\n${selector} {`, `\n${selector},`].map((head) => css.indexOf(head)).filter((at) => at >= 0);
  if (starts.length === 0) throw new Error(`The stylesheet has no ${selector} block.`);
  const start = Math.min(...starts);
  const block = css.slice(start, css.indexOf("\n}\n", start));
  return new Map([...block.matchAll(/^\s*(--[\w-]+):\s*([^;]+);/gm)].map((found) => [found[1]!, found[2]!.trim()]));
}

function luminance(hex: string) {
  const full = hex.length === 4 ? `#${[...hex.slice(1)].map((digit) => digit + digit).join("")}` : hex;
  const [red, green, blue] = [1, 3, 5].map((at) => {
    const channel = parseInt(full.slice(at, at + 2), 16) / 255;
    return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * red! + 0.7152 * green! + 0.0722 * blue!;
}

function contrast(first: string, second: string) {
  const [high, low] = [luminance(first), luminance(second)].sort((a, b) => b - a);
  return (high! + 0.05) / (low! + 0.05);
}

describe("the color tokens", () => {
  const css = read("style.css");
  const light = tokens(css, ":root");
  const dark = new Map([...light, ...tokens(css, ':root[data-theme="dark"]')]);
  const color = (theme: Map<string, string>, name: string): string => {
    const value = theme.get(name);
    if (value === undefined) throw new Error(`The stylesheet has no ${name} token.`);
    const reference = /^var\((--[\w-]+)\)$/.exec(value);
    if (reference) return color(theme, reference[1]!);
    if (!/^#[0-9a-f]{3}([0-9a-f]{3})?$/i.test(value)) throw new Error(`${name} is not a plain color: ${value}`);
    return value;
  };
  const SURFACES = ["--paper", "--surface", "--surface-2", "--surface-3", "--surface-hi", "--select", "--field", "--orange-surface", "--brick-surface"];
  const INKS = ["--ink", "--ink-2", "--navy-ink", "--orange-ink", "--sprout-ink", "--straw-ink", "--brick-ink", "--stone-ink", "--sky-ink"];
  const FILLS = ["--navy", "--navy-hi", "--navy-lo", "--orange-fill", "--sprout", "--brick", "--panel-head"];

  for (const [name, theme] of [["light", light], ["dark", dark]] as const) {
    test(`each text color of the ${name} theme has a contrast of 4.5:1 on each surface`, () => {
      for (const ink of INKS) {
        for (const surface of SURFACES) {
          expect(contrast(color(theme, ink), color(theme, surface)), `${ink} on ${surface}`).toBeGreaterThanOrEqual(4.5);
        }
      }
      for (const surface of ["--paper", "--surface", "--surface-2", "--field"]) {
        expect(contrast(color(theme, "--ink-3"), color(theme, surface)), `--ink-3 on ${surface}`).toBeGreaterThanOrEqual(4.5);
        expect(contrast(color(theme, "--ink-4"), color(theme, surface)), `--ink-4 on ${surface}`).toBeGreaterThanOrEqual(4.5);
      }
      expect(contrast(color(theme, "--field-disabled-ink"), color(theme, "--field-disabled"))).toBeGreaterThanOrEqual(4.5);
    });

    test(`white text has a contrast of 4.5:1 on each fill of the ${name} theme`, () => {
      for (const fill of FILLS) {
        expect(contrast("#ffffff", color(theme, fill)), fill).toBeGreaterThanOrEqual(4.5);
      }
      expect(contrast(color(theme, "--mast-ink"), color(theme, "--mast")), "--mast-ink on --mast").toBeGreaterThanOrEqual(4.5);
      for (const action of ["--action-fill", "--action-hover"]) {
        expect(contrast(color(theme, "--action-ink"), color(theme, action)), `--action-ink on ${action}`).toBeGreaterThanOrEqual(4.5);
      }
    });

    test(`the focus ring and each input border of the ${name} theme have a contrast of 3:1`, () => {
      for (const surface of ["--paper", "--surface", "--surface-2", "--surface-3"]) {
        expect(contrast(color(theme, "--focus"), color(theme, surface)), `--focus on ${surface}`).toBeGreaterThanOrEqual(3);
      }
      for (const surface of ["--field", "--surface"]) {
        expect(contrast(color(theme, "--field-line"), color(theme, surface)), `--field-line on ${surface}`).toBeGreaterThanOrEqual(3);
      }
      expect(contrast(color(theme, "--field-disabled-line"), color(theme, "--field-disabled"))).toBeGreaterThanOrEqual(3);
      if (contrast(color(theme, "--panel-head"), color(theme, "--surface")) < 3) {
        for (const surface of ["--panel-head", "--surface"]) {
          expect(contrast(color(theme, "--panel-line"), color(theme, surface)), `--panel-line on ${surface}`).toBeGreaterThanOrEqual(3);
        }
      }
    });
  }

  test("the dark theme changes each color token that names paper, ink, or a surface", () => {
    for (const token of ["--paper", "--surface", "--surface-2", "--surface-3", "--ink", "--ink-2", "--ink-3", "--ink-4", "--field", "--landscape", "--grid", "--veil"]) {
      const actual = (theme: Map<string, string>) => ["--landscape", "--grid", "--veil"].includes(token) ? theme.get(token) : color(theme, token);
      expect(actual(dark), token).not.toBe(actual(light));
    }
  });
});
