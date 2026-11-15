import { describe, expect, test } from "bun:test";
import { parseInline, parseMarkdown } from "./markdown.js";

describe("markdown inline", () => {
  test("reads code, links, strong and emphasis", () => {
    expect(parseInline("plain `code` text")).toEqual([
      { type: "text", text: "plain " },
      { type: "code", text: "code" },
      { type: "text", text: " text" },
    ]);
    expect(parseInline("[docs](https://example.test/x)")).toEqual([
      { type: "link", text: "docs", href: "https://example.test/x" },
    ]);
    expect(parseInline("**bold** and _thin_")).toEqual([
      { type: "strong", text: "bold" },
      { type: "text", text: " and " },
      { type: "em", text: "thin" },
    ]);
  });

  test("markup inside a code span stays literal", () => {
    expect(parseInline("`**not bold**`")).toEqual([{ type: "code", text: "**not bold**" }]);
  });

  test("unsupported image syntax remains literal text", () => {
    const text = "Tools: ![**diagram**](https://example.test/image.png)";
    expect(parseInline(text)).toEqual([{ type: "text", text }]);
  });

  test("an unclosed marker survives as text", () => {
    expect(parseInline("2 * 3 * 4")).toEqual([{ type: "text", text: "2 * 3 * 4" }]);
    expect(parseInline("")).toEqual([]);
  });
});

describe("markdown blocks", () => {
  test("headings, paragraphs, rules and quotes", () => {
    const blocks = parseMarkdown("# Review packet\n\nOne line.\nStill one paragraph.\n\n---\n\n> a quote\n");
    expect(blocks[0]).toEqual({ type: "heading", level: 1, spans: [{ type: "text", text: "Review packet" }] });
    expect(blocks[1]).toEqual({
      type: "paragraph",
      spans: [{ type: "text", text: "One line. Still one paragraph." }],
    });
    expect(blocks[2]).toEqual({ type: "rule" });
    expect(blocks[3]).toEqual({ type: "quote", spans: [{ type: "text", text: "a quote" }] });
  });

  test("bulleted and numbered lists gather their items", () => {
    const blocks = parseMarkdown("- one\n- two\n\n1. first\n2. second\n");
    expect(blocks[0].type).toBe("list");
    expect(blocks[0].ordered).toBe(false);
    expect(blocks[0].items).toHaveLength(2);
    expect(blocks[1].ordered).toBe(true);
    expect(blocks[1].items[1]).toEqual([{ type: "text", text: "second" }]);
  });

  test("a fenced block keeps its bytes verbatim", () => {
    const blocks = parseMarkdown("text\n\n```sh\nmc packet list\n# not a heading\n```\n\nafter\n");
    expect(blocks[1]).toEqual({ type: "code", lang: "sh", text: "mc packet list\n# not a heading" });
    expect(blocks[2].type).toBe("paragraph");
  });

  test("an empty document is an empty tree", () => {
    expect(parseMarkdown("")).toEqual([]);
    expect(parseMarkdown(null)).toEqual([]);
  });
});

describe("markdown list continuation", () => {
  test("a wrapped list item stays one item of one list", () => {
    const blocks = parseMarkdown(
      "1. **First** — 7h in queue.\n   Approving it closes the loudest question.\n2. **Second** — ready for review.\n",
    );
    expect(blocks).toHaveLength(1);
    expect(blocks[0].type).toBe("list");
    expect(blocks[0].ordered).toBe(true);
    expect(blocks[0].items).toHaveLength(2);
    // The continuation line rides with the item it continues.
    expect(blocks[0].items[0].map((s: { text: string }) => s.text).join("")).toContain(
      "Approving it closes",
    );
  });

  test("a blank line ends the item and the list", () => {
    const blocks = parseMarkdown("- one\n\nafter the list\n");
    expect(blocks[0].type).toBe("list");
    expect(blocks[1]).toEqual({ type: "paragraph", spans: [{ type: "text", text: "after the list" }] });
  });

  test("a heading right after an item closes the list", () => {
    const blocks = parseMarkdown("- one\n# Head\n");
    expect(blocks.map((b: { type: string }) => b.type)).toEqual(["list", "heading"]);
  });
});
