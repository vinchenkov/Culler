package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"mc/dispatch"
	"mc/domain"
	"mc/refusal"
)

// fhClaim claims the lease of the Project of the task for one run without a repository.
func fhClaim(t *testing.T, db *sql.DB, task int64, run, role string) *RunIdentity {
	t.Helper()
	var project string
	if err := db.QueryRow(`SELECT project FROM tasks WHERE id = ?`, task).Scan(&project); err != nil {
		t.Fatal(err)
	}
	now := dvNow(t, db)
	if err := inTx(db, func(ctx context.Context, q Q) error {
		_, err := domain.Claim(ctx, q, now, domain.ClaimArgs{RunID: run, Project: project, Owner: baseRole(role), SubjectID: &task, HardDeadlineMinutes: 240})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `UPDATE runs SET agent_started_at=datetime('now') WHERE id=?`, run)
	return &RunIdentity{RunID: run, Tier: "pipeline", Role: role}
}

func fhReap(t *testing.T, db *sql.DB, run string) {
	t.Helper()
	if err := inTx(db, func(ctx context.Context, q Q) error {
		_, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: run, Reason: "container-gone"})
		if err != nil {
			return err
		}
		return domain.AcknowledgeRunStop(ctx, q, run)
	}); err != nil {
		t.Fatal(err)
	}
}

func fhReport(t *testing.T, db *sql.DB, id *RunIdentity, report domain.InfrastructureFailureReport) map[string]any {
	t.Helper()
	raw, err := RunInfrastructureFailure(db, id, id.RunID, report)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("report result = %T", raw)
	}
	return result
}

// fhFailedRun starts a run in the role, records one infrastructure failure, and cleans the run.
func fhFailedRun(t *testing.T, f *deliveryFixture, task int64, role string, report domain.InfrastructureFailureReport) {
	t.Helper()
	run, root, id := f.run(task, role)
	fhReport(t, f.db, id, report)
	fhReap(t, f.db, run)
	f.cleanup(task, run, root)
}

func TestFailureHandlingReportAndTaskReadsPreserveFacts(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Report the infrastructure failure")
	run, _, id := f.run(task, "worker")
	report := domain.InfrastructureFailureReport{
		Condition: "The service refused the request.", Source: "agent", Cause: "credentials",
		Evidence: "The service response states that the credential expired.", OperatorAction: "Renew the credential.",
	}
	first := fhReport(t, f.db, id, report)
	if first["charged"] != true || first["remaining"] != 2 || first["stalled"] != false {
		t.Fatalf("first report = %v", first)
	}
	second := fhReport(t, f.db, id, report)
	if second["charged"] != false || second["remaining"] != 2 {
		t.Fatalf("repeat report = %v", second)
	}
	if got := dfStr(t, f.db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
		t.Fatalf("report released the lease: %s", got)
	}
	fhReap(t, f.db, run)
	if got := dfStr(t, f.db, `SELECT outcome FROM runs WHERE id = ?`, run); got != "infra-failed" {
		t.Fatalf("outcome = %s", got)
	}
	if n := dfInt(t, f.db, `SELECT count(*) FROM infrastructure_failures`); n != 1 {
		t.Fatalf("records = %d", n)
	}
	rawTask, err := TaskGet(f.db, task)
	if err != nil {
		t.Fatal(err)
	}
	row := rawTask.(map[string]any)
	if row["infrastructure_failure_count"] != int64(1) || row["stalled"] != int64(0) || row["status"] != "seeded" {
		t.Fatalf("task = %v", row)
	}
	latest, ok := row["latest_infrastructure_failure"].(map[string]any)
	if !ok {
		t.Fatalf("failure report = %T", row["latest_infrastructure_failure"])
	}
	if latest["source"] != "agent" || latest["cause"] != "unknown" || latest["evidence"] != report.Evidence || latest["operator_action"] != "" {
		t.Fatalf("cause record = %v", latest)
	}
	listed, err := TaskList(f.db, false, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	tasks := listed.(map[string]any)["tasks"].([]map[string]any)
	if len(tasks) != 1 || !reflect.DeepEqual(tasks[0]["latest_infrastructure_failure"], latest) {
		t.Fatalf("listed tasks = %v", tasks)
	}
}

func TestFailureHandlingReportRejectsForeignRunAndUnsupportedCause(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Refuse the invalid report")
	run, _, id := f.run(task, "worker")
	report := domain.InfrastructureFailureReport{Condition: "runner-exit", Source: "runner"}
	if _, err := RunInfrastructureFailure(f.db, id, "unrelated", report); err == nil {
		t.Fatal("the agent reported a different run")
	}
	report.Source = "resident"
	if _, err := RunInfrastructureFailure(f.db, id, run, report); err == nil {
		t.Fatal("the agent claimed the resident source")
	}
	report.Source = "agent"
	report.Cause = "quota"
	if _, err := RunInfrastructureFailure(f.db, id, run, report); err == nil {
		t.Fatal("quota diagnosis without evidence passed")
	}
	if n := dfInt(t, f.db, `SELECT infrastructure_failure_count FROM tasks WHERE id = ?`, task); n != 0 {
		t.Fatalf("refused reports charged %d failures", n)
	}
	report.Cause = "unknown"
	report.Evidence = "The service returned HTTP 429 without a quota cause."
	fhReport(t, f.db, id, report)
	if got := dfStr(t, f.db, `SELECT cause FROM infrastructure_failures WHERE run_id = ?`, run); got != "unknown" {
		t.Fatalf("rate limit became %s", got)
	}
}

func TestFailureHandlingBlockedReportEndsRunWithoutCharge(t *testing.T) {
	f, task, run, _, id := spentBudget(t)
	reason := "The operator must select the permitted release scope."
	if _, err := TaskBlock(f.db, id, task, reason); err != nil {
		t.Fatal(err)
	}
	if got := dfStr(t, f.db, `SELECT outcome FROM runs WHERE id = ?`, run); got != "blocked" {
		t.Fatalf("outcome = %s", got)
	}
	if got := dfStr(t, f.db, `SELECT run_id FROM leases WHERE project = 'ws-test'`); got != run {
		t.Fatalf("lease = %s", got)
	}
	fhReap(t, f.db, run)
	if n := dfInt(t, f.db, `SELECT infrastructure_failure_count FROM tasks WHERE id = ?`, task); n != 2 {
		t.Fatalf("blocked report changed the count: %d", n)
	}
	if got := dfStr(t, f.db, `SELECT blocked_reason FROM tasks WHERE id = ?`, task); got != reason {
		t.Fatalf("reason = %s", got)
	}
	if n := dfInt(t, f.db, `SELECT count(*) FROM infrastructure_failures`); n != 0 {
		t.Fatalf("blocked report recorded %d infrastructure failures", n)
	}
	_, err := TaskUnblock(f.db, nil, task, "The operator resolved the condition.")
	requireBoardCode(t, err, domain.CodeIllegalTransition)
	if state := boardRevision(t, f.db, task, "The release scope is the current minor version.")["revision_state"]; state != "applied" {
		t.Fatalf("the revision is %v", state)
	}
	if got := dfStr(t, f.db, `SELECT blocked||'/'||infrastructure_failure_count FROM tasks WHERE id = ?`, task); got != "0/2" {
		t.Fatalf("the resolution of the report left %s", got)
	}
}

func TestFailureHandlingExplicitResumeRestoresStalledAllowance(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("blocked=%v", blocked), func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(7, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
			dvExec(t, db, `UPDATE tasks SET dispatch_retries = 0 WHERE id = 7`)
			if blocked {
				if _, err := TaskBlock(db, nil, 7, "The operator must confirm the scope."); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := TaskResume(db, nil, 7, "The operator restored task execution."); err != nil {
				t.Fatal(err)
			}
			if n := dfInt(t, db, `SELECT infrastructure_failure_count + stalled FROM tasks WHERE id = 7`); n != 0 {
				t.Fatalf("Task resume retained its exhausted allowance: %d", n)
			}
			if got := dfInt(t, db, `SELECT blocked FROM tasks WHERE id=7`); (got == 1) != blocked {
				t.Fatal("Task resume changed the independent operator decision")
			}
		})
	}
}

func TestFailureHandlingInfraTerminalUsesCommonReport(t *testing.T) {
	f, task, run, _, id := spentBudget(t)
	raw, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Infra: true, Reason: "The execution stopped before its result."})
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(map[string]any)
	if result["stalled"] != true || result["remaining"] != 0 {
		t.Fatalf("infra report = %v", result)
	}
	if n := dfInt(t, f.db, `SELECT blocked FROM tasks WHERE id = ?`, task); n != 0 {
		t.Fatal("the infrastructure terminal created an operator block")
	}
	if got := dfStr(t, f.db, `SELECT source || ':' || cause FROM infrastructure_failures WHERE run_id = ?`, run); got != "agent:unknown" {
		t.Fatalf("record = %s", got)
	}
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Infra: true, Reason: "The container then exited."}); err != nil {
		t.Fatal(err)
	}
	fhReap(t, f.db, run)
	if n := dfInt(t, f.db, `SELECT count(*) FROM infrastructure_failures`); n != 1 {
		t.Fatalf("duplicate records = %d", n)
	}
}

func TestFailureHandlingFinalReportPreventsAnotherTerminal(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Keep the final outcome")
	run, root, id := f.run(task, "worker")
	head := f.commit(root, "work.txt", "The complete work.\n")
	fhReport(t, f.db, id, domain.InfrastructureFailureReport{Condition: "The execution cannot continue.", Source: "agent"})
	if _, err := TaskBlock(f.db, id, task, "The operator must decide the scope."); err == nil {
		t.Fatal("a block replaced the final outcome")
	}
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: head}); err == nil {
		t.Fatal("a success replaced the final outcome")
	}
	if got := dfStr(t, f.db, `SELECT outcome FROM runs WHERE id = ?`, run); got != "infra-failed" {
		t.Fatalf("outcome = %s", got)
	}
	if got := dfStr(t, f.db, `SELECT status FROM tasks WHERE id = ?`, task); got != "seeded" {
		t.Fatalf("stage = %s", got)
	}
	if n := dfInt(t, f.db, `SELECT infrastructure_failure_count FROM tasks WHERE id = ?`, task); n != 1 {
		t.Fatalf("failure count = %d", n)
	}
}

func TestFailureHandlingAttentionStopsOnlyItsProject(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "second", "active")
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	for _, id := range []int64{7, 8, 9} {
		task := dvTask(id, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
		task.Project = "second"
		dvInsertTask(t, db, task)
	}
	dvExec(t, db, `UPDATE tasks SET dispatch_retries=0 WHERE id IN(7,8,9)`)
	if _, err := TaskBlock(db, nil, 7, "The operator must confirm the input."); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"ws-test", "second"} {
		rec, err := loadVisionRecords(context.Background(), db, project)
		if err != nil {
			t.Fatal(err)
		}
		action := dispatch.Decide(rec, dispatch.Lease{}, dvConfig(), dispatch.Clock{Now: dvNow(t, db)})
		if project == "second" {
			if !reflect.DeepEqual(rec.AttentionTaskIDs, []int64{7, 8, 9}) || action.Idle != dispatch.IdleAttentionRequired {
				t.Fatalf("The affected Project state is invalid: %+v, %+v", rec.AttentionTaskIDs, action)
			}
		} else if len(rec.AttentionTaskIDs) != 0 || action.Spawn == nil {
			t.Fatalf("The unrelated Project stopped: %+v, %+v", rec.AttentionTaskIDs, action)
		}
	}
	if _, err := TaskResume(db, nil, 9, "The operator restored task execution."); err != nil {
		t.Fatal(err)
	}
	rec, err := loadVisionRecords(context.Background(), db, "second")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.AttentionTaskIDs, []int64{7, 8}) {
		t.Fatalf("The remaining attention tasks are %v.", rec.AttentionTaskIDs)
	}
	if action := dispatch.Decide(rec, dispatch.Lease{}, dvConfig(), dispatch.Clock{Now: dvNow(t, db)}); action.Idle == dispatch.IdleAttentionRequired {
		t.Fatal("The Project stopped below the attention threshold.")
	}
}

func TestFailureHandlingAttentionInvalidatesOnlyItsPreparedProject(t *testing.T) {
	for _, project := range []string{"ws-test", "second"} {
		t.Run(project, func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
			dvAddProject(t, db, "second", "active")
			for _, id := range []int64{7, 8, 9} {
				task := dvTask(id, dispatch.ScopeTask, dispatch.StatusSeeded, 2)
				task.Project = project
				dvInsertTask(t, db, task)
			}
			dvExec(t, db, `UPDATE tasks SET dispatch_retries=0 WHERE id IN(7,8)`)
			prepared := dfPrepare(t, db, dfRequestID)
			if prepared.candidate == nil {
				t.Fatalf("The preparation has no candidate: %+v", prepared)
			}
			attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
			if err != nil {
				t.Fatal(err)
			}
			dvExec(t, db, `UPDATE tasks SET dispatch_retries=0 WHERE id=9`)
			effect := dfCommit(t, db, prepared, attested)
			if project == "second" {
				if effect["action"] != "spawn" {
					t.Fatalf("Unrelated attention invalidated dispatch: %v", effect)
				}
				return
			}
			dfAssertInert(t, db, effect)
			if effect["code"] != refusal.CodeStale || effect["consequence"] != "none" {
				t.Fatalf("The stale effect is %v.", effect)
			}
			next := dfPrepare(t, db, "0011223344556600")
			if next.final == nil || next.final["action"] != "idle" || next.final["reason"] != "attention-required" {
				t.Fatalf("The next preparation is %+v.", next)
			}
		})
	}
}

// Each forward stage move restores the allowance. An operator revision moves the task back and keeps the count.
func TestFailureHandlingRealRolesResetOnlyAtForwardStage(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Restore the allowance at each forward stage")
	report := domain.InfrastructureFailureReport{Condition: "The runner stopped before its result.", Source: "runner"}
	count := func() int64 {
		t.Helper()
		return dfInt(t, f.db, `SELECT infrastructure_failure_count FROM tasks WHERE id = ?`, task)
	}
	fhFailedRun(t, f, task, "worker", report)
	fhFailedRun(t, f, task, "worker", report)
	if n := count(); n != 2 {
		t.Fatalf("Worker failure count = %d", n)
	}
	run, root, id := f.run(task, "worker")
	head := f.commit(root, "work.txt", "The complete work.\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: head}); err != nil {
		t.Fatal(err)
	}
	f.capture(task, run, root)
	if n := count(); n != 0 {
		t.Fatalf("Worker progress count = %d", n)
	}
	fhFailedRun(t, f, task, "verifier", report)
	fhFailedRun(t, f, task, "verifier", report)
	if n := count(); n != 2 {
		t.Fatalf("Verifier failure count = %d", n)
	}
	run, root, id = f.run(task, "verifier")
	evidence := filepath.Join(f.artifacts, "test", run+".html")
	writeFile(t, evidence, "<html><body>The required checks pass.</body></html>")
	if _, err := VerifierVerdict(f.db, id, VerdictArgs{Task: task, Run: run, Outcome: "pass", Evidence: evidence, SHA: head, StoreWorktree: filepath.Join(root, "repo")}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	if n := count(); n != 0 {
		t.Fatalf("Verifier progress count = %d", n)
	}
	run, root, id = f.run(task, "packager")
	packet := filepath.Join(f.artifacts, "test", run+".html")
	writeFile(t, packet, "<html><body>The task adds work.txt. The required checks pass.</body></html>")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: packet, Thesis: "The task adds the required work file."}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	if n := count(); n != 0 {
		t.Fatalf("Packager progress count = %d", n)
	}
	f.merge(task, "prepare")
	dvExec(t, f.db, `UPDATE tasks SET dispatch_retries=2 WHERE id=?`, task)
	var packetID, version int64
	if err := f.db.QueryRow(`SELECT p.id,p.version_id FROM review_versions p JOIN tasks d ON d.submitted_version=p.version_id WHERE p.task_id=?`, task).Scan(&packetID, &version); err != nil {
		t.Fatal(err)
	}
	if _, err := PacketDecide(f.db, nil, task, "revise", "Add evidence for the requested edge case.", packetID, version); err != nil {
		t.Fatal(err)
	}
	if got := dfStr(t, f.db, `SELECT status FROM tasks WHERE id = ?`, task); got != "seeded" {
		t.Fatalf("the revision left the task %s", got)
	}
	if n := count(); n != 1 {
		t.Fatalf("The revision reset the infrastructure failure count: %d", n)
	}
	if got := dfStr(t, f.db, `SELECT input_commit FROM tasks WHERE id = ?`, task); got != head {
		t.Fatalf("The revision lost the saved commit: %s", got)
	}
	if dfInt(t, f.db, `SELECT completion_sha IS NULL FROM tasks WHERE id=?`, task) != 1 {
		t.Fatal("The old completion remains current after the revision.")
	}
	if n := dfInt(t, f.db, `SELECT count(*) FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ? AND t.archived = 0`, task); n != 1 {
		t.Fatal("The revision lost the previous packet.")
	}
}

func TestFailureHandlingActiveRunCanFinishDuringBoardPause(t *testing.T) {
	f, task, run, root, id := spentBudget(t)
	head := f.commit(root, "work.txt", "The complete work.\n")
	for i := 0; i < 3; i++ {
		other := f.add(fmt.Sprintf("Decide the scope of task %d", i))
		if _, err := TaskBlock(f.db, nil, other, "The operator must select the task scope."); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := loadVisionRecords(context.Background(), f.db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := loadLease(context.Background(), f.db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	action := dispatch.Decide(rec, lease, dvConfig(), dvClock(t, f.db))
	if action.Kind != dispatch.KindIdle || action.Idle != dispatch.IdleLeaseHeld {
		t.Fatalf("active run action = %+v", action)
	}
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: head}); err != nil {
		t.Fatal(err)
	}
	f.capture(task, run, root)
	if n := dfInt(t, f.db, `SELECT infrastructure_failure_count FROM tasks WHERE id = ?`, task); n != 0 {
		t.Fatalf("active progress retained %d failures", n)
	}
	rec, err = loadVisionRecords(context.Background(), f.db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	action = dispatch.Decide(rec, dispatch.Lease{}, dvConfig(), dispatch.Clock{Now: dvNow(t, f.db)})
	if action.Kind != dispatch.KindIdle || action.Idle != dispatch.IdleAttentionRequired {
		t.Fatalf("board pause after success = %+v", action)
	}
}
