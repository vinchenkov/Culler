"""Dispose verified trial repositories after durable evidence preservation."""

from __future__ import annotations

import json
import hashlib
import os
from pathlib import Path
import re
import shutil
import stat
import threading


REPOSITORIES = {"authority": "case/repository", "packager": "packager/repository"}
LOCK = threading.RLock()


class TrialResourceError(RuntimeError):
    """The private trial directories cannot support safe disposal."""


def _refusal():
    return TrialResourceError("Trial repository disposal refused an unsafe directory. Restore the recorded private trial directories before continuation.")


def _identity(info) -> dict:
    return {"device": info.st_dev, "inode": info.st_ino, "owner": info.st_uid}


def _directory(path: Path) -> dict:
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid():
        raise _refusal()
    return _identity(info)


def _trial_path(store, trial_id: str) -> Path:
    if not isinstance(trial_id, str) or not re.fullmatch(r"trial-[0-9a-f]{32}", trial_id):
        raise _refusal()
    trials = store.root / "trials"
    _directory(trials)
    return trials / trial_id


def repository_fingerprint(repository: Path, excluded_root_names=(".git",)) -> str:
    _directory(repository)
    rows = []
    for directory, names, files in os.walk(repository, followlinks=False):
        names[:] = sorted(name for name in names if not (Path(directory) == repository and name in excluded_root_names))
        for name in [*names, *sorted(files)]:
            path = Path(directory) / name
            if Path(directory) == repository and name in excluded_root_names:
                continue
            info = path.lstat()
            row = {"path": path.relative_to(repository).as_posix()}
            if stat.S_ISLNK(info.st_mode):
                row.update(kind="link", target=os.readlink(path))
            elif stat.S_ISDIR(info.st_mode):
                row.update(kind="directory")
            elif stat.S_ISREG(info.st_mode):
                row.update(kind="file", sha256=hashlib.sha256(path.read_bytes()).hexdigest(), executable=bool(info.st_mode & 0o111))
            else:
                raise _refusal()
            rows.append(row)
    return hashlib.sha256(json.dumps(rows, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def capture_authority_state(store, trial_id: str, prepared: dict) -> dict | None:
    declared = prepared.get("repository_path")
    if declared is None:
        return None
    trial = _trial_path(store, trial_id)
    repository = trial / REPOSITORIES["authority"]
    if Path(os.path.abspath(declared)) != repository:
        raise _refusal()
    _directory(repository)
    _directory(repository / ".git")
    head_path = repository / ".git/HEAD"
    if head_path.is_symlink() or not head_path.is_file():
        raise _refusal()
    head = head_path.read_text().strip()
    commit = prepared.get("reviewed_commit")
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        return None
    branch = head[5:] if head.startswith("ref: refs/heads/") else "HEAD" if head == commit else None
    if branch is None:
        return None
    return {"commit": commit, "branch": branch, "fingerprint": repository_fingerprint(repository),
            "git_fingerprint": repository_fingerprint(repository / ".git", ("index",))}


def capture_repositories(store, trial_id: str, prepared: dict, expected_state: dict | None = None) -> dict:
    """Record only the two private repository directories owned by this trial."""
    trial = _trial_path(store, trial_id)
    record = {"version": 1, "trial": _directory(trial), "copies": [], "expected_state": expected_state}
    declared = prepared.get("repository_path")
    if declared is not None and Path(os.path.abspath(declared)) != trial / REPOSITORIES["authority"]:
        raise _refusal()
    for role, name in REPOSITORIES.items():
        path = trial / name
        if not path.exists() and not path.is_symlink():
            continue
        _directory(path.parent)
        record["copies"].append({"role": role, "path": name, "parent": _directory(path.parent),
                                "directory": _directory(path)})
    return record


def _verify_evidence(store, saved: dict, row: dict) -> None:
    for name in ("prepared", "runtime_result", "completed"):
        store.read_blob(saved[name])
    if json.loads(store.read_blob(saved["completed"])) != row:
        raise TrialResourceError("The completed trial differs from its preserved score. Restore the recorded trial evidence before continuation.")
    if json.loads(store.read_blob(saved["runtime_result"])).get("metadata") != row.get("metadata"):
        raise TrialResourceError("The trial source attestation differs from its preserved runtime result. Restore the recorded trial evidence before continuation.")
    for reference in row.get("artifacts", {}).values():
        if reference is not None:
            store.read_blob(reference)
    if row.get("judgment"):
        judgment = json.loads(store.read_blob(row["judgment"]))
        for reference in judgment.get("preserved_artifacts", {}).values():
            if reference is not None:
                store.read_blob(reference)


def _verify_previous_completion(store, saved: dict, row: dict, reference: dict) -> None:
    update = store.read_manifest("resume_update")
    original_slot = (update or {}).get("slots", {}).get("trial-slot-" + row.get("sample_slot", ""))
    if (not update or not update.get("applied") or not original_slot
            or reference != saved.get("previous_completed")):
        raise _refusal()
    original = json.loads(store.read_blob(original_slot))
    if (original.get("completed") != reference
            or any(original.get(name) != saved.get(name) for name in
                   ("repositories", "prepared", "runtime_result", "artifacts"))):
        raise _refusal()
    previous = json.loads(store.read_blob(reference))
    fixed = ("trial_id", "case_id", "stage", "sample_slot", "artifacts", "terminal", "metadata",
             "generation_external_attempt", "trial_number", "trial_count", "limitations")
    if any(previous.get(name) != row.get(name) for name in fixed):
        raise _refusal()
    _verify_evidence(store, saved | {"completed": reference}, previous)


def _verify_directories(trial: Path, record: dict, manifest: dict) -> None:
    if record.get("version") != 1 or not isinstance(record.get("copies"), list):
        raise _refusal()
    seen = set()
    for copy in record["copies"]:
        role = copy.get("role")
        if role not in REPOSITORIES or role in seen or copy.get("path") != REPOSITORIES[role]:
            raise _refusal()
        seen.add(role)
        path = trial / copy["path"]
        if _directory(path.parent) != copy["parent"]:
            raise _refusal()
        state = manifest["copies"].get(copy["path"])
        if not isinstance(state, dict) or state.get("status") not in ("pending", "disposed"):
            raise _refusal()
        try:
            actual = _directory(path)
        except FileNotFoundError:
            if not state.get("started"):
                raise _refusal()
        else:
            if actual != copy["directory"] or state["status"] == "disposed":
                raise _refusal()


def dispose_completed_repositories(store, saved: dict, row: dict, *, case_id: str, stage: str, probe=None,
                                   case_manifest: dict | None = None) -> dict | None:
    """Keep incomplete or changed copies; resume disposal of a completed sample."""
    with LOCK:
        if not saved.get("completed") or not saved.get("repositories", {}).get("copies"):
            return None
        _verify_evidence(store, saved, row)
        trial_id = row["trial_id"]
        name = "trial-repositories-" + trial_id
        record = saved["repositories"]
        manifest = store.read_manifest(name)
        metadata = row.get("metadata") or {}
        identity = {"trial_id": trial_id, "case_id": case_id, "stage": stage}
        if manifest and manifest.get("repositories") != record:
            raise _refusal()
        if manifest and manifest.get("completed") != saved["completed"]:
            _verify_previous_completion(store, saved, row, manifest.get("completed"))
        if manifest and manifest.get("status") == "retained":
            return manifest
        if metadata.get("repository_changed") is not False or metadata.get("evidence_changed") != []:
            reason = ("The role changed its repository or evidence." if metadata.get("repository_changed") is True
                      or metadata.get("evidence_changed") else "The trial lacks an explicit unchanged-repository attestation.")
            manifest = {"status": "retained", "reason": reason, "repositories": record,
                        "completed": saved["completed"], "case_manifest_hash": json.loads(store.read_blob(saved["prepared"]))["manifest_hash"]}
            store.write_manifest(name, manifest)
            store.append("trial_repositories_retained", manifest, **identity)
            return manifest
        if not getattr(shutil.rmtree, "avoids_symlink_attacks", False):
            raise _refusal()
        trial = _trial_path(store, trial_id)
        if _directory(trial) != record.get("trial"):
            raise _refusal()
        if not manifest:
            manifest = {"status": "pending", "repositories": record, "completed": saved["completed"],
                        "case_manifest_hash": json.loads(store.read_blob(saved["prepared"]))["manifest_hash"],
                        "case_manifest": store.put_blob(case_manifest or json.loads(store.read_blob(saved["prepared"]))),
                        "copies": {copy["path"]: {"status": "pending", "started": False} for copy in record["copies"]}}
            store.write_manifest(name, manifest)
        _verify_directories(trial, record, manifest)
        expected = record.get("expected_state")
        for copy in record["copies"]:
            state = manifest["copies"][copy["path"]]
            if state["started"]:
                if not state.get("verification") or not expected:
                    raise _refusal()
                proof = json.loads(store.read_blob(state["verification"]))
                observed = proof.get("states", {}).get(copy["role"], {})
                files = proof.get("file_fingerprints", {}).get(copy["role"], {})
                if (observed.get("commit") != expected["commit"] or observed.get("branch") != expected["branch"]
                        or observed.get("clean") is not True or files.get("fingerprint") != expected["fingerprint"]
                        or files.get("git_fingerprint") != expected["git_fingerprint"]
                        or metadata.get("image") and proof.get("image") != metadata["image"]):
                    raise _refusal()
        if manifest["status"] == "disposed":
            for copy in record["copies"]:
                path = trial / copy["path"]
                if path.exists() or path.is_symlink():
                    raise _refusal()
            return manifest
        unchecked = {copy["role"]: str(trial / copy["path"]) for copy in record["copies"]
                     if not manifest["copies"][copy["path"]]["started"]}
        if unchecked:
            fresh = None
            same_files = False
            fingerprints = {}
            try:
                fingerprints = {role: {"fingerprint": repository_fingerprint(Path(path)),
                                       "git_fingerprint": repository_fingerprint(Path(path) / ".git", ("index",))}
                                for role, path in unchecked.items()}
                same_files = bool(expected) and all(values["fingerprint"] == expected["fingerprint"]
                                                    and values["git_fingerprint"] == expected["git_fingerprint"]
                                                    for values in fingerprints.values())
            except (OSError, TrialResourceError):
                pass
            if probe is not None and same_files:
                try:
                    fresh = probe(unchecked)
                except (RuntimeError, OSError, TimeoutError):
                    fresh = None
            if isinstance(fresh, dict):
                fresh = {**fresh, "file_fingerprints": fingerprints}
            manifest["fresh_verification"] = store.put_blob(fresh) if fresh is not None else None
            states = fresh.get("states", {}) if isinstance(fresh, dict) else {}
            unchanged = same_files and all(
                isinstance(states.get(role), dict)
                and states.get(role, {}).get("commit") == expected["commit"]
                and states.get(role, {}).get("branch") == expected["branch"]
                and states.get(role, {}).get("clean") is True
                for role in unchecked)
            if not unchanged:
                manifest.update(status="retained", reason="The fresh source check finds a changed or unverifiable repository.")
                store.write_manifest(name, manifest)
                store.append("trial_repositories_retained", manifest, **identity)
                return manifest
            store.write_manifest(name, manifest)
        flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
        try:
            trials_fd = os.open(store.root / "trials", flags)
            try:
                trial_fd = os.open(trial_id, flags, dir_fd=trials_fd)
                try:
                    if _identity(os.fstat(trial_fd)) != record["trial"]:
                        raise _refusal()
                    for copy in record["copies"]:
                        role = copy.get("role")
                        if role not in REPOSITORIES or copy.get("path") != REPOSITORIES[role]:
                            raise _refusal()
                        parent, leaf = copy["path"].split("/")
                        parent_fd = os.open(parent, flags, dir_fd=trial_fd)
                        try:
                            if _identity(os.fstat(parent_fd)) != copy["parent"]:
                                raise _refusal()
                            state = manifest["copies"][copy["path"]]
                            try:
                                info = os.stat(leaf, dir_fd=parent_fd, follow_symlinks=False)
                            except FileNotFoundError:
                                if not state["started"]:
                                    raise _refusal()
                                observed = "absent_after_disposal_started"
                            else:
                                if (not stat.S_ISDIR(info.st_mode) or _identity(info) != copy["directory"]
                                        or state["status"] == "disposed"):
                                    raise _refusal()
                                if not state["started"]:
                                    state["started"] = True
                                    state["verification"] = manifest["fresh_verification"]
                                    store.write_manifest(name, manifest)
                                    store.append("trial_repository_disposal_started", {"path": copy["path"], "directory": copy["directory"],
                                                                                     "verification": state["verification"], "expected_state": expected}, **identity)
                                shutil.rmtree(leaf, dir_fd=parent_fd)
                                os.fsync(parent_fd)
                                observed = "removed"
                            if state["status"] != "disposed":
                                state.update(status="disposed", observation=observed)
                                store.write_manifest(name, manifest)
                                store.append("trial_repository_disposed", {"path": copy["path"], "observation": observed}, **identity)
                        finally:
                            os.close(parent_fd)
                finally:
                    os.close(trial_fd)
            finally:
                os.close(trials_fd)
        except (OSError, TrialResourceError) as error:
            store.append("trial_repository_disposal_failed", {"type": type(error).__name__, "reason": str(error)}, **identity)
            if isinstance(error, TrialResourceError):
                raise
            raise _refusal() from error
        manifest["status"] = "disposed"
        store.write_manifest(name, manifest)
        store.append("trial_repositories_disposed", {"paths": list(manifest["copies"]), "completed": saved["completed"],
                                                       "case_manifest_hash": manifest["case_manifest_hash"],
                                                       "case_manifest": manifest["case_manifest"]}, **identity)
        return manifest
