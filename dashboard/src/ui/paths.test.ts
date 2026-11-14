// paths.test.ts — the display rewrite that makes a packet's own citations
// openable. It is tested because a rewrite that silently stops matching shows
// the operator a path it cannot open while looking exactly as correct as one
// it can.

import { describe, expect, test } from "bun:test";

import { hostPaths } from "./paths.js";

const roots = { "llm-council": "/Users/op/dev/llm-council/.mc-artifacts" };

describe("hostPaths", () => {
  test("rewrites a cited container path onto the host", () => {
    expect(hostPaths("see /mc/artifacts/llm-council/33/verification/gates.md", roots)).toBe(
      "see /Users/op/dev/llm-council/.mc-artifacts/33/verification/gates.md",
    );
  });

  test("rewrites every occurrence, not just the first", () => {
    const body = "/mc/artifacts/llm-council/33/packet/p.md and /mc/artifacts/llm-council/33/output/o.md";
    expect(hostPaths(body, roots).split("/Users/op").length - 1).toBe(2);
  });

  // An unmapped target is left alone on purpose: the container path is at
  // least true, while a fabricated host path is not.
  test("leaves an unmapped target exactly as written", () => {
    expect(hostPaths("/mc/artifacts/other/1/packet/p.md", roots)).toBe("/mc/artifacts/other/1/packet/p.md");
  });

  test("leaves the plane root alone when no target segment follows", () => {
    expect(hostPaths("write under /mc/artifacts", roots)).toBe("write under /mc/artifacts");
  });

  // The lookahead is what makes a bare plane directory not a citation. Without
  // it, prose naming the plane itself would be rewritten into a host directory
  // the sentence was not talking about.
  test("leaves a bare plane directory alone", () => {
    expect(hostPaths("the llm-council plane is /mc/artifacts/llm-council", roots)).toBe(
      "the llm-council plane is /mc/artifacts/llm-council",
    );
  });

  test("passes the text through untouched when no map has loaded", () => {
    const body = "/mc/artifacts/llm-council/33/packet/p.md";
    expect(hostPaths(body, null)).toBe(body);
  });

  test("touches nothing outside the plane", () => {
    expect(hostPaths("/workspace/source/main.go and /mc/outputs/x", roots)).toBe(
      "/workspace/source/main.go and /mc/outputs/x",
    );
  });
});
