import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { randomBytes, randomUUID } from "node:crypto";
import { parseProviderAccess, type ProviderAccess } from "../../runner/agent-runner/adapters/provider-access";

const source = resolve(import.meta.dir, "../..");
const verifiedImages = new Set<string>();
const event = (type: string, value: object) => `event: ${type}\ndata: ${JSON.stringify({ type, ...value })}\n\n`;

export function syntheticFinalResponse(harness: string, body: unknown, text = "CULLER_REPAIR_OK", phase?: "commentary" | "final"): Response {
  const model = (body as any)?.model;
  if (typeof model !== "string" || !model) throw new Error("The synthetic response requires the requested model.");
  const id = randomUUID();
  let stream: string;
  if (harness === "codex") {
    const message = { id: `msg_${id}`, type: "message", role: "assistant", status: "completed", ...(phase ? { phase } : {}),
      content: [{ type: "output_text", text, annotations: [] }] };
    const response = { id: `resp_${id}`, object: "response", model, status: "completed", output: [message],
      usage: { input_tokens: 20, output_tokens: 10, total_tokens: 30,
        input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 } } };
    stream = event("response.created", { response: { ...response, status: "in_progress", output: [] } })
      + event("response.output_item.added", { output_index: 0, item: { ...message, content: [] } })
      + event("response.output_text.delta", { item_id: message.id, output_index: 0, content_index: 0, delta: text })
      + event("response.output_item.done", { output_index: 0, item: message })
      + event("response.completed", { response });
  } else if (harness === "claude-sdk") {
    stream = event("message_start", { message: { id: `msg_${id}`, type: "message", role: "assistant", content: [], model,
      stop_reason: null, stop_sequence: null, usage: { input_tokens: 20, output_tokens: 0 } } })
      + event("content_block_start", { index: 0, content_block: { type: "text", text: "" } })
      + event("content_block_delta", { index: 0, delta: { type: "text_delta", text } })
      + event("content_block_stop", { index: 0 })
      + event("message_delta", { delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 10 } })
      + event("message_stop", {});
  } else throw new Error("The synthetic response requires a supported harness.");
  return new Response(stream, { headers: { "content-type": "text/event-stream" } });
}

export function syntheticModelTestResponse(harness: string, body: any): Response {
  const marker = "MC_LIVE_PROVIDER_TOOL_OK";
  const returned = harness === "codex"
    ? body.input?.some((item: any) => item.type === "function_call_output" && JSON.stringify(item.output).includes(marker))
    : body.messages?.some((message: any) => Array.isArray(message.content) && message.content.some((item: any) =>
      item.type === "tool_result" && JSON.stringify(item.content).includes(marker)));
  if (returned) return syntheticFinalResponse(harness, body, "MC_LIVE_PROVIDER_TURN_OK");
  const shell = "printf 'MC_LIVE_PROVIDER_TOOL_OK' | tee mc-live-provider-tool.txt";
  const tool = harness === "codex" ? body.tools?.find((item: any) => ["exec_command", "shell", "shell_command"].includes(item.name))
    : body.tools?.find((item: any) => item.name === "Bash");
  if (!tool) throw new Error("The model test requires the native shell tool.");
  let stream: string;
  const id = randomUUID();
  if (harness === "codex") {
    const properties = tool.parameters?.properties ?? {};
    const args = properties.cmd ? { cmd: shell, max_output_tokens: 100 }
      : properties.command?.type === "array" ? { command: ["sh", "-c", shell] } : { command: shell };
    const action = { id: `fc_${id}`, type: "function_call", call_id: `call_${id}`, name: tool.name,
      arguments: JSON.stringify(args), status: "completed" };
    const response = { id: `resp_${id}`, object: "response", model: body.model, status: "completed", output: [action],
      usage: { input_tokens: 20, output_tokens: 10, total_tokens: 30,
        input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 } } };
    stream = event("response.created", { response: { ...response, status: "in_progress", output: [] } })
      + event("response.output_item.added", { output_index: 0, item: { ...action, arguments: "" } })
      + event("response.function_call_arguments.delta", { item_id: action.id, output_index: 0, delta: action.arguments })
      + event("response.output_item.done", { output_index: 0, item: action })
      + event("response.completed", { response });
  } else {
    stream = event("message_start", { message: { id: `msg_${id}`, type: "message", role: "assistant", content: [], model: body.model,
      stop_reason: null, stop_sequence: null, usage: { input_tokens: 20, output_tokens: 0 } } })
      + event("content_block_start", { index: 0, content_block: { type: "tool_use", id: `tool_${id}`, name: "Bash", input: {} } })
      + event("content_block_delta", { index: 0, delta: { type: "input_json_delta",
        partial_json: JSON.stringify({ command: shell, description: "Verify the model test artifact" }) } })
      + event("content_block_stop", { index: 0 })
      + event("message_delta", { delta: { stop_reason: "tool_use", stop_sequence: null }, usage: { output_tokens: 10 } })
      + event("message_stop", {});
  }
  return new Response(stream, { headers: { "content-type": "text/event-stream" } });
}

async function command(args: string[]): Promise<string> {
  const child = Bun.spawn(args, { stdin: "ignore", stdout: "pipe", stderr: "pipe" });
  const [code, stdout, stderr] = await Promise.all([child.exited,
    new Response(child.stdout).text(), new Response(child.stderr).text()]);
  if (code) throw new Error(`The synthetic fixture command failed: ${args[0]}: ${stderr.slice(0, 4096)}`);
  return stdout;
}

export interface NativeHarnessOptions {
  image: string;
  access: ProviderAccess;
  model: string;
  prompt: string;
  nativeReference?: string;
  sessionDirectory?: string;
  workspaceDirectory?: string;
  runtimeDirectory?: string;
  adapterArgs?: string[];
  containerArgs?: string[];
  maxTurns?: number;
  onEvent?: (event: Record<string, any>) => void;
}

export interface NativeHarnessResult {
  exitCode: number;
  events: Record<string, any>[];
  output?: string;
  nativeReference?: string;
  failure?: Record<string, any>;
  stderr: string;
}

export async function runNativeHarness(options: NativeHarnessOptions): Promise<NativeHarnessResult> {
  const access = parseProviderAccess(JSON.stringify(options.access), options.access.harness);
  const root = await mkdtemp(join(tmpdir(), "culler-native-harness-"));
  const name = `culler-proxy-harness-${randomBytes(8).toString("hex")}`;
  let child: ReturnType<typeof Bun.spawn> | undefined;
  try {
    if (!verifiedImages.has(options.image)) {
      const pins = JSON.parse(await readFile(join(source, "runner/agent-runner/package.json"), "utf8")).dependencies;
      const actual = await command(["docker", "run", "--rm", "--network", "none", "--user", "10002:10002", "--cap-drop", "ALL",
        ...(options.containerArgs ?? []),
      "--entrypoint", "/opt/culler-runtime/bin/bun", options.image, "-e",
        `console.log(JSON.stringify(Object.fromEntries(${JSON.stringify(Object.keys(pins))}.map(name => [name, require('/opt/culler-runtime/app/node_modules/' + name + '/package.json').version]))))`]);
      if (JSON.stringify(JSON.parse(actual)) !== JSON.stringify(pins)) throw new Error("The test image has different harness versions.");
      verifiedImages.add(options.image);
    }
    const runtime = options.runtimeDirectory ?? join(root, "runtime");
    if (!options.runtimeDirectory) await command(["python3", join(source, "runner/image/stage-runtime.py"), runtime]);
    const session = options.sessionDirectory ?? join(root, "session");
    const workspace = options.workspaceDirectory ?? join(root, "workspace");
    for (const path of [session, workspace]) {
      await mkdir(path, { recursive: true, mode: 0o777 });
      await chmod(path, 0o777);
    }
    const path = join(root, "access.json");
    await writeFile(path, JSON.stringify(access), { mode: 0o644 });
    const args = ["docker", "run", "--rm", "--interactive", "--name", name, "--init", "--user", "10002:10002", "--cap-drop", "ALL",
      ...(process.platform === "linux" ? ["--network", "host"] : []),
      "--mount", `type=bind,src=${runtime},dst=/opt/culler-runtime/app/src,readonly`,
      "--mount", `type=bind,src=${path},dst=/mc/provider-access.json,readonly`,
      "--mount", `type=bind,src=${session},dst=/mc/session`,
      "--mount", `type=bind,src=${workspace},dst=/workspace/source`,
      "-e", "HOME=/home/mc-model", "-e", "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json",
      "-e", "MC_PRIVATE_BUN=/opt/culler-runtime/bin/bun", "-e", "MC_PRIVATE_NODE=/opt/culler-runtime/bin/node",
      ...(options.containerArgs ?? []),
      "--entrypoint", "/opt/culler-runtime/bin/bun", options.image,
      `/opt/culler-runtime/app/src/agent-runner/adapters/${access.harness === "codex" ? "codex" : "claude"}.ts`,
      "--session-dir", "/mc/session", "--workspace", "/workspace/source", "--model", options.model,
      ...(access.harness === "codex" ? ["--mode", options.nativeReference ? "native" : "fresh"] : ["--channel", "claude", "--max-turns", String(options.maxTurns ?? 2)]),
      ...(options.adapterArgs ?? []),
      ...(options.nativeReference ? ["--native-ref", options.nativeReference] : [])];
    child = Bun.spawn(args, { stdin: new TextEncoder().encode(options.prompt), stdout: "pipe", stderr: "pipe" });
    const timer = setTimeout(() => child?.kill(), 60_000);
    const events: Record<string, any>[] = [];
    let stdout = "";
    const accept = (line: string) => {
      let value: any;
      try { value = JSON.parse(line); } catch { return; }
      if (!value || typeof value !== "object" || Array.isArray(value)) return;
      events.push(value);
      options.onEvent?.(value);
    };
    async function consume(): Promise<void> {
      const decoder = new TextDecoder();
      let pending = "";
      for await (const chunk of child!.stdout) {
        const text = decoder.decode(chunk, { stream: true });
        pending += text; stdout += text;
        if (stdout.length > 16 << 20) throw new Error("The harness output exceeds its limit.");
        for (let index = pending.indexOf("\n"); index >= 0; index = pending.indexOf("\n")) {
          accept(pending.slice(0, index)); pending = pending.slice(index + 1);
        }
      }
      pending += decoder.decode();
      if (pending) accept(pending);
    }
    async function errors(): Promise<string> {
      let bytes = 0; const chunks: Uint8Array[] = [];
      for await (const chunk of child!.stderr) {
        bytes += chunk.length;
        if (bytes > 1 << 20) throw new Error("The harness diagnostic exceeds its limit.");
        chunks.push(chunk);
      }
      return Buffer.concat(chunks).toString("utf8");
    }
    let result: [number, string, void];
    try { result = await Promise.all([child.exited, errors(), consume()]); }
    finally { clearTimeout(timer); }
    const [exitCode, stderr] = result;
    if (stdout.includes(access.token) || stderr.includes(access.token)) throw new Error("The harness exposed its Culler token.");
    return { exitCode, events, stderr,
      output: events.find(event => event.event === "turn-complete")?.output,
      nativeReference: events.find(event => event.event === "session-start")?.session_id,
      failure: events.findLast(event => event.event === "infrastructure-failure") };
  } finally {
    if (child && child.exitCode === null) child.kill();
    const removed = Bun.spawn(["docker", "rm", "--force", name], { stdin: "ignore", stdout: "pipe", stderr: "pipe" });
    const [code, error] = await Promise.all([removed.exited, new Response(removed.stderr).text()]);
    if (code && !error.includes("No such container")) throw new Error("The synthetic harness container cleanup failed.");
    await rm(root, { recursive: true, force: true });
  }
}
