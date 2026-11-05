---
type: concepts
title: Structured logging and log parity
description: How Go's mc/mclog and TypeScript's runner/shared/log.ts write one byte-identical NDJSON format, rotate shared files, and are held in parity by a shared golden fixture.
tags: [logging, ndjson, mclog, parity, rotation]
sources:
  - id: openwiki-source-43c99b2bf6bbadedfe48eeb3
    resource: repo://mc/check.sh
  - id: openwiki-source-d75ace6ccbeb2d45580cecc6
    resource: repo://mc/cmd/mc/cli_log.go
  - id: openwiki-source-c399657e717998faab2fdc92
    resource: repo://mc/deployment/log_event_registry_test.go
  - id: openwiki-source-fe60283cedac3a23d5e9b133
    resource: repo://mc/deployment/log_parity_test.go
  - id: openwiki-source-2acc0ee479b49df653c51c3c
    resource: repo://mc/mclog/mclog_test.go
  - id: openwiki-source-3007affdd6a005d3387629bc
    resource: repo://mc/mclog/mclog.go
  - id: openwiki-source-af883cb063a2e0ea839e0057
    resource: repo://mc/mclog/record_test.go
  - id: openwiki-source-8d08077dd6ac878131efc3f5
    resource: repo://mc/mclog/record.go
  - id: openwiki-source-48962b2fa553cc4ae6a80983
    resource: repo://mc/mclog/rotate_test.go
  - id: openwiki-source-2126d67fb4b387fdd269622c
    resource: repo://mc/mclog/rotate.go
  - id: openwiki-source-97a21c7214182db704538284
    resource: repo://runner/shared/golden.test.ts
  - id: openwiki-source-553dd2db9fad6442ea2c619e
    resource: repo://runner/shared/log-sink.ts
generated: { by: "culler-scribe", at: "2026-10-17T00:25:06.382Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-10-19T19:56:40.385Z
---

# Structured logging and log parity

Culler writes every operational log as NDJSON: one JSON object per line, with a fixed head and flat scalar context. Two implementations produce that format. `mc/mclog` serves the Go `mc` binary. `runner/shared/log.ts` and `runner/shared/log-sink.ts` serve the resident, the dashboard, and the runners. Both must emit byte-identical lines, because short-lived `mc` processes and long-lived TypeScript processes can append to the same component files and the same readers (`mc logs`, `mc health`) render them.

## The record format

A record has five reserved head keys in a fixed order: `ts`, `level`, `comp`, `event`, `msg`. All other keys are context fields, sorted alphabetically after the head. The Go encoder is `encodeRecord` in `mc/mclog/record.go`. The TypeScript writer is `serialize` in `runner/shared/log.ts`.

- **Timestamp.** Go formats `ts` with the layout `2006-01-02T15:04:05.000Z07:00` on a UTC time. This matches JavaScript's `Date.prototype.toISOString()` (millisecond precision, `Z` suffix). `TestGoWriterEmitsTheAgreedKeyOrder` in `mc/deployment/log_parity_test.go` checks this shape.
- **Levels.** The levels are `error`, `warn`, `info`, `debug`, ordered from least to most verbose. A logger writes a record only when the record's level is at or below the configured level. Below the threshold, nothing is encoded.
- **Context values.** Only scalars survive: strings, booleans, integers, finite floats, and null. Go's `mergeContext` and `scalarValue` drop any reserved key, any nested value, and any NaN or infinity. The TypeScript `isScalar` applies the same rule. When fields are dropped, the record gains a `dropped_fields` count. Bound context (`With` in Go, `child` in TypeScript) merges first, and per-call context overrides it.
- **Caps.** `msg` is capped at 2048 bytes and the whole line at 8192 bytes. A cut ends with `…` and lands on a UTF-8 rune boundary. When a line is too long, the encoder sets `truncated: true` and sheds the largest context fields first. If only the head remains and it is still too long, only `msg` shrinks further. The head keys are never removed.
- **Escaping.** Go encodes values with `SetEscapeHTML(false)` so that `<`, `>`, and `&` are written literally, as `JSON.stringify` writes them (`TestValuesUseJavaScriptEscaping` in `mc/mclog/record_test.go`).

`RenderText` (Go) and `renderText` (TypeScript) turn a record into the operator line: `ts`, the upper-case level padded to five characters, `comp`, `event`, sorted `key=value` pairs, then ` — ` and the message. Go's `RenderText` returns `false` for any line that is not a JSON object with string `ts`, `level`, `comp`, and `event`. `mc/verbs/logs.go` and `mc/verbs/health.go` use it to show log tails, so a half-written line or a crash-channel line does not break the rest of the window.

## The Go package: `mc/mclog`

`mc/mclog` has three files:

- `mclog.go` defines `Level`, `ParseLevel`, `Ctx`, `Logger`, `OpenComponent`, and the level methods (`Error`, `Warn`, `Info`, `Debug`, `At`), plus `With` and `Close`.
- `record.go` holds the encoder and `RenderText`.
- `rotate.go` holds the rotation ring.

`ParseLevel` never fails. It trims and lower-cases the input and returns the fallback for any unknown string, so a wrong `MC_LOG_LEVEL` cannot stop a process.

`OpenComponent(dir, comp, level)` validates the component name, then opens `<dir>/<comp>.ndjson` with `O_APPEND`, mode `0600`. A component name must be non-empty, must not start with a dot, and may contain only `[A-Za-z0-9._-]`. This rejects path-like names such as `a/b`, `..`, and `.hidden` before any file is touched.

The package exposes two contracts:

1. **`OpenComponent` never creates an absent directory and returns `(nil, nil)` for it.** A missing log root is a state, not an error. `TestOpenComponentTreatsAMissingDirectoryAsNoLogging` in `mc/mclog/mclog_test.go` checks both the nil result and that the directory stays absent.
2. **A nil `*Logger` is a no-op.** Every method checks for a nil receiver. `With` on nil returns nil, and `Close` on nil returns nil. `TestEveryMethodIsSafeOnANilLogger` covers each method.

These two contracts work together in `mc/cmd/mc/cli_log.go`. `openCLILogger` resolves `MC_HOME`, reads `MC_LOG_LEVEL` (default `info`), and opens `$MC_HOME/logs/mc.ndjson`. Inside the helper container and inside agent containers, `$MC_HOME/logs` is not visible. `OpenComponent` returns nil there, and the nil logger silently does nothing. As a result, a self-delegated verb is logged exactly once, on the host, with no special branch. Only `mc/cmd/mc` constructs the logger, so the verb layer stays free of logging. The CLI records only the verb, subverb, and flag names that match a name-shaped pattern, never flag values.

### Rotation

`ROTATE_MAX_BYTES` is 16 MiB and `ROTATE_KEEP` is 4. After each write, `maybeRotate` checks the file size. At or over the cap, the logger takes `<path>.rotate.lock` with `O_EXCL`. It re-checks the size, renames the live file to `.rotating`, and reopens a fresh file. Then it shifts the gzipped ring (`.1.gz` through `.4.gz`, dropping the oldest) and compresses into `.1.gz` through a `.next` temporary file. Finally it removes `.rotating` and fsyncs the directory.

Rotation is best-effort and never blocks a write. If another appender holds a live lock, the writer keeps appending past the cap. A lock older than 60 seconds counts as stale and is broken once. All rotation errors are swallowed. `mc/mclog/rotate_test.go` covers these cases: `TestALiveLockMakesRotationOvershootRatherThanStall`, `TestAStaleLockIsBrokenExactlyOnce`, `TestRotationFailureNeverReachesTheCaller`, and `TestConcurrentAppendersOnOneFileStayWellFormedAcrossRotations`. Many `mc` processes append to one file, so this matters. Ownership is never exclusive, and a stalled verb would be worse than an oversized file.

## The TypeScript twin

`runner/shared/log.ts` is pure: it has no filesystem access. It exports `LEVELS`, `parseLevel` (which also never throws), `levelEnabled`, `serialize`, `renderText`, `makeLogger`, and `makeTestLogger`. It also owns `EVENTS`, the registry of every event name a TypeScript call site may use, and `EVENT_GRAMMAR`, the pattern each name must match. The `Logger` type accepts only registered events. No free-text form exists. The module header forbids any mapping from refusal codes or exit statuses to levels: the call site chooses the level, and codes travel as context fields.

`runner/shared/log-sink.ts` is the IO half:

- `openFileSink` writes to an `O_APPEND` file. It uses the same rotation ring and constants as Go (`ROTATE_MAX_BYTES = 16 * 1024 * 1024`, `ROTATE_KEEP = 4`) and swallows every failure.
- `stderrSink` writes the same NDJSON to stderr for processes without a log directory.
- `openRunSink` writes the per-run `runner.ndjson` with no rotation and a hard 4 MiB cap (`RUN_SINK_MAX_BYTES`). It writes one `log.sink_failed` record when it reaches the cap.

The resident writes `resident.ndjson` and the dashboard writes `dashboard.ndjson`, both through `openFileSink`. The agent runner, the Homie runner, and the Claude and Codex adapters use `runLogSink`. It selects the log directory from `MC_RUN_LOG_DIR`, with `/mc/logs` as the default. It uses `openRunSink` when that directory exists, and `stderrSink` otherwise.

The shared module must stay under `runner/`. `mc/deployment/runner_release.go` addresses files relative to `runner/` and cannot express `../`, while the host manifest can reach `runner/` from the repository root.

## Parity enforcement

The parity guard is one shared fixture: `runner/shared/testdata/render-golden.json`. Each entry pairs an `ndjson` line with its expected `text` rendering. Both suites read the same file, so a change that suits one implementation fails in the other. When the fixture disagrees with a change, change both implementations. Do not edit the fixture to satisfy one side.

The comment above `renderGolden` in `log_parity_test.go` names `log.test.ts` as the TypeScript reader. The file that reads the fixture is `runner/shared/golden.test.ts`.

- `runner/shared/golden.test.ts` checks that `renderText` reproduces every golden `text` and that `serialize` re-emits every golden line byte for byte. It also checks that the fixture is non-empty and still covers all four levels, a record with no context, `null`, `true`, numbers, an em dash inside `msg`, and an empty `msg`.
- `mc/deployment/log_parity_test.go` checks that `mclog.RenderText` matches each golden `text` (`TestGoAndTypeScriptRenderTheSameText`). It also checks that non-records are refused and that the Go writer emits the agreed key order with a `toISOString`-shaped timestamp.
- `mc/deployment/log_event_registry_test.go` parses `EVENTS` out of `log.ts`. It fails when any non-test TypeScript `log.<level>("…")` call in `resident/src`, `dashboard/src`, `runner/agent-runner`, `runner/homie-runner`, or `runner/shared` names an unregistered event. It also fails when a registered name breaks the grammar, when the removed `legacy.line` event reappears, when a repository event has no call site, or when the Go and TypeScript rotation constants disagree.

These Go tests live in `mc/deployment/` rather than `mc/mclog/` because they read files outside the Go module. Go's test cache does not track such files, so `mc/check.sh` runs `go test -count=1 ./deployment/` to prevent a stale cached pass. No TypeScript typechecker runs in this repository, so the registry test is the only check against a misspelled event name.

## Related pages

- [Runner containers and harness adapters](../architecture/runners.md): the run log sinks.
- [Resident](../architecture/resident.md) and [Dashboard](../architecture/dashboard.md): the long-lived writers.
- [Checks and test lanes](../tests/checks-and-test-lanes.md): where the parity tests run.
- [Install and deployment](../operations/install-and-deployment.md): release manifests.

## Command duration evidence

The Go `cli.invoked` record includes the process identifier and whole-command duration.
It covers dispatch and helper delegation together.
The dashboard emits `dash.mc_completed` after process exit, closed output streams, and result parse.
That event uses the command observer in `dashboard/src/mc.ts`.
These durations have different boundaries. Compare them only with those boundaries in view.
See [performance experiments](../tests/performance-and-evaluations.md).
