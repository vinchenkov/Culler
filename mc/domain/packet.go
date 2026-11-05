package domain

import "context"

// SelectReview reserves capacity for the exact review of the current task version.
func SelectReview(ctx context.Context, q Q, taskID, reviewID int64) error {
	r, err := getTask(ctx, q, taskID)
	if err != nil {
		return err
	}
	if err := requireLive(r); err != nil {
		return err
	}
	if r.Status != "packaged" {
		return Errf(CodeNotPackaged, "A review requires packaged work.")
	}
	var current bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM review_versions p JOIN tasks t ON t.id=p.task_id
 WHERE p.id=? AND p.task_id=? AND p.version_id=t.submitted_version AND p.requirements_version=t.requirements_version)`, reviewID, taskID).Scan(&current); err != nil {
		return err
	}
	if !current {
		return Errf(CodeStaleRun, "The review does not identify the current task version.")
	}

	held := true
	if err := RequireNoRequirementsHold(ctx, q, taskID); err != nil {
		if rejection, ok := err.(*DomainError); !ok || rejection.Code != CodeRevisionPending {
			return err
		}
		held = false
	}
	var priorHeld bool
	if err := q.QueryRowContext(ctx, `SELECT review_slot_held FROM tasks WHERE id=?`, taskID).Scan(&priorHeld); err != nil {
		return err
	}
	if held && !priorHeld {
		occupancy, err := Occupancy(ctx, q, r.Project)
		if err != nil {
			return err
		}
		var capacity int
		if err := q.QueryRowContext(ctx, `SELECT review_wip_cap FROM settings WHERE id=1`).Scan(&capacity); err != nil {
			return err
		}
		if occupancy >= capacity {
			return Errf(CodeWIPCap, "The review queue reached its capacity.")
		}
	}
	_, err = q.ExecContext(ctx, `UPDATE tasks SET current_review_id=?,review_slot_held=? WHERE id=?`, reviewID, held, taskID)
	return err
}
