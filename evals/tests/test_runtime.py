import copy
from contextlib import nullcontext
import hashlib
import json
from pathlib import Path
import subprocess
from types import SimpleNamespace

import pytest

from proxy_fixture import proxy_credentials

from culler_evals.runtime import ProductContracts, Runtime, RuntimeErrorWithEvidence, _json_response, _render_failures
from culler_evals.runtime_sources import runtime_source_manifest, runtime_source_hash
from culler_evals.product_sources import product_source_manifest, product_source_hash


SOURCE = Path(__file__).resolve().parents[2]


@pytest.fixture(scope="module")
def product_contracts(tmp_path_factory):
    return ProductContracts(SOURCE, tmp_path_factory.mktemp("runtime-product-contracts"))


def test_repository_probe_uses_frozen_image_mise_and_private_read_only_mounts(tmp_path, monkeypatch):
    calls = []
    def execute(argv, **settings):
        calls.append(argv)
        assert settings["timeout"] == 60
        return subprocess.CompletedProcess(argv, 0, '{"states":{"authority":{"commit":"fixed","branch":"HEAD","clean":true}}}', '')
    monkeypatch.setattr(subprocess, "run", execute)
    with Runtime(SOURCE, source_manifest=runtime_source_manifest(SOURCE), product_manifest=product_source_manifest(SOURCE),
                 snapshot_root=tmp_path / "snapshot") as runtime:
        result = runtime.repository_states({"authority": str(tmp_path / "private-case")}, {"image": "sha256:" + "1" * 64})
    argv = calls[0]
    assert argv[:5] == ["docker", "run", "--rm", "--network", "none"]
    assert "--read-only" in argv
    assert argv[-7:] == ["sha256:" + "1" * 64, "mise", "exec", "--", "bun", "/app/eval/repository-state.ts", "authority"]
    assert all(argv[index + 1].endswith(",readonly") for index, value in enumerate(argv) if value == "--mount")
    assert "--env-file" not in argv
    assert result["states"]["authority"]["clean"] is True
    assert result["runtime_source_hash"]


class ContractsDouble:
    def call(self, operation, **fields):
        assert operation == "input"
        return {"input": fields["directive"] + "\n" + json.dumps(fields["parts"]),
                "skills": [{"name": "mc-packager", "body": "Fixed command skill"}], "brief_schema": "fixture"}


def test_product_input_preserves_skills_and_omits_writing(product_contracts):
    result = product_contracts.call("input", run_id="eval-fixture", directive="Original and merged writing body",
                            parts=[{"heading": "Run", "explanation": "This is the current run.",
                                    "facts": [{"label": "Run identifier", "value": "eval-fixture"}]}], project_skills=[])
    assert "<role_directive>\nOriginal and merged writing body" in result["input"]
    assert "## Run\n\nThis is the current run." in result["input"]
    names = [skill["name"] for skill in result["skills"]]
    assert names[0] == "mc-packager"
    assert "writing" not in names
    assert "~/.agents/skills/writing" not in result["input"]
    assert "mc.spawn-brief." in result["brief_schema"]
    skill = next(item for item in result["skills"] if item["name"] == "mc-packager")
    assert skill["body"] == (SOURCE / "mc/verbs/skills/packager/mc-packager/SKILL.md").read_text()


def test_all_case_briefs_use_current_product_parts_without_case_changes(product_contracts):
    stale = {"Verdict history", "Scribe report", "Earlier requirements"}
    stale_facts = {"Verified source commit", "Scribe input commit", "Scribe result commit", "Scribe state",
                   "Wiki source baseline"}
    manifests = sorted((SOURCE / "evals/cases/memos").glob("*.json"))
    manifests += sorted((SOURCE / "evals/cases/memos/corpus").glob("*.json"))
    assert len(manifests) == 35
    for path in manifests:
        before = hashlib.sha256(path.read_bytes()).hexdigest()
        case = json.loads(path.read_text())
        parts = copy.deepcopy(case["role_inputs"]["brief_parts"])
        original = copy.deepcopy(parts)
        result = product_contracts.call("input", run_id="eval-current-fixture", directive="The editable directive.",
                                        parts=parts, project_skills=case.get("project_skills", []))
        assert result["omitted_parts"] == [part["heading"] for part in parts if part["heading"] in stale], path
        git_facts = [fact for part in parts if part["heading"] == "Git state" for fact in part["facts"]]
        assert result["omitted_facts"] == [fact["label"] for fact in git_facts if fact["label"] in stale_facts], path
        assert result["omitted_facts"], path
        for fact in git_facts:
            line = "- " + fact["label"] + ": " + fact["value"] + "\n"
            assert (line in result["input"]) == (fact["label"] not in stale_facts), (path, fact["label"])
        for part in parts:
            heading = "## " + part["heading"] + "\n"
            assert (heading in result["input"]) == (part["heading"] not in stale), (path, part["heading"])
            if part["heading"] not in stale and part.get("text"):
                assert part["text"] in result["input"], path
        assert parts == original
        assert hashlib.sha256(path.read_bytes()).hexdigest() == before


def test_conditional_current_parts_remain_available(product_contracts):
    headings = ["Task", "Vision", "Previous packet"]
    parts = [{"heading": heading, "explanation": "This part supplies current product context.",
              "facts": [{"label": "Current value", "value": heading}]} for heading in headings]
    result = product_contracts.call("input", run_id="eval-conditional", directive="The editable directive.",
                                    parts=parts, project_skills=[])
    assert not result["omitted_parts"]
    assert not result["omitted_facts"]
    for heading in headings:
        assert "## " + heading + "\n" in result["input"]


def test_optimizer_mounts_only_permitted_workspace(tmp_path):
    calls = []
    def execute(request, settings, root, mounts, env):
        calls.append((request, mounts))
        return {"output": json.dumps({"directive": "child", "hypothesis": "A clearer explanation."}), "artifact_paths": []}
    runtime = Runtime(SOURCE, executor=execute, contracts=ContractsDouble())
    result = runtime.propose("parent", [{"case": "training", "feedback": "Explain the condition."}], tmp_path,
                             {"harness": "codex", "model": "configured"}, {"version": "fixture"})
    assert result["directive"] == "child"
    request, mounts = calls[0]
    assert "training_feedback" in request["prompt"]
    assert len(mounts) == 1
    assert mounts[0]["target"] == "/workspace/source"
    assert "final" not in request["prompt"]


def test_separate_writing_skill_refused(tmp_path):
    runtime = Runtime(SOURCE, executor=lambda *_: {}, contracts=ContractsDouble())
    with pytest.raises(RuntimeErrorWithEvidence, match="duplicates"):
        runtime.assemble_input({"brief_parts": [], "project_skills": [{"name": "writing", "body": "duplicate"}]}, "merged", "eval-run")


def test_render_failure_requires_independent_failure_in_the_uninspected_section():
    report = {"render": {"failed_images": [{"id": "before", "section": "Visual"}], "failed_media": []}}
    assert _render_failures({"uninspected_content": ["Visual"]}, report) == [{"kind": "failed_images", "id": "before", "section": "Visual"}]
    assert _render_failures({"uninspected_content": ["Risk and rollback"]}, report) == []
    assert _render_failures({"uninspected_content": []}, report) == []
    assert _render_failures({"uninspected_content": ["Visual"]}, {"render": {}}) == []


def test_scope_judge_receives_no_case_files(tmp_path):
    calls = []
    def execute(request, settings, root, mounts, env):
        calls.append((request, mounts))
        return {"output": '{"permitted":true,"reasons":[]}', "artifact_paths": []}
    runtime = Runtime(SOURCE, executor=execute, contracts=ContractsDouble())
    result = runtime.assess_edit("parent", "child", {"semantic_requirements": ["Keep the glossary."]},
                                 {"harness": "claude", "model": "configured", "effort": "high", "trial_root": str(tmp_path)})
    assert result["permitted"] is True
    assert calls[0][1][0]["read_only"] is True
    assert calls[0][0]["output_schema"]["properties"]["permitted"] == {"type": "boolean"}
    assert list(Path(calls[0][1][0]["source"]).iterdir()) == []


@pytest.mark.parametrize("output", ["{", "```json\n{\n```", "[]", "null", "true", '"text"', 7, None, '{} {}'])
def test_response_requires_one_object_and_retains_failure_artifacts(output):
    artifacts = [{"type": "txt", "path": "/fixture/response.txt"}]
    with pytest.raises(RuntimeErrorWithEvidence) as caught:
        _json_response(output, artifacts)
    assert caught.value.artifact_paths == artifacts


@pytest.mark.parametrize("output", ['{"directive":"child"}', '```json\n{"directive":"child"}\n```'])
def test_response_accepts_plain_and_fenced_objects(output):
    assert _json_response(output) == {"directive": "child"}


@pytest.mark.parametrize("role", ["optimizer", "scope_judge", "judge"])
@pytest.mark.parametrize("output", ["```json\n{\n```", "[]"])
def test_role_parse_failure_preserves_harness_and_browser_files(tmp_path, monkeypatch, role, output):
    harness_paths = []

    def execute(request, settings, root, mounts, env):
        root.mkdir(parents=True)
        response = root / "response.txt"
        response.write_text(output)
        events = root / "events.jsonl"
        events.write_text('{"event":"fixture"}\n')
        harness_paths.extend([{"type": "txt", "path": str(response)}, {"type": "jsonl", "path": str(events)}])
        return {"output": output, "artifact_paths": harness_paths}

    class BrowserDouble:
        def __init__(self, source, packet, destination, *_args):
            destination.mkdir(parents=True)
            (destination / "screenshot.txt").write_text("The browser artifact fixture.")

        def __enter__(self):
            return self

        def __exit__(self, *_args):
            return None

        def report(self):
            return {"visible_headings": ["Summary"], "render": {}}

    class ServerDouble:
        url, token = "http://fixture.invalid/tool", "fixture"

        def __init__(self, _browser):
            pass

        def __enter__(self):
            return self

        def __exit__(self, *_args):
            return None

    monkeypatch.setattr("culler_evals.runtime.PacketBrowser", BrowserDouble)
    monkeypatch.setattr("culler_evals.runtime.BrowserServer", ServerDouble)
    monkeypatch.setattr(subprocess, "run", lambda argv, **_fields: subprocess.CompletedProcess(argv, 0, "The source diff."))
    runtime = Runtime(SOURCE, executor=execute, contracts=ContractsDouble())
    settings = {"harness": "claude", "model": "fixture", "trial_root": str(tmp_path)}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    with pytest.raises(RuntimeErrorWithEvidence) as caught:
        if role == "optimizer":
            runtime.propose("parent", [], tmp_path, settings, {})
        elif role == "scope_judge":
            runtime.assess_edit("parent", "child", {}, settings)
        else:
            case = {"case_id": "fixture", "repository_path": str(tmp_path), "artifacts_path": str(tmp_path),
                    "references_path": str(tmp_path), "base_commit": "base", "reviewed_commit": "reviewed", "references": {}}
            runtime.assess_packet(case, tmp_path / "packet.html", tmp_path, settings, {})
    assert all(item in caught.value.artifact_paths for item in harness_paths)
    assert all(Path(item["path"]).is_file() for item in caught.value.artifact_paths)
    if role == "judge":
        assert {Path(item["path"]).name for item in caught.value.artifact_paths} >= {"screenshot.txt", "report.json"}


def test_judge_prompt_transfers_current_semantics_and_fixed_operator_context(tmp_path, monkeypatch):
    from culler_evals.assessment import JUDGE_INSTRUCTIONS, SECTIONS

    requests = []

    def execute(request, settings, root, mounts, env):
        requests.append((request, mounts))
        return {"output": "{}", "artifact_paths": []}

    def browser(source, packet, destination, *_args):
        destination.mkdir(parents=True)
        return nullcontext(SimpleNamespace(report=lambda: {"visible_headings": list(SECTIONS), "render": {}}))

    monkeypatch.setattr("culler_evals.runtime.PacketBrowser", browser)
    monkeypatch.setattr("culler_evals.runtime.BrowserServer",
                        lambda browser: nullcontext(SimpleNamespace(url="http://fixture.invalid/tool", token="fixture")))
    monkeypatch.setattr(subprocess, "run",
                        lambda argv, **kwargs: subprocess.CompletedProcess(argv, 0, "The fixed reviewed diff."))
    context = json.loads((SOURCE / "evals/config/packager.json").read_text())["operator_context"]
    case = {"case_id": "fixture", "repository_path": str(tmp_path), "artifacts_path": str(tmp_path),
            "references_path": str(tmp_path), "base_commit": "fixed-base", "reviewed_commit": "fixed-reviewed", "references": {}}
    runtime = Runtime(SOURCE, executor=execute, contracts=ContractsDouble())
    runtime.assess_packet(case, tmp_path / "packet.html", tmp_path,
                          {"harness": "claude", "model": "fixture"}, {"operator_context": context})
    request, mounts = requests[0]
    from culler_evals.assessment import judge_output_schema
    assert request["output_schema"] == judge_output_schema({"operator_context": context})
    payload = json.loads(request["prompt"][len(JUDGE_INSTRUCTIONS):])
    assert payload["rubric"]["operator_context"] == context
    assert "Project code, documents, wiki, and interface text before the reviewed change" in payload["semantic_contracts"]["glossary"]
    assert payload["reviewed_range"] == {"base_commit": "fixed-base", "reviewed_commit": "fixed-reviewed"}
    assert next(mount for mount in mounts if mount["target"] == "/evidence/source")["read_only"] is True
    for phrase in ("pipeline roles", "lacks context", "omits Culler", "Paths under /mc/artifacts/",
                   "without a Verification history section or artifact citations", "where it first appears", "in the Technical section"):
        assert phrase not in request["prompt"]


def test_interrupt_removes_only_its_evaluation_container(tmp_path, monkeypatch):
    calls = []
    interruption = KeyboardInterrupt()

    def execute(argv, **fields):
        calls.append((argv, fields))
        if argv[1] == "run":
            assert fields["input"] == "The complete directive and case brief."
            assert "--interactive" in argv
            raise interruption
        assert argv == ["docker", "rm", "--force", "culler-eval-interrupt-fixture"]
        assert fields["timeout"] == 10
        return subprocess.CompletedProcess(argv, 0)

    monkeypatch.setattr(subprocess, "run", execute)
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=runtime_source_manifest(SOURCE),
                      product_manifest=product_source_manifest(SOURCE), snapshot_root=tmp_path / "runtime-source")
    request = {"harness": "codex", "effort": "high", "role": "optimizer", "workspace": "/workspace/source",
               "prompt": "The complete directive and case brief."}
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "channel": "codex", "base_url": "", "_container_name": "culler-eval-interrupt-fixture"}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    with pytest.raises(KeyboardInterrupt) as caught:
        runtime._docker_execute(request, settings, tmp_path / "execution", [], {})
    assert caught.value is interruption
    assert len(calls) == 2


@pytest.mark.parametrize("field,value", [("channel", "none"), ("base_url", "http://fixture.invalid"),
                                        ("env", {"OPENAI_BASE_URL": "http://fixture.invalid"})])
def test_actual_credential_transport_cannot_override_the_frozen_configuration(tmp_path, monkeypatch, field, value):
    def forbidden(*args, **kwargs):
        pytest.fail("The runtime must refuse transport drift before a Docker invocation.")
    monkeypatch.setattr(subprocess, "run", forbidden)
    manifest = runtime_source_manifest(SOURCE)
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=manifest,
                      product_manifest=product_source_manifest(SOURCE), snapshot_root=tmp_path / "runtime-source")
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "channel": "codex", "base_url": "",
                "credentials": {"channel": "codex", "base_url": "", field: value}}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    request = {"harness": "codex", "effort": "high", "role": "optimizer", "workspace": "/workspace/source", "prompt": "The fixed request."}
    with pytest.raises(RuntimeErrorWithEvidence, match="differs") as caught:
        runtime._docker_execute(request, settings, tmp_path / "execution", [], {})
    assert {Path(item["path"]).name for item in caught.value.artifact_paths} >= {"prompt.txt", "browser-tools.json"}


def test_container_mounts_frozen_runtime_bytes_and_records_their_identity(tmp_path, monkeypatch):
    manifest = runtime_source_manifest(SOURCE)
    snapshot = tmp_path / "runtime-source"
    def execute(argv, **fields):
        assert f"type=bind,src={snapshot / 'runner'},dst=/app/src,readonly" in argv
        assert f"type=bind,src={snapshot / 'evals/bridge'},dst=/app/eval,readonly" in argv
        assert not any(f"src={SOURCE / 'runner'}," in argument for argument in argv)
        fields["stdout"].write(json.dumps({"event": "runtime-config", "harness_version": "fixture-version", "requested_effort": "high", "configured_effort": "high"}) + "\n")
        fields["stdout"].write(json.dumps({"event": "turn-complete", "status": "success", "output": "{}", "model": "configured"}) + "\n")
        return subprocess.CompletedProcess(argv, 0)
    monkeypatch.setattr(subprocess, "run", execute)
    product_manifest = product_source_manifest(SOURCE)
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=manifest,
                      product_manifest=product_manifest, snapshot_root=snapshot)
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "channel": "codex", "base_url": ""}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    request = {"harness": "codex", "effort": "high", "role": "optimizer", "workspace": "/workspace/source", "prompt": "The fixed request."}
    result = runtime._docker_execute(request, settings, tmp_path / "execution", [], {})
    assert result["metadata"]["runtime_source_hash"] == runtime_source_hash(manifest)
    assert result["metadata"]["product_source_hash"] == product_source_hash(product_manifest)
    assert result["metadata"]["channel"] == "codex"
    assert result["metadata"]["base_url"] == ""
    assert result["metadata"]["requested_effort"] == "high"
    assert result["metadata"]["configured_effort"] == "high"
    assert result["metadata"]["observed_efforts"] is None


@pytest.mark.parametrize("effort", [None, "", "unrecognized", 7])
def test_real_runtime_refuses_missing_or_invalid_effort_before_source_or_model_access(tmp_path, effort):
    runtime = Runtime(SOURCE, contracts=ContractsDouble())
    request = {"harness": "codex", "effort": effort}
    settings = {"image": "sha256:fixture", "model": "configured", "effort": effort}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    with pytest.raises(RuntimeErrorWithEvidence, match="explicit effort"):
        runtime._docker_execute(request, settings, tmp_path / "execution", [], {})
    assert not (tmp_path / "execution").exists()


def test_real_runtime_refuses_effort_drift_before_source_or_model_access(tmp_path):
    runtime = Runtime(SOURCE, contracts=ContractsDouble())
    with pytest.raises(RuntimeErrorWithEvidence, match="requested effort differs"):
        runtime._docker_execute({"harness": "codex", "effort": "low"},
                                {"image": "sha256:fixture", "model": "configured", "effort": "high"},
                                tmp_path / "execution", [], {})
    assert not (tmp_path / "execution").exists()


@pytest.mark.parametrize("configured,observed,accepted", [("high", "high", True), ("low", "high", False),
                                                         (None, "high", False), ("high", "low", False)])
@pytest.mark.parametrize("harness", ["codex", "claude"])
def test_runtime_binds_effort_configuration_and_native_observation(tmp_path, monkeypatch, configured, observed, accepted, harness):
    def execute(argv, **fields):
        request_path = next(item.split("src=", 1)[1].split(",dst=", 1)[0]
                            for item in argv if "dst=/eval/request.json,readonly" in item)
        assert json.loads(Path(request_path).read_text())["effort"] == "high"
        if harness == "codex":
            session = tmp_path / "execution/container/session"
            session.mkdir()
            (session / "native.jsonl").write_text(json.dumps({"type": "turn_context", "payload": {"model": "configured", "effort": observed}}) + "\n")
        else:
            fields["stdout"].write(json.dumps({"type": "system", "subtype": "init", "model": "configured", "effort": observed}) + "\n")
        fields["stdout"].write(json.dumps({"event": "runtime-config", "requested_effort": "high", "configured_effort": configured}) + "\n")
        fields["stdout"].write(json.dumps({"event": "turn-complete", "status": "success", "output": "{}", "model": "configured"}) + "\n")
        return subprocess.CompletedProcess(argv, 0)
    monkeypatch.setattr(subprocess, "run", execute)
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=runtime_source_manifest(SOURCE),
                      product_manifest=product_source_manifest(SOURCE), snapshot_root=tmp_path / "snapshot")
    request = {"harness": harness, "effort": "high", "role": "optimizer", "workspace": "/workspace/source", "prompt": "Fixed request."}
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "channel": harness, "base_url": ""}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    if accepted:
        result = runtime._docker_execute(request, settings, tmp_path / "execution", [], {})
        assert result["metadata"]["observed_efforts"] == ["high"]
    else:
        with pytest.raises(RuntimeErrorWithEvidence) as caught:
            runtime._docker_execute(request, settings, tmp_path / "execution", [], {})
        assert any(Path(item["path"]).name == "events.jsonl" for item in caught.value.artifact_paths)


def test_real_runtime_requires_a_frozen_manifest_and_declared_transport(tmp_path):
    request = {"harness": "codex", "effort": "high", "role": "optimizer", "workspace": "/workspace/source", "prompt": "The fixed request."}
    runtime = Runtime(SOURCE, contracts=ContractsDouble())
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "channel": "codex", "base_url": ""}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    with pytest.raises(RuntimeErrorWithEvidence, match="frozen runtime source manifest"):
        runtime._docker_execute(request, settings, tmp_path / "execution", [], {})
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=runtime_source_manifest(SOURCE))
    with pytest.raises(RuntimeErrorWithEvidence, match="declared credential"):
        runtime._docker_execute(request, {"image": "sha256:fixture", "model": "configured", "effort": "high"}, tmp_path / "execution", [], {})
    with pytest.raises(RuntimeErrorWithEvidence, match="frozen product source manifest"):
        runtime._docker_execute(request, settings, tmp_path / "execution", [], {})


def test_packager_copy_preserves_the_authoritative_case(tmp_path):
    class ProjectionDouble(ContractsDouble):
        def call(self, operation, **fields):
            return {**super().call(operation, **fields), "omitted_parts": ["Verdict history"]}

    source = tmp_path / "case/repository"
    source.mkdir(parents=True)
    def git(*argv):
        return subprocess.run(["git", "-C", str(source), *argv], check=True, capture_output=True, text=True).stdout.strip()
    git("init", "--quiet")
    git("config", "user.name", "Evaluation fixture")
    git("config", "user.email", "fixture@example.invalid")
    (source / "feature.txt").write_text("The original authoritative source.")
    git("add", ".")
    git("commit", "--quiet", "-m", "The reviewed contribution")
    commit = git("rev-parse", "HEAD")
    evidence = tmp_path / "case/artifacts"
    (evidence / "eval/701").mkdir(parents=True)
    (evidence / "eval/701/report.txt").write_text("The authoritative upstream report.")
    def execute(request, settings, root, mounts, env):
        role_source = Path(next(mount["source"] for mount in mounts if mount["target"] == "/workspace/source"))
        role_evidence = Path(next(mount["source"] for mount in mounts if mount["target"] == "/mc/artifacts/eval"))
        assert role_source != source
        assert role_evidence != evidence / "eval"
        (role_source / "feature.txt").write_text("The role changed its private source copy.")
        (role_source / ".git/config").write_text("The role corrupted its private Git configuration.")
        (role_evidence / "701/report.txt").write_text("The role changed its private evidence copy.")
        return {"output": "No terminal.", "artifact_paths": []}
    case = {"task_id": "701", "repository_path": str(source), "artifacts_path": str(evidence), "base_commit": commit,
            "reviewed_commit": commit, "brief_parts": [], "project_skills": [], "readiness": {"ready": True}}
    runtime = Runtime(SOURCE, executor=execute, contracts=ProjectionDouble())
    result = runtime.execute_packager(case, "The directive.", tmp_path / "trial", {"harness": "codex", "model": "fixture"})
    assert (source / "feature.txt").read_text() == "The original authoritative source."
    assert git("rev-parse", "HEAD") == commit
    assert git("status", "--porcelain") == ""
    assert (evidence / "eval/701/report.txt").read_text() == "The authoritative upstream report."
    assert result["metadata"]["evidence_changed"] == ["eval/701/report.txt"]
    assert result["metadata"]["omitted_parts"] == ["Verdict history"]


@pytest.mark.parametrize("configured", [True, False, None])
def test_runtime_requests_and_records_codex_fast_mode(tmp_path, monkeypatch, configured):
    def execute(argv, **fields):
        request_path = next(argument.split("src=", 1)[1].split(",dst=", 1)[0]
                            for argument in argv if "dst=/eval/request.json" in argument)
        assert json.loads(Path(request_path).read_text())["fast_mode"] is True
        fields["stdout"].write(json.dumps({"event": "runtime-config", "requested_effort": "high",
            "configured_effort": "high", "requested_fast_mode": True, "configured_fast_mode": configured,
            "configured_service_tier": "fast"}) + "\n")
        fields["stdout"].write(json.dumps({"event": "turn-complete", "status": "success",
            "output": "{}", "model": "configured"}) + "\n")
        return subprocess.CompletedProcess(argv, 0)
    monkeypatch.setattr(subprocess, "run", execute)
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=runtime_source_manifest(SOURCE),
                      product_manifest=product_source_manifest(SOURCE), snapshot_root=tmp_path / "source")
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "fast_mode": True,
                "harness": "codex", "channel": "codex", "base_url": ""}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    if configured is True:
        result = runtime._execute("optimizer", "Fixed request.", tmp_path / "trial", settings)
        assert result["metadata"]["requested_fast_mode"] is True
        assert result["metadata"]["configured_fast_mode"] is True
        assert result["metadata"]["configured_service_tier"] == "fast"
    else:
        with pytest.raises(RuntimeErrorWithEvidence, match="fast mode configuration record"):
            runtime._execute("optimizer", "Fixed request.", tmp_path / "trial", settings)


@pytest.mark.parametrize("enabled", [True, False])
@pytest.mark.parametrize("configured", [True, False, None])
def test_runtime_records_claude_fast_mode_and_fallback(tmp_path, monkeypatch, enabled, configured):
    def execute(argv, **fields):
        request_path = next(argument.split("src=", 1)[1].split(",dst=", 1)[0]
                            for argument in argv if "dst=/eval/request.json" in argument)
        assert json.loads(Path(request_path).read_text())["fast_mode"] is enabled
        for event in (
            {"event": "runtime-config", "requested_effort": "high", "configured_effort": "high",
             "requested_fast_mode": enabled, "configured_fast_mode": configured},
            {"type": "system", "subtype": "init", "fast_mode_state": "on" if enabled else "off"},
            {"type": "system", "subtype": "status", "fast_mode_state": "cooldown", "fast_mode_disabled_reason": "network_error"},
            {"event": "turn-complete", "status": "success", "output": "{}", "model": "configured"},
        ):
            fields["stdout"].write(json.dumps(event) + "\n")
        return subprocess.CompletedProcess(argv, 0)
    monkeypatch.setattr(subprocess, "run", execute)
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=runtime_source_manifest(SOURCE),
                      product_manifest=product_source_manifest(SOURCE), snapshot_root=tmp_path / "source")
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "fast_mode": enabled,
                "harness": "claude", "channel": "claude", "base_url": ""}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    if configured is enabled:
        result = runtime._execute("scope_judge", "Fixed request.", tmp_path / "trial", settings)
        metadata = result["metadata"]
        assert metadata["requested_fast_mode"] is enabled
        assert metadata["configured_fast_mode"] is enabled
        assert metadata["configured_service_tier"] is None
        assert metadata["fast_mode_observations"] == [
            {"fast_mode_state": "on" if enabled else "off"},
            {"fast_mode_state": "cooldown", "fast_mode_disabled_reason": "network_error"},
        ]
    else:
        with pytest.raises(RuntimeErrorWithEvidence, match="fast mode configuration record"):
            runtime._execute("scope_judge", "Fixed request.", tmp_path / "trial", settings)


@pytest.mark.parametrize("configuration,result_format", [(True, "json_schema"), (False, "json_schema"), (True, None)])
def test_runtime_requires_the_configured_schema_and_validated_result(tmp_path, monkeypatch, configuration, result_format):
    from culler_evals.assessment import scope_judge_output_schema
    from culler_evals.protocol import digest

    schema = scope_judge_output_schema()

    def execute(argv, **fields):
        request_path = next(argument.split("src=", 1)[1].split(",dst=", 1)[0]
                            for argument in argv if "dst=/eval/request.json" in argument)
        assert json.loads(Path(request_path).read_text())["output_schema"] == schema
        fields["stdout"].write(json.dumps({"event": "runtime-config", "requested_effort": "high", "configured_effort": "high",
            "configured_output_format": "json_schema", "configured_output_schema": schema if configuration else {}}) + "\n")
        fields["stdout"].write(json.dumps({"event": "turn-complete", "status": "success", "output": '{"permitted":true,"reasons":[]}',
                                          "output_format": result_format, "model": "configured"}) + "\n")
        return subprocess.CompletedProcess(argv, 0)

    monkeypatch.setattr(subprocess, "run", execute)
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=runtime_source_manifest(SOURCE),
                      product_manifest=product_source_manifest(SOURCE), snapshot_root=tmp_path / "source")
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "harness": "claude", "channel": "claude", "base_url": ""}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    if configuration and result_format:
        result = runtime._execute("scope_judge", "Fixed request.", tmp_path / "trial", settings, output_schema=schema)
        assert result["metadata"]["output_format"] == "json_schema"
        assert result["metadata"]["output_schema_hash"] == digest(schema)
        assert _json_response(result["output"]) == {"permitted": True, "reasons": []}
    else:
        with pytest.raises(RuntimeErrorWithEvidence, match="validated structured output record"):
            runtime._execute("scope_judge", "Fixed request.", tmp_path / "trial", settings, output_schema=schema)


def test_proxy_access_stays_in_private_read_only_mount_and_is_removed_after_failure(tmp_path, monkeypatch):
    credentials = proxy_credentials()
    token = credentials["provider_access"]["token"]
    mounted = []

    def execute(argv, **fields):
        access_mount = next(value for value in argv if "dst=/mc/provider-access.json" in value)
        assert access_mount.endswith(",readonly")
        path = Path(access_mount.split("src=", 1)[1].split(",dst=", 1)[0])
        mounted.append(path)
        assert json.loads(path.read_text()) == credentials["provider_access"]
        assert path.parent.stat().st_mode & 0o777 == 0o700
        environment = Path(argv[argv.index("--env-file") + 1]).read_text()
        assert token not in environment
        assert "MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json" in environment
        assert not any("auth.json" in argument or "refresh-grants" in argument for argument in argv)
        fields["stdout"].write(json.dumps({"reflection": token}) + "\n")
        fields["stderr"].write(token)
        return subprocess.CompletedProcess(argv, 1)

    monkeypatch.setattr(subprocess, "run", execute)
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=runtime_source_manifest(SOURCE),
                      product_manifest=product_source_manifest(SOURCE), snapshot_root=tmp_path / "snapshot")
    request = {"harness": "codex", "effort": "high", "role": "optimizer", "workspace": "/workspace/source", "prompt": "Fixed request."}
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "channel": "codex", "base_url": "", "credentials": credentials}
    with pytest.raises(RuntimeErrorWithEvidence, match="infrastructure failure") as error:
        runtime._docker_execute(request, settings, tmp_path / "execution", [], {})
    assert mounted and not mounted[0].exists()
    for artifact in error.value.artifact_paths:
        assert token not in Path(artifact["path"]).read_text()
        assert Path(artifact["path"]).name != "provider-access.json"


@pytest.mark.parametrize("field", ["auth_json", "authJson", "env", "mounts"])
def test_runtime_rejects_raw_provider_credentials_before_docker(tmp_path, monkeypatch, field):
    monkeypatch.setattr(subprocess, "run", lambda *_a, **_k: pytest.fail("Raw credentials must reach no container."))
    runtime = Runtime(SOURCE, contracts=ContractsDouble(), source_manifest=runtime_source_manifest(SOURCE),
                      product_manifest=product_source_manifest(SOURCE), snapshot_root=tmp_path / "snapshot")
    request = {"harness": "codex", "effort": "high", "role": "optimizer", "workspace": "/workspace/source", "prompt": "Fixed request."}
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "channel": "codex", "base_url": "",
                "credentials": proxy_credentials() | {field: "synthetic-provider-secret"}}
    with pytest.raises(RuntimeErrorWithEvidence, match="saved Culler connection"):
        runtime._docker_execute(request, settings, tmp_path / "execution", [], {})


@pytest.mark.parametrize("duration", [0, 1801, True, 3.5])
def test_runtime_rejects_an_unbounded_session_before_credentials(tmp_path, duration):
    runtime = Runtime(SOURCE, contracts=ContractsDouble())
    with pytest.raises(RuntimeErrorWithEvidence, match="timeout from 1 to 1800"):
        runtime._docker_execute({"harness": "codex"}, {"image": "sha256:fixture", "model": "exact", "timeout_seconds": duration},
                                tmp_path / "execution", [], {})
