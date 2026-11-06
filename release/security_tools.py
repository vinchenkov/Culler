#!/usr/bin/env python3
"""Install the fixed Trivy scanner into a private directory."""

import argparse
import hashlib
import http.client
import io
import json
import platform
import tarfile
import time
import urllib.error
import urllib.request
from pathlib import Path

PINS = Path(__file__).with_name("security") / "tools.json"
RETRY_DELAYS = (5, 15)


def temporary(error):
    if isinstance(error, urllib.error.HTTPError):
        return error.code == 429 or 500 <= error.code <= 599
    return isinstance(error, (urllib.error.URLError, http.client.IncompleteRead, ConnectionError, TimeoutError))


def download(record):
    for attempt in range(len(RETRY_DELAYS) + 1):
        try:
            with urllib.request.urlopen(record["url"], timeout=300) as response:
                body = response.read()
            break
        except Exception as error:
            if attempt == len(RETRY_DELAYS) or not temporary(error):
                raise
            time.sleep(RETRY_DELAYS[attempt])
    if hashlib.sha256(body).hexdigest() != record["sha256"]:
        raise ValueError("The scanner download differs from its checksum.")
    return body


def install(destination):
    pins = json.loads(PINS.read_text())
    machine = {"aarch64": "arm64", "AMD64": "x86_64"}.get(platform.machine(), platform.machine())
    target = platform.system() + "-" + machine
    destination.mkdir(parents=True, exist_ok=True, mode=0o700)
    with tarfile.open(fileobj=io.BytesIO(download(pins["trivy"]["archives"][target]))) as archive:
        member = archive.getmember("trivy")
        if not member.isfile():
            raise ValueError("The scanner archive lacks its executable.")
        path = destination / "trivy"
        path.write_bytes(archive.extractfile(member).read())
        path.chmod(0o700)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    install(parser.parse_args().directory.resolve())
