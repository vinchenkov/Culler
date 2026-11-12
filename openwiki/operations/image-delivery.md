---
type: operations
title: Image delivery and update cleanup
description: The page explains image contents, runtime volume updates, source OpenCode package reuse, and cleanup after a published update.
tags: [release, images, runtime, deployment]
sources:
  - id: openwiki-source-4d1d392666be6dfdd7a91a2e
    resource: repo://.github/workflows/release.yml
  - id: openwiki-source-096a781fb160ef979fa31121
    resource: repo://INSTALL.md
  - id: openwiki-source-8887a56335011659b61b947b
    resource: repo://mc/cmd/mc/candidate-runtime-check.sh
  - id: openwiki-source-b3bb40a3cb242690b078558d
    resource: repo://mc/cmd/mc/image_removal_prod.go
  - id: openwiki-source-579b9c04dbd98eaf836c5c4c
    resource: repo://mc/cmd/mc/setup_update_candidate_prod.go
  - id: openwiki-source-49772750db335e70b60de417
    resource: repo://mc/deployment/image_removal.go
  - id: openwiki-source-c3574bc58db62fef903f9230
    resource: repo://mc/deployment/opencode_source.go
  - id: openwiki-source-337b7a6a3a1693697c3f6ffd
    resource: repo://mc/deployment/opencode_source.py
  - id: openwiki-source-0be9f5eae2f9bcb36e0a31d1
    resource: repo://mc/deployment/opencode.go
  - id: openwiki-source-f1bc6f316274cdca239588ea
    resource: repo://mc/deployment/runtime_install.js
  - id: openwiki-source-f9c7b390acaec04c56009b24
    resource: repo://release/image_contents.py
  - id: openwiki-source-9aeaaeae231c075c8e37ece2
    resource: repo://release/size_report.py
  - id: openwiki-source-09c5383289d9635a85a372a3
    resource: repo://release/UPDATES.md
  - id: openwiki-source-cace99190acf5608e60583a3
    resource: repo://runner/image/Dockerfile
  - id: openwiki-source-729fed85a7f093179cea3802
    resource: repo://runner/image/project-smoke.sh
  - id: openwiki-source-1430dd49737c0f35b616bb76
    resource: repo://runner/image/runtime-check.sh
  - id: openwiki-source-471e78eafbc0a8c07a4a7149
    resource: repo://runner/image/runtime.Dockerfile
generated: { by: "culler-scribe", at: "2026-11-12T17:31:21.412Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-12T17:31:21.412Z
---

# Image delivery and update cleanup

Culler publishes a default Project image and a product image. The Project image supplies tools for repository work. The product image adds Culler's private runtime and uses the Project image as its base. See [runner containers](../architecture/runners.md) for the execution boundary and [installation](install-and-deployment.md) for the release front door.

## Image contents

`runner/image/Dockerfile` builds the default Project image from pinned tools. It places each tool in a separate build step and clears caches in that step. `runner/image/runtime.Dockerfile` builds the product image in stages. Its private runtime holds Node.js, a browser, OpenWiki, Bun, harness packages, source files, and protected commands. The files that change with each release form the last runtime layers.

`release/image_contents.py` reads the saved archives of both images. It checks file presence, layer order, repeated tool files, build caches, executable access, and debug sections. It also checks the protected `mc-real` owner and mode. The default Project image must have no setuid or setgid file. The product image keeps `mc-real` with owner `10001:10001` and mode `6755`.

`release/size_report.py` writes the fresh install and update size report from those archives. It measures compressed layer downloads and installed disk use. An update can compare the current archives with previous archives and count only changed layers. The runtime volume count treats hard-linked files as one file.

## Runtime volume update

`mc/deployment/InstallRuntimeVolume` checks the release marker and refuses a replacement while agent containers use the runtime volume. The installer runs the embedded `runtime_install.js` in a container of the product image. The script compares the image manifest with the volume manifest. It keeps matching files, writes changed files through temporary paths, and removes obsolete entries. It clears the release marker before the walk and writes it after the files and manifest. An interrupted install has no release marker, so a later install repairs the volume.

The script writes changed files as new files before rename. This keeps other paths safe when volume files share hard links. It applies owners before modes, then verifies them. It syncs changed files and directories before it writes the release marker.

## Candidate runtime compatibility

Before a published update replaces software, it checks the candidate runtime on each distinct selected Project image, including archived Projects.
`mc/cmd/mc/setup_update_candidate_prod.go` requires each selected image and base image to exist locally.
It copies the candidate runtime into a temporary volume and checks the selected images against that copy.
The check containers receive no live spine, Project files, or host credentials.
The installed runtime volume remains separate.

Each check runs as `10002:10002` with these restrictions:

- Network access is disabled, and all capabilities are dropped.
- The root filesystem and runtime mount are read-only; `/tmp` uses disposable memory storage.
- `no-new-privileges` blocks privilege gains. Each check has a limit of 2,048 processes and sets no CPU or memory limit.
- Each image check has a two-minute timeout.

`mc/cmd/mc/candidate-runtime-check.sh` is the shared shell program embedded in the updater.
It requires real directories at `/mc` and `/workspace`, an absent `/mc/run.json`, a successful runtime check, and the expected release identity.
The default Project image supplies `/mc/spine` with owner `10001:10001` and mode `0700`.
That protected directory is compatible with the update check.
`runner/image/runtime-check.sh` requires the directory to remain unreadable by the check user.
The check therefore preserves the access boundary while permitting the default Project image.

When an installed OpenCode volume requires an update, the check uses a separate temporary copy of the candidate package.
The shell program then checks its native program version with disposable data and configuration paths.
A failed image check stops the update before replacement and identifies the Project whose selected image requires repair.
Temporary containers and volumes receive cleanup after the checks.
Ownership checks protect resources from another deployment.

`runner/image/project-smoke.sh` runs the same shared shell program against the default Project image with the candidate runtime mounted read-only.
It supplies the runtime image's release identity and applies the update check's container restrictions.
The `release-images` job in `.github/workflows/release.yml` runs this smoke before it saves the checked images.
This shared program keeps the release check aligned with the updater's default-image compatibility check.
See [update recovery checks](../tests/checks-and-test-lanes.md#opencode-and-update-recovery-checks) for the unit assertions and execution limits.

## Cleanup after a published update

After commit, a published update removes the previous product image and default Project images that no Project selects as an image or base.
The setup reads selected digests through the helper, because the spine owns that state.
`RemoveSupersededImages` removes image references instead of image IDs.
It keeps a needed image, an image without a registry digest, or an image that a container uses.
The update operation record lists removed images and kept images with their reasons.
A cleanup failure records a retry and leaves the committed release available.
A source install does not remove images.

## Separate OpenCode runtime

The release also publishes `culler-opencode-linux-arm64.tar.gz` for installations that use OpenCode connections.
`mc/deployment/opencode.go` verifies its checksum, archive paths, required files, platform, and program version.
It installs the archive into a deployment-specific Docker volume at `/opt/culler-opencode`.
It refuses replacement while a container uses that volume.
The runner mounts the volume read-only for OpenCode runs.

Published installations defer this package until an OpenCode connection requires it.
Source deployments with real harnesses prepare the package before service activation, including deployments without an OpenCode connection.
The package remains separate from the product image and the ordinary runtime volume.

### Source package preparation and reuse

`mc/deployment/opencode_source.go` and `INSTALL.md` define source preparation and reuse for `scripts/install-source.sh` and `redeploy.sh` with real harnesses.
The development tier with a fake harness retains its separate behavior.

`mc/cmd/mc/onboard_home_prod.go` records the source and calls `UpdateOpenCodeVolume` before activation.
`mc/deployment/opencode_source.go` owns source preparation and serializes it through the OpenCode operation worker lock.
It rejects a checkout whose commit or OpenCode files differ from the selected release.
It exports committed files from `runner/opencode/` and `release/opencode/` into a temporary snapshot.
The preparation container receives that snapshot read-only, without the repository or host credentials.

The selected source image supplies the package tools for Linux arm64.
The host requires no npm installation.
Preparation resolves the image to its immutable identity and uses user `10002:10002`, dropped capabilities, and `no-new-privileges`.
The container receives a fixed environment and disposable npm configuration and cache paths.
`release/opencode/build.sh` uses `npm ci` with the lock file, copies the control files, and validates the folder and archive.
The Go installer then validates the archive before it replaces the volume.

`MC_HOME/state/opencode-source-package.json` separates the package identity from the Culler release commit.
The package identity includes these inputs:

| Input | Reason |
| --- | --- |
| Committed file contents and modes under `runner/opencode/` and `release/opencode/` | Covers declarations, locked dependencies, control files, and archive rules. |
| Node.js and npm versions and content digests | Detects package tool changes even when the image name stays equal. |
| The embedded preparation program, install script, and fixed environment | Detects changes to preparation and installation rules. |
| Linux arm64 and the declared OpenCode version | Binds the package to its platform and native version. |

The record also stores the archive checksum and installed content digest.
The release commit, checkout path, file timestamps, and image identity alone do not invalidate equal package inputs.
A different image still must pass the tool identity and package compatibility checks.

Reuse requires a volume with the deployment's labels, a matching input identity, and complete checksum records.
`mc/deployment/opencode_source.py` compares the installed paths, file contents, modes, and symbolic link targets with the recorded content digest.
It checks the archive and release markers separately.
It executes the native program only after the content check and requires the declared version.
These checks run without network access against a read-only volume.

Successful reuse preserves package files and requires no download or archive preparation.
When the Culler commit differs, the installer updates the release marker after it checks for active volume users.
It then saves the record for the selected source deployment.
An absent, damaged, incomplete, or unrecorded package receives preparation and installation again.

### Readiness and repair

The existing `opencode-download` operation reports source progress and readiness to terminal setup and the dashboard.
Successful preparation or verified reuse records `succeeded` and clears the error code.
The first connection uses that status without another preparation, including after the checkout moves.
See [agent connections](agent-connections.md#opencode-connections) for the model test boundary.

A failure records its stage and cause, and prevents activation of the proposed source deployment.
The stage distinguishes download, preparation, validation, and installation failures.
Download and archive validation occur before volume replacement, so those failures preserve the previous volume.
Replacement refuses a foreign volume or active volume users.
An incomplete installation remains unready; a retry verifies the actual files and repairs the package when necessary.

### Published updates and retained source records

A public update replaces an existing OpenCode volume before service activation.
The checkpoint preserves its previous contents for recovery before commit.
The previous product image reconstructs the ordinary runtime volume during restore.
See [Culler lifecycle](culler-lifecycle.md#public-updates-and-recovery) for the update sequence and checkpoint limits.

An active published update or matching published image takes precedence over a retained source record.
A source package with the same Culler commit still requires the published archive checksum before reuse on the published path.
An absent OpenCode volume preserves the deferred download and clears obsolete source readiness after the worker lock and a second volume check.

The image content tests and runtime install tests check their respective boundaries.
The source package tests cover input changes, content damage, concurrent requests, readiness repair, and published transitions.
An opt-in Docker test prepares a real package, then checks reuse without network access.
See [OpenCode checks](../tests/checks-and-test-lanes.md#opencode-and-update-recovery-checks) for test paths and execution limits.

The OpenCode update waits for the operation worker lock before volume replacement.
This serializes installation with live model refreshes that read that volume.
After a successful update, the host starts a fresh model-list refresh for the current catalog and pinned program.
