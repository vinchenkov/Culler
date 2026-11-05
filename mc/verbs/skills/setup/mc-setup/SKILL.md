---
name: mc-setup
description: Use this command surface for Project setup and environment preparation.
---

# Project setup commands

The agent profile sets the model and effort for this run.

Each command accepts one JavaScript Object Notation (JSON) object on standard input.
The immutable run identity supplies your authority.
Use the operation identifier from your brief.
Each attempt has a 30-minute limit.
A heartbeat does not extend this limit.
Culler permits two automatic attempts for each operation.

- `mc environment get`
- `mc environment heartbeat`
- `mc environment report`

The get request contains `project_id`.
The heartbeat request contains `operation_id`.
The report request contains these fields:

- `operation_id`: the assigned operation.
- `outcome`: `proposal` or `complete`.
- `summary`: one paragraph with at most two sentences and 25 words per sentence.
- `details`: optional Markdown prose with useful command observations and limits.

The summary states the preparation result or proposed image change.
Include a material limit when it affects the result.
Use the details for short paragraphs and lists.
The summary permits at most 1024 bytes.
The details permit at most 24000 bytes.
Full command output stays in the trace.
Keep secret values outside every report field.
The report remains available independently of the trace.
A repeated identical report preserves one result.
An accepted report survives a later process failure.

## Proposal

Include the exact `configuration`.
The configuration follows the object in your brief.
Initial setup permits one image decision.
An initial proposal must change the image inputs.
The initial continuation requires `complete` after approval or denial.
Agent assistance after import produces a proposal for operator approval.

The proposal explains the expected development benefit and the evidence that the image can enable it.
Its details identify affected commands, feasibility evidence, costs, assumptions, and unresolved prerequisites.
Expected capabilities remain unverified until the approved image executes the affected commands.

A proposal includes these image inputs:

- `base_digest`: the exact selected base.
- `dockerfile`: its relative path and exact contents.
- `build_files`: each required relative path and exact contents.
- `build_args`: nonsecret build arguments.

Use the brief's `base_reference` as the Dockerfile's sole FROM image.
Select exact additional versions and integrity digests for downloads.
During initial setup, keep proposed build contents in the configuration until approval.
Preserve repository files independently of the proposal.
Culler materializes the approved contents before the continuation.

The report retains the proposal and ends this operation.
Approval permits Culler to build the exact image inputs.
Denial starts a continuation in the image that ran this operation.
Further image improvements use the Environment page after initial import.

## Complete

Include `files_digest`, the Secure Hash Algorithm 256 (SHA-256) digest of the Project files, with the `sha256:` prefix.
Include `configuration` when dependency preparation changes the retained configuration.
The image inputs must match the assigned configuration.
During initial setup, other configuration changes, such as the setup script, require no operator approval.
The setup script must prepare a fresh workspace and permit repeated execution against the persistent Project home.
The home retains normal dependency caches and tools across Setup Agent runs and pipeline runs.
Record required explicit cache paths in the retained configuration.
Validation after initial setup preserves the entire approved configuration.
Include `source_commit` for validation after initial setup.
Use the full assigned source commit.

These optional fields retain observations:

- `tool_versions`: tool names and observed versions.
- `package_records`: applicable declarations and resolved package records.
- `details`: actual commands, results, and unresolved limits as Markdown prose.

Before initial completion, assess every missing prerequisite through the setup checklist.
Explain each deferred Image dependency through its benefit, feasibility, unresolved prerequisites, or the assignment's authority.
A complete report permits source acceptance even when every build or test remains unavailable.
An unexecuted command remains unverified.
A failed command retains its actual result.
Successful import proves source preservation and preparation completion.
Repository test requirements remain authoritative during later task verification.

## File digest

Keep installed dependencies, secrets, and disposable outputs in Git exclusions.
Execute this command after preparation:

```sh
/opt/culler-runtime/bin/bun /opt/culler-runtime/app/src/shared/project-files.ts digest /workspace/source
```

Use its exact output as `files_digest`.
Preserve the files after this command.
Culler confirms their identity and preserves the development commit before pipeline work.
