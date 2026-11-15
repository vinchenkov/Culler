# Synthetic provider proxy tests

These tests use synthetic credentials and synthetic model responses.
They make no real provider calls.
The complete host suite requires macOS, Docker Desktop, and the fixed Go and Bun tools.
Portable component checks also run on Linux.
The complete browser model-test path requires the macOS host network path.

`resident/src/connection-recovery.test.ts` checks automatic renewal through the resident tick and the actual Go connection controls.
The test uses an isolated database and synthetic provider responses.
It checks recovery without a model request or user action.
The resident component check includes this test.
The [recovery record](RECOVERY.md) describes its scope and separate defects.

The complete host command prepares task images and dependencies, then runs the component, Docker, and browser checks:

```sh
./tests/provider-proxy/check.sh /private/tmp/culler-provider-proxy-evidence
```

The evidence directory contains commands' logs, source identity, image identities, test results, and browser evidence.
The command removes its task image tags after execution.
It preserves production image tags and services.
It fails when a required selected group executes no tests or skips a test.
Each underlying test removes its isolated containers, volumes, and files.

Run only the Docker boundary group with an existing task image:

```sh
cd mc
MC_BOUNDARY_IMAGE=mc-real-e2e mise exec -- go test -tags docker_boundary -count=1 -v ./boundarydocker \
  -run 'TestAgentProfile.*ModelDockerBoundary|TestCredentialLeakDetectorPositiveControlsDockerBoundary'
```

`harness.ts` runs the exact pinned harness packages through the current adapters.
Each harness uses both connection methods.
The test verifies these results:

- The harness trusts the configured certificate.
- The harness sends only its Culler token to the synthetic endpoint.
- Preliminary text arrives before the response completes.
- A shell command executes and produces its expected artifact.
- A deliberately echoed Culler token disappears from the retained native session.
- A second turn retains the same native session and prior response.
- Concurrent conversations retain separate context.
- A local authentication refusal retains its host request identifier without a retry.

`leak-controls.ts` proves that each leak detector rejects a deliberate leak.
The test then removes the leak and requires the detector to pass.
Its markers represent access tokens, refresh tokens, API keys, identity tokens, and private keys.
It covers these surfaces:

- Docker inspection output.
- Container and process environments.
- Container files and a named volume.
- Container logs.
- A saved image archive, including compressed layer archives.
- Scratch host configuration, dashboard responses, trace exports, and resident logs.

The controls verify the detectors.
Separate product tests apply these detectors to product artifacts.
The controls create isolated containers, a named volume, and a small image.
Their names start with `culler-leak-control-`.
The controls delete their resources after each test.

Real provider tests belong only in `tests/live-model/`.
