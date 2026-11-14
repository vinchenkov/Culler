package domain_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"mc/domain"
)

func reportFailure(t *testing.T, db *sql.DB, run string, report domain.InfrastructureFailureReport) domain.InfrastructureFailureResult {
	t.Helper()
	var result domain.InfrastructureFailureResult
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		result, err = domain.RecordInfrastructureFailure(ctx, q, run, report)
		return err
	})
	return result
}

func TestInfrastructureFailureCountsOnceAcrossReportAndExit(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	mustExec(t, db, `UPDATE tasks SET completion_sha = 'accepted-version', correction_count = 2 WHERE id = ?`, id)
	if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "agent-report", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	report := domain.InfrastructureFailureReport{Condition: "The service refused the request.", Source: "agent", Cause: "credentials", Evidence: "The service returned an expired credential response.", OperatorAction: "Renew the credential."}
	first := reportFailure(t, db, "agent-report", report)
	if !first.Charged || first.Remaining != 2 || first.Stalled || first.TaskID != id {
		t.Fatalf("first report = %+v", first)
	}
	if got := lockStr(t, db, "run_id"); got != "agent-report" {
		t.Fatalf("report released the container lease: %s", got)
	}
	if got := reportFailure(t, db, "agent-report", report); got.Charged || got.Remaining != 2 {
		t.Fatalf("repeat report = %+v", got)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		result, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "agent-report", Reason: "container-gone"})
		if result.Charged {
			t.Error("container exit charged the same run twice")
		}
		return err
	})
	// A repeated observation before the stop still cannot charge the run.
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		result, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "agent-report", Reason: "hard-deadline"})
		if result.Charged {
			t.Error("a second stop condition charged the same run")
		}
		return err
	})
	if got := oneStr(t, db, `SELECT outcome FROM runs WHERE id = 'agent-report'`); got != "infra-failed" {
		t.Fatalf("outcome = %s", got)
	}
	if got := oneStr(t, db, `SELECT COUNT(*) FROM infrastructure_failures`); got != "1" {
		t.Fatalf("records = %s", got)
	}
	if got := taskStr(t, db, id, "completion_sha"); got != "accepted-version" {
		t.Fatalf("accepted work changed: %s", got)
	}
	if got := taskInt(t, db, id, "correction_count"); got != 2 {
		t.Fatalf("correction count = %d", got)
	}
	if got := lockStr(t, db, "run_id"); got != "agent-report" {
		t.Fatalf("reap released lease before the stop: %s", got)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, "agent-report") })
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, "agent-report") })
	if got := lockStr(t, db, "run_id"); got != "<NULL>" {
		t.Fatalf("acknowledgment retained lease: %s", got)
	}
	wantAbort(t, db, `UPDATE infrastructure_failures SET cause = 'unknown' WHERE run_id = 'agent-report'`)
	wantAbort(t, db, `DELETE FROM infrastructure_failures WHERE run_id = 'agent-report'`)
}

func TestInfrastructureThirdFailureStallsOnlyAssignedTask(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	other := mkTask(t, db, "task", "seeded")
	for n := 1; n <= 3; n++ {
		run := fmt.Sprintf("failure-%d", n)
		if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: run, Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
			t.Fatal(err)
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			result, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: run, Reason: "container-gone"})
			if result.Stalled != (n == 3) || !result.Charged || result.Blocked {
				t.Errorf("failure %d result = %+v", n, result)
			}
			return err
		})
		mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, run) })
		if got := taskInt(t, db, id, "infrastructure_failure_count"); got != int64(n) {
			t.Fatalf("count = %d", got)
		}
	}
	if got := taskInt(t, db, id, "stalled"); got != 1 {
		t.Fatal("task did not stall")
	}
	if got := taskInt(t, db, id, "blocked"); got != 0 {
		t.Fatal("infrastructure failure created an operator block")
	}
	if got := taskInt(t, db, other, "infrastructure_failure_count"); got != 0 {
		t.Fatal("unrelated task lost its allowance")
	}
}

func TestInfrastructureFinalOutcomeAndCancellationPreventCharges(t *testing.T) {
	for _, outcome := range []string{"completed", "blocked", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			db := openSpine(t)
			id := mkTask(t, db, "task", "seeded")
			if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "final", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, `UPDATE runs SET ended_at = datetime('now'), outcome = ? WHERE id = 'final'`, outcome)
			result := reportFailure(t, db, "final", domain.InfrastructureFailureReport{Condition: "runner-exit", Source: "runner"})
			if result.Charged || result.Remaining != 3 {
				t.Fatalf("final report = %+v", result)
			}
			mustTx(t, db, func(ctx context.Context, q domain.Q) error {
				result, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "final", Reason: "container-gone"})
				if result.Charged {
					t.Error("final outcome incurred a failure")
				}
				return err
			})
			if got := oneStr(t, db, `SELECT outcome FROM runs WHERE id = 'final'`); got != outcome {
				t.Fatalf("outcome = %s", got)
			}
			if got := oneStr(t, db, `SELECT count(*) FROM infrastructure_failures`); got != "0" {
				t.Fatalf("records = %s", got)
			}
		})
	}
	t.Run("operator_cancel_wins_before_run_terminal", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "cancelled-task", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `UPDATE tasks SET decision = 'cancelled', decided_at = datetime('now'), archived = 1 WHERE id = ?`, id)
		result := reportFailure(t, db, "cancelled-task", domain.InfrastructureFailureReport{Condition: "runner-exit", Source: "runner"})
		if result.Charged || result.Remaining != 3 {
			t.Fatalf("cancelled report = %+v", result)
		}
		if got := oneStr(t, db, `SELECT outcome FROM runs WHERE id = 'cancelled-task'`); got != "cancelled" {
			t.Fatalf("outcome = %s", got)
		}
	})
}

func TestInfrastructureAllowanceResetsOnlyOnForwardProgress(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	mustExec(t, db, `UPDATE tasks SET dispatch_retries = 1 WHERE id = ?`, id)
	if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "same-stage", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.Heartbeat(ctx, q, "same-stage")
		return err
	})
	mustExec(t, db, `UPDATE runs SET ended_at = datetime('now'), outcome = 'completed' WHERE id = 'same-stage'`)
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.Release(ctx, q, "same-stage") })
	mustExec(t, db, `UPDATE tasks SET status = status WHERE id = ?`, id)
	if got := taskInt(t, db, id, "dispatch_retries"); got != 1 {
		t.Fatalf("same-stage allowance = %d", got)
	}
	for _, stage := range []string{"worked", "verified", "packaged"} {
		mustExec(t, db, `UPDATE tasks SET dispatch_retries = 1 WHERE id = ?`, id)
		if stage == "packaged" {
			seedVerifiedVersion(t, db, id)
		}
		mustExec(t, db, `UPDATE tasks SET status = ? WHERE id = ?`, stage, id)
		if got := taskInt(t, db, id, "dispatch_retries"); got != 3 {
			t.Fatalf("%s allowance = %d", stage, got)
		}
	}
	mustExec(t, db, `UPDATE tasks SET dispatch_retries = 1 WHERE id = ?`, id)
	mkPacket(t, db, id)
	mustExec(t, db, `UPDATE tasks SET status = 'seeded' WHERE id = ?`, id)
	if got := taskInt(t, db, id, "dispatch_retries"); got != 1 {
		t.Fatalf("revision allowance = %d", got)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.Block(ctx, q, id, domain.BlockOperator, "The operator must select the scope.")
	})
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.Unblock(ctx, q, id) })
	if got := taskInt(t, db, id, "dispatch_retries"); got != 1 {
		t.Fatalf("block resolution allowance = %d", got)
	}
}

func TestInfrastructureReportRequiresEvidenceForKnownCause(t *testing.T) {
	for _, report := range []domain.InfrastructureFailureReport{
		{Condition: "runner-exit", Source: "runner", Cause: "credentials"},
		{Condition: "runner-exit", Source: "runner", Cause: "quota"},
		{Condition: "runner-exit", Source: "runner", OperatorAction: "Renew credentials."},
		{Condition: "runner-exit", Source: "runner", QuotaResetAt: "2026-09-07T00:00:00Z"},
		{Condition: "quota response", Source: "agent", Cause: "quota", Evidence: "The service confirmed quota exhaustion.", QuotaResetAt: "tomorrow"},
	} {
		if err := domain.ValidateInfrastructureFailureReport(report); err == nil {
			t.Errorf("accepted report without valid evidence: %+v", report)
		}
	}
	if err := domain.ValidateInfrastructureFailureReport(domain.InfrastructureFailureReport{
		Condition: "The service refused the request.", Source: "agent", Cause: "quota",
		Evidence: "The service confirmed quota exhaustion and supplied its reset time.", QuotaResetAt: "2026-09-07T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInfrastructureOperatorInterruptKeepsItsOutcome(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	mustExec(t, db, `UPDATE tasks SET dispatch_retries = 1 WHERE id = ?`, id)
	if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "operator-interrupt", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE tasks SET decision = 'cancelled', decided_at = datetime('now'), archived = 1 WHERE id = ?`, id)
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		result, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "operator-interrupt", Reason: "interrupted", NoCharge: true, Outcome: "interrupted"})
		if result.Charged {
			t.Error("the operator interrupt charged an infrastructure failure")
		}
		return err
	})
	if got := oneStr(t, db, `SELECT outcome FROM runs WHERE id = 'operator-interrupt'`); got != "interrupted" {
		t.Fatalf("outcome = %s", got)
	}
	if got := taskInt(t, db, id, "dispatch_retries"); got != 1 {
		t.Fatalf("remaining allowance = %d", got)
	}
	if got := oneStr(t, db, `SELECT count(*) FROM infrastructure_failures`); got != "0" {
		t.Fatalf("failure records = %s", got)
	}
	if got := lockStr(t, db, "run_id"); got != "operator-interrupt" {
		t.Fatalf("reap released lease before the stop: %s", got)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.AcknowledgeRunStop(ctx, q, "operator-interrupt")
	})
	if got := lockStr(t, db, "run_id"); got != "<NULL>" {
		t.Fatalf("lease = %s", got)
	}
}

func TestInfrastructureStopAcknowledgmentRequiresFinalRun(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "active", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, "active") })
	if got := lockStr(t, db, "run_id"); got != "active" {
		t.Fatalf("active lease = %s", got)
	}
	if got := taskInt(t, db, id, "infrastructure_failure_count"); got != 0 {
		t.Fatalf("count = %d", got)
	}
}

func TestInfrastructureStopAcknowledgmentCannotReleaseNewerRun(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "old", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "old", Reason: "container-gone"})
		return err
	})
	if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "premature", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err == nil {
		t.Fatal("a new run claimed the lease before the stop")
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, "old") })
	if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "new", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, "old") })
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		result, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "old", Reason: "container-gone"})
		if result.Charged {
			t.Error("a repeated old observation charged twice")
		}
		return err
	})
	if got := lockStr(t, db, "run_id"); got != "new" {
		t.Fatalf("new lease = %s", got)
	}
	if got := taskInt(t, db, id, "infrastructure_failure_count"); got != 1 {
		t.Fatalf("count = %d", got)
	}
}

func TestStrategistFailureAllowanceAndHistory(t *testing.T) {
	db := openSpine(t)
	for count := 1; count <= 3; count++ {
		run := fmt.Sprintf("proposal-%d", count)
		if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: run, Owner: "strategist", HardDeadlineMinutes: 240}); err != nil {
			t.Fatal(err)
		}
		report := domain.InfrastructureFailureReport{Condition: "The runner failed before its result.", Source: "resident"}
		result := reportFailure(t, db, run, report)
		if !result.Charged || result.Remaining != 3-count {
			t.Fatalf("result=%+v", result)
		}
		if reportFailure(t, db, run, report).Charged {
			t.Fatal("The duplicate report charged the allowance.")
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, run) })
	}
	if got := oneStr(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws'`); got != "3" {
		t.Fatal(got)
	}
	if got := oneStr(t, db, `SELECT COUNT(*) FROM infrastructure_failures WHERE task_id IS NULL`); got != "3" {
		t.Fatal(got)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.ResetProjectInfrastructure(ctx, q, "ws", "operator", "")
	})
	if got := oneStr(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws'`); got != "0" {
		t.Fatal(got)
	}
	if got := oneStr(t, db, `SELECT COUNT(*) FROM infrastructure_failures`); got != "3" {
		t.Fatal("Resume removed failure history.")
	}
}

func TestInfrastructureSystemConditionsUseNoAllowance(t *testing.T) {
	status := 503
	for _, c := range []struct {
		name, reason string
		report       domain.InfrastructureFailureReport
	}{
		{"network", "network", domain.InfrastructureFailureReport{Condition: "API Error: Connection refused", Source: "runner", NetworkError: "connect"}},
		{"provider", "provider", domain.InfrastructureFailureReport{Condition: "API Error: 503", Source: "runner", HTTPStatus: &status}},
		{"launch_check", "connection", domain.InfrastructureFailureReport{Condition: "The connection changed or became unavailable before launch.", Source: "resident", Cause: "connection", Evidence: "The connection changed after dispatch."}},
		{"credentials", "connection", domain.InfrastructureFailureReport{Condition: "spawn refused", Source: "resident", Cause: "credentials", Evidence: "The credential store contains no grant.", OperatorAction: "Restore the credentials."}},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := openSpine(t)
			id := mkTask(t, db, "task", "seeded")
			if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "system", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
				t.Fatal(err)
			}
			if result := reportFailure(t, db, "system", c.report); result.Charged || result.Remaining != 3 || result.Stalled {
				t.Fatalf("result = %+v", result)
			}
			if got := oneStr(t, db, `SELECT outcome || ' ' || end_reason FROM runs WHERE id = 'system'`); got != "infra-failed "+c.reason {
				t.Fatalf("run end = %s", got)
			}
			if got := oneStr(t, db, `SELECT COUNT(*) FROM infrastructure_failures`); got != "0" {
				t.Fatalf("records = %s", got)
			}
			if got := oneStr(t, db, `SELECT count_before || '/' || count_after FROM diagnostic_events WHERE run_id = 'system' AND outcome = 'failed'`); got != "0/0" {
				t.Fatalf("diagnostic counts = %s", got)
			}
		})
	}
}

func TestInfrastructureRunFailuresUseTheAllowance(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		report       domain.InfrastructureFailureReport
	}{
		{"agent_credentials", "agent", domain.InfrastructureFailureReport{Condition: "The deploy service refused the token.", Source: "agent", Cause: "credentials", Evidence: "HTTP 403 from the deploy service.", OperatorAction: "Renew the deploy token."}},
		{"agent_network", "agent", domain.InfrastructureFailureReport{Condition: "The package index was unreachable.", Source: "agent", NetworkError: "connect"}},
		{"harness", "harness", domain.InfrastructureFailureReport{Condition: "The harness exited with code 1.", Source: "runner"}},
		{"launch", "launch", domain.InfrastructureFailureReport{Condition: "docker create failed", Source: "resident"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := openSpine(t)
			id := mkTask(t, db, "task", "seeded")
			if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "own", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
				t.Fatal(err)
			}
			if result := reportFailure(t, db, "own", c.report); !result.Charged || result.Remaining != 2 {
				t.Fatalf("result = %+v", result)
			}
			if got := oneStr(t, db, `SELECT end_reason FROM runs WHERE id = 'own'`); got != c.reason {
				t.Fatalf("end reason = %s", got)
			}
		})
	}
	t.Run("reap", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "silent", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
			t.Fatal(err)
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			_, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "silent", Reason: "lease-timeout"})
			return err
		})
		if got := oneStr(t, db, `SELECT outcome || ' ' || end_reason FROM runs WHERE id = 'silent'`); got != "reaped lease-timeout" {
			t.Fatalf("run end = %s", got)
		}
	})
	if err := domain.ValidateInfrastructureFailureReport(domain.InfrastructureFailureReport{Condition: "x", Source: "agent", Cause: "connection", Evidence: "x"}); err == nil {
		t.Fatal("an agent reported a connection cause")
	}
}

func TestInfrastructureCompletedRunRecordsNoFailure(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "done", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE runs SET ended_at = datetime('now'), outcome = 'completed' WHERE id = 'done'`)
	reportFailure(t, db, "done", domain.InfrastructureFailureReport{Condition: "The harness exited with code 0.", Source: "runner"})
	if got := oneStr(t, db, `SELECT COUNT(*) FROM diagnostic_events WHERE run_id = 'done'`); got != "0" {
		t.Fatalf("diagnostics = %s", got)
	}
	if got := oneStr(t, db, `SELECT COALESCE(end_reason, '')  FROM runs WHERE id = 'done'`); got != "" {
		t.Fatalf("end reason = %s", got)
	}
}
