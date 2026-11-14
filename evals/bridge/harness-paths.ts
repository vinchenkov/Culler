import { createRequire } from "node:module";
import { dirname, join } from "node:path";

export async function nativeSdkExecutable(): Promise<string> {
  const require = createRequire(import.meta.url);
  const packageFile = join(dirname(require.resolve("@anthropic-ai/claude-agent-sdk")), "package.json");
  const manifest = JSON.parse(await Bun.file(packageFile).text());
  const names = Object.keys(manifest.optionalDependencies ?? {});
  for (const suffix of [`${process.platform}-${process.arch}`, `${process.platform}-${process.arch}-musl`]) {
    const name = names.find(candidate => candidate.endsWith(suffix));
    if (!name) continue;
    try {
      const platformManifest = require.resolve(`${name}/package.json`);
      const path = join(dirname(platformManifest), "claude");
      if (await Bun.file(path).exists()) return path;
    } catch {}
  }
  throw new Error("The image lacks the SDK's native harness executable.");
}
