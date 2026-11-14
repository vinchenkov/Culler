import json
import subprocess
import sys
from pathlib import Path

from culler_evals.cli import execute, parser
from culler_evals import cli
from culler_evals.protocol import source_revision
from culler_evals.records import RecordStore


def test_unset_configuration_fails_without_model_calls(tmp_path):
    protocol = tmp_path / "incomplete.json"
    protocol.write_text(json.dumps({"version": 1, "id": "deliberately-incomplete", "role": "packager",
                                   "runtime": {"packager": {"model": None}},
                                   "datasets": {"trainset": [], "valset": [], "final": []}}))
    result = subprocess.run([sys.executable, "-m", "culler_evals.cli", "--json", "validate", "--protocol", str(protocol)],
                            capture_output=True, text=True)
    assert result.returncode == 1
    report = json.loads(result.stdout)
    assert report["ready"] is False
    assert "Set runtime.packager.model." in report["missing_inputs"]


def test_native_demo_exports_complete_chronology_and_artifacts(tmp_path):
    output = tmp_path / "demo"
    args = parser().parse_args(["demo", "--output", str(output)])
    result = execute(args)
    assert result["model_calls"] == 0
    assert result["complete"] is True
    document = Path(result["viewer"]).read_text()
    assert "Candidate ancestry" in document
    assert "Training set (trainset)" in document
    with RecordStore(output, read_only=True) as store:
        assert any(event["kind"] == "minibatch_decision" for event in store.events())
        assert store.completeness()["complete"]


def test_source_export_preserves_original_bodies(tmp_path):
    root = Path(__file__).resolve().parents[2]
    result = execute(parser().parse_args(["original", "--source", str(root), "--output", str(tmp_path)]))
    text = Path(result["directive"]).read_text()
    assert (root / "mc/verbs/directives/packager.md").read_text() in text
    writing_body = (root / "mc/verbs/skills/shared/writing/SKILL.md").read_text()[4:].partition("\n---\n")[2]
    assert writing_body in text
    assert json.loads((tmp_path / "protected.json").read_text())["spans"]


def test_contract_command_requires_no_judge_or_quality_configuration(tmp_path, monkeypatch):
    root = Path(__file__).resolve().parents[1]
    protocol = {"version": 1, "id": "contract-only", "role": "packager",
                "source_revision": source_revision(cli.SOURCE),
                "runtime": {"packager": {"model": "test-model", "harness": "codex", "effort": "high", "image": "sha256:" + "a" * 64}},
                "retry": {"external_attempts": 1}}
    path = tmp_path / "protocol.json"
    path.write_text(json.dumps(protocol))
    closed = []
    class ContractEvaluator:
        class runtime:
            @staticmethod
            def close():
                closed.append(True)
        def evaluate_contract(self, candidate, cases, *, trial_count):
            assert trial_count == 1
            assert "packager_directive" in candidate
            return [{"case_id": case["id"], "passed": True, "quality_score": None} for case in cases]
    monkeypatch.setattr(cli, "_runtime", lambda *args: ContractEvaluator())
    result = execute(parser().parse_args(["contract", "--protocol", str(path), "--cases",
                     str(root / "cases/memos/memos-link-cover-retry.json"), "--trials", "1",
                     "--output", str(tmp_path / "experiment"), "--cache", str(tmp_path / "cache")]))
    assert result["contract_passed"] is True
    assert result["quality_score"] is None
    assert closed == [True]
    with RecordStore(tmp_path / "experiment", read_only=True) as store:
        assert store.read_manifest("protocol")["contract_trials"] == 1


def test_validate_resolves_checkout_requirements_without_models(tmp_path, monkeypatch):
    protocol = tmp_path / "checkout.json"
    original = {"version": 1, "id": "source-readiness", "role": "packager", "source_revision": "checkout",
                "assessment_status": "uncalibrated", "datasets": {"trainset": [], "valset": [], "final": []}}
    protocol.write_text(json.dumps(original))
    def unexpected_runtime(*_):
        raise AssertionError("The readiness command must not call a model.")
    monkeypatch.setattr(cli, "_runtime", unexpected_runtime)
    report = execute(parser().parse_args(["validate", "--protocol", str(protocol)]))
    assert report["ready"] is False
    assert "Set source_revision." not in report["missing_inputs"]
    assert "Set protected.spans." not in report["missing_inputs"]
    assert "Set protected.semantic_requirements." not in report["missing_inputs"]
    assert json.loads(protocol.read_text()) == original
