---
type: operations
title: Image delivery and update cleanup
description: The page explains image contents, runtime volume updates, and cleanup after a published update.
tags: [release, images, runtime, deployment]
verified:
  - by: openwiki/0.7.0
    at: 2026-11-05T09:00:19.897Z
sources:
  - id: openwiki-source-b3bb40a3cb242690b078558d
    resource: repo://mc/cmd/mc/image_removal_prod.go
  - id: openwiki-source-49772750db335e70b60de417
    resource: repo://mc/deployment/image_removal.go
  - id: openwiki-source-f1bc6f316274cdca239588ea
    resource: repo://mc/deployment/runtime_install.js
  - id: openwiki-source-f9c7b390acaec04c56009b24
    resource: repo://release/image_contents.py
  - id: openwiki-source-9aeaaeae231c075c8e37ece2
    resource: repo://release/size_report.py
  - id: openwiki-source-471e78eafbc0a8c07a4a7149
    resource: repo://runner/image/runtime.Dockerfile
generated: { by: "culler-scribe", at: "2026-11-05T09:00:19.897Z" }
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

## Cleanup after a published update

After activation, a published update removes the previous product image and default Project images that no Project selects as an image or base. The setup reads selected digests through the helper, because the spine owns that state. `RemoveSupersededImages` removes image references instead of image IDs. It keeps a needed image, an image without a registry digest, or an image that a container uses. The update operation record lists removed images and kept images with their reasons. A source install does not remove images.

The image content tests and runtime install tests check these boundaries. See [checks and test lanes](../tests/checks-and-test-lanes.md) for the release gate.
