# Dispatch and control

`mc` owns every control decision and every database change.
The resident requests a tick at start and at each tick interval, 60 seconds by default. The resident executes the agent run effects that `mc` returns.
The resident also sends a start request at each start event, and `mc` records each due repository operation as an execution.
The dashboard presents records and submits operator decisions.

## The task model

A Project has one Project lease and its own pause and failure state.
Only agent runs claim the Project lease.
The Scribe run of a wiki update is an agent run that claims no Project lease.
A task holds its current stage, base commit, version, correction condition, block, and review reservation.
A run records one agent run or one execution of a repository operation.

An immutable version identifies the submitted Git commit.
An immutable candidate records the Git merge of that version with a specific destination commit.
An operator decision identifies an exact review of that submitted version.

Homie operates separately from the Project lease.
Repository operations claim no Project lease.
They run at the same time as the agent run of the Project.

## Project environments

Project initialization uses separate setup operations, outside the task pipeline and the Project lease.
Vision acceptance authorizes the initial setup operation.
The setup agent prepares the available dependencies and reports actual command results and limits.
A complete report permits source acceptance despite unavailable builds or tests.
A proposal for Image dependencies retains a standard Dockerfile and its build files.
Operator approval authorizes those exact contents before the resident builds the image.
Culler checks runtime compatibility before the continuation uses the approved image.
Denial starts a continuation in the exact current image.
Either initial continuation must submit `complete` with accurate limits.
Further image improvements use the Environment page after import.
Source acceptance preserves the files, configuration, and explicit Project pause.
Repository requirements still control task verification.

Each Project retains its selected image and immutable configuration revision.
A Culler update replaces its separate runtime files without rebasing that Project image.
An incompatible runtime holds new Project work and preserves the selected image.
Each source run records its environment assignment and observed tool and package evidence.

A pipeline proposal remains a candidate until its source reaches `main` through ordinary review and acceptance.
Retained task versions and merge candidates preserve that environment across source continuation.
An interrupted source association holds new work until receipt recovery completes the association.
That branch does not change the Project's global selection.

## The next agent run

`Decide` reads one Project's records and an explicit clock.
It returns a stop of an agent run, a spawn of an agent run, or an idle reason.
It does not change its inputs.
The tick evaluates Projects in identifier order.
`Decide` selects only the agent runs that claim the Project lease.
The section "Wiki updates" states the rules for the Scribe run of a wiki update.

The decision follows this order:

1. A current lease keeps its agent run or requires a stop.
2. An explicit pause, an inactive Project, or an exhausted Project infrastructure allowance prevents new agent runs.
3. The Project requires an accepted environment, a valid vision, and fewer than three attention tasks.
4. A due or active capture or merge candidate step holds new agent runs.
5. An approved task holds the Project priority. Only the priority task and the tasks that it admits receive agent runs.
6. Without a priority task, the agent run of an open correction starts first.
7. Review capacity limits ordinary task execution.
8. An eligible task proceeds, or the Strategist proposes more work.
9. A task with an active repository operation, except cleanup, receives no agent run.

The hold of a capture requires a task that is not stalled.
The hold of a merge candidate step requires a packaged task that is not blocked or stalled.
Each condition that holds agent runs corresponds to an active execution, a due operation, a retry time, or a stall.
A repository operation of a wiki update is no such condition.

`Decide` receives one fact of a wiki update.
The fact is true while the merge authorization of a wiki commit of the Project is unfinished and the execution of that merge ended.
Receipt recovery then checks the result of that merge.
While the fact is true, each merge step of a task waits, and the wait holds no new agent run.
New agent runs of the Project thus continue under the other rules of this section.

While the merge candidate step or the merge of a wiki commit is active, the fact is false.
A due merge candidate step of a task then holds new agent runs, as it does with no wiki update.
The section "Selection" states each rule of this wait.

A spawn claims its run before `mc` records it in the activity feed.
A failed connection check stops the spawn and claims no run.
The Project then records the connection and the task of that spawn.
The activity feed receives one record when the wait starts and one record when it ends.

## Wiki updates

The Scribe has no task.
The agent steps of a task are the Worker, the Verifier, and the Packager.
A wiki update is one Scribe run for one Project, together with the merge of its wiki commit into `main`.
A Project has a maximum of one active wiki update.
A wiki update is active from the start of its run until `mc` records its result.

These terms apply to a wiki update:

- The Project wiki is the `openwiki/` folder of the Project repository.
- The accepted commit is the commit at the `main` branch of the Project repository.
- The start commit is the accepted commit at the time that `mc` creates the run.
- The wiki commit is the commit that the Scribe submits.
- A source change is a change that a commit other than a wiki commit makes to `main`.

### Wiki state

`mc` derives the wiki state of a Project from commit equality only.

| Wiki state | Condition |
| --- | --- |
| `update-active` | The Project has an active wiki update. |
| `current` | The last successful wiki update covers each source change of the accepted commit. |
| `behind` | Each other case. |

The last successful wiki update covers each source change of the accepted commit in these two cases:

- Culler merged its wiki commit, the destination commit of that merge equals its start commit, and the accepted commit equals the merged commit.
- The Project wiki needed no change, and the accepted commit equals its start commit.

A Project with no successful wiki update is behind.
A Project with an unknown accepted commit is also behind.
A wiki commit on `main` thus starts no wiki update by itself.
Each other new commit on `main` makes the Project wiki behind.
`mc wiki-state list` returns the wiki state, the active wiki update, and the last wiki update of each Project that is not archived.
The dashboard shows each text of that read without its own derivation.

### Due conditions

`mc` decides when a wiki update is due.
A wiki update of a Project is due only when each of these conditions is true:

- The Project is active.
- No install pause request is in effect.
- The Project wiki is behind.
- The Project has no active wiki update.
- The Project has no earlier wiki update, or the wiki update interval elapsed since the start of the last one.
- The accepted commit of the Project is known.
- Culler confirmed the stop of each earlier wiki update run of the Project.

These facts have no effect on the due conditions:

- The Project lease.
- The stage, the block, and the review decision of each task.
- The Vision state of the Project.
- The attention threshold.
- Each failure allowance.
- Each role block and the connection wait of the Project.
- An active artifact expiry.

The wiki update interval is the minimum time between the starts of two wiki updates of one Project.
The setting `timing.wiki_update_interval_s` holds the interval in seconds, and its default is 3600.
The operator changes the setting on the Configs page of the dashboard.
Each wiki update counts for the interval, with one exception.
The exception is a wiki update whose connection changed or became unavailable before the launch of its run.

### The wiki round

Each tick runs one wiki round for each Project of the tick order, after all pipeline rounds.
The host reads the `main` branch of each Project before the first wiki round, under one shared time limit of 5 seconds.
A wiki round has the steps prepare, attest, and commit.
`DecideWiki` reads the wiki facts of one Project and an explicit clock.
It returns a stop of a wiki update run, a spawn of a Scribe run, or an idle reason.
It leaves its inputs unchanged.

The decision follows this order:

1. An active wiki update run in an archived Project receives a stop.
2. An active wiki update run receives a stop when a liveness rule applies.
3. An ended wiki update run without a stop confirmation receives a stop of its container.
4. A Project that is not active starts no wiki update.
5. An active wiki update prevents another wiki update.
6. An unknown accepted commit prevents the start.
7. A current Project wiki prevents the start.
8. The wiki update interval prevents the start.
9. `mc` starts a Scribe run with a null task subject at the accepted commit.

A wiki round also serves a paused Project and an archived Project, because it can stop a run there.
The idle reason of a wiki round stays outside the tick result.
The next tick waits for each wiki update spawn of the tick.
Each spawn includes one clone of the repository.

A wiki round that fails adds one error to the tick result.
The tick then continues with the wiki round of the next Project.
A wiki round that reaches the time limit of the helper container ends the wiki rounds of that tick.
A tick that fails before its first round completes runs no wiki round.

### Start and liveness

The commit step reads the `main` branch again.
The wiki update starts only when that commit, the stored accepted commit, and the start commit are equal.
The start claims no Project lease.
The run uses the agent profile and the custom instructions of the Scribe role.

The run receives the Project repository at the start commit in its own temporary repository.
The destination commit of that repository is the start commit, and the reference `refs/mc/destination` names it.
The repository holds no later commit of `main`.
The run can be active at the same time as the agent run of the Project, a Homie session, and each repository operation.

The connection check of an agent run applies before the start.
A failed check starts no run and records no connection wait for the Project.
The wiki update stays due, and the next tick checks the connection again.
A wiki update run and an agent run share the connection state of their agent profile.
A connection failure of a wiki update run holds each new run of that profile until the connection is ready.

The heartbeat rules, the timeout, and the hard deadline of an agent run apply to a wiki update run.
The first-heartbeat grace of the run is the larger of the spawn grace and 60 seconds.
The wiki update record holds the liveness facts of its run.
The resident observes the container of each active wiki update run.
An explicit pause of the Project permits an active wiki update run to finish.
An archive of the Project stops the run and ends the wiki update as failed.

After each end of a wiki update run, the wiki round requests the stop of its container.
The request repeats at each tick until the resident confirms the stop.
This rule includes a run that completed.
A refused start has no container, and `mc` records that stop confirmation itself.

The resident runs no tick while an install pause request is in effect.
No wiki round runs then, and no wiki update starts.
The section "Start requests" states how an install pause request waits for a wiki update.

### Terminal actions and result

The Scribe ends each run with exactly one of these commands:

- `mc complete --run <run_id> --commit <full-commit> --wiki-evidence <path>` submits the wiki commit and the OpenWiki completion receipt.
- `mc complete --run <run_id> --infra --reason <cause>` reports a run that cannot give a result.

`mc` accepts a wiki commit only when each check of the wiki commit passes.
The [OpenWiki integration guide](../../runner/openwiki/README.md) states the checks.
A failed check ends the run and ends the wiki update as failed.
A wiki commit that equals the start commit is a successful wiki update with no capture and no merge.
Each other accepted wiki commit keeps the wiki update active until the result of its merge.

The activity feed receives one record when a wiki update starts and one record when it ends.
The end record states the result and, for a failed wiki update, the cause.

### Failed wiki update

A failed wiki update leaves `main` unchanged, and its source changes stay uncovered.
The next wiki update of the Project becomes due after the wiki update interval.
A failed wiki update has no retry limit.
A wiki update run uses no failure allowance.
A failed wiki update starts no agent run and no correction.
It records no infrastructure failure and blocks no role.

A failed wiki update leaves these facts unchanged:

- The stage, the version, the block, and the failure allowance of each task.
- The Project lease.
- The Project infrastructure allowance.
- The pause state of the Project.
- The count of tasks that require attention.

### Merge of the wiki commit

Culler merges each accepted wiki commit into `main` with no review packet and no operator decision.
The merge uses capture, the merge candidate step, the merge, and receipt recovery, as the merge of a task does.
The section "Selection" states the rules of the merge lane and the effects of these merge steps on a task.
The merge of a wiki commit leaves each review decision valid.

The merge candidate step applies the scope check to the merge candidate of a wiki commit.
The scope check compares the merge candidate with its destination commit.
It reads the temporary repository of the merge candidate step, and no agent writes that repository.
The [OpenWiki integration guide](../../runner/openwiki/README.md) states the rules of the scope check.
Culler retains a merge candidate only after it passes the scope check.
The merge of a wiki commit thus changes no path of `main` outside `openwiki/`.

These results end a wiki update that has an accepted wiki commit:

- The merge succeeds, and the wiki update succeeds.
- The merge candidate step finds a conflict. The wiki update fails, and Culler discards the wiki commit.
- The `main` branch does not hold the start commit. The wiki update fails, and Culler discards the wiki commit.
- The merge candidate fails the scope check. The wiki update fails, its cause states the failed check, and Culler discards the wiki commit.
- Git refuses the merge in the operator checkout. The wiki update fails, and its cause states the refusal.
- The merge ends with no result, and receipt recovery proves that `main` is unchanged. The wiki update fails.

A changed destination branch ends no wiki update.
For a wiki commit, Culler makes no destination observation.
A new merge candidate step starts after one destination quiet interval.

The temporary repositories of tasks and the temporary repositories of wiki updates use separate folders.
The temporary disk limit applies to each folder.

## Repository operations

A repository operation is Git work or file work that the resident executes on the host.
These are the repository operations:

- Capture copies the submitted commit of an agent run into the Project repository.
- Cleanup removes the temporary repository of a run.
- Destination observation reads the destination branch after the branch changes.
- The merge candidate step merges a submitted version or a wiki commit with the destination branch in a temporary repository.
- The merge is the conditional update of the destination branch to the merge candidate.
- Receipt recovery completes or cancels an interrupted merge from its receipt and its journal.

A merge step is destination observation, the merge candidate step, the merge, or receipt recovery.
An execution is one attempt of one repository operation.
Each execution has its own run.
Each repository operation serves one task or one wiki update.
A wiki update has no destination observation.

### Start requests

The resident sends a start request at each of these start events:

- The resident starts.
- A container of an agent run exits.
- An execution ends.
- A tick ends.
- The dashboard records a packet decision.
- A Homie turn ends.
- The earliest due time from the latest start response arrives.
- An artifact expiry call ends.
- An install pause request starts or ends.

The resident sends one start request at a time.
An event during a start request causes one more start request after it.
A start request records each due repository operation as an active execution in one transaction.
The response lists each active execution of the resident, the stop list, and the earliest due time.
The resident applies the stop list before it starts the new executions of the response.
A failed start of one operation leaves the other starts of the request intact.
`mc` publishes a review packet when its merge candidate merged without a conflict and the Project has review capacity.
After an install pause request, a start request starts only receipt recovery until the resident confirms the pause.
After the confirmation, the resident sends no start request until the pause request ends.
`mc` reports the pipeline as idle only when no execution is active and no merge authorization is unfinished.

An install pause request waits for the run of a wiki update as it waits for an agent run.
A wiki update that waits for the start of a repository operation holds no install pause.
Its operations start after the installation.

### Selection

`Decide`, the start selection, and the merge state use the same predicates.
The start selection and the merge state also read the facts of the wiki update of the Project.
`Decide` receives one fact of a wiki update.
The section "The next agent run" defines that fact.

- Each operation target has a maximum of one active execution.
- Each Project has a maximum of one active merge step. The limit counts the merge steps of tasks and of wiki commits together.
- Culler runs a maximum of 4 cleanups at a time.
- A repository operation of a task, except cleanup, starts only while no agent run of that task is active.
- A repository operation of a wiki update starts only after the run of that wiki update ended.
- A Project with an active artifact expiry starts no repository operation.

Capture and cleanup follow these rules:

- Capture and cleanup proceed during an explicit pause, at the attention threshold, without a valid `VISION.md`, and while another task holds the Project priority.
- A stalled task receives no capture.
- Capture precedes each later agent run of its task.
- Cleanup starts only after these conditions hold:
  - The run ended.
  - The capture of the run is complete, or the run submitted no commit.
  - No unfinished merge operation uses the run.
  - The resident confirms that each container of the run is absent.
- When capture or cleanup finds a container of an ended agent run during its grace of 60 seconds, the operation ends without a charge.
- After the grace, the resident stops and removes each container of the ended agent run.

These rules select the merge steps of a Project:

1. Receipt recovery precedes each other merge step.
2. Receipt recovery holds each other merge step of its Project while it waits, waits for its retry time, or runs.
3. An unfinished merge authorization of a blocked or stalled task holds each merge step of its Project.
4. An operator approval gives its task the Project priority. The approved task with the smallest task number holds it.
5. An approved task holds the Project priority until its merge completes or the task cancels. A merge repair clears the approval and ends the priority.
6. While a task holds the Project priority, the merge of each other task waits for the merge of the priority task.
7. Each other packaged task without an approval receives its merge candidate step as in a Project without a priority task.
8. A due merge step of the priority task starts before a due merge candidate step of another task.
9. An approved task that waits for another approved task also waits with its new merge candidate.
10. Approved tasks merge in task number order.
11. A blocked or stalled task receives no merge step. A blocked or stalled priority task holds each merge of another task. The merge candidate steps of the other tasks continue.
12. A changed destination branch requires the destination quiet interval and a new merge candidate.
13. The approval stays valid when the new merge candidate merges without a conflict.

These conditions prevent each merge step of a task except receipt recovery:

- An explicit pause of the Project.
- The attention threshold: three tasks that require attention in the Project.
- A Project without a valid `VISION.md`.

A merge that started can complete during an explicit pause.
Receipt recovery preserves the destination branch.

These rules select the merge steps of a wiki commit:

- One merge lane serves each Project. No merge step of a task runs at the same time as a merge step of a wiki commit.
- A merge step of a wiki commit starts only when no merge step of a task can start or is active in that Project.
- The merge candidate step and the merge of a wiki commit start only in an active Project. An explicit pause holds them until the Project is active.
- An unfinished merge authorization of a task also holds them.
- A merge step of a wiki commit proceeds at the attention threshold, without a valid `VISION.md`, and while a task holds the Project priority.
- The order of approved tasks does not apply to a wiki commit.
- Capture, cleanup, and receipt recovery of a wiki update start as those of a task do.
- An unfinished merge authorization of a wiki commit holds each other merge step of its Project. The hold ends when the merge or receipt recovery proves the result.

The merge steps of a wiki commit have these effects on a task.
A merge step of a wiki commit can delay a merge step of a task by the time of one merge step.
The deadline of that merge step is the bound of the delay.
During the merge candidate step and during the merge of a wiki commit, a due merge candidate step of a task holds new agent runs.

The merge of a wiki commit can end with a Git refusal or with no result.
Each merge step of the Project then also waits for one receipt recovery.
After an end with no result, the retry time precedes that receipt recovery.

These rules apply while the merge authorization of a wiki commit is unfinished and the execution of that merge ended:

- Each merge step of a task waits, and the wait holds no new agent run. New agent runs of the Project continue.
- The retry time of a merge candidate step of a task holds new agent runs until that time ends.
- `mc` publishes no review packet for a task whose merge candidate step waits. A task with no review packet uses no review capacity.
- An approved task keeps the Project priority, and unrelated work of the Project waits.

While receipt recovery cannot prove the merge of a wiki commit, each merge step of the Project waits for the repair of the operator checkout.
The wiki state, each approved packet that waits, and System health of the dashboard state the repair.
When no result names a repair, they state the condition of the last receipt recovery.
System health states the condition and the repair after one receipt recovery ended with no proof.
It states them for each Project that is not archived.
When no result names a repair, System health also states the delay before the next receipt recovery and the instruction for the operator.

### Liveness and recovery

Each resident instance registers a new owner number at start.
Each new owner number is larger than each earlier owner number.
A start request from a new owner ends each execution of an earlier owner.
`mc` refuses a start request and a heartbeat from an earlier owner.
The refused resident exits, and launchd starts a new resident instance.

`mc` records the first heartbeat of an execution when it starts the execution.
The resident renews all its active executions in one heartbeat every 20 seconds.
An execution without a heartbeat for 90 seconds ends.
The merge candidate step and the merge end at a 10-minute deadline.
Each other repository operation ends at a 5-minute deadline.

Each record of a result ends its execution, including a repeated record.
Each other end gives the operation target a retry time of 60 seconds.
Each other end charges the task, with these exceptions:

- A container of an ended agent run is present during its grace.
- An owner change ends the execution for the first time. The second consecutive owner change on one target charges the task.
- A host restart ends the execution.
- The task has a cancel decision, and the operation is not a capture.

An execution of a wiki update charges no allowance.
Before a merge authorization exists, an end that charges a task fails the wiki update.
While a merge authorization is unfinished, only a result of receipt recovery ends the wiki update.
An end of a cleanup changes no wiki update.

Each end of a capture execution of a task marks its transfer record as finished.

Each ended execution stays in the stop list until the resident confirms that its host work stopped.
`mc` accepts the stop confirmation of an earlier owner only after 90 seconds without a heartbeat or after a host restart.
Receipt recovery starts only after these conditions hold:

- The execution of the interrupted merge ended, and its retry time ended.
- The owner of that execution confirmed the stop of its host work.
- The resident confirms that the container of the interrupted merge is absent.
- For a task, the task is not blocked or stalled.

## Approval and correction

Approval gives the task the Project priority at once.
The merge starts without a wait for the active agent run.
`mc packet list` states the merge state of each approved packet: the reason that the merge is not complete.
The dashboard and Homie show that text without their own derivation.
The task retains that priority through destination changes until its merge completes or the task cancels.
The other packaged tasks receive their merge candidate steps and review packets during that priority.
The operator controls the merge order through approvals.

A changed main branch does not invalidate the submitted version.
Culler merges the submitted version with current main in a new candidate.
Approval remains valid when the new merge succeeds and the submitted version stays the same.
A changed submitted version requires a new review and a new approval.

Each Git conflict starts a merge repair, before and after approval.
The merge repair clears the approval and ends the Project priority of its task.
The Worker repairs the task.
The agent of the merge repair reports an operator decision when the two changes contradict, and the task blocks.

A Verifier correction, a merge repair, and an operator packet revision each open a correction.
A merge candidate of the task without a conflict closes the correction.
An open correction holds no Project priority and no merge step of another task.
The packet of a task with an open correction keeps its review slot.

In a Project without a priority task, the agent run of an open correction starts first.
Among open corrections, the smaller task number starts first.
When no open correction can start an agent run, the ordinary rules select the next agent run.
The other tasks of the Project continue while a correction is blocked or stalled.

A Git refusal in the operator checkout blocks the task at once, before and after approval.
Other Projects and Homie continue.

The initial Verifier rejection starts a correction sequence at zero.
Three subsequent rejected corrections require an operator grant.
A Verifier pass resets the count to zero.
An applied requirements revision or explicit grant also restores the Verifier allowance.
An operator packet revision and a run retry preserve the Verifier allowance.
Assigned run failures after process start use the task run failure allowance.
Dispatch and launch failures of a run with the Project lease use the shared Project infrastructure allowance.

Cancellation remains available before an application authorization exists.
An unresolved authorization requires receipt recovery first.
A receipt proves the merge completed; an absent receipt and journal prove that the authorization remains unused.
An unresolved journal of a task requires an operator decision.
An unresolved journal of a wiki commit keeps the wiki update active.
Receipt recovery repeats after each retry time, and the wiki state and System health state the repair.

## Decision Pending

A blocked task has Decision Pending.
The task records the condition type and the reason of its block.
A later block of a blocked task replaces the condition type and the reason, with these exceptions:

- A correction allowance limit and a proposal decision keep an existing condition. The task then records the allowance limit separately. The limit must end before Resolve decision with a reason.
- `mc` refuses an operator block of a task with an operator decision report.

| Condition type | Source | Resolution |
| --- | --- | --- |
| Operator decision report | The Worker, the Verifier, or the Packager. | A requirements revision, or Cancel task. |
| Customization conflict | The Worker, the Verifier, or the Packager. | An edit of the role customization, then Resolve decision with a reason. |
| Correction allowance limit | `mc`, after the third rejected correction. | Three more corrections, a requirements revision, or Cancel task. |
| Confinement refusal | `mc`, at the launch of a run. | A change of the launch configuration, then Resolve decision with a reason. |
| Operator checkout condition | `mc`, at the merge. | A repair of the operator checkout, then Resolve decision with a reason. |
| Operator block | The operator. | Resolve decision with a reason. |
| Proposal decision | The Strategist or the Arbiter. | The resolution of the Project proposal decision. |

Resolve decision with a reason keeps the description of the task.
`mc` refuses Resolve decision with a reason for an operator decision report.

A requirements revision replaces the description of the task and records a reason.
The revised description differs from the current description.
The description holds every requirement of the task.
The reason holds only the explanation of the change.
The revision waits for each affected active run and repository operation to end.
Decision Pending stays until the revision applies.
The applied revision ends an operator decision report or a correction allowance limit in the same transaction.
The applied revision keeps each other condition type.
The applied revision keeps an open merge repair and its conflict.

The next Worker brief holds the reason as context in its **Requirements revision** part.
The Verifier and Packager briefs hold the revised description without the reason.
Each brief omits the Verifier corrections and verdicts of earlier requirements versions.
The [Git delivery guide](../../GIT-DELIVERY.md) states the commits of a task after a revision.

## Agent failure controls

Each task owns three task run failure attempts.
Each Project owns three infrastructure failure attempts.
The Project shares those attempts across the dispatch and the launch of each run with the Project lease, Strategist proposal creation, and Arbiter proposal review.
A proposal run failure consumes a Project attempt before or during the run.
An assigned run failure consumes a task attempt after process start.
A wiki update run uses no failure allowance.

The first failure permits a retry after 30 seconds.
The second failure permits a retry after two minutes.
The third failure stops new runs under its allowance.
A Project infrastructure stop preserves each task's direct condition and the explicit Project pause state.
Confirmed connection conditions retain their separate connection policies.
Repository operations retain their retry schedule and control rules.

Forward stage progress restores three task attempts.
A successful terminal result of a run with the Project lease that completes the assigned work restores three Project infrastructure attempts.
A checkpoint preserves the Project infrastructure allowance because the assigned work remains incomplete.
A task resume restores three task attempts and preserves an independent operator decision.
Decision resolution clears its own block and preserves an exhausted task allowance.

The dashboard shows `blocked` as Decision Pending and `stalled` as Retries Exhausted.
The Project counts distinct unarchived tasks with either direct condition.
At three tasks, this threshold stops new agent runs with the Project lease and ordinary merges of tasks.
The threshold preserves the Project's explicit pause state and the repository exceptions.

## Proposal assignment and decisions

Strategist proposal creation belongs to the Project.
The Strategist proposes exactly two tasks.
The Arbiter selects from those two tasks.
Arbiter proposal review belongs to the Project and its complete pool snapshot.
Both proposal runs have a null task subject.
The Scribe run of a wiki update also has a null task subject.
The pool snapshot preserves the assigned proposal identifiers.
Each assigned proposal shows the shared review and retains access to the same run record.

One shared proposal decision identifies its question and affected proposals.
Each affected proposal receives a direct Decision Pending condition.
The unresolved decision stops proposal creation and proposal review.
Decision resolution clears only the blocks that belong to that decision.
The next proposal run receives the recorded operator answer.

Operator promotion, cancellation, deletion, and edits remain unavailable for Proposed tasks.
The restriction applies before and during Arbiter review.
The Arbiter retains its proposal promotion and rejection authority.

## Current state and evidence

The schema stores each mutable task fact once.
Separate records preserve evidence or authority across execution failures.

| Records | Purpose |
| --- | --- |
| `tasks` | Current stage, base commit, selected versions, candidate, correction, direct block and its condition type, and review reservation. |
| `runs`, `leases` | Run history, process start, proposal assignment, and one active agent run with the Project lease per Project. |
| `wiki_updates` | Each wiki update: its commits, its liveness, its merge facts, its result, and its cause. |
| `accepted_commits` | The accepted commit of each Project, as the host observed it. |
| `proposal_decisions`, `proposal_decision_tasks` | Shared proposal questions, affected proposals, and operator resolutions. |
| `repository_runs` | Isolated repository allocation, capture state, and cleanup state. |
| `repository_transfers`, `repository_observations` | Exact identities for capture and external observations. |
| `version_submissions`, `task_versions` | The submitted result before capture and the retained immutable version after capture. |
| `version_verdicts`, `correction_results` | Exact verification evidence and one correction result per Verifier version. |
| `merge_candidates`, `merge_operations` | Merge candidates and durable application authorizations and results of a task or of a wiki update. |
| `review_versions`, `review_decisions` | Immutable reports and operator decisions. |
| `requirements_revisions`, `requirements_evidence`, `run_requirements` | Accepted requirements, earlier evidence, and the exact requirements assigned to a run. |
| `repository_executions`, `execution_owners` | Executions of repository operations of a task or of a wiki update, their owners, retry times, and stop confirmations. |
| `vision_checks` | The Vision state of the latest start request. |
| `repository_limits` | The destination quiet interval and the temporary repository limit of each Project. |
| `activity` | Operator actions, including correction allowance grants. |

`tasks.current_review_id` selects the current report.
`tasks.review_slot_held` reserves capacity independently of the task stage.
An ordinary correction retains its slot; a requirements revision can release that slot.

Each task merges into `main`.
The verified commit derives from the selected source version.

## Code locations

| Responsibility | Location |
| --- | --- |
| Pure agent run selection. | [dispatch.go](dispatch.go), [delivery.go](delivery.go). |
| Pure wiki update decision and wiki state. | [wikiupdate.go](wikiupdate.go). |
| Pure repository operation selection and merge state. | [repository.go](repository.go). |
| Transactional authorization. | [dispatchseam.go](../verbs/dispatchseam.go). |
| The wiki round. | [wikiround.go](../verbs/wikiround.go). |
| Wiki update records and their ends. | [wikiupdate.go](../domain/wikiupdate.go), [wikicandidate.go](../domain/wikicandidate.go). |
| Terminal action of the Scribe and the checks of the wiki commit. | [wikicomplete.go](../verbs/wikicomplete.go), [completion.go](../wiki/completion.go). |
| Scope check of the merge candidate of a wiki commit. | [repositoryexecutor.go](../verbs/repositoryexecutor.go), [completion.go](../wiki/completion.go). |
| Wiki state read. | [wikistate.go](../verbs/wikistate.go). |
| System health condition of a merge of a wiki commit. | [systemstatus.go](../verbs/systemstatus.go). |
| Accepted commit read on the host. | [accepted_commit.go](../cmd/mc/accepted_commit.go). |
| Start requests, heartbeats, and failure reports. | [deliverystart.go](../verbs/deliverystart.go). |
| Repository result checks. | [delivery.go](../verbs/delivery.go). |
| Application and receipt recovery. | [repository](../repository). |
| Start events, heartbeats, and stops on the host. | [repository-lane.ts](../../resident/src/repository-lane.ts). |
| Host execution and observations. | [repository.ts](../../resident/src/repository.ts), [pipeline-liveness.ts](../../resident/src/pipeline-liveness.ts). |
| Current schema. | [schema.sql](../substrate/schema.sql), [delivery.sql](../substrate/delivery.sql). |

## Project role customization

Each Project and role has an immutable revision history in the spine.
Dispatch selects the current revision in the transaction that creates the run.
The run retains that revision and its standard Culler directive.
An empty revision leaves standard defaults in effect.

A contract conflict of a run with the Project lease ends the run with a customization conflict.
A task retains its stage and accepted work.
A proposal run without a subject blocks its role within its Project.
This applies to Strategist proposal runs and Arbiter proposal reviews.
The block preserves all failure and correction allowances and the Project pause state.
Other work follows the ordinary dispatch rules.
The operator clears the block through Homie or the dashboard.
A customization save preserves the block.

A contract conflict of a wiki update run ends the wiki update as failed and blocks no role.
