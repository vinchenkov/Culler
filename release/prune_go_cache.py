#!/usr/bin/env python3
"""Retain two Go build caches per job and platform on main."""

import os
import re
import urllib.error

from publish import GitHub


PREFIX = "culler-go-build-v1-"
KEY = re.compile(
    r"^(culler-go-build-v1-(?:macOS|Linux)-(?:ARM64|X64)-"
    r"(?:fast-suite-macos|application-package|deployment-linux|dashboard-browser))-"
    r"[0-9a-f]{64}-[0-9a-f]{40}$"
)


def obsolete_caches(caches):
    groups = {}
    for cache in caches:
        match = KEY.fullmatch(cache["key"])
        if match and cache["ref"] == "refs/heads/main":
            groups.setdefault(match[1], []).append(cache)
    return [cache["id"] for group in groups.values()
            for cache in sorted(group, key=lambda item: (item["created_at"], item["id"]), reverse=True)[2:]]


def prune(api):
    caches = []
    page = 1
    while True:
        result = api.request("GET", "/actions/caches?key=" + PREFIX + "&ref=refs%2Fheads%2Fmain&per_page=100&page=" + str(page))
        rows = result["actions_caches"]
        caches.extend(rows)
        if len(rows) < 100:
            break
        page += 1
    ids = obsolete_caches(caches)
    for cache_id in ids:
        api.request("DELETE", "/actions/caches/" + str(cache_id), binary=True)
    return len(ids)


if __name__ == "__main__":
    if os.environ.get("GITHUB_EVENT_NAME") != "push" or os.environ.get("GITHUB_REF") != "refs/heads/main":
        raise SystemExit("Go cache cleanup requires a push to main.")
    try:
        count = prune(GitHub(os.environ["GITHUB_REPOSITORY"], os.environ["GH_TOKEN"]))
        print("Removed " + str(count) + " obsolete Go build caches.")
    except (OSError, RuntimeError, ValueError, urllib.error.URLError) as error:
        print("::warning::Go cache cleanup failed: " + str(error))
