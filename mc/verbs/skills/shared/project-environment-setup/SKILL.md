---
name: project-environment-setup
description: Inspect Project prerequisites, install permitted dependencies, and assess the available development capabilities.
---

# Project environment setup

Follow the setup directive's ordered checklist during Project setup.
Other roles apply this procedure within their assigned command surface and task authority.

## Execution environment

Setup and pipeline agents share container permissions, resource limits, network policy, and workspace paths.
Role authority and credentials remain separate.
Culler keeps provider credentials on the host.
This container uses a temporary Culler token through the host provider proxy.
The host renews account access without a credential update inside the container.
Preserve the proxy configuration and exclude Culler tokens from output.
Each agent container has a limit of 2048 processes.
The operator can set CPU and memory limits; by default, no CPU or memory limit applies.
Read the effective limits from the cgroup files, such as `/sys/fs/cgroup/cpu.max` and `/sys/fs/cgroup/memory.max`.
Agent containers use the default Docker seccomp profile, which denies user namespaces.
Fake harness containers have no network access.

Culler runs Project code in a Linux container inside a Linux virtual machine on the Mac host.
The container has no host command executor or access to the host Docker daemon.
Confirm the execution environment before installation:

- The container distribution and processor architecture.
- The effective permissions, writable mounts, and available services.
- The resource limits, command deadlines, and network access.

Use supplied configuration and permitted local observations; state unknown limits.

## Project dependency storage

The Project home at `HOME` persists across Setup Agent runs and pipeline runs.
It retains package archives, metadata, extracted files, installed tools, and Project tool configuration.
Normal package manager cache paths within the home need no Culler configuration.
The Project home remains separate from the operator's home and other Projects.
Credentials, sessions, role skills, and role instructions belong to each run.
Preserve those files and the files of concurrent runs.

The Worker, Verifier, Packager, and Scribe receive a fresh source workspace at the assigned commit.
The Strategist and Arbiter read a fresh committed projection.
The runner executes the retained setup script before each Worker, Verifier, and Packager starts.
The Scribe, Strategist, and Arbiter omit automatic dependency preparation.
The script recreates workspace dependencies such as `node_modules` from repository declarations.

Retain commands that permit repeated execution against the persistent Project home.
Use the package manager's checks for current declarations and the selected image.
An image change preserves stored files but can require dependency replacement.

The Project also shares `/cache` across Setup Agent runs and pipeline runs.
This directory permits arbitrary package archives, metadata, extracted files, and executables.
Cache removal can remove its contents; the Project home survives that action.
An explicit path outside the home persists only when it names persistent Project storage.
Describe each required explicit path in the retained setup configuration.
Keep installed tools and Project tool configuration in the Project home when they must survive cache removal.

A complete local cache can permit installation without network access.
An absent dependency can require network access.
Report actual installation results separately from build and test results.
Keep an unavailable command unverified.

## Project prerequisites

Inspect the available evidence before a change:

- The accepted Vision and Project documentation.
- Reference files and supplied media.
- Dependency manifests and lockfiles.
- Tool version declarations.
- Repository commands and continuous integration workflows.
- Dockerfiles and required services.

For a new Project, create the minimum files that dependency installation requires.
Preserve reference files and media.
Leave application features for ordinary development.

For an imported Project, preserve the architecture and declared versions.
Inspect each package root and its applicable commands.
Identify required build, test, and other development commands with their prerequisites.
Distinguish routine checks from commands that require separate authorization or infrastructure.
A Project without tests still requires an assessment of its applicable development commands.
Separate environment failures from application failures.
Use application start checks only when applicable code or a required service exists.

## Workspace dependencies and Image dependencies

Workspace dependencies install with ordinary agent permissions in the workspace or other permitted container locations.
Prefer Workspace dependencies when they preserve the Project's required versions and workflow.
Use repository declarations and lockfiles for repeatable installation.
Compatible runtimes and compilers can be Workspace dependencies.
Keep ordinary installed dependency directories outside commits.
Retain the installation command in the setup script when a fresh workspace requires it.
Confirm dependency reuse through the Project home or an explicit persistent path.
Files in a disposable workspace or container directory do not persist across runs.

Image dependencies require changes to the Project image.
They can include system tools, libraries, or system configuration that requires protected locations.
Prepare a standard Dockerfile and the necessary build files.
Use at most two short sentences for the operator's proposal explanation.
Identify the image change and why the Project needs it.
Keep check evidence, diagnostic output, and continuation instructions separate from that explanation.
Use short Markdown paragraphs and lists for separate facts in other operator text.
Use `FROM culler-project-base:<digest>` and exact additional versions.
Replace `<digest>` with the selected base's 64 hexadecimal digest characters.
Culler assigns this local Docker tag to that exact image before the build.
Leave the image build to Culler after operator approval.

Propose Image dependencies only when image contents can resolve a required prerequisite.
Assess each missing prerequisite separately before a proposal or a decision to defer it.
Image additions cannot supply macOS system interfaces, host access, or additional container privileges.
A Docker client alone provides no Docker daemon.
Describe unavailable capabilities and affected repository commands in the report.
Do not propose unrelated packages.

## Image proposal assessment

For each proposed image change, establish these facts:

- The missing prerequisite and the required commands that depend on it.
- The commands that currently execute and the additional commands that the image can enable.
- The importance of those commands for development, rather than a percentage of individual tests.
- The expected image size, build time, and maintenance cost, when evidence permits an estimate.
- The evidence that the dependency can install and operate in the selected image.
- The remaining permissions, services, credentials, platform requirements, and other prerequisites of those commands.

Use permitted probes and available package metadata to assess feasibility before a proposal.
Check the selected base, processor architecture, declared versions, and required libraries for compatibility.
Use a minimal execution probe when ordinary permissions permit one.
A probe must preserve Project files, declared versions, and control permissions.
Leave the actual image build to Culler after operator approval.

State the evidence, assumptions, and unresolved uncertainty for each expected capability.
Do not invent a probability or claim verification from a package name alone.
Distinguish successful dependency installation from successful execution of the dependent command.
Execution can expose application failures; a proposal need not promise that tests pass.

Propose changes that provide a material development benefit with credible feasibility evidence.
A single essential command can justify a proposal.
An unrelated unavailable command does not invalidate that benefit.
When another unresolved prerequisite prevents the benefit, explain the dependency and the reason for deferral.
Record each missing prerequisite as a Workspace dependency, an Image dependency, an external requirement, or a justified deferral.

## Preparation results

Repository instructions define build and test requirements.
Execute relevant commands when the environment and authorization permit them.
Continue independent checks after a failure.
Classify each unresolved result as an application failure, an environment failure, or an unverified command.
Identify commands that never start and every known missing prerequisite.
If every required command passes, complete setup without an image proposal.
Preserve test assertions and declared versions.
Record actual commands, directories, exit codes, and useful conclusions in Markdown details.
An unexecuted command remains unverified.
A failed command retains its actual result.

Install declared dependencies before commands that require them.
Confirm service readiness before dependent commands.
Stop service processes when their work ends.
An absent cache requires an assessment of the repository's permitted installation methods.
Distinguish an absent cache from an unavailable dependency source.
Preserve an explicit repository requirement for a prepared cache.
Report that requirement as a limit when preparation cannot satisfy it.

Initial setup prepares what the available environment permits.
The setup agent assesses each missing prerequisite before it decides whether to submit an image proposal.
After that assessment, it submits `complete` when no permitted image proposal provides a credible material benefit.
This outcome remains valid when every repository command remains unavailable.
Its summary describes the preparation result or image decision and any material limit.
Its details distinguish verified capabilities from expected capabilities and explain each deferred Image dependency.
Its optional Markdown details explain useful observations.
Source acceptance and initialization complete import independently of repository command results.
Later task verification retains the repository's test requirements.

An initial image proposal requires an operator decision.
Keep its exact build contents in the report until approval.
The continuation after approval examines the tools in the approved image.
It exercises useful repository commands when possible and submits `complete` with unresolved limits.
The continuation after denial prepares the Project in the current image and submits `complete`.
Further image improvements use the Environment page after import.

The assigned role's command surface controls reports and proposals.
Pipeline work uses its assigned branch, task controls, and normal source review.
A dependency preparation failure remains context for source work when the runtime can execute the agent.
Keep secret values outside reports, build files, and reusable caches.
Culler runtime files remain separate from Project requirements.
