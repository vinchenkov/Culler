package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"time"

	"mc/domain"
)

var diagnosticIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var connectionIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

type ConnectionCheck struct {
	ConnectionID    string `json:"connection_id"`
	AuthorizationID string `json:"authorization_id"`
	Operation       string `json:"operation"`
	ProfileID       string `json:"profile_id"`
	ProfileRevision string `json:"profile_revision"`
	CheckID         string `json:"check_id"`
}

type ConnectionObservation struct {
	ConnectionCheck
	RequestSent       bool   `json:"request_sent"`
	EventID           string `json:"event_id"`
	Outcome           string `json:"outcome"`
	ObservedAt        string `json:"observed_at"`
	HTTPStatus        *int   `json:"http_status"`
	ProviderErrorCode string `json:"provider_error_code"`
	NetworkError      string `json:"network_error"`
	Condition         string `json:"condition"`
	Evidence          string `json:"evidence"`
	ExpiresAt         string `json:"expires_at"`
	RetryAfter        string `json:"retry_after"`
}

// A network error class describes a request that received no provider response.
func networkErrorClass(class string) bool {
	switch class {
	case "connect", "reset", "timeout", "tls", "other":
		return true
	}
	return false
}

// tokenRefreshAction names the operator action for a stopped token refresh.
// Only a provider rejection of the grant, or a lost grant, requires Reconnect.
func tokenRefreshAction(status *int, code string) string {
	switch {
	case code == "invalid_client" || code == "invalid_request" || code == "invalid_scope" || code == "unauthorized_client":
		return "Review connection"
	case code == "invalid_grant" || code == "access_denied" || code == "credential_store" || (status != nil && *status == 401):
		return "Reconnect"
	}
	return "Retry connection"
}

func validateConnectionCheck(a ConnectionCheck) error {
	if !connectionIDPattern.MatchString(a.ConnectionID) || len(a.AuthorizationID) > 128 || a.AuthorizationID == "" || !diagnosticIDPattern.MatchString(a.CheckID) {
		return Usagef("The connection check identity is invalid.")
	}
	if a.Operation != "token_refresh" && a.Operation != "connection_access" && a.Operation != "model_access" {
		return Usagef("The connection operation is invalid.")
	}
	if (a.Operation == "model_access") != (a.ProfileID != "") || (a.ProfileID != "" && (!connectionIDPattern.MatchString(a.ProfileID) || a.ProfileRevision == "")) {
		return Usagef("The connection profile scope is invalid.")
	}
	return nil
}

func ConnectionCheckStart(db *sql.DB, id *RunIdentity, a ConnectionCheck) (any, error) {
	if err := RequireHostScope(id, "connection check"); err != nil {
		return nil, err
	}
	if err := validateConnectionCheck(a); err != nil {
		return nil, err
	}
	out := map[string]any{"check_id": a.CheckID, "operation": a.Operation, "allowed": false}
	err := inTx(db, func(ctx context.Context, q Q) error {
		now := time.Now().UTC()
		_, err := q.ExecContext(ctx, `INSERT INTO connection_health(connection_id,operation,profile_id,authorization_id,profile_revision) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, a.ConnectionID, a.Operation, a.ProfileID, a.AuthorizationID, a.ProfileRevision)
		if err != nil {
			return err
		}
		var auth, profileRevision, state, check string
		var count int
		var deadline, retry sql.NullString
		err = q.QueryRowContext(ctx, `SELECT authorization_id,profile_revision,state,check_id,failure_count,request_deadline,next_retry_at FROM connection_health WHERE connection_id=? AND operation=? AND profile_id=?`, a.ConnectionID, a.Operation, a.ProfileID).Scan(&auth, &profileRevision, &state, &check, &count, &deadline, &retry)
		if err != nil {
			return err
		}
		if auth != a.AuthorizationID || profileRevision != a.ProfileRevision {
			_, err = q.ExecContext(ctx, `UPDATE connection_health SET authorization_id=?,profile_revision=?,state='unknown',revision=revision+1,check_id='',model_refresh_used=0,failure_count=0,first_failure_at=NULL,latest_failure_at=NULL,next_retry_at=NULL,request_deadline=NULL,verified_at=NULL,expires_at=NULL WHERE connection_id=? AND operation=? AND profile_id=?`, a.AuthorizationID, a.ProfileRevision, a.ConnectionID, a.Operation, a.ProfileID)
			if err != nil {
				return err
			}
			state = "unknown"
			count = 0
			deadline = sql.NullString{}
			retry = sql.NullString{}
		}
		if deadline.Valid {
			end, e := time.Parse(time.RFC3339Nano, deadline.String)
			if e == nil && now.Before(end) {
				out["reason"] = "A connection check is active."
				out["next_retry_at"] = deadline.String
				return nil
			}
			// A token request without a recorded result retries on the network schedule.
			next, nextCount, nextRetry := "stopped", count, sql.NullString{}
			if a.Operation == "token_refresh" {
				next, nextCount = "retry_wait", min(count+1, 3)
				nextRetry = sql.NullString{String: now.Add(domain.ConnectionRetryDelay(nextCount)).Format(time.RFC3339Nano), Valid: true}
			}
			diagnostic, err := domain.AppendDiagnostic(ctx, q, domain.Diagnostic{Source: "mc", Operation: a.Operation, Outcome: "failed", ConnectionID: a.ConnectionID, AuthorizationID: a.AuthorizationID, CheckID: check, ProfileID: a.ProfileID, ProfileRevision: a.ProfileRevision, Condition: "The previous connection check ended without a recorded result.", StateBefore: state, StateAfter: next, CountBefore: count, CountAfter: nextCount}, "")
			if err != nil {
				return err
			}
			observed := now.Format(time.RFC3339Nano)
			_, err = q.ExecContext(ctx, `UPDATE connection_health SET state=?,failure_count=?,revision=revision+1,request_deadline=NULL,next_retry_at=?,latest_diagnostic_id=?,first_failure_at=COALESCE(first_failure_at,?),latest_failure_at=? WHERE connection_id=? AND operation=? AND profile_id=?`, next, nextCount, nextRetry, diagnostic, observed, observed, a.ConnectionID, a.Operation, a.ProfileID)
			if err != nil {
				return err
			}
			state, count, retry = next, nextCount, nextRetry
		}
		if state == "stopped" {
			out["reason"] = "The connection requires repair."
			return nil
		}
		if retry.Valid {
			at, e := time.Parse(time.RFC3339Nano, retry.String)
			if e == nil && now.Before(at) {
				out["next_retry_at"] = retry.String
				out["reason"] = "The connection retry time is in the future."
				return nil
			}
		}
		deadlineAt := now.Add(30 * time.Second).Format(time.RFC3339Nano)
		// A reservation supplies no new evidence about credential validity.
		_, err = q.ExecContext(ctx, `UPDATE connection_health SET check_id=?,check_sequence=check_sequence+1,request_deadline=? WHERE connection_id=? AND operation=? AND profile_id=?`, a.CheckID, deadlineAt, a.ConnectionID, a.Operation, a.ProfileID)
		if err != nil {
			return err
		}
		_, err = domain.AppendDiagnostic(ctx, q, domain.Diagnostic{Source: "resident", Operation: a.Operation, Outcome: "started", ConnectionID: a.ConnectionID, AuthorizationID: a.AuthorizationID, CheckID: a.CheckID, ProfileID: a.ProfileID, ProfileRevision: a.ProfileRevision, StateBefore: state, StateAfter: state, CountBefore: count, CountAfter: count}, "")
		out["allowed"] = true
		out["authorization_id"] = a.AuthorizationID
		out["deadline"] = deadlineAt
		return err
	})
	return out, err
}

// ConnectionCheckCancel releases a reservation without new provider evidence.
func ConnectionCheckCancel(db *sql.DB, id *RunIdentity, a ConnectionCheck) (any, error) {
	if err := RequireHostScope(id, "connection check cancellation"); err != nil {
		return nil, err
	}
	if err := validateConnectionCheck(a); err != nil {
		return nil, err
	}
	cleared := false
	err := inTx(db, func(ctx context.Context, q Q) error {
		var state string
		var count int
		err := q.QueryRowContext(ctx, `SELECT state,failure_count FROM connection_health WHERE connection_id=? AND operation=? AND profile_id=? AND authorization_id=? AND profile_revision=? AND check_id=? AND request_deadline IS NOT NULL`, a.ConnectionID, a.Operation, a.ProfileID, a.AuthorizationID, a.ProfileRevision, a.CheckID).Scan(&state, &count)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE connection_health SET check_id='',request_deadline=NULL WHERE connection_id=? AND operation=? AND profile_id=?`, a.ConnectionID, a.Operation, a.ProfileID)
		if err != nil {
			return err
		}
		_, err = domain.AppendDiagnostic(ctx, q, domain.Diagnostic{Source: "resident", Operation: a.Operation, Outcome: "cancelled", ConnectionID: a.ConnectionID, AuthorizationID: a.AuthorizationID, CheckID: a.CheckID, ProfileID: a.ProfileID, ProfileRevision: a.ProfileRevision, Condition: "The connection check ended without new provider evidence.", StateBefore: state, StateAfter: state, CountBefore: count, CountAfter: count}, "")
		cleared = err == nil
		return err
	})
	return map[string]any{"cleared": cleared}, err
}

// Only recognized codes survive provider error parsing.
// providerCodePattern holds the characters of a provider error code.
var providerCodePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{0,64}$`)

func recognizedProviderCode(code string) bool {
	return providerCodePattern.MatchString(code)
}

func ConnectionObserve(db *sql.DB, id *RunIdentity, a ConnectionObservation) (any, error) {
	if err := RequireHostScope(id, "connection observation"); err != nil {
		return nil, err
	}
	if err := validateConnectionCheck(a.ConnectionCheck); err != nil {
		return nil, err
	}
	if !diagnosticIDPattern.MatchString(a.EventID) || !recognizedProviderCode(a.ProviderErrorCode) || (a.Outcome != "ready" && a.Outcome != "failed") {
		return nil, Usagef("The connection observation is invalid.")
	}
	if a.ObservedAt == "" {
		return nil, Usagef("The observation time is required.")
	}
	if a.HTTPStatus != nil && (*a.HTTPStatus < 100 || *a.HTTPStatus > 599) {
		return nil, Usagef("The response status is invalid.")
	}
	if a.NetworkError != "" && (!networkErrorClass(a.NetworkError) || a.Outcome != "failed" || a.HTTPStatus != nil || a.ProviderErrorCode != "") {
		return nil, Usagef("The network error is invalid.")
	}
	for _, stamp := range []string{a.ObservedAt, a.ExpiresAt, a.RetryAfter} {
		if stamp != "" {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				return nil, Usagef("The observation time is invalid.")
			}
		}
	}
	// Evidence comes from the closed response fields. Raw response text never enters the spine.
	a.Condition = ""
	a.Evidence = ""
	raw, _ := json.Marshal(a)
	out := map[string]any{"accepted": true, "event_id": a.EventID}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var stored string
		var diagnostic int64
		err := q.QueryRowContext(ctx, `SELECT id,submission FROM diagnostic_events WHERE event_id=?`, a.EventID).Scan(&diagnostic, &stored)
		if err == nil {
			if stored != string(raw) {
				return Domainf("The observation identifier already has different contents.")
			}
			out["diagnostic_id"] = diagnostic
			out["duplicate"] = true
			var state string
			var retry sql.NullString
			var latest sql.NullInt64
			if err := q.QueryRowContext(ctx, `SELECT state,next_retry_at,latest_diagnostic_id FROM connection_health WHERE connection_id=? AND operation=? AND profile_id=?`, a.ConnectionID, a.Operation, a.ProfileID).Scan(&state, &retry, &latest); err != nil {
				return err
			}
			out["applied"] = latest.Valid && latest.Int64 == diagnostic
			out["state"] = state
			if retry.Valid {
				out["next_retry_at"] = retry.String
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		var auth, profileRevision, state, check string
		var count int
		var deadline sql.NullString
		err = q.QueryRowContext(ctx, `SELECT authorization_id,profile_revision,state,check_id,failure_count,request_deadline FROM connection_health WHERE connection_id=? AND operation=? AND profile_id=?`, a.ConnectionID, a.Operation, a.ProfileID).Scan(&auth, &profileRevision, &state, &check, &count, &deadline)
		if err != nil {
			return err
		}
		applicable := auth == a.AuthorizationID && profileRevision == a.ProfileRevision && check == a.CheckID && deadline.Valid
		nextState := state
		nextCount := count
		var nextRetry any
		condition := "The connection check succeeded."
		if a.Outcome == "failed" {
			condition = "The connection check failed."
			if a.ProviderErrorCode != "" {
				condition += " Code: " + a.ProviderErrorCode + "."
			}
			if a.NetworkError != "" {
				condition = "The request received no provider response."
			}
		}
		if applicable {
			if a.Outcome == "ready" {
				nextState = "ready"
				nextCount = 0
			} else {
				nextCount++
				if nextCount > 3 {
					nextCount = 3
				}
				nextState = "stopped"
				repeatable := a.HTTPStatus != nil && (*a.HTTPStatus == 429 || *a.HTTPStatus >= 500)
				if a.ProviderErrorCode != "" && a.ProviderErrorCode != "temporarily_unavailable" && a.ProviderErrorCode != "server_error" && a.ProviderErrorCode != "rate_limit_exceeded" {
					repeatable = false
				}
				if a.NetworkError != "" {
					// A network failure retries without a limit.
					nextState = "retry_wait"
					nextRetry = time.Now().UTC().Add(domain.ConnectionRetryDelay(nextCount)).Format(time.RFC3339Nano)
				} else if repeatable && nextCount < 3 {
					nextState = "retry_wait"
					delay := domain.ConnectionRetryDelay(nextCount)
					// The random identifier supplies bounded jitter without another random source.
					delay += time.Duration(int(a.EventID[0])%21) * delay / 100
					at := time.Now().UTC().Add(delay)
					if requested, err := time.Parse(time.RFC3339Nano, a.RetryAfter); err == nil && requested.After(at) {
						at = requested
					}
					nextRetry = at.Format(time.RFC3339Nano)
				}
			}
		}
		diagnostic, err = domain.AppendDiagnostic(ctx, q, domain.Diagnostic{RequestSent: a.RequestSent, EventID: a.EventID, Source: "resident", Operation: a.Operation, Outcome: a.Outcome,
			ConnectionID: a.ConnectionID, AuthorizationID: a.AuthorizationID, CheckID: a.CheckID, ProfileID: a.ProfileID, ProfileRevision: a.ProfileRevision,
			ObservedAt: a.ObservedAt, HTTPStatus: a.HTTPStatus, ProviderErrorCode: a.ProviderErrorCode, NetworkError: a.NetworkError, Condition: condition,
			StateBefore: state, StateAfter: nextState, CountBefore: count, CountAfter: nextCount}, string(raw))
		if err != nil {
			return err
		}
		if applicable {
			_, err = q.ExecContext(ctx, `UPDATE connection_health SET state=?,failure_count=?,revision=revision+1,request_deadline=NULL,next_retry_at=?,latest_diagnostic_id=?,
    first_failure_at=CASE WHEN ?='ready' THEN NULL ELSE COALESCE(first_failure_at,?) END,
    latest_failure_at=CASE WHEN ?='ready' THEN NULL ELSE ? END,
    verified_at=CASE WHEN ?='ready' THEN ? ELSE verified_at END,
    expires_at=CASE WHEN ?='ready' THEN NULLIF(?,'') ELSE expires_at END
    WHERE connection_id=? AND operation=? AND profile_id=?`, nextState, nextCount, nextRetry, diagnostic, nextState, a.ObservedAt, nextState, a.ObservedAt, nextState, a.ObservedAt, nextState, a.ExpiresAt, a.ConnectionID, a.Operation, a.ProfileID)
			if err != nil {
				return err
			}
		}
		out["diagnostic_id"] = diagnostic
		out["applied"] = applicable
		out["state"] = nextState
		out["next_retry_at"] = nextRetry
		return nil
	})
	return out, err
}

func ConnectionAdmission(ctx context.Context, q Q, connection, profile string, now time.Time, expectedRevision ...string) (string, int64, error) {
	if err := ProviderAccessAdmission(ctx, q, connection); err != nil {
		return "", 0, err
	}
	rows, err := q.QueryContext(ctx, `SELECT authorization_id,state,expires_at,revision,profile_id,profile_revision,request_deadline,next_retry_at FROM connection_health WHERE connection_id=? AND (profile_id='' OR profile_id=?) ORDER BY profile_id<>'',operation`, connection, profile)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	var auth string
	var revision int64
	found := false
	for rows.Next() {
		var a, state, scope, revisionID string
		var expires, deadline, retry sql.NullString
		var rev int64
		if err := rows.Scan(&a, &state, &expires, &rev, &scope, &revisionID, &deadline, &retry); err != nil {
			return "", 0, err
		}
		if scope == "" {
			found = true
		}
		if scope != "" && state == "ready" && found && auth != a && (len(expectedRevision) == 0 || expectedRevision[0] == revisionID) {
			continue
		}
		if scope != "" && len(expectedRevision) > 0 && revisionID != expectedRevision[0] {
			return "", 0, domain.Errf(domain.CodeRuntimeChanged, "The agent profile changed. The launch requires the current profile.")
		}
		if deadline.Valid {
			end, err := time.Parse(time.RFC3339Nano, deadline.String)
			if err != nil || !now.Before(end) {
				return "", 0, Domainf("The connection check outcome is unknown.")
			}
		}
		if scope != "" && state == "retry_wait" {
			// After a network error, the next run of the profile is the automatic retry.
			if at, err := time.Parse(time.RFC3339Nano, retry.String); err != nil || now.Before(at) {
				return "", 0, Domainf("The agent profile waits for an automatic retry after a network error.")
			}
		} else if !domain.ConnectionEligible(state, expires, now) {
			return "", 0, Domainf("The connection requires attention. Open System health in the dashboard.")
		}
		if auth != "" && auth != a {
			return "", 0, Domainf("The connection authorization requires verification.")
		}
		auth = a
		revision += rev
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	if !found {
		return "", 0, Domainf("The connection requires verification. Open System health in the dashboard.")
	}
	return auth, revision, nil
}

// ConnectionVerified records a model test that the host authorized.
func ConnectionVerified(db *sql.DB, id *RunIdentity, a ConnectionObservation) (any, error) {
	if err := RequireHostScope(id, "connection verification"); err != nil {
		return nil, err
	}
	if err := validateConnectionCheck(a.ConnectionCheck); err != nil {
		return nil, err
	}
	if !diagnosticIDPattern.MatchString(a.EventID) || (a.Outcome != "ready" && a.Outcome != "failed") {
		return nil, Usagef("The verification evidence is invalid.")
	}
	if _, err := time.Parse(time.RFC3339Nano, a.ObservedAt); err != nil {
		return nil, Usagef("The verification time is invalid.")
	}
	a.Condition = ""
	a.Evidence = ""
	raw, _ := json.Marshal(a)
	err := inTx(db, func(ctx context.Context, q Q) error {
		var stored string
		err := q.QueryRowContext(ctx, `SELECT submission FROM diagnostic_events WHERE event_id=?`, a.EventID).Scan(&stored)
		if err == nil {
			if stored != string(raw) {
				return Domainf("The verification identifier has different contents.")
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		state, condition := "ready", "The model response and tool call succeeded."
		operation := a.Operation
		if a.Outcome == "failed" {
			state = "stopped"
			condition = "The model test failed. Review the agent profile and its connection."
			if a.ProviderErrorCode == "authorization_failed" {
				condition = "Authorization failed. Reconnect the connection."
				operation = "authorization"
			}
		}
		diagnostic, err := domain.AppendDiagnostic(ctx, q, domain.Diagnostic{EventID: a.EventID, Source: "operator", Operation: operation, Outcome: a.Outcome, ConnectionID: a.ConnectionID, AuthorizationID: a.AuthorizationID, CheckID: a.CheckID, ProfileID: a.ProfileID, ProfileRevision: a.ProfileRevision, ObservedAt: a.ObservedAt, Condition: condition, StateAfter: state}, string(raw))
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `INSERT INTO connection_health(connection_id,operation,profile_id,authorization_id,profile_revision,state,failure_count,first_failure_at,verified_at,latest_failure_at,latest_diagnostic_id,revision)
   VALUES(?,?,?,?,?,?,CASE WHEN ?='stopped' THEN 1 ELSE 0 END,CASE WHEN ?='stopped' THEN ? END,CASE WHEN ?='ready' THEN ? END,CASE WHEN ?='stopped' THEN ? END,?,1)
   ON CONFLICT(connection_id,operation,profile_id) DO UPDATE SET authorization_id=excluded.authorization_id,profile_revision=excluded.profile_revision,state=excluded.state,failure_count=CASE WHEN excluded.state='ready' THEN 0 ELSE MIN(connection_health.failure_count+1,3) END,first_failure_at=CASE WHEN excluded.state='ready' THEN NULL ELSE COALESCE(connection_health.first_failure_at,excluded.latest_failure_at) END,latest_failure_at=excluded.latest_failure_at,next_retry_at=NULL,request_deadline=NULL,verified_at=COALESCE(excluded.verified_at,connection_health.verified_at),latest_diagnostic_id=excluded.latest_diagnostic_id,revision=connection_health.revision+1
   WHERE COALESCE(connection_health.verified_at,'')<=? AND COALESCE(connection_health.latest_failure_at,'')<=?`, a.ConnectionID, a.Operation, a.ProfileID, a.AuthorizationID, a.ProfileRevision, state, state, state, a.ObservedAt, state, a.ObservedAt, state, a.ObservedAt, diagnostic, a.ObservedAt, a.ObservedAt)
		return err
	})
	return map[string]any{"verified": err == nil && a.Outcome == "ready"}, err
}

func RunLaunchCheck(db *sql.DB, id *RunIdentity, run string, requirements ...SystemStatusRequest) (any, error) {
	if err := RequireHostScope(id, "launch check"); err != nil {
		return nil, err
	}
	var connection, auth, profile, profileRevision string
	var revision int64
	var ended sql.NullString
	if err := db.QueryRow(`SELECT connection_id,authorization_id,profile_id,profile_revision,connection_revision,ended_at FROM runs WHERE id=?`, run).Scan(&connection, &auth, &profile, &profileRevision, &revision, &ended); err != nil {
		return nil, err
	}
	if ended.Valid {
		return nil, Domainf("The run already ended.")
	}
	if connection != "" {
		if len(requirements) > 0 {
			valid := false
			for _, current := range requirements[0].Connections {
				if current.ID == connection && current.AuthorizationID == auth && current.Profiles[profile] == profileRevision {
					valid = true
				}
			}
			if !valid {
				return nil, Domainf("The connection configuration changed after dispatch.")
			}
		}
		current, currentRevision, err := ConnectionAdmission(context.Background(), db, connection, profile, time.Now(), profileRevision)
		if err != nil {
			return nil, err
		}
		if current != auth || currentRevision != revision {
			return nil, Domainf("The connection changed after dispatch.")
		}
	}
	clock, err := domain.ReadVMClock()
	if err != nil {
		return nil, err
	}
	expires, err := providerLaunchExpiry(context.Background(), db, ProviderLaunchIdentity{RunID: run, LaunchID: run}, time.Now().UTC(), clock)
	if err != nil || !expires.After(time.Now()) {
		return nil, Domainf("The run deadline is unavailable or expired.")
	}
	return map[string]any{"allowed": true, "expires_at": expires.Format(time.RFC3339Nano)}, nil
}

func ConnectionRetry(db *sql.DB, id *RunIdentity, a ConnectionCheck) (any, error) {
	if err := RequireHostScope(id, "retry connection"); err != nil {
		return nil, err
	}
	if err := validateConnectionCheck(a); err != nil {
		return nil, err
	}
	if a.Operation != "token_refresh" {
		return nil, Domainf("This connection requires a model test.")
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var existing string
		err := q.QueryRowContext(ctx, `SELECT connection_id FROM diagnostic_events WHERE event_id=?`, a.CheckID).Scan(&existing)
		if err == nil {
			if existing != a.ConnectionID {
				return Domainf("The retry identity belongs to another connection.")
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		var state, auth, code string
		var status sql.NullInt64
		var count int
		if err := q.QueryRowContext(ctx, `SELECT h.state,h.authorization_id,h.failure_count,COALESCE(d.provider_error_code,''),d.http_status FROM connection_health h LEFT JOIN diagnostic_events d ON d.id=h.latest_diagnostic_id WHERE h.connection_id=? AND h.operation=? AND h.profile_id=''`, a.ConnectionID, a.Operation).Scan(&state, &auth, &count, &code, &status); err != nil {
			return err
		}
		if auth != a.AuthorizationID {
			return Domainf("The authorization changed.")
		}
		var httpStatus *int
		if status.Valid {
			value := int(status.Int64)
			httpStatus = &value
		}
		if state != "stopped" || tokenRefreshAction(httpStatus, code) != "Retry connection" {
			return Domainf("This failure requires connection repair.")
		}
		if _, err := domain.AppendDiagnostic(ctx, q, domain.Diagnostic{EventID: a.CheckID, Source: "operator", Operation: a.Operation, Outcome: "resumed", ConnectionID: a.ConnectionID, AuthorizationID: auth, Condition: "The operator authorized a new connection allowance.", StateBefore: state, StateAfter: "unknown", CountBefore: count}, ""); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE connection_health SET state='unknown',failure_count=0,revision=revision+1,next_retry_at=NULL,request_deadline=NULL WHERE connection_id=? AND operation=? AND profile_id=''`, a.ConnectionID, a.Operation)
		return err
	})
	return map[string]any{"resumed": err == nil}, err
}
