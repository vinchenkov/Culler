# Native OpenCode traces

Culler supplies native OpenCode databases to AgentsView and Collie.
The [AgentsView release record](../mc/deployment/agentsview.json) pins the native reader.
The [OpenCode package](../runner/opencode/package.json) pins the source format.

## Identity and access

Each execution retains its native main session reference.
The trace record names `opencode/data/opencode/opencode.db` below its session folder.
AgentsView identifies that session as `opencode:<native_session_ref>`.
Its child sessions retain their native references and parent relationships.

The trace service covers these executions:

- Each pipeline run uses native traces, including each Packager and Scribe run.
- Each Collie session uses native traces for all turns.
- Each setup operation uses native traces.
- Each validation operation uses native traces.

OpenCode resumes from its original session store.
The resident reads that store through read-only access.
AgentsView reads separate copies under `MC_HOME/agentsview/traces/opencode/`.
Each execution has one flat file named `opencode-<id>.db`.

Collie receives a read-only mount of `MC_HOME/agentsview/inspection/` at `/mc/traces`.
Each database in that mount links to the corresponding complete trace copy.
The runtime supplies [trace-read.ts](../runner/shared/trace-read.ts) for bounded reads of native records.
The Collie directive, conversation brief, and command skill describe that access.

The dashboard grants authenticated access to the native AgentsView session.
The AgentsView token remains on the host.
Packager evaluation records preserve the masked database as `execution/trace.db`.

## Copies and masks

Each copy contains one committed SQLite state.
The copy includes committed records from the write-ahead log (WAL).
An incomplete transaction stays outside the copy.
An idle database without auxiliary files uses an unchanged-file check around a private memory copy.
A WAL without its shared-memory file delays the copy until OpenCode supplies both files.
The trace service creates no auxiliary file in the original store.

The resident builds a fresh database from the transcript tables that the native reader uses.
The copy retains native messages, tool records, and child sessions.
It also retains model metadata, token usage, and record times.
Account tables and unused source pages stay outside the copy.

The resident applies credential masks before it publishes the copy.
The masks cover known provider credentials and temporary Culler tokens.
The copy remains valid JavaScript Object Notation (JSON) inside a valid native database.
A private hash record preserves prior masks across credential changes and resident restarts.
The hash record contains no credential values.

An atomic rename publishes each complete database.
A content digest detects record changes when the source file size stays unchanged.
The resident requests an explicit AgentsView import for changed copies.
That import also detects changes when native timestamps stay unchanged.

## Availability and limits

The trace lane runs separately from the dispatch tick.
Each copy pass selects at most four native executions.
The normal pass interval is two seconds.
An ended execution becomes due for another refresh after one minute.
The pass limit and retry delay control its next attempt.

Each native read has these limits:

- The worker has a five-second deadline.
- Each source file has a 512-mebibyte limit.
- The transcript has a 128-mebibyte limit.
- The database has a 200,000-record limit.
- The database has a 256-session limit.

A failed source read can require another bounded read of the last valid copy.
That read applies current masks to the retained copy.
Each AgentsView pass has a separate request limit.
The trace lane preserves agent results and task state after a trace failure.

Each inspection folder contains `status.json`.
The `copy` field uses these values:

- `ready` identifies a current complete copy.
- `pending` identifies a copy that needs another pass.
- `delayed` identifies an older valid copy after a failed refresh.
- `unavailable` identifies an absent valid copy.

The `cause` field explains a copy failure.
The `import` field separately reports `pending` or `current`.
The `import_cause` field explains a failed AgentsView request.
The trace health report identifies imports that exceed five minutes after AgentsView becomes available.

## Retained executions

The trace service projects retained OpenCode locators onto their native databases.
The projection preserves the spine records and original session stores.
Setup recovery uses the native main session marker or an unambiguous native root session.

A successful native import permits retirement of the converted Codex entry.
AgentsView keeps that export and its operator annotations in its trash.
The resident records retirement across restarts and removes the converted trace copy.

An unavailable native database leaves its converted evidence available.
The display name and inspection status identify that evidence as an OpenCode export.
A dashboard trace link selects that export until the native entry becomes available.

## Acceptance checks

The component checks cover native identity, masks, recovery, and access limits.
The dashboard browser suite covers the authenticated native trace link.
The optional checks below use synthetic content and credentials.
They use scratch directories and remove their containers after each check.

Run these commands from `resident/`:

```sh
AGENTSVIEW_BIN=<pinned-executable> mise exec -- bun acceptance/opencode.ts
NATIVE_TEST_IMAGE=<runtime-image> mise exec -- bun acceptance/opencode-container.ts
NATIVE_TEST_IMAGE=<runtime-image> NATIVE_OPENCODE_ROOT=<package-directory> mise exec -- bun acceptance/opencode-resume.ts
```

The AgentsView check verifies the executable digest against the release record.
It verifies native content, child relationships, and Project association.
It also verifies credential changes and annotation retention after export retirement.

The container check verifies committed WAL records while a writer remains active.
It also verifies the final copy after the writer exits.
The resume check uses the pinned OpenCode executable with a synthetic provider inside an isolated container.
It verifies that a native resume preserves the session reference and both turns after a host snapshot.
