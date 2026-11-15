# Packager pilot

The pilot evaluates the unchanged source directive before automatic evolution.
The directive includes the complete shared `writing` instruction body.
The pilot supplies exploratory scores from an uncalibrated Judge.
It establishes no directive improvement claim.

## Fixed pilot configuration

The [pilot configuration](config/packager.pilot.json) fixes these conditions:

- The Packager uses GPT-6.1 Sol through Codex.
- The Judge uses Claude Opus 5.5 through Claude Code.
- Each case receives three independent trials.
- Each trial starts from fresh files, command state, and a harness session.
- The six approved dimensions receive equal provisional weights.
- The source revision and container image remain fixed.
- External attempts and Judge assessment attempts each have a limit of two.

The trial count produces 15 packets across five cases.
A failed provider or assessment attempt remains in the record.
A replacement attempt supplies no additional independent case.
A Packager failure retains its zero contribution.

| Case | Explanation type |
| --- | --- |
| Link cover retries | State transition or recovery |
| Server log level | Configuration or compatibility |
| Archived memo access | Data or access |
| Hostname label iteration | Internal refactor |
| Request-scoped settings | Performance change |

The source cache retrieves exact upstream Memos commits.
The case manifests fix source trees, reports, reference facts, and evidence limits.
These cases require no application instance or captured interface media.
The two interface cases remain outside this pilot until their supplied evidence exists.

## Pilot evidence

The pilot must preserve the actual case manifests and their content hashes.
Each trial must retain these artifacts:

- The packet and submitted summary
- The command record and harness trace
- The Judge's scores and feedback
- The browser screenshots and interactions
- The contract result and evidence limits
- The actual model, harness, and image metadata

The report must distinguish raw quality from the contribution after contract and critical failure checks.
The report must show differences between repeated trials.
The pilot must identify incomplete required assessments without a fabricated score.

## Judge calibration references

A compact reference batch follows the pilot.
It uses three case groups for calibration and two separate case groups for the independent check.
Each group supplies two reference packets.
The first submitted packet in each group supplies the reference source.
This selection uses chronological order rather than the Judge's score.
A review must verify each reference packet's claims against the fixed facts.
An accurate variant can correct a source packet that fails the factual review.
Reference packets can include controlled explanation defects or material errors.
A private record must identify each modification and its source packet.
The Judge must assess each modified packet through a fresh browser assessment.

The human review must exclude these private details:

- The Judge's scores and feedback
- The variant labels and expected errors
- The original trial's quality score

The human review must include the case facts, evidence limits, rubric anchors, and blank assessments.
The human must assess the same six dimensions and identify critical failures.
The framework must preserve human assessments independently from Judge assessments.
A later comparison must expose agreements and disagreements by dimension and critical failure.

All pilot and reference groups must remain outside the final holdout set.
Human calibration does not establish the full evolution parameters or complete case corpus.
