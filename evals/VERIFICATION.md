# Framework verification

This record describes framework checks and an exploratory pilot.
It establishes no measured directive improvement.

The integration worktree starts from local `main` revision `73ae7300d1e545f081c15a04951b6d5b4003c0dc`.
The earlier pilot uses product inputs from revision `02a33e61a012fdf4ee59291dc645b15abb81d218`.
The framework changes reside under `evals/`.
The integration preserves the product source and directives from this `main` revision.
The release manifests and deployment state remain unchanged.

## Completed checks

| Check | Result |
| --- | --- |
| Documented `culler-evals check` command | All 531 collected tests pass. Another 61 checks cover the final control and viewer changes. These checks execute zero model calls. |
| Native Genetic-Pareto (GEPA) integration | Acceptance, rejection, duplicate proposals, checkpoint recovery, and confirmation checks pass against GEPA 0.1.4. |
| Packet display | Real Chromium checks pass through the product wrapper. Hidden content receives no display credit. |
| Product contracts | Disposable Go module checks verify the product brief, skills, artifact validation, schema, and Packager terminal behavior. |
| Container runtime | The final token-free Docker check verifies frozen sources, exact prompt input, private commands, repository preservation, packet snapshots, and Judge file access. |
| Credential projection | Checks verify private credentials, grant refresh, the resident broker, and execution from the frozen product snapshot. |
| Runtime failures | Malformed model responses retain their raw traces and browser evidence. Exact model identities must match. |
| Product source identity | Token-free checks reject brief, skill, toolchain, credential helper, and bridge changes before initial calls or continuation. |
| Optimizer reflection | The native adapter and proposer request include semantic failure reasons and exact browser-visible parent text. |
| Case reconstruction | All 35 Memos manifests validate their pinned commits, source trees, dependency files, reports, and independent reviews. |
| Dataset assignments | Checks verify the default case counts. Each set covers all seven types. Case facts and evidence remain unchanged. |
| Current Packager input | Compiled bridge checks project all 35 fixed cases into the current brief and preserve each manifest hash. |
| Current Packager rules | Source and prompt checks verify the latest Reader, term authority, and glossary rules for the Judge and Optimizer. |
| Experiment viewer | Browser checks verify chronology, trial identity, scores, rejection records, parent navigation, live updates, and truthful execution labels. |
| Browser controls | Checks verify configuration, case selection, fixed inputs, duplicate start protection, live records, and truthful completion states. |
| Saved presets | The [preset record](verification/presets.json) binds parameter checks, browser captures, terminal captures, and source hashes. |
| Experiment layout | The [layout record](verification/viewer-layout.json) binds browser checks, independent visual review, and source hashes. |
| Browser server shutdown | Parallel trials stop before retries. Cleanup removes only containers with the exact experiment label. |
| Interruption recovery | Checks preserve completed samples and close stale operations before a continued final comparison. |
| Repository branch | Command and Bun lifecycle checks reject a branch change at the same commit, including detached starts. |
| Trial repository disposal | Completed scores preserve their evidence before verified unchanged copies leave the disk. Changed and incomplete copies remain. |
| Calibration integrity | Checks reject altered packets, references, rubric text, runtime settings, wrapper bytes, viewport, or human presentation. |
| Calibration isolation | Human and Judge assessments remain separate. Older assessments without complete context cannot qualify for reuse. |
| Terminal text | Independent reviews accept the earlier captures and all current command, source, branch, and listener diagnostics. |
| Local MLflow index | The demonstration exports four candidate records, two validation score histories, and portable artifacts to a local database. |
| Git diff | The whitespace check passes. The task changes include no production files. |

Each terminal review uses actual output at 80 and 40 columns, with and without color.
The calibration review includes 52 captures.
A separate review accepts eight assessment contract diagnostics.
The pilot and image reviews each include 12 captures.

The delivery reviews include these actual captures:

- The live command review includes 28 captures.
- The checkpoint review includes four captures.
- The listener review includes 12 captures.
- The product source reviews include 28 captures.
- The branch review includes eight captures.
- The case author review includes 24 captures.
- The capture helper review includes eight captures.
- The repository disposal review includes 12 captures.

The [final container record](verification/container-runtime.json) binds the actual Docker check to its source identities.
The [actual viewer review](verification/native-integration-viewer-review.json) binds six browser captures to the real model record.
The [repository disposal review](verification/trial-repository-disposal-review.json) binds 25 resource checks and the actual Docker probe.
The [earlier source review](verification/latest-main-alignment.json) retains its original directive, brief, and term rule identities.
The [current source review](verification/current-main-alignment.json) records the current directive, writing skill, and Judge contracts.
The [browser controls review](verification/browser-controls-review.json) binds the browser captures, terminal captures, and cancellation checks.

## Browser controls

The `serve` command opens configuration controls before an experiment exists.
Six actual Chromium checks cover configuration, validation, start, live records, completion, and failure.
The controlled child writes durable fixture records and invokes no model.
The full tree retains its chronological records and candidate inspection controls.

The backend checks enforce fixed case assignments and one active experiment per server.
The runtime checks stop each role before a new model call after an interruption.
Two concurrent container failures receive interruption results rather than model retries.
The actual Docker check removes a container with the experiment's exact label.
It preserves a container with a separate experiment label.
The check uses the pinned evaluation image and invokes no model.

The browser starts the ordinary experiment runner after the explicit Start experiment action.
The full 35-case experiment remains unstarted at delivery.

## Effort configuration

Both current protocol profiles set `high` effort for the Packager, Judge, and Optimizer.
The browser exposes effort beside each model and harness.
The fixed protocol hash and Judge assessment context include effort.
Checks reject a missing effort or a changed effort before model execution.
They also reject incompatible Judge assessments.

The bridge supplies explicit Codex arguments and Claude Code options.
The network-disabled image check verifies both harness configurations without a model call.
The final container check verifies the request, arguments, configuration record, and native effort observation through a test harness.
The actual check preserves the repository and supplied evidence.

The full framework check passes 531 tests in 148.76 seconds.
The final control and viewer check passes 61 tests in 38.55 seconds.
These checks include the final added control regression and viewer layout.
The [effort check record](verification/effort-configuration.json) binds these checks to the current source files and container result.
The [independent effort review](verification/effort-review.json) preserves 40 actual terminal captures and eight browser captures.
The captures retain their exact source identities.

Historical protocols and scores remain unchanged.
The viewer shows `Effort unrecorded` when an older protocol supplies no effort.
Configured effort and observed effort remain distinct.
An absent native observation remains explicit.
These checks establish no new model execution or directive improvement.

## Actual model trials

The exploratory pilot completed fifteen packets across five cases.
Each case received three fresh Packager trials.
The Packager used GPT-6.1 Sol through Codex 0.159.2.
The Judge used Claude Opus 5.5 through Claude Code 2.1.280.
The Judge inspected each packet through Chromium 145.0.7632.6 at 1280 × 900 pixels.

The pilot uses this immutable evaluation image:

```text
sha256:77883aaedd8f96bd9fa72650852583e223e8d0692a9128bc872bd05ee78ddd3b
```

The evaluation overlay replaces only the Codex packages in the selected production image.
The production image remains unchanged:

```text
sha256:8fd68cf6b75a9196e1853db613ecd8012e9df0203ad36fc6ad10652d85e22266
```

The older Codex version rejected the selected model during an actual trial.
The evaluation overlay supports the selected model without a model substitution.
The private command file also permits Packager commands when Codex removes token variables from shell commands.
The token-free container check verifies that behavior.

The private pilot record preserves actual packets, command records, traces, screenshots, assessments, and chronological events.
The [pilot result record](PILOT-RESULTS.md) describes the measured results and excluded attempts.
The Judge remains uncalibrated, and all pilot scores remain exploratory.
The pilot exposes a risk contract defect that demands evidence beyond the Packager's authorized scope.
The framework corrects this defect and preserves the original scores.
Ten fresh reference assessments use the corrected contract and explicit JSON response instructions.
All ten pass the complete context and artifact checks.
Two malformed responses require genuine retries; the record preserves both failures.
The [current calibration status](CALIBRATION-STATUS.md) identifies the score-blind human page.

## Actual native GEPA check

The development check completed one GEPA mutation against two seed cases.
The training set contained `memos-log-level`.
The validation set contained `memos-webhook-label-refactor`.
The check used no final cases.

The Packager and Optimizer used GPT-6.1 Sol through Codex 0.159.2.
The Judge used Claude Opus 5.5 through Claude Code 2.1.280.
Every role used the immutable evaluation image above and the frozen runner and evaluation bridge.
Each role recorded its actual model, harness version, credential channel, and source identity.
The check used source revision `8cd10800939b002509b7b7f06b3bfbb6ce2de47e`.

Three Packager trials and their packet assessments completed.
The Optimizer proposed one directive edit from actual parent feedback.
The scope Judge permitted that edit.
GEPA compared the parent and child on the same training case.
Both scores equaled `0.9166666666666667`.
GEPA rejected the child under its strict improvement rule and selected the original directive, `D0`.
The check executed no child validation or final comparison.

The first process omitted the external browser-cache path.
The continuation supplied that path and reassessed the exact preserved packet.
One malformed Judge response required a genuine second attempt on the same packet.
The record preserves both infrastructure failures and all completed model work.
The journal contains 98 events, and every required artifact passes its content check.

The [native integration record](verification/native-integration.json) binds the exact protocol, journal, model identities, packets, and result.
The Judge remains uncalibrated.
This check establishes real Optimizer execution and native GEPA control, with no directive improvement claim.
Production source and deployment state remain unchanged.

The later source guard, reflection, repository disposal, and current brief changes received token-free checks.
Those checks preserve the exact inputs through the native adapter and real runtime preparation path.
The recorded native model check predates those changes.
Its evidence retains its original source identities and establishes no live result for the later changes.

## Reproduce the checks

Run these commands from `evals/`:

```sh
uv sync --locked
uv run playwright install chromium
uv run culler-evals check
uv run culler-evals demo --output /private/tmp/culler-eval-demo
```

Use a new output directory for each demonstration.
The host must permit Chromium processes and disposable product contract fixtures.
The [README](README.md) defines optional Docker and local MLflow checks.
The [pilot protocol](PILOT.md) defines the actual model trial.
The [calibration guide](CALIBRATION.md) defines the human reference review.

## Evidence limits

The corpus contains 35 Memos changes with disjoint training, validation, and final assignments.
The default assigns 13 training cases, 13 validation cases, and nine final holdout cases.
Each set covers all seven case types.
The [assignment record](cases/verification/corpus-assignments.json) binds the current assignments to the original reconstruction evidence.
Six interface cases supply 29 actual frontend captures with synthetic service responses.
The cases contain reconstructed reports, rather than reports from actual Culler Worker and Verifier runs.
The application tests, deployment checks, and performance benchmarks remain unverified where the case states those limits.
The trials reproduce Packager commands without the production spine or dispatch lifecycle.

The final set measures new Memos changes. It supplies no evidence about an unseen repository.
The full experiment fixes its models, case assignments, trial counts, rubric weights, and completion rule.
The Judge remains uncalibrated. Reference packets and calibration remain outside this run's readiness.
The experiment preserves candidate directives without an automatic improvement claim or product adoption.

The final human comparison binds packet hashes and pair order.
The presentation files and wrapper source require additional hash checks before human results can support qualification.

Framework checks prove the declared mechanics with controlled fixtures.
The pilot proves actual model execution and packet assessment.
Only an independent final comparison can support a directive improvement claim.
