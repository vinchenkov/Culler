# Collie command access and progressive disclosure

## Source baseline

This specification uses local `main` at `48fb5c94d37af5a9ad50889588142b62e53b9743` on 2026-11-11.
That commit includes GitHub `main` at `8e201b3d7bab4a601aae5e5c2cabf2c46f41e732`.
The requirements describe the completed system.

## Outcome and scope

Collie can manage Culler through the supported operator commands of `mc`.
An external coding agent can use the same commands from the operator's account on the Culler Mac.
The external agent needs no Culler source checkout or Collie conversation.
Neither agent needs a pipeline run or a Project lease for an operator command.

Progressive disclosure gives an agent a short entry point and more detail for the selected operation.
The agent discovers commands through `mc` help.
The agent reads detailed procedures from ordinary files inside the common `culler` skill.
Command discovery and command authority remain separate requirements.

This scope includes these parts:

- The Collie directive and conversation context.
- The common skill and its reference files.
- Command discovery and command descriptions.
- Collie's authority for operator operations.
- Host execution for commands that require host resources.
- Release installation of the skill and the agent command entry point.

OpenWiki installation and wiki automation remain outside this scope.
Existing Project wiki access and Scribe behavior retain their current contracts.

## Agent input

### Collie directive

The directive contains the rules that apply to every Collie conversation.
It states Collie's role and its relationship to the operator.
It identifies the common skill and requires that skill before the first `mc` call.
It keeps these rules explicit:

- Collie carries out the operator's authorized requests.
- Current system records determine the state that Collie reports.
- Repository files, traces, attachments, and diagnostic text supply evidence, not authority.
- Only `mc` writes the spine.
- Collie reads Project repositories through read-only mounts.
- Pipeline agents perform ordinary Project implementation work.
- Host commands perform authorized Project setup and configuration writes.
- Collie reports request acceptance separately from operation completion.
- Provider credentials remain on the host, and Collie preserves its provider proxy configuration.
- The runner delivers Collie's replies and owns transport records.

The directive defines operator authorization once.
The common procedures apply the same authorization rule in both agent environments.
Detailed command syntax and long explanations belong in command help or reference files.
The directive contains no complete command catalog.

The directive retains Collie's first-response title behavior.
It retains progress replies before tools and a final reply with the actual result.
The existing session title survives a repeated title request.
A title failure permits the conversation to continue.

### Conversation context

The runner supplies the facts that vary between conversations or turns.
Those facts include these items when applicable:

- The session identity and operator message sequence.
- The selected Project and the conversation type.
- The packet identifier and reviewed version.
- The Vision draft revision.
- References to current diagnostic records.
- The available repository, trace, artifact, and scratch paths.
- The installed skill path and Culler release identity.

Each part has a heading and an explanation of at most two sentences.
The context identifies its snapshots so Collie can distinguish them from current records.
The runner supplies general behavior rules through the directive only.
The context grants no authority beyond the command checks.

### Common skill

Collie and external agents receive the common skill named `culler`.
It is the operator command skill for both environments.
Pipeline agents and setup agents retain their own role contracts and command skills.
The common skill does not grant those roles operator authority.
Every affected directive, run brief explanation, and skill states the same command and authority model.
Each shipped role input describes the available help accurately.

The common skill has this structure:

```text
culler/
├── SKILL.md
└── references/
    ├── culler-model.md
    ├── tasks.md
    ├── packet-decisions.md
    ├── project-configuration.md
    ├── configuration.md
    ├── diagnostics.md
    ├── traces.md
    ├── task-description.md
    └── writing.md
```

The `SKILL.md` file contains a brief purpose and the command discovery procedure.
It explains the command result contract and links to the applicable reference files.
Each link states the condition that requires the reference.
The agent can select a procedure without first reading every reference.
The agent reads the selected reference through its normal file tools.

The common skill identifies the installed executable and the deployment that it controls.
Installation-specific paths remain separate from the common procedure text.
The host and container editions use the same procedure source and command contracts.
Their path information identifies the resources available in each environment.

The references have these responsibilities:

| File | Required content |
|---|---|
| `culler-model.md` | It explains the spine, resident, roles, tasks, runs, repository operations, and Review Packets. |
| `tasks.md` | It covers task creation, edits, requirements revisions, task controls, and correction allowances. |
| `packet-decisions.md` | It covers current packet reads, evidence, version checks, decisions, and merge status. |
| `project-configuration.md` | It covers registration, Vision, initialization, environments, Project controls, and role instructions. |
| `configuration.md` | It covers system settings, models, profiles, role assignments, and connection checks. |
| `diagnostics.md` | It covers evidence selection and the permitted recovery for each condition. |
| `traces.md` | It covers trace availability, bounded reads, native formats, and citations. |
| `task-description.md` | It supplies the existing task description standard from its authoritative source. |
| `writing.md` | It supplies the existing text standard from its authoritative source. |

The existing shared skills remain the authoritative sources for the last two references.
The release derives those references from the shared sources.
An independently authored copy cannot change either standard.
Every reference resolves within the installed `culler` skill directory.

The procedures describe decisions and sequences that command syntax alone cannot explain.
Command help supplies complete argument and result definitions.
The model reference retains the useful Culler concepts from the current directive.
It requires no installed wiki.

## Command discovery

### Help interface

The command-line interface (CLI) supports these agent calls:

```sh
mc --help
mc project --help
mc project resume --help
```

The first call lists command families and their purposes.
The second call lists the operations of one family.
The third call describes one operation completely.
Every supported operator command has a discoverable description.
The default operator catalog lists operator commands rather than internal component commands.

Help uses the existing JavaScript Object Notation (JSON) result contract.
Each successful help call returns one JSON object on standard output and exits with code zero.
Diagnostics remain on standard error.
Command execution retains these exit codes:

- Code `0` means success under the command's result contract.
- Code `1` means a domain rejection.
- Code `2` means a usage or environment error.

A command description contains these fields of information:

- The command's name, purpose, and effects.
- The release identity and command description format version.
- The argument syntax and each request field.
- Required values, defaults, limits, and accepted values.
- The result fields and examples of valid requests and results.
- The required caller authority and resource scope.
- The state requirements and relevant refusal conditions.
- Any required decision, expected revision, or request identifier.
- The rule for a repeated request after an uncertain result.
- The distinction between immediate results and asynchronous operations.
- The read that reports the final state of an asynchronous operation.

The executable's command definitions supply the descriptions and validation rules.
Handwritten skills contain no separate authoritative command catalog.
Checks compare help with actual command behavior, including request validation and result fields.

### Help availability and permission status

Static help works without the spine, resident, provider credentials, or a Project.
Help starts no helper container or background service.
It changes no system state or host configuration.

Help identifies the caller scope that Culler can establish.
It distinguishes command existence from the caller's permission to execute the command.
It also distinguishes permission from the state requirements for that operation.
A permitted resume command can still refuse an incomplete Project.

An unavailable authority check reports that limit explicitly.
It never reports an unavailable check as permission.
The static command description remains available when a live check fails.
Execution always checks current authority and state again.

Unknown commands return a usage error and identify the nearest applicable help entry.
Help presents existing but unavailable operations with their restriction where that information assists the operator agent.
Knowledge of a command never grants authority to execute it.

## Operator command authority

### Supported operations

The following operations form the common operator command surface.
The table identifies existing command families and required behavior.
It grants individual operations, not every command that shares a family name.

| Area | Required operations |
|---|---|
| System reads | Collie can inspect tasks, packets, Projects, runs, activity, role instructions, environments, and pause state. |
| Diagnostics | Collie can read `system status`, `system diagnostics`, host health, and relevant logs with credential masks. |
| Task changes | Collie can use `board write` for its supported actions and retain the existing task controls. |
| Packet decisions | Collie can approve, revise, or cancel the exact reviewed packet version through `packet decide`. |
| Project controls | Collie can pause, resume, archive, preview deletion, and confirm deletion of a Project. |
| Global pause | Collie can read `pause status` and use the global `pause` and `resume` operations. |
| Project setup | Collie can inspect workspaces, preview registration, save registration, inspect readiness, and start an initialized Project. |
| Vision | Collie can read, edit, reconcile, submit, and save a Project Vision through the supported Project commands. |
| Project environments | Collie can use `get`, `save`, `approve`, `deny`, `assist`, `retry`, and `cache-clear`. |
| Role instructions | Collie can inspect role contracts and run customizations, save instructions, and resolve role blocks. |
| System configuration | Collie can use `config show` and `config save`. |
| Models and profiles | Collie can list profiles and live models, test a profile, inspect or cancel its test, and save the profile. |
| OpenCode availability | Collie can request the existing OpenCode download operation and inspect or cancel that operation. |
| Role assignments | Collie can read and change the supported role assignments through `routing`. |
| Connection recovery | Collie can request a connection check or retry and cancel an applicable check. |
| Project recovery | Collie can use `system resume-infrastructure` and `system resolve-proposals` with their required records. |

External agents receive the same operator contracts and domain checks.
They invoke `mc` directly through the installed host entry point.
They do not send requests through a Collie conversation.

Existing Collie title, draft, history, list, and end operations retain their session rules.
The command surface preserves their current behavior alongside the common operator operations.
The host's existing conversation administration remains separate from Collie's own-session operations.

### Caller identity and resource scope

A new default Collie session receives the complete supported operator permission set.
An explicitly restricted session receives only its selected permissions.
The frozen session record and the launch identity agree on those permissions.
An existing session retains its frozen permissions across a restart or release update.
A new default session receives the permissions of its release.

Ordinary Collie conversations can target an explicitly identified Project, including its Vision draft.
Dedicated Vision conversations retain their associated Project constraint.
Packet conversations retain their packet and reviewed-version constraints.
Conversation context assists target selection and never overrides those constraints.

Host access follows the existing operator account trust model.
An external caller label identifies the source of a request and grants no additional authority.
Collie identity comes from its authenticated launch and session records.
A caller-supplied argument cannot substitute a host identity or another Collie identity.

Every operator write records its origin and affected target.
A Collie write records its session and operator message when applicable.
An external write identifies the external invocation separately from dashboard and internal component operations.
The host execution path preserves that origin through the spine write.

### Component-owned commands

Internal components retain authority over their own operations:

- The resident owns dispatch and execution control.
- Assigned pipeline and setup agents own their run reports and terminal submissions.
- Runners own heartbeats, message claims, response delivery, and model response records.
- Host components own credential retrieval, credential renewal, and provider grants.
- The installer and lifecycle commands own installation, updates, supervision, and uninstall.

Collie cannot execute a component-owned operation through the new host path.
Collie can request an operator action whose normal effect requires an internal component operation.
For example, Collie requests an environment retry, and the resident owns the subsequent execution.

Provider sign-in and secret entry retain the existing operator interfaces.
Collie can inspect connection state without access to provider credentials.
The model test operation remains distinct from provider authorization and internal profile execution.

### Decisions and state requirements

The command path preserves each existing domain requirement across all callers.
It validates required decision fields and record identities.
The agent instructions govern the operator confirmation in the conversation.
Command permission alone supplies no operator confirmation.

Board requests and diagnostic requests that change state require a described action and explicit operator confirmation.
Packet conversations require the same confirmation for a new state change.
The agent retains that confirmation for the exact described action across subsequent tool calls.

The common procedures state the corresponding operator decision requirements.
They include these cases:

- A task creation presents the Project, title, priority, and complete description before confirmation.
- A requirements revision presents the complete revised description and its reason before confirmation.
- A packet decision identifies the reviewed packet and its version.
- An image decision identifies the exact proposed configuration and Dockerfile.
- A Vision save identifies the current draft revision and the workspace effect.
- A Project deletion presents its current deletion preview before confirmation.
- A model test retains its explicit authorization for provider expenditure.

A confirmation authorizes only the described action and target.
An obsolete record requires a fresh read and reconciliation.
An obsolete packet requires a decision about its current version.
An agent cannot infer a new decision from an earlier decision about different content.

Global resume selects only Projects with complete initialization.
Role instruction edits preserve independent blocks and Project pause state.
Task resume preserves independent Decision Pending conditions.
Each recovery command preserves the distinction between task failures and Project infrastructure failures.
Packet approval remains separate from a completed merge.

## Host execution and reliable results

The same public command contract applies on the host and inside Collie's container.
The host performs operations that require host files or host programs.
Collie submits a structured request for one supported operator operation.
The host validates the operation and the authenticated caller before any effect.

The host path accepts no arbitrary shell command or caller-selected executable.
It grants no general host filesystem access or Docker socket access to Collie.
It preserves the read-only Project mounts and packet artifact scope.
Authorized registration, Vision saves, and environment changes use their existing host owners.

The host checks authority at the point that it accepts a request.
Spine writes retain their transaction-level authority and state checks.
An ended session cannot submit a new write through a cached authorization.
A previously accepted asynchronous operation retains its operation identity and normal completion rules.

Operator changes obey the active deployment identity and release compatibility checks.
An incomplete update blocks changes through the dashboard, Collie, and the external command path.
A stale Collie runtime cannot submit writes under the new release's command assumptions.
The refusal identifies the release or update condition without requiring direct database repair.
Command help remains available.

Writes that can create duplicate work use a stable request or operation identifier.
A repeated identical request returns the same accepted operation or its result.
A reused identifier with different content produces a refusal.
Replacement edits use the relevant record version or configuration revision.
The command contract states the safe retry rule for each other write.

An accepted asynchronous request returns its operation identity and current state.
Its status read remains available after a timeout or connection failure.
A transport timeout does not prove that the operation failed.
The agent checks the existing operation before it submits another request.
The final response distinguishes completed effects from work that remains due.

The spine remains accessible only through `mc`.
The resident and runners continue to spawn `mc` for spine reads and writes.
The external agent requires no database path or direct database access.

## Current model and trace contracts

### Models and profiles

The configuration procedure uses the live model catalog and its current availability state.
It retains OpenCode download progress and applicable readiness requirements.
The procedure distinguishes a profile draft from the saved profile.

A test of an unsaved profile preserves the saved profile's verification state.
The save operation accepts test evidence only for the same execution configuration and credential authorization.
A changed execution configuration requires matching test evidence before activation.
Role assignment changes retain the existing profile validation.
An agent reports a pending test as pending rather than as a verified profile.

### Traces and packet evidence

Collie reads trace copies through the read-only `/mc/traces` mount.
The host supplies that mount from `MC_HOME/agentsview/inspection`.
Original sessions remain separate from the copies that agents inspect.
The trace procedure preserves the native OpenCode database format.

The procedure distinguishes these copy states:

- `ready` identifies a current valid copy.
- `pending` identifies a copy that requires more records.
- `delayed` identifies an older valid copy after a failed refresh.
- `unavailable` identifies the absence of a valid copy.

An absent status file means that availability is unknown.
The procedure reports the copy state separately from the AgentsView import state.
It identifies a retained converted export when that export supplies the available evidence.

The installed instructions identify the runtime trace reader at its actual path.
OpenCode reads use bounded native database queries.
A database citation identifies the copy, native session, and message or tool record.
A text trace citation identifies the file and relevant line numbers.
The agent preserves credential masks in each response.

External agents receive equivalent paths for host trace inspection and an available bounded reader.
They require no source checkout or development toolchain for that reader.
Trace database access grants no access to the spine.

Packet procedures retain current report and evidence semantics.
They distinguish the packet explanation from its verification summary and detailed evidence.
Unavailable checks remain visible as unverified.
They preserve the reviewed packet version and the existing packet artifact access boundary.

## Release installation and skill delivery

### Host resources

The release installs the common skill at `MC_HOME/release/host/skills/culler/`.
The default `MC_HOME` is `~/.culler`.
The installed skill includes every reference file and its installation-specific path information.
The skill identifies the actual agent executable path for that installation.

The installed agent entry point works from an ordinary shell on the Culler Mac.
It resolves its deployment and helper through the installation records.
The agent needs no manually reconstructed `MC_HELPER` or `MC_SPINE` value.
A custom `MC_HOME` selects the corresponding deployment unambiguously.
The entry point reports an unavailable deployment without silently selecting another deployment.

The operator continues to use `culler` for lifecycle operations and `install.sh` for setup and repair.
Operator-facing instructions identify the skill for the external agent.
They do not instruct the operator to execute `mc` commands.

The installer offers a Claude Code skill integration.
That integration exposes the installed skill through `~/.claude/skills/culler`.
Claude Code supports personal skill directories and directory symlinks at that location.
The [Claude Code skill documentation](https://code.claude.com/docs/en/skills#choose-where-skills-load) defines that discovery behavior.

The integration preserves an existing skill or link that Culler does not own.
The installation record identifies every integration link that Culler owns.
A link conflict leaves the canonical installed skill available through its direct path.
An external agent with file and command access can use that path without harness-specific command adapters.

The release owns its skill files under the release directory.
The operator owns custom skills under `MC_HOME/skills/`.
An update preserves the operator's custom files.
Uninstall removes Culler-owned integration links and release resources while preserving unrelated skills.

### Container resources

Collie receives the common skill under `~/.agents/skills/culler/`.
Each container receives the complete reference tree before its harness starts.
The launch envelope and skill installer preserve relative paths and file contents.
Skill names still match their frontmatter names.

The delivery format permits declared ordinary files within a skill directory.
It rejects absolute paths and traversal outside that directory.
It rejects links that escape that directory.
Reference links stay inside the delivered skill tree.
Concurrent containers preserve their separate skills and instructions.
Existing operator skills remain compatible with their current single-file format.

The release manifests include every required skill and runtime resource.
The installed skill and executable identify the same release.
An active Collie container retains the complete skill files for its runtime release.
An update cannot replace only part of that container's reference tree.

## Acceptance

The implementation meets these observable requirements:

| Case | Required result |
|---|---|
| Root, family, and command help | Each response supplies the appropriate detail through the real executable. |
| Static help with unavailable services | Help succeeds without credentials or container startup and leaves system state unchanged. |
| Command definitions | Descriptions agree with accepted inputs, rejected inputs, and actual result fields. |
| Default Collie permissions | A new session can execute each supported operator operation when its domain requirements hold. |
| Restricted Collie permissions | Help identifies the restriction, and direct and host requests enforce it. |
| Existing Collie session | A release update preserves its frozen permission set. |
| Caller identity | Forged identity fields and ended sessions cannot submit new writes. |
| Component ownership | Collie and pipeline agents cannot use the new path for another component's internal commands. |
| Host and Collie equivalence | Equivalent operator requests produce equivalent state changes and domain refusals. |
| Decision records | Obsolete packet versions, stale revisions, and absent required decision fields produce refusals. |
| Request retries | A repeated request produces no duplicate work, including after a transport timeout. |
| Asynchronous results | The caller can inspect an accepted operation and distinguish it from completion. |
| Release checks | Incomplete updates and incompatible releases block operator writes through every entry point. |
| Project setup | Registration and Vision saves work through the host path while Collie's repository mount stays read-only. |
| Environment and recovery | The permitted commands preserve image approval, readiness, pause, and independent failure conditions. |
| Profile draft test | A draft test preserves the saved profile's verification state until a matching save. |
| Native trace inspection | Bounded reads and citations preserve native OpenCode records and distinguish copy state from import state. |
| Release-only external agent | An agent can discover commands and complete a representative read and write without a source checkout. |
| Skill references | Every installed reference resolves for both host and container paths. |
| Skill integration | A custom home, an unrelated existing skill, an update, and uninstall preserve ownership rules. |
| Agent input | The directive and turn context omit duplicate rules, and procedures retain the required operator confirmations. |

Command and authority checks exercise the executable and real domain validation with scratch state.
Host transport checks use controlled host effects and cover permission failures before any effect.
Credential-free checks remain suitable for pull requests and the fast suite.
Separate release checks cover the actual package and container boundaries.
Unavailable platform checks remain explicitly unverified under the repository's test rules.

## Source references

These files establish the baseline contracts that the requirements preserve or extend:

- [Collie directive](../mc/verbs/directives/collie.md) and [command skill](../mc/verbs/skills/collie/mc-collie/SKILL.md).
- [Collie permissions](../mc/verbs/collie.go) and [caller scope](../mc/verbs/verbs.go).
- [Collie turn context](../runner/collie-runner/main.ts).
- [Skill definitions](../mc/verbs/roleskills.go) and [skill installer](../runner/agent-runner/skills.ts).
- [Task writes](../mc/verbs/board.go), [Project setup](../mc/cmd/mc/project_setup.go), and [Vision authority](../mc/verbs/visiondraft.go).
- [Environment authority](../mc/verbs/environment.go) and [host environment commands](../mc/cmd/mc/environment.go).
- [System commands](../mc/cmd/mc/systemstatus.go) and [profile operations](../mc/cmd/mc/agentprofiles.go).
- [Profile verification checks](../mc/cmd/mc/systemstatus_setup_test.go).
- [Native trace contract](native-opencode-traces.md) and [trace reader](../runner/shared/trace-read.ts).
- [Dashboard command mapping](../dashboard/src/api.ts) and [dashboard release checks](../dashboard/src/server.ts).
- [Host release manifest](../mc/deployment/host_release.go) and [runner release manifest](../mc/deployment/runner_release.go).
