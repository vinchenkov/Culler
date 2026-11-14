---
type: architecture
title: "The spine: SQLite substrate and schema"
description: How the spine database is defined, opened, guarded, and identified, and how the Go domain layer and the schema's triggers share the job of keeping state legal.
tags: [architecture, spine, sqlite, schema, substrate]
sources:
  - id: openwiki-source-70d6bb2c9f20c1b26bc55007
    resource: repo://mc/cmd/mc/resident_control.go
  - id: openwiki-source-4470a63a5128bc251c50ad5e
    resource: repo://mc/dispatch/README.md
  - id: openwiki-source-d32bd868b0bab75d28de9e3c
    resource: repo://mc/domain/domain.go
  - id: openwiki-source-03fd17633dd4db241e157faa
    resource: repo://mc/domain/task.go
  - id: openwiki-source-b626fc6968a28feee7f4cd27
    resource: repo://mc/substrate/delivery.sql
  - id: openwiki-source-02c69d44f2d2e01b14bf865a
    resource: repo://mc/substrate/lockdomain.go
  - id: openwiki-source-682f6a75e8fc9bd66f8e0dfc
    resource: repo://mc/substrate/schema_identity_test.go
  - id: openwiki-source-e876d4f934fe1b8e2de3721c
    resource: repo://mc/substrate/schema.sql
  - id: openwiki-source-45770f873ba593d4d685f66f
    resource: repo://mc/substrate/substrate.go
  - id: openwiki-source-dcc4b26bf8c3604faddafb6a
    resource: repo://mc/substrate/task_model_test.go
  - id: openwiki-source-2ace014b73897f1358a673f8
    resource: repo://mc/verbs/customization.go
  - id: openwiki-source-f3d64f8fe895a5a9e20afaa3
    resource: repo://mc/verbs/discord.go
  - id: openwiki-source-5676d4669a3daedad8c410c7
    resource: repo://mc/verbs/outbox.go
  - id: openwiki-source-c7f396a36b94cc764d4a5cdb
    resource: repo://resident/src/resident-control.ts
generated: { by: "culler-scribe", at: "2026-11-13T23:23:34.077Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-13T23:23:34.077Z
---

# The spine: SQLite substrate and schema

The spine is the single SQLite database that holds all Culler state. It is the message bus between components and the ground truth for Projects, tasks, runs, leases, activity, Collie sessions, and repository executions. Only the `mc` binary opens it (see [Culler system architecture](system-overview.md)). This page covers the package that defines and opens it, `mc/substrate`, and the Go layer above it, `mc/domain`.

## Schema files

The DDL lives in three files embedded into the `mc` binary by `mc/substrate/substrate.go`:

- `mc/substrate/schema.sql`: core records, including `meta`, `sandbox_profiles`, `projects`, `tasks`, `settings`, `leases`, `runs`, `infrastructure_failures`, `activity`, `collie_sessions`, and `collie_bindings`.
- `mc/substrate/delivery.sql`: the Git delivery model, including `repository_runs`, `task_versions`, `merge_candidates`, `merge_operations`, `repository_executions`, and artifact retention.
- `mc/substrate/environments.sql`: Project environments, revisions, operations, and source associations.

At package init, `Schema` becomes `schema.sql`, `delivery.sql` and `environments.sql` joined with newlines. `Init` applies the whole schema in one transaction to an empty database. If any statement fails, the transaction rolls back and the database keeps its previous contents (`TestInitFailurePreservesTheDatabase` in `mc/substrate/schema_identity_test.go`).

## Illegal states are unrepresentable

The schema enforces the state machine directly. CHECK constraints restrict enumerations and pair related columns. Triggers abort illegal writes with `RAISE(ABORT, …)`. Examples:

- `tasks.status` is limited to `proposed`, `seeded`, `worked`, `verified`, `packaged`. The `tasks_legal_transitions` trigger lists every allowed status edge. Some edges have conditions: `verified → seeded` requires a pending requirements revision and a bumped requirements version. Every other change aborts with `illegal status transition`.
- `leases` has one row per Project. The `projects_insert_lease` trigger creates it, and `leases_no_delete` prevents removal. CHECK constraints require all claim fields (`run_id`, `owner`, `acquired_at`, `boot_id`, uptime fields) to be all null or all set, so a free lease carries no residue of a past run. A partial unique index lets one run hold at most one lease.
- `activity` is append-only: `activity_append_only_update` and `activity_append_only_delete` abort every update and delete. Its dispatch receipt keys are CHECKed by both character length and byte length, so a value with an embedded NUL cannot pass as a valid hex key.
- Many record kinds are never deleted (`tasks_no_delete`, `runs_no_delete`, `projects_no_delete`) or are immutable once written (`task_versions_immutable`, `version_verdicts_immutable`, `merge_candidates_immutable`).

`mc/substrate/substrate_test.go` proves these rules against a real database. `TestTransitionMatrix` tries every status pair for tasks, and expects exactly the legal edges to succeed. Other tests cover singletons, append-only tables, no-delete backstops, and Collie launch fences.

## The domain layer above the schema

`mc/domain` checks task state through the transaction that its caller supplies through `Q`.
Domain errors carry stable codes such as `illegal-transition`, `lease-held`, and `role-mismatch`.
The schema checks each write as a separate backstop.
This boundary keeps the connection and transaction lifecycle in the verbs layer.

Every task has scope `task` and destination `culler`: `tasks.destination_branch` is a stored generated column with that constant value.
The merge candidate and merge operation triggers in `delivery.sql` also require the destination `culler` for each wiki update merge.
`culler` is the Project branch that Culler owns; see [Git delivery](../workflows/git-delivery.md) for its start from `main`.
`mc/substrate/task_model_test.go` checks those constraints and the retained task columns.


`mc/verbs` runs every mutation through `inTx`, which takes one connection and issues `BEGIN IMMEDIATE`. `classify` maps errors. Typed `DomainError` and `UsageError` pass through. A raw SQLite driver error becomes a `DomainError` with code `spine-fault`, so a storage failure such as a locking protocol error is not mistaken for a rule refusal. Exit-code mapping is covered in [The mc CLI, verb scope, and boundaries](mc-cli-and-verb-scope.md).

## Strategic rationale storage

`tasks.strategic_rationale` holds the Strategist's hypothesis of how the task advances the Project vision, with any evidence, separately from `description`.
A non-null rationale requires `origin='autonomous'` and nonblank text.
The task immutability trigger prevents replacement, removal, or a later addition.
A requirements revision can change the description while the original rationale remains intact.
This preserves the evidence that justified the proposal without adding task requirements.

`BirthProposal` in `mc/domain/task.go` requires a rationale for each new Strategist proposal.
The domain accepts valid UTF-8 text of at most 8,192 bytes, after normalization of carriage-return/newline pairs.
It permits newline and tab controls and refuses other control characters.
Operator work holds a null rationale. The schema permits null for an autonomous record, while the creation path requires a value.
`mc/substrate/task_model_test.go` checks immutability and origin constraints.
`mc/domain/task_test.go` checks creation validation.
See [the task pipeline](../workflows/task-pipeline.md) for proposal and packet use.

## Role customization storage

Three tables in `mc/substrate/schema.sql` hold the operator's custom instructions for pipeline roles.
`mc/verbs/customization.go` is their only writer.

- `role_customizations` has the primary key `(project, role)`, so each Project role has one set of instructions or none.
  The text must be nonblank and at most 65,536 bytes.
  `ProjectRoleSave` upserts the row and updates `author` and `updated_at`; blank text deletes the row.
  The table keeps no revision history, and a save needs no expected revision.
- `run_customizations` keeps a copy of the instructions, the standard directive, and its digest for each run, with an empty string when the role has no instructions.
  `recordRunCustomization` writes it when the run starts. The `run_customizations_assigned_run` trigger requires an active pipeline run of the same Project and role that holds a lease or an open wiki update.
  A later save cannot change the input of a run that has already started.
- `customization_conflicts` holds one immutable report per run.
  The `customization_conflicts_assigned_run` trigger requires an active run and a passage that occurs in that run's copied instructions.
  The report names no revision. Clearing it sets `cleared_at`, `cleared_by`, and a nonblank `resolution` together.

Run copies and conflict reports are retained; only a recorded Project deletion removes them.
See [agent input](../concepts/agent-input.md) for the run brief part that carries the instructions.

## Opening the spine

`substrate.Open` is the read-write opener. It first calls `GuardLockDomain`, then opens the file with three DSN pragmas: `busy_timeout(5000)`, `journal_mode(WAL)`, and `foreign_keys(1)`. Because they are DSN pragmas, every pooled connection carries them. `TestPragmasAndSingletons` checks that the journal mode is `wal` and foreign keys are on.

`substrate.OpenReadOnly` is the one read-only opener; every `mc … get/list` read uses it through `verbs.OpenSpineRead`. It uses `mode=ro` and `query_only(1)` and never changes the journal mode or creates a database. A WAL database normally needs `-wal` and `-shm` sidecars, which SQLite may try to create even for a read-only connection. When that fails on a read-only mount and no `-wal` file exists, the open retries with `immutable=1`. When a `-wal` file exists it does not retry, because `immutable` would ignore committed WAL frames.

In the verbs layer, `OpenSpine` and `OpenSpineRead` (`mc/verbs/init.go`) wrap these openers and report a missing spine path as a usage error.

## The lock-domain guard

SQLite locking is safe only when one kernel arbitrates the locks. On macOS, Docker runs containers in a Linux VM; a host bind mount would split locks between the macOS kernel and the VM kernel and could corrupt the database. The spine therefore lives in a Docker named volume, and `mc/substrate/lockdomain.go` refuses anything else.

`GuardLockDomain` resolves symlinks for both the spine's directory and the spine file, reads `/proc/self/mountinfo`, and finds the governing mount by longest whole-component prefix. Ties go to the later line, which is the mount processes see. It accepts only `ext4`, `ext3`, `xfs`, or `btrfs` with a `/dev/` source. This is an allowlist on purpose: a host bind can appear under a FUSE ownership shim whose filesystem type names no sharing technology, so a denylist would let it through. The guard checks the file as well as the directory because Docker can bind a single file over a legitimate volume. A mountinfo line that does not parse is an error, not a line to skip, because skipping could approve the parent mount. The guard runs before `sql.Open`, so a refused path never gets a database file.

Platform behavior is chosen by build files:

- `lockdomain_linux.go` (`linux && !test_scratch_spine`) reads the real mount table. `TestLinuxRequiresMountTable` proves that a Linux build without the tag gets a non-empty table.
- `lockdomain_other.go` (`!linux`) returns no table, and the guard accepts. A native macOS process cannot reach the real spine at all, and the macOS fast lane needs temporary spines on APFS.
- A Linux build with the `test_scratch_spine` tag also returns no table, so a Linux test build can open a test database on any filesystem. Release builds reject the tag.

The mount table source is an unexported package variable that only the package's own tests replace. There is no environment variable or flag to bypass the guard. `mc/substrate/lockdomain_test.go` covers single-file binds, shadowed mount points, not-yet-created spines, unreadable mount tables, and proves that `Open` and `OpenReadOnly` refuse before creating any file.

## Schema identity

`CurrentSchemaID` is the SHA-256 hex digest of the joined schema text. Provisioning writes it into the singleton `meta` row beside `deployment_uuid`. There is no hand-maintained version number.

`CheckSchema` runs in a read-only transaction and changes nothing. It rebuilds the current schema in an in-memory database, hashes the `sqlite_schema` objects of both databases, and compares them. It then requires `meta.schema_id` to equal `CurrentSchemaID`, and finally validates dispatch scalars: `meta.deployment_uuid`, every `tasks.title`, and the dispatch mount projection must be non-empty UTF-8 text of at most 4 KiB with no control characters. A mismatch wraps `ErrSchemaMismatch`. `TestCheckSchemaRejectsIncompatibleStateWithoutMutation` changes identity, drops a trigger, or adds a column or table, and expects a mismatch with `PRAGMA schema_version` unchanged.

TypeScript computes the same identity. `SPINE_SCHEMA_ID` in `resident/src/resident-control.ts` hashes `schema.sql`, a newline, `delivery.sql`, a newline, and `environments.sql`, the same bytes as Go. In every `mc dispatch` control exchange, `exchangeControlHello` in `mc/cmd/mc/resident_control.go` sends a `hello` frame carrying the Go `CurrentSchemaID`. The resident's `serveControl` compares the frame with its own expected hello, which carries `SPINE_SCHEMA_ID`, and refuses any difference. `mc` in turn requires the echoed `hello_ack` to match exactly. A resident from one release therefore cannot drive an `mc` from another. The release packager `mc/cmd/culler-package` also records `CurrentSchemaID` in the package metadata.

## Wiki updates and environment operations

The schema stores wiki updates separately from tasks.
The `runs_scribe_has_no_task` trigger rejects a Scribe run with a task subject.
Project setup also uses separate environment operations, outside the task lease.
See [Project environments](../workflows/project-environments.md) for the source and image association lifecycle.

## Related pages

- [Culler system architecture](system-overview.md)
- [The mc CLI, verb scope, and boundaries](mc-cli-and-verb-scope.md)
- [Dispatch and the tick loop](../workflows/dispatch-and-tick-loop.md)
- [Task pipeline](../workflows/task-pipeline.md)
- [Checks and test lanes](../tests/checks-and-test-lanes.md)

## Decision conditions and task commits

The `tasks.blocked_kind` field identifies the cause of Decision Pending.
The schema permits `report`, `customization`, `allowance`, `confinement`, `checkout`, `operator`, and `proposal`.
A blocked task requires both a reason and a condition type.
The unblock trigger clears both fields when `blocked` changes to zero.
A change to the condition type also advances `record_version`, so stale Board edits fail their version check.

`base_commit` records the first Worker start commit.
`tasks_base_commit_immutable` refuses a later change once that value exists.
`input_commit` can change when revised requirements resume retained work.
This separation preserves the full task change for the Verifier.
`mc/substrate/substrate_test.go` checks base immutability and condition constraints.
See [decision resolution](../workflows/decision-resolution.md) for the transaction and operator controls.

## Discord availability and retired delivery

The singleton `settings` row retains `discord_available` and `discord_connection`.
Initial source acceptance sets availability in the same transaction that makes the Project ready.
`DiscordAvailable` also recognizes an accepted existing Project or an enabled connection.
Once set, this flag survives Project deletion and Discord disconnect.
The connection field records a digest of the host policy; the bot token stays in host configuration.

`DiscordApply` replaces only Discord alert routes in one transaction.
A retired connection deactivates its Discord Collie bindings and marks pending Discord outbox rows with `retired_at`.
It also retires the stored Discord thread associations.
`collie_discord_threads` keeps one session, server, configured channel, and thread identifier together.
The thread identifier is unique, and the schema requires it to differ from the configured channel identifier.
Those rows retain their original destination and remain undelivered.
The schema permits retirement only for an undelivered Discord row and prevents a later change to its retirement timestamp.
`OutboxPoll` excludes retired rows; `OutboxAck` refuses their delivery acknowledgement.

`mc/verbs/discord_test.go` checks retained availability, route preservation, retirement, and refused acknowledgement.
See [Discord configuration](../integrations/discord.md) for the host transaction and resident activation sequence.

## Operator request receipts

`operator_requests` retains operator commands across the container and host boundary.
Each row fixes its caller origin, session and message identity, permission, target, command, input, and release.
Schema triggers prevent changes to that identity and to a completed result.
The states are pending, active, complete, and uncertain. Complete requires a result, exit code, and completion time.

`mc/verbs/operator_requests.go` owns submission, authority checks, claims, results, and recovery.
A repeated identifier must have identical content. Recovery preserves uncertainty instead of repeating an effect.
The resident executes through `mc`; it never reads this table directly.
See [operator command access](mc-cli-and-verb-scope.md#supported-operator-commands) for the host boundary.
