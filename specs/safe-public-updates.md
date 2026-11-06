# Safe public updates

## Purpose

Culler provides one update process for one operator on one Mac.
A supported update preserves durable user data and activates one coherent release.
Each failure leaves an explicit recovery action through `culler` or `install.sh`.
An update can interrupt active agent work after the operator confirms the warning.
An interrupted turn can lose output that the harness did not save.
Culler preserves committed Git history and the durable state present after verified process exit.
An interrupted Project command can leave a partial file.
Culler retains that file for recovery; it does not promise atomic completion of arbitrary Project commands.

## Public support boundary

The first public release establishes the compatibility baseline at version 0.1.0.
Unreleased internal formats require no conversion support.
The public compatibility policy identifies a supported baseline, including when internal releases already contain a version.
Each release has an immutable identity, an ordered publication sequence, and a public version.
The public version follows Semantic Versioning.
The `VERSION` file supplies the public version.
Patch releases preserve public behavior and persistent formats.
Minor releases can introduce tested forward migrations.
Before version 1.0.0, minor releases can change the public interface.
After version 1.0.0, an incompatible public interface change requires a major release.
Every supported update still preserves user data.

The release record declares its accepted source formats and minimum supported public version.
The source policy declares these compatibility values separately from `VERSION`.
Consecutive releases have a tested update path.
A skipped release is supported when the candidate accepts every source format in the installed deployment.
A required intermediate release has retained public artifacts and an explicit reason.
The installer refuses unsupported sources before software or data replacement.
The first implementation accepts unchanged persistent formats.
The spine hash identifies its format.
The configuration version identifies its format.
Credential stores and native stores declare their format or compatible producer identities.
A changed native producer requires compatible fixtures and release evidence before the update accepts that source.
A different spine schema remains a refusal until its actual forward migration passes the required tests.
A general migration framework is unnecessary before the first format change.

Ordinary downgrades are refused.
An incomplete update can restore its checkpoint and matched previous software.
A committed update uses current data for subsequent repair.
Only the latest public release receives fixes.
The maintainer retains old public artifacts and data fixtures needed for supported update tests.
A user's deployment keeps previous software only while an incomplete update requires it.

Development installs use isolated scratch homes and permit resets.
Source redeploys retain their separate maintainer contract.
A dashboard source redeploy remains a compatible source shortcut.
Public dashboard releases use the complete public update process.

## Release contents and interfaces

One release record identifies these managed components:

- The `mc` executable and the saved installer.
- The resident and dashboard files.
- The runners and their launch envelopes.
- The product image and its runtime manifest.
- The host Bun executable.
- Each harness and its bundled plugins.
- Optional OpenCode and its runtime archive.
- Bundled tools, AgentsView, and OpenWiki.

The record includes artifact checksums, sizes, and persistent format identities.
Internal commands, responses, launch envelopes, and runtime files use the exact tested release.
A service that reports a different release cannot pass activation.
A stale dashboard page cannot submit a mutation to a different release.
The dashboard identifies that case and requires a page reload.

Project images retain their selected versions and build inputs.
The candidate checks each selected Project image against its own runtime before replacement.
The check includes archived Projects or preserves an explicit check before their next activation.
Candidate checks use separate runtime storage and preserve the live runtime.
A disabled optional component requires no installation.
An enabled optional component participates in the complete update and recovery process.

## Operator experience

Read-only candidate discovery and profile assessment precede confirmation.
The assessment records the configuration digest and identifies affected work.
After verified shutdown, the candidate repeats local checks and verifies that digest.
A changed assessment blocks replacement and identifies the retry action.
The update presents one warning before it stops work.
The warning states that active runs stop and unfinished work can require a retry.
It shows the active run count when that count is available.
The actions are Cancel and Stop and update.
Cancellation before confirmation leaves the deployment available.
An unattended invocation requires explicit consent to interruption.
The updater does not infer consent from a timeout or absent input.

After confirmation, Culler records the stop intent and blocks new work.
It immediately signals the services and containers of this deployment.
It permits ten seconds for process exit, then forces termination.
The complete stop phase has one thirty-second deadline.
Container stops execute concurrently.
Agent completion cannot delay the update.

The stop covers these processes:

- The resident, dashboard, AgentsView, and their child processes.
- Pipeline agents, setup agents, and Homie containers.
- Repository operations, image operations, and native harness processes.
- Trace and wiki processes that can write protected data.

Each managed container carries the deployment identity.
The updater confines stop actions to that identity.
It verifies process exit independently of the stop command result.
An unknown process state or a live writer keeps maintenance active and refuses replacement.
The failure identifies the affected process and the retry action.

The update records interrupted runs after it verifies their exit.
An update interruption preserves task retry allowances and Project failure allowances.
It does not cancel a task.
Recovery reconciles leases before normal work resumes.
An unfinished repository receipt blocks new pipeline and wiki work in its affected Project.
Receipt recovery remains available while that block applies.
An operation with an unknown external result retains its evidence until reconciliation resolves that result.
Repository recovery preserves completed effects and prevents duplicate operations.
The update preserves the operator's Project pause choices.

## Durable update record

One lock serializes installation, update recovery, and lifecycle mutations for a deployment.
The update record lives outside every directory that the update replaces.
The record is readable by the operator account only.
Every record replacement synchronizes the file and its parent directory.
The record contains these values:

- The deployment identity and update identity.
- The exact installed release and candidate release.
- Artifact locations, checksums, and persistent format identities.
- Previous service activity and explicit service stop choices.
- Project pause choices where the update changes them.
- The phase, checkpoint manifest, and recovery result.
- Cleanup progress and any unresolved failure.

The phases are stop, prepare, checkpoint, replace, verify, committed, and complete.
Each phase records its intent before its first mutation.
A phase can repeat without data loss or selection of a different candidate.
The candidate remains fixed even when a newer public release becomes available.
A partial or unreadable record prevents normal service work before commit.
A verified committed record permits normal work while cleanup remains incomplete.
Every lifecycle entry point and service startup enforces that condition.
The update can start candidate services for verification behind the maintenance gate.
Those services cannot dispatch work or accept ordinary mutations before commit.

The verified candidate package remains available until the update completes.
Recovery starts before the installer discovers the latest release.
Recovery uses the recorded candidate and retained artifacts.
A failed updater cannot remove the executable required for its own recovery.
Normal error cleanup preserves the update record, maintenance state, and required recovery artifacts.

## Persistent data

The inventory assigns each store to the component that defines and writes its format.
Ownership describes technical responsibility.
Culler remains responsible for preservation across the complete update.

| Store | Format owner | Update requirement |
| --- | --- | --- |
| Spine | `mc` | Preserve records, identities, relationships, and operation receipts. |
| Configuration | `mc` | Preserve explicit values and effective Culler defaults. |
| Credentials | Culler and macOS Keychain | Preserve secrets, references, and the newest valid provider grant. |
| Project repositories and worktrees | Git and Project tools | Preserve commits, references, and uncommitted files. |
| Project homes | Project tools | Preserve installed tools, user files, and configuration. |
| Selected Project images | Culler and Project tools | Preserve exact selections and required image bytes. |
| Artifacts and attachments | Culler and Project tools | Preserve bytes and recorded paths. |
| Original traces | Harnesses and runners | Preserve native files and session identity links. |
| Native sessions | Each harness | Preserve the complete store and its producer format identity. |
| OpenWiki source | OpenWiki and the Project | Preserve source, control records, and Claim sidecars. |
| AgentsView indexes and wiki exports | Their viewer | Rebuild only data with a preserved complete source. |
| Runtime software volumes | Culler | Reconstruct exact software from retained release artifacts. |
| Disposable dependency caches | Project tools | Permit reconstruction while preserving Project homes. |

A checkpoint covers every persistent store that the candidate can modify before commit.
Stores outside that write set remain in place and retain their locations in the checkpoint manifest.
The update never resets a Project repository or removes uncommitted files.
The first implementation refuses a persistent format conversion without a declared and tested path.

The checkpoint contains a consistent spine snapshot when the update can write the spine.
It includes configuration, mutable credential references, and applicable native or plugin stores.
It protects host stores that home preparation can migrate or retire.
It includes both installed commands and the deployment's login service definitions.
The checkpoint identifies preserved Keychain references without writing secrets into release records or logs.
The candidate cannot invalidate the only usable credential as part of an unsuccessful update.

A complete checkpoint has a manifest, checksums, and validated store identities.
An incomplete checkpoint cannot replace a complete checkpoint.
Free-space checks include temporary copies and the candidate artifacts.
Normal backup retention cannot remove an active update checkpoint.

Required native-store migrations complete behind the update gate before commit.
An unverified native-store migration blocks commit.
A harness upgrade requires proof of saved-session access and supported continuation.
A readable transcript alone does not prove native continuation.

## Recovery and cleanup

Before commit, recovery offers a retry of the exact candidate or restoration of the previous release.
Restoration uses matched software and data from the same checkpoint.
It prepares replacement stores before it exchanges live paths.
A partial restore preserves the checkpoint and a repeatable recovery action.
A spine restore uses fresh destination storage when the current storage is not empty.
Recovery validates database integrity, deployment identity, and component identities before it releases maintenance.

The updater keeps the previous product image locally during the update.
An exported duplicate is unnecessary.
The recovery set also retains previous host files, executables, runtime material, and required optional archives.
Runtime reconstruction and restoration require no download after checkpoint completion.

Successful verification precedes the durable committed phase.
The updater synchronizes installed host files and runtime data before it records that phase.
The matching maintenance request blocks work until the durable commit completes and the updater removes that request.
The durable committed phase precedes normal work and obsolete artifact removal.
After commit, cleanup removes obsolete software and temporary update checkpoints.
Cleanup preserves these resources:

- The installed release and current user data.
- Selected Project images and images that containers use.
- Separate user backups.
- Small release manifests and diagnostics.

Cleanup removes only artifacts owned by the update.
A cleanup failure records a retry and leaves the committed release active.
Cleanup never restores an old checkpoint over newer data.
Incomplete updates retain every artifact required by their recovery action.

The support contract assumes the same Mac and intact storage.
A separate user backup policy covers disk loss and credential recovery.

## Configuration and provider compatibility

The candidate parses a copy of the saved configuration before replacement.
It maps removed options through a tested conversion or reports an actionable incompatibility.
Changed Culler defaults preserve the effective choices of existing users.
New installations can receive new defaults.

OpenCode checks use the catalog of the pinned candidate release.
Each saved selection receives these local checks:

- The provider exists in the candidate catalog.
- The model exists for that provider.
- An explicit effort exists among the choices Culler exposes for that model.

These checks require no provider request.
Culler preserves the exact saved provider, model, and explicit effort.
An empty effort retains the meaning use the OpenCode default.
Upstream reasoning behavior, output, and cost can change independently of that saved choice.
Known changes appear in the release information.

A missing catalog entry identifies a local compatibility problem.
It does not establish that an external provider removed the model.
Disabled profiles retain their saved values and show a repair message.
They do not block the update.
An affected active profile requires visible disclosure before update confirmation.
The confirmed update can proceed with affected work paused until repair.
Culler never silently substitutes a model or drops an explicit effort.

An unavailable provider remains unknown during an update.
A provider outage does not establish a local format incompatibility.
The user can test a specific profile through the existing profile test.
The release suite uses representative adapter tests and a small declared set of live integrations.
Live tests require explicit credentials and remain outside credential-free pull-request and fast checks.
A provider outage cannot block local compatibility checks.
It does not certify every remote provider, model, effort, and account combination.

## Release evidence

The maintainer has an inventory of every managed dependency pin.
The inventory includes optional components and tools outside ordinary lock files.
Each changed pin identifies its affected interface, persistent store, and required evidence.
The release checks exercise the exact packaged artifacts.
The old published update entry point must load and validate the candidate.
Before the first public release, a packaged baseline and synthetic candidate supply this test.
After publication, actual supported installers and data fixtures supply it.

A native format change requires representative old-store fixtures and a migration recovery test.
A Project image change requires runtime compatibility checks.
An AgentsView change requires old-trace access and authentication checks.
An OpenWiki change requires source preservation and export checks.
A plugin change requires its pinned harness and affected interface tests.
OpenCode request tests cover distinct adapter behavior and exact effort forwarding.
A model catalog lookup never counts as proof of remote availability.

## Acceptance criteria

### Implementation order

| Order | Affected area | Prerequisite | Acceptance |
| --- | --- | --- | --- |
| 1 | Release record and source policy | The first public baseline | Unsupported versions and formats refuse before replacement. |
| 2 | Update record and maintenance | The serialized lifecycle operation | An interruption preserves the exact candidate and blocks ordinary work. |
| 3 | Services, containers, and execution records | The durable stop intent | Exit verification meets the stop deadline and preserves history and allowances. |
| 4 | Checkpoints and restoration | Verified process exit | A failed restore preserves its sources and permits another attempt. |
| 5 | Configuration and selected Project images | The fixed candidate | Local compatibility checks preserve choices and identify affected work. |
| 6 | Component verification and commit | A complete checkpoint and compatible candidate | Exact component identities pass before the durable commit releases maintenance. |
| 7 | Cleanup and release evidence | The durable commit | Cleanup preserves current data; packaged host acceptance covers the complete path. |

### Required checks

The completed implementation satisfies these checks:

1. The update warning appears before any stop action.
2. Cancellation before confirmation preserves the available deployment.
3. A two-hour run cannot delay the stop phase.
4. A process that ignores termination receives forced termination within the stop deadline.
5. An unavailable process manager or Docker service blocks replacement with a repeatable recovery action.
6. The updater retains interrupted run files and preserves the task retry allowance.
7. A repository operation interrupted after its external effect recovers without a duplicate effect.
8. Termination before or after each replacement leaves a durable phase and exact recovery candidate.
9. A newer remote release cannot change an incomplete update's candidate.
10. An incomplete precommit update blocks ordinary service work after process restart or machine restart.
11. A checkpoint failure preserves the prior data and any complete recovery copy.
12. A restore failure preserves its source checkpoint and permits another restore attempt.
13. Recovery after checkpoint completion works without a network connection.
14. Optional OpenCode and the ordinary runtime recover with the matched previous release.
15. Successful verification checks operations and exact component identities before commit.
16. Cleanup after commit removes obsolete artifacts and preserves selected Project images and user backups.
17. Cleanup failure permits a retry without reversing the committed update or losing new records.
18. A stale dashboard mutation fails with a page reload action.
19. Candidate checks cover saved profiles and selected Project images before replacement.
20. OpenCode catalog problems preserve saved choices and permit the declared repair path.
21. The old packaged installer can load the candidate updater.
22. Required component, deployment, browser, and release checks pass.
23. Unavailable host checks remain explicit in the verification record.
24. Terminal changes pass adversarial review at 80 and 40 columns, with and without color.

## Repository basis

These files define the current integration points:

- `mc/cmd/mc/setup_published_prod.go`: published installation, maintenance, and completion.
- `mc/cmd/mc/setup_terminal_prod.go`: terminal update orchestration.
- `mc/cmd/mc/culler_services_prod.go`: lifecycle serialization and service state.
- `mc/cmd/mc/onboard_home_prod.go`: host files, runtime volumes, and credential store preparation.
- `mc/verbs/spine_transfer.go`: consistent snapshots and spine restoration.
- `mc/distribution/record.go`: immutable release identity and artifact checks.
- `resident/src/install-maintenance.ts`: the maintenance request.
- `resident/src/repository-lane.ts`: operation receipt recovery.
- `mc/opencodecatalog/catalog.go`: local saved-choice validation.
- `release/opencode/generate.py`: representative local provider request checks.
- `mc/deployment/image_removal.go`: obsolete image removal with preservation checks.
