package substrate_test

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"

	sqlite3 "modernc.org/sqlite/lib"
)

// openSpine creates a fresh spine on a real temp file, applies the schema,
// and seeds the one fixture Project that every task row references.
func openSpine(t *testing.T) *sql.DB {
	t.Helper()
	db, err := substrate.Open(filepath.Join(t.TempDir(), "spine.db"))
	if err != nil {
		t.Fatalf("open spine: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := substrate.Init(db); err != nil {
		t.Fatalf("init spine: %v", err)
	}
	mkProject(t, db, "ws")
	return db
}

// mkProject inserts one Project row with the profile every Project carries.
func mkProject(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO sandbox_profiles (id, workspace_root) VALUES (?, ?)`, id+"-profile", "/workspaces/"+id)
	mustExec(t, db, `INSERT INTO projects (id, title, sandbox_profile) VALUES (?, ?, ?)`, id, "Project "+id, id+"-profile")
}

// mustExec asserts the statement commits.
func mustExec(t *testing.T, db *sql.DB, q string, args ...any) sql.Result {
	t.Helper()
	res, err := db.Exec(q, args...)
	if err != nil {
		t.Fatalf("want commit, got abort:\n  %s\n  %v", q, err)
	}
	return res
}

// wantAbort requires a database constraint rejection.
func wantAbort(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	wantSQLiteError(t, db, sqlite3.SQLITE_CONSTRAINT, "", q, args...)
}

func wantSQLiteError(t *testing.T, db *sql.DB, code int, message, q string, args ...any) {
	t.Helper()
	_, err := db.Exec(q, args...)
	if err == nil {
		t.Fatalf("want abort, got commit:\n  %s", q)
	}
	var failure interface{ Code() int }
	if !errors.As(err, &failure) || failure.Code()&0xff != code || !strings.Contains(err.Error(), message) {
		t.Fatalf("the statement returned an unexpected database error:\n  %s\n  %v", q, err)
	}
}

// oneStr returns a single scalar as a string; NULL comes back as "<NULL>".
func oneStr(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var v sql.NullString
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	if !v.Valid {
		return "<NULL>"
	}
	return v.String
}

// oneInt returns a single scalar as an int64.
func oneInt(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return v
}

var statuses = []string{"proposed", "seeded", "worked", "verified", "packaged"}

// walkOrder lists the legal-edge walk from birth (proposed) to each status.
var walkOrder = map[string][]string{
	"proposed": {},
	"seeded":   {"seeded"},
	"worked":   {"seeded", "worked"},
	"verified": {"seeded", "worked", "verified"},
	"packaged": {"seeded", "worked", "verified", "packaged"},
}

// mkTask inserts a row of the given scope in the fixture Project and walks
// it along legal edges to the target status.
func mkTask(t *testing.T, db *sql.DB, scope, status string) int64 {
	t.Helper()
	return mkTaskIn(t, db, "ws", scope, status)
}

// mkTaskIn inserts a row in the named Project and walks it to the target status.
func mkTaskIn(t *testing.T, db *sql.DB, project, scope, status string) int64 {
	t.Helper()
	res := mustExec(t, db,
		`INSERT INTO tasks (title, scope, project) VALUES ('fixture', ?, ?)`, scope, project)
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	walkTask(t, db, id, walkOrder[status])
	return id
}

// walkTask moves a task along the given legal edges. The packaged stage
// requires the verified submitted version, so the walk seeds it first.
func walkTask(t *testing.T, db *sql.DB, id int64, stages []string) {
	t.Helper()
	for _, s := range stages {
		if s == "packaged" {
			seedVerifiedVersion(t, db, id)
		}
		mustExec(t, db, `UPDATE tasks SET status = ? WHERE id = ?`, s, id)
	}
}

// mkPacket reserves a review slot for the packaged task.
func mkPacket(t *testing.T, db *sql.DB, taskID int64) {
	t.Helper()
	review := seedReviewVersion(t, db, taskID)
	mustExec(t, db, `UPDATE tasks SET current_review_id=?,review_slot_held=1 WHERE id=?`, review, taskID)
}

func seedReviewVersion(t *testing.T, db *sql.DB, task int64) int64 {
	t.Helper()
	mustExec(t, db, `INSERT INTO review_versions(task_id,version_id,candidate_id,render_path,report_sha256,thesis,criteria,requirements_version)
 SELECT id,submitted_version,candidate_id,'packet.html','hash','The result satisfies the task.',COALESCE(description,''),requirements_version
 FROM tasks WHERE id=? AND NOT EXISTS(SELECT 1 FROM review_versions r WHERE r.task_id=tasks.id AND r.version_id=tasks.submitted_version)`, task)
	return oneInt(t, db, `SELECT r.id FROM review_versions r JOIN tasks t ON t.id=r.task_id AND t.submitted_version=r.version_id WHERE t.id=?`, task)
}

func mkCollieSession(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	mustExec(t, db, `
		INSERT INTO collie_sessions
			(id, container_name, verb_allowlist, session_path, binding)
		VALUES (?, ?, '["collie.list"]', ?, 'fake/fake')`,
		id, "mc-collie-"+id, "sessions/"+id)
}

// cancelTask applies the operator cancel bookkeeping: decision, timestamp,
// and archive in one statement.
func cancelTask(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	mustExec(t, db,
		`UPDATE tasks SET decision = 'cancelled', decided_at = datetime('now'), archived = 1 WHERE id = ?`,
		id)
}

func taskStr(t *testing.T, db *sql.DB, id int64, col string) string {
	t.Helper()
	return oneStr(t, db, `SELECT `+col+` FROM tasks WHERE id = ?`, id)
}

func taskInt(t *testing.T, db *sql.DB, id int64, col string) int64 {
	t.Helper()
	return oneInt(t, db, `SELECT `+col+` FROM tasks WHERE id = ?`, id)
}

// mkEndedRun inserts a finished pipeline run with its repository record.
func mkEndedRun(t *testing.T, db *sql.DB, id, role string, subject int64, inputCommit string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,ended_at,outcome,binding)
		SELECT ?,'pipeline',?,project,id,datetime('now'),'completed','fake' FROM tasks WHERE id=?`, id, role, subject)
	mustExec(t, db, `INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,temporary_bytes,captured,cleaned)
		VALUES(?,?,?,'base',0,1,1)`, id, subject, inputCommit)
}

// seedVerifiedVersion records a submitted Worker version and its Verifier pass
// for the task. The packaged stage requires this state.
func seedVerifiedVersion(t *testing.T, db *sql.DB, task int64) int64 {
	t.Helper()
	round := oneInt(t, db, `SELECT COUNT(*) FROM runs WHERE subject=?`, task)
	verifier := fmt.Sprintf("delivery-verifier-%d-%d", task, round)
	version := seedWorkerVersion(t, db, task)
	mkEndedRun(t, db, verifier, "verifier", task, "source")
	mustExec(t, db, `UPDATE repository_runs SET version_id=? WHERE run_id=?`, version, verifier)
	mustExec(t, db, `INSERT INTO version_verdicts(version_id,run_id,outcome,evidence_path) VALUES(?,?,'pass','evidence.md')`, version, verifier)
	mustExec(t, db, `UPDATE tasks SET submitted_version=?,source_version=? WHERE id=?`, version, version, task)
	return version
}

// seedWorkerVersion records a captured Worker version of the task. The
// version has no verdict.
func seedWorkerVersion(t *testing.T, db *sql.DB, task int64) int64 {
	t.Helper()
	worker := fmt.Sprintf("delivery-worker-%d-%d", task, oneInt(t, db, `SELECT COUNT(*) FROM runs WHERE subject=?`, task))
	mkEndedRun(t, db, worker, "worker", task, "source")
	mustExec(t, db, `INSERT INTO version_submissions(run_id,task_id,input_commit,result_commit) VALUES(?,?,'source','source')`, worker, task)
	mustExec(t, db, `INSERT INTO task_versions(task_id,run_id,input_commit,result_commit,destination_branch,retained_ref)
        VALUES(?,?,'source','source','culler',?)`, task, worker, "refs/test/"+worker)
	version := oneInt(t, db, `SELECT id FROM task_versions WHERE run_id=?`, worker)
	captureVersion(t, db, worker, version)
	return version
}

// captureVersion records the captured version of the producer run in one
// transaction. A capture execution records it and ends with its result, as
// the capture operation does.
func captureVersion(t *testing.T, db *sql.DB, producer string, version int64) {
	t.Helper()
	execution := "capture-" + producer
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	for _, step := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO execution_owners DEFAULT VALUES`, nil},
		{`INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
			SELECT ?,'pipeline','worker',t.project,t.id,'sessions/'||?,'mechanical'
			FROM version_submissions s JOIN tasks t ON t.id=s.task_id WHERE s.run_id=?`, []any{execution, execution, producer}},
		{`INSERT INTO repository_executions(run_id,project,task_id,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms)
			SELECT ?,t.project,t.id,'capture',s.run_id,(SELECT MAX(owner) FROM execution_owners),'{}','fixture',0,300000
			FROM version_submissions s JOIN tasks t ON t.id=s.task_id WHERE s.run_id=?`, []any{execution, producer}},
		{`UPDATE version_submissions SET captured_version=? WHERE run_id=?`, []any{version, producer}},
		{`UPDATE runs SET ended_at=datetime('now'), outcome='completed' WHERE id=?`, []any{execution}},
		{`UPDATE repository_executions SET ended_at=datetime('now'), end_reason='result', stopped_at=datetime('now') WHERE run_id=?`, []any{execution}},
	} {
		if _, err := tx.Exec(step.q, step.args...); err != nil {
			t.Fatalf("capture fixture:\n  %s\n  %v", step.q, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit capture fixture: %v", err)
	}
}

// seedMergedCandidate records a merged candidate for the submitted
// version of the task. Approval requires this state.
func seedMergedCandidate(t *testing.T, db *sql.DB, task int64) int64 {
	t.Helper()
	round := oneInt(t, db, `SELECT COUNT(*) FROM runs WHERE subject=?`, task)
	run := fmt.Sprintf("delivery-merge-%d-%d", task, round)
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,ended_at,outcome,binding)
		SELECT ?,'pipeline','worker',project,id,datetime('now'),'completed','mechanical' FROM tasks WHERE id=?`, run, task)
	mustExec(t, db, `INSERT INTO merge_candidates(task_id,run_id,version_id,destination_branch,destination_commit,candidate_commit,result,status,retained_ref)
		SELECT t.id,?,t.submitted_version,t.destination_branch,'base','candidate','{}','merged',? FROM tasks t WHERE t.id=?`,
		run, "refs/test/"+run, task)
	candidate := oneInt(t, db, `SELECT id FROM merge_candidates WHERE run_id=?`, run)
	mustExec(t, db, `UPDATE tasks SET candidate_id=? WHERE id=?`, candidate, task)
	return candidate
}

// recordReviewApproval records the review version of the submitted version
// and the operator approval of that review.
func recordReviewApproval(t *testing.T, db *sql.DB, task int64) {
	t.Helper()
	packet := seedReviewVersion(t, db, task)
	mustExec(t, db, `INSERT INTO review_decisions(packet_id,decision) VALUES(?,'approved')`, packet)
}

// approveTask records the operator approval of a packaged task with a live
// packet. The task then waits unarchived for its merge.
func approveTask(t *testing.T, db *sql.DB, task int64) {
	t.Helper()
	seedMergedCandidate(t, db, task)
	recordReviewApproval(t, db, task)
	mustExec(t, db, `UPDATE tasks SET decision = 'approved', decided_at = datetime('now') WHERE id = ?`, task)
}
