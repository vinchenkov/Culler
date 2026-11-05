import json
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import pytest
from playwright.sync_api import expect, sync_playwright

from culler_evals.records import RecordStore
from culler_evals.view_server import LiveViewer


def test_loopback_server_tracks_journal_and_restricts_artifacts(tmp_path):
    with RecordStore(tmp_path / "run") as store:
        store.write_manifest("protocol", {"assessment_status": "uncalibrated"})
        artifact = store.put_blob("The artifact contains fixed evidence.")
        store.append("candidate_created", {"texts": {"directive": "Original"}, "directive": artifact}, candidate_id="D0")
        with LiveViewer(store.root) as viewer:
            payload = json.load(urlopen(viewer.url + "records.json"))
            assert payload["live"] is True
            assert len(payload["events"]) == 1
            store.append("candidate_status", {"status": "stored"}, candidate_id="D0")
            assert len(json.load(urlopen(viewer.url + "records.json"))["events"]) == 2
            response = urlopen(viewer.url + artifact["path"])
            assert response.read() == b"The artifact contains fixed evidence."
            assert response.headers["X-Content-Type-Options"] == "nosniff"
            for path in ("../events.jsonl", "../../.writer.lock", "blobs/not-a-hash"):
                with pytest.raises(HTTPError) as error:
                    urlopen(viewer.url + path)
                assert error.value.code == 404
            with pytest.raises(HTTPError) as error:
                urlopen(Request(viewer.url, headers={"Host": "a-remote-host.example"}))
            assert error.value.code == 404


def test_live_browser_preserves_history_and_inspects_verified_artifacts(tmp_path):
    with RecordStore(tmp_path / "run") as store:
        store.write_manifest("protocol", {"assessment_status": "uncalibrated", "rubric": {
            "dimensions": {"clarity": {"weight": 1}}}})
        store.write_manifest("case_corpus", {"valset": [{"id": "validation-case", "title": "Explain the export path"}]})
        artifact = store.put_blob("The original directive is fixed.")
        store.append("candidate_created", {"texts": {"directive": "Original"}, "directive": artifact,
                     "status": "original", "parent_ids": []}, candidate_id="D0", stage="preparation")
        with LiveViewer(store.root) as viewer, sync_playwright() as driver:
            browser = driver.chromium.launch()
            page = browser.new_page(viewport={"width": 1440, "height": 1000})
            errors = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.goto(viewer.url)
            expect(page.locator("#follow")).to_be_checked()
            expect(page.locator("#assessmentstatus")).to_contain_text("Judge is uncalibrated")
            page.locator("#position").fill("0")
            page.locator("#position").dispatch_event("input")
            store.append("trial_assessed", {"trial_id": "t1", "trial_number": 1, "trial_count": 1, "quality": 80, "contribution": 80,
                         "dimensions": {"clarity": 4}, "contract_passed": True, "critical_failures": []},
                         trial_id="t1", candidate_id="D0", case_id="validation-case", stage="validation")
            store.append("validation_scored", {"scores": {"validation-case": .8}, "mean": .8},
                         candidate_id="D0", stage="validation")
            expect(page.locator("#counter")).to_have_text("1 / 3")
            assert page.locator("#matrix tbody tr").count() == 0
            assert "validation-case" not in page.locator("#casescores").inner_text()
            expect(page.locator("#currentcounts")).to_contain_text("1 completed trial")
            page.locator("#follow").check()
            expect(page.locator("#counter")).to_have_text("3 / 3")
            expect(page.locator("#casescores")).to_contain_text("80.0")
            expect(page.locator("#casescores")).to_contain_text("4.0")
            page.locator("#position").fill("1")
            page.locator("#position").dispatch_event("input")
            expect(page.locator("#ownership")).to_have_text("Candidate D0 · Explain the export path · Trial 1 of 1")
            assert "validation-case" not in page.locator("#ownership").inner_text()
            assert "validation-case" in page.locator("#eventdetail").text_content()
            page.locator("#latest").click()
            page.locator(".node").click()
            page.locator("#candidatelinks").get_by_role("button", name="directive", exact=True).click()
            expect(page.locator("#artifacttext")).to_have_text("The original directive is fixed.")
            page.locator("#position").fill("0")
            page.locator("#position").dispatch_event("input")
            expect(page.locator("#artifactpanel")).to_be_hidden()
            page.locator("#follow").check()
            store.append("candidate_created", {"texts": {"directive": "Rejected"}, "parent_ids": ["D0"],
                         "status": "proposed", "hypothesis": "This edit makes the sequence clear."}, candidate_id="D1", stage="proposal")
            store.append("scope_checked", {"accepted": False, "reason": "The edit removes a required condition."}, candidate_id="D1", stage="scope")
            expect(page.locator("#counter")).to_have_text("5 / 5")
            page.locator("#rejected").click()
            expect(page.locator(".node")).to_have_count(1)
            page.locator(".node").click()
            expect(page.locator("#candidateexplanation")).to_contain_text("removes a required condition")
            assert not errors
            browser.close()


def test_slow_live_refresh_has_one_request_and_recovers_after_failure(tmp_path):
    with RecordStore(tmp_path / "run") as store:
        store.write_manifest("protocol", {"status": "token-free demonstration", "model_calls": 0})
        store.append("candidate_created", {"status": "original", "parent_ids": []}, candidate_id="D0")
        with LiveViewer(store.root) as viewer, sync_playwright() as driver:
            browser = driver.chromium.launch()
            page = browser.new_page()
            page.goto(viewer.url)
            store.append("candidate_status", {"status": "stored"}, candidate_id="D0")
            observed = page.evaluate("""async () => {
                const original = window.fetch;
                let active = 0, maximum = 0, requests = 0;
                window.fetch = async (...args) => {
                    requests++; active++; maximum = Math.max(maximum, active);
                    try {
                        await new Promise(resolve => setTimeout(resolve, 2400));
                        return await original(...args);
                    } finally { active--; }
                };
                await Promise.all([refresh(), refresh(), refresh()]);
                window.fetch = original;
                return {maximum, requests};
            }""")
            assert observed == {"maximum": 1, "requests": 1}
            expect(page.locator("#counter")).to_have_text("2 / 2")
            page.evaluate("""async () => {
                const original = window.fetch;
                window.fetch = async () => { throw new Error('The controlled request failed.'); };
                await refresh();
                window.fetch = original;
                await refresh();
            }""")
            expect(page.locator("#connection")).to_have_text("Live records · connected")
            browser.close()


def test_live_workspace_keeps_current_phase_separate_from_deliberate_history(tmp_path):
    with RecordStore(tmp_path / "run") as store:
        store.write_manifest("protocol", {"assessment_status": "uncalibrated"})
        store.append("candidate_created", {"texts": {"directive": "Original"},
                     "status": "original", "parent_ids": []}, candidate_id="D0", stage="preparation")
        store.append("validation_scored", {"scores": {"validation-one": .8}, "mean": .8},
                     candidate_id="D0", stage="validation")
        with LiveViewer(store.root) as viewer, sync_playwright() as driver:
            browser = driver.chromium.launch()
            page = browser.new_page(viewport={"width": 1440, "height": 900})
            errors = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.goto(viewer.url)
            expect(page.locator("#follow")).to_be_checked()
            expect(page.locator("#counter")).to_have_text("2 / 2")
            expect(page.locator("#phasebar .current")).to_have_text("Validation")
            assert page.locator("h1").count() == 1
            assert not page.locator("#setup").evaluate("element => element.open")
            assert not page.locator("#diagnostics").evaluate("element => element.open")
            assert page.locator("#graph").bounding_box()["y"] < 360
            node = page.locator(".node").bounding_box()
            assert node["y"] + node["height"] < 900
            assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
            page.screenshot(path=str(tmp_path / "viewer-desktop-live.png"))
            page.locator("#position").fill("0")
            page.locator("#position").dispatch_event("input")
            expect(page.locator("#follow")).not_to_be_checked()
            expect(page.locator("#viewing")).to_contain_text("Viewing: Fixed inputs")
            expect(page.locator("#historynote")).to_contain_text("History paused")
            expect(page.locator("#phasebar .current")).to_have_text("Validation")
            store.append("candidate_created", {"texts": {"directive": "Child"},
                         "status": "proposed", "parent_ids": ["D0"]}, candidate_id="D1", stage="proposal")
            page.evaluate("refresh()")
            expect(page.locator("#counter")).to_have_text("1 / 3")
            expect(page.locator("#phasebar .current")).to_have_text("Directive edit")
            expect(page.locator("#viewing")).to_contain_text("Viewing: Fixed inputs")
            expect(page.locator(".node")).to_have_count(1)
            page.screenshot(path=str(tmp_path / "viewer-desktop-history.png"))
            page.locator("#latest").click()
            expect(page.locator("#follow")).to_be_checked()
            expect(page.locator("#counter")).to_have_text("3 / 3")
            expect(page.locator(".node")).to_have_count(2)
            expect(page.locator("#historynote")).to_have_text("Latest visible event")
            store.append("scope_checked", {"accepted": True}, candidate_id="D1", stage="scope")
            page.evaluate("refresh()")
            expect(page.locator("#counter")).to_have_text("4 / 4")
            page.screenshot(path=str(tmp_path / "viewer-desktop-ancestry.png"))
            page.set_viewport_size({"width": 390, "height": 844})
            assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
            expect(page.locator("#currentphase")).to_be_visible()
            expect(page.locator("#viewing")).to_be_visible()
            page.screenshot(path=str(tmp_path / "viewer-narrow-live.png"))
            assert not errors
            browser.close()


def test_native_callback_keeps_the_current_phase_and_history_label(tmp_path):
    with RecordStore(tmp_path / "run") as store:
        store.write_manifest("protocol", {"assessment_status": "uncalibrated"})
        store.append("candidate_created", {"status": "original", "parent_ids": []},
                     candidate_id="D0", stage="preparation")
        store.append("validation_scored", {"scores": {"validation-one": .6}, "mean": .6},
                     candidate_id="D0", stage="validation")
        store.append("native_event", {"hook": "on_validation_complete"})
        with LiveViewer(store.root) as viewer, sync_playwright() as driver:
            browser = driver.chromium.launch()
            page = browser.new_page()
            page.goto(viewer.url)
            expect(page.locator("#currentphase")).to_have_text("Current phase: Validation")
            expect(page.locator("#phasebar .current")).to_have_text("Validation")
            expect(page.locator("#viewing")).to_contain_text("Viewing: Validation")
            page.locator("#position").fill("0")
            page.locator("#position").dispatch_event("input")
            expect(page.locator("#viewing")).to_contain_text("Viewing: Fixed inputs")
            expect(page.locator("#currentphase")).to_have_text("Current phase: Validation")
            store.append("native_event", {"hook": "on_logger_event"})
            page.evaluate("refresh()")
            expect(page.locator("#counter")).to_have_text("1 / 4")
            expect(page.locator("#phasebar .current")).to_have_text("Validation")
            browser.close()


def test_validation_progress_labels_case_scores_and_hides_disposal_events(tmp_path):
    with RecordStore(tmp_path / "run") as store:
        store.write_manifest("protocol", {"assessment_status": "uncalibrated"})
        store.write_manifest("case_corpus", {"valset": [
            {"id": "validation-one", "title": "Share the decoder"},
            {"id": "validation-two", "title": "Preserve the access boundary"},
        ]})
        store.append("candidate_created", {"status": "original", "parent_ids": []},
                     candidate_id="D0", stage="preparation")
        store.append("case_assessed", {"mean": 83.3, "trials": [{}]},
                     candidate_id="D0", case_id="validation-one", stage="validation")
        for kind in ("trial_repository_disposal_started", "trial_repository_disposed", "trial_repositories_disposed"):
            store.append(kind, {}, stage="validation", candidate_id="D0", case_id="validation-one")
        with LiveViewer(store.root) as viewer, sync_playwright() as driver:
            browser = driver.chromium.launch()
            page = browser.new_page(viewport={"width": 1440, "height": 900})
            page.goto(viewer.url)
            expect(page.locator("#timeline .event")).to_have_count(2)
            expect(page.locator("#counter")).to_have_text("2 / 5")
            expect(page.locator("#historynote")).to_have_text("Latest visible event")
            expect(page.locator(".node")).to_contain_text("Validation in progress")
            expect(page.locator(".node")).to_contain_text("1 of 2 validation cases complete")
            expect(page.locator(".casescore")).to_have_text("Case 1: 83.3")
            assert page.locator(".casescore").get_attribute("title").startswith("Share the decoder")
            assert "83.3 / 100" not in page.locator(".node .score").inner_text()
            page.locator("#validationscores summary").click()
            expect(page.locator("#matrix")).to_contain_text("Case 1 · Share the decoder")
            page.locator("#technical").check()
            expect(page.locator("#timeline .event")).to_have_count(5)
            expect(page.locator("#counter")).to_have_text("5 / 5")
            page.locator("#technical").uncheck()
            expect(page.locator("#counter")).to_have_text("2 / 5")
            store.append("trial_repository_disposal_failed", {"reason": "The isolated fixture refuses the directory."},
                         stage="validation", candidate_id="D0", case_id="validation-one")
            page.evaluate("refresh()")
            expect(page.locator("#timeline .event")).to_have_count(3)
            expect(page.locator("#counter")).to_have_text("6 / 6")
            expect(page.locator("#stepname")).to_have_text("trial repository disposal failed")
            browser.close()
