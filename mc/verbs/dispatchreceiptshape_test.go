package verbs

import (
	"context"
	"os"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/refusal"
	"mc/repository"
)

func TestDispatchReceiptRejectsContradictoryApplicationResults(t *testing.T) {
	f, task, run, application := pendingDispatchApplication(t)
	applied := repository.Applied{Authorization: application.Authorization, Commit: application.Candidate.Commit}
	before := dfStr(t, f.db, `SELECT json_object('finished',m.finished,'result',m.result,'decision',t.decision,
 'archived',t.archived,'blocked',t.blocked,'candidate',t.candidate_id,'outcome',r.outcome,'ended_at',r.ended_at,
 'lease',(SELECT run_id FROM leases WHERE project=t.project)) FROM merge_operations m
 JOIN tasks t ON t.id=m.task_id JOIN runs r ON r.id=m.run_id WHERE m.run_id=?`, run)
	for _, test := range []struct {
		name   string
		result RepositoryResult
	}{
		{"receipt and changed destination", RepositoryResult{TaskID: task, Applied: &applied, Status: "destination-changed", DestinationCommit: application.Candidate.DestinationCommit}},
		{"receipt and operator condition", RepositoryResult{TaskID: task, Applied: &applied, Status: "operator-attention", OperatorAction: "The checkout requires repair."}},
		{"receipt and unknown status", RepositoryResult{TaskID: task, Applied: &applied, Status: "unexpected"}},
		{"receipt and candidate", RepositoryResult{TaskID: task, Applied: &applied, Candidate: &application.Candidate}},
		{"unknown refusal", RepositoryResult{TaskID: task, Status: "unexpected"}},
		{"reconciliation result during apply", RepositoryResult{TaskID: task, Status: "not-applied"}},
		{"empty result", RepositoryResult{TaskID: task}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := RepositoryRecord(f.db, nil, "apply", run, test.result); err == nil {
				t.Fatal("The application accepted incompatible result facts.")
			}
			after := dfStr(t, f.db, `SELECT json_object('finished',m.finished,'result',m.result,'decision',t.decision,
 'archived',t.archived,'blocked',t.blocked,'candidate',t.candidate_id,'outcome',r.outcome,'ended_at',r.ended_at,
 'lease',(SELECT run_id FROM leases WHERE project=t.project)) FROM merge_operations m
 JOIN tasks t ON t.id=m.task_id JOIN runs r ON r.id=m.run_id WHERE m.run_id=?`, run)
			if after != before {
				t.Fatal("The rejected result changed its authorization, task, or run.")
			}
		})
	}
	result := f.execute(RepositoryEnvelope{Operation: "apply", TaskID: task, RunID: run, WorkspaceRoot: f.workspace, DestinationBranch: "culler", Application: &application})
	f.record("apply", run, result)
	if dfInt(t, f.db, `SELECT archived FROM tasks WHERE id=?`, task) != 1 {
		t.Fatal("The exact receipt did not complete the task.")
	}
}

func TestDispatchReceiptOperatorConditionRetainsTheAuthorization(t *testing.T) {
	f, task, run, application := pendingDispatchApplication(t)
	independent := f.expeditedTask()
	f.record("apply", run, RepositoryResult{TaskID: task, Status: "operator-attention", OperatorAction: "The prior application journal requires checkout repair."})
	if dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, run) != 0 || dfInt(t, f.db, `SELECT blocked FROM tasks WHERE id=?`, task) != 1 {
		t.Fatal("The operator condition discarded the unresolved authorization.")
	}
	if effect := f.nextWork(); effect["action"] != "idle" || effect["reason"] != "attention-required" {
		t.Fatalf("The unresolved application permitted other work: %#v", effect)
	}
	if dfStr(t, f.db, `SELECT status FROM tasks WHERE id=?`, independent) != "seeded" {
		t.Fatal("The unrelated task started during the operator hold.")
	}
	if _, err := TaskCancel(f.db, nil, task, "The operator withdrew the task."); err == nil {
		t.Fatal("Cancellation discarded the unresolved application.")
	}
	if _, err := TaskUnblock(f.db, nil, task, "The operator repaired the checkout."); err != nil {
		t.Fatal(err)
	}
	effect := f.nextWork()
	if effect["action"] != "repository" || effect["operation"] != "reconcile" || effect["previous_run"] != run {
		t.Fatalf("The resolved condition skipped its authorization check: %#v", effect)
	}
	result := f.repository(effect)
	if result.Status != "not-applied" || result.Applied != nil {
		t.Fatalf("The absent application produced a receipt: %+v", result)
	}
	if dfInt(t, f.db, `SELECT finished FROM merge_operations WHERE run_id=?`, run) != 1 {
		t.Fatal("The absent application retained its old authorization.")
	}
	if result := f.apply(task); result.Applied == nil || result.Applied.Commit != application.Candidate.Commit {
		t.Fatalf("The approved task lost its exact candidate: %+v", result)
	}
	f.requireWorker(independent)
}

func TestDispatchRetriesPreparationAfterThePriorRunStops(t *testing.T) {
	f := newDispatchLifecycle(t)
	task := f.add("Retry the interrupted candidate preparation")
	f.pass(task, "retry.txt", "The candidate keeps the submitted contribution.\n")
	first := f.nextWork()
	if first["action"] != "repository" || first["operation"] != "prepare" || first["task_id"] != task {
		t.Fatalf("The task omitted candidate preparation: %#v", first)
	}
	original := first["run_id"].(string)
	clocks := fixClocks(t, f.db)
	f.reportLostApplication(original)
	if effect := f.nextWork(); effect["action"] != "idle" || effect["reason"] != "repository-operation" {
		t.Fatalf("The preparation retried before its retry time ended: %#v", effect)
	}
	clocks.advance(domain.RetryDelay + time.Second)
	retry := f.nextWork()
	if retry["action"] != "repository" || retry["operation"] != "prepare" || retry["previous_run"] != original || retry["run_id"] == original {
		t.Fatalf("The preparation retry lost its prior run: %#v", retry)
	}
	if dfStr(t, f.db, `SELECT json_extract(result,'$.status') FROM merge_operations WHERE run_id=? AND finished=1`, original) != "interrupted" {
		t.Fatal("The retry left the prior preparation unfinished.")
	}
	result := f.repository(retry)
	if result.Candidate == nil || result.Candidate.Status != "merged" {
		t.Fatalf("The new preparation failed: %+v", result)
	}
	if _, err := RepositoryRecord(f.db, nil, "prepare", original, result); err == nil {
		t.Fatal("The prior preparation replaced its interrupted result.")
	}
	if dfInt(t, f.db, `SELECT count(*) FROM merge_operations WHERE task_id=? AND operation='prepare' AND finished=1`, task) != 2 {
		t.Fatal("The preparation retry lost either run result.")
	}
	f.approve(task)
	if result := f.apply(task); result.Applied == nil {
		t.Fatalf("The task failed after its preparation retry: %+v", result)
	}
}

func TestDispatchApprovalInvalidatesPreparedUnrelatedWork(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusPackaged, 2))
	packet, version := dvPublishPacket(t, db, 1, "", dvOld)
	dvInsertTask(t, db, dvTask(2, dispatch.ScopeTask, dispatch.StatusSeeded, -1))
	beforeRuns := dfInt(t, db, `SELECT count(*) FROM runs`)
	prepared := dfPrepare(t, db, dfRequestID)
	if prepared.candidate == nil || prepared.candidate.spawn.SubjectID == nil || *prepared.candidate.spawn.SubjectID != 2 {
		t.Fatalf("The initial preparation selected %+v.", prepared)
	}
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PacketDecide(db, nil, 1, "approve", "", packet, version); err != nil {
		t.Fatal(err)
	}
	effect := dfCommit(t, db, prepared, attested)
	if effect["code"] != refusal.CodeStale || effect["consequence"] != "none" {
		t.Fatalf("The stale preparation survived the approval: %v", effect)
	}
	if dfInt(t, db, `SELECT count(*) FROM runs`) != beforeRuns || dfInt(t, db, `SELECT run_id IS NULL FROM leases WHERE project='ws-test'`) != 1 || effect["action"] != "refused" {
		t.Fatal("The stale preparation changed the Project execution.")
	}
	records, err := loadVisionRecords(context.Background(), db, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	wantStart(t, records, dvNow(t, db), dispatch.OperationApply, 1)
}
