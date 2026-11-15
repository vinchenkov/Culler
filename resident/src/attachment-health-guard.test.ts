import { expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startAttachmentHealthGuard } from "./attachment-health-guard";

test("attachment health guard keeps its framed generation off argv until shutdown", async () => {
  const root = await mkdtemp(join(tmpdir(), "mc-health-guard-"));
  const script = join(root, "guard.js");
  await writeFile(script, `
    const chunks = [];
    let line = "";
    process.stdin.on("data", (chunk) => {
      line += chunk.toString("utf8");
      const newline = line.indexOf("\\n");
      if (newline < 0) return;
      const frame = JSON.parse(line.slice(0, newline));
      if (process.argv.includes(frame.generation)) process.exit(91);
      process.stdout.write(JSON.stringify({
        generation: frame.generation, state: "guarded", version: 1,
      }) + "\\n");
      process.stdin.removeAllListeners("data");
      process.stdin.resume();
    });
  `);
  const generation = "a".repeat(32);
  const handle = await startAttachmentHealthGuard([process.execPath, script], root, generation);
  await expect(handle.stop()).resolves.toBeUndefined();
  await expect(handle.stop()).resolves.toBeUndefined();
  await rm(root, { recursive: true });
});
