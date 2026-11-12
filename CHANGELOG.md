# Changelog

This file holds one section for each Culler version, from the newest version to the oldest version.
The release workflow copies the section of a new version into the description of its GitHub release.
The [installation guide](INSTALL.md#versions) gives the version rules.

## 0.2.1

### Changes

- Each role of a Project has one set of custom instructions. A save replaces them, and the dashboard shows no revision history.
- Collie and external coding agents share the supported operator commands and a common `culler` skill.
- The command help describes each operation, its request, its result, and its authority requirements.
- Culler retains operator requests and their results. An uncertain request never repeats its effects automatically.
- The release includes the common skill, its procedure files, and the native trace reader.
- Terminal setup links the `culler` skill for external agents at `~/.agents/skills/` and for each selected Claude Code or Codex harness. It preserves existing skill files.
- Terminal setup highlights the notice for the `culler` command.
- A role assignment requires the current assignment, so a stale request preserves a later change.
- The dashboard browser test waits for profile recovery before it checks Save.
- The test closes the profile dialog after a failure to protect subsequent tests.
- The application package builds alongside the component checks, so the image build can start sooner.
- The workflow retains the Go build cache for each commit and preserves the separate image cache.
- The test release policy permits repeated versions and schema changes through the normal install path.
- An update accepts a Project that uses the default Project image.
- The release image checks run the Project image check of an update against the default Project image.
- A restore starts the helper container of the previous release before it starts the services.
- The terminal setup provider search updates its list of matching providers after each key, as the dashboard search does.
- The provider search in terminal setup and the dashboard lists name matches before identifier and address matches.
- The provider search ignores the `opencode:` prefix of catalog identifiers, so a search for "opencode" matches only OpenCode providers.
- Terminal setup and the dashboard first offer each provider with a sign-in, then **Use another provider**. A provider with a sign-in offers the sign-in before the API key.
- Terminal setup and the dashboard name each new connection, and they request no connection name or account name.
- The provider search lists the providers that OpenCode runs, and an empty search lists each of them.
- The dashboard marks the selected provider in the search results and shows it above the API key field.
- Each Strategist proposal holds a Strategic rationale, a task field separate from the description. Culler refuses a Strategist proposal without one.
- The Arbiter receives the Strategic rationale of each proposal and checks its evidence. The Worker and the Verifier receive the description without it.
- The task dialog of a task that the Strategist proposed shows the Strategic rationale as its first content section, above the description.
- The Review Packet of a task that the Strategist proposed starts with the Strategic rationale section. The packet of a task that the operator defined starts with the Summary section.
- The Strategist, the Arbiter, and Collie apply one shared standard for a task description: the scope and the acceptance criteria that an agent container can check.
- Collie shows the full task description before the operator confirms a new task or a requirements revision, and it identifies each rule of the standard that the draft violates.
- An OpenCode connection offers the release catalog models of its provider and the live models. Culler takes a live model from the live model list of the OpenCode project, and a live model replaces the release catalog model with the same identifier.
- Culler accepts a live model only for a provider in its release catalog, and only at the network origin of a release catalog address of that provider. A provider key goes only to those origins.
- Culler starts a refresh of the live models after the OpenCode installation, after a Culler update, and at a model choice when no refresh started during the previous hour. The request for the live model list holds no provider key and no data about the operator, a Project, or the deployment.
- The model choice of terminal setup and the dashboard states the source of its model list.
- Terminal setup returns to the model choice when a saved choice names a model that the connection does not offer.
- When an update changes the release catalog or the OpenCode version, the update confirmation names each enabled agent profile whose model or effort level only a live model supplies. The update starts a refresh of the live models, and a failed refresh stops the work of such a profile until repair in the dashboard.
- The Configure step of the Agent profiles panel shows the OpenCode download as a card with a progress indicator, the current stage, and the statement that the model test waits for the download.
- A background update of the Agent profiles panel changes only the elements whose content changed. Each control keeps its focus, its entered text, its selection, its scroll position, and its open list.
- Culler uses OpenWiki 0.9.0.
- The begin response of a wiki update gives the newest commit messages after the last review of each page group. It limits the lists of changed source paths and Claim issues and gives their exact numbers, and the Scribe lists the remaining paths through Git.
- A wiki update reviews each Claim issue. Culler rejects a page plan that defers one.
- Culler uses OpenCode 2.2.7.
- With OpenCode, a background command keeps the turn of the agent open, and the agent receives the result when the command ends. Culler stops the background commands of a turn after ten minutes without a new event from OpenCode and completes the turn with the last reply.
- The OpenCode release catalog holds 209 providers. Wally is a new provider.
- The release catalog omits a provider that reads its key from `GITHUB_TOKEN`, because the commands of an agent read that variable for GitHub. This rule omits GitHub Copilot.
- Culler uses Claude Agent SDK 0.3.322 and Codex 0.167.2.
- With the Claude harness, a background command keeps the turn of the agent open, and the agent receives the result when the command ends. Culler stops the background commands of a turn after ten minutes without a new event from the harness and completes the turn with the last reply.
- With a Claude account, the harness repeats a request one time after the provider rejects the credential. Culler renews the account credential before the second request, and the run, the connection test, or the model test continues after a successful renewal.
- After the provider rejects a request with status 400 or 422, the Claude harness sends the request again without optional features. A request that the provider rejects with status 400, 401, or 422 does not use the request limit of a connection test or a model test, so the test shows the reason of the provider or passes after an accepted request.
- The Claude harness loads the tools of a Model Context Protocol (MCP) server after a tool search. The OpenWiki tools of a wiki update load with each request.
- With the Claude harness, the context window of a model can have 1,000,000 tokens, where it had 200,000 tokens. When the provider rejects a prompt as too long, the harness compacts the conversation and continues the run.
- With Codex, a command of an agent keeps each variable of its container, also a variable with KEY, SECRET, or TOKEN in its name. The temporary Culler token is absent from the environment of a command.
- A profile that uses a default of its harness follows the new default. For the effort Default, the Claude harness selects the level medium for some models that had the level high. Codex selects a newer default model.
- Culler uses AgentsView 0.47.0.
- AgentsView records a Codex run as a child session. The resident requests the child sessions with its session list, so the name and the import state of a Codex trace stay correct.
- The trace viewer uses the fonts that AgentsView embeds in its page, and the page requests no font from another origin.
- AgentsView offers Search by meaning, which downloads a model. Culler keeps AgentsView without network access, and the Culler proxy refuses the embeddings operations.
- Culler sets no CPU or memory limit on its containers by default. Each container has a limit of 2048 processes.
- The Advanced settings page sets an optional CPU limit and an optional memory limit for each new agent container. A memory limit permits no additional swap.

### Known limits

- Culler supports one operator on one Mac with Apple silicon.

### Database compatibility

- The operator request records change the database schema.
- The Strategic rationale field changes the database schema.
- The updater requires a compatible database from an earlier release.

### Update effects

- This test release permits new installations. The updater rejects an incompatible database from an earlier release.

## 0.2.0

### Changes

- The macOS installer accepts terminal input when the install command uses a pipe.
- Setup shows automatic progress and a clear dashboard handoff.
- Collie replaces the Homie name.

### Known limits

- Culler supports one operator on one Mac with Apple silicon.

### Database compatibility

- The Collie rename changes the database schema. The current update code cannot convert a 0.1.0 database.

### Update effects

- This test release permits new installations. The updater rejects the incompatible database from 0.1.0.

## 0.1.0

### Changes

- Version 0.1.0 is the first version of Culler.
- Culler is a local-first orchestration system for AI coding agents, with one human operator.
- `culler version`, `culler update --check`, setup, and the dashboard show the version and the short commit of each release.
- Public updates verify one complete release and preserve saved user data.
- The updater warns before it interrupts active work and preserves the saved Project pause choices.
- Before Culler records update success, an interrupted update can retry the same release or restore the previous release.
- After a successful update, Culler removes obsolete software and keeps each Project's selected image.

### Known limits

- Culler is in early access. A 0.x minor version can change the public interface.
- Culler supports one operator on one Mac with Apple silicon.
