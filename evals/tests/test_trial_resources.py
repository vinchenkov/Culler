import json
from pathlib import Path
import shutil
import subprocess

import pytest

import culler_evals.evaluator as evaluator_module
import culler_evals.trial_resources as resources
from culler_evals.evaluator import EvaluationError, Evaluator
from culler_evals.records import RecordError, RecordStore
from test_evaluator import setup


TRIAL_ID = "trial-" + "1" * 32


def git(repository, *arguments):
    return subprocess.run(["git", "-C", str(repository), *arguments], check=True, capture_output=True, text=True).stdout.strip()


def source_fixture(repository):
    repository.mkdir(parents=True)
    git(repository, "init", "--quiet")
    git(repository, "config", "user.name", "Resource fixture")
    git(repository, "config", "user.email", "fixture@example.invalid")
    (repository / "tracked.txt").write_text("The reviewed source.\n")
    (repository / ".gitignore").write_text("ignored.txt\n")
    git(repository, "add", ".")
    git(repository, "commit", "--quiet", "-m", "The reviewed source")
    return git(repository, "rev-parse", "HEAD")


def probe(paths):
    return {"states": {role: {"commit": git(path, "rev-parse", "HEAD"),
                              "branch": git(path, "rev-parse", "--symbolic-full-name", "HEAD"),
                              "clean": git(path, "status", "--porcelain=v1", "--untracked-files=all", "--ignored") == ""}
                       for role, path in paths.items()}}


@pytest.fixture
def completed(tmp_path):
    with RecordStore(tmp_path / "experiment") as store:
        trial = store.root / "trials" / TRIAL_ID
        authority = trial / "case/repository"
        commit = source_fixture(authority)
        role = trial / "packager/repository"
        role.parent.mkdir()
        shutil.copytree(authority, role)
        (trial / "case/artifacts").mkdir()
        (trial / "case/artifacts/report.txt").write_text("The fixed report.")
        prepared = {"repository_path": str(authority), "reviewed_commit": commit, "manifest_hash": "fixed-case-hash"}
        metadata = {"repository_changed": False, "evidence_changed": []}
        packet = store.put_blob("The preserved packet.")
        row = {"trial_id": TRIAL_ID, "metadata": metadata, "artifacts": {"packet_path": packet}, "judgment": None, "contribution": 75}
        saved = {"prepared": store.put_blob(prepared), "runtime_result": store.put_blob({"metadata": metadata}),
                 "completed": store.put_blob(row),
                 "repositories": resources.capture_repositories(store, TRIAL_ID, prepared, resources.capture_authority_state(store, TRIAL_ID, prepared))}
        yield store, saved, row, trial


def dispose(completed, **options):
    store, saved, row, _ = completed
    return resources.dispose_completed_repositories(store, saved, row, case_id="fixture", stage="training",
                                                    probe=options.pop("probe", probe), case_manifest={"id": "fixture", "source": {"reviewed_commit": "fixed"}}, **options)


def test_completed_unchanged_copies_leave_scores_evidence_artifacts_and_cache(completed, tmp_path):
    store, saved, row, trial = completed
    cache = tmp_path / "cache/repository"
    cache.mkdir(parents=True)
    (cache / "source.txt").write_text("The reusable source cache.")
    record = dispose(completed)
    assert record["status"] == "disposed"
    assert not (trial / "case/repository").exists()
    assert not (trial / "packager/repository").exists()
    assert (trial / "case/artifacts/report.txt").read_text() == "The fixed report."
    assert (cache / "source.txt").read_text() == "The reusable source cache."
    assert json.loads(store.read_blob(saved["completed"])) == row
    assert store.read_blob(row["artifacts"]["packet_path"]) == b"The preserved packet."
    assert json.loads(store.read_blob(record["case_manifest"]))["id"] == "fixture"
    assert dispose(completed) == record
    assert len([event for event in store.events() if event["kind"] == "trial_repositories_disposed"]) == 1


def record_reassessment(completed):
    store, saved, row, _ = completed
    row["sample_slot"] = "fixed-slot"
    saved["completed"] = store.put_blob(row)
    original = dict(saved)
    store.write_manifest("resume_update", {"applied": True,
                         "slots": {"trial-slot-fixed-slot": store.put_blob(original)}})
    return original


@pytest.mark.parametrize("state", ["disposed", "retained", "pending"])
def test_reassessment_preserves_the_original_disposal_record(completed, monkeypatch, state):
    store, saved, row, trial = completed
    original = record_reassessment(completed)
    if state == "pending":
        real_remove = shutil.rmtree
        def interrupt(*_arguments, **_options):
            raise KeyboardInterrupt()
        interrupt.avoids_symlink_attacks = True
        monkeypatch.setattr(shutil, "rmtree", interrupt)
        with pytest.raises(KeyboardInterrupt):
            dispose(completed)
        monkeypatch.setattr(shutil, "rmtree", real_remove)
        receipt = store.read_manifest("trial-repositories-" + TRIAL_ID)
    else:
        receipt = dispose(completed, probe=(lambda _: {"states": {}}) if state == "retained" else probe)
    saved["previous_completed"] = original["completed"]
    row["contribution"] = 92.5
    saved["completed"] = store.put_blob(row)
    result = dispose(completed)
    assert result["completed"] == original["completed"]
    assert result["status"] == ("retained" if state == "retained" else "disposed")
    if state != "pending":
        assert result == receipt
    assert json.loads(store.read_blob(saved["completed"]))["contribution"] == 92.5
    assert dispose(completed) == result
    assert (trial / "case/repository").exists() is (state == "retained")


@pytest.mark.parametrize("change", ["no-update", "unapplied", "wrong-slot", "wrong-reference", "packet", "terminal", "metadata"])
def test_reassessment_refuses_unrecorded_or_changed_generation(completed, change):
    store, saved, row, _ = completed
    original = record_reassessment(completed)
    receipt = dispose(completed)
    saved["previous_completed"] = original["completed"]
    row["contribution"] = 92.5
    update = store.read_manifest("resume_update")
    if change == "no-update":
        store.write_manifest("resume_update", None)
    elif change == "unapplied":
        store.write_manifest("resume_update", update | {"applied": False})
    elif change == "wrong-slot":
        store.write_manifest("resume_update", update | {"slots": {}})
    elif change == "wrong-reference":
        saved["previous_completed"] = store.put_blob({"contribution": 50})
    elif change == "packet":
        row["artifacts"] = {"packet_path": store.put_blob("A different packet.")}
    elif change == "terminal":
        row["terminal"] = {"status": "packaged", "summary": "A different submission."}
    else:
        row["metadata"] = {"repository_changed": True, "evidence_changed": []}
        saved["runtime_result"] = store.put_blob({"metadata": row["metadata"]})
    saved["completed"] = store.put_blob(row)
    with pytest.raises(resources.TrialResourceError):
        dispose(completed)
    assert store.read_manifest("trial-repositories-" + TRIAL_ID) == receipt


@pytest.mark.parametrize("state", ["incomplete", "changed", "unattested", "changed-evidence", "unverifiable"])
def test_incomplete_changed_or_unverified_repositories_stay(completed, state):
    store, saved, row, trial = completed
    if state == "incomplete":
        saved["completed"] = None
    elif state in ("changed", "unattested"):
        row["metadata"]["repository_changed"] = True if state == "changed" else None
    elif state == "changed-evidence":
        row["metadata"]["evidence_changed"] = ["report.txt"]
    if state != "incomplete":
        saved["completed"] = store.put_blob(row)
        saved["runtime_result"] = store.put_blob({"metadata": row["metadata"]})
    result = dispose(completed, probe=(lambda _paths: {"states": {}}) if state == "unverifiable" else probe)
    assert result is None if state == "incomplete" else result["status"] == "retained"
    assert (trial / "case/repository/tracked.txt").is_file()
    assert (trial / "packager/repository/tracked.txt").is_file()
    assert not any(event["kind"] == "trial_repository_disposal_started" for event in store.events())


@pytest.mark.parametrize("change", ["file", "branch", "commit", "ignored", "configuration", "authority-file", "git-root-link"])
def test_later_same_directory_edits_retain_both_copies(completed, change):
    _, _, _, trial = completed
    repository = trial / ("case/repository" if change == "authority-file" else "packager/repository")
    inode = repository.stat().st_ino
    if change in ("file", "authority-file"):
        (repository / "tracked.txt").write_text("A later source edit.\n")
    elif change == "branch":
        git(repository, "checkout", "--quiet", "-b", "later-branch")
    elif change == "commit":
        (repository / "tracked.txt").write_text("A later committed edit.\n")
        git(repository, "commit", "--quiet", "-am", "The later commit")
    elif change == "ignored":
        (repository / "ignored.txt").write_text("An ignored extra file.")
    elif change == "configuration":
        git(repository, "config", "core.fsmonitor", "false")
    else:
        location = trial / "packager/original-git"
        (repository / ".git").rename(location)
        (repository / ".git").symlink_to(location, target_is_directory=True)
    assert repository.stat().st_ino == inode
    record = dispose(completed)
    assert record["status"] == "retained"
    assert (trial / "case/repository").is_dir()
    assert (trial / "packager/repository").is_dir()


@pytest.mark.parametrize("change", ["outside-path", "root-link", "parent-link", "repository-link", "replacement"])
def test_unsafe_paths_refuse_disposal_and_preserve_external_files(completed, tmp_path, change):
    store, saved, _, trial = completed
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "keep.txt").write_text("The external file.")
    if change == "outside-path":
        saved["repositories"]["copies"][0]["path"] = str(outside)
    elif change == "root-link":
        trial.rename(trial.with_name("original-trial"))
        trial.symlink_to(outside, target_is_directory=True)
    elif change == "parent-link":
        (trial / "case").rename(trial / "original-case")
        (trial / "case").symlink_to(outside, target_is_directory=True)
    elif change == "repository-link":
        (trial / "packager/repository").rename(trial / "packager/original-repository")
        (trial / "packager/repository").symlink_to(outside, target_is_directory=True)
    else:
        (trial / "packager/repository").rename(trial / "packager/original-repository")
        (trial / "packager/repository").mkdir()
    with pytest.raises(resources.TrialResourceError, match="unsafe directory"):
        dispose(completed)
    assert (outside / "keep.txt").read_text() == "The external file."
    assert not any(event["kind"] == "trial_repository_disposal_started" for event in store.events())


@pytest.mark.parametrize("damage", ["tracked-file", "git-directory", "entire-copy"])
def test_partial_removal_resumes_from_durable_verification(completed, monkeypatch, damage):
    real_remove = shutil.rmtree
    interrupted = [False]

    def remove(name, **arguments):
        if not interrupted[0]:
            interrupted[0] = True
            parent_fd = arguments["dir_fd"]
            import os
            if damage == "tracked-file":
                os.unlink("repository/tracked.txt", dir_fd=parent_fd)
            else:
                real_remove("repository/.git" if damage == "git-directory" else "repository", dir_fd=parent_fd)
            raise KeyboardInterrupt()
        return real_remove(name, **arguments)

    remove.avoids_symlink_attacks = True
    monkeypatch.setattr(shutil, "rmtree", remove)
    with pytest.raises(KeyboardInterrupt):
        dispose(completed)
    store, _, _, trial = completed
    state = store.read_manifest("trial-repositories-" + TRIAL_ID)
    assert state["fresh_verification"]
    assert state["copies"]["case/repository"]["started"] is True
    assert (trial / "case/repository/.git").is_dir() is (damage == "tracked-file")
    monkeypatch.setattr(shutil, "rmtree", real_remove)
    assert dispose(completed)["status"] == "disposed"
    assert not (trial / "case/repository").exists()


def test_completed_score_reuse_finishes_disposal_without_new_model_work(setup, monkeypatch):
    evaluator, runtime, store, case = setup
    evaluator.protocol["trials"]["pilot"] = 1

    def prepare(_case, destination, *_arguments, **_options):
        repository = destination / "repository"
        commit = source_fixture(repository)
        return {"repository_path": str(repository), "reviewed_commit": commit, "manifest_hash": "fixed-manifest"}

    execute = runtime.execute_packager
    def run(prepared, directive, root, settings):
        (root / "packager").mkdir()
        shutil.copytree(prepared["repository_path"], root / "packager/repository")
        return execute(prepared, directive, root, settings)

    monkeypatch.setattr(evaluator_module, "prepare_case", prepare)
    runtime.execute_packager = run
    runtime.repository_states = lambda paths, _settings: probe(paths)
    real_dispose = evaluator_module.dispose_completed_repositories
    monkeypatch.setattr(evaluator_module, "dispose_completed_repositories", lambda *_args, **_options: (_ for _ in ()).throw(KeyboardInterrupt()))
    candidate = {"packager_directive": "Fixed contract"}
    with pytest.raises(KeyboardInterrupt):
        evaluator.evaluate(candidate, [case], "pilot", False)
    completed_event = next(event for event in store.events() if event["kind"] == "trial_assessed")
    trial_id = completed_event["trial_id"]
    counts = runtime.packager_calls, runtime.judge_calls
    store.close()
    monkeypatch.setattr(evaluator_module, "dispose_completed_repositories", real_dispose)
    with RecordStore(store.root) as resumed_store:
        resumed = Evaluator(evaluator.protocol, evaluator.source, resumed_store, runtime, cache=evaluator.cache)
        result = resumed.evaluate(candidate, [case], "pilot", False)
        assert result["scores"] == [.75]
        assert result["outputs"][0]["trials"][0]["trial_id"] == trial_id
        assert not (store.root / "trials" / trial_id / "case/repository").exists()
        assert any(event["kind"] == "trial_reused" for event in resumed_store.events())
    assert (runtime.packager_calls, runtime.judge_calls) == counts == (1, 1)


def test_attestation_or_evidence_corruption_prevents_disposal(completed):
    store, saved, row, trial = completed
    saved["runtime_result"] = store.put_blob({"metadata": {"repository_changed": True, "evidence_changed": []}})
    with pytest.raises(resources.TrialResourceError, match="attestation differs"):
        dispose(completed)
    saved["runtime_result"] = store.put_blob({"metadata": row["metadata"]})
    packet = row["artifacts"]["packet_path"]
    (store.root / packet["path"]).write_bytes(b"A corrupt packet.")
    with pytest.raises(RecordError):
        dispose(completed)
    assert (trial / "case/repository").is_dir()


def test_wrong_repository_owner_refuses_disposal(completed, monkeypatch):
    _, _, _, trial = completed
    target = trial / "packager/repository"
    lstat = Path.lstat
    def ownership(path):
        info = lstat(path)
        if path == target:
            import os
            values = list(info)
            values[4] += 1
            return os.stat_result(values)
        return info
    monkeypatch.setattr(Path, "lstat", ownership)
    with pytest.raises(resources.TrialResourceError, match="unsafe directory"):
        dispose(completed)
    assert (trial / "case/repository").is_dir()


def test_disposal_start_keeps_its_exact_fresh_verification(completed, monkeypatch):
    real_remove = shutil.rmtree
    def interrupt(*_arguments, **_options):
        raise KeyboardInterrupt()
    interrupt.avoids_symlink_attacks = True
    monkeypatch.setattr(shutil, "rmtree", interrupt)
    with pytest.raises(KeyboardInterrupt):
        dispose(completed)
    store, _, _, _ = completed
    started = next(event for event in store.events() if event["kind"] == "trial_repository_disposal_started")
    proof = json.loads(store.read_blob(started["data"]["verification"]))
    assert proof["states"]["authority"]["clean"] is True
    assert proof["file_fingerprints"]["authority"]["fingerprint"] == started["data"]["expected_state"]["fingerprint"]
    monkeypatch.setattr(shutil, "rmtree", real_remove)
    dispose(completed)
    assert json.loads(store.read_blob(started["data"]["verification"])) == proof
