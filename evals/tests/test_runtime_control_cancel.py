from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import subprocess
import threading

import pytest

from proxy_fixture import proxy_credentials

from culler_evals.product_sources import product_source_manifest
from culler_evals.runtime import Runtime, RuntimeErrorWithEvidence
from culler_evals.runtime_sources import runtime_source_manifest

SOURCE = Path(__file__).resolve().parents[2]


def controlled_runtime(tmp_path, monkeypatch, *, executor=None):
    stop = tmp_path / "stop"
    monkeypatch.setenv("CULLER_EVAL_CONTROL_RUN", "20261002-test-run")
    monkeypatch.setenv("CULLER_EVAL_CONTROL_CANCEL_FILE", str(stop))
    runtime = Runtime(SOURCE, executor=executor, contracts=object(),
                      source_manifest=runtime_source_manifest(SOURCE),
                      product_manifest=product_source_manifest(SOURCE),
                      snapshot_root=tmp_path / "snapshot")
    return runtime, stop


@pytest.mark.parametrize("role", ["packager", "judge", "optimizer"])
def test_stop_prevents_each_role_before_workspace_or_model_access(tmp_path, monkeypatch, role):
    runtime, stop = controlled_runtime(tmp_path, monkeypatch,
                                       executor=lambda *_: pytest.fail("A stopped run must invoke no model."))
    stop.write_text("Stop this experiment.")
    with runtime, pytest.raises(KeyboardInterrupt, match="requested experiment interruption"):
        if role == "packager":
            runtime.execute_packager({}, "Directive", tmp_path / "trial", {})
        elif role == "judge":
            runtime.assess_packet({}, "Packet", tmp_path / "trial", {}, {})
        else:
            runtime.propose("Directive", {}, tmp_path / "trial", {}, {})
    assert not (tmp_path / "trial").exists()


def test_parallel_container_failures_after_stop_prevent_retries(tmp_path, monkeypatch):
    runtime, stop = controlled_runtime(tmp_path, monkeypatch)
    arrived = threading.Barrier(3)
    release = threading.Event()
    calls = []
    lock = threading.Lock()

    def execute(argv, **fields):
        with lock:
            calls.append(argv)
        if argv[1] == "run":
            assert "org.culler.evals.control-run=20261002-test-run" in argv
            arrived.wait(timeout=10)
            assert release.wait(timeout=10)
            return subprocess.CompletedProcess(argv, 1)
        assert argv[:3] == ["docker", "rm", "--force"]
        return subprocess.CompletedProcess(argv, 0)

    monkeypatch.setattr(subprocess, "run", execute)
    request = {"harness": "codex", "effort": "high", "role": "optimizer", "workspace": "/workspace/source", "prompt": "Fixed directive."}
    settings = {"image": "sha256:fixture", "model": "configured", "effort": "high", "channel": "codex", "base_url": ""}
    settings.setdefault("credentials", proxy_credentials(settings.get("channel", "codex")))
    with runtime, ThreadPoolExecutor(max_workers=2) as pool:
        futures = [pool.submit(runtime._docker_execute, request,
                               settings | {"_container_name": f"culler-eval-owned-{index}"},
                               tmp_path / f"execution-{index}", [], {}) for index in range(2)]
        arrived.wait(timeout=10)
        stop.write_text("Stop this experiment.")
        release.set()
        for future in futures:
            with pytest.raises(KeyboardInterrupt):
                future.result(timeout=10)
        with pytest.raises(KeyboardInterrupt):
            runtime._docker_execute(request, settings, tmp_path / "retry", [], {})
    assert len([call for call in calls if call[1] == "run"]) == 2
    assert {call[-1] for call in calls if call[1] == "rm"} == {"culler-eval-owned-0", "culler-eval-owned-1"}
    assert not (tmp_path / "retry").exists()


@pytest.mark.parametrize("owner,stop", [("run", None), (None, "/tmp/stop"), ("invalid=run", "/tmp/stop")])
def test_incomplete_or_invalid_owner_identity_cannot_launch(tmp_path, monkeypatch, owner, stop):
    for name, value in (("CULLER_EVAL_CONTROL_RUN", owner), ("CULLER_EVAL_CONTROL_CANCEL_FILE", stop)):
        if value is None:
            monkeypatch.delenv(name, raising=False)
        else:
            monkeypatch.setenv(name, value)
    with pytest.raises(RuntimeErrorWithEvidence, match="browser run"):
        Runtime(SOURCE, contracts=object())
