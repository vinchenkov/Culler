# Public updates

Culler supports one complete release on one Mac.
Version 0.1.0 starts the public compatibility baseline.
The public compatibility policy in the release record identifies a supported public baseline.
An internal record without that policy requires a separate deployment home, even when it contains a version.
Internal source installations use the separate source redeploy procedure.
Source redeploys refuse an incomplete public update.
Source redeploys cannot run concurrently with public lifecycle commands.
Public updates preserve user data and retain a recovery action after each incomplete phase.

## Support policy

Each public release has a unique Semantic Versioning version and an immutable commit identity.
The `VERSION` file supplies that version.
Patch releases preserve persistent formats and public behavior.
Minor releases can introduce tested forward migrations.
Before version 1.0.0, minor releases can change the public interface.
After version 1.0.0, an incompatible public interface change requires a major release.
Every supported path preserves durable user data.

The release record contains these compatibility values:

- The minimum supported public version.
- The spine schema hash and configuration version.
- The credential format and native producer identities.
- Accepted source identities for each format.
- Checksums of dependency manifests, lock files, and direct tool pins.

Consecutive public releases require a supported path.
Skipped releases work when the candidate accepts all installed formats and the minimum version.
A required intermediate release remains available with its exact artifacts.
Ordinary downgrades remain unsupported.
Only the latest public release receives fixes.

The initial implementation supports unchanged spine, configuration, and credential formats.
An accepted-format list cannot enable a conversion by itself.
A native dependency change requires fixture evidence before its previous producer enters the accepted list.
Native migrations also require code that protects and verifies their affected stores before commit.

## Update and recovery

The operator starts an update with `culler update`.
The warning describes interruption and any affected saved profiles before confirmation.
The updater then stops services and containers immediately.
It forces termination after ten seconds and verifies exit within thirty seconds.
An unknown process state refuses replacement.

The update follows these phases:

1. Record the stop intent and block ordinary work.
2. Stop the deployment and reconcile interrupted executions.
3. Check the fixed candidate, saved configuration, and selected Project images.
4. Create and verify the checkpoint.
5. Replace the managed software.
6. Verify component identities and local readiness while work remains blocked.
7. Synchronize files, record the commit, and release maintenance to permit work.
8. Remove obsolete software and temporary checkpoints.

`culler update` retries an incomplete update with its exact candidate.
`culler update --restore` restores the checkpoint before commit.
The saved installer provides the same recovery path when the command is unavailable.
Recovery checks the retained package before execution and needs no download after checkpoint completion.
The updater preserves the checkpoint if restoration fails.
The operator can retry restoration after the reported cause is resolved.

A committed update never restores its old checkpoint over current data.
A cleanup failure leaves the committed release available and records a retry.
The updater removes previous images after commit.
It preserves selected Project images and images that containers use.

The checkpoint protects the spine and optional OpenCode runtime volume.
It also protects configuration, host software, installed commands, service definitions, and applicable AgentsView data.
The previous product image reconstructs the ordinary runtime volume.
Project repositories, worktrees, homes, artifacts, and native sessions remain in place.
The public update avoids legacy credential retirement and native session migrations outside a tested conversion path.
Provider grants retain their newest saved values.

The checkpoint covers an update on intact storage.
It does not replace the operator's separate backup for disk loss.
A separate backup must cover durable Project data, native sessions, configuration, and credential recovery.
Keep secrets outside release records and diagnostic logs.

## Maintainer procedure

Start with the smallest dependency change.
Use a task worktree and leave publication to the maintainer.

1. Change the dependency pin and its lock file together.
2. Identify its affected interface and persistent store.
3. Add representative fixtures for that interface or store.
4. Run the component checks and the affected image checks.
5. Increase the public version in `VERSION` and add its `CHANGELOG.md` section.
6. Add accepted previous producers only after the fixture tests pass.
7. Run the release and update checks against the exact packaged artifacts.
8. Review the release record and recovery evidence before publication.

The source policy in `mc/distribution/update_policy.json` declares the minimum version and accepted source formats.
With `test_releases` absent or false, the workflow publishes nothing when `VERSION` equals the latest public version.
It checks compatibility with the previous public release before publication of a higher version.
With `test_releases` true, each new commit can produce a test release through the normal install path.
That policy bypasses version and migration-path gates without a data conversion or reset.
An incompatible database requires a fresh install in a separate deployment home.
[The test release procedure](README.md#test-releases) describes the policy and its reversal before the public release.

Use these checks for the affected dependency:

| Dependency | Required evidence |
| --- | --- |
| Harness | Adapter behavior, saved native sessions, continuation, and interruption recovery. |
| Plugin | The pinned harness, its plugin interface, and any plugin store. |
| OpenCode catalog | Saved selections and exact explicit effort forwarding through representative adapters. |
| Project image | The candidate runtime on every selected image, including archived Projects. |
| AgentsView | Old trace access, database compatibility, and authenticated browser access. |
| OpenWiki | Source preservation and export compatibility. |
| Bun, Node.js, Go, bundled tools | Component checks and exact product-image smoke checks. |

The [provider contract](PROVIDER-COMPATIBILITY.md) separates local checks from external provider behavior.
The release inventory covers optional components and tools outside normal package lock files.

## First format conversion

Implement a converter only when a real format change requires one.
Do not advance a format declaration before its converter and recovery tests exist.

1. Keep a fixture from each supported source format.
2. Define the preserved records and their relationships.
3. Copy every store that the converter can modify into the checkpoint.
4. Run the converter behind the update gate.
5. Verify the result before commit.
6. Test interruption before and after each irreversible write.
7. Test a second restore attempt after an interrupted restore.
8. Update the source policy and publication checks with the verified path.

Preserve explicit configuration choices and effective Culler defaults.
A removed option requires a conversion or an actionable refusal.
A changed default applies to new installations unless the release explicitly migrates existing values.
Native database readability alone does not prove session continuation.

## Release evidence

The required fast, deployment, browser, and image checks remain release gates.
The security scans retain their separate reporting policy.
Use `./release/check.sh` for the credential-free release tools.
Use `./mc/check.sh` for the command and deployment checks.

The opt-in `update_docker` test uses an existing product image and a Linux command built from the checkout.
Set `MC_UPDATE_TEST_IMAGE` and an absolute `MC_UPDATE_TEST_BINARY` before this test:

```sh
cd mc
mise exec -- go test -tags update_docker -count=1 ./deployment -run '^TestUpdateCheckpointDocker' -timeout 12m
```

This test uses scratch volumes and simulates a failed restore during the optional runtime copy.
It verifies another restore attempt, the spine identity, and the saved optional runtime bytes.
It does not activate host services.

Before the first public release, test the complete packaged update on a separate Mac or isolated deployment.
Use a baseline package and a candidate with different release identities.
Exercise the old installed command and old saved installer.
Terminate the updater at each phase and retry without network access.
Check immediate interruption, service identity, saved data, restoration, and cleanup.
Retain the actual command output and store comparisons with the release evidence.
After publication, use actual supported installers and data fixtures for this test.

## Emergency recovery

1. Read `culler status` and the update diagnostic.
2. Preserve the update record and its artifact directory.
3. Resolve the reported disk, Docker, process, or file problem.
4. Run `culler update` to retry the exact candidate.
5. Use `culler update --restore` for a precommit failure that requires the previous release.

After commit, repair the installed release with current data.
Do not copy an old spine alone over native stores or Project data.
An unreadable update record keeps work stopped until the maintainer repairs its identity and phase from verified evidence.
Keep that repair explicit; do not infer success from the presence of candidate files.
