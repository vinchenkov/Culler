import { Database } from "bun:sqlite";
import { randomBytes } from "node:crypto";

// The wiki update rows of the smoke tests. Each function writes rows with SQL
// on the scratch spine.

/** The five statements of the Go test helper `wikitest.RecordCurrentWiki`, in
 * order. `mc/wikitest/wikitest.go` holds the same strings. */
export const STATEMENTS = [
  "INSERT INTO accepted_commits(project,accepted_commit,observed_at) VALUES(?,?,?) ON CONFLICT(project) DO UPDATE SET accepted_commit=excluded.accepted_commit,observed_at=excluded.observed_at WHERE accepted_commits.accepted_commit IS NOT excluded.accepted_commit",
  "INSERT INTO runs(id,tier,role,project,subject,session_path,binding) SELECT ?1,'pipeline','scribe',?2,NULL,'sessions/'||?1,'fake/fake' WHERE NOT EXISTS (SELECT 1 FROM wiki_updates WHERE project=?2 AND ended_at IS NULL)",
  "INSERT INTO wiki_updates(project,run_id,start_commit,started_at,merge_marker,boot_id,acquired_uptime_ms,deadline_uptime_ms) SELECT ?2,?1,?3,?4,(SELECT COALESCE(MAX(r.created_seq),0) FROM merge_operations m JOIN runs r ON r.id=m.run_id WHERE m.project=?2 AND m.operation='apply' AND m.finished=1 AND m.destination_branch='culler' AND m.task_id IS NOT NULL AND json_extract(m.result,'$.applied.commit') IS NOT NULL),'wikitest',0,0 WHERE EXISTS (SELECT 1 FROM runs WHERE id=?1)",
  "UPDATE runs SET ended_at=?2, outcome='completed' WHERE id=?1 AND ended_at IS NULL",
  "UPDATE wiki_updates SET ended_at=?2, result='succeeded', wiki_commit=start_commit, stopped_at=?2 WHERE run_id=?1 AND ended_at IS NULL",
] as const;

/** A time in the format of the spine: UTC, with one-second precision. */
function spineTime(time: Date): string {
  return time.toISOString().replace("T", " ").slice(0, 19);
}

// Runs one write transaction on the scratch spine.
function write<T>(spinePath: string, rows: (db: Database) => T): T {
  const db = new Database(spinePath);
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.exec("PRAGMA foreign_keys = ON");
    return db.transaction(() => rows(db)).immediate();
  } finally {
    db.close();
  }
}

// Records the commit as the accepted commit of the Project and starts one wiki
// update at it. The Scribe run of the wiki update is active.
function start(db: Database, project: string, commit: string, time: string): string {
  const run = randomBytes(8).toString("hex");
  db.run(STATEMENTS[0], [project, commit, time]);
  db.run(STATEMENTS[1], [run, project]);
  if (db.run(STATEMENTS[2], [run, project, commit, time]).changes !== 1) {
    throw new Error(`The Project ${project} has an active wiki update.`);
  }
  return run;
}

/** Records one successful wiki update of the Project at the commit, with no
 * wiki change. The wiki state of the Project is then current. Returns the run
 * identifier, or null when the Project has an active wiki update. */
export function recordCurrentWiki(spinePath: string, project: string, commit: string, startedAt: Date): string | null {
  const run = randomBytes(8).toString("hex");
  const time = spineTime(startedAt);
  return write(spinePath, (db) => {
    db.run(STATEMENTS[0], [project, commit, time]);
    db.run(STATEMENTS[1], [run, project]);
    db.run(STATEMENTS[2], [run, project, commit, time]);
    db.run(STATEMENTS[3], [run, time]);
    return db.run(STATEMENTS[4], [run, time]).changes === 1 ? run : null;
  });
}

/** Gives the run a Claude binding and a main trace with the native session
 * identifier. `mc run list` then computes an AgentsView session for the run. */
export function recordMainTrace(spinePath: string, run: string, nativeSession: string): void {
  write(spinePath, (db) => {
    const row = db.run("UPDATE runs SET binding='claude-sdk/claude', native_session_ref=?2, trace_filename='native.jsonl' WHERE id=?1", [run, nativeSession]);
    if (row.changes !== 1) throw new Error(`The spine holds no run ${run}.`);
  });
}

/** Records one failed wiki update of the Project that started at the commit.
 * The commit becomes the accepted commit of the Project. Returns the run
 * identifier. */
export function seedFailedWikiUpdate(spinePath: string, project: string, commit: string, cause: string): string {
  const time = spineTime(new Date());
  return write(spinePath, (db) => {
    const run = start(db, project, commit, time);
    db.run(STATEMENTS[3], [run, time]);
    db.run("UPDATE wiki_updates SET ended_at=?2, result='failed', cause=?3, stopped_at=?2 WHERE run_id=?1 AND ended_at IS NULL", [run, time, cause]);
    return run;
  });
}

/** Records one active wiki update of the Project that started at the commit.
 * The commit becomes the accepted commit of the Project. The Scribe run has a
 * started agent and a current heartbeat. Returns the run identifier. */
export function seedActiveWikiUpdate(spinePath: string, project: string, commit: string): string {
  const time = spineTime(new Date());
  return write(spinePath, (db) => {
    const run = start(db, project, commit, time);
    db.run("UPDATE runs SET agent_started_at=?2 WHERE id=?1", [run, time]);
    db.run("UPDATE wiki_updates SET last_heartbeat_at=?2, heartbeat_uptime_ms=0 WHERE run_id=?1 AND ended_at IS NULL", [run, time]);
    return run;
  });
}
