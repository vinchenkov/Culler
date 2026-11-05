---
name: onboard
description: Set up or repair Culler through install.sh and its terminal setup interface. Use for installation, setup, or repair requests.
---

# /onboard — install and set up Culler

You are the agent that runs this procedure. The operator uses these interfaces:

- `install.sh` for setup and repair.
- `culler` for lifecycle operations.
- The dashboard or Homie for configuration.
- The coding harness for host diagnostics.

You run `mc`. The operator never runs `mc`. Do not instruct the operator to run
an `mc` command. Do not put an `mc` command in operator-facing text.

The `culler` command supports status, version, start, stop, restart, update, logs, and help.
The installer installs the command and retains the current setup interface.
An explicit service stop persists across login and computer restart.
Updates preserve that stop choice.

## Published installation and updates

The permanent launcher address is:

`https://api.github.com/repos/vinchenkov/Culler/contents/install.sh?ref=main`

Each saved launcher selects the latest complete GitHub release.
The launcher verifies that release's installer before execution.
The installer verifies the application package before execution.
The container image uses the immutable digest from the same release record.
An incomplete workflow preserves the previous available release.

A public repository supports this download:

```sh
curl --fail --location \
  --header 'Accept: application/vnd.github.raw+json' \
  'https://api.github.com/repos/vinchenkov/Culler/contents/install.sh?ref=main' \
  --output install.sh
sh install.sh
```

A private repository requires GitHub credentials before the initial download.
The GitHub command tool can supply a classic token from the operator's credential store.
A classic token requires `repo` for private repository files and `read:packages` for the private container image.
The account also requires access to the repository and its container package.
These are separate access checks.
The [GitHub registry guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry) defines package access.
The following command requires a stored classic token with the required access.

```sh
gh api \
  --header 'Accept: application/vnd.github.raw+json' \
  'repos/vinchenkov/Culler/contents/install.sh?ref=main' >install.sh
sh install.sh
```

The launcher reuses `gh auth token` for later downloads.
A private token file can replace the GitHub command tool.
The default file is `~/.config/culler/github-token`.
`CULLER_GITHUB_TOKEN_FILE` selects another absolute path for the current invocation.
Automatic checks use the default file or the GitHub credential store.
A custom path requires the same environment override on each manual invocation.
The file must belong to the operator and use mode `600`.
The file contains only the token.
The coding harness must keep its value out of commands and logs.
The [install guide](../../../INSTALL.md) explains the initial download with the token file.

The installer accepts the existing setup arguments.
`--mc-home <path>` selects the deployment home.
`--check-updates` reports the installed release and the available release without deployment changes.
`--help` shows the principal options without a download.
An installed launcher retains its deployment home and executable directory.
The update check can use scratch downloads but installs no host tools.
The same `install.sh` invocation applies an update after operator authorization.
A download through a shell pipe retains terminal input for setup.
The launcher returns the selected installer's exit status.

The development entry point remains `./install.sh --dev --mc-home <scratch-path>`.
It requires a checkout and invokes `scripts/install-source.sh`.
The local deployment scripts retain their separate purpose.

## Redeploy from a checkout

`./redeploy.sh` moves the live production deployment to the files of a checkout.
The redeploy keeps the deployed database.
Before any change, the redeploy checks the deployed database while the services run.
The check opens the database read-only in a container of the deployed image.
A database schema that differs from the release stops the redeploy before any change.
An absent database also stops the redeploy before any change.
The stop message names the cause, the state of the deployment, and the database reset option.
After this stop, running services continue to run the deployed release.
When the check cannot run, the redeploy continues, and the deploy step checks the database after the services stop.
That later stop also keeps the database. The services then stay stopped until a redeploy completes.

`./redeploy.sh --reset-database` is the database reset option.
The reset omits the database check.
The reset replaces the deployed database with an empty database with the current schema.
The reset deletes the board records: Projects, tasks, runs, review packets, Homie conversations, and Project setup records.
The reset removes the agent and Homie containers that use the database.
The reset keeps these items:

- The Project repositories and worktrees.
- The credentials and `config.toml`.
- The deployment identity.

The redeploy then copies the `config.toml` settings into the new database.
After the reset, the operator adds each Project again in the dashboard.

Before the first build step, the reset asks the operator to type `reset` at a terminal.
A run without a terminal, or without that word, changes nothing.
The agent must get the operator's decision before a reset, because the reset deletes the board records.
The agent must not type the confirmation word for the operator.

## Default terminal setup

The production entry point is `./install.sh`.
The terminal user interface guides the operator through these stages:

1. Welcome.
2. Prepare your Mac.
3. Connect an agent.
4. Review your setup.
5. Start Culler.
6. Open your dashboard.

Setup downloads a complete release before the Docker Desktop handoff.
The release contains the executable and the runtime files.
The release also contains the pinned AgentsView trace viewer and its license text.
Setup verifies the AgentsView digest, then installs the executable at `MC_HOME/bin/agentsview`.
`./redeploy.sh` installs the same pinned AgentsView release before it stops the services.
A failed AgentsView install leaves the services running.
Production setup requires no source checkout or Go compiler.
A published setup downloads the Bun release archive that the release record names.
The installer verifies the SHA-256 value of that archive against the release record.
A checksum mismatch stops the setup, and the message names both values.
The installer places the host Bun at `MC_HOME/bun/<version>/bin/bun`.
The resident and the dashboard run that host Bun.
A published setup uses no mise. A mise installation of the operator stays unchanged.
An absent Apple tool installation opens Apple's installer and leaves a resume instruction.
The operator completes Apple's permission and agreement prompts.
The [Apple installation guide](https://developer.apple.com/documentation/xcode/installing-the-command-line-tools) describes that installer.

Both uninstall modes remove the host Bun under `MC_HOME/bun/`.
Docker, Git, the mise of the operator, Go, and the Apple developer tools are shared host prerequisites.
Both uninstall modes preserve them.
Culler removes its private libraries with its deployed files and container images.
`./uninstall.sh --keep-data` preserves deployment and Project data.
`./uninstall.sh` defaults to `--full-wipe`. It removes Culler data and preserves Project repositories.

The operator installs Docker Desktop from its [official Mac installation page](https://docs.docker.com/desktop/setup/install/mac-install/).
The operator completes Docker Desktop's permission and agreement prompts.
The Docker check distinguishes these states:

- Docker Desktop is absent.
- The Docker engine is unavailable.
- The Docker command tools are unavailable.
- Docker Desktop is ready.

The **Check Docker Desktop again** action checks Docker Desktop after the operator returns.
The terminal retains completed choices through an exit or failure.
The same `install.sh` entry point resumes setup and checks the current machine state.
Healthy setup stages retain their results.

Setup reads no credential store of the operator.
Setup offers these connection choices:

- **Sign in to Codex**.
- **Sign in to Claude Code**.

Setup also offers each healthy connection that the deployment holds.
Setup connects one agent.
Setup explains that the operator can add more agents in the dashboard after setup.

A sign-in uses the Codex or Claude Code program on the Mac.
When the program is absent, setup tells the operator to install the agent.
Each sign-in uses a home folder and a credential folder that Culler creates for that sign-in.
The logins of the operator stay unchanged.

Setup tests the connection directly after sign-in.
Sign-in completion and a successful model test remain separate results.
For an unnamed, disabled Codex reference profile, setup lets Codex choose its default model.
The connection test verifies the selected model and saves its name in the profile.
Later runs use that saved model until the operator edits the profile in Model configuration.

A successful test selects the agent, and the review stage follows.
A failed test shows a short cause.
A failed token refresh adds one diagnostic line with the status code and the provider error code.
After a failed sign-in, setup shows the connection choices again.
Service activation requires a successful connection test for the selected agent.
A setup session with an unfinished agent test resumes at the connection choices.

The main path names the next action.
The terminal shows completed, current, and future stages with distinct symbols.
Color supports those symbols; plain output retains their meaning.
The active container step names the current operation.
Completed stages require verified results.
An adversarial subagent reviews each terminal message or display change against actual terminal output.
The review covers 80-column and 40-column output, with color and without color.

Setup preserves the native logins of the operator.
Secret values remain absent from diagnostic output and saved setup records.

The dashboard owns Project and vision setup.
Culler develops each Project from its files and vision.
The terminal prepares the Mac and connects the first agent before any project exists.
The dashboard offers the same project form for the first Project and each later Project.
The form preserves an existing `VISION.md` unless the operator saves an edit.
The operator can save a paused Project and add its vision later.
**Start work** checks the project and agent access before the Project resumes.
Recommended role assignments and routine time limits use their default values.
The dashboard provides advanced options after setup.
The operator confirms service activation while present.

Setup reports verified service states before it declares readiness.
The final panel reports Mac setup completion and directs the operator to add a project in the dashboard.
**Open dashboard** is the primary completion action.
The browser handoff preserves dashboard authentication.
A browser failure leaves a usable local address and a retry action.
The operator can close the terminal after the background services are ready.
Pipeline smoke and provider acceptance remain separate opt-in checks.

## Agent-assisted procedure

An agent can supply established operator choices through flags.
The agent retains authorization and choices from the current session.

### 1. Collect the operator-owned inputs

The agent supplies these inputs from the current session or asks for each unresolved choice:

- The deployment home (`MC_HOME`). Recommend `~/.culler` for
  production.
- The agent profiles to enable. Each profile selects a supported connection and a model.
  Culler supports Codex sign-in with an OpenAI account and Claude Code sign-in with an Anthropic account.
  One account is enough. The operator can add more accounts in the dashboard.
  The `chatgpt` reference profile uses Codex.
  The `claude` reference profile uses Claude Code.
- The role routing: the recommended routing, or an absolute path to the
  intended routing table.
- The tunables: the recommended values, or values for `REVIEW_WIP_CAP`,
  `timeout_minutes`, `grace_minutes`, `heartbeat_interval_s`, and
  `spawn_grace_s`. Explain that `REVIEW_WIP_CAP` is the daily review-load limit
  of the operator.
- The Git identity: the non-personal default, or an explicit role author name
  and email.
- The dashboard bind: the loopback bind, or an explicit bind. The dashboard
  requires a bearer token on every bind. The operator can provide an owner-only
  token file. When the operator provides no file, onboarding mints a token into
  `config.toml` (`surfaces.dashboard.auth_token`). After activation, give the
  operator the browser URL in the form `http://<bind>/#token=<auth_token>`.
  The operator opens that URL once per browser. The browser keeps the token,
  and the plain address `http://<bind>/` works in that browser after that.
- Discord: explicitly disable Discord for this release. Enablement stays
  fail-closed until the resident connector and the posted test-message gate
  exist.
- The runtime credentials: a Culler sign-in or an application programming interface (API) key for each selected connection.
  The terminal accepts API keys through a concealed field.
  API keys stay outside command arguments and setup records.
  The host stores each credential in the macOS Keychain.
  A failed model test keeps the saved connection available for a retry.
- Confirmation that the operator is present for service activation.

For a development deployment, use the scratch home recorded in
`OPERATOR-INPUTS.md`. Do not use `~/.culler`. All other inputs are
deterministic. Do not ask about them.

### 2. Run the production front door

Run the front door from the repository root without interaction. Build the
argument list from the answers. Do not copy the placeholder brackets:

    ./install.sh --yes \
      --mc-home <home> \
      --runtime-bindings <csv> \
      --accept-default-routing \
      --accept-default-tunables \
      --accept-default-git-identity \
      --accept-default-dashboard-bind \
      --discord-mode disabled \
      --acquire-runtime-auth \
      --activate

The default installation leaves project configuration to the dashboard.
An explicit request for project setup through the agent can use the optional Project flags below.
The agent first shows the exact folder, repository creation, Git identity, and proposed access.
Culler preserves an existing repository and its history.
The operator must confirm project access before the agent supplies `--accept-generated-sandbox`.
The agent preserves the current vision unless the operator requests an edit.

Apply these substitutions:

- Replace `--accept-default-routing` with `--routing-file <absolute-path>` for
  an explicit remap.
- Replace `--accept-default-tunables` with `--review-wip-cap <n> --timeout-minutes <n> --grace-minutes <n> --heartbeat-interval-s <n> --spawn-grace-s <n>` when the operator adjusts those values.
- Replace `--accept-default-git-identity` with `--git-author-name <name> --git-author-email <email>` for a custom role identity.
- Replace `--accept-default-dashboard-bind` with `--dashboard-bind <host:port>`
  for an explicit bind.
- Add `--dashboard-auth-token-file <absolute-path>` to supply the bearer token.
  Without the flag, onboarding mints a token into `config.toml`.
- Add `--project-git-author-name <name> --project-git-author-email <email>`
  when the first Project has its own identity. Omit both flags to inherit
  the deployment identity.
- Omit `--project-vision` when the workspace root holds `VISION.md`.
  Onboarding reads the flag only when the file is absent.
- Add `--confirm-live-spend` to run live acceptance for every enabled agent profile after final verification.
  Each acceptance sends a maximum of two provider requests.
  Without the flag, an interactive run asks for spend confirmation for each profile.
  A `--yes` run skips acceptance without the flag.

The production front door accepts these flags:

- `--mc-home <path>`: the deployment home.
- `--bin-dir <path>`: the install destination of `mc`.
- `--project <id>`, `--workspace-root <path>`: the
  first Project.
- `--project-git-author-name <name>`, `--project-git-author-email <email>`:
  the Project commit identity.
- `--accept-generated-sandbox`: the reviewed sandbox profile.
- `--project-vision <create|later>`: the vision file choice.
- `--runtime-bindings <csv>`: the agent profile identifiers to enable, separated by commas.
  Each identifier names a profile in `MC_HOME/config.toml`.
  An absent configuration uses the `chatgpt` and `claude` reference profiles.
- `--acquire-runtime-auth`: a Culler sign-in for each subscription binding.
  A `--yes` run with a subscription binding requires this flag.
- `--confirm-live-spend`: the live acceptance runs for every enabled binding.
- `--accept-default-routing`, `--routing-file <path>`: the role routing.
- `--accept-default-tunables`, `--review-wip-cap <n>`, `--timeout-minutes <n>`,
  `--grace-minutes <n>`, `--heartbeat-interval-s <n>`, `--spawn-grace-s <n>`,
  `--hard-deadline-minutes <n>`, `--homie-idle-timeout-s <n>`: the tunables.
- `--accept-default-git-identity`, `--git-author-name <name>`,
  `--git-author-email <email>`: the role commit identity.
- `--accept-default-dashboard-bind`, `--dashboard-bind <host:port>`,
  `--dashboard-auth-token-file <path>`: the dashboard listener.
- `--discord-mode <disabled|enabled>`: the Discord surface.
- `--restore-latest`: the restore of the newest snapshot when the spine is absent. The snapshot must use the current schema.
- `--activate`: the operator-present supervision activation.
- `--reconfigure`: the stop and the re-apply of a changed `config.toml`.
- `--smoke`, `--smoke-approve-task <id>`: the full-pipeline ride-through.
- `--yes`: the non-interactive mode.

Culler creates isolated folders for each sign-in.
The macOS Keychain holds each API key and account credential.
Each agent container receives a scoped Culler token and the public proxy certificate.

Agents send model requests through the host provider proxy.
The proxy adds provider credentials on the host and sends requests only to the configured provider destinations.
The host renews account access and saves each replacement credential in the macOS Keychain.

For the development tier, use the same front door with `--dev --mc-home
<scratch-home>`. Omit runtime-auth acquisition and `--activate`. These are real
host effects, not development probes. Omit `--smoke`. The smoke is
production-only.

### 3. Read the result

The front door prints progress and JSON receipts for the sectioned wizard. In
the final receipt, `done` means the state changed. `ok` means the exact state
was already healthy. Do not report production complete while a required
section is `deferred` or failed. Give the operator a plain-language summary of
the result.

Setup completion depends on service readiness and the dashboard handoff.
The agent directs the operator to **Add Project** when the deployment has no project.
The dashboard derives project guidance from the current configuration.

### Optional acceptance

These checks require an explicit request after setup.
Their result remains separate from setup completion.

**Runner toolchain acceptance.** A complete runtime deployment supplies both the current runner release and the rebuilt runner image.
The image build requires stopped supervision.
The host verifies the rebuilt image with this check:

    ./runner/image/toolchain-smoke.sh <image>

The check builds `mc` from a copy of the checkout as agent user 10002.
The build runs through `mise exec` with `GOTOOLCHAIN=local` and `GOFLAGS=-mod=readonly`.
The build downloads the Go modules over the network.
The check compares the Go version of the image with the `toolchain` line of `mc/go.mod`.
The check confirms that `go.mod` and `go.sum` stay unchanged.
Live Scribe acceptance requires both supported harness families.
A registered Project without a wiki must receive its first wiki update without another Project setup.
A stalled task retains its source commit and its stage until an explicit resume through the task controls.

**Pipeline smoke.** The `--smoke` flag requests this separate check.
The smoke waits for a real Arbiter, Worker, Verifier, and Packager.
Then the smoke stops at the real review packet. Present that packet to the operator.
After the operator explicitly approves that exact task, run the front door
again with the same inputs plus `--smoke-approve-task <task-id>`. Do not infer
approval. Do not substitute a different task id. Do not describe a stopped
smoke as complete. The resumed run:

- merges the submitted commit into the destination branch;
- checks the durable role records, which must hold no Scribe run for the smoke task;
- waits for the first wiki update of the smoke Project and requires `openwiki/index.md` on `main`;
- archives the smoke Project;
- emits the final smoke receipt.

The run that stops at the review packet prints the approval prompt. The prompt
names the steps that the approval starts and the time limit of each wait. The
wait for the merge of the smoke task has a bound of ten minutes. The smoke
prints no text during a wait.

The two waits of the resumed run can last 20 minutes together. Use a command
time limit that is longer. Do not stop the command before its result. Do not
start a second smoke while one smoke runs.

The Scribe has no task. A wiki update is one Scribe run on the `main` branch
of a Project, together with the merge of its wiki commit into `main`. That
merge needs no operator approval. The smoke proves that one task reaches
review with no Scribe run in the task. The smoke also proves that one wiki
update merges a wiki commit.

The wait for the first wiki update has a bound of ten minutes. When the wait
reaches that bound, run the front door again with the same inputs. A message
of the smoke names this action with the words "resume the smoke check".

A failed wiki update stops the resumed run, and the message states the cause.
The next wiki update of the smoke Project becomes due after the wiki update
interval, 60 minutes by default. For the selected Project, the Scribe role
detail on the Pipeline page of the dashboard shows the result of the last wiki
update. After a wiki update merges its wiki commit, run the front door again
with the same inputs.

A message can ask for the trace of a Scribe run of the smoke Project. The
recent runs on the Pipeline page of the dashboard show a wiki update of the
selected Project as a Scribe run with no task. The row has the trace link of
the run. The trace link opens the trace in AgentsView. The folder
`MC_HOME/sessions/<run_id>` holds the trace files of a run.

A message can state that the smoke cannot read the main branch of the smoke
repository. The message names the Git command that failed. Run the front door
again with the same inputs. The resumed run reads the smoke repository again.
While the failure stays, the resumed run stops again. Its message can hold the
error text of Git.

When the resumed run prints the same message, run the Git command of the
message in the smoke repository. The file `MC_HOME/smoke/current.json` names
the smoke repository in `workspace_root`. The output of Git holds the error
text.

A message can state that Culler holds no wiki state of the smoke Project. Use
the section "Repair a failure" to find the cause.

The file `MC_HOME/smoke/current.json` holds the smoke state, and the smoke
state has a version. A smoke state with another version counts as no smoke
state. The front door then starts a new smoke Project with a new smoke task.
Do not use the approval of an earlier smoke task for the new smoke task.

**Live-provider acceptance.** The acceptance applies to each enabled agent profile.
While the operator is present,
run the live-provider acceptance for one enabled binding at a time. Confirm the
one-turn provider spend for that binding. Then run:

    MC_HOME=<home> <home>/bin/mc __acceptance provider run \
      --binding <binding> --confirm-live-spend

The command sends a maximum of two provider requests for the binding: one
for the tool call and one for the final reply. The command requires:

- one incremental stream;
- exactly one successful shell-tool artifact;
- an exact final reply.

The whole wizard runs acceptance for every enabled agent profile when the front door carries `--confirm-live-spend`.
The acceptance uses the host provider proxy after final verification.
When one acceptance fails, the wizard stops at the `acceptance` section and
names the binding. The binding leaves the enabled list at once. Each role that the routing table maps to the binding moves to
the first enabled binding; the receipt names the moved roles. Reconnect the
account or correct the profile with the operator, then run the front door again with
the same inputs. A binding that passed replays its receipt and spends
nothing.

Each acceptance turn uses a scoped Culler token for the selected connection and model.
The host proxy records the provider request count for that attempt.
Provider credentials stay on the host throughout the turn.

A separate request can check another enabled binding.
A repeated successful command replays its release-bound receipt without another provider request.

After a lost response or a failure, run
`mc __acceptance provider status --binding <binding>`. A `running` result is
ambiguous by design. The status command does not retry. Run
`cleanup --binding <binding> --attempt <attempt-id>` with the exact
`attempt_id` that status returned. Before the cleanup, explain that a
subsequent `run` is a new explicit provider spend. Run the cleanup only after
fresh operator confirmation.

**S7 sleep acceptance.** The operator must be present. First confirm in the
Docker Desktop settings that Resource Saver is disabled. Record only that
non-secret fact in `OPERATOR-INPUTS.md`. While a real subject-carrying pipeline
lease is active, run:

    MC_HOME=<home> <home>/bin/mc __acceptance s7 prepare --resource-saver-disabled

After the prepared receipt, ask the operator to sleep the Mac for a minimum of
30 minutes. Do not interrupt Culler. Do not unload a Culler LaunchAgent.
Immediately after wake, run `mc __acceptance s7 verify`. The command waits for
three production ticks. The command proves:

- the macOS Sleep/Wake interval;
- the wake freshness;
- the cadence;
- the VM/host clock agreement;
- the same resident/lease identity.

Then run `mc __acceptance s7 cleanup`. The command retains the closed verified
receipt and leaves supervision loaded. Do not create or mutate a lease for the
drill. When prepare reports no live lease, wait for a real subject-carrying
run.

### 4. Repair a failure

On a failure, run `mc doctor` with the same `MC_HOME` and `MC_SPINE`
environment that install.sh used. Each failed check names the onboarding
section that repairs it. Correct the named input with the operator when a
correction is necessary. Then run only that section again:

    MC_HOME=<home> MC_SPINE=<home>/spine.db <home>/bin/mc onboard <section> [flags]

A repeated run is safe. Onboarding is idempotent and sectioned. A healthy
section skips itself.

### 5. Rules

- Do not load launchd during development.
- In production, pass `--activate` only while the operator is present.
- Do not repeat activation to inspect a deployment that is already healthy.
- Do not commit, log, or copy values from `OPERATOR-INPUTS.md` or credential
  source files into tracked files.

## Failure vocabulary

- An AgentsView digest mismatch stops setup.
  The message names AgentsView and the expected and actual digests.
  Run `install.sh` again to download a new copy.
- An incompatible database stops the installer.
  The installer supplies no database reset authorization.
  The installer database update and migration policy remains unspecified.
- An incompatible or absent database stops `./redeploy.sh`, and the database stays unchanged.
  When the database check can run, the stop occurs before any change, and running services continue to run.
  `./redeploy.sh --reset-database` replaces the database after the operator confirms at a terminal.
  The reset verifies the deployment identity and stops every database user before the replacement.
- A refused `MC_HOME` inside a git working tree: select a path outside every
  repository, or a path that git ignores in every containing working tree.
