# Evaluation cases

A case fixes the Packager input and its source evidence.
A trial receives a fresh repository and private copies of the case artifacts.
The Judge receives reference facts outside the Packager artifact directory.

## Current corpus

The [corpus inventory](corpus.json) assigns 35 independent Memos changes to the experiment datasets.
Each change has one distinct case group.
The corpus preserves the source facts of the seven development seed cases.
Their metadata assigns six seed groups to the training set and the account usage group to the validation set.
The five pilot groups remain outside the final set.

| Case type | Training set | Validation set | Final set |
| --- | ---: | ---: | ---: |
| A small visible change | 2 | 2 | 1 |
| A feature across several components | 1 | 2 | 2 |
| A state transition or recovery change | 2 | 2 | 1 |
| A configuration or compatibility change | 2 | 2 | 1 |
| A data or access change | 2 | 2 | 1 |
| An internal refactor | 2 | 2 | 1 |
| A performance change | 2 | 1 | 2 |
| **Total** | **13** | **13** | **9** |

The training set, or trainset, supplies reflection minibatches.
The validation set, or valset, supplies the fixed candidate comparison cases.
The final set supplies the independent final comparison.
A case group belongs to exactly one dataset.
Nearby source snapshots can share context, but each group changes different underlying behavior or structure.
This single-project corpus does not establish generalization to unrelated projects.

The final set contains two feature cases and two performance cases for complex mechanism coverage.
Every set contains all seven case types.
The assignments use case type and complexity, rather than packet scores.

The inventory records each manifest path relative to this directory.
The 28 new manifests reside under [memos/corpus](memos/corpus/).
The inventory records the change complexity for each new group.
All 35 cases passed independent source review and reconstruction.
The six interface cases also passed independent media review.
The case loader checks those bindings before a live trial.

## Evidence status

The case author inspected the actual source before and after each change.
The case author executed Git reconstruction checks and recorded their results.
Those checks establish source reconstruction and the recorded source observations.
They do not establish application behavior through executed application tests.
The [reconstruction record](verification/corpus-reconstruction.json) retains the original inspected manifests, assignments, and source ranges.
The [assignment record](verification/corpus-assignments.json) binds the current assignments to that unchanged reconstruction evidence.
The assignment record verifies that only six dataset fields changed.
The source facts, reports, media, and independent reviews remain unchanged.

The Worker, Verifier, Strategist, and Scribe records identify their `authored_for_experiment` origin.
No production Culler role executed those records.
The reports explicitly separate source inspection from unverified application checks.
No case claims that upstream test files passed merely because those files exist.
The Judge receives the reference facts through a separate artifact directory.

The corpus includes these source conditions:

- Small changes that require a short explanation.
- Features with several component boundaries.
- Recovery rules with explicit failure and success conditions.
- Compatibility rules that differ for fresh and existing instances.
- Access boundaries and configuration exceptions.
- Refactors with equivalent behavior or disclosed error-path differences.
- Performance mechanisms without fabricated numerical benefits.

Several fixtures contain an authored earlier verdict and correction.
Each record identifies its source-inspection scope.
A correction can narrow an overbroad report without a new implementation commit.
The local storage case also contains an earlier requirements version.
The packet must distinguish historical evidence from the current requirements.

The scheduler fixture supplies actual derived Git commits.
Three source commits partition the pinned upstream scheduler change.
A fourth commit contains only the package README and documentation source.
Its final tree equals the pinned upstream tree.
The [history record](memos/corpus/memos-scheduler-plugin/authored-history.json) identifies every contribution and the documentation range.
The fixed Git bundle supplies that history; it represents no production Culler execution.

The six interface cases require supplied media:

| Case | Dataset | Captured states |
| --- | --- | --- |
| [Notification theme colors](memos/memos-dark-mode-toast.json) | Trainset | Light and dark notifications. |
| [Account attachment usage](memos/memos-account-attachment-usage.json) | Trainset | Total, zero, pending, and unavailable values. |
| [Pinned memo marker](memos/corpus/memos-pinned-marker.json) | Trainset | Pinned, unpinned, and pointer-hover states. |
| [Memo author spacing](memos/corpus/memos-author-spacing.json) | Valset | The short author label Al at a desktop width of 1280 pixels. |
| [Metadata hover and focus](memos/corpus/memos-metadata-hover.json) | Final set | The owner editable row and another user’s readonly row, each at rest and on pointer hover. |
| [Markdown disclosure spacing](memos/corpus/memos-disclosure-spacing.json) | Final set | Collapsed and expanded disclosures beside body text. |

Each manifest records its media and fixed source revisions.
The independent reviewer accepted all six cases and 29 Portable Network Graphics (PNG) files.
The captures show literal components with fixed synthetic data.
They establish no full backend deployment or application integration test.
The author case leaves narrow widths unverified.
The metadata case leaves keyboard focus and narrow widths unverified.
The other cases introduce no new rendered interface control or layout.
Their source behavior and explicit evidence limits define their visual scope.

The [seed review](memos/reviews/memos-seed.json) records source fact acceptance for the original seven cases.
The [corpus source review](memos/corpus/reviews/independent-source.json) accepts the 24 new cases without required interface media.
The [media review](memos/corpus/reviews/memos-media.json) accepts the six interface cases and their source facts.
Both reviews inspect incidental changes, error paths, provenance, and evidence limits.
A source review establishes neither application test execution nor human Judge calibration.

## Author tools

The author tools use pinned objects from an external bare Memos mirror.
They create absent files and preserve existing manifests, media, and review records.
They make no model call.
The scheduler tool creates the actual derived source history after the base corpus exists.

```sh
evals/.venv/bin/python evals/cases/author_corpus.py --cache /absolute/external/memos.git
evals/.venv/bin/python evals/cases/author_scheduler_fixture.py --cache /absolute/external/memos.git
```

For a new case, the first tool records source objects, license hashes, dependency hashes, and Git diff check results.
The second tool verifies that its final source tree equals the original upstream tree.
A contributor must preserve each accepted manifest and supply a new case for a different underlying change.

## Manifest fields

| Field | Content |
| --- | --- |
| `id` | The stable case identifier. |
| `group` | The stable identifier for one change and its related variants. |
| `types` | The established explanation requirements that the case covers. |
| `source` | The repository address, license record, full commits, source tree, and change origin. |
| `project_skills` | The fixed Project skills. An empty list records their absence. |
| `role_inputs` | The task, brief parts, command responses, and report provenance. |
| `references` | The Judge's behavior facts, evidence limits, and independent review record. |
| `artifacts` | Each artifact's source, destination, content hash, visibility, and provenance. |
| `visual_evidence` | The required media status and capture instructions. |
| `environment` | The trial requirements, capture environment, and dependency content hashes. |
| `paths` | Each role path and its prepared content. |
| `expected_terminal` | The expected packaged result, infrastructure report, or operator request. |
| `absent_paths` | The intentionally absent evidence paths for an infrastructure contract case. |
| `content_hashes` | The hashes of fixed input groups. |
| `dataset` | The experiment's assignment. Each manifest matches the inventory. |

The source license record uses a Software Package Data Exchange (SPDX) identifier.
The artifact records use Secure Hash Algorithm 256-bit (SHA-256) content hashes.
Git supplies the commit and tree identifiers.
Every dependency record fixes the content of its source dependency file.

The role input contains product brief parts with fixed explanations and facts.
The product bridge projects the current Packager parts from that fixed input.
It omits obsolete Verdict history, Scribe report, and Earlier requirements parts.
The fixed files and reference facts remain available as separate evidence.
The runtime records each omitted part and preserves the original manifest hash.
The runtime substitutes only the synthetic run identifier through `{{run_id}}`.
Most upstream cases use the same commit for the verified source and Scribe result.
The scheduler fixture fixes distinct implementation and documentation commits.
Its history record identifies actual source commits.
Authored verdict histories identify report corrections without an extra implementation commit.

## Source preparation

External source checkouts and caches reside outside the Culler checkout.
The evaluator obtains the repository from the manifest address at runtime.
The Culler repository contains neither the Memos source nor a source archive.
The original source license remains in each trial repository.

`prepare_case` verifies these records before it returns a prepared case:

- Both recorded commit objects exist.
- The base commit precedes the reviewed commit.
- The reviewed source tree matches its recorded identifier.
- The license and dependency files match their content hashes.
- Every required artifact matches its content hash.
- The isolated source checkout remains clean.

The cache preserves the upstream Git history.
Each trial clone uses independent Git object files and an independent worktree.
The trial repository has no origin remote.
A source change in one trial cannot alter another trial or the cache.

An authored change requires an immutable Git bundle with a recorded content hash.
The bundle supplies its actual reviewed commit and source tree.
The framework applies no mutable patch or moving branch name.

The framework supports these artifact sources:

- A file beside the case manifest.
- A Hypertext Transfer Protocol Secure (HTTPS) artifact address with a content hash.
- A source file from a full Git commit identifier.
- The exact binary diff of the recorded reviewed range.

The framework rejects traversal paths and local artifacts that leave their case directory through symbolic links.
The framework places Judge artifacts under a separate `references` directory.
The Packager receives only the `artifacts` directory and its source repository.

An infrastructure contract case can declare an intentionally absent evidence path.
Its `paths` record maps that path to `absent`.
Preparation verifies the absence and preserves that condition for the trial.
A missing required artifact without that declaration remains a preparation failure.

## Case API

The Application Programming Interface (API) exposes four functions:

```python
load_case(path) -> dict
validate_cases(paths, require_ready=False) -> list[dict]
case_readiness(case) -> dict
prepare_case(manifest, destination, cache, require_ready=False) -> dict
```

The destination must be absent before preparation.
`validate_cases` checks manifests and local artifact hashes without a source download.
`prepare_case` also verifies the external source, dependencies, and source-derived artifacts.
A failed preparation leaves no completed destination.

Live model trials require `require_ready=True`.
This setting blocks pending independent reviews and missing required media.
It also requires a review of the exact supplied media artifacts when a case requires media.
Development reconstruction and test doubles can use cases with recorded readiness limits.
Neither mode converts those limits into a packet quality score.

The prepared result contains these runtime fields:

- `repository_path` and `artifacts_path` identify the Packager's files.
- `references_path` and `references` identify the Judge's private evidence.
- `brief_parts`, `task_id`, and `command_responses` preserve the fixed role input.
- `base_commit` and `reviewed_commit` identify the actual source range.
- `case_id`, `group_id`, and `types` preserve corpus identity and coverage.
- `manifest_hash`, `paths`, and `readiness` record provenance and execution limits.
- `expected_terminal` and `project_skills` define the contract and fixed Project skills.

## Required case review

An independent reviewer must inspect both source commits and every material change in their range.
The reviewer must verify the reference facts and their conditions.
The reviewer must verify the limits that the authored reports state.
For a media case, the reviewer must verify the captured application states against their recorded revisions.

The review record must identify the reviewer, checked evidence, findings, and final status.
The case manifest must identify that record and its content hash before its status becomes `reviewed`.
Preparation verifies the review record and keeps a private copy for the Judge.
The review record fixes the source range and the hashes of its reference facts and limits.
A later fact edit requires a new review.
Every dataset requires independent case groups with complete review records.

Source provenance comes from the immutable [Memos commit history](https://github.com/usememos/memos).
Each manifest and reference fact links to its exact upstream commit or source file.
