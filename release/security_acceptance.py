#!/usr/bin/env python3
"""Check the secret and vulnerability reports with the fixed scanners and controlled fixtures."""

import argparse
import gzip
import hashlib
import io
import json
import os
import random
import string
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from security_scan import Scanners, execute, history
from security_tools import PINS

TOOLS = None
FIXTURE_DB = None


def credential():
    generator = random.Random(724691)
    return "gh" + "p_" + "".join(generator.choices(string.ascii_letters + string.digits, k=36))


def tar_bytes(files):
    result = io.BytesIO()
    with tarfile.open(fileobj=result, mode="w") as archive:
        for name, body in files.items():
            entry = tarfile.TarInfo(name)
            entry.size = len(body)
            entry.mode = 0o644
            archive.addfile(entry, io.BytesIO(body))
    return result.getvalue()


def fixture_image(path):
    project = {"tools": {"node": "v24.18.0", "bun": "1.3.12"}, "browser_versions": {"chrome": "150.0.0.1"}}
    first = tar_bytes({
        "etc/os-release": b'NAME="Alpine Linux"\nID=alpine\nVERSION_ID=3.23.0\n',
        "lib/apk/db/installed": b"P:example\nV:1.0.0-r0\nA:aarch64\nT:Test package\no:example\nL:MIT\n\n",
        "usr/share/culler-project/manifest.json": json.dumps(project).encode(),
        "opt/culler-runtime/manifest.json": json.dumps({"release": "a" * 40, "tools": {"node": "v24.18.0", "bun": "1.3.12"}}).encode(),
        "root/notes": b"API_KEY",
    })
    second = tar_bytes({"root/.wh.notes": b""})
    config = {"architecture": "arm64", "os": "linux", "config": {"Env": []},
              "rootfs": {"type": "layers", "diff_ids": ["sha256:" + hashlib.sha256(item).hexdigest() for item in (first, second)]},
              "history": [{"created_by": "fixture"}, {"created_by": "fixture"}]}
    body = json.dumps(config).encode()
    name = hashlib.sha256(body).hexdigest() + ".json"
    manifest = [{"Config": name, "RepoTags": ["culler-runner:check"], "Layers": ["first/layer.tar", "second/layer.tar"]}]
    path.write_bytes(tar_bytes({"manifest.json": json.dumps(manifest).encode(), name: body,
                               "first/layer.tar": first, "second/layer.tar": second}))
    return "sha256:" + hashlib.sha256(body).hexdigest()


class ScannerAcceptance(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.scratch = self.root / "scratch"
        self.scratch.mkdir()
        self.report = {"scanners": {}, "secrets": []}
        self.scanners = Scanners(TOOLS, self.scratch, self.report)

    def repository(self, files):
        repo = self.root / "repo"
        repo.mkdir()
        def git(*args):
            return subprocess.check_output(["git", "-C", str(repo), "-c", "user.name=Security test",
                                            "-c", "user.email=security@example.invalid", *args], stderr=subprocess.DEVNULL).decode().strip()
        git("init", "-b", "main")
        for name, body in files.items():
            (repo / name).write_bytes(body)
        git("add", ".")
        git("commit", "-m", "Add the fixture files")
        return repo, git

    def assert_masked_result(self, location):
        self.assertTrue(credential() not in json.dumps(self.report), "A report disclosed the synthetic credential.")
        self.assertTrue(any(location == item["file"] for item in self.report["secrets"]), self.report["secrets"])

    def test_deleted_commit_credentials_are_reported_with_their_commit_and_file(self):
        nested = io.BytesIO()
        with zipfile.ZipFile(nested, mode="w", compression=zipfile.ZIP_DEFLATED) as archive:
            archive.writestr("credential", credential())
        repo, git = self.repository({
            "credential": credential().encode(),
            "credential.bin": b"\0" + credential().encode(),
            "archive.tar": tar_bytes({"credential": credential().encode()}),
            "application.tar.gz": gzip.compress(tar_bytes({"host/nested.zip": nested.getvalue()})),
            ".gitattributes": b"credential -diff\n",
        })
        first = git("rev-parse", "HEAD")
        git("rm", "credential", "credential.bin", "archive.tar", "application.tar.gz")
        git("commit", "-m", "Delete the synthetic credential")
        history(SimpleNamespace(repo=repo, ref=["refs/heads/main"], public_refs=False), self.scanners, self.report)
        for location in ("credential", "credential.bin", "archive.tar!credential", "application.tar.gz!host/nested.zip!credential"):
            with self.subTest(location=location):
                self.assert_masked_result(location)
        self.assertIn(first, [item["commit"] for item in self.report["secrets"]])
        self.assertEqual(self.report["coverage"]["commits"], 2)

    def test_variable_names_are_not_credentials(self):
        repo, _ = self.repository({"names.txt": b"API_KEY\nexport API_KEY=\n"})
        history(SimpleNamespace(repo=repo, ref=["refs/heads/main"], public_refs=False), self.scanners, self.report)
        self.assertEqual(self.report["secrets"], [])

    def test_failed_tool_produces_an_unverified_masked_record(self):
        tools = self.root / "broken-tools"
        tools.mkdir()
        program = tools / "gitleaks"
        program.write_text("#!/bin/sh\nprintf '%s' '" + credential() + "' >&2\nexit 2\n")
        program.chmod(0o700)
        repo, _ = self.repository({"file": b"value"})
        report = self.root / "failed.json"
        result = subprocess.run([sys.executable, str(Path(__file__).with_name("security_scan.py")), "history",
                                 "--repo", str(repo), "--ref", "refs/heads/main", "--tools", str(tools),
                                 "--report", str(report)], capture_output=True)
        self.assertEqual(result.returncode, 2)
        self.assertEqual(json.loads(report.read_text())["status"], "unverified")
        self.assertTrue(credential().encode() not in result.stdout + result.stderr + report.read_bytes(),
                        "A failed tool disclosed the synthetic credential.")

    def test_missing_layer_leaves_the_image_scan_unverified(self):
        archive = self.root / "incomplete.tar"
        fixture_image(archive)
        with tarfile.open(archive) as source:
            values = {entry.name: source.extractfile(entry).read() for entry in source if entry.isfile()}
        del values["first/layer.tar"]
        archive.write_bytes(tar_bytes(values))
        report = self.root / "incomplete.json"
        result = subprocess.run([sys.executable, str(Path(__file__).with_name("security_scan.py")), "image",
                                 "--archive", str(archive), "--image", "runner", "--tools", str(TOOLS),
                                 "--report", str(report)], capture_output=True)
        self.assertEqual(result.returncode, 2)
        self.assertEqual(json.loads(report.read_text())["status"], "unverified")

    def test_fresh_advisory_data_reports_a_new_advisory_for_the_unchanged_image(self):
        archive = self.root / "image.tar"
        identity = fixture_image(archive)
        for mode in ("empty", "advisory"):
            directory = self.root / mode
            directory.mkdir()
            report = {"image_id": identity, "scanners": {}, "vulnerabilities": []}
            scanners = Scanners(TOOLS, directory, report)
            def controlled_download(command, **kwargs):
                if "--download-db-only" in command:
                    cache = Path(command[command.index("--cache-dir") + 1])
                    subprocess.run([str(FIXTURE_DB), str(cache / "db"), mode], check=True, capture_output=True)
                    return subprocess.CompletedProcess(command, 0, b"", b"")
                return execute(command, **kwargs)
            with patch("security_scan.execute", side_effect=controlled_download):
                scanners.trivy(archive, "runner")
            self.assertEqual(report["image_id"], identity)
            self.assertTrue(report["advisory_data"]["sha256"])
            found = [(item["package"], item["version"], item["advisory"], item["severity"], item["fixed_version"])
                     for item in report["vulnerabilities"]]
            self.assertEqual(found, [] if mode == "empty" else [("example", "1.0.0-r0", "CVE-2099-1234", "HIGH", "1.1.0-r0")])


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tools", type=Path, required=True)
    parser.add_argument("--fixture-db", type=Path, required=True)
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    TOOLS = args.tools.resolve()
    FIXTURE_DB = args.fixture_db.resolve()
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(ScannerAcceptance)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    if args.report:
        pins = json.loads(PINS.read_text())
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps({"status": "passed" if result.wasSuccessful() else "failed",
            "tests": result.testsRun, "commit": os.environ.get("GITHUB_SHA", ""),
            "scanners": {name: item["version"] for name, item in pins.items()},
            "fixture_database_tool_sha256": hashlib.sha256(FIXTURE_DB.read_bytes()).hexdigest()}, indent=2) + "\n")
    raise SystemExit(0 if result.wasSuccessful() else 1)
