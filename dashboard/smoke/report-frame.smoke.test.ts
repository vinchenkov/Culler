import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { Frame } from "playwright";
import { bootSmoke, evidencePath, gotoReady, mc, seedProject, selectProject, teardown, type Smoke, openSpine } from "./harness";
import { seedDeliveryPacket } from "./delivery-fixture";

let smoke: Smoke;
let taskID = 0;
// A second origin records each request that reaches it. A link to an
// absolute address names this origin.
const otherHits: string[] = [];
const other = Bun.serve({
  hostname: "127.0.0.1", port: 0,
  fetch(req) {
    otherHits.push(new URL(req.url).pathname);
    return new Response("<!doctype html><h1>Another address</h1>", { headers: { "content-type": "text/html" } });
  },
});
const OTHER = `http://127.0.0.1:${other.port}`;
const pageRequests: string[] = [];

const block = "display:block;width:320px;max-width:100%;height:48px";
const svgLink = (address: string, label: string) =>
  `<svg width="320" height="48" style="display:block"><a xlink:href="${address}"><rect width="320" height="48" fill="#2e6b96"/><text x="12" y="30" fill="#fff">${label}</text></a></svg>`;

// Each link case of the report frame. The nested form case is last: the
// first parse of that markup holds the rest of the report as style text.
const REPORT = `<!doctype html><html style="background: black !important; color-scheme: dark !important"><head><meta charset="utf-8"><title>Link report</title>
<style>
html, body { background: white !important; color: black !important; font: 22px/2 serif !important; padding: 80px !important; }
.diagram { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.diagram div { background: #e5f0fa; color: #163a5a; padding: 8px; }
</style></head><body style="background: #fff !important; color: red !important; font: 30px serif !important">
<h1>The link report</h1>
<figure class="diagram"><div>The source</div><div>The result</div></figure>
${svgLink(`${OTHER}/svg-xlink`, "SVG guide")}
${svgLink("verification.html", "SVG evidence")}
<p><a href="" style="${block}">Top of the report</a></p>
<p><a href="verification.html" style="${block}">Relative evidence</a><a href="/x" style="${block}">Root evidence</a></p>
<p><a href="${OTHER}/absolute" style="${block}">Absolute guide</a></p>
<div><template shadowrootmode="open"><a href="${OTHER}/shadow" style="${block}">Shadow link</a></template></div>
${Array.from({ length: 30 }, (_, i) => `<p>Report paragraph ${i + 1}.</p>`).join("")}
<form><math><mtext></form><form><mglyph><style></math><a href="${OTHER}/mutation" style="${block}">Mutation link</a>
</body></html>`;

beforeAll(async () => {
  smoke = await bootSmoke();
  smoke.context.on("request", (request) => pageRequests.push(request.url()));
  await seedProject(smoke, "links", { title: "Links", vision: false });
  const plane = join(smoke.scratch, "reports", "links");
  writeFileSync(join(smoke.home, "mount-allowlist"), `version = 1\n\n[[allow]]\npath = ${JSON.stringify(plane)}\ntarget = "links"\naccess = "ro"\n`, { mode: 0o600 });
  taskID = Number((await mc(["task", "add", "link task", "--project", "links", "--description", "The report links behave."]))["task_id"]);
  mkdirSync(join(plane, String(taskID), "packet"), { recursive: true });
  writeFileSync(join(plane, String(taskID), "packet", "report.html"), REPORT);
  const db = openSpine(smoke);
  try {
    seedDeliveryPacket(db, {
      taskID, thesis: "The link packet thesis",
      renderPath: join(process.env["MC_ARTIFACTS_ROOT"]!, "links", String(taskID), "packet", "report.html"),
      reportSHA256: new Bun.CryptoHasher("sha256").update(REPORT).digest("hex"),
    });
  } finally { db.close(); }
  await gotoReady(smoke);
  await smoke.page.getByTestId("project-links").waitFor({ state: "attached" });
  await selectProject(smoke, "links");
  await smoke.page.getByTestId("tab-review").click();
  await smoke.page.getByTestId("packet-view-live").click();
  await smoke.page.locator(`[data-testid="packet-row"][data-task="${taskID}"]`).click();
  await report().getByRole("heading", { name: "The link report" }).waitFor();
}, 90000);
afterAll(async () => {
  await teardown(smoke);
  other.stop(true);
});

const report = () => smoke.page.frameLocator('[data-testid="packet-frame"]');

async function reportFrame(): Promise<Frame> {
  const frame = await (await smoke.page.getByTestId("packet-frame").elementHandle())?.contentFrame();
  if (!frame) throw new Error("The report frame has no document.");
  return frame;
}

test("the page style stays standard while the diagram keeps its local style", async () => {
  const frame = await reportFrame();
  const originalViewport = smoke.page.viewportSize()!;
  try {
    for (const width of [1280, 390]) {
      await smoke.page.setViewportSize({ width, height: 900 });
      const styles = await frame.evaluate(() => {
        const root = getComputedStyle(document.documentElement);
        const body = getComputedStyle(document.body);
        const diagram = getComputedStyle(document.querySelector(".diagram")!);
        const cell = getComputedStyle(document.querySelector(".diagram div")!);
        return {
          rootBackground: root.backgroundColor, rootImage: root.backgroundImage, scheme: root.colorScheme,
          background: body.backgroundColor, image: body.backgroundImage, color: body.color,
          font: body.fontFamily, size: body.fontSize, lineHeight: body.lineHeight,
          padding: body.padding, margin: body.margin,
          diagram: diagram.display, cell: cell.backgroundColor,
          overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
        };
      });
      expect(styles).toMatchObject({
        rootBackground: "rgb(253, 253, 254)", rootImage: "none", scheme: "light",
        background: "rgb(253, 253, 254)", image: "none", color: "rgb(23, 47, 71)",
        size: "14px", lineHeight: "22.68px", padding: "20px 24px", margin: "0px",
        diagram: "grid", cell: "rgb(229, 240, 250)", overflow: false,
      });
      expect(styles.font).toContain("Figtree");
      await smoke.page.screenshot({ path: evidencePath(`packet-style-${width}.png`), animations: "disabled" });
    }
  } finally {
    await smoke.page.setViewportSize(originalViewport);
  }
}, 30000);

/** The frame still holds the report and the page loaded nothing inside it. */
async function reportStays(frame: Frame) {
  expect(frame.url().startsWith("about:srcdoc")).toBe(true);
  expect(await report().getByRole("heading", { name: "The link report" }).count()).toBe(1);
  expect(await smoke.page.getByTestId("packet-frame").count()).toBe(1);
}

test("an SVG link with an XLink address opens a new tab and the frame keeps the report", async () => {
  const frame = await reportFrame();
  const guide = report().locator("svg a").first();
  expect(await guide.getAttribute("href")).toBe(`${OTHER}/svg-xlink`);
  expect(await guide.getAttribute("target")).toBe("_blank");
  expect(await guide.evaluate((node) => node.hasAttributeNS("http://www.w3.org/1999/xlink", "href"))).toBe(false);
  const [tab] = await Promise.all([smoke.context.waitForEvent("page", { timeout: 10000 }), guide.click()]);
  await tab.waitForLoadState();
  expect(tab.url()).toBe(`${OTHER}/svg-xlink`);
  await tab.close();
  await Bun.sleep(300);
  await reportStays(frame);
  expect(otherHits).toEqual(["/svg-xlink"]);
  await smoke.page.screenshot({ path: evidencePath("report-frame-links.png") });
}, 30000);

test("an empty address moves the frame to the top of the report", async () => {
  const frame = await reportFrame();
  const top = report().getByRole("link", { name: "Top of the report" });
  expect(await top.getAttribute("href")).toBe("#");
  expect(await top.getAttribute("target")).toBe("_self");
  await frame.evaluate(() => window.scrollTo(0, 400));
  await top.click();
  const end = Date.now() + 10000;
  while (frame.url() !== "about:srcdoc#" && Date.now() < end) await Bun.sleep(50);
  expect(frame.url()).toBe("about:srcdoc#");
  await reportStays(frame);
}, 30000);

test("a relative address has no link and a click on it loads nothing", async () => {
  const frame = await reportFrame();
  const before = frame.url();
  pageRequests.length = 0;
  for (const name of ["Relative evidence", "Root evidence"]) {
    const text = report().getByText(name, { exact: true });
    expect(await text.evaluate((node) => node.hasAttribute("href"))).toBe(false);
    await text.click();
  }
  const evidence = report().locator("svg a").nth(1);
  expect(await evidence.evaluate((node) => node.hasAttribute("href") || node.hasAttributeNS("http://www.w3.org/1999/xlink", "href"))).toBe(false);
  await evidence.click();
  await Bun.sleep(500);
  expect(frame.url()).toBe(before);
  expect(pageRequests.filter((url) => url.includes("verification.html") || new URL(url).pathname === "/x")).toEqual([]);
  expect(smoke.context.pages()).toHaveLength(1);
  await reportStays(frame);
}, 30000);

test("a link to an absolute address names a new tab", async () => {
  const absolute = report().getByRole("link", { name: "Absolute guide" });
  expect(await absolute.getAttribute("target")).toBe("_blank");
  expect(await absolute.getAttribute("rel")).toBe("noopener noreferrer");
}, 30000);

test("a declarative shadow root and a nested form hide no link from the rules", async () => {
  const frame = await reportFrame();
  expect(await frame.evaluate(() => [...document.querySelectorAll("*")].some((node) => node.shadowRoot !== null))).toBe(false);
  expect(await report().getByRole("link", { name: "Shadow link" }).count()).toBe(0);
  const mutation = report().getByRole("link", { name: "Mutation link" });
  expect(await mutation.getAttribute("target")).toBe("_blank");
  expect(await mutation.getAttribute("rel")).toBe("noopener noreferrer");
  expect(otherHits).toEqual(["/svg-xlink"]);
}, 30000);

test("the dashboard page blocks a frame navigation to another address", async () => {
  // The frame gets a raw document without the link rules.
  await smoke.page.getByTestId("packet-frame").evaluate((node, address) => {
    (node as HTMLIFrameElement).srcdoc = `<a href="${address}/forced" target="_self" style="display:block;width:320px;height:48px">Forced link</a>`;
  }, OTHER);
  await report().getByRole("link", { name: "Forced link" }).click();
  await Bun.sleep(1000);
  const frame = await reportFrame();
  expect(frame.url()).not.toBe(`${OTHER}/forced`);
  expect(otherHits).toEqual(["/svg-xlink"]);
  expect(smoke.browserLog.some((line) => line.includes("frame-src 'self'"))).toBe(true);
}, 30000);
