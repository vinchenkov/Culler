import json
from pathlib import Path
import subprocess

from culler_evals.assessment import judge_output_schema


SOURCE = Path(__file__).resolve().parents[2]


def test_bridge_uses_validated_data_and_preserves_the_native_result(tmp_path):
    script = tmp_path / "structured-output.ts"
    helper = json.dumps(str(SOURCE / "evals/bridge/structured-output.ts"))
    schema = judge_output_schema()
    script.write_text('import { configureClaudeStructuredOutput, claudeStructuredOutputEmitter } from ' + helper + ';\n' + '''
const schema = JSON.parse(process.argv[2]);
const options = { model: "fixed-model", effort: "high", tools: ["Read"],
  settings: { fastMode: true }, permissionMode: "bypassPermissions" };
configureClaudeStructuredOutput(options, schema);
if (options.outputFormat.type !== "json_schema" || JSON.stringify(options.outputFormat.schema) !== JSON.stringify(schema)) {
  throw new Error("The bridge omitted the output schema.");
}
if (options.model !== "fixed-model" || options.effort !== "high" || options.tools.join() !== "Read" ||
    options.settings.fastMode !== true || options.permissionMode !== "bypassPermissions") {
  throw new Error("The bridge altered the role configuration.");
}
const validated = { semantic_checks: [{ id: "evidence_authority", passed: true }] };
const raw = '{"semantic_checks":[{"id":"evidence_authority"},"summary_matches_thesis":{"passed":true}]}';
const result = { type: "result", subtype: "success", is_error: false, result: raw, structured_output: validated };
const events = [];
const emit = claudeStructuredOutputEmitter(event => events.push(event));
emit({ type: "system", subtype: "init" });
emit(result);
emit({ event: "turn-complete", status: "success", output: raw });
const complete = events.filter(event => event.event === "turn-complete");
if (events[1] !== result || events[1].result !== raw || complete.length !== 1 ||
    JSON.stringify(JSON.parse(complete[0].output)) !== JSON.stringify(validated) || complete[0].output_format !== "json_schema") {
  throw new Error("The bridge used malformed prose or lost the native evidence.");
}
for (const output of [undefined, null, [], "text", 7]) {
  const events = [];
  const emit = claudeStructuredOutputEmitter(event => events.push(event));
  let rejected = false;
  try { emit({ type: "result", subtype: "success", is_error: false, result: "{}", structured_output: output }); }
  catch { rejected = true; }
  if (!rejected || events.some(event => event.event === "turn-complete")) {
    throw new Error("The bridge accepted a missing or invalid structured result.");
  }
}
const failed = [];
claudeStructuredOutputEmitter(event => failed.push(event))({ type: "result", subtype: "error_max_structured_output_retries", is_error: true });
if (failed.length !== 1 || failed.some(event => event.event === "turn-complete")) {
  throw new Error("The bridge converted a failed assessment into a successful result.");
}
console.log(JSON.stringify({ passed: true, model_calls: 0 }));
''')
    result = subprocess.run(["mise", "exec", "--", "bun", str(script), json.dumps(schema)],
                            cwd=SOURCE, check=True, capture_output=True, text=True)
    assert json.loads(result.stdout) == {"passed": True, "model_calls": 0}
