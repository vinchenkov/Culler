# Evaluation cases

The host captures an evaluation case when the canonical development flag authorizes a Packager run.
The resident captures the effective envelope after it adds Project skills.
The capture holds the original artifact inventory and a self-contained Git bundle before repository cleanup.
The resident retries incomplete captures after a restart.
A cleanup retry for an incomplete source capture preserves the task's dispatch allowance.

The host stores cases beneath `MC_HOME/evals/cases/` with owner-only access.
Each manifest uses format version 1.
Each artifact records its origin, capture time, media type, size, and Secure Hash Algorithm 256-bit (SHA-256) hash.
The case group combines the deployment identity, Project identifier, and task identifier.
A complete capture proves that the preserved inputs exist; it does not establish authoritative behavior facts.

## Original input and later records

The case separates its records into these directories:

- `source/` contains the original source bundle, commit and tree identities, and binary-capable reviewed diff.
- `input/` contains the original effective input, access policy, runtime, records, and artifact inventory.
- `evidence/` contains the original authorized artifact copies.
- `packet/` contains the submitted report, report resources, and terminal records.
- `execution/` contains the original execution trace when available.
- `observations/` contains later source comparisons that the operator displays.
- `assessments/` contains immutable assessment revisions and their receipts.

A later source comparison preserves its own candidate and destination objects.
An assessment revision includes that comparison through its manifest.
The original manifest and Packager input remain unchanged.
An absent optional record carries its absence reason.
A required artifact failure prevents a successful score submission.

## Submission and retention

Go verifies the packet identity and artifact hashes before it writes an assessment revision.
A revision records the exact rubric and displayed report wrapper.
An atomic directory rename publishes the revision and its receipt together.
A repeated request identifier returns the original receipt.
A changed assessment requires a new request identifier and an explicit prior revision.

The authenticated dashboard mirrors the host commands.
The canonical flag controls new submissions, including submissions from an old dialog.
A flag change preserves saved cases and permits an authorized capture to finish.
Saved cases remain readable after a database reset through their manifests.
Unscored captures follow the task artifact retention period.
Full removal follows the existing uninstall policy.

This schema change requires a fresh development database at deployment.
The schema identity derives from the current schema files.
A deployment reset preserves `evals/` with the other operator data.

## Experiment integration

`Store.Import` validates archive paths, listed artifact hashes, assessment manifests, and original source identities.
The importer preserves the original records and receipts.
`Store.Trial` accepts an exact case identifier, assessment revision, trial role, destination, and identity substitutions.
The trial role is `packager` or `judge`.
Each trial requires a synthetic run identifier and an isolated source path.

A trial keeps the original text separate from the substituted text.
The substitution record describes each replacement.
A Packager trial receives original source, input, and evidence.
A Judge trial also receives the report, displayed wrapper, and rubric snapshot.
Human assessments, execution traces, and later observations remain outside trial inputs.
The experiment record identifies the exact assessment revision outside those inputs.

The report wrapper retains the dashboard sandbox policy.
Supported report media remains embedded in the wrapper.
The archive also preserves authorized local report resources under `packet/resources/`.
A separate reference review must check behavior claims against the preserved source and recorded evidence.
Dataset assignment and calibration remain separate experiment decisions.
