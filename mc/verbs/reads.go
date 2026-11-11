package verbs

import (
	"context"
	"database/sql"
	"strings"

	"mc/trace"
)

// LeaseList includes final run facts so the dashboard can identify a pending container stop.
// Only an agent run holds a lease.
func LeaseList(db *sql.DB) (any, error) {
	rows, err := db.Query(`
		SELECT l.project,
               CASE WHEN l.run_id IS NULL THEN 'free' ELSE 'held' END AS status,
               l.run_id, l.owner, l.subject, l.acquired_at, l.last_heartbeat_at,
               r.binding,r.agent_started_at, r.pool_snapshot,r.role AS run_role, r.outcome AS run_outcome, r.ended_at AS run_ended_at
        FROM leases l LEFT JOIN runs r ON r.id = l.run_id ORDER BY l.project`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out, err := rowsToMaps(rows)
	if err != nil {
		return nil, classify(err)
	}
	return map[string]any{"leases": out}, nil
}

// SettingsGet returns the one settings row as JSON — the deployment-wide
// settings — a pure read under the `mc <record> get/list` surface.
func SettingsGet(db *sql.DB) (any, error) {
	rows, err := db.Query(`
		SELECT timeout_minutes, grace_minutes, heartbeat_interval_s,
		       spawn_grace_s, hard_deadline_minutes, review_wip_cap,
		       collie_idle_timeout_s, wiki_update_interval_s
		FROM settings WHERE id = 1`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out, err := rowsToMaps(rows)
	if err != nil {
		return nil, classify(err)
	}
	if len(out) == 0 {
		return nil, Domainf("no settings row (uninitialized spine)")
	}
	return out[0], nil
}

// RunSelection limits `mc run list` to the runs of one task, of one Project,
// or of both. The zero value selects each run.
type RunSelection struct {
	Task    int64
	Project string
}

// RunList returns the selected runs rows as JSON (reads only). Run rows are
// never deleted, so the unselected list grows for the life of the deployment.
// The wiki_update key holds the result of the wiki update of a Scribe run. It
// is null for each other run.
func RunList(db *sql.DB, selection RunSelection) (any, error) {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, classify(err)
	}
	defer tx.Rollback()
	query := `
		SELECT id, tier, role, project, subject, created_at, ended_at,
		       outcome, end_reason, agent_started_at, session_path, binding, native_session_ref,
		       trace_filename, pool_snapshot, verdict_outcome
		FROM runs`
	var where []string
	var args []any
	if selection.Task != 0 {
		where = append(where, "subject = ?")
		args = append(args, selection.Task)
	}
	if selection.Project != "" {
		where = append(where, "project = ?")
		args = append(args, selection.Project)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := tx.QueryContext(ctx, query+" ORDER BY created_at, created_seq", args...)
	if err != nil {
		return nil, classify(err)
	}
	out, err := rowsToMaps(rows)
	rows.Close()
	if err != nil {
		return nil, classify(err)
	}
	updates, err := loadRunWikiUpdates(ctx, tx)
	if err != nil {
		return nil, classify(err)
	}
	for _, row := range out {
		row["agentsview"] = agentsViewSession(row["binding"], row["native_session_ref"])
		if binding, ok := row["binding"].(string); ok {
			if filename, ok := row["trace_filename"].(string); ok {
				row["trace_filename"] = trace.Filename(binding, filename)
			}
		}
		id, _ := row["id"].(string)
		row["wiki_update"] = updates[id]
	}
	return map[string]any{"runs": out}, nil
}

// agentsViewSession is the computed "agentsview" key of a run or Collie
// session row: the AgentsView session of its main trace, or nil when the row
// has no main trace.
func agentsViewSession(binding, nativeRef any) any {
	bindingText, _ := binding.(string)
	nativeText, _ := nativeRef.(string)
	session := trace.SessionID(bindingText, nativeText)
	if session == "" {
		return nil
	}
	return map[string]any{"session": session, "path": trace.Path(bindingText, nativeText)}
}

// TaskList returns the task pool as JSON — the Board tab's read path:
// `mc task get` is by-id only, so the kanban cannot enumerate its own
// columns without it.
//
// It returns LIVE work by default and takes `all` to include archived rows.
// That default is a filter, not a judgement: archived tasks accumulate for the
// lifetime of the deployment and are never deleted, so an unfiltered list would
// grow without bound and the Board polls it.
//
// `mc packet list` returns everything by default. The REVIEW_WIP_CAP bounds
// only the LIVE subset; the archived remainder is unbounded and monotonic
// exactly as the task pool is. What holds that default is that the dashboard
// shells the verb unmodified and derives the review occupancy from the
// unfiltered array, so the default is load-bearing for a consumer rather
// than justified by a ceiling. `mc packet list --live` exists for callers
// that want the capped half.
//
// The columns are TaskGet's, minus the prose ones a board never shows
// (description, revision_notes), so a card renders from one row.
func TaskList(db *sql.DB, all bool, project string) (any, error) {
	query := `
		SELECT id, title, scope, priority, created_at, status,
		       stage_rank, stage_entered_at, correction_count, blocked,
		       blocked_reason, blocked_kind, dispatch_retries,run_retry_at, stalled, infrastructure_failure_count,
               ` + latestInfrastructureFailureSQL + `, ` + connectionWaitSQL + `, ` + projectInfrastructureSQL + `, decision, decided_at, archived, origin,
		       project, completion_sha, (SELECT result_commit FROM task_versions WHERE id=tasks.source_version) AS verified_sha, base_commit, ` + deliveryTaskSQL + `
		FROM tasks`
	var where []string
	var args []any
	if !all {
		where = append(where, "archived = 0")
	}
	if project != "" {
		where = append(where, "project = ?")
		args = append(args, project)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	// Furthest-first inside a status, then oldest-first: the same shape the
	// board reads top-to-bottom, and stable across polls.
	query += " ORDER BY stage_rank DESC, priority, created_at, id"
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out, err := taskRowsToMaps(rows)
	if err != nil {
		return nil, classify(err)
	}
	return map[string]any{"tasks": out}, nil
}

// ActivityList filters the current records before it applies the row limit.
// subject_kind names what the subject identifies: a run, a task, a connection,
// or a Project.
func ActivityList(db *sql.DB, kind string, limit int, project string) (any, error) {
	if limit <= 0 {
		limit = activityListDefaultLimit
	}
	if limit > activityListMaxLimit {
		limit = activityListMaxLimit
	}
	query := `WITH scoped AS (
        SELECT a.id, a.created_at, a.actor, a.kind, a.subject, a.detail,
            CASE WHEN a.subject IS NULL THEN NULL
            WHEN a.kind LIKE 'project.%' THEN 'project'
            WHEN a.kind IN ('dispatch.wait_started', 'dispatch.wait_ended') THEN 'connection'
            WHEN EXISTS (SELECT 1 FROM runs r WHERE r.id = a.subject) THEN 'run'
            WHEN EXISTS (SELECT 1 FROM tasks t WHERE t.id = CAST(a.subject AS INTEGER) AND CAST(t.id AS TEXT) = a.subject) THEN 'task'
            END AS subject_kind,
            CASE WHEN a.project IS NOT NULL THEN a.project
            WHEN a.kind LIKE 'project.%' THEN
                (SELECT w.id FROM projects w WHERE w.id = a.subject)
            WHEN a.kind NOT LIKE 'collie.%' THEN COALESCE(
                (SELECT t.project FROM tasks t WHERE t.id = CAST(a.subject AS INTEGER) AND CAST(t.id AS TEXT) = a.subject),
                (SELECT r.project FROM runs r WHERE r.id = a.subject AND r.tier = 'pipeline'),
                CASE WHEN json_valid(a.dispatch_result) THEN json_extract(a.dispatch_result, '$.project') END,
                (SELECT r.project FROM runs r WHERE r.id = a.actor AND r.tier = 'pipeline')
            ) END AS project
        FROM activity a
    ) SELECT * FROM scoped WHERE 1 = 1`
	var args []any
	if kind != "" {
		query += " AND kind = ?"
		args = append(args, kind)
	}
	if project != "" {
		query += " AND project = ?"
		args = append(args, project)
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out, err := rowsToMaps(rows)
	if err != nil {
		return nil, classify(err)
	}
	return map[string]any{"activity": out, "limit": limit}, nil
}

const (
	activityListDefaultLimit = 100
	activityListMaxLimit     = 1000
)
