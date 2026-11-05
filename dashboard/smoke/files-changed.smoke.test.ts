import { afterAll, beforeAll, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket, type ChangeFixtureFile } from "./delivery-fixture";

let smoke: Smoke;
const task: Record<string, number> = {};
const WIDE = { width: 1920, height: 1080 };

const lines = (prefix: string, count: number, text: string) => Array.from({ length: count }, (_, i) => `${prefix}${text} ${i + 1}.`);
const patch = (...rows: string[]) => `${rows.join("\n")}\n`;

const FILES: ChangeFixtureFile[] = [
  { status: "modified", path: "dashboard/src/ui/review.js", old_mode: "100644", new_mode: "100644", patch: patch(
    "diff --git a/dashboard/src/ui/review.js b/dashboard/src/ui/review.js",
    "index 1111111..2222222 100644",
    "--- a/dashboard/src/ui/review.js",
    "+++ b/dashboard/src/ui/review.js",
    "@@ -1,4 +1,5 @@",
    " import { relativeAge } from \"./format.js\";",
    "-const LIMIT = 10;",
    "+const LIMIT = 20;",
    "+const LABEL = \"new\";",
    " export function view() {",
    "   return LIMIT;") },
  { status: "added", path: "docs/reader.md", new_mode: "100644", patch: patch(
    "diff --git a/docs/reader.md b/docs/reader.md",
    "new file mode 100644",
    "index 0000000..3333333",
    "--- /dev/null",
    "+++ b/docs/reader.md",
    "@@ -0,0 +1,80 @@",
    ...lines("+", 80, "The reader guide line")) },
  { status: "deleted", path: "old/notes.txt", old_mode: "100644", patch: patch(
    "diff --git a/old/notes.txt b/old/notes.txt",
    "deleted file mode 100644",
    "index 4444444..0000000",
    "--- a/old/notes.txt",
    "+++ /dev/null",
    "@@ -1,2 +0,0 @@",
    "-The first note.",
    "-The second note.") },
  { status: "renamed", path: "docs/guide.md", previous_path: "guide.md", old_mode: "100644", new_mode: "100644", similarity: 100, patch: patch(
    "diff --git a/guide.md b/docs/guide.md",
    "similarity index 100%",
    "rename from guide.md",
    "rename to docs/guide.md") },
  { status: "modified", path: "scripts/run.sh", old_mode: "100644", new_mode: "100755", patch: patch(
    "diff --git a/scripts/run.sh b/scripts/run.sh",
    "old mode 100644",
    "new mode 100755") },
  { status: "modified", path: "assets/logo.png", old_mode: "100644", new_mode: "100644", binary: true, patch: patch(
    "diff --git a/assets/logo.png b/assets/logo.png",
    "index 5555555..6666666 100644",
    "Binary files a/assets/logo.png and b/assets/logo.png differ") },
  { status: "added", path: "data/large.txt", new_mode: "100644", patch: patch(
    "diff --git a/data/large.txt b/data/large.txt",
    "new file mode 100644",
    "index 0000000..7777777",
    "--- /dev/null",
    "+++ b/data/large.txt",
    "@@ -0,0 +1,3000 @@",
    ...lines("+", 3000, "A line of the large file")) },
];

// Repository text in a path and in a patch shows as text and runs nothing.
const INERT: ChangeFixtureFile[] = [
  { status: "added", path: "docs/<b>bold</b>.md", new_mode: "100644", patch: patch(
    "diff --git a/docs/<b>bold</b>.md b/docs/<b>bold</b>.md",
    "new file mode 100644",
    "index 0000000..7777777",
    "--- /dev/null",
    "+++ b/docs/<b>bold</b>.md",
    "@@ -0,0 +1,2 @@",
    "+<img src=\"x\" onerror=\"window.__inert = 1\">",
    "+<script>window.__inert = 2</script>") },
];

const SEEDS = ["first", "second", "plain", "inert", "expand"] as const;
const sha1 = (text: string) => createHash("sha1").update(text).digest("hex");

const EXPAND_PATH = "src/expand.ts";
const expandLines = (changed: boolean) => Array.from({ length: 60 }, (_, i) =>
  i === 29 && changed ? "export const line30 = 300;" : `export const line${i + 1} = ${i + 1};`).join("\n") + "\n";

/** Writes two commits of one file to the Project repository through a separate
 * index, so the branches and the working tree stay the same. Returns the
 * commits and the Git patch of the file. */
function commitPair(workspace: string) {
  const env = {
    ...process.env, GIT_INDEX_FILE: join(smoke.scratch, "expand-index"), GIT_CONFIG_GLOBAL: "/dev/null", GIT_CONFIG_NOSYSTEM: "1",
    GIT_AUTHOR_NAME: "Smoke", GIT_AUTHOR_EMAIL: "smoke@example.invalid", GIT_COMMITTER_NAME: "Smoke", GIT_COMMITTER_EMAIL: "smoke@example.invalid",
  };
  const git = (args: string[], input?: string) => {
    const result = Bun.spawnSync(["git", "-C", workspace, ...args], { env, stdin: input === undefined ? "ignore" : Buffer.from(input) });
    if (result.exitCode !== 0) throw new Error(`git ${args.join(" ")}: ${result.stderr.toString()}`);
    return result.stdout.toString();
  };
  const commit = (body: string, parent?: string) => {
    const blob = git(["hash-object", "-w", "--stdin"], body).trim();
    git(["update-index", "--add", "--cacheinfo", `100644,${blob},${EXPAND_PATH}`]);
    const tree = git(["write-tree"]).trim();
    return git(["commit-tree", tree, ...(parent ? ["-p", parent] : []), "-m", "Smoke"]).trim();
  };
  const from = commit(expandLines(false));
  const to = commit(expandLines(true), from);
  const patch = git(["diff", "--no-color", "--unified=3", "--src-prefix=a/", "--dst-prefix=b/", from, to, "--", EXPAND_PATH]);
  return { from, to, patch };
}

beforeAll(async () => {
  smoke = await bootSmoke();
  await smoke.page.setViewportSize(WIDE);
  const workspace = await seedProject(smoke, "changes", { title: "Changes", vision: false });
  const expand = commitPair(workspace);
  const plane = join(smoke.scratch, "artifacts", "changes");
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "changes"\naccess = "ro"\n`, { mode: 0o600 });
  for (const name of SEEDS) {
    task[name] = Number((await mc(["task", "add", `${name} task`, "--project", "changes", "--description", "The change must hold each file."]))["task_id"]);
  }
  const db = openSpine(smoke);
  try {
    db.run("UPDATE settings SET review_wip_cap=10 WHERE id=1");
    SEEDS.forEach((name, index) => {
      const id = task[name]!;
      const body = `<!doctype html><h1>The ${name} report</h1>${lines("<p>", 60, `The ${name} packet explains its change. Paragraph`).join("")}`;
      mkdirSync(join(plane, String(id), "packet"), { recursive: true });
      writeFileSync(join(plane, String(id), "packet", "report.html"), body);
      seedDeliveryPacket(db, {
        taskID: id, thesis: `The ${name} packet thesis`,
        renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "changes", String(id), "packet", "report.html"),
        createdAt: new Date(Date.now() - (10 - index) * 3600_000).toISOString().replace("T", " ").slice(0, 19),
        reportSHA256: new Bun.CryptoHasher("sha256").update(body).digest("hex"),
        changes: { plane, files: name === "plain" ? FILES.slice(0, 1) : name === "inert" ? INERT : name === "expand"
          ? [{ status: "modified", path: EXPAND_PATH, old_mode: "100644", new_mode: "100644", patch: expand.patch }] : FILES },
        ...(name === "expand" ? { input: expand.from, commit: expand.to } : {}),
      });
    });
  } finally { db.close(); }
  await gotoReady(smoke);
  await smoke.page.getByTestId("project-changes").waitFor({ state: "attached" });
  await selectProject(smoke, "changes");
}, 90000);
afterAll(async () => { await teardown(smoke); });

async function readerShows(name: string) {
  await smoke.page.waitForFunction((id) => location.hash === `#review/${id}` &&
    (document.querySelector('[data-testid="reader-title"]')?.textContent ?? "").startsWith(`Task #${id} · `), task[name], { timeout: 15000 });
}

/** Opens the reader of one packet from its address and waits for its report. */
async function openPacket(name: string) {
  const { page, server } = smoke;
  await page.goto(`${server.url}?changes=${crypto.randomUUID()}#review/${task[name]}`);
  await page.waitForSelector("body[data-ready]");
  await readerShows(name);
  await page.frameLocator('[data-testid="packet-frame"]').getByText(`The ${name} report`, { exact: true }).waitFor();
}

async function openChanges(name: string) {
  await openPacket(name);
  await smoke.page.getByTestId("reader-view-changes").click();
  await smoke.page.getByTestId("changes-path").waitFor();
}

const treeItem = (path: string) => smoke.page.locator(`file-tree-container [data-item-path="${path}"]`);
const shownPath = () => smoke.page.getByTestId("changes-path").innerText();

async function showFile(path: string) {
  await treeItem(path).click();
  await smoke.page.waitForFunction((path) => document.querySelector('[data-testid="changes-path"]')?.textContent === path, path);
}

/** The paths of the file rows of the tree. */
const treeFiles = () => smoke.page.locator("file-tree-container [data-item-path]").evaluateAll((nodes) =>
  nodes.map((node) => node.getAttribute("data-item-path")!).filter((path) => !path.endsWith("/")));

/** The rendered rows of the diff of the selected file. */
const diffRows = () => smoke.page.evaluate(() => {
  const roots = [...document.querySelectorAll('[data-testid="changes-diff"] diffs-container')].map((node) => node.shadowRoot!);
  const rows = roots.flatMap((root) => [...root.querySelectorAll("[data-line]")]);
  return {
    parts: roots.length,
    type: roots[0]?.querySelector("pre")?.getAttribute("data-diff-type") ?? "",
    added: rows.filter((row) => row.getAttribute("data-line-type") === "change-addition").length,
    removed: rows.filter((row) => row.getAttribute("data-line-type") === "change-deletion").length,
    text: rows.map((row) => row.textContent).join("\n"),
    colors: new Set(rows.flatMap((row) => [...row.querySelectorAll("span[style]")].map((span) => getComputedStyle(span).color))).size,
    numbers: roots.flatMap((root) => [...root.querySelectorAll("[data-line-number-content]")]).length,
  };
});

const factTitles = () => smoke.page.getByTestId("changes-facts").locator(".fact").evaluateAll((nodes) => nodes.map((node) => [
  node.querySelector(".fact-label")!.textContent, node.querySelector(".fact-detail")?.textContent ?? "", node.querySelector("button")?.getAttribute("title") ?? ""]));

test("the Packet view opens by default and Files changed shows the changes of the merge candidate", async () => {
  const { page } = smoke;
  const reads: string[] = [];
  const listen = (request: { url(): string }) => { if (/^\/api\/packets\/\d+\/changes/.test(new URL(request.url()).pathname)) reads.push(request.url()); };
  page.on("request", listen);
  try {
    await openPacket("first");
    // The Packet view shows the report without the Git records of the change.
    expect(await page.getByTestId("reader-view-packet").getAttribute("aria-selected")).toBe("true");
    expect(await page.getByTestId("changes-view").evaluate((node) => (node as HTMLElement).inert)).toBe(true);
    expect(await page.getByTestId("changes-view").isVisible()).toBe(false);
    expect(await page.locator('[data-testid="packet-facts-strip"], [data-testid="packet-facts"]').count()).toBe(0);
    expect(await page.getByTestId("reader-view-changes").innerText()).toBe("Files changed · 7");
    expect(await page.getByTestId("changes-tools").isVisible()).toBe(false);
    // The page reads the change data only when the operator opens the view.
    expect(reads).toEqual([]);

    await page.getByTestId("reader-view-changes").click();
    await page.getByTestId("changes-path").waitFor();
    expect(await page.getByTestId("reader-view-changes").getAttribute("aria-selected")).toBe("true");
    expect(await page.getByTestId("packet-render").isVisible()).toBe(false);
    expect(reads.some((url) => url.includes("packet=") && url.includes("version="))).toBe(true);
    // The view holds one comparison and no comparison control.
    expect(await page.getByTestId("changes-view").locator("[data-comparison]").count()).toBe(0);
    expect((await treeFiles()).sort()).toEqual(FILES.map((file) => file.path).sort());
    expect(await page.getByTestId("changes-summary").innerText()).toBe("7 files · +3082 −3");
    // The tree order starts with the first directory, and the view shows its first file.
    expect(await shownPath()).toBe("assets/logo.png");
    // The view row holds the display controls, and the tree and the diff start directly below it.
    const views = (await page.getByTestId("reader-views").boundingBox())!;
    for (const testid of ["changes-tree-toggle", "changes-layout-unified", "changes-layout-split"]) {
      const control = (await page.getByTestId(testid).boundingBox())!;
      expect(control.y >= views.y && control.y + control.height <= views.y + views.height).toBe(true);
    }
    expect((await page.getByTestId("changes-side").boundingBox())!.y).toBeCloseTo(views.y + views.height, 0);
    expect((await page.getByTestId("changes-file").boundingBox())!.y).toBeCloseTo(views.y + views.height, 0);
    // The commits name the range and the verified Worker commit.
    const worker = sha1(`smoke-task-${task.first}`);
    expect(await factTitles()).toEqual([
      ["Destination commit", "main", sha1(`smoke-input-${task.first}`)],
      ["Merge candidate commit", "", worker],
      ["Verified Worker commit", "", worker],
    ]);
    expect(await page.getByTestId("approval-scope").count()).toBe(0);
    // The decision controls stay available in the Files changed view.
    await page.waitForFunction(() => !(document.querySelector('[data-testid="packet-approve"]') as HTMLButtonElement).disabled);
    await showFile("dashboard/src/ui/review.js");
    await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
    await page.waitForFunction(() => (document.querySelector('[data-testid="changes-diff"] diffs-container')?.shadowRoot?.querySelectorAll("span[style]").length ?? 0) > 0);
    const rows = await diffRows();
    expect(rows).toMatchObject({ parts: 1, type: "single", added: 2, removed: 1 });
    expect(rows.text).toContain("const LIMIT = 20;");
    // The diff shows line numbers and syntax colors.
    expect(rows.numbers).toBeGreaterThan(4);
    expect(rows.colors).toBeGreaterThan(3);
    await page.screenshot({ path: evidencePath("files-changed-1920.png") });

    // The diff follows the theme of the dashboard, and its ground fills the view.
    const ground = () => page.evaluate(() => {
      const view = document.querySelector('[data-testid="changes-diff"]')!;
      const pre = view.querySelector("diffs-container")!.shadowRoot!.querySelector("pre")!;
      return { theme: document.documentElement.dataset.theme!, diff: getComputedStyle(pre).backgroundColor, view: getComputedStyle(view).backgroundColor };
    });
    const first = await ground();
    expect(first.diff).toBe(first.view);
    const other = first.theme === "dark" ? "light" : "dark";
    await page.emulateMedia({ colorScheme: other });
    await page.waitForFunction((was) => getComputedStyle(document.querySelector('[data-testid="changes-diff"] diffs-container')!
      .shadowRoot!.querySelector("pre")!).backgroundColor !== was, first.diff);
    const second = await ground();
    expect(second.theme).toBe(other);
    expect(second.diff).toBe(second.view);
    expect((await diffRows()).colors).toBeGreaterThan(3);
    await page.screenshot({ path: evidencePath(`files-changed-${other}.png`) });
    await page.emulateMedia({ colorScheme: first.theme as "light" | "dark" });
    await page.waitForFunction((want) => document.documentElement.dataset.theme === want, first.theme);
  } finally { page.off("request", listen); }
}, 30000);

test("each change type states its kind, and a change without a text diff states why", async () => {
  const { page } = smoke;
  await openChanges("first");
  const cases: [string, string, string[]][] = [
    ["assets/logo.png", "Modified", ["Git treats this file as binary. The comparison holds no text diff for it."]],
    ["docs/guide.md", "Renamed", ["Renamed from guide.md with 100% similarity.", "The file content is unchanged."]],
    ["scripts/run.sh", "Modified", ["The file mode changed from 100644 (file) to 100755 (executable file).", "The file content is unchanged."]],
    ["old/notes.txt", "Deleted", []],
    ["docs/reader.md", "Added", []],
  ];
  for (const [path, label, notes] of cases) {
    await showFile(path);
    expect(await page.getByTestId("changes-file").locator(".tag").innerText()).toBe(label);
    expect(await page.getByTestId("changes-notes").locator("li").allInnerTexts()).toEqual(notes);
    expect(await page.getByTestId("changes-notes").isVisible()).toBe(notes.length > 0);
    if (notes.length > 0) {
      expect(await page.locator('[data-testid="changes-diff"] diffs-container').count()).toBe(0);
    } else {
      await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
    }
  }
  const deleted = await (async () => { await showFile("old/notes.txt"); await page.locator('[data-testid="changes-diff"] diffs-container').waitFor(); return diffRows(); })();
  expect(deleted).toMatchObject({ added: 0, removed: 2 });
  // The tree marks each change type.
  const status = await page.locator("file-tree-container [data-item-path]").evaluateAll((nodes) =>
    Object.fromEntries(nodes.map((node) => [node.getAttribute("data-item-path"), node.getAttribute("data-item-git-status") ?? ""])));
  expect(status["old/notes.txt"]).toBe("deleted");
  expect(status["docs/reader.md"]).toBe("added");
  expect(status["docs/guide.md"]).toBe("renamed");
}, 30000);

test("a large diff waits for the operator and then shows every line", async () => {
  const { page } = smoke;
  await openChanges("first");
  await showFile("data/large.txt");
  expect(await page.getByTestId("changes-deferred").innerText()).toContain("This diff holds 3000 changed lines");
  expect(await page.locator('[data-testid="changes-diff"] diffs-container').count()).toBe(0);
  await page.getByTestId("changes-show-diff").click();
  await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
  const rows = await diffRows();
  expect(rows.added).toBe(3000);
  expect(rows.text).toContain("A line of the large file 3000.");
}, 30000);

test("the operator selects the unified or the side-by-side layout", async () => {
  const { page } = smoke;
  await openChanges("first");
  await showFile("dashboard/src/ui/review.js");
  await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
  expect((await diffRows()).type).toBe("single");
  await page.getByTestId("changes-layout-split").click();
  await page.waitForFunction(() => document.querySelector('[data-testid="changes-diff"] diffs-container')?.shadowRoot?.querySelector('pre[data-diff-type="split"]') !== null);
  expect(await page.getByTestId("changes-layout-split").getAttribute("aria-pressed")).toBe("true");
  // The layout applies to the next file too.
  await showFile("old/notes.txt");
  await showFile("dashboard/src/ui/review.js");
  await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
  expect((await diffRows()).type).toBe("split");
  await page.screenshot({ path: evidencePath("files-changed-split-1920.png") });
  await page.getByTestId("changes-layout-unified").click();
  await page.waitForFunction(() => document.querySelector('[data-testid="changes-diff"] diffs-container')?.shadowRoot?.querySelector('pre[data-diff-type="single"]') !== null);
}, 30000);

test("the operator hides and shows the file tree, and the browser keeps the choice", async () => {
  const { page } = smoke;
  try {
    await openChanges("first");
    await showFile("dashboard/src/ui/review.js");
    const toggle = page.getByTestId("changes-tree-toggle");
    expect(await toggle.getAttribute("aria-expanded")).toBe("true");
    expect(await toggle.innerText()).toBe("Hide the file tree");
    const before = (await page.getByTestId("changes-file").boundingBox())!;
    await toggle.click();
    expect(await page.getByTestId("changes-side").isVisible()).toBe(false);
    expect(await toggle.getAttribute("aria-expanded")).toBe("false");
    expect(await toggle.innerText()).toBe("Show the file tree");
    // The diff takes the width of the tree column.
    const after = (await page.getByTestId("changes-file").boundingBox())!;
    expect(after.x).toBeLessThan(before.x);
    expect(after.width).toBeGreaterThan(before.width + 200);
    // The previous and next controls still move through the files in the order of the tree.
    await page.getByTestId("changes-next").click();
    await page.waitForFunction(() => document.querySelector('[data-testid="changes-path"]')?.textContent === "data/large.txt");
    // Another packet and a reload keep the hidden tree.
    await openChanges("second");
    expect(await page.getByTestId("changes-side").isVisible()).toBe(false);
    expect(await toggle.getAttribute("aria-expanded")).toBe("false");
    await page.screenshot({ path: evidencePath("files-changed-no-tree-1920.png") });
    // The tree shows each file again.
    await toggle.click();
    await treeItem("docs/reader.md").waitFor();
    expect((await treeFiles()).sort()).toEqual(FILES.map((file) => file.path).sort());
    expect(await toggle.getAttribute("aria-expanded")).toBe("true");
    await showFile("docs/reader.md");
  } finally {
    await page.evaluate(() => localStorage.removeItem("mc.changes-tree"));
  }
}, 30000);

test("the operator expands the unmodified lines from the versions of the file in the Project repository", async () => {
  const { page } = smoke;
  const route = "**/api/packets/*/changes/files?*";
  try {
    await openChanges("expand");
    expect(await shownPath()).toBe(EXPAND_PATH);
    await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
    const patch = await diffRows();
    expect(patch).toMatchObject({ added: 1, removed: 1 });
    expect(patch.text).not.toContain("export const line1 = 1;");
    // The patch states the size of the region above the change only.
    const regions = async () => [...new Set(await page.locator('[data-testid="changes-diff"] [data-unmodified-lines]').allInnerTexts())];
    expect(await regions()).toEqual(["26 unmodified lines", "More unchanged context may be available"]);
    // A failed read states its cause, and the diff keeps the patch.
    await page.route(route, (request) => request.fulfill({ status: 500, json: { error: { code: "environment", message: "The repository is unreachable." } } }));
    const expand = page.locator('[data-testid="changes-diff"] [data-expand-button]').first();
    await expand.click();
    await page.getByTestId("changes-expand-error").waitFor();
    expect(await page.getByTestId("changes-expand-error").innerText()).toBe("The unmodified lines are unavailable. The repository is unreachable.");
    await page.screenshot({ path: evidencePath("files-changed-expand-error-1920.png") });
    await page.unroute(route);
    // The next request reads the file at the two commits of the comparison.
    await expand.click();
    await page.waitForFunction(() => [...document.querySelectorAll('[data-testid="changes-diff"] diffs-container')]
      .some((node) => node.shadowRoot?.textContent?.includes("export const line1 = 1;")));
    expect(await page.getByTestId("changes-expand-error").count()).toBe(0);
    expect(await page.getByTestId("changes-notes").isVisible()).toBe(false);
    const expanded = await diffRows();
    expect(expanded).toMatchObject({ added: 1, removed: 1 });
    expect(expanded.text).toContain("export const line26 = 26;");
    expect(expanded.text).not.toContain("export const line60 = 60;");
    // The file versions also state the size of the region below the change.
    expect(await regions()).toEqual(["27 unmodified lines"]);
    await page.screenshot({ path: evidencePath("files-changed-expanded-1920.png") });
  } finally {
    await page.unroute(route);
  }
}, 30000);

test("another packet opens in the Packet view, and an absent merge candidate is explicit", async () => {
  const { page } = smoke;
  await openChanges("first");
  await showFile("docs/reader.md");
  // Another packet opens in the Packet view. Its Files changed view shows its first file.
  await page.getByTestId("reader-next").click();
  await readerShows("second");
  expect(await page.getByTestId("reader-view-packet").getAttribute("aria-selected")).toBe("true");
  await page.getByTestId("reader-view-changes").click();
  await page.waitForFunction(() => document.querySelector('[data-testid="changes-summary"]')?.textContent === "7 files · +3082 −3");
  expect(await shownPath()).toBe("assets/logo.png");

  /** Shows the Files changed view of the second packet without the given facts. */
  const unavailable = async (absent: Record<string, null>) => {
    await page.route("**/api/packets", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.packets = body.packets.map((row: any) => row.task_id === task.second ? { ...row, ...absent } : row);
      await route.fulfill({ response, json: body });
    });
    try {
      await openPacket("second");
      expect(await page.getByTestId("reader-view-changes").innerText()).toBe("Files changed");
      await page.getByTestId("reader-view-changes").click();
      await page.getByTestId("changes-unavailable").waitFor();
      return { text: await page.getByTestId("changes-unavailable").innerText(), facts: await factTitles() };
    } finally { await page.unroute("**/api/packets"); }
  };
  const destination = sha1(`smoke-input-${task.second}`);
  const submitted = sha1(`smoke-task-${task.second}`);
  // A merge candidate without change data states that Culler recorded none.
  expect(await unavailable({ candidate_changes: null })).toEqual({
    text: "Culler recorded no change data for this merge candidate.",
    facts: [
      ["Destination commit", "main", destination],
      ["Merge candidate commit", "", submitted],
      ["Verified Worker commit", "", submitted],
    ],
  });
  // A packet without a merge candidate states that no merge candidate is available.
  expect(await unavailable({ destination_commit: null, candidate_commit: null, candidate_changes: null })).toEqual({
    text: "No merge candidate is available for this packet.",
    facts: [["Verified Worker commit", "", submitted]],
  });
  expect(await page.getByTestId("changes-file").isVisible()).toBe(false);
}, 30000);

test("the keyboard selects the view and moves through the files with visible focus", async () => {
  const { page } = smoke;
  await openPacket("first");
  const focused = () => page.evaluate(() => document.activeElement?.getAttribute("data-testid") ?? document.activeElement?.tagName);
  const ring = (testid: string) => page.getByTestId(testid).evaluate((node) => getComputedStyle(node).outlineStyle !== "none");
  await page.getByTestId("reader-view-packet").focus();
  await page.keyboard.press("ArrowRight");
  await page.getByTestId("changes-path").waitFor();
  expect(await focused()).toBe("reader-view-changes");
  expect(await page.getByTestId("reader-view-changes").getAttribute("aria-selected")).toBe("true");
  expect(await ring("reader-view-changes")).toBe(true);

  // The tree takes the focus, and the arrow keys and Enter select a file.
  await treeItem("assets/logo.png").focus();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector('[data-testid="changes-path"]')?.textContent === "dashboard/src/ui/review.js");
  const tree = await page.evaluate(() => {
    const root = document.querySelector("file-tree-container")!.shadowRoot!;
    const active = root.activeElement as HTMLElement | null;
    const style = active ? getComputedStyle(active, "::before") : null;
    return {
      host: document.activeElement?.tagName, path: active?.getAttribute("data-item-path"),
      ring: style !== null && style.outlineStyle === "solid" &&
        style.outlineWidth === "2px" && style.outlineColor !== "rgba(0, 0, 0, 0)",
    };
  });
  expect(tree).toEqual({ host: "FILE-TREE-CONTAINER", path: "dashboard/src/ui/review.js", ring: true });

  // The next file control keeps the focus while the view moves.
  await page.getByTestId("changes-next").focus();
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector('[data-testid="changes-path"]')?.textContent === "data/large.txt");
  expect(await focused()).toBe("changes-next");
  expect(await ring("changes-next")).toBe(true);
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector('[data-testid="changes-path"]')?.textContent === "docs/guide.md");

  // The arrow keys return to the Packet view.
  await page.getByTestId("reader-view-changes").focus();
  await page.keyboard.press("ArrowLeft");
  expect(await focused()).toBe("reader-view-packet");
  expect(await page.getByTestId("reader-view-packet").getAttribute("aria-selected")).toBe("true");
  await page.frameLocator('[data-testid="packet-frame"]').getByText("The first report", { exact: true }).waitFor();
}, 30000);

test("each view keeps its position when the operator switches views", async () => {
  const { page } = smoke;
  await openChanges("first");
  await showFile("data/large.txt");
  await page.getByTestId("changes-show-diff").click();
  await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
  await page.getByTestId("changes-diff").evaluate((node) => { node.scrollTop = 2000; });
  await page.getByTestId("reader-view-packet").click();
  const frame = (await (await page.getByTestId("packet-frame").elementHandle())!.contentFrame())!;
  await frame.evaluate(() => window.scrollTo(0, 500));
  await page.getByTestId("reader-view-changes").click();
  expect(await page.getByTestId("changes-diff").evaluate((node) => node.scrollTop)).toBe(2000);
  expect(await shownPath()).toBe("data/large.txt");
  await page.getByTestId("reader-view-packet").click();
  expect(await frame.evaluate(() => window.scrollY)).toBe(500);
}, 30000);

test("rapid packet changes show only the change data of the open packet", async () => {
  const { page } = smoke;
  await openPacket("first");
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  let requested!: () => void;
  const seen = new Promise<void>((resolve) => { requested = resolve; });
  const pattern = `**/api/packets/${task.first}/changes?*`;
  await page.route(pattern, async (route) => { requested(); await gate; await route.continue(); });
  try {
    await page.getByTestId("reader-view-changes").click();
    await seen;
    expect(await page.getByTestId("changes-state").innerText()).toBe("Loading change data…");
    await page.locator(`[data-testid="packet-row"][data-task="${task.plain}"]`).click();
    await readerShows("plain");
    await page.getByTestId("reader-view-changes").click();
    await page.waitForFunction(() => document.querySelector('[data-testid="changes-summary"]')?.textContent === "1 file · +2 −1");
    release();
    await Bun.sleep(500);
    expect(await treeFiles()).toEqual(["dashboard/src/ui/review.js"]);
    expect(await page.getByTestId("changes-summary").innerText()).toBe("1 file · +2 −1");
    expect(await page.getByTestId("changes-state").isVisible()).toBe(false);
  } finally {
    release();
    await page.unroute(pattern);
  }

  // A late diff of a file that the operator left changes nothing.
  await openChanges("first");
  let releasePatch!: () => void;
  const patchGate = new Promise<void>((resolve) => { releasePatch = resolve; });
  const patchPattern = `**/api/packets/${task.first}/changes/patch?*`;
  await page.route(patchPattern, async (route) => {
    if (new URL(route.request().url()).searchParams.get("file") === "0") await patchGate;
    await route.continue();
  });
  try {
    await showFile("dashboard/src/ui/review.js");
    await showFile("old/notes.txt");
    await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
    releasePatch();
    await Bun.sleep(500);
    expect(await shownPath()).toBe("old/notes.txt");
    expect(await diffRows()).toMatchObject({ added: 0, removed: 2 });
  } finally {
    releasePatch();
    await page.unroute(patchPattern);
  }
}, 30000);

test("repository text in a path and in a diff shows as text and runs nothing", async () => {
  const { page } = smoke;
  await openChanges("inert");
  expect(await shownPath()).toBe("docs/<b>bold</b>.md");
  expect(await treeFiles()).toEqual(["docs/<b>bold</b>.md"]);
  await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
  const rows = await diffRows();
  expect(rows.text).toContain('<img src="x" onerror="window.__inert = 1">');
  expect(rows.text).toContain("<script>window.__inert = 2</script>");
  const inert = await page.evaluate(() => {
    const roots: (Document | ShadowRoot)[] = [document];
    for (const host of document.querySelectorAll("file-tree-container, diffs-container")) roots.push(host.shadowRoot!);
    return {
      ran: (window as { __inert?: number }).__inert ?? null,
      markup: roots.flatMap((root) => [...root.querySelectorAll("img[onerror], script:not([src]), b")])
        .filter((node) => node.closest('[data-testid="changes-view"]') !== null || node.getRootNode() instanceof ShadowRoot).length,
    };
  });
  expect(inert).toEqual({ ran: null, markup: 0 });
}, 30000);

test("a local-only deployment loads every viewer asset from the dashboard", async () => {
  const { page, server } = smoke;
  const origin = new URL(server.url).origin;
  const outside: string[] = [];
  const failed: string[] = [];
  const modules = new Map<string, number>();
  await page.route("**/*", (route) => {
    if (new URL(route.request().url()).origin === origin) return route.continue();
    outside.push(route.request().url());
    return route.abort();
  });
  const onResponse = (response: { url(): string; status(): number }) => {
    const url = new URL(response.url());
    if (url.pathname.startsWith("/pierre/")) modules.set(url.pathname, response.status());
  };
  const onFailure = (request: { url(): string }) => { failed.push(request.url()); };
  page.on("response", onResponse);
  page.on("requestfailed", onFailure);
  try {
    await openChanges("first");
    await showFile("dashboard/src/ui/review.js");
    await page.waitForFunction(() => (document.querySelector('[data-testid="changes-diff"] diffs-container')?.shadowRoot?.querySelectorAll("span[style]").length ?? 0) > 0);
    await showFile("docs/reader.md");
    await page.waitForFunction(() => (document.querySelector('[data-testid="changes-diff"] diffs-container')?.shadowRoot?.querySelectorAll("span[style]").length ?? 0) > 0);
    expect(outside).toEqual([]);
    expect(failed).toEqual([]);
    expect(modules.get("/pierre/pierre.js")).toBe(200);
    expect(modules.size).toBeGreaterThan(3);
    for (const [path, status] of modules) expect([path, status]).toEqual([path, 200]);
  } finally {
    page.off("response", onResponse);
    page.off("requestfailed", onFailure);
    await page.unroute("**/*");
  }
}, 30000);

test("an expired packet states the expiry of its change data", async () => {
  const { page } = smoke;
  await page.route("**/api/packets", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.packets = body.packets.map((row: any) => row.task_id === task.second
      ? { ...row, artifact_retention: { status: "expired", expired_at: "2026-09-20 09:14:00" } } : row);
    await route.fulfill({ response, json: body });
  });
  try {
    await smoke.page.goto(`${smoke.server.url}?expired#review/${task.second}`);
    await page.waitForSelector("body[data-ready]");
    await readerShows("second");
    await page.getByTestId("reader-view-changes").click();
    await page.getByTestId("changes-unavailable").waitFor();
    expect(await page.getByTestId("changes-unavailable").innerText())
      .toMatch(/^The change data expired on Sep 20, 2026 \d{2}:\d{2}\. The repository keeps the commits\.$/);
  } finally { await page.unroute("**/api/packets"); }
}, 30000);

test("at 390 px Files changed fits the window, with the tree above the diff", async () => {
  const { page, server } = smoke;
  const narrow = { width: 390, height: 844 };
  try {
    await page.setViewportSize(narrow);
    await page.goto(`${server.url}?changes-narrow#review`);
    await page.waitForSelector("body[data-ready]");
    await page.locator(`[data-testid="packet-row"][data-task="${task.first}"]`).click();
    await readerShows("first");
    await page.getByTestId("reader-view-changes").click();
    await showFile("dashboard/src/ui/review.js");
    await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
    const tree = await page.getByTestId("changes-tree").boundingBox();
    const diff = await page.getByTestId("changes-file").boundingBox();
    expect(tree!.y + tree!.height).toBeLessThanOrEqual(diff!.y + 0.5);
    const fit = await page.evaluate((width) => {
      const reader = document.querySelector('[data-testid="packet-reader"]')!;
      const outside = [...reader.querySelectorAll("*")].filter((node) => {
        if (!node.checkVisibility({ visibilityProperty: true }) || node.closest('[data-testid="changes-diff"]')) return false;
        const rect = node.getBoundingClientRect();
        return rect.right > width + 0.5 || rect.left < -0.5;
      }).map((node) => node.id || node.className || node.tagName);
      return { outside, scroll: document.documentElement.scrollWidth };
    }, narrow.width);
    expect(fit.outside).toEqual([]);
    expect(fit.scroll).toBeLessThanOrEqual(narrow.width);
    const short: [string, number][] = [];
    for (const testid of ["reader-view-packet", "reader-view-changes", "packet-task-open",
      "changes-tree-toggle", "changes-layout-split", "changes-previous", "changes-next"]) {
      const height = (await page.getByTestId(testid).boundingBox())!.height;
      if (height < 40) short.push([testid, height]);
    }
    expect(short).toEqual([]);
    await page.screenshot({ path: evidencePath("files-changed-390.png") });
    // The view scrolls as one column: the diff follows the tree, and the file header stays visible.
    await showFile("data/large.txt");
    await page.getByTestId("changes-show-diff").click();
    await page.locator('[data-testid="changes-diff"] diffs-container').waitFor();
    const view = page.getByTestId("changes-view");
    await view.evaluate((node) => { node.scrollTop = 3000; });
    const column = await view.evaluate((node) => {
      const head = document.querySelector('[data-testid="changes-path"]')!.parentElement!.getBoundingClientRect();
      const diff = document.querySelector('[data-testid="changes-diff"]')!.getBoundingClientRect();
      const box = node.getBoundingClientRect();
      return {
        scrolled: node.scrollTop > 0, wide: node.scrollWidth > node.clientWidth,
        headAtTop: Math.abs(head.top - box.top) < 1, diffVisible: diff.top < box.bottom && diff.bottom > head.bottom,
      };
    });
    expect(column).toEqual({ scrolled: true, wide: false, headAtTop: true, diffVisible: true });
    await page.screenshot({ path: evidencePath("files-changed-390-diff.png") });
  } finally {
    await page.setViewportSize(WIDE);
  }
}, 30000);
