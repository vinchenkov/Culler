---
type: operations
title: Release security checks
description: Source, file, and image scans protect release publication and retain evidence for later assessment.
tags: [release, security, operations]
sources:
  - id: openwiki-source-7d136c52cf93e1c2d1a25295
    resource: repo://.github/release-ruleset.json
  - id: openwiki-source-4d1d392666be6dfdd7a91a2e
    resource: repo://.github/workflows/release.yml
  - id: openwiki-source-b043a0a63e8c71685ecb1b3d
    resource: repo://.github/workflows/security-nightly.yml
  - id: openwiki-source-7c4f80bb47c6f9d6e14cc99f
    resource: repo://dashboard/src/security-gate.test.ts
  - id: openwiki-source-6926a7f59d9d7d096748e09e
    resource: repo://release/publish.py
  - id: openwiki-source-f75eda6c3cdd48976dd36eeb
    resource: repo://release/security_acceptance.py
  - id: openwiki-source-ed3c203997a592d233446bb2
    resource: repo://release/security_content.py
  - id: openwiki-source-f86c7b88ebfec000979ab650
    resource: repo://release/security_images.py
  - id: openwiki-source-dca64798ff3720638b214064
    resource: repo://release/security_nightly.py
  - id: openwiki-source-fd8085e3062585796475b9ad
    resource: repo://release/security_policy.py
  - id: openwiki-source-90417f84d70bf5b2027a1f52
    resource: repo://release/security_scan.py
  - id: openwiki-source-fb3816c1ae7c7356519a203a
    resource: repo://release/security_tools.py
  - id: openwiki-source-985fc8702c7fef3be1d90e4d
    resource: repo://release/security/README.md
  - id: openwiki-source-24be247f1b8c8859a8117333
    resource: repo://runner/image/project-manifest.sh
  - id: openwiki-source-d334a6b92b5bd753eb43c852
    resource: repo://runner/image/runtime-manifest.sh
generated: { by: "culler-scribe", at: "2026-11-05T09:00:19.897Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-05T10:01:23.917Z
---

# Release security checks

The release workflow owns security checks before publication.
Python tools under `release/` scan source history, release files, and both release images.
The workflow keeps scan records beside the exact inputs that later jobs upload.
See [Installation, deployment, and release](install-and-deployment.md) for the full release lifecycle.

## Checks and publication

`.github/workflows/release.yml` runs on pull requests and pushes to `main`.
Its default permission is `contents: read`.
Pull request jobs receive no provider credentials or publication credentials.
Third-party actions use full commit identifiers.

| Job | Input and result |
| --- | --- |
| `source-secrets` | Checks the full fetched history and selected refs. The fast suite requires its success. |
| `fast-suite-macos` | Checks components, builds the application, and scans the archive and installer files before it preserves them. |
| `release-images` | Builds and checks both images with read-only permissions. It saves and scans each archive once. |
| `security-acceptance` | Exercises the fixed scanners with controlled secret and advisory fixtures. |
| `release-security` | Requires source, acceptance, fast-suite, deployment, browser, and image jobs to succeed. |
| `product-image` | Verifies and loads the scanned image archives, then uploads them to the registry. |
| `release-downloads` | Verifies scanned files and checks the final release files before publication. |

`release-security` runs even after a prerequisite fails.
A failed, cancelled, or skipped prerequisite fails that job.
Both publication jobs require its success and run only on a push to `main`.

`security_images.py` checks the archive checksum, source commit, scan status, scan mode, and current exceptions.
After Docker loads the archive, it compares the image identity with the scan record.
The upload job performs no image build.
This sequence keeps the uploaded image equal to the checked image.

`publish.py` first verifies the application and installer checksums against `security-files.json`.
It then creates `release.json` locally and scans all release files into `security-publication.json`.
Each upload checks its file checksum again and verifies the downloaded bytes.
A file change after the scan prevents upload.

## Scanner inputs and evidence

`security_tools.py` installs Gitleaks and Trivy from the versions and archive checksums in `release/security/tools.json`.
It verifies each download and removes broad path exclusions from the fixed Gitleaks configuration.
`security_scan.py` checks scanner versions and records executable checksums.
Gitleaks reports also record the configuration checksum.

The history command rejects shallow repositories and Git grafts.
It disables Git replacement objects.
An explicit local invocation names every intended public branch and tag with `--ref`.
The workflow uses `--public-refs`, which includes fetched origin branches, tags, and `HEAD`.
The scan covers deleted files, merge changes, commit messages, and binary objects.
Each changed ref requires another check before publication.
The [source publication procedure](../../release/security/README.md#source-publication) supplies the commands for the coding harness.

`security_content.py` expands tar and ZIP archives and gzip, bzip2, and xz streams.
It presents content under neutral names and maps results back to original locations.
Binary text retains line positions after invalid bytes and zero bytes receive replacements.
The archive depth limit is 16, and the expanded content limit is 80 gibibytes per scan group.
The history scan limits each Git object to 512 mebibytes.
Unreadable archives and exceeded limits leave the check unverified.

Image scans verify Linux arm64, the source revision label, the image configuration identity, and every retained layer checksum.
Gitleaks checks image metadata and every retained layer, including content that a later layer deletes.
Trivy checks vulnerabilities, file secrets, and image configuration secrets.
The checks ignore repository ignore files and inline Gitleaks allow comments.
The separate [image content checks](image-delivery.md) verify file layout, access, and layer order before publication.

Reports retain source locations, rule identifiers, scanner identities, and exact input checksums.
They omit credential values, source excerpts, raw scanner output, and image environment values.
History reports retain selected refs and commit identifiers.
Image reports retain package inventories and bundled tool version records.

## Advisory coverage and exceptions

Each image scan uses a fresh Trivy advisory cache.
The scan rejects advisory data older than two days or more than five minutes in the future.
The report records database metadata, its checksum, and the retrieval time.
Missing inventory, unsupported operating system coverage, absent required version records, or scanner failure leaves the result unverified.
An unverified required check prevents publication.

`runner/image/project-manifest.sh` records Project tools and browser versions.
`runner/image/runtime-manifest.sh` records private Node.js, Bun, and browser versions with runtime file hashes.
A version record supports assessment; it does not prove that Trivy covers that tool's advisories.
A successful scan covers known advisories for dependencies that Trivy identifies.
Custom Project images remain outside this release workflow.

`security_policy.py` applies the exact exceptions in `release/security/exceptions.json`.
All severities remain visible.
Unresolved secrets and high or critical vulnerabilities fail the scan.

| Exception | Exact match fields | Required justification |
| --- | --- | --- |
| Secret | Scanner, subject, file, rule, line | Reason |
| Vulnerability | Image, target, package, installed version, advisory | Reason and expiry date |

A vulnerability exception remains valid through its expiry date in Coordinated Universal Time (UTC).
An expired or unrelated exception leaves the result unsuppressed.
Reports retain each result and its matching exceptions, including suppressed results.
Exceptions contain no credential values.

## Repository enforcement and nightly checks

`.github/release-ruleset.json` requires `release-security` from GitHub Actions and the current base commit.
It declares no bypass actors.
The repository owner imports the ruleset through **Settings → Rules → Rulesets**.
The committed file alone does not establish active GitHub enforcement.
The [security procedure](../../release/security/README.md#required-checks-and-exact-inputs) explains access and plan limits.

`.github/workflows/security-nightly.yml` runs nightly and also supports a manual run.
`security_nightly.py` reads the current release record and retrieves both immutable registry image references.
It checks them with fresh advisory data and records their registry digests.
An unavailable release record or image produces an unverified report.
The workflow retains reports for 90 days, including failed and unverified results.
This check can detect a new advisory without a source change.

## Verification boundaries

`release/security_test.py` checks exact exceptions, expiry, retained lower severities, masked reports, archive paths, and changed input refusal.
`release/publish_test.py` checks publication refusal when scans fail or input files change.
`dashboard/src/security-gate.test.ts` checks workflow dependencies, read-only pull request permissions, exact image reuse, and nightly report retention.

`release/security_acceptance.py` uses the real fixed scanners with synthetic credentials in history, nested archives, deleted layers, and image metadata.
Its Go fixture creates two advisory databases for one unchanged image.
The second database adds a high-severity advisory, and Trivy identifies it in that same image.
These fixtures prove scanner behavior; they do not establish a clean production release.
See [Checks and test lanes](../tests/checks-and-test-lanes.md) for local checks and acceptance requirements.
