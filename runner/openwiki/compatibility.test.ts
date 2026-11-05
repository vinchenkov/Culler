import { afterEach, beforeAll, expect, test } from "bun:test";
import { lstat, mkdtemp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { createIntegration, git, loadUpstream, wikiDigest, wikiFiles } from "./integration.mjs";
import { patchUpstream } from "./patch-upstream.mjs";

let upstream;
const roots: string[] = [];
beforeAll(async () => {
  const packageRoot = resolve(import.meta.dir, "node_modules/openwiki");
  await patchUpstream(packageRoot);
  upstream = await loadUpstream(packageRoot);
});
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });

async function put(root: string, path: string, text: string) {
  await mkdir(join(root, path, ".."), { recursive: true });
  await writeFile(join(root, path), text);
}
async function fixture() {
  const parent = await mkdtemp(join(tmpdir(), "mc-openwiki-"));
  roots.push(parent);
  const root = join(parent, "repo");
  await mkdir(root);
  await git(root, "init", "--quiet", "-b", "main");
  await git(root, "config", "user.email", "fixture@example.invalid");
  await git(root, "config", "user.name", "Fixture");
  await put(root, "source.txt", "The server returns a project document.\n");
  await put(root, "AGENTS.md", "The user controls these instructions.\n");
  await put(root, ".github/workflows/user.yml", "name: User workflow\n");
  await commit(root);
  const input = (await git(root, "rev-parse", "HEAD")).trim();
  const create = () => createIntegration({ root, inputCommit: input, upstream });
  return { root, input, create };
}
async function commit(root: string) {
  await git(root, "add", ".");
  await git(root, "commit", "--quiet", "-m", "Preserve the fixture");
}
function call(provider, name: string, input) { return provider.tools().find((tool) => tool.name === name).handle(input); }
const page = "/openwiki/quickstart.md";
const plan = [{ path: page, title: "Project", purpose: "Explain the verified server response.", instructions: ["Preserve complete reports."] }];
const report = "---\ntitle: Complete research report\ntype: Research\ndocument: true\ntask_id: 1\ntask_status: verified\ntags: [research, evidence]\n---\n\n# Complete research report\n\nThe accepted report keeps every section.\n\n## Evidence\n\nThe report cites [the source](../../source.txt).\n\n## Limitations\n\nThe experiment covers one server.\n";

async function finishPage(provider, root: string, runId: string) {
  const pending = await call(provider, "openwiki_next_page", { runId });
  const job = pending.job;
  await put(root, "openwiki/quickstart.md", "---\ntitle: Project\ntype: Guide\n---\n\n# Project\n\nThe server returns a project document.\n");
  const inspected = await call(provider, "openwiki_inspect_page_claims", { runId, jobId: job.id });
  expect(inspected.page).toBe(page);
  await call(provider, "openwiki_submit_page", { runId, jobId: job.id, claims: [{ statement: "The server returns a project document.", evidence: [{ resource: "repo://source.txt#L1" }] }] });
  return call(provider, "openwiki_finish", { runId });
}

test("the server supplies instructions for its six lifecycle tools", async () => {
  const { create } = await fixture();
  const server = upstream.createOpenWikiMcpServer(await create());
  const client = new Client({ name: "culler-test", version: "1.0.0" });
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  try {
    await server.connect(serverTransport);
    await client.connect(clientTransport);
    const names = (await client.listTools()).tools.map((tool) => tool.name);
    expect(names).toEqual([
      "openwiki_begin", "openwiki_submit_plan", "openwiki_next_page",
      "openwiki_inspect_page_claims", "openwiki_submit_page", "openwiki_finish",
    ]);
    const instructions = client.getInstructions() ?? "";
    expect(instructions).toContain("call openwiki_begin");
    expect(instructions).toContain("call openwiki_finish");
    for (const name of instructions.match(/openwiki_[a-z_]+/g) ?? []) expect(names).toContain(name);
  } finally {
    await client.close();
    await server.close();
  }
});

test("a separate page update preserves the earlier page source baseline", async () => {
  const { root, create } = await fixture();
  let provider = await create();
  let begin = await call(provider, "openwiki_begin", { root, mode: "init" });
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
  await finishPage(provider, root, begin.runId);
  await commit(root);
  const manifest = () => readFile(join(root, "openwiki/.page-manifest.json"), "utf8").then(JSON.parse);
  const previous = (await manifest()).pages[page];

  await put(root, "storage.txt", "The storage keeps one report.\n");
  await commit(root);
  const source = (await git(root, "rev-parse", "HEAD")).trim();
  provider = await createIntegration({ root, upstream });
  begin = await call(provider, "openwiki_begin", { root, mode: "update" });
  const storagePage = "/openwiki/storage.md";
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: [
    { path: storagePage, title: "Storage", purpose: "Explain report storage." },
  ] });
  const { job } = await call(provider, "openwiki_next_page", { runId: begin.runId });
  await put(root, "openwiki/storage.md", "---\ntitle: Storage\ntype: Guide\n---\n\nThe storage keeps one report.\n");
  await call(provider, "openwiki_submit_page", { runId: begin.runId, jobId: job.id, claims: [
    { statement: "The storage keeps one report.", evidence: [{ resource: "repo://storage.txt#L1" }] },
  ] });
  await call(provider, "openwiki_finish", { runId: begin.runId });
  const current = (await manifest()).pages;
  expect(current[page].gitHead).toBe(previous.gitHead);
  expect(current[page].sourceFingerprint).toBe(previous.sourceFingerprint);
  expect(current[page].completedRunId).toBe(previous.completedRunId);
  expect(current[storagePage].gitHead).toBe(source);
  expect(current[storagePage].completedRunId).toBe(begin.runId);
}, 30000);

test("the real host workflow preserves complete documents and user automation", async () => {
  const { root, create } = await fixture();
  await put(root, "openwiki/reports/research.md", report);
  await commit(root);
  const provider = await create();
  const begin = await call(provider, "openwiki_begin", { root, mode: "init" });
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
  const pending = await call(provider, "openwiki_next_page", { runId: begin.runId });
  expect(pending.job.purpose).toBe(plan[0].purpose);
  expect(pending.job.instructions).toEqual(plan[0].instructions);
  const result = await finishPage(provider, root, begin.runId);
  expect(result.status).toBe("complete");
  expect(await readFile(join(root, "openwiki/reports/research.md"), "utf8")).toBe(report);
  expect(await readFile(join(root, ".github/workflows/user.yml"), "utf8")).toBe("name: User workflow\n");
  expect(await Bun.file(join(root, ".github/workflows/openwiki-update.yml")).exists()).toBe(false);
  expect(await readFile(join(root, "AGENTS.md"), "utf8")).toBe("The user controls these instructions.\n");
  const receipt = JSON.parse(await readFile(provider.receiptPath, "utf8"));
  expect(receipt.wiki_digest).toBe(wikiDigest(await wikiFiles(root)));
  const claims = JSON.parse(await readFile(join(root, "openwiki/.claims/quickstart.json"), "utf8"));
  expect(claims.claims).toHaveLength(1);
  expect(await Bun.file(join(root, "openwiki/.claims/reports/research.json")).exists()).toBe(false);
  expect(await readFile(join(root, "openwiki/reports/index.md"), "utf8")).toContain("Complete research report");
}, 30000);

test("complete documents preserve Markdown examples and valid heading links", async () => {
  const { root, create } = await fixture();
  const content = report + "\nUse `[label](example.md)` for link syntax.\n\n~~~markdown\n[Example](another-example.md)\n~~~\n\n" +
    "[The second section](#evidence-1) uses the same title.\n\n## Evidence\n\n" +
    "[The named section](#review) retains its identifier.\n\n<a id=\"review\"></a>\n\n" +
    "[The final section](#source--report) uses a text entity.\n\nSource &amp; Report\n-------------------\n\n" +
    "[The source](/source.txt?raw=1) uses a repository root path.\n";
  await put(root, "openwiki/reports/research.md", content);
  await commit(root);
  const provider = await create();
  const begin = await call(provider, "openwiki_begin", { root, mode: "init" });
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
  await finishPage(provider, root, begin.runId);
  expect(await readFile(join(root, "openwiki/reports/research.md"), "utf8")).toBe(content);
}, 30000);

test("an invalid submission stays pending and a replacement process resumes the queue", async () => {
  const { root, create } = await fixture();
  let provider = await create();
  const begin = await call(provider, "openwiki_begin", { root, mode: "init" });
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
  const next = await call(provider, "openwiki_next_page", { runId: begin.runId });
  await expect(call(provider, "openwiki_submit_page", { runId: begin.runId, jobId: next.job.id })).rejects.toThrow();
  provider = await create();
  const resumed = await call(provider, "openwiki_begin", { root, mode: "init" });
  expect(resumed.resumed).toBe(true);
  expect((await call(provider, "openwiki_next_page", { runId: resumed.runId })).job.id).toBe(next.job.id);
  await finishPage(provider, root, resumed.runId);
}, 30000);

test("document changes and source changes block completion", async () => {
  const { root, create } = await fixture();
  await put(root, "openwiki/reports/research.md", report);
  await commit(root);
  const provider = await create();
  const begin = await call(provider, "openwiki_begin", { root, mode: "init" });
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
  await put(root, "openwiki/reports/research.md", report + "An unauthorized edit.\n");
  await expect(call(provider, "openwiki_next_page", { runId: begin.runId })).rejects.toThrow("Project documents changed");
  await put(root, "openwiki/reports/research.md", report);
  const next = await call(provider, "openwiki_next_page", { runId: begin.runId });
  await put(root, "source.txt", "The source changed.\n");
  await expect(call(provider, "openwiki_submit_page", { runId: begin.runId, jobId: next.job.id })).rejects.toThrow();
  expect(await Bun.file(provider.receiptPath).exists()).toBe(false);
}, 30000);

test("an incompatible wiki stays intact and broken document links fail", async () => {
  const { root, create } = await fixture();
  await put(root, "openwiki/existing.md", "A user document without OpenWiki metadata.\n");
  const provider = await create();
  await expect(call(provider, "openwiki_begin", { root, mode: "init" })).rejects.toThrow("existing wiki");
  await expect(call(provider, "openwiki_begin", { root, mode: "update" })).rejects.toThrow("compatible");
  expect(await readFile(join(root, "openwiki/existing.md"), "utf8")).toBe("A user document without OpenWiki metadata.\n");
  expect(await readFile(join(root, "AGENTS.md"), "utf8")).toBe("The user controls these instructions.\n");
  await rm(join(root, "openwiki/existing.md"));
  await put(root, "openwiki/reports/research.md", report.replace("../../source.txt", "missing.md"));
  await commit(root);
  await expect(call(provider, "openwiki_begin", { root, mode: "init" })).rejects.toThrow("link target");
}, 30000);

test("a retained wiki preserves reports during a later source update and a clean no-op", async () => {
  const { root, create } = await fixture();
  await put(root, "openwiki/reports/research.md", report);
  await commit(root);
  let provider = await create();
  let begin = await call(provider, "openwiki_begin", { root, mode: "init" });
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
  await finishPage(provider, root, begin.runId);
  await commit(root);
  provider = await createIntegration({ root, upstream });
  const unchanged = await call(provider, "openwiki_begin", { root, mode: "update" });
  expect(unchanged).toMatchObject({ status: "noop" });
  expect((await git(root, "status", "--porcelain")).trim()).toBe("");
  await put(root, "another-source.txt", "The later task adds a source file.\n");
  await commit(root);
  provider = await createIntegration({ root, upstream });
  begin = await call(provider, "openwiki_begin", { root, mode: "update" });
  expect(begin.status).toBe("active");
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: [] });
  await call(provider, "openwiki_finish", { runId: begin.runId });
  expect(await readFile(join(root, "openwiki/reports/research.md"), "utf8")).toBe(report);
}, 30000);

test("document-only accepted changes refresh navigation without Claim jobs", async () => {
  const { root, create } = await fixture();
  let provider = await create();
  const begin = await call(provider, "openwiki_begin", { root, mode: "init" });
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
  await finishPage(provider, root, begin.runId);
  await commit(root);
  await put(root, "openwiki/reports/research.md", report);
  await commit(root);
  provider = await createIntegration({ root, upstream });
  const update = await call(provider, "openwiki_begin", { root, mode: "update" });
  if (update.status === "active") {
    await call(provider, "openwiki_submit_plan", { runId: update.runId, pages: [] });
    await call(provider, "openwiki_finish", { runId: update.runId });
  }
  expect(await readFile(join(root, "openwiki/reports/index.md"), "utf8")).toContain("Complete research report");
  expect(await readFile(join(root, "openwiki/reports/research.md"), "utf8")).toBe(report);
}, 30000);


test("project documents at generated index paths remain intact before a run", async () => {
  for (const path of ["openwiki/index.md", "openwiki/reports/index.md"]) {
    const { root, create } = await fixture();
    const document = "---\ntitle: Complete accepted report\ntype: Research\ndocument: true\n---\n\n# Complete accepted report\n\n" +
      "The report preserves its evidence and limitations.\n".repeat(100);
    await put(root, path, document);
    await commit(root);
    const before = await wikiFiles(root);
    const provider = await create();
    for (const mode of ["init", "update"]) {
      await expect(call(provider, "openwiki_begin", { root, mode })).rejects.toThrow("reserved generated index path");
      expect(await wikiFiles(root)).toEqual(before);
      expect((await git(root, "status", "--porcelain")).trim()).toBe("");
      expect(await Bun.file(provider.receiptPath).exists()).toBe(false);
    }
  }
}, 30000);


test("the receipt remains readable across user identities under a restrictive umask", async () => {
  const { root, create } = await fixture();
  const provider = await create();
  const begin = await call(provider, "openwiki_begin", { root, mode: "init" });
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
  await writeFile(provider.receiptPath + ".tmp", "A stale temporary receipt.\n", { mode: 0o600 });
  const previous = process.umask(0o077);
  try { await finishPage(provider, root, begin.runId); }
  finally { process.umask(previous); }
  const receipt = await lstat(provider.receiptPath);
  expect(receipt.isFile()).toBe(true);
  expect(receipt.mode & 0o777).toBe(0o644);
  expect(JSON.parse(await readFile(provider.receiptPath, "utf8")).status).toBe("complete");
  expect(await readFile(provider.receiptPath + ".tmp", "utf8")).toBe("A stale temporary receipt.\n");
  expect((await readdir(dirname(provider.receiptPath))).filter((name) => name.endsWith(".tmp"))).toEqual(["openwiki-completion.json.tmp"]);
}, 30000);

for (const source of ["openwiki/reports/research.md", "docs/research/research.md"]) {
  test(`Claims cite the original report at ${source} and detect later evidence changes`, async () => {
    const { root, create } = await fixture();
    const original = report.replace("../../source.txt", "../../source.txt");
    await put(root, source, original);
    await commit(root);
    const provider = await create();
    const begin = await call(provider, "openwiki_begin", { root, mode: "init" });
    await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
    const { job } = await call(provider, "openwiki_next_page", { runId: begin.runId });
    const link = source.startsWith("openwiki/") ? source.slice(9) : `../${source}`;
    await put(root, "openwiki/quickstart.md", `---\ntitle: Project\ntype: Guide\n---\n\nThe experiment covers one server.\n\n[Complete report](${link})\n`);
    await call(provider, "openwiki_submit_page", { runId: begin.runId, jobId: job.id, claims: [
      { statement: "The experiment covers one server.", evidence: [{ resource: `repo://${source}` }] },
    ] });
    await call(provider, "openwiki_finish", { runId: begin.runId });
    expect(await readFile(join(root, source), "utf8")).toBe(original);
    const sidecar = JSON.parse(await readFile(join(root, "openwiki/.claims/quickstart.json"), "utf8"));
    expect(JSON.stringify(sidecar)).toContain(`repo://${source}`);
    await commit(root);
    await put(root, source, original.replace("one server", "two servers"));
    await commit(root);
    const update = await call(await createIntegration({ root, upstream }), "openwiki_begin", { root, mode: "update" });
    expect(update.status).toBe("active");
    expect(update.changedPaths).toContain(source);
    expect(JSON.stringify(update)).toContain("stale");
  }, 30000);
}

test("generated pages cannot establish evidence through a cycle", async () => {
  const { root, create } = await fixture();
  const provider = await create();
  const begin = await call(provider, "openwiki_begin", { root, mode: "init" });
  await call(provider, "openwiki_submit_plan", { runId: begin.runId, pages: plan });
  const { job } = await call(provider, "openwiki_next_page", { runId: begin.runId });
  await put(root, "openwiki/quickstart.md", "---\ntitle: Project\ntype: Guide\n---\nThe server returns a document.\n");
  await put(root, "openwiki/other.md", "---\ntitle: Other\ntype: Guide\n---\n[The source](quickstart.md).\n");
  await expect(call(provider, "openwiki_submit_page", { runId: begin.runId, jobId: job.id, claims: [
    { statement: "The server returns a document.", evidence: [{ resource: "repo://openwiki/other.md" }] },
  ] })).rejects.toThrow("original evidence");
  expect(await Bun.file(provider.receiptPath).exists()).toBe(false);
}, 30000);

test("original reports require committed content and durable evidence links", async () => {
  const { root, create } = await fixture();
  await put(root, "openwiki/reports/research.md", report);
  const provider = await create();
  await expect(call(provider, "openwiki_begin", { root, mode: "init" })).rejects.toThrow("Commit project documents");
  await put(root, ".mc-artifacts/1/capture.md", "Temporary evidence.\n");
  await put(root, "openwiki/reports/research.md", report + "\n[Evidence](../../.mc-artifacts/1/capture.md)\n");
  await commit(root);
  await expect(call(provider, "openwiki_begin", { root, mode: "init" })).rejects.toThrow("durable repository evidence");
}, 30000);
