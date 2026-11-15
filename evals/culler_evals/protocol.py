"""Protocol validation before any model operation."""

from __future__ import annotations

import hashlib
import json
import math
import re
import subprocess
from pathlib import Path
from typing import Any

from .cases import CASE_TYPES as CASE_TYPE_DEFINITIONS, case_readiness, content_hash


DIMENSIONS = (
    "comprehension", "causal_explanation", "decision_support",
    "visual_explanation", "clarity", "packet_length",
)
CASE_TYPES = tuple(CASE_TYPE_DEFINITIONS)
PRODUCT_WRAPPER_FILES = ("dashboard/src/ui/review.js", "dashboard/src/ui/format.js")
EFFORT_CHOICES = {
    "codex": ("low", "medium", "high", "xhigh", "max", "ultra"),
    "claude": ("low", "medium", "high", "xhigh", "max"),
}


class ProtocolError(ValueError):
    """The protocol cannot support the requested operation."""


def effort_choices(harness: str) -> tuple[str, ...]:
    """Return the accepted effort syntax for the selected harness."""
    return EFFORT_CHOICES.get(harness, ()) if isinstance(harness, str) else ()


def _effort_error(settings: dict, path: str) -> str | None:
    choices = effort_choices(settings.get("harness"))
    if settings.get("effort") not in choices:
        return (f"Set {path} to one of: {', '.join(choices)}." if choices
                else f"Set {path} for a supported harness.")
    return None


def fast_mode_error(settings: dict, path: str) -> str | None:
    value = settings.get("fast_mode", False)
    if type(value) is not bool:
        return f"Set {path} to true or false."
    if value and settings.get("harness") not in ("codex", "claude"):
        return f"Use {path} with a supported harness."
    return None


def dimension_maximum(rubric: dict | None, name: str) -> int:
    maximum = ((5 if name == "visual_explanation" else 4) if rubric is None
               else rubric.get("dimensions", {}).get(name, {}).get("max_score", 4))
    allowed = (4, 5) if name == "visual_explanation" else (4,)
    if type(maximum) is not int or maximum not in allowed:
        raise ProtocolError(f"Set rubric.dimensions.{name}.max_score to " + " or ".join(map(str, allowed)) + ".")
    return maximum


def digest(value: Any) -> str:
    data = json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False)
    return hashlib.sha256(data.encode()).hexdigest()


def read_protocol(path: Path) -> dict:
    protocol = json.loads(path.read_text())
    if protocol.get("version") != 1:
        raise ProtocolError("The protocol version must equal 1.")
    if protocol.get("role") != "packager":
        raise ProtocolError("The live role adapter supports Packager only.")
    if not isinstance(protocol.get("id"), str) or not protocol["id"]:
        raise ProtocolError("The protocol requires an identifier.")
    if "rubric_file" in protocol:
        rubric_path = path.parent / protocol["rubric_file"]
        protocol["rubric"] = json.loads(rubric_path.read_text())
    private_keys = {"credentials", "api_key", "access_token", "refresh_token", "oauth_token", "auth_json", "grants", "env"}
    def check_private(value):
        if isinstance(value, dict):
            if private_keys.intersection(value):
                raise ProtocolError("Use the separate private credential file for authentication values.")
            for item in value.values():
                check_private(item)
        elif isinstance(value, list):
            for item in value:
                check_private(item)
    check_private(protocol)
    protocol["_config_root"] = str(path.parent.resolve())
    return protocol


def public_protocol(protocol: dict) -> dict:
    """Private credentials enter through a separate runtime configuration."""
    return {key: value for key, value in protocol.items() if not key.startswith("_")}


def source_revision(source: Path) -> str:
    return subprocess.check_output(
        ["git", "rev-parse", "HEAD"], cwd=source, text=True,
    ).strip()


def product_wrapper_hashes(source: Path) -> dict[str, str]:
    try:
        return {path: hashlib.sha256((Path(source) / path).read_bytes()).hexdigest() for path in PRODUCT_WRAPPER_FILES}
    except OSError as error:
        raise ProtocolError("Use a Culler checkout with both product packet wrapper files.") from error


def framework_source_hashes(source: Path) -> dict[str, str]:
    root = Path(source)
    paths = sorted((root / "evals/culler_evals").glob("*.py"))
    paths += [root / "evals/pyproject.toml", root / "evals/uv.lock"]
    return {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest() for path in paths}


def verify_frozen_sources(protocol: dict, source: Path) -> None:
    expected = protocol.get("framework_source_hashes")
    if expected is not None and expected != framework_source_hashes(source):
        raise ProtocolError("The framework source changed after the experiment started. Restore the recorded source before continuation.")
    expected = protocol.get("product_wrapper_hashes")
    if expected is not None and expected != product_wrapper_hashes(source):
        raise ProtocolError("The packet display changed after the experiment started. Restore the recorded source before continuation.")
    expected = protocol.get("product_source_manifest")
    if expected is not None:
        from .product_sources import ProductSourceError, product_source_hash, verify_product_sources
        try:
            actual = verify_product_sources(source, expected)
        except (ProductSourceError, OSError) as error:
            raise ProtocolError(str(error)) from error
        if protocol.get("product_source_hash") != actual or actual != product_source_hash(expected):
            raise ProtocolError("The product source identity differs from the fixed protocol. Use the recorded protocol and source before continuation.")


def assessment_contract() -> dict:
    """Identify the exact packet contract and fixed Judge instructions."""
    from .assessment import (JUDGE_INSTRUCTIONS, PACKET_CONTRACT_REVISION, REQUIRED_SECTIONS,
                             SECTIONS, SEMANTIC_CHECKS, SEMANTIC_CONTRACTS, judge_output_schema)
    return {"deterministic_contract_revision": PACKET_CONTRACT_REVISION,
            "section_order": list(SECTIONS), "required_sections": list(REQUIRED_SECTIONS),
            "semantic_checks": list(SEMANTIC_CHECKS), "semantic_contracts": dict(SEMANTIC_CONTRACTS),
            "judge_instructions_hash": hashlib.sha256(JUDGE_INSTRUCTIONS.encode()).hexdigest(),
            "judge_output_schema_template": judge_output_schema()}


def _fixed_assessment_contract(protocol: dict) -> dict:
    current = assessment_contract()
    if (protocol.get("assessment_contract") is not None and protocol["assessment_contract"] != current
            or protocol.get("assessment_contract_hash") is not None
            and protocol["assessment_contract_hash"] != digest(current)):
        raise ProtocolError("The assessment contract differs from the fixed protocol. Create a new protocol for the current assessment rules.")
    return current


def calibration_context(protocol: dict) -> dict:
    """Bind Judge assessments independently of aggregate weights and calibration labels."""
    roles = protocol.get("runtime")
    settings = roles.get("judge") if isinstance(roles, dict) else None
    if not isinstance(settings, dict):
        raise ProtocolError("Set the public Judge runtime configuration.")
    runtime = {key: value for key, value in settings.items() if key not in {"credentials", "model_name"}}
    viewport = runtime.get("viewport", {})
    if not isinstance(viewport, dict):
        raise ProtocolError("Set the Judge viewport width and height.")
    runtime["viewport"] = {"width": 1280, "height": 900} | viewport
    operator = protocol.get("operator_context")
    if not isinstance(operator, str) or not operator.strip():
        raise ProtocolError("Set the fixed operator context.")
    wrappers = protocol.get("product_wrapper_hashes")
    if (not isinstance(wrappers, dict) or set(wrappers) != set(PRODUCT_WRAPPER_FILES)
            or any(not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{64}", value) is None for value in wrappers.values())):
        raise ProtocolError("Set product_wrapper_hashes for the selected Culler checkout.")
    rubric = protocol.get("rubric")
    if not isinstance(rubric, dict) or not isinstance(rubric.get("dimensions"), dict):
        raise ProtocolError("Set the fixed assessment rubric.")
    assessment = {key: value for key, value in rubric.items() if key not in {"status", "calibration_status", "weight_status"}}
    if any(not isinstance(dimension, dict) for dimension in rubric["dimensions"].values()):
        raise ProtocolError("Define each rubric dimension as an assessment configuration.")
    assessment["dimensions"] = {name: {key: value for key, value in dimension.items() if key != "weight"}
                                for name, dimension in rubric["dimensions"].items()}
    return {"judge_runtime": runtime, "operator_context_hash": digest(operator),
            "rubric_assessment_hash": digest(assessment), "wrapper_hashes": dict(wrappers),
            "assessment_contract_hash": digest(_fixed_assessment_contract(protocol))}


def dataset_paths(protocol: dict, dataset: str) -> list[Path]:
    root = Path(protocol.get("_config_root", "."))
    return [(root / value).resolve() for value in protocol["datasets"][dataset]]


def validate_partitions(datasets: dict[str, list[dict]]) -> list[str]:
    errors = []
    groups: dict[str, str] = {}
    identifiers: set[str] = set()
    for partition, cases in datasets.items():
        for case in cases:
            identifier = case.get("id")
            if identifier in identifiers:
                errors.append(f"Case {identifier} occurs more than once.")
            identifiers.add(identifier)
            group = case.get("group")
            if not group:
                errors.append(f"Case {identifier} requires an independent group identifier.")
            elif group in groups and groups[group] != partition:
                errors.append(f"Group {group} occurs in {groups[group]} and {partition}.")
            groups[group] = partition
    return errors


def calibration_errors(protocol: dict, datasets: dict | None = None) -> list[str]:
    """Verify human reference records and recompute the accepted calibration."""
    calibration = protocol.get("calibration", {})
    errors = []
    try:
        expected_context = calibration_context(protocol)
    except (ProtocolError, TypeError, AttributeError, ValueError) as error:
        errors.append(f"Invalid calibration context: {error}")
        expected_context = None
    current_cases = {case["id"]: case for cases in (datasets or {}).values() for case in cases}
    thresholds = {}
    for name in ("minimum_agreement", "minimum_critical_detection"):
        value = calibration.get(name)
        if type(value) not in (float, int) or not math.isfinite(value) or not 0 <= value <= 1:
            errors.append(f"Set calibration.{name} to a finite value from zero to one.")
        else:
            thresholds[name] = value
    loaded = {}
    final_groups = {case["group"] for case in (datasets or {}).get("final", [])}
    for name in ("record", "check_record"):
        reference = calibration.get(name)
        if not isinstance(reference, dict) or not isinstance(reference.get("path"), str) or not re.fullmatch(r"[0-9a-f]{64}", str(reference.get("sha256", ""))):
            errors.append(f"Set calibration.{name} to a file path and its content hash.")
            continue
        try:
            path = (Path(protocol.get("_config_root", ".")) / reference["path"]).resolve()
            data = path.read_bytes()
            if hashlib.sha256(data).hexdigest() != reference["sha256"]:
                raise ValueError("The calibration record content hash differs.")
            records = json.loads(data)
            if not isinstance(records, list) or not records:
                raise ValueError("The calibration record requires human reference assessments.")
            packet_ids, packet_hashes, assessment_ids, groups = set(), set(), set(), set()
            from .assessment import validate_judgment
            for record in records:
                if not isinstance(record, dict) or not all(isinstance(record.get(key), str) and record[key] for key in ("packet_id", "case_id", "group")):
                    raise ValueError("Each calibration record requires packet, case, and group identifiers.")
                if record["packet_id"] in packet_ids:
                    raise ValueError("The calibration record repeats a packet identifier.")
                packet_ids.add(record["packet_id"])
                groups.add(record["group"])
                human = record.get("human", {})
                if (not isinstance(human, dict) or not isinstance(human.get("reviewer"), str)
                        or not human["reviewer"].strip() or type(human.get("critical_failure")) is not bool):
                    raise ValueError("Each calibration record requires a named human reviewer and a critical failure determination.")
                scores = human.get("dimensions", {})
                if (not isinstance(scores, dict) or set(scores) != set(DIMENSIONS)
                        or any(type(value) not in (int, float) or not math.isfinite(value) or not 0 <= value <= dimension_maximum(protocol["rubric"], dimension) for dimension, value in scores.items())):
                    raise ValueError("Human reference assessments must cover all six score dimensions.")
                validate_judgment(record.get("judge"), protocol["rubric"])
                if record["judge"]["uninspected_content"] or record["judge"].get("_runtime", {}).get("render_failures"):
                    raise ValueError("Each calibration reference requires complete Judge inspection without a confirmed render failure.")
                context = record.get("assessment_context")
                if not isinstance(context, dict) or expected_context is None or context != expected_context:
                    raise ValueError("The calibration assessment context differs from the selected Judge, rubric, operator context, or wrapper.")
                if "effort" in context["judge_runtime"]:
                    effort = context["judge_runtime"]["effort"]
                    metadata = record["judge"].get("_runtime", {}).get("metadata", {})
                    if (not isinstance(metadata, dict)
                            or metadata.get("requested_effort") != effort
                            or metadata.get("configured_effort") != effort
                            or metadata.get("observed_efforts") is not None
                            and metadata["observed_efforts"] != [effort]):
                        raise ValueError("The Judge effort differs from the fixed assessment context. Assess the reference with the recorded effort.")
                if record["judge"].get("_runtime", {}).get("assessment_contract_hash") != context["assessment_contract_hash"]:
                    raise ValueError("The Judge assessment lacks the exact packet contract and instruction identity. Assess the reference again.")
                packet = record.get("packet", {})
                packet_hash = packet.get("sha256") if isinstance(packet, dict) else None
                reference_hash = record.get("reference_hash")
                if any(not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{64}", value) is None for value in (packet_hash, reference_hash)):
                    raise ValueError("Each calibration record requires its exact packet and reference hashes.")
                if packet_hash in packet_hashes:
                    raise ValueError("The calibration record repeats packet content.")
                packet_hashes.add(packet_hash)
                binding = record["judge"].get("assessment", {})
                judged_packet = binding.get("packet") if isinstance(binding, dict) else None
                if (not isinstance(binding, dict) or not isinstance(binding.get("id"), str) or not binding["id"].strip()
                        or binding.get("case_id") != record["case_id"] or binding.get("context") != context
                        or not isinstance(judged_packet, dict) or judged_packet.get("sha256") != packet_hash
                        or binding.get("reference_hash") != reference_hash):
                    raise ValueError("The Judge assessment differs from its fixed case, packet, references, or assessment context.")
                if binding["id"] in assessment_ids:
                    raise ValueError("Each calibration packet requires a distinct Judge assessment identifier.")
                assessment_ids.add(binding["id"])
                case = current_cases.get(record["case_id"])
                if case and (case["group"] != record["group"] or content_hash(case["references"]) != reference_hash):
                    raise ValueError("The calibration record differs from the selected case references or group.")
            if groups & final_groups:
                raise ValueError("Judge calibration cannot use final case groups.")
            loaded[name] = {"records": records, "packet_ids": packet_ids, "packet_hashes": packet_hashes,
                            "assessment_ids": assessment_ids, "groups": groups}
            if len(thresholds) == 2:
                from .comparison import calibration_report
                report = calibration_report(records, maxima={dimension: dimension_maximum(protocol["rubric"], dimension) for dimension in DIMENSIONS}, **thresholds)
                if not report["passed"]:
                    raise ValueError("The human reference comparison fails its calibration thresholds.")
        except (OSError, ValueError, TypeError, KeyError) as error:
            errors.append(f"Invalid calibration.{name}: {error}")
    if len(loaded) == 2:
        if loaded["record"]["packet_ids"] & loaded["check_record"]["packet_ids"]:
            errors.append("The calibration and check records must use distinct packets.")
        if loaded["record"]["groups"] & loaded["check_record"]["groups"]:
            errors.append("The calibration and check records must use independent case groups.")
        if loaded["record"]["packet_hashes"] & loaded["check_record"]["packet_hashes"]:
            errors.append("The calibration and check records must use distinct packet content.")
        if loaded["record"]["assessment_ids"] & loaded["check_record"]["assessment_ids"]:
            errors.append("The calibration and check records must use distinct Judge assessments.")
    return errors


def contract_readiness(protocol: dict) -> list[str]:
    """Require the source and Packager runtime for separate contract trials."""
    errors = []
    source = protocol.get("source_revision")
    if not isinstance(source, str) or re.fullmatch(r"[0-9a-f]{40}", source) is None:
        errors.append("Set source_revision.")
    settings = protocol.get("runtime", {}).get("packager", {})
    if not isinstance(settings.get("model"), str) or not settings["model"].strip():
        errors.append("Set runtime.packager.model.")
    image = settings.get("image")
    if not isinstance(image, str) or re.fullmatch(r"(?:.+@)?sha256:[0-9a-f]{64}", image) is None:
        errors.append("Set runtime.packager.image.")
    if settings.get("harness") not in ("codex", "claude"):
        errors.append("Set runtime.packager.harness.")
    effort_error = _effort_error(settings, "runtime.packager.effort")
    if effort_error:
        errors.append(effort_error)
    error = fast_mode_error(settings, "runtime.packager.fast_mode")
    if error:
        errors.append(error)
    attempts = protocol.get("retry", {}).get("external_attempts")
    if type(attempts) is not int or attempts <= 0:
        errors.append("Set retry.external_attempts.")
    return errors


def readiness(protocol: dict, datasets: dict | None = None, *, full: bool = True) -> list[str]:
    errors: list[str] = []

    def require(path: str, test=lambda value: value is not None):
        value: Any = protocol
        for key in path.split("."):
            value = value.get(key) if isinstance(value, dict) else None
        if not test(value):
            errors.append(f"Set {path}.")

    positive = lambda value: type(value) is int and value > 0
    number = lambda value: type(value) in (float, int) and math.isfinite(value) and value >= 0
    require("source_revision", lambda value: isinstance(value, str) and re.fullmatch(r"[0-9a-f]{40}", value) is not None)
    require("operator_context", lambda value: isinstance(value, str) and bool(value.strip()))
    for role in ("packager", "judge", "optimizer") if full else ("packager", "judge"):
        require(f"runtime.{role}.model", lambda value: isinstance(value, str) and bool(value))
        require(f"runtime.{role}.image", lambda value: isinstance(value, str) and re.fullmatch(r"(?:.+@)?sha256:[0-9a-f]{64}", value) is not None)
        require(f"runtime.{role}.harness", lambda value: value in ("codex", "claude"))
        settings = protocol.get("runtime", {}).get(role, {})
        effort_error = _effort_error(settings if isinstance(settings, dict) else {}, f"runtime.{role}.effort")
        if effort_error:
            errors.append(effort_error)
        error = fast_mode_error(settings if isinstance(settings, dict) else {}, f"runtime.{role}.fast_mode")
        if error:
            errors.append(error)
    for phase in ("training", "validation", "final") if full else ("pilot",):
        require(f"trials.{phase}", positive)
    require("retry.external_attempts", positive)
    require("retry.assessment_attempts", positive)
    rubric = protocol.get("rubric", {})
    dimensions = rubric.get("dimensions", {})
    if set(dimensions) != set(DIMENSIONS):
        errors.append("The rubric must define the six packet quality dimensions.")
    for name in DIMENSIONS:
        dimension = dimensions.get(name, {})
        weight = dimension.get("weight")
        if type(weight) not in (int, float) or not math.isfinite(weight) or weight <= 0:
            errors.append(f"Set a positive weight for rubric.{name}.")
        anchors = dimension.get("anchors", {})
        try:
            maximum = dimension_maximum(rubric, name)
        except ProtocolError as error:
            errors.append(str(error))
            continue
        if set(anchors) != {str(index) for index in range(maximum + 1)} or not all(isinstance(value, str) and value.strip() for value in anchors.values()):
            errors.append(f"Define score anchors from 0 to {maximum} for rubric.{name}.")
    if datasets is not None:
        errors.extend(validate_partitions(datasets))
        if full and set(datasets) != {"trainset", "valset", "final"}:
            errors.append("The complete experiment requires training, validation, and final datasets.")
        for partition, cases in datasets.items():
            if full and not cases:
                errors.append(f"Assign cases to {partition}.")
            for case in cases:
                if case.get("expected_terminal") != "packaged":
                    errors.append(f"Case {case.get('id')} cannot enter a packet quality dataset.")
                for problem in case_readiness(case)["problems"]:
                    errors.append(f"Case {case.get('id')}: {problem}")
    if full:
        for key in ("proposal_count", "minibatch_size", "concurrency"):
            require(f"search.{key}", positive)
        require("search.seed", lambda value: type(value) is int)
        require("search.sampler", lambda value: value == "epoch_shuffled")
        require("search.skip_perfect", lambda value: type(value) is bool)
        require("search.confirmation.enabled", lambda value: type(value) is bool)
        completion = protocol.get("search", {}).get("completion", {})
        if not any(positive(completion.get(key)) for key in ("max_iterations", "stagnation_iterations")):
            errors.append("Set an iteration or stagnation completion rule.")
        if completion.get("stagnation_iterations") is not None:
            require("search.completion.minimum_gain", number)
        for name in ("max_iterations", "stagnation_iterations"):
            if completion.get(name) is not None and not positive(completion[name]):
                errors.append(f"Set search.completion.{name} to a positive integer.")
        if protocol.get("search", {}).get("confirmation", {}).get("enabled"):
            require("trials.confirmation", positive)
            require("search.confirmation.reference", lambda value: value == "original")
            require("search.confirmation.counter_policy", lambda value: value == "reset_on_confirmed_gain")
        require("protected.spans", lambda value: isinstance(value, list) and bool(value)
                and all(isinstance(span, dict) and all(isinstance(span.get(key), str) and span[key].strip()
                                                      for key in ("name", "text")) for span in value))
        require("protected.semantic_requirements", lambda value: isinstance(value, list) and bool(value)
                and all(isinstance(requirement, str) and requirement.strip() for requirement in value))
        require("final_decision.minimum_gain", number)
        require("final_decision.bootstrap_samples", positive)
        require("final_decision.minimum_groups", lambda value: positive(value) and value >= 2)
        require("final_decision.maximum_interval_width", number)
        require("final_decision.human_review", lambda value: isinstance(value, dict)
                and value.get("method") == "strict_pair_majority" and value.get("required") is True)
        require("final_decision.case_type_regressions", lambda value: isinstance(value, dict) and set(value) == set(CASE_TYPES)
                and all(number(allowance) and allowance <= 100 for allowance in value.values()))
        assessment_status = protocol.get("assessment_status")
        if assessment_status == "uncalibrated":
            if rubric.get("status") == "calibrated":
                errors.append("Use an uncalibrated rubric for an uncalibrated experiment.")
        elif assessment_status in (None, "calibrated"):
            if rubric.get("status") != "calibrated":
                errors.append("Complete the Judge calibration or select an explicit uncalibrated experiment.")
            errors.extend(calibration_errors(protocol, datasets))
        else:
            errors.append("Set assessment_status to calibrated or uncalibrated.")
        if datasets is not None:
            coverage = {name: set() for name in CASE_TYPES}
            for cases in datasets.values():
                for case in cases:
                    for name in case.get("types", []):
                        if name in coverage:
                            coverage[name].add(case["group"])
            for name, groups in coverage.items():
                if len(groups) < 2:
                    errors.append(f"The corpus requires multiple independent groups for {name}.")
            count = len(datasets.get("trainset", []))
            minibatch = protocol.get("search", {}).get("minibatch_size")
            if positive(minibatch) and minibatch > count:
                errors.append("The minibatch size exceeds the training set size.")
    return errors


def freeze_protocol(protocol: dict, source: Path) -> dict:
    frozen = public_protocol(protocol)
    revision = source_revision(source)
    if frozen.get("source_revision") != revision:
        raise ProtocolError("The checkout differs from the protocol source revision.")
    wrappers = product_wrapper_hashes(source)
    if frozen.get("product_wrapper_hashes") is not None and frozen["product_wrapper_hashes"] != wrappers:
        raise ProtocolError("The product packet wrapper differs from the fixed protocol.")
    frozen["product_wrapper_hashes"] = wrappers
    framework = framework_source_hashes(source)
    if frozen.get("framework_source_hashes") is not None and frozen["framework_source_hashes"] != framework:
        raise ProtocolError("The framework source differs from the fixed protocol.")
    frozen["framework_source_hashes"] = framework
    from .runtime_sources import runtime_source_manifest
    runtime_sources = runtime_source_manifest(source)
    if frozen.get("runtime_source_manifest") is not None and frozen["runtime_source_manifest"] != runtime_sources:
        raise ProtocolError("The runtime source differs from the fixed protocol.")
    frozen["runtime_source_manifest"] = runtime_sources
    frozen["runtime_source_hash"] = digest(runtime_sources)
    from .product_sources import ProductSourceError, product_source_manifest
    try:
        product_sources = product_source_manifest(source)
    except (ProductSourceError, OSError) as error:
        raise ProtocolError(str(error)) from error
    if frozen.get("product_source_manifest") is not None and frozen["product_source_manifest"] != product_sources:
        raise ProtocolError("The product source changed after the experiment started. Restore the recorded source before continuation.")
    frozen["product_source_manifest"] = product_sources
    frozen["product_source_hash"] = digest(product_sources)
    frozen["assessment_contract"] = _fixed_assessment_contract(frozen)
    frozen["assessment_contract_hash"] = digest(frozen["assessment_contract"])
    frozen.pop("protocol_hash", None)
    frozen["protocol_hash"] = digest(frozen)
    return frozen
