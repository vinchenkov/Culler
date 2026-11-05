#!/usr/bin/env python3
"""Prove secret coverage with the fixed scanners and synthetic credentials."""

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

from security_policy import apply_exceptions
from security_scan import Scanners, execute, files, history, image
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


def fixture_image(path, secret=False, metadata=False):
    project = {"tools": {"node": "v24.18.0", "bun": "1.3.12"}, "browser_versions": {"chrome": "150.0.0.1"}}
    first = tar_bytes({
        "etc/os-release": b'NAME="Alpine Linux"\nID=alpine\nVERSION_ID=3.23.0\n',
        "lib/apk/db/installed": b"P:example\nV:1.0.0-r0\nA:aarch64\nT:Test package\no:example\nL:MIT\n\n",
        "usr/share/culler-project/manifest.json": json.dumps(project).encode(),
        "opt/culler-runtime/manifest.json": json.dumps({"release": "a" * 40, "tools": {"node": "v24.18.0", "bun": "1.3.12"}}).encode(),
        "root/credential": (credential() if secret else "API_KEY").encode(),
    })
    second = tar_bytes({"root/.wh.credential": b""})
    config = {"architecture": "arm64", "os": "linux", "config": {"Env": ["EXAMPLE=" + credential()] if metadata else []},
              "rootfs": {"type": "layers", "diff_ids": ["sha256:" + hashlib.sha256(item).hexdigest() for item in (first, second)]},
              "history": [{"created_by": "fixture"}, {"created_by": "ENV EXAMPLE=" + credential() if metadata else "fixture"}]}
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
        self.report = {"scanners": {}, "secrets": [], "vulnerabilities": []}
        self.scanners = Scanners(TOOLS, self.scratch, self.report)

    def assert_masked_failure(self, location):
        self.assertFalse(apply_exceptions(self.report, {"secrets": [], "vulnerabilities": []}))
        self.assertTrue(credential() not in json.dumps(self.report), "A report disclosed the synthetic credential.")
        self.assertTrue(any(location in item["file"] for item in self.report["secrets"]), self.report["secrets"])

    def test_deleted_commit_credential_fails_with_its_commit_and_file(self):
        repo = self.root / "repo"
        repo.mkdir()
        def git(*args):
            return subprocess.check_output(["git", "-C", str(repo), "-c", "user.name=Security test",
                                            "-c", "user.email=security@example.invalid", *args], stderr=subprocess.DEVNULL).decode().strip()
        git("init", "-b", "main")
        (repo / "credential").write_text(credential())
        (repo / "credential.bin").write_bytes(b"\0" + credential().encode())
        (repo / "archive.tar").write_bytes(tar_bytes({"credential": credential().encode()}))
        (repo / ".gitattributes").write_text("credential -diff\n")
        git("add", ".")
        git("commit", "-m", "Add the synthetic credential")
        first = git("rev-parse", "HEAD")
        git("rm", "credential", "credential.bin", "archive.tar")
        git("commit", "-m", "Delete the synthetic credential")
        history(SimpleNamespace(repo=repo, ref=["refs/heads/main"], public_refs=False), self.scanners, self.report)
        self.assert_masked_failure("credential")
        self.assertIn(first, [item["commit"] for item in self.report["secrets"]])
        self.assertEqual(self.report["coverage"]["commits"], 2)
        self.assertTrue(any(item["file"] == "credential.bin" for item in self.report["secrets"]))
        self.assertTrue(any(item["file"] == "archive.tar!credential" for item in self.report["secrets"]))

    def test_missing_layer_fails_the_required_check(self):
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

    def test_archive_credential_fails_with_its_member_path(self):
        assets = self.root / "assets"
        assets.mkdir()
        nested = io.BytesIO()
        with zipfile.ZipFile(nested, mode="w", compression=zipfile.ZIP_DEFLATED) as archive:
            archive.writestr("credential", credential())
        (assets / "application.tar.gz").write_bytes(gzip.compress(tar_bytes({"host/nested.zip": nested.getvalue()})))
        files(SimpleNamespace(directory=assets), self.scanners, self.report)
        self.assert_masked_failure("application.tar.gz!host/nested.zip!credential")

    def test_deleted_layer_and_metadata_credentials_fail_without_values(self):
        archive = self.root / "image.tar"
        fixture_image(archive, secret=True, metadata=True)
        original = self.scanners.trivy
        def secret_scan(archive, name):
            original(archive, name, vulnerabilities=False)
        with patch.object(self.scanners, "trivy", side_effect=secret_scan):
            image(SimpleNamespace(archive=archive, image="runner", vulnerabilities_only=False), self.scanners, self.report)
        self.assert_masked_failure("root/credential")
        self.assertTrue(any(item.get("layer") for item in self.report["secrets"]))
        self.assertTrue(any(item["scanner"] == "gitleaks" and item["file"] == "image-config.json" for item in self.report["secrets"]))
        self.assertTrue(any(item["scanner"] == "trivy" for item in self.report["secrets"]))
        self.assertEqual(len(self.report["coverage"]), 3)

    def test_variable_name_and_deleted_file_are_not_credentials(self):
        assets = self.root / "assets"
        assets.mkdir()
        (assets / "names.txt").write_text("API_KEY\nexport API_KEY=\n")
        files(SimpleNamespace(directory=assets), self.scanners, self.report)
        self.assertEqual(self.report["secrets"], [])

    def test_failed_tool_produces_an_unverified_masked_record(self):
        tools = self.root / "broken-tools"
        tools.mkdir()
        program = tools / "gitleaks"
        program.write_text("#!/bin/sh\nprintf '%s' '" + credential() + "' >&2\nexit 2\n")
        program.chmod(0o700)
        assets = self.root / "assets"
        assets.mkdir()
        (assets / "file").write_text("value")
        report = self.root / "failed.json"
        result = subprocess.run([sys.executable, str(Path(__file__).with_name("security_scan.py")), "files",
                                 "--directory", str(assets), "--tools", str(tools), "--report", str(report)], capture_output=True)
        self.assertEqual(result.returncode, 2)
        self.assertEqual(json.loads(report.read_text())["status"], "unverified")
        self.assertTrue(credential().encode() not in result.stdout + result.stderr + report.read_bytes(),
                        "A failed tool disclosed the synthetic credential.")

    def test_fresh_advisory_data_fails_the_unchanged_image(self):
        archive = self.root / "image.tar"
        identity = fixture_image(archive)
        for mode, passed in (("empty", True), ("advisory", False)):
            directory = self.root / mode
            directory.mkdir()
            report = {"image_id": identity, "scanners": {}, "secrets": [], "vulnerabilities": []}
            scanners = Scanners(TOOLS, directory, report)
            def controlled_download(command, **kwargs):
                if "--download-db-only" in command:
                    cache = Path(command[command.index("--cache-dir") + 1])
                    subprocess.run([str(FIXTURE_DB), str(cache / "db"), mode], check=True, capture_output=True)
                    return subprocess.CompletedProcess(command, 0, b"", b"")
                return execute(command, **kwargs)
            with patch("security_scan.execute", side_effect=controlled_download):
                scanners.trivy(archive, "runner")
            self.assertEqual(apply_exceptions(report, {"secrets": [], "vulnerabilities": []}), passed)
            self.assertEqual(report["image_id"], identity)
            self.assertTrue(report["advisory_data"]["sha256"])
            if not passed:
                item = report["vulnerabilities"][0]
                self.assertEqual((item["package"], item["version"], item["advisory"], item["severity"], item["fixed_version"]),
                                 ("example", "1.0.0-r0", "CVE-2099-1234", "HIGH", "1.1.0-r0"))


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
