package domain_test

import (
	"context"
	"mc/domain"
	"testing"
)

func TestProjectInfrastructureAllowancePreservesTaskControls(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mustExec(t, db, `UPDATE tasks SET correction_count=2,dispatch_retries=2,blocked=1,blocked_kind='operator',blocked_reason='Independent question' WHERE id=?`, task)
	for want := 2; want >= 0; want-- {
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			result, err := domain.ChargeProjectInfrastructure(ctx, q, "ws", "The launch failed.", "")
			if result.Remaining != want || result.Stalled != (want == 0) {
				t.Fatalf("Project allowance: %+v", result)
			}
			return err
		})
	}
	if got := taskInt(t, db, task, "dispatch_retries"); got != 2 {
		t.Fatalf("Task allowance changed: %d", got)
	}
	if got := taskInt(t, db, task, "correction_count"); got != 2 {
		t.Fatalf("Correction allowance changed: %d", got)
	}
	if got := taskStr(t, db, task, "blocked_reason"); got != "Independent question" {
		t.Fatalf("Task decision changed: %s", got)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.ResetProjectInfrastructure(ctx, q, "ws", "operator", "")
	})
	if got := oneStr(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws'`); got != "0" {
		t.Fatalf("Project did not resume: %s", got)
	}
	if got := taskInt(t, db, task, "dispatch_retries"); got != 2 {
		t.Fatal("Project resume restored the task allowance")
	}
}

func TestClaimPreservesProjectInfrastructureAllowance(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "seeded")
	mustExec(t, db, `UPDATE projects SET infrastructure_failure_count=2 WHERE id='ws'`)
	if err := claim(t, db, domain.ClaimArgs{Project: "ws", RunID: "claim", Owner: "worker", SubjectID: &task, HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	if got := oneStr(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws'`); got != "2" {
		t.Fatalf("The claim restored the allowance: %s", got)
	}
	if got := oneStr(t, db, `SELECT agent_started_at IS NULL FROM runs WHERE id='claim'`); got != "1" {
		t.Fatal("The claim recorded a process start")
	}
}
