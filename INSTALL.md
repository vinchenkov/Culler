# Install and update Culler

Culler supports Macs with Apple silicon.
Culler is in early access. The [version rules](#versions) describe what a 0.x version means.
A published release supplies the executable, runtime files, and container image.
The release also supplies AgentsView, the trace viewer of the `kenn-io/agentsview` project.
The Mac requires no Culler checkout or Go compiler.

## Install the public release

Run this command in Terminal on a Mac with Apple silicon:

```sh
curl -fsSL https://raw.githubusercontent.com/vinchenkov/Culler/main/install.sh | bash
```

The Culler repository and its release downloads are public.
Public installation requires no GitHub account or GitHub token.
The launcher selects the latest complete GitHub release.
It downloads and verifies the installer for that release.
The command installs the published release, even when you run it from a source checkout.

To save the launcher for later use, run these commands:

```sh
curl -fsSL https://raw.githubusercontent.com/vinchenkov/Culler/main/install.sh -o /tmp/culler-install.sh &&
sh /tmp/culler-install.sh
```

The installer can also use an existing GitHub credential.
It first checks `CULLER_GITHUB_TOKEN_FILE`, or `~/.config/culler/github-token` when that variable is absent.
Without an explicit token path or a default token file, it checks the GitHub command tool.
A token file must contain only the token, belong to the operator, and use mode `600`.
An invalid configured credential can stop installation even when the release is public.

## Setup and updates

The installer prepares the runtime and guides service setup.
The same command installs `culler` and opens the terminal user interface (TUI) for setup.
The pipe supplies the script, and the terminal supplies your answers.
Setup installs `culler` beside the runtime executable, normally in `~/.local/bin`.
The `--bin-dir` option selects another absolute directory.
Setup preserves an unrelated executable with the same name and reports the conflict.
Setup checks command access in new zsh and bash shells.
The final screen gives an exact command when the current shell needs a path update.
The primary final action remains **Open dashboard**.
Setup installs the pinned AgentsView executable at `MC_HOME/bin/agentsview`.
Setup verifies the size and SHA-256 digest of that executable before the installation.
A digest mismatch stops the installation, and the message names AgentsView and both digests.

The installer downloads the Bun release archive that the release record names.
The installer verifies the SHA-256 value of that archive against the release record.
A checksum mismatch stops the installation, and the message names both values.
The installer places the host Bun at `MC_HOME/bun/<version>/bin/bun`.
The resident and the dashboard run the host Bun.
The installer requires no mise and leaves a mise installation of the operator unchanged.
An update to another Bun version removes the previous host Bun after the activation completes.
Both uninstall modes remove the host Bun with the other Culler software.

Apple tool installation and Docker Desktop can require operator actions.
The installer names each action and the command to resume.
The operator authorizes service activation in the terminal.

Setup offers native ChatGPT and Claude account sign-in.
Setup also accepts an OpenAI or Anthropic application programming interface (API) key.
Setup also offers healthy connections that Culler already holds.
Culler keeps each login separate from the native login of the operator.
Sign-in completion and model verification remain separate results.
A failed model test keeps the saved connection available.
The retry uses that connection without another sign-in or key entry.
The dashboard keeps connection, model selection, and model verification in three steps.

Culler stores provider credentials in the macOS Keychain.
Each container receives a temporary Culler token and the public proxy certificate.
The host proxy attaches provider credentials to permitted model requests.
The host renews account tokens and saves each replacement in the Keychain.
Both request connections use HTTPS.
A locked Keychain requires host recovery; it does not require another account sign-in.
Unattended operation requires the operator's macOS user session and an unlocked login Keychain.
After a computer restart, macOS must restore those conditions before Culler can use provider connections.

For a new Codex profile, setup lets Codex choose its default model.
After the connection test succeeds, setup saves the selected model name.
Later runs use that saved model.
The dashboard can select another model through the profile's Edit form.

After installation, apply the latest complete release with this command:

```sh
culler update
```

Check release availability without a deployment change:

```sh
culler update --check
```

If the `culler` command is unavailable, run the public installation command again.
Public updates require a published installation with a declared public compatibility baseline.
An internal source installation requires a separate deployment home for the first public release.
A saved launcher supports `sh /tmp/culler-install.sh` and `sh /tmp/culler-install.sh --check-updates` while that file exists.

For a different deployment home, pass `--mc-home /absolute/path` to the saved launcher.
The dashboard provides **Check for updates** and **Open installer** under **System health**.
**Open installer** opens Terminal and uses the same script.
The installer asks before service activation.

Each new update fetches the current installer for one complete release.
Recovery uses the exact retained candidate before it requests another release.
The installer verifies download checksums and the container image digest.
An incomplete release never replaces the available release.
An update preserves these resources:

- The configuration and credentials.
- The Project repositories and worktrees.
- Compatible database records.
- Project pause choices.
- Explicit service stop choices.
- The AgentsView archive, its trace copies, and their display names.
- The AgentsView token and the wiki key.

The installer warns before it stops services and interrupts active runs.
After confirmation, the stop phase has a thirty-second deadline.
An agent run cannot delay that deadline.
An interrupted Project command can leave a partial file.
The update preserves that file and saved native history.
An incompatible database blocks activation.
The installer never authorizes a database reset.
A healthy deployment of the available release requires no service restart.

After an interruption, run `culler update` to retry the exact candidate.
Use `culler update --restore` to restore the previous software and checkpoint before commit.
An incomplete update keeps ordinary work stopped.
The updater verifies the candidate before it records a commit and permits work.
After commit, cleanup removes previous software and temporary checkpoints.
Cleanup preserves selected Project images and separate user backups.
An ordinary downgrade remains unsupported.
Release availability remains separate from service health.
The [update procedure](release/UPDATES.md) describes the public contract and recovery checks.

## Lifecycle commands

The command-line interface (CLI) controls the local Culler lifecycle.
Configure Culler in the dashboard or through Homie.
Help, status, and version repeat this boundary.

| Command | Result |
| --- | --- |
| `culler help` | Show the commands and their options. |
| `culler status` | Show current service checks and saved stop choices. |
| `culler version` | Show the versions of the command and the installed release. |
| `culler start [service]` | Start the selected services. |
| `culler stop [service]` | Stop the selected services safely. |
| `culler restart [service]` | Restart the selected services. |
| `culler update --check` | Check release availability. |
| `culler update` | Open the same installer in this terminal. |
| `culler update --restore` | Restore an incomplete update before its commit. |
| `culler uninstall` | Preview removal of all Culler installations, then ask for confirmation. |
| `culler uninstall --keep-data` | Remove software and preserve Culler data after confirmation. |
| `culler uninstall --dry-run` | Show the removal plan without a change. |
| `culler logs [service] --tail 100` | Show recent service diagnostics and exit. |

The service names are:

- `resident`
- `dashboard`
- `agentsview`

Start, stop, and restart select all services when you omit the service name.
Logs select the resident when you omit the service name.
A dashboard restart preserves the resident and active agents.
A resident stop blocks new work and waits up to three minutes for active work.
An unsafe stop fails.
The command attempts to restore the previous service state and reports the current service checks.
An explicit stop persists after login and computer restart.
An explicit start or restart clears that stop choice.

Use `--mc-home /absolute/path` to select another deployment for commands other than uninstall.
The option overrides `MC_HOME`; the default deployment is `~/.culler`.
Use `--no-color` for plain text.
The `NO_COLOR` variable also disables color.
Redirected output contains plain text.

The exit codes are:

- `0`: The command completed.
- `1`: The operation failed or a required check remains unavailable.
- `2`: The command, service, or environment is invalid.
- `130`: The operator cancelled the command.

## Uninstall Culler

`culler uninstall` uses the installed executable and requires no checkout or Go compiler.
The command removes all detected Culler installations, including old Mission Control installations.
It shows the removal plan before confirmation.

The default mode is `--full-wipe`.
This mode removes these resources:

- Culler software and its commands.
- Culler data and credentials.
- Culler task branches and review files.

Type `wipe` to confirm this mode.
Use `--keep-data` to preserve configuration, credentials, and history for reinstallation.
Type `uninstall` to confirm that mode.
Reinstall at the same deployment paths to reuse retained data.

Both modes stop active agents.
Removal can discard unfinished work.
Both modes preserve Project repositories and shared host tools.
Culler removes only its own command files and shell entries.

Use `--dry-run` to inspect the plan without removal.
Press Enter without text or press Ctrl-C to cancel the confirmation.
A command without a terminal requires `--yes` to authorize removal without a prompt.

The uninstall command rejects `--mc-home` because removal covers all detected installations.
`MC_HOME` includes an additional deployment; it does not restrict removal to that deployment.
The command accepts these additional paths for recovery:

- `--home /absolute/path`: Include another deployment.
- `--workspace /absolute/path`: Include another Project.
- `--binary /absolute/path/mc`: Include a custom runtime executable.

Each path option accepts repeated uses.
The command reports each failure and returns a nonzero exit code after incomplete removal.

## Versions

Culler is in early access, and the major version 0 states this.
Each release has a version in the form MAJOR.MINOR.PATCH, for example `0.1.0`.
`culler version`, `culler update --check`, setup, and the dashboard show the version first and the short commit second.
For example, they show `0.1.0 (1a2b3c4d5e6f)`.

These rules apply before version 1.0.0:

- A compatible fix increases the patch number, for example from 0.1.0 to 0.1.1.
- A feature or an incompatible change increases the minor number, for example from 0.1.1 to 0.2.0.
- Version 1.0.0 declares a stable public contract. After version 1.0.0, an incompatible change increases the major number.

Version 0.1.0 starts the public data compatibility contract.
Every supported update preserves durable user data, including updates between 0.x minor versions.
Consecutive public releases require a supported update path.
Skipped releases require accepted source formats and a supported source version.
An ordinary downgrade remains unsupported.

[CHANGELOG.md](CHANGELOG.md) has one section for each version.
Each section gives the changes, the known limits, the database compatibility, and the update effects.
Read the section of a new version before you update.

## Release files

GitHub Releases stores these files for each version:

- `install.sh`, the permanent launcher.
- `installer.sh`, the installer for that release.
- `culler-darwin-arm64.tar.gz`, the application package.
- `culler-opencode-linux-arm64.tar.gz`, the OpenCode program and its web search plugin.
- `release.json`, the release record.

The release record holds the version, the commit, and the tag of the release.
The release tag is `v<version>`, for example `v0.1.0`.

GitHub Container Registry stores the matching container image.
The release record identifies its exact digest.
The release record also lists the AgentsView version and the digests of the AgentsView release archive and executable.
The release record also names the host Bun version and the SHA-256 value of its archive for a Mac with Apple silicon.
The release archive contains only the declared runtime files and executables.
The release archive holds the verified AgentsView executable and the AgentsView license text.
Published image layers exclude Go build caches, Go build settings, and operator state.

The release tools record publication duration and local update duration.
Build duration depends on the required checks and available caches.
A commit becomes installable only after complete publication.

## Publish releases

The repository owner publishes Git changes.
A push to `main` starts `.github/workflows/release.yml` for that commit.
GitHub Actions must be enabled for the repository.
The workflow uses GitHub's temporary `GITHUB_TOKEN` for release files and container images.
The test Mac can download public releases without a GitHub credential.

The package build downloads the AgentsView release archive that `mc/deployment/agentsview.json` pins.
The build verifies the AgentsView release archive and executable against that pin before it packages the executable.
Only a change to that pin changes the AgentsView version of a release.
The build also verifies the Bun release archive that `mc/deployment/hostbun.json` pins.
Only a change to that pin changes the host Bun version of a release.

The workflow checks these release inputs:

- The full fast suite on macOS.
- The portable deployment tests on Linux.
- The complete dashboard browser suite in real Chromium on Linux.
- The application package and its production binaries.
- The product image and every image layer.
- The file contents and the sizes of both images.
- The uploaded release files and the container image download.

The workflow also scans both images for known dependency vulnerabilities.
GitHub secret scanning checks the repository for secrets.
These scans report their results and never prevent a release.

The checks exercise the product image without a mounted Culler checkout.
Each release uses one version, one full commit identifier, and an immutable image digest.
The `VERSION` file supplies the public version.
The minimum source version and accepted source formats reside in `mc/distribution/update_policy.json`.
Publication rejects an undeclared path from the previous public release.
Spine, configuration, and credential conversions require converter code before publication.
The workflow verifies each release file after upload.
The workflow makes the release available only after all required checks pass.
An incomplete release preserves the previous available release.

### Versions and release notes

The `VERSION` file holds the version of the next release.
A push to `main` publishes a release only when `VERSION` holds a version above the version of the latest release.
With the same version, the workflow runs the checks and publishes no release.
A lower version fails the `release-version` job, and the workflow publishes no release.
The release name is `Culler <version>`.
The release description is the `CHANGELOG.md` section of that version.
The workflow never marks a release as a prerelease, because Culler installs only releases that are not prereleases.

To publish a release, make one change with these edits:

1. Increase the version in `VERSION` by the [version rules](#versions).
2. Add a `## <version>` section at the top of `CHANGELOG.md`.
3. Include `### Changes`, then `### Known limits`.

Version 0.1.0 requires only these two parts.
For later releases, add these parts after `Known limits`, in this order:

- `### Database compatibility`.
- `### Update effects`.

The pull request checks reject an absent or malformed `VERSION` file.
They also reject a version without a complete `CHANGELOG.md` section.

Before a version increase, compare the database schema of the new version with the `schema` value in the `release.json` of the latest release.
Ask your coding harness to make this comparison.
A different schema blocks publication until its conversion and recovery tests pass.
An accepted-format declaration alone cannot enable a conversion.
Use the Database compatibility and Update effects parts to describe preserved data and required intermediate releases.
Before publication, test the exact packages on a separate Mac or isolated deployment.
Confirm the update and recovery results that those parts state.

The repository's Actions page shows each workflow and its result.
The Releases page shows complete releases.
After the first complete release, the test Mac can download the launcher and complete setup.
Later pushes use the same release workflow.
Later updates use the same saved launcher.

The coding harness can run `./release/check.sh` to check the release tools locally.
That check uses local fixtures and requires no publication credential.
It also checks `VERSION` and `CHANGELOG.md`.

The [release security guide](release/security/README.md) describes the security scans and the nightly image scans.
It also describes the GitHub ruleset of the `main` branch.
The [update procedure](release/UPDATES.md) describes dependency changes, migration evidence, and emergency recovery.
The [provider support contract](release/PROVIDER-COMPATIBILITY.md) defines saved-model checks and repair behavior.
With that ruleset, a pull request can merge only after its `release-checks` job passes.
A failed or unverified required check prevents release publication.

## Source installation

Run the source installer from the repository root:

```sh
sh scripts/install-source.sh
```

The source installer builds Culler from the checkout and opens the same setup wizard as the published installer.
Both paths check prerequisites, configure agent connections, request service activation, and verify dashboard access.
The source installer uses the Go and Bun versions from `.mise.toml` through mise.
It builds the agent image locally and downloads the pinned AgentsView executable.
The published installer downloads verified release files and images instead.

Run the source installer again to resume an incomplete source installation.
Plain `./install.sh` selects the published release, even inside a source checkout.

Use `./redeploy.sh` to deploy the checkout over an existing production installation.
That command preserves the database and stops when the database schema differs.
Published release updates use `culler update` and retain their separate recovery procedure.

## Development installation

Development installation retains its separate command:

```sh
./install.sh --dev --mc-home /absolute/scratch/path
```

That command requires a checkout.
The local deployment scripts retain their separate purpose.

### Other API providers

**Use another provider's API key** selects an OpenCode connection.
The provider search accepts a name, identifier, or full model address.
Culler sends keys only to providers in its release catalog.
The provider rows show the address and website before the concealed key prompt.
A release integration keeps its existing harness when its address matches a catalog entry.

The first OpenCode connection starts a separate download of the pinned harness and web search plugin.
The model and effort lists remain available during the download.
The agent test waits for the download.
A failed download keeps the connection and its key and offers **Retry**.
The model list shows the context limit and prices for each million input and output tokens.
**Default** uses the provider default; each other effort selects a model variant.
The page states whether agents can search the web.

An installation without an OpenCode connection downloads no OpenCode program.
An update replaces an existing OpenCode volume with the archive of that release.
Both uninstall modes remove the OpenCode volume.
A local source deployment obtains the pinned packages from its unchanged source commit when the first connection requires them.
