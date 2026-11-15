# Provider proxy verification inventory

The controlled suite uses synthetic credentials and provider responses.
The live suite supplies separate evidence for each real connection method.
A test result describes only the paths that the test executes.

| Behavior | Required result | Executable evidence |
| --- | --- | --- |
| Native sign-in | The native program saves a protected account session. | `mc/deployment/runtimeauth_signin_test.go`, `runtimeauth_selected_test.go`, `tests/live-model/proxy.ts` |
| API key creation | One masked entry saves the key outside the container. | `dashboard/smoke/api-key-connection.smoke.test.ts`, `mc/deployment/provider_credential_test.go` |
| Complete key repair | The actual harness refusal reaches the dashboard; Replace API key and Test restore access. | `tests/provider-proxy/failure-repair.test.ts` |
| Complete failure attribution | Provider rate limits and host failures retain their scope through real diagnostic writes and browser recovery. | `tests/provider-proxy/failure-origin.test.ts` |
| Complete preliminary display | The native Collie container saves its draft before provider completion; the browser displays an active draft. | `mc/e2e/collie_native_proxy_test.go`, `tests/provider-proxy/collie-stream.test.ts` |
| Deletion | Active requests stop at every host proxy; stale consent preserves the replacement; a role assignment refuses deletion; no block or credential read remains. | `mc/deployment/connection_delete_test.go`, `dashboard/smoke/connection-delete.smoke.test.ts`, `resident/src/credential-projector.test.ts` |
| Saved credential after a failed test | The user can retry without another key or sign-in. | `dashboard/smoke/api-key-connection.smoke.test.ts`, `mc/deployment/runtimeauth_selected_test.go` |
| Exact profile verification | Another model or authorization cannot reuse a successful test. | `mc/deployment/agentprofiles_test.go`, `agentprofile_auth_verify_test.go`, `provider_acceptance_test.go` |
| Cancellation | A cancelled operation cannot publish a delayed credential. | `mc/deployment/runtimeauth_cancellation_test.go`, `agentprofile_testop_test.go` |
| Failed replacement | A rejected candidate preserves the previous key and authorization; that key still completes a native request. | `mc/deployment/api_key_validation_test.go`, `tests/provider-proxy/failure-repair.test.ts` |
| Credential isolation | Each container receives only scoped Culler access and public trust. | `mc/e2e/credential_projection_test.go`, `mc/boundarydocker/agent_user_test.go`, `resident/src/effects.test.ts`, `environment-lane.test.ts` |
| Detector accuracy | Each deliberate credential leak fails its detector; removal passes. | `tests/provider-proxy/leak-controls.ts`, `runner/shared/provider-leak-detector.test.ts` |
| Request authority | Unknown tokens, destinations, methods, paths, models, and excess requests fail before provider access. | `resident/src/provider-proxy.test.ts`, `provider-acceptance.test.ts` |
| Current launch authority | Ended or obsolete launches lose access. | `mc/verbs/providerlifetime_test.go`, `resident/src/provider-lifetime.test.ts` |
| Host restart | Previous tokens fail; replacement containers require fresh authority. | `resident/src/credential-projector.test.ts`, `collie-runtime.test.ts`, `resident-control.test.ts` |
| Conversation recovery | Native context remains available; an ambiguous reply requires explicit retry. | `mc/verbs/collierestart_test.go`, `colliesendresume_test.go`, `runner/collie-runner/main.test.ts` |
| Completed reply | Recovery preserves the durable reply and does not repeat execution. | `mc/verbs/connectionhealth_test.go`, `colliedraft_test.go`, `runner/collie-runner/main.test.ts` |
| Preliminary response | The pinned harness emits text before the provider response ends. | `tests/provider-proxy/harness.ts`, `dashboard/smoke/collie.smoke.test.ts` |
| Concurrent conversations | Each conversation retains its native identity and context. | `tests/provider-proxy/harness.ts`, `mc/verbs/colliemodel_test.go` |
| Shared renewal | Concurrent requests use one renewal; accepted rotation becomes durable before use. | `resident/src/provider-credentials.test.ts` |
| Automatic renewal recovery | A resident tick retries a due connection without a model request or user action. Successful renewal clears the condition and restores admission. | `resident/src/connection-recovery.test.ts`, `resident/testdata/connection-recovery-control.go` |
| Delayed old-token rejection | Current credentials and authorization health remain intact. | `resident/src/provider-credentials.test.ts`, `mc/verbs/provideraccess_test.go` |
| Diagnostic order | Duplicate and reordered reports preserve cause and allowance. | `mc/verbs/provideraccess_test.go`, `runner/agent-runner/diagnostics.test.ts` |
| Host failure | Keychain or proxy failures identify host recovery and preserve account health. | `mc/verbs/provideraccess_test.go`, `connectionhealth_test.go`, `resident/src/main.test.ts` |
| Provider refusal | Account, model, quota, and temporary failures retain their distinct remedies. | `mc/verbs/provideraccess_test.go`, `dashboard/src/ui/systemhealth.test.ts` |
| Certificate lifecycle | Private keys stay protected; leaf renewal preserves active streams and listener identity. | `mc/deployment/provider_tls_test.go`, `resident/src/provider-proxy.test.ts` |
| Unattended host storage | Isolated macOS Keychain records support large values, reads, and deletion. | `mc/deployment/host_secret_darwin_test.go`, `host_secret_lifecycle_test.go` |
| Original sessions | Deployment and adapters preserve original records and provider blocks. | `mc/deployment/host_release_test.go`, `runner/agent-runner/adapters/startup.test.ts`, `session-store.test.ts`, `runner/agent-runner/opencode.test.ts` |
| Trace copies | Copies mask identified credentials. Collie and AgentsView inspect copies. | `resident/src/trace-mask.test.ts`, `trace-copy.test.ts`, `trace-lane.test.ts`, `tests/provider-proxy/harness.ts` |
| Conversation errors | A rejected request preserves shared access and the identity of its reply attempt. | `mc/verbs/provideraccess_test.go`, `resident/src/provider-proxy.test.ts`, `provider-lifetime.test.ts` |
| Real provider protocol | Each real connection completes a shell tool and native continuation through the proxy. | `tests/live-model/proxy.ts` |
| Live test cost | The suite selects the cheapest compatible offered model and records bounded requests and available usage. | `resident/src/live-model-policy.test.ts`, `live-model-usage.test.ts`, `tests/live-model/proxy.ts` |
| Scribe tools | Both harnesses complete the OpenWiki tool protocol through the proxy. | `tests/provider-proxy/scribe-native.ts`, `tests/live-model/scribe.ts`, `resident/src/scribe-acceptance.test.ts` |

The controlled browser integration supplements the separate unit and display tests.
The native Collie test uses the actual resident, helper, container, adapter, proxy, and spine writes.
It proves a durable draft before terminal release and a final reply with exactly one provider request.
The browser test separately connects the production draft writer to the actual dashboard.
It retains actual status responses, diagnostic records, and repair commands.
The full component checks assess regressions outside this inventory.

The former Refresh Broker and credential-return tests have these replacements:

- Host renewal tests preserve grant serialization, expiry, failure classification, and durable rotation.
- Proxy tests verify credential isolation, request limits, response streams, and local refusal identifiers.
- Adapter tests verify public trust, Culler token use, native context, and token removal from retained files.
- Acceptance tests verify host request counts and exact profile authorization.

Real provider evidence does not establish provider terms or account eligibility beyond the successful requests.
A fresh native sign-in and a retained account session are separate evidence.
A host restart simulation does not establish behavior after an actual computer restart.
