package verbs

import (
	"context"
	"mc/dispatch"
)

func loadRequirementsRecords(ctx context.Context, q Q, rec *dispatch.Records) error {
	rows, err := q.QueryContext(ctx, `SELECT t.id,t.requirements_version,
 EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.task_id=t.id AND r.state='pending')
 FROM tasks t WHERE t.project=?`, rec.Project)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, version int64
		var pending bool
		if err := rows.Scan(&taskID, &version, &pending); err != nil {
			return err
		}
		for i := range rec.Tasks {
			task := &rec.Tasks[i]
			if task.ID != taskID {
				continue
			}
			task.RequirementsVersion = version
			task.ExecutionHeld = pending && task.Decision == dispatch.DecisionNone && !task.Archived
		}
	}
	return rows.Err()
}
