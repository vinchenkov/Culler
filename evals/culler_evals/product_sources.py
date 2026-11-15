"""Frozen product sources for contract compilation and credential projection."""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import shutil
import tomllib


ROOTS = ("mc", "resident", "runner/shared")
FIXED_FILES = (".mise.toml", "evals/bridge/contracts.go", "evals/bridge/contracts-main.go",
               "evals/bridge/spine-parity_test.go", "evals/bridge/project-credentials.ts",
               "evals/bridge/resident-credentials.ts", "runner/agent-runner/adapters/provider-access.ts")
EXCLUDED = {"node_modules", "__pycache__", ".git", ".pytest_cache"}
COMPILE_ENVIRONMENT = {"GOWORK": "off", "GOENV": "off", "GOTOOLCHAIN": "local",
                       "CGO_ENABLED": "0", "GOFLAGS": "-mod=readonly -trimpath -buildvcs=false"}


class ProductSourceError(RuntimeError):
    """The product sources differ from the frozen manifest."""


def product_source_hash(manifest: dict) -> str:
    return hashlib.sha256(json.dumps(manifest, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()).hexdigest()


def product_source_manifest(source_root: Path) -> dict:
    source_root = Path(source_root).resolve()
    paths = []
    for name in ROOTS:
        root = source_root / name
        if not root.is_dir() or root.is_symlink():
            raise ProductSourceError("The product source requires its declared directories.")
        for directory, names, files in os.walk(root, followlinks=False):
            names[:] = sorted(name for name in names if name not in EXCLUDED)
            paths.extend(Path(directory) / name for name in files)
            if any((Path(directory) / name).is_symlink() for name in [*names, *files]):
                raise ProductSourceError("The product source cannot contain a symbolic link.")
    paths.extend(source_root / name for name in FIXED_FILES)
    rows = []
    for path in sorted(paths):
        if path.is_symlink() or not path.is_file():
            raise ProductSourceError("The product source requires regular files.")
        rows.append({"path": path.relative_to(source_root).as_posix(),
                     "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                     "executable": bool(path.stat().st_mode & 0o111)})
    try:
        go_version = tomllib.loads((source_root / ".mise.toml").read_text())["tools"]["go"]
    except (KeyError, ValueError, TypeError) as error:
        raise ProductSourceError("The product source requires its fixed Go toolchain.") from error
    if not isinstance(go_version, str) or not go_version:
        raise ProductSourceError("The product source requires its fixed Go toolchain.")
    architecture = {"aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine(), platform.machine())
    recipe = {"go_version": "go" + go_version, "target": "./eval-contracts",
              "environment": COMPILE_ENVIRONMENT | {"GOOS": platform.system().lower(), "GOARCH": architecture}}
    return {"version": 1, "roots": list(ROOTS), "fixed_files": list(FIXED_FILES),
            "compile_recipe": recipe, "files": sorted(rows, key=lambda row: row["path"])}


def _validate_manifest(manifest: dict) -> None:
    if (not isinstance(manifest, dict) or manifest.get("version") != 1 or manifest.get("roots") != list(ROOTS)
            or manifest.get("fixed_files") != list(FIXED_FILES) or not isinstance(manifest.get("compile_recipe"), dict)):
        raise ProductSourceError("The product source manifest is invalid.")
    rows = manifest.get("files")
    if not isinstance(rows, list) or not rows:
        raise ProductSourceError("The product source manifest requires files.")
    seen = set()
    for row in rows:
        if not isinstance(row, dict) or set(row) != {"path", "sha256", "executable"} or not isinstance(row["path"], str):
            raise ProductSourceError("The product source manifest contains an invalid file record.")
        name = row["path"]
        path = PurePosixPath(name)
        if path.is_absolute() or ".." in path.parts or str(path) != name or name in seen:
            raise ProductSourceError("The product source manifest contains an unsafe path.")
        if (name not in FIXED_FILES and not any(path.is_relative_to(root) for root in ROOTS)
                or any(part in EXCLUDED for part in path.parts)):
            raise ProductSourceError("The product source manifest contains an unsupported path.")
        checksum = row["sha256"]
        if (not isinstance(checksum, str) or len(checksum) != 64
                or any(char not in "0123456789abcdef" for char in checksum) or type(row["executable"]) is not bool):
            raise ProductSourceError("The product source manifest contains an invalid checksum or executable flag.")
        seen.add(name)


def verify_product_sources(source_root: Path, manifest: dict) -> str:
    _validate_manifest(manifest)
    try:
        current = product_source_manifest(source_root)
    except (ProductSourceError, OSError) as error:
        raise ProductSourceError("The product source changed after the experiment started. Restore the recorded source before continuation.") from error
    if current != manifest:
        raise ProductSourceError("The product source changed after the experiment started. Restore the recorded source before continuation.")
    return product_source_hash(manifest)


def create_product_snapshot(source_root: Path, destination: Path, manifest: dict) -> Path:
    source_root, destination = Path(source_root).resolve(), Path(destination).resolve()
    if destination.is_relative_to(source_root):
        raise ProductSourceError("The product snapshot must reside outside the source checkout.")
    verify_product_sources(source_root, manifest)
    if destination.exists():
        verify_product_sources(destination, manifest)
        return destination
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(destination.name + ".temporary")
    temporary.mkdir(mode=0o700)
    try:
        for row in manifest["files"]:
            path = temporary / row["path"]
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes((source_root / row["path"]).read_bytes())
            path.chmod(0o555 if row["executable"] else 0o444)
        verify_product_sources(source_root, manifest)
        verify_product_sources(temporary, manifest)
        for directory, _names, _files in os.walk(temporary):
            Path(directory).chmod(0o555)
        temporary.rename(destination)
    except BaseException:
        if temporary.exists():
            for directory, _names, _files in os.walk(temporary):
                Path(directory).chmod(0o700)
            shutil.rmtree(temporary)
        raise
    return destination
