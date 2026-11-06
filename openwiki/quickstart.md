---
type: guide
title: Culler quickstart
description: The page introduces Culler, its components and vocabulary, and directs readers to the pages for common tasks and key concepts.
tags: [quickstart, overview]
sources:
  - id: openwiki-source-4d1d392666be6dfdd7a91a2e
    resource: repo://.github/workflows/release.yml
  - id: openwiki-source-8037e2358a2c4f9b2c722a11
    resource: repo://AGENTS.md
  - id: openwiki-source-5581b682dfee807415160cbe
    resource: repo://dashboard/PAGES.md
  - id: openwiki-source-ddff79bc537156c26cb2035f
    resource: repo://dashboard/smoke/board-writes.smoke.test.ts
  - id: openwiki-source-6ff7ec04aac5c9f6567d2d84
    resource: repo://dashboard/smoke/harness.ts
  - id: openwiki-source-9bacaebabf4730d3f0a79cc9
    resource: repo://dashboard/src/server.ts
  - id: openwiki-source-2ead01a8e29c111bfb46b6f1
    resource: repo://dashboard/src/ui/agentprofiles.js
  - id: openwiki-source-2b9db73e8eaa164d3fba79a2
    resource: repo://dashboard/src/ui/app.js
  - id: openwiki-source-41b10777200e9da21f4768dc
    resource: repo://dashboard/src/ui/boardwrites.js
  - id: openwiki-source-b72b7ea677ab1e9f8fcc0e50
    resource: repo://dashboard/src/ui/diagnosticchat.js
  - id: openwiki-source-9dc12756d2cc067f897826b6
    resource: repo://dashboard/src/ui/discord.js
  - id: openwiki-source-56b812868de34201e0046a3c
    resource: repo://dashboard/src/ui/packetchat.js
  - id: openwiki-source-b69f10da7d235560b52d92b2
    resource: repo://dashboard/src/ui/pipeline.js
  - id: openwiki-source-460c27048f01dfd9cb2c51ee
    resource: repo://dashboard/src/ui/setup-journal.js
  - id: openwiki-source-97e7306380c1609d77e9989e
    resource: repo://dashboard/src/ui/style.css
  - id: openwiki-source-8f16f07389eccb57a739027f
    resource: repo://dashboard/src/ui/systemhealth.js
  - id: openwiki-source-233dd29606c16bd136fa941d
    resource: repo://dashboard/src/ui/taskdialog.js
  - id: openwiki-source-0c51ba8e775e5d76ec1840dd
    resource: repo://dashboard/src/ui/theme.js
  - id: openwiki-source-096a781fb160ef979fa31121
    resource: repo://INSTALL.md
  - id: openwiki-source-02886ae4c9efa40e167cf53b
    resource: repo://mc/config/config.go
  - id: openwiki-source-4b4fe89674376e41801774a5
    resource: repo://mc/deployment/agentprofile_auth.go
  - id: openwiki-source-a4b6d508300704786e4ce1a2
    resource: repo://mc/dispatch/delivery.go
  - id: openwiki-source-3007affdd6a005d3387629bc
    resource: repo://mc/mclog/mclog.go
  - id: openwiki-source-45770f873ba593d4d685f66f
    resource: repo://mc/substrate/substrate.go
  - id: openwiki-source-4b2c2a82df1cffc64cdb7c34
    resource: repo://mc/verbs/directives/setup.md
  - id: openwiki-source-f1ae658c98ff5e2d177d4b8a
    resource: repo://mc/verbs/packet.go
  - id: openwiki-source-19fe7d061285a802d1068aca
    resource: repo://mc/verbs/roles.go
  - id: openwiki-source-bb09a9b9edeb77b9bd13e9d0
    resource: repo://mc/verbs/skills/setup/mc-setup/SKILL.md
  - id: openwiki-source-cd6248e71941a23b0297cb03
    resource: repo://resident/src/credential-projector.ts
  - id: openwiki-source-c18554fde4cd0e38831a6f1d
    resource: repo://resident/src/discord.ts
  - id: openwiki-source-e028a0037f071b894557c29d
    resource: repo://resident/src/effects.ts
  - id: openwiki-source-7092e09d830ef9ed23536948
    resource: repo://resident/src/environment-lane.ts
  - id: openwiki-source-fca867d7fea1f4f6917fe44b
    resource: repo://resident/src/provider-credentials.ts
  - id: openwiki-source-afc1b9cc18bf71238f6d8781
    resource: repo://work/dashboard-theme-sets/implementation-inventory.md
  - id: openwiki-source-56ebfdef4e75b22f9a00bc23
    resource: repo://work/dashboard-theme-sets/index.html
  - id: openwiki-source-8728a03b395bc31867fc976f
    resource: repo://work/pipeline-fonts.html
  - id: openwiki-source-14429182ea3f0f7fb7ef44f9
    resource: repo://work/render-font-comparison.swift
  - id: openwiki-source-757fb199673cdc77495feb98
    resource: repo://work/security-posture-2026-10-19/culler-security-posture.html
  - id: openwiki-source-ab3baadfec4a3008dbdc8ae9
    resource: repo://work/security-posture-2026-10-19/culler-security-research.html
generated: { by: "culler-scribe", at: "2026-11-05T09:00:19.897Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-05T10:01:23.917Z
---

# Culler quickstart

Culler is a local-first orchestration system for AI coding agents with one human operator. It deploys onto one Mac with Apple silicon and runs unattended. The operator gives Culler a Project, which is a Git repository. A pipeline of agent roles then proposes, selects, builds, verifies, and packages tasks on that Project. The operator approves each result, asks for a revision, or cancels it.

`VISION.md` is the current work order of this repository: make the codebase a portfolio-grade open-source project. It states intent and priorities, not current behavior.

## The components

| Component | Path | Responsibility |
| --- | --- | --- |
| `mc` | `mc/` (Go) | The command-line program. It is the only writer of the spine and owns dispatch and control. |
| Spine | `mc/substrate/schema.sql` | One SQLite database. It is the message bus and the ground truth. |
| Resident | `resident/` (Bun) | The host daemon. It runs the tick loop, starts containers, and runs the repository, environment, trace, and wiki lanes. |
| Dashboard | `dashboard/` (Bun) | The operator UI on `127.0.0.1:7333`. Its API mirrors `mc` verbs. |
| Runners | `runner/` | Container code that runs one agent harness per run, plus Homie and setup runners, the fake harness, Project images, and the private runtime. |
| AgentsView | `mc/deployment/agentsview.json` | The pinned third-party trace viewer that launchd supervises. |
| Evals | `evals/` (Python) | The evaluation harness for agent roles. |

TypeScript processes never open the spine. They spawn `mc` and read one JSON object from its standard output. Exit code 0 means success, 1 means a domain rejection, and 2 means a usage or environment error. See [System architecture](architecture/system-overview.md).

## Vocabulary

- **Project**: one Git repository that Culler works on.
- **Task**: one unit of work. Each task delivers one Git commit.
- **Roles**: the Strategist proposes, the Arbiter selects, the Worker builds, the Verifier judges, the Scribe updates the project wiki, and the Packager writes the review packet. See [The task pipeline roles](workflows/task-pipeline.md).
- **Run**: one role execution in a fresh container. The launch envelope `MC_HOME/runs/<run_id>.json` is mounted at `/mc/run.json`.
- **Lease**: each Project has at most one agent run with its lease. Homie, Scribe runs, and repository operations take no lease.
- **Homie**: the conversational tier for the operator. See [Homie](concepts/homie.md).

## Common tasks

| I want to… | Read |
| --- | --- |
| Understand how processes fit together | [System architecture](architecture/system-overview.md) |
| Change the database schema | [The spine](architecture/spine-and-substrate.md) |
| Add or change an `mc` verb | [The mc CLI and verb scope](architecture/mc-cli-and-verb-scope.md) |
| Understand why a Project is idle or what runs next | [Dispatch and the tick loop](workflows/dispatch-and-tick-loop.md) |
| Change what an agent role receives | [Agent input](concepts/agent-input.md) |
| Follow a commit from Worker to merge | [Git delivery](workflows/git-delivery.md) |
| Resolve an operator decision report | [Decision Pending and requirements revisions](workflows/decision-resolution.md) |
| Debug retries, stalls, or Decision Pending | [Failure controls](concepts/failure-controls.md) |
| Change the host daemon | [The resident](architecture/resident.md) |
| Change container or harness behavior | [Runners](architecture/runners.md) |
| Configure Discord conversations and alerts | [Discord setup and delivery](integrations/discord.md) |
| Discuss a review packet with Homie | [Packet conversations](workflows/packet-conversations.md) |
| Inspect task runs and evidence from Review | [Task details from Review](architecture/dashboard.md#task-details-from-review) |
| Inspect a Verifier correction | [Task correction details](architecture/dashboard.md#verifier-correction-details) |
| Change the operator UI | [The dashboard](architecture/dashboard.md) |
| Apply shared colors and dialog controls | [Culler blue conventions](architecture/dashboard.md#shared-culler-blue-conventions) |
| Add a log event or change log output | [Logging and log parity](concepts/logging.md) |
| Understand trace access and the host-only viewer token | [Viewer authentication](integrations/openwiki-and-viewers.md#browser-authentication) |
| Work on the wiki export or trace viewer | [OpenWiki and AgentsView](integrations/openwiki-and-viewers.md) |
| Configure agent sign-in or inspect access renewal | [Agent connections](operations/agent-connections.md) |
| Install, redeploy, or release | [Installation and deployment](operations/install-and-deployment.md) |
| Check image contents, update size, or image cleanup | [Image delivery and update cleanup](operations/image-delivery.md) |
| Check or control local services | [Culler lifecycle command](operations/culler-lifecycle.md) |
| Assess release scans, secret scanning, or nightly reports | [Release security scans](operations/release-security.md) |
| Understand reuse of image compatibility results | [Recorded runtime checks](workflows/project-environments.md#reuse-of-recorded-runtime-checks) |
| Understand persistent dependencies and separate role files | [Project storage](workflows/project-environments.md#runtime-and-per-run-dependencies) |
| Check dependency reuse across containers | [Dependency reuse checks](tests/checks-and-test-lanes.md#project-dependency-reuse-checks) |
| Prepare a Project or change its dependencies | [Project environments](workflows/project-environments.md) |
| Assess missing prerequisites or an image proposal | [Setup checklist and evidence](workflows/project-environments.md#setup-checklist-and-evidence) |
| Interpret setup milestones, retries, or retained history | [Setup progress](workflows/project-environments.md#setup-progress-and-history) |
| Interpret performance or agent experiment results | [Performance and evaluations](tests/performance-and-evaluations.md) |
| Understand task verdicts and unchanged test failures | [Task verification and base comparisons](tests/checks-and-test-lanes.md#task-verification-and-base-comparisons) |
| Understand invalid proposal rejection | [Invalid criteria and all-reject batches](workflows/task-pipeline.md#invalid-criteria-and-all-reject-batches) |
| Run the right checks before a commit | [Checks and test lanes](tests/checks-and-test-lanes.md) |

The Pipeline page places the Project wiki section beside the current task run banner, above the task path.
At narrow widths, the wiki section follows the banner.
Select the Scribe to open its profile and custom instruction controls.
Open the task dialog to inspect the unsuccessful Verifier correction count and the task history.
The correction report appears in the alert when the Verifier correction limit applies.
Routine correction results use a neutral tone in recent runs and task history.
See [the role map](architecture/dashboard.md#role-map-and-trace-access) for state, layout, and role details.

## Source map and system boundaries

The source map follows responsibility across components:

| Responsibility | Source entry points | Detailed explanation |
| --- | --- | --- |
| State and legal transitions | `mc/substrate/substrate.go` combines the schema files; `mc/domain/` supplies domain operations. | [Spine](architecture/spine-and-substrate.md) |
| Agent selection and execution | `mc/dispatch/dispatch.go`, `mc/verbs/dispatchseam.go`, `resident/src/tick-loop.ts`, and `resident/src/effects.ts` connect decisions to containers. | [Dispatch](workflows/dispatch-and-tick-loop.md) |
| Source delivery and review | `mc/substrate/delivery.sql` retains versions and decisions; `resident/src/repository-lane.ts` executes repository work. | [Git delivery](workflows/git-delivery.md) |
| Project preparation | `resident/src/environment-lane.ts` runs environment operations; `runner/setup-runner/` prepares dependencies and reports results. | [Project environments](workflows/project-environments.md) |
| Agent input and execution | `mc/verbs/brief.go` and the role skills define input; `runner/agent-runner/main.ts` selects a harness adapter. | [Agent input](concepts/agent-input.md) and [runners](architecture/runners.md) |
| Operator actions and progress | `dashboard/src/api.ts` invokes commands; `dashboard/src/ui/app.js` connects browser views. | [Dashboard](architecture/dashboard.md) |
| Wiki updates and exports | `mc/dispatch/wikiupdate.go` selects wiki runs; `resident/src/wiki-lane.ts` manages exports. | [OpenWiki and viewers](integrations/openwiki-and-viewers.md) |
| Release contents | `mc/deployment/host_release.go` and `runner_release.go` list shipped files; `.github/workflows/release.yml` checks and publishes releases. | [Installation and deployment](operations/install-and-deployment.md) and [image delivery](operations/image-delivery.md) |

A setup report and source acceptance represent different boundaries.
The Setup Journal keeps one current row for each milestone and preserves earlier operation messages.
To revisit setup history, open **Configs**, select **Projects**, and open the Project's Environment page.
Select **Setup history** to open its journal on the Board.
The action appears after initial setup completes or selects an environment, when a setup identifier exists.
**Open Environment** returns to that Project's Environment page.
A retry resets the relevant milestone until its own evidence proves completion.
See [setup progress and history](workflows/project-environments.md#setup-progress-and-history) before interpreting a complete preparation report as a ready Project.

## Toolchain and first checks

`.mise.toml` pins Go and Bun. Run every `go` and `bun` command through `mise exec --`. Each component has a `check.sh`. The fast suite is free of Docker and model tokens:

```sh
./mc/check.sh && ./runner/fake-harness/check.sh && ./runner/agent-runner/check.sh && \
./runner/homie-runner/check.sh && ./runner/setup-runner/check.sh && ./runner/shared/check.sh && ./runner/image/check.sh && \
./resident/check.sh && ./dashboard/check.sh
```

The required browser suite runs separately through `./dashboard/smoke.sh`.
Pull requests and pushes to `main` run that suite in the `dashboard-browser` job.
Both publication jobs depend on its success from the same workflow run.
The [test guide](tests/checks-and-test-lanes.md#required-browser-checks) explains the report gate, browser versions, and retained evidence.

The same component checks run unchanged on the Mac, on Linux, and in an agent container. `AGENTS.md` holds the repository rules that bind every coding agent, and `INSTALL.md` holds the operator installation guide.

## Code standards in force

`VISION.md` and `AGENTS.md` require that code stands on its own. Comments must not cite absent documents, decision labels, or stale `file:line` locations. Comments must be short and cover only tricky code. See [Logging and log parity](concepts/logging.md).

Project initialization prepares available dependencies before task work starts.
Setup and pipeline runs share a persistent Project home at `/home/agent`.
It retains normal dependency caches, tools, and Project tool configuration.
Each source workspace remains fresh, and each container has separate role files.
The Environment page reports home and disposable cache storage separately.
Clear /cache preserves the Project home.
The setup agent assesses every missing prerequisite before its final image decision.
An image proposal requires a material development benefit and credible feasibility evidence.
Expected capabilities remain unverified until the assigned image executes the affected commands.
See the [setup checklist](workflows/project-environments.md#setup-checklist-and-evidence) for proposal and completion requirements.
A complete setup report can retain build or test limits; task verification still follows repository requirements.
The Scribe updates the wiki on a schedule, separately from task execution.
Its wiki commit merges into `main` without a review packet.

The repository also retains operational evidence and design material.
`acceptance/` holds host acceptance procedures and checks.
`performance/` and `evals/` preserve experiment tools and evidence.
`third_party/` holds vendored dependency material.
Production ships only the explicit release manifests.

The task model uses only tasks with destination `main`.
The Strategist submits at most two proposals per batch.
An operator approval gives its task Project priority. An open correction receives preference without that Project priority.
See the [task pipeline](workflows/task-pipeline.md) and [dispatch](workflows/dispatch-and-tick-loop.md) for the exact controls.

For this repository, `Dockerfile.culler-project` supplies `/private/tmp` and the `/usr/bin/git` path required by repository checks.
The ordinary component checks run in the Project container.
The Verifier checks task criteria, new or changed tests, and broad Project checks for task regressions.
Unchanged failures and unavailable host checks outside the criteria permit task progress when the other requirements hold.
The evidence and Review Packet preserve those limits without an additional approval requirement.
Separate release and host acceptance checks retain their requirements.
Platform and host dependencies can leave individual checks unverified; the [test guide](tests/checks-and-test-lanes.md) explains that evidence.

## Packet discussions and retained design material

The Live queue and Ledger cards identify each packet by task number and task title.
The title appears below the card metadata and uses at most three lines.
The reader and quick chat headings use the packet row title.
See [the packet viewer](architecture/dashboard.md#the-review-packet-viewer) for the title source and checks.

Select **Task** in the reader to open the task description, run history, trace links, and Verifier evidence.
The evidence control copies its host path. Escape closes the task dialog and keeps the reader open.
See [task details from Review](architecture/dashboard.md#task-details-from-review) for refresh and layout behavior.

The Review reader offers a Homie conversation tied to the selected packet and version.
Select Ask Homie in the wide decision bar, or the Homie view in the narrow layout.
The quick chat model control selects the profile for new Homie conversations.
A different profile starts a new packet conversation; confirmation protects a conversation with messages.
The same conversation remains available on the Homie page after its first message.
The runner supplies the report context and read-only access to that task's artifacts.
See [packet conversations](workflows/packet-conversations.md) for preparation, report limits, decisions, and recovery.

The repository keeps additional design and research material under `work/`:

- [Dashboard theme comparison](../work/dashboard-theme-sets/index.html) compares Culler blue, Soft blue, Paper, and Slate in light and dark previews.
- [Dashboard conventions](../work/dashboard-theme-sets/dashboard-conventions.md) defines the Culler blue design requirements.
- [Implementation inventory](../work/dashboard-theme-sets/implementation-inventory.md) maps shared components, exceptions, and recorded checks.

- [Pipeline font examples](../work/pipeline-fonts.html) compare typefaces in a Pipeline preview.
- [Font comparison](../work/pipeline-font-comparison.html) displays the retained comparison image.
- [The image](../work/pipeline-font-comparison.png) and [Swift renderer](../work/render-font-comparison.swift) preserve the comparison artifact and its generation code.
- [Security proposals](../work/security-posture-2026-10-19/culler-security-posture.html) describe proposed controls and a separate dependency-storage follow-up.
- [Security research](../work/security-posture-2026-10-19/culler-security-research.html) preserves the source review and its evidence dated 19 October 2026.

The security research names commit `816470a9f790005da233081a7d8ef60ba84b5973` as its reviewed source.
It separates selected executed checks from unverified deployment, authentication, and broader security checks.
Its findings remain attributed research at that commit.
The proposal page identifies a separate documentation baseline at `839b8ef2402f` for its Tools across runs follow-up.
Neither document establishes fresh verification of the accepted deployment.

The proposal page contains component diagrams, data-flow examples, and glossaries for its proposed controls.
Its scope covers these groups:

- Release checks, dashboard access, secret storage, and release identity.
- Provider credentials, Project secrets, network access, and Homie file access.
- Sandboxes, external actions, tool isolation, hostile Projects, and tools across runs.

The Tools across runs section proposes separate role tool state and verified disposable download caches.
The current implementation instead shares a persistent Project home while separating role credentials, sessions, skills, and instructions.
See [Project storage](workflows/project-environments.md#runtime-and-per-run-dependencies) for that implemented boundary.
The comparison section proposes deployment checks with synthetic data and explicit evidence for each image and platform.
The document labels these controls as proposals and the diagrams as illustrative examples.
The font and theme previews are design material. The dashboard source defines the product's actual appearance.
The shipped dashboard uses Culler blue in light and dark themes.
The light theme pairs porcelain paper with white surfaces and navy text.
The dark theme pairs navy paper with dark blue surfaces and pale text.
The page shows the background picture's tile lines until the operator selects the logo button.
The browser keeps that selection across reloads and dashboard tabs.
The theme comparison viewer keeps its own preference and preview controls; it supplies no production theme selector.
See [shared dashboard conventions](architecture/dashboard.md#shared-culler-blue-conventions) for dialog controls, trace pages, and browser checks.
The implementation inventory records earlier check results. Those results are separate from this wiki update's source review.


## Discuss a diagnostic issue

Select **Ask Homie** from an issue view to open an ordinary conversation with its summary and relevant record identifiers.
The action asks for an explanation and proposed action, and preserves the existing conversation and composer draft.
The [diagnostic conversation guide](architecture/dashboard.md#diagnostic-conversations) explains the available views, evidence limits, and retry behavior.
The [Homie authority guide](concepts/homie.md#diagnostic-context-and-authority) explains current-state reads, unavailable evidence, and explicit confirmation before state changes.

## Resolve an operator decision report

Open Resolve decision for the task on the Board, or discuss the report with Homie.
The task dialog offers no Require decision control.
For an operator decision report, the resolution requires a changed description and a reason.
Put every requirement in the description. Use the reason to explain the change.
Cancel task is the other resolution.

A packaged task shows no Edit title and priority control in its task dialog.
The task dialog still shows controls that its decision and conditions permit.

The applied revision ends the report condition and returns the task to Seeded.
An active run or repository operation can delay application.
The Worker continues from retained task work, and the Verifier checks the full change from the original task base.
Other Decision Pending conditions have their own controls.
See [decision resolution](workflows/decision-resolution.md) for those controls and the evidence behind them.

## Connect Discord

After the first Project completes initial source acceptance, open **Configs → Discord**.
Select **Set up Discord**, check the bot, choose a channel, and send the explicit test message.
Use **Refresh list** if the server or channel is absent.
Send only the displayed code in the selected Discord channel, then approve the identified account in the dashboard.
Discord remains optional, and one connection serves the installation.
Each new channel message starts a Homie conversation in its own thread.
Continue the conversation in that thread or open the same session in the dashboard.
The bot needs Create Public Threads and Send Messages in Threads permissions.

The connection card controls Blocked tasks and System health alerts, connection checks, edits, and disconnect.
Check connection sends no test message.
The [Discord guide](integrations/discord.md) explains permissions, temporary drafts, account access, delivery retirement, and recovery.
The [verification report](../.tracking/discord-configuration/REPORT.md) preserves historical results and their limits.
It leaves a reply from a live agent unverified.

## Agent sign-in

Culler supports account sign-in and static API keys for Codex and Claude Code.
The host retains provider credentials and supplies temporary Culler tokens to agent containers.
Profiles select a connection, model, and optional effort; several profiles can share a connection.
The model list offers its supported effort levels when it supplies them.
The model test checks a selected effort before the profile is saved.
The role assignment dialog offers an Edit action for each enabled profile.
The release defines permitted provider destinations.
See [Agent connections and access renewal](operations/agent-connections.md) for configuration, host storage, runner behavior, and verification boundaries.


## Resolve a shared proposal decision

Several blocked proposals can await one shared decision.
The notice, issue dialog, and System health count that decision once.
The Board's **Blocked tasks** count includes each blocked task.
Open the shared issue to read its complete question and follow its affected proposal links.
Select **Resolve proposal decision** to record the resolution reason.
Separate task blocks, retry exhaustion, and allowance conditions retain their own issues.
See [shared proposal decisions](workflows/decision-resolution.md#shared-proposal-decisions) for the controls and resolution boundaries.
