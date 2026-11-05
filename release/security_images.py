#!/usr/bin/env python3
"""Check image inputs before the workflow uploads them."""

import argparse
import json
import os
import subprocess
from pathlib import Path

from security_scan import verify_image


def load_checked(directory, commit):
    for name in ("project", "runner"):
        archive = directory / (name + ".tar")
        record = json.loads((directory / (name + ".json")).read_text())
        verify_image(archive, record, commit)
        if record.get("image") != name:
            raise ValueError("The image scan names a different release image.")
        subprocess.run(["docker", "image", "load", "-i", str(archive)], check=True)
        actual = subprocess.check_output(["docker", "image", "inspect", "--format={{.Id}}", "culler-" + name + ":check"], text=True).strip()
        if actual != record["image_id"]:
            raise ValueError("The loaded image differs from the scanned image.")
        tag = os.environ["PROJECT_IMAGE_TAG" if name == "project" else "IMAGE_TAG"]
        subprocess.run(["docker", "tag", actual, tag], check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    load_checked(args.directory, args.commit)
