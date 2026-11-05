---
name: verify-behavior
description: Use Playwright Command Line Interface browser tools when the Verifier needs browser interaction or visual evidence for a user interface change.
---

# Verify behavior

Use this skill when a Worker's change needs browser interaction or visual
confirmation. Do not use this skill for other changes.

## Browser tool

Culler ships the Playwright Command Line Interface (CLI) and the
Chrome for Testing browser. Start each browser session with this command:

    playwright-cli -s=verify-<run_id> --browser=chromium open <target-url>

`<run_id>` is the `Run identifier` fact of the **Run** part of your run brief.
The `--browser=chromium` flag selects the installed browser. The default
browser is unavailable.

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

Save each screenshot and video in this task's durable verification directory:

    /mc/artifacts/<plane>/<task-id>/verification/

Cite each file in the evidence report.

## Session cleanup

End every session with this command:

    playwright-cli -s=verify-<run_id> delete-data

Do not let session data enter another role run.
