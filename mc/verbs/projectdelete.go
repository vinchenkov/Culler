package verbs

import (
	"context"
	"database/sql"
	"fmt"
)

type ProjectDeletion struct {
	ProjectID       string   `json:"project_id"`
	Title           string   `json:"title"`
	Tasks           int      `json:"tasks"`
	Reviews         int      `json:"reviews"`
	SetupOperations int      `json:"setup_operations"`
	Blockers        []string `json:"blockers"`
}

// projectSessions selects the Vision conversations of the Project and the
// packet conversations of its tasks.
const projectSessions = `SELECT id FROM collie_sessions WHERE vision_project_id=:project
	OR packet_id IN (SELECT id FROM review_versions WHERE task_id IN (SELECT id FROM tasks WHERE project=:project))`

// Each predicate identifies records that belong to the selected Project.
// Shared profiles, image sources, and connection access remain intact.
var projectDeletionTables = []struct{ table, where string }{
	{"projects", "id=:project"},
	{"tasks", "project=:project"},
	{"runs", "project=:project"},
	{"collie_sessions", "id IN (" + projectSessions + ")"},
	{"activity", "project=:project OR (project IS NULL AND (subject IN (SELECT id FROM tasks WHERE project=:project) OR subject IN (SELECT id FROM runs WHERE project=:project) OR subject IN (" + projectSessions + ")))"},
	{"vision_drafts", "project_id=:project"},
	{"vision_submissions", "project_id=:project"},
	{"leases", "project=:project"},
	{"infrastructure_failures", "run_id IN (SELECT id FROM runs WHERE project=:project)"},
	{"collie_bindings", "session_id IN (" + projectSessions + ")"},
	{"collie_discord_threads", "session_id IN (" + projectSessions + ")"},
	{"conversation_messages", "session_id IN (" + projectSessions + ")"},
	{"attachment_objects", "session_id IN (" + projectSessions + ")"},
	{"conversation_attachment_refs", "session_id IN (" + projectSessions + ")"},
	{"attachment_ingresses", "session_id IN (" + projectSessions + ")"},
	{"attachment_batches", "session_id IN (" + projectSessions + ")"},
	{"attachment_publications", "session_id IN (" + projectSessions + ")"},
	{"attachment_ingress_digests", "ingress_id IN (SELECT id FROM attachment_ingresses WHERE session_id IN (" + projectSessions + "))"},
	{"attachment_object_publications", "session_id IN (" + projectSessions + ")"},
	{"attachment_attempts", "session_id IN (" + projectSessions + ")"},
	{"outbox", "session_id IN (" + projectSessions + ") OR source_activity_id IN (SELECT row_id FROM project_deletion_records WHERE project=:project AND table_name='activity') OR (kind='blocked_alert' AND json_extract(payload,'$.task_id') IN (SELECT id FROM tasks WHERE project=:project)) OR (kind='health' AND json_extract(payload,'$.activity_id') IN (SELECT row_id FROM project_deletion_records WHERE project=:project AND table_name='activity'))"},
	{"attachment_batch_receipts", "session_id IN (" + projectSessions + ")"},
	{"board_operations", "task_id IN (SELECT id FROM tasks WHERE project=:project)"},
	{"requirements_revisions", "task_id IN (SELECT id FROM tasks WHERE project=:project)"},
	{"requirements_evidence", "task_id IN (SELECT id FROM tasks WHERE project=:project)"},
	{"run_requirements", "run_id IN (SELECT id FROM runs WHERE project=:project) OR task_id IN (SELECT id FROM tasks WHERE project=:project)"},
	{"diagnostic_events", "project_id=:project OR run_id IN (SELECT id FROM runs WHERE project=:project) OR reply_attempt_id IN (SELECT attempt_id FROM collie_reply_attempts WHERE session_id IN (" + projectSessions + "))"},
	{"collie_reply_attempts", "session_id IN (" + projectSessions + ")"},
	{"evaluation_captures", "project=:project"},
	{"proposal_decisions", "project=:project"},
	{"proposal_decision_tasks", "decision_id IN (SELECT id FROM proposal_decisions WHERE project=:project)"},
	{"accepted_commits", "project=:project"},
	{"wiki_updates", "project=:project"},
	{"role_customizations", "project=:project"},
	{"run_customizations", "project=:project"},
	{"customization_conflicts", "run_id IN (SELECT id FROM runs WHERE project=:project)"},
	{"project_environments", "project_id=:project"},
	{"environment_revisions", "project_id=:project"},
	{"environment_approvals", "project_id=:project"},
	{"environment_operations", "project_id=:project"},
	{"environment_results", "project_id=:project"},
	{"environment_decisions", "project_id=:project"},
	{"run_environments", "project_id=:project"},
	{"environment_run_evidence", "run_id IN (SELECT run_id FROM run_environments WHERE project_id=:project)"},
	{"environment_storage", "project_id=:project"},
	{"environment_runtime_checks", "project_id=:project"},
	{"environment_proposals", "revision_id IN (SELECT id FROM environment_revisions WHERE project_id=:project)"},
	{"environment_candidates", "project_id=:project"},
	{"environment_associations", "project_id=:project"},
	{"repository_limits", "project=:project"},
	{"repository_runs", "run_id IN (SELECT id FROM runs WHERE project=:project)"},
	{"repository_transfers", "run_id IN (SELECT id FROM runs WHERE project=:project)"},
	{"repository_observations", "run_id IN (SELECT id FROM runs WHERE project=:project)"},
	{"version_submissions", "task_id IN (SELECT id FROM tasks WHERE project=:project)"},
	{"task_versions", "task_id IN (SELECT id FROM tasks WHERE project=:project)"},
	{"version_verdicts", "version_id IN (SELECT id FROM task_versions WHERE task_id IN (SELECT id FROM tasks WHERE project=:project))"},
	{"merge_candidates", "run_id IN (SELECT id FROM runs WHERE project=:project)"},
	{"review_versions", "task_id IN (SELECT id FROM tasks WHERE project=:project)"},
	{"review_decisions", "packet_id IN (SELECT id FROM review_versions WHERE task_id IN (SELECT id FROM tasks WHERE project=:project))"},
	{"correction_results", "task_id IN (SELECT id FROM tasks WHERE project=:project)"},
	{"merge_operations", "project=:project"},
	{"repository_executions", "project=:project"},
	{"vision_checks", "project=:project"},
	{"task_artifacts", "task_id IN (SELECT id FROM tasks WHERE project=:project)"},
	{"artifact_retention", "task_id IN (SELECT id FROM tasks WHERE project=:project)"},
}

func projectDeletionView(ctx context.Context, q Q, project string) (ProjectDeletion, error) {
	v := ProjectDeletion{ProjectID: project, Blockers: []string{}}
	err := q.QueryRowContext(ctx, `SELECT title,
		(SELECT count(*) FROM tasks WHERE project=p.id),
		(SELECT count(*) FROM review_versions v JOIN tasks t ON t.id=v.task_id WHERE t.project=p.id),
		(SELECT count(*) FROM environment_operations WHERE project_id=p.id)
		FROM projects p WHERE p.id=?`, project).Scan(&v.Title, &v.Tasks, &v.Reviews, &v.SetupOperations)
	if err == sql.ErrNoRows {
		return v, Domainf("This Project is no longer registered. Refresh the Projects page.")
	}
	if err != nil {
		return v, err
	}
	return v, nil
}

func ProjectDeletePreview(db *sql.DB, id *RunIdentity, project string) (ProjectDeletion, error) {
	if err := RequireHostScope(id, "project.delete-preview"); err != nil {
		return ProjectDeletion{}, err
	}
	v, err := projectDeletionView(context.Background(), db, project)
	if err != nil {
		return v, classify(err)
	}
	return v, nil
}

func ProjectDelete(db *sql.DB, id *RunIdentity, project string) (ProjectDeletion, error) {
	if err := RequireHostScope(id, "project.delete"); err != nil {
		return ProjectDeletion{}, err
	}
	var v ProjectDeletion
	err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		v, err = projectDeletionView(ctx, q, project)
		if err != nil {
			return err
		}
		// Foreign keys check cyclic references at commit.
		if _, err = q.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
			return err
		}
		for _, record := range projectDeletionTables {
			statement := fmt.Sprintf(`INSERT INTO project_deletion_records(project,table_name,row_id)
				SELECT :project,:table,rowid FROM %s WHERE %s`, record.table, record.where)
			if _, err = q.ExecContext(ctx, statement, sql.Named("project", project), sql.Named("table", record.table)); err != nil {
				return fmt.Errorf("identify Project records in %s: %w", record.table, err)
			}
		}
		if _, err = q.ExecContext(ctx, `UPDATE connection_health SET latest_diagnostic_id=NULL
			WHERE latest_diagnostic_id IN (SELECT row_id FROM project_deletion_records WHERE table_name='diagnostic_events')`); err != nil {
			return err
		}
		for i := len(projectDeletionTables) - 1; i >= 0; i-- {
			table := projectDeletionTables[i].table
			statement := fmt.Sprintf(`DELETE FROM %s WHERE rowid IN
				(SELECT row_id FROM project_deletion_records WHERE project=? AND table_name=?)`, table)
			if _, err = q.ExecContext(ctx, statement, project, table); err != nil {
				return fmt.Errorf("delete Project records from %s: %w", table, err)
			}
		}
		_, err = q.ExecContext(ctx, `DELETE FROM project_deletion_records WHERE project=?`, project)
		return err
	})
	return v, err
}

// ProjectContainerTargets includes completed records because their containers can remain on the host.
func ProjectContainerTargets(db *sql.DB, id *RunIdentity, project string) (map[string]any, error) {
	if err := RequireHostScope(id, "project.container-targets"); err != nil {
		return nil, err
	}
	if _, err := projectDeletionView(context.Background(), db, project); err != nil {
		return nil, err
	}
	out := map[string]any{"project_id": project}
	for key, query := range map[string]string{
		"runs": `SELECT id FROM runs WHERE project=? UNION SELECT run_id FROM repository_executions WHERE project=?`,
		"sessions": `SELECT id FROM collie_sessions WHERE vision_project_id=?
			OR packet_id IN (SELECT id FROM review_versions WHERE task_id IN (SELECT id FROM tasks WHERE project=?))`,
		"operations": `SELECT id FROM environment_operations WHERE project_id=?`,
	} {
		args := []any{project}
		if key != "operations" {
			args = append(args, project)
		}
		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		values := []string{}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return nil, err
			}
			values = append(values, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		out[key] = values
	}
	return out, nil
}
