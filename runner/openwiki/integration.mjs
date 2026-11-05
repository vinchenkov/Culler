import { createHash, randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { lstat, mkdir, open, readFile, readdir, realpath, rename, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve, posix } from "node:path";
import { pathToFileURL } from "node:url";

const execute = promisify(execFile);
export const VERSION = "0.7.0";
export const REVISION = "sha512-Tw3AZE9eEmB9P8078lWCfjtrd7Xrl2rwBKfX5suie6sUH3gPLzEeGVIIugGtg1fnWfyJiVcqmw6WbXzujA1N7A==";
const STATE = "openwiki/.culler-run.json";
const LIFECYCLE_TOOLS = new Set([
  "openwiki_begin", "openwiki_submit_plan", "openwiki_next_page",
  "openwiki_inspect_page_claims", "openwiki_submit_page", "openwiki_finish",
]);
const regularCommit = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/;
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

export async function git(root, ...args) {
  const { stdout } = await execute("git", ["-C", root, "-c", `safe.directory=${root}`, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", ...args], {
    env: { PATH: process.env.PATH, HOME: process.env.HOME, GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null", GIT_NO_REPLACE_OBJECTS: "1", GIT_TERMINAL_PROMPT: "0" },
    maxBuffer: 64 << 20,
  });
  return stdout;
}

export async function wikiFiles(root) {
  const files = new Map();
  let bytes = 0;
  async function collect(relative) {
    let entries;
    try { entries = await readdir(join(root, relative), { withFileTypes: true }); }
    catch (error) { if (error.code === "ENOENT") return; throw error; }
    for (const entry of entries) {
      const name = `${relative}/${entry.name}`;
      if (/[\x00\r\n\\]/.test(name)) throw new Error("The wiki contains an invalid path.");
      if (entry.isSymbolicLink()) throw new Error(`The wiki contains a symbolic link: ${name}`);
      if (entry.isDirectory()) { await collect(name); continue; }
      if (!entry.isFile()) throw new Error(`The wiki requires regular files: ${name}`);
      const stat = await lstat(join(root, name));
      bytes += stat.size;
      if (bytes > 64 << 20) throw new Error("The wiki exceeds the 64 MiB limit.");
      files.set(name, await readFile(join(root, name)));
    }
  }
  const wiki = await lstat(join(root, "openwiki")).catch((error) => { if (error.code === "ENOENT") return null; throw error; });
  if (wiki && !wiki.isDirectory()) throw new Error("The wiki root requires a real directory.");
  await collect("openwiki");
  return files;
}

export function wikiDigest(files) {
  const hash = createHash("sha256");
  const names = [...files.keys()].sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)));
  for (const name of names) hash.update(`${name}\0${sha256(files.get(name))}\n`);
  return hash.digest("hex");
}

export async function loadUpstream(packageRoot = import.meta.url.startsWith("file:///opt/culler-runtime/")
  ? "/opt/culler-runtime/openwiki/node_modules/openwiki" : "/opt/openwiki/node_modules/openwiki") {
  const manifest = JSON.parse(await readFile(join(packageRoot, "package.json"), "utf8"));
  if (manifest.version !== VERSION) throw new Error("The OpenWiki package requires compatibility tests.");
  const load = (path) => import(pathToFileURL(join(packageRoot, "dist", path)).href);
  const [session, server, documents, frontmatter, indexes, backend, errors] = await Promise.all([
    load("integrations/core/session-manager.js"), load("integrations/mcp/server.js"),
    load("culler.js"), load("okf/frontmatter.js"), load("okf/index-sync.js"), load("agent/docs-only-backend.js"), load("integrations/core/errors.js"),
  ]);
  const transport = await import(pathToFileURL(join(packageRoot, "..", "@modelcontextprotocol/sdk/dist/esm/server/stdio.js")).href);
  const markdown = await import(pathToFileURL(join(packageRoot, "..", "marked/lib/marked.esm.js")).href);
  const entities = await import(pathToFileURL(join(packageRoot, "..", "entities/dist/esm/index.js")).href);
  return { ...session, ...server, ...documents, ...frontmatter, ...indexes, ...backend, ...transport, ...errors, marked: markdown.marked, decodeHTML: entities.decodeHTML };
}

function documentsIn(files, upstream) {
  const documents = {};
  for (const name of [...files.keys()].sort()) {
    const bytes = files.get(name);
    if (!name.endsWith(".md") || name.split("/").some((part) => part.startsWith("."))) continue;
    const fields = upstream.parseFrontmatterFields(bytes.toString("utf8"));
    if (fields?.document !== true) continue;
    if (posix.basename(name) === "index.md") throw new Error(`The project document uses a reserved generated index path: ${name}`);
    const validation = upstream.validateOkfFrontmatter(bytes.toString("utf8"));
    if (!validation.valid || typeof fields.title !== "string" || !fields.title.trim()) throw new Error(`The project document requires valid metadata and a title: ${name}`);
    if (files.has(`openwiki/.claims/${name.slice("openwiki/".length).replace(/\.md$/, ".json")}`)) {
      throw new Error(`A generated page must retain its Claim checks: ${name}`);
    }
    documents[name] = sha256(bytes);
  }
  return documents;
}

function markdownState(content, upstream) {
  const tokens = upstream.marked.lexer(content.replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/, ""));
  const links = [];
  const anchors = new Set();
  upstream.marked.walkTokens(tokens, (token) => {
    if (token.type !== "html") return;
    for (const match of token.text.matchAll(/\b(?:id|name)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/gi)) {
      anchors.add(upstream.decodeHTML(match[1] ?? match[2] ?? match[3]));
    }
  });
  upstream.marked.walkTokens(tokens, (token) => {
    if (token.type === "link" || token.type === "image") links.push(token.href);
    if (token.type === "heading") {
      const html = upstream.marked.parser([token]);
      const text = upstream.decodeHTML(html.replace(/<[^>]*>/g, "")).trim();
      const base = text.toLowerCase().replace(/[^\p{L}\p{M}\p{N}\s_-]/gu, "").replace(/\s/gu, "-");
      let id = base, suffix = 0;
      while (anchors.has(id)) id = `${base}-${++suffix}`;
      anchors.add(id);
    }
  });
  return { links, anchors };
}

async function validateLinks(root, files, documentNames, upstream) {
  for (const name of documentNames) {
    const { links } = markdownState(files.get(name).toString("utf8"), upstream);
    for (const href of links) {
      if (/^(?:file:|\/mc\/artifacts\/)/i.test(href)) throw new Error(`The document requires durable repository evidence: ${name}`);
      if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith("//")) continue;
      const separator = href.indexOf("#");
      const rawPath = (separator < 0 ? href : href.slice(0, separator)).split("?")[0];
      const anchor = separator < 0 ? "" : href.slice(separator + 1);
      if (!rawPath && !anchor) continue;
      let relative;
      try { relative = decodeURIComponent(rawPath); } catch { throw new Error(`The document link is invalid: ${name}`); }
      const target = relative ? posix.normalize(relative.startsWith("/") ? relative.slice(1) : posix.join(posix.dirname(name), relative)) : name;
      if (target === ".." || target.startsWith("../") || target.startsWith("/")) throw new Error(`The document link leaves the repository: ${name}`);
      if (target.split("/").includes(".mc-artifacts")) throw new Error(`The document requires durable repository evidence: ${name}`);
      const targetPath = join(root, target);
      const physical = await realpath(targetPath).catch(() => null);
      if (!physical || !physical.startsWith(root + "/")) throw new Error(`The document link target is absent: ${name}: ${href}`);
      if (anchor && target.endsWith(".md")) {
        const content = files.get(target)?.toString("utf8") ?? await readFile(targetPath, "utf8");
        const { anchors } = markdownState(content, upstream);
        if (!anchors.has(decodeURIComponent(anchor))) throw new Error(`The document link anchor is absent: ${name}: ${href}`);
      }
    }
  }
}

async function referencedDocuments(root, files, upstream, changed = []) {
  const pending = [...files.keys(), ...changed.filter((name) => name.endsWith(".md"))];
  const visited = new Set();
  const imported = new Map();
  while (pending.length) {
    const name = pending.shift();
    if (visited.has(name) || !name.endsWith(".md")) continue;
    visited.add(name);
    let content = files.get(name) ?? imported.get(name);
    if (!content) {
      const physical = await realpath(join(root, name)).catch(() => null);
      if (!physical) continue;
      if (physical !== join(root, name)) throw new Error(`The project document requires a regular repository path: ${name}`);
      content = await readFile(physical);
      imported.set(name, content);
      if ([...imported.values()].reduce((total, bytes) => total + bytes.length, 0) > 64 << 20) throw new Error("The project documents exceed the 64 MiB limit.");
    }
    for (const href of markdownState(content.toString("utf8"), upstream).links) {
      if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith("//")) continue;
      const relative = decodeURIComponent(href.split(/[?#]/)[0]);
      const target = posix.normalize(relative.startsWith("/") ? relative.slice(1) : posix.join(posix.dirname(name), relative));
      if (target.startsWith("../") || target.split("/").some((part) => part.startsWith("."))) continue;
      if (target.endsWith(".md") && !target.startsWith("openwiki/")) pending.push(target);
    }
  }
  return imported;
}

export async function createIntegration({ root, inputCommit, receiptPath, upstream }) {
  root = await realpath(root);
  const repositoryRoot = (await git(root, "rev-parse", "--show-toplevel")).trim();
  if (await realpath(repositoryRoot) !== root) throw new Error("The OpenWiki root must equal the assigned repository root.");
  inputCommit ??= (await git(root, "rev-parse", "HEAD")).trim();
  if (!regularCommit.test(inputCommit)) throw new Error("The assigned input commit is invalid.");
  receiptPath ??= join(dirname(root), "openwiki-completion.json");
  if (resolve(receiptPath).startsWith(root + "/")) throw new Error("The completion receipt must remain outside the repository.");
  const manager = upstream.HostSessionManager.create({ host: "culler", producerActor: "culler-scribe" });
  let state;
  let busy = false;
  async function saveState() {
    await mkdir(join(root, "openwiki"), { recursive: true });
    await writeFile(join(root, STATE), JSON.stringify(state) + "\n");
  }
  async function checkDocuments() {
    const files = await wikiFiles(root);
    const documents = documentsIn(files, upstream);
    if (JSON.stringify(documents) !== JSON.stringify(state.documents)) throw new Error("Project documents changed during generated page work. Begin a new plan.");
    const imported = await referencedDocuments(root, files, upstream, Object.keys(state.imported ?? {}));
    for (const [name, digest] of Object.entries(state.imported ?? {})) {
      if (!imported.has(name) || sha256(imported.get(name)) !== digest) throw new Error("Project documents changed during generated page work. Begin a new plan.");
    }
    await validateLinks(root, new Map([...files, ...imported]), [...Object.keys(documents), ...imported.keys()], upstream);
    return files;
  }
  async function complete(status) {
    await checkDocuments();
    const files = await wikiFiles(root);
    if (files.has("openwiki/.run.json")) throw new Error("The OpenWiki page queue remains incomplete.");
    const metadata = JSON.parse(files.get("openwiki/.last-update.json")?.toString("utf8") ?? "null");
    if (metadata?.status !== "complete") throw new Error("The OpenWiki update remains incomplete.");
    const backend = new upstream.OpenWikiLocalShellBackend({ rootDir: root, virtualMode: true, docsOnly: true, outputMode: "repository" });
    await upstream.synchronizeWikiIndexes(backend, "repository");
    await checkDocuments();
    await rm(join(root, STATE), { force: true });
    const receipt = { format: 1, status, input_commit: inputCommit, source_commit: state.source_commit,
      wiki_digest: wikiDigest(await wikiFiles(root)), openwiki_version: VERSION, openwiki_revision: REVISION };
    await mkdir(dirname(receiptPath), { recursive: true });
    const temporary = `${receiptPath}.${randomUUID()}.tmp`;
    const file = await open(temporary, "wx", 0o644);
    try {
      try {
        await file.writeFile(JSON.stringify(receipt) + "\n");
        // The terminal uses another user identity. The receipt contains no secrets.
        await file.chmod(0o644);
        await file.sync();
      } finally { await file.close(); }
      await rename(temporary, receiptPath);
    } finally { await rm(temporary, { force: true }); }
    return receipt;
  }
  const tools = manager.tools().filter((tool) => LIFECYCLE_TOOLS.has(tool.name)).map((tool) => ({ ...tool, handle: async (input) => {
    if (busy) throw new Error("An OpenWiki operation is active.");
    busy = true;
    try {
      if (tool.name === "openwiki_begin") {
        if (await realpath(input.root) !== root) throw new Error("OpenWiki must use the assigned repository.");
        await rm(receiptPath, { force: true });
        const files = await wikiFiles(root);
        const documents = documentsIn(files, upstream);
        const existingGenerated = [...files.keys()].filter((name) => name.endsWith(".md") && name !== "openwiki/INSTRUCTIONS.md" && !documents[name]);
        if (input.mode === "init" && existingGenerated.length && !files.has("openwiki/.run.json")) throw new Error("The existing wiki requires an update. Its replacement requires an explicit document task.");
        if (input.mode === "update" && existingGenerated.length && !files.has("openwiki/.last-update.json") && !files.has("openwiki/.run.json")) throw new Error("The existing wiki lacks compatible OpenWiki metadata.");
        const source = (await git(root, "rev-parse", "HEAD")).trim();
        await git(root, "merge-base", "--is-ancestor", inputCommit, source);
        const changed = (await git(root, "diff", "--name-only", "-z", "HEAD", "--")).split("\0").filter(Boolean);
        const untracked = (await git(root, "ls-files", "--others", "--exclude-standard", "-z")).split("\0").filter(Boolean);
        if ([...changed, ...untracked].some((name) => documents[name] || (!name.startsWith("openwiki/")))) {
          throw new Error("Commit project documents and source files before generated page work.");
        }
        const repaired = (await git(root, "diff", "--name-only", "-z", inputCommit, source, "--")).split("\0").filter(Boolean);
        const imported = await referencedDocuments(root, files, upstream, repaired);
        await validateLinks(root, new Map([...files, ...imported]), [...Object.keys(documents), ...imported.keys()], upstream);
        upstream.setProjectDocuments(Object.keys(documents).map((name) => "/" + name));
        const saved = files.get(STATE);
        state = saved && files.has("openwiki/.run.json") ? JSON.parse(saved.toString("utf8")) : { format: 1, input_commit: inputCommit, source_commit: source, documents, imported: Object.fromEntries([...imported].map(([name, bytes]) => [name, sha256(bytes)])) };
        if (state.input_commit !== inputCommit && !regularCommit.test(state.input_commit)) throw new Error("The retained OpenWiki state is invalid.");
        state.input_commit = inputCommit;
        if (state.source_commit !== source) { state.source_commit = source; state.documents = documents; state.imported = Object.fromEntries([...imported].map(([name, bytes]) => [name, sha256(bytes)])); }
        await checkDocuments();
        const result = await tool.handle(input);
        if (result.status === "noop") {
          for (const name of ["openwiki/.last-update.json", "openwiki/.page-manifest.json"]) {
            if (files.has(name)) await writeFile(join(root, name), files.get(name));
          }
          result.completion = await complete("noop");
        } else await saveState();
        return result;
      }
      if (!state) throw new Error("Begin the OpenWiki run before page work.");
      await checkDocuments();
      if (tool.name === "openwiki_submit_plan") {
        for (const name of [...(input.pages ?? []).map((page) => page.path), ...(input.deletePages ?? [])]) {
          const page = posix.normalize(name.startsWith("/openwiki/") ? name.slice(1) : name.startsWith("openwiki/") ? name : `openwiki/${name}`);
          if (state.documents[page]) throw new Error("A generated page plan cannot revise a project document.");
        }
      }
      const result = await tool.handle(input);
      await checkDocuments();
      if (tool.name === "openwiki_finish") {
        if (result.sourceChanged) throw new Error("The source changed. Begin a new plan.");
        result.completion = await complete("complete");
      }
      return result;
    } catch (error) {
      if (error instanceof upstream.HostIntegrationError) throw error;
      throw new upstream.HostIntegrationError("invalid_state", error instanceof Error ? error.message : "The OpenWiki operation failed.");
    } finally { busy = false; }
  } }));
  return { tools: () => tools, receiptPath };
}
