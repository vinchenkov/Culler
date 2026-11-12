---
type: architecture
title: The resident host daemon
description: The Bun process on the host that drives dispatch, starts containers, runs repository, trace, and wiki lanes, and supplies provider access through a host proxy. It reaches the spine through mc.
tags: [resident, daemon, dispatch, credentials, lanes]
sources:
  - id: openwiki-source-81e49db5f10e4699bcadf8d3
    resource: repo://mc/verbs/collierestart_test.go
  - id: openwiki-source-1655883a2300ff18955c5910
    resource: repo://resident/src/collie-runtime.ts
  - id: openwiki-source-6dc928cb15677dde622f6a7e
    resource: repo://resident/src/discord-config.ts
  - id: openwiki-source-e028a0037f071b894557c29d
    resource: repo://resident/src/effects.ts
  - id: openwiki-source-7092e09d830ef9ed23536948
    resource: repo://resident/src/environment-lane.ts
  - id: openwiki-source-76e2df8149603feba8077d5b
    resource: repo://resident/src/install-maintenance.ts
  - id: openwiki-source-b65f5016ff00568fc3622948
    resource: repo://resident/src/main.ts
  - id: openwiki-source-e3aeeb02910b669a4b8e3bd5
    resource: repo://resident/src/mc-verb-surface.test.ts
  - id: openwiki-source-169bda06f2ca62ff273679a9
    resource: repo://resident/src/nudge-socket.ts
  - id: openwiki-source-fca867d7fea1f4f6917fe44b
    resource: repo://resident/src/provider-credentials.ts
  - id: openwiki-source-c7f396a36b94cc764d4a5cdb
    resource: repo://resident/src/resident-control.ts
  - id: openwiki-source-99a07edc39aa8ad56f56f80c
    resource: repo://resident/src/run-log-prune.ts
  - id: openwiki-source-79628d0b1a3667fb37eebff9
    resource: repo://resident/src/tick-loop.ts
generated: { by: "culler-scribe", at: "2026-11-12T00:11:01.469Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-12T00:11:01.469Z
---

# The resident host daemon

The resident is a Bun/TypeScript process (`resident/src/main.ts`) that launchd supervises on the operator's Mac. It has no state of its own. It turns decisions that `mc` commits into host actions: Docker containers, run folders, trace copies, wiki exports, and short-lived credentials. The deployed supervision configuration starts it with the pinned host Bun.

## The invariant: never open the spine

The resident never opens the SQLite spine. Every read and write runs the host `mc` binary and parses one JSON object from stdout (`AGENTS.md`, "Hard rules"). `execMcVia` in `resident/src/resident-control.ts` is the single exec seam that `main.ts` passes to every component as `runMc`. Its `mcEnvironment` sets `MC_HOME` and deletes `MC_SPINE` and `MC_RUN_JSON`. Ambient developer variables therefore cannot turn a resident call into a direct-spine call or an agent-scoped call. The `main.ts` header notes that the host `mc` delegates through `MC_HELPER` to the helper container that can reach the spine volume.

This matters because `mc` is the sole spine writer. Its transactions, triggers, and CHECK constraints hold every invariant. A TypeScript writer would bypass them, and TypeScript cannot reach the Docker volume anyway. Only tests import `bun:sqlite` (`split-brain.test.ts`, `dispatch-control-crossing.test.ts`), and they use it only to age a committed lease or heartbeat. `resident/src/mc-verb-surface.test.ts` extracts every literal `runMc([...])` verb from the resident source, builds the real binary, and fails if the binary does not know a verb. Every resident-side unit test mocks the exec seam, so only this test catches a renamed verb.

### The dispatch control channel

`mc dispatch` is the only verb that gets more than stdio. `execControlledDispatch` creates a kernel socketpair and passes it to the child as fd 3. Within `HELLO_TIMEOUT_MS` (2 seconds) it exchanges `hello`/`hello_ack` frames. The frames carry the release build ID, `GATEWAY_CONTROL_VERSION`, the deployment UUID, and `SPINE_SCHEMA_ID`, a SHA-256 of `mc/substrate/schema.sql`, `delivery.sql` and `environments.sql`. The channel then answers Collie inventory and pipeline liveness requests. The resident collects pipeline liveness (`collectPipelineLiveness` in `pipeline-liveness.ts`, from `mc lease list` plus `docker inspect`) before the child starts, outside the 2-second budget. A timeout fails that tick only, and the next tick retries. Every other verb gets `/dev/null` on fd 3.

## Startup

Before normal startup, `waitForUpdateActivation` checks the saved update transaction and maintenance request.
Only the expected candidate or restored release can proceed through the applicable update phase.
During release verification, it publishes receipts while the update keeps normal work paused.
`resident/src/install-maintenance.ts` owns this check and validates the transaction's identity, ownership, mode, and phase.

`main()` validates its inputs and exits with code 2 on any refusal. The checks cover config schema and release identity, the Git identity (`git-identity.ts`), the runner route allowlist, `dockerPath`, workspace roots, the AgentsView bind, and the tick interval (`MC_TICK_INTERVAL_MS` or `tickIntervalMs`, default 60 seconds). It opens `MC_HOME/logs/resident.ndjson`, then runs these steps in order:

1. **Attachment confinement.** `proveAttachmentConfinement` runs `mc __attachment-canary`. `AttachmentConfinementHealth.markHealthy` (`attachment-health.ts`) accepts only a closed receipt with exact UIDs and denial results, once per boot. `startAttachmentHealthGuard` (`attachment-health-guard.ts`) keeps `mc __attachment-health-guard` alive for the boot.
2. **Startup backup.** `createBackupChore` runs `mc backup` and validates the receipt. It repeats before a tick once `backupIntervalMs` (default one hour) has passed.
3. **Attachment terminal sweep.** `createAttachmentTerminalChore` (`attachment-ingress.ts`) expires due attachment attempts through `mc`.
4. **Credentials.** `readAgentProfileRuntimeCatalog` (`agent-profile-runtime.ts`) reads `mc agent-profile runtime-catalog`. The host credential interface reads protected Keychain records. The resident starts the HTTPS provider proxy. Containers receive temporary Culler access tokens.
5. **Provider acceptance socket** at `MC_HOME/health/provider-acceptance.sock`.
6. **Lanes and tick.** The environment lane, repository lane, trace lane, wiki lane, tick loop, nudge socket, and Discord configuration service start last.

Two timers run beside these steps. Every 60 seconds the resident runs `mc evaluation recover`. Every 10 seconds it runs `mc system probe` and writes `health/resident-availability.json`.

## Tick loop and effects

At the end of startup, `main()` calls `handle.nudge()` after it starts the independent lanes.
This requests the first round immediately, without a wait for the 60-second default interval.
The round uses the same preparation guard, maintenance check, and receipt path as later rounds.
Release activation waits for the tick receipt.


`startTickLoop` (`tick-loop.ts`) fires on an interval and runs one tick at a time. A timer firing during a tick is skipped. A nudge during a tick books exactly one more round. Each tick:

1. Asks the installer pause check (`maintenance`). If an install pause is active, the tick only writes its receipt.
The resident acknowledges the pause as ready after repository, environment, and operator operations finish.
The [lifecycle command](../operations/culler-lifecycle.md) uses that result before it stops the resident.
2. Runs `beforeTick` chores: the environment preparation guard, pending Vision saves (`vision-save.ts`), agent profile refresh, backup, account renewal selection from `mc system status`, attachment expiry, run log pruning, and artifact expiry (`mc __prune-artifacts`).
3. Runs `mc dispatch` and requires one `{action: "tick", actions: [...]}` object.
4. Calls `applyEffect` (`effects.ts`) for each action. A failed action leaves the others intact.
5. Writes `MC_HOME/health/resident-tick.json` through a rename (`publishTickReceipt`).

`applyEffect` handles `spawn`, `reap`, and the Collie actions `collie-wake`, `collie-adopt`, `collie-stop`, and `collie-remove`. It logs a `refused` effect verbatim and does nothing else, because `mc` already applied the consequence. An unknown action is logged and ignored. `spawn` refuses an effect that lacks a brief or a Git identity decision. It prepares the isolated repository, writes the launch envelope `MC_HOME/runs/<run_id>.json` (mounted read-only at `/mc/run.json`), and runs `docker create` and `docker start`. `TZ` comes from `hostZone()` (`host-zone.ts`): `TZ` in the environment, else the `/etc/localtime` link. `reap` stops `mc-setup-<run>` and `mc-<project>-<run>`.

The runtime envelope carries a configured effort when the profile selects one.
The setup lane uses the same value for setup agent runs.
For a Collie wake, the resident reads the conversation's stored effort and puts it in the launch envelope.
The current profile still supplies the connection and model access for that launch.

## Project storage at launch

The setup lane and pipeline effects share `projectHomeArguments` from `resident/src/environment-lane.ts`.
The helper binds the Project home at `/home/agent` and sets `HOME` to that path.
Separate temporary mounts cover `.agents`, `.claude`, and `.codex` inside that home.
This preserves dependencies while concurrent runs retain their own role files.

The setup lane requests the canonical home through the host-only Project home command.
Pipeline effects require the attested home in the mount plan when the runtime volume is configured.
`mc/verbs/mountattest.go` prepares the selected home and binds its filesystem identity into the plan.
The resident rejects a missing or malformed home before container creation.
Scribe runs use the same home while retaining a separate source repository.

The environment lane measures home bytes separately from disposable cache bytes.
Its cache-clear operation removes `/cache` contents and preserves the home.
See [Project environments](../workflows/project-environments.md#runtime-and-per-run-dependencies) for preparation and compatibility requirements.

## Lanes outside the tick

- **Environment lane** (`environment-lane.ts`). It executes setup, image build, source acceptance, and recovery operations outside the task pipeline. Its preparation guard runs before dispatch. See [Project environments](../workflows/project-environments.md).
- **Repository lane** (`repository-lane.ts`). It registers an owner number, sends `mc delivery start` at each start event, heartbeats every 20 seconds, and runs each repository execution. It exits the resident when `mc` reports a stale owner. See [Git delivery](../workflows/git-delivery.md).
- **Trace lane** (`trace-lane.ts`). It copies run, Collie, and setup agent traces into `MC_HOME/agentsview/traces/` on a 2-second copy timer and reconciles AgentsView sessions on separate timers.
- **Wiki lane** (`wiki-lane.ts`). Every 20 seconds it runs `mc __wiki-export` with one call in flight. See [OpenWiki and viewers](../integrations/openwiki-and-viewers.md).

The lanes keep slow host work off the dispatch path. A tick never waits for a merge, a trace copy, or a wiki export.

## Wake and control surfaces

- **Nudge socket** (`nudge-socket.ts`): an owner-only Unix socket at `MC_HOME/resident.socket`. It accepts one line of at most 64 bytes: a Collie session ID, `vision-save`, `project-start`, or `packet-decide`. `routeNudge` in `main.ts` sends `packet-decide` to the repository lane and wakes the tick for every other value. The socket refuses a stale path that is not an owned socket.
- **Install maintenance** (`install-maintenance.ts`). When `MC_HOME/state/install-maintenance.json` exists as an owner-only regular file, the resident writes an acknowledgement. The acknowledgement is `ready` only when no repository, environment, or operator operation is active. This lets `install.sh` wait for a quiet resident.
- **Discord** (`discord.ts`). An optional native surface that routes messages through `mc` and drains the outbox. Its header states that it never opens the spine and never interprets natural language.

## Credentials for runs

The host retains provider credentials in macOS Keychain.
Each container receives a temporary Culler token and a public certificate authority certificate.
The host proxy adds provider credentials only to outbound provider requests.
The host keeps account renewal data outside agent containers.
The provider proxy checks each container token against its assigned connection, model, operations, and lifetime.
The resident restarts Collie when its profile or authorization changes.

Before dispatch, the resident selects account `token_refresh` conditions in `unknown` or `retry_wait` state.
It starts renewal without a wait for the provider response, so dispatch can continue.
The connection check enforces the retry time and permits one active renewal check.
The credential service records a failed provider renewal with its original evidence.
The resident cancels that check without a second failure observation.

- `credential-projector.ts` authorizes each container launch for one connection and its permitted models.
- `provider-proxy.ts` validates requests, forwards response streams, and records safe diagnostic facts.
- `provider-credentials.ts` reads protected host records and serializes renewal for each connection.
- `connection-owner.ts` coordinates renewal across host processes.
- `provider-lifetime.ts` checks trusted control state and revokes obsolete launch authorization.
- `provider-acceptance.ts` supplies bounded authorization through an owner-only Unix socket.
- `provider-foreground.ts` supplies the same proxy before resident activation.
- `tests/live-model/` contains separate tests that require real provider responses.

The runtime catalog supports account sign-in and static API keys.
The proxy distinguishes provider failures from host failures.
A host credential-store failure preserves the recorded provider authorization.
See [Agent connections and access renewal](../operations/agent-connections.md) for validation and recovery boundaries.

## Run log pruning

`run-log-prune.ts` bounds `MC_HOME/logs/runs`. It keeps the newest `KEEP_RUN_LOGS` (500) ended runs and removes at most 8 directories per tick. It skips runs that have not ended and runs whose container is present. It never throws, so it cannot cost a tick its dispatch.

## Shutdown

On SIGINT or SIGTERM, `shutdown` stops Discord and the credential projector, then the nudge socket and the acceptance socket, then the tick loop. It then waits for the repository, trace, wiki, environment, and operator lanes together, stops the health guard, and exits. The host allows a sent renewal to save its rotated token before a bounded shutdown completes.

## Tests

`resident/check.sh` runs `bun test`. Unit tests use fake timer, exec, and fs dependencies and need no Docker.

- `tick-loop.test.ts`: one dispatch per firing, install pause behavior, and `afterTick` on failures.
- `resident-control.test.ts`: fd 3 ownership, the hello exchange, and recovery after a timeout.
- `main.test.ts`: `MC_HOME` canonicalization, attachment health per boot, and the refresh grant store boundary.
- `nudge-socket.test.ts`, `install-maintenance.test.ts`, `run-log-prune.test.ts`, `provider-credentials.test.ts`, `provider-proxy.test.ts`, `credential-projector.test.ts`, `discord.test.ts`, `host-zone.test.ts`, `provider-acceptance.test.ts`: one module each.
- `split-brain.test.ts`: builds the real test-tagged `mc` and drives the ordinary tick loop and repository lane through injected faults at spawn, Worker Git, approval and merge, and outbox boundaries.
- `dispatch-control-crossing.test.ts`: the dispatch control channel against the real binary.

Related: [system overview](system-overview.md), [mc CLI and verb scope](mc-cli-and-verb-scope.md), [runners](runners.md), [dispatch and tick loop](../workflows/dispatch-and-tick-loop.md), [Collie](../concepts/collie.md), [logging](../concepts/logging.md), [install and deployment](../operations/install-and-deployment.md).

## Discord service lifecycle

`startDiscordConfiguration` reads the current private policy through the host command at startup.
A failed connection records an attention warning and leaves the configuration socket available for recovery.
The service serializes requests and owns active connections, temporary setup drafts, and staged replacements in memory.
Its owner-only socket lives at `MC_HOME/discord.socket`.
A 30-second timer expires abandoned drafts and staged connections.

The generated supervision configuration contains no Discord policy or bot token.
The host command coordinates configuration writes, spine projection, and service activation without a resident restart.
A replacement first proves its access with an inactive preview connection.
The host then stops old delivery, projects the new routes, and activates the checked connection.
See [Discord setup and delivery](../integrations/discord.md) for account checks, expiry, rollback, and delivery retirement.

## Retained operator execution and native traces

`operator-lane.ts` polls due operator requests independently of dispatch.
It starts recovery before its first poll, permits one execution at a time, and applies a five-minute execution deadline.
Maintenance prevents new execution. An interrupted execution asks `mc` to retain an uncertain result for inspection.
The lane never opens the spine and supplies no arbitrary command interface to Collie.

The trace lane publishes masked native OpenCode database copies through `opencode-copy.ts` and `opencode-snapshot.ts`.
The original session remains available for resume. Collie reads a separate inspection mount.
Copy status and AgentsView import status remain distinct; a failed refresh preserves the last valid copy.
See [trace access](../integrations/openwiki-and-viewers.md) for evidence limits and retirement of converted exports.
