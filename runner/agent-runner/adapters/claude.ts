#!/usr/bin/env bun
import { loadProviderAccess, writeProviderCA, redactNativeAccess, type ProviderAccess } from "./provider-access";
import { cp, mkdir, mkdtemp, rm, stat } from "node:fs/promises";
import { join } from "node:path";
import { query, type Options } from "@anthropic-ai/claude-agent-sdk";
import { makeLogger, parseLevel } from "../../shared/log";
import { runLogSink } from "../../shared/log-sink";
import { MAIN_NATIVE_FILE, NativeSessionStore } from "./session-store";
import { agentSkillRoot } from "../skills";
import { openWikiServer, OpenWikiStartup, OPENWIKI_STARTUP_MS } from "./openwiki";
import { redactDiagnosticEvent, providerFailureFields, adapterFailure, failureFacts, limitDiagnostic, loadDiagnosticRedactor } from "../diagnostics";
import { isNetworkErrorClass, type NetworkErrorClass } from "../../shared/network-error";

// The adapter is a separate process spawned by the runner with `stderr:
// "inherit"`, so before this every line below went to the container's stderr
// and died with the `--rm` container. It writes to the same per-run file the
// runner does — one write(2) per record on an O_APPEND fd, which is what lets
// two processes share the file without interleaving — and falls back to stderr
// when the resident did not compose the bind.
const log = makeLogger({
  comp: "claude-adapter",
  level: parseLevel(process.env["MC_LOG_LEVEL"]),
  sink: runLogSink(),
});


const ALL_CREDENTIAL_KEYS = [
  "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
  "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY",
  "CODEX_REFRESH_TOKEN_URL_OVERRIDE", "OPENAI_API_KEY", "MC_CREDENTIAL_REFRESH_URL",
  "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
  "NODE_TLS_REJECT_UNAUTHORIZED", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "CODEX_CA_CERTIFICATE", "CULLER_PROXY_TOKEN",
  "CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY", "CLAUDE_CODE_CLIENT_KEY_PASSPHRASE", "CODEX_HOME",
];

// The SDK starts this program. The runtime holds the Linux arm64 build only.
export const CLAUDE_BIN = process.env["MC_CLAUDE_BIN"] ||
  `${process.env.MC_PRIVATE_BUN ? "/opt/culler-runtime/app" : "/app"}/node_modules/@anthropic-ai/claude-agent-sdk-linux-arm64/claude`;

/** The harness receives only the temporary Culler token. */
export function claudeAdapterEnv(
  channel: "claude",
  base: Record<string, string | undefined>,
  configDir: string,
  access: ProviderAccess,
  caPath: string,
): Record<string, string> {
  if (channel !== "claude" || access.harness !== "claude-sdk" || !caPath) throw new Error("The Claude proxy configuration is unavailable.");
  const env: Record<string, string> = {};
  for (const [name, value] of Object.entries(base)) {
    if (value !== undefined && !ALL_CREDENTIAL_KEYS.includes(name) && !name.includes("REFRESH_TOKEN") &&
        !/^(OPENAI_|ANTHROPIC_)/.test(name) && !/^CODEX_.*(?:BASE_URL|API_KEY|AUTH_TOKEN|CUSTOM_HEADERS)$/.test(name) &&
        !/^CLAUDE_CODE_USE_/.test(name) && !/^CLAUDE_LOCAL_OAUTH_/.test(name) &&
        !["CLAUDE_CODE_CUSTOM_OAUTH_URL", "CLAUDE_CODE_OAUTH_CLIENT_ID", "CLAUDE_CODE_API_KEY_HELPER_TTL_MS", "CLAUDE_CODE_CUSTOM_HEADERS", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"].includes(name)) {
      env[name] = value;
    }
  }
  env.CLAUDE_CONFIG_DIR = configDir;
  env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1";
  env.DISABLE_AUTOUPDATER = "1";
  env.IS_SANDBOX = "1";
  // The harness Bash tool truncates command output at BASH_MAX_OUTPUT_LENGTH
  // characters — and under the 30000-character default, long verbose gates
  // repeatedly lost their terminal exit status inside verifier projections
  // 150000 is not a taste choice: SDK 0.3.280 clamps any larger value back to 150000 with a
  // "Capped from" warning, so this IS the most generous ceiling the harness
  // honors, and a bigger number would pin a value that never takes effect.
  // MAX_OUTPUT_LENGTH is the un-prefixed name of the same ceiling. The
  // 0.3.280 binary consumes only the BASH_-prefixed one,
  // so it rides along inert today and catches a harness that reads it.
  env.BASH_MAX_OUTPUT_LENGTH = "150000";
  env.MAX_OUTPUT_LENGTH = "150000";
  env.ANTHROPIC_BASE_URL = access.url;
  env.NODE_EXTRA_CA_CERTS = caPath;
  env.CLAUDE_CODE_MAX_RETRIES = "0";
  if (access.authMode === "account") {
    env.CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST = "1";
    env.CLAUDE_CODE_OAUTH_TOKEN = access.token;
  } else {
    env.ANTHROPIC_AUTH_TOKEN = access.token;
  }
  return env;
}

export function claudeOptions(
  model: string,
  workspace: string,
  env: Record<string, string>,
  store: NativeSessionStore,
  nativeRef?: string,
  maxTurns?: number,
  openWiki = false,
  openWikiInputCommit?: string,
  effort?: Options["effort"],
  maxOutputTokens?: number,
): Options {
  return {
    cwd: workspace,
    pathToClaudeCodeExecutable: CLAUDE_BIN,
    env: { ...env, ...(maxOutputTokens === undefined ? {} : { CLAUDE_CODE_MAX_OUTPUT_TOKENS: String(maxOutputTokens) }) },
    ...(effort ? { effort } : {}),
    // An empty configured model means the SDK's own default.
    ...(model === "" ? {} : { model }),
    ...(nativeRef ? { resume: nativeRef } : {}),
    // The live acceptance bounds its provider requests with this cap. A
    // pipeline run carries no cap.
    ...(maxTurns === undefined ? {} : { maxTurns }),
    allowDangerouslySkipPermissions: true,
    permissionMode: "bypassPermissions",
    // "user" is the narrowest source that carries skills, and here it resolves
    // to a private per-run tmpdir this adapter creates and deletes (see
    // CLAUDE_CONFIG_DIR above), whose only contents are the skill set the
    // runner installed. It is NOT the operator's `~/.claude` and NOT the
    // model-writable workspace: "project" would re-enable `.claude/settings.json`,
    // CLAUDE.md, hooks and agents from /workspace/source, which the agent can
    // write — the model would be authoring its own configuration. Keep it "user".
    settingSources: ["user"],
    // The documented switch for the Skill tool; it also spares us adding
    // "Skill" to an allowlist by hand. "all" means every DISCOVERED skill, and
    // only this run's own set is ever installed: the skills Culler
    // ships for its role and the operator's skills for that role and Project.
    skills: "all",
    strictMcpConfig: true,
    mcpServers: openWiki ? { openwiki: openWikiServer(workspace, openWikiInputCommit) } : {},
    settings: { autoMemoryEnabled: false, autoDreamEnabled: false },
    systemPrompt: { type: "preset", preset: "claude_code" },
    tools: { type: "preset", preset: "claude_code" },
    // The container is the isolation boundary, as for the codex adapter. The
    // SDK sandbox sets no_new_privs, and the kernel then ignores the setuid
    // bit of mc, so no shell command could reach the spine.
    sandbox: { enabled: false },
    persistSession: true,
    sessionStore: store,
    sessionStoreFlush: "eager",
    includePartialMessages: true,
    forwardSubagentText: true,
    stderr: (data) => process.stderr.write(data),
  };
}

/** Normalize the SDK's final successful result. Intermediate assistant/tool
 * messages remain native events and are never submitted as the Homie reply. */
export function claudeReplyFromMessage(message: unknown): string | undefined {
  if (message === null || typeof message !== "object") return undefined;
  const result = message as { type?: unknown; is_error?: unknown; result?: unknown };
  if (result.type === "result" && result.is_error === false &&
    typeof result.result === "string") {
    return result.result;
  }
  return undefined;
}

/** Assistant text as it streams, for the runner's provisional draft. The
 * SDK emits text deltas because partial messages are on; only the top-level
 * assistant's deltas count, never a subagent's. A later assistant message in
 * the same turn starts a new paragraph. Returns the delta to append. */
export function makeClaudeTextExtractor(): (message: unknown) => string | undefined {
  let seen = false;
  let paragraph = false;
  return (message) => {
    if (message === null || typeof message !== "object") return undefined;
    const m = message as {
      type?: unknown;
      parent_tool_use_id?: unknown;
      event?: { type?: unknown; delta?: { type?: unknown; text?: unknown } };
    };
    if (m.type !== "stream_event" || typeof m.parent_tool_use_id === "string") return undefined;
    if (m.event?.type === "message_start") {
      paragraph = seen;
      return undefined;
    }
    const delta = m.event?.delta;
    if (m.event?.type !== "content_block_delta" || delta?.type !== "text_delta" ||
      typeof delta.text !== "string" || delta.text === "") {
      return undefined;
    }
    const text = paragraph ? `\n\n${delta.text}` : delta.text;
    seen = true;
    paragraph = false;
    return text;
  };
}

// A complete top-level response requires both its fresh start and its successful stop.
export function makeClaudeResponseObserver(model: string | undefined): (message: unknown) => boolean {
  let current = false;
  let complete = false;
  return (message) => {
    if (!message || typeof message !== "object") return false;
    const m = message as any;
    if (m.type !== "stream_event" || m.parent_tool_use_id != null) return false;
    const event = m.event;
    if (event?.type === "message_start") {
      current = typeof model === "string" && model !== "" && event.message?.model === model &&
        typeof event.message?.id === "string";
      complete = false;
    } else if (event?.type === "message_delta") {
      complete = current && ["end_turn", "tool_use", "stop_sequence", "max_tokens"].includes(event.delta?.stop_reason);
    } else if (event?.type === "message_stop") {
      const success = current && complete;
      current = complete = false;
      return success;
    } else if (event?.type === "error") {
      current = complete = false;
    }
    return false;
  };
}

/** Copy the current role skills into the private harness configuration directory.
 * The original files remain available at the path in the role directive. */
export async function mirrorRoleSkill(
  configDir: string,
  env: Record<string, string | undefined> = process.env,
): Promise<boolean> {
  const root = agentSkillRoot(env);
  if (root === undefined) return false;
  try {
    await cp(root, join(configDir, "skills"), { recursive: true });
    return true;
  } catch {
    return false;
  }
}

function arg(name: string): string | undefined {
  const i = process.argv.indexOf(name);
  return i >= 0 ? process.argv[i + 1] : undefined;
}

/** The optional `--max-turns` value: undefined when absent, null when it is
 * not a positive integer. */
export function parseMaxTurns(raw: string | undefined): number | undefined | null {
  if (raw === undefined) return undefined;
  if (!/^[1-9][0-9]*$/.test(raw)) return null;
  return Number(raw);
}

export async function directoryExists(path: string): Promise<boolean> {
  try {
    return (await stat(path)).isDirectory();
  } catch {
    return false;
  }
}

export async function executeClaudeQuery(
  prompt: string,
  options: Options,
  openWiki: boolean,
  dependencies: {
    query?: typeof query;
    emit?: (event: unknown) => void;
    signal?: AbortSignal;
    startupMs?: number;
    pollMs?: number;
    /** The endpoint proxy's observation of a request without an endpoint response. */
    networkError?: () => NetworkErrorClass | undefined;
  } = {},
): Promise<number> {
  const responseCompleted = makeClaudeResponseObserver(options.model);
  const output = dependencies.emit ?? ((event) => console.log(JSON.stringify(event)));
  const redact = await loadDiagnosticRedactor(options.env);
  const emit = (event: unknown) => output(redactDiagnosticEvent(event, redact));
  const abort = new AbortController();
  const startup = openWiki ? new OpenWikiStartup(dependencies.startupMs) : undefined;
  let stderr = "";
  let sessionSeen = false;
  let failure: string | undefined;
  let providerFailure = {};
  let providerMessage: string | undefined;
  let reply: string | undefined;
  const textFrom = makeClaudeTextExtractor();
  const stop = () => { startup?.cancel(); abort.abort(); };
  dependencies.signal?.addEventListener("abort", stop, { once: true });
  if (dependencies.signal?.aborted) stop();
  const streamOptions: Options = {
    ...options,
    abortController: abort,
    env: { ...options.env, ...(openWiki ? { MCP_TIMEOUT: String(OPENWIKI_STARTUP_MS) } : {}) },
    stderr: (data) => {
      stderr = limitDiagnostic(stderr + data, 32_768);
      options.stderr?.(redact(data));
    },
    ...(startup ? { hooks: {
      ...options.hooks,
      PreToolUse: [...(options.hooks?.PreToolUse ?? []), {
        matcher: "mcp__openwiki__.*",
        hooks: [async () => {
          await startup.ready;
          return {};
        }],
      }],
    } } : {}),
  };
  let stream: ReturnType<typeof query> | undefined;
  try {
    if (abort.signal.aborted) return 143;
    stream = (dependencies.query ?? query)({ prompt, options: streamOptions });
    const consume = async () => {
      for await (const message of stream!) {
        emit(message);
        if (responseCompleted(message)) emit({ event: "model-response" });
        if (!sessionSeen && message.type === "system" && message.subtype === "init") {
          if (typeof message.session_id === "string" && message.session_id !== "") {
            emit({ event: "session-start", session_id: message.session_id, native_file: MAIN_NATIVE_FILE });
            sessionSeen = true;
          }
          startup?.observe(message.mcp_servers ?? []);
        }
        // A retried request can still succeed, so only a final error describes the run.
        const retried = message.type === "system" && message.subtype === "api_retry";
        const facts = retried ? {} : providerFailureFields(message);
        if (facts.provider_error_code || facts.http_status) {
          providerFailure = { ...providerFailure, ...facts };
          failure = `The harness reported ${facts.provider_error_code ?? "a provider request failure"}.`;
        }
        if (message.type === "system" && message.subtype === "mirror_error") failure = JSON.stringify(message);
        if (message.type === "result" && message.is_error) {
          failure = JSON.stringify(message);
          providerFailure = { ...providerFailure, ...providerFailureFields(message) };
          // The provider text names the cause, for example a model that this harness version cannot use.
          const text = (message as { result?: unknown }).result;
          if (typeof text === "string" && text.trim() !== "") providerMessage = text.trim();
        }
        const text = textFrom(message);
        if (text !== undefined) emit({ event: "turn-text", text });
        const normalized = claudeReplyFromMessage(message);
        if (normalized !== undefined) reply = normalized;
      }
    };
    const poll = startup?.poll(() => stream!.mcpServerStatus(), dependencies.pollMs).catch((error) => {
      startup.fail("the status request failed", error instanceof Error ? error.message : String(error));
      throw error;
    });
    await Promise.all([consume(), poll]);
    if (!sessionSeen) throw new Error("The harness ended without a native session");
    if (failure) {
      const code = providerFailureFields(providerFailure).provider_error_code;
      if (!code) throw new Error(failure);
      throw new Error(`The harness reported ${code}.${providerMessage ? ` ${providerMessage}` : ""}`);
    }
    if (reply !== undefined) emit({ event: "turn-complete", status: "success", output: reply });
    return 0;
  } catch (error) {
    if (dependencies.signal?.aborted) return 143;
    const message = error instanceof Error ? error.message : String(error);
    const evidence = startup?.evidence(startup.outcome === "unknown" ? "the harness stream failed" : startup.outcome)
      ?? "Server: unknown\nLast connection status: unknown";
    emit({ ...adapterFailure(message, `${evidence}\nAdapter error: ${message}\nHarness stderr: ${stderr || "unknown"}`, redact),
      ...failureFacts({ ...providerFailure, ...providerFailureFields(error) }, message, dependencies.networkError?.()) });
    return 1;
  } finally {
    startup?.cancel();
    abort.abort();
    stream?.close();
    dependencies.signal?.removeEventListener("abort", stop);
  }
}

async function main(): Promise<number> {
  const channel = arg("--channel");
  const sessionDir = arg("--session-dir");
  const workspace = arg("--workspace");
  const nativeRef = arg("--native-ref");
  const model = arg("--model") ?? "";
  const maxTurns = parseMaxTurns(arg("--max-turns"));
  const effort = arg("--effort");
  const maxOutputTokens = parseMaxTurns(arg("--max-output-tokens"));
  const openWiki = process.argv.includes("--openwiki");
  const refuse = async (message: string) => {
    const failure = adapterFailure(message,
      `Server: ${openWiki ? "openwiki" : "unknown"}\nLast connection status: unknown\nOutcome: adapter launch failed`, await loadDiagnosticRedactor());
    console.log(JSON.stringify(failure));
    log.error("adapter.launch_failed", failure.condition);
    return 2;
  };
  if (channel !== "claude" || process.argv.includes("--base-url") || process.argv.includes("--test-upstream") || !sessionDir || !workspace || maxTurns === null ||
      effort !== undefined && !["low", "medium", "high", "xhigh", "max"].includes(effort) || maxOutputTokens === null ||
      maxOutputTokens !== undefined && (!Number.isSafeInteger(maxOutputTokens) || maxOutputTokens > 128_000)) {
    return refuse("claude adapter: invalid invocation");
  }
  // The workspace becomes the harness `cwd`. When it is absent the SDK still
  // spawns its binary, the spawn fails on the missing directory, and the error
  // it surfaces blames a libc mismatch on the binary itself — which sends the
  // reader after the image instead of after the mount. Name the real cause.
  if (!(await directoryExists(workspace))) {
    return refuse(`claude adapter: workspace ${JSON.stringify(workspace)} is not a directory in this container; nothing mounted it`);
  }
  const localRoot = await mkdtemp("/tmp/mc-claude-");
  const configDir = join(localRoot, "config");
  await mkdir(configDir, { mode: 0o700 });
  if (!(await mirrorRoleSkill(configDir))) {
    log.error("adapter.launch_failed", "claude adapter: no role skill was mirrored into the config dir");
  }
  let redact = await loadDiagnosticRedactor();
  try {
    const access = await loadProviderAccess("claude-sdk");
    await redactNativeAccess(sessionDir, access.token);
    const store = new NativeSessionStore(sessionDir, value => value.split(access.token).join("[redacted]"));
    const ca = await writeProviderCA(access, localRoot);
    const env = claudeAdapterEnv(channel, process.env, configDir, access, ca);
    redact = await loadDiagnosticRedactor(env);
    const abort = new AbortController();
    const stop = () => abort.abort();
    process.on("SIGTERM", stop);
    process.on("SIGINT", stop);
    try {
      return await executeClaudeQuery(await Bun.stdin.text(),
        claudeOptions(model, workspace, env, store, nativeRef, maxTurns,
          openWiki, arg("--openwiki-input-commit"), effort as Options["effort"], maxOutputTokens), openWiki, { signal: abort.signal });
    } finally {
      process.off("SIGTERM", stop);
      process.off("SIGINT", stop);
    }
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    const failure = { ...adapterFailure(message, `Server: ${openWiki ? "openwiki" : "unknown"}\nLast connection status: unknown\nOutcome: adapter launch failed`, redact),
      ...providerFailureFields(err),
      ...(isNetworkErrorClass((err as { network_error?: unknown })?.network_error) ? { network_error: (err as { network_error: NetworkErrorClass }).network_error } : {}),
    };
    console.log(JSON.stringify(failure));
    log.error("adapter.stream_error", failure.condition);
    return 1;
  } finally {
    await rm(localRoot, { recursive: true, force: true });
  }
}

if (import.meta.main) process.exit(await main());
