# Real model tests

Every executable test in this directory requires real provider responses.
These tests can incur API charges or consume subscription allowance.
The ordinary component, browser, and Docker suites exclude this directory.

The proxy test uses the product connection interface, host credential store, proxy, and pinned harnesses.
Each connection executes one shell tool and one native session continuation.
The host permits at most three model requests and five minutes for that connection.
The model catalog permits at most five requests for each connection.
The combined maximum is eight provider requests for each connection.
Import of an API key adds at most one non-model authentication probe for that key.
The probe occurs only when the import replaces an existing protected key.
Each probe has a ten-second deadline.
The test reports its combined request limit before execution.

The host requires these inputs:

- A current host test binary with `test_fake_routing,test_scratch_spine`.
- A task image from `runner/image/build.sh --real`.
- A private test home below `~/.config/culler-tests/`.
- Dedicated account sessions or the private API key file.

Build both test artifacts from the checkout root:

```sh
(
  cd mc
  mise exec -- go build -tags=test_fake_routing,test_scratch_spine \
    -o /private/tmp/culler-proxy-host-mc ./cmd/mc
)
MC_TEST_IMAGE=culler-proxy-real-test ./runner/image/build.sh --real
```

These commands create the host binary and task image used in the example below.
The image build also creates `culler-proxy-real-test-project`.
The build preserves production image tags.
The test resolves the selected image once before credential access.
Every test container uses that immutable image identifier.
The evidence records both the original selector and the immutable identifier.

The test creates a new private scratch spine through `mc`.
Its preflight verifies the schema and write access before provider access.
A production binary refuses this test-only host operation.
Cleanup removes the scratch spine.
The evidence retains bounded diagnostic metadata.

The test home uses mode `0700`.
The key file is `~/.config/culler-tests/live-model.env`.
Its directory uses mode `0700`, and its file uses mode `0600`.
The file contains `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` assignments.
The test reads those assignments as data.
It keeps the file outside image contexts and container mounts.

The following command imports both keys through the normal host connection interface.
Cleanup removes only the imported test keys with the same authorization revision.
It preserves the source key file and dedicated account sessions.

```sh
mise exec -- bun tests/live-model/proxy.ts --confirm-live-spend \
  --home "$HOME/.config/culler-tests/proxy-acceptance" \
  --mc-binary /private/tmp/culler-proxy-host-mc \
  --image culler-proxy-real-test \
  --connections proxy-test-codex-api,proxy-test-claude-api \
  --import-api-keys \
  --evidence /private/tmp/culler-live-api-evidence.json
```

Tests of existing protected key connections omit `--import-api-keys`.
Dedicated account tests select their saved connection identifiers through `--connections`.
They use the same test command and proxy boundary.
Native sign-in requires the provider's browser consent when no valid dedicated session exists.
The test never imports the operator's ordinary native session.

`models.json` records official price sources and compatible economical models.
The test selects the cheapest offered model from that evidence.
It refuses unknown model prices and evidence older than 30 days.
The estimate uses expected input and output tokens and records its assumptions.
The estimate is not a spend cap.
The host request limit remains the execution bound.
The Codex test sets low effort through the pinned native interface.
Its native tool set includes web search, which rejects minimal effort.
That interface exposes no verified output-token limit.
The Claude test sets low effort and a 1,024-token response limit.
Synthetic native tests verify those settings in actual provider requests.

Each result records the model, request count, native continuation, and tool artifact.
It records numeric usage from the harness when available.
Unavailable usage remains explicit.
Subscription results identify allowance use separately from API charges.
The test excludes credentials, temporary Culler tokens, and raw provider output from evidence.

The [Scribe test](SCRIBE.md) checks real OpenWiki tool use through the same proxy boundary.
It also requires the cheapest compatible model for each selected test profile.

The [controlled suite](../provider-proxy/README.md) supplies repeatable faults without real provider calls.
