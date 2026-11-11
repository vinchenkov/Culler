import { expect, test } from "bun:test";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { loadProviderAccess, parseProviderAccess, writeProviderCA } from "./provider-access";

const access = { version: 1, url: "https://host.docker.internal:4433", token: "culler_proxy_" + "c".repeat(43),
  ca: "-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----", harness: "codex", authMode: "api-key" };

test("the access file rejects provider secrets and caller-selected transport controls", () => {
  expect(parseProviderAccess(JSON.stringify(access), "codex")).toEqual(access);
  for (const patch of [
    { access_token: "synthetic-provider-token" }, { refresh_token: "synthetic-refresh-token" },
    { api_key: "synthetic-key" }, { ca: "-----BEGIN CERTIFICATE-----\n-----BEGIN PRIVATE KEY-----" },
    { url: "http://host.docker.internal" }, { url: "https://name:password@host.docker.internal" },
    { url: "https://host.docker.internal?destination=external" }, { url: "https://host.docker.internal#secret" },
    { harness: "claude-sdk" }, { token: "short" }, { authMode: "unknown" }, { version: 2 },
  ]) expect(() => parseProviderAccess(JSON.stringify({ ...access, ...patch }), "codex")).toThrow();
  expect(() => parseProviderAccess(" ".repeat(65537), "codex")).toThrow("exceeds its limit");
  expect(() => parseProviderAccess("not-json", "codex")).toThrow("invalid JSON");
});

test("the adapter requires an explicit absolute access file", async () => {
  await expect(loadProviderAccess("codex", {})).rejects.toThrow("unavailable");
  await expect(loadProviderAccess("codex", { MC_PROVIDER_ACCESS_FILE: "relative.json" })).rejects.toThrow("unavailable");
});

test("the public certificate file is private and refuses an existing replacement", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-access-test-"));
  try {
    const config = parseProviderAccess(JSON.stringify(access), "codex");
    const path = await writeProviderCA(config, root);
    expect(await readFile(path, "utf8")).toBe(access.ca);
    expect((await stat(path)).mode & 0o777).toBe(0o400);
    await expect(writeProviderCA({ ...config, ca: "replacement" }, root)).rejects.toThrow();
    expect(await readFile(path, "utf8")).toBe(access.ca);
  } finally { await rm(root, { recursive: true, force: true }); }
});
