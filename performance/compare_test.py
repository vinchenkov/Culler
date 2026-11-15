"""Check controller boundaries without Docker or host process inspection."""

import importlib.util
import io
import json
import os
from pathlib import Path
import selectors
import signal
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location("benchmark_compare", Path(__file__).with_name("compare.py"))
controller = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(controller)


def result(identifier, batch="batch"):
    return {"type": "result", "id": identifier, "batchId": batch, "argv": ["task", "list"],
            "elapsedMs": 10, "wallElapsedMs": 10, "completedAt": "2026-10-11T22:00:00.010Z",
            "code": 0, "valid": True, "validCode": True,
            "validJson": True, "outputDigest": "a" * 64}


def protocol(messages):
    worker = controller.Worker.__new__(controller.Worker)
    worker.concurrency = 16
    descriptor, writer = os.pipe()
    worker.process = SimpleNamespace(stdin=os.fdopen(writer, "wb"))
    deadlines = []
    replies = iter(messages)
    def receive(deadline):
        deadlines.append(deadline)
        return next(replies)
    worker.receive = receive
    try:
        records = list(worker.request_batch("batch", [{"id": str(index), "argv": ["task", "list"]}
                                                     for index in range(3)]))
        request = json.loads(os.read(descriptor, 65536))
        return records, request, deadlines
    finally:
        worker.process.stdin.close()
        os.close(descriptor)


class BatchTests(unittest.TestCase):
    def test_out_of_order_results_keep_identities_and_one_deadline(self):
        records, request, deadlines = protocol([result(str(index)) for index in (2, 0, 1)] +
                                               [{"type": "batch-complete", "id": "batch", "count": 3}])
        self.assertEqual([record["id"] for record in records], ["2", "0", "1"])
        self.assertEqual(request["concurrency"], 16)
        self.assertEqual(len(request["requests"]), 3)
        self.assertEqual(len(set(deadlines)), 1)

    def test_invalid_protocol_cannot_complete_a_batch(self):
        cases = [[result("2"), result("2")], [result("unknown")],
                 [dict(result("0"), argv=["project", "list"])],
                 [dict(result("0"), batchId="other")], [dict(result("0"), elapsedMs=True)],
                 [{"type": "batch-complete", "id": "batch", "count": 3}],
                 [result(str(index)) for index in range(3)] +
                 [{"type": "batch-complete", "id": "batch", "count": 2}]]
        for replies in cases:
            with self.subTest(replies=replies), self.assertRaises(controller.ExperimentError):
                protocol(replies)

    def test_timeout_retains_completed_and_unfinished_reads(self):
        worker = SimpleNamespace(variant={"name": "candidate", "policy": "standard"}, concurrency=16)
        def request_batch(identifier, requests):
            yield result(requests[0]["id"], identifier)
            raise controller.WatchdogTimeout("The test reached the deadline.")
        worker.request_batch = request_batch
        output = io.StringIO()
        with self.assertRaises(controller.WatchdogTimeout):
            list(controller.run_batch(worker, output, "trial", 7, range(20), [["task", "list"]], 0))
        records = [json.loads(line) for line in output.getvalue().splitlines()]
        self.assertEqual(len(records), 20)
        self.assertEqual(sorted(record["pairId"] for record in records), list(range(20)))
        self.assertEqual({record["blockId"] for record in records}, {7})
        self.assertEqual(sum(record["timedOut"] for record in records), 19)
        self.assertEqual(sum(record["valid"] for record in records), 1)


class FakeProcess:
    pid = 100
    def __init__(self, code=None):
        self.returncode = code
        self.stdin, self.stdout = io.BytesIO(), io.BytesIO()
    def poll(self):
        return self.returncode
    def wait(self, timeout):
        if self.returncode is None:
            self.returncode = -signal.SIGKILL
        return self.returncode


def identity(pid, parent, group):
    return {"pid": pid, "parent": parent, "group": group, "started": f"start-{pid}"}


class CleanupTests(unittest.TestCase):
    def close(self, snapshot, code=None):
        worker = controller.Worker.__new__(controller.Worker)
        worker.process = FakeProcess(code)
        worker.selector = selectors.DefaultSelector()
        worker.stderr = io.BytesIO()
        with patch.object(controller, "process_snapshot", side_effect=snapshot), \
                patch.object(controller.os, "getpgrp", return_value=900), \
                patch.object(controller.os, "killpg") as send:
            try:
                worker.close()
            except controller.ExperimentError as error:
                return send.call_args_list, str(error)
            return send.call_args_list, None

    def test_detached_child_stops_before_bun(self):
        snapshot = {100: identity(100, 900, 100), 101: identity(101, 100, 101)}
        calls, error = self.close(lambda: snapshot)
        self.assertIsNone(error)
        self.assertEqual([call.args for call in calls], [(100, signal.SIGSTOP), (101, signal.SIGSTOP),
                                                       (101, signal.SIGKILL), (100, signal.SIGKILL)])

    def test_foreign_group_member_prevents_group_signal(self):
        snapshot = {100: identity(100, 900, 100), 101: identity(101, 100, 101),
                    200: identity(200, 900, 101)}
        calls, error = self.close(lambda: snapshot)
        self.assertIn("does not own", error)
        self.assertEqual({call.args[0] for call in calls}, {100})

    def test_foreign_group_leader_prevents_group_signal(self):
        snapshot = {100: identity(100, 900, 100), 101: identity(101, 100, 500),
                    500: identity(500, 900, 500)}
        calls, error = self.close(lambda: snapshot)
        self.assertIn("does not own", error)
        self.assertEqual({call.args[0] for call in calls}, {100})

    def test_inspection_loss_resumes_the_detached_group(self):
        snapshot = {100: identity(100, 900, 100), 101: identity(101, 100, 101)}
        replies = iter([snapshot, snapshot])
        def inspect():
            try:
                return next(replies)
            except StopIteration:
                raise controller.ExperimentError("The test cannot inspect processes.")
        calls, error = self.close(inspect)
        self.assertIn("cannot inspect", error)
        self.assertIn((101, signal.SIGCONT), [call.args for call in calls])
        self.assertIn((100, signal.SIGKILL), [call.args for call in calls])

    def test_exited_worker_does_not_authorize_group_signal(self):
        calls, error = self.close(lambda: {}, code=0)
        self.assertIn("exited before", error)
        self.assertEqual(calls, [])

    def test_inspection_preflight_precedes_worker_launch(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = root / "config.json"
            config.write_text("{}")
            args = SimpleNamespace(config=str(config), output=str(root / "output"), trials=20,
                                   block_size=20, warmup=0, variants=None, concurrency=16)
            with patch.object(controller.sys, "platform", "darwin"), \
                    patch.object(controller, "read_config", return_value={"mcHome": directory, "expectedImage": "image"}), \
                    patch.object(controller, "absolute_file", return_value=Path("/usr/bin/true")), \
                    patch.object(controller.shutil, "which", return_value="/usr/bin/true"), \
                    patch.object(controller, "command_output", return_value="/usr/bin/true"), \
                    patch.object(controller, "image_identity"), \
                    patch.object(controller, "process_snapshot", side_effect=controller.ExperimentError("inspection unavailable")), \
                    patch.object(controller, "Worker") as worker:
                with self.assertRaisesRegex(controller.ExperimentError, "inspection unavailable"):
                    controller.execute(args)
                worker.assert_not_called()
                self.assertFalse((root / "output").exists())


if __name__ == "__main__":
    unittest.main()
