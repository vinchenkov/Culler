# AGENTS.md

This file provides guidance to coding agents that work in this repository.

## Agent worktrees

Host agents create task worktrees through `git worktree add` under `/private/tmp/culler-worktrees/<task>`.
Each task uses a separate local branch.
Host agents keep worktree checkouts outside the repository.
Pipeline agents use the repository that Culler supplies at `/workspace/source`.
Pipeline agents keep their task commit on the assigned branch.
Agents must not create a `worktrees/` folder or another checkout inside the repository.
Git manages its worktree metadata in `.git/worktrees/`.
Agents must not place checkout files in that metadata directory.
Agents remove obsolete checkouts through `git worktree remove` after they verify that required work remains available.
The historical `.claude/worktrees/` preservation rule remains in effect.

## What this repository is

Culler: a local-first orchestration system for AI coding agents, with one human operator. It deploys onto one Mac and runs unattended. A Go CLI (`mc/`) owns all state. Two Bun/TypeScript host daemons (`resident/`, `dashboard/`) and containerized runners (`runner/`) do the work. `VISION.md` is the current work order: productionize this codebase into a portfolio-grade open-source project.

The `mc` command acts as mission control for Culler.
It owns dispatch, control, and the spine.
The command serves agents and system components.

## Toolchain and commands

`.mise.toml` pins Go 1.25.12 and Bun 1.3.12. The default Project image supplies the same versions.
A published install downloads the Bun release archive that `mc/deployment/hostbun.json` pins.
The installer verifies the SHA-256 value of that archive against the release record.
The installer places the host Bun under `MC_HOME/bun/<version>/`, and the resident and the dashboard run it.
A published install uses no mise. A source install keeps the `.mise.toml` toolchain through mise.
The separate Culler runtime supplies private programs through a Docker volume.
Project images contain Project tools and compatible platform libraries.
Run every `go` and `bun` command through `mise exec --`.

Each component has a `check.sh` entry point.
The local fast suite runs these checks:

```sh
./mc/check.sh && ./runner/fake-harness/check.sh && ./runner/agent-runner/check.sh && \
./runner/collie-runner/check.sh && ./runner/setup-runner/check.sh && ./runner/shared/check.sh && ./runner/image/check.sh && \
./resident/check.sh && ./dashboard/check.sh
```

The GitHub Actions workflow at `.github/workflows/release.yml` verifies release inputs after a push to `main`.
The workflow publishes a release only after its required checks pass.
The `test_releases` field in `mc/distribution/update_policy.json` selects the release policy.
An absent field or `false` requires a version above the latest public version and a supported update path.
The value `true` permits each new commit to publish a test release through the normal install path.
Test releases use immutable `test-<full-commit>` tags and bypass version and migration-path gates.
Set the field to `false` before the public release to restore both gates in the publisher and subsequent builds.
The bypass provides no data conversion or reset.
Each version needs a `CHANGELOG.md` section, and `./release/check.sh` checks both files.
Pull requests and pushes to `main` also run the `dashboard-browser` job.
That job runs the complete `dashboard/smoke/` suite through `./dashboard/smoke.sh` on Linux arm64.
The job uses the Go and Bun versions of `.mise.toml` and the Node.js version of the default Project image.
The job installs Playwright from `runner/image/project-tools/package-lock.json`, the lock file of the default Project image.
That Playwright installs its matching Chromium build, so the job and the default Project image use the same browser.
The `product-image` job and the `release-downloads and publication` job require the `dashboard-browser` job of the same workflow run.
The `dashboard-browser` job retains the suite log, the JUnit report, and the browser evidence as the `culler-dashboard-browser-<attempt>` artifact.
`./release/check.sh` checks the release tools without credentials or publication.
`release/image_contents.py` checks the file contents of both image archives, and `release/size_report.py` writes the size report of a fresh install and an update.
The `application-package` job builds the package alongside the fast suite and the browser suite.
The `release-images` job starts after the package job.
The `release-checks` job requires the fast suite, package, deployment, browser, and image jobs.
The publication jobs require that job before their uploads.
The vulnerability scans report results in their job summaries and artifacts.
No release job waits for a vulnerability scan, and a scan result never prevents a release.
GitHub secret scanning reports secrets in this repository on its Security tab.
The read-only `release-images` job restores the build cache of the latest `main` build from the GitHub Actions cache.
Only a push to `main` saves that cache.
The Go build caches use a separate key for each commit, job, and platform.
Each job restores the latest compatible Go build cache.
The cleanup job retains two Go build caches per job and platform on `main`.
The cleanup preserves the image caches and the dependency caches.
The product image builds on the default Project image of the same run.
The job runs the Project browser check, the runtime browser check, and the image content checks.
The job saves both images and writes the size report.
The `image-vulnerabilities` job scans those exact archives for known vulnerabilities.
The image upload job verifies and loads those exact archives.
The nightly security workflow scans the current release image digests with fresh advisory data.
`release/security/README.md` describes the security scans and the GitHub ruleset.
The [installation guide](INSTALL.md) describes public installation, updates, and release publication.

An agent container is a container that Culler starts for a setup agent or a pipeline agent.
The run variables are the environment variables that Culler sets in an agent container, such as `MC_*` and `CODEX_HOME`.
The component checks run unchanged on the Mac, on Linux, and in an agent container.
Linux test builds of `mc` use the `test_scratch_spine` build tag. The tag permits a test database on any filesystem.
On Linux, `mc/test.sh` selects that tag. A `-tags` argument of the caller replaces that selection.
Release builds reject the `test_scratch_spine` tag.
On Linux, `mc/check.sh` runs the filesystem guard tests again with `-tags=`.
Linux continuous integration (CI) also runs the filesystem guard tests without the tag.
A host-scope test requires the absence of `/mc/run.json`, the real operator home, or a block-device filesystem.
In an agent container, each check names each host-scope test that it cannot run and reports that test as unverified.
`mc/test.sh` prints one `UNVERIFIED <package> <test>: <reason>` line for each skipped test.
Each Bun component loads `runner/shared/test-environment.ts` through its `bunfig.toml` before its tests.
That file removes the run variables and points `MC_RUN_JSON` and `MC_RUN_LOG_DIR` at absent paths.
`MC_RUN_LOG_DIR` selects the log directory of a runner. The default directory is `/mc/logs`.

The fast suite is Docker-free and token-free. `mc/check.sh` is also the full lint gate: `gofmt -l`, `go vet` (untagged, per-tag, and `GOOS=linux GOARCH=arm64`), `go test -count=1 ./deployment/ ./testenv/`, the `install_test.sh`, `redeploy_test.sh`, and `uninstall_test.sh` contract tests, then `go test ./...`. On Linux, the gate then runs the filesystem guard tests without `test_scratch_spine`. There is no TypeScript lint, format, or typecheck command; `bun test` is the only Bun verb.

Host single tests:

- Go: `cd mc && ./test.sh ./dispatch/ -run '^TestName$'`
- Bun: `cd <component> && mise exec -- bun test <file> -t '<name>'`

Opt-in lanes. Run these at phase completion, never in the fast suite:

- `cd mc && mise exec -- go test -tags docker_e2e -v -timeout 30m ./e2e/...`
- `cd mc && mise exec -- go test -tags docker_boundary -count=1 ./boundarydocker/...`
- `cd mc && mise exec -- go test -tags nightly ./property/...`
- `./dashboard/smoke.sh` — real Chromium against a real `mc` on a scratch spine. Do not run the smoke files directly; the harness requires `MC_SMOKE_BIN` from the script.
  This command is the local form of the required `dashboard-browser` CI gate.
  The script fails when the browser or a test fails, and when a run executes zero tests or skips a test.
  A run without arguments also fails unless each `dashboard/smoke/*.test.ts` file executes a test.
  `MC_SMOKE_REPORT` names an absolute path for the JUnit report. The default report is scratch.
  `MC_SMOKE_EVIDENCE_DIR` names the screenshot directory.
  Each smoke screenshot writes through `evidencePath()` of `dashboard/smoke/harness.ts`, so the CI artifact retains it. A fast-suite test rejects other screenshot paths.
  The script builds a scratch `mc` from the checkout with `test_fake_routing,test_scratch_spine` the same way on each platform, unless `MC_SMOKE_BIN` names another binary.

Platform test requirements:

- The portable deployment tests execute on macOS and Linux.
- The macOS deployment tests validate generated property lists with `/usr/bin/plutil`.
- The `*_darwin_test.go` files require macOS. Linux results leave those checks unverified.
- `mc/test.sh` rejects a selected Go test group that executes zero tests.
- Go tests that read files outside the module require `-count=1`.
- A required check remains unverified when its execution dependency is unavailable.
- A check passes when its command exits zero. Its skipped tests remain unverified.
- The Verifier preserves each `UNVERIFIED` entry in the evidence file and Review Packet for the operator.
- A task pass requires successful acceptance criteria and new or changed tests, plus assessment of broad checks for task regressions.
- The Verifier compares submitted failures with the base commit under comparable conditions.
- Unchanged failures and unavailable host tests outside the criteria permit task progress when the other requirements hold.
- These observations alone require no operator decision, mandatory manual check, or additional approval.
- Separate release and host acceptance requirements retain their authority.
- The Scribe first-heartbeat grace is at least 60 seconds. The run deadline still applies.

`runner/image/toolchain-smoke.sh <image>` checks the toolchain of an image on the host.
The check copies `mc/` and `.mise.toml` from the checkout into a container of the image.
The check builds `mc` in that copy as user 10002 through `mise exec --` with `GOTOOLCHAIN=local` and `GOFLAGS=-mod=readonly`.
The build downloads the Go modules over the network.
The check compares the Go version of the image with the toolchain version in `mc/go.mod`.
The check confirms that `go.mod` and `go.sum` stay unchanged.
The host performs image checks and live acceptance through the normal acceptance procedures.

Deployment front doors. They are not interchangeable:

- `./install.sh --dev --mc-home <scratch-path>` — development install. Never point a dev install at `~/.culler`.
- `./install.sh` — production bootstrap plus the terminal setup interface and the `culler` lifecycle command.
- `./redeploy.sh` — move the live production deployment to this checkout and keep its database. Production tier only. When the deployed database has a different schema, `redeploy.sh` stops and keeps that database. The stop message names the cause and the `--reset-database` option.
- `./redeploy.sh --reset-database` — replace the deployed database with an empty database with the current schema. The operator must type a confirmation at a terminal first. The reset keeps the Project repositories and worktrees, the credentials, the operator configuration, and the deployment identity.
- `./redeploy-dashboard.sh` — move only the live dashboard to the files committed at `HEAD`. No image build, no resident restart. A change that needs a new `mc` verb or a new manifest file needs `./redeploy.sh`.
- `./dev-reset.sh --confirm [--rebuild] [--smoke]` — destroy and re-provision the dev deployment. Pass `--rebuild` when the tree moved since the last activation.
- `./uninstall.sh --keep-data` — remove software and preserve deployment and Project data.
- `./uninstall.sh` (default) or `./uninstall.sh --full-wipe` — remove software and Culler data; preserve Project repositories.

Both uninstall modes remove the host Bun under `MC_HOME/bun/` with the other Culler software.
Both uninstall modes preserve the shared host prerequisites.
Docker, Git, the mise of the operator, Go, and the Apple developer tools remain installed.

A published update removes the previous product image after it completes.
The update also removes each default Project image that no Project selects as its image or as its base.
The removal keeps each image that a container uses.
The operation record of the update lists each removed image and each kept image with its reason.
A source install removes no image.
Culler removes its private libraries with its deployed files and container images.

## Architecture

**The spine.** One SQLite database is the sole message bus and ground truth. `mc/substrate/schema.sql` is the DDL; triggers and CHECK constraints make illegal states unrepresentable. The spine lives in a Docker named volume, never on a host path (`mc/substrate/lockdomain.go` refuses non-block-device filesystems).
`mc` gives an absent or empty database the current schema.
Go and TypeScript derive the current schema identity from the same schema files.
This identity checks compatibility and requires no manual version number.

**`mc` is the sole spine writer.** No TypeScript process opens the database. The resident, the dashboard, and the runners spawn `mc` and read one JSON object from stdout. Exit codes: 0 success, 1 domain rejection, 2 usage or environment. On the host, `MC_HELPER` makes `mc` re-exec itself as `docker exec` into the helper container that can reach the spine volume. Verb scope comes only from the presence of `/mc/run.json`, never from flags; inside the production image a setuid wrapper fences the agent-facing verbs.

**Dispatch.** The resident's tick timer (`resident/src/tick-loop.ts`, one tick at a time) runs `mc dispatch`. The first tick runs at resident start. `dispatch.Decide` (`mc/dispatch/dispatch.go`) is a pure function: one Project's records plus an injected clock in, one stop, one spawn, or one idle reason out. The verbs layer executes each round through a prepare/attest/commit seam with frozen canonical JSON (`mc/verbs/dispatchseam.go`); changing any wire shape is a design decision, not a refactor. Leases are per Project: one in-flight agent run with the lease each. The Collie conversational tier and the Scribe run of a wiki update are lease-free. Repository operations claim no lease. `mc delivery start` records each due operation as an execution, and the resident starts it at once.

The [dispatch guide](mc/dispatch/README.md) describes the current control model and each retained record.
Each Project counts its own blocked or stalled tasks.
Operator approval gives a task priority within its Project.
Without an approved task, the task priority orders the next agent run.
In one task priority, a merge repair after an approval starts first, and then a task that received a Worker run.
Required receipt recovery preserves the destination during an explicit pause.
The repository lane of the resident (`resident/src/repository-lane.ts`) sends a start request at each start event and runs each execution outside the tick loop. It renews all executions in one heartbeat every 20 seconds.

**Wiki updates.** The Scribe has no task. The agent steps of a task are the Worker, the Verifier, and the Packager.
Only the Scribe maintains `openwiki/`; every other pipeline role treats it as read-only.
The Worker commits requested reports and original project documents outside the wiki through existing Project conventions, with `docs/` as the default.
Task submissions and merge candidates reject task-authored wiki changes while permitting accepted wiki updates from a destination merge.
A wiki update is one Scribe run on the accepted commit of a Project and the merge of its wiki commit into `main`.
That merge has no review packet and no operator decision.
The accepted commit is the commit at the `main` branch of the Project repository.

A wiki update becomes due when the Project wiki is behind the accepted commit and the wiki update interval ended.
`dispatch.DecideWiki` (`mc/dispatch/wikiupdate.go`) decides the wiki update of one Project in a wiki round, after the pipeline rounds of the tick.
A failed wiki update changes no task and no allowance.

**Execution.** `resident/src/effects.ts` writes each launch envelope under `MC_HOME/runs/` and starts its container.
The container reads its fixed identity at `/mc/run.json`.
The runtime volume supplies the runner, harness packages, protected commands, and private Node.js and Bun executables.
The runtime volume install copies only the runtime files that differ from the volume.
It compares the manifests of the product image and the volume.
It writes each changed file as a new file.
It removes the release marker until the install completes.
The mount permits read and execute access and blocks file changes.
Project commands use the Project image and its selected tools.

Setup Agent runs and pipeline runs share a persistent Project home at `/home/agent` through `HOME`.
The home retains normal dependency caches, installed tools, and Project tool configuration.
The Worker, Verifier, Packager, and Scribe receive a fresh source workspace at the assigned commit.
The Strategist and Arbiter read a fresh committed projection.
The runner executes the retained setup script before each Worker, Verifier, and Packager starts.
The package manager checks compatibility with current declarations and the selected Project image.

The Project also shares generic disposable dependency storage at `/cache`.
Cache eviction and removal preserve the Project home.

Credentials, sessions, role skills, and role instructions remain separate for each run.
Each task delivers one Git commit.
A Worker receives its repository under `MC_HOME/runs/repositories/<run_id>/repo`, bound at `/workspace/source`.
A Scribe receives its repository under `MC_HOME/runs/wiki-repositories/<run_id>/repo`.
The merge candidate and source acceptance use separate containers.

**Project environments.** The setup phase precedes pipeline development and uses separate operations in the spine.
The resident executes those operations through `resident/src/environment-lane.ts`.
The setup agent prepares the available dependencies and reports actual command results and environment limits.
Workspace dependencies install with ordinary agent permissions in the workspace or other permitted container locations.
Image dependencies require Project image changes through an approved standard Dockerfile and retained build files.
The builder checks Culler runtime compatibility before a continuation agent receives the image.
Each Project retains its selected base and image across Culler updates.
The resident repeats the runtime check of a selected image only for a new image or a new Culler release.
The Environment page controls drafts, image decisions, setup reports, and package caches.
Source acceptance preserves the checked configuration and source association before new work starts.
The Board shows initial setup as a full-page Setup Journal with retained operations, setup reports, and image decisions.
The journal uses the shared Markdown viewer for explanatory text and literal file views for exact Dockerfile and build contents.
Each setup report includes a summary with at most two short sentences and optional Markdown details.
The details describe useful observations. Full output remains in the trace or technical diagnostics.
The journal retains initial setup history after development starts.
A complete report permits source acceptance despite unavailable builds or tests.
An initial image decision starts a continuation that must submit `complete`.
Denial retains the current image; approval selects the exact compatible image.
Further image improvements use the Environment page after import.
Dependency preparation failures remain context when the runtime can execute the agent.
Repository requirements still control task verification.

**Dashboard.** A dependency-free Bun server on `127.0.0.1:7333`. `dashboard/src/api.ts` is a one-to-one mirror of `mc` verbs with no cache, no interpretation, and no durable state; grouping and filtering are the browser's job. The dashboard also provides Project pipeline pause controls through dedicated `mc` commands.
Pause all pauses every active Project in one transaction.
Resume all resumes every paused Project with complete initialization in one transaction.
Projects with incomplete initialization remain paused.
Pause permits active runs to finish and leaves Collie available.

**Viewers.** launchd supervises AgentsView, the trace viewer, as the third unit of a production deployment.
`mc/deployment/agentsview.json` pins the AgentsView release.
The trace lane of the resident (`resident/src/trace-lane.ts`) copies each trace into `MC_HOME/agentsview/traces/` outside the tick loop.
The trace service includes pipeline runs, Collie sessions, and setup agent operations.
Setup traces use their environment operation identity and the fixed session folder under `MC_HOME/environment-runs/`.
The runner registers each native session separately from its setup report.
Trace failures leave setup execution and its result unchanged.
The wiki lane (`resident/src/wiki-lane.ts`) runs `mc __wiki-export`, which runs the OpenWiki export command in the runner image.
AgentsView requires the AgentsView token in `MC_HOME/agentsview/token` on each API request. Each Culler request sends the token.
The dashboard authenticates browser access to AgentsView through a local proxy on a separate browser origin.
Each Culler trace link first opens the dashboard access page.
The page obtains a temporary grant through dashboard authentication.
The proxy consumes the grant once and keeps the AgentsView token on the host.
The proxy reads its service address and token through the host-only `mc __agentsview-access` command.
The dashboard process serves each wiki export at the wiki address below the wiki key, a secret path prefix in `MC_HOME/state/wiki-key`.
An agent container can reach the loopback addresses of the host. The token and the wiki key refuse those requests.
Each Collie container reads the session folders in `MC_HOME/sessions` through a read-only mount at `/mc/traces`.
The Collie container of a packet conversation reads the artifact files of its task through a read-only mount.
Each artifact path that the spine records for that task resolves through that mount.
The resident resolves the host folder of that mount through the host-only `mc __task-artifacts` command.
Each Collie container shows the `.mc-artifacts` folder of each Project folder as an empty read-only folder.

**Shipped skills.** Each shipped skill lives at `mc/verbs/skills/<role>/<skill-name>/SKILL.md`.
The `mc` binary embeds these files.
Each role directory contains its `mc-<role>` command surface and additional skills.
The `shared` directory supplies skills to pipeline agents, setup agents, and Collie.
Its `writing` skill defines the standard for text that a person or another agent reads.
Its `project-environment-setup` skill defines dependency preparation and reports of environment limits.
Its `task-description` skill defines the standard for a task description. The Strategist, the Arbiter, and Collie apply it.

The launch envelope carries the shipped skills.
The resident adds the operator's Project skills for the assigned role.
The runner installs these skills at `~/.agents/skills/` before the harness starts.
That path uses storage that belongs to each container, separate from the persistent Project home.
Concurrent roles preserve each other's skills and instructions.

An additional skill can name only commands from the role's command surface.
The directory name and the frontmatter `name` must match.

**Agent input.** Each pipeline run starts in a fresh container, and the agent knows only its agent input. The agent input has three parts: the role directive (`mc/verbs/directives/<role>.md`), the run brief (`mc/verbs/brief.go`), and the skills of the role. The run brief is a text document that a human operator can read and check. Each part of the run brief has a heading and an explanation of a maximum of two sentences. The role directive holds the rules for each run. A skill holds the procedure for a case that occurs in some runs.

**Release manifests.** Production does not run source from the clone. `mc/deployment/host_release.go` and `mc/deployment/runner_release.go` list the exact shipped files. A new resident, dashboard, or runner file must be added to the matching manifest, or the deployment fails at activation. These tests read files outside the Go module, which is why `mc/check.sh` runs `./deployment/` with `-count=1`.

**Log parity.** `runner/shared/log.ts` is the TypeScript twin of Go's `mc/mclog`. Both must write byte-identical NDJSON. The shared golden fixture `runner/shared/testdata/render-golden.json` is read by both languages' suites; change both implementations, never the fixture to satisfy one side. `runner/shared/` must stay under `runner/` — the runner release manifest cannot address `../`.

## Hard rules

- Every terminal setup message or display change requires an adversarial subagent review.
- The review covers clarity, concise text, inviting tone, specific actions, visible progress, and truthful completion states.
- The review uses actual terminal output at 80 and 40 columns, with color and without color.
- The agent must revise each rejected change and repeat the review until the reviewer accepts the result.
- A change can alter what a role does, what it receives, or what it can call. Such a change must also update the agent input that it affects: the role directive, the run brief explanations, and the skills of the role.
- The update must state the new model succinctly and remove each statement that the change made false. The update must hold no history of the change.
- Never open the spine from TypeScript. Every read and write spawns `mc`.
- A new browser-loaded dashboard module needs three edits: `UI_MODULES` in `dashboard/src/server.ts`, `hostReleaseFiles` in `mc/deployment/host_release.go`, and the import in `app.js`. Non-JS assets must be hand-listed in the manifest. Run `./mc/check.sh` after adding any asset.
- `runner/image/build-prod.sh` refuses while any `com.culler.*` launchd unit is loaded. This is deliberate and has no override; stop supervision first.
- Never load launchd during development. In production, activate supervision only while the operator is present.
- `OPERATOR-INPUTS.md` holds live secrets and is gitignored. Never commit, log, or copy its values into tracked files. When a Docker suite fails strangely, first diff the live Docker Desktop settings against the snapshot in that file.
- The operator never runs `mc`. Operator-facing text must never instruct a human to run an `mc` command.
- The operator uses `culler` for lifecycle operations and `install.sh` for setup and repair.
- The dashboard and Collie provide configuration. Agent containers retain their internal `mc` command authority.
- `.claude/worktrees/` holds uncommitted historical data. Never delete it; exclude it from searches.
- The dashboard smokes' `data-testid` hooks must survive refactors; rename one only together with the smoke that uses it. One Chromium per Bun process: use the smoke harness's `bootSmoke()`/`teardown()`, never launch your own browser.

## Agent containers

Culler can develop this repository as an ordinary Project.
In an agent container, the `mc` on PATH belongs to the deployed Culler.
Use that `mc` for your role commands only. Test the `mc` and the dashboard that you build from the checkout.

## Productionization rules (VISION.md)

- The code must stand on its own. Do not add comments, logs, or errors that cite documents absent from the repo (spec §-numbers, ADRs, invariant numbers). Remove such references where you touch them.
- No provider names in code. Runtime specifics (models, base URLs, keys) belong in configuration; code knows only the harness families.
- Cut oversized comments. Comment only tricky code.
- Fix or delete tests that do not prove real behavior. Never skip them.
- Order work cheapest and lowest-risk first; architectural changes come last.

## Authoritative documents

- `VISION.md` — the current work order and its priority order. (A `VISION.md` at a Project workspace root is a different thing: that Project's north star.)
- `dashboard/PAGES.md` — the dashboard page set and per-page done-when lists.
- Release checks must preserve these test invariants:
  - Pull request jobs hold no provider credentials or release publication permissions.
  - A selected test group with zero tests fails.
  - Go tests that read files outside their module run with `-count=1`.
- Write files in `specs/` in the declarative style: state end states, not steps; remove instructions by omission; use ASD-STE100 Simplified Technical English.
