---
name: packager-browser
description: Read when browser evidence can confirm the change for media selection and capture with the supplied browser.
---

# Browser evidence

Capture new media only for a required state that the Verifier evidence omits.
Culler supplies the Playwright Command Line Interface (CLI) and Chrome for Testing.
Culler prepares workspace dependencies before the run.
Before use, check retained dependencies against current declarations and the selected **Project environment**.

Read **Repository** in `~/.agents/skills/mc-packager/SKILL.md` before a build for its temporary copy requirements.
Read **Packet result** there for the durable packet directory.
Save new media in that directory.

## Browser session

Start the application of the reviewed change yourself.
Use its address as `<target-url>`.
Use the `<run_id>` from **Run** to give the session its identity:

    playwright-cli -s=packager-<run_id> --browser=chromium open <target-url>

The flag selects the installed browser.
The default browser is unavailable.
Use the same session for each subsequent command:

    playwright-cli -s=packager-<run_id> <command>

Use only commands that the packet requires:

- `snapshot` identifies the page elements.
- `click` operates a page control.
- `fill` enters text in a field.
- `press` sends a keyboard key.
- `goto` opens another page.

Capture a useful state with this command:

    playwright-cli -s=packager-<run_id> screenshot --full-page --filename=<absolute-path>.png

Use a video when an interaction needs it.
Start the video before the first page action:

- `video-start <absolute-path>.webm` starts the video.
- `video-chapter <title>` marks a relevant step.
- `video-stop` stops the video.

End every session with this command to remove its data:

    playwright-cli -s=packager-<run_id> delete-data
