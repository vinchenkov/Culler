# Release checks

`./release/check.sh` runs every `*_test.py` file in this directory with `unittest` and checks the shell scripts.
The check needs no credentials, no network access, and no Docker.
The release workflow runs the scripts of this directory on the release inputs.
[security/README.md](security/README.md) describes the vulnerability scans and the secret scanning.

## Versions and release notes

`release/version.py check` checks the `VERSION` file and `CHANGELOG.md`.
The check requires one `MAJOR.MINOR.PATCH` line in `VERSION`, for example `0.1.0`.
It requires the first `CHANGELOG.md` section to have the version in `VERSION`.
Each section must have the parts Changes, Known limits, Database compatibility, and Update effects, in that order.
The sections go from the newest version to the oldest version.
`./release/check.sh` runs this check, so the `fast-suite-macos` job checks each pull request.

`release/build.sh` puts the version into each `mc` binary through `-X main.releaseVersion`.
It also writes `version` and `release-notes.md` into the release inputs.

For a push to `main`, the `release-version` job runs `release/publish.py --decide`.
That job writes `publish=true` only for a version above the version of the latest release.
With the same version, the image upload job and the publication job do not run.
A lower version fails the `release-version` job.
`release/publish.py` compares the versions again immediately before publication.
A higher version also requires a commit after the commit of the latest release.
The publisher tags the release `v<version>`, adds the version to `release.json`, names the release `Culler <version>`, and uses the changelog section as the release description.

## Image archive checks

The image checks read the archives that `docker image save` writes for the default Project image and the product image.
Each archive holds `manifest.json`, the image configuration, and one tar per layer.
The scripts accept the legacy layout and the Open Container Initiative (OCI) layout, and plain or gzip layer blobs.
Each script uses the Python standard library only and runs with `python3 -I` on Python 3.9 and later.

`release/image_layers.py <image.tar>` checks one archive for development state and operator state.
It rejects Go build caches, Go build settings, and Culler home folders in any layer.

`release/image_contents.py --project <project.tar> --runner <runner.tar>` checks the file contents of both images.
The command prints `ok <check>` or `fail <check>: <detail>` for each check and exits 1 when a check fails.
The default Project image checks cover these rules:

- Each tool file under `/opt`, `/ms-playwright`, `/usr/local/bin`, and `/usr/local/libexec` appears in one layer.
- The browser folders hold no `.pak.info` file.
- The image holds no Git translations.
- Under `/root`, the image holds only `.bashrc` and `.profile`. The image holds no tool cache under `/home` and no file under `/tmp`.
- The browser programs hold no symbol table and no debug sections.
- The Git programs hold no debug sections. The rule covers `/usr/local/bin/git` and each Executable and Linkable Format (ELF) file under `/usr/local/libexec/git-core`.
- The final file view holds no setuid or setgid file.
- The agent account can read each tool file and enter each tool folder under the four tool paths.

The product image checks cover the layers above the default Project image:

- The runtime browser folder holds the full browser build and ffmpeg, no headless shell, and no `.pak.info` file.
- The OpenWiki packages hold no source maps and no TypeScript type files.
- The harness packages hold the glibc Claude program and the Codex program, and no musl Claude program and no unused peer package.
- The private Node.js holds no npm and no corepack.
- The layers hold no entry under `/root` except `.bashrc` and `.profile`, no tool cache under `/home`, and no file under `/tmp`.
- `mc-real` appears in one layer with owner `10001:10001`, mode `6755`, a symbol table, and no debug sections.
- Each runtime file appears in one layer.
- The runtime parts come in the order Node.js, browser, OpenWiki, Bun and harness packages, runtime source, per-release files.
- The per-release layers compress to less than 50 MB together.
- No build step changes the mode of every tool file. No build step changes the owner or mode of `mc-real` after its `COPY`.

`release/size_report.py --project <project.tar> --runner <runner.tar> --output <report.md>` writes the size report.
The report states the download size and the disk size of a fresh install and of an update.
The compressed size of a layer is the gzip level 6 size of its tar, the compression that the registry uses.
The runtime volume size counts each hard-linked file once.
The report compares each value with the baseline of release 9055488a.
With `--previous-project` and `--previous-runner`, the report compares with the previous archives instead.
The report then also states the size of the layers that differ from the previous archives.
