#!/usr/bin/env python3
"""Load the checked image archives and tag them for the upload."""

import argparse
import hashlib
import json
import os
import subprocess
import tarfile
from pathlib import Path


def archive_identity(archive, commit):
    with tarfile.open(archive) as source:
        manifest = json.load(source.extractfile("manifest.json"))
        if len(manifest) != 1:
            raise ValueError("The archive must contain one image.")
        config_body = source.extractfile(manifest[0]["Config"]).read()
    labels = json.loads(config_body).get("config", {}).get("Labels") or {}
    if labels.get("org.opencontainers.image.revision") != commit:
        raise ValueError("The image revision differs from the source commit.")
    return "sha256:" + hashlib.sha256(config_body).hexdigest()


def load_checked(directory, commit):
    for name in ("project", "runner"):
        archive = directory / (name + ".tar")
        expected = archive_identity(archive, commit)
        subprocess.run(["docker", "image", "load", "-i", str(archive)], check=True)
        actual = subprocess.check_output(["docker", "image", "inspect", "--format={{.Id}}", "culler-" + name + ":check"], text=True).strip()
        if actual != expected:
            raise ValueError("The loaded image differs from the checked image archive.")
        tag = os.environ["PROJECT_IMAGE_TAG" if name == "project" else "IMAGE_TAG"]
        subprocess.run(["docker", "tag", actual, tag], check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    load_checked(args.directory, args.commit)
