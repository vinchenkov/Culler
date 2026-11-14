package domain

import (
	"context"
	"time"
)

const ProjectInfrastructureBudget = 3

// ChargeProjectInfrastructure records one failure under the Project allowance.
func ChargeProjectInfrastructure(ctx context.Context, q Q, project, reason, run string) (ChargeResult, error) {
	var count int
	if err := q.QueryRowContext(ctx, `SELECT infrastructure_failure_count FROM projects WHERE id=?`, project).Scan(&count); err != nil {
		return ChargeResult{}, err
	}
	count++
	if count > ProjectInfrastructureBudget {
		count = ProjectInfrastructureBudget
	}
	var retry any
	if count < ProjectInfrastructureBudget {
		retry = time.Now().UTC().Add(ConnectionRetryDelay(count)).Format(time.RFC3339)
	}
	_, err := q.ExecContext(ctx, `UPDATE projects SET infrastructure_failure_count=?,infrastructure_next_retry_at=?,infrastructure_last_failed_run_id=NULLIF(?,''),infrastructure_condition=? WHERE id=?`, count, retry, run, reason, project)
	return ChargeResult{Remaining: ProjectInfrastructureBudget - count, Stalled: count == ProjectInfrastructureBudget}, err
}
