import { recoveryPatches } from "./recovery-patches.mjs";
import { readFile, writeFile, copyFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";

const originalHashes = {
  "generation/page-manifest.js": "e375f4fda0da32b503f6e30f2b3ded38cff59aec6eb91fbc4d31aaa1ff0ec496",
  "claims/brains/code/preflight.js": "e7924cf4c7303759d754f849f2c0a020bb239ed88c42545867317f4c273892f1",
  "integrations/mcp/server.js": "0f57b7840465b1800c092f3d5c73b360c453055df7aeb67bcc73c717a01d9ffb",
  "agent/utils.js": "5f8a6af94959826d936468785d99ce6dbaeacd26d6ed097c1cebca43fb38db8d",
  "claims/evidence/repository/resolver.js": "c74185081e7d7e61cb50a6fa2e8aef48de3780622f283c0d1e7f0b379927133d",
  "claims/evidence/repository/resource.js": "0a143f8cec906fa5d36a7f3c625f5fbc0e99dc515f5586d45eda326d8e560a63",
  "generation/repository-run.js": "733b54287f13e36f2c29488e12db92a4655ecba24617e1c1c5fa2def4a5d1bd4",
  "claims/brains/code/store.js": "639274171a320ba6a8d7e088621583c20cdb2553e70643656652c2500ac4dafa",
  "okf/index-sync.js": "979afafdf360a4ca7eb9578adf71f1cfe044b8248119b3b8816cdcf0744d764a",
  "agent/wiki-link-validator.js": "0ce650934f06ae72731fabfb4fb4bc2123757ebf5039bdc6d66d62dba790e6bd",
  "mermaid/wiki.js": "1c640e2d4d3b7e5f8fed9afdc40370815e1d25ed3ff9e5fe32fad345b2026db2",
};
const patchMarker = "// culler-openwiki-patch\n";

export async function patchUpstream(root) {
  const manifest = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
  if (manifest.version !== "0.9.0") throw new Error("The OpenWiki package version requires compatibility tests.");
  const patches = [
    ["generation/page-manifest.js", []],
    ["claims/brains/code/preflight.js", []],
    ["integrations/mcp/server.js", [
      ["instructions: INSTRUCTIONS", "instructions: \"Read ~/.agents/skills/openwiki/SKILL.md before any wiki change.\""],
    ]],
    ["claims/evidence/repository/resource.js", [
      ['normalizedLower.startsWith(".git/") ||\n        normalizedLower === "openwiki" ||\n        normalizedLower.startsWith("openwiki/")', 'normalizedLower.startsWith(".git/") || normalizedLower === ".mc-artifacts" || normalizedLower.startsWith(".mc-artifacts/")'],
    ]],
    ["claims/evidence/repository/resolver.js", [
      ['import { createHash }', 'import { isProjectDocument } from "../../../culler.js";\nimport { createHash }'],
      ['source = await readFile(physicalPath, "utf8");', 'if (parsed.path.startsWith("openwiki/") && !isProjectDocument("/" + parsed.path)) {\n                throw new EvidenceResourceError("A generated Claim requires original evidence.");\n            }\n            source = await readFile(physicalPath, "utf8");'],
    ]],
    ["agent/utils.js", [
      ['import path from "node:path";', 'import path from "node:path";\nimport { isProjectDocument } from "../culler.js";'],
      ['!isOpenWikiPath(candidate)', '(!isOpenWikiPath(candidate) || isProjectDocument("/" + candidate))'],
    ]],
    ["generation/repository-run.js", [
      ['await ensureCodeModeRepoSetup(input.root, {\n        createWorkflow: input.mode === "init",\n    });', '// Culler owns repository setup.'],
      ['const replacement = input.mode === "init"\n        ? await beginRepositoryWikiReplacement(input.root)\n        : undefined;', 'const replacement = undefined;'],
      ['const sourceChangedBeforeFinish = await hasRepositorySourceChanged(run);', 'const sourceChangedBeforeFinish = await hasRepositorySourceChanged(run);\n    if (sourceChangedBeforeFinish) throw new RepositoryRunError("invalid_state", "The source changed. Begin a new plan.");'],
    ]],
    ["claims/brains/code/store.js", [
      ['import { createHash, randomUUID }', 'import { isProjectDocument } from "../../../culler.js";\nimport { createHash, randomUUID }'],
      ['.filter(isGroundedWikiPage)\n            .sort', '.filter(isGroundedWikiPage)\n            .filter((page) => !isProjectDocument(page))\n            .sort'],
    ]],
    ["okf/index-sync.js", [
      ['import path from "node:path";', 'import path from "node:path";\nimport { isProjectDocument } from "../culler.js";'],
      ['.sort((left, right) => left.localeCompare(right));', '.filter((page) => !isProjectDocument(page))\n        .sort((left, right) => left.localeCompare(right));'],
      ['const original = await readText(backend, filePath);', 'const original = await readText(backend, filePath);\n    if (isProjectDocument(filePath)) return original;'],
    ]],
    ["agent/wiki-link-validator.js", [
      ['import path from "node:path";', 'import path from "node:path";\nimport { isProjectDocument } from "../culler.js";'],
      ['for (const sourcePath of await collectMarkdownFiles(backend, wikiRoot)) {', 'for (const sourcePath of await collectMarkdownFiles(backend, wikiRoot)) {\n        if (isProjectDocument(sourcePath)) continue;'],
    ]],
    ["mermaid/wiki.js", [
      ['import path from "node:path";', 'import path from "node:path";\nimport { isProjectDocument } from "../culler.js";'],
      ['for (const filePath of await listMarkdownFiles(backend, root)) {', 'for (const filePath of await listMarkdownFiles(backend, root)) {\n        if (isProjectDocument(filePath)) continue;'],
    ]],
  ];
  for (const [relative, replacements] of Object.entries(recoveryPatches)) {
    patches.find(([name]) => name === relative)[1].push(...replacements);
  }
  const files = [];
  for (const [relative, replacements] of patches) {
    const path = join(root, "dist", relative);
    let text = await readFile(path, "utf8");
    if (text.startsWith(patchMarker)) {
      text = await readFile(`${path}.culler-original`, "utf8");
      let expected = text;
      for (const [before, after] of replacements) expected = expected.replaceAll(before, after);
      if (await readFile(path, "utf8") !== patchMarker + expected) throw new Error(`The OpenWiki package file requires review: ${relative}`);
    }
    if (createHash("sha256").update(text).digest("hex") !== originalHashes[relative]) {
      throw new Error(`The OpenWiki package file requires review: ${relative}`);
    }
    await writeFile(`${path}.culler-original`, text);
    for (const [before, after] of replacements) {
      if (!text.includes(before)) throw new Error(`The OpenWiki patch requires review: ${relative}`);
      text = text.replaceAll(before, after);
    }
    files.push({ path, text: `${patchMarker}${text}` });
  }
  for (const file of files) await writeFile(file.path, file.text);
  await copyFile(new URL("./project-documents.mjs", import.meta.url), join(root, "dist", "culler.js"));
  await copyFile(new URL("./recovery.mjs", import.meta.url), join(root, "dist", "culler-recovery.js"));
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  await patchUpstream(process.argv[2] ?? "/opt/openwiki/node_modules/openwiki");
}
