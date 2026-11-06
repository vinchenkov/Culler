---
name: project-wiki
description: Read and preserve project knowledge at its authoritative repository paths.
---

Project knowledge resides in source files, tests, and original project documents.
Each document has one authoritative repository path.
OpenWiki supplies generated explanations and navigation to that knowledge.
Generated explanations cite original evidence directly.
Source files and tests establish implemented behavior.
Approved documents establish intent and decisions.

Each role reads knowledge from its assigned repository version.
Read `openwiki/` from that repository when the Project has a wiki.
Current repository evidence takes precedence over a generated page.
Proposed intent remains distinct from accepted decisions and verified behavior.

Only the Scribe maintains `openwiki/`.
The wiki is read-only for every other role.
The Scribe maintains the control records, the Claim sidecars, the generated pages, and the navigation pages.
The Scribe does this work on a schedule.
A generated page or a navigation page can be behind the accepted commit until the next wiki update.
The Scribe preserves each file outside `openwiki/`.

Original project knowledge remains available when the generated wiki is absent.

The Worker commits requested reports and other original project documents outside `openwiki/`.
Use this priority for document locations outside the wiki:

1. An explicit task requirement identifies the destination.
2. A revision preserves the document's authoritative path.
3. A new document follows the established project convention for its document type.
4. An unclear convention selects an appropriate folder under `docs/`.

Project instructions and comparable current documents establish a convention.
Project navigation provides additional evidence.
A folder name alone supplies insufficient evidence.
Imported projects preserve their authoritative document paths and established formats.
An explicit task can authorize a move.
Routine wiki maintenance preserves those paths.

The task's requested format takes precedence over the default text format, Markdown.
Commit the complete document and its required local assets.
Original documents preserve attribution and relevant source dates.
Project documents follow their own format and metadata conventions.
The Verifier returns substantive document errors to the Worker.

An original project document is a task deliverable or preserves knowledge necessary for later Project work.
A task execution report describes the work, its checks, and its completion evidence.
An execution report belongs in task artifacts even when the task requires that report.
Do not create a project document solely to record task completion or satisfy the commit requirement.

The repository contains durable deliverables and their necessary local evidence.
An attributed external reference can supply independently available evidence.
A unique local capture necessary for a conclusion belongs in the repository.
The accepted project remains understandable after operational artifacts expire.

The `.mc-artifacts/` folder holds operational records across runs and reviews:

- Task execution reports.
- Verifier evidence and correction instructions.
- Operator review packets.
- Intermediate results that another role requires.

The run envelope of a pipeline run identifies each mounted artifact plane under `/mc/artifacts/`.
Each task keeps its artifacts under `/mc/artifacts/<plane>/<task-id>/`.
A Homie container shows an empty `.mc-artifacts/` folder in each Project mount.
A packet conversation reads the artifact files of its task only at the paths in its packet context.
Task artifacts expire 90 days after task closure and the end of active dependencies.
Active tasks and reviews preserve their artifacts.
Temporary files for one run use temporary run storage.
