#!/usr/bin/env python3
"""Capture the Culler command through a scratch pseudo-terminal."""

import argparse
import errno
import fcntl
import html
import json
import os
from pathlib import Path
import pty
import re
import select
import struct
import subprocess
import tempfile
import termios
import time

from preview_terminal import Screen, setup_terminal_session


def capture(binary, scenario, columns, color, output, redirected=False):
    mode = "color" if color else "no-color"
    stem = output / f"{scenario}-{columns}-{mode}{'-redirected' if redirected else ''}"
    with tempfile.TemporaryDirectory(prefix="culler-command-preview-") as scratch:
        module = Path(__file__).resolve().parents[2]
        paths = subprocess.check_output(["mise", "exec", "--", "go", "env", "GOROOT", "GOMODCACHE", "GOCACHE"], cwd=module, text=True).splitlines()
        env = {key: value for key, value in os.environ.items()
               if key in {"PATH", "TMPDIR", "LANG", "LC_ALL", "MISE_DATA_DIR"}}
        env.update(GOMODCACHE=paths[1], GOCACHE=paths[2])
        result_path = Path(scratch) / "result.json"
        env.update(HOME=scratch, TERM="xterm-256color", CULLER_TERMINAL_PREVIEW=scenario, CULLER_PREVIEW_RESULT=str(result_path), COLUMNS=str(columns))
        if not color:
            env["NO_COLOR"] = "1"
        command = [str(binary), "-test.run=^TestCullerTerminalPreview$"]
        raw = bytearray()
        if redirected:
            result = subprocess.run(command, stdin=subprocess.DEVNULL, capture_output=True,
                                    env=env, cwd=module / "cmd" / "mc", timeout=30)
            raw.extend(result.stdout + result.stderr)
            process_code = result.returncode
            stem.with_suffix(".stdout").write_bytes(result.stdout)
            stem.with_suffix(".stderr").write_bytes(result.stderr)
            if b"\x1b" in raw:
                raise RuntimeError("Redirected output contains terminal controls.")
        else:
            master, slave = pty.openpty()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, columns, 0, 0))
            child = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=slave, env=env, cwd=module / "cmd" / "mc",
                                     preexec_fn=setup_terminal_session)
            os.close(slave)
            deadline = time.monotonic() + 30
            try:
                while time.monotonic() < deadline:
                    if select.select([master], [], [], 0.05)[0]:
                        try:
                            data = os.read(master, 65536)
                        except OSError as error:
                            if error.errno == errno.EIO:
                                break
                            raise
                        if not data:
                            break
                        raw.extend(data)
                    elif child.poll() is not None:
                        break
                process_code = child.wait(timeout=2)
            finally:
                if child.poll() is None:
                    child.kill()
                    child.wait()
                os.close(master)
        stem.with_suffix(".raw").write_bytes(raw)
        if process_code != 0:
            raise RuntimeError("The terminal fixture failed: " + raw.decode("utf-8", "replace"))
        metadata = json.loads(result_path.read_text())
        if not metadata["fixture_success"]:
            raise RuntimeError("The command exit result differs from its contract.")
        text = raw.decode("utf-8", "replace")
        screen = Screen(columns, 160)
        screen.feed(text)
        frame = screen.snapshot(scenario)
        stem.with_suffix(".raw").write_bytes(raw)
        stem.with_suffix(".txt").write_text(re.sub(r"\x1b\[[0-9;?]*[A-Za-z]", "", text).replace("\r", ""))
        stem.with_suffix(".json").write_text(json.dumps({
            "command": command, "environment": {"CULLER_TERMINAL_PREVIEW": scenario,
            "TERM": env["TERM"], "NO_COLOR": env.get("NO_COLOR")}, "columns": columns,
            "frame": frame, "process_exit_status": process_code, "redirected": redirected, **metadata,
        }, indent=2))
        page = '<!doctype html><meta charset="utf-8"><title>Culler terminal</title>'
        page += '<style>body{background:#0b0f15;color:#e7ecf3;margin:32px;font:15px system-ui}pre{font:14px/1.55 Menlo,monospace;white-space:pre;background:#151b25;padding:24px;display:inline-block}</style>'
        page += f'<h1>{html.escape(scenario)} · {columns} columns · {mode}</h1><pre>{frame["html"]}</pre>'
        stem.with_suffix(".html").write_text(page)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--scenario", action="append")
    parser.add_argument("--redirected", action="store_true")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    for columns in (80, 40):
        for color in (True, False):
            for scenario in args.scenario or ("help", "help-start", "help-stop", "help-restart", "help-logs", "help-update", "help-status", "help-version", "usage", "logs", "logs-empty", "update-check", "update-check-wait", "update-check-cancel", "update-unknown", "status-cancel", "incomplete-setup", "version", "version-mismatch", "status-absent", "status-ready",
                             "status-stopped", "status-partial", "status-unknown", "start", "stop",
                             "restart", "restart-failure", "service-timeout", "wait-timeout", "cancel", "update-recovery"):
                capture(args.binary.resolve(), scenario, columns, color, args.output, args.redirected)
    print(args.output)


if __name__ == "__main__":
    main()
