# Install and update Culler

Culler supports Macs with Apple silicon.
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
This also updates a production installation that used a source checkout.
A saved launcher supports `sh /tmp/culler-install.sh` and `sh /tmp/culler-install.sh --check-updates` while that file exists.

For a different deployment home, pass `--mc-home /absolute/path` to the saved launcher.
The dashboard provides **Check for updates** and **Open installer** under **System health**.
**Open installer** opens Terminal and uses the same script.
The installer asks before service activation.

Each install or update invocation fetches the current installer for one complete release.
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

The installer waits for active work before service replacement.
An incompatible database blocks activation.
The installer never authorizes a database reset.
A healthy deployment of the available release requires no service restart.

After an interruption, run the same script again.
The installer retains valid setup choices and reports the active release.
Release availability remains separate from service health.

## Lifecycle commands

The command-line interface (CLI) controls the local Culler lifecycle.
Configure Culler in the dashboard or through Homie.
Help, status, and version repeat this boundary.

| Command | Result |
| --- | --- |
| `culler help` | Show the commands and their options. |
| `culler status` | Show current service checks and saved stop choices. |
| `culler version` | Show the full command and installed release identities. |
| `culler start [service]` | Start the selected services. |
| `culler stop [service]` | Stop the selected services safely. |
| `culler restart [service]` | Restart the selected services. |
| `culler update --check` | Check release availability. |
| `culler update` | Open the same installer in this terminal. |
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

## Release files

GitHub Releases stores these files for one commit:

- `install.sh`, the permanent launcher.
- `installer.sh`, the installer for that release.
- `culler-darwin-arm64.tar.gz`, the application package.
- `release.json`, the release record.

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
Each release uses one full commit identifier and an immutable image digest.
The workflow verifies each release file after upload.
The workflow makes the release available only after all required checks pass.
An incomplete release preserves the previous available release.
An older workflow preserves a newer available release.

The repository's Actions page shows each workflow and its result.
The Releases page shows complete releases.
After the first complete release, the test Mac can download the launcher and complete setup.
Later pushes use the same release workflow.
Later updates use the same saved launcher.

The coding harness can run `./release/check.sh` to check the release tools locally.
That check uses local fixtures and requires no publication credential.

The [release security guide](release/security/README.md) describes the security scans and the nightly image scans.
It also describes the required GitHub ruleset.
The repository owner must activate the required ruleset before a pull request can merge.
A failed or unverified required check prevents release publication.

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
