# Command duration report

**Run status: complete.**

The report contains 2000 measured trials and excludes 0 declared warmups.

All durations use milliseconds. Failed trials remain in the summaries.

| Variant | Command | Count | Errors | Timeouts | Missing durations | Median | 95th percentile | 99th percentile | Maximum | Above 15 seconds |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| control | task list --project benchmark | 1000 | 0 | 0 | 0 | 131.34 | 141.97 | 146.75 | 158.88 | 0 |
| duration | task list --project benchmark | 1000 | 0 | 0 | 0 | 131.42 | 142.16 | 146.58 | 354.62 | 0 |

## Paired comparisons

| Baseline | Treatment | Command | Valid pairs | Blocks with valid pairs | Ratio | 95% interval | Missing pairs | Invalid pairs | Different output | Unpaired records |
| --- | --- | --- | ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: |
| control | duration | task list --project benchmark | 1000 | 50 | 1.0016 | 0.9967–1.0066 | 0 | 0 | 0 | 0 |

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

Input digest, Secure Hash Algorithm 256 (SHA-256): `87f430a196b22075313de084a1526f05d0608ad109bc67762703d4cea27459f9`.
