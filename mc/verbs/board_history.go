package verbs

import (
	"context"
	"database/sql"
)

// BoardHistoryReport resolves a report from the requested task's durable history.
func BoardHistoryReport(db *sql.DB, task, record int64, kind string) (any, error) {
	var query string
	switch kind {
	case "packet":
		query = `SELECT render_path, report_sha256 FROM review_versions p WHERE task_id=? AND id=? AND EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.task_id=p.task_id AND r.previous_requirements_version=p.requirements_version)`
	case "evidence":
		query = `SELECT json_extract(delivery,'$.report_path'), json_extract(delivery,'$.report_sha256')
			FROM requirements_evidence WHERE task_id=? AND id=?`
	default:
		return nil, Usagef("The history report kind must be packet or evidence.")
	}
	var path, digest sql.NullString
	if err := db.QueryRow(query, task, record).Scan(&path, &digest); err != nil && err != sql.ErrNoRows {
		return nil, classify(err)
	}
	result := map[string]any{"task_id": task, "record_id": record, "render_path": nil}
	if path.Valid {
		result["render_path"] = path.String
	}
	if digest.Valid {
		result["report_sha256"] = digest.String
	}
	state, err := artifactState(context.Background(), db, task, path.String)
	if err != nil {
		return nil, err
	}
	result["artifact_retention"] = state
	return result, nil
}
