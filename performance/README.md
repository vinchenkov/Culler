# Mac command performance tests

This suite measures the dashboard command path on macOS.
The timer starts immediately before Bun calls the dashboard command runner.
It includes any wait for a dashboard admission slot.
The timer ends after the process exits, both output streams close, and the runner parses the result.
It also includes the completion observer when a variant enables that observer.

The measured path contains these components:

1. The dashboard command runner in Bun.
2. The Mac `mc` process and its library initialization.
3. Three Docker inspections of the image, volume, and helper.
4. The Docker execution request.
5. The Linux `mc` process and its database read.
6. The result return and parse in Bun.

The browser, the network request, and Bun startup remain outside this measurement.
The resident does not participate in this dashboard path.
The helper stays active across trials.

The product changes use Standard policy and command duration records.
This suite supports separate comparisons of policy, processor load, and duration records.
The [historical report](RESULTS.md) also retains deadline, admission-limit, and library experiments that remain outside the product.

## Test category

This is an integration performance benchmark.
It measures several real components together.
Functional tests check whether a result is correct.
Performance tests measure time and resource use for a correct result.

The suite remains separate from the fast suite.
It requires macOS, Docker, a compatible local `mc-prod` image, and the pinned Bun version.
The native launcher uses a private Apple process-type interface and refuses execution when that interface is absent.
It starts scratch Bun processes without launchd.
The controller verifies their task priorities.
A separate native preflight confirmed child thread priorities of 4 for Background and 31 for Standard.
The child task priority alone does not identify its thread policy.

Run the benchmark manually before a relevant performance change.
A dedicated Mac can also execute it on a schedule or before a release.
Shared continuous integration (CI) workers introduce differences in processor load and Docker state.
An absolute time limit on those workers can reject correct changes or miss a regression.
Repeated comparisons on the same Mac provide more useful evidence.

The deterministic benchmark checks use no Docker.
They can run in a pre-commit check or a continuous integration job.

```sh
mise exec -- bun test performance/statistics.test.ts performance/worker.test.ts
python3 -m unittest discover -s performance -p '*_test.py'
```

The repository fast suite remains Docker-free.
This change keeps the benchmark checks separate from that suite.
The repository release workflow currently runs its fast checks for pull requests and pushes to `main`.
This performance suite adds no automatic publication or deployment.

## Fixture and commands

The following commands serve the coding harness.
Use a new root for every fixture.
The preparation script copies the selected Mac binary and the dashboard runner before any experiment.
The selected binary and image must use compatible source and schema versions.
The default binary is the installed binary at `~/.local/bin/mc`.

```sh
python3 performance/prepare.py --root /private/tmp/culler-performance/example
caffeinate -i python3 performance/compare.py \
  --config /private/tmp/culler-performance/example/config.json \
  --output /private/tmp/culler-performance/example/baseline-results \
  --variants background --trials 1000
```

Preparation creates one Project and 100 tasks in a separate Docker volume.
The fixture has no resident, agent runs, credentials, or provider requests.
Its resource names derive from its scratch home.
The preparation script never uses the live deployment home.
Each comparison needs a new output directory.
The fixture reserves its `baseline` directory for the copied dashboard runner.

After the baseline completes, compare Standard with Background:

```sh
caffeinate -i python3 performance/compare.py \
  --config /private/tmp/culler-performance/example/config.json \
  --output /private/tmp/culler-performance/example/policy \
  --trials 1000
mise exec -- bun performance/report.ts \
  --input /private/tmp/culler-performance/example/policy/trials.jsonl \
  --output /private/tmp/culler-performance/example/policy/report.json \
  --baseline background --treatment standard
```

Each variant uses a persistent Bun process.
Each pair executes the same read against the same fixture.
The controller chooses the order within each pair with a fixed random seed.
The default executes one measured command at a time.
With `--concurrency 16`, each block contains one batch per variant.
The controller randomizes variant order within each block and compares matching read indices.
Each batch admits up to 16 requests to the dashboard runner.
The worker computes an output digest after each command returns.
That work can delay other concurrent reads on the shared Bun event loop.
This finite-batch experiment measures command duration, not browser throughput.
Ten initial calls per variant prepare caches and remain in a separate file.
The controller stops if the image identity changes, a read fails, or two variants return different results.
The controller retains the failed trial and marks the run incomplete.

Keep the Mac awake for each experiment.
The `caffeinate -i` prefix prevents idle system sleep while the command runs.
It does not prevent forced sleep or sleep from a closed laptop lid.
Each result records its completion time and elapsed wall-clock time.
The wall clock includes sleep that the Bun performance timer can exclude.
The controller compares both durations for every result.
A difference greater than 1000 milliseconds marks the result invalid and stops the experiment with `clock_discontinuity`.
The raw result retains both durations and the command exit code.
Inspect the sleep and clock records before a new run.

## Results and confidence

The [retained results](RESULTS.md) contain the completed comparisons and their limits.

The report includes these measures:

- The median describes the middle observation.
- The 95th percentile describes the time at or above 95 percent of observations.
- The 99th percentile describes the time at or above 99 percent of observations.
- The maximum describes the slowest observation.
- Error counts and calls above 15 seconds remain visible.
- The paired ratio compares treatment duration with baseline duration.
- A 95 percent confidence interval describes uncertainty in that ratio under the observed conditions.

The confidence calculation resamples blocks of adjacent pairs.
This preserves some dependence between nearby observations.
A ratio below one indicates a shorter duration for the treatment.
The ratio includes only valid pairs with equal output digests.
The report separately counts each excluded pair.
Duration summaries retain failures with recorded durations.
A controller timeout has no exact completion duration and remains an error.

One thousand trials give useful estimates of common delays.
They give limited evidence about rare delays.
More trials cannot correct a different workload, an empty fixture, or an uncontrolled change between variants.
Repeat experiments under predefined host loads and across sessions before a release decision.
Record each load condition separately.
The `load.py` wrapper supplies a separate, bounded processor workload.
Its workers repeatedly hash a fixed 64 KiB buffer and retain no results.
The wrapper caps their count at half the logical processors and stops its own process groups afterward.

```sh
caffeinate -i python3 performance/load.py \
  --workers 6 --max-seconds 1800 \
  --policy-launcher /private/tmp/culler-performance/example/bin/policy-launcher \
  --record /private/tmp/culler-performance/example/load.json -- \
  python3 performance/compare.py \
    --config /private/tmp/culler-performance/example/config.json \
    --output /private/tmp/culler-performance/example/loaded-policy \
    --trials 1000
```

Use the launcher path recorded in the fixture configuration.

Raw records include the source identity, executable hashes, image identity, process priorities, memory data, and host load.
Output digests establish equal results without a copy of the records from the database.
The timer includes any delay before Bun observes process exit.
That delay belongs to the dashboard response time.
It does not identify the exact kernel exit time.

## Separate experiments

Keep the Standard variant as the control for each later change.
Change only one factor per comparison.
Duration records measure diagnostic coverage and their own overhead.

The duration comparison uses the current dashboard runner for both variants.
The control omits the completion observer.
The treatment uses `duration-records.ts` to enable the production observer and a scratch log file.
Both variants use the same copied Mac binary, helper, fixture, and Standard policy.
Run these commands from the repository root after fixture preparation:

```sh
python3 - <<'PY'
import json
from pathlib import Path

root = Path('/private/tmp/culler-performance/example')
config = json.loads((root / 'config.json').read_text())
binary = config['variants'][0]['mcBin']
repo = Path.cwd()
config['variants'] = [
    {'name': 'control', 'policy': 'standard', 'mcBin': binary,
     'runnerModule': str(repo / 'dashboard/src/mc.ts')},
    {'name': 'duration', 'policy': 'standard', 'mcBin': binary,
     'runnerModule': str(repo / 'performance/duration-records.ts')},
]
with (root / 'duration-config.json').open('x') as output:
    json.dump(config, output, indent=2)
    output.write('\n')
PY
caffeinate -i python3 performance/compare.py \
  --config /private/tmp/culler-performance/example/duration-config.json \
  --output /private/tmp/culler-performance/example/duration-results \
  --trials 1000
mise exec -- bun performance/report.ts \
  --input /private/tmp/culler-performance/example/duration-results/trials.jsonl \
  --output /private/tmp/culler-performance/example/duration-results/report.json \
  --baseline control --treatment duration
```

The historical deadline and library experiments require the original experimental code.
The local `perf/mac-command-latency` branch preserves that code.
The reusable suite excludes its deadline adapter, disconnect test, helper installer, and library overlay.
The retained results describe the original experiments and establish no measurement of later code.

## Cleanup

```sh
python3 performance/cleanup.py --config /private/tmp/culler-performance/example/config.json
```

Cleanup verifies the exact helper identity and volume mount before it removes those scratch resources.
It preserves the benchmark evidence.
Archive the raw records and reports outside temporary storage when they support a retained result.
