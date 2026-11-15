package domain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

var proxyRequestIdentityPattern = regexp.MustCompile(`^(?:[0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

type ProviderAccessObservation struct {
	RequestID                    string `json:"request_id"`
	AuthorizationID              string `json:"authorization_id,omitempty"`
	RunID                        string `json:"run_id,omitempty"`
	LaunchID                     string `json:"launch_id,omitempty"`
	Connection                   string `json:"connection,omitempty"`
	AuthorizationRevision        string `json:"authorization_revision,omitempty"`
	CredentialRef                string `json:"credential_ref,omitempty"`
	ProfileID                    string `json:"profile_id,omitempty"`
	ProfileRevision              string `json:"profile_revision,omitempty"`
	AttemptID                    string `json:"attempt_id,omitempty"`
	Operation                    string `json:"operation,omitempty"`
	Origin                       string `json:"origin"`
	Outcome                      string `json:"outcome"`
	ProviderSent                 bool   `json:"provider_sent"`
	HTTPStatus                   *int   `json:"http_status,omitempty"`
	ProviderErrorCode            string `json:"provider_error_code,omitempty"`
	ProviderRequestID            string `json:"provider_request_id,omitempty"`
	RetryAfter                   string `json:"retry_after,omitempty"`
	ExpiresAt                    string `json:"expires_at,omitempty"`
	NetworkError                 string `json:"network_error,omitempty"`
	ObservedAt                   string `json:"observed_at"`
	DurationMS                   int64  `json:"duration_ms"`
	CurrentAuthorizationRevision string `json:"current_authorization_revision,omitempty"`
	CurrentCredentialRef         string `json:"current_credential_ref,omitempty"`
	CurrentCredentialType        string `json:"current_credential_type,omitempty"`
}

type ProviderAccessResult struct {
	DiagnosticID int64  `json:"diagnostic_id"`
	RequestID    string `json:"request_id"`
	Duplicate    bool   `json:"duplicate"`
	Applied      bool   `json:"applied"`
}

func providerDiagnosticID(request string) string {
	sum := sha256.Sum256([]byte("provider-request:" + request))
	return hex.EncodeToString(sum[:16])
}

func providerObservationFailed(a ProviderAccessObservation) bool {
	return a.Outcome == "failed" || a.Outcome == "rejected" || a.Outcome == "interrupted" ||
		a.HTTPStatus != nil && *a.HTTPStatus >= 400 || a.ProviderErrorCode != ""
}

func providerCondition(a ProviderAccessObservation) (string, string) {
	switch a.Origin {
	case "proxy_authorization":
		return "The container launch lost provider access.", "Restore provider access"
	case "proxy_limit":
		return "The request reached a Culler proxy limit.", "Review request limits"
	case "proxy":
		return "The host provider proxy is unavailable.", "Restore provider access"
	case "credential_store":
		return "The host cannot read the connection credential.", "Restore credential access"
	case "provider_network":
		return "The host cannot complete the provider request.", "Restore provider access"
	case "provider_renewal":
		if a.ProviderErrorCode == "invalid_grant" || a.ProviderErrorCode == "access_denied" {
			return "The provider rejected the account authorization.", "Reconnect"
		}
		return "The host could not renew provider access.", "Retry connection"
	case "client":
		return "The client ended the provider request.", "Retry reply"
	}
	if providerRequestLocal(a) {
		switch a.ProviderErrorCode {
		case "invalid_encrypted_content":
			return "The provider rejected the conversation's encrypted content.", ""
		case "invalid_signature":
			return "The provider rejected the conversation's signature.", ""
		}
		return "The provider rejected this request.", ""
	}
	if a.HTTPStatus != nil && *a.HTTPStatus == 401 || a.ProviderErrorCode == "invalid_api_key" || a.ProviderErrorCode == "authentication_error" || a.ProviderErrorCode == "authentication_failed" {
		return "The provider rejected the request credential.", "Review connection"
	}
	switch a.ProviderErrorCode {
	case "insufficient_quota", "billing_error", "credit_balance_exhausted", "usage_limit_reached":
		return "The provider account has no available allowance.", "Review account allowance"
	case "model_not_found", "permission_denied", "permission_error", "not_found_error", "oauth_org_not_allowed", "access_denied":
		return "The provider refused the selected model.", "Review connection"
	}
	if providerTransient(a) {
		return "The provider temporarily refused the request.", ""
	}
	return "The provider refused the request.", "Review connection"
}

func providerTransient(a ProviderAccessObservation) bool {
	if a.Origin == "provider_network" {
		return true
	}
	if a.Origin != "provider" {
		return false
	}
	switch a.ProviderErrorCode {
	case "rate_limit_exceeded", "rate_limit_error", "rate_limit", "overloaded", "overloaded_error", "server_error", "api_error", "temporarily_unavailable", "provider_stream_error":
		return true
	case "":
		return a.HTTPStatus != nil && (*a.HTTPStatus == 429 || *a.HTTPStatus >= 500)
	}
	return false
}

// A request failure needs evidence of a shared restriction before it changes access.
func providerRequestLocal(a ProviderAccessObservation) bool {
	if a.Origin != "provider" || providerTransient(a) {
		return false
	}
	switch a.ProviderErrorCode {
	case "invalid_encrypted_content", "invalid_signature", "invalid_request", "invalid_request_error":
		return true
	case "invalid_api_key", "authentication_error", "authentication_failed",
		"insufficient_quota", "billing_error", "credit_balance_exhausted", "usage_limit_reached",
		"model_not_found", "permission_denied", "permission_error", "not_found_error", "oauth_org_not_allowed", "access_denied":
		return false
	}
	return a.HTTPStatus == nil || *a.HTTPStatus != 401
}

func currentProviderObservation(ctx context.Context, q Q, a ProviderAccessObservation, operation, profile string) (bool, error) {
	var verified, failed string
	err := q.QueryRowContext(ctx, `SELECT COALESCE(verified_at,''),COALESCE(latest_failure_at,'') FROM connection_health
 WHERE connection_id=? AND operation=? AND profile_id=?`, a.Connection, operation, profile).Scan(&verified, &failed)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	observed, err := time.Parse(time.RFC3339Nano, a.ObservedAt)
	if err != nil {
		return false, err
	}
	for _, value := range []string{verified, failed} {
		if value == "" {
			continue
		}
		stored, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			stored, err = time.Parse(SpineTime, value)
		}
		if err != nil {
			return false, err
		}
		if observed.Before(stored) {
			return false, nil
		}
	}
	return true, nil
}

func RecordProviderAccess(ctx context.Context, q Q, a ProviderAccessObservation) (ProviderAccessResult, error) {
	result := ProviderAccessResult{RequestID: a.RequestID}
	currentAuthorization, currentCredential := a.CurrentAuthorizationRevision, a.CurrentCredentialRef
	currentType := a.CurrentCredentialType
	a.CurrentAuthorizationRevision, a.CurrentCredentialRef, a.CurrentCredentialType = "", "", ""
	body, err := json.Marshal(a)
	if err != nil {
		return result, err
	}
	event := providerDiagnosticID(a.RequestID)
	var previous string
	err = q.QueryRowContext(ctx, `SELECT id,submission FROM diagnostic_events WHERE event_id=?`, event).Scan(&result.DiagnosticID, &previous)
	if err == nil {
		if previous != string(body) {
			return result, Errf("provider-observation-conflict", "The provider request already has different evidence.")
		}
		result.Duplicate = true
		return result, nil
	}
	if err != sql.ErrNoRows {
		return result, err
	}
	failed := providerObservationFailed(a)
	condition, _ := providerCondition(a)
	outcome := "failed"
	if !failed {
		outcome, condition = "ready", "The host completed the provider request."
	}
	operation := "provider_request"
	if a.Operation == "service" {
		operation = "provider_service"
	}
	d := Diagnostic{EventID: event, Source: "resident", Operation: operation, Outcome: outcome,
		RequestSent: a.ProviderSent, ConnectionID: a.Connection, AuthorizationID: a.AuthorizationRevision, CheckID: a.RequestID,
		ProfileID: a.ProfileID, ProfileRevision: a.ProfileRevision, ObservedAt: a.ObservedAt,
		HTTPStatus: a.HTTPStatus, ProviderErrorCode: a.ProviderErrorCode, NetworkError: a.NetworkError, Condition: condition}
	if a.RunID != "" {
		err := q.QueryRowContext(ctx, `SELECT id,COALESCE(project,'') FROM runs WHERE id=?`, a.RunID).Scan(&d.RunID, &d.ProjectID)
		if err != nil && err != sql.ErrNoRows {
			return result, err
		}
	}
	if a.AttemptID != "" {
		err := q.QueryRowContext(ctx, `SELECT attempt_id FROM collie_reply_attempts WHERE attempt_id=?`, a.AttemptID).Scan(&d.ReplyAttemptID)
		if err != nil && err != sql.ErrNoRows {
			return result, err
		}
	}
	result.DiagnosticID, err = AppendDiagnostic(ctx, q, d, string(body))
	if err != nil {
		return result, err
	}
	if failed && providerRequestLocal(a) {
		return result, nil
	}
	current := a.Connection != "" && a.AuthorizationRevision != "" && a.AuthorizationRevision == currentAuthorization &&
		a.CredentialRef != "" && a.CredentialRef == currentCredential
	if current && (a.Origin == "provider_renewal" || failed && (a.Origin == "provider" || a.Origin == "provider_network")) && currentType != "" {
		operation := "connection_access"
		if currentType == "account" {
			operation = "token_refresh"
		}
		_, err = q.ExecContext(ctx, `INSERT INTO connection_health(connection_id,operation,authorization_id) VALUES(?,?,?)
 ON CONFLICT(connection_id,operation,profile_id) DO NOTHING`, a.Connection, operation, a.AuthorizationRevision)
		if err != nil {
			return result, err
		}
	}
	if current && a.Origin == "provider_renewal" {
		return applyProviderRenewal(ctx, q, a, result)
	}
	if !current || a.Origin != "provider" && a.Origin != "provider_network" || !failed {
		return result, nil
	}
	var connectionOperation, auth string
	err = q.QueryRowContext(ctx, `SELECT operation,authorization_id FROM connection_health WHERE connection_id=? AND profile_id='' AND authorization_id=? ORDER BY operation LIMIT 1`, a.Connection, a.AuthorizationRevision).Scan(&connectionOperation, &auth)
	if err == sql.ErrNoRows || err == nil && auth != a.AuthorizationRevision {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	credentialFailure := a.HTTPStatus != nil && *a.HTTPStatus == 401 || a.ProviderErrorCode == "invalid_api_key" || a.ProviderErrorCode == "authentication_error" || a.ProviderErrorCode == "authentication_failed"
	quotaFailure := a.ProviderErrorCode == "insufficient_quota" || a.ProviderErrorCode == "billing_error" || a.ProviderErrorCode == "credit_balance_exhausted" || a.ProviderErrorCode == "usage_limit_reached"
	if credentialFailure || quotaFailure {
		if current, err := currentProviderObservation(ctx, q, a, connectionOperation, ""); err != nil || !current {
			return result, err
		}
		state := "stopped"
		if credentialFailure && connectionOperation == "token_refresh" {
			state = "unknown"
		}
		_, err = q.ExecContext(ctx, `UPDATE connection_health SET state=?,revision=revision+1,latest_diagnostic_id=?,next_retry_at=NULL,request_deadline=NULL,
 failure_count=CASE WHEN ?='unknown' THEN 0 ELSE MIN(failure_count+1,3) END,
 first_failure_at=COALESCE(first_failure_at,?),latest_failure_at=? WHERE connection_id=? AND profile_id='' AND authorization_id=?`,
			state, result.DiagnosticID, state, a.ObservedAt, a.ObservedAt, a.Connection, a.AuthorizationRevision)
		result.Applied = err == nil
		return result, err
	}
	if a.ProfileID == "" {
		return result, nil
	}
	if current, err := currentProviderObservation(ctx, q, a, "model_access", a.ProfileID); err != nil || !current {
		return result, err
	}
	var profileRevision string
	err = q.QueryRowContext(ctx, `SELECT profile_revision FROM connection_health WHERE connection_id=? AND operation='model_access' AND profile_id=?`, a.Connection, a.ProfileID).Scan(&profileRevision)
	if err != nil && err != sql.ErrNoRows {
		return result, err
	}
	if err == nil && profileRevision != a.ProfileRevision {
		return result, nil
	}
	state, retry := "stopped", ""
	if providerTransient(a) {
		state = "retry_wait"
		at := time.Now().UTC().Add(ConnectionRetryDelay(1))
		if specified, err := time.Parse(time.RFC3339Nano, a.RetryAfter); err == nil && specified.After(at) {
			at = specified
		}
		retry = at.Format(time.RFC3339Nano)
	}
	_, err = q.ExecContext(ctx, `INSERT INTO connection_health(connection_id,operation,profile_id,authorization_id,profile_revision,state,revision,failure_count,first_failure_at,latest_failure_at,next_retry_at,latest_diagnostic_id)
 VALUES(?,'model_access',?,?,?,?,1,1,?,?,NULLIF(?,''),?)
 ON CONFLICT(connection_id,operation,profile_id) DO UPDATE SET state=excluded.state,revision=connection_health.revision+1,
 failure_count=MIN(connection_health.failure_count+1,3),first_failure_at=COALESCE(connection_health.first_failure_at,excluded.first_failure_at),
 latest_failure_at=excluded.latest_failure_at,next_retry_at=excluded.next_retry_at,latest_diagnostic_id=excluded.latest_diagnostic_id
 WHERE connection_health.authorization_id=excluded.authorization_id AND connection_health.profile_revision=excluded.profile_revision
 AND (connection_health.state<>'stopped' OR excluded.state='stopped')`,
		a.Connection, a.ProfileID, a.AuthorizationRevision, a.ProfileRevision, state, a.ObservedAt, a.ObservedAt, retry, result.DiagnosticID)
	result.Applied = err == nil
	return result, err
}

func applyProviderRenewal(ctx context.Context, q Q, a ProviderAccessObservation, result ProviderAccessResult) (ProviderAccessResult, error) {
	if current, err := currentProviderObservation(ctx, q, a, "token_refresh", ""); err != nil || !current {
		return result, err
	}
	var auth, previous string
	var count int
	err := q.QueryRowContext(ctx, `SELECT h.authorization_id,h.failure_count,COALESCE(d.submission,'') FROM connection_health h
 LEFT JOIN diagnostic_events d ON d.id=h.latest_diagnostic_id WHERE h.connection_id=? AND h.operation='token_refresh' AND h.profile_id=''`, a.Connection).Scan(&auth, &count, &previous)
	if err == sql.ErrNoRows || err == nil && auth != a.AuthorizationRevision {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var old ProviderAccessObservation
	if json.Unmarshal([]byte(previous), &old) == nil && old.Origin == "provider" {
		switch old.ProviderErrorCode {
		case "insufficient_quota", "billing_error", "credit_balance_exhausted", "usage_limit_reached":
			return result, nil
		}
	}
	state, retry := "ready", ""
	if providerObservationFailed(a) {
		count = min(count+1, 3)
		state = "stopped"
		if a.NetworkError != "" || a.HTTPStatus != nil && (*a.HTTPStatus == 429 || *a.HTTPStatus >= 500) {
			state = "retry_wait"
			at := time.Now().UTC().Add(ConnectionRetryDelay(count))
			if requested, err := time.Parse(time.RFC3339Nano, a.RetryAfter); err == nil && requested.After(at) {
				at = requested
			}
			retry = at.Format(time.RFC3339Nano)
		}
	} else {
		count = 0
	}
	_, err = q.ExecContext(ctx, `UPDATE connection_health SET state=?,failure_count=?,revision=revision+1,latest_diagnostic_id=?,
 next_retry_at=NULLIF(?,''),request_deadline=NULL,first_failure_at=CASE WHEN ?='ready' THEN NULL ELSE COALESCE(first_failure_at,?) END,
 latest_failure_at=CASE WHEN ?='ready' THEN NULL ELSE ? END,verified_at=CASE WHEN ?='ready' THEN ? ELSE verified_at END,
 expires_at=CASE WHEN ?='ready' THEN NULLIF(?,'') ELSE expires_at END WHERE connection_id=? AND operation='token_refresh' AND authorization_id=?`,
		state, count, result.DiagnosticID, retry, state, a.ObservedAt, state, a.ObservedAt, state, a.ObservedAt, state, a.ExpiresAt, a.Connection, a.AuthorizationRevision)
	result.Applied = err == nil
	return result, err
}

type ProviderServiceCondition struct {
	Origin       string
	Connection   string
	Condition    string
	Action       string
	DiagnosticID int64
	ObservedAt   string
}

func ProviderServiceConditions(ctx context.Context, q Q, connection string) ([]ProviderServiceCondition, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,submission FROM diagnostic_events
 WHERE operation IN ('provider_request','provider_service') AND (connection_id='' OR ?='' OR connection_id=?)
 AND (operation='provider_service' OR json_extract(submission,'$.origin')='credential_store' OR request_sent=1)
 ORDER BY observed_at DESC,id DESC`, connection, connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var result []ProviderServiceCondition
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var a ProviderAccessObservation
		if json.Unmarshal([]byte(raw), &a) != nil {
			continue
		}
		origin := a.Origin
		if a.ProviderSent {
			origin = "credential_store"
		}
		if origin != "proxy" && origin != "credential_store" {
			continue
		}
		key := origin + ":" + a.Connection
		if seen[key] {
			continue
		}
		seen[key] = true
		if a.ProviderSent || !providerObservationFailed(a) {
			continue
		}
		condition, action := providerCondition(a)
		result = append(result, ProviderServiceCondition{Origin: origin, Connection: a.Connection, Condition: condition, Action: action, DiagnosticID: id, ObservedAt: a.ObservedAt})
	}
	return result, rows.Err()
}

// ApplyProviderEvidence uses only the host's record to establish a proxy failure's origin.
func ApplyProviderEvidence(ctx context.Context, q Q, run string, report InfrastructureFailureReport, attempts ...string) (InfrastructureFailureReport, error) {
	if report.Source == "agent" {
		report.ProxyRequestID = ""
		return report, nil
	}
	var raw string
	var err error
	attempt := ""
	if len(attempts) > 0 {
		attempt = attempts[0]
	}
	if attempt != "" && report.ProxyRequestID != "" {
		err = q.QueryRowContext(ctx, `SELECT submission FROM diagnostic_events WHERE event_id=? AND operation='provider_request' AND reply_attempt_id=?`, providerDiagnosticID(report.ProxyRequestID), attempt).Scan(&raw)
	} else if attempt != "" {
		err = q.QueryRowContext(ctx, `SELECT submission FROM diagnostic_events WHERE operation='provider_request' AND reply_attempt_id=? ORDER BY observed_at DESC,id DESC LIMIT 1`, attempt).Scan(&raw)
	} else if report.ProxyRequestID != "" {
		err = q.QueryRowContext(ctx, `SELECT submission FROM diagnostic_events WHERE event_id=? AND operation='provider_request' AND run_id=?`, providerDiagnosticID(report.ProxyRequestID), run).Scan(&raw)
	} else {
		err = q.QueryRowContext(ctx, `SELECT submission FROM diagnostic_events WHERE operation='provider_request' AND run_id=? ORDER BY observed_at DESC,id DESC LIMIT 1`, run).Scan(&raw)
	}
	if err != nil && err != sql.ErrNoRows {
		return report, err
	}
	if err == sql.ErrNoRows && report.ProxyRequestID == "" {
		return report, nil
	}
	var a ProviderAccessObservation
	if err == sql.ErrNoRows || json.Unmarshal([]byte(raw), &a) != nil {
		report.Source, report.Cause = "resident", "connection"
		report.HTTPStatus, report.ProviderErrorCode, report.NetworkError = nil, "", ""
		report.Condition, report.Evidence = "The provider request ended before its host evidence arrived.", "The request identity awaits its host observation."
		report.TrustedProxy = true
		return report, nil
	}
	if !providerObservationFailed(a) {
		return report, nil
	}
	report.TrustedProxy = true
	report.ProxyRequestID = a.RequestID
	report.Source, report.Cause = "resident", "connection"
	report.Condition, report.OperatorAction = providerCondition(a)
	if providerRequestLocal(a) {
		report.Cause = "unknown"
	}
	report.Evidence = "The host recorded request " + a.RequestID + " from " + a.Origin + "."
	report.HTTPStatus, report.ProviderErrorCode, report.NetworkError = nil, "", ""
	if a.Origin == "provider" {
		report.HTTPStatus = a.HTTPStatus
		switch a.ProviderErrorCode {
		case "invalid_api_key", "insufficient_quota", "rate_limit_exceeded", "model_not_found", "permission_denied", "authentication_failed", "oauth_org_not_allowed", "billing_error", "rate_limit", "overloaded", "invalid_request", "invalid_request_error", "invalid_encrypted_content", "invalid_signature", "server_error":
			report.ProviderErrorCode = a.ProviderErrorCode
		}
	} else if a.Origin == "provider_network" {
		report.NetworkError = a.NetworkError
		if report.NetworkError == "" {
			report.NetworkError = "other"
		}
	}
	return report, nil
}

// RecoverProviderAttempt requires a native model response from the affected attempt.
func RecoverProviderAttempt(ctx context.Context, q Q, run, attempt string) (bool, error) {
	var connection, auth, profile, revision string
	var err error
	if run != "" {
		err = q.QueryRowContext(ctx, `SELECT connection_id,authorization_id,profile_id,profile_revision FROM runs WHERE id=?`, run).Scan(&connection, &auth, &profile, &revision)
	} else {
		err = q.QueryRowContext(ctx, `SELECT connection_id,authorization_id,profile_id,profile_revision FROM collie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&connection, &auth, &profile, &revision)
	}
	if err != nil || connection == "" || profile == "" {
		return false, err
	}
	var diagnostic int64
	var raw string
	err = q.QueryRowContext(ctx, `SELECT d.id,d.submission FROM connection_health h JOIN diagnostic_events d ON d.id=h.latest_diagnostic_id
 WHERE h.connection_id=? AND h.profile_id=? AND h.authorization_id=? AND h.profile_revision=? AND h.state='retry_wait'
 AND d.operation='provider_request' AND ((?<>'' AND d.run_id=?) OR (?<>'' AND d.reply_attempt_id=?))`, connection, profile, auth, revision, run, run, attempt, attempt).Scan(&diagnostic, &raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var observation ProviderAccessObservation
	if json.Unmarshal([]byte(raw), &observation) != nil || !providerTransient(observation) {
		return false, nil
	}
	var currentAuth string
	if err := q.QueryRowContext(ctx, `SELECT authorization_id FROM connection_health WHERE connection_id=? AND profile_id='' LIMIT 1`, connection).Scan(&currentAuth); err != nil || auth != currentAuth {
		return false, err
	}
	d := Diagnostic{Source: "runner", Operation: "model_access", Outcome: "ready", ConnectionID: connection, AuthorizationID: auth, ProfileID: profile, ProfileRevision: revision, RunID: run, ReplyAttemptID: attempt, RequestSent: true, StateBefore: "retry_wait", StateAfter: "ready", Condition: "The native harness completed a model response."}
	body, _ := json.Marshal(struct {
		Diagnostic
		ResolvedDiagnosticID int64 `json:"resolved_diagnostic_id"`
	}{d, diagnostic})
	id, err := AppendDiagnostic(ctx, q, d, string(body))
	if err != nil {
		return false, err
	}
	_, err = q.ExecContext(ctx, `UPDATE connection_health SET state='ready',failure_count=0,next_retry_at=NULL,first_failure_at=NULL,verified_at=?,latest_diagnostic_id=? WHERE connection_id=? AND profile_id=?`, time.Now().UTC().Format(time.RFC3339Nano), id, connection, profile)
	return err == nil, err
}

func ProviderRepairAction(submission, kind string) string {
	var a ProviderAccessObservation
	if json.Unmarshal([]byte(submission), &a) != nil || a.RequestID == "" {
		return ""
	}
	_, action := providerCondition(a)
	if a.Origin == "provider" && (a.HTTPStatus != nil && *a.HTTPStatus == 401 || a.ProviderErrorCode == "invalid_api_key" || strings.HasPrefix(a.ProviderErrorCode, "authentication")) {
		if strings.HasSuffix(kind, "oauth") {
			return "Retry connection"
		}
		return "Replace API key"
	}
	return action
}
