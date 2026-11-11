---
name: openwiki-documents
description: Read before the first OpenWiki call when `openwiki/` holds an original project document, such as a research report. The rules for those documents.
---

# Original documents in the wiki

Each original document must meet these requirements:

- It exists in the `Start commit` of the **Git state** part.
- Its filename differs from `index.md`.
- Its frontmatter contains `document: true`, a title, and a descriptive type.

Correct only its `title`, `type`, `document`, and `tags` fields.
Preserve its body, attribution, path, and task provenance, including when its content contains an error.
Commit metadata repairs before `openwiki_begin`.
Keep these documents outside generated page plans.
For a document that cannot meet these requirements, preserve its content.
Then use the terminal action in the "Report a failure" section of `~/.agents/skills/mc-scribe/SKILL.md`.
