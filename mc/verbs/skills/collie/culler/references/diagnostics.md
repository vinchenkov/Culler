# Diagnostics

Read the referenced current task, run, environment, or system record before you explain a diagnostic snapshot.
Use system diagnostics, host health, and masked logs when those records explain the condition.
Read the relevant trace when the question concerns an agent decision.
State each unavailable source and each inference.
Apply the authorization rule before a recovery action.

## Decision Pending


The dashboard shows a blocked task as Decision Pending.
`mc task get <id>` states the condition type in `blocked_kind` and the report or the reason in `blocked_reason`.
Resolve decision is the dashboard control that resolves Decision Pending on a task.
For the type `report`, Resolve decision opens a form that saves a requirements revision.
For each other type that permits it, Resolve decision takes a reason only.
The types `allowance` and `proposal` have no Resolve decision.
The `unblock` action and `mc task unblock` give the resolution with a reason only.

Each condition type has its own resolutions:

- The type `report` is an operator decision report of the Worker, the Verifier, or the Packager.
  Resolve it with the `revise` action, or cancel the task.
  `mc` refuses the `unblock` action and the `block` action for this type.
- The type `customization` is a customization conflict.
  The operator edits the role customization first. Then send the `unblock` action with a reason.
- The type `allowance` is the correction allowance limit after the third rejected correction.
  Resolve it with the `allow-corrections` action or the `revise` action, or cancel the task.
- The type `confinement` is a confinement refusal at the launch of a run.
  The operator changes the launch configuration first. Then send the `unblock` action with a reason.
- The type `checkout` is an operator checkout condition at the merge.
  The operator repairs the operator checkout first. Then send the `unblock` action with a reason.
- The type `operator` is an operator block.
  Resolve it with the `unblock` action and a reason.
- The type `proposal` is a proposal decision of the Strategist or the Arbiter.
  The operator resolves the shared proposal decision through its Project control.

Use the `unblock` action only for these types:

- the type `customization`
- the type `confinement`
- the type `checkout`
- the type `operator`

A resolution with a reason keeps the description of the task.
An applied requirements revision ends the types `report` and `allowance`.
It keeps each other type.

A correction allowance limit can occur on a task that already has another type.
The task then keeps that type, and `delivery.allowance_condition` in the task response has a value.
Resolve the limit first with the `allow-corrections` action or the `revise` action.
`mc` refuses the `unblock` action until the limit ends.

## Wiki updates

A wiki update has no task and holds no Project lease.
It can run at the same time as a task run.
Culler merges an accepted wiki commit without operator review.

## Task failures

The dashboard shows `blocked` as Decision Pending and `stalled` as Retries Exhausted.
A task with Retries Exhausted consumed its three task run failure attempts.
The task retains its stage and accepted work.
Three distinct blocked or stalled tasks stop new task runs and ordinary task merges within their Project.
A wiki update still runs.
This threshold preserves the explicit Project pause state.
A correction that requires an operator decision holds its Project immediately.
An explicit resume leaves an unresolved correction intact.
Other Projects and Collie remain available.
`mc task resume <id> --reason <prose>` restores three task run failure attempts.
A task resume preserves an independent Decision Pending condition.
`mc task unblock <id> --reason <prose>` resolves Decision Pending of the types that permit it and preserves the task run failure count.
Proposed tasks are read-only for operator task controls.
The operator can resolve a shared proposal decision through its Project control.
Dispatch, launch, Strategist proposal creation, and Arbiter proposal review share one Project infrastructure allowance of three failures.
A confirmed connection condition uses its connection controls and consumes zero pipeline failure units.

The runner owns `mc run model-response` and `mc collie model-response`.
These commands record complete model responses for the current attempt.
Do not call these runner commands.
Continue the assigned work after connection recovery.
