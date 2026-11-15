package verbs

import (
	"context"
	"database/sql"

	"mc/domain"
	"mc/substrate"
)

type UpdateReconciliation struct {
	Runs         int `json:"runs"`
	Executions   int `json:"executions"`
	Sessions     int `json:"sessions"`
	Environments int `json:"environments"`
}

// ReconcileUpdate records interruption only after the updater confirms all writers stopped.
func ReconcileUpdate(spine string) (UpdateReconciliation, error) {
	var result UpdateReconciliation
	inspection, err := inspectSpineReadOnly(spine)
	if err != nil {
		return result, err
	}
	if !inspection.exists || inspection.tables == 0 {
		return result, nil
	}
	if !inspection.compatible {
		return result, Domainf("The update requires the previous release to record its interrupted work.")
	}
	db, err := substrate.Open(spine)
	if err != nil {
		return result, err
	}
	defer db.Close()
	return reconcileUpdateDB(db)
}

func updateIDs(ctx context.Context, q Q, query string) ([]string, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func reconcileUpdateDB(db *sql.DB) (UpdateReconciliation, error) {
	var result UpdateReconciliation
	err := inTx(db, func(ctx context.Context, q Q) error {
		executions, err := updateIDs(ctx, q, `SELECT run_id FROM repository_executions WHERE ended_at IS NULL ORDER BY id`)
		if err != nil {
			return err
		}
		for _, run := range executions {
			if _, err := domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: run, Reason: domain.EndOwnerChange, Stopped: true, NoCharge: true, Condition: domain.UpdateInterruptionCondition}); err != nil {
				return err
			}
			result.Executions++
		}
		// The retained apply receipts remain due. Their recovery preserves completed Git effects.
		if _, err := q.ExecContext(ctx, `UPDATE repository_executions SET stopped_at=datetime('now') WHERE ended_at IS NOT NULL AND stopped_at IS NULL`); err != nil {
			return err
		}
		runs, err := updateIDs(ctx, q, `SELECT id FROM runs WHERE tier='pipeline' AND ended_at IS NULL AND id NOT IN (SELECT run_id FROM repository_executions) ORDER BY created_seq`)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if _, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: run, Reason: "interrupted", Outcome: "interrupted", NoCharge: true}); err != nil {
				return err
			}
			result.Runs++
		}
		stopped, err := updateIDs(ctx, q, `SELECT id FROM runs WHERE tier='pipeline' AND ended_at IS NOT NULL AND outcome IS NOT NULL AND (id IN (SELECT run_id FROM leases WHERE run_id IS NOT NULL) OR id IN (SELECT run_id FROM wiki_updates WHERE stopped_at IS NULL))`)
		if err != nil {
			return err
		}
		for _, run := range stopped {
			if err := domain.AcknowledgeRunStop(ctx, q, run); err != nil {
				return err
			}
		}
		sessions, err := updateIDs(ctx, q, `SELECT id FROM collie_sessions WHERE status='active' AND current_launch_id IS NOT NULL`)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			if _, _, err := collieEndTx(ctx, q, "resident", session, "The update interrupted this session. Resume the session to continue."); err != nil {
				return err
			}
			result.Sessions++
		}
		operations, err := environmentOperationsTx(ctx, q, `WHERE status='running' ORDER BY rowid`)
		if err != nil {
			return err
		}
		for _, op := range operations {
			if err := finishEnvironmentOperationTx(ctx, q, op, "failed", "The update interrupted this operation. Retry the operation from the Environment page.", environmentHash(map[string]string{"update_interruption": op.ID})); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `UPDATE project_environments SET phase='failed' WHERE project_id=?`, op.ProjectID); err != nil {
				return err
			}
			result.Environments++
		}
		return nil
	})
	return result, err
}

func updateReceiptRecovery(ctx context.Context, q Q, project string) (bool, error) {
	var pending bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM merge_operations m
 JOIN repository_executions e ON e.run_id=m.run_id
 WHERE e.project=? AND e.end_condition=? AND m.finished=0)`, project, domain.UpdateInterruptionCondition).Scan(&pending)
	return pending, err
}
