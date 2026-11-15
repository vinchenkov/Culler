#!/usr/bin/env python3
"""Capture the scratch setup fixture through a real pseudo-terminal."""

import argparse
import codecs
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
import unicodedata


COLORS = {32: "#7bd88f", 33: "#f2c66d", 36: "#6bd5df", 90: "#a4acb9"}


class Screen:
    """Replay the cursor and color controls that the setup renderer emits."""

    def __init__(self, columns, rows=24):
        self.columns, self.rows = columns, rows
        self.lines = [self.blank() for _ in range(rows)]
        self.x = self.y = 0
        self.color, self.bold, self.dim = "#e7ecf3", False, False
        self.escape = ""

    def blank(self):
        return [(" ", "#e7ecf3", False, False)] * self.columns

    def feed(self, text):
        for char in text:
            if self.escape:
                self.escape += char
                if len(self.escape) == 2 and char != "[":
                    self.escape = ""
                elif len(self.escape) > 2 and "@" <= char <= "~":
                    self.control(self.escape[2:-1], char)
                    self.escape = ""
                continue
            if char == "\x1b":
                self.escape = char
            elif char == "\r":
                self.x = 0
            elif char == "\n":
                self.advance()
            elif char == "\b":
                self.x = max(0, self.x - 1)
            elif ord(char) >= 32:
                cells = 2 if unicodedata.east_asian_width(char) in ("W", "F") else 1
                if unicodedata.combining(char):
                    if self.x:
                        old = self.lines[self.y][self.x - 1]
                        self.lines[self.y][self.x - 1] = (old[0] + char, *old[1:])
                    continue
                if self.x + cells > self.columns:
                    self.x = 0
                    self.advance()
                self.lines[self.y][self.x] = (char, self.color, self.bold, self.dim)
                self.x += 1
                if cells == 2:
                    self.lines[self.y][self.x] = ("", self.color, self.bold, self.dim)
                    self.x += 1

    def advance(self):
        self.y += 1
        if self.y == self.rows:
            self.lines.pop(0)
            self.lines.append(self.blank())
            self.y -= 1

    def control(self, arguments, command):
        values = [int(value or "0") for value in arguments.split(";") if not value.startswith("?")]
        n = values[0] if values else 0
        if command == "A":
            self.y = max(0, self.y - (n or 1))
        elif command == "D":
            self.x = max(0, self.x - (n or 1))
        elif command == "J":
            if n == 2:
                self.lines = [self.blank() for _ in range(self.rows)]
            else:
                self.lines[self.y][self.x:] = self.blank()[self.x:]
                for row in range(self.y + 1, self.rows):
                    self.lines[row] = self.blank()
        elif command == "K":
            start = 0 if n == 2 else min(self.x, self.columns)
            self.lines[self.y][start:] = self.blank()[start:]
        elif command == "m":
            for value in values or [0]:
                if value == 0:
                    self.color, self.bold, self.dim = "#e7ecf3", False, False
                elif value == 1:
                    self.bold = True
                elif value == 2:
                    self.dim = True
                elif value in COLORS:
                    self.color = COLORS[value]

    def snapshot(self, name):
        rows = self.lines[:]
        while rows and not "".join(cell[0] for cell in rows[-1]).strip():
            rows.pop()
        if rows and "".join(cell[0] for cell in rows[-1]).strip() == "PASS":
            rows.pop()
        text = "\n".join("".join(cell[0] for cell in row).rstrip() for row in rows)
        body = []
        for row in rows:
            chunks = []
            for char, color, bold, dim in row:
                if not char:
                    continue
                style = f"color:{color};font-weight:{700 if bold else 400};opacity:{0.78 if dim else 1}"
                chunks.append(f'<span style="{style}">{html.escape(char)}</span>')
            body.append("".join(chunks))
        return {"name": name, "text": text, "html": "\n".join(body)}


def setup_terminal_session():
    """Give the fixture the terminal control of an interactive shell."""
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("scenario")
    parser.add_argument("--columns", type=int, default=80)
    parser.add_argument("--no-color", action="store_true")
    parser.add_argument("--dumb", action="store_true")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    module = Path(__file__).resolve().parents[2]
    go_paths = subprocess.check_output(
        ["mise", "exec", "--", "go", "env", "GOROOT", "GOMODCACHE", "GOCACHE"],
        cwd=module, text=True,
    ).splitlines()
    args.output.mkdir(parents=True, exist_ok=True)
    mode = "dumb" if args.dumb else "no-color" if args.no_color else "color"
    stem = args.output / f"{args.scenario}-{args.columns}-{mode}"
    screen = Screen(args.columns)
    raw, visible, snapshots = bytearray(), "", []
    decoder = codecs.getincrementaldecoder("utf-8")("replace")
    controls = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")

    with tempfile.TemporaryDirectory(prefix="mc-preview-") as scratch:
        plan_path = Path(scratch) / "plan.json"
        env = {key: os.environ[key] for key in ("PATH", "TMPDIR", "LANG", "LC_ALL", "MISE_DATA_DIR") if key in os.environ}
        env.update(PATH=str(Path(go_paths[0]) / "bin") + os.pathsep + env.get("PATH", ""),
                   GOMODCACHE=go_paths[1], GOCACHE=go_paths[2])
        env.update(HOME=scratch, TERM="dumb" if args.dumb else "xterm-256color",
                   MC_SETUP_PREVIEW_SCENARIO=args.scenario, MC_SETUP_PREVIEW_PLAN=str(plan_path))
        if args.no_color:
            env["NO_COLOR"] = "1"
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, args.columns, 0, 0))
        child = subprocess.Popen([str(args.binary.resolve()), "-test.run", "^TestTerminalSetupPreview$"],
                                 stdin=slave, stdout=slave, stderr=slave, env=env, cwd=module / "cmd" / "mc",
                                 preexec_fn=setup_terminal_session)
        os.close(slave)

        def read(timeout=0.05):
            nonlocal visible
            if select.select([master], [], [], timeout)[0]:
                try:
                    data = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        return False
                    raise
                if not data:
                    return False
                raw.extend(data)
                text = decoder.decode(data)
                screen.feed(text)
                visible = controls.sub("", raw.decode("utf-8", "replace")).replace("\r", "")
                return True
            return child.poll() is None

        try:
            deadline = time.monotonic() + 30
            plan = None
            while plan is None:
                if plan_path.exists():
                    try:
                        plan = json.loads(plan_path.read_text())
                        break
                    except json.JSONDecodeError:
                        pass
                if not read() or time.monotonic() > deadline:
                    raise RuntimeError("The preview child did not supply an input plan.")
            offset = 0
            for index, answer in enumerate(plan):
                deadline = time.monotonic() + 30
                expected = answer["wait"]
                while expected not in visible[offset:] and expected.replace("\n", "") not in visible[offset:].replace("\n", ""):
                    if not read() or time.monotonic() > deadline:
                        raise RuntimeError(f"The preview did not display {expected!r}.")
                drain_deadline = time.monotonic() + 0.15
                while time.monotonic() < drain_deadline and select.select([master], [], [], 0.05)[0]:
                    if not read(0):
                        break
                snapshots.append(screen.snapshot(answer.get("name", f"Choice {index + 1}")))
                offset = len(visible)
                if answer["send"]:
                    os.write(master, answer["send"].encode())
            deadline = time.monotonic() + 30
            while read():
                if time.monotonic() > deadline:
                    raise RuntimeError("The preview child did not finish.")
            if child.wait(timeout=3) != 0:
                raise RuntimeError("The preview fixture failed.")
            snapshots.append(screen.snapshot("Final frame"))
        finally:
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait()
            os.close(master)
            stem.with_suffix(".raw").write_bytes(raw)
            stem.with_suffix(".json").write_text(json.dumps(snapshots, ensure_ascii=False, indent=2))
            stem.with_suffix(".metadata.json").write_text(json.dumps({"command": [str(args.binary.resolve()), "-test.run", "^TestTerminalSetupPreview$"], "scenario":args.scenario, "columns":args.columns, "color_mode":mode, "process_exit_status":child.returncode, "fixture_success":child.returncode==0, "steps":plan}, indent=2))

    sections = "".join(f'<article><h2>{html.escape(frame["name"])}</h2><pre>{frame["html"]}</pre></article>' for frame in snapshots)
    page = f'''<!doctype html><meta charset="utf-8"><title>Setup terminal preview</title>
<style>body{{background:#0b0f15;color:#e7ecf3;font:15px system-ui;margin:32px}}h1{{font-size:20px}}h2{{font-size:14px;color:#b3bdcd}}article{{margin:28px 0}}pre{{display:inline-block;background:#151b25;padding:22px 26px;border:1px solid #303a49;border-radius:10px;font:14px/1.55 Menlo,Consolas,monospace;white-space:pre;margin:0}}</style>
<h1>{html.escape(args.scenario)} · {args.columns} columns · {mode}</h1>{sections}'''
    stem.with_suffix(".html").write_text(page)
    print(stem.with_suffix(".html"))


if __name__ == "__main__":
    main()
