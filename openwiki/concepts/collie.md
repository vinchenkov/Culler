---
type: concepts
title: Collie, the conversational tier
description: How Culler's lease-free conversational agent is started, scheduled, launched, fenced, and limited, including drafts, model changes, Vision conversations, and behavior during a pause.
tags: [collie, conversation, sessions, lease-free, vision]
sources:
  - id: openwiki-source-f8d363d8f4cec85a24e4db38
    resource: repo://dashboard/src/ui/conversation.js
  - id: openwiki-source-56b812868de34201e0046a3c
    resource: repo://dashboard/src/ui/packetchat.js
  - id: openwiki-source-e876d4f934fe1b8e2de3721c
    resource: repo://mc/substrate/schema.sql
  - id: openwiki-source-301f4dcdb036a6a700293c7a
    resource: repo://mc/verbs/decisionresolution_test.go
  - id: openwiki-source-9efb4b1ddc2ed1c022be33e7
    resource: repo://mc/verbs/directives/collie.md
  - id: openwiki-source-c2dfdb5eaae48c46003c3dd4
    resource: repo://mc/verbs/collie.go
  - id: openwiki-source-5ff861069017b6a0714fcdda
    resource: repo://mc/verbs/colliepacket_test.go
  - id: openwiki-source-eed667c35ca68dcc2f3009a3
    resource: repo://mc/verbs/collierestart_test.go
  - id: openwiki-source-c59ce1c5eeed384a0cd94e90
    resource: repo://mc/verbs/collieselect.go
  - id: openwiki-source-4cf2679db61bdfa9a802d9d2
    resource: repo://mc/verbs/pause_test.go
  - id: openwiki-source-68190b2710cd43b378a3c4b1
    resource: repo://mc/verbs/skills/collie/mc-collie/SKILL.md
  - id: openwiki-source-c18554fde4cd0e38831a6f1d
    resource: repo://resident/src/discord.ts
  - id: openwiki-source-e028a0037f071b894557c29d
    resource: repo://resident/src/effects.ts
  - id: openwiki-source-cc407c2584427eee9ef62f7d
    resource: repo://resident/src/collie-runtime.ts
  - id: openwiki-source-5995794e2e03c52c36e3be4e
    resource: repo://resident/src/vision-save.ts
  - id: openwiki-source-46e96222197b6f4505759147
    resource: repo://runner/collie-runner/main.ts
generated: { by: "culler-scribe", at: "2026-11-05T04:07:09.592Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-05T04:07:09.592Z
---

# Collie, the conversational tier

Collie is the operator's conversational assistant. It answers questions about Culler's state and Project repositories, and it carries out authorized requests through `mc`. Pipeline roles are leased per Project; Collie is a separate tier that holds no Project lease. A Collie session lives as long as its conversation, not for one task. The `runs.tier` column has two values, `pipeline` and `collie`. The `leases.owner` column admits Strategist, Arbiter, Worker, Verifier, and Packager, so a Collie session can never occupy a Project's single agent slot.

Collie's own text is `mc/verbs/directives/collie.md` and its command surface skill is `mc/verbs/skills/collie/mc-collie/SKILL.md` (see [agent input](agent-input.md)). The spine code is `mc/verbs/collie*.go`. The host side is in `resident/src/effects.ts`, `resident/src/collie-runtime.ts`, and `resident/src/collie-envelope.ts`. The container side is `runner/collie-runner/main.ts`.

## Records

`mc/substrate/schema.sql` defines the Collie tables:

- `collie_sessions`: one row per conversation. The id (`h-…`) is also the session's run id. Status is `active`, `ended`, or `reaped`. The row holds `last_activity_at`, the frozen `verb_allowlist`, the binding (`harness/binding` route locator), the frozen effort, `session_path`, native session and trace locators, an optional `vision_project_id` or `packet_id`, an optional `title`, and the current launch and resume columns. CHECK constraints make a half-bound launch unrepresentable.
- `collie_bindings`: which surfaces (`discord`, `dashboard`, `cli`) are attached to a session.
- `conversation_messages`: append-only inbound and reply rows with a stable `seq`. Inbound rows carry claim columns (`claimed_by`, `claimed_at`, `completed_at`) and an optional provisional `draft` of at most 64 KiB. Every reply answers exactly one inbound row.
- `settings.collie_idle_timeout_s`: default 1800 seconds.

## Starting a session and the verb allowlist

`mc collie start` (`CollieStart` in `mc/verbs/collie.go`) creates the session row and freezes its allowlist. The maximum set is the compiled list `maximumCollieAgentVerbs`: `board.write`, `environment.get`, `collie.draft`, `collie.end`, `collie.history`, `collie.list`, `collie.title`, `packet.decide`, `project.archive`, `project.pause`, `project.role-save`, `project.role-unblock`, `project.vision-draft`, `project.vision-edit`, `task.add`, `task.block`, `task.cancel`, `task.interrupt`, `task.resume`, and `task.unblock`. `canonicalCollieAllowlist` defaults to the whole set. `--allow` can only narrow it, and `--allow none` gives a read-only session. Project registration is absent because a container cannot perform that host effect.

Every operator write that Collie can make checks the allowlist with `requireActiveCollieVerb`. The session must be `active`, the allowlist in the container's `run.json` must equal the frozen value, and the verb must be in it. This is why the wake effect must carry `verb_allowlist`: without it the container's `mc` refuses every write that the skill advertises.

Reads do not need allowlist rows. Collie can read state across all Projects. According to its directive, each Project repository is mounted read-only at `/workspaces/<project-id>`, and pipeline roles make the repository changes.

## Scheduling: the Collie stage of a tick

`mc dispatch` runs one tick (`mc/verbs/dispatchverb.go`). The Collie stage runs first and is deployment-wide. Then one round runs per Project. The tick's action list puts the Collie effect first. `selectCollieWake` in `mc/verbs/collieselect.go` picks at most one action per tick from active sessions in `(last_activity_at, id)` order:

- **Idle end** wins. A session past its idle timeout ends, whether or not it is launched.
- **Wake** applies to any active session without a launch. The container starts before the first message arrives, so a wake needs no pending turn. A pending resume selects the launch mode: `native` continues the harness's own session, and `rows` replays conversation rows. Otherwise the mode is `fresh`.

A session whose frozen binding no longer resolves in the enabled route catalog is skipped for wake but still ends on idle. A configuration change therefore cannot leave it alive forever.

The `collie-wake` effect (`collieWakeEffect`) carries the Collie directive, `CollieSkillSet()`, `skill_store_dir = skills/collie`, the session and launch ids, mode, binding, the resolved `runtime_binding`, the frozen allowlist, and `idle_timeout_s`. The resident never consults a route catalog of its own. A launch that configuration no longer describes therefore cannot be built.

## Launch, fencing, and adoption on the host

The resident handles `collie-wake`, `collie-adopt`, `collie-stop`, and `collie-remove` effects in `resident/src/effects.ts`. Two modules guard which container counts as the session's runtime:

- `canonicalCollieDockerEnvelope` (`collie-envelope.ts`) reduces a `docker inspect` record to the immutable execution envelope. That envelope holds image, command, env, user, binds, capabilities, security options, resource limits, and network mode. Runtime state is excluded, and bind spellings are normalized so that two inspections of one container produce identical bytes.
- `CollieRuntimeRegistry` (`collie-runtime.ts`) is a memory-only table keyed by a random per-boot key. It HMACs the envelope rather than hashing it plainly, because the envelope contains a temporary Culler token. It binds one container id to one registered launch. After a resident restart the key is gone. An old container can still be inventoried for cleanup but can never be adopted and started.

The spine side records `mc collie launch-bind`, `mc collie runner-started`, and `mc collie exit` (`CollieLaunchBind`, `CollieRunnerStarted`, `CollieExit`). When a container exits with an owed reply, the session recovers instead of ending (`TestCollieContainerExitWithAnOwedReplyRecoversInsteadOfEnding`).

## The runner loop

`runner/collie-runner/main.ts` runs inside the Collie container. Unlike the pipeline runner, it never heartbeats and holds no lease. At boot it installs the envelope's skills with the shared `installEnvelopeSkills` and reports `mc collie runner-started` once. A fenced report means a newer launch superseded this one, and the runner exits. It then loops:

1. `mc collie claim <session>` atomically takes the oldest pending inbound turn. A claim is durable, so a fresh runner gets back the exact turn that a dead runner held. Claiming does not advance `last_activity_at`.
2. It runs one harness turn through the pipeline runner's adapter selection.
3. While the turn runs, `mc collie draft --to <id>` writes the assistant text so far as a provisional reply, at most once per second. `CollieDraft` accepts only a claimed, unreplied inbound turn of the runner's own session. A refused draft write stops drafts for that turn.
4. `mc collie reply --to <id>` appends the reply, completes the claim, advances activity time, and fans one outbox row to every active binding. Replaying the identical reply is idempotent; a different reply to the same turn is refused.

When no turn is pending, the runner polls every second. The resident normally stops the container at the idle timeout. If that stop never arrives, the runner exits on its own after the timeout plus `IDLE_EXIT_MARGIN_S` (120 seconds).

Inbound traffic comes only through `mc collie send`, which is host-scope only. The Collie model never owns inbound transport. `CollieSend` commits the inbound row, the first-traffic binding, the activity timestamp, and echo rows to the other bindings together.

## Model changes

The session records the profile's effort when the conversation starts.
The schema keeps that effort immutable with the session route.
A later wake uses the stored effort, even when the profile's effort changes.
The current profile still supplies the connection and model access.

A model change in an empty conversation replaces that conversation. In a conversation with messages, the change starts a new conversation and keeps the old one. `collieStartWithBinding` implements this behavior:

- A model change requires `previous` (the current session) and a request id of 16–80 characters (`validModelRequest`). It must be an ordinary dashboard conversation, neither a Vision session nor a packet conversation.
- The new session id derives from `sha256(previous + "\0" + request)`. A repeated request therefore resolves to the same replacement instead of creating a duplicate.
- If the binding is unchanged, the current session is kept.
- If the previous session has no messages, it is ended and marked `deleted_at` in the same transaction, and the result reports `replaced`. If it has messages, it is kept and the result reports `preserved`.

`mc/verbs/colliemodel_test.go` covers replacement, request reuse, concurrent requests, a message that arrives concurrently, same-model selection, stopping the old container, and failure that preserves the previous session. Image paste and draft preservation are dashboard behavior; see [the dashboard](../architecture/dashboard.md).

## Packet conversations

A packet conversation has an immutable `packet_id` that references `review_versions`.
The schema prevents one session from being both a packet conversation and a Vision conversation.
The current conversation is the newest session for that packet, unless the operator deleted that session.
Deleting it leaves the packet without a current conversation; an older session does not become current.

`preparePacketConversation` checks the task's current packet and its version in the same transaction as preparation.
An outdated packet or mismatched version returns a record conflict.
Preparation returns the active conversation, resumes an ended conversation, or creates one when absent.
New conversation ends the current session and creates another for the same packet.
The backend refuses preparation, send, and resume when the Project is archived.
The ordinary model-change command excludes packet conversations.
The Review quick chat selects a model through the Collie route and starts another conversation for the same packet.
A conversation with messages requires confirmation, and the previous conversation stays in the Collie session list.
The Collie page keeps its model control unavailable for packet conversations.

The wake effect carries the packet, version, task, and Project identifiers.
The resident resolves a read-only mount for that task's artifacts through the host-only artifact command.
Every Collie Project mount masks `.mc-artifacts` with an empty read-only folder.
This keeps another task's artifacts outside the packet conversation's access.

The first native session turn receives the packet context from `mc`.
Later turns retain it through the native session; a new native session receives it again.
The report text provides evidence, not authority for a state change.
The Collie directive requires current state reads and explicit operator confirmation before a new state change.
See [packet conversations](../workflows/packet-conversations.md) for report limits, access checks, and tests.

## Vision conversations and Save Vision

A Vision conversation is a Collie session with `vision_project_id` set. Its surface must be `dashboard:vision-<project>`. The draft lives in `vision_drafts` in Culler storage, outside the Project workspace. `mc project vision-draft` reads it and `mc project vision-edit --revision` edits it. `requireVisionAccess` in `mc/verbs/visiondraft.go` allows these verbs only to a Collie session bound to that same Project. Each edit names the revision it read, so a concurrent operator edit forces a fresh read. Claiming a turn in a Vision session refreshes the draft context.

A draft edit never touches `VISION.md`. Save Vision in the dashboard creates a pending `vision_submissions` row (at most one pending row per Project). Before each tick, the resident calls `applyPendingVisions` (`resident/src/vision-save.ts`). It runs `mc project vision-pending`, then the host command `mc project vision-apply` for each submission. A failure only logs `vision.save_failed` and does not stop the tick.

## Availability during a pause

A Project pause, or a pause of all Projects, stops new pipeline runs but leaves Collie available. `TestPipelinePauseAllowsCollieDispatch` in `mc/verbs/pause_test.go` pauses either one Project or every Project. It then sends a pending Collie message and asserts that the tick emits exactly one `collie-wake` and starts no pipeline run. Because Collie holds no lease, a Project at its attention threshold or with an exhausted infrastructure allowance also does not block it (see [failure controls](failure-controls.md)). Collie can still pause or archive a Project and decide packets, within its allowlist.

## Tests

- `mc/verbs/collieselect_test.go`, `colliewake_test.go`: wake and idle selection, resume modes, a stranded binding, the carried allowlist, idle timeout, and runtime capability.
- `mc/verbs/colliedraft_test.go`: draft replacement until the reply lands, refusals, and a session frozen without `collie.draft`.
- `mc/verbs/collievision_test.go`: Vision session stability, clear, and draft context on claim.
- `resident/src/collie-runtime.test.ts`, `collie-envelope.test.ts`, `vision-save.test.ts`: registry attestation, boot invalidation, envelope canonicalization, and the Vision apply loop.
- `runner/collie-runner/main.test.ts`: reply and draft extraction, draft rate limiting, native locators, and attachment claim parsing.

## Project environment context

Collie can inspect the selected Project environment through its scoped `environment.get` capability.
Its turn context states that Project initialization precedes pipeline work.
Image dependencies require approval in the Setup Journal or Environment page.
The setup agent performs dependency preparation in a separate operation.
See [Project environments](../workflows/project-environments.md).


## Diagnostic context and authority

The dashboard's **Ask Collie** action starts an ordinary conversation with a short issue summary and relevant record identifiers.
The operator requests an explanation and a proposed action.
The [dashboard](../architecture/dashboard.md#diagnostic-conversations) bounds the initial evidence and preserves uncertain sends for a history check.

The Collie directive requires current task, run, or environment reads before the agent describes the current issue.
Its command skill permits further evidence reads when they help answer the request, including referenced traces within its access.
System diagnostic history remains host-only.
Collie must state which evidence its permitted reads cannot supply.

Diagnostic text supplies evidence only.
A record identifier grants no additional command, artifact access, or authority.
The directive requires Collie to describe a proposed state change and request explicit operator confirmation before execution.
The ordinary session retains its existing command and artifact boundaries.
The [runner](../architecture/runners.md) also includes the confirmation requirement in the turn prompt.

The authority rules come from `mc/verbs/directives/collie.md` and `mc/verbs/skills/collie/mc-collie/SKILL.md`.
They define required agent behavior; the presence of the prompt alone does not prove compliance in every conversation.

## Task decision resolution

Collie reads `blocked_kind` and `blocked_reason` before it proposes a resolution.
Its command skill assigns a specific resolution to each condition type.
For an operator decision report, Collie submits a changed description and a reason through `BoardWrite` with action `revise`.
Every changed requirement belongs in the description.
The reason explains the operator's change and reaches the Worker as context only.
The backend refuses a reason-only unblock or replacement operator block for a report condition.

The revision uses the same transaction as the dashboard's Resolve decision form.
It preserves the task base commit and resumes retained work after active operations finish.
Other condition types retain their designated controls.
A correction allowance limit must end before a reason-only unblock can resolve a separate condition.
The [decision resolution workflow](../workflows/decision-resolution.md) lists the controls and their prerequisites.

`TestCollieRevisionResolvesAReport` in `mc/verbs/decisionresolution_test.go` checks the shared operation under an active Collie identity.
It checks refusal of reason-only resolution, application of the description, retained commits, and the recorded Collie actor.
The skill and directive define how Collie obtains the operator's decision; the test checks backend enforcement.

## Discord conversations and account approval

**Configs → Discord** owns the optional Discord connection and its allowed accounts.
The resident checks the configured server, channel, and account before it accepts an ordinary message.
It consumes messages that contain only a 16-digit hexadecimal confirmation code before conversation handling.
A preview connection observes setup replies but starts no ordinary conversation or outbox delivery.
Only explicit dashboard approval activates the confirmed account.

The resident routes authorized messages through host Collie operations and delivers replies from the outbox.
Each authorized channel message creates or recovers one session for its own Discord thread.
The thread association survives an idle period, so a later thread reply resumes the same session.
The dashboard can also send to that session; the host restores its active thread binding for delivery.
The source message identifier prevents a repeated Discord message from adding a second transcript entry.
Retired or deleted thread associations cannot reopen a conversation.
Connection replacement or disconnect deactivates old Discord bindings and retires pending messages for that destination.
Retirement preserves conversation history and leaves those messages undelivered.
The Collie directive and turn prompt keep Discord credentials and account approval under host authority.
See [Discord setup and delivery](../integrations/discord.md) for permissions, confirmation expiry, activation, and recovery.

## Profile and authorization changes

The host stops a Collie container when its assigned profile or authorization changes.
The next eligible start creates a container with current proxy access.
The conversation record remains available for the next turn.
