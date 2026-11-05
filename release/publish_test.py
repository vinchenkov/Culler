import datetime
import json
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path

from publish import CHECKS, file_record, publish
from security_scan import file_inputs

COMMIT = "a" * 40
IMAGE = "ghcr.io/example/culler/runner@sha256:" + "b" * 64
NOW = datetime.datetime(2026, 9, 26, tzinfo=datetime.timezone.utc)
AGENTSVIEW = {"version": "0.44.0", "archive_sha256": "e" * 64, "executable_sha256": "f" * 64}
HOST_BUN = {"version": "1.3.9", "archives": {"darwin-arm64": {
    "name": "bun-darwin-aarch64.zip",
    "url": "https://github.com/oven-sh/bun/releases/download/bun-v1.3.9/bun-darwin-aarch64.zip",
    "size": 22000000, "sha256": "d" * 64}}}


class FakeGitHub:
    def __init__(self, latest=None, relation="ahead", corrupt=None):
        self.latest = latest
        self.relation = relation
        self.corrupt = corrupt
        self.assets = {}
        self.published = []
        self.release = None

    def request(self, method, path, body=None, binary=False):
        if method == "GET":
            if path == "/releases/latest":
                return self.latest
            if path.endswith("...main"):
                return {"status": "identical"}
            if path.startswith("/compare/"):
                return {"status": self.relation}
            if path.startswith("/releases/tags/"):
                return self.release
            if path.startswith("/actions/runs/"):
                return {"created_at": "2026-09-25T23:59:00Z"}
            if path.startswith("/releases/assets/"):
                asset = self.assets[int(path.rsplit("/", 1)[1])]
                return b"corrupt" if asset["name"] == self.corrupt else asset["body"]
        if method == "POST" and path == "/releases":
            self.release = {"id": 1, "draft": True, "assets": [], "upload_url": "https://uploads.github.com/releases/1/assets{?name}"}
            return self.release
        if method == "POST" and "?name=" in path:
            name = path.split("?name=", 1)[1]
            asset = {"id": len(self.assets) + 1, "name": name, "body": body}
            self.assets[asset["id"]] = asset
            self.release["assets"].append(asset)
            return asset
        if method == "PATCH":
            self.published.append(body)
            return body
        raise AssertionError((method, path))


class PublishTest(unittest.TestCase):
    def setUp(self):
        self.scan = patch("publish.scan_release_files").start()
        self.scan.side_effect = lambda assets, output, commit, tools: {"inputs": file_inputs(assets)}
        self.addCleanup(patch.stopall)

    def fixture(self, root):
        output = root / "output"
        assets = output / "assets"
        assets.mkdir(parents=True)
        for name in ("install.sh", "installer.sh", "culler-darwin-arm64.tar.gz"):
            (assets / name).write_bytes(b"verified fixture " + name.encode())
        package = file_record(assets / "culler-darwin-arm64.tar.gz")
        package["schema"] = "c" * 64
        package["runtimes"] = {"go": "1.25.12", "bun": "1.3.9"}
        package["agentsview"] = dict(AGENTSVIEW)
        package["host_bun"] = json.loads(json.dumps(HOST_BUN))
        (output / "package.json").write_text(json.dumps(package))
        (output / "sequence").write_text("17\n")
        (output / "security-files.json").write_text(json.dumps({"kind": "files", "commit": COMMIT,
            "status": "passed", "inputs": file_inputs(assets)}))
        return output

    def test_a_failed_scan_prevents_every_release_upload(self):
        with tempfile.TemporaryDirectory() as directory:
            output = self.fixture(Path(directory))
            api = FakeGitHub()
            self.scan.side_effect = ValueError("The file check failed.")
            with self.assertRaises(ValueError):
                publish(api, output, COMMIT, IMAGE, 1, NOW)
            self.assertIsNone(api.release)
            self.assertEqual(api.assets, {})

    def test_replaced_files_fail_before_every_release_upload(self):
        with tempfile.TemporaryDirectory() as directory:
            output = self.fixture(Path(directory))
            (output / "assets/install.sh").write_text("replacement")
            api = FakeGitHub()
            with self.assertRaises(ValueError):
                publish(api, output, COMMIT, IMAGE, 1, NOW)
            self.assertEqual(api.assets, {})

    def test_a_file_change_after_the_scan_prevents_its_upload(self):
        with tempfile.TemporaryDirectory() as directory:
            output = self.fixture(Path(directory))
            def change(assets, output, commit, tools):
                checked = {"inputs": file_inputs(assets)}
                (assets / "install.sh").write_text("replacement after the scan")
                return checked
            self.scan.side_effect = change
            api = FakeGitHub()
            with self.assertRaises(ValueError):
                publish(api, output, COMMIT, IMAGE, 1, NOW)
            self.assertEqual(api.assets, {})

    def test_publication_follows_all_download_checks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            output = self.fixture(root)
            api = FakeGitHub()
            self.assertTrue(publish(api, output, COMMIT, IMAGE, 1, NOW))
            self.assertEqual(api.published, [{"draft": False, "make_latest": "true"}])
            record = json.loads((output / "assets/release.json").read_text())
            self.assertEqual(record["commit"], COMMIT)
            self.assertEqual(record["workflow_seconds"], 60)
            self.assertEqual(record["runtimes"], {"go": "1.25.12", "bun": "1.3.9"})
            self.assertEqual(record["agentsview"], AGENTSVIEW)
            self.assertEqual(record["host_bun"], HOST_BUN)
            self.assertEqual(record["checks"], [{"name": name, "passed": True} for name in CHECKS])
            self.assertEqual(len(api.assets), 4)

    def test_a_package_without_its_agentsview_release_is_not_published(self):
        for change in (None, {"version": "latest"}, {"executable_sha256": "F" * 64}, {"extra": "value"}):
            with self.subTest(change=change):
                with tempfile.TemporaryDirectory() as directory:
                    output = self.fixture(Path(directory))
                    package = json.loads((output / "package.json").read_text())
                    if change is None:
                        del package["agentsview"]
                    else:
                        package["agentsview"].update(change)
                    (output / "package.json").write_text(json.dumps(package))
                    api = FakeGitHub()
                    with self.assertRaisesRegex(ValueError, "AgentsView release"):
                        publish(api, output, COMMIT, IMAGE, 1, NOW)
                    self.assertEqual(api.published, [])
                    self.assertEqual(api.assets, {})

    def test_a_package_without_its_host_bun_release_is_not_published(self):
        archive = HOST_BUN["archives"]["darwin-arm64"]
        for change in (
            None,
            {"version": "latest"},
            {"archives": {}},
            {"archives": {"linux-arm64": archive}},
            {"archives": {"darwin-arm64": {**archive, "sha256": "D" * 64}}},
            {"archives": {"darwin-arm64": {**archive, "url": "http://example.com/bun.zip"}}},
            {"archives": {"darwin-arm64": {**archive, "size": 0}}},
            {"archives": {"darwin-arm64": {**archive, "name": "bun.tar.gz"}}},
            {"archives": {"darwin-arm64": {**archive, "mirror": "https://example.com"}}},
            {"extra": "value"},
        ):
            with self.subTest(change=change):
                with tempfile.TemporaryDirectory() as directory:
                    output = self.fixture(Path(directory))
                    package = json.loads((output / "package.json").read_text())
                    if change is None:
                        del package["host_bun"]
                    else:
                        package["host_bun"].update(change)
                    (output / "package.json").write_text(json.dumps(package))
                    api = FakeGitHub()
                    with self.assertRaisesRegex(ValueError, "host Bun release"):
                        publish(api, output, COMMIT, IMAGE, 1, NOW)
                    self.assertEqual(api.published, [])
                    self.assertEqual(api.assets, {})

    def test_bad_download_preserves_available_release(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            api = FakeGitHub(corrupt="installer.sh")
            with self.assertRaises(ValueError):
                publish(api, self.fixture(root), COMMIT, IMAGE, 1, NOW)
            self.assertEqual(api.published, [])

    def test_old_workflow_cannot_replace_newer_release(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            api = FakeGitHub(latest={"tag_name": "culler-" + "d" * 40}, relation="behind")
            self.assertFalse(publish(api, self.fixture(root), COMMIT, IMAGE, 1, NOW))
            self.assertEqual(api.published, [])
            self.assertEqual(api.assets, {})

    def test_diverged_history_requires_a_publication_decision(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            api = FakeGitHub(latest={"tag_name": "culler-" + "d" * 40}, relation="diverged")
            with self.assertRaises(ValueError):
                publish(api, self.fixture(root), COMMIT, IMAGE, 1, NOW)
            self.assertEqual(api.published, [])

    def test_newer_publication_during_upload_preserves_availability(self):
        class ConcurrentGitHub(FakeGitHub):
            reads = 0

            def request(self, method, path, body=None, binary=False):
                if method == "GET" and path == "/releases/latest":
                    self.reads += 1
                    if self.reads > 1:
                        self.latest = {"tag_name": "culler-" + "d" * 40}
                        self.relation = "behind"
                return super().request(method, path, body, binary)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            api = ConcurrentGitHub()
            self.assertFalse(publish(api, self.fixture(root), COMMIT, IMAGE, 1, NOW))
            self.assertEqual(api.published, [])
            self.assertEqual(len(api.assets), 4)

    def test_changed_draft_asset_is_not_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            output = self.fixture(root)
            api = FakeGitHub(corrupt="installer.sh")
            with self.assertRaises(ValueError):
                publish(api, output, COMMIT, IMAGE, 1, NOW)
            api.corrupt = None
            (output / "assets/installer.sh").write_text("different content")
            with self.assertRaises(ValueError):
                publish(api, output, COMMIT, IMAGE, 1, NOW)
            self.assertEqual(api.published, [])


if __name__ == "__main__":
    unittest.main()
