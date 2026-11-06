package verbs

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"mc/domain"
)

func healthCheck(t *testing.T, db *sql.DB, auth string) ConnectionCheck {
	t.Helper()
	a := ConnectionCheck{ConnectionID: "test", AuthorizationID: auth, Operation: "token_refresh", CheckID: domain.DiagnosticID()}
	result, err := ConnectionCheckStart(db, nil, a)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["allowed"] != true {
		t.Fatalf("Check refused: %v", result)
	}
	return a
}
func healthObservation(t *testing.T, db *sql.DB, check ConnectionCheck, outcome, code string, status int) ConnectionObservation {
	t.Helper()
	a := ConnectionObservation{ConnectionCheck: check, EventID: domain.DiagnosticID(), Outcome: outcome, ProviderErrorCode: code, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if status != 0 {
		a.HTTPStatus = &status
	}
	if _, err := ConnectionObserve(db, nil, a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestConnectionFailureRetryLimitAndDuplicate(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	for count := 1; count <= 3; count++ {
		check := healthCheck(t, db, "authorization")
		a := healthObservation(t, db, check, "failed", "server_error", 503)
		if _, err := ConnectionObserve(db, nil, a); err != nil {
			t.Fatal(err)
		}
		var actual int
		var state string
		if err := db.QueryRow(`SELECT failure_count,state FROM connection_health WHERE connection_id='test'`).Scan(&actual, &state); err != nil {
			t.Fatal(err)
		}
		if actual != count || (count == 3) != (state == "stopped") {
			t.Fatalf("count=%d state=%s", actual, state)
		}
		if count < 3 {
			if _, err := db.Exec(`UPDATE connection_health SET next_retry_at='2000-01-01T00:00:00Z'`); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := ConnectionCheckStart(db, nil, ConnectionCheck{ConnectionID: "test", AuthorizationID: "authorization", Operation: "token_refresh", CheckID: domain.DiagnosticID()})
	if err != nil || result.(map[string]any)["allowed"] != false {
		t.Fatalf("Exhausted check: %v %v", result, err)
	}
	var records int
	if err := db.QueryRow(`SELECT COUNT(*) FROM diagnostic_events WHERE outcome='failed'`).Scan(&records); err != nil || records != 3 {
		t.Fatalf("history=%d err=%v", records, err)
	}
}

func TestConnectionRecoveryPreservesHistoryAndRejectsStaleResults(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	old := healthCheck(t, db, "old-authorization")
	healthObservation(t, db, old, "failed", "invalid_grant", 400)
	repaired := healthCheck(t, db, "new-authorization")
	healthObservation(t, db, repaired, "ready", "", 0)
	healthObservation(t, db, old, "failed", "invalid_grant", 400)
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := SystemStatus(db, nil, SystemStatusRequest{Connections: []ConnectionRequirement{{ID: "test", AuthorizationID: "new-authorization", Operation: "token_refresh"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.(SystemStatusResult).Conditions) != 0 {
		t.Fatalf("Recovered alert remains: %+v", raw)
	}
	var history int
	if err := db.QueryRow(`SELECT COUNT(*) FROM diagnostic_events WHERE outcome='failed'`).Scan(&history); err != nil || history != 2 {
		t.Fatalf("history=%d err=%v", history, err)
	}
}

func TestConnectionObservationReplayRetainsTheCommittedResult(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "authorization")
	observation := healthObservation(t, db, check, "ready", "", 0)
	result, err := ConnectionObserve(db, nil, observation)
	if err != nil {
		t.Fatal(err)
	}
	replay := result.(map[string]any)
	if replay["applied"] != true || replay["state"] != "ready" || replay["duplicate"] != true {
		t.Fatalf("Replay lost its result: %v", replay)
	}
}

func TestReauthorizationPreservesRestrictionsWithoutRequiringAnOptionalTest(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "old")
	healthObservation(t, db, check, "ready", "", 0)
	for _, profile := range []string{"passed", "restricted"} {
		outcome := "ready"
		if profile == "restricted" {
			outcome = "failed"
		}
		observation := ConnectionObservation{ConnectionCheck: ConnectionCheck{ConnectionID: "test", AuthorizationID: "old", Operation: "model_access", ProfileID: profile, ProfileRevision: "same", CheckID: domain.DiagnosticID()}, EventID: domain.DiagnosticID(), Outcome: outcome, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if _, err := ConnectionVerified(db, nil, observation); err != nil {
			t.Fatal(err)
		}
	}
	check = healthCheck(t, db, "new")
	healthObservation(t, db, check, "ready", "", 0)
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "passed", time.Now(), "same"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "restricted", time.Now(), "same"); err == nil {
		t.Fatal("Authorization cleared an unverified model restriction.")
	}
	status, err := SystemStatus(db, nil, SystemStatusRequest{Connections: []ConnectionRequirement{{ID: "test", AuthorizationID: "new", Operation: "token_refresh", Profiles: map[string]string{"passed": "same", "restricted": "same"}}}})
	if err != nil {
		t.Fatal(err)
	}
	conditions := status.(SystemStatusResult).Conditions
	if len(conditions) != 1 || conditions[0].ProfileID != "restricted" || conditions[0].State != "unknown" || conditions[0].ProviderErrorCode != "" {
		t.Fatalf("Incorrect current conditions: %+v", conditions)
	}
}

func TestModelFailureAllowsOneRefreshAndPreservesAnotherProfile(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	fail := func(status int) {
		t.Helper()
		_, revision, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var statusValue *int
		code := "authentication_failed"
		if status != 0 {
			statusValue, code = &status, ""
		}
		err = inTx(db, func(ctx context.Context, q Q) error {
			diagnostic, err := domain.AppendDiagnostic(ctx, q, domain.Diagnostic{Source: "runner", Operation: "run", Outcome: "failed", ConnectionID: "test", HTTPStatus: statusValue, ProviderErrorCode: code}, "")
			if err != nil {
				return err
			}
			return domain.RecordConnectionFailure(ctx, q, "test", "authorization", "profile", "revision", revision, diagnostic, domain.InfrastructureFailureReport{HTTPStatus: statusValue, ProviderErrorCode: code})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	fail(0)
	var state string
	if err := db.QueryRow(`SELECT state FROM connection_health WHERE connection_id='test' AND profile_id=''`).Scan(&state); err != nil || state != "unknown" {
		t.Fatalf("First rejection: %s %v", state, err)
	}
	check = healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	check = healthCheck(t, db, "authorization")
	fail(401)
	healthObservation(t, db, check, "ready", "", 0)
	if err := db.QueryRow(`SELECT state FROM connection_health WHERE connection_id='test' AND profile_id=''`).Scan(&state); err != nil || state != "stopped" {
		t.Fatalf("Repeated rejection: %s %v", state, err)
	}
	check = healthCheck(t, db, "replacement")
	healthObservation(t, db, check, "ready", "", 0)
	model := ConnectionObservation{ConnectionCheck: ConnectionCheck{ConnectionID: "test", AuthorizationID: "replacement", Operation: "model_access", ProfileID: "profile", ProfileRevision: "revision", CheckID: domain.DiagnosticID()}, EventID: domain.DiagnosticID(), Outcome: "failed", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := ConnectionVerified(db, nil, model); err != nil {
		t.Fatal(err)
	}
	request := SystemStatusRequest{Connections: []ConnectionRequirement{{ID: "test", AuthorizationID: "replacement", Operation: "token_refresh", Profiles: map[string]string{"profile": "revision", "other": "revision"}, Roles: []string{"worker", "collie"}, ProfileRoles: map[string][]string{"profile": {"worker"}, "other": {"collie"}}}}}
	request.Connections[0].ProfileModels = map[string]string{"profile": "review-model", "other": "general-model"}
	result, err := SystemStatus(db, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	conditions := result.(SystemStatusResult).Conditions
	if len(conditions) != 1 || len(conditions[0].Roles) != 1 || conditions[0].Roles[0] != "worker" || conditions[0].FailureCount != 1 || conditions[0].FirstFailureAt == "" {
		t.Fatalf("Incorrect profile scope: %+v", conditions)
	}
	if conditions[0].Model != "review-model" {
		t.Fatalf("Incorrect model: %+v", conditions[0])
	}
	for _, connection := range result.(SystemStatusResult).Connections {
		if connection.ProfileID == "" && connection.Model != "" {
			t.Fatalf("A connection without a profile includes a model: %+v", connection)
		}
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "other", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestModelRejectionDuringRefreshBlocksAdmissionAndRetainsThreeAttempts(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	_, revision, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	check = healthCheck(t, db, "authorization")
	status := 401
	err = inTx(db, func(ctx context.Context, q Q) error {
		diagnostic, err := domain.AppendDiagnostic(ctx, q, domain.Diagnostic{Source: "runner", Operation: "run", Outcome: "failed", ConnectionID: "test", HTTPStatus: &status}, "")
		if err != nil {
			return err
		}
		return domain.RecordConnectionFailure(ctx, q, "test", "authorization", "profile", "revision", revision, diagnostic, domain.InfrastructureFailureReport{HTTPStatus: &status})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now()); err == nil {
		t.Fatal("A pending refresh hid the model rejection.")
	}
	for count := 1; count <= 3; count++ {
		healthObservation(t, db, check, "failed", "server_error", 503)
		var actual int
		var state string
		if err := db.QueryRow(`SELECT failure_count,state FROM connection_health WHERE connection_id='test'`).Scan(&actual, &state); err != nil {
			t.Fatal(err)
		}
		if actual != count || (state == "stopped") != (count == 3) {
			t.Fatalf("Refresh attempt %d: count=%d state=%s", count, actual, state)
		}
		if count < 3 {
			dvExec(t, db, `UPDATE connection_health SET next_retry_at='2000-01-01T00:00:00Z'`)
			check = healthCheck(t, db, "authorization")
		}
	}
}

func TestModelRejectionBeforeVerifiedRefreshKeepsCurrentConnectionReady(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	_, revision, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	check = healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	status := 401
	err = inTx(db, func(ctx context.Context, q Q) error {
		diagnostic, err := domain.AppendDiagnostic(ctx, q, domain.Diagnostic{Source: "runner", Operation: "run", Outcome: "failed", ConnectionID: "test", HTTPStatus: &status}, "")
		if err != nil {
			return err
		}
		return domain.RecordConnectionFailure(ctx, q, "test", "authorization", "profile", "revision", revision, diagnostic, domain.InfrastructureFailureReport{HTTPStatus: &status})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionObservationAndHistoryRollbackTogether(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "authorization")
	if _, err := db.Exec(`CREATE TRIGGER reject_diagnostic BEFORE INSERT ON diagnostic_events WHEN NEW.outcome='failed' BEGIN SELECT RAISE(ABORT,'test refusal'); END`); err != nil {
		t.Fatal(err)
	}
	a := ConnectionObservation{ConnectionCheck: check, EventID: domain.DiagnosticID(), Outcome: "failed", ProviderErrorCode: "invalid_grant", ObservedAt: time.Now().UTC().Format(time.RFC3339)}
	if _, err := ConnectionObserve(db, nil, a); err == nil {
		t.Fatal("The transaction accepted a missing history record.")
	}
	var count int
	if err := db.QueryRow(`SELECT failure_count FROM connection_health`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestConnectionRefreshPreservesModelRestrictionAndExpiry(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	model := ConnectionCheck{ConnectionID: "test", AuthorizationID: "authorization", Operation: "model_access", ProfileID: "profile", ProfileRevision: "revision", CheckID: domain.DiagnosticID()}
	if _, err := ConnectionCheckStart(db, nil, model); err != nil {
		t.Fatal(err)
	}
	healthObservation(t, db, model, "failed", "permission_denied", 403)
	check = healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now()); err == nil {
		t.Fatal("Refresh cleared the model restriction.")
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "other-profile", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE connection_health SET expires_at='2000-01-01T00:00:00Z' WHERE operation='token_refresh'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "other-profile", time.Now()); err == nil {
		t.Fatal("Expired access allowed dispatch.")
	}
}

func TestCollieReplyFailureRequiresExplicitIdempotentRetry(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	session := startedSession(t, db, "dashboard:dash")
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:dash", Body: "Hello"}); err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Tier: "collie", RunID: session}
	claimed, err := CollieClaim(db, id, session)
	if err != nil {
		t.Fatal(err)
	}
	message := claimed.(map[string]any)["message"].(map[string]any)
	attempt := message["attempt_id"].(string)
	if _, err := CollieReplyFailure(db, id, attempt, "The harness ended without a reply."); err != nil {
		t.Fatal(err)
	}
	next, err := CollieClaim(db, id, session)
	if err != nil || len(next.(map[string]any)["message"].(map[string]any)) != 0 {
		t.Fatalf("Failed attempt repeated: %v %v", next, err)
	}
	var messageID int64
	if err := db.QueryRow(`SELECT message_id FROM collie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	key := domain.DiagnosticID()
	first, err := CollieReplyRetry(db, nil, session, messageID, key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CollieReplyRetry(db, nil, session, messageID, key)
	if err != nil || first.(map[string]any)["attempt_id"] != second.(map[string]any)["attempt_id"] {
		t.Fatalf("Retry duplication: %v %v", second, err)
	}
	if _, err := CollieClaim(db, id, session); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieReply(db, id, CollieReplyArgs{Session: session, To: messageID, Body: "Hello"}); err != nil {
		t.Fatal(err)
	}
	var failures int
	if err := db.QueryRow(`SELECT COUNT(*) FROM collie_reply_attempts WHERE state='failed'`).Scan(&failures); err != nil || failures != 1 {
		t.Fatalf("history=%d err=%v", failures, err)
	}
}

func TestConnectionNetworkFailureRetriesWithoutLimit(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	delays := []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for attempt, delay := range delays {
		check := healthCheck(t, db, "authorization")
		observed := ConnectionObservation{ConnectionCheck: check, EventID: domain.DiagnosticID(), Outcome: "failed", NetworkError: "connect", RequestSent: true, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		before := time.Now().UTC()
		if _, err := ConnectionObserve(db, nil, observed); err != nil {
			t.Fatal(err)
		}
		var state, retry string
		var count int
		if err := db.QueryRow(`SELECT state,failure_count,next_retry_at FROM connection_health WHERE connection_id='test'`).Scan(&state, &count, &retry); err != nil {
			t.Fatal(err)
		}
		at, err := time.Parse(time.RFC3339Nano, retry)
		if err != nil {
			t.Fatal(err)
		}
		if state != "retry_wait" || count != min(attempt+1, 3) || at.Before(before.Add(delay)) || at.After(time.Now().UTC().Add(delay)) {
			t.Fatalf("attempt %d: state=%s count=%d retry=%s", attempt+1, state, count, retry)
		}
		raw, err := SystemStatus(db, nil, SystemStatusRequest{Connections: []ConnectionRequirement{{ID: "test", AuthorizationID: "authorization", Operation: "token_refresh", Roles: []string{"worker"}}}})
		if err != nil {
			t.Fatal(err)
		}
		condition := raw.(SystemStatusResult).Connections[0]
		if condition.Action != "" || condition.NetworkError != "connect" || condition.State != "retry_wait" {
			t.Fatalf("A network failure requested an operator action: %+v", condition)
		}
		dvExec(t, db, `UPDATE connection_health SET next_retry_at='2000-01-01T00:00:00Z'`)
	}
	var class string
	if err := db.QueryRow(`SELECT network_error FROM diagnostic_events WHERE outcome='failed' ORDER BY id DESC LIMIT 1`).Scan(&class); err != nil || class != "connect" {
		t.Fatalf("The diagnostic lost the network error class: %q %v", class, err)
	}
	history, err := DiagnosticHistory(db, nil, "test", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if transport := history.(map[string]any)["refresh_outcomes"].(map[string]any)["day"].(map[string]int)["transport_failures"]; transport != len(delays) {
		t.Fatalf("transport failures=%d", transport)
	}
	check := healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "", time.Now()); err != nil {
		t.Fatalf("A successful retry did not restore admission: %v", err)
	}
}

func TestConnectionNetworkErrorRejectsProviderFields(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "authorization")
	status := 503
	for _, observed := range []ConnectionObservation{
		{ConnectionCheck: check, EventID: domain.DiagnosticID(), Outcome: "failed", NetworkError: "offline", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		{ConnectionCheck: check, EventID: domain.DiagnosticID(), Outcome: "failed", NetworkError: "reset", HTTPStatus: &status, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		{ConnectionCheck: check, EventID: domain.DiagnosticID(), Outcome: "ready", NetworkError: "timeout", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)},
	} {
		if _, err := ConnectionObserve(db, nil, observed); err == nil {
			t.Fatalf("An invalid network observation was accepted: %+v", observed)
		}
	}
}

func TestConnectionObservationAcceptsOnlyAProviderCodeOfIdentifierCharacters(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	status := 401
	check := healthCheck(t, db, "authorization")
	for _, code := range []string{"invalid grant", "code\nline", strings.Repeat("a", 65)} {
		observed := ConnectionObservation{ConnectionCheck: check, EventID: domain.DiagnosticID(), Outcome: "failed", ProviderErrorCode: code, HTTPStatus: &status, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if _, err := ConnectionObserve(db, nil, observed); err == nil {
			t.Fatalf("The observation accepted the provider code %q.", code)
		}
	}
	healthObservation(t, db, check, "failed", "token.expired-"+strings.Repeat("a", 50), 401)
}

func TestConnectionReconnectOnlyForAGrantRejection(t *testing.T) {
	for _, tc := range []struct {
		code     string
		status   int
		failures int
		action   string
	}{
		{"invalid_grant", 400, 1, "Reconnect"},
		{"", 401, 1, "Reconnect"},
		{"refresh_token_reused", 401, 1, "Reconnect"},
		{"invalid_client", 401, 1, "Review connection"},
		{"server_error", 503, 3, "Retry connection"},
		{"invalid_response", 0, 1, "Retry connection"},
	} {
		db := dvSpine(t)
		dvExec(t, db, `DELETE FROM connection_health`)
		var check ConnectionCheck
		for failure := 0; failure < tc.failures; failure++ {
			dvExec(t, db, `UPDATE connection_health SET next_retry_at=NULL`)
			check = healthCheck(t, db, "authorization")
			healthObservation(t, db, check, "failed", tc.code, tc.status)
		}
		raw, err := SystemStatus(db, nil, SystemStatusRequest{Connections: []ConnectionRequirement{{ID: "test", AuthorizationID: "authorization", Operation: "token_refresh", Roles: []string{"worker"}}}})
		if err != nil {
			t.Fatal(err)
		}
		condition := raw.(SystemStatusResult).Connections[0]
		if condition.State != "stopped" || condition.Action != tc.action {
			t.Fatalf("%s %d: state=%s action=%q", tc.code, tc.status, condition.State, condition.Action)
		}
		check.CheckID = domain.DiagnosticID()
		_, err = ConnectionRetry(db, nil, check)
		if (err == nil) != (tc.action == "Retry connection") {
			t.Fatalf("%s %d: retry error=%v", tc.code, tc.status, err)
		}
	}
}

func TestConnectionCheckWithoutAResultRetries(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	healthCheck(t, db, "authorization")
	dvExec(t, db, `UPDATE connection_health SET request_deadline='2000-01-01T00:00:00Z'`)
	result, err := ConnectionCheckStart(db, nil, ConnectionCheck{ConnectionID: "test", AuthorizationID: "authorization", Operation: "token_refresh", CheckID: domain.DiagnosticID()})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["allowed"] != false || result.(map[string]any)["next_retry_at"] == nil {
		t.Fatalf("An unrecorded check did not schedule a retry: %v", result)
	}
	var state string
	var count int
	if err := db.QueryRow(`SELECT state,failure_count FROM connection_health`).Scan(&state, &count); err != nil || state != "retry_wait" || count != 1 {
		t.Fatalf("state=%s count=%d err=%v", state, count, err)
	}
	dvExec(t, db, `UPDATE connection_health SET next_retry_at='2000-01-01T00:00:00Z'`)
	healthCheck(t, db, "authorization")
}

func TestConnectionReservationExpiryStopsAdmissionWithoutAStatusWrite(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	healthCheck(t, db, "authorization")
	dvExec(t, db, `UPDATE connection_health SET request_deadline='2000-01-01T00:00:00Z'`)
	if _, _, err := ConnectionAdmission(context.Background(), db, "test", "profile", time.Now()); err == nil {
		t.Fatal("An expired request allowed dispatch.")
	}
	raw, err := SystemStatus(db, nil, SystemStatusRequest{Connections: []ConnectionRequirement{{ID: "test", AuthorizationID: "authorization", Operation: "token_refresh"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.(SystemStatusResult).Conditions) != 1 {
		t.Fatal("The uncertain request has no notice.")
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM connection_health`).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("Status wrote state: %s %v", state, err)
	}
}

func TestProjectInfrastructureResumeIsIndependentAndIdempotent(t *testing.T) {
	db := dvSpine(t)
	dvExec(t, db, `DELETE FROM connection_health`)
	check := healthCheck(t, db, "authorization")
	healthObservation(t, db, check, "ready", "", 0)
	dvExec(t, db, `UPDATE projects SET infrastructure_failure_count=3,status='paused' WHERE id='ws-test'`)
	resume := ConnectionCheck{CheckID: domain.DiagnosticID(), AuthorizationID: "authorization"}
	if _, err := ProjectInfrastructureResume(db, nil, "ws-test", "test", "profile", resume); err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `UPDATE projects SET infrastructure_failure_count=1 WHERE id='ws-test'`)
	if _, err := ProjectInfrastructureResume(db, nil, "ws-test", "test", "profile", resume); err != nil {
		t.Fatal(err)
	}
	var count int
	var status string
	if err := db.QueryRow(`SELECT infrastructure_failure_count,status FROM projects WHERE id='ws-test'`).Scan(&count, &status); err != nil || count != 1 || status != "paused" {
		t.Fatalf("count=%d status=%s error=%v", count, status, err)
	}
}

func TestCollieRejectsTheResultOfAnEarlierAttempt(t *testing.T) {
	db := dvSpine(t)
	session := startedSession(t, db, "dashboard:dash")
	if _, err := CollieSend(db, nil, CollieSendArgs{Session: session, From: "dashboard:dash", Body: "Hello"}); err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Tier: "collie", RunID: session}
	claimed, err := CollieClaim(db, id, session)
	if err != nil {
		t.Fatal(err)
	}
	attempt := claimed.(map[string]any)["message"].(map[string]any)["attempt_id"].(string)
	var message int64
	if err := db.QueryRow(`SELECT message_id FROM collie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieReplyFailure(db, id, attempt, "The harness stopped."); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieReplyRetry(db, nil, session, message, domain.DiagnosticID()); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieClaim(db, id, session); err != nil {
		t.Fatal(err)
	}
	if _, err := CollieReply(db, id, CollieReplyArgs{Session: session, To: message, Body: "Late reply", Attempt: attempt}); err == nil {
		t.Fatal("The earlier attempt completed a newer attempt.")
	}
	var replies int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_messages WHERE direction='reply'`).Scan(&replies); err != nil || replies != 0 {
		t.Fatalf("replies=%d error=%v", replies, err)
	}
}

func TestConnectionCheckCancelPreservesProviderEvidence(t *testing.T) {
	for _, operation := range []string{"token_refresh", "connection_access", "model_access"} {
		t.Run(operation, func(t *testing.T) {
			db := dvSpine(t)
			dvExec(t, db, `DELETE FROM connection_health`)
			a := ConnectionCheck{ConnectionID: "test", AuthorizationID: "current", Operation: operation, CheckID: domain.DiagnosticID()}
			if operation == "model_access" {
				a.ProfileID, a.ProfileRevision = "profile", "revision"
			}
			if _, err := ConnectionCheckStart(db, nil, a); err != nil {
				t.Fatal(err)
			}
			healthObservation(t, db, a, "ready", "", 0)
			a.CheckID = domain.DiagnosticID()
			if _, err := ConnectionCheckStart(db, nil, a); err != nil {
				t.Fatal(err)
			}
			read := func() string {
				t.Helper()
				var state string
				if err := db.QueryRow(`SELECT json_object('state',state,'revision',revision,'failure_count',failure_count,'first_failure_at',first_failure_at,'latest_failure_at',latest_failure_at,'next_retry_at',next_retry_at,'verified_at',verified_at,'expires_at',expires_at,'latest_diagnostic_id',latest_diagnostic_id) FROM connection_health WHERE connection_id='test'`).Scan(&state); err != nil {
					t.Fatal(err)
				}
				return state
			}
			before := read()
			stale := a
			stale.AuthorizationID = "old"
			result, err := ConnectionCheckCancel(db, nil, stale)
			if err != nil || result.(map[string]any)["cleared"] != false {
				t.Fatalf("Stale cancellation: %v %v", result, err)
			}
			result, err = ConnectionCheckCancel(db, nil, a)
			if err != nil || result.(map[string]any)["cleared"] != true {
				t.Fatalf("Cancellation: %v %v", result, err)
			}
			if after := read(); before != after {
				t.Fatalf("Provider evidence changed: %s -> %s", before, after)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM connection_health WHERE check_id='' AND request_deadline IS NULL`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("Reservation remains: %d %v", count, err)
			}
			result, err = ConnectionCheckCancel(db, nil, a)
			if err != nil || result.(map[string]any)["cleared"] != false {
				t.Fatalf("Repeated cancellation: %v %v", result, err)
			}
			healthObservation(t, db, a, "failed", "invalid_grant", 401)
			if after := read(); before != after {
				t.Fatalf("Late failure changed provider evidence: %s", after)
			}
			a.CheckID = domain.DiagnosticID()
			result, err = ConnectionCheckStart(db, nil, a)
			if err != nil || result.(map[string]any)["allowed"] != true {
				t.Fatalf("New check refused: %v %v", result, err)
			}
		})
	}
}
