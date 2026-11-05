"""Private command state for one Packager trial."""

from __future__ import annotations

import datetime as dt
import hmac
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import secrets
import stat
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


class CommandRefusal(Exception):
    def __init__(self, message: str, exit_code: int = 1):
        super().__init__(message)
        self.exit_code = exit_code


class CommandAdapter:
    """The adapter owns terminal state. The role cannot rewrite that state."""

    def __init__(self, case: dict, repository: Path, artifacts: Path, run_id: str, root: Path):
        self.case = case
        self.repository = Path(repository).resolve()
        self.artifacts = Path(artifacts).resolve()
        self.run_id = run_id
        self.task_id = str(case["task_id"])
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=True)
        self.log_path = self.root / "commands.jsonl"
        self.terminal_path = self.root / "terminal.json"
        self.terminal: dict | None = None
        self.lock = threading.Lock()
        self.responses = case.get("command_responses", {})
        self.planes = [f"/mc/artifacts/{entry.name}" for entry in sorted(self.artifacts.iterdir()) if entry.is_dir()]
        self.start_commit = case.get("start_commit", case["reviewed_commit"])
        self.git_runner = None
        self._git("cat-file", "-e", case["base_commit"] + "^{commit}")
        self._git("cat-file", "-e", case["reviewed_commit"] + "^{commit}")
        self.start_branch = self._git("rev-parse", "--symbolic-full-name", "HEAD")

    def _git(self, *args: str) -> str:
        result = self.git_runner(args) if self.git_runner is not None else subprocess.run(["git", "-C", str(self.repository), *args], capture_output=True, text=True)
        if result.returncode:
            raise CommandRefusal("The repository check failed: " + result.stderr.strip(), 2)
        return result.stdout.strip()

    @staticmethod
    def _flags(argv: list[str], allowed: set[str], booleans: set[str] = frozenset()) -> dict:
        flags: dict[str, Any] = {}
        index = 0
        while index < len(argv):
            name = argv[index]
            if name not in allowed or name in flags:
                raise CommandRefusal("The command contains an unsupported or repeated flag.", 2)
            if name in booleans:
                flags[name] = True
                index += 1
            else:
                if index + 1 >= len(argv) or argv[index + 1].startswith("--"):
                    raise CommandRefusal("The command flag requires a value.", 2)
                flags[name] = argv[index + 1]
                index += 2
        return flags

    def _subject(self, value: str) -> None:
        if value != self.task_id:
            raise CommandRefusal("The task is not the run's assigned task.")

    def _run(self, value: str | None) -> None:
        if value != self.run_id:
            raise CommandRefusal("The run identifier does not match this run.")

    def _artifact(self, value: str) -> Path:
        path = PurePosixPath(value)
        clean = os.path.normpath(value)
        if not path.is_absolute() or clean != value:
            raise CommandRefusal("The packet path must be an absolute path inside a mounted artifact plane.")
        if not any(value.startswith(plane + "/") for plane in self.planes):
            raise CommandRefusal("Write the packet inside a mounted artifact plane, then resubmit the terminal.")
        relative = path.relative_to("/mc/artifacts")
        target = self.artifacts.joinpath(*relative.parts)
        try:
            info = target.lstat()
        except OSError:
            raise CommandRefusal("The packet file is absent. Write the file, then resubmit the terminal.") from None
        if not stat.S_ISREG(info.st_mode):
            raise CommandRefusal("The packet path must name a regular file.")
        plane_root = self.artifacts / relative.parts[0]
        if not target.resolve().is_relative_to(plane_root.resolve()):
            raise CommandRefusal("The packet path resolves outside its mounted artifact plane.")
        if info.st_size == 0:
            raise CommandRefusal("The packet file is empty. Write the packet, then resubmit the terminal.")
        if info.st_size > 64 << 20:
            raise CommandRefusal("The packet file exceeds the 64 MiB limit.")
        return target

    def _packaged(self, flags: dict) -> dict:
        if set(flags) != {"--run", "--status", "--outputs", "--thesis"} or flags["--status"] != "packaged":
            raise CommandRefusal("The packaged terminal requires --run, --status packaged, --outputs, and --thesis.", 2)
        summary = flags["--thesis"]
        if not summary.strip() or len(summary.encode("utf-8")) > 4096:
            raise CommandRefusal("The summary must contain prose within the 4096-byte limit.", 2)
        if any((ord(char) < 32 and char != "\n") or ord(char) == 127 for char in summary):
            raise CommandRefusal("The summary must contain no control characters.", 2)
        target = self._artifact(flags["--outputs"])
        if self._git("rev-parse", "HEAD") != self.start_commit:
            raise CommandRefusal("HEAD differs from the start commit. Restore the repository, then resubmit the terminal.")
        if self._git("rev-parse", "--symbolic-full-name", "HEAD") != self.start_branch:
            raise CommandRefusal("The repository branch differs from its start state. Restore the original branch state, then resubmit the terminal.")
        if self._git("status", "--porcelain=v1", "--untracked-files=all"):
            raise CommandRefusal("The repository contains changed files. Restore the repository, then resubmit the terminal.")
        snapshot = self._snapshot(target)
        return {"kind": "packaged", "task_id": self.task_id, "run_id": self.run_id,
                "outputs": flags["--outputs"], "packet_path": str(snapshot), "summary": summary,
                "packet_sha256": hashlib.sha256(snapshot.read_bytes()).hexdigest()}

    def _snapshot(self, target: Path) -> Path:
        relative = target.relative_to(self.artifacts)
        descriptor = os.open(self.artifacts, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            for part in relative.parts[:-1]:
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=descriptor)
                os.close(descriptor)
                descriptor = child
            leaf = os.open(relative.parts[-1], os.O_RDONLY | os.O_NOFOLLOW, dir_fd=descriptor)
            with os.fdopen(leaf, "rb") as source:
                if not stat.S_ISREG(os.fstat(source.fileno()).st_mode):
                    raise CommandRefusal("The packet path must name a regular file.")
                body = source.read((64 << 20) + 1)
            if not body or len(body) > 64 << 20:
                raise CommandRefusal("The packet must contain content within the 64 MiB limit.")
        except OSError:
            raise CommandRefusal("The packet path changed during the terminal. Restore the file, then resubmit.") from None
        finally:
            os.close(descriptor)
        accepted = self.root / "accepted"
        accepted.mkdir(mode=0o700, exist_ok=True)
        snapshot = accepted / target.name
        snapshot.write_bytes(body)
        return snapshot

    def _dispatch(self, argv: list[str]) -> dict:
        if self.terminal is not None:
            raise CommandRefusal("This run already has a successful terminal result.")
        if len(argv) >= 2 and argv[0] == "complete":
            self._subject(argv[1])
            flags = self._flags(argv[2:], {"--run", "--status", "--outputs", "--thesis", "--infra", "--needs-operator", "--reason"}, {"--infra", "--needs-operator"})
            self._run(flags.get("--run"))
            if "--status" in flags:
                result = self._packaged(flags)
            else:
                terminals = [flag for flag in ("--infra", "--needs-operator") if flags.get(flag)]
                if len(terminals) != 1 or set(flags) != {"--run", terminals[0], "--reason"}:
                    raise CommandRefusal("The failure terminal requires one event, --run, and --reason.", 2)
                if not flags["--reason"].strip():
                    raise CommandRefusal("The failure terminal requires a reason.", 2)
                result = {"kind": "infra" if terminals[0] == "--infra" else "needs_operator",
                          "task_id": self.task_id, "run_id": self.run_id, "reason": flags["--reason"]}
            self.terminal = result
            self.terminal_path.write_text(json.dumps(result, indent=2) + "\n")
            return {"task_id": int(self.task_id), "status": result["kind"]}
        if argv[:2] == ["task", "block"] and len(argv) >= 3:
            self._subject(argv[2])
            flags = self._flags(argv[3:], {"--reason"})
            if not flags.get("--reason", "").strip():
                raise CommandRefusal("The block command requires a reason.", 2)
            self.terminal = {"kind": "needs_operator", "task_id": self.task_id, "run_id": self.run_id, "reason": flags["--reason"]}
            self.terminal_path.write_text(json.dumps(self.terminal, indent=2) + "\n")
            return {"task_id": int(self.task_id), "blocked": True}
        if argv[:2] == ["run", "infra-failure"] and len(argv) >= 3:
            self._run(argv[2])
            flags = self._flags(argv[3:], {"--condition", "--cause", "--evidence", "--operator-action", "--quota-reset-at"})
            if not flags.get("--condition", "").strip():
                raise CommandRefusal("The infrastructure report requires an observed condition.", 2)
            cause = flags.get("--cause")
            if cause not in (None, "credentials", "quota"):
                raise CommandRefusal("The infrastructure report contains an unsupported cause.", 2)
            if cause and not flags.get("--evidence"):
                raise CommandRefusal("The cause requires service evidence.", 2)
            if cause == "credentials" and not flags.get("--operator-action"):
                raise CommandRefusal("A credential report requires the operator action.", 2)
            if "--quota-reset-at" in flags:
                if cause != "quota":
                    raise CommandRefusal("A reset time requires the quota cause.", 2)
                try:
                    dt.datetime.fromisoformat(flags["--quota-reset-at"].replace("Z", "+00:00"))
                except ValueError:
                    raise CommandRefusal("The reset time must use RFC 3339.", 2) from None
            self.terminal = {"kind": "infra", "task_id": self.task_id, "run_id": self.run_id,
                             "reason": flags["--condition"], "report": flags}
            self.terminal_path.write_text(json.dumps(self.terminal, indent=2) + "\n")
            return {"run_id": self.run_id, "outcome": "infra-failed"}
        read = (argv[:2] in (["task", "get"], ["task", "list"], ["packet", "list"], ["activity", "list"], ["project", "list"]))
        if not read:
            raise CommandRefusal("This command is outside the Packager command surface.", 2)
        if argv[:2] == ["task", "get"]:
            if len(argv) != 3 or not argv[2].isdigit():
                raise CommandRefusal("The task read requires one numeric identifier.", 2)
        elif argv[:2] == ["activity", "list"]:
            self._flags(argv[2:], {"--kind", "--limit"})
        elif len(argv) != 2:
            raise CommandRefusal("This read accepts no flags.", 2)
        key = " ".join(argv)
        if key not in self.responses:
            raise CommandRefusal("The fixed case supplies no response for this permitted read.")
        return self.responses[key]

    def invoke(self, argv: list[str]) -> dict:
        with self.lock:
            timestamp = dt.datetime.now(dt.timezone.utc).isoformat()
            try:
                if not isinstance(argv, list) or not all(isinstance(arg, str) for arg in argv):
                    raise CommandRefusal("The command arguments must be text.", 2)
                response = {"exit_code": 0, "stdout": self._dispatch(argv), "stderr": ""}
            except CommandRefusal as error:
                response = {"exit_code": error.exit_code, "stdout": None, "stderr": str(error)}
            record = {"at": timestamp, "argv": argv, **response}
            with self.log_path.open("a") as stream:
                stream.write(json.dumps(record) + "\n")
                stream.flush()
                os.fsync(stream.fileno())
            return response


class CommandServer:
    def __init__(self, adapter: CommandAdapter):
        self.adapter = adapter
        self.token = secrets.token_urlsafe(32)
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                return

            def do_POST(self):
                if self.path != "/command" or not hmac.compare_digest(self.headers.get("Authorization", ""), "Bearer " + owner.token):
                    self.send_error(403)
                    return
                length = int(self.headers.get("Content-Length", "0"))
                if length <= 0 or length > 1 << 20:
                    self.send_error(413)
                    return
                try:
                    argv = json.loads(self.rfile.read(length))["argv"]
                    result = owner.adapter.invoke(argv)
                except (ValueError, KeyError):
                    self.send_error(400)
                    return
                data = json.dumps(result).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        self.server = ThreadingHTTPServer(("0.0.0.0", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self):
        self.thread.start()
        return self

    @property
    def url(self):
        return f"http://host.docker.internal:{self.server.server_port}/command"

    def __exit__(self, *_args):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
