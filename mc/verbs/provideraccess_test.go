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

func proxyHealth(t *testing.T, operation string) *sql.DB {
	t.Helper()
	db := dvSpine(t)
	dvExec(t, db, `INSERT INTO connection_health(connection_id,operation,authorization_id,state,verified_at) VALUES('proxy-test',?,'authorization-1','ready',datetime('now'))`, operation)
	dvExec(t, db, `INSERT INTO connection_health(connection_id,operation,profile_id,authorization_id,profile_revision,state,verified_at) VALUES('proxy-test','model_access','profile','authorization-1','profile-revision','ready',datetime('now'))`)
	return db
}

func proxyObservation(origin string, status int) ProviderAccessObservation {
	a := ProviderAccessObservation{RequestID: domain.DiagnosticID(), AuthorizationID: "launch-authorization", Connection: "proxy-test",
		RunID: "proxy-run", LaunchID: "proxy-launch", AuthorizationRevision: "authorization-1", CredentialRef: "credential-1",
		ProfileID: "profile", ProfileRevision: "profile-revision", Origin: origin, Outcome: "response_complete", Operation: "model",
		ProviderSent:                 origin == "provider" || origin == "provider_network" || origin == "provider_renewal",
		CurrentAuthorizationRevision: "authorization-1", CurrentCredentialRef: "credential-1", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if status != 0 {
		a.HTTPStatus = &status
	}
	return a
}

func observeProxy(t *testing.T, db *sql.DB, a ProviderAccessObservation) domain.ProviderAccessResult {
	t.Helper()
	result, err := ProviderAccessObserve(db, nil, a)
	if err != nil {
		t.Fatal(err)
	}
	return result.(domain.ProviderAccessResult)
}

func proxyState(t *testing.T, db *sql.DB, profile string) string {
	t.Helper()
	var state string
	if err := db.QueryRow(`SELECT state FROM connection_health WHERE connection_id='proxy-test' AND profile_id=?`, profile).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestProviderAccessLocalFailuresPreserveProviderAuthorization(t *testing.T) {
	for _, origin := range []string{"proxy_authorization", "proxy_limit", "proxy", "credential_store", "client"} {
		t.Run(origin, func(t *testing.T) {
			db := proxyHealth(t, "connection_access")
			a := proxyObservation(origin, 401)
			a.Outcome = "rejected"
			result := observeProxy(t, db, a)
			if result.Applied || proxyState(t, db, "") != "ready" || proxyState(t, db, "profile") != "ready" {
				t.Fatal("A host failure changed provider authorization or model access.")
			}
			var body string
			if err := db.QueryRow(`SELECT submission FROM diagnostic_events WHERE id=?`, result.DiagnosticID).Scan(&body); err != nil || !strings.Contains(body, `"origin":"`+origin+`"`) {
				t.Fatalf("The trusted origin is absent: %v", err)
			}
		})
	}
}

func TestProviderAccessRejectsObsoleteAuthorizationAndCredentialResults(t *testing.T) {
	for _, obsolete := range []string{"authorization", "credential", "profile"} {
		t.Run(obsolete, func(t *testing.T) {
			db := proxyHealth(t, "connection_access")
			a := proxyObservation("provider", 403)
			a.ProviderErrorCode = "permission_denied"
			switch obsolete {
			case "authorization":
				a.CurrentAuthorizationRevision = "authorization-2"
			case "credential":
				a.CurrentCredentialRef = "credential-2"
			case "profile":
				a.ProfileRevision = "obsolete-profile"
			}
			if observeProxy(t, db, a).Applied || proxyState(t, db, "profile") != "ready" {
				t.Fatal("An obsolete request changed current model access.")
			}
		})
	}
}

func TestProviderAccessAPIKeyRejectionAndRepairAction(t *testing.T) {
	db := proxyHealth(t, "connection_access")
	a := proxyObservation("provider", 401)
	a.ProviderErrorCode = "invalid_api_key"
	if !observeProxy(t, db, a).Applied || proxyState(t, db, "") != "stopped" {
		t.Fatal("The current API key rejection did not hold new requests.")
	}
	status, err := SystemStatus(db, nil, SystemStatusRequest{Connections: []ConnectionRequirement{{ID: "proxy-test", Kind: "api_key", Operation: "connection_access", AuthorizationID: "authorization-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	conditions := status.(SystemStatusResult).Conditions
	if len(conditions) != 1 || conditions[0].Action != "Replace API key" || conditions[0].Origin != "provider" || conditions[0].RequestID != a.RequestID {
		t.Fatalf("The repair does not match key rejection: %+v", conditions)
	}
}

func TestProviderAccessReplacementKeyTestRetainsRefusalAfterTheCheckCloses(t *testing.T) {
	db := proxyHealth(t, "connection_access")
	check := ConnectionCheck{ConnectionID: "proxy-test", Operation: "connection_access",
		AuthorizationID: "authorization-2", CheckID: domain.DiagnosticID()}
	started, err := ConnectionCheckStart(db, nil, check)
	if err != nil || started.(map[string]any)["allowed"] != true || proxyState(t, db, "") != "unknown" {
		t.Fatalf("The replacement key test did not start: %v %v", started, err)
	}
	a := proxyObservation("provider", 401)
	a.AuthorizationRevision, a.CurrentAuthorizationRevision = check.AuthorizationID, check.AuthorizationID
	a.CredentialRef, a.CurrentCredentialRef, a.CurrentCredentialType = "credential-2", "credential-2", "api_key"
	if !observeProxy(t, db, a).Applied {
		t.Fatal("The replacement key refusal lost its diagnostic.")
	}
	if _, err := ConnectionCheckCancel(db, nil, check); err != nil {
		t.Fatal(err)
	}
	stale := proxyObservation("provider", 401)
	stale.CurrentAuthorizationRevision, stale.CurrentCredentialRef = check.AuthorizationID, "credential-2"
	stale.CurrentCredentialType = "api_key"
	if observeProxy(t, db, stale).Applied {
		t.Fatal("The old key refusal changed the replacement key condition.")
	}
	status, err := SystemStatus(db, nil, SystemStatusRequest{Connections: []ConnectionRequirement{{
		ID: check.ConnectionID, Kind: "api_key", Operation: check.Operation, AuthorizationID: check.AuthorizationID,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	conditions := status.(SystemStatusResult).Conditions
	if len(conditions) != 1 || conditions[0].State != "stopped" || conditions[0].Action != "Replace API key" || conditions[0].RequestID != a.RequestID {
		t.Fatalf("The replacement key refusal lost its action: %+v", conditions)
	}
}

func TestProviderAccessFirstRefusalCreatesTheCurrentAccountCondition(t *testing.T) {
	for _, kind := range []string{"api_key", "account"} {
		for _, quota := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "credential", true: "quota"}[quota], func(t *testing.T) {
				db := dvSpine(t)
				a := proxyObservation("provider", 401)
				a.CurrentCredentialType = kind
				if quota {
					status := 429
					a.HTTPStatus, a.ProviderErrorCode = &status, "insufficient_quota"
				}
				result := observeProxy(t, db, a)
				state, operation := "stopped", "connection_access"
				if kind == "account" {
					operation = "token_refresh"
					if !quota {
						state = "unknown"
					}
				}
				if !result.Applied || proxyState(t, db, "") != state {
					t.Fatal("The first provider refusal lost its account condition")
				}
				var stored, verified string
				if err := db.QueryRow(`SELECT operation,COALESCE(verified_at,'') FROM connection_health WHERE connection_id='proxy-test' AND profile_id=''`).Scan(&stored, &verified); err != nil || stored != operation || verified != "" {
					t.Fatal("The first refusal inferred verification or changed the credential type")
				}
				var count int
				if err := db.QueryRow(`SELECT COUNT(*) FROM connection_health WHERE profile_id<>''`).Scan(&count); err != nil || count != 0 {
					t.Fatal("An account refusal created a model restriction")
				}
			})
		}
	}
}

func TestProviderAccessFirstRefusalCannotReplaceANewerAccountRow(t *testing.T) {
	db := proxyHealth(t, "connection_access")
	dvExec(t, db, `UPDATE connection_health SET authorization_id='authorization-2' WHERE profile_id=''`)
	a := proxyObservation("provider", 401)
	a.CurrentCredentialType = "api_key"
	if observeProxy(t, db, a).Applied || proxyState(t, db, "") != "ready" {
		t.Fatal("A delayed host snapshot changed a newer account authorization")
	}
	var authorization string
	if err := db.QueryRow(`SELECT authorization_id FROM connection_health WHERE connection_id='proxy-test' AND profile_id=''`).Scan(&authorization); err != nil || authorization != "authorization-2" {
		t.Fatal("A delayed host snapshot replaced the newer account authorization")
	}
}

func TestProviderAccessTokenRejectionRequiresRenewalBeforeReconnection(t *testing.T) {
	db := proxyHealth(t, "token_refresh")
	a := proxyObservation("provider", 401)
	observeProxy(t, db, a)
	if proxyState(t, db, "") != "unknown" {
		t.Fatal("An access-token rejection did not request renewal.")
	}
	renewed := proxyObservation("provider_renewal", 0)
	renewed.Operation, renewed.Outcome = "token_refresh", "ready"
	renewed.CredentialRef, renewed.CurrentCredentialRef = "credential-2", "credential-2"
	renewed.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	observeProxy(t, db, renewed)
	a.RequestID = domain.DiagnosticID()
	a.CurrentCredentialRef = "credential-2"
	if observeProxy(t, db, a).Applied || proxyState(t, db, "") != "ready" {
		t.Fatal("An obsolete access-token rejection damaged the renewed session.")
	}
	rejected := proxyObservation("provider_renewal", 400)
	rejected.CredentialRef, rejected.CurrentCredentialRef = "credential-2", "credential-2"
	rejected.Operation, rejected.Outcome, rejected.ProviderErrorCode = "token_refresh", "failed", "invalid_grant"
	observeProxy(t, db, rejected)
	if proxyState(t, db, "") != "stopped" {
		t.Fatal("Rejected account authorization did not require reconnection.")
	}
}

func TestProviderAccessQuotaAndTransientRateLimitsHaveDifferentScope(t *testing.T) {
	for _, code := range []string{"insufficient_quota", "rate_limit_error"} {
		t.Run(code, func(t *testing.T) {
			db := proxyHealth(t, "token_refresh")
			a := proxyObservation("provider", 429)
			a.ProviderErrorCode = code
			a.RetryAfter = time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)
			observeProxy(t, db, a)
			if code == "insufficient_quota" {
				if proxyState(t, db, "") != "stopped" || proxyState(t, db, "profile") != "ready" {
					t.Fatal("The quota failure did not retain account scope.")
				}
				renewed := proxyObservation("provider_renewal", 0)
				renewed.Operation, renewed.Outcome = "token_refresh", "ready"
				observeProxy(t, db, renewed)
				if proxyState(t, db, "") != "stopped" {
					t.Fatal("Token renewal cleared an account quota failure.")
				}
			} else {
				if proxyState(t, db, "") != "ready" || proxyState(t, db, "profile") != "retry_wait" {
					t.Fatal("A transient rate limit changed account authorization.")
				}
				var retry string
				if err := db.QueryRow(`SELECT next_retry_at FROM connection_health WHERE connection_id='proxy-test' AND profile_id='profile'`).Scan(&retry); err != nil || retry != a.RetryAfter {
					t.Fatalf("The provider retry time changed: %s %v", retry, err)
				}
			}
		})
	}
}

func TestProviderAccessStreamErrorsKeepTheirScopeAtHTTP200(t *testing.T) {
	for _, code := range []string{"overloaded_error", "rate_limit_error", "provider_stream_error", "authentication_error", "insufficient_quota"} {
		t.Run(code, func(t *testing.T) {
			db := proxyHealth(t, "connection_access")
			observation := proxyObservation("provider", 200)
			observation.Outcome = "interrupted"
			observation.ProviderErrorCode = code
			if !observeProxy(t, db, observation).Applied {
				t.Fatal("The stream error did not affect the current connection.")
			}
			if code == "authentication_error" || code == "insufficient_quota" {
				if proxyState(t, db, "") != "stopped" || proxyState(t, db, "profile") != "ready" {
					t.Fatal("The account stream error lost connection scope.")
				}
			} else if proxyState(t, db, "") != "ready" || proxyState(t, db, "profile") != "retry_wait" {
				t.Fatal("The transient stream error changed account authorization.")
			}
		})
	}
}

func TestProviderAccessSharedServiceRecoveryPreservesIndependentAccountFailure(t *testing.T) {
	db := proxyHealth(t, "connection_access")
	account := proxyObservation("provider", 401)
	observeProxy(t, db, account)
	service := proxyObservation("proxy", 503)
	service.Connection, service.Operation, service.Outcome = "", "service", "failed"
	observeProxy(t, db, service)
	if err := ProviderAccessAdmission(context.Background(), db, "other-connection"); err == nil {
		t.Fatal("A shared proxy failure did not hold another connection.")
	}
	service.RequestID, service.Outcome, service.HTTPStatus, service.ObservedAt = domain.DiagnosticID(), "ready", nil, time.Now().UTC().Format(time.RFC3339Nano)
	observeProxy(t, db, service)
	if err := ProviderAccessAdmission(context.Background(), db, "other-connection"); err != nil {
		t.Fatal(err)
	}
	if proxyState(t, db, "") != "stopped" {
		t.Fatal("Proxy recovery cleared an independent account rejection.")
	}
}

func TestProviderAccessKeychainRecoveryPreservesSavedAuthorization(t *testing.T) {
	db := proxyHealth(t, "connection_access")
	a := proxyObservation("credential_store", 503)
	a.Operation, a.Outcome = "service", "failed"
	observeProxy(t, db, a)
	if err := ProviderAccessAdmission(context.Background(), db, "proxy-test"); err == nil {
		t.Fatal("The unavailable credential store did not hold its connection.")
	}
	if err := ProviderAccessAdmission(context.Background(), db, "independent"); err != nil {
		t.Fatal("The credential-store failure affected an independent connection.")
	}
	a.RequestID, a.Outcome, a.HTTPStatus, a.ObservedAt = domain.DiagnosticID(), "ready", nil, time.Now().UTC().Format(time.RFC3339Nano)
	observeProxy(t, db, a)
	if err := ProviderAccessAdmission(context.Background(), db, "proxy-test"); err != nil || proxyState(t, db, "") != "ready" {
		t.Fatalf("Credential-store recovery required reconnection: %v", err)
	}
}

func TestProviderAccessObservationIsIdempotentAndRejectsConflictingOrigin(t *testing.T) {
	db := proxyHealth(t, "connection_access")
	a := proxyObservation("provider", 403)
	a.ProviderErrorCode = "permission_denied"
	first := observeProxy(t, db, a)
	a.CurrentCredentialRef = "credential-replaced-after-observation"
	replay := observeProxy(t, db, a)
	if !replay.Duplicate || replay.DiagnosticID != first.DiagnosticID {
		t.Fatal("A replay changed the immutable request observation.")
	}
	a.Origin = "proxy_limit"
	if _, err := ProviderAccessObserve(db, nil, a); err == nil {
		t.Fatal("A conflicting origin replaced trusted evidence.")
	}
	var count int
	if err := db.QueryRow(`SELECT failure_count FROM connection_health WHERE connection_id='proxy-test' AND profile_id='profile'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Duplicate evidence changed the failure count: %d %v", count, err)
	}
}

func proxyRun(t *testing.T, db *sql.DB) *RunIdentity {
	t.Helper()
	dvInsertTask(t, db, dispatch.Task{ID: 901, Title: "Proxy diagnostic task", Scope: dispatch.ScopeTask, Status: dispatch.StatusSeeded, Project: "ws-test", CreatedAt: time.Now(), DispatchRetries: 3})
	id := fhClaim(t, db, 901, "proxy-run", "worker")
	dvExec(t, db, `UPDATE runs SET connection_id='proxy-test',authorization_id='authorization-1',profile_id='profile',profile_revision='profile-revision' WHERE id='proxy-run'`)
	return id
}

func TestProviderAccessReorderedReportsPreserveTaskAllowanceAndOrigin(t *testing.T) {
	for _, hostFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "host-first", false: "runner-first"}[hostFirst], func(t *testing.T) {
			db := proxyHealth(t, "connection_access")
			id := proxyRun(t, db)
			a := proxyObservation("proxy_authorization", 401)
			a.Outcome = "rejected"
			if hostFirst {
				observeProxy(t, db, a)
			}
			status := 401
			report := domain.InfrastructureFailureReport{Source: "runner", Condition: "The request failed.", ProxyRequestID: a.RequestID, HTTPStatus: &status}
			result := fhReport(t, db, id, report)
			if result["charged"] != false || result["remaining"] != 3 {
				t.Fatalf("A host failure consumed the task allowance: %+v", result)
			}
			if !hostFirst {
				observeProxy(t, db, a)
			}
			if again := fhReport(t, db, id, report); again["charged"] != false {
				t.Fatal("A duplicate report consumed the task allowance.")
			}
			if proxyState(t, db, "") != "ready" {
				t.Fatal("The runner misclassified a local 401 as provider authorization failure.")
			}
		})
	}
}

func TestProviderAccessNativeSuccessClearsOnlyItsTransientRestriction(t *testing.T) {
	db := proxyHealth(t, "connection_access")
	proxyRun(t, db)
	a := proxyObservation("provider", 503)
	observeProxy(t, db, a)
	if proxyState(t, db, "profile") != "retry_wait" {
		t.Fatal("The transient provider failure did not retain a retry state.")
	}
	transport := proxyObservation("provider", 200)
	observeProxy(t, db, transport)
	if proxyState(t, db, "profile") != "retry_wait" {
		t.Fatal("HTTP delivery alone cleared the model failure.")
	}
	if err := inTx(db, func(ctx context.Context, q Q) error {
		ok, err := domain.RecoverNetworkAttempt(ctx, q, "proxy-run", "")
		if err == nil && !ok {
			t.Error("A native model response did not clear its temporary provider failure.")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if proxyState(t, db, "profile") != "ready" {
		t.Fatal("The successful retry retained a permanent model restriction.")
	}
	late := proxyObservation("provider", 429)
	late.ObservedAt = transport.ObservedAt
	late.ProviderErrorCode = "rate_limit_error"
	retained := observeProxy(t, db, late)
	if retained.Applied || proxyState(t, db, "profile") != "ready" {
		t.Fatal("A delayed failure replaced newer native recovery.")
	}
	if retained.DiagnosticID == 0 {
		t.Fatal("The delayed failure lost its diagnostic evidence.")
	}
	var raw string
	if err := db.QueryRow(`SELECT d.submission FROM connection_health h JOIN diagnostic_events d ON d.id=h.latest_diagnostic_id WHERE h.connection_id='proxy-test' AND h.profile_id='profile'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var recovered map[string]any
	if json.Unmarshal([]byte(raw), &recovered) != nil || recovered["resolved_diagnostic_id"] == nil {
		t.Fatal("The recovery lost the diagnostic that it resolved.")
	}
}

func TestProviderAccessDelayedConnectionEvidencePreservesNewerHealth(t *testing.T) {
	for _, operation := range []string{"connection_access", "token_refresh"} {
		t.Run(operation, func(t *testing.T) {
			db := proxyHealth(t, operation)
			old := proxyObservation("provider", 401)
			old.ObservedAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
			if operation == "token_refresh" {
				old.Origin = "provider_renewal"
				old.Operation = "token_refresh"
				old.Outcome = "failed"
				old.ProviderErrorCode = "invalid_grant"
			}
			if observeProxy(t, db, old).Applied || proxyState(t, db, "") != "ready" {
				t.Fatal("A delayed credential failure changed newer verified health.")
			}
			newer := proxyObservation("provider", 401)
			observeProxy(t, db, newer)
			if operation == "token_refresh" {
				lateSuccess := old
				lateSuccess.RequestID = domain.DiagnosticID()
				lateSuccess.Outcome = "ready"
				lateSuccess.HTTPStatus = nil
				lateSuccess.ProviderErrorCode = ""
				if observeProxy(t, db, lateSuccess).Applied || proxyState(t, db, "") != "unknown" {
					t.Fatal("A delayed renewal success cleared a newer access failure.")
				}
			}
		})
	}
}

func TestProviderAccessConversationErrorsPreserveSharedHealth(t *testing.T) {
	for _, code := range []string{"invalid_encrypted_content", "invalid_signature", "invalid_request_error", "invalid_request", ""} {
		t.Run("code="+code, func(t *testing.T) {
			db := proxyHealth(t, "connection_access")
			id := proxyRun(t, db)
			snapshot := func() string {
				var result string
				if err := db.QueryRow(`SELECT json_group_array(json_object('operation',operation,'state',state,'verified',verified_at,'failures',failure_count,'revision',revision,'diagnostic',latest_diagnostic_id)) FROM connection_health`).Scan(&result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			before := snapshot()
			a := proxyObservation("provider", 400)
			a.ProviderErrorCode, a.CurrentCredentialType = code, "api_key"
			first := observeProxy(t, db, a)
			if first.Applied || !observeProxy(t, db, a).Duplicate || snapshot() != before {
				t.Fatal("A conversation error changed shared connection evidence")
			}
			if err := ProviderAccessAdmission(context.Background(), db, a.Connection); err != nil {
				t.Fatal(err)
			}
			result := fhReport(t, db, id, domain.InfrastructureFailureReport{Source: "runner", Condition: "The request failed.", ProxyRequestID: a.RequestID})
			if result["charged"] != true || result["remaining"] != 2 {
				t.Fatalf("The request lost its task allowance: %v", result)
			}
			if again := fhReport(t, db, id, domain.InfrastructureFailureReport{Source: "runner", Condition: "The request failed.", ProxyRequestID: a.RequestID}); again["charged"] != false {
				t.Fatal("A duplicate charged twice")
			}
			if snapshot() != before {
				t.Fatal("The run report changed shared connection evidence")
			}
			var stored, condition string
			if err := db.QueryRow(`SELECT provider_error_code,condition FROM diagnostic_events WHERE id=?`, first.DiagnosticID).Scan(&stored, &condition); err != nil {
				t.Fatal(err)
			}
			if stored != code || strings.Contains(condition, "connection") {
				t.Fatalf("The request lost its cause: %s %s", stored, condition)
			}
		})
	}
}

func TestProviderAccessConversationErrorsRetainReplyIdentityAndPermitOtherWork(t *testing.T) {
	for _, code := range []string{"invalid_encrypted_content", "invalid_request_error"} {
		t.Run(code, func(t *testing.T) {
			db, session, id, _ := collieRuntimeChangeFixture(t)
			claimed, err := CollieClaim(db, id, session)
			if err != nil {
				t.Fatal(err)
			}
			attempt := claimed.(map[string]any)["message"].(map[string]any)["attempt_id"].(string)
			a := proxyObservation("provider", 400)
			a.RunID, a.LaunchID, a.ProviderErrorCode = session, id.Launch, code
			control, err := ProviderAccessLifetimes(db, nil, []ProviderLaunchIdentity{{RunID: session, LaunchID: id.Launch}})
			if err != nil {
				t.Fatal(err)
			}
			current := control.(map[string]any)["launches"].([]ProviderLaunchLifetime)[0]
			if !current.Allowed || current.AttemptID != attempt {
				t.Fatalf("The request received another attempt: %+v", current)
			}
			a.AttemptID = current.AttemptID
			result := observeProxy(t, db, a)
			var linked string
			if err := db.QueryRow(`SELECT reply_attempt_id FROM diagnostic_events WHERE id=?`, result.DiagnosticID).Scan(&linked); err != nil || linked != attempt {
				t.Fatalf("The request lost its reply attempt: %s %v", linked, err)
			}
			if _, err := CollieReplyFailure(db, id, attempt, "The request failed.", domain.InfrastructureFailureReport{ProxyRequestID: a.RequestID}); err != nil {
				t.Fatal(err)
			}
			var actual, condition string
			if err := db.QueryRow(`SELECT d.provider_error_code,d.condition FROM collie_reply_attempts a JOIN diagnostic_events d ON d.id=a.latest_diagnostic_id WHERE a.attempt_id=?`, attempt).Scan(&actual, &condition); err != nil || actual != code {
				t.Fatalf("The reply lost its specific cause: %s %v", actual, err)
			}
			if strings.Contains(condition, "connection") || proxyState(t, db, "") != "ready" || proxyState(t, db, "profile") != "ready" {
				t.Fatal("A reply error changed shared access")
			}
			if _, _, err := ConnectionAdmission(context.Background(), db, "proxy-test", "profile", time.Now(), "profile-revision"); err != nil {
				t.Fatal("The request stopped pipeline admission:", err)
			}
			other := startedSession(t, db, "dashboard:other")
			if _, err := CollieSend(db, nil, CollieSendArgs{Session: other, From: "dashboard:other", Body: "Independent message."}); err != nil {
				t.Fatal(err)
			}
			peer := &RunIdentity{Tier: "collie", RunID: other, RuntimeBinding: id.RuntimeBinding}
			if _, err := CollieClaim(db, peer, other); err != nil {
				t.Fatal("The request stopped another reply:", err)
			}
			status, err := SystemStatus(db, nil, SystemStatusRequest{})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, issue := range status.(SystemStatusResult).Conditions {
				if issue.Action == "Review connection" {
					t.Fatal("A reply error requested connection repair")
				}
				if issue.SessionID == session {
					found = issue.Action == "Retry reply"
				}
			}
			if !found {
				t.Fatal("The failed reply has no reply action")
			}
		})
	}
}
