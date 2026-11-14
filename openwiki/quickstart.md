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
  - id: openwiki-source-ca6cb4b1a14fd7969dfae3ec
    resource: repo://CHANGELOG.md
  - id: openwiki-source-5581b682dfee807415160cbe
    resource: repo://dashboard/PAGES.md
  - id: openwiki-source-ddff79bc537156c26cb2035f
    resource: repo://dashboard/smoke/board-writes.smoke.test.ts
  - id: openwiki-source-6ff7ec04aac5c9f6567d2d84
    resource: repo://dashboard/smoke/harness.ts
  - id: openwiki-source-4ea15ae4c53141155926c420
    resource: repo://dashboard/smoke/model-configuration.smoke.test.ts
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
  - id: openwiki-source-927b6a817962ef194108a04b
    resource: repo://dashboard/src/ui/index.html
  - id: openwiki-source-56b812868de34201e0046a3c
    resource: repo://dashboard/src/ui/packetchat.js
  - id: openwiki-source-b69f10da7d235560b52d92b2
    resource: repo://dashboard/src/ui/pipeline.js
  - id: openwiki-source-b10e7836992abc73d7e0459e
    resource: repo://dashboard/src/ui/scene.js
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
  - id: openwiki-source-b268cde76acf05adc6f0e703
    resource: repo://GIT-DELIVERY.md
  - id: openwiki-source-096a781fb160ef979fa31121
    resource: repo://INSTALL.md
  - id: openwiki-source-2421a6d0188a5ae9913930bf
    resource: repo://mc/cmd/mc/agent_skill_prod.go
  - id: openwiki-source-8887a56335011659b61b947b
    resource: repo://mc/cmd/mc/candidate-runtime-check.sh
  - id: openwiki-source-a7ca3be58a8971f19014beb8
    resource: repo://mc/cmd/mc/cli_test.go
  - id: openwiki-source-b7fbb75b893eadf967f11537
    resource: repo://mc/cmd/mc/setup_agents_prod.go
  - id: openwiki-source-ae83eaa3edce8d9e15735e02
    resource: repo://mc/cmd/mc/setup_untested_connections_prod_test.go
  - id: openwiki-source-30e353995389d69cb272185f
    resource: repo://mc/cmd/mc/setup_update_transaction_prod.go
  - id: openwiki-source-07cdfe29b43b302d6030a5c9
    resource: repo://mc/cmd/mc/systemstatus_setup_test.go
  - id: openwiki-source-02886ae4c9efa40e167cf53b
    resource: repo://mc/config/config.go
  - id: openwiki-source-4b4fe89674376e41801774a5
    resource: repo://mc/deployment/agentprofile_auth.go
  - id: openwiki-source-a4b6d508300704786e4ce1a2
    resource: repo://mc/dispatch/delivery.go
  - id: openwiki-source-b578a24182c530b5d8630c21
    resource: repo://mc/dispatch/dispatch.go
  - id: openwiki-source-4470a63a5128bc251c50ad5e
    resource: repo://mc/dispatch/README.md
  - id: openwiki-source-45770f873ba593d4d685f66f
    resource: repo://mc/substrate/substrate.go
  - id: openwiki-source-f3e18532108643d261c29701
    resource: repo://mc/verbs/configsave.go
  - id: openwiki-source-4b2c2a82df1cffc64cdb7c34
    resource: repo://mc/verbs/directives/setup.md
  - id: openwiki-source-72313a4f79741897a46d7a11
    resource: repo://mc/verbs/directives/strategist.md
  - id: openwiki-source-f1ae658c98ff5e2d177d4b8a
    resource: repo://mc/verbs/packet.go
  - id: openwiki-source-19fe7d061285a802d1068aca
    resource: repo://mc/verbs/roles.go
  - id: openwiki-source-bb09a9b9edeb77b9bd13e9d0
    resource: repo://mc/verbs/skills/setup/mc-setup/SKILL.md
  - id: openwiki-source-a6635e9c51b05f45725be1ce
    resource: repo://mc/verbs/skills/shared/task-description/SKILL.md
  - id: openwiki-source-0097b0a41bffd53dce10abf3
    resource: repo://mc/verbs/skills/verifier/mc-verifier/SKILL.md
  - id: openwiki-source-f1f8658bc93aa1e51c399e87
    resource: repo://mc/verbs/skills/worker/mc-worker/SKILL.md
  - id: openwiki-source-23775c3de52f3ab95a13cb8b
    resource: repo://README.md
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
  - id: openwiki-source-cace99190acf5608e60583a3
    resource: repo://runner/image/Dockerfile
  - id: openwiki-source-1430dd49737c0f35b616bb76
    resource: repo://runner/image/runtime-check.sh
generated: { by: "culler-scribe", at: "2026-11-13T23:23:34.077Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-13T23:23:34.077Z
---

# Culler quickstart

Culler is a local-first orchestration system for AI coding agents with one human operator. It deploys onto one Mac with Apple silicon and runs unattended. The operator gives Culler a Project, which is a Git repository. A pipeline of agent roles then proposes, selects, builds, verifies, and packages tasks on that Project. The operator approves each result, asks for a revision, or cancels it. Approved work and wiki updates land on the Project's `culler` branch, which initialization starts from `main`; see [Git delivery](workflows/git-delivery.md#the-culler-branch).

`VISION.md` is the current work order of this repository. It states intent and priorities, not current behavior.

`README.md` is the public product overview. It introduces the five-role factory line, the features, the one-line installer, the host and container architecture, the security boundaries, the requirements, and the v1 roadmap.
Its header shows `docs/logo-full.svg`, and its figures are `docs/hero-dark.svg` and `docs/architecture-dark.svg`. The roadmap states plans, not current behavior.
For installation details use `INSTALL.md`; for agent rules use `AGENTS.md`.

## The components

| Component | Path | Responsibility |
| --- | --- | --- |
| `mc` | `mc/` (Go) | The command-line program. It is the only writer of the spine and owns dispatch and control. |
| Spine | `mc/substrate/schema.sql` | One SQLite database. It is the message bus and the ground truth. |
| Resident | `resident/` (Bun) | The host daemon. It runs the tick loop, starts containers, and runs the repository, environment, trace, and wiki lanes. |
| Dashboard | `dashboard/` (Bun) | The operator UI on `127.0.0.1:7333`. Its API mirrors `mc` verbs. |
| Runners | `runner/` | Container code that runs one agent harness per run, plus Collie and setup runners, the fake harness, Project images, and the private runtime. |
| AgentsView | `mc/deployment/agentsview.json` | The pinned third-party trace viewer that launchd supervises. |
| Evals | `evals/` (Python) | The evaluation harness for agent roles. |

TypeScript processes never open the spine. They spawn `mc` and read one JSON object from its standard output. Exit code 0 means success, 1 means a domain rejection, and 2 means a usage or environment error. See [System architecture](architecture/system-overview.md).

## Vocabulary

- **Project**: one Git repository that Culler works on.
- **Task**: one unit of work. Each task delivers one Git commit.
- **Roles**: the Strategist proposes, the Arbiter selects, the Worker builds, the Verifier judges, the Scribe updates the project wiki, and the Packager writes the review packet. See [The task pipeline roles](workflows/task-pipeline.md).
- **Run**: one role execution in a fresh container. The launch envelope `MC_HOME/runs/<run_id>.json` is mounted at `/mc/run.json`.
- **Lease**: each Project has at most one agent run with its lease. Collie, Scribe runs, and repository operations take no lease.
- **Collie**: the conversational tier for the operator. See [Collie](concepts/collie.md).

## Common tasks

| I want to… | Read |
| --- | --- |
| Understand how processes fit together | [System architecture](architecture/system-overview.md) |
| Change the database schema | [The spine](architecture/spine-and-substrate.md) |
| Add or change an `mc` verb | [The mc CLI and verb scope](architecture/mc-cli-and-verb-scope.md) |
| Understand why a Project is idle or what runs next | [Dispatch and the tick loop](workflows/dispatch-and-tick-loop.md) |
| Change what an agent role receives | [Agent input](concepts/agent-input.md) |
| Find Strategist rules and conditional procedures | [Strategist input](concepts/agent-input.md#strategist-instructions-and-procedures) |
| Find Arbiter rules and conditional procedures | [Arbiter input](concepts/agent-input.md#arbiter-instructions-and-procedures) |
| Follow proposal research and batch submission | [Strategist workflow](workflows/task-pipeline.md#strategist-research-and-submission) |
| Understand task descriptions and acceptance criteria | [Shared task description standard](concepts/agent-input.md#shared-task-description-standard) |
| Understand proposal evidence and packet openings | [Strategic rationale in packets](workflows/task-pipeline.md#the-packet-opening) |
| Follow proposal review and verdict submission | [Arbiter workflow](workflows/task-pipeline.md#arbiter-review-and-submission) |
| Follow a commit from Worker to merge | [Git delivery](workflows/git-delivery.md) |
| Resolve an operator decision report | [Decision Pending and requirements revisions](workflows/decision-resolution.md) |
| Debug retries, stalls, or Decision Pending | [Failure controls](concepts/failure-controls.md) |
| Change the host daemon | [The resident](architecture/resident.md) |
| Change container or harness behavior | [Runners](architecture/runners.md) |
| Configure Discord conversations and alerts | [Discord setup and delivery](integrations/discord.md) |
| Discuss a review packet with Collie | [Packet conversations](workflows/packet-conversations.md) |
| Inspect task runs and evidence from Review | [Task details from Review](architecture/dashboard.md#task-details-from-review) |
| Inspect a Verifier correction | [Task correction details](architecture/dashboard.md#verifier-correction-details) |
| Change the operator UI | [The dashboard](architecture/dashboard.md) |
| Understand the logo button and background picture motion | [Dashboard background picture and motion](workflows/dashboard-background.md) |
| Apply shared colors and dialog controls | [Culler blue conventions](architecture/dashboard.md#shared-culler-blue-conventions) |
| Add a log event or change log output | [Logging and log parity](concepts/logging.md) |
| Understand trace access and the host-only viewer token | [Viewer authentication](integrations/openwiki-and-viewers.md#browser-authentication) |
| Work on the wiki export or trace viewer | [OpenWiki and AgentsView](integrations/openwiki-and-viewers.md) |
| Configure agent sign-in or inspect access renewal | [Agent connections](operations/agent-connections.md) |
| Install, redeploy, or release | [Installation and deployment](operations/install-and-deployment.md) |
| Retry or restore an interrupted published update | [Update recovery](operations/culler-lifecycle.md) |
| Check image contents, update size, or image cleanup | [Image delivery and update cleanup](operations/image-delivery.md) |
| Understand the Project image check before a published update | [Candidate runtime compatibility](operations/image-delivery.md#candidate-runtime-compatibility) |
| Understand OpenCode preparation and reuse in source deployments | [Source OpenCode packages](operations/image-delivery.md#source-package-preparation-and-reuse) |
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
| Understand unavailable task checks and their outcomes | [Unavailable checks](tests/checks-and-test-lanes.md#unavailable-checks) |
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
./runner/collie-runner/check.sh && ./runner/setup-runner/check.sh && ./runner/shared/check.sh && ./runner/image/check.sh && \
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
An image proposal requires an absent platform capability or toolchain that blocks development and that image contents can supply, with credible feasibility evidence.
A setup attempt has a 45-minute limit; after an attempt reaches it, a review attempt decides the result from the failed attempt's logs and trace.
Expected capabilities remain unverified until the assigned image executes the affected commands.
See the [setup checklist](workflows/project-environments.md#setup-checklist-and-evidence) for proposal and completion requirements.
A complete setup report can retain build or test limits; task verification still follows repository requirements.
The Scribe updates the wiki on a schedule, separately from task execution.
Its wiki commit merges into `culler` without a review packet.

The repository also retains operational evidence and design material.
`acceptance/` holds host acceptance procedures and checks.
`performance/` and `evals/` preserve experiment tools and evidence.
`third_party/` holds vendored dependency material.
Production ships only the explicit release manifests.

The task model uses only tasks with destination `culler`.
The Strategist submits at most two proposals per batch.
Its directive requires two proposals together; the command skill permits an empty batch when no valid task remains.
Each proposal uses repository evidence, excludes equivalent work, and states scope and container-checkable criteria.
The Strategist starts with a useful result, checks existing capabilities, and considers the full cost and risk.
Each proposal holds a Strategic rationale: a one-sentence hypothesis of how the change can advance the Vision, with supporting evidence when available.
An unsupported expected benefit is identified as an assumption.
The rationale stays separate from the description and adds no requirements.
The Worker and Verifier receive the description without it.
The Arbiter checks the rationale; the Packager presents it after its statement of the delivered change.
The first packet section is Strategic rationale for a Strategist task and Summary for an operator task.

The shared `task-description` skill defines task descriptions and acceptance criteria.
The Strategist, Arbiter, and Collie apply it.
Collie shows the complete draft for operator confirmation and creates tasks through the Board create action.
See [Collie task descriptions](concepts/collie.md#task-descriptions-and-creation) and the [shared standard](concepts/agent-input.md#shared-task-description-standard).
Only the Arbiter selects proposals for development.
Proposals enter at priority 2. The operator controls task priority, and the Strategist proposal schema rejects priority values.
The [Strategist input guide](concepts/agent-input.md#strategist-instructions-and-procedures) identifies the directive, brief, and command procedures.
The [role instruction checks](tests/checks-and-test-lanes.md#role-instruction-checks) explain their test coverage.
An operator approval gives its task Project priority.
Otherwise, eligible tasks follow task priority, then repairs after approval, prior Worker runs, stage, age, and task number.
At review capacity, only tasks with a live packet can proceed through that order.
A priority change preserves an active agent run.
See the [task pipeline](workflows/task-pipeline.md) and [dispatch](workflows/dispatch-and-tick-loop.md) for the exact controls.

For this repository, `Dockerfile.culler-project` supplies `/private/tmp` and the `/usr/bin/git` path required by repository checks.
The ordinary component checks run in the Project container.
The Verifier checks task criteria, new or changed tests, and broad Project checks for task regressions.
Unchanged failures and unavailable host checks outside the criteria permit task progress when the other requirements hold.
The unavailable-host-check exception excludes new or changed tests, which must pass.
An unavailable criterion check or new or changed test follows the role procedure for its cause.
Repairable prerequisites require repair or an image proposal; capabilities absent from every agent container require an operator decision.
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

The Review reader offers a Collie conversation tied to the selected packet and version.
Select Ask Collie in the wide decision bar, or the Collie view in the narrow layout.
The quick chat model control selects the profile for new Collie conversations.
A different profile starts a new packet conversation; confirmation protects a conversation with messages.
The same conversation remains available on the Collie page after its first message.
The runner supplies the report context and read-only access to that task's artifacts.
See [packet conversations](workflows/packet-conversations.md) for preparation, report limits, decisions, and recovery.

The shipped dashboard uses Culler blue in light and dark themes.
It uses the light theme until the operator selects another theme.
The browser preserves the selection across reloads and dashboard tabs.
The light theme pairs porcelain paper with white surfaces and navy text.
The dark theme pairs navy paper with dark blue surfaces and pale text.
The page shows the background picture's tile lines until the operator selects the logo button.
The browser keeps that selection across reloads and dashboard tabs.
A click moves each picture part separately; another click during motion reverses it.
Page loads, theme changes, window size changes, and selections from other tabs apply the state without motion.
The system's reduced motion preference also gives an immediate state change.
See [Dashboard background picture and motion](workflows/dashboard-background.md) for the sequence and controller.

See [shared dashboard conventions](architecture/dashboard.md#shared-culler-blue-conventions) for dialog controls, trace pages, and browser checks.


## Discuss a diagnostic issue

Select **Ask Collie** from an issue view to open an ordinary conversation with its summary and relevant record identifiers.
The action asks for an explanation and proposed action, and preserves the existing conversation and composer draft.
The [diagnostic conversation guide](architecture/dashboard.md#diagnostic-conversations) explains the available views, evidence limits, and retry behavior.
The [Collie authority guide](concepts/collie.md#diagnostic-context-and-authority) explains current-state reads, unavailable evidence, and explicit confirmation before state changes.

## Resolve an operator decision report

Open Resolve decision for the task on the Board, or discuss the report with Collie.
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
Each new channel message starts a Collie conversation in its own thread.
Continue the conversation in that thread or open the same session in the dashboard.
The bot needs Create Public Threads and Send Messages in Threads permissions.

The connection card controls Blocked tasks and System health alerts, connection checks, edits, and disconnect.
Check connection sends no test message.
The [Discord guide](integrations/discord.md) explains permissions, temporary drafts, account access, delivery retirement, and recovery.

## Agent sign-in

Culler supports account sign-in and static application programming interface (API) keys for Codex and Claude Code.
Terminal setup and dashboard profile creation first offer providers with sign-in, then **Use another provider**.
Each subscription provider offers sign-in before its API key method.
Culler gives new records snake_case names, such as `openai_connection` and `openai_profile`.
A number before the final word distinguishes an occupied name, such as `openai_2_connection`.
Terminal setup names each profile. The dashboard leaves the agent profile name empty for the operator.
A profile name cannot contain whitespace; each other character is permitted.
See [default names](operations/agent-connections.md#default-names).
**Use another provider** selects an OpenCode connection from the release catalog.
An empty provider search lists all providers that OpenCode runs.
The dashboard marks the selected provider and keeps its summary above the key field when another query hides that provider.
The terminal and dashboard provider searches list name matches before identifier and address matches.
Terminal setup updates the list after each key when screen controls are available.
In a published installation, the first such connection starts a separate download of OpenCode and its web search plugin.
The release catalog fixes provider destinations and baseline models. Validated live OpenCode model records extend the available model list.

A published installation without an OpenCode connection downloads no OpenCode program.
Source deployments prepare and verify OpenCode before service activation, even without an OpenCode connection.
They use package tools inside the selected source image and require no host npm installation.
Equal package inputs and verified installed contents permit reuse across unrelated Culler commits and checkout paths.
The first source connection uses the prepared package without another download.
See [source package preparation and reuse](operations/image-delivery.md#source-package-preparation-and-reuse) for the specification, failure behavior, and package checks.

The host retains provider credentials and supplies temporary Culler tokens to agent containers.
Profiles select a connection, model, and optional effort; several profiles can share a connection.
The model list offers its supported effort levels when it supplies them.
The model test checks a selected effort before the profile is saved.
The role assignment dialog offers an Edit action for each enabled profile.
The release defines permitted provider destinations.

Setup keeps a failed connection for retry and removes unused, untested setup connections when another connection replaces them.
A successful setup test records verification for the dashboard.
If setup cannot record the result, it keeps the connection and asks for another test.
System health excludes connections that no role uses from its issues and retains them in the unused connections disclosure.

See [Agent connections and access renewal](operations/agent-connections.md) for configuration, host storage, runner behavior, and verification boundaries.

## Recover a published update

After an interruption, `culler update` retries the exact retained candidate.
Before the update commits, `culler update --restore` restores the previous software and checkpoint.
Restore starts the previous release's helper before services so resident verification can read the spine.
A helper failure keeps work stopped; correct the reported cause, then retry `culler update --restore`.
An incomplete update keeps ordinary work stopped.
The updater verifies the candidate before it records the commit and permits work.
Its isolated Project image check accepts the default image's protected spine directory when the runtime checks pass.
See [candidate runtime compatibility](operations/image-delivery.md#candidate-runtime-compatibility) for the check and its release smoke.
See [the lifecycle guide](operations/culler-lifecycle.md) for commands and [installation and deployment](operations/install-and-deployment.md) for the update boundaries.


## Resolve a shared proposal decision

Several blocked proposals can await one shared decision.
The notice, issue dialog, and System health count that decision once.
The Board's **Blocked tasks** count includes each blocked task.
Open the shared issue to read its complete question and follow its affected proposal links.
Select **Resolve proposal decision** to record the resolution reason.
Separate task blocks, retry exhaustion, and allowance conditions retain their own issues.
See [shared proposal decisions](workflows/decision-resolution.md#shared-proposal-decisions) for the controls and resolution boundaries.

## Agent command access and native traces

Collie and external agents use the common `culler` skill and supported operator command catalog.
The installed host skill identifies the exact deployment executable and evidence paths; an external agent needs no source checkout.
Setup links it automatically under `~/.agents/skills/culler` and under the personal skill directory for a selected Claude Code or Codex harness.
Existing skills that Culler does not own remain intact.
See [operator command boundaries](architecture/mc-cli-and-verb-scope.md#supported-operator-commands) and [external agent installation](operations/install-and-deployment.md#external-agent-installation).

Native OpenCode traces use masked database copies, separate from original sessions.
The [viewer guide](integrations/openwiki-and-viewers.md#native-opencode-copy-lifecycle) explains copy availability, import status, and bounded inspection.
The resident source and its tests define that contract, and the operator command catalog in `mc/operatorcmd/catalog.go` defines command access.

Selecting subscription sign-in in terminal setup starts authorization directly.
A draft profile test preserves the saved profile's verification until matching evidence applies to its save.
See [agent connections](operations/agent-connections.md) for model refresh and connection lifecycle.

## Guide a Project role

Custom instructions apply to one role in one Project.
Save replaces the current text. Save empty text to use the standard instructions.
Each run keeps the instructions it received.
Saving instructions preserves an existing customization block.
See [agent input](concepts/agent-input.md#the-run-brief) for the contract boundary and run snapshots.


## Set agent container limits

Open **Configs → Advanced** to set the CPU limit and memory limit for new agent containers.
Zero means no limit and is the default for both settings. A memory limit permits no additional swap.
The settings apply to new pipeline, setup, and Collie containers. Existing containers keep their launch settings.
Each container has a limit of 2,048 processes.
See [resident limits](architecture/resident.md#agent-container-limits) for the settings read and launch behavior, and [policy checks](tests/checks-and-test-lanes.md#container-resource-policy-checks) for source-defined test coverage.
