#!/usr/bin/env python3
"""Compare persistent Bun workers with separate macOS process policies."""

import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import random
import selectors
import shutil
import signal
import subprocess
import sys
import time
from datetime import datetime, timezone


SCRATCH_ROOT = Path("/private/tmp/culler-performance")
WATCHDOG_SECONDS = 60
MAX_PROTOCOL_BYTES = 2 * 1024 * 1024
MAX_CLOCK_DIFFERENCE_MS = 1000


class ExperimentError(Exception):
    pass


class WatchdogTimeout(ExperimentError):
    pass


class ClockDiscontinuity(ExperimentError):
    pass


def clock_difference(result):
    elapsed = result.get("wallElapsedMs")
    if isinstance(elapsed, bool) or not isinstance(elapsed, (float, int)) or not math.isfinite(elapsed):
        raise ExperimentError("A worker returned an invalid wall-clock duration.")
    return abs(elapsed - result["elapsedMs"])


def clock_error():
    return ClockDiscontinuity(
        "The two command clocks differ by more than 1000 milliseconds. "
        "The benchmark is incomplete. Check sleep and clock records before a new run."
    )


def utc_now():
    return datetime.now(timezone.utc).isoformat()


def emit(stream, value):
    stream.write(json.dumps(value, sort_keys=True, allow_nan=False) + "\n")
    stream.flush()


def write_json(path, value):
    with path.open("x", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, sort_keys=True, allow_nan=False)
        stream.write("\n")


def digest(path):
    result = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(chunk)
    return result.hexdigest()


def absolute_file(value, name, executable=False):
    if not isinstance(value, str) or not Path(value).is_absolute():
        raise ExperimentError(f"{name} must name an absolute file path.")
    path = Path(value).resolve(strict=True)
    if not path.is_file() or (executable and not os.access(path, os.X_OK)):
        raise ExperimentError(f"{name} does not name the required file.")
    return path


def read_config(path, selected):
    with path.open(encoding="utf-8") as stream:
        config = json.load(stream)
    value = config.get("mcHome")
    if not isinstance(value, str) or not Path(value).is_absolute():
        raise ExperimentError("mcHome must name an absolute scratch directory.")
    home = Path(value).resolve(strict=True)
    scratch_root = SCRATCH_ROOT.resolve(strict=True)
    if home == scratch_root or not home.is_relative_to(scratch_root):
        raise ExperimentError("mcHome must stay below /private/tmp/culler-performance.")
    if not home.is_dir():
        raise ExperimentError("mcHome must name an existing directory.")
    config["mcHome"] = str(home)
    expected = config.get("expectedImage")
    if not isinstance(expected, str) or len(expected) != 71 or not expected.startswith("sha256:"):
        raise ExperimentError("expectedImage must contain a complete Docker image ID.")
    try:
        int(expected[7:], 16)
    except ValueError as error:
        raise ExperimentError("expectedImage contains an invalid digest.") from error
    launcher = absolute_file(config.get("policyLauncher"), "policyLauncher", True)
    config["policyLauncher"] = str(launcher)
    commands = config.get("commands", [["project", "list"], ["task", "list"]])
    if not isinstance(commands, list) or not commands:
        raise ExperimentError("commands must contain at least one read command.")
    for command in commands:
        permitted = command in (["project", "list"], ["task", "list"])
        permitted = permitted or (
            isinstance(command, list)
            and len(command) == 4
            and command[:3] == ["task", "list", "--project"]
            and isinstance(command[3], str)
            and bool(command[3])
        )
        if not permitted:
            raise ExperimentError("commands contains a command outside the read surface.")
    config["commands"] = commands
    variants = config.get("variants")
    if not isinstance(variants, list) or not variants:
        raise ExperimentError("variants must contain at least one variant.")
    names = set()
    for variant in variants:
        if not isinstance(variant, dict):
            raise ExperimentError("Each variant must contain a configuration object.")
        name = variant.get("name")
        if not isinstance(name, str) or not name or not all(
            character.isascii() and (character.isalnum() or character in "-_")
            for character in name
        ):
            raise ExperimentError("Each variant needs a simple unique name.")
        if name in names:
            raise ExperimentError("Variant names must be unique.")
        names.add(name)
        if variant.get("policy") not in ("standard", "background"):
            raise ExperimentError("Each variant policy must be standard or background.")
        for key in ("mcBin", "runnerModule"):
            variant[key] = str(absolute_file(variant.get(key), key, key == "mcBin"))
    if selected:
        requested = selected.split(",")
        if len(set(requested)) != len(requested) or not set(requested).issubset(names):
            raise ExperimentError("--variants must select unique configured variant names.")
        variants = [variant for variant in variants if variant["name"] in requested]
    config["variants"] = variants
    return config


def command_output(argv, cwd, env, timeout=15):
    try:
        result = subprocess.run(
            argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
            capture_output=True, text=True, timeout=timeout, check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ExperimentError(f"The metadata command failed: {argv[0]}.") from error
    if result.returncode != 0:
        raise ExperimentError(f"The metadata command failed: {argv[0]}.")
    return result.stdout.strip()


def image_identity(docker, expected, cwd, env):
    actual = command_output(
        [docker, "image", "inspect", "--format", "{{.Id}}", "mc-prod"], cwd, env,
    )
    if actual != expected:
        raise ExperimentError("The mc-prod image identity differs from expectedImage.")
    return actual


def block_resources(cwd, env):
    return {
        "observedAt": utc_now(),
        "loadAverage": list(os.getloadavg()),
        "memory": command_output(["/usr/bin/vm_stat"], cwd, env),
        "swap": command_output(["/usr/sbin/sysctl", "-n", "vm.swapusage"], cwd, env),
    }


def process_snapshot():
    result = subprocess.run(
        ["/bin/ps", "-axo", "pid=,ppid=,pgid=,lstart="],
        capture_output=True, text=True, timeout=2, check=False,
    )
    if result.returncode != 0:
        raise ExperimentError("The worker cleanup could not inspect process identities.")
    processes = {}
    for line in result.stdout.splitlines():
        fields = line.split(None, 3)
        if len(fields) != 4:
            raise ExperimentError("The worker cleanup received an invalid process identity.")
        pid, parent, group = map(int, fields[:3])
        processes[pid] = {"pid": pid, "parent": parent, "group": group, "started": fields[3]}
    return processes


def descendant_processes(snapshot, parents):
    owned = set(parents)
    while True:
        children = {pid for pid, item in snapshot.items() if item["parent"] in owned}
        if children.issubset(owned):
            return {pid: item for pid, item in snapshot.items() if pid in owned}
        owned.update(children)


class Worker:
    def __init__(self, variant, config, mise, bun, worker_file, cwd, env, output, concurrency=1):
        self.variant = variant
        self.concurrency = concurrency
        self.buffer = bytearray()
        self.selector = selectors.DefaultSelector()
        self.stderr = (output / f"worker-{variant['name']}.stderr").open("xb")
        self.process = None
        try:
            self.process = subprocess.Popen(
                [mise, "exec", "--", config["policyLauncher"], variant["policy"],
                 bun, str(worker_file), "--mc-bin", variant["mcBin"],
                 "--runner-module", variant["runnerModule"], "--max-concurrency", str(concurrency)],
                cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                stderr=self.stderr, start_new_session=True,
            )
            self.selector.register(self.process.stdout, selectors.EVENT_READ)
        except BaseException:
            self.close()
            raise

    def receive(self, deadline=None):
        batch_deadline = deadline is not None
        if deadline is None:
            deadline = time.monotonic() + WATCHDOG_SECONDS
        while True:
            if batch_deadline and time.monotonic() >= deadline:
                raise WatchdogTimeout("A worker exceeded the external 60-second watchdog.")
            if b"\n" in self.buffer:
                line, _, remainder = self.buffer.partition(b"\n")
                self.buffer = bytearray(remainder)
                try:
                    value = json.loads(line)
                except (UnicodeDecodeError, json.JSONDecodeError) as error:
                    raise ExperimentError("A worker returned invalid protocol data.") from error
                if not isinstance(value, dict):
                    raise ExperimentError("A worker returned an invalid protocol object.")
                return value
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise WatchdogTimeout("A worker exceeded the external 60-second watchdog.")
            if not self.selector.select(remaining):
                continue
            chunk = os.read(self.process.stdout.fileno(), 65536)
            if not chunk:
                raise ExperimentError("A worker closed its result stream.")
            self.buffer.extend(chunk)
            if len(self.buffer) > MAX_PROTOCOL_BYTES:
                raise ExperimentError("A worker exceeded the protocol size limit.")

    def ready(self, launcher, cwd, env):
        result = self.receive()
        if result.get("type") != "ready" or not isinstance(result.get("pid"), int):
            raise ExperimentError("A worker did not report its identity.")
        if self.concurrency > 1 and result.get("maxConcurrency") != self.concurrency:
            raise ExperimentError("A worker did not report its configured concurrency.")
        report = json.loads(command_output(
            [launcher, "--report", str(result["pid"])], cwd, env,
        ))
        expected_priority = 4 if self.variant["policy"] == "background" else 31
        if report.get("task_priority") != expected_priority:
            raise ExperimentError("A worker did not receive its configured task priority.")
        return {"variant": self.variant["name"], "ready": result, "policyReport": report}

    def request(self, identifier, argv):
        request = json.dumps({"id": identifier, "argv": argv}).encode() + b"\n"
        try:
            self.process.stdin.write(request)
            self.process.stdin.flush()
        except (BrokenPipeError, OSError) as error:
            raise ExperimentError("A worker refused a trial request.") from error
        result = self.receive()
        if result.get("type") != "result" or result.get("id") != identifier:
            raise ExperimentError("A worker returned an unexpected trial identity.")
        elapsed = result.get("elapsedMs")
        if not isinstance(elapsed, (float, int)) or not math.isfinite(elapsed) or elapsed < 0:
            raise ExperimentError("A worker returned an invalid elapsed duration.")
        return result

    def request_batch(self, identifier, requests):
        deadline = time.monotonic() + WATCHDOG_SECONDS
        expected = {item["id"]: item["argv"] for item in requests}
        if len(expected) != len(requests) or not requests:
            raise ExperimentError("A batch needs distinct request identities.")
        request = json.dumps({"id": identifier, "requests": requests,
                              "concurrency": self.concurrency}).encode() + b"\n"
        descriptor = self.process.stdin.fileno()
        try:
            os.set_blocking(descriptor, False)
            with selectors.DefaultSelector() as selector:
                selector.register(descriptor, selectors.EVENT_WRITE)
                offset = 0
                while offset < len(request):
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise WatchdogTimeout("A worker exceeded the external 60-second watchdog.")
                    if not selector.select(remaining):
                        continue
                    try:
                        offset += os.write(descriptor, request[offset:offset + 65536])
                    except BlockingIOError:
                        continue
        except (BrokenPipeError, OSError) as error:
            raise ExperimentError("A worker refused a batch request.") from error
        finally:
            os.set_blocking(descriptor, True)
        received = set()
        while True:
            result = self.receive(deadline)
            if result.get("type") == "batch-complete":
                if result.get("id") != identifier or result.get("count") != len(requests) or len(received) != len(requests):
                    raise ExperimentError("A worker returned an incomplete batch marker.")
                return
            result_id = result.get("id")
            if result.get("type") != "result" or not isinstance(result_id, str) or \
                    result_id not in expected or result_id in received or result.get("batchId") != identifier:
                raise ExperimentError("A worker returned an unexpected batch request identity.")
            if result.get("argv") != expected[result_id]:
                raise ExperimentError("A worker returned a different command for a batch request.")
            elapsed = result.get("elapsedMs")
            if isinstance(elapsed, bool) or not isinstance(elapsed, (float, int)) or not math.isfinite(elapsed) or elapsed < 0:
                raise ExperimentError("A worker returned an invalid elapsed duration.")
            received.add(result_id)
            yield result

    def close(self):
        process = self.process
        errors = []
        stopped_groups = {}
        if process is not None:
            worker_exited = False
            try:
                if process.poll() is not None:
                    worker_exited = True
                    raise ExperimentError("The Bun worker exited before the controller could verify its child groups.")
                else:
                    os.killpg(process.pid, signal.SIGSTOP)
                    stopped_groups[process.pid] = None
                    roots = {}
                    for _ in range(8):
                        snapshot = process_snapshot()
                        if not roots and process.pid in snapshot:
                            roots[process.pid] = snapshot[process.pid]
                        parents = {pid for pid, item in roots.items()
                                   if snapshot.get(pid, {}).get("started") == item["started"]}
                        owned = descendant_processes(snapshot, parents)
                        roots.update(owned)
                        new_groups = {}
                        for item in owned.values():
                            group = item["group"]
                            if group not in stopped_groups:
                                if group <= 1 or group == os.getpgrp():
                                    raise ExperimentError("A worker child joined an unowned process group.")
                                new_groups.setdefault(group, []).append(item)
                        if not new_groups:
                            break
                        for group, identities in new_groups.items():
                            current = process_snapshot()
                            if not any(current.get(item["pid"], {}).get("started") == item["started"] and
                                       current.get(item["pid"], {}).get("group") == group for item in identities):
                                continue
                            parents = {pid for pid, item in roots.items()
                                       if current.get(pid, {}).get("started") == item["started"]}
                            verified = descendant_processes(current, parents)
                            members = [item for item in current.values() if item["group"] == group]
                            if group not in verified or any(item["pid"] not in verified for item in members):
                                raise ExperimentError("A worker child belongs to a process group that the controller does not own.")
                            roots.update(verified)
                            stopped_groups[group] = members
                            try:
                                os.killpg(group, signal.SIGSTOP)
                            except ProcessLookupError:
                                pass
                    else:
                        raise ExperimentError("The worker process tree did not stop within its cleanup limit.")
            except ProcessLookupError:
                pass
            except Exception as error:
                errors.append(str(error))
            finally:
                # Detached read commands own separate groups. Stop them before Bun.
                for group, identities in reversed(list(stopped_groups.items())):
                    if group == process.pid:
                        continue
                    try:
                        current = process_snapshot()
                        matches = [item for item in identities if current.get(item["pid"], {}).get("started") == item["started"]
                                   and current.get(item["pid"], {}).get("group") == group]
                        if matches:
                            os.killpg(group, signal.SIGKILL)
                        elif any(item["group"] == group for item in current.values()):
                            os.killpg(group, signal.SIGCONT)
                            errors.append("A stopped group lost its verified process identity; the cleanup resumed that group.")
                    except ProcessLookupError:
                        pass
                    except Exception as error:
                        errors.append(str(error))
                        try:
                            os.killpg(group, signal.SIGCONT)
                        except ProcessLookupError:
                            pass
                        except OSError as resume_error:
                            errors.append(str(resume_error))
                if not worker_exited:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    except OSError as error:
                        errors.append(str(error))
                        try:
                            os.killpg(process.pid, signal.SIGCONT)
                        except ProcessLookupError:
                            pass
                        except OSError as resume_error:
                            errors.append(str(resume_error))
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    errors.append("The Bun worker did not exit after its cleanup signal.")
                for stream in (process.stdin, process.stdout):
                    if stream:
                        stream.close()
                self.process = None
        self.selector.close()
        self.stderr.close()
        if errors:
            raise ExperimentError("The worker cleanup needs inspection: " + "; ".join(errors))


def run_trial(worker, stream, phase, block, pair, argv, order):
    identifier = f"{phase}-{pair}-{worker.variant['name']}"
    row = {
        "type": "trial", "blockId": block, "pairId": pair,
        "warmup": phase == "warmup", "argv": argv,
        "phase": phase, "block": block, "pair": pair, "order": order,
        "variant": worker.variant["name"], "policy": worker.variant["policy"],
        "command": argv, "controllerObservedAt": utc_now(),
        "hostLoadAverage": list(os.getloadavg()),
    }
    try:
        result = worker.request(identifier, argv)
    except ExperimentError as error:
        row.update({
            "status": "watchdog_timeout" if isinstance(error, WatchdogTimeout) else "protocol_error",
            "valid": False, "elapsedMs": None, "error": str(error),
            "timedOut": isinstance(error, WatchdogTimeout),
        })
        if isinstance(error, WatchdogTimeout):
            row["externalWatchdogSeconds"] = WATCHDOG_SECONDS
        emit(stream, row)
        raise
    row.update(result)
    clock_changed = clock_difference(result) > MAX_CLOCK_DIFFERENCE_MS
    row.update({"type": "trial", "status": "clock_discontinuity" if clock_changed else "complete", "timedOut": False,
                "elapsedMs": result["elapsedMs"], "valid": result.get("valid") is True and not clock_changed,
                "exceededBrowserDeadline": result["elapsedMs"] > 15000})
    if clock_changed:
        row["error"] = str(clock_error())
    emit(stream, row)
    if clock_changed:
        raise clock_error()
    if result.get("valid") is not True or result.get("code") != 0:
        raise ExperimentError("A read command failed its correctness check.")
    return result


def run_batch(worker, stream, phase, block, pairs, commands, order):
    identifier = f"{phase}-block-{block}-{worker.variant['name']}"
    rows = {}
    for pair in pairs:
        request_id = f"{phase}-{pair}-{worker.variant['name']}"
        argv = commands[pair % len(commands)]
        rows[request_id] = {
            "type": "trial", "id": request_id, "batchId": identifier,
            "blockId": block, "pairId": pair, "block": block, "pair": pair,
            "warmup": phase == "warmup", "phase": phase, "order": order,
            "variant": worker.variant["name"], "policy": worker.variant["policy"],
            "argv": argv, "command": argv, "concurrency": worker.concurrency,
            "controllerObservedAt": utc_now(), "hostLoadAverage": list(os.getloadavg()),
        }
    requests = [{"id": key, "argv": row["argv"]} for key, row in rows.items()]
    received = set()
    try:
        for result in worker.request_batch(identifier, requests):
            row = rows[result["id"]]
            clock_changed = clock_difference(result) > MAX_CLOCK_DIFFERENCE_MS
            output_digest = result.get("outputDigest")
            valid = result.get("valid") is True and result.get("code") == 0 and \
                result.get("validCode") is True and result.get("validJson") is True and \
                isinstance(output_digest, str) and len(output_digest) == 64 and \
                all(character in "0123456789abcdef" for character in output_digest)
            row.update(result)
            row.update({"type": "trial", "status": "clock_discontinuity" if clock_changed else "complete", "timedOut": False,
                        "valid": valid and not clock_changed,
                        "exceededBrowserDeadline": result["elapsedMs"] > 15000})
            if clock_changed:
                row["error"] = str(clock_error())
            emit(stream, row)
            received.add(result["id"])
            if clock_changed:
                raise clock_error()
            if not valid:
                raise ExperimentError("A read command failed its correctness check.")
            yield row
    except (ExperimentError, OSError, KeyboardInterrupt) as error:
        for request_id, row in rows.items():
            if request_id in received:
                continue
            row.update({
                "status": "watchdog_timeout" if isinstance(error, WatchdogTimeout) else "batch_aborted",
                "valid": False, "elapsedMs": None, "error": str(error),
                "timedOut": isinstance(error, WatchdogTimeout),
            })
            if isinstance(error, WatchdogTimeout):
                row["externalWatchdogSeconds"] = WATCHDOG_SECONDS
            emit(stream, row)
        raise


def execute(args):
    if sys.platform != "darwin":
        raise ExperimentError("This benchmark requires macOS.")
    if min(args.trials, args.block_size) <= 0 or args.warmup < 0:
        raise ExperimentError("Trial and block counts must be positive; warm-up counts must be nonnegative.")
    if not 1 <= args.concurrency <= 64:
        raise ExperimentError("The concurrency must be from one to 64.")
    if args.concurrency > 1 and args.block_size > 10000:
        raise ExperimentError("A concurrent block must contain at most 10000 reads.")
    config_path = Path(args.config).resolve(strict=True)
    config = read_config(config_path, args.variants)
    cwd = Path(__file__).resolve().parent.parent
    worker_file = absolute_file(str(cwd / "performance" / "worker.ts"), "worker")
    mise = shutil.which("mise")
    docker = shutil.which("docker")
    if mise is None or docker is None:
        raise ExperimentError("The benchmark requires mise and Docker.")
    mise, docker = str(Path(mise).resolve()), str(Path(docker).resolve())
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("MC_", "CULLER_")) and key != "GODEBUG"}
    env["MC_HOME"] = config["mcHome"]
    bun = str(absolute_file(command_output([mise, "which", "bun"], cwd, env), "Bun", True))
    image = image_identity(docker, config["expectedImage"], cwd, env)
    process_snapshot()
    output = Path(args.output).absolute()
    output.mkdir(parents=True, exist_ok=False)
    workers = []
    completed = 0
    outcome = "failed"
    error_text = None
    rng = random.Random(args.seed)
    try:
        metadata = {
            "schemaVersion": 1, "startedAt": utc_now(), "config": config,
            "configSha256": digest(config_path), "trialsPerVariant": args.trials,
            "warmupPerVariant": args.warmup, "blockSize": args.block_size,
            "concurrency": args.concurrency,
            "variantOrder": "per-read" if args.concurrency == 1 else "per-block",
            "seed": args.seed, "watchdogSeconds": WATCHDOG_SECONDS,
            "maxClockDifferenceMs": MAX_CLOCK_DIFFERENCE_MS,
            "timedBoundary": "Bun worker before McRunner.run through parsed result and process exit",
            "host": {
                "macOS": command_output(["/usr/bin/sw_vers"], cwd, env),
                "machine": platform.machine(), "kernelRelease": platform.release(),
                "hardware": command_output(
                    ["/usr/sbin/sysctl", "hw.model", "hw.ncpu", "hw.memsize"], cwd, env),
                "python": platform.python_version(),
            },
            "worktreeHead": command_output(["git", "rev-parse", "HEAD"], cwd, env),
            "imageID": image,
            "dockerClientVersion": command_output(
                [docker, "version", "--format", "{{.Client.Version}}"], cwd, env),
            "files": {
                "controller": {"path": str(Path(__file__).resolve()), "sha256": digest(Path(__file__))},
                "worker": {"path": str(worker_file), "sha256": digest(worker_file)},
                "launcher": {"path": config["policyLauncher"], "sha256": digest(Path(config["policyLauncher"]))},
                "bun": {"path": bun, "sha256": digest(Path(bun))},
            },
            "variants": [{**variant, "mcSha256": digest(Path(variant["mcBin"])),
                          "runnerSha256": digest(Path(variant["runnerModule"]))}
                         for variant in config["variants"]],
        }
        write_json(output / "metadata.json", metadata)
        for variant in config["variants"]:
            worker = Worker(variant, config, mise, bun, worker_file, cwd, env, output, args.concurrency)
            workers.append(worker)
        identities = [worker.ready(config["policyLauncher"], cwd, env) for worker in workers]
        write_json(output / "workers.json", identities)
        with (output / "warmup.jsonl").open("x", encoding="utf-8") as stream:
            if args.concurrency == 1:
                for pair in range(args.warmup):
                    order = list(workers)
                    rng.shuffle(order)
                    argv = config["commands"][pair % len(config["commands"])]
                    for position, worker in enumerate(order):
                        run_trial(worker, stream, "warmup", -1, pair, argv, position)
            else:
                for first in range(0, args.warmup, args.block_size):
                    order = list(workers)
                    rng.shuffle(order)
                    pairs = range(first, min(first + args.block_size, args.warmup))
                    for position, worker in enumerate(order):
                        for _ in run_batch(worker, stream, "warmup", -1 - first // args.block_size,
                                           pairs, config["commands"], position):
                            pass
        with (output / "trials.jsonl").open("x", encoding="utf-8") as trials, \
                (output / "blocks.jsonl").open("x", encoding="utf-8") as blocks:
            if args.concurrency == 1:
                for pair in range(args.trials):
                    block = pair // args.block_size
                    if pair % args.block_size == 0:
                        identity = image_identity(docker, config["expectedImage"], cwd, env)
                        emit(blocks, {"block": block, "imageID": identity,
                                      "resources": block_resources(cwd, env)})
                    order = list(workers)
                    rng.shuffle(order)
                    argv = config["commands"][pair % len(config["commands"])]
                    results = []
                    for position, worker in enumerate(order):
                        results.append(run_trial(worker, trials, "trial", block, pair, argv, position))
                        completed += 1
                    if len({result.get("outputDigest") for result in results}) != 1:
                        raise ExperimentError("Paired variants returned different read results.")
                    if (pair + 1) % args.block_size == 0 or pair + 1 == args.trials:
                        trials.flush()
                        os.fsync(trials.fileno())
                        emit(sys.stdout, {"type": "progress", "block": block,
                                          "trialsPerVariantComplete": pair + 1,
                                          "trialsPerVariantTotal": args.trials})
            else:
                for first in range(0, args.trials, args.block_size):
                    block = first // args.block_size
                    identity = image_identity(docker, config["expectedImage"], cwd, env)
                    emit(blocks, {"block": block, "imageID": identity,
                                  "resources": block_resources(cwd, env)})
                    pairs = range(first, min(first + args.block_size, args.trials))
                    order = list(workers)
                    rng.shuffle(order)
                    digests = {pair: set() for pair in pairs}
                    for position, worker in enumerate(order):
                        for result in run_batch(worker, trials, "trial", block, pairs, config["commands"], position):
                            digests[result["pairId"]].add(result["outputDigest"])
                            completed += 1
                    if any(len(values) != 1 for values in digests.values()):
                        raise ExperimentError("Paired variants returned different read results.")
                    trials.flush()
                    os.fsync(trials.fileno())
                    emit(sys.stdout, {"type": "progress", "block": block,
                                      "trialsPerVariantComplete": pairs.stop,
                                      "trialsPerVariantTotal": args.trials,
                                      "concurrency": args.concurrency})
        image_identity(docker, config["expectedImage"], cwd, env)
        outcome = "complete"
    except BaseException as error:
        error_text = str(error) or type(error).__name__
        if isinstance(error, ClockDiscontinuity):
            outcome = "clock_discontinuity"
        raise
    finally:
        cleanup_errors = []
        for worker in workers:
            try:
                worker.close()
            except Exception as error:
                cleanup_errors.append(str(error))
        write_json(output / "completion.json", {
            "finishedAt": utc_now(), "outcome": "cleanup_failed" if cleanup_errors else outcome,
            "completedTrials": completed, "error": error_text,
            "cleanupErrors": cleanup_errors,
            "fixtureCleanup": "The caller owns the scratch helper and volume.",
        })
        if cleanup_errors and error_text is None:
            raise ExperimentError("A worker process group needs cleanup.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--trials", type=int, default=1000)
    parser.add_argument("--block-size", type=int, default=20)
    parser.add_argument("--concurrency", type=int, default=1,
                        help="Concurrent reads per variant, from 1 to 64. Default: 1.")
    parser.add_argument("--seed", type=int, default=31)
    parser.add_argument("--warmup", type=int, default=10)
    parser.add_argument("--variants", help="Comma-separated configured variant names.")
    args = parser.parse_args()
    def interrupted(_signal, _frame):
        raise KeyboardInterrupt("The controller received a termination signal.")
    signal.signal(signal.SIGTERM, interrupted)
    try:
        execute(args)
    except (ExperimentError, OSError, ValueError, KeyboardInterrupt) as error:
        record = {"type": "error", "error": str(error) or type(error).__name__}
        if isinstance(error, ClockDiscontinuity):
            record["code"] = "clock_discontinuity"
        emit(sys.stderr, record)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
