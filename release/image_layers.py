#!/usr/bin/env python3
"""Check every product image layer before publication."""

import argparse
import json
import tarfile
from pathlib import PurePosixPath


def forbidden_path(name):
    while name.startswith("./"):
        name = name[2:]
    parts = PurePosixPath(name.lstrip("/")).parts
    if any(part in {".culler", ".mission-control", "OPERATOR-INPUTS.md"} for part in parts):
        return True
    path = "/".join(parts)
    return any(path == base or path.startswith(base + "/") for base in (
        "tmp/go-build-cache", "tmp/go-mod-cache",
    ))


def verify(path):
    with tarfile.open(path) as image:
        manifest = json.load(image.extractfile("manifest.json"))
        if len(manifest) != 1:
            raise ValueError("The image archive must contain one product image.")
        config = json.load(image.extractfile(manifest[0]["Config"]))
        if config.get("os") != "linux" or config.get("architecture") != "arm64":
            raise ValueError("The product image requires Linux arm64.")
        for value in config["config"].get("Env", []):
            if value.partition("=")[0] in {"GOFLAGS", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN"}:
                raise ValueError("The image contains a Culler build environment.")
        for layer in manifest[0]["Layers"]:
            with tarfile.open(fileobj=image.extractfile(layer), mode="r|*") as contents:
                for item in contents:
                    if forbidden_path(item.name) or (item.issym() and forbidden_path(item.linkname)):
                        raise ValueError("A product image layer contains development or operator state: " + item.name)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive")
    verify(parser.parse_args().archive)
