---
type: workflows
title: Dispatch and the resident tick loop
description: How the resident's tick runs `mc dispatch`, how the pure `dispatch.Decide` picks one action per Project, how the prepare/attest/commit seam commits it, and how the resident applies the resulting effect JSON.
tags: [dispatch, resident, tick, lease, seam]
sources:
  - id: openwiki-source-a4b6d508300704786e4ce1a2
    resource: repo://mc/dispatch/delivery.go
  - id: openwiki-source-b578a24182c530b5d8630c21
    resource: repo://mc/dispatch/dispatch.go
  - id: openwiki-source-4470a63a5128bc251c50ad5e
    resource: repo://mc/dispatch/README.md
  - id: openwiki-source-74d6ad23a2a8895d737d9013
    resource: repo://mc/verbs/delivery.go
  - id: openwiki-source-58989c94d2eb04ff452f5b9d
    resource: repo://mc/verbs/dispatchorder_test.go
  - id: openwiki-source-c78a55ff7e0637dd368cb5fd
    resource: repo://mc/verbs/dispatchseam.go
  - id: openwiki-source-d5b0d35c57d0012e3c02877e
    resource: repo://mc/verbs/dispatchverb.go
  - id: openwiki-source-092d6a0d4f690c037c26bca8
    resource: repo://mc/verbs/mountattest.go
  - id: openwiki-source-000595f05ff9445f26008057
    resource: repo://mc/verbs/pause.go
  - id: openwiki-source-e028a0037f071b894557c29d
    resource: repo://resident/src/effects.ts
  - id: openwiki-source-b65f5016ff00568fc3622948
    resource: repo://resident/src/main.ts
  - id: openwiki-source-169bda06f2ca62ff273679a9
    resource: repo://resident/src/nudge-socket.ts
  - id: openwiki-source-79628d0b1a3667fb37eebff9
    resource: repo://resident/src/tick-loop.ts
generated: { by: "culler-scribe", at: "2026-11-12T00:11:01.469Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-12T00:11:01.469Z
---

# Dispatch and the resident tick loop

Dispatch is the part of Culler that decides which agent run starts next. `mc` owns every decision and every database write. The resident (`resident/`) only asks for a tick and applies the effects that `mc` returns. This split keeps all control logic inside one Go binary that is the sole spine writer (see [the spine and substrate](../architecture/spine-and-substrate.md) and [the mc CLI](../architecture/mc-cli-and-verb-scope.md)).

The resident requests the first tick at startup through `handle.nudge()` in `resident/src/main.ts`.
Later timer events use the configured interval, which defaults to 60 seconds.
Startup and later nudges use the same tick loop.
A nudge during an active tick requests one additional round.

The flow for one tick:

1. A timer or a nudge in `resident/src/tick-loop.ts` starts one tick.
2. The tick runs `mc dispatch`.
3. `mc` runs a Collie stage and then one pipeline round per Project, followed by wiki rounds. Each pipeline round calls `dispatch.Decide` and commits its result through the prepare/attest/commit seam.
4. `mc` prints one tick object: `{"action":"tick","actions":[...],"idle":[...]}`.
5. `resident/src/effects.ts` applies each action in order.

Repository operations (capture, cleanup, merges) do not run in the tick. The repository lane runs them; see [git delivery](git-delivery.md).

## `dispatch.Decide`: the pure decision

`Decide` in `mc/dispatch/dispatch.go` evaluates one Project. Its inputs are:

- `Records`: one Project's tasks, packets, active executions, retry times, cleanups, vision state, attention task ids, connection state, and Project infrastructure failure count.
- `Lease`: the Project lease row (holder run, owner role, subject, boot id, and uptime stamps for claim, last heartbeat, and hard deadline).
- `Config`: review queue cap (default 3), lease timeout (60 minutes), grace (15 minutes), and spawn grace (60 seconds).
- `Clock`: wall time, VM boot id and uptime, and an optional `HostLiveness` observation.

It returns one `Action`: `KindIdle` with an `IdleReason`, `KindReap` (a stop of the current run), or `KindSpawn` (a new agent run). It performs no I/O and does not mutate its inputs. `TestProperty_ExactlyZeroOrOneAction_DeterministicAndPure` and `TestProperty_RecordOrderInvariance` in `mc/dispatch/property_test.go` check this.

The decision order inside `Decide`:

1. **Lease held.** If a run holds the lease, `reapable` checks, in order: a recorded final outcome, a cancelled subject (`interrupted`), a changed VM boot id, the first-heartbeat watchdog (spawn grace), the lease timeout (timeout plus grace after the last heartbeat), the hard deadline, and a host-confirmed missing container. A match returns a reap. Otherwise the result is `lease-held`.
2. **Project gates.** A paused or archived Project returns `project-inactive`. Update recovery returns `repository-operation`. An environment condition holds new work. Three Project infrastructure failures return `project-infrastructure-exhausted`. A future Project retry time returns `project-infrastructure-retry-wait`. A task with a future retry time is marked execution-held.
3. **Vision and attention.** No valid `VISION.md` returns `vision-required`. Three or more attention tasks return `attention-required`.
4. **Repository holds.** An active or due repository operation that holds agent runs returns `repository-operation`.
5. **Selection** in `decideAgentRun`: an approved task takes precedence, then review capacity limits the eligible tasks. Task priority orders the eligible tasks. A pending proposal decision returns `proposal-decision-pending` when no task can proceed. Otherwise, an available Strategist receives the next run when no task can proceed.

The lease timeout and deadlines compare VM uptime, not wall time. Host sleep therefore uses none of a run's time budget. The container-gone check uses only host-measured launch age, and absent evidence never reaps. A wrong reap destroys live work; a missed reap costs one tick.

### Priority rules

`priorityTask` in `mc/dispatch/delivery.go` selects the approved task with the smallest task number.
While that task remains approved, `priorityAction` restricts agent selection to it.
Its approved merge retains precedence over expedited work.
A merge repair clears the task approval, so the task returns to the ordinary selection rules.

Without an approved task, `lessNextDispatch` compares eligible tasks in this order:

1. The task priority: expedited (-1), then P0 through P3.
2. An open merge repair after approval, before other tasks of that priority.
3. A task that received a Worker run, before a task without one.
4. The furthest pipeline stage.
5. The older task, then the smaller task number.

A Seeded P1 task therefore precedes a Verified P2 task.
An open correction follows the same comparisons. Higher task priority precedes a correction or merge repair.
The active lease remains in effect after a priority change, so an active agent run continues.

At review capacity, `inFlightRevision` considers only tasks with a packet that is neither archived nor superseded.
It applies the same comparison order to those tasks.
An expedited task without a live packet waits even when it received a Worker run.
This rule preserves the review slots that revisions already occupy.
Blocked, stalled, archived, or execution-held tasks remain ineligible.

`spawnFor` maps Proposed to Arbiter, Seeded to Worker, Worked to Verifier, and Verified to Packager.
An Arbiter assignment contains the whole proposed pool.
The tests in `mc/dispatch/priority_test.go` cover approval precedence, task order, active leases, and review capacity.
`mc/dispatch/dispatch_phase2_test.go` checks priority before stage at review capacity.

### Retained task facts

`loadDeliveryRecords` in `mc/verbs/delivery.go` derives two facts from the spine for dispatch:

- `WorkStarted` is true when the task has its first Worker base commit.
- `RepairAfterApproval` is true when a correction remains open and the current review packet has a recorded approval.

A merge repair clears the task approval but keeps that packet.
The retained decision therefore identifies a repair after approval through repeated conflicts.
A completed repair clears this preference. An operator packet revision alone does not create it.
`mc/verbs/dispatchorder_test.go` checks these facts across actual delivery transitions.
See [Git delivery](git-delivery.md) for retained commits, packets, and merge recovery.

### Per-Project leases

Each Project has one lease row, so each Project has at most one agent run with its lease. Projects do not share a lease. `TickOrder` returns every Project id in sorted order, including paused Projects, because a reap can still apply to them. Collie, Scribe wiki runs, and repository operations claim no lease. The dispatch guide `mc/dispatch/README.md` states this model and the full repository-operation rules.

## The prepare/attest/commit seam

`Dispatch` in `mc/verbs/dispatchverb.go` is the one caller of `Decide`. It reads the tick order, runs the Collie stage, then calls `dispatchRoundWithIdentity` once per Project. A round failure after the first round is appended to `actions` as an error envelope and ends the pipeline rounds early. Committed effects stay valid.

Each round has three steps (`mc/verbs/dispatchseam.go`):

- **Prepare** (`dispatchPrepareWithIdentity`, `dispatchPreparePipelineRound`) runs under the dispatch process flock and one `BEGIN IMMEDIATE` transaction. It checks the deployment identity and the receipt of the request id first, then selects. An idle or reap result finishes inside prepare; a reap writes its receipt in the same transaction. A spawn becomes a candidate: prepare allocates a run id, loads the mount state, and freezes a canonical projection of the inputs under a SHA-256 preparation token.
- **Attest** (`dispatchAttest`) runs with no spine lock held. It re-reads the deployment mirror, loads runtime authority from config, parses `routing.md`, resolves the role route, and attests the mount plan. Host-file faults become classified refusals, not errors. `dispatchRecheckAttestation` repeats the attestation right before commit; any drift becomes a stale refusal.
- **Commit** (`dispatchCommit`) takes a fresh flock and transaction. It re-selects with its own clock, rebuilds the canonical projection, and requires the token to match byte for byte. A changed token is `stale`; a different reselected candidate is `candidate_mismatch`. Both are inert. Only then does `applySpawn` claim the lease and build the spawn effect. `deriveDispatchKey` binds the token and the canonical action into an idempotency key, and the receipt makes a retried request replay rather than claim twice.

The canonical structs at the top of `dispatchseam.go` define the wire contract.
They use closed structs, declared field order, sorted collections, and explicit zero values where required.
The projection excludes maps, floats, and wall-clock time.
`canonicalDelivery` includes `version_id`, `work_started`, and `repair_after_approval`; the two Boolean fields omit false values.
The preparation token therefore includes the retained facts that affect task order.
The dispatch-key domain is `MC-DISPATCH-ACTION-V10`.
Golden vectors in `mc/verbs/dispatchseam_test.go` check canonical preparation and key derivation.

In production, the same seam crosses a process boundary. `mc/verbs/dispatchprivate.go` defines closed frames (`PrivateDispatchPlanRequest`, `PrivateDispatchPrepareRequest`, `PrivateDispatchCommitRequest`) that the host broker in `mc/cmd/mc/private_dispatch.go` exchanges with the helper through `__dispatch-plan`, `__dispatch-prepare`, and `__dispatch-commit`. The prepare frame carries the host's `PipelineLiveness` observation. The resident collects it with `collectPipelineLiveness` (`resident/src/pipeline-liveness.ts`), wired in through `execMcVia` in `resident/src/resident-control.ts`.

## The resident tick loop

`startTickLoop` in `resident/src/tick-loop.ts` keeps one tick in flight at a time:

- A timer firing during a tick is skipped and logged as `tick.skipped`. It is not queued.
- `nudge()` runs a tick at once when idle. During a tick it sets one pending flag, so many nudges coalesce into one extra tick.
- `stop()` clears the timer and waits for the in-flight tick.

`tick()` first checks an installer maintenance pause, then runs `mc dispatch`. A nonzero exit, unparseable output, or a non-tick object is logged and skipped. `applyTick` applies each action in order; a thrown or failed action is logged and the next action still runs. Idle Projects are logged at debug level. The default interval is 60 seconds (dispatch guide).

### The nudge socket

`resident/src/nudge-socket.ts` listens on `MC_HOME/resident.socket` with mode 0600. Each connection sends one line of at most 64 bytes. `parseNudgeLine` accepts only a Collie session id (`h-` plus 16 hex digits), `vision-save`, `project-start`, or `packet-decide`. Other input is refused and logged. `main.ts` routes each accepted line to `handle.nudge()` and to the repository lane.

## Applying effects

`applyEffect` in `resident/src/effects.ts` handles `idle`, `spawn`, `reap`, and the Collie actions. A `refused` effect is only logged, because `mc` already recorded its consequence. Unknown actions are logged and ignored (fail-closed).

A **spawn** validates the run id and Project id, writes the launch envelope to `MC_HOME/runs/<run_id>.json` (mounted read-only at `/mc/run.json`), writes a host-only mount-plan sibling, runs `mc __mount-recheck`, runs `docker create`, rechecks again, and runs `docker start`. The container is named `mc-<project>-<run_id>`. Drift after create removes the unstarted container and launch files. A preparation, create, or start failure is reported through `mc run infra-failure --source resident`, which charges the Project infrastructure allowance because no agent process started (see [failure controls](../concepts/failure-controls.md)).

A **reap** stops `mc-setup-<run_id>` and the pipeline container.
If a container can still run, it stops there.
Otherwise it calls `mc run stopped`.
Only after `mc` accepts that result does it delete provider access, envelope, plan, seeding, and scratch files.
Run logs remain available for diagnosis.

## Project pause and resume

`mc pause` and `mc resume` (`mc/cmd/mc/main.go`) call `SetEveryProjectPause` in `mc/verbs/pause.go`. Pause sets every active Project to paused in one transaction. Resume sets every paused Project with `setup_status = 'ready'` back to active; Projects with incomplete setup stay paused. Pipeline agents cannot call it. The dashboard and permitted operator agents use these commands through `RequireOperatorVerb` and transaction-level session authorization.

A pause makes `Decide` return `project-inactive` but still reconciles the lease, so an active run finishes and a dead run is still reaped. Collie keeps working. Tests:

- `mc/dispatch/pause_test.go`: pause idles every role and resume restores the same selection.
- `mc/verbs/pause_test.go`: `TestPauseAllAndResumeAllChangeEveryProject`, `TestPipelinePauseAllowsCollieDispatch`, `TestPausedPipelineReleasesExpiredLease`, and `TestPauseBetweenPrepareAndCommitRefusesRun`, which shows that a pause between prepare and commit makes the commit inert.

## Related tests

- `mc/verbs/dispatchverb_test.go`: `TestDispatchTickRunsOneRoundPerProjectInIDOrder`.
- `mc/verbs/dispatchreceipt_test.go`, `dispatchprivate_test.go`, `dispatchframe_test.go`: receipts and private frames.
- `resident/src/tick-loop.test.ts`: one tick at a time, nudge coalescing, and fail-closed tick failures.
- `resident/src/nudge-socket.test.ts`: socket contract.

Related pages: [resident](../architecture/resident.md), [task pipeline](task-pipeline.md), [runners](../architecture/runners.md).

## Independent wiki rounds

`dispatchWikiRounds` evaluates each Project after the pipeline rounds.
The host observes accepted commits under a shared time limit, then performs prepare, attest, and commit for wiki work.
`DecideWiki` selects a Scribe run without a task or Project lease.
The commit recheck prevents a run from starting with a stale accepted commit.
A wiki round failure remains in the tick result; later Projects can still receive wiki rounds.

An active wiki run can finish during an explicit Project pause.
The wiki round repeats container stop requests until the host confirms the stop.
Wiki failures use no task or Project allowance.
See [wiki updates and viewers](../integrations/openwiki-and-viewers.md) for due conditions and completion checks.

An open correction follows the task priority rules and leaves other tasks' merge steps eligible.
Required receipt recovery preserves the destination during a pause.
An unfinished wiki merge authorization holds task merge steps until recovery resolves it; it does not hold new agent runs.

## Project home attestation

`captureDispatchMountHostSnapshot` in `mc/verbs/mountattest.go` prepares the selected Project home before it records the filesystem identity.
`expectedPrivateToolHome` derives the expected path for both task and wiki runs.
The wiki commit step requires that home with its isolated repository and jurisdiction digest.
It refuses ordinary mount entries and committed projections for the Scribe.

The resident uses the attested canonical path for `HOME=/home/agent`.
A runtime-volume launch without that home is refused before container creation.
Separate temporary role directories preserve concurrent Scribe and task instructions.
Persistent dependencies do not replace the assigned source or change lease ownership.
See [Project environments](project-environments.md#runtime-and-per-run-dependencies) for the storage lifecycle.
