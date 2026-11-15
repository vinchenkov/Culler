import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { collectProjectEvidence, collectProjectImageInventory, type EvidenceDependencies } from "./project-evidence";
import type { ProjectEnvironment } from "./project-environment";

const configuration = (declarations: string[]): ProjectEnvironment => ({
  base_digest: `sha256:${"a".repeat(64)}`, build_files: [], build_args: {},
  setup: { directory: ".", command: "" }, services: [], variables: [],
  secrets: [{ name: "REGISTRY_TOKEN", reference: "registry", scope: "agent" }], declarations,
});

test("image inventory observes replacement tools and added system packages", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-image-inventory-"));
  const status = join(root, "status");
  try {
    await writeFile(status, "Package: libpq-dev\nStatus: install ok installed\nVersion: 16.13-0ubuntu1\nArchitecture: arm64\n\n" +
      "Package: removed-tool\nStatus: deinstall ok config-files\nVersion: 1.0\nArchitecture: arm64\n");
    const inventory = await collectProjectImageInventory(async (command, directory, env, timeout) => {
      expect(directory).toBe("/workspace");
      expect(timeout).toBe(8000);
      expect(env.GOPROXY).toBe("off");
      return command === "'node' '--version'" ? { exitCode: 0, output: "v22.20.0\n" }
        : { exitCode: 127, output: "unavailable" };
    }, status);
    expect(inventory.tools).toEqual({ node: "v22.20.0" });
    expect(inventory.packages).toEqual([{ name: "libpq-dev", version: "16.13-0ubuntu1", architecture: "arm64" }]);
    expect(inventory.unverified).toContain("bun");
    expect(inventory.unverified).not.toContain("system packages");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("run evidence records selected tools and lockfile identities for each declared package root", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-evidence-"));
  try {
    await mkdir(join(root, "web"));
    await mkdir(join(root, "api", ".venv", "bin"), { recursive: true });
    const lock = JSON.stringify({ packages: { "": { name: "web" }, "node_modules/zod": { version: "4.1.0", resolved: "https://secret-token@registry.invalid/zod" } } });
    await writeFile(join(root, "web", "package-lock.json"), lock);
    await writeFile(join(root, "web", ".mise.toml"), '[tools]\nnode = "22.18.0"\n');
    await writeFile(join(root, "api", "uv.lock"), 'version = 1\n[[package]]\nname = "httpx"\nversion = "0.28.1"\n');
    await writeFile(join(root, "api", ".venv", "bin", "python"), "fixture");
    const calls: Array<{ argv: string[]; directory: string; env: Record<string, string | undefined> }> = [];
    const deps: EvidenceDependencies = { async execute(argv, directory, env) {
      calls.push({ argv, directory, env });
      if (argv[0] === "mise" && argv[1] === "env") return { exit_code: 0, stdout: JSON.stringify({ PATH: `${directory}/selected-tools`, REGISTRY_TOKEN: "secret-token" }) };
      if (argv[0] === "node") return { exit_code: 0, stdout: directory.endsWith("/web") ? "v22.18.0\n" : "v24.3.0\n" };
      if (argv[0].endsWith("/.venv/bin/python")) return { exit_code: 0, stdout: "Python 3.14.0\n" };
      if (argv[0] === "go") return { exit_code: 1, stdout: "secret-token" };
      return { exit_code: null, stdout: "" };
    } };
    const result = await collectProjectEvidence(configuration(["web/package-lock.json", "web/.mise.toml", "api/uv.lock"]), root,
      { PATH: "/initial", HOME: root, REGISTRY_TOKEN: "secret-token", PROVIDER_API_KEY: "provider-secret" }, deps);
    expect(result.tool_versions.node).toBe("v24.3.0");
    expect(result.tool_versions["web:node"]).toBe("v22.18.0");
    expect(result.tool_versions["api:python"]).toBe("3.14.0");
    expect(result.tool_versions.go).toBeUndefined();
    expect(result.package_records.join("\n")).toContain(`web/package-lock.json: sha256:${createHash("sha256").update(lock).digest("hex")}`);
    expect(result.package_records.join("\n")).toContain("zod@4.1.0");
    expect(result.package_records.join("\n")).toContain("httpx@0.28.1");
    expect(result.package_records.join("\n")).toContain("go version; exit 1");
    expect(JSON.stringify(result)).not.toContain("secret-token");
    expect(JSON.stringify(result)).not.toContain("registry.invalid");
    const node = calls.find(call => call.directory.endsWith("/web") && call.argv[0] === "node");
    expect(node?.env.PATH).toBe(`${await realpath(root)}/web/selected-tools`);
    for (const call of calls) {
      expect(call.env.REGISTRY_TOKEN).toBeUndefined();
      expect(call.env.PROVIDER_API_KEY).toBeUndefined();
      expect(call.env.MISE_OFFLINE).toBe("true");
      expect(call.env.COREPACK_ENABLE_NETWORK).toBe("0");
    }
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("declaration evidence reports absent and malformed files without reading an external symbolic link", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-evidence-path-"));
  const outside = await mkdtemp(join(tmpdir(), "culler-evidence-secret-"));
  try {
    await writeFile(join(outside, "secret"), "private-token-value");
    await symlink(join(outside, "secret"), join(root, "external.lock"));
    await writeFile(join(root, "package-lock.json"), "invalid token=private-token-value");
    const result = await collectProjectEvidence(configuration(["external.lock", "missing.lock", "package-lock.json"]), root, {}, {
      async execute() { return { exit_code: null, stdout: "" }; },
    });
    expect(result.tool_versions).toEqual({});
    expect(result.package_records.join("\n")).toContain("Declaration external.lock: unavailable file type or outside the Project.");
    expect(result.package_records.join("\n")).toContain("Declaration missing.lock: unavailable.");
    expect(result.package_records.join("\n")).toContain("package details could not be parsed");
    expect(JSON.stringify(result)).not.toContain("private-token-value");
  } finally { await rm(root, { recursive: true, force: true }); await rm(outside, { recursive: true, force: true }); }
});

test("the evidence process records real version stdout and failed probes without an install command", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-evidence-command-"));
  try {
    const bin = join(root, "tools");
    await mkdir(bin);
    await writeFile(join(bin, "mise"), '#!/bin/sh\nif [ "$1" = env ]; then printf \'{"PATH":"%s"}\\n\' "$PATH"; else printf \'2026.8.0\\n\'; fi\n', { mode: 0o755 });
    await writeFile(join(bin, "node"), '#!/bin/sh\nprintf \'v22.18.0\\n\'\n', { mode: 0o755 });
    const result = await collectProjectEvidence(configuration([]), root, { PATH: bin, HOME: root });
    expect(result.tool_versions).toEqual({ node: "v22.18.0", mise: "2026.8.0" });
    expect(result.package_records.join("\n")).toContain("Tool node at .: node --version; exit 0; output version v22.18.0.");
    expect(result.package_records.join("\n")).toContain("npm --version; exit 127");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("the collector parses common lock formats and retains the complete Go version token", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-evidence-locks-"));
  try {
    await writeFile(join(root, "bun.lock"), '{\n// lock contents\n"packages": {"zod": ["zod@4.1.0", "", {}, "sha512-safe"]},\n}\n');
    await writeFile(join(root, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\npackages:\n  '@scope/tool@2.3.4': {}\n");
    await writeFile(join(root, "Cargo.lock"), 'version = 4\n[[package]]\nname = "serde"\nversion = "1.0.219"\n');
    const result = await collectProjectEvidence(configuration([]), root, {}, {
      async execute(argv) { return argv[0] === "go" ? { exit_code: 0, stdout: "go version go1.25.12 linux/arm64\n" } : { exit_code: null, stdout: "" }; },
    });
    expect(result.tool_versions.go).toBe("go1.25.12");
    expect(result.package_records.join("\n")).toContain("zod@4.1.0");
    expect(result.package_records.join("\n")).toContain("@scope/tool@2.3.4");
    expect(result.package_records.join("\n")).toContain("serde@1.0.219");
  } finally { await rm(root, { recursive: true, force: true }); }
});
