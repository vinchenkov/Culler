import fcntl
import hashlib
import io
import json
import os
import subprocess
import tarfile
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch

from validate_package import DOWNLOAD_URL, LOCK_REFUSAL, PACKAGE_NAME, archive_files, validate

AGENTSVIEW = b"synthetic agentsview"
HOST_BUN = {"version": "1.2.3", "archives": {"darwin-arm64": {
    "name": "bun-darwin-aarch64.zip", "url": "https://example.com/bun-v1.2.3/bun-darwin-aarch64.zip",
    "size": 22000000, "sha256": "d" * 64}}}


class PackageValidationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.output = Path(self.directory.name).resolve()
        (self.output / "assets").mkdir()
        self.archive = self.output / "assets" / PACKAGE_NAME
        self.pin = {
            "version": "1.2.3",
            "archive": {"sha256": "a" * 64},
            "executable": {"sha256": hashlib.sha256(AGENTSVIEW).hexdigest()},
        }
        pin_path = self.output / "agentsview.json"
        pin_path.write_text(json.dumps(self.pin))
        patcher = patch("validate_package.PIN_PATH", pin_path)
        patcher.start()
        self.addCleanup(patcher.stop)
        host_bun_pin_path = self.output / "hostbun.json"
        host_bun_pin_path.write_text(json.dumps(HOST_BUN))
        host_bun_patcher = patch("validate_package.HOST_BUN_PIN_PATH", host_bun_pin_path)
        host_bun_patcher.start()
        self.addCleanup(host_bun_patcher.stop)
        self.identity = {"version": "1.2.3", "archive_sha256": "a" * 64,
                         "executable_sha256": hashlib.sha256(AGENTSVIEW).hexdigest()}
        self.install_bun = True

    def package(self, extra=None, agentsview=AGENTSVIEW):
        entries = [(name, b"runtime", mode, tarfile.REGTYPE) for name, mode in (
            ("bin/mc", 0o700), ("bin/install.sh", 0o700),
            ("host/resident/main.ts", 0o600), ("runner/main.ts", 0o600),
        )]
        if agentsview is not None:
            entries.append(("bin/agentsview", agentsview, 0o700, tarfile.REGTYPE))
        if extra:
            entries.extend(extra)
        with tarfile.open(self.archive, "w:gz") as writer:
            for name, data, mode, kind in entries:
                entry = tarfile.TarInfo(name)
                entry.mode, entry.type = mode, kind
                entry.size = len(data) if kind == tarfile.REGTYPE else 0
                if kind == tarfile.SYMTYPE:
                    entry.linkname = "/outside"
                writer.addfile(entry, io.BytesIO(data))
        metadata = {"name": PACKAGE_NAME, "size": self.archive.stat().st_size,
                    "sha256": hashlib.sha256(self.archive.read_bytes()).hexdigest(),
                    "commit": "a" * 40, "schema": "b" * 64,
                    "runtimes": {"bun": "1.3.9", "go": "1.25.12"}, "agentsview": dict(self.identity),
                    "host_bun": json.loads(json.dumps(HOST_BUN))}
        (self.output / "package.json").write_text(json.dumps(metadata))
        (self.output / "assets" / "installer.sh").write_text("#!/bin/sh\nexit 99\n")
        (self.output / "version").write_text("0.1.0\n")

    def handoff(self, command, *, cwd, env, **options):
        setup = cwd.name == "setup"
        self.assertEqual(command, ["/bin/sh", str(self.output / "assets" / "installer.sh")] + ([] if setup else ["--check-updates"]))
        self.assertEqual(options["stdin"], subprocess.DEVNULL)
        self.assertNotIn("GH_TOKEN", env)
        self.assertNotIn("MC_HELPER", env)
        self.assertNotIn("GOFLAGS", env)
        self.assertFalse(Path(env["CULLER_GITHUB_TOKEN_FILE"]).exists())
        self.assertEqual(Path(env["HOME"]).parent, cwd)
        self.assertEqual(Path(env["MC_HOME"]).parent, cwd)
        self.assertEqual(env["CULLER_BOOTSTRAP_DIR"], str(cwd / "alias") + "//bootstrap")
        bootstrap = Path(env["CULLER_BOOTSTRAP_DIR"])
        self.assertEqual(bootstrap.resolve(), cwd / "bootstrap")
        record = json.loads((bootstrap / "release.json").read_text())
        self.assertEqual(record["commit"], "a" * 40)
        self.assertEqual(record["version"], "0.1.0")
        self.assertEqual(record["schema"], "b" * 64)
        self.assertEqual(record["release_id"], record["commit"])
        self.assertEqual(record["tag"], "v0.1.0")
        self.assertEqual(record["runtimes"], {"bun": "1.3.9", "go": "1.25.12"})
        self.assertEqual(record["agentsview"], self.identity)
        self.assertEqual(record["package"]["sha256"], hashlib.sha256(self.archive.read_bytes()).hexdigest())
        # The record names the pinned Bun release and a local archive in place of the pinned one.
        bun_archive = Path(env["CULLER_PACKAGE_CHECK_BUN_ARCHIVE"])
        pinned = HOST_BUN["archives"]["darwin-arm64"]
        self.assertEqual(record["host_bun"]["version"], HOST_BUN["version"])
        self.assertEqual(env["CULLER_PACKAGE_CHECK_BUN_URL"], pinned["url"])
        archive = record["host_bun"]["archives"]["darwin-arm64"]
        self.assertEqual((archive["name"], archive["url"]), (pinned["name"], pinned["url"]))
        self.assertEqual(archive["sha256"], hashlib.sha256(bun_archive.read_bytes()).hexdigest())
        self.assertEqual(archive["size"], bun_archive.stat().st_size)
        self.assertNotEqual(archive["sha256"], pinned["sha256"])
        mise = Path(env["PATH"].split(":")[0]) / "mise"
        self.assertEqual(mise.read_text().splitlines()[-1], "exit 93")
        with (Path(env["MC_HOME"]) / "install.lock").open() as lock:
            with self.assertRaises(BlockingIOError):
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        target = bootstrap / "package"
        target.mkdir(mode=0o700)
        with tarfile.open(self.archive) as reader:
            for member in reader:
                destination = target / member.name
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(reader.extractfile(member).read())
                destination.chmod(member.mode)
        fetches = DOWNLOAD_URL + "\n"
        if setup:
            fetches += env["CULLER_PACKAGE_CHECK_BUN_URL"] + "\n"
            if self.install_bun:
                with zipfile.ZipFile(bun_archive) as reader:
                    program = Path(env["MC_HOME"]) / "bun" / record["host_bun"]["version"] / "bin" / "bun"
                    program.parent.mkdir(parents=True)
                    program.write_bytes(reader.read("bun-darwin-aarch64/bun"))
                    program.chmod(0o755)
        Path(env["CULLER_PACKAGE_CHECK_FETCHES"]).write_text(fetches)
        code, message = (2, LOCK_REFUSAL) if setup else (1, "GitHub refused access.")
        return subprocess.CompletedProcess(command, code, "Culler passed the download checks.\n" + message + "\n")

    def test_validation_checks_staged_bytes_and_uses_a_private_environment(self):
        self.package()
        with patch.dict(os.environ, {"GH_TOKEN": "fixture-secret", "MC_HELPER": "live-helper", "GOFLAGS": "-tags=test_scratch_spine"}):
            with patch("validate_package.subprocess.run", side_effect=self.handoff) as run:
                result = validate(self.output)
        self.assertEqual(result["files"], 5)
        self.assertTrue(result["installer_handoff"])
        self.assertTrue(result["setup_handoff"])
        self.assertEqual(run.call_count, 2)
        for call in run.call_args_list:
            self.assertFalse(Path(call.kwargs["cwd"]).exists())

    def test_setup_path_refusal_fails_after_a_successful_status_check(self):
        self.package()

        def path_refusal(*args, **kwargs):
            result = self.handoff(*args, **kwargs)
            if kwargs["cwd"].name == "setup":
                result.stdout = "Culler passed the download checks.\nThe application package paths differ from the selected release.\n"
            return result

        with patch("validate_package.subprocess.run", side_effect=path_refusal) as run:
            with self.assertRaisesRegex(ValueError, "application package paths differ"):
                validate(self.output)
            self.assertEqual(run.call_count, 2)

    def test_missing_or_invalid_commit_fails_before_program_execution(self):
        for commit in (None, "", "short", "a" * 39, "A" * 40):
            with self.subTest(commit=commit):
                self.package()
                path = self.output / "package.json"
                metadata = json.loads(path.read_text())
                metadata["commit"] = commit
                path.write_text(json.dumps(metadata))
                with patch("validate_package.subprocess.run") as run:
                    with self.assertRaisesRegex(ValueError, "lacks its commit"):
                        validate(self.output)
                    run.assert_not_called()

    def test_deployment_writes_before_the_lock_fail(self):
        self.package()

        def wrote_deployment(*args, **kwargs):
            result = self.handoff(*args, **kwargs)
            (Path(kwargs["env"]["MC_HOME"]) / "installed.json").write_text("{}")
            return result

        with patch("validate_package.subprocess.run", side_effect=wrote_deployment):
            with self.assertRaisesRegex(ValueError, "wrote deployment files"):
                validate(self.output)

    def test_unsafe_archive_files_fail_before_program_execution(self):
        for extra in (
            ("host/../outside", b"bad", 0o600, tarfile.REGTYPE),
            ("runner/link", b"", 0o600, tarfile.SYMTYPE),
            ("host/writable", b"bad", 0o666, tarfile.REGTYPE),
            ("bin/mc", b"duplicate", 0o700, tarfile.REGTYPE),
            ("private/state", b"private", 0o600, tarfile.REGTYPE),
        ):
            with self.subTest(name=extra[0]):
                self.package([extra])
                with patch("validate_package.subprocess.run") as run:
                    with self.assertRaises(ValueError):
                        validate(self.output)
                    run.assert_not_called()

    def test_a_package_without_agentsview_fails_before_program_execution(self):
        self.package(agentsview=None)
        with patch("validate_package.subprocess.run") as run:
            with self.assertRaisesRegex(ValueError, "required runtime"):
                validate(self.output)
            run.assert_not_called()

    def test_a_different_agentsview_fails_before_program_execution(self):
        self.package(agentsview=b"changed agentsview")
        with patch("validate_package.subprocess.run") as run:
            with self.assertRaisesRegex(ValueError, "AgentsView executable differs from its pin: expected SHA-256 "
                                        + self.identity["executable_sha256"]):
                validate(self.output)
            run.assert_not_called()

    def test_a_build_record_of_another_agentsview_fails_before_program_execution(self):
        for change in ({"version": "9.9.9"}, {"archive_sha256": "b" * 64}, None):
            with self.subTest(change=change):
                self.package()
                path = self.output / "package.json"
                metadata = json.loads(path.read_text())
                if change is None:
                    del metadata["agentsview"]
                else:
                    metadata["agentsview"].update(change)
                path.write_text(json.dumps(metadata))
                with patch("validate_package.subprocess.run") as run:
                    with self.assertRaisesRegex(ValueError, "different AgentsView release"):
                        validate(self.output)
                    run.assert_not_called()

    def test_a_build_record_of_another_host_bun_fails_before_program_execution(self):
        archive = HOST_BUN["archives"]["darwin-arm64"]
        for change in ({"version": "9.9.9"}, {"archives": {"darwin-arm64": {**archive, "sha256": "e" * 64}}}, None):
            with self.subTest(change=change):
                self.package()
                path = self.output / "package.json"
                metadata = json.loads(path.read_text())
                if change is None:
                    del metadata["host_bun"]
                else:
                    metadata["host_bun"].update(change)
                path.write_text(json.dumps(metadata))
                with patch("validate_package.subprocess.run") as run:
                    with self.assertRaisesRegex(ValueError, "different host Bun release"):
                        validate(self.output)
                    run.assert_not_called()

    def test_a_setup_handoff_without_the_host_bun_fails(self):
        self.package()
        self.install_bun = False
        with patch("validate_package.subprocess.run", side_effect=self.handoff):
            with self.assertRaisesRegex(ValueError, "host Bun directory"):
                validate(self.output)

    def test_a_setup_handoff_with_a_changed_host_bun_fails(self):
        self.package()

        def changed(*args, **kwargs):
            result = self.handoff(*args, **kwargs)
            if kwargs["cwd"].name == "setup":
                program = Path(kwargs["env"]["MC_HOME"]) / "bun" / "1.2.3" / "bin" / "bun"
                program.write_text("#!/bin/sh\nexit 1\n")
            return result

        with patch("validate_package.subprocess.run", side_effect=changed):
            with self.assertRaisesRegex(ValueError, "differs from the archive"):
                validate(self.output)

    def test_a_package_without_runtime_files_fails(self):
        with tarfile.open(self.archive, "w:gz"):
            pass
        with self.assertRaisesRegex(ValueError, "required runtime"):
            archive_files(self.archive)

    def test_a_changed_archive_fails_before_program_execution(self):
        self.package()
        with self.archive.open("ab") as file:
            file.write(b"changed")
        with patch("validate_package.subprocess.run") as run:
            with self.assertRaisesRegex(ValueError, "differs from its build record"):
                validate(self.output)
            run.assert_not_called()

    def test_a_changed_staged_file_fails(self):
        self.package()

        def changed(*args, **kwargs):
            result = self.handoff(*args, **kwargs)
            (Path(kwargs["env"]["CULLER_BOOTSTRAP_DIR"]) / "package/bin/mc").write_bytes(b"different")
            return result

        with patch("validate_package.subprocess.run", side_effect=changed):
            with self.assertRaisesRegex(ValueError, "staged file differs"):
                validate(self.output)

    def test_a_failed_executable_handoff_fails(self):
        self.package()
        with patch("validate_package.subprocess.run", return_value=subprocess.CompletedProcess([], 2, "invalid executable")):
            with self.assertRaisesRegex(ValueError, "expected local check"):
                validate(self.output)
