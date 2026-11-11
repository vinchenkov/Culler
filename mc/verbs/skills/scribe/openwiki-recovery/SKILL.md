---
name: openwiki-recovery
description: Read when OpenWiki reports Claim issues, unavailable history, interrupted work, or concurrent edits. The required review for each case.
---

# Wiki recovery

This skill continues the wiki update procedure of the `openwiki` skill, which defines a Claim and `pageUpdateWindows`.
A Claim record is the file under `openwiki/.claims/` that holds the Claims of one generated page.

## Required review

The `openwiki_begin` response reports Claim issues in `claimIssues`.
Use each `claimIssues` entry to select the required review:

- Review a changed page or Claim record against the current source evidence for that page.
- For an absent generated page, plan its restoration or deletion and check the affected links and coverage.
- For an absent or malformed Claim record, establish new Claims for its page.
- Review legacy pages that lack a complete verification record.

Preserve each accurate edit that another author made.

A group of `pageUpdateWindows` with `fullReview: true` has unavailable history.
Review every page in that group against the current source.
Extend the review to related pages only when the evidence requires it.
Continue the update when history is unavailable.

## Interrupted work and concurrent edits

Resume an interrupted run with `openwiki_begin` and its original `mode`.
Complete the returned queue; OpenWiki retains valid page completions and reopens invalid page completions.
For an edit after inspection, read the current page and inspect its Claims again before submission.
For a changed Claim record after the run begins, begin again to load its current Claims.
For a source change, begin again and submit the replacement plan against the current source.

For an error that prevents further review, use the terminal action in the "Report a failure" section of `~/.agents/skills/mc-scribe/SKILL.md`.
Include the affected paths, the unresolved review, and the exact error in the failure reason.
