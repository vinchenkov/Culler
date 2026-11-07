---
type: concepts
title: "Agent input: directives, run brief, and shipped skills"
description: What a Culler agent receives at the start of a run, where each part is authored, how it reaches the container, and the rules and tests that keep it consistent.
tags: [agent-input, directives, run-brief, skills, prompts]
sources:
  - id: openwiki-source-8037e2358a2c4f9b2c722a11
    resource: repo://AGENTS.md
  - id: openwiki-source-b578a24182c530b5d8630c21
    resource: repo://mc/dispatch/dispatch.go
  - id: openwiki-source-b93e6f09bf1a3c497f977289
    resource: repo://mc/verbs/brief_history_test.go
  - id: openwiki-source-36bed318ff0007713cf91e1b
    resource: repo://mc/verbs/brief_resolution_test.go
  - id: openwiki-source-b631f4735113cb033d303c97
    resource: repo://mc/verbs/brief_test.go
  - id: openwiki-source-7f547d2c5de5589b3c27e6a4
    resource: repo://mc/verbs/brief.go
  - id: openwiki-source-6227f25ba09e5ec68ceb60f9
    resource: repo://mc/verbs/directives.go
  - id: openwiki-source-3bc5b077708f9534abc3b95d
    resource: repo://mc/verbs/directives/arbiter.md
  - id: openwiki-source-6c249547ec5cf1c015cbdcb1
    resource: repo://mc/verbs/directives/collie.md
  - id: openwiki-source-959768f661d526f65eea9d8d
    resource: repo://mc/verbs/directives/packager.md
  - id: openwiki-source-4b2c2a82df1cffc64cdb7c34
    resource: repo://mc/verbs/directives/setup.md
  - id: openwiki-source-72313a4f79741897a46d7a11
    resource: repo://mc/verbs/directives/strategist.md
  - id: openwiki-source-db55b76d7f0573ce8b068ebf
    resource: repo://mc/verbs/directives/verifier.md
  - id: openwiki-source-116843f6a2d38ecfc11c910e
    resource: repo://mc/verbs/directives/worker.md
  - id: openwiki-source-f8cf21fb5ecc251716901875
    resource: repo://mc/verbs/dispatchprivate.go
  - id: openwiki-source-04786a87679eea8954d2f952
    resource: repo://mc/verbs/environment_execution.go
  - id: openwiki-source-81ff81fd9648ddfb1d004cea
    resource: repo://mc/verbs/roleskills.go
  - id: openwiki-source-58286c7d3d2cfcf24add95f4
    resource: repo://mc/verbs/setup_agent_input_test.go
  - id: openwiki-source-332034bad01f6c1d71fb1474
    resource: repo://mc/verbs/skills/collie/mc-collie/SKILL.md
  - id: openwiki-source-f7b77b556b18b6d3270e2193
    resource: repo://mc/verbs/skills/scribe/openwiki-documents/SKILL.md
  - id: openwiki-source-f50fb8f0947937402f7ebff6
    resource: repo://mc/verbs/skills/scribe/openwiki-recovery/SKILL.md
  - id: openwiki-source-0b1c56f800b720f3a116da01
    resource: repo://mc/verbs/skills/scribe/openwiki/SKILL.md
  - id: openwiki-source-04cae9da859027e703632c1b
    resource: repo://mc/verbs/skills/shared/project-environment-setup/SKILL.md
  - id: openwiki-source-fbe8f84251137d3cfb7fb6f4
    resource: repo://mc/verbs/skillstore.go
  - id: openwiki-source-7092e09d830ef9ed23536948
    resource: repo://resident/src/environment-lane.ts
  - id: openwiki-source-8538c0fae787322c5331cccf
    resource: repo://resident/src/skill-store.ts
  - id: openwiki-source-20c7a26c8321ed0c7a2cf49b
    resource: repo://runner/agent-runner/skills.ts
  - id: openwiki-source-d1b22419782a460caf6dbe3c
    resource: repo://runner/collie-runner/main.ts
generated: { by: "culler-scribe", at: "2026-11-07T01:30:07.289Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-07T01:30:07.289Z
---

# Agent input: directives, run brief, and shipped skills

Each pipeline run starts in a fresh container, and the agent knows only its agent input. The agent input has three parts:

1. The **role directive**: frozen Markdown under `mc/verbs/directives/<role>.md` that holds the rules for every run of the role.
2. The **run brief**: a text document that `mc/verbs/brief.go` builds for this run from the spine. It holds the facts of this assignment.
3. The **skills**: `SKILL.md` files installed at `~/.agents/skills/` in the container. A skill holds the procedure for a case that occurs in some runs, plus the role's command surface.

`mc` owns all three. The directives and shipped skills are embedded in the `mc` binary with `go:embed`, so an installation cannot omit them or change them locally. The resident adds the operator's per-Project skills, and the runner installs the set. The [Collie](collie.md) tier uses the same directive and skill mechanism but has no run brief.

The agent profile sets the model and effort for each run.
The Strategist, Arbiter, Verifier, Packager, and setup directives state this fact.
They name Codex, the Claude SDK, and OpenCode as supported harnesses.
The private runtime capability and launch envelope carry the selected effort to the runner.
An empty effort leaves the harness default in effect.

## Role directives

`mc/verbs/directives.go` embeds one file per role:

- `strategist.md`
- `arbiter.md`
- `worker.md`
- `verifier.md`
- `scribe.md`
- `packager.md`
- `collie.md`
- `setup.md`

`roleProseDirective` maps each `dispatch.Role` to its file and returns an error for an unknown role.

Each directive states the Culler contract, the customization rules, and the role responsibility. The Scribe updates the wiki separately from tasks. See [the task pipeline roles](../workflows/task-pipeline.md).

`directiveForRole` appends one generated section, built by `skillPointer`, to the prose:

- **Your command surface** names the `mc-<role>` skill and its path, `~/.agents/skills/mc-<role>/SKILL.md`. It tells the agent to load the skill before the first `mc` call and again before the terminal. It also states that `mc` has no help surface: bare `mc` names zero verbs, and `mc --help` is an unknown verb.
- **Your other skills** lists each additional and shared skill with its path and frontmatter description.

The pointer carries only names and descriptions, never skill bodies. It is the trigger that makes the agent load a skill. It is also the fallback when a harness fails to list skills, because each skill is a readable file at a fixed path.

## Proposal criteria in the role directives

The Strategist directive requires each success criterion to name a check that the Worker and Verifier can run in an agent container.
The Arbiter directive requires proposals to meet that same checkability condition.
Criteria describe behavior within the task scope. Whole-suite success requirements are invalid criteria.
The roles must check test capabilities; an available suite command does not prove that its tests can run.
The Arbiter rejects invalid criteria with `rejection_category: invalid_criteria` and a reason.
The [task pipeline](../workflows/task-pipeline.md) explains the batch guard and retained rejection records.
Both directives exclude these capabilities from the available checks:

- Host access.
- A Docker daemon.
- macOS system interfaces.
- Additional container privileges.
- A run on a hosted continuous integration (CI) service, such as GitHub Actions.

These requirements guide proposal authorship and selection before the Worker receives a task.
They are role instructions in `mc/verbs/directives/strategist.md` and `mc/verbs/directives/arbiter.md`.
They do not establish that a particular check passed.
The [task pipeline](../workflows/task-pipeline.md) explains the roles that apply these requirements.

## The run brief

The dispatch transaction freezes the brief with its run assignment. Task pipeline runs claim the Project lease; Scribe runs use a separate wiki assignment. The spawn effect carries the brief text as `brief`. The resident copies it into `run.json` unchanged and never reads the spine, so the brief cannot drift between the dispatch decision and the launch.

`renderRunBrief` writes a fixed shape:

~~~text
Culler immutable run brief (mc.spawn-brief.v23)

# <Role title> run <run_id>

<role_directive>
...directive plus skill pointer...
</role_directive>

## <Part heading>

<one- or two-sentence explanation>
- Label: value          (facts)
<tag> ... </tag>        (outside prose, e.g. a task description)
```json ... ```         (structured rows)
~~~

The version string is the constant `spawnBriefSchema` in `brief.go`. Every part is a `briefPart` with a heading, an explanation, and then facts, tagged prose, or one JSON block. Explanations come before data so a first-time reader knows what the data is for. JSON rows are encoded with HTML escaping off, because the reader is a person or an agent and not a browser.

`brief.go` selects facts for each role.
Task runs receive their assigned criteria, Git facts, relevant verdicts, and correction context.
Proposal runs receive the Project pool and recorded operator answers.
Only the Strategist and the Arbiter receive the Vision part.
Each part uses facts, tagged prose, or a JSON block.

The Project role customization part names the exact revision and standard directive digest.
Custom instructions replace defaults within the Culler contract.
A contract conflict ends the run with a customization conflict.
A Scribe conflict ends its wiki update as failed.
Saving a new customization preserves an existing block.

The Scribe brief identifies the Project and the accepted start commit.
It includes the prior successful start commit and merged task descriptions when those records exist.
Source evidence establishes actual behavior; task descriptions establish intent.
Without a prior successful update, the Scribe covers the whole repository.

The Project environment part identifies the configuration revision, selected image, and base.
The runner adds actual dependency preparation evidence before the agent receives the brief.
Project import permits unavailable capabilities. Required task checks remain required.

A subject never carries board state. Dispatch selects only an open, unblocked subject, so that state holds no information.

## Regression duties in the role input

The Worker directive and command skill require successful new or changed tests and correction of task regressions.
The Worker brief repeats those duties beside the assigned criteria.
The Verifier directive and skill require independent criterion checks and normal broad Project checks on the submitted commit.
The Verifier compares submitted failures with the base commit under comparable conditions.
The brief states these duties beside the task subject.

The Packager brief, directive, and skill require preservation of unchanged failures, base comparisons, and unverified coverage.
Each `UNVERIFIED` entry remains in the Review Packet.
These observations alone create no additional approval requirement.
The [test guide](../tests/checks-and-test-lanes.md) explains the distinction between command results and the task verdict.

## Packager page style guidance

The Packager input assigns the page style to the dashboard across three sources:

- `subjectExplanation` in `mc/verbs/brief.go` requires coverage of each criterion and identifies the dashboard as the page style owner.
- `mc/verbs/directives/packager.md` requires preservation of the off-white background and base typography, with transparent page containers.
- The `mc-packager` skill repeats these constraints and requires preservation of the page style when the Packager copies a previous packet.

The directive permits optional inline styles for diagrams and other visual content.
Its packet section structure is a default that Project instructions can replace.
The packet must retain decision evidence, material behavior, risks, and verification results.
See [packet content](../workflows/task-pipeline.md#packet-content-and-project-instructions) and [report page style](../architecture/dashboard.md#report-page-style).

## Historical tasks in proposal briefs

The Strategist and Arbiter receive the same compact history from the Project's spine snapshot.
`addHistoricalParts` in `mc/verbs/brief.go` selects up to 10 cancelled tasks first.
Rejected tasks fill the remaining places, with at most 20 historical tasks total.
Each part orders decisions from newest to oldest, then larger task identifiers first for equal times.
An empty part is absent. Archived tasks remain eligible for this history.

Each entry contains the task identifier, recorded title, and decision.
Historical entries exclude descriptions.
A cancelled entry includes a reason excerpt when available.
The excerpt uses the latest `task.cancelled` activity for that task and normalizes whitespace.
It contains at most 160 Unicode characters, including an ellipsis when truncated.
The original activity reason remains intact.
The agent can read the historical task through its permitted task read commands when its scope is unclear.

The historical limit preserves the other brief parts.
The Strategist receives active task identifiers, titles, and stages.
The Arbiter receives full descriptions for active tasks and the proposed pool.
Both roles retain the complete Vision.
The dispatch protocol retains `DedupeTitles`; the brief selects history directly from the spine snapshot.

`mc/verbs/brief_history_test.go` checks limits, equal role history, Project isolation, decision order, and empty part omission.
It also checks cancellation visibility, Unicode excerpts, retained source reasons, and complete active context.
These statements describe source-defined checks, not a test execution during this wiki update.
See [the task pipeline](../workflows/task-pipeline.md) for the role rules that use this history.

## Credential instructions

The role directives state that Culler supplies a temporary Culler token and proxy trust.
The host retains and renews provider credentials.
Agents preserve the supplied access configuration and exclude token values from output.

`credentialPart()` in `mc/verbs/brief.go` supplies the Agent credentials explanation.
The setup brief and shared environment skill describe the same boundary.
The Culler token permits only requests through the host proxy.

See [Agent connections and access renewal](../operations/agent-connections.md) for the implementation.

## Setup operation input

Setup operations receive a separate brief from `mc/verbs/environment_execution.go`.
The brief combines the setup directive, operation identity, selected image, accepted Vision, requested change, and retained configuration.
It also supplies the setup skills and any initial continuation requirements.

Setup and pipeline agents share container permissions, resource limits, network policy, and workspace paths.
Their role authority and credentials remain separate.
The setup directive requires an ordered checklist within the assignment's authority and attempt limit.
The agent identifies required commands, inspects capabilities, prepares Workspace dependencies, executes applicable checks, and assesses every missing prerequisite.
The shared `project-environment-setup` skill defines the evidence for an image proposal and the reasons for deferral.
Other roles apply that procedure within their own command surface and task authority.

The report separates actual command results from expected image capabilities.
An image proposal requires a material development benefit and credible feasibility evidence.
An initial continuation submits `complete` after either approval or denial and reports the assigned image's actual capabilities.
See [Project environments](../workflows/project-environments.md#setup-checklist-and-evidence) for the assessment and source acceptance boundaries.

`mc/verbs/setup_agent_input_test.go` checks that the generated brief includes prerequisite assessment, permitted probes, uncertainty, and continuation requirements.
It also checks that setup receives the command skill and the shared environment skill.
These assertions check supplied instructions. They do not prove that an agent follows the checklist or that a proposed image works.

## Shipped skills

`mc/verbs/roleskills.go` embeds `skills/*/*/SKILL.md`. The layout is `mc/verbs/skills/<role-basename>/<skill-name>/SKILL.md`. One map, `roleDirectiveBasename`, gives each role both its directive file and its skill directory, so the two cannot drift apart. The current tree:

- Each role directory holds `mc-<role>`, the command surface. It is a bullet list of exact `mc` commands with flags.
- The Scribe receives `openwiki`, `openwiki-documents`, and `openwiki-recovery`.
- The Verifier receives `verify-behavior` for browser evidence.
- `shared/writing`, `shared/project-wiki`, and `shared/project-environment-setup` close every pipeline set and the Collie set.
- `collie/mc-collie` is the Collie tier's command surface.

`RoleSkillSet` returns the command surface first, then the role's additional skills in name order, then the shared skills in name order. `skillByPath` enforces two rules at load. The directory name must match `^[a-z0-9][a-z0-9-]*$`, because the runner joins it under a root it wipes. The frontmatter `name` must equal the directory name. `SkillFrontmatterField` reads flat `key: value` lines and is deliberately not a YAML parser.

The spawn effect in `mc/verbs/dispatchverb.go` carries the set as `skills` (`SkillsEffectValue`: `{name, body}` per skill) and the operator store directory as `skill_store_dir`. The Collie wake effect in `mc/verbs/collieselect.go` carries `CollieSkillSet()` and `skills/collie` the same way.

## Scribe coverage and recovery

The Scribe directive defines the wiki scope, preservation requirements, permitted state changes, and one terminal action.
The role skills supply the detailed procedure:

- `mc-scribe` defines commit checks, completion evidence, failure reports, and customization conflicts.
- `openwiki` defines repository coverage, page jobs, source Claims, inspection, and finalization.
- `openwiki-documents` protects original documents and permits only specified metadata repairs before page work.
- `openwiki-recovery` requires review after external edits, incomplete records, interrupted work, or unavailable source history.

Each page uses its own source comparison window.
Unavailable Git history requires a saved source inventory or a full review of the affected pages.
The Scribe inspects Claims after the final page edit and submits explicit decisions for stale or unresolved Claims.
See [OpenWiki and viewers](../integrations/openwiki-and-viewers.md) for the verification record and completion receipt.

## Operator skills

The Strategist uses `projects/<project-id>/strategist-propose/<skill-name>/SKILL.md` for operator skills.
That path preserves access to existing operator skills.
`roleSkillStoreBasename` selects this directory independently from the shipped directive basename.
The run brief title and directive identify the role as Strategist.
Its shipped command skill uses `strategist/mc-strategist/SKILL.md` in the repository.

Operators add skills under `MC_HOME/skills/`. The paths are `projects/<project-id>/<role-dir>/<skill-name>/SKILL.md` for pipeline roles and `collie/<skill-name>/SKILL.md` for Collie (`mc/verbs/skillstore.go`). `resident/src/skill-store.ts` reads the directory that the effect names. `readOperatorSkills` visits entries in name order. It skips and reports any entry with a bad name, an unreadable file, missing frontmatter, a `name` that differs from the directory, an empty description, or an empty body. An absent directory is not a problem. `composeRunSkills` appends operator skills after the shipped set and refuses an operator skill that reuses a shipped name, so an operator cannot shadow Culler's guidance. The resident also creates the per-role store directories for a Project the first time it spawns there (`ensureProjectSkillStore`).

Skill delivery never stops a run. Each problem becomes a line in `run.json`'s `skill_problems`, and the runner logs it.

## Runner install

`runner/agent-runner/skills.ts` resolves the skill root from an absolute `HOME`.
`installEnvelopeSkills` logs delivery problems, validates entries, and calls `installRunSkills`.
That function replaces the root and writes each `<name>/SKILL.md` with mode 0600.
An empty set clears the root. Duplicate names install once.
The envelope installer logs failures and permits execution with degraded guidance.
The Collie runner uses the same installer.

Project containers mount separate temporary `.agents`, `.claude`, and `.codex` directories inside the persistent home.
These mounts isolate concurrent role files before skill installation starts.
Replacement affects only the current container's skills.
The Claude adapter copies those skills into its private harness configuration directory.
Dependencies elsewhere in the Project home survive both installation and container removal.
`resident/src/environment-lane.ts` defines these mounts.

## Dependency context

The setup and pipeline directives explain the persistent home and separate credentials, sessions, and role instructions.
The Worker, Verifier, Packager, and Scribe receive fresh source workspaces at their assigned commits.
The Strategist and Arbiter read fresh committed projections.
The shared environment skill requires compatibility checks against current declarations and the selected image.

The retained setup script prepares each fresh Worker, Verifier, and Packager workspace.
The Scribe, Strategist, and Arbiter omit automatic dependency preparation.
The OpenWiki skill states the Scribe exemption and requires dependency compatibility checks before use.
Setup input requires a script that permits repeated execution against that home.
Actual preparation, build, and test results remain separate evidence.


## Collie packet context

A packet conversation uses the Collie directive and skills with packet context in its first native session turn.
`runner/collie-runner/main.ts` requests the context through the runner-only packet-context command.
It checks the returned session, packet, and version identifiers against the launch envelope.
A mismatch prevents the harness turn.
A later turn with the native session reference retains context through that session.
A new native session receives the context again.

`mc/verbs/packetcontext.go` supplies the identity, thesis, criteria, report text, commit identifiers, and readable artifact paths.
The context excludes current review decisions, merge state, and blocks.
The Collie directive requires a current packet read before statements about those states or a packet decision.
Report text and artifact files supply evidence. Only an operator message authorizes a state change.
The directive requires a description and explicit confirmation before a new state change.

The `mc-collie` skill explains packet reads, candidate diffs, evidence files, report media, task traces, and refusal of an outdated decision.
The shared `project-wiki` skill defines document ownership and separates original deliverables from task execution artifacts.
See [packet conversations](../workflows/packet-conversations.md) for the complete sequence and access checks.

## Rules for changing agent input

`AGENTS.md` makes agent input a hard rule:

- A change that alters what a role does, what it receives, or what it can call must update the affected directive, run brief explanations, and skills.
- The update states the new model succinctly, removes each statement the change made false, and holds no history.
- Each brief part has a heading and an explanation of at most two sentences.
- An additional skill may name only `mc` commands that its command surface already lists.

Changing a part's shape or meaning is a wire change to agent behavior. The `mc.spawn-brief.vN` version string identifies the brief format. Treat changes to `brief.go` as design changes, not refactors.

## Tests

Tests enforce most of these rules mechanically:

- `mc/verbs/directives_test.go`: every spawn role has a directive, and unknown roles have none (`TestFrozenRoleDirectivesCoverEverySpawnRole`, `TestUnknownSpawnRoleHasNoFallbackDirective`). `TestAgentTextNamesOnlyThePartsThatTheRunBriefRenders` fails if a directive or skill names a `**Part** part` heading that the brief does not render. Other tests pin role-specific contract text, such as one Project per role and no approval wait.
- `mc/verbs/brief_test.go`: each role's brief holds its directive. Only the selecting and planning roles get the vision. `TestRunBriefExplainsEachPart` requires unique headings and a one- or two-sentence explanation ending in a period. Subjects carry no board state.
- `mc/verbs/brief_render_test.go`: `parseRenderedBrief` reads rendered text back into header, directive, and parts. The brief tests assert on that parse.
- `mc/verbs/roleskills_test.go`: every role ships `mc-<role>` first, and every tier ships `writing`. No two roles share a skill. The Collie skill lists exactly its allowlist. `TestProseNamesNoCommandTheSkillOmits` fails if a directive names an `mc` command or flag that the command surface omits. `TestEveryDirectiveForbidsASilentExit` checks that each directive forbids ending without a terminal.
- `mc/verbs/skillstore_test.go`, `resident/src/skill-store.test.ts`, and `runner/agent-runner/skills.test.ts` cover store layout, operator skill validation, shipped-name protection, wipe-before-install, and traversal-shaped names.

## Requirements after decision resolution

The Task description holds the acceptance criteria.
A requirements revision gives each Worker run on that version a separate Requirements revision part.
Its reason explains why the operator changed the description; it supplies context only.
A packet return instead supplies a Revision request that the Worker must meet.

The Worker and Verifier receive the original task base commit separately from the run start commit.
A Worker can submit its start commit unchanged when it differs from the task base and satisfies the description.
The Verifier judges the full change from the base to the submitted commit.
The Verifier and Packager receive the revised description without the requirements revision reason.
Briefs exclude Verifier corrections and verdicts from earlier requirements versions.
An open merge repair retains its Merge candidate context across a requirements revision.

The Verifier directive distinguishes a failed criterion from a required check that needs unavailable container capabilities.
Source, tests, Workspace dependencies, and setup script problems require a correction when the Worker can change their cause.
A criterion check that needs host access, a Docker daemon, macOS interfaces, or additional privileges requires an operator decision report.
Uncertainty between those cases requires a correction.
These instructions preserve the operator's authority to change criteria.

`mc/verbs/brief_resolution_test.go` checks reason isolation, later Worker context, original base commits, and packet request obligations.
`mc/verbs/directives_test.go` checks the corresponding role instructions.
See [decision resolution](../workflows/decision-resolution.md) for the state transition and retained commits.

## Collie Discord authority

The Collie directive assigns Discord setup, account approval, and connection changes to the dashboard.
The command skill directs the operator to **Configs → Discord** and states that setup codes grant no agent authority.
The Collie runner adds the same boundary to its turn prompt.
The directive and command skill tell Collie that one Discord thread and its dashboard view share a session.
They assign delivery to Culler, so Collie returns its reply to the runner instead of sending it to a surface.
Culler consumes account confirmation codes before it forwards ordinary messages to Collie.
See [Discord setup and account access](../integrations/discord.md) for the host implementation.
