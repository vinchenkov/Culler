// Unit tests for review.js — the Review Queue view models (DOM-free, pure:
// every "now" arrives as an argument).

import { describe, expect, test } from "bun:test";
import {
  FRAME_SANDBOX,
  LEDGER_FILTERS,
  applyLinkRules,
  firstPacket,
  ledgerEmptyText,
  ledgerPackets,
  linkRole,
  livePackets,
  packetDocument,
  openingLabel,
  packetView,
  packetsFor,
  queueStats,
  readerPosition,
  reasonRequired,
  renderMode,
  reportArea,
  reportDocument,
  shortCommit,
  decisionPayload,
} from "./review.js";

const NOW = Date.parse("2026-08-17T12:00:00Z");

const packet = (over: Record<string, unknown> = {}) => ({
  task_id: 1,
  title: "Add the README",
  render_path: "/renders/1.md",
  thesis: "ship it",
  archived: 0,
  review_state: "awaits_decision",
  created_at: "2026-08-17 11:00:00",
  ...over,
});

// One packet in each review state. The creation order differs from the task
// order and from the decision order, so each sort is visible.
const PACKETS = [
  packet({ task_id: 2, created_at: "2026-08-17 10:00:00" }),
  packet({ task_id: 1, created_at: "2026-08-16 09:00:00" }),
  packet({ task_id: 5, created_at: "2026-08-14 09:00:00", review_state: "approved", decided_at: "2026-08-15 09:00:00" }),
  packet({ task_id: 6, created_at: "2026-08-17 08:00:00", review_state: "returned", return_cause: "revise", reason: "Rename it." }),
  packet({ task_id: 3, created_at: "2026-08-10 11:30:00", archived: 1, review_state: "merged", decided_at: "2026-08-12 10:00:00", destination_branch: "culler", merge_commit: "c".repeat(40) }),
  packet({ task_id: 4, created_at: "2026-08-15 08:00:00", archived: 1, review_state: "cancelled", decided_at: "2026-08-16 10:00:00", reason: "Out of scope." }),
  packet({ task_id: 8, created_at: "2026-08-09 08:00:00", archived: 1, review_state: "merged", decided_at: "2026-08-12 10:00:00", destination_branch: "culler" }),
  packet({ task_id: 7, created_at: "2026-08-16 08:00:00", review_state: "replaced" }),
];

describe("livePackets", () => {
  test("keeps the three live states: awaiting decision first, then oldest first", () => {
    expect(livePackets(PACKETS).map((p) => p.task_id)).toEqual([1, 2, 5, 6]);
  });

  test("tolerates an absent list", () => {
    expect(livePackets(null)).toEqual([]);
    expect(livePackets([])).toEqual([]);
  });
});

describe("ledgerPackets", () => {
  test("the Approved filter shows merged packets, newest decision first, higher task first on a tie", () => {
    expect(ledgerPackets(PACKETS, "approved").map((p) => p.task_id)).toEqual([8, 3]);
  });

  test("the Cancelled filter shows cancelled packets", () => {
    expect(ledgerPackets(PACKETS, "cancelled").map((p) => p.task_id)).toEqual([4]);
  });

  test("the All filter shows both final decisions in one decision order", () => {
    expect(ledgerPackets(PACKETS, "all").map((p) => p.task_id)).toEqual([4, 8, 3]);
  });

  test("Approved is the default filter", () => {
    expect(LEDGER_FILTERS[0]).toBe("approved");
    expect(ledgerPackets(PACKETS, "bogus").map((p) => p.task_id)).toEqual([8, 3]);
    expect(ledgerPackets(PACKETS, undefined).map((p) => p.task_id)).toEqual([8, 3]);
  });

  test("a replaced packet appears in neither view", () => {
    const views = [...livePackets(PACKETS), ...ledgerPackets(PACKETS, "all")];
    expect(views.some((p) => p.task_id === 7)).toBe(false);
  });

  test("each filter names its own empty state", () => {
    expect(ledgerEmptyText("approved")).toBe("No approved packets yet.");
    expect(ledgerEmptyText("cancelled")).toBe("No cancelled packets yet.");
    expect(ledgerEmptyText("all")).toBe("No decided packets yet.");
  });
});

describe("the opening text of a packet", () => {
  test("a task that the Strategist proposed names its opening text Strategic rationale", () => {
    const v = packetView(packet({ origin: "autonomous", thesis: "The export holds the date.\n\nObserved: the export omits the date." }), NOW);
    expect(openingLabel({ origin: "autonomous" })).toBe("Strategic rationale");
    expect(v.openingLabel).toBe("Strategic rationale");
    expect(v.strategist).toBe(true);
    expect(v.thesis).toBe("The export holds the date.\n\nObserved: the export omits the date.");
  });

  test("a task that the operator defined keeps the Thesis heading", () => {
    const v = packetView(packet({ origin: "user" }), NOW);
    expect(v.openingLabel).toBe("Thesis");
    expect(v.strategist).toBe(false);
    expect(openingLabel({})).toBe("Thesis");
    expect(openingLabel(null)).toBe("Thesis");
  });
});

describe("packetView", () => {
  test("carries the task title, age, thesis and render presence", () => {
    const v = packetView(packet(), NOW);
    expect(v.taskID).toBe(1);
    expect(v.title).toBe("Add the README");
    expect(v.age).toBe("1h");
    expect(v.ageMs).toBe(3600_000);
    expect(v.thesis).toBe("ship it");
    expect(v.hasRender).toBe(true);
    expect(v.live).toBe(true);
    expect(v.decidable).toBe(true);
    expect(v.label).toBe("");
    expect(v.decision).toBe("");
  });

  test("a row without a task title has an empty title", () => {
    expect(packetView(packet({ title: undefined }), NOW).title).toBe("");
    expect(packetView(packet({ title: null }), NOW).title).toBe("");
  });

  test("age tone flips exactly at 24h in queue", () => {
    expect(packetView(packet({ created_at: "2026-08-16 12:00:01" }), NOW).ageTone).toBe("leaf");
    expect(packetView(packet({ created_at: "2026-08-16 12:00:00" }), NOW).ageTone).toBe("rust");
    expect(packetView(packet({ created_at: "2026-08-16 11:59:59" }), NOW).ageTone).toBe("rust");
  });

  test("only a ready packet that awaits a decision offers the decision controls", () => {
    expect(packetView(packet({ ready: 0 }), NOW).decidable).toBe(false);
    for (const review_state of ["approved", "returned", "merged", "cancelled", "replaced"]) {
      expect(packetView(packet({ review_state, ready: 1 }), NOW).decidable).toBe(false);
    }
  });

  test("an approved row states the approval and the merge state text of mc", () => {
    const text = "Culler merges task #6 first.";
    const v = packetView({ ...PACKETS[2], merge_state: "after-task", merge_state_text: text, merge_task_id: 6 }, NOW, "UTC");
    expect([v.label, v.detail, v.mergeState]).toEqual(["Approved", text, text]);
    expect(v.decision).toBe(`Approved on Aug 15, 2026 09:00. ${text}`);
  });

  test("an approved row without a merge state text states only the approval", () => {
    const v = packetView(PACKETS[2], NOW, "UTC");
    expect([v.detail, v.mergeState]).toEqual(["", ""]);
    expect(v.decision).toBe("Approved on Aug 15, 2026 09:00.");
  });

  test("only an approved row carries a merge state", () => {
    for (const review_state of ["awaits_decision", "returned", "merged", "cancelled", "replaced"]) {
      expect(packetView(packet({ review_state, merge_state_text: "Culler merges this change now." }), NOW).mergeState).toBe("");
    }
  });

  test("the reader follows one decided packet from its approval to its merge", () => {
    const approved = packet({ task_id: 5, packet_id: 11, review_state: "approved", decided_at: "2026-08-15 09:00:00",
      merge_state: "merge", merge_state_text: "Culler merges this change now." });
    expect(packetView(approved, NOW, "UTC").decision).toBe("Approved on Aug 15, 2026 09:00. Culler merges this change now.");
    const merged = { ...approved, archived: 1, review_state: "merged", merge_state: null, merge_state_text: null,
      destination_branch: "culler", merge_commit: "d".repeat(40) };
    const v = packetView(merged, NOW, "UTC");
    expect([v.live, v.mergeState]).toEqual([false, ""]);
    expect(v.decision).toBe(`Approved on Aug 15, 2026 09:00. Merged into culler at ${"d".repeat(40)}.`);
  });

  test("a returned row states the return and its cause", () => {
    const revise = packetView(PACKETS[3], NOW);
    expect([revise.label, revise.detail]).toEqual(["Returned to the pipeline", "Revise: Rename it."]);
    expect(packetView(packet({ review_state: "returned", return_cause: "conflict" }), NOW).detail)
      .toBe("A merge conflict returned the task to the Worker for a merge repair.");
    const bare = packetView(packet({ review_state: "returned", return_cause: null }), NOW);
    expect([bare.detail, bare.decision]).toEqual(["", "Returned to the pipeline."]);
  });

  test("a blocked row carries the block reason", () => {
    const v = packetView(packet({ blocked: 1, blocked_reason: "Confirm the license." }), NOW);
    expect([v.blocked, v.blockedReason]).toEqual([true, "Confirm the license."]);
    expect(packetView(packet({ blocked: 0 }), NOW).blocked).toBe(false);
  });

  test("a ledger row states the decision, its local time, and the merge or the reason", () => {
    const merged = packetView(PACKETS[4], NOW, "UTC");
    expect([merged.label, merged.detail, merged.decidedAt, merged.live]).toEqual(["Approved", "Merged into culler.", "Aug 12, 2026 10:00", false]);
    expect(merged.decision).toBe(`Approved on Aug 12, 2026 10:00. Merged into culler at ${"c".repeat(40)}.`);
    const cancelled = packetView(PACKETS[5], NOW, "UTC");
    expect([cancelled.label, cancelled.detail]).toEqual(["Cancelled", "Reason: Out of scope."]);
    expect(cancelled.decision).toBe("Cancelled on Aug 16, 2026 10:00. Reason: Out of scope.");
    expect(packetView(PACKETS[4], NOW, "America/Los_Angeles").decidedAt).toBe("Aug 12, 2026 03:00");
  });

  test("an expired report carries its expiry date", () => {
    const v = packetView(packet({ artifact_retention: { status: "expired", expired_at: "2026-11-10 09:14:00" } }), NOW, "UTC");
    expect([v.expired, v.expiredAt]).toEqual([true, "Nov 10, 2026 09:14"]);
    expect(packetView(packet({ artifact_retention: { status: "retained" } }), NOW).expired).toBe(false);
  });

  test("absent packet fields use the empty state", () => {
    const v = packetView({ task_id: 9, created_at: null }, NOW);
    expect(v.thesis).toBe("(no thesis rendered)");
    expect(v.hasRender).toBe(false);
    expect(v.age).toBe("");
    expect(v.ageMs).toBe(0);
    expect(v.ageTone).toBe("leaf");
    expect(v.live).toBe(false);
    expect(v.decidable).toBe(false);
    expect(packetView({ thesis: "", render_path: "" }, NOW).thesis).toBe("(no thesis rendered)");
    expect(packetView(null, NOW).taskID).toBe(null);
  });
});

describe("the review state tag", () => {
  test("a packet that awaits a decision has no tag", () => {
    expect(packetView(packet({ review_state: "awaits_decision" }), NOW).label).toBe("");
  });

  test("each other live or final review state has a tag", () => {
    for (const state of ["approved", "returned", "merged", "cancelled"]) {
      expect(packetView(packet({ review_state: state }), NOW).label).not.toBe("");
    }
  });
});

describe("packetsFor", () => {
  test("the Live queue view shows the live states", () => {
    expect(packetsFor(PACKETS, "live", "approved", NOW).map((v) => v.taskID)).toEqual([1, 2, 5, 6]);
  });

  test("the Ledger view applies the filter", () => {
    expect(packetsFor(PACKETS, "ledger", "approved", NOW).map((v) => v.taskID)).toEqual([8, 3]);
    expect(packetsFor(PACKETS, "ledger", "cancelled", NOW).map((v) => v.taskID)).toEqual([4]);
    expect(packetsFor(PACKETS, "ledger", "all", NOW).map((v) => v.taskID)).toEqual([4, 8, 3]);
  });

  test("an unknown view falls back to the Live queue", () => {
    expect(packetsFor(PACKETS, "bogus", "all", NOW).map((v) => v.taskID)).toEqual([1, 2, 5, 6]);
    expect(packetsFor(PACKETS, undefined, undefined, NOW).map((v) => v.taskID)).toEqual([1, 2, 5, 6]);
  });

  test("empty inputs render no rows", () => {
    expect(packetsFor(null, "ledger", "all", NOW)).toEqual([]);
    expect(packetsFor([], "live", "approved", NOW)).toEqual([]);
  });

  test("does not mutate the caller's array", () => {
    const rows = PACKETS.slice();
    packetsFor(rows, "ledger", "all", NOW);
    packetsFor(rows, "live", "approved", NOW);
    expect(rows.map((p) => p.task_id)).toEqual([2, 1, 5, 6, 3, 4, 8, 7]);
  });
});

describe("queueStats", () => {
  test("counts the decisions owed, the review slots, and the ledger", () => {
    const s = queueStats(PACKETS, { review_wip_cap: 5 });
    expect(s).toEqual({
      awaiting: 2,
      slots: 4,
      cap: 5,
      capKnown: true,
      over: false,
      approved: 2,
      cancelled: 1,
    });
  });

  test("the slots in use are judged against the review capacity", () => {
    expect(queueStats(PACKETS, { review_wip_cap: 3 }).over).toBe(true);
    expect(queueStats(PACKETS, { review_wip_cap: 4 }).over).toBe(false);
  });

  test("the badge count excludes approved and returned packets", () => {
    const rows = [packet({ review_state: "approved" }), packet({ task_id: 2, review_state: "returned" })];
    expect(queueStats(rows, { review_wip_cap: 3 })).toMatchObject({ awaiting: 0, slots: 2 });
  });

  test("an unread settings row reports the cap unknown, not zero", () => {
    const s = queueStats(PACKETS, {});
    expect(s.cap).toBe(null);
    expect(s.capKnown).toBe(false);
    expect(s.over).toBe(false);
    expect(queueStats(PACKETS, null).capKnown).toBe(false);
    expect(queueStats(PACKETS, { review_wip_cap: 0 }).capKnown).toBe(true);
  });

  test("an empty queue has zero counts", () => {
    expect(queueStats([], null)).toEqual({
      awaiting: 0, slots: 0, cap: null, capKnown: false, over: false, approved: 0, cancelled: 0,
    });
    expect(queueStats(null, null).awaiting).toBe(0);
  });
});

describe("reasonRequired", () => {
  test("revise and cancel owe a reason, approve does not", () => {
    expect(reasonRequired("revise")).toBe(true);
    expect(reasonRequired("cancel")).toBe(true);
    expect(reasonRequired("approve")).toBe(false);
    expect(reasonRequired("")).toBe(false);
  });
});

describe("decisionPayload", () => {
  test("approve sends the verb alone", () => {
    expect(decisionPayload("approve", "")).toEqual({ decision: "approve" });
  });

  test("approve never carries a reason, even when one was typed", () => {
    expect(decisionPayload("approve", "looks good")).toEqual({ decision: "approve" });
  });

  test("revise and cancel carry the trimmed reason", () => {
    expect(decisionPayload("revise", "  tighten the thesis  ")).toEqual({
      decision: "revise",
      reason: "tighten the thesis",
    });
    expect(decisionPayload("cancel", "superseded")).toEqual({
      decision: "cancel",
      reason: "superseded",
    });
  });

  test("the operator's words ride untouched apart from trimming", () => {
    const words = "no: the § 7 gate\nis wrong — redo it";
    expect(decisionPayload("revise", `\n${words}\n`)).toEqual({ decision: "revise", reason: words });
  });

  test("a blank reason abandons the decision", () => {
    expect(decisionPayload("revise", "")).toBe(null);
    expect(decisionPayload("revise", "   ")).toBe(null);
    expect(decisionPayload("cancel", null)).toBe(null);
    expect(decisionPayload("revise", undefined)).toBe(null);
  });

  test("an unknown verb sends nothing", () => {
    expect(decisionPayload("approve!", "x")).toBe(null);
    expect(decisionPayload("", "x")).toBe(null);
    expect(decisionPayload(null, null)).toBe(null);
  });
});

describe("renderMode", () => {
  test("the server's content type is the primary signal", () => {
    expect(renderMode("text/html; charset=utf-8", "anything")).toBe("html");
    expect(renderMode("text/markdown; charset=utf-8", "# hi")).toBe("markdown");
    expect(renderMode("text/plain; charset=utf-8", "prose")).toBe("text");
  });

  test("a sniff catches HTML written under a bare name", () => {
    expect(renderMode("text/plain", "  <!doctype html><p>x</p>")).toBe("html");
    expect(renderMode("text/plain", "<HTML><body>x</body>")).toBe("html");
    // A stray tag mid-prose is not a document.
    expect(renderMode("text/plain", "use <b>bold</b> sparingly")).toBe("text");
  });

  test("absent inputs land on the honest floor", () => {
    expect(renderMode(undefined, undefined)).toBe("text");
    expect(renderMode("", "")).toBe("text");
  });
});

describe("packetDocument", () => {
  test("wraps the bytes in a skeleton whose head carries the CSP first", () => {
    const doc = packetDocument("<h1>packet</h1>");
    expect(doc.startsWith("<!doctype html>")).toBe(true);
    const csp = doc.indexOf("Content-Security-Policy");
    expect(csp).toBeGreaterThan(-1);
    expect(doc.indexOf("default-src 'none'")).toBeGreaterThan(-1);
    // The packet's bytes land AFTER the policy, so it already governs them.
    expect(csp).toBeLessThan(doc.indexOf("<h1>packet</h1>"));
    expect(doc.endsWith("</body></html>")).toBe(true);
  });

  test("no network grant of any kind — media is data:/blob: only", () => {
    const doc = packetDocument("");
    expect(doc).toContain("img-src data: blob:");
    expect(doc).not.toContain("http:");
    expect(doc).not.toContain("script-src");
  });

  test("the sandbox grant never includes scripts or same-origin", () => {
    expect(FRAME_SANDBOX).not.toContain("allow-scripts");
    expect(FRAME_SANDBOX).not.toContain("allow-same-origin");
  });

  test("in-page links resolve inside the frame, before any report byte", () => {
    const doc = packetDocument("<a href=\"#glossary\">Glossary</a>");
    const base = doc.indexOf('<base href="about:srcdoc">');
    expect(base).toBeGreaterThan(doc.indexOf("Content-Security-Policy"));
    expect(base).toBeLessThan(doc.indexOf("<a href"));
    expect(doc).not.toContain("target=");
  });

  test("non-string bytes render an empty body rather than the word undefined", () => {
    expect(packetDocument(undefined)).toMatch(/<body style="[^"]*"><\/body>/);
  });
});

describe("readerPosition", () => {
  const place = (view: string, filter: string, taskID: number) =>
    readerPosition(PACKETS, view, filter, taskID);

  test("the Live queue order sets the position and the neighbours", () => {
    expect(place("live", "approved", 1)).toEqual({ position: "1 of 4", previous: null, next: 2 });
    expect(place("live", "approved", 2)).toEqual({ position: "2 of 4", previous: 1, next: 5 });
    expect(place("live", "approved", 5)).toEqual({ position: "3 of 4", previous: 2, next: 6 });
    expect(place("live", "approved", 6)).toEqual({ position: "4 of 4", previous: 5, next: null });
  });

  test("each Ledger filter sets its own order", () => {
    expect(place("ledger", "approved", 8)).toEqual({ position: "1 of 2", previous: null, next: 3 });
    expect(place("ledger", "approved", 3)).toEqual({ position: "2 of 2", previous: 8, next: null });
    expect(place("ledger", "cancelled", 4)).toEqual({ position: "1 of 1", previous: null, next: null });
    expect(place("ledger", "all", 4)).toEqual({ position: "1 of 3", previous: null, next: 8 });
    expect(place("ledger", "all", 8)).toEqual({ position: "2 of 3", previous: 4, next: 3 });
    expect(place("ledger", "all", 3)).toEqual({ position: "3 of 3", previous: 8, next: null });
  });

  test("the order agrees with the rows of the packet list", () => {
    for (const [view, filter] of [["live", "approved"], ["ledger", "approved"], ["ledger", "cancelled"], ["ledger", "all"]]) {
      const rows = packetsFor(PACKETS, view, filter, NOW).map((v) => v.taskID);
      rows.forEach((taskID, index) => {
        const p = place(view!, filter!, taskID!);
        expect(p.previous).toBe(index > 0 ? rows[index - 1]! : null);
        expect(p.next).toBe(index + 1 < rows.length ? rows[index + 1]! : null);
      });
    }
  });

  test("a packet absent from the current list has no position and no neighbours", () => {
    const absent = { position: "", previous: null, next: null };
    // A merged packet is absent from the Live queue, a live packet from the ledger.
    expect(place("live", "approved", 3)).toEqual(absent);
    expect(place("ledger", "approved", 1)).toEqual(absent);
    // The Cancelled filter leaves out the merged packets.
    expect(place("ledger", "cancelled", 8)).toEqual(absent);
    // A replaced packet appears in neither view.
    expect(place("live", "approved", 7)).toEqual(absent);
    expect(place("ledger", "all", 7)).toEqual(absent);
    expect(readerPosition(null, "live", "approved", 1)).toEqual(absent);
  });
});

describe("firstPacket", () => {
  test("each view and each Ledger filter opens the first row of its list", () => {
    for (const [view, filter] of [["live", "approved"], ["ledger", "approved"], ["ledger", "cancelled"], ["ledger", "all"]]) {
      expect(firstPacket(PACKETS, view, filter)).toBe(packetsFor(PACKETS, view, filter, NOW)[0]!.taskID);
    }
    expect(firstPacket(PACKETS, "live", "approved")).toBe(1);
    expect(firstPacket(PACKETS, "ledger", "cancelled")).toBe(4);
  });

  test("an empty list has no first packet", () => {
    expect(firstPacket([], "live", "approved")).toBeNull();
    expect(firstPacket(null, "ledger", "all")).toBeNull();
    expect(firstPacket(PACKETS.filter((p) => p.review_state !== "cancelled"), "ledger", "cancelled")).toBeNull();
  });
});

describe("shortCommit", () => {
  test("keeps the first 7 characters", () => {
    expect(shortCommit("0123456789abcdef0123456789abcdef01234567")).toBe("0123456");
    expect(shortCommit("abc")).toBe("abc");
  });

  test("an absent commit has no short form", () => {
    expect(shortCommit(null)).toBe("");
    expect(shortCommit(undefined)).toBe("");
    expect(shortCommit("")).toBe("");
  });
});

describe("reportArea", () => {
  const view = (over: Record<string, unknown> = {}) => packetView(packet(over), NOW);
  const expired = { artifact_retention: { status: "expired", expired_at: "2026-11-10 09:14:00" } };

  test("the report area shows the thesis only when it shows no report", () => {
    expect(reportArea(view(), "report")).toEqual({ kind: "report", thesis: false, records: false });
    expect(reportArea(view(), "loading")).toEqual({ kind: "loading", thesis: false, records: false });
    expect(reportArea(view(), "failed")).toEqual({ kind: "failed", thesis: true, records: false });
    expect(reportArea(view({ render_path: "" }), "loading")).toEqual({ kind: "missing", thesis: true, records: false });
  });

  test("an expired report shows the thesis and the criteria", () => {
    expect(reportArea(view(expired), "loading")).toEqual({ kind: "expired", thesis: true, records: true });
    // The expiry wins over any read state: the page reads no expired report.
    expect(reportArea(view(expired), "report").kind).toBe("expired");
  });

  test("a packet without a recorded report never shows a report", () => {
    expect(reportArea(view({ render_path: null }), "report")).toMatchObject({ kind: "missing", thesis: true });
    expect(reportArea(view(), undefined).kind).toBe("loading");
  });
});

describe("linkRole", () => {
  test("an empty address is a link to the top of the report", () => {
    for (const href of ["", "  ", "\t\n", null]) expect(linkRole(href)).toBe("top");
  });

  test("an in-page link stays in the report", () => {
    for (const href of ["#glossary", "#", "  #toc"]) expect(linkRole(href)).toBe("page");
  });

  test("a link to an absolute address opens a new tab", () => {
    for (const href of ["https://example.com/", "http://x", " HTTPS://x", "mailto:op@example.com", "about:srcdoc#x"]) {
      expect(linkRole(href)).toBe("tab");
    }
  });

  test("a relative address and a script address lose the link", () => {
    for (const href of ["verification.html", "/x", "//example.com/p", "./a", "?q=1",
      "javascript:alert(1)", " JavaScript:void(0)", "java\tscript:x", "vbscript:x"]) {
      expect(linkRole(href)).toBe("none");
    }
  });
});

const XLINK = "http://www.w3.org/1999/xlink";

// A minimal stand-in for one parsed element. The key "xlink:href" holds the
// href attribute in the XLink namespace, as the HTML parser names it.
function fakeElement(tag: string, attrs: Record<string, string> = {}) {
  const values = new Map(Object.entries(attrs));
  const xlink = (ns: string, name: string) => (ns === XLINK ? `xlink:${name}` : `${ns}|${name}`);
  return {
    tag,
    removed: false,
    getAttribute: (name: string) => values.get(name) ?? null,
    getAttributeNS: (ns: string, name: string) => values.get(xlink(ns, name)) ?? null,
    setAttribute: (name: string, value: string) => void values.set(name, value),
    removeAttribute: (name: string) => void values.delete(name),
    removeAttributeNS: (ns: string, name: string) => void values.delete(xlink(ns, name)),
    remove() { this.removed = true; },
    attrs: () => Object.fromEntries(values),
  };
}
type FakeElement = ReturnType<typeof fakeElement>;

// A minimal stand-in for a parsed document: a flat list of elements. The
// serialization writes each start tag with its attributes.
function fakeDocument(elements: FakeElement[]) {
  const live = () => elements.filter((element) => !element.removed);
  return {
    querySelectorAll: (selector: string) => {
      const tags = selector.split(",").map((part) => part.trim());
      return live().filter((element) => tags.includes(element.tag));
    },
    documentElement: {
      get outerHTML() {
        return live().map((element) =>
          `<${element.tag}${Object.entries(element.attrs()).map(([name, value]) => ` ${name}="${value}"`).join("")}>`).join("");
      },
    },
  };
}

// A stand-in parser that reads each start tag and its quoted attributes.
function fakeParse(html: string) {
  return fakeDocument([...html.matchAll(/<([a-z][a-z0-9-]*)((?:\s+[a-z][\w:-]*="[^"]*")*)\s*>/gi)].map(([, tag, attrs]) =>
    fakeElement(tag!, Object.fromEntries([...attrs!.matchAll(/([a-z][\w:-]*)="([^"]*)"/gi)].map(([, name, value]) => [name!, value!])))));
}

describe("applyLinkRules", () => {
  test("each link gets the rule of its address", () => {
    const links = [
      fakeElement("a", { href: "#glossary" }),
      fakeElement("a", { href: "#top", target: "_blank" }),
      fakeElement("a", { href: "", target: "_blank" }),
      fakeElement("a", { href: " " }),
      fakeElement("a", { href: "https://example.com/", target: "_top" }),
      fakeElement("area", { href: "https://example.com/map" }),
      fakeElement("a", { href: "verification.html", target: "_blank" }),
      fakeElement("area", { href: "/x" }),
      fakeElement("a", { href: "javascript:alert(1)", target: "_blank" }),
      fakeElement("a", { name: "anchor" }),
    ];
    applyLinkRules(fakeDocument(links));
    expect(links.map((link) => link.attrs())).toEqual([
      { href: "#glossary", target: "_self" },
      { href: "#top", target: "_self" },
      { href: "#", target: "_self" },
      { href: "#", target: "_self" },
      { href: "https://example.com/", target: "_blank", rel: "noopener noreferrer" },
      { href: "https://example.com/map", target: "_blank", rel: "noopener noreferrer" },
      {},
      {},
      {},
      { name: "anchor" },
    ]);
  });

  test("an SVG link address in the XLink namespace gets the same rules", () => {
    const links = [
      fakeElement("a", { "xlink:href": "https://example.com/" }),
      fakeElement("a", { "xlink:href": "verification.html" }),
      fakeElement("a", { "xlink:href": "javascript:alert(1)" }),
      fakeElement("a", { "xlink:href": "" }),
      fakeElement("a", { href: "#glossary", "xlink:href": "https://example.com/" }),
    ];
    applyLinkRules(fakeDocument(links));
    expect(links.map((link) => link.attrs())).toEqual([
      { href: "https://example.com/", target: "_blank", rel: "noopener noreferrer" },
      {},
      {},
      { href: "#", target: "_self" },
      { href: "#glossary", target: "_self" },
    ]);
  });

  test("a template loses its shadow root mode", () => {
    const template = fakeElement("template", { shadowrootmode: "open", shadowroot: "open", id: "t" });
    applyLinkRules(fakeDocument([template]));
    expect(template.attrs()).toEqual({ id: "t" });
  });

  test("an SVG animation of a link address or target is removed", () => {
    const animations = [
      fakeElement("set", { attributeName: "href", to: "https://example.com/" }),
      fakeElement("animate", { attributeName: " xlink:href ", values: "https://example.com/" }),
      fakeElement("set", { attributeName: "target", to: "_self" }),
      fakeElement("animate", { attributeName: "opacity", values: "0;1" }),
    ];
    applyLinkRules(fakeDocument(animations));
    expect(animations.map((animation) => animation.removed)).toEqual([true, true, true, false]);
  });
});

describe("reportDocument", () => {
  const rules = (body: string) => {
    const inputs: string[] = [];
    const doc = reportDocument(body, (html: string) => { inputs.push(html); return fakeParse(html); });
    return { doc, inputs };
  };

  test("the frame document is the stable serialization of the packet document with the link rules", () => {
    const body = "<a href=\"https://example.com/\">x</a><svg><a xlink:href=\"verification.html\">y</a></svg><a href=\"\">top</a>";
    const { doc, inputs } = rules(body);
    expect(inputs[0]).toBe(packetDocument(body));
    expect(inputs).toHaveLength(2);
    expect(inputs[1]).toBe(doc);
    expect(doc.startsWith("<!doctype html><html style=")).toBe(true);
    expect(doc).toContain('<a href="https://example.com/" target="_blank" rel="noopener noreferrer">');
    expect(doc).toContain("<svg><a>");
    expect(doc).toContain('<a href="#" target="_self">');
    expect(doc).not.toContain("xlink:href");
  });

  test("a declarative shadow root does not reach the frame", () => {
    const { doc } = rules("<div><template shadowrootmode=\"open\"><a href=\"https://example.com/\">x</a></template></div>");
    expect(doc).toContain("<div><template><a href=");
    expect(doc).not.toContain("shadowroot");
  });

  test("a link that appears only on a later parse gets the link rules", () => {
    // The first parse holds the link as style text. The serialization of that
    // tree builds the link element on the next parse.
    const mutation = "<form><math><mtext><form><mglyph><style></math><a href=\"https://example.com/p\">x</a>";
    const body = "<form><math><mtext></form><form><mglyph><style></math><a href=\"https://example.com/p\">x</a>";
    const inputs: string[] = [];
    const doc = reportDocument(body, (html: string) => {
      inputs.push(html);
      return inputs.length === 1 ? { querySelectorAll: () => [], documentElement: { outerHTML: mutation } } : fakeParse(html);
    });
    expect(inputs).toEqual([packetDocument(body), `<!doctype html>${mutation}`, doc]);
    expect(doc).toContain('<a href="https://example.com/p" target="_blank" rel="noopener noreferrer">');
  });

  test("a report without a stable serialization shows as plain text", () => {
    let round = 0;
    const body = "<a href=\"https://example.com/\">x</a> & more";
    const doc = reportDocument(body, () => {
      round += 1;
      return { querySelectorAll: () => [], documentElement: { outerHTML: `<html>${round}</html>` } };
    });
    expect(round).toBe(4);
    expect(doc).toBe(packetDocument("<pre style=\"white-space: pre-wrap\">\n&lt;a href=\"https://example.com/\">x&lt;/a> &amp; more</pre>"));
  });
});
