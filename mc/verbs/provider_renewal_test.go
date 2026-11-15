package verbs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"mc/domain"
)

func renewalObservation(status int, code, network string) ProviderAccessObservation {
	a := proxyObservation("provider_renewal", status)
	a.Operation, a.Outcome = "token_refresh", "failed"
	a.ProviderErrorCode, a.NetworkError = code, network
	return a
}

func renewalCheck(t *testing.T, db *sql.DB) ConnectionCheck {
	t.Helper()
	a := ConnectionCheck{ConnectionID: "proxy-test", AuthorizationID: "authorization-1", Operation: "token_refresh", CheckID: domain.DiagnosticID()}
	result, err := ConnectionCheckStart(db, nil, a)
	if err != nil || result.(map[string]any)["allowed"] != true {
		t.Fatalf("The renewal check was refused: %v %v", result, err)
	}
	return a
}

func renewalState(t *testing.T, db *sql.DB) (string, int, string) {
	t.Helper()
	var state, retry string
	var count int
	if err := db.QueryRow(`SELECT state,failure_count,COALESCE(next_retry_at,'') FROM connection_health WHERE connection_id='proxy-test' AND operation='token_refresh'`).Scan(&state, &count, &retry); err != nil {
		t.Fatal(err)
	}
	return state, count, retry
}

func TestProviderRenewalTransportFailuresPreserveTheirScheduleAndSingleCharge(t *testing.T) {
	db := proxyHealth(t, "token_refresh")
	for attempt := 1; attempt <= 5; attempt++ {
		check := renewalCheck(t, db)
		a := renewalObservation(0, "", "timeout")
		before := time.Now().UTC()
		observeProxy(t, db, a)
		wrapper, err := ConnectionObserve(db, nil, ConnectionObservation{ConnectionCheck: check,
			EventID: domain.DiagnosticID(), Outcome: "failed", ProviderErrorCode: "renewal_unavailable", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		if err != nil || wrapper.(map[string]any)["applied"] != false {
			t.Fatalf("The wrapper replaced the canonical renewal evidence: %v %v", wrapper, err)
		}
		state, count, retry := renewalState(t, db)
		at, err := time.Parse(time.RFC3339Nano, retry)
		delay := domain.ConnectionRetryDelay(min(attempt, 3))
		if err != nil || state != "retry_wait" || count != min(attempt, 3) || at.Before(before.Add(delay)) || at.After(time.Now().UTC().Add(delay)) {
			t.Fatalf("The transport retry lost its schedule: state=%s count=%d retry=%s", state, count, retry)
		}
		var network string
		var active bool
		if err := db.QueryRow(`SELECT d.network_error,h.request_deadline IS NOT NULL FROM connection_health h JOIN diagnostic_events d ON d.id=h.latest_diagnostic_id WHERE h.connection_id='proxy-test' AND h.operation='token_refresh'`).Scan(&network, &active); err != nil || network != "timeout" || active {
			t.Fatal("The wrapper lost the transport error or retained its reservation.")
		}
		dvExec(t, db, `UPDATE connection_health SET next_retry_at='2000-01-01T00:00:00Z' WHERE connection_id='proxy-test'`)
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, "proxy-test", "profile", time.Now()); err == nil {
		t.Fatal("An overdue renewal retry admitted a launch without successful renewal.")
	}
	check := renewalCheck(t, db)
	renewed := renewalObservation(0, "", "")
	renewed.Outcome = "ready"
	renewed.CredentialRef, renewed.CurrentCredentialRef = "credential-2", "credential-2"
	renewed.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	observeProxy(t, db, renewed)
	wrapper, err := ConnectionObserve(db, nil, ConnectionObservation{ConnectionCheck: check,
		EventID: domain.DiagnosticID(), Outcome: "ready", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ExpiresAt: renewed.ExpiresAt})
	if err != nil || wrapper.(map[string]any)["applied"] != false {
		t.Fatalf("The wrapper replaced the durable renewal result: %v %v", wrapper, err)
	}
	if state, count, retry := renewalState(t, db); state != "ready" || count != 0 || retry != "" {
		t.Fatalf("Successful renewal retained its restriction: %s %d %s", state, count, retry)
	}
	if _, _, err := ConnectionAdmission(context.Background(), db, "proxy-test", "profile", time.Now()); err != nil {
		t.Fatalf("Successful renewal did not restore launch admission: %v", err)
	}
}

func TestProviderRenewalObsoleteEvidencePreservesCurrentHealth(t *testing.T) {
	for _, obsolete := range []string{"authorization", "credential", "time"} {
		t.Run(obsolete, func(t *testing.T) {
			db := proxyHealth(t, "token_refresh")
			observeProxy(t, db, renewalObservation(0, "", "reset"))
			dvExec(t, db, `UPDATE connection_health SET next_retry_at='2000-01-01T00:00:00Z' WHERE connection_id='proxy-test'`)
			check := renewalCheck(t, db)
			state, count, retry := renewalState(t, db)
			for _, outcome := range []string{"ready", "failed"} {
				a := renewalObservation(0, "", "")
				a.Outcome = outcome
				if outcome == "failed" {
					a.ProviderErrorCode = "invalid_grant"
				}
				switch obsolete {
				case "authorization":
					a.AuthorizationRevision = "old-authorization"
				case "credential":
					a.CredentialRef = "old-credential"
				case "time":
					a.ObservedAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
				}
				if observeProxy(t, db, a).Applied {
					t.Fatal("An obsolete renewal changed current health.")
				}
			}
			if _, err := ConnectionCheckCancel(db, nil, check); err != nil {
				t.Fatal(err)
			}
			if current, actual, next := renewalState(t, db); current != state || actual != count || next != retry {
				t.Fatal("The canceled wrapper changed health after obsolete renewal evidence.")
			}
		})
	}
}
