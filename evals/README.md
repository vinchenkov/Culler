# Directive evaluations

This developer framework evaluates the Packager's explanation of a reviewed change.
It lives outside the product release and the production spine.
The [specification](SPEC.md) defines the experiment and its boundaries.

The framework includes these components:

- Native Genetic-Pareto (GEPA) evolution with a pinned library.
- Isolated Packager, Judge, and Optimizer sessions through the product harness adapters.
- Pinned external repository cases and content-verified evidence.
- Durable chronological records, candidate ancestry, and local MLflow indices.
- Packet checks, Judge assessments, and independent final comparisons.

## Current status

The full corpus contains 35 independent [Memos cases](cases/README.md).
The [inventory](cases/corpus.json) assigns 13 training cases, 13 validation cases, and nine final holdout cases.
Each dataset covers all seven change types.
The interface cases supply fixed captures from the unchanged frontend with synthetic service responses.

The experiment [configuration](config/packager.json) fixes the models, datasets, trial counts, and completion rule.
The [experiment rubric](config/rubric.experiment.json) uses the approved dimensions and equal weights.
The Judge remains uncalibrated. The run produces experimental directives without an automatic claim of improvement.
Reference packets and Judge calibration remain optional for this exploratory run.
The framework checks case readiness before it invokes a model.
The [verification record](VERIFICATION.md) states the completed framework checks and their limits.

The [pilot protocol](PILOT.md) fixes five ready cases and three trials per case.
Its [configuration](config/packager.pilot.json) uses the requested models and equal provisional rubric weights.
The exploratory pilot completed fifteen packets across five cases.
The [pilot result record](PILOT-RESULTS.md) preserves the aggregate evidence.
The Judge remains uncalibrated during this exploratory pilot.
The [calibration workflow](CALIBRATION.md) remains available for later work.

No measured directive improvement claim follows from framework tests or the demonstration.
Directive adoption requires the final evidence and a separate source change.

## Setup

The host requires these tools:

- Python 3.11 or later and `uv`.
- The repository's `mise` toolchain.
- Docker for real model trials.
- A pinned Culler runner image with the selected harnesses.
- Private credentials for each selected harness configuration.

Run these commands from `evals/`:

```sh
uv sync --locked
uv run playwright install chromium
uv run culler-evals check
```

The checks execute no model calls.
The browser check uses real Chromium and the product packet wrapper.
The product contract bridge builds a disposable copy of the Go module through `mise exec --`.
It changes no product source.

A host can select a private browser cache with `PLAYWRIGHT_BROWSERS_PATH`.
Use the same path for browser installation and evaluation.
The lock file fixes Python dependencies, including GEPA and Playwright.

An optional token-free container check uses an existing Culler runner image:

```sh
uv run python bridge/container-smoke.py \
  --image "$CULLER_EVAL_IMAGE" \
  --output /private/tmp/culler-eval-container-check
```

Set `CULLER_EVAL_IMAGE` to its immutable image digest or local image identifier.
Use a new output directory for each check.
The check uses the actual product Codex adapter with a deterministic test executable.
It verifies mounts, private commands, packet snapshots, repository preservation, and Judge file access without a model call.
It builds no image and changes no deployment.

The optional evaluation image overlay replaces only the Codex packages in a selected immutable runner image.
Its [configuration](config/runtime-image.codex-0.159.2.json) fixes the package versions, artifact addresses, and Secure Hash Algorithm 256-bit (SHA-256) hashes.
The overlay uses a separate local image tag and preserves the production image.

```sh
uv run python bridge/build-runtime-image.py \
  --configuration config/runtime-image.codex-0.159.2.json \
  --cache /private/tmp/culler-eval-image-cache \
  --output /private/tmp/culler-eval-runtime-image.json
```

Select the resulting immutable image identifier in the experiment protocol.
The build record identifies the base image and the verified package inputs.
Model availability remains subject to the ordinary authenticated trial.

## Inspect the framework without credentials

Create a native GEPA demonstration:

```sh
uv run culler-evals demo --output /private/tmp/culler-eval-demo
```

Open the reported `viewer/index.html` file.
The demonstration uses a minimal test role and deterministic test doubles.
Its scores illustrate the protocol and establish no packet quality result.

Inspect the real configuration:

```sh
uv run culler-evals validate --protocol config/packager.json
```

Prepare a pinned repository case:

```sh
uv run culler-evals prepare cases/memos/memos-link-cover-retry.json \
  --cache ~/.local/share/culler-evals/cache \
  --output /private/tmp/culler-eval-prepared
```

Preparation downloads the external source into the cache.
The case fixes full Git commits, its source tree, license, and dependency content hashes.
The repository contains no Memos source archive.
Preparation verifies the source and evidence status without a fabricated score.

## Original directive

Export the source input:

```sh
uv run culler-evals original --output /private/tmp/culler-eval-original
```

The original merges the complete Packager directive and shared `writing` instruction body unchanged.
The merged text forms one editable GEPA component.
The trial omits the separate `writing` skill and its pointer.
The remaining skills retain their source content and product order.

`protected.json` identifies fixed text and semantic role requirements.
The Optimizer can edit explanation guidance, including the merged writing guidance.
A changed protected span rejects the proposal before a child trial.
The Judge separately checks semantic conflicts in a fresh session.

## Packet contract

The operator uses the Project and knows its core terms.
The packet uses established Project or domain terms.

The packet uses these sections in order, with omissions only when the source directive permits them:

1. Strategic rationale or Summary
2. Background
3. Intuition
4. Technical
5. Criterion evidence
6. Evidence media
7. Risk and rollback
8. Glossary

The first section is Strategic rationale for a task that the Strategist proposed and Summary for a task that the operator defined.
The Strategic rationale part of the run brief identifies a task that the Strategist proposed.

Criterion evidence describes the latest Verifier gates and their results in prose.
The evaluator retains evidence identities and paths in separate records.
Code and Git details appear only when the explanation requires them.

The prose uses core and established Project terms without definitions.
The code and documents before the reviewed change can establish term use.
The wiki and interface text can also establish term use.
The prose can define a central new term.
The Glossary defines other new terms and can refresh established terms.
It excludes core terms and explicit origin labels.
Each glossary entry has an identifier, and each use of its term in prose links to that entry.

## Protocol preparation

Use the repository configuration directly. The run preserves its resolved inputs in the experiment directory.
An external configuration must retain valid relative paths to its rubric and cases.
The `run` command resolves `source_revision: "checkout"` to the exact checkout commit before the first model call.
Keep all compared models, harness settings, cases, references, and rubric values fixed.

The selected role assignments reside in configuration:

| Role | Selected model | Harness | Effort |
| --- | --- | --- | --- |
| Packager | GPT-6.1 Sol | Codex | high |
| Judge | Claude Opus 5.5 | Claude Code | high |
| Optimizer | GPT-6.1 Sol | Codex | high |

The `model` field requires the exact identifier that the harness accepts.
The `image` field requires an immutable image digest or local image identifier.
Accepted forms include `registry/image@sha256:<64 hexadecimal characters>` and `sha256:<64 hexadecimal characters>`.
The local form supports a Culler image without a registry digest.
The framework permits no implicit model substitution.

Each role declares its `effort` in the public runtime configuration.
The browser exposes that value beside the model and harness.
The configuration hash includes effort, and the Judge assessment context includes its effort.
An effort change creates different conditions for an experiment and its assessments.

The runtime records requested effort and configured effort separately.
It also records native effort observations when the harness supplies them.
The runtime rejects a different configured or observed effort.
An absent observation remains explicit; it supplies no proof of observed effort.
The viewer labels older records without effort as `Effort unrecorded`.

The evaluation bridge sets Codex reasoning effort and its default effort for subagents.
It sets Claude Code's effort option and effort environment override.
It selects the `adaptive` value for the `thinking` option.
These controls follow the [Codex configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)
and the [Claude Code model configuration](https://code.claude.com/docs/en/model-config).

Codex and Claude Code roles expose `runtime.<role>.fast_mode` in the configuration and browser.
Production, Production — 5 iterations, and Test enable it for the Packager, Judge, and Optimizer.
The Codex bridge sets `service_tier="fast"` and `features.fast_mode=true` when the setting is true.
A false value selects the default service tier and disables the feature.
The Claude Code bridge sets the session's `fastMode` value explicitly to true or false.
It preserves the other session settings and high effort.
When enabled, it permits Claude Code's account availability check.
It uses neither an account-check bypass nor a change to the host's settings.
The [Claude Code fast mode documentation](https://code.claude.com/docs/en/fast-mode) defines model and account requirements.
Noninteractive fast mode requires Claude Code 2.1.205 or later.
Claude Code can fall back to standard speed because of account restrictions or service limits.
The framework preserves reported fast mode states and reasons in the execution metadata.
A requested or configured value does not prove that every request used fast service.

Fast mode changes service speed and usage cost, while it preserves the model and effort.
The [official speed documentation](https://learn.chatgpt.com/docs/agent-configuration/speed) describes availability and usage rates.
The setting requires account and model support.
The framework records the requested setting and configured setting with each execution.
For Codex, it also records the configured service tier.
These records identify configuration, not proof that the server supplied faster service.
Fast mode needs no separate rubric, case set, or evaluation version.
The exact protocol snapshot still retains the setting for execution records.

Claude Code packet assessments and edit scope assessments use an explicit JSON Schema.
The bridge consumes the SDK's `structured_output` field and preserves the native result.
The packet schema requires all six dimensions and each semantic check exactly once.
It uses each dimension's score range from the fixed rubric.
The evaluator still checks inspection, evidence, and score validity before a result counts.
The runtime records the output format and schema hash.
An absent validated result follows the existing retry policy and receives no score.
The [structured output documentation](https://code.claude.com/docs/en/agent-sdk/structured-outputs) describes SDK validation and retries.

Visual explanation uses scores from 0 to 5. Every other dimension uses scores from 0 to 4.
A visual score of 5 rewards original visuals that convey the essential model accurately with little prose.
The framework divides each dimension score by its rubric maximum before it computes the weighted quality score out of 100.
All six dimensions retain equal weight in the saved presets.

The Judge assesses comprehension through both text and visuals.
Case evidence checks accuracy; it supplies no credit for explanations that the packet omits.

The protocol fixes these experiment inputs:

- Disjoint training, validation, and final case groups.
- Trial counts for each phase.
- The rubric, dimension weights, and operator context.
- Protected directive spans and semantic requirements.
- Proposal count, minibatch size, concurrency, seed, and perfect-score skip policy.
- An iteration or stagnation completion rule.
- Optional confirmation and its counter policy.
- Retry rules and final decision thresholds.
- An explicit uncalibrated status, or accepted calibration records for a calibrated experiment.

The training set supplies feedback and matched parent-child comparisons.
The validation set supplies native parent selection and the returned candidate.
The final holdout set remains outside Optimizer files and tools until search ends.
Related corrections and evidence variants belong to the same case group.

## Configure and start in the browser

Open the experiment controls from `evals/`:

```sh
uv run culler-evals serve \
  --protocol config/packager.json \
  --credentials "$HOME/.local/share/culler-evals/credentials/credentials.json"
```

The default record directory is `~/.local/share/culler-evals/experiments/`.
The default repository cache is `~/.local/share/culler-evals/cache/`.
The browser displays the record directory and local backup directory.
Custom record directories must reside outside temporary storage.
The framework rejects temporary paths and symbolic links into temporary storage before model calls.
The framework also keeps required records under `~/.local/share/culler-evals/backups/`.
Each record write must preserve both copies before dependent work continues.
Both copies use the same disk. An external disk backup remains necessary for disk failure protection.

The records include the exact source archive and a Git bundle for the source revision.
Resume can restore an absent task worktree from these artifacts.
Recovery preserves existing checkouts and verifies the frozen source manifests.
The archive excludes untracked credentials and disposable repository caches.
Case inputs outside the source checkout must use persistent paths.
Keep credentials and credential profiles outside temporary storage.

Restore an absent experiment directory from its local backup:

```sh
uv run culler-evals recover "$HOME/.local/share/culler-evals/backups/EXPERIMENT_BACKUP"
```

Recovery verifies the journal and artifacts before it restores the original record directory.
Recovery preserves an existing record directory without replacement.
Recovery invokes no models. Reopen the browser server and select **Resume experiment** to continue.
Scratch repositories can be absent after recovery. The framework restores required evidence from the fixed case inputs.

The `serve` command opens a local browser page. It invokes no model until you select **Start experiment**.
The host retains the credential configuration. The browser receives public parameters only.

The browser provides three saved presets:

| Parameter | Production | Production — 5 iterations | Test |
| --- | --- | --- | --- |
| Training / validation / final cases | 13 / 13 / 9 | 13 / 13 / 9 | 7 / 7 / 9 |
| Trials per training / validation / final case | 2 / 2 / 3 | 2 / 2 / 3 | 1 / 1 / 1 |
| Maximum iterations | 12 | 5 | 2 |
| Iterations without a gain | 4 | 4 | 2 |
| Proposals per iteration | 2 | 2 | 1 |
| Training cases per minibatch | 3 | 3 | 2 |
| Maximum Packager packets before retries | 992 | 460 | 47 |

[Production](config/packager.json) preserves the full experiment parameters.
[Production — 5 iterations](config/packager.production-short.json) changes only the maximum iteration count.
[Test](config/packager.test.json) checks execution with less work.
The [preset catalog](config/packager.presets.json) supplies the browser labels and protocol files.
All presets use the same models, `high` effort, four concurrent operations, and final decision rules.
All presets keep all nine final cases.
The Test preset covers all seven change types in each dataset.
Its scores remain exploratory; directive improvement requires a full evaluation.
Judge calls, Optimizer calls, scope checks, and retries add work beyond the displayed packet count.
GEPA can return the original directive and skip the final comparison.

The five-iteration preset permits ten proposals and at most 940 agent calls before retries.
These calls comprise 460 Packager calls, 470 Judge calls, and ten Optimizer calls.
One agent call starts one harness session, which can make several provider requests.
The preset preserves each candidate's assessment procedure and the independent final comparison.
Fewer iterations reduce opportunities for improvement.
Two trials per case reduce variation but do not guarantee a reliable score.
The same completion rules can stop either Production preset before its maximum.

The **Preset** selector applies every parameter and the selected cases together.
Manual edits show **Custom** until the parameters match a saved preset.
**Restore preset sets** restores the cases from the last selected preset.
Each experiment records its exact parameters, preset identity, and parameter hash.
The preset selector locks when an experiment starts.

Add `--preset production-short` or `--preset test` to open that preset initially.
Preset selection invokes no model.
The production preset remains the default for the existing command.
The `run` command also accepts `config/packager.test.json` as its protocol.

The page provides these controls:

- Case selection within the assigned training, validation, and final holdout sets.
- Trial counts for each phase.
- Proposal count, minibatch size, concurrency, and random seed.
- Iteration and stagnation completion rules.
- Exact model identifiers and harness settings.
- Confirmation comparisons, retries, and final decision thresholds.

The page validates the configuration before it starts an experiment.
Each case stays in its assigned set to preserve the final holdout.
Each experiment receives its own output directory and fixed configuration.
The page shows the live chronological tree, scores, packets, and directive edits after the experiment starts.
The experiment view fills the browser below compact navigation.
Its current phase stays separate from the event you inspect.
The chronology and candidate ancestry appear before detailed evidence.
Expandable sections retain the setup, diagnostic details, and complete score tables.
**Latest** resumes automatic updates after history inspection.
Configuration changes apply to a new experiment.

Closing the browser leaves the experiment active.
Pressing Ctrl+C in the server terminal interrupts an active experiment and preserves its records.
**Resume experiment** continues a failed or interrupted experiment in the same browser viewer.
The server restores saved experiments after a restart.
The Saved experiments selector opens earlier experiments without model calls.
The `run --resume` command also supports continuation with the recorded configuration.
Portable exports and the `view` command remain read-only.

The server saves each launch under `OUTPUT_ROOT/.control/EXPERIMENT_DIRECTORY/`.
That private directory contains `protocol.json`, `launch.json`, and `session.log`.
The browser shows the experiment directory name beside the live status.
The browser displays the resume settings before the action.
Ordinary resume reuses completed trials and the original runtime.
A runtime update is available only before initial validation completes.
This update enables fast mode and requires validated JavaScript Object Notation (JSON) from the Judge.
The update retains the original product source, runner, directive, cases, rubric, models, and effort levels.
The Judge reassesses retained packets. Earlier scores remain in the chronology.
The update preserves both protocols and the original records.
The viewer uses the new scores after the update event.
The same experiment identifier and output directory remain in use.
A resumed experiment still requires service availability for fast mode.
The runtime records each harness report about fast mode.

The server creates a separate resume checkout under `/private/tmp/culler-worktrees/`.
Its product and runner files come from the original revision.
Ordinary resume uses the preserved evaluation files. An explicit runtime update uses the server evaluation files.
The source manifests identify their exact bytes.
Other product changes on main remain outside the resumed experiment.

Before initial validation completes, browser resume can apply the repository disposal repair.
The repair preserves completed Judge scores and the original cleanup records.
The repair requires the same packet, submission, source attestation, and repository identities.
The repair preserves the runtime, assessment rules, directive, cases, and trial counts.
The chronology records both source protocols before execution.

Use the private `protocol.json` with `run --resume` for command-line continuation.

The frozen protocol identifies the actual framework, packet wrapper, runner, bridge, and product source bytes.
The product manifest includes the `mc` module, resident credential helpers, shared runner files, and toolchain configuration.
It also fixes the contract bridge and credential bridge files.
The runtime copies these files into verified private snapshots before use.
The contract bridge uses the recorded Go version, module checksums, and compile environment.
Every model session records the product and runtime source hashes.
An initial call or continuation rejects source changes, including brief or skill changes.
Restore the recorded source before continuation.

The default uses two trials per training or validation case and three trials per final case.
Each iteration proposes two edits from minibatches of three training cases.
The search ends after 12 iterations or four iterations without the declared validation gain.
The declared gain is two quality points out of 100.
The default disables merge proposals and confirmation comparisons.
These values remain fixed for the run. They are initial experiment choices, not proven optimal values.

Before retries, the largest default search produces 938 packets when every proposed child reaches full validation.
The final comparison adds 54 packets when GEPA returns a changed directive.
Judge calls, scope checks, Optimizer calls, and external retries add separate work.
GEPA can return the original directive when no candidate improves its stored validation mean.

The [case guide](cases/README.md) defines manifests and review evidence.
The full corpus requires multiple independent groups for each explanation type.
Repeated trials measure model variation and do not create independent cases.

## Judge calibration

Human records assess reference packets through the same six dimensions.
They include accurate packets, weak explanations, attractive factual errors, and an instruction-injection case.
The independent check set contains different packet identifiers and case groups.
Both sets exclude final case groups.

The protocol identifies each calibration file with its path and Secure Hash Algorithm 256-bit (SHA-256) hash.
The [calibration workflow](CALIBRATION.md) generates the records from complete human and Judge assessments.
Each exported record binds the exact packet, case references, rubric, assessment instructions, and actual runtime.
Use these generated records rather than construct partial records manually.

Each Judge dimension requires a score, reason, and evidence references.
Each semantic check requires an identifier, result, reason, and evidence.
The Judge must assess these semantic checks exactly once:

- `evidence_authority`
- `summary_matches_thesis`
- `section_applicability`
- `glossary`
- `criterion_evidence`
- `risk_and_rollback`
- `technical_detail`
- `reader_context`

Generate a calibration summary with explicit thresholds:

```sh
uv run culler-evals calibrate /private/tmp/calibration-records.json \
  --minimum-agreement 0.9 --minimum-critical-detection 1 \
  --output /private/tmp/calibration-report.json
```

These command values illustrate syntax and define no experiment threshold.
Agreement uses one minus the absolute score difference divided by the dimension maximum, averaged across dimensions.
Critical-error detection measures the fraction of known critical errors that the Judge identifies.
Calibration validation verifies the actual hashed records and recomputes both measures.
An uncalibrated Judge can supply exploratory pilots, directive evolution, and final comparisons.
Those results preserve the uncalibrated status and establish no automatic improvement claim.

## Real trials

Real trials use Docker with synthetic run identity and private command state.
Each trial receives the reviewed Git range at `/workspace/source`.
Each trial receives fixed skills and upstream evidence at the product paths.
The command adapter records every request and response.
It permits refusal repair and exactly one successful terminal result.

The Judge receives a private empty workspace and authoritative case references.
A narrow browser interface supplies screenshots, visible text, and ordinary packet interactions.
The Judge receives neither the packet source file nor hidden packet content.
The browser uses Culler's packet wrapper and frame restrictions.
The initial viewport uses a width of 1280 pixels.

The Optimizer receives the parent directive, fixed rubric, and training minibatch evidence.
Its private workspace contains no final cases or experiment store.
Reflection includes each trial's dimension reasons, semantic checks, critical failures, and contract failures.
Reflection also includes the exact visible text from preserved browser states, with trial identifiers and display conditions.
An absent browser state receives an explicit absence record.
Reflection includes no raw packet source, image data, private transcript, or final case evidence.
GEPA controls protocol decisions.

A completed packet assessment preserves its score, packet, traces, screenshots, and case manifest before repository disposal.
Disposal removes only the two verified repository copies owned by that trial.
A fresh token-free check uses the recorded evaluation image with read-only mounts and no network or credentials.
The check compares the commit, branch, worktree, Git metadata, and fixed source fingerprints.
It also detects unexpected ignored files.
The journal binds each disposal start to its exact verification evidence.

Incomplete trials retain their repository copies for Judge continuation.
Changed or unverifiable copies remain available for analysis.
Continuation can finish a partial disposal and reuse the completed score without a new model call.
The source cache, source checkout, worktrees, and fixed evidence files remain intact.
The preserved case manifest contains the pinned source and reconstruction inputs.

## Private credentials

Supply credentials through a separate private JavaScript Object Notation (JSON) file.
The file maps each role to a `credentials` object.
It can supply credentials only; model, image, and harness settings reside in the protocol.

```json
{
  "packager": {
    "credentials": {
      "profile": "/Users/YOUR_USER/.local/share/culler-evals/credentials/profile.json",
      "binding": "codex-eval"
    }
  },
  "judge": {
    "credentials": {
      "profile": "/Users/YOUR_USER/.local/share/culler-evals/credentials/profile.json",
      "binding": "claude-eval"
    }
  },
  "optimizer": {
    "credentials": {
      "profile": "/Users/YOUR_USER/.local/share/culler-evals/credentials/profile.json",
      "binding": "codex-eval"
    }
  }
}
```

The connection profile names an enabled Culler agent profile and its saved connection.
The resident keeps the provider credentials in the host Keychain.
The evaluation bridge uses the resident's private socket.
It reads no provider credential files.

```json
{
  "purpose": "directive-evaluation",
  "resident_home": "/absolute/path/to/private/culler-home",
  "catalog": {
    "codex-eval": {
      "harness": "codex",
      "credential": "configured-connection",
      "channel": "codex",
      "kind": "codex-api-key",
      "inference_url": "https://api.openai.com/v1"
    }
  }
}
```

Each catalog entry must match the saved connection and enabled profile.
Use `claude` as the evaluation harness and channel for a `claude-sdk` profile.
Native sign-in entries also include the configured `token_url` and `client_id`.
The supported connection kinds are:

- `codex-api-key`
- `codex-oauth`
- `claude-api-key`
- `claude-oauth`

The profile requires permissions `0600` and the current user's ownership.
It resides outside the Culler checkout.
Private grant files and direct provider credential projections are unsupported.
Save those credentials as a Culler connection, then replace the evaluation profile.

One host bridge serves concurrent sessions for each profile and binding.
Each session receives a separate grant for its exact model.
Each grant permits at most 100 provider requests and lasts at most 1800 seconds.
The session timeout must stay within that limit.
The runtime reads the request count and revokes the grant after the session.
It closes all remaining grants when the pool closes.

Containers receive only a temporary Culler token, the proxy address, and the public certificate.
The provider access file uses a private temporary directory and a read-only container mount.
The runtime excludes that file from exported artifacts.
Native sessions and retained output conceal the Culler token.

Trusted integrations can supply `provider_access` through the private `credentials` object or an injected `credential_provider`.
That object must use the same Culler proxy contract.
The runtime rejects provider environment variables, credential files, and credential mounts.
The runtime retains the configured channel and endpoint without authentication values.

## Execute model trials

Execute a pilot on reviewed development cases:

```sh
uv run culler-evals evaluate --protocol /private/tmp/experiment/packager.json \
  --cases cases/memos/memos-link-cover-retry.json \
  --credentials ~/.local/share/culler-evals/credentials/credentials.json \
  --cache ~/.local/share/culler-evals/cache \
  --output ~/.local/share/culler-evals/experiments/pilot
```

Execute the fixed experiment:

```sh
uv run culler-evals run --protocol config/packager.json \
  --credentials ~/.local/share/culler-evals/credentials/credentials.json \
  --cache ~/.local/share/culler-evals/cache \
  --output ~/.local/share/culler-evals/experiments/example
```

The command opens a live browser view and executes GEPA followed by the final comparison.
The browser preserves chronological events, accepted ancestry, rejected ancestry, scores, packets, and directive edits.
Select an earlier event to inspect what the experiment knew at that time.
The live server stays open after completion. A portable viewer remains in the output directory.
The returned directive appears in `manifests/search_result.json` under `best_candidate.packager_directive`.
The viewer opens each candidate's complete directive, edit, scores, and Judge feedback.

Use `run --resume` with the same arguments after an interruption.
Continuation checks the protocol, case inputs, checkpoint, candidate texts, and native random state.
It preserves interrupted attempts and completed evidence.
A failed Judge call preserves the produced packet. Continuation assesses that exact packet again.
Completed Optimizer proposals also remain fixed across continuation.
Use `view ~/.local/share/culler-evals/experiments/example` to reopen the live browser view.
The lower-level `evolve`, `continue`, and `final` commands remain available for separate phases.

Execute separate contract trials with an explicit trial count:

```sh
uv run culler-evals contract --protocol /private/tmp/experiment/packager.json \
  --cases /private/tmp/experiment/contract-case.json \
  --directive /private/tmp/culler-eval-original/directive.md \
  --trials "$CULLER_EVAL_CONTRACT_TRIALS" \
  --credentials ~/.local/share/culler-evals/credentials/credentials.json \
  --cache ~/.local/share/culler-evals/cache \
  --output ~/.local/share/culler-evals/experiments/contract
```

The count remains an experiment choice.
Contract trials require only the fixed source revision, Packager runtime, external retry rule, and reviewed case inputs.
They compare actual terminal results with expected terminal results.
They preserve artifacts and apply packet checks when the expected result includes a packet.
They invoke no Judge and supply no packet quality score.
Infrastructure contract cases remain outside the training, validation, and final quality datasets.
All model commands accept `--source` for the selected checkout.

## Native GEPA sequence

The framework pins GEPA 0.1.4 and exposes one component, `packager_directive`.
It uses native per-instance Pareto selection, independent proposal tasks, strict improvement, and full validation.
It disables merge proposals and the evaluation cache.

Each iteration follows this sequence:

1. GEPA selects parents and training minibatches from stored validation scores.
2. Fresh parent trials supply scores and Judge feedback.
3. The Optimizer proposes a complete directive edit.
4. The scope check permits or rejects the edit.
5. Fresh child trials use the parent's exact minibatch.
6. GEPA compares the stored parent scores with the child scores.
7. Accepted children receive full validation.
8. GEPA updates its candidate pool and parent probabilities.

GEPA 0.1.4 shares a parent evaluation when several tasks select the same parent and identical minibatch.
The journal records that reuse and its originating trial evidence.
Independent evaluation batches can run concurrently.
The pinned native proposer invokes Optimizer sessions in sequence.
The record preserves those actual intervals.

The native candidate pool retains historical candidates independently of their parent eligibility.
An undominated candidate can receive zero parent probability when it leads no validation case.
GEPA returns the candidate with the highest stored validation mean.

Optional confirmation executes fresh paired trials on validation cases.
It retains separate scores and never overwrites native validation scores.
Its configured counter policy can reset stagnation for a newly confirmed candidate.
The same candidate cannot reset stagnation indefinitely.

## Final comparison

The final comparison fixes the returned candidate and evaluates it against the unchanged original.
The evaluator alternates directive order across fresh paired trials.
External failures retain every attempt and require a replacement pair.
Role failures remain in the sample with zero contribution.

The analysis preserves repeated trials within cases and related cases within independent groups.
A paired cluster bootstrap supplies the 95 percent uncertainty interval.
The report states case counts, trial counts, critical failures, contract failures, and case-type regressions.
An incomplete or imprecise comparison remains inconclusive.
The final decision also requires a human comparison without directive labels.
The first automated report can state `awaiting_human_review`.

Prepare that comparison after the final trials:

```sh
uv run culler-evals prepare-review ~/.local/share/culler-evals/experiments/example \
  --output /private/tmp/culler-eval-human-review
```

Open the reported `index.html` and assess each pair without the experiment viewer.
The page identifies the packets as A and B in a random order.
It preserves the product packet wrapper and binds each packet to its final trial and content hash.
Complete the generated `review.json` with these fields:

- `reviewer` identifies the human reviewer.
- `blinded` confirms that the reviewer saw no directive labels.
- `reason` explains the overall comparison.
- Each comparison contains its identifier, preference, and reason.

Each preference equals `A`, `B`, `tie`, or `insufficient_evidence`.
The human must assess every presented pair exactly once.

```sh
uv run culler-evals record-review ~/.local/share/culler-evals/experiments/example \
  --record /private/tmp/culler-eval-human-review/review.json
```

The strict pair majority rule confirms improvement when more than half the pairs favor the candidate.
Ties and insufficient evidence count against confirmation.
A strict majority for the original records a worse candidate.
Otherwise, the review records a tie or insufficient evidence.
The human result complements the automated final requirements and cannot replace them.
The command records the review, updates the final decision, and retains the exact packet bindings.

GEPA can return the original directive.
That result records no evolved candidate and a declared final-comparison skip.

## Records and views

The experiment directory contains these records:

- `events.jsonl` supplies the chronological hash chain.
- `blobs/` supplies immutable content-addressed artifacts.
- `manifests/` supplies the fixed protocol, results, and checkpoint references.
- `native/` supplies the GEPA checkpoint for continuation.
- `trials/` and `proposals/` retain private execution workspaces.
- `viewer/` supplies the portable report and copied artifacts.

Each operation registers before execution and retains a terminal outcome.
A failed required record write stops dependent work.
Artifact hashes and absent outcomes participate in the completeness check.
The journal preserves rejected proposals and their exact reasons.

Export a view without a service account:

```sh
uv run culler-evals export ~/.local/share/culler-evals/experiments/example \
  --output /private/tmp/culler-eval-report
```

The view includes chronology, candidate ancestry, validation scores, and direct artifact references.
The event slider excludes scores that did not exist at the selected event.
The accepted and rejected graph views retain references to every parent.

Install the optional MLflow dependency:

```sh
uv sync --locked --extra tracking
uv run culler-evals index ~/.local/share/culler-evals/experiments/example \
  --tracking-uri sqlite:////private/tmp/culler-eval-mlflow.db
uv run mlflow ui --backend-store-uri sqlite:////private/tmp/culler-eval-mlflow.db
```

MLflow creates a parent experiment run and a nested run for each candidate.
Each candidate run records its parents and validation score history.
The parent run records the final result and summary metrics.
The index also copies the portable journal, manifests, and blobs.
New experiments store these copies under `mlflow-artifacts/` in the experiment directory.
The portable records remain authoritative and independent of MLflow.
Public evidence requires an explicit review of licenses, private content, and claim scope before publication.
The user publishes Git changes.

## Extend another role

The common engine consumes normalized case scores and role artifacts.
It contains no Packager assessment dependency.
A role adapter defines its input, command contract, artifacts, references, and assessment.
The demonstration proves the engine boundary with a minimal test role.

Strategist and Arbiter require cases that assess useful work against a Project vision.
Worker and Verifier require a demonstrated Culler-specific evaluation need.
The framework keeps generic software benchmarks outside this Packager release.

## Evidence limits

The command adapter substitutes private fixed command state for the production spine and dispatch lifecycle.
Product bridge tests compare input assembly and relevant validation behavior.
These checks establish no complete production pipeline acceptance.

The supplied media comes from case preparation.
The trials assess its use and explanation.
Application deployment and media capture remain outside the trial scope.
Token-free checks establish no live Judge browser session through the selected model.
The reviewed pilot must provide that evidence.
Model outputs can vary even when the protocol remains fixed.
Every result claim must identify its exact models, harnesses, source, cases, and evidence limits.
