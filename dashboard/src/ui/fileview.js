// fileview.js — the file viewer of the Setup Journal. A viewer shows the
// Dockerfile, one required build file, or the setup script of a proposal.
// It compares the recorded proposal contents with the accepted version through
// the local Pierre Diffs modules. A viewer keeps its state while the journal
// refreshes.

import { el, tag } from "./dom.js";

/** A view of more lines opens collapsed at this number of lines. */
export const COLLAPSED_LINES = 20;

const versions = config => new Map([
  ...(config?.dockerfile ? [config.dockerfile] : []), ...(config?.build_files ?? []),
].map(file => [file.path, file.content]));

/** The change type of a file. An absent version is null. */
export function fileChange(before, after) {
  if (before === null) return "Added";
  if (after === null) return "Removed";
  return before === after ? "Unchanged" : "Changed";
}

/** The Dockerfile and the required build files of a proposal. Each file has
 * its accepted contents, its proposed contents, and its change type. The
 * Dockerfile also has its language. A first proposal has no accepted
 * configuration. */
export function proposalFiles(accepted, proposed) {
  const old = versions(accepted), current = versions(proposed);
  const dockerfile = proposed?.dockerfile?.path ?? accepted?.dockerfile?.path ?? null;
  const files = [...new Set([...current.keys(), ...old.keys()])].map(path => {
    const before = old.get(path) ?? null, after = current.get(path) ?? null;
    return { path, before, after, change: fileChange(before, after) };
  });
  const primary = files.find(file => file.path === dockerfile);
  return { dockerfile: primary ? { ...primary, lang: "dockerfile" } : null, buildFiles: files.filter(file => file.path !== dockerfile) };
}

/** The setup script of a proposal as a file, or null when neither version
 * has a script. The file header shows `label` in place of the path. A typed
 * script often has no final line break, and that difference has no effect,
 * so each version ends with one. */
export function setupScriptFile(accepted, proposed) {
  const script = config => {
    const text = config?.setup?.command;
    return text ? text.replace(/(\r\n|\r|\n)?$/, match => match || "\n") : null;
  };
  const before = script(accepted), after = script(proposed);
  if (before === null && after === null) return null;
  return { path: "Setup script", label: "Bash", before, after, change: fileChange(before, after), lang: "shellscript" };
}

/** The Pierre input of one file view: a comparison of two versions, or one
 * version without change marks. `complete` selects the proposed file. Pierre
 * selects the language from the base name when the file has no language. */
export function fileViewInput(file, complete = false) {
  const name = file.path.split("/").pop();
  const version = contents => contents === null ? null : { name, contents, ...(file.lang ? { lang: file.lang } : {}) };
  if (complete || file.change === "Unchanged") return { file: version(file.after) };
  return { oldFile: version(file.before), newFile: version(file.after) };
}

const LINE_BREAK = /\r\n|\r|\n/;
const FINAL_BREAK = /(\r\n|\r|\n)$/;

/** The number of lines of a file view. A comparison counts the lines of its
 * hunks. A final line break starts no line. */
export function viewLines(input, comparison = null) {
  if (comparison) return comparison.hunks.reduce((sum, hunk) => sum + hunk.unifiedLineCount, 0);
  const text = input.file.contents;
  return text === "" ? 0 : text.split(LINE_BREAK).length - (FINAL_BREAK.test(text) ? 1 : 0);
}

/** The number of added lines and removed lines of a comparison. */
export function changeCounts(comparison) {
  return comparison.hunks.reduce((sum, hunk) => ({ added: sum.added + hunk.additionLines, removed: sum.removed + hunk.deletionLines }),
    { added: 0, removed: 0 });
}

const lineCount = (number, change) => `${number} ${change} line${number === 1 ? "" : "s"}`;

const rgb = color => {
  const hex = color.slice(1);
  return [0, 2, 4].map(at => parseInt(hex.slice(at, at + 2), 16));
};
const hexColor = channels => `#${channels.map(value => Math.round(value).toString(16).padStart(2, "0")).join("")}`;
const mix = (from, to, share) => from.map((value, at) => value + (to[at] - value) * share);

/** The relative luminance of an sRGB color. */
export function luminance(channels) {
  const [red, green, blue] = channels.map(value => {
    const channel = value / 255;
    return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
}

/** The contrast ratio of two sRGB colors. */
export function contrast(first, second) {
  const [high, low] = [luminance(first), luminance(second)].sort((a, b) => b - a);
  return (high + 0.05) / (low + 0.05);
}

/** The line backgrounds of a Pierre theme: the code background and its mixes
 * with the added-line color and the removed-line color. Pierre mixes in Lab
 * with 12 percent of the line color in a light theme and 20 percent in a dark
 * theme. An sRGB mix gives a slightly lower contrast estimate. */
export function lineBackgrounds(theme) {
  const ground = rgb(theme.colors["editor.background"]);
  const share = theme.type === "dark" ? 0.2 : 0.12;
  return [ground, ...["gitDecoration.addedResourceForeground", "gitDecoration.deletedResourceForeground"]
    .map(name => mix(ground, rgb(theme.colors[name]), share))];
}

/** The minimum contrast of a token color with each line background. The value
 * holds a margin over 4.5:1 for the estimate of the backgrounds. */
export const TOKEN_CONTRAST = 4.6;

/** A copy of a Pierre theme with readable token colors. A dark theme moves
 * each low-contrast token color toward white, and a light theme moves it toward
 * black. The line colors stay the same as in the Files changed view. */
export function readableTheme(theme, name) {
  const grounds = lineBackgrounds(theme);
  const limit = theme.type === "dark" ? [255, 255, 255] : [0, 0, 0];
  const readable = color => {
    if (typeof color !== "string" || !/^#[0-9a-f]{6}$/i.test(color)) return color;
    for (let step = 0; step <= 50; step++) {
      const candidate = mix(rgb(color), limit, step / 50);
      if (grounds.every(ground => contrast(candidate, ground) >= TOKEN_CONTRAST)) return step === 0 ? color : hexColor(candidate);
    }
    return hexColor(limit);
  };
  return {
    ...theme, name, fg: readable(theme.fg),
    settings: theme.settings.map(rule => rule.settings?.foreground
      ? { ...rule, settings: { ...rule.settings, foreground: readable(rule.settings.foreground) } } : rule),
  };
}

const THEMES = { light: "pierre-light-readable", dark: "pierre-dark-readable" };
const pierreAddress = "./pierre/pierre.js";
let library = null;
let attempts = 0;

/** The local Pierre modules with the readable themes. A browser keeps the
 * failure of a module address for the page, so each later attempt reads the
 * entry module at a new address. */
function loadPierre() {
  library ??= import(attempts++ === 0 ? pierreAddress : `${pierreAddress}?attempt=${attempts}`).then(async modules => {
    for (const [type, name] of Object.entries(THEMES)) {
      const theme = readableTheme(await modules.resolveTheme(`pierre-${type}`), name);
      modules.registerCustomTheme(name, () => Promise.resolve(theme));
    }
    return modules;
  }).catch(err => {
    library = null;
    throw err;
  });
  return library;
}

const VIEW_OPTIONS = {
  theme: THEMES,
  overflow: "wrap",
  disableFileHeader: true,
  // The color scheme of the page selects the theme colors. A separator only
  // states the number of unchanged lines. A click on it expands nothing,
  // because the line count and the collapsed range count the rows of the
  // comparison.
  unsafeCSS: `:host { color-scheme: inherit; }
[data-line] { padding-inline-start: 1.5ch; }
[data-expand-button] { display: none; }
[data-separator-content] { grid-column: 2; pointer-events: none; }`,
};
// A bar marks each changed line: solid for an added line, striped for a
// removed line.
const DIFF_OPTIONS = { ...VIEW_OPTIONS, diffStyle: "unified", diffIndicators: "bars", hunkSeparators: "line-info-basic", lineDiffType: "none" };
const COLLAPSED_RANGE = { startingLine: 0, totalLines: COLLAPSED_LINES, bufferBefore: 0, bufferAfter: 0 };
const message = err => String(err instanceof Error ? err.message : err);
let serial = 0;

/** The file viewers of one page. `load` returns the Pierre modules. */
export function createFileViewers(load = loadPierre) {
  const viewers = new Map();
  return {
    /** Starts one journal render. Each viewer that the last render did not
     * use ends. */
    begin() {
      for (const [key, viewer] of viewers) {
        if (viewer.used) viewer.used = false;
        else {
          viewer.end();
          viewers.delete(key);
        }
      }
    },
    /** The node of one file viewer. A later render of the same file returns
     * the same node with its state. */
    node(file, { key, testid = null }) {
      const signature = JSON.stringify([file.path, file.change, file.before, file.after]);
      let viewer = viewers.get(key);
      if (viewer?.signature !== signature) {
        viewer?.end();
        viewer = fileViewer(file, testid, load);
        viewer.signature = signature;
        viewers.set(key, viewer);
      }
      viewer.used = true;
      viewer.show();
      return viewer.element;
    },
  };
}

function fileViewer(file, testid, load) {
  const s = { state: "idle", modules: null, error: "", collapsed: true, complete: false, view: null, ended: false };
  const note = text => el("p", { class: "file-view-note", role: "status", text });
  const code = el("div", { class: "file-view-code" }, note("Loading the file view…"));
  const count = el("span", { class: "file-view-count" });
  const expand = el("button", {
    type: "button", class: "btn small", testid: "setup-file-expand", onclick: () => {
      s.collapsed = !s.collapsed;
      paint();
      if (s.collapsed) footer.scrollIntoView({ block: "nearest" });
    },
  });
  const footer = el("div", { class: "file-view-footer", hidden: true }, count, expand);
  const toggle = file.change === "Changed" && el("button", {
    type: "button", class: "btn small", testid: "setup-file-complete", text: "Show complete file",
    onclick: () => {
      s.complete = !s.complete;
      toggle.textContent = s.complete ? "Show changes" : "Show complete file";
      paint();
    },
  });
  const counts = el("span", { class: "file-view-counts", testid: "setup-file-counts", hidden: true });
  // The unique key keeps the journal refresh from merging two viewers.
  const element = el("section", {
    class: "file-view", testid, data: { key: `file-view-${++serial}`, path: file.path, change: file.change, state: s.state },
  }, el("div", { class: "file-view-head" },
    file.label ? el("span", { class: "file-view-label", text: file.label }) : el("code", { class: "file-view-path", text: file.path }), counts, tag(file.change, "file-view-change"), toggle),
  code, footer);

  function showCounts({ added, removed }) {
    counts.hidden = false;
    counts.replaceChildren(...[
      added > 0 && el("span", { class: "file-view-added", "aria-hidden": "true", text: `+${added}` }),
      removed > 0 && el("span", { class: "file-view-removed", "aria-hidden": "true", text: `−${removed}` }),
      el("span", { class: "sr-only", text: `${lineCount(added, "added")}, ${lineCount(removed, "removed")}` }),
    ].filter(Boolean));
  }

  function start() {
    if (s.state !== "idle" || s.ended) return;
    s.state = "loading";
    load().then(modules => { s.modules = modules; s.state = "ready"; }, err => { s.error = message(err); s.state = "failed"; })
      .finally(() => { if (!s.ended) paint(); });
  }

  function endView() {
    s.view?.instance.cleanUp();
    s.view = null;
  }

  /** The exact proposed contents as literal code, with a statement and a retry. */
  function fallback() {
    const removed = file.after === null;
    return el("div", { class: "file-view-fallback", testid: "setup-file-fallback" },
      el("p", { role: "alert", text: removed
        ? "The comparison is unavailable. The proposal removes this file. The exact accepted contents follow."
        : "The comparison is unavailable. The exact proposed contents follow." }),
      s.error && el("p", { class: "file-view-error", text: s.error }),
      el("button", { type: "button", class: "btn small", testid: "setup-file-retry", text: "Retry", onclick: () => {
        s.state = "idle";
        s.error = "";
        paint();
        start();
      } }),
      el("pre", { class: "journal-code file-view-literal", tabindex: "0", "aria-label": `Exact contents of ${file.path}` },
        el("code", { text: (removed ? file.before : file.after) ?? "" })));
  }

  /** The Pierre view of the current mode. A mode change replaces the view. */
  function view() {
    if (s.view?.complete === s.complete) return s.view;
    endView();
    code.replaceChildren();
    const input = fileViewInput(file, s.complete);
    const { File, FileDiff, parseDiffFromFile } = s.modules;
    const comparison = input.file ? null : parseDiffFromFile(input.oldFile, input.newFile);
    s.view = {
      complete: s.complete, input, comparison, lines: viewLines(input, comparison),
      instance: comparison ? new FileDiff(DIFF_OPTIONS) : new File(VIEW_OPTIONS),
    };
    return s.view;
  }

  function paint() {
    if (s.ended) return;
    element.dataset.state = s.state;
    footer.hidden = true;
    if (s.state === "idle" || s.state === "loading") {
      endView();
      code.replaceChildren(note("Loading the file view…"));
      return;
    }
    if (s.state === "ready") {
      try {
        const current = view();
        if (current.comparison) showCounts(changeCounts(current.comparison));
        const long = current.lines > COLLAPSED_LINES;
        element.dataset.collapsed = String(long && s.collapsed);
        if (current.lines === 0) {
          code.replaceChildren(note("The file is empty."));
          return;
        }
        // Pierre shows an empty line after a final line break. The line count
        // and the comparison have no such line.
        const file = current.input.file && { ...current.input.file, contents: current.input.file.contents.replace(FINAL_BREAK, "") };
        current.instance.render({
          ...(current.comparison ? { fileDiff: current.comparison } : { file }),
          containerWrapper: code, renderRange: long && s.collapsed ? COLLAPSED_RANGE : undefined,
        });
        footer.hidden = !long;
        count.textContent = s.collapsed ? `${COLLAPSED_LINES} of ${current.lines} lines` : `${current.lines} lines`;
        expand.textContent = s.collapsed ? "Expand" : "Collapse";
        expand.setAttribute("aria-expanded", String(!s.collapsed));
        return;
      } catch (err) {
        endView();
        s.state = "failed";
        s.error = message(err);
        element.dataset.state = s.state;
      }
    }
    code.replaceChildren(fallback());
  }

  return {
    element,
    /** The viewer reads the Pierre modules after the page shows it. */
    show() {
      if (s.state === "idle") queueMicrotask(() => { if (code.isConnected) start(); });
    },
    end() {
      s.ended = true;
      endView();
    },
  };
}
