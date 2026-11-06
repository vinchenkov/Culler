# Culler failure specification

## Purpose and scope

A task can stop because it needs an operator decision or because its execution fails.
These conditions have separate names and separate effects.

Each task has an allowance of three infrastructure failures.
Forward stage progress restores that allowance.
The board pauses automatic dispatch when three distinct tasks require attention.

This specification covers pipeline runs, connection restrictions, Collie replies, and the board pause.

## Terms

- A **blocked** task needs an operator decision before an agent can continue the task.
  The agent identifies the decision and reports the reason.
- An **infrastructure failure** ends an execution attempt before the task produces its required result.
  An agent report or an observed execution failure establishes this outcome.
  The cause can remain unknown.
- A **stalled** task reaches three infrastructure failures since its last allowance reset.
  The system stops automatic retries for that task.

A code defect that an agent can correct remains part of the task workflow.
An infrastructure failure describes a failed execution attempt.
The failure can affect one task or several tasks.

Blocked and stalled describe whether a task can continue.
The task retains its stage and its accepted work.

## A blocked task

An agent reports blocked when an operator decision prevents further task progress.
For example, the task requires a change to release mechanics that the operator owns.

The report identifies the required decision and explains why the task needs it.
The task becomes blocked immediately.
The infrastructure failure count stays unchanged.
Automatic dispatch excludes the task until the operator resolves the block.

## Infrastructure failure sources

An agent can report an infrastructure failure that prevents progress on its assigned task.
The report states the observed failure and separates evidence from a suspected cause.

The system also records an infrastructure failure for these conditions:

- The container fails to start for an assigned run.
- The runner exits before the system accepts a result for the run.
- The first heartbeat fails to arrive within the allowed time.
- Subsequent heartbeats stop beyond the allowed time.
- The run reaches its maximum runtime before the system accepts a result.
- The resident confirms that the container disappeared before the system accepts a result.

A runner exit before an accepted result counts as an infrastructure failure, regardless of its exit code.
These observations establish that the run failed.
They can leave the cause unknown.

The command derives the assigned task, the proposal Project, or the wiki update from the run.
The resident supplies the run identity and the observed failure.
A cause diagnosis is optional.

## The task allowance

Each task starts with an infrastructure failure count of zero.
The third failure exhausts the allowance.

| Failure count | Required behavior |
| --- | --- |
| Zero | The task has its full allowance. |
| One | The task remains eligible for an automatic retry. |
| Two | The task remains eligible for an automatic retry. |
| Three | The task becomes stalled. Automatic retries stop for that task. |

Retry eligibility also depends on the other dispatch conditions.

A successful forward stage transition resets the failure count to zero.
For example, successful Worker completion advances the task to the Verifier and resets the count.

These events preserve the count:

- A container starts.
- A heartbeat arrives.
- The system accepts a successful result that keeps the task at the same stage.
- The task returns to an earlier stage for a correction or revision.
- The resident restarts.

## One failure per run

Each run contributes at most one infrastructure failure to its assigned task or proposal Project.
An agent report followed by a container exit contributes one failure.
Several stop conditions for the same run also contribute one failure.

The system preserves this limit across repeated observations and resident restarts.
A run with a final recorded outcome keeps that outcome after a later process exit.

These outcomes add zero infrastructure failures:

- The system accepts a successful result for the run.
- The system accepts a blocked report for the run.
- The operator deliberately cancels the run.

Each outcome of a wiki update run also adds zero infrastructure failures.

## The board pause

The board counts each distinct task that is blocked or stalled.
Each task contributes at most one to this count.
Tasks with one or two infrastructure failures contribute zero unless they are blocked.

At a count of three or more, the board pauses automatic dispatch.
The pause prevents new automatic runs of tasks and of proposals.
An active run can finish.
The operator retains access to task records and task decisions.

When the count falls below three, this failure rule permits automatic dispatch again.
Other dispatch conditions still apply.

The count represents tasks that require attention.
A shared service diagnosis requires evidence about that service.

## Credentials and quota

A system condition is a condition outside a task that stops work for more than one task.
A run failure from a system condition uses no allowance.
These run failures are system conditions:

- A network error that the runner or the resident observes.
- A provider error response that the runner or the resident observes.
- A connection failure that the resident confirms.
- A VM restart after the claim of the run.

An agent report of a credential failure or a quota failure uses the allowance.
An assigned run uses its task allowance.
A proposal run uses its Project's Strategist allowance.
A wiki update run uses no allowance.
A connection failure also prevents new dependent launches before another run exists.
While a connection check stops the next run of a task, the task names the connection.
Dispatch starts the run when the connection is available.

The dashboard names either cause when a specific service response or a direct credential check supplies evidence.
An agent report identifies the agent as the source of the diagnosis.
An unexplained runner exit retains an unknown cause.
A generic rate limit response alone leaves quota exhaustion unconfirmed.

The dashboard shows a required operator action when the evidence establishes that action.
It shows a quota reset time only when the service supplies that time.

## State and operator information

The `mc` command owns the task state and the failure count.
Agents report task outcomes through `mc`.
The resident supplies observations about run execution.
The runner can supply concrete execution facts without a cause diagnosis.

The system retains these facts for each infrastructure failure:

- The assigned task or proposal Project.
- The run identity.
- The observed condition.
- The report source.
- The failure time.
- The evidence for a known cause, when available.

The dashboard shows these facts for each affected task:

- The task stage.
- The blocked or stalled state, when applicable.
- The infrastructure failure count out of three.
- The required operator decision for a blocked task.
- The latest infrastructure failure for a stalled task.
- The connection that stops the next run, when a connection check stops it.
- The accepted work that remains available.

During a pause, the dashboard shows the combined count and identifies the tasks that contribute to it.
The dashboard uses blocked for an operator decision and stalled for an exhausted infrastructure allowance.

## Required examples

Each example starts with a zero failure count unless it states another count.
Each task starts without a block.

- **An unknown crash:** Task A's runner exits before an accepted result.
  Task A's count becomes one.
  Task A remains eligible for a retry.
- **A repeated failure:** Task A has three infrastructure failures without forward stage progress.
  Task A becomes stalled.
  Task A contributes one to the board count.
- **Forward progress:** Task A has two infrastructure failures, then successfully advances from the Worker to the Verifier.
  Task A's count becomes zero.
- **A revision failure:** A revision run has an infrastructure failure for Task A.
  Task A keeps its accepted work.
  Its infrastructure failure count increases by one.
- **A duplicate observation:** An agent reports an infrastructure failure, then its container disappears.
  The run contributes one failure to its task.
- **Three tasks require attention:** Task A is blocked.
  Task B and Task C are stalled.
  The combined count is three, so the board pauses automatic dispatch.
- **Three failures across separate tasks:** Task A, Task B, and Task C each have one infrastructure failure.
  All three tasks remain eligible for retries.
  Their failures contribute zero to the board count.

## Strategist allowance

Each Project stores a separate count of consecutive proposal failures.
The first failure permits a retry after 30 seconds.
The second failure permits a retry after two minutes.
The third failure stops automatic proposal runs until an explicit resume.
An accepted proposal result resets this count, including a result with no proposals.
The board count continues to count distinct blocked or stalled tasks.
Connection recovery preserves an exhausted Strategist allowance.

## Wiki updates

The Scribe has no task.
The agent steps of a task are the Worker, the Verifier, and the Packager.
A wiki update is one Scribe run for one Project, together with the merge of its wiki commit into the main branch.
The task allowance, the Strategist allowance, and the board pause have no effect on a wiki update.
Each condition of the section "Infrastructure failure sources" ends a wiki update run and ends the wiki update as failed.

A failed wiki update has these effects:

- It charges no allowance.
- It contributes no infrastructure failure to a task or to a Project.
- It blocks no task and no role.
- It changes no pause state and no count of tasks that require attention.
- It starts no agent run and no correction.

The next wiki update of the Project becomes due after the wiki update interval.
A failed wiki update has no retry limit.
A wiki update run and each other run share the connection state of their agent profile.
The diagnostic history retains each reported failure of a wiki update run.
The Scribe role detail on the Pipeline page of the dashboard shows the result and the cause of the last wiki update.

While Culler cannot prove the result of the merge of a wiki commit, the wiki update stays active.
Each merge step of the Project then waits for the repair of the operator checkout.
A merge step of a task that waits holds no new agent run.
System health of the dashboard states the condition and the repair.
The [dispatch guide](mc/dispatch/README.md) states each rule of that wait.

## Connection state and diagnostic history

The `connection_health` table controls connection eligibility.
The `collie_reply_attempts` table controls each reply attempt.
The `diagnostic_events` table retains observations and state transitions.
The `activity` table retains dispatch activity.
A state change and its diagnostic event commit in one transaction.

The dashboard reads current operational restrictions through `mc system status`.
A complete status response removes only conditions with verified recovery.
An incomplete response preserves known conditions and reports unavailable status.
The Configs System health panel separates deployment checks, connections, and execution.
The deployment inspection sends no provider request and preserves all retry allowances.

The [connection policy](specs/credential-failure-handling-plan.md) defines connection retry limits and recovery evidence.
The [technical design](specs/credential-failure-data-flow.md) defines process responsibilities and persistence.
