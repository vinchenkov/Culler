package domain

import (
	"context"
	"fmt"
	"strings"
)

func returnToSeeded(ctx context.Context, q Q, task int64, kind, reason string) error {
	_, err := q.ExecContext(ctx, `UPDATE tasks SET decision=NULL,decided_at=NULL,status='seeded',revision_notes=?,
 correction_open=1,returned_kind=?,candidate_id=NULL,report_path=NULL,thesis=NULL,merge_after=NULL,
 source_version=NULL WHERE id=?`, reason, kind, task)
	return err
}

func RequestPacketRevision(ctx context.Context, q Q, task int64, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return Errf(CodeReasonRequired, "The packet revision requires a reason.")
	}
	var version int64
	if err := q.QueryRowContext(ctx, `SELECT record_version FROM tasks WHERE id=?`, task).Scan(&version); err != nil {
		return err
	}
	_, err := RequestTaskRevision(ctx, q, task, version, fmt.Sprintf("packet-revision:%d:%d", task, version), nil, nil, reason, "operator")
	return err
}

// blockAllowance records the allowance limit. It sets Decision Pending only
// when the task has no other Decision Pending condition.
func blockAllowance(ctx context.Context, q Q, task int64, condition, reason string) error {
	_, err := q.ExecContext(ctx, `UPDATE tasks SET allowance_condition=?,allowance_reason=?,allowance_version=submitted_version,
 blocked_kind=CASE WHEN blocked=0 THEN 'allowance' ELSE blocked_kind END,
 blocked_reason=CASE WHEN blocked=0 THEN ? ELSE blocked_reason END,blocked=1 WHERE id=?`, condition, reason, reason, task)
	return err
}

func clearAllowance(ctx context.Context, q Q, task int64, kind string) error {
	var valid bool
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(allowance_condition=? AND allowance_version=submitted_version AND archived=0,0)
 FROM tasks WHERE id=?`, kind, task).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return Errf(CodeIllegalTransition, "The task requires the current unresolved allowance limit.")
	}
	_, err := q.ExecContext(ctx, `UPDATE tasks SET blocked=CASE WHEN blocked_kind='allowance' THEN 0 ELSE blocked END,
 blocked_kind=CASE WHEN blocked_kind='allowance' THEN NULL ELSE blocked_kind END,
 allowance_condition=NULL,allowance_reason=NULL,allowance_version=NULL WHERE id=?`, task)
	return err
}

func GrantCorrectionAllowance(ctx context.Context, q Q, task int64, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return Errf(CodeReasonRequired, "The allowance requires an operator explanation.")
	}
	if err := RequireOperatorTaskControl(ctx, q, task); err != nil {
		return err
	}
	var report string
	if err := q.QueryRowContext(ctx, `SELECT correction_reason FROM tasks WHERE id=? AND correction_count=3`, task).Scan(&report); err != nil {
		return err
	}
	if err := clearAllowance(ctx, q, task, "verifier"); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET correction_count=0,verifier_correction_open=1,correction_operator_reason=? WHERE id=?`, reason, task); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES('operator','task.correction_allowance',?,json_object('reason',?,'attempts',3))`, task, reason); err != nil {
		return err
	}
	return returnToSeeded(ctx, q, task, "verifier", report)
}

// RequestMergeRepair returns the task to the pipeline for a merge repair of
// the conflict. Each conflict starts a merge repair.
func RequestMergeRepair(ctx context.Context, q Q, task, candidate int64) error {
	var body string
	if err := q.QueryRowContext(ctx, `SELECT c.result FROM tasks t JOIN merge_candidates c ON c.id=?
 WHERE t.id=? AND c.task_id=t.id AND c.version_id=t.submitted_version AND c.status='conflict' AND t.archived=0`, candidate, task).Scan(&body); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET correction_reason=?,correction_operator_reason=NULL WHERE id=?`, body, task); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES('mc','task.merge_repair',?,json_object('candidate',?))`, task, candidate); err != nil {
		return err
	}
	return returnToSeeded(ctx, q, task, "merge", body)
}

// CloseCorrection ends the open correction of the task at a merge candidate without a conflict.
func CloseCorrection(ctx context.Context, q Q, task int64) error {
	_, err := q.ExecContext(ctx, `UPDATE tasks SET correction_open=0 WHERE id=?`, task)
	return err
}
