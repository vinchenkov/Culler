import json
from pathlib import Path
import socketserver
import tempfile
import threading

import pytest

from culler_evals.credentials import CredentialPool, CredentialProjection
from culler_evals.product_sources import create_product_snapshot, product_source_manifest

SOURCE = Path(__file__).resolve().parents[2]


def private_json(path, value):
    path.write_text(json.dumps(value))
    path.chmod(0o600)


@pytest.fixture
def resident_owner(tmp_path):
    directory = tempfile.TemporaryDirectory(prefix="ce-", dir="/tmp")
    home = Path(directory.name)
    (home / "health").mkdir(mode=0o700)
    state = {"requests": [], "wrong_nonce": False, "raw_response": False, "grants": {}, "harness": "codex", "kind": "codex-oauth"}

    class Owner(socketserver.StreamRequestHandler):
        def handle(self):
            request = json.loads(self.rfile.readline())
            state["requests"].append(request)
            response = {"version": 1, "status": "ok", "nonce": "0" * 32 if state["wrong_nonce"] else request["nonce"]}
            key = request["run_id"], request["launch_id"]
            if request["op"] == "profile_authorize":
                state["grants"][key] = request
                response |= {"credential": "fixture", "proxy_config": {
                    "version": 1, "harness": state["harness"], "url": "https://host.docker.internal:12345",
                    "token": "culler_proxy_" + "x" * 43, "ca": "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----",
                    "authMode": "api-key" if state["kind"].endswith("-api-key") else "account"}}
                if state["raw_response"]:
                    response["access_token"] = "synthetic-provider-secret"
            elif request["op"] == "profile_status":
                response["provider_requests"] = 2
            elif request["op"] == "profile_revoke":
                state["grants"].pop(key, None)
                response["revoked"] = True
            else:
                pytest.fail("The bridge requested provider credentials.")
            self.wfile.write((json.dumps(response) + "\n").encode())

    socket_path = home / "health/provider-acceptance.sock"
    owner = socketserver.ThreadingUnixStreamServer(str(socket_path), Owner)
    socket_path.chmod(0o600)
    thread = threading.Thread(target=owner.serve_forever, daemon=True)
    thread.start()

    def profile(harness="codex", method="oauth"):
        channel = "claude" if harness == "claude-sdk" else harness
        state["harness"], state["kind"] = harness, f"{channel}-{method}"
        (home / "config.toml").write_text(f'''[runtimes]
enabled_bindings = ["fixture"]
[runtimes.bindings.fixture]
harness = "{harness}"
credential = "fixture"
[runtimes.credentials.fixture]
kind = "{state['kind']}"
inference_url = "https://provider.example.invalid"
token_url = "https://provider.example.invalid/token"
client_id = "fixture-client"
''')
        path = tmp_path / "profile.json"
        private_json(path, {"purpose": "directive-evaluation", "resident_home": str(home), "catalog": {"fixture": {
            "harness": channel, "credential": "fixture", "channel": channel, "kind": state["kind"],
            "inference_url": "https://provider.example.invalid", "token_url": "https://provider.example.invalid/token", "client_id": "fixture-client"}}})
        return path

    try:
        yield state, home, socket_path, profile
    finally:
        owner.shutdown()
        owner.server_close()
        thread.join()
        directory.cleanup()


@pytest.mark.parametrize("harness", ["codex", "claude-sdk"])
@pytest.mark.parametrize("method", ["oauth", "api-key"])
def test_resident_projection_has_only_scoped_access_and_revokes_each_trial(resident_owner, harness, method):
    state, home, _, profile = resident_owner
    path = profile(harness, method)
    with CredentialProjection(SOURCE, str(path), "fixture") as handle:
        result = handle.current("exact-fixture-model", 90)
        request = state["requests"][0]
        assert request["op"] == "profile_authorize"
        assert request["credential"] == "fixture" and request["profile"] == "fixture"
        assert request["model"] == "exact-fixture-model" and request["max_requests"] == 100
        assert request["run_id"].startswith("eval-") and request["launch_id"]
        assert result["provider_access"]["harness"] == harness
        assert set(result) == {"harness", "channel", "base_url", "provider_access", "grant_id"}
        assert not (home / "refresh-grants").exists()
        assert handle.release(result) == 2
        assert state["grants"] == {}
    assert [r["op"] for r in state["requests"]] == ["profile_authorize", "profile_status", "profile_revoke"]


def test_pool_retains_one_bridge_but_isolates_concurrent_grants(resident_owner):
    state, _, _, profile = resident_owner
    path = profile()
    pool = CredentialPool(SOURCE)
    try:
        first = pool.project(str(path), "fixture", "first-model", 30)
        second = pool.project(str(path), "fixture", "second-model", 60)
        assert len(pool.handles) == 1 and first["grant_id"] != second["grant_id"]
        assert len(state["grants"]) == 2
        assert pool.release(str(path), "fixture", first) == 2
        assert len(state["grants"]) == 1
    finally:
        pool.close()
    assert pool.handles == {} and state["grants"] == {}


@pytest.mark.parametrize("failure", ["socket_permissions", "nonce", "raw_response", "disabled_binding"])
def test_bridge_refuses_unsafe_or_changed_authority(resident_owner, failure):
    state, home, socket_path, profile = resident_owner
    path = profile()
    if failure == "socket_permissions":
        socket_path.chmod(0o666)
    elif failure == "nonce":
        state["wrong_nonce"] = True
    elif failure == "raw_response":
        state["raw_response"] = True
    else:
        config = home / "config.toml"
        config.write_text(config.read_text().replace('enabled_bindings = ["fixture"]', 'enabled_bindings = []'))
    with pytest.raises(RuntimeError, match="refused"):
        with CredentialProjection(SOURCE, str(path), "fixture") as handle:
            handle.current("exact-model", 10)
    assert not any(r["op"] == "profile_mint" for r in state["requests"])
    if failure in ("nonce", "raw_response"):
        assert state["grants"] == {}
    else:
        assert state["requests"] == []


def test_raw_grant_profiles_require_a_culler_connection_without_reading_the_grant(tmp_path):
    grant = tmp_path / "grant.json"
    private_json(grant, {"secret": "synthetic-secret"})
    profile = tmp_path / "profile.json"
    private_json(profile, {"purpose": "directive-evaluation", "catalog": {}, "grant_files": {"fixture": str(grant)}})
    original = grant.read_bytes()
    with pytest.raises(RuntimeError, match="Save the credentials as a Culler connection"):
        with CredentialProjection(SOURCE, str(profile), "fixture"):
            pass
    assert grant.read_bytes() == original
    assert not (tmp_path / "grant.json.eval-lock").exists()


def test_profile_requires_private_permissions_and_external_storage(tmp_path):
    profile = tmp_path / "profile.json"
    private_json(profile, {"purpose": "directive-evaluation"})
    profile.chmod(0o644)
    with pytest.raises(RuntimeError, match="refused"):
        with CredentialProjection(SOURCE, str(profile), "fixture"):
            pass
    with pytest.raises(RuntimeError, match="outside the Culler checkout"):
        with CredentialProjection(SOURCE, str(profile), "fixture", checkout_root=tmp_path):
            pass


def test_projection_imports_frozen_product_sources(tmp_path, resident_owner):
    state, _, _, profile = resident_owner
    source = create_product_snapshot(SOURCE, tmp_path / "snapshot", product_source_manifest(SOURCE))
    with CredentialProjection(source, str(profile()), "fixture", checkout_root=SOURCE) as handle:
        result = handle.current("exact-model", 10)
        assert result["provider_access"]["harness"] == "codex"
    assert state["grants"] == {}


@pytest.mark.parametrize("model,duration", [("", 10), ("exact", 0), ("exact", 1801)])
def test_trial_scope_requires_exact_model_and_bounded_duration(resident_owner, model, duration):
    state, _, _, profile = resident_owner
    with pytest.raises(RuntimeError, match="refused"):
        with CredentialProjection(SOURCE, str(profile()), "fixture") as handle:
            handle.current(model, duration)
    assert state["requests"] == []


@pytest.mark.parametrize("change", ["disabled", "endpoint"])
def test_pool_rechecks_the_connection_before_each_trial(resident_owner, change):
    state, home, _, profile = resident_owner
    path = profile()
    with pytest.raises(RuntimeError, match="refused"):
        with CredentialProjection(SOURCE, str(path), "fixture") as handle:
            first = handle.current("first-model", 30)
            config = home / "config.toml"
            before = config.read_text()
            config.write_text(before.replace('enabled_bindings = ["fixture"]', 'enabled_bindings = []') if change == "disabled"
                              else before.replace('inference_url = "https://provider.example.invalid"',
                                                  'inference_url = "https://different.example.invalid"'))
            handle.current("second-model", 30)
    assert len([request for request in state["requests"] if request["op"] == "profile_authorize"]) == 1
    assert state["grants"] == {}
