import pytest

from culler_evals.comparison import calibration_report, compare, human_review_binding, validate_human_review


def test_calibration_agreement_uses_each_recorded_dimension_range():
    record = {"packet_id": "one", "score_maxima": {"visual_explanation": 5, "clarity": 4},
              "human": {"dimensions": {"visual_explanation": 5, "clarity": 4}, "critical_failure": True},
              "judge": {"dimensions": {"visual_explanation": {"score": 4}, "clarity": {"score": 3}},
                        "critical_failures": [{"category": "false_claim"}]}}
    report = calibration_report([record], minimum_agreement=.7, minimum_critical_detection=1)
    assert report["passed"] and report["agreement"] == pytest.approx((.8 + .75) / 2)
    record["judge"]["dimensions"]["visual_explanation"]["score"] = 6
    with pytest.raises(ValueError, match="rubric range"):
        calibration_report([record], minimum_agreement=.7, minimum_critical_detection=1)


def test_cluster_comparison_does_not_count_repeats_as_independent_groups():
    cases = [{"id": "one", "group": "shared", "types": ["performance"]},
             {"id": "two", "group": "shared", "types": ["performance"]}]
    def rows(value):
        return [{"case_id": case["id"], "mean": value,
                 "trials": [{"trial_id": f"{value}-{case['id']}-{index}", "contribution": value,
                             "contract_passed": True, "critical_failures": []} for index in range(5)]}
                for case in cases]
    protocol = {"trials": {"final": 5}, "search": {"seed": 1}, "rubric": {"status": "calibrated"},
                "final_decision": {"minimum_groups": 2, "bootstrap_samples": 100, "maximum_interval_width": 20,
                                   "minimum_gain": 1, "case_type_regressions": {"performance": 0}, "human_review": "confirmed_blind"}}
    result = compare(rows(60), rows(80), cases, protocol, complete=True)
    assert result["status"] == "inconclusive"
    assert result["independent_groups"] == 1
    assert result["trials_per_directive"] == 10


def comparison_sample():
    cases = [{"id": "one", "group": "first", "types": ["performance"]},
             {"id": "two", "group": "second", "types": ["performance"]}]
    def rows(value, name):
        return [{"case_id": case["id"], "mean": value,
                 "trials": [{"trial_id": f"{name}-{case['id']}", "contribution": value,
                             "contract_passed": True, "critical_failures": [],
                             "artifacts": {"packet_path": {"sha256": f"packet-{name}-{case['id']}"}}}]}
                for case in cases]
    protocol = {"trials": {"final": 1}, "search": {"seed": 1}, "rubric": {"status": "calibrated"},
                "final_decision": {"minimum_groups": 2, "bootstrap_samples": 100,
                                   "maximum_interval_width": 20, "minimum_gain": 1,
                                   "case_type_regressions": {"performance": 0}, "human_review": "confirmed_blind"}}
    return rows(60, "original"), rows(80, "candidate"), cases, protocol


def review_proof(original, candidate):
    binding = human_review_binding(original, candidate)
    return {"version": 1, "reviewer": "the human reviewer", "recorded_at": "2026-09-29T12:00:00+00:00",
            "blinded": True, "decision": "improved", "reason": "The packets explain the mechanism with less effort.",
            "binding": binding, "reviewed_packets": list(reversed(binding["packets"]))}


def test_protocol_human_flag_supplies_no_human_evidence():
    original, candidate, cases, protocol = comparison_sample()
    result = compare(original, candidate, cases, protocol, complete=True)
    assert result["status"] == "awaiting_human_review"
    assert result["human_review"] is None


def test_human_review_binds_exact_final_trials_and_packet_artifacts():
    original, candidate, cases, protocol = comparison_sample()
    proof = review_proof(original, candidate)
    result = compare(original, candidate, cases, protocol, complete=True, human_review=proof)
    assert result["status"] == "qualified"
    candidate[0]["trials"][0]["artifacts"]["packet_path"]["sha256"] = "a different packet"
    assert validate_human_review(proof, original, candidate)
    assert compare(original, candidate, cases, protocol, complete=True, human_review=proof)["status"] == "awaiting_human_review"


def test_duplicate_trial_or_case_cannot_support_more_samples():
    original, candidate, cases, protocol = comparison_sample()
    candidate[0]["trials"][0]["trial_id"] = original[0]["trials"][0]["trial_id"]
    result = compare(original, candidate, cases, protocol, complete=True)
    assert result["status"] == "inconclusive" and any("unique identifier" in reason for reason in result["reasons"])
    original, candidate, cases, protocol = comparison_sample()
    candidate.append(candidate[0])
    assert compare(original, candidate, cases, protocol, complete=True)["status"] == "inconclusive"


def test_fabricated_mean_or_nonfinite_trial_is_incomplete():
    original, candidate, cases, protocol = comparison_sample()
    candidate[0]["mean"] = 100
    result = compare(original, candidate, cases, protocol, complete=True)
    assert result["status"] == "inconclusive" and any("trial contributions" in reason for reason in result["reasons"])
    candidate[0]["trials"][0]["contribution"] = float("nan")
    assert compare(original, candidate, cases, protocol, complete=True)["status"] == "inconclusive"


def test_human_reports_tie_worse_and_insufficient_evidence():
    original, candidate, cases, protocol = comparison_sample()
    for decision, status in (("tie", "inconclusive"), ("worse", "failed"), ("insufficient_evidence", "inconclusive")):
        proof = review_proof(original, candidate) | {"decision": decision}
        assert compare(original, candidate, cases, protocol, complete=True, human_review=proof)["status"] == status


def test_dimension_report_uses_only_available_paired_observations():
    original, candidate, cases, protocol = comparison_sample()
    original[0]["dimensions"] = {"clarity": 60, "visuals": None, "detail": 70}
    candidate[0]["dimensions"] = {"clarity": 90, "visuals": 80}
    original[1]["dimensions"] = {"clarity": None, "visuals": 60, "detail": 50}
    candidate[1]["dimensions"] = {"clarity": 20, "visuals": None, "detail": 80}
    result = compare(original, candidate, cases, protocol, complete=True)
    assert result["dimension_gains"] == {"clarity": 30, "detail": 30}


def test_absent_cases_and_invalid_trial_identifiers_are_inconclusive():
    original, candidate, cases, protocol = comparison_sample()
    assert compare([], [], [], protocol, complete=True)["status"] == "inconclusive"
    candidate[0]["trials"][0]["trial_id"] = {"invalid": "identifier"}
    assert compare(original, candidate, cases, protocol, complete=True)["status"] == "inconclusive"
