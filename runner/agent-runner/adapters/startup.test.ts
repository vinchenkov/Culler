import { afterEach, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { executeClaudeQuery, runClaudeAdapter } from "./claude";
import { codexArgv, executeCodexProcess, runCodexAdapter } from "./codex";
import { OPENWIKI_TOOLS, type OpenWikiStatus } from "./openwiki";

const connected: OpenWikiStatus = {
  name: "openwiki", status: "connected", tools: OPENWIKI_TOOLS.map((name) => ({ name })),
};
const roots: string[] = [];
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });

async function claudeFixture(input: {
  initial?: OpenWikiStatus[];
  status?: (attempt: number) => Promise<OpenWikiStatus[]>;
  signal?: AbortSignal;
  stderr?: string;
  failAfterReady?: boolean;
  endBeforeReady?: boolean;
}) {
  const events: any[] = [];
  let attempts = 0;
  let toolComplete = false;
  let initialized!: () => void;
  const init = new Promise<void>((resolve) => { initialized = resolve; });
  const start = performance.now();
  const code = await executeClaudeQuery("Prepare the wiki", {} as any, true, {
    startupMs: 100, pollMs: 5, signal: input.signal,
    emit: (event) => events.push(event),
    query: (({ options }: any) => {
      options.stderr?.(input.stderr ?? "");
      const iterator = (async function* () {
        yield { type: "system", subtype: "init", session_id: "native-session", mcp_servers: input.initial ?? [] };
        initialized();
        yield { type: "system", subtype: "status", status: "busy" };
        if (input.endBeforeReady) return;
        await options.hooks.PreToolUse[0].hooks[0]({ tool_name: "mcp__openwiki__openwiki_begin" });
        toolComplete = true;
        yield { type: "assistant", message: { content: [{ type: "tool_use", name: "mcp__openwiki__openwiki_begin" }] } };
        if (input.failAfterReady) throw new Error("OpenWiki transport closed after initialization");
        yield { type: "system", subtype: "init", session_id: "native-session", mcp_servers: [connected] };
        yield { type: "result", is_error: false, result: "The wiki is ready" };
      })();
      return Object.assign(iterator, {
        mcpServerStatus: async () => { await init; return input.status ? input.status(++attempts) : [connected]; },
        close: () => { void iterator.return(undefined); },
      });
    }) as any,
  });
  return { code, events, toolComplete, attempts, elapsed: performance.now() - start };
}

for (const initial of [[{ name: "openwiki", status: "pending" }], []]) {
  test(`Claude permits delayed readiness after ${initial.length ? "pending" : "an absent server"}`, async () => {
    const result = await claudeFixture({ initial, status: async (attempt) => attempt < 3 ? initial : [connected] });
    expect(result.code).toBe(0);
    expect(result.toolComplete).toBe(true);
    expect(result.attempts).toBe(3);
    expect(result.events.filter((event) => event.event === "session-start")).toHaveLength(1);
    expect(result.events.findIndex((event) => event.event === "session-start"))
      .toBeLessThan(result.events.findIndex((event) => event.type === "assistant"));
    expect(result.events.some((event) => event.subtype === "status")).toBe(true);
  });
}

for (const noResponse of [false, true]) {
  test(`Claude keeps one deadline when status ${noResponse ? "never responds" : "stays pending"}`, async () => {
    const result = await claudeFixture({
      initial: [{ name: "openwiki", status: "pending" }],
      status: () => noResponse ? new Promise(() => {}) : Promise.resolve([{ name: "openwiki", status: "pending" }]),
    });
    expect(result.code).toBe(1);
    expect(result.toolComplete).toBe(false);
    expect(result.elapsed).toBeLessThan(1000);
    const failures = result.events.filter((event) => event.event === "infrastructure-failure");
    expect(failures).toHaveLength(1);
    expect(failures[0].condition).toContain("deadline expired");
    expect(failures[0].evidence).toContain("Last connection status: pending");
    expect(failures[0].evidence).toContain("Harness error: unknown");
    expect(result.events.filter((event) => event.event === "session-start")).toHaveLength(1);
  });
}

test("Claude retains the connection failure and the process error", async () => {
  const result = await claudeFixture({
    initial: [{ name: "openwiki", status: "failed", error: "Cannot find the OpenWiki runtime" }],
    stderr: "Error: missing module in the OpenWiki process",
  });
  expect(result.code).toBe(1);
  expect(result.toolComplete).toBe(false);
  const failure = result.events.find((event) => event.event === "infrastructure-failure");
  expect(failure.evidence).toContain("Last connection status: failed");
  expect(failure.evidence).toContain("Cannot find the OpenWiki runtime");
  expect(failure.evidence).toContain("missing module");
});

test("Claude refuses a ready server that lacks a required tool", async () => {
  const result = await claudeFixture({ status: async () => [{ ...connected, tools: [] }] });
  expect(result.code).toBe(1);
  expect(result.toolComplete).toBe(false);
  expect(result.events.find((event) => event.event === "infrastructure-failure").evidence).toContain("openwiki_finish");
});

test("Claude retains a failure after readiness and session registration", async () => {
  const result = await claudeFixture({ failAfterReady: true });
  expect(result.code).toBe(1);
  expect(result.events.filter((event) => event.event === "session-start")).toHaveLength(1);
  expect(result.events.find((event) => event.event === "infrastructure-failure").condition).toContain("transport closed");
});

test("Claude stops an incomplete connection without a failure report", async () => {
  const abort = new AbortController();
  setTimeout(() => abort.abort(), 25);
  const result = await claudeFixture({ signal: abort.signal, status: () => new Promise(() => {}) });
  expect(result.code).toBe(143);
  expect(result.toolComplete).toBe(false);
  expect(result.events.filter((event) => event.event === "infrastructure-failure")).toHaveLength(0);
});

test("Claude preserves the deadline after an incomplete stream snapshot", async () => {
  const result = await claudeFixture({ endBeforeReady: true, status: () => new Promise(() => {}) });
  expect(result.code).toBe(1);
  expect(result.events.find((event) => event.event === "infrastructure-failure").condition).toContain("deadline expired");
});

async function codexFixture(script: string, signal?: AbortSignal) {
  const root = await mkdtemp(join(tmpdir(), "mc-codex-startup-"));
  roots.push(root);
  const thread = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
  const original = JSON.stringify({ type: "response_item", payload: {
    type: "reasoning", id: "reasoning-1", encrypted_content: "AAAA-sk-" + "a".repeat(160),
    token: "culler_proxy_" + "c".repeat(43),
  } }) + "\n";
  await writeFile(join(root, `${thread}.jsonl`), original);
  const proc = Bun.spawn([process.execPath, "-e", `
    const emit = (event) => console.log(JSON.stringify(event));
    const thread = ${JSON.stringify(thread)};
    ${script}
  `], { env: process.env, stdin: "ignore", stdout: "pipe", stderr: "pipe" });
  const events: any[] = [];
  const code = await executeCodexProcess(proc, root, true, { emit: (event) => events.push(event), signal });
  return { code, events };
}

test("Codex retains its native startup deadline for fresh and resumed sessions", () => {
  for (const mode of ["fresh", "native"] as const) {
    const args = codexArgv(mode, "thread", "", "/workspace/source");
    expect(args).toContain("mcp_servers.openwiki.startup_timeout_sec=30");
    expect(args).toContain("mcp_servers.openwiki.required=true");
  }
});

test("Codex consumes delayed native tool events with one registered session", async () => {
  const result = await codexFixture(`
    emit({ type: "thread.started", thread_id: thread });
    await Bun.sleep(25);
    emit({ type: "item.completed", item: { type: "mcp_tool_call", server: "openwiki", tool: "openwiki_begin", status: "completed" } });
    emit({ type: "thread.started", thread_id: thread });
    emit({ type: "item.completed", item: { type: "agent_message", text: "The wiki is ready" } });
  `);
  expect(result.code).toBe(0);
  expect(result.events.filter((event) => event.event === "session-start")).toHaveLength(1);
  expect(result.events.some((event) => event.item?.tool === "openwiki_begin")).toBe(true);
});

for (const registered of [false, true]) {
  for (const message of ["MCP client for openwiki timed out after 30 seconds", "OpenWiki failed: module unavailable"]) {
    test(`Codex retains ${message} ${registered ? "after" : "before"} registration`, async () => {
      const result = await codexFixture(`
        ${registered ? 'emit({ type: "thread.started", thread_id: thread }); await Bun.sleep(25);' : ""}
        console.error("OpenWiki process: module loader failed");
        emit({ type: "turn.failed", error: { message: ${JSON.stringify(message)} } });
        process.exit(1);
      `);
      expect(result.code).toBe(1);
      const failure = result.events.find((event) => event.event === "infrastructure-failure");
      expect(failure.condition).toContain(message);
      expect(failure.evidence).toContain("Server: openwiki");
      expect(failure.evidence).toContain("Last connection status: unknown");
      expect(failure.evidence).toContain("module loader failed");
      expect(failure.evidence).toContain("process exit 1");
      expect(result.events.filter((event) => event.event === "session-start")).toHaveLength(registered ? 1 : 0);
    });
  }
}

test("Codex cancellation stops a silent native startup without a failure report", async () => {
  const abort = new AbortController();
  setTimeout(() => abort.abort(), 40);
  const result = await codexFixture("await Bun.sleep(60_000);", abort.signal);
  expect(result.code).toBe(143);
  expect(result.events.filter((event) => event.event === "infrastructure-failure")).toHaveLength(0);
});

test("Claude retains a failure before session registration", async () => {
  const events: any[] = [];
  const code = await executeClaudeQuery("Prepare the wiki", {} as any, true, {
    emit: (event) => events.push(event),
    query: (() => { throw new Error("OpenWiki transport initialization failed"); }) as any,
  });
  expect(code).toBe(1);
  expect(events.filter((event) => event.event === "session-start")).toHaveLength(0);
  expect(events.filter((event) => event.event === "infrastructure-failure")).toHaveLength(1);
  expect(events[0].condition).toBe("OpenWiki transport initialization failed");
  expect(events[0].evidence).toContain("Last connection status: unknown");
});

test("Claude reports a refused connection as a network error without provider facts", async () => {
  const events: any[] = [];
  const refused = "API Error: Connection refused — a firewall or proxy may be blocking it (ECONNREFUSED)";
  const code = await executeClaudeQuery("Reply", {} as any, false, {
    emit: (event) => events.push(event),
    query: (() => Object.assign((async function* () {
      yield { type: "system", subtype: "init", session_id: "session-1", mcp_servers: [] };
      yield { type: "system", subtype: "api_retry", attempt: 1, max_retries: 10, retry_delay_ms: 500, error_status: null, error: "server_error" };
      yield { type: "assistant", error: "server_error", message: { content: [{ type: "text", text: refused }] } };
      yield { type: "result", subtype: "success", is_error: true, api_error_status: null, result: refused };
    })(), { close: () => {} })) as any,
  });
  expect(code).toBe(1);
  const failure = events.find((event) => event.event === "infrastructure-failure");
  expect(failure.network_error).toBe("connect");
  expect(failure).not.toHaveProperty("provider_error_code");
  expect(failure).not.toHaveProperty("http_status");
});

test("Claude reports no failure after a retried request succeeds", async () => {
  const events: any[] = [];
  const code = await executeClaudeQuery("Reply", {} as any, false, {
    emit: (event) => events.push(event),
    query: (() => Object.assign((async function* () {
      yield { type: "system", subtype: "init", session_id: "session-1", mcp_servers: [] };
      yield { type: "system", subtype: "api_retry", attempt: 1, max_retries: 10, retry_delay_ms: 500, error_status: 529, error: "overloaded" };
      yield { type: "result", subtype: "success", is_error: false, result: "Done." };
    })(), { close: () => {} })) as any,
  });
  expect(code).toBe(0);
  expect(events.some((event) => event.event === "infrastructure-failure")).toBe(false);
});

test("Claude keeps the provider text of a refused request", async () => {
  const events: any[] = [];
  const refusal = "API Error: 400 Claude Code 2.1.217 does not support this model; version 2.1.280 or newer is required.";
  const code = await executeClaudeQuery("Reply", {} as any, false, {
    emit: (event) => events.push(event),
    query: (() => Object.assign((async function* () {
      yield { type: "system", subtype: "init", session_id: "session-1", mcp_servers: [] };
      yield { type: "assistant", error: "invalid_request", message: { content: [{ type: "text", text: refusal }] } };
      yield { type: "result", subtype: "success", is_error: true, api_error_status: 400, result: refusal };
    })(), { close: () => {} })) as any,
  });
  expect(code).toBe(1);
  const failure = events.find((event) => event.event === "infrastructure-failure");
  expect(failure.provider_error_code).toBe("invalid_request");
  expect(failure.condition).toBe(`The harness reported invalid_request. ${refusal}`);
});

test("Claude excludes the Culler token from native output and diagnostics", async () => {
  const events: any[] = [];
  const stderr: string[] = [];
  const secret = "synthetic-culler-access-value";
  const code = await executeClaudeQuery("Reply", {
    env: { CLAUDE_CODE_OAUTH_TOKEN: secret }, stderr: (data: string) => stderr.push(data),
  } as any, false, {
    emit: (event) => events.push(event),
    query: (({ options }: any) => Object.assign((async function* () {
      options.stderr(`Provider failure: ${secret}`);
      yield { type: "system", subtype: "init", session_id: "session-1", mcp_servers: [] };
      yield { type: "assistant", error: "authentication_failed", message: { content: [{ type: "text", text: secret }] } };
      yield { type: "result", is_error: true, api_error_status: 401, result: `Provider failure: ${secret}` };
    })(), { close: () => {} })) as any,
  });
  expect(code).toBe(1);
  expect(JSON.stringify(events)).not.toContain(secret);
  expect(stderr.join("\n")).not.toContain(secret);
  expect(events.find((event) => event.event === "infrastructure-failure")).toMatchObject({ http_status: 401 });
});

test("Codex preserves native identity and redacts Culler access after its file is removed", async () => {
  const root = await mkdtemp(join(tmpdir(), "mc-codex-access-"));
  roots.push(root);
  const token = "synthetic-culler-token-000000000000000000000000";
  const thread = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
  const request = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff";
  const reply = `The response belongs to ${thread}.`;
  const accessPath = join(root, "provider-access.json");
  await writeFile(accessPath, JSON.stringify({ token }));
  const original = JSON.stringify({ type: "response_item", payload: {
    type: "reasoning", id: "reasoning-1", encrypted_content: "AAAA-sk-" + "a".repeat(160),
    token: "culler_proxy_" + "c".repeat(43),
  } }) + "\n";
  await writeFile(join(root, `${thread}.jsonl`), original);
  const proc = Bun.spawn([process.execPath, "-e", `
    const emit = (event) => console.log(JSON.stringify(event));
    await Bun.sleep(50);
    emit({ type: "thread.started", thread_id: ${JSON.stringify(thread)} });
    emit({ type: "item.completed", item: { type: "agent_message", text: ${JSON.stringify(reply)} } });
    await Bun.file(${JSON.stringify(accessPath)}).delete();
    console.error(${JSON.stringify(token)});
    emit({ type: "turn.failed", error: { status: 401, code: "authentication_failed", request_id: ${JSON.stringify(request)}, message: ${JSON.stringify(token)} } });
    process.exit(1);
  `], { stdin: "ignore", stdout: "pipe", stderr: "pipe" });
  const events: any[] = [];
  const code = await executeCodexProcess(proc, root, false, {
    env: { MC_PROVIDER_ACCESS_FILE: accessPath }, emit: (event) => events.push(event),
  });
  expect(code).toBe(1);
  expect(JSON.stringify(events)).not.toContain(token);
  expect(events.find((event) => event.type === "thread.started").thread_id).toBe(thread);
  expect(events.find((event) => event.event === "session-start").session_id).toBe(thread);
  expect(events.find((event) => event.event === "turn-text").text).toBe(reply);
  expect(events.find((event) => event.item?.type === "agent_message").item.text).toBe(reply);
  expect(events.find((event) => event.event === "infrastructure-failure")).toMatchObject({
    http_status: 401, provider_error_code: "authentication_failed", proxy_request_id: request,
  });
  expect(events.find((event) => event.type === "turn.failed").error.message).toBe("[redacted]");
  expect(await Bun.file(join(root, `${thread}.jsonl`)).text()).toBe(original);
});

test("both adapters preserve existing originals during startup and shutdown", async () => {
  const root = await mkdtemp(join(tmpdir(), "mc-original-lifecycle-")); roots.push(root);
  const token = "culler_proxy_" + "c".repeat(43);
  const native = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
  const original = JSON.stringify({ type: "assistant", uuid: "signed-1", message: { content: [
    { type: "thinking", thinking: token, signature: Buffer.from("sk-" + "a".repeat(80)).toString("base64") },
    { type: "redacted_thinking", data: "AAAA-sk-" + "a".repeat(160) },
  ] }, encrypted_content: "AAAA-sk-" + "b".repeat(160) }) + "\n";
  const previous = process.env.MC_PROVIDER_ACCESS_FILE;
  try {
    process.env.MC_PROVIDER_ACCESS_FILE = join(root, "access.json");
    for (const harness of ["codex", "claude-sdk"] as const) {
      const directory = join(root, harness);
      await mkdir(join(directory, "subagents"), { recursive: true });
      const file = join(directory, harness === "codex" ? `rollout-${native}.jsonl` : "native.jsonl");
      const child = join(directory, "subagents/agent-1.jsonl");
      await writeFile(file, original, { mode: 0o600 });
      await writeFile(child, original, { mode: 0o600 });
      await writeFile(process.env.MC_PROVIDER_ACCESS_FILE, JSON.stringify({ version: 1, harness, authMode: "api-key", token,
        url: "https://host.docker.internal:4433", ca: "-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----" }));
      const args = ["--channel", "claude", "--workspace", root, "--session-dir", directory, "--native-ref", native, "--mode", "native", "--model", "fixture-model"];
      for (let repeat = 0; repeat < 2; repeat++) {
        const code = harness === "codex"
          ? await runCodexAdapter(args, { input: async () => "Continue", spawn: ((_argv: unknown, options: any) => {
            return Bun.spawn([process.execPath, "-e", `console.log(JSON.stringify({type:'thread.started',thread_id:'${native}'})); console.log(JSON.stringify({type:'turn.completed'}));`], { ...options, stdin: "ignore" });
          }) as typeof Bun.spawn })
          : await runClaudeAdapter(args, { input: async () => "Continue", query: (({ options }: any) => Object.assign((async function* () {
            expect(await options.sessionStore.load({ projectKey: "workspace", sessionId: native })).toEqual([JSON.parse(original)]);
            yield { type: "system", subtype: "init", session_id: native };
            yield { type: "result", subtype: "success", is_error: false, result: "Complete" };
          })(), { close: () => {} })) as any });
        expect(code).toBe(0);
        expect(await readFile(file, "utf8")).toBe(original);
        expect(await readFile(child, "utf8")).toBe(original);
      }
    }
  } finally {
    if (previous === undefined) delete process.env.MC_PROVIDER_ACCESS_FILE;
    else process.env.MC_PROVIDER_ACCESS_FILE = previous;
  }
});
