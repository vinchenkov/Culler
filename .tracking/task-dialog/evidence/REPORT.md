# The Board task dialog — verify evidence

- Mode: verify
- Branch: main, on top of d99eb8d
- Status: **verified**
- Script: `dialog.verify.ts` (this directory)
- Viewport: 1440×1000, then 720×900 for the narrow story

## Stories

| # | Story | Status |
|---|-------|--------|
| 1 | A click on a card opens the task dialog over the page. The dialog covers most of the window. The head names the task with the stage pill and the expedited pill. The description renders as markdown with an ordered list and inline code. The carried notes are a callout. The facts grid names the id, the origin, the created age, the stage and the stage age. The open card wears a ring. The poll keeps the dialog open. | verified |
| 2 | Escape closes the dialog and the card drops its ring. A blocked task's reason is a rust callout at the top of the main column; the head carries the attention chip. | verified |
| 3 | The live run is a sky panel with the role, the run id, the started age and the heartbeat age. The timeline lists six of eight runs and names the two older ones. The head carries the running chip. | verified |
| 5 | An approved packaged task shows a leaf decision callout with the age, and the packet button. | verified |
| 6 | A stale heartbeat paints the live panel rust and says "Heartbeat stale". | verified |
| 7 | The packet button closes the dialog and lands on Review with the task's packet selected. | verified |
| 8 | A click on the backdrop closes the dialog and the card drops its ring. | verified |
| 9 | The close button closes the dialog. | verified |
| 10 | A 720px window stacks the side column under the main column. | verified |

No page errors and no console errors during the run.

## Artifacts

- `01-board.png` — the Board before any click.
- `02-dialog-prose-notes-facts.png` — story 1.
- `03-blocked-reason-callout.png` — story 2.
- `04-live-run-and-timeline.png` — story 3.
- `06-decision-and-packet-button.png` — story 5.
- `07-stale-heartbeat.png` — story 6.
- `08-review-packet-selected.png` — story 7.
- `09-board-after-backdrop-close.png` — story 8.
- `10-narrow-window-stacked.png` — story 10.
- `dialog.webm` — the whole run.

The PNG and webm files are gitignored. The script and this report are tracked.

## Re-run

```sh
cd mc && mise exec -- go build -tags test_fake_routing -o /tmp/mc-fake ./cmd/mc
cd ../dashboard && MC_SMOKE_BIN=/tmp/mc-fake mise exec -- bun ../.tracking/task-dialog/evidence/dialog.verify.ts
```
