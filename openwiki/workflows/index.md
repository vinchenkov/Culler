# Files

- [Decision Pending and requirements revisions](decision-resolution.md) - How operator decisions become task requirements while Culler preserves accepted work and the original task base.
- [Dispatch and the resident tick loop](dispatch-and-tick-loop.md) - How the resident's tick runs `mc dispatch`, how the pure `dispatch.Decide` picks one action per Project, how the prepare/attest/commit seam commits it, and how the resident applies the resulting effect JSON.
- [Git delivery and repository operations](git-delivery.md) - How Culler gives each code run an isolated repository, captures exactly one commit per task, builds a merge candidate, and applies it to the operator checkout.
- [Packet conversations from Review to Homie](packet-conversations.md) - The page traces packet conversations through the dashboard, spine, artifact mounts, and Homie runner.
- [Project setup and environment lifecycle](project-environments.md) - Separate setup operations prepare dependencies and preserve exact source, configuration, and image assignments.
- [The task pipeline roles](task-pipeline.md) - How task roles produce a verified source version and review packet, with wiki updates outside the task pipeline.
