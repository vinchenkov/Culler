# Host acceptance

These checks use isolated temporary files and synthetic traces.
The fast component suite excludes this folder.

## Setup traces

This check uses the pinned AgentsView executable and the ordinary resident trace exporter.
It checks both native trace formats, authenticated access, direct session pages, new trace lines, and recovery after archive loss.
It changes no deployed configuration or trace file.
It starts one temporary AgentsView process on an available loopback port.

```sh
CULLER_AGENTSVIEW_BIN=/path/to/pinned/agentsview mise exec -- bun test ./acceptance/setup-traces.test.ts
```

The test checks the executable digest against `mc/deployment/agentsview.json`.
It removes its temporary files and stops its process after the check.

## Deployment schema

Setup trace identity adds two nullable fields to `environment_operations`: `native_session_ref` and `trace_filename`.
The deployment requires a compatible schema before activation.
History recovery requires the retained operation records and their source traces.
The coding harness must verify these requirements before deployment.
