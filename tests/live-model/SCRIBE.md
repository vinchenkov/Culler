# Live Scribe acceptance

This opt-in command tests both configured harness families with real model calls.
The confirmation flag records the existing authorization for provider costs.
The fast suite never starts it.

The test requires these prerequisites:

- A host session outside a pipeline container.
- Docker Desktop with a current Culler runtime image.
- The fixed Go and Bun tools from `.mise.toml`.
- A host test binary with `test_fake_routing,test_scratch_spine`.
- The current source checkout with its shipped Scribe skills.
- A private test home below `~/.config/culler-tests/`.
- An absent proxy socket in that test home.
- One enabled `codex` profile and one enabled `claude-sdk` profile.
- A new absolute output directory outside the checkout.

The image must include the fixed OpenWiki package and both fixed harness packages.
The `--image` value must identify an immutable local image, such as `sha256:<64 hexadecimal digits>`.
The existing image build procedure supplies that image.
This test builds and deploys no image.

Run this command from the checkout within the authorized cost limit:

```sh
./tests/live-model/scribe.sh \
  --confirm-live-spend \
  --mc-home "$HOME/.config/culler-tests/proxy-acceptance" \
  --mc-binary /private/tmp/culler-scribe-host-mc \
  --image sha256:<64-hexadecimal-digits> \
  --codex-binding <enabled-codex-profile> \
  --claude-binding <enabled-sdk-profile> \
  --output /absolute/new-evidence-directory
```

The command resolves each profile through the normal host configuration surface.
It uses the selected profile's runtime configuration.
The command starts the normal foreground proxy and its private acceptance socket.
It stops that proxy after acceptance.
The test requires no resident, dashboard, or production deployment.
It creates a private scratch spine through `mc` for the proxy diagnostics.
Cleanup removes that scratch spine.
Provider credentials remain on the host.

Each profile receives a fresh repository, home directory, and native session directory.
The command mounts the current runner source read-only and invokes the normal Scribe adapter.
The adapter uses the private Bun and Node.js executables in `/opt/culler-runtime`.
The image package check runs without network access before proxy authorization.
The OpenWiki process uses the normal environment that excludes provider credentials.
Both adapters disable automatic project memory through their normal configuration.

Each live turn has a five-minute limit, two processor cores, and four GiB of memory.
The SDK adapter also has a 30-turn limit and a 4,096-token response limit.
The model uses the lowest configured test effort.
The Scribe test excludes `gpt-5-nano` after two bounded attempts failed the native lifecycle criterion.
The latest retained events confirmed that all six required lifecycle calls were absent.
This exception applies only to the Scribe test.
The ordinary proxy test still uses that model.
The Scribe test selects the next cheapest offered model with recorded price evidence.
The pinned Codex command supports an effort control but exposes no verified output-token limit.
The host permits at most 24 provider requests for each profile.
The Culler proxy token expires after the test deadline.
The host handles provider credential renewal.
The command revokes its Culler proxy token after the test.
The command makes one attempt for each profile and stops after a failure.

The test requires these results for each profile:

- A durable native session and a successful adapter terminal event.
- Successful native events for all six OpenWiki tools.
- A complete generated page with a Claim and repository evidence.
- An exact copy of the complete accepted report.
- A report entry in the generated wiki navigation.
- A completion receipt at `/tmp/openwiki-completion.json` inside the container.
- The fixed package identity and a matching digest of every wiki file.
- An absent page queue and absent document check state.

The output directory contains the test evidence:

- The resulting wiki.
- The source fixture.
- The `result.json` file.

The result records the acceptance evidence.
The command deletes each container after its attempt.
It also deletes the private fixture directory.
It excludes raw model output and credentials from the evidence directory.
A failed cleanup reports the container name and private directory for host recovery.

## Isolated profile Test and Save

The complete test can create its own dedicated profiles through the normal product Test and Save operations.
It uses existing protected test connections and preserves their credentials.
Its cleanup restores the prior test configuration and runner files.

The host binary requires the `test_fake_routing,test_scratch_spine` tags for the isolated image selector.
The real macOS credential store remains active.
The test leaves `CULLER_TEST_HOST_SECRET_DIR` unset.
The task image supplies the fixed harness packages.

```sh
mise exec -- bun tests/live-model/scribe-acceptance.ts \
  --confirm-live-spend \
  --mc-home "$HOME/.config/culler-tests/proxy-acceptance" \
  --mc-binary /private/tmp/culler-scribe-host-mc \
  --image sha256:<64-hexadecimal-digits> \
  --connections proxy-test-codex-api,proxy-test-claude-api \
  --output /private/tmp/culler-scribe-evidence
```

The complete test permits six profile model requests, 48 Scribe requests, and 20 model catalog requests.
The combined maximum is 74 provider requests.
Reuse of both profile tests reduces that maximum to 68.
The profile operations use the selected model's normal native defaults.
The Scribe turns use the explicit test effort and output limits above.
Each operation records its request count and cost estimate.
The product profile operation removes its native session, so its provider usage remains explicitly unavailable.
The Scribe results retain numeric usage when the harness supplies it.

A failed Scribe turn can reuse its successful profile tests with `--reuse-profile-tests <codex-operation>,<claude-operation>`.
The operation identifiers appear in `profiles.json`.
The product rechecks the saved model, connection, and authorization before it enables either profile.
Use `-` for a profile test that requires a new call, such as `--reuse-profile-tests -,<claude-operation>`.
Reuse of one profile test permits 71 total provider requests.
Reuse of both profile tests makes no new profile model calls.
A retry uses a new output directory and records its own Scribe request count.

`--harness codex` or `--harness claude-sdk` selects one Scribe turn after another harness passes.
The complete command tests and saves only that harness profile.
A selected harness permits 24 Scribe requests and ten catalog requests.
A new profile test adds at most three model requests.
