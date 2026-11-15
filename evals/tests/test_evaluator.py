import json
from contextlib import nullcontext
from pathlib import Path

import pytest

import culler_evals.evaluator as module
from culler_evals.assessment import SEMANTIC_CHECKS, case_sections
from culler_evals.cases import CaseError
from culler_evals.evaluator import EvaluationError, Evaluator, Proposer
from culler_evals.protocol import DIMENSIONS
from culler_evals.records import RecordStore


SECTIONS = case_sections({})

def judgment():
    return {"dimensions": {name: {"score": 3, "reason": "The visible claim matches the reference.", "evidence": ["reference:behavior"]} for name in DIMENSIONS},
            "critical_failures": [], "semantic_checks": [{"id": name, "passed": True, "reason": "The claims preserve the conditions.", "evidence": ["reference:behavior"]} for name in SEMANTIC_CHECKS],
            "uninspected_content": [], "feedback": ["Explain the material condition."], "artifacts": ["screenshot:1"],
            "_runtime": {"browser": {"visible_headings": list(SECTIONS)}, "artifact_paths": []}}


class RuntimeDouble:
    def __init__(self):
        self.packager_calls = 0
        self.judge_calls = 0
        self.judgments = []
        self.terminal = "packaged"
        self.changed = []
        self.repository_changed = False
        self.submitted_output = "/mc/artifacts/eval/fixture/packet.html"

    def execute_packager(self, prepared, candidate, root, settings):
        self.packager_calls += 1
        packet = root / "packet.html"
        packet.write_text('<a href="#summary">Summary</a>' + "".join(f'<h2 id="{name.lower().replace(" ", "-")}">{name}</h2><p>Visible explanation.</p>' for name in SECTIONS))
        return {"terminal": {"status": self.terminal, "summary": "The reviewed change improves the interface.", "outputs": self.submitted_output}, "packet_path": str(packet) if self.terminal == "packaged" else None,
                "metadata": {"evidence_changed": self.changed, "repository_changed": self.repository_changed}, "_runtime": {"artifact_paths": []}}

    def assess_packet(self, prepared, packet, root, settings, rubric):
        self.judge_calls += 1
        return self.judgments.pop(0) if self.judgments else judgment()


@pytest.fixture
def setup(tmp_path, monkeypatch):
    case = {"id": "fixture", "group": "fixture", "expected_terminal": "packaged", "role_inputs": {"brief": "Fixed brief"}, "references": {"fact": "after"}}
    monkeypatch.setattr(module, "prepare_case", lambda *args, **kwargs: {"manifest_hash": "fixed-manifest"})
    protocol = {"runtime": {role: {"harness": "codex", "model": "fixed"} for role in ("packager", "judge", "optimizer")},
                "retry": {"external_attempts": 2, "assessment_attempts": 2}, "trials": {"pilot": 2, "training": 2, "final": 2},
                "operator_context": "Desktop operator", "search": {"seed": 1},
                "rubric": {"dimensions": {name: {"weight": 1} for name in DIMENSIONS}},
                "protected": {"spans": [{"name": "contract", "text": "Fixed contract"}], "semantic_requirements": ["Preserve the terminal contract."]}}
    store = RecordStore(tmp_path / "experiment")
    runtime = RuntimeDouble()
    evaluator = Evaluator(protocol, tmp_path, store, runtime, cache=tmp_path / "cache")
    yield evaluator, runtime, store, case
    store.close()


def test_scores_keep_raw_quality_and_zero_a_critical_trial(setup):
    evaluator, runtime, store, case = setup
    critical = judgment()
    critical["critical_failures"] = [{"category": "false_claim", "reason": "The packet invents a successful check.", "evidence": ["reference:limits"]}]
    runtime.judgments = [critical, judgment()]
    result = evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", True)
    assert result["scores"] == [.375]
    trials = result["outputs"][0]["trials"]
    assert [trial["quality"] for trial in trials] == [75, 75]
    assert [trial["contribution"] for trial in trials] == [0, 75]
    assert trials[0]["trial_id"] != trials[1]["trial_id"]
    assessment = __import__("json").loads(store.read_blob(trials[0]["judgment"]))["assessment"]
    assert assessment["trial_id"] == trials[0]["trial_id"]
    assert assessment["reference_hash"]


def test_native_reflection_request_receives_semantic_reason_and_visible_parent_text(setup):
    from culler_evals.engine import _Adapter, _batch
    from culler_evals.runtime import Runtime

    evaluator, runtime, store, case = setup
    evaluator.protocol["trials"]["training"] = 1
    trace = store.root / "browser-visible.jsonl"
    trace.write_text(json.dumps({"action": "inspect", "arguments": {}, "screenshot": "0001-inspect.png",
                                 "visible": {"text": "The parent packet visibly explains the retry condition.",
                                             "headings": ["Technical"], "scroll_y": 900,
                                             "viewport": {"width": 1280, "height": 900}, "at_end": False}}) + "\n")
    judged = judgment()
    judged["feedback"] = []
    judged["semantic_checks"][0].update(passed=False, reason="The packet omits the exact private access condition.",
                                       evidence=["reference:private-condition"])
    judged["_runtime"].update(browser={"visible_headings": list(SECTIONS), "trace_path": str(trace)},
                             artifact_paths=[{"type": "jsonl", "path": str(trace)}],
                             raw_trace="The private transcript marker.")
    runtime.judgments = [judged]
    parent = {"packager_directive": "Fixed contract"}
    result = evaluator.evaluate(parent, [case], "training_parent", True)
    assert result["scores"] == [0.0]
    batch = _batch(result, 1, True)
    protocol = evaluator.protocol | {"seed_candidate": parent,
                                     "search": evaluator.protocol["search"] | {"concurrency": 1}}
    adapter = _Adapter(protocol, [case], [], evaluator, Proposer(evaluator), store, False)
    adapter.tasks = [{"parent_candidate": parent, "cases": [case["id"]], "reflection": "pending",
                      "parent_evaluation": {"event_id": "parent-evaluation"}}]
    adapter.evaluation_batches[id(batch)] = {"context": {"case_ids": [case["id"]]}, "cases": [case]}
    reflective = adapter.make_reflective_dataset(parent, batch, ["packager_directive"])
    feedback = reflective["packager_directive"][0]["Feedback"][0]
    assert feedback["contract"]["passed"] is True
    assert feedback["contract_passed"] is False
    assert feedback["semantic_checks"][0]["evidence"] == ["reference:private-condition"]
    prompts = []

    def execute(request, *_args):
        prompts.append(request["prompt"])
        return {"output": '{"directive":"Fixed contract","hypothesis":"Keep the contract."}', "artifact_paths": []}

    with Runtime(Path(__file__).resolve().parents[2], executor=execute) as bridge:
        runtime.propose = bridge.propose
        Proposer(evaluator).propose(parent, reflective, ["packager_directive"], {"proposal_task_id": "1-0"})
    assert "The packet omits the exact private access condition." in prompts[0]
    assert "The parent packet visibly explains the retry condition." in prompts[0]
    assert "0001-inspect.png" in prompts[0]
    assert "The private transcript marker." not in prompts[0]
    assert str(trace) not in prompts[0]
    assert "<html" not in prompts[0]


def test_reflection_explicitly_records_missing_display_text_and_render_failure(setup):
    evaluator, _, store, _ = setup
    judged = judgment()
    judged["uninspected_content"] = ["Visual"]
    judged["_runtime"]["render_failures"] = [{"kind": "failed_images", "section": "Visual", "src": "data:image/private"}]
    trial = {"trial_id": "fixture", "quality": 75, "contribution": 0, "contract_passed": False,
             "contract": {"passed": True, "failures": []}, "judgment": store.put_blob(judged), "feedback": []}
    feedback = evaluator._reflection_feedback([trial])[0]
    assert feedback["uninspected_content"] == ["Visual"]
    assert feedback["render_failures"] == [{"kind": "failed_images", "section": "Visual"}]
    output = evaluator._reflection_outputs([trial])[0]
    assert output["rendered_text_status"] == "unavailable"
    assert output["absence_reason"]
    assert output["display_states"] == []


def test_final_cases_cannot_create_optimizer_reflection(setup):
    evaluator, runtime, _, case = setup
    with pytest.raises(EvaluationError, match="Final cases cannot enter"):
        evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "final", True)
    assert runtime.packager_calls == runtime.judge_calls == 0


@pytest.mark.parametrize("name", ["mc/verbs/brief.go", "mc/verbs/skills/packager/mc-packager/SKILL.md"])
@pytest.mark.parametrize("resume", [False, True])
def test_product_input_drift_blocks_initial_or_resumed_model_calls(setup, name, resume):
    from culler_evals.product_sources import product_source_manifest, product_source_hash
    from test_product_sources import source_files

    evaluator, runtime, store, case = setup
    source_files(evaluator.source)
    manifest = product_source_manifest(evaluator.source)
    evaluator.protocol.update(product_source_manifest=manifest, product_source_hash=product_source_hash(manifest))
    execute, assess = runtime.execute_packager, runtime.assess_packet

    def bound_execute(*args):
        result = execute(*args)
        result["metadata"]["product_source_hash"] = evaluator.protocol["product_source_hash"]
        return result

    def bound_assess(*args):
        result = assess(*args)
        result["_runtime"]["metadata"] = {"product_source_hash": evaluator.protocol["product_source_hash"]}
        return result

    runtime.execute_packager, runtime.assess_packet = bound_execute, bound_assess
    candidate = {"packager_directive": "Fixed contract"}
    if resume:
        evaluator.evaluate(candidate, [case], "pilot", False)
        store.close()
    original_counts = runtime.packager_calls, runtime.judge_calls
    (evaluator.source / name).write_text("A changed production input after freeze.")
    with RecordStore(store.root) if resume else nullcontext(store) as target_store:
        target = Evaluator(evaluator.protocol, evaluator.source, target_store, runtime, cache=evaluator.cache)
        with pytest.raises(EvaluationError, match="remains incomplete"):
            target.evaluate(candidate, [case], "pilot", False)
        assert not any(event["kind"] == "trial_reused" for event in target_store.events())
    assert (runtime.packager_calls, runtime.judge_calls) == original_counts


def test_case_preparation_failure_remains_incomplete_and_never_calls_a_model(setup, monkeypatch):
    evaluator, runtime, store, case = setup
    def fail(*args, **kwargs):
        raise CaseError("The required source cannot be fetched.")
    monkeypatch.setattr(module, "prepare_case", fail)
    with pytest.raises(EvaluationError, match="remains incomplete"):
        evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", True)
    assert runtime.packager_calls == 0
    assert not any(event["kind"] == "case_assessed" for event in store.events())
    assert len([event for event in store.events() if event["kind"] == "operation_completed"]) == 2
    starts = [event for event in store.events() if event["kind"] == "operation_started"]
    assert [(event["trial_number"], event["trial_count"], event["external_attempt"]) for event in starts] == [(1, 2, 1), (1, 2, 2)]


def test_trial_records_distinguish_repetitions_from_replacement_attempts(setup):
    evaluator, _, store, case = setup
    result = evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", False)
    starts = [event for event in store.events()
              if event["kind"] == "operation_started" and event["data"]["operation_kind"] == "trial"]
    assert [(event["trial_number"], event["trial_count"], event["external_attempt"]) for event in starts] == [(1, 2, 1), (2, 2, 1)]
    assert [(row["trial_number"], row["trial_count"], row["external_attempt"]) for row in result["outputs"][0]["trials"]] == [(1, 2, 1), (2, 2, 1)]


def test_paired_trials_preserve_each_directives_repetition_number(setup):
    evaluator, _, store, case = setup
    evaluator.evaluate_pair({"packager_directive": "Fixed contract"}, {"packager_directive": "A second directive"}, [case])
    starts = [event for event in store.events()
              if event["kind"] == "operation_started" and event["data"]["operation_kind"] == "trial"]
    assert [(event["trial_number"], event["trial_count"], event["external_attempt"]) for event in starts] == [(1, 2, 1), (1, 2, 1), (2, 2, 1), (2, 2, 1)]


@pytest.mark.parametrize("first_terminal", ["packaged", "infra"])
def test_paired_retry_preserves_a_completed_counterpart_including_a_zero_sample(setup, first_terminal):
    evaluator, runtime, store, case = setup
    execute = runtime.execute_packager
    calls = []
    def fail_second(prepared, directive, root, settings):
        calls.append(directive)
        if len(calls) == 2:
            raise RuntimeError("The candidate provider failed before its result.")
        runtime.terminal = first_terminal if len(calls) == 1 else "packaged"
        return execute(prepared, directive, root, settings)
    runtime.execute_packager = fail_second
    original = {"packager_directive": "Fixed contract"}
    candidate = {"packager_directive": "A second directive"}
    left, right = evaluator.evaluate_pair(original, candidate, [case])
    assert calls == [original["packager_directive"], candidate["packager_directive"],
                     candidate["packager_directive"], candidate["packager_directive"], original["packager_directive"]]
    assert len(left[0]["trials"]) == len(right[0]["trials"]) == 2
    assert left[0]["mean"] == (75 if first_terminal == "packaged" else 37.5)
    assert right[0]["mean"] == 75
    first = left[0]["trials"][0]
    assert first["external_attempt"] == 1
    assert first["contribution"] == (75 if first_terminal == "packaged" else 0)
    assert right[0]["trials"][0]["external_attempt"] == 2
    events = store.events()
    failed = next(event for event in events if event["kind"] == "operation_completed"
                  and event["data"].get("result", {}).get("retained_sample_trial_ids"))
    assert failed["data"]["result"] == {"retained_sample_trial_ids": {"original": first["trial_id"]},
                                         "incomplete_counterparts": ["candidate"]}
    assert len([event for event in events if event["kind"] == "trial_assessed"]) == 4


def test_runtime_source_and_transport_attestation_are_required_when_frozen(setup):
    evaluator, _runtime, _store, _case = setup
    evaluator.protocol["runtime_source_hash"] = "frozen-source"
    evaluator.protocol["runtime"]["optimizer"] |= {"channel": "codex", "base_url": ""}
    metadata = {"runtime_source_hash": "frozen-source", "channel": "codex", "base_url": ""}
    evaluator.verify_runtime_identity({"_runtime": {"metadata": metadata}}, "optimizer")
    for name, value in (("runtime_source_hash", "changed"), ("channel", "none"), ("base_url", "http://fixture.invalid")):
        with pytest.raises(EvaluationError, match="differs"):
            evaluator.verify_runtime_identity({"metadata": metadata | {name: value}}, "optimizer")
    with pytest.raises(EvaluationError, match="differs"):
        evaluator.verify_runtime_identity({}, "optimizer")


@pytest.mark.parametrize("role", ["packager", "judge", "optimizer"])
@pytest.mark.parametrize("changed", ["missing_requested", "missing_configured", "null_requested", "null_configured", "requested", "configured", "observed", "mixed", "empty", "scalar"])
def test_explicit_effort_rejects_missing_or_different_runtime_evidence(setup, role, changed):
    evaluator, _, _, _ = setup
    evaluator.protocol["runtime"][role]["effort"] = "high"
    metadata = {"requested_effort": "high", "configured_effort": "high", "observed_efforts": ["high"]}
    if changed.startswith("missing_"):
        metadata.pop(changed.removeprefix("missing_") + "_effort")
    elif changed.startswith("null_"):
        metadata[changed.removeprefix("null_") + "_effort"] = None
    elif changed in ("requested", "configured"):
        metadata[changed + "_effort"] = "low"
    else:
        metadata["observed_efforts"] = {"observed": ["low"], "mixed": ["high", "low"], "empty": [], "scalar": "high"}[changed]
    with pytest.raises(EvaluationError, match="effort"):
        evaluator.verify_runtime_identity({"_runtime": {"metadata": metadata}}, role)


@pytest.mark.parametrize("role", ["packager", "judge", "optimizer"])
def test_explicit_effort_accepts_matching_request_and_configuration_without_an_observed_value(setup, role):
    evaluator, _, _, _ = setup
    evaluator.protocol["runtime"][role]["effort"] = "high"
    metadata = {"requested_effort": "high", "configured_effort": "high"}
    evaluator.verify_runtime_identity({"metadata": metadata}, role)
    unavailable = metadata | {"observed_efforts": None}
    evaluator.verify_runtime_identity({"metadata": unavailable}, role)
    assert unavailable["observed_efforts"] is None
    evaluator.verify_runtime_identity({"metadata": metadata | {"observed_efforts": ["high"]}}, role)


def test_historical_protocol_without_effort_keeps_the_runtime_record_unchanged(setup):
    evaluator, _, _, _ = setup
    result = {"metadata": {"harness": "codex"}}
    evaluator.verify_runtime_identity(result, "packager")
    assert result == {"metadata": {"harness": "codex"}}


def test_effort_mismatch_preserves_the_packet_without_scoring_it(setup):
    evaluator, runtime, store, case = setup
    evaluator.protocol["runtime"]["packager"]["effort"] = "high"
    execute = runtime.execute_packager

    def wrong_effort(*args):
        result = execute(*args)
        result["metadata"].update(requested_effort="high", configured_effort="low")
        return result

    runtime.execute_packager = wrong_effort
    with pytest.raises(EvaluationError, match="remains incomplete"):
        evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", False)
    events = store.events()
    preserved = [event for event in events if event["kind"] == "role_result_preserved"]
    assert len(preserved) == 1
    assert store.read_blob(preserved[0]["data"]["packet"])
    assert all(event["kind"] != "trial_assessed" for event in events)
    failures = [event for event in events if event["kind"] == "operation_completed" and event["data"]["outcome"] == "failed"]
    assert failures and all("effort" in event["data"]["error"]["message"] for event in failures)
    assert runtime.judge_calls == 0


def test_incomplete_browser_inspection_retries_the_judge_on_the_same_packet(setup):
    evaluator, runtime, store, case = setup
    incomplete = judgment()
    incomplete["_runtime"]["browser"]["visible_headings"] = ["Summary"]
    runtime.judgments = [incomplete, judgment(), judgment()]
    result = evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", True)
    assert result["scores"] == [.75]
    assert runtime.packager_calls == 2
    assert runtime.judge_calls == 3
    failed = [event for event in store.events() if event["kind"] == "operation_completed" and event["data"]["outcome"] == "failed"]
    assert len(failed) == 1
    assert failed[0]["data"]["result"]["judgment"]


def test_judge_exhaustion_and_external_retry_preserve_the_produced_packet(setup):
    evaluator, runtime, store, case = setup
    incomplete = judgment()
    incomplete["_runtime"]["browser"]["visible_headings"] = ["Summary"]
    runtime.judgments = [incomplete, incomplete, judgment(), judgment()]
    seen = []
    assess = runtime.assess_packet
    def inspect(prepared, packet, root, settings, rubric):
        seen.append((str(packet), Path(packet).read_bytes(), str(root)))
        return assess(prepared, packet, root, settings, rubric)
    runtime.assess_packet = inspect
    result = evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", False)
    assert runtime.packager_calls == 2
    assert runtime.judge_calls == 4
    assert seen[0][:2] == seen[1][:2] == seen[2][:2]
    assert len({row[2] for row in seen[:3]}) == 3
    assert result["outputs"][0]["trials"][0]["generation_external_attempt"] == 1
    assert result["outputs"][0]["trials"][0]["external_attempt"] == 2
    reused = [event for event in store.events() if event["kind"] == "packet_reused"]
    assert len(reused) == 1
    assert reused[0]["trial_id"] == result["outputs"][0]["trials"][0]["trial_id"]


def test_pending_packet_and_completed_zero_sample_survive_evaluator_continuation(setup):
    evaluator, runtime, store, case = setup
    candidate = {"packager_directive": "Fixed contract"}
    runtime.terminal = "infra"
    zero = evaluator._trial(candidate, case, "pilot", {"trial_number": 1, "trial_count": 2, "external_attempt": 1})
    runtime.terminal = "packaged"
    incomplete = judgment()
    incomplete["_runtime"]["browser"]["visible_headings"] = ["Summary"]
    runtime.judgments = [incomplete] * 4
    with pytest.raises(EvaluationError, match="remains incomplete"):
        evaluator.evaluate(candidate, [case], "pilot", False)
    assert runtime.packager_calls == 2
    assert runtime.judge_calls == 4
    continued = Evaluator(evaluator.protocol, evaluator.source, store, runtime, cache=evaluator.cache)
    runtime.judgments = [judgment()]
    result = continued.evaluate(candidate, [case], "pilot", False)
    assert runtime.packager_calls == 2
    assert result["outputs"][0]["trials"][0]["trial_id"] == zero["trial_id"]
    assert result["outputs"][0]["trials"][0]["contribution"] == 0
    assert result["scores"] == [.375]
    assert len([event for event in store.events() if event["kind"] == "trial_assessed"]) == 2


def test_completed_semantic_failure_is_a_fixed_sample_and_never_reassessed(setup):
    evaluator, runtime, store, case = setup
    critical = judgment()
    critical["critical_failures"] = [{"category": "false_claim", "reason": "The packet invents a successful check.", "evidence": ["reference:limits"]}]
    runtime.judgments = [critical, judgment()]
    candidate = {"packager_directive": "Fixed contract"}
    first = evaluator.evaluate(candidate, [case], "pilot", False)
    second = evaluator.evaluate(candidate, [case], "pilot", False)
    assert first["scores"] == second["scores"] == [.375]
    assert runtime.packager_calls == runtime.judge_calls == 2
    assert [row["trial_id"] for row in first["outputs"][0]["trials"]] == [row["trial_id"] for row in second["outputs"][0]["trials"]]


def test_retained_packet_preserves_an_invalid_source_filename(setup):
    evaluator, runtime, _store, case = setup
    execute = runtime.execute_packager
    def invalid_filename(*args):
        result = execute(*args)
        packet = Path(result["packet_path"])
        renamed = packet.with_name("report.html")
        packet.rename(renamed)
        return result | {"packet_path": str(renamed), "terminal": result["terminal"] | {"outputs": None}}
    runtime.execute_packager = invalid_filename
    result = evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", False)
    assert result["scores"] == [0]


def test_fixed_evidence_modification_is_a_contract_failure(setup):
    evaluator, runtime, _, case = setup
    runtime.changed = ["eval/1/verdict.txt"]
    result = evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", False)
    assert result["scores"] == [0]
    assert result["outputs"][0]["trials"][0]["quality"] == 75
    assert not result["outputs"][0]["trials"][0]["contract_passed"]


@pytest.mark.parametrize("violation", ["repository", "evidence"])
def test_post_terminal_changes_fail_both_quality_and_contract_trials(setup, violation):
    evaluator, runtime, _, case = setup
    if violation == "repository":
        runtime.repository_changed = True
    else:
        runtime.changed = ["eval/1/verdict.txt"]
    candidate = {"packager_directive": "Fixed contract"}
    quality = evaluator.evaluate(candidate, [case], "pilot", False)
    assert quality["scores"] == [0]
    assert all(trial["quality"] == 75 and not trial["contract_passed"] for trial in quality["outputs"][0]["trials"])
    contract = evaluator.evaluate_contract(candidate, [case], trial_count=1)
    assert not contract[0]["passed"]
    assert contract[0]["quality_score"] is None
    assert contract[0]["trials"][0]["contract"]["failures"]
    assert runtime.judge_calls == 2


def test_a_snapshot_name_cannot_hide_an_invalid_submitted_packet_name(setup):
    evaluator, runtime, _, case = setup
    runtime.submitted_output = "/mc/artifacts/eval/fixture/report.html"
    candidate = {"packager_directive": "Fixed contract"}
    quality = evaluator.evaluate(candidate, [case], "pilot", False)
    assert quality["scores"] == [0]
    assert "submitted packet filename" in " ".join(quality["outputs"][0]["trials"][0]["contract"]["failures"])
    contract = evaluator.evaluate_contract(candidate, [case], trial_count=1)
    assert not contract[0]["passed"]


def test_failed_harness_attempt_keeps_its_trace_artifact(setup, tmp_path):
    evaluator, runtime, store, case = setup
    trace = tmp_path / "failed-trace.jsonl"
    trace.write_text('{"type":"provider_error"}\n')
    class Failure(RuntimeError):
        artifact_paths = [{"type": "trace", "path": str(trace)}]
    runtime.execute_packager = lambda *args, **kwargs: (_ for _ in ()).throw(Failure("The provider failed."))
    with pytest.raises(EvaluationError):
        evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", False)
    failed = [event for event in store.events() if event["kind"] == "operation_completed"]
    assert len(failed) == 2
    artifact = failed[0]["data"]["result"]["artifacts"]["failed:trace:0"]
    assert store.read_blob(artifact) == trace.read_bytes()


def test_an_expected_infrastructure_terminal_receives_no_quality_score(setup):
    evaluator, runtime, _, case = setup
    case["expected_terminal"] = "infra"
    runtime.terminal = "infra"
    with pytest.raises(EvaluationError, match="cannot enter packet quality"):
        evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", False)
    result = evaluator.evaluate_contract({"packager_directive": "Fixed contract"}, [case], trial_count=2)
    assert result[0]["passed"]
    assert result[0]["quality_score"] is None
    assert runtime.judge_calls == 0


def test_credential_files_cannot_override_the_frozen_model(setup):
    evaluator, _, _, _ = setup
    evaluator.credentials = {"packager": {"model": "different"}}
    with pytest.raises(EvaluationError, match="credentials only"):
        evaluator.settings("packager")
    evaluator.credentials = {"packager": {"credentials": {"env": {"PRIVATE_TOKEN": "private"}}}}
    assert evaluator.settings("packager")["model"] == "fixed"


def test_no_packet_means_unassessed_dimensions_instead_of_fabricated_zero_scores(setup):
    evaluator, runtime, _, case = setup
    runtime.terminal = "infra"
    result = evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", False)
    assert result["scores"] == [0]
    assert set(result["outputs"][0]["dimensions"].values()) == {None}


def test_optimizer_receives_protected_requirements_and_scope_requires_a_boolean(setup):
    evaluator, runtime, _, _ = setup
    received = []
    def propose(parent, feedback, root, settings, rubric):
        received.append(rubric)
        return {"directive": "Fixed contract\nClearer explanation", "hypothesis": "Improve comprehension."}
    runtime.propose = propose
    runtime.assess_edit = lambda *args, **kwargs: {"permitted": "yes", "reasons": []}
    with pytest.raises(EvaluationError, match="Boolean result"):
        Proposer(evaluator).propose({"packager_directive": "Fixed contract"}, [], ["packager_directive"], {})
    assert received[0]["protected"] == evaluator.protocol["protected"]


def test_optimizer_and_scope_judge_receive_current_product_requirements(setup):
    source = Path(__file__).resolve().parents[2]
    evaluator, runtime, _, _ = setup
    parent, identity = module.original_directive(source)
    directive = (source / "mc/verbs/directives/packager.md").read_text()
    writing = (source / "mc/verbs/skills/shared/writing/SKILL.md").read_text().split("\n---\n", 1)[1]
    assert parent == directive + "\n\n" + writing
    assert identity["separate_writing_skill"] is False
    evaluator.protocol["protected"] = module.protected_requirements(source)
    received = []

    def propose(text, feedback, root, settings, rubric):
        received.append(rubric["protected"])
        return {"directive": text + "\nExplain the material conditions clearly.\n", "hypothesis": "Improve clarity."}

    def assess_edit(text, child, protected, settings):
        received.append(protected)
        return {"permitted": True, "reasons": []}

    runtime.propose, runtime.assess_edit = propose, assess_edit
    result = Proposer(evaluator).propose({"packager_directive": parent}, [], ["packager_directive"], {})
    assert result["scope"]["accepted"]
    for protected in received:
        requirements = " ".join(protected["semantic_requirements"])
        assert "selected directive's Reader requirements" in requirements
        assert "omits Culler" not in requirements
        assert "artifact paths" not in requirements
    assert len(received) == 2


def test_completed_optimizer_response_survives_a_failed_scope_assessment(setup):
    evaluator, runtime, store, _ = setup
    child = "Fixed contract\nClearer explanation"
    runtime.propose = lambda *args, **kwargs: {"directive": child, "hypothesis": "Improve comprehension."}
    runtime.assess_edit = lambda *args, **kwargs: (_ for _ in ()).throw(RuntimeError("The scope Judge failed."))
    with pytest.raises(RuntimeError, match="scope Judge failed"):
        Proposer(evaluator).propose({"packager_directive": "Fixed contract"}, [], ["packager_directive"],
                                    {"proposal_task_id": "same-task", "attempt": "first"})
    response_event = next(event for event in store.events() if event["kind"] == "optimizer_response_received")
    artifacts = response_event["data"]
    assert json.loads(store.read_blob(artifacts["response"]))["directive"] == child
    assert store.read_blob(artifacts["directive"]).decode() == child
    assert "Clearer explanation" in store.read_blob(artifacts["diff"]).decode()
    assert json.loads(store.read_blob(artifacts["request"]))["protected"] == evaluator.protocol["protected"]
    failed = [event for event in store.events() if event["kind"] == "operation_completed" and event["data"]["outcome"] == "failed"]
    assert failed[0]["data"]["result"]["response"] == artifacts["response"]


def test_malformed_optimizer_response_retries_with_its_failed_raw_artifacts(setup, tmp_path):
    from culler_evals.runtime import RuntimeErrorWithEvidence
    evaluator, runtime, store, _case = setup
    raw = tmp_path / "invalid-response.txt"
    raw.write_text('The malformed model response: {"directive":')
    roots = []
    def propose(parent, feedback, root, settings, rubric):
        roots.append(root)
        if len(roots) == 1:
            raise RuntimeErrorWithEvidence("The harness returned no complete JSON object.", [{"type": "response", "path": str(raw)}])
        return {"directive": parent, "hypothesis": "The directive remains unchanged."}
    runtime.propose = propose
    result = Proposer(evaluator).propose({"packager_directive": "Fixed contract"}, [], ["packager_directive"], {})
    assert result["texts"] == {"packager_directive": "Fixed contract"}
    assert len(roots) == 2 and roots[0] != roots[1]
    failed = next(event for event in store.events() if event["kind"] == "operation_completed" and event["data"]["outcome"] == "failed")
    assert store.read_blob(failed["data"]["result"]["artifacts"]["failed:response:0"]) == raw.read_bytes()
    assert len([event for event in store.events() if event["kind"] == "optimizer_response_received"]) == 1


def test_scope_retry_preserves_the_completed_optimizer_directive(setup):
    evaluator, runtime, store, _case = setup
    calls, scopes = [], []
    child = "Fixed contract\nClearer explanation"
    def propose(parent, feedback, root, settings, rubric):
        calls.append(root)
        return {"directive": child, "hypothesis": "Improve comprehension."}
    def scope(parent, candidate, protected, settings):
        scopes.append((candidate, settings["trial_root"]))
        if len(scopes) == 1:
            raise RuntimeError("The scope Judge returned an incomplete response.")
        return {"permitted": True, "reasons": ["The edit preserves the protected requirements."]}
    runtime.propose, runtime.assess_edit = propose, scope
    result = Proposer(evaluator).propose({"packager_directive": "Fixed contract"}, [], ["packager_directive"], {})
    assert result["scope"]["accepted"]
    assert len(calls) == 1
    assert [candidate for candidate, root in scopes] == [child, child]
    assert scopes[0][1] != scopes[1][1]
    assert len([event for event in store.events() if event["kind"] == "optimizer_response_received"]) == 1


def test_resumed_proposal_preserves_the_completed_optimizer_result(setup):
    evaluator, runtime, store, _ = setup
    roots = []
    def propose(parent, feedback, root, settings, rubric):
        roots.append(root)
        return {"directive": parent, "hypothesis": "The directive remains unchanged."}
    runtime.propose = propose
    for attempt in ("first", "replacement"):
        Proposer(evaluator).propose({"packager_directive": "Fixed contract"}, [], ["packager_directive"],
                                    {"proposal_task_id": "same-task", "attempt": attempt})
    assert len(roots) == 1
    assert any(event["kind"] == "optimizer_response_reused" for event in store.events())


def test_proposal_continuation_reuses_semantic_feedback_and_the_same_child(setup):
    evaluator, runtime, store, _case = setup
    calls, scopes = [], []
    child = "Fixed contract\nClearer explanation"
    def propose(parent, feedback, root, settings, rubric):
        calls.append(root)
        return {"directive": child, "hypothesis": "Improve comprehension."}
    def scope(parent, candidate, protected, settings):
        scopes.append(candidate)
        if len(scopes) <= 2:
            raise RuntimeError("The scope Judge failed.")
        return {"permitted": True, "reasons": ["The edit preserves the requirements."]}
    runtime.propose, runtime.assess_edit = propose, scope
    feedback = {"packager_directive": [{"Feedback": ["Explain the condition."], "score": .5,
                                        "Generated Outputs": {"mean": 50, "event_id": "earlier-event"},
                                        "evaluation": {"operation_id": "earlier-operation"}}]}
    context = {"proposal_task_id": "1-0", "attempt": "first"}
    with pytest.raises(RuntimeError, match="scope Judge failed"):
        Proposer(evaluator).propose({"packager_directive": "Fixed contract"}, feedback, ["packager_directive"], context)
    resumed = json.loads(json.dumps(feedback))
    resumed["packager_directive"][0]["evaluation"] = {"operation_id": "replacement-operation"}
    resumed["packager_directive"][0]["Generated Outputs"]["event_id"] = "replacement-event"
    result = Proposer(evaluator).propose({"packager_directive": "Fixed contract"}, resumed, ["packager_directive"], context | {"attempt": "replacement"})
    assert result["texts"] == {"packager_directive": child}
    assert len(calls) == 1
    assert scopes == [child, child, child]
    assert any(event["kind"] == "optimizer_response_reused" for event in store.events())
    again = Proposer(evaluator).propose({"packager_directive": "Fixed contract"}, resumed, ["packager_directive"], context | {"attempt": "later"})
    assert again == result
    assert len(scopes) == 3
    assert any(event["kind"] == "edit_scope_reused" for event in store.events())


def test_identical_parallel_parent_tasks_preserve_distinct_optimizer_samples(setup):
    evaluator, runtime, _store, _case = setup
    calls = []
    def propose(parent, feedback, root, settings, rubric):
        calls.append(root)
        return {"directive": parent, "hypothesis": "The directive remains unchanged."}
    runtime.propose = propose
    for task_id in ("1-0", "1-1"):
        Proposer(evaluator).propose({"packager_directive": "Fixed contract"}, [], ["packager_directive"],
                                    {"proposal_task_id": task_id, "attempt": "first"})
    assert len(calls) == 2 and calls[0] != calls[1]


def test_judge_receives_the_exact_submitted_summary(setup):
    evaluator, runtime, _, case = setup
    seen = []
    def assess(prepared, *args, **kwargs):
        seen.append(prepared["packet_summary"])
        return judgment()
    runtime.assess_packet = assess
    evaluator.evaluate({"packager_directive": "Fixed contract"}, [case], "pilot", False)
    assert seen == ["The reviewed change improves the interface."] * 2


def test_runtime_update_reassesses_packets_without_new_packager_calls(setup):
    import copy
    evaluator, runtime, store, case = setup
    evaluator.protocol['protocol_hash'] = 'before'
    evaluator.protocol['runtime_source_hash'] = 'old-runtime'
    execute = runtime.execute_packager
    assess = runtime.assess_packet

    def generate(*args, **kwargs):
        result = execute(*args, **kwargs)
        result['metadata']['runtime_source_hash'] = 'old-runtime'
        return result

    def score(*args, **kwargs):
        result = assess(*args, **kwargs)
        result['_runtime']['metadata'] = {'runtime_source_hash': evaluator.protocol['runtime_source_hash']}
        return result

    runtime.execute_packager = generate
    runtime.assess_packet = score
    first = evaluator.evaluate({'packager_directive': 'Fixed contract'}, [case], 'pilot', False)
    before = copy.deepcopy(evaluator.protocol)
    store.write_manifest('generation_protocol-before', before)
    for path in (store.root / 'manifests').glob('trial-slot-*'):
        value = json.loads(path.read_text())
        value['generation_protocol_hash'] = 'before'
        value['previous_completed'] = value.pop('completed')
        # The fixture has no repository. The real continuation restores disposed evidence.
        prepared = json.loads(store.read_blob(value['prepared']))
        prepared['repository_path'] = str(store.root)
        value['prepared'] = store.put_blob(prepared)
        store.write_manifest(path.stem, value)
    evaluator.protocol.update(protocol_hash='after', sample_protocol_hash='before', runtime_source_hash='new-runtime')
    runtime.execute_packager = lambda *_args, **_kwargs: pytest.fail('Reuse the original packets.')
    second = evaluator.evaluate({'packager_directive': 'Fixed contract'}, [case], 'pilot', False)
    assert runtime.packager_calls == 2
    assert runtime.judge_calls == 4
    assert [row['trial_id'] for row in second['outputs'][0]['trials']] == [row['trial_id'] for row in first['outputs'][0]['trials']]
    third = evaluator.evaluate({'packager_directive': 'Fixed contract'}, [case], 'pilot', False)
    assert third['scores'] == second['scores']
    assert third['outputs'][0]['trials'] == second['outputs'][0]['trials']
    assert runtime.judge_calls == 4
