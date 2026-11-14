package verbs

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

// networkRun claims a Worker run of task 1 on the admitted state of profile "profile".
func networkRun(t *testing.T, db *sql.DB, run string) {
	t.Helper()
	_, revision, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now(), "revision")
	if err != nil {
		t.Fatal(err)
	}
	subject := int64(1)
	if err := inTx(db, func(ctx context.Context, q Q) error {
		if _, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{Project: "ws-test", RunID: run, Owner: "worker", SubjectID: &subject, HardDeadlineMinutes: 240}); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE runs SET connection_id='test',authorization_id='authorization',connection_revision=?,profile_id='profile',profile_revision='revision' WHERE id=?`, revision, run)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func networkReport(t *testing.T, db *sql.DB, run string, report domain.InfrastructureFailureReport) domain.InfrastructureFailureResult {
	t.Helper()
	var result domain.InfrastructureFailureResult
	if err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		if result, err = domain.RecordInfrastructureFailure(ctx, q, run, report); err != nil {
			return err
		}
		return domain.AcknowledgeRunStop(ctx, q, run)
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRunNetworkErrorHoldsTheProfileUntilItsRetry(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	healthObservation(t, db, healthCheck(t, db, "authorization"), "ready", "", 0)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 0))
	network := domain.InfrastructureFailureReport{Condition: "API Error: Connection refused (ECONNREFUSED)", Source: "runner", NetworkError: "connect"}
	for count, delay := range []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		run := "network-" + string(rune('a'+count))
		networkRun(t, db, run)
		before := time.Now()
		result := networkReport(t, db, run, network)
		if result.Charged || result.Remaining != 3 {
			t.Fatalf("A network error used the allowance: %+v", result)
		}
		var state, retry string
		var failures int
		if err := db.QueryRow(`SELECT state,failure_count,next_retry_at FROM connection_health WHERE connection_id='test' AND profile_id='profile'`).Scan(&state, &failures, &retry); err != nil {
			t.Fatal(err)
		}
		at, err := time.Parse(time.RFC3339Nano, retry)
		if err != nil || state != "retry_wait" || failures != min(count+1, 3) || at.Before(before.Add(delay)) || at.After(time.Now().Add(delay)) {
			t.Fatalf("Failure %d: state=%s failures=%d retry=%s", count+1, state, failures, retry)
		}
		if _, _, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now(), "revision"); err == nil || !strings.Contains(err.Error(), "automatic retry") {
			t.Fatalf("Failure %d did not hold the profile: %v", count+1, err)
		}
		if _, _, err := ConnectionAdmission(context.Background(), db, "test", "other", time.Now(), "revision"); err != nil {
			t.Fatalf("Failure %d held another profile: %v", count+1, err)
		}
		if _, _, err := ConnectionAdmission(context.Background(), db, "test", "profile", at.Add(time.Second), "revision"); err != nil {
			t.Fatalf("The retry time did not admit the next run: %v", err)
		}
		dvExec(t, db, `UPDATE connection_health SET next_retry_at=? WHERE profile_id='profile'`, before.Add(-time.Minute).UTC().Format(time.RFC3339Nano))
	}
	var endReason string
	if err := db.QueryRow(`SELECT end_reason FROM runs WHERE id='network-a'`).Scan(&endReason); err != nil || endReason != "network" {
		t.Fatalf("end reason=%q err=%v", endReason, err)
	}
	status, err := SystemStatus(db, nil, SystemStatusRequest{Connections: []ConnectionRequirement{{ID: "test", AuthorizationID: "authorization", Operation: "token_refresh", Profiles: map[string]string{"profile": "revision"}}}})
	if err != nil {
		t.Fatal(err)
	}
	conditions := status.(SystemStatusResult).Conditions
	if len(conditions) != 1 || conditions[0].State != "retry_wait" || conditions[0].NetworkError != "connect" || conditions[0].Action != "" {
		t.Fatalf("Network condition: %+v", conditions)
	}
	networkRun(t, db, "reached-model")
	if err := inTx(db, func(ctx context.Context, q Q) error { return endRun(ctx, q, "reached-model", "completed") }); err != nil {
		t.Fatal(err)
	}
	var state string
	var failures int
	if err := db.QueryRow(`SELECT state,failure_count FROM connection_health WHERE connection_id='test' AND profile_id='profile'`).Scan(&state, &failures); err != nil || state != "ready" || failures != 0 {
		t.Fatalf("A finished run kept the hold: state=%s failures=%d err=%v", state, failures, err)
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now(), "revision"); err != nil {
		t.Fatal(err)
	}
}

func TestRunNetworkErrorKeepsAStoppedProfileAndIgnoresAnOlderState(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	healthObservation(t, db, healthCheck(t, db, "authorization"), "ready", "", 0)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 0))
	network := domain.InfrastructureFailureReport{Condition: "API Error: Connection dropped (ECONNRESET)", Source: "runner", NetworkError: "reset"}
	networkRun(t, db, "older")
	healthObservation(t, db, healthCheck(t, db, "authorization"), "ready", "", 0)
	networkReport(t, db, "older", network)
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM connection_health WHERE profile_id='profile'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("A failure of an older state held the profile: rows=%d err=%v", rows, err)
	}
	networkRun(t, db, "stopped")
	status := 503
	if err := inTx(db, func(ctx context.Context, q Q) error {
		_, err := domain.RecordInfrastructureFailure(ctx, q, "stopped", domain.InfrastructureFailureReport{Condition: "API Error: 503", Source: "runner", HTTPStatus: &status})
		if err != nil {
			return err
		}
		return domain.AcknowledgeRunStop(ctx, q, "stopped")
	}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM connection_health WHERE profile_id='profile'`).Scan(&state); err != nil || state != "stopped" {
		t.Fatalf("state=%s err=%v", state, err)
	}
	var revision int64
	if err := db.QueryRow(`SELECT SUM(revision) FROM connection_health WHERE connection_id='test'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	subject := int64(1)
	if err := inTx(db, func(ctx context.Context, q Q) error {
		if _, err := domain.Claim(ctx, q, time.Now(), domain.ClaimArgs{Project: "ws-test", RunID: "late", Owner: "worker", SubjectID: &subject, HardDeadlineMinutes: 240}); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE runs SET connection_id='test',authorization_id='authorization',connection_revision=?,profile_id='profile',profile_revision='revision' WHERE id='late'`, revision)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	networkReport(t, db, "late", network)
	if err := db.QueryRow(`SELECT state FROM connection_health WHERE profile_id='profile'`).Scan(&state); err != nil || state != "stopped" {
		t.Fatalf("A network error replaced the stop: state=%s err=%v", state, err)
	}
}

func recoveryFixture(t *testing.T) (*sql.DB, *RunIdentity) {
	t.Helper()
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	healthObservation(t, db, healthCheck(t, db, "authorization"), "ready", "", 0)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 0))
	networkRun(t, db, "prime")
	networkReport(t, db, "prime", domain.InfrastructureFailureReport{Condition: "The response stream disconnected.", Source: "runner", NetworkError: "reset"})
	dvExec(t, db, `UPDATE connection_health SET next_retry_at='2000-01-01T00:00:00Z' WHERE profile_id='profile'`)
	networkRun(t, db, "replacement")
	return db, &RunIdentity{RunID: "replacement", Tier: "pipeline", Role: "worker"}
}

func recoveryStatus(t *testing.T, db *sql.DB) SystemCondition {
	t.Helper()
	raw, err := SystemStatus(db, nil, SystemStatusRequest{Connections: []ConnectionRequirement{{ID: "test", AuthorizationID: "authorization", Operation: "token_refresh", Profiles: map[string]string{"profile": "revision"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range raw.(SystemStatusResult).Connections {
		if c.ProfileID == "profile" {
			return c
		}
	}
	t.Fatal("The profile status is absent.")
	return SystemCondition{}
}

func TestModelResponseRecoversBeforeRunEndsAndRetainsLaterFailure(t *testing.T) {
	db, id := recoveryFixture(t)
	before := recoveryStatus(t, db)
	if before.RetryPhase != "active" || len(before.Attempts) != 1 || before.Attempts[0].RunID != id.RunID || before.Attempts[0].State != "starting" {
		t.Fatalf("Attempt status: %+v", before)
	}
	for i := 0; i < 2; i++ {
		result, err := RunModelResponse(db, id, id.RunID)
		if err != nil || result.(map[string]any)["recovered"] != true {
			t.Fatalf("Report %d: %v %v", i, result, err)
		}
	}
	after := recoveryStatus(t, db)
	if after.State != "ready" || after.Recovery == nil || after.Recovery.RunID != id.RunID || after.ResolvedDiagnosticID != before.DiagnosticID {
		t.Fatalf("Recovery status: %+v", after)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM diagnostic_events WHERE operation='model_access' AND outcome='ready'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Recovery records: %d %v", count, err)
	}
	var ended sql.NullString
	if err := db.QueryRow(`SELECT ended_at FROM runs WHERE id=?`, id.RunID).Scan(&ended); err != nil || ended.Valid {
		t.Fatalf("Recovery ended the run: %v %v", ended, err)
	}
	result := networkReport(t, db, id.RunID, domain.InfrastructureFailureReport{Condition: "The next request timed out.", Source: "runner", NetworkError: "timeout"})
	if result.Charged {
		t.Fatal("The network failure used the task allowance.")
	}
	next := recoveryStatus(t, db)
	if next.State != "retry_wait" || next.FailureCount != 1 || next.NetworkError != "timeout" || next.DiagnosticID == before.DiagnosticID {
		t.Fatalf("Later failure: %+v", next)
	}
}

func TestModelResponsePreservesChangedConditions(t *testing.T) {
	for _, change := range []string{
		`UPDATE connection_health SET revision=revision+1 WHERE profile_id='profile'`,
		`UPDATE connection_health SET authorization_id='new' WHERE profile_id=''`,
		`UPDATE connection_health SET profile_revision='new' WHERE profile_id='profile'`,
		`UPDATE connection_health SET state='stopped' WHERE profile_id='profile'`,
	} {
		t.Run(change, func(t *testing.T) {
			db, id := recoveryFixture(t)
			dvExec(t, db, change)
			out, err := RunModelResponse(db, id, id.RunID)
			if err != nil || out.(map[string]any)["recovered"] != false {
				t.Fatalf("Obsolete evidence changed the condition: %v %v", out, err)
			}
			if err := inTx(db, func(ctx context.Context, q Q) error { return domain.ClearNetworkRetry(ctx, q, id.RunID) }); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM diagnostic_events WHERE operation='model_access' AND outcome='ready'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("Terminal recovery bypassed the revision: %d %v", count, err)
			}
		})
	}
}

func TestModelResponseScopeAndIndependentRestrictions(t *testing.T) {
	db, id := recoveryFixture(t)
	for _, caller := range []*RunIdentity{nil, {Tier: "collie", RunID: id.RunID}, {Tier: "pipeline", RunID: "other"}} {
		if _, err := RunModelResponse(db, caller, id.RunID); err == nil {
			t.Fatal("A caller reported another attempt.")
		}
	}
	dvExec(t, db, `INSERT INTO connection_health(connection_id,operation,profile_id,authorization_id,profile_revision,state) VALUES('test','model_access','other','authorization','revision','stopped')`)
	dvExec(t, db, `UPDATE projects SET status='paused' WHERE id='ws-test'`)
	if _, err := RunModelResponse(db, id, id.RunID); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM connection_health WHERE profile_id='other'`).Scan(&state); err != nil || state != "stopped" {
		t.Fatalf("Other profile: %s %v", state, err)
	}
	var paused int
	if err := db.QueryRow(`SELECT status='paused' FROM projects WHERE id='ws-test'`).Scan(&paused); err != nil || paused != 1 {
		t.Fatalf("Pause: %d %v", paused, err)
	}
	if _, err := RunModelResponse(db, id, "prime"); err == nil {
		t.Fatal("A response changed the earlier failed run.")
	}
}

func TestCollieModelResponseRecoveryAndLaterNetworkFailure(t *testing.T) {
	db, _ := recoveryFixture(t)
	session := startedSession(t, db, "dashboard:recovery")
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:recovery", Body: "Hello"}); err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Tier: "collie", RunID: session, RuntimeBinding: PrivateRuntimeBinding{ID: "profile", Credential: "test", AuthorizationID: "authorization", ProfileRevision: "revision", Harness: "codex"}}
	claimed, err := CollieClaim(db, id, session)
	if err != nil {
		t.Fatal(err)
	}
	attempt := claimed.(map[string]any)["message"].(map[string]any)["attempt_id"].(string)
	for _, caller := range []*RunIdentity{nil, {Tier: "pipeline", RunID: session}, {Tier: "collie", RunID: "other"}} {
		if _, err := CollieModelResponse(db, caller, attempt); err == nil {
			t.Fatal("A caller reported another Collie attempt.")
		}
	}
	out, err := CollieModelResponse(db, id, attempt)
	if err != nil || out.(map[string]any)["recovered"] != true {
		t.Fatalf("Collie recovery: %v %v", out, err)
	}
	if c := recoveryStatus(t, db); c.Recovery == nil || c.Recovery.AttemptID != attempt || c.Recovery.State != "active" {
		t.Fatalf("Recovery: %+v", c)
	}
	if _, err := CollieReplyFailure(db, id, attempt, "The next request disconnected.", domain.InfrastructureFailureReport{Source: "runner", NetworkError: "reset"}); err != nil {
		t.Fatal(err)
	}
	c := recoveryStatus(t, db)
	if c.State != "retry_wait" || c.FailureCount != 1 || c.RetryPhase != "delay" {
		t.Fatalf("Later Collie failure: %+v", c)
	}
	if _, err := CollieModelResponse(db, id, attempt); err == nil {
		t.Fatal("An ended attempt supplied recovery.")
	}
	if err := inTx(db, func(ctx context.Context, q Q) error { return domain.ClearNetworkRetry(ctx, q, "replacement") }); err != nil {
		t.Fatal(err)
	}
	if c := recoveryStatus(t, db); c.State != "retry_wait" {
		t.Fatal("A concurrent older run cleared the new Collie failure.")
	}
}

func TestModelResponseRetainsTaskFailureAndTerminalRecovery(t *testing.T) {
	t.Run("later task failure", func(t *testing.T) {
		db, id := recoveryFixture(t)
		if _, err := RunProcessStarted(db, id, id.RunID); err != nil {
			t.Fatal(err)
		}
		if _, err := RunModelResponse(db, id, id.RunID); err != nil {
			t.Fatal(err)
		}
		result := networkReport(t, db, id.RunID, domain.InfrastructureFailureReport{Condition: "The harness failed.", Source: "runner"})
		if !result.Charged {
			t.Fatal("Recovery removed the task failure.")
		}
		if c := recoveryStatus(t, db); c.State != "ready" || c.Recovery == nil || c.Recovery.State == "active" {
			t.Fatalf("Ended attempt: %+v", c)
		}
	})
	t.Run("terminal fallback", func(t *testing.T) {
		db, id := recoveryFixture(t)
		if err := inTx(db, func(ctx context.Context, q Q) error { return endRun(ctx, q, id.RunID, "completed") }); err != nil {
			t.Fatal(err)
		}
		if c := recoveryStatus(t, db); c.State != "ready" || c.Recovery == nil || c.Recovery.State != "completed" {
			t.Fatalf("Terminal recovery: %+v", c)
		}
	})
}

func TestProviderTimeoutKeepsItsActiveAttemptVisible(t *testing.T) {
	for _, role := range []string{"worker", "collie"} {
		t.Run(role, func(t *testing.T) {
			db, id := recoveryFixture(t)
			a := proxyObservation("provider_network", 200)
			a.Connection, a.AuthorizationRevision, a.CurrentAuthorizationRevision = "test", "authorization", "authorization"
			a.ProfileRevision = "revision"
			a.RunID, a.LaunchID = id.RunID, id.RunID
			a.Outcome, a.NetworkError = "interrupted", "timeout"
			if role == "collie" {
				session := startedSession(t, db, "dashboard:timeout")
				if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:timeout", Body: "Hello"}); err != nil {
					t.Fatal(err)
				}
				caller := &RunIdentity{Tier: "collie", RunID: session, RuntimeBinding: PrivateRuntimeBinding{ID: "profile", Credential: "test", AuthorizationID: "authorization", ProfileRevision: "revision", Harness: "codex"}}
				claimed, err := CollieClaim(db, caller, session)
				if err != nil {
					t.Fatal(err)
				}
				a.AttemptID = claimed.(map[string]any)["message"].(map[string]any)["attempt_id"].(string)
				a.RunID, a.LaunchID = "", ""
			}
			if !observeProxy(t, db, a).Applied {
				t.Fatal("The timeout did not change the connection status.")
			}
			for _, retryAt := range []string{"2099-01-01T00:00:00Z", "2000-01-01T00:00:00Z"} {
				dvExec(t, db, `UPDATE connection_health SET next_retry_at=? WHERE profile_id='profile'`, retryAt)
				c := recoveryStatus(t, db)
				if c.RetryPhase != "active" || len(c.Attempts) != 1 || c.Attempts[0].Role != role || c.Attempts[0].RunID != a.RunID || c.Attempts[0].AttemptID != a.AttemptID {
					t.Fatalf("The timeout hid its active attempt or included another attempt: %+v", c)
				}
			}
			c := SystemCondition{State: "retry_wait", DiagnosticID: recoveryStatus(t, db).DiagnosticID}
			scope := ConnectionRequirement{ID: "test", AuthorizationID: "authorization", ProfileID: "profile", ProfileRevision: "other"}
			if err := connectionAttemptStatus(context.Background(), db, scope, &c, time.Now()); err != nil {
				t.Fatal(err)
			}
			if len(c.Attempts) != 0 || c.RetryPhase != "pending" {
				t.Fatalf("A different profile revision appeared as a retry: %+v", c)
			}
		})
	}
}
