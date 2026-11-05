import json
import errno
from pathlib import Path

import pytest

from culler_evals import cli
from culler_evals.records import RecordStore


def configured_run(tmp_path, monkeypatch):
    cases = {name: [{"id": name + str(index), "group": name + str(index), "types": ["performance"]}
                    for index in range(2)] for name in ("trainset", "valset", "final")}
    protocol = {"version": 1, "id": "controlled-full-run", "role": "packager", "source_revision": "checkout",
                "status": "token-free demonstration", "model_calls": 0,
                "assessment_status": "uncalibrated", "rubric": {"status": "draft"},
                "search": {"proposal_count": 1, "minibatch_size": 1, "concurrency": 1, "seed": 1,
                           "sampler": "epoch_shuffled", "skip_perfect": False,
                           "completion": {"max_iterations": 1}, "confirmation": {"enabled": False}},
                "trials": {"final": 1}, "final_decision": {"minimum_groups": 2, "bootstrap_samples": 100,
                  "maximum_interval_width": 20, "minimum_gain": 1, "case_type_regressions": {"performance": 0},
                  "human_review": {"required": True, "method": "strict_pair_majority"}}}
    path = tmp_path / "protocol.json"
    path.write_text(json.dumps(protocol))
    monkeypatch.setattr(cli, "_cases", lambda _: cases)
    monkeypatch.setattr(cli, "_assert_ready", lambda *_: None)
    calls = []
    class Runtime:
        def close(self):
            calls.append("runtime_closed")
    class Evaluator:
        runtime = Runtime()
        def evaluate(self, candidate, selected, stage, capture_traces, context):
            calls.append(stage)
            score = .8 if candidate["packager_directive"].endswith("Improved.") else .6
            return {"outputs": [{"case_id": case["id"]} for case in selected], "scores": [score] * len(selected),
                    "trajectories": [{"feedback": "Explain the condition."} for _ in selected] if capture_traces else None}
        def evaluate_pair(self, original, candidate, selected, stage="final"):
            calls.append(stage)
            def rows(identity, score):
                return [{"case_id": case["id"], "mean": score, "trials": [{"trial_id": identity + case["id"],
                    "contribution": score, "contract_passed": True, "critical_failures": [], "dimensions": {}}]} for case in selected]
            return rows("original", 60), rows("candidate", 80)
    def runtime(args, frozen, store):
        assert frozen["source_revision"] == cli.source_revision(args.source)
        assert "protocol_hash" in frozen
        calls.append("runtime_started")
        return Evaluator()
    class Proposer:
        def __init__(self, *_):
            pass
        def propose(self, parent, reflective_dataset, components_to_update, context):
            return {"texts": {"packager_directive": parent["packager_directive"] + "\nImproved."},
                    "scope": {"accepted": True, "reason": "This controlled proposal preserves requirements."}}
    monkeypatch.setattr(cli, "_runtime", runtime)
    monkeypatch.setattr(cli, "Proposer", Proposer)
    args = cli.parser().parse_args(["run", "--protocol", str(path), "--output", str(tmp_path / "run"),
                                   "--cache", str(tmp_path / "cache"), "--no-browser", "--no-wait"])
    return args, calls


def test_one_command_runs_native_search_final_comparison_and_portable_export(tmp_path, monkeypatch):
    args, calls = configured_run(tmp_path, monkeypatch)
    result = cli.execute(args)
    assert "training_parent" in calls
    assert "training_child" in calls
    assert "validation" in calls
    assert "final" in calls
    assert calls[-1] == "runtime_closed"
    assert Path(result["viewer"]).is_file()
    assert result["result"]["status"] != "qualified"
    with RecordStore(args.output, read_only=True) as store:
        assert store.read_manifest("search_result")["search_complete"]
        assert store.read_manifest("final_comparison")
        assert store.events()[-1]["kind"] == "run_session_completed"
    with pytest.raises(ValueError, match="Use --resume"):
        cli.execute(args)
    args.resume = True
    calls.clear()
    cli.execute(args)
    assert calls == ["runtime_started", "runtime_closed"]


def test_failed_run_preserves_portable_records(tmp_path, monkeypatch):
    args, calls = configured_run(tmp_path, monkeypatch)
    runtime = cli._runtime
    def broken(*_):
        raise RuntimeError("The controlled model transport failed.")
    monkeypatch.setattr(cli, "_runtime", broken)
    with pytest.raises(RuntimeError, match="controlled model transport"):
        cli.execute(args)
    assert (args.output / "viewer/index.html").is_file()
    with RecordStore(args.output, read_only=True) as store:
        assert store.events()[-1]["data"]["outcome"] == "failed"
    monkeypatch.setattr(cli, "_runtime", runtime)
    args.resume = True
    result = cli.execute(args)
    assert Path(result["viewer"]).is_file()
    assert "final" in calls


@pytest.mark.parametrize("field,value,error", [
    ("seed_candidate", {"packager_directive": "A changed original directive."}, "original directive differs"),
    ("case_hashes", {"trainset": {}}, "cases differ"),
])
def test_preselected_inputs_cannot_change_before_the_child_starts(tmp_path, monkeypatch, field, value, error):
    args, calls = configured_run(tmp_path, monkeypatch)
    protocol = json.loads(args.protocol.read_text())
    protocol[field] = value
    args.protocol.write_text(json.dumps(protocol))
    with pytest.raises(ValueError, match=error):
        cli.execute(args)
    assert calls == []
    assert not args.output.exists()


@pytest.mark.parametrize("error_number,action", [
    (errno.EADDRINUSE, "Use another --port value"),
    (errno.EACCES, "Permit a loopback listener"),
    (errno.EMFILE, "Resolve the reported listener error"),
])
def test_viewer_start_failure_records_outcome_and_exports_without_models(tmp_path, monkeypatch, error_number, action):
    args, calls = configured_run(tmp_path, monkeypatch)
    class UnavailableViewer:
        def __init__(self, *_, **__):
            pass
        def __enter__(self):
            raise OSError(error_number, "The controlled listener fault.")
        def __exit__(self, *_):
            pass
    monkeypatch.setattr(cli, "LiveViewer", UnavailableViewer)
    with pytest.raises(ValueError, match=action):
        cli.execute(args)
    assert calls == []
    assert (args.output / "viewer/index.html").is_file()
    with RecordStore(args.output, read_only=True) as store:
        assert store.read_manifest("protocol")
        failed = store.events()[-1]
        assert failed["kind"] == "run_session_completed"
        assert failed["data"]["outcome"] == "failed"
        assert "live viewer cannot start" in failed["data"]["error"]["message"]
        assert "--resume" in failed["data"]["error"]["message"]
        assert "fault.." not in failed["data"]["error"]["message"]
        assert not any(event["kind"] == "trial_assessed" for event in store.events())


def test_final_continuation_reconciles_hard_interruption_and_retains_samples(tmp_path, monkeypatch):
    args, calls = configured_run(tmp_path, monkeypatch)
    cli.execute(args)
    with RecordStore(args.output) as store:
        saved_final = store.read_manifest("final_comparison")
        packet = store.put_blob("This packet already exists before the hard interruption.")
        retained = {"completed": True, "packet": packet, "trial_id": "retained-final-trial"}
        store.write_manifest("trial-slot-retained", retained)
        old_operations = [store.begin_operation(kind, stage="final")
                          for kind in ("final_comparison", "paired_trial", "trial", "judge")]
        (store.root / "manifests/final_comparison.json").unlink()
    args.resume = True
    calls.clear()
    result = cli.execute(args)
    assert calls == ["runtime_started", "final", "runtime_closed"]
    assert result["completeness"]["complete"] is True
    with RecordStore(args.output, read_only=True) as store:
        assert store.read_manifest("trial-slot-retained") == retained
        assert store.read_blob(packet) == b"This packet already exists before the hard interruption."
        final = store.read_manifest("final_comparison")
        assert final["original"] == saved_final["original"]
        assert final["candidate"] == saved_final["candidate"]
        interrupted = {event["operation_id"] for event in store.events()
                       if event["kind"] == "operation_completed" and event["data"]["outcome"] == "interrupted"}
        assert set(old_operations) <= interrupted
        assert "The experiment record is incomplete." not in final["report"]["reasons"]


@pytest.mark.parametrize("width", [80, 40])
def test_run_help_introduces_full_run_and_continuation(monkeypatch, capsys, width):
    import os
    monkeypatch.setattr(cli.shutil, "get_terminal_size", lambda *_: os.terminal_size((width, 24)))
    with pytest.raises(SystemExit):
        cli.parser().parse_args(["run", "--help"])
    output = capsys.readouterr().out
    assert all(len(line) <= width for line in output.splitlines())
    text = " ".join(output.split())
    assert "Source adoption remains separate." in text
    assert "Continue the unchanged run" in text


def test_recovered_native_search_reuses_completed_results_without_model_calls(tmp_path, monkeypatch):
    from culler_evals.storage import restore_records
    import shutil
    args, calls = configured_run(tmp_path, monkeypatch)
    cli.execute(args)
    with RecordStore(args.output, read_only=True) as store:
        backup = Path(store.read_manifest("storage")["backup"])
        candidates = store.candidates()
    shutil.rmtree(args.output)
    restore_records(backup)
    args.resume = True
    calls.clear()
    cli.execute(args)
    assert calls == ["runtime_started", "runtime_closed"]
    with RecordStore(args.output, read_only=True) as store:
        assert store.candidates() == candidates
        assert store.read_manifest("search_result")["search_complete"]
        assert store.read_manifest("final_comparison")
