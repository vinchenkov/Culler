import { expect, test } from "bun:test";
import { mkdtemp, mkdir, rm, symlink, utimes, writeFile, readFile, lstat, readlink, readdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import { createHash } from "node:crypto";
import { clearProjectCache, limitProjectCache, projectCacheUsage, projectHomeUsage, importLegacyNpmCache } from "./project-cache";

async function archive(root: string, content: string) {
  const digest = createHash("sha512").update(content).digest("hex");
  const directory = join(root, "npm/_cacache/content-v2/sha512", digest.slice(0, 2), digest.slice(2, 4));
  await mkdir(directory, { recursive: true });
  const path = join(directory, digest.slice(4));
  await writeFile(path, content);
  return path;
}

test("cache eviction removes old files and preserves home files and external targets", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-cache-test-"));
  try {
    const cache = join(root, "cache"), home = join(root, "home");
    await mkdir(cache); await mkdir(home);
    await writeFile(join(home, "tool"), "#!/bin/sh\nexit 0\n", { mode: 0o755 });
    await writeFile(join(home, "configuration"), "configured");
    await symlink(join(home, "tool"), join(cache, "link"));
    await writeFile(join(cache, "old"), "12345");
    await utimes(join(cache, "old"), new Date(0), new Date(0));
    await writeFile(join(cache, "new"), "67890");
    expect((await projectCacheUsage(cache)).bytes).toBe(10);
    expect((await projectHomeUsage(home)).bytes).toBeGreaterThan(10);
    expect(await limitProjectCache(cache, 5)).toBe(5);
    expect(await readFile(join(cache, "new"), "utf8")).toBe("67890");
    expect(await limitProjectCache(cache, 0)).toBe(0);
    expect(await readFile(join(home, "configuration"), "utf8")).toBe("configured");
    expect(Bun.spawnSync([join(home, "tool")]).exitCode).toBe(0);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("cache inspection preserves arbitrary files, executables, metadata, and symbolic links", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-cache-formats-"));
  try {
    const home = join(root, "home"), cache = join(root, "cache");
    await mkdir(home); await mkdir(cache);
    for (const path of ["npm/_npx/bin/tool", "npm/_cacache/index-v5/index", "uv/archive-v0/package", "unknown/dependency"]) {
      await mkdir(join(cache, path, ".."), { recursive: true });
      await writeFile(join(cache, path), path);
    }
    const executable = join(cache, "unknown/run");
    await writeFile(executable, "#!/bin/sh\nprintf 'retained'\n", { mode: 0o755 });
    await symlink("run", join(cache, "unknown/link"));
    const before = await projectCacheUsage(cache);
    await importLegacyNpmCache(cache, home);
    expect(await limitProjectCache(cache, before.bytes)).toBe(before.bytes);
    expect(await projectCacheUsage(cache)).toEqual(before);
    expect(await readlink(join(cache, "unknown/link"))).toBe("run");
    const result = Bun.spawnSync([join(cache, "unknown/link")]);
    expect(result.exitCode).toBe(0);
    expect(result.stdout.toString()).toBe("retained");
    expect(await readdir(home)).toEqual([]);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("cache inspection refuses a symbolic link root and preserves its target", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-cache-links-"));
  try {
    await mkdir(join(root, "outside"));
    await writeFile(join(root, "outside/keep"), "private");
    await symlink(join(root, "outside"), join(root, "linked-root"));
    await expect(projectCacheUsage(join(root, "linked-root"))).rejects.toThrow("must be a directory");
    expect(await readFile(join(root, "outside/keep"), "utf8")).toBe("private");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("concurrent imports preserve legacy npm archives after disposable cache removal", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-legacy-npm-"));
  try {
    const cache = join(root, "cache"), home = join(root, "home");
    await mkdir(home);
    const source = await archive(cache, "retained archive");
    const destination = join(home, ".npm", relative(join(cache, "npm"), source));
    await Promise.all(Array.from({ length: 8 }, () => importLegacyNpmCache(cache, home)));
    expect(await readFile(destination, "utf8")).toBe("retained archive");
    expect((await readdir(join(destination, ".."))).length).toBe(1);
    const before = await lstat(destination);
    await writeFile(source, "new cache content");
    await importLegacyNpmCache(cache, home);
    expect((await lstat(destination)).ino).toBe(before.ino);
    expect(await readFile(destination, "utf8")).toBe("retained archive");
    expect(await limitProjectCache(cache, 0)).toBe(0);
    expect(await readFile(destination, "utf8")).toBe("retained archive");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("legacy imports preserve existing home cache files and links", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-existing-npm-"));
  try {
    const cache = join(root, "cache"), home = join(root, "home");
    await mkdir(home);
    const source = await archive(cache, "legacy archive");
    const destination = join(home, ".npm", relative(join(cache, "npm"), source));
    await mkdir(join(destination, ".."), { recursive: true });
    await writeFile(join(home, "retained"), "existing content");
    await symlink(join(home, "retained"), destination);
    await mkdir(join(home, ".npm/_cacache/index-v5"));
    await writeFile(join(home, ".npm/_cacache/index-v5/index"), "existing metadata");
    await importLegacyNpmCache(cache, home);
    expect(await readlink(destination)).toBe(join(home, "retained"));
    expect(await readFile(destination, "utf8")).toBe("existing content");
    expect(await readFile(join(home, ".npm/_cacache/index-v5/index"), "utf8")).toBe("existing metadata");
    expect(await readFile(source, "utf8")).toBe("legacy archive");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("manual cache removal deletes links and lock files but preserves their home targets", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-cache-clear-"));
  try {
    const cache = join(root, "cache"), home = join(root, "home");
    await mkdir(cache); await mkdir(home);
    await writeFile(join(home, "configuration"), "retained");
    await symlink(home, join(cache, "home-link"));
    await mkdir(join(cache, "nested"));
    await writeFile(join(cache, "nested/package.lock"), "cache lock");
    await writeFile(join(cache, "nested/package"), "cache package");
    expect(await clearProjectCache(cache)).toBe(0);
    expect(await readdir(cache)).toEqual([]);
    expect(await readFile(join(home, "configuration"), "utf8")).toBe("retained");
    expect(await clearProjectCache(cache)).toBe(0);
    await symlink(home, join(root, "linked-root"));
    await expect(clearProjectCache(join(root, "linked-root"))).rejects.toThrow("must be a directory");
    expect(await readFile(join(home, "configuration"), "utf8")).toBe("retained");
  } finally { await rm(root, { recursive: true, force: true }); }
});
