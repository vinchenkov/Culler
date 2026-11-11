#!/usr/bin/env bun
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { OPENCODE_DATABASE } from "../../shared/opencode-trace";
import { loadProviderAccess, writeProviderCA, type ProviderAccess } from "./provider-access";
import { adapterFailure, loadDiagnosticRedactor, redactDiagnosticEvent } from "../diagnostics";
import { openWikiServer, OPENWIKI_TOOLS } from "./openwiki";

export const OPENCODE_ROOT = "/opt/culler-opencode";

export function openCodeConfiguration(access: ProviderAccess, model: string, effort: string | undefined, skills: string, root = OPENCODE_ROOT, wiki?: { workspace: string; commit?: string }) {
  const selected = access.opencode;
  if (access.harness !== "opencode" || access.authMode !== "api-key" || !selected || selected.model.id !== model ||
      !selected.provider || !selected.keyVariable || !selected.model.package.startsWith("@opencode/ai/providers/") ||
      (effort && !selected.model.variants.includes(effort))) throw new Error("The OpenCode profile is invalid.");
  const selection = `${selected.provider}/${model}${effort ? `#${effort}` : ""}`;
  const server = wiki && openWikiServer(wiki.workspace, wiki.commit);
  return {
    model: selection, update: "disable", share: "disabled", warming: false, snapshots: false,
    shell: `${root}/control/shell`, lsp: false, formatter: false,
    skills: [skills],
    providers: { [selected.provider]: {
      settings: { baseURL: access.url, apiKey: access.token, maxRetries: 0 },
      models: { [model]: { package: selected.model.package, settings: { baseURL: access.url, apiKey: access.token } } },
    } },
    agents: {
      build: { model: selection, permissions: [{ action: "*", resource: "*", effect: "allow" }] },
      general: { model: selection }, explore: { model: selection },
      compaction: { model: selection }, title: { model: selection, disabled: true }, summary: { model: selection, disabled: true },
    },
    permissions: [{ action: "*", resource: "*", effect: "allow" }],
    policies: [
      { action: "provider.use", resource: "*", effect: "deny" },
      { action: "provider.use", resource: selected.provider, effect: "allow" },
      { action: "tool.use", resource: "question:*", effect: "deny" },
      ...(!selected.webSearch ? [{ action: "tool.use", resource: "websearch:*", effect: "deny" }] : []),
    ],
    websearch: selected.webSearch ? { provider: "native" } : false,
    plugins: [
      ...(selected.webSearch ? [`file://${root}/node_modules/opencode-websearch`] : []),
      `file://${root}/control`,
    ],
    ...(server ? { mcp: { servers: { openwiki: { type: "local", command: [server.command, ...server.args], codemode: false } } } } : {}),
  };
}

/** OpenCode reads the facts of a live model from the model record file. It makes no request for the live model list. */
export function openCodeEnvironment(base: Record<string, string | undefined>, config: unknown, directory: string, ca: string, record?: string) {
  const env: Record<string, string> = {};
  for (const [name, value] of Object.entries(base)) {
    if (value === undefined || /^(OPENCODE_|CULLER_OPENCODE_|CULLER_PROXY_|XDG_|CODEX_|CLAUDE_|ANTHROPIC_|OPENAI_)/.test(name) ||
        /(?:API_KEY|AUTH_TOKEN|ACCESS_TOKEN|REFRESH_TOKEN)$/.test(name) ||
        /^(HTTP_PROXY|HTTPS_PROXY|ALL_PROXY|http_proxy|https_proxy|all_proxy|NODE_OPTIONS|BUN_OPTIONS|NODE_TLS_REJECT_UNAUTHORIZED|NODE_EXTRA_CA_CERTS|SSL_CERT_FILE|MC_PROVIDER_ACCESS_FILE)$/.test(name)) continue;
    env[name] = value;
  }
  for (const part of ["DATA", "CONFIG", "CACHE", "STATE"]) env[`XDG_${part}_HOME`] = join(directory, part.toLowerCase());
  Object.assign(env, {
    OPENCODE_CONFIG_CONTENT: JSON.stringify(config), OPENCODE_CONFIG_DIR: join(directory, "config"),
    OPENCODE_DISABLE_PROJECT_CONFIG: "1", OPENCODE_DISABLE_CLAUDE_CODE: "1", OPENCODE_DISABLE_MODELS_FETCH: "1",
    OPENCODE_DISABLE_FREE_MODELS: "1", OPENCODE_DISABLE_AUTOUPDATE: "1", OPENCODE_DISABLE_FILEWATCHER: "1",
    NODE_EXTRA_CA_CERTS: ca, SSL_CERT_FILE: ca,
    ...(record ? { OPENCODE_MODELS_PATH: record } : {}),
  });
  return env;
}

export function openCodeEvent(value: any): { session?: string; text?: string; tool?: any; failure?: string; complete?: boolean } {
  const session = typeof value?.sessionID === "string" ? value.sessionID : undefined;
  switch (value?.type) {
    case "text": return { session, text: typeof value.part?.text === "string" ? value.part.text : undefined };
    case "tool_use": return { session, tool: value.part };
    case "error": return { session, failure: value.error?.data?.message ?? value.error?.message ?? JSON.stringify(value.error) };
    case "step_finish": return { session, complete: value.part?.reason === "stop" };
    default: return { session };
  }
}

async function main(): Promise<number> {
  const arg = (flag: string) => { const at = process.argv.indexOf(flag); return at < 0 ? undefined : process.argv[at + 1]; };
  const directory = arg("--session-dir"), workspace = arg("--workspace"), model = arg("--model");
  const redact = await loadDiagnosticRedactor();
  const emit = (event: unknown) => console.log(JSON.stringify(redactDiagnosticEvent(event, redact)));
  if (!directory || !workspace || !model) { emit(adapterFailure("The OpenCode invocation is invalid.", "", redact)); return 2; }
  const root = join(directory, "opencode");
  await mkdir(root, { recursive: true, mode: 0o700 });
  const access = await loadProviderAccess("opencode");
  const safe = (text: string) => redact(text.split(access.token).join("[REDACTED]"));
  const caDirectory = join(root, "trust");
  await mkdir(caDirectory, { recursive: true });
  await rm(join(caDirectory, "provider-ca.pem"), { force: true });
  const ca = await writeProviderCA(access, caDirectory);
  const config = openCodeConfiguration(access, model, arg("--effort"), join(process.env.HOME ?? "/home/agent", ".agents/skills"),
    process.env.MC_OPENCODE_ROOT ?? OPENCODE_ROOT,
    process.argv.includes("--openwiki") ? { workspace, commit: arg("--openwiki-input-commit") } : undefined);
  const record = join(root, "model-record.json");
  await rm(record, { force: true });
  if (access.opencode!.model.record !== undefined) await writeFile(record, access.opencode!.model.record, { mode: 0o400, flag: "wx" });
  const env = openCodeEnvironment(process.env, config, root, ca, access.opencode!.model.record === undefined ? undefined : record);
  const sessionFile = join(root, "native-session");
  await rm(sessionFile, { force: true });
  await rm(`${sessionFile}.tmp`, { force: true });
  env.CULLER_OPENCODE_SESSION_FILE = sessionFile;
  env.CULLER_OPENCODE_WEBSEARCH = access.opencode!.webSearch ? "1" : "0";
  if (process.argv.includes("--openwiki")) env.CULLER_OPENCODE_OPENWIKI_TOOLS = JSON.stringify(OPENWIKI_TOOLS);
  env[access.opencode!.keyVariable] = access.token;
  env.PWD = workspace;
  const native = arg("--native-ref");
  const argv = [process.env.MC_OPENCODE_BIN ?? `${OPENCODE_ROOT}/node_modules/@opencode/cli-linux-arm64/bin/opencode`,
    "run", "--standalone", "--auto", "--format", "json", "--title", "Culler agent", ...(native ? ["--session", native] : [])];
  const prompt = await Bun.stdin.text();
  let session: string | undefined, reply: string | undefined, failed = false, complete = false;
  const proc = Bun.spawn(argv, { cwd: workspace, env, stdin: new TextEncoder().encode(prompt), stdout: "pipe", stderr: "pipe" });
  const stop = () => proc.kill("SIGTERM");
  process.on("SIGTERM", stop); process.on("SIGINT", stop);
  const stderr = new Response(proc.stderr).text();
  let registration: Promise<void> | undefined;
  function registerSession(id: string): Promise<void> {
    if (registration) return registration;
    if (!/^ses_[A-Za-z0-9_-]+$/.test(id) || (native && native !== id)) {
      return Promise.reject(new Error("The OpenCode session identity is invalid."));
    }
    session = id;
    registration = (async () => {
      emit({ event: "session-start", session_id: id, native_file: OPENCODE_DATABASE });
    })();
    return registration;
  }
  let observeSession = true;
  let sessionError: unknown;
  // The native JSON stream starts after the first response. The prompt hook supplies the identity before that request.
  const sessionObserver = (async () => {
    while (observeSession && !session) {
      try { await registerSession(await readFile(sessionFile, "utf8")); }
      catch (error) {
        if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
      }
      if (!session) await Bun.sleep(50);
    }
  })().catch(error => { sessionError = error; proc.kill("SIGTERM"); });
  try {
    let buffer = "";
    const decoder = new TextDecoder();
    for await (const chunk of proc.stdout) {
      buffer += decoder.decode(chunk, { stream: true });
      for (let at = buffer.indexOf("\n"); at >= 0; at = buffer.indexOf("\n")) {
        const line = buffer.slice(0, at); buffer = buffer.slice(at + 1);
        let value; try { value = JSON.parse(line); } catch { continue; }
        emit(redactDiagnosticEvent(value, safe));
        const event = openCodeEvent(value);
        if (event.complete) complete = true;
        if (event.session) await registerSession(event.session);
        if (event.failure) {
          failed = true;
          const status = value.error?.data?.statusCode ?? value.error?.statusCode ?? value.error?.status;
          emit({ ...adapterFailure(`The OpenCode request failed${Number.isInteger(status) ? ` with HTTP ${status}` : ""}.`, safe(JSON.stringify(value.error)), redact),
            ...(Number.isInteger(status) && status >= 400 && status <= 599 ? { http_status: status } : {}) });
        }
        if (event.text !== undefined) {
          reply = event.text;
          emit({ event: "model-response" }); emit({ event: "turn-text", text: safe(event.text) });
        }
        if (event.tool) {
          emit({ event: "model-response" });
        }
      }
    }
    const code = await proc.exited;
    observeSession = false;
    await sessionObserver;
    if (sessionError) throw sessionError;
    const error = safe(await stderr);
    if (code !== 0 || failed || !session || reply === undefined || !complete) {
      if (!failed) emit(adapterFailure("The OpenCode run ended without a complete reply.", error, redact));
      return code || 2;
    }
    emit({ event: "turn-complete", status: "success", output: safe(reply) });
    return 0;
  } finally {
    observeSession = false;
    await sessionObserver;
    if (proc.exitCode === null) { proc.kill("SIGTERM"); await proc.exited; }
    process.off("SIGTERM", stop); process.off("SIGINT", stop);
    await rm(caDirectory, { recursive: true, force: true });
    await rm(record, { force: true });
  }
}

if (import.meta.main) {
  try { process.exit(await main()); }
  catch (error) { console.log(JSON.stringify(adapterFailure("The OpenCode adapter failed.", String(error), await loadDiagnosticRedactor()))); process.exit(2); }
}
