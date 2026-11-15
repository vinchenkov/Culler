import hashlib
import json
from pathlib import Path

import pytest

import culler_evals.calibration as module
from culler_evals.assessment import SEMANTIC_CHECKS
from culler_evals.calibration import (
    CalibrationError, calibration_assessment_requests, export_calibration_records,
    prepare_calibration_review, record_calibration_assessments, record_human_calibration,
)
from culler_evals.cases import content_hash as case_hash
from culler_evals.protocol import DIMENSIONS, digest, calibration_context, product_wrapper_hashes, source_revision, freeze_protocol
from culler_evals.records import RecordStore


SOURCE = Path(__file__).resolve().parents[2]


def fixture_protocol(rubric):
    return {"rubric": rubric, "operator_context": "Public fixed operator context",
            "source_revision": source_revision(SOURCE), "product_wrapper_hashes": product_wrapper_hashes(SOURCE),
            "runtime": {"judge": {"model": "fixture-model", "harness": "claude", "image": "sha256:" + "a" * 64,
                                    "viewport": {"width": 1280, "height": 900}}}}


def judgment(packet, case, rubric, identifier):
    context = calibration_context(fixture_protocol(rubric))
    judge = context["judge_runtime"]
    return {
        "_runtime": {"assessment_contract_hash": context["assessment_contract_hash"], "metadata": {"requested_model": judge["model"], "effective_models": [judge["model"]],
                                  "harness": judge["harness"], "image": judge["image"]},
                     "browser": {"wrapper_hashes": context["wrapper_hashes"], "viewport": judge["viewport"]}},
        "dimensions": {name: {"score": 3, "reason": "PRIVATE_JUDGE_REASON", "evidence": ["reference:behavior"]} for name in DIMENSIONS},
        "critical_failures": [], "semantic_checks": [{"id": name, "passed": True, "reason": "The visible explanation meets the contract.", "evidence": ["reference:behavior"]} for name in SEMANTIC_CHECKS],
        "uninspected_content": [], "feedback": ["PRIVATE_JUDGE_FEEDBACK"], "artifacts": ["screenshot:1"],
        "assessment": {"id": identifier, "case_id": case["id"], "packet": packet,
                       "reference_hash": case_hash(case["references"]), "rubric_hash": digest(rubric), "context": context},
    }


@pytest.fixture
def setup(tmp_path, monkeypatch):
    store = RecordStore(tmp_path / "experiment")
    rubric = {"scope": "Public fixed rubric scope", "assessment_guidance": ["Public fixed assessment guidance"], "dimensions": {name: {"weight": 1, "guidance": ["Public fixed dimension guidance " + name], "anchors": {str(index): "Public score anchor " + str(index) for index in range(5)}} for name in DIMENSIONS}}
    cases, rows, items = [], [], []
    groups = {"record": ["group-0", "group-1", "group-2"], "check_record": ["group-3", "group-4"]}
    conditions = ["weak_explanation", "excess_repetition", "material_error", "instruction_injection", "weak_explanation"]
    for index in range(5):
        case = {"id": "case-" + str(index), "group": "group-" + str(index),
                "references": {"facts": [{"after": "Public case fact " + str(index)}], "limits": ["The test fixture verifies the reference workflow."]}}
        cases.append(case)
        source = store.put_blob(f"<h1>Actual fixture packet {index}</h1>".encode())
        trial_id = "trial-" + str(index)
        assessed = judgment(source, case, rubric, "source-assessment-" + str(index))
        assessed["assessment"]["trial_id"] = trial_id
        rows.append({"case_id": case["id"], "trials": [{"trial_id": trial_id, "stage": "pilot", "artifacts": {"packet_path": source}, "judgment": store.put_blob(assessed)}]})
        for kind in ("accurate", "challenge"):
            condition = [] if kind == "accurate" else [conditions[index]]
            if kind == "accurate":
                packet = {"origin": "pilot", "sha256": source["sha256"]}
            else:
                file = tmp_path / f"challenge-{index}.html"
                file.write_text(f"<h1>Authored fixture variant {index}</h1>")
                packet = {"origin": "authored_variant", "path": str(file), "sha256": hashlib.sha256(file.read_bytes()).hexdigest()}
            review = tmp_path / f"{index}-{kind}-review.json"
            review.write_text(json.dumps({"reviewer": "Fixture independent reviewer", "result": "accepted", "packet_sha256": packet["sha256"],
                                          "case_id": case["id"], "source_trial_id": trial_id, "kind": kind, "conditions": condition}))
            items.append({"id": f"private-item-{index}-{kind}", "case_id": case["id"], "source_trial_id": trial_id,
                          "source_packet_sha256": source["sha256"], "packet": packet, "kind": kind, "conditions": condition,
                          "author": "Fixture variant author" if kind == "challenge" else None,
                          "review": {"path": str(review), "sha256": hashlib.sha256(review.read_bytes()).hexdigest()}})
    store.write_manifest("case_corpus", {"pilot": cases, "final": []})
    store.write_manifest("pilot", {"outputs": rows})
    store.write_manifest("protocol", fixture_protocol(rubric))
    monkeypatch.setattr(module, "freeze_protocol", lambda protocol, source: freeze_protocol(protocol, SOURCE))
    monkeypatch.setattr(module, "_packet_page", lambda source, packet, output, viewport: {
        "html": '<!doctype html><h1>Anonymous packet</h1>' + packet.read_text(),
        "wrapper_hashes": product_wrapper_hashes(SOURCE)})
    yield store, {"version": 1, "groups": groups, "items": items}, tmp_path / "human", cases, rubric
    store.close()


def prepare(setup):
    store, batch, output, _, _ = setup
    return prepare_calibration_review(store, Path("/fixture-source"), output, batch)


def human_record(store, output):
    record = json.loads((output / "review.json").read_text())
    record["reviewer"] = "Fixture human reviewer"
    record["blinded"] = True
    for row in record["records"]:
        row["dimensions"] = {name: 3 for name in DIMENSIONS}
        row["critical_failure"] = False
        row["reason"] = "The fixture packet matches the case facts."
    return record


def test_human_directory_hides_judge_scores_variant_labels_and_set_assignments(setup):
    store, _, output, _, _ = setup
    result = prepare(setup)
    assert result["packets"] == 10
    assert result["pending_judge_assessments"] == 5
    text = "\n".join(file.read_text() for file in output.iterdir())
    for secret in ("PRIVATE_JUDGE_REASON", "PRIVATE_JUDGE_FEEDBACK", "private-item", "authored_variant", "source-assessment", '"dataset"', '"kind"', '"group"'):
        assert secret not in text
    review = json.loads((output / "review.json").read_text())
    assert all(set(row["dimensions"].values()) == {None} and row["critical_failure"] is None for row in review["records"])
    assert "Public case fact" in text
    assert "Public fixed operator context" in text
    assert "Public fixed assessment guidance" in text
    assert "Public fixed dimension guidance comprehension" in text
    presentation = store.read_manifest("calibration_presentation")
    assert len(presentation["groups"]["record"]) == 3
    assert len(presentation["groups"]["check_record"]) == 2


def test_authored_variants_require_fresh_judge_assessments_with_exact_hashes(setup):
    store, _, _, cases, rubric = setup
    prepare(setup)
    requests = calibration_assessment_requests(store)
    assert len(requests) == 5
    request = requests[0]
    original = json.loads(store.read_blob(next(row for row in store.read_manifest("pilot")["outputs"] if row["case_id"] == request["case"]["id"])["trials"][0]["judgment"]))
    with pytest.raises(CalibrationError, match="different packet bytes"):
        record_calibration_assessments(store, {request["packet_id"]: original})
    original["assessment"]["packet"] = request["packet"]
    with pytest.raises(CalibrationError, match="fresh Judge"):
        record_calibration_assessments(store, {request["packet_id"]: original})
    assessments = {item["packet_id"]: judgment(item["packet"], item["case"], rubric, "fresh-" + item["packet_id"]) for item in requests}
    assert record_calibration_assessments(store, assessments) == {"recorded": 5, "pending": 0}


def test_export_joins_fixed_human_and_judge_assessments_into_independent_sets(setup, tmp_path):
    store, _, output, _, rubric = setup
    prepare(setup)
    human = human_record(store, output)
    assert record_human_calibration(store, human) == {"recorded": 10}
    requests = calibration_assessment_requests(store)
    assert not any("human" in item or "kind" in item or "conditions" in item for item in requests)
    with pytest.raises(CalibrationError, match="complete human and Judge"):
        export_calibration_records(store, tmp_path / "early-export")
    record_calibration_assessments(store, {item["packet_id"]: judgment(item["packet"], item["case"], rubric, "fresh-" + item["packet_id"]) for item in requests})
    references = export_calibration_records(store, tmp_path / "records")
    datasets = {name: json.loads(Path(reference["path"]).read_text()) for name, reference in references.items()}
    assert len(datasets["record"]) == 6
    assert len(datasets["check_record"]) == 4
    assert not {row["group"] for row in datasets["record"]} & {row["group"] for row in datasets["check_record"]}
    for reference in references.values():
        assert hashlib.sha256(Path(reference["path"]).read_bytes()).hexdigest() == reference["sha256"]
    with pytest.raises(CalibrationError, match="already contains human assessments"):
        record_human_calibration(store, human)


@pytest.mark.parametrize("problem", ["group_overlap", "final_group", "source_hash", "variant_hash", "review_hash", "missing_challenge"])
def test_reference_batch_rejects_invalid_provenance_and_set_leakage(setup, problem):
    store, batch, _, cases, _ = setup
    if problem == "group_overlap":
        batch["groups"]["check_record"].append("group-0")
    elif problem == "final_group":
        store.write_manifest("case_corpus", {"pilot": cases, "final": [cases[0]]})
    elif problem == "source_hash":
        batch["items"][0]["source_packet_sha256"] = "0" * 64
    elif problem == "variant_hash":
        Path(batch["items"][1]["packet"]["path"]).write_text("Altered variant")
    elif problem == "review_hash":
        Path(batch["items"][0]["review"]["path"]).write_text("Altered review")
    else:
        batch["items"].pop()
    with pytest.raises(CalibrationError):
        prepare(setup)


@pytest.mark.parametrize("problem", ["unblinded", "missing_score", "nonfinite", "boolean_score", "duplicate_packet", "missing_reason"])
def test_human_scores_require_complete_blind_assessments(setup, problem):
    store, _, output, _, _ = setup
    prepare(setup)
    record = human_record(store, output)
    if problem == "unblinded":
        record["blinded"] = False
    elif problem == "missing_score":
        record["records"][0]["dimensions"].pop("clarity")
    elif problem == "nonfinite":
        record["records"][0]["dimensions"]["clarity"] = float("nan")
    elif problem == "boolean_score":
        record["records"][0]["dimensions"]["clarity"] = True
    elif problem == "duplicate_packet":
        record["records"][0]["packet_id"] = record["records"][1]["packet_id"]
    else:
        record["records"][0]["reason"] = ""
    with pytest.raises(CalibrationError):
        record_human_calibration(store, record)


@pytest.mark.parametrize("problem", ["changed_instructions", "changed_packet", "missing_packet"])
def test_human_scores_require_the_original_rendered_presentation(setup, problem):
    store, _, output, _, _ = setup
    prepare(setup)
    record = human_record(store, output)
    packet = next(path for path in output.glob("reference-*.html"))
    if problem == "changed_instructions":
        (output / "index.html").write_text("Different human instructions")
    elif problem == "changed_packet":
        packet.write_text("Different human packet")
    else:
        packet.unlink()
    with pytest.raises(CalibrationError, match="artifact"):
        record_human_calibration(store, record)


def test_human_can_complete_the_blank_score_file_without_changing_the_presentation(setup):
    store, _, output, _, _ = setup
    prepare(setup)
    record = human_record(store, output)
    (output / "review.json").write_text(json.dumps(record))
    assert record_human_calibration(store, json.loads((output / "review.json").read_text())) == {"recorded": 10}


@pytest.mark.parametrize("changed", ["case", "rubric", "packet", "operator_context"])
def test_fixed_reference_context_cannot_change_before_the_judge_assessment(setup, changed):
    store, _, _, cases, rubric = setup
    prepare(setup)
    if changed == "case":
        cases[0]["references"]["facts"] = ["Changed reference"]
        store.write_manifest("case_corpus", {"pilot": cases, "final": []})
    elif changed == "rubric":
        rubric["dimensions"]["clarity"]["anchors"]["3"] = "Changed score anchor"
        store.write_manifest("protocol", fixture_protocol(rubric))
    elif changed == "operator_context":
        store.write_manifest("protocol", fixture_protocol(rubric) | {"operator_context": "Changed operator context"})
    else:
        request = calibration_assessment_requests(store)[0]
        Path(request["packet_path"]).write_text("Changed reference packet")
    with pytest.raises(CalibrationError, match="differs from"):
        calibration_assessment_requests(store)


def test_review_interface_exposes_five_only_for_visual_explanation(tmp_path):
    from playwright.sync_api import sync_playwright
    from culler_evals.calibration import _review_page
    rubric = json.loads((Path(__file__).resolve().parents[1] / "config/rubric.experiment.json").read_text())
    template = {"batch_id": "visual-five", "records": [{"packet_id": "one",
                "dimensions": {name: None for name in DIMENSIONS}, "critical_failure": None}]}
    page_path = tmp_path / "review.html"
    page_path.write_text(_review_page(template, [{"packet_id": "one", "page": "about:blank",
                         "facts_html": "", "limits_html": ""}], rubric, "The operator knows the Project."))
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page()
        page.goto(page_path.as_uri())
        for name in DIMENSIONS:
            options = page.locator("#score-" + name + " option").all_text_contents()
            assert ("5" in options) == (name == "visual_explanation")
        page.locator("#score-visual_explanation").select_option("5")
        assert page.locator("#anchor-visual_explanation").inner_text() == rubric["dimensions"]["visual_explanation"]["anchors"]["5"]
        browser.close()


def test_review_interface_keeps_scores_blank_and_downloads_only_complete_human_records(setup, tmp_path):
    from playwright.sync_api import sync_playwright
    _, _, output, _, _ = setup
    prepare(setup)
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto((output / "index.html").as_uri())
        assert page.locator("#position").inner_text() == "Packet 1 / 10"
        assert page.locator("#score-clarity").input_value() == ""
        page.get_by_text("Assessment instructions", exact=True).click()
        assert page.locator("#operator-context").inner_text() == "Public fixed operator context"
        assert "Public fixed assessment guidance" in page.locator("#assessment-guidance").inner_text()
        page.get_by_text("Assessment instructions", exact=True).click()
        assert page.locator("#open").get_attribute("href").endswith(".html")
        page.screenshot(path=str(tmp_path / "reference-review-initial.png"), full_page=True)
        page.locator("#save").click()
        assert "Complete each assessment" in page.locator("#message").inner_text()
        page.locator("#reviewer").fill("Fixture human reviewer")
        page.locator("#blinded").check()
        for index in range(10):
            for name in DIMENSIONS:
                page.locator("#score-" + name).select_option("3")
            page.locator("#critical").select_option("false")
            page.locator("#reason").fill("The fixture packet matches the case facts.")
            if index < 9:
                page.locator("#next").click()
                assert page.locator(".panel").evaluate("element => element.scrollTop") == 0
        assert page.locator("#message").inner_text() == ""
        page.screenshot(path=str(tmp_path / "reference-review.png"), full_page=True)
        with page.expect_download() as download:
            page.locator("#save").click()
        download.value.save_as(tmp_path / "human.json")
        record = json.loads((tmp_path / "human.json").read_text())
        assert len(record["records"]) == 10
        assert record["blinded"] is True
        assert "judge" not in record
        assert errors == []
        browser.close()


def test_legacy_source_assessments_require_fresh_reference_assessments(setup):
    store, _, output, _, _ = setup
    pilot = store.read_manifest("pilot")
    for row in pilot["outputs"]:
        trial = row["trials"][0]
        result = json.loads(store.read_blob(trial["judgment"]))
        result["assessment"].pop("context")
        result["_runtime"]["browser"].pop("wrapper_hashes")
        trial["judgment"] = store.put_blob(result)
    store.write_manifest("pilot", pilot)
    result = prepare(setup)
    assert result["pending_judge_assessments"] == 10
    presentation = store.read_manifest("calibration_presentation")
    source_items = [item for item in presentation["items"] if item["origin"] == "pilot"]
    assert len(source_items) == 5
    assert all(item["source_nonreuse_reason"] for item in source_items)
    assert "source_nonreuse_reason" not in (output / "index.html").read_text()


@pytest.mark.parametrize("changed", ["model", "mixed_models", "wrapper", "viewport"])
def test_fresh_assessment_requires_actual_matching_runtime_and_display(setup, changed):
    from culler_evals.assessment import AssessmentError
    store, _, _, _, rubric = setup
    prepare(setup)
    request = calibration_assessment_requests(store)[0]
    result = judgment(request["packet"], request["case"], rubric, "fresh-actual-binding")
    if changed == "model":
        result["_runtime"]["metadata"]["effective_models"] = ["different-model"]
    elif changed == "mixed_models":
        result["_runtime"]["metadata"]["effective_models"].append("different-model")
    elif changed == "wrapper":
        result["_runtime"]["browser"]["wrapper_hashes"] = {name: "f" * 64 for name in result["_runtime"]["browser"]["wrapper_hashes"]}
    else:
        result["_runtime"]["browser"]["viewport"]["width"] = 600
    with pytest.raises(AssessmentError, match="different"):
        record_calibration_assessments(store, {request["packet_id"]: result})
    assert len(calibration_assessment_requests(store)) == 5


@pytest.mark.parametrize("changed", ["missing_requested", "missing_configured", "null_requested", "null_configured", "requested", "configured", "observed", "mixed", "empty", "scalar"])
def test_calibration_binding_rejects_unverified_or_different_effort(setup, changed):
    from culler_evals.assessment import AssessmentError

    store, _, _, cases, rubric = setup
    protocol = store.read_manifest("protocol")
    protocol["runtime"]["judge"]["effort"] = "high"
    context = calibration_context(protocol)
    result = judgment(store.put_blob("The reference packet."), cases[0], rubric, "effort-assessment")
    result["assessment"]["context"] = context
    metadata = result["_runtime"]["metadata"]
    metadata.update(requested_effort="high", configured_effort="high", observed_efforts=["high"])
    if changed.startswith("missing_"):
        metadata.pop(changed.removeprefix("missing_") + "_effort")
    elif changed.startswith("null_"):
        metadata[changed.removeprefix("null_") + "_effort"] = None
    elif changed in ("requested", "configured"):
        metadata[changed + "_effort"] = "low"
    else:
        metadata["observed_efforts"] = {"observed": ["low"], "mixed": ["high", "low"], "empty": [], "scalar": "high"}[changed]
    with pytest.raises(AssessmentError, match="effort"):
        module.bind_assessment_context(result, protocol)


def test_calibration_binding_accepts_configured_effort_without_an_observed_value(setup):
    store, _, _, cases, rubric = setup
    protocol = store.read_manifest("protocol")
    protocol["runtime"]["judge"]["effort"] = "high"
    context = calibration_context(protocol)
    result = judgment(store.put_blob("The reference packet."), cases[0], rubric, "effort-assessment")
    result["assessment"]["context"] = context
    result["_runtime"]["metadata"].update(requested_effort="high", configured_effort="high")
    assert module.bind_assessment_context(result, protocol) == context
    result["_runtime"]["metadata"]["observed_efforts"] = None
    assert module.bind_assessment_context(result, protocol) == context
    assert result["_runtime"]["metadata"]["observed_efforts"] is None
    result["_runtime"]["metadata"]["observed_efforts"] = ["high"]
    assert module.bind_assessment_context(result, protocol) == context


def test_calibration_reuse_rejects_source_metadata_without_the_selected_effort(setup):
    from culler_evals.assessment import AssessmentError

    store, _, _, _, _ = setup
    pilot = store.read_manifest("pilot")
    saved = pilot["outputs"][0]["trials"][0]["judgment"]
    before = store.read_blob(saved)
    protocol = store.read_manifest("protocol")
    protocol["runtime"]["judge"]["effort"] = "high"
    store.write_manifest("protocol", protocol)
    with pytest.raises(AssessmentError, match="effort"):
        prepare(setup)
    assert store.read_blob(saved) == before
    assert store.read_manifest("calibration_assessments") is None


def test_historical_calibration_context_does_not_acquire_an_effort_value(setup):
    store, _, _, cases, rubric = setup
    protocol = store.read_manifest("protocol")
    result = judgment(store.put_blob("The reference packet."), cases[0], rubric, "historical-assessment")
    before = json.dumps(result, sort_keys=True)
    context = module.bind_assessment_context(result, protocol)
    assert "effort" not in context["judge_runtime"]
    assert json.dumps(result, sort_keys=True) == before


def test_complete_calibration_context_includes_operator_rubric_and_wrapper_bindings(setup, tmp_path):
    store, _, output, _, rubric = setup
    prepare(setup)
    requests = calibration_assessment_requests(store)
    record_human_calibration(store, human_record(store, output))
    record_calibration_assessments(store, {item["packet_id"]: judgment(item["packet"], item["case"], rubric, "fresh-" + item["packet_id"]) for item in requests})
    records = export_calibration_records(store, tmp_path / "records")
    expected = calibration_context(store.read_manifest("protocol"))
    for reference in records.values():
        for row in json.loads(Path(reference["path"]).read_text()):
            assert row["assessment_context"] == row["judge"]["assessment"]["context"] == expected
            assert row["reference_hash"] == row["judge"]["assessment"]["reference_hash"]


def test_calibration_requires_complete_reference_inspection(setup):
    store, _, _, _, rubric = setup
    prepare(setup)
    request = calibration_assessment_requests(store)[0]
    result = judgment(request["packet"], request["case"], rubric, "fresh-incomplete")
    result["uninspected_content"] = ["Technical"]
    result["_runtime"]["render_failures"] = [{"kind": "failed_images", "section": "Technical"}]
    with pytest.raises(CalibrationError, match="complete Judge inspection"):
        record_calibration_assessments(store, {request["packet_id"]: result})
    assert len(calibration_assessment_requests(store)) == 5


@pytest.mark.parametrize("actual_hash", [None, "0" * 64])
def test_fresh_reference_rejects_unverified_or_changed_assessment_instructions(setup, actual_hash):
    store, _, _, _, rubric = setup
    prepare(setup)
    request = calibration_assessment_requests(store)[0]
    result = judgment(request["packet"], request["case"], rubric, "fresh-contract-mismatch")
    result["_runtime"]["assessment_contract_hash"] = actual_hash
    with pytest.raises(module.AssessmentError, match="assessment instructions and packet contracts"):
        record_calibration_assessments(store, {request["packet_id"]: result})
