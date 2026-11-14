// behavior.test.ts — validator coverage for the fail-closed behavior
// schema (README.md "Behavior-file schema"). Every accept class and every
// reject class the validator distinguishes gets a case.

import { describe, expect, test } from "bun:test";
import { BehaviorError, parseBehavior } from "./behavior";

function accepts(doc: unknown) {
  return parseBehavior(JSON.stringify(doc));
}

/** Assert rejection and return the message for shape checks. */
function rejects(doc: unknown, msgPart: string) {
  const text = typeof doc === "string" ? doc : JSON.stringify(doc);
  let caught: unknown;
  try {
    parseBehavior(text);
  } catch (e) {
    caught = e;
  }
  expect(caught).toBeInstanceOf(BehaviorError);
  expect((caught as Error).message).toStartWith("invalid behavior file: ");
  expect((caught as Error).message).toContain(msgPart);
}

const SUCCEED = { do: "succeed", output: "done" };

describe("accepts", () => {
  test("minimal succeed-only behavior", () => {
    const b = accepts({ steps: [SUCCEED] });
    expect(b.steps).toHaveLength(1);
    expect(b.session_id).toBeUndefined();
    expect(b.fixed_ts).toBeUndefined();
  });

  test("crash-only and hang-only terminals", () => {
    expect(accepts({ steps: [{ do: "crash", code: 7 }] }).steps[0].do).toBe("crash");
    expect(accepts({ steps: [{ do: "hang" }] }).steps[0].do).toBe("hang");
  });

  test("crash code bounds 1 and 255 are legal", () => {
    accepts({ steps: [{ do: "crash", code: 1 }] });
    accepts({ steps: [{ do: "crash", code: 255 }] });
  });

  test("full document: session_id, fixed_ts, exec + sleep then terminal", () => {
    const b = accepts({
      session_id: "s-1",
      fixed_ts: "2026-01-01T00:00:00.000Z",
      steps: [
        { do: "exec", command: "true" },
        { do: "sleep", seconds: 0.25 },
        { do: "exec", command: "false" },
        SUCCEED,
      ],
    });
    expect(b.session_id).toBe("s-1");
    expect(b.fixed_ts).toBe("2026-01-01T00:00:00.000Z");
    expect(b.steps.map((s) => s.do)).toEqual(["exec", "sleep", "exec", "succeed"]);
  });

  test("sleep seconds 0 and fractional are legal", () => {
    accepts({ steps: [{ do: "sleep", seconds: 0 }, SUCCEED] });
    accepts({ steps: [{ do: "sleep", seconds: 2.5 }, SUCCEED] });
  });

  test("succeed output may be empty string (typed, not truthy-checked)", () => {
    accepts({ steps: [{ do: "succeed", output: "" }] });
  });
});

describe("rejects: document shape", () => {
  test("not valid JSON", () => {
    rejects("{nope", "not valid JSON");
  });

  test("top level must be an object: array, string, number, null", () => {
    rejects([SUCCEED], "top level must be a JSON object");
    rejects('"steps"', "top level must be a JSON object");
    rejects("42", "top level must be a JSON object");
    rejects("null", "top level must be a JSON object");
  });

  test("unknown top-level key", () => {
    rejects({ steps: [SUCCEED], extra: 1 }, 'unknown top-level key "extra"');
  });

  test("session_id must be a string", () => {
    rejects({ session_id: 5, steps: [SUCCEED] }, "session_id must be a string");
  });

  test("fixed_ts must be a string and parseable as a timestamp", () => {
    rejects({ fixed_ts: 12345, steps: [SUCCEED] }, "fixed_ts must be an ISO-8601");
    rejects({ fixed_ts: "not-a-time", steps: [SUCCEED] }, "fixed_ts must be an ISO-8601");
  });

  test("steps missing, empty, or not an array", () => {
    rejects({}, "steps must be a non-empty array");
    rejects({ steps: [] }, "steps must be a non-empty array");
    rejects({ steps: "succeed" }, "steps must be a non-empty array");
    rejects({ steps: { do: "succeed" } }, "steps must be a non-empty array");
  });
});

describe("rejects: step shape", () => {
  test("step must be an object", () => {
    rejects({ steps: ["succeed"] }, "steps[0] must be an object");
    rejects({ steps: [null] }, "steps[0] must be an object");
    rejects({ steps: [[SUCCEED]] }, "steps[0] must be an object");
  });

  test("do missing or unknown kind", () => {
    rejects({ steps: [{ output: "x" }] }, "steps[0].do must be one of");
    rejects({ steps: [{ do: "explode" }] }, "steps[0].do must be one of");
    rejects({ steps: [{ do: 3 }] }, "steps[0].do must be one of");
  });

  test("unknown key per kind (cross-kind key smuggling rejected)", () => {
    rejects({ steps: [{ do: "succeed", output: "x", code: 1 }] }, 'has unknown key "code"');
    rejects({ steps: [{ do: "exec", command: "true", output: "x" }, SUCCEED] }, 'has unknown key "output"');
    rejects({ steps: [{ do: "hang", seconds: 1 }] }, 'has unknown key "seconds"');
    rejects({ steps: [{ do: "crash", code: 1, command: "x" }] }, 'has unknown key "command"');
    rejects({ steps: [{ do: "sleep", seconds: 1, do2: 1 }, SUCCEED] }, 'has unknown key "do2"');
  });

  test("succeed requires string output", () => {
    rejects({ steps: [{ do: "succeed" }] }, 'requires string "output"');
    rejects({ steps: [{ do: "succeed", output: 42 }] }, 'requires string "output"');
  });

  test("exec requires non-empty string command", () => {
    rejects({ steps: [{ do: "exec" }, SUCCEED] }, 'non-empty string "command"');
    rejects({ steps: [{ do: "exec", command: "" }, SUCCEED] }, 'non-empty string "command"');
    rejects({ steps: [{ do: "exec", command: ["ls"] }, SUCCEED] }, 'non-empty string "command"');
  });

  test("sleep requires number seconds >= 0 (negative, NaN, string, missing)", () => {
    rejects({ steps: [{ do: "sleep", seconds: -1 }, SUCCEED] }, '"seconds" >= 0');
    rejects({ steps: [{ do: "sleep", seconds: "2" }, SUCCEED] }, '"seconds" >= 0');
    rejects({ steps: [{ do: "sleep" }, SUCCEED] }, '"seconds" >= 0');
    // NaN survives typeof === "number"; the >= 0 guard must catch it.
    rejects('{"steps":[{"do":"sleep","seconds":null},{"do":"succeed","output":""}]}', '"seconds" >= 0');
  });

  test("crash requires integer code in 1..255 (0, 256, float, string)", () => {
    rejects({ steps: [{ do: "crash", code: 0 }] }, '"code" in 1..255');
    rejects({ steps: [{ do: "crash", code: 256 }] }, '"code" in 1..255');
    rejects({ steps: [{ do: "crash", code: 1.5 }] }, '"code" in 1..255');
    rejects({ steps: [{ do: "crash", code: "1" }] }, '"code" in 1..255');
    rejects({ steps: [{ do: "crash" }] }, '"code" in 1..255');
  });
});

describe("rejects: terminal placement", () => {
  test("terminal step mid-list (each terminal kind)", () => {
    rejects({ steps: [SUCCEED, { do: "exec", command: "true" }] }, "terminal but not the last step");
    rejects({ steps: [{ do: "crash", code: 1 }, SUCCEED] }, "terminal but not the last step");
    rejects({ steps: [{ do: "hang" }, SUCCEED] }, "terminal but not the last step");
    rejects({ steps: [SUCCEED, SUCCEED] }, "terminal but not the last step");
  });

  test("non-terminal last step (exec, sleep)", () => {
    rejects({ steps: [{ do: "exec", command: "true" }] }, "last step must be terminal");
    rejects({ steps: [{ do: "sleep", seconds: 1 }] }, "last step must be terminal");
    rejects({ steps: [{ do: "exec", command: "true" }, { do: "sleep", seconds: 0 }] }, "last step must be terminal");
  });
});
