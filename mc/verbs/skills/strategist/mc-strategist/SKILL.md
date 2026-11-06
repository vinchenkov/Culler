---
name: mc-strategist
description: The complete mc command surface for the Strategist. Load this skill before each mc call. The role can call only commands in this skill.
---

# mc — Strategist

The agent profile sets the model and effort for this run.

Every write carries `--run <run_id>`. `<run_id>` is the `Run identifier` fact of the **Run** part of the run brief. Copy the value without change. `mc` rejects a call whose run identifier does not match the live run. `mc` applies no part of a rejected call. A terminal action is an `mc` command that ends the run.

## Terminal action

`--batch` accepts only `-`. `mc` reads the payload from stdin as strict JSON. An unknown field rejects the whole batch. Text after the closing brace also rejects the whole batch. This includes a second JSON value and any trailing text. Send the object alone.

If you execute work inline instead of through subagents, state the reason in your response before the terminal action. Do not put the reason in a proposal. Do not append the reason to the payload. Culler keeps your response in the record of the run.

- `mc strategist propose --run <run_id> --batch -`

Payload shape: an object with one field, `proposals`. The `proposals` field is an array of proposal objects. Each proposal has these fields:

- The `title` field is required.
- The `project` field is optional.
- The `scope` field is optional. Its only value is `task`. An omitted `scope` means `task`.
- The `description` field is optional.
- The `priority` field is optional.

`mc` validates every proposal inside one transaction. One violation of the constraints below rejects the entire batch. A rejected batch writes nothing.

`mc` files every proposal into the pool of this run's Project. That Project id is the `Identifier` fact of the **Project** part of the run brief. An omitted `project` field defaults to that id. A present `project` field must equal that id. Any other id refuses the whole batch.

`title` is one line. It must be valid UTF-8 with no control characters, such as a newline or a tab. It must have a maximum of 4096 bytes. `description` has no such check. Put prose in `description`.

`priority` is an integer from -1 to 3. The value -1 is expedited. The values 0 to 3 are P0 to P3. An omitted `priority` means 2. `mc` refuses any other value.

Propose two tasks in one batch. Submit fewer only when fewer valid tasks satisfy the Project vision.
`mc` rejects a batch with more than two tasks.

## Acceptance criteria

Each criterion describes task behavior and identifies evidence available in an agent container.
New or changed tests must pass on the submitted commit.
Whole-suite success requirements, such as `./mc/check.sh passes`, are invalid criteria.
An available suite command does not establish the capabilities of its tests.

Criteria exclude these capabilities:

- Access to the operator's host.
- A Docker daemon.
- macOS system interfaces.
- Additional container privileges.
- A hosted continuous integration service.

Broad regression checks belong to the Verifier's duties for every task.
An unchanged Project test failure alone permits a proposal that satisfies these rules.
The Arbiter rejects invalid criteria through the `invalid_criteria` category.

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
Do not propose equivalent work from either historical part, even with a different title.
Use each cancellation reason as context for the operator's preference.
The **Active tasks** part retains task numbers, titles, and stages.

The **Active tasks** part of the run brief holds each task of this Project that `mc task list` can show. A sweep with `mc task list` thus adds no row of this Project. Use `mc task get <id>` when you need the description of one row. Your subagents read the committed files of this Project at the `Repository copy` fact of the **Project** part. Your subagents read no tree of any other Project.

- `mc task get <id>`
- `mc task list`
- `mc packet list`
- `mc activity list --kind <kind> --limit <n>`
- `mc project list`

## Out of scope

These operations belong to the operator and to Culler:

- create a task;
- start a run;
- decide a review packet.

Only the Arbiter promotes a proposal. Your run belongs to the Project, with no task subject.

## Execution problems and operator decisions

- `mc complete --run <run_id> --infra --reason <observed problem>`
- `mc complete --run <run_id> --needs-operator --reason <question>`

Dispatch, launch, Strategist proposal creation, and Arbiter proposal review share one Project infrastructure allowance of three counted failures.
The third counted failure stops each new agent run with the Project lease in the Project.
A successful proposal batch restores that Project allowance. An operator decision report preserves its count.
The operator resolves the shared Project decision before proposal work resumes.
An agent diagnosis supplies an unknown cause. Culler requires direct runner or resident evidence for shared connection conditions.

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
