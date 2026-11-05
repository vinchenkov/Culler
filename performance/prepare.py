#!/usr/bin/env python3
"""Create an isolated fixture for the Mac command benchmark."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys


class Arguments(argparse.ArgumentParser):
    def error(self, message):
        raise ValueError(message)


def emit(value, stream=sys.stdout):
    print(json.dumps(value), file=stream, flush=True)


def absent(result, kind, name):
    if result.returncode == 0:
        return False
    prefix = r"(?:Error(?: response from daemon)?:\s*)?"
    missing = rf"No such {kind}:?\s+{re.escape(name)}"
    if kind == "volume":
        missing = rf"(?:{missing}|get {re.escape(name)}: no such volume)"
    return re.fullmatch(prefix + missing, result.stderr.strip(), re.IGNORECASE) is not None


def command(argv, env=None, check=True):
    result = subprocess.run(argv, env=env, capture_output=True, text=True, timeout=120)
    if check and result.returncode:
        raise RuntimeError(f"The fixture command failed: {result.stderr.strip() or argv[0]}")
    return result


def inspect_resource(kind, name):
    result = command(["docker", kind, "inspect", name], check=False)
    if result.returncode:
        if absent(result, kind, name):
            return None
        raise RuntimeError(f"Docker could not inspect the {kind}: {result.stderr.strip()}")
    objects = json.loads(result.stdout)
    if not isinstance(objects, list) or len(objects) != 1 or not isinstance(objects[0], dict):
        raise ValueError(f"Docker returned an invalid {kind} inspection.")
    return objects[0]


def save_config(root, config):
    temporary = root / "config.json.next"
    temporary.write_text(json.dumps(config, indent=2) + "\n")
    temporary.replace(root / "config.json")


def main():
    parser = Arguments(description=__doc__)
    parser.add_argument("--root", required=True, help="New directory below /private/tmp/culler-performance.")
    parser.add_argument("--mc-bin", default=str(Path.home() / ".local/bin/mc"), help="Executable to copy into the fixture.")
    parser.add_argument("--tasks", type=int, default=100, help="Number of fixture tasks, from 1 to 1000.")
    args = parser.parse_args()
    root = Path(args.root).resolve()
    if not root.is_relative_to(Path("/private/tmp/culler-performance")) or root == Path("/private/tmp/culler-performance"):
        raise ValueError("The fixture must be below /private/tmp/culler-performance.")
    if not 1 <= args.tasks <= 1000:
        raise ValueError("The fixture needs one to 1000 tasks.")
    root.mkdir(parents=True, exist_ok=False)
    emit({"type": "fixture-progress", "stage": "copy", "root": str(root)})
    home = root / "home"
    home.mkdir(mode=0o700)
    (home / "logs").mkdir(mode=0o700)
    (root / "bin").mkdir()
    (root / "baseline").mkdir()
    repo = Path(__file__).resolve().parent.parent
    binary = root / "bin/mc-baseline"
    shutil.copy2(Path(args.mc_bin).resolve(), binary)
    shutil.copy2(repo / "dashboard/src/mc.ts", root / "baseline/mc.ts")
    launcher = root / "bin/policy-launcher"
    emit({"type": "fixture-progress", "stage": "compile-launcher"})
    command(["cc", "-std=c11", "-Wall", "-Wextra", "-Werror", str(repo / "performance/policy-launcher.c"), "-o", str(launcher)])
    image = command(["docker", "image", "inspect", "mc-prod", "--format", "{{.Id}}"]).stdout.strip()
    if re.fullmatch(r"sha256:[0-9a-f]{64}", image) is None:
        raise ValueError("Docker returned an invalid image identity.")
    suffix = hashlib.sha256(("mc-deployment-v1\0" + str(home)).encode()).hexdigest()[:12]
    helper, volume = "mc-helper-" + suffix, "mc-spine-" + suffix
    env = {key: value for key, value in os.environ.items() if not key.startswith(("MC_", "CULLER_", "GODEBUG"))}
    env["MC_HOME"] = str(home)
    config = {
        "mcHome": str(home), "expectedImage": image, "helper": helper, "volume": volume,
        "policyLauncher": str(launcher), "fixtureTasks": args.tasks,
        "sourceCommit": command(["git", "-C", str(repo), "rev-parse", "HEAD"]).stdout.strip(),
        "commands": [["task", "list", "--project", "benchmark"]],
        "variants": [
            {"name": policy, "policy": policy, "mcBin": str(binary), "runnerModule": str(root / "baseline/mc.ts")}
            for policy in ["background", "standard"]
        ],
    }
    emit({"type": "fixture-progress", "stage": "verify-resource-names"})
    for kind, name in [("container", helper), ("volume", volume)]:
        if inspect_resource(kind, name) is not None:
            raise RuntimeError("A fixture resource already exists. Choose another --root directory.")
    # A collision must never create a record that authorizes resource removal.
    config["resourcesVerifiedAbsent"] = True
    save_config(root, config)
    emit({"type": "fixture-progress", "stage": "create-helper"})
    command([str(binary), "task", "list"], env=env, check=False)
    created_volume = inspect_resource("volume", volume)
    if created_volume is None or created_volume.get("Name") != volume or created_volume.get("Driver") != "local" or \
            created_volume.get("Options") not in (None, {}) or not isinstance(created_volume.get("CreatedAt"), str) or \
            not created_volume["CreatedAt"]:
        raise ValueError("The new fixture volume lacks a valid local identity.")
    config["volumeCreatedAt"] = created_volume["CreatedAt"]
    config["volumeDriver"] = created_volume["Driver"]
    save_config(root, config)
    container = inspect_resource("container", helper)
    if container is None or container.get("Image") != image or re.fullmatch(r"[0-9a-f]{64}", container.get("Id", "")) is None:
        raise RuntimeError("The image changed during fixture preparation.")
    config["helperID"] = container["Id"]
    save_config(root, config)
    emit({"type": "fixture-progress", "stage": "initialize-database"})
    command(["docker", "exec", "--user", "10001:10001", "-e", "MC_HOME=/mc/spine", helper,
             "/usr/local/libexec/mc-real", "__init", "--project", "benchmark", "--workspace-root", "/workspace/benchmark"])
    emit({"type": "fixture-progress", "stage": "create-tasks", "completed": 0, "total": args.tasks})
    for index in range(args.tasks):
        command(["docker", "exec", helper, "mc", "task", "add", f"Benchmark task {index + 1}",
                 "--project", "benchmark", "--description", "A fixed task for the command latency benchmark."])
        if (index + 1) % 10 == 0 or index + 1 == args.tasks:
            emit({"type": "fixture-progress", "stage": "create-tasks", "completed": index + 1, "total": args.tasks})
    result = command([str(binary), "task", "list", "--project", "benchmark"], env=env)
    task_result = json.loads(result.stdout)
    if not isinstance(task_result, dict) or not isinstance(task_result.get("tasks"), list) or len(task_result["tasks"]) != args.tasks:
        raise RuntimeError("The fixture task count differs from the requested count.")
    config["fixtureReady"] = True
    save_config(root, config)
    emit({"type": "fixture-ready", "config": str(root / "config.json"), "tasks": args.tasks})


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, KeyError, TypeError, subprocess.TimeoutExpired) as error:
        emit({"type": "error", "operation": "prepare", "error": str(error)}, sys.stderr)
        raise SystemExit(1)
    except KeyboardInterrupt:
        emit({"type": "error", "operation": "prepare", "error": "The fixture preparation stopped before completion."}, sys.stderr)
        raise SystemExit(130)
