import json
from pathlib import Path
import shutil
import subprocess
import tempfile

import pytest

from culler_evals import cli, storage
from culler_evals.records import RecordError, RecordStore


@pytest.mark.parametrize("path", ["/tmp/evaluation", "/private/tmp/evaluation", "/var/tmp/evaluation"])
def test_temporary_records_are_rejected(path):
    with pytest.raises(ValueError, match="persistent directory"):
        storage.durable_directory(path)


def test_temporary_directory_alias_and_environment_are_rejected(tmp_path, monkeypatch):
    alias = tmp_path / "alias"
    alias.symlink_to(tempfile.gettempdir(), target_is_directory=True)
    with pytest.raises(ValueError, match="persistent directory"):
        storage.durable_directory(alias / "experiment")
    monkeypatch.setenv("TMPDIR", str(tmp_path / "disposable"))
    with pytest.raises(ValueError, match="persistent directory"):
        storage.durable_directory(tmp_path / "disposable/run")
    assert storage.durable_directory(tmp_path / "persistent") == tmp_path / "persistent"


def test_run_rejects_temporary_records_before_input_or_model_access(tmp_path, monkeypatch):
    args = cli.parser().parse_args(["run", "--protocol", "absent.json", "--output", "/tmp/unsafe-experiment",
                                   "--cache", str(tmp_path / "cache")])
    monkeypatch.setattr(cli, "_resolved_protocol", lambda *_: pytest.fail("Unsafe storage reached input access."))
    with pytest.raises(ValueError, match="persistent directory"):
        cli.execute(args)


def test_backup_restores_journal_candidates_and_blobs_after_primary_deletion(tmp_path):
    root = tmp_path / "experiments/run"
    with RecordStore(root) as store:
        backup = store.enable_backup()
        blob = store.put_blob("A retained packet.")
        store.write_manifest("checkpoint", {"artifact": blob})
        store.append("candidate_created", {"texts": {"packager_directive": "Preserve the explanation."}}, candidate_id="D1")
    shutil.rmtree(root)
    assert storage.restore_records(backup) == root
    with RecordStore(root, read_only=True) as store:
        assert store.read_blob(blob) == b"A retained packet."
        assert store.candidates()["D1"]["texts"]["packager_directive"] == "Preserve the explanation."
        assert store.read_manifest("checkpoint")["artifact"] == blob
        assert store.events()[-1]["kind"] == "records_recovered"
    with pytest.raises(ValueError, match="exists"):
        storage.restore_records(backup)


def test_backup_failure_stops_dependent_work(tmp_path, monkeypatch):
    with RecordStore(tmp_path / "run") as store:
        store.enable_backup()
        mirror = store._mirror
        def fail(*_):
            store.fail(OSError("Backup disk unavailable."))
            raise RecordError("Cannot preserve the experiment backup.")
        monkeypatch.setattr(store, "_mirror", fail)
        with pytest.raises(RecordError, match="backup"):
            store.put_blob("Required output.")
        with pytest.raises(RecordError, match="Dependent work"):
            store.begin_operation("optimizer")
        monkeypatch.setattr(store, "_mirror", mirror)


def test_removed_primary_directory_cannot_silently_create_a_new_journal(tmp_path):
    root = tmp_path / "run"
    with RecordStore(root) as store:
        backup = store.enable_backup()
        store.append("original_event")
        shutil.rmtree(root)
        with pytest.raises(RecordError, match="Dependent work"):
            store.append("later_event")
        assert not root.exists()
    with RecordStore(backup, read_only=True) as store:
        assert [event["kind"] for event in store.events()] == ["original_event"]


def test_source_archive_recovers_a_deleted_worktree_with_exact_overlay(tmp_path, monkeypatch):
    from culler_evals import continuation
    monkeypatch.setattr(storage, "WORKTREE_ROOT", tmp_path / "worktrees")
    repository = tmp_path / "repository"
    repository.mkdir()
    subprocess.run(["git", "init", str(repository)], check=True, capture_output=True)
    (repository / "code.py").write_text("original\n")
    subprocess.run(["git", "add", "."], cwd=repository, check=True)
    subprocess.run(["git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Initial source"],
                   cwd=repository, check=True, capture_output=True)
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repository, text=True).strip()
    source = storage.WORKTREE_ROOT / ("storage-test-" + tmp_path.name)
    subprocess.run(["git", "worktree", "add", "--detach", str(source), revision], cwd=repository, check=True, capture_output=True)
    (source / "code.py").write_text("exact overlay\n")
    (source / "private-auth.json").write_text("private fixture")
    def verify(protocol, checkout):
        assert subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=checkout, text=True).strip() == revision
        assert (checkout / "code.py").read_text() == "exact overlay\n"
    monkeypatch.setattr(continuation, "verify_source", verify)
    protocol = {"source_revision": revision, "protocol_hash": "fixed", "framework_source_hashes": {"code.py": "test"},
                "product_source_manifest": {"files": []}, "runtime_source_manifest": {"files": []}}
    try:
        with RecordStore(tmp_path / "run") as store:
            store.enable_backup()
            storage.preserve_source(store, protocol, source)
            shutil.rmtree(source)
            assert storage.restore_source(store, protocol, repository) == source
            verify(protocol, source)
            assert not (source / "private-auth.json").exists()
    finally:
        subprocess.run(["git", "worktree", "remove", "--force", str(source)], cwd=repository, check=True, capture_output=True)


def test_new_run_cannot_replace_the_backup_of_an_absent_experiment(tmp_path):
    root = tmp_path / "run"
    with RecordStore(root) as store:
        backup = store.enable_backup()
        packet = store.put_blob("Preserve the previous experiment.")
        store.append("original_event", {"packet": packet})
    before = (backup / "events.jsonl").read_bytes()
    shutil.rmtree(root)
    with RecordStore(root) as store:
        with pytest.raises(RecordError, match="backup already exists"):
            store.enable_backup()
    assert (backup / "events.jsonl").read_bytes() == before
    with RecordStore(backup, read_only=True) as store:
        assert store.read_blob(packet) == b"Preserve the previous experiment."
