---
type: integration
title: Discord setup, account access, and delivery
description: The optional Discord connection, its dashboard setup, host authority, delivery lifecycle, and verification boundaries.
tags: [discord, collie, configuration, alerts]
sources:
  - id: openwiki-source-4d1d392666be6dfdd7a91a2e
    resource: repo://.github/workflows/release.yml
  - id: openwiki-source-6ff7ec04aac5c9f6567d2d84
    resource: repo://dashboard/smoke/harness.ts
  - id: openwiki-source-9dc12756d2cc067f897826b6
    resource: repo://dashboard/src/ui/discord.js
  - id: openwiki-source-1bb4f1508c7edc9615caeee6
    resource: repo://mc/cmd/mc/discord.go
  - id: openwiki-source-02886ae4c9efa40e167cf53b
    resource: repo://mc/config/config.go
  - id: openwiki-source-f3d64f8fe895a5a9e20afaa3
    resource: repo://mc/verbs/discord.go
  - id: openwiki-source-04786a87679eea8954d2f952
    resource: repo://mc/verbs/environment_execution.go
  - id: openwiki-source-6dc928cb15677dde622f6a7e
    resource: repo://resident/src/discord-config.ts
  - id: openwiki-source-c18554fde4cd0e38831a6f1d
    resource: repo://resident/src/discord.ts
generated: { by: "culler-scribe", at: "2026-11-12T00:11:01.469Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-13T23:23:34.077Z
---

# Discord setup, account access, and delivery

Culler connects one bot to one server and one channel for Collie conversations and selected alerts.
The operator uses **Configs → Discord** after the first Project completes initial source acceptance.
The connection serves the installation, independently of a selected Project.
Culler must run on the Mac for Discord access.

## Owners and entry points

| Component | Responsibility |
| --- | --- |
| `dashboard/src/ui/discord.js` | Setup steps, account approval, connection status, alert controls, and disconnect confirmation. |
| `dashboard/src/api.ts` | Authenticated requests to the corresponding host command, with JSON bodies on standard input. |
| `mc/cmd/mc/discord.go` | Host authority, configuration revisions, saved policy, and coordinated activation. |
| `mc/verbs/discord.go` | Durable availability, alert routes, and retirement of old delivery destinations. |
| `resident/src/discord-config.ts` | Discord checks, temporary credentials, confirmation receivers, and connection activation. |
| `resident/src/discord.ts` | Gateway events, authorized Collie messages, and outbox delivery. |

The host command communicates with the resident through `MC_HOME/discord.socket`.
It verifies an operator-owned socket with mode 0600 before use.
The resident serializes requests to prevent concurrent changes within its service.
All spine changes remain Go operations through `mc`.
Agent containers cannot configure Discord or approve an account.

## Availability and storage

Initial source acceptance sets `settings.discord_available` when the Project becomes ready.
The availability read also recognizes an existing ready Project with successful initial acceptance, or an enabled Discord connection.
Project creation and a complete preparation report alone do not satisfy this condition.
The retained flag preserves access after Project deletion or disconnect.

The sidebar hides Discord until the host reports availability.
An unavailable Discord section address opens System.
Once available, the section appears without an automatic redirect to setup.
The Advanced settings editor omits raw Discord values.

`MC_HOME/config.toml` holds the enabled state, bot token, server, channel, allowed accounts, and alert selections.
The host's atomic writer protects configuration files with mode 0600.
The generated resident supervision file contains no Discord policy.
The resident reads the private runtime policy through a host-only command at startup.
Public dashboard responses omit the bot token.

Temporary setup drafts live in resident memory for 15 minutes, with at most eight drafts.
A resident restart loses unfinished drafts and confirmations.
Browser session storage holds an opaque draft identifier, configuration revision, step, and destination selections.
It contains neither the bot token nor the confirmation code.
A reload restores a valid host draft or explains that setup expired.

## Setup and account confirmation

The setup flow has three steps:

1. **Connect bot** checks the token and displays the bot identity.
2. **Choose channel** discovers server and channel names, checks permissions, and sends an explicit test message.
3. **Confirm account** requires a human reply with the displayed code, followed by explicit dashboard approval.

Check bot sends no channel message.
Channel discovery offers text channels that the bot can view and identifies missing permissions.
One Refresh list control reloads the servers and channels.
It keeps the selected channel if that channel remains available.
The service calculates base roles, server-wide overrides, combined role overrides, and member overrides in that order.
Its required permissions cover these actions:

- View Channel.
- Send Messages.
- Attach Files.
- Read Message History.
- Create Public Threads.
- Send Messages in Threads.

The dashboard guide also requires Message Content Intent.
The service opens a preview Gateway receiver and requires readiness before the setup test succeeds.
The test message uses a nonce and requests nonce enforcement for retry handling.
It omits the account confirmation code.

The displayed code contains 16 unpredictable hexadecimal digits.
The operator sends only the code in the selected channel.
The Copy code control copies only those digits.
It expires after five minutes.
The receiver accepts one human account from the exact draft server and channel.
It accepts either letter case after it trims surrounding space.
It rejects bot replies, other destinations, expired codes, and later replies after account selection.
A renewed code clears the earlier account selection.
A server or channel reset clears dependent confirmation state.

The candidate connection requires confirmed test delivery, an unexpired account confirmation, and a connected receiver.
An existing connection preserves its account list unless the request explicitly replaces it.
The dashboard names accounts that lose access before replacement.
The final **Allow … and connect** action authorizes activation.
Preview receivers start no ordinary conversation or outbox delivery.

## Activation, replacement, and recovery

Each mutation carries the configuration revision that the dashboard read.
The host checks it again while it holds the configuration lock.
A stale request fails before it changes the saved connection.

A connection replacement follows this sequence:

1. The resident checks the candidate destination and opens an inactive preview connection.
2. The host writes the desired configuration under the configuration lock.
3. The resident stops the current connection and waits for its pending inbound and outbound work.
4. The host updates the spine projection and retires the previous destination.
5. The resident activates the checked connection.

A failed preview preserves the active connection.
A staged preview expires after one minute if activation never uses it.
After an apply failure, `UpdateAtomicWithRecovery` restores the previous configuration and invokes its recovery callback.
If recovery fails, the error states that the saved configuration exists but activation remains unverified.
The operator can use **Check connection** to retry projection and activation, then check access and health.

This coordination spans a configuration file, the spine, and a network service.
It is not one database transaction across all three.
Retirement remains permanent even when recovery restores the previous policy.
A restored connection does not revive retired messages or old bindings automatically.

The connection card distinguishes Connected, Needs attention, and Status unavailable.
A health result older than two minutes shows its timestamp and an unavailable current status.
Periodic refresh and Check connection send no test message.
Only **Send test message** performs that explicit delivery check.

## Conversations, alerts, and disconnect

Ordinary inbound messages require a non-bot author in the allowed account list and the configured server.
The destination must be the configured channel or a recognized thread below it.
The connector consumes bare confirmation codes before ordinary Collie routing.
Discord credentials and account approval belong to host controls.
See [Collie](../concepts/collie.md) for conversation records and run authority.

### Thread conversations

Each authorized channel message starts a public thread attached to that message and one Collie session.
The resident verifies the thread before it sends the initial message to Collie.
Each reply in that thread continues the associated session.
If a start stops before the initial message reaches Collie, the resident checks the bot-owned thread and recovers the original channel message.
The source message identifier keeps a repeated Discord message to one transcript entry.

The dashboard lists sessions from both surfaces and can continue a Discord session.
The thread association stays with the session across idle periods and resident restarts.
A dashboard message in that session sends the reply to the associated thread.
A dashboard session starts without a Discord thread.
Retirement prevents the old thread from reopening its association.

The outbox stores each reply's thread destination.
The resident splits long text into parts of at most 2,000 characters and acknowledges the row after delivery.
A failed thread keeps its queued rows; the drain can deliver rows for other threads.

New connections enable Blocked tasks and System health alerts.
The host changes only the Discord routes and preserves dashboard routes.
A failed alert save leaves the browser's confirmed snapshot in place.

Connection retirement deactivates Discord Collie bindings and marks pending Discord outbox rows with `retired_at`.
Those rows retain their original destination and remain undelivered.
Outbox polling excludes them, and acknowledgement refuses them.
The schema prevents a later change to a retirement timestamp.

Disconnect requires explicit dashboard confirmation.
It clears the saved token, destination, account list, enabled state, and Discord alert selections.
The resident stops the active connector and removes temporary setup credentials.
Project data, conversation history, and access to the Discord section remain available.

## Checks and retained evidence

| Source | Evidence boundary |
| --- | --- |
| `mc/verbs/discord_test.go` | Persistent availability, retirement, refused acknowledgement, and preserved dashboard routes. |
| `mc/cmd/mc/config_cli_test.go` | Host scope checks before protected configuration access. |
| `mc/config/recovery_test.go` | Configuration restore and failed recovery reporting. |
| `resident/src/discord-config.test.ts` | Confirmation, expiry, invalidation, account replacement, inactive previews, and failed activation preparation. |
| `resident/src/discord.test.ts` | Inbound authorization and connector delivery behavior. |
| `mc/verbs/colliethread_test.go` | Thread identity, repeated messages, resume, retirement, and dashboard delivery bindings. |
| `dashboard/smoke/discord-conversation.smoke.test.ts` | One conversation across a Discord thread and the dashboard, plus a separate channel conversation. |
| `dashboard/smoke/discord.smoke.test.ts` | Browser setup through real host commands with simulated Discord responses. |

The browser smoke checks invalid tokens, delivery failure, reload, approval, alert changes, edit cancellation, and token removal.
It captures desktop and narrow layouts in both themes.
Run that suite through `./dashboard/smoke.sh`; the [test guide](../tests/checks-and-test-lanes.md) explains its prerequisites.
The smoke harness rejects a scratch `discord.socket` path above 103 bytes before it creates the scratch home.
The CI browser job sets `MC_SMOKE_ROOT` below `$RUNNER_TEMP/smoke` to keep this path short.
