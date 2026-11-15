import copy
import hashlib
import json
import subprocess
from pathlib import Path

import pytest

from culler_evals.cases import CASE_TYPES, content_hash
from culler_evals.assessment import SEMANTIC_CHECKS
from culler_evals.protocol import (DIMENSIONS, PRODUCT_WRAPPER_FILES, ProtocolError, assessment_contract, calibration_context,
                                  calibration_errors, contract_readiness, freeze_protocol,
                                  digest, effort_choices, product_wrapper_hashes, read_protocol, readiness,
                                  validate_partitions, verify_frozen_sources)


def judgment(score=3, critical=True):
    return {
        "dimensions": {name: {"score": score, "reason": "The explanation matches the reference.", "evidence": ["reference:behavior"]} for name in DIMENSIONS},
        "critical_failures": [{"category": "false_claim", "reason": "The packet claims an unverified check passed.", "evidence": ["reference:limits"]}] if critical else [],
        "semantic_checks": [{"id": name, "passed": not critical, "reason": "The reference identifies the claim.", "evidence": ["reference:limits"]} for name in SEMANTIC_CHECKS],
        "uninspected_content": [], "feedback": ["Preserve the evidence limit."], "artifacts": ["screenshot:summary"],
    }


def calibration_record(packet, group, protocol):
    context = calibration_context(protocol)
    packet_ref = {"sha256": hashlib.sha256(packet.encode()).hexdigest()}
    reference_hash = content_hash({"review_status": "reviewed"})
    judge = judgment()
    judge["assessment"] = {"id": packet + "-assessment", "case_id": "case-" + group,
                           "packet": packet_ref, "reference_hash": reference_hash, "context": context}
    judge["_runtime"] = {"assessment_contract_hash": context["assessment_contract_hash"],
                         "metadata": {"requested_effort": context["judge_runtime"].get("effort"),
                                      "configured_effort": context["judge_runtime"].get("effort"),
                                      "observed_efforts": None}}
    return {"packet_id": packet, "case_id": "case-" + group, "group": group,
            "packet": packet_ref, "reference_hash": reference_hash, "assessment_context": context,
            "human": {"reviewer": "Fixture human reviewer", "dimensions": {name: 3 for name in DIMENSIONS}, "critical_failure": True},
            "judge": judge}


@pytest.fixture
def protocol(tmp_path):
    result = {
        "_config_root": str(tmp_path), "source_revision": "a" * 40, "operator_context": "A technically literate desktop user without project context.",
        "product_wrapper_hashes": {path: "c" * 64 for path in PRODUCT_WRAPPER_FILES},
        "runtime": {role: {"model": "explicit-model", "image": "test@sha256:" + "b" * 64,
                          "harness": "codex", "effort": "high"} for role in ("packager", "judge", "optimizer")},
        "trials": {"training": 1, "validation": 1, "final": 1, "pilot": 1},
        "retry": {"external_attempts": 1, "assessment_attempts": 1},
        "rubric": {"status": "calibrated", "dimensions": {name: {"weight": 1, "anchors": {str(index): "Fixed score anchor " + str(index) for index in range(5)}} for name in DIMENSIONS}},
        "calibration": {"minimum_agreement": .9, "minimum_critical_detection": 1},
        "search": {"proposal_count": 1, "minibatch_size": 1, "concurrency": 1, "seed": 1, "sampler": "epoch_shuffled", "skip_perfect": False,
                   "completion": {"max_iterations": 1, "stagnation_iterations": None}, "confirmation": {"enabled": False}},
        "protected": {"spans": [{"name": "contract", "text": "Fixed contract"}], "semantic_requirements": ["Preserve accurate claims."]},
        "final_decision": {"minimum_gain": 1, "bootstrap_samples": 100, "minimum_groups": 2, "maximum_interval_width": 20,
                           "case_type_regressions": {name: 0 for name in CASE_TYPES},
                           "human_review": {"method": "strict_pair_majority", "required": True}},
    }
    for name, group in (("record", "calibration"), ("check_record", "check")):
        write_records(result, name, [calibration_record(name, group, result)])
    return result


def write_records(protocol, name, records):
    path = Path(protocol["_config_root"]) / (name + ".json")
    path.write_text(json.dumps(records))
    protocol["calibration"][name] = {"path": path.name, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}


def datasets():
    return {partition: [{"id": partition, "group": partition, "types": list(CASE_TYPES), "expected_terminal": "packaged",
                         "references": {"review_status": "reviewed"}, "visual_evidence": {"required": False}}]
            for partition in ("trainset", "valset", "final")}


def test_verified_calibration_and_canonical_coverage_pass(protocol):
    assert readiness(protocol, datasets()) == []


def test_readiness_requires_the_visual_five_anchor(protocol):
    protocol["assessment_status"] = "uncalibrated"
    protocol["rubric"]["status"] = "uncalibrated"
    visual = protocol["rubric"]["dimensions"]["visual_explanation"]
    visual["max_score"] = 5
    assert any("0 to 5" in error for error in readiness(protocol, datasets()))
    visual["anchors"]["5"] = "The visuals convey the essential model accurately with little prose."
    assert readiness(protocol, datasets()) == []
    protocol["rubric"]["dimensions"]["clarity"]["max_score"] = 5
    assert any("clarity.max_score" in error for error in readiness(protocol, datasets()))


def test_a_calibrated_label_cannot_replace_human_reference_evidence(protocol):
    protocol["calibration"]["record"] = "self-declared acceptance"
    errors = readiness(protocol, datasets())
    assert any("file path and its content hash" in error for error in errors)


def test_explicit_uncalibrated_experiment_can_evolve_without_reference_packets(protocol):
    protocol["assessment_status"] = "uncalibrated"
    protocol["rubric"]["status"] = "draft"
    protocol.pop("calibration")
    assert readiness(protocol, datasets()) == []


def test_uncalibrated_experiment_cannot_claim_calibrated_rubric(protocol):
    protocol["assessment_status"] = "uncalibrated"
    assert any("uncalibrated rubric" in error for error in readiness(protocol, datasets()))


def test_unknown_assessment_status_cannot_bypass_calibration(protocol):
    protocol["assessment_status"] = "unknown"
    assert any("Set assessment_status" in error for error in readiness(protocol, datasets()))


def test_calibration_records_cannot_change_after_their_hash_is_fixed(protocol, tmp_path):
    (tmp_path / "record.json").write_text("[]")
    assert any("content hash differs" in error for error in calibration_errors(protocol))


def test_final_cases_cannot_enter_calibration(protocol, tmp_path):
    path = tmp_path / "record.json"
    path.write_text(json.dumps([calibration_record("record", "final", protocol)]))
    protocol["calibration"]["record"]["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
    assert any("final case groups" in error for error in calibration_errors(protocol, datasets()))


def test_check_records_require_independent_packets_and_groups(protocol, tmp_path):
    path = tmp_path / "check_record.json"
    path.write_text(json.dumps([calibration_record("record", "calibration", protocol)]))
    protocol["calibration"]["check_record"]["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
    errors = calibration_errors(protocol)
    assert any("distinct packets" in error for error in errors)
    assert any("independent case groups" in error for error in errors)


def test_calibration_recomputes_scores_instead_of_trusting_passed_flag(protocol, tmp_path):
    path = tmp_path / "record.json"
    record = calibration_record("record", "calibration", protocol)
    record["judge"]["dimensions"] = judgment(score=0)["dimensions"]
    record["passed"] = True
    path.write_text(json.dumps([record]))
    protocol["calibration"]["record"]["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
    assert any("fails its calibration thresholds" in error for error in calibration_errors(protocol))


@pytest.mark.parametrize("bad", [float("inf"), float("nan"), True, -1])
def test_nonfinite_or_invalid_numeric_values_block_model_operations(protocol, bad):
    protocol["rubric"]["dimensions"]["clarity"]["weight"] = bad
    protocol["final_decision"]["minimum_gain"] = bad
    protocol["calibration"]["minimum_agreement"] = bad
    errors = readiness(protocol, datasets())
    assert any("rubric.clarity" in error for error in errors)
    assert any("final_decision.minimum_gain" in error for error in errors)
    assert any("minimum_agreement" in error for error in errors)


def test_cases_with_media_limits_and_wrong_terminal_cannot_enter_quality_sets(protocol):
    cases = datasets()
    cases["trainset"][0]["visual_evidence"] = {"required": True, "status": "missing"}
    cases["valset"][0]["expected_terminal"] = "infra"
    errors = readiness(protocol, cases)
    assert any("supplied application media" in error for error in errors)
    assert any("cannot enter a packet quality dataset" in error for error in errors)


def test_empty_or_overlapping_partitions_cannot_support_a_full_experiment(protocol):
    assert any("complete experiment requires" in error for error in readiness(protocol, {}))
    cases = datasets()
    cases["final"][0]["group"] = "trainset"
    assert any("occurs in trainset and final" in error for error in readiness(protocol, cases))
    assert validate_partitions({"trainset": [{"id": "one", "group": "group"}], "final": [{"id": "variant", "group": "group"}]})


def test_source_and_image_identifiers_require_complete_hexadecimal_hashes(protocol):
    protocol["source_revision"] = "z" * 40
    protocol["runtime"]["judge"]["image"] = "image@sha256:missing"
    errors = readiness(protocol, datasets())
    assert "Set source_revision." in errors
    assert "Set runtime.judge.image." in errors


def test_local_content_addressed_image_identifiers_are_immutable(protocol):
    for role in protocol["runtime"]:
        protocol["runtime"][role]["image"] = "sha256:" + "c" * 64
    for name, group in (("record", "calibration"), ("check_record", "check")):
        write_records(protocol, name, [calibration_record(name, group, protocol)])
    assert readiness(protocol, datasets()) == []


def test_minibatches_cannot_repeat_cases_to_exceed_the_training_set(protocol):
    protocol["search"]["minibatch_size"] = 2
    assert any("exceeds the training set" in error for error in readiness(protocol, datasets()))


def test_unsupported_sampler_and_unset_confirmation_policy_block_search(protocol):
    protocol["search"]["sampler"] = "different"
    protocol["search"]["confirmation"] = {"enabled": True, "reference": "original"}
    protocol["trials"]["confirmation"] = 1
    errors = readiness(protocol, datasets())
    assert "Set search.sampler." in errors
    assert "Set search.confirmation.counter_policy." in errors


def test_malformed_protected_requirements_block_optimizer_calls(protocol):
    protocol["protected"] = {"spans": [{"name": "contract"}], "semantic_requirements": [None]}
    errors = readiness(protocol, datasets())
    assert "Set protected.spans." in errors
    assert "Set protected.semantic_requirements." in errors


def test_contract_trials_require_only_source_packager_and_external_retries():
    protocol = {"source_revision": "a" * 40,
                "runtime": {"packager": {"model": "explicit-model", "image": "sha256:" + "b" * 64,
                                         "harness": "codex", "effort": "high"}},
                "retry": {"external_attempts": 1}}
    assert contract_readiness(protocol) == []
    protocol["runtime"]["packager"]["model"] = " "
    protocol["retry"]["external_attempts"] = True
    errors = contract_readiness(protocol)
    assert "Set runtime.packager.model." in errors
    assert "Set retry.external_attempts." in errors
    assert not any("judge" in error or "rubric" in error for error in errors)


@pytest.mark.parametrize("field,value", [
    ("model", "different-model"), ("harness", "claude"),
    ("image", "sha256:" + "d" * 64), ("viewport", {"width": 1024, "height": 900}),
    ("base_url", "https://example.invalid"), ("channel", "different-channel"),
    ("effort", "medium"),
])
def test_calibration_cannot_transfer_to_a_different_judge_runtime(protocol, field, value):
    protocol["runtime"]["judge"][field] = value
    assert any("assessment context differs" in error for error in calibration_errors(protocol))


@pytest.mark.parametrize("change", ["operator", "anchor", "guidance", "critical", "wrapper"])
def test_calibration_cannot_transfer_to_a_different_assessment_context(protocol, change):
    if change == "operator":
        protocol["operator_context"] = "A different operator context."
    elif change == "anchor":
        protocol["rubric"]["dimensions"]["clarity"]["anchors"]["4"] = "A different score anchor."
    elif change == "guidance":
        protocol["rubric"]["assessment_guidance"] = ["A different assessment requirement."]
    elif change == "critical":
        protocol["rubric"]["critical_rules"] = ["A different critical failure definition."]
    else:
        protocol["product_wrapper_hashes"][PRODUCT_WRAPPER_FILES[0]] = "d" * 64
    assert any("assessment context differs" in error for error in calibration_errors(protocol))


def test_calibration_context_excludes_aggregate_weights_and_status_labels(protocol):
    fixed = calibration_context(protocol)
    protocol["rubric"] |= {"status": "draft", "calibration_status": "uncalibrated", "weight_status": "provisional"}
    protocol["rubric"]["dimensions"]["clarity"]["weight"] = 7
    protocol["runtime"]["judge"] |= {"model_name": "display label", "credentials": {"private": "fixture"}}
    assert calibration_context(protocol) == fixed
    assert calibration_errors(protocol) == []


def test_calibration_context_normalizes_the_default_viewport(protocol):
    fixed = calibration_context(protocol)
    protocol["runtime"]["judge"]["viewport"] = {"width": 1280, "height": 900}
    assert calibration_context(protocol) == fixed


@pytest.mark.parametrize("role", ["packager", "judge", "optimizer"])
@pytest.mark.parametrize("value", [None, "", "HIGH", "automatic", True, ["high"]])
def test_new_runs_require_explicit_supported_effort(protocol, role, value):
    protocol["runtime"][role]["effort"] = value
    assert any(f"runtime.{role}.effort" in error for error in readiness(protocol, datasets()))
    if role != "optimizer":
        assert any(f"runtime.{role}.effort" in error for error in readiness(protocol, full=False))
    if role == "packager":
        assert any("runtime.packager.effort" in error for error in contract_readiness(protocol))


def test_missing_effort_does_not_receive_an_implicit_default(protocol):
    for settings in protocol["runtime"].values():
        settings.pop("effort")
    original = copy.deepcopy(protocol)
    assert "effort" not in calibration_context(protocol)["judge_runtime"]
    errors = readiness(protocol, datasets())
    for role in protocol["runtime"]:
        assert any(f"runtime.{role}.effort" in error for error in errors)
        assert "effort" not in protocol["runtime"][role]
    assert protocol == original


def test_historical_calibration_keeps_its_unrecorded_effort(protocol):
    protocol["runtime"]["judge"].pop("effort")
    for name, group in (("record", "calibration"), ("check_record", "check")):
        record = calibration_record(name, group, protocol)
        record["judge"]["_runtime"].pop("metadata")
        write_records(protocol, name, [record])
    assert "effort" not in calibration_context(protocol)["judge_runtime"]
    assert calibration_errors(protocol) == []
    assert any("runtime.judge.effort" in error for error in readiness(protocol, datasets()))


def test_packager_and_optimizer_effort_do_not_change_the_judge_context(protocol):
    fixed = calibration_context(protocol)
    for role in ("packager", "optimizer"):
        protocol["runtime"][role]["effort"] = "medium"
    assert calibration_context(protocol) == fixed


def test_effort_choices_follow_the_selected_harness(protocol):
    assert effort_choices("codex") == ("low", "medium", "high", "xhigh", "max", "ultra")
    assert effort_choices("claude") == ("low", "medium", "high", "xhigh", "max")
    assert effort_choices("unknown") == effort_choices(None) == ()
    protocol["runtime"]["packager"] |= {"harness": "claude", "effort": "ultra"}
    assert any("runtime.packager.effort" in error for error in readiness(protocol, full=False))
    protocol["runtime"]["packager"]["harness"] = "codex"
    assert not any("runtime.packager.effort" in error for error in readiness(protocol, full=False))


@pytest.mark.parametrize("field,value", [
    ("requested_effort", None), ("requested_effort", "medium"),
    ("configured_effort", None), ("configured_effort", "medium"),
    ("observed_efforts", []), ("observed_efforts", ["medium"]),
    ("observed_efforts", ["high", "medium"]),
])
def test_imported_calibration_requires_the_actual_recorded_effort(protocol, field, value):
    record = calibration_record("record", "calibration", protocol)
    record["judge"]["_runtime"]["metadata"][field] = value
    write_records(protocol, "record", [record])
    assert any("Judge effort differs" in error for error in calibration_errors(protocol))


@pytest.mark.parametrize("observed", [None, ["high"]])
def test_imported_calibration_can_record_unavailable_or_matching_effort_observations(protocol, observed):
    record = calibration_record("record", "calibration", protocol)
    record["judge"]["_runtime"]["metadata"]["observed_efforts"] = observed
    write_records(protocol, "record", [record])
    assert calibration_errors(protocol) == []


@pytest.mark.parametrize("field", ["packet", "references", "context", "identity", "legacy"])
def test_calibration_requires_exact_judge_binding(protocol, field):
    record = calibration_record("record", "calibration", protocol)
    binding = record["judge"]["assessment"]
    if field == "packet":
        binding["packet"] = None
    elif field == "references":
        binding["reference_hash"] = "e" * 64
    elif field == "context":
        binding["context"] = {}
    elif field == "identity":
        binding["case_id"] = "different-case"
    else:
        record.pop("assessment_context")
    write_records(protocol, "record", [record])
    errors = calibration_errors(protocol)
    assert any("assessment context differs" in error or "Judge assessment differs" in error for error in errors)


def test_calibration_records_bind_current_case_references_and_group(protocol):
    record = calibration_record("record", "calibration", protocol)
    cases = {"trainset": [{"id": record["case_id"], "group": record["group"],
                           "references": {"review_status": "reviewed"}}]}
    assert calibration_errors(protocol, cases) == []
    cases["trainset"][0]["references"]["facts"] = ["The source facts changed."]
    assert any("selected case references or group" in error for error in calibration_errors(protocol, cases))
    cases["trainset"][0]["references"] = {"review_status": "reviewed"}
    cases["trainset"][0]["group"] = "different-group"
    assert any("selected case references or group" in error for error in calibration_errors(protocol, cases))


@pytest.mark.parametrize("field", ["human", "dimensions"])
def test_malformed_human_calibration_returns_a_specific_readiness_error(protocol, field):
    record = calibration_record("record", "calibration", protocol)
    if field == "human":
        record["human"] = []
    else:
        record["human"]["dimensions"] = []
    write_records(protocol, "record", [record])
    errors = calibration_errors(protocol)
    assert any("human reviewer" in error or "six score dimensions" in error for error in errors)


@pytest.mark.parametrize("uninspected", [False, True])
def test_raw_render_failure_diagnostics_cannot_qualify_a_calibration_reference(protocol, uninspected):
    record = calibration_record("record", "calibration", protocol)
    record["judge"]["_runtime"] = {"render_failures": [{"kind": "failed_images", "section": "Technical"}]}
    if uninspected:
        record["judge"]["uninspected_content"] = ["Technical"]
    write_records(protocol, "record", [record])
    assert any("complete Judge inspection" in error for error in calibration_errors(protocol))


@pytest.mark.parametrize("field", ["packet", "assessment"])
def test_calibration_and_check_records_require_independent_actual_content(protocol, field):
    left = calibration_record("record", "calibration", protocol)
    right = calibration_record("check_record", "check", protocol)
    if field == "packet":
        right["packet"] = left["packet"]
        right["judge"]["assessment"]["packet"] = left["packet"]
    else:
        right["judge"]["assessment"]["id"] = left["judge"]["assessment"]["id"]
    write_records(protocol, "check_record", [right])
    errors = calibration_errors(protocol)
    expected = "distinct packet content" if field == "packet" else "distinct Judge assessments"
    assert any(expected in error for error in errors)


def test_freeze_binds_exact_wrapper_bytes_without_rejecting_unrelated_changes(protocol, tmp_path):
    source = tmp_path / "source"
    source.mkdir()
    subprocess.run(["git", "init", "--quiet", str(source)], check=True)
    subprocess.run(["git", "-C", str(source), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                    "commit", "--allow-empty", "--quiet", "-m", "Fixture source"], check=True)
    protocol["source_revision"] = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    protocol.pop("product_wrapper_hashes")
    for path in PRODUCT_WRAPPER_FILES:
        target = source / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text("Fixture wrapper: " + path)
    for path in ("evals/pyproject.toml", "evals/uv.lock", "evals/culler_evals/evaluator.py",
                 "runner/fixture.ts", "evals/bridge/fixture.ts"):
        target = source / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text("Fixture source: " + path)
    from test_product_sources import source_files
    source_files(source)
    fixed = freeze_protocol(protocol, source)
    assert fixed["runtime"]["judge"]["effort"] == "high"
    assert fixed["protocol_hash"] == digest({key: value for key, value in fixed.items() if key != "protocol_hash"})
    different_effort = copy.deepcopy(protocol)
    different_effort["runtime"]["judge"]["effort"] = "medium"
    changed = freeze_protocol(different_effort, source)
    assert changed["protocol_hash"] != fixed["protocol_hash"]
    assert calibration_context(changed)["judge_runtime"]["effort"] == "medium"
    assert fixed["product_wrapper_hashes"] == product_wrapper_hashes(source)
    assert fixed["assessment_contract"] == assessment_contract()
    assert fixed["assessment_contract_hash"] == calibration_context(fixed)["assessment_contract_hash"]
    assert "product_wrapper_hashes" not in protocol
    assert "assessment_contract" not in protocol
    (source / "unrelated.txt").write_text("An unrelated checkout change.")
    assert freeze_protocol(fixed, source) == fixed
    verify_frozen_sources(fixed, source)
    for name in ("mc/verbs/brief.go", "mc/verbs/skills/packager/mc-packager/SKILL.md"):
        target = source / name
        body = target.read_bytes()
        target.write_text("A later product input edit.")
        with pytest.raises(ProtocolError, match="product source changed"):
            verify_frozen_sources(fixed, source)
        with pytest.raises(ProtocolError, match="product source changed"):
            freeze_protocol(fixed, source)
        target.write_bytes(body)
    evaluator = source / "evals/culler_evals/evaluator.py"
    original = evaluator.read_text()
    evaluator.write_text("A later evaluator edit.")
    with pytest.raises(ProtocolError, match="framework source changed"):
        verify_frozen_sources(fixed, source)
    with pytest.raises(ProtocolError, match="framework source differs"):
        freeze_protocol(fixed, source)
    evaluator.write_text(original)
    for field, value in (("assessment_contract", {}), ("assessment_contract_hash", "d" * 64)):
        with pytest.raises(ProtocolError, match="Create a new protocol"):
            freeze_protocol(fixed | {field: value}, source)
    (source / PRODUCT_WRAPPER_FILES[0]).write_text("Different wrapper bytes.")
    with pytest.raises(ProtocolError, match="wrapper differs"):
        freeze_protocol(fixed, source)


@pytest.mark.parametrize("field,value", [("runtime", []), ("rubric", []), ("product_wrapper_hashes", None)])
def test_invalid_context_shapes_fail_with_a_protocol_error(protocol, field, value):
    protocol[field] = value
    with pytest.raises(ProtocolError):
        calibration_context(protocol)


@pytest.mark.parametrize("field", ["SEMANTIC_CONTRACTS", "SEMANTIC_CHECKS", "SECTIONS", "REQUIRED_SECTIONS",
                                   "PACKET_CONTRACT_REVISION", "JUDGE_INSTRUCTIONS"])
def test_changed_packet_contract_or_judge_instructions_invalidate_calibration(protocol, monkeypatch, field):
    from culler_evals import assessment
    value = getattr(assessment, field)
    if isinstance(value, dict):
        value = value | {"risk_and_rollback": "A different risk requirement."}
    elif isinstance(value, tuple):
        value = (*value, "A different requirement")
    elif isinstance(value, int):
        value += 1
    else:
        value += "A different Judge instruction."
    monkeypatch.setattr(assessment, field, value)
    assert any("assessment context differs" in error or "required semantic packet contract" in error
               for error in calibration_errors(protocol))


@pytest.mark.parametrize("field", ["assessment_contract", "assessment_contract_hash"])
def test_frozen_contract_mismatch_requires_a_new_protocol(protocol, field):
    protocol[field] = {} if field == "assessment_contract" else "d" * 64
    with pytest.raises(ProtocolError, match="Create a new protocol"):
        calibration_context(protocol)


@pytest.mark.parametrize("missing", [False, True])
def test_imported_calibration_requires_actual_runtime_contract_identity(protocol, missing):
    record = calibration_record("record", "calibration", protocol)
    if missing:
        record["judge"]["_runtime"].pop("assessment_contract_hash")
    else:
        record["judge"]["_runtime"]["assessment_contract_hash"] = "d" * 64
    write_records(protocol, "record", [record])
    assert any("exact packet contract and instruction identity" in error for error in calibration_errors(protocol))


@pytest.mark.parametrize("role", ["packager", "judge", "optimizer"])
@pytest.mark.parametrize("value", [None, "true", 1])
def test_fast_mode_rejects_invalid_values(protocol, role, value):
    protocol["runtime"][role]["fast_mode"] = value
    assert any(f"runtime.{role}.fast_mode" in error for error in readiness(protocol, datasets()))


def test_fast_mode_requires_supported_harness(protocol):
    protocol["runtime"]["packager"] |= {"harness": "unknown", "fast_mode": True}
    assert any("runtime.packager.fast_mode" in error for error in readiness(protocol, datasets()))
    assert any("runtime.packager.fast_mode" in error for error in contract_readiness(protocol))


def test_fast_mode_presets_keep_high_effort():
    for name in ("packager.json", "packager.production-short.json", "packager.test.json"):
        protocol = read_protocol(Path(__file__).resolve().parents[2] / "evals/config" / name)
        for role in ("packager", "judge", "optimizer"):
            assert protocol["runtime"][role]["fast_mode"] is True
            assert protocol["runtime"][role]["effort"] == "high"


@pytest.mark.parametrize("role", ["packager", "judge", "optimizer"])
def test_claude_fast_mode_accepts_explicit_values(protocol, role):
    protocol["assessment_status"] = "uncalibrated"
    protocol["rubric"]["status"] = "uncalibrated"
    protocol["runtime"][role]["harness"] = "claude"
    for enabled in (True, False):
        protocol["runtime"][role]["fast_mode"] = enabled
        assert readiness(protocol, datasets()) == []
        assert not contract_readiness(protocol)
