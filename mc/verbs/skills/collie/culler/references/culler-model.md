# The Culler model

Culler coordinates software agents for one operator on one Mac.
A Project groups work for a repository and retains its own configuration.
The Project's `VISION.md` states the operator's goals.
A task defines a bounded result and acceptance criteria.
A run is one agent execution; a task can receive several runs.

The spine is the SQLite database that holds the authoritative system state.
Only `mc` reads and writes the spine for Culler components.
The resident asks `mc` for due work and executes the resulting host and container operations.
The dashboard displays state and sends operator requests through `mc`.
A runner starts an agent and owns its response and execution records.
Collie answers the operator and requests authorized operations without a Project lease.

## Work and review

The pipeline roles have these responsibilities:

- The Strategist proposes two tasks from the Project goals and repository evidence.
- The Arbiter evaluates each proposal and promotes or rejects it.
- The Worker implements a task or a correction.
- The Verifier checks acceptance criteria and records evidence.
- The Packager prepares a Review Packet after verification.

Task stages are `proposed`, `seeded`, `worked`, `verified`, and `packaged`.
The stage stays separate from the decision, block, and failure allowance.
A Review Packet presents the result and evidence for the operator's decision.
Approval authorizes acceptance; a separate repository operation merges the approved result.
A successful merge confirms that the destination contains the approved change.

The Project lease permits one agent run of a Project at a time.
Repository operations, such as a merge, can run at the same time as an agent run.
Different Projects can run concurrently.
Repository operations hold no Project lease.
An approved task receives priority within its Project until its merge.
The merge starts without a wait for the active agent run.
A task interrupt stops the current agent run of the task.
A merge conflict clears that approval and requires repair, verification, a new packet, and a new decision.
The review capacity can pause new task work while the operator reviews packaged results.

## Setup and wiki updates

Project initialization precedes pipeline development.
A setup agent prepares dependencies and reports actual checks and environment limits.
Image dependencies require an approved Dockerfile and a compatible Project image.
The resident owns image builds and subsequent setup executions.

A wiki update is a separate Scribe run on the accepted Project commit.
It has no task and holds no Project lease.
Only the Scribe changes `openwiki/`.
Culler merges an accepted wiki commit without a Review Packet.
The Project wiki can lag behind its accepted commit until the next due update.
Current repository evidence takes precedence over a generated page.

## Instructions and conversations

A directive defines rules for each role run.
A skill supplies procedures for particular requests.
A brief supplies the facts for a pipeline run.
A harness executes the agent with the configured model and effort.
A Collie conversation persists separately from task runs and can resume after a runner restart.

A Discord channel message starts a Collie conversation in its own thread.
The operator can continue that conversation from the thread or dashboard.
The reply returns to the associated thread.
Configs → Discord owns Discord setup and account approval after the first Project completes setup.
Culler consumes account confirmation codes before Collie receives the message.

A wiki update has no task and holds no Project lease.
Culler merges an accepted wiki commit without operator review.
