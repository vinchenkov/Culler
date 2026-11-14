import { mkdir } from "node:fs/promises";
import { randomUUID, createHash } from "node:crypto";

if (process.argv.includes("--version")) {
  console.log("Evaluation test double (no model calls)");
  process.exit(0);
}
const prompt = await Bun.stdin.text();
if (!process.argv.includes('model_reasoning_effort="high"') || !process.argv.includes('agents.default_subagent_reasoning_effort="high"')) {
  throw new Error("The test harness received no explicit high effort.");
}
if (!process.argv.includes('service_tier="fast"') || !process.argv.includes('features.fast_mode=true')) {
  throw new Error("The test harness received no fast mode configuration.");
}
if (!prompt.includes("Preserve the repository and submit the fixture packet.")) throw new Error("The test harness received no complete role directive.");
console.log(JSON.stringify({ type: "evaluation.prompt_received", sha256: createHash("sha256").update(prompt).digest("hex"), bytes: Buffer.byteLength(prompt) }));
const run = JSON.parse(await Bun.file("/mc/run.json").text());
const plane = run.mounts.allow_write[0];
const directory = `${plane}/${run.subject}/packet`;
await mkdir(directory, { recursive: true });
const packet = `${directory}/packet.html`;
await Bun.write(packet, "<!doctype html><h1>Summary</h1><p>The fixture changes.</p>");
const id = randomUUID();
await Bun.write(`/eval/session/${id}.jsonl`, JSON.stringify({ type: "session_meta", payload: { id, cli_version: "evaluation-test-double" } }) + "\n" +
  JSON.stringify({ type: "turn_context", payload: { model: "fixture-model", effort: "high" } }) + "\n");
console.log(JSON.stringify({ type: "thread.started", thread_id: id }));
const shellEnv = Object.fromEntries(Object.entries(process.env).filter(([name]) => !name.includes("TOKEN")));
delete shellEnv.CULLER_EVAL_COMMAND_URL;
console.log(JSON.stringify({ type: "evaluation.command_environment", token_present: "CULLER_EVAL_COMMAND_TOKEN" in shellEnv,
  private_file_present: await Bun.file("/mc/eval-command.json").exists() }));
const command = Bun.spawn(["mc", "complete", String(run.subject), "--run", run.run_id, "--status", "packaged", "--outputs", packet,
  "--thesis", "The fixture changes."], { env: shellEnv, stdout: "pipe", stderr: "pipe" });
const output = await new Response(command.stdout).text();
const error = await new Response(command.stderr).text();
const code = await command.exited;
console.log(JSON.stringify({ type: "item.completed", item: { type: "command_execution", command: "mc complete (fixture)", aggregated_output: output + error, exit_code: code } }));
if (code !== 0) process.exit(code);
console.log(JSON.stringify({ type: "item.completed", item: { type: "agent_message", text: "The fixture packet is complete." } }));
console.log(JSON.stringify({ type: "turn.completed", usage: { input_tokens: 0, output_tokens: 0 } }));
