import copy
import json
from pathlib import Path
import signal
import stat
import subprocess
from concurrent.futures import ThreadPoolExecutor
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import pytest

from culler_evals.control_server import BODY_DEPTH_LIMIT, BODY_LIMIT, ControlServer
from culler_evals.protocol import digest, effort_choices, read_protocol
from culler_evals.records import RecordStore

SOURCE = Path(__file__).resolve().parents[2]
CONFIG = SOURCE / "evals/config/packager.json"


class FakeProcess:
    pid = 123456

    def __init__(self, argv, **kwargs):
        self.argv = argv
        self.kwargs = kwargs
        self.code = None
        self.signals = []
        self.waits = []
        self.output = Path(argv[argv.index("--output") + 1])
        self.protocol = Path(argv[argv.index("--protocol") + 1])
        self.frozen = json.loads(self.protocol.read_text())

    def poll(self):
        return self.code

    def freeze_records(self):
        with RecordStore(self.output) as store:
            store.write_manifest("protocol", self.frozen)
            store.append("run_session_started", {"continued": False}, stage="preparation")
            store.append("candidate_created", {"status": "original", "parent_ids": [],
                         "texts": {"packager_directive": "The original directive."}}, candidate_id="D0")
            artifact = store.put_blob("The retained artifact.")
        return artifact

    def complete(self, outcome="success", code=0):
        with RecordStore(self.output) as store:
            store.append("run_session_completed", {"outcome": outcome}, stage="final")
        self.code = code

    def send_signal(self, value):
        self.signals.append(value)
        self.code = 130

    def wait(self, timeout):
        self.waits.append(timeout)
        return self.code


@pytest.fixture
def server(tmp_path):
    instances = []

    def factory(argv, **kwargs):
        process = FakeProcess(argv, **kwargs)
        instances.append(process)
        return process

    with ControlServer(CONFIG, SOURCE, tmp_path / "private-profile.json", tmp_path / "cache",
                       tmp_path / "runs", process_factory=factory, docker_runner=empty_docker, group_signal=missing_group) as control:
        control.test_processes = instances
        yield control


def missing_group(*_):
    raise ProcessLookupError()


def empty_docker(argv, **kwargs):
    return subprocess.CompletedProcess(argv, 0, stdout="")


def request(server, resource, body=None, *, headers=None, raw=None):
    if raw is None and body is not None:
        raw = json.dumps(body).encode()
    supplied = {"Origin": server.origin, "Content-Type": "application/json"} if raw is not None else {}
    supplied.update(headers or {})
    response = urlopen(Request(server.url + resource, data=raw, headers=supplied))
    data = response.read()
    return response, json.loads(data) if "application/json" in response.headers["Content-Type"] else data


def assert_http_status(server, resource, code, **kwargs):
    with pytest.raises(HTTPError) as error:
        request(server, resource, **kwargs)
    assert error.value.code == code
    return json.load(error.value)


def test_defaults_and_catalog_exclude_private_paths(server):
    response, config = request(server, "config.json")
    assert response.status == 200
    assert len(config["cases"]) == 35
    assert config["fixed"]["model_names"] == {settings["model"]: settings.get("model_name", settings["model"])
                                               for settings in server._base["runtime"].values()}
    assert {case["default_set"] for case in config["cases"]} == {"trainset", "valset", "final"}
    assert len(config["defaults"]["datasets"]["trainset"]) == 13
    assert len(config["defaults"]["datasets"]["valset"]) == 13
    assert len(config["defaults"]["datasets"]["final"]) == 9
    assert {field["type"] for field in config["schema"]} == {"string", "number", "integer", "boolean"}
    for role in ("packager", "judge", "optimizer"):
        assert config["defaults"]["runtime"][role]["effort"] == "high"
        effort = next(field for field in config["schema"] if field["path"] == f"runtime.{role}.effort")
        assert effort["harness_path"] == f"runtime.{role}.harness"
        assert effort["enum_by_harness"] == {name: list(effort_choices(name)) for name in ("codex", "claude")}
    assert config["storage"]["experiments"] == str(server.output_root)
    text = json.dumps({key: value for key, value in config.items() if key != "storage"})
    for private in (server.credentials, server.cache, server.output_root, SOURCE):
        assert str(private) not in text
    _, status = request(server, "status.json")
    assert status["state"] == "ready"
    assert status["parameters"] is None
    assert status["history"] == []


def test_default_validation_freezes_paths_and_predicts_maximum(server):
    _, result = request(server, "validate.json", {"parameters": server.defaults})
    assert result["valid"]
    assert result["summary"] == {"case_counts": {"trainset": 13, "valset": 13, "final": 9},
                                 "maximum_packets_before_retries": 992, "maximum_proposals": 24}
    assert not server.output_root.exists()
    assert not server.test_processes
    increased = copy.deepcopy(server.defaults)
    increased["trials"]["training"] = 3
    variation = server.validate(increased)
    assert variation["valid"]
    assert variation["summary"]["maximum_packets_before_retries"] == 1136
    increased["trials"]["training"] = 2
    increased["search"]["confirmation"]["enabled"] = True
    confirmed = server.validate(increased)
    assert confirmed["valid"]
    assert confirmed["summary"]["maximum_packets_before_retries"] == 1616
    assert not server.test_processes


def test_saved_presets_preserve_final_cases_models_and_decision_rules(server):
    config = server.configuration()
    presets = {record["id"]: record for record in config["presets"]}
    assert config["default_preset"] == "production"
    production = presets["production"]["parameters"]
    test = presets["test"]["parameters"]
    short = presets["production-short"]["parameters"]
    expected_short = copy.deepcopy(production)
    expected_short["search"]["completion"]["max_iterations"] = 5
    assert short == expected_short
    assert production == config["defaults"]
    assert test["runtime"] == production["runtime"]
    assert test["final_decision"] == production["final_decision"]
    assert test["retry"] == production["retry"]
    assert test["datasets"]["final"] == production["datasets"]["final"]
    assert {name: len(items) for name, items in test["datasets"].items()} == {
        "trainset": 7, "valset": 7, "final": 9}
    for name, identifiers in test["datasets"].items():
        assert set(identifiers) <= set(production["datasets"][name])
        assert {kind for identifier in identifiers for kind in server._cases[identifier]["types"]} == {
            kind for case in server._cases.values() for kind in case["types"]}
    for preset, maximum in ((production, 992), (short, 460), (test, 47)):
        result = server.validate(preset)
        assert result["valid"]
        assert result["summary"]["maximum_packets_before_retries"] == maximum
    assert not server.test_processes
    assert not server.output_root.exists()


@pytest.mark.parametrize("filename", ["packager.json", "packager.production-short.json", "packager.test.json"])
def test_preset_selection_retains_catalog_and_freezes_actual_parameters(tmp_path, filename):
    with ControlServer(CONFIG.parent / filename, SOURCE, None, tmp_path / "cache", tmp_path / "runs", preset="test",
                       process_factory=FakeProcess, docker_runner=empty_docker, group_signal=missing_group) as control:
        assert control.configuration()["default_preset"] == "test"
        assert len(control.configuration()["cases"]) == 35
        parameters = copy.deepcopy(control.defaults)
        result, frozen = control._validation(parameters)
        assert result["valid"]
        assert frozen["preset"] == {"id": "test", "label": "Test", "purpose": "execution_check",
                                    "parameters_hash": digest(parameters)}
        assert frozen["trials"]["training"] == 1
        assert frozen["final_decision"]["minimum_groups"] == 9
        parameters["search"]["completion"]["max_iterations"] = 3
        result, changed = control._validation(parameters)
        assert result["valid"]
        assert changed["preset"]["id"] == "custom"
        assert changed["preset"]["parameters_hash"] == digest(parameters)
        assert changed["protocol_hash"] != frozen["protocol_hash"]
        assert not control.output_root.exists()


def test_short_production_freezes_its_preset_and_preserves_evidence(tmp_path):
    with ControlServer(CONFIG, SOURCE, None, tmp_path / "cache", tmp_path / "runs", preset="production-short",
                       process_factory=FakeProcess, docker_runner=empty_docker, group_signal=missing_group) as control:
        parameters = copy.deepcopy(control.defaults)
        result, frozen = control._validation(parameters)
        assert result["valid"]
        assert result["summary"]["maximum_packets_before_retries"] == 460
        assert result["summary"]["maximum_proposals"] == 10
        assert frozen["preset"] == {
            "id": "production-short", "label": "Production — 5 iterations", "purpose": "directive_evaluation",
            "parameters_hash": digest(parameters)}
        assert frozen["search"]["completion"]["max_iterations"] == 5
        assert frozen["trials"]["training"] == 2
        assert frozen["trials"]["validation"] == 2
        assert frozen["trials"]["final"] == 3
        assert {name: len(items) for name, items in frozen["datasets"].items()} == {
            "trainset": 13, "valset": 13, "final": 9}
        assert not control.output_root.exists()


def test_unknown_preset_cannot_open_a_launch_server(tmp_path):
    with pytest.raises(ValueError, match="Select a saved preset: production, production-short, test"):
        ControlServer(CONFIG, SOURCE, None, tmp_path / "cache", tmp_path / "runs", preset="missing")
    assert not (tmp_path / "runs").exists()


def test_unknown_preset_without_a_catalog_names_the_required_action(tmp_path):
    protocol = json.loads(CONFIG.read_text())
    protocol.pop("presets_file")
    protocol["rubric_file"] = str(CONFIG.parent / protocol["rubric_file"])
    protocol["datasets"] = {partition: [str((CONFIG.parent / path).resolve()) for path in items]
                            for partition, items in protocol["datasets"].items()}
    path = tmp_path / "protocol.json"
    path.write_text(json.dumps(protocol))
    with pytest.raises(ValueError, match="Remove --preset"):
        ControlServer(path, SOURCE, None, tmp_path / "cache", tmp_path / "runs", preset="test")
    assert not (tmp_path / "runs").exists()


@pytest.mark.parametrize("field,value", [
    ("trials.training", True), ("trials.validation", 0), ("search.seed", -1),
    ("search.completion.minimum_gain", float("nan")), ("final_decision.minimum_gain", float("inf")),
    ("final_decision.minimum_gain", 10 ** 400), ("search.sampler", "random"),
    ("runtime.packager.harness", "shell"), ("runtime.packager.model", "bad\nmodel"),
    ("runtime.packager.effort", None), ("runtime.judge.effort", "ultra"),
    ("runtime.optimizer.effort", "automatic"), ("runtime.optimizer.effort", True),
])
def test_invalid_types_and_nonfinite_values_never_launch(server, field, value):
    parameters = copy.deepcopy(server.defaults)
    keys = field.split(".")
    selected = parameters
    for key in keys[:-1]:
        selected = selected[key]
    selected[keys[-1]] = value
    result = server.validate(parameters)
    assert not result["valid"]
    assert any(error["field"] == field for error in result["errors"])
    status, _ = server.start(parameters)
    assert status == 422
    assert not server.test_processes


def test_effort_follows_harness_and_freezes_the_accepted_value(server):
    parameters = copy.deepcopy(server.defaults)
    parameters["runtime"]["packager"]["effort"] = "ultra"
    assert server.validate(parameters)["valid"]
    parameters["runtime"]["packager"]["harness"] = "claude"
    parameters["runtime"]["packager"]["fast_mode"] = False
    invalid = server.validate(parameters)
    assert not invalid["valid"]
    assert any(error["field"] == "runtime.packager.effort" and "selected harness" in error["message"]
               for error in invalid["errors"])
    assert server.start(parameters)[0] == 422
    assert not server.test_processes
    parameters["runtime"]["packager"]["effort"] = "high"
    parameters["runtime"]["judge"]["effort"] = "medium"
    assert server.start(parameters)[0] == 202
    frozen = server.test_processes[0].frozen
    assert frozen["runtime"]["packager"]["effort"] == "high"
    assert frozen["runtime"]["judge"]["effort"] == "medium"
    assert frozen["runtime"]["optimizer"]["effort"] == "high"


def test_missing_effort_blocks_launch_without_supplying_a_default(server):
    parameters = copy.deepcopy(server.defaults)
    parameters["runtime"]["judge"].pop("effort")
    result = server.validate(parameters)
    assert not result["valid"]
    assert any(error["field"] == "runtime.judge.effort" for error in result["errors"])
    assert server.start(parameters)[0] == 422
    assert "effort" not in parameters["runtime"]["judge"]
    assert not server.test_processes


def test_unknown_harness_gives_an_action_before_effort_selection(server):
    parameters = copy.deepcopy(server.defaults)
    parameters["runtime"]["packager"]["harness"] = "unknown"
    result = server.validate(parameters)
    assert not result["valid"]
    assert {"field": "runtime.packager.effort", "message": "Select a supported harness before you set effort."} in result["errors"]
    assert not server.test_processes


@pytest.mark.parametrize("field", ["credentials", "source_revision", "rubric", "protected", "runtime.packager.base_url"])
def test_fixed_fields_cannot_enter_parameters(server, field):
    parameters = copy.deepcopy(server.defaults)
    keys = field.split(".")
    selected = parameters
    for key in keys[:-1]:
        selected = selected[key]
    selected[keys[-1]] = "unauthorized"
    result = server.validate(parameters)
    assert not result["valid"]
    assert any(error["field"] == field for error in result["errors"])
    assert not server.test_processes


def test_case_selection_preserves_sets_and_validates_independent_groups(server):
    parameters = copy.deepcopy(server.defaults)
    training = parameters["datasets"]["trainset"]
    parameters["datasets"]["final"].append(training.pop())
    result = server.validate(parameters)
    assert not result["valid"]
    assert any("configured set" in error["message"] for error in result["errors"])
    parameters = copy.deepcopy(server.defaults)
    parameters["datasets"]["trainset"].append(parameters["datasets"]["trainset"][0])
    result = server.validate(parameters)
    assert not result["valid"]
    assert any("more than once" in error["message"] for error in result["errors"])


def test_selecting_smaller_training_subset_remains_valid(server):
    parameters = copy.deepcopy(server.defaults)
    parameters["datasets"]["trainset"] = parameters["datasets"]["trainset"][:7]
    assert server.validate(parameters)["valid"]
    parameters["search"]["minibatch_size"] = 8
    result = server.validate(parameters)
    assert not result["valid"]
    assert any("minibatch size" in error["message"] for error in result["errors"])


def test_completion_can_use_stagnation_without_an_iteration_limit(server):
    parameters = copy.deepcopy(server.defaults)
    parameters["search"]["completion"]["max_iterations"] = None
    result = server.validate(parameters)
    assert result["valid"]
    assert result["summary"]["maximum_packets_before_retries"] is None
    assert result["summary"]["maximum_proposals"] is None
    parameters["search"]["completion"]["stagnation_iterations"] = None
    assert not server.validate(parameters)["valid"]


def test_optional_confirmation_uses_fixed_reference_and_counter_policy(server):
    parameters = copy.deepcopy(server.defaults)
    parameters["search"]["confirmation"]["enabled"] = True
    code, status = server.start(parameters)
    assert code == 202
    assert status["state"] == "starting"
    frozen = server.test_processes[0].frozen
    assert frozen["search"]["confirmation"] == {
        "enabled": True, "reference": "original", "counter_policy": "reset_on_confirmed_gain"}


def test_start_serializes_requests_and_freezes_inputs(server):
    parameters = copy.deepcopy(server.defaults)
    parameters["runtime"]["packager"]["model"] = "gpt-6.1-sol-test"
    parameters["search"]["completion"]["max_iterations"] = 2
    with ThreadPoolExecutor(2) as pool:
        results = list(pool.map(lambda _: server.start(parameters), range(2)))
    assert sorted(code for code, _ in results) == [202, 409]
    assert len(server.test_processes) == 1
    process = server.test_processes[0]
    frozen = process.frozen
    assert frozen["source_revision"] != "checkout"
    assert frozen["runtime"]["packager"]["model_name"] == "gpt-6.1-sol-test"
    assert frozen["framework_source_hashes"]
    assert frozen["runtime_source_manifest"]
    assert frozen["product_source_manifest"]
    assert frozen["case_hashes"]
    assert frozen["seed_candidate"]["packager_directive"]
    assert "rubric_file" not in frozen
    assert all(Path(path).is_absolute() for paths in frozen["datasets"].values() for path in paths)
    assert read_protocol(process.protocol)["rubric"] == frozen["rubric"]
    assert stat.S_IMODE(process.protocol.stat().st_mode) == 0o600
    assert stat.S_IMODE(process.protocol.parent.stat().st_mode) == 0o700
    assert stat.S_IMODE((process.protocol.parent / "session.log").stat().st_mode) == 0o600
    assert process.kwargs["cwd"] == SOURCE / "evals"
    assert process.kwargs["stdin"] == subprocess.DEVNULL
    assert process.kwargs["start_new_session"] is True
    assert "shell" not in process.kwargs
    assert "--no-browser" in process.argv and "--no-wait" in process.argv
    parameters["search"]["completion"]["max_iterations"] = 100
    assert server.status()["parameters"]["search"]["completion"]["max_iterations"] == 2
    assert json.loads(process.protocol.read_text())["search"]["completion"]["max_iterations"] == 2


def test_tree_and_artifacts_follow_durable_records_and_completed_run(server):
    _, status = server.start(server.defaults)
    assert status["state"] == "starting"
    process = server.test_processes[0]
    assert_http_status(server, "experiment/records.json", 503)
    artifact = process.freeze_records()
    _, status = request(server, "status.json")
    assert status["state"] == "running"
    assert status["records_ready"] is True
    _, records = request(server, "experiment/records.json")
    assert records["live"] is True
    assert len(records["events"]) == 2
    _, content = request(server, "experiment/" + artifact["path"])
    assert content == b"The retained artifact."
    _, page = request(server, "experiment/")
    assert b"Candidate ancestry" in page
    for route in ("experiment/../events.jsonl", "experiment/blobs/not-a-hash", "runs/unknown/records.json"):
        assert_http_status(server, route, 404)
    process.complete()
    completed = server.status()
    assert completed["state"] == "complete"
    first_url = completed["experiment_url"]
    first_output = process.output
    _, next_status = server.start(server.defaults)
    assert next_status["run_id"] != completed["run_id"]
    assert server.test_processes[1].output != first_output
    assert len(next_status["history"]) == 2
    assert len(json.load(urlopen(first_url + "records.json"))["events"]) == 3
    assert first_output.exists()


def test_exit_zero_without_success_record_is_failed(server):
    server.start(server.defaults)
    server.test_processes[0].code = 0
    result = server.status()
    assert result["state"] == "failed"
    assert result["records_ready"] is False
    assert result["exit_code"] == 0


def test_failed_journal_never_reports_complete(server):
    server.start(server.defaults)
    process = server.test_processes[0]
    process.freeze_records()
    process.complete(outcome="failed", code=0)
    assert server.status()["state"] == "failed"


def test_spawn_failure_preserves_private_launch_record_and_public_failure(tmp_path):
    secret_path = tmp_path / "credential-private-value.json"

    def fail(*_, **__):
        raise OSError(f"Cannot use private credentials: {secret_path}")

    with ControlServer(CONFIG, SOURCE, secret_path, tmp_path / "cache", tmp_path / "runs", process_factory=fail, docker_runner=empty_docker, group_signal=missing_group) as server:
        code, result = server.start(server.defaults)
        assert code == 503
        assert result["state"] == "failed"
        assert str(secret_path) not in json.dumps(result)
        launches = list(server.output_root.glob(".control/*/launch.json"))
        assert len(launches) == 1
        assert json.loads(launches[0].read_text())["state"] == "failed"
        assert not list(server.output_root.glob("*/events.jsonl"))
        assert b"credential-private-value" not in (launches[0].parent / "session.log").read_bytes()


def test_source_mismatch_blocks_start_before_launch(server, monkeypatch):
    import culler_evals.control_server as module
    real_freeze = module.freeze_protocol

    def different_source(protocol, source):
        protocol["source_revision"] = "0" * 40
        return real_freeze(protocol, source)

    monkeypatch.setattr(module, "freeze_protocol", different_source)
    result = server.validate(server.defaults)
    assert not result["valid"]
    assert "source revision" in result["errors"][0]["message"]
    assert server.start(server.defaults)[0] == 422
    assert not server.test_processes


def test_remote_hosts_origins_and_unknown_tokens_cannot_mutate(server):
    for headers in ({"Host": "evil.example"}, {"Host": "localhost"}):
        assert_http_status(server, "config.json", 404, headers=headers)
    for origin in ("http://evil.example", "null", "http://127.0.0.1:1", ""):
        assert_http_status(server, "start.json", 403, body={"parameters": server.defaults}, headers={"Origin": origin})
    with pytest.raises(HTTPError) as error:
        urlopen(server.origin + "/status.json")
    assert error.value.code == 404
    assert_http_status(server, "unknown.json", 404, body={"parameters": server.defaults})
    assert not server.test_processes


def test_request_shape_size_and_content_type_limits(server):
    assert_http_status(server, "start.json", 400, body={"parameters": server.defaults, "command": "sh"})
    assert_http_status(server, "start.json", 400, raw=b"[]")
    assert_http_status(server, "start.json", 400, raw=b"not JSON")
    assert_http_status(server, "start.json", 400, body={"parameters": server.defaults}, headers={"Content-Type": "text/plain"})
    assert_http_status(server, "start.json", 413, raw=b"x" * (BODY_LIMIT + 1))
    assert_http_status(server, "start.json", 400, raw=b'{"parameters":NaN}')
    for opening, closing in ((b'{"x":', b'}'), (b'[', b']')):
        for depth in (BODY_DEPTH_LIMIT - 1, BODY_DEPTH_LIMIT, 1200):
            body = b'{"parameters":' + opening * depth + b'1' + closing * depth + b'}'
            status = 422 if depth < BODY_DEPTH_LIMIT else 400
            assert_http_status(server, "start.json", status, raw=body)
    assert not server.test_processes


def test_shutdown_interrupts_child_and_preserves_records(tmp_path):
    processes = []

    def factory(argv, **kwargs):
        process = FakeProcess(argv, **kwargs)
        processes.append(process)
        return process

    with ControlServer(CONFIG, SOURCE, None, tmp_path / "cache", tmp_path / "runs", process_factory=factory, docker_runner=empty_docker, group_signal=missing_group) as server:
        server.start(server.defaults)
        processes[0].freeze_records()
        root = processes[0].output
    assert processes[0].signals == [signal.SIGINT]
    assert processes[0].waits == [1]
    assert (processes[0].protocol.parent / "stop").is_file()
    assert (root / "events.jsonl").exists()
    assert server.status()["state"] == "interrupted"


def test_launch_receipt_precedes_process_and_storage_failure_blocks_launch(server, monkeypatch):
    observed = []
    factory = server._process_factory

    def inspect(argv, **kwargs):
        config = Path(argv[argv.index("--protocol") + 1])
        launch = json.loads(config.with_name("launch.json").read_text())
        observed.append(launch["state"])
        return factory(argv, **kwargs)

    server._process_factory = inspect
    server.start(server.defaults)
    assert observed == ["starting"]


def test_initial_receipt_failure_never_starts_child(server, monkeypatch):
    original = server._private_write

    def fail(path, data):
        if path.name == "launch.json":
            raise OSError("The local status file is unavailable.")
        return original(path, data)

    monkeypatch.setattr(server, "_private_write", fail)
    assert_http_status(server, "start.json", 503, body={"parameters": server.defaults})
    assert not server.test_processes


def test_postlaunch_receipt_failure_interrupts_child(server, monkeypatch):
    original = server._private_write
    calls = 0

    def fail(path, data):
        nonlocal calls
        if path.name == "launch.json":
            calls += 1
            if calls == 2:
                raise OSError("The local status file is unavailable.")
        return original(path, data)

    monkeypatch.setattr(server, "_private_write", fail)
    assert_http_status(server, "start.json", 503, body={"parameters": server.defaults})
    assert len(server.test_processes) == 1
    assert server.test_processes[0].signals == [signal.SIGINT]
    assert server.test_processes[0].poll() == 130
    assert server.status()["state"] == "interrupted"


def test_status_storage_failure_returns_truthful_error(server, monkeypatch):
    server.start(server.defaults)
    server.test_processes[0].freeze_records()

    def fail(*_):
        raise OSError("Do not expose private paths.")

    monkeypatch.setattr(server, "_private_write", fail)
    result = assert_http_status(server, "status.json", 503)
    assert "status is unavailable" in result["error"]
    monkeypatch.undo()


def test_shutdown_timeout_reports_active_process(server, monkeypatch):
    class SlowProcess:
        pid = 123456
        signals = []
        def send_signal(self, value):
            self.signals.append(value)
        def poll(self):
            return None
    process = SlowProcess()
    times = iter([0, 31])
    monkeypatch.setattr("culler_evals.control_server.time.monotonic", lambda: next(times))
    with pytest.raises(RuntimeError, match="process remains active"):
        server._interrupt_run({"process": process, "config": server.output_root.parent / "protocol.json", "run_id": "fake-run"})
    assert process.signals == [signal.SIGINT]


def test_child_receives_private_cancellation_markers(server):
    server.start(server.defaults)
    process = server.test_processes[0]
    env = process.kwargs["env"]
    assert env["CULLER_EVAL_CONTROL_RUN"] == server.status()["run_id"]
    assert env["CULLER_EVAL_CONTROL_CANCEL_FILE"] == str(process.protocol.with_name("stop"))
    assert "CULLER_EVAL_CONTROL_CANCEL_FILE" not in json.dumps(server.status())


def test_cleanup_checks_exact_label_before_removing_only_owned_containers(server):
    server.start(server.defaults)
    run = server._runs[server._current]
    identifier = "a" * 64
    commands = []

    def docker(argv, **kwargs):
        commands.append(argv)
        if argv[1] == "ps":
            return subprocess.CompletedProcess(argv, 0, stdout=identifier + "\n")
        if argv[1] == "inspect":
            return subprocess.CompletedProcess(argv, 0, stdout=json.dumps({"org.culler.evals.control-run": run["run_id"]}))
        return subprocess.CompletedProcess(argv, 0, stdout="")

    server._docker_runner = docker
    server._remove_control_containers(run)
    assert commands[0][-1] == "label=org.culler.evals.control-run=" + run["run_id"]
    assert commands[-1] == ["docker", "rm", "--force", identifier]
    server._docker_runner = empty_docker


def test_cleanup_refuses_an_unrelated_container_or_invalid_identifier(server):
    server.start(server.defaults)
    run = server._runs[server._current]
    commands = []

    def docker(argv, **kwargs):
        commands.append(argv)
        if argv[1] == "ps":
            return subprocess.CompletedProcess(argv, 0, stdout="b" * 64)
        return subprocess.CompletedProcess(argv, 0, stdout=json.dumps({"org.culler.evals.control-run": "different-run"}))

    server._docker_runner = docker
    with pytest.raises(RuntimeError, match="label differs"):
        server._remove_control_containers(run)
    assert not any(argv[1] == "rm" for argv in commands)
    server._docker_runner = lambda argv, **kwargs: subprocess.CompletedProcess(argv, 0, stdout="--all")
    with pytest.raises(RuntimeError, match="invalid identifier"):
        server._remove_control_containers(run)
    server._docker_runner = empty_docker


def test_group_signal_covers_child_process_group(server):
    server.start(server.defaults)
    process = server.test_processes[0]
    observed = []

    def group(pid, value):
        observed.append((pid, value))
        process.send_signal(value)

    server._group_signal = group
    server._interrupt_run(server._runs[server._current])
    assert observed == [(process.pid, signal.SIGINT)]
    assert process.poll() == 130


def test_same_revision_directive_drift_blocks_start(server, monkeypatch):
    import culler_evals.control_server as module
    original = module.original_directive

    def changed(source):
        text, identity = original(source)
        return text + "\nChanged at the same commit.", identity | {"merged_hash": "changed"}

    monkeypatch.setattr(module, "original_directive", changed)
    result = server.validate(server.defaults)
    assert not result["valid"]
    assert "directive or writing skill changed" in result["errors"][0]["message"]
    assert server.start(server.defaults)[0] == 422
    assert not server.test_processes


def test_final_cleanup_catches_container_after_child_exit(server):
    server.start(server.defaults)
    run = server._runs[server._current]
    identifier = "c" * 64
    commands = []
    lists = 0

    def docker(argv, **kwargs):
        nonlocal lists
        commands.append(argv)
        if argv[1] == "ps":
            lists += 1
            return subprocess.CompletedProcess(argv, 0, stdout="" if lists == 1 else identifier)
        if argv[1] == "inspect":
            return subprocess.CompletedProcess(argv, 0, stdout=json.dumps({"org.culler.evals.control-run": run["run_id"]}))
        return subprocess.CompletedProcess(argv, 0, stdout="")

    server._docker_runner = docker
    server._interrupt_run(run)
    assert lists == 2
    assert commands[-1] == ["docker", "rm", "--force", identifier]
    server._docker_runner = empty_docker


def test_inspection_failure_requires_confirmed_container_absence(server):
    server.start(server.defaults)
    run = server._runs[server._current]
    identifier = "d" * 64
    commands = []

    def docker(argv, **kwargs):
        commands.append(argv)
        if argv[1] == "inspect":
            return subprocess.CompletedProcess(argv, 1, stdout="")
        return subprocess.CompletedProcess(argv, 0, stdout=identifier)

    server._docker_runner = docker
    with pytest.raises(RuntimeError, match="identity is unavailable"):
        server._remove_control_containers(run)
    assert not any(argv[1] == "rm" for argv in commands)
    server._docker_runner = empty_docker


def test_claude_fast_mode_freezes_the_selected_value(server):
    parameters = copy.deepcopy(server.defaults)
    assert parameters["runtime"]["judge"]["fast_mode"] is True
    parameters["runtime"]["judge"]["fast_mode"] = False
    assert server.validate(parameters)["valid"]
    assert server.start(parameters)[0] == 202
    assert server.test_processes[0].frozen["runtime"]["judge"]["fast_mode"] is False
    assert server.test_processes[0].frozen["runtime"]["judge"]["effort"] == "high"


def test_browser_resume_keeps_the_experiment_and_appends_its_log(server):
    _, first = request(server, 'start.json', {'parameters': server.defaults})
    process = server.test_processes[-1]
    process.freeze_records()
    process.complete('failed', 1)
    log = process.protocol.with_name('session.log')
    log.write_text('The original failure.\n')
    _, stopped = request(server, 'status.json')
    assert stopped['resume']['available']
    assert stopped['resume']['update_runtime'] is False
    response, resumed = request(server, 'resume.json', {'run_id': first['run_id'], 'update_runtime': False})
    assert response.status == 202
    assert resumed['run_id'] == first['run_id']
    assert resumed['experiment_url'] == first['experiment_url']
    assert resumed['parameters'] == first['parameters']
    assert '--resume' in server.test_processes[-1].argv
    assert log.read_text() == 'The original failure.\n'
    assert len(resumed['history']) == 1
    assert_http_status(server, 'resume.json', 409, body={'run_id': first['run_id'], 'update_runtime': False})


def test_browser_resume_offers_and_applies_a_score_preserving_repair(server, monkeypatch):
    import culler_evals.control_server as module
    _, first = server.start(server.defaults)
    process = server.test_processes[-1]
    process.freeze_records()
    process.complete('failed', 1)
    with RecordStore(process.output) as store:
        store.write_manifest('resume_update', {'applied': True})
    monkeypatch.setattr(module, 'repair_preview', lambda *_: {'files': ['trial_resources.py'], 'assessments_to_repeat': 0})
    def repair(output, source):
        assert output == process.output
        assert source == SOURCE
        return process.frozen, SOURCE
    monkeypatch.setattr(module, 'prepare_update', repair)
    stopped = server.status()
    assert stopped['resume']['update_runtime'] is True
    assert stopped['resume']['assessments_to_repeat'] == 0
    assert 'reuses completed Judge scores' in stopped['resume']['note']
    response, resumed = request(server, 'resume.json', {'run_id': first['run_id'], 'update_runtime': True})
    assert response.status == 202
    assert resumed['run_id'] == first['run_id']
    assert resumed['parameters'] == first['parameters']
    assert '--resume' in server.test_processes[-1].argv


def test_server_restores_stopped_experiments_after_restart(server):
    _, first = request(server, 'start.json', {'parameters': server.defaults})
    process = server.test_processes[-1]
    process.freeze_records()
    process.complete('failed', 1)
    request(server, 'status.json')
    with ControlServer(CONFIG, SOURCE, server.credentials, server.cache, server.output_root,
                       process_factory=FakeProcess, docker_runner=empty_docker, group_signal=missing_group) as restored:
        _, status = request(restored, 'status.json')
        assert status['run_id'] == first['run_id']
        assert status['state'] == 'failed'
        assert status['resume']['available']
        assert status['experiment_url'].startswith(restored.url)
        _, payload = request(restored, 'runs/' + first['run_id'] + '/records.json')
        assert any(event['kind'] == 'candidate_created' for event in payload['events'])
        response, resumed = request(restored, 'resume.json', {'run_id': first['run_id'], 'update_runtime': False})
        assert response.status == 202
        assert resumed['run_id'] == first['run_id']


def test_history_selection_does_not_start_or_delete_an_experiment(server):
    identifiers = []
    for _ in range(2):
        _, status = request(server, 'start.json', {'parameters': server.defaults})
        identifiers.append(status['run_id'])
        process = server.test_processes[-1]
        process.freeze_records()
        process.complete('failed', 1)
    _, selected = request(server, 'select.json', {'run_id': identifiers[0]})
    assert selected['run_id'] == identifiers[0]
    assert len(selected['history']) == 2
    assert len(server.test_processes) == 2
    assert all((server.output_root / identifier).is_dir() for identifier in identifiers)
    assert_http_status(server, 'select.json', 404, body={'run_id': '../private'})


def test_restored_server_observes_external_writer_without_resuming(server):
    _, first = request(server, 'start.json', {'parameters': server.defaults})
    process = server.test_processes[-1]
    process.freeze_records()
    with RecordStore(process.output):
        with ControlServer(CONFIG, SOURCE, server.credentials, server.cache, server.output_root,
                           process_factory=lambda *_args, **_kwargs: pytest.fail('An experiment already runs.'),
                           docker_runner=empty_docker, group_signal=missing_group) as restored:
            _, status = request(restored, 'status.json')
            assert status['state'] == 'running'
            assert not status['resume']['available']
            assert_http_status(restored, 'resume.json', 409, body={'run_id': first['run_id'], 'update_runtime': False})


def test_resume_preserves_origin_and_request_shape_controls(server):
    assert_http_status(server, 'resume.json', 403, body={'run_id': 'absent', 'update_runtime': False}, headers={'Origin': 'http://example.com'})
    assert_http_status(server, 'resume.json', 400, body={'run_id': 'absent'})
    assert_http_status(server, 'resume.json', 400, body={'run_id': '../private', 'update_runtime': False})


def test_resume_receipt_failure_stops_a_spawned_child(server, monkeypatch):
    _, initial = server.start(server.defaults)
    process = server.test_processes[-1]
    process.freeze_records()
    process.complete('failed', 1)
    server.status()
    original = server._write_launch_record

    def reject_receipt(run):
        if run['process'] is not None and run['process'] is not process:
            raise OSError('The launch receipt failed.')
        return original(run)

    monkeypatch.setattr(server, '_write_launch_record', reject_receipt)
    assert_http_status(server, 'resume.json', 503, body={'run_id': initial['run_id'], 'update_runtime': False})
    assert len(server.test_processes) == 2
    assert server.test_processes[-1].signals == [signal.SIGINT]
    monkeypatch.setattr(server, '_write_launch_record', original)


def test_resume_receipt_failure_before_spawn_preserves_the_experiment(server, monkeypatch):
    _, initial = server.start(server.defaults)
    process = server.test_processes[-1]
    process.freeze_records()
    process.complete('failed', 1)
    server.status()
    original = server._write_launch_record

    def reject_receipt(run):
        if run['state'] == 'starting':
            raise OSError('The launch receipt failed.')
        return original(run)

    monkeypatch.setattr(server, '_write_launch_record', reject_receipt)
    assert_http_status(server, 'resume.json', 503, body={'run_id': initial['run_id'], 'update_runtime': False})
    assert len(server.test_processes) == 1
    assert process.output.is_dir()
    monkeypatch.setattr(server, '_write_launch_record', original)


def test_browser_recognizes_recovered_records_without_private_control_receipts(server):
    from culler_evals.storage import restore_records
    import shutil
    _, first = request(server, "start.json", {"parameters": server.defaults})
    process = server.test_processes[-1]
    process.freeze_records()
    process.complete("failed", 1)
    server.status()
    receipt = json.loads((server.output_root / ".control" / first["run_id"] / "launch.json").read_bytes())
    with RecordStore(process.output) as store:
        backup = store.enable_backup()
        store.write_manifest("browser_launch", receipt)
    shutil.rmtree(process.output)
    shutil.rmtree(server.output_root / ".control")
    restore_records(backup)
    processes = []
    def factory(*args, **kwargs):
        process = FakeProcess(*args, **kwargs)
        processes.append(process)
        return process
    with ControlServer(CONFIG, SOURCE, server.credentials, server.cache, server.output_root,
                       process_factory=factory, docker_runner=empty_docker, group_signal=missing_group) as restored:
        _, status = request(restored, "status.json")
        assert status["run_id"] == first["run_id"]
        assert status["state"] == "failed"
        assert status["resume"]["available"]
        assert processes == []
        response, resumed = request(restored, "resume.json", {"run_id": first["run_id"], "update_runtime": False})
        assert response.status == 202
        assert resumed["run_id"] == first["run_id"]
        assert "--resume" in processes[0].argv
