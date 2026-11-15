"""Durable experiment records independent of the production spine."""

from __future__ import annotations

import dataclasses
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import threading
import uuid
from typing import Any


FORMAT_VERSION = 1


class RecordError(RuntimeError):
    """A required record is absent, corrupt, or unwritable."""


def json_value(value: Any) -> Any:
    if dataclasses.is_dataclass(value):
        return json_value(dataclasses.asdict(value))
    if isinstance(value, dict):
        return {str(key): json_value(item) for key, item in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [json_value(item) for item in value]
    if isinstance(value, Path):
        return str(value)
    if isinstance(value, BaseException):
        return {"type": type(value).__name__, "message": str(value)}
    return value


def canonical(value: Any) -> bytes:
    return json.dumps(json_value(value), sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode()


def content_hash(value: Any) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def _time() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="microseconds")


class RecordStore:
    """One local journal, content-addressed artifacts, and atomic manifests."""

    def __init__(self, root: Path | str, experiment_id: str | None = None, *, read_only: bool = False):
        self.root = Path(root).resolve()
        self.read_only = read_only
        self._backup = None
        if not read_only:
            self.root.mkdir(parents=True, exist_ok=True)
        self._root_descriptor = os.open(self.root, os.O_RDONLY) if not read_only else None
        self._root_identity = (self.root.stat().st_dev, self.root.stat().st_ino) if self.root.exists() else None
        self._lock = threading.RLock()
        self._failure: BaseException | None = None
        self._journal = self.root / "events.jsonl"
        self._writer = None
        if not read_only:
            self._writer = open(self.root / ".writer.lock", "a+b")
            try:
                fcntl.flock(self._writer.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
            except OSError as exc:
                self._writer.close()
                self.close()
                raise RecordError("Another process owns this experiment record store.") from exc
        try:
            self._events = self._read_events()
        except BaseException:
            self.close()
            raise
        ids = {event["experiment_id"] for event in self._events}
        if len(ids) > 1 or (ids and experiment_id is not None and experiment_id not in ids):
            self.close()
            raise RecordError("The experiment identifier does not match the journal.")
        self.experiment_id = next(iter(ids)) if ids else experiment_id or str(uuid.uuid4())
        if not read_only:
            for directory in ("blobs", "manifests", "native"):
                (self.root / directory).mkdir(exist_ok=True)
        try:
            storage = self.read_manifest("storage")
            if storage and not read_only:
                from .storage import durable_directory
                self._backup = durable_directory(storage["backup"])
        except BaseException:
            self.close()
            raise

    def enable_backup(self):
        from .storage import backup_directory, durable_directory
        durable_directory(self.root)
        if self._backup is None:
            backup = backup_directory(self.root)
            if backup.exists() and any(backup.iterdir()):
                raise RecordError("A backup already exists for this experiment path. Preserve that backup and select a new experiment directory.")
            self._backup = backup
            self.write_manifest("storage", {"version": 1, "primary": str(self.root), "backup": str(self._backup)})
        for folder in ("blobs", "manifests"):
            for path in (self.root / folder).iterdir():
                if path.is_file():
                    target = self._backup / path.relative_to(self.root)
                    if not target.exists() or target.read_bytes() != path.read_bytes():
                        self._mirror(path.relative_to(self.root), path.read_bytes())
        if self._journal.exists():
            self._mirror(Path("events.jsonl"), self._journal.read_bytes())
        return self._backup

    def _mirror_event(self, data):
        path = self._backup / "events.jsonl"
        expected_size = self._journal.stat().st_size - len(data)
        with open(path, "a+b") as output:
            os.chmod(path, 0o600)
            fcntl.flock(output.fileno(), fcntl.LOCK_EX)
            if output.seek(0, os.SEEK_END) != expected_size:
                raise RecordError("The experiment backup journal differs. Dependent work must stop.")
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        self._sync_directory(path.parent)

    def _mirror(self, relative, data):
        path = self._backup / relative
        temporary = path.with_name(path.name + "." + uuid.uuid4().hex + ".tmp")
        try:
            path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            with open(temporary, "xb") as output:
                os.chmod(temporary, 0o600)
                output.write(data)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, path)
            self._sync_directory(path.parent)
        except BaseException as error:
            self.fail(error)
            raise RecordError("Cannot preserve the experiment backup. Dependent work must stop.") from error
        finally:
            temporary.unlink(missing_ok=True)

    def close(self) -> None:
        if self._root_descriptor is not None:
            os.close(self._root_descriptor)
            self._root_descriptor = None
        if self._writer is not None and not self._writer.closed:
            fcntl.flock(self._writer.fileno(), fcntl.LOCK_UN)
            self._writer.close()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()

    def _read_events(self) -> list[dict]:
        if not self._journal.exists():
            return []
        events = []
        previous = None
        try:
            with open(self._journal, "rb") as source:
                fcntl.flock(source.fileno(), fcntl.LOCK_SH)
                raw = source.read()
            for line in raw.splitlines(keepends=True):
                if not line.endswith(b"\n"):
                    raise RecordError("The journal contains an incomplete event.")
                event = json.loads(line)
                expected = event.pop("hash")
                if event["version"] != FORMAT_VERSION or event["sequence"] != len(events) + 1:
                    raise RecordError("The journal version or event sequence is invalid.")
                if event["previous_hash"] != previous or content_hash(event) != expected:
                    raise RecordError("The journal hash chain is invalid.")
                event["hash"] = expected
                events.append(event)
                previous = expected
        except (ValueError, KeyError, OSError) as exc:
            raise RecordError("The journal cannot supply complete records.") from exc
        return events

    def barrier(self) -> None:
        if not self.read_only:
            try:
                if (self.root.stat().st_dev, self.root.stat().st_ino) != self._root_identity:
                    raise RecordError("The experiment directory changed. Dependent work must stop.")
            except OSError as error:
                self.fail(error)
        if self._failure is not None:
            raise RecordError("A required record write failed. Dependent work must stop.") from self._failure

    def fail(self, error: BaseException) -> None:
        self._failure = error

    def _atomic_write(self, path: Path, data: bytes) -> None:
        self.barrier()
        if self.read_only:
            raise RecordError("This record store permits only reads.")
        temporary = path.with_name(path.name + "." + uuid.uuid4().hex + ".tmp")
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            with open(temporary, "xb") as output:
                os.chmod(temporary, 0o600)
                output.write(data)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, path)
            self._sync_directory(path.parent)
            if self._backup is not None and path.is_relative_to(self.root):
                self._mirror(path.relative_to(self.root), data)
        except BaseException as exc:
            self.fail(exc)
            if isinstance(exc, RecordError):
                raise
            raise RecordError(f"Cannot preserve required artifact {path.name}.") from exc
        finally:
            temporary.unlink(missing_ok=True)

    @staticmethod
    def _sync_directory(path: Path) -> None:
        descriptor = os.open(path, os.O_RDONLY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)

    def append(self, kind: str, data: dict | None = None, **identity) -> dict:
        with self._lock:
            self.barrier()
            if self.read_only:
                raise RecordError("This record store permits only reads.")
            reserved = {"version", "sequence", "id", "timestamp", "experiment_id", "kind", "data", "hash", "previous_hash"}
            if reserved.intersection(identity):
                raise ValueError("Identity fields must not replace journal fields.")
            event = {
                "version": FORMAT_VERSION, "sequence": len(self._events) + 1,
                "id": f"event-{len(self._events) + 1}", "timestamp": _time(),
                "experiment_id": self.experiment_id, "kind": kind,
                "previous_hash": self._events[-1]["hash"] if self._events else None,
                **json_value(identity), "data": json_value(data or {}),
            }
            event["hash"] = content_hash(event)
            try:
                with open(self._journal, "ab") as output:
                    fcntl.flock(output.fileno(), fcntl.LOCK_EX)
                    os.chmod(self._journal, 0o600)
                    output.write(canonical(event) + b"\n")
                    output.flush()
                    os.fsync(output.fileno())
                self._sync_directory(self.root)
                if self._backup is not None:
                    self._mirror_event(canonical(event) + b"\n")
            except BaseException as exc:
                self.fail(exc)
                raise RecordError("Cannot preserve a required experiment event.") from exc
            self._events.append(event)
            return json_value(event)

    def events(self) -> list[dict]:
        with self._lock:
            if self.read_only:
                self._events = self._read_events()
            return json.loads(canonical(self._events))

    def begin_operation(self, kind: str, **identity) -> str:
        operation_id = identity.pop("operation_id", None) or "operation-" + uuid.uuid4().hex
        if any(event.get("operation_id") == operation_id for event in self._events):
            raise RecordError("An operation identifier already exists.")
        self.append("operation_started", {"operation_kind": kind}, operation_id=operation_id, **identity)
        return operation_id

    def end_operation(self, operation_id: str, outcome: str, result: Any = None, error: Any = None,
                      absence_reasons: dict | None = None, **identity) -> dict:
        if outcome not in {"success", "rejected", "failed", "interrupted", "skipped"}:
            raise ValueError("The operation outcome is invalid.")
        started = [event for event in self._events if event.get("operation_id") == operation_id]
        if not started or started[0]["kind"] != "operation_started" or any(
                event["kind"] == "operation_completed" for event in started):
            raise RecordError("The operation is absent or already complete.")
        owner = {key: value for key, value in started[0].items() if key in {
            "stage", "dataset", "iteration", "proposal_task_id", "candidate_id", "parent_ids", "case_id", "trial_id", "dependencies"
        }}
        return self.append("operation_completed", {"outcome": outcome, "result": result,
                           "error": error, "absence_reasons": absence_reasons or {}},
                           operation_id=operation_id, **(owner | identity))

    def put_blob(self, data: bytes | str | dict | list) -> dict:
        self.barrier()
        if self.read_only:
            raise RecordError("This record store permits only reads.")
        raw = data.encode() if isinstance(data, str) else data if isinstance(data, bytes) else canonical(data)
        digest = hashlib.sha256(raw).hexdigest()
        path = self.root / "blobs" / digest
        with self._lock:
            if not path.exists():
                self._atomic_write(path, raw)
            elif hashlib.sha256(path.read_bytes()).hexdigest() != digest:
                self.fail(RecordError("An artifact hash does not match its content."))
                self.barrier()
        return {"sha256": digest, "path": f"blobs/{digest}", "size": len(raw)}

    def read_blob(self, reference: dict) -> bytes:
        path = self.root / "blobs" / reference["sha256"]
        try:
            raw = path.read_bytes()
        except OSError as exc:
            raise RecordError("A required artifact is absent.") from exc
        if hashlib.sha256(raw).hexdigest() != reference["sha256"]:
            raise RecordError("A required artifact hash is invalid.")
        return raw

    @staticmethod
    def _manifest_name(name: str) -> str:
        if not name or any(character not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-" for character in name):
            raise ValueError("The manifest name must be a simple identifier.")
        return name + ".json"

    def write_manifest(self, name: str, data: Any) -> None:
        with self._lock:
            self._atomic_write(self.root / "manifests" / self._manifest_name(name), canonical(data))

    def read_manifest(self, name: str, default: Any = None) -> Any:
        path = self.root / "manifests" / self._manifest_name(name)
        return json.loads(path.read_bytes()) if path.exists() else default

    def candidates(self) -> dict[str, dict]:
        candidates = {}
        for event in self.events():
            if event["kind"] == "candidate_created":
                candidates[event["candidate_id"]] = {"candidate_id": event["candidate_id"], **event["data"]}
            if event["kind"] == "candidate_status" and event["candidate_id"] in candidates:
                candidates[event["candidate_id"]].update(event["data"])
        return candidates

    def completeness(self) -> dict:
        self.events()
        started = {event["operation_id"] for event in self._events if event["kind"] == "operation_started"}
        ended = {event["operation_id"] for event in self._events if event["kind"] == "operation_completed"}
        missing = sorted(started - ended)
        artifact_errors = []

        def check(value):
            if isinstance(value, dict):
                if {"sha256", "path", "size"}.issubset(value):
                    try:
                        self.read_blob(value)
                    except RecordError as exc:
                        artifact_errors.append(str(exc))
                for item in value.values():
                    check(item)
            elif isinstance(value, list):
                for item in value:
                    check(item)

        check(self._events)
        return {"complete": not missing and not artifact_errors and self._failure is None,
                "unfinished_operations": missing, "artifact_errors": artifact_errors,
                "record_failure": str(self._failure) if self._failure else None}

    def interrupt_unfinished(self, reason: str) -> list[str]:
        missing = self.completeness()["unfinished_operations"]
        for operation_id in missing:
            self.end_operation(operation_id, "interrupted", absence_reasons={"completion": reason})
        return missing
