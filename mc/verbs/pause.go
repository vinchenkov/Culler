package verbs

import (
	"context"
	"database/sql"

	"mc/substrate"
)

type PipelinePauseStatus struct {
	Projects []PipelineProjectPause `json:"projects"`
}

type PipelineProjectPause struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	SetupStatus string `json:"setup_status"`
}

// PauseStatus reads the pause state of every Project from one database snapshot.
func PauseStatus(db *sql.DB) (any, error) {
	return pauseStatus(context.Background(), db)
}

func pauseStatus(ctx context.Context, q Q) (PipelinePauseStatus, error) {
	result := PipelinePauseStatus{Projects: []PipelineProjectPause{}}
	rows, err := q.QueryContext(ctx, `SELECT id, title, status, setup_status FROM projects ORDER BY id`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var row PipelineProjectPause
		if err := rows.Scan(&row.ID, &row.Title, &row.Status, &row.SetupStatus); err != nil {
			return result, err
		}
		result.Projects = append(result.Projects, row)
	}
	return result, rows.Err()
}

// SetEveryProjectPause pauses every active Project, or resumes every paused
// Project whose setup is ready. Pipeline agents cannot change a pause.
func SetEveryProjectPause(db *sql.DB, id *RunIdentity, paused bool) (any, error) {
	if err := RequireHostScope(id, "pipeline pause control"); err != nil {
		return nil, err
	}
	update := `UPDATE projects SET status = 'active' WHERE status = 'paused' AND setup_status = 'ready'`
	if paused {
		update = `UPDATE projects SET status = 'paused' WHERE status = 'active'`
	}
	var result PipelinePauseStatus
	err := inTx(db, func(ctx context.Context, q Q) error {
		if paused {
			if _, err := q.ExecContext(ctx, `UPDATE project_environments SET pause_after_setup=1 WHERE project_id IN (SELECT id FROM projects WHERE status<>'archived' AND setup_status<>'ready')`); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, update); err != nil {
			return err
		}
		if err := substrate.ValidateDispatchMountProjection(ctx, q); err != nil {
			return err
		}
		var err error
		result, err = pauseStatus(ctx, q)
		return err
	})
	return result, err
}
