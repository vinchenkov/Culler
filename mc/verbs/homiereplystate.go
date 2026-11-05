package verbs

import (
	"context"
	"database/sql"
	"time"

	"mc/domain"
)

func claimReplyAttempt(ctx context.Context, q Q, id *RunIdentity, session string, message int64) (string, error) {
	if id.Launch != "" {
		var current sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT current_launch_id FROM homie_sessions WHERE id=?`, session).Scan(&current); err != nil {
			return "", err
		}
		if current.String != id.Launch {
			return "", Domainf("The Homie launch changed.")
		}
	}
	var attempt, state, launch string
	err := q.QueryRowContext(ctx, `SELECT attempt_id,state,launch_id FROM homie_reply_attempts WHERE message_id=? ORDER BY rowid DESC LIMIT 1`, message).Scan(&attempt, &state, &launch)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if err == nil {
		if state == "failed" || state == "cancelled" {
			return "", nil
		}
		if launch != "" && launch != id.Launch {
			return "", Domainf("The reply belongs to another launch.")
		}
		if state == "authorized" {
			runtime := id.RuntimeBinding
			auth, revision := "", int64(0)
			if runtime.Credential != "" && runtime.Harness != "fake" {
				auth, revision, err = ConnectionAdmission(ctx, q, runtime.Credential, runtime.ID, time.Now(), runtime.ProfileRevision)
				if err != nil {
					return "", err
				}
				if auth != runtime.AuthorizationID {
					return "", domain.Errf(domain.CodeRuntimeChanged, "The connection authorization changed. The launch requires the current authorization.")
				}
			}
			_, err = q.ExecContext(ctx, `UPDATE homie_reply_attempts SET state='active',launch_id=?,connection_id=?,authorization_id=?,connection_revision=?,profile_id=?,profile_revision=? WHERE attempt_id=?`, id.Launch, runtime.Credential, auth, revision, runtime.ID, runtime.ProfileRevision, attempt)
		}
		return attempt, err
	}
	attempt = domain.DiagnosticID()
	runtime := id.RuntimeBinding
	var auth string
	var revision int64
	if runtime.Credential != "" && runtime.Harness != "fake" {
		var err error
		auth, revision, err = ConnectionAdmission(ctx, q, runtime.Credential, runtime.ID, time.Now(), runtime.ProfileRevision)
		if err != nil {
			return "", err
		}
		if auth != runtime.AuthorizationID {
			return "", domain.Errf(domain.CodeRuntimeChanged, "The connection authorization changed. The launch requires the current authorization.")
		}
	}
	_, err = q.ExecContext(ctx, `INSERT INTO homie_reply_attempts(attempt_id,session_id,message_id,launch_id,connection_id,authorization_id,connection_revision,profile_id,profile_revision,state) VALUES(?,?,?,?,?,?,?,?,?,'active')`, attempt, session, message, id.Launch, runtime.Credential, auth, revision, runtime.ID, runtime.ProfileRevision)
	return attempt, err
}

func requireReplyAttempt(ctx context.Context, q Q, id *RunIdentity, message int64, expected string, completed bool) error {
	var attempt, launch, state string
	if err := q.QueryRowContext(ctx, `SELECT attempt_id,launch_id,state FROM homie_reply_attempts WHERE message_id=? ORDER BY rowid DESC LIMIT 1`, message).Scan(&attempt, &launch, &state); err != nil {
		return err
	}
	if launch != id.Launch || (expected != "" && expected != attempt) || (id.Launch != "" && expected == "") {
		return Domainf("The reply attempt changed.")
	}
	if state != "active" && !(completed && state == "succeeded") {
		return Domainf("The reply attempt is no longer active.")
	}
	return nil
}

func finishReplyAttempt(ctx context.Context, q Q, attempt, state, source, condition string, reports ...domain.InfrastructureFailureReport) error {
	var session, current string
	var message int64
	err := q.QueryRowContext(ctx, `SELECT session_id,message_id,state FROM homie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&session, &message, &current)
	if err != nil {
		return err
	}
	if current == "failed" || current == "succeeded" || current == "cancelled" {
		return nil
	}
	var connection, auth, profile, profileRevision string
	var revision int64
	if err := q.QueryRowContext(ctx, `SELECT connection_id,authorization_id,profile_id,profile_revision,connection_revision FROM homie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&connection, &auth, &profile, &profileRevision, &revision); err != nil {
		return err
	}
	if state == "succeeded" {
		if _, err := domain.RecoverNetworkAttempt(ctx, q, "", attempt, "mc"); err != nil {
			return err
		}
	}
	report := domain.InfrastructureFailureReport{}
	if len(reports) > 0 {
		report = reports[0]
	}
	if state == "failed" {
		report, err = domain.ApplyProviderEvidence(ctx, q, "", report, attempt)
		if err != nil {
			return err
		}
		if report.TrustedProxy {
			condition = report.Condition
		}
	}
	diagnostic, err := domain.AppendDiagnostic(ctx, q, domain.Diagnostic{Source: source, Operation: "homie_reply", Outcome: state, ReplyAttemptID: attempt, ConnectionID: connection, AuthorizationID: auth, ProfileID: profile, ProfileRevision: profileRevision, HTTPStatus: report.HTTPStatus, ProviderErrorCode: report.ProviderErrorCode, NetworkError: report.NetworkError, Condition: condition, StateBefore: current, StateAfter: state}, "")
	if err != nil {
		return err
	}
	if state == "failed" && report.NetworkError != "" && !report.TrustedProxy {
		if err := domain.RecordNetworkFailure(ctx, q, connection, auth, profile, profileRevision, revision, diagnostic); err != nil {
			return err
		}
	}
	if err := domain.RecordConnectionFailure(ctx, q, connection, auth, profile, profileRevision, revision, diagnostic, report); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE homie_reply_attempts SET state=?,ended_at=datetime('now'),latest_diagnostic_id=? WHERE attempt_id=?`, state, diagnostic, attempt)
	return err
}

func HomieReplyFailure(db *sql.DB, id *RunIdentity, attempt, condition string, reports ...domain.InfrastructureFailureReport) (any, error) {
	if !diagnosticIDPattern.MatchString(attempt) || len(condition) > 2048 || condition == "" {
		return nil, Usagef("The reply failure is invalid.")
	}
	if len(reports) > 0 {
		reports[0].Condition = condition
		reports[0].Source = "runner"
		if err := domain.ValidateInfrastructureFailureReport(reports[0]); err != nil {
			return nil, err
		}
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var session, launch string
		if err := q.QueryRowContext(ctx, `SELECT session_id,launch_id FROM homie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&session, &launch); err != nil {
			return err
		}
		source := "resident"
		if id != nil {
			if err := requireHomieRunner(id, session, "reply failure"); err != nil {
				return err
			}
			if launch != id.Launch {
				return Domainf("The reply belongs to another launch.")
			}
			source = "runner"
		}
		return finishReplyAttempt(ctx, q, attempt, "failed", source, condition, reports...)
	})
	return map[string]any{"attempt_id": attempt, "recorded": err == nil}, err
}

func HomieReplyRetry(db *sql.DB, id *RunIdentity, session string, message int64, key string) (any, error) {
	if err := RequireHostScope(id, "retry reply"); err != nil {
		return nil, err
	}
	if !diagnosticIDPattern.MatchString(key) {
		return nil, Usagef("The reply retry identity is invalid.")
	}
	var attempt string
	err := inTx(db, func(ctx context.Context, q Q) error {
		var recordedSession string
		var recordedMessage int64
		err := q.QueryRowContext(ctx, `SELECT attempt_id,session_id,message_id FROM homie_reply_attempts WHERE retry_key=?`, key).Scan(&attempt, &recordedSession, &recordedMessage)
		if err == nil {
			if recordedSession != session || recordedMessage != message {
				return Domainf("The retry identity belongs to another message.")
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		var old, state, connection, profile, oldLaunch string
		var completed sql.NullString
		err = q.QueryRowContext(ctx, `SELECT a.attempt_id,a.state,a.connection_id,a.profile_id,m.completed_at,a.launch_id FROM homie_reply_attempts a JOIN conversation_messages m ON m.id=a.message_id WHERE a.session_id=? AND a.message_id=? ORDER BY a.rowid DESC LIMIT 1`, session, message).Scan(&old, &state, &connection, &profile, &completed, &oldLaunch)
		if err != nil {
			return err
		}
		if state != "failed" || completed.Valid {
			return Domainf("This reply does not require a retry.")
		}
		if connection != "" {
			if _, _, err := ConnectionAdmission(ctx, q, connection, profile, time.Now()); err != nil {
				return err
			}
		}
		var status, surface, channel string
		var currentLaunch sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT status,current_launch_id FROM homie_sessions WHERE id=?`, session).Scan(&status, &currentLaunch); err != nil {
			return err
		}
		if status == "active" && oldLaunch != "" && currentLaunch.String == oldLaunch {
			return Domainf("The previous Homie process must stop before this reply can retry. Retry after the next status update.")
		}
		if status != "active" {
			if err := q.QueryRowContext(ctx, `SELECT surface,channel_ref FROM homie_bindings WHERE session_id=? ORDER BY surface='dashboard' DESC,id DESC LIMIT 1`, session).Scan(&surface, &channel); err != nil {
				return err
			}
			if err := resumeBoundSessionForSend(ctx, q, session, SurfaceRef{Surface: surface, ChannelRef: channel}); err != nil {
				return err
			}
			if _, err := ensureActiveBinding(ctx, q, session, SurfaceRef{Surface: surface, ChannelRef: channel}); err != nil {
				return err
			}
		}
		attempt = domain.DiagnosticID()
		_, err = q.ExecContext(ctx, `INSERT INTO homie_reply_attempts(attempt_id,session_id,message_id,state,retry_key,connection_id,authorization_id,connection_revision,profile_id,profile_revision)
   SELECT ?,session_id,message_id,'authorized',?,connection_id,authorization_id,connection_revision,profile_id,profile_revision FROM homie_reply_attempts WHERE attempt_id=?`, attempt, key, old)
		if err != nil {
			return err
		}
		_, err = domain.AppendDiagnostic(ctx, q, domain.Diagnostic{Source: "operator", Operation: "homie_reply", Outcome: "resumed", ReplyAttemptID: attempt, Condition: "The operator authorized a reply retry."}, "")
		return err
	})
	return map[string]any{"attempt_id": attempt, "session_id": session}, err
}

func failSessionReplies(ctx context.Context, q Q, session, condition string, terminal ...string) error {
	rows, err := q.QueryContext(ctx, `SELECT attempt_id FROM homie_reply_attempts WHERE session_id=? AND ended_at IS NULL`, session)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	state, source := "failed", "resident"
	if len(terminal) > 0 {
		state = terminal[0]
		source = "operator"
	}
	for _, attempt := range ids {
		if err := finishReplyAttempt(ctx, q, attempt, state, source, condition); err != nil {
			return err
		}
	}
	return nil
}

// HomieLaunchFailure records the unanswered messages of the current launch.
func HomieLaunchFailure(db *sql.DB, id *RunIdentity, session, launch string) (any, error) {
	if err := RequireHostScope(id, "Homie launch failure"); err != nil {
		return nil, err
	}
	changed := false
	err := inTx(db, func(ctx context.Context, q Q) error {
		var current sql.NullString
		var state string
		if err := q.QueryRowContext(ctx, `SELECT status,current_launch_id FROM homie_sessions WHERE id=?`, session).Scan(&state, &current); err != nil {
			return err
		}
		if state != "active" || current.String != launch {
			return nil
		}
		rows, err := q.QueryContext(ctx, `SELECT id FROM conversation_messages m WHERE session_id=? AND direction='inbound' AND completed_at IS NULL AND NOT EXISTS(SELECT 1 FROM homie_reply_attempts a WHERE a.message_id=m.id)`, session)
		if err != nil {
			return err
		}
		messages := []int64{}
		for rows.Next() {
			var message int64
			if err := rows.Scan(&message); err != nil {
				rows.Close()
				return err
			}
			messages = append(messages, message)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, message := range messages {
			if _, err := q.ExecContext(ctx, `INSERT INTO homie_reply_attempts(attempt_id,session_id,message_id,launch_id,state) VALUES(?,?,?,?,'authorized')`, domain.DiagnosticID(), session, message, launch); err != nil {
				return err
			}
		}
		changed, _, err = homieEndTx(ctx, q, "resident", session, "The Homie launch failed.")
		return err
	})
	return map[string]any{"recorded": changed}, err
}

func HomieLaunchCheck(db *sql.DB, id *RunIdentity, session, launch, connection, authorization, profile, profileRevision string) (any, error) {
	if err := RequireHostScope(id, "Homie launch check"); err != nil {
		return nil, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var current sql.NullString
		var state string
		if err := q.QueryRowContext(ctx, `SELECT status,current_launch_id FROM homie_sessions WHERE id=?`, session).Scan(&state, &current); err != nil {
			return err
		}
		if state != "active" || current.String != launch {
			return Domainf("The Homie launch changed.")
		}
		auth, _, err := ConnectionAdmission(ctx, q, connection, profile, time.Now(), profileRevision)
		if err != nil {
			return err
		}
		if auth != authorization {
			return domain.Errf(domain.CodeRuntimeChanged, "The connection authorization changed. The launch requires the current authorization.")
		}
		return nil
	})
	return map[string]any{"allowed": err == nil}, err
}

func HomieModelResponse(db *sql.DB, id *RunIdentity, attempt string) (any, error) {
	if !diagnosticIDPattern.MatchString(attempt) {
		return nil, Usagef("The reply attempt identity is invalid.")
	}
	var recovered bool
	err := inTx(db, func(ctx context.Context, q Q) error {
		var session string
		var message int64
		if err := q.QueryRowContext(ctx, `SELECT session_id,message_id FROM homie_reply_attempts WHERE attempt_id=?`, attempt).Scan(&session, &message); err != nil {
			return err
		}
		if err := requireHomieRunner(id, session, "model response"); err != nil {
			return err
		}
		if err := requireActiveHomieSessionTx(ctx, q, session); err != nil {
			return err
		}
		if err := requireReplyAttempt(ctx, q, id, message, attempt, false); err != nil {
			return err
		}
		var launch sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT current_launch_id FROM homie_sessions WHERE id=?`, session).Scan(&launch); err != nil {
			return err
		}
		if launch.String != id.Launch {
			return Domainf("The Homie launch changed.")
		}
		var err error
		recovered, err = domain.RecoverNetworkAttempt(ctx, q, "", attempt)
		return err
	})
	return map[string]any{"recovered": recovered}, err
}
