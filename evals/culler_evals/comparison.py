"""Paired analysis that preserves independent case groups."""

from __future__ import annotations

import math
import random
from collections import defaultdict
from datetime import datetime
from statistics import mean

from .records import content_hash


def quantile(values: list[float], probability: float) -> float:
    values = sorted(values)
    position = (len(values) - 1) * probability
    lower = int(position)
    upper = min(lower + 1, len(values) - 1)
    return values[lower] + (values[upper] - values[lower]) * (position - lower)


def human_review_binding(original: list[dict], candidate: list[dict]) -> dict:
    packets = []
    for row in original + candidate:
        for trial in row["trials"]:
            artifact = trial.get("artifacts", {}).get("packet_path")
            if artifact:
                packets.append({"trial_id": trial["trial_id"], "sha256": artifact["sha256"]})
    return {"original_hash": content_hash(original), "candidate_hash": content_hash(candidate),
            "packets": sorted(packets, key=lambda packet: packet["trial_id"])}


def validate_human_presentation(presentation: dict, original: list[dict], candidate: list[dict]) -> list[str]:
    if not isinstance(presentation, dict):
        return ["The human presentation requires an object."]
    expected = {}
    expected_pairs = set()
    for left, right in zip(original, candidate, strict=True):
        for baseline, evolved in zip(left["trials"], right["trials"], strict=True):
            pair = frozenset((baseline["trial_id"], evolved["trial_id"]))
            expected_pairs.add(pair)
            for identity, trial in (("original", baseline), ("candidate", evolved)):
                artifact = trial.get("artifacts", {}).get("packet_path")
                if not artifact:
                    return ["The human presentation requires every final packet artifact."]
                expected[trial["trial_id"]] = {"identity": identity, "case_id": left["case_id"],
                                              "sha256": artifact["sha256"]}
    if presentation.get("binding") != human_review_binding(original, candidate):
        return ["The human presentation does not identify the exact final results and packet artifacts."]
    comparisons = presentation.get("comparisons")
    if not isinstance(comparisons, list) or len(comparisons) != len(expected_pairs):
        return ["The human presentation must retain every final pair exactly once."]
    identifiers = set()
    pairs = set()
    for comparison in comparisons:
        if not isinstance(comparison, dict):
            return ["Each human comparison requires an object."]
        identifier = comparison.get("id")
        if not isinstance(identifier, str) or not identifier or identifier in identifiers:
            return ["Each human comparison requires a unique identifier."]
        identifiers.add(identifier)
        packets = comparison.get("packets")
        if not isinstance(packets, dict) or set(packets) != {"A", "B"}:
            return ["Each human comparison requires packet aliases A and B."]
        trial_ids = []
        for packet in packets.values():
            if not isinstance(packet, dict) or not isinstance(packet.get("trial_id"), str):
                return ["Each packet alias requires its actual final trial identifier."]
            actual = expected.get(packet["trial_id"])
            if (not actual or packet.get("identity") != actual["identity"]
                    or packet.get("sha256") != actual["sha256"]
                    or comparison.get("case_id") != actual["case_id"]):
                return ["A packet alias differs from its actual final trial, directive, case, or artifact."]
            trial_ids.append(packet["trial_id"])
        pair = frozenset(trial_ids)
        if len(pair) != 2 or pair not in expected_pairs or pair in pairs:
            return ["The human presentation must preserve each matched final pair exactly once."]
        pairs.add(pair)
    return []


def validate_human_review(review: dict | None, original: list[dict], candidate: list[dict]) -> list[str]:
    if not isinstance(review, dict):
        return ["A human must review the final packets without directive labels."]
    errors = []
    if review.get("version") != 1:
        errors.append("The human review requires format version 1.")
    if not isinstance(review.get("reviewer"), str) or not review["reviewer"].strip():
        errors.append("The human review must identify its reviewer.")
    try:
        timestamp = datetime.fromisoformat(review.get("recorded_at", ""))
        if timestamp.tzinfo is None:
            raise ValueError()
    except (ValueError, TypeError):
        errors.append("The human review requires a timestamp with its time zone.")
    if review.get("blinded") is not True:
        errors.append("The reviewer must confirm the absence of directive labels during the comparison.")
    if review.get("decision") not in {"improved", "tie", "worse", "insufficient_evidence"}:
        errors.append("The human review requires an explicit comparison decision.")
    if not isinstance(review.get("reason"), str) or not review["reason"].strip():
        errors.append("The human review requires a reason for its decision.")
    binding = human_review_binding(original, candidate)
    if not binding["packets"]:
        errors.append("The human comparison requires actual final packet artifacts.")
    if review.get("binding") != binding:
        errors.append("The human review does not identify the exact final results and packet artifacts.")
    inspected = review.get("reviewed_packets")
    if not isinstance(inspected, list) or any(not isinstance(item, dict) for item in inspected):
        errors.append("The human review must record the packet presentation order.")
    elif sorted(inspected, key=lambda packet: str(packet.get("trial_id", ""))) != binding["packets"]:
        errors.append("The human review must include every final packet exactly once.")
    return errors


def _sample_errors(original: list[dict], candidate: list[dict], cases: list[dict], protocol: dict) -> list[str]:
    errors = []
    identifiers = set()
    if not cases:
        return ["The final comparison requires cases."]
    case_ids = {case["id"] for case in cases}
    for name, rows in (("original", original), ("candidate", candidate)):
        if len(rows) != len(cases) or {row.get("case_id") for row in rows} != case_ids:
            errors.append(f"The {name} comparison must retain each case exactly once.")
            continue
        for row in rows:
            trials = row.get("trials", [])
            if len(trials) != protocol["trials"]["final"]:
                errors.append(f"Case {row['case_id']} lacks its required trials for {name}.")
                continue
            contributions = []
            for trial in trials:
                identifier = trial.get("trial_id")
                if not isinstance(identifier, str) or not identifier or identifier in identifiers:
                    errors.append("Each final trial must have a unique identifier across both directives and all cases.")
                if isinstance(identifier, str):
                    identifiers.add(identifier)
                if trial.get("case_id", row["case_id"]) != row["case_id"]:
                    errors.append("A final trial identifies a different case from its case result.")
                value = trial.get("contribution")
                if type(value) not in (int, float) or not math.isfinite(value) or not 0 <= value <= 100:
                    errors.append("Every final trial requires a finite contribution from zero to 100.")
                else:
                    contributions.append(value)
            average = row.get("mean")
            if len(contributions) == len(trials) and (type(average) not in (int, float) or not math.isfinite(average)
                    or not math.isclose(average, mean(contributions), rel_tol=1e-10, abs_tol=1e-10)):
                errors.append(f"The recorded mean for case {row['case_id']} differs from its actual trial contributions.")
    return errors


def compare(original: list[dict], candidate: list[dict], cases: list[dict], protocol: dict, *, complete: bool,
            human_review: dict | None = None) -> dict:
    """Bootstrap independent groups, with every paired case retained."""
    sample_errors = _sample_errors(original, candidate, cases, protocol)
    if sample_errors:
        return {"status": "inconclusive", "reasons": sample_errors}
    left = {row["case_id"]: row for row in original}
    right = {row["case_id"]: row for row in candidate}
    expected = {case["id"] for case in cases}
    if set(left) != expected or set(right) != expected:
        return {"status": "inconclusive", "reasons": ["The final comparison lacks required case results."]}
    groups = defaultdict(list)
    gains = {}
    by_type = defaultdict(list)
    critical = []
    contract_failures = []
    dimension_gains = defaultdict(list)
    for case in cases:
        identifier = case["id"]
        lrow, rrow = left[identifier], right[identifier]
        if len(lrow["trials"]) != protocol["trials"]["final"] or len(rrow["trials"]) != protocol["trials"]["final"]:
            return {"status": "inconclusive", "reasons": [f"Case {identifier} lacks its required trials."]}
        gain = rrow["mean"] - lrow["mean"]
        gains[identifier] = gain
        groups[case["group"]].append(identifier)
        for name in case["types"]:
            by_type[name].append(gain)
        for trial in rrow["trials"]:
            critical.extend(trial.get("critical_failures", []))
            if not trial.get("contract_passed", False):
                contract_failures.append(trial["trial_id"])
        for name, left_score in lrow.get("dimensions", {}).items():
            right_score = rrow.get("dimensions", {}).get(name)
            if left_score is not None and right_score is not None:
                dimension_gains[name].append(right_score - left_score)
    decision = protocol["final_decision"]
    reasons = []
    gain = mean(gains.values())
    interval = None
    if len(groups) >= decision["minimum_groups"]:
        generator = random.Random(protocol["search"]["seed"])
        identifiers = sorted(groups)
        bootstrap = []
        for _ in range(decision["bootstrap_samples"]):
            sample = generator.choices(identifiers, k=len(identifiers))
            bootstrap.append(mean(gains[case] for group in sample for case in groups[group]))
        interval = [quantile(bootstrap, .025), quantile(bootstrap, .975)]
    else:
        reasons.append("The comparison has too few independent case groups.")
    if not complete:
        reasons.append("The experiment record is incomplete.")
    if protocol["rubric"].get("status") != "calibrated":
        reasons.append("The Judge lacks accepted calibration evidence.")
    if critical:
        reasons.append("The candidate records critical failures.")
    if contract_failures:
        reasons.append("The candidate fails a final packet contract.")
    if gain < decision["minimum_gain"]:
        reasons.append("The measured gain falls below the minimum useful gain.")
    if interval and interval[0] <= 0:
        reasons.append("The uncertainty interval includes zero or a loss.")
    if interval and interval[1] - interval[0] > decision["maximum_interval_width"]:
        reasons.append("The comparison lacks the required precision.")
    type_gains = {name: mean(values) for name, values in by_type.items()}
    for name, allowance in decision["case_type_regressions"].items():
        if name not in type_gains:
            reasons.append(f"The final cases cannot assess {name}.")
        elif type_gains[name] < -allowance:
            reasons.append(f"The candidate exceeds the permitted regression for {name}.")
    human_errors = validate_human_review(human_review, original, candidate)
    automatic_reasons = list(reasons)
    reasons.extend(human_errors)
    if not human_errors and human_review["decision"] != "improved":
        reasons.append("The human comparison does not confirm a useful improvement.")
    failed = bool(critical or contract_failures or gain < decision["minimum_gain"] or any(
        type_gains.get(name, 0) < -allowance for name, allowance in decision["case_type_regressions"].items()
    ))
    if human_review and not human_errors and human_review["decision"] == "worse":
        failed = True
    status = "failed" if failed else "inconclusive" if automatic_reasons else (
        "awaiting_human_review" if human_errors else "inconclusive" if reasons else "qualified")
    return {"status": status, "reasons": reasons, "original_mean": mean(row["mean"] for row in original),
            "candidate_mean": mean(row["mean"] for row in candidate), "gain": gain,
            "interval_95_percent": interval, "independent_groups": len(groups), "cases": len(cases),
            "trials_per_directive": sum(len(row["trials"]) for row in original),
            "case_gains": gains, "case_type_gains": type_gains,
            "dimension_gains": {name: mean(values) for name, values in dimension_gains.items()},
            "critical_failures": critical, "contract_failures": contract_failures,
            "human_review_binding": human_review_binding(original, candidate),
            "human_review": human_review if not human_errors else None,
            "human_review_errors": human_errors,
            "uncertainty_method": "Paired cluster bootstrap over independent case groups; equal case weights."}


def calibration_report(records: list[dict], *, maxima: dict | None = None, minimum_agreement: float, minimum_critical_detection: float) -> dict:
    if not records:
        raise ValueError("Calibration requires human reference assessments.")
    agreements = []
    errors = []
    critical_count = 0
    detected = 0
    for record in records:
        human, judge = record["human"], record["judge"]
        for dimension, score in human["dimensions"].items():
            delta = abs(score - judge["dimensions"][dimension]["score"])
            maximum = (maxima or record.get("score_maxima", {})).get(dimension, 4)
            if (type(maximum) not in (int, float) or not math.isfinite(maximum) or maximum <= 0
                    or not 0 <= score <= maximum or not 0 <= judge["dimensions"][dimension]["score"] <= maximum):
                raise ValueError("A calibration score exceeds its recorded rubric range.")
            agreements.append(1 - delta / maximum)
            if delta:
                errors.append({"packet_id": record["packet_id"], "dimension": dimension, "difference": delta})
        if human["critical_failure"]:
            critical_count += 1
            detected += bool(judge["critical_failures"])
    if not critical_count:
        raise ValueError("Calibration requires packets with known critical errors.")
    agreement = mean(agreements)
    detection = detected / critical_count
    return {"passed": agreement >= minimum_agreement and detection >= minimum_critical_detection,
            "agreement": agreement, "critical_detection": detection, "false_acceptance": critical_count - detected,
            "disagreement": errors, "packets": len(records)}
