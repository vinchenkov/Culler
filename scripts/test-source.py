#!/usr/bin/env python3
"""Copy the current source files into a temporary test repository."""
import shutil
import subprocess
import sys
from pathlib import Path

source = Path(sys.argv[1]).resolve()
target = Path(sys.argv[2]).resolve()
target.mkdir()
names = subprocess.check_output([
    "git", "-C", str(source), "ls-files", "--cached", "--others", "--exclude-standard", "-z",
])
for name in names.decode().split("\0"):
    if not name:
        continue
    if not (source / name).exists() and not (source / name).is_symlink():
        continue
    destination = target / name
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source / name, destination, follow_symlinks=False)

subprocess.run(["git", "init", "-q", "-b", "main", str(target)], check=True)
subprocess.run(["git", "-C", str(target), "add", "."], check=True)
subprocess.run([
    "git", "-C", str(target), "-c", "core.hooksPath=/dev/null",
    "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
    "-c", "commit.gpgsign=false", "commit", "-qm", "Test source",
], check=True)
