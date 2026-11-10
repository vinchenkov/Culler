---
type: architecture
title: The operator dashboard
description: How the Bun dashboard server mirrors mc verbs over an authenticated loopback API, serves the browser modules and wiki exports, and how its smoke harness proves it.
tags: [dashboard, bun, api, security, smoke]
verified:
  - by: openwiki/0.7.0
    at: 2026-11-10T19:18:31.051Z
sources:
  - id: openwiki-source-4d1d392666be6dfdd7a91a2e
    resource: repo://.github/workflows/release.yml
  - id: openwiki-source-5581b682dfee807415160cbe
    resource: repo://dashboard/PAGES.md
  - id: openwiki-source-ddff79bc537156c26cb2035f
    resource: repo://dashboard/smoke/board-writes.smoke.test.ts
  - id: openwiki-source-1756d5381ff309245b052d21
    resource: repo://dashboard/smoke/conventions.smoke.test.ts
  - id: openwiki-source-63f3e8ddda5125ed93fa2caa
    resource: repo://dashboard/smoke/diagnostic-collie.smoke.test.ts
  - id: openwiki-source-e9e1272cb64a0826b6090ebb
    resource: repo://dashboard/smoke/files-changed.smoke.test.ts
  - id: openwiki-source-6ff7ec04aac5c9f6567d2d84
    resource: repo://dashboard/smoke/harness.ts
  - id: openwiki-source-a7feed67202b644d5c9b515a
    resource: repo://dashboard/smoke/packet-reader.smoke.test.ts
  - id: openwiki-source-2754ae842e4a2d8b685b6077
    resource: repo://dashboard/smoke/review-ledger.smoke.test.ts
  - id: openwiki-source-07f6002de97028cff35d1563
    resource: repo://dashboard/smoke/role-map.smoke.test.ts
  - id: openwiki-source-96321a8fb2110d04d63348e3
    resource: repo://dashboard/smoke/scribe.smoke.test.ts
  - id: openwiki-source-97d75c2035d8d87341f7484d
    resource: repo://dashboard/smoke/setup-journal.smoke.test.ts
  - id: openwiki-source-347bac712a55ec458495b64a
    resource: repo://dashboard/src/agentsview-pages.ts
  - id: openwiki-source-8d31e9741b11f4dcc49fde73
    resource: repo://dashboard/src/agentsview-proxy.ts
  - id: openwiki-source-365606f9a00690187caa1ff1
    resource: repo://dashboard/src/api.ts
  - id: openwiki-source-cce742409a25de60a2b7e3cd
    resource: repo://dashboard/src/hostfile.ts
  - id: openwiki-source-b3867973ecd3305990816c65
    resource: repo://dashboard/src/main.ts
  - id: openwiki-source-123f1d90d8214e8aba060fa6
    resource: repo://dashboard/src/mc.ts
  - id: openwiki-source-aa7b84e47893dc39457a0e72
    resource: repo://dashboard/src/pause.test.ts
  - id: openwiki-source-7b916d9a6889ba022c357211
    resource: repo://dashboard/src/server.test.ts
  - id: openwiki-source-9bacaebabf4730d3f0a79cc9
    resource: repo://dashboard/src/server.ts
  - id: openwiki-source-2ead01a8e29c111bfb46b6f1
    resource: repo://dashboard/src/ui/agentprofiles.js
  - id: openwiki-source-2b9db73e8eaa164d3fba79a2
    resource: repo://dashboard/src/ui/app.js
  - id: openwiki-source-0c84ae704d5461f889682650
    resource: repo://dashboard/src/ui/board.js
  - id: openwiki-source-41b10777200e9da21f4768dc
    resource: repo://dashboard/src/ui/boardwrites.js
  - id: openwiki-source-0f05f7449c5bd71de69d757a
    resource: repo://dashboard/src/ui/changes.js
  - id: openwiki-source-8297d2f420c6d78a10ecd110
    resource: repo://dashboard/src/ui/changeview.js
  - id: openwiki-source-36ab52ea84a21f6efcf0c4bc
    resource: repo://dashboard/src/ui/components.js
  - id: openwiki-source-f8d363d8f4cec85a24e4db38
    resource: repo://dashboard/src/ui/conversation.js
  - id: openwiki-source-63ba3f1bb74de15dcf4fbd72
    resource: repo://dashboard/src/ui/customization.js
  - id: openwiki-source-b72b7ea677ab1e9f8fcc0e50
    resource: repo://dashboard/src/ui/diagnosticchat.js
  - id: openwiki-source-9dc12756d2cc067f897826b6
    resource: repo://dashboard/src/ui/discord.js
  - id: openwiki-source-6d5fa0336e6209c8197f9f84
    resource: repo://dashboard/src/ui/environment.js
  - id: openwiki-source-81c3203ef12b4b513820c84a
    resource: repo://dashboard/src/ui/evaluation.js
  - id: openwiki-source-79689e3354e54aafb6a37054
    resource: repo://dashboard/src/ui/flowmap.js
  - id: openwiki-source-063a86c813476661efd64230
    resource: repo://dashboard/src/ui/flowmap.test.ts
  - id: openwiki-source-927b6a817962ef194108a04b
    resource: repo://dashboard/src/ui/index.html
  - id: openwiki-source-56b812868de34201e0046a3c
    resource: repo://dashboard/src/ui/packetchat.js
  - id: openwiki-source-b69f10da7d235560b52d92b2
    resource: repo://dashboard/src/ui/pipeline.js
  - id: openwiki-source-0e92aff1cd13c69e678bd89a
    resource: repo://dashboard/src/ui/projects.js
  - id: openwiki-source-98f567e407d867deb58a4215
    resource: repo://dashboard/src/ui/review.js
  - id: openwiki-source-e51b5dc1a5c6bbfd524e7ccd
    resource: repo://dashboard/src/ui/review.test.ts
  - id: openwiki-source-b10e7836992abc73d7e0459e
    resource: repo://dashboard/src/ui/scene.js
  - id: openwiki-source-460c27048f01dfd9cb2c51ee
    resource: repo://dashboard/src/ui/setup-journal.js
  - id: openwiki-source-18796b2f3f0781d432f2c984
    resource: repo://dashboard/src/ui/setup-journal.test.ts
  - id: openwiki-source-97e7306380c1609d77e9989e
    resource: repo://dashboard/src/ui/style.css
  - id: openwiki-source-8f16f07389eccb57a739027f
    resource: repo://dashboard/src/ui/systemhealth.js
  - id: openwiki-source-233dd29606c16bd136fa941d
    resource: repo://dashboard/src/ui/taskdialog.js
  - id: openwiki-source-145c0d885072e4246aa745c1
    resource: repo://dashboard/src/ui/taskdialog.test.ts
  - id: openwiki-source-0c51ba8e775e5d76ec1840dd
    resource: repo://dashboard/src/ui/theme.js
  - id: openwiki-source-9ea19154aa59c39cc41bced6
    resource: repo://dashboard/src/ui/theme.test.ts
  - id: openwiki-source-da3362571c72764c13447a98
    resource: repo://dashboard/src/ui/topology.js
  - id: openwiki-source-ec1ab673f7783f0c2272fd8e
    resource: repo://dashboard/src/wiki-server.ts
  - id: openwiki-source-1c02462faa6dc8230a32f788
    resource: repo://mc/cmd/mc/repository_file.go
  - id: openwiki-source-07cdfe29b43b302d6030a5c9
    resource: repo://mc/cmd/mc/systemstatus_setup_test.go
  - id: openwiki-source-d98ef2f07998b9fbb2240456
    resource: repo://mc/cmd/mc/systemstatus.go
  - id: openwiki-source-78f1d76d5a5b9577a60c9016
    resource: repo://mc/deployment/host_release_closure_test.go
  - id: openwiki-source-d2a2a705448c3432ad6c1337
    resource: repo://mc/deployment/host_release.go
  - id: openwiki-source-623998b6f95a9f34af7f3ee5
    resource: repo://mc/repository/read.go
  - id: openwiki-source-a44ab4c2d26db975bde190a0
    resource: repo://mc/verbs/collie.go
  - id: openwiki-source-959768f661d526f65eea9d8d
    resource: repo://mc/verbs/directives/packager.md
  - id: openwiki-source-f1ae658c98ff5e2d177d4b8a
    resource: repo://mc/verbs/packet.go
  - id: openwiki-source-0024d9d1b366186698efea24
    resource: repo://mc/verbs/packetlist_test.go
  - id: openwiki-source-4cf2679db61bdfa9a802d9d2
    resource: repo://mc/verbs/pause_test.go
  - id: openwiki-source-000595f05ff9445f26008057
    resource: repo://mc/verbs/pause.go
  - id: openwiki-source-62f6ea7c43ca0bcf5c15f2cc
    resource: repo://mc/verbs/repositoryfile.go
  - id: openwiki-source-229935765a2017befaf3bc59
    resource: repo://mc/verbs/systemstatus.go
generated: { by: "culler-scribe", at: "2026-11-10T19:18:31.051Z" }
---

# The operator dashboard

The dashboard is the operator's browser interface to Culler. It lives in `dashboard/`. It is a dependency-free Bun process with dashboard and wiki listeners: the dashboard address (default `127.0.0.1:7333`) and the wiki address (default `127.0.0.1:7335`). It holds no durable state and never opens the spine. Every read and write spawns `mc` and returns that command's JSON. See [the mc CLI and verb scope](mc-cli-and-verb-scope.md) for the verbs it calls and [the spine and substrate](spine-and-substrate.md) for why `mc` is the only writer.

## Entry point and configuration

`dashboard/src/main.ts` is wiring only. It reads `--config <path>`, a JSON object parsed by `parseConfig`. `mc_bin` and `auth_token` are required. `bind`, `wiki_bind`, and `wiki_key` are optional; a `wiki_key` must be 64 lowercase hexadecimal characters. `main.ts` resolves `MC_HOME` the same way the resident does (default `~/.culler`), opens the structured log `MC_HOME/logs/dashboard.ndjson` through the shared logger in `runner/shared/log.ts`, and starts both servers. A wiki address that cannot start is logged as `dash.wiki_failed`; the dashboard address keeps running. The process is stateless by design: launchd restarts it on death, and the browser reconnects.

In production the dashboard runs as a launchd unit. Its config lives at `MC_HOME/supervision/dashboard.json`; see [installation and deployment](../operations/install-and-deployment.md).

## The dashboard server

`startDashboardServer` in `dashboard/src/server.ts` builds a `Bun.serve` handler. Its security checks run in a fixed order:

1. Startup fails closed when the auth token is empty. This applies to every bind, loopback included. The reason: an agent container can reach the host's loopback interface, so loopback alone is not a boundary.
2. Each request's `Host` header must name the bind host or a loopback spelling (`127.0.0.1`, `::1`, `localhost`). Anything else gets 403. This blocks DNS rebinding.
3. A present `Origin` header must pass the same hostname check. This check compares hostnames, rather than complete browser origins.
4. Every `/api/` path requires `Authorization: Bearer <token>` before any `mc` spawn; a miss returns 401.

Static assets stay public so the shell page can load and then present the token. The browser receives the token in the URL fragment (`#token=...`), as the smoke harness's `gotoReady` shows. The static table `STATIC_FILES` maps `/` to `ui/index.html`, `/style.css`, the typefaces and license notices in `FONT_FILES` (`ui/fonts/`), each name in `UI_MODULES`, and the Pierre files that `pierreFiles` reads from `ui/pierre/provenance.json`. The shell page carries the policy `frame-src 'self'`. Any other path returns 404, never a listing.

One extra route is not an `mc` verb: `POST /api/projects/choose-folder` runs `osascript` to show a native macOS folder chooser. It allows one open chooser at a time (409 otherwise) and returns `null` on cancel.

## api.ts: a one-to-one mirror of mc verbs

`handleApi` in `dashboard/src/api.ts` maps each route to one `mc` invocation. For example, `GET /api/tasks` runs `mc task list`, `GET /api/projects` runs `mc project list`, and `POST /api/config` runs `mc config save` with the body on stdin. The module has no cache and no interpretation. Grouping and filtering happen in the browser. `verbResponse` turns the result into HTTP: exit 0 returns the JSON with 200; exit 1 or a `usage` error returns 400; `record-conflict` and `operation-conflict` return 409; other failures return 500. When `mc` prints no error envelope, `verbResponse` synthesizes one from stderr.

`dashboard/src/mc.ts` is the single spawn seam. `makeMcRunner` runs `Bun.spawn([mcBin, ...argv])` with a fresh copy of the process environment and optional stdin. It parses stdout as one JSON object or returns `json: null`. Passing the environment through untouched lets the production binary delegate into the helper container and lets the `test_fake_routing` build honor `MC_SPINE`. Structured input and secrets travel on stdin, not in arguments; `mc.test.ts` verifies this.

### Release identity and update recovery

The server checks each authenticated mutation against the deployed release identity through `x-culler-release`.
A different identity returns HTTP 409 and asks the operator to reload the page.
An incomplete update returns HTTP 503 and directs the operator to `install.sh` for recovery.
Read requests remain available. The AgentsView access grant has its own request checks.
`updateBlocksMutations` validates the transaction and maintenance files before it permits changes.
`dashboard/src/server.test.ts` checks stale pages and update recovery restrictions.

### Pause controls

The dashboard exposes Project pipeline pause through dedicated commands:

| Route | Command |
| --- | --- |
| `GET /api/pause` | `mc pause status` |
| `POST /api/pause` | `mc pause` (Pause all) |
| `POST /api/resume` | `mc resume` (Resume all) |
| `POST /api/projects/<id>/pause` or `/resume` | `mc project pause|resume <id>` |

The work happens in `SetEveryProjectPause` (`mc/verbs/pause.go`). Pause all sets every active Project to paused in one transaction. Resume all resumes only paused Projects whose `setup_status` is `ready`, so Projects with incomplete initialization stay paused. Pipeline agents cannot change a pause (`RequireHostScope`). `mc/verbs/pause_test.go` verifies that pause still allows Collie dispatch. In the browser, `pauseAllView` (`dashboard/src/ui/format.js`) counts only non-archived, setup-ready Projects to decide whether the button reads "Pause all" or "Resume all".

### Side seams

- `dashboard/src/resident-nudge.ts` writes one line, the session id, to `MC_HOME/resident.socket` after a Collie write or a packet decision. The nudge is best effort. A failure is logged as `dash.nudge_failed` and the resident's tick timer is the fallback (see [the resident](resident.md)).
- `dashboard/src/hostfile.ts` serves attachment bytes after `mc __attachment-bytes` has verified a path. It opens the leaf with `O_NOFOLLOW` and re-checks through the open descriptor: a regular file, operator-owned, at most 64 MiB. Without that check, a link planted in the agent-writable attachment tree between verification and read could widen what the dashboard serves.

## The wiki server

`dashboard/src/wiki-server.ts` is the second listener. It serves wiki exports from `MC_HOME/wiki` at a separate origin, so exported pages never run at the dashboard origin and never see its token. Every path must start with the wiki key, compared in constant time by `wikiKeyMatches`. A wrong key returns 404. Only `GET` and `HEAD` are accepted. The `Host` header must be a loopback name with the bound port. `currentExport` follows `wiki/<project>/export.json` to a commit-named folder. `rewriteExportPage` points library references at local copies under `dashboard/src/openwiki/`, but only when source and `sha384` integrity match the provenance record. A strict content security policy blocks any remaining network reference. Each such reference is logged once as `dash.wiki_library_remote`. See [OpenWiki and viewers](../integrations/openwiki-and-viewers.md).

## The browser modules

`dashboard/src/ui/` holds plain ES modules with no build step. `app.js` is the root; it imports the page modules (`board.js`, `review.js`, `pipeline.js`, `configs.js`, `logs.js`, `systemhealth.js`, `conversation.js`, and others). Each has a colocated `*.test.ts` unit test where logic warrants one.

### Header status and theme control

`dashboard/src/ui/index.html` places the theme button immediately before the lease badge inside `.header-status`.
Wiki and Traces remain in the navigation's viewer links.
The button and badge retain their element identifiers and `data-testid` hooks.

`dashboard/src/ui/style.css` keeps the button and badge together in a flex row with an 8-pixel gap.
The badge uses the remaining width and can shrink.
The desktop header reserves `calc(12rem + 40px)` for this group.
At widths from 721 through 1024 pixels, the group occupies the upper-right cell, above the viewer links.
At widths of 720 pixels or less, the header wraps and retains both controls in the same status group.

`app.js` connects `theme-button` to `mountTheme` in `theme.js`.
The browser stores the selection under `mc.theme` and uses the light theme when the selection is absent or invalid.
Changes to the system theme leave this choice unchanged.
The button's tooltip names the next theme; `aria-pressed` indicates the dark theme.
`theme.test.ts` checks stored selections, the light default, the initial page theme, and the button state.

### Background picture

`index.html` places a logo button beside the Culler name.
`app.js` connects `mountScene` in `theme.js` to `mountSceneMotion` in `scene.js`.
The button has the accessible name “Background picture”. Its pressed state reports the selected picture state.
The browser stores this choice under `mc.scene` and applies changes from other dashboard tabs.
The initial page script reads both selections before the first paint. Without a picture selection, the page shows only tile lines.
`style.css` uses a theme-specific grid for those lines and preserves their position when the full picture appears.
The shipped `landscape.png` and `landscape-dark.png` show a dark egg, a nest, and an orange chick.
The `.scene-art` element holds the grid, the picture, and the temporary parts that move.
On wide windows, the picture fits the window and sits five viewport-width units left of center at the bottom.
At widths of 720 pixels or less, the picture covers the window and shows the large egg under a denser veil.

A logo click moves each picture part separately.
The large egg, nest, and three eggs arrive in sequence.
A whole egg rolls into place, cracks, and opens before the chick jumps to its place.
To hide the picture, the chick returns to the shell and each part leaves separately.
A click during that motion reverses it from the current position.
After the parts arrive, the full picture replaces them under the same theme veil.
Page loads, theme changes, window size changes, and selections from other tabs apply the selected state without motion.
The system's reduced motion preference also gives an immediate state change.

`scene.js` uses the light and dark `scene-parts.webp` assets for separate picture parts.
`dashboard/src/server.ts` serves the module and both assets.
`mc/deployment/host_release.go` includes them in the host release.
See [Dashboard background picture and motion](../workflows/dashboard-background.md) for the animation lifecycle, image failures, and reversal behavior.

The empty Board uses the `project-welcome-logo` template in `index.html`.
Its inline image uses the theme's ink and the same orange chick as the header logo.
The dark theme gives its eye a separate light fill and its pupil the theme ink color.
The logo chick moves once each 10 seconds.
A pointer over the logo and active picture motion hold the chick in its moved position.
`theme.test.ts`, `scene.test.ts`, and `smoke/theme.smoke.test.ts` define checks for the picture state, motion, and browser behavior.
These are source-defined checks; this wiki update does not execute them or verify the header in a browser.

### Shared Culler blue conventions

`dashboard/src/ui/style.css` owns the shared color roles and component geometry.
The light theme uses porcelain paper (`#f2f3f4`), white body surfaces (`#fdfdfe`), and blue primary actions with white text.
The dark theme uses navy paper, dark blue bodies, and pale blue primary actions with dark text.
Structural headers use blue with white titles in both themes.
The light theme uses navy text (`#172f47`) and a subtle surface (`#eef1f4`).
The background image has a theme-specific paper veil; the egg and chick logo and favicon use the same palette.
Page titles remain on the page background, and Board columns and Pipeline stages retain their stage colors.
Fraunces identifies the logo; Figtree supplies interface text; literal identifiers use the shared monospace font.

`dashboard/PAGES.md` defines the appearance and accessibility requirements.
Text requires contrast of at least 4.5:1 against its actual surface.
Focus indicators and field boundaries require at least 3:1 contrast against their applicable surfaces.
Dialogs and bounded panels use a 16-pixel radius; fields use 9 pixels; buttons use 7 pixels.
Dialog regions share a 24-pixel horizontal inset, reduced to 16 pixels on narrow screens.
Close controls use a cross with the accessible name Close.
They measure 36 pixels square with a precise pointer; coarse-pointer actions have a 44-pixel minimum size.

`components.js` supplies `dialogHeader`, `dialogBody`, `dialogFooter`, `dialogFrame`, and `showDialog`.
The frame places the header, body, and optional footer inside a shared shell.
Only the body scrolls when the dialog exceeds the viewport height.
This keeps its header and action footer available.
The caller owns validation, draft storage, cancellation rules, and focus restoration.
The helper creates no durable state and makes no API request.

The shared frame serves these dialogs:

- Task details and Board task actions.
- Project creation and deletion.
- Agent profiles and role customization.
- Collie model changes, conversation names, and conversation deletion.
- System details and case assessment.

Board task actions refuse closure while a save is busy.
Conversation rename and deletion disable their close controls during the operation.
Project and agent profile renders preserve the active field, selection range, selection direction, and body scroll position.
Their asynchronous responses can therefore refresh content without losing the current draft position.
The server lists `components.js` in `UI_MODULES`; `app.js` imports it; the host release manifest ships it.

Specialized content keeps its required appearance.
Report frames retain a light sheet, logs retain a dark pane, and the Setup Journal approval view retains navy.
Source comparisons and agent artifacts retain their content colors.
OpenWiki and AgentsView own their viewer appearance.
Culler controls around those regions use the shared conventions.

### Trace access appearance

`dashboard/src/agentsview-pages.ts` reads the shipped dashboard stylesheet when the module loads.
It extracts the light and dark tokens, resolves references for a fixed role map, and embeds the resulting palettes.
An absent token or a cyclic token reference raises an error.
The access page uses a blue header, a body surface, and a themed Retry action.
Its inline script reads the local `mc.theme` selection and uses the light theme unless that selection is dark.
The dashboard origin shares the dashboard selection; the proxy origin uses its own selection or the light default.
The isolated page uses a system font fallback when Figtree is unavailable.

The page preserves nonce-protected styles and scripts, escaped destination text, and the Content Security Policy.
Theme selection does not change the grant exchange or expose the AgentsView token.
`agentsview-pages.test.ts` checks palette parity, safe text, nonce protection, security headers, and failure actions.
See [viewer authentication](../integrations/openwiki-and-viewers.md#browser-authentication) for access control.

### Convention checks and retained design

`dashboard/smoke/conventions.smoke.test.ts` checks both themes across dialogs, role details, Environment, System health, and trace access pages.
It checks draft preservation, text selection, header colors, contrast, narrow layouts, modal focus, and the lighter stacked backdrop.
The long-task check uses a 720 × 450 viewport to represent a 1440 × 900 desktop at 200 percent zoom.
This checks the effective layout size, rather than a native browser zoom command.
The model-configuration and Project setup smokes delay responses to check draft, focus, selection, and body-position preservation.
`components.test.ts` checks that the stack marker precedes modal activation and resets when a dialog opens again.
Other updated smokes use the shared body scroll region and Close control.
These descriptions identify test assertions; this wiki update does not execute the browser suite.

The [theme comparison viewer](../../work/dashboard-theme-sets/index.html) is a retained design artifact.
The [conventions document](../../work/dashboard-theme-sets/dashboard-conventions.md) states the design requirements.
The [implementation inventory](../../work/dashboard-theme-sets/implementation-inventory.md) maps components, exceptions, and recorded checks.
Its pass counts describe its recorded implementation review, rather than fresh evidence from this wiki update.
Production appearance comes from the shipped stylesheet and browser modules.

### Dialog backdrops and case assessment

`dashboard/src/ui/style.css` gives classed modal dialogs a shared backdrop with `rgba(var(--scrim-rgb), 0.46)` and a fade animation.
The role customization dialog uses this rule, as does the task dialog.
`showDialog()` marks a new dialog with `data-dialog-stacked="true"` when another native modal is open.
The stylesheet gives that marked dialog a backdrop opacity of `0.16`.
These rules keep an additional dialog from adding the full backdrop opacity again.

`dashboard/src/ui/evaluation.js` appends the Score case dialog to the packet reader and calls the shared `showDialog()` before it requests focus.
The native modal dialog participates in the shared backdrop rules.
The Close control has the tooltip Close and keep draft.
Closure removes the dialog, retains the draft in memory, and returns focus to the opener when available.
The dialog's cancel handler uses the same close function.

`dashboard/smoke/evaluation.smoke.test.ts` checks dialog visibility, assessment submission, immutable assessment revisions, and containment at a 390-pixel viewport width.
These checks describe the test source. This wiki update does not report a browser test execution or a visual backdrop check.
See [product case capture](../tests/performance-and-evaluations.md#product-case-capture) for assessment storage and identity checks.

### The Review packet viewer

The Live queue and Ledger cards show the task number, age or decision details, and task title.
The title appears below the metadata and occupies at most three lines.
Merge state and return details appear below the title.

`PacketList` in `mc/verbs/packet.go` reads `tasks.title` into each packet row.
The dashboard API passes that row to the browser.
`packetView` supplies its title to the card and reader heading.
The quick chat uses the same row title, with the conversation task title as a fallback.
These headings require no separate task read.
The packet row retains its `origin` and `thesis` for report fallback states.
The `thesis` field stores the opening text.
For a Strategist task, that text contains the Packager's statement of the delivered change, followed by the Strategic rationale.
The fallback heading is Strategic rationale, and the browser renders this text as Markdown.
For an operator task, the heading is Thesis, and the browser renders plain text.
The report request state hides this text until the request completes.

`mc/verbs/packetlist_test.go` checks titles for live and decided tasks.
`review.test.ts` checks title propagation and absent title values.
The packet reader smoke checks title placement below metadata, absent card thesis, and title containment in the narrow Ledger queue.
The Ledger smoke checks that excluded task titles stay outside each selected view.


`loadReader` in `dashboard/src/ui/app.js` paints the report area before it requests the selected packet's report. During the read, the area shows a spinner, a "Loading packet…" status, and placeholder lines. The placeholders are hidden from assistive technology. The area sets `aria-busy` to `true` and shows no thesis during this state. The motion stops when the browser requests reduced motion.

`reportArea` in `dashboard/src/ui/review.js` selects the report area's state. A successful read replaces the placeholder with the report and clears `aria-busy`. A failed read shows an error and a Retry button with the thesis. An absent report shows an absence message with the thesis. An expired report shows its expiry date, the thesis, and the criteria. The decision controls stay disabled until the selected packet version's report loads.

`dashboard/src/ui/review.test.ts` checks the state selection. The delayed-response test in `dashboard/smoke/packet-reader.smoke.test.ts` holds the report request open. It checks the placeholder, busy state, absent thesis, and disabled controls before it releases the response. It then checks the report and enabled controls. See [checks and test lanes](../tests/checks-and-test-lanes.md) for the browser test procedure.

### Task details from Review

The Task control opens the shared task dialog over the selected packet.
`app.js` reads `/api/tasks/<id>` before it opens the dialog.
A failed read appears inside the reader. A different packet selection prevents the old response from opening a dialog.
The control stays disabled while the page sends a packet decision.

`taskdialog.js` supplies the description, conditions, facts, and run history for both Board and Review.
A Strategist task shows its Strategic rationale before the Description.
The rationale provides proposal evidence; the Description supplies the task requirements.
An absent rationale displays an explicit absence message. An operator task has only the Description.
`board.js` carries the rationale into the shared task model.
Review opens the dialog without a prior Board read and refreshes its task data with the Review page.
The dialog omits its packet link on Review because the packet already appears below it.
Its run rows show the result, role, age, diagnostics, and available main trace.
Older runs remain in a disclosure. The stylesheet gives the timeline no list numbers.
An origin mark ends the timeline below the first run.
When older runs exist, their list holds that mark.

The facts box shows the run failure and correction allowances with tooltips.
The tooltips explain when each count increases, when it resets, and when the task waits for the operator.
The task history appears after the other task details and has no internal section headings.
The packet reader retains its separate evidence access.

The wide reader places Task beside the previous and next controls.
The narrow reader places Task in the view row beside Packet, Files changed, and Collie.
The narrow reader excludes native dialogs from its inert background, so the task dialog accepts input.
Escape closes the task dialog first and retains the reader and its selected view.
Closure returns focus to Task. Without a dialog, Escape first handles the reason field, then closes a narrow reader.
A reason field with text retains its text.

The reader shows the decision time for a decided packet and Decision Pending for a blocked task.
A packet that awaits a decision has no review state tag or queue age in the reader bar.
The queue card retains its age.

`dashboard/smoke/packet-reader.smoke.test.ts` checks these behaviors at desktop and narrow widths.
It also checks task refresh, focus return, and long path containment.
The viewer smoke checks trace links through the shared task dialog.
The accent contrast, page, file comparison, and packet chat smokes use the same Task entry point.
The conventions smoke checks the retained structural headers.
`taskdialog.test.ts` checks run history, trace links, and stable run hooks.
`taskdialog.test.ts` checks the allowance facts and their tooltips.
`review.test.ts` checks the absent state tag for packets that await a decision.
`theme.test.ts` checks panel header separation against the shared surface color.
These statements describe source assertions. This wiki update does not execute those tests.

`dashboard/smoke/strategic-rationale.smoke.test.ts` checks the rationale in task details, report sections, and fallback text.
`mc/verbs/strategic_rationale_test.go` checks the opening text in packet conversations.
It also checks that an operator task retains its own packet form.

### Report page style

`packetDocument` in `dashboard/src/ui/review.js` supplies inline priority styles on the document root and body.
These styles preserve the light report sheet (`#fdfdfe`), navy text (`#172f47`), light color scheme, and base typography when a packet supplies document styles.
The body uses 14-pixel Figtree text, a 1.62 line height, zero margin, and 20-by-24-pixel padding.
The base stylesheet wraps long text and limits image, video, and SVG widths.

Report styles can arrange diagrams and other visual content within the page.
The frame preserves report content and section order; the Packager directive defines the default sections.
Project instructions can replace the default structure.
A Strategist task retains Strategic rationale as its first section.
See [the task pipeline](../workflows/task-pipeline.md#packet-content-and-project-instructions) for the content requirements.

`reportDocument` applies the link rules to the composed document until its serialization is stable.
The sandbox and Content Security Policy still restrict scripts and external resources.
The report-frame smoke checks competing document styles at desktop and narrow widths while preserving a local diagram grid.
See [the report style checks](../tests/checks-and-test-lanes.md#report-page-style-checks) for the assertions.

### Packet quick chat

The Review reader offers a Collie conversation for its selected packet in both Live queue and Ledger views.
`packetchat.js` owns the transcript, composer, draft text, and refresh requests.
The spine owns the conversation identity and message history.
The empty chat describes the packet content by origin: Strategic rationale for a Strategist task, and thesis for an operator task.
`BuildPacketContext` supplies the same opening text and heading to Collie.
`app.js` connects the chat to packet selection and reader layout.

The wide layout starts the decision bar with the “Ask Collie” control in the selected theme’s primary action colors.
It opens and closes the chat column for Live queue and Ledger packets.
The reader bar holds the Task control.
Closing the chat returns focus to Ask Collie.
The narrow layout hides Ask Collie and hides an empty decision bar.

The wide layout defaults to a 400-pixel chat column and uses a 200-pixel queue while the chat is open.
The queue hides when the selected chat width would leave the reader less than 640 pixels.
The narrow layout offers a Collie view beside Packet and Files changed.
The browser preserves the open state across reloads. Unsent drafts remain in memory for each packet.

`createPacketChatResize` owns the divider and its width preference in `packetchat.js`.
The divider changes the column width without rebuilding the packet or conversation.
A leftward drag expands Collie. The arrow keys change its width by 24 pixels.
Home selects 320 pixels. End selects the maximum that leaves a 400-pixel reader.
A double-click restores the 400-pixel default, subject to the available width.
The divider exposes its current width and limits through an accessible separator.

The browser stores the preferred width under `mc.packet-collie-width` across packets, pages, and reloads.
A `ResizeObserver` limits the visible width when the window shrinks and preserves the preference for a larger window.
If browser storage fails, the preference lasts until the page reloads.
The narrow layout hides the divider and uses the Collie view.
During a drag, the stylesheet disables text selection and iframe pointer events.
Pointer release, cancellation, capture loss, or window blur ends the drag and saves the preference.

Opening the composer prepares the current conversation through `POST /api/sessions` with the packet and version identifiers.
Selecting another packet alone creates no conversation.
A failed send restores its text. A packet replacement of the same task preserves that text and its error.
The visible chat refreshes every second during an owed reply and every ten seconds otherwise.
A closed chat stops its refresh requests.

New conversation ends the current conversation and creates another for the same packet.
Open in Collie selects the same conversation on the Collie page.
After a packet revision, an empty chat can link to the immediately preceding packet's conversation with messages.
An archived Project permits history display but disables new messages.
The Collie page hides empty packet conversations and disables their model control.
The Review quick chat provides the model control for packet conversations.
Its header shows the model and harness beside Collie, with the task title on a second row.

The control lists enabled agent profiles and sets the Collie route for new conversations.
A different selection starts a new packet conversation when a current conversation exists.
A conversation with messages requires confirmation; its history remains in the Collie session list.
With no current conversation, selection changes the route without creating a conversation.
The control is unavailable during preparation, send, an owed reply, or replacement, and for archived Projects.
A failed route update preserves the current model.
If the route changes but conversation creation fails, the error explains that new conversations use the selected model and offers Retry.

The API forwards packet and version identifiers to the Collie start command.
It stores no conversation state itself.
`packetchat.test.ts`, `api.test.ts`, and `packet-chat.smoke.test.ts` cover these boundaries.
The queue unit test checks the 1264-pixel panel boundary that leaves a 640-pixel reader.
The packet chat smoke checks Ask Collie placement, focus return, compact width, model selection, and retained history.
The packet reader smoke checks that Ledger filters fit the 200-pixel queue.
These statements describe test coverage; this wiki update does not report a fresh browser test execution.
See [packet conversations](../workflows/packet-conversations.md) for context, artifact access, and decision authority.

### Collie panel headers

The Collie session and conversation headers use the shared blue panel color and white text.
The New conversation button uses the inverse colors.
The stylesheet supplies visible focus outlines and a separate background for a disabled model control.
These rules appear under `#collie-pane` in `dashboard/src/ui/style.css`.

### Refreshes and visible errors

`app.js` counts each displayed page error.
A successful periodic read clears the error only when that count and the page scope remain unchanged.
An older read therefore preserves an error that appears after it starts.
After a Board write, a refresh preserves a task dialog that the operator closed during that refresh.
`dashboard/smoke/board-writes.smoke.test.ts` covers Board write flows.
The refresh guarantees above come from source inspection.

### Stats on the Board, Review, and Configs pages

The shared `stat` helper in `dashboard/src/ui/app.js` puts the value before a caption. The value uses larger, heavier type. The Board, Review, and Configs pages use sentence-case captions. The Board also uses the same labels while task data loads.

`boardStats` in `dashboard/src/ui/board.js` counts live tasks and held leases for the selected Project. `boardStatItems` shows these Board stats in order:

- **Blocked tasks** counts blocked tasks that need an operator decision. A positive count gets the alert treatment.
- **Agent runs** counts active runs.
- **Tasks** counts live tasks.
- **Stalled** counts stalled tasks.
- **Expedited** counts expedited tasks. This stat appears only when its count is positive.

Blocked tasks and Agent runs lead the row with more weight. A zero count recedes. The Review page uses **Awaiting decision** for its separate packet count. Its Review slots stat keeps the alert treatment when capacity is exceeded. See `dashboard/PAGES.md` for the page contract and [checks and test lanes](../tests/checks-and-test-lanes.md) for the dashboard checks.

**The three-edit rule.** A new browser-loaded module needs three edits:

1. Its name in `UI_MODULES` in `dashboard/src/server.ts`. A missing entry 404s the import and breaks the whole `app.js` module graph. The smokes catch this; unit tests do not.
2. Its path in `hostReleaseFiles` in `mc/deployment/host_release.go`. Production serves only manifest files.
3. The `import` in `app.js`.

Non-JS assets must be listed in the manifest by hand. `TestHostReleaseManifestShipsNothingUnreachable` in `mc/deployment/host_release_closure_test.go` fails when the dashboard serves a file the manifest does not ship, or the manifest ships a file nothing imports or serves. Run `./mc/check.sh` after adding any asset.

### The Pierre diff viewer

The Review page's Files changed view uses Pierre Diffs and Pierre Trees. The view shows the exact Git changes, independent of the Packager's explanation. It uses local pinned assets with license notices and requires no network access. `dashboard/pierre/` holds the build inputs: `package.json` pins `@pierre/diffs` 1.5.1 and `@pierre/trees` 1.0.0-beta.6. `build.ts` bundles a subset of Shiki grammars and two themes into `dashboard/src/ui/pierre/`. It writes `provenance.json` with each file's SHA-256 and rewrites the Pierre block of `host_release.go`. `dashboard/src/pierre.test.ts` checks that each module matches its recorded digest and that the record matches the locked versions.

The Files changed view places its file tree control and diff layout controls in the reader view row. The file tree and diff start directly below that row. `changeview.js` shows the controls while the view loads or shows files. The controls disappear when another reader view opens or the comparison has no files. `index.html` defines the controls and the file column; `style.css` places the columns beside each other on wide screens and stacks them on narrow screens.

The file column shows the file count and line totals above the tree. It shows the comparison commits below the tree. When the view shows a state message instead of files, it places the commits below that message. The tree control hides the column, and the diff fills the available width. `changeview.js` stores the choice under `mc.changes-tree`, so the browser keeps it across packets and reloads. The previous and next controls still move through the file order when the tree is hidden.

The diff shows three unmodified lines around each Git hunk. Pierre offers an expand control for other unmodified regions in a modified, renamed, or copied text file. The view keeps the recorded patch as the source of the exact Git hunks. It reads the two complete file versions only when the operator first expands a region. `changeview.js` keeps that read for later expansions of the shown file.

The browser requests `/api/packets/<task>/changes/files` with the current packet, version, and file index. `api.ts` checks the packet and its recorded inventory, then finds the Project repository. It reads the old path at `from_commit` and the current path at `to_commit` through the host-only `mc __repository-file` command. The command accepts an exact commit and a relative path. `mc/repository/read.go` permits only regular files. `mc/verbs/repositoryfile.go` limits each file to 1 MiB and requires UTF-8 text.

An ineligible file has no expand control. A failed repository read places its cause below the file header. The next expansion retries the read. `dashboard/src/api.test.ts` checks the route and an ineligible file. The Go tests check exact commits, regular files, size limits, text validation, and host scope. `dashboard/smoke/files-changed.smoke.test.ts` checks the expand control, a failed read, a retry, and the unmodified lines from the two commits.

`dashboard/smoke/files-changed.smoke.test.ts` checks the control positions, the diff's position below the view row, and the tree control at a narrow width. It also checks the wider diff, file navigation, and the saved tree choice. These statements describe test source; this wiki update does not execute the browser suite. See [checks and test lanes](../tests/checks-and-test-lanes.md) for its procedure.

## Page set and configuration docs

`dashboard/PAGES.md` is the authoritative page set: six main pages plus Project Vision and Environment pages, with per-page done-when lists. `dashboard/CONFIGURATION.md` audits the configuration keys. It names which keys the Configs page edits, which need setup or host activation, and which the model-configuration flow owns.

## Performance

`dashboard/performance/latency.test.ts` drives the smoke harness with injected delay. It records time-to-content and layout shift. `dashboard/performance/RESULTS.md` reports a controlled comparison. In that comparison the initial Board median fell from 266 ms to 153 ms after request sharing and a cache of at most ten seconds in the browser.

## Tests

- `dashboard/check.sh` runs `bun test src/`. These are unit tests only, with no browser and no Docker. It is part of the fast suite.
- `dashboard/smoke.sh` runs the browser suite locally and in the required `dashboard-browser` continuous integration job. It builds a scratch `mc` with `test_fake_routing,test_scratch_spine`, unless `MC_SMOKE_BIN` supplies one. Its report gate rejects failures, skips, zero tests, and absent suite files in a default run.
- `dashboard/smoke/harness.ts` provides `bootSmoke()` and `teardown()`. `bootSmoke` refuses to run without an absolute `MC_SMOKE_BIN`. It clears inherited `MC_*` variables, onboards a scratch home through real `mc onboard` commands, and starts a real dashboard, a wiki server, and a fake AgentsView. Every smoke file shares one Chromium per Bun process, because a second launch wedges Playwright under Bun. Never launch a separate browser; never run smoke files directly.
- `bootSmoke()` checks the byte length of the scratch home's `discord.socket` path before it creates the home. It rejects paths above 103 bytes and asks for a shorter `MC_SMOKE_ROOT`. The CI job sets that root below `$RUNNER_TEMP/smoke`.
- The smokes locate elements through `data-testid` hooks. Rename a hook only together with the smoke that uses it.

See [checks and test lanes](../tests/checks-and-test-lanes.md) for how these lanes fit the suite.

## Project setup, environment, and deletion

The Board shows initial setup in a full-page Setup Journal.
`setup-journal.js` derives each entry from retained operations, reports, and image decisions.
A successful agent process alone does not prove that preparation completed.
The journal distinguishes a complete report from source acceptance and preserves history after development starts.
The shared Markdown viewer renders report details. Literal file views show exact Dockerfile and build contents.
The journal permits approval only for the current eligible revision.
Ask Collie for the current setup failure appears beside the setup controls below the transcript.
Earlier failure messages remain in the transcript without duplicate action controls.
`setup-journal.test.ts` checks these distinctions and rejects stale decisions.

`journalEntries` keeps one row per milestone at its first position in the stream.
Each row has a stable key and shows the latest state from the retained operations.
A later attempt resets its milestone to Started until that attempt supplies completion evidence.
Earlier operation messages remain visible.
A complete report can finish Prepare Project environment while Save Project files remains Started.
Ready for development requires successful initial source acceptance and a ready environment.
An environment marked ready without retained initial acceptance shows Evidence unavailable.

The Suggested setup card explains the Project image and shows its Dockerfile and retained build files below the progress stream.
The Setup script section shows the command that each agent container executes before the agent starts.
The file viewers compare the retained configuration with the proposal. Approval submits the displayed revision.
The Environment page provides the broader dependency configuration controls.
Its header places **Setup history** beside **Ask agent to configure**.
The action appears when the Project has a setup identifier and either completed initial acceptance or selected an environment revision.
`setupHistoryAvailable` in `setup-journal.js` owns this rule; `environment.js` applies it after each status read.
The header actions wrap at narrow widths, and the stylesheet preserves the hidden state of unavailable history.

`app.js` opens the Board, selects that Project, and opens its Setup Journal when the operator selects **Setup history**.
It scrolls to the top, focuses the journal heading, and requests a refresh.
**Open Environment** returns to the same Project's Environment page.
A reload of a completed Project opens the normal Board.
The Board heading contains the task creation action; the Environment page provides access to retained setup history.

The milestone tests cover stable identity through retries and completion, plus the order of rows and operation messages.
`dashboard/smoke/setup-journal.smoke.test.ts` checks the visible milestones before and after acceptance and after the operator opens setup history through the Environment page.
It also checks the return to Environment and the normal Board after reload.
`setup-journal.test.ts` checks history availability for completed setup, a selected revision, and an absent setup identifier.
The race smokes retain checks for late Project responses, unavailable reads, and absent acceptance evidence.
These descriptions come from source inspection; this wiki update does not report a fresh test execution.

The Environment page owns later configuration drafts, image decisions, and package cache controls.
It reports Project image size, Project home size, and disposable `/cache` size separately.
The Clear /cache action preserves the Project home and source files.
The button is disabled during an operation, for archived Projects, and before an image revision is selected.
`dashboard/src/ui/environment.js` renders these controls from the environment response.
See [Project environments](../workflows/project-environments.md#runtime-and-per-run-dependencies) for storage ownership and preparation.
The Project menu also supports deletion through `projectdelete.js` and the corresponding API routes.
Deletion first stops the Project's containers. The backend preserves the source repository.
See [Project environments](../workflows/project-environments.md) for setup limits, source acceptance, and deletion boundaries.

The command observer in `dashboard/src/mc.ts` measures completion through process exit, closed output streams, and result parse.
Observer errors preserve the command result. This measurement supports the [performance experiments](../tests/performance-and-evaluations.md).

## Role map and trace access

The Pipeline page uses `flowmap.js` and `topology.js` to show task roles, current runs, and task counts.
The task chain leads from Strategist proposals through the Packager to operator review.
The Arbiter card states its purpose as “Chooses from proposed tasks”.
The Scribe appears outside that chain because its wiki update has no task.
The map distinguishes launch pending, a current heartbeat, and a stopped heartbeat.
`flowmap.test.ts` and `role-map.smoke.test.ts` check the map and its disclosures.

The Project wiki section sits beside the current task run banner, above the task path.
At viewport widths of 1100 pixels or less, it follows the banner.
Activity follows the task path directly.
Operator decisions and diagnostics appear in the persistent notice area.
`paintFlowmap` renders the run banner, wiki section, task path, and selected role detail.
`flowmapView` supplies role states, task counts, current runs, pause state, and wiki state.
`flowWikiNode` shows the selected Project's wiki state and the Scribe's active or last run.
Before the first read, the section explains the Scribe schedule.
A failed wiki state request produces an explicit unavailable message in that section.
The wiki state text reserves at least two lines to reduce height changes.
A failed wiki update exposes its cause in recent runs.

Pipeline and Configs role details open in centered native modal dialogs.
The dialog fits the viewport and preserves the page layout.
Close, Escape, and a click outside the dialog close it and return focus to the selected role.
The native modal keeps keyboard focus inside the dialog.
The Scribe detail uses the same title, state, description, Agent profile row, and Custom instructions row as task roles.
The profile applies to all Projects. Custom instructions apply to the selected Project.
`createRoleDialog` in `app.js` appends each dialog to the document body.
`updateRoleDialog` preserves focus across polls and leaves the content intact while a child modal is open.
It keeps the latest role content while the child modal is open and applies that content when the child closes.
This lets the role dialog show a new Agent profile assignment before the next poll.
The role dialog permits internal scroll when its content exceeds the viewport height.

The browser identifies the Strategist as `strategist`.
`customization.js` maps that identity to the stored `strategist(propose)` identifier for custom instructions.
Task role numbers use single digits and the shared Figtree font stack.
The current run card places a pale egg outline behind its content through a CSS pseudo-element.
The outline ignores pointer events, so it cannot intercept controls.

Each role card egg uses its stage color: proposed, seeded, worked, verified, or packaged.
The flat egg has a solid fill and a thin outline.
An inactive egg mixes its stage color with the surface color.
An active egg uses the full stage color and a faint four-pixel halo.
The active treatment requires `running` without `starting` or `stale`.
At widths of 600 pixels or less, the stylesheet hides role eggs.
`flowmapView` preserves the topology's `starting` flag, and `roleCard` adds the corresponding CSS class.
Only a `running` card without `starting` or `stale` animates its egg.
The `egg-rattle` animation repeats every 1.8 seconds, with small horizontal movements and rotations around the egg's lower center.
Launch-pending and stale cards keep their eggs still.
The global `prefers-reduced-motion: reduce` rule disables this animation.
The egg is decorative and hidden from assistive technology; the card's state text remains visible.

For a role with several active runs, `topology.js` sets `starting` only when every run awaits process start.
It sets `stale` when any held run has a stopped heartbeat.
These aggregate flags also control the egg animation.
`flowmap.test.ts` checks launch-pending, active, and stopped-heartbeat labels, including a role with mixed launch states.
The animation description comes from the CSS rules; these tests do not establish its visual appearance.

The role map smoke checks centered dialogs at 1440, 1000, and 390 pixels in both themes.
It checks that Activity keeps its position while role details open and after they close.
The Configs smoke checks the table position and focus return after Escape and Close.
The smoke also captures role egg screenshots for each theme and width.
The Scribe smoke checks the separate section, shared detail parts, Project-specific state, and unavailable state.
It also checks that a new Agent profile appears when the profile dialog closes and that focus returns to the profile control.
These are source-defined checks; this wiki update does not report their execution.

Trace links open a dashboard access page.
The authenticated dashboard grants access to a separate proxy origin through `agentsview-proxy.ts`.
The proxy keeps the AgentsView token on the host.
See [viewer authentication](../integrations/openwiki-and-viewers.md) for the grant and session boundaries.

## Verifier correction details

`board.js` passes the correction count and recorded report into the task dialog model.
The task dialog shows the count of unsuccessful Verifier corrections in its facts box.
An open correction adds no separate section, problem text, or report control to the dialog.
When `allowance_condition` equals `verifier`, the correction limit alert displays the report.
The Pipeline task path keeps its run states and task counts during the correction.

`pipeline.js` supplies the run rows for Pipeline activity and task history.
A routine correction reads **Requested a correction** and uses the neutral `stone` tone.
A run with a blocked, infrastructure failure, or reaped outcome retains its `rust` alert tone.
A recognized end reason takes precedence over the correction result text.
The historical correction result remains after the open correction closes.

The source defines these checks:

- `flowmap.test.ts` checks task counts and the absence of correction details in the task path.
- `taskdialog.test.ts` checks the absent correction section, the correction count, and one report control at the correction limit.
- `pipeline.test.ts` checks neutral correction results and the alert tone for a network failure.
- `role-map.smoke.test.ts` checks the absent problem text, retained history, and task dialog state chip contrast in both themes.

These checks describe test coverage. This wiki update does not report test execution.


## Diagnostic conversations

**Ask Collie** opens an ordinary conversation and submits the selected issue for explanation and a proposed action.
These views provide the action:

- System notices, issue dialogs, and health conditions that require attention.
- Deployment failures and unavailable resident services.
- Task blocks, stalls, stale heartbeats, connection restrictions, and Project infrastructure issues.
- Failed Setup Journal operations and Environment conditions that require repair.
- Diagnostics for the selected run.

`dashboard/src/ui/diagnosticchat.js` constructs the message from an explicit field list.
The message includes record identifiers, a summary, state, impact, recorded action, and observation time when available.
The summary permits 1,200 characters before a labeled excerpt marker.
References preserve task, Project, run, connection, diagnostic, and environment operation identities when supplied.
Full logs and reports stay outside the initial message.
An unavailable current system status identifies the evidence as the last known status.

`createDiagnosticChat` creates the ordinary session through `POST /api/sessions` with an empty object.
It then sends the message through the session's send endpoint.
`dashboard/src/api.ts` forwards these requests to the Collie commands; the spine owns the durable session and message records.
Concurrent requests with the same message share one pending operation in browser memory.
After an uncertain send response, a retry reads fresh history before it sends again.
A matching inbound message proves that the earlier send succeeded.
Unavailable history preserves uncertainty and prevents another send until a reliable read succeeds.
After successful completion, a later request creates a fresh conversation.
This retry state lasts within the current browser module instance.

The button shows progress, disables duplicate clicks, and retains an error with a retry action.
`app.js` selects the new conversation and closes issue dialogs only after successful submission.
The prior conversation and composer draft remain available.
The CSS permits the action and error text to wrap at narrow widths.

The module appears in the server's `UI_MODULES`, the `app.js` imports, and `mc/deployment/host_release.go`.
These entries make the browser import available in a deployed release.
`diagnosticchat.test.ts` covers evidence limits, relevant identifiers, concurrent clicks, and uncertain-response retries.
`diagnostic-collie.smoke.test.ts` covers task, connection, environment, setup, and run entry points through the browser and scratch spine.
It also checks draft preservation, one message after a lost response, and an unchanged task block.
These tests establish their stated assertions; this wiki update does not execute them.
See [Collie diagnostic authority](../concepts/collie.md#diagnostic-context-and-authority) for permitted reads and explicit confirmation.

## Resolve decision

The task dialog offers no Require decision control. A task becomes Decision Pending through the applicable report or condition.
For a packaged task, the dialog omits Edit title and priority. Its other controls follow the task's decision and conditions.
`boardRecordControls` in `dashboard/src/ui/boardwrites.js` selects these controls. The Board write smoke checks both omissions.

The Board selects the resolution form from `blocked_kind`.
For an operator decision report (`report`), Resolve decision shows the agent report, editable description, and reason field.
Save requirements requires both a changed description and a nonempty reason.
The form also offers Cancel task.
The description holds every requirement; the reason explains the change.

`resolveAction` in `dashboard/src/ui/boardwrites.js` selects this form.
`boardWritePayload` sends its save as the existing `revise` action through the Board API.
`mc/verbs/board.go` applies the same requirements operation for the dashboard and Collie.
An active run or repository operation delays application, so Decision Pending remains until the revision applies.
The Worker then continues from retained task work.

Other condition types use their applicable controls, including a reason-only Resolve decision form for an operator block.
A correction allowance limit requires its specific allowance action.
`boardwrites.test.ts` checks form selection, save rules, and payloads.
`dashboard/smoke/board-writes.smoke.test.ts` exercises report resolution and operator block resolution against a scratch spine.
See [Decision Pending and requirements revisions](../workflows/decision-resolution.md) for the complete condition table and commit rules.

## Discord configuration

**Configs → Discord** controls one optional connection for the installation.
The section appears after the first Project completes initial source acceptance, or when an existing connection enables access.
The spine retains availability across Project deletion and disconnect.
An unavailable Discord address returns to System without a setup prompt.

The sidebar orders System, Agents, Projects, Discord, and Advanced settings.
Local outline icons identify each section while visible labels and keyboard targets remain available.
`app.js` retains the selected section during the page session and handles section addresses through the router.

`discord.js` implements Connect bot, Choose channel, and Confirm account.
Choose channel has one Refresh list control for server and channel discovery.
It keeps the selected channel when that channel remains available.
Confirm account shows the bare code and provides a Copy code control.
The browser sends the token through the authenticated API and clears the field after host acceptance.
Browser session storage keeps the opaque draft identifier and navigation values, without the token or confirmation code.
The resident owns temporary credentials and account confirmation.
Explicit account approval activates the connection.

`GET /api/discord` maps to the host configuration read.
The POST action allowlist in `dashboard/src/api.ts` forwards each request body on standard input to the corresponding host action.
The server and host release manifest include `discord.js`; `app.js` imports it.
Advanced settings omits the raw Discord configuration.

The connection card distinguishes Connected, Needs attention, and Status unavailable.
A health timestamp older than two minutes produces Status unavailable and shows the last check time.
Check connection reads actual access and connector health without a test message.
Send test message reports delivery only after the host request succeeds.
Alert controls preserve dashboard routes, and failed saves restore the confirmed snapshot.
The setup lists Create Public Threads and Send Messages in Threads among the bot permissions.
The channel step explains that each channel message starts a conversation in a new thread.
The connection card directs the operator to continue that conversation in its thread or in the dashboard.
The dashboard shows sessions from both surfaces, while dashboard sessions have no automatic Discord thread.
After a connection check, the card refreshes its status and reports when the connection is ready for a test message.
See [Discord setup and delivery](../integrations/discord.md) for persistence, replacement, disconnect, and verification limits.

## Agent connections

System health excludes a connection condition from its issues when no role uses that connection.
The unused connection remains available in the closed disclosure with connected entries.
`connectionGroups` and `systemIssues` in `dashboard/src/ui/systemhealth.js` apply this rule.
The rule affects browser presentation; the system status still supplies the connection records.

Connection notices and detail headings show the profile name, with the model and then “Agent profile” as fallbacks.
The facts list retains the profile identifier for diagnostics.
`systemhealth.js` applies the same name selection to recovery notices and accessible control labels.
`mc/verbs/systemstatus.go` supplies the configured name with the connection condition.

A request failure can advance the connection revision while its run or Collie reply attempt remains active.
System status retains that active attempt when the current diagnostic identifies it.
This lets the detail view distinguish recovery from a process that still runs.
`mc/verbs/networkhold_test.go` checks both task runs and Collie reply attempts after a network timeout.

`agentprofiles.js` first offers the catalog providers for the Codex and Claude Code harnesses.
**Use another provider** follows those choices, then **Use an existing connection**.
Each provider with a subscription integration offers sign-in before its API key form.
The catalog supplies provider names, and the host configuration supplies the integration templates.

**Use another provider** opens a search limited to providers that OpenCode runs.
An empty query lists all those providers.
Name matches precede identifier matches, which precede address matches.
The search excludes the common `opencode:` prefix from identifier matches.
A checked radio button and a selection mark identify the selected provider.
A summary above the concealed key field keeps that selection visible when another query hides its result row.
The results list has its own scroll position, which survives a render.

The form derives a connection name from the provider and access method.
It can reuse a disconnected connection with that name, the same kind, and the same catalog entry.
Reuse also requires that no enabled profile uses the connection.
Otherwise, the form selects the next free numeric suffix for an occupied name.
The operator can edit the suggested agent profile name during model configuration.

A new connection request contains its name, catalog revision, submission identity, and integration template.
The host saves the connection before the panel starts authorization.
The model review requires a model test result before Save becomes available.
Several profiles can use one connection.
Each profile can select an effort level offered by its model, or use the harness default.
The model list marks whether it knows the model's supported levels.
When the list has no support data, the selector shows the harness levels and the model test checks the choice.
Changing the model or connection clears the draft effort.
The profile list, assignment choices, and review show the selected effort.
An unavailable saved level stays visible, but the operator must select an available level before a model test.
The page explains that higher effort can use more time and tokens.
The role assignment dialog offers an Edit action for each enabled profile.
The Edit action opens that profile's configuration form within the dialog.
The assignment applies to all Projects, and the next run uses the selected profile.

`dashboard/src/api.ts` forwards profile reads and operations to the host commands.
Operation requests travel as JSON on standard input.
The browser retains the existing focus, text selection, and body position when the dialog renders again.
Terminal setup records successful connection and model tests through `mc/cmd/mc/systemstatus.go`.
The dashboard uses those records for verification status after setup.
`mc/cmd/mc/systemstatus_setup_test.go` checks that the tested connection and profile appear ready.
The browser tests in `systemhealth.test.ts` check unused connection groups and issue counts.
See [Agent connections and access renewal](../operations/agent-connections.md) for configuration validation, credential storage, and runner behavior.


## Shared proposal issues

One unresolved proposal decision appears as one issue across the notice, issue dialog, and System health.
The Board still counts each blocked task. Several tasks can await the same decision.
Proposal cards show **Awaits shared proposal decision**.
The task dialog offers **View shared proposal decision** when the corresponding system condition is available.
The issue and detail views link to each affected proposal.
The detail and resolution dialogs retain the complete question.

`mc/verbs/systemstatus.go` supplies the decision identity and its non-archived affected tasks, including each title and current block ownership.
Ownership requires the recorded association, an active proposal block, and a reason equal to the decision question.
Identical question text alone cannot establish ownership.
Task conditions retain their separate block decision identity and stall state.
The server returns the individual conditions; the browser selects the visible issues.

`systemIssues` in `dashboard/src/ui/systemhealth.js` excludes a task condition only when a shared decision owns its proposal block.
The match includes the Project, task, and decision identifiers.
A stall or allowance condition keeps the task issue visible.
Missing ownership evidence also keeps the task issue visible.
This prevents a shared decision from hiding a separate repair action.
`app.js` uses this selection for the issue dialog and System health; the notice uses the same function.
`affectedProposalNodes` supplies the proposal links, with list spacing from `style.css`.

`mc/verbs/systemstatus_test.go` checks existing blocks, identical text, replaced reasons, replaced kinds, stalls, archived proposals, and resolved decisions.
`systemhealth.test.ts` checks identity boundaries, absent ownership, separate conditions, and partial responses.
`dashboard/smoke/proposal-review.smoke.test.ts` checks one shared issue, complete text, proposal links, focus preservation, and narrow containment.
It also checks that three blocked tasks can produce two issues and that resolution preserves an independent task block.
These statements describe test source; this wiki update does not execute those tests.
See [decision resolution](../workflows/decision-resolution.md#shared-proposal-decisions) for the operator controls and state transition.
