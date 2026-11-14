# Project images and the Culler runtime

`Dockerfile` builds the default Project image for Linux arm64.
The image contains Ubuntu 24.04 and common Project tools.
The Ubuntu digest and snapshot date fix the system inputs.
Exact archive digests and package locks fix the additional tools.
The image records its installed tools in `/usr/share/culler-project/manifest.json`.
The resident observes the selected image's tools and system packages separately.
Culler records those observations after each image change.

The build steps come in this order:

1. The system packages, with the setuid and setgid bits removed.
2. Git, stripped and without message translations.
3. Python.
4. uv.
5. Node.js.
6. The Project tools and the browser, with stripped browser programs and no `.pak.info` files.
7. The mise program.
8. Go.
9. Bun.
10. The mise links for Go, Bun, Node.js, and Python.
11. The fixed accounts, the mount directories, and the system package list.
12. The image manifest.

Each version pin stands before the first step that reads it.
A pin change rebuilds only the step of that tool and the later steps.
The Go and Bun pins change most often, so the Go step and the Bun step come after the other tool steps.
The mise link step reads every tool pin, so it comes after the Go step and the Bun step.
The mise program step reads only the mise pin.

Each step stores its own files once.
The step that adds a tool sets read and execute permission for all users on those files.
No later step changes the tool files of an earlier step.
The browser step changes the package state and the linker cache of the system.
The accounts step changes the account files of the system.
Each step removes the caches that it writes under `/root` and `/tmp` before it ends.
The image holds no npm download cache and no Node.js compile cache.
The manifest step reads its script through a bind mount, so no layer holds the script.

`runtime.Dockerfile` builds the Culler service image in two stages.
The `tools` stage downloads and installs each runtime part.
Its npm and its package caches stay in that stage.
The `runtime` stage copies the finished parts into `/opt/culler-runtime`.
The parts that change least come first:

1. The private Node.js, without npm and corepack.
2. The browser tools and the full browser build with ffmpeg.
   The runtime holds no headless shell.
   Each browser program holds no symbol table and no debug data.
   The browser folders hold no `.pak.info` files.
3. OpenWiki, without source maps and TypeScript type files.
4. The private Bun and the harness packages.
   The runtime holds one Claude program, the glibc build for Linux arm64, and the Codex program for Linux arm64.
   The install omits the optional packages and the peer packages that no Culler code imports.
5. The runtime source and the control commands.
6. The files that change at each release: `mc-real` with owner `10001:10001` and mode `6755`, the release ID, and the runtime manifest.

Each runtime file has its final owner and mode in the layer that adds it.
The runtime check runs as the agent account and writes no file.
The Node.js and Bun declarations remain separate from Project declarations.
The runtime manifest records the checksum of each runtime file.
The Claude adapter starts the Claude program by its path, `/opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-arm64/claude`.
Each `mc` program for a deployment or a release links with `-w`.
A production `mc` also links with `-X main.releaseBuildID=<commit>`.
The program holds no DWARF debug data and keeps its symbol table, because release verification reads the release build ID from the symbol table.

The runtime volume install runs the embedded program `mc/deployment/runtime_install.js` with the runtime Node.js of the product image after its containers stop.
The program compares the file hashes of the image manifest and the volume manifest.
It writes each changed file as a new file in the same directory and renames it over the old path.
It removes the volume entries that the image lacks.
It removes the release marker first, fsyncs the written files and the changed directories, and writes the marker last.
A volume without a release marker, or a volume whose marker already names the release, gets a hash check of each file.
The volume name contains the deployment identity.
Agent containers receive read and execute access; the mount blocks file changes.
The mounted programs execute inside each agent container.
Project commands use Project tools.
Culler commands use explicit private paths.

Each Project retains its selected image digest through a Culler update.
The service image and runtime volume follow the installed Culler release.
The default image applies to new Projects.
An accepted environment change selects another image for an existing Project.
A published update removes the previous product image and each unused default Project image after it completes.
A Project keeps its selected image and its base.
The resident downloads a removed default Project image again from its recorded registry reference when a build needs it.

The source build scripts produce `mc-project` and `mc-prod`.
Published releases record separate immutable registry references for both images.
Each build uses the `product` target of `runtime.Dockerfile`.
`build.sh` produces the images for the isolated container tests.

A host command checks each candidate image before any spine mount.
It reads the complete archive of a stopped container.
It executes no program from the candidate image.
The check rejects these changes:

- Additional setuid files, setgid files, or file capabilities.
- Changes to protected accounts, mount directories, or platform libraries.
- Changes that redirect the dynamic linker to replacement platform libraries.

A Dockerfile can add development tools and libraries.
It preserves the supported platform files from its selected base.

The checks cover these boundaries:

- `project-smoke.sh` checks the tool inventory and mounted runtime.
- `product-smoke.sh` checks the service image and capability restrictions.
- `browser-smoke.sh` checks the Project browser with the agent account. With `--runtime`, it checks the runtime browser the same way that an agent starts it.
- `toolchain-smoke.sh` builds `mc` from a copy of the checkout with the image toolchain and the agent account.
  The build uses `GOTOOLCHAIN=local` and `GOFLAGS=-mod=readonly` and downloads the Go modules over the network.
  The image Go version must equal the `mc/go.mod` toolchain version.
  The build must leave `go.mod` and `go.sum` unchanged.

Image checks use scratch containers and volumes.
They require no live deployment change.

## Project setup and recovery

The accepted Vision starts a separate setup agent through **Initialize project**.
The setup agent and source acceptance work in a temporary repository at the last commit of the `culler` branch.
The operator checkout stays unchanged until the setup commit reaches the `culler` branch.
The setup agent creates only the files that dependency installation requires.
Its complete report permits source acceptance independently of available build and test capabilities.
A proposal for Image dependencies retains a Dockerfile and its required files for operator approval.
Culler builds the approved image and checks runtime compatibility before the continuation uses it.
The continuation assesses the tools and submits `complete` with accurate limits.
Denial starts a continuation in the exact current image.
The initial file snapshot survives setup retries.
The denial continuation restores original build files and excludes the denied changes.
Further image improvements use the Environment page after import.
Repository requirements still control later task verification.

The Environment page stores the Project configuration and secret references.
Secret values remain in protected deployment files under `project-secrets/<project>/<reference>`.
The operator's coding harness supplies those files when the Project requires credentials.
The resident supplies each value only to its declared setup or agent scope.

Setup Agent runs and pipeline runs share one persistent Project home at `/home/agent` through `HOME`.
The home retains normal dependency caches, installed tools, and Project tool configuration.
Each Project uses separate storage from other Projects and the operator's home.
Collie retains its separate home and session rules.

The Worker, Verifier, Packager, and Scribe receive a fresh source workspace at the assigned commit.
The Strategist and Arbiter read a fresh committed projection of the `culler` branch.
The declared setup command recreates workspace dependencies before each Worker, Verifier, and Packager starts.
The Scribe, Strategist, and Arbiter omit automatic dependency preparation.
The package manager checks retained dependencies against current declarations and the selected image.
An image change preserves stored files but can require dependency replacement.

A complete local cache can permit preparation without network access.

The Project also shares `/cache` across Setup Agent runs and pipeline runs.
This disposable storage supports arbitrary package archives, metadata, extracted files, and executables.
Normal package manager paths within the home require no Culler configuration.
An explicit path outside the home persists only when it names persistent Project storage.
Retained setup configuration describes each required explicit path.

Credentials, sessions, role skills, and role instructions remain separate for each run.
The runner supplies role skills at `~/.agents/skills/` through storage that belongs to that container.
Concurrent roles preserve each other's files without a Project-wide execution lock.
The private Culler runtime retains read and execute access without write access.

Restarts, updates, redeploys, and image changes preserve the Project home and `/cache`.
The Environment page reports their disk use separately and provides removal of disposable cache storage.
Automatic cache eviction and manual cache removal preserve the Project home and source repository.
Cache removal remains unavailable during active Project operations.

The keep-data uninstall preserves Project homes, cache storage, and derived images.
The full wipe removes deployment-owned Project homes, cache volumes, and derived image tags.
Both modes preserve the operator's home, Project repositories, and shared host tools.

The resident records each published base's immutable registry reference.
A retry can restore that exact base without changing the Project selection.
A missing derived image requires a build from its retained approved Dockerfile and current source checks.
A missing local base requires its saved image archive before a retry.
Recovery preserves the selected base and any later configuration draft.

## Dependency reuse acceptance

Run this check from the repository root:

```sh
./resident/dependency-reuse.sh
```

The check requires these prerequisites:

- Docker.
- Local `mc-prod` and `mc-project` images.
- The repository's mise toolchain.
- Network access for the initial fixed dependency downloads.

`CULLER_ACCEPT_RUNTIME_IMAGE` selects another Culler service image.
`CULLER_ACCEPT_PROJECT_IMAGE` selects another Project image.
The host downloads `is-number` 7.0.0 and `idna` 3.10 once per invocation.
A local package source supplies them to the Setup Agent.
Worker containers receive neither that source nor external network access.

The check uses the real resident launch functions and agent runners with a fake harness.
The real host command creates mode-0700 Project homes and checks mount identities.
Fake `mc` control replies and repository allocation isolate the check from live control state.

## Setup review attempt acceptance

Run this check from the repository root:

```sh
./resident/setup-review-attempt.sh
```

The check has the prerequisites and the image variables of the dependency reuse acceptance. It needs no network access.

The check proves this sequence with real containers and a fake harness:

1. A first setup agent attempt reaches the attempt limit during a test command. Culler writes the limit record and stops the container.
2. A review attempt reads the trace copy, the command log, and the limit record of the first attempt through read-only mounts.
3. The review attempt submits a report. The report states the stopped command, its completed work, and the time between its last output and the limit.

The first attempt starts 45 seconds before its attempt limit, so the check takes approximately two minutes.
The check uses scratch repositories, containers, images, and volumes without provider credentials.
The parent process removes those resources when the check ends.
The live deployment remains unchanged.

The check verifies these behaviors:

- Offline npm, Bun, and uv installation in fresh Worker workspaces.
- Arbitrary home files, executable permissions, symbolic links, and additional cache files across container removal.
- Concurrent Setup Agent, Worker, and Scribe role files, including preservation after Scribe removal.
- Dependency reuse after a new host process starts with another Project image.
- Separate Project storage and truthful preparation failure evidence.
- Retained npm archive reuse and Project home preservation after cache removal.

The image change adds a label while it preserves the toolchain.
Package managers verify the fixed dependency declarations during each installation.
The separate control, credential, image compatibility, and deployment checks remain necessary.
The fast suite keeps this Docker check separate.

## Setup and runtime acceptance

The Go checks also cover the real setup runner and runtime replacement.
These checks use the `docker_boundary` build tag.
The setup checks accept these image variables:

- `CULLER_PROJECT_TEST_IMAGE`: the default Project image.
- `CULLER_RUNTIME_TEST_IMAGE`: the complete Culler service image.

From `mc/`, this command tests setup, approval, and denial:

```sh
mise exec -- go test -tags docker_boundary -count=1 -v -timeout 10m ./verbs \
  -run '^TestEnvironment(SetupRunnerUsesMountedRuntime|ImageDependenciesResumeWithOneAdditionalAgent|DeniedImageImportsWithCurrentImage)$'
```

The runtime replacement check also requires `CULLER_RUNTIME_NEXT_TEST_IMAGE`.
Its Project image requires the fixture file for Image dependencies, `/usr/local/share/image-dependency-proof`.
The two runtime images require different release identities.

```sh
mise exec -- go test -tags docker_boundary -count=1 -v -timeout 10m ./deployment \
  -run '^TestRuntimeUpdatePreservesProjectImageDocker$'
```

[The acceptance record](ACCEPTANCE.md) contains the measured results and test limits.
