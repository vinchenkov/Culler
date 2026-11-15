import hashlib
from pathlib import Path

import pytest

from culler_evals.runtime_sources import (RuntimeSourceError, create_runtime_snapshot,
                                         runtime_source_manifest, runtime_source_hash,
                                         verify_runtime_sources)


def source_files(root: Path):
    (root / "runner/agent-runner").mkdir(parents=True)
    (root / "runner/agent-runner/main.ts").write_text("The exact runner source.\n")
    (root / "evals/bridge").mkdir(parents=True)
    (root / "evals/bridge/launch.ts").write_text("The exact evaluation bridge.\n")
    command = root / "evals/bridge/mc"
    command.write_text("#!/bin/sh\nexit 0\n")
    command.chmod(0o755)


def test_runtime_snapshot_preserves_exact_bytes_and_executable_identity(tmp_path):
    source = tmp_path / "checkout"
    source_files(source)
    excluded = source / "runner/node_modules/package"
    excluded.mkdir(parents=True)
    (excluded / "fixture.js").write_text("The image supplies dependencies.")
    manifest = runtime_source_manifest(source)
    assert len(manifest["files"]) == 3
    snapshot = create_runtime_snapshot(source, tmp_path / "experiment/runtime-source", manifest)
    assert verify_runtime_sources(snapshot, manifest) == runtime_source_hash(manifest)
    assert (snapshot / "evals/bridge/mc").stat().st_mode & 0o111
    assert not (snapshot / "evals/bridge/launch.ts").stat().st_mode & 0o222
    for row in manifest["files"]:
        assert hashlib.sha256((snapshot / row["path"]).read_bytes()).hexdigest() == row["sha256"]
    assert create_runtime_snapshot(source, snapshot, manifest) == snapshot


@pytest.mark.parametrize("change", ["bytes", "executable", "added", "removed"])
def test_runtime_source_drift_is_rejected_before_reuse(tmp_path, change):
    source = tmp_path / "checkout"
    source_files(source)
    manifest = runtime_source_manifest(source)
    snapshot = create_runtime_snapshot(source, tmp_path / "snapshot", manifest)
    target = source / "evals/bridge/launch.ts"
    if change == "bytes":
        target.write_text("A different runtime.")
    elif change == "executable":
        target.chmod(0o755)
    elif change == "added":
        (source / "evals/bridge/new.ts").write_text("A new import.")
    else:
        target.unlink()
    with pytest.raises(RuntimeSourceError, match="differs"):
        create_runtime_snapshot(source, snapshot, manifest)
    assert verify_runtime_sources(snapshot, manifest)


def test_snapshot_drift_and_unsafe_manifest_paths_are_rejected(tmp_path):
    source = tmp_path / "checkout"
    source_files(source)
    manifest = runtime_source_manifest(source)
    snapshot = create_runtime_snapshot(source, tmp_path / "snapshot", manifest)
    target = snapshot / "evals/bridge/launch.ts"
    target.chmod(0o644)
    target.write_text("A different mounted bridge.")
    with pytest.raises(RuntimeSourceError, match="differs"):
        create_runtime_snapshot(source, snapshot, manifest)
    row = manifest["files"][0] | {"path": "evals/bridge/../../private"}
    with pytest.raises(RuntimeSourceError, match="unsafe"):
        create_runtime_snapshot(source, tmp_path / "another", manifest | {"files": [row]})


def test_runtime_source_symlinks_cannot_escape_the_manifest(tmp_path):
    source = tmp_path / "checkout"
    source_files(source)
    (source / "evals/bridge/escape.ts").symlink_to(tmp_path / "private")
    with pytest.raises(RuntimeSourceError, match="symbolic link"):
        runtime_source_manifest(source)
