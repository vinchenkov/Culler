# Mac command results: 2026-10-11

[Open the interactive visual report](RESULTS.html).

This report preserves the experiments from 2026-10-11.
The product changes use Standard policy and command duration records.
Read deadlines, the eight-command admission limit, and deferred library initialization remain outside those product changes.
Their results remain here as historical evidence.
The original experimental code remains on the local `perf/mac-command-latency` branch.
The archive establishes no measurement of later code.

Standard reduced command duration in both completed policy comparisons.
It reduced the paired geometric mean duration by 43.6 percent without an added processor workload.
It reduced that duration by 42.4 percent with six processor workers.
Standard is the only retained product change that reduced duration in the serial comparisons.

The experimental read deadline and eight-read admission limit reduced duration by 32.33 percent with 16 concurrent requests.
That comparison uses Standard policy for both variants.
It does not isolate the admission limit from the deadline wrapper.

Deferred netdb initialization reduced duration by 3.24 percent in a separate dependency experiment.
That overlay changes public dependency behavior and remains outside the product fix.

The tests did not reproduce the original delay of approximately 16 seconds.
They do not establish the cause of that entire delay.
They also provide no proof that a memory leak caused it.

## Measured boundary

The timer starts immediately before the persistent Bun worker calls the dashboard command runner.
It ends when the runner returns the parsed result after process exit, closed output streams, and any enabled completion observer.

```text
Mac: persistent Bun worker
  -> Mac: new mc process and library initialization
  -> Mac: three Docker commands inspect the image, volume, and helper
  -> Mac: Docker client sends docker exec
  -> Docker virtual machine: new Linux mc process in the active helper
  -> Docker volume: database read
  -> Mac: result return, parse, and optional completion observer in Bun
```

The helper remains active between reads.
Each read starts a Linux process inside that helper.
The browser request, Bun startup, and resident remain outside this measurement.
The dashboard command path does not pass through the resident.

These results measure the complete command path above.
They do not assign separate durations to library initialization, Docker inspections, Docker execution, or the database read.
The selected Mac process policy explains the difference between the paired variants.
The measurements do not assign the original 16-second delay to any one of those operations.
The product policy change also affects the resident, but these experiments measure only dashboard reads.
The duration results establish no rate of completed requests under sustained demand.

## Fixture and method

The fixture contains one Project and 100 fixed tasks in a separate Docker volume.
Each measured call reads the task list for that Project.
The helper stays active, and the fixture has no resident or agent runs.

The host uses macOS 26.6.2, 14 logical processors, and Bun 1.3.9.
Each policy pair uses the same Mac binary, dashboard runner, Linux image, and fixture.
The controller checks the image identity before each block and at completion.
The metadata records the binary, source, runner, and tool identities.

The policy experiments ran in this order:

1. Background baseline: 1000 reads, with trial-order seed 31.
2. Background versus Standard: 1000 pairs, with trial-order seed 47.
3. Background versus Standard with six processor workers: 1000 pairs, with trial-order seed 59.

Each variant uses one persistent Bun worker.
Ten initial reads per variant prepare caches and remain in a separate file.
The measured trials contain 50 blocks of 20 pairs, or 20 reads for the baseline.
The controller randomizes the variant order within each pair.
These three experiments execute one measured read at a time.

The added workload uses six separate processes with Standard policy.
Each process repeatedly hashes the same 64-kibibyte buffer and retains no results.
The [load record](results/2026-10-11/load-six.json) confirms six workers and successful cleanup.
This workload exercises processor contention without deliberate memory pressure.

The confidence interval uses 5000 bootstrap samples of whole blocks.
The reported ratio is the geometric mean of the paired Standard-to-Background duration ratios.
Its 95 percent confidence interval describes that ratio, not the difference between the medians.
The calculation uses pairs with valid results and equal output digests.

## Completed results

All durations below use milliseconds.
Every row contains 1000 measured reads.
The 95th percentile is the duration at or above 95 percent of observations.

| Experiment | Policy | Median | 95th percentile | 99th percentile | Maximum |
| --- | --- | ---: | ---: | ---: | ---: |
| First baseline | Background | 245.92 | 286.37 | 317.13 | 1125.07 |
| Paired, without added workload | Background | 249.48 | 296.72 | 409.44 | 1289.06 |
| Paired, without added workload | Standard | 139.98 | 163.44 | 176.98 | 253.64 |
| Paired, six processor workers | Background | 215.80 | 246.82 | 273.17 | 441.83 |
| Paired, six processor workers | Standard | 124.86 | 136.06 | 167.41 | 340.66 |

| Paired experiment | Valid pairs | Blocks | Standard / Background ratio | 95 percent confidence interval |
| --- | ---: | ---: | ---: | --- |
| Without added workload | 1000 | 50 | 0.5640 | 0.5550–0.5726 |
| Six processor workers | 1000 | 50 | 0.5756 | 0.5688–0.5821 |

All 5000 policy-test reads succeeded.
No read exceeded 15 seconds.
Both paired comparisons contain no missing pairs, invalid pairs, or different-output pairs.

The first baseline describes the initial condition.
The randomized comparisons provide the evidence for the policy effect.
The two load conditions occurred at different times.
Their absolute durations do not establish that additional processor load improves performance.

The complete reports and completion records are retained here:

- Baseline: [report](results/2026-10-11/baseline-1000/validated-report.md), [completion](results/2026-10-11/baseline-1000/completion.json), [metadata](results/2026-10-11/baseline-1000/metadata.json).
- Policy comparison: [report](results/2026-10-11/policy-1000/validated-report.md), [completion](results/2026-10-11/policy-1000/completion.json), [metadata](results/2026-10-11/policy-1000/metadata.json).
- Loaded policy comparison: [report](results/2026-10-11/policy-loaded-1000/report.md), [completion](results/2026-10-11/policy-loaded-1000/completion.json), [metadata](results/2026-10-11/policy-loaded-1000/metadata.json).

Each result directory also retains compressed trial records, initial reads, and block records.

## Command duration records

The fourth experiment compares two Standard workers with the same rebuilt Mac binary and scratch Linux binary.
The control uses the product command runner without its completion observer.
The treatment enables the dashboard completion observer and its file sink.
It changes no command deadline or admission limit.

This comparison uses 1000 randomized pairs in 50 blocks, with trial-order seed 67.
Each variant also executes ten separate initial reads.
The rebuilt binaries differ from the binaries in the earlier policy experiments.
The [build manifest](results/2026-10-11/build-manifest.json) and [scratch Linux record](results/2026-10-11/linux-replacement.json) preserve those identities.
The paired result isolates the duration-record cost within this experiment.

| Variant | Reads | Median | 95th percentile | 99th percentile | Maximum |
| --- | ---: | ---: | ---: | ---: | ---: |
| Control | 1000 | 131.34 | 141.97 | 146.75 | 158.88 |
| Duration records | 1000 | 131.42 | 142.16 | 146.58 | 354.62 |

The paired geometric mean ratio is 1.0016.
Its 95 percent confidence interval is 0.9967–1.0066.
The interval includes one, so this comparison establishes no typical duration change.
It does not prove zero overhead.
The treatment maximum of 354.62 milliseconds remains part of the evidence.

All 2000 reads succeeded and returned equal paired results.
No read exceeded 15 seconds.
The [report](results/2026-10-11/duration-1000/report.md), [completion record](results/2026-10-11/duration-1000/completion.json), and [metadata](results/2026-10-11/duration-1000/metadata.json) retain the full comparison.

## Read deadlines with serial requests

This experimental behavior remains outside the product changes.
The fifth experiment compares the plain command runner with the read-deadline adapter.
Both variants use Standard policy and the same Mac and Linux binaries.
The adapter gives each read a 15-second deadline and uses the shared admission limit of eight.
It enables no completion observer.

This comparison uses 1000 randomized pairs in 50 blocks, with trial-order seed 71.
Each variant also executes ten separate initial reads.
The serial workload never exhausts the eight admission slots.
It measures the deadline path for successful reads, with no deadline expiration or request cancellation.

| Variant | Reads | Median | 95th percentile | 99th percentile | Maximum |
| --- | ---: | ---: | ---: | ---: | ---: |
| Control | 1000 | 130.68 | 140.83 | 145.14 | 148.29 |
| Read deadline | 1000 | 130.86 | 140.04 | 144.51 | 154.81 |

The paired geometric mean ratio is 0.9993.
Its 95 percent confidence interval is 0.9950–1.0034.
The interval includes one, so this comparison establishes no typical duration change.
It provides no evidence of recovery after overload or immediate Linux cancellation.

All 2000 reads succeeded and returned equal paired results.
No read exceeded 15 seconds.
The [report](results/2026-10-11/deadline-1000/report.md), [completion record](results/2026-10-11/deadline-1000/completion.json), and [metadata](results/2026-10-11/deadline-1000/metadata.json) retain the full comparison.

## Deferred netdb initialization

The sixth experiment changes the Mac dependency that initializes local protocol and service tables.
The control initializes those tables when its package starts.
The treatment postpones that initialization until the first table lookup.
Both Mac binaries use the same scratch module file; only the treatment uses the source overlay.

Both variants use Standard policy, the same dashboard runner, and the same Linux helper binary.
The three Docker inspections remain in both command paths.
This comparison measures the Mac dependency change within the complete command path.
It changes no Linux library initialization or database operation.

This comparison uses 1000 randomized pairs in 50 blocks, with trial-order seed 73.
Each variant also executes ten separate initial reads.

| Variant | Reads | Median | 95th percentile | 99th percentile | Maximum |
| --- | ---: | ---: | ---: | ---: | ---: |
| Control | 1000 | 130.44 | 141.47 | 146.01 | 349.83 |
| Deferred netdb initialization | 1000 | 126.63 | 136.51 | 142.24 | 149.75 |

The paired geometric mean ratio is 0.9676.
Its 95 percent confidence interval is 0.9623–0.9722.
This corresponds to 3.24 percent shorter duration, with a confidence interval from 2.78 to 3.77 percent shorter.
All 2000 reads succeeded and returned equal paired results.
No read exceeded 15 seconds.

The overlay leaves exported tables empty until the first lookup.
That difference changes the dependency's public behavior.
This overlay remains an experiment.
A product change must preserve the required public behavior and the Mac command's backup verification.
The small measured reduction does not explain the original 16-second delay.

The [report](results/2026-10-11/netdb-1000/report.md), [completion record](results/2026-10-11/netdb-1000/completion.json), and [metadata](results/2026-10-11/netdb-1000/metadata.json) retain the full comparison.
The [build manifest](results/2026-10-11/build-manifest.json) identifies both Mac binaries and their shared scratch module file.

## Linux deadline after Mac client exit

This test verifies the experimental Linux wrapper, which remains outside the product changes.
This test uses the verified scratch helper and its normal setuid command wrapper.
It starts a Linux status read and blocks its input on a named pipe.
The test then kills that read's Mac Docker client.
Each Linux process remains present immediately after the client exits.

Five plain reads remain present beyond the two-second test deadline.
The test stops each plain read after it verifies the process identity.
Five reads with the private deadline wrapper exit with code 124 without that cleanup signal.
Their Linux durations are 2004, 2005, 2003, 2009, and 2004 milliseconds.
All ten trials pass, and all ten records confirm cleanup.

The two-second deadline starts from the Linux clock before the command begins.
The recorded duration extends from that Linux timestamp through the observed Linux process exit.
This test proves that the Linux deadline can stop this blocked read after the Mac Docker client exits.
It does not prove immediate Linux cancellation when the browser cancels a request.
It does not test a long delay before the Linux deadline wrapper starts.
It also does not test differences between the Mac and Linux clocks.

The [disconnect evidence](results/2026-10-11/deadline-disconnect-real/evidence.json) retains each process identity, user identity, exit, and cleanup result.
The [helper install record](results/2026-10-11/helper-install-verified.json) identifies the tested binary and confirms its owner and mode.

## Incomplete concurrent experiment

The first concurrent experiment stopped after a read exceeded its absolute deadline during Mac sleep.
The Mac power record identifies Idle Sleep at 14:47:46, with a duration of 1047 seconds.
The failed read records its start at 14:47:48.
The Mac records DarkWake at 15:05:13, and the controller records its failure at that time.
All times in this paragraph use the local offset of minus seven hours.

The failed read is `trial-269-deadline`, in block 13.
Its monotonic duration is 526.364 milliseconds; that duration excludes the sleep interval.
Its exit code is two, and its error digest matches "The read exceeded its deadline."
The absolute deadline includes the sleep interval and expires before the read returns.
The controller stops the experiment and confirms worker cleanup.

This incomplete experiment supports no performance comparison.
It identifies the cause of this experimental deadline failure, not the cause of the original dashboard incident.
The [sleep events](results/2026-10-11/concurrent-1000/sleep-events.txt), [attribution record](results/2026-10-11/concurrent-1000/sleep-attribution.json), and [completion record](results/2026-10-11/concurrent-1000/completion.json) preserve the evidence.

The replacement experiment inhibits idle sleep for its duration.
The revised worker records both wall-clock and monotonic durations.
The controller rejects a read when those durations differ by more than 1000 milliseconds.
That guard identifies an invalid time comparison; the power record establishes whether sleep caused it.

## Read deadlines with concurrent requests

This experimental behavior remains outside the product changes.
The replacement experiment completes 1000 reads per variant in 50 blocks of 20 reads.
Each variant receives up to 16 concurrent requests per block.
The controller randomizes the variant order within each block, with trial-order seed 79.
Ten initial reads per variant remain outside the measured trials.
The experiment adds no separate processor workload.

Both variants use Standard policy and the same Mac and Linux binaries.
The control uses the plain command runner, which admits all 16 requests.
The treatment uses the read-deadline adapter and admits at most eight reads on the Mac.
Other treatment reads wait for admission, and each read's measured duration includes that wait.
The treatment applies the same 15-second deadline as the serial experiment.

| Variant | Reads | Median | 95th percentile | 99th percentile | Maximum |
| --- | ---: | ---: | ---: | ---: | ---: |
| Control | 1000 | 963.25 | 1401.25 | 1656.67 | 1760.59 |
| Read deadline and eight-read admission limit | 1000 | 600.15 | 982.07 | 1169.35 | 1187.28 |

The paired geometric mean ratio is 0.6767.
Its 95 percent confidence interval is 0.6455–0.7095.
This corresponds to 32.33 percent shorter duration, with a confidence interval from 29.05 to 35.45 percent shorter.
The confidence interval resamples the 50 blocks.
All 2000 reads succeed, and all 1000 pairs return equal results.
No read expires, exceeds 15 seconds, or fails the clock comparison.

The measured effect belongs to the combined deadline and admission change under this workload.
No deadline expires, so the experiment establishes no duration benefit from deadline termination.
The admission limit reduces simultaneous Mac commands, but the experiment does not isolate that limit from the wrapper.
The successful serial deadline experiment establishes no typical duration change.

This finite-batch experiment measures per-read duration, not browser throughput.
The worker computes each output digest after that read's timer stops.
That digest calculation can delay other concurrent reads on the shared Bun event loop.
The harness therefore differs from the complete dashboard request path.

The [report](results/2026-10-11/concurrent-awake-1000/report.md), [completion record](results/2026-10-11/concurrent-awake-1000/completion.json), and [metadata](results/2026-10-11/concurrent-awake-1000/metadata.json) retain the complete comparison.

## Policy attribution

The native launcher sets the process type on the persistent Bun worker without launchd.
A separate native check reads the policy of a child that Bun starts.

| Bun policy | Native child task priority | Native child thread priority |
| --- | ---: | ---: |
| Background | 31 | 4 |
| Standard | 31 | 31 |

The child task priority alone would hide the difference.
The [thread evidence](results/2026-10-11/preflight-thread-policy-inheritance.json) confirms different inherited thread priorities in the native probe.
The earlier [task-only check](results/2026-10-11/preflight-policy-inheritance.json) covers a different field.

The performance comparison changes process type.
It does not isolate the effect of thread priority alone.
It does not establish equivalence with every resource policy that launchd applies.
The experiment changes the policy of scratch processes and leaves the deployed launchd units unchanged.

## Memory and incident limits

The retained memory summaries measure the scratch Bun workers.
They do not measure allocations inside the Mac command, Docker, the Linux command, or the deployed resident.

The first Background baseline has a lower memory median in its last 100 reads than its first 100 reads.
The paired comparison has higher final memory medians in both policy variants.
Those snapshots cannot identify retained allocations or establish a leak during long use.
See the [baseline memory summary](results/2026-10-11/baseline-1000/worker-memory-summary.json) and [paired memory summary](results/2026-10-11/policy-1000/worker-memory-summary.json).

The longest observation in the completed performance comparisons is 1760.59 milliseconds.
The original delay of approximately 16 seconds remains outside the delays that these trials reproduced.
Zero failures in this fixture do not prove that production timeouts cannot recur.
One thousand observations per variant also provide limited evidence about rare delays.

## Remaining limits

The experimental deadline design limits admitted dashboard reads on the Mac to eight.
It does not establish a strict maximum of eight Linux processes.
Clock differences and delays before Linux wrapper entry remain separate limits.

The netdb overlay changes when exported tables receive their contents.
It remains an experiment until a product change preserves the required dependency behavior.

## Historical source and cleanup

The [candidate source hashes](results/2026-10-11/candidate-sources.json) identify the original experimental source.

The cleanup script removes the exact scratch helper and volume after all experiments end.
The [cleanup record](results/2026-10-11/fixture-cleanup.jsonl) retains the removal result.
The separate [verification record](results/2026-10-11/cleanup-verified.json) confirms that both resources are absent.
The experiments leave the deployed helper, volume, and launchd units unchanged.
