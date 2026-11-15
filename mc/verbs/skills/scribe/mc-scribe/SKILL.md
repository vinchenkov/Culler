---
name: mc-scribe
description: Submit a wiki result, report a failure, or report a conflict with Project role customization.
---

# mc — Scribe

`mc --help` describes operator commands; it grants no additional role authority.
Use this role skill for assigned run commands and terminal submissions.

## Repository checks

Keep the `Start commit` of the **Git state** part as an ancestor of the wiki result.
Each entry under `openwiki/` must be a regular file.
Its path must contain no backslash or line break.
Each changed wiki file must have no executable permissions.
When an entry of the `Start commit` violates the type rule or the path rule, preserve that entry.
Then use the terminal action in the "Report a failure" section.

## Submit a result

Commit the complete wiki result before this call:

- `mc complete --run <run_id> --commit <full-commit> --wiki-evidence <path>`

Supply these values:

- The `--commit` value is the full identifier of `HEAD` in the assigned repository.
- The `--wiki-evidence` value is `/tmp/openwiki-completion.json`, the receipt of the OpenWiki run.

OpenWiki writes the receipt when `openwiki_finish` completes or `openwiki_begin` returns a `noop` result.
The repository must contain no uncommitted files.
When OpenWiki changes no file, make no commit and submit the `Start commit`.
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
When the wiki update cannot finish, report the cause through this terminal action:

- `mc complete --run <run_id> --infra --reason <cause>`

## Report a customization conflict

Use this terminal action when a custom instruction of the operator contradicts the Culler contract:

- `mc run customization-conflict --request <json>`

The JavaScript Object Notation (JSON) object contains these fields:

- The `run` field contains the `Run identifier` fact of the **Run** part.
- The `passage` field contains the exact conflicting text from the custom instructions.
- The `requirement` field names the Culler contract requirement that the text contradicts.
- The `reason` field explains why both instructions cannot hold.

This terminal action ends the wiki update as failed.
