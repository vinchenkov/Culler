import { expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

test("the worker preserves both clocks for a result and a runner failure", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-worker-clock-"));
  try {
    const module = join(root, "runner.ts");
    await writeFile(module, `export function makeMcRunner() {
      return { async run(argv) {
        if (argv[0] === "project") throw new Error("The fixture rejects this read.");
        return { code: 0, json: { tasks: [] }, stderr: "" };
      } };
    }`);
    const input = JSON.stringify({ id: "clock-check", concurrency: 2, requests: [
      { id: "success", argv: ["task", "list"] },
      { id: "failure", argv: ["project", "list"] },
    ] }) + "\n";
    const process = Bun.spawn(["mise", "exec", "--", "bun", join(import.meta.dir, "worker.ts"),
      "--mc-bin", "/usr/bin/false", "--runner-module", module, "--max-concurrency", "2"], {
      stdin: new TextEncoder().encode(input), stdout: "pipe", stderr: "pipe",
    });
    const [output, errors, code] = await Promise.all([
      new Response(process.stdout).text(), new Response(process.stderr).text(), process.exited,
    ]);
    expect(errors).toBe("");
    expect(code).toBe(0);
    const rows = output.trim().split("\n").map((line) => JSON.parse(line));
    const results = rows.filter((row) => row.type === "result");
    expect(results).toHaveLength(2);
    expect(results.find((row) => row.id === "success").valid).toBe(true);
    expect(results.find((row) => row.id === "failure").valid).toBe(false);
    for (const row of results) {
      expect(Number.isFinite(Date.parse(row.completedAt))).toBe(true);
      expect(row.wallElapsedMs).toBe(Date.parse(row.completedAt) - Date.parse(row.startedAt));
      expect(row.elapsedMs).toBeGreaterThanOrEqual(0);
    }
    expect(rows.at(-1)).toEqual({ type: "batch-complete", id: "clock-check", count: 2 });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
