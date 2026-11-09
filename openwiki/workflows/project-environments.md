---
type: workflow
title: Project setup and environment lifecycle
description: Separate setup operations prepare dependencies and preserve exact source, configuration, and image assignments.
tags: [projects, setup, environments, security]
sources:
  - id: openwiki-source-6ff7ec04aac5c9f6567d2d84
    resource: repo://dashboard/smoke/harness.ts
  - id: openwiki-source-97d75c2035d8d87341f7484d
    resource: repo://dashboard/smoke/setup-journal.smoke.test.ts
  - id: openwiki-source-460c27048f01dfd9cb2c51ee
    resource: repo://dashboard/src/ui/setup-journal.js
  - id: openwiki-source-18796b2f3f0781d432f2c984
    resource: repo://dashboard/src/ui/setup-journal.test.ts
  - id: openwiki-source-b3bb40a3cb242690b078558d
    resource: repo://mc/cmd/mc/image_removal_prod.go
  - id: openwiki-source-41ac1483f28c7f71efcfb61e
    resource: repo://mc/cmd/mc/project_delete.go
  - id: openwiki-source-4b4bf9b862fd9dfc845ab19b
    resource: repo://mc/deployment/project_image.go
  - id: openwiki-source-4b2c2a82df1cffc64cdb7c34
    resource: repo://mc/verbs/directives/setup.md
  - id: openwiki-source-e3744a39a2148b262c35bf87
    resource: repo://mc/verbs/environment_acceptance_test.go
  - id: openwiki-source-04786a87679eea8954d2f952
    resource: repo://mc/verbs/environment_execution.go
  - id: openwiki-source-28bf13c35d1e5c48d02318b6
    resource: repo://mc/verbs/environment_test.go
  - id: openwiki-source-3730a4a7a3030a2fc724699d
    resource: repo://mc/verbs/environment.go
  - id: openwiki-source-ff3195b0a5a37a2736e48a3b
    resource: repo://mc/verbs/platform_input_test.go
  - id: openwiki-source-e2e66f6b849d1ef0be9a0b40
    resource: repo://mc/verbs/project_image_digests.go
  - id: openwiki-source-cdcae076ed897cdf74637751
    resource: repo://mc/verbs/providerlifetime_test.go
  - id: openwiki-source-aa950240d47a765b29e8be03
    resource: repo://mc/verbs/providerlifetime.go
  - id: openwiki-source-bb09a9b9edeb77b9bd13e9d0
    resource: repo://mc/verbs/skills/setup/mc-setup/SKILL.md
  - id: openwiki-source-04cae9da859027e703632c1b
    resource: repo://mc/verbs/skills/shared/project-environment-setup/SKILL.md
  - id: openwiki-source-0097b0a41bffd53dce10abf3
    resource: repo://mc/verbs/skills/verifier/mc-verifier/SKILL.md
  - id: openwiki-source-f1f8658bc93aa1e51c399e87
    resource: repo://mc/verbs/skills/worker/mc-worker/SKILL.md
  - id: openwiki-source-872ae27b171d1fd2858b2ce1
    resource: repo://mc/verbs/update_reconcile.go
  - id: openwiki-source-a2a135f3f8016d9eeb4be1d9
    resource: repo://mc/verbs/workspacesetup_test.go
  - id: openwiki-source-fe137a380d8afc80657647f3
    resource: repo://mc/verbs/workspacesetup.go
  - id: openwiki-source-09c5383289d9635a85a372a3
    resource: repo://release/UPDATES.md
  - id: openwiki-source-e028a0037f071b894557c29d
    resource: repo://resident/src/effects.ts
  - id: openwiki-source-f3babed308d9c6a9d29d3eeb
    resource: repo://resident/src/environment-lane.test.ts
  - id: openwiki-source-7092e09d830ef9ed23536948
    resource: repo://resident/src/environment-lane.ts
  - id: openwiki-source-074a0c31b85dc048be2b3972
    resource: repo://runner/shared/project-cache.ts
generated: { by: "culler-scribe", at: "2026-11-09T18:05:27.232Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-09T18:05:27.232Z
---

# Project setup and environment lifecycle

Project initialization precedes task development and uses separate environment operations.
`mc` owns their state. The resident's environment lane executes their host and container work.
Setup uses no task and claims no Project lease.

## State and entry points

Vision acceptance authorizes initial setup.
The Board presents the Setup Journal; the Environment page manages later configuration and image changes.
The dashboard routes each action through `mc/verbs/environment.go`.
`mc/verbs/workspacesetup.go` previews a Project folder before setup.
An existing file path returns guidance to choose the folder that contains the Project.
An absent folder requires an accessible parent directory.
`environment_types.go` defines the configuration: base digest, Dockerfile, build files, setup command, services, variables, and secret references.

`mc/substrate/environments.sql` stores selected state, immutable revisions, operations, results, decisions, runtime checks, and source associations.
Each operation identifies its Project, exact revision, image, input digest, and attempt.
`resident/src/environment-lane.ts` executes setup, build, validate, accept, cache-clear, and runtime-check operations.
The setup runner receives its assigned operation identity and skills.
It registers its native session separately from its report.

## Preparation and acceptance

The setup agent prepares dependencies available under ordinary workspace permissions.
It reports actual command results and unavailable capabilities.
A report has a short summary and optional Markdown details.
The report validator redacts credential patterns and known secret values before persistence.
A successful process exit alone does not establish a complete report.

A `complete` report permits source acceptance even when builds or required tests remain unavailable or fail.
Acceptance preserves the checked files, source commit, configuration, and explicit Project pause.
This establishes the Project's source association. It does not certify task acceptance criteria.
Workers and Verifiers must still satisfy repository verification requirements.
`environment_acceptance_test.go` exercises missing commands, denied namespaces, failed builds, absent caches, and platform limits.

Each operation permits two automatic attempts.
The first failure queues another attempt after 30 seconds.
The resident bounds each attempt to 30 minutes.
After exhaustion, the retained failure supports an explicit retry.
The initial file snapshot survives retries, so preparation does not silently lose its original baseline.

## Setup checklist and evidence

`mc/verbs/directives/setup.md` defines the setup agent's ordered checklist.
The shared `project-environment-setup` skill supplies the detailed assessment procedure.
These are agent requirements; the presence of the instructions does not prove successful execution.

1. The agent identifies required development commands and their prerequisites.
2. It inspects tools, permissions, services, and resource limits.
3. It prepares Workspace dependencies and confirms how fresh agent containers receive them.
4. It executes applicable commands and continues independent checks after a failure.
5. It classifies unresolved results and identifies each known missing prerequisite.
6. It assesses which image contents can enable the affected commands.
7. It compares the development benefit, feasibility evidence, and image cost.
8. It reports its decision, actual results, reasons, and unresolved limits.
9. After an image decision, it checks the assigned image and reports actual preparation results.

The report distinguishes application failures, environment failures, and unverified commands.
A command that never starts remains unverified.
Each missing prerequisite receives a classification: Workspace dependency, Image dependency, external requirement, or justified deferral.
An installation in one container alone does not prove availability in later containers.
Retained configuration or supported dependency installation must supply that continuity.
Application corrections remain ordinary Worker work.

### Image feasibility

A proposal identifies the missing prerequisite and the required commands that it can enable.
It compares the importance of those commands with the expected image size, build time, and maintenance cost where evidence permits estimates.
One essential command can justify a proposal even when an unrelated command remains unavailable.

Permitted probes and package metadata provide feasibility evidence.
The assessment checks the selected base, processor architecture, declared versions, libraries, and remaining external requirements.
It records assumptions and uncertainty without invented probabilities.
A package name or successful installation alone does not prove that its dependent command can execute.
A command that executes can still expose an application failure.

Image contents cannot supply host access, macOS interfaces, or extra container privileges.
A Docker client alone does not supply a Docker daemon.
The proposal therefore identifies unresolved services, credentials, permissions, and platform requirements.
The agent explains a deferral when another prerequisite prevents the expected benefit.
Culler builds the proposed image only after operator approval.

### Completion and continuation

When every required command passes, the setup agent submits `complete` without an image proposal.
Otherwise, it assesses each missing prerequisite before its final decision.
It submits `complete` when no permitted proposal offers a credible material development benefit.
That outcome remains valid when every repository command remains unavailable.
The details explain each deferred Image dependency and distinguish expected capabilities from verified capabilities.

After initial approval, the continuation executes affected commands when permitted and reports remaining limits.
After denial, it reports the actual capabilities of the current image.
Both continuations require `complete`; further image improvements use the Environment page after import.
Import completion does not prove that required development commands work.
The [agent input page](../concepts/agent-input.md#setup-operation-input) identifies the brief and skills that carry these requirements.

## Unavailable checks during task work

The Worker and Verifier use the shared environment skill when a missing prerequisite prevents a required check.
The role command skills select the task outcome.
Preparation evidence preserves every skipped or unavailable check, including each `UNVERIFIED` entry.
Dependency installation alone proves no dependent behavior.

The Worker repairs source, tests, Workspace dependencies, or setup script defects within its task authority.
For a prerequisite that Image dependencies can supply, it follows the environment proposal procedure and submits a checkpoint.
The Worker commits the proposed Dockerfile and build files before that proposal.
The Verifier returns CORRECT for these causes so the Worker can supply the repair or image proposal.
The Verifier preserves the submitted dependency versions and recreates Workspace dependencies from those declarations.

A criterion check or new or changed test requires an operator decision when no agent container has its required capability.
An unavailable host check outside the criteria alone requires no decision or task run failure.
This exception excludes new or changed tests, which must pass.
Independent work and supported checks continue, and unavailable checks remain unverified.
Separate release and host acceptance requirements retain their authority.

`mc/verbs/platform_input_test.go` checks these procedures in delivered role input and shared skills.
See [decision resolution](decision-resolution.md#role-input-and-unavailable-checks) for unclear causes and operator reports.
The [test guide](../tests/checks-and-test-lanes.md#unavailable-checks) explains the required evidence and verdicts.

## Access during setup

Setup uses the selected connection through the host proxy.
The host retains and renews provider credentials.
The setup container receives a temporary Culler token and public proxy trust.
The shared environment skill requires preservation of this configuration and exclusion of token values from output.
The resident can issue access while the active setup or validation operation is queued, before its first heartbeat.
The same access continues while the operation runs, until 30 minutes after its creation.
The host revokes access when the operation ends, loses active status, or belongs to an archived Project.

Shared execution limits do not combine role authority or credential storage.
Setup and pipeline containers retain their separate run identities.
See [Agent connections and access renewal](../operations/agent-connections.md) for the host grant and container boundaries.

## Setup progress and history

Open **Configs**, select **Projects**, and open the Project's Environment page.
Select **Setup history** beside **Ask agent to configure** to open that Project's Setup Journal on the Board.
The action requires a setup identifier and either completed initial setup or a selected environment revision.
Completed initial setup requires a ready environment and a successful initial acceptance operation without candidate metadata.
History availability alone does not prove that setup completed.
`setupHistoryAvailable` and `initialSetupComplete` in `dashboard/src/ui/setup-journal.js` define these checks.

**Open Environment** returns to the Project's Environment page.
After completion, **Open Board** displays the normal Board.
A reload also displays the normal Board for a completed Project.
The browser smokes check this navigation and preserve the completion milestones across history access.

The Setup Journal derives progress from retained operations and their reports.
Each milestone occupies one row at its first chronological position.
That row shows the latest state, while operation messages preserve the earlier attempts.
A retry resets its milestone until the new attempt supplies completion evidence.

Ask Collie for the current failure appears with the setup controls below the transcript.
`diagnosticNode` selects the current failed operation or the general retry condition.
The transcript preserves each failure message without duplicate diagnostic buttons.
The setup journal smoke checks the diagnostic button beside Retry and the absence of action controls inside the transcript.

Preparation and source acceptance have separate milestones.
A complete report can finish preparation while Save Project files still shows Started.
Ready for development requires both a successful initial acceptance record and a ready environment.
The journal ends initial history at the first successful acceptance and excludes later environment maintenance.
An absent acceptance record produces Evidence unavailable, even when the environment is ready.

The [dashboard page](../architecture/dashboard.md) explains the Suggested setup card, exact file comparisons, setup script, and browser checks.

## Image decisions

Workspace dependencies use the selected Project image and ordinary agent permissions.
Image dependencies require an exact standard Dockerfile and retained build files.
The operator decides that proposal in the Setup Journal or Environment page.
A stale revision cannot authorize a build, and an agent cannot approve its own proposal.

Approval queues a build and compatibility check before a continuation receives the new image.
Denial starts the continuation in the exact current image and restores original build files.
Both initial continuations must submit `complete` with accurate limits.
They cannot propose another initial image change.
During initial setup, a complete report can change other configuration, such as the setup script, without image approval.
The image inputs must still match the assigned configuration.
Validation after initial setup preserves the entire approved configuration.
Later improvements use the Environment page after import.

`mc/deployment/project_image.go` inspects complete container archives without executing candidate programs.
It compares protected platform files, accounts, mount paths, privileges, and dynamic linker controls with the selected base.
This check occurs before a candidate image can receive the spine mount.
An incompatible image therefore cannot replace the runtime's protected authority.

## Runtime and per-run dependencies

Setup and pipeline agents share the same container policy: 4 GiB of memory, two processors, and 512 processes.
Docker init reaps child processes. The default seccomp profile denies user namespaces.
Role authority and credential scopes remain separate.

The Project image supplies development tools.
The separate Culler runtime volume supplies private executables, harness packages, protected commands, and runner source.
Agent containers receive read and execute access to that volume.
A Culler release update preserves each Project's selected image digest and configuration revision.
The published update reads each selected image and base digest before it removes superseded default Project images.
It keeps images that Projects select and images that containers use. See [image delivery](../operations/image-delivery.md) for the cleanup record.
An incompatible runtime holds new work until compatibility is restored.

### Reuse of recorded runtime checks

`mc/verbs/environment.go` returns the last runtime check with each active Project selection.
The spine stores that check in `environment_runtime_checks`.
An absent check produces empty check fields.
The resident reuses a successful check when its image digest and runtime identity match the current selection and Culler release.
The runtime identity uses `releaseBuildId`, or `development` when that value is absent.

Reuse still requires the exact selected image and its base to exist locally.
`requireImages` restores a missing base from its recorded registry reference when available, then verifies the digest.
A missing selected image records a failed check with `image_missing: true`.
A prior success therefore cannot hide image removal.

`confirmRecordedCompatibility` populates the resident cache after those image checks.
Its key includes the base digest, selected image digest, release build identity, and runtime volume.
Without an OpenCode volume, this avoids another image audit and compatibility container after a resident restart.
When that optional volume exists, the resident performs the compatibility check and includes OpenCode in the cache key.
The check mounts the optional volume read-only and executes its program version command in the selected image.
This verifies that the image can execute the optional harness alongside the ordinary Culler runtime.
A missing, failed, or mismatched record uses the full compatibility path and records its result through `mc`.
A failed check leaves the cache available for a later retry.
The resident continues with other Project selections after it records a failure.

`resident/src/environment-lane.test.ts` covers reuse, changed images, changed runtimes, failed records, and removed images.
`TestEnvironmentSelectionsCarryTheLastRuntimeCheck` verifies absent check fields and the recorded result in the Go response.
These tests define expected behavior; this wiki update does not report a new test execution.

### Persistent home and separate role files

Setup and pipeline runs share one persistent Project home at `/home/agent` through `HOME`.
`ProjectToolHome` in `mc/verbs/onboardworkspace.go` selects `MC_HOME/state/projects/<project>/home` on the host.
`PrepareProjectHome` creates absent directories and preserves existing contents.
Its path checks reject invalid Project identities and unsafe host paths.
The setup lane calls the host-only home command; pipeline dispatch attests the home with its mount plan.

The home retains ordinary package caches, extracted dependencies, installed executables, symbolic links, and Project tool configuration.
Each Project has separate storage from other Projects and the operator's home.
Container removal, resident restart, and image replacement preserve that host directory.
Persistence alone does not establish compatibility with a different image or newer dependency declarations.
The package manager performs that check during preparation.

`projectHomeArguments` in `resident/src/environment-lane.ts` adds separate temporary mounts for `.agents`, `.claude`, and `.codex`.
These directories belong to each container even though their parent home persists.
Credentials and sessions use separate run locations.
Concurrent Setup Agent, Worker, and Scribe runs therefore retain separate role instructions and harness state.
Collie retains its separate home and session rules.

### Fresh source and preparation

The Worker, Verifier, Packager, and Scribe receive fresh source workspaces at their assigned commits.
The Strategist and Arbiter read fresh committed projections.
The retained setup script recreates workspace dependencies before Worker, Verifier, and Packager execution.
The Scribe, Strategist, and Arbiter omit automatic dependency preparation.
The shared environment skill requires retained dependency compatibility checks before use.

`runner/shared/project-environment.ts` executes the setup command and manages declared services.
It separates setup variables from agent variables and supplies secrets only in their declared scopes.
The runner appends actual preparation evidence to the brief and records tool and package observations.
A preparation failure remains context when the harness can still execute.
A complete setup report can retain limitations; repository requirements still govern later task verification.

### Disposable cache and disk controls

Setup and pipeline containers also share `/cache` within their Project and deployment.
`projectCacheArguments` retains one volume identity across Project image revisions on the supported platform.
This storage accepts arbitrary package files, metadata, extracted files, and executables.
Normal home caches need no package-specific Culler configuration.
An explicit cache path persists only when it addresses persistent Project storage.

`runner/shared/project-cache.ts` measures regular files without following symbolic links.
Automatic eviction removes the oldest regular files and preserves files whose names end in `.lock`.
It reports an error if the remaining files exceed the two-gibibyte limit.
Manual removal clears all entries inside `/cache`, including symbolic links, while preserving their targets.
Both operations preserve the Project home.
Tools and configuration that must survive cache removal belong in that home.

During preparation, `importLegacyNpmCache` copies absent retained npm archives into the normal home cache before eviction.
A temporary file and hard link publish each copied archive without overwriting an existing destination.
Package managers retain responsibility for dependency versions and integrity.

The environment lane reports `home_bytes`, `cache_bytes`, and `image_bytes` separately.
The Environment page displays those values and labels the removal action Clear /cache.
An active environment operation prevents cache removal.
Keep-data uninstall preserves the home and cache storage; full wipe removes deployment-owned storage and preserves source repositories.
See [installation](../operations/install-and-deployment.md#persistent-project-dependencies) and [dependency reuse checks](../tests/checks-and-test-lanes.md#project-dependency-reuse-checks).


## Source continuity and recovery

A pipeline environment proposal stays a candidate until ordinary source review and acceptance complete.
Retained task versions and merge candidates preserve the candidate assignment across continuations.
That use preserves the Project's global selection.
An interrupted source association holds new work until receipt recovery resolves it.

Published bases retain their immutable registry references for recovery.
A missing derived image requires its retained approved build inputs.
Recovery preserves the selected base and any later draft.
Before a public update replaces software, the candidate checks every selected Project image, including images of archived Projects.
The update preserves selected images instead of replacing them with the new default image.
It checks the optional OpenCode archive when that runtime is installed.
An interrupted setup operation records a failure after the updater confirms that its writers stopped.
The operator can retry that operation from the Environment page.
See [Git delivery](git-delivery.md) for source and receipt ownership.

## Project deletion

The dashboard exposes Project deletion through `projectdelete.js`.
The host broker in `mc/cmd/mc/project_delete.go` closes admission to new Project work and stops owned containers.
It checks deployment and Project identities before container removal.
A final inspection covers a container created during an earlier inspection.
Only after work stops does the broker submit deletion to the spine.
A stop failure preserves Project records and reports a retry action.
The source repository remains intact.

## Checks

The Go environment tests cover decisions, source acceptance, explicit pause preservation, retries, and secret redaction.
The setup runner tests cover dependency scopes, report limits, native sessions, and trace failure isolation.
The resident tests cover operation execution, timeout, and host observations.
Docker boundary tests cover mounted runtime use, image decisions, and runtime replacement without a Project image change.
The dashboard journal tests and smokes cover visible progress, stale responses, exact decisions, and retained history.
These are source-defined checks; this wiki update does not report a fresh execution of them.

Related: [runners](../architecture/runners.md), [dashboard](../architecture/dashboard.md), and [failure controls](../concepts/failure-controls.md).

## Initial acceptance and Discord availability

`acceptEnvironmentTx` sets `settings.discord_available` during the first accepted environment selection.
The same transaction marks the Project ready and applies its recorded pause choice.
A complete preparation report alone leaves this availability condition unmet.
The availability flag belongs to the installation and survives later Project deletion.

The dashboard reveals **Configs → Discord** after the authoritative availability read.
It preserves the current section and adds no setup requirement to Project work.
See [Discord setup and delivery](../integrations/discord.md) for existing-installation recognition and the optional connection lifecycle.
