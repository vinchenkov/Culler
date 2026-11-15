// Unit tests for format.js — the shared stamp formatters. Every absolute stamp
// renders in the browser zone; each case names the zone it asserts under, so
// the suite reads the same in every zone it runs in.

import { describe, expect, test } from "bun:test";
import { clockLabel, dateTimeLabel, leaseStateText, localStamp, pauseAllView, relativeAge, stampLabel, utcMs, projectStateText } from "./format.js";

describe("projectStateText", () => {
  test("names the pause state of one Project", () => {
    expect(projectStateText("archived")).toBe("Archived");
    expect(projectStateText("paused")).toBe("Project paused");
    expect(projectStateText("active")).toBe("Pipeline active");
    expect(projectStateText("paused", "vision")).toBe("Initialization incomplete");
    expect(projectStateText("paused", "configured")).toBe("Initialization incomplete");
    expect(projectStateText("paused", "ready")).toBe("Project paused");
    expect(projectStateText("archived", "configured")).toBe("Archived");
  });
});

describe("pauseAllView", () => {
  const row = (status: string, setup_status = "ready") => ({ status, setup_status });

  test("offers bulk controls for at least two eligible Projects", () => {
    expect(pauseAllView([row("active"), row("active")])).toEqual({ allPaused: false, available: true });
    expect(pauseAllView([row("active"), row("paused")])).toEqual({ allPaused: false, available: true });
    expect(pauseAllView([row("paused"), row("paused"), row("paused", "vision"), row("archived")])).toEqual({ allPaused: true, available: true });
  });

  test("hides bulk controls with fewer than two eligible Projects", () => {
    expect(pauseAllView([row("active"), row("paused", "configured")]).available).toBe(false);
    expect(pauseAllView([row("paused"), row("archived")])).toEqual({ allPaused: true, available: false });
    expect(pauseAllView([row("paused", "vision"), row("archived")]).available).toBe(false);
    expect(pauseAllView(undefined)).toEqual({ allPaused: false, available: false });
  });
});

describe("leaseStateText", () => {
  test("names the lease state of one Project", () => {
    expect(leaseStateText({ held: 0, stale: false })).toBe("Lease free");
    expect(leaseStateText({ held: 1, stale: false })).toBe("Lease held");
    expect(leaseStateText({ held: 1, stale: true })).toBe("Lease stale");
  });
});

describe("dateTimeLabel", () => {
  test("renders a spine stamp with its year in the named zone", () => {
    expect(dateTimeLabel("2026-01-01 05:21:46", "America/Los_Angeles")).toBe("Dec 31, 2025 21:21");
    expect(dateTimeLabel("2026-09-08 05:21:46", "UTC")).toBe("Sep 8, 2026 05:21");
  });

  test("an unreadable stamp renders empty", () => {
    expect(dateTimeLabel("")).toBe("");
    expect(dateTimeLabel(null)).toBe("");
    expect(dateTimeLabel("not a stamp")).toBe("");
  });
});

describe("stampLabel", () => {
  test("renders diagnostic timestamps with their UTC marker and fractional seconds", () => {
    expect(stampLabel("2026-09-20T06:28:42.438307Z", "America/Los_Angeles")).toBe("Sep 19 23:28");
    expect(stampLabel("2026-09-19T23:28:42-07:00", "America/Los_Angeles")).toBe("Sep 19 23:28");
  });

  test("renders a spine stamp in the named zone", () => {
    expect(stampLabel("2026-09-08 05:21:46", "America/Los_Angeles")).toBe("Sep 07 22:21");
    expect(stampLabel("2026-09-08 05:21:46", "Asia/Tokyo")).toBe("Sep 08 14:21");
    expect(stampLabel("2026-09-08 05:21:46", "UTC")).toBe("Sep 08 05:21");
  });

  test("agrees with the header clock on the hour and the date", () => {
    const ms = utcMs("2026-09-08 05:21:46");
    expect(clockLabel(ms, "America/Los_Angeles")).toBe("22:21");
    expect(stampLabel("2026-09-08 05:21:46", "America/Los_Angeles").endsWith("22:21")).toBe(true);
    expect(clockLabel(utcMs("2026-09-08 00:05:00"), "UTC")).toBe("00:05");
  });

  test("an unreadable stamp renders empty", () => {
    expect(stampLabel("")).toBe("");
    expect(stampLabel(null)).toBe("");
    expect(stampLabel("not a stamp")).toBe("");
  });
});

describe("localStamp", () => {
  test("renders a log record stamp in the named zone with a numeric offset", () => {
    expect(localStamp("2026-09-08T05:21:46.377Z", "America/Los_Angeles")).toBe("2026-09-07T22:21:46.377-07:00");
    expect(localStamp("2026-09-08T05:21:46.377Z", "Asia/Kolkata")).toBe("2026-09-08T10:51:46.377+05:30");
    expect(localStamp("2026-09-08T05:21:46.377Z", "UTC")).toBe("2026-09-08T05:21:46.377+00:00");
    expect(localStamp("2026-01-08T05:21:46.000Z", "America/Los_Angeles")).toBe("2026-01-07T21:21:46.000-08:00");
  });

  test("an unreadable stamp renders empty", () => {
    expect(localStamp("t1")).toBe("");
    expect(localStamp("")).toBe("");
    expect(localStamp(undefined)).toBe("");
  });
});

describe("relative ages", () => {
  test("stay the same in every zone", () => {
    const now = utcMs("2026-09-08 05:21:46");
    expect(relativeAge("2026-09-08 03:21:46", now)).toBe("2h");
    expect(stampLabel("2026-09-08 03:21:46", "Asia/Tokyo")).toBe("Sep 08 12:21");
    expect(stampLabel("2026-09-08 03:21:46", "America/Los_Angeles")).toBe("Sep 07 20:21");
  });
});
