package domain

import (
	"context"
	"time"
)

const InfraBudget = 3

type ChargeResult struct {
	Remaining int
	Stalled   bool
	Blocked   bool // Infrastructure failures never create an operator block.
}

func ChargeInfra(ctx context.Context, q Q, taskID int64, reason, runID string) (ChargeResult, error) {
	var res ChargeResult
	var retries int
	if err := q.QueryRowContext(ctx, `SELECT dispatch_retries FROM tasks WHERE id = ?`, taskID).Scan(&retries); err != nil {
		return res, Errf(CodeNotFound, "no task %d", taskID)
	}
	remaining := retries - 1
	if remaining < 0 {
		remaining = 0
	}
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET dispatch_retries = ?,run_retry_at=NULL WHERE id = ?`, remaining, taskID); err != nil {
		return res, err
	}
	res.Remaining = remaining
	res.Stalled = remaining == 0
	return res, nil
}

func ClearInfra(ctx context.Context, q Q, taskID int64) error {
	_, err := q.ExecContext(ctx,
		`UPDATE tasks SET dispatch_retries = ?,run_retry_at=NULL WHERE id = ? AND dispatch_retries <> ?`,
		InfraBudget, taskID, InfraBudget)
	return err
}

func taskRetryAt(remaining int) any {
	if remaining == 0 {
		return nil
	}
	return time.Now().UTC().Add(ConnectionRetryDelay(InfraBudget - remaining)).Format(time.RFC3339)
}
