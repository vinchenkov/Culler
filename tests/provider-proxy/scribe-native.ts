import { chmod, lstat, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { randomBytes, randomUUID } from "node:crypto";
import { runNativeHarness, syntheticFinalResponse } from "./native-harness";
import { ACCEPTANCE_REPORT, SCRIBE_TOOLS, successfulScribeTools } from "../live-model/scribe";

const image = process.argv[process.argv.indexOf("--image") + 1];
if (!image || !process.argv.includes("--image")) throw new Error("Select the isolated native test image.");
const root = await mkdtemp("/tmp/culler-scribe-native-");
let server: ReturnType<typeof Bun.serve> | undefined;
async function command(argv: string[]) {
  const child = Bun.spawn(argv, { stdout: "pipe", stderr: "pipe" });
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]);
  if (code) throw new Error(`The synthetic fixture command failed: ${argv[0]}: ${stderr}`);
  return stdout;
}
const event = (type: string, value: object) => `event: ${type}\ndata: ${JSON.stringify({ type, ...value })}\n\n`;
try {
  await command(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", join(root, "ca.key"),
    "-out", join(root, "ca.pem"), "-days", "1", "-subj", "/CN=Culler synthetic CA"]);
  await command(["openssl", "req", "-newkey", "rsa:2048", "-nodes", "-keyout", join(root, "server.key"),
    "-out", join(root, "server.csr"), "-subj", "/CN=host.docker.internal"]);
  await writeFile(join(root, "server.ext"), "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:host.docker.internal,DNS:localhost,IP:127.0.0.1\n");
  await command(["openssl", "x509", "-req", "-in", join(root, "server.csr"), "-CA", join(root, "ca.pem"),
    "-CAkey", join(root, "ca.key"), "-CAcreateserial", "-out", join(root, "server.pem"), "-days", "1", "-extfile", join(root, "server.ext")]);
  const ca = await readFile(join(root, "ca.pem"), "utf8");
  const states = new Map<string, { harness: "codex" | "claude-sdk"; requests: number; workspace: string }>();
  server = Bun.serve({ hostname: "127.0.0.1", port: 0,
    tls: { cert: await readFile(join(root, "server.pem"), "utf8"), key: await readFile(join(root, "server.key"), "utf8") },
    async fetch(request) {
      const state = states.get(request.headers.get("authorization")?.replace(/^Bearer /, "") ?? "");
      if (!state || request.method !== "POST") return new Response("Invalid synthetic access.", { status: 401 });
      if (++state.requests > 8) return new Response("Synthetic request limit.", { status: 429 });
      const body = await request.json() as any;
      if (state.requests === 8) return syntheticFinalResponse(state.harness, body);
      let runId: string | undefined, jobId: string | undefined;
      const inspect = (value: any, depth = 0) => {
        if (depth > 14) return;
        if (typeof value === "string") {
          const start = value.indexOf("{");
          try { inspect(JSON.parse(start < 0 ? value : value.slice(start)), depth + 1); } catch {}
          return;
        }
        if (!value || typeof value !== "object") return;
        if (typeof value.runId === "string") runId = value.runId;
        if (typeof value.job?.id === "string") jobId = value.job.id;
        for (const child of Object.values(value)) inspect(child, depth + 1);
      };
      inspect(state.harness === "codex" ? body.input : body.messages);
      const sequence = ["openwiki_begin", "openwiki_submit_plan", "openwiki_next_page", "openwiki_inspect_page_claims",
        "openwiki_submit_page", "openwiki_next_page", "openwiki_finish"];
      const name = sequence[state.requests - 1]!;
      const namespace = body.tools.find((tool: any) => tool.type === "namespace" && tool.name === "mcp__openwiki");
      const tool = (namespace?.tools ?? body.tools).find((tool: any) => tool.name?.endsWith(name));
      if (!tool) throw new Error(`The native request omitted OpenWiki: ${JSON.stringify(body.tools.map((tool: any) => ({ type: tool.type, name: tool.name, tools: tool.tools?.map((child: any) => child.name) })))}`);
      if (state.requests > 1 && !runId) throw new Error("The native tool result omitted its run identity.");
      if (state.requests === 4) await writeFile(join(state.workspace, "openwiki/quickstart.md"),
        "---\ntitle: Quickstart\ntype: Guide\n---\n\nThe fixture proves one native tool call.\n");
      const args = state.requests === 1 ? { root: "/workspace/source", mode: "init" }
        : state.requests === 2 ? { runId, pages: [{ path: "/openwiki/quickstart.md", title: "Quickstart", purpose: "Explain the fixture.", seedPaths: ["source.txt"] }] }
        : state.requests === 4 ? { runId, jobId }
        : state.requests === 5 ? { runId, jobId, claims: [{ statement: "The fixture proves one native tool call.", evidence: [{ resource: "repo://source.txt#L1" }] }] }
        : { runId };
      const id = randomUUID();
      let stream: string;
      if (state.harness === "codex") {
        const action = { id: `fc_${id}`, type: "function_call", call_id: `call_${id}`, name: tool.name,
          arguments: JSON.stringify(args), status: "completed", ...(namespace ? { namespace: namespace.name } : {}) };
        const response = { id: `resp_${id}`, object: "response", model: body.model, status: "completed", output: [action],
          usage: { input_tokens: 20, output_tokens: 10, total_tokens: 30 } };
        stream = event("response.created", { response: { ...response, status: "in_progress", output: [] } })
          + event("response.output_item.added", { output_index: 0, item: { ...action, arguments: "" } })
          + event("response.function_call_arguments.delta", { item_id: action.id, output_index: 0, delta: action.arguments })
          + event("response.output_item.done", { output_index: 0, item: action }) + event("response.completed", { response });
      } else {
        stream = event("message_start", { message: { id: `msg_${id}`, type: "message", role: "assistant", content: [], model: body.model,
          stop_reason: null, usage: { input_tokens: 20, output_tokens: 0 } } })
          + event("content_block_start", { index: 0, content_block: { type: "tool_use", id: `tool_${id}`, name: tool.name, input: {} } })
          + event("content_block_delta", { index: 0, delta: { type: "input_json_delta", partial_json: JSON.stringify(args) } })
          + event("content_block_stop", { index: 0 })
          + event("message_delta", { delta: { stop_reason: "tool_use", stop_sequence: null }, usage: { output_tokens: 10 } })
          + event("message_stop", {});
      }
      return new Response(stream, { headers: { "content-type": "text/event-stream" } });
    },
  });
  for (const harness of ["codex", "claude-sdk"] as const) {
    const workspace = join(root, harness); await mkdir(workspace); await chmod(workspace, 0o777);
    await mkdir(join(workspace, "openwiki/reports"), { recursive: true });
    await writeFile(join(workspace, "openwiki/reports/accepted.md"), ACCEPTANCE_REPORT);
    await writeFile(join(workspace, "AGENTS.md"), "Preserve complete accepted project reports.\n");
    const home = join(root, `${harness}-home`), temporary = join(root, `${harness}-tmp`);
    for (const path of [home, temporary]) { await mkdir(path); await chmod(path, 0o777); }
    await writeFile(join(workspace, "source.txt"), "The fixture proves one native tool call.\n");
    await command(["git", "-C", workspace, "init", "--quiet", "--template=", "-b", "main"]);
    await command(["git", "-C", workspace, "add", "."]);
    await command(["git", "-C", workspace, "-c", "user.name=Synthetic fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "Create the fixture"]);
    const token = "culler_proxy_" + randomBytes(32).toString("base64url");
    const state = { harness, requests: 0, workspace }; states.set(token, state);
    const result = await runNativeHarness({ image, access: { version: 1, harness, authMode: "api-key", token, ca,
      url: `https://${process.platform === "darwin" ? "host.docker.internal" : "127.0.0.1"}:${server.port}` },
      model: harness === "codex" ? "gpt-5-nano" : "claude-haiku-5-5", prompt: "Call the supplied OpenWiki tool once.", workspaceDirectory: workspace,
      containerArgs: ["--workdir", "/workspace/source", "--pids-limit", "2048",
        "-v", `${home}:/home/agent`, "-v", `${temporary}:/tmp`, "-e", "HOME=/home/agent",
        "-e", "PATH=/opt/culler-runtime/commands:/usr/local/bin:/usr/bin:/bin",
        "-e", "LD_PRELOAD=", "-e", "LD_LIBRARY_PATH=", "-e", "NODE_OPTIONS=", "-e", "BUN_OPTIONS="],
      adapterArgs: ["--openwiki", "--openwiki-input-commit", (await command(["git", "-C", workspace, "rev-parse", "HEAD"])).trim(), "--effort", "low"], maxTurns: 10 });
    const tools = successfulScribeTools(result.events.map(event => JSON.stringify(event)).join("\n"), harness);
    const metadata = result.events.filter(event => event.item?.type?.includes("tool") || event.type === "assistant" || event.type === "user")
      .map(event => ({ type: event.type, item_type: event.item?.type, item_keys: Object.keys(event.item ?? {}),
        server: event.item?.server, tool: event.item?.tool, status: event.item?.status,
        result_keys: Object.keys(event.item?.result ?? {}),
        content_types: event.message?.content?.map((item: any) => item.type) }));
    console.log(JSON.stringify({ harness, exit_code: result.exitCode, requests: state.requests, tools, metadata, errors: result.events.filter(event => event.item?.type === "error"), failure: result.failure }));
    if (result.exitCode || tools.join(",") !== SCRIBE_TOOLS.join(",")) throw new Error("The native Scribe tool evidence is incompatible.");
    const manifest = JSON.parse(await readFile(join(workspace, "openwiki/.page-manifest.json"), "utf8"));
    if (!manifest.pages?.["/openwiki/quickstart.md"]?.completedRunId) throw new Error("The native tools left the page incomplete.");
    for (const path of ["openwiki/.run.json", "openwiki/.culler-run.json"]) {
      if (await lstat(join(workspace, path)).then(() => true, error => { if (error.code === "ENOENT") return false; throw error; })) {
        throw new Error("The native tools left the wiki run incomplete.");
      }
    }
  }
} finally { server?.stop(true); await rm(root, { recursive: true, force: true }); }
