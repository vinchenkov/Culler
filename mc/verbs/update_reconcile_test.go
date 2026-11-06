package verbs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

func TestUpdateReconcilePreservesAllowancePausesAndRunFiles(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
	dvInsertTask(t, db, task)
	dvExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,session_path,native_session_ref,trace_filename) VALUES('update-run','pipeline','worker','ws-test',1,'sessions/update-run','native-history','native.jsonl')`)
	id := int64(1)
	dvHold(t, db, "ws-test", "update-run", "worker", &id, 0, nil, time.Hour)
	dvExec(t, db, `UPDATE projects SET status='paused' WHERE id='ws-test'`)
	path := filepath.Join(os.Getenv("MC_HOME"), "sessions", "update-run", "native.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "retained native history\n")
	before := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`)
	result, err := reconcileUpdateDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runs != 1 {
		t.Fatalf("result %+v", result)
	}
	if dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`) != before {
		t.Fatal("update charged the task allowance")
	}
	if dfInt(t, db, `SELECT run_id IS NULL FROM leases WHERE project='ws-test'`) != 1 {
		t.Fatal("update retained a stopped lease")
	}
	if dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`) != "paused" {
		t.Fatal("update changed the Project pause")
	}
	if dfStr(t, db, `SELECT outcome FROM runs WHERE id='update-run'`) != "interrupted" {
		t.Fatal("run has no interruption record")
	}
	if dfStr(t, db, `SELECT native_session_ref FROM runs WHERE id='update-run'`) != "native-history" {
		t.Fatal("native locator changed")
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "retained native history\n" {
		t.Fatalf("history %q %v", body, err)
	}
	again, err := reconcileUpdateDB(db)
	if err != nil || again.Runs != 0 {
		t.Fatalf("repeat %+v %v", again, err)
	}
}

func TestUpdateReconcileKeepsNativeHomieHistory(t *testing.T) {
	db := hbSpine(t)
	hbBound(t, db)
	dvExec(t, db, `UPDATE homie_sessions SET native_session_ref='saved-native',trace_filename='native.jsonl' WHERE id='sess-1'`)
	result, err := reconcileUpdateDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sessions != 1 || hbStatus(t, db) != "ended" {
		t.Fatalf("session result %+v", result)
	}
	if dfStr(t, db, `SELECT native_session_ref FROM homie_sessions WHERE id='sess-1'`) != "saved-native" {
		t.Fatal("native history locator changed")
	}
}

func TestUpdateReconcileEndsSetupWithoutRemovingItsHistory(t *testing.T) {
	db := environmentFixture(t)
	operations := environmentWork(t, db)
	if len(operations) != 1 {
		t.Fatalf("operations %+v", operations)
	}
	op := operations[0]
	environmentCall(t, db, nil, "heartbeat", EnvironmentRequest{OperationID: op.ID})
	result, err := reconcileUpdateDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if result.Environments != 1 {
		t.Fatalf("result %+v", result)
	}
	if dfStr(t, db, `SELECT status FROM environment_operations WHERE id=?`, op.ID) != "failed" {
		t.Fatal("interrupted setup remained active")
	}
	if dfStr(t, db, `SELECT phase FROM project_environments WHERE project_id='ws-test'`) != "failed" {
		t.Fatal("setup has no repair state")
	}
	if dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`) != "paused" {
		t.Fatal("setup changed the Project pause")
	}
}

func TestUpdateReconcileRecoversAnAppliedReceiptOnce(t *testing.T) {
	f, task, run, application := pendingDispatchApplication(t)
	applied := f.execute(RepositoryEnvelope{Operation: "apply", TaskID: task, RunID: run, WorkspaceRoot: f.workspace, DestinationBranch: "main", Application: &application})
	if applied.Applied == nil {
		t.Fatalf("apply %+v", applied)
	}
	clocks := fixClocks(t, f.db)
	allowance := dfInt(t, f.db, `SELECT dispatch_retries FROM tasks WHERE id=?`, task)
	active := dfInt(t, f.db, `SELECT COUNT(*) FROM repository_executions WHERE ended_at IS NULL`)
	result, err := reconcileUpdateDB(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if int64(result.Executions) != active {
		t.Fatalf("result %+v", result)
	}
	if dfInt(t, f.db, `SELECT dispatch_retries FROM tasks WHERE id=?`, task) != allowance {
		t.Fatal("update charged receipt recovery")
	}
	if dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, run) != 0 {
		t.Fatal("update discarded the apply receipt")
	}
	if pending, err := updateReceiptRecovery(context.Background(), f.db, "ws-test"); err != nil || !pending {
		t.Fatalf("update receipt gate %v %v", pending, err)
	}
	rec, err := loadRecords(context.Background(), f.db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	if !rec.UpdateRecovery {
		t.Fatal("Project candidates omit receipt recovery")
	}
	if action := dispatch.Decide(rec, dispatch.Lease{}, dispatch.DefaultConfig(), dispatch.Clock{Now: time.Now()}); action.Idle != dispatch.IdleRepositoryOperation {
		t.Fatalf("Project work bypassed receipt recovery: %+v", action)
	}
	if action := dispatch.DecideWiki(dispatch.WikiRecords{Project: "ws-test", Status: "active", UpdateRecovery: true}, dispatch.DefaultConfig(), time.Hour, dispatch.Clock{Now: time.Now()}); action.Idle != dispatch.IdleRepositoryOperation {
		t.Fatalf("Scribe bypassed receipt recovery: %+v", action)
	}
	if pending, err := updateReceiptRecovery(context.Background(), f.db, "another-project"); err != nil || pending {
		t.Fatalf("other Project gate %v %v", pending, err)
	}
	f.started = nil
	clocks.advance(domain.RetryDelay + time.Second)
	recovery := f.nextWork()
	if recovery["operation"] != "reconcile" || recovery["previous_run"] != run {
		t.Fatalf("recovery %#v", recovery)
	}
	recovered := f.repository(recovery)
	if recovered.Applied == nil || !recovered.Applied.Recovered || recovered.Applied.Commit != application.Candidate.Commit {
		t.Fatalf("receipt %+v", recovered)
	}
	f.record("reconcile", recovery["run_id"].(string), recovered)
	if pending, err := updateReceiptRecovery(context.Background(), f.db, "ws-test"); err != nil || pending {
		t.Fatalf("resolved receipt retained the gate: %v %v", pending, err)
	}
	if dfInt(t, f.db, `SELECT count(*) FROM merge_operations WHERE task_id=? AND operation='apply' AND finished=1`, task) != 1 {
		t.Fatal("update repeated the completed effect")
	}
}
