---
type: integrations
title: OpenWiki export and AgentsView trace viewer
description: How Culler runs OpenWiki for the Scribe, exports each Project's accepted wiki for the dashboard's wiki address, and feeds agent traces to a pinned, token-protected AgentsView.
tags: [openwiki, agentsview, wiki, traces, viewers, scribe]
sources:
  - id: openwiki-source-8d31e9741b11f4dcc49fde73
    resource: repo://dashboard/src/agentsview-proxy.ts
  - id: openwiki-source-ec1ab673f7783f0c2272fd8e
    resource: repo://dashboard/src/wiki-server.ts
  - id: openwiki-source-d7b6a450e793afbfd727775a
    resource: repo://mc/cmd/mc/agentsview_access.go
  - id: openwiki-source-34ce6a1ce9505093ba601ecc
    resource: repo://mc/cmd/mc/wiki_export.go
  - id: openwiki-source-b40f0fb7710571de842a0cb3
    resource: repo://mc/deployment/agentsview_supervision.go
  - id: openwiki-source-b498a26a6023737a41613629
    resource: repo://mc/deployment/agentsview.go
  - id: openwiki-source-19bf0f989c7b56785c7143d0
    resource: repo://mc/deployment/viewer_secrets.go
  - id: openwiki-source-0d1d52008655ac2b4b8c49f8
    resource: repo://mc/dispatch/wikiupdate.go
  - id: openwiki-source-585b56cb97757a0c9e515309
    resource: repo://mc/trace/trace.go
  - id: openwiki-source-527341836a986f10395ba880
    resource: repo://mc/verbs/doctorviewers.go
  - id: openwiki-source-20bef77e6c50208d3b9a101e
    resource: repo://mc/verbs/wikicomplete.go
  - id: openwiki-source-fde55848bbd2ed54bff3bf77
    resource: repo://mc/verbs/wikiround.go
  - id: openwiki-source-abd47ff674dffecc582d6036
    resource: repo://mc/wiki/export.go
  - id: openwiki-source-d43ba5150b1dcd48ee382e77
    resource: repo://resident/src/agentsview.ts
  - id: openwiki-source-85e596193d0d86cbb20c87e8
    resource: repo://resident/src/opencode-copy.ts
  - id: openwiki-source-8251344551ae8ddd6e723ab3
    resource: repo://resident/src/opencode-snapshot.ts
  - id: openwiki-source-32f0aa3264ff5051674a12ed
    resource: repo://resident/src/wiki-lane.ts
  - id: openwiki-source-dea7f0a13923e087ab24e792
    resource: repo://runner/agent-runner/adapters/openwiki.ts
  - id: openwiki-source-45c5b80a26df13dfbeddcb87
    resource: repo://runner/openwiki/integration.mjs
  - id: openwiki-source-0ed903ccac623e89a97565d1
    resource: repo://runner/openwiki/package.json
  - id: openwiki-source-9bdf2689e08b7fd68ad0d8c9
    resource: repo://runner/openwiki/patch-upstream.mjs
  - id: openwiki-source-bd92323608bb4fe6f961f632
    resource: repo://runner/openwiki/README.md
  - id: openwiki-source-ace63dad3a5b181560e37003
    resource: repo://runner/openwiki/server.ts
  - id: openwiki-source-a37b5e4aedd4e9eae5294f1a
    resource: repo://runner/shared/opencode-trace.ts
generated: { by: "culler-scribe", at: "2026-11-12T05:23:35.047Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-12T05:23:35.047Z
---

# OpenWiki export and AgentsView trace viewer

Culler integrates two third-party tools. **OpenWiki** (package version 0.9.0) maintains a Claim-checked knowledge base under each Project's `openwiki/` folder. The Scribe role writes it, and the operator reads it through the wiki address. **AgentsView** is a local trace viewer that shows the native transcripts of pipeline runs, Collie sessions, and setup operations. Both are optional to the pipeline's correctness, so failures in either one are logged and never block dispatch. Both listen on loopback addresses that agent containers can reach, so each protects itself with a viewer secret.

## OpenWiki for the Scribe

### Runner side: `runner/openwiki/`

The runner image installs the locked OpenWiki package into `/opt/culler-runtime/openwiki` with `npm ci --omit=dev --ignore-scripts`. It then runs `patch-upstream.mjs`, which checks each upstream file against a recorded SHA-256 and the exact version `0.9.0` before it rewrites that file. According to `runner/openwiki/README.md`, the patches give Culler control of repository setup, preserve project documents (Markdown with `document: true` frontmatter), and keep those documents out of generated Claim state. They also stop finalization when the source changed. For example, the resolver patch refuses a generated Claim whose evidence is under `openwiki/` unless that path is a project document. `project-documents.mjs` holds the set of project-document paths that the patched code consults.

`server.ts` is the MCP server. It takes `--repo`, `--input-commit`, and `--receipt`, builds the integration with `createIntegration` from `integration.mjs`, and serves OpenWiki's six host tools over stdio:

- `openwiki_begin`
- `openwiki_submit_plan`
- `openwiki_next_page`
- `openwiki_inspect_page_claims`
- `openwiki_submit_page`
- `openwiki_finish`

`integration.mjs` pins `VERSION` and the package integrity `REVISION`, computes a digest over all wiki files, validates links and documents, and writes the completion receipt.

On a Scribe run, the agent runner passes `--openwiki` and the input commit to the harness adapter (see [Runner containers and harness adapters](../architecture/runners.md)). `openWikiServer` in `runner/agent-runner/adapters/openwiki.ts` launches the server through `/usr/bin/env -i` with only `PATH` and `HOME=/tmp/mc-openwiki`. As a result, the OpenWiki process sees no provider credentials, and all model inference comes from the Scribe's own harness. The receipt goes to `/tmp/openwiki-completion.json`. The adapter waits up to 30 seconds for the server to report ready.

The Scribe's procedure is the shipped skill `mc/verbs/skills/scribe/openwiki/SKILL.md`, alongside its command surface `mc-scribe`. The skill tells the Scribe to resolve the repository root, call `openwiki_begin` in `init` or `update` mode, keep the returned `runId`, and treat content outside `openwiki/` as read-only.

When the Scribe submits, `mc/verbs/wikicomplete.go` calls `wiki.ValidateCompletion` in `mc/wiki/completion.go`. The receipt must be a regular file. It must name the input commit, the source commit, the OpenWiki version and revision, and a wiki digest that matches the submitted Git objects. The check also enforces the wiki write scope and preservation of verified document content. The Scribe submits a separate wiki update, outside the task pipeline.

### Page verification and recovery

`openwiki/.verification.json` binds each verified page to its Claim sidecar and source inventory.
The inventory records source paths, file content, file types, and executable modes.
It detects source changes when the previous Git commit is unavailable.
OpenWiki uses a Git comparison when no saved inventory exists.
If neither comparison works, the affected page group requires a full review.
Added source files also require assessment, even when no existing Claim cites them.

The begin response limits changed-path and Claim-issue lists and reports their exact counts.
Each page group with a readable Git baseline includes recent commit messages for change intent.
The Scribe expands truncated source lists through Git and uses `claimIssuePages` to cover each affected page.
The integration rejects a plan that defers a Claim issue; every issue requires review in the same update.

An external edit to a generated page or Claim sidecar requires page review.
A malformed or absent sidecar requires new Claims.
A deleted generated page requires explicit restoration or deletion in the plan.
A legacy page without complete verification records requires review before reuse.
Original project documents retain their content protection.

The Scribe inspects Claims after the final page edit.
Submission rejects a later edit to the page or its Claims.
Completion rejects an external edit to a verified page.
A resumed update keeps valid page completions and reopens invalid completions.
These rules prevent an interrupted write from establishing a complete result.
`runner/openwiki/README.md` describes the contract; `integration.mjs` implements its checks.

The receipt protects against an incomplete OpenWiki run by an honest Scribe.
The Scribe can write the receipt file, so the receipt cannot establish trust against a hostile Scribe.

### Export: `mc __wiki-export` and `mc/wiki/export.go`

The resident's wiki lane (`resident/src/wiki-lane.ts`) calls `mc __wiki-export --image <runner image>` at start and every `WIKI_INTERVAL_MS` (20 s). Calls run one at a time on a promise chain, outside the tick loop. The lane logs `wiki.exported` when a Project's export moves to a new commit. It logs `wiki.export_failed` once per distinct cause, so a persistent failure does not flood the log.

`runWikiExport` in `mc/cmd/mc/wiki_export.go` requires host scope and a valid image reference. It reads the Project list and runs `wiki.Exporter.Export` against `MC_HOME/wiki` (`deployment.WikiExportRoot`). For each Project, `exportProject` does the following:

- It reads the `main` branch commit and its `openwiki` tree id. A Project with no repository or no `openwiki/` tree gets its export removed (status `absent`).
- If `export.json` already names the same commit and tree, nothing runs (status `current`).
- If the same commit and tree failed less than `ExportRetryDelay` (10 minutes) ago, the export is not retried yet.
- Otherwise `replaceExport` materializes the `openwiki/` tree of the accepted commit into a private stage. Uncommitted files therefore never reach an export. It then runs the export container. It moves the output to `<project>/<commit>/`, writes `export.json`, and prunes older folders.

A failure writes `failure.json` and keeps the previous export. A stop signal is not recorded as a failure. Projects that failed last time run last. A lock file serializes concurrent exporters.

`ExportDockerArgs` defines the container: `--network none`, uid 10002, `--cap-drop ALL`, `no-new-privileges`, `--read-only`, 1 CPU, 1024 MB of memory, 128 pids, and an empty environment (`env -i PATH=…`). The command is OpenWiki's `visualize … --export /output`. `DockerExport` enforces `ExportTimeout` (120 s) and force-removes a failed container. These properties are covered by `mc/wiki/export_test.go`, for example `TestExportUsesTheAcceptedOpenWikiTreeOnly`, `TestAFailedExportKeepsThePreviousExport`, and `TestExportDockerArgsIsolateTheContainer`.

### Serving: the wiki address in the dashboard process

`dashboard/src/wiki-server.ts` is the dashboard's second listener. It runs on a separate origin, `[surfaces.wiki] bind`, which defaults to `127.0.0.1:7335`. Because the origin differs from the dashboard's, an exported page can never read the dashboard token. Every path must begin with the wiki key, a 64-hex secret stored in `MC_HOME/state/wiki-key`. `EnsureWikiKey` and `ReadWikiKey` in `mc/deployment/viewer_secrets.go` create and read it. The key is created once and never rewritten.

The server compares the key in constant time (`wikiKeyMatches`). A request without a matching key gets 404. Only `GET` and `HEAD` are allowed. Every response carries a strict Content-Security-Policy and the `x-culler-service: wiki` header. Exports live at `/<wiki key>/<project>/`.

The export page normally loads four libraries from a CDN. `rewriteExportPage` points each reference that matches `dashboard/src/openwiki/provenance.json` exactly (source and sha384 integrity) at a local copy below the wiki key, and it removes Google Fonts links. The viewer receives no task controls and no credentials. A separate host verb, `mc wiki show --project <id>` (`mc/cmd/mc/wiki.go`, `mc/wiki/reader.go`), reads the accepted wiki of a Project. The resident uses it to write a wiki snapshot that it mounts read-only at `/mc/wiki.json` for Arbiter runs.

## AgentsView

### Pin and install

`mc/deployment/agentsview.json` pins one AgentsView release. It records the version, the `darwin-arm64` platform, and the archive URL, size, and SHA-256. It also records the extracted executable's size and SHA-256, plus a `disabled_agents` list. The file is embedded in `mc`. `ParseAgentsViewPin` rejects unknown fields and invalid versions, and it refuses a `disabled_agents` list that contains `claude`, `codex`, or `opencode`, because AgentsView must read those traces. `mc/deployment/agentsview_install.go` downloads the archive (HTTPS only), verifies size and digest, extracts the single executable, verifies it, and installs it at `MC_HOME/bin/agentsview`. `redeploy.sh` runs this step as `mc __onboard agentsview`.

### Supervision and the token

`SupervisedUnits` in `mc/deployment/agentsview_supervision.go` is `resident`, `dashboard`, `agentsview` in stop order, so AgentsView is the third launchd unit. Activation starts the units in reverse order. `agentsViewLaunchAgent` runs `agentsview serve --host … --port … --no-browser --no-update-check` with these settings:

- `AGENTSVIEW_DATA_DIR` points at `MC_HOME/agentsview/data`.
- Telemetry and update checks are disabled.
- `HTTP_PROXY` and `HTTPS_PROXY` point at the dead `127.0.0.1:9`, which blocks the pricing refresh.
- `PATH` is `/usr/bin:/bin`.

`agentsViewConfigTOML` writes `require_auth = true` with the deployment token, and it points AgentsView's `claude`, `codex`, and `opencode` agents at the trace-copy folders. The default bind is `127.0.0.1:7334`.

The AgentsView token lives at `MC_HOME/agentsview/token`, outside the data folder, so it survives a lost archive. `EnsureAgentsViewToken` creates it once and never rewrites it. Both viewer secrets must be 64 lowercase hex characters, readable only by the operator account. `PingAgentsView` sends the token on every Go request. The resident's client in `resident/src/agentsview.ts` reads the same file and sends `Authorization: Bearer <token>` on every request. That client handles the ping, the project mapping rules (`/workspace`, `/workspaces`, `/mc` map to the AgentsView project `Culler`), the session list, and the display names.

### Browser authentication

Culler trace links first open `/agentsview/open` on the dashboard.
The access page uses dashboard authentication to request a grant from `/api/agentsview/access`.
The server requires the exact dashboard origin before it obtains host credentials.
`mc/cmd/mc/agentsview_access.go` restricts credential access to host scope.

`dashboard/src/agentsview-proxy.ts` starts a separate loopback listener on an available port.
A grant expires after 30 seconds and permits one use.
The proxy exchanges it for a random session cookie with `HttpOnly` and `SameSite=Strict`.
A session expires after 12 hours. The proxy retains both grants and sessions in memory.
A dashboard restart therefore requires a new grant.

The proxy supplies the AgentsView token only to upstream requests.
It removes the token from settings responses and refuses service configuration, synchronization, and embeddings paths.
Embeddings operations download a model and remain outside the browser proxy.
The content security policy permits embedded data fonts from the pinned viewer.
The proxy adds its bridge script to the viewer page and preserves the page's font links.
The separate origin and content security policy isolate trace content from the dashboard.
The proxy validates session destinations and checks the service and requested trace before it grants access.
Failures show a specific access state with a path back through Culler.

`dashboard/src/agentsview-proxy.test.ts` checks authorization, exact origins, single-use grants, expiry, deployment separation, and credential replacement.
`dashboard/smoke/viewers.smoke.test.ts` checks browser access through the dashboard.

### The trace lane

`resident/src/trace-lane.ts` runs on its own timers, outside the tick loop. Every 2 seconds it copies traces. It obtains sessions from `mc __trace-sessions` (`mc/verbs/tracesessions.go`), which returns each run, Collie session, and setup operation with its format, native session ref, AgentsView session id, and display name. The lane writes Claude traces to `MC_HOME/agentsview/traces/claude/projects/culler/` and Codex traces to `MC_HOME/agentsview/traces/codex/sessions/`.

`resident/src/trace-copy.ts` copies only new bytes and stops at the last complete line. The copy size is the resume position, so a restart produces no duplicate and no gap. It opens files and folders with `O_NOFOLLOW` and checks that each one is inside the real session folder, because an agent can swap a folder for a symlink. Each pass has work limits and per-file retry backoff. A separate reconcile pass sets display names and reports main trace copies that AgentsView has not imported.
The resident requests child sessions as well as one-shot and automated sessions.
The pinned viewer records non-interactive Codex traces as child sessions, so this selection preserves their display names and import status.

`mc/trace/trace.go` is the single source of AgentsView identity.
`Format` maps each harness to its native trace format.
OpenCode uses `opencode:<native_session_ref>` and the database locator in `runner/shared/opencode-trace.ts`.
The adapter registers that original session; the resident publishes its masked database copy.
`Path` builds `/sessions/…?msg=last` links.
`RunDisplayName` and `CollieDisplayName` join their parts with ` · `.
A native ref that is not a plain identifier gets no trace, because the ref becomes a file name and link path.

## Doctor checks

`ViewerDoctorFindings` in `mc/verbs/doctorviewers.go` reports two checks, `agentsview` and `wiki`. The AgentsView check passes only when launchd loaded the unit from its prepared definition and a token-authenticated ping succeeds. The wiki check passes when the configured wiki address answers with the wiki service header. Each failure names a repair. Examples: free the port or change `surfaces.*.bind` in `config.toml`, then rerun `install.sh`; or remove an unusable token file. `mc/verbs/doctorviewers_test.go` covers each fault and its repair text.

## Related pages

- [Resident](../architecture/resident.md): the lanes outside the tick loop.
- [Dashboard](../architecture/dashboard.md): the wiki listener.
- [Install and deployment](../operations/install-and-deployment.md): launchd units and onboarding.
- [Structured logging](../concepts/logging.md): the `wiki.*`, `trace.*`, and `agentsview.*` events.

## Scheduled wiki updates

`mc/dispatch/wikiupdate.go` owns the pure wiki decision for one Project.
The tick evaluates wiki rounds after task pipeline rounds.
A wiki update requires an active Project, a known accepted commit, a behind wiki, and an elapsed interval.
The default interval is 3600 seconds. A Project has at most one active wiki update.
The Scribe holds no Project lease and receives no task.
Its first-heartbeat grace is at least 60 seconds.

The Scribe works from the accepted start commit in a separate repository.
It shares the persistent Project home, with separate temporary role directories for its container.
The wiki mount plan includes the expected home and a jurisdiction digest, but excludes ordinary mounts and committed projections.
`mc/verbs/wikiround.go` checks that shape before the run starts.
The Scribe omits automatic dependency preparation and checks retained dependencies before use.
Its repository remains read-only outside `openwiki/` and Git metadata.
See [Project environments](../workflows/project-environments.md#runtime-and-per-run-dependencies) for storage ownership.
A successful OpenWiki finish writes a receipt with the input commit, package identity, and every wiki file digest.
The completion check preserves project document bodies, provenance, and `openwiki/INSTRUCTIONS.md`.
A no-change submission succeeds at the start commit without a merge.
Other accepted wiki commits proceed through capture, candidate preparation, and merge into `main`.
No review packet or operator decision applies to that merge.
The candidate step checks wiki scope again in a repository outside agent control.
A conflict or failed scope check ends the wiki update without a task allowance charge.

The wiki state is `current`, `behind`, or `update-active`.
`WikiStateOf` uses accepted commit equality and successful update records; the browser displays that result.
The export lane serves the accepted wiki tree, so an unfinished Scribe edit cannot change the viewer.
`wikicomplete_test.go`, `wikimerge_test.go`, and `wikiupdate_test.go` cover these boundaries.

Setup traces use `environment-runs/<operation>/session` and the setup operation identity.
The runner registers native sessions separately from setup reports.
The trace lane can recover a missing registration. Trace failures leave the setup result unchanged.
`resident/src/setup-traces.test.ts` and `mc/verbs/setuptrace_test.go` check that separation.

## Native OpenCode copy lifecycle

`docs/native-opencode-traces.md` defines the native trace contract and its acceptance lanes.
`opencode-copy.ts` validates bounded regular source files and publishes each complete copy by atomic rename.
`opencode-snapshot.ts` reads one committed state, including committed write-ahead log records, and builds a fresh database from transcript tables.
It preserves native session relationships and message identifiers while excluding account tables and unused source pages.
Credential masks apply before publication. Hash records retain prior masks without retaining credential values.

Original databases remain available for native resume.
AgentsView reads flat `opencode-<id>.db` copies under `MC_HOME/agentsview/traces/opencode/`.
Collie reads the inspection mount at `/mc/traces`; `runner/shared/trace-read.ts` provides bounded native-record queries and citations.
Each inspection status separates copy state (ready, pending, delayed, unavailable) from import state.
A failed refresh preserves the last valid copy. A successful native import permits retirement of the converted export into AgentsView trash, preserving annotations.

Copy work runs outside dispatch with a five-second worker deadline, source and transcript size limits, and record and session limits.
The copy, lane, trace-reader, and browser tests cover masks, committed records, recovery, bounded access, and authenticated native links.
Optional acceptance programs in `resident/acceptance/` check the pinned viewer, concurrent container writes, and native resume with synthetic inputs.
