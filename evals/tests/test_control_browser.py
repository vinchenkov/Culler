import json
from pathlib import Path
import threading
import subprocess
import time

import pytest
from playwright.sync_api import expect, sync_playwright

from culler_evals.control_server import ControlServer
from culler_evals.records import RecordStore


SOURCE = Path(__file__).resolve().parents[2]


class ControlledProcess:
    """A token-free child writes real durable events after a short delay."""

    def __init__(self, argv, code=0):
        self.code = None
        self.desired_code = code
        self.finished = threading.Event()
        self.ready = threading.Event()
        self.output = Path(argv[argv.index("--output") + 1])
        self.protocol = json.loads(Path(argv[argv.index("--protocol") + 1]).read_text())
        self.thread = threading.Thread(target=self.run, daemon=True)
        self.thread.start()

    def run(self):
        time.sleep(.8)
        with RecordStore(self.output) as store:
            store.write_manifest("protocol", self.protocol | {"status": "token-free demonstration", "model_calls": 0})
            store.append("run_session_started", {"continued": False}, stage="preparation")
            store.append("candidate_created", {"parent_ids": [], "status": "original",
                         "texts": self.protocol["seed_candidate"]}, candidate_id="D0", stage="preparation")
            operation = store.begin_operation("trial", stage="validation", candidate_id="D0",
                                              case_id="controlled-case", trial_number=1, trial_count=2)
            time.sleep(.2)
            store.append("trial_assessed", {"trial_id": "controlled-trial", "quality": 75,
                         "contribution": 75, "contract_passed": True, "critical_failures": [],
                         "dimensions": {name: 3 for name in self.protocol["rubric"]["dimensions"]},
                         "trial_number": 1, "trial_count": 2}, trial_id="controlled-trial",
                         candidate_id="D0", case_id="controlled-case", stage="validation")
            store.end_operation(operation, "success", result={"controlled": True})
            store.append("validation_scored", {"scores": {"controlled-case": .75}, "mean": .75},
                         candidate_id="D0", stage="validation")
            self.ready.set()
            self.finished.wait(15)
            store.append("run_session_completed", {"outcome": "success" if self.desired_code == 0 else "interrupted" if self.desired_code == 130 else "failed"}, stage="final")
        self.code = self.desired_code

    def poll(self):
        return self.code

    def wait(self, timeout=None):
        self.thread.join(timeout)
        return self.code

    def send_signal(self, _):
        self.desired_code = 130
        self.finished.set()


def make_server(tmp_path, outcomes=(0,), *, preset=None):
    processes = []

    def launch(argv, **_):
        process = ControlledProcess(argv, outcomes[min(len(processes), len(outcomes) - 1)])
        processes.append(process)
        return process

    def empty_docker(argv, **_):
        return subprocess.CompletedProcess(argv, 0, stdout="")

    def missing_group(*_):
        raise ProcessLookupError()

    server = ControlServer(SOURCE / "evals/config/packager.json", SOURCE, None,
                           tmp_path / "private-cache", tmp_path / "private-runs", process_factory=launch,
                           docker_runner=empty_docker, group_signal=missing_group,
                           **({"preset": preset} if preset else {}))
    return server, processes


def test_browser_configures_fixed_sets_validates_and_runs_with_real_records(tmp_path):
    server, processes = make_server(tmp_path)
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 1000})
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(server.url)
        expect(page.locator("#storage-path")).to_have_text(str(server.output_root))
        expect(page.locator("#backup-path")).to_contain_text("backups")
        expect(page.locator("#start-button")).to_be_enabled()
        expect(page.locator("#packet-estimate")).to_have_text("992 maximum packets before retries")
        expect(page.locator("#models")).to_contain_text("GPT-6.1 Sol")
        expect(page.locator("#models")).to_contain_text("Claude Opus 5.5")
        assert page.locator("#models").inner_text().count("Effort: high") == 3
        expect(page.locator("#training-count")).to_have_text("13")
        expect(page.locator("#validation-count")).to_have_text("13")
        expect(page.locator("#final-count")).to_have_text("9")
        assert not page.locator('[data-path="trials.pilot"]').count()
        page.locator("#case-details").evaluate("element => element.open = true")
        first = page.locator(".case-row select").first
        assert first.locator("option").count() == 2
        assert first.locator("option").first.get_attribute("value") == "trainset"
        first.select_option("unused")
        expect(page.locator("#training-count")).to_have_text("12")
        page.locator("#restore-cases").click()
        expect(page.locator("#training-count")).to_have_text("13")
        page.locator("#case-details").evaluate("element => element.open = false")
        field = page.locator("#field-trials-training")
        field.fill("0")
        page.locator("#start-button").click()
        expect(field).to_have_attribute("aria-invalid", "true")
        expect(field).to_have_value("0")
        expect(page.locator("#global-error")).to_contain_text("at least 1")
        assert not processes
        field.fill("3")
        expect(page.locator("#packet-estimate")).to_have_text("1,136 maximum packets before retries")
        page.locator("#start-button").click()
        expect(page.locator("#field-trials-training")).to_be_disabled()
        expect(page.locator("#experiment-panel")).to_be_visible()
        assert len(processes) == 1
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        expect(page.locator("#status-run")).to_contain_text("Experiment directory:")
        frame_size = page.locator("#experiment-frame").bounding_box()
        assert frame_size["height"] >= 650
        assert frame_size["y"] <= 260
        assert page.evaluate("document.documentElement.scrollHeight <= window.innerHeight")
        frame = page.frame_locator("#experiment-frame")
        expect(frame.locator("#currentcounts")).to_contain_text("1 completed")
        expect(frame.locator(".node")).to_have_count(1)
        expect(frame.locator("#recordkind")).to_have_text("Controlled fixture · zero model calls")
        page.locator("#configure-tab").click()
        expect(field).to_have_value("3")
        expect(field).to_be_disabled()
        expect(page.locator("#locked-note")).to_be_visible()
        processes[0].finished.set()
        expect(page.locator("#status-title")).to_have_text("The experiment completed.")
        expect(page.locator("#new-experiment")).to_be_visible()
        assert not errors
        assert str(server.cache) not in page.locator("body").inner_text()
        browser.close()


@pytest.mark.parametrize("width,height", [(1440, 1000), (560, 900)])
def test_active_experiment_uses_the_viewport_without_duplicate_headers(tmp_path, width, height):
    server, processes = make_server(tmp_path, preset="test")
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": width, "height": height})
        page.goto(server.url)
        page.locator("#start-button").click()
        frame = page.frame_locator("#experiment-frame")
        expect(frame.locator(".node")).to_have_count(1)
        expect(page.locator("header h1")).to_be_hidden()
        expect(page.locator("#status-panel")).to_be_hidden()
        expect(page.locator("#show-experiment")).to_be_hidden()
        expect(page.locator("#connection")).to_be_hidden()
        size = page.locator("#experiment-frame").bounding_box()
        assert size["x"] == 0
        assert size["width"] == width
        assert size["y"] <= 64
        assert size["height"] >= height - 64
        assert page.evaluate("document.documentElement.scrollHeight <= window.innerHeight")
        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        page.locator("#configure-tab").click()
        expect(page.locator("header h1")).to_be_visible()
        expect(page.locator("#field-trials-training")).to_be_disabled()
        page.locator("#experiment-tab").click()
        expect(page.locator("#status-panel")).to_be_hidden()
        processes[0].finished.set()
        expect(page.locator("#status-title")).to_have_text("The experiment completed.")
        expect(page.locator("#status-panel")).to_be_visible()
        browser.close()


def test_fresh_experiment_after_failure_keeps_separate_records_and_locks_new_inputs(tmp_path):
    server, processes = make_server(tmp_path, outcomes=(1, 0))
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        page.goto(server.url)
        expect(page.locator("#start-button")).to_be_enabled()
        page.locator("#start-button").click()
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        processes[0].finished.set()
        expect(page.locator("#status-title")).to_have_text("The experiment stopped with a failure.")
        page.locator("#new-experiment").click()
        expect(page.locator("#field-trials-training")).to_be_enabled()
        field = page.locator("#field-trials-final")
        field.fill("4")
        page.locator("#start-button").click()
        expect(page.locator("#field-trials-training")).to_be_disabled()
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        assert len(processes) == 2
        assert processes[0].output != processes[1].output
        assert processes[0].output.is_dir()
        page.locator("#configure-tab").click()
        expect(field).to_have_value("4")
        expect(field).to_be_disabled()
        processes[1].finished.set()
        expect(page.locator("#status-title")).to_have_text("The experiment completed.")
        assert len(server.status()["history"]) == 2
        browser.close()


def test_start_request_has_one_flight_and_status_poll_recovers_without_erasing_inputs(tmp_path):
    server, processes = make_server(tmp_path)
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        page.goto(server.url)
        expect(page.locator("#start-button")).to_be_enabled()
        page.locator("#field-trials-validation").fill("3")
        page.evaluate("""async () => {
            const original = window.fetch;
            let active = 0, maximum = 0, starts = 0;
            window.fetch = async (...args) => {
                if (args[0] === 'validate.json') await new Promise(resolve => setTimeout(resolve, 700));
                if (args[0] === 'start.json') starts++;
                if (args[0] === 'status.json') { active++; maximum = Math.max(maximum, active); }
                try { return await original(...args); }
                finally { if (args[0] === 'status.json') active--; }
            };
            await Promise.all([startExperiment(new Event('submit')), startExperiment(new Event('submit'))]);
            window.controlCounts = {starts, maximum};
            window.fetch = original;
        }""")
        assert page.evaluate("window.controlCounts.starts") == 1
        assert len(processes) == 1
        expect(page.locator("#field-trials-training")).to_be_disabled()
        page.locator("#configure-tab").click()
        expect(page.locator("#field-trials-validation")).to_have_value("3")
        observed = page.evaluate("""async () => {
            const original = window.fetch;
            let active = 0, maximum = 0;
            window.fetch = async (...args) => {
                active++; maximum = Math.max(maximum, active);
                try { await new Promise(resolve => setTimeout(resolve, 1900)); return await original(...args); }
                finally { active--; }
            };
            await Promise.all([refreshStatus(), refreshStatus(), refreshStatus()]);
            window.fetch = async () => { throw new Error('Controlled connection fault.'); };
            await refreshStatus();
            window.fetch = original;
            await refreshStatus();
            return maximum;
        }""")
        assert observed == 1
        expect(page.locator("#connection")).to_have_text("Connected to the local experiment server.")
        expect(page.locator("#field-trials-validation")).to_have_value("3")
        browser.close()


def test_browser_layout_and_capture_states_at_desktop_and_narrow_widths(tmp_path):
    server, processes = make_server(tmp_path, outcomes=(0, 1))
    captures = tmp_path / "captures"
    captures.mkdir()
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 1000})
        page.goto(server.url)
        expect(page.locator("#start-button")).to_be_enabled()
        page.screenshot(path=str(captures / "setup-desktop.png"), full_page=True)
        page.locator("#field-search-minibatch_size").fill("0")
        page.locator("#start-button").click()
        expect(page.locator("#global-error")).to_be_visible()
        page.screenshot(path=str(captures / "validation-error.png"), full_page=True)
        page.locator("#field-search-minibatch_size").fill("3")
        page.locator("#start-button").click()
        expect(page.locator("#status-title")).to_have_text("The experiment prepares its fixed inputs.")
        expect(page.locator("#viewer-shell")).to_be_hidden()
        page.screenshot(path=str(captures / "start-in-progress.png"), full_page=True)
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        expect(page.frame_locator("#experiment-frame").locator("#currentcounts")).to_contain_text("1 completed trial")
        page.screenshot(path=str(captures / "active-experiment.png"), full_page=True)
        page.locator("#configure-tab").click()
        page.screenshot(path=str(captures / "fixed-configuration.png"), full_page=True)
        page.set_viewport_size({"width": 560, "height": 900})
        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        page.screenshot(path=str(captures / "fixed-configuration-narrow.png"), full_page=True)
        processes[0].finished.set()
        expect(page.locator("#status-title")).to_have_text("The experiment completed.")
        page.screenshot(path=str(captures / "complete.png"), full_page=True)
        page.set_viewport_size({"width": 1280, "height": 1000})
        page.locator("#new-experiment").click()
        page.locator("#start-button").click()
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        processes[1].finished.set()
        expect(page.locator("#status-title")).to_have_text("The experiment stopped with a failure.")
        page.screenshot(path=str(captures / "failed.png"), full_page=True)
        browser.close()


def test_advanced_controls_preserve_supported_parameters_and_truthful_bounds(tmp_path):
    server, processes = make_server(tmp_path, outcomes=(130,))
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 1000})
        page.goto(server.url)
        expect(page.locator("#start-button")).to_be_enabled()
        page.locator("#advanced-details").evaluate("element => element.open = true")
        assert page.locator(".field[data-path]").count() == len(server.schema)
        expect(page.locator("#field-search-sampler")).to_be_disabled()
        for role in ("packager", "judge", "optimizer"):
            expect(page.locator(f"#field-runtime-{role}-effort")).to_have_value("high")
        expect(page.locator('[data-path="trials.confirmation"]').first).to_be_hidden()
        page.locator("#field-search-confirmation-enabled").check()
        expect(page.locator('[data-path="trials.confirmation"]').first).to_be_visible()
        expect(page.locator("#packet-estimate")).to_have_text("1,616 maximum packets before retries")
        page.locator("#field-search-confirmation-enabled").uncheck()
        page.locator("#field-runtime-judge-model").fill("controlled-judge-model")
        expect(page.locator("#models")).to_contain_text("controlled-judge-model")
        page.locator("#field-runtime-judge-viewport-width").fill("1400")
        page.locator("#field-search-completion-max_iterations").fill("")
        expect(page.locator("#packet-estimate")).to_contain_text("no simple packet maximum")
        assert "at null" not in page.locator("#completion-note").inner_text()
        page.locator("#field-search-completion-max_iterations").fill("12")
        page.locator("#start-button").click()
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        assert processes[0].protocol["runtime"]["judge"]["model"] == "controlled-judge-model"
        assert processes[0].protocol["runtime"]["judge"]["viewport"]["width"] == 1400
        assert processes[0].protocol["search"]["confirmation"]["enabled"] is False
        processes[0].finished.set()
        expect(page.locator("#status-title")).to_have_text("The experiment was interrupted.")
        expect(page.locator("#status-run")).to_contain_text(server.status()["run_id"])
        expect(page.locator("#new-experiment")).to_be_visible()
        browser.close()


def test_effort_controls_preserve_input_require_harness_choices_and_lock_the_recorded_value(tmp_path):
    server, processes = make_server(tmp_path)
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 1000})
        page.goto(server.url)
        expect(page.locator("#start-button")).to_be_enabled()
        page.locator("#advanced-details").evaluate("element => element.open = true")
        effort = page.locator("#field-runtime-packager-effort")
        effort.select_option("ultra")
        expect(page.locator("#models")).to_contain_text("Effort: ultra")
        page.locator("#field-runtime-packager-harness").select_option("claude")
        expect(effort).to_have_value("ultra")
        expect(effort).to_have_attribute("aria-invalid", "true")
        expect(page.locator("#field-runtime-packager-effort-error")).to_have_text("Select an effort supported by this harness.")
        assert effort.locator('option[value="ultra"]').is_disabled()
        page.locator("#start-button").click()
        expect(page.locator("#global-error")).to_contain_text("selected harness")
        assert not processes
        effort.select_option("high")
        page.locator("#field-runtime-judge-effort").select_option("medium")
        expect(page.locator("#models")).to_contain_text("Effort: medium")
        page.locator("#start-button").click()
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        page.locator("#configure-tab").click()
        expect(effort).to_be_disabled()
        expect(page.locator("#field-runtime-judge-effort")).to_have_value("medium")
        expect(page.locator("#field-runtime-judge-effort")).to_be_disabled()
        assert processes[0].protocol["runtime"]["packager"]["effort"] == "high"
        assert processes[0].protocol["runtime"]["judge"]["effort"] == "medium"
        assert processes[0].protocol["runtime"]["optimizer"]["effort"] == "high"
        assert not page.locator("#global-error").is_visible()
        processes[0].finished.set()
        expect(page.locator("#status-title")).to_have_text("The experiment completed.")
        browser.close()


def test_assessment_notice_uses_the_recorded_configuration_status(tmp_path):
    server, _ = make_server(tmp_path)
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 1000})

        def calibrated_config(route):
            response = route.fetch()
            configured = response.json()
            configured["fixed"]["assessment_status"] = "calibrated"
            route.fulfill(response=response, json=configured)

        page.route("**/config.json", calibrated_config)
        page.goto(server.url)
        expect(page.locator("#start-button")).to_be_enabled()
        expect(page.locator("#assessment-notice")).to_contain_text("accepted calibration evidence")
        assert "uncalibrated" not in page.locator("#assessment-notice").inner_text()
        assert not server.status()["history"]
        browser.close()


def test_saved_presets_replace_all_parameters_and_restore_the_selected_sets(tmp_path):
    server, processes = make_server(tmp_path)
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 1000})
        page.goto(server.url)
        preset = page.locator("#preset-select")
        expect(preset).to_have_value("production")
        expect(page.locator("#packet-estimate")).to_have_text("992 maximum packets before retries")
        page.locator("#field-trials-training").fill("0")
        expect(preset).to_have_value("custom")
        expect(preset.locator('option:checked')).to_have_text("Custom")
        page.locator("#start-button").click()
        expect(page.locator("#global-error")).to_be_visible()
        preset.select_option("test")
        expect(page.locator("#global-error")).to_be_hidden()
        expect(page.locator("#field-trials-training")).to_have_value("1")
        expect(page.locator("#field-trials-validation")).to_have_value("1")
        expect(page.locator("#field-trials-final")).to_have_value("1")
        expect(page.locator("#field-search-proposal_count")).to_have_value("1")
        expect(page.locator("#field-search-minibatch_size")).to_have_value("2")
        expect(page.locator("#field-search-completion-max_iterations")).to_have_value("2")
        expect(page.locator("#field-search-completion-stagnation_iterations")).to_have_value("2")
        expect(page.locator("#training-count")).to_have_text("7")
        expect(page.locator("#validation-count")).to_have_text("7")
        expect(page.locator("#final-count")).to_have_text("9")
        expect(page.locator("#total-cases")).to_have_text("23 cases")
        expect(page.locator("#packet-estimate")).to_have_text("47 maximum packets before retries")
        expect(page.locator("#preset-description")).to_contain_text("execution")
        assert page.locator("#models").inner_text().count("Effort: high") == 3
        assert not processes
        assert not server.status()["history"]
        page.locator("#case-details").evaluate("element => element.open = true")
        assert page.locator(".case-row").count() == 35
        included = page.locator('.case-row select').filter(has=page.locator('option[value="trainset"]:checked')).first
        included.select_option("unused")
        expect(preset).to_have_value("custom")
        expect(page.locator("#training-count")).to_have_text("6")
        page.locator("#restore-cases").click()
        expect(page.locator("#training-count")).to_have_text("7")
        expect(preset).to_have_value("test")
        page.locator("#advanced-details").evaluate("element => element.open = true")
        page.locator("#field-runtime-judge-model").fill("controlled-custom-model")
        expect(page.locator("#models")).to_contain_text("controlled-custom-model")
        expect(preset).to_have_value("custom")
        preset.select_option("production")
        expect(page.locator("#total-cases")).to_have_text("35 cases")
        expect(page.locator("#packet-estimate")).to_have_text("992 maximum packets before retries")
        expect(page.locator("#field-runtime-judge-model")).to_have_value("claude-opus-5-5")
        assert "controlled-custom-model" not in page.locator("#models").inner_text()
        preset.select_option("production-short")
        expect(page.locator("#field-search-completion-max_iterations")).to_have_value("5")
        expect(page.locator("#field-trials-training")).to_have_value("2")
        expect(page.locator("#field-trials-validation")).to_have_value("2")
        expect(page.locator("#field-trials-final")).to_have_value("3")
        expect(page.locator("#total-cases")).to_have_text("35 cases")
        expect(page.locator("#packet-estimate")).to_have_text("460 maximum packets before retries")
        expect(page.locator("#preset-description")).to_contain_text("Fewer proposals")
        assert not processes
        assert not server.status()["history"]
        browser.close()


def test_test_preset_defaults_keep_the_full_catalog_and_fit_a_narrow_view(tmp_path):
    server, processes = make_server(tmp_path, preset="test")
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 560, "height": 900})
        page.goto(server.url)
        expect(page.locator("#start-button")).to_be_enabled()
        expect(page.locator("#preset-select")).to_have_value("test")
        expect(page.locator("#packet-estimate")).to_have_text("47 maximum packets before retries")
        expect(page.locator("#total-cases")).to_have_text("23 cases")
        expect(page.locator("#preset-description")).to_contain_text("directive improvement requires a full evaluation")
        page.locator("#case-details").evaluate("element => element.open = true")
        assert page.locator(".case-row").count() == 35
        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        assert not processes
        assert not server.status()["history"]
        page.locator("#preset-select").select_option("production")
        expect(page.locator("#packet-estimate")).to_have_text("992 maximum packets before retries")
        expect(page.locator("#total-cases")).to_have_text("35 cases")
        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        browser.close()


def test_preset_locks_during_start_and_matches_the_active_recorded_parameters(tmp_path):
    server, processes = make_server(tmp_path)
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 1000})
        page.goto(server.url)
        expect(page.locator("#start-button")).to_be_enabled()
        page.locator("#preset-select").select_option("test")
        page.evaluate("""() => {
            const original = window.fetch;
            window.fetch = async (...args) => {
                if (args[0] === 'validate.json') await new Promise(resolve => setTimeout(resolve, 700));
                return original(...args);
            };
            startExperiment(new Event('submit'));
        }""")
        expect(page.locator("#preset-select")).to_be_disabled()
        assert not processes
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        assert len(processes) == 1
        assert len(processes[0].protocol["datasets"]["trainset"]) == 7
        assert len(processes[0].protocol["datasets"]["valset"]) == 7
        assert len(processes[0].protocol["datasets"]["final"]) == 9
        page.reload()
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        page.locator("#configure-tab").click()
        expect(page.locator("#preset-select")).to_have_value("test")
        expect(page.locator("#preset-select")).to_be_disabled()
        expect(page.locator("#restore-cases")).to_be_disabled()
        expect(page.locator("#packet-estimate")).to_have_text("47 maximum packets before retries")
        processes[0].finished.set()
        expect(page.locator("#status-title")).to_have_text("The experiment completed.")
        page.locator("#new-experiment").click()
        expect(page.locator("#preset-select")).to_be_enabled()
        expect(page.locator("#preset-select")).to_have_value("test")
        assert len(processes) == 1
        browser.close()


def test_browser_fast_mode_presets_and_claude_configuration(tmp_path):
    server, processes = make_server(tmp_path)
    with server, sync_playwright() as driver:
        browser = driver.chromium.launch()
        page = browser.new_page()
        page.goto(server.url)
        expect(page.locator("#preset-select")).to_have_value("production")
        page.locator("#advanced-details").evaluate("element => element.open = true")
        for preset in ("production", "production-short", "test"):
            page.locator("#preset-select").select_option(preset)
            for role in ("packager", "judge", "optimizer"):
                expect(page.locator(f"#field-runtime-{role}-fast_mode")).to_be_checked()
                expect(page.locator(f"#field-runtime-{role}-fast_mode")).to_be_enabled()
            expect(page.locator("#models")).to_contain_text("Fast mode: On")
            page.locator("#advanced-fields > section").first.screenshot(path=str(tmp_path / f"{preset}-fast-mode.png"))
        page.locator("#field-runtime-packager-fast_mode").uncheck()
        expect(page.locator("#preset-select")).to_have_value("custom")
        page.locator("#field-runtime-packager-fast_mode").check()
        expect(page.locator("#preset-select")).to_have_value("test")
        page.locator("#field-runtime-packager-harness").select_option("claude")
        expect(page.locator("#field-runtime-packager-fast_mode")).to_be_checked()
        expect(page.locator("#field-runtime-packager-fast_mode")).to_be_enabled()
        expect(page.locator("#models")).to_contain_text("Fast mode: On")
        page.locator("#field-runtime-judge-fast_mode").uncheck()
        page.locator("#start-button").click()
        expect(page.locator("#status-title")).to_have_text("The experiment is active.")
        assert len(processes) == 1
        assert processes[0].protocol["runtime"]["judge"]["fast_mode"] is False
        assert processes[0].protocol["runtime"]["packager"]["fast_mode"] is True
        expect(page.locator("#field-runtime-judge-fast_mode")).to_be_disabled()
        processes[0].finished.set()
        browser.close()


def test_browser_resumes_the_same_experiment_and_follows_new_events(tmp_path):
    from test_control_server import FakeProcess, empty_docker, missing_group
    processes = []

    def factory(argv, **kwargs):
        process = FakeProcess(argv, **kwargs)
        processes.append(process)
        if '--resume' in argv:
            with RecordStore(process.output) as store:
                store.append('run_session_started', {'continued': True}, stage='preparation')
        return process

    server = ControlServer(SOURCE / 'evals/config/packager.json', SOURCE, None, tmp_path / 'cache',
                           tmp_path / 'runs', process_factory=factory, docker_runner=empty_docker, group_signal=missing_group)
    with server, sync_playwright() as driver:
        _, initial = server.start(server.defaults)
        process = processes[-1]
        process.freeze_records()
        process.complete('failed', 1)
        browser = driver.chromium.launch()
        page = browser.new_page(viewport={'width': 1440, 'height': 1000})
        errors = []
        page.on('pageerror', lambda error: errors.append(str(error)))
        page.goto(server.url)
        expect(page.locator('#resume-experiment')).to_be_visible()
        expect(page.locator('#resume-note')).to_contain_text('Reuse its completed trials')
        frame = page.frame_locator('#experiment-frame')
        with RecordStore(process.output, read_only=True) as store:
            experiment_id = store.experiment_id
        expect(frame.locator('#experiment')).to_have_text(experiment_id)
        page.locator('#resume-experiment').click()
        expect(page.locator('#state-badge')).to_have_text('Experiment active')
        expect(page.locator('#resume-experiment')).to_be_hidden()
        expect(frame.locator('#experiment')).to_have_text(experiment_id)
        assert len(processes) == 2
        assert '--resume' in processes[-1].argv
        assert processes[-1].output == process.output
        with RecordStore(process.output) as store:
            store.append('candidate_created', {'texts': {'packager_directive': 'A proposed edit.'},
                         'parent_ids': ['D0'], 'status': 'proposed'}, candidate_id='D1', stage='proposal')
        expect(frame.locator('#graph')).to_contain_text('D1', timeout=10000)
        assert server.status()['run_id'] == initial['run_id']
        assert not errors
        browser.close()
