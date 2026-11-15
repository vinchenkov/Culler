# fake-harness — the `fake` family's Runtime Adapter contract

A CLI-shaped, deterministic, token-free harness stub that implements the
Runtime Adapter contract. A test configuration alone registers the fake
family as a harness family. Everything it does comes from a scripted
behavior file: no network, no LLM, no credentials. The harness needs no
runtime authentication.

This document is the fake family's adapter contract. `cli.ts` is the
implementation; `behavior.ts` is the behavior-file schema and its
fail-closed validator; `*.test.ts` (run via `./check.sh`) is the
executable form of this contract.

## Invocation

```
bun cli.ts --behavior <file> --session-dir <dir> [--turn-file <file>] [--session-id <id>]
```

(in-container: `bun /app/src/fake-harness/cli.ts …` — runner source is
bind-mounted read-only at `/app/src`.)

- `--behavior <file>` (required) — the scripted behavior JSON (schema
  below). Invalid or unreadable ⇒ exit 2 with a message on stderr.
- `--session-dir <dir>` (required) — the session folder. The host creates
  it before launch. The harness only ever writes into it; a missing dir ⇒
  exit 2. **Existence is checked, writability is not**: a read-only mount
  dies on the first append, which is the intended fail-closed behavior.
- `--turn-file <file>` (optional) — read the one top-level turn from this
  file; when omitted the turn arrives on **stdin**. The runner supplies it
  from `run.json.brief` unchanged. One pipeline run takes one turn.
  The two channels are equivalent; the turn text lands verbatim in the
  `turn-start` event's `input` field either way.
- `--session-id <id>` (optional) — override the native session handle.
  Precedence: `--session-id` > behavior `session_id` > `"fake-session"`.

The harness makes exactly one turn. There is no resume mode, no second
turn, no interactivity.

### Environment (brief comprehension)

A real role reads its brief; a scripted behavior cannot. The runner
exports `MC_RUN_ID`, `MC_ROLE`, `MC_SUBJECT_ID` (empty when null),
`MC_POOL_IDS` (arbiter), and `MC_SPINE` into the harness environment, and
behavior `exec` steps invoke the real scoped `mc` using them. The harness
itself reads **no** environment variables — it only passes its environment
through to `exec` children.

## Behavior-file schema (`behavior.ts`)

A single JSON object:

```json
{
  "session_id": "fake-session",
  "fixed_ts": "2026-01-01T00:00:00.000Z",
  "steps": [
    { "do": "sleep", "seconds": 2.5 },
    { "do": "exec", "command": "mc complete $MC_SUBJECT_ID --run $MC_RUN_ID --status worked" },
    { "do": "succeed", "output": "done" }
  ]
}
```

Top-level keys (all others rejected):

| key | type | meaning |
|---|---|---|
| `session_id` | string, optional | fixed native session handle; default `"fake-session"` (deterministic) |
| `fixed_ts` | ISO-8601 string, optional | stamped as `ts` on **every** event, making `native.jsonl` byte-deterministic for golden tests; when omitted, real wall-clock ISO timestamps are used |
| `steps` | non-empty array, required | executed in order; **exactly one terminal step, and it must be last** |

Step kinds (any other `do`, missing/extra keys, or bad types are rejected):

| `do` | keys | terminal | semantics |
|---|---|---|---|
| `succeed` | `output` (string) | yes | emit `turn-complete` with `status:"success"` and the output, exit **0** |
| `crash` | `code` (integer 1–255) | yes | exit immediately with `code`; **no** `turn-complete` — a real harness dying mid-turn writes no completion event |
| `hang` | — | yes | never complete, never exit; the process stays alive (silent) until killed — exercises the hard-deadline/reap path |
| `exec` | `command` (non-empty string) | no | run via `/bin/sh -c`, record a `tool-use` event with its exit code and captured output; the child's exit code **never** changes the harness's own flow — the scripted terminal step alone decides the exit status |
| `sleep` | `seconds` (number ≥ 0, fractional ok) | no | pause; used to make a run span heartbeat intervals |

The validator is **fail-closed**: unknown step kinds,
unknown keys, a terminal step mid-list, a non-terminal last step, out-of-
range values — all reject with exit 2, never default. A typo in a behavior
file must fail the test loudly, never silently succeed as an empty turn.

## `native.jsonl` — the native session record

Appended to `<session-dir>/native.jsonl` **as it runs** — one `appendFileSync`
per event, no buffered write-at-exit. A container that dies mid-run loses
nothing. The filename is fixed here, but the runner must still register it
with `mc run register-session … --file native.jsonl` rather than assume it.
The filename stays a locator because real harness families differ.

One JSON object per line, `ts` always the last key:

```
{"event":"session-start","session_id":"fake-session","harness":"fake","ts":…}
{"event":"turn-start","session_id":"fake-session","turn":1,"input":"<the turn>","ts":…}
{"event":"tool-use","session_id":"fake-session","turn":1,"tool":"shell","command":…,"exit_code":N,"stdout":…,"stderr":…,"ts":…}   (one per exec step)
{"event":"turn-complete","session_id":"fake-session","turn":1,"status":"success","output":…,"ts":…}   (succeed only)
```

`tool-use` captures child stdout and stderr each truncated to **8 KiB**
(8192 bytes, measured in UTF-8 bytes). A cut that would split a multi-byte
character backs off to the previous character boundary, so the kept prefix
is always valid UTF-8 and ≤ 8192 bytes.

## Event stream on stdout

Every event is mirrored to stdout as the same JSON line, **after** the
durable `native.jsonl` append. The durable write comes first. The runner
reads this stream *only* to detect completion and exit status:

- `session-start` yields the native session handle → the runner's
  `mc run register-session`.
- `turn-complete` with `status:"success"` followed by exit 0 = clean turn.
- Process exit with **no** `turn-complete` = crash; the runner reports the
  exit to `mc` and exits with the exit code of the harness. The runner never
  converts an ordinary harness exit into success.

Diagnostics go to stderr only; stdout carries nothing but event lines.

## Exit statuses

| exit | meaning |
|---|---|
| 0 | clean turn (`succeed`; `turn-complete` was emitted) |
| 1–255 (scripted) | `crash` step: died mid-turn, no `turn-complete` |
| 2 | usage or environment error: bad/missing flags, missing session dir, unreadable turn file, invalid behavior file (message on stderr) |
| 70 | internal invariant breach (validator let a non-terminal ending through — should be unreachable) |
| (none — killed) | `hang`: alive and silent forever; only SIGKILL/`docker stop` ends it |

Exit 2 and 70 both fail the spawn.

## What it deliberately does NOT do

- **No heartbeats — ever, including during `hang` and `sleep`.** The
  heartbeat is the **runner's**, never the harness's. A silent
  `hang` with the runner still heartbeating is exactly how the
  hard-deadline reap is exercised. Do not "fix" this.
- No network, no LLM/API calls, no credentials, no resume, no retries.
- Never touches the spine itself — `exec` steps may invoke the scoped
  `mc`, which is the *agent side* of the container acting, not the harness.
- No writes outside `<session-dir>/native.jsonl` (plus whatever `exec`
  children do, which is the behavior author's responsibility).

## Determinism

With `fixed_ts` set and a fixed `session_id`, `native.jsonl` output is
byte-for-byte identical across runs (golden-testable). Without `fixed_ts`,
only the `ts` values vary; event order, key order, and everything else
remain deterministic.

## Running the tests

```
./check.sh        # cd here, mise exec -- bun test
```

Docker-free, token-free, part of the fast suite.
