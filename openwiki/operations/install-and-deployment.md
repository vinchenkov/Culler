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
  - id: openwiki-source-91a533664b7e9f0fa7a313b0
    resource: repo://install_test.sh
  - id: openwiki-source-096a781fb160ef979fa31121
    resource: repo://INSTALL.md
  - id: openwiki-source-03ffc32a0ca502ab67c54b25
    resource: repo://install.sh
  - id: openwiki-source-f4ac2493f15eb122b68f1029
    resource: repo://mc/cmd/mc/host_bun_prod.go
  - id: openwiki-source-c6b3c0255eecd5c285f3893e
    resource: repo://mc/cmd/mc/setup_install_prod.go
  - id: openwiki-source-d713bb120e2f3e26458d1540
    resource: repo://mc/cmd/mc/uninstall_modes_prod_test.go
  - id: openwiki-source-4e67d5d53e1cf48154d0675b
    resource: repo://mc/cmd/mc/uninstall_prod.go
  - id: openwiki-source-02886ae4c9efa40e167cf53b
    resource: repo://mc/config/config.go
  - id: openwiki-source-0c1bf38213ed3591fc2a6f4d
    resource: repo://mc/deployment/cli_install.go
  - id: openwiki-source-78f1d76d5a5b9577a60c9016
    resource: repo://mc/deployment/host_release_closure_test.go
  - id: openwiki-source-d2a2a705448c3432ad6c1337
    resource: repo://mc/deployment/host_release.go
  - id: openwiki-source-f2370cd7c903f813dbc4ad1c
    resource: repo://mc/deployment/runtimeauth_host_sources_test.go
  - id: openwiki-source-da04f25cb9fa15d5a8447612
    resource: repo://mc/deployment/runtimeauth_setup_login.go
  - id: openwiki-source-44ff1dc1a6b4e5974f9836a4
    resource: repo://mc/deployment/supervision.go
  - id: openwiki-source-12f32e50a07508468db6789a
    resource: repo://redeploy.sh
  - id: openwiki-source-2aee5c75a683753fe072cd2d
    resource: repo://release/image_load.py
  - id: openwiki-source-2e0b70f7c6b582e5eaae24c8
    resource: repo://release/installer.sh
  - id: openwiki-source-6926a7f59d9d7d096748e09e
    resource: repo://release/publish.py
  - id: openwiki-source-985fc8702c7fef3be1d90e4d
    resource: repo://release/security/README.md
  - id: openwiki-source-4f766d7b168fe76a82f503a8
    resource: repo://runner/image/build-prod.sh
  - id: openwiki-source-074a0c31b85dc048be2b3972
    resource: repo://runner/shared/project-cache.ts
  - id: openwiki-source-be37791327f4a5dbaa8f4f9f
    resource: repo://scripts/install-source.sh
generated: { by: "culler-scribe", at: "2026-11-05T10:01:23.917Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-05T10:01:23.917Z
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
After installation, `culler update` opens the installer for the latest complete release.
The command `culler update --check` checks release availability without a deployment change.
The [installation guide](../../INSTALL.md) describes setup and updates.

| Command | Purpose |
| --- | --- |
| `sh install.sh` | Install or update from the latest complete GitHub release with a saved launcher. |
| `culler update` | Open the installer for the latest complete GitHub release. |
| `culler update --check` | Check release availability without a deployment change. |
| `./install.sh --dev --mc-home <scratch>` | Development install from this checkout into a scratch home. Never `~/.culler`. |
| `./redeploy.sh` | Move the live production deployment to this checkout and keep its database. |
| `./redeploy.sh --reset-database` | Move the deployment and replace its database with an empty database, after a typed confirmation. |
| `./redeploy-dashboard.sh` | Move only the live dashboard files to `HEAD`. |
| `./dev-reset.sh --confirm [--rebuild] [--smoke]` | Destroy and re-provision deployment data. |
| `./uninstall.sh [--keep-data \| --full-wipe]` | Remove software, with an explicit data choice. |

### install.sh: the release launcher

`install.sh` is a permanent launcher. It never builds from source in production. It first handles `--help`. With `--dev`, it `exec`s `scripts/install-source.sh`, which requires a checkout. Otherwise it requires `Darwin:arm64` and then does the following:

1. It uses an optional GitHub credential from `CULLER_GITHUB_TOKEN_FILE`, the default token file, or `gh auth token`. The default file is `~/.config/culler/github-token`. A token file must be a regular file with mode `600` and belong to the operator. The launcher keeps the token outside command arguments. Public downloads work without a credential. An invalid configured credential can stop installation.
2. It fetches `releases/latest` and rejects drafts and prereleases.
3. It downloads `release.json` and checks `format`, `platform`, and the 40-hex commit identity against the tag.
4. It requires each of the five checks (`fast-suite-macos`, `deployment-linux`, `application-package`, `product-image`, `release-downloads`) to appear exactly once with `passed: true`.
5. It downloads `installer.sh` and verifies its SHA-256 and size before executing it.

`release/installer.sh` downloads `culler-darwin-arm64.tar.gz` and verifies its checksum and size. Before extraction it rejects any path outside `bin/{mc,culler,install.sh,agentsview}`, `host/`, and `runner/`, and any link or special file. It also checks that `bin/culler` matches `bin/mc`. `--check-updates` runs `mc __release status-terminal` and exits. Otherwise the installer ensures the Apple developer tools and Git. It downloads the Bun archive named by the release record, checks its size and SHA-256 value, and installs Bun under `MC_HOME/bun/<version>/bin/bun`. It then runs `mc __setup` with `--release-source` and `--host-release-source` pointing into the unpacked package.

The resident and dashboard run the host Bun of the published release. A source install uses Bun from the source toolchain. The published installer does not require mise. An update removes an old host Bun after activation completes.

Setup installs `culler` beside the runtime executable, normally in `~/.local/bin`.
The `--bin-dir` option selects another absolute directory.
Setup preserves an unrelated executable with the same name and reports the conflict.
It verifies command access in new zsh and bash shells and gives a path command when the current shell needs one.
`mc/deployment/cli_install.go` records the command path and shell edits for later checks and removal.
The [lifecycle command](culler-lifecycle.md) controls service state after setup.

`mc __setup` (`mc/cmd/mc/setup_terminal_prod.go`, `runTerminalSetup`) is the terminal setup wizard, macOS only. Its stages are Welcome, Prepare your Mac, Connect an agent, Review your setup, Start Culler, and Open your dashboard. It persists progress through `onboarding.LoadSession`/`SaveSession` under `MC_HOME/setup`, so rerunning `install.sh` resumes. `mc/onboarding` provides the wizard's pieces: Docker detection and image preparation (`prepare.go`, `image_progress.go`), the terminal UI (`terminal*.go`), access checks (`access.go`), and diagnostics. INSTALL.md states that an update preserves configuration, credentials, Project repositories, compatible database records, pause choices, the AgentsView archive, and the viewer secrets. It also states that an incompatible database blocks activation.

### Development install

`scripts/install-source.sh --dev` builds `mc` with `-tags test_fake_routing` into `MC_HOME/bin` (direct spine, no helper). It then hands off to `mc __onboard` with the first Project, routing, tunables, and dashboard choices as flags. A production run of this script from a checkout instead builds a scratch `mc` and opens the same `__setup` wizard, A non-interactive production run builds the production image with `runner/image/build-prod.sh` and runs the `mc __onboard` sections in sequence.

### redeploy.sh and redeploy-dashboard.sh

`redeploy.sh` is production tier only. Its one option is `--reset-database`. Its order is deliberate:

1. Build the host binary into a scratch path. A compile error leaves the deployment active.
2. Check the deployed database with the new Linux binary through a read-only volume mount, unless a reset was confirmed.
3. Install the pinned AgentsView executable while the services remain active.
4. Stop the supervised units and confirm that each unit stopped.
5. Build the production images, install the binary, prepare the home and tunables, activate supervision, and run the health checks.

Source redeploys also pass the selected executable directory to setup so it can install `culler`.

The constraint behind this order: `runner/image/build-prod.sh` exits 3 while any `com.culler.*` unit is loaded, with no override. Rebuilding the image under a live helper would re-stamp the release identity and make every tick fail closed. `redeploy_test.sh` proves this sequence with fake tools.
A failed service list or an unconfirmed service stop also ends the attempt before the image build.

The early database check stops before deployment changes when it finds a schema difference or an absent deployed database.
An unavailable check permits the later deploy step to perform the database check after services stop.
A refused check stops the attempt. The stop message identifies recovery choices and the previous deployed commit when available.
The deploy step keeps the deployed database. When that database has a different schema from the release, or when the database is absent, `redeploy.sh` stops and keeps the database. The stop message names the cause and the `./redeploy.sh --reset-database` option. It names no `mc` command.

`--reset-database` replaces the deployed database with an empty database with the current schema. Before the first build step, the script asks the operator to type `reset` at a terminal. A run without a terminal or without that word changes nothing. The reset stops the agent and Homie containers that use the database. It keeps the Project repositories and worktrees, the credentials, `config.toml`, and the deployment identity. The tunables section then copies the `config.toml` settings into the database. `redeploy_test.sh` proves the stop on a schema difference, the kept database, and the confirmed reset.

`redeploy-dashboard.sh` copies only the dashboard part of the host release manifest as committed at `HEAD`. It ignores uncommitted `dashboard/src` changes. It swaps each changed file with one rename. It restarts the dashboard unit only when a `.ts` file changed; browser-only changes need a page reload. A change that needs a new `mc` verb or a new manifest entry needs `redeploy.sh`.

### dev-reset.sh and uninstall.sh

`dev-reset.sh` with no flags prints its plan. With `--confirm`, it copies `refresh-grants`, `runtime-auth-sources`, `config.toml`, and `routing.md` to a safe-hold. It then boots out the units, removes stray pipeline containers by label, runs `mc __reset --confirm`, and re-provisions with development lease tunables. `--rebuild` first rebuilds host `mc` and the `mc-prod` image at `HEAD`. This matters because activation refuses when the two carry different commits. `--smoke` runs the full-pipeline ride-through.

`uninstall.sh` builds or locates an `mc`. It runs `mc __uninstall --dry-run`, then `--confirm` with the selected mode. `--full-wipe` is the default. It deletes Culler data, task branches, credentials, and review files, and keeps Project repositories. `--keep-data` removes software and keeps deployment data for a later reinstall. Both modes remove the host Bun under `MC_HOME/bun/`. They leave Docker, Git, mise, Go, and the operator's Bun installed.

## mc/deployment

- **Release manifests.** `hostReleaseFiles` in `host_release.go` and `runnerReleaseFiles` in `runner_release.go` list the exact shipped files. `InstallHostRelease` and `InstallRunnerRelease` copy them into `MC_HOME/release` through a staging directory. `ValidateHostRelease` walks the installed tree. It rejects any unexpected or missing file, any symlink, any file not owned by the operator, and any mode other than 0700 for directories or 0600 for files. A new resident, dashboard, or runner file that is not in the manifest fails at activation. The closure tests (`host_release_closure_test.go`, `runner_release_closure_test.go`) read files outside the module, so `mc/check.sh` runs `./deployment/` with `-count=1`. See [the dashboard](../architecture/dashboard.md) for the three-edit rule.

The host release lists the dashboard fonts and their license notices. The dashboard server maps their URLs to the shipped files. `TestHostReleaseManifestShipsNothingUnreachable` checks that every served dashboard file appears in the host release. It also checks that each shipped file exists and has a caller or server route.
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

- `fast-suite-macos` runs every component `check.sh` on `macos-15`. It fails a non-`mc` component whose log shows zero successful tests. It then runs `./release/check.sh`, `release/build.sh`, and `release/validate_package.py` and uploads the release inputs with their digest.
- `deployment-linux` runs the portable `./deployment/` tests and the Linux filesystem-guard tests on `ubuntu-24.04-arm`.
- `dashboard-browser` runs the complete browser suite on Linux arm64 for pull requests and pushes to `main`. It uses the Project image Playwright lock file and matching Chromium.
- `security-acceptance` checks the real Trivy scanner with controlled advisory fixtures. No job waits for it.
- `release-images` builds both images with read-only permissions. It checks product behavior, inspects layers, and saves each checked archive.
- The image checks verify file contents and write a fresh install and update size report. See [image delivery and update cleanup](image-delivery.md).
- `image-vulnerabilities` reports Trivy vulnerability results for the saved archives. No job waits for it.
- `release-checks` requires the fast suite, deployment, browser, and image jobs to succeed.
- `product-image` runs only on a push to `main`. It checks and loads the saved archives before registry upload, then records both immutable digests.
- `release-downloads` runs only on a push to `main`. It uses `release/publish.py` under serialized publication control.

The publication jobs require `dashboard-browser` from the same workflow run.
Both `product-image` and `release-downloads` list it in `needs`.
The read-only `release-images` job depends on the fast suite and portable deployment checks.
The browser job runs independently.
The browser job holds read-only repository permission and receives no provider credentials.
It retains the suite log, JUnit report, and screenshots in `culler-dashboard-browser-<attempt>` for seven days.
See [browser checks](../tests/checks-and-test-lanes.md#required-browser-checks) for local execution and failure rules.

`release/build.sh` requires a clean tree at `CULLER_COMMIT`. It verifies the pinned AgentsView archive, cross-compiles `mc` for darwin and linux arm64 with the commit stamped in, and assembles the package and image inputs outside the checkout. `publish.py` creates a draft `culler-<commit>` release and uploads `install.sh`, `installer.sh`, the package, and `release.json` with all five checks. It publishes only if the commit is on `main` and strictly ahead of the available release. So an older workflow never replaces a newer release, and a published release is immutable. `release/check.sh` runs the Python unit tests for these tools locally, without credentials. It fails when it selects zero tests.

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
Candidate Project images receive a compatibility check before they can access the spine.
The check examines a stopped container archive without executing candidate programs.
See [Project environments](../workflows/project-environments.md).

Codex and Claude Code connections use sign-in that Culler starts in separate home and credential folders.
`mc/deployment/runtimeauth_setup_login.go` launches the detected agent program in that isolated environment.
The operator's agent login remains separate from Culler's login.
Setup tests the selected connection before service activation.
The host retains provider credentials in macOS Keychain.
Each agent receives a temporary Culler token and a public proxy certificate.
Static API keys use the same host boundary.
`runtimeauth_signin_test.go` and the terminal setup tests cover connection creation.
`runtimeauth_host_sources_test.go` checks cleanup after a credential directory changes during sign-in. Its fixture restores directory write access before it renames the sealed directory, so the rename works on macOS 15.

The release defines the supported provider authorities.
Configuration validation rejects custom provider destinations and unsupported credential kinds.
The host renews account access while the container retains its Culler token.
A new Codex setup profile uses the native default model during its connection test.
After success, setup saves the selected model for later runs.
See [Agent connections and access renewal](agent-connections.md) for profiles, grant storage, and failure behavior.

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
