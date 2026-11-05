"""Capture the unchanged Memos frontend with fixed synthetic service responses."""

from __future__ import annotations

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import time
from urllib.parse import urlsplit
from urllib.request import urlopen

from playwright.sync_api import sync_playwright


OWNER = {"name": "users/1", "username": "case-owner", "displayName": "Case owner", "role": "ADMIN",
         "state": "NORMAL", "createTime": "2026-09-01T12:00:00Z"}
OTHER = OWNER | {"name": "users/2", "username": "other-user", "displayName": "Other user", "role": "USER"}
NOTE = {"name": "memos/case-note", "state": "NORMAL", "creator": "users/1", "visibility": "PUBLIC", "pinned": True,
        "createTime": "2026-09-01T12:00:00Z", "updateTime": "2026-09-01T12:00:00Z",
        "content": "A note for the evaluation case.\n\n<details><summary>Details</summary>\n\nAdditional explanation.\n\n</details>\n\nOrdinary text follows.",
        "snippet": "A note for the evaluation case.", "attachments": []}
ATTACHMENT = {"name": "attachments/case-file", "filename": "example.txt", "type": "text/plain", "size": "2048",
              "memo": "memos/case-note", "createTime": "2026-09-01T12:00:00Z"}


def states(case_id, side):
    if case_id == "memos-dark-mode-toast":
        return [{"name": name, "theme": theme, "toast": True} for name, theme in (("light", "default"), ("dark", "default-dark"))]
    if case_id == "memos-account-attachment-usage":
        return [{"name": "total"}] if side == "before" else [
            {"name": "total"}, {"name": "zero", "storage_bytes": "0"},
            {"name": "pending", "stats_pending": True}, {"name": "unavailable", "stats_error": True}]
    if case_id == "memos-pinned-marker":
        return [{"name": "pinned", "pinned": True}, {"name": "unpinned", "pinned": False},
                {"name": "pinned-hover", "pinned": True, "hover": "header"}]
    if case_id == "memos-author-spacing":
        return [{"name": "author", "display_name": "Al"}]
    if case_id == "memos-metadata-hover":
        return [{"name": name, "attachment": True, "other": other, "hover": hover, "edit": not other}
                for name, other, hover in (("owner-idle", False, None), ("owner-hover", False, "attachment"),
                                           ("other-idle", True, None), ("other-hover", True, "attachment"))]
    if case_id == "memos-disclosure-spacing":
        return [{"name": "collapsed"}, {"name": "expanded", "expand": True}]
    raise ValueError("The case has no supplied capture procedure.")


def capture(manifest, repository, output, port):
    case = json.loads(manifest.read_text())
    output.mkdir(parents=True, exist_ok=False)
    origin = f"http://127.0.0.1:{port}"
    tool = Path(__file__).with_name("memos-fixture.ts")
    record = {"case_id": case["id"], "method": "unchanged_frontend_with_synthetic_service_responses",
              "limits": ["These captures verify the frontend display with synthetic service responses.",
                         "They execute no Memos backend, database, authorization, or application test suite."],
              "viewport": {"width": 1280, "height": 900}, "clock": "2026-09-30T12:00:00Z",
              "timezone": "UTC", "locale": "en-US", "images": [],
              "tools": {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in (Path(__file__), tool)}}
    if case["id"] == "memos-dark-mode-toast":
        record["limits"].append("The capture triggers the unchanged toast library directly. It verifies notification appearance, not a completed save.")
    for side, revision in (("before", case["source"]["base_commit"]), ("after", case["source"]["reviewed_commit"])):
        subprocess.run(["git", "-C", str(repository), "checkout", "--detach", revision], check=True, capture_output=True)
        if subprocess.check_output(["git", "-C", str(repository), "diff", "--name-only"], text=True).strip():
            raise ValueError("The source contains changed tracked files.")
        with (output / f"{side}-vite.log").open("w") as log:
            process = subprocess.Popen(["mise", "exec", "--", "bun", "x", "pnpm@11.0.1", "exec", "vite",
                                        "--host", "127.0.0.1", "--port", str(port), "--strictPort"],
                                       cwd=repository / "web", stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            try:
                for _ in range(100):
                    if process.poll() is not None:
                        raise ValueError("The frontend process failed. Inspect its capture log.")
                    try:
                        with urlopen(origin, timeout=1) as response:
                            if response.status == 200: break
                    except OSError: time.sleep(.1)
                else: raise ValueError("The frontend process did not become ready.")
                with sync_playwright() as driver:
                    browser = driver.chromium.launch()
                    record["browser_version"] = browser.version
                    for state in states(case["id"], side):
                        owner = OWNER | {"displayName": state.get("display_name", OWNER["displayName"])}
                        fixture = {"owner": owner, "current_user": OTHER if state.get("other") else owner,
                                   "memo": NOTE | {"pinned": state.get("pinned", True),
                                                   "attachments": [ATTACHMENT] if state.get("attachment") else []},
                                   "origin": origin, "theme": state.get("theme", "default"),
                                   "storage_bytes": state.get("storage_bytes", "2048")}
                        fixture_path = output / f"{side}-{state['name']}-fixture.json"
                        fixture_path.write_text(json.dumps(fixture, indent=2) + "\n")
                        responses = json.loads(subprocess.check_output(["mise", "exec", "--", "bun", str(tool),
                                                                        str(repository / "web"), str(fixture_path)], text=True))
                        page = browser.new_page(viewport=record["viewport"], timezone_id=record["timezone"], locale=record["locale"])
                        page.clock.set_fixed_time(datetime(2026, 9, 30, 12, tzinfo=timezone.utc))
                        page.add_init_script("localStorage.setItem('memos_access_token','synthetic-capture-token');"
                                             "localStorage.setItem('memos_token_expires_at','2099-01-01T00:00:00Z');"
                                             f"localStorage.setItem('memos-theme',{json.dumps(fixture['theme'])});")
                        requests, errors, unresolved = [], [], []
                        page.on("pageerror", lambda error: errors.append(str(error)))
                        def respond(route):
                            path = urlsplit(route.request.url).path
                            requests.append(route.request.url)
                            if path == "/memos.api.v1.UserService/GetUserStats" and state.get("stats_error"):
                                route.fulfill(status=503, content_type="application/json", body='{"code":"unavailable","message":"Synthetic unavailable statistic"}')
                            elif path == "/memos.api.v1.UserService/GetUserStats" and state.get("stats_pending"):
                                unresolved.append(route)
                            elif path in responses:
                                route.fulfill(status=200, content_type="application/proto", body=base64.b64decode(responses[path]))
                            elif path.startswith("/api/v1/sse"):
                                route.fulfill(status=200, content_type="text/event-stream", body="")
                            elif path.startswith("/memos.api.v1."):
                                raise ValueError("An API request lacks a synthetic response: " + path)
                            else: route.continue_()
                        page.route("**/*", respond)
                        path = "/setting?section=my-account" if case["id"] == "memos-account-attachment-usage" else "/memos/case-note"
                        page.goto(origin + path, wait_until="domcontentloaded", timeout=60000)
                        if case["id"] == "memos-account-attachment-usage":
                            page.get_by_text("Account Information", exact=True).wait_for()
                            if side == "after": page.get_by_text("Statistics", exact=True).wait_for()
                            if state.get("stats_error"):
                                page.get_by_text("Your attachment storage is unavailable.", exact=True).wait_for(timeout=30000)
                        else:
                            page.locator("[data-slot='memo-header-meta']").first.wait_for()
                        page.wait_for_timeout(600)
                        if state.get("toast"):
                            module = next(url for url in requests if "/react-hot-toast.js" in url)
                            page.evaluate("async url => {const module=await import(url);module.default.success('The note was saved.',{duration:60000});}", module)
                            page.get_by_text("The note was saved.", exact=True).wait_for()
                        if state.get("edit"):
                            page.get_by_text("Edit", exact=True).first.click()
                            page.get_by_role("button", name=re.compile("^Update")).wait_for()
                            page.get_by_text("example.txt", exact=True).first.wait_for()
                            page.wait_for_timeout(600)
                        if state.get("hover") == "header": page.locator("[data-slot='memo-header-meta']").first.hover()
                        if state.get("hover") == "attachment":
                            page.get_by_text("example.txt", exact=True).first.locator("..").hover(position={"x": 400, "y": 14})
                        if state.get("expand"): page.locator("details summary").first.click()
                        page.wait_for_timeout(300)
                        if state.get("stats_error"):
                            page.get_by_text("Your attachment storage is unavailable.", exact=True).wait_for(timeout=30000)
                        text = page.locator("body").inner_text()
                        if errors: raise ValueError("The frontend capture contains errors: " + json.dumps(errors))
                        name = f"{side}-{state['name']}.png"
                        page.screenshot(path=str(output / name), full_page=True)
                        digest = hashlib.sha256((output / name).read_bytes()).hexdigest()
                        (output / f"{side}-{state['name']}-text.txt").write_text(text)
                        display = page.get_by_text("example.txt", exact=True).first.locator("..").evaluate(
                            "el => ({class:el.className,hover:el.matches(':hover'),children:[...el.children].map(c=>({class:c.className,opacity:getComputedStyle(c).opacity,text:c.textContent}))})"
                        ) if state.get("attachment") else None
                        record["images"].append({"path": name, "sha256": digest, "source_revision": revision,
                                                 "side": side, "state": state, "fixture": fixture_path.name,
                                                 "fixture_sha256": hashlib.sha256(fixture_path.read_bytes()).hexdigest(),
                                                 "requests": [urlsplit(url).path for url in requests if "/memos.api.v1." in url],
                                                 "attachment_display": display})
                        for route in unresolved:
                            route.fulfill(status=200, content_type="application/proto",
                                          body=base64.b64decode(responses["/memos.api.v1.UserService/GetUserStats"]))
                        page.close()
                    browser.close()
            finally:
                os.killpg(process.pid, signal.SIGTERM)
                process.wait(timeout=15)
    (output / "capture.json").write_text(json.dumps(record, indent=2) + "\n")
    return record


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Capture the pinned Memos frontend with synthetic service responses. The captures verify display only.")
    parser.add_argument("manifest", type=Path, help="Read this fixed case manifest.")
    parser.add_argument("--repository", type=Path, required=True, help="Use this disposable Memos checkout with its fixed frontend dependencies installed.")
    parser.add_argument("--output", type=Path, required=True, help="Create this absent directory for captures and provenance.")
    parser.add_argument("--port", type=int, default=4181, help="Use this loopback port. The default is 4181.")
    args = parser.parse_args()
    capture(args.manifest, args.repository, args.output, args.port)
