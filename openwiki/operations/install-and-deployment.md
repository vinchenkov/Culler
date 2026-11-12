---
type: operations
title: Installation, deployment, and release
description: The deployment front doors (install.sh, redeploy scripts, dev-reset.sh, uninstall.sh), the Go deployment and onboarding packages behind them, and the GitHub release pipeline that produces installable releases.
tags: [install, deployment, release, launchd, onboarding]
sources:
  - id: openwiki-source-4d1d392666be6dfdd7a91a2e
    resource: repo://.github/workflows/release.yml
  - id: openwiki-source-7c4f80bb47c6f9d6e14cc99f
    resource: repo://dashboard/src/security-gate.test.ts
  - id: openwiki-source-9bacaebabf4730d3f0a79cc9
    resource: repo://dashboard/src/server.ts
  - id: openwiki-source-096a781fb160ef979fa31121
    resource: repo://INSTALL.md
  - id: openwiki-source-03ffc32a0ca502ab67c54b25
    resource: repo://install.sh
  - id: openwiki-source-2421a6d0188a5ae9913930bf
    resource: repo://mc/cmd/mc/agent_skill_prod.go
  - id: openwiki-source-8887a56335011659b61b947b
    resource: repo://mc/cmd/mc/candidate-runtime-check.sh
  - id: openwiki-source-f4ac2493f15eb122b68f1029
    resource: repo://mc/cmd/mc/host_bun_prod.go
  - id: openwiki-source-d43a8f01d76df743f94a7d70
    resource: repo://mc/cmd/mc/onboard_home_prod.go
  - id: openwiki-source-b7fbb75b893eadf967f11537
    resource: repo://mc/cmd/mc/setup_agents_prod.go
  - id: openwiki-source-5a0c2550f7339d2e521c247e
    resource: repo://mc/cmd/mc/setup_choices_prod.go
  - id: openwiki-source-c6b3c0255eecd5c285f3893e
    resource: repo://mc/cmd/mc/setup_install_prod.go
  - id: openwiki-source-023af04189f7504ce88d56f5
    resource: repo://mc/cmd/mc/setup_opencode_prod.go
  - id: openwiki-source-ba92167dda59467828397749
    resource: repo://mc/cmd/mc/setup_opencode_source_prod_test.go
  - id: openwiki-source-1f9fad5e3ff6a05580945e60
    resource: repo://mc/cmd/mc/setup_published_prod.go
  - id: openwiki-source-37456df394e5501dc5f5c7ad
    resource: repo://mc/cmd/mc/setup_terminal_prod.go
  - id: openwiki-source-ae83eaa3edce8d9e15735e02
    resource: repo://mc/cmd/mc/setup_untested_connections_prod_test.go
  - id: openwiki-source-579b9c04dbd98eaf836c5c4c
    resource: repo://mc/cmd/mc/setup_update_candidate_prod.go
  - id: openwiki-source-30e353995389d69cb272185f
    resource: repo://mc/cmd/mc/setup_update_transaction_prod.go
  - id: openwiki-source-d98ef2f07998b9fbb2240456
    resource: repo://mc/cmd/mc/systemstatus.go
  - id: openwiki-source-d713bb120e2f3e26458d1540
    resource: repo://mc/cmd/mc/uninstall_modes_prod_test.go
  - id: openwiki-source-4e67d5d53e1cf48154d0675b
    resource: repo://mc/cmd/mc/uninstall_prod.go
  - id: openwiki-source-02886ae4c9efa40e167cf53b
    resource: repo://mc/config/config.go
  - id: openwiki-source-742785bcdc10cca69fe87d3f
    resource: repo://mc/deployment/agent_skill_integration.go
  - id: openwiki-source-7c0475e3159de8f777665c70
    resource: repo://mc/deployment/agent_skill.go
  - id: openwiki-source-0c1bf38213ed3591fc2a6f4d
    resource: repo://mc/deployment/cli_install.go
  - id: openwiki-source-78f1d76d5a5b9577a60c9016
    resource: repo://mc/deployment/host_release_closure_test.go
  - id: openwiki-source-d2a2a705448c3432ad6c1337
    resource: repo://mc/deployment/host_release.go
  - id: openwiki-source-c3574bc58db62fef903f9230
    resource: repo://mc/deployment/opencode_source.go
  - id: openwiki-source-f2370cd7c903f813dbc4ad1c
    resource: repo://mc/deployment/runtimeauth_host_sources_test.go
  - id: openwiki-source-d0b091e57853771f453ce84a
    resource: repo://mc/deployment/runtimeauth_setup_login_test.go
  - id: openwiki-source-da04f25cb9fa15d5a8447612
    resource: repo://mc/deployment/runtimeauth_setup_login.go
  - id: openwiki-source-44ff1dc1a6b4e5974f9836a4
    resource: repo://mc/deployment/supervision.go
  - id: openwiki-source-1539e79687d52c9972a1201f
    resource: repo://mc/distribution/update_policy.json
  - id: openwiki-source-3dcbed55f4a2df7c7a474bd0
    resource: repo://mc/onboarding/terminal_search.go
  - id: openwiki-source-12f32e50a07508468db6789a
    resource: repo://redeploy.sh
  - id: openwiki-source-2aee5c75a683753fe072cd2d
    resource: repo://release/image_load.py
  - id: openwiki-source-2e0b70f7c6b582e5eaae24c8
    resource: repo://release/installer.sh
  - id: openwiki-source-ae3f4330fd1215664310fac0
    resource: repo://release/PROVIDER-COMPATIBILITY.md
  - id: openwiki-source-6926a7f59d9d7d096748e09e
    resource: repo://release/publish.py
  - id: openwiki-source-985fc8702c7fef3be1d90e4d
    resource: repo://release/security/README.md
  - id: openwiki-source-09c5383289d9635a85a372a3
    resource: repo://release/UPDATES.md
  - id: openwiki-source-4f766d7b168fe76a82f503a8
    resource: repo://runner/image/build-prod.sh
  - id: openwiki-source-cace99190acf5608e60583a3
    resource: repo://runner/image/Dockerfile
  - id: openwiki-source-1430dd49737c0f35b616bb76
    resource: repo://runner/image/runtime-check.sh
  - id: openwiki-source-074a0c31b85dc048be2b3972
    resource: repo://runner/shared/project-cache.ts
  - id: openwiki-source-be37791327f4a5dbaa8f4f9f
    resource: repo://scripts/install-source.sh
  - id: openwiki-source-94e7bca26bc3525581ff6cdf
    resource: repo://VERSION
generated: { by: "culler-scribe", at: "2026-11-12T09:22:03.309Z" }
verified:
  - by: openwiki/0.9.0
    at: 2026-11-12T09:22:03.309Z
---

# Installation, deployment, and release

Culler deploys onto one Apple-silicon Mac. Production never runs source from a clone. A deployment home (`MC_HOME`, default `~/.culler`) holds an installed copy of the exact shipped files, the configuration, and three launchd units: resident, dashboard, and AgentsView. The spine lives in a Docker named volume. Several shell scripts at the repository root are the front doors, and they are not interchangeable. The Go code behind them lives in `mc/deployment`, `mc/onboarding`, and `mc/distribution`. `release/` and `.github/workflows/release.yml` build and publish releases.

The operator uses `install.sh` for setup and repair and `culler` for lifecycle operations. Operator-facing text never tells a human to run `mc`.

## The front doors

The Culler repository and its release downloads are public.
Run this command in Terminal on a Mac with Apple silicon:

```sh
curl -fsSL https://raw.githubusercontent.com/vinchenkov/Culler/main/install.sh | bash
```

The launcher selects the latest complete release.
Public installation requires no GitHub account or GitHub token.
After installation, `culler update` resumes an incomplete update or opens the installer for the latest complete release.
The command `culler update --check` checks release availability without a deployment change.
The [installation guide](../../INSTALL.md) describes setup and updates.

| Command | Purpose |
| --- | --- |
| `sh install.sh` | Install or update from the latest complete GitHub release with a saved launcher. |
| `culler update` | Resume an incomplete update or open the installer for the latest complete release. |
| `culler update --check` | Check release availability without a deployment change. |
| `culler update --restore` | Restore an incomplete update before commit. |
| `culler uninstall [--keep-data \| --dry-run]` | Remove all detected installations or inspect the removal plan. |
| `./install.sh --dev --mc-home <scratch>` | Development install from this checkout into a scratch home. Never `~/.culler`. |
| `sh scripts/install-source.sh` | Build and install a production deployment from the source checkout. |
| `./redeploy.sh` | Move the live production deployment to this checkout and keep its database. |
| `./redeploy.sh --reset-database` | Move the deployment and replace its database with an empty database, after a typed confirmation. |
| `./redeploy-dashboard.sh` | Move only the live dashboard files to `HEAD`. |
| `./dev-reset.sh --confirm [--rebuild] [--smoke]` | Destroy and re-provision deployment data. |
| `./uninstall.sh [--keep-data \| --full-wipe]` | Remove software, with an explicit data choice. |

### install.sh: the release launcher

`install.sh` is a permanent release launcher.
It first handles `--help`.
With `--dev`, it executes `scripts/install-source.sh`, which requires a checkout.
Otherwise it requires `Darwin:arm64`.

An incomplete public update takes precedence over release discovery.
The launcher verifies the retained candidate package and resumes that exact update.
After checkpoint completion, recovery requires no new download.
For a new update, it performs these release checks:

1. It uses an optional GitHub credential from `CULLER_GITHUB_TOKEN_FILE`, the default token file, or `gh auth token`. The default file is `~/.config/culler/github-token`. A token file must be a regular file with mode `600` and belong to the operator. The launcher keeps the token outside command arguments. Public downloads work without a credential. An invalid configured credential can stop installation.
2. It fetches `releases/latest` and rejects drafts and prereleases.
3. It downloads `release.json` and checks `format`, `platform`, and the 40-hex commit identity against the tag.
4. It requires each of the five checks (`fast-suite-macos`, `deployment-linux`, `application-package`, `product-image`, `release-downloads`) to appear exactly once with `passed: true`.
5. It downloads `installer.sh` and verifies its SHA-256 and size before executing it.

`release/installer.sh` downloads `culler-darwin-arm64.tar.gz` and verifies its checksum and size. Before extraction it rejects any path outside `bin/{mc,culler,install.sh,agentsview}`, `host/`, and `runner/`, and any link or special file. It also checks that `bin/culler` matches `bin/mc`. `--check-updates` runs `mc __release status-terminal` and exits. Otherwise the installer ensures the Apple developer tools and Git. It downloads the Bun archive named by the release record, checks its size and SHA-256 value, and installs Bun under `MC_HOME/bun/<version>/bin/bun`. It then runs `mc __setup` with `--release-source` and `--host-release-source` pointing into the unpacked package.

The resident and dashboard run the host Bun of the published release.
A source install uses Bun from the source toolchain.
The published installer does not require mise.
An update removes an old host Bun after commit.

Setup installs `culler` beside the runtime executable, normally in `~/.local/bin`.
The `--bin-dir` option selects another absolute directory.
Setup preserves an unrelated executable with the same name and reports the conflict.
It verifies command access in new zsh and bash shells and gives a path command when the current shell needs one.
`mc/deployment/cli_install.go` records the command path and shell edits for later checks and removal.
The [lifecycle command](culler-lifecycle.md) controls service state after setup.

`mc __setup` (`mc/cmd/mc/setup_terminal_prod.go`, `runTerminalSetup`) is the terminal setup wizard, macOS only. Its stages are Welcome, Prepare your Mac, Connect an agent, Review your setup, Start Culler, and Open your dashboard. It persists progress through `onboarding.LoadSession`/`SaveSession` under `MC_HOME/setup`, so rerunning `install.sh` resumes. `mc/onboarding` provides the wizard's pieces: Docker detection and image preparation (`prepare.go`, `image_progress.go`), the terminal UI (`terminal*.go`), access checks (`access.go`), and diagnostics. INSTALL.md states that an update preserves configuration, credentials, Project repositories, compatible database records, pause choices, the AgentsView archive, and the viewer secrets. It also states that an incompatible database blocks activation.

### Development install

`scripts/install-source.sh --dev` builds `mc` with `-tags test_fake_routing` into `MC_HOME/bin` and uses the spine directly.
It supplies the first Project, routing, tunables, and dashboard choices to the internal setup command.
This fake-harness tier retains its separate behavior.

### Source production deployment

`sh scripts/install-source.sh` builds Culler from the checkout and opens the terminal setup wizard.
It uses the Go and Bun versions from `.mise.toml` through mise.
The non-interactive path builds the production image and executes the internal setup sections in sequence.
`./redeploy.sh` uses the same home preparation for an existing production deployment.

Both source paths prepare OpenCode before service activation, even without an OpenCode connection.
The selected Linux arm64 image supplies npm and the other package tools.
The host requires no npm installation.
The package occupies its own Docker volume, separate from the ordinary Culler runtime.

`mc/cmd/mc/onboard_home_prod.go` installs the runtime, records the source, and calls `UpdateOpenCodeVolume`.
`mc/deployment/opencode_source.go` verifies installed contents before reuse and prepares a replacement when required inputs differ.
An unrelated Culler commit, checkout path, or timestamp change preserves reuse when package inputs remain equal.
Successful preparation or verified reuse records readiness for the dashboard and terminal setup.
The first OpenCode connection uses that package without another download.

A failure stops home preparation before activation and records the failed stage and cause.
`mc/cmd/mc/setup_terminal_prod.go` preserves that source error through its safe diagnostic formatter.
The source installer can resume an incomplete installation after repair.
The published installer retains its first-connection download and its separate update recovery procedure.

`mc/deployment/opencode_source.go` and `INSTALL.md` define these source and published boundaries.
See [source package preparation and reuse](image-delivery.md#source-package-preparation-and-reuse) for package identity, verification, and repair.

### redeploy.sh and redeploy-dashboard.sh

`redeploy.sh` is production tier only. Its one option is `--reset-database`. Its order is deliberate:

1. Build the host binary into a scratch path. A compile error leaves the deployment active.
2. Check the deployed database with the new Linux binary through a read-only volume mount, unless a reset was confirmed.
3. Install the pinned AgentsView executable while the services remain active.
4. Stop the supervised units and confirm that each unit stopped.
5. Build the production images, install the binary, prepare the home and tunables, activate supervision, and run the health checks.

Source redeploys also pass the selected executable directory to setup so it can install `culler`.
They refuse an incomplete public update and cannot run concurrently with public lifecycle operations.

The constraint behind this order: `runner/image/build-prod.sh` exits 3 while any `com.culler.*` unit is loaded, with no override. Rebuilding the image under a live helper would re-stamp the release identity and make every tick fail closed. `redeploy_test.sh` proves this sequence with fake tools.
A failed service list or an unconfirmed service stop also ends the attempt before the image build.

The early database check stops before deployment changes when it finds a schema difference or an absent deployed database.
An unavailable check permits the later deploy step to perform the database check after services stop.
A refused check stops the attempt. The stop message identifies recovery choices and the previous deployed commit when available.
The deploy step keeps the deployed database. When that database has a different schema from the release, or when the database is absent, `redeploy.sh` stops and keeps the database. The stop message names the cause and the `./redeploy.sh --reset-database` option. It names no `mc` command.

`--reset-database` replaces the deployed database with an empty database with the current schema. Before the first build step, the script asks the operator to type `reset` at a terminal. A run without a terminal or without that word changes nothing. The reset stops the agent and Collie containers that use the database. It keeps the Project repositories and worktrees, the credentials, `config.toml`, and the deployment identity. The tunables section then copies the `config.toml` settings into the database. `redeploy_test.sh` proves the stop on a schema difference, the kept database, and the confirmed reset.

`redeploy-dashboard.sh` copies only the dashboard part of the host release manifest as committed at `HEAD`. It ignores uncommitted `dashboard/src` changes. It swaps each changed file with one rename. It restarts the dashboard unit only when a `.ts` file changed; browser-only changes need a page reload. A change that needs a new `mc` verb or a new manifest entry needs `redeploy.sh`.

### dev-reset.sh and uninstall.sh

`dev-reset.sh` with no flags prints its plan. With `--confirm`, it copies `refresh-grants`, `runtime-auth-sources`, `config.toml`, and `routing.md` to a safe-hold. It then boots out the units, removes stray pipeline containers by label, runs `mc __reset --confirm`, and re-provisions with development lease tunables. `--rebuild` first rebuilds host `mc` and the `mc-prod` image at `HEAD`. This matters because activation refuses when the two carry different commits. `--smoke` runs the full-pipeline ride-through.

`uninstall.sh` builds or locates an `mc`. It runs `mc __uninstall --dry-run`, then `--confirm` with the selected mode. `--full-wipe` is the default. It deletes Culler data, task branches, credentials, and review files, and keeps Project repositories. `--keep-data` removes software and keeps deployment data for a later reinstall. Both modes remove the host Bun under `MC_HOME/bun/`. They leave Docker, Git, mise, Go, and the operator's Bun installed.

## mc/deployment

- **Release manifests.** `hostReleaseFiles` in `host_release.go` and `runnerReleaseFiles` in `runner_release.go` list the exact shipped files. `InstallHostRelease` and `InstallRunnerRelease` copy them into `MC_HOME/release` through a staging directory. `ValidateHostRelease` walks the installed tree. It rejects any unexpected or missing file, any symlink, any file not owned by the operator, and any mode other than 0700 for directories or 0600 for files. A new resident, dashboard, or runner file that is not in the manifest fails at activation. The closure tests (`host_release_closure_test.go`, `runner_release_closure_test.go`) read files outside the module, so `mc/check.sh` runs `./deployment/` with `-count=1`. See [the dashboard](../architecture/dashboard.md) for the three-edit rule.

The host release lists the dashboard fonts and their license notices. The dashboard server maps their URLs to the shipped files. `TestHostReleaseManifestShipsNothingUnreachable` checks that every served dashboard file appears in the host release. It also checks that each shipped file exists and has a caller or server route.
The same manifest ships `scene.js`, `scene-parts.webp`, and `scene-parts-dark.webp` for the [background picture motion](../workflows/dashboard-background.md).
The server lists `scene.js` in `UI_MODULES` and serves both image assets as `image/webp`.
`app.js` connects that module to the logo button through the theme controls.
- **Names.** `RuntimeNames` derives the spine volume `mc-spine-<12 hex>` and helper `mc-helper-<12 hex>` from a SHA-256 of the canonical `MC_HOME`. Several deployments can therefore share one Docker daemon.
- **Supervision.** `PrepareSupervision` (`supervision.go`) publishes the native configs and three LaunchAgent plists under `MC_HOME/supervision`. It never writes `~/Library/LaunchAgents` and never bootstraps a job. `ActivateSupervision` (`supervision_activate.go`) is the operator-present half. It installs the plists, bootstraps the jobs, requires a fresh release-bound resident tick receipt, and rolls back a partial first activation. `ReconfigureSupervision` replaces loaded units. `requireLaunchdTCCSafeHome` refuses homes in TCC-protected user folders, because Full Disk Access does not transfer to a LaunchAgent. Never load launchd during development.
- **AgentsView.** `agentsview.json` pins version, archive, and executable digests. Only a change to that file changes the shipped AgentsView. `viewer_secrets.go` manages the AgentsView token and wiki key.
- **Agent access.** `agentprofiles*.go`, `agentaccess*.go`, `runtimeauth*.go`, and `provider_acceptance*.go` implement agent profiles, isolated sign-in and refresh grants, and live model acceptance. Their behavior is out of scope here.

`mc/distribution` defines the published release contract (`record.go`: `RequiredChecks`, `InstalledRecord = state/published-release.json`) and a GitHub client for update checks.
The record also names the host Bun archive, its size, and its SHA-256 value. `mc/deployment/hostbun.json` pins that archive for the package build.

## config.example.toml

Onboarding copies `config.example.toml` into `MC_HOME/config.toml`, and only that copy is edited. Sections: `[runtimes]` (enabled bindings, forbidden env, credentials, bindings), `[budgets]`, `[timing]`, `[container]`, `[surfaces.dashboard]` (`127.0.0.1:7333`), `[surfaces.agentsview]` (`7334`), `[surfaces.wiki]` (`7335`), `[surfaces.discord]`, `[surfaces.alerts]`, `[git]`, and `[development]`. The dashboard, AgentsView, and wiki addresses must use three different ports. `dashboard/CONFIGURATION.md` records which keys the dashboard edits.

## The release pipeline

`.github/workflows/release.yml` runs on pull requests and pushes to `main`. Its default permission is `contents: read`.

- `fast-suite-macos` runs every component `check.sh` and `./release/check.sh` on `macos-15`. It fails a non-`mc` component whose log shows zero successful tests.
- `application-package` runs alongside the fast and browser suites. It builds and validates the package, then uploads the exact release inputs with their digest.
- `deployment-linux` runs the portable `./deployment/` tests and the Linux filesystem-guard tests on `ubuntu-24.04-arm`.
- `dashboard-browser` runs the complete browser suite on Linux arm64 for pull requests and pushes to `main`. It uses the Project image Playwright lock file and matching Chromium.
- `security-acceptance` checks the real Trivy scanner with controlled advisory fixtures. No job waits for it.
- `release-images` builds both images with read-only permissions. It checks product behavior, inspects layers, and saves each checked archive.
- The image checks verify file contents and write a fresh install and update size report. See [image delivery and update cleanup](image-delivery.md).
- `runner/image/project-smoke.sh` also runs the updater's shared runtime check against the default Project image under the same container restrictions.
- `image-vulnerabilities` reports Trivy vulnerability results for the saved archives. No job waits for it.
- `release-checks` requires the fast suite, package, deployment, browser, and image jobs to succeed.
- `release-version` selects publication through the release policy.
- `product-image` runs only on a push to `main`. It checks and loads the saved archives before registry upload, then records both immutable digests.
- `release-downloads` runs only on a push to `main`. It uses `release/publish.py` under serialized publication control.

The publication jobs require `dashboard-browser` from the same workflow run.
Both `product-image` and `release-downloads` list it in `needs`.
The read-only `release-images` job depends on `application-package`.
The browser job runs independently.
The browser job holds read-only repository permission and receives no provider credentials.
It retains the suite log, JUnit report, and screenshots in `culler-dashboard-browser-<attempt>` for seven days.
See [browser checks](../tests/checks-and-test-lanes.md#required-browser-checks) for local execution and failure rules.

`release/build.sh` requires a clean tree at `CULLER_COMMIT`.
It verifies the pinned AgentsView archive and builds `mc` for Darwin and Linux arm64 with the commit identity.
It assembles the package and image inputs outside the checkout.
`publish.py` uploads the launcher, installer, application package, optional OpenCode archive, and `release.json` with all five checks.
It checks that the commit belongs to `main` and preserves published releases as immutable records.
`release/check.sh` runs the release tool tests without credentials and fails when it selects zero tests.

The current `VERSION` is `0.2.1`.
`mc/distribution/update_policy.json` currently sets `test_releases` to `true`.
This policy permits each new commit to publish through the normal install path with the tag `test-<full-commit>`.
It bypasses the version and migration-path gates; it supplies no data conversion or database reset.
The maintainer sets the field to `false` before public release.
The ordinary policy uses `v<version>` tags, requires a version above the latest public version, and checks the supported update path.
Each version requires a section in `CHANGELOG.md`.

The image job restores the latest compatible image cache; only a push to `main` saves it.
Go build caches use a separate key for each commit, job, and platform.
The cleanup job retains two Go build caches per job and platform on `main` and preserves image and dependency caches.

Both publication jobs also require `release-checks`.
The vulnerability scans report their results in job summaries and artifacts, and a scan result never prevents a release.
GitHub secret scanning reports secrets on the **Security** tab of the repository.
`publish.py` downloads each uploaded release file again and compares it with the local file.
See [Release security scans](release-security.md) for coverage, reports, secret scanning, nightly scans, and repository ruleset enforcement.

## Contract tests

`install_test.sh` runs the launcher and `release/installer.sh` against fixtures with fake `curl`, `mise`, and `mc`. `redeploy_test.sh` proves the redeploy order and its failure behavior. `scripts/install-source_test.sh` and `uninstall_test.sh` cover their scripts. `mc/check.sh` runs the first two; see [checks and test lanes](../tests/checks-and-test-lanes.md).

## Runtime updates and agent sign-in

`runtime.Dockerfile` supplies the private Culler runtime.
The installer updates its deployment-specific volume after its users stop.
Each Project retains its selected image and configuration revision across that update.
Project image admission checks a stopped container archive before the image can access the spine.
That archive check executes no candidate programs.
See [Project environments](../workflows/project-environments.md).

A published update separately runs the candidate runtime on each distinct selected Project image, including images of archived Projects.
`mc/cmd/mc/setup_update_candidate_prod.go` uses temporary runtime volumes and containers without a live spine mount.
The embedded `candidate-runtime-check.sh` checks the required directories, absent run identity, runtime behavior, and release identity.
The default image's protected `/mc/spine` directory is permitted; the runtime check requires it to remain unreadable by the agent user.
A failed check stops the update before replacement and identifies the affected Project.
See [candidate runtime compatibility](image-delivery.md#candidate-runtime-compatibility) for restrictions and the shared release smoke.

Terminal setup first offers providers with a sign-in, then **Use another provider**.
Each subscription provider offers sign-in before its API key method.
Setup assigns connection and additional account names automatically.
An additional subscription account receives an available harness name with a numeric suffix.
Selecting sign-in starts authorization for that account directly.
`mc/cmd/mc/setup_agents_prod.go` owns the provider and access choices.
`mc/cmd/mc/setup_connection_choices_prod.go` owns additional account names and access transitions.

Codex and Claude Code connections use sign-in that Culler starts in separate home and credential folders.
`mc/deployment/runtimeauth_setup_login.go` launches the detected agent program in that isolated environment.
The operator's agent login remains separate from Culler's login.
For Codex browser authorization, the child process receives no standard input, so it leaves the operator's terminal input unread.
The output filter still delivers the authorization address to setup.
`TestSetupRuntimeAuthLoginLeavesCodexTerminalInputUnread` in `runtimeauth_setup_login_test.go` checks both properties.

Setup tests the selected connection before service activation.
It records successful connection and model tests in the spine so the dashboard can show their verification state.
If the result record fails, setup keeps the connection and asks for another test.
The host retains provider credentials in macOS Keychain.
Each agent receives a temporary Culler token and a public proxy certificate.
Static API keys use the same host boundary.
`runtimeauth_signin_test.go` and the terminal setup tests cover connection creation.
`runtimeauth_host_sources_test.go` checks cleanup after a credential directory changes during sign-in. Its fixture restores directory write access before it renames the sealed directory, so the rename works on macOS 15.

The release defines the supported provider authorities.
Configuration validation rejects custom provider destinations and unsupported credential kinds.
The host renews account access while the container retains its Culler token.
Other catalog providers use the OpenCode harness and its separate runtime volume.
Published installations download this package on demand; source deployments prepare it before activation.
**Use another provider** searches the catalog providers that OpenCode runs.
An empty search lists all those providers.
The provider search updates after each key on terminals with screen controls.
It lists name matches before identifier and address matches and shows details for the selected provider.
A terminal without screen controls uses a query prompt followed by a choice list.
`mc/onboarding/terminal_search.go` provides this interaction, and `mc/cmd/mc/setup_opencode_prod.go` supplies the provider matches.

Setup keeps a failed connection for retry.
When another connection replaces it, setup removes unused connections it created that never passed a test.
The selected connection and connections used by enabled profiles or roles remain available.
`mc/onboarding/session.go` retains the cleanup list across restarts.
`mc/cmd/mc/setup_agents_prod.go` retries failed removals during later cleanup attempts.
The connection and terminal tests cover cleanup, retry, result record failures, and search output at narrow widths.

A new Codex setup profile uses the native default model during its connection test.
After success, setup saves the selected model for later runs.
See [Agent connections and access renewal](agent-connections.md) for profiles, grant storage, and failure behavior.

Public updates use the compatibility values in the release record to check the installed formats and dependency producers.
The initial implementation supports unchanged spine, configuration, and credential formats.
A declared accepted format alone cannot convert data.
`release/UPDATES.md` defines the support policy, checkpoint, restore procedure, and required packaged acceptance.
`release/PROVIDER-COMPATIBILITY.md` separates local compatibility checks from external provider behavior.
The candidate checks saved OpenCode providers, models, and explicit efforts against its catalog without a provider request.
A disabled profile keeps invalid choices and shows its problem.
An enabled profile receives a warning before confirmation and can remain unavailable after the confirmed update.
Other valid profiles remain available, and the affected profile consumes no task attempt.
Culler preserves explicit choices; local checks cannot prove current provider access or identical behavior for provider defaults.
See [Culler lifecycle](culler-lifecycle.md#public-updates-and-recovery) for the update sequence and retained verification limits.

Restore applies the available checkpoint and prior stop choices before it starts the previous release's helper container.
The helper supplies spine access for resident verification.
`mc/cmd/mc/setup_update_transaction_prod.go` restores services only after the helper starts, then verifies those services before it releases maintenance.
A helper failure keeps work stopped and preserves the restore path through `culler update --restore` after repair.

Keep-data uninstall preserves Project caches and derived images.
Full wipe removes deployment cache volumes and derived image tags.
Both modes preserve Project repositories and shared host tools.

## Persistent Project dependencies

Each Project home resides at `MC_HOME/state/projects/<project>/home`.
Setup and pipeline containers use it through `HOME=/home/agent`.
A Culler update or Project image change preserves its files.
The package manager must still check compatibility with the selected image and current declarations.
`PrepareProjectHome` creates absent directories and preserves existing contents.
It refuses invalid identities and unsafe host paths.

The shared `/cache` volume remains separate and disposable.
Its stable Project and platform identity permits reuse across image revisions.
During dependency preparation, `importLegacyNpmCache` copies absent retained npm archives into the normal home cache.
It preserves existing destination files and publishes each new file through a temporary copy and hard link.
Current dependency reuse otherwise follows package managers' normal home paths.

Keep-data uninstall also preserves installed tools and configuration in the Project home.
Full wipe removes that deployment-owned home.
`mc/cmd/mc/uninstall_modes_prod_test.go` checks these files together with preserved Project repositories.
The Environment page's Clear /cache action preserves the home.
See [Project environments](../workflows/project-environments.md#runtime-and-per-run-dependencies) for preparation, disk use, and test boundaries.

## Discord configuration and deployment boundary

The operator configures Discord through **Configs → Discord** after initial Project source acceptance.
The host saves the token and policy in protected configuration and applies connection changes through the resident's local socket.
Routine connection edits require no install command or process restart.
The generated resident supervision file excludes the Discord policy and bot token.

The host release manifest ships `dashboard/src/ui/discord.js` and `resident/src/discord-config.ts`.
The feature also requires host command and spine schema changes.
A dashboard-only redeploy cannot supply those changes; the full deployment procedure applies.
The normal schema compatibility check still controls database preservation or a separately confirmed reset.
See [Discord configuration](../integrations/discord.md) for connection recovery and the limits of the retained acceptance report.

## External agent installation

The host release installs the common `culler` skill at `MC_HOME/release/host/skills/culler/`.
`mc/deployment/agent_skill.go` resolves its authoritative source files and writes installation-specific executable, release, trace-reader, and scratch paths.
The installed entry point selects the deployment without a source checkout.
The host and runner manifests include the complete skill references and native trace reader.

Terminal setup automatically links the skill at `~/.agents/skills/culler`.
It also links `~/.claude/skills/culler` for a selected Claude Code harness and `~/.codex/skills/culler` for a selected Codex harness.
`mc/cmd/mc/agent_skill_prod.go` derives those locations from the selected runtime bindings.
`mc/deployment/agent_skill_integration.go` records owned links and preserves unrelated files.
It creates links only in operator-owned directories that other users cannot write.
A link failure leaves completed setup intact and leaves the canonical installed skill available.
A later selection removes owned links outside the selected locations. The integration record also accepts the earlier single-link format.
Uninstall removes only owned integration links. Operator skills remain separate under `MC_HOME/skills/`.

Selecting subscription sign-in starts authorization and its connection test directly.
API key and saved-connection model tests retain their expenditure confirmation.
`setup_agents_prod.go` owns this distinction; the setup screen tests cover the resulting flow.
