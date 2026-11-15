import { afterEach, expect, test } from "bun:test";
import { cp, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { patchUpstream } from "./patch-upstream.mjs";

const roots: string[] = [];
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });

async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "mc-openwiki-patch-"));
  roots.push(root);
  await cp(join(import.meta.dir, "node_modules/openwiki/dist"), join(root, "dist"), { recursive: true });
  await cp(join(import.meta.dir, "node_modules/openwiki/package.json"), join(root, "package.json"));
  return root;
}

test("the patch accepts only the fixed package bytes and remains repeatable", async () => {
  const root = await fixture();
  await patchUpstream(root);
  const file = join(root, "dist/generation/repository-run.js");
  const patched = await readFile(file, "utf8");
  await patchUpstream(root);
  expect(await readFile(file, "utf8")).toBe(patched);
  const altered = `${patched}\n// The package changed.\n`;
  await writeFile(file, altered);
  await expect(patchUpstream(root)).rejects.toThrow("The OpenWiki package file requires review");
  expect(await readFile(file, "utf8")).toBe(altered);
});

test("a different package version requires compatibility review", async () => {
  const root = await fixture();
  const file = join(root, "dist/generation/repository-run.js");
  const original = await readFile(file, "utf8");
  await writeFile(join(root, "package.json"), JSON.stringify({ version: "0.0.0" }));
  await expect(patchUpstream(root)).rejects.toThrow("The OpenWiki package version requires compatibility tests");
  expect(await readFile(file, "utf8")).toBe(original);
});
