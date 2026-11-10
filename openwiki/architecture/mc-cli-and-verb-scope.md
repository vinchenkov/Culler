---
type: architecture
title: The mc CLI, verb scope, and boundaries
description: How the mc binary dispatches verbs, reports results and refusals, crosses into the helper container, derives caller scope from /mc/run.json, and which supporting packages define its policy.
tags: [architecture, mc, cli, scope, security, boundary]
sources:
  - id: openwiki-source-a7ca3be58a8971f19014beb8
    resource: repo://mc/cmd/mc/cli_test.go
  - id: openwiki-source-1bb4f1508c7edc9615caeee6
    resource: repo://mc/cmd/mc/discord.go
  - id: openwiki-source-7a1823f57484ffb5cb397037
    resource: repo://mc/cmd/mc/helper_manager.go
  - id: openwiki-source-d836d340a3e63f02aab7a979
    resource: repo://mc/cmd/mc/main.go
  - id: openwiki-source-933d0f6cd75c6be8beefdb55
    resource: repo://mc/cmd/mc/runtime_scope_prod.go
  - id: openwiki-source-42421fcd4e919150079ddaf4
    resource: repo://mc/cmd/mc/runtime_scope_testfake.go
  - id: openwiki-source-7ba97e8fd5c9270622015405
    resource: repo://mc/routing/registry_active_fake.go
  - id: openwiki-source-02743ab9dfa15349cb2629c2
    resource: repo://mc/routing/registry_active.go
  - id: openwiki-source-a44ab4c2d26db975bde190a0
    resource: repo://mc/verbs/collie.go
  - id: openwiki-source-1cf141b5d1589e627f8f9d6d
    resource: repo://mc/verbs/runjson_test.go
  - id: openwiki-source-3e36da904db22c61871990fa
    resource: repo://mc/verbs/task.go
  - id: openwiki-source-947e8b3d1474c80d6712c56f
    resource: repo://mc/verbs/verbs.go
  - id: openwiki-source-1af81418c71ef16693e3dabe
    resource: repo://runner/image/mc_dispatch.go
  - id: openwiki-source-471e78eafbc0a8c07a4a7149
    resource: repo://runner/image/runtime.Dockerfile
generated: { by: "culler-scribe", at: "2026-11-10T18:18:18.729Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-10T18:18:18.729Z
---

# The mc CLI, verb scope, and boundaries

`mc` is the Go command that owns dispatch, control, and the spine. Every other Culler component (the resident, the dashboard, the runners, and the setup front doors) acts by spawning `mc` and reading its output. This page covers the command's entry point, its output contract, how it reaches the spine from the host, how it decides what a caller may do, and the policy packages it relies on. For the topology around it, see [Culler system architecture](system-overview.md).

## Entry point and verb dispatch

The entry point is `mc/cmd/mc/main.go`. `main` calls `run`, which opens the CLI logger, calls `dispatchArgs`, records one `cli.invoked` log line with the exit code and duration, and closes the logger. `run` saves and restores the logger so that nested calls (the onboarding smoke re-enters `run`) do not stop the outer invocation's logging.

`dispatchArgs` routes the first argument in three stages:

1. **Special crossings.** Private verbs prefixed with `__` (for example `__dispatch-prepare`, `__onboard`, `__release`, `__attachment-*`, `__reset`, `__uninstall`) and a few public verbs (`onboard`, `doctor`, `backup`, `collie start`, `dispatch`) go to dedicated broker functions. A broker does host-side work and sends only a closed, path-free frame into the helper. The `__` names are the front doors' spelling for install-time mechanics. Agent-facing `onboard` accepts only the operator setup sections. `mc/cmd/mc/agent_surface_test.go` checks that removed release verbs return `unknown verb` and that `__` mechanics refuse container callers with `host scope only`.
2. **Host-local verbs.** Verbs that read host files under `MC_HOME` run in the host process even on macOS: `health`, `logs`, `config`, `routing`, `system`, `connection`, `agent-profile`, `evaluation`, and the `run`/`collie launch-check` identity rechecks. `health` must answer when Docker is down, so it cannot depend on the helper.
3. **Everything else.** On a host that delegates, `mc` ensures the helper is running and forwards the whole argv. Otherwise it calls `runLocal`.

`runLocal` calls `dispatchVerb`, a large `switch` over verb names that parses flags with the standard `flag` package, opens the spine through `withSpine` or `withSpineRead`, and calls a function in `mc/verbs`. The `mc/verbs` package comment states the contract: each verb returns one JSON-marshalable value, and rejections are typed. An unrecognized verb returns a usage error, `unknown verb "<name>"`.

## Output and exit codes

A successful verb writes exactly one JSON object and a newline to stdout and exits 0. All failures pass through `writeVerbError`:

- A `*verbs.UsageError` (built with `verbs.Usagef`) exits 2 with error code `usage`. This covers bad arguments and environment problems, such as a missing spine path.
- Any other error exits 1. A `*domain.DomainError` with a `Code` reports that slug, for example `not-found`, `role-mismatch`, `lease-held`, or `spine-fault`. An uncoded rejection reports `domain-rejection`.

In both cases stderr carries `mc: <message>`, and stdout carries an error envelope `{"error":{"code":…,"message":…}}`. The function also writes a `cli.verb_refused` warning to the structured log, because the resident discards stderr. `TestStructuredErrorJSON` in `mc/cmd/mc/cli_test.go` checks the three cases: exit 1 with `not-found`, exit 2 with `usage`, and exit 1 with `domain-rejection` for a scope refusal. See [Logging](../concepts/logging.md) for the log format.

Callers rely on this split. Exit 1 means the spine refused the request on its merits. Exit 2 means the request or its environment was wrong. A SQLite driver failure is exit 1 with code `spine-fault`, kept apart from rule refusals (see [The spine](spine-and-substrate.md)).

## Re-exec into the helper container

On macOS the spine lives in a Docker named volume that the host cannot open. The production build (`mc/cmd/mc/runtime_scope_prod.go`, built without `test_fake_routing`) sets:

- `shouldDelegateToHelper()` to `runtime.GOOS == "darwin"`;
- `helperSpinePath()` to the fixed `/mc/spine/spine.db`;
- `helperContainerName()` to the name from `deployment.RuntimeNames` of the canonical `MC_HOME`, which is `mc-helper-<12 hex>`.

`delegate` runs `docker exec -i <helper> mc <argv…>`. It passes stdin through and tees stdout and stderr: the caller gets every byte unchanged, and `mc` keeps a bounded copy (8 KiB, first bytes kept) so it can log the refusal code from the inner error envelope. A failure of the wrapper itself, where the inner `mc` never ran, exits 2 with `self-delegation failed`.

`ensureRuntimeHelper` (`mc/cmd/mc/helper_manager.go`) makes the helper match one exact shape before use. It runs the production image as `/bin/sleep infinity`, uid `10002:10002`, `--network none`, `--cap-drop ALL`, private IPC, 0.5 CPU, 512 MiB memory, 128 pids, the host time zone, and one mount: the spine volume at `/mc/spine`. `helperExact` inspects every one of these fields. A managed container that drifts is removed and recreated. A container with the helper's name but without the `mc-managed`/`mc-component=helper` labels is never removed; `ensure` refuses instead. `mc/cmd/mc/helper_manager_test.go` covers the create arguments, rejection of authority widening, and preservation of unmanaged name collisions.

`AGENTS.md` describes delegation in terms of `MC_HELPER`. In the code, that variable matters only in `test_fake_routing` builds. There `mc/cmd/mc/runtime_scope_testfake.go` delegates when `MC_HELPER` is set and `MC_SPINE` is not, and uses `MC_SPINE` as the direct spine path. The CLI tests and the development install use this build.

## Verb scope from /mc/run.json

`mc` decides who is calling from one fact: whether the launch envelope exists. `verbs.LoadIdentity` (`mc/verbs/verbs.go`) reads `RunJSONPath()`. If the file is absent it returns `nil`, which means host scope. If it is present, it parses a `RunIdentity` with `run_id`, `tier` (`pipeline` or `collie`), `role`, `pool_ids`, `verb_allowlist`, and the writable mounts. No flag or argument can set tier or role.

Scope checks build on that value:

- `RequireHostScope` refuses any caller with an identity: `<verb> is host scope only; run.json callers are refused`.
- `requirePipeline`, `requireRole`, and `requireExactRole` require tier `pipeline` and a matching role. A mismatch returns the coded `role-mismatch` refusal. `requireExactRole` checks the exact wire identifier in the launch envelope.
- `RequireOperatorVerb` admits the host, admits a Collie session only for verbs frozen into its `verb_allowlist`, and denies every pipeline run.

Scope is checked before the spine is opened. `TestScopeRefusalPrecedesSpineOpen` points a pipeline identity at a fresh spine path and checks that refused verbs leave no database file behind.

## Task reads and Collie authority

`mc/verbs/task.go` selects task fields for the caller through `TaskRead`.
Worker and Verifier reads omit `strategic_rationale`, including reads by roles with a case suffix.
Their descriptions hold the task requirements. Other permitted callers can read the rationale as proposal evidence.
`mc/cmd/mc/main.go` passes the run identity into this read.

Collie creates operator tasks through the `create` action of `board.write`.
`maximumCollieAgentVerbs` in `mc/verbs/collie.go` excludes `task.add`.
A Collie start request that includes that verb fails before a session starts.
Host `task add` remains an internal interface and creates work at `seeded`.
The operator uses the dashboard or Collie.
`mc/cmd/mc/cli_test.go` checks the refused Collie allowlist and host task creation.
See [Collie task descriptions](../concepts/collie.md) for operator confirmation and description rules.

## The setuid wrapper

The separate Culler runtime supplies the protected commands to agent containers.
`runner/image/runtime.Dockerfile` installs the command wrapper and the real binary under `/opt/culler-runtime`.
The wrapper in `runner/image/mc_dispatch.go` executes `/opt/culler-runtime/libexec/mc-real`.
The real binary belongs to uid and gid 10001 and has mode `6755`.
The runtime volume permits read and execute access and blocks file changes.
See [runner containers](runners.md) for the Project image boundary.

Agents run as `mc-model` (uid 10002). The spine directory `/mc/spine` is owned by uid 10001 with mode 0700, so only the setuid binary can open the spine. `runJSONPathForCredentials` returns the fixed `/mc/run.json` whenever the real and effective uids differ, and ignores `MC_RUN_JSON`. A direct host or test invocation, with equal uids, may still use `MC_RUN_JSON` as a test seam. `mc/verbs/runjson_test.go` covers both cases. `fixedMountRoot` applies the same rule to `/mc/artifacts` and `/workspace`. The resident bind-mounts the envelope read-only, so the agent cannot rewrite its own scope.

## Supporting packages

- **`mc/config`** owns `MC_HOME/config.toml`, the machine-wide operator policy: runtimes and credential sources, budgets, timing, container limits, surfaces, and Git identity. `Load`, `Parse`, and the atomic write helpers validate it. `Ensure` writes the embedded `config.example.toml` when no file exists.
- **`mc/routing`** parses `MC_HOME/routing.md`, a single Markdown table `| role | harness | binding |` covering seven roles: strategist, arbiter, worker, verifier, scribe, packager, collie. `Parse` rejects prose, unknown or duplicate roles, unresolved bindings, and harness mismatches. `registry_active.go` uses only the production catalog. `registry_active_fake.go`, built only with `test_fake_routing`, adds the `fake` harness family.
- **`mc/boundary`** is the pure, fail-closed confinement policy for profile admission and dispatch planning. It covers the TOML mount allowlist (`ParseMountAllowlist`, `ResolveAccess`, which never downgrades RW to RO), the denied-path jurisdiction (`ResolveJurisdiction`, which takes the owner uid as a parameter and never reads ambient identity), a shipped blocked-path floor (`.ssh`, `.aws`, `*.pem`, `auth.json`, and others) that operator additions can extend but not remove, and an environment guard with a shipped forbidden-variable floor.
- **`mc/refusal`** classifies dispatch refusals. Each code maps to one of three classes: `stale` (retry, no durable effect), `health` (record deployment health, charge no task), or `candidate` (consequence depends on the subject: block the task, record health, or end a Collie session). The package is storage-free so the whole table can be tested without a spine. It imports the `mount.*` codes from `mc/boundary` rather than copying them. See [Failure controls](../concepts/failure-controls.md).

## Other commands under mc/cmd

- **`mc/cmd/culler-package`** builds the release package. It validates the production binary for the commit and platform, extracts the pinned AgentsView executable, writes the package, and prints metadata that includes the commit, the SHA-256, the runtime pins, and `substrate.CurrentSchemaID`. `release/build.sh` calls it. See [Install and deployment](../operations/install-and-deployment.md).
- **`mc/cmd/probe-resolve`** is a diagnostic. It runs `boundary.ResolveSource` on common credential directories under `$HOME` and on each argument, and prints `ok`, `FAIL`, or `SKIP` for each.

## Related pages

- [Culler system architecture](system-overview.md)
- [The spine: SQLite substrate and schema](spine-and-substrate.md)
- [Dispatch and the tick loop](../workflows/dispatch-and-tick-loop.md)
- [Agent input](../concepts/agent-input.md)

## Discord host commands

`mc/cmd/mc/discord.go` separates host configuration from spine projection and resident network operations.
The `discord` command owns dashboard actions; `__discord-config` supplies the resident's private runtime policy.
`__discord-state` applies the durable availability and route projection through the spine.
Each entry point requires host scope before it reads configuration or opens the database.
These commands are internal component interfaces. The operator uses **Configs → Discord**.

The host checks the owner, socket type, and mode 0600 of `MC_HOME/discord.socket` before a resident request.
The request carries JSON over that local socket.
The host compares the expected configuration revision before changes and repeats that check within the atomic configuration update.
This rejects a stale dashboard write before it changes the connection.
See [Discord configuration](../integrations/discord.md) for activation and recovery across these boundaries.
