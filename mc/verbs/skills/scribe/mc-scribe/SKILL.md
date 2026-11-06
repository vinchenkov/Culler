---
name: mc-scribe
description: Submit a wiki result, report a failure, or report a conflict with Project role customization.
---

# mc — Scribe

## Repository checks

Keep the start commit as an ancestor of the result.
Each wiki entry must be a regular file.
Its path must contain no backslash or line break.
Each changed wiki file must have no executable permissions.
If a start entry violates the type or path restriction, preserve it and use the failure terminal below.

## Submit a result

Commit the complete wiki result before this call:

- `mc complete --run <run_id> --commit <full-commit> --wiki-evidence <path>`

Supply these values:

- The `--commit` value is the full identifier of `HEAD` in the assigned repository.
- The `--wiki-evidence` value is the receipt at `/tmp/openwiki-completion.json` from the supplied OpenWiki tools.

The repository must contain no uncommitted files.
The receipt must be a readable regular file.
When OpenWiki changes no file, make no commit and submit the start commit.
Keep the wiki files unchanged after OpenWiki creates the receipt.

A refusal before the result checks leaves the run active.
Correct the stated error and repeat the call.
Culler then checks the wiki result against the contract and the receipt.
A failed result check ends the run with exit code 1.
Do not submit again after a failed result check.
Submit the result without operator confirmation.
Leave the merge to Culler.

## Report a failure

Continue the wiki update after connection recovery.
When the wiki update cannot finish, report the cause through this terminal:

- `mc complete --run <run_id> --infra --reason <cause>`

## Report a customization conflict

Use this terminal for a conflict with the Culler contract:

- `mc run customization-conflict --request <json>`

The JavaScript Object Notation (JSON) object contains these fields:

- The `run` field contains the run identifier.
- The `revision` field contains the customization revision from the run brief.
- The `passage` field contains the exact conflicting text from that revision.
- The `requirement` field names the Culler contract requirement that the text contradicts.
- The `reason` field explains why both instructions cannot hold.

This terminal ends the wiki update as failed.
