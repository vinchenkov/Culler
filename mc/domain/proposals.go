package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// RequireOperatorTaskControl keeps Proposed work under Arbiter authority.
func RequireOperatorTaskControl(ctx context.Context, q Q, task int64) error {
	var stage string
	var archived bool
	var decision sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT status,archived,decision FROM tasks WHERE id=?`, task).Scan(&stage, &archived, &decision); err != nil {
		if err == sql.ErrNoRows {
			return Errf(CodeNotFound, "no task %d", task)
		}
		return err
	}
	if stage == "proposed" {
		if archived || decision.Valid {
			return Errf(CodeAlreadyDecided, "The finalized record is read-only.")
		}
		return Errf(CodeEditRestricted, "Culler permits operator changes after a task leaves Proposed.")
	}
	return nil
}

// RecordProposalDecision binds one question to the run's affected proposals.
func RecordProposalDecision(ctx context.Context, q Q, run, question string, affected []int64) (int64, error) {
	if strings.TrimSpace(question) == "" {
		return 0, Errf(CodeReasonRequired, "The proposal decision requires a question.")
	}
	subject, err := Fence(ctx, q, run)
	if err != nil {
		return 0, err
	}
	var project, role string
	var snapshot sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT project,role,pool_snapshot FROM runs WHERE id=?`, run).Scan(&project, &role, &snapshot); err != nil {
		return 0, err
	}
	if subject != nil || role != "strategist" && role != "arbiter" {
		return 0, Errf(CodeIllegalTransition, "The run does not belong to Project proposal work.")
	}
	pool := []int64{}
	if snapshot.Valid {
		if err := json.Unmarshal([]byte(snapshot.String), &pool); err != nil {
			return 0, err
		}
	}
	if affected == nil {
		affected = pool
	}
	assigned := map[int64]bool{}
	for _, task := range pool {
		assigned[task] = true
	}
	seen := map[int64]bool{}
	for _, task := range affected {
		if !assigned[task] || seen[task] {
			return 0, Errf(CodePoolMismatch, "Each affected proposal must occur once in the run assignment.")
		}
		seen[task] = true
	}
	return RecordProjectProposalDecision(ctx, q, project, run, question, affected)
}

// RecordProjectProposalDecision records a proposal decision before or during a run.
func RecordProjectProposalDecision(ctx context.Context, q Q, project, run, question string, affected []int64) (int64, error) {
	if strings.TrimSpace(question) == "" {
		return 0, Errf(CodeReasonRequired, "The proposal decision requires a question.")
	}
	var runID any
	if run != "" {
		runID = run
	}
	result, err := q.ExecContext(ctx, `INSERT INTO proposal_decisions(project,run_id,question) VALUES(?,?,?)`, project, runID, question)
	if err != nil {
		return 0, err
	}
	decision, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, task := range affected {
		result, err := q.ExecContext(ctx, `INSERT INTO proposal_decision_tasks(decision_id,task_id,owns_block)
 SELECT ?,id,blocked=0 FROM tasks WHERE id=? AND project=? AND status='proposed' AND archived=0`, decision, task, project)
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if n != 1 {
			return 0, Errf(CodePoolMismatch, "The affected proposal is outside the current Project assignment.")
		}
		if _, err := q.ExecContext(ctx, `UPDATE tasks SET blocked=1,blocked_kind='proposal',blocked_reason=? WHERE id=? AND blocked=0`, question, task); err != nil {
			return 0, err
		}
	}
	return decision, nil
}

func ResolveProposalDecision(ctx context.Context, q Q, project string, decision int64, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return Errf(CodeReasonRequired, "The decision resolution requires a reason.")
	}
	var question string
	var resolved sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT question,resolved_at FROM proposal_decisions WHERE id=? AND project=?`, decision, project).Scan(&question, &resolved); err != nil {
		return err
	}
	if resolved.Valid {
		return nil
	}
	if _, err := q.ExecContext(ctx, `UPDATE proposal_decisions SET resolved_at=datetime('now'),resolution=? WHERE id=?`, reason, decision); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE tasks SET blocked=0,blocked_reason=NULL,blocked_kind=NULL WHERE blocked_reason=? AND blocked_kind='proposal' AND id IN
 (SELECT task_id FROM proposal_decision_tasks WHERE decision_id=? AND owns_block=1)`, question, decision)
	return err
}
