# Git delivery

Each task produces one operator packet.
Each task requires operator approval before its commit reaches main.

## Configuration

Every task uses the repository delivery protocol.
Project setup supplies the default repository limits.
The current database schema includes the delivery tables.

The following command surface belongs to the coding harness and resident.
The operator uses the dashboard and their coding harness.

| Command | Purpose |
| --- | --- |
| `mc delivery limits --project <id> [--quiet-seconds <n>] [--max-temporary-bytes <n>]` | Records the repository limits of a Project. |
| `mc delivery allow-corrections --task <id> --reason <text>` | Grants three additional correction attempts after exhaustion. |
| `mc delivery register` | Records the owner number of a new resident instance. The resident calls it at start. |
| `mc delivery start --request -` | Records each due repository operation as an execution and lists the active executions and stops. The resident calls it at each start event. |
| `mc delivery heartbeat --request -` | Renews the active executions of the resident and lists the stops. The resident calls it every 20 seconds. |
| `mc delivery fail --run <id> --condition <text> [--container-present]` | Ends an execution with a failure after its host work stopped. The resident calls it. |
| `mc delivery record --operation <name> --run <id> --result -` | Records the result of an execution from standard input. The resident calls it. |

The Project lease permits one agent run.
The Scribe run of a wiki update claims no lease.
Repository operations claim no lease.
Each Project allocation enforces its temporary limit across all run folders of tasks.
That limit defaults to 20 gibibytes.
The limits command accepts `--max-temporary-bytes` and `--quiet-seconds`.
An omitted limit preserves its current value.
The native executor checks the configured limit and available disk space before a repository copy.
Incomplete cleanup contributes to that limit.
Merge containers use one processor, 1,024 mebibytes of memory, and at most 128 processes.
Merge containers receive no network access.

## Versions, merges, and approval

Each code role receives a new independent repository at `/workspace/source`.
The repository contains the assigned commits and their required history.
Its objects have no hard links or alternates to another repository.
The container mount exposes that repository without another copy.

The resident records the selected input before the copy starts.
The base commit of a task is the commit where its first Worker run started.
Each requirements revision preserves the base commit.
A Worker run starts at the first of these commits that exists:

1. The checkpoint commit.
2. The last submitted commit.
3. The base commit.

The Worker submits an exact committed result.
A Worker submission and a checkpoint differ from the base commit.
A Worker can submit its start commit when that commit differs from the base commit.
The Verifier judges the change from the base commit to the submitted commit against the current description.
The native executor imports that result and creates an immutable retained reference before the database accepts the version.
The Verifier assesses that exact Worker version.
A Verifier pass advances the task to the Packager.
The submitted version holds the commit that the Verifier passed.
The Packager receives the complete submitted commit, even when main advances.

The `mc complete --checkpoint` terminal preserves an exact Worker checkpoint.
It leaves the task seeded.
The next Worker receives that checkpoint after capture and cleanup.
Unsubmitted file edits have no durability guarantee.

After the Packager finishes, `mc-land prepare` merges the submitted version with the recorded destination.
A clean Git merge produces the candidate commit, and the task goes to review.
A conflict returns the task to the Worker for a merge repair.
The native executor retains the merged candidate and checks destination access before review.
It also runs the merge into the operator checkout as a dry run that changes no file.
The review record separates the Packager report from the candidate facts.
The report hash protects the displayed report from later edits.

Approval identifies an immutable packet and submitted version.
The dashboard sends both identifiers with the decision.
It enables the decision controls only after the selected report loads.
A requirements revision or new submitted version invalidates the previous approval.

If the destination advances, the task waits for a quiet interval before another preparation.
The interval defaults to 30 seconds.
A new candidate keeps the approval of the same submitted version when its merge succeeds.
A conflict clears the approval and returns the task to the Worker.
Worker edits require another Verifier assessment, another report, and another approval for a main destination.

## Project knowledge and task artifacts

The Worker commits complete reports and other knowledge work directly at their authoritative paths.
A new document follows the established convention for that document type.
The default location is `openwiki/` when the project supplies no clear convention.
A document revision preserves its path unless the task authorizes a move.
Imported documents retain their established formats.

The Scribe maintains the control records, the Claim sidecars, the generated pages, and the navigation pages in wiki updates.
The [OpenWiki integration guide](runner/openwiki/README.md) defines these wiki files.

The Scribe maintains wiki display metadata before generated page work.
It preserves original content and attribution.
Generated explanations cite original documents directly, including original documents under `openwiki/`.
A project document is an original document under `openwiki/` whose front matter declares `document: true`.
The next wiki update after the merge of a task adds each new project document of that task to the navigation pages.

The `.mc-artifacts/` folder stores operational records across runs and reviews.
The repository stores complete deliverables and their necessary local evidence.
Artifacts expire 90 days after task closure and the end of active dependencies.
Active reviews retain their artifacts.
Task history identifies expiry and retains task decisions.
The accepted project remains usable after artifact expiry.

## Wiki updates

The Scribe has no task.
A wiki update is one Scribe run for one Project, together with the merge of its wiki commit into `main`.
The run starts at the accepted commit: the commit at the `main` branch of the Project repository.
The start commit is the accepted commit at the time that `mc` creates the run.

The Scribe run receives an independent repository at `/workspace/source`.
The host keeps that repository below `MC_HOME/runs/wiki-repositories`.
The local branch of that repository is `mc/wiki-update`.
Only the `openwiki/` folder and the Git folder of that repository accept writes.
The destination commit of that repository is the start commit, and the reference `refs/mc/destination` names it.
The repository holds no later commit of `main`.

The native executor retains each commit of a wiki update under one reference in the Project repository:

| Reference | Commit |
| --- | --- |
| `refs/mc/wiki/inputs/<run_id>` | The commit at which the temporary repository of the run starts. |
| `refs/mc/wiki/commits/<run_id>` | The wiki commit that the Scribe submits. |
| `refs/mc/wiki/candidates/<run_id>` | The merge candidate of the wiki commit. |

Each reference stays after a failed wiki update.
The temporary repositories of tasks and the temporary repositories of wiki updates use separate folders.
The temporary disk limit applies to each folder.

Culler merges each accepted wiki commit into `main` with no review packet and no operator decision.
The merge uses capture, the merge candidate step, the merge, and receipt recovery, as the merge of a task does.
One merge lane serves each Project, so a merge step of a task and a merge step of a wiki commit run at different times.
A due merge step of a task starts before a merge step of a wiki commit.
An explicit pause holds the merge candidate step and the merge of a wiki commit until the Project is active.
The merge of a wiki commit leaves each review decision valid.

The merge candidate step applies the scope check to the merge candidate of a wiki commit.
The scope check compares the merge candidate with its destination commit in the temporary repository of that step.
No agent writes that repository.
The [OpenWiki integration guide](runner/openwiki/README.md) states the rules of the scope check.
Culler retains a merge candidate only after it passes the scope check.
The merge of a wiki commit thus changes no path of `main` outside `openwiki/`.

These conditions end a wiki update as failed, and `main` stays unchanged:

- A conflict in the merge candidate step. Culler discards the wiki commit.
- A `main` branch that does not hold the start commit. Culler discards the wiki commit.
- A merge candidate that fails the scope check. Culler discards the wiki commit.
- A Git refusal in the operator checkout.

While receipt recovery cannot prove the result of the merge of a wiki commit, the wiki update stays active.
Each merge step of the Project then waits for the repair of the operator checkout.
A merge step of a task that waits holds no new agent run, so new agent runs of the Project continue.
An approved task keeps the Project priority, and unrelated work of the Project waits.
On the dashboard, System health and the role detail of the Scribe on the Pipeline page state the repair.

A failed wiki update blocks no task and starts no correction.
A repository operation of a wiki update charges no allowance.
The next wiki update becomes due after the wiki update interval.
The [dispatch guide](mc/dispatch/README.md) states each rule of a wiki update.

## Failure and recovery

The Worker resolves a conflict under the same task identity when one commit can keep the intent of both changes.
The repair preserves both required contributions.
Contradictory changes require an operator decision, and the task blocks.
The operator records the decision in the description through a requirements revision.
The revision keeps the open merge repair and its conflict for the next Worker run.
Each conflict starts a merge repair.
Three completed Verifier correction attempts exhaust the automatic allowance.
A Verifier pass alone leaves the correction open until the corrected version merges without a conflict.
Infrastructure failures use the separate infrastructure allowance.
Repository setup and destination observations also use that allowance.

Only distinct blocked or stalled tasks contribute to the board pause count.

The apply operation executes no project commands and ignores project Git configuration.
It reserves the destination reference and the operator checkout index before an update.
It uses the Git two-way merge from the destination commit to the candidate commit.
The merge proceeds whenever Git accepts the local changes in the operator checkout.
Local changes stay in place, and a staged change stays staged.
A Git refusal blocks the task at once, before and after approval.
A Git operation in progress, such as a rebase, also blocks the task.
The block message names each path and the operator action.
The operator commits, stashes, or discards each change, then uses Resolve decision with a reason.

A durable journal precedes the branch update.
An immutable receipt follows the branch update.
Recovery can finish the original record after a process crash between those writes.
The receipt identifies the exact original candidate and authorization.
Recovery accepts a partial checkout only when each changed path matches the destination version or the candidate version.
Another local change to such a path blocks the task.
Git reference locks from an external or unidentifiable process also require a decision.

The merge of a wiki commit uses the same apply operation and the same recovery.
A wiki commit has no task, so these conditions block no task.
The section "Wiki updates" states their results for a wiki commit.

The resident confirms that the producer container stopped before capture or cleanup.
After a grace of 60 seconds, the resident stops and removes each container of an ended agent run.
It preserves a run folder until required commits and reports survive outside that folder.
Temporary cleanup failures receive a retry delay and remain visible in the task details.
A successful update remains successful when checkout cleanup requires attention.

## Verification

The fast Go tests exercise database rules and real scratch Git repositories.
They cover exact versions, stale approvals, correction counts, storage limits, and interrupted copies.
Reproducible event sequences vary main updates and duplicate reports.
Each failed sequence records its seed and event order.
The process crash tests terminate the executable at acknowledged durable boundaries.
They verify recovery for main.

The separate container suite checks task execution through the real resident and runners.
The suite uses isolated test data.

```sh
cd mc
mise exec -- go test -tags docker_e2e -v -timeout 30m ./e2e/...
```

The storage test reports preparation time and temporary repository bytes for its fixture.
Large project repositories require representative measurements before deployment.
The scripted tests establish deterministic execution behavior.
They do not measure real-agent repair quality.
