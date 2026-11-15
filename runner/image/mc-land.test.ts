import { afterAll, describe, expect, test } from "bun:test";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const SCRIPT = join(import.meta.dir, "mc-land");
const roots: string[] = [];

afterAll(() => {
  for (const root of roots) rmSync(root, { recursive: true, force: true });
});

/** A fake `mc` on PATH that records its argv and exits with the given code. */
function fakeMc(exitCode: number) {
  const root = mkdtempSync(join(tmpdir(), "mc-land-test-"));
  roots.push(root);
  const record = join(root, "argv");
  const mc = join(root, "mc");
  writeFileSync(mc, `#!/bin/sh\nprintf '%s\\n' "$@" > "${record}"\nexit ${exitCode}\n`);
  chmodSync(mc, 0o755);
  return { root, record };
}

function run(argv: string[], root: string) {
  return Bun.spawnSync([SCRIPT, ...argv], {
    cwd: root,
    env: { ...process.env, PATH: `${root}:${process.env.PATH ?? ""}` },
    stdout: "pipe",
    stderr: "pipe",
  });
}

describe("mc-land", () => {
  test("prepare and apply run the repository operation of mc on the envelope", () => {
    for (const operation of ["prepare", "apply"]) {
      const f = fakeMc(0);
      const res = run([operation, "/mc/repository.json"], f.root);
      expect(res.exitCode).toBe(0);
      expect(readFileSync(f.record, "utf8")).toBe("__repository\n/mc/repository.json\n");
    }
  });

  test("the exit code of mc is the exit code of the script", () => {
    const f = fakeMc(3);
    expect(run(["apply", "/mc/repository.json"], f.root).exitCode).toBe(3);
  });

  test("another operation or argument count is a usage error that runs nothing", () => {
    for (const argv of [[], ["prepare"], ["land", "/mc/repository.json"], ["mc/task-1", "abc", "main"]]) {
      const f = fakeMc(0);
      const res = run(argv, f.root);
      expect(res.exitCode).toBe(2);
      expect(res.stderr.toString()).toMatch(/prepare\|apply|prepare or apply/);
      expect(() => readFileSync(f.record)).toThrow();
    }
  });
});
