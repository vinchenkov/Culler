# Project image acceptance

This record describes the scratch Docker checks on 2026-10-10.
The checks used the Project image and private Culler runtime from this checkout.
The checks changed no live deployment or service.

## Test machine

| Component | Measured configuration |
| --- | --- |
| Host | Apple M3 Max, Mac15,11 |
| Host processors | 14 physical cores; 14 logical cores |
| Host memory | 38,654,705,664 bytes (36 GiB) |
| Docker virtual machine | Linux arm64; 14 processors |
| Docker memory | 12,528,017,408 bytes (11.67 GiB) |

## Image contents

The default Project image built from its declared Ubuntu digest and package snapshot.
Archive downloads passed their declared SHA-256 checks.
The Project image included these tested tools:

- Node.js 24.18.0 and npm.
- Bun 1.3.9.
- Python 3.14.7 and uv 0.12.3.
- Go 1.25.12 and mise 2026.10.0.
- pnpm 10.32.1.
- The C and C++ compilers, make, CMake, Ninja, and pkg-config.
- Git 2.48.1 and the common command tools.
- Chromium 151.0.7922.10 and its system libraries.

Real mise selected the repository's Go and Bun versions under user 10002.
The Project image contained no Culler control command or private runtime.
The private runtime used separate Node.js and Bun declarations.

| Artifact | Measured size |
| --- | --- |
| Default Project image | 3,433,354,043 bytes (3.20 GiB) |
| Culler service image | 5,903,348,373 bytes (5.50 GiB) |
| Private runtime files | 2,399,694,352 bytes (2.23 GiB) |

Docker reports uncompressed image sizes.
The service image shares the Project image layers.
The image sizes do not represent separate download sizes.
The runtime volume requires additional storage for its files.
The registry download size remains unmeasured because these images came from local builds.

## Observed behavior

| Check | Result |
| --- | --- |
| Project tools | The image ran every declared tool under user 10002. |
| Runtime mount | The agent executed the private Node.js, Bun, and Culler commands through the volume. |
| File protection | The agent could read and execute runtime files; file changes failed. |
| Spine access | The agent used the setuid command; direct database access failed. |
| Command scope | A fixed agent identity could not call a host-only command. |
| Browser | Chromium started under the agent account. |
| Workspace dependencies | One setup agent ran the setup command, ran checks, reported results, and accepted the minimum Project source. |
| Image dependencies | The first agent submitted a Dockerfile. Approval produced an image and one additional setup agent. |
| Validation of Image dependencies | The second agent found the added system file, ran checks, and completed setup. |
| Secret scope | Setup commands received setup secrets. The agent received agent secrets. The brief and output excluded both secret values. |
| Runtime update | One retained derived Project image executed two Culler runtime releases. Its image digest stayed unchanged. |
| Repeated runtime install | A repeated install preserved the completed runtime volume. |
| Image rejection | The host refused an image with a hidden setuid file and a replacement `find` command. |
| File capabilities | The Docker archive retained the capability metadata that the host audit rejects. |
| Complete pipeline | The resident ran a Scribe update and the Worker, Verifier, and Packager flow with the separate Project image. |

The host image audit read the complete archive of a stopped container.
It executed no program from the candidate image.
The build for Image dependencies used an alias for the selected immutable base digest.
Docker resolved that alias to the recorded image before the build.

## Measurements

These checks used cached images and local Docker storage.
Some later checks overlapped other Docker checks.

| Check | Elapsed time |
| --- | --- |
| Mounted runtime and tool checks | 9.60 seconds |
| Product and browser checks | 3.84 seconds |
| Workspace dependencies | 6.97 seconds |
| Image dependencies: setup, build, audit, and validation | 54.14 seconds |
| Runtime update across two releases | 35.44 seconds |
| Additional Dockerfile layer | 0.41 seconds |
| Host audit of the derived image and its base | 50.41 seconds |
| Complete pipeline fixture | 66.96 seconds |

The final fixture for Workspace dependencies completed its first application and environment checks 0.481 seconds after its container start request.
That interval excludes image download, image audit, and runtime volume preparation.

The full initial image build succeeded; this record has no reliable duration for that build.
These measurements describe one machine and one run.
They establish functional evidence, not a performance guarantee.

## Imported repository and package cache

A separate fixture contained an existing TypeScript application with its package lock and tests.
Its dependencies were TypeScript 5.9.3 and Zod 4.1.12.
Each check copied the same committed source into a fresh container with a fresh home directory.
The final Culler workflow helper installed dependencies, checked applicability, built the application, passed its test, and started its command program.
Both containers used the same npm archive cache with a two-gibibyte limit.
The helper checked archive hashes before reuse.
Each home contained its private npm indexes, temporary files, and executable cache.
Only two verified archives remained in the shared cache after each run.

The first container used an empty cache and network access.
The second container used the populated cache with `npm ci --offline` and Docker network access disabled.
Both containers passed all three application checks.

| Measurement | Empty cache | Populated cache |
| --- | --- | --- |
| Dependency installation | 0.797 seconds | 0.483 seconds |
| TypeScript build | 0.347 seconds | 0.333 seconds |
| Application test | 0.113 seconds | 0.104 seconds |
| Application start | 0.089 seconds | 0.080 seconds |
| Complete workflow helper | 1.359 seconds | 1.019 seconds |
| Complete container command | 2.020 seconds | 1.463 seconds |
| Cache file bytes after completion | 5,017,436 bytes | 5,017,436 bytes |

The workflow measurement includes cache checks, dependency installation, applicability commands, application checks, and service cleanup.
The complete container command also includes the source copy and runtime start.
The populated cache reused downloads; each new workspace still received a fresh installation.
These final cache checks ran without another Docker acceptance check.
This small fixture establishes the imported application's baseline.
It does not represent a large repository or a network service.

An earlier direct npm check also passed with an empty cache and an offline populated cache.
That earlier check bypassed the Culler workflow helper.
The table records the final helper checks only.

## Docker test suites

The complete Docker boundary suite passed all 20 cases in 91.04 seconds.
The first complete end-to-end suite passed 15 cases and failed five cases.
Those five cases counted the new image audit within their old scenario deadlines.
The test fixture now waits for resident startup before each scenario deadline starts.
The infrastructure failure case also allows 180 seconds for its three failures and retry delays.
The five focused retries passed in 493.98 seconds.
Every end-to-end case has successful evidence across those two invocations.
This record does not claim one complete successful end-to-end invocation.

## Scope and limits

The setup checks used a fake harness with the real setup runner and real `mc` capability boundary.
They used a scratch spine and synthetic secrets.
They made no model-provider request.
The fixture for Image dependencies added one system file through an ordinary Dockerfile.
It did not test every package installer.

A separate C fixture required the PostgreSQL development header `libpq-fe.h`.
The default image refused that compilation because the header was absent.
The subsequent Ubuntu snapshot query stalled and required termination of its scratch container.
This record therefore claims no successful installation of `libpq-dev`.

The runtime update check changed the Culler release identity.
Both releases used the same private Node.js and Bun versions.
The check proves independent runtime replacement on a retained Project image.
It does not prove compatibility with a future Ubuntu platform.

The default Project image includes a browser and several language tools.
Its size reflects that broad default.
The current audit reads complete image archives, so a first audit adds measurable setup time.
