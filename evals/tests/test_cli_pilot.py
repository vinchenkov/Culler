import json
import os
import sys
from pathlib import Path

import pytest

from culler_evals import cli
from culler_evals.cases import validate_cases
from culler_evals.protocol import digest, read_protocol, readiness, source_revision
from culler_evals.records import RecordStore


CONFIG_ROOT = Path(__file__).resolve().parents[1] / "config"


@pytest.fixture
def pilot(tmp_path):
    protocol = json.loads((CONFIG_ROOT / "packager.pilot.json").read_text())
    protocol["source_revision"] = source_revision(cli.SOURCE)
    protocol["rubric_file"] = str(CONFIG_ROOT / protocol["rubric_file"])
    paths = [(CONFIG_ROOT / value).resolve() for value in protocol["pilot"]["cases"]]
    protocol["pilot"]["cases"] = [str(path) for path in paths]
    path = tmp_path / "pilot.json"
    path.write_text(json.dumps(protocol))
    return path, protocol, paths


def arguments(pilot, tmp_path, paths=None, directive=None):
    protocol_path, _, configured_paths = pilot
    values = ["evaluate", "--protocol", str(protocol_path), "--cases"]
    values += [str(path) for path in (configured_paths if paths is None else paths)]
    values += ["--output", str(tmp_path / "experiment"), "--cache", str(tmp_path / "cache")]
    if directive is not None:
        values += ["--directive", str(directive)]
    return cli.parser().parse_args(values)


def fake_runtime(monkeypatch):
    calls = []
    class PilotEvaluator:
        class runtime:
            @staticmethod
            def close():
                calls.append({"closed": True})

        def evaluate(self, candidate, cases, stage, capture_traces):
            calls.append({"candidate": candidate, "cases": cases, "stage": stage, "capture_traces": capture_traces})
            return {"outputs": [{} for _ in cases], "scores": [.5 for _ in cases], "trajectories": []}

    def build(args, protocol, store):
        assert protocol["trials"]["pilot"] == 3
        calls.append({"runtime": True})
        return PilotEvaluator()
    monkeypatch.setattr(cli, "_runtime", build)
    return calls


def test_approved_pilot_is_ready_without_evolution_inputs_or_calibration(pilot):
    path, _, paths = pilot
    protocol = read_protocol(path)
    cases = validate_cases(paths, require_ready=True)
    assert len(cases) == 5
    assert len({case["group"] for case in cases}) == 5
    assert not any(case["visual_evidence"]["required"] for case in cases)
    assert readiness(protocol, {"pilot": cases}, full=False) == []
    assert protocol["rubric"]["status"] != "calibrated"
    assert {dimension["weight"] for dimension in protocol["rubric"]["dimensions"].values()} == {1}
    assert any("Complete the Judge calibration" in error for error in readiness(protocol, {"trainset": [], "valset": [], "final": []}))
    evolution = read_protocol(CONFIG_ROOT / "packager.json")
    assert evolution["assessment_status"] == "uncalibrated"
    assert all(dimension["weight"] == 1 for dimension in evolution["rubric"]["dimensions"].values())


def test_pilot_freezes_actual_cases_candidate_and_exploratory_status(pilot, tmp_path, monkeypatch):
    calls = fake_runtime(monkeypatch)
    directive = tmp_path / "directive.md"
    directive.write_text("The pilot evaluates this explicit directive.")
    result = cli.execute(arguments(pilot, tmp_path, directive=directive))
    assert result["status"] == "exploratory pilot complete"
    assert result["assessment_status"] == "exploratory"
    assert result["scores"] == [.5] * 5
    evaluation = next(call for call in calls if "candidate" in call)
    assert evaluation["stage"] == "pilot"
    assert evaluation["capture_traces"] is True
    assert evaluation["candidate"] == {"packager_directive": directive.read_text()}
    with RecordStore(tmp_path / "experiment", read_only=True) as store:
        frozen = store.read_manifest("protocol")
        corpus = store.read_manifest("case_corpus")
        assert frozen["case_hashes"]["pilot"] == {case["id"]: digest(case) for case in corpus["pilot"]}
        assert corpus["pilot"] == evaluation["cases"]
        assert frozen["evaluated_candidate"] == evaluation["candidate"]
        assert frozen["assessment_status"] == "exploratory"
        assert frozen["protocol_hash"] == digest({key: value for key, value in frozen.items() if key != "protocol_hash"})
        assert store.read_manifest("pilot")["assessment_status"] == "exploratory"
        assert not store.read_manifest("search_result")
    assert calls[-1] == {"closed": True}


@pytest.mark.parametrize("reorder", [False, True])
def test_pilot_rejects_case_selection_changes_before_runtime(pilot, tmp_path, monkeypatch, reorder):
    calls = fake_runtime(monkeypatch)
    paths = list(pilot[2])
    paths = list(reversed(paths)) if reorder else paths[1:]
    with pytest.raises(ValueError, match="Use the cases listed in pilot.cases, in that order"):
        cli.execute(arguments(pilot, tmp_path, paths))
    assert calls == []
    assert not (tmp_path / "experiment").exists()


def test_pilot_rejects_final_case_groups_before_runtime(pilot, tmp_path, monkeypatch):
    calls = fake_runtime(monkeypatch)
    path, protocol, paths = pilot
    protocol["datasets"]["final"] = [str(paths[0])]
    path.write_text(json.dumps(protocol))
    with pytest.raises(ValueError, match="pilot cannot use final case groups"):
        cli.execute(arguments(pilot, tmp_path))
    assert calls == []
    assert not (tmp_path / "experiment").exists()


def test_pilot_cannot_reuse_output_with_a_changed_directive(pilot, tmp_path, monkeypatch):
    calls = fake_runtime(monkeypatch)
    directive = tmp_path / "directive.md"
    directive.write_text("The first explicit directive.")
    cli.execute(arguments(pilot, tmp_path, directive=directive))
    directive.write_text("The second explicit directive.")
    with pytest.raises(ValueError, match="preserve its frozen protocol"):
        cli.execute(arguments(pilot, tmp_path, directive=directive))
    assert len([call for call in calls if "runtime" in call]) == 1


def test_pilot_rubric_preserves_every_approved_anchor_and_assessment_rule():
    approved = json.loads((CONFIG_ROOT / "rubric.draft.json").read_text())
    provisional = json.loads((CONFIG_ROOT / "rubric.pilot.json").read_text())
    for dimension in provisional["dimensions"].values():
        dimension["weight"] = None
    provisional.pop("calibration_status")
    provisional.pop("weight_status")
    assert provisional == approved


@pytest.mark.parametrize("width", [80, 40])
def test_pilot_help_explains_required_inputs_within_terminal_width(monkeypatch, capsys, width):
    monkeypatch.setattr(cli.shutil, "get_terminal_size", lambda *args: os.terminal_size((width, 24)))
    with pytest.raises(SystemExit) as error:
        cli.parser().parse_args(["evaluate", "--help"])
    assert error.value.code == 0
    output = capsys.readouterr().out
    assert all(len(line) <= width for line in output.splitlines())
    text = " ".join(output.split())
    assert "Run Packager trials and Judge assessments on development cases." in text
    assert "Scores from an uncalibrated Judge remain exploratory." in text
    assert "Use their order from pilot.cases when that field exists." in text
    assert "Save trial records and the viewer in this directory." in text
    assert "Use this private credential configuration." in text


def test_terminal_wrap_preserves_long_paths_and_case_identifiers(monkeypatch, capsys):
    monkeypatch.setattr(cli.shutil, "get_terminal_size", lambda *args: os.terminal_size((40, 24)))
    path = "/private/tmp/culler-eval-pilot/experiment/viewer/index.html"
    identifier = "memos-webhook-label-refactor"
    cli.message("Viewer: " + path, stream=sys.stdout)
    cli.message("Trial 2 of 3; attempt 1: pilot; case " + identifier, stream=sys.stdout)
    output = capsys.readouterr().out
    assert path in output
    assert identifier in output
