---
name: openwiki-recovery
description: Read when OpenWiki reports Claim issues, unavailable history, interrupted work, or concurrent edits.
---

# Wiki recovery

## Required review

Use each `claimIssues` entry to select the required review:

- Review a changed page or Claim record against the current source evidence for that page.
- For an absent generated page, plan its restoration or deletion and check the affected links and coverage.
- For an absent or malformed Claim record, establish new Claims for its page.
- Review legacy pages that lack a complete verification record.

Preserve accurate external edits.

The `basis` field in each `pageUpdateWindows` group identifies the source comparison.
For `source_inventory` or `git`, assess the returned `changedPaths` and plan the affected pages.
Include new source files in that assessment, even when no existing Claim cites them.
For `fullReview: true`, review every page in that group against the current source.
Extend the review to related pages only when the evidence requires it.
Continue the update when history is unavailable.

## Interrupted work and concurrent edits

Resume an interrupted run with `openwiki_begin` and its original `mode`.
Complete the returned queue; OpenWiki retains valid page completions and reopens invalid page completions.
For an edit after inspection, read the current page and inspect its Claims again before submission.
For a changed Claim record after the run begins, begin again to load its current Claims.
For a source change, begin again and submit the replacement plan against the current source.

For an error that prevents further review, use the failure procedure in `~/.agents/skills/mc-scribe/SKILL.md`.
Include the affected paths, the unresolved review, and the exact error in the failure reason.
