# Automatic connection recovery

The resident retries account renewal after the recorded retry time, without a model request or user action.
The connection check controls the retry time and permits one active check.
New agent runs that use the affected connection wait until renewal succeeds.
The credential service records the provider result.
A failed check releases its reservation without a second failure observation.

## Regression evidence

`resident/src/connection-recovery.test.ts` executes the resident tick with the actual credential service and Go connection controls.
The test uses synthetic credentials, synthetic provider responses, and an isolated database.
The test replaces the dispatch command with an idle result.
The actual `ConnectionAdmission` function verifies the change in launch permission.
The actual `SystemStatus` function verifies that successful renewal removes the connection condition.

The tests cover these cases:

- A request encounters a transport failure during renewal.
- A tick before the retry time sends no renewal request.
- A tick after credential service recreation retries the saved failure.
- An expired connection renews before a new agent run starts.
- A second transport failure preserves its cause and extends the retry delay.
- Successful renewal clears the condition and restores launch permission.
- Concurrent ticks start one renewal request.
- A rejected refresh credential requires repair.
- A delayed failure preserves a replacement credential.

`mc/verbs/provider_renewal_test.go` also checks repeated transport failures, failure counts, and obsolete results.
The component checks include these tests.
These tests make no real provider requests.
The recovery test fails with the original selection rule.
The obsolete credential test fails with the original failure wrapper.

## Check results

The task branch starts at local `main` commit `6d1ee5a9`.
The following checks passed:

- `./mc/check.sh`: the complete Go check, with no skipped tests.
- `./resident/check.sh`: 681 tests and 4,457 assertions.
- `./dashboard/check.sh`: 889 tests and 5,398 assertions.
- The separate recovery integration run: five tests and 72 assertions.

An independent agent reviewed the correction and its tests.
This work changes no production deployment.

## Separate defects

The review found these separate defects, which remain outside this correction:

- Temporary renewal responses can bypass the recorded delay and the three-failure limit.
  The [credential service](../../resident/src/provider-credentials.ts) repeats requests inside one call.
  Requests from active launches can also renew without a connection check.
  `applyProviderRenewal` also retains `retry_wait` after the third temporary response.
- A resident restart loses the rejected credential reference from memory.
  A connection check can then accept an unexpired token that the provider previously rejected.
- Repeated account authorization rejections bypass the one-recovery allowance.
  The proxy observation path resets the connection to `unknown` without `model_refresh_used`.

The automatic transport recovery tests do not cover these separate defects.
