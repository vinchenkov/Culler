package verbs

import (
	"testing"
	"time"

	"mc/domain"
	"mc/repository"
)

func pendingDispatchApplication(t *testing.T) (*dispatchLifecycle, int64, string, repository.Application) {
	t.Helper()
	f := newDispatchLifecycle(t)
	task := f.add("Retain the authorized result")
	candidate := f.candidate(task, func(run lifecycleRun) {
		f.submit(run, f.commit(run.root, "receipt.txt", "The authorization identifies this contribution.\n"))
	})
	if candidate.Status != "merged" {
		t.Fatalf("the candidate failed: %+v", candidate)
	}
	f.approve(task)
	effect := f.nextWork()
	if effect["action"] != "repository" || effect["operation"] != "apply" || effect["task_id"] != task {
		t.Fatalf("the start request omitted the authorized merge: %#v", effect)
	}
	return f, task, effect["run_id"].(string), effect["application"].(repository.Application)
}

// reportLostApplication reports the failure of the merge execution, as the
// resident does after the result connection closed. It returns the run outcome.
func (f *dispatchLifecycle) reportLostApplication(run string) string {
	f.t.Helper()
	if _, err := DeliveryFail(f.db, nil, run, "The result connection closed before acknowledgment.", false); err != nil {
		f.t.Fatal(err)
	}
	return dfStr(f.t, f.db, `SELECT outcome FROM runs WHERE id=?`, run)
}

// executionActive reports whether the execution of the run is active.
func (f *dispatchLifecycle) executionActive(run string) bool {
	f.t.Helper()
	return dfInt(f.t, f.db, `SELECT ended_at IS NULL FROM repository_executions WHERE run_id=?`, run) == 1
}

func TestDispatchReceiptRefusesLateApplicationAndRecoversItOnce(t *testing.T) {
	f, task, run, application := pendingDispatchApplication(t)
	result := f.execute(RepositoryEnvelope{
		Operation: "apply", TaskID: task, RunID: run, WorkspaceRoot: f.workspace,
		DestinationBranch: "main", Application: &application,
	})
	if result.Applied == nil {
		t.Fatalf("the actual merge failed: %+v", result)
	}
	clocks := fixClocks(t, f.db)
	outcome := f.reportLostApplication(run)
	independent := f.expeditedTask()
	if _, err := RepositoryRecord(f.db, nil, "apply", run, result); err == nil {
		t.Fatal("the late receipt completed the merge after its execution ended")
	}
	if dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, run) != 0 || dfInt(t, f.db, `SELECT archived FROM tasks WHERE id=?`, task) != 0 {
		t.Fatal("the refused receipt changed the merge authorization or the task")
	}
	if got := dfStr(t, f.db, `SELECT outcome FROM runs WHERE id=?`, run); got != outcome {
		t.Fatalf("the late receipt changed the run outcome: %q", got)
	}
	clocks.advance(domain.RetryDelay + time.Second)
	recovery := f.nextWork()
	if recovery["action"] != "repository" || recovery["operation"] != "reconcile" || recovery["previous_run"] != run {
		t.Fatalf("the start request omitted receipt recovery: %#v", recovery)
	}
	recovered := f.repository(recovery)
	if recovered.Applied == nil || !recovered.Applied.Recovered || recovered.Applied.Commit != application.Candidate.Commit {
		t.Fatalf("receipt recovery lost the branch receipt: %+v", recovered)
	}
	if dfInt(t, f.db, `SELECT archived FROM tasks WHERE id=?`, task) != 1 {
		t.Fatal("receipt recovery left the accepted task active")
	}
	f.record("reconcile", recovery["run_id"].(string), recovered)
	if dfInt(t, f.db, `SELECT count(*) FROM merge_operations WHERE task_id=? AND operation='apply' AND finished=1`, task) != 1 {
		t.Fatal("the merge completed more than once")
	}
	f.requireWorker(independent)
}

func TestDispatchReceiptRefusesDifferentApplicationFacts(t *testing.T) {
	f, task, run, application := pendingDispatchApplication(t)
	for _, wrong := range []repository.Applied{
		{Authorization: "another-authorization", Commit: application.Candidate.Commit},
		{Authorization: application.Authorization, Commit: application.Candidate.DestinationCommit},
	} {
		if _, err := RepositoryRecord(f.db, nil, "apply", run, RepositoryResult{TaskID: task, Applied: &wrong}); err == nil {
			t.Fatalf("the merge result accepted different facts: %+v", wrong)
		}
		if dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, run) != 0 {
			t.Fatal("the wrong receipt completed the authorization")
		}
		if dfInt(t, f.db, `SELECT archived FROM tasks WHERE id=?`, task) != 0 {
			t.Fatal("the wrong receipt accepted the task")
		}
		if !f.executionActive(run) {
			t.Fatal("the wrong receipt ended the merge execution")
		}
	}
}

func TestDispatchReceiptRefusesCancellationOfAnAuthorizedApplication(t *testing.T) {
	f, task, run, _ := pendingDispatchApplication(t)
	if _, err := TaskCancel(f.db, nil, task, "The operator withdrew the contribution."); err == nil {
		t.Fatal("cancellation discarded an unresolved merge authorization")
	}
	if !f.executionActive(run) {
		t.Fatal("cancellation ended the active merge")
	}
	if dfInt(t, f.db, `SELECT archived FROM tasks WHERE id=?`, task) != 0 {
		t.Fatal("cancellation archived the authorized task")
	}
}

func TestDispatchReceiptReconcilesAnUnappliedAuthorizationDuringPause(t *testing.T) {
	f, task, run, application := pendingDispatchApplication(t)
	clocks := fixClocks(t, f.db)
	f.reportLostApplication(run)
	if _, err := SetEveryProjectPause(f.db, nil, true); err != nil {
		t.Fatal(err)
	}
	clocks.advance(domain.RetryDelay + time.Second)
	effect := f.nextWork()
	if effect["action"] != "repository" || effect["operation"] != "reconcile" || effect["previous_run"] != run {
		t.Fatalf("the pause hid the unresolved authorization: %#v", effect)
	}
	result := f.repository(effect)
	if result.Status != "not-applied" || result.Applied != nil {
		t.Fatalf("receipt recovery changed the merge result: %+v", result)
	}
	if dfInt(t, f.db, `SELECT run_id IS NULL FROM leases WHERE project='ws-test'`) != 1 {
		t.Fatal("receipt recovery claimed the Project lease")
	}
	if dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, run) != 1 {
		t.Fatal("receipt recovery retained a resolved authorization")
	}
	if got := srcGit(t, f.workspace, "rev-parse", "main"); got != application.Candidate.DestinationCommit {
		t.Fatal("receipt recovery advanced main during the pause")
	}
	if effect := f.nextWork(); effect["action"] != "idle" || effect["reason"] != "project-inactive" {
		t.Fatalf("the pause permitted another merge: %#v", effect)
	}
	if _, err := SetEveryProjectPause(f.db, nil, false); err != nil {
		t.Fatal(err)
	}
	if result := f.apply(task); result.Applied == nil || result.Applied.Commit != application.Candidate.Commit {
		t.Fatalf("the approved task failed after resume: %+v", result)
	}
}
