// The smoke fixture of the wiki updates holds a copy of the statements of the
// Go test helper. The fast lane compares the copy with the Go file. It also
// runs each function of the fixture on a scratch database with the current
// schema.

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { copyFileSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { recordCurrentWiki, recordMainTrace, seedActiveWikiUpdate, seedFailedWikiUpdate, STATEMENTS } from "../smoke/wiki-fixture";
import { createDatabase, readRows } from "../smoke/wiki-fixture-database";

const mcRoot = join(import.meta.dir, "..", "..", "mc");
const suiteRoot = mkdtempSync(join(tmpdir(), "dashboard-wiki-fixture-"));
const template = join(suiteRoot, "template.db");
const PROJECT = "ws-test";
const FIRST = "a".repeat(40);
const SECOND = "b".repeat(40);
const THIRD = "c".repeat(40);
// The native session identifier of a main trace.
const SESSION = "33333333-3333-4333-8333-333333333333";
const RUN = /^[0-9a-f]{16}$/;
// The time format of the spine: UTC, with a precision of one second.
const TIME = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/;

beforeAll(() => {
  // The schema is the three files of `mc/substrate/substrate.go`, in its order.
  const schema = ["schema.sql", "delivery.sql", "environments.sql"]
    .map((name) => readFileSync(join(mcRoot, "substrate", name), "utf8"))
    .join("\n");
  createDatabase(template, schema, PROJECT);
});

afterAll(() => {
  rmSync(suiteRoot, { recursive: true, force: true });
});

/** A scratch database with the current schema and the one Project PROJECT. Returns its path. */
function scratchSpine(): string {
  const spine = join(mkdtempSync(join(suiteRoot, "spine-")), "spine.db");
  copyFileSync(template, spine);
  return spine;
}

const acceptedCommits = (spine: string) => readRows(spine, "SELECT project, accepted_commit, observed_at FROM accepted_commits ORDER BY project");

const runs = (spine: string) => readRows(spine, `
  SELECT id, tier, role, project, subject, session_path, binding, ended_at, outcome, agent_started_at
  FROM runs ORDER BY created_seq`);

const wikiUpdates = (spine: string) => readRows(spine, `
  SELECT project, run_id, start_commit, wiki_commit, started_at, ended_at, result, cause, counted, stopped_at,
    last_heartbeat_at, heartbeat_uptime_ms, merged_commit
  FROM wiki_updates ORDER BY id`);

const traces = (spine: string) => readRows(spine, "SELECT id, binding, native_session_ref, trace_filename FROM runs ORDER BY created_seq");

test("the wiki fixture holds the five statements of the Go test helper", () => {
  const source = readFileSync(join(mcRoot, "wikitest", "wikitest.go"), "utf8");
  const block = source.match(/^var Statements = \[5\]string\{\n([\s\S]*?)\n\}$/m);
  const statements = [...(block?.[1] ?? "").matchAll(/^\t`([^`]*)`,$/gm)].map((match) => match[1]);
  expect(statements).toHaveLength(5);
  expect(statements).toEqual([...STATEMENTS]);
});

describe("the wiki fixture on a scratch database with the current schema", () => {
  test("recordCurrentWiki records one successful wiki update with no wiki change", () => {
    const spine = scratchSpine();
    const run = recordCurrentWiki(spine, PROJECT, FIRST, new Date(Date.UTC(2026, 0, 2, 3, 4, 5, 678)));
    expect(run).toMatch(RUN);
    const time = "2026-01-02 03:04:05";
    expect(acceptedCommits(spine)).toEqual([{ project: PROJECT, accepted_commit: FIRST, observed_at: time }]);
    expect(runs(spine)).toEqual([{
      id: run, tier: "pipeline", role: "scribe", project: PROJECT, subject: null,
      session_path: `sessions/${run}`, binding: "fake/fake", ended_at: time, outcome: "completed", agent_started_at: null,
    }]);
    expect(wikiUpdates(spine)).toEqual([{
      project: PROJECT, run_id: run, start_commit: FIRST, wiki_commit: FIRST, started_at: time, ended_at: time,
      result: "succeeded", cause: null, counted: 1, stopped_at: time,
      last_heartbeat_at: null, heartbeat_uptime_ms: null, merged_commit: null,
    }]);
  });

  test("seedFailedWikiUpdate records one failed wiki update with its cause", () => {
    const spine = scratchSpine();
    const run = seedFailedWikiUpdate(spine, PROJECT, FIRST, "The Scribe run reached its time limit.");
    expect(run).toMatch(RUN);
    expect(acceptedCommits(spine)).toEqual([{ project: PROJECT, accepted_commit: FIRST, observed_at: expect.stringMatching(TIME) }]);
    expect(runs(spine)).toEqual([{
      id: run, tier: "pipeline", role: "scribe", project: PROJECT, subject: null,
      session_path: `sessions/${run}`, binding: "fake/fake", ended_at: expect.stringMatching(TIME), outcome: "completed",
      agent_started_at: null,
    }]);
    expect(wikiUpdates(spine)).toEqual([{
      project: PROJECT, run_id: run, start_commit: FIRST, wiki_commit: null, started_at: expect.stringMatching(TIME),
      ended_at: expect.stringMatching(TIME), result: "failed", cause: "The Scribe run reached its time limit.", counted: 1,
      stopped_at: expect.stringMatching(TIME), last_heartbeat_at: null, heartbeat_uptime_ms: null, merged_commit: null,
    }]);
  });

  test("seedActiveWikiUpdate records one active wiki update with a started agent and a heartbeat", () => {
    const spine = scratchSpine();
    const run = seedActiveWikiUpdate(spine, PROJECT, FIRST);
    expect(run).toMatch(RUN);
    expect(acceptedCommits(spine)).toEqual([{ project: PROJECT, accepted_commit: FIRST, observed_at: expect.stringMatching(TIME) }]);
    expect(runs(spine)).toEqual([{
      id: run, tier: "pipeline", role: "scribe", project: PROJECT, subject: null,
      session_path: `sessions/${run}`, binding: "fake/fake", ended_at: null, outcome: null,
      agent_started_at: expect.stringMatching(TIME),
    }]);
    expect(wikiUpdates(spine)).toEqual([{
      project: PROJECT, run_id: run, start_commit: FIRST, wiki_commit: null, started_at: expect.stringMatching(TIME),
      ended_at: null, result: null, cause: null, counted: 1, stopped_at: null,
      last_heartbeat_at: expect.stringMatching(TIME), heartbeat_uptime_ms: 0, merged_commit: null,
    }]);
  });

  test("a later call records a later wiki update", () => {
    const spine = scratchSpine();
    const first = recordCurrentWiki(spine, PROJECT, FIRST, new Date(Date.UTC(2026, 0, 2, 3, 0, 0)));
    const failed = seedFailedWikiUpdate(spine, PROJECT, SECOND, "The wiki update failed.");
    const third = recordCurrentWiki(spine, PROJECT, THIRD, new Date(Date.UTC(2026, 0, 2, 5, 0, 0)));
    expect(new Set([first, failed, third]).size).toBe(3);
    expect(wikiUpdates(spine).map((row) => [row.run_id, row.start_commit, row.result])).toEqual([
      [first, FIRST, "succeeded"],
      [failed, SECOND, "failed"],
      [third, THIRD, "succeeded"],
    ]);
    expect(acceptedCommits(spine).map((row) => row.accepted_commit)).toEqual([THIRD]);
  });

  test("an active wiki update stops each function that records a wiki update", () => {
    const spine = scratchSpine();
    const active = seedActiveWikiUpdate(spine, PROJECT, FIRST);

    // The two seed functions stop with an error, and the transaction writes no row.
    expect(() => seedFailedWikiUpdate(spine, PROJECT, SECOND, "The wiki update failed.")).toThrow("has an active wiki update");
    expect(() => seedActiveWikiUpdate(spine, PROJECT, SECOND)).toThrow("has an active wiki update");
    expect(acceptedCommits(spine).map((row) => row.accepted_commit)).toEqual([FIRST]);

    // recordCurrentWiki returns null. As the Go helper, it still records the accepted commit.
    expect(recordCurrentWiki(spine, PROJECT, THIRD, new Date())).toBeNull();
    expect(acceptedCommits(spine).map((row) => row.accepted_commit)).toEqual([THIRD]);
    expect(runs(spine).map((row) => row.id)).toEqual([active]);
    expect(wikiUpdates(spine).map((row) => [row.run_id, row.ended_at])).toEqual([[active, null]]);
  });

  test("each function that records a wiki update refuses a Project that the database does not hold", () => {
    const spine = scratchSpine();
    expect(() => recordCurrentWiki(spine, "absent", FIRST, new Date())).toThrow("FOREIGN KEY constraint failed");
    expect(() => seedFailedWikiUpdate(spine, "absent", FIRST, "The wiki update failed.")).toThrow("FOREIGN KEY constraint failed");
    expect(() => seedActiveWikiUpdate(spine, "absent", FIRST)).toThrow("FOREIGN KEY constraint failed");
    expect(acceptedCommits(spine)).toEqual([]);
    expect(runs(spine)).toEqual([]);
    expect(wikiUpdates(spine)).toEqual([]);
  });

  test("recordMainTrace gives one run a Claude binding and a main trace", () => {
    const spine = scratchSpine();
    const traced = recordCurrentWiki(spine, PROJECT, FIRST, new Date(Date.UTC(2026, 0, 2, 3, 0, 0)))!;
    const other = seedFailedWikiUpdate(spine, PROJECT, SECOND, "The wiki update failed.");
    recordMainTrace(spine, traced, SESSION);
    expect(traces(spine)).toEqual([
      { id: traced, binding: "claude-sdk/claude", native_session_ref: SESSION, trace_filename: "native.jsonl" },
      { id: other, binding: "fake/fake", native_session_ref: null, trace_filename: null },
    ]);
    // The function refuses a run that the database does not hold.
    expect(() => recordMainTrace(spine, "absent", SESSION)).toThrow("The spine holds no run absent.");
  });
});
