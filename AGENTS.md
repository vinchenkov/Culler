# AGENTS.md

Rules for the coding agents that work in this repository.

- A **host agent** runs on the operator's Mac, outside Culler.
- A **pipeline agent** is a Culler role that develops this repository as a Culler Project, in an agent container.

Each rule applies to both kinds of agent unless its section names one.

## Culler

Culler is a local-first orchestration system for AI coding agents with one human operator.
It deploys onto one Mac and runs unattended.
The **spine** is the one SQLite database of a deployment. It is the message bus and the ground truth.

- `mc/`: the Go command-line interface (CLI) `mc`. It owns dispatch, control, and the spine.
- `resident/`: the Bun host daemon. It runs the dispatch tick and starts agent containers.
- `dashboard/`: the Bun dashboard server and its browser UI.
- `runner/`: the runner image and the runners inside agent containers.
- `release/` and `.github/workflows/`: the release tools and continuous integration (CI).
- `evals/`, `performance/`, `acceptance/`, `tests/`: evaluations, experiments, and live checks outside the fast suite.

The Strategist proposes tasks, and the Arbiter promotes one proposal.
The Worker, the Verifier, and the Packager then take the task to a review packet for the operator.
The Scribe updates `openwiki/`. Collie is the conversational agent of the operator. The setup agent prepares a Project environment.
`VISION.md` is the North Star of this repository as a Culler Project.

## Read the wiki for architecture

`openwiki/` explains the architecture and the cross-cutting parts of the code.
Start at `openwiki/quickstart.md` for the components and the vocabulary. Then read the page for your area:

| Area | Pages |
|---|---|
| Processes, the spine, `mc` verb scope | `openwiki/architecture/` |
| Agent input, Collie, failure controls, log parity | `openwiki/concepts/` |
| Dispatch, the task pipeline, Git delivery, Project environments | `openwiki/workflows/` |
| Install, deployment, updates, release | `openwiki/operations/` |
| Checks, test lanes, CI jobs | `openwiki/tests/checks-and-test-lanes.md` |

The Scribe writes the wiki from the accepted commit, so a page can lag the code. When they disagree, the code is correct.
Only the Scribe maintains `openwiki/`. Culler rejects a task submission that changes it.

These guides hold the subsystem contracts:

- `mc/dispatch/README.md`: the control model and each retained record.
- `GIT-DELIVERY.md`: repository operations.
- `runner/image/README.md`: the images and the runtime volume.
- `release/README.md`, `release/UPDATES.md`, `release/security/README.md`: release, updates, and security scans.
- `INSTALL.md`: operator installation and updates.
- `dashboard/PAGES.md`: the dashboard pages and the done-when list of each page.

## Git

- A host agent works in a worktree at `/private/tmp/culler-worktrees/<task>` on its own local branch, for example `task/<name>` or `fix/<name>`.
- macOS deletes `/private/tmp` at boot. Commit work to the branch before the session ends.
- Do not create a checkout inside the repository. Remove an obsolete worktree with `git worktree remove` after its work is safe on a branch.
- A commit subject is one imperative sentence in sentence case, with no type prefix.
- Do not push. Merge into `main` only when the operator asks.
- A pipeline agent works in `/workspace/source` and keeps its one task commit on the assigned branch.

## Toolchain

`.mise.toml` pins Go and Bun. Run each `go` and `bun` command through `mise exec --`.
Install the locked test dependencies before the first check, as CI does:

```sh
npm ci --prefix runner/openwiki --ignore-scripts
node runner/openwiki/patch-upstream.mjs runner/openwiki/node_modules/openwiki
(cd runner/agent-runner && mise exec -- bun install --frozen-lockfile)
(cd dashboard && mise exec -- bun install --frozen-lockfile)
```

## Checks

The fast suite needs no Docker and no model tokens. Run it before you hand off a change:

```sh
./mc/check.sh && ./runner/fake-harness/check.sh && ./runner/agent-runner/check.sh && \
./runner/collie-runner/check.sh && ./runner/setup-runner/check.sh && ./runner/shared/check.sh && \
./runner/image/check.sh && ./resident/check.sh && ./dashboard/check.sh
```

- `./mc/check.sh` is also the Go lint gate: `gofmt`, `go vet` for each build tag and for Linux arm64, the shell tests of the install, redeploy, uninstall, and dev-reset scripts, then all Go tests.
- `./release/check.sh` checks the release tools, `VERSION`, and `CHANGELOG.md`.
- TypeScript has no lint, format, or typecheck step. `bun test` is the only Bun test command.
- One Go test: `cd mc && ./test.sh ./dispatch/ -run '^TestName$'`.
- One Bun test: `cd <component> && mise exec -- bun test <file> -t '<name>'`.

`mc/test.sh` wraps `go test`:

- It fails when the selected test group runs zero tests.
- It prints `UNVERIFIED <package> <test>: <reason>` for each skipped test.
- On Linux it adds the `test_scratch_spine` tag, which permits a test spine on any filesystem. A `-tags` argument replaces that tag. Release builds reject it.

A Go test that reads files outside the `mc` module must run with `-count=1`.

Run an opt-in lane when your change touches what the lane proves:

- `cd mc && mise exec -- go test -tags docker_e2e -v -timeout 30m ./e2e/...`: the full pipeline with real containers.
- `cd mc && mise exec -- go test -tags docker_boundary -count=1 ./boundarydocker/...`: the container boundaries. The tag also selects tests in `mc/cmd/mc`, `mc/deployment`, and `mc/verbs`.
- `cd mc && mise exec -- go test -tags nightly ./property/...`: the dispatch properties.
- `update_docker`: the update checkpoint. `openwiki/tests/checks-and-test-lanes.md` gives its command and variables.
- `./dashboard/smoke.sh`: real Chromium against a real `mc` on a scratch spine. It is the local form of the required `dashboard-browser` CI job. Run it after each dashboard change.

## Deployments

`MC_HOME` names the deployment home. When `MC_HOME` is unset, Culler uses `~/.culler`, the production home.

| Tier | Install | Update |
|---|---|---|
| Development | `./install.sh --dev --mc-home <scratch-path>` | None |
| Source production | `sh scripts/install-source.sh` | `./redeploy.sh` |
| Published | `./install.sh` (the latest GitHub release) | `culler update` |

- The development tier uses the fake harness, a spine file at `MC_HOME/spine.db`, and no launchd. Never point it at `~/.culler`.
- `./redeploy.sh` builds the working tree, with uncommitted edits, and labels the release with the `HEAD` commit. It activates launchd. When the deployed spine has a different schema, it stops and keeps the spine. `--reset-database` replaces the spine with an empty spine after the operator types a confirmation.
- `./redeploy-dashboard.sh` moves only the live dashboard to the files committed at `HEAD`. A change that needs a new `mc` verb or a new manifest file needs `./redeploy.sh`.
- `MC_HOME=<home> ./dev-reset.sh --confirm` destroys the spine and the run data of that production-tier deployment, keeps the credentials and the configuration, and activates launchd again. It refuses to run without `MC_HOME`. `--rebuild` rebuilds the host `mc` and the image at `HEAD` as a matched pair. After a schema change, it cannot reset the spine; use `./redeploy.sh --reset-database`.
- `./uninstall.sh` removes every Culler installation that it finds, production included, and every `mc-managed` container. `--keep-data` keeps the deployment and Project data. `--dry-run` shows the plan.
- Never load launchd during development. Run `redeploy.sh`, `dev-reset.sh`, and `uninstall.sh` only when the operator asks and is present.

## Invariants

- `mc` is the only program that opens the spine. Production TypeScript spawns `mc` and reads one JSON object from stdout. Exit codes: 0 success, 1 domain rejection, 2 usage or environment error.
- `mc` gets its verb scope only from the presence of `/mc/run.json`, never from flags.
- `mc/substrate/schema.sql`, `delivery.sql`, and `environments.sql` are the schema. Their SHA-256 hash is the schema identity. A change to one of them makes each deployed spine incompatible, and no converter exists.
- `dispatch.Decide` (`mc/dispatch/dispatch.go`) is a pure function. The prepare/attest/commit seam (`mc/verbs/dispatchseam.go`) freezes canonical JSON. A change to a wire shape is a design decision, not a refactor.
- Production runs only the files that `mc/deployment/host_release.go` and `mc/deployment/runner_release.go` list. Add each new resident, dashboard, or runner file to the matching list. The closure tests in `./mc/check.sh` fail on a missing or an unused entry.
- `runner/shared/` stays under `runner/`, because the runner list cannot address `../`.
- `runner/shared/log.ts` and `mc/mclog` write byte-identical NDJSON (newline-delimited JSON). Both suites read `runner/shared/testdata/render-golden.json`. Change both implementations. Never change the fixture to satisfy one side.
- `runner/image/build-prod.sh` refuses while a `com.culler.*` launchd unit is loaded. It has no override. Stop supervision first.
- Pull request CI jobs hold no provider credentials and no publication permissions.

## When you change these things

- **What a role does, receives, or can call.** Also update the agent input: the role directive (`mc/verbs/directives/<role>.md`), the run brief (`mc/verbs/brief.go`), and the role skills (`mc/verbs/skills/<role>/`). State the new model in few words. Delete each statement that the change made false. Keep no history of the change.
- **A shipped skill.** The folder name and the frontmatter `name` must match. An additional skill can name only commands from the command surface of its role.
- **Terminal setup text** (`mc/onboarding/terminal_*.go`, `mc/cmd/mc/setup_*.go`). Each message or display change needs an adversarial subagent review of actual output at 80 and 40 columns, with and without color. The review covers clarity, concise text, inviting tone, specific actions, visible progress, and truthful completion states. Revise and review again until the reviewer accepts.
- **Operator-facing text.** The operator never runs `mc`. Never tell a human to run an `mc` command. The operator uses `culler` for lifecycle operations, `install.sh` for setup and repair, and the dashboard and Collie for configuration.
- **A dashboard browser module.** Add it to `UI_MODULES` in `dashboard/src/server.ts` and to `hostReleaseFiles` in `mc/deployment/host_release.go`, and import it from `dashboard/src/ui/app.js` or from another module. A non-JS asset needs a route in `dashboard/src/server.ts` and a manifest entry. Only `./dashboard/smoke.sh` finds a missing `UI_MODULES` entry.
- **A dashboard smoke.** Run smokes only through `./dashboard/smoke.sh`, which sets `MC_SMOKE_BIN`. Use `bootSmoke()` and `teardown()` from `dashboard/smoke/harness.ts`: one Chromium for each Bun process. Write screenshots through `evidencePath()`. Rename a `data-testid` hook only with the smoke that uses it.
- **`.github/workflows/release.yml`.** `release/publish.py`, `mc/distribution/record.go`, and `install.sh` use the job IDs. `dashboard/src/release-workflow.test.ts`, `security-gate.test.ts`, and `smoke-gate.test.ts` check the workflow.
- **A release.** Each version needs a `CHANGELOG.md` section. While `test_releases` is `true` in `mc/distribution/update_policy.json`, each passing push to `main` publishes a `test-<commit>` release without the version and migration-path gates.

## Code

- Code stands on its own. A comment, log, or error cites no document outside the repository, such as spec section numbers, decision records, or invariant numbers. Remove such references where you touch them.
- Comment only tricky code, and keep each comment short.
- Models, base URLs, and keys belong in configuration. Code knows only the harness families.
- A test proves real behavior. Fix or delete a test that does not. Never skip it.

## Pipeline agents in this repository

- The `mc` on `PATH` belongs to the deployed Culler. Use it only for your role commands. Build and test the `mc` and the dashboard of the checkout.
- The checks above run unchanged in an agent container. A test that needs a host resource skips there, and `mc/test.sh` names it `UNVERIFIED`. The `*_darwin_test.go` files need macOS.
- A check passes when its command exits zero. Its skipped tests remain unverified.
- The Verifier preserves each `UNVERIFIED` entry in the evidence file for the operator.
- The Packager follows **Verification summary** in `mc/verbs/directives/packager.md` for the review packet.
