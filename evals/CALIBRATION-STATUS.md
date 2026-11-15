# Current Judge calibration status

The reference batch contains ten independently reviewed packets from five pilot cases.
The Judge completed all ten assessments under the corrected risk contract.
Two malformed responses required genuine retries.
The record preserves those failed responses and accepts no repaired response.

The human scores remain blank.
The Judge remains uncalibrated until the human comparison satisfies fixed acceptance thresholds.
The current phase establishes no directive improvement.

## Human review

Open the [human reference review](/private/tmp/culler-eval-human-calibration-20260930-final/index.html).
Keep the pilot results, Judge results, and private reference records closed during the review.
Use the full-width packet view to assess the layout.
Score each packet through all six dimensions.
Identify any critical failure and give a reason.
Download the complete review after all ten packets receive assessments.

The page contains these inputs:

- The actual packet through the product wrapper
- The authoritative facts and evidence limits
- The fixed operator context and rubric guidance
- The six dimension anchors
- Blank human assessments

The page contains no Judge scores, private variant labels, or calibration assignments.
An independent Chromium review verifies all ten packets at 1280 × 900 pixels.
The controls, links, and full-width views work.
The review confirms unchanged presentation hashes and no browser errors.

## Actual reference experiment

| Property | Recorded value |
| --- | --- |
| Experiment identifier | `23092a74-3846-4117-8b70-49676dac0e05` |
| Source pilot identifier | `494d1935-fb4a-460a-b216-81c2529bc544` |
| Completed Judge assessments | 10 |
| Judge attempts | 12 |
| Failed Judge attempts | 2 |
| Pending Judge assessments | 0 |
| Recorded human assessments | 0 |
| Complete record integrity | Verified. |

The private experiment resides at `/private/tmp/culler-eval-calibration-20260930-final`.
Its source provenance binds the original pilot protocol, input, case corpus, and event 114.
The imported pilot packets retain their original bytes and source trial identities.
The source pilot's scores remain exploratory and retain their recorded limitations.

The reference experiment uses the same Judge model, harness, image, rubric, and operator context.
Its risk contract permits only the evidence that the durable record supports.
Its instructions require one complete response object without prose or Markdown fences.
The actual instruction and contract identity uses this Secure Hash Algorithm 256-bit (SHA-256) hash:

```text
1263e7cf13a0a491b4ac28891b46c14dcaea67259d77e2d80c189158571f4af4
```

All ten assessments bind the actual model, image, wrapper, viewport, packet, references, rubric, and instructions.
The final verification reports no unfinished operations, artifact errors, or record failure.
The [calibration guide](CALIBRATION.md) defines the record and export requirements.

## Record the human review

Use the downloaded file as the record input:

```sh
uv run culler-evals record-calibration /private/tmp/culler-eval-calibration-20260930-final \
  --record /absolute/path/human-reference-scores.json
```

Export the complete joined records after acceptance of the human review:

```sh
uv run culler-evals export-calibration /private/tmp/culler-eval-calibration-20260930-final \
  --output /private/tmp/culler-eval-calibration-records-20260930
```

These commands require no new Packager or Judge call.
The export creates separate calibration and independent check records.
The acceptable agreement and critical failure detection thresholds remain unresolved.
A complete reference batch supplies evidence rather than an automatic calibration pass.
