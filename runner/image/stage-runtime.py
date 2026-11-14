#!/usr/bin/env python3
"""Copy the exact runner release into an image context."""
import re
import shutil
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[1]
target = Path(sys.argv[1])
manifest = (root.parent / "mc/deployment/runner_release.go").read_text()
block = manifest.split("var runnerReleaseFiles = []string{", 1)[1].split("\n}", 1)[0]
files = re.findall(r'^\s*"([^"]+)",', block, re.M)
if len(sys.argv) == 3 and sys.argv[2] == "--include-fake":
    files += ["fake-harness/cli.ts", "fake-harness/behavior.ts"]
elif len(sys.argv) != 2:
    raise SystemExit("The runtime source option is invalid.")
if not files:
    raise SystemExit("The runner release has no files.")
if target.exists():
    shutil.rmtree(target)
target.mkdir(parents=True)
for name in files:
    source = root / name
    if source.is_symlink() or not source.is_file():
        raise SystemExit("The runner release requires a regular file: " + name)
    destination = target / name
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
target.chmod(0o755)
for path in target.rglob("*"):
    path.chmod(0o755 if path.is_dir() else 0o644)
