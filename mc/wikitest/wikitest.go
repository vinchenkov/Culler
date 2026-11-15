// Package wikitest gives a test fixture a Project wiki with the state current.
// The command layer then starts no wiki update for the Project.
package wikitest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"mc/domain"
)

// Statements holds the statements of RecordCurrentWiki in their order. Each
// statement is one raw string literal on its own line: the fixtures in other
// languages hold the same five strings.
var Statements = [5]string{
	`INSERT INTO accepted_commits(project,accepted_commit,observed_at) VALUES(?,?,?) ON CONFLICT(project) DO UPDATE SET accepted_commit=excluded.accepted_commit,observed_at=excluded.observed_at WHERE accepted_commits.accepted_commit IS NOT excluded.accepted_commit`,
	`INSERT INTO runs(id,tier,role,project,subject,session_path,binding) SELECT ?1,'pipeline','scribe',?2,NULL,'sessions/'||?1,'fake/fake' WHERE NOT EXISTS (SELECT 1 FROM wiki_updates WHERE project=?2 AND ended_at IS NULL)`,
	`INSERT INTO wiki_updates(project,run_id,start_commit,started_at,merge_marker,boot_id,acquired_uptime_ms,deadline_uptime_ms) SELECT ?2,?1,?3,?4,(SELECT COALESCE(MAX(r.created_seq),0) FROM merge_operations m JOIN runs r ON r.id=m.run_id WHERE m.project=?2 AND m.operation='apply' AND m.finished=1 AND m.destination_branch='culler' AND m.task_id IS NOT NULL AND json_extract(m.result,'$.applied.commit') IS NOT NULL),'wikitest',0,0 WHERE EXISTS (SELECT 1 FROM runs WHERE id=?1)`,
	`UPDATE runs SET ended_at=?2, outcome='completed' WHERE id=?1 AND ended_at IS NULL`,
	`UPDATE wiki_updates SET ended_at=?2, result='succeeded', wiki_commit=start_commit, stopped_at=?2 WHERE run_id=?1 AND ended_at IS NULL`,
}

// RecordCurrentWiki records one successful wiki update of the Project at the commit, with no wiki change.
// It returns the run identifier and true, or "" and false when the Project has an active wiki update.
func RecordCurrentWiki(ctx context.Context, db *sql.DB, project, commit string, startedAt time.Time) (string, bool, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", false, err
	}
	run := hex.EncodeToString(raw[:])
	at := startedAt.UTC().Format(domain.SpineTime)

	conn, err := db.Conn(ctx)
	if err != nil {
		return "", false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", false, err
	}
	var recorded int64
	for i, args := range [5][]any{
		{project, commit, at},
		{run, project},
		{run, project, commit, at},
		{run, at},
		{run, at},
	} {
		res, err := conn.ExecContext(ctx, Statements[i], args...)
		if err == nil {
			recorded, err = res.RowsAffected()
		}
		if err != nil {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
			return "", false, err
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return "", false, err
	}
	if recorded != 1 {
		return "", false, nil
	}
	return run, true, nil
}
