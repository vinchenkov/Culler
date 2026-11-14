// Unit tests for logs.js — the System Logs page's pure logic. The browser
// still parses no line: levels and events come from the verb's own ALIGNED
// records, display text from the verb's rendering, and the prose streams stay
// entirely opaque.

import { describe, expect, test } from "bun:test";
import {
  LOG_UNITS,
  LOG_STREAMS,
  LOG_LEVELS,
  RUN_LANE,
  UNIT_OPTIONS,
  streamsFor,
  TAIL_DEFAULT,
  TAIL_MAX,
  logsQuery,
  nextTail,
  canLoadOlder,
  levelCounts,
  logsView,
  parseLogsHash,
  logsHash,
  copyText,
  runOptions,
  processRuns,
  validRunID,
} from "./logs.js";
import { localStamp } from "./format.js";

const WINDOW = ["boot ok", "WARN disk slow", "warn again", "quiet"];

/** A structured window the way the verb answers `--format json`: raw lines,
 * aligned rendering, aligned records with null in a malformed line's slot. */
const STRUCTURED = {
  unit: "resident",
  stream: "events",
  format: "json",
  exists: true,
  truncated: false,
  lines: ['{"level":"info"}', '{"level":"warn"}', "Abort trap: 6", '{"level":"error"}'],
  rendered: ["T INFO resident tick.completed — done", "T WARN resident reap.stop_failed — slow", "Abort trap: 6", "T ERROR resident spawn.failed — boom"],
  records: [
    { ts: "t1", level: "info", comp: "resident", event: "tick.completed" },
    { ts: "t2", level: "warn", comp: "resident", event: "reap.stop_failed" },
    null,
    { ts: "t3", level: "error", comp: "resident", event: "spawn.failed" },
  ],
  malformed: 1,
};

describe("pickers", () => {
  test("the tables mirror the verb, events first, run lane last", () => {
    expect(LOG_UNITS).toEqual(["resident", "dashboard", "agentsview", "mc"]);
    expect(UNIT_OPTIONS).toEqual(["resident", "dashboard", "agentsview", "mc", "run"]);
    expect(LOG_STREAMS).toEqual(["events", "stderr", "stdout", "debug"]);
    expect(LOG_LEVELS).toEqual(["error", "warn", "info", "debug"]);
    // Per unit, because mc is not supervised and has no launchd redirect: a
    // flat list would let the picker compose a pair the verb refuses.
    expect(streamsFor("resident")).toEqual(["events", "stderr", "stdout"]);
    expect(streamsFor("mc")).toEqual(["events"]);
    expect(streamsFor("agentsview")).toEqual(["debug", "stderr", "stdout"]);
    expect(streamsFor(RUN_LANE)).toEqual(["events"]);
    expect(streamsFor("nonsense")).toEqual(streamsFor("resident"));
    expect(TAIL_DEFAULT).toBe(500);
    expect(TAIL_MAX).toBe(2000);
  });
});

describe("logsQuery", () => {
  test("builds the query for a valid pick — prose streams stay text", () => {
    expect(logsQuery({ unit: "dashboard", stream: "stdout", tail: 50 })).toBe(
      "/api/logs/dashboard?stream=stdout&tail=50",
    );
  });

  test("the AgentsView log is plain text in each of its streams, and the debug stream opens first", () => {
    expect(logsQuery({ unit: "agentsview", stream: "events" })).toBe("/api/logs/agentsview?stream=debug&tail=500");
    expect(logsQuery({ unit: "agentsview" })).toBe("/api/logs/agentsview?stream=debug&tail=500");
    expect(logsQuery({ unit: "agentsview", stream: "stderr", tail: 50 })).toBe("/api/logs/agentsview?stream=stderr&tail=50");
    expect(logsQuery({ unit: "agentsview", stream: "stdout" })).toBe("/api/logs/agentsview?stream=stdout&tail=500");
    expect(parseLogsHash("#logs/agentsview")).toEqual({ unit: "agentsview", stream: "debug", run: null });
    expect(parseLogsHash("#logs/agentsview/stderr")).toEqual({ unit: "agentsview", stream: "stderr", run: null });
    expect(parseLogsHash("#logs/agentsview/events")).toBeNull();
    expect(logsHash({ unit: "agentsview", stream: "events" })).toBe("#logs/agentsview/debug");
  });

  test("the structured stream asks for records beside the rendering", () => {
    expect(logsQuery({ unit: "resident", stream: "events", tail: 50 })).toBe(
      "/api/logs/resident?stream=events&tail=50&format=json",
    );
  });

  test("invalid picks fall back to the defaults rather than reaching the server", () => {
    expect(logsQuery({ unit: "../etc", stream: "syslog", tail: 0 })).toBe(
      "/api/logs/resident?stream=events&tail=500&format=json",
    );
    expect(logsQuery()).toBe("/api/logs/resident?stream=events&tail=500&format=json");
    expect(logsQuery({ tail: 1.5 })).toBe("/api/logs/resident?stream=events&tail=500&format=json");
  });

  test("tail clamps to the verb's own maximum", () => {
    expect(logsQuery({ tail: 99999 })).toBe("/api/logs/resident?stream=events&tail=2000&format=json");
  });

  test("the run lane names its run in the path, encoded, structured", () => {
    expect(logsQuery({ unit: RUN_LANE, run: "ab12", tail: 50 })).toBe(
      "/api/logs/run/ab12?tail=50&format=json",
    );
  });

  test("the run lane with no run picked has nothing to ask for", () => {
    expect(logsQuery({ unit: RUN_LANE })).toBeNull();
    expect(logsQuery({ unit: RUN_LANE, run: "" })).toBeNull();
    // A token outside the safe-id shape is dropped before any fetch; the
    // verb's own fences stay the authority for anything that passes.
    expect(logsQuery({ unit: RUN_LANE, run: "../etc" })).toBeNull();
  });
});

describe("validRunID", () => {
  test("mirrors the resident's safe-id shape", () => {
    expect(validRunID("ab12")).toBe(true);
    expect(validRunID("run_1.2-x")).toBe(true);
    expect(validRunID("")).toBe(false);
    expect(validRunID(".hidden")).toBe(false);
    expect(validRunID("a/b")).toBe(false);
    expect(validRunID(null)).toBe(false);
    expect(validRunID("x".repeat(65))).toBe(false);
  });
});

describe("nextTail / canLoadOlder", () => {
  test("adds 500 lines", () => {
    expect(nextTail(500)).toBe(1000);
    expect(nextTail(1000)).toBe(1500);
  });

  test("clamps at the maximum", () => {
    expect(nextTail(1500)).toBe(TAIL_MAX);
    expect(nextTail(TAIL_MAX)).toBe(TAIL_MAX);
  });

  test("an absent or invalid current widens from the default window", () => {
    expect(nextTail()).toBe(1000);
    expect(nextTail(null)).toBe(1000);
    expect(nextTail(1.5)).toBe(1000);
    expect(nextTail(-10)).toBe(1000);
  });

  test("the affordance disables itself at the clamp", () => {
    expect(canLoadOlder()).toBe(true);
    expect(canLoadOlder(200)).toBe(true);
    expect(canLoadOlder(1999)).toBe(true);
    expect(canLoadOlder(TAIL_MAX)).toBe(false);
    expect(canLoadOlder(9999)).toBe(false);
  });
});

describe("logsView", () => {
  test("a log not yet written is a state, not an error", () => {
    const v = logsView({ unit: "resident", stream: "stdout", exists: false, lines: [], truncated: false });
    expect(v.state).toBe("missing");
    expect(v.note).toBe("no resident stdout log yet — the unit has not written under supervision");
    expect(v.lines).toEqual([]);
    expect(v.total).toBe(0);
    expect(v.shown).toBe(0);
  });

  test("a run that captured nothing is a state with its own sentence", () => {
    const v = logsView({ run_id: "ab12", exists: false, lines: [] });
    expect(v.state).toBe("missing");
    expect(v.note).toBe("no diagnostics were captured for run ab12 — nothing wrote to its log bind");
  });

  test("an absent response is missing too, with unknown pickers", () => {
    const v = logsView(undefined);
    expect(v.state).toBe("missing");
    expect(v.unit).toBe("?");
    expect(v.stream).toBe("?");
    expect(v.note).toContain("no ? ? log yet");
  });

  test("an existing but empty log is its own state", () => {
    const v = logsView({ unit: "dashboard", stream: "stderr", exists: true, lines: [], truncated: false });
    expect(v.state).toBe("empty");
    expect(v.note).toBe("the log is empty");
    expect(v.total).toBe(0);
  });

  test("ok carries the whole window, numbered, unmatched", () => {
    const v = logsView({ unit: "resident", stream: "stderr", exists: true, lines: WINDOW, truncated: false });
    expect(v.state).toBe("ok");
    expect(v.note).toBe("");
    expect(v.total).toBe(4);
    expect(v.shown).toBe(4);
    expect(v.lines.map((l) => l.n)).toEqual([1, 2, 3, 4]);
    expect(v.lines[1]).toMatchObject({ n: 2, text: "WARN disk slow", match: false, level: null });
    expect(v.unit).toBe("resident");
    expect(v.stream).toBe("stderr");
    // A prose stream offers no facet.
    expect(v.hasRecords).toBe(false);
    expect(levelCounts(v)).toBeNull();
  });

  test("a grep keeps only hits, with their original line numbers", () => {
    const v = logsView(
      { unit: "resident", stream: "stderr", exists: true, lines: WINDOW, truncated: false },
      { grep: "warn" },
    );
    expect(v.state).toBe("filtered");
    expect(v.total).toBe(4);
    expect(v.shown).toBe(2);
    expect(v.lines.map((l) => l.n)).toEqual([2, 3]);
    expect(v.lines.every((l) => l.match)).toBe(true);
    expect(v.note).toBe('2/4 lines match "warn"');
  });

  test("a grep that matches nothing still says so against the window", () => {
    const v = logsView(
      { unit: "resident", stream: "stderr", exists: true, lines: WINDOW, truncated: false },
      { grep: "foo" },
    );
    expect(v.state).toBe("filtered");
    expect(v.shown).toBe(0);
    expect(v.lines).toEqual([]);
    expect(v.note).toBe('no line matches "foo"');
  });

  test("a whitespace-only grep is no grep at all", () => {
    const v = logsView({ unit: "resident", stream: "stderr", exists: true, lines: WINDOW }, { grep: "   " });
    expect(v.state).toBe("ok");
    expect(v.shown).toBe(4);
  });

  test("the truncated flag passes through — older lines exist beyond the window", () => {
    const v = logsView({ unit: "resident", stream: "stderr", exists: true, lines: WINDOW, truncated: true });
    expect(v.truncated).toBe(true);
    const plain = logsView({ unit: "resident", stream: "stderr", exists: true, lines: WINDOW });
    expect(plain.truncated).toBe(false);
  });

  test("the grep note counts hits against the whole window", () => {
    const v = logsView({ unit: "resident", stream: "stderr", exists: true, lines: ["only"] }, { grep: "ONLY" });
    expect(v.note).toBe('1/1 lines match "ONLY"');
    expect(v.shown).toBe(1);
  });
});

describe("logsView over a structured window", () => {
  test("displays the verb's rendering and takes levels from the aligned records", () => {
    const v = logsView(STRUCTURED);
    expect(v.state).toBe("ok");
    expect(v.hasRecords).toBe(true);
    expect(v.lines.map((l) => l.text)).toEqual(STRUCTURED.rendered);
    expect(v.lines.map((l) => l.level)).toEqual(["info", "warn", null, "error"]);
    expect(v.lines.map((l) => l.event)).toEqual(["tick.completed", "reap.stop_failed", null, "spawn.failed"]);
  });

  test("shows each record's stamp in the browser zone and leaves every other line verbatim", () => {
    const v = logsView({
      ...STRUCTURED,
      lines: ['{"level":"info"}', '{"level":"warn"}', "Abort trap: 6", '{"level":"error"}'],
      rendered: [
        "2026-09-08T05:21:46.377Z INFO  resident tick.completed — done",
        "2026-09-08T05:21:47.000Z WARN  resident reap.stop_failed — slow",
        "Abort trap: 6",
        "T ERROR resident spawn.failed — boom",
      ],
      records: [
        { ts: "2026-09-08T05:21:46.377Z", level: "info", comp: "resident", event: "tick.completed" },
        // The record's stamp does not open the text: the text stays as the verb wrote it.
        { ts: "2026-09-08T05:21:48.000Z", level: "warn", comp: "resident", event: "reap.stop_failed" },
        null,
        { ts: "t3", level: "error", comp: "resident", event: "spawn.failed" },
      ],
    });
    const local = localStamp("2026-09-08T05:21:46.377Z");
    expect(local).toMatch(/^2026-09-0[78]T\d\d:\d\d:46\.377[+-]\d\d:\d\d$/);
    expect(v.lines.map((l) => l.text)).toEqual([
      `${local} INFO  resident tick.completed — done`,
      "2026-09-08T05:21:47.000Z WARN  resident reap.stop_failed — slow",
      "Abort trap: 6",
      "T ERROR resident spawn.failed — boom",
    ]);
  });

  test("levelCounts tallies the whole window for the facet chips", () => {
    expect(levelCounts(logsView(STRUCTURED))).toEqual({ error: 1, warn: 1, info: 1, debug: 0 });
  });

  test("a level facet narrows, and a line with no record always survives it", () => {
    const v = logsView(STRUCTURED, { levels: ["error"] });
    expect(v.state).toBe("filtered");
    // The crash line keeps its place: it is exactly the interesting line.
    expect(v.lines.map((l) => l.n)).toEqual([3, 4]);
    expect(v.shown).toBe(2);
  });

  test("all levels can be hidden while raw crash output remains visible", () => {
    expect(logsView(STRUCTURED, { levels: [] }).lines.map((line) => line.text)).toEqual(["Abort trap: 6"]);
  });

  test("naming every level is no facet at all", () => {
    const v = logsView(STRUCTURED, { levels: [...LOG_LEVELS] });
    expect(v.state).toBe("ok");
    expect(v.shown).toBe(4);
  });

  test("grep and facet compose, and grep runs over the DISPLAYED text", () => {
    const v = logsView(STRUCTURED, { grep: "resident", levels: ["warn"] });
    // grep matches the three rendered record lines; the facet then keeps the
    // warn one plus nothing else (the abort line does not say resident).
    expect(v.lines.map((l) => l.n)).toEqual([2]);
    expect(v.note).toBe('3/4 lines match "resident"');
  });

  test("a facet with no survivors says so", () => {
    const noDebug = { ...STRUCTURED, lines: ['{"level":"info"}'], rendered: ["T INFO r e — m"], records: [{ level: "info", event: "e" }] };
    const v = logsView(noDebug, { levels: ["debug"] });
    expect(v.shown).toBe(0);
    expect(v.note).toBe("no line at the picked levels");
  });

  test("the level facet never applies to a prose window", () => {
    const v = logsView(
      { unit: "resident", stream: "stderr", exists: true, lines: WINDOW },
      { levels: ["error"] },
    );
    expect(v.state).toBe("ok");
    expect(v.shown).toBe(4);
  });
});

describe("runOptions", () => {
  test("joins the verb's run list against the spine's rows, newest first", () => {
    const spine = [
      { id: "old1", role: "worker", subject: 4 },
      { id: "new2", role: "packager", subject: 9 },
    ];
    const options = runOptions(["old1", "new2", "ghost"], spine);
    expect(options.map((o) => o.id)).toEqual(["new2", "old1", "ghost"]);
    expect(options[0].label).toBe("new2 · packager · #9");
    // A run the spine does not know keeps its place with a bare label.
    expect(options[2].label).toBe("ghost");
  });

  test("tolerates absent inputs", () => {
    expect(runOptions(undefined, undefined)).toEqual([]);
    expect(runOptions(["a"], null)).toEqual([{ id: "a", label: "a" }]);
  });
});

describe("hash deep-link", () => {
  test("a bare unit defaults to the stream that matters", () => {
    expect(parseLogsHash("#logs/resident")).toEqual({ unit: "resident", stream: "events", run: null });
  });

  test("an explicit stream is honoured, with or without the #", () => {
    expect(parseLogsHash("#logs/dashboard/stdout")).toEqual({ unit: "dashboard", stream: "stdout", run: null });
    expect(parseLogsHash("logs/dashboard/stdout")).toEqual({ unit: "dashboard", stream: "stdout", run: null });
  });

  test("the run lane deep-links one run's diagnostics", () => {
    expect(parseLogsHash("#logs/run/ab12")).toEqual({ unit: RUN_LANE, stream: "events", run: "ab12" });
    // A token outside the safe-id shape is refused, not guessed.
    expect(parseLogsHash("#logs/run/..")).toBeNull();
    expect(parseLogsHash("#logs/run")).toBeNull();
  });

  test("anything else is not a logs link", () => {
    expect(parseLogsHash("#board")).toBeNull();
    expect(parseLogsHash("")).toBeNull();
    expect(parseLogsHash(null)).toBeNull();
    expect(parseLogsHash("#logs")).toBeNull();
    expect(parseLogsHash("#logs/resident/stderr/extra")).toBeNull();
  });

  test("unknown units and streams are refused, not guessed", () => {
    expect(parseLogsHash("#logs/runner")).toBeNull();
    expect(parseLogsHash("#logs/../etc/passwd")).toBeNull();
    expect(parseLogsHash("#logs/resident/syslog")).toBeNull();
    expect(parseLogsHash("#logs/Resident")).toBeNull();
  });

  test("logsHash is canonical and round-trips", () => {
    expect(logsHash({ unit: "dashboard", stream: "stdout" })).toBe("#logs/dashboard/stdout");
    for (const unit of LOG_UNITS) {
      for (const stream of streamsFor(unit)) {
        expect(parseLogsHash(logsHash({ unit, stream }))).toEqual({ unit, stream, run: null });
      }
    }
    expect(logsHash(parseLogsHash("#logs/resident"))).toBe("#logs/resident/events");
    expect(logsHash({ unit: RUN_LANE, run: "ab12" })).toBe("#logs/run/ab12");
    expect(parseLogsHash(logsHash({ unit: RUN_LANE, run: "ab12" }))).toEqual({
      unit: RUN_LANE,
      stream: "events",
      run: "ab12",
    });
  });

  test("logsHash falls back to the defaults for an invalid pick", () => {
    expect(logsHash()).toBe("#logs/resident/events");
    expect(logsHash({ unit: "runner", stream: "syslog" })).toBe("#logs/resident/events");
    // A run-lane hash with no run stays a lane, never a fabricated id.
    expect(logsHash({ unit: RUN_LANE })).toBe("#logs/run");
  });
});

describe("copyText", () => {
  test("copies exactly what the unit wrote — no numbers, no markers", () => {
    const v = logsView({ unit: "resident", stream: "stderr", exists: true, lines: WINDOW, truncated: true });
    expect(copyText(v)).toBe("boot ok\nWARN disk slow\nwarn again\nquiet");
  });

  test("copies only the filtered lines when a grep is applied", () => {
    const v = logsView({ unit: "resident", stream: "stderr", exists: true, lines: WINDOW }, { grep: "warn" });
    expect(copyText(v)).toBe("WARN disk slow\nwarn again");
  });

  test("copies the rendering the operator is reading on a structured window", () => {
    expect(copyText(logsView(STRUCTURED))).toBe(STRUCTURED.rendered.join("\n"));
  });

  test("an empty or missing view copies nothing", () => {
    expect(copyText(logsView(undefined))).toBe("");
    expect(copyText(undefined)).toBe("");
  });
});

describe("the mc unit and the events stream", () => {
  test("mc offers only its events stream, in the query and in the hash", () => {
    expect(logsQuery({ unit: "mc" })).toBe("/api/logs/mc?stream=events&tail=500&format=json");
    // A pair the verb would refuse must not survive the picker: asking for
    // mc/stderr yields mc's only stream, not a 400.
    expect(logsQuery({ unit: "mc", stream: "stderr" })).toBe("/api/logs/mc?stream=events&tail=500&format=json");
    expect(logsHash({ unit: "mc", stream: "stdout" })).toBe("#logs/mc/events");
    expect(parseLogsHash("#logs/mc/stderr")).toBeNull();
    expect(parseLogsHash("#logs/mc")).toEqual({ unit: "mc", stream: "events", run: null });
  });

  test("the supervised units still reach both launchd files", () => {
    expect(logsQuery({ unit: "resident", stream: "stderr" })).toBe(
      "/api/logs/resident?stream=stderr&tail=500",
    );
    expect(parseLogsHash("#logs/dashboard/stdout")).toEqual({ unit: "dashboard", stream: "stdout", run: null });
  });
});

describe("processRuns", () => {
  test("selects only referenced runs, with the latest reference first", () => {
    const records = [{ run_id: "a" }, { run_id: "b" }, null, { run_id: "a" }, { run_id: "../bad" }];
    expect(processRuns(records, [
      { id: "a", project: "project", outcome: "completed" },
      { id: "unrelated", outcome: "failed" },
    ], ["b", "unrelated"], [{ id: "project", title: "Example Project" }])).toEqual([
      { id: "a", label: "a · Example Project", status: "completed" },
      { id: "b", label: "b", status: "recorded" },
    ]);
  });

  test("an empty process log has no runs, even when other runs exist", () => {
    expect(processRuns([], [{ id: "a" }], ["a"])).toEqual([]);
    expect(processRuns(undefined)).toEqual([]);
  });
});
