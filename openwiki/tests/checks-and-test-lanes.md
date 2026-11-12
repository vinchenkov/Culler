---
type: tests
title: Checks, test lanes, and evaluations
description: The fast suite of per-component check.sh scripts, what mc/check.sh and mc/test.sh enforce, the opt-in Docker, nightly, and browser lanes, the CI jobs, and the separate Packager evaluation framework.
tags: [testing, ci, check.sh, e2e, evals]
sources:
  - id: openwiki-source-4d1d392666be6dfdd7a91a2e
    resource: repo://.github/workflows/release.yml
  - id: openwiki-source-6f13d0daf8751c1237cda839
    resource: repo://dashboard/smoke-gate.ts
  - id: openwiki-source-625e188f9a51f295b304424b
    resource: repo://dashboard/smoke.sh
  - id: openwiki-source-d11ad730400b322fe1904062
    resource: repo://dashboard/smoke/api-key-connection.smoke.test.ts
  - id: openwiki-source-6f67a284a812c80a8c3f44f9
    resource: repo://dashboard/smoke/discord-conversation.smoke.test.ts
  - id: openwiki-source-90e8fc313d2eb084ce9b2e61
    resource: repo://dashboard/smoke/discord.smoke.test.ts
  - id: openwiki-source-6ff7ec04aac5c9f6567d2d84
    resource: repo://dashboard/smoke/harness.ts
  - id: openwiki-source-3543262fff6631f316da46e4
    resource: repo://dashboard/smoke/latency.smoke.test.ts
  - id: openwiki-source-4ea15ae4c53141155926c420
    resource: repo://dashboard/smoke/model-configuration.smoke.test.ts
  - id: openwiki-source-85db7682a784da205fd6cd4c
    resource: repo://dashboard/smoke/packet-chat.smoke.test.ts
  - id: openwiki-source-813caded59ec98bbd274a4c1
    resource: repo://dashboard/smoke/report-frame.smoke.test.ts
  - id: openwiki-source-614e912b08da5924b7d82e0a
    resource: repo://dashboard/smoke/theme.smoke.test.ts
  - id: openwiki-source-7c4f80bb47c6f9d6e14cc99f
    resource: repo://dashboard/src/security-gate.test.ts
  - id: openwiki-source-57f23fa446ee816a9629303a
    resource: repo://dashboard/src/smoke-gate.test.ts
  - id: openwiki-source-e51b5dc1a5c6bbfd524e7ccd
    resource: repo://dashboard/src/ui/review.test.ts
  - id: openwiki-source-c02688fe098052a93aa61796
    resource: repo://dashboard/src/ui/scene.test.ts
  - id: openwiki-source-a53a0703fceb74e7d8de47c1
    resource: repo://dashboard/src/ui/systemhealth.test.ts
  - id: openwiki-source-9ea19154aa59c39cc41bced6
    resource: repo://dashboard/src/ui/theme.test.ts
  - id: openwiki-source-6d6fcc0012d90237873626ea
    resource: repo://evals/check.sh
  - id: openwiki-source-65883ffe29cd4af8710495d9
    resource: repo://evals/culler_evals/cli.py
  - id: openwiki-source-43c99b2bf6bbadedfe48eeb3
    resource: repo://mc/check.sh
  - id: openwiki-source-956e6a6507b5fa74d11c5e2e
    resource: repo://mc/cmd/mc/agent_skill_prod_test.go
  - id: openwiki-source-54e7de3880f31c0fd5089921
    resource: repo://mc/cmd/mc/arbiter_criteria_test.go
  - id: openwiki-source-a7ca3be58a8971f19014beb8
    resource: repo://mc/cmd/mc/cli_test.go
  - id: openwiki-source-d9949d9a9d2f424493fa411e
    resource: repo://mc/cmd/mc/culler_prod_test.go
  - id: openwiki-source-e43cd30f2cb134a8cbad79f9
    resource: repo://mc/cmd/mc/setup_connection_choices_prod_test.go
  - id: openwiki-source-84c1006ce1b2be1373838f69
    resource: repo://mc/cmd/mc/setup_opencode_screen_prod_test.go
  - id: openwiki-source-ba92167dda59467828397749
    resource: repo://mc/cmd/mc/setup_opencode_source_prod_test.go
  - id: openwiki-source-ae83eaa3edce8d9e15735e02
    resource: repo://mc/cmd/mc/setup_untested_connections_prod_test.go
  - id: openwiki-source-69a26a0f5b89ca934021a7bf
    resource: repo://mc/cmd/mc/setup_untested_connections_screen_prod_test.go
  - id: openwiki-source-7db06a144aa42e48eb68568a
    resource: repo://mc/cmd/mc/setup_update_candidate_prod_test.go
  - id: openwiki-source-579b9c04dbd98eaf836c5c4c
    resource: repo://mc/cmd/mc/setup_update_candidate_prod.go
  - id: openwiki-source-769bbb5f564fdccd1e6ebbb1
    resource: repo://mc/cmd/mc/setup_update_stop_prod_test.go
  - id: openwiki-source-07cdfe29b43b302d6030a5c9
    resource: repo://mc/cmd/mc/systemstatus_setup_test.go
  - id: openwiki-source-9f816a6d1f5994d193ba7ab4
    resource: repo://mc/config/agentprofiles_test.go
  - id: openwiki-source-a1ac2c30b885592aaecb74c3
    resource: repo://mc/deployment/agent_skill_test.go
  - id: openwiki-source-e52c730e2e47b3a8099643b1
    resource: repo://mc/deployment/agentprofile_models_test.go
  - id: openwiki-source-01b3ed2178627bc0a419fe9f
    resource: repo://mc/deployment/agentprofile_testop_test.go
  - id: openwiki-source-31704b283966353f2dbd4bbd
    resource: repo://mc/deployment/cli_install_test.go
  - id: openwiki-source-414aa10e02df8456691e0f9b
    resource: repo://mc/deployment/opencode_source_docker_test.go
  - id: openwiki-source-2d611a4b791928013c2f5ff3
    resource: repo://mc/deployment/opencode_source_program_test.go
  - id: openwiki-source-94a13a8eafcc90f4670b41f5
    resource: repo://mc/deployment/opencode_source_test.go
  - id: openwiki-source-cf4736c38e48c2b892615899
    resource: repo://mc/deployment/opencode_test.go
  - id: openwiki-source-f2370cd7c903f813dbc4ad1c
    resource: repo://mc/deployment/runtimeauth_host_sources_test.go
  - id: openwiki-source-d0b091e57853771f453ce84a
    resource: repo://mc/deployment/runtimeauth_setup_login_test.go
  - id: openwiki-source-e1cbcf72fcc0e3291147da09
    resource: repo://mc/deployment/runtimeauth_verifier_test.go
  - id: openwiki-source-f7942d03e00cb4bf890dfd6f
    resource: repo://mc/deployment/update_checkpoint_docker_test.go
  - id: openwiki-source-8cdaa71a8672d00c472d655b
    resource: repo://mc/dispatch/dispatch_phase2_test.go
  - id: openwiki-source-9f9aa8c7d686dddb13c6a5b9
    resource: repo://mc/dispatch/priority_test.go
  - id: openwiki-source-4df7c93afbb316f003a94e95
    resource: repo://mc/distribution/record.go
  - id: openwiki-source-587a5c662cbe23556b5136d7
    resource: repo://mc/e2e/collie_packet_artifacts_test.go
  - id: openwiki-source-d30dd1702c428f2ab74b5b6d
    resource: repo://mc/e2e/credential_projection_test.go
  - id: openwiki-source-ea49a40872eecce65130f399
    resource: repo://mc/e2e/e2e_test.go
  - id: openwiki-source-bdb34dc5adc4a25690eda4c2
    resource: repo://mc/evalcase/README.md
  - id: openwiki-source-21ce972eae0f79cc3c8b6d7d
    resource: repo://mc/onboarding/terminal_search_test.go
  - id: openwiki-source-c2af084a67a7e25f724fdf19
    resource: repo://mc/property/dispatch_case_test.go
  - id: openwiki-source-01b61ecca94c01db2c10f8d3
    resource: repo://mc/test.sh
  - id: openwiki-source-b4da7cec419704ae15f02306
    resource: repo://mc/verbs/arbiter_input_test.go
  - id: openwiki-source-3a72fe65b15fd6f2f9988f26
    resource: repo://mc/verbs/colliepacket_test.go
  - id: openwiki-source-d51bf11cb67af161f4de940e
    resource: repo://mc/verbs/customization_test.go
  - id: openwiki-source-bdcdf989ab8381c989787604
    resource: repo://mc/verbs/directives_test.go
  - id: openwiki-source-959768f661d526f65eea9d8d
    resource: repo://mc/verbs/directives/packager.md
  - id: openwiki-source-db55b76d7f0573ce8b068ebf
    resource: repo://mc/verbs/directives/verifier.md
  - id: openwiki-source-117392b618e5fbf773f5eb79
    resource: repo://mc/verbs/dispatchcorrection_test.go
  - id: openwiki-source-58989c94d2eb04ff452f5b9d
    resource: repo://mc/verbs/dispatchorder_test.go
  - id: openwiki-source-6a1e391719e8dc303420ffc3
    resource: repo://mc/verbs/dispatchverb_test.go
  - id: openwiki-source-e3610626a23cdc8d1f3a1018
    resource: repo://mc/verbs/packetcontext_test.go
  - id: openwiki-source-ff3195b0a5a37a2736e48a3b
    resource: repo://mc/verbs/platform_input_test.go
  - id: openwiki-source-95d6e106a75ae807cab7e009
    resource: repo://mc/verbs/provider_renewal_test.go
  - id: openwiki-source-cdcae076ed897cdf74637751
    resource: repo://mc/verbs/providerlifetime_test.go
  - id: openwiki-source-0097b0a41bffd53dce10abf3
    resource: repo://mc/verbs/skills/verifier/mc-verifier/SKILL.md
  - id: openwiki-source-9cde79f59cdb3b1ba6b34d30
    resource: repo://mc/verbs/skills/verifier/verify-behavior/SKILL.md
  - id: openwiki-source-4565b2c94892e8cea0c22610
    resource: repo://mc/verbs/strategist_input_test.go
  - id: openwiki-source-fe13f5a431cbfaf8f480dd65
    resource: repo://mc/verbs/verifier_input_test.go
  - id: openwiki-source-a2a135f3f8016d9eeb4be1d9
    resource: repo://mc/verbs/workspacesetup_test.go
  - id: openwiki-source-d52f6b19a62da2af5f6d937e
    resource: repo://release/check.sh
  - id: openwiki-source-525363b51889e1eeb6aa600f
    resource: repo://release/image_contents_test.py
  - id: openwiki-source-4b2923d58836e80cd70ddec6
    resource: repo://release/image_load_test.py
  - id: openwiki-source-f75eda6c3cdd48976dd36eeb
    resource: repo://release/security_acceptance.py
  - id: openwiki-source-34fd3fd4d6f7d65629db18e4
    resource: repo://release/security_test.py
  - id: openwiki-source-d98254c64a3b0af4e5dc117a
    resource: repo://release/size_report_test.py
  - id: openwiki-source-28cb905efa6ae5924f0edc26
    resource: repo://resident/bunfig.toml
  - id: openwiki-source-473a9f9ce4c4f72240c3f031
    resource: repo://resident/src/connection-recovery.test.ts
  - id: openwiki-source-ed495aeb639dfd64cad84dde
    resource: repo://resident/src/dependency-reuse.ts
  - id: openwiki-source-a34ade05dc0d7c781a7d0f1d
    resource: repo://resident/src/main.test.ts
  - id: openwiki-source-41da535b70edb44b29c38c2c
    resource: repo://resident/src/setup-proxy-start.test.ts
  - id: openwiki-source-cb29828fa65995fca70eafea
    resource: repo://runner/agent-runner/adapters/adapters.test.ts
  - id: openwiki-source-049143960b4daa8f1c93b13c
    resource: repo://runner/agent-runner/opencode.test.ts
  - id: openwiki-source-5bafebdedf9045e1ae8594df
    resource: repo://runner/image/browser-smoke.test.sh
  - id: openwiki-source-729fed85a7f093179cea3802
    resource: repo://runner/image/project-smoke.sh
  - id: openwiki-source-4f081c9b33aa27c81df3d636
    resource: repo://runner/openwiki/compatibility.test.ts
  - id: openwiki-source-2c053b8b3481c318e616a628
    resource: repo://runner/shared/test-environment.ts
generated: { by: "culler-scribe", at: "2026-11-12T09:22:03.309Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-12T09:22:03.309Z
---

# Checks, test lanes, and evaluations

Culler's verification is split into lanes by cost and dependency. The **fast suite** runs on every change. It needs no Docker and no model tokens. **Opt-in lanes** need Docker, a browser, or a long budget and run at phase completion. **Continuous integration (CI)** in `.github/workflows/release.yml` runs the fast suite, the browser suite, and release checks. The **evaluation framework** under `evals/` is a separate developer tool. It measures Packager directive quality and never ships.

Run every `go` and `bun` command through `mise exec --`. `.mise.toml` pins Go and Bun.

## The fast suite

Each component has a `check.sh` entry point. The full fast suite is:

```sh
./mc/check.sh && ./runner/fake-harness/check.sh && ./runner/agent-runner/check.sh && \
./runner/collie-runner/check.sh && ./runner/setup-runner/check.sh && ./runner/shared/check.sh && ./runner/image/check.sh && \
./resident/check.sh && ./dashboard/check.sh
```

| Component | What its `check.sh` runs |
| --- | --- |
| `mc` | The Go lint gate and test suite (below). |
| `runner/fake-harness`, `runner/agent-runner`, `runner/collie-runner`, `runner/setup-runner`, `runner/shared` | `bun test`. `runner/shared` covers the structured logger whose output must match Go's `mc/mclog` byte for byte (see [logging](../concepts/logging.md)). |
| `runner/image` | Shell tests for `build.sh`, `build-prod.sh`, the browser and toolchain smokes, `release/check.sh`, `runner/openwiki/check.sh`, and `bun test mc-land.test.ts`. |
| `resident` | `bun test`, with fake timer, exec, and filesystem seams plus split-brain acceptance against a real test-tagged `mc` binary. |
| `dashboard` | `bun test src/`: unit tests only, with no browser (see [the dashboard](../architecture/dashboard.md)). |

There is no TypeScript lint, format, or typecheck command; `bun test` is the only Bun verb. Agents run the same checks in an agent container. Linux test builds of `mc` use the `test_scratch_spine` tag. In an agent container, each host-scope test that cannot run is reported as unverified. Each Bun component loads `runner/shared/test-environment.ts` through its `bunfig.toml` before its tests. That file removes the run variables and points `MC_RUN_JSON` and `MC_RUN_LOG_DIR` at absent paths, so the code under test finds no run identity and logs to stderr.

### mc/check.sh

`mc/check.sh` is the full Go lint gate. In order, it runs:

1. `gofmt -l .`, failing on any listed file.
2. `go vet ./...` untagged.
3. `go vet -tags <tag> ./...` for `nightly`, `docker_e2e`, `docker_boundary`, `update_docker`, `test_fake_routing`, and `test_scratch_spine`. Tagged suites do not run in the fast lane, but they must still compile.
4. `GOOS=linux GOARCH=arm64 go vet ./...`, without a tag and with `test_scratch_spine`. The fast lane normally runs on macOS, but the only production implementation of the spine lock-domain guard (`mc/substrate/lockdomain_linux.go`) is Linux-only.
5. `./test.sh -count=1 ./deployment/ ./testenv/`. These tests read files outside the Go module, which Go's test cache does not track. `-count=1` prevents a stale cached pass.
6. The shell contract tests: `install_test.sh`, `redeploy_test.sh`, `scripts/source-update-guard_test.sh`, and `uninstall_test.sh`. These Docker-free tests use fake tools (see [installation and deployment](../operations/install-and-deployment.md)).
7. `./test.sh -timeout 20m ./...`, which runs all Go tests without a tag on macOS and with `test_scratch_spine` on Linux.
8. On Linux only, `./test.sh -count=1 -tags= ./substrate/ -run '...'`. This step runs the filesystem guard tests again without `test_scratch_spine`, so the production guard stays proven.

### mc/test.sh: zero tests is a failure

`mc/test.sh` wraps `go test -v "$@"`. On Linux it adds `-tags=test_scratch_spine`, and a `-tags` argument of the caller replaces that selection. When `go test` fails, it prints the full output and exits with that status. When `go test` succeeds but the output contains no `=== RUN` line, it fails with "The selected test group executed zero tests." Otherwise it prints only the `ok` and `?` package lines. After each package line, it prints one `UNVERIFIED <package> <test>: <reason>` line for each skipped test of that package. A last line states the number of skipped tests. A mistyped `-run` pattern or a package whose tests are all behind a build tag therefore cannot pass silently. Use it for single tests:

```sh
cd mc && ./test.sh ./dispatch/ -run '^TestName$'
cd <component> && mise exec -- bun test <file> -t '<name>'
```

`mc/testenv` isolates Go tests from inherited run variables. Its variable list matches the Bun preload.
Host-scope tests still require their actual platform or filesystem dependency.
The Linux CI job fails when a production filesystem guard check reports `UNVERIFIED`.

The same invariant appears elsewhere. `release/check.sh` fails when unittest discovery selects zero tests. The CI fast-suite job fails a non-`mc` component whose log lacks a positive `N pass` line.

### Platform limits

The portable deployment tests run on macOS and Linux. The macOS deployment tests validate generated plists with `/usr/bin/plutil`; see `mc/deployment/supervision_plist_darwin_test.go`. `*_darwin_test.go` files need macOS, so a Linux run leaves them unverified. A required check whose dependency is unavailable stays unverified. A task pass requires successful criteria and new or changed tests, plus a broad assessment with no unresolved task regression.
The host source sign-in tests restore write access before they rename a sealed credential directory in their replacement fixture. This lets the fixture run on macOS 15 while it checks cleanup after directory replacement.

## Task verification and base comparisons

The Verifier runs criterion checks, new or changed tests, and normal broad Project checks on the submitted commit.
Repository instructions define broad checks and the conditions for additional lanes.
The Verifier preserves required build and analysis checks.
Successful submitted checks require no base execution for regression detection.
New or changed tests require successful submitted results without a required failure on the base.
A criterion about an earlier state still requires evidence of that state.
One execution can supply both criterion evidence and broad evidence.
A source file read alone cannot verify behavior whose consumer runs outside the container.

For each failed check present on the base, the Verifier compares the smallest group that reproduces the failure.
Each commit uses its own declared dependencies and configuration under comparable conditions.
A separate source copy and separate build files protect the submitted repository.
Relevant test order and prerequisites remain part of the comparison.
Package build failures need assessment before conclusions about tests that could not start.

The same check and failure cause on both commits establish an unchanged failure.
Two nonzero exit statuses alone establish no such result.
The Verifier investigates inconsistent results with limited repetitions and records inconclusive comparisons.
An unresolved executable failure prevents PASS.
A task regression requires CORRECT, including a regression outside the criteria.
New skips and lost assertions also require assessment.

An early failure leaves later independent checks within scope.
The Verifier runs those checks separately when their prerequisites are available.
An unchanged failure explains only the reproduced failure, not the tests that it prevented from executing.

A zero exit status means the command succeeds; its skipped tests remain unverified.
A nonzero exit status remains unsuccessful even when the task receives PASS.
Unchanged failures and unavailable host tests outside the criteria permit PASS when the other requirements hold.
The unavailable-host-test exception excludes new or changed tests, which must pass.
A criterion that promises repair of an old defect still requires that repair.
A criterion without its required evidence remains unmet.
Separate release and host acceptance requirements retain their authority.

The Verifier evidence preserves each exact `UNVERIFIED` entry. The packet groups checks and shared coverage limits while preserving material results and base comparisons.
These observations alone create no mandatory manual check, separate repair task, or additional approval requirement.
The source rules are `mc/verbs/directives/verifier.md` and `mc/verbs/skills/verifier/mc-verifier/SKILL.md`.
See [the task pipeline](../workflows/task-pipeline.md) for verdict effects and packet duties.

### Unavailable checks

The `mc-verifier` skill selects outcomes for an unavailable criterion check or new or changed test by cause.

| Cause | Verifier outcome |
| --- | --- |
| Source, tests, Workspace dependencies, or setup script defects that the Worker can change. | CORRECT identifies the prerequisite and required source or declaration changes. |
| An Image dependency can supply the prerequisite. | CORRECT lets the Worker propose the image change. |
| No agent container has the required capability. | An operator decision report identifies the required decision and why it prevents completion. |
| The cause remains unclear. | CORRECT. |

Host access, a Docker daemon, macOS interfaces, and additional container privileges are examples of absent capabilities.
An unavailable check that supplies the only criterion evidence leaves that criterion unmet.
Unavailable host tests outside the criteria permit PASS when the other requirements hold, except for new or changed tests.
Separate release and host acceptance requirements retain their authority.
See [decision resolution](../workflows/decision-resolution.md#role-input-and-unavailable-checks) for the Worker procedure and report resolution.

The Verifier preserves the submitted dependency versions and checks their compatibility with the selected image.
It recreates Workspace dependencies from submitted declarations.
The Verifier keeps the submitted repository clean, restores paths that checks change, and writes logs and scratch files elsewhere.
Checks that can exceed thirty seconds or produce verbose output run in the background with a recorded exit status.
A check without terminal status remains unverified.
The evidence identifies optional checks outside the required scope as not applicable (N/A), with a reason.

### Role instruction checks

These tests check the instructions and procedures delivered to agents:

| Source | Evidence checked |
| --- | --- |
| `mc/verbs/strategist_input_test.go` | Strategist assignment data, proposal authority, procedure pointers, delivered skills, and single occurrences of selected role rules. |
| `mc/verbs/arbiter_input_test.go` | Arbiter assignment data, review constraints, conditional procedures, delivered skills, and preservation of each brief payload. |
| `mc/verbs/verifier_input_test.go` | Assessment belongs to the customization contract; briefs preserve assignment and case data while procedures remain discoverable through skills. |
| `mc/verbs/platform_input_test.go` | Worker and Verifier outcomes preserve required tests, independent work, failed-read terminals, and unverified evidence. |
| `mc/verbs/directives_test.go` | Role duties include broad checks, comparison by failure cause, unavailable-check outcomes, and contract conflict procedures. |
| `mc/verbs/brief_test.go` | Role briefs retain their required parts and concise explanations. |
| `mc/verbs/roleskills_test.go` | Skill pointers, command authority, explicit terminals, and browser commands remain available. |

`TestStrategistInputDeliversRulesOnceAndResolvesProcedures` renders a brief with proposals, task history, custom instructions, a proposal resolution, and the accepted Vision.
It checks the repository path, preserved instructions and resolution, and installed paths for every delivered skill.
It checks the command skill's conditional procedures and the directive's execution, operator decision, and customization pointers.
Selected rules must occur once across the rendered brief and `mc-strategist` skill.
The task description audit checks every rule once across the rendered brief and all delivered skills.
The test also checks proposal requirements in the extracted customization contract and excludes unrelated system explanations.

`TestStrategistBriefDeliversTaskSelectionGuidance` checks the directive inside the rendered brief.
Its assertions cover useful results, existing capabilities, repository evidence, full cost, rationale review, and the Arbiter's final rank.
It also checks that custom instructions remain within the Culler contract.
`TestDirectivesEncodeTheSelfOrchestrationContract` checks fixed orchestration procedures for the Worker, Verifier, and Packager.
`TestWorkerRecordsInlineExceptionBeforeTheTerminal` checks where the Worker states its reason for inline work.

`TestStrategistBriefPreservesAssignmentData` checks that brief simplification preserves each retained part's facts, text, rows, and text tag.
`mc/verbs/brief_history_test.go` separately checks history limits, order, Project isolation, and cancellation excerpts.
`TestArbiterInputPreservesAssignmentAndDeliversRulesOnce` checks the Arbiter's use of historical work and cancellation reasons.
The Strategist tests check the supplied duplicate-check rules and task-read procedure.
These tests establish instruction content; they do not prove proposal quality or task selection by an agent.
See [Strategist input](../concepts/agent-input.md#strategist-instructions-and-procedures) and [proposal submission](../workflows/task-pipeline.md#strategist-research-and-submission).

The Arbiter input test renders a brief with proposal descriptions, active descriptions, task history, custom instructions, a proposal resolution, and the accepted Vision.
It checks the run identity, repository path, and installed path of every delivered skill.
It checks pointers and sections for historical work, all-reject batches, failed results, and customization conflicts.
Selected role rules occur once across the rendered brief and `mc-arbiter` skill.
The task description audit includes all delivered skills.
The contract assertions cover the promotion limit, criteria rules, preservation of accepted work, and restricted authority.
The customization assertion requires an explicit conflict terminal in the skill.

`TestArbiterBriefPreservesPartPayloads` checks that the brief transformation preserves facts, text, rows, and text tags for each retained part.
`TestDispatchSpawnBriefCarriesClaimedState` checks proposal descriptions, historical entries, and the Vision-based ranking instruction in the dispatch output.
`TestArbiterBriefDeliversProposalReviewGuidance` checks task results, feasible benefits, full cost, and consideration of new capabilities.
The Arbiter input test checks the required exception for each rejection in an all-reject batch.
It also rejects fixed orchestration text and a Research dependencies section in the combined brief and command skill.
The dependency test in `directives_test.go` excludes the Arbiter from its role-specific dependency assertions.
Its customization test checks the contract boundary and procedure pointer text, but does not check the named skill.
See [Arbiter input](../concepts/agent-input.md#arbiter-instructions-and-procedures) for the current procedure locations.
These checks establish delivered instruction content and payload preservation; they do not prove an agent's review quality.

The `verify-behavior` skill applies when a criterion or broad check needs browser interaction or visual confirmation.
It selects the installed browser with `--browser=chromium` and defines evidence media and session cleanup.
`runner/image/browser-smoke.test.sh` checks that browser selection in the Verifier and Packager browser skills.
These are source-defined assertions. This wiki update does not report fresh execution or prove that an agent follows the instructions.

### Invalid criteria checks

`mc/cmd/mc/arbiter_criteria_test.go` exercises all-invalid and mixed duplicate/invalid rejection batches.
It checks archival, retained reasons, lease release, completed runs, and subsequent Strategist dispatch without an operator decision.
Malformed categories, missing reasons, conflicting exceptions, invalid references, pool mismatches, and wrong identities must leave the run unchanged.
A blocked proposal after the pool snapshot tests transaction rollback.
A separate test permits an ordinary all-reject batch when other work remains ready.
`mc/cmd/mc/cli_test.go` retains checks for duplicate reference validation.
These statements describe test assertions, not test execution during this wiki update.

## Task description and Strategic rationale checks

`TestTaskDescriptionSkillIsTheOneStandard` in `mc/verbs/roleskills_test.go` checks delivery to the Strategist, Arbiter, and Collie.
It checks directive references and refuses commands inside the content standard.
It also checks that other role instructions do not repeat the standard's rules.
`requireTaskDescriptionRulesOnce` checks each rule across a complete Strategist or Arbiter input.

`mc/verbs/strategic_rationale_test.go` checks these boundaries:

- Missing, blank, oversized, or invalid rationale input causes proposal rejection without partial writes.
- The Arbiter and Packager receive the rationale; Worker and Verifier briefs omit it.
- Task reads omit the rationale for the Worker and Verifier while permitted decision readers receive it.
- Packet text and Collie context use the correct opening for each task origin.
- The role input describes a one-sentence Vision hypothesis, supporting evidence, explicit assumptions, and the first packet section.
- The Packager directive owns the opening requirements, and the command skill points to that directive.

`mc/substrate/task_model_test.go` checks immutable rationale storage and the operator origin constraint.
`mc/cmd/mc/cli_test.go` checks refusal of a Collie allowlist with `task.add` and retains host task creation at Seeded.
`dashboard/smoke/strategic-rationale.smoke.test.ts` checks task details, report sections, and absent-report fallback text for both task origins.
The browser checks use `./dashboard/smoke.sh` and retain screenshots through the shared evidence path.
The smoke uses fixture reports; it does not prove that a live Packager follows its directive.

The direct-page check in `dashboard/smoke/latency.smoke.test.ts` visits a blank page before it starts each request counter.
It removes prior request routes and waits for their completion.
This prevents requests from the prior page from entering the next page's initial-read count.
The assertions check one read per path, Configs-only doctor reads, and narrow layout containment.

These descriptions identify source assertions. This wiki update does not execute the product checks.

## Task priority checks

The dispatch tests check the next eligible run within one Project.
They distinguish task priority from the precedence of an operator approval.

| Source | Behavior checked |
| --- | --- |
| `mc/dispatch/priority_test.go` | Higher task priority precedes later stages and merge repairs. Equal priorities favor repairs after approval, then tasks that received a Worker run. |
| `mc/dispatch/dispatch_phase2_test.go` | Priority precedes stage among tasks with live packets at review capacity. |
| `mc/dispatch/dispatch_test.go` | The order retains stage, age, and task number as later comparisons. |
| `mc/verbs/dispatchorder_test.go` | Delivery records distinguish initial work, repairs before and after approval, repeated conflicts, completed repairs, and operator revisions. |
| `mc/verbs/dispatchcorrection_test.go` | A higher priority starts before a merge repair through the actual dispatch and repository workflow. |
| `mc/cmd/mc/cli_test.go` | A Strategist proposal with `priority` fails. A valid batch then creates proposals at priority 2 and releases the lease. |

`TestNextAgentRunOrder` checks that a live packet remains necessary at review capacity, even for an expedited task.
`TestApprovalReservesTheProject` checks approved merge precedence over an expedited task.
`TestACorrectionPreservesTheActiveRunAndPause` checks the active lease and explicit pause.
These are source-defined checks. This wiki update reports no fresh execution of these tests.
See [dispatch priority rules](../workflows/dispatch-and-tick-loop.md#priority-rules) for the complete order and its retained facts.

## Opt-in lanes

Run these at phase completion, never in the fast suite:

| Lane | Command | What it proves |
| --- | --- | --- |
| Docker end-to-end | `cd mc && mise exec -- go test -tags docker_e2e -v -timeout 30m ./e2e/...` | One task moves through the full pipeline (tick, dispatch, lease, fake-harness Worker, `mc complete`, packet, approve, apply) through the real `mc`, the real resident on its timer, and real containers. The tests never call `mc dispatch`, so every advance is timer-driven. Other files cover backpressure, correction rallies, the fault matrix, Collie conversations, landing failure, and role skill isolation. |
| Docker boundary | `cd mc && mise exec -- go test -tags docker_boundary -count=1 ./boundarydocker/...` | Container boundaries: agent user, agent profiles, attachment canaries, doctor probes, the landing envelope, ownership, the production image, and wiki export. |
| Nightly properties | `cd mc && mise exec -- go test -tags nightly ./property/...` | `TestDispatchStateFuzzer` and metamorphic invisibility properties over `dispatch.Decide`. |
| Dashboard smoke | `./dashboard/smoke.sh` | Real Chromium against a real `mc` on a scratch spine. |
| Update checkpoint | `cd mc && mise exec -- go test -tags update_docker -count=1 ./deployment -run '^TestUpdateCheckpointDocker' -timeout 12m` | Scratch spine and optional runtime recovery after an interrupted restore. |

The update checkpoint lane requires `MC_UPDATE_TEST_IMAGE` and an absolute `MC_UPDATE_TEST_BINARY` for a Linux command built from the checkout.
It uses existing images and scratch volumes without host service activation.
`release/UPDATES.md` describes the separate packaged acceptance procedure on a Mac or isolated deployment.

`mc/e2e/doc.go` carries no build tag, so the untagged toolchain reports `mc/e2e [no test files]` instead of "matched no packages". The property package shares its case generator between lanes. The untagged `TestGeneratorHonesty` and `TestPlantedMutantsKilled` run in the fast suite and use the same corpus as the nightly properties. As a result, the fast lane cannot certify a different generator than the one the nightly lane uses.

The dashboard smoke harness requires `MC_SMOKE_BIN`, which `smoke.sh` sets. Never run smoke files directly. Use `bootSmoke()`/`teardown()`, one Chromium per Bun process, and keep `data-testid` hooks stable. Smokes prove dashboard behavior only.
The harness checks the scratch home's `discord.socket` path before it creates the home. It rejects paths above 103 bytes with a request for a shorter `MC_SMOKE_ROOT`. The CI browser job uses `$RUNNER_TEMP/smoke` for that root.

### Background picture checks

`dashboard/src/ui/theme.test.ts` checks the background picture selection, button state, tab updates, and theme-specific grid tokens.
It checks that only a click starts motion, while a theme change or another tab's selection ends it.
It also checks image preparation when the logo receives pointer entry or keyboard focus.
`theme.test.ts` checks the light default with absent, invalid, or blocked browser storage.
`dashboard/src/agentsview-pages.test.ts` checks the light default and stored dark selection for trace access pages.

`dashboard/src/ui/scene.test.ts` checks the separate picture parts and their motion:

- The image cuts fit the light and dark sheets and preserve the required paint order.
- The parts reach their positions, move separately, and leave the window at the end of a hide.
- The shell stays closed until the chick appears and closes again before the egg leaves.
- The shadows follow the parts, and the whole egg rolls without a slip.
- A second click reverses active motion and cancels motion that awaits its images.
- Completion removes the temporary parts and gives the page to the static picture or tile lines.
- Reduced motion, a hidden page, absent elements, failed images, and zero picture width preserve the selected static state.

`dashboard/smoke/theme.smoke.test.ts` checks the light default with either system theme.
It checks successful delivery and content types for both landscape images and both picture part images.
It also checks the logo button in Chromium.
The smoke checks the picture after a click and reload, the veil, the button state, and the chick animation period.
The motion test samples separate part positions, the shell crack, the chick's jump, and the return to the shell.
It checks reversal, final picture alignment, removal of temporary parts, and immediate state changes with reduced motion.

`selectTheme` in the smoke harness selects a theme through the dashboard button.
`MC_SMOKE_COLOR_SCHEME=dark` supplies an explicit stored dark selection when the browser context starts.
The smoke captures the shown, hidden, and intermediate motion states through `evidencePath()` under `theme/`.
See [Dashboard background picture and motion](../workflows/dashboard-background.md) for the controller and its failure behavior.
These statements describe test assertions. This wiki update does not execute the unit or browser tests.

## CI: .github/workflows/release.yml

The workflow runs on pull requests and pushes to `main`. Its default permission is `contents: read`, and pull request jobs hold no provider credentials or publication rights.

| Job | Runs on | Checks |
| --- | --- | --- |
| `fast-suite-macos` | `macos-15` | Installs locked dependencies and runs nine component checks with a zero-pass guard, then `./release/check.sh`. |
| `application-package` | `macos-15` | Runs `release/build.sh` and `release/validate_package.py` alongside the checks and preserves the release inputs. |
| `deployment-linux` | `ubuntu-24.04-arm` | `./mc/test.sh -count=1 ./deployment/` and the Linux filesystem-guard tests in `./substrate/` without `test_scratch_spine`. |
| `dashboard-browser` | `ubuntu-24.04-arm` | Runs the complete `./dashboard/smoke.sh` suite and retains its log, report, and browser evidence. |
| `security-acceptance` | `ubuntu-24.04-arm` | Exercises the real Trivy scanner with controlled advisory databases. No job waits for it. |
| `release-images` | `ubuntu-24.04-arm` | Builds both images, checks both browsers and image contents, writes a size report, and saves the exact checked archives. |
| `image-vulnerabilities` | `ubuntu-24.04-arm` | Reports Trivy vulnerability results for the exact saved archives. No job waits for it. |
| `release-checks` | `ubuntu-24.04-arm` | Requires the fast suite, package, deployment, browser, and image jobs to succeed; failed, cancelled, or skipped results fail the gate. |
| `product-image` | `ubuntu-24.04-arm` | Push only. Checks and loads the saved archives before registry upload. |
| `release-downloads` | `ubuntu-24.04-arm` | Push only. Uploads the release files, verifies downloads, and publishes the release. |

The release record retains five required checks in `mc/distribution/record.go`.
The browser requirement and the `release-checks` requirement belong to the workflow dependency graph.
The image job starts after `application-package`.
Both publication jobs require `dashboard-browser` and `release-checks` from the same workflow run.
`install.sh` requires each release record check once with a successful result.

`./release/check.sh` runs the release tools' Python tests locally with fixtures and no credentials. It also syntax-checks `release/build.sh` and `runner/image/product-smoke.sh`.
The Python tests cover the image archive reader, image content rules, and size report. The release image job applies the content rules to both saved images. It writes the size report from those same archives. See [image delivery](../operations/image-delivery.md) for the checked file rules.

## Release security scans

`./release/check.sh` runs the unit tests of the scan reports and the image load without scanner downloads or network access.
The scans report results only, so no scan result fails a release check.

| Test source | Behavior under test |
| --- | --- |
| `release/security_test.py` | Severity counts, packages without a version, absent inventories and version records, masked reports, job summaries, and scanner download retries. |
| `release/image_load_test.py` | The image identity is the digest of the image configuration, and the check refuses an image of another commit. |
| `dashboard/src/security-gate.test.ts` | Required `release-checks` prerequisites, no job that waits for a scan job, fixed action references, read-only pull request jobs, exact image reuse, and nightly report retention. |
| `release/security_acceptance.py` | The real Trivy scanner reports a new advisory for an unchanged image. A failed tool or an absent image layer leaves the scan unverified. The report of a failed tool omits a credential from the tool output. |

The acceptance lane needs the fixed Trivy executable and the Go tool in `release/security/fixture-db`.
It stays separate from `./release/check.sh`.
Its advisory fixture changes only the database download response for one unchanged image.
Trivy identifies the added high-severity advisory, and the report of that image lists it.
A scanner outage or incomplete coverage leaves the scan unverified.

The nightly workflow scans the current release images with fresh advisory data and retains reports for 90 days.
See [Release security scans](../operations/release-security.md) for report contents, coverage limits, and GitHub secret scanning.
These descriptions establish test contracts, not a new scanner execution or live release assessment.

## Required browser checks

`./dashboard/smoke.sh` is the local entry point for the required browser job.
The browser suite remains separate from the fast suite.
The script builds a scratch `mc` with `test_fake_routing,test_scratch_spine`, unless `MC_SMOKE_BIN` supplies an executable.
It uses the default Project image Playwright installation when available.
Otherwise it installs the locked dashboard dependencies and matching Chromium.

The CI job uses the Go and Bun versions from `.mise.toml` and Node.js 24.18.0.
It installs Playwright from `runner/image/project-tools/package-lock.json`, then installs that Playwright version's Chromium.
The job runs on pull requests and pushes to `main`, with read-only repository permission and no provider credentials.
A browser or test failure fails the job.

The script writes a JUnit report and checks it through `dashboard/smoke-gate.ts`.
The gate rejects these results:

- A missing report or invalid report root.
- Zero executed tests.
- Any failure, error, or skipped test.
- A default suite run whose report omits a smoke test file.

A selected subset still requires executed tests and no failure or skip.
`dashboard/src/smoke-gate.test.ts` checks report rejection, command exit codes, default suite selection, and workflow dependencies.
Its source checks also require smoke screenshots to use the shared evidence path.

`MC_SMOKE_REPORT` selects an absolute report path. The default report lives in temporary scratch storage.
`MC_SMOKE_EVIDENCE_DIR` selects the screenshot directory used by `evidencePath()` in `dashboard/smoke/harness.ts`.
The CI artifact `culler-dashboard-browser-<attempt>` retains the log, report, and screenshots for seven days, including failed runs.
These descriptions identify the check contracts; they do not report a new browser execution.

## Report page style checks

`dashboard/smoke/report-frame.smoke.test.ts` supplies a full report document with competing root, body, and stylesheet declarations.
The test checks the computed styles at viewport widths of 1280 and 390 pixels.
It checks these results:

- The root and body retain the light report sheet (`#fdfdfe`) and have no background image.
- The page retains the light color scheme and the body retains navy text (`#172f47`), Figtree font, size, and line height.
- The body retains its padding and zero margin.
- The local diagram retains its grid layout and cell background.
- The document has no horizontal overflow for this fixture.

The test saves `packet-style-1280.png` and `packet-style-390.png` through the shared `evidencePath()` helper.
It restores the original viewport after the checks.
The fixture constrains its link blocks to the available width.
These assertions cover this report fixture; they do not establish that arbitrary report layouts fit every viewport.

`dashboard/src/ui/review.test.ts` checks the styled empty body and stable report serialization with link rules.
The browser smoke checks actual computed styles; the unit tests check the document construction and link behavior.
The browser suite uses `./dashboard/smoke.sh` as described above.
This wiki update reports source inspection, not a fresh test execution.
See [report page style](../architecture/dashboard.md#report-page-style) for the implementation.

## Evaluations

`evals/` is a Python framework (`uv`, `pyproject.toml`, `uv.lock`). It evaluates and evolves the Packager directive with GEPA. `evals/SPEC.md` is its implementation brief. The spec requires that it live outside product releases and that the deployed product stay independent of it. The framework is not in the fast suite or in CI.

- `evals/check.sh` runs `uv run --locked culler-evals check`, which runs `pytest` over `evals/tests/` and makes zero model calls.
- `evals/culler_evals/` holds the CLI, protocol, runtime, evaluator, records, and viewer. `evals/config/` fixes models, datasets, and rubric weights.
- `evals/cases/` holds the case corpus. `corpus.json` assigns 35 pinned external Memos changes to 13 training, 13 validation, and 9 final cases across seven change types.
- `evals/bridge/` connects to product code. `contracts.go` is compiled only in a temporary copy of the Go module, so the product source stays unchanged. The TypeScript bridges drive the product harness adapters, and `container-smoke.py` runs a token-free container check.
- `evals/VERIFICATION.md` records completed framework checks. It states that no measured directive improvement has been established. `evals/CALIBRATION.md` defines how human reference scores calibrate the Judge, which remains uncalibrated.

`mc/evalcase` is the product side. When the development flag `capture_evaluation_cases` authorizes a Packager run, the resident captures the effective input, a Git bundle, artifacts, and the packet under `MC_HOME/evals/cases/` with owner-only access. `mc/evalcase/store_test.go` verifies reconstruction after source removal, submission retry and tamper rejection, traversal and symlink refusal, and retention boundaries. The dashboard's evaluation controls mirror these host commands.

The release image jobs build separate Project and runtime images and check both.
The setup runner has its own component check and Docker boundary tests.
For experiment methodology and retained results, see [performance and evaluations](performance-and-evaluations.md).
This wiki update describes test contracts from source. It does not establish a fresh execution of those suites.

## Packet conversation checks

The packet conversation crosses the spine, the host container launch, the runner, and the Review page.
Its checks cover different boundaries:

| Source | Behavior under test |
| --- | --- |
| `mc/verbs/colliepacket_test.go` | Current packet and version checks, concurrent preparation, resume, deletion, immutable identity, archive refusal, and model restrictions. |
| `mc/verbs/packetcontext_test.go` | Packet identity and content, mutable state exclusion, media replacement, report fences, size limits, digest checks, and absent artifacts. |
| `mc/verbs/taskartifacts_test.go` | Task folder resolution, absent folders, and refusal of folders outside the expected task path. |
| `mc/cmd/mc/packet_list_selection_test.go` | Task selection through the real command interface. |
| `resident/src/effects.test.ts` and `collie-envelope.test.ts` | Task artifact mounts, Project artifact masks, and rejection of altered mount envelopes. |
| `runner/collie-runner/main.test.ts` | Packet context on the first native session turn, retained context on later turns, and context identity checks. |
| `dashboard/src/ui/packetchat.test.ts` | Conversation selection, earlier packet links, and width calculations. |
| `dashboard/smoke/packet-chat.smoke.test.ts` | Browser layout, shared history, draft retention, packet changes, decisions, and archived Project behavior. |
| `mc/e2e/collie_packet_artifacts_test.go` | Real container reads of the task report and evidence, refused writes, and isolation from another task. |

The Go and Bun component checks cover the unit tests.
The browser tests require `./dashboard/smoke.sh`.
The artifact container test belongs to the separate `docker_e2e` lane.
Test definitions establish intended checks; they do not establish a fresh successful execution.
This wiki update inspects source and test assertions. It does not report a new application test run or live acceptance.
See [packet conversations](../workflows/packet-conversations.md) for the behavior these checks address.

### Collie divider browser checks

`dashboard/smoke/packet-chat.smoke.test.ts` checks the divider through pointer and keyboard input.
The pointer test expands the column from 400 to 620 pixels and checks the matching reader width reduction.
It checks that the report iframe remains intact and the composer preserves its draft.
It also checks width retention across packet changes, close and reopen, and reload.
A double-click restores the default width.

The keyboard test checks 24-pixel arrow steps, Home, End, and the accessible width values.
The maximum width leaves a 400-pixel reader and hides the queue.
The minimum width is 320 pixels and permits the queue to return when space allows.
The test reduces the window width, checks the narrow layout, and restores the window to check the retained preference.
It also checks composer containment and the absence of browser page errors.

The tests save wide, maximum, and minimum width screenshots through `evidencePath()`.
These assertions define source coverage. This wiki update did not execute the browser suite.
See [the dashboard](../architecture/dashboard.md#packet-quick-chat) for the width controller.

## Project dependency reuse checks

The storage checks distinguish source isolation, persistent dependencies, and private role files.
The normal component suites contain these checks:

| Source | Behavior under test |
| --- | --- |
| `mc/cmd/mc/project_home_test.go` | Home creation, retained executable permissions and symbolic links, distinct Projects, scope refusal, and unsafe paths. |
| `mc/verbs/mountattest_test.go` | Creation and preservation of the attested Project home. |
| `mc/verbs/wikiround_test.go` | A Scribe mount plan with its isolated repository and Project home. |
| `resident/src/environment-lane.test.ts` | Setup home mounts, separate temporary role directories, disk reports, and cache removal. |
| `resident/src/effects.test.ts` | Pipeline home mounts and failure when required mount evidence is absent. |
| `runner/shared/project-cache.test.ts` | Generic file retention, eviction, separate home accounting, cache removal, and retained npm archive import. |
| `mc/cmd/mc/uninstall_modes_prod_test.go` | Project home preservation for keep-data and removal for full wipe. |

`./resident/dependency-reuse.sh` is a separate Docker acceptance lane.
It starts scratch setup and pipeline containers through the resident's launch functions with a fake harness.
Fixed npm, Bun, and uv fixtures populate normal home caches during setup.
A fresh Worker then prepares dependencies without network access and executes the dependency checks.
The fixture also exercises concurrent roles, container removal, a new host process, another image, and a separate Project.
It checks retained tools, symbolic links, configuration, legacy archives, and cache removal.
The script removes its scratch containers, volumes, image tags, and files afterward.

This lane requires Docker and compatible Project and runtime images.
It stays outside the fast suite and uses no provider credentials.
The assertions define what a successful execution proves. This wiki update did not execute this lane or the application suites.
See [Project environments](../workflows/project-environments.md#runtime-and-per-run-dependencies) for the implemented lifecycle.

## Discord checks

The Discord checks separate simulated network behavior from retained live acceptance evidence.
`resident/src/discord-config.test.ts` supplies an injected clock, Discord API, and connection factory.
It checks confirmation expiry, changed destinations, stale revisions, failed delivery, account replacement, preview expiry, and failed replacement preparation.
`mc/verbs/discord_test.go` checks persistent availability, preserved dashboard alert routes, and retired messages that cannot receive delivery acknowledgement.
`mc/config/recovery_test.go` checks saved configuration recovery after activation failure.

`dashboard/smoke/discord.smoke.test.ts` uses the real dashboard, host commands, spine, and local configuration socket.
Discord responses and the Gateway receiver are simulated.
The smoke checks unavailable section access, setup, invalid tokens, failed delivery, reload, account approval, alert changes, edit cancellation, and disconnect.
It saves screenshots at 1280 and 390 pixels in both themes through `evidencePath()`.
The normal `./dashboard/smoke.sh` entry point runs these checks.

`mc/verbs/colliethread_test.go` checks one session per thread, source message replay, idle resume, retirement, and dashboard replies to a thread.
`resident/src/discord.test.ts` checks thread creation, recovery, authorization, and outbox delivery across thread failures.
`dashboard/smoke/discord-conversation.smoke.test.ts` checks one conversation across Discord and dashboard after an idle period and connector restart.
It also checks a separate channel conversation, interrupted starts, and delivery when another thread fails.

The Packet divider smoke waits for the narrow conversation before it checks visibility.
Its cleanup restores the desktop viewport before it resets the divider.
These waits preserve the existing layout assertions and prevent a transition from being mistaken for a completed state.

## Sign-in connection checks

`mc/deployment/runtimeauth_setup_login_test.go` checks isolated login directories, the context deadline, and concealed command output.
`TestSetupRuntimeAuthLoginLeavesCodexTerminalInputUnread` gives the login an input reader and a child command that attempts to read it.
The test requires the reader to remain untouched and the authorization address to reach setup.
This test uses a local command fixture; it makes no provider request.

`mc/verbs/providerlifetime_test.go` checks access before the first setup heartbeat, the fixed deadline, and revocation after an operation ends.
It also checks the active operation, Project status, operation kind, and launch identity limits.
`resident/src/setup-proxy-start.test.ts` uses a local proxy fixture to check access during container creation and after the first heartbeat.
It checks that a finished operation loses access.
`mc/verbs/workspacesetup_test.go` checks that a file path receives folder guidance and a missing parent receives the directory error.

`mc/config/agentprofiles_test.go` rejects unsupported connection kinds, changed provider destinations, credential fields, and unsafe identifiers.
It also rejects an empty `base_url` field in the current configuration format.
`mc/deployment/runtimeauth_verifier_test.go` checks refusal of an unsupported authority before any provider request.
The assertion also checks that the error excludes the supplied refresh value.

The resident tests cover proxy authorization, certificate trust, renewal, revocation, and diagnostic attribution.
`provider-proxy.test.ts` distinguishes local refusals from provider failures and checks incremental response delivery.
It checks that upstream HTTP 400, 401, and 422 responses permit another request within a one-request limit.
The same tests require HTTP 404, 429, and 500 responses to retain their request charge.
`provider-credentials.test.ts` checks shared renewal, cancellation, durable rotation, and bounded retries.
`resident/src/connection-recovery.test.ts` runs resident ticks against the actual Go connection controls with synthetic credentials and provider responses.
It checks retry timing, service recreation, expired access, launch admission, overlapping ticks, stopped authorization, and obsolete results.
`mc/verbs/provider_renewal_test.go` checks repeated transport failures, one failure charge per result, durable evidence, and obsolete results.
`resident/src/main.test.ts` checks account condition selection, dispatch while renewal awaits a response, and check cancellation after failure.
These tests make no real provider request.
The [recovery record](../../tests/provider-proxy/RECOVERY.md) identifies separate renewal limits outside this coverage.
The adapter tests preserve native context and remove Culler tokens from retained sessions.
The profile tests cover harness effort validation, model catalog effort levels, and unavailable model effort errors.
The runner adapter tests check the accepted effort arguments for each harness.
The dashboard model configuration smoke checks effort selection and profile display.
It also checks Edit actions from the role assignment dialog.
The lifecycle command tests cover status, service changes, failure recovery, and terminal output.
The command installation tests cover path checks, executable conflicts, and shell access.

The `docker_e2e` credential tests use a controlled provider and synthetic host credentials.
They exercise the resident and actual container launches without a live account.
The refusal test rejects an invalid Culler token before a provider request.
The `docker_boundary` tests run the pinned harnesses through both connection methods.
Deliberate leak controls prove that the credential detectors reject contaminated artifacts.
`tests/provider-proxy/README.md` describes the controlled suite.
`tests/live-model/README.md` describes the separate real model suite.

These are source-defined assertions, not fresh execution results from this wiki update.
See [Agent connections and access renewal](../operations/agent-connections.md) for the behavior and related test paths.

### Terminal provider search and setup connection checks

`mc/onboarding/terminal_search_test.go` checks the terminal search through synthetic key input.
It covers these behaviors:

- Each query edit refreshes the matches, including deletion, clear, and cursor edits.
- Arrow keys select matches, and Enter refuses an empty result.
- The list shows four choices per page and details for the selected choice.
- Back, exit, closed input, and terminals without screen controls preserve their expected actions.

`mc/cmd/mc/setup_opencode_screen_prod_test.go` checks the catalog search and terminal output.
The search assertions compare name, identifier, and address matches and exclude matches caused only by the common `opencode:` prefix.
The screen tests use 80 and 40 columns, with and without color.
A separate screen-fit test traverses every provider within a 24-row terminal.
`dashboard/src/ui/agentprofiles.test.ts` checks the corresponding provider order with a controlled catalog.

`mc/cmd/mc/setup_untested_connections_prod_test.go` checks setup connection retention and cleanup:

- A failed connection remains available for retry across a setup reload.
- Another key replaces an unused connection whose test failed.
- A role assignment protects its connection from cleanup.
- A failed credential removal remains available for another attempt.
- A successful test protects its connection even when setup cannot record the result.

`mc/deployment/provider_disconnect_test.go` checks credential deletion and repeat removal without another revocation.
`mc/cmd/mc/systemstatus_setup_test.go` uses a scratch spine to check connection and profile verification after a successful setup test.
`mc/cmd/mc/setup_untested_connections_screen_prod_test.go` checks failure and retry screens at both widths, with and without color.

`dashboard/src/ui/systemhealth.test.ts` checks that unused connection conditions stay outside issues and inside the unused group.
It also checks that a provider service failure remains an issue.
The model configuration smoke checks that an unused failed connection stays outside the notice while an assigned failure retains its repair action.
The System health smoke assigns roles to its failed connections before it checks their issue controls.
These statements describe test source; this wiki update does not execute these tests.

### Provider choices and automatic names

`mc/cmd/mc/setup_agent_choices_prod_test.go` checks the provider menu and its access-method choices.
`setup_connection_choices_prod_test.go` checks automatic additional account names, name reuse, name collisions, and Back transitions.
The terminal screen tests cover the provider and access choices at 80 and 40 columns, with and without color.

`dashboard/src/ui/agentprofiles.test.ts` checks provider order and searches restricted to OpenCode providers.
It checks eligible disconnected connection reuse and numeric suffixes for occupied names.
`dashboard/smoke/api-key-connection.smoke.test.ts` checks provider order, sign-in before API keys, and automatically assigned connection names.
It checks a concealed key field and the selected provider summary after a query hides that provider.
Its retry checks retain a saved credential after a failed model test.
These descriptions identify source assertions; this wiki update does not execute those checks.

## OpenCode and update recovery checks

`runner/agent-runner/opencode.test.ts` checks model variants, local plugins, role skills, OpenWiki readiness, native session identity, continuation, completion, and diagnostic token removal.
Its background-command fixtures check that a finished job continues the turn and that an expired wait retains the completed reply.
`runner/agent-runner/adapters/startup.test.ts` checks the corresponding Claude SDK continuation and wait limit.
The fixtures shorten the wait; the production limit is ten minutes.
`mc/deployment/opencode_test.go` checks baseline model lists, download retry, unsafe archives, interrupted extraction, native version checks, and model-test evidence.
These checks use controlled fixtures and do not establish live provider availability.

### Source OpenCode package checks

The source package checks separate controlled deployment tests, filesystem verification, and real Docker preparation.

| Source | Behavior under test |
| --- | --- |
| `mc/deployment/opencode_source_test.go` | Preparation without a connection; reuse across commits, image identities, checkout paths, and timestamps; input changes; repair; ownership; active agents; and concurrent requests. |
| `mc/deployment/opencode_source_program_test.go` | Actual archive and directory digests, file contents, modes, symbolic links, required markers, and native version response checks through a fixture executable. |
| `mc/cmd/mc/setup_opencode_source_prod_test.go` | Source failure stages and causes survive terminal formatting. Preview fixtures cover progress, failure, details, and retry. |
| `mc/deployment/opencode_source_docker_test.go` | Real package preparation from committed declarations, native version verification, offline reuse, and detection of a changed plugin. |

The deployment fixture checks each package input separately, including declarations, the lock file, control files, archive rules, and tool identity.
It checks absent, damaged, legacy, and partial volumes, plus an obsolete failure status after successful verification.
It rejects uncommitted package inputs before preparation.
Concurrent requests must produce one package build and installation.

Published transition cases preserve deferred download when the volume is absent.
They require the retained published archive even when the source package has the same Culler commit.
They exclude source preparation during published recovery and clear obsolete source readiness after volume removal.
They also check a volume that becomes available between the first inspection and the second inspection under the worker lock.

The ordinary Go suite runs the controlled deployment, filesystem, and terminal formatter tests.
The filesystem test requires Python 3 and uses a shell fixture for the native program.
It does not replace the real package test.

The Docker test has the `docker_boundary` tag and resides in `mc/deployment/`.
It requires Docker, a compatible Linux arm64 source image, and network access for the first package preparation.
`CULLER_OPENCODE_TEST_IMAGE` selects the image; its default is `mc-prod`.
The test excludes host npm, uses a temporary committed repository and deployment home, and removes its scratch volume.
After the first installation, it rejects another archive build or a container run with network access during reuse.
It then moves the checkout and checks that the first connection requests no new preparation.
These checks require no provider credentials or provider requests.

Run the focused Docker test from `mc/`:

```sh
./test.sh -tags=docker_boundary -count=1 -timeout 20m ./deployment/ -run '^TestOpenCodeSourceDockerPreparationAndOfflineReuse$'
```

The separate `./boundarydocker/` package selection does not include this deployment test.
These descriptions state source assertions. This wiki update did not execute these application suites or the Docker test.
See [source package preparation and reuse](../operations/image-delivery.md#source-package-preparation-and-reuse) for the implementation and specification.

### Published update recovery checks

`mc/cmd/mc/setup_update_transaction_prod_test.go` checks retained candidate recovery, configuration changes, maintenance, durable commit, refused post-commit restore, and cleanup retry.

The candidate and restore tests separate command construction from actual container behavior:

| Source | Behavior under test |
| --- | --- |
| `mc/cmd/mc/setup_update_candidate_prod_test.go` | The candidate shell program parses with and without the optional OpenCode command. Controlled Docker responses check selected and base images, archived Projects, one check per distinct image, isolation, cleanup, and runtime failure. |
| `mc/cmd/mc/setup_update_stop_prod_test.go` | Restore starts the previous image's helper before services. A helper failure prevents service restoration and identifies the helper in the error. |
| `runner/image/project-smoke.sh` | The real default Project image runs the updater's shared shell program with the candidate runtime and the update check's container restrictions. |

`TestCandidateRuntimeScriptIsValidShell` uses `/bin/sh -n`; it checks shell syntax and optional command selection without executing the runtime.
`TestCandidateChecksArchivedImagesWithIsolatedRuntime` uses a Docker substitute and verifies that the runtime check receives no live spine mount.
`TestPublishedRestoreStartsThePreviousHelperBeforeServices` uses helper and service substitutes to check order and failure propagation.
These unit assertions belong to the ordinary Go suite.

The `release-images` job runs `project-smoke.sh` against the actual default Project and product images.
The smoke reads `mc/cmd/mc/candidate-runtime-check.sh`, which the Go updater embeds.
It checks the release identity and runtime access rules, including the protected spine directory in the default Project image.
The smoke requires Docker and compatible images; it remains separate from the Docker-free fast suite.
See [candidate runtime compatibility](../operations/image-delivery.md#candidate-runtime-compatibility) for the implementation.

`mc/deployment/update_checkpoint_docker_test.go` checks real scratch volume recovery after an interrupted optional-runtime copy.
It compares the restored configuration, spine identity, and optional runtime bytes.
The [retained update verification report](../../release/UPDATE-VERIFICATION.md) records prior results and leaves complete packaged recovery through macOS launchd unverified.
The release acceptance procedure in `release/UPDATES.md` retains that separate requirement.
This wiki update reviews these source assertions and does not report a fresh Go, Docker, or macOS acceptance run.

## OpenWiki recovery checks

`runner/openwiki/compatibility.test.ts` uses the fixed upstream package without model calls.
It checks original document preservation, Claim evidence, navigation, and receipt access across user identities.
The recovery cases cover external page and sidecar edits, absent Git history, source inventory changes, deleted pages, and malformed metadata.
They also check edits after inspection, edits during evidence resolution, rejected completion, and resumed updates with valid prior page completions.
A 205-file fixture checks the 200-path response limit, exact counts, truncation flags, complete Git diff, and commit intent.
A separate case requires a plan to review each Claim issue and rejects attempted deferral.
`runner/openwiki/check.sh` runs through the image component check.
See [OpenWiki and viewers](../integrations/openwiki-and-viewers.md#page-verification-and-recovery) for the contract.

## Operator commands and native trace checks

`mc/check.sh` runs the external-agent release-skill test without the Go test cache because it reads release files outside the module.
The command catalog, operator help, bridge, guard, and request tests cover executable syntax, static discovery, frozen permissions, target constraints, stable retries, and release refusal before effects.
`mc/boundarydocker/operator_transport_test.go` exercises the protected production command across the host boundary.
`docs/collie-command-access-validation.md` records prior results and limits; it does not establish a complete fast-suite or browser pass.

Native trace tests in `resident/src/opencode-copy.test.ts` check committed WAL records, excluded incomplete transactions, masking, child sessions, unchanged original stores, and retention after failed refreshes.
The lane and trace-reader tests cover import state and bounded native queries.
`docs/native-opencode-traces.md` describes the optional synthetic viewer, container, and resume acceptance checks.

`mc/deployment/opencode_models_test.go` covers live model refresh, retained lists after failures, catalog restrictions, and refresh isolation from credentials and Project data.
Dashboard model tests cover download progress and preservation of focused controls.
`systemstatus_setup_test.go` checks saved-profile verification during a draft model test.
Setup sign-in screen tests cover direct authorization after one selection and retained confirmation for other model tests.

### Viewer child sessions and embedded fonts

`resident/src/trace-lane.test.ts` requires child sessions in each viewer session-list request and checks Codex display names through a child-session fixture.
`dashboard/src/agentsview-proxy.test.ts` checks embedded data fonts in the content security policy and refusal of embeddings endpoints.
`dashboard/acceptance/agentsview.test.ts` checks loaded fonts through the authenticated real viewer and trace restoration after cookie removal.
These are source assertions; this wiki update did not execute the application tests or viewer acceptance lane.

## Role instruction and default name checks

`mc/verbs/customization_test.go` checks that a save replaces the one instruction set for a Project role, blank text removes it, and an existing run retains its exact input.
The tests also check isolation between roles and Projects.
`dashboard/smoke/customization.smoke.test.ts` checks saved and removed instructions, draft retention between role selections, and preservation of a customization block after a save.

`mc/cmd/mc/setup_connection_choices_prod_test.go` checks default display names for connection and profile records.
`dashboard/src/ui/agentprofiles.test.ts` checks the same final words, collision numbers before those words, and reuse of an existing profile name.
These checks keep the visible record types distinct while several profiles can share one connection.
These statements describe test coverage; this wiki update does not execute the suites.

### External agent skill installation checks

`mc/cmd/mc/agent_skill_prod_test.go` checks automatic links for selected harnesses and the absence of an integration question.
`mc/deployment/agent_skill_test.go` checks changes in harness selection, the earlier single-link record, ownership across updates and uninstall, and preservation of unrelated skills or replacement files.
These are source-defined checks; this wiki update does not execute the setup or deployment suites.
