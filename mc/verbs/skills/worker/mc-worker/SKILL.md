---
name: mc-worker
description: Worker commands, submission checks, and procedures for conditional run brief parts.
---

# mc — Worker

`mc --help` describes operator commands; it grants no additional role authority.
Use this role skill for assigned run commands and terminal submissions.

The same command authority applies to Codex, the Claude Agent Software Development Kit (SDK), and OpenCode.
Culler supplies the harness configuration and the skills at `~/.agents/skills/`.

`<id>` in a read can identify any task.

## Your repository

The **Git state** part names the destination branch and, on later runs, the start and base commits.
Your checkout starts at the Start commit when that fact is present.
The base commit is where the first Worker run started.
Without a Base commit fact, the initial HEAD is the base commit.
The start commit is the checkpoint, else the last submitted commit, else the base commit.

## Task terminal

Use a success terminal when the work meets the criteria:

- `mc complete <task> --run <run_id> --status worked --commit <full HEAD sha>`
- `mc complete <task> --run <run_id> --status worked --commit <full HEAD sha> --outputs <path>`

Before submission, run these checks:

1. `git -C /workspace/source status --porcelain=v1 --untracked-files=all` must print nothing, including untracked files.
2. Read `git -C /workspace/source rev-parse HEAD` and pass that exact full lowercase identifier as `<full HEAD sha>`.
3. HEAD must be the start commit or its descendant.
4. HEAD must differ from the base commit.
5. The commit must preserve `openwiki/` from the start commit or an incorporated destination commit.

A start commit that meets these conditions and all criteria needs no new commit.
Fix a refused submission before another attempt.

### The task execution report

`--outputs` supplies the Verifier with the absolute path of a report that identifies committed files and completion evidence.
List `/mc/artifacts/` and select an existing plane directory.
Create the report at `/mc/artifacts/<plane>/<task>/output/<your-file>`.
Only mounted planes retain artifacts after the run; do not create a new directory directly under `/mc/artifacts/`.
The report must be a nonempty regular file of at most 64 MiB.

## A run that cannot give a result

Complete the work with available results after an execution failure when possible.
Otherwise select the applicable terminal below.

### A decision that only the operator can make

Use this terminal only for a required operator decision, such as contradictory criteria or a criterion without a checkable measure:

- `mc complete <task> --run <run_id> --needs-operator --reason <prose>`

State the required decision and why it prevents completion.
This alternative records the same decision, applies only to your task, and takes no run flag:

- `mc task block <task> --reason <prose>`

### A task run failure

For an unavailable requirement, first apply Unavailable actions and checks.
Use one of these terminals for another execution condition that prevents completion:

- `mc run infra-failure <run_id> --condition <observed condition>`
- `mc complete <task> --run <run_id> --infra --reason <observed condition>`

Report direct observations without an inferred connection diagnosis.
Continue the assigned work after connection recovery; leave connection reports to the runner.

## Case: correction round

A **Correction from the Verifier** part supplies absolute Correction file and Evidence file paths.
Read both files with ordinary file reads; no mc verb returns their contents.
The correction file supplies the complete objection for this run.
Resolve its unmet criteria and task regressions, including regressions outside the acceptance criteria.
Unchanged failures outside the criteria alone require no correction.
If either file is missing or unreadable, use the task run failure terminal and identify the failed read.

## Case: merge repair

A **Merge candidate** part identifies the submitted commit, destination commit, and conflict paths.
Read both changes at each conflict path.
When both intents can hold, merge the destination commit into your work and resolve each conflict.
Preserve both merge inputs and commit no conflict markers.
For a wiki conflict, report the paths through the operator decision terminal.
When the intents contradict, keep the work unchanged and use the operator decision terminal.
Name each conflict path and both intents.
Apply any conflict decision in the Task description.
The completion report must describe the repair and any unresolved decision.

## Case: revision request

Meet the operator's Revision request in full before submission.

## Case: task revision

The **Task revision** part identifies the exact revision assigned to this run.
The Task description defines the required result. Revision instructions define the requested corrections.
If the part supplies an Instructions command, read every page before changes.
Use `next_offset` for the next page until it is null.
For a changed description, read the Description diff before edits.
An Earlier instructions command points to prior corrections without a Verifier pass before this revision.
Read those instructions. The current description and latest instructions replace any incompatible earlier instruction.
Follow `next_offset` until it is null for each selected text view.
The Prior evidence command returns paths and commit references, not report contents.
Read only the report sections and source files that explain the required change.

Start from the saved checkout and preserve each correct part of the work.
For a one-line text correction, locate the exact text and inspect its callers and relevant tests.
Change that text and any affected tests; preserve unrelated implementation and documents.
For a broad revision, map each changed criterion to the saved files before edits.
Use the revision start commit to inspect the cumulative correction with Git.
Keep the complete task requirements and the normal check requirements in effect.
If a **Merge candidate** part is present, complete its repair before submission.

The completion report identifies the revision, changed files, checks, and unresolved requirements.
Keep it concise. Link detailed logs and prior reports instead of copying them.

## Revision reads

- `mc task revision <task> --revision <revision>`
- `mc task revision <task> --revision <revision> --view <view> --offset <offset>`

Views are summary, instructions, earlier-instructions, changes, and evidence.
The offset counts characters and starts at zero.

## Case: checkpoint

Use this terminal to retain incomplete work for another Worker run:

- `mc complete <task> --run <run_id> --checkpoint --commit <full HEAD sha>`
- `mc complete <task> --run <run_id> --checkpoint --commit <full HEAD sha> --outputs <path>`

The Task terminal repository checks also apply to checkpoints.
A Checkpoint version fact means that your repository starts at that saved commit; continue the work there.

## Unavailable actions and checks

Use the project-environment-setup skill to assess each unavailable prerequisite.
Preserve required behavior and test assertions.
Continue independent work and checks that the container supports.
Record actual command results and every skipped or unavailable check, including each `UNVERIFIED` entry, in the completion report.
Keep unavailable checks unverified; dependency installation alone proves no dependent behavior.

Select the action for each unavailable requirement:

- Fix source, tests, Workspace dependencies, or setup script defects within the task's authority.
- For a prerequisite that Image dependencies can supply, follow Project dependencies and submit a checkpoint.
- For a criterion or new or changed test, use the operator decision terminal when every agent container lacks its capability.
- Investigate an unclear cause before choosing a terminal; report an execution failure when it prevents further work.

State the required capability, the affected requirement, and the evidence in an operator decision report.
An unavailable host check outside the criteria requires no operator decision or task run failure by itself.
This exception excludes new or changed tests, which must pass.
Separate release and host acceptance requirements retain their authority.

## Project dependencies

Use these commands with one JavaScript Object Notation (JSON) object on standard input:

- `mc environment get`
- `mc environment propose`

The get request contains your `project_id` from Project role customization.
Use the returned configuration object for the proposal.
Preserve the selected base and Culler's private runtime.
Commit the proposed Dockerfile and every build file first.
The proposal contains the complete `configuration` and an `explanation` that follows the project-environment-setup skill.
Set `configuration.source_commit` to the full committed source identifier.
Include the Dockerfile and required file contents in `dockerfile` and `build_files`.
After the proposal, use the checkpoint terminal.

## Reads

- `mc task get <id>`
- `mc task list`
- `mc packet list`
- `mc activity list --kind <kind> --limit <n>`
- `mc project list`

## Project role customization

For a custom instruction that conflicts with the Culler contract, use this terminal:

- `mc run customization-conflict --request <json>`

The JSON object contains:

- The `run` field: your run identifier;
- The `passage` field: the exact conflicting text;
- The `requirement` field: the contract requirement that it contradicts;
- The `reason` field: why both instructions cannot hold.
