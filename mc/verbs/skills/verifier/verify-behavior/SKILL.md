---
name: verify-behavior
description: Read when a criterion or broad check needs browser interaction or visual confirmation.
---

# Verify behavior

## Browser tool

Culler ships the Playwright Command Line Interface (CLI) and the
Chrome for Testing browser. Start each browser session with this command:

    playwright-cli -s=verify-<run_id> --browser=chromium open <target-url>

The `--browser=chromium` flag selects the installed browser.

Run each later command in the same session:

    playwright-cli -s=verify-<run_id> <command>

Use only the commands that the confirmation requires:

- `snapshot` identifies the current page elements.
- `click` operates a page control.
- `fill` enters text in a field.
- `press` sends a keyboard key.
- `goto` opens another page.
- `console warning` shows browser console warnings and errors.

## Evidence

Use `screenshot` when an image proves a relevant state:

    playwright-cli -s=verify-<run_id> screenshot --full-page --filename=<absolute-path>.png

Use a video when it helps prove an interaction. Start the video before the
first page action:

- `video-start <absolute-path>.webm` starts the video.
- `video-chapter <title>` marks a relevant step.
- `video-stop` stops the video.

Save screenshots and videos beside the evidence file defined in mc-verifier.

Cite each file in the evidence report.

## Session cleanup

End every session with this command:

    playwright-cli -s=verify-<run_id> delete-data
