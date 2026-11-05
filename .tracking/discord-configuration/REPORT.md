# Discord configuration verification

Date: 2026-10-28.

The implementation adds Configs → Discord after the first Project completes initial source acceptance.
The Configs sidebar includes five local outline icons.
The dashboard configures the bot, destination, account access, and alert routes.
The resident applies connection changes without a process restart.

## Automated checks

| Check | Result |
| --- | --- |
| `./mc/check.sh` | Passed. This includes deployment, installation, release contract, and Go checks. |
| `./runner/fake-harness/check.sh` | Passed: 43 tests. |
| `./runner/agent-runner/check.sh` | Passed: 130 tests. |
| `./runner/homie-runner/check.sh` | Passed: 51 tests. |
| `./runner/setup-runner/check.sh` | Passed: 9 tests. |
| `./runner/shared/check.sh` | Passed: 116 tests. |
| `./runner/image/check.sh` | Passed, including release and OpenWiki checks. |
| `./resident/check.sh` | Passed: 613 tests. |
| `./dashboard/check.sh` | Passed: 873 tests. |
| Complete dashboard browser suite | Passed: 285 tests across 48 files. |
| Final Discord browser lifecycle | Passed: 2 tests through the actual host command boundary. |
| Final Discord unit checks | Passed: 28 tests. |
| Final targeted Go checks and Go vet | Passed. |

The final targeted checks cover the connection changes after the complete component checks.
The browser evidence covers desktop and narrow displays in both themes.
The browser tests use simulated Discord responses.

The complete browser check exposed four test defects.
The customization test left its parent dialog open.
The evaluation, Project deletion, and Activity tests checked their final state before refresh completion.
The corrected tests close the parent dialog and wait for the required state.
The checks retain their original assertions.

One earlier full run timed out after a Homie model change.
The isolated repeat passed without a Homie change.
The final complete browser run also passed that check.

## Live Discord checks

The live check used the supplied credentials in a scratch deployment.
It exercised the actual dashboard, host commands, Discord service, and Discord network connection.
The first check used an existing connection.
A later check completed new account confirmation with the operator's actual Discord reply.
Each outbound message identified itself as a Culler test.

| Behavior | Result |
| --- | --- |
| Bot authentication and destination permissions | Passed. |
| Message Content Intent | Passed. |
| Connection status and connection check | Passed. |
| Explicit test message | Passed. |
| Blocked task alert delivery | Passed through the configured alert route. |
| System health alert delivery | Passed through the configured alert route. |
| Disconnect and token removal | Passed. |
| New account confirmation | Passed with the operator's actual Discord reply. |
| Authorized Discord message receipt | Passed through the live Gateway into Homie. |
| Homie reply delivery | Passed with a scripted reply after a retry of the original Discord message. |
| Reply from a live agent | Unverified. The test used a scripted reply. |

The temporary test script caused two failures after account confirmation.
Its first check required exact message text and missed the operator's additional sentence.
Its next check compared a text message identifier with a numeric identifier.
That check stopped after the live Gateway delivered the operator's message into Homie.

The corrected script replayed the original Discord message through the same Homie route.
It submitted a labeled test reply through Homie's claim and reply operations.
Discord confirmed delivery, and Culler recorded the delivery receipt.
The test then disconnected and removed the token.
These corrections changed the temporary test script only.

## Credentials and deployment

The task branch includes local main at `1e32800c` through merge commit `99e7b11e`.
The dashboard component check passed all 873 tests after that merge.
The focused Go checks passed for Discord, role directives, role skills, and run briefs.
The affected browser check passed all 38 tests across four files.
That check covers Discord, Configs, Packet conversations, and report frames.

The first integration check used the long default macOS temporary path.
The Discord token check timed out with that path.
The repeated checks used `MC_SMOKE_ROOT=/private/tmp` and passed the Discord lifecycle.
The Discord service uses a Unix socket, whose path has an operating system length limit.

The Packet resize test from main exposed a separate test synchronization defect.
The test now waits for the mobile conversation before it checks visibility.
Its cleanup restores the desktop viewport before it resets the divider.
The final affected browser check passed with those corrections.

The credential scan found none of the supplied credential values in changed files.
The repository excludes `discord.env` through `.gitignore`.
The saved bot token remains in the host configuration.
The setup flow holds temporary token values in the entry field and host memory.
The dashboard responses omit the token.

This change alters the database schema identity and the host release manifest.
Deployment requires the full release procedure and its database compatibility check.
The read-only check of the installed database returned `differs` on 2026-10-28.
The current deployment script requires a database reset to activate this schema.
The operator must approve that reset before deployment.
The verification left the production deployment unchanged.
