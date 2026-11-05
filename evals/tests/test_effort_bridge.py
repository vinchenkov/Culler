import json
from pathlib import Path
import subprocess

from culler_evals.protocol import effort_choices


SOURCE = Path(__file__).resolve().parents[2]


def test_bridge_applies_explicit_effort_without_changing_product_permissions(tmp_path):
    script = tmp_path / "effort-check.ts"
    helper = json.dumps(str(SOURCE / "evals/bridge/effort.ts"))
    script.write_text('import { codexEffortArgv, configureClaudeEffort, requireEffort } from ' + helper + ';\n' + '''
const original = ["codex", "exec", "--model", "fixed-model", "--strict-config", "-"];
const actual = codexEffortArgv(original, "high");
const expected = [...original.slice(0, -1), "--config", 'model_reasoning_effort="high"',
  "--config", 'agents.default_subagent_reasoning_effort="high"', "-"];
if (JSON.stringify(actual) !== JSON.stringify(expected)) throw new Error("The Codex effort arguments differ.");
if (original.length !== 6) throw new Error("The effort helper altered the product arguments.");
const options = { model: "fixed-model", permissionMode: "bypassPermissions", sandbox: { enabled: false },
  settingSources: ["user"], env: { FIXED_ENV: "retained", CLAUDE_CODE_EFFORT_LEVEL: "low" } };
configureClaudeEffort(options, "high");
if (options.effort !== "high" || options.thinking.type !== "adaptive" || options.env.CLAUDE_CODE_EFFORT_LEVEL !== "high") {
  throw new Error("The Claude effort options differ.");
}
if (options.model !== "fixed-model" || options.permissionMode !== "bypassPermissions" || options.sandbox.enabled ||
  options.settingSources.join() !== "user" || options.env.FIXED_ENV !== "retained") {
  throw new Error("The effort helper altered the product configuration.");
}
const choices = JSON.parse(process.argv[2]);
for (const [harness, values] of Object.entries(choices)) {
  for (const value of values) if (requireEffort(harness, value) !== value) throw new Error("The effort choices differ.");
  for (const value of [undefined, null, "", "unknown", 7]) {
    let rejected = false;
    try { requireEffort(harness, value); } catch { rejected = true; }
    if (!rejected) throw new Error("The bridge accepted an implicit or invalid effort.");
  }
}
let rejected = false;
try { requireEffort("claude", "ultra"); } catch { rejected = true; }
if (!rejected) throw new Error("The bridge accepted effort from a different harness.");
console.log(JSON.stringify({ passed: true, model_calls: 0 }));
''')
    choices = {harness: effort_choices(harness) for harness in ("codex", "claude")}
    result = subprocess.run(["mise", "exec", "--", "bun", str(script), json.dumps(choices)],
                            cwd=SOURCE, check=True, capture_output=True, text=True)
    assert json.loads(result.stdout) == {"passed": True, "model_calls": 0}


def test_codex_fast_mode_preserves_model_effort_and_permissions(tmp_path):
    script = tmp_path / "fast-mode-check.ts"
    helper = json.dumps(str(SOURCE / "evals/bridge/fast-mode.ts"))
    script.write_text('import { codexFastModeArgv } from ' + helper + ';\n' + '''
const original = ["codex", "exec", "--model", "fixed-model", "--strict-config", "--config", 'model_reasoning_effort="high"', "-"];
for (const enabled of [true, false]) {
  const actual = codexFastModeArgv(original, enabled);
  if (actual.at(-1) !== "-" || actual.slice(0, original.length - 1).join() !== original.slice(0, -1).join()) {
    throw new Error("Fast mode altered the model, effort, or permissions.");
  }
  if (!actual.includes(`service_tier="${enabled ? "fast" : "default"}"`) || !actual.includes(`features.fast_mode=${enabled}`)) {
    throw new Error("The Codex fast mode configuration differs.");
  }
}
for (const value of [null, "true", 1]) {
  let rejected = false;
  try { codexFastModeArgv(original, value); } catch { rejected = true; }
  if (!rejected) throw new Error("The bridge accepted an invalid fast mode setting.");
}
console.log(JSON.stringify({ passed: true, model_calls: 0 }));
''')
    result = subprocess.run(["mise", "exec", "--", "bun", str(script)], cwd=SOURCE,
                            check=True, capture_output=True, text=True)
    assert json.loads(result.stdout) == {"passed": True, "model_calls": 0}


def test_claude_fast_mode_preserves_session_settings_and_allows_account_check(tmp_path):
    script = tmp_path / "claude-fast-mode-check.ts"
    helper = json.dumps(str(SOURCE / "evals/bridge/fast-mode.ts"))
    script.write_text('import { configureClaudeFastMode } from ' + helper + ';\n' + '''
for (const enabled of [true, false]) {
  const settings = { autoMemoryEnabled: false, autoDreamEnabled: false, fastMode: !enabled };
  const env = { FIXED_ENV: "retained", CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1", CLAUDE_CODE_EFFORT_LEVEL: "high" };
  const options = { model: "fixed-model", effort: "high", permissionMode: "bypassPermissions",
    sandbox: { enabled: false }, settingSources: ["user"], settings, env };
  configureClaudeFastMode(options, enabled);
  if (options.settings.fastMode !== enabled || options.settings.autoMemoryEnabled !== false ||
      options.settings.autoDreamEnabled !== false || settings.fastMode !== !enabled) {
    throw new Error("The bridge altered the other session settings.");
  }
  if (options.model !== "fixed-model" || options.effort !== "high" || options.permissionMode !== "bypassPermissions" ||
      options.sandbox.enabled || options.settingSources.join() !== "user" || options.env.FIXED_ENV !== "retained" ||
      options.env.CLAUDE_CODE_EFFORT_LEVEL !== "high") {
    throw new Error("The bridge altered the model, effort, permissions, or environment.");
  }
  if (enabled && "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC" in options.env ||
      !enabled && options.env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC !== "1" ||
      env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC !== "1" ||
      "CLAUDE_CODE_SKIP_FAST_MODE_ORG_CHECK" in options.env || "CLAUDE_CODE_SKIP_FAST_MODE_NETWORK_ERRORS" in options.env) {
    throw new Error("The bridge altered the account check incorrectly.");
  }
}
for (const value of [null, "true", 1]) {
  let rejected = false;
  try { configureClaudeFastMode({}, value); } catch { rejected = true; }
  if (!rejected) throw new Error("The bridge accepted an invalid fast mode setting.");
}
console.log(JSON.stringify({ passed: true, model_calls: 0 }));
''')
    result = subprocess.run(["mise", "exec", "--", "bun", str(script)], cwd=SOURCE,
                            check=True, capture_output=True, text=True)
    assert json.loads(result.stdout) == {"passed": True, "model_calls": 0}
