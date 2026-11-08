# OpenCode for source installations

## Purpose

Culler source deployments prepare OpenCode before agent use, like Codex and the Claude Agent Software Development Kit (SDK).
A source deployment installs the required OpenCode package when that package is absent or differs.
A source deployment reuses a complete package when its inputs match.
Published installations retain their optional OpenCode download.

## Scope

A harness is the program or library that runs an agent.
This specification covers source deployments that use real agent harnesses.
The source installer at `scripts/install-source.sh` and the source redeploy at `redeploy.sh` must satisfy the same package requirements.
The development tier with a fake harness retains its current behavior.
Codex and the Claude Agent SDK retain their current installation behavior.

The change belongs to source preparation and its required calls from shared deployment code.
The published installation behavior remains unchanged.
The source installation guide must describe the distinction between source preparation and the published download.

## Package contents and declarations

The package targets Linux on the 64-bit Arm processor architecture (arm64).
The OpenCode package contains these components:

- The package contains the Linux arm64 OpenCode program.
- The package contains the web search plugin and all other dependencies selected by its lock file.
- The package contains Culler's OpenCode plugin and shell launcher.

A checksum is a value computed from file contents.
`runner/opencode/package.json` declares the package versions.
`runner/opencode/package-lock.json` records exact dependency versions and archive checksums.
`runner/opencode/control/` supplies Culler's OpenCode plugin and shell launcher.

The package must pass the current release checks for its declared versions.
This change preserves the current version declarations and catalog checks.
The installer must reject package inputs that differ from the source revision selected for deployment.

The package remains in the separate OpenCode Docker volume for the deployment.
Agent containers mount that volume at `/opt/culler-opencode`.
Agent containers can read and execute its files but cannot change them.
Only the verified volume for this deployment satisfies the package requirement.

## Package identity and reuse

The package identity describes the inputs that determine the installed package.
The Culler release identity identifies the deployed Culler commit.
These identities remain separate.

The installation record must identify these package inputs:

- The record identifies the contents of the package declarations.
- The record identifies the contents of the lock file.
- The record identifies Culler's OpenCode plugin and shell launcher.
- The record identifies the archive preparation rules that affect package contents.
- The record identifies Linux and the arm64 processor architecture.

The record must also identify any other preparation input that can change the installed files.
The source deployment must verify that the installed package matches its record and the required inputs.
The installed package must pass local compatibility checks with the proposed source deployment.
The native OpenCode program must report the declared version.
The OpenCode version alone is insufficient evidence for reuse.

These changes alone must preserve package reuse:

- A different Culler commit preserves reuse when the package inputs remain equal.
- A different checkout path preserves reuse when the package inputs remain equal.
- Different file timestamps preserve reuse when the package inputs remain equal.
- A different container image identity preserves reuse when the package inputs remain equal and compatibility checks pass.

Reuse requires no package download or archive preparation.
Reuse preserves the installed package files.
Release metadata must identify the current source deployment after successful reuse.
An absent, damaged, or different package requires preparation and installation.
A volume without sufficient identity evidence requires verification or replacement.

## Source preparation and readiness

A successful source deployment must leave OpenCode ready before service activation, even without an OpenCode connection.
OpenCode preparation uses package tools inside the Linux container image selected by the source deployment.
The host requires only the current prerequisites for source installation.
Container package tools provide npm independently of the host.
Preparation preserves the checkout files.
Temporary package files use disposable storage.

The source deployment uses the current OpenCode archive builder and archive format.
The archive builder remains unchanged.
The source deployment retains the current archive validation and native version checks.
Preparation uses the declared versions and locked dependencies.

Successful installation or reuse must record that OpenCode is ready.
This requirement also applies when the deployment has no earlier OpenCode download record.
Successful verification must replace any obsolete failure status.
The dashboard and terminal setup must recognize the package as ready through their current status interface.

The first OpenCode connection must use the installed package without another package download or archive preparation.
The installed package remains available after the source checkout moves or changes.
Provider access tests retain their current behavior.

## Failure and repair

An OpenCode preparation or installation failure must prevent activation of the proposed source deployment.
The error must identify the failed stage and its cause.
A failure record must distinguish a package download failure from a local preparation or validation failure.
The ready status must describe the package that the proposed source deployment requires.
An incomplete package cannot satisfy that status.

Download and archive validation failures must preserve the previous verified volume.
An interruption during replacement leaves the proposed package unready until verification succeeds.
A retry must inspect the actual package and repair an incomplete installation.
A retry must reuse a verified package that matches the required inputs.

The current rules for deployment ownership and agent shutdown continue to govern volume replacement.
Concurrent preparation requests must preserve a consistent package and status.
Both uninstall modes retain their current OpenCode cleanup behavior.

## Published installation constraints

The release workflow and release archive builder remain unchanged.
The product image retains its current package contents.
The release image checks retain their OpenCode exclusion.
The published downloader and archive installer retain their current behavior.
Source package records reside only in local deployment storage.

Published installations must retain these outcomes:

- A fresh published installation defers OpenCode until an OpenCode connection requires it.
- A published update downloads no OpenCode archive when the OpenCode volume is absent.
- A published update uses its release archive when the OpenCode volume exists.
- Published archive verification retains the current checksum and native version checks.
- Published recovery restores the OpenCode package required by the retained release.

The active published installation takes precedence over source records from an earlier source deployment.
The published updater must use its required archive even when the source package uses equal dependency versions.
A deployment that returns to published updates retains the current treatment of an installed OpenCode volume.
The current support rules still determine whether that transition is permitted.
Shared deployment changes must preserve each published outcome.

## Acceptance criteria

1. A fresh source installation with no OpenCode connection prepares a verified OpenCode volume before service activation.
2. Fresh package preparation succeeds when host npm is unavailable.
3. A repeated source deployment with equal package inputs reuses the package without a download or archive preparation.
4. An unrelated Culler commit change permits OpenCode reuse with package downloads unavailable.
5. A checkout path or timestamp change preserves reuse when the package inputs remain equal.
6. A consistent OpenCode version change installs a native program that reports the new declared version.
7. A dependency change in the lock file prevents reuse of the previous package, even when the OpenCode version stays equal.
8. A change to Culler's OpenCode plugin or shell launcher prevents reuse of the previous package.
9. A change to an archive preparation input that affects package contents prevents reuse of the previous package.
10. An absent or damaged package prevents a ready result, even when an earlier record reports success.
11. A volume without sufficient identity evidence receives verification or replacement before the source deployment reports success.
12. Successful verification clears an obsolete failure status without unnecessary package preparation.
13. The first OpenCode connection uses the installed package after the checkout moves, with host npm unavailable.
14. A failed preparation prevents activation and reports the actual cause. A successful retry establishes the correct ready status.
15. A volume from another deployment remains unchanged when the source installation refuses it.
16. Published installation tests prove deferred download and preserve the current download, update, and recovery outcomes.
17. A published install or update with a retained source record keeps the published behavior.
18. The fake-harness development tier retains its current behavior.

An isolated Docker check must exercise real package preparation and installation from the declared inputs.
That check must verify the native version and the required package files.
The check uses disposable deployment storage and preserves the live deployment.
Package tests require no provider credentials or provider requests.

Tests of shared deployment changes must cover both source and published installations.
Required repository checks remain applicable to the implementation.
Any terminal display change requires the adversarial review defined by `AGENTS.md`.
