#!/usr/bin/env python3
"""Check runtime source access under different file permission masks."""
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path


class RuntimeSourceTests(unittest.TestCase):
    def test_agent_can_read_sources_with_each_mask(self):
        script = Path(__file__).with_name("stage-runtime.py")
        with tempfile.TemporaryDirectory() as scratch:
            for mask in (0o022, 0o077):
                with self.subTest(mask=oct(mask)):
                    target = Path(scratch) / str(mask)
                    subprocess.run(
                        ["python3", str(script), str(target)], check=True, umask=mask
                    )
                    self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o755)
                    for path in target.rglob("*"):
                        expected = 0o755 if path.is_dir() else 0o644
                        self.assertEqual(stat.S_IMODE(path.stat().st_mode), expected, path)
                        if path.is_file():
                            source = script.parents[1] / path.relative_to(target)
                            self.assertEqual(path.read_bytes(), source.read_bytes())
                    self.assertTrue((target / "agent-runner/adapters/claude.ts").is_file())
                    self.assertTrue((target / "setup-runner/main.ts").is_file())


if __name__ == "__main__":
    unittest.main()
