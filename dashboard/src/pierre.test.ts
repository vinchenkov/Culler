// pierre.test.ts — the local Pierre modules match their provenance record, the
// record matches the pinned build inputs, and every module import is local.

import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { pierreFiles } from "./server";

const directory = join(import.meta.dir, "ui", "pierre");
const inputs = join(import.meta.dir, "..", "pierre");
const provenance = JSON.parse(readFileSync(join(directory, "provenance.json"), "utf8")) as {
  entry: string;
  packages: { name: string; version: string; license: string; license_file: string }[];
  files: { file: string; sha256: string }[];
};

/** The package versions that the lockfile of the build inputs pins. */
function lockedVersions(): Set<string> {
  const text = readFileSync(join(inputs, "bun.lock"), "utf8").replace(/,(\s*[}\]])/g, "$1");
  const lock = JSON.parse(text) as { packages: Record<string, [string, ...unknown[]]> };
  return new Set(Object.values(lock.packages).map(([id]) => id));
}

describe("local Pierre modules", () => {
  test("each module holds the bytes that the provenance record states", () => {
    expect(provenance.files.map((f) => f.file)).toContain(provenance.entry);
    for (const { file, sha256 } of provenance.files) {
      const bytes = readFileSync(join(directory, file));
      expect(new Bun.CryptoHasher("sha256").update(bytes).digest("hex")).toBe(sha256);
    }
  });

  test("the directory holds only recorded modules and license notices", () => {
    const recorded = new Set([
      "provenance.json",
      ...provenance.files.map((f) => f.file),
      ...provenance.packages.map((p) => p.license_file),
    ]);
    expect(readdirSync(directory).filter((file) => !recorded.has(file))).toEqual([]);
  });

  test("each bundled package has its pinned version and its license notice", () => {
    const locked = lockedVersions();
    for (const { name, version, license_file } of provenance.packages) {
      expect(locked.has(`${name}@${version}`)).toBe(true);
      const notice = readFileSync(join(directory, license_file), "utf8");
      expect(notice.length).toBeGreaterThan(200);
    }
    for (const name of ["@pierre/diffs", "@pierre/trees"]) {
      expect(provenance.packages.some((p) => p.name === name)).toBe(true);
    }
  });

  test("the build inputs pin each direct dependency to one exact version", () => {
    const manifest = JSON.parse(readFileSync(join(inputs, "package.json"), "utf8")) as Record<string, Record<string, string>>;
    const pins = { ...manifest["dependencies"], ...manifest["devDependencies"] };
    expect(Object.keys(pins).length).toBeGreaterThan(0);
    const locked = lockedVersions();
    for (const [name, version] of Object.entries(pins)) {
      expect(version).toMatch(/^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$/);
      expect(locked.has(`${name}@${version}`)).toBe(true);
    }
  });

  test("each module imports only other local modules", () => {
    const transpiler = new Bun.Transpiler({ loader: "js" });
    const local = new Set(provenance.files.map((f) => `./${f.file}`));
    let imports = 0;
    for (const { file } of provenance.files) {
      const source = readFileSync(join(directory, file), "utf8");
      for (const { path } of transpiler.scanImports(source)) {
        imports++;
        expect(local.has(path)).toBe(true);
      }
      expect(source).not.toContain("importScripts(");
      expect(source).not.toMatch(/new Worker\(/);
    }
    expect(imports).toBeGreaterThan(provenance.files.length);
  });

  test("the server serves each module and each license notice", () => {
    const served = pierreFiles(readFileSync(join(directory, "provenance.json"), "utf8"));
    for (const { file } of provenance.files) {
      expect(served[`/pierre/${file}`]).toEqual({ path: `ui/pierre/${file}`, type: "text/javascript; charset=utf-8" });
    }
    for (const { license_file } of provenance.packages) {
      expect(served[`/pierre/${license_file}`]?.type).toBe("text/plain; charset=utf-8");
    }
    expect(() => pierreFiles(JSON.stringify({ files: [{ file: "../app.js" }], packages: [] }))).toThrow();
  });
});
