import datetime
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from publish import CHECKS, file_record, latest_public_release, main, publish, test_release_is_new, test_releases_enabled, write_decision

COMMIT = "a" * 40
IMAGE = "ghcr.io/example/culler/runner@sha256:" + "b" * 64
NOW = datetime.datetime(2026, 9, 26, tzinfo=datetime.timezone.utc)
AGENTSVIEW = {"version": "0.44.0", "archive_sha256": "e" * 64, "executable_sha256": "f" * 64}
NOTES = "### Changes\n\n- A change.\n"
HOST_BUN = {"version": "1.3.9", "archives": {"darwin-arm64": {
    "name": "bun-darwin-aarch64.zip",
    "url": "https://github.com/oven-sh/bun/releases/download/bun-v1.3.9/bun-darwin-aarch64.zip",
    "size": 22000000, "sha256": "d" * 64}}}


def latest_release(version="0.1.0"):
    formats = {name: "1" for name in ("configuration", "credentials", "claude", "codex", "opencode", "opencode-websearch", "openwiki", "agentsview")}
    formats["spine"] = "c" * 64
    record = {"version": version, "schema": formats["spine"],
              "update": {"minimum_version": "0.1.0", "formats": formats,
                         "accepted": {name: [value] for name, value in formats.items()}},
              "dependencies": [{"path": "fixture.json", "sha256": "a" * 64}]}
    return {"tag_name": "v" + version,
            "assets": [{"id": 1000, "name": "release.json", "body": json.dumps(record).encode()}]}


class FakeGitHub:
    def __init__(self, latest=None, relation="ahead", corrupt=None):
        self.latest = latest
        self.relation = relation
        self.corrupt = corrupt
        self.assets = {}
        self.published = []
        self.created = []
        self.release = None
        self.compared = []

    def request(self, method, path, body=None, binary=False):
        if method == "GET":
            if path == "/releases/latest":
                return self.latest
            if path.endswith("...main"):
                return {"status": "identical"}
            if path.startswith("/compare/"):
                self.compared.append(path)
                return {"status": self.relation}
            if path.startswith("/releases/tags/"):
                return self.release if self.release and self.release["tag_name"] == path.rsplit("/", 1)[1] else None
            if path.startswith("/actions/runs/"):
                return {"created_at": "2026-09-25T23:59:00Z"}
            if path.startswith("/releases/assets/"):
                asset_id = int(path.rsplit("/", 1)[1])
                if asset_id == 1000:
                    return self.latest["assets"][0]["body"]
                asset = self.assets[asset_id]
                return b"corrupt" if asset["name"] == self.corrupt else asset["body"]
        if method == "POST" and path == "/releases":
            self.created.append(body)
            self.release = {"id": 1, "tag_name": body["tag_name"], "draft": True, "assets": [], "upload_url": "https://uploads.github.com/releases/1/assets{?name}"}
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
    def fixture(self, root):
        output = root / "output"
        assets = output / "assets"
        assets.mkdir(parents=True)
        for name in ("install.sh", "installer.sh", "culler-darwin-arm64.tar.gz", "culler-opencode-linux-arm64.tar.gz"):
            (assets / name).write_bytes(b"verified fixture " + name.encode())
        package = file_record(assets / "culler-darwin-arm64.tar.gz")
        package["schema"] = "c" * 64
        package["runtimes"] = {"go": "1.25.12", "bun": "1.3.9"}
        package["agentsview"] = dict(AGENTSVIEW)
        package["host_bun"] = json.loads(json.dumps(HOST_BUN))
        package["version"] = "0.2.0"
        formats = {name: "1" for name in ("configuration", "credentials", "claude", "codex", "opencode", "opencode-websearch", "openwiki", "agentsview")}
        formats["spine"] = package["schema"]
        package["update"] = {"minimum_version": "0.1.0", "formats": formats, "accepted": {name: [value] for name, value in formats.items()}}
        package["dependencies"] = [{"path": "fixture.json", "sha256": "a" * 64}]
        (output / "package.json").write_text(json.dumps(package))
        (output / "opencode.json").write_text(json.dumps(file_record(assets / "culler-opencode-linux-arm64.tar.gz")))
        (output / "sequence").write_text("17\n")
        (output / "version").write_text("0.2.0\n")
        (output / "release-notes.md").write_text(NOTES)
        return output

    def test_policy_switch_controls_the_workflow_decision_and_publication(self):
        env = {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main",
               "GITHUB_SHA": COMMIT, "GITHUB_REPOSITORY": "example/culler", "GH_TOKEN": "fixture", "GITHUB_RUN_ID": "1"}
        for enabled in (True, False):
            with self.subTest(enabled=enabled), mock.patch.dict(os.environ, env):
                with mock.patch("publish.test_releases_enabled", return_value=enabled), \
                     mock.patch("publish.GitHub", return_value=FakeGitHub(latest=latest_release("0.2.0"))), \
                     mock.patch("publish.read_version", return_value="0.2.0"), \
                     mock.patch("publish.write_decision") as decision, \
                     mock.patch("sys.argv", ["publish.py", "--decide"]):
                    main()
                    decision.assert_called_once_with("0.2.0", enabled, enabled)
                with mock.patch("publish.test_releases_enabled", return_value=enabled), \
                     mock.patch("publish.GitHub") as github, \
                     mock.patch("publish.publish", return_value=True) as publication, \
                     mock.patch("publish.subprocess.run"), \
                     mock.patch("sys.argv", ["publish.py", "--output", "/fixture", "--image", IMAGE, "--project-image", IMAGE]):
                    main()
                    publication.assert_called_once_with(github.return_value, Path("/fixture"), COMMIT, IMAGE, "1",
                                                        project_image=IMAGE, test_release=enabled)

    def test_policy_switch_defaults_to_public_releases_and_requires_a_boolean(self):
        for value, expected in (({}, False), ({"test_releases": False}, False), ({"test_releases": True}, True)):
            with self.subTest(value=value), mock.patch.object(Path, "read_text", return_value=json.dumps(value)):
                self.assertEqual(test_releases_enabled(), expected)
        for value in ("true", 1, None):
            with self.subTest(value=value), mock.patch.object(Path, "read_text", return_value=json.dumps({"test_releases": value})):
                with self.assertRaisesRegex(ValueError, "boolean"):
                    test_releases_enabled()

    def test_test_releases_allow_reused_and_lower_versions_and_changed_formats(self):
        for version in ("0.2.0", "9.0.0"):
            with self.subTest(previous=version), tempfile.TemporaryDirectory() as directory:
                api = FakeGitHub(latest=latest_release(version))
                output = self.fixture(Path(directory))
                package = json.loads((output / "package.json").read_text())
                package["schema"] = "d" * 64
                package["update"]["formats"]["spine"] = package["schema"]
                package["update"]["accepted"]["spine"] = [package["schema"]]
                (output / "package.json").write_text(json.dumps(package))
                self.assertTrue(publish(api, output, COMMIT, IMAGE, 1, NOW, test_release=True))
                record = json.loads((output / "assets/release.json").read_text())
                self.assertEqual(record["tag"], "test-" + COMMIT)
                self.assertTrue(record["test_release"])
                self.assertFalse(api.created[0]["prerelease"])
                self.assertEqual(api.compared, [])
                self.assertEqual(len(api.assets), 5)
                api.release["draft"] = False
                self.assertFalse(test_release_is_new(api, COMMIT))
                self.assertFalse(publish(api, output, COMMIT, IMAGE, 1, NOW, test_release=True))
                next_commit = "e" * 40
                self.assertTrue(publish(api, output, next_commit, IMAGE, 2, NOW, test_release=True))
                self.assertEqual([item["tag_name"] for item in api.created], ["test-" + COMMIT, "test-" + next_commit])

    def test_test_releases_keep_download_checks(self):
        with tempfile.TemporaryDirectory() as directory:
            api = FakeGitHub(corrupt="installer.sh")
            with self.assertRaisesRegex(ValueError, "uploaded release file differs"):
                publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW, test_release=True)
            self.assertEqual(api.published, [])

    def test_an_older_workflow_cannot_replace_a_later_test_release(self):
        for relation in ("behind", "diverged", "identical"):
            with self.subTest(relation=relation), tempfile.TemporaryDirectory() as directory:
                api = FakeGitHub(latest={"tag_name": "test-" + "e" * 40}, relation=relation)
                self.assertFalse(publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW, test_release=True))
                self.assertEqual(api.created, [])
                self.assertEqual(api.published, [])

    def test_a_later_test_commit_can_publish_the_same_version(self):
        with tempfile.TemporaryDirectory() as directory:
            api = FakeGitHub(latest={"tag_name": "test-" + "e" * 40})
            self.assertTrue(publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW, test_release=True))
            self.assertEqual(len(api.published), 1)

    def test_public_release_selection_ignores_test_releases_and_reads_all_pages(self):
        previous = latest_release()
        calls = []

        class API:
            def request(self, method, path):
                calls.append(path)
                test = {"tag_name": "test-" + COMMIT}
                if path == "/releases/latest":
                    return test
                if path.endswith("page=1"):
                    return [test] * 100
                return [{"tag_name": "v99.0.0", "draft": True}, {"tag_name": "v98.0.0", "prerelease": True}, previous]

        self.assertEqual(latest_public_release(API()), previous)
        self.assertEqual(len(calls), 3)

    def test_publication_follows_all_download_checks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            output = self.fixture(root)
            api = FakeGitHub()
            self.assertTrue(publish(api, output, COMMIT, IMAGE, 1, NOW))
            self.assertEqual(api.published, [{"name": "Culler 0.2.0", "body": NOTES, "draft": False, "make_latest": "true"}])
            self.assertEqual(api.created[0]["name"], "Culler 0.2.0")
            self.assertEqual(api.created[0]["body"], NOTES)
            self.assertEqual(api.created[0]["tag_name"], "v0.2.0")
            self.assertFalse(api.created[0]["prerelease"])
            record = json.loads((output / "assets/release.json").read_text())
            self.assertEqual(record["version"], "0.2.0")
            self.assertEqual(record["format"], 1)
            self.assertEqual(record["tag"], "v0.2.0")
            self.assertEqual(record["release_id"], COMMIT)
            self.assertEqual(record["sequence"], 17)
            self.assertEqual(record["commit"], COMMIT)
            self.assertEqual(record["workflow_seconds"], 60)
            self.assertEqual(record["runtimes"], {"go": "1.25.12", "bun": "1.3.9"})
            self.assertEqual(record["agentsview"], AGENTSVIEW)
            self.assertEqual(record["host_bun"], HOST_BUN)
            self.assertEqual(record["checks"], [{"name": name, "passed": True} for name in CHECKS])
            self.assertEqual(len(api.assets), 5)
            self.assertEqual(record["opencode"], file_record(output / "assets/culler-opencode-linux-arm64.tar.gz"))
            package = json.loads((output / "package.json").read_text())
            self.assertEqual(record["update"], package["update"])
            self.assertEqual(record["dependencies"], package["dependencies"])

    def test_a_package_version_must_match_the_release_input(self):
        with tempfile.TemporaryDirectory() as directory:
            output = self.fixture(Path(directory))
            (output / "version").write_text("0.3.0\n")
            api = FakeGitHub()
            with self.assertRaisesRegex(ValueError, "package version differs"):
                publish(api, output, COMMIT, IMAGE, 1, NOW)
            self.assertEqual(api.assets, {})
            self.assertEqual(api.published, [])

    def test_the_previous_release_requires_one_compatible_record(self):
        for failure in ("absent", "duplicate", "version", "format", "schema"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                latest = latest_release("0.1.1" if failure == "schema" else "0.1.0")
                previous = json.loads(latest["assets"][0]["body"])
                if failure == "absent":
                    latest["assets"] = []
                elif failure == "duplicate":
                    latest["assets"] *= 2
                else:
                    if failure == "version":
                        previous["version"] = "0.1.1"
                    elif failure == "format":
                        previous["update"]["formats"]["opencode"] = "older"
                        previous["update"]["accepted"]["opencode"] = ["older"]
                    else:
                        previous["schema"] = "d" * 64
                        previous["update"]["formats"]["spine"] = previous["schema"]
                        previous["update"]["accepted"]["spine"] = [previous["schema"]]
                    latest["assets"][0]["body"] = json.dumps(previous).encode()
                api = FakeGitHub(latest=latest)
                with self.assertRaises(ValueError):
                    publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW)
                self.assertEqual(api.assets, {})
                self.assertEqual(api.published, [])

    def test_an_unsupported_previous_release_during_upload_blocks_publication(self):
        class ConcurrentGitHub(FakeGitHub):
            reads = 0

            def request(self, method, path, body=None, binary=False):
                if method == "GET" and path == "/releases/latest":
                    self.reads += 1
                    if self.reads > 1:
                        self.latest = latest_release("0.1.1")
                        record = json.loads(self.latest["assets"][0]["body"])
                        record["update"]["formats"]["opencode"] = "older"
                        record["update"]["accepted"]["opencode"] = ["older"]
                        self.latest["assets"][0]["body"] = json.dumps(record).encode()
                return super().request(method, path, body, binary)

        with tempfile.TemporaryDirectory() as directory:
            api = ConcurrentGitHub(latest=latest_release())
            with self.assertRaisesRegex(ValueError, "opencode"):
                publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW)
            self.assertEqual(len(api.assets), 5)
            self.assertEqual(api.published, [])

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

    def test_a_higher_version_replaces_the_latest_release(self):
        for previous in ("0.1.0", "0.1.9"):
            with self.subTest(previous=previous), tempfile.TemporaryDirectory() as directory:
                api = FakeGitHub(latest=latest_release(previous))
                self.assertTrue(publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW))
                self.assertEqual(api.compared[-1], "/compare/v" + previous + "..." + COMMIT)
                self.assertEqual(len(api.published), 1)

    def test_the_version_of_the_latest_release_publishes_nothing(self):
        with tempfile.TemporaryDirectory() as directory:
            api = FakeGitHub(latest=latest_release("0.2.0"))
            self.assertFalse(publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW))
            self.assertEqual(api.published, [])
            self.assertEqual(api.assets, {})

    def test_a_lower_version_fails_publication(self):
        for previous in ("0.3.0", "0.10.0", "1.0.0"):
            with self.subTest(previous=previous), tempfile.TemporaryDirectory() as directory:
                api = FakeGitHub(latest=latest_release(previous))
                with self.assertRaisesRegex(ValueError, "lower than version " + previous):
                    publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW)
                self.assertEqual(api.published, [])
                self.assertEqual(api.assets, {})

    def test_a_higher_version_requires_a_later_commit(self):
        for relation in ("behind", "identical", "diverged"):
            with self.subTest(relation=relation), tempfile.TemporaryDirectory() as directory:
                api = FakeGitHub(latest=latest_release("0.1.0"), relation=relation)
                with self.assertRaisesRegex(ValueError, "requires a commit after"):
                    publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW)
                self.assertEqual(api.published, [])

    def test_a_latest_release_without_a_version_tag_fails_publication(self):
        for tag in ("v0.1", "0.1.0", "v01.0.0", "culler-" + "d" * 40, ""):
            with self.subTest(tag=tag), tempfile.TemporaryDirectory() as directory:
                api = FakeGitHub(latest={"tag_name": tag})
                with self.assertRaisesRegex(ValueError, "not a version tag"):
                    publish(api, self.fixture(Path(directory)), COMMIT, IMAGE, 1, NOW)
                self.assertEqual(api.published, [])

    def test_a_release_without_notes_or_with_a_malformed_version_is_not_published(self):
        for name, text in (("release-notes.md", "\n"), ("version", "0.2\n")):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                output = self.fixture(Path(directory))
                (output / name).write_text(text)
                api = FakeGitHub()
                with self.assertRaises(ValueError):
                    publish(api, output, COMMIT, IMAGE, 1, NOW)
                self.assertEqual(api.assets, {})

    def test_the_same_version_published_during_upload_preserves_availability(self):
        class ConcurrentGitHub(FakeGitHub):
            reads = 0

            def request(self, method, path, body=None, binary=False):
                if method == "GET" and path == "/releases/latest":
                    self.reads += 1
                    if self.reads > 1:
                        self.latest = latest_release("0.2.0")
                return super().request(method, path, body, binary)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            api = ConcurrentGitHub()
            self.assertFalse(publish(api, self.fixture(root), COMMIT, IMAGE, 1, NOW))
            self.assertEqual(api.published, [])
            self.assertEqual(len(api.assets), 5)

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

    def test_the_decision_reaches_the_step_outputs_and_the_summary(self):
        for new, value in ((True, "publish=true\n"), (False, "publish=false\n")):
            with self.subTest(new=new), tempfile.TemporaryDirectory() as directory:
                outputs, summary = Path(directory) / "outputs", Path(directory) / "summary"
                with mock.patch.dict(os.environ, {"GITHUB_OUTPUT": str(outputs), "GITHUB_STEP_SUMMARY": str(summary)}), \
                        mock.patch("builtins.print"):
                    write_decision("0.2.0", new)
                self.assertEqual(outputs.read_text(), value)
                self.assertIn("Version 0.2.0", summary.read_text())


if __name__ == "__main__":
    unittest.main()
