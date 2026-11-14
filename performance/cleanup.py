#!/usr/bin/env python3
"""Remove the exact Docker resources of a command benchmark."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys


class Arguments(argparse.ArgumentParser):
    def error(self, message):
        raise ValueError(message)


def emit(value, stream=sys.stdout):
    print(json.dumps(value), file=stream, flush=True)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True, timeout=60)


def absent(result, kind, name):
    if result.returncode == 0:
        return False
    prefix = r"(?:Error(?: response from daemon)?:\s*)?"
    missing = rf"No such {kind}:?\s+{re.escape(name)}"
    if kind == "volume":
        missing = rf"(?:{missing}|get {re.escape(name)}: no such volume)"
    return re.fullmatch(prefix + missing, result.stderr.strip(), re.IGNORECASE) is not None


def inspect_resource(kind, name):
    result = docker(kind, "inspect", name)
    if result.returncode:
        if absent(result, kind, name):
            return None
        raise RuntimeError(f"Docker could not inspect the {kind}: {result.stderr.strip()}")
    objects = json.loads(result.stdout)
    if not isinstance(objects, list) or len(objects) != 1 or not isinstance(objects[0], dict):
        raise ValueError(f"Docker returned an invalid {kind} inspection.")
    return objects[0]


def verified_volume(name, config):
    actual = inspect_resource("volume", name)
    if actual is None:
        return None
    if not isinstance(config.get("volumeCreatedAt"), str) or not config["volumeCreatedAt"] or \
            config.get("volumeDriver") != "local":
        raise ValueError("The fixture record lacks the volume identity. Preserve the volume for inspection.")
    if actual.get("Name") != name or actual.get("Driver") != config["volumeDriver"] or \
            actual.get("CreatedAt") != config["volumeCreatedAt"] or actual.get("Options") not in (None, {}):
        raise ValueError("The volume identity differs from the fixture record. Preserve the volume for inspection.")
    return actual


def main():
    parser = Arguments(description=__doc__)
    parser.add_argument("--config", required=True, help="Configuration of the scratch fixture to remove.")
    args = parser.parse_args()
    config_path = Path(args.config).resolve(strict=True)
    config = json.loads(config_path.read_text())
    if not isinstance(config, dict):
        raise ValueError("The fixture configuration must contain a JSON object.")
    if config.get("resourcesVerifiedAbsent") is not True:
        raise ValueError("The fixture record lacks proof that its resource names were initially absent.")
    if not isinstance(config.get("mcHome"), str) or not isinstance(config.get("expectedImage"), str):
        raise ValueError("The fixture configuration needs mcHome and expectedImage values.")
    home = Path(config["mcHome"]).resolve(strict=True)
    if not home.is_relative_to(Path("/private/tmp/culler-performance")) or home.parent != config_path.parent:
        raise ValueError("The configuration must identify its own scratch home.")
    suffix = hashlib.sha256(("mc-deployment-v1\0" + str(home)).encode()).hexdigest()[:12]
    helper, volume = "mc-helper-" + suffix, "mc-spine-" + suffix
    if config.get("helper") != helper or config.get("volume") != volume:
        raise ValueError("The resource names differ from the scratch home identity.")
    emit({"type": "fixture-progress", "stage": "verify-resources"})
    verified_volume(volume, config)
    actual = inspect_resource("container", helper)
    if actual is not None:
        if not config.get("helperID") or actual.get("Id") != config["helperID"] or actual.get("Image") != config["expectedImage"]:
            raise ValueError("The helper identity differs from the fixture record.")
        mounts = actual.get("Mounts", [])
        if not isinstance(mounts, list) or len(mounts) != 1 or not isinstance(mounts[0], dict) or \
                mounts[0].get("Name") != volume or mounts[0].get("Destination") != "/mc/spine":
            raise ValueError("The helper mounts differ from the fixture record.")
        emit({"type": "fixture-progress", "stage": "remove-helper"})
        removed = docker("container", "rm", "--force", actual["Id"])
        if removed.returncode:
            raise RuntimeError(removed.stderr.strip())
    if verified_volume(volume, config) is not None:
        emit({"type": "fixture-progress", "stage": "remove-volume"})
        removed = docker("volume", "rm", volume)
        if removed.returncode and not absent(removed, "volume", volume):
            raise RuntimeError(removed.stderr.strip())
    if inspect_resource("container", helper) is not None or inspect_resource("volume", volume) is not None:
        raise RuntimeError("A fixture resource remains. The cleanup is incomplete.")
    emit({"type": "fixture-removed", "helper": helper, "volume": volume, "evidenceRetained": str(config_path.parent)})


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, KeyError, TypeError, subprocess.TimeoutExpired) as error:
        emit({"type": "error", "operation": "cleanup", "error": str(error)}, sys.stderr)
        raise SystemExit(1)
    except KeyboardInterrupt:
        emit({"type": "error", "operation": "cleanup", "error": "The cleanup stopped before completion."}, sys.stderr)
        raise SystemExit(130)
