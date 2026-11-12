---
type: architecture
title: Runner containers and harness adapters
description: How the runner image is built, how the agent runner selects a harness adapter and reports back through the setuid mc, and how the Collie runner, fake harness, and shared runner modules fit in.
tags: [runner, docker, adapters, harness, image]
sources:
  - id: openwiki-source-2f0a77787a810afb1a87cbbb
    resource: repo://Dockerfile.culler-project
  - id: openwiki-source-096a781fb160ef979fa31121
    resource: repo://INSTALL.md
  - id: openwiki-source-8887a56335011659b61b947b
    resource: repo://mc/cmd/mc/candidate-runtime-check.sh
  - id: openwiki-source-6a84afb40e064a711b4d99bd
    resource: repo://mc/deployment/runner_release.go
  - id: openwiki-source-e028a0037f071b894557c29d
    resource: repo://resident/src/effects.ts
  - id: openwiki-source-7092e09d830ef9ed23536948
    resource: repo://resident/src/environment-lane.ts
  - id: openwiki-source-1f5e3c410b2b7d805b40d03a
    resource: repo://resident/src/provider-proxy.ts
  - id: openwiki-source-db41c5a3435f9629d5738809
    resource: repo://runner/agent-runner/adapters/claude.ts
  - id: openwiki-source-1a9cc0a52db0fc38615324bc
    resource: repo://runner/agent-runner/adapters/codex.ts
  - id: openwiki-source-1e8e75808ab8bc689f8e57e8
    resource: repo://runner/agent-runner/adapters/opencode.ts
  - id: openwiki-source-ecd0ea64f1d6275775d38f43
    resource: repo://runner/agent-runner/main.ts
  - id: openwiki-source-a8fe83c6ef27b7992b328a1d
    resource: repo://runner/agent-runner/runner-process.test.ts
  - id: openwiki-source-20c7a26c8321ed0c7a2cf49b
    resource: repo://runner/agent-runner/skills.ts
  - id: openwiki-source-d1b22419782a460caf6dbe3c
    resource: repo://runner/collie-runner/main.ts
  - id: openwiki-source-81d4bb4f0203db5a26c2bc57
    resource: repo://runner/fake-harness/README.md
  - id: openwiki-source-4f766d7b168fe76a82f503a8
    resource: repo://runner/image/build-prod.sh
  - id: openwiki-source-729fed85a7f093179cea3802
    resource: repo://runner/image/project-smoke.sh
  - id: openwiki-source-471e78eafbc0a8c07a4a7149
    resource: repo://runner/image/runtime.Dockerfile
  - id: openwiki-source-95ee54782931a1ab50a49d93
    resource: repo://runner/image/toolchain-smoke.sh
  - id: openwiki-source-a1d3951819531015b34e3c43
    resource: repo://runner/shared/setup-error.ts
generated: { by: "culler-scribe", at: "2026-11-12T05:20:43.904Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-12T09:22:03.309Z
---

# Runner containers and harness adapters

Every agent run executes in a fresh Docker container started by the resident. The container image comes from `runner/image/`. The TypeScript code that runs inside it comes from `runner/agent-runner/` (pipeline roles), `runner/collie-runner/` (the conversational Collie tier), `runner/fake-harness/` (a deterministic test harness), and `runner/shared/` (logging and small shared helpers). The runner never opens the spine. It reports everything by spawning `mc`, which inside the container is a setuid gate that fences the agent-facing verbs (see [mc CLI and verb scope](mc-cli-and-verb-scope.md)).

## The image: `runner/image/`

`runner/image/Dockerfile` builds the default Project image from fixed Ubuntu 24.04 inputs.
The Project image supplies development tools and records its tool inventory.
`runtime.Dockerfile` builds the separate Culler service image and private runtime at `/opt/culler-runtime`.

The runtime includes private Node.js and Bun executables, the runner source, harness packages, OpenWiki, and protected commands.
The installer copies those files into a deployment-specific Docker volume.
Agent containers mount that volume with read and execute access. Project commands use the Project image's tools.
A Culler update compares runtime manifests and writes only files that differ into the runtime volume.
It clears the release marker until the volume matches the product image.
The update preserves each Project's selected image digest.
See [image delivery and update cleanup](../operations/image-delivery.md) for the volume and image checks.

The runtime's `commands/mc` wrapper executes `libexec/mc-real` under `/opt/culler-runtime`.
The real binary belongs to uid and gid 10001 with mode `6755`.
Agents use uid 10002. Privileged commands read the fixed identity at `/mc/run.json`.
Agent containers drop all capabilities and use the default Docker seccomp profile.
The runtime also supplies `mc-land` for isolated repository operations.

`mc/deployment/runner_release.go` lists the shipped source files.
It includes the agent, Collie, and setup runners, adapters, OpenWiki files, and shared modules.
The production list excludes the fake harness.
`resident/src/effects.ts` uses the runtime volume when configured; source mounts remain a fallback for test configurations.

### Build scripts

- `runner/image/build.sh` cross-compiles a linux/arm64 `mc` with `CGO_ENABLED=0`, plus the dispatcher, and builds a test image. By default it builds `mc-fake-e2e` with the `test_fake_routing` tag. `--real` builds the untagged `mc-real-e2e`.
- `runner/image/build-prod.sh` builds `mc-prod`. It requires a 40 to 64 character hex `MC_RELEASE_BUILD_ID` (default `HEAD`) and stamps it into `mc` with `-ldflags`. It builds `mc-project`, then the separate product image with runtime dependencies. It runs browser checks for both the Project browser and the private runtime browser.
- `build-prod.sh` **refuses with exit 3 while any `com.culler.*` launchd unit is loaded**. It prints the `launchctl bootout` commands and builds nothing. A loaded deployment recreates its helper from the shared `mc-prod` tag. A rebuild would re-stamp that image, and every later tick would fail closed with a release identity mismatch. The refusal has no override. `build-prod.test.sh` stubs `launchctl` and checks that the refusal happens before any compile.

### Smokes

These scripts run against a built image and need Docker, so they sit outside the fast suite:

- `browser-smoke.sh` runs Playwright as uid 10002 with no network and requires a snapshot and a screenshot.
- `product-smoke.sh` checks the service image and capability restrictions. The release workflow in `.github/workflows/release.yml` runs it.
- `toolchain-smoke.sh` copies `mc/` and `.mise.toml` from the checkout into a container of the image. It builds `mc` there as uid 10002 through `mise exec --` with `GOTOOLCHAIN=local` and `GOFLAGS=-mod=readonly`. The build downloads the Go modules over the network. The script requires the image Go version to equal the `mc/go.mod` toolchain version, and it checks that `go.mod` and `go.sum` stay unchanged.

`project-smoke.sh` also executes the compatibility check that a published update uses before it accepts a Project image.
It mounts the candidate runtime volume read-only and runs as user 10002 with no network or additional privileges.
The container has a read-only root, a writable temporary directory, and explicit resource limits.
`mc/cmd/mc/candidate-runtime-check.sh` requires real `/mc` and `/workspace` directories and an absent `/mc/run.json`.
It executes the runtime check and requires the expected release identifier.
This checks the image and runtime combination before a later agent launch.
See [candidate compatibility](../operations/image-delivery.md#candidate-runtime-compatibility) for update behavior.

`runner/image/check.sh` runs only Docker-free script tests (`*.test.sh`), `release/check.sh`, `runner/openwiki/check.sh`, and `mc-land.test.ts`.

## The agent runner: `runner/agent-runner/main.ts`

`main` reads `/mc/run.json` (the launch envelope, `RunEnvelope`) and calls `executeRun`. The steps run in this order:

1. **Adapter selection.** `adapterInvocation` picks exactly one adapter. The route key `fake/fake`, or a key listed in `MC_AGENT_RUNNER_ROUTES`, runs the fake harness with `harness_config.behavior`. Any other route needs a complete `runtime` capability whose `harness` and `id` match the envelope. The runner then chooses by **harness family**: `codex` runs `adapters/codex.ts`, `claude-sdk` runs `adapters/claude.ts`, and `opencode` runs `adapters/opencode.ts`. Any other family throws "this image ships no … adapter". The runner holds no provider names. The model and channel come from configuration through the envelope. The runner rejects a custom base URL or a channel that mismatches the harness. Scribe runs get `--openwiki` and the input commit.

The runtime capability includes the profile's optional effort.
The runner passes a selected effort to the adapter as `--effort`.
The adapters check the harness level before they start a model call.
The Codex adapter accepts `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, and `ultra`.
The Claude SDK adapter accepts `low`, `medium`, `high`, `xhigh`, and `max`.
Without a selected effort, the harness uses its default.
2. **Repository preamble.** `runStorePreamble` runs only when the envelope carries `repository`. It requires a canonical commit id, a Git work tree, and a resolvable commit. For a Scribe run, the preamble only verifies that the tree equals the input commit. For other roles, it runs `git reset --hard <commit>` and `git clean -fdx`. A refusal is logged as `runner.store_preamble_refused` and reported as an infrastructure failure, and the runner exits 2.
3. **Skills.** `installEnvelopeSkills` in `skills.ts` replaces `$HOME/.agents/skills/` with the assigned skills. A temporary mount gives each container separate `.agents` storage. Replacement preserves concurrent roles and persistent dependencies. Skill names must match `^[a-z0-9][a-z0-9-]*$`. The installer logs malformed entries and returns false when the complete installation or a required skill is unavailable. Collie requires the common `culler` skill before it starts. Declared reference files are validated and staged with the main skill file. The Claude adapter mirrors the set into its own config directory, because the SDK reads skills from `CLAUDE_CONFIG_DIR`.
4. **Process start and brief.** The runner spawns the adapter and calls `mc run process-started`. It writes the brief to the adapter's stdin only after that call succeeds. If the call fails, the runner kills the adapter before it receives the brief.
5. **Event stream.** The adapter writes JSON event lines to stdout. On the first `session-start`, the runner calls `mc run register-session --native-ref … --file …` with a validated relative locator. It sends an immediate heartbeat. Runs with environment preparation already have a heartbeat timer; other runs start that timer after session registration. The harness never heartbeats. A `model-response` event triggers `mc run model-response` once per attempt, through `modelResponseReporter` in `runner/shared/model-response.ts`. An `infrastructure-failure` event is kept as the diagnosis.
6. **Exit.** After the adapter exits, the runner waits for any pending registration and, unless it was stopped by a signal, calls `mc run infra-failure`. It does this even for exit 0. The `mc` side compares the report with the recorded run outcome and keeps an accepted result. Role terminals (`mc complete`, `mc arbiter decide`, `mc verifier verdict`) are called by the agent itself through the same setuid `mc`.

A failing `mc` call is logged as `runner.mc_failed` and never kills the run. Missed heartbeats and missed reports leave the resident watchdog as the recovery path (see [Failure controls](../concepts/failure-controls.md)).

### Adapters and diagnostics

`adapters/claude.ts` drives the Claude Agent SDK `query`. Its `NativeSessionStore` (`adapters/session-store.ts`) writes the native transcript into the session directory. `adapters/codex.ts` spawns the vendored Codex binary. It removes provider credential variables from the environment. Its native configuration uses the host proxy and a temporary Culler token.

The adapters emit `session-start`, `model-response`, `turn-text`, `turn-complete`, and `infrastructure-failure` events. The Codex and Claude adapters log through `runLogSink` in `runner/shared/log-sink.ts`. It writes `runner.ndjson` in the directory that `MC_RUN_LOG_DIR` names, with `/mc/logs` as the default, or to stderr when that directory is absent. `adapters/openwiki.ts` configures the OpenWiki MCP server for Scribe runs and waits up to `OPENWIKI_STARTUP_MS` (30 s) for it to start. The host proxy caps provider requests during a model test.

`diagnostics.ts` builds `AdapterFailure` records. It redacts the values of environment variables whose names contain `TOKEN`, `SECRET`, `PASSWORD`, `CREDENTIAL`, `API_KEY`, or `AUTH`. It also redacts bearer tokens and truncates long text. It extracts HTTP status, provider error code, and a network error class. `runner/shared/network-error.ts` maps error codes to `connect`, `reset`, `timeout`, `tls`, or `other`. The runner passes these as `--http-status`, `--provider-error-code`, and `--network-error`.

### OpenCode runtime and traces

`runner/agent-runner/adapters/opencode.ts` uses the selected catalog model, including a validated live model record, and its supported effort variants.
The resident mounts the separate OpenCode volume read-only at `/opt/culler-opencode` through `openCodeArguments` in `resident/src/environment-lane.ts`.
The installation supplies the executable, provider packages, control plugin, and shell wrapper.
Source deployments prepare and verify this package before service activation, even without an OpenCode connection.
Published installations download the package when the first OpenCode connection requires it.
`mc/deployment/opencode_source.go` owns source preparation and verified reuse in the selected Linux arm64 image.
See [image delivery](../operations/image-delivery.md#source-package-preparation-and-reuse) for package identity and repair.
See [agent connections](../operations/agent-connections.md) for catalog ownership and connection readiness.

The adapter supplies the proxy address and temporary token through its generated configuration.
It disables project configuration, automatic updates, model discovery, sharing, and snapshots.
Its policy permits the selected provider and controls web search through the assigned capability.
The native process uses separate data, configuration, cache, and state paths within the run's session directory.

A prompt hook exposes the native session identity before the first provider response.
The adapter registers that identity with the native database locator.
The resident publishes masked trace copies and preserves the original session database for resume.
These databases belong to OpenCode; Culler state remains in the spine behind `mc`.
The adapter requires a session identity, reply text, and a completed step before it reports success.
A process failure prevents success unless the adapter stopped an expired background wait.
`runner/agent-runner/opencode.test.ts` checks configuration isolation, trace registration, redaction, and terminal results.

### Background commands and shell variables

The Claude SDK and OpenCode harnesses keep a turn open while background commands run.
Their adapters allow ten minutes without a new event during that wait.
A later model response continues the turn, and the last reply becomes the result.
When the wait expires, the adapter stops the harness and retains the completed reply.
An expired wait cannot turn an incomplete OpenCode reply into success.
`adapters/startup.test.ts` and `opencode.test.ts` check continuation and the wait limit.

The Codex shell keeps the container environment, including variable names that contain `KEY`, `SECRET`, or `TOKEN`.
Its explicit shell exclusion removes `CULLER_PROXY_TOKEN`.
The adapter removes provider credentials before it starts the harness.
This preserves Project tool credentials in agent commands while keeping the temporary proxy token outside those commands.

### Provider access

All three adapters use the host HTTPS provider proxy.
Each launch receives a temporary Culler token and the public proxy certificate.
The token permits the assigned connection and model.
The host supplies and renews provider credentials outside the container.
The proxy accepts only the connection, model, operations, and lifetime that the host assigns to the run.
The adapters remove ambient provider credentials and provider destination overrides.
The host records the origin of each request failure.
During a model test, upstream HTTP 400, 401, and 422 responses release the request count and model request count.
A harness can therefore retry a rejected request after renewal or with another request form.
These responses remain failures in the diagnostic record; other upstream failures retain their request charge.
See [Agent connections and access renewal](../operations/agent-connections.md) for the host storage and failure behavior.

## Project preparation and setup

Setup and pipeline containers share the persistent Project home at `/home/agent` through `HOME`.
The resident mounts separate temporary `.agents`, `.claude`, and `.codex` directories within that home.
Credentials and native sessions use their separate run locations.
Collie retains its own home and session rules.

`runner/shared/project-environment.ts` prepares declared dependencies before Worker, Verifier, and Packager execution.
The Scribe omits automatic preparation and checks retained dependency compatibility before use.
Each run receives its assigned source independently of the retained home.
Package managers assess retained files against current declarations and the selected Project image.
The shared `/cache` volume stores disposable files under a two-gibibyte limit.
Cache eviction preserves the Project home.
The agent runner adds actual environment evidence to the brief and records tool and package observations.
A preparation failure remains context when the harness can still execute.
The setup runner uses separate environment operations and reports through their assigned identities.
See [Project environments](../workflows/project-environments.md) for image decisions, caches, and the acceptance boundary.

## The Collie runner: `runner/collie-runner/main.ts`

The Collie runner serves the lease-free conversational tier (see [Collie](../concepts/collie.md)). It reuses `adapterInvocation`, `installEnvelopeSkills`, and the diagnostics from the agent runner, but it never heartbeats and holds no lease. It reports `mc collie runner-started` once and exits if a newer launch fenced that report. It then loops: `mc collie claim` takes the oldest pending turn, the runner runs one harness turn, it posts provisional text through `mc collie draft` at most once per second, and it finishes with `mc collie reply --to <id>`. The resident stops the container on the idle timeout. The resident pins `HOME=/home/mc-model` for Collie containers, so skill installation has an absolute root.

`collieTurnPrompt` supplies variable facts: session and message identity, conversation type, release, repository, trace, scratch, packet, and Vision context.
The directive and common `culler` skill own authorization and procedures.
The skill reference tree includes diagnostics, traces, configuration, tasks, and packet decisions.
This keeps stable rules separate from the facts of a turn.
`runner/collie-runner/main.test.ts` checks prompt context and required skill delivery.

## The fake harness: `runner/fake-harness/`

`runner/fake-harness/README.md` is the contract for the `fake` harness family. `cli.ts` runs one turn from a behavior JSON file. The schema is validated fail-closed in `behavior.ts`. Steps are `exec`, `sleep`, and exactly one terminal step: `succeed`, `crash`, or `hang`. The harness appends each event to `<session-dir>/native.jsonl` before it mirrors the event to stdout. It uses no network, model, credentials, or Docker. It never heartbeats, so a `hang` step exercises the hard-deadline reap. `exec` steps call the scoped `mc` with the `MC_RUN_ID`, `MC_ROLE`, `MC_SUBJECT_ID`, and `MC_POOL_IDS` variables that `harnessEnv` exports. The e2e lane routes every role to this harness.

## Shared modules: `runner/shared/`

`log.ts` and `log-sink.ts` are the TypeScript structured logger (see [Structured logging and log parity](../concepts/logging.md)). `model-response.ts` reports at most one successful model response per attempt. `network-error.ts` classifies network failures for both the resident and the runners. The directory stays under `runner/` because the runner manifest cannot address `../`.

## Tests

Each component's `check.sh` runs `bun test` without Docker or tokens:

- `runner/agent-runner/runner-process.test.ts` runs `main()` as a subprocess against a temporary envelope and a stub `mc`. It covers route refusal, single `register-session`, heartbeats during the adapter wait, the brief gated on `process-started`, preamble refusals, and the exit-0 infra report.
- `runner.test.ts`, `skills.test.ts`, `diagnostics.test.ts`, and `adapters/*.test.ts` cover the pure functions and the adapters.
- `runner/collie-runner/main.test.ts` covers the Collie loop.
- `runner/fake-harness/behavior.test.ts` and `cli.test.ts` cover the fake harness.
- `runner/shared/*.test.ts` covers the shared modules.

See [Checks and test lanes](../tests/checks-and-test-lanes.md) for the Docker e2e lanes that run real containers.

## Project container policy

Setup and pipeline agents share the container policy in `resident/src/environment-lane.ts`.
The policy includes Docker's init process to reap child processes, the agent user, capability removal, and configured resource limits.
The launch envelope and credentials still define each role's separate authority.
`resident/src/effects.ts` applies that policy to pipeline containers.

`runner/shared/setup-error.ts` preserves a structured adapter diagnosis when one exists.
Otherwise it extracts a Docker daemon or container-wait error before it falls back to the process exit code.
It redacts diagnostic text and bounds the message and details separately.
The setup-error tests check these fallback paths.

This repository's `Dockerfile.culler-project` creates `/private/tmp` and supplies `/usr/bin/git` as a link to the installed Git executable.
These files support repository tests inside the selected Project image.
The file preserves the pinned Project base.
