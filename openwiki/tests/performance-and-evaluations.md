---
type: testing
title: Performance experiments and agent evaluations
description: Experiment boundaries, retained evidence, and limits on performance and agent quality claims.
tags: [tests, performance, evaluations]
sources:
  - id: openwiki-source-897d1ce76174ffad4e936b32
    resource: repo://evals/cases/verification/corpus-amendments.json
  - id: openwiki-source-9653c835faef73816261afa0
    resource: repo://evals/culler_evals/assessment.py
  - id: openwiki-source-2be379c33f071a0be3a55552
    resource: repo://evals/culler_evals/runtime.py
  - id: openwiki-source-9bcc2dc53d09a23139672935
    resource: repo://mc/cmd/mc/evaluation.go
  - id: openwiki-source-bdb34dc5adc4a25690eda4c2
    resource: repo://mc/evalcase/README.md
  - id: openwiki-source-26c5d70e54df94e2f1b4579b
    resource: repo://performance/compare.py
generated: { by: "culler-scribe", at: "2026-11-12T00:11:01.469Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-12T00:11:01.469Z
---

# Performance experiments and agent evaluations

Culler keeps performance experiments and agent evaluations outside the product fast suite.
Both use isolated fixtures and preserve evidence for later review.
A successful framework check establishes tool behavior. It does not establish a product improvement.

## Dashboard command performance

`performance/prepare.py` creates a scratch fixture with one Project and 100 tasks in a separate Docker volume.
The fixture has no resident, agent runs, or provider requests.
`performance/compare.py` drives persistent Bun workers through the dashboard command runner.
The path includes the Mac command, helper checks, Docker execution, the Linux database read, and result parse.
Browser requests and Bun startup remain outside the measurement.

The timer ends after process exit, closed output streams, and result parse.
An enabled completion observer also lies inside the measured call.
The controller randomizes variant order with a fixed seed and compares matching read results.
A failed read, changed image identity, or unequal output digest stops the experiment as incomplete.
Warmup calls remain separate from measured trials.

The controller compares wall time with the command timer.
A difference above 1000 milliseconds stops the experiment with a clock discontinuity.
This prevents host sleep from silently distorting the result.
`statistics.ts` calculates duration summaries and paired ratios with block resampling.
The report retains errors and excluded pairs.
The concurrent workload measures finite batches of commands, not browser throughput.

`performance/RESULTS.md` and its visual report preserve the 2026-10-11 experiments.
They report shorter serial command durations under Standard policy in the measured conditions.
They do not reproduce the entire original delay or establish a memory leak.
Experimental deadlines, admission limits, and deferred library initialization remain separate from the retained product changes.
The archive establishes no measurement of later source.

The benchmark requires macOS, Docker, compatible binaries, and fixed tool versions.
Its deterministic Python and TypeScript checks require no Docker.
`clock_test.py`, `compare_test.py`, `statistics.test.ts`, and `worker.test.ts` check the experiment machinery.
The [test lanes](checks-and-test-lanes.md) describe normal product verification.

## Agent evaluations

`evals/` contains the Python framework for Packager directive experiments.
Its protocol fixes the cases, models, rubric, trial counts, and completion rule.
The corpus contains 35 Memos cases, divided into training, validation, and final holdout sets.
The Judge remains uncalibrated. Directive adoption requires final evidence and a separate source change.

The framework runs isolated Packager, Judge, and Optimizer sessions through product harness adapters.
Its Go bridge builds a disposable module copy and preserves product source.
Pinned cases and artifact digests establish reproducible inputs.
Chronological records preserve candidate ancestry and trial results; the viewer exposes those records for review.
`evals/check.sh` checks the framework without model calls.
Real trials require the selected runtime image and private credentials.
The Go contract parity check uses the copied module's `test.sh`, including its Linux scratch-spine selection.

`evals/cases/verification/corpus-amendments.json` binds current case manifests to the retained assignment proof and source review.
It records task scope and report-role adjustments. It supplies no new application test evidence.

## Packet sections in evaluations

`evals/culler_evals/assessment.py` defines packet contract revision 3.
The case's brief parts select the first section.
A Strategic rationale part selects Strategic rationale; its absence selects Summary.
Only the selected first section enters the Judge's section order.
The checker refuses a packet that contains both first sections.

The default required sections are the selected first section and Risk and rollback.
A case can supply its own required section list.
`evaluator.py` and calibration in `cli.py` use the same selection procedure.
`runtime.py` supplies the matching section order to the Judge.
This keeps assessment consistent with the task origin represented in the case input.

The semantic contract requires the first section and submitted text to describe the same change and practical effect.
For Strategic rationale, it also requires preservation of the Strategist's claims and evidence.
`evals/tests/test_assessment.py` checks section selection, required sections, and refusal of two first sections.
The evaluator and runtime tests check propagation into assessment and the Judge prompt.
`evals/README.md` and `evals/SPEC.md` state the evaluation requirements.
These checks define framework behavior; they do not establish a live packet's quality.
See [packet content](../workflows/task-pipeline.md#packet-content-and-project-instructions) for production requirements.

## Product case capture

`mc/evalcase` owns the product capture and assessment format.
The development configuration authorizes capture of a Packager's effective input, source bundle, artifacts, and packet.
The resident preserves the original input before repository cleanup and retries incomplete captures after restart.
Cases reside under `MC_HOME/evals/cases/` with owner-only access.

Original input remains separate from later observations and assessment revisions.
Go verifies identities and hashes before an atomic rename publishes an assessment and its receipt.
Repeated request identifiers return the original receipt.
A changed assessment requires a new request identifier and an explicit prior revision.
A complete capture proves preserved inputs, not the truth of every behavior claim in a packet.
`mc/evalcase/store_test.go` checks reconstruction, tamper rejection, and retention boundaries.

Related: [command duration logs](../concepts/logging.md), [Git delivery](../workflows/git-delivery.md), and [installation](../operations/install-and-deployment.md).

Native OpenCode evaluation capture preserves a masked SQLite artifact as `execution/trace.db` with its SQLite media type.
`mc/cmd/mc/evaluation.go` selects the artifact by native trace format.
This preserves record identity for later assessment without changing the original session store.
