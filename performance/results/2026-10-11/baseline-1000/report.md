# Command duration report

The report contains 1000 measured trials and excludes 0 declared warmups.

All durations use milliseconds. Failed trials remain in the summaries.

| Variant | Command | Count | Errors | Timeouts | Missing durations | Median | 95th percentile | 99th percentile | Maximum | Above 15 seconds |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| background | task list --project benchmark | 1000 | 0 | 0 | 0 | 245.92 | 286.37 | 317.13 | 1125.07 | 0 |

## Paired comparisons

No paired comparison was requested.

## Method

- Only trials explicitly marked as warmups are excluded from duration summaries.
- Failed trials with measured durations remain in every duration summary.
- Trials without durations count as errors when their valid field is false; they do not enter duration quantiles.
- Timeout durations are lower bounds when the controller stops a command before completion.
- Paired ratios require valid, positive durations and matching output digests when both digests exist.
- The comparison reports missing, invalid, and different-output pairs separately.
- The bootstrap resamples blocks and needs at least two blocks with valid pairs for a confidence interval.
- The completeBlocks field counts blocks with at least one valid pair.
- A ratio below one means the treatment has a lower geometric mean duration.
- Confidence intervals do not remove bias from different host conditions between variants.

Input digest, Secure Hash Algorithm 256 (SHA-256): `02e56c02aefce1723ad66bb425144b78fe11a8f9f519e1cdb44450056c84417a`.
