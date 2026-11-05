# Real model acceptance

The host acceptance ran on 2026-11-02 in Coordinated Universal Time (UTC) in a private test home.
It used an isolated task image and protected host credentials.
It made 55 model requests in total.
Separate model catalog checks made no model calls.

| Test | Model requests | Result |
| --- | ---: | --- |
| Four connection methods, with a tool call and native continuation | 12 | Passed |
| Three normal profile Test and Save operations | 6 | Passed |
| Rejected minimal-effort Scribe attempts | 2 | The provider rejected the native web-search tool set |
| Two bounded nano Scribe attempts | 13 | Native lifecycle acceptance failed |
| Codex Scribe with the next cheapest compatible model | 12 | Passed after a zero-call artifact recheck |
| Claude Scribe with the cheapest compatible model | 10 | Passed |

The four connection methods cover native account sessions and static keys for each application programming interface (API).
Their evidence appears in these files:

- [API key results](/private/tmp/culler-proxy-live-api-evidence.json).
- [ChatGPT account result](/private/tmp/culler-proxy-live-chatgpt-account-evidence.json).
- [Claude account result](/private/tmp/culler-proxy-live-claude-account-evidence.json).

Those 12 earlier requests preceded the isolated diagnostic spine requirement in the current test command.
They prove the provider path, tool call, and native continuation.
They do not prove the current durable diagnostic write requirement.
The later profile and Scribe checks used an isolated diagnostic spine.

The normal product interface tested and saved three profiles:

- `gpt-5-nano` through the Codex harness.
- `gpt-6-luna` through the Codex harness.
- `claude-haiku-5-5` through the Claude software development kit (SDK) harness.

Each profile test used two requests.
The later attempts reused successful profile tests.
[Initial profile results](/private/tmp/culler-scribe-live-evidence-5/profiles.json) and [replacement profile results](/private/tmp/culler-scribe-live-evidence-9/profiles.json) retain those outcomes.

The Scribe-only compatibility exception excludes `gpt-5-nano` after two bounded attempts failed the native lifecycle criterion.
The second attempt confirms that the six required tool calls were absent.
The first attempt reached terminal success, but its original native output no longer exists.
Its exact tool behavior remains unverified.

The ordinary proxy test still uses `gpt-5-nano`.
The Scribe test selected `gpt-6-luna` as the next cheapest offered model with recorded price evidence.
[Retained nano evidence](/private/tmp/culler-scribe-live-evidence-8/scribe/scribe-codex-44d8c0b55c69ff68/result.json) records the second failure.

Both successful Scribe tests executed all six real OpenWiki tools.
They preserved the complete report and produced a page, source Claim, navigation, and completion receipt.
The Codex assertion initially rejected the canonical source range `#L1-L1` because it expected `#L1` literally.

A focused regression test now accepts both forms for the same source line.
The retained artifacts passed a recheck without another model call.
The original failed result remains intact beside the [Codex recheck](/private/tmp/culler-scribe-live-evidence-9/scribe/scribe-codex-192f7a25bc16f850/recheck.json).
The [Claude result](/private/tmp/culler-scribe-live-evidence-10/scribe/scribe-claude-44d8c0b55c69ff68/result.json) passed directly.

Each attempt declared its request limit and cost estimate before its model calls.
The successful Scribe estimates were $0.054144 each for their full 24-request limits.
An estimate is not a spend cap.
The host enforced each request limit and time limit.

The Scribe results retain provider-reported numeric usage through the native harness records.
The Codex result reports 213,653 input tokens and 1,138 output tokens across 12 responses.
The Claude result reports 20 input tokens, 361,754 cache-read tokens, 42,991 cache-write tokens, and 2,554 output tokens.
The Claude harness reports a cost of $0.3384658.
That value is a harness report, not a billing receipt.

The earlier API key and Claude account results lack numeric usage because their original collector did not retain it.
The ChatGPT result retains conversation totals, which cannot be added as separate turn usage.
The product profile tests also lack numeric usage because they remove their private native sessions.
Rejected requests retain unavailable usage explicitly.

The final cleanup removed three completed profile-operation records and both dedicated API test keys.
It used the current product disconnect operation with each current authorization identity.
It preserved the source key file and both dedicated account sessions.
No enabled test profile, connected API test key, or proxy socket remains.
[Cleanup evidence](/private/tmp/culler-scribe-cleanup-evidence.json) records those checks.
