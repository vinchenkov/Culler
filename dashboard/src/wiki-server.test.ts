import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { makeLogger, type LogRecord } from "../../runner/shared/log";
import {
  WIKI_LIBRARIES,
  WIKI_LIBRARIES_ROOT,
  WIKI_POLICY,
  handleWikiRequest,
  parseProvenance,
  rewriteExportPage,
  startWikiServer,
  wikiHostAllowed,
  wikiKeyMatches,
  type WikiServerHandle,
} from "./wiki-server";

const KEY = "5e".repeat(32);

const FONT_PRECONNECT = '<link rel="preconnect" href="https://fonts.googleapis.com" />';
const FONT_STYLESHEET = '<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&display=swap" rel="stylesheet" />';

/** A synthetic page with the head structure of the OpenWiki static export. */
function exportPage(libraries = WIKI_LIBRARIES) {
  const scripts = libraries.map((library) =>
    `<script\n  src="${library.source}"\n  integrity="${library.integrity}"\n  crossorigin="anonymous"\n></script>`).join("\n");
  return `<!doctype html>\n<html lang="en" data-static-export="true">\n<head>\n` +
    `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'self' https://cdn.jsdelivr.net" />\n` +
    `<title>OpenWiki visualizer</title>\n${FONT_PRECONNECT}\n${FONT_STYLESHEET}\n${scripts}\n` +
    `<link rel="stylesheet" href="./styles.css" />\n</head>\n<body>\n<div id="graph"></div>\n` +
    `<script type="module" src="./client.js"></script>\n</body>\n</html>\n`;
}

describe("the provenance record", () => {
  test("each local library copy matches its integrity value and keeps its license", () => {
    expect(WIKI_LIBRARIES.map((library) => library.package)).toEqual(["force-graph", "marked", "dompurify", "mermaid"]);
    for (const library of WIKI_LIBRARIES) {
      const bytes = readFileSync(join(WIKI_LIBRARIES_ROOT, library.file));
      expect(`sha384-${new Bun.CryptoHasher("sha384").update(bytes).digest("base64")}`).toBe(library.integrity);
      expect(library.source.startsWith(`https://cdn.jsdelivr.net/npm/${library.package}@${library.version}/`)).toBe(true);
      expect(readFileSync(join(WIKI_LIBRARIES_ROOT, library.license_file), "utf8").length).toBeGreaterThan(0);
    }
  });

  test("the record names the OpenWiki version that the runner image pins", () => {
    const record = JSON.parse(readFileSync(join(WIKI_LIBRARIES_ROOT, "provenance.json"), "utf8"));
    const runner = join(import.meta.dir, "..", "..", "runner", "openwiki");
    const pin = JSON.parse(readFileSync(join(runner, "package.json"), "utf8")).dependencies.openwiki;
    const lock = JSON.parse(readFileSync(join(runner, "package-lock.json"), "utf8")).packages;
    expect(pin).toMatch(/^[0-9]+\.[0-9]+\.[0-9]+$/);
    expect(lock[""].dependencies.openwiki).toBe(pin);
    expect(lock["node_modules/openwiki"].version).toBe(pin);
    expect(record.openwiki_version).toBe(pin);
  });

  test("the record refuses unsafe, incomplete, and repeated entries", () => {
    const entry = { ...WIKI_LIBRARIES[0]! };
    const record = (libraries: unknown[]) => JSON.stringify({ libraries });
    expect(parseProvenance(record([entry]))).toEqual([entry]);
    expect(() => parseProvenance(record([]))).toThrow("must list the libraries");
    expect(() => parseProvenance(record([{ ...entry, file: "../app.js" }]))).toThrow("unsafe or repeated file");
    expect(() => parseProvenance(record([entry, entry]))).toThrow("unsafe or repeated file");
    expect(() => parseProvenance(record([{ ...entry, license_file: "../LICENSE" }]))).toThrow("unsafe license file");
    expect(() => parseProvenance(record([{ ...entry, integrity: "sha256-abc" }]))).toThrow("sha384 integrity");
    expect(() => parseProvenance(record([{ ...entry, version: "9.9.9" }]))).toThrow("its package and version");
    const { license: _license, ...unlicensed } = entry;
    expect(() => parseProvenance(record([unlicensed]))).toThrow('requires "license"');
  });
});

describe("the page rewrite", () => {
  test("points each matching library at its local copy and removes the Google Fonts links", () => {
    const page = exportPage();
    let expected = page.replace(`${FONT_PRECONNECT}`, "").replace(`${FONT_STYLESHEET}`, "");
    for (const library of WIKI_LIBRARIES) expected = expected.replace(`src="${library.source}"`, `src="/${KEY}/_openwiki/${library.file}"`);
    const served = rewriteExportPage(page, KEY);
    expect(served).toBe(expected);
    expect(served).not.toContain("https://cdn.jsdelivr.net/npm/");
    expect(served).not.toContain("fonts.googleapis.com/css2");
    for (const library of WIKI_LIBRARIES) expect(served).toContain(`integrity="${library.integrity}"`);
    expect(served.match(/crossorigin="anonymous"/g)?.length).toBe(WIKI_LIBRARIES.length);
    expect(served).toContain('<script type="module" src="./client.js"></script>');
  });

  test("keeps a reference whose version or integrity value differs from the record", () => {
    const [first, second] = WIKI_LIBRARIES;
    const page = exportPage([
      { ...first!, source: first!.source.replace(`@${first!.version}/`, "@0.0.1/") },
      { ...second!, integrity: "sha384-AAAA" },
    ]);
    const remote: string[] = [];
    expect(rewriteExportPage(page, KEY, WIKI_LIBRARIES, (reference) => remote.push(reference)))
      .toBe(page.replace(FONT_PRECONNECT, "").replace(FONT_STYLESHEET, ""));
    expect(remote).toEqual([first!.source.replace(`@${first!.version}/`, "@0.0.1/"), second!.source]);
  });

  test("reports each network reference that stays in the page, and no local or replaced reference", () => {
    const remote: string[] = [];
    rewriteExportPage(exportPage(), KEY, WIKI_LIBRARIES, (reference) => remote.push(reference));
    expect(remote).toEqual([]);
    const page = exportPage().replace("</head>",
      '<link rel="stylesheet" href="https://cdn.example/katex.css" />\n<script src="//cdn.example/katex.js"></script>\n</head>');
    rewriteExportPage(page, KEY, WIKI_LIBRARIES, (reference) => remote.push(reference));
    expect(remote).toEqual(["//cdn.example/katex.js", "https://cdn.example/katex.css"]);
  });
});

describe("the Host rule", () => {
  test("accepts only a loopback name with the bound port", () => {
    expect(wikiHostAllowed("127.0.0.1:7335", 7335)).toBe(true);
    expect(wikiHostAllowed("localhost:7335", 7335)).toBe(true);
    expect(wikiHostAllowed("LOCALHOST:7335", 7335)).toBe(true);
    expect(wikiHostAllowed("[::1]:7335", 7335)).toBe(true);
    expect(wikiHostAllowed("127.0.0.1:7333", 7335)).toBe(false);
    expect(wikiHostAllowed("127.0.0.1", 7335)).toBe(false);
    expect(wikiHostAllowed("attacker.example:7335", 7335)).toBe(false);
    expect(wikiHostAllowed("127.0.0.1.attacker.example:7335", 7335)).toBe(false);
    expect(wikiHostAllowed(null, 7335)).toBe(false);
  });
});

describe("the wiki key rule", () => {
  test("matches only the exact key", () => {
    expect(wikiKeyMatches(KEY, KEY)).toBe(true);
    expect(wikiKeyMatches("", KEY)).toBe(false);
    expect(wikiKeyMatches(KEY.slice(1), KEY)).toBe(false);
    expect(wikiKeyMatches(`${KEY}0`, KEY)).toBe(false);
    expect(wikiKeyMatches(KEY.toUpperCase(), KEY)).toBe(false);
    expect(wikiKeyMatches(`${KEY.slice(0, 63)}f`, KEY)).toBe(false);
  });

  test("the server refuses to start without a valid key", () => {
    for (const key of ["", "short", KEY.toUpperCase(), `${KEY}0`]) {
      expect(() => startWikiServer({ bind: "127.0.0.1:0", home: tmpdir(), key })).toThrow("the wiki key must be");
    }
  });
});

describe("the wiki server", () => {
  const first = "a".repeat(40);
  const second = "b".repeat(40);
  let home: string;
  let server: WikiServerHandle;
  let host: { host: string };

  function writeExport(project: string, commit: string, files: Record<string, string>, record: Record<string, unknown> = {}) {
    const root = join(home, "wiki", project);
    mkdirSync(join(root, commit), { recursive: true });
    for (const [name, body] of Object.entries(files)) writeFileSync(join(root, commit, name), body);
    writeFileSync(join(root, "export.json"), JSON.stringify({
      project, commit, tree: "c".repeat(40), directory: commit, exported_at: "2026-09-29T00:00:00Z", ...record,
    }));
  }

  /** A request below the wiki key. */
  const get = (path: string, init: RequestInit = {}) => raw(`/${KEY}${path}`, init);
  /** A request at the exact path, with no key added. */
  const raw = (path: string, init: RequestInit = {}) =>
    fetch(`${server.url}${path.replace(/^\//, "")}`, { redirect: "manual", ...init, headers: { ...host, ...(init.headers ?? {}) } });

  beforeAll(() => {
    home = mkdtempSync(join(tmpdir(), "culler-wiki-server-"));
    writeExport("alpha", first, {
      "index.html": exportPage(), "client.js": "export const ready = true;\n", "graph.json": '{"nodes":[]}\n', "styles.css": "body{}\n",
    });
    mkdirSync(join(home, "wiki", "alpha", ".stage-1"), { recursive: true });
    writeFileSync(join(home, "wiki", "alpha", ".stage-1", "index.html"), "staged");
    symlinkSync("/etc/hosts", join(home, "wiki", "alpha", first, "hosts.json"));
    server = startWikiServer({ bind: "127.0.0.1:0", home, key: KEY });
    host = { host: `127.0.0.1:${server.port}` };
  });

  afterAll(() => {
    server?.stop();
    rmSync(home, { recursive: true, force: true });
  });

  test("serves the rewritten page at the Project folder address below the key without a token", async () => {
    const redirect = await get("/alpha");
    expect(redirect.status).toBe(302);
    expect(redirect.headers.get("location")).toBe(`/${KEY}/alpha/`);
    const page = await get("/alpha/");
    expect(page.status).toBe(200);
    expect(page.headers.get("content-type")).toBe("text/html; charset=utf-8");
    const served = await page.text();
    expect(served).toBe(rewriteExportPage(exportPage(), KEY));
    for (const library of WIKI_LIBRARIES) expect(served).toContain(`src="/${KEY}/_openwiki/${library.file}"`);
    expect(await (await get("/alpha/index.html")).text()).toBe(rewriteExportPage(exportPage(), KEY));
  });

  test("answers 404 for each address without the exact key", async () => {
    const wrong = "6f".repeat(32);
    const paths = [
      "/alpha/", "/alpha", "/alpha/graph.json", "/_openwiki/marked.min.js",
      `/${wrong}/alpha/`, `/${wrong}/alpha/graph.json`, `/${wrong}/_openwiki/marked.min.js`,
      `/${KEY.slice(1)}/alpha/`, `/${KEY}0/alpha/`, `/${KEY.toUpperCase()}/alpha/`, `/x/${KEY}/alpha/`,
      `/${KEY}`, `/${KEY}/`, "/", `//alpha/`,
    ];
    for (const path of paths) {
      const response = await raw(path);
      expect({ path, status: response.status }).toEqual({ path, status: 404 });
      expect(await response.text()).not.toContain(KEY);
    }
  });

  test("serves the flat files of the current export and the library copies", async () => {
    const graph = await get("/alpha/graph.json");
    expect(graph.headers.get("content-type")).toBe("application/json; charset=utf-8");
    expect(await graph.text()).toBe('{"nodes":[]}\n');
    expect((await get("/alpha/client.js")).headers.get("content-type")).toBe("text/javascript; charset=utf-8");
    expect((await get("/alpha/styles.css")).headers.get("content-type")).toBe("text/css; charset=utf-8");
    for (const library of WIKI_LIBRARIES) {
      const response = await get(`/_openwiki/${library.file}`);
      expect(response.status).toBe(200);
      expect(response.headers.get("content-type")).toBe("text/javascript; charset=utf-8");
      expect(Buffer.from(await response.arrayBuffer()).equals(readFileSync(join(WIKI_LIBRARIES_ROOT, library.file)))).toBe(true);
    }
  });

  test("every response names the wiki service", async () => {
    writeExport("locked", first, { "index.html": "locked" });
    chmodSync(join(home, "wiki", "locked", first, "index.html"), 0o000);
    const responses: Array<[string, Response]> = [
      ["page", await get("/alpha/")],
      ["library", await get("/_openwiki/marked.min.js")],
      ["redirect", await get("/alpha")],
      ["missing", await get("/missing/")],
      ["no key", await raw("/alpha/")],
      ["method", await get("/alpha/", { method: "POST" })],
      ["host", await get("/alpha/", { headers: { host: "attacker.example:7335" } })],
      ["read", await get("/alpha/", { method: "HEAD" })],
      ["failure", await get("/locked/")],
    ];
    expect(responses.map(([name, response]) => [name, response.status]))
      .toEqual([["page", 200], ["library", 200], ["redirect", 302], ["missing", 404], ["no key", 404],
        ["method", 405], ["host", 403], ["read", 200], ["failure", 500]]);
    for (const [name, response] of responses) {
      expect([name, response.headers.get("x-culler-service")]).toEqual([name, "wiki"]);
    }
  });

  test("logs each network reference of a page once", async () => {
    const records: LogRecord[] = [];
    const log = makeLogger({ comp: "dashboard", level: "debug", sink: { write: (line) => { records.push(JSON.parse(line)); } } });
    const logged = startWikiServer({ bind: "127.0.0.1:0", home, key: KEY, log });
    try {
      const reference = "https://cdn.example/x\u2028.js";
      writeExport("remote", first, { "index.html": `<script src="${reference}"></script>` });
      for (let request = 0; request < 3; request += 1) {
        const response = await fetch(`${logged.url}${KEY}/remote/`, { headers: { host: `127.0.0.1:${logged.port}` } });
        expect(await response.text()).toBe(`<script src="${reference}"></script>`);
      }
      expect(records.filter((record) => record.event === "dash.wiki_library_remote").map((record) => [record.level, record.msg]))
        .toEqual([["warn", 'The wiki page of Project remote loads "https://cdn.example/x\\u2028.js" from the network. ' +
          "The OpenWiki provenance record names no local copy for it, so the page policy blocks the load."]]);
    } finally {
      logged.stop();
    }
  });

  test("every response carries the wiki policy headers", async () => {
    for (const path of ["/alpha/", "/alpha/graph.json", "/_openwiki/marked.min.js", "/missing/", "/alpha"]) {
      const response = await get(path);
      expect(response.headers.get("content-security-policy")).toBe(WIKI_POLICY);
      expect(response.headers.get("x-content-type-options")).toBe("nosniff");
      expect(response.headers.get("referrer-policy")).toBe("no-referrer");
      expect(response.headers.get("cache-control")).toBe("no-store");
    }
    const refused = await get("/alpha/", { method: "POST" });
    expect(refused.headers.get("content-security-policy")).toBe(WIKI_POLICY);
    expect((await raw("/alpha/")).headers.get("referrer-policy")).toBe("no-referrer");
    expect(WIKI_POLICY).toContain("frame-ancestors 'none'");
    expect(WIKI_POLICY).toContain("connect-src 'self'");
  });

  test("accepts only read methods", async () => {
    for (const method of ["POST", "PUT", "PATCH", "DELETE", "OPTIONS"]) {
      const response = await get("/alpha/", { method });
      expect(response.status).toBe(405);
      expect(response.headers.get("allow")).toBe("GET, HEAD");
    }
    const head = await get("/alpha/", { method: "HEAD" });
    expect(head.status).toBe(200);
    expect(await head.text()).toBe("");
  });

  test("refuses a Host header outside the loopback names of the bound port", async () => {
    expect((await raw("/alpha/", { headers: { host: "attacker.example:7335" } })).status).toBe(403);
    expect((await get("/alpha/", { headers: { host: "attacker.example:7335" } })).status).toBe(403);
    expect((await get("/alpha/", { headers: { host: `attacker.example:${server.port}` } })).status).toBe(403);
    expect((await get("/alpha/", { headers: { host: "127.0.0.1:1" } })).status).toBe(403);
    expect((await get("/alpha/", { headers: { host: `localhost:${server.port}` } })).status).toBe(200);
  });

  test("answers 404 for each other path", async () => {
    const paths = [
      "/", "/beta/", "/alpha/missing.js", "/alpha/hosts.json", "/alpha/.stage-1/index.html", "/alpha/export.json",
      `/alpha/${first}/index.html`, "/_openwiki/LICENSE-marked", "/_openwiki/provenance.json", "/_openwiki/app.js",
      "/_openwiki/", "/.stage-1/", "/alpha/%2e%2e/export.json", "/app.js",
    ];
    for (const path of paths) {
      const response = await handleWikiRequest(
        new Request(`http://127.0.0.1:${server.port}/${KEY}${path}`, { headers: host }), home, server.port, KEY);
      expect({ path, status: response.status }).toEqual({ path, status: 404 });
    }
  });

  test("refuses an export record that names another Project, an unsafe folder, or a link", async () => {
    writeExport("gamma", first, { "index.html": "gamma" }, { project: "alpha" });
    expect((await get("/gamma/")).status).toBe(404);
    writeExport("delta", first, { "index.html": "delta" }, { directory: "../alpha" });
    expect((await get("/delta/")).status).toBe(404);
    mkdirSync(join(home, "wiki", "epsilon"), { recursive: true });
    symlinkSync(join(home, "wiki", "alpha", first), join(home, "wiki", "epsilon", first));
    writeFileSync(join(home, "wiki", "epsilon", "export.json"), JSON.stringify({ project: "epsilon", commit: first, directory: first }));
    expect((await get("/epsilon/")).status).toBe(404);
    writeFileSync(join(home, "wiki", "zeta.json"), "{}");
    expect((await get("/zeta/")).status).toBe(404);
  });

  test("follows the export record to the current export at each request", async () => {
    writeExport("omega", first, { "index.html": "first export" });
    expect(await (await get("/omega/")).text()).toBe("first export");
    writeExport("omega", second, { "index.html": "second export" });
    expect(await (await get("/omega/")).text()).toBe("second export");
    rmSync(join(home, "wiki", "omega", "export.json"));
    expect((await get("/omega/")).status).toBe(404);
    expect(existsSync(join(home, "wiki", "omega", second))).toBe(true);
  });
});
