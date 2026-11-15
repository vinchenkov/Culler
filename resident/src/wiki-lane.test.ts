// wiki-lane.test.ts — the wiki lane against a scripted mc and a manual
// clock: the start call, the interval, one call at a time, the failure log,
// and the stop.

import { afterEach, describe, expect, test } from "bun:test";
import { makeLogger, type LogRecord } from "../../runner/shared/log";
import { createWikiLane, WIKI_INTERVAL_MS, type WikiLane } from "./wiki-lane";
import { deferred, FakeScheduler, ok, settle } from "./test-helpers";
import type { ExecResult } from "./types";

const COMMIT_A = "a".repeat(40);
const COMMIT_B = "b".repeat(40);

class WikiRig {
  scheduler = new FakeScheduler();
  calls: string[][] = [];
  signals: Array<AbortSignal | undefined> = [];
  answers: Array<ExecResult | Promise<ExecResult> | ((signal?: AbortSignal) => Promise<ExecResult>)> = [];
  records: LogRecord[] = [];
  lane: WikiLane = createWikiLane({
    runMc: async (argv, _stdin, options) => {
      this.calls.push(argv);
      this.signals.push(options?.signal);
      const answer = this.answers.shift() ?? projects([]);
      return typeof answer === "function" ? answer(options?.signal) : answer;
    },
    log: makeLogger({
      comp: "resident", level: "debug",
      sink: { write: (line) => { this.records.push(JSON.parse(line) as LogRecord); } },
    }),
    image: "ghcr.io/example/culler-runner@sha256:" + "c".repeat(64),
    schedule: this.scheduler.schedule,
    cancel: this.scheduler.cancel,
  });

  events(name: string): LogRecord[] {
    return this.records.filter((record) => record.event === name);
  }
}

function projects(rows: Array<Record<string, unknown>>): ExecResult {
  return ok(JSON.stringify({ projects: rows }));
}

const rigs: WikiRig[] = [];
function rig(): WikiRig {
  const made = new WikiRig();
  rigs.push(made);
  return made;
}

afterEach(async () => {
  await Promise.all(rigs.splice(0).map((made) => made.lane.stop()));
});

describe("wiki lane", () => {
  test("start exports at once, then 20 seconds after each call ends", async () => {
    const r = rig();
    r.lane.start();
    await r.scheduler.advance(0);
    expect(r.calls).toEqual([["__wiki-export", "--image", `ghcr.io/example/culler-runner@sha256:${"c".repeat(64)}`]]);
    expect(r.scheduler.pending()).toEqual([WIKI_INTERVAL_MS]);
    await r.scheduler.advance(WIKI_INTERVAL_MS - 1);
    expect(r.calls).toHaveLength(1);
    await r.scheduler.advance(1);
    expect(r.calls).toHaveLength(2);
  });

  test("one export call runs at a time", async () => {
    const r = rig();
    const slow = deferred<ExecResult>();
    r.answers.push(slow.promise);
    r.lane.start();
    await r.scheduler.advance(0);
    const extra = r.lane.exportNow();
    await r.scheduler.advance(WIKI_INTERVAL_MS * 5);
    expect(r.calls).toHaveLength(1);
    expect(r.scheduler.pending()).toEqual([]);
    slow.resolve(projects([]));
    await extra;
    await settle();
    expect(r.calls).toHaveLength(2);
    expect(r.scheduler.pending()).toEqual([WIKI_INTERVAL_MS]);
  });

  test("each failed Project is logged once per commit and cause, and the previous export stays in use", async () => {
    const r = rig();
    const failed = (commit: string, error: string) =>
      projects([{ project: "alpha", status: "failed", commit, error }, { project: "beta", status: "current", commit: COMMIT_A }]);
    r.answers.push(
      failed(COMMIT_A, "the export command timed out"),
      failed(COMMIT_A, "the export command timed out"),
      failed(COMMIT_A, "the export command exited 1"),
      failed(COMMIT_B, "the export command exited 1"),
      projects([{ project: "alpha", status: "exported", commit: COMMIT_B }]),
      failed(COMMIT_B, "the export command exited 1"),
    );
    for (let call = 0; call < 6; call += 1) await r.lane.exportNow();
    expect(r.events("wiki.export_failed").map((record) => [record.project, record.commit, record.msg])).toEqual([
      ["alpha", COMMIT_A, `The wiki export of Project alpha at commit ${COMMIT_A} failed, so the previous export stays in use: the export command timed out`],
      ["alpha", COMMIT_A, `The wiki export of Project alpha at commit ${COMMIT_A} failed, so the previous export stays in use: the export command exited 1`],
      ["alpha", COMMIT_B, `The wiki export of Project alpha at commit ${COMMIT_B} failed, so the previous export stays in use: the export command exited 1`],
      ["alpha", COMMIT_B, `The wiki export of Project alpha at commit ${COMMIT_B} failed, so the previous export stays in use: the export command exited 1`],
    ]);
    expect(r.events("wiki.exported").map((record) => record.msg))
      .toEqual([`The wiki export of Project alpha is now at commit ${COMMIT_B}.`]);
  });

  test("a failed call is logged once per cause", async () => {
    const r = rig();
    const refused = { exitCode: 2, stdout: "", stderr: "mc: docker is unavailable" };
    r.answers.push(refused, refused, ok("not json"), projects([]), refused);
    for (let call = 0; call < 5; call += 1) await r.lane.exportNow();
    expect(r.events("wiki.export_failed").map((record) => record.msg)).toEqual([
      "mc __wiki-export exited 2: mc: docker is unavailable",
      "mc __wiki-export returned an invalid response: the output is not JSON",
      "mc __wiki-export exited 2: mc: docker is unavailable",
    ]);
  });

  test("stop ends the call in flight and waits for it", async () => {
    const r = rig();
    let ended = false;
    r.answers.push((signal) => new Promise<ExecResult>((resolve) => {
      signal?.addEventListener("abort", () => {
        ended = true;
        resolve({ exitCode: 143, stdout: "", stderr: "" });
      });
    }));
    r.lane.start();
    await r.scheduler.advance(0);
    await r.lane.stop();
    expect(ended).toBe(true);
    expect(r.signals[0]!.aborted).toBe(true);
    expect(r.scheduler.pending()).toEqual([]);
    expect(r.events("wiki.export_failed")).toEqual([]);
  });
});
