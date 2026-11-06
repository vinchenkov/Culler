#!/usr/bin/env python3
"""Read and check the Culler version and its changelog section."""

import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PATTERN = re.compile(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)")
PARTS = ("Changes", "Known limits", "Database compatibility", "Update effects")


def parse(value):
    """Return a version as three integers."""
    match = PATTERN.fullmatch(value) if isinstance(value, str) else None
    if match is None:
        raise ValueError("A version uses MAJOR.MINOR.PATCH without leading zeros, for example 0.1.0: " + repr(value))
    return tuple(int(part) for part in match.groups())


def read_version(root=ROOT):
    try:
        text = (Path(root) / "VERSION").read_text()
    except FileNotFoundError:
        raise ValueError("The VERSION file is absent.") from None
    value = text[:-1] if text.endswith("\n") else text
    if PATTERN.fullmatch(value) is None:
        raise ValueError("The VERSION file must hold one version line, for example 0.1.0.")
    return value


def sections(text):
    """Return each changelog version section as (version, body) in file order."""
    found = []
    for line in text.splitlines():
        if line.startswith("## "):
            heading = line[3:]
            if PATTERN.fullmatch(heading) is None:
                raise ValueError("The changelog heading must be a version, for example ## 0.1.0: " + repr(line))
            found.append((heading, []))
        elif found:
            found[-1][1].append(line)
    return [(version, "\n".join(lines).strip()) for version, lines in found]


def check_section(version, body):
    required = PARTS[:2] if version == "0.1.0" else PARTS
    headings = [line[4:] for line in body.splitlines() if line.startswith("### ")]
    if headings != list(required):
        raise ValueError("The changelog section " + version + " must hold these parts in this order: " + ", ".join(required) + ".")
    parts = re.split(r"^### .*$", body, flags=re.MULTILINE)
    if parts[0].strip():
        raise ValueError("The changelog section " + version + " must start with its " + required[0] + " part.")
    for name, text in zip(required, parts[1:]):
        if not text.strip():
            raise ValueError("The " + name + " part of the changelog section " + version + " is empty.")


def release_notes(root=ROOT):
    """Return the changelog section of VERSION after a check of the complete changelog."""
    version = read_version(root)
    try:
        text = (Path(root) / "CHANGELOG.md").read_text()
    except FileNotFoundError:
        raise ValueError("The CHANGELOG.md file is absent.") from None
    found = sections(text)
    versions = [item[0] for item in found]
    if version not in versions:
        raise ValueError("CHANGELOG.md has no section for version " + version + ". Add a ## " + version + " section.")
    if len(set(versions)) != len(versions):
        raise ValueError("CHANGELOG.md holds one version section more than once.")
    if [parse(item) for item in versions] != sorted((parse(item) for item in versions), reverse=True):
        raise ValueError("The CHANGELOG.md sections must go from the newest version to the oldest version.")
    if versions[0] != version:
        raise ValueError("The first CHANGELOG.md section must be " + version + ", the version in VERSION.")
    for item, body in found:
        check_section(item, body)
    return found[0][1] + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("check", "show", "notes"),
                        help="check the version and changelog, print the version, or print its changelog section")
    parser.add_argument("--root", type=Path, default=ROOT, help="the repository root")
    args = parser.parse_args()
    notes = release_notes(args.root)
    if args.command == "check":
        print("Version " + read_version(args.root) + " and its changelog section are valid.")
    elif args.command == "show":
        print(read_version(args.root))
    else:
        sys.stdout.write(notes)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError) as error:
        print("Version check failed: " + str(error), file=sys.stderr)
        sys.exit(1)
