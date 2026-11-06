---
type: operations
title: Release security scans
description: Secret and vulnerability scans report results for later assessment and never prevent a push or a release.
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
  - id: openwiki-source-2aee5c75a683753fe072cd2d
    resource: repo://release/image_load.py
  - id: openwiki-source-6926a7f59d9d7d096748e09e
    resource: repo://release/publish.py
  - id: openwiki-source-f75eda6c3cdd48976dd36eeb
    resource: repo://release/security_acceptance.py
  - id: openwiki-source-ed3c203997a592d233446bb2
    resource: repo://release/security_content.py
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

# Release security scans

The release workflows use Gitleaks and Trivy to report security results.
Python tools under `release/` scan the source history of this repository and both release images.
A scan result never prevents a push or a release.
The repository owner reads the reports and decides each action.
See [Installation, deployment, and release](install-and-deployment.md) for the full release lifecycle.

## Scan jobs and reports

`.github/workflows/release.yml` runs on pull requests and pushes to `main`.
Its default permission is `contents: read`.
Pull request jobs receive no provider credentials or publication credentials.
Third-party actions use full commit identifiers.

| Job | Scan | Report |
| --- | --- | --- |
| `source-secrets` | Gitleaks checks the full fetched history and selected refs of this repository. | The job summary and the `culler-security-history-<attempt>` artifact. |
| `image-vulnerabilities` | Trivy checks the saved archives of both release images for known vulnerabilities. | The job summary and the `culler-image-vulnerabilities-<attempt>` artifact. |
| `current-release-images` | `security-nightly.yml` checks the images of the current release with fresh advisory data. | The job summary and the `culler-security-nightly-<attempt>` artifact. |
| `security-acceptance` | The acceptance tests exercise the fixed scanners with controlled secret and advisory fixtures. | The `culler-security-acceptance-<attempt>` artifact. |

No job waits for a scan job.
A secret result or a vulnerability result makes no job fail.
A scan that cannot complete writes an unverified report and exits with status 2.
Only the job of that scan fails.
The secret scan covers only the source history of this repository.
The dependencies, the release files, and the images are outside the secret scan.

`image-vulnerabilities` starts after `release-images` succeeds.
It downloads the same image archive artifact as the upload job.

## Required release checks

| Job | Input and result |
| --- | --- |
| `fast-suite-macos` | Checks components, runs `./release/check.sh`, builds the application, and preserves the release inputs. |
| `deployment-linux` | Runs the portable deployment tests and the Linux filesystem guard tests. |
| `dashboard-browser` | Runs the complete dashboard browser suite. |
| `release-images` | Builds and checks both images with read-only permissions. It saves each archive once. |
| `release-checks` | Requires the four jobs above to succeed. |
| `product-image` | Checks and loads the saved image archives, then uploads them to the registry. |
| `release-downloads` | Uploads the release files, verifies each download, and publishes the release. |

`release-checks` runs even after a prerequisite fails.
A failed, cancelled, or skipped prerequisite fails that job.
Both publication jobs require its success and run only on a push to `main`.

`image_load.py` reads the image configuration of each saved archive.
It requires the `org.opencontainers.image.revision` label to equal the source commit.
After Docker loads the archive, it compares the loaded image identity with the SHA-256 value of that configuration.
The upload job performs no image build.
This sequence keeps the uploaded image equal to the checked image.
The separate [image content checks](image-delivery.md) verify file layout, access, and layer order before publication.

`publish.py` compares the application archive with its build record before it creates `release.json`.
Each upload downloads the release file again and compares its bytes with the local file.
A difference stops the publication.

## Secret scan inputs and evidence

`security_tools.py` installs Gitleaks and Trivy from the versions and archive checksums in `release/security/tools.json`.
It verifies each download and uses the default Gitleaks configuration, including its path exclusions.
`security_scan.py` checks scanner versions and records executable checksums.
History reports also record the Gitleaks configuration checksum.

The history command rejects shallow repositories and Git grafts.
It disables Git replacement objects.
An explicit local invocation names every intended public branch and tag with `--ref`.
The workflow uses `--public-refs`, which includes fetched origin branches, tags, and `HEAD`.
The scan covers deleted files, merge changes, commit messages, and binary objects.
The [source history scan procedure](../../release/security/README.md#source-history-scan) supplies the commands for the coding harness.

`security_content.py` expands tar and ZIP archives and gzip, bzip2, and xz streams in binary Git objects.
It presents content under neutral names and maps each result back to its commit, file, and archive member.
Binary text retains line positions after invalid bytes and zero bytes receive replacements.
The archive depth limit is 16, and the expanded content limit is 80 gibibytes per scan group.
The history scan limits each Git object to 512 mebibytes.
Unreadable archives and exceeded limits leave the scan unverified.

The scan ignores repository ignore files and inline Gitleaks allow comments.
Reports retain source locations, rule identifiers, and scanner identities.
They omit credential values, source excerpts, and raw scanner output.
History reports retain selected refs and commit identifiers.

## Exceptions

`security_policy.py` matches secret results to the exceptions in `release/security/exceptions.json`.
That file holds only secret exceptions.
Each exception names the scanner, subject, file, rule, and line of one result, and it gives a reason.
The subject identifies the commit of the result.

The report and the job summary show the reason of each matched result.
An exception changes no scan status and no job result.
Exceptions contain no credential values.

## Vulnerability coverage

Each image scan verifies Linux arm64, the source revision label, the image configuration identity, and every retained layer checksum.
Trivy then checks installed operating system packages and supported bundled dependencies with its vulnerability scanner only.
Each image scan uses a fresh Trivy advisory cache.
The scan rejects advisory data older than two days or more than five minutes in the future.
The report records database metadata, its checksum, and the retrieval time.
An absent package inventory, absent required version records, unavailable or stale advisory data, or scanner failure leaves the scan unverified.

The report lists every result, every package that Trivy identifies, and each package without an identified version.
Trivy checks no advisories for a package without a version.
The report records the operating system data of Trivy, including its end of support state.
The job summary shows the number of results and the number of results with a fixed version for each severity.
It also lists each package without a version and states when the operating system is past its end of support.

`runner/image/project-manifest.sh` records Project tools and browser versions.
`runner/image/runtime-manifest.sh` records private Node.js, Bun, and browser versions with runtime file hashes.
The image report lists these version records separately from the Trivy packages.
A version record supports assessment; it does not prove that Trivy covers that tool's advisories.
A complete scan covers known advisories for dependencies that Trivy identifies.
Custom Project images remain outside this release workflow.

## Repository enforcement and nightly scans

`.github/release-ruleset.json` requires `release-checks` from GitHub Actions and the current base commit.
It declares no bypass actors.
The repository owner imports the ruleset through **Settings → Rules → Rulesets**.
The committed file alone does not establish active GitHub enforcement.
The [release checks procedure](../../release/security/README.md#required-release-checks) explains the plan limits.

`.github/workflows/security-nightly.yml` runs nightly and also supports a manual run.
`security_nightly.py` reads the current release record and retrieves both immutable registry image references.
It scans them with fresh advisory data and records their registry digests.
Without a current release, it writes a note in the job summary and succeeds.

An unavailable release record or image produces an unverified report, and the nightly job fails.
The workflow retains reports for 90 days, including unverified results.
This scan can detect a new advisory without a source change.

## Verification boundaries

`release/security_test.py` checks exact secret exceptions, the secret-only exception file, severity counts, packages without a version, masked reports, job summaries, and archive paths.
`release/image_load_test.py` checks the image identity and the refusal of an image of another commit.
`dashboard/src/security-gate.test.ts` checks the `release-checks` prerequisites, read-only pull request permissions, exact image reuse, and nightly report retention.
It also checks that no job waits for a scan job.

`release/security_acceptance.py` uses the real fixed scanners with synthetic credentials in deleted commits, binary blobs, and nested archives.
It also checks that a failed tool or an absent image layer leaves the scan unverified.
Its Go fixture creates two advisory databases for one unchanged image.
The second database adds a high-severity advisory, and the report of that same image lists it.
These fixtures prove scanner behavior; they do not establish a clean production release.
See [Checks and test lanes](../tests/checks-and-test-lanes.md) for local checks and acceptance requirements.
