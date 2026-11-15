# Command duration report

**Run status: complete.**

The report contains 2000 measured trials and excludes 0 declared warmups.

All durations use milliseconds. Failed trials remain in the summaries.

| Variant | Command | Count | Errors | Timeouts | Missing durations | Median | 95th percentile | 99th percentile | Maximum | Above 15 seconds |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| control | task list --project benchmark | 1000 | 0 | 0 | 0 | 130.44 | 141.47 | 146.01 | 349.83 | 0 |
| netdb | task list --project benchmark | 1000 | 0 | 0 | 0 | 126.63 | 136.51 | 142.24 | 149.75 | 0 |

## Paired comparisons

| Baseline | Treatment | Command | Valid pairs | Blocks with valid pairs | Ratio | 95% interval | Missing pairs | Invalid pairs | Different output | Unpaired records |
| --- | --- | --- | ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: |
| control | netdb | task list --project benchmark | 1000 | 50 | 0.9676 | 0.9623–0.9722 | 0 | 0 | 0 | 0 |

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

Input digest, Secure Hash Algorithm 256 (SHA-256): `91d3916c6fb31c6966a5d8b6480d21e0d18f798f630d49bfe1e956483522f8db`.
