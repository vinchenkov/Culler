# Provider proxy verification record

This record describes the implementation branch `codex/agent-container-credentials`.
The base revision is `50fc0be8f424672b07cf32a4fe1489b3523863c0`.
The host is macOS with Docker Desktop.
The [inventory](INVENTORY.md) maps the behavior to executable tests.
The [controlled suite](README.md) uses synthetic credentials and responses.
The [live suite](../live-model/README.md) contains every test that requires real model responses.

## Implementation

The resident owns the provider proxy for Hypertext Transfer Protocol Secure (HTTPS).
The macOS Keychain holds provider credentials and private certificate material.
Each model container receives a scoped Culler token and a public certificate authority (CA) certificate.
The helper container receives no provider credential.
The Refresh Broker and container-accessible credential-return endpoints are absent.

The native ChatGPT and Claude sign-in flows remain available.
The dashboard and setup interface also accept application programming interface (API) keys.
Connection health distinguishes provider failures from host failures.
An exact successful model test controls profile activation.
An API key replacement must pass a bounded model-list check before it replaces the saved key.
The separate model test establishes inference access for the selected model.

## Automated results

| Check | Result | Retained evidence |
| --- | --- | --- |
| Complete Go gate | Passed; no skipped tests | `/private/tmp/culler-proxy-final-complete-mc.log` |
| Resident | Passed: 663 tests | `/private/tmp/culler-proxy-final-complete-resident.log` |
| Agent runner | Passed: 137 tests | `/private/tmp/culler-proxy-final-complete-runner.log` |
| Final live-suite unit checks | Passed: 20 tests, 92 assertions; zero model calls | `/private/tmp/culler-live-focused-final.log` |
| Other component checks | Passed | `/private/tmp/culler-provider-proxy-final3-evidence/` |
| Docker boundary suite | Passed | `/private/tmp/culler-provider-proxy-final3-evidence/docker-boundary.jsonl` |
| Resident and native Collie containers | Passed | `/private/tmp/culler-provider-proxy-final3-evidence/resident-container.jsonl` |
| Complete failure and repair cases | Passed: 20 tests, 528 assertions | `/private/tmp/culler-provider-complete-final.xml` |
| Complete dashboard suite | Passed: 291 tests across 50 files | `/private/tmp/culler-dashboard-final-all.xml` |
| Evaluation framework | Passed: 655 tests; zero model calls | `/private/tmp/culler-evals-final.log` |
| Evaluation container and adapter | Passed with synthetic responses | `/private/tmp/culler-evals-container-final.log`, `/private/tmp/culler-evals-adapter-smoke.log` |
| Linux deployment, boundary, configuration, and routing | Passed with three host checks unverified | `/private/tmp/culler-proxy-linux-focused-final-20261102.log` |
| Terminal review | Accepted at 40 and 80 columns, with and without color | `/private/tmp/culler-native-terminal-review/`, `/private/tmp/culler-api-key-replacement-terminal/` |

The complete failure cases use both native harnesses and both connection methods.
They cover provider rate limits, provider overload, host credential failures, and proxy failures.
They also cover first-use key rejection, rejected key replacement, successful repair, and preliminary Collie text.

The native Collie tests use the resident, helper, container, adapter, proxy, and actual spine writes.
Each test records a draft before terminal release and a durable reply with exactly one synthetic provider request.
Separate browser tests display those draft semantics through the actual dashboard.

One earlier full browser run reported a Board scroll-position failure.
The isolated test passed on this branch and the base revision.
The later complete suites passed on both revisions.
The tests did not establish a repeatable cause, and this change includes no Board layout fix.

## Real provider results

Each connection completed one shell tool and a second turn in the same native session.
Each connection used exactly three model requests.
These results total twelve model requests.
Model discovery and authentication requests are separate.
Additional profile and Scribe checks used forty-three model requests.
The complete task used fifty-five model requests.
The [live result record](../live-model/RESULTS.md) lists those checks and their limits.

| Harness | Connection | Selected model | Result |
| --- | --- | --- | --- |
| Codex | OpenAI API key | `gpt-5-nano` | Passed |
| Codex | ChatGPT sign-in | `gpt-6-luna` | Passed |
| Claude Code | Anthropic API key | `claude-haiku-5-5` | Passed |
| Claude Code | Claude sign-in | `claude-haiku-5-5` | Passed |

Fresh native sign-in also succeeded for each account connection.
The test sessions belong to the isolated test home.
The checks preserve the ordinary native sessions and production credentials.

The model policy selects the cheapest compatible model from the connection catalog and recorded official prices.
The policy records its price sources and refuses stale price evidence.
Subscription evidence records the absence of comparable allowance prices.
The price estimate does not establish a spend cap.
The proxy enforces the declared request limit and duration.

The earlier API and Claude account results contain no numeric usage record.
The ChatGPT result records cumulative conversation usage.
The report does not add resumed cumulative totals together.

## Evidence limits

An actual computer restart remains unverified.
That action affects production services and falls outside the authorized test scope.
The tests cover process restart, expired authority, retained context, and isolated Keychain access separately.
Unattended operation requires the operator's macOS user session and an unlocked login Keychain.

The Linux checks cover deployment, boundary, configuration, and routing behavior.
Linux leaves three macOS filesystem checks unverified.
The macOS checks cover those host requirements.
The complete browser model-test path requires the macOS host network path.

Legacy cleanup detects known credential values and recognized credential formats.
It preserves the native session identity and retained context.
It cannot identify an unknown credential after an arbitrary custom transformation.

Successful provider requests establish protocol behavior for those requests.
They do not establish provider terms or future account eligibility.

The implementation remains uncommitted on the local task branch.
The final source identity is `/private/tmp/culler-provider-proxy-final-source.json`.
The task preserves the production deployment and the unrelated edit in the main checkout.
