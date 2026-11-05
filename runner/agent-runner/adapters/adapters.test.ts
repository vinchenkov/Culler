import { afterEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  CLAUDE_BIN,
  claudeAdapterEnv,
  claudeOptions,
  claudeReplyFromMessage,
  makeClaudeTextExtractor,
  mirrorRoleSkill,
  parseMaxTurns,
} from "./claude";
import { installRunSkills } from "../skills";
import {
  CODEX_BIN,
  codexAdapterEnv,
  codexArgv,
  codexReplyFromEvent,
  makeCodexTextExtractor,
  findCodexNativeFile,
} from "./codex";
import { NativeSessionStore } from "./session-store";
import type { ProviderAccess } from "./provider-access";

const proxyAccess: ProviderAccess = { version: 1, url: "https://host.docker.internal:7444", token: "culler_proxy_" + "c".repeat(43),
  ca: "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----", harness: "codex", authMode: "api-key" };
const claudeAccess: ProviderAccess = { ...proxyAccess, harness: "claude-sdk" };

const roots: string[] = [];
afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

describe("Codex production adapter", () => {
  test("normalizes only a completed agent message into the Homie reply", () => {
    expect(codexReplyFromEvent(JSON.stringify({
      type: "item.completed",
      item: { type: "agent_message", text: "codex reply" },
    }))).toBe("codex reply");
    expect(codexReplyFromEvent(JSON.stringify({
      type: "item.completed",
      item: { type: "command_execution", text: "tool output" },
    }))).toBeUndefined();
  });

  test("streams each completed agent message as draft text, later ones as new paragraphs", () => {
    const textFrom = makeCodexTextExtractor();
    const agent = (text: string) => JSON.stringify({ type: "item.completed", item: { type: "agent_message", text } });
    expect(textFrom(JSON.stringify({ type: "item.completed", item: { type: "command_execution", text: "ls" } }))).toBeUndefined();
    expect(textFrom(agent("Looking."))).toBe("Looking.");
    expect(textFrom("not json")).toBeUndefined();
    expect(textFrom(agent("Done."))).toBe("\n\nDone.");
  });

  test("fresh/resume argv are closed, noninteractive, and un-sandboxed by decision", () => {
    expect(codexArgv("fresh")).toEqual([
      CODEX_BIN, "exec", "--json", "--strict-config",
      "--config", 'approval_policy="never"',
      "--config", 'sandbox_mode="danger-full-access"',
      "--config", "shell_environment_policy.ignore_default_excludes=false",
      "--config", 'shell_environment_policy.exclude=["CULLER_PROXY_TOKEN"]',
      "--config", "tool_output_token_limit=100000",
      "--config", "memories.generate_memories=false",
      "--config", "memories.use_memories=false",
      "--skip-git-repo-check", "--ignore-user-config", "--ignore-rules", "-",
    ]);
    expect(codexArgv("native", "thread-1")).toEqual([
      CODEX_BIN, "exec", "resume", "--json", "--strict-config",
      "--config", 'approval_policy="never"',
      "--config", 'sandbox_mode="danger-full-access"',
      "--config", "shell_environment_policy.ignore_default_excludes=false",
      "--config", 'shell_environment_policy.exclude=["CULLER_PROXY_TOKEN"]',
      "--config", "tool_output_token_limit=100000",
      "--config", "memories.generate_memories=false",
      "--config", "memories.use_memories=false",
      "--skip-git-repo-check", "--ignore-user-config", "--ignore-rules", "thread-1", "-",
    ]);
    expect(() => codexArgv("native")).toThrow("thread id");
  });

  // The resume path is the one that silently reverts: an `exec`-only flag put
  // here parses fresh and dies on resume, and a sandbox that comes back on
  // resume breaks `mc` only for continued sessions — the rarest run to test by
  // hand. Assert the posture rides BOTH paths, and that no permission-profile
  // remnant survives to fight the mode it replaced.
  test("no sandbox survives on either path, and no profile remnant contradicts it", () => {
    for (const argv of [codexArgv("fresh"), codexArgv("native", "thread-1")]) {
      expect(argv).toContain('sandbox_mode="danger-full-access"');
      expect(argv.filter((a) => a.startsWith("permissions.") ||
        a.startsWith("default_permissions"))).toEqual([]);
      expect(argv).not.toContain("--dangerously-bypass-approvals-and-sandbox");
      // The broker URL is the only in-container string that outlives the
      // access token it sits beside; it must be scrubbed from spawned shells.
      expect(argv).toContain('shell_environment_policy.exclude=["CULLER_PROXY_TOKEN"]');
    }
  });

  // A verification gate must return a terminal status regardless of its
  // output volume. A long verbose gate loses its exit status under a
  // harness-default output ceiling. Codex has no
  // env knob; tool_output_token_limit is the config key that governs
  // exec-output truncation, and the pinned ceiling must ride resume too —
  // a limit that reverts on resume starves exactly the longest sessions.
  test("the exec-tool output ceiling rides both paths", () => {
    for (const argv of [codexArgv("fresh"), codexArgv("native", "thread-1")]) {
      expect(argv).toContain("tool_output_token_limit=100000");
    }
  });

  test("the configured model rides as a flag, and an empty one is omitted", () => {
    expect(codexArgv("fresh", undefined, "operator-chosen")).toEqual(expect.arrayContaining([
      "--model", "operator-chosen",
    ]));
    expect(codexArgv("fresh")).not.toContain("--model");
    // The prompt sentinel stays last no matter what precedes it.
    expect(codexArgv("native", "thread-1", "operator-chosen").slice(-2)).toEqual(["thread-1", "-"]);
  });

  test("Scribe config supplies OpenWiki in fresh and resumed sessions", () => {
    for (const mode of ["fresh", "native"] as const) {
      const argv = codexArgv(mode, mode === "native" ? "thread-1" : undefined, "configured-model", "/workspace/source");
      expect(argv).toContain('mcp_servers.openwiki.command="/usr/bin/env"');
      expect(argv).toContain("mcp_servers.openwiki.required=true");
      expect(argv).toContain("--ignore-user-config");
      expect(argv).toContain("memories.generate_memories=false");
      expect(argv).toContain("memories.use_memories=false");
      expect(argv.at(-1)).toBe("-");
    }
  });

  test("the Codex environment replaces inherited credentials with Culler authority", () => {
    const env = codexAdapterEnv({
      HOME: "/home/agent", PATH: "/home/agent/.local/bin:/bin", CODEX_HOME: "/mc/codex",
      CODEX_REFRESH_TOKEN_URL_OVERRIDE: "http://host.docker.internal:7/oauth/token",
      OPENAI_API_KEY: "metered", CODEX_ACCESS_TOKEN: "identity", ANTHROPIC_AUTH_TOKEN: "foreign",
    }, proxyAccess, "/tmp/private/ca.pem", "/tmp/private");
    expect(env.HOME).toBe("/home/agent");
    expect(env.PATH).toBe("/home/agent/.local/bin:/bin");
    expect(env.CODEX_HOME).toBe("/tmp/private");
    expect(env.CULLER_PROXY_TOKEN).toBe(proxyAccess.token);
    expect(env.CODEX_CA_CERTIFICATE).toBe("/tmp/private/ca.pem");
    expect(env.CODEX_REFRESH_TOKEN_URL_OVERRIDE).toBeUndefined();
    expect(env.OPENAI_API_KEY).toBeUndefined();
    expect(env.CODEX_ACCESS_TOKEN).toBeUndefined();
    expect(env.ANTHROPIC_AUTH_TOKEN).toBeUndefined();
  });

  test("the registered locator is the exact durable rollout under the session root", async () => {
    const root = mkdtempSync(join(tmpdir(), "mc-codex-native-"));
    roots.push(root);
    mkdirSync(join(root, "2026", "07", "22"), { recursive: true });
    writeFileSync(join(root, "2026", "07", "22", "rollout-11111111-1111-4111-8111-111111111111.jsonl"), "{}\n");
    expect(await findCodexNativeFile(root, "11111111-1111-4111-8111-111111111111", 10)).toBe(
      "2026/07/22/rollout-11111111-1111-4111-8111-111111111111.jsonl",
    );
  });
});

describe("Claude-SDK production adapter", () => {
  test("normalizes only a successful result message into the Homie reply", () => {
    expect(claudeReplyFromMessage({
      type: "result", is_error: false, result: "claude reply",
    })).toBe("claude reply");
    expect(claudeReplyFromMessage({
      type: "result", is_error: true, result: "provider error",
    })).toBeUndefined();
  });

  test("streams top-level text deltas as draft text, a later message as a new paragraph", () => {
    const textFrom = makeClaudeTextExtractor();
    const delta = (text: string, parent: string | null = null) => ({
      type: "stream_event", parent_tool_use_id: parent,
      event: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text } },
    });
    const start = { type: "stream_event", parent_tool_use_id: null, event: { type: "message_start" } };
    expect(textFrom(start)).toBeUndefined();
    expect(textFrom(delta("Let me"))).toBe("Let me");
    expect(textFrom(delta(" look."))).toBe(" look.");
    expect(textFrom({ type: "assistant", message: { content: [{ type: "text", text: "Let me look." }] } })).toBeUndefined();
    expect(textFrom(delta("subagent", "toolu_1"))).toBeUndefined();
    expect(textFrom(start)).toBeUndefined();
    expect(textFrom({ type: "stream_event", parent_tool_use_id: null,
      event: { type: "content_block_delta", index: 0, delta: { type: "input_json_delta", partial_json: "{" } } })).toBeUndefined();
    expect(textFrom(delta("Four seeded."))).toBe("\n\nFour seeded.");
    expect(textFrom(delta(""))).toBeUndefined();
  });

  test("each configured credential channel receives exactly its own material", () => {
    const base = {
      HOME: "/home/agent", PATH: "/home/agent/.local/bin:/bin", MC_SPINE: "/mc/spine/spine.db",
      CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: "1",
      CLAUDE_CODE_OAUTH_TOKEN: "short-oauth",
      ANTHROPIC_AUTH_TOKEN: "static-token-a",
      ANTHROPIC_API_KEY: "metered",
    };
    const claude = claudeAdapterEnv("claude", base, "/tmp/config", claudeAccess, "/tmp/private/ca.pem");
    expect(claude.HOME).toBe("/home/agent");
    expect(claude.PATH).toBe("/home/agent/.local/bin:/bin");
    expect(claude.CLAUDE_CONFIG_DIR).toBe("/tmp/config");
    expect(claude.CLAUDE_CODE_OAUTH_TOKEN).toBeUndefined();
    expect(claude.ANTHROPIC_AUTH_TOKEN).toBe(claudeAccess.token);
    expect(claude.ANTHROPIC_API_KEY).toBeUndefined();
    for (const channel of ["static", "none"]) {
      expect(() => claudeAdapterEnv(channel as "claude", base, "/tmp/config", claudeAccess, "/tmp/ca.pem")).toThrow("proxy configuration");
    }
    expect(claude.BASH_MAX_OUTPUT_LENGTH).toBe("150000");
    expect(claude.MAX_OUTPUT_LENGTH).toBe("150000");
  });

  test("SDK options pin isolation, tools, native persistence, and resume", () => {
    const store = new NativeSessionStore("/mc/session");
    const options = claudeOptions("endpoint-model", "/workspace/source", {
      PATH: "/bin", CLAUDE_CONFIG_DIR: "/tmp/config",
    }, store, "native-1");
    expect(options).toMatchObject({
      cwd: "/workspace/source", model: "endpoint-model", resume: "native-1",
      permissionMode: "bypassPermissions", allowDangerouslySkipPermissions: true,
      // "user" resolves to the adapter's own per-run tmpdir, never the
      // operator's ~/.claude and never the model-writable workspace, and it is
      // what makes the mirrored role skill discoverable.
      settingSources: ["user"], skills: "all",
      strictMcpConfig: true, persistSession: true,
      mcpServers: {}, settings: { autoMemoryEnabled: false, autoDreamEnabled: false },
      sessionStore: store, sessionStoreFlush: "eager",
      includePartialMessages: true, forwardSubagentText: true,
      systemPrompt: { type: "preset", preset: "claude_code" },
      tools: { type: "preset", preset: "claude_code" },
    });
  });

  // The SDK sandbox sets no_new_privs, and the kernel then ignores the setuid
  // bit of mc. The container is the isolation boundary, as for codex.
  test("the SDK sandbox is off, so a shell command can run the setuid mc", () => {
    const options = claudeOptions("endpoint-model", "/workspace/source", {
      PATH: "/bin", CLAUDE_CONFIG_DIR: "/tmp/config",
    }, new NativeSessionStore("/mc/session"));
    expect(options.sandbox).toEqual({ enabled: false });
  });

  test("Scribe config supplies OpenWiki without a project configuration file", () => {
    const options = claudeOptions("configured-model", "/workspace/source", {
      PATH: "/bin", CLAUDE_CONFIG_DIR: "/tmp/config",
    }, new NativeSessionStore("/mc/session"), undefined, undefined, true);
    expect(options.strictMcpConfig).toBe(true);
    expect(options.mcpServers).toEqual({
      openwiki: {
        command: "/usr/bin/env",
        args: [
          "-i", "PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp/mc-openwiki",
          "/usr/local/bin/node", "/app/src/openwiki/server.ts", "--repo", "/workspace/source",
          "--receipt", "/tmp/openwiki-completion.json",
        ],
      },
    });
    expect(options.settings).toEqual({ autoMemoryEnabled: false, autoDreamEnabled: false });
  });

  // Enabling a setting source to carry the role skill is the narrow part of
  // this design. "project" would re-enable .claude/settings.json, CLAUDE.md,
  // hooks and agents read from /workspace/source — a directory the model
  // can write — so the agent could author the
  // configuration it then runs under. "user" here is a per-run tmpdir this
  // adapter creates and deletes, holding nothing but the mirrored skill.
  test("the skill setting source is the private config dir, never the writable project", () => {
    const store = new NativeSessionStore("/mc/session");
    const options = claudeOptions("", "/workspace/source", {
      PATH: "/bin", CLAUDE_CONFIG_DIR: "/tmp/config",
    }, store) as unknown as { settingSources: string[]; skills: string };
    expect(options.settingSources).toEqual(["user"]);
    expect(options.settingSources).not.toContain("project");
    expect(options.settingSources).not.toContain("local");
    expect(options.skills).toBe("all");
  });

  // The live acceptance bounds its provider requests with the turn cap. A
  // pipeline run passes no cap, so the SDK option is absent.
  test("--max-turns caps the SDK turns only when the launch carries it", () => {
    const store = new NativeSessionStore("/mc/session");
    const env = { PATH: "/bin", CLAUDE_CONFIG_DIR: "/tmp/config" };
    const capped = claudeOptions("hosted-model", "/workspace/source", env, store, undefined, 2);
    expect(capped.maxTurns).toBe(2);
    const uncapped = claudeOptions("hosted-model", "/workspace/source", env, store);
    expect("maxTurns" in uncapped).toBe(false);
    expect(parseMaxTurns(undefined)).toBeUndefined();
    expect(parseMaxTurns("2")).toBe(2);
    for (const bad of ["0", "-1", "two", "1.5", ""]) expect(parseMaxTurns(bad)).toBeNull();
  });

  test("concurrent roles preserve separate skills, configuration, and sessions after cleanup", async () => {
    const root = mkdtempSync(join(tmpdir(), "mc-concurrent-roles-"));
    roots.push(root);
    const runs = ["worker", "scribe", "setup"].map(role => {
      const home = join(root, role, "home"), config = join(root, role, "config");
      const session = join(root, role, "session");
      return { role, home, config, session, store: new NativeSessionStore(session),
        key: { projectKey: "project", sessionId: role } };
    });
    await Promise.all(runs.map(async run => {
      await installRunSkills(join(run.home, ".agents/skills"), [{ name: `mc-${run.role}`, body: run.role }]);
      expect(await mirrorRoleSkill(run.config, { HOME: run.home })).toBe(true);
      await run.store.append(run.key, [{ type: "assistant", uuid: run.role, body: run.role }]);
    }));
    await rm(join(root, "worker"), { recursive: true });
    for (const run of runs.slice(1)) {
      expect(await Bun.file(join(run.home, `.agents/skills/mc-${run.role}/SKILL.md`)).text()).toBe(run.role);
      expect(await Bun.file(join(run.config, `skills/mc-${run.role}/SKILL.md`)).text()).toBe(run.role);
      expect(await Bun.file(join(run.config, "skills/mc-worker/SKILL.md")).exists()).toBe(false);
      expect(await run.store.load(run.key)).toEqual([{ type: "assistant", uuid: run.role, body: run.role }]);
    }
  });

  test("mirrorRoleSkill copies the runner-installed skill into the config dir", async () => {
    const home = mkdtempSync(join(tmpdir(), "mc-skill-home-"));
    const config = mkdtempSync(join(tmpdir(), "mc-skill-config-"));
    roots.push(home, config);
    const shared = join(home, ".agents", "skills");
    await installRunSkills(shared, [{ name: "mc-worker", body: "worker surface\n" }]);

    expect(await mirrorRoleSkill(config, { HOME: home })).toBe(true);
    const mirrored = join(config, "skills", "mc-worker", "SKILL.md");
    expect(await Bun.file(mirrored).text()).toBe("worker surface\n");
    // Mirrored, not moved: the shared root is the path the frozen directive
    // advertises, and it must stay readable when SDK discovery does not fire.
    expect(await Bun.file(join(shared, "mc-worker", "SKILL.md")).exists()).toBe(true);
    // A container with no HOME degrades to the directive's pointer rather than
    // failing the run.
    expect(await mirrorRoleSkill(config, {})).toBe(false);
  });
});

// The workspace becomes the harness `cwd`, and a missing one is not an
// abstract possibility: the resident wrote `/workspace/source` into every
// launch envelope, including plan-less runs that bind no workspace at all
// and run on an image shipping no /workspace. The SDK spawn failed and
// reported a libc mismatch on its own binary, so the run crash-looped for a
// day while the log accused the wrong component.
//
// These two RUN the adapters as real subprocesses. The rest of this file
// asserts argv and env shapes it never executes, which is exactly the blind
// spot that let the bad cwd ship.
describe("adapters refuse a workspace nothing mounted", () => {
  for (const [family, script] of [["claude", "./claude.ts"], ["codex", "./codex.ts"]] as const) {
    test(`the ${family} adapter names the missing mount instead of spawning into it`, async () => {
      const root = mkdtempSync(join(tmpdir(), "mc-adapter-cwd-"));
      roots.push(root);
      const absent = join(root, "no-such-workspace");
      const proc = Bun.spawn(
        [
          "bun", join(import.meta.dir, script),
          "--session-dir", root,
          "--workspace", absent,
          // Each family's own required flags; neither is reached.
          ...(family === "claude" ? ["--channel", "claude"] : ["--mode", "fresh"]),
        ],
        { env: process.env, stdin: new TextEncoder().encode("brief"), stdout: "pipe", stderr: "pipe" },
      );
      const [code, stderr] = await Promise.all([proc.exited, new Response(proc.stderr).text()]);
      expect(stderr).toContain("nothing mounted it");
      expect(stderr).toContain(absent);
      expect(code).toBe(2);
    });
  }
});


test("inherited settings cannot change the provider or authentication method", () => {
  const forbidden = {
    OPENAI_BASE_URL: "https://wrong.invalid", OPENAI_API_KEY: "fixture-key",
    OPENAI_CUSTOM_HEADERS: "Authorization: fixture-key", ANTHROPIC_BASE_URL: "https://wrong.invalid",
    ANTHROPIC_AUTH_TOKEN: "fixture-token", ANTHROPIC_API_KEY: "fixture-key",
    ANTHROPIC_CUSTOM_HEADERS: "Authorization: fixture-key", CODEX_CHATGPT_BASE_URL: "https://wrong.invalid",
    CODEX_REALTIME_API_BASE_URL: "https://wrong.invalid", CODEX_API_KEY: "fixture-key",
    CLAUDE_CODE_USE_BEDROCK: "1", CLAUDE_CODE_USE_VERTEX: "1", CLAUDE_CODE_USE_FOUNDRY: "1",
    CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR: "17", REAL_REFRESH_TOKEN: "fixture-refresh",
  };
  const transport = {
    HTTP_PROXY: "http://proxy.invalid:8080", HTTPS_PROXY: "http://proxy.invalid:8080", NO_PROXY: "localhost",
    NODE_EXTRA_CA_CERTS: "/certs/ca.pem", SSL_CERT_FILE: "/certs/ca.pem", CODEX_CA_CERTIFICATE: "/certs/ca.pem",
    CLAUDE_CODE_CLIENT_CERT: "/certs/client.pem", CLAUDE_CODE_CLIENT_KEY: "/certs/client.key",
  };
  const base = { ...forbidden, ...transport,
    CODEX_HOME: "/mc/codex", CODEX_REFRESH_TOKEN_URL_OVERRIDE: "http://host.docker.internal:7/oauth/token",
    CLAUDE_CODE_OAUTH_TOKEN: "fixture-access", CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: "1",
  };
  const environments = [
    claudeAdapterEnv("claude", base, "/scratch/config", claudeAccess, "/scratch/ca.pem"),
    codexAdapterEnv(base, proxyAccess, "/scratch/ca.pem", "/scratch/config"),
  ];
  for (const env of environments) {
    for (const [key, value] of Object.entries(forbidden)) expect(env[key]).not.toBe(value);
    expect(env.HTTP_PROXY).toBeUndefined();
    expect(env.HTTPS_PROXY).toBeUndefined();
    expect(env.MC_CREDENTIAL_REFRESH_URL).toBeUndefined();
    expect(env.CODEX_REFRESH_TOKEN_URL_OVERRIDE).toBeUndefined();
  }
  expect(environments[0]!.ANTHROPIC_BASE_URL).toBe(claudeAccess.url);
  expect(environments[0]!.NODE_EXTRA_CA_CERTS).toBe("/scratch/ca.pem");
  expect(environments[1]!.CODEX_CA_CERTIFICATE).toBe("/scratch/ca.pem");
});

test("account and key connections retain their selected Claude authentication mode", () => {
  const base = { CLAUDE_CODE_OAUTH_TOKEN: "old-provider-token", ANTHROPIC_AUTH_TOKEN: "old-provider-key" };
  for (const authMode of ["account", "api-key"] as const) {
    const access = { ...claudeAccess, authMode };
    const first = claudeAdapterEnv("claude", base, "/tmp/first", access, "/tmp/ca.pem");
    const next = claudeAdapterEnv("claude", first, "/tmp/next", access, "/tmp/ca.pem");
    expect(JSON.stringify(next)).not.toContain("old-provider");
    expect(next.ANTHROPIC_BASE_URL).toBe(access.url);
    expect(next.CLAUDE_CODE_OAUTH_TOKEN).toBe(authMode === "account" ? access.token : undefined);
    expect(next.ANTHROPIC_AUTH_TOKEN).toBe(authMode === "api-key" ? access.token : undefined);
    expect(next.MC_CREDENTIAL_REFRESH_URL).toBeUndefined();
    expect(next.CLAUDE_CODE_MAX_RETRIES).toBe("0");
  }
});

test("explicit native effort and output controls preserve ordinary run defaults", () => {
  const ordinary = codexArgv("fresh");
  expect(ordinary.some(arg => arg.startsWith("model_reasoning_effort="))).toBe(false);
  const bounded = codexArgv("fresh", undefined, "fixture", undefined, undefined, "minimal");
  expect(bounded).toContain('model_reasoning_effort="minimal"');
  const store = new NativeSessionStore("/tmp/unused-native-session");
  const options = claudeOptions("fixture", "/tmp", {}, store);
  expect(options.effort).toBeUndefined();
  expect(options.env?.CLAUDE_CODE_MAX_OUTPUT_TOKENS).toBeUndefined();
  const limited = claudeOptions("fixture", "/tmp", {}, store, undefined, 2, false, undefined, "low", 1024);
  expect(limited.effort).toBe("low");
  expect(limited.env?.CLAUDE_CODE_MAX_OUTPUT_TOKENS).toBe("1024");
});

test("the adapters start the Linux arm64 harness programs by their runtime paths", () => {
  const store = new NativeSessionStore("/tmp/unused-native-session");
  const options = claudeOptions("fixture", "/tmp", {}, store);
  expect(options.pathToClaudeCodeExecutable).toBe(CLAUDE_BIN);
  expect(CLAUDE_BIN).toBe("/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-arm64/claude");
  expect(CODEX_BIN).toBe("/app/node_modules/@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/bin/codex");
});

test("native effort controls preserve the selected level", () => {
  for (const effort of ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"]) {
    expect(codexArgv("fresh", undefined, "selected", undefined, undefined, effort)).toContain(`model_reasoning_effort="${effort}"`);
    expect(codexArgv("native", "thread", "selected", undefined, undefined, effort)).toContain(`model_reasoning_effort="${effort}"`);
  }
  for (const effort of ["low", "medium", "high", "xhigh", "max"] as const) {
    const options = claudeOptions("selected", "/workspace", {}, {} as never, undefined, undefined, false, undefined, effort);
    expect(options.effort).toBe(effort);
  }
});
