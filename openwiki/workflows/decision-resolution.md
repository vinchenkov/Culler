---
type: workflows
title: Decision Pending and requirements revisions
description: How operator decisions become task requirements while Culler preserves accepted work and the original task base.
tags: [tasks, decisions, requirements, verification]
sources:
  - id: openwiki-source-ddff79bc537156c26cb2035f
    resource: repo://dashboard/smoke/board-writes.smoke.test.ts
  - id: openwiki-source-2b9db73e8eaa164d3fba79a2
    resource: repo://dashboard/src/ui/app.js
  - id: openwiki-source-0c84ae704d5461f889682650
    resource: repo://dashboard/src/ui/board.js
  - id: openwiki-source-41b10777200e9da21f4768dc
    resource: repo://dashboard/src/ui/boardwrites.js
  - id: openwiki-source-85462c8d8a05dd5cb7456318
    resource: repo://dashboard/src/ui/boardwrites.test.ts
  - id: openwiki-source-8f16f07389eccb57a739027f
    resource: repo://dashboard/src/ui/systemhealth.js
  - id: openwiki-source-833cfcb1ce94b8d49fc966fd
    resource: repo://mc/domain/requirements.go
  - id: openwiki-source-31daf0508c2344b34e93eec3
    resource: repo://mc/verbs/board.go
  - id: openwiki-source-7f547d2c5de5589b3c27e6a4
    resource: repo://mc/verbs/brief.go
  - id: openwiki-source-301f4dcdb036a6a700293c7a
    resource: repo://mc/verbs/decisionresolution_test.go
  - id: openwiki-source-47e2ca52aab2f12bfb519d6d
    resource: repo://mc/verbs/deliverycomplete.go
  - id: openwiki-source-db55b76d7f0573ce8b068ebf
    resource: repo://mc/verbs/directives/verifier.md
  - id: openwiki-source-717b9c51f4b41e8d918f5c19
    resource: repo://mc/verbs/skills/arbiter/mc-arbiter/SKILL.md
generated: { by: "culler-scribe", at: "2026-11-03T04:34:51.748Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-07T01:30:07.289Z
---

# Decision Pending and requirements revisions

An operator decision report states that a task needs a decision only the operator can make.
The Worker, Verifier, or Packager records that report through its terminal action.
`mc/verbs/complete.go` records Decision Pending with `blocked_kind = 'report'`.
The task retains its stage until a resolution changes it.

The operator resolves the report through the dashboard or Collie.
Both use the requirements revision operation in `mc/verbs/board.go`.
The description holds every requirement of the task.
The reason explains why the operator changed those requirements.
Task cancellation is the other resolution.

## Select the resolution by condition type

The spine stores the condition type in `tasks.blocked_kind`.
Task reads, packet reads, and system conditions expose that type.
The dashboard and Collie use it to select the applicable control.

| Type | Cause | Resolution |
| --- | --- | --- |
| `report` | A Worker, Verifier, or Packager needs an operator decision. | Revised requirements and a reason, or Cancel task. |
| `customization` | Custom instructions conflict with the role contract. | Edit the role customization, then Resolve decision with a reason. |
| `allowance` | The third Verifier correction fails. | Authorize three more corrections, revise requirements, or cancel the task. |
| `confinement` | Launch checks refuse the assigned configuration. | Repair the launch configuration, then Resolve decision with a reason. |
| `checkout` | The operator checkout prevents a merge. | Repair the checkout, then Resolve decision with a reason. |
| `operator` | The operator blocks the task. | Resolve decision with a reason. |
| `proposal` | The Strategist or Arbiter needs a proposal decision. | Resolve the shared Project proposal decision. |

The task dialog shows Resolve decision for a blocked task. It offers no control to set Decision Pending.
For a packaged task, the dialog omits Edit title and priority. A revision can still return packaged work to Seeded when its other conditions permit it.

The prerequisites in this table come from the role instructions and dashboard procedures.
A reason-only resolution preserves the description.
The backend refuses that resolution for `report`.
It also refuses an operator block that would replace a report condition.
These checks prevent a reason from becoming an undocumented requirement.

An allowance limit can coexist with another condition type.
The task retains that type while `delivery.allowance_condition` records the limit.
The allowance must end before a reason-only unblock can resolve the other condition.
A proposal resolution clears only the proposal blocks that it owns.

## The Board transaction

`resolveAction` in `dashboard/src/ui/boardwrites.js` opens the report resolution form for `report`.
The form shows the agent report, editable description, reason, and Cancel task alternative.
Save requirements requires a changed description and a nonempty reason.
The browser sends the existing `revise` action with an operation identifier and expected record version.

`BoardWrite` validates the request and uses one transaction for the revision and its Board history.
`RequestRequirementsRevision` in `mc/domain/requirements.go` compares the descriptions after it trims outer whitespace.
An unchanged description fails without a revision record.
A stale record version or an existing pending revision also prevents a new revision.
The revision request releases the old review slot.

Application waits while the task holds an active run, an active repository operation other than cleanup, or an uncaptured submission.
Decision Pending remains during that wait.
`ApplyPendingTaskRevisions` retries application before dispatch selects more task work.
A final decision cancels a revision that remains pending.

When application can proceed, the transaction:

1. Preserves the prior requirements and delivery evidence.
2. Replaces the description and advances its requirements version.
3. Returns the task to Seeded and clears obsolete review and verdict references.
4. Ends a report condition and any Verifier correction allowance limit.
5. Marks the revision applied.

The revision preserves independent customization, confinement, checkout, and operator conditions.
It preserves an open merge repair and its conflict context.
Retries Exhausted remains an independent condition.
The task response exposes `pending_revision` and `requirements_history` for progress and history.

## Retained work and verification

`base_commit` identifies the first Worker start commit.
The schema prevents later changes to that value.
A requirements revision preserves the checkpoint and retains the last submitted commit as the next input fallback.
The next Worker starts from the first available source:

1. The checkpoint commit.
2. The last submitted commit.
3. The original task base.

`mc/verbs/deliverycomplete.go` requires a clean tree and the exact submitted `HEAD`.
The submission must preserve the assigned input as an ancestor and differ from the task base.
A Worker can therefore submit its unchanged start commit when retained work satisfies the revised description.
A checkpoint equal to the base also fails.

The Verifier judges the full change from the original base to the submitted commit against the revised description.
The requirements revision does not itself establish a pass.
A fresh verdict and review packet establish the result for the effective requirements.
See [Git delivery](git-delivery.md) and [task pipeline](task-pipeline.md).

## Role input and unavailable checks

The run brief uses `mc.spawn-brief.v23`.
Each Worker run on the revised requirements receives a Requirements revision part with the reason as context.
The Verifier and Packager receive the revised description without that reason.
Briefs omit Verifier corrections and verdicts from earlier requirements versions.
A packet return instead supplies a Revision request that the Worker must meet.

The Verifier directive requires CORRECT for a failed criterion or a criterion check blocked by a cause the Worker can change.
Those causes include source, tests, Workspace dependencies, and the setup script.
A criterion check that requires host access, a Docker daemon, macOS interfaces, or additional container privileges requires an operator decision report.
An unclear cause for an unavailable criterion check requires CORRECT.
An unavailable required check remains unverified until the operator changes the criteria or the required evidence exists.
Unchanged failures and unavailable host tests outside the criteria alone require no operator decision or additional approval.
The evidence file and Review Packet retain those observations.
Task regressions and failed new or changed tests require CORRECT.
See [agent input](../concepts/agent-input.md) and [the test guide](../tests/checks-and-test-lanes.md).

## Evidence and tests

The source and tests establish the behavior on this page.

| Source | Behavior checked |
| --- | --- |
| `mc/verbs/decisionresolution_test.go` | Report resolution, unchanged-description refusal, delayed application, retained commits, independent conditions, Collie parity, and merge repair preservation. |
| `mc/verbs/brief_resolution_test.go` | Worker reason context, later runs, original base commits, and reason omission for Verifier and Packager. |
| `mc/verbs/deliveryonecommit_test.go` | Refusal of submissions and checkpoints equal to the task base. |
| `mc/substrate/substrate_test.go` | Required condition types and immutable base commits. |
| `dashboard/src/ui/boardwrites.test.ts` | Form selection, save rules, payloads, and stale condition handling. |
| `dashboard/smoke/board-writes.smoke.test.ts` | Report resolution and reason-only operator block resolution against a scratch spine. |

This page reports source inspection. It does not report fresh execution of these tests.


## Shared proposal decisions

A shared proposal decision appears once in the dashboard issue count.
The Board's **Blocked tasks** count still includes each affected blocked task.
Open **View details** from the notice, or select the issue in **Review issues**.
The issue details link to the affected proposals and show the complete question.
A proposal task dialog offers **View shared proposal decision** when system status contains that decision.
Select **Resolve proposal decision**, enter the resolution reason, and submit **Resolve decision**.
The resolution dialog also shows the complete question.

`app.js` sends the Project identifier, decision identifier, and reason to `/api/system/resolve-proposals`.
`ResolveProposalDecision` in `mc/domain/proposals.go` records the resolution and clears only the proposal blocks that the decision owns.
The current block kind and reason must still match the decision.
A pre-existing block remains independent even when its text matches the question.
Separate task blocks, exhausted retries, and allowance conditions remain separate dashboard issues.
The browser uses explicit ownership from `mc/verbs/systemstatus.go` to determine which task conditions the shared issue represents.
See [shared proposal issues](../architecture/dashboard.md#shared-proposal-issues) for that boundary and its tests.

`dashboard/smoke/proposal-review.smoke.test.ts` checks resolution with two affected proposals and one independent blocked task.
It asserts that the shared issue disappears while the independent task remains blocked.
It also checks the complete question, task links, focus after status refresh, and narrow dialog layout.
These are source assertions, not fresh browser test results from this wiki update.

## Invalid proposal criteria

The Arbiter can reject invalid criteria without a shared operator decision when each rejection satisfies the batch exception rules.
`invalid_criteria` covers whole-suite success requirements, behavior outside the task scope, and checks that need unavailable capabilities.
The rejection reason identifies the criterion and its violated rule.
`mc/verbs/roles.go` validates the declared category; the Arbiter judges whether the criteria qualify.
The command archives accepted rejections and retains their reasons.
See [the task pipeline](task-pipeline.md) for empty-queue exceptions and batch validation.
