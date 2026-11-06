---
name: mc-scribe
description: The command surface and the repository rules of a wiki update.
---

# mc — Scribe

The agent profile sets the model and effort for this run.

`<run_id>` is the `Run identifier` fact of the **Run** part of the run brief. Each command carries it. `mc` refuses a call whose run identifier differs from the live run.

## Terminal

Submit exactly one terminal. A terminal is an `mc` command that ends the run.

- `mc complete --run <run_id> --commit <full-commit> --wiki-evidence <path>`

This terminal requires these two values:

- The `--commit` value is the full identifier of `HEAD` in `/workspace/source`.
- The `--wiki-evidence` value is the OpenWiki completion receipt at `/tmp/openwiki-completion.json`.

The receipt exists after a successful OpenWiki finish. It also exists after a confirmed result with no generated page update.

When OpenWiki changed no file, make no commit. `HEAD` is then the start commit, and the wiki update succeeds with no merge.

The terminal submission and the Git commit are separate operations. Culler keeps a wiki commit only after an accepted terminal submission.

## Project dependencies

The Project home retains dependency caches, installed tools, and Project tool configuration across runs.
The source workspace starts fresh at the assigned commit.
The Scribe omits automatic dependency preparation.
Check retained dependency compatibility before use and preserve the repository restrictions.
Credentials, sessions, role skills, and instructions remain separate for each run.

## Repository

The **Git state** part of the run brief holds the Git facts of this wiki update. The repository at `/workspace/source` starts at the `Start commit` fact. The `Start commit of the last successful wiki update` fact is the start of the range that this wiki update covers. The fact is absent when no wiki update succeeded.

You can write only under `openwiki/`. Repository content outside that folder is read-only. Git metadata stays writable for the commit.

Commit the complete result before the terminal call. The terminal refuses a submission with one of these properties. Correct the submission and submit it again:

- the `--commit` value differs from `HEAD`;
- the repository holds an uncommitted file;
- the `--wiki-evidence` path names no readable regular file.

Culler then checks the wiki commit. A failed check ends the run and ends the wiki update as failed. The command then exits with code 1 and states the failed check. Do not submit again. These are the checks:

- the start commit is an ancestor of the wiki commit;
- each changed path is under `openwiki/`;
- the file `openwiki/INSTRUCTIONS.md` is the same as in the start commit;
- each original document under `openwiki/` preserves its content, except the fields `title`, `type`, `document`, and `tags`;
- each original document under `openwiki/` exists in the start commit;
- each entry under `openwiki/` of the start commit and of the wiki commit is a regular file;
- each path under `openwiki/` of those two commits holds no backslash and no line break;
- each wiki file that the wiki commit changes has no executable permissions;
- the receipt names the start commit, a complete OpenWiki run, and the pinned OpenWiki version;
- the wiki files of the wiki commit are exactly the files of that complete run;
- the wiki commit holds no record of an incomplete OpenWiki run.

## Project documents

Original project documents reside outside `openwiki/`.
Integrate relevant project documents through generated explanations that cite their authoritative paths.
Preserve each original file and its format.

An imported wiki can contain original documents.
Change only their wiki display metadata through the openwiki skill.

## Evidence

A Claim states one factual property that evidence can confirm or refute. The openwiki skill holds the Claim procedure. A generated Claim cites original repository evidence directly. A generated page supplies context. A generated page cannot be the original evidence of another Claim. An evidence cycle supplies no factual support.

## A substantive error in a project document

Do not correct the body of a project document.
A Worker corrects original documents outside `openwiki/` in a task.

## A run that cannot give a result

Report the cause that prevents the wiki update. This command ends your run. Submit it in place of the terminal above:

- `mc complete --run <run_id> --infra --reason <cause>`

A symbolic link or a submodule under `openwiki/` of the start commit prevents the wiki update. Do not remove it. Report it with this command.

A failed wiki update changes no task and no allowance. The next wiki update of the Project starts after the wiki update interval.

The runner owns `mc run model-response`.
This command records complete model responses for the current attempt.
Do not call this runner command.
Continue the wiki update after connection recovery.

## Project role customization

The Culler contract has priority over custom instructions.
Custom instructions replace standard defaults in this skill within that contract.
Follow the role directive's distinction between the contract and defaults.
If a custom instruction conflicts with the contract during work, submit this terminal:

- `mc run customization-conflict --request <json>`

The JavaScript Object Notation (JSON) object contains these fields:

- The `run` field: your run identifier.
- The `revision` field: the customization revision from the run brief.
- The `passage` field: the exact conflicting text from that revision.
- The `requirement` field: the Culler contract requirement that the text contradicts.
- The `reason` field: why both instructions cannot hold.

The command records the Project and the role from your run.
It ends the run and ends the wiki update as failed.
It changes no task, no allowance, and no pause state.
