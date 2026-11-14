import { expect, spyOn, test } from "bun:test";
import * as fs from "node:fs";
import { chmod, mkdir, mkdtemp, rm, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  parseProfileAccess, parseScribeAcceptanceArgs, runScribeAcceptance,
  SCRIBE_TOOLS, successfulScribeTools, hasScribeSourceClaim,
  scribeAdapterInvocation, validateScribeHome,
} from "../../tests/live-model/scribe";

const args = [
  "--mc-home", "/tmp/example-home", "--mc-binary", "/tmp/example-mc", "--image", `sha256:${"a".repeat(64)}`,
  "--codex-binding", "codex-example", "--claude-binding", "sdk-example", "--output", "/tmp/example-output",
];

test("the live Scribe uses the private runtime and the normal adapter arguments", () => {
  for (const harness of ["codex", "claude-sdk"]) {
    const invocation = scribeAdapterInvocation({ run_id: "scribe-test", role: "scribe",
      harness, model_binding: "selected", subject_id: null, heartbeat_interval_s: 30,
      brief: "", mode: "fresh", repository: { input_commit: "a".repeat(40) },
      runtime: { harness, id: "selected", model: "fixture", channel: harness === "codex" ? "codex" : "claude", base_url: "" },
      mounts: { session: "/mc/session", workspace: "/workspace/source" } });
    expect(invocation.slice(0, 2)).toEqual(["/opt/culler-runtime/bin/bun",
      `/opt/culler-runtime/app/src/agent-runner/adapters/${harness === "codex" ? "codex" : "claude"}.ts`]);
    expect(invocation).toContain("--openwiki");
    expect(invocation).toContain("a".repeat(40));
    expect(invocation).toContain("/mc/session");
  }
});

test("the live command refuses before profile access without explicit spend consent", async () => {
  await expect(runScribeAcceptance(args)).rejects.toThrow("--confirm-live-spend");
  expect(() => parseScribeAcceptanceArgs(["--confirm-live-spend", ...args, "--image", "mc-prod"])).toThrow();
  const mutableImage = args.map((value) => value.startsWith("sha256:") ? "mc-prod" : value);
  expect(() => parseScribeAcceptanceArgs(["--confirm-live-spend", ...mutableImage])).toThrow("immutable Docker image ID");
});

test("the live command preserves an existing evidence directory before profile access", async () => {
  const root = await mkdtemp(join(tmpdir(), "mc-scribe-consent-"));
  const existsSync = fs.existsSync;
  const absentIdentity = join(root, "absent-run.json");
  const exists = spyOn(fs, "existsSync").mockImplementation((path) =>
    existsSync(path === "/mc/run.json" ? absentIdentity : path));
  try {
    await expect(runScribeAcceptance(["--confirm-live-spend", ...args.slice(0, -2), "--output", root])).rejects.toThrow("must be new");
  } finally {
    exists.mockRestore();
    await rm(root, { recursive: true, force: true });
  }
});

test("the Codex evidence counts successful MCP completions and ignores text or failed calls", () => {
  const events = SCRIBE_TOOLS.map((tool) => ({ type: "item.completed", item: {
    type: "mcp_tool_call", server: "openwiki", tool, status: "completed", result: { content: [] },
  } }));
  const native = events.map((event) => JSON.stringify(event)).join("\n");
  expect(successfulScribeTools(native, "codex")).toEqual(SCRIBE_TOOLS);
  events[5]!.item.status = "failed";
  const prompt = { type: "item.completed", item: { type: "agent_message", text: native } };
  const failed = [...events, prompt].map((event) => JSON.stringify(event)).join("\n");
  expect(successfulScribeTools(failed, "codex")).toEqual(SCRIBE_TOOLS.slice(0, 5));
});

test("the SDK evidence requires a successful result for the exact MCP call identifier", () => {
  const events = SCRIBE_TOOLS.flatMap((tool, i) => [
    { type: "assistant", message: { content: [{ type: "tool_use", id: String(i), name: `mcp__openwiki__${tool}` }] } },
    { type: "user", message: { content: [{ type: "tool_result", tool_use_id: String(i), is_error: i === 5 }] } },
  ]);
  events.push({ type: "user", message: { content: [{ type: "tool_result", tool_use_id: "unknown", is_error: false }] } });
  expect(successfulScribeTools(events.map((event) => JSON.stringify(event)).join("\n"), "claude-sdk")).toEqual(SCRIBE_TOOLS.slice(0, 5));
  events[11]!.message.content[0]!.is_error = false;
  expect(successfulScribeTools(events.map((event) => JSON.stringify(event)).join("\n"), "claude-sdk")).toEqual(SCRIBE_TOOLS);
});

test("the resident access response rejects provider secrets and another connection", () => {
  const access = { version: 1, harness: "codex", authMode: "account", url: "https://host.docker.internal:7444",
    token: "culler_proxy_" + "c".repeat(43), ca: "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----" };
  const value = { version: 1, status: "ok", credential: "selected", nonce: "a".repeat(32), proxy_config: access };
  const parse = (input: unknown) => parseProfileAccess(JSON.stringify(input) + "\n", "selected", value.nonce, "codex");
  expect(parse(value)).toEqual(access);
  for (const altered of [
    { ...value, credential: "other" }, { ...value, nonce: "b".repeat(32) },
    { ...value, access_token: "provider-secret" }, { ...value, refresh_token: "provider-secret" },
    { ...value, proxy_config: { ...access, refresh_token: "provider-secret" } },
  ]) expect(() => parse(altered)).toThrow();
});

test("the Scribe test refuses public homes and paths outside the isolated test root", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-scribe-home-"));
  try {
    const tests = join(root, "tests");
    const home = join(tests, "home");
    await mkdir(home, { recursive: true, mode: 0o700 });
    await validateScribeHome(home, tests);
    await expect(validateScribeHome(root, tests)).rejects.toThrow("private directory");
    await chmod(home, 0o755);
    await expect(validateScribeHome(home, tests)).rejects.toThrow("private directory");
    await symlink(root, join(tests, "outside"));
    await expect(validateScribeHome(join(tests, "outside"), tests)).rejects.toThrow("private directory");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("the live Scribe accepts the canonical source line range and rejects another source", () => {
  const claims = (resource: string) => ({ claims: [{ statement: "The service returns one complete project report.", evidence: [{ resource }] }] });
  expect(hasScribeSourceClaim(claims("repo://source.txt#L1"))).toBe(true);
  expect(hasScribeSourceClaim(claims("repo://source.txt#L1-L1"))).toBe(true);
  expect(hasScribeSourceClaim(claims("repo://source.txt#L1-L2"))).toBe(false);
  expect(hasScribeSourceClaim(claims("repo://another.txt#L1"))).toBe(false);
  expect(hasScribeSourceClaim({})).toBe(false);
  expect(parseScribeAcceptanceArgs(["--confirm-live-spend", ...args, "--harness", "claude-sdk"]).harness).toBe("claude-sdk");
  expect(() => parseScribeAcceptanceArgs(["--confirm-live-spend", ...args, "--harness", "other"])).toThrow("supported Scribe harness");
});
