# Exploratory Packager pilot results

The pilot completed fifteen Packager trials across five fixed Memos cases.
It evaluated one unchanged directive, with the shared `writing` instruction body merged into that directive.
It performed no directive evolution or final comparison.
The Judge remains uncalibrated.

The pilot also exposed a Judge contract defect.
The risk check demanded risks and rollback conditions that the durable records did not establish.
The Packager directive prohibits a new risk assessment.
The framework corrects this check and requires fresh calibration assessments.
The original recorded scores remain intact.

## Recorded results

Raw quality averages the six dimension scores with equal provisional weights.
A contract failure or critical failure sets the trial's contribution to zero.
All values below use a zero-to-100 scale.
These values describe the original assessments, including the contract defect.

| Case | Raw quality across three trials | Recorded mean contribution |
| --- | --- | ---: |
| Link cover retries | 87.5 · 75.0 · 87.5 | 83.3 |
| Server log level | 87.5 · 100.0 · 95.8 | 94.4 |
| Archived memo access | 83.3 · 91.7 · 79.2 | 58.3 |
| Hostname label iteration | 79.2 · 87.5 · 83.3 | 83.3 |
| Request-scoped settings | 70.8 · 75.0 · 75.0 | 0.0 |

The raw mean is 83.9.
The recorded mean contribution is 63.9.
These means establish no validated directive score or improvement.

All fifteen packets pass the deterministic packet checks.
Four trials fail at least one semantic check.
Three trials contain a recorded critical failure.
All three critical failures concern omitted material conditions in the performance case.

The omitted conditions concern empty results and concurrent settings updates.
The packet must represent the relevant behavioral boundary rather than only describe the removed lookup.
A fresh human reference review will assess the Judge's interpretation of these omissions.

The invalid risk requirement alone causes one archived-access trial's zero contribution.
It also affects feedback on other packets.
The framework preserves these observations rather than revises old scores after the fact.

## Runtime and evidence

| Entity | Actual model | Harness |
| --- | --- | --- |
| Packager | `gpt-6.1-sol` | Codex 0.159.2 |
| Judge | `claude-opus-5-5` | Claude Code 2.1.280 |

The Judge uses Chromium 145.0.7632.6 at 1280 × 900 pixels.
The evaluation image changes only the Codex packages in the selected runner image.
The production image and deployment remain unchanged.

The [aggregate record](PILOT-RESULTS.json) contains the exact experiment identity, input hashes, runtime identity, packet hashes, and assessment hashes.
It also contains variation, dimension means, elapsed time, recorded token totals, and record integrity checks.
It excludes credentials, raw traces, and private calibration labels.

The private experiment directory retains these actual artifacts:

- Packets and submitted summaries
- Command records and model traces
- Judge scores, reasons, and feedback
- Browser screenshots and interactions
- Content-addressed artifacts and chronological events

The pilot's original assessments lack complete context bindings.
They cannot supply the corrected calibration assessments.
The framework now binds the actual Judge instructions, packet contracts, wrapper bytes, viewport, rubric, and operator context.

## Excluded attempts

| Attempt | Reason | Use as packet quality evidence |
| --- | --- | --- |
| 01 | The container omitted the prompt from standard input. | None. |
| 02 | The older Codex version rejected the requested model. | None. |
| 03 | Codex removed the command token from shell commands. | Invalidated. |

Attempt 04 completes all fifteen planned trials after the runtime corrections.
The earlier attempts remain available in separate records.
They contribute no fabricated quality score to this pilot.

## Remaining work

The ten reviewed reference packets require independent human scores.
The Judge completed ten fresh reference assessments under the corrected risk contract.
The [current calibration status](CALIBRATION-STATUS.md) identifies the actual human page.
Their comparison will expose agreement, disagreement, and critical failure detection.
Calibration acceptance thresholds remain unset.

The five cases cover five explanation types from one repository.
The two interface cases still require supplied media.
The full experiment requires more independent case groups and fixed dataset assignments.
The evolution parameters also remain unresolved.

The case reports contain authored evidence from pinned source.
The pilot executes no Memos application tests, deployment checks, or performance benchmarks.
The Packager receives those evidence limits explicitly.
The pilot reproduces the Packager environment without the complete Culler pipeline.
