import json
from pathlib import Path

import pytest
from playwright.sync_api import sync_playwright

from culler_evals.cli import _prepare_review, _record_review
from culler_evals.comparison import compare, human_review_binding, validate_human_review
from culler_evals.records import RecordStore


SOURCE = Path(__file__).resolve().parents[2]


def final_records(store, *, search_complete=True):
    cases = [{"id": "case-one", "group": "group-one", "types": ["performance"]},
             {"id": "case-two", "group": "group-two", "types": ["performance"]}]
    protocol = {"trials": {"final": 1}, "search": {"seed": 1}, "rubric": {"status": "calibrated"},
                "final_decision": {"minimum_groups": 2, "bootstrap_samples": 100,
                                   "maximum_interval_width": 20, "minimum_gain": 1,
                                   "case_type_regressions": {"performance": 0},
                                   "human_review": {"required": True, "method": "strict_pair_majority"}}}
    def rows(identity, contribution):
        return [{"case_id": case["id"], "mean": contribution,
                 "trials": [{"trial_id": f"{identity}-{case['id']}", "contribution": contribution,
                             "contract_passed": True, "critical_failures": [],
                             "artifacts": {"packet_path": store.put_blob(
                                 '<!doctype html><html><head><title>A sample packet</title></head><body>'
                                 '<h1>Summary</h1><p>A sample change has a representative explanation.</p>'
                                 '<details><summary>More detail</summary><p>The condition controls the result.</p></details>'
                                 '<h2>Glossary</h2><p>Condition: a requirement for the result.</p></body></html>')}}]}
                for case in cases]
    original, candidate = rows("baseline", 60), rows("evolved", 80)
    store.write_manifest("protocol", protocol)
    store.write_manifest("case_corpus", {"final": cases})
    store.write_manifest("search_result", {"search_complete": search_complete})
    store.write_manifest("final_comparison", {"original": original, "candidate": candidate,
                        "report": compare(original, candidate, cases, protocol, complete=search_complete)})
    return original, candidate, cases, protocol


def make_presentation(store, original, candidate):
    records = []
    for index, (left, right) in enumerate(zip(original, candidate, strict=True)):
        records.append({"id": f"pair-{index}", "case_id": left["case_id"], "packets": {
            "A": {"identity": "candidate", "trial_id": right["trials"][0]["trial_id"],
                  "sha256": right["trials"][0]["artifacts"]["packet_path"]["sha256"]},
            "B": {"identity": "original", "trial_id": left["trials"][0]["trial_id"],
                  "sha256": left["trials"][0]["artifacts"]["packet_path"]["sha256"]}}})
    store.write_manifest("human_presentation", {"binding": human_review_binding(original, candidate),
                                               "comparisons": records})
    return records


def review_input(path, pairs, preferences):
    record = {"version": 1, "reviewer": "The human reviewer", "blinded": True,
              "reason": "The preferred packets require less effort for representative understanding.",
              "comparisons": [{"id": pair["id"], "preference": preference,
                               "reason": "This packet makes the condition clear."}
                              for pair, preference in zip(pairs, preferences, strict=True)]}
    path.write_text(json.dumps(record))
    return record


def test_human_presentation_uses_the_product_wrapper_and_omits_directive_identity(tmp_path):
    with RecordStore(tmp_path / "experiment") as store:
        original, candidate, _, _ = final_records(store)
        output = tmp_path / "presentation"
        _prepare_review(store, SOURCE, output)
        private = store.read_manifest("human_presentation")
        template = json.loads((output / "review.json").read_text())
        assert {item["id"] for item in template["comparisons"]} == {item["id"] for item in private["comparisons"]}
        public = "\n".join(path.read_text() for path in output.iterdir())
        for rows in (original, candidate):
            for row in rows:
                assert row["trials"][0]["trial_id"] not in public
                assert row["trials"][0]["artifacts"]["packet_path"]["sha256"] not in public
        assert '"identity"' not in public
        with sync_playwright() as driver:
            browser = driver.chromium.launch()
            page = browser.new_page()
            page.goto(next(output.glob("*-A.html")).as_uri())
            frame = page.frame_locator("iframe")
            assert page.locator("iframe").get_attribute("sandbox") == "allow-popups allow-popups-to-escape-sandbox"
            assert frame.locator('meta[http-equiv="Content-Security-Policy"]').count() == 1
            assert frame.locator("h1").inner_text() == "Summary"
            frame.locator("summary").click()
            assert frame.get_by_text("The condition controls the result.").is_visible()
            browser.close()
        with pytest.raises(ValueError, match="fixed human presentation"):
            _prepare_review(store, SOURCE, tmp_path / "second")


@pytest.mark.parametrize("preferences,status", [(["A", "A"], "qualified"), (["A", "tie"], "inconclusive"),
                                                (["B", "B"], "failed"), (["A", "insufficient_evidence"], "inconclusive")])
def test_human_decision_uses_a_strict_majority_of_all_pairs(tmp_path, preferences, status):
    with RecordStore(tmp_path / "experiment") as store:
        original, candidate, _, _ = final_records(store)
        pairs = make_presentation(store, original, candidate)
        path = tmp_path / "review.json"
        review_input(path, pairs, preferences)
        operation = store.begin_operation("record_review", stage="human_review")
        report = _record_review(store, path, operation)["result"]
        assert report["status"] == status
        proof = store.read_manifest("human_review")["proof"]
        assert not validate_human_review(proof, original, candidate)
        store.end_operation(operation, "success")


def test_human_evidence_cannot_qualify_an_incomplete_search(tmp_path):
    with RecordStore(tmp_path / "experiment") as store:
        original, candidate, _, _ = final_records(store, search_complete=False)
        pairs = make_presentation(store, original, candidate)
        path = tmp_path / "review.json"
        review_input(path, pairs, ["A", "A"])
        operation = store.begin_operation("record_review", stage="human_review")
        report = _record_review(store, path, operation)["result"]
        assert report["status"] == "inconclusive"
        assert "The experiment record is incomplete." in report["reasons"]


def test_human_evidence_rejects_a_changed_packet_binding(tmp_path):
    with RecordStore(tmp_path / "experiment") as store:
        original, candidate, _, _ = final_records(store)
        pairs = make_presentation(store, original, candidate)
        path = tmp_path / "review.json"
        review_input(path, pairs, ["A", "A"])
        saved = store.read_manifest("final_comparison")
        saved["candidate"][0]["trials"][0]["artifacts"]["packet_path"] = store.put_blob("A replacement packet")
        store.write_manifest("final_comparison", saved)
        operation = store.begin_operation("record_review", stage="human_review")
        with pytest.raises(ValueError, match="exact final results"):
            _record_review(store, path, operation)
        assert store.read_manifest("human_review") is None


@pytest.mark.parametrize("alteration", ["object", "version", "comparisons", "reason"])
def test_human_input_errors_have_explicit_validation(tmp_path, alteration):
    with RecordStore(tmp_path / "experiment") as store:
        original, candidate, _, _ = final_records(store)
        pairs = make_presentation(store, original, candidate)
        path = tmp_path / "review.json"
        record = review_input(path, pairs, ["A", "A"])
        if alteration == "object":
            record = []
        elif alteration == "version":
            record["version"] = 99
        elif alteration == "comparisons":
            record["comparisons"] = None
        else:
            record["comparisons"][0]["reason"] = 1
        path.write_text(json.dumps(record))
        operation = store.begin_operation("record_review", stage="human_review")
        with pytest.raises(ValueError):
            _record_review(store, path, operation)
        assert store.read_manifest("human_review") is None


def test_changed_alias_identity_cannot_reverse_the_human_decision(tmp_path):
    with RecordStore(tmp_path / "experiment") as store:
        original, candidate, _, _ = final_records(store)
        pairs = make_presentation(store, original, candidate)
        path = tmp_path / "review.json"
        review_input(path, pairs, ["B", "B"])
        presentation = store.read_manifest("human_presentation")
        for pair in presentation["comparisons"]:
            pair["packets"]["B"]["identity"] = "candidate"
        store.write_manifest("human_presentation", presentation)
        operation = store.begin_operation("record_review", stage="human_review")
        with pytest.raises(ValueError, match="alias"):
            _record_review(store, path, operation)
        assert store.read_manifest("human_review") is None
