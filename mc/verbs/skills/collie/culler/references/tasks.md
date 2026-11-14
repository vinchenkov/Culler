# Tasks

Read the current Project and task records before a change.
Apply [the task description standard](task-description.md) to every complete description.
Use command help for the exact request fields and supported Board actions.

Before task creation, present the Project, title, priority, and complete description.
Before a task revision, present the title, complete current description, and any revision instructions.
The operator can change or remove any part of the draft.
Identify any conflict with the task description standard before confirmation.
Submit the exact text that the operator confirms.

A task description holds every task requirement.
Revision instructions identify corrections to that result. New requirements belong in the description.
Use the Board revise action for both Board and Review revisions.
It accepts title, description, and optional instructions. An edit or instructions must change the task request.
Include packet_id and version_id when the revision concerns a reviewed packet.
For a small correction, preserve the description and state the exact correction in instructions.
Reports and prior work remain available through revision reads and file pointers.
A revision waits for an affected active run or repository operation to finish.
Inspect the pending revision and requirements history to report its progress.
Use the current task version for an edit.
A stale version requires a fresh read and reconciliation that preserves the operator's draft.

Task creation through Board records an operator request at `seeded`.
Agent proposals start at `proposed` and require Arbiter acceptance.
Task priority selects later work and does not interrupt an active run.
Review capacity, approved merge work, and required repository operations retain their precedence.

Use task cancellation when the operator abandons a task.
Use packet cancellation when the operator decides the reviewed packet.
A task interrupt requires the live subject of the active Project lease.
It does not apply to a queued or completed task.
An approved task can cancel before a merge authorization exists.
An unresolved authorization requires receipt recovery before cancellation.

Read [diagnostics](diagnostics.md) before a resume or block resolution.
A task resume restores the task failure allowance and preserves independent Decision Pending conditions.
A task revision resolves only the condition types that its contract permits.
A third rejected Verifier correction requires an operator grant; a Verifier pass restores the allowance.
A revision with an unchanged description preserves that allowance.
The correction grant requires the current unresolved limit, a reason, and the current record version.
