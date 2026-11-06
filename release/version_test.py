import tempfile
import unittest
from pathlib import Path

from version import ROOT, parse, read_version, release_notes

FIRST_SECTION = """## 0.1.0

### Changes

- The first release.

### Known limits

- A limit.
"""

SECTION = """## {version}

### Changes

- A change.

### Known limits

- A limit.

### Database compatibility

The database schema is the same.

### Update effects

The update keeps the database.
"""


class VersionTest(unittest.TestCase):
    def repository(self, directory, version="0.2.0\n", changelog=None):
        root = Path(directory)
        if version is not None:
            (root / "VERSION").write_text(version)
        if changelog is None:
            changelog = "# Changelog\n\n" + SECTION.format(version="0.2.0") + "\n" + FIRST_SECTION
        (root / "CHANGELOG.md").write_text(changelog)
        return root

    def test_the_repository_version_has_a_complete_changelog_section(self):
        version = read_version(ROOT)
        notes = release_notes(ROOT)
        self.assertEqual(len(parse(version)), 3)
        self.assertTrue(notes.startswith("### Changes\n"))

    def test_versions_compare_by_number(self):
        self.assertLess(parse("0.9.0"), parse("0.10.0"))
        self.assertLess(parse("0.1.9"), parse("0.2.0"))
        self.assertEqual(parse("1.0.0"), (1, 0, 0))

    def test_a_malformed_version_file_is_rejected(self):
        for text in (None, "", "0.1\n", "v0.1.0\n", "0.01.0\n", "0.1.0-rc.1\n", "0.1.0\n\n", " 0.1.0\n", "0.1.0 \n"):
            with self.subTest(text=text), tempfile.TemporaryDirectory() as directory:
                root = self.repository(directory, version=text)
                with self.assertRaisesRegex(ValueError, "VERSION file"):
                    read_version(root)

    def test_a_version_file_without_a_final_line_break_is_valid(self):
        with tempfile.TemporaryDirectory() as directory:
            self.assertEqual(read_version(self.repository(directory, version="0.2.0")), "0.2.0")

    def test_the_notes_are_the_section_of_the_current_version(self):
        with tempfile.TemporaryDirectory() as directory:
            notes = release_notes(self.repository(directory))
            self.assertEqual(notes, SECTION.format(version="0.2.0").split("\n", 2)[2])

    def test_the_first_release_requires_no_update_parts(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.repository(directory, version="0.1.0\n", changelog=FIRST_SECTION)
            self.assertEqual(release_notes(root), FIRST_SECTION.split("\n", 2)[2])

    def test_later_releases_require_both_update_parts(self):
        for version in ("0.1.1", "0.2.0", "1.0.0"):
            for heading in ("### Database compatibility", "### Update effects"):
                changelog = SECTION.format(version=version).split(heading)[0]
                with self.subTest(version=version, heading=heading), tempfile.TemporaryDirectory() as directory:
                    root = self.repository(directory, version=version + "\n", changelog=changelog)
                    with self.assertRaisesRegex(ValueError, "in this order"):
                        release_notes(root)

    def test_the_first_release_requires_nonempty_changes_and_limits(self):
        for heading, content in (("Changes", "- The first release.\n"), ("Known limits", "- A limit.\n")):
            with self.subTest(heading=heading), tempfile.TemporaryDirectory() as directory:
                root = self.repository(directory, version="0.1.0\n", changelog=FIRST_SECTION.replace(content, ""))
                with self.assertRaisesRegex(ValueError, heading + " part"):
                    release_notes(root)

    def test_a_version_increase_requires_its_changelog_section(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.repository(directory, version="0.3.0\n")
            with self.assertRaisesRegex(ValueError, "no section for version 0.3.0"):
                release_notes(root)

    def test_the_changelog_order_and_parts_are_checked(self):
        cases = {
            "newest version": "# Changelog\n\n" + SECTION.format(version="0.1.0") + SECTION.format(version="0.2.0"),
            "first CHANGELOG.md section": "# Changelog\n\n" + SECTION.format(version="0.3.0") + SECTION.format(version="0.2.0"),
            "more than once": SECTION.format(version="0.2.0") + SECTION.format(version="0.2.0"),
            "heading must be a version": "## Unreleased\n\n" + SECTION.format(version="0.2.0"),
            "in this order": SECTION.format(version="0.2.0").replace("### Known limits", "### Limits"),
            "Update effects part": SECTION.format(version="0.2.0").replace("The update keeps the database.\n", ""),
            "start with its Changes part": SECTION.format(version="0.2.0").replace("\n### Changes", "Text.\n\n### Changes"),
        }
        for message, changelog in cases.items():
            with self.subTest(message=message), tempfile.TemporaryDirectory() as directory:
                with self.assertRaisesRegex(ValueError, message):
                    release_notes(self.repository(directory, changelog=changelog))


if __name__ == "__main__":
    unittest.main()
