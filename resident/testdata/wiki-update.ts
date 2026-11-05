// A test fixture uses this helper to give a Project a current wiki. The
// command layer of `mc` then starts no wiki update for the Project.

import { Database } from "bun:sqlite";
import { randomBytes } from "node:crypto";

/** The five statements of the Go test helper `wikitest.RecordCurrentWiki`, in
 * order. `mc/wikitest/wikitest.go` holds the same strings. */
export const STATEMENTS = [
  "INSERT INTO accepted_commits(project,accepted_commit,observed_at) VALUES(?,?,?) ON CONFLICT(project) DO UPDATE SET accepted_commit=excluded.accepted_commit,observed_at=excluded.observed_at WHERE accepted_commits.accepted_commit IS NOT excluded.accepted_commit",
  "INSERT INTO runs(id,tier,role,project,subject,session_path,binding) SELECT ?1,'pipeline','scribe',?2,NULL,'sessions/'||?1,'fake/fake' WHERE NOT EXISTS (SELECT 1 FROM wiki_updates WHERE project=?2 AND ended_at IS NULL)",
  "INSERT INTO wiki_updates(project,run_id,start_commit,started_at,merge_marker,boot_id,acquired_uptime_ms,deadline_uptime_ms) SELECT ?2,?1,?3,?4,(SELECT COALESCE(MAX(r.created_seq),0) FROM merge_operations m JOIN runs r ON r.id=m.run_id WHERE m.project=?2 AND m.operation='apply' AND m.finished=1 AND m.destination_branch='main' AND m.task_id IS NOT NULL AND json_extract(m.result,'$.applied.commit') IS NOT NULL),'wikitest',0,0 WHERE EXISTS (SELECT 1 FROM runs WHERE id=?1)",
  "UPDATE runs SET ended_at=?2, outcome='completed' WHERE id=?1 AND ended_at IS NULL",
  "UPDATE wiki_updates SET ended_at=?2, result='succeeded', wiki_commit=start_commit, stopped_at=?2 WHERE run_id=?1 AND ended_at IS NULL",
] as const;

/** Records one successful wiki update of the Project at the commit, with no
 * wiki change. The wiki state of the Project is then current, and the wiki
 * update interval starts at `startedAt`. Returns the run identifier, or null
 * when the Project has an active wiki update. */
export function recordCurrentWiki(spinePath: string, project: string, commit: string, startedAt: Date): string | null {
  const run = randomBytes(8).toString("hex");
  // The time format of the spine: UTC, with a precision of one second.
  const time = startedAt.toISOString().replace("T", " ").slice(0, 19);
  const db = new Database(spinePath);
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.exec("PRAGMA foreign_keys = ON");
    return db.transaction(() => {
      db.run(STATEMENTS[0], [project, commit, time]);
      db.run(STATEMENTS[1], [run, project]);
      db.run(STATEMENTS[2], [run, project, commit, time]);
      db.run(STATEMENTS[3], [run, time]);
      return db.run(STATEMENTS[4], [run, time]).changes === 1 ? run : null;
    }).immediate();
  } finally {
    db.close();
  }
}
