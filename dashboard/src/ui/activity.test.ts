import { describe, expect, test } from "bun:test";
import {
  splitStreams,
  activityRows,
  kindOptions,
  mergeRows,
  newRowIDs,
  streamStats,
} from "./activity.js";
import { stampLabel } from "./format.js";

const NOW = Date.parse("2026-08-17T12:00:00Z");

const rows = [
  { id: 9, created_at: "2026-08-17 11:57:00", actor: "worker", kind: "task.seeded", subject: "12", subject_kind: "task", detail: "picked up" },
  { id: 8, created_at: "2026-08-17 11:50:00", actor: null, kind: "dispatch.health", subject: null, detail: "lease stale" },
  { id: 7, created_at: "2026-08-17 09:00:00", actor: "collie", kind: "collie.reply", subject: null, detail: null },
  { id: 6, created_at: "2026-08-17 08:00:00", actor: "packager", kind: "packet.rendered", subject: "12", subject_kind: "task", detail: "" },
  { id: 5, created_at: "2026-08-16 12:00:00", actor: "operator", kind: "config.set", subject: null, detail: "knob" },
];

describe("splitStreams", () => {
  test("dispatch errors and connection waits are the separate error stream, everything else is events", () => {
    const { events, errors } = splitStreams(rows);
    expect(errors.map((r) => r.id)).toEqual([8]);
    expect(events.map((r) => r.id)).toEqual([9, 7, 6, 5]);
    const waits = splitStreams([
      { id: 11, kind: "dispatch.wait_ended" }, { id: 10, kind: "dispatch.wait_started" }, { id: 4, kind: "dispatch.spawn" },
    ]);
    expect(waits.errors.map((r) => r.id)).toEqual([11, 10]);
    expect(waits.events.map((r) => r.id)).toEqual([4]);
  });

  test("empty and absent reads split into two empty streams", () => {
    expect(splitStreams([])).toEqual({ errors: [], events: [] });
    expect(splitStreams(null)).toEqual({ errors: [], events: [] });
    expect(splitStreams(undefined)).toEqual({ errors: [], events: [] });
  });
});

describe("activityRows", () => {
  test("preserves the verb's newest-first order", () => {
    expect(activityRows(rows, NOW).map((r) => r.id)).toEqual([9, 8, 7, 6, 5]);
  });

  test("tones group by kind family, not by severity", () => {
    const tone = (kind: string) => activityRows([{ kind, created_at: "2026-08-17 11:00:00" }], NOW)[0].tone;
    expect(tone("dispatch.health")).toBe("rust");
    expect(tone("dispatch.wait_started")).toBe("rust");
    expect(tone("dispatch.wait_ended")).toBe("stone");
    expect(tone("collie.reply")).toBe("sky");
    expect(tone("collie.session.started")).toBe("sky");
    expect(tone("task.seeded")).toBe("leaf");
    expect(tone("packet.rendered")).toBe("leaf");
    expect(tone("config.set")).toBe("stone");
    expect(tone("")).toBe("stone");
  });

  test("actor falls back to system when the spine wrote the row itself", () => {
    const out = activityRows(rows, NOW);
    expect(out[0].actor).toBe("worker");
    expect(out[1].actor).toBe("system");
    expect(activityRows([{ kind: "x", created_at: "2026-08-17 11:00:00" }], NOW)[0].actor).toBe("system");
  });

  test("the subject names a task, a run, a connection, or a Project, and detail degrades to an empty string", () => {
    const out = activityRows(rows, NOW);
    expect(out[0].subject).toBe("task #12");
    expect(out[1].subject).toBe(null);
    expect(out[2].detail).toBe("");
    expect(out[0].detail).toBe("picked up");
    const names = {
      connections: [{ connection_id: "connection-1", name: "Claude subscription" }],
      projects: [{ id: "alpha", title: "Alpha Site" }],
    };
    const label = (subject: string, kind?: string) => activityRows([{ subject, subject_kind: kind }], NOW, names)[0].subject;
    expect(label("0123456789abcdef", "run")).toBe("run 0123456789abcdef");
    expect(label("connection-1", "connection")).toBe("connection Claude subscription");
    expect(label("connection-2", "connection")).toBe("Connection name unavailable");
    expect(label("alpha", "project")).toBe("Project Alpha Site");
    expect(label("beta", "project")).toBe("Project name unavailable");
    expect(label("session-1")).toBe("session-1");
  });

  test("carries both a relative age and the shared formatter's stamp", () => {
    const out = activityRows(rows, NOW);
    expect(out[0].age).toBe("3m ago");
    expect(out[0].stamp).toBe(stampLabel("2026-08-17 11:57:00"));
    expect(out[0].stamp).toMatch(/^Aug 1\d \d\d:\d\d$/);
    expect(out[2].age).toBe("3h ago");
    expect(out[4].age).toBe("1d ago");
  });

  test("a row missing every optional column renders unknowns, never throws", () => {
    const out = activityRows([{}], NOW);
    expect(out).toEqual([
      { id: null, kind: "", actor: "system", subject: null, detail: "", age: "", stamp: "", tone: "stone" },
    ]);
  });

  test("empty and absent inputs give no rows", () => {
    expect(activityRows([], NOW)).toEqual([]);
    expect(activityRows(null, NOW)).toEqual([]);
  });
});

describe("kindOptions", () => {
  test("suggests only the unique kinds this read contained, sorted", () => {
    expect(kindOptions(rows)).toEqual([
      "collie.reply",
      "config.set",
      "dispatch.health",
      "packet.rendered",
      "task.seeded",
    ]);
  });

  test("duplicates collapse and missing kinds are skipped", () => {
    expect(kindOptions([{ kind: "task.seeded" }, { kind: "task.seeded" }, { kind: "" }, {}])).toEqual(["task.seeded"]);
  });

  test("empty and absent inputs suggest nothing", () => {
    expect(kindOptions([])).toEqual([]);
    expect(kindOptions(null)).toEqual([]);
  });
});

describe("mergeRows", () => {
  const existing = [
    { id: 3, created_at: "2026-08-17 11:00:00", kind: "task.seeded" },
    { id: 2, created_at: "2026-08-17 10:00:00", kind: "task.seeded" },
  ];

  test("a poll window that overlaps what is on screen does not duplicate rows", () => {
    const incoming = [
      { id: 4, created_at: "2026-08-17 11:30:00", kind: "task.worked" },
      { id: 3, created_at: "2026-08-17 11:00:00", kind: "task.seeded" },
    ];
    expect(mergeRows(existing, incoming).map((r) => r.id)).toEqual([4, 3, 2]);
  });

  test("orders newest first, breaking a shared stamp by id", () => {
    const merged = mergeRows(
      [{ id: 1, created_at: "2026-08-17 11:00:00" }],
      [
        { id: 5, created_at: "2026-08-17 11:00:00" },
        { id: 3, created_at: "2026-08-17 11:00:00" },
      ],
    );
    expect(merged.map((r) => r.id)).toEqual([5, 3, 1]);
  });

  test("an id-less row is kept once and deduped on its content", () => {
    const noID = { created_at: "2026-08-17 11:30:00", kind: "task.worked", actor: "worker" };
    const merged = mergeRows([...existing, noID], [{ ...noID }, { created_at: "2026-08-17 11:31:00", kind: "task.worked", actor: "worker" }]);
    expect(merged.length).toBe(4);
    expect(merged.map((r) => r.id ?? "-")).toEqual(["-", "-", 3, 2]);
    expect(merged[0].created_at).toBe("2026-08-17 11:31:00");
  });

  test("a row with an unreadable stamp sorts last rather than claiming to be newest", () => {
    const merged = mergeRows(existing, [{ id: 9, created_at: null }]);
    expect(merged.map((r) => r.id)).toEqual([3, 2, 9]);
  });

  test("empty and absent sides merge to the other side", () => {
    expect(mergeRows([], []).length).toBe(0);
    expect(mergeRows(null, null)).toEqual([]);
    expect(mergeRows(null, existing).map((r) => r.id)).toEqual([3, 2]);
    expect(mergeRows(existing, null).map((r) => r.id)).toEqual([3, 2]);
  });
});

describe("newRowIDs", () => {
  const existing = [{ id: 3 }, { id: 2 }];

  test("returns only the ids the poll added, in incoming order", () => {
    expect(newRowIDs(existing, [{ id: 5 }, { id: 4 }, { id: 3 }])).toEqual([5, 4]);
  });

  test("an id-less row flashes nothing and a repeat is reported once", () => {
    expect(newRowIDs(existing, [{}, { id: 4 }, { id: 4 }])).toEqual([4]);
  });

  test("nothing new, and empty or absent inputs, give no flashes", () => {
    expect(newRowIDs(existing, existing)).toEqual([]);
    expect(newRowIDs([], [])).toEqual([]);
    expect(newRowIDs(null, null)).toEqual([]);
    expect(newRowIDs(null, [{ id: 1 }])).toEqual([1]);
  });
});

describe("streamStats", () => {
  test("counts the window, its span, and its distinct actors", () => {
    expect(streamStats(rows, NOW)).toEqual({ count: 5, newest: "3m ago", oldest: "1d ago", actors: 5 });
  });

  test("an actorless row counts as the system actor", () => {
    const stats = streamStats(
      [
        { actor: "worker", created_at: "2026-08-17 11:59:30" },
        { created_at: "2026-08-17 11:00:00" },
        { actor: null, created_at: "2026-08-17 10:00:00" },
      ],
      NOW,
    );
    expect(stats).toEqual({ count: 3, newest: "just now", oldest: "2h ago", actors: 2 });
  });

  test("rows with unreadable stamps leave the span empty rather than lie", () => {
    expect(streamStats([{ actor: "a", created_at: null }], NOW)).toEqual({
      count: 1,
      newest: "",
      oldest: "",
      actors: 1,
    });
  });

  test("an empty or absent stream reports zeroes and no ages", () => {
    const zero = { count: 0, newest: "", oldest: "", actors: 0 };
    expect(streamStats([], NOW)).toEqual(zero);
    expect(streamStats(null, NOW)).toEqual(zero);
  });
});
