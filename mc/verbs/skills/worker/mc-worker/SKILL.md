---
name: mc-worker
description: The complete and only mc verb surface for the Worker role, including the submission checks and the procedure for each case that a run brief can start; load before any mc call and before submitting the terminal — mc ships no --help, and a verb absent from this skill does not exist for this role.
---

# mc — Worker

The agent profile sets the model and effort for this run.

`<run_id>` is the `Run identifier` fact of the **Run** part of your run brief. Each `mc complete` call carries it as `--run <run_id>`. `mc run infra-failure` takes it as its first argument. `mc task block` and the reads take no run identifier. `<task>` is the `Identifier` fact of the **Task** part. `<id>` in a read is the identifier of any task. `mc` rejects a call whose run identifier does not match the live run. `mc` never applies a rejected call. A terminal is an `mc` command that ends the run. A successful terminal ends the run.

## Your repository

`/workspace/source` is an independent repository for this run. It starts at the `Start commit` fact of the **Git state** part. It holds the history of the start commit and of the destination branch. The `Destination branch` fact of the **Git state** part names that branch. The Project repository stays outside your write access. Culler retains the commit that you submit after the run folder disappears.

The `Base commit` fact names the commit where the first Worker run of the task started. The start commit is the checkpoint commit, else the last submitted commit of the task, else the base commit. When the **Git state** part has no `Base commit` fact, this run is the first Worker run of the task. Your repository then starts at the head of the destination branch, and that commit becomes the base commit.

## Project dependencies

The Project home retains dependency caches, installed tools, and Project tool configuration across runs.
The source workspace starts fresh at the assigned commit.
The runner executes the retained setup script before the agent starts.
Check dependency compatibility with the current declarations and the selected image.
Credentials, sessions, role skills, and instructions remain separate for each run.

Use the project-environment-setup skill for dependency changes.
These commands accept one JavaScript Object Notation (JSON) object on standard input:

- `mc environment get`
- `mc environment propose`

The get request contains your `project_id`.
The proposal contains the complete `configuration` and a plain-language `explanation`.
Write `explanation` for the operator in at most two short sentences.
Identify the Image dependencies and why the Project needs them.
Keep check evidence, diagnostic output, and continuation instructions separate from `explanation`.
Use the configuration object from the get response.
Record the full committed source as `configuration.source_commit`.
Commit the proposed Dockerfile and every build file before this report.
Include the Dockerfile and required file contents in `dockerfile` and `build_files`.
The proposal preserves the selected environment until approval, preparation, and source acceptance.
After a proposal, use the checkpoint terminal to retain your source.
Culler resumes development after the setup report and source acceptance.
Repository test requirements still control task verification.
Source delivery still requires the ordinary task review.

## Task terminal

If you execute the work inline and do not orchestrate, state the reason in your response before the terminal. Do not add an unsupported flag to carry the reason. Culler keeps the transcript of the run, so your response stays on record.

Submit exactly one terminal. Commit the complete deliverable first. `<full HEAD sha>` is the full lowercase output of `git -C /workspace/source rev-parse HEAD`. Do not pass an abbreviation, a branch name, or a commit that you did not read back from the repository.

- `mc complete <task> --run <run_id> --status worked --commit <full HEAD sha>`
- `mc complete <task> --run <run_id> --status worked --commit <full HEAD sha> --outputs <path>`

The terminal examines the repository before it records anything. The repository must satisfy four conditions:

1. Clean tree. `git -C /workspace/source status --porcelain=v1 --untracked-files=all` must print nothing. Untracked files count. Commit or delete each scratch file, build output, log, and notes file.
2. Correct HEAD. HEAD must equal the commit that you pass.
3. Correct lineage. The commit that you pass must be the start commit or a descendant of the start commit. If it is not, bring your commits onto the start commit with a rebase or a cherry-pick.
4. Changed result. The commit that you pass must differ from the base commit. The start commit satisfies this condition when it differs from the base commit.

Run the two checks yourself before you submit: `status` and `rev-parse HEAD`. Nothing moves when the terminal refuses. A resubmit after the fix is safe.

### The task execution report

`--outputs` names your task execution report. The report identifies the committed files and the completion evidence. The Verifier receives its path. Write the report at `/mc/artifacts/<plane>/<task>/output/<your-file>` and pass that absolute path.

A plane is one durable directory that Culler mounts under `/mc/artifacts/`. Its content stays after the run ends, and the later roles mount the same plane. List `/mc/artifacts/` and use a plane directory that already exists. The plane is writable, so create your folder with `mkdir -p`.

Do not write at `/mc/artifacts/<your-file>`. Do not create a directory at that level. `/mc/artifacts/` itself is ordinary container storage. A write there succeeds, looks correct, and dies with your run. `/tmp`, `$TMPDIR`, and your working directory die with your run too.

The terminal opens the file before it records the path. The terminal refuses the path in each of these cases:

- the path is outside a mounted plane;
- the file is missing;
- the file is empty;
- the file is over 64 MiB;
- the file is not a regular file.

The report does not replace the commit. The complete deliverable, including knowledge work and necessary local evidence, must be in the commit. Write original documents directly at the paths that the shared project-wiki skill defines. OpenWiki is the tool that generates the wiki pages of the Project under `openwiki/`. It owns each `index.md` file and its control records there. Keep those files unchanged during ordinary document work.

## A run that cannot give a result

Two events end a run without a result. The event selects the command. Each command in this section ends your run, so submit exactly one. No command here substitutes for the completed work.

### A decision that only the operator can make

These are examples of such decisions:

- Two criteria contradict each other.
- A criterion has no measure that you can check.

The reason states the decision that you need and why. The command sets Decision Pending on the task. The operator resolves the decision with a requirements revision, or the operator cancels the task.

- `mc complete <task> --run <run_id> --needs-operator --reason <prose>`
- `mc task block <task> --reason <prose>`

The two commands record the same decision. Use the first command. The second command applies to your own task only and takes no `--run` flag.

### A task run failure

Report the condition that prevents completion:

- `mc run infra-failure <run_id> --condition <observed condition>`
- `mc complete <task> --run <run_id> --infra --reason <observed condition>`

The assigned task owns three task run failure attempts.
Each failed run after process start consumes one attempt.
The first failure permits an automatic retry after 30 seconds.
The second failure permits an automatic retry after two minutes.
The third failure sets `stalled`, which the dashboard shows as Retries Exhausted.
Forward stage progress or an operator task resume restores three attempts.
A task resume preserves an independent Decision Pending condition.

Dispatch and launch failures before process start consume the Project infrastructure allowance.
A required operator decision consumes zero failure or correction attempts.
Only direct execution observations can establish a connection condition.
An agent report retains an unknown cause.


### Case: correction round

A **Correction from the Verifier** part means that the Verifier rejected the last submission. **The `Correction file` fact is an absolute path to a real file. Open it with ordinary file reads.** No `mc` verb returns its contents. Culler stores the path, not the prose.

The file lives under `/mc/artifacts/<plane>/<task>/corrections/`. The `Evidence file` fact names the Verifier's evidence under `verification/`. Read that evidence too.

Read the correction file before you change anything.
The file names unmet criteria or task regressions and their evidence.
Resolve task regressions even when they affect checks outside the acceptance criteria.
New or changed tests must pass.
Unchanged failures and unavailable host tests outside the criteria alone require no correction.
The correction file supplies the complete objection for this run.

If the path is missing or unreadable, report a task run failure. Do not guess. Report `--infra` and state the failure.

### Case: merge repair

Culler merges each submitted commit with the destination branch. A **Merge candidate** part means that the merge had a conflict. The part holds one JSON record with these fields:

- the `status` field is `conflict`;
- the `submitted_commit` field and the `destination_commit` field name the two merge inputs;
- the `conflict_paths` field names each path with a conflict.

Read the two changes at each conflict path. Then decide whether one commit can keep the intent of both changes.

- When one commit can keep both intents, merge the destination commit into your work and resolve each conflict. Your commit must preserve both merge inputs. Commit no file that contains a conflict marker.
- When the two changes contradict, keep your work unchanged. Report the decision for the operator through the `--needs-operator` terminal. Name each conflict path and the intent of each change. The operator records the decision in the description. The next Worker run receives the revised description and the same **Merge candidate** part.

The same repair procedure applies before and after operator approval.
A merge repair clears the operator approval of the task.
The repaired version requires verification, a new review packet, and a new operator decision.
A later conflict starts another merge repair.

Your repair must preserve complete Project documents and one consistent set of accepted OpenWiki control records. The Verifier assesses the repaired commit again.

### Case: revision request

The **Revision request** part holds the request of the operator. The operator returned the review packet with this request. You must meet the request in full in this run. Then submit the commit.

### Case: requirements revision

A **Requirements revision** part means that the operator changed the requirements of the task. The part holds the reason for the change. The reason explains why the operator changed the requirements. The reason is context and adds no requirement. The description of the **Task** part holds every requirement.

1. Check the start commit against each criterion of the description.
2. Change the work where a criterion fails.
3. Submit the commit.

When the start commit meets each criterion and differs from the base commit, submit the start commit unchanged.

A **Merge candidate** part in the same run brief means that the merge repair remains open. Do the merge repair before you submit. When the description states a decision about the conflict, apply that decision in the repair.

## Case: checkpoint

The checkpoint terminal action asks Culler to keep your commit for another Worker run. Use it when this run cannot finish the deliverable. The terminal action applies the same four repository conditions.

- `mc complete <task> --run <run_id> --checkpoint --commit <full HEAD sha>`
- `mc complete <task> --run <run_id> --checkpoint --commit <full HEAD sha> --outputs <path>`

Culler retains the checkpoint commit. The task stays with the Worker, and the next Worker run starts at the checkpoint.

A `Checkpoint version` fact in the **Git state** part means that an earlier Worker run saved a checkpoint. Your repository starts at that checkpoint. Continue the work from that commit.

## Reads

- `mc task get <id>`
- `mc task list`
- `mc packet list`
- `mc activity list --kind <kind> --limit <n>`
- `mc project list`

## Out of scope

Culler and the operator dispatch, verify, package, merge, and grade your work. A probe for a verb not listed here wastes the run.

The runner owns `mc run model-response` and `mc homie model-response`.
These commands record complete model responses for the current attempt.
Do not call these runner commands.
Continue the assigned work after connection recovery.

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

The command records the Project, role, and subject from your run.
It ends the run with a customization conflict and preserves the subject's stage and accepted work.
A run without a subject blocks that role within its Project.
The Project retains its pause state.
The command preserves all failure and correction allowances.

## Correction allowances

The Verifier correction allowance permits three unsuccessful corrections.
A Verifier pass restores that allowance.
Run retries and an operator packet revision preserve that allowance.
Each merge conflict starts a merge repair.
A merge repair clears the operator approval of its task.
The agent runs of a correction start first, unless an approved task in the Project waits for its merge.
The other tasks of the Project continue while a correction waits for an operator decision.

The merge repair must preserve both intents where they agree.
Contradictory intents require an operator decision that identifies each path and both intents.
The completion report must describe the repair and any unresolved decision.
