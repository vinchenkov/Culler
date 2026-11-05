"""Capture terminal text without model calls."""
import os
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[3]
commands = [
    ("serve help", ["-m", "culler_evals.cli", "serve", "--help"]),
    ("run help", ["-m", "culler_evals.cli", "run", "--help"]),
    ("recover help", ["-m", "culler_evals.cli", "recover", "--help"]),
    ("temporary output rejection", ["-m", "culler_evals.cli", "run", "--protocol", "absent.json", "--output", "/private/tmp/culler-review-run", "--cache", "/private/tmp/culler-review-cache", "--no-browser"]),
    ("browser startup", ["-c", "from culler_evals.control_server import ControlServer; from culler_evals.cli import main; ControlServer.wait=lambda self: None; raise SystemExit(main())", "serve", "--protocol", "evals/config/packager.production-short.json", "--no-browser"]),
]
for width in (80, 40):
    for color in (True, False):
        env = os.environ.copy()
        env.update(PYTHONPATH=str(root / "evals"), COLUMNS=str(width), LINES="24", TERM="xterm-256color" if color else "dumb")
        if color:
            env.pop("NO_COLOR", None)
            env["FORCE_COLOR"]="1"
        else:
            env.pop("FORCE_COLOR", None)
            env["NO_COLOR"]="1"
        sections=[]
        for name, command in commands:
            result=subprocess.run([sys.executable,*command],cwd=root,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=40)
            sections.append(f"COMMAND: {name}\nEXIT: {result.returncode}\n{result.stdout}")
        target=Path(__file__).parent / f"terminal-{width}-{'color' if color else 'plain'}.txt"
        target.write_text("\n".join(sections))
        print(target.name)
