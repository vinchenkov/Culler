package domain_test

import (
	"context"
	"fmt"
	"mc/domain"
	"testing"
)

func TestRunFailureOwnerUsesProcessStartAndAssignment(t *testing.T) {
	for _, c := range []struct {
		name, role                string
		started, subject, project bool
	}{
		{"Worker launch", "worker", false, true, true},
		{"Worker run", "worker", true, true, false},
		{"Proposal creation", "strategist", true, false, true},
		{"Proposal review", "arbiter", true, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := openSpine(t)
			task := mkTask(t, db, "task", "seeded")
			args := domain.ClaimArgs{Project: "ws", RunID: "run", Owner: c.role, HardDeadlineMinutes: 240}
			if c.subject {
				args.SubjectID = &task
			}
			if err := claim(t, db, args); err != nil {
				t.Fatal(err)
			}
			if c.started {
				mustExec(t, db, `UPDATE runs SET agent_started_at=datetime('now') WHERE id='run'`)
			}
			report := domain.InfrastructureFailureReport{Source: "runner", Condition: "The execution stopped."}
			first := reportFailure(t, db, "run", report)
			second := reportFailure(t, db, "run", report)
			if !first.Charged || second.Charged {
				t.Fatal("The same run must consume one failure unit")
			}
			project := oneStr(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws'`)
			retries := taskInt(t, db, task, "dispatch_retries")
			if c.project && (project != "1" || retries != 3) {
				t.Fatalf("Project failure: %s, task allowance: %d", project, retries)
			}
			if !c.project && (project != "0" || retries != 2) {
				t.Fatalf("Task failure: %s, task allowance: %d", project, retries)
			}
			if got := lockStr(t, db, "run_id"); got != "run" {
				t.Fatal("The failure released the lease before the container stop")
			}
		})
	}
}

func TestLaunchFailuresAcrossTasksExhaustOneProjectAllowance(t *testing.T) {
	db := openSpine(t)
	for n := 1; n <= 3; n++ {
		task := mkTask(t, db, "task", "seeded")
		run := fmt.Sprintf("launch-%d", n)
		if err := claim(t, db, domain.ClaimArgs{Project: "ws", RunID: run, Owner: "worker", SubjectID: &task, HardDeadlineMinutes: 240}); err != nil {
			t.Fatal(err)
		}
		reportFailure(t, db, run, domain.InfrastructureFailureReport{Source: "resident", Condition: "The container launch failed."})
		mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, run) })
		if got := taskInt(t, db, task, "dispatch_retries"); got != 3 {
			t.Fatal("The launch failure consumed the task allowance")
		}
	}
	if got := oneStr(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws'`); got != "3" {
		t.Fatalf("Project failure count: %s", got)
	}
	if got := oneStr(t, db, `SELECT count(*) FROM tasks WHERE blocked=1 OR stalled=1`); got != "0" {
		t.Fatal("The Project failure created direct task conditions")
	}
}

func TestSharedProposalDecisionPreservesIndependentConditions(t *testing.T) {
	db := openSpine(t)
	pool := []int64{}
	for n := 0; n < 3; n++ {
		pool = append(pool, mkTask(t, db, "task", "proposed"))
	}
	mustExec(t, db, `UPDATE tasks SET blocked=1,blocked_kind='operator',blocked_reason='Independent question',dispatch_retries=0 WHERE id=?`, pool[0])
	if err := claim(t, db, domain.ClaimArgs{Project: "ws", RunID: "review", Owner: "arbiter", PoolSnapshot: pool, HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	var decision int64
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		decision, err = domain.RecordProposalDecision(ctx, q, "review", "Which requirement applies?", nil)
		return err
	})
	if got := oneStr(t, db, `SELECT count(*) FROM tasks WHERE blocked=1`); got != "3" {
		t.Fatalf("Direct proposal conditions: %s", got)
	}
	if got := oneStr(t, db, `SELECT group_concat(blocked_kind,',') FROM (SELECT blocked_kind FROM tasks ORDER BY id)`); got != "operator,proposal,proposal" {
		t.Fatalf("The proposal conditions have the types %s", got)
	}
	if got := oneStr(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws'`); got != "0" {
		t.Fatal("The decision consumed a failure unit")
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.ResolveProposalDecision(ctx, q, "ws", decision, "Use the Project requirements.")
	})
	if got := taskInt(t, db, pool[0], "blocked"); got != 1 || taskStr(t, db, pool[0], "blocked_kind") != "operator" {
		t.Fatal("Resolution cleared the independent decision")
	}
	if got := taskInt(t, db, pool[0], "stalled"); got != 1 {
		t.Fatal("Resolution restored the task allowance")
	}
	for _, task := range pool[1:] {
		if got := taskInt(t, db, task, "blocked"); got != 0 {
			t.Fatal("Resolution retained its shared decision condition")
		}
	}
}
