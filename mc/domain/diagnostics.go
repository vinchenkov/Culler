package domain

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"
)

func DiagnosticID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

type Diagnostic struct {
	RequestSent       bool   `json:"request_sent"`
	EventID           string `json:"event_id"`
	Source            string `json:"source"`
	Operation         string `json:"operation"`
	Outcome           string `json:"outcome"`
	ConnectionID      string `json:"connection_id"`
	AuthorizationID   string `json:"authorization_id"`
	CheckID           string `json:"check_id"`
	ProfileID         string `json:"profile_id"`
	ProfileRevision   string `json:"profile_revision"`
	ProjectID         string `json:"project_id"`
	RunID             string `json:"run_id"`
	ReplyAttemptID    string `json:"reply_attempt_id"`
	ObservedAt        string `json:"observed_at"`
	HTTPStatus        *int   `json:"http_status"`
	ProviderErrorCode string `json:"provider_error_code"`
	NetworkError      string `json:"network_error"`
	Condition         string `json:"condition"`
	Evidence          string `json:"evidence"`
	StateBefore       string `json:"state_before"`
	StateAfter        string `json:"state_after"`
	CountBefore       int    `json:"count_before"`
	CountAfter        int    `json:"count_after"`
}

func AppendDiagnostic(ctx context.Context, q Q, d Diagnostic, submission string) (int64, error) {
	if d.EventID == "" {
		d.EventID = DiagnosticID()
	}
	if d.ObservedAt == "" {
		d.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if submission == "" {
		b, err := json.Marshal(d)
		if err != nil {
			return 0, err
		}
		submission = string(b)
	}
	result, err := q.ExecContext(ctx, `INSERT INTO diagnostic_events
 (event_id,request_sent,source,operation,outcome,connection_id,authorization_id,check_id,profile_id,profile_revision,
 project_id,run_id,reply_attempt_id,observed_at,http_status,provider_error_code,network_error,condition,evidence,
 state_before,state_after,count_before,count_after,submission)
 VALUES (?,?,?,?,?,?,?,?,?,?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?,?,?,?,?,?,?,?,?,?,?)`,
		d.EventID, d.RequestSent, d.Source, d.Operation, d.Outcome, d.ConnectionID, d.AuthorizationID, d.CheckID, d.ProfileID, d.ProfileRevision,
		d.ProjectID, d.RunID, d.ReplyAttemptID, d.ObservedAt, d.HTTPStatus, d.ProviderErrorCode, d.NetworkError, d.Condition, d.Evidence,
		d.StateBefore, d.StateAfter, d.CountBefore, d.CountAfter, submission)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func ResetProjectInfrastructure(ctx context.Context, q Q, project, source, run string, eventID ...string) error {
	var count int
	if err := q.QueryRowContext(ctx, `SELECT infrastructure_failure_count FROM projects WHERE id=?`, project).Scan(&count); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE projects SET infrastructure_failure_count=0, infrastructure_next_retry_at=NULL, infrastructure_condition='' WHERE id=?`, project); err != nil {
		return err
	}
	outcome := "resumed"
	if run != "" {
		outcome = "succeeded"
	}
	event := ""
	if len(eventID) > 0 {
		event = eventID[0]
	}
	_, err := AppendDiagnostic(ctx, q, Diagnostic{EventID: event, Source: source, Operation: "infrastructure", Outcome: outcome, ProjectID: project, RunID: run, CountBefore: count}, "")
	return err
}

// ConnectionRetryDelay is the wait before automatic retry number count.
// Automatic connection retries wait 30 seconds, then 2 minutes, then 5 minutes.
func ConnectionRetryDelay(count int) time.Duration {
	switch count {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	}
	return 5 * time.Minute
}

// ConnectionEligible supplies one rule for dispatch and status reads.
func ConnectionEligible(state string, expires sql.NullString, now time.Time) bool {
	if state != "ready" {
		return false
	}
	if !expires.Valid || expires.String == "" {
		return true
	}
	end, err := time.Parse(time.RFC3339Nano, expires.String)
	return err == nil && now.Before(end)
}

func recordRunConnectionFailure(ctx context.Context, q Q, run string, diagnostic int64, report InfrastructureFailureReport) error {
	if report.TrustedProxy {
		return nil
	}
	if report.HTTPStatus == nil && report.ProviderErrorCode == "" {
		return nil
	}
	var connection, auth, profile, profileRevision string
	var revision int64
	if err := q.QueryRowContext(ctx, `SELECT connection_id,authorization_id,profile_id,profile_revision,connection_revision FROM runs WHERE id=?`, run).Scan(&connection, &auth, &profile, &profileRevision, &revision); err != nil {
		return err
	}
	return RecordConnectionFailure(ctx, q, connection, auth, profile, profileRevision, revision, diagnostic, report)
}

// connectionCurrent reports whether the connection state is still the state
// that admitted the attempt. A failure of an older state changes nothing.
func connectionCurrent(ctx context.Context, q Q, connection, auth, profile, profileRevision string, revision int64) (bool, error) {
	var currentRevision int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(revision),0) FROM connection_health WHERE connection_id=? AND (profile_id='' OR (profile_id=? AND NOT(state='ready' AND authorization_id<>? AND profile_revision=?)))`, connection, profile, auth, profileRevision).Scan(&currentRevision); err != nil {
		return false, err
	}
	if currentRevision != revision {
		return false, nil
	}
	var currentAuth string
	if err := q.QueryRowContext(ctx, `SELECT authorization_id FROM connection_health WHERE connection_id=? AND profile_id=''`, connection).Scan(&currentAuth); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return currentAuth == auth, nil
}

// recordRunNetworkFailure holds new runs of the profile until the next retry
// time. The next run of the profile is the retry. A stopped profile stays stopped.
func recordRunNetworkFailure(ctx context.Context, q Q, run string, diagnostic int64) error {
	var connection, auth, profile, profileRevision string
	var revision int64
	if err := q.QueryRowContext(ctx, `SELECT connection_id,authorization_id,profile_id,profile_revision,connection_revision FROM runs WHERE id=?`, run).Scan(&connection, &auth, &profile, &profileRevision, &revision); err != nil {
		return err
	}
	return RecordNetworkFailure(ctx, q, connection, auth, profile, profileRevision, revision, diagnostic)
}

func RecordNetworkFailure(ctx context.Context, q Q, connection, auth, profile, profileRevision string, revision, diagnostic int64) error {
	if connection == "" || profile == "" {
		return nil
	}
	if current, err := connectionCurrent(ctx, q, connection, auth, profile, profileRevision, revision); err != nil || !current {
		return err
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT failure_count FROM connection_health WHERE connection_id=? AND operation='model_access' AND profile_id=? AND state='retry_wait'`, connection, profile).Scan(&count); err != nil && err != sql.ErrNoRows {
		return err
	}
	count = min(count+1, 3)
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	_, err := q.ExecContext(ctx, `INSERT INTO connection_health(connection_id,operation,profile_id,authorization_id,profile_revision,state,revision,failure_count,first_failure_at,latest_failure_at,next_retry_at,latest_diagnostic_id)
  VALUES(?,'model_access',?,?,?,'retry_wait',1,?,?,?,?,?) ON CONFLICT(connection_id,operation,profile_id) DO UPDATE SET authorization_id=excluded.authorization_id,profile_revision=excluded.profile_revision,state='retry_wait',failure_count=excluded.failure_count,first_failure_at=COALESCE(connection_health.first_failure_at,excluded.first_failure_at),latest_failure_at=excluded.latest_failure_at,next_retry_at=excluded.next_retry_at,revision=connection_health.revision+1,latest_diagnostic_id=excluded.latest_diagnostic_id
  WHERE connection_health.state<>'stopped'`, connection, profile, auth, profileRevision, count, stamp, stamp, now.Add(ConnectionRetryDelay(count)).Format(time.RFC3339Nano), diagnostic)
	return err
}

// ClearNetworkRetry preserves the same revision so a later failure of this run remains valid.
func ClearNetworkRetry(ctx context.Context, q Q, run string) error {
	_, err := RecoverNetworkAttempt(ctx, q, run, "", "mc")
	return err
}

// RecoverNetworkAttempt resolves only the condition that admitted this attempt.
func RecoverNetworkAttempt(ctx context.Context, q Q, run, attempt string, source ...string) (bool, error) {
	if recovered, err := RecoverProviderAttempt(ctx, q, run, attempt); err != nil || recovered {
		return recovered, err
	}
	var connection, auth, profile, profileRevision, project string
	var revision int64
	var err error
	if run != "" {
		err = q.QueryRowContext(ctx, `SELECT connection_id,authorization_id,profile_id,profile_revision,connection_revision,project FROM runs WHERE id=?`, run).Scan(&connection, &auth, &profile, &profileRevision, &revision, &project)
	} else {
		err = q.QueryRowContext(ctx, `SELECT connection_id,authorization_id,profile_id,profile_revision,connection_revision FROM collie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&connection, &auth, &profile, &profileRevision, &revision)
	}
	if err != nil || connection == "" || profile == "" {
		return false, err
	}
	if current, err := connectionCurrent(ctx, q, connection, auth, profile, profileRevision, revision); err != nil || !current {
		return false, err
	}
	var failure int64
	var count int
	err = q.QueryRowContext(ctx, `SELECT latest_diagnostic_id,failure_count FROM connection_health WHERE connection_id=? AND profile_id=? AND operation='model_access' AND state='retry_wait' AND authorization_id=? AND profile_revision=?`, connection, profile, auth, profileRevision).Scan(&failure, &count)
	if err == sql.ErrNoRows {
		var recovered bool
		err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM connection_health h JOIN diagnostic_events d ON d.id=h.latest_diagnostic_id WHERE h.connection_id=? AND h.profile_id=? AND h.state='ready' AND d.operation='model_access' AND d.outcome='ready' AND ((?<>'' AND d.run_id=?) OR (?<>'' AND d.reply_attempt_id=?)))`, connection, profile, run, run, attempt, attempt).Scan(&recovered)
		return recovered, err
	}
	if err != nil {
		return false, err
	}
	origin := "runner"
	if len(source) > 0 {
		origin = source[0]
	}
	d := Diagnostic{EventID: DiagnosticID(), RequestSent: true, Source: origin, Operation: "model_access", Outcome: "ready", ConnectionID: connection, AuthorizationID: auth, ProfileID: profile, ProfileRevision: profileRevision, ProjectID: project, RunID: run, ReplyAttemptID: attempt, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Condition: "The model responded.", StateBefore: "retry_wait", StateAfter: "ready", CountBefore: count}
	submission, err := json.Marshal(struct {
		Diagnostic
		ResolvedDiagnosticID int64 `json:"resolved_diagnostic_id"`
	}{d, failure})
	if err != nil {
		return false, err
	}
	diagnostic, err := AppendDiagnostic(ctx, q, d, string(submission))
	if err != nil {
		return false, err
	}
	_, err = q.ExecContext(ctx, `UPDATE connection_health SET state='ready',failure_count=0,first_failure_at=NULL,next_retry_at=NULL,verified_at=?,latest_diagnostic_id=? WHERE connection_id=? AND profile_id=? AND operation='model_access'`, d.ObservedAt, diagnostic, connection, profile)
	return err == nil, err
}

func RecordConnectionFailure(ctx context.Context, q Q, connection, auth, profile, profileRevision string, revision, diagnostic int64, report InfrastructureFailureReport) error {
	if report.TrustedProxy {
		return nil
	}
	if (report.HTTPStatus == nil && report.ProviderErrorCode == "") || connection == "" {
		return nil
	}
	if current, err := connectionCurrent(ctx, q, connection, auth, profile, profileRevision, revision); err != nil || !current {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if (report.HTTPStatus != nil && *report.HTTPStatus == 401) || report.ProviderErrorCode == "authentication_failed" || report.ProviderErrorCode == "invalid_api_key" {
		_, err := q.ExecContext(ctx, `UPDATE connection_health SET state=CASE WHEN operation='token_refresh' AND model_refresh_used=0 THEN 'unknown' ELSE 'stopped' END,
   model_refresh_used=1,revision=revision+1,next_retry_at=NULL,
   request_deadline=CASE WHEN operation='token_refresh' AND model_refresh_used=0 THEN request_deadline ELSE NULL END,
   failure_count=CASE WHEN operation='token_refresh' AND model_refresh_used=0 THEN 0 ELSE MIN(failure_count+1,3) END,
   first_failure_at=COALESCE(first_failure_at,?),latest_failure_at=?,latest_diagnostic_id=?
   WHERE connection_id=? AND authorization_id=? AND profile_id=''`, now, now, diagnostic, connection, auth)
		return err
	}
	if profile == "" {
		return nil
	}
	_, err := q.ExecContext(ctx, `INSERT INTO connection_health(connection_id,operation,profile_id,authorization_id,profile_revision,state,revision,failure_count,first_failure_at,latest_failure_at,latest_diagnostic_id)
  VALUES(?,'model_access',?,?,?,'stopped',1,1,?,?,?) ON CONFLICT(connection_id,operation,profile_id) DO UPDATE SET authorization_id=excluded.authorization_id,profile_revision=excluded.profile_revision,state='stopped',failure_count=MIN(connection_health.failure_count+1,3),first_failure_at=COALESCE(connection_health.first_failure_at,excluded.first_failure_at),latest_failure_at=excluded.latest_failure_at,revision=connection_health.revision+1,latest_diagnostic_id=excluded.latest_diagnostic_id`, connection, profile, auth, profileRevision, now, now, diagnostic)
	return err
}
