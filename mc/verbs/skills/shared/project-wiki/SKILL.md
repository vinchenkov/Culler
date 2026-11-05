---
name: project-wiki
description: Read and preserve project knowledge at its authoritative repository paths.
---

OpenWiki supplies the code map and access to the project knowledgebase.
The knowledgebase includes complete project documents and generated explanations.
Each document has one authoritative repository path.
Generated explanations cite original evidence directly.
Source files and tests establish implemented behavior.
Approved documents establish intent and decisions.

Each role reads knowledge from its assigned repository version.
Read `openwiki/` from that repository when the Project has a wiki.
Current repository evidence takes precedence over a generated page.
Proposed intent remains distinct from accepted decisions and verified behavior.

The Worker uses this priority for document locations:

1. An explicit task requirement identifies the destination.
2. A revision preserves the document's authoritative path.
3. A new document follows the established project convention for its document type.
4. An unclear convention selects an appropriate folder under `openwiki/`.

Project instructions and comparable current documents establish a convention.
Project navigation provides additional evidence.
A folder name alone supplies insufficient evidence.
Imported projects preserve their authoritative document paths and established formats.
An explicit task can authorize a move.
Routine wiki maintenance preserves those paths.

The Worker commits complete knowledge work directly at its intended repository path.
A new Project uses `openwiki/` by default.
Markdown supplies the default text format.
Original Markdown documents under `openwiki/` declare these fields:

- `document: true` identifies an original project document.
- `title` identifies its subject.
- `type` identifies its document type.
- `task_id` identifies the original task for a task document.
- `task_status` identifies that task's actual status at this version.

Original documents preserve attribution and relevant source dates.
OpenWiki owns generated `index.md` files and its control records.
Original documents use other filenames.
The Scribe maintains the control records, the Claim sidecars, the generated pages, and the navigation pages.
The Scribe does this work on a schedule.
A generated page or a navigation page can be behind the accepted commit until the next wiki update.
The Scribe preserves the body of each project document and each file outside `openwiki/`.
The Verifier returns substantive document errors to the Worker.

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
