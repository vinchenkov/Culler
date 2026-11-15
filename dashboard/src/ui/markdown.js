// markdown.js — a small markdown reader for agent-authored renders (the
// markdown packet render), which must read as prose rather than as a wall
// of asterisks.
//
// It parses to a BLOCK TREE, never to an HTML string: app.js builds real DOM
// nodes from the tree, so no path exists by which rendered bytes become
// markup. That is the whole reason this is a tree and not a template — the
// bytes come from an agent's render, and a renderer that emits HTML would
// make every future render an injection surface.
//
// Deliberately partial: headings, paragraphs, lists, fenced code, block
// quotes, rules, and inline code/emphasis/links. Anything it does not know
// survives as literal text, which is the correct failure for a document whose
// author is not this file.

const HEADING = /^(#{1,6})\s+(.*)$/;
const FENCE = /^\s*```(.*)$/;
const RULE = /^\s*(?:-{3,}|\*{3,}|_{3,})\s*$/;
const QUOTE = /^\s*>\s?(.*)$/;
const BULLET = /^\s*[-*+]\s+(.*)$/;
const ORDERED = /^\s*(\d+)[.)]\s+(.*)$/;

/** Inline spans for one line of text. */
export function parseInline(text) {
  const spans = [];
  let buffer = "";
  const flush = () => {
    if (buffer !== "") {
      spans.push({ type: "text", text: buffer });
      buffer = "";
    }
  };
  const source = String(text ?? "");
  let i = 0;
  while (i < source.length) {
    const rest = source.slice(i);
    // Code first: inside a span of backticks nothing else is markup.
    const code = /^`([^`]+)`/.exec(rest);
    if (code) {
      flush();
      spans.push({ type: "code", text: code[1] });
      i += code[0].length;
      continue;
    }
    const image = /^!\[[^\]]*\]\([^)]*\)/.exec(rest);
    if (image) {
      buffer += image[0];
      i += image[0].length;
      continue;
    }
    const link = /^\[([^\]]*)\]\(([^)\s]+)\)/.exec(rest);
    if (link) {
      flush();
      spans.push({ type: "link", text: link[1] === "" ? link[2] : link[1], href: link[2] });
      i += link[0].length;
      continue;
    }
    const strong = /^(\*\*|__)(.+?)\1/.exec(rest);
    if (strong) {
      flush();
      spans.push({ type: "strong", text: strong[2] });
      i += strong[0].length;
      continue;
    }
    const em = /^(\*|_)(?!\s)(.+?)\1/.exec(rest);
    if (em) {
      flush();
      spans.push({ type: "em", text: em[2] });
      i += em[0].length;
      continue;
    }
    buffer += source[i];
    i += 1;
  }
  flush();
  return spans;
}

/** The block tree for a markdown document. */
export function parseMarkdown(src) {
  const lines = String(src ?? "").replace(/\r\n?/g, "\n").split("\n");
  const blocks = [];
  let paragraph = [];
  let quote = [];
  // The open list item, if any. A list item may run over several lines — a
  // packet's own ranked list does — and closing the item at the first line
  // break would split one list into one list per item, renumbering each from 1.
  let item = null;
  let itemOrdered = false;

  const closeParagraph = () => {
    if (paragraph.length > 0) {
      blocks.push({ type: "paragraph", spans: parseInline(paragraph.join(" ")) });
      paragraph = [];
    }
  };
  const closeQuote = () => {
    if (quote.length > 0) {
      blocks.push({ type: "quote", spans: parseInline(quote.join(" ")) });
      quote = [];
    }
  };
  const closeItem = () => {
    if (item === null) return;
    const spans = parseInline(item.join(" "));
    const last = blocks[blocks.length - 1];
    if (last && last.type === "list" && last.ordered === itemOrdered) last.items.push(spans);
    else blocks.push({ type: "list", ordered: itemOrdered, items: [spans] });
    item = null;
  };
  const close = () => {
    closeItem();
    closeParagraph();
    closeQuote();
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    const fence = FENCE.exec(line);
    if (fence) {
      close();
      const body = [];
      i += 1;
      while (i < lines.length && !FENCE.test(lines[i])) {
        body.push(lines[i]);
        i += 1;
      }
      blocks.push({ type: "code", lang: fence[1].trim(), text: body.join("\n") });
      continue;
    }

    if (line.trim() === "") {
      close();
      continue;
    }

    if (RULE.test(line)) {
      close();
      blocks.push({ type: "rule" });
      continue;
    }

    const heading = HEADING.exec(line);
    if (heading) {
      close();
      blocks.push({ type: "heading", level: heading[1].length, spans: parseInline(heading[2]) });
      continue;
    }

    const quoted = QUOTE.exec(line);
    if (quoted) {
      closeItem();
      closeParagraph();
      quote.push(quoted[1]);
      continue;
    }
    closeQuote();

    const bullet = BULLET.exec(line);
    const ordered = ORDERED.exec(line);
    if (bullet || ordered) {
      closeParagraph();
      closeItem();
      itemOrdered = Boolean(ordered);
      item = [(bullet ? bullet[1] : ordered[2]).trim()];
      continue;
    }

    // Lazy continuation: a plain line under an open item belongs to it.
    if (item !== null) {
      item.push(line.trim());
      continue;
    }

    paragraph.push(line.trim());
  }
  close();
  return blocks;
}
