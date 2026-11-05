import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { bootSmoke, evidencePath, gotoReady, seedProject, selectProject, teardown, type Smoke } from "./harness";

let smoke: Smoke;
const project = "proposal-files";
const digest = `sha256:${"a".repeat(64)}`;
const longLine = `RUN echo ${"x".repeat(300)}`.slice(0, 300);
const dockerLines = Array.from({ length: 40 }, (_, at) => at === 5 ? longLine : `RUN echo "step ${at + 1}"`);
dockerLines[0] = `FROM culler-project-base:${"a".repeat(64)}`;
const dockerfile = `${dockerLines.join("\n")}\n`;
const acceptedDockerfile = `${dockerLines.filter((_, at) => at !== 12).map((line, at) => at === 28 ? "RUN echo \"earlier step\"" : line).join("\n")}\n`;
const tools = "#!/bin/sh\n\n# **Literal build contents**\n  printf '%s\\n' '<script>window.setupScriptRan = true</script>'\n";
const removed = "#!/bin/sh\nrm -rf /var/cache/tools\nexit 0\n";

interface Files { dockerfile: string; build_files: { path: string; content: string }[] }
const firstFiles: Files = { dockerfile, build_files: [{ path: ".culler/tools.sh", content: tools }] };
const acceptedFiles: Files = { dockerfile: acceptedDockerfile, build_files: [{ path: ".culler/tools.sh", content: tools }, { path: ".culler/clean.sh", content: removed }] };

/** A Setup Journal read with a current proposal for Image dependencies. An
 * earlier acceptance supplies the accepted version. */
function approvalView(proposed: Files, accepted: Files | null = null): any {
  const configuration = (files: Files) => ({ base_digest: digest, dockerfile: { path: ".culler/Dockerfile", content: files.dockerfile },
    build_files: files.build_files, build_args: { TOOL_VERSION: "16" }, setup: { directory: ".", command: "npm ci" } });
  const revision = { id: "viewer-proposal", configuration: configuration(proposed), editor: "setup:viewer-setup",
    explanation: "The image supplies database tools. The Project needs them for its checks." };
  const base = { id: "viewer-base", configuration: accepted ? configuration(accepted) : { base_digest: digest }, explanation: "The accepted image." };
  const setup = { id: "viewer-setup", project_id: project, revision_id: base.id, kind: "setup", status: "succeeded", attempt: 1,
    created_at: "2026-10-10 12:01:00", ended_at: "2026-10-10 12:02:00", metadata: { report_outcome: "proposal", proposed_revision_id: revision.id } };
  const accept = { id: "viewer-accept", project_id: project, revision_id: base.id, kind: "accept", status: "succeeded", attempt: 1,
    created_at: "2026-10-10 12:00:00", ended_at: "2026-10-10 12:00:30", metadata: {} };
  return {
    environment: { project_id: project, project_title: "Proposal files", project_status: "paused", setup_status: "configured", setup_id: "viewer-setup-id",
      vision_revision: "viewer-vision", selected_revision_id: "", draft_revision_id: revision.id, phase: "approval", readiness: "setup_required",
      active_operation_id: "", cause: "" },
    draft: revision, selected: null, revisions: [revision, base], operations: accepted ? [setup, accept] : [setup], approvals: [], decisions: [],
    vision: { revision: "viewer-vision", content: "# Proposal files\n\nShow each proposal as a code change.", created_at: "2026-10-10 11:59:00" },
    results: [{ id: "viewer-report", operation_id: setup.id, revision_id: revision.id, outcome: "proposal", summary: revision.explanation, details: "" }],
  };
}

beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, project, { title: "Proposal files" });
  await smoke.context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: new URL(smoke.server.url).origin });
  await gotoReady(smoke);
}, 60000);
afterEach(async () => {
  await smoke.page.unrouteAll({ behavior: "wait" });
  await smoke.page.evaluate(() => { document.documentElement.style.zoom = ""; });
  await smoke.page.setViewportSize({ width: 1280, height: 720 });
  await smoke.page.emulateMedia({ colorScheme: "light" });
});
afterAll(async () => { if (smoke) await teardown(smoke); });

async function installFixture(read: () => any) {
  await smoke.page.route("**/api/pause", async route => {
    const response = await route.fetch();
    const state = await response.json();
    state.projects = state.projects.map((row: any) => row.id === project ? { ...row, setup_status: read().environment.setup_status } : row);
    await route.fulfill({ json: state });
  });
  await smoke.page.route("**/api/environment/get", async route => {
    if (route.request().postDataJSON()?.project_id !== project) return route.continue();
    await route.fulfill({ json: read() });
  });
  await smoke.page.getByTestId("tab-board").click();
  await selectProject(smoke, project);
  await smoke.page.reload();
  await smoke.page.getByTestId("setup-approve").waitFor();
}

const refresh = () => smoke.page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
const dockerSelector = '[data-testid="setup-dockerfile"]';
const buildSelector = (path: string) => `[data-journal-key="viewer-proposal:file:${path}"] .file-view`;

/** Waits until a file viewer shows its rows with syntax colors. */
async function ready(selector: string) {
  await smoke.page.waitForFunction(selector => {
    const node = document.querySelector(selector);
    const root = node?.querySelector("diffs-container")?.shadowRoot;
    return node?.getAttribute("data-state") === "ready" && (root?.querySelectorAll("[data-line] span[style]").length ?? 0) > 0;
  }, selector);
}

/** The rows of one file viewer, with their change marks and line numbers. */
const viewerRows = (selector: string) => smoke.page.locator(selector).evaluate(node => {
  const root = node.querySelector("diffs-container")!.shadowRoot!;
  const rows = [...root.querySelectorAll("[data-line]")];
  return {
    collapsed: (node as HTMLElement).dataset.collapsed,
    types: rows.map(row => row.getAttribute("data-line-type")),
    marks: rows.map(row => getComputedStyle(row, "::before").content),
    // The bar of each row: solid for an added line, striped for a removed line.
    bars: [...root.querySelectorAll("[data-column-number]")].map(cell => {
      const bar = getComputedStyle(cell, "::before");
      return bar.content === "none" ? "none" : bar.backgroundImage === "none" ? "solid" : "striped";
    }),
    numbers: [...root.querySelectorAll("[data-column-number]")].map(cell => cell.textContent),
    // An empty row holds one line break.
    text: rows.map(row => row.textContent === "\n" ? "" : row.textContent),
    separators: [...new Set([...root.querySelectorAll("[data-unmodified-lines]")].map(text => text.textContent))],
    expandButtons: [...root.querySelectorAll("[data-expand-button]")].filter(button => button.checkVisibility()).length,
    scripts: root.querySelectorAll("script").length,
    footer: node.querySelector(".file-view-footer:not([hidden])")?.textContent ?? "",
  };
});

/** Selects rows `from` through `to` of one file viewer with the mouse, copies
 * them, and returns the copied text. */
async function copyRows(selector: string, from: number, to: number) {
  const { page } = smoke;
  const box = await page.locator(selector).evaluate((node, [from, to]) => {
    const rows = [...node.querySelector("diffs-container")!.shadowRoot!.querySelectorAll("[data-line]")];
    rows[from]!.scrollIntoView({ block: "center" });
    const first = rows[from]!.firstElementChild!.getBoundingClientRect(), last = rows[to]!.lastElementChild!.getBoundingClientRect();
    return { x: first.left + 2, y: first.top + first.height / 2, endX: last.right + 4, endY: last.top + last.height / 2 };
  }, [from, to]);
  await page.mouse.move(box.x, box.y);
  await page.mouse.down();
  await page.mouse.move(box.endX, box.endY, { steps: 8 });
  await page.mouse.up();
  await page.keyboard.press("ControlOrMeta+c");
  return page.evaluate(() => navigator.clipboard.readText());
}

test("a first proposal marks each line as added, wraps long lines, and collapses a long file", async () => {
  const { page } = smoke;
  let view = approvalView(firstFiles);
  await installFixture(() => view);
  await ready(dockerSelector);
  const viewer = page.locator(dockerSelector);
  expect(await viewer.locator(".file-view-head").innerText()).toContain(".culler/Dockerfile");
  expect(await viewer.locator(".file-view-change").innerText()).toBe("Added");
  expect(await viewer.locator(".file-view-added").innerText()).toBe("+40");
  expect(await viewer.locator(".file-view-removed").count()).toBe(0);
  expect(await viewer.getByTestId("setup-file-counts").textContent()).toBe("+4040 added lines, 0 removed lines");
  let rows = await viewerRows(dockerSelector);
  expect(rows.collapsed).toBe("true");
  expect(rows.types).toEqual(Array(20).fill("change-addition"));
  expect(rows.bars).toEqual(Array(20).fill("solid"));
  expect(rows.marks).toEqual(Array(20).fill("none"));
  expect(rows.numbers).toEqual(Array.from({ length: 20 }, (_, at) => String(at + 1)));
  expect(rows.text).toEqual(dockerLines.slice(0, 20));
  expect(rows.footer).toBe("20 of 40 linesExpand");

  // A long line wraps inside the code region and keeps one line number.
  const wrap = await viewer.evaluate(node => {
    const root = node.querySelector("diffs-container")!.shadowRoot!;
    const pre = root.querySelector("pre")!;
    const row = root.querySelector('[data-line="6"]')!;
    return {
      length: row.textContent!.length, height: row.getBoundingClientRect().height, line: parseFloat(getComputedStyle(row).lineHeight),
      numbers: root.querySelectorAll('[data-column-number="6"]').length, overflow: pre.scrollWidth - pre.clientWidth,
      page: document.documentElement.scrollWidth - innerWidth,
    };
  });
  expect(wrap.length).toBe(300);
  expect(wrap.height).toBeGreaterThan(wrap.line * 1.5);
  expect(wrap.numbers).toBe(1);
  expect(wrap.overflow).toBeLessThanOrEqual(0);
  expect(wrap.page).toBeLessThanOrEqual(0);

  // Expand shows the complete file in the page, without an inner scroll region.
  await viewer.getByTestId("setup-file-expand").click();
  rows = await viewerRows(dockerSelector);
  expect(rows.collapsed).toBe("false");
  expect(rows.text).toEqual(dockerLines);
  expect(rows.footer).toBe("40 linesCollapse");
  // The text for a screen reader clips itself and is no scroll region.
  expect(await viewer.evaluate(node => [node, ...node.querySelectorAll(":not(.sr-only)")].every(part => part.scrollHeight <= part.clientHeight + 1)
    && [...node.querySelector("diffs-container")!.shadowRoot!.querySelectorAll("*")].every(part => part.scrollHeight <= part.clientHeight + 1))).toBe(true);
  await page.screenshot({ path: evidencePath("setup-file-viewer-expanded.png"), fullPage: true });
  await viewer.getByTestId("setup-file-expand").click();
  expect((await viewerRows(dockerSelector)).text).toEqual(dockerLines.slice(0, 20));
  await viewer.getByTestId("setup-file-expand").click();
  expect((await viewerRows(dockerSelector)).text).toHaveLength(40);

  // A refresh keeps the collapse state, the focus, and the scroll position.
  await viewer.getByTestId("setup-file-expand").focus();
  await page.evaluate(() => window.scrollTo(0, 600));
  const scroll = await page.evaluate(() => window.scrollY);
  expect(scroll).toBeGreaterThan(0);
  view = { ...view, inventory: { tools: { node: "24" } } };
  await refresh();
  await page.waitForFunction(() => document.querySelector('[data-testid="setup-panel"]') !== null);
  await page.waitForTimeout(400);
  rows = await viewerRows(dockerSelector);
  expect(rows.collapsed).toBe("false");
  expect(rows.text).toHaveLength(40);
  expect(await page.evaluate(() => window.scrollY)).toBe(scroll);
  expect(await viewer.getByTestId("setup-file-expand").evaluate(node => document.activeElement === node)).toBe(true);

  // A copy of selected lines holds only the file text.
  expect(await copyRows(dockerSelector, 1, 3)).toBe(dockerLines.slice(1, 4).join("\n"));

  // A required build file stays in collapsed details. Its text stays literal and runs no script.
  const details = page.locator('[data-journal-key="viewer-proposal:file:.culler/tools.sh"]');
  expect(await details.evaluate(node => (node as HTMLDetailsElement).open)).toBe(false);
  expect(await details.locator("summary").innerText()).toBe("Added: .culler/tools.sh");
  await details.locator("summary").click();
  await ready(buildSelector(".culler/tools.sh"));
  const build = await viewerRows(buildSelector(".culler/tools.sh"));
  expect(build.types).toEqual(Array(4).fill("change-addition"));
  expect(build.text).toEqual(tools.trimEnd().split("\n"));
  expect(build.scripts).toBe(0);
  expect(await copyRows(buildSelector(".culler/tools.sh"), 0, 3)).toBe(tools.trimEnd());
  expect(await page.evaluate(() => (window as { setupScriptRan?: boolean }).setupScriptRan)).toBeUndefined();
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

test("a changed proposal marks only its changed lines and keeps the complete file available", async () => {
  const { page } = smoke;
  await installFixture(() => approvalView(firstFiles, acceptedFiles));
  await ready(dockerSelector);
  const viewer = page.locator(dockerSelector);
  expect(await viewer.locator(".file-view-change").innerText()).toBe("Changed");
  expect(await viewer.getByTestId("setup-file-counts").textContent()).toBe("+2−12 added lines, 1 removed line");
  const rows = await viewerRows(dockerSelector);
  expect(rows.types.filter(type => type === "change-addition")).toHaveLength(2);
  expect(rows.types.filter(type => type === "change-deletion")).toHaveLength(1);
  expect(rows.types.filter(type => type === "context").length).toBeGreaterThan(0);
  expect(rows.bars.filter(bar => bar === "solid")).toHaveLength(2);
  expect(rows.bars.filter(bar => bar === "striped")).toHaveLength(1);
  expect(rows.marks.every(mark => mark === "none")).toBe(true);
  expect(rows.text).toContain("RUN echo \"earlier step\"");
  expect(rows.text).toContain(dockerLines[12]);
  expect(rows.text.length).toBeLessThan(40);
  expect(rows.separators.length).toBeGreaterThan(0);
  expect(rows.separators.every(text => /^\d+ unmodified lines?$/.test(text ?? ""))).toBe(true);
  expect(rows.expandButtons).toBe(0);
  // A click on a separator keeps the rows of the comparison.
  const separator = await viewer.evaluate(node => {
    const text = node.querySelector("diffs-container")!.shadowRoot!.querySelector("[data-unmodified-lines]")!;
    text.scrollIntoView({ block: "center" });
    const box = text.getBoundingClientRect();
    return { x: box.left + box.width / 2, y: box.top + box.height / 2, width: box.width };
  });
  expect(separator.width).toBeGreaterThan(60);
  await page.mouse.click(separator.x, separator.y);
  await page.waitForTimeout(200);
  expect((await viewerRows(dockerSelector)).types).toEqual(rows.types);

  // The complete proposed file has its line numbers and no change marks.
  await viewer.getByTestId("setup-file-complete").click();
  await page.waitForFunction(selector => document.querySelector(selector)?.querySelector("diffs-container")?.shadowRoot
    ?.querySelector("pre[data-file]") !== null, dockerSelector);
  await viewer.getByTestId("setup-file-expand").click();
  const complete = await viewerRows(dockerSelector);
  expect(complete.text).toEqual(dockerLines);
  expect(new Set(complete.types)).toEqual(new Set(["context"]));
  expect(complete.marks.every(mark => mark === "none")).toBe(true);
  expect(complete.bars.every(bar => bar === "none")).toBe(true);
  expect(complete.numbers).toEqual(Array.from({ length: 40 }, (_, at) => String(at + 1)));
  expect(await viewer.getByTestId("setup-file-complete").innerText()).toBe("Show changes");
  await viewer.getByTestId("setup-file-complete").click();
  await page.waitForFunction(selector => document.querySelector(selector)?.querySelector("diffs-container")?.shadowRoot
    ?.querySelector("pre[data-diff]") !== null, dockerSelector);

  // A removed build file marks each line as removed. An unchanged build file has no marks.
  const removedDetails = page.locator('[data-journal-key="viewer-proposal:file:.culler/clean.sh"]');
  expect(await removedDetails.locator("summary").innerText()).toBe("Removed: .culler/clean.sh");
  await removedDetails.locator("summary").click();
  await ready(buildSelector(".culler/clean.sh"));
  const gone = await viewerRows(buildSelector(".culler/clean.sh"));
  expect(gone.types).toEqual(Array(3).fill("change-deletion"));
  expect(gone.bars).toEqual(Array(3).fill("striped"));
  expect(gone.text).toEqual(removed.trimEnd().split("\n"));
  const sameDetails = page.locator('[data-journal-key="viewer-proposal:file:.culler/tools.sh"]');
  expect(await sameDetails.locator("summary").innerText()).toBe("Unchanged: .culler/tools.sh");
  await sameDetails.locator("summary").click();
  await ready(buildSelector(".culler/tools.sh"));
  const same = await viewerRows(buildSelector(".culler/tools.sh"));
  expect(same.types).toEqual(Array(4).fill("context"));
  expect(same.marks).toEqual(Array(4).fill("none"));
  expect(same.bars).toEqual(Array(4).fill("none"));
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

/** The colors and contrast ratios of the approval view in the current theme. */
const approvalColors = () => smoke.page.evaluate(async () => {
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 1;
  const paint = canvas.getContext("2d", { willReadFrequently: true })!;
  const rgba = (color: string) => {
    paint.clearRect(0, 0, 1, 1);
    paint.fillStyle = "rgba(0, 0, 0, 0)";
    paint.fillStyle = color;
    paint.fillRect(0, 0, 1, 1);
    const [red, green, blue, alpha] = paint.getImageData(0, 0, 1, 1).data;
    return { rgb: [red!, green!, blue!], alpha: alpha! / 255 };
  };
  const over = (top: { rgb: number[]; alpha: number }, under: number[]) => top.rgb.map((value, at) => value * top.alpha + under[at]! * (1 - top.alpha));
  const luminance = (rgb: number[]) => {
    const [red, green, blue] = rgb.map(value => {
      const channel = value / 255;
      return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * red! + 0.7152 * green! + 0.0722 * blue!;
  };
  const ratio = (first: number[], second: number[]) => {
    const [high, low] = [luminance(first), luminance(second)].sort((a, b) => b - a);
    return (high! + 0.05) / (low! + 0.05);
  };
  const parent = (node: Element) => node.parentElement ?? ((node.getRootNode() as ShadowRoot).host ?? null);
  /** Each background from the element up to the first solid one, over the paper color. */
  const ground = (element: Element) => {
    const layers = [];
    for (let at: Element | null = element; at; at = parent(at)) {
      const layer = rgba(getComputedStyle(at).backgroundColor);
      if (layer.alpha > 0) layers.push(layer);
      if (layer.alpha === 1) break;
    }
    let result = rgba(getComputedStyle(document.body).backgroundColor).rgb;
    for (const layer of layers.reverse()) result = over(layer, result);
    return result;
  };
  const textRatio = (element: Element) => {
    const back = ground(element);
    return ratio(over(rgba(getComputedStyle(element).color), back), back);
  };
  const same = (first: number[], second: number[]) => first.every((value, at) => Math.abs(value - second[at]!) < 1);
  const journal = document.querySelector('[data-testid="setup-panel"]')!;
  const approval = document.querySelector('[data-testid="setup-proposal"]')!;
  const viewer = document.querySelector('[data-testid="setup-dockerfile"]')!;
  const surface = ground(journal), fill = ground(approval), head = ground(viewer.querySelector(".file-view-head")!);
  const style = getComputedStyle(approval);
  const boundary = rgba(style.borderTopColor), code = rgba(getComputedStyle(viewer).borderTopColor);
  const palette = getComputedStyle(document.documentElement);
  const approvalLine = rgba(palette.getPropertyValue("--approval-line").trim());
  const approvalFill = rgba(palette.getPropertyValue("--approval-fill").trim());

  const low: string[] = [];
  const walker = document.createTreeWalker(approval, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const element = node.parentElement!;
    if (!(node.nodeValue ?? "").trim() || !element.checkVisibility()) continue;
    const value = textRatio(element);
    if (value < 4.5) low.push(`${element.tagName.toLowerCase()}.${element.className} "${node.nodeValue!.trim().slice(0, 30)}" ${value.toFixed(2)}`);
  }
  const codes: Record<string, number> = {};
  for (const host of approval.querySelectorAll("diffs-container")) {
    if (!host.checkVisibility()) continue;
    const root = host.shadowRoot!;
    const parts: [string, Element][] = [
      ...[...root.querySelectorAll("[data-line] span")].filter(span => span.textContent!.trim()).map(span =>
        [`token ${span.closest("[data-line]")!.getAttribute("data-line-type")}`, span] as [string, Element]),
      ...[...root.querySelectorAll("[data-line-number-content]")].map(number =>
        [`number ${number.closest("[data-column-number]")!.getAttribute("data-line-type")}`, number] as [string, Element]),
      ...[...root.querySelectorAll("[data-unmodified-lines]")].map(text => ["separator", text] as [string, Element]),
    ];
    for (const [kind, element] of parts) codes[kind] = Math.min(codes[kind] ?? Infinity, textRatio(element));
  }

  // A reference diff with the options of the Files changed view in the dark
  // theme. The approval view uses the dark theme in both themes.
  const theme = document.documentElement.dataset.theme as "light" | "dark";
  const pierre = await import("/pierre/pierre.js");
  const reference = Object.assign(document.createElement("div"), { style: "position:absolute;left:-9999px;width:600px" });
  document.body.append(reference);
  const diff = new pierre.FileDiff({ theme: { light: "pierre-light", dark: "pierre-dark" }, themeType: "dark", diffStyle: "unified", overflow: "scroll" });
  diff.render({ fileDiff: pierre.parseDiffFromFile({ name: "Dockerfile", contents: "RUN one\n" }, { name: "Dockerfile", contents: "RUN two\n" }), containerWrapper: reference });
  const referenceRoot = reference.querySelector("diffs-container")!.shadowRoot!;
  for (let wait = 0; wait < 50 && !referenceRoot.querySelector("[data-line]"); wait++) await new Promise(resolve => setTimeout(resolve, 20));
  const lineColor = (root: ShadowRoot, type: string) => rgba(getComputedStyle(root.querySelector(`[data-line][data-line-type="${type}"]`)!).backgroundColor).rgb;
  const viewerRoot = viewer.querySelector("diffs-container")!.shadowRoot!;
  const matches = ["change-addition", "change-deletion"].every(type => same(lineColor(referenceRoot, type), lineColor(viewerRoot, type)));
  const contextGround = same(ground(viewerRoot.querySelector('[data-line][data-line-type="context"]')!), ground(viewerRoot.querySelector("pre")!));
  diff.cleanUp();
  reference.remove();

  return {
    theme,
    boundary: ratio(over(boundary, surface), surface),
    approvalPalette: same(boundary.rgb, approvalLine.rgb) && same(fill, approvalFill.rgb),
    fillDiffers: !same(fill, surface),
    headDiffers: !same(head, fill),
    code: ratio(over(code, fill), fill),
    low, codes, matches, contextGround,
    codeGround: getComputedStyle(viewerRoot.querySelector("pre")!).backgroundColor,
    fill: fill.join(","),
  };
});

test("the approval view keeps its contrast and follows the theme", async () => {
  const { page } = smoke;
  await installFixture(() => approvalView(firstFiles, acceptedFiles));
  await ready(dockerSelector);
  for (const path of [".culler/clean.sh", ".culler/tools.sh"]) {
    await page.locator(`[data-journal-key="viewer-proposal:file:${path}"] summary`).click();
    await ready(buildSelector(path));
  }
  const grounds: string[] = [], fills: string[] = [];
  for (const theme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await page.waitForFunction(theme => document.documentElement.dataset.theme === theme, theme);
    await page.waitForFunction(() => document.getAnimations().every(animation =>
      animation.playState === "finished" || animation.effect?.getComputedTiming().iterations === Infinity));
    const colors = await approvalColors();
    expect(colors.boundary).toBeGreaterThanOrEqual(3);
    expect(colors.approvalPalette).toBe(true);
    expect(colors.fillDiffers).toBe(true);
    expect(colors.headDiffers).toBe(true);
    expect(colors.code).toBeGreaterThanOrEqual(3);
    expect(colors.low).toEqual([]);
    expect(Object.keys(colors.codes).sort()).toEqual([
      "number change-addition", "number change-deletion", "number context", "separator",
      "token change-addition", "token change-deletion", "token context",
    ]);
    for (const [kind, value] of Object.entries(colors.codes)) expect([kind, value >= 4.5]).toEqual([kind, true]);
    expect(colors.matches).toBe(true);
    expect(colors.contextGround).toBe(true);
    grounds.push(colors.codeGround);
    fills.push(colors.fill);
    // A viewport taller than the approval view keeps the sticky status banner out of the screenshot.
    await page.setViewportSize({ width: 1280, height: 2400 });
    await page.getByTestId("setup-proposal").screenshot({ path: evidencePath(`setup-approval-view-${theme}.png`) });
    await page.setViewportSize({ width: 1280, height: 720 });
  }
  // A theme change changes the approval fill. The file viewer keeps the dark code colors.
  expect(fills[0]).not.toBe(fills[1]);
  expect(grounds[0]).toBe(grounds[1]);
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);

test("a failed load of the Pierre Diffs assets shows the exact contents and a retry", async () => {
  const { page } = smoke;
  const blocked = (url: URL) => url.pathname === "/pierre/pierre.js";
  await page.route(blocked, route => route.abort());
  await installFixture(() => approvalView(firstFiles));
  const fallback = page.locator(dockerSelector).getByTestId("setup-file-fallback");
  await fallback.waitFor();
  expect(await page.locator(dockerSelector).getAttribute("data-state")).toBe("failed");
  expect(await fallback.locator("[role=alert]").innerText()).toBe("The comparison is unavailable. The exact proposed contents follow.");
  expect(await fallback.locator("pre code").textContent()).toBe(dockerfile);
  expect(await fallback.locator("pre").evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  expect(await page.getByTestId("setup-approve").isEnabled()).toBe(true);
  await page.screenshot({ path: evidencePath("setup-file-viewer-fallback.png"), fullPage: true });
  await page.unroute(blocked);
  await fallback.getByTestId("setup-file-retry").click();
  await ready(dockerSelector);
  expect((await viewerRows(dockerSelector)).text).toEqual(dockerLines.slice(0, 20));
}, 60000);

test("the approval view is usable in a window 400 pixels wide and at 200 percent zoom", async () => {
  const { page } = smoke;
  await installFixture(() => approvalView(firstFiles, acceptedFiles));
  await ready(dockerSelector);
  const fits = () => page.evaluate(selector => {
    const pre = document.querySelector(selector)!.querySelector("diffs-container")!.shadowRoot!.querySelector("pre")!;
    return document.documentElement.scrollWidth <= innerWidth + 1 && pre.scrollWidth <= pre.clientWidth;
  }, dockerSelector);
  for (const theme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await page.setViewportSize({ width: 400, height: 900 });
    expect(await fits()).toBe(true);
    await page.getByTestId("setup-approve").scrollIntoViewIfNeeded();
    expect(await page.getByTestId("setup-approve").isVisible()).toBe(true);
    await page.screenshot({ path: evidencePath(`setup-file-viewer-${theme}-400.png`), fullPage: true });
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.evaluate(() => { document.documentElement.style.zoom = "2"; });
    expect(await fits()).toBe(true);
    await page.screenshot({ path: evidencePath(`setup-file-viewer-${theme}-zoom200.png`), fullPage: true });
    await page.evaluate(() => { document.documentElement.style.zoom = ""; });
  }
}, 60000);

test("Edit Dockerfile opens the suggested Dockerfile on the Environment page", async () => {
  const { page } = smoke;
  await installFixture(() => approvalView(firstFiles));
  const edit = page.getByTestId("setup-proposal").getByTestId("setup-edit-dockerfile");
  expect(await edit.innerText()).toBe("Edit Dockerfile");
  await edit.click();
  await page.waitForFunction(() => document.activeElement?.getAttribute("data-testid") === "environment-dockerfile");
  const editor = page.getByTestId("environment-dockerfile");
  expect(await editor.inputValue()).toBe(dockerfile);
  expect(await editor.evaluate(node => {
    const box = node.getBoundingClientRect();
    return box.top >= 0 && box.top < innerHeight;
  })).toBe(true);
  expect(new URL(page.url()).hash).toBe(`#environment/${project}`);
  await page.screenshot({ path: evidencePath("setup-edit-dockerfile.png") });
  expect(smoke.browserLog.filter(line => line.startsWith("pageerror:"))).toEqual([]);
}, 60000);
