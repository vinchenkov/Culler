"""Check clock-discontinuity records without a workload or Docker."""

import importlib.util
import io
import json
from pathlib import Path
from types import SimpleNamespace
import unittest


SPEC = importlib.util.spec_from_file_location("benchmark_compare", Path(__file__).with_name("compare.py"))
controller = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(controller)


def result(identifier, wall, elapsed=10):
    return {"type": "result", "id": identifier, "argv": ["task", "list"], "elapsedMs": elapsed,
            "wallElapsedMs": wall, "completedAt": "2026-10-11T22:00:01.100Z",
            "code": 0, "valid": True, "validCode": True, "validJson": True, "outputDigest": "a" * 64}


class ClockTests(unittest.TestCase):
    def test_exact_tolerance_remains_valid(self):
        worker = SimpleNamespace(variant={"name": "control", "policy": "standard"},
                                 request=lambda identifier, argv: result(identifier, 1010))
        output = io.StringIO()
        controller.run_trial(worker, output, "trial", 0, 0, ["task", "list"], 0)
        self.assertTrue(json.loads(output.getvalue())["valid"])

    def test_forward_and_backward_clock_changes_preserve_the_trial(self):
        for wall in (1011, -991):
            with self.subTest(wall=wall):
                worker = SimpleNamespace(variant={"name": "control", "policy": "standard"},
                                         request=lambda identifier, argv: result(identifier, wall))
                output = io.StringIO()
                with self.assertRaises(controller.ClockDiscontinuity):
                    controller.run_trial(worker, output, "trial", 0, 0, ["task", "list"], 0)
                row = json.loads(output.getvalue())
                self.assertEqual(row["status"], "clock_discontinuity")
                self.assertFalse(row["valid"])
                self.assertEqual(row["elapsedMs"], 10)
                self.assertEqual(row["wallElapsedMs"], wall)
                self.assertIn("completedAt", row)

    def test_sleep_clock_gap_precedes_a_command_error(self):
        worker = SimpleNamespace(variant={"name": "deadline", "policy": "standard"}, concurrency=16)
        def request_batch(identifier, requests):
            row = result(requests[0]["id"], 1_047_526, elapsed=526)
            row.update(batchId=identifier, code=2, valid=False, validCode=False)
            yield row
        worker.request_batch = request_batch
        output = io.StringIO()
        with self.assertRaises(controller.ClockDiscontinuity):
            list(controller.run_batch(worker, output, "trial", 13, range(20), [["task", "list"]], 1))
        rows = [json.loads(line) for line in output.getvalue().splitlines()]
        self.assertEqual(len(rows), 20)
        self.assertEqual(rows[0]["status"], "clock_discontinuity")
        self.assertEqual(rows[0]["code"], 2)
        self.assertEqual(rows[0]["elapsedMs"], 526)
        self.assertTrue(all(row["status"] == "batch_aborted" for row in rows[1:]))

    def test_invalid_wall_durations_fail(self):
        for wall in (None, True, float("nan"), "10"):
            with self.subTest(wall=wall), self.assertRaises(controller.ExperimentError):
                controller.clock_difference(result("invalid", wall))


if __name__ == "__main__":
    unittest.main()
