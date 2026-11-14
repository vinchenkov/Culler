# Safe update verification

Date: 2026-11-06.

The task branch is `task/safe-public-updates`.
Its base is main commit `bd7b4b7bc19ff974e1beaa58d9e05b6b763e6ef7`.
The specification precedes the implementation in the branch history.
These checks use scratch data and leave the production deployment unchanged.

## Completed checks

| Check | Result |
| --- | --- |
| `./mc/check.sh` | Passed formatting, vet, platform checks, installer contracts, and all Go packages. No test reported an unverified result. |
| `./release/check.sh` | Passed 100 tests and the version and changelog check. |
| `./resident/check.sh` | Passed 691 tests. |
| `./dashboard/check.sh` | Passed 905 tests. |
| `./dashboard/smoke.sh` | Passed 301 tests across all 51 files, with 4,704 assertions. |
| `./runner/agent-runner/check.sh` | Passed 149 tests. |
| `./runner/collie-runner/check.sh` | Passed 54 tests. |
| `./runner/setup-runner/check.sh` | Passed nine tests. |
| `./runner/fake-harness/check.sh` | Passed 43 tests. |
| `./runner/shared/check.sh` | Passed 118 tests. |
| `./runner/image/check.sh` | Passed its script, package, and release checks. |
| Docker checkpoint recovery | Passed with actual scratch volumes and an interrupted restore. |
| Terminal review | Accepted at 80 and 40 columns, with color and without color. |

The resident and runner checks precede the final rebase.
The rebase adds the current release version policy and leaves their implementation unchanged.
The command, release, dashboard, browser, and Docker checks cover the rebased implementation.

The browser suite exposed a race in the existing manual retry test.
The test now stops periodic refresh requests before it exercises the Retry button.
It checks the actual response and preserves the saved conversation assertions.
The complete suite passes without a longer timeout or a skipped test.

The final recovery tests simulate a directory synchronization failure after the journal rename.
They verify that maintenance continues to block work until a durable journal save succeeds.
They cover committed, complete, and restored phases, plus deferred setup cleanup.

## Docker recovery evidence

The Docker test uses `culler-migration-previous:20261105` and a Linux command built from this checkout.
It creates a real spine and an optional runtime volume in scratch storage.
It interrupts restoration of the optional runtime volume and retries the restore.
It verifies the spine identity and the saved runtime bytes.
The test removes its temporary volumes.

The test command is:

```sh
cd mc
MC_UPDATE_TEST_IMAGE=culler-migration-previous:20261105 \
MC_UPDATE_TEST_BINARY=/private/tmp/culler-safe-linux-mc \
mise exec -- go test -tags update_docker -count=1 ./deployment \
  -run '^TestUpdateCheckpointDocker' -timeout 12m -v
```

## Required release acceptance

The complete packaged update and restore remain unverified through macOS launchd.
These checks build no product image and activate no production services.
The installer contract tests use a shell substitute for the installed command.
They verify installer selection, integrity checks, recovery dispatch, and explicit interruption consent.
They do not prove complete service activation.

Before public release, the maintainer must complete the packaged acceptance procedure in [UPDATES.md](UPDATES.md#release-evidence).
That procedure uses distinct baseline and candidate packages on a separate Mac or isolated deployment.
It verifies interruption, offline recovery, durable data, service identities, and cleanup.
These repository checks do not replace that release requirement.
