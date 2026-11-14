// golden.test.ts — the other half of the cross-language parity guard.
//
// One wire format, two implementations: this module and mc/mclog in Go. They
// were byte-identical the day they were written, and nothing about that
// survives on its own — the next person to widen a field, change how a null
// renders, or "tidy" the level padding will do it in one language and ship a
// log the other half cannot read the same way.
//
// testdata/render-golden.json is read by BOTH this file and
// mc/deployment/log_parity_test.go, so a change that suits one implementation
// fails in the other. Two hand-maintained copies of the expectations would just
// move the drift somewhere quieter.
//
// If you are here because this test failed: do not edit the fixture to match
// your change. Change both implementations, regenerate, and confirm the Go
// suite agrees — `mise exec -- go test -count=1 ./deployment/` from mc/.

import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

import { renderText, serialize } from "./log.js";

interface Golden {
  ndjson: string;
  text: string;
}

const golden: Golden[] = JSON.parse(
  readFileSync(join(import.meta.dir, "testdata", "render-golden.json"), "utf8"),
);

describe("cross-language render parity", () => {
  test("the fixture is not empty", () => {
    // An empty fixture passes every assertion below while proving nothing, and
    // it is exactly what a botched regeneration leaves behind.
    expect(golden.length).toBeGreaterThan(0);
  });

  test("every golden line renders to its golden text", () => {
    for (const { ndjson, text } of golden) {
      expect(renderText(JSON.parse(ndjson))).toBe(text);
    }
  });

  // renderText sorts on read, so it cannot catch a writer whose key order
  // drifted. serialize is the writer, and re-serializing a golden record must
  // reproduce the golden line byte for byte.
  test("re-serializing a golden record reproduces the golden line", () => {
    for (const { ndjson } of golden) {
      expect(serialize(JSON.parse(ndjson))).toBe(`${ndjson}\n`);
    }
  });

  test("the golden set still covers the cases it was built to cover", () => {
    const lines = golden.map((g) => g.ndjson);
    const levels = new Set(golden.map((g) => JSON.parse(g.ndjson).level));
    // All four levels, because the text renderer pads the level and ERROR is
    // the one width that does not need padding.
    expect([...levels].sort()).toEqual(["debug", "error", "info", "warn"]);
    // A record with no context at all — the branch that must not leave a
    // double space where the pair section would have been.
    expect(lines.some((l) => Object.keys(JSON.parse(l)).length === 5)).toBe(true);
    // The three scalar shapes that serialize differently in Go and JS.
    expect(lines.some((l) => l.includes(":null"))).toBe(true);
    expect(lines.some((l) => l.includes(":true"))).toBe(true);
    expect(lines.some((l) => /:\d/.test(l))).toBe(true);
    // An em dash inside msg, so the " — " separator cannot be found by
    // scanning for the first dash.
    expect(lines.some((l) => JSON.parse(l).msg.includes("—"))).toBe(true);
    // The empty message, which must still render its separator.
    expect(lines.some((l) => JSON.parse(l).msg === "")).toBe(true);
  });
});
