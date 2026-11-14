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
  - id: openwiki-source-d836d340a3e63f02aab7a979
    resource: repo://mc/cmd/mc/main.go
  - id: openwiki-source-47d33f42b4eeb3c2e4a59220
    resource: repo://mc/cmd/mc/operator_bridge.go
  - id: openwiki-source-b578a24182c530b5d8630c21
    resource: repo://mc/dispatch/dispatch.go
  - id: openwiki-source-e876d4f934fe1b8e2de3721c
    resource: repo://mc/substrate/schema.sql
  - id: openwiki-source-a44ab4c2d26db975bde190a0
    resource: repo://mc/verbs/collie.go
  - id: openwiki-source-3a72fe65b15fd6f2f9988f26
    resource: repo://mc/verbs/colliepacket_test.go
  - id: openwiki-source-81e49db5f10e4699bcadf8d3
    resource: repo://mc/verbs/collierestart_test.go
  - id: openwiki-source-c264187d95e49cb66eb75ae4
    resource: repo://mc/verbs/collieselect.go
  - id: openwiki-source-301f4dcdb036a6a700293c7a
    resource: repo://mc/verbs/decisionresolution_test.go
  - id: openwiki-source-6c249547ec5cf1c015cbdcb1
    resource: repo://mc/verbs/directives/collie.md
  - id: openwiki-source-fd26e4ff540a1d0d8f28905d
    resource: repo://mc/verbs/operatorlane.go
  - id: openwiki-source-4cf2679db61bdfa9a802d9d2
    resource: repo://mc/verbs/pause_test.go
  - id: openwiki-source-70036e7e15ac4ec2f0a44592
    resource: repo://mc/verbs/skills/collie/culler/references/diagnostics.md
  - id: openwiki-source-2ac019cf0635c7e749a5ac3f
    resource: repo://mc/verbs/skills/collie/culler/references/tasks.md
  - id: openwiki-source-b8bfd4cae8b08f0dd90bb773
    resource: repo://mc/verbs/skills/collie/culler/SKILL.md
  - id: openwiki-source-1655883a2300ff18955c5910
    resource: repo://resident/src/collie-runtime.ts
  - id: openwiki-source-c18554fde4cd0e38831a6f1d
    resource: repo://resident/src/discord.ts
  - id: openwiki-source-e028a0037f071b894557c29d
    resource: repo://resident/src/effects.ts
  - id: openwiki-source-8b49643c3705c97ff875fa4b
    resource: repo://resident/src/operator-lane.test.ts
  - id: openwiki-source-12764378a8b31b3916b3e3ed
    resource: repo://resident/src/operator-lane.ts
  - id: openwiki-source-5995794e2e03c52c36e3be4e
    resource: repo://resident/src/vision-save.ts
  - id: openwiki-source-d1b22419782a460caf6dbe3c
    resource: repo://runner/collie-runner/main.ts
generated: { by: "culler-scribe", at: "2026-11-13T23:23:34.077Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-13T23:23:34.077Z
---

# Collie, the conversational tier

Collie is the operator's conversational assistant. It answers questions about Culler's state and Project repositories, and it carries out authorized requests through `mc`. Pipeline roles are leased per Project; Collie is a separate tier that holds no Project lease. A Collie session lives as long as its conversation, not for one task. The `runs.tier` column has two values, `pipeline` and `collie`. The `leases.owner` column admits Strategist, Arbiter, Worker, Verifier, and Packager, so a Collie session can never occupy a Project's single agent slot.

Collie's own text is `mc/verbs/directives/collie.md` and its command surface skill is `mc/verbs/skills/collie/culler/SKILL.md` (see [agent input](agent-input.md)). The spine code is `mc/verbs/collie*.go`. The host side is in `resident/src/effects.ts`, `resident/src/collie-runtime.ts`, and `resident/src/collie-envelope.ts`. The container side is `runner/collie-runner/main.ts`.

## Records

`mc/substrate/schema.sql` defines the Collie tables:

- `collie_sessions`: one row per conversation. The id (`h-…`) is also the session's run id. Status is `active`, `ended`, or `reaped`. The row holds `last_activity_at`, the frozen `verb_allowlist`, the binding (`harness/binding` route locator), the frozen effort, `session_path`, native session and trace locators, an optional `vision_project_id` or `packet_id`, an optional `title`, and the current launch and resume columns. CHECK constraints make a half-bound launch unrepresentable.
- `collie_bindings`: which surfaces (`discord`, `dashboard`, `cli`) are attached to a session.
- `conversation_messages`: append-only inbound and reply rows with a stable `seq`. Inbound rows carry claim columns (`claimed_by`, `claimed_at`, `completed_at`) and an optional provisional `draft` of at most 64 KiB. Every reply answers exactly one inbound row.
- `settings.collie_idle_timeout_s`: default 1800 seconds.

## Starting a session and the verb allowlist

`CollieStart` in `mc/verbs/collie.go` freezes the session's permission set.
The maximum derives from `mc/operatorcmd` plus Collie conversation commands.
New default sessions receive the release's supported operator permissions; existing sessions retain their frozen set.
An explicit allowlist can narrow that set.
Host operations, including Project registration, use retained requests through the resident operator lane.

Every operator write that Collie can make checks the allowlist with `requireActiveCollieVerb`. The session must be `active`, the allowlist in the container's `run.json` must equal the frozen value, and the verb must be in it. This is why the wake effect must carry `verb_allowlist`: without it the container's `mc` refuses every write that the skill advertises.

Reads retain session and target checks; supported commands can also require a permission. Ordinary conversations can target an explicitly identified Project. According to its directive, each Project repository is mounted read-only at `/workspaces/<project-id>`, and pipeline roles make the repository changes.

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

New Collie containers use the current optional CPU and memory limits from **Configs → Advanced**.
Both default to no limit. A memory limit permits no additional swap, and each container has a limit of 2,048 processes.
`agentContainerLimits` reads the settings before launch; the expected container envelope uses those memory and process values.
See [resident limits](../architecture/resident.md#agent-container-limits).

## The runner loop

`runner/collie-runner/main.ts` runs inside the Collie container. Unlike the pipeline runner, it never heartbeats and holds no lease. At boot it installs the envelope's skills with the shared `installEnvelopeSkills` and reports `mc collie runner-started` once. A fenced report means a newer launch superseded this one, and the runner exits. It then loops:

1. `mc collie claim <session>` atomically takes the oldest pending inbound turn. The spine retains the original turn and its reply attempts. Claiming does not advance `last_activity_at`.
2. It runs one harness turn through the pipeline runner's adapter selection.
3. While the turn runs, `mc collie draft --to <id>` writes the assistant text so far as a provisional reply, at most once per second. `CollieDraft` accepts only a claimed, unreplied inbound turn of the runner's own session. A refused draft write stops drafts for that turn.
4. `mc collie reply --to <id>` appends the reply, completes the claim, advances activity time, and fans one outbox row to every active binding. Replaying the identical reply is idempotent; a different reply to the same turn is refused.

When no turn is pending, the runner polls every second. The resident normally stops the container at the idle timeout. If that stop never arrives, the runner exits on its own after the timeout plus `IDLE_EXIT_MARGIN_S` (120 seconds).

Inbound traffic comes only through `mc collie send`, which is host-scope only. The Collie model never owns inbound transport. `CollieSend` commits the inbound row, the first-traffic binding, the activity timestamp, and echo rows to the other bindings together.

## Interrupted replies and explicit retry

A container exit with an owed reply preserves the conversation and native trace context.
The spine marks the interrupted attempt as failed and retains any partial draft.
A replacement process can resume the native session, but it does not automatically repeat that ambiguous reply.
The operator's Retry action creates one new attempt for the original message.
A repeated retry request reuses that attempt, and the old process cannot complete it.
`mc/verbs/collierestart_test.go` verifies these boundaries.

A network timeout can advance the connection revision before the active process ends.
System status keeps the attempt identified by the current diagnostic visible through that transition.
See [connection diagnostics](../operations/agent-connections.md#connection-diagnostics) for status and recovery evidence.

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
For a Strategist task, its opening heading is Strategic rationale.
The opening text contains the Packager's statement of the delivered change, followed by the Strategist's rationale.
For an operator task, the heading is Thesis.
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
System diagnostics are available through the supported host request path.
Collie must state which evidence its permitted reads cannot supply.

Diagnostic text supplies evidence only.
A record identifier grants no additional command, artifact access, or authority.
The directive requires Collie to describe a proposed state change and request explicit operator confirmation before execution.
The ordinary session retains its existing command and artifact boundaries.
The common skill supplies the confirmation requirement; the runner supplies turn facts.

The authority rules come from `mc/verbs/directives/collie.md` and `mc/verbs/skills/collie/culler/SKILL.md`.
They define required agent behavior; the presence of the prompt alone does not prove compliance in every conversation.

## Task descriptions and creation

Collie applies the shared `task-description` skill to each new description and requirements revision.
The operator request selects the work; repository code and tests support the description.
Before creation, Collie shows the title, Project, priority, and full description.
Before a requirements revision, it shows the full revised description and the reason.
The operator can change the draft. Collie identifies violations of the standard before confirmation and submits the text the operator confirms.
These are agent instructions, not proof that every conversation follows the procedure.

Collie creates tasks through `board.write` with action `create`.
The command creates an operator task at `seeded` with `origin:user`.
The Collie allowlist excludes `task.add`; a start request that includes it fails.
Host task creation retains that internal command.
`mc/cmd/mc/cli_test.go` checks these authority boundaries.

A task read includes `strategic_rationale` for a Strategist proposal.
A revision changes the description while the rationale remains immutable.
The rationale explains the original proposal and adds no task requirement.
See [the shared task description standard](agent-input.md#shared-task-description-standard) for scope and authority.

## Task priority

Collie records explicit operator task requests at Seeded through the `create` action of `board.write`.
Task creation uses priority 2 by default; priority -1 is the highest.
A higher task priority receives the next eligible agent run before a lower task priority.
An active run continues after a priority change.
Approved merge work, required branch updates, and review capacity retain precedence.

Within one task priority, an open merge repair after approval starts first.
A task that received a Worker run precedes a task without one.
The furthest stage, task age, and task number resolve the remaining ties.
At review capacity, only tasks with a live packet can proceed through this order.
See [dispatch priority rules](../workflows/dispatch-and-tick-loop.md#priority-rules) for the source selection and tests.

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
Discord credentials and account approval remain under host authority.
See [Discord setup and delivery](../integrations/discord.md) for permissions, confirmation expiry, activation, and recovery.

## Profile and authorization changes

The host stops a Collie container when its assigned profile or authorization changes.
The next eligible start creates a container with current proxy access.
The conversation record remains available for the next turn.

## Command discovery and retained requests

The common `culler` skill selects procedures for tasks, packets, configuration, diagnostics, and traces.
Command help supplies syntax and result contracts without a live service.
`mc/verbs/operator_requests.go` preserves request identity and rechecks the active session before execution.
A stable request identifier prevents duplicate effects after an uncertain response.
A completed request retains its result. Acceptance alone does not establish completion.
The host checks release compatibility and update maintenance before writes.
See [command boundaries](../architecture/mc-cli-and-verb-scope.md#supported-operator-commands).

The resident operator lane (`resident/src/operator-lane.ts`) is the only executor of retained requests, so a stopped lane leaves Collie changes pending and Collie replies without draft text until they complete.
Three safeguards keep the lane alive:

- The lane bounds each `__operator-due` listing and each recovery command with a 30-second limit. A command that never returns fails one round, and the next tick starts the next round.
- `__operator-due` and `__operator-list` read no input, and delegated `mc` calls attach an input stream only when input exists. On macOS, Docker could lose the end of an empty input stream, and a listing then waited forever.
- The lane records its last working check in `MC_HOME/health/operator-lane.json`. `mc system status` reports the failed **Collie requests** condition when that record of the current resident is older than one minute, and the condition advises `culler restart resident`.

See [the resident](../architecture/resident.md#retained-operator-execution-and-native-traces) for the lane lifecycle.
