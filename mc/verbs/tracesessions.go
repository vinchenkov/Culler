package verbs

import (
	"context"
	"database/sql"
	"encoding/json"

	"mc/trace"
)

// TraceSession is one main trace that the resident copies for AgentsView.
type TraceSession struct {
	Kind              string `json:"kind"`
	ID                string `json:"id"`
	SessionPath       string `json:"session_path"`
	Format            string `json:"format"`
	NativeSessionRef  string `json:"native_session_ref"`
	TraceFilename     string `json:"trace_filename"`
	LegacyFilename    string `json:"legacy_filename,omitempty"`
	AgentsViewSession string `json:"agentsview_session"`
	DisplayName       string `json:"display_name"`
	Ended             bool   `json:"ended"`
}

// TraceSessions lists pipeline, Collie, and setup traces. Retained setup operations
// without locators supply candidates for bounded recovery by the resident.
func TraceSessions(db *sql.DB) (any, error) {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, classify(err)
	}
	defer tx.Rollback()
	sessions := []TraceSession{}
	rows, err := tx.QueryContext(ctx, `
		SELECT r.id, COALESCE(r.role, ''), COALESCE(r.project, ''), COALESCE(w.title, ''),
		       COALESCE(t.title, ''), r.session_path, COALESCE(r.binding, ''),
		       r.native_session_ref, COALESCE(r.trace_filename, ''), r.ended_at IS NOT NULL
		FROM runs r
		LEFT JOIN projects w ON w.id = r.project
		LEFT JOIN tasks t ON t.id = r.subject
		WHERE r.native_session_ref IS NOT NULL AND r.session_path IS NOT NULL
		ORDER BY r.created_at, r.created_seq, r.rowid`)
	if err != nil {
		return nil, classify(err)
	}
	for rows.Next() {
		var session TraceSession
		var role, project, projectTitle, taskTitle, binding string
		if err := rows.Scan(&session.ID, &role, &project, &projectTitle, &taskTitle, &session.SessionPath,
			&binding, &session.NativeSessionRef, &session.TraceFilename, &session.Ended); err != nil {
			rows.Close()
			return nil, classify(err)
		}
		session.Kind = "run"
		session.DisplayName = trace.RunDisplayName(trace.ProjectName(projectTitle, project), role, taskTitle, session.ID)
		if traceSession(&session, binding) {
			sessions = append(sessions, session)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, classify(err)
	}
	if err := rows.Close(); err != nil {
		return nil, classify(err)
	}
	rows, err = tx.QueryContext(ctx, `
		SELECT h.id, COALESCE(h.vision_project_id, ''), COALESCE(w.title, ''), h.session_path, h.binding,
		       h.native_session_ref, COALESCE(h.trace_filename, ''),
		       h.deleted_at IS NOT NULL OR h.status <> 'active'
		FROM collie_sessions h
		LEFT JOIN projects w ON w.id = h.vision_project_id
		WHERE h.native_session_ref IS NOT NULL
		ORDER BY h.rowid`)
	if err != nil {
		return nil, classify(err)
	}
	for rows.Next() {
		var session TraceSession
		var visionProject, visionTitle, binding string
		if err := rows.Scan(&session.ID, &visionProject, &visionTitle, &session.SessionPath, &binding,
			&session.NativeSessionRef, &session.TraceFilename, &session.Ended); err != nil {
			rows.Close()
			return nil, classify(err)
		}
		session.Kind = "collie"
		if visionProject != "" {
			visionTitle = trace.ProjectName(visionTitle, visionProject)
		}
		session.DisplayName = trace.CollieDisplayName(visionTitle, session.ID)
		if traceSession(&session, binding) {
			sessions = append(sessions, session)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, classify(err)
	}
	if err := rows.Close(); err != nil {
		return nil, classify(err)
	}
	rows, err = tx.QueryContext(ctx, `SELECT o.id,o.project_id,COALESCE(p.title,''),o.kind,o.runtime_binding,
        COALESCE(o.native_session_ref,''),COALESCE(o.trace_filename,''),o.ended_at IS NOT NULL
        FROM environment_operations o JOIN projects p ON p.id=o.project_id
        WHERE o.kind IN ('setup','validate') ORDER BY o.created_at,o.rowid`)
	if err != nil {
		return nil, classify(err)
	}
	pending := []TraceSession{}
	for rows.Next() {
		var session TraceSession
		var project, title, kind, runtime string
		if err := rows.Scan(&session.ID, &project, &title, &kind, &runtime, &session.NativeSessionRef, &session.TraceFilename, &session.Ended); err != nil {
			rows.Close()
			return nil, classify(err)
		}
		var binding PrivateRuntimeBinding
		if err := json.Unmarshal([]byte(runtime), &binding); err != nil {
			rows.Close()
			return nil, classify(err)
		}
		historical := binding.Harness + "/" + binding.ID
		session.Kind = "setup"
		session.SessionPath = "environment-runs/" + session.ID + "/session"
		session.DisplayName = trace.SetupDisplayName(trace.ProjectName(title, project), kind, session.ID)
		if traceSession(&session, historical) || session.NativeSessionRef != "" || session.TraceFilename != "" {
			sessions = append(sessions, session)
		} else if session.NativeSessionRef == "" && session.TraceFilename == "" && trace.Format(historical) != "" {
			pending = append(pending, session)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, classify(err)
	}
	if err := rows.Close(); err != nil {
		return nil, classify(err)
	}
	result := map[string]any{"sessions": sessions}
	if len(pending) > 0 {
		result["setup_pending"] = pending
	}
	return result, nil
}

// traceSession completes the trace identity of a row and reports whether the
// row has a main trace.
func traceSession(session *TraceSession, binding string) bool {
	session.Format = trace.Format(binding)
	session.AgentsViewSession = trace.SessionID(binding, session.NativeSessionRef)
	if session.Format == trace.FormatOpenCode && session.NativeSessionRef != "" {
		if session.TraceFilename != trace.OpenCodeDatabase && trace.ValidFilename(binding, session.TraceFilename) {
			session.LegacyFilename = session.TraceFilename
		}
		session.TraceFilename = trace.OpenCodeDatabase
	}
	return session.AgentsViewSession != "" && session.SessionPath != "" && session.TraceFilename != ""
}
