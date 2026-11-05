# Install and update Culler

Culler supports Macs with Apple silicon.
A published release supplies the executable, runtime files, and container image.
The release also supplies AgentsView, the trace viewer of the `kenn-io/agentsview` project.
The Mac requires no Culler checkout or Go compiler.

## Private repository access

The GitHub account requires access to the repository and its container package.
The installer checks both resources separately.
GitHub Container Registry requires a classic token with `read:packages` for private images.
Private repository files require `repo` with that token.
The [GitHub registry guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry) describes these permissions.

Create a classic token with `repo` and `read:packages` access.
Save that token in a private file.
The default path is `~/.config/culler/github-token`.
The file must contain only the token and use mode `600`.
The operator must own the file.
The coding harness can prepare the file without secret values in commands or logs.

With that file, `curl` can fetch the private launcher:

```sh
{
  printf 'header = "Authorization: Bearer '
  tr -d '\r\n' < "$HOME/.config/culler/github-token"
  printf '"\n'
} | curl -q --config - --fail --location \
  --header 'Accept: application/vnd.github.raw+json' \
  'https://api.github.com/repos/vinchenkov/Culler/contents/install.sh?ref=main' \
  --output install.sh
sh install.sh
```

The token travels through standard input.
The command contains no token value.
The installer reuses the file for updates.
An expired credential requires renewed access.
The installer keeps the current deployment after an access failure.

The installer can also reuse a classic token that the GitHub command tool already stores.
That token requires the same repository and package access.
With that credential, the initial download can use this command:

```sh
gh api --header 'Accept: application/vnd.github.raw+json' \
  'repos/vinchenkov/Culler/contents/install.sh?ref=main' > install.sh
sh install.sh
```

## Public repository access

Public distribution supports this command without a credential:

```sh
curl -fsSL https://raw.githubusercontent.com/vinchenkov/Culler/main/install.sh | bash
```

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

Run the same saved script to apply the latest complete release:

```sh
sh install.sh
```

For a production setup that used a repository checkout, download the launcher once from the address above.
Later updates can reuse that launcher.

Check release availability without a deployment change:

```sh
sh install.sh --check-updates
```

For a different deployment home, add `--mc-home /absolute/path`.
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

Use `--mc-home /absolute/path` to select another deployment.
The option overrides `MC_HOME`; the default deployment is `~/.culler`.
Use `--no-color` for plain text.
The `NO_COLOR` variable also disables color.
Redirected output contains plain text.

The exit codes are:

- `0`: The command completed.
- `1`: The operation failed or a required check remains unavailable.
- `2`: The command, service, or environment is invalid.
- `130`: The operator cancelled the command.

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
The test Mac uses its separate private download credential.

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
- The source history, release files, and both images for likely secrets.
- Both images for known dependency vulnerabilities.
- The uploaded release files and the container image download.

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

The [release security guide](release/security/README.md) describes the local secret check before source publication.
It also describes the required GitHub ruleset and the nightly image checks.
The repository owner must activate the required ruleset before a pull request can merge.
An unresolved secret, high or critical vulnerability, or unverified required check prevents release publication.

## Development installation

Development installation retains its separate command:

```sh
./install.sh --dev --mc-home /absolute/scratch/path
```

That command requires a checkout.
The local deployment scripts retain their separate purpose.
