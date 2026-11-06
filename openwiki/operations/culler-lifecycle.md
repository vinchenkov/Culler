---
type: operations
title: Culler lifecycle command
description: The operator command controls local services, reports their state, and checks for updates.
tags: [operations, lifecycle, launchd]
verified:
  - by: openwiki/0.7.0
    at: 2026-11-05T05:07:38.661Z
sources:
  - id: openwiki-source-de03631980be3775e6b20749
    resource: repo://mc/cmd/mc/culler_prod.go
  - id: openwiki-source-c0c8c1965a051d7700920804
    resource: repo://mc/cmd/mc/culler_services_prod.go
  - id: openwiki-source-a69635b1c1f62a5cb37186fd
    resource: repo://mc/deployment/lifecycle.go
generated: { by: "culler-scribe", at: "2026-11-05T05:07:38.661Z" }
---

# Culler lifecycle command

The `culler` command controls the local Culler deployment on the Mac host.
The operator uses the dashboard or Collie for configuration.
The command accepts `--mc-home` for another deployment and `--no-color` for plain output.
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
| `culler update` | Open the installer in the terminal. |
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

## Results and diagnostics

Status reads local service checks without a release network request.
Update check reads release availability without an installation.
The logs command reads a bounded tail and exits.
The command returns `0` for success, `1` for an operation failure, `2` for invalid usage or environment, and `130` for cancellation.
The service command reports current service checks after a failure.
`mc/cmd/mc/culler_prod_test.go` covers command outcomes and failure behavior.
`mc/deployment/lifecycle_test.go` covers the stop record and service definition checks.
