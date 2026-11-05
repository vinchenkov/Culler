"""Exact source files for the container runtime and the evaluation bridge."""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil


ROOTS = ("runner", "evals/bridge")
EXCLUDED = {"node_modules", "__pycache__", ".git", ".pytest_cache"}


class RuntimeSourceError(RuntimeError):
    """The runtime source differs from the frozen source manifest."""


def runtime_source_hash(manifest: dict) -> str:
    value = json.dumps(manifest, sort_keys=True, separators=(",", ":"), allow_nan=False)
    return hashlib.sha256(value.encode()).hexdigest()


def runtime_source_manifest(source_root: Path) -> dict:
    source_root = Path(source_root).resolve()
    files = []
    for root_name in ROOTS:
        root = source_root / root_name
        if not root.is_dir() or root.is_symlink():
            raise RuntimeSourceError("The runtime source requires both source directories.")
        for directory, names, filenames in os.walk(root, followlinks=False):
            names[:] = sorted(name for name in names if name not in EXCLUDED)
            for name in [*names, *filenames]:
                if (Path(directory) / name).is_symlink():
                    raise RuntimeSourceError("The runtime source cannot contain a symbolic link.")
            for name in sorted(filenames):
                path = Path(directory) / name
                if not path.is_file():
                    raise RuntimeSourceError("The runtime source requires regular files.")
                files.append({"path": path.relative_to(source_root).as_posix(),
                              "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                              "executable": bool(path.stat().st_mode & 0o111)})
    return {"version": 1, "roots": list(ROOTS), "files": sorted(files, key=lambda row: row["path"])}


def _validate_manifest(manifest: dict) -> None:
    if not isinstance(manifest, dict) or manifest.get("version") != 1 or manifest.get("roots") != list(ROOTS):
        raise RuntimeSourceError("The runtime source manifest is invalid.")
    rows = manifest.get("files")
    if not isinstance(rows, list) or not rows:
        raise RuntimeSourceError("The runtime source manifest requires files.")
    seen = set()
    for row in rows:
        if not isinstance(row, dict) or set(row) != {"path", "sha256", "executable"}:
            raise RuntimeSourceError("The runtime source manifest contains an invalid file record.")
        name = row["path"]
        if not isinstance(name, str):
            raise RuntimeSourceError("The runtime source manifest contains an invalid path.")
        path = PurePosixPath(name)
        if path.is_absolute() or ".." in path.parts or str(path) != name or name in seen:
            raise RuntimeSourceError("The runtime source manifest contains an unsafe path.")
        if not any(path.is_relative_to(root) for root in ROOTS) or any(part in EXCLUDED for part in path.parts):
            raise RuntimeSourceError("The runtime source manifest contains an unsupported path.")
        checksum = row["sha256"]
        if not isinstance(checksum, str) or len(checksum) != 64 or any(char not in "0123456789abcdef" for char in checksum):
            raise RuntimeSourceError("The runtime source manifest contains an invalid checksum.")
        if type(row["executable"]) is not bool:
            raise RuntimeSourceError("The runtime source manifest requires an executable flag.")
        seen.add(name)


def verify_runtime_sources(source_root: Path, expected_manifest: dict) -> str:
    _validate_manifest(expected_manifest)
    if runtime_source_manifest(source_root) != expected_manifest:
        raise RuntimeSourceError("The runtime source differs from the frozen source manifest.")
    return runtime_source_hash(expected_manifest)


def create_runtime_snapshot(source_root: Path, destination: Path, expected_manifest: dict) -> Path:
    source_root, destination = Path(source_root).resolve(), Path(destination).resolve()
    if destination.is_relative_to(source_root):
        raise RuntimeSourceError("The runtime snapshot must reside outside the source checkout.")
    verify_runtime_sources(source_root, expected_manifest)
    if destination.exists():
        verify_runtime_sources(destination, expected_manifest)
        return destination
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(destination.name + ".temporary")
    temporary.mkdir(mode=0o700)
    try:
        for row in expected_manifest["files"]:
            path = temporary / row["path"]
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes((source_root / row["path"]).read_bytes())
            path.chmod(0o555 if row["executable"] else 0o444)
        verify_runtime_sources(source_root, expected_manifest)
        verify_runtime_sources(temporary, expected_manifest)
        for root, _names, _files in os.walk(temporary):
            Path(root).chmod(0o555)
        temporary.rename(destination)
    except BaseException:
        if temporary.exists():
            for root, _names, _files in os.walk(temporary):
                Path(root).chmod(0o700)
            shutil.rmtree(temporary)
        raise
    return destination
