import { expect, test } from "bun:test";
import { openWikiCodexConfig, openWikiServer } from "./openwiki";

test("the OpenWiki process receives no provider credentials", async () => {
  const server = openWikiServer("/workspace/source");
  const runtime = server.args.indexOf("/usr/local/bin/node");
  const child = Bun.spawn([
    server.command, ...server.args.slice(0, runtime), process.execPath, "-e",
    "process.stdout.write(JSON.stringify(process.env))",
  ], {
    env: {
      ...process.env,
      ANTHROPIC_AUTH_TOKEN: "fixture-token",
      CODEX_REFRESH_TOKEN_URL_OVERRIDE: "https://example.invalid/refresh",
      PROVIDER_CREDENTIAL: "fixture-secret",
    },
    stdout: "pipe", stderr: "pipe",
  });
  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited, new Response(child.stdout).text(), new Response(child.stderr).text(),
  ]);
  expect(exitCode).toBe(0);
  expect(stderr).toBe("");
  expect(JSON.parse(stdout)).toEqual({
    PATH: "/usr/local/bin:/usr/bin:/bin", HOME: "/tmp/mc-openwiki",
  });
});

test("Codex receives the exact repository path through TOML configuration", () => {
  const workspace = '/workspace/project "notes"';
  const args = openWikiCodexConfig(workspace);
  const document = args.filter((_, index) => index % 2 === 1).join("\n");
  const config = Bun.TOML.parse(document) as {
    mcp_servers: { openwiki: { command: string; args: string[]; required: boolean } };
  };
  expect(config.mcp_servers.openwiki.command).toBe("/usr/bin/env");
  const serverArgs = config.mcp_servers.openwiki.args;
  expect(serverArgs[serverArgs.indexOf("--repo") + 1]).toBe(workspace);
  expect(serverArgs[serverArgs.indexOf("--receipt") + 1]).toBe("/tmp/openwiki-completion.json");
  expect(config.mcp_servers.openwiki.required).toBe(true);
});

test("OpenWiki rejects a repository path outside the absolute-path contract", () => {
  expect(() => openWikiServer("source")).toThrow("absolute repository path");
  expect(() => openWikiServer("/workspace/source\0other")).toThrow("absolute repository path");
  expect(() => openWikiServer("/workspace/source", "main")).toThrow("canonical input commit");
});

test("the server receives the assigned input commit and its receipt path", () => {
  const commit = "a".repeat(40);
  const server = openWikiServer("/workspace/task/source", commit);
  expect(server.args.slice(-4)).toEqual([
    "--receipt", "/tmp/openwiki-completion.json", "--input-commit", commit,
  ]);
});
