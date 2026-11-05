---
name: mc-arbiter
description: The complete and only mc verb surface for the Arbiter's pool pass over proposed tasks; load before any mc call and before submitting the terminal — mc ships no --help, and a verb absent from this skill does not exist for this role.
---

# mc — Arbiter (pool pass)

The agent profile sets the model and effort for this run.

`<run_id>` is the `Run identifier` fact of the **Run** part.
Use that value in each terminal action.
The **Project** and **Proposed pool** parts define the assignment.
The Strategist normally supplies two task proposals.
This proposal run has no task subject.
`mc` refuses a terminal action from another run and applies zero changes.
A terminal action ends the run.

## Terminal action

`--batch` accepts only `-`. `mc` reads the payload from stdin as strict JSON.

- `mc arbiter decide --run <run_id> --batch -`

The payload is an object with one field, `verdicts`. The field `verdicts` is an array of verdict objects. Each verdict has these fields:

- The field `task` is required. Its value is the `id` of one row of the **Proposed pool** part.
- The field `decision` is required. Its value is one of `promote|reject`.
- The field `reason` is required for `reject`.
- The field `duplicate_of` is optional. It is permitted on a `reject` only. Its value is an integer task id. The section "The all-reject batch" defines its use.
- The field `rejection_category` is optional. Its only value is `invalid_criteria`, and it requires a `reject` decision.

The fields `rejection_category` and `duplicate_of` are mutually exclusive.
Each rejection reason must contain readable text.
For `invalid_criteria`, the reason identifies the invalid criterion and the rule that it violates.
The command checks the declaration and requires a nonblank reason.
You judge whether the criterion violates the rules.
The command does not infer that judgment from prose.

The payload permits no other key at either level. An unknown field, for example `rank`, `leverage`, or `notes`, causes `mc` to refuse the whole batch. Data after the object also causes `mc` to refuse the whole batch. Put the reasons for your judgment in the `reason` field of each verdict.

The batch must cover the **Proposed pool** part exactly. `mc` refuses a batch that omits a row of the part as a pool mismatch. `mc` refuses a batch that names a task outside the part as a pool mismatch. `mc` applies no part of a refused batch.

## The all-reject batch

This case occurs when you reject every proposal.

`mc` accepts the batch when the ready queue is not empty. `mc` counts a task as ready when all these conditions are true:

- the task is outside your pool;
- its status is `proposed`, `seeded`, `worked`, or `verified`;
- the task is not blocked and not stalled.

The **Active tasks** part does not show the stalled state. `mc task list` shows it in the `stalled` field.

When the ready queue is empty, each rejection requires one of these exceptions:

- A valid `duplicate_of` reference.
- A `rejection_category` value of `invalid_criteria` and a reason.

The batch can combine these exceptions across proposals.
Each rejection uses only one exception.
A low rank or general preference does not qualify.

Each `duplicate_of` reference names prior work that the proposal duplicates.
The reference must name a row of the **Active tasks** part or a merged task of this Project.
`mc task list --all` shows merged tasks with `archived` set and `decision` equal to `approved`.
Do not cite a member of your own pool.
When two valid pool proposals overlap, promote the superset.

`mc` checks only that the cited row is existing work of this Project. The overlap itself is your judgment. State the overlap in `reason`.

The `invalid_criteria` category applies when acceptance criteria violate the proposal rules in the role directive.
Examples include these invalid criteria:

- A whole-suite success requirement, such as `./mc/check.sh passes`.
- Behavior outside the task's scope.
- A required check that needs an unavailable capability.

An available suite command does not establish that its tests can run in the container.
Required capabilities exclude these resources:

- Access to the operator's host.
- A Docker daemon.
- macOS system interfaces.
- Additional container privileges.
- A hosted continuous integration service.

New or changed tests must pass on the submitted commit.
Broad regression checks belong to the Verifier's duties for every task.
An unchanged Project test failure alone permits a proposal that satisfies these rules.

An accepted rejection archives the proposal and retains its reason in `task.rejected` activity.
A later Strategist run proposes new work under the normal dispatch rules.
Invalid criteria alone require no operator decision when each rejection qualifies for an exception.
Other decisions that only the operator can make use the procedure below.

## A run that cannot give a result

Two events end a run without a result. The event selects the command. Each command in this section ends your run, so submit exactly one. No command here substitutes for a judgment of the pool.

### A decision that only the operator can make

The report records one shared Project question. Each affected proposal receives Decision Pending and retains Proposed.

- `mc complete --run <run_id> --needs-operator --reason <question>`
- `mc complete --run <run_id> --needs-operator --reason <question> --affected <id,id>`

The first command includes every assigned proposal. The second command identifies the affected subset of your assignment.
The operator resolves the shared decision before proposal work resumes. The report consumes zero failure units.

### An execution problem

- `mc complete --run <run_id> --infra --reason <observed problem>`
- `mc run infra-failure <run_id> --condition <observed problem>`

Your proposal run belongs to the Project, with no task subject.
Dispatch, launch, Strategist proposal creation, and Arbiter proposal review share one Project infrastructure allowance of three counted failures.
The third counted failure stops each new agent run with the Project lease in the Project.
A successful terminal result restores that Project allowance. An operator decision report preserves its count.
An agent diagnosis supplies an unknown cause. Culler requires direct runner or resident evidence for shared connection conditions.

## Reads

The **Rejected work** and **Cancelled work** parts contain at most 20 historical tasks total from this Project.
Culler selects up to 10 cancelled tasks first.
Rejected tasks fill the unused places.
Each part lists the newest decisions first, with larger task numbers first for equal decision times.
An empty part is absent.
Older tasks remain available through the permitted read commands.

Each historical entry contains these fields:

- The task number.
- The recorded title.
- The decision.

A cancelled entry also contains a recorded reason excerpt when available, with at most 160 Unicode characters.
Historical entries exclude descriptions.
Use `mc task get <id>` when a historical title leaves the scope unclear.
The **Active tasks** and **Proposed pool** parts retain full descriptions for comparison of scope and success criteria.

Compare each proposal with both historical parts.
Treat equivalent work as a reason to reject the proposal under the permitted batch rules, even with a different title.
Use each cancellation reason as context for the operator's preference.
Historical tasks do not qualify as existing work for `duplicate_of`.
Use the operator decision procedure when the all-reject rules prevent a permitted verdict.

- `mc task get <id>`
- `mc task list`
- `mc task list --all`
- `mc packet list`
- `mc activity list --kind <kind> --limit <n>`
- `mc project list`

`mc task list` returns no `description`. It therefore cannot show you what a row touches. You find overlap in what a row touches. The **Active tasks** part of the run brief holds the full description of each active task. Judge overlap against that part. Use `mc task get <id>` only for a row that neither the **Proposed pool** part nor the **Active tasks** part names.

## Out of scope

These operations belong to the operator and to Culler:

- add a task;
- unblock a task;
- interrupt a task;
- start a run.

A search for a command that this skill does not list wastes the run.

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
