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
from update_contract import validate_update_contract, require_new_public_version

from version import parse as parse_version
from version import read_version

CHECKS = ["fast-suite-macos", "deployment-linux", "application-package", "product-image", "release-downloads"]


def test_releases_enabled():
    policy = Path(__file__).resolve().parent.parent / "mc/distribution/update_policy.json"
    value = json.loads(policy.read_text()).get("test_releases", False)
    if not isinstance(value, bool):
        raise ValueError("The test release policy requires a boolean.")
    return value


def test_release_is_new(api, commit):
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("The release requires a full commit identifier.")
    release = api.request("GET", "/releases/tags/test-" + commit)
    if release is not None and not release.get("draft", False):
        return False
    latest = api.request("GET", "/releases/latest")
    tag = latest.get("tag_name", "") if latest else ""
    if tag:
        comparison = api.request("GET", "/compare/" + tag + "..." + commit)
        return comparison["status"] == "ahead"
    return True


def latest_public_release(api):
    latest = api.request("GET", "/releases/latest")
    if latest is None or not latest.get("tag_name", "").startswith("test-"):
        return latest
    page = 1
    while True:
        releases = api.request("GET", "/releases?per_page=100&page=" + str(page))
        for release in releases:
            if not release.get("draft") and not release.get("prerelease") and not release.get("tag_name", "").startswith("test-"):
                return release
        if len(releases) < 100:
            return None
        page += 1


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


def latest_version(latest):
    """Return the version of the latest complete release, or None without a release."""
    if latest is None:
        return None
    tag = latest.get("tag_name", "")
    version = tag.removeprefix("v")
    try:
        if version == tag:
            raise ValueError
        parse_version(version)
    except ValueError:
        raise ValueError("The latest release has the tag " + repr(tag) + ", which is not a version tag.") from None
    return version


def version_is_new(api, version, commit, candidate=None):
    """Return True for a version above the latest release and False for the version of the latest release.

    A lower version fails.
    """
    latest = latest_public_release(api)
    previous = latest_version(latest)
    if previous is None:
        return True
    if parse_version(version) == parse_version(previous):
        return False
    if parse_version(version) < parse_version(previous):
        raise ValueError("Version " + version + " is lower than version " + previous + " of the latest release.")
    comparison = api.request("GET", "/compare/v" + previous + "..." + commit)
    if comparison["status"] != "ahead":
        raise ValueError("Version " + version + " requires a commit after the commit of the latest release.")
    if candidate is not None:
        records = [asset for asset in latest.get("assets", []) if asset["name"] == "release.json"]
        if len(records) != 1:
            raise ValueError("The available release lacks its unique release record.")
        previous_record = json.loads(api.request("GET", "/releases/assets/" + str(records[0]["id"]), binary=True))
        if previous_record.get("version") != previous:
            raise ValueError("The available release record names a different version from its tag.")
        require_new_public_version(candidate, previous_record)
    return True


def ensure_asset(api, release, path):
    expected = path.read_bytes()
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


def publish(api, output, commit, image, run_id, now=None, project_image=None, test_release=False):
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("The release requires a full commit identifier.")
    if not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}", image):
        raise ValueError("The release requires an immutable container image digest.")
    if project_image is not None and not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}", project_image):
        raise ValueError("The release requires an immutable Project image digest.")
    version = (output / "version").read_text().strip()
    parse_version(version)
    notes = (output / "release-notes.md").read_text()
    if not notes.strip():
        raise ValueError("The release requires its changelog section.")
    package = json.loads((output / "package.json").read_text())
    validate_update_contract(package)
    if package["version"] != version:
        raise ValueError("The package version differs from the release input.")
    agentsview = agentsview_record(package)
    host_bun = host_bun_record(package)
    assets = output / "assets"
    package_file = file_record(assets / "culler-darwin-arm64.tar.gz")
    opencode_file = file_record(assets / "culler-opencode-linux-arm64.tar.gz")
    if json.loads((output / "opencode.json").read_text()) != opencode_file:
        raise ValueError("The OpenCode archive differs from its build record.")
    if any(package.get(key) != package_file[key] for key in package_file):
        raise ValueError("The application package differs from its build record.")
    branch = api.request("GET", "/compare/" + commit + "...main")
    if branch["status"] not in {"identical", "ahead"}:
        raise ValueError("The release commit is outside the current main history.")
    new = test_release_is_new(api, commit) if test_release else version_is_new(api, version, commit, package)
    if not new:
        return False
    tag = "test-" + commit if test_release else "v" + version
    title = "Culler test " + commit[:12] + " (" + version + ")" if test_release else "Culler " + version
    release = api.request("GET", "/releases/tags/" + tag)
    if release is not None and not release.get("draft"):
        raise ValueError("The published release is immutable.")
    now = now or datetime.datetime.now(datetime.timezone.utc)
    workflow = api.request("GET", "/actions/runs/" + str(run_id))
    started = datetime.datetime.fromisoformat(workflow["created_at"].replace("Z", "+00:00"))
    record = {
        "format": 1, "version": version, "commit": commit, "release_id": commit, "tag": tag,
        "update": package["update"], "dependencies": package["dependencies"],
        "sequence": int((output / "sequence").read_text()),
        "published_at": now.isoformat().replace("+00:00", "Z"), "platform": "darwin-arm64",
        "schema": package["schema"], "runtimes": package["runtimes"], "agentsview": agentsview, "host_bun": host_bun,
        "installer": file_record(assets / "installer.sh"), "package": package_file, "image": image,
        "opencode": opencode_file,
        "checks": [{"name": name, "passed": True} for name in CHECKS],
        "workflow_seconds": max(0, int((now - started).total_seconds())),
    }
    if test_release:
        record["test_release"] = True
    if project_image is not None:
        record["project_image"] = project_image
    record_path = assets / "release.json"
    if release and any(asset["name"] == "release.json" for asset in release.get("assets", [])):
        old = next(asset for asset in release["assets"] if asset["name"] == "release.json")
        old_record = json.loads(api.request("GET", "/releases/assets/" + str(old["id"]), binary=True))
        for key in ("published_at", "workflow_seconds"):
            record[key] = old_record[key]
    record_path.write_text(json.dumps(record, indent=2) + "\n")
    if release is None:
        release = api.request("POST", "/releases", {
            "tag_name": tag, "target_commitish": commit, "name": title, "body": notes,
            "draft": True, "prerelease": False, "make_latest": "false",
        })
    for name in ("install.sh", "installer.sh", "culler-darwin-arm64.tar.gz", "culler-opencode-linux-arm64.tar.gz", "release.json"):
        ensure_asset(api, release, assets / name)
    # The workflow serializes this final comparison and publication.
    new = test_release_is_new(api, commit) if test_release else version_is_new(api, version, commit, package)
    if not new:
        return False
    api.request("PATCH", "/releases/" + str(release["id"]), {
        "name": title, "body": notes, "draft": False, "make_latest": "true",
    })
    return True


def write_decision(version, new, test_release=False):
    text = ("The workflow publishes this commit as a test release after the required checks." if new else
            "This commit already has a complete test release.") if test_release else (
            "Version " + version + " is new. The workflow publishes it after the required checks." if new else
            "Version " + version + " is the version of the latest release. The workflow publishes no release.")
    print(text)
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.write("publish=" + ("true" if new else "false") + "\n")
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
            summary.write(text + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--decide", action="store_true",
                        help="write the publication decision for the selected release policy, then stop")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--image")
    parser.add_argument("--project-image")
    args = parser.parse_args()
    if os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("GITHUB_EVENT_NAME") != "push" or os.environ.get("GITHUB_REF") != "refs/heads/main":
        raise ValueError("Publication requires the main push workflow.")
    commit = os.environ["GITHUB_SHA"]
    api = GitHub(os.environ["GITHUB_REPOSITORY"], os.environ["GH_TOKEN"])
    test_release = test_releases_enabled()
    if args.decide:
        version = read_version()
        new = test_release_is_new(api, commit) if test_release else version_is_new(api, version, commit)
        write_decision(version, new, test_release)
        return
    if args.output is None or args.image is None or args.project_image is None:
        parser.error("Publication requires --output, --image, and --project-image.")
    if not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}", args.image):
        raise ValueError("The release requires an immutable container image digest.")
    # A private image must pass an authenticated pull before release availability.
    subprocess.run(["docker", "pull", args.image], check=True, stdout=subprocess.DEVNULL)
    if not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}", args.project_image):
        raise ValueError("The release requires an immutable Project image digest.")
    subprocess.run(["docker", "pull", args.project_image], check=True, stdout=subprocess.DEVNULL)
    published = publish(api, args.output, commit, args.image, os.environ["GITHUB_RUN_ID"], project_image=args.project_image, test_release=test_release)
    print("The release is available." if published else "The selected release is already available.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, RuntimeError, OSError, subprocess.CalledProcessError) as error:
        print("Release publication failed: " + str(error), file=sys.stderr)
        sys.exit(1)
