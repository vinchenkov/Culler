#!/usr/bin/env python3
"""Publish one complete release after the required workflow checks."""

import argparse
import datetime
import hashlib
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

from security_scan import verify_files

CHECKS = ["fast-suite-macos", "deployment-linux", "application-package", "product-image", "release-downloads"]


class SafeRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, url):
        if urllib.parse.urlsplit(url).scheme != "https":
            raise ValueError("The download redirect requires HTTPS.")
        result = super().redirect_request(request, file, code, message, headers, url)
        if urllib.parse.urlsplit(url).netloc != urllib.parse.urlsplit(request.full_url).netloc:
            result.remove_header("Authorization")
        return result


class GitHub:
    def __init__(self, repository, token):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
            raise ValueError("The repository must use owner/name.")
        self.root = "https://api.github.com/repos/" + repository
        self.token = token
        self.opener = urllib.request.build_opener(SafeRedirect())

    def request(self, method, path, body=None, binary=False):
        url = path if path.startswith("https://") else self.root + path
        if urllib.parse.urlsplit(url).netloc not in {"api.github.com", "uploads.github.com"}:
            raise ValueError("The release request uses an unsupported host.")
        headers = {"Authorization": "Bearer " + self.token, "X-GitHub-Api-Version": "2022-11-28",
                   "Accept": "application/octet-stream" if binary else "application/vnd.github+json"}
        if isinstance(body, dict):
            body = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        elif body is not None:
            headers["Content-Type"] = "application/octet-stream"
        request = urllib.request.Request(url, data=body, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=300) as response:
                data = response.read()
        except urllib.error.HTTPError as error:
            if error.code == 404 and method == "GET":
                return None
            raise RuntimeError("GitHub refused the release request with HTTP " + str(error.code)) from None
        return data if binary else json.loads(data)


def file_record(path):
    body = path.read_bytes()
    return {"name": path.name, "size": len(body), "sha256": hashlib.sha256(body).hexdigest()}


def agentsview_record(package):
    record = package.get("agentsview")
    if not isinstance(record, dict) or set(record) != {"version", "archive_sha256", "executable_sha256"} or \
            not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", str(record["version"])) or \
            not all(re.fullmatch(r"[0-9a-f]{64}", str(record[key])) for key in ("archive_sha256", "executable_sha256")):
        raise ValueError("The package build record lacks its AgentsView release.")
    return record


def host_bun_record(package):
    record = package.get("host_bun")
    if not isinstance(record, dict) or set(record) != {"version", "archives"} or \
            not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", str(record["version"])) or \
            not isinstance(record["archives"], dict) or "darwin-arm64" not in record["archives"]:
        raise ValueError("The package build record lacks its host Bun release.")
    for platform, archive in record["archives"].items():
        if not re.fullmatch(r"[a-z0-9]+-[a-z0-9]+", str(platform)) or not isinstance(archive, dict) or \
                set(archive) != {"name", "url", "size", "sha256"} or \
                not re.fullmatch(r"[A-Za-z0-9_.-]+\.zip", str(archive["name"])) or \
                not str(archive["url"]).startswith("https://") or \
                not isinstance(archive["size"], int) or archive["size"] <= 0 or \
                not re.fullmatch(r"[0-9a-f]{64}", str(archive["sha256"])):
            raise ValueError("The package build record lacks its host Bun release.")
    return record


def newer_than_available(api, commit):
    latest = api.request("GET", "/releases/latest")
    if latest is None:
        return True
    tag = latest.get("tag_name", "")
    if not re.fullmatch(r"culler-[0-9a-f]{40}", tag):
        raise ValueError("The available release has an unknown identity.")
    previous = tag.removeprefix("culler-")
    comparison = api.request("GET", "/compare/" + previous + "..." + commit)
    if comparison["status"] in {"behind", "identical"}:
        return False
    if comparison["status"] != "ahead":
        raise ValueError("The release commit diverges from the available release.")
    return True


def ensure_asset(api, release, path, expected_sha256):
    expected = path.read_bytes()
    if hashlib.sha256(expected).hexdigest() != expected_sha256:
        raise ValueError("The release file changed after its secret check.")
    matches = [asset for asset in release.get("assets", []) if asset["name"] == path.name]
    if len(matches) > 1:
        raise ValueError("The draft contains duplicate release files.")
    if matches:
        asset = matches[0]
    else:
        url = release["upload_url"].split("{", 1)[0] + "?name=" + urllib.parse.quote(path.name)
        asset = api.request("POST", url, expected)
    actual = api.request("GET", "/releases/assets/" + str(asset["id"]), binary=True)
    if actual != expected:
        raise ValueError("The uploaded release file differs: " + path.name)


def scan_release_files(assets, output, commit, tools):
    record = output / "security-publication.json"
    subprocess.run([sys.executable, str(Path(__file__).with_name("security_scan.py")), "files",
                    "--directory", str(assets), "--commit", commit, "--tools", str(tools),
                    "--report", str(record)], check=True)
    data = json.loads(record.read_text())
    verify_files(assets, data, commit)
    return data


def publish(api, output, commit, image, run_id, now=None, project_image=None, tools=None):
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("The release requires a full commit identifier.")
    if not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}", image):
        raise ValueError("The release requires an immutable container image digest.")
    if project_image is not None and not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}", project_image):
        raise ValueError("The release requires an immutable Project image digest.")
    package = json.loads((output / "package.json").read_text())
    agentsview = agentsview_record(package)
    host_bun = host_bun_record(package)
    assets = output / "assets"
    verify_files(assets, json.loads((output / "security-files.json").read_text()), commit)
    package_file = file_record(assets / "culler-darwin-arm64.tar.gz")
    if any(package.get(key) != package_file[key] for key in package_file):
        raise ValueError("The application package differs from its build record.")
    branch = api.request("GET", "/compare/" + commit + "...main")
    if branch["status"] not in {"identical", "ahead"}:
        raise ValueError("The release commit is outside the current main history.")
    if not newer_than_available(api, commit):
        return False
    tag = "culler-" + commit
    release = api.request("GET", "/releases/tags/" + tag)
    if release is not None and not release.get("draft"):
        raise ValueError("The published release is immutable.")
    now = now or datetime.datetime.now(datetime.timezone.utc)
    workflow = api.request("GET", "/actions/runs/" + str(run_id))
    started = datetime.datetime.fromisoformat(workflow["created_at"].replace("Z", "+00:00"))
    record = {
        "format": 1, "commit": commit, "release_id": commit, "tag": tag,
        "sequence": int((output / "sequence").read_text()),
        "published_at": now.isoformat().replace("+00:00", "Z"), "platform": "darwin-arm64",
        "schema": package["schema"], "runtimes": package["runtimes"], "agentsview": agentsview, "host_bun": host_bun,
        "installer": file_record(assets / "installer.sh"), "package": package_file, "image": image,
        "checks": [{"name": name, "passed": True} for name in CHECKS],
        "workflow_seconds": max(0, int((now - started).total_seconds())),
    }
    if project_image is not None:
        record["project_image"] = project_image
    record_path = assets / "release.json"
    if release and any(asset["name"] == "release.json" for asset in release.get("assets", [])):
        old = next(asset for asset in release["assets"] if asset["name"] == "release.json")
        old_record = json.loads(api.request("GET", "/releases/assets/" + str(old["id"]), binary=True))
        for key in ("published_at", "workflow_seconds"):
            record[key] = old_record[key]
    record_path.write_text(json.dumps(record, indent=2) + "\n")
    scanned = scan_release_files(assets, output, commit, tools)
    if release is None:
        release = api.request("POST", "/releases", {
            "tag_name": tag, "target_commitish": commit, "name": "Culler " + commit[:12],
            "draft": True, "prerelease": False, "make_latest": "false",
        })
    for name in ("install.sh", "installer.sh", "culler-darwin-arm64.tar.gz", "release.json"):
        ensure_asset(api, release, assets / name, scanned["inputs"][name])
    # The workflow serializes this final comparison and publication.
    if not newer_than_available(api, commit):
        return False
    api.request("PATCH", "/releases/" + str(release["id"]), {"draft": False, "make_latest": "true"})
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--image", required=True)
    parser.add_argument("--project-image", required=True)
    parser.add_argument("--tools", required=True, type=Path)
    args = parser.parse_args()
    if os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("GITHUB_EVENT_NAME") != "push" or os.environ.get("GITHUB_REF") != "refs/heads/main":
        raise ValueError("Publication requires the main push workflow.")
    commit = os.environ["GITHUB_SHA"]
    if not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}", args.image):
        raise ValueError("The release requires an immutable container image digest.")
    # A private image must pass an authenticated pull before release availability.
    subprocess.run(["docker", "pull", args.image], check=True, stdout=subprocess.DEVNULL)
    if not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}", args.project_image):
        raise ValueError("The release requires an immutable Project image digest.")
    subprocess.run(["docker", "pull", args.project_image], check=True, stdout=subprocess.DEVNULL)
    api = GitHub(os.environ["GITHUB_REPOSITORY"], os.environ["GH_TOKEN"])
    published = publish(api, args.output, commit, args.image, os.environ["GITHUB_RUN_ID"], project_image=args.project_image, tools=args.tools)
    print("The release is available." if published else "A complete release already includes this commit or a later commit.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, RuntimeError, OSError, subprocess.CalledProcessError) as error:
        print("Release publication failed: " + str(error), file=sys.stderr)
        sys.exit(1)
