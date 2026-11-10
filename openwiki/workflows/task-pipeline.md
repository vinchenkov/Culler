---
type: workflows
title: The task pipeline roles
description: How task roles produce a verified source version and review packet, with wiki updates outside the task pipeline.
tags: [pipeline, roles, task-status, verdicts, review-packet]
sources:
  - id: openwiki-source-b578a24182c530b5d8630c21
    resource: repo://mc/dispatch/dispatch.go
  - id: openwiki-source-636e77a0ecbade42aa7a7799
    resource: repo://mc/domain/allowances.go
  - id: openwiki-source-a6c0c3cb67f430bbbf6ef144
    resource: repo://mc/domain/delivery.go
  - id: openwiki-source-833cfcb1ce94b8d49fc966fd
    resource: repo://mc/domain/requirements.go
  - id: openwiki-source-e876d4f934fe1b8e2de3721c
    resource: repo://mc/substrate/schema.sql
  - id: openwiki-source-255135c85fe9ddb288212bbf
    resource: repo://mc/verbs/artifactsnapshot.go
  - id: openwiki-source-7f547d2c5de5589b3c27e6a4
    resource: repo://mc/verbs/brief.go
  - id: openwiki-source-0cffee2e42bab2a231ca898e
    resource: repo://mc/verbs/complete.go
  - id: openwiki-source-301f4dcdb036a6a700293c7a
    resource: repo://mc/verbs/decisionresolution_test.go
  - id: openwiki-source-74d6ad23a2a8895d737d9013
    resource: repo://mc/verbs/delivery.go
  - id: openwiki-source-bdcdf989ab8381c989787604
    resource: repo://mc/verbs/directives_test.go
  - id: openwiki-source-3bc5b077708f9534abc3b95d
    resource: repo://mc/verbs/directives/arbiter.md
  - id: openwiki-source-959768f661d526f65eea9d8d
    resource: repo://mc/verbs/directives/packager.md
  - id: openwiki-source-72313a4f79741897a46d7a11
    resource: repo://mc/verbs/directives/strategist.md
  - id: openwiki-source-db55b76d7f0573ce8b068ebf
    resource: repo://mc/verbs/directives/verifier.md
  - id: openwiki-source-6a1e391719e8dc303420ffc3
    resource: repo://mc/verbs/dispatchverb_test.go
  - id: openwiki-source-f1ae658c98ff5e2d177d4b8a
    resource: repo://mc/verbs/packet.go
  - id: openwiki-source-19fe7d061285a802d1068aca
    resource: repo://mc/verbs/roles.go
  - id: openwiki-source-04cae9da859027e703632c1b
    resource: repo://mc/verbs/skills/shared/project-environment-setup/SKILL.md
  - id: openwiki-source-2244ea442a4c07aeb880c8c8
    resource: repo://mc/verbs/skills/strategist/mc-strategist/SKILL.md
  - id: openwiki-source-0097b0a41bffd53dce10abf3
    resource: repo://mc/verbs/skills/verifier/mc-verifier/SKILL.md
  - id: openwiki-source-f1f8658bc93aa1e51c399e87
    resource: repo://mc/verbs/skills/worker/mc-worker/SKILL.md
  - id: openwiki-source-4565b2c94892e8cea0c22610
    resource: repo://mc/verbs/strategist_input_test.go
  - id: openwiki-source-1ea8a5db4f70db6483d19a8a
    resource: repo://openwiki/operations/worker-instruction-audit.md
generated: { by: "culler-scribe", at: "2026-11-09T23:41:46.402Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-09T23:41:46.402Z
---

# The task pipeline roles

Culler turns a Project's goals into reviewed commits through a fixed chain of agent roles. Each role runs in a fresh container, reads only its [agent input](../concepts/agent-input.md), and ends its run with exactly one `mc` terminal command. The `tasks` table in `mc/substrate/schema.sql` holds the stage of every unit of work, and SQLite triggers make illegal stage moves fail. `mc dispatch` decides which role runs next for a Project (see [the dispatch and tick loop](dispatch-and-tick-loop.md)); this page explains what each role does and what record it changes.

## The roles

The role names live in `mc/dispatch/dispatch.go` as `dispatch.Role` constants.
The Strategist proposes tasks. The Go constant is `dispatch.RoleStrategist`.
Its wire identifier remains `strategist(propose)` for compatibility with stored records and launch envelopes.

| Role | Wire identifier | Input | Terminal |
| --- | --- | --- | --- |
| Strategist | `strategist(propose)` | Project vision, active task titles, rejected and cancelled history | `mc strategist propose --batch -` |
| Arbiter | `arbiter` | The whole proposed pool, active descriptions, rejected and cancelled history | `mc arbiter decide --batch -` |
| Worker | `worker` | One task, its isolated repository | `mc complete --status worked --commit <sha>` or `--checkpoint` |
| Verifier | `verifier` | One submitted commit | `mc verifier verdict --outcome pass|correct` |
| Packager | `packager` | The verified task | `mc complete --status packaged --outputs --thesis` |

The command lists come from each role's command surface skill under `mc/verbs/skills/<role>/mc-<role>/SKILL.md`. Every role also has two exit terminals: `mc complete --needs-operator --reason` records an operator decision and blocks the task, and `mc complete --infra --reason` reports an infrastructure failure (see [failure controls](../concepts/failure-controls.md)).

The lease owner stores the flat names Strategist, Arbiter, Worker, Verifier, and Packager. Scribe runs have no task and hold no lease. `ArbiterDecide` in `mc/verbs/roles.go` checks its exact role with `requireExactRole`.

## Task statuses and legal transitions

A task row has one of five statuses: `proposed`, `seeded`, `worked`, `verified`, `packaged`. The status is separate from three other facts on the row: `blocked` (Decision Pending), `stalled` (retries exhausted, generated from `dispatch_retries = 0`), and `decision` (`approved`, `rejected`, `cancelled`). A block preserves the task stage.
A reason-only resolution preserves that stage; an applied requirements revision returns the task to Seeded.

The `tasks_legal_transitions` trigger permits only these edges:

- `proposed → seeded`: the Arbiter promotes a proposal.
- `seeded → worked`: Culler captures a Worker submission.
- `worked → verified`: a Verifier pass.
- `worked → seeded`: a Verifier correction.
- `verified → seeded`: only with an accepted requirements revision (`requirements_version` rises by one and a pending revision exists).
- `verified → packaged`: the Packager terminal.
- `packaged → seeded`: an operator revision or a merge-conflict correction.

Birth rules in `tasks_birth_rules` add two constraints. Agent proposals start `proposed`; operator work (`origin = 'user'`) may start `seeded`. Other triggers make archived rows terminal, forbid un-archiving, freeze task identity columns, and forbid deleting any task row (`tasks_no_delete`). Rejected rows stay as the record of what the Arbiter turned down.

Forward stage progress resets the task's run failure allowance: `tasks_forward_progress_restores_infrastructure_allowance` sets `dispatch_retries` back to 3 whenever the status rank rises.

## Which role runs for which status

`spawnFor` in `mc/dispatch/dispatch.go` maps the selected row to a role:

- `proposed` → Arbiter, with the entire proposed pool as its subject set and no single task subject.
- `seeded` task → Worker.
- `worked` → Verifier.
- `verified` → Packager.

`packaged` rows rank 0 in the generated `stage_rank` column and leave pipeline dispatch.
They wait in the operator's review queue.
`lessNextDispatch` selects expedited tasks (`priority = -1`) first.
It then orders tasks by stage rank, priority, age, and identifier.

`decideAgentRun` starts the Strategist when no task can proceed, no proposal decision awaits an answer, and the role is available.
The Strategist and Arbiter briefs include compact rejected and cancelled task history from their Project.
The review cap (`settings.review_wip_cap`, default 3) stops ordinary work when the Project review queue is full.
Revisions of packaged work can still proceed.

## Rejected and cancelled work

The brief selects up to 10 cancelled tasks, then fills unused places with rejected tasks, within a total limit of 20.
Each group orders decisions from newest to oldest and uses larger task identifiers first for equal times.
Entries contain identifiers, titles, and decisions, plus available cancellation reason excerpts.
See [agent input](../concepts/agent-input.md#historical-tasks-in-proposal-briefs) for excerpt limits and source selection.

The Strategist directive forbids proposals that repeat repository work, active tasks, rejected work, or cancelled work.
A different title for equivalent work still counts as repeated work.
The Arbiter compares proposals with both historical parts and treats equivalent work as a rejection reason under its batch rules.
Both roles use cancellation reasons as context for the operator's preference.
They can retrieve historical task details through their permitted read commands when scope is unclear.

Historical tasks do not qualify as existing work for `duplicate_of`.
The Arbiter uses its operator decision procedure when the all-reject rules prevent a permitted verdict.
These are role instructions; historical context does not prove that an agent follows them.
`mc/verbs/directives_test.go` checks the detailed history instructions for the Arbiter.
`mc/verbs/strategist_input_test.go` checks the Strategist duplicate rules, task read, and use of cancellation reasons across its delivered input.
`mc/verbs/dispatchverb_test.go` checks historical entries in dispatched briefs and isolation between Projects.

## Strategist research and submission

The Strategist uses repository evidence, the accepted Vision, and relevant operator resolutions to propose work within its Project.
It selects candidates with the best reward for the cost.
Only the Arbiter selects proposals for development; the Strategist cannot rank proposals, implement them, start runs, or decide review packets.

The Strategist starts with an important result that the Project cannot yet deliver well.
It traces the user's route through the repository and identifies the step that prevents success or causes substantial effort.
The research considers new capabilities as well as defects.
Before another implementation, the Strategist checks whether the step remains necessary and whether an existing capability solves the problem.

The directive requires evidence for the problem and the proposed benefit.
For repeated work, the proposal identifies who repeats it and where it occurs.
For a shared fix, it identifies the workflows that benefit.
For a prerequisite, it identifies the later capability and the work still required.
The Strategist considers the result, feasible benefit, and full cost when it selects candidates.

Research investigates assumptions that can make a task unnecessary or ineffective.
It stops when the benefit and a feasible task are clear, unless another fact can change the selection.
The Strategist and its subagents can use internet research when it helps the Project goal.

The `Research dependencies` procedure in `mc-strategist` applies before research uses Project dependencies.
It requires compatibility checks against current declarations and the selected image because the run omits automatic dependency preparation.
The shared environment skill applies within the Strategist's authority.
See [Strategist input](../concepts/agent-input.md#strategist-instructions-and-procedures) for procedure locations and delivery.

The directive requires two task proposals together.
One task can span several components and delivers one Git commit.
Each description states these points:

- The present problem or absent capability, with repository evidence.
- The useful result or repeated cost that the task removes.
- The reason the result matters to the Project now.
- The substantial effort or risk through verification and maintenance.
- The scope and success criteria that agents can check in their container.

The command skill permits an empty `proposals` array when no valid task remains.
The Strategist reads that skill before each `mc` call.

`mc strategist propose --run <run_id> --batch -` receives one JavaScript Object Notation (JSON) object on standard input.
Its only field is `proposals`; extra fields and trailing text cause rejection.
Each proposal supplies a title and can supply a description, Project identifier, task scope, and priority.
The command schema permits an absent description, but the directive requires descriptions with scope and criteria.
`StrategistPropose` in `mc/verbs/roles.go` checks role, run identity, the subjectless lease, and the limit of two tasks.
It confines each proposal to the run's Project.
One transaction creates all proposals, ends the run, and releases its lease.
A rejected batch writes nothing; the Strategist can correct the rejection and retry.

The Strategist completes proposal work after a failure when available results permit it.
When an execution condition prevents completion, its command skill supplies the infrastructure failure terminal.
When completion requires an operator decision, it supplies the question and the reason that completion depends on the answer.
A customization conflict uses the separate conflict terminal.
The directive requires exactly one permitted terminal action, including after an execution failure.
See [failure controls](../concepts/failure-controls.md) for the state effects of these reports.

## Checks required for a proposal

The Strategist must name a check for each success criterion.
The Worker and Verifier must be able to run that check in an agent container.
The Arbiter must require this property when it evaluates each proposal.
Each criterion describes behavior within the task scope.
Whole-suite success requirements, such as `./mc/check.sh passes`, are invalid criteria.
An available suite command does not prove that its tests can run in the container.
New or changed tests must pass on the submitted commit.
Broad regression checks belong to the Verifier duties for every task.
An unchanged Project failure alone permits a proposal that satisfies these rules.

The role directives exclude checks that need these capabilities:

- Host access.
- A Docker daemon.
- macOS system interfaces.
- Additional container privileges.
- A run on a hosted continuous integration (CI) service, such as GitHub Actions.

These are proposal requirements from `mc/verbs/directives/strategist.md` and `mc/verbs/directives/arbiter.md`.
They constrain the criteria before task execution; actual check results remain part of the Worker and Verifier evidence.
See [agent input](../concepts/agent-input.md#proposal-criteria-in-the-role-directives) for how the directives carry these requirements.

## Invalid criteria and all-reject batches

`ArbiterDecide` in `mc/verbs/roles.go` accepts `rejection_category` only on a reject decision.
Its only accepted value is `invalid_criteria`.
A rejection requires a reason with non-whitespace text.
`rejection_category` and `duplicate_of` are mutually exclusive on one verdict.
The Arbiter identifies the invalid criterion and violated rule; the command validates the declaration rather than the criterion semantics.

The command checks the complete snapshotted pool and applies the batch in one transaction.
For an ordinary all-reject batch, it counts other ready tasks in the same Project.
Ready tasks are unarchived, unblocked, unstalled rows with positive stage rank outside the pool.
With no such task, `requireAllRejectException` requires an exception on every rejection.
A batch can combine invalid-criteria rejections and duplicate rejections.
A duplicate reference must name work outside the pool in the same Project.
Eligible rows are unarchived tasks beyond Proposed or archived approved tasks.
An ordinary low-rank rejection supplies no exception.

A pending requirements revision retains the existing superseded-verdict path.
The command records those verdicts as `proposal.superseded` rather than applying them to revised requirements.
Accepted rejections archive proposals and retain each reason in `task.rejected` activity.
The transaction ends the run and releases its lease.
Normal dispatch can then select the Strategist without an operator decision for the invalid criteria.
A failed batch leaves the run, lease, and proposals unchanged.

`mc/cmd/mc/arbiter_criteria_test.go` checks valid, malformed, mixed, and rollback cases through the command boundary.
Its assertions include retained reasons and subsequent Strategist dispatch.
See [the test guide](../tests/checks-and-test-lanes.md#invalid-criteria-checks) for coverage details.

## Step by step

**Strategist.** `StrategistPropose` in `mc/verbs/roles.go` files at most two task proposals as `proposed` rows with scope `task`. The run has no task subject; its Project owns it.

**Arbiter.** The claim snapshots the proposed pool into `runs.pool_snapshot`, and `mc arbiter decide` must cover that set exactly. A `promote` moves the row to `seeded`. A `reject` sets `decision = 'rejected'` and archives the row, and requires a reason. An empty ready queue requires each rejection to declare a valid duplicate reference or the `invalid_criteria` category. A blocked proposal cannot be promoted or rejected (`blocked_proposals_cannot_promote`, `blocked_proposals_cannot_reject`).

**Proposal decisions.** A proposal run that needs the operator calls `mc complete --needs-operator` without a task. `Complete` then records a shared row in `proposal_decisions` and blocks each affected proposal. The unique index `proposal_decisions_open` allows one open decision per Project, and `proposal_decision_requires_resolution` refuses an unblock until the operator resolves it. The next Strategist or Arbiter brief carries the resolution in its **Proposal decisions** part.

**Worker.** The Worker commits in its isolated repository at `/workspace/source` and calls `mc complete --status worked --commit <HEAD>`. `completeDelivery` in `mc/verbs/deliverycomplete.go` requires the commit to equal HEAD, the tree to be clean, and the commit to descend from the assigned input; a submission equal to the original task base is refused.
The Worker can resubmit its unchanged start commit when that commit differs from the base and meets the current description. The terminal records a `version_submissions` row only. The status moves to `worked` later, when the resident's capture operation copies the commit into the Project repository and `mc` records the `task_versions` row (`mc/verbs/delivery.go`). See [git delivery](git-delivery.md). `--checkpoint` saves partial work without advancing the stage.

**Verifier.** `VerifierVerdict` in `mc/verbs/roles.go` first checks that the container's HEAD is the exact assigned commit and the tree is clean. A pass requires `--sha` equal to the submitted commit; a correction forbids `--sha` and requires `--correction <path>`. Both outcomes require `--evidence <path>`. `domain.ApplyVerdict` writes a `version_verdicts` row and the verdict columns on the Verifier's own `runs` row. A pass sets `verified` and `source_version`. A correction calls `RequestCorrection`. When the task has correction attempts left, it returns to `seeded` with the correction as `revision_notes`. That return clears the candidate, report, and source version. The initial rejection opens the Verifier correction sequence at zero.
`correction_count` counts subsequent rejected corrections, from zero through three.
A Verifier pass resets the count and closes that sequence.
The third rejected correction blocks the task for an operator decision instead of returning it to the Worker.

**Wiki updates.** The Scribe works separately from tasks at the Project's accepted commit.
It updates only `openwiki/` and submits an OpenWiki completion receipt.
Culler merges the wiki commit without a task packet or operator review.
See [OpenWiki and viewers](../integrations/openwiki-and-viewers.md).

**Packager.** The Packager terminal requires HEAD to equal the submitted version, a passing verdict on the exact submitted source version. It then advances the task to `packaged` and stores `report_path`, `report_sha256`, and the one-paragraph `thesis` (at most 4096 bytes, no control characters other than newline).

**Operator decision.** `PacketDecide` in `mc/verbs/packet.go` accepts exactly one of `--approve`, `--revise`, or `--cancel`, plus the reviewed packet and version identifiers. Approve forbids a reason; revise and cancel require one. Approval goes through `domain.ApproveReview` and gives the task merge priority. Revise records a `review_decisions` row and calls `RequestPacketRevision`, which returns the task to `seeded`. Cancel archives the task. A revise or cancel that names an obsolete packet is refused. The schema permits `decision = 'approved'` only on a `packaged` row, and `tasks_approval_revision_fence` refuses approval while a requirements revision is pending.

## Verification duties and task verdicts

The Worker must satisfy the criteria, pass new or changed tests, and resolve task regressions that the Verifier identifies.
The Verifier obtains independent results on the submitted commit.
It runs criterion checks, new or changed tests, and normal broad checks, including required build and analysis checks.
The Verifier Assessment duties and unavailable-check outcomes belong to the Culler contract.
Project customization preserves those duties while it can replace methods and optional checks.
For submitted failures present on the base, it compares the failure under comparable conditions with that commit.
The same check and cause establish an unchanged failure; matching nonzero exit statuses alone do not.

A criterion failure, failed new or changed test, or task regression requires CORRECT.
A task regression can affect behavior outside the acceptance criteria.
Unchanged failures and unavailable host checks outside the criteria permit PASS when the other requirements hold.
The unavailable-host-check exception excludes new or changed tests, which must pass.
An unresolved executable failure prevents PASS.
The Verifier assesses new skips and lost coverage, and runs later independent checks after an earlier failure.
Successful submitted checks require no base failure for regression detection.
A criterion about an earlier state still requires evidence of that state.
See [task verification](../tests/checks-and-test-lanes.md#task-verification-and-base-comparisons) for the comparison procedure and evidence rules.

## Packet content and Project instructions

The Packager directive defines these default sections, in order:

1. Summary, with the same text as the submitted thesis.
2. Changes since the previous packet, when the brief includes a Previous packet part.
3. Background.
4. Intuition.
5. Technical.
6. Criterion evidence, with each criterion, the Verifier checks, and their results.
7. Evidence media, with captions.
8. Risk and rollback.
9. Glossary.

Under the default structure, a section is absent only when the task has no records for it.
Project instructions can replace the section structure and omit the glossary.
The packet must retain all decision evidence, material behavior, risks, and verification results.
It includes unchanged failures, their base comparisons, skipped checks, unavailable checks, and each `UNVERIFIED` entry.
The packet separates actual command results from the task verdict.
A nonzero command remains unsuccessful even when the task receives PASS.
These observations alone create no mandatory manual check or additional approval requirement.
These rules come from `mc/verbs/directives/packager.md`; they govern authorship rather than an automatic reordering of the report.

The Packager writes one self-contained `packet.html` with embedded media and no scripts.
The dashboard supplies the off-white page background and base typography.
The Packager keeps page containers transparent and can add inline styles for diagrams or other visual content.
The `mc-packager` skill requires preservation of this page style when a previous packet supplies the starting content.
See [agent input](../concepts/agent-input.md#packager-page-style-guidance) for these instructions and [the report frame](../architecture/dashboard.md#report-page-style) for their display.

## Requirements versions

A task's description is its requirements, versioned by `requirements_version`. Board edits from the operator go through `BoardWrite` in `mc/verbs/board.go`, which accepts actions `create`, `edit`, `revise`, `block`, `unblock`, `allow-corrections`, `resume`, and `cancel`. Each request carries an `operation_id` and the expected `record_version`, and the call is idempotent per operation. Every accepted operation is kept in the immutable `board_operations` table. A `revise` creates a pending `requirements_revisions` row through `domain.RequestRequirementsRevision`. The row releases the review slot. The trigger `tasks_requirements_revision` lets `requirements_version` rise only by one, only to the text of the pending revision, and only on an undecided, unarchived row. `run_requirements` captures, at run creation, the exact requirements each run was assigned. If a revision supersedes a run mid-flight, that run's later block or verdict is recorded as superseded rather than applied.

## Decision reports and revised requirements

A Worker, Verifier, or Packager operator decision report records `blocked_kind = 'report'`.
The dashboard and Collie resolve it through a requirements revision or task cancellation.
The revision requires a changed description and a reason.
An active run or repository operation can delay application; Decision Pending remains until application succeeds.

The revision preserves the original task base, checkpoint, and open merge repair.
It clears the report condition and obsolete Verifier correction context.
The next Worker receives the revised description and the reason as context.
The Verifier judges the full change from the original base against the current description.
The Packager receives the revised description and the current verdict.
Neither role receives the revision reason.

The Worker and Verifier skills select outcomes for unavailable criterion checks and new or changed tests by cause.
The Worker repairs permitted source, test, Workspace dependency, and setup script defects.
It proposes Image dependencies and submits a checkpoint when those dependencies can supply the prerequisite.
The Verifier returns CORRECT for either repairable cause.
Both roles report an operator decision when no agent container has the required capability.
The Worker investigates an unclear cause; the Verifier returns CORRECT for that uncertainty.
Unavailable checks remain unverified, and both roles continue independent work within their authority.
See [decision resolution](decision-resolution.md) for the condition types, operation, and focused tests.

## Artifacts under `/mc/artifacts`

An agent writes reports, evidence, corrections, and packets inside its container. Only files on a mounted artifact plane survive the run. `ArtifactsMountRoot` in `mc/verbs/verbs.go` fixes the container root at `/mc/artifacts`. `requireDurableRunArtifact` in `mc/verbs/complete.go` refuses any recorded path that is outside that root, outside one of this run's own planes (read from `run.json`), missing, empty, a symlink or non-regular file, or over its size bound. The bounds are 64 MiB for packets and deliverables, 16 MiB for evidence, and 1 MiB for corrections. A refusal names a writable destination such as `<plane>/<task>/corrections/<your-file>`, so the agent can retry instead of stranding the run.

After the check, `snapshotRunArtifact` in `mc/verbs/artifactsnapshot.go` copies the bytes to a read-only `snapshot-<sha256><ext>` file beside the source. The copy is atomic, and the recorded path is the snapshot. The snapshot is refused if the source changes while it is read, and an existing snapshot with different bytes is an error. Later edits to the original file therefore cannot change what the Verifier or operator sees. On the host, `ResolveArtifactHostPath` in `mc/verbs/artifactpath.go` maps a recorded container path back through the mount allowlist. It resolves symlinks on both sides and requires containment, so a symlink planted on a read-write plane cannot make the dashboard serve another host file.

## Tests

- `mc/verbs/board_test.go` and `board_unblock_test.go` cover board operations and unblock with pending revisions.
- `mc/verbs/requirements_brief_test.go` covers requirements in run briefs.
- `mc/verbs/artifactpath_test.go` and `artifactsnapshot_test.go` cover host path resolution and snapshotting.
- `mc/verbs/dispatchcorrection_test.go` and `dispatchlifecycle_test.go` drive the full status lifecycle through dispatch.

## Worker instruction audit

The original [Worker instruction audit](../operations/worker-instruction-audit.md) records the instruction review for task 56.
It explains which rules the Worker directive and command skill retain, and why each rule belongs there.
Conditional procedures reside in the command skill, while brief explanations identify the supplied data.
The audit records shorter instructions with unchanged runtime authority, command forms, terminal handlers, and approval behavior.
Its counts and validation results describe that task and its merge repair.
The recorded checks include an unchanged operator-home failure and unavailable host checks.
They do not establish verification of the current accepted commit.

The current `mc/verbs/directives/worker.md` and `mc/verbs/skills/worker/mc-worker/SKILL.md` define the present Worker instructions.
See [agent input](../concepts/agent-input.md) for their delivery and the shared skills.

## Environment and merge repairs

Project setup precedes this pipeline and permits a complete report with unavailable capabilities.
That report does not waive a task's required checks.
A Worker can propose Image dependencies through the environment procedure and retain incomplete source through a checkpoint.
The operator approves exact image build inputs before the continuation uses the result.
See [Project environments](project-environments.md).

Each task merge conflict starts a merge repair and clears its approval.
Merge repairs have no separate finite allowance.
Verifier corrections permit three unsuccessful corrections and retain their operator grant procedure.
An approval gives the task Project priority; a correction alone does not.

## Dependency state across roles

Setup and pipeline runs share the Project home at `HOME=/home/agent`.
It retains dependency caches, installed tools, and Project tool configuration.
The Worker, Verifier, and Packager still receive fresh source workspaces at their assigned commits.
The retained setup script prepares each workspace before that role starts.
The package manager checks retained files against current declarations and the selected image.

The Strategist and Arbiter read fresh committed projections and omit automatic dependency preparation.
The Scribe also omits that preparation while it updates its separate wiki repository.
Temporary directories inside the home isolate role skills and harness state for each container.
Credentials and sessions remain separate for each run.

Shared dependencies preserve files; they do not establish a Verifier pass.
Repository requirements and actual execution evidence still determine task completion.
See [Project environments](project-environments.md#runtime-and-per-run-dependencies) and [agent input](../concepts/agent-input.md#dependency-context).

The shared environment skill describes the provider credential boundary.
Each run receives a temporary Culler token for the host proxy.
The host retains and renews provider credentials.
Agents preserve the supplied access configuration and exclude token values from output.
Shared Project dependencies do not change this separate authority.
See [Agent connections and access renewal](../operations/agent-connections.md).
