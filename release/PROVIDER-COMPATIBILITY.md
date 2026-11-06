# Provider compatibility during an update

Culler checks saved choices against the candidate release before the user confirms an update.
The check reads configuration and makes no provider request.
The check preserves saved connections, profiles, models, and explicit effort choices.

## What the update checks

The candidate configuration parser checks the complete saved configuration.
An unsupported configuration format or option prevents replacement.
The updater records the configuration checksum before confirmation.
It repeats the check after the services stop.
A changed configuration requires another assessment before replacement.

For every saved OpenCode profile, the candidate checks these choices:

- The connection names a provider in the candidate catalog.
- The provider includes the saved model.
- The model supports the explicit effort among the choices that Culler exposes.

The candidate also checks saved OpenCode connections that no profile uses.
These checks use the catalog that the candidate binary embeds.
They require no model request or credential disclosure.

An empty effort means **Default**.
Culler preserves that empty value.
OpenCode and the provider control the behavior of that default.
The update does not promise identical reasoning, output, latency, or cost.

## What a catalog problem does

A catalog problem identifies a difference between saved choices and the candidate catalog.
It does not prove that the provider removed a model.

A disabled profile keeps its choices and displays its problem in the dashboard.
It does not prevent the update.
An enabled profile receives a visible warning before update confirmation.
The confirmed update can complete with that profile unavailable for new work.

The launch checks enforce that restriction for these uses:

- Pipeline runs and wiki updates.
- Setup agents.
- New Collie sessions and resumed Collie sessions.

Other valid profiles remain available.
A profile problem consumes no task attempt.
Culler does not replace the model or remove an explicit effort.

The user repairs the profile through the dashboard.
The user can select an available model and effort or assign another valid profile.
The existing profile test can verify the selected connection with a provider request.

## What the update cannot prove

A local catalog check cannot prove current provider availability.
The provider can change access after Culler publishes a release.
These changes include model removal, account permissions, authentication requirements, and service outages.

Culler preserves the saved choice when a provider refuses a request.
The dashboard provides the connection and profile controls for repair.
A provider outage does not permit an automatic model substitution.
The user can retry a connection or authorize a different choice.

The update does not test every provider, model, and effort combination.
Culler supports the saved configuration format, its adapter behavior, and exact forwarding of explicit choices.
Live provider behavior remains a separate check.

## Evidence for a dependency change

The release manifest identifies native formats through their producer versions.
A changed harness or plugin requires an explicit accepted source identity before a public update can proceed.
That declaration requires test evidence.
It is not a substitute for the evidence.

For an OpenCode change, the maintainer supplies these checks:

- The exact archive contains the pinned OpenCode program and plugin.
- Local catalog tests cover an absent provider, an absent model, an invalid explicit effort, and **Default**.
- Adapter tests cover each distinct request path and exact effort forwarding.
- A saved native session remains readable and can continue with the candidate producer.
- The candidate runtime and optional archive execute in each selected Project image.

Credential-free tests use controlled fixtures.
Live checks use a small declared set of connections with explicit credentials.
Live checks remain outside pull-request tests and the fast suite.
An unavailable provider remains an unverified live check.

The maintainer records behavior changes in the release information.
Changes to provider defaults require that disclosure even when the saved empty effort remains unchanged.
The maintainer never adds an accepted native format solely to make publication pass.

## Implementation references

- `mc/cmd/mc/setup_update_candidate_prod.go` performs the candidate assessment and isolated Project image checks.
- `mc/opencodecatalog/catalog.go` validates saved OpenCode choices.
- `mc/verbs/runtimeauthority.go` excludes invalid profiles from launch capabilities.
- `mc/verbs/dispatchseam.go` refuses affected pipeline work before a task attempt.
- `mc/verbs/dispatchverb.go` refuses affected role selections.
- `mc/distribution/update_policy.go` checks public source versions and persistent format identities.
- `mc/deployment/agentprofiles.go` supplies profile problems to the dashboard.
