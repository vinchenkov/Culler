import contextlib
import gzip
import io
import os
import tempfile
import unittest

from image_archive import compressed_sizes, read_image
from image_archive_test import build_archive, layer_tar, member
from image_contents import RUNTIME
from size_report import BASELINE, BASELINE_HOST_FILES, Measure, changed_layers, gb, main, report, runtime_volume, signed


def project_layers():
    return [
        [member("file", "bin/sh", b"shell " * 100, mode=0o755)],
        [member("file", "opt/go/bin/go", b"go " * 500, mode=0o755)],
    ]


def runner_layers():
    return [
        [member("file", RUNTIME + "node/bin/node", b"node " * 300, mode=0o755)],
        [member("file", RUNTIME + "app/node_modules/a/index.js", b"a" * 200),
         member("link", RUNTIME + "app/node_modules/b/index.js", linkname=RUNTIME + "app/node_modules/a/index.js"),
         member("symlink", RUNTIME + "bin/node", linkname="../node/bin/node")],
        [member("file", RUNTIME + "libexec/mc-real", b"mc" * 50, mode=0o6755, uid=10001, gid=10001),
         member("file", RUNTIME + "release-id", b"abc\n")],
    ]


def size(members):
    return len(layer_tar(members))


def gzipped(members):
    return len(gzip.compress(layer_tar(members), 6))


class SizeReportTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)

    def measure(self, project_layers_, runner_added, prefix="current"):
        project = read_image(build_archive(self.directory.name, prefix + "-project.tar", project_layers_))
        runner = read_image(build_archive(self.directory.name, prefix + "-runner.tar", project_layers_ + runner_added))
        return Measure(project, runner, compressed_sizes((project, runner), workers=2))

    def test_formats(self):
        self.assertEqual(gb(2_203_456_789), "2.20 GB (2203456789 bytes)")
        self.assertEqual(signed(-120_000_000), "-0.12 GB (-120000000 bytes)")
        self.assertEqual(signed(5_000_000), "+0.01 GB (+5000000 bytes)")

    def test_runtime_volume_counts_each_hard_link_once(self):
        current = self.measure(project_layers(), runner_layers())
        self.assertEqual(runtime_volume(current.runner), 1500 + 200 + 100 + 4)
        self.assertEqual(current.volume, 1804)

    def test_values(self):
        project, added = project_layers(), runner_layers()
        current = self.measure(project, added)
        project_size = sum(size(layer) for layer in project)
        project_gzip = sum(gzipped(layer) for layer in project)
        added_size = sum(size(layer) for layer in added)
        added_gzip = sum(gzipped(layer) for layer in added)
        self.assertEqual(len(current.added), 3)
        self.assertEqual([layer.index for layer in current.release], [4])
        self.assertEqual(current.values(), {
            "fresh_download": project_gzip + added_gzip,
            "fresh_disk": project_size + added_size + 1804,
            "update_download": project_gzip - gzipped(project[0]) + added_gzip,
            "update_disk": project_size - size(project[0]) + added_size,
        })

    def test_a_repeated_layer_downloads_once(self):
        added = runner_layers()
        added.insert(2, list(added[1]))
        current = self.measure(project_layers(), added)
        self.assertEqual(len(current.added), 4)
        self.assertEqual(current.added[1].diff_id, current.added[2].diff_id)
        self.assertEqual(current.gzip(current.added), sum(gzipped(layer) for layer in runner_layers()))
        self.assertEqual(current.size(current.added), sum(size(layer) for layer in added))
        self.assertIn("| Product image | 6 |", report(current))

    def test_report_against_the_release_baseline(self):
        current = self.measure(project_layers(), runner_layers())
        text = report(current)
        values = current.values()
        self.assertIn("| Default Project image | 2 | " + gb(sum(size(layer) for layer in project_layers())), text)
        self.assertIn("| Product image | 5 |", text)
        self.assertIn("| Product image layers above the default Project image | 3 |", text)
        self.assertIn("| Per-release layers of the product image | 1 | " + gb(size(runner_layers()[2])) + " | " + gb(gzipped(runner_layers()[2])), text)
        self.assertIn("| Runtime volume | 0.00 GB (0 bytes) | " + gb(1804) + " |", text)
        self.assertIn("| Total | " + gb(values["fresh_download"]) + " | " + gb(values["fresh_disk"]) + " |", text)
        self.assertIn("| Only the per-release layers changed | " + gb(gzipped(runner_layers()[2])) + " | " + gb(size(runner_layers()[2])) + " |", text)
        self.assertIn("release 9055488a", text)
        self.assertIn("| Fresh install download | " + gb(BASELINE["fresh_download"]) + " | " + gb(values["fresh_download"]) + " | "
                      + signed(values["fresh_download"] - BASELINE["fresh_download"]) + " |", text)
        self.assertIn("| Fresh install disk | " + gb(BASELINE["fresh_disk"] - BASELINE_HOST_FILES) + " | ", text)
        self.assertIn("| Update disk | " + gb(BASELINE["update_disk"]) + " | " + gb(values["update_disk"]) + " | ", text)
        self.assertNotIn("previous archives", text)

    def test_report_against_previous_archives(self):
        previous = self.measure(project_layers(), runner_layers(), prefix="previous")
        added = runner_layers()
        added[2][0] = member("file", RUNTIME + "libexec/mc-real", b"MC" * 50, mode=0o6755, uid=10001, gid=10001)
        current = self.measure(project_layers(), added)
        changed = changed_layers(current, previous)
        self.assertEqual([layer.index for layer in changed], [4])
        text = report(current, previous)
        self.assertIn("The baseline is the previous archives", text)
        self.assertIn("| Layers that differ from the previous archives (1 layers) | " + gb(gzipped(added[2])) + " | " + gb(size(added[2])) + " |", text)
        before, after = previous.values(), current.values()
        self.assertIn("| Update download | " + gb(before["update_download"]) + " | " + gb(after["update_download"]) + " | "
                      + signed(after["update_download"] - before["update_download"]) + " |", text)
        self.assertIn("| Fresh install disk | " + gb(before["fresh_disk"]) + " | " + gb(after["fresh_disk"]) + " | +0.00 GB (+0 bytes) |", text)
        self.assertNotIn("release 9055488a", text)

    def test_main_writes_the_report(self):
        project = build_archive(self.directory.name, "project.tar", project_layers())
        runner = build_archive(self.directory.name, "runner.tar", project_layers() + runner_layers())
        output = os.path.join(self.directory.name, "report.md")
        printed = io.StringIO()
        with contextlib.redirect_stdout(printed):
            self.assertEqual(main(["--project", project, "--runner", runner, "--output", output, "--workers", "1"]), 0)
        with open(output, encoding="utf-8") as handle:
            text = handle.read()
        self.assertTrue(text.startswith("# Size report\n"))
        self.assertIn("## Comparison with the baseline", text)
        self.assertEqual(printed.getvalue(), text)


if __name__ == "__main__":
    unittest.main()
