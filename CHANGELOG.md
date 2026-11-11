# Changelog

This file holds one section for each Culler version, from the newest version to the oldest version.
The release workflow copies the section of a new version into the description of its GitHub release.
The [installation guide](INSTALL.md#versions) gives the version rules.

## 0.2.1

### Changes

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

### Known limits

- Culler supports one operator on one Mac with Apple silicon.

### Database compatibility

- The Strategic rationale field changes the database schema.
- The current update code cannot convert a database of version 0.2.0 or version 0.1.0.

### Update effects

- This test release permits new installations. The updater rejects the incompatible database of an earlier version.

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
