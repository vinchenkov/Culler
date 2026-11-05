---
name: explain-diff-html
description: Use when the user asks for a rich explanation of a code change, diff, branch, or PR. Produces HTML output.
---

# Explain Diff

Produce a rich, interactive explanation of the specified code change.

## Sections

The explanation must have these sections:

- Background. Explain the existing system that is relevant to this change. Explore the surrounding code broadly for this section. The reader's knowledge is unknown. Include a deep background for beginners. State that a reader who knows the system can skip the deep background. Then include a narrow background that is directly relevant to the change.
- Intuition. Explain the core intuition for the code change. Explain the essence. Do not explain the full details. Use concrete examples with toy data. Use many figures and diagrams.
- Code. Give a high-level walkthrough of the changes to the code. Group and order the changes in an understandable way.
- Quiz. Write five questions that test the reader's knowledge of this PR. The questions have medium difficulty. A reader must understand the substance of the PR to answer them. Do not write trick questions. The quiz helps the reader confirm that they understood the PR. Present the questions as interactive multiple-choice questions. When the user clicks an answer, the page tells the user whether the answer is correct and gives feedback.

## Format

- Output one self-contained HTML file. The file includes the CSS and the JavaScript. The whole explanation is one long page with section headers and a table of contents. Do not use tabs for the top-level structure. Include basic responsive CSS so that the user can view the page on a phone.
- Put the file in a global location on the user's computer, outside of the code repository. The file name must start with today's date in `YYYY-MM-DD-` format. This keeps the files time-sorted and out of version control. Example: /tmp/2026-01-12-explanation-<slug>.html
- Write with the clarity and flow of Martin Kleppmann. Write in the classic style. Make the text engaging. Make the transitions between sections smooth.
- Select a small number of diagram families. Reuse them through the explanation to explain the different cases. These kinds of diagrams are useful:
  - A very simplified version of the UI that the user sees in the app, to explain UI changes.
  - A system diagram that shows the data flow or the communication between components. Include example data in this diagram.
- Do not use ASCII diagrams. Use simple HTML designs for the diagrams. Use HTML lists for lists of things.
- Use `<pre>` tags for code blocks. A custom styled div in place of `<pre>` must have `white-space: pre-wrap` in its CSS. Without it, the browser collapses all newlines into one line. Before you save the file, scan each code block in the HTML source. Confirm that its CSS includes `white-space: pre` or `pre-wrap`.
- Use callouts for key concepts, definitions, important edge cases, and similar items.
