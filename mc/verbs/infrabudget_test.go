package verbs

import (
	"context"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

// The retained submission moves the task forward. The forward move restores the allowance.
func TestForwardStageProgressRestoresTheInfraBudget(t *testing.T) {
	f, task, run, root, id := spentBudget(t)
	head := f.commit(root, "work.txt", "The complete work.\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: head}); err != nil {
		t.Fatal(err)
	}
	f.capture(task, run, root)
	if got := dfStr(t, f.db, `SELECT status FROM tasks WHERE id=?`, task); got != "worked" {
		t.Fatalf("status after the retained submission = %s", got)
	}
	if got := dfInt(t, f.db, `SELECT dispatch_retries FROM tasks WHERE id=?`, task); got != domain.InfraBudget {
		t.Fatalf("dispatch_retries after the forward move = %d, want %d", got, domain.InfraBudget)
	}
}

// The infrastructure terminal charges the allowance. Its own release keeps the charge.
func TestAnInfraTerminalKeepsItsOwnCharge(t *testing.T) {
	f, task, run, _, id := spentBudget(t)
	raw, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Infra: true, Reason: "The container stopped before its result."})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := raw.(map[string]any)
	if !ok || got["dispatch_retries"] != 0 || got["stalled"] != true {
		t.Fatalf("infra terminal result = %v, want the last unit spent and the subject stalled", raw)
	}
	if n := dfInt(t, f.db, `SELECT dispatch_retries FROM tasks WHERE id=?`, task); n != 0 {
		t.Fatalf("dispatch_retries = %d, want the charge to survive its own release", n)
	}
	if b := dfInt(t, f.db, `SELECT stalled FROM tasks WHERE id=?`, task); b != 1 {
		t.Fatal("exhaustion did not stall the subject")
	}
}

// A run without a subject has no task allowance to restore.
func TestASubjectlessTerminalRestoresNothing(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(7, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `UPDATE tasks SET dispatch_retries = 1 WHERE id = 7`)
	dvExec(t, db, `INSERT INTO runs (id,tier,role,project) VALUES ('propose','pipeline','strategist','ws-test')`)
	dvHold(t, db, "ws-test", "propose", "strategist", nil, 0, nil, time.Hour)

	if err := inTx(db, func(ctx context.Context, q Q) error {
		return releaseLease(ctx, q, "propose")
	}); err != nil {
		t.Fatal(err)
	}
	if got := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=7`); got != 1 {
		t.Fatalf("dispatch_retries = %d, want the unrelated task untouched", got)
	}
}

// An unblock is the decision to try again. It restores the allowance of an exhausted task.
func TestResumeRestoresOnlyTheTaskRunFailureAllowance(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(7, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `UPDATE tasks SET dispatch_retries = 0, blocked = 1, blocked_kind = 'operator',
		blocked_reason = 'dispatch retries exhausted (container-gone)' WHERE id = 7`)

	if _, err := TaskResume(db, nil, 7, "The operator resolved the condition."); err != nil {
		t.Fatal(err)
	}
	if got := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=7`); got != domain.InfraBudget {
		t.Fatalf("dispatch_retries after unblock = %d, want %d", got, domain.InfraBudget)
	}
	if b := dfInt(t, db, `SELECT blocked FROM tasks WHERE id=7`); b != 1 {
		t.Fatal("Task resume cleared the independent decision")
	}
}
