import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

// The runtime image installs one of two manifest pairs by TARGETARCH.
// The pair must stay on one version set: the platform packages differ only
// by their architecture suffix, and the main packages must be byte-equal
// entries across the two files. A drift here ships two harnesses at once.
const here = import.meta.dir;

function deps(file: string): Record<string, string> {
  return JSON.parse(readFileSync(join(here, file), "utf8")).dependencies;
}

describe("the harness manifest pair", () => {
  const arm = deps("package.json");
  const x64 = deps("package.linux-x64.json");
  const suffix = (name: string, arch: string) => name.replace(arch, "ARCH");
  const normalize = (set: Record<string, string>, arch: string) =>
    Object.fromEntries(
      Object.entries(set)
        .filter(([name]) => !name.includes("-linux-"))
        .map(([name, version]) => [suffix(name, arch), version]),
    );

  test("the cross-platform packages agree", () => {
    expect(normalize(arm, "arm64")).toEqual(normalize(x64, "x64"));
  });

  test("each manifest carries exactly its own platform programs", () => {
    const armPlatforms = Object.keys(arm).filter((name) => name.includes("-linux-"));
    const x64Platforms = Object.keys(x64).filter((name) => name.includes("-linux-"));
    expect(armPlatforms).toEqual([
      "@anthropic-ai/claude-agent-sdk-linux-arm64",
      "@openai/codex-linux-arm64",
    ]);
    expect(x64Platforms).toEqual([
      "@anthropic-ai/claude-agent-sdk-linux-x64",
      "@openai/codex-linux-x64",
    ]);
    expect(arm["@openai/codex-linux-arm64"]).toBe("npm:@openai/codex@0.167.2-linux-arm64");
    expect(x64["@openai/codex-linux-x64"]).toBe("npm:@openai/codex@0.167.2-linux-x64");
  });

  test("both locks exist and name their pair manifest packages", () => {
    const armLock = readFileSync(join(here, "bun.lock"), "utf8");
    const x64Lock = readFileSync(join(here, "bun.linux-x64.lock"), "utf8");
    // The root workspace section is the pair's fingerprint; the package
    // sections below it legitimately carry the full optional matrix.
    const rootOf = (text: string) => text.slice(text.indexOf('"workspaces"'), text.indexOf('"packages"'));
    expect(rootOf(armLock)).toContain("@anthropic-ai/claude-agent-sdk-linux-arm64");
    expect(rootOf(armLock)).not.toContain("@anthropic-ai/claude-agent-sdk-linux-x64");
    expect(rootOf(x64Lock)).toContain("@anthropic-ai/claude-agent-sdk-linux-x64");
    expect(rootOf(x64Lock)).not.toContain("@anthropic-ai/claude-agent-sdk-linux-arm64");
    const versionOf = (text: string) => text.match(/"lockfileVersion":\s*(\d+)/)?.[1];
    expect(versionOf(armLock)).toBeDefined();
    expect(versionOf(x64Lock)).toBe(versionOf(armLock));
  });
});
