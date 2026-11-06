# Changelog

This file holds one section for each Culler version, from the newest version to the oldest version.
The release workflow copies the section of a new version into the description of its GitHub release.
The [installation guide](INSTALL.md#versions) gives the version rules.

## 0.1.0

### Changes

- Version 0.1.0 is the first version of Culler.
- Culler is a local-first orchestration system for AI coding agents, with one human operator.
- `culler version`, `culler update --check`, setup, and the dashboard show the version and the short commit of each release.
- Public updates verify one complete release and preserve durable user data.
- The updater warns before it interrupts active work and preserves the saved Project pause choices.
- An incomplete update supports a retry with its exact candidate or a checkpoint restore before commit.
- The updater removes previous software after commit and preserves selected Project images.

### Known limits

- Culler is in early access. A 0.x minor version can change the public interface.
- Culler supports one operator on one Mac with Apple silicon.
- Culler refuses a different database schema until a tested conversion exists.
- External providers can change models or authentication after the local compatibility checks.

### Database compatibility

Version 0.1.0 is the first version. Each install of version 0.1.0 starts with a new database.
Version 0.1.0 establishes the public compatibility baseline.
Every supported update preserves durable user data.
The initial update implementation accepts unchanged database, configuration, and credential formats.

### Update effects

Version 0.1.0 is the first version, so no update to version 0.1.0 exists.
An internal source installation requires a separate deployment home for the first public release.
Future public updates stop services and active containers after confirmation.
They verify the candidate before they permit work.
An incomplete update preserves its exact recovery artifacts until recovery completes.
