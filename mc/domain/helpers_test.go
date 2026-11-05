package domain_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"mc/domain"
	"mc/substrate"
)

// openSpine creates a fresh spine on a real temp file and seeds the fixture
// Project. The schema trigger gives the Project its check configuration.
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

// tx runs fn under BEGIN IMMEDIATE, the production transaction shape of every
// domain function. An error from fn rolls the transaction back.
func tx(t *testing.T, db *sql.DB, fn func(ctx context.Context, q domain.Q) error) error {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin immediate: %v", err)
	}
	if err := fn(ctx, conn); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	return nil
}

// mustTx asserts fn commits.
func mustTx(t *testing.T, db *sql.DB, fn func(ctx context.Context, q domain.Q) error) {
	t.Helper()
	if err := tx(t, db, fn); err != nil {
		t.Fatalf("want commit, got: %v", err)
	}
}

// wantCode asserts fn is rejected with the named DomainError code.
func wantCode(t *testing.T, db *sql.DB, code string, fn func(ctx context.Context, q domain.Q) error) {
	t.Helper()
	err := tx(t, db, fn)
	if err == nil {
		t.Fatalf("want DomainError %q, got commit", code)
	}
	var de *domain.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("want DomainError %q, got %T: %v", code, err, err)
	}
	if de.Code != code {
		t.Fatalf("DomainError code = %q (%s), want %q", de.Code, de.Msg, code)
	}
}

// wantReject asserts fn is rejected. The rejection can come from the domain
// or from the substrate.
func wantReject(t *testing.T, db *sql.DB, fn func(ctx context.Context, q domain.Q) error) {
	t.Helper()
	if err := tx(t, db, fn); err == nil {
		t.Fatal("want rejection, got commit")
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) sql.Result {
	t.Helper()
	res, err := db.Exec(q, args...)
	if err != nil {
		t.Fatalf("want commit, got abort:\n  %s\n  %v", q, err)
	}
	return res
}

// wantAbort asserts the raw SQL write aborts on the substrate.
func wantAbort(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err == nil {
		t.Fatalf("want substrate abort, got commit:\n  %s", q)
	}
}

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

func oneInt(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return v
}

func taskStr(t *testing.T, db *sql.DB, id int64, col string) string {
	t.Helper()
	return oneStr(t, db, `SELECT `+col+` FROM tasks WHERE id = ?`, id)
}

func taskInt(t *testing.T, db *sql.DB, id int64, col string) int64 {
	t.Helper()
	return oneInt(t, db, `SELECT `+col+` FROM tasks WHERE id = ?`, id)
}

func deliveryStr(t *testing.T, db *sql.DB, id int64, col string) string {
	t.Helper()
	return oneStr(t, db, `SELECT `+col+` FROM tasks WHERE id = ?`, id)
}

func deliveryInt(t *testing.T, db *sql.DB, id int64, col string) int64 {
	t.Helper()
	return oneInt(t, db, `SELECT `+col+` FROM tasks WHERE id = ?`, id)
}

var walkOrder = map[string][]string{
	"proposed": {},
	"seeded":   {"seeded"},
	"worked":   {"seeded", "worked"},
	"verified": {"seeded", "worked", "verified"},
	"packaged": {"seeded", "worked", "verified", "packaged"},
}

// mkTask inserts a row in the fixture Project and walks it to the target status.
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

// mkRun inserts a bare runs row.
func mkRun(t *testing.T, db *sql.DB, id, role string, subject int64) {
	t.Helper()
	mustExec(t, db, `
		INSERT INTO runs (id, tier, role, project, subject)
		SELECT ?, 'pipeline', ?, project, id FROM tasks WHERE id = ?`, id, role, subject)
}

// mkPacket inserts a packet for a packaged task.
func seedReviewVersion(t *testing.T, db *sql.DB, taskID int64) {
	t.Helper()
	mustExec(t, db, `INSERT INTO review_versions(task_id,version_id,candidate_id,render_path,report_sha256,thesis,criteria,requirements_version)
 SELECT id,submitted_version,candidate_id,'packet.html','hash','thesis',COALESCE(description,''),requirements_version FROM tasks WHERE id=? ON CONFLICT(task_id,version_id) DO NOTHING`, taskID)
}

func mkPacket(t *testing.T, db *sql.DB, taskID int64) {
	t.Helper()
	seedReviewVersion(t, db, taskID)
	mustExec(t, db, `UPDATE tasks SET current_review_id=(SELECT id FROM review_versions WHERE task_id=tasks.id AND version_id=tasks.submitted_version),review_slot_held=1 WHERE id=?`, taskID)
}

// mkEndedRun inserts a finished pipeline run with its repository record.
func mkEndedRun(t *testing.T, db *sql.DB, id, role string, subject int64, inputCommit string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,ended_at,outcome,binding)
		SELECT ?,'pipeline',?,project,id,datetime('now'),'completed','fake' FROM tasks WHERE id=?`, id, role, subject)
	mustExec(t, db, `INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,temporary_bytes,captured,cleaned)
		VALUES(?,?,?,'base',0,1,1)`, id, subject, inputCommit)
}

// submitVersion records one Worker result as the submitted version of the
// task and assigns a live Verifier run to it. It returns the version id.
func submitVersion(t *testing.T, db *sql.DB, task int64, worker, verifier, commit string) int64 {
	t.Helper()
	mkEndedRun(t, db, worker, "worker", task, "base")
	mustExec(t, db, `INSERT INTO version_submissions(run_id,task_id,input_commit,result_commit) VALUES(?,?,'base',?)`, worker, task, commit)
	mustExec(t, db, `INSERT INTO task_versions(task_id,run_id,input_commit,result_commit,destination_branch,retained_ref)
		VALUES(?,?,'base',?,'main',?)`, task, worker, commit, "refs/test/"+worker)
	version := oneInt(t, db, `SELECT id FROM task_versions WHERE run_id=?`, worker)
	captureVersion(t, db, worker, version)
	mustExec(t, db, `UPDATE tasks SET submitted_version=?,input_commit='base' WHERE id=?`, version, task)
	mkRun(t, db, verifier, "verifier", task)
	mustExec(t, db, `INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,version_id,temporary_bytes)
		VALUES(?,?,?,'base',?,0)`, verifier, task, commit, version)
	return version
}

// seedVerifiedVersion records a submitted Worker version and its Verifier pass
// for the task. The packaged stage requires this state.
func seedVerifiedVersion(t *testing.T, db *sql.DB, task int64) {
	t.Helper()
	round := oneInt(t, db, `SELECT COUNT(*) FROM runs WHERE subject=?`, task)
	worker := fmt.Sprintf("delivery-worker-%d-%d", task, round)
	verifier := fmt.Sprintf("delivery-verifier-%d-%d", task, round)
	for role, run := range map[string]string{"worker": worker, "verifier": verifier} {
		mkEndedRun(t, db, run, role, task, "source")
	}
	mustExec(t, db, `INSERT INTO version_submissions(run_id,task_id,input_commit,result_commit) VALUES(?,?,'source','source')`, worker, task)
	mustExec(t, db, `INSERT INTO task_versions(task_id,run_id,input_commit,result_commit,destination_branch,retained_ref)
        VALUES(?,?,'source','source','main',?)`, task, worker, "refs/test/"+worker)
	version := oneInt(t, db, `SELECT id FROM task_versions WHERE run_id=?`, worker)
	captureVersion(t, db, worker, version)
	mustExec(t, db, `UPDATE repository_runs SET version_id=? WHERE run_id=?`, version, verifier)
	mustExec(t, db, `INSERT INTO version_verdicts(version_id,run_id,outcome,evidence_path) VALUES(?,?,'pass','evidence.md')`, version, verifier)
	mustExec(t, db, `UPDATE tasks SET submitted_version=?,source_version=? WHERE id=?`, version, version, task)
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

// approveReviewed publishes the review of a packaged task and approves it.
// The task then waits unarchived for its merge.
func approveReviewed(t *testing.T, db *sql.DB, task int64) {
	t.Helper()
	seedMergedCandidate(t, db, task)
	mustExec(t, db, `UPDATE tasks SET report_path='report.md',report_sha256='hash',thesis='The result satisfies the task.' WHERE id=?`, task)
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.PublishReview(ctx, q, task)
	})
	packet := oneInt(t, db, `SELECT id FROM review_versions WHERE task_id=? ORDER BY id DESC LIMIT 1`, task)
	version := deliveryInt(t, db, task, "submitted_version")
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.ApproveReview(ctx, q, task, packet, version)
	})
}

func publishReviewFixture(ctx context.Context, q domain.Q, task int64, render, thesis string) error {
	if _, err := q.ExecContext(ctx, `INSERT INTO review_versions(task_id,version_id,candidate_id,render_path,report_sha256,thesis,criteria,requirements_version)
 SELECT id,submitted_version,candidate_id,?,'hash',?,COALESCE(description,''),requirements_version FROM tasks
 WHERE id=? AND status='packaged' AND decision IS NULL AND archived=0 ON CONFLICT(task_id,version_id) DO NOTHING`, render, thesis, task); err != nil {
		return err
	}
	var review int64
	err := q.QueryRowContext(ctx, `SELECT p.id FROM review_versions p JOIN tasks t ON t.id=p.task_id WHERE t.id=? AND p.version_id=t.submitted_version`, task).Scan(&review)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	return domain.SelectReview(ctx, q, task, review)
}
