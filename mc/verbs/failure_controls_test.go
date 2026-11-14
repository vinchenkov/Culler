package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

func proposalReview(t *testing.T) (*sql.DB, *RunIdentity) {
	t.Helper()
	db := dvSpine(t)
	for task := int64(1); task <= 4; task++ {
		dvInsertTask(t, db, dvTask(task, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		_, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{Project: "ws-test", RunID: "review", Owner: "arbiter", PoolSnapshot: []int64{1, 2, 3}, HardDeadlineMinutes: 60})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Tier: "pipeline", Role: "arbiter", RunID: "review", PoolIDs: []int{1, 2, 3}}
	if _, err := RunProcessStarted(db, id, "review"); err != nil {
		t.Fatal(err)
	}
	return db, id
}

func TestOperatorProposedTasksRemainReadOnly(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "before review", true: "during review"}[active], func(t *testing.T) {
			var db *sql.DB
			if active {
				db, _ = proposalReview(t)
			} else {
				db = dvSpine(t)
				dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
			}
			for _, action := range []string{"edit", "revise", "block", "unblock", "resume", "cancel"} {
				request := BoardWriteRequest{OperationID: "refuse-" + action, Action: action, TaskID: 1, ExpectedVersion: boardVersion(t, db, 1)}
				if action == "edit" {
					request.Title = boardText("Changed title")
				}
				if action == "revise" {
					request.Description = boardText("Changed requirements")
				}
				if action != "edit" {
					request.Reason = boardText("The operator requested a change.")
				}
				_, err := BoardWrite(db, nil, request)
				requireBoardCode(t, err, domain.CodeEditRestricted)
			}
			_, err := TaskCancel(db, nil, 1, "The operator withdrew the proposal.")
			requireBoardCode(t, err, domain.CodeEditRestricted)
			_, err = TaskBlock(db, nil, 1, "The operator requested a decision.")
			requireBoardCode(t, err, domain.CodeEditRestricted)
			_, err = TaskUnblock(db, nil, 1, "The operator resolved the decision.")
			requireBoardCode(t, err, domain.CodeEditRestricted)
			_, err = TaskResume(db, nil, 1, "The operator requested a retry.")
			requireBoardCode(t, err, domain.CodeEditRestricted)
			if dfInt(t, db, `SELECT COUNT(*) FROM board_operations`) != 0 {
				t.Fatal("A refused action wrote a receipt")
			}
			if dfStr(t, db, `SELECT status FROM tasks WHERE id=1`) != "proposed" {
				t.Fatal("A refused action changed the stage")
			}
		})
	}
}

func TestSharedProposalDecisionAndCanonicalHistory(t *testing.T) {
	db, id := proposalReview(t)
	dvExec(t, db, `UPDATE projects SET infrastructure_failure_count=1 WHERE id='ws-test'`)
	raw, err := Complete(db, id, CompleteArgs{Run: "review", NeedsOperator: true, Reason: "Which requirement applies?"})
	if err != nil {
		t.Fatal(err)
	}
	decision := raw.(map[string]any)["proposal_decision_id"].(int64)
	if dfInt(t, db, `SELECT COUNT(*) FROM tasks WHERE blocked=1`) != 3 {
		t.Fatal("The shared question did not affect all assigned proposals")
	}
	if dfInt(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`) != 1 {
		t.Fatal("The question changed the Project allowance")
	}
	if dfStr(t, db, `SELECT run_id FROM leases WHERE project='ws-test'`) != "review" {
		t.Fatal("The question released the lease before the container stop")
	}
	if _, err := RunStopped(db, nil, "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := Complete(db, id, CompleteArgs{Run: "review", NeedsOperator: true, Reason: "A late question"}); err == nil {
		t.Fatal("An ended run changed the proposals")
	}
	if _, err := ProposalDecisionResolve(db, nil, "ws-test", decision, "Use the Project requirements."); err != nil {
		t.Fatal(err)
	}
	if dfInt(t, db, `SELECT COUNT(*) FROM tasks WHERE blocked=1`) != 0 {
		t.Fatal("The resolution retained its direct proposal conditions")
	}
	brief, err := buildSpawnBrief(context.Background(), db, &dispatch.Spawn{Project: "ws-test", Role: dispatch.RoleArbiter, ProposedPool: []int64{1, 2, 3}}, "next-review")
	if err != nil || !strings.Contains(brief, "Use the Project requirements.") {
		t.Fatalf("The next review lost the operator answer: %v", err)
	}
	for task := int64(1); task <= 4; task++ {
		if task == 1 {
			dvExec(t, db, `UPDATE tasks SET status='seeded' WHERE id=1`)
		}
		raw, err := TaskGet(db, task)
		if err != nil {
			t.Fatal(err)
		}
		history := raw.(map[string]any)["run_history"].([]map[string]any)
		if task == 4 {
			if len(history) != 0 {
				t.Fatal("An unassigned proposal received this run in its history")
			}
			continue
		}
		if len(history) != 1 || history[0]["id"] != "review" || history[0]["subject"] != nil {
			t.Fatalf("History for task %d: %+v", task, history)
		}
		var pool []int64
		if err := json.Unmarshal([]byte(history[0]["pool_snapshot"].(string)), &pool); err != nil || len(pool) != 3 {
			t.Fatal("The canonical run lost its assignment")
		}
	}
}

func TestProposalDecisionRejectsUnassignedAffectedTasks(t *testing.T) {
	db, id := proposalReview(t)
	for _, affected := range [][]int64{{4}, {1, 1}} {
		_, err := Complete(db, id, CompleteArgs{Run: "review", NeedsOperator: true, Reason: "Which requirement applies?", Affected: affected})
		requireBoardCode(t, err, domain.CodePoolMismatch)
	}
	if dfInt(t, db, `SELECT COUNT(*) FROM proposal_decisions`) != 0 {
		t.Fatal("A refused report recorded a decision")
	}
	if dfInt(t, db, `SELECT COUNT(*) FROM tasks WHERE blocked=1`) != 0 {
		t.Fatal("A refused report changed task conditions")
	}
}

func TestDecisionResolutionAndTaskResumeRemainIndependent(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `UPDATE tasks SET blocked=1,blocked_kind='operator',blocked_reason='Which requirement applies?',dispatch_retries=0 WHERE id=1`)
	if _, err := TaskResume(db, nil, 1, "The operator repaired the run condition."); err != nil {
		t.Fatal(err)
	}
	if dfInt(t, db, `SELECT blocked=1 AND stalled=0 AND dispatch_retries=3 FROM tasks WHERE id=1`) != 1 {
		t.Fatal("Task resume changed the independent decision")
	}
	dvExec(t, db, `UPDATE tasks SET dispatch_retries=0 WHERE id=1`)
	if _, err := TaskUnblock(db, nil, 1, "Use the Project requirements."); err != nil {
		t.Fatal(err)
	}
	if dfInt(t, db, `SELECT blocked=0 AND stalled=1 AND dispatch_retries=0 FROM tasks WHERE id=1`) != 1 {
		t.Fatal("Decision resolution changed the run allowance")
	}
}

func TestSuccessfulProposalResultRestoresProjectInfrastructure(t *testing.T) {
	db := dvSpine(t)
	err := inTx(db, func(ctx context.Context, q Q) error {
		_, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{Project: "ws-test", RunID: "propose", Owner: "strategist", HardDeadlineMinutes: 60})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `UPDATE projects SET infrastructure_failure_count=2,infrastructure_next_retry_at='2099-01-01T00:00:00Z' WHERE id='ws-test'`)
	id := &RunIdentity{Tier: "pipeline", Role: "strategist(propose)", RunID: "propose"}
	if _, err := StrategistPropose(db, id, "propose", strings.NewReader(`{"proposals":[]}`)); err != nil {
		t.Fatal(err)
	}
	if dfInt(t, db, `SELECT infrastructure_failure_count=0 AND infrastructure_next_retry_at IS NULL FROM projects WHERE id='ws-test'`) != 1 {
		t.Fatal("The successful proposal result did not restore the Project allowance")
	}
}

func TestCheckpointPreservesProjectInfrastructureAllowance(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Retain partial work without a completed result")
	run, root, id := f.run(task, "worker")
	dvExec(t, f.db, `UPDATE projects SET infrastructure_failure_count=2 WHERE id='ws-test'`)
	dvExec(t, f.db, `UPDATE tasks SET dispatch_retries=2 WHERE id=?`, task)
	commit := f.commit(root, "partial.txt", "The task remains incomplete.\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Checkpoint: true, Commit: commit}); err != nil {
		t.Fatal(err)
	}
	if dfInt(t, f.db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`) != 2 {
		t.Fatal("The checkpoint restored the Project allowance before completion")
	}
	if dfInt(t, f.db, `SELECT dispatch_retries FROM tasks WHERE id=?`, task) != 2 {
		t.Fatal("The checkpoint restored the task allowance before stage progress")
	}
}
