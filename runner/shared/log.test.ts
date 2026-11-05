// log.test.ts — the record contract. Every rule here is one a reader or a query
// downstream is entitled to assume: fixed key order, sorted context, protected
// head keys, hard byte caps, one write per record, and a level filter that
// costs the sink nothing.

import { describe, expect, test } from "bun:test";

import {
  EVENTS,
  EVENT_GRAMMAR,
  LEVELS,
  levelEnabled,
  makeLogger,
  makeTestLogger,
  parseLevel,
  renderText,
  serialize,
  type Level,
  type LogRecord,
  type Sink,
} from "./log";

const AT = Date.parse("2026-08-18T09:12:33.412Z");
const frozen = () => AT;

function rec(over: Partial<LogRecord> & Record<string, unknown> = {}): LogRecord {
  return {
    ts: "2026-08-18T09:12:33.412Z",
    level: "warn",
    comp: "resident",
    event: "reap.stop_failed",
    msg: "docker stop refused",
    ...over,
  } as LogRecord;
}

function collector(): Sink & { lines: string[] } {
  const lines: string[] = [];
  return { lines, write: (line) => void lines.push(line) };
}

describe("the event registry", () => {
  test("every name matches the grammar", () => {
    for (const name of EVENTS) expect(name).toMatch(EVENT_GRAMMAR);
  });

  test("holds no duplicates", () => {
    expect(new Set(EVENTS).size).toBe(EVENTS.length);
  });

  // The rule that keeps the registry finite. A name minted per refusal code
  // would show up here first, as a family of near-identical siblings.
  test("carries no identifier-shaped segment", () => {
    for (const name of EVENTS) expect(name).not.toMatch(/\d{3,}|[0-9a-f]{8}/);
  });
});

describe("parseLevel", () => {
  test("never throws, whatever the environment says", () => {
    expect(parseLevel(undefined)).toBe("info");
    expect(parseLevel("")).toBe("info");
    expect(parseLevel("LOUD")).toBe("info");
    expect(parseLevel("  ")).toBe("info");
    expect(parseLevel(undefined, "error")).toBe("error");
  });

  test("matches case-insensitively", () => {
    expect(parseLevel("INFO")).toBe("info");
    expect(parseLevel("Debug")).toBe("debug");
    expect(parseLevel(" warn ")).toBe("warn");
  });
});

describe("levelEnabled", () => {
  test("admits everything at or above the configured level", () => {
    expect(levelEnabled("info", "error")).toBe(true);
    expect(levelEnabled("info", "info")).toBe(true);
    expect(levelEnabled("info", "debug")).toBe(false);
    expect(levelEnabled("error", "warn")).toBe(false);
    expect(levelEnabled("debug", "debug")).toBe(true);
  });

  test("LEVELS is ordered error < warn < info < debug", () => {
    expect([...LEVELS]).toEqual(["error", "warn", "info", "debug"]);
  });
});

describe("serialize", () => {
  test("emits the head keys in fixed order, then context sorted", () => {
    expect(serialize(rec({ zebra: 1, run_id: "ab12", alpha: true }))).toBe(
      '{"ts":"2026-08-18T09:12:33.412Z","level":"warn","comp":"resident","event":"reap.stop_failed",' +
        '"msg":"docker stop refused","alpha":true,"run_id":"ab12","zebra":1}\n',
    );
  });

  test("ends in exactly one newline", () => {
    const line = serialize(rec());
    expect(line.endsWith("}\n")).toBe(true);
    expect(line.split("\n")).toHaveLength(2);
  });

  test("is byte-stable regardless of insertion order", () => {
    expect(serialize(rec({ b: 2, a: 1 }))).toBe(serialize(rec({ a: 1, b: 2 })));
  });

  test("drops a non-scalar and counts it", () => {
    const line = serialize(rec({ nested: { a: 1 }, ok: "yes" }) as LogRecord);
    const parsed = JSON.parse(line);
    expect(parsed.nested).toBeUndefined();
    expect(parsed.ok).toBe("yes");
    expect(parsed.dropped_fields).toBe(1);
  });

  test("drops a non-finite number, which would otherwise serialize as null", () => {
    const parsed = JSON.parse(serialize(rec({ ratio: Number.NaN, count: 3 })));
    expect("ratio" in parsed).toBe(false);
    expect(parsed.count).toBe(3);
    expect(parsed.dropped_fields).toBe(1);
  });

  test("caps msg at 2048 bytes and marks the cut", () => {
    const parsed = JSON.parse(serialize(rec({ msg: "x".repeat(5000) })));
    expect(Buffer.byteLength(parsed.msg, "utf8")).toBeLessThanOrEqual(2048);
    expect(parsed.msg.endsWith("…")).toBe(true);
  });

  test("counts the msg cap in BYTES, not code units", () => {
    // 3 bytes each: 2048 bytes of budget is fewer than 2048 characters.
    const parsed = JSON.parse(serialize(rec({ msg: "☃".repeat(2000) })));
    expect(Buffer.byteLength(parsed.msg, "utf8")).toBeLessThanOrEqual(2048);
    expect(parsed.msg.length).toBeLessThan(2000);
    // A cut that lands mid-sequence must not leave a replacement character.
    expect(parsed.msg).not.toContain("�");
  });

  test("caps the whole line at 8192 bytes, largest context value first", () => {
    const line = serialize(
      rec({ huge: "H".repeat(7000), medium: "M".repeat(3000), small: "s", run_id: "ab12" }),
    );
    expect(Buffer.byteLength(line, "utf8")).toBeLessThanOrEqual(8192);
    const parsed = JSON.parse(line);
    expect(parsed.truncated).toBe(true);
    expect("huge" in parsed).toBe(false);
    expect(parsed.small).toBe("s");
    expect(parsed.run_id).toBe("ab12");
  });

  test("never drops ts, level, comp or event to make room", () => {
    const parsed = JSON.parse(serialize(rec({ msg: "m".repeat(3000), fat: "F".repeat(9000) })));
    expect(parsed.ts).toBe("2026-08-18T09:12:33.412Z");
    expect(parsed.level).toBe("warn");
    expect(parsed.comp).toBe("resident");
    expect(parsed.event).toBe("reap.stop_failed");
  });

  test("honours a caller-supplied line cap", () => {
    const line = serialize(rec({ a: "A".repeat(400), b: "B".repeat(400) }), 512);
    expect(Buffer.byteLength(line, "utf8")).toBeLessThanOrEqual(512);
    expect(JSON.parse(line).truncated).toBe(true);
  });

  test("shrinks msg only after every context field is gone", () => {
    const line = serialize(rec({ msg: "m".repeat(3000), a: "A".repeat(2000) }), 900);
    expect(Buffer.byteLength(line, "utf8")).toBeLessThanOrEqual(900);
    const parsed = JSON.parse(line);
    expect("a" in parsed).toBe(false);
    expect(parsed.msg.endsWith("…")).toBe(true);
  });

  test("an impossible cap terminates at the head instead of looping", () => {
    // The head is not negotiable, so a cap below it cannot be met. What matters
    // is that `serialize` returns rather than shrinking `msg` forever.
    const line = serialize(rec({ msg: "m".repeat(500) }), 1);
    expect(line).toBe(
      '{"ts":"2026-08-18T09:12:33.412Z","level":"warn","comp":"resident","event":"reap.stop_failed","msg":"…","truncated":true}\n',
    );
  });

  test("emits an unregistered event name rather than refusing it", () => {
    // The Event type is the author-time constraint. A throw inside a logger at
    // runtime is worse than a bad label on one line.
    expect(JSON.parse(serialize(rec({ event: "improvised.name" }))).event).toBe("improvised.name");
  });
});

describe("renderText", () => {
  test("renders the documented line", () => {
    expect(
      renderText(rec({ run_id: "ab12", container: "mc-run-ab12", exit: 1 })),
    ).toBe(
      "2026-08-18T09:12:33.412Z WARN  resident reap.stop_failed " +
        "container=mc-run-ab12 exit=1 run_id=ab12 — docker stop refused",
    );
  });

  test("pads the level to five characters", () => {
    expect(renderText(rec({ level: "info" as Level }))).toContain(" INFO  resident ");
    expect(renderText(rec({ level: "error" as Level }))).toContain(" ERROR resident ");
  });

  test("omits the pair section entirely when there is no context", () => {
    expect(renderText(rec())).toBe(
      "2026-08-18T09:12:33.412Z WARN  resident reap.stop_failed — docker stop refused",
    );
    expect(renderText(rec())).not.toContain("  —");
  });
});

describe("makeLogger", () => {
  test("stamps ts from the injected clock, in ISO-8601 Z with milliseconds", () => {
    const sink = collector();
    makeLogger({ comp: "resident", level: "info", sink, now: frozen }).info("tick.started", "go");
    expect(JSON.parse(sink.lines[0]!).ts).toBe("2026-08-18T09:12:33.412Z");
  });

  test("writes exactly once per record, newline inside that one string", () => {
    let calls = 0;
    let seen = "";
    const log = makeLogger({
      comp: "resident",
      level: "info",
      now: frozen,
      sink: {
        write(line) {
          calls++;
          seen = line;
        },
      },
    });
    log.info("tick.started", "go", { run_id: "ab12" });
    expect(calls).toBe(1);
    expect(seen.endsWith("\n")).toBe(true);
  });

  test("does not touch the sink for a record below the configured level", () => {
    let calls = 0;
    const log = makeLogger({ comp: "resident", level: "warn", now: frozen, sink: { write: () => void calls++ } });
    log.debug("tick.started", "quiet");
    log.info("tick.started", "quiet");
    expect(calls).toBe(0);
    log.warn("tick.failed", "loud");
    log.error("tick.failed", "louder");
    expect(calls).toBe(2);
  });

  test("drops a context key that collides with a head key and counts it", () => {
    const sink = collector();
    const log = makeLogger({ comp: "resident", level: "info", sink, now: frozen });
    log.info("tick.started", "go", { comp: "impostor", ts: "1970", run_id: "ab12" } as never);
    const parsed = JSON.parse(sink.lines[0]!);
    expect(parsed.comp).toBe("resident");
    expect(parsed.ts).toBe("2026-08-18T09:12:33.412Z");
    expect(parsed.run_id).toBe("ab12");
    expect(parsed.dropped_fields).toBe(2);
  });

  test("drops a non-scalar context value the same way", () => {
    const sink = collector();
    const log = makeLogger({ comp: "resident", level: "info", sink, now: frozen });
    log.info("tick.started", "go", { plan: { steps: 3 }, at: 1 } as never);
    const parsed = JSON.parse(sink.lines[0]!);
    expect("plan" in parsed).toBe(false);
    expect(parsed.at).toBe(1);
    expect(parsed.dropped_fields).toBe(1);
  });
});

describe("child", () => {
  test("merges child context under the per-call context", () => {
    const sink = collector();
    const log = makeLogger({ comp: "resident", level: "info", sink, now: frozen });
    log.child({ run_id: "ab12", tier: "pipeline" }).info("spawn.started", "up", { tier: "homie" });
    const parsed = JSON.parse(sink.lines[0]!);
    expect(parsed.run_id).toBe("ab12");
    expect(parsed.tier).toBe("homie");
  });

  test("is chainable", () => {
    const sink = collector();
    const log = makeLogger({ comp: "resident", level: "info", sink, now: frozen });
    log.child({ run_id: "ab12" }).child({ attempt: 2 }).info("spawn.started", "up");
    const parsed = JSON.parse(sink.lines[0]!);
    expect(parsed.run_id).toBe("ab12");
    expect(parsed.attempt).toBe(2);
  });

  test("leaves the parent's context alone", () => {
    const sink = collector();
    const log = makeLogger({ comp: "resident", level: "info", sink, now: frozen });
    log.child({ run_id: "ab12" }).info("spawn.started", "up");
    log.info("tick.started", "go");
    expect("run_id" in JSON.parse(sink.lines[1]!)).toBe(false);
  });
});

describe("makeTestLogger", () => {
  test("keeps both the lines and the parsed records", () => {
    const log = makeTestLogger("runner");
    log.info("runner.started", "up", { run_id: "ab12" });
    log.debug("runner.turn_started", "turn 1");
    expect(log.lines).toHaveLength(2);
    expect(log.records[0]!.comp).toBe("runner");
    expect(log.records[0]!.run_id).toBe("ab12");
    expect(log.records[1]!.level).toBe("debug");
  });

  test("captures what a child writes as well", () => {
    const log = makeTestLogger();
    log.child({ run_id: "ab12" }).error("reap.stop_failed", "no");
    expect(log.records[0]!.run_id).toBe("ab12");
  });
});
