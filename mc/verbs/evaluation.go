package verbs

import (
	"context"
	"database/sql"
)

// EvaluationRecords captures the original records without authentication fields from run rows.
func EvaluationRecords(db *sql.DB, run string) (any, error) {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var task int64
	var project, workspace string
	if err = tx.QueryRowContext(ctx, `SELECT r.subject,r.project,s.workspace_root FROM runs r JOIN projects p ON p.id=r.project JOIN sandbox_profiles s ON s.id=p.sandbox_profile WHERE r.id=? AND r.role='packager'`, run).Scan(&task, &project, &workspace); err != nil {
		return nil, Domainf("The original Packager run is unavailable.")
	}
	var deployment string
	if err = tx.QueryRowContext(ctx, `SELECT deployment_uuid FROM meta WHERE id=1`).Scan(&deployment); err != nil {
		return nil, err
	}
	out := map[string]any{"deployment": deployment, "workspace_root": workspace, "project_id": project, "task_id": task, "run_id": run}
	queries := map[string]string{
		"task":            `SELECT * FROM tasks WHERE id=?`,
		"project":         `SELECT p.id,p.title,p.jurisdiction FROM projects p JOIN tasks t ON t.project=p.id WHERE t.id=?`,
		"versions":        `SELECT * FROM task_versions WHERE task_id=? ORDER BY id`,
		"submissions":     `SELECT * FROM version_submissions WHERE task_id=? ORDER BY run_id`,
		"verdicts":        `SELECT v.* FROM version_verdicts v JOIN task_versions t ON t.id=v.version_id WHERE t.task_id=? ORDER BY v.version_id`,
		"requirements":    `SELECT * FROM requirements_revisions WHERE task_id=? ORDER BY id`,
		"corrections":     `SELECT * FROM correction_results WHERE task_id=? ORDER BY version_id`,
		"runs":            `SELECT id,role,subject,created_at,ended_at,outcome,end_reason,binding,verdict_outcome,evidence_path,correction_path,output_path FROM runs WHERE subject=? ORDER BY created_seq`,
		"executions":      `SELECT run_id,operation,target,started_at,ended_at,end_reason FROM repository_executions WHERE task_id=? ORDER BY id`,
		"repository_runs": `SELECT * FROM repository_runs WHERE task_id=? ORDER BY run_id`,
		"candidates":      `SELECT * FROM merge_candidates WHERE task_id=? ORDER BY id`,
		"packets":         `SELECT * FROM review_versions WHERE task_id=? ORDER BY id`,
	}
	for key, query := range queries {
		rows, err := tx.QueryContext(ctx, query, task)
		if err != nil {
			return nil, err
		}
		records, err := rowsToMaps(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		out[key] = records
	}
	rows, err := tx.QueryContext(ctx, `SELECT * FROM evaluation_captures WHERE run_id=?`, run)
	if err != nil {
		return nil, err
	}
	out["capture"], err = rowsToMaps(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	return out, nil
}
func EvaluationRegister(db *sql.DB, run, caseID string, enabled bool) (any, error) {
	result, err := db.Exec(`INSERT INTO evaluation_captures(run_id,project,task_id,version_id,enabled,case_id)
 SELECT r.id,r.project,r.subject,rr.version_id,?,? FROM runs r JOIN repository_runs rr ON rr.run_id=r.id
 WHERE r.id=? AND r.role='packager' AND r.ended_at IS NULL ON CONFLICT(run_id) DO NOTHING`, enabled, caseID, run)
	if err != nil {
		return nil, err
	}
	_, err = result.RowsAffected()
	if err != nil {
		return nil, err
	}
	return EvaluationRecords(db, run)
}
func EvaluationStatus(db *sql.DB, run, status, message string, packet int64, hash string) (any, error) {
	if status != "pending" && status != "complete" && status != "failed" {
		return nil, Usagef("The capture status is invalid.")
	}
	result, err := db.Exec(`UPDATE evaluation_captures SET status=?,error=?,packet_id=CASE WHEN ?>0 THEN ? ELSE packet_id END,report_sha256=CASE WHEN ?>0 THEN ? ELSE report_sha256 END
 WHERE run_id=? AND enabled=1 AND (?=0 OR EXISTS(SELECT 1 FROM review_versions p JOIN runs r ON r.id=evaluation_captures.run_id
 WHERE p.id=? AND p.task_id=evaluation_captures.task_id AND p.version_id=evaluation_captures.version_id AND p.render_path=r.output_path AND p.report_sha256=?))`, status, message, packet, packet, packet, hash, run, packet, packet, hash)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, Domainf("The capture does not identify the submitted packet.")
	}
	return map[string]any{"status": status}, nil
}
func EvaluationLookup(db *sql.DB, project string, task, packet, version int64) (any, error) {
	var run, caseID, status, message, hash string
	err := db.QueryRow(`SELECT c.run_id,c.case_id,c.status,c.error,p.report_sha256 FROM evaluation_captures c JOIN review_versions p ON p.task_id=c.task_id AND p.version_id=c.version_id JOIN runs r ON r.id=c.run_id AND r.output_path=p.render_path
 WHERE c.project=? AND c.task_id=? AND p.id=? AND p.version_id=? AND c.enabled=1`, project, task, packet, version).Scan(&run, &caseID, &status, &message, &hash)
	if err == sql.ErrNoRows {
		return map[string]any{"available": false, "error": "The original effective Packager input and source capture are unavailable."}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"available": status == "complete", "run_id": run, "case_id": caseID, "status": status, "error": message, "packet_sha256": hash}, nil
}
func EvaluationPending(db *sql.DB) (any, error) {
	rows, err := db.Query(`SELECT c.run_id FROM evaluation_captures c JOIN runs r ON r.id=c.run_id WHERE c.enabled=1 AND c.status<>'complete' AND r.ended_at IS NOT NULL ORDER BY c.captured_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := rowsToMaps(rows)
	return map[string]any{"runs": out}, err
}
func EvaluationCaptureRecord(db *sql.DB, run string) (any, error) {
	rows, err := db.Query(`SELECT * FROM evaluation_captures WHERE run_id=?`, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records, err := rowsToMaps(rows)
	return map[string]any{"capture": records}, err
}

func EvaluationExpiredCases(db *sql.DB, task int64) (any, error) {
	rows, err := db.Query(`SELECT case_id FROM evaluation_captures WHERE task_id=?`, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records, err := rowsToMaps(rows)
	return map[string]any{"cases": records}, err
}

func EvaluationTrace(db *sql.DB, run string) (any, error) {
	var session, trace sql.NullString
	if err := db.QueryRow(`SELECT session_path,trace_filename FROM runs WHERE id=? AND role='packager'`, run).Scan(&session, &trace); err != nil {
		return nil, err
	}
	return map[string]any{"session_path": session.String, "trace_filename": trace.String}, nil
}
