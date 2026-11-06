---
name: openwiki-documents
description: Read before the first OpenWiki call when the wiki contains original project documents.
---

# Original documents in the wiki

Each original document must meet these requirements:

- It exists in the start commit.
- Its filename differs from `index.md`.
- Its frontmatter contains `document: true`, a title, and a descriptive type.

Correct only its `title`, `type`, `document`, and `tags` fields.
Preserve its body, attribution, path, and task provenance, including when its content contains an error.
Commit metadata repairs before `openwiki_begin`.
Keep these documents outside generated page plans.
For an incompatible document, preserve its content and use the failure procedure in `~/.agents/skills/mc-scribe/SKILL.md`.
