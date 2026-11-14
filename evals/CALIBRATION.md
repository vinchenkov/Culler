# Judge calibration references

This workflow collects human reference scores without exposure to Judge scores.
It uses actual pilot packets and reviewed variants of those packets.
It supplies separate calibration records and independent check records.
The [specification](SPEC.md) and [case guide](cases/README.md) define the evidence requirements.

## Reference batch

The current reference plan uses five independent pilot case groups.
Three groups supply calibration records.
Two other groups supply check records.
Each group supplies one accurate packet and one challenge packet.
The batch therefore contains ten packets.
This plan defines the reference batch only.
It defines no training, validation, or final dataset size.

The packet author can select a completed pilot packet or create a variant of it.
The current pilot selects the first submitted packet in each case group.
This selection uses chronological order rather than the Judge's score.
An accurate packet requires an independent review of its factual content.
An accurate variant can correct a source packet after independent review.
A challenge variant requires a named author and an independent reviewer.
The author and reviewer must be different entities.
The review must identify the exact packet content hash and source trial.

The challenge packets collectively cover these conditions:

- A weak explanation preserves the facts but obscures the useful mental model.
- Excess repetition adds effort without useful explanatory content.
- An attractive material error presents an incorrect claim clearly.
- An instruction injection attempts to alter the Judge's assessment instructions.

The author must preserve the source packet's submitted Summary when the variant does not test that requirement.
The author must preserve a representative case context for each variant.
The workflow generates no packet content and invents no human scores.

## Private batch manifest

The batch manifest uses format version 1.
Its `groups` object contains `record` and `check_record` lists.
These lists identify disjoint case groups from the completed pilot.
They exclude every final case group.

Each item contains these fields:

| Field | Required content |
| --- | --- |
| `id` | A distinct private item identifier. |
| `case_id` | The actual pilot case identifier. |
| `source_trial_id` | The completed pilot trial that supplies the original packet. |
| `source_packet_sha256` | The original packet's Secure Hash Algorithm 256-bit (SHA-256) content hash. |
| `kind` | `accurate` or `challenge`. This label remains private. |
| `conditions` | An empty list for an accurate packet. A challenge names its covered conditions. |
| `packet` | The packet origin and exact content hash. A variant also identifies its file path. |
| `author` | The named author of an `authored_variant`. |
| `review` | The independent review's file path and exact content hash. |

The permitted challenge condition identifiers are:

- `weak_explanation`
- `excess_repetition`
- `material_error`
- `instruction_injection`

The `packet.origin` field equals `pilot` or `authored_variant`.
An unchanged pilot packet uses the stored source bytes.
A variant identifies a file path and its actual content hash.
Relative artifact paths resolve beside the batch manifest.
Each item must contain distinct packet bytes.

The independent review uses this structure:

```json
{
  "reviewer": "independent reviewer",
  "result": "accepted",
  "packet_sha256": "the actual packet content hash",
  "case_id": "the actual case identifier",
  "source_trial_id": "the actual source trial identifier",
  "kind": "challenge",
  "conditions": ["material_error"],
  "expected_critical_failures": ["material_contradiction"],
  "observations": ["The review identifies the incorrect claim and its authoritative reference."]
}
```

The final two fields preserve private review findings.
The human page excludes those findings and the packet's challenge labels.
The Judge receives the case facts and packet without those labels or human scores.

## Human reference review

Prepare the review from a completed pilot experiment:

```sh
uv run culler-evals prepare-calibration /private/tmp/culler-eval-pilot \
  --batch /private/tmp/culler-eval-reference-batch.json \
  --output /private/tmp/culler-eval-human-calibration
```

The output directory must be absent and outside the private experiment directory.
The preparation assigns anonymous packet identifiers and a random display order.
The human page contains these inputs:

- The rendered packet through the product wrapper.
- The authoritative case facts and evidence limits.
- The fixed operator context, rubric scope, and assessment guidance.
- The approved guidance for each dimension.
- The six rubric dimensions and their score anchors.
- Blank score fields and a blank critical failure determination.

The page includes an option to open the packet at full width.
The human must use that view to assess the packet's layout at the configured desktop width.
The human must keep Judge scores, private variant records, and the experiment viewer closed during the review.
The output directory contains no Judge scores or calibration/check labels.

The human gives a score from zero to four for each dimension:

- Comprehension
- Causal explanation
- Decision support
- Visual explanation
- Clarity
- Packet length

The human also identifies a critical failure, gives a reason, and names the reviewer.
The page requires an explicit confirmation that the reviewer inspected no Judge scores or private labels.
The page saves local progress and downloads a complete human record.
The generated `review.json` also supports direct edits.
Its blank values cannot count as reference scores.

Record the human review:

```sh
uv run culler-evals record-calibration /private/tmp/culler-eval-pilot \
  --record /private/tmp/human-reference-scores.json
```

The command requires every reference packet exactly once.
The command verifies the original human instructions and rendered packet files through their content hashes.
An edited or absent human presentation prevents acceptance of the scores.
It freezes the human record independently of Judge results.
A second human record requires a new reference batch and experiment record.

## Judge assessments

An unchanged packet can reuse its original Judge assessment when its context is complete and matches the fixed presentation.
That assessment must bind the exact packet bytes, source trial, reference facts, and rubric.
It must also bind these assessment inputs:

- The exact public Judge runtime configuration.
- The actual model, harness, and immutable image recorded by the runtime.
- The configured desktop viewport.
- The exact product wrapper bytes that the browser served.
- The fixed operator context.
- The rubric's assessment text, score anchors, and critical failure rules.
- The exact Judge instructions, semantic checks, and deterministic packet contract.

The workflow preserves an older source assessment that lacks these context bindings.
It records why that assessment cannot support reuse.
The unchanged packet then receives a fresh Judge assessment.
The workflow invents no missing context or trace evidence.
A corrected assessment contract requires a new reference experiment and fresh Judge results.
An imported pilot packet retains its original source identity and assessment limitations.
An authored variant requires a fresh Judge assessment.
The workflow rejects an original assessment for changed packet bytes.
Every calibration reference requires complete inspection.
Uninspected content or a confirmed render failure prevents acceptance of that assessment.

Assess the pending reference packets:

```sh
uv run culler-evals assess-calibration /private/tmp/culler-eval-pilot \
  --cache /private/tmp/culler-eval-source-cache \
  --credentials /private/tmp/private-config.json
```

This command invokes the configured Judge through the actual browser.
It supplies the same case facts, rubric, and submitted Summary as the source trial.
The Judge receives no human ratings or expected challenge outcomes.
Each assessment must bind its own packet content hash.
The record retains raw Judge results, including a rejected malformed result.

The human review and Judge assessment can occur in either order.
Their results remain separate until both are complete.
The pending assessment interface checks the fixed packet bytes, case facts, and rubric before execution.
It also checks the fixed operator context.
The command checks current wrapper bytes and the Judge configuration against the fixed presentation.

## Calibration records

Export the complete records:

```sh
uv run culler-evals export-calibration /private/tmp/culler-eval-pilot \
  --output /private/tmp/culler-eval-calibration-records
```

The export creates `record.json` and `check_record.json`.
Each file contains the human and Judge assessments of the same packets.
The command returns each file path and content hash for the experiment protocol.
The three calibration groups remain separate from the two check groups.
Every final case group remains outside both sets.
Each exported record binds its packet, case references, and assessment context.
The full experiment verifies that context against its selected Judge configuration and product wrapper.
Rubric status and aggregate dimension weights can change after calibration.
The assessment text, operator context, and Judge configuration must match the reference records.

The calibration summary compares the six dimension scores and detects known critical failures.
The protocol fixes the acceptable agreement and detection thresholds separately.
A complete reference batch establishes no automatic calibration pass.
The full experiment requires accepted results from both the calibration and independent check records.

## Python interface

The interface supplies these functions:

```python
prepare_calibration_review(store, source, output, batch, batch_root=None)
calibration_assessment_requests(store)
record_calibration_assessments(store, assessments)
record_human_calibration(store, record)
export_calibration_records(store, output)
```

Each pending Judge request supplies an anonymous packet identifier, fixed case, packet artifact, packet path, and source trial.
It supplies no human scores, challenge labels, or expected errors.
Each new Judge result requires a distinct assessment identifier.
The result must identify its case, packet artifact, reference hash, and rubric hash.
The private presentation retains the author, reviewer, source trial, challenge conditions, and dataset assignment.

The workflow requires actual completed pilot records.
Test fixtures demonstrate the interface and establish no packet quality result.
