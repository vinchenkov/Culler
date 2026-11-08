---
type: operations
title: Agent connections and access renewal
description: How account sign-in and API keys supply agent profiles through the host provider proxy.
tags: [configuration, credentials, agents, security]
sources:
  - id: openwiki-source-8f16f07389eccb57a739027f
    resource: repo://dashboard/src/ui/systemhealth.js
  - id: openwiki-source-096a781fb160ef979fa31121
    resource: repo://INSTALL.md
  - id: openwiki-source-cd6f7a4ea29f9d0931880195
    resource: repo://mc/boundary/envpolicy.go
  - id: openwiki-source-02886ae4c9efa40e167cf53b
    resource: repo://mc/config/config.go
  - id: openwiki-source-4b4fe89674376e41801774a5
    resource: repo://mc/deployment/agentprofile_auth.go
  - id: openwiki-source-52fc8d31cb52929022117055
    resource: repo://mc/deployment/agentprofile_models.go
  - id: openwiki-source-43964424fef4e0fd3d42eefc
    resource: repo://mc/deployment/agentprofile_testop.go
  - id: openwiki-source-23f70b586a2d1069b5d784e4
    resource: repo://mc/deployment/agentprofiles.go
  - id: openwiki-source-dc8d3c70a68adffcb07de33a
    resource: repo://mc/deployment/api_key_validation.go
  - id: openwiki-source-414aa10e02df8456691e0f9b
    resource: repo://mc/deployment/opencode_source_docker_test.go
  - id: openwiki-source-c3574bc58db62fef903f9230
    resource: repo://mc/deployment/opencode_source.go
  - id: openwiki-source-c55cdefe405ad9fe4d481e84
    resource: repo://mc/deployment/provider_credential.go
  - id: openwiki-source-da04f25cb9fa15d5a8447612
    resource: repo://mc/deployment/runtimeauth_setup_login.go
  - id: openwiki-source-1a0baff337229874d8b3640a
    resource: repo://mc/verbs/connectionhealth.go
  - id: openwiki-source-aa950240d47a765b29e8be03
    resource: repo://mc/verbs/providerlifetime.go
  - id: openwiki-source-229935765a2017befaf3bc59
    resource: repo://mc/verbs/systemstatus.go
  - id: openwiki-source-3fc938040bede67eab99b0e0
    resource: repo://resident/src/agent-profile-runtime.ts
  - id: openwiki-source-473a9f9ce4c4f72240c3f031
    resource: repo://resident/src/connection-recovery.test.ts
  - id: openwiki-source-b65f5016ff00568fc3622948
    resource: repo://resident/src/main.ts
  - id: openwiki-source-fca867d7fea1f4f6917fe44b
    resource: repo://resident/src/provider-credentials.ts
  - id: openwiki-source-67e3529d84ef7148d75124ac
    resource: repo://resident/src/provider-lifetime.ts
generated: { by: "culler-scribe", at: "2026-11-08T02:41:27.984Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-08T02:41:27.984Z
---

# Agent connections and access renewal

Culler supports these connection methods:

- ChatGPT account sign-in through Codex.
- Claude account sign-in through Claude Code.
- An OpenAI application programming interface (API) key.
- An Anthropic API key.
- An API key for another provider in the release catalog, through OpenCode.

A connection identifies one account or API key.
An agent profile selects a connection, a harness family, a model, and an optional effort level.
Several profiles can share one connection.
Role assignments select profiles across Projects.

The operator manages profiles through Model configuration in the dashboard.
The installation wizard establishes and tests initial access.
See [Installation, deployment, and release](install-and-deployment.md) for the installation entry points.

## Configuration and connection creation

`mc/config/config.go` validates `MC_HOME/config.toml`.
The release catalog fixes each provider destination and account authorization configuration.
The supported connection kinds are:

- `codex-oauth`.
- `claude-oauth`.
- `codex-api-key`.
- `claude-api-key`.
- `opencode-api-key`.

The harness family must match the connection kind.
The configuration contains no provider credential values.
Custom provider destinations fail validation.
An OpenCode connection selects one release catalog entry instead of an address.
The catalog defines the provider, model, effort variants, prices, context limits, and web search capability.

The dashboard offers Connect, Configure, and Test and save.
Native sign-in uses the existing provider authorization process.
Each native sign-in uses private host directories outside deployment mounts.
These directories remain separate from the operator's normal native login.
The host removes each temporary credential source after import.

The API key form contains one concealed key field.
A successful save clears that field.
The key stays outside browser storage, command arguments, and diagnostic records.
The host refuses a replacement that names an obsolete authorization revision.
The Codex and Claude API integrations check a replacement key against the provider's model catalog before save.
This check makes no model call.
A rejected or incomplete check preserves the previous key, authorization revision, and configuration.
A successful check verifies catalog access; the model test still verifies the selected model.

A saved connection and a successful model test remain separate results.
A failed model test preserves the saved connection and the nonsecret form values.
A retry uses the saved connection without another sign-in or key entry.
A profile save requires a successful model test of its current values.
Configuration validation checks that a selected effort belongs to the harness's supported levels.
The model catalog gives the supported model levels when the provider supplies them.
When that data is absent, the dashboard offers the harness levels and the model test checks the selected level.
The host checks a selected effort against the model list before it runs the model test.
The model test reports an effort error when that level is unavailable.
An empty effort uses the harness default.

### OpenCode connections

**Use another provider's API key** opens the catalog search.
The search accepts a provider name, identifier, or full model address.
Each provider row shows its address and website before the concealed key prompt.
The release catalog supplies the model list without a live provider request.
The model test verifies the saved key and selected model.
An existing release integration keeps its harness when its address matches a catalog entry.

In a published installation, the first OpenCode connection starts a separate download of the pinned program and web search plugin.
The model and effort lists remain available during the download.
The model test waits for a successful download.
A failed download preserves the connection and key and offers **Retry**.
Each effort selects a catalog model variant; **Default** uses the provider default.
The dashboard states whether the connection supports web search.

Source deployments prepare and verify OpenCode before service activation, even without an OpenCode connection.
The first connection uses that verified package without another download, including after the source checkout moves.
The source image supplies npm and the other package tools; the host requires no npm installation.
Preparation records readiness through the same operation status that the dashboard and terminal setup read.
A preparation failure prevents activation and records its stage and cause.
Successful preparation or verified reuse clears an obsolete failure status.

`mc/opencodecatalog/` owns the release catalog and profile validation.
`mc/deployment/opencode.go` owns the download, archive checks, and deployment-specific volume.
`mc/deployment/opencode_source.go` owns source preparation, package identity, verification, and reuse.
A published installation downloads OpenCode only when a connection requires it.
Published updates replace an existing volume with the archive of the selected release.
Both uninstall modes remove the volume.
See [source package preparation and reuse](image-delivery.md#source-package-preparation-and-reuse) for package checks and repair.

The Connections disclosure offers Disconnect for each saved connection.
The confirmation stops active requests and removes the saved credential.
Disconnection preserves agent profiles, assignments, and separate native sessions.
The result states whether the provider confirmed revocation.
Local disconnection alone does not establish provider revocation.

## Host credential storage

The macOS Keychain stores complete provider credentials and the private proxy certificates.
`MC_HOME/refresh-grants/<connection>.json` contains only a protected credential reference and its authorization revision.
The directory requires owner-only access.
Each reference file uses mode `0600`.
The host verifies a protected copy before it replaces a legacy credential file.

A credential replacement creates a new authorization revision.
An account token renewal preserves that revision and changes the protected credential reference.
The host refuses a renewal result from an obsolete credential or authorization.
A failed protected save preserves the previous connection.

`resident/src/provider-credentials.ts` reads credentials through host-only commands.
It serializes account renewal for each connection.
It saves and verifies a renewed session before it supplies that session to a provider request.
Static API keys require replacement only when the operator changes them.

## Automatic account renewal

Before each dispatch, the resident reads the connection conditions from `mc system status`.
It starts account renewal for `token_refresh` conditions in `unknown` or `retry_wait` state.
The renewal runs without a wait for the provider response, so the resident can continue dispatch.
The connection check admits a renewal only after its retry time and permits one active check.
It refuses a `stopped` connection until the operator repairs the authorization.

The credential service records the provider result and its original failure evidence.
When renewal fails, the resident cancels its check without another failure observation.
A transport failure keeps the retry schedule and blocks new agent runs on that connection.
A successful renewal clears the condition and restores launch admission.
An obsolete result cannot change the current credential state.

The synthetic [recovery tests](../../tests/provider-proxy/RECOVERY.md) verify these paths.
That record also lists separate limits in temporary renewal, restart behavior, and repeated authorization rejection.

## Container access

Each container receives a temporary Culler token and the public proxy certificate.
The token permits only its assigned connection, model, operations, and lifetime.
The host proxy attaches the provider credential to a permitted request.
Both connections use HTTPS.
Provider credentials remain on the host for all three harness families.

`mc/verbs/providerlifetime.go` derives a setup or validation token lifetime from the active environment operation.
The operation can be queued or running, so the resident can grant access before the first heartbeat.
The launch identity must match the operation, and the Project must remain active.
The deadline stays 30 minutes after operation creation.
A completed operation or a replaced active operation loses access.
`resident/src/provider-lifetime.ts` applies each lifetime result to the host proxy token.

The proxy checks the method, path, query fields, model, request size, and request limits.
It rejects an unsupported destination and an upstream redirect.
It replaces caller authentication with host authentication.
It removes credential values from provider responses and diagnostic records.
Revocation ends the token's active requests.

The adapters remove ambient provider credentials and provider destination overrides.
The native harness receives the Culler token and the proxy destination.
The environment policy in `mc/boundary/envpolicy.go` also rejects provider credential variables.
Project tool secrets remain subject to their separate environment policy.

## Connection diagnostics

The host records each request with a Culler request identifier and its failure origin.
A proxy authorization failure differs from a provider rejection.
A locked Keychain requires host recovery.
Unattended operation requires the operator's macOS user session and an unlocked login Keychain.
After a computer restart, macOS must restore those conditions before Culler can use provider connections.
A rejected API key requires a replacement key.
An account renewal rejection can require another native sign-in.
A quota failure directs the operator to the provider account allowance.

Model tests preserve their selected model and tool evidence.
A successful HTTP response alone does not establish a successful model test.
A later request cannot repair an obsolete authorization or an unrelated profile.
See [Failure controls](../concepts/failure-controls.md) for task retry behavior.

System conditions display the configured profile name, with the model as a fallback.
The diagnostic facts retain the raw profile identifier.
A timeout can change the connection revision while its attempt remains active.
The diagnostic keeps that active attempt visible through its run or attempt identity until it ends.

## Verification

The fast suites use synthetic credentials and local provider fixtures.
The isolated macOS test checks actual Keychain access with synthetic data.
Real provider tests reside under `tests/live-model/` and use the cheapest compatible models.
The test fixture backend exists only in `test_fake_routing` builds.
It accepts synthetic provider credentials and requires a private directory outside deployment mounts.
