---
type: workflow
title: Packet conversations from Review to Collie
description: The page traces packet conversations through the dashboard, spine, artifact mounts, and Collie runner.
tags: [collie, review, artifacts, security]
sources:
  - id: openwiki-source-85db7682a784da205fd6cd4c
    resource: repo://dashboard/smoke/packet-chat.smoke.test.ts
  - id: openwiki-source-2b9db73e8eaa164d3fba79a2
    resource: repo://dashboard/src/ui/app.js
  - id: openwiki-source-f8d363d8f4cec85a24e4db38
    resource: repo://dashboard/src/ui/conversation.js
  - id: openwiki-source-56b812868de34201e0046a3c
    resource: repo://dashboard/src/ui/packetchat.js
  - id: openwiki-source-b5018534042f60dde3347c5a
    resource: repo://mc/e2e/collie_packet_artifacts_test.go
  - id: openwiki-source-e876d4f934fe1b8e2de3721c
    resource: repo://mc/substrate/schema.sql
  - id: openwiki-source-9efb4b1ddc2ed1c022be33e7
    resource: repo://mc/verbs/directives/collie.md
  - id: openwiki-source-c2dfdb5eaae48c46003c3dd4
    resource: repo://mc/verbs/collie.go
  - id: openwiki-source-5ff861069017b6a0714fcdda
    resource: repo://mc/verbs/colliepacket_test.go
  - id: openwiki-source-f1ae658c98ff5e2d177d4b8a
    resource: repo://mc/verbs/packet.go
  - id: openwiki-source-e3610626a23cdc8d1f3a1018
    resource: repo://mc/verbs/packetcontext_test.go
  - id: openwiki-source-e5d28e2540a852102e190208
    resource: repo://mc/verbs/packetcontext.go
  - id: openwiki-source-b2183a36b08ae9d3254fe2ae
    resource: repo://mc/verbs/projectdelete.go
  - id: openwiki-source-68190b2710cd43b378a3c4b1
    resource: repo://mc/verbs/skills/collie/mc-collie/SKILL.md
  - id: openwiki-source-e7572dd15953464a4a2391cf
    resource: repo://mc/verbs/taskartifacts.go
  - id: openwiki-source-e028a0037f071b894557c29d
    resource: repo://resident/src/effects.ts
  - id: openwiki-source-46e96222197b6f4505759147
    resource: repo://runner/collie-runner/main.ts
generated: { by: "culler-scribe", at: "2026-10-29T16:53:36.977Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-05T04:07:09.592Z
---

# Packet conversations from Review to Collie

A packet conversation lets the operator discuss one review packet from the Review page or the Collie page.
`mc` owns the conversation identity, messages, and packet records in the spine.
The dashboard displays those records. The resident starts the container, and the Collie runner supplies the model input.

## From the reader to a session

The Review page connects its selected packet to `createPacketChat` in `dashboard/src/ui/packetchat.js`.
Focus in the composer prepares the session through the sessions API with packet and version identifiers.
Sending also prepares the session before it submits the message.
A move to another packet only reads its conversation; it starts no container.

`dashboard/src/api.ts` forwards the preparation to the Collie start command.
`preparePacketConversation` in `mc/verbs/collie.go` checks the task's current review identifier and packet version inside a transaction.
It returns the active session, resumes an ended session, or creates a session when none exists.
Concurrent preparations therefore share one current conversation.
An outdated packet or version returns a record conflict.

The schema stores the packet identifier in `collie_sessions.packet_id` and prevents changes to it.
A session can reference a packet or a Vision Project, but cannot reference both.
The newest session for a packet is current unless the operator deleted it.
New conversation ends the current session and creates a new one for the same packet.
The Collie list returns the packet identity and whether each conversation is current.

## Adjust the Collie column

The wide Review layout places a divider between the packet reader and Collie.
A leftward drag expands Collie immediately while the reader gives up the same space.
The divider also accepts these keyboard controls:

- Left expands the column by 24 pixels.
- Right reduces the column by 24 pixels.
- Home selects the 320-pixel minimum.
- End selects the maximum width that leaves a 400-pixel reader.

A double-click restores the 400-pixel default within the available space.
The queue uses the selected width and hides when it would leave the reader less than 640 pixels.
The browser retains the preferred width across packets, pages, and reloads.
A smaller window limits the visible width but preserves that preference for a larger window.
The narrow layout hides the divider and uses the Collie view.

`createPacketChatResize` in `dashboard/src/ui/packetchat.js` changes layout without replacing the report or conversation.
A resize preserves the current packet and draft text.
The width preference lives in browser storage; the spine still owns the conversation and its messages.
See [the dashboard](../architecture/dashboard.md#packet-quick-chat) for the controller and its storage fallback.

## Model selection from the quick chat

In the wide Review layout, Ask Collie opens the chat from the decision bar.
The narrow layout uses the Collie view.
The quick chat header shows the current model and harness, with the task title below them.

`createModelPicker` in `dashboard/src/ui/conversation.js` lists enabled agent profiles.
Its packet callback first updates the Collie route through `POST /api/routing`.
That route applies to new Collie conversations across the dashboard.
With no current packet conversation, selection changes only the route.
For a current conversation with another profile, the callback starts a new conversation for the same packet.
A conversation with messages requires an in-app confirmation. The previous conversation remains in the Collie session list.
The existing session keeps its model; the new session uses the selected profile.

The control is unavailable during conversation preparation, replacement, send, and an owed reply.
An archived Project also disables it.
A route update failure preserves the current model.
If the route update succeeds but the new conversation fails, the error states both outcomes and provides Retry.
`packet-chat.smoke.test.ts` checks route selection before the first message and history retention after a model change.

## Launch and artifact access

The deployment-wide Collie round selects a wake independently of the Project lease.
`mc/verbs/collieselect.go` adds the packet, version, task, and Project identifiers to the wake effect.
The resident validates that identity and requires the packet Project in its available workspace list.
The session keeps the ordinary Collie workspace list.

The resident calls the host-only `__task-artifacts` command to resolve the task folder.
`mc/verbs/taskartifacts.go` resolves the Project artifact plane and the canonical task directory.
It refuses a task directory that resolves to another location.
An absent task directory returns a null source, which permits expired artifact state without broader access.
An unreadable artifact root refuses the launch.

The container receives the available task directory as a read-only mount at its recorded `/mc/artifacts/` path.
Project repository mounts mask an existing `.mc-artifacts` directory with an empty read-only folder.
The source workspace alias receives the same mask.
The resident keeps the mask when a directory read fails for a reason other than absence or a non-directory path.
It also requires the host mask directory to be empty.

The canonical container envelope includes these mounts.
A missing or writable artifact mount changes that envelope and prevents acceptance of the altered container.
The scope is one task's artifacts, including that task's retained versions.
It does not grant access to another task's artifacts.

## Context for the native session

The runner claims an operator message through the ordinary Collie message loop.
Before a turn without a native session reference, it requests packet context from the runner-only packet-context command.
`readPacketContext` checks the returned session, packet, and version identifiers against the envelope.
A missing or mismatched context prevents the harness turn.
A resumed native session retains its earlier context. A new native session receives context again.
The stored operator message remains separate from this context.

`BuildPacketContext` in `mc/verbs/packetcontext.go` supplies these facts:

- The Project, task, packet, and version identities.
- The thesis, criteria, and report text.
- The input commit, Worker commit, and available merge candidate identifiers.
- Readable report, Verifier evidence, inventory, and patch paths.

The context excludes review state, merge state, and blocks because those facts can change during the conversation.
The Collie directive requires current packet reads before answers about those states or a packet decision.
The packet list supports task selection while preserving the normal record fields.
Each row includes the task `title`, `packet_id`, and `version_id`.
The Collie command skill identifies these fields for the current packet read.
The Review reader and quick chat use the packet row title for their headings.
The quick chat falls back to the conversation task title when the row title is absent.

## Report integrity and size

The context builder reads the report through the artifact root and checks its recorded SHA-256 digest.
It rejects a symbolic link at the final path element and checks that the opened file matches the inspected file.
An absent, expired, or changed report produces an explicit unavailable statement while preserving the thesis and criteria.

The builder replaces each data address with a media type and byte count.
It limits the resulting report text to 128 KiB and preserves a valid UTF-8 boundary.
A truncated report includes the full transformed size and the report path.
A code fence longer than any backtick sequence in the report keeps report content inside that fence.
The full report remains available through its permitted artifact path when the file exists.

## Replies, decisions, and recovery

The runner sends provisional drafts and the final reply through the ordinary Collie commands.
The Review page and Collie page read the same conversation history.
The visible quick chat polls more frequently while a reply is owed.
A failed send restores the draft text. A newer packet of the same task retains that text and the failure message.

The Collie directive treats the report and artifact files as evidence.
Only an operator message authorizes a state change.
The directive requires an action description and explicit confirmation before a new state change.
A packet decision uses the original packet and version identifiers.
A newer packet requires a current record read and a new decision for that packet.

An archived Project refuses preparation, send, and resume for its packet conversations.
The browser preserves the history and disables the composer.
The Collie page disables model selection for packet conversations.
The Review quick chat provides that control through a new conversation.
Project deletion includes its packet conversations in the cleanup scope.

## Verification and related systems

`mc/verbs/colliepacket_test.go` checks session lifecycle and identity restrictions.
`packetcontext_test.go` checks context contents, report limits, media replacement, and unavailable reports.
`taskartifacts_test.go` checks canonical folder resolution and absent folders.
The resident and runner tests check the launch envelope and context delivery.
The browser smoke checks the shared conversation through packet changes, reloads, and both layouts.
The Docker test checks artifact reads, refused writes, and isolation from another task.

These are source-defined checks. This page does not claim a fresh test execution or production acceptance.

- [Collie](../concepts/collie.md) explains claims, replies, native sessions, and idle recovery.
- [The dashboard](../architecture/dashboard.md) explains the reader layout and API boundary.
- [Agent input](../concepts/agent-input.md) explains directives and skills.
- [Checks and test lanes](../tests/checks-and-test-lanes.md) locates each verification lane.

## Decision Pending during a packet conversation

The current packet read includes `blocked_kind` with the block reason.
Collie uses current task state to select the resolution for that condition.
An operator decision report requires revised requirements or task cancellation.
The revision writes every changed criterion into the description and records the explanation separately.
A reason-only unblock cannot resolve this report.

A packet return remains a separate operation: its Revision request tells the Worker what the operator requires in that run.
An applied requirements revision releases the old review slot and requires a new result for the effective requirements.
The conversation retains its original packet identity.
See [decision resolution](decision-resolution.md) for the shared dashboard and Collie operation.

The divider browser check waits for the narrow conversation before it asserts visibility.
Its cleanup restores the desktop viewport before it resets the divider.
These checks preserve packet content, draft text, and the preferred width across layout changes.
See [divider browser checks](../tests/checks-and-test-lanes.md#collie-divider-browser-checks) for the complete assertions.
