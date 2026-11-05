"""Persistent experiment locations and exact source archives."""

from __future__ import annotations

import hashlib
import io
import os
from pathlib import Path, PurePosixPath
import subprocess
import tarfile
import tempfile


DATA_HOME = Path.home() / ".local/share/culler-evals"
DEFAULT_EXPERIMENTS = DATA_HOME / "experiments"
DEFAULT_CACHE = DATA_HOME / "cache"
WORKTREE_ROOT = Path("/private/tmp/culler-worktrees")


def durable_directory(path):
    resolved = Path(path).expanduser().resolve()
    temporary = {Path(name).resolve() for name in ("/tmp", "/private/tmp", "/var/tmp", tempfile.gettempdir())}
    if os.environ.get("TMPDIR"):
        temporary.add(Path(os.environ["TMPDIR"]).resolve())
    if any(resolved == root or resolved.is_relative_to(root) for root in temporary):
        raise ValueError("Experiment records require a persistent directory. Use ~/.local/share/culler-evals/experiments instead of a temporary directory.")
    return resolved


def preserve_source(store, protocol, source):
    from .continuation import verify_source
    source = Path(source).resolve()
    verify_source(protocol, source)
    saved = store.read_manifest("source_archive")
    if saved and saved["protocol_hash"] == protocol["protocol_hash"]:
        store.read_blob(saved["archive"])
        store.read_blob(saved["bundle"])
        return
    names = set(subprocess.check_output(["git", "ls-files", "-z"], cwd=source).decode().split("\0")) - {""}
    names.update(protocol["framework_source_hashes"])
    for manifest in (protocol["product_source_manifest"], protocol["runtime_source_manifest"]):
        names.update(row["path"] for row in manifest["files"])
    for cases in (store.read_manifest("case_corpus") or {}).values():
        for case in cases:
            if not case.get("_manifest_path"):
                continue
            manifest = Path(case["_manifest_path"]).resolve()
            references = [manifest]
            local = [artifact["source"] for artifact in case.get("artifacts", [])]
            local += [case.get("source", {}).get("bundle", {})]
            local += [case.get("references", {}).get("review_evidence", {})]
            for reference in local:
                if reference.get("path") and reference.get("kind", "file") == "file":
                    references.append((manifest.parent / reference["path"]).resolve())
            for path in references:
                if path.is_relative_to(source):
                    names.add(path.relative_to(source).as_posix())
                else:
                    durable_directory(path)
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        for name in sorted(names):
            path = source / name
            if path.is_file() and not path.is_symlink():
                archive.add(path, arcname=name, recursive=False)
    with tempfile.TemporaryDirectory(prefix="culler-eval-source-") as temporary:
        bundle = Path(temporary) / "source.bundle"
        subprocess.run(["git", "bundle", "create", str(bundle), "HEAD"], cwd=source,
                       check=True, capture_output=True)
        reference = store.put_blob(bundle.read_bytes())
    verify_source(protocol, source)
    store.write_manifest("source_archive", {"version": 1, "source": str(source),
        "protocol_hash": protocol["protocol_hash"], "archive": store.put_blob(buffer.getvalue()), "bundle": reference})


def restore_source(store, protocol, current):
    saved = store.read_manifest("source_archive")
    if not saved or saved["protocol_hash"] != protocol["protocol_hash"]:
        raise ValueError("The recorded source archive is unavailable. Restore the original source before resume.")
    destination = Path(saved["source"])
    parent = WORKTREE_ROOT.resolve()
    if destination.parent.resolve() != parent or destination.is_symlink() or destination.exists():
        raise ValueError("Automatic source recovery requires an absent task worktree. The existing checkout remains unchanged.")
    raw = store.read_blob(saved["archive"])
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:gz") as archive:
        members = archive.getmembers()
        for member in members:
            path = PurePosixPath(member.name)
            if (not member.isfile() or path.is_absolute() or ".." in path.parts
                    or ".git" in path.parts or str(path) != member.name):
                raise ValueError("The source archive contains an unsafe file.")
        with tempfile.TemporaryDirectory(prefix="culler-eval-recover-") as temporary:
            bundle = Path(temporary) / "source.bundle"
            bundle.write_bytes(store.read_blob(saved["bundle"]))
            subprocess.run(["git", "fetch", str(bundle), "HEAD"], cwd=current, check=True, capture_output=True)
        destination.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "worktree", "add", "--detach", "--force", str(destination), protocol["source_revision"]],
                       cwd=current, check=True, capture_output=True)
        for member in members:
            target = destination / member.name
            if target.is_symlink() or target.resolve() != target:
                raise ValueError("The recovered source contains a symbolic link.")
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(archive.extractfile(member).read())
            target.chmod(0o755 if member.mode & 0o111 else 0o644)
    from .continuation import verify_source
    verify_source(protocol, destination)
    return destination


def backup_directory(root):
    identity = hashlib.sha256(str(Path(root).resolve()).encode()).hexdigest()[:20]
    backup = durable_directory(DATA_HOME / "backups" / identity)
    primary = durable_directory(root)
    if backup == primary or backup.is_relative_to(primary) or primary.is_relative_to(backup):
        raise ValueError("The backup requires a separate directory outside the experiment records.")
    return backup


def restore_records(backup):
    import shutil
    from .records import RecordStore
    backup = durable_directory(backup)
    with RecordStore(backup, read_only=True) as store:
        storage = store.read_manifest("storage")
        if not storage or Path(storage["backup"]).resolve() != backup:
            raise ValueError("Select a complete experiment backup directory.")
        destination = durable_directory(storage["primary"])
        if destination.exists() or destination.is_symlink():
            raise ValueError("The experiment directory exists. Recovery preserves it without replacement.")
        errors = store.completeness()["artifact_errors"]
        if errors:
            raise ValueError("The backup contains absent or invalid artifacts. Recovery stopped.")
        for path in (backup / "blobs").iterdir():
            store.read_blob({"sha256": path.name})
        destination.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix=".recover-", dir=destination.parent) as temporary:
            staged = Path(temporary) / "records"
            staged.mkdir(mode=0o700)
            for name in ("blobs", "manifests"):
                shutil.copytree(backup / name, staged / name)
            if (backup / "events.jsonl").exists():
                shutil.copy2(backup / "events.jsonl", staged / "events.jsonl")
            staged.rename(destination)
    with RecordStore(destination) as recovered:
        recovered.write_manifest("record_recovery", {"backup": str(backup), "scratch_repositories_retained": False})
        recovered.append("records_recovered", {"backup": str(backup)}, stage="preparation")
    return destination
