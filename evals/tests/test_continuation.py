import copy
import json
from pathlib import Path

import pytest

import culler_evals.continuation as module
from culler_evals.cli import _resolved_protocol
from culler_evals.continuation import prepare_update, update_preview
from culler_evals.evaluator import original_directive
from culler_evals.protocol import digest, freeze_protocol
from culler_evals.records import RecordStore, content_hash

SOURCE = Path(__file__).resolve().parents[2]


def original_protocol():
    protocol = _resolved_protocol(SOURCE / "evals/config/packager.production-short.json", SOURCE)
    protocol["runtime"]["judge"]["fast_mode"] = False
    protocol["seed_candidate"] = {"packager_directive": original_directive(SOURCE)[0]}
    protocol["case_hashes"] = {"valset": {"fixture": "fixed"}}
    return freeze_protocol(protocol, SOURCE)


def initialize(root):
    protocol = original_protocol()
    with RecordStore(root) as store:
        store.write_manifest("protocol", protocol)
        identity = {"protocol": protocol, "gepa_version": "0.1.4", "trainset": [{"id": "training"}], "valset": [{"id": "fixture"}]}
        store.write_manifest("search_identity", {"identity": identity, "hash": content_hash(identity)})
        store.append("candidate_created", {"texts": protocol["seed_candidate"], "parent_ids": []}, candidate_id="D0")
        slot = {"slot": "fixed-slot", "trial_id": "trial-" + "a" * 32,
                "runtime_result": store.put_blob({"metadata": {"runtime_source_hash": "old-runtime"}}),
                "prepared": store.put_blob({"manifest_hash": "fixed"}), "completed": store.put_blob({"quality": 75}),
                "artifacts": {"packet_path": store.put_blob("The original packet.")}, "judge_attempts": 1}
        store.write_manifest("trial-slot-fixture", slot)
        store.append("run_session_completed", {"outcome": "failed"})
    return protocol, slot


def test_update_preserves_inputs_packets_and_prior_scores(tmp_path):
    root = tmp_path / "experiment"
    before, original_slot = initialize(root)
    with RecordStore(root, read_only=True) as store:
        prior_events = store.events()
    after, source = prepare_update(root, SOURCE, source_factory=lambda *_: SOURCE)
    assert source == SOURCE
    for name in ("seed_candidate", "case_hashes", "datasets", "rubric", "operator_context", "protected", "trials", "search",
                 "product_source_manifest", "product_wrapper_hashes"):
        assert after[name] == before[name]
    assert after["sample_protocol_hash"] == before["protocol_hash"]
    for role, settings in after["runtime"].items():
        assert settings == before["runtime"][role] | {"fast_mode": True}
    with RecordStore(root, read_only=True) as store:
        assert store.events()[:len(prior_events)] == prior_events
        slot = store.read_manifest("trial-slot-fixture")
        assert "completed" not in slot
        assert slot["previous_completed"] == original_slot["completed"]
        assert store.read_blob(slot["previous_completed"]) == b'{"quality":75}'
        assert slot["runtime_result"] == original_slot["runtime_result"]
        assert slot["artifacts"] == original_slot["artifacts"]
        assert store.read_manifest("generation_protocol-" + before["protocol_hash"]) == before
        identity = store.read_manifest("search_identity")
        assert identity["identity"]["protocol"] == after
        assert identity["hash"] == content_hash(identity["identity"])
        assert [event["kind"] for event in store.events()].count("resume_runtime_updated") == 1
    assert prepare_update(root, SOURCE, source_factory=lambda *_: pytest.fail("Repeated resume must reuse its source.")) == (after, SOURCE)


@pytest.mark.parametrize("kind", ["parent_selected", "validation_scored", "optimizer_response_received"])
def test_update_refuses_a_search_that_advanced(tmp_path, kind):
    root = tmp_path / "experiment"
    before, _ = initialize(root)
    with RecordStore(root) as store:
        store.append(kind, {})
    with pytest.raises(ValueError, match="incomplete initial validation"):
        prepare_update(root, SOURCE, source_factory=lambda *_: pytest.fail("The source must stay intact."))
    with RecordStore(root, read_only=True) as store:
        assert store.read_manifest("protocol") == before
        assert store.read_manifest("resume_update") is None


def test_update_recovers_after_a_manifest_write_fails(tmp_path, monkeypatch):
    root = tmp_path / "experiment"
    initialize(root)
    original = RecordStore.write_manifest
    failed = False

    def fail_once(self, name, data):
        nonlocal failed
        if name == "protocol" and not failed:
            failed = True
            raise OSError("The disk write failed.")
        return original(self, name, data)

    monkeypatch.setattr(RecordStore, "write_manifest", fail_once)
    with pytest.raises(OSError):
        prepare_update(root, SOURCE, source_factory=lambda *_: SOURCE)
    after, _ = prepare_update(root, SOURCE, source_factory=lambda *_: pytest.fail("Recover the recorded update."))
    with RecordStore(root, read_only=True) as store:
        assert store.read_manifest("protocol") == after
        assert store.read_manifest("resume_update")["applied"] is True
        assert [event["kind"] for event in store.events()].count("resume_runtime_updated") == 1


def test_update_refuses_other_assessment_changes(tmp_path):
    root = tmp_path / "experiment"
    initialize(root)
    with RecordStore(root) as store:
        protocol = store.read_manifest("protocol")
        protocol["assessment_contract"]["semantic_contracts"]["glossary"] = "Different rules."
        protocol["protocol_hash"] = digest({key: value for key, value in protocol.items() if key != "protocol_hash"})
        store.write_manifest("protocol", protocol)
        with pytest.raises(ValueError, match="assessment rules differ"):
            update_preview(store)


def test_the_json_format_fix_preserves_the_assessment_rules():
    old = copy.deepcopy(module.assessment_contract())
    old.pop("judge_output_schema_template")
    import hashlib
    old["judge_instructions_hash"] = hashlib.sha256(module.JUDGE_INSTRUCTIONS.replace(
        "The semantic_checks value is an array of objects, with one object for each required identifier. ", "").encode()).hexdigest()
    assert module._compatible_assessment(old)


def repair_fixture(root, monkeypatch):
    initialize(root)
    before, _ = prepare_update(root, SOURCE, source_factory=lambda *_: SOURCE)
    changed = before["framework_source_hashes"] | {"evals/culler_evals/trial_resources.py": "repaired"}
    monkeypatch.setattr(module, "framework_source_hashes", lambda _: changed)
    def freeze(value, source):
        frozen = freeze_protocol(value, source)
        frozen["framework_source_hashes"] = changed
        frozen["protocol_hash"] = digest({key: value for key, value in frozen.items() if key != "protocol_hash"})
        return frozen
    monkeypatch.setattr(module, "freeze_protocol", freeze)
    monkeypatch.setattr(module, "verify_source", lambda *_: None)
    monkeypatch.setattr(module, "recorded_source", lambda *_: SOURCE)
    with RecordStore(root) as store:
        slot = store.read_manifest("trial-slot-fixture")
        slot["completed"] = store.put_blob({"quality": 92.5})
        store.write_manifest("trial-slot-fixture", slot)
    return before, slot


def test_framework_repair_preserves_new_scores_and_records_source_changes(tmp_path, monkeypatch):
    root = tmp_path / "experiment"
    before, slot = repair_fixture(root, monkeypatch)
    after, _ = prepare_update(root, SOURCE, source_factory=lambda *_: SOURCE)
    assert {key: value for key, value in after.items() if key not in ("framework_source_hashes", "protocol_hash")} == {
        key: value for key, value in before.items() if key not in ("framework_source_hashes", "protocol_hash")}
    with RecordStore(root, read_only=True) as store:
        assert store.read_manifest("trial-slot-fixture") == slot
        assert store.read_manifest("search_identity")["identity"]["protocol"] == after
        event = next(event for event in store.events() if event["kind"] == "framework_repaired")
        assert event["data"]["assessments_to_repeat"] == 0
        assert json.loads(store.read_blob(event["data"]["before"])) == before
    assert prepare_update(root, SOURCE, source_factory=lambda *_: pytest.fail("Reuse the recorded repair."))[0] == after


def test_framework_repair_recovers_a_protocol_write_failure(tmp_path, monkeypatch):
    root = tmp_path / "experiment"
    _, slot = repair_fixture(root, monkeypatch)
    write = RecordStore.write_manifest
    failed = False
    def interrupt(self, name, data):
        nonlocal failed
        if name == "protocol" and not failed:
            failed = True
            raise OSError("The disk write failed.")
        return write(self, name, data)
    monkeypatch.setattr(RecordStore, "write_manifest", interrupt)
    with pytest.raises(OSError):
        prepare_update(root, SOURCE, source_factory=lambda *_: SOURCE)
    after, _ = prepare_update(root, SOURCE, source_factory=lambda *_: pytest.fail("Recover the recorded repair."))
    with RecordStore(root, read_only=True) as store:
        assert store.read_manifest("protocol") == after
        assert store.read_manifest("trial-slot-fixture") == slot
        assert len([event for event in store.events() if event["kind"] == "framework_repaired"]) == 1


def test_framework_repair_refuses_changes_to_the_judge(tmp_path, monkeypatch):
    root = tmp_path / "experiment"
    before, _ = repair_fixture(root, monkeypatch)
    monkeypatch.setattr(module, "framework_source_hashes", lambda _: before["framework_source_hashes"] | {
        "evals/culler_evals/assessment.py": "different-rules"})
    with pytest.raises(ValueError, match="other evaluation files"):
        prepare_update(root, SOURCE, source_factory=lambda *_: pytest.fail("Keep the recorded source."))
