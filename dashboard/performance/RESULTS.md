# Dashboard load latency

The required dashboard scope preserves the current `mc` command-line interface (CLI).
The browser shares requests and retains successful results for at most ten seconds.
The server performs the independent outbox reads concurrently.

## Controlled comparison

The baseline uses commit `e7e2cfe44820e5c4789daf88b35424706a729032`.
Both variants use the same scratch setup and browser harness.
Each scenario has twelve observations.

The dataset contains these records:

- Two Projects.
- Twenty-four tasks through the real command interface.
- Five hundred synthetic run records.
- Five hundred synthetic archived packet records.

Controlled responses describe a configured system with valid vision files.
Each normal request still reaches the real scratch command before the harness supplies these display facts.
Each request receives an additional 100 milliseconds of delay.
The failure scenario supplies a task read failure after the same delay.

| Scenario | Baseline median | Updated median | Baseline 95th percentile | Updated 95th percentile | Baseline reads | Updated reads |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Initial Board content | 266 ms | 153 ms | 274 ms | 167 ms | 9 | 8 |
| First Pipeline content after Board | 137 ms | 132 ms | 160 ms | 143 ms | 8 | 3 |
| Fresh Board return | 1 ms | 2 ms | 2 ms | 3 ms | 6 | 0 |
| Periodic Board response | 250 ms | 142 ms | 256 ms | 155 ms | 8 | 8 |
| Failed task response | 233 ms | 135 ms | 237 ms | 140 ms | 8 | 8 |

The initial Board median decreased by 42 percent in this comparison.
The Pipeline result primarily reduces command traffic.
Its remaining independent reads still determine the time to content.
Both variants retain content immediately on a fresh Board return.
The updated variant eliminates the six extra reads for that return.

The median initial layout shift decreased from 0.366 to 0.018.
The median Pipeline layout shift decreased from 0.112 to zero.
These sums include shifts after an input and differ from the standard cumulative layout shift metric.
New issue notices can still move content when they appear.
The placeholders reserve the principal regions and avoid false empty states.

Both variants retained task cards during all twelve periodic refreshes.
The baseline cleared task cards after all twelve task read failures.
The updated variant retained those cards and identified their unavailable status.
Registry failures still clear project records.

## Measurement limits

The environment uses these tools:

- macOS.
- Chromium 139.0.7258.5.
- Bun 1.3.9.
- Go 1.25.12.

The scratch binary uses `test_fake_routing`.
It omits production Docker delegation and helper inspections.
The additional delay exposes serial request dependencies.
Production load times remain unverified.
Twelve observations provide a limited sample for the 95th percentile.

The harness measures command duration around each server command invocation.
It separately records the request duration and the time to browser content.
The host clock measures the initial navigation.
The browser clock advances the periodic refresh timers.
The response measurements use elapsed host time after that advance starts.

The evidence contains request identities and durations without tokens or record content.
The [baseline evidence](evidence/main.json) and [updated evidence](evidence/updated.json) retain the individual observations.
The [comparison test](latency.test.ts) defines the shared dataset and measurement procedure.

The comparison uses this command:

```sh
./dashboard/smoke.sh performance/latency.test.ts
```

`LATENCY_EVIDENCE` selects the output directory.
The default directory is `/tmp/homie-latency-performance`.

## Request policy

A normal page read has a maximum freshness interval of ten seconds.
An unanswered Homie conversation uses one second.
System status uses five seconds.
Explicit status checks after window focus invalidate the prior status result.
Profile forms and explicit report reads require a current response.

A successful write invalidates the affected results.
A record conflict invalidates prior results before the form reads the current record.
An old response cannot restore invalid data.
A cancelled consumer preserves a read that another consumer still requires.
The last cancelled consumer cancels its request.

Prefetch has at most one active request.
It starts after current page requests finish and yields when a visible page requests data.
Its routes contain read operations only.
It starts no conversation and acknowledges no record.

## Verification

The dashboard browser suite covers these conditions:

- Direct page links and shared request counts.
- Delayed pause reads and independent page content.
- Read deadlines and explicit retries.
- Delayed log lists and outbox lag.
- Fresh data reuse across pages.
- Focus and text selection after a refresh.
- Narrow displays and reduced motion.
- Hidden documents and request suppression.
- Authorization failure and registry failure.
- Project isolation and late responses.
- Delayed diagram assets with usable document text.

The server checks prove concurrent outbox reads and complete failure propagation.
The request checks prove freshness, invalidation, cancellation, and prefetch bounds.
The Go checks verify the new asset in the release manifest.
