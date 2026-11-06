---
type: workflows
title: Git delivery and repository operations
description: How Culler gives each code run an isolated repository, captures exactly one commit per task, builds a merge candidate, and applies it to the operator checkout.
tags: [git, delivery, repository, resident, mc-land]
sources:
  - id: openwiki-source-8cf383f17a2b2b2173d39ad2
    resource: repo://mc/repository/apply.go
  - id: openwiki-source-f083eee884c6d921088122d9
    resource: repo://mc/repository/git.go
  - id: openwiki-source-301f4dcdb036a6a700293c7a
    resource: repo://mc/verbs/decisionresolution_test.go
  - id: openwiki-source-47e2ca52aab2f12bfb519d6d
    resource: repo://mc/verbs/deliverycomplete.go
  - id: openwiki-source-fff91d356626229649429b8c
    resource: repo://mc/verbs/deliverystart.go
  - id: openwiki-source-81f2f4b1e598b24bdff78b73
    resource: repo://mc/verbs/repositoryexecutor.go
  - id: openwiki-source-e028a0037f071b894557c29d
    resource: repo://resident/src/effects.ts
  - id: openwiki-source-b46e02202a0bd3cd7611b6cb
    resource: repo://resident/src/git-identity.ts
  - id: openwiki-source-d3a126975cc118409c3aa9e0
    resource: repo://resident/src/repository-lane.test.ts
  - id: openwiki-source-ff8e03b40aacdccc4e669987
    resource: repo://resident/src/repository-lane.ts
  - id: openwiki-source-63b7e6511308c96efab1eed9
    resource: repo://resident/src/repository.ts
  - id: openwiki-source-876efa27a35b34c8f8651db4
    resource: repo://runner/image/mc-land
generated: { by: "culler-scribe", at: "2026-10-21T23:30:26.761Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-10-21T23:30:26.761Z
---

# Git delivery and repository operations

Every Culler task delivers one Git commit. The delivery path has three owners. `mc` decides which repository operation is due and records each result in the spine. The resident runs the host work of each operation on its repository lane. The native executor (`mc __repository`, in `mc/verbs/repositoryexecutor.go` and the `mc/repository` package) performs the actual Git work, either on the host or inside a confined merge container started through `mc-land`.

`GIT-DELIVERY.md` at the repository root states the intended design: tasks, approvals, the quiet interval, artifact expiry, and recovery. This page explains what the code does and marks where it relies on that document.

## Repository operations

`mc/dispatch/repository.go` names the operations: `capture`, `cleanup`, `observe`, `prepare`, `apply`, and `reconcile`. `observe`, `prepare`, `apply`, and `reconcile` are merge steps. The native executor also accepts internal sub-operations that the resident sends: `allocate`, `ready`, `prepare-repository`, and `retain-candidate`.

A repository operation claims no Project lease (`GIT-DELIVERY.md`; also the header comment of `resident/src/repository-lane.ts`). The tick loop only spawns agent runs. The repository lane runs operations beside it, so a long merge never blocks dispatch and an agent run holding the lease never blocks a merge. `TestDeliveryStartWhileAgentRunHoldsLease` in `mc/verbs/deliverystart_test.go` covers this.

## Per-run isolated repositories

When a spawn effect carries a repository plan, `spawn` in `resident/src/effects.ts` calls `prepareRepositoryRun` (`resident/src/repository.ts`) before it creates the container. That function sends two native calls:

1. `allocate` runs under a process-wide allocation lock (`withAllocationLock`), so two size checks cannot pass together. The executor estimates the required bytes, checks free disk space, and checks the Project's temporary limit across every folder under `MC_HOME/runs/repositories/`. It then retains the input commit under `refs/mc/tasks/<task>/inputs/<run>` in the operator repository. The resident records the result with `mc delivery record --operation ready`, so the database holds the selected input before the copy starts.
2. `ready` builds the copy. `repository.Isolated` (`mc/repository/git.go`) runs `git init` plus `git fetch` from the source repository. It does not use `git clone`, because a local clone can create hard links. It refuses a result that contains `objects/info/alternates`, runs `fsck --connectivity-only`, and checks out the input commit detached. The executor then creates the local branch `mc/task-<task>` and makes the run folder writable for the sandbox user.

The copy lives at `MC_HOME/runs/repositories/<run_id>/repo` (`repositoryRunRoot`). `effects.ts` binds it at `/workspace/source`. A Scribe gets the repository read-only, with only `openwiki/` and `.git` writable. The launch envelope carries only `repository.input_commit`. The host workspace path stays out of the container. `TestIndependentRepositoriesHaveNoSharedObjectFiles` in `mc/repository/repository_test.go` verifies that the copy shares no object files with the source.

Every Git child process runs through `Git.process`, which fixes a hardened environment: no system or global config, hooks at `/dev/null`, no fsmonitor, `protocol.allow=never` except `file`, fsck on transfer, and `fsync` for objects and references. Project Git configuration therefore cannot run code during delivery.

## One commit per task

The Worker submits with `mc complete`.
For a pipeline run with a repository, `Complete` routes to `completeDelivery` in `mc/verbs/deliverycomplete.go`.
It reads the assigned input and the original task base separately.
The tree must be clean, the submitted commit must equal `HEAD`, and the input must remain an ancestor.
The submission must differ from the task base.
A Worker can submit its unchanged start commit when that commit differs from the base and satisfies the task description.
The same base check applies to checkpoints.
`TestDeliveryWorkerCompletionRequiresANewCommit` in `mc/verbs/deliveryonecommit_test.go` checks refusal at the base and acceptance beyond it.

`recordRepositoryReady` initializes `base_commit` once.
A requirements revision preserves that base and resumes the checkpoint or last submitted work.
The Verifier receives the original base, so its judgment covers the full task change.
`TestReportRevisionResolvesTheDecisionAndKeepsTheLastCommit` checks unchanged resubmission through capture and a new verdict.
See [decision resolution](decision-resolution.md) for the requirements transaction.

After the producer container stops, a `capture` operation imports the result into the operator repository as an immutable reference: `refs/mc/tasks/<task>/versions/<run>`, or `.../checkpoints/<run>` for `mc complete --checkpoint`. `Git.Retain` only fetches into `refs/mc/`. It leaves accepted branches and operator files unchanged, and a repeat with the same commit is a no-op, so a database failure permits an identical retry. A later `cleanup` removes the run folder.

## The start protocol: delivery start, heartbeat, record, fail

The resident drives operations through host-scope verbs in `mc/verbs/deliverystart.go`. Each verb calls `RequireHostScope`.

- `mc delivery register` returns a new owner number for this resident instance (`DeliveryRegister`). The lane registers once at start and retries every 5 seconds on failure.
- `mc delivery start --request -` (`DeliveryStart`) runs in one transaction. It checks that the caller is the current owner, confirms stops, sweeps executions that exceeded a liveness limit, publishes due review packets, and calls `dispatch.SelectStarts` to record each due operation as an active execution. It returns all active executions of the owner, stop requests, start failures, and `due_in_ms`, the next time an operation becomes due (at least 1 second).
- `mc delivery heartbeat --request -` (`DeliveryHeartbeat`) renews the named running executions. It also lists `unstarted` executions the resident does not hold and `container_stops`: ended agent runs whose containers are still present after `domain.ContainerGrace` (60 seconds).
- `mc delivery record --operation <op> --run <id> --result -` stores the native result. `mc delivery fail --run <id> --condition <text> [--container-present]` ends an execution whose host work failed. With `--container-present` the end carries no charge.

`resident/src/repository-lane.ts` owns this loop. `request(event)` coalesces events: an event during a start request causes exactly one more request. Start events include resident start, container exit (from a `docker events` follower), execution end, tick end, packet decision (`packet-decide` on the nudge socket), Collie turn end, due time, and artifact expiry end. `HEARTBEAT_INTERVAL_MS = 20_000` renews every held execution in one heartbeat. A response that reports a stale owner makes the lane call `exit`, which shuts the resident down. A new owner number therefore fences out an older resident instance.

`runExecution` (`resident/src/repository.ts`) runs one execution. Capture and cleanup first confirm that the ended run's containers are absent. Inside the grace window a present container ends the execution with `--container-present`; after the grace the resident stops and removes it. Failure reports retry 3 times, 5 seconds apart. If all of them fail, the execution ends through the `mc` heartbeat limit.

## Merge candidate and merge: mc-land

`runner/image/mc-land` is a short shell script supplied by the private runtime. It accepts `prepare` or `apply` and an envelope file, and runs `exec mc __repository <file>`. It never opens the database. The resident starts it through `mergeContainer` in `resident/src/repository.ts` with `docker run --rm --network none`, user `10002:10002`, `--cap-drop ALL`, `no-new-privileges`, one CPU, 1,024 MiB memory, and a limit of 128 processes. A stop request repeats `docker stop` until the container run ends.

`mc` inside the image is `runner/image/mc_dispatch.go`, a wrapper that execs the setuid `/opt/culler-runtime/libexec/mc-real`. That binary fixes identity reads to `/mc/run.json` so a process cannot redirect its authorization through the environment.

- **prepare.** The host runs `allocate` and `prepare-repository` to build an isolated repository for the merge run. The container mounts only that run folder, never the operator checkout. `repository.Prepare` (`mc/repository/prepare.go`) checks out the destination commit and either fast-forwards to the submitted commit or runs `git merge --no-ff --no-verify --no-gpg-sign` with the configured author. A conflict returns `status: "conflict"` with the conflicting paths, and the task goes back to the Worker. A merged candidate must have both inputs as ancestors. The host then runs `retain-candidate`: it retains `refs/mc/tasks/<task>/candidates/<run>`, runs `repository.Preflight` (a dry two-way merge into the operator checkout that changes no file), and stores the change data for review.
- **apply.** The container mounts the operator workspace at `/workspace/source`. `repository.Apply` (`mc/repository/apply.go`) checks the authorization format, refuses if the live destination moved (`ErrDestinationChanged`), reserves `.git/index.lock` exclusively, writes a durable journal under `.git/mc/applications/<authorization>.json`, runs `git read-tree -u -m <destination> <candidate>`, conditionally updates the branch, and then writes the receipt `refs/mc/applications/<authorization>`. Local operator changes on other paths stay in place. A Git refusal or an operation in progress returns `operator-attention` with a message that names the path and the action.
- **reconcile.** After a crash, `ReconcileApplication` uses the journal and receipt to finish the original record. It does not apply the change a second time.

## Registered Git controls

`mc/verbs/gitregistry.go` is not part of delivery execution. It feeds the mount boundary. `resolveProjectGitControls` resolves the real Git control directories of a registered Project at every mount attest (`mc/verbs/mountattest.go`): a `.git` directory, a bare repository root, or a linked-worktree pointer with its `commondir`. It refuses symlinked, oversized, unparsable, or dangling shapes. `probeRepoObjectFormat` reads `extensions.objectFormat` from the config files, without running the host's Git. These protected identities keep a mount grant from exposing Git internals.

## Git identity

`configuredGitEnvironment` (`resident/src/git-identity.ts`) validates the configured author name and email: non-empty, an `@` in the email, at most 256 characters, and no NUL, CR, or LF. It returns `GIT_AUTHOR_*` and `GIT_COMMITTER_*` Docker environment flags. `main.ts` calls it at boot and exits with code 2 on an unsafe identity. `spawn` calls it per run, so an effect can carry a per-Project `git_identity_override`. A missing override decision refuses the spawn. Merge preparation uses the same configured identity as `author_name`/`author_email` in the envelope.

## Limits and configuration

`mc delivery limits --project <id>` sets `destination_quiet_seconds` and `max_temporary_bytes` (`ConfigureRepositoryLimits`, `mc/verbs/delivery.go`). A zero value keeps the current value. `GIT-DELIVERY.md` states the defaults: 20 GiB and a 30-second quiet interval.

## Tests

- `mc/repository/repository_test.go`, `process_test.go`, `reconcile_test.go`: real scratch repositories. They cover exact versions across main movement, conflicts, destination races, local changes, and recovery at each durable boundary, including killing the process mid-apply.
- `mc/verbs/delivery_test.go`, `deliverybehavior_test.go`, `deliverystart_test.go`, `deliveryrecovery_test.go`, `deliveryonecommit_test.go`: the spine rules for start, heartbeat, charges, owners, approvals, and tasks.
- `mc/e2e/` (`docker_e2e` tag): the suite runs the real resident executor and containers.
- `resident/src/repository.test.ts`: the operation order, mounts, the absence of the operator path in prepare, stop handling, and grace behavior.
- `resident/src/repository-lane.test.ts`: event coalescing, the 20-second heartbeat during a long tick, stop lists, owner refusal, and the install pause.

Related: [dispatch and tick loop](dispatch-and-tick-loop.md), [task pipeline](task-pipeline.md), [resident](../architecture/resident.md), [runners](../architecture/runners.md), [failure controls](../concepts/failure-controls.md).

## Wiki delivery and environment association

Repository operations identify either a task or a wiki update.
Wiki repositories use `MC_HOME/runs/wiki-repositories/<run_id>/repo`.
Their retained references use `refs/mc/wiki/<kind>/<run_id>`.
The executor applies the temporary disk limit separately to the task and wiki repository folders.

A wiki merge candidate must preserve its destination outside `openwiki/`.
`wikiCandidateCheck` validates that candidate in an isolated repository outside agent control.
A conflict ends the wiki update; it starts no task correction.
A task conflict instead starts a Worker merge repair for each conflict.
The task repair clears its approval and returns it to Seeded.

A task version can carry a candidate Project environment.
Source acceptance associates the checked source with its immutable environment revision.
An interrupted association holds new work until receipt recovery resolves it.
That use preserves the Project's global image selection.
See [Project environments](project-environments.md) and [wiki updates](../integrations/openwiki-and-viewers.md).
