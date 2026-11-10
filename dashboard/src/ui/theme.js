// theme.js — the light theme and the dark theme of the dashboard. The
// operator selects a theme with the theme button, and the browser keeps the
// selection. Without a selection, the dashboard uses the light theme.
// The page head sets the theme before the first paint; this module keeps it
// current after that. The logo button shows and hides the background picture
// in the same way; the picture is hidden until the operator shows it.

export const THEME_KEY = "mc.theme";
const THEMES = ["light", "dark"];

/** The theme to show: the stored selection, or the light theme. */
export function resolveTheme(stored) {
  if (THEMES.includes(stored)) return stored;
  return "light";
}

/** The state of the theme button for a theme. The tooltip names the action. */
export function themeControl(theme) {
  const dark = theme === "dark";
  return { pressed: dark, title: dark ? "Use the light theme" : "Use the dark theme" };
}

const listeners = new Set();

/** The theme that the page shows. */
export function currentTheme(root = document.documentElement) {
  return root.dataset.theme === "dark" ? "dark" : "light";
}

/** Calls `listener` with the new theme after each theme change. */
export function onThemeChange(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const mastColor = (root) => getComputedStyle(root).getPropertyValue("--mast").trim();

/** Connects the theme button and the stored selection. */
export function mountTheme({
  button,
  root = document.documentElement,
  storage = localStorage,
  meta = document.querySelector('meta[name="theme-color"]'),
  page = window,
  mast = mastColor,
}) {
  const stored = () => {
    try { return storage.getItem(THEME_KEY); } catch { return null; }
  };
  const show = (theme) => {
    const changed = root.dataset.theme !== theme;
    root.dataset.theme = theme;
    const control = themeControl(theme);
    button.setAttribute("aria-pressed", String(control.pressed));
    button.title = control.title;
    // The browser frame takes the color of the masthead.
    const color = mast(root);
    if (color) meta?.setAttribute("content", color);
    if (changed) for (const listener of listeners) listener(theme);
  };
  const follow = () => show(resolveTheme(stored()));
  follow();
  // A selection in another dashboard tab applies in this tab.
  page.addEventListener("storage", (event) => { if (event.key === THEME_KEY) follow(); });
  button.addEventListener("click", () => {
    const next = currentTheme(root) === "dark" ? "light" : "dark";
    try { storage.setItem(THEME_KEY, next); } catch {}
    show(next);
  });
}

export const SCENE_KEY = "mc.scene";

/** The state of the background picture: the stored selection, or hidden. */
export function resolveScene(stored) {
  return stored === "shown" ? "shown" : "hidden";
}

/** The state of the logo button for a picture state. The tooltip names the action. */
export function sceneControl(scene) {
  const shown = scene === "shown";
  return { pressed: shown, title: shown ? "Hide the background picture" : "Show the background picture" };
}

/** Connects the logo button to the background picture and the stored selection. */
export function mountScene({ button, root = document.documentElement, storage = localStorage, page = window }) {
  const stored = () => {
    try { return storage.getItem(SCENE_KEY); } catch { return null; }
  };
  const show = (scene) => {
    root.dataset.scene = scene;
    const control = sceneControl(scene);
    button.setAttribute("aria-pressed", String(control.pressed));
    button.title = control.title;
  };
  show(resolveScene(stored()));
  // A selection in another dashboard tab applies in this tab.
  page.addEventListener("storage", (event) => { if (event.key === SCENE_KEY) show(resolveScene(stored())); });
  button.addEventListener("click", () => {
    const next = root.dataset.scene === "shown" ? "hidden" : "shown";
    try { storage.setItem(SCENE_KEY, next); } catch {}
    show(next);
  });
}
