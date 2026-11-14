#!/usr/bin/env python3
"""Run a command under a bounded processor workload on macOS."""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import sys
import time


BUFFER_BYTES = 64 * 1024
BATCH_HASHES = 64
MAX_SECONDS = 1800


class LoadError(Exception):
    pass


def timestamp():
    return datetime.now(timezone.utc).isoformat()


def status(value, stream=sys.stdout):
    print(json.dumps(value, sort_keys=True), file=stream, flush=True)


def worker(deadline):
    payload = bytes(BUFFER_BYTES)
    status({"type": "ready", "pid": os.getpid(), "startedAt": timestamp()})
    count = 0
    while time.monotonic() < deadline:
        for _ in range(BATCH_HASHES):
            hashlib.sha256(payload).digest()
        count += BATCH_HASHES
    status({"type": "finished", "pid": os.getpid(), "hashes": count,
            "finishedAt": timestamp()})
    return 0


def wait_ready(process, deadline):
    buffer = bytearray()
    readiness_deadline = min(deadline, time.monotonic() + 30)
    with selectors.DefaultSelector() as selector:
        selector.register(process.stdout, selectors.EVENT_READ)
        while b"\n" not in buffer:
            remaining = readiness_deadline - time.monotonic()
            if remaining <= 0 or not selector.select(remaining):
                raise LoadError("A processor worker did not start before its deadline.")
            chunk = os.read(process.stdout.fileno(), 4096)
            if not chunk:
                raise LoadError("A processor worker closed its result stream.")
            buffer.extend(chunk)
            if len(buffer) > 65536:
                raise LoadError("A processor worker exceeded its output limit.")
    try:
        result = json.loads(buffer.partition(b"\n")[0])
    except (ValueError, UnicodeDecodeError) as error:
        raise LoadError("A processor worker returned invalid status data.") from error
    if not isinstance(result, dict) or result.get("type") != "ready" or result.get("pid") != process.pid:
        raise LoadError("A processor worker returned an unexpected identity.")
    return result


def stop_group(process, grace_seconds):
    errors = []
    for number, timeout in ((signal.SIGTERM, grace_seconds), (signal.SIGKILL, 5)):
        try:
            os.killpg(process.pid, number)
        except ProcessLookupError:
            pass
        except OSError as error:
            errors.append(f"The process group {process.pid} refused signal {number}: {error}.")
        try:
            process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            continue
    if process.poll() is None:
        errors.append(f"The process group {process.pid} did not exit.")
    if process.stdout is not None:
        process.stdout.close()
    return errors


def update_record(stream, record):
    stream.seek(0)
    json.dump(record, stream, indent=2, sort_keys=True, allow_nan=False)
    stream.write("\n")
    stream.truncate()
    stream.flush()
    os.fsync(stream.fileno())


def task_priority(launcher, pid):
    try:
        result = subprocess.run(
            [str(launcher), "--report", str(pid)], stdin=subprocess.DEVNULL,
            capture_output=True, text=True, timeout=10, check=False,
        )
        report = json.loads(result.stdout)
    except (OSError, ValueError, subprocess.TimeoutExpired) as error:
        raise LoadError("The processor worker priority check failed.") from error
    if result.returncode != 0 or not isinstance(report, dict) or report.get("pid") != pid or report.get("task_priority") != 31:
        raise LoadError("A processor worker did not receive Standard task priority 31.")
    return report


def run(args):
    if sys.platform != "darwin":
        raise LoadError("This processor workload requires macOS.")
    if args.workers < 1:
        raise LoadError("The worker count must be positive.")
    if not 1 <= args.max_seconds <= MAX_SECONDS:
        raise LoadError("The workload duration must be from 1 to 1800 seconds.")
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        raise LoadError("Supply the command after --.")
    launcher = Path(args.policy_launcher).resolve(strict=True)
    if not launcher.is_file() or not os.access(launcher, os.X_OK):
        raise LoadError("The policy launcher must name an executable file.")
    logical_processors = os.cpu_count() or 1
    if logical_processors < 2:
        raise LoadError("The workload requires at least two logical processors.")
    maximum_workers = logical_processors // 2
    actual_workers = min(args.workers, maximum_workers)
    output = Path(args.record).absolute()
    output.parent.mkdir(parents=True, exist_ok=True)
    processes = []
    command_process = None
    started = time.monotonic()
    deadline = started + args.max_seconds
    record = {
        "schemaVersion": 1, "state": "starting", "startedAt": timestamp(),
        "requestedWorkers": args.workers, "selectedWorkers": actual_workers, "actualWorkers": 0,
        "logicalProcessors": logical_processors, "workerCap": maximum_workers,
        "maxSeconds": args.max_seconds, "command": command,
        "policyLauncher": str(launcher), "policyLauncherSha256": hashlib.sha256(launcher.read_bytes()).hexdigest(),
        "pythonExecutable": sys.executable, "pythonVersion": sys.version.split()[0],
        "generator": {
            "algorithm": "SHA-256 over the same zero-filled byte buffer",
            "bufferBytesPerWorker": BUFFER_BYTES, "hashesPerDeadlineCheck": BATCH_HASHES,
            "retainedResults": 0, "policy": "standard", "taskPriority": 31,
            "sourceSha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        },
        "initialLoadAverage": list(os.getloadavg()), "workers": [],
    }
    returncode = 1
    with output.open("x+", encoding="utf-8") as stream:
        update_record(stream, record)
        try:
            for _ in range(actual_workers):
                process = subprocess.Popen(
                    [str(launcher), "standard", str(Path(sys.executable).resolve()),
                     str(Path(__file__).resolve()), "--internal-worker", str(deadline)],
                    stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                    stderr=subprocess.DEVNULL, start_new_session=True,
                )
                processes.append(process)
                record["actualWorkers"] = len(processes)
            for process in processes:
                ready = wait_ready(process, deadline)
                ready["policyReport"] = task_priority(launcher, process.pid)
                record["workers"].append(ready)
            if time.monotonic() >= deadline:
                raise LoadError("The workload deadline expired before the command started.")
            record["state"] = "running"
            record["commandStartedAt"] = timestamp()
            record["loadAverageAtCommandStart"] = list(os.getloadavg())
            command_process = subprocess.Popen(command, start_new_session=True)
            record["commandPid"] = command_process.pid
            update_record(stream, record)
            status({"type": "cpu-load-start", "workers": actual_workers,
                    "policy": "standard", "maxSeconds": args.max_seconds})
            while True:
                result = command_process.poll()
                if result is not None:
                    returncode = result if result >= 0 else 128 - result
                    record["state"] = "complete" if result == 0 else "command_failed"
                    record["commandReturnCode"] = result
                    break
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    returncode = 124
                    record["state"] = "deadline"
                    record["error"] = "The workload reached its maximum duration."
                    break
                if any(process.poll() is not None for process in processes):
                    raise LoadError("A processor worker exited before the command completed.")
                try:
                    command_process.wait(timeout=min(0.25, remaining))
                except subprocess.TimeoutExpired:
                    pass
        except KeyboardInterrupt:
            returncode = 130
            record["state"] = "interrupted"
            record["error"] = "The workload received an interruption."
        except (LoadError, OSError) as error:
            returncode = 1
            record["state"] = "failed"
            record["error"] = str(error)
        finally:
            cleanup_errors = []
            if command_process is not None:
                cleanup_errors.extend(stop_group(command_process, 10))
                record["commandReturnCode"] = command_process.returncode
            for process in processes:
                cleanup_errors.extend(stop_group(process, 1))
            record["workerReturnCodes"] = [process.returncode for process in processes]
            record["finishedAt"] = timestamp()
            record["elapsedSeconds"] = time.monotonic() - started
            record["finalLoadAverage"] = list(os.getloadavg())
            record["cleanupErrors"] = cleanup_errors
            if cleanup_errors:
                record["state"] = "cleanup_failed"
                returncode = 1
            update_record(stream, record)
    if record.get("error"):
        status({"type": "error", "error": record["error"]}, sys.stderr)
    status({"type": "cpu-load-end", "state": record["state"], "workers": actual_workers,
            "elapsedSeconds": record["elapsedSeconds"], "exitCode": returncode,
            "record": str(output), "cleanupErrors": record["cleanupErrors"]})
    return returncode


def main():
    if len(sys.argv) == 3 and sys.argv[1] == "--internal-worker":
        try:
            deadline = float(sys.argv[2])
            remaining = deadline - time.monotonic()
            if not 0 < remaining <= MAX_SECONDS:
                raise ValueError()
            return worker(deadline)
        except (ValueError, KeyboardInterrupt):
            return 1
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workers", type=int, default=6,
                        help="Requested workers; the limit is half the logical processors.")
    parser.add_argument("--max-seconds", type=int, default=MAX_SECONDS,
                        help="Maximum workload duration, from 1 to 1800 seconds.")
    parser.add_argument("--record", required=True, help="A new JSON record path.")
    parser.add_argument("--policy-launcher", required=True, help="The compiled policy launcher path.")
    parser.add_argument("command", nargs=argparse.REMAINDER,
                        help="The command to run after --.")
    args = parser.parse_args()
    def interrupted(_signal, _frame):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupted)
    try:
        return run(args)
    except (LoadError, OSError, ValueError, KeyboardInterrupt) as error:
        status({"type": "error", "error": str(error) or "The workload received an interruption."}, sys.stderr)
        return 1
    except Exception as error:
        status({"type": "error", "error": f"The workload failed: {type(error).__name__}."}, sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
