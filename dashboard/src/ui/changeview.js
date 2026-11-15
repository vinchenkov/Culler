// changeview.js — the Files changed view of the Review page. The view reads
// the recorded change data of the open packet and shows it through the local
// Pierre Trees and Pierre Diffs modules. Each read carries the serial of the
// view, so a response for a packet that the operator left changes nothing.

import { el, factNode, svg, tag } from "./dom.js";
import {
  changeRecord,
  changesAddress,
  comparisonFacts,
  comparisonState,
  comparisonStateText,
  deferredDiff,
  expandableFile,
  fileIndex,
  fileNotes,
  comparePaths,
  fileOrder,
  fileStatus,
  lineCounts,
  sizeLabel,
  treeStatus,
} from "./changes.js";
import { currentTheme, onThemeChange } from "./theme.js";

let modules = null;

/** The local Pierre modules. The page loads them at the first use of the view. */
function pierre() {
  modules ??= import("./pierre/pierre.js").catch((err) => {
    modules = null;
    throw err;
  });
  return modules;
}

const $ = (id) => document.getElementById(id);

/** The chevron of a step control, as the reader bar draws it. */
const chevron = (path) => svg("svg", { viewBox: "0 0 16 16", "aria-hidden": "true" },
  svg("path", { d: path, fill: "none", stroke: "currentColor", "stroke-width": "1.8", "stroke-linecap": "round", "stroke-linejoin": "round" }));
const message = (err) => String(err instanceof Error ? err.message : err);
const aborted = (err) => err?.name === "AbortError";

const TREE_KEY = "mc.changes-tree";

/** Whether the operator hid the file tree on this browser. */
function storedTreeHidden() {
  try { return localStorage.getItem(TREE_KEY) === "hidden"; } catch { return false; }
}

/** The Files changed view. `read` returns the JSON of an address and
 * `readText` returns its text; both accept an abort signal. */
export function createChangeView({ read, readText }) {
  const s = {
    row: null,
    view: null,
    layout: "unified",
    treeShown: !storedTreeHidden(),
    active: false,
    serial: 0,
    fileSerial: 0,
    controller: null,
    status: "idle",
    error: "",
    record: null,
    inventory: null,
    selected: null,
    shown: null,
    expanded: new Set(),
    tree: null,
    diffs: [],
    factsKey: "",
  };

  // A shown diff follows the theme of the dashboard.
  onThemeChange((theme) => { for (const diff of s.diffs) diff.setThemeType(theme); });

  for (const button of document.querySelectorAll("[data-layout]")) {
    button.addEventListener("click", () => setLayout(button.dataset.layout));
  }
  $("changes-tree-toggle").addEventListener("click", () => setTreeShown(!s.treeShown));

  function destroyDiffs() {
    for (const diff of s.diffs) diff.cleanUp();
    s.diffs = [];
    s.shown = null;
  }

  function destroyTree() {
    s.tree?.cleanUp();
    s.tree = null;
    $("changes-tree").replaceChildren();
  }

  /** Stops each read and removes the tree and the diff. */
  function reset() {
    s.serial++;
    s.fileSerial++;
    s.controller?.abort();
    s.controller = null;
    s.status = "idle";
    s.error = "";
    s.record = null;
    s.inventory = null;
    s.selected = null;
    s.expanded = new Set();
    destroyDiffs();
    destroyTree();
    $("changes-file").hidden = true;
    $("changes-diff").replaceChildren();
  }

  function paintControls() {
    for (const button of document.querySelectorAll("[data-layout]")) {
      const on = button.dataset.layout === s.layout;
      button.classList.toggle("on", on);
      button.setAttribute("aria-pressed", String(on));
    }
    $("changes-side").hidden = !s.treeShown;
    $("changes-tree-toggle").setAttribute("aria-expanded", String(s.treeShown));
    $("changes-tree-label").textContent = s.treeShown ? "Hide the file tree" : "Show the file tree";
    // A poll that keeps the commits keeps each copy control and its focus.
    const facts = s.row ? comparisonFacts(s.row) : [];
    const key = JSON.stringify(facts);
    if (key === s.factsKey) return;
    s.factsKey = key;
    $("changes-facts").replaceChildren(...facts.map(factNode));
  }

  /** The display controls show while the view shows, or reads, its files. */
  function paintTools() {
    const files = s.inventory?.files ?? [];
    $("changes-tools").hidden = !s.active || !(s.status === "loading" || (s.status === "ready" && files.length > 0));
  }

  /** Shows the state of the comparison: the read, the failed read, an absent
   * comparison, or the summary of the file inventory. */
  function paintState() {
    const state = $("changes-state");
    const text = $("changes-message");
    const body = $("changes-body");
    const files = s.inventory?.files ?? [];
    const retry = el("button", { type: "button", class: "btn small", text: "Retry", onclick: () => load() });
    const content = {
      idle: null,
      loading: el("p", { class: "loading", text: "Loading change data…" }),
      unavailable: el("p", { testid: "changes-unavailable", text: s.error }),
      failed: [el("p", { text: `The change data is unavailable. ${s.error}` }), retry],
      ready: files.length === 0 ? el("p", { testid: "changes-empty", text: "The comparison holds no changed files." }) : null,
    }[s.status];
    text.replaceChildren(...[content].flat().filter(Boolean));
    state.hidden = text.childNodes.length === 0;
    body.hidden = s.status !== "ready" || files.length === 0;
    // Without the file tree, the commits show below the state text.
    const facts = $("changes-facts");
    const host = s.status === "loading" || !body.hidden ? $("changes-side") : state;
    if (facts.parentElement !== host) host.append(facts);
    paintTools();
    const summary = $("changes-summary");
    if (s.status === "ready" && files.length > 0) {
      const added = files.reduce((sum, file) => sum + (file.additions ?? 0), 0);
      const removed = files.reduce((sum, file) => sum + (file.deletions ?? 0), 0);
      summary.textContent = `${files.length} ${files.length === 1 ? "file" : "files"} · +${added} −${removed}`;
    } else summary.textContent = "";
  }

  /** Reads the file inventory of the comparison, then shows the selected
   * file or the first file of the tree. */
  async function load() {
    const serial = ++s.serial;
    s.fileSerial++;
    s.controller?.abort();
    s.controller = new AbortController();
    const { row, view } = s;
    const selected = s.selected;
    destroyDiffs();
    destroyTree();
    s.inventory = null;
    $("changes-file").hidden = true;
    const state = comparisonState(row, view);
    s.record = changeRecord(row);
    if (state !== "ready") {
      s.status = "unavailable";
      s.error = comparisonStateText(state, view, row);
      paintState();
      return;
    }
    s.status = "loading";
    paintState();
    try {
      const [data, library] = await Promise.all([read(changesAddress(row), s.controller.signal), pierre()]);
      if (serial !== s.serial) return;
      if (data?.from_commit !== s.record.from_commit || data?.to_commit !== s.record.to_commit || !Array.isArray(data.files)) {
        throw new Error("The change data differs from its record.");
      }
      s.inventory = { files: data.files, index: fileIndex(data.files), order: fileOrder(data.files) };
      s.status = "ready";
      paintState();
      buildTree(library);
      select(s.inventory.index.has(selected) ? selected : s.inventory.order[0] ?? null);
    } catch (err) {
      if (serial !== s.serial || aborted(err)) return;
      s.status = "failed";
      s.error = message(err);
      paintState();
    }
  }

  function buildTree({ FileTree }) {
    const { files, index } = s.inventory;
    const decoration = ({ item }) => {
      const file = item.kind === "file" ? files[index.get(item.path)] : null;
      if (!file) return null;
      if (file.binary) return { text: "binary" };
      return {
        text: lineCounts(file),
        parts: [
          { text: `+${file.additions ?? 0}`, color: "var(--leaf-ink)" },
          { text: `\u00a0−${file.deletions ?? 0}`, color: "var(--rust-ink)" },
        ],
      };
    };
    s.tree = new FileTree({
      paths: files.map((file) => file.path),
      initialExpansion: "open",
      sort: (left, right) => comparePaths(left.path, right.path),
      search: true,
      fileTreeSearchMode: "hide-non-matches",
      gitStatus: treeStatus(files),
      renderRowDecoration: decoration,
      onSelectionChange: (paths) => {
        const path = paths.at(-1);
        if (path !== undefined && path !== s.selected && index.has(path)) select(path, false);
      },
    });
    s.tree.render({ containerWrapper: $("changes-tree") });
  }

  /** Selects one file in the tree and shows its diff. */
  function select(path, moveTree = true) {
    s.selected = path;
    if (path === null) return;
    if (moveTree && s.tree) {
      for (const other of s.tree.getSelectedPaths()) if (other !== path) s.tree.getItem(other)?.deselect();
      const item = s.tree.getItem(path);
      if (item && !item.isSelected()) item.select();
      s.tree.focusPath(path);
      s.tree.scrollToPath(path, { offset: "nearest" });
    }
    void showFile();
  }

  /** Selects the previous or the next file. A step control keeps the focus
   * while it stays available. */
  function step(offset) {
    const order = s.inventory?.order ?? [];
    const next = order[order.indexOf(s.selected) + offset];
    if (next === undefined) return;
    const focused = document.activeElement?.dataset?.testid;
    select(next);
    if (focused !== "changes-previous" && focused !== "changes-next") return;
    const control = $("changes-file-head").querySelector(`[data-testid="${focused}"]`);
    (control && !control.disabled ? control : $("changes-diff")).focus();
  }

  function paintFileHead(file) {
    const { order } = s.inventory;
    const position = order.indexOf(file.path);
    const status = fileStatus(file);
    const previous = el("button", {
      type: "button", class: "reader-step", testid: "changes-previous", "aria-label": "Previous file", title: "Previous file",
      disabled: position <= 0, onclick: () => step(-1),
    }, chevron("M10 3.5 5.5 8l4.5 4.5"));
    const next = el("button", {
      type: "button", class: "reader-step", testid: "changes-next", "aria-label": "Next file", title: "Next file",
      disabled: position >= order.length - 1, onclick: () => step(1),
    }, chevron("M6 3.5 10.5 8 6 12.5"));
    $("changes-file-head").replaceChildren(
      el("h4", { class: "changes-path", testid: "changes-path", title: file.path, text: file.path }),
      tag(status.label, `tone-${status.tone}`),
      el("span", { class: "changes-counts", text: lineCounts(file) }),
      el("span", { class: "changes-nav" }, previous,
        el("span", { class: "reader-position", text: `${position + 1} of ${order.length}` }), next),
    );
    const notes = fileNotes(file);
    $("changes-notes").replaceChildren(...notes.map((text) => el("li", { text })));
    $("changes-notes").hidden = notes.length === 0;
    $("changes-file").hidden = false;
  }

  /** Shows the diff of the selected file. A large diff waits for the operator. */
  async function showFile() {
    const serial = s.serial;
    const fileSerial = ++s.fileSerial;
    const index = s.inventory.index.get(s.selected);
    const file = s.inventory.files[index];
    destroyDiffs();
    paintFileHead(file);
    const area = $("changes-diff");
    area.scrollTop = 0;
    if (!file.text_diff) {
      area.replaceChildren();
      return;
    }
    if (deferredDiff(file) && !s.expanded.has(file.path)) {
      const lines = (file.additions ?? 0) + (file.deletions ?? 0);
      area.replaceChildren(el("div", { class: "changes-deferred", testid: "changes-deferred" },
        el("p", { text: `This diff holds ${lines} changed lines (${sizeLabel(file.patch_bytes ?? 0)}). The view shows it when you ask.` }),
        el("button", {
          type: "button", class: "btn small", testid: "changes-show-diff", text: "Show the diff",
          onclick: () => {
            s.expanded.add(file.path);
            void showFile().then(() => area.focus());
          },
        })));
      return;
    }
    area.replaceChildren(el("p", { class: "loading", text: "Loading diff…" }));
    try {
      const [text, library] = await Promise.all([
        readText(changesAddress(s.row, index), s.controller.signal), pierre()]);
      if (serial !== s.serial || fileSerial !== s.fileSerial) return;
      s.shown = { text, library, index, files: null };
      renderDiff();
    } catch (err) {
      if (serial !== s.serial || fileSerial !== s.fileSerial || aborted(err)) return;
      area.replaceChildren(el("div", { class: "changes-deferred" },
        el("p", { text: `The diff is unavailable. ${message(err)}` }),
        el("button", { type: "button", class: "btn small", text: "Retry", onclick: () => void showFile() })));
    }
  }

  /** Reads the version of the shown file at each commit of the comparison.
   * The diff reads them at the first request for unmodified lines. */
  function fileVersions(shown) {
    return async () => {
      shown.files ??= read(changesAddress(s.row, shown.index, "files"), s.controller.signal).then((data) => ({
        oldFile: { name: data.old_file.path, contents: data.old_file.content },
        newFile: { name: data.new_file.path, contents: data.new_file.content },
      }));
      try {
        const files = await shown.files;
        if (shown === s.shown) showExpandError("");
        return files;
      } catch (err) {
        shown.files = null;
        if (shown === s.shown && !aborted(err)) showExpandError(`The unmodified lines are unavailable. ${message(err)}`);
        throw err;
      }
    };
  }

  /** States a failed read of the unmodified lines below the file header. */
  function showExpandError(text) {
    const notes = $("changes-notes");
    notes.querySelector('[data-testid="changes-expand-error"]')?.remove();
    if (text) notes.append(el("li", { testid: "changes-expand-error", text }));
    notes.hidden = notes.childElementCount === 0;
  }

  function renderDiff() {
    const { text, library } = s.shown;
    const file = s.inventory.files[s.shown.index];
    for (const diff of s.diffs) diff.cleanUp();
    s.diffs = [];
    const area = $("changes-diff");
    area.replaceChildren();
    const parts = library.parsePatchFiles(text, `${s.row.packet_id}:${s.row.version_id}:${s.record.to_commit}:${s.selected}:`)
      .flatMap((patch) => patch.files);
    for (const fileDiff of parts) {
      const diff = new library.FileDiff({
        theme: { light: "pierre-light", dark: "pierre-dark" },
        themeType: currentTheme(),
        diffStyle: s.layout,
        overflow: "scroll",
        // One part shows under the file head. Each part of a type change
        // names its own side.
        disableFileHeader: parts.length === 1,
        hunkSeparators: "line-info",
        lineDiffType: "word-alt",
        ...(expandableFile(file) ? { loadDiffFiles: fileVersions(s.shown) } : {}),
      });
      diff.render({ fileDiff, containerWrapper: area });
      s.diffs.push(diff);
    }
  }

  function setTreeShown(shown) {
    s.treeShown = shown;
    try { localStorage.setItem(TREE_KEY, shown ? "shown" : "hidden"); } catch { /* The choice lasts until the page loads again. */ }
    paintControls();
  }

  function setLayout(layout) {
    if (layout !== "unified" && layout !== "split") return;
    s.layout = layout;
    paintControls();
    if (s.shown) renderDiff();
  }

  return {
    /** Shows a different packet, with no selection. The view reads nothing
     * until the operator opens it. */
    open(row, view) {
      reset();
      s.row = row;
      s.view = view;
      paintControls();
      paintState();
      if (s.active) void load();
    },
    /** Follows a new read of the same packet. A new record, such as the record
     * of a new merge candidate, reads the change data again. */
    refresh(row, view) {
      s.row = row;
      s.view = view;
      paintControls();
      if (s.status === "idle") return;
      const record = changeRecord(row);
      const state = comparisonState(row, view);
      const changed = state === "ready"
        ? record.inventory_sha256 !== s.record?.inventory_sha256
        : s.status !== "unavailable" || comparisonStateText(state, view, row) !== s.error;
      if (changed) void load();
    },
    /** The view becomes visible. The first time reads the change data. */
    activate() {
      s.active = true;
      if (s.status === "idle" && s.row) void load();
      paintTools();
    },
    deactivate() {
      s.active = false;
      paintTools();
    },
    /** The reader shows no packet. */
    clear() {
      reset();
      s.row = null;
      s.view = null;
      s.active = false;
      paintControls();
      paintState();
    },
  };
}
