#!/usr/bin/env bun
import { loadProviderAccess, writeProviderCA, codexProviderArgs, type ProviderAccess } from "./provider-access";
import { open, readdir, stat, mkdtemp, symlink, rm } from "node:fs/promises";
import { join, relative } from "node:path";
import { makeLogger, parseLevel } from "../../shared/log";
import { runLogSink } from "../../shared/log-sink";
import { openWikiCodexConfig } from "./openwiki";
import { redactDiagnosticEvent, providerFailureFields, adapterFailure, failureFacts, limitDiagnostic, loadDiagnosticRedactor } from "../diagnostics";

// The adapter is a separate process spawned by the runner with `stderr:
// "inherit"`, so before this every line below went to the container's stderr
// and died with the `--rm` container. It writes to the same per-run file the
// runner does — one write(2) per record on an O_APPEND fd, which is what lets
// two processes share the file without interleaving — and falls back to stderr
// when the resident did not compose the bind.
const log = makeLogger({
  comp: "codex-adapter",
  level: parseLevel(process.env["MC_LOG_LEVEL"]),
  sink: runLogSink(),
});

const FORBIDDEN = new Set([
  "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
  "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY",
  "OPENAI_API_KEY", "MC_CREDENTIAL_REFRESH_URL", "CODEX_REFRESH_TOKEN_URL_OVERRIDE",
  "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
  "NODE_TLS_REJECT_UNAUTHORIZED", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "CODEX_CA_CERTIFICATE", "CULLER_PROXY_TOKEN",
  "CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY", "CLAUDE_CODE_CLIENT_KEY_PASSPHRASE", "CODEX_HOME",
]);
export const CODEX_BIN = process.env["MC_CODEX_BIN"] ||
  `${process.env.MC_PRIVATE_BUN ? "/opt/culler-runtime/app" : "/app"}/node_modules/@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/bin/codex`;

export function codexAdapterEnv(
  base: Record<string, string | undefined>, access: ProviderAccess, caPath: string, home: string,
): Record<string, string> {
  const env: Record<string, string> = {};
  for (const [name, value] of Object.entries(base)) {
    if (value !== undefined && !FORBIDDEN.has(name) && !name.includes("REFRESH_TOKEN") &&
        !/^(OPENAI_|ANTHROPIC_)/.test(name) && !/^CODEX_.*(?:BASE_URL|API_KEY|AUTH_TOKEN|CUSTOM_HEADERS)$/.test(name) &&
        !/^CLAUDE_CODE_USE_/.test(name) && !/^CLAUDE_LOCAL_OAUTH_/.test(name) &&
        !["CLAUDE_CODE_CUSTOM_OAUTH_URL", "CLAUDE_CODE_OAUTH_CLIENT_ID", "CLAUDE_CODE_API_KEY_HELPER_TTL_MS", "CLAUDE_CODE_CUSTOM_HEADERS", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"].includes(name)) {
      env[name] = value;
    }
  }
  if (access.harness !== "codex" || !home || !caPath) throw new Error("The Codex proxy configuration is unavailable.");
  env.CODEX_HOME = home;
  env.CODEX_CA_CERTIFICATE = caPath;
  env.CULLER_PROXY_TOKEN = access.token;
  return env;
}

export function codexArgv(
  mode: "fresh" | "native",
  nativeRef?: string,
  model = "",
  openWikiWorkspace?: string,
  openWikiInputCommit?: string,
  effort?: string,
): string[] {
  const fixed = [
    // The operator's configured model; empty means the CLI default.
    ...(model === "" ? [] : ["--model", model]),
    "--json", "--strict-config",
    ...(effort ? ["--config", `model_reasoning_effort="${effort}"`] : []),
    // Approval is a config key, not an `exec` flag: codex 0.156.1 accepts
    // --ask-for-approval only before the subcommand and rejects it here. Under
    // --strict-config an unknown key is a hard error, so a future rename fails
    // loudly instead of silently reverting to interactive approval.
    "--config", 'approval_policy="never"',
    // The container supplies the isolation boundary. The inner sandbox blocks the protected mc command.
    "--config", 'sandbox_mode="danger-full-access"',
    "--config", "shell_environment_policy.ignore_default_excludes=false",
    "--config", 'shell_environment_policy.exclude=["CULLER_PROXY_TOKEN"]',
    // Codex reads no env var for shell-tool output; tool_output_token_limit is
    // the config key that governs exec-output truncation (unit: tokens, about
    // four bytes each; the exit status survives a truncated output). Under the
    // model-default budget, long verbose gates are exactly what trips
    // truncation, so pin a generous ceiling: 100000 tokens is roughly 400KB of text — an
    // order of magnitude above a verbose full-suite run — while one tool
    // result still leaves the rest of the context window usable.
    // --strict-config turns a future rename of the key into a hard error
    // rather than a silent fall-back to the model default.
    "--config", "tool_output_token_limit=100000",
    "--config", "memories.generate_memories=false",
    "--config", "memories.use_memories=false",
    ...(openWikiWorkspace === undefined ? [] : openWikiCodexConfig(openWikiWorkspace, openWikiInputCommit)),
    "--skip-git-repo-check", "--ignore-user-config", "--ignore-rules",
  ];
  if (mode === "native") {
    if (!nativeRef) throw new Error("Codex native resume requires a thread id");
    return [CODEX_BIN, "exec", "resume", ...fixed, nativeRef, "-"];
  }
  return [CODEX_BIN, "exec", ...fixed, "-"];
}

/** Normalize the provider-specific final assistant item. Tool output and other
 * stream items are never conversation replies. */
export function codexReplyFromEvent(line: string): string | undefined {
  let event: {
    type?: unknown;
    item?: { type?: unknown; text?: unknown };
  };
  try {
    event = JSON.parse(line) as typeof event;
  } catch {
    return undefined;
  }
  if (event.type === "item.completed" &&
    event.item?.type === "agent_message" &&
    typeof event.item.text === "string") {
    return event.item.text;
  }
  return undefined;
}

/** Assistant text as it arrives, for the runner's provisional draft. Codex
 * emits each agent message whole when it completes; a turn with tool calls
 * carries several before the final one. A later message starts a new
 * paragraph. Returns the text to append. */
export function makeCodexTextExtractor(): (line: string) => string | undefined {
  let seen = false;
  return (line) => {
    const text = codexReplyFromEvent(line);
    if (text === undefined || text === "") return undefined;
    const out = seen ? `\n\n${text}` : text;
    seen = true;
    return out;
  };
}

async function walk(dir: string): Promise<string[]> {
  const out: string[] = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...await walk(path));
    else if (entry.isFile() && entry.name.endsWith(".jsonl")) out.push(path);
  }
  return out;
}

export async function findCodexNativeFile(sessionDir: string, threadId: string, timeoutMs = 5_000): Promise<string> {
  if (!/^[0-9a-f-]{32,64}$/i.test(threadId)) throw new Error("Codex emitted an invalid thread id");
  const deadline = Date.now() + timeoutMs;
  while (true) {
    let files: string[] = [];
    try {
      files = await walk(sessionDir);
    } catch (err) {
      if (!(err !== null && typeof err === "object" && "code" in err && err.code === "ENOENT")) throw err;
    }
    const matches = files.filter((path) => path.includes(threadId));
    if (matches.length === 1) {
      const locator = relative(sessionDir, matches[0]!);
      if (locator.startsWith("..") || locator.startsWith("/")) throw new Error("Codex native trace escaped the session root");
      return locator;
    }
    if (matches.length > 1) throw new Error("Codex produced ambiguous native traces for one thread");
    if (Date.now() >= deadline) throw new Error("Codex native trace did not appear in the durable session root");
    await Bun.sleep(25);
  }
}

// Usage records describe complete upstream responses. Ordinary token-count events can replay old totals.
export function makeCodexResponseObserver(thread: string, model: string): (line: string) => boolean {
  let turn: string | undefined;
  let responseItem = false;
  const seen = new Set<string>();
  return line => {
    let event: any;
    try { event = JSON.parse(line); } catch { return false; }
    const p = event.payload;
    if (event.type === "turn_context") {
      turn = p?.model === model && (!p.root_turn_id || p.root_turn_id === p.turn_id) && typeof p.turn_id === "string" ? p.turn_id : undefined;
      responseItem = false;
    } else if (event.type === "response_item") {
      responseItem ||= ["function_call", "custom_tool_call", "local_shell_call", "tool_search_call", "agent_message"].includes(p?.type) ||
        (p?.type === "message" && p.role === "assistant");
    } else if (event.type === "compacted") {
      responseItem = false;
    } else if (event.type === "token_usage_record" && turn && responseItem &&
        p?.thread_id === thread && p.turn_id === turn && p.root_turn_id === turn &&
        typeof p.response_id === "string" && p.response_id !== "" && !seen.has(p.response_id)) {
      seen.add(p.response_id);
      responseItem = false;
      return true;
    }
    return false;
  };
}

export async function codexTraceOffsets(sessionDir: string): Promise<Map<string, number>> {
  const offsets = new Map<string, number>();
  for (const path of await walk(sessionDir)) offsets.set(relative(sessionDir, path), (await stat(path)).size);
  return offsets;
}

export function codexResponseWatcher(path: string, offset: number, observe: (line: string) => boolean, completed: () => void) {
  let buffer = "";
  const decoder = new TextDecoder();
  let pending: Promise<void> = Promise.resolve();
  let stopped = false;
  let failed = false;
  const read = () => {
    if (failed) return pending;
    pending = pending.then(async () => {
      const file = await open(path, "r");
      try {
        const bytes = new Uint8Array(64 * 1024);
        while (true) {
          const { bytesRead } = await file.read(bytes, 0, bytes.length, offset);
          if (!bytesRead) break;
          offset += bytesRead;
          buffer += decoder.decode(bytes.subarray(0, bytesRead), { stream: true });
          for (let end = buffer.indexOf("\n"); end >= 0; end = buffer.indexOf("\n")) {
            if (observe(buffer.slice(0, end))) completed();
            buffer = buffer.slice(end + 1);
          }
          // An oversized partial record supplies no recovery evidence.
          if (buffer.length > 8 * 1024 * 1024) throw new Error("The response record exceeds the read limit.");
        }
      } finally { await file.close(); }
    }).catch(() => { failed = true; });
    return pending;
  };
  const poll = async () => { await read(); if (!stopped) timer = setTimeout(poll, 100); };
  let timer = setTimeout(poll, 0);
  return { async stop() { stopped = true; clearTimeout(timer); await read(); } };
}


export async function directoryExists(path: string): Promise<boolean> {
  try {
    return (await stat(path)).isDirectory();
  } catch {
    return false;
  }
}

export async function executeCodexProcess(
  proc: ReturnType<typeof Bun.spawn>,
  sessionDir: string,
  openWiki: boolean,
  dependencies: { emit?: (event: unknown) => void; signal?: AbortSignal; model?: string; offsets?: Map<string, number>; env?: Record<string, string | undefined> } = {},
): Promise<number> {
  const env = dependencies.env ?? process.env;
  const secrets = new Set<string>();
  let redact = await loadDiagnosticRedactor(env, secrets);
  const output = dependencies.emit ?? ((event) => console.log(JSON.stringify(event)));
  const emit = (event: unknown) => output(redactDiagnosticEvent(event, redact));
  let stderr = "";
  let diagnosis = "";
  let providerFailure = {};
  let status = "unknown";
  let session: Promise<void> | undefined;
  let sessionError: unknown;
  let responses: ReturnType<typeof codexResponseWatcher> | undefined;
  let modelChanged = false;
  let reply: string | undefined;
  const textFrom = makeCodexTextExtractor();
  const stop = () => proc.kill("SIGTERM");
  dependencies.signal?.addEventListener("abort", stop, { once: true });
  if (dependencies.signal?.aborted) stop();
  const readStderr = async () => {
    const decoder = new TextDecoder();
    for await (const chunk of proc.stderr as ReadableStream<Uint8Array>) {
      stderr = limitDiagnostic(stderr + decoder.decode(chunk, { stream: true }), 32_768);
    }
    stderr += decoder.decode();
  };
  const onLine = async (line: string) => {
    let event: any;
    try { event = JSON.parse(line); } catch { return; }
    redact = await loadDiagnosticRedactor(env, secrets);
    emit(event);
    if (event.item?.type === "error" && typeof event.item.message === "string" && event.item.message.startsWith("model rerouted:")) modelChanged = true;
    const text = textFrom(line);
    if (text !== undefined) emit({ event: "turn-text", text });
    const normalized = codexReplyFromEvent(line);
    if (normalized !== undefined) reply = normalized;
    if (event.type === "error" || event.type === "turn.failed") {
      providerFailure = { ...providerFailure, ...providerFailureFields(event.error ?? event) };
      const message = event.error?.message ?? event.message;
      if (typeof message === "string") diagnosis = limitDiagnostic(diagnosis + "\n" + message, 16_384);
    }
    if (event.item?.type === "mcp_tool_call" && event.item.server === "openwiki") status = "connected";
    if (!session && event.type === "thread.started" && typeof event.thread_id === "string") {
      // The stream continues while the durable session file becomes available.
      session = findCodexNativeFile(sessionDir, event.thread_id).then((file) => {
        emit({ event: "session-start", session_id: event.thread_id, native_file: file });
        if (dependencies.model && dependencies.offsets) {
          responses = codexResponseWatcher(join(sessionDir, file), dependencies.offsets.get(file) ?? 0,
            makeCodexResponseObserver(event.thread_id, dependencies.model),
            () => { if (!modelChanged) emit({ event: "model-response" }); });
        }
      }).catch((error) => { sessionError = error; proc.kill(); });
    }
  };
  try {
    const readStdout = async () => {
      const decoder = new TextDecoder();
      let buffer = "";
      for await (const chunk of proc.stdout as ReadableStream<Uint8Array>) {
        buffer += decoder.decode(chunk, { stream: true });
        for (let newline = buffer.indexOf("\n"); newline >= 0; newline = buffer.indexOf("\n")) {
          await onLine(buffer.slice(0, newline));
          buffer = buffer.slice(newline + 1);
        }
      }
      buffer += decoder.decode();
      if (buffer) await onLine(buffer);
    };
    const [code] = await Promise.all([proc.exited, readStdout(), readStderr()]);
    await session;
    await responses?.stop();
    redact = await loadDiagnosticRedactor(env, secrets);
    if (dependencies.signal?.aborted) return 143;
    if (sessionError) throw sessionError;
    if (code !== 0 || diagnosis || !session) {
      const condition = diagnosis.trim() || (openWiki ? `OpenWiki harness exited ${code}` : `The harness exited ${code}`);
      emit({ ...adapterFailure(condition,
        `Server: ${openWiki ? "openwiki" : "unknown"}\nLast connection status: ${status}\n` +
        `Outcome: process exit ${code}\nHarness error: ${diagnosis.trim() || "unknown"}\nHarness stderr: ${stderr || "unknown"}`, redact),
        ...failureFacts(providerFailure, diagnosis) });
      return code || 1;
    }
    if (reply !== undefined) emit({ event: "turn-complete", status: "success", output: reply });
    return code;
  } catch (error) {
    proc.kill();
    await proc.exited;
    redact = await loadDiagnosticRedactor(env, secrets);
    if (dependencies.signal?.aborted) return 143;
    const message = error instanceof Error ? error.message : String(error);
    emit({ ...adapterFailure(message,
      `Server: ${openWiki ? "openwiki" : "unknown"}\nLast connection status: ${status}\n` +
      `Outcome: the adapter failed\nHarness error: ${diagnosis || "unknown"}\nHarness stderr: ${stderr || "unknown"}`, redact),
      ...providerFailure, ...providerFailureFields(error) });
    return 2;
  } finally {
    await responses?.stop();
    dependencies.signal?.removeEventListener("abort", stop);
  }
}

export async function runCodexAdapter(args: string[] = process.argv, dependencies: { input?: () => Promise<string>; spawn?: typeof Bun.spawn } = {}): Promise<number> {
  const arg = (flag: string) => { const index = args.indexOf(flag); return index < 0 ? undefined : args[index + 1]; };
  const sessionDir = arg("--session-dir");
  const workspace = arg("--workspace");
  const mode = (arg("--mode") ?? "fresh") as "fresh" | "native";
  const nativeRef = arg("--native-ref");
  const model = arg("--model") ?? "";
  const effort = arg("--effort");
  const refuse = async (message: string) => {
    const failure = adapterFailure(message,
      `Server: ${args.includes("--openwiki") ? "openwiki" : "unknown"}\nLast connection status: unknown\nOutcome: adapter launch failed`, await loadDiagnosticRedactor());
    console.log(JSON.stringify(failure));
    log.error("adapter.launch_failed", failure.condition);
    return 2;
  };
  if ((args.includes("--base-url") || args.includes("--test-upstream")) || !sessionDir || !workspace || (mode !== "fresh" && mode !== "native") ||
      effort !== undefined && !["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"].includes(effort)) {
    return refuse("codex adapter: invalid invocation");
  }
  // Same reason as the claude adapter: an absent cwd fails the harness spawn
  // with an error that points at the binary rather than at the missing mount.
  if (!(await directoryExists(workspace))) {
    return refuse(`codex adapter: workspace ${JSON.stringify(workspace)} is not a directory in this container; nothing mounted it`);
  }
  let env: Record<string, string>;
  let argv: string[];
  const localRoot = await mkdtemp("/tmp/mc-codex-");
  try {
    const access = await loadProviderAccess("codex");
    const ca = await writeProviderCA(access, localRoot);
    await symlink(sessionDir, join(localRoot, "sessions"));
    env = codexAdapterEnv(process.env, access, ca, localRoot);
    argv = codexArgv(mode, nativeRef, model,
      args.includes("--openwiki") ? workspace : undefined, arg("--openwiki-input-commit"),
      effort);
    argv.splice(argv.length - 1, 0, ...codexProviderArgs(access));
  } catch (err) {
    const failure = adapterFailure(err instanceof Error ? err.message : String(err),
      `Server: ${args.includes("--openwiki") ? "openwiki" : "unknown"}\nLast connection status: unknown\nOutcome: adapter launch failed`, await loadDiagnosticRedactor());
    console.log(JSON.stringify(failure));
    log.error("adapter.stream_error", failure.condition);
    await rm(localRoot, { recursive: true, force: true });
    return 2;
  }
  try {
    const abort = new AbortController();
    const stop = () => abort.abort();
    process.on("SIGTERM", stop);
    process.on("SIGINT", stop);
    try {
      // The byte boundary excludes responses from a resumed attempt.
      let offsets: Map<string, number> | undefined;
      try { offsets = await codexTraceOffsets(sessionDir); } catch {}
      const proc = (dependencies.spawn ?? Bun.spawn)(argv, {
        cwd: workspace, env,
        stdin: new TextEncoder().encode(await (dependencies.input ?? (() => Bun.stdin.text()))()),
        stdout: "pipe", stderr: "pipe",
      });
      return await executeCodexProcess(proc, sessionDir, args.includes("--openwiki"), { signal: abort.signal, model, offsets, env });
    } finally {
      process.off("SIGTERM", stop);
      process.off("SIGINT", stop);
    }
  } catch (error) {
    console.log(JSON.stringify(adapterFailure(error instanceof Error ? error.message : String(error),
      `Server: ${args.includes("--openwiki") ? "openwiki" : "unknown"}\nLast connection status: unknown\nOutcome: adapter launch failed`, await loadDiagnosticRedactor())));
    return 2;
  } finally {
    await rm(localRoot, { recursive: true, force: true });
  }
}

if (import.meta.main) process.exit(await runCodexAdapter());
