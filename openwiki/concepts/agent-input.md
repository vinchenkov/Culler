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
  - id: openwiki-source-b4da7cec419704ae15f02306
    resource: repo://mc/verbs/arbiter_input_test.go
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
  - id: openwiki-source-ff3195b0a5a37a2736e48a3b
    resource: repo://mc/verbs/platform_input_test.go
  - id: openwiki-source-81ff81fd9648ddfb1d004cea
    resource: repo://mc/verbs/roleskills.go
  - id: openwiki-source-58286c7d3d2cfcf24add95f4
    resource: repo://mc/verbs/setup_agent_input_test.go
  - id: openwiki-source-717b9c51f4b41e8d918f5c19
    resource: repo://mc/verbs/skills/arbiter/mc-arbiter/SKILL.md
  - id: openwiki-source-332034bad01f6c1d71fb1474
    resource: repo://mc/verbs/skills/collie/mc-collie/SKILL.md
  - id: openwiki-source-9992dda8c04b35911bec9f64
    resource: repo://mc/verbs/skills/packager/mc-packager/SKILL.md
  - id: openwiki-source-f7b77b556b18b6d3270e2193
    resource: repo://mc/verbs/skills/scribe/openwiki-documents/SKILL.md
  - id: openwiki-source-f50fb8f0947937402f7ebff6
    resource: repo://mc/verbs/skills/scribe/openwiki-recovery/SKILL.md
  - id: openwiki-source-0b1c56f800b720f3a116da01
    resource: repo://mc/verbs/skills/scribe/openwiki/SKILL.md
  - id: openwiki-source-04cae9da859027e703632c1b
    resource: repo://mc/verbs/skills/shared/project-environment-setup/SKILL.md
  - id: openwiki-source-a6635e9c51b05f45725be1ce
    resource: repo://mc/verbs/skills/shared/task-description/SKILL.md
  - id: openwiki-source-2244ea442a4c07aeb880c8c8
    resource: repo://mc/verbs/skills/strategist/mc-strategist/SKILL.md
  - id: openwiki-source-0097b0a41bffd53dce10abf3
    resource: repo://mc/verbs/skills/verifier/mc-verifier/SKILL.md
  - id: openwiki-source-fbe8f84251137d3cfb7fb6f4
    resource: repo://mc/verbs/skillstore.go
  - id: openwiki-source-4565b2c94892e8cea0c22610
    resource: repo://mc/verbs/strategist_input_test.go
  - id: openwiki-source-fe13f5a431cbfaf8f480dd65
    resource: repo://mc/verbs/verifier_input_test.go
  - id: openwiki-source-7092e09d830ef9ed23536948
    resource: repo://resident/src/environment-lane.ts
  - id: openwiki-source-8538c0fae787322c5331cccf
    resource: repo://resident/src/skill-store.ts
  - id: openwiki-source-20c7a26c8321ed0c7a2cf49b
    resource: repo://runner/agent-runner/skills.ts
  - id: openwiki-source-d1b22419782a460caf6dbe3c
    resource: repo://runner/collie-runner/main.ts
generated: { by: "culler-scribe", at: "2026-11-10T18:18:18.729Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-10T18:18:18.729Z
---

# Agent input: directives, run brief, and shipped skills

Each pipeline run starts in a fresh container, and the agent knows only its agent input. The agent input has three parts:

1. The **role directive**: frozen Markdown under `mc/verbs/directives/<role>.md` that holds the rules for every run of the role.
2. The **run brief**: a text document that `mc/verbs/brief.go` builds for this run from the spine. It holds the facts of this assignment.
3. The **skills**: `SKILL.md` files installed at `~/.agents/skills/` in the container. A skill holds the procedure for a case that occurs in some runs, plus the role's command surface.

`mc` owns all three. The directives and shipped skills are embedded in the `mc` binary with `go:embed`, so an installation cannot omit them or change them locally. The resident adds the operator's per-Project skills, and the runner installs the set. The [Collie](collie.md) tier uses the same directive and skill mechanism but has no run brief.

The agent profile sets the model and effort for each run.
The Verifier, Packager, and setup directives state this fact.
The Verifier directive states the profile rule once and leaves harness details to the runtime.
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

- **Your command surface** names the `mc-<role>` skill and its path, `~/.agents/skills/mc-<role>/SKILL.md`. The Strategist reads this skill before each `mc` call. Other pipeline roles read it before the first call and before the terminal. The pointer also states that `mc` has no help command.
- **Your other skills** lists each additional and shared skill with its path and frontmatter description.

The pointer carries only names and descriptions, never skill bodies. It is the trigger that makes the agent load a skill. It is also the fallback when a harness fails to list skills, because each skill is a readable file at a fixed path.

The Worker and Verifier pointers also supply a failure terminal for an absent or unreadable command skill.
The agent reports the failed read through its assigned task and run identity.
This fallback preserves an explicit terminal when the normal command instructions are unavailable.
`mc/verbs/directives.go` defines the pointer and fallback.

## Strategist instructions and procedures

`mc/verbs/directives/strategist.md` defines proposal authority, criteria, customization, and research for each Strategist run.
Repository evidence and the accepted Vision guide proposals.
The directive requires duplicate checks against repository work, active tasks, rejected work, and cancelled work.
Only the Arbiter selects proposals for development.

The directive starts task selection with a useful result that the Project cannot yet deliver well.
The Strategist traces the user's route through the repository and identifies the step that prevents success or causes substantial effort.
It considers new capabilities and defects, checks existing capabilities, and investigates assumptions that can invalidate the proposed benefit.
Each Strategic rationale explains the present problem, expected result, current value, and substantial effort or risk.
The Strategist writes each rationale to its own temporary file and reviews it before submission.
The submitted `strategic_rationale` field contains that file's text unchanged.
The description holds every task requirement; the rationale supplies decision evidence.
One task can span several components and must have scoped checks that agents can execute in their container.
The directive requires two proposals together and leaves the final rank to the Arbiter.
The command skill permits an empty batch when no valid task remains.

The command skill, `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, contains command forms and conditional procedures:

| Condition | Procedure in `mc-strategist` |
| --- | --- |
| A proposal batch is ready. | Proposal batch defines the payload, an empty result, and rejected-batch correction. |
| An active or historical title leaves scope unclear. | Reads identifies the permitted task read. |
| Research needs Project dependencies. | Research dependencies requires compatibility checks before use and applies the shared environment skill within role authority. |
| An execution problem prevents completion, or an operator decision is required. | Execution problems and operator decisions selects the terminal and its evidence. |
| Custom instructions conflict with the contract. | Project role customization defines the conflict report. |

The directive points to the execution, operator decision, and customization procedures.
The command skill supplies the research dependency procedure.
The generated command pointer names the installed file at `~/.agents/skills/mc-strategist/SKILL.md`.
`RoleSkillSet` in `mc/verbs/roleskills.go` delivers that file and the shared skills.
These pointers keep procedures available when their conditions apply.
See [Strategist research and submission](../workflows/task-pipeline.md#strategist-research-and-submission) for the complete proposal workflow.

## Arbiter instructions and procedures

`mc/verbs/directives/arbiter.md` defines the review rules for each Arbiter run.
The Arbiter ranks proposals by their contribution to the accepted Vision relative to their cost.
It judges a small task by its result and a large task by its feasible benefit and full cost.
It considers new capabilities as well as defects.
It checks overlap through descriptions and repository evidence, and preserves proposal requirements and accepted work.
The contract limits verdicts to the assigned pool and includes the promotion limit and criteria rules.
These boundaries keep customization from changing the assignment or weakening required criteria.

The directive points to conditional procedures in `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`:

| Condition | Procedure |
| --- | --- |
| Rejected or cancelled work is present. | Historical work requires scope comparison and use of cancellation reasons as operator context. |
| The Arbiter intends to reject every proposal. | The all-reject batch requires a duplicate reference or invalid-criteria category for each rejection. |
| An execution problem or required operator decision prevents a verdict. | A run that cannot give a result selects the applicable terminal. |
| Custom instructions conflict with the contract. | Project role customization supplies the conflict terminal and its fields. |

The command skill also defines the verdict payload and permitted reads.
Those reads cover tasks, activity, and Projects.
The task list omits descriptions; an individual task read supplies absent scope.

The customization paragraph points to the Project role customization procedure in `mc-arbiter`.
The shared `project-environment-setup` skill supplies dependency requirements within the role command authority.
`RoleSkillSet` delivers `mc-arbiter` with the shared skills.
The generated pointer in `mc/verbs/directives.go` requires a read before the first command and before the terminal.
Its installed path is `~/.agents/skills/mc-arbiter/SKILL.md`.
See [Arbiter review and submission](../workflows/task-pipeline.md#arbiter-review-and-submission) for the workflow and failure boundaries.

## Shared task description standard

`mc/verbs/skills/shared/task-description/SKILL.md` holds the standard for task descriptions and acceptance criteria.
The Strategist applies it to proposals. The Arbiter judges proposal descriptions against it.
Collie applies it to new tasks and requirements revisions.
The Strategist and Arbiter contracts include the skill's rules, so Project customization cannot replace those rules.

The description states the scope and every requirement for one repository commit.
Each acceptance criterion describes task behavior and names a check that an agent container can run.
The writer checks the tests' actual prerequisites.
The standard excludes host access, a Docker daemon, macOS interfaces, additional privileges, and hosted continuous integration runs from required checks.
Whole-suite success is not an acceptance criterion. The Verifier owns broad regression checks.
New or changed tests must pass; an unchanged Project failure alone keeps a task valid.

The Arbiter rejects a proposal that violates the standard.
For an acceptance criteria violation, it uses `invalid_criteria` and identifies the criterion and the violated rule.
The [task pipeline](../workflows/task-pipeline.md) explains the all-reject guard.

`specs/task-description-standard.md` records the shared standard's scope, authority, and required role behavior.
Its Current gap section describes an earlier system; the current implementation uses the shared skill.
Its original document remains at that authoritative path.

## The run brief

The dispatch transaction freezes the brief with its run assignment. Task pipeline runs claim the Project lease; Scribe runs use a separate wiki assignment. The spawn effect carries the brief text as `brief`. The resident copies it into `run.json` unchanged and never reads the spine, so the brief cannot drift between the dispatch decision and the launch.

`renderRunBrief` writes a fixed shape:

~~~text
Culler immutable run brief (mc.spawn-brief.v24)

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

`trimStrategistBrief` in `mc/verbs/brief.go` selects concise explanations for Strategist assignment data.
Its brief omits the Agent credentials and Failure controls parts.
It retains the accepted Vision, Project path, environment identifiers, task history, custom instructions, and proposal decisions.
The Cancelled work explanation requires use of each reason as context for the operator's preference.
The Proposal decisions explanation requires application of each relevant operator resolution.
The directive and command skill supply the procedures that use these facts.

`trimArbiterBrief` omits the Agent credentials and Failure controls parts from the Arbiter brief.
It retains the pool, active descriptions, task history, environment identifiers, repository path, custom instructions, proposal resolutions, and accepted Vision.
Its concise explanations identify the supplied data; the directive and skill define review procedures.
The Proposed pool rows include each proposal's `strategic_rationale` for evidence review.
The Arbiter checks its cited facts and gives assumptions less weight than observations.
The Proposed pool explanation defines `priority` as the task priority that the operator sets.
The value -1 means expedited work; 0 through 3 mean P0 through P3.
The Proposal decisions explanation requires application of each relevant operator resolution.

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

`trimWorkerBrief` and `trimVerifierBrief` keep procedures in the directive and skills.
Their briefs omit Agent credentials, Failure controls, Correction allowances, and the unsuccessful correction count.
They retain the assignment, identifiers, Git facts, and applicable case payloads.
The Verifier Git explanation distinguishes the original task base from the submitted source.
Its Last report explanation identifies that report as the submitter's claims.
Its environment identifiers support dependency compatibility checks.

## Rationale delivery and packet input

Only a Strategist proposal holds a Strategic rationale.
The Arbiter receives it in the Proposed pool; the Packager receives a separate Strategic rationale part for an autonomous task.
Worker and Verifier briefs omit it. Their task reads also omit the field.
This keeps task requirements in the description while the rationale remains proposal evidence.
`mc/verbs/strategic_rationale_test.go` checks delivery and omission across the roles.

The Packager starts the first section with its statement of the delivered change.
For a Strategist task, the section is Strategic rationale, followed by the complete rationale with the Strategist as author.
The Packager converts its Markdown to HTML and preserves its claims and evidence.
It identifies the rationale as observations and assumptions from before the work.
An operator task instead starts with Summary.
Project instructions preserve the first Strategic rationale section while they can replace the remaining default structure.

## Regression duties in the role input

The Worker directive and command skill require successful new or changed tests and correction of task regressions.
The Worker directive points to Unavailable actions and checks in `mc-worker` when a requirement cannot run.
The Verifier directive makes Assessment and unavailable-check outcomes part of the Culler contract.
Project customization can replace methods and optional checks while preserving these required duties.

The Verifier obtains independent results for every criterion and each new or changed test on the submitted commit.
It runs normal broad Project checks, including required build and analysis checks.
One execution can supply criterion evidence and broad evidence.
A successful submitted check needs no base execution for regression detection.
A criterion about an earlier state still requires evidence of that state.
A file read alone cannot verify behavior whose consumer runs outside the container.

The `mc-verifier` skill owns the procedures for these cases:

- Broad check failures and comparable base checks.
- Unavailable checks and their outcomes.
- Long checks with a recorded terminal status.
- Destination merges that contain accepted work.
- Operator decisions and execution failures.

The Verifier keeps the submitted repository clean and leaves HEAD at the submitted commit.
It places logs and scratch files outside that repository.
The `verify-behavior` skill applies when a criterion or broad check needs browser interaction or visual confirmation.
It defines browser sessions, evidence media, and cleanup.

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
Project instructions can replace default sections while preserving the first Strategic rationale section for a Strategist task.
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

The shared environment skill explains the temporary Culler token and host proxy.
The host retains and renews provider credentials.
The Worker and Verifier directives also require preservation of the supplied connection configuration and exclusion of Culler tokens from output.

`credentialPart()` in `mc/verbs/brief.go` supplies the Agent credentials explanation for briefs that retain that part.
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
- `shared/` supplies writing, Project wiki, environment, and task description skills to pipeline agents, setup agents, and Collie.
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

The shared environment skill explains the persistent home and separate credentials, sessions, and role instructions.
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

`mc/verbs/packetcontext.go` supplies the identity, opening text, criteria, report text, commit identifiers, and readable artifact paths.
The opening heading is Strategic rationale for an autonomous task and Thesis for an operator task.
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
- `mc/verbs/roleskills_test.go`: every role ships `mc-<role>` first, and every tier ships `writing`. Role-specific skills remain separate; shared skills reach every role. The Collie skill lists exactly its allowlist. `TestProseNamesNoCommandTheSkillOmits` fails if a directive names an `mc` command or flag that the command surface omits. `TestEveryDirectiveForbidsASilentExit` checks that each directive forbids ending without a terminal.
- `mc/verbs/skillstore_test.go`, `resident/src/skill-store.test.ts`, and `runner/agent-runner/skills.test.ts` cover store layout, operator skill validation, shipped-name protection, wipe-before-install, and traversal-shaped names.

`mc/verbs/verifier_input_test.go` checks the Verifier contract exposed to Collie and the rendered Verifier input.
It checks required assessment duties, concise facts, skill discovery, and retained case payloads.
`mc/verbs/platform_input_test.go` checks unavailable-check procedures, the failed-read fallback, and evidence duties across the required roles.
`mc/verbs/strategist_input_test.go` checks preserved assignment data, delivered procedure pointers, and single occurrences of selected Strategist rules.
A separate helper checks each task description rule once across the rendered brief and every delivered skill.
It also checks proposal requirements in the extracted customization contract.
`TestStrategistBriefDeliversTaskSelectionGuidance` checks the delivered guidance for useful results, evidence, existing capabilities, effort, risk, and the rationale review before submission.
`mc/verbs/arbiter_input_test.go` checks preserved assignments, operator instructions, proposal resolutions, skill delivery, and conditional procedure pointers.
Selected Arbiter rules occur once across the rendered brief and command skill.
The task description audit also includes every delivered skill.
Its payload test checks that concise explanations preserve each retained part's facts, text, rows, and text tag.
`TestArbiterBriefDeliversProposalReviewGuidance` checks value relative to cost, small and large task outcomes, and consideration of new capabilities.
The orchestration tests in `directives_test.go` apply their fixed workflow requirements to the Worker, Verifier, and Packager.
The Strategist directive permits subagents and internet research without that fixed workflow procedure.
These tests check supplied instructions. They do not prove agent behavior or establish fresh test results in this wiki update.

The [Worker instruction audit](../operations/worker-instruction-audit.md) is an original report for task 56 at its recorded base commit.
It records passage decisions, text counts, retained duties, and the separation between role rules and procedures.

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

The Verifier command skill selects unavailable-check outcomes for criterion checks and new or changed tests.
Source, tests, Workspace dependencies, and setup script problems require a correction when the Worker can change their cause.
An Image dependency also requires a correction so the Worker can propose the change.
An absent capability that no agent container has requires an operator decision report.
Examples include host access, a Docker daemon, macOS interfaces, and additional privileges.
An unclear cause requires a correction.
The exception for unavailable host tests outside the criteria excludes new or changed tests, which must pass.
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
