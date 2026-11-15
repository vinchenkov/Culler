import json
import os
from pathlib import Path
import shutil
import subprocess

import pytest

from test_commands import command_case


SOURCE = Path(__file__).resolve().parents[2]


@pytest.mark.parametrize("detached", [False, True])
@pytest.mark.parametrize("change_branch", [False, True])
def test_launch_preserves_branch_after_the_harness_exits(tmp_path, detached, change_branch):
    _, _, repository, git = command_case.__wrapped__(tmp_path)
    if detached:
        git("checkout", "--quiet", "--detach", "HEAD")
    start_commit = git("rev-parse", "HEAD")
    fixture = tmp_path / "launch-fixture"
    fixture.mkdir()
    request = fixture / "request.json"
    request.write_text(json.dumps({"role": "packager", "harness": "codex", "model": "fixture", "effort": "high", "workspace": str(repository)}))
    git_args = ["git", "-C", str(repository)] + (
        ["switch", "--quiet", "-c", "unexpected-after-terminal"] if change_branch else ["status", "--porcelain"])
    modules = {
        "skills": "export function runSkillsFrom(){return []} export async function installRunSkills(){}",
        "codex": "export function codexAdapterEnv(env){return env} export function codexArgv(){return "
                 + json.dumps(git_args) + "} export async function executeCodexProcess(child){return await child.exited}",
        "claude": "export function claudeAdapterEnv(){} export function claudeOptions(){} export async function executeClaudeQuery(){} export async function mirrorRoleSkill(){}",
        "store": "export class NativeSessionStore{}",
        "access": "export async function loadProviderAccess(){return {token:'culler_proxy_'+'x'.repeat(43)}} export async function writeProviderCA(){return 'fixture-ca'} export function codexProviderArgs(){return []} export async function redactNativeAccess(){}",
        "paths": "export function nativeSdkExecutable(){}",
        "tools": "export function judgeFileTool(){}",
        "fast-mode": "export function codexFastModeArgv(a){return a} export function configureClaudeFastMode(){}",
        "effort": "export function requireEffort(_h,e){return e} export function codexEffortArgv(a){return a} export function configureClaudeEffort(){}",
    }
    for name, body in modules.items():
        (fixture / (name + ".ts")).write_text(body)
    git_executable = shutil.which("git")
    assert git_executable is not None
    replacements = {
        'Bun.spawn(["git",': 'Bun.spawn([' + json.dumps(git_executable) + ',',
        "/app/src/agent-runner/skills": "./skills",
        "/app/src/agent-runner/adapters/codex": "./codex",
        "/app/src/agent-runner/adapters/claude": "./claude",
        "/app/src/agent-runner/adapters/session-store": "./store",
        "/app/src/agent-runner/adapters/provider-access": "./access",
        "./harness-paths": "./paths",
        "./judge-tools": "./tools",
        "./structured-output": str(SOURCE / "evals/bridge/structured-output"),
        "/eval/request.json": str(request),
        "/eval/session": str(fixture / "session"),
        "/eval/private-config": str(fixture / "private-config"),
    }
    launch = (SOURCE / "evals/bridge/launch.ts").read_text()
    for original, replacement in replacements.items():
        launch = launch.replace(original, replacement)
    (fixture / "launch.ts").write_text(launch)
    env = os.environ.copy()
    env["CULLER_EVAL_START_COMMIT"] = start_commit
    result = subprocess.run(["mise", "exec", "--", "bun", str(fixture / "launch.ts")],
                            cwd=SOURCE, input="", text=True, capture_output=True, env=env, check=True)
    events = [json.loads(line) for line in result.stdout.splitlines()]
    final = [event for event in events if event.get("event") == "repository-state"]
    assert len(final) == 1
    assert final[0]["preserved"] is (not change_branch)
    assert git("rev-parse", "HEAD") == start_commit


@pytest.mark.parametrize("role", ["judge", "scope_judge"])
@pytest.mark.parametrize("missing_result", [False, True])
def test_claude_launch_uses_the_structured_result_and_keeps_the_native_response(tmp_path, role, missing_result):
    from culler_evals.assessment import judge_output_schema, scope_judge_output_schema
    from test_assessment import judgment

    schema = judge_output_schema() if role == "judge" else scope_judge_output_schema()
    data = judgment() if role == "judge" else {"permitted": False, "reasons": ["The proposal removed a protected requirement."]}
    raw = '{"semantic_checks":[{"id":"evidence_authority"},"summary_matches_thesis":{"passed":true}]}'
    request = tmp_path / "request.json"
    request.write_text(json.dumps({"role": role, "harness": "claude", "model": "fixed-model", "effort": "high", "fast_mode": True,
                                   "channel": "claude", "base_url": "", "workspace": str(tmp_path), "output_schema": schema}))
    capture = tmp_path / "options.json"
    native_result = {"type": "result", "subtype": "success", "is_error": False, "result": raw}
    if not missing_result:
        native_result["structured_output"] = data
    modules = {
        "skills": "export function runSkillsFrom(){return []} export async function installRunSkills(){}",
        "codex": "export function codexAdapterEnv(){} export function codexArgv(){} export function executeCodexProcess(){}",
        "claude": "export function claudeAdapterEnv(_c,e){return {...e}} export function claudeOptions(model,_w,env){return {model,env,settings:{autoMemoryEnabled:false}}} "
                  "export async function mirrorRoleSkill(){} export async function executeClaudeQuery(_p,options,_o,{emit}){"
                  + f"await Bun.write({json.dumps(str(capture))},JSON.stringify(options));"
                  + "emit({type:'system',subtype:'init'});emit(" + json.dumps(native_result) + ");"
                  + "emit({event:'turn-complete',status:'success',output:" + json.dumps(raw) + "});return 0;}",
        "store": "export class NativeSessionStore{}",
        "access": "export async function loadProviderAccess(){return {token:'culler_proxy_'+'x'.repeat(43)}} export async function writeProviderCA(){return 'fixture-ca'} export function codexProviderArgs(){return []} export async function redactNativeAccess(){}",
        "paths": "export async function nativeSdkExecutable(){return process.execPath}",
        "tools": "export function judgeFileTool(){}",
    }
    for name, body in modules.items():
        (tmp_path / (name + ".ts")).write_text(body)
    replacements = {
        "/app/src/agent-runner/skills": "./skills", "/app/src/agent-runner/adapters/codex": "./codex",
        "/app/src/agent-runner/adapters/claude": "./claude", "/app/src/agent-runner/adapters/session-store": "./store",
        "/app/src/agent-runner/adapters/provider-access": "./access",
        "./harness-paths": "./paths", "./judge-tools": "./tools",
        "./effort": str(SOURCE / "evals/bridge/effort"), "./fast-mode": str(SOURCE / "evals/bridge/fast-mode"),
        "./structured-output": str(SOURCE / "evals/bridge/structured-output"),
        "/eval/request.json": str(request), "/eval/session": str(tmp_path / "session"),
        "/eval/private-config": str(tmp_path / "private-config"),
    }
    launch = (SOURCE / "evals/bridge/launch.ts").read_text()
    for old, new in replacements.items():
        launch = launch.replace(old, new)
    path = tmp_path / "launch.ts"
    path.write_text(launch)
    result = subprocess.run(["mise", "exec", "--", "bun", str(path)], cwd=SOURCE, input="The assessment fixture.",
                            text=True, capture_output=True)
    options = json.loads(capture.read_text())
    assert options["outputFormat"] == {"type": "json_schema", "schema": schema}
    assert options["model"] == "fixed-model" and options["effort"] == "high" and options["settings"]["fastMode"] is True
    events = [json.loads(line) for line in result.stdout.splitlines()]
    native = next(event for event in events if event.get("type") == "result")
    assert native == native_result
    completed = [event for event in events if event.get("event") == "turn-complete"]
    if missing_result:
        assert result.returncode != 0 and not completed
    else:
        assert result.returncode == 0 and len(completed) == 1
        assert json.loads(completed[0]["output"]) == data
        assert completed[0]["output_format"] == "json_schema"
