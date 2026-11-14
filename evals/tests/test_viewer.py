import json
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

from culler_evals.records import RecordStore
from culler_evals.viewer import export_view, view_payload
from culler_evals.view_server import LiveViewer


def test_case_score_headers_identify_the_visual_five_range(tmp_path):
    rubric = json.loads((Path(__file__).resolve().parents[1] / "config/rubric.experiment.json").read_text())
    with RecordStore(tmp_path / "records") as store:
        store.write_manifest("protocol", {"rubric": rubric})
        store.append("trial_assessed", {"quality": 100, "contribution": 100,
                     "dimensions": {name: dimension["max_score"] for name, dimension in rubric["dimensions"].items()}},
                     stage="validation", case_id="one", candidate_id="D0")
        page_path = export_view(store, tmp_path / "viewer")
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page()
        page.goto(page_path.as_uri())
        page.locator("#trialscores summary").click()
        headers = page.locator("#casescores th").all_text_contents()
        assert "visual explanation / 5" in headers
        assert "comprehension / 4" in headers
        assert "clarity / 4" in headers
        assert page.locator("#casescores tr").nth(1).locator("td").nth(headers.index("visual explanation / 5")).inner_text() == "5.0"
        browser.close()


def test_case_labels_use_task_titles_without_exposing_private_references(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        store.write_manifest("case_corpus", {"trainset": [
            {"id": "nested", "group": "first", "types": ["refactor"],
             "title": "Obsolete top-level title", "role_inputs": {"task": {"title": "Share the note decoder"}},
             "references": {"facts": ["Private reference fact"]}},
            {"id": "legacy", "title": "Legacy case title"},
            {"id": "fallback"},
        ]})
        cases = view_payload(store)["cases"]["trainset"]
    assert [case["title"] for case in cases] == ["Share the note decoder", "Legacy case title", "fallback"]
    assert cases[0]["group"] == "first"
    assert all("references" not in case and "role_inputs" not in case for case in cases)


def test_display_sidecar_requires_the_preserved_case_hash(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        prepared = store.put_blob({"case_id": "case-one", "manifest_hash": "fixed-case-hash",
                                   "group_id": "group-one", "types": ["internal_refactor"]})
        store.write_manifest("case_display", {"cases": {"valset": [
            {"id": "case-one", "manifest_hash": "fixed-case-hash", "title": "Share the decoder", "prepared": prepared},
            {"id": "case-one", "manifest_hash": "changed-case-hash", "title": "Unbound title", "prepared": prepared},
        ]}})
        cases = view_payload(store)["cases"]["valset"]
    assert [case["title"] for case in cases] == ["Share the decoder"]


def test_missing_batch_preserves_the_incomplete_view(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        batch = store.put_blob({"outputs": []})
        operation = store.begin_operation("evaluation")
        store.end_operation(operation, "success", {"batch": batch})
        (store.root / batch["path"]).unlink()
        payload = view_payload(store)
    assert payload["evaluation_batches"] == {}
    assert payload["completeness"]["complete"] is False
    assert payload["completeness"]["artifact_errors"]


def test_preserved_runtime_result_identifies_model_execution_before_assessment(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        runtime = store.put_blob({"metadata": {"runtime_source_hash": "source-hash", "harness": "codex",
                                               "effective_models": ["recorded-model"]}})
        store.write_manifest("trial-slot-fixed-sample", {"runtime_result": runtime})
        event = store.append("role_result_preserved", {"sample_slot": "fixed-sample"})
        payload = view_payload(store)
    assert payload["model_execution_events"] == [event["id"]]


def test_actual_runtime_evidence_overrides_fixture_label_and_pending_setup_is_truthful(tmp_path):
    folder = tmp_path / "records"
    with RecordStore(folder) as store:
        store.write_manifest("protocol", {"runtime": {"packager": {"model": "configured-model"}}})
        store.append("run_session_started", {"continued": False}, stage="preparation")
    with LiveViewer(folder) as live, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page()
        page.goto(live.url)
        page.locator("#diagnostics summary").click()
        assert page.locator("#recordkind").inner_text() == "Model experiment · runtime results pending"
        assert page.locator("#newrecords").is_hidden()
        with RecordStore(folder) as store:
            store.write_manifest("protocol", {"status": "token-free demonstration", "model_calls": 0})
            store.append("trial_assessed", {"metadata": {"runtime_source_hash": "source-hash", "harness": "codex",
                         "effective_models": ["recorded-model"]}, "quality": 60, "contribution": 60,
                         "contract_passed": True, "critical_failures": [], "dimensions": {}, "feedback": []},
                         stage="training_parent", trial_id="actual-runtime-record", case_id="one-case")
        page.reload()
        page.locator("#diagnostics summary").click()
        assert page.locator("#recordkind").inner_text() == "Actual model execution recorded"
        assert "zero model calls" not in page.locator("header").inner_text()
        assert page.locator("#newrecords").is_hidden()
        browser.close()


def test_setup_shows_each_selected_effort_and_labels_historical_effort_as_unrecorded(tmp_path):
    protocol = {"status": "token-free demonstration", "model_calls": 0, "runtime": {
        "packager": {"model": "configured-packager", "harness": "codex", "effort": "high", "fast_mode": True},
        "judge": {"model": "configured-judge", "harness": "claude", "effort": "medium", "fast_mode": True},
        "optimizer": {"model": "historical-optimizer", "harness": "codex"},
    }}
    with RecordStore(tmp_path / "records") as store:
        store.write_manifest("protocol", protocol)
        store.append("run_session_started", {"continued": False}, stage="preparation")
        page_path = export_view(store, tmp_path / "viewer")
        assert store.read_manifest("protocol") == protocol
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(page_path.as_uri())
        page.locator("#setup summary").click()
        assert page.locator("#setupdetails th").all_text_contents() == ["Role", "Model", "Harness", "Effort", "Fast mode"]
        rows = page.locator("#setupdetails tr")
        actual = {rows.nth(index).locator("td").first.inner_text(): rows.nth(index).locator("td").all_text_contents()
                  for index in range(1, rows.count())}
        assert actual == {"packager": ["packager", "configured-packager", "codex", "high", "On"],
                          "judge": ["judge", "configured-judge", "claude", "medium", "On"],
                          "optimizer": ["optimizer", "historical-optimizer", "codex", "Effort unrecorded", "Unrecorded"]}
        page.locator("#setup").screenshot(path=str(tmp_path / "effort-setup-desktop.png"))
        page.set_viewport_size({"width": 390, "height": 844})
        expect(page.locator("#setupdetails").get_by_text("Effort unrecorded", exact=True)).to_be_visible()
        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        page.locator("#setup").screenshot(path=str(tmp_path / "effort-setup-narrow.png"))
        assert not errors
        browser.close()


def test_rejected_child_shows_exact_batch_trials_and_preserves_phase_history(tmp_path):
    folder = tmp_path / "records"
    with RecordStore(folder) as store:
        store.write_manifest("protocol", {"status": "token-free demonstration", "model_calls": 0})
        store.write_manifest("case_corpus", {"trainset": [
            {"id": "training-case", "role_inputs": {"task": {"title": "Configure the log level"}}},
        ]})
        store.append("candidate_created", {"status": "original", "parent_ids": [],
                                           "texts": {"directive": "Original directive."}}, candidate_id="D0")
        completions = {}
        for candidate, side, count in (("D0", "parent", 2), ("D1", "child", 1)):
            if side == "child":
                created = store.append("candidate_created", {"status": "proposed", "parent_ids": ["D0"],
                         "texts": {"directive": "Actual child directive."}, "directive": store.put_blob("Actual child directive.")},
                         candidate_id="D1", proposal_task_id="1-0")
            operation = store.begin_operation("evaluation", candidate_id=candidate, stage="training_" + side)
            trials = []
            for number in range(1, count + 1):
                row = {"trial_id": f"trial-{side}-{number}", "trial_number": number, "trial_count": count,
                       "quality": 60, "contribution": 60, "contract_passed": True,
                       "critical_failures": [], "dimensions": {}, "feedback": ["The case condition remains clear."]}
                store.append("trial_assessed", row, candidate_id=candidate, stage="training_" + side,
                             case_id="training-case", trial_id=row["trial_id"])
                trials.append(row)
            assessed = store.append("case_assessed", {"mean": 60, "trials": trials}, candidate_id=candidate,
                                    case_id="training-case", stage="training_" + side)
            if side == "parent":
                parent_assessed = assessed
            batch = store.put_blob({"outputs": [{"case_id": "training-case", "event_id": assessed["id"],
                                                 "mean": 60, "trials": trials}], "scores": [.6]})
            completions[side] = store.end_operation(operation, "success", {"batch": batch})
        store.append("minibatch_decision", {"case_ids": ["training-case"], "parent_scores": [.6], "child_scores": [.6],
                     "outcome": "minibatch_rejected", **{side + "_evaluation": {"event_id": event["id"]}
                                                          for side, event in completions.items()}},
                     candidate_id="D1", stage="training", proposal_task_id="1-0")
        selected = store.append("search_selected", {}, candidate_id="D0", stage="search")
        store.append("final_comparison_skipped", {"reason": "The controlled fixture skips the final comparison."}, stage="final")
    with LiveViewer(folder) as live, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1440, "height": 1000})
        page.goto(live.url)
        assert page.locator("#phasebar .current").inner_text() == "Final holdout"
        page.locator("#position").fill(str(selected["sequence"] - 1))
        page.locator("#position").dispatch_event("input")
        assert page.locator("#phasebar .current").inner_text() == "Final holdout"
        assert "Viewing: Returned candidate" in page.locator("#viewing").inner_text()
        assert "History paused" in page.locator("#historynote").inner_text()
        page.locator("#rejected").click()
        page.locator(".node").filter(has_text="D1").click()
        panel = page.locator("#candidateexplanation")
        assert "Actual child directive." in panel.locator("pre").first.text_content()
        assert "Configure the log level" in panel.locator("table").inner_text()
        assert "60.0" in panel.locator("table").inner_text()
        assert "a tie rejects the child" in panel.inner_text()
        panel.get_by_role("button", name="2 trials", exact=True).click()
        assert page.locator("#position").input_value() == str(parent_assessed["sequence"] - 1)
        page.locator("#latest").click()
        page.locator(".node").filter(has_text="D1").click()
        panel.get_by_role("button", name="Parent batch evidence").click()
        expect(page.locator("#artifacttext")).to_contain_text("trial-parent-2")
        page.locator("#position").fill(str(created["sequence"] - 1))
        page.locator("#position").dispatch_event("input")
        assert panel.locator("table").count() == 0
        assert page.locator("#artifactpanel").is_hidden()
        browser.close()


def test_historical_view_excludes_future_scores_and_links_other_graph_parents(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        store.write_manifest("protocol", {"status": "token-free demonstration"})
        store.append("candidate_created", {"status": "original", "parent_ids": []}, candidate_id="D0", stage="preparation")
        store.append("validation_scored", {"scores": {"validation-1": .6}, "mean": .6}, candidate_id="D0", stage="validation")
        store.append("candidate_created", {"status": "proposed", "parent_ids": ["D0"]}, candidate_id="D1", stage="proposal")
        store.append("candidate_status", {"status": "scope_rejected"}, candidate_id="D1", stage="scope")
        page_path = export_view(store, tmp_path / "viewer")
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(page_path.as_uri())
        page.locator("#position").fill("0")
        page.locator("#position").dispatch_event("input")
        assert page.locator("#matrix tbody tr").count() == 0
        assert page.locator(".node").count() == 1
        assert "D0" in page.locator(".node").inner_text()
        assert "No stored validation mean" in page.locator(".node").inner_text()
        page.locator("#latest").click()
        page.locator("#validationscores summary").click()
        assert "60.0" in page.locator("#matrix").inner_text()
        page.locator("#rejected").click()
        assert page.locator(".node").count() == 1
        page.locator(".node").click()
        page.get_by_role("button", name="Open parent D0").click()
        assert page.locator("#candidateheading").inner_text() == "Candidate D0"
        assert not errors
        browser.close()


def test_partial_validation_does_not_appear_as_a_stored_validation_mean(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        store.append("candidate_created", {"status": "original", "parent_ids": []}, candidate_id="D0", stage="preparation")
        for case_id, value in (("validation-one", 80), ("validation-two", 40)):
            store.append("case_assessed", {"mean": value, "trials": [{}, {}]},
                         candidate_id="D0", stage="validation", case_id=case_id)
        store.append("validation_scored", {"scores": {"validation-one": .8, "validation-two": .4}, "mean": .6},
                     candidate_id="D0", stage="validation")
        page_path = export_view(store, tmp_path / "viewer")
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page()
        page.goto(page_path.as_uri())
        for position in (1, 2):
            page.locator("#position").fill(str(position))
            page.locator("#position").dispatch_event("input")
            text = page.locator(".node").inner_text()
            assert any(label in text for label in ("Partial validation", "No validation score", "No stored validation mean"))
        page.locator("#latest").click()
        assert "60.0 / 100" in page.locator(".node").inner_text()
        page.locator("#validationscores summary").click()
        assert "2 trials" in page.locator("#matrix").inner_text()
        browser.close()


def test_graph_applies_known_decisions_before_later_status_events(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        store.append("candidate_created", {"status": "original", "parent_ids": []}, candidate_id="D0", stage="preparation")
        store.append("candidate_created", {"status": "proposed", "parent_ids": ["D0"]}, candidate_id="D1", stage="proposal")
        store.append("scope_checked", {"accepted": False, "reason": "The edit changes a protected requirement."},
                     candidate_id="D1", stage="scope")
        store.append("candidate_created", {"status": "proposed", "parent_ids": ["D0"]}, candidate_id="D2", stage="proposal")
        store.append("minibatch_decision", {"outcome": "minibatch_accepted", "case_ids": ["training-one"],
                     "parent_scores": [.3], "child_scores": [.4]}, candidate_id="D2", stage="training")
        page_path = export_view(store, tmp_path / "viewer")
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page()
        page.goto(page_path.as_uri())
        child = page.locator(".node").filter(has_text="D2").inner_text()
        assert "minibatch accepted" in child
        assert "awaits its training comparison" not in child
        page.locator("#position").fill("2")
        page.locator("#position").dispatch_event("input")
        page.locator("#rejected").click()
        assert page.locator(".node").count() == 1
        assert "scope check rejected" in page.locator(".node").inner_text()
        browser.close()


def test_pilot_view_preserves_raw_quality_beside_failed_contributions(tmp_path):
    trials = [
        {"quality": 75.0, "contribution": 75.0, "contract_passed": True,
         "critical_failures": [], "dimensions": {"comprehension": 3}, "feedback": ["The explanation preserves the material conditions."]},
        {"quality": 87.5, "contribution": 0.0, "contract_passed": True,
         "critical_failures": [{"category": "invented_verification"}], "dimensions": {"comprehension": 4}, "feedback": []},
        {"quality": None, "contribution": 0.0, "contract_passed": False,
         "critical_failures": [], "dimensions": {}, "feedback": ["The required packet is absent."]},
    ]
    with RecordStore(tmp_path / "records") as store:
        store.append("trial_assessed", trials[0], stage="pilot", case_id="pilot-case")
        store.append("case_assessed", {"mean": 25.0, "trials": trials}, stage="pilot", case_id="pilot-case")
        page_path = export_view(store, tmp_path / "viewer")
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(page_path.as_uri())
        assert page.locator("#stage").text_content() == "Culler · pilot"
        assert "Judge remains uncalibrated" in page.locator("#summary").inner_text()
        assert "75.0 to 87.5" in page.locator("#summary").inner_text()
        rows = page.locator("#assessment-scores tr")
        assert rows.count() == 4
        assert rows.nth(2).inner_text().split() == ["2", "87.5", "0.0", "Pass", "1"]
        assert rows.nth(3).inner_text().split() == ["3", "Absent", "0.0", "Fail", "0"]
        page.locator("#position").fill("0")
        page.locator("#position").dispatch_event("input")
        assert page.locator("#assessment-scores tr").count() == 2
        assert "87.5" not in page.locator("#summary").inner_text()
        assert not errors
        browser.close()


def test_pilot_view_uses_fixed_assessment_status_directive_and_trial_identity(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        store.write_manifest("protocol", {"assessment_status": "calibrated",
                             "seed_candidate": {"packager_directive": "original"},
                             "evaluated_candidate": {"packager_directive": "custom"}})
        for identifier in ("first", "second"):
            store.append("trial_assessed", {"trial_id": identifier, "quality": 75, "contribution": 75,
                         "contract_passed": True, "critical_failures": [], "dimensions": {}, "feedback": []},
                         trial_id=identifier, case_id="pilot-case", stage="pilot")
        page_path = export_view(store, tmp_path / "viewer")
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page()
        page.goto(page_path.as_uri())
        summary = page.locator("#summary").inner_text()
        assert "calibrated Judge" in summary
        assert "explicitly supplied directive" in summary
        assert "unchanged source directive" not in summary
        assert "uncalibrated" not in summary
        assert "one fixed directive" in page.locator("#graph").inner_text()
        page.locator("#validationscores summary").click()
        assert "no validation set" in page.locator("#matrix").inner_text()
        assert page.locator("#assessment-scores tr").nth(1).locator("td").first.inner_text() == "2"
        page.locator("#position").fill("0")
        page.locator("#position").dispatch_event("input")
        assert page.locator("#assessment-scores tr").nth(1).locator("td").first.inner_text() == "1"
        browser.close()


def test_revisited_training_case_uses_recorded_trial_ordinal(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        store.write_manifest("protocol", {})
        for index, number in enumerate((1, 2, 3, 1)):
            store.append("trial_assessed", {"trial_id": str(index), "trial_number": number, "trial_count": 3,
                         "quality": 75, "contribution": 75, "contract_passed": True,
                         "critical_failures": [], "dimensions": {}, "feedback": []},
                         trial_id=str(index), case_id="revisited-case", candidate_id="parent", stage="training_parent")
        page_path = export_view(store, tmp_path / "viewer")
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page()
        page.goto(page_path.as_uri())
        assert page.locator("#assessment-scores tr").nth(1).locator("td").first.inner_text() == "1 of 3"
        page.locator("#summary details").evaluate("element => element.open = true")
        assert page.locator("#summary h3").inner_text() == "Trial 1 of 3"
        browser.close()


def test_large_candidate_generation_preserves_arrow_coordinates_inside_the_graph(tmp_path):
    with RecordStore(tmp_path / "records") as store:
        store.write_manifest("protocol", {"status": "token-free demonstration", "model_calls": 0})
        store.append("candidate_created", {"status": "original", "parent_ids": []}, candidate_id="D0", stage="preparation")
        for number in range(1, 10):
            store.append("candidate_created", {"status": "proposed", "parent_ids": ["D0"]},
                         candidate_id=f"D{number}", stage="proposal")
        page_path = export_view(store, tmp_path / "viewer")
    with sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.goto(page_path.as_uri())
        expect(page.locator(".node")).to_have_count(10)
        expect(page.locator(".edges > path")).to_have_count(9)
        geometry = page.locator(".edges").evaluate("element => ({width: element.getBoundingClientRect().width, coordinates: element.viewBox.baseVal.width})")
        assert abs(geometry["width"] - geometry["coordinates"]) <= 1
        assert page.locator("#graph").evaluate("element => element.scrollWidth > element.clientWidth")
        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        page.set_viewport_size({"width": 390, "height": 844})
        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        browser.close()
