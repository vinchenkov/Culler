import { chmod, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { resolve, join, relative } from "node:path";
import { randomBytes, randomUUID } from "node:crypto";
import { copyTrace } from "../../resident/src/trace-copy";
import { runNativeHarness } from "./native-harness";
import { liveModelControls } from "../live-model/models";

const argument = (name: string) => process.argv[process.argv.indexOf(name) + 1];
const image = argument("--image");
const harness = argument("--harness");
const authMode = argument("--auth-mode");
if (!image || !["codex", "claude-sdk"].includes(harness) || !["api-key", "account"].includes(authMode)) {
  throw new Error("The synthetic harness test requires an image, harness, and authentication method.");
}
const model = harness === "codex" ? "gpt-5-nano" : "claude-haiku-5-5";
const controls = liveModelControls(harness, model, "proxy");
const source = resolve(import.meta.dir, "../..");
const root = await mkdtemp("/private/tmp/culler-provider-harness-").catch(() => mkdtemp("/tmp/culler-provider-harness-"));
const failures: string[] = [];
let server: ReturnType<typeof Bun.serve> | undefined;
async function command(args: string[]) {
  const child = Bun.spawn(args, { stdin: "ignore", stdout: "pipe", stderr: "pipe" });
  const [code, out, error] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]);
  if (code) throw new Error(`${args[0]} failed: ${error}`);
  return out;
}
const event = (type: string, value: object) => `event: ${type}\ndata: ${JSON.stringify({ type, ...value })}\n\n`;
const finalText = "CULLER_PROXY_REPLY_OK";
const preliminary = "CULLER_PROXY_PRELIMINARY";
interface Scenario {
  label: string;
  token: string;
  requests: number;
  preliminaryObserved: boolean;
  responseOpen: boolean;
  release?: () => void;
  rejectedRequest?: string;
}
const states = new Map<string, Scenario>();
try {
  const stage = join(root, "runtime");
  await command(["python3", join(source, "runner/image/stage-runtime.py"), stage]);
  const pins = JSON.parse(await readFile(join(source, "runner/agent-runner/package.json"), "utf8")).dependencies;
  const actual = await command(["docker", "run", "--rm", "--network", "none", "--user", "10002:10002", "--cap-drop", "ALL",
    "--entrypoint", "/opt/culler-runtime/bin/bun", image, "-e",
    `console.log(JSON.stringify(Object.fromEntries(${JSON.stringify(Object.keys(pins))}.map(name => [name, require('/app/node_modules/' + name + '/package.json').version]))))`]);
  if (JSON.stringify(JSON.parse(actual)) !== JSON.stringify(pins)) throw new Error("The test image has different harness versions.");
  await command(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", join(root, "ca.key"),
    "-out", join(root, "ca.pem"), "-days", "1", "-subj", "/CN=Culler synthetic CA"]);
  await command(["openssl", "req", "-newkey", "rsa:2048", "-nodes", "-keyout", join(root, "server.key"),
    "-out", join(root, "server.csr"), "-subj", "/CN=host.docker.internal"]);
  await writeFile(join(root, "server.ext"), "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:host.docker.internal,DNS:localhost,IP:127.0.0.1\n");
  await command(["openssl", "x509", "-req", "-in", join(root, "server.csr"), "-CA", join(root, "ca.pem"),
    "-CAkey", join(root, "ca.key"), "-CAcreateserial", "-out", join(root, "server.pem"), "-days", "1", "-extfile", join(root, "server.ext")]);
  const ca = await readFile(join(root, "ca.pem"), "utf8");
  server = Bun.serve({ hostname: "127.0.0.1", port: 0,
    tls: { cert: await readFile(join(root, "server.pem"), "utf8"), key: await readFile(join(root, "server.key"), "utf8") },
    async fetch(request) {
      const state = states.get(request.headers.get("authorization")?.replace(/^Bearer /, "") ?? "");
      if (!state || request.headers.has("x-api-key")) return new Response("The Culler token is invalid.", { status: 401 });
      const path = new URL(request.url).pathname;
      if (request.method !== "POST" || !["/responses", "/v1/messages"].includes(path)) return new Response("No operation", { status: 404 });
      state.requests++;
      if (state.requests > 3) return new Response("The request limit is exhausted.", { status: 429 });
      if (state.rejectedRequest) {
        return Response.json({ error: { type: "culler_proxy_error", code: "proxy_token_expired",
          message: "The host could not authorize or complete this request." }, request_id: state.rejectedRequest },
        { status: 401, headers: { "request-id": state.rejectedRequest, "x-request-id": state.rejectedRequest,
          "x-culler-request-id": state.rejectedRequest, "x-culler-origin": "proxy" } });
      }
      const body = await request.json() as any;
      if (harness === "codex" ? body.reasoning?.effort !== controls.effort
        : body.max_tokens !== controls.output_token_limit || body.output_config?.effort !== controls.effort) {
        failures.push(`The native request omitted its live test output controls: ${JSON.stringify({ effort: body.reasoning?.effort, verbosity: body.text?.verbosity, max_tokens: body.max_tokens, output_effort: body.output_config?.effort })}.`);
        return new Response("The test controls are invalid.", { status: 400 });
      }
      const input = JSON.stringify(harness === "codex" ? body.input : body.messages);
      const other = state.label.endsWith("A") ? "PRIVATE_CONVERSATION_B" : "PRIVATE_CONVERSATION_A";
      if (body.model !== model || !input.includes(state.label) || input.includes(other) ||
          (state.requests === 3 && !input.includes(finalText))) {
        failures.push("The harness lost or mixed its native conversation context.");
        return new Response("The conversation context is invalid.", { status: 400 });
      }
      const final = state.requests > 1;
      const tool = harness === "codex" ? body.tools.find((item: any) => ["exec_command", "shell", "shell_command"].includes(item.name))
        : body.tools.find((item: any) => item.name === "Bash");
      if (!final && !tool) throw new Error("The harness supplied no shell tool.");
      const shell = "printf 'CULLER_TOOL_OK' > culler-tool.txt; cat /mc/provider-access.json";
      const properties = tool?.parameters?.properties ?? {};
      const arguments_ = properties.cmd ? { cmd: shell, max_output_tokens: 100 }
        : properties.command?.type === "array" ? { command: ["sh", "-c", shell] } : { command: shell };
      let before: string, after: string;
      if (harness === "codex") {
        const text = { id: `msg_${state.requests}`, type: "message", role: "assistant", status: "completed",
          content: [{ type: "output_text", text: final ? finalText : preliminary, annotations: [] }] };
        const action = { id: "fc_fixture", type: "function_call", call_id: "call_fixture", name: tool?.name,
          arguments: JSON.stringify(arguments_), status: "completed" };
        const response = { id: `resp_${state.requests}`, object: "response", model, status: "completed",
          output: final ? [text] : [text, action], usage: { input_tokens: 20, output_tokens: 20, total_tokens: 40,
            input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 } } };
        before = event("response.created", { response: { ...response, status: "in_progress", output: [] } })
          + event("response.output_item.added", { output_index: 0, item: { ...text, content: [] } })
          + event("response.output_text.delta", { item_id: text.id, output_index: 0, content_index: 0, delta: final ? finalText : preliminary })
          + event("response.output_item.done", { output_index: 0, item: text });
        after = (final ? "" : event("response.output_item.added", { output_index: 1, item: { ...action, arguments: "" } })
          + event("response.function_call_arguments.delta", { item_id: action.id, output_index: 1, delta: action.arguments })
          + event("response.output_item.done", { output_index: 1, item: action })) + event("response.completed", { response });
      } else {
        before = event("message_start", { message: { id: `msg_${state.requests}`, type: "message", role: "assistant", content: [],
          model, stop_reason: null, stop_sequence: null, usage: { input_tokens: 20, output_tokens: 0 } } })
          + event("content_block_start", { index: 0, content_block: { type: "text", text: "" } })
          + event("content_block_delta", { index: 0, delta: { type: "text_delta", text: final ? finalText : preliminary } })
          + event("content_block_stop", { index: 0 });
        after = (final ? "" : event("content_block_start", { index: 1, content_block: { type: "tool_use", id: "tool_fixture", name: "Bash", input: {} } })
          + event("content_block_delta", { index: 1, delta: { type: "input_json_delta", partial_json: JSON.stringify({ command: shell, description: "Create the synthetic artifact" }) } })
          + event("content_block_stop", { index: 1 }))
          + event("message_delta", { delta: { stop_reason: final ? "end_turn" : "tool_use", stop_sequence: null }, usage: { output_tokens: 20 } })
          + event("message_stop", {});
      }
      return new Response(new ReadableStream({ async start(controller) {
        state.responseOpen = true;
        controller.enqueue(new TextEncoder().encode(before));
        if (!final) {
          await new Promise<void>(accept => {
            const timer = setTimeout(() => { failures.push("The harness withheld preliminary text until completion."); accept(); }, 15_000);
            state.release = () => { clearTimeout(timer); accept(); };
          });
        }
        controller.enqueue(new TextEncoder().encode(after));
        state.responseOpen = false;
        controller.close();
      } }), { headers: { "content-type": "text/event-stream" } });
    },
  });
  const host = process.platform === "darwin" ? "host.docker.internal" : "127.0.0.1";
  const address = `https://${host}:${server.port}`;
  async function scenario(label: string, rejected = false) {
    const state: Scenario = { label, token: "culler_proxy_" + randomBytes(32).toString("base64url"), requests: 0, preliminaryObserved: false, responseOpen: false };
    if (rejected) state.rejectedRequest = randomUUID();
    states.set(state.token, state);
    const work = join(root, label);
    await mkdir(work, { mode: 0o777 });
    for (const part of ["session", "workspace"]) { await mkdir(join(work, part), { mode: 0o777 }); await chmod(join(work, part), 0o777); }
    async function turn(native?: string) {
      const result = await runNativeHarness({ image,
        access: { version: 1, url: address, token: state.token, ca, harness: harness as "codex" | "claude-sdk",
          authMode: authMode as "api-key" | "account" },
        model, runtimeDirectory: stage, adapterArgs: controls.args,
        sessionDirectory: join(work, "session"), workspaceDirectory: join(work, "workspace"), nativeReference: native,
        prompt: native ? "Continue the conversation and repeat the previous reply." : label,
        onEvent(item) {
          if (item.event === "turn-text" && String(item.text).includes(preliminary)) {
            state.preliminaryObserved ||= state.responseOpen;
            state.release?.();
          }
        },
      });
      if (rejected) {
        if (result.exitCode === 0 || result.failure?.proxy_request_id !== state.rejectedRequest || result.failure?.origin !== undefined) {
          throw new Error(`The ${harness} harness lost the host request identity.`);
        }
        return "rejected";
      }
      if (result.exitCode) throw new Error(`The ${harness} harness exited ${result.exitCode}: ${result.stderr}; ${failures.join("; ")}; ${JSON.stringify(result.failure)}`);
      if (result.output !== finalText) throw new Error("The harness produced no final reply.");
      if (harness === "codex") {
        const usage = result.events.find(event => event.type === "turn.completed")?.usage;
        if (!usage || usage.input_tokens !== (native ? 60 : 40) || usage.output_tokens !== (native ? 60 : 40)) {
          throw new Error("The fixed Codex usage no longer reports cumulative conversation totals.");
        }
      }
      return result.nativeReference;
    }
    const native = await turn();
    if (rejected) {
      if (state.requests !== 1) throw new Error("The harness retried a rejected proxy token.");
      return native;
    }
    if (!native || !state.preliminaryObserved || state.requests !== 2) throw new Error("The first turn lacks native or incremental response evidence.");
    if (await readFile(join(work, "workspace/culler-tool.txt"), "utf8") !== "CULLER_TOOL_OK") throw new Error("The agent action failed.");
    let retainedToken = false;
    async function scan(path: string): Promise<void> {
      for (const entry of await readdir(path, { withFileTypes: true })) {
        const child = join(path, entry.name);
        if (entry.isDirectory()) { await scan(child); continue; }
        const original = await readFile(child);
        retainedToken ||= original.includes(Buffer.from(state.token));
        if (!child.endsWith(".jsonl")) continue;
        const sessionRoot = join(work, "session");
        const dest = join(work, "copies", relative(sessionRoot, child));
        const copy = { root: sessionRoot, source: child, dest };
        while (!(await copyTrace(copy, Date.now())).caughtUp) {}
        if (!original.equals(await readFile(child))) throw new Error("The trace copy changed an original session.");
        if (await Bun.file(dest).exists() && (await readFile(dest)).includes(Buffer.from(state.token))) {
          throw new Error("The trace copy exposed the Culler token.");
        }
      }
    }
    await scan(join(work, "session"));
    if (!retainedToken) throw new Error("The original session lost the native tool result.");
    const next = await turn(native);
    if (next !== native || state.requests !== 3) throw new Error("The resumed turn changed its native context or request count.");
    return native;
  }
  const sessions = await Promise.all(["PRIVATE_CONVERSATION_A", "PRIVATE_CONVERSATION_B"].map(scenario));
  if (sessions[0] === sessions[1]) throw new Error("The concurrent conversations share a native session.");
  await scenario("PRIVATE_REJECTED_CONVERSATION", true);
  if (failures.length) throw new Error(failures.join("\n"));
  console.log(JSON.stringify({ result: "CULLER_HARNESS_PROXY_OK", harness, authMode, versions: pins, requests: 7,
    tls: true, preliminary_before_completion: true, native_resume: true, isolated_conversations: true,
    proxy_rejection_correlation: true, original_session_preservation: true, trace_token_redaction: true }));
} finally {
  for (const state of states.values()) state.release?.();
  server?.stop(true);
  await rm(root, { recursive: true, force: true });
}
