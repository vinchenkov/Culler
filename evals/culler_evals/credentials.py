"""Host requests for scoped Culler provider access."""

from __future__ import annotations

import json
import os
from pathlib import Path
import select
import subprocess
import threading


class CredentialProjection:
    """One host bridge owns separate grants for concurrent evaluation sessions."""

    def __init__(self, source_root: Path, profile: str, binding: str, bridge_root: Path | None = None,
                 checkout_root: Path | None = None):
        self.source_root = Path(source_root).resolve()
        self.checkout_root = Path(checkout_root or source_root).resolve()
        self.bridge_root = Path(bridge_root) if bridge_root else self.source_root / "evals/bridge"
        self.profile = Path(profile).resolve()
        self.binding = binding
        self.process = None
        self.lock = threading.Lock()

    def __enter__(self):
        if self.profile.is_relative_to(self.source_root) or self.profile.is_relative_to(self.checkout_root):
            raise RuntimeError("The credential profile must reside outside the Culler checkout.")
        environment = os.environ.copy()
        environment["MISE_TRUSTED_CONFIG_PATHS"] = str(self.source_root / ".mise.toml")
        self.process = subprocess.Popen(["mise", "exec", "--", "bun", str(self.bridge_root / "project-credentials.ts"),
                                         str(self.profile), self.binding, str(self.source_root)], cwd=self.source_root,
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True,
                                        env=environment)
        self._read_response()
        return self

    def _read_response(self):
        ready, _, _ = select.select([self.process.stdout], [], [], 65)
        if not ready:
            self.__exit__(RuntimeError, None, None)
            raise RuntimeError("The host provider access bridge did not respond.")
        line = self.process.stdout.readline()
        try:
            response = json.loads(line)
            if response.get("status") != "ready":
                raise ValueError("The response is absent.")
            return response
        except (ValueError, TypeError):
            self.__exit__(RuntimeError, None, None)
            if isinstance(locals().get("response"), dict) and response.get("category") == "connection_required":
                raise RuntimeError("Save the credentials as a Culler connection. Replace the grant profile with a resident connection profile.") from None
            raise RuntimeError("The host provider access bridge refused the configured profile.") from None

    def _request(self, value):
        with self.lock:
            if self.process is None or self.process.poll() is not None:
                raise RuntimeError("The host provider access bridge ended before the trial.")
            self.process.stdin.write(json.dumps(value) + "\n")
            self.process.stdin.flush()
            return self._read_response()

    def current(self, model: str, duration_seconds: int = 1800):
        return self._request({"action": "project", "model": model, "duration_seconds": duration_seconds})["projection"]

    def release(self, projection: dict):
        return self._request({"action": "release", "grant_id": projection["grant_id"]})["provider_requests"]

    def __exit__(self, *_args):
        if self.process is None:
            return
        if self.process.stdin and not self.process.stdin.closed:
            self.process.stdin.close()
        try:
            self.process.wait(timeout=65)
        except subprocess.TimeoutExpired:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
        if self.process.stdout:
            self.process.stdout.close()
        if self.process.returncode and (not _args or _args[0] is None):
            raise RuntimeError("The host provider access bridge could not close its grants.")


class CredentialPool:
    """One bridge serves each profile and binding; each session receives a separate grant."""

    def __init__(self, source_root: Path, *, bridge_root=None, checkout_root: Path | None = None):
        self.source_root = source_root
        self.checkout_root = checkout_root
        self.bridge_root = bridge_root
        self.lock = threading.Lock()
        self.handles: dict[tuple[str, str], CredentialProjection] = {}

    def project(self, profile: str, binding: str, model: str, duration_seconds: int = 1800) -> dict:
        key = str(Path(profile).resolve()), binding
        with self.lock:
            handle = self.handles.get(key)
            source = self.source_root() if callable(self.source_root) else self.source_root
            if handle is None:
                bridge = self.bridge_root() if callable(self.bridge_root) else self.bridge_root
                handle = CredentialProjection(source, key[0], binding, bridge, self.checkout_root)
                handle.__enter__()
                self.handles[key] = handle
        return handle.current(model, duration_seconds)

    def release(self, profile: str, binding: str, projection: dict):
        key = str(Path(profile).resolve()), binding
        with self.lock:
            handle = self.handles[key]
        return handle.release(projection)

    def close(self):
        with self.lock:
            handles, self.handles = self.handles, {}
        failed = False
        for handle in handles.values():
            try:
                handle.__exit__(None, None, None)
            except RuntimeError:
                failed = True
        if failed:
            raise RuntimeError("The provider access pool could not close all grants.")
