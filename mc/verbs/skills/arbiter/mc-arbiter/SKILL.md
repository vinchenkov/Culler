---
name: mc-arbiter
description: Arbiter commands, batch constraints, and procedures for the cases named in the directive.
---

# mc — Arbiter

`mc --help` describes operator commands; it grants no additional role authority.
Use this role skill for assigned run commands and terminal submissions.

## Terminal action

- `mc arbiter decide --run <run_id> --batch -`

Supply one JavaScript Object Notation (JSON) object on standard input, with no data after it.
Its only field, `verdicts`, contains an array of objects with these fields:

- The `task` field: the proposal's integer `id`, required.
- The `decision` field: `promote` or `reject`, required.
- The `reason` field: a concrete reason, required for `reject`.
- The `duplicate_of` field: an optional integer task identifier, permitted only for `reject`.
- The `rejection_category` field: the optional value `invalid_criteria`, permitted only for `reject`.

Use no other fields.
The fields `duplicate_of` and `rejection_category` are mutually exclusive.
A refused batch applies no changes; correct the refusal before another attempt.

## The all-reject batch

The Arbiter must choose one proposal to proceed.

It can reject both only if each proposal has at least one of these defects:

It duplicates active and recent (see Historical work) tasks in the same Project.
It requires checks that Culler’s rules do not allow.
The two proposals can have different defects. Explain the defect in each proposal. For a duplicate, identify the existing task.

Each rejection in an all-reject batch requires one of these exceptions:

- A valid `duplicate_of` reference.
- A `rejection_category` value of `invalid_criteria` and a reason.

The batch can combine exceptions across proposals.

Each `duplicate_of` reference must name an active or historical task of this Project.
The task list with `--all` identifies merged tasks by `archived` and `decision` equal to `approved`.
State the overlap in `reason`; `mc` checks the reference, not your overlap judgment.

## Historical work

Compare proposals with **Rejected work** and **Cancelled work**.
Treat equivalent work as a reason for rejection under the batch rules.
Use each cancellation reason as context for the operator's preference.
Use the permitted reads to inspect older work.

## A run that cannot give a result

Judge the pool when possible.
Otherwise, select the applicable terminal below.

### A decision that only the operator can make

- `mc complete --run <run_id> --needs-operator --reason <question>`
- `mc complete --run <run_id> --needs-operator --reason <question> --affected <id,id>`

State the required decision and why it prevents a verdict.
The first form includes every assigned proposal; `--affected` selects a subset of the assignment.
The report preserves proposals until the operator resolves the shared question, and proposal work stops meanwhile.

### An execution problem

- `mc complete --run <run_id> --infra --reason <observed problem>`
- `mc run infra-failure <run_id> --condition <observed problem>`

Report direct observations without an inferred connection diagnosis.
Continue the assigned work after connection recovery; leave connection reports to the runner.

## Reads

- `mc task get <id>`
- `mc task list`
- `mc task list --all`
- `mc activity list --kind <kind> --limit <n>`
- `mc project list`

The task list omits descriptions.
Use the task read for scope absent from **Proposed pool** and **Active tasks**.
The task read returns the `strategic_rationale` of a task that the Strategist proposed.

## Project role customization

Submit this terminal to end the run with a customization conflict:

- `mc run customization-conflict --request <json>`

The JSON object contains these fields:

- The `run` field: your run identifier.
- The `revision` field: the customization revision from the brief.
- The `passage` field: the exact conflicting text.
- The `requirement` field: the Culler contract requirement that it contradicts.
- The `reason` field: why both instructions cannot hold.
