---
type: operations
title: Culler lifecycle command
description: The operator command controls local services, reports their state, and checks for updates.
tags: [operations, lifecycle, launchd]
sources:
  - id: openwiki-source-096a781fb160ef979fa31121
    resource: repo://INSTALL.md
  - id: openwiki-source-de03631980be3775e6b20749
    resource: repo://mc/cmd/mc/culler_prod.go
  - id: openwiki-source-c0c8c1965a051d7700920804
    resource: repo://mc/cmd/mc/culler_services_prod.go
  - id: openwiki-source-579b9c04dbd98eaf836c5c4c
    resource: repo://mc/cmd/mc/setup_update_candidate_prod.go
  - id: openwiki-source-769bbb5f564fdccd1e6ebbb1
    resource: repo://mc/cmd/mc/setup_update_stop_prod_test.go
  - id: openwiki-source-30e353995389d69cb272185f
    resource: repo://mc/cmd/mc/setup_update_transaction_prod.go
  - id: openwiki-source-a69635b1c1f62a5cb37186fd
    resource: repo://mc/deployment/lifecycle.go
  - id: openwiki-source-26d5c493ee5be4c7eb7219f5
    resource: repo://release/UPDATE-VERIFICATION.md
  - id: openwiki-source-09c5383289d9635a85a372a3
    resource: repo://release/UPDATES.md
generated: { by: "culler-scribe", at: "2026-11-09T19:06:13.611Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-09T19:06:13.611Z
---

# Culler lifecycle command

The `culler` command controls the local Culler deployment on the Mac host.
The operator uses the dashboard or Collie for configuration.
Commands other than uninstall accept `--mc-home` for another deployment.
The command accepts `--no-color` for plain output.
`NO_COLOR` and redirected output also select plain text.
See [installation and deployment](install-and-deployment.md) for command installation and repair.

## Commands

| Command | Result |
| --- | --- |
| `culler help [command]` | Show available commands or command details. |
| `culler status` | Check the installed deployment and service state. |
| `culler version` | Show the command and installed release identities. |
| `culler start [service]` | Start selected services. |
| `culler stop [service]` | Stop selected services. |
| `culler restart [service]` | Restart selected services. |
| `culler update --check` | Check release availability. |
| `culler update` | Resume an incomplete update or open the installer for a new update. |
| `culler update --restore` | Restore an incomplete update before its commit. |
| `culler uninstall` | Preview removal of all detected installations and request confirmation. |
| `culler uninstall --keep-data` | Remove software and preserve Culler data after confirmation. |
| `culler uninstall --dry-run` | Show the removal plan. |
| `culler logs [service] --tail N` | Show recent service records. |

The service names are `resident`, `dashboard`, and `agentsview`.
Start, stop, and restart select all three services when no service is named.
Logs select the resident and 100 records by default.
The `--tail` value accepts 1 through 1000.
`mc/cmd/mc/culler.go` parses commands and options.
`mc/cmd/mc/culler_prod.go` performs host checks and routes each command.

## Service state and safe transitions

The command reads service state through launchd and checks the installed release before a service change.
`mc/deployment/lifecycle.go` stores explicit stop choices in `MC_HOME/state/stopped-services.json`.
The record separates an operator stop from a service failure.
An explicit stop survives a login or computer restart.
A start or restart clears the selected stop choice.

`mc/cmd/mc/culler_services_prod.go` serializes a service change with the setup lock.
It records the operation so a later command can restore an interrupted change.
Before a resident stop, it waits up to three minutes for active agents and repository work.
The resident publishes an install maintenance acknowledgement when its operations finish.
The command then stops the selected units and checks their quiet state.
A start checks service readiness against the installed release.
An unhealthy active service requires inspection before a start command can proceed.
If a change fails, the command attempts to restore the prior service state and stop choices.
See [the resident](../architecture/resident.md) for its tick and maintenance behavior.

## Public updates and recovery

`culler update` resumes an incomplete update with its retained candidate before it selects another release.
The installer warns about interruption and affected saved profiles before confirmation.
It then stops services and containers immediately, forces termination after ten seconds, and verifies exit within thirty seconds.
An unknown process state refuses replacement.
This stop procedure differs from the three-minute wait for an ordinary resident stop.

The update record blocks ordinary work throughout replacement and verification.
The updater checks the candidate, saved configuration, and every selected Project image.
The Project image check runs the candidate runtime with temporary volumes and no live database mount.
It accepts the default image's protected spine directory when the runtime checks pass.
See [candidate runtime compatibility](image-delivery.md#candidate-runtime-compatibility) for the checks and their isolation.
It verifies a checkpoint before replacement and checks component identities and local readiness before commit.
It synchronizes files and records the commit before it releases maintenance.
An incomplete update keeps work stopped.

`culler update --restore` restores the checkpoint before commit.
Recovery verifies the retained package and requires no download after checkpoint completion.
A failed restore preserves the checkpoint for another attempt.

`mc/cmd/mc/setup_update_transaction_prod.go` starts the previous release's helper before it restores the prior service state.
The update stop removes the helper, and resident verification needs that helper to read the spine.
Restore first applies the available checkpoint and prior stop choices, then starts the helper with the previous image.
A helper failure keeps work stopped and prevents service restoration.
The diagnostic names the cause and directs the operator to retry `culler update --restore` after repair.
Successful service verification precedes the restored state and release of maintenance.

After commit, cleanup can retry without restoring old data over the current deployment.
Cleanup preserves selected Project images and images that containers use.
An ordinary downgrade remains unsupported.

The checkpoint covers the spine, optional OpenCode volume, configuration, host software, commands, service definitions, and applicable AgentsView data.
Project repositories, worktrees, homes, artifacts, and native sessions remain in place.
Provider grants retain their newest saved values.
The checkpoint assumes intact storage; it does not replace a separate backup for disk loss.

`release/UPDATES.md` defines the public update contract and emergency recovery procedure.
The [retained verification report](../../release/UPDATE-VERIFICATION.md) records scratch checks from 6 November 2026.
It leaves complete packaged update and restore through macOS launchd unverified.
Those historical results are separate from this wiki review and from required public release acceptance.

## Removal

Uninstall covers all detected Culler installations and rejects `--mc-home`.
`MC_HOME` adds a deployment to discovery; it does not restrict removal to that deployment.
The default full wipe removes software, Culler data, credentials, task branches, and review files after the operator types `wipe`.
The keep-data mode preserves configuration, credentials, and history after the operator types `uninstall`.
Both modes preserve Project repositories and shared host tools.
Removal stops active agents and can discard unfinished work.
The [installation guide](../../INSTALL.md#uninstall-culler) describes recovery paths and unattended authorization.

## Results and diagnostics

Status reads local service checks without a release network request.
Update check reads release availability without an installation.
The logs command reads a bounded tail and exits.
The command returns `0` for success, `1` for an operation failure, `2` for invalid usage or environment, and `130` for cancellation.
The service command reports current service checks after a failure.
`mc/cmd/mc/culler_prod_test.go` covers command outcomes and failure behavior.
`mc/deployment/lifecycle_test.go` covers the stop record and service definition checks.
`mc/cmd/mc/setup_update_stop_prod_test.go` checks the previous helper identity, helper-before-services order, and refusal after helper failure.
These source assertions remain separate from complete packaged restoration through macOS launchd.
