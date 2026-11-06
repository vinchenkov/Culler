---
name: mc-strategist
description: Read before Strategist commands, dependency use, or reports of execution problems, operator decisions, and customization conflicts.
---

# mc — Strategist

## Proposal batch

Submit two proposals together, or fewer when fewer valid tasks satisfy the Project vision.
Use an empty `proposals` array when no valid task remains.

- `mc strategist propose --run <run_id> --batch -`

Send one JavaScript Object Notation (JSON) object on standard input, with no extra fields or trailing text.
Its only field is `proposals`, an array of objects with these fields:

| Field | Constraint |
| --- | --- |
| `title` | Required; one line of valid UTF-8, without control characters, and at most 4096 bytes. |
| `description` | Optional in the command schema; contains the proposal prose. |
| `project` | Omit it to use the **Project** Identifier, or supply that exact identifier. |
| `scope` | Omit it or use `task`. |
| `priority` | Integer from -1 to 3; -1 is expedited, 0–3 mean P0–P3, and omission means 2. |

A successful batch creates all proposals and ends the run in one transaction.
A rejected batch writes nothing; fix the rejection before another attempt.

## Reads

Use the task read when an active or historical title leaves the scope unclear.
The task list adds no Project rows to **Active tasks**; older historical tasks remain available through these reads.

- `mc task get <id>`
- `mc task list`
- `mc packet list`
- `mc activity list --kind <kind> --limit <n>`
- `mc project list`

## Research dependencies

This run omits automatic dependency preparation.
Check retained dependencies against the current declarations and the selected image before use.
Apply the project-environment-setup skill for dependency preparation within this role's authority.

## Execution problems and operator decisions

Complete proposal work after a failure when available results permit it.
Continue the assigned work after connection recovery; leave connection reports to the runner.
When an execution condition prevents completion, report the observed problem without an inferred connection diagnosis:

- `mc complete --run <run_id> --infra --reason <observed problem>`

When completion requires an operator decision, state the question and why it prevents completion:

- `mc complete --run <run_id> --needs-operator --reason <question>`

## Project role customization

For a contract conflict, submit this terminal:

- `mc run customization-conflict --request <json>`

The JSON object contains:

- The `run` field: the run identifier.
- The `revision` field: the customization revision from the brief.
- The `passage` field: the exact conflicting text from that revision.
- The `requirement` field: the contract requirement that the text contradicts.
- The `reason` field: why both instructions cannot hold.
