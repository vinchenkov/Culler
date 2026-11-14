// The helper of a current Project wiki, against the Go helper and against a
// spine that the real mc binary initialized.

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { Database } from "bun:sqlite";
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { recordCurrentWiki, STATEMENTS } from "./wiki-update";

const repoRoot = resolve(import.meta.dir, "..", "..");
const mcSourceDir = join(repoRoot, "mc", "cmd", "mc");
const suiteRoot = realpathSync(mkdtempSync(join(tmpdir(), "mc-wiki-update-helper-")));
const mcBin = join(suiteRoot, "mc");
const PROJECT = "ws-test";
const FIRST = "a".repeat(40);
const SECOND = "b".repeat(40);
const THIRD = "c".repeat(40);

beforeAll(() => {
  const tags = process.platform === "linux" ? "test_fake_routing,test_scratch_spine" : "test_fake_routing";
  const built = Bun.spawnSync(["mise", "exec", "--", "go", "build", "-tags", tags, "-o", mcBin, "."], {
    cwd: mcSourceDir,
    env: process.env,
  });
  if (built.exitCode !== 0) {
    throw new Error(`build real mc test binary: ${built.stderr.toString() || built.stdout.toString()}`);
  }
}, 300_000);

afterAll(() => {
  rmSync(suiteRoot, { recursive: true, force: true });
});

function mc(home: string, spine: string | undefined, argv: string[]): Record<string, unknown> {
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (value === undefined) continue;
    if (["MC_HOME", "MC_SPINE", "MC_HELPER", "MC_RUN_JSON"].includes(key)) continue;
    env[key] = value;
  }
  env.MC_HOME = home;
  env.MC_RUN_JSON = join(home, "absent-run.json");
  if (spine !== undefined) env.MC_SPINE = spine;
  const result = Bun.spawnSync([mcBin, ...argv], { env, stdout: "pipe", stderr: "pipe" });
  if (result.exitCode !== 0) {
    throw new Error(`mc ${argv.join(" ")} exited ${result.exitCode}: ${result.stderr.toString() || result.stdout.toString()}`);
  }
  return JSON.parse(result.stdout.toString()) as Record<string, unknown>;
}

/** A spine with the one Project PROJECT. Returns the home and the spine path. */
function scratchSpine(): { home: string; spine: string } {
  const home = mkdtempSync(join(suiteRoot, "home-"));
  const workspace = join(home, "workspace");
  mkdirSync(workspace);
  const spine = join(home, "spine.db");
  mc(home, undefined, ["__init", "--spine", spine, "--project", PROJECT, "--workspace-root", workspace]);
  return { home, spine };
}

function rows(spine: string, query: string): Record<string, unknown>[] {
  const db = new Database(spine, { readonly: true });
  try {
    return db.query(query).all() as Record<string, unknown>[];
  } finally {
    db.close();
  }
}

const wikiUpdates = (spine: string) => rows(spine, `
  SELECT project, run_id, start_commit, wiki_commit, started_at, ended_at, result, cause, counted, stopped_at, merged_commit
  FROM wiki_updates ORDER BY id`);

describe("the helper of a current Project wiki", () => {
  test("the statements equal the statements of the Go helper", () => {
    const source = readFileSync(join(repoRoot, "mc", "wikitest", "wikitest.go"), "utf8");
    const block = source.match(/^var Statements = \[5\]string\{\n([\s\S]*?)\n\}$/m);
    const statements = [...(block?.[1] ?? "").matchAll(/^\t`([^`]*)`,$/gm)].map((match) => match[1]);
    expect(statements).toEqual([...STATEMENTS]);
  });

  test("the helper records one successful wiki update with no wiki change", () => {
    const { spine } = scratchSpine();
    const run = recordCurrentWiki(spine, PROJECT, FIRST, new Date(Date.UTC(2026, 0, 2, 3, 4, 5, 678)));
    expect(run).toMatch(/^[0-9a-f]{16}$/);
    const time = "2026-01-02 03:04:05";
    expect(rows(spine, "SELECT project, accepted_commit, observed_at FROM accepted_commits")).toEqual([
      { project: PROJECT, accepted_commit: FIRST, observed_at: time },
    ]);
    expect(rows(spine, "SELECT id, tier, role, project, subject, session_path, binding, ended_at, outcome FROM runs")).toEqual([{
      id: run, tier: "pipeline", role: "scribe", project: PROJECT, subject: null,
      session_path: `sessions/${run}`, binding: "fake/fake", ended_at: time, outcome: "completed",
    }]);
    expect(wikiUpdates(spine)).toEqual([{
      project: PROJECT, run_id: run, start_commit: FIRST, wiki_commit: FIRST, started_at: time, ended_at: time,
      result: "succeeded", cause: null, counted: 1, stopped_at: time, merged_commit: null,
    }]);
  });

  test("mc reads the wiki of the Project as current after the helper", () => {
    const { home, spine } = scratchSpine();
    const run = recordCurrentWiki(spine, PROJECT, FIRST, new Date());
    const listed = mc(home, spine, ["wiki-state", "list"]);
    expect(listed.wiki_states).toEqual([expect.objectContaining({
      project: PROJECT, state: "current", accepted_commit: FIRST, active: null,
      last: expect.objectContaining({ run_id: run, result: "succeeded", start_commit: FIRST, wiki_commit: FIRST, merged_commit: null }),
    })]);
  });

  test("a later call records a later wiki update, and an active wiki update stops the helper", () => {
    const { spine } = scratchSpine();
    const first = recordCurrentWiki(spine, PROJECT, FIRST, new Date(Date.UTC(2026, 0, 2, 3, 0, 0)));
    const second = recordCurrentWiki(spine, PROJECT, SECOND, new Date(Date.UTC(2026, 0, 2, 5, 0, 0)));
    expect(second).toMatch(/^[0-9a-f]{16}$/);
    expect(second).not.toBe(first);
    expect(wikiUpdates(spine).map((row) => [row.run_id, row.start_commit, row.started_at])).toEqual([
      [first, FIRST, "2026-01-02 03:00:00"],
      [second, SECOND, "2026-01-02 05:00:00"],
    ]);

    // An active wiki update: the two insert statements of the helper with no end.
    const active = "0123456789abcdef";
    const db = new Database(spine);
    try {
      db.run(STATEMENTS[1], [active, PROJECT]);
      db.run(STATEMENTS[2], [active, PROJECT, SECOND, "2026-01-02 06:00:00"]);
    } finally {
      db.close();
    }
    expect(recordCurrentWiki(spine, PROJECT, THIRD, new Date(Date.UTC(2026, 0, 2, 7, 0, 0)))).toBeNull();
    expect(wikiUpdates(spine).map((row) => [row.run_id, row.ended_at])).toEqual([
      [first, "2026-01-02 03:00:00"],
      [second, "2026-01-02 05:00:00"],
      [active, null],
    ]);
    expect(rows(spine, "SELECT COUNT(*) AS count FROM runs")).toEqual([{ count: 3 }]);
    // The helper still records the commit as the accepted commit.
    expect(rows(spine, "SELECT accepted_commit FROM accepted_commits")).toEqual([{ accepted_commit: THIRD }]);
  });
});
