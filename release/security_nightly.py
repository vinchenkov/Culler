#!/usr/bin/env python3
"""Scan the exact images of the current release with fresh advisory data."""

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

from publish import GitHub


def scan(output, tools):
    output.mkdir(parents=True, exist_ok=True)
    api = GitHub(os.environ["GITHUB_REPOSITORY"], os.environ["GH_TOKEN"])
    release = api.request("GET", "/releases/latest")
    if release is None:
        raise ValueError("The nightly check requires a current release.")
    assets = [item for item in release["assets"] if item["name"] == "release.json"]
    if len(assets) != 1:
        raise ValueError("The current release requires one release record.")
    record = json.loads(api.request("GET", "/releases/assets/" + str(assets[0]["id"]), binary=True))
    commit = record["commit"]
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("The current release lacks its source commit.")
    status = 0
    for name, key in (("project", "project_image"), ("runner", "image")):
        reference = record[key]
        if not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+/" + name + r"@sha256:[0-9a-f]{64}", reference):
            raise ValueError("The current release image requires its registry digest.")
        archive = output / (name + ".tar")
        try:
            subprocess.run(["docker", "pull", reference], check=True)
            subprocess.run(["docker", "image", "save", reference, "-o", str(archive)], check=True)
            result = subprocess.run([sys.executable, str(Path(__file__).with_name("security_scan.py")), "image",
                                     "--archive", str(archive), "--image", name, "--commit", commit,
                                     "--tools", str(tools), "--report", str(output / (name + ".json")),
                                     "--vulnerabilities-only"])
            status = max(status, result.returncode)
            report = output / (name + ".json")
            if report.exists():
                data = json.loads(report.read_text())
                data["registry_digest"] = reference
                report.write_text(json.dumps(data, indent=2) + "\n")
        except (OSError, subprocess.SubprocessError):
            (output / (name + ".json")).write_text(json.dumps({"status": "unverified", "registry_digest": reference,
                "error": "The current release image could not be retrieved."}) + "\n")
            status = 2
        finally:
            archive.unlink(missing_ok=True)
    return status


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--tools", required=True, type=Path)
    args = parser.parse_args()
    try:
        sys.exit(scan(args.output, args.tools))
    except (ValueError, KeyError, OSError, RuntimeError):
        args.output.mkdir(parents=True, exist_ok=True)
        (args.output / "unverified.json").write_text('{"status":"unverified","error":"The current release record is unavailable or incomplete."}\n')
        sys.exit(2)
