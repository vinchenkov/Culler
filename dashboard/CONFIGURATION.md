# Advanced Settings audit

The dashboard exposes eleven editable settings.
The audit covers each of the 35 configuration keys in the table.
“Setup” means the value requires the existing setup procedure or host activation.
“Model configuration” means the dashboard connection and agent profile flow owns the value.
The internal configuration file retains the omitted fields for compatibility with their current consumers.

| Configuration key | Dashboard disposition | Verified behavior and source |
| --- | --- | --- |
| `budgets.review_wip_cap` | Editable. | `mc/verbs/dispatchverb.go` reads the limit per Project. `mc/substrate/delivery.sql` enforces review capacity. Approved tasks retain their slots until archive. |
| `budgets.max_correction_count` | Omitted. | `mc/substrate/schema.sql` fixes the correction limit at three. The configuration field has no runtime consumer. |
| `budgets.dispatch_retries` | Omitted. | `mc/substrate/schema.sql` fixes the initial and maximum retry counts at three. The configuration field has no runtime consumer. |
| `timing.timeout_minutes` | Editable. | `mc/dispatch/dispatch.go` uses the spine value for lease expiry. The next lease check uses the saved value. |
| `timing.grace_minutes` | Editable. | `mc/dispatch/dispatch.go` adds the spine value to the lease timeout. The next lease check uses the saved value. |
| `timing.heartbeat_interval_s` | Editable. | `mc/verbs/dispatchseam.go` captures the interval for new runs. Current runs retain their launch values. |
| `timing.spawn_grace_s` | Editable. | `mc/dispatch/dispatch.go` checks the first heartbeat deadline. Scribe receives at least 60 seconds. The hard deadline still applies. |
| `timing.hard_deadline_minutes` | Editable. | `mc/verbs/dispatchverb.go` captures the deadline for a new run. Current runs retain their deadlines. |
| `timing.tick_interval_s` | Setup. | `mc/verbs/onboardconfig.go` projects the interval into the resident configuration. `resident/src/main.ts` reads it at startup. |
| `timing.collie_idle_timeout_s` | Editable. | `mc/verbs/dispatchseam.go` reads the spine value for Collie session expiry. The next session check uses the saved value. |
| `timing.wiki_update_interval_s` | Editable. | The wiki update decision in `mc/dispatch` reads the spine value. The next dispatch uses the saved value. |
| `timing.backup_interval_s` | Setup. | `mc/verbs/onboardconfig.go` projects the interval into the host policy. `resident/src/main.ts` creates the backup timer at startup. |
| `timing.backup_retain` | Editable. | `mc/verbs/spine_transfer.go` captures the current retention count for a backup. `mc/verbs/ops.go` applies retention. |
| `container.runtime` | Omitted. | `mc/config/config.go` accepts only Docker. The resident executes Docker commands. |
| `container.base_image` | Omitted. | `mc/deployment/supervision.go` requires `mc-prod` for production. The configuration field has no image selection consumer. |
| `container.package_cache_root` | Omitted. | The field has no runtime consumer. The mount planner selects cache paths independently. |
| `container.mount_allowlist` | Omitted. | `mc/verbs/mountplan.go` uses the fixed `mount-allowlist` path. The configuration field cannot select another file. |
| `container.additional_blocked_patterns` | Editable. | `mc/verbs/dispatchseam.go` constructs the path policy for new runs. `mc/boundary` accepts `component` and `basename-glob` rules. |
| `surfaces.dashboard.bind` | Setup. | Host activation selects the dashboard address. A text edit cannot move the active server. |
| `surfaces.dashboard.auth_token` | Setup. | The dashboard reads its authentication token at startup. The editor omits the token and its state. |
| `surfaces.agentsview.bind` | Setup. | Host activation selects the AgentsView address. The address requires a loopback host and a port that no other address uses. A text edit cannot move the active server. |
| `surfaces.wiki.bind` | Setup. | Host activation selects the wiki address, where the dashboard process serves the wiki exports. The address requires a loopback host and a port that no other address uses. A text edit cannot move the active server. |
| `surfaces.discord.enabled` | Configs → Discord. | The dedicated flow validates and activates the connection. |
| `surfaces.discord.bot_token` | Configs → Discord. | The masked field submits the token to the host. Responses omit the token. |
| `surfaces.discord.guild_id` | Configs → Discord. | The operator selects a server by name. |
| `surfaces.discord.channel_id` | Configs → Discord. | A test message proves delivery to the selected channel. |
| `surfaces.discord.operator_ids` | Configs → Discord. | A temporary code identifies the account. Explicit confirmation authorizes the account. |
| `surfaces.alerts.blocked` | Configs → Discord. | The Blocked tasks control changes the Discord route and preserves dashboard alerts. |
| `surfaces.alerts.health` | Configs → Discord. | The System health control changes the Discord route and preserves dashboard alerts. |
| `git.author_name` | Setup. | Custom names are supported. `resident/src/main.ts` reads the default at startup. `resident/src/effects.ts` and `resident/src/repository.ts` consume it. |
| `git.author_email` | Setup. | Custom addresses are supported. The resident retains the default until restart. Project overrides take precedence. |
| `runtimes.enabled_bindings` | Model configuration. | The profile flow validates access and model proof before it enables a profile. A raw list edit bypasses that flow. |
| `runtimes.forbidden_env` | Editable additions. | `mc/verbs/dispatchseam.go` captures the policy for new runs. The editor preserves the two mandatory restrictions. |
| `runtimes.credentials` | Model configuration. | The connection flow owns credentials and access checks. The editor omits the credential summary. |
| `runtimes.bindings` | Model configuration. | The profile flow owns model selection and validation. The editor omits the profile summary. |

The numeric editors accept positive integers within explicit bounds.
Duration bounds prevent overflow during conversion to system durations.
The server validates path rules and environment names through the existing configuration validators.
The path editor presents only the two supported rule types.
The environment editor exposes additions and preserves the mandatory restrictions.

A save holds the configuration lock through file persistence and the required spine update.
The save compares each previous value with the current value before it changes the configuration.
The save preserves unrelated fields, including secrets and model configuration.
An identical retry can repeat an incomplete spine update.
A failed spine update reports that file persistence succeeded and activation remains unverified.
The browser retains the edits for a retry.

The release requires the full deployment procedure because the dashboard uses a new command.
