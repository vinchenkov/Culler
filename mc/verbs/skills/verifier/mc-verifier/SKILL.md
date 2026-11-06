---
name: mc-verifier
description: The complete and only mc verb surface for the Verifier role, including the flag law and SHA provenance checks on a verdict and the procedure for each case; load before any mc call and before submitting the terminal — mc ships no --help, and a verb absent from this skill does not exist for this role.
---

# mc — Verifier

The agent profile sets the model and effort for this run.

`<run_id>` is the `Run identifier` fact of the **Run** part of your run brief. Each `mc verifier verdict` call and each `mc complete` call carries it as `--run <run_id>`. `mc run infra-failure` takes it as its first argument. `mc task block` and the reads take no run identifier. `<task>` is the `Identifier` fact of the **Task** part. `<id>` in a read is the identifier of any task. `mc` rejects a call whose run identifier does not match the live run. `mc` does not apply a rejected call. A terminal is an `mc` command that ends the run.

## Your repository

`/workspace/source` is an independent repository for this run. It starts at the `Submitted commit` fact of the **Git state** part, with a clean tree. The Project repository stays outside your write access.

The `Base commit` fact of the **Git state** part names the commit where the first Worker run of the task started. The change that you judge starts at the base commit and ends at the submitted commit. This command shows that change:

    git -C /workspace/source diff <Base commit> <Submitted commit>

A merge repair merges the destination branch into the work of the task. The diff then also holds the changes of the destination branch. This command shows each merge commit of the task:

    git -C /workspace/source log --first-parent --merges <Base commit>..<Submitted commit>

Judge the changes of this task against its criteria and assess broad checks for task regressions.
Apply the project-wiki skill to requested reports and original project documents outside `openwiki/`.
Check the committed deliverable in its requested format; a task execution report does not replace it.
Return task-authored wiki changes to the Worker; accepted wiki updates from the destination branch are separate changes.

## Project dependencies

The Project home retains dependency caches, installed tools, and Project tool configuration across runs.
The source workspace starts fresh at the assigned commit.
The runner executes the retained setup script before the agent starts.
Check dependency compatibility with the current declarations and the selected image.
Credentials, sessions, role skills, and instructions remain separate for each run.

Use the project-environment-setup skill when prerequisites prevent required checks.
Project import permits limited capabilities.
Repository test requirements still control task verification.
This command accepts one JavaScript Object Notation (JSON) object on standard input:

- `mc environment get`

The get request contains your `project_id`.
Record each gate that cannot run as not executed.
The section "The outcome" of your directive selects the terminal for that gate.
In a correction, describe the unavailable prerequisite and the files or declarations that the Worker must change.
The correction Worker commits the Dockerfile and build files before a proposal for Image dependencies.
That proposal requires operator approval before the image build.
Preserve the submitted source during verification.

## Verdict terminal

Submit exactly one terminal. Both verdicts require `--evidence`. The `pass` verdict requires `--sha` and prohibits `--correction`. The `correct` verdict requires `--correction` and prohibits `--sha`.

- `mc verifier verdict <task> --run <run_id> --outcome pass --evidence <path> --sha <sha>`
- `mc verifier verdict <task> --run <run_id> --outcome correct --evidence <path> --correction <path> --problem <prose> --explanation-evidence <prose> --resolution <prose>`

Run `git -C /workspace/source rev-parse HEAD`. Pass its exact lowercase output as `--sha`. The terminal checks that value against the submitted commit. The terminal refuses an invented commit and a stale commit.

A `pass` verdict moves the work to the Packager, and Culler merges the commit later.

The agent runs of a correction start first in its Project until the corrected commit merges without a conflict.
An approved task that waits for its merge precedes them.
The other tasks of the Project continue while a correction waits for an operator decision.
A task run failure uses the task run failure procedure and preserves the Verifier allowance.

### The gates of a `pass` verdict

A `pass` verdict requires a successful result on the submitted commit for each gate that a criterion binds.
A `pass` verdict also requires successful new or changed tests and a broad assessment with no unresolved task regression.
A gate whose execution dependency is unavailable stays unverified.
A selected test group that executes zero tests fails.
Keep the dependency versions of the submitted commit during verification.
Reject a submitted file that contains an unresolved merge conflict.

### Broad checks and base comparison

Run the Project's normal broad checks on the submitted commit, including checks outside the acceptance criteria.
Repository instructions define the normal checks and the conditions for additional checks.
Preserve required build and analysis checks.
One execution can supply both criterion evidence and broad evidence.
Successful submitted checks require no base execution for regression detection.
New or changed tests require successful submitted results without a required base failure.

For each failed check present on the base commit, compare the failure with that commit:

1. Identify the smallest test or group that reproduces the submitted failure.
2. Preserve relevant test order, inputs, and environment conditions.
3. Create a separate source copy at the base commit outside `/workspace/source`.
4. Prepare that commit's declared dependencies and configuration in separate build files.
5. Run only the failed check and its necessary prerequisites on the base commit.
6. Compare the failure causes and retain the results in the evidence file.

Keep the assigned repository at the submitted commit with a clean tree.
Compare a package build failure before conclusions about tests that could not start.
Two nonzero exit statuses alone do not establish an unchanged failure.
A base dependency failure does not explain a submitted assertion failure.

Use limited repetitions with the relevant context when results differ between attempts.
An inconclusive comparison remains explicit in the evidence and prevents PASS for an unresolved executable failure.
An execution problem that prevents the required assessment uses the task run failure procedure.

The same check and failure cause on both commits establish an unchanged failure.
An unchanged product defect and an unchanged platform failure receive the same treatment.
An unchanged failure outside the criteria permits PASS when the other requirements hold.
A task that promises to repair an old defect still requires evidence of that repair.

A regression from the task requires CORRECT, including one outside the acceptance criteria.
A new or changed test that fails also requires CORRECT.
Assess new skips and lost test coverage as part of the task's changes.
Changed tests must preserve required behavior and assertions.

Run later independent checks separately when an earlier command stops a script or a chain of component commands.
Keep checks with unavailable prerequisites explicit as unverified.
An unchanged failure explains only the reproduced failure, not the tests that it prevented from executing.

Record each skipped or unavailable host test and its required capability.
Outside the criteria, these tests permit PASS when the other requirements hold.
Unchanged failures and unavailable host tests alone create no operator decision, mandatory manual check, or additional approval.
The normal Review Packet supplies this information to the operator.

### The two files

You record two paths. Culler retains the paths and the structured explanation. Later runs read both files. Later runs have no memory of your run. A file that a later run cannot open is the same as no file.

`--evidence` names the evidence file.
The Packager receives this path and cites its material results in the Review Packet.
The evidence file contains these facts:

- The submitted commit and the base commit used for comparison.
- Each executed check and its actual result.
- The evidence for each acceptance criterion and each new or changed test.
- Each unchanged failure and the comparison that establishes it.
- Each skipped or unavailable check and its reason, including every `UNVERIFIED` entry.
- Each incomplete comparison and its effect on the verdict.
- Checks that stopped before completion and the coverage that remains unverified.
- Each optional check outside the required scope, marked not applicable (N/A), with its reason.

A command that exits zero succeeds; its skipped tests remain unverified.
A command that exits nonzero remains unsuccessful even when the task receives `pass`.
Unchanged failures and unavailable host tests outside the criteria create no separate repair task or additional approval requirement.

`--correction` names the correction file. The correction file supplies the complete report for the next Worker. The next Worker receives this path as binding input with the operator explanation. An inaccessible correction file causes a task run failure. Only a rejected correction consumes the Verifier allowance. A pass restores it.

Write both files inside one plane. Write both files in the folder of this task. Pass absolute paths. The evidence path is `/mc/artifacts/<plane>/<task>/verification/<your-file>`. The correction path is `/mc/artifacts/<plane>/<task>/corrections/<your-file>`. List `/mc/artifacts/` and use a plane directory that already exists. The content of a plane stays after the run ends. The Worker and the Packager mount the same plane. The plane is writable. Run `mkdir -p` for your two folders. New folders in a plane are correct.

Do not write a file directly at `/mc/artifacts/<your-file>`. Do not create a new directory at that level. `/mc/artifacts/` itself is ordinary container storage. A write at that level succeeds and looks correct. The container destroys that write when your run ends. `/workspace/source`, `/tmp`, `$TMPDIR`, and your working directory end with the container too.

The terminal opens both files before it records anything. The terminal refuses a path in each of these cases:

- The path is not inside a mounted plane.
- The file is missing.
- The file is empty.
- The file is not a regular file.
- The evidence file is larger than 16 MiB.
- The correction file is larger than 1 MiB.

The terminal writes nothing when it refuses. The task does not move. After a refusal, do not pass a path that you did not write. Write the file. If `/mc/artifacts/` contains no plane directory, report `--infra`.

Write the correction for the next Worker. The correction must state:

- which criteria failed or which task regressions require correction;
- what the evidence shows;
- what satisfies the criteria and resolves the task regressions.

### `/workspace/source` stays at the submitted commit for both verdicts

The terminal derives the state of `/workspace/source` before it records anything, for both verdicts. HEAD must be the submitted commit. `git -C /workspace/source status --porcelain=v1 --untracked-files=all` must print nothing. Tracked files must have no drift. Nothing must be staged. No untracked file must exist. A file that the Project ignores does not count.

A gate that you run again can dirty the tree. Each of these actions dirties it:

- a build that rewrites a lockfile;
- a formatter;
- a test that regenerates a fixture;
- an experimental edit that tests a fix;
- a log or a status file that a gate writes in the tree.

The terminal then refuses your verdict at the end of the whole run. Run gates in a way that leaves the tree unchanged. Write gate logs outside `/workspace/source`.

## A run that cannot give a result

Two events end a run without a result. The event selects the command. Each command in this section ends your run, so submit exactly one. No command here replaces a verdict that the evidence supports.

### A decision that only the operator can make

These are examples of such decisions:

- Two criteria contradict each other.
- A criterion has no measure that you can check.
- A gate that a criterion binds needs a capability that no agent container has.

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

## Correction explanation

A CORRECT verdict requires these arguments:

- The `--problem` argument identifies the unmet criteria or task regressions.
- The `--explanation-evidence` argument states the evidence for the rejection.
- The `--resolution` argument states the change that satisfies the criteria or resolves the task regressions.

Each argument must contain readable text for the operator.
The initial rejection starts a correction sequence at zero.
Three subsequent rejected corrections require another operator allowance.
A pass resets the count to zero.
