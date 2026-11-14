---
name: project-wiki
description: Read before repository work or wiki questions. The ownership and document boundaries form part of the Culler contract.
---

## Project knowledge

Each role reads knowledge from its assigned repository version.
Read `openwiki/` from that repository when the Project has a wiki.
Use current source files and tests to establish behavior.
Use approved documents for accepted intent and decisions.
Keep proposed intent distinct from accepted decisions.
Current repository evidence takes precedence over a generated page.
The wiki can lag behind the accepted commit until the next scheduled update.

Treat `openwiki/` as read-only unless you are the Scribe.

## Original project documents

Requested project reports and other original documents are repository deliverables outside `openwiki/`.
Select their paths in this order:

1. Use the destination that the task specifies.
2. Preserve an existing document's authoritative path unless the task authorizes a move.
3. Follow the Project's convention for that document type.
4. Use an appropriate folder under `docs/` when no convention applies.

Establish conventions from Project instructions and comparable current documents.
A folder name alone does not establish a convention.
Use the task's requested format.
Otherwise, preserve an existing document's format or follow the Project convention.
Use Markdown when no requested or established format applies.
Follow the Project's metadata conventions for that format.
Preserve attribution and relevant source dates.
Include the document's required local assets and evidence in the commit.
An attributed external reference can supply independently available evidence.
Retain a unique local capture in the repository when a conclusion requires it.

## Task execution reports

Use task artifacts for reports about task execution, checks, and completion evidence.
Do not create a project document solely to record task completion or satisfy the commit requirement.
Use the command skill for artifact paths and submission requirements.
