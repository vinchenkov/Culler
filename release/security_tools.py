#!/usr/bin/env python3
"""Install the fixed security scanners into a private directory."""

import argparse
import hashlib
import io
import json
import platform
import re
import tarfile
import urllib.request
from pathlib import Path

PINS = Path(__file__).with_name("security") / "tools.json"


def download(record):
    with urllib.request.urlopen(record["url"], timeout=300) as response:
        body = response.read()
    if hashlib.sha256(body).hexdigest() != record["sha256"]:
        raise ValueError("The scanner download differs from its checksum.")
    return body


def install(destination):
    pins = json.loads(PINS.read_text())
    machine = {"aarch64": "arm64", "AMD64": "x86_64"}.get(platform.machine(), platform.machine())
    target = platform.system() + "-" + machine
    destination.mkdir(parents=True, exist_ok=True, mode=0o700)
    for name in ("gitleaks", "trivy"):
        with tarfile.open(fileobj=io.BytesIO(download(pins[name]["archives"][target]))) as archive:
            member = archive.getmember(name)
            if not member.isfile():
                raise ValueError("The scanner archive lacks its executable.")
            path = destination / name
            path.write_bytes(archive.extractfile(member).read())
            path.chmod(0o700)
    # Keep the established rules. Remove broad path exclusions from the default configuration.
    config = download(pins["gitleaks"]["rules"]).decode()
    config, count = re.subn(r"(?ms)(^\[allowlist\]\n.*?^paths = )\[.*?^\]", r"\1[]", config, count=1)
    if count != 1:
        raise ValueError("The Gitleaks path configuration requires review.")
    (destination / "gitleaks.toml").write_text(config)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    install(parser.parse_args().directory.resolve())
