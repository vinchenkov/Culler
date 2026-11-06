# Changelog

This file holds one section for each Culler version, from the newest version to the oldest version.
The release workflow copies the section of a new version into the description of its GitHub release.
The [installation guide](INSTALL.md#versions) gives the version rules.

## 0.2.1

### Changes

- The dashboard browser test waits for profile recovery before it checks Save.
- The test closes the profile dialog after a failure to protect subsequent tests.

### Known limits

- Culler supports one operator on one Mac with Apple silicon.

### Database compatibility

- The database schema is the same as version 0.2.0.
- The current update code cannot convert a 0.1.0 database.

### Update effects

- This test release permits new installations. The updater rejects the incompatible database from 0.1.0.

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
