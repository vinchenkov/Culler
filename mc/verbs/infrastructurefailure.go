package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"mc/domain"
)

func RunProcessStarted(db *sql.DB, id *RunIdentity, run string) (any, error) {
	if err := requirePipeline(id); err != nil {
		return nil, err
	}
	if err := requireOwnRun(id, run); err != nil {
		return nil, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if _, err := domain.FenceAgentRun(ctx, q, run); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE runs SET agent_started_at=COALESCE(agent_started_at,datetime('now')) WHERE id=?`, run)
		return err
	})
	return map[string]any{"run_id": run, "agent_started": err == nil}, err
}

func resumeTaskTx(ctx context.Context, q Q, task int64, reason, actor string) error {
	if strings.TrimSpace(reason) == "" {
		return domain.Errf(domain.CodeReasonRequired, "Task resume requires a reason.")
	}
	if err := domain.RequireOperatorTaskControl(ctx, q, task); err != nil {
		return err
	}
	var stalled, archived bool
	if err := q.QueryRowContext(ctx, `SELECT stalled,archived FROM tasks WHERE id=?`, task).Scan(&stalled, &archived); err != nil {
		return err
	}
	if archived || !stalled {
		return Domainf("The task has no current Retries Exhausted condition.")
	}
	if err := domain.ClearInfra(ctx, q, task); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES(?,'task.resumed',?,?)`, actor, task, reason)
	return err
}

func ProposalDecisionResolve(db *sql.DB, id *RunIdentity, project string, decision int64, reason string) (any, error) {
	if err := RequireOperatorVerb(id, "system.resolve-proposals"); err != nil {
		return nil, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := AuthorizeOperator(ctx, q, id, "system.resolve-proposals", project); err != nil {
			return err
		}
		return domain.ResolveProposalDecision(ctx, q, project, decision, reason)
	})
	return map[string]any{"project_id": project, "decision_id": decision, "resolved": err == nil}, err
}

// RunInfrastructureFailure records facts from an agent or an execution observer.
func RunInfrastructureFailure(db *sql.DB, id *RunIdentity, runID string, report domain.InfrastructureFailureReport) (any, error) {
	if runID == "" {
		return nil, Usagef("an infrastructure report requires a run identity")
	}
	if report.Source == "" {
		report.Source = "agent"
	}
	if id != nil {
		if err := requirePipeline(id); err != nil {
			return nil, err
		}
		if err := requireOwnRun(id, runID); err != nil {
			return nil, err
		}
		if report.Source == "resident" {
			return nil, Domainf("the resident reports from the host")
		}
	}
	if err := domain.ValidateInfrastructureFailureReport(report); err != nil {
		return nil, err
	}
	var result domain.InfrastructureFailureResult
	err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		if report.ConnectionRefused, err = wikiConnectionRefused(ctx, q, runID); err != nil {
			return err
		}
		result, err = domain.RecordInfrastructureFailure(ctx, q, runID, report)
		return err
	})
	if err != nil {
		return nil, err
	}
	if result.WikiUpdate {
		return map[string]any{"run_id": runID, "wiki_update": true, "charged": false, "project_id": result.ProjectID}, nil
	}
	resultMap := map[string]any{"run_id": runID, "task_id": result.TaskID, "charged": result.Charged,
		"allowance_owner": result.Allowance, "project_id": result.ProjectID, "remaining": result.Remaining, "exhausted": result.Stalled}
	if result.Allowance == "task" {
		resultMap["dispatch_retries"] = result.Remaining
		resultMap["stalled"] = result.Stalled
	}
	return resultMap, nil
}

// wikiConnectionRefused reports whether the connection of a wiki update run
// refuses the run now. It uses the admission that the connection check of the
// start and the launch check use. A task run gets false.
func wikiConnectionRefused(ctx context.Context, q Q, run string) (bool, error) {
	var connection, authorization, profile, profileRevision string
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT r.connection_id,r.authorization_id,r.profile_id,r.profile_revision,r.connection_revision
		FROM runs r JOIN wiki_updates w ON w.run_id=r.id WHERE r.id=?`, run).Scan(&connection, &authorization, &profile, &profileRevision, &revision)
	if err == sql.ErrNoRows || (err == nil && connection == "") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now, err := spineClock(ctx, q)
	if err != nil {
		return false, err
	}
	current, currentRevision, err := ConnectionAdmission(ctx, q, connection, profile, now, profileRevision)
	var refused *DomainError
	if errors.As(err, &refused) {
		return true, nil
	}
	return current != authorization || currentRevision != revision, err
}

// RunStopped accepts the resident's confirmation that the run containers stopped.
func RunStopped(db *sql.DB, id *RunIdentity, runID string) (any, error) {
	if err := RequireHostScope(id, "mc run stopped"); err != nil {
		return nil, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		return domain.AcknowledgeRunStop(ctx, q, runID)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"run_id": runID, "stopped": true}, nil
}

const latestInfrastructureFailureSQL = `(SELECT json_object(
    'run_id', f.run_id, 'task_id', f.task_id, 'condition', f.condition,
    'source', f.source, 'cause', f.cause, 'evidence', f.evidence, 'allowance',f.allowance,
    'operator_action', f.operator_action, 'quota_reset_at', f.quota_reset_at,
    'failed_at', f.failed_at)
    FROM infrastructure_failures f JOIN runs r ON r.id = f.run_id
    WHERE f.task_id = tasks.id AND f.allowance='task' ORDER BY f.failed_at DESC, r.created_seq DESC LIMIT 1)
    AS latest_infrastructure_failure`

// connectionWaitSQL names the connection that stops the next run of the task.
const projectInfrastructureSQL = `(SELECT json_object('failure_count',p.infrastructure_failure_count,
 'remaining',3-p.infrastructure_failure_count,'retry_at',p.infrastructure_next_retry_at,'condition',p.infrastructure_condition)
 FROM projects p WHERE p.id=tasks.project AND p.infrastructure_failure_count>0) AS project_infrastructure,
 (SELECT d.id FROM proposal_decisions d JOIN proposal_decision_tasks a ON a.decision_id=d.id
 WHERE a.task_id=tasks.id AND d.resolved_at IS NULL) AS proposal_decision_id`

const connectionWaitSQL = `(SELECT p.connection_wait FROM projects p
    WHERE p.id = tasks.project AND p.connection_wait_task = tasks.id) AS connection_wait`

func taskRowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	tasks, err := rowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if raw, ok := task["delivery"].(string); ok {
			var delivery map[string]any
			if err := json.Unmarshal([]byte(raw), &delivery); err != nil {
				return nil, err
			}
			task["delivery"] = delivery
		}
		if raw, ok := task["latest_infrastructure_failure"].(string); ok {
			var report map[string]any
			if err := json.Unmarshal([]byte(raw), &report); err != nil {
				return nil, err
			}
			task["latest_infrastructure_failure"] = report
		}
		if raw, ok := task["project_infrastructure"].(string); ok {
			var condition map[string]any
			if err := json.Unmarshal([]byte(raw), &condition); err != nil {
				return nil, err
			}
			task["project_infrastructure"] = condition
		}
	}
	return tasks, nil
}
