import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent


class OpenCodePathsTest(unittest.TestCase):
    def test_npm_receives_physical_paths(self):
        for script in ("build.sh", "check.sh"):
            with self.subTest(script=script), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary).resolve()
                physical = root / "physical directory"
                physical.mkdir()
                alias = root / "linked directory"
                alias.symlink_to(physical, target_is_directory=True)
                commands = root / "commands"
                commands.mkdir()
                npm = commands / "npm"
                npm.write_text(
                    '#!/bin/sh\n'
                    '[ "$1" = ci ] && [ "$2" = --prefix ] || exit 78\n'
                    '[ -f "$3/package.json" ] && [ -f "$3/package-lock.json" ] || exit 79\n'
                    'printf "%s\\n" "$3" > "$NPM_PATH_RECORD"\n'
                    'exit 77\n'
                )
                npm.chmod(0o755)
                uname = commands / "uname"
                uname.write_text('#!/bin/sh\ncase "$1" in -s) echo Darwin ;; -m) echo arm64 ;; esac\n')
                uname.chmod(0o755)
                record = root / "npm-path"
                env = dict(os.environ, PATH=f"{commands}{os.pathsep}{os.environ['PATH']}",
                           TMPDIR=str(alias), NPM_PATH_RECORD=str(record))
                argument = root / "archive.tar.gz" if script == "build.sh" else alias / "scratch"
                result = subprocess.run(
                    ["sh", str(ROOT / "release/opencode" / script), str(argument)],
                    env=env, text=True, capture_output=True,
                )
                self.assertEqual(result.returncode, 77, result.stdout + result.stderr)
                prefix = Path(record.read_text().strip())
                self.assertEqual(prefix, prefix.resolve())
                self.assertEqual(prefix.parent, physical)
                if script == "build.sh":
                    self.assertFalse(prefix.exists(), "The build must remove its temporary directory.")
