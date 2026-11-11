# Collie command access validation

The implementation uses local `main` at `3c47247726704adedb48a632a1070ee441fbd69b` as its base.
The checks ran on macOS.
The Docker boundary check used the production Linux command and protected wrapper.

## Acceptance coverage

The new checks cover these contracts:

- Static help works without the spine, credentials, services, or an available deployment.
- The real executable validates example syntax. The checks compare fields from successful results.
- Collie permissions remain frozen and agree with the launch identity.
- Restricted sessions, ended sessions, and forged identities cannot gain operator authority.
- Packet and Vision conversations retain their resource constraints.
- A missing task or run cannot create a request without its Project constraint.
- The host path rejects component commands and credential authorization access from Collie.
- Retained request identifiers prevent duplicate effects and reject different request content.
- A failed claim preserves the receipt of an active executor.
- Update maintenance and release mismatches prevent operator changes.
- Project registration, Vision, role instructions, and dashboard configuration retain their domain checks.
- An external agent can use installed resources to discover commands, read scratch state, and change scratch state.
- Host and container skills contain every reference at the declared path.
- Skill integration preserves unrelated files through installation, update, and uninstall.

The external agent check uses copied release files and a custom deployment home.
Its command uses a test-only build and an explicit scratch spine path.
The test supplies that path through `MC_SPINE`.
The agent calls run without a source checkout or development tools on `PATH`.
A separate check uses the production build to select the helper from custom `MC_HOME`.
That check supplies no `MC_HELPER` or `MC_SPINE` and starts no helper.

The Docker check uses agent user 10002, the protected command wrapper, and scratch volumes.
It verifies an actual host pause and refusal of direct spine access.
A complete production installation remains unverified.

The terminal skill offer passed an independent review of 12 captured outputs.
Those captures cover 40 and 80 columns, color and plain text, and installation, conflict, and skip results.

## Check results

| Check | Result |
|---|---|
| Command catalog, help, authority, retry, and request validation | The focused tests pass. |
| External agent resource check | The check passes without a source checkout or development tools. |
| Docker transport boundary | The check passes through the protected production command. |
| Deployment and test-environment packages | The uncached checks pass. |
| Installation, redeployment, update guard, and uninstall contracts | The shell checks pass. |
| Release checks | The checks pass. |
| Fake harness, setup runner, Collie runner, shared runtime, and image component | The component checks pass. |
| Agent runner | All 153 tests pass. |
| Dashboard unit checks | The component check passes. |
| Dashboard pages browser file | All 22 tests pass. |
| Project setup browser file | All 11 tests pass. |
| Role customization browser file | All 3 tests pass. |
| Final command regression group | All 105 selected tests pass. |
| `gofmt` and `go vet` | The checks pass. |

The complete Go attempt reached the 20-minute package limit for the command and verb packages.
The remaining tests ran in separate groups.
Focused checks verified each corrected regression.
The verb groups retain the two base failures below.

The complete browser attempt reported 285 passes and 23 failures before the final integration fixes.
The three affected browser files pass after those fixes.
The complete browser suite retains the base Collie parity failure below.
The complete resident check reported 724 passes and one base timeout.

These results do not establish a successful complete fast-suite or browser gate.

## Base failures

Each failure below also occurred in an isolated copy of the base commit.

| Check | Observed failure |
|---|---|
| `TestPackagerConditionalProceduresKeepTheirRequirements` | The Packager text contract fails. |
| `TestRoleInputsDescribeTheStrategicRationale` | The role text contract fails. |
| Resident AgentsView bounded-request test | The test exceeds its five-second limit. |
| `collie-parity.smoke.test.ts` | The selected test connection is absent after the harness switch, so the composer stays disabled. |
| Layout address test | A focused attempt exceeds its five-second limit on both trees. The final complete attempt passes this test. |

A diagnostic run of the resident test passes with a 15-second test limit.
The source retains its original limit.

## Release limits

The operator request table changes the spine schema.
The updater rejects an incompatible database from an earlier release.
This task changes no live deployment and publishes no release.
The checks use scratch state and no provider expenditure.
The full Docker end-to-end and nightly suites remain unverified.
