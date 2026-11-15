---
name: mc-verifier
description: Verifier commands, evidence requirements, and procedures for the cases named in the directive.
---

# mc — Verifier

`mc --help` describes operator commands; it grants no additional role authority.
Use this role skill for assigned run commands and terminal submissions.

## Verdict terminal

- `mc verifier verdict <task> --run <run_id> --outcome pass --evidence <path> --sha <sha>`
- `mc verifier verdict <task> --run <run_id> --outcome correct --evidence <path> --correction <path> --problem <prose> --explanation-evidence <prose> --resolution <prose>`

Run `git -C /workspace/source rev-parse HEAD` and pass its exact lowercase output as `--sha` on PASS.
PASS prohibits `--correction`; CORRECT prohibits `--sha`.
Before either verdict, `git -C /workspace/source status --porcelain=v1 --untracked-files=all` must print nothing.

For CORRECT, supply readable operator text in each explanation argument:

- The `--problem`: the unmet criteria or task regressions.
- The `--explanation-evidence`: the evidence for rejection.
- The `--resolution`: the change that satisfies the criteria or resolves the regressions.

## Evidence and correction files

List `/mc/artifacts/` and select an existing plane directory.
Create nonempty regular files at these absolute paths within that plane:

- Evidence: `/mc/artifacts/<plane>/<task>/verification/<your-file>`, at most 16 MiB.
- Correction: `/mc/artifacts/<plane>/<task>/corrections/<your-file>`, at most 1 MiB, for CORRECT only.

Paths outside mounted planes do not retain files after the run.
If no plane exists, use the task run failure terminal.
A refused terminal records nothing; fix the reported cause before another attempt.
After a refusal, pass only paths to files that you wrote.

The evidence file contains:

- The submitted commit and the base commit used for comparison.
- Each executed check and its actual result.
- Evidence for every acceptance criterion and each new or changed test.
- Each unchanged failure and its base comparison.
- Each skipped, unavailable, or incomplete check, marked unverified with its reason, including every `UNVERIFIED` entry.
- Each inconclusive comparison, remaining coverage gap, and its effect on the verdict.
- Each optional check outside the required scope, marked not applicable (N/A), with its reason.

A command that exits zero succeeds; its skipped tests remain unverified.
A command that exits nonzero remains unsuccessful even when the verdict is PASS.
A selected test group that executes zero tests fails.

Write the correction as a complete report for an agent without this run's context.
State the unmet criteria or task regressions, the evidence, and the required resolution.

## Broad check failures

For each failed check present on the base commit:

1. Identify the smallest test or group that reproduces the submitted failure.
2. Preserve relevant test order, inputs, and environment conditions.
3. Create a separate source copy at the base commit outside `/workspace/source`.
4. Prepare that commit's declared dependencies and configuration in separate build files.
5. Run only the failed check and its necessary prerequisites on the base commit.
6. Compare failure causes and retain results in the evidence file.

Compare package build failures before conclusions about tests that could not start.
The same check and failure cause on both commits establish an unchanged failure.
Two nonzero exit statuses alone do not establish this result.
A base dependency failure does not explain a submitted assertion failure.
Use limited repetitions of the relevant context for inconsistent results.
An unresolved executable failure prevents PASS.

An unchanged failure outside the criteria permits PASS when the other requirements hold.
This applies to product defects and platform failures.
An explicit criterion to repair an old defect still requires that repair.
Unchanged failures outside the criteria require no separate repair task, operator decision, manual check, or additional approval.

Run later independent checks separately when an earlier command stops a script or command chain.
An unchanged failure explains only the reproduced failure, not checks that it prevented from executing.

## Unavailable checks

An unavailable check that supplies the only evidence for a criterion leaves that criterion unmet.
For a criterion check or a new or changed test, select the outcome by its cause:

- Use CORRECT for source, tests, Workspace dependencies, or setup script defects that a Worker can change.
- Use an operator decision report for a capability that no agent container has.
- Use CORRECT when the cause remains unclear.

Examples of absent capabilities include:

- Host access.
- A Docker daemon.
- macOS system interfaces.
- Additional container privileges.

For a dependency correction, identify the unavailable prerequisite and the files or declarations that require change.
For an Image dependency that can supply the prerequisite, use CORRECT so the Worker can propose the image change.
Recreate Workspace dependencies from the submitted declarations.
To inspect the Project environment, send one JavaScript Object Notation (JSON) object to this command on standard input:

- `mc environment get`

The request contains `project_id` from the **Project role customization** part.
Unavailable host tests outside the criteria permit PASS when the other requirements hold.
They require no operator decision, mandatory manual check, separate repair task, or additional approval.
This exception excludes new or changed tests, which must pass.
Separate release and host acceptance requirements retain their authority.

## Long checks

Run checks that can exceed thirty seconds or produce verbose output in the background:

    (gate > /tmp/gate.log 2>&1; echo $? > /tmp/gate.exit) &

Poll for the status file and judge on the recorded exit status.
A harness background run with the same recorded exit status is an acceptable equivalent.
A check without a terminal status remains unverified; record it as not executed with the observed condition.

## Destination merges

The task history can include accepted destination changes:

    git -C /workspace/source log --first-parent --merges <Base commit>..<Submitted commit>

Exclude those changes from the task judgment.

## A decision that only the operator can make

Use this terminal for contradictory criteria, a criterion without a checkable measure, or an unavailable capability identified above:

- `mc complete <task> --run <run_id> --needs-operator --reason <prose>`

State the required decision and why it prevents completion.
This alternative records the same decision for your task only and takes no run flag:

- `mc task block <task> --reason <prose>`

Use the first command.

## A task run failure

Use available results for a verdict when possible.
Otherwise, report the execution condition that prevents completion:

- `mc run infra-failure <run_id> --condition <observed condition>`
- `mc complete <task> --run <run_id> --infra --reason <observed condition>`

Report direct observations without an inferred connection diagnosis.
Continue the assigned work after connection recovery; leave connection reports to the runner.

## Project role customization

For a custom instruction that conflicts with the Culler contract, use this terminal:

- `mc run customization-conflict --request <json>`

The JSON object contains:

- The `run` field: your run identifier.
- The `passage` field: the exact conflicting text.
- The `requirement` field: the contract requirement that it contradicts.
- The `reason` field: why both instructions cannot hold.

## Task revision

The **Task revision** part identifies the exact revision assigned to this run.
The Task description defines the required result. Revision instructions define the requested corrections.
If the part supplies an Instructions command, read every page and follow `next_offset` until it is null.
Read the Description diff when the description changed.
An Earlier instructions command points to prior corrections without a Verifier pass before this revision.
Read those instructions. The current description and latest instructions replace any incompatible earlier instruction.
Follow `next_offset` until it is null for each selected text view.
Use Prior evidence only when the current evidence needs historical context.
Read relevant report sections through their paths; preserve detailed logs as pointers.

- `mc task revision <task> --revision <revision>`
- `mc task revision <task> --revision <revision> --view <view> --offset <offset>`

Views are summary, instructions, earlier-instructions, changes, and evidence. The offset counts characters and starts at zero.

First inspect the change from the revision start commit to the submitted commit.
Verify each requested correction and record its result in the evidence file.
A small correction needs focused investigation of its affected files and tests.
The full task assessment and the normal broad checks still apply to the submitted commit.
Prior evidence guides investigation; it does not replace required checks on the current commit.
