import copy
import datetime
import io
import json
import tarfile
import tempfile
import unittest
from pathlib import Path

from security_content import Content, digest
from security_policy import apply_exceptions
from security_scan import consume_trivy, verify_files, verify_image


def advisory():
    return {"image": "runner", "target": "os", "package": "example", "version": "1.0",
            "advisory": "CVE-2099-1234", "severity": "HIGH", "fixed_version": "1.1"}


class SecurityPolicyTest(unittest.TestCase):
    def test_exact_unexpired_exception_preserves_the_result(self):
        item = advisory()
        exception = {key: value for key, value in item.items() if key not in {"severity", "fixed_version"}}
        exception.update(reason="The fixture proves the exception match.", expires="2026-10-23")
        for change, passed in [({}, True), ({"expires": "2026-10-21"}, False),
                               ({"version": "0.9"}, False), ({"package": "other"}, False),
                               ({"advisory": "CVE-2099-9999"}, False), ({"image": "project"}, False),
                               ({"target": "elsewhere"}, False)]:
            with self.subTest(change=change):
                report = {"secrets": [], "vulnerabilities": [dict(item)]}
                exceptions = {"secrets": [], "vulnerabilities": [{**exception, **change}]}
                self.assertEqual(apply_exceptions(report, exceptions, datetime.date(2026, 10, 22)), passed)
                self.assertEqual(report["vulnerabilities"][0]["advisory"], item["advisory"])
                self.assertEqual(report["vulnerabilities"][0]["suppressed"], passed)

    def test_low_severity_results_remain_visible(self):
        report = {"secrets": [], "vulnerabilities": [{**advisory(), "severity": "LOW"}]}
        self.assertTrue(apply_exceptions(report, {"secrets": [], "vulnerabilities": []}))
        self.assertEqual(len(report["vulnerabilities"]), 1)
        self.assertFalse(report["vulnerabilities"][0]["suppressed"])

    def test_secret_exceptions_require_the_exact_location(self):
        item = {"scanner": "gitleaks", "subject": "commit:abc", "file": "fixture", "rule": "rule", "line": 1}
        for change, passed in (({}, True), ({"subject": "commit:def"}, False),
                               ({"file": "different"}, False), ({"line": 2}, False)):
            report = {"secrets": [dict(item)], "vulnerabilities": []}
            exceptions = {"secrets": [{**item, **change, "reason": "The value is a synthetic fixture."}], "vulnerabilities": []}
            self.assertEqual(apply_exceptions(report, exceptions), passed)

    def test_missing_bundled_versions_remain_unverified(self):
        from security_scan import bundled_tools
        with self.assertRaises(KeyError):
            bundled_tools({}, "runner")
        with self.assertRaises(ValueError):
            bundled_tools({"usr/share/culler-project/manifest.json": json.dumps({"tools": {"bun": "unknown"}, "browser_versions": {}})}, "project")

    def test_new_advisory_changes_the_result_for_the_same_image(self):
        source = {"SchemaVersion": 2, "Metadata": {"ImageID": "sha256:unchanged"}, "Results": [
            {"Target": "os", "Class": "os-pkgs", "Type": "alpine", "Packages": [{"Name": "example", "Version": "1.0"}]}]}
        before = {"image_id": "sha256:unchanged", "secrets": [], "vulnerabilities": []}
        after = copy.deepcopy(before)
        consume_trivy(source, before, "runner")
        source["Results"][0]["Vulnerabilities"] = [{"PkgName": "example", "InstalledVersion": "1.0",
            "VulnerabilityID": "CVE-2099-1234", "Severity": "CRITICAL", "FixedVersion": "1.1"}]
        consume_trivy(source, after, "runner")
        exceptions = {"secrets": [], "vulnerabilities": []}
        self.assertTrue(apply_exceptions(before, exceptions))
        self.assertFalse(apply_exceptions(after, exceptions))
        self.assertEqual(before["image_id"], after["image_id"])

    def test_missing_inventory_or_wrong_image_remains_unverified(self):
        for data in ({"SchemaVersion": 2, "Metadata": {"ImageID": "wrong"}},
                     {"SchemaVersion": 2, "Metadata": {"ImageID": "expected"}, "Results": []}):
            with self.assertRaises(ValueError):
                consume_trivy(data, {"image_id": "expected", "secrets": [], "vulnerabilities": []}, "runner")

    def test_reports_omit_secret_match_and_image_metadata(self):
        report = {"image_id": "expected", "secrets": [], "vulnerabilities": []}
        data = {"SchemaVersion": 2, "Metadata": {"ImageID": "expected", "ImageConfig": {"Env": ["private-value"]}},
                "Results": [{"Target": "config", "Secrets": [{"RuleID": "rule", "StartLine": 2,
                    "Match": "private-value", "Code": {"Lines": [{"Content": "private-value"}]}}]}]}
        consume_trivy(data, report, "runner", vulnerabilities=False)
        self.assertNotIn("private-value", json.dumps(report))
        self.assertEqual(report["secrets"][0]["file"], "config")

    def test_upload_verification_rejects_replaced_inputs_and_failed_scans(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            file = root / "install.sh"
            file.write_text("first")
            report = {"kind": "files", "status": "passed", "commit": "commit", "inputs": {"install.sh": digest(file)}}
            verify_files(root, report, "commit")
            file.write_text("second")
            with self.assertRaises(ValueError):
                verify_files(root, report, "commit")
            for status in ("failed", "unverified"):
                with self.assertRaises(ValueError):
                    verify_files(root, {**report, "status": status}, "commit")
            image = {"kind": "image", "scan_mode": "release", "status": "passed", "commit": "commit", "archive_sha256": digest(file)}
            verify_image(file, image, "commit")
            with self.assertRaises(ValueError):
                verify_image(file, {**image, "scan_mode": "vulnerabilities"}, "commit")
            file.write_text("third")
            with self.assertRaises(ValueError):
                verify_image(file, image, "commit")

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


if __name__ == "__main__":
    unittest.main()
