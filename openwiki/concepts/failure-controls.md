---
type: concepts
title: Failure controls and retry allowances
description: How Culler classifies failed agent runs, which allowance each failure consumes, how retries are timed and restored, and how operator decisions and connection conditions stay separate.
tags: [failure, retries, allowance, blocked, stalled, connections]
sources:
  - id: openwiki-source-2b9db73e8eaa164d3fba79a2
    resource: repo://dashboard/src/ui/app.js
  - id: openwiki-source-b578a24182c530b5d8630c21
    resource: repo://mc/dispatch/dispatch.go
  - id: openwiki-source-636e77a0ecbade42aa7a7799
    resource: repo://mc/domain/allowances.go
  - id: openwiki-source-1de6f18d791c6e34600c5cbc
    resource: repo://mc/domain/diagnostics.go
  - id: openwiki-source-bf785650a2dcf568641c9869
    resource: repo://mc/domain/health.go
  - id: openwiki-source-f4f4b05a061bf55b5fd93485
    resource: repo://mc/domain/infrastructure.go
  - id: openwiki-source-833cfcb1ce94b8d49fc966fd
    resource: repo://mc/domain/requirements.go
  - id: openwiki-source-e876d4f934fe1b8e2de3721c
    resource: repo://mc/substrate/schema.sql
  - id: openwiki-source-301f4dcdb036a6a700293c7a
    resource: repo://mc/verbs/decisionresolution_test.go
  - id: openwiki-source-419bfabff8715c9aa9b81ab1
    resource: repo://mc/verbs/failure_controls_test.go
  - id: openwiki-source-07fb00066b1d46072cdfcd3e
    resource: repo://mc/verbs/infrabudget_test.go
  - id: openwiki-source-ea398cb791fe089d23c9e34d
    resource: repo://mc/verbs/infrastructurefailure.go
  - id: openwiki-source-6590b775234fedddab69a28c
    resource: repo://mc/verbs/runner.go
  - id: openwiki-source-0097b0a41bffd53dce10abf3
    resource: repo://mc/verbs/skills/verifier/mc-verifier/SKILL.md
  - id: openwiki-source-f1f8658bc93aa1e51c399e87
    resource: repo://mc/verbs/skills/worker/mc-worker/SKILL.md
  - id: openwiki-source-947e8b3d1474c80d6712c56f
    resource: repo://mc/verbs/verbs.go
generated: { by: "culler-scribe", at: "2026-11-12T00:11:01.469Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-13T23:23:34.077Z
---

# Failure controls and retry allowances

Culler runs unattended, so it must decide alone whether a failed run deserves another attempt. It answers with small, owned allowances. Each failure is charged to exactly one owner, and each kind of condition stops only the work it owns. `mc` holds every count in the spine. The resident and the runners only report what they observe.

This page describes the implemented behavior in code and tests.

## Related documents

- The "Agent failure controls" section of `mc/dispatch/README.md` describes the current model.

## Task conditions: Decision Pending and Retries Exhausted

A task has two independent conditions. Neither changes its pipeline stage.

| Database field | Dashboard label | Meaning |
|---|---|---|
| `blocked` | Decision Pending | The task needs an operator decision or repair. |
| `stalled` | Retries Exhausted | The task used its task run failure allowance. |

In the schema, `stalled` is a generated column: `dispatch_retries = 0`. `dispatch_retries` holds the remaining units (0 to 3). `infrastructure_failure_count` is also generated, as `3 - dispatch_retries`. So "stalled" cannot disagree with the count.

An agent sets Decision Pending through its terminal action `mc complete --needs-operator --reason <question>` (`mc/verbs/complete.go`). For an assigned run, this blocks the task and ends the run with outcome `blocked`. For a proposal run (no task subject), `domain.RecordProposalDecision` records one shared proposal decision; each affected proposal gets a direct block. Neither path charges an allowance. The role directives (for example `mc/verbs/directives/worker.md`) tell agents to use `--needs-operator` only for a decision only the operator can make, and `--infra` only for a task run failure.

## The three allowances

| Allowance | Owner | Consumed by | At zero |
|---|---|---|---|
| Task run failure | each task | an assigned run that fails after its agent process started | the task is stalled |
| Project infrastructure | each Project | a launch failure before the process starts; any failed Strategist proposal or Arbiter proposal-review run | the Project starts no new agent run with its lease |
| Correction | each task | each completed correction result | an unresolved defect needs an operator decision |

The correction allowance belongs to the delivery flow; see [task pipeline](../workflows/task-pipeline.md).

### Classification in code

`RunInfrastructureFailure` in `mc/verbs/infrastructurefailure.go` is the entry for `mc run infra-failure` and for `mc complete --infra`. It checks the caller's scope (an agent may report only its own run; only the host may report as `resident`), validates the report, and calls `domain.RecordInfrastructureFailure` (`mc/domain/infrastructure.go`) in one transaction. The response states `charged`, `allowance_owner`, `remaining`, and `exhausted`.

`finishInfrastructureFailure` selects the owner from the run row:

- **Project allowance** when the run has no `agent_started_at`, or when it is a subjectless `strategist` or `arbiter` run.
- **Task allowance** otherwise.

`agent_started_at` is set by `RunProcessStarted` (`mc run process-started`). The agent runner calls it before it writes the brief to the harness (`runner/agent-runner/main.ts`). A claim alone is not evidence that the agent started.

`runFailureReason` then decides whether the failure is a **system condition**. A network error or resident-confirmed credential or connection cause is a system condition. Provider evidence normally identifies a system condition, but request-local provider failures remain charged run failures unless reported as a connection cause. It charges nothing and holds new runs through the connection state. An agent's own report is never a system condition; its cause is forced to `unknown`. A runner report without provider evidence is a `harness` failure and is charged.

Other no-charge paths:

- A cancelled subject. The run ends as `cancelled`.
- Reaps for `interrupted`, `reported-outcome`, and `vm-restart` (`ReapReason.chargesAllowance` in `mc/dispatch/dispatch.go`). `applyAction` in `mc/verbs/dispatchverb.go` passes `NoCharge` to `domain.ApplyReap`.
- A second report for a run that already ended. It may add a diagnostic event but keeps the count. Each run is charged at most once.

## Retry timing and exhaustion

`ConnectionRetryDelay` in `mc/domain/diagnostics.go` returns 30 seconds after the first failure, two minutes after the second, and five minutes after that. Both allowances use it:

- Task: `ChargeInfra` (`mc/domain/retry.go`) decrements `dispatch_retries`; `taskRetryAt` stores `run_retry_at`. No retry time is stored at zero.
- Project: `ChargeProjectInfrastructure` (`mc/domain/health.go`) increments `infrastructure_failure_count`, capped at 3, and stores `infrastructure_next_retry_at` only below 3.

`dispatch.Decide` enforces the waits. A Project at three failures idles with `project-infrastructure-exhausted`. A future Project retry time idles with `project-infrastructure-retry-wait`. A task with a future retry time is marked execution-held, so another task in the same Project can still run (`TestTaskRunRetryWaitDoesNotStopAnotherTask`). A retry time grants eligibility, not a start; every other dispatch control still applies. See [dispatch and the tick loop](../workflows/dispatch-and-tick-loop.md).

The Project stop leaves the Project's pause state and every task's `blocked` and `stalled` fields unchanged (`TestProjectInfrastructureStopsAssignedWorkWithoutPause`). Repository operations keep their own retry schedule: 60 seconds per target, described in `mc/dispatch/README.md`.

## Restoring allowances

- **Forward stage progress** restores three task units. The schema trigger `tasks_forward_progress_restores_infrastructure_allowance` fires when the stage rank rises.
- **A completed terminal result** restores the Project allowance through `ResetProjectInfrastructure`, unless the run submitted a checkpoint (`mc/verbs/verbs.go`). A checkpoint leaves both counts unchanged, because the work is incomplete.
- **Task resume** (`resumeTaskTx`) needs a reason and a current Retries Exhausted condition. It restores three units with `ClearInfra` and leaves an independent block in place.
- **Operator Project resume** (`ProjectInfrastructureResume` in `mc/verbs/systemstatus.go`) resets the Project allowance after a fresh admission check.
- **Decision resolution** (`ProposalDecisionResolve`) clears only the blocks of that decision.

## The attention threshold

The attention threshold is a count of distinct unarchived tasks with `blocked` or `stalled`, per Project. It is not an allowance. At three, `Decide` returns `attention-required` and ordinary merges stop. An active run can finish. Capture, cleanup, and receipt recovery continue. Other Projects are unaffected (`TestFailureHandlingAttentionStopsOnlyItsProject`).

## Heartbeats and run deadlines

Task run liveness is enforced by lease reconciliation in `dispatch.Decide`. Wiki runs use `DecideWiki` and separate liveness records. The agent runner sends `mc heartbeat` at session start and every `heartbeat_interval_s` (default 60). The `settings` table defaults are: spawn grace 60 seconds, lease timeout 60 minutes plus 15 minutes grace, and hard deadline 240 minutes of awake time. An ordinary task run that exceeds its applicable liveness limit ends through the reap path and its allowance classification.
The Scribe receives at least 60 seconds for its first heartbeat. Its hard deadline still applies, and wiki failure charges no allowance. These limits use VM uptime, so host sleep uses none of them.

`resident/src/pipeline-liveness.ts` supplies the container-gone evidence. `collectPipelineLiveness` reads `mc lease list`, measures each run's launch age from its launch record, and inspects its container. Only Docker's exact not-found counts as absent; an ambiguous inspect yields no observation. `confirmRunContainersAbsent` also gates repository cleanup.

## Connection recovery

Connection conditions own neither pipeline allowance. The runners classify a request that got no service response with `runner/shared/network-error.ts` (`connect`, `reset`, `timeout`, `tls`, `other`) and pass it as `--network-error` on `mc run infra-failure`. Such a failure is a system condition. The connection then waits through its retry schedule.

When the harness emits a `model-response` event, `modelResponseReporter` (`runner/shared/model-response.ts`) calls `mc run model-response <run_id>` once per attempt. `RunModelResponse` in `mc/verbs/runner.go` calls `domain.RecoverNetworkAttempt`.
That function first checks provider recovery, then clears an eligible network condition under the current connection, authorization, and profile. A role terminal also clears it (`ClearNetworkRetry`). Collie uses the same reporter through `mc collie model-response`.

A network timeout can change the connection revision before the process ends.
System status retains the active run or Collie reply attempt that the current diagnostic identifies.
`mc/verbs/networkhold_test.go` checks this visibility for both execution types.
An interrupted Collie reply preserves its partial draft and requires explicit retry; see [Collie](collie.md#interrupted-replies-and-explicit-retry).

[Agent connections and access renewal](../operations/agent-connections.md) describes account renewal, rejected keys, and quota failures.

## Tests

- `mc/dispatch/failure_controls_test.go`: Project exhaustion idles without a pause, a pending proposal decision still permits Seeded work, and a task retry wait does not stop another task.
- `mc/dispatch/failurehandling_test.go`: a stalled task is excluded at every stage; the attention pause keeps active runs and cleanup.
- `mc/verbs/infrabudget_test.go`: forward progress restores three task units; an `--infra` terminal keeps its own charge and stalls at zero; resume restores only the task allowance and keeps the block.
- `mc/verbs/failure_controls_test.go`: a successful proposal result restores the Project allowance; a checkpoint preserves both; proposals stay read-only to the operator; decision resolution and task resume stay independent.
- `mc/verbs/failurehandling_test.go`: report validation, a blocked report ends a run without a charge, final reports prevent another terminal, and attention is per Project.

Related pages: [resident](../architecture/resident.md), [runners](../architecture/runners.md), [dashboard](../architecture/dashboard.md).

## Wiki, setup, and merge repair boundaries

A failed wiki update consumes no task or Project failure allowance.
It preserves the task stages, Project lease, and explicit pause state.
The next scheduled wiki update can retry after its interval.
`mc/verbs/infrastructurefailure.go` handles this path separately.

Setup operations also have a separate attempt limit.
Each operation permits two attempts, with a 30-minute limit per attempt.
The [environment workflow](../workflows/project-environments.md) explains how reports and limits affect source acceptance.

Each merge conflict starts a merge repair through `domain.RequestMergeRepair`.
The task returns to Seeded with merge context.
Merge repairs have no separate finite allowance. Verifier corrections retain their correction allowance.
An approved task holds Project priority; an open correction alone does not create that priority.

`RequestMergeRepair` starts a repair for each conflict and clears the current approval through `returnToSeeded`.
`priorityTask` selects a currently approved task; an open correction alone holds no Project priority.

## Resolve the recorded condition

`blocked_kind` identifies the condition that owns Decision Pending.
An operator decision report uses `report` and requires revised requirements or task cancellation.
A reason alone cannot resolve that report, and an operator block cannot replace it.
The applied revision clears the report condition and returns the task to Seeded.
Until an active run or repository operation permits application, the report remains unresolved.

A requirements revision also ends a Verifier correction allowance limit.
It preserves independent customization, confinement, checkout, and operator conditions.
`blockAllowance` records `allowance` as the condition type only when the task has no other block.
`clearAllowance` clears Decision Pending only when `allowance` owns that condition.
This rule prevents an allowance grant from removing a separate operator decision.

The Worker and Verifier skills select outcomes for unavailable criterion checks and new or changed tests by cause.
An absent capability that no agent container has requires an operator decision report.
The report identifies the capability, affected requirement, and evidence.
A cause that the Worker can change requires repair; the Verifier returns CORRECT for that cause.
An Image dependency follows the Worker proposal and checkpoint procedure.
An unclear cause requires Worker investigation or a Verifier correction.

An unavailable host check outside the criteria requires no operator decision or task run failure by itself.
This exception excludes new or changed tests, which must pass.
Every unavailable check remains unverified, and independent work and checks continue.
An execution condition that prevents completion uses the role's task run failure procedure.
These rules keep a missing capability distinct from an execution failure that consumes a retry allowance.
See [decision resolution](../workflows/decision-resolution.md) for the complete condition table.

`mc/verbs/decisionresolution_test.go` checks report resolution, preserved independent conditions, allowance reset, and refusal of reason-only report resolution.
