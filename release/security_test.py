import hashlib
import http.client
import io
import json
import tarfile
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest import mock

import security_tools
from security_content import Content
from security_policy import apply_exceptions, load_exceptions
from security_scan import bundled_tools, consume_trivy, summary


def trivy_data(vulnerabilities=(), packages=None):
    group = {"Target": "os", "Class": "os-pkgs", "Type": "debian",
             "Packages": packages if packages is not None else [{"Name": "example", "Version": "1.0"}]}
    if vulnerabilities:
        group["Vulnerabilities"] = [{"PkgName": "example", "InstalledVersion": "1.0", "VulnerabilityID": advisory,
                                     "Severity": severity, "FixedVersion": fixed} for advisory, severity, fixed in vulnerabilities]
    return {"SchemaVersion": 2, "Metadata": {"ImageID": "sha256:unchanged"}, "Results": [group]}


def image_report():
    return {"kind": "image", "image": "runner", "image_id": "sha256:unchanged", "vulnerabilities": []}


class SecurityReportTest(unittest.TestCase):
    def test_secret_exceptions_require_the_exact_location(self):
        item = {"scanner": "gitleaks", "subject": "commit:abc", "file": "fixture", "rule": "rule", "line": 1}
        for change, matched in (({}, True), ({"subject": "commit:def"}, False),
                                ({"file": "different"}, False), ({"line": 2}, False)):
            with self.subTest(change=change):
                report = {"secrets": [dict(item)]}
                exceptions = {"secrets": [{**item, **change, "reason": "The value is a synthetic fixture."}]}
                apply_exceptions(report, exceptions)
                self.assertEqual(report["secrets"][0].get("reason"), "The value is a synthetic fixture." if matched else None)

    def test_the_exceptions_hold_only_secret_results(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "exceptions.json"
            for data in ({"secrets": [], "vulnerabilities": []}, {"secrets": [{"scanner": "gitleaks", "reason": "partial"}]}):
                path.write_text(json.dumps(data))
                with self.assertRaises(ValueError):
                    load_exceptions(path)
        self.assertTrue(load_exceptions()["secrets"])

    def test_missing_bundled_versions_remain_unverified(self):
        with self.assertRaises(KeyError):
            bundled_tools({}, "runner")
        with self.assertRaises(ValueError):
            bundled_tools({"usr/share/culler-project/manifest.json": json.dumps({"tools": {"bun": "unknown"}, "browser_versions": {}})}, "project")

    def test_new_advisory_changes_the_result_for_the_same_image(self):
        before, after = image_report(), image_report()
        consume_trivy(trivy_data(), before, "runner")
        consume_trivy(trivy_data([("CVE-2099-1234", "CRITICAL", "1.1")]), after, "runner")
        self.assertEqual(before["vulnerabilities"], [])
        self.assertEqual(after["vulnerabilities"], [{"image": "runner", "target": "/", "package": "example", "version": "1.0",
                                                     "advisory": "CVE-2099-1234", "severity": "CRITICAL", "fixed_version": "1.1"}])
        self.assertEqual(before["image_id"], after["image_id"])

    def test_missing_inventory_or_wrong_image_remains_unverified(self):
        for data in ({"SchemaVersion": 2, "Metadata": {"ImageID": "wrong"}},
                     {"SchemaVersion": 2, "Metadata": {"ImageID": "sha256:unchanged"}, "Results": []}):
            with self.assertRaises(ValueError):
                consume_trivy(data, image_report(), "runner")

    def test_a_package_without_a_version_is_listed_in_the_report(self):
        report = image_report()
        consume_trivy(trivy_data(packages=[{"Name": "example", "Version": "1.0"}, {"Name": "mc"}]), report, "runner")
        self.assertEqual(report["packages_without_version"], ["/ mc"])

    def test_reports_omit_image_metadata(self):
        report = image_report()
        data = trivy_data()
        data["Metadata"]["ImageConfig"] = {"Env": ["private-value"]}
        consume_trivy(data, report, "runner")
        self.assertNotIn("private-value", json.dumps(report))

    def test_the_image_summary_counts_each_severity(self):
        report = image_report()
        consume_trivy(trivy_data([("CVE-1", "HIGH", "1.1"), ("CVE-2", "HIGH", ""), ("CVE-3", "LOW", "")]), report, "runner")
        report.update(status="complete", advisory_data={"metadata": {"UpdatedAt": "2026-11-05T00:00:00Z"}})
        text = summary(report)
        self.assertIn("- Packages: 1\n- Trivy vulnerability results: 3\n", text)
        self.assertIn("| HIGH | 2 | 1 |", text)
        self.assertIn("| LOW | 1 | 0 |", text)
        self.assertIn("| CRITICAL | 0 | 0 |", text)

    def test_the_history_summary_lists_each_result_and_its_exception(self):
        report = {"kind": "history", "status": "complete", "coverage": {"commits": 2}, "secrets": [
            {"file": "a|b", "commit": "c" * 40, "rule": "generic-api-key", "line": 3, "reason": "A fixture."},
            {"file": "other", "commit": "d" * 40, "rule": "generic-api-key", "line": 4}]}
        text = summary(report)
        self.assertIn("- Commits: 2\n- Gitleaks results: 2\n- Results without an exception", text)
        self.assertIn("`release/security/exceptions.json`: 1\n", text)
        self.assertIn("| a\\|b | cccccccccccc | generic-api-key | 3 | A fixture. |", text)
        self.assertIn("| other | dddddddddddd | generic-api-key | 4 | None |", text)

    def test_an_unverified_scan_names_its_last_stage(self):
        text = summary({"kind": "image", "image": "project", "status": "unverified", "stage": "Check the image with Trivy."})
        self.assertIn("The scan did not complete. The last stage was: Check the image with Trivy.", text)

    def test_archive_paths_never_write_outside_the_scan_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            body = io.BytesIO()
            with tarfile.open(fileobj=body, mode="w") as archive:
                entry = tarfile.TarInfo("../../escape")
                entry.size = 4
                archive.addfile(entry, io.BytesIO(b"text"))
            body.seek(0)
            content = Content(root / "files")
            content.add(body, {"file": "archive", "subject": "fixture"})
            self.assertFalse((root / "escape").exists())
            self.assertTrue(any(item["file"] == "archive!../../escape" for item in content.locations.values()))

    def test_links_with_archive_names_retain_their_target_text(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            body = io.BytesIO()
            with tarfile.open(fileobj=body, mode="w") as archive:
                for kind, name in ((tarfile.SYMTYPE, "usr/share/doc/x/changelog.Debian.gz"),
                                   (tarfile.LNKTYPE, "usr/share/doc/x/copy.tar")):
                    entry = tarfile.TarInfo(name)
                    entry.type = kind
                    entry.linkname = "../y/changelog.Debian.gz"
                    archive.addfile(entry)
            body.seek(0)
            content = Content(root / "files")
            content.add(body, {"file": "layer.tar", "subject": "fixture"})
            content.add(io.BytesIO(b"../z/notes.gz"), {"file": "notes.gz", "subject": "fixture"}, expand=False)
            texts = {item["file"]: (content.root / name).read_text() for name, item in content.locations.items()}
            self.assertEqual(texts["layer.tar!usr/share/doc/x/changelog.Debian.gz"], "../y/changelog.Debian.gz")
            self.assertEqual(texts["layer.tar!usr/share/doc/x/copy.tar"], "../y/changelog.Debian.gz")
            self.assertEqual(texts["notes.gz"], "../z/notes.gz")
            with self.assertRaises(ValueError):
                content.add(io.BytesIO(b"plain text"), {"file": "damaged.gz", "subject": "fixture"})


class ScannerDownloadTest(unittest.TestCase):
    BODY = b"scanner archive"
    RECORD = {"url": "https://example.invalid/scanner.tar.gz", "sha256": hashlib.sha256(BODY).hexdigest()}

    def download(self, responses):
        calls = []

        def urlopen(url, timeout):
            calls.append(url)
            response = responses.pop(0)
            if isinstance(response, BaseException):
                raise response
            return io.BytesIO(response)

        with mock.patch.object(security_tools.urllib.request, "urlopen", urlopen), \
                mock.patch.object(security_tools.time, "sleep") as sleep:
            try:
                return security_tools.download(self.RECORD), len(calls), [call.args[0] for call in sleep.call_args_list]
            except Exception as error:
                return error, len(calls), [call.args[0] for call in sleep.call_args_list]

    def server_error(self, code):
        return urllib.error.HTTPError(self.RECORD["url"], code, "error", {}, None)

    def test_temporary_failures_receive_two_more_attempts(self):
        for failure in (self.server_error(504), self.server_error(429), urllib.error.URLError("reset"),
                        TimeoutError(), http.client.IncompleteRead(b"partial")):
            with self.subTest(failure=type(failure).__name__):
                self.assertEqual(self.download([failure, self.BODY]), (self.BODY, 2, [5]))
        result, calls, sleeps = self.download([self.server_error(503)] * 3)
        self.assertIsInstance(result, urllib.error.HTTPError)
        self.assertEqual((calls, sleeps), (3, [5, 15]))

    def test_permanent_failures_and_checksum_mismatches_fail_at_once(self):
        result, calls, sleeps = self.download([self.server_error(404), self.BODY])
        self.assertIsInstance(result, urllib.error.HTTPError)
        self.assertEqual((calls, sleeps), (1, []))
        result, calls, sleeps = self.download([b"different archive", self.BODY])
        self.assertIsInstance(result, ValueError)
        self.assertEqual((calls, sleeps), (1, []))


if __name__ == "__main__":
    unittest.main()
