import { expect, test } from "bun:test";
import { verifyProjectBuildFiles } from "./project-files";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

test("a source change to an approved build file requires a new image validation", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-build-files-"));
  const files = [{ path: "Dockerfile", content: "FROM culler-project-base:exact\nRUN true\n" }];
  try {
    await writeFile(join(root, "Dockerfile"), files[0]!.content);
    await verifyProjectBuildFiles(root, files);
    await writeFile(join(root, "Dockerfile"), files[0]!.content + "RUN install-new-tool\n");
    await expect(verifyProjectBuildFiles(root, files)).rejects.toThrow("differs from the checked image");
  } finally { await rm(root, { recursive: true, force: true }); }
});
