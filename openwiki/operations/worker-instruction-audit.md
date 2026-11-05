---
document: true
title: Worker instruction audit
type: task report
task_id: 56
task_status: implemented
---

# Worker instruction audit

Base commit: `72cc9bb21af5d4c9608eada377bb9e5b509208c9`.

The Worker directive and command skill retain actionable rules for arbitrary operator Projects. This task leaves shared skills and other roles unchanged; the merge preserves destination changes to other roles.

The reference is [Matt Pocock’s writing-for-agents](https://github.com/mattpocock/skills/tree/main/skills/productivity/writing-for-agents), read for this task. The existing writing skill remains the language standard.

## Counts

Both counts use Python `len(re.findall(r"\S+", text))` on the complete UTF-8 file. This includes headings, frontmatter, and command forms.

| File | Base words | Final words |
| --- | ---: | ---: |
| `mc/verbs/directives/worker.md` | 1433 | 336 |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | 2323 | 952 |

The command skill is the only Worker-specific skill. No skill was added or removed.

## Retained sections

The directive title identifies the role. The skill title and frontmatter identify its discoverable command surface.

| File | Section | Concrete reason |
| --- | --- | --- |
| `mc/verbs/directives/worker.md` | Culler contract | Binds criteria, verification, complete source delivery, write scope, truthful evidence, safety, authority, and one terminal. |
| `mc/verbs/directives/worker.md` | Project role customization | Distinguishes replaceable defaults from contract rules and selects the conflict procedure. |
| `mc/verbs/directives/worker.md` | Cases | Selects conditional procedures before changes and retains the dependency compatibility check. |
| `mc/verbs/directives/worker.md` | Orchestration | Controls delegation, sole-writer authority, integration, and the inline exception. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Introduction | Permits a read to identify any task, rather than only the assigned task. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Your repository | Defines the Git facts needed to evaluate submission eligibility. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Task terminal | Supplies success forms and the four repository checks, including unchanged-start eligibility. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | The task execution report | Makes reports durable and usable by the Verifier; applies document and evidence rules. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | A run that cannot give a result | Requires use of available results before selection of a failure terminal. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | A decision that only the operator can make | Limits decision reports and defines their required reason and alternate command. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | A task run failure | Defines failure commands and prevents unsupported connection diagnoses or runner reports. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Case: correction round | Requires file reads and correction of unmet criteria and regressions; handles failed reads. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Case: merge repair | Preserves both intents and documents, or requires an operator decision for contradictory changes. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Case: revision request | Requires the complete operator revision before submission. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Case: requirements revision | Separates the current requirements from context and preserves concurrent merge repair. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Case: checkpoint | Defines incomplete-work retention and continuation under the same repository checks. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Project dependencies | Defines the environment request schema, committed build inputs, preserved base, and checkpoint after a proposal. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Reads | Enumerates the permitted read commands and flags. |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` | Project role customization | Defines the conflict terminal and the exact request fields. |

## Delivery and generated explanations

`trimWorkerBrief` applies only to the Worker. It removes Agent credentials, Correction allowances, Failure controls, and the correction-count fact.

The remaining changed explanations identify supplied data. Procedures live in the skill; the directive names each case that requires them.

The command pointer resolves to `~/.agents/skills/mc-worker/SKILL.md`. It requires a read before the first command and before the terminal.

The generated pointers to shared skills keep their paths, descriptions, and meaning. Their contents are outside this audit.

The command forms, runtime authority, terminal handlers, and approval behavior remain unchanged. The operator decision procedure still covers contradictory criteria and uncheckable criteria.

| Worker part | Final explanation |
| --- | --- |
| Project role customization | This part identifies the operator's custom instructions and their revision. |
| Run | Use this identifier as `<run_id>` in command forms that require it. |
| Task | Use the Identifier as `<task>` in command forms. |
| Project environment | Use these identifiers to assess dependency compatibility and prepare an environment proposal. |
| Git state | Use these Git facts for the repository checks in the mc-worker skill. |
| Requirements revision | This is the operator's reason for the requirements revision. |
| Revision request | This is the operator's request for this revision. |
| Merge candidate | These are the merge inputs and conflict paths. |
| Correction from the Verifier | These files contain the Verifier's objection and evidence. |
| Operator allowance explanation | Use the operator's explanation as context for the unresolved condition. |

## Initial submission validation

The initial submission’s final `./mc/check.sh` command exited 1. Its only failure was `TestValidateHomeAcceptsTheRealOperatorHome` in `mc/boundary`.
The container home `/home/agent` is a filesystem mount root. The real operator home is unavailable.
All new and changed tests passed. The complete `mc/verbs` package passed in 173.054 seconds.
Every other Go package passed.

| Check | Exit | Result |
| --- | ---: | --- |
| `./mc/check.sh` | 1 | Only the unchanged operator-home test failed. |
| Formatting and all vet configurations within that command | 0 | Untagged, per-tag, and Linux arm64 checks passed. |
| Deployment and test-environment checks within that command | 0 | Passed with `-count=1`. |
| Installer, redeploy, and uninstall contracts within that command | 0 | Passed. |
| Final targeted instruction and delivery tests | 0 | Passed in 4.011 seconds. |
| Linux filesystem guard command, with `-tags=` and `-count=1` | 0 | Passed; two tests skipped on the overlay filesystem. |
| Skill frontmatter validator | 0 | Reported `Skill is valid!`. |
| `git diff --check` | 0 | No whitespace errors. |

The targeted command was:

```sh
./mc/test.sh ./verbs/ -run 'Test(Worker|LaterWorker|PacketRevisionRequest|ProseLess|AgentInputStatesProject|EveryDirective|EveryRoleShips|EachDirective|AgentInputNames|AgentTextNames|RunBriefSubject|DispatchSpawnBrief)'
```

The separate filesystem command was:

```sh
./mc/test.sh -count=1 -tags= ./substrate/ -run 'Test(LockDomain|OpenRefuses|OpenReadOnlyRefuses|GuardResolves|LinuxRequires)'
```

The baseline comparison used the unchanged boundary package and its unchanged refusal dependency.
`git diff --exit-code` against the base returned 0 for `mc/boundary`, `mc/refusal`, `mc/go.mod`, and `mc/go.sum`.
The selected operator-home test failed with the same home and cause on that identical source:

```text
home_test.go:47: the REAL operator HOME "/home/agent" (mode drwx------) was REJECTED: HOME "/home/agent" is a filesystem root (its device differs from its parent); broad_root would then reject every source
FAIL mc/boundary
```

The initial deployment check rejected the checkout's pre-existing `0777` directory permissions.
Removal of group and other write access from owned checkout directories resolved that fixture prerequisite.
The historical `.claude/worktrees/` directory and Git metadata remained untouched.
No tracked file mode changed.

The skill validator initially lacked PyYAML. A temporary installation of PyYAML 6.0.3 supplied that validator dependency.
The Project dependency declarations and selected image remain unchanged.

The independent read-only review covered removed behavior, duplication, pointers, and the passage audit.
The final review found no instruction blocker.
Tests compare other pipeline roles' directives, pointers, and brief explanations with digests captured from the base generators.
Shared files and other roles' instruction files have no diff.

The changes affect Go instruction content and delivery only.
Bun component suites and opt-in Docker, browser, and nightly lanes were not run.
No terminal setup display, browser asset, runtime handler, or command authority changed.

### Unverified checks

The first three entries below come from skipped tests in the full check.
The next two preserve the filesystem command's exact `UNVERIFIED` entries.
The operator-home entry records a failed host-only check, not a skip.
The remaining entries name macOS tests excluded by this Linux build.

```text
UNVERIFIED mc/boundary TestBroadRootRejectsFirmlinkAliasRoutes: firmlinks are macOS-specific
UNVERIFIED mc/boundary TestValidateHomeRejectsNonSlashFilesystemRoot: the repository's measured non-/ volume witness is macOS-specific
UNVERIFIED mc/boundary TestOwnControlAncestorExemptionIsExactAndAssociated/same-file_different-spelling_workspace_permits: case-sensitive volume: no same-file/different-spelling pair available
UNVERIFIED mc/substrate TestOpenRefusesASpineOutsideTheLockDomain: This test requires a test directory on a block-device filesystem: refusing to open the spine under /tmp/TestOpenRefusesASpineOutsideTheLockDomain1858139431/001: its mount / is fstype=overlay source=overlay, which is not a block-device-backed local filesystem — the spine must live on a runtime-local named volume, because a shared host path splits its locks across two kernels and corrupts it
UNVERIFIED mc/substrate TestOpenReadOnlyRefusesASpineOutsideTheLockDomain: This test requires a test directory on a block-device filesystem: refusing to open the spine under /tmp/TestOpenReadOnlyRefusesASpineOutsideTheLockDomain2137058019/001: its mount / is fstype=overlay source=overlay, which is not a block-device-backed local filesystem — the spine must live on a runtime-local named volume, because a shared host path splits its locks across two kernels and corrupts it
UNVERIFIED mc/boundary TestValidateHomeAcceptsTheRealOperatorHome: the container HOME is a mount root; the real operator home is unavailable. The test fails rather than skips.
UNVERIFIED mc/boundary TestDarwinACLPrincipalResolverRequiresOwnerUserIdentity: requires macOS; this run uses Linux.
UNVERIFIED mc/boundary TestDarwinACLInspectorWiresMembershipResolver: requires macOS; this run uses Linux.
UNVERIFIED mc/boundary TestTrustPolicyFileReadsNativeDarwinACL: requires macOS; this run uses Linux.
UNVERIFIED mc/boundary TestTrustHomeDirReadsNativeDarwinACL: requires macOS; this run uses Linux.
UNVERIFIED mc/boundary TestOperatorHomeDoesNotAcquireMCHomeACLSeam: requires macOS; this run uses Linux.
UNVERIFIED mc/boundary TestSuffixCaseModeForPathReportsKnownDarwinVolume: requires macOS; this run uses Linux.
UNVERIFIED mc/boundary TestSuffixCaseModeForPathFailsClosedOnDarwinLookupError: requires macOS; this run uses Linux.
UNVERIFIED mc/cmd/mc TestAttachmentCanaryScratchRefusesGrantingNativeACL: requires macOS; this run uses Linux.
UNVERIFIED mc/cmd/mc TestPrivateAttachmentSpineRejectsProductionHostScope: requires macOS; this run uses Linux.
UNVERIFIED mc/cmd/mc TestUninstallFindsTheHomeOfALoneAgentsViewUnit: requires macOS; this run uses Linux.
UNVERIFIED mc/deployment TestAuthorizationExecutableOutsideSupervisedPath: requires macOS; this run uses Linux.
UNVERIFIED mc/deployment TestHostSecretKeychainRoundTripUsesIsolatedStoreAndHandlesLongValues: requires macOS; this run uses Linux.
UNVERIFIED mc/deployment TestAcquireRuntimeAuthExportsClaudeCredentialsFromItsIsolatedKeychain: requires macOS; this run uses Linux.
UNVERIFIED mc/deployment TestAcquireRuntimeAuthRefusesAmbiguousKeychainCandidates: requires macOS; this run uses Linux.
UNVERIFIED mc/deployment TestAcquireRuntimeAuthRefusesWhenTheIsolatedKeychainStayedEmpty: requires macOS; this run uses Linux.
UNVERIFIED mc/deployment TestClaudeCredentialStoreNeverFallsBackToAnotherKeychain: requires macOS; this run uses Linux.
UNVERIFIED mc/deployment TestAcquireRuntimeAuthPreparesTheKeychainBeforeSpendingALogin: requires macOS; this run uses Linux.
UNVERIFIED mc/deployment TestSupervisionPropertyListsPassPlatformValidation: requires macOS; this run uses Linux.
UNVERIFIED mc/onboarding TestSessionRejectsNativeAccessGrants: requires macOS; this run uses Linux.
UNVERIFIED mc/onboarding TestSessionRejectsInheritedAccessGrantBeforePublication: requires macOS; this run uses Linux.
```


## Passage audit

Each quote gives original text from the base commit. Wrapped lines remain as stored where the quote spans them.

`duplicate` includes old formulations consolidated into one retained instruction. The named destination identifies that instruction and its section.

`moved behind a pointer` identifies conditional procedure text removed from the directive or command skill. Each entry names its destination and trigger.

`unneeded` identifies system descriptions, redundant structure, or text without a Worker action. Unchanged sentences are omitted.

### Passage 1

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Culler keeps provider credentials on the host.

This fact does not select a Worker action.

### Passage 2

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> This container uses a temporary Culler token through the host provider proxy.

This fact does not select a Worker action.

### Passage 3

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The host renews account access without a credential update inside the container.

This fact does not select a Worker action.

### Passage 4

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Preserve the proxy configuration and exclude Culler tokens from output.

The action remains without connection mechanics.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 5

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Setup and pipeline agents share container permissions, resource limits, network policy, and workspace paths.

This fact does not select a Worker action.

### Passage 6

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Role authority and credentials remain separate.

This fact does not select a Worker action.

### Passage 7

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Culler preserves the Project home at `HOME` across Setup Agent runs and pipeline runs.

This fact does not select a Worker action.

### Passage 8

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The home retains dependency caches, installed tools, and Project tool configuration.

This fact does not select a Worker action.

### Passage 9

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Each run receives a fresh source workspace and separate credentials, sessions, role skills, and instructions.

This fact does not select a Worker action.

### Passage 10

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The runner prepares workspace dependencies before the agent starts.

This fact does not select a Worker action.

### Passage 11

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Implement the assigned acceptance criteria and resolve task regressions that the Verifier identifies.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 12

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Submit the complete deliverable as one verified repository commit through the permitted terminal.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 13

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The contract retains the pipeline topology and this role's responsibility.

This fact does not select a Worker action.

### Passage 14

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The contract also retains these requirements:

This fact does not select a Worker action.

### Passage 15

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Preserve the acceptance criteria and accepted work.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 16

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Report evidence and results truthfully.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 17

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Produce the required deliverables.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 18

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Use `mc`, the Mission Control command, for Culler state changes.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 19

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Use only permitted commands with your own run identity.

The contract retains the command identity constraint.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 20

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Preserve repository, artifact, and display safety constraints.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 21

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Submit exactly one permitted terminal action to end the run.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 22

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The **Project role customization** part contains the operator's instructions for this Project and role.

The part identifies its own content.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Project role customization`.

### Passage 23

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Follow them within your Culler contract.

The hierarchy remains once.

Retained location: `mc/verbs/directives/worker.md — Project role customization`.

### Passage 24

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> They replace standard defaults where those defaults differ.

The hierarchy remains once.

Retained location: `mc/verbs/directives/worker.md — Project role customization`.

### Passage 25

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Custom instructions also replace defaults in the run brief and role skills.

The hierarchy remains once.

Retained location: `mc/verbs/directives/worker.md — Project role customization`.

### Passage 26

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> If an instruction conflicts with your contract, report a customization conflict and explain the conflict.

The skill contains the conflict terminal and required explanation.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project role customization`.

Pointer condition: A custom instruction conflicts with the contract.

### Passage 27

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> The command skill describes the `mc run customization-conflict` terminal.

The skill contains the conflict terminal and required explanation.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project role customization`.

Pointer condition: A custom instruction conflicts with the contract.

### Passage 28

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> This command preserves the Project pause state and all failure and correction allowances.

This fact does not select a Worker action.

### Passage 29

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Revision zero means that the run received no custom instructions.

This fact does not select a Worker action.

### Passage 30

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Culler runs a pipeline of agent roles on one Project, a Git repository, for one human operator.

This fact does not select a Worker action.

### Passage 31

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The Strategist proposes tasks.

This fact does not select a Worker action.

### Passage 32

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The Arbiter selects them.

This fact does not select a Worker action.

### Passage 33

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The Worker builds each task.

The contract already defines the work and terminal obligation.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 34

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The Verifier judges the result.

This fact does not select a Worker action.

### Passage 35

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The Packager writes the review packet that the operator approves or rejects.

This fact does not select a Worker action.

### Passage 36

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The Scribe has no task.

This fact does not select a Worker action.

### Passage 37

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> It updates the Project wiki on a schedule.

This fact does not select a Worker action.

### Passage 38

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Each role runs in a fresh container and knows only this run brief and the skills that it names.

This fact does not select a Worker action.

### Passage 39

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> `mc` is the command-line program of Culler.

The contract already defines the work and terminal obligation.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 40

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> A role reports its result with one `mc` command, the terminal action, and that command ends the run.

The contract already defines the work and terminal obligation.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 41

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> You are the Worker.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 42

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> You produce the deliverable of one task in your own copy of
> the Project repository at `/workspace/source`.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 43

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Your run ends when you submit one
> commit that holds the complete deliverable.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 44

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Use the project-environment-setup skill for dependency changes.

One dependency pointer selects the procedure.

Retained location: `mc/verbs/directives/worker.md — Cases`.

### Passage 45

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Project import permits limited environment capabilities.

This fact does not select a Worker action.

### Passage 46

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Repository requirements still control task verification.

Required criteria and honest test results remain universal.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 47

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> Workspace dependencies use ordinary task declarations and checks.

The conditional procedure applies the unchanged shared environment skill.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

Pointer condition: Dependency changes.

### Passage 48

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> Image dependencies require a Dockerfile proposal and operator approval before an image build.

The conditional procedure applies the unchanged shared environment skill.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

Pointer condition: Dependency changes.

### Passage 49

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> Preserve the Project's selected base and Culler's private runtime.

The conditional procedure applies the unchanged shared environment skill.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

Pointer condition: Dependency changes.

### Passage 50

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> Retain incomplete source through the checkpoint terminal after an environment proposal.

The conditional procedure applies the unchanged shared environment skill.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

Pointer condition: Dependency changes.

### Passage 51

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> ## The run brief

The heading or connective text no longer organizes retained instructions.

### Passage 52

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The parts of the run brief follow this directive.

The visible brief structure needs no prose description.

### Passage 53

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Each part has a heading
> and an explanation.

The visible brief structure needs no prose description.

### Passage 54

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The data of a part is a list of labeled facts, a text in
> tags, or one JSON block.

The visible brief structure needs no prose description.

### Passage 55

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Each part is input for this run.

The visible brief structure needs no prose description.

### Passage 56

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The explanation of a
> part states how you use its data.

The visible brief structure needs no prose description.

### Passage 57

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The description of the **Task** part holds
> every requirement of the task.

The skill distinguishes requirements from revision context.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: requirements revision`.

### Passage 58

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - The **Run** part holds the run identifier.

The generated Run explanation defines the placeholder.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Run`.

### Passage 59

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> It is the `<run_id>` value of each
>   `mc` command.

The generated Run explanation defines the placeholder.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Run`.

### Passage 60

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - The **Task** part holds the task identifier and the description.

The Task explanation defines the placeholder.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Task`.

### Passage 61

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The
>   identifier is the `<task>` value of each `mc` command.

The Task explanation defines the placeholder.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Task`.

### Passage 62

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The description states
>   each criterion.

The contract binds the Task description.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 63

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The `Unsuccessful Verifier corrections` fact counts rejected corrections.

This fact does not select a Worker action.

### Passage 64

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - The **Git state** part holds the `Destination branch` fact.

The skill holds the Git definitions.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 65

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> After the first
>   Worker run of the task, the part also holds the `Start commit` fact and the
>   `Base commit` fact.

The skill holds the Git definitions.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 66

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Your repository starts at the start commit.

The skill holds the Git definitions.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 67

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> A part or a fact that occurs only in some runs starts a case.

The pointer now lists the exact triggers.

Retained location: `mc/verbs/directives/worker.md — Cases`.

### Passage 68

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The mc-worker
> skill holds the procedure for each case under a heading that names the case.

The pointer now lists the exact triggers.

Retained location: `mc/verbs/directives/worker.md — Cases`.

### Passage 69

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Read that procedure before you change anything.

The pointer now lists the exact triggers.

Retained location: `mc/verbs/directives/worker.md — Cases`.

### Passage 70

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> ## The work

The heading or connective text no longer organizes retained instructions.

### Passage 71

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Produce the deliverable against each criterion.

The work boundary and merge prohibition remain universal.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 72

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Your submission must be the start commit or a descendant
> of the start commit.

The submission check enforces lineage.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 73

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The destination branch is the branch that receives your commit.

The Git fact definition remains in one place.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 74

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The
> `Destination branch` fact names it.

The Git fact definition remains in one place.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 75

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Culler merges your commit with that branch before acceptance.

The Worker cannot perform acceptance; the merge prohibition remains.

### Passage 76

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Each task requires operator approval before acceptance into main.

The Worker cannot perform acceptance; the merge prohibition remains.

### Passage 77

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Do not merge your commit into the destination
> branch yourself.

The work boundary and merge prohibition remain universal.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 78

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The base commit is the commit where the first Worker run of the task started.

The skill holds start and base definitions.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 79

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The start commit of this run is the first of these commits that exists:

The skill holds start and base definitions.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 80

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> 1. the checkpoint commit;

The skill holds start and base definitions.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 81

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> 2. the last submitted commit of the task;

The skill holds start and base definitions.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 82

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> 3. the base commit.

The skill holds start and base definitions.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 83

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Your submission must differ from the base commit.

The four checks and unchanged-start option cover this rule.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 84

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> `mc` refuses a submission
> or a checkpoint that equals the base commit.

The four checks and unchanged-start option cover this rule.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 85

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> You can submit the start commit
> unchanged when it differs from the base commit and meets each criterion of the
> description.

The four checks and unchanged-start option cover this rule.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 86

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The commit holds the complete deliverable.

The deliverable explicitly includes project documents.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 87

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> This rule applies to source code
> and to project documents.

The deliverable explicitly includes project documents.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 88

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> A project
> document is a file of project knowledge in the
> repository.

The definition does not change document work.

### Passage 89

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The project-wiki skill defines its location and its metadata.

The project document pointer retains its meaning.

Retained location: `mc/verbs/directives/worker.md — Cases`.

### Passage 90

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> You
> write the content of each project document.

The deliverable explicitly includes project documents.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 91

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> A later wiki update of the Scribe
> adds each project document to the navigation pages of the Project wiki.

This fact does not select a Worker action.

### Passage 92

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The Verifier permits three unsuccessful corrections per allowance.

This fact does not select a Worker action.

### Passage 93

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> A Verifier pass restores that allowance.

This fact does not select a Worker action.

### Passage 94

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Run retries preserve that allowance and use the task run failure allowance.

This fact does not select a Worker action.

### Passage 95

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Each merge conflict starts a merge repair.

This fact does not select a Worker action.

### Passage 96

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The agent runs of a correction start first, unless an approved task in the Project waits for its merge.

This fact does not select a Worker action.

### Passage 97

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The other tasks of the Project continue while a correction waits for an operator decision.

This fact does not select a Worker action.

### Passage 98

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Orchestrate by default.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 99

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Fan out these items to read-only, depth-1 subagents:

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 100

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - compressible investigation;

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 101

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - implementation alternatives;

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 102

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - self-checks.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 103

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Author the fan-out as a dynamic workflow.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 104

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Select from these reference
> patterns:

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 105

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Fanout-And-Synthesize;

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 106

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Adversarial Verification;

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 107

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Generate-And-Filter;

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 108

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - Tournament.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 109

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> You can also author any other orchestration.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 110

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> The condition is that you can
> discard the full work context of the sub-problem after a small artifact
> returns.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 111

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Examples of a small artifact are a brief, a verdict, and a ranked
> list.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 112

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> If you must hold the whole context to make the next step, do not spawn.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 113

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> This top-level run is the sole writer.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 114

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> This top-level run integrates the
> result.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 115

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Execute inline only when the work is trivially single-context.

The same delegation constraints use one compact section.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 116

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Do not state the
> reason as an extra `mc` flag.

Only permitted command forms can be called.

Retained location: `mc/verbs/directives.go — skillPointer / Your command surface`.

### Passage 117

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> ## The terminal action

The heading or connective text no longer organizes retained instructions.

### Passage 118

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> On success, submit exactly one terminal completion with the exact committed
> result.

Exact HEAD is a submission check.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 119

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> Use `--needs-operator` only for a decision that only the operator can
> make.

The terminal selection rule remains with its command.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A decision that only the operator can make`.

Pointer condition: Before a terminal, when only the operator can decide.

### Passage 120

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> Use `--infra` only for a task run failure.

The terminal selection rule remains with its command.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A task run failure`.

Pointer condition: Before a terminal, when an execution condition prevents completion.

### Passage 121

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Do not
> self-grade.

Authority limits remain universal.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 122

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Do not call the Verifier.

Authority limits remain universal.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 123

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Do not dispatch a successor.

Authority limits remain universal.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 124

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> Always end your turn with a terminal action.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 125

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> A failed orchestration is not an
> exemption.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 126

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> You owe one call in each of these cases:

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 127

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - a subagent errors;

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 128

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - a provider stream disconnects in the middle of a fan-out;

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 129

Source: `mc/verbs/directives/worker.md`. Category: `duplicate`.

> - you cannot complete your plan.

The contract states this obligation once.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 130

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> Complete the work with what you have if you can.

The skill selects the terminal after an execution failure.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A run that cannot give a result / A task run failure`.

Pointer condition: Before a terminal, when execution prevents completion.

### Passage 131

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> The `--checkpoint` terminal
> action keeps your commit for another Worker run, and the mc-worker skill holds
> that case.

The skill supplies incomplete-work submission.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: checkpoint`.

Pointer condition: Before a terminal, when the deliverable cannot finish in this run.

### Passage 132

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> Otherwise report
> `mc complete <task> --run <run_id> --infra --reason <what failed>`.

The skill selects the terminal after an execution failure.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A run that cannot give a result / A task run failure`.

Pointer condition: Before a terminal, when execution prevents completion.

### Passage 133

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> A silent exit is the one outcome that gives the system no information.

This fact does not select a Worker action.

### Passage 134

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> A task
> has a task run failure allowance of three failures, and a silent exit uses one.

This fact does not select a Worker action.

### Passage 135

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The third failure makes the task stalled with no diagnosis attached.

This fact does not select a Worker action.

### Passage 136

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> ## Failure controls

The heading or connective text no longer organizes retained instructions.

### Passage 137

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Failures before the agent process starts use the Project infrastructure allowance.

This fact does not select a Worker action.

### Passage 138

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> A failure after process start uses your assigned task's task run failure allowance.

This fact does not select a Worker action.

### Passage 139

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Each failure allowance permits three counted failures.

This fact does not select a Worker action.

### Passage 140

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> An operator decision report creates Decision Pending and consumes zero failure units.

This fact does not select a Worker action.

### Passage 141

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> A task resume preserves an independent Decision Pending condition.

This fact does not select a Worker action.

### Passage 142

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> The runner reports complete model responses and confirms connection recovery.

This fact does not select a Worker action.

### Passage 143

Source: `mc/verbs/directives/worker.md`. Category: `unneeded`.

> Connection recovery preserves the task outcome and its retry allowance.

This fact does not select a Worker action.

### Passage 144

Source: `mc/verbs/directives/worker.md`. Category: `moved behind a pointer`.

> Do not submit connection recovery reports yourself.

The skill leaves connection reports to the runner.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A task run failure`.

Pointer condition: Before a terminal after an execution condition.

### Passage 145

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> ---
> name: mc-worker
> description: The complete and only mc verb surface for the Worker role, including the submission checks and the procedure for each case that a run brief can start; load before any mc call and before submitting the terminal — mc ships no --help, and a verb absent from this skill does not exist for this role.
> ---

The pointer alone defines when to load and which commands are permitted.

Retained location: `mc/verbs/directives.go — skillPointer / Your command surface`.

### Passage 146

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `<run_id>` is the `Run identifier` fact of the **Run** part of your run brief.

The brief defines the run placeholder.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Run`.

### Passage 147

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Each `mc complete` call carries it as `--run <run_id>`.

The exact syntax already shows argument placement.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — command forms`.

### Passage 148

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `mc run infra-failure` takes it as its first argument.

The exact syntax already shows argument placement.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — command forms`.

### Passage 149

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `mc task block` and the reads take no run identifier.

The exact syntax already shows argument placement.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — command forms`.

### Passage 150

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `<task>` is the `Identifier` fact of the **Task** part.

The Task explanation defines the placeholder.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Task`.

### Passage 151

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `<id>` in a read is the identifier of any task.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Introduction`.

### Passage 152

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `mc` rejects a call whose run identifier does not match the live run.

The contract requires the assigned run identity.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 153

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> `mc` never applies a rejected call.

This system fact does not select a Worker action.

### Passage 154

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A terminal is an `mc` command that ends the run.

The contract states how the run ends.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 155

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A successful terminal ends the run.

The contract states how the run ends.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 156

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> `/workspace/source` is an independent repository for this run.

This system fact does not select a Worker action.

### Passage 157

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> It starts at the `Start commit` fact of the **Git state** part.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 158

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> It holds the history of the start commit and of the destination branch.

This system fact does not select a Worker action.

### Passage 159

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The `Destination branch` fact of the **Git state** part names that branch.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 160

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The Project repository stays outside your write access.

This system fact does not select a Worker action.

### Passage 161

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Culler retains the commit that you submit after the run folder disappears.

This system fact does not select a Worker action.

### Passage 162

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The `Base commit` fact names the commit where the first Worker run of the task started.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 163

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The start commit is the checkpoint commit, else the last submitted commit of the task, else the base commit.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 164

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> When the **Git state** part has no `Base commit` fact, this run is the first Worker run of the task.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 165

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Your repository then starts at the head of the destination branch, and that commit becomes the base commit.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 166

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The Project home retains dependency caches, installed tools, and Project tool configuration across runs.

This system fact does not select a Worker action.

### Passage 167

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The source workspace starts fresh at the assigned commit.

This system fact does not select a Worker action.

### Passage 168

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The runner executes the retained setup script before the agent starts.

This system fact does not select a Worker action.

### Passage 169

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Check dependency compatibility with the current declarations and the selected image.

The compatibility check remains once.

Retained location: `mc/verbs/directives/worker.md — Cases`.

### Passage 170

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Credentials, sessions, role skills, and instructions remain separate for each run.

This system fact does not select a Worker action.

### Passage 171

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Use the project-environment-setup skill for dependency changes.

The dependency pointer appears once.

Retained location: `mc/verbs/directives/worker.md — Cases`.

### Passage 172

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> These commands accept one JavaScript Object Notation (JSON) object on standard input:

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

### Passage 173

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The get request contains your `project_id`.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

### Passage 174

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The proposal contains the complete `configuration` and a plain-language `explanation`.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

### Passage 175

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `moved behind a pointer`.

> Write `explanation` for the operator in at most two short sentences.

The existing shared skill remains unchanged and supplies the proposal explanation rules.

Retained location: `mc/verbs/skills/shared/project-environment-setup/SKILL.md — Workspace dependencies and Image dependencies (via mc-worker / Project dependencies)`.

Pointer condition: Dependency changes that require an environment proposal.

### Passage 176

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `moved behind a pointer`.

> Identify the Image dependencies and why the Project needs them.

The existing shared skill remains unchanged and supplies the proposal explanation rules.

Retained location: `mc/verbs/skills/shared/project-environment-setup/SKILL.md — Workspace dependencies and Image dependencies (via mc-worker / Project dependencies)`.

Pointer condition: Dependency changes that require an environment proposal.

### Passage 177

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `moved behind a pointer`.

> Keep check evidence, diagnostic output, and continuation instructions separate from `explanation`.

The existing shared skill remains unchanged and supplies the proposal explanation rules.

Retained location: `mc/verbs/skills/shared/project-environment-setup/SKILL.md — Workspace dependencies and Image dependencies (via mc-worker / Project dependencies)`.

Pointer condition: Dependency changes that require an environment proposal.

### Passage 178

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Use the configuration object from the get response.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

### Passage 179

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Record the full committed source as `configuration.source_commit`.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

### Passage 180

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Commit the proposed Dockerfile and every build file before this report.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

### Passage 181

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The proposal preserves the selected environment until approval, preparation, and source acceptance.

This system fact does not select a Worker action.

### Passage 182

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> After a proposal, use the checkpoint terminal to retain your source.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

### Passage 183

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Culler resumes development after the setup report and source acceptance.

This system fact does not select a Worker action.

### Passage 184

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Repository test requirements still control task verification.

Task criteria still control verification.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 185

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Source delivery still requires the ordinary task review.

This system fact does not select a Worker action.

### Passage 186

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> If you execute the work inline and do not orchestrate, state the reason in your response before the terminal.

The inline explanation rule appears only in the directive.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 187

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Do not add an unsupported flag to carry the reason.

The inline explanation rule appears only in the directive.

Retained location: `mc/verbs/directives/worker.md — Orchestration`.

### Passage 188

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Culler keeps the transcript of the run, so your response stays on record.

This system fact does not select a Worker action.

### Passage 189

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Submit exactly one terminal.

The contract states the terminal and deliverable obligations.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 190

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Commit the complete deliverable first.

The contract states the terminal and deliverable obligations.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 191

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `<full HEAD sha>` is the full lowercase output of `git -C /workspace/source rev-parse HEAD`.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 192

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Do not pass an abbreviation, a branch name, or a commit that you did not read back from the repository.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 193

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The terminal examines the repository before it records anything.

This system fact does not select a Worker action.

### Passage 194

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The repository must satisfy four conditions:

This system fact does not select a Worker action.

### Passage 195

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> 1. Clean tree.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 196

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `git -C /workspace/source status --porcelain=v1 --untracked-files=all` must print nothing.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 197

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Untracked files count.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 198

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Commit or delete each scratch file, build output, log, and notes file.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 199

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> 2. Correct HEAD.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 200

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> HEAD must equal the commit that you pass.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 201

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> 3. Correct lineage.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 202

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The commit that you pass must be the start commit or a descendant of the start commit.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 203

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> If it is not, bring your commits onto the start commit with a rebase or a cherry-pick.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 204

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> 4. Changed result.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 205

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The commit that you pass must differ from the base commit.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 206

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The start commit satisfies this condition when it differs from the base commit.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 207

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Run the two checks yourself before you submit: `status` and `rev-parse HEAD`.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 208

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Nothing moves when the terminal refuses.

This system fact does not select a Worker action.

### Passage 209

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A resubmit after the fix is safe.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 210

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `--outputs` names your task execution report.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 211

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The report identifies the committed files and the completion evidence.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 212

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The Verifier receives its path.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 213

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Write the report at `/mc/artifacts/<plane>/<task>/output/<your-file>` and pass that absolute path.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 214

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A plane is one durable directory that Culler mounts under `/mc/artifacts/`.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 215

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Its content stays after the run ends, and the later roles mount the same plane.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 216

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> List `/mc/artifacts/` and use a plane directory that already exists.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 217

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The plane is writable, so create your folder with `mkdir -p`.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 218

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Do not write at `/mc/artifacts/<your-file>`.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 219

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Do not create a directory at that level.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 220

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `/mc/artifacts/` itself is ordinary container storage.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 221

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A write there succeeds, looks correct, and dies with your run.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 222

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> `/tmp`, `$TMPDIR`, and your working directory die with your run too.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 223

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The terminal opens the file before it records the path.

This system fact does not select a Worker action.

### Passage 224

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The terminal refuses the path in each of these cases:

This system fact does not select a Worker action.

### Passage 225

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - the path is outside a mounted plane;

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 226

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - the file is missing;

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 227

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - the file is empty;

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 228

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - the file is over 64 MiB;

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 229

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - the file is not a regular file.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 230

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The report does not replace the commit.

One reference applies the existing project-wiki rules without restating them.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 231

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The complete deliverable, including knowledge work and necessary local evidence, must be in the commit.

The contract covers documents and evidence in the commit.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 232

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Write original documents directly at the paths that the shared project-wiki skill defines.

One reference applies the existing project-wiki rules without restating them.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 233

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> OpenWiki is the tool that generates the wiki pages of the Project under `openwiki/`.

One reference applies the existing project-wiki rules without restating them.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 234

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> It owns each `index.md` file and its control records there.

One reference applies the existing project-wiki rules without restating them.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 235

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Keep those files unchanged during ordinary document work.

One reference applies the existing project-wiki rules without restating them.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — The task execution report`.

### Passage 236

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Two events end a run without a result.

This system fact does not select a Worker action.

### Passage 237

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The event selects the command.

This system fact does not select a Worker action.

### Passage 238

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Each command in this section ends your run, so submit exactly one.

The contract alone requires one terminal.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 239

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> No command here substitutes for the completed work.

This system fact does not select a Worker action.

### Passage 240

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> These are examples of such decisions:

The examples now need no separate introduction.

### Passage 241

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - Two criteria contradict each other.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A decision that only the operator can make`.

### Passage 242

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - A criterion has no measure that you can check.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A decision that only the operator can make`.

### Passage 243

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The reason states the decision that you need and why.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A decision that only the operator can make`.

### Passage 244

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The command sets Decision Pending on the task.

This system fact does not select a Worker action.

### Passage 245

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The operator resolves the decision with a requirements revision, or the operator cancels the task.

This system fact does not select a Worker action.

### Passage 246

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The two commands record the same decision.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A decision that only the operator can make`.

### Passage 247

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Use the first command.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A decision that only the operator can make`.

### Passage 248

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The second command applies to your own task only and takes no `--run` flag.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A decision that only the operator can make`.

### Passage 249

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Report the condition that prevents completion:

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A task run failure`.

### Passage 250

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The assigned task owns three task run failure attempts.

This system fact does not select a Worker action.

### Passage 251

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Each failed run after process start consumes one attempt.

This system fact does not select a Worker action.

### Passage 252

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The first failure permits an automatic retry after 30 seconds.

This system fact does not select a Worker action.

### Passage 253

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The second failure permits an automatic retry after two minutes.

This system fact does not select a Worker action.

### Passage 254

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The third failure sets `stalled`, which the dashboard shows as Retries Exhausted.

This system fact does not select a Worker action.

### Passage 255

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Forward stage progress or an operator task resume restores three attempts.

This system fact does not select a Worker action.

### Passage 256

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> A task resume preserves an independent Decision Pending condition.

This system fact does not select a Worker action.

### Passage 257

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Dispatch and launch failures before process start consume the Project infrastructure allowance.

This system fact does not select a Worker action.

### Passage 258

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> A required operator decision consumes zero failure or correction attempts.

This system fact does not select a Worker action.

### Passage 259

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Only direct execution observations can establish a connection condition.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A task run failure`.

### Passage 260

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> An agent report retains an unknown cause.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A task run failure`.

### Passage 261

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> ### Case: correction round

The heading no longer organizes a retained section.

### Passage 262

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> A **Correction from the Verifier** part means that the Verifier rejected the last submission.

This system fact does not select a Worker action.

### Passage 263

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> **The `Correction file` fact is an absolute path to a real file.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 264

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Open it with ordinary file reads.**

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 265

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> No `mc` verb returns its contents.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 266

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Culler stores the path, not the prose.

This system fact does not select a Worker action.

### Passage 267

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The file lives under `/mc/artifacts/<plane>/<task>/corrections/`.

This system fact does not select a Worker action.

### Passage 268

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The `Evidence file` fact names the Verifier's evidence under `verification/`.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 269

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Read that evidence too.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 270

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Read the correction file before you change anything.

The pointer makes the correction procedure precede changes.

Retained location: `mc/verbs/directives/worker.md — Cases`.

### Passage 271

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The file names unmet criteria or task regressions and their evidence.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 272

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Resolve task regressions even when they affect checks outside the acceptance criteria.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 273

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> New or changed tests must pass.

Changed tests must pass in every run.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 274

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> If the path is missing or unreadable, report a task run failure.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 275

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Do not guess.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 276

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Report `--infra` and state the failure.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: correction round`.

### Passage 277

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> ### Case: merge repair

The heading no longer organizes a retained section.

### Passage 278

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Culler merges each submitted commit with the destination branch.

This system fact does not select a Worker action.

### Passage 279

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A **Merge candidate** part means that the merge had a conflict.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 280

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The part holds one JSON record with these fields:

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 281

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> - the `status` field is `conflict`;

This system fact does not select a Worker action.

### Passage 282

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - the `submitted_commit` field and the `destination_commit` field name the two merge inputs;

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 283

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - the `conflict_paths` field names each path with a conflict.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 284

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Read the two changes at each conflict path.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 285

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Then decide whether one commit can keep the intent of both changes.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 286

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - When one commit can keep both intents, merge the destination commit into your work and resolve each conflict.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 287

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Your commit must preserve both merge inputs.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 288

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Commit no file that contains a conflict marker.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 289

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - When the two changes contradict, keep your work unchanged.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 290

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Report the decision for the operator through the `--needs-operator` terminal.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 291

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Name each conflict path and the intent of each change.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 292

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The operator records the decision in the description.

This system fact does not select a Worker action.

### Passage 293

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The next Worker run receives the revised description and the same **Merge candidate** part.

This system fact does not select a Worker action.

### Passage 294

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The same repair procedure applies before and after operator approval.

This system fact does not select a Worker action.

### Passage 295

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> A merge repair clears the operator approval of the task.

This system fact does not select a Worker action.

### Passage 296

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The repaired version requires verification, a new review packet, and a new operator decision.

This system fact does not select a Worker action.

### Passage 297

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> A later conflict starts another merge repair.

This system fact does not select a Worker action.

### Passage 298

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Your repair must preserve complete Project documents and one consistent set of accepted OpenWiki control records.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 299

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The Verifier assesses the repaired commit again.

This system fact does not select a Worker action.

### Passage 300

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> ### Case: revision request

The heading no longer organizes a retained section.

### Passage 301

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The **Revision request** part holds the request of the operator.

The generated part identifies the request.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Revision request`.

### Passage 302

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The operator returned the review packet with this request.

The generated part identifies the request.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Revision request`.

### Passage 303

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> You must meet the request in full in this run.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: revision request`.

### Passage 304

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Then submit the commit.

The success terminal applies after the work meets the criteria.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 305

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> ### Case: requirements revision

The heading no longer organizes a retained section.

### Passage 306

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A **Requirements revision** part means that the operator changed the requirements of the task.

The generated part identifies the reason.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Requirements revision`.

### Passage 307

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The part holds the reason for the change.

The generated part identifies the reason.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Requirements revision`.

### Passage 308

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The reason explains why the operator changed the requirements.

The generated part identifies the reason.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Requirements revision`.

### Passage 309

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The reason is context and adds no requirement.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: requirements revision`.

### Passage 310

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The description of the **Task** part holds every requirement.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: requirements revision`.

### Passage 311

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> 1. Check the start commit against each criterion of the description.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: requirements revision`.

### Passage 312

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> 2. Change the work where a criterion fails.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: requirements revision`.

### Passage 313

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> 3. Submit the commit.

The success terminal applies after the work meets the criteria.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 314

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> When the start commit meets each criterion and differs from the base commit, submit the start commit unchanged.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Task terminal`.

### Passage 315

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A **Merge candidate** part in the same run brief means that the merge repair remains open.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: requirements revision`.

### Passage 316

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Do the merge repair before you submit.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: requirements revision`.

### Passage 317

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> When the description states a decision about the conflict, apply that decision in the repair.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 318

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The checkpoint terminal action asks Culler to keep your commit for another Worker run.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: checkpoint`.

### Passage 319

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Use it when this run cannot finish the deliverable.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: checkpoint`.

### Passage 320

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The terminal action applies the same four repository conditions.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: checkpoint`.

### Passage 321

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Culler retains the checkpoint commit.

The checkpoint purpose and continuation rule already state this.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: checkpoint`.

### Passage 322

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The task stays with the Worker, and the next Worker run starts at the checkpoint.

The checkpoint purpose and continuation rule already state this.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: checkpoint`.

### Passage 323

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A `Checkpoint version` fact in the **Git state** part means that an earlier Worker run saved a checkpoint.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: checkpoint`.

### Passage 324

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Your repository starts at that checkpoint.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: checkpoint`.

### Passage 325

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Continue the work from that commit.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: checkpoint`.

### Passage 326

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> ## Out of scope

The heading no longer organizes a retained section.

### Passage 327

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Culler and the operator dispatch, verify, package, merge, and grade your work.

Authority prohibitions and command discovery each have one location.

Retained location: `mc/verbs/directives/worker.md — Culler contract; mc/verbs/directives.go — skillPointer / Your command surface`.

### Passage 328

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> A probe for a verb not listed here wastes the run.

Authority prohibitions and command discovery each have one location.

Retained location: `mc/verbs/directives/worker.md — Culler contract; mc/verbs/directives.go — skillPointer / Your command surface`.

### Passage 329

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The runner owns `mc run model-response` and `mc homie model-response`.

The allowed command forms exclude runner commands.

Retained location: `mc/verbs/directives.go — skillPointer / Your command surface`.

### Passage 330

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> These commands record complete model responses for the current attempt.

This system fact does not select a Worker action.

### Passage 331

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Do not call these runner commands.

The allowed command forms exclude runner commands.

Retained location: `mc/verbs/directives.go — skillPointer / Your command surface`.

### Passage 332

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Continue the assigned work after connection recovery.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — A task run failure`.

### Passage 333

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The Culler contract has priority over custom instructions.

The hierarchy has one location.

Retained location: `mc/verbs/directives/worker.md — Project role customization`.

### Passage 334

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Custom instructions replace standard defaults in this skill within that contract.

The hierarchy has one location.

Retained location: `mc/verbs/directives/worker.md — Project role customization`.

### Passage 335

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Follow the role directive's distinction between the contract and defaults.

The hierarchy has one location.

Retained location: `mc/verbs/directives/worker.md — Project role customization`.

### Passage 336

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> If a custom instruction conflicts with the contract during work, submit this terminal:

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project role customization`.

### Passage 337

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The JavaScript Object Notation (JSON) object contains these fields:

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project dependencies`.

### Passage 338

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - The `run` field: your run identifier.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project role customization`.

### Passage 339

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - The `revision` field: the customization revision from the run brief.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project role customization`.

### Passage 340

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - The `passage` field: the exact conflicting text from that revision.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project role customization`.

### Passage 341

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> - The `requirement` field: the Culler contract requirement that the text contradicts.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Project role customization`.

### Passage 342

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The command records the Project, role, and subject from your run.

This system fact does not select a Worker action.

### Passage 343

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> It ends the run with a customization conflict and preserves the subject's stage and accepted work.

This system fact does not select a Worker action.

### Passage 344

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> A run without a subject blocks that role within its Project.

This system fact does not select a Worker action.

### Passage 345

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The Project retains its pause state.

This system fact does not select a Worker action.

### Passage 346

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The command preserves all failure and correction allowances.

This system fact does not select a Worker action.

### Passage 347

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> ## Correction allowances

The heading no longer organizes a retained section.

### Passage 348

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The Verifier correction allowance permits three unsuccessful corrections.

This system fact does not select a Worker action.

### Passage 349

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> A Verifier pass restores that allowance.

This system fact does not select a Worker action.

### Passage 350

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Run retries and an operator packet revision preserve that allowance.

This system fact does not select a Worker action.

### Passage 351

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> Each merge conflict starts a merge repair.

This system fact does not select a Worker action.

### Passage 352

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> A merge repair clears the operator approval of its task.

This system fact does not select a Worker action.

### Passage 353

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The agent runs of a correction start first, unless an approved task in the Project waits for its merge.

This system fact does not select a Worker action.

### Passage 354

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `unneeded`.

> The other tasks of the Project continue while a correction waits for an operator decision.

This system fact does not select a Worker action.

### Passage 355

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> The merge repair must preserve both intents where they agree.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 356

Source: `mc/verbs/skills/worker/mc-worker/SKILL.md`. Category: `duplicate`.

> Contradictory intents require an operator decision that identifies each path and both intents.

The section consolidates this meaning into the retained instruction.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 357

Source: `mc/verbs/brief.go — Agent credentials`. Category: `unneeded`.

> Culler keeps provider credentials on the host. This container uses a temporary Culler token through the host provider proxy.

The Worker cannot act on this system description.

### Passage 358

Source: `mc/verbs/brief.go — Project role customization`. Category: `duplicate`.

> These operator instructions replace standard defaults within the Culler contract. A contract conflict ends the run with a customization conflict.

The final Worker explanation identifies the data; the procedure stays at its retained location.

Retained location: `mc/verbs/directives/worker.md — Project role customization; mc/verbs/skills/worker/mc-worker/SKILL.md — Project role customization`.

### Passage 359

Source: `mc/verbs/brief.go — Run`. Category: `duplicate`.

> This value identifies this run. It is the `<run_id>` value of each `mc` command.

The final Worker explanation identifies the data; the procedure stays at its retained location.

Retained location: `mc/verbs/brief.go — trimWorkerBrief / Run`.

### Passage 360

Source: `mc/verbs/brief.go — Project environment`. Category: `unneeded`.

> Culler supplies the selected image and persistent Project home. A task run receives a fresh source workspace and dependency preparation before the agent starts.

The Worker cannot act on this system description.

### Passage 361

Source: `mc/verbs/brief.go — Task`. Category: `duplicate`.

> Meet each criterion in the description and pass new or changed tests. Resolve task regressions that the Verifier identifies.

The final Worker explanation identifies the data; the procedure stays at its retained location.

Retained location: `mc/verbs/directives/worker.md — Culler contract`.

### Passage 362

Source: `mc/verbs/brief.go — Task fact`. Category: `unneeded`.

> Unsuccessful Verifier corrections

The Worker cannot act on this system description.

### Passage 363

Source: `mc/verbs/brief.go — Correction allowances`. Category: `unneeded`.

> The Verifier correction allowance limits rejected corrections. These rules govern returned work.

The Worker cannot act on this system description.

### Passage 364

Source: `mc/verbs/brief.go — Correction allowances`. Category: `unneeded`.

> The initial Verifier rejection starts a sequence at zero. Three subsequent rejected corrections require an operator grant.

The Worker cannot act on this system description.

### Passage 365

Source: `mc/verbs/brief.go — Correction allowances`. Category: `unneeded`.

> A Verifier pass restores the allowance. A requirements revision or explicit grant also restores it.

The Worker cannot act on this system description.

### Passage 366

Source: `mc/verbs/brief.go — Correction allowances`. Category: `unneeded`.

> Each merge conflict starts a merge repair. A merge repair clears the operator approval of its task.

The Worker cannot act on this system description.

### Passage 367

Source: `mc/verbs/brief.go — Correction allowances`. Category: `unneeded`.

> An operator packet revision preserves the allowance. A run retry uses the separate task run failure allowance.

The Worker cannot act on this system description.

### Passage 368

Source: `mc/verbs/brief.go — Operator allowance explanation`. Category: `unneeded`.

> The operator authorized more work for the unresolved condition. Independent controls remain effective.

The Worker cannot act on this system description.

### Passage 369

Source: `mc/verbs/brief.go — Requirements revision`. Category: `duplicate`.

> The operator changed the requirements of this task for this reason. The description holds every requirement.

The final Worker explanation identifies the data; the procedure stays at its retained location.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: requirements revision`.

### Passage 370

Source: `mc/verbs/brief.go — Revision request`. Category: `duplicate`.

> The operator returned the review packet with this request. You must meet the request in this run.

The final Worker explanation identifies the data; the procedure stays at its retained location.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: revision request`.

### Passage 371

Source: `mc/verbs/brief.go — Merge candidate`. Category: `duplicate`.

> The merge with the destination branch had a conflict. Keep the intent of both changes in one commit, or report an operator decision when the changes contradict.

The final Worker explanation identifies the data; the procedure stays at its retained location.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Case: merge repair`.

### Passage 372

Source: `mc/verbs/brief.go — Correction from the Verifier`. Category: `duplicate`.

> The Verifier rejected the last submission. The mc-worker skill holds the correction procedure.

The final Worker explanation identifies the data; the procedure stays at its retained location.

Retained location: `mc/verbs/directives/worker.md — Cases`.

### Passage 373

Source: `mc/verbs/brief.go — Git state`. Category: `unneeded`.

> These are the Git facts of this work.

The Worker cannot act on this system description.

### Passage 374

Source: `mc/verbs/brief.go — Git state, when a start commit exists`. Category: `duplicate`.

> Your repository at `/workspace/source` starts at the start commit.

The final Worker explanation identifies the data; the procedure stays at its retained location.

Retained location: `mc/verbs/skills/worker/mc-worker/SKILL.md — Your repository`.

### Passage 375

Source: `mc/verbs/brief.go — Failure controls`. Category: `unneeded`.

> Execution problems and operator decisions use separate controls. A confirmed connection condition uses the connection controls.

The Worker cannot act on this system description.

### Passage 376

Source: `mc/verbs/brief.go — Failure controls`. Category: `unneeded`.

> The runner confirms connection recovery from complete model responses. Recovery preserves the task outcome and its retry allowance.

The Worker cannot act on this system description.

### Passage 377

Source: `mc/verbs/brief.go — Failure controls`. Category: `unneeded`.

> Dispatch and launch failures use the Project infrastructure allowance before the agent process starts.

The Worker cannot act on this system description.

### Passage 378

Source: `mc/verbs/brief.go — Failure controls`. Category: `unneeded`.

> Strategist proposal creation and Arbiter proposal review share that Project allowance throughout their runs.

The Worker cannot act on this system description.

### Passage 379

Source: `mc/verbs/brief.go — Failure controls`. Category: `unneeded`.

> Assigned task failures use their task run failure allowance after the agent process starts.

The Worker cannot act on this system description.

### Passage 380

Source: `mc/verbs/brief.go — Failure controls`. Category: `unneeded`.

> Each failure allowance permits three counted failures. An operator decision report consumes zero failure units.

The Worker cannot act on this system description.

### Passage 381

Source: `mc/verbs/directives.go — skillPointer / Your command surface`. Category: `duplicate`.

> Every `mc` command available to you, with its exact flags and constraints, is the mc-worker skill, installed in this container at `~/.agents/skills/mc-worker/SKILL.md`. Load it before your first `mc` call and again before you submit your terminal.

The shorter pointer retains the same path and load conditions.

Retained location: `mc/verbs/directives.go — skillPointer / Your command surface`.

### Passage 382

Source: `mc/verbs/directives.go — skillPointer / Your command surface`. Category: `duplicate`.

> `mc` ships no help surface: bare `mc` names zero verbs and `mc --help` is an unknown verb. That skill is the complete surface your role may call. A verb absent from it does not exist for you, and a search for one wastes the run.

The contract retains the allowed-command boundary; the pointer states the absence of a help command.

Retained location: `mc/verbs/directives.go — skillPointer / Your command surface`.

## Merge repair

The destination commit is `a7292ccef4b2d3b222cf0cc56d2eda6eb091b2ad`.
Both conflict paths add this original passage: “The agent profile sets the model and effort for this run.”

| Source | Original text | Category | Retained location |
| --- | --- | --- | --- |
| `mc/verbs/directives/worker.md` at the destination | The agent profile sets the model and effort for this run. | duplicate | None |
| `mc/verbs/skills/worker/mc-worker/SKILL.md` at the destination | The agent profile sets the model and effort for this run. | duplicate | None |

The Worker directive and the skill omit this passage.
The agent profile sets the model and effort of each Worker run.
All destination runtime changes remain intact.
The merge introduces no further Worker brief explanations or pointers.
The assigned-base counts above still use the original base, not the destination commit.

The other-role digest test uses the destination input as its baseline.
A temporary inverse check removed only the destination's two profile-text additions and reproduced all five original digests.
This check passed before the test received the destination digests.
The Worker delivery test requires zero profile references across the rendered brief and command skill.
The directive test requires that the command skill omits the agent profile.

## Merge repair validation

`./mc/check.sh` exited 1 with one failure: `TestValidateHomeAcceptsTheRealOperatorHome`.
All other Go packages passed, including `mc/verbs` in 111.796 seconds.
All new and changed tests passed.
Formatting, all vet configurations, deployment checks, and all three shell contract suites passed.
The separate Linux filesystem guard command exited 0 with two skipped tests.
The isolated operator-home test exited 1 with the same mount-root rejection.
The boundary package, refusal package, and Go module declarations match both the assigned base and destination.
The unavailable macOS checks and three boundary skips remain those listed under Initial submission validation.

The supplied checkout directories had mode 0777.
The first two check attempts stopped at the installed-host-release test because those directories permitted writes by other users.
Removing group and other write permissions from tracked-file parent directories resolved that setup failure.
This permission preparation changes no tracked file content or executable bit.

A read-only subagent accepted the merge resolution and final test changes.
No runtime authority, command behavior, or shared skill changed relative to the destination.
The Bun, browser, Docker, and nightly execution lanes were not run for this instruction repair.

The separate guard command reported:

```text
UNVERIFIED mc/substrate TestOpenRefusesASpineOutsideTheLockDomain: This test requires a test directory on a block-device filesystem: refusing to open the spine under /tmp/TestOpenRefusesASpineOutsideTheLockDomain1127129673/001: its mount / is fstype=overlay source=overlay, which is not a block-device-backed local filesystem — the spine must live on a runtime-local named volume, because a shared host path splits its locks across two kernels and corrupts it
UNVERIFIED mc/substrate TestOpenReadOnlyRefusesASpineOutsideTheLockDomain: This test requires a test directory on a block-device filesystem: refusing to open the spine under /tmp/TestOpenReadOnlyRefusesASpineOutsideTheLockDomain3045592539/001: its mount / is fstype=overlay source=overlay, which is not a block-device-backed local filesystem — the spine must live on a runtime-local named volume, because a shared host path splits its locks across two kernels and corrupts it
```
