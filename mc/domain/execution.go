package domain

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"mc/dispatch"
)

// The liveness limit of an execution, the retry time of a failed operation,
// and the grace of an ended agent run.
const (
	HeartbeatLimit = 90 * time.Second
	RetryDelay     = 60 * time.Second
	ContainerGrace = 60 * time.Second
)

// The end reasons of an execution.
const (
	EndResult            = "result"
	EndFailure           = "failure"
	EndEvaluationPending = "evaluation-pending"
	EndContainerPresent  = "container-present"
	EndOwnerChange       = "owner-change"
	EndHeartbeat         = "heartbeat"
	EndDeadline          = "deadline"
	EndVMRestart         = "vm-restart"
)

// RegisterOwner inserts the next owner number.
func RegisterOwner(ctx context.Context, q Q) (int64, error) {
	res, err := q.ExecContext(ctx, `INSERT INTO execution_owners DEFAULT VALUES`)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RequireCurrentOwner refuses an earlier owner with CodeStaleOwner.
func RequireCurrentOwner(ctx context.Context, q Q, owner int64) error {
	var current sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MAX(owner) FROM execution_owners`).Scan(&current); err != nil {
		return err
	}
	if !current.Valid || owner != current.Int64 {
		return Errf(CodeStaleOwner, "owner %d is not the current resident owner", owner)
	}
	return nil
}

// ExecutionStart is one execution that a start request records.
// Its subject is one task or one wiki update.
type ExecutionStart struct {
	RunID      string
	Project    string
	TaskID     int64
	WikiUpdate string
	Operation  string
	Target     string
	Owner      int64
	Effect     string // the JSON object that the start response lists
	Deadline   time.Duration
	Clock      VMClock
}

// Subject returns the subject of the execution.
func (s ExecutionStart) Subject() dispatch.Subject {
	return dispatch.Subject{TaskID: s.TaskID, WikiUpdate: s.WikiUpdate}
}

// SubjectOf returns the subject of a row that holds a task column and a wiki
// update column. A row with zero or two subjects is an error.
func SubjectOf(task sql.NullInt64, wiki sql.NullString) (dispatch.Subject, error) {
	if task.Valid == wiki.Valid {
		return dispatch.Subject{}, fmt.Errorf("the record of a repository operation requires one task or one wiki update")
	}
	return dispatch.Subject{TaskID: task.Int64, WikiUpdate: wiki.String}, nil
}

// StartExecution inserts the mechanical run and the active execution.
// The start is the first heartbeat.
func StartExecution(ctx context.Context, q Q, s ExecutionStart) error {
	if err := RequireCurrentOwner(ctx, q, s.Owner); err != nil {
		return err
	}
	subject := s.Subject()
	if !subject.Valid() {
		return fmt.Errorf("the repository operation requires one task or one wiki update")
	}
	var project string
	var task, wiki any
	if subject.Wiki() {
		if err := q.QueryRowContext(ctx, `SELECT project FROM wiki_updates WHERE run_id=?`, s.WikiUpdate).Scan(&project); err != nil {
			if err == sql.ErrNoRows {
				return Errf(CodeNotFound, "no wiki update %q", s.WikiUpdate)
			}
			return err
		}
		if project != s.Project {
			return Errf(CodeProjectMismatch, "wiki update %q belongs to Project %q, not %q", s.WikiUpdate, project, s.Project)
		}
		wiki = s.WikiUpdate
	} else {
		if err := q.QueryRowContext(ctx, `SELECT project FROM tasks WHERE id=?`, s.TaskID).Scan(&project); err != nil {
			if err == sql.ErrNoRows {
				return Errf(CodeNotFound, "no task %d", s.TaskID)
			}
			return err
		}
		if project != s.Project {
			return Errf(CodeProjectMismatch, "task %d belongs to Project %q, not %q", s.TaskID, project, s.Project)
		}
		task = s.TaskID
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
		VALUES(?,'pipeline','worker',?,?,?,'mechanical')`, s.RunID, s.Project, task, "sessions/"+s.RunID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO repository_executions
		(run_id,project,task_id,wiki_update,operation,target,owner,effect,boot_id,heartbeat_uptime_ms,deadline_uptime_ms)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		s.RunID, s.Project, task, wiki, s.Operation, s.Target, s.Owner, s.Effect,
		s.Clock.BootID, s.Clock.Uptime.Milliseconds(), (s.Clock.Uptime + s.Deadline).Milliseconds())
	return err
}

// ActiveExecution is the record of one active execution.
type ActiveExecution struct {
	RunID, Project, Operation, Target string
	TaskID, Owner                     int64
	WikiUpdate                        string
	StartedAt                         time.Time
}

// Subject returns the subject of the execution.
func (x ActiveExecution) Subject() dispatch.Subject {
	return dispatch.Subject{TaskID: x.TaskID, WikiUpdate: x.WikiUpdate}
}

// FenceExecution returns the active execution of the run, or CodeStaleRun.
func FenceExecution(ctx context.Context, q Q, runID string) (ActiveExecution, error) {
	var x ActiveExecution
	var started string
	var task sql.NullInt64
	var wiki sql.NullString
	err := q.QueryRowContext(ctx, `SELECT x.run_id,x.project,x.operation,x.target,x.task_id,x.wiki_update,x.owner,x.started_at
		FROM repository_executions x JOIN runs r ON r.id=x.run_id
		WHERE x.run_id=? AND x.ended_at IS NULL AND r.ended_at IS NULL AND r.outcome IS NULL`, runID).
		Scan(&x.RunID, &x.Project, &x.Operation, &x.Target, &task, &wiki, &x.Owner, &started)
	if err == sql.ErrNoRows {
		return x, Errf(CodeStaleRun, "stale run: %q has no active repository execution", runID)
	}
	if err != nil {
		return x, err
	}
	subject, err := SubjectOf(task, wiki)
	if err != nil {
		return x, err
	}
	x.TaskID, x.WikiUpdate = subject.TaskID, subject.WikiUpdate
	x.StartedAt, err = ParseSpineTime(started)
	return x, err
}

// ExecutionEnd is the end of one active execution.
type ExecutionEnd struct {
	RunID     string
	Reason    string // One End constant.
	Outcome   string // for a result: completed or obsolete
	Condition string // the operator text of a failure, or of a result of a wiki update that names a repair
	Stopped   bool   // the owner confirms that its host work stopped
	Now       time.Time
}

// ExecutionEndResult reports the charge and the retry time of an ended execution.
// An execution of a wiki update has no charge, no allowance, and no stall.
type ExecutionEndResult struct {
	TaskID     int64
	WikiUpdate string
	Charged    bool
	Failed     bool // the end is a failure of the operation
	RetryAfter *time.Time
	Remaining  int
	Stalled    bool
}

// Subject returns the subject of the ended execution.
func (r ExecutionEndResult) Subject() dispatch.Subject {
	return dispatch.Subject{TaskID: r.TaskID, WikiUpdate: r.WikiUpdate}
}

// EndExecution ends one active execution and its run in the caller's transaction.
func EndExecution(ctx context.Context, q Q, end ExecutionEnd) (ExecutionEndResult, error) {
	var res ExecutionEndResult
	outcome, runReason := end.Outcome, ""
	switch end.Reason {
	case EndResult:
		if outcome == "" {
			outcome = "completed"
		}
		if outcome != "completed" && outcome != "obsolete" {
			return res, Errf(CodeInvalidRequest, "a repository result ends as completed or obsolete")
		}
	case EndEvaluationPending:
		outcome, runReason = "obsolete", ""
	case EndOwnerChange:
		outcome, runReason = "interrupted", "interrupted"
	case EndVMRestart:
		outcome, runReason = "interrupted", "vm-restart"
	case EndFailure, EndContainerPresent, EndHeartbeat, EndDeadline:
		outcome, runReason = "infra-failed", "repository"
	default:
		return res, Errf(CodeInvalidRequest, "the execution end reason %q is not valid", end.Reason)
	}
	x, err := FenceExecution(ctx, q, end.RunID)
	if err != nil {
		return res, err
	}
	if end.Reason == EndEvaluationPending && x.Operation != "cleanup" {
		return res, Errf(CodeInvalidRequest, "An evaluation capture can defer only its repository cleanup.")
	}
	res.TaskID, res.WikiUpdate = x.TaskID, x.WikiUpdate
	wiki := x.Subject().Wiki()
	now := end.Now
	if now.IsZero() {
		if now, err = Now(ctx, q); err != nil {
			return res, err
		}
	}
	var cancelled bool
	if !wiki {
		if err := q.QueryRowContext(ctx, `SELECT dispatch_retries, decision IS 'cancelled' FROM tasks WHERE id=?`, x.TaskID).
			Scan(&res.Remaining, &cancelled); err != nil {
			return res, err
		}
		res.Stalled = res.Remaining == 0
	}

	switch end.Reason {
	case EndFailure, EndHeartbeat, EndDeadline:
		res.Failed = true
	case EndContainerPresent:
		inGrace, err := startedInContainerGrace(ctx, q, x)
		if err != nil {
			return res, err
		}
		res.Failed = !inGrace
	case EndOwnerChange:
		var previous sql.NullString
		err := q.QueryRowContext(ctx, `SELECT end_reason FROM repository_executions
			WHERE target=? AND ended_at IS NOT NULL ORDER BY id DESC LIMIT 1`, x.Target).Scan(&previous)
		if err != nil && err != sql.ErrNoRows {
			return res, err
		}
		res.Failed = previous.String == EndOwnerChange
	}
	// A failure of a wiki update uses no failure allowance. A capture of a
	// cancelled task repeats until it succeeds, so the charge limits its retries.
	charge := res.Failed && !wiki && (!cancelled || x.Operation == "capture")

	var retry any
	if end.Reason != EndResult {
		after := now.UTC().Add(RetryDelay)
		res.RetryAfter = &after
		retry = after.Format(SpineTime)
	}
	if charge {
		condition := executionCondition(end, x.Operation)
		if _, err := q.ExecContext(ctx, `INSERT INTO infrastructure_failures(run_id,task_id,allowance,condition,source,cause)
			VALUES(?,?,'task',?,'resident','unknown')`, x.RunID, x.TaskID, condition); err != nil {
			return res, err
		}
		before := InfraBudget - res.Remaining
		allowance, err := ChargeInfra(ctx, q, x.TaskID, condition, x.RunID)
		if err != nil {
			return res, err
		}
		res.Charged = true
		res.Remaining = allowance.Remaining
		res.Stalled = allowance.Stalled
		if _, err := AppendDiagnostic(ctx, q, Diagnostic{Source: "resident", Operation: "run", Outcome: "failed",
			RunID: x.RunID, ProjectID: x.Project, Condition: condition,
			CountBefore: before, CountAfter: InfraBudget - res.Remaining}, ""); err != nil {
			return res, err
		}
	}
	if x.Operation == "capture" && !wiki {
		if _, err := q.ExecContext(ctx, `UPDATE repository_transfers SET finished=1 WHERE run_id=? AND finished=0`, x.RunID); err != nil {
			return res, err
		}
	}
	// A failed capture or a failed merge candidate step ends the wiki update as failed.
	// The schema permits the write of a merge candidate step only while this execution is active.
	wikiFails := wiki && res.Failed && (x.Operation == "capture" || x.Operation == "prepare")
	if wikiFails {
		if _, err := q.ExecContext(ctx, `UPDATE merge_operations SET finished=1,result='{"status":"interrupted"}'
			WHERE wiki_update=? AND operation='prepare' AND finished=0`, x.WikiUpdate); err != nil {
			return res, err
		}
	}
	if _, err := q.ExecContext(ctx, `UPDATE runs SET ended_at=datetime('now'), outcome=?, end_reason=? WHERE id=?`,
		outcome, nullIfEmpty(runReason), x.RunID); err != nil {
		return res, err
	}
	var stopped any
	if end.Stopped {
		stopped = now.UTC().Format(SpineTime)
	}
	charged := 0
	if charge {
		charged = 1
	}
	// The record of a wiki update keeps the condition of each end with no
	// result, and the condition that a result names.
	var condition any
	if wiki {
		text := cutCondition(strings.TrimSpace(end.Condition))
		if end.Reason != EndResult {
			text = executionCondition(end, x.Operation)
		}
		condition = nullIfEmpty(text)
	}
	if _, err := q.ExecContext(ctx, `UPDATE repository_executions SET ended_at=datetime('now'), end_reason=?,
		retry_after=?, charged=?, stopped_at=?, end_condition=? WHERE run_id=?`, end.Reason, retry, charged, stopped, condition, x.RunID); err != nil {
		return res, err
	}
	if wikiFails {
		cause := wikiCaptureCause
		if x.Operation == "prepare" {
			cause = wikiCandidateCause
		}
		return res, EndWikiUpdate(ctx, q, x.WikiUpdate, WikiEnd{Result: "failed", Cause: cause + executionCondition(end, x.Operation)})
	}
	return res, nil
}

// The causes of a wiki update that a capture or a merge candidate step fails.
// The condition of the ended execution follows each text.
const (
	wikiCaptureCause   = "Culler did not capture the wiki commit. "
	wikiCandidateCause = "Culler did not prepare the merge candidate of the wiki commit. "
)

// startedInContainerGrace reports whether the execution started before the
// grace of its ended target run ended.
func startedInContainerGrace(ctx context.Context, q Q, x ActiveExecution) (bool, error) {
	var ended sql.NullString
	err := q.QueryRowContext(ctx, `SELECT ended_at FROM runs WHERE id=?`, x.Target).Scan(&ended)
	if err == sql.ErrNoRows || (err == nil && !ended.Valid) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	end, err := ParseSpineTime(ended.String)
	if err != nil {
		return false, err
	}
	return x.StartedAt.Before(end.Add(ContainerGrace)), nil
}

// executionCondition returns the operator text of an end with no result.
func executionCondition(end ExecutionEnd, operation string) string {
	condition := strings.TrimSpace(end.Condition)
	if condition == "" {
		switch end.Reason {
		case EndHeartbeat:
			condition = fmt.Sprintf("The resident sent no heartbeat for %d seconds.", int(HeartbeatLimit.Seconds()))
		case EndDeadline:
			if operation == "prepare" || operation == "apply" {
				condition = "The merge step exceeded its 10-minute limit."
			} else {
				condition = "The repository operation exceeded its 5-minute limit."
			}
		case EndOwnerChange:
			condition = "A resident restart ended the repository operation."
		case EndContainerPresent:
			condition = "A container of the ended run is present."
		default:
			condition = "The repository operation failed."
		}
	}
	return cutCondition(condition)
}

// cutCondition cuts an operator text to the limit of a stored condition.
func cutCondition(condition string) string {
	if utf8.RuneCountInString(condition) > 2048 {
		condition = string([]rune(condition)[:2048])
	}
	return condition
}

// HeartbeatExecutions renews each listed active execution of the owner on the current boot.
func HeartbeatExecutions(ctx context.Context, q Q, owner int64, runs []string, clock VMClock) ([]string, error) {
	renewed := []string{}
	seen := map[string]bool{}
	for _, run := range runs {
		if seen[run] {
			continue
		}
		seen[run] = true
		res, err := q.ExecContext(ctx, `UPDATE repository_executions SET heartbeat_uptime_ms=?
			WHERE run_id=? AND owner=? AND ended_at IS NULL AND boot_id=?`, clock.Uptime.Milliseconds(), run, owner, clock.BootID)
		if err != nil {
			return nil, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return nil, err
		} else if n == 1 {
			renewed = append(renewed, run)
		}
	}
	return renewed, nil
}

// ConfirmStops records the stop of each listed ended execution that the owner can confirm.
// It returns the confirmed runs. A run with a recorded stop is not an error.
// The owner confirms its own execution at once. The execution of an earlier
// owner requires 90 seconds without a heartbeat, or a boot change. A merge flow
// sends its last heartbeat before its container starts, so that flow cannot
// start a container after the confirmation.
func ConfirmStops(ctx context.Context, q Q, owner int64, runs []string, clock VMClock, now time.Time) ([]string, error) {
	confirmed := []string{}
	seen := map[string]bool{}
	stamp := now.UTC().Format(SpineTime)
	for _, run := range runs {
		if seen[run] {
			continue
		}
		seen[run] = true
		res, err := q.ExecContext(ctx, `UPDATE repository_executions SET stopped_at=?
			WHERE run_id=? AND ended_at IS NOT NULL AND stopped_at IS NULL
			  AND (owner=? OR (owner<? AND (boot_id<>? OR ?-heartbeat_uptime_ms>=?)))`,
			stamp, run, owner, owner, clock.BootID, clock.Uptime.Milliseconds(), HeartbeatLimit.Milliseconds())
		if err != nil {
			return nil, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return nil, err
		} else if n == 1 {
			confirmed = append(confirmed, run)
		}
	}
	return confirmed, nil
}

// SweepExecutions ends each active execution that exceeds a liveness limit.
// It returns the ended runs and records no stop. An execution of a wiki
// update whose end returns an error stays active, and the next sweep ends it.
func SweepExecutions(ctx context.Context, q Q, owner int64, clock VMClock, now time.Time) ([]string, error) {
	if err := RequireCurrentOwner(ctx, q, owner); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT run_id,owner,boot_id,heartbeat_uptime_ms,deadline_uptime_ms,wiki_update IS NOT NULL
		FROM repository_executions WHERE ended_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type active struct {
		run, boot           string
		owner               int64
		heartbeat, deadline int64
		wiki                bool
	}
	var executions []active
	for rows.Next() {
		var a active
		if err := rows.Scan(&a.run, &a.owner, &a.boot, &a.heartbeat, &a.deadline, &a.wiki); err != nil {
			rows.Close()
			return nil, err
		}
		executions = append(executions, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	uptime := clock.Uptime.Milliseconds()
	ended := []string{}
	for _, a := range executions {
		reason := ""
		switch {
		case a.boot != clock.BootID:
			// A host restart also changes the owner. It never charges the task.
			reason = EndVMRestart
		case a.owner < owner:
			reason = EndOwnerChange
		case uptime-a.heartbeat >= HeartbeatLimit.Milliseconds():
			reason = EndHeartbeat
		case uptime >= a.deadline:
			reason = EndDeadline
		default:
			continue
		}
		end := ExecutionEnd{RunID: a.run, Reason: reason, Now: now}
		if a.wiki {
			done, err := endWikiExecutionInSweep(ctx, q, end)
			if err != nil {
				return nil, err
			}
			if !done {
				continue
			}
		} else if _, err := EndExecution(ctx, q, end); err != nil {
			return nil, err
		}
		ended = append(ended, a.run)
	}
	return ended, nil
}

// endWikiExecutionInSweep ends one execution of a wiki update in its own
// savepoint. An error of the end rolls the savepoint back, so an error of a
// wiki update fails no start request. It reports whether the execution ended.
func endWikiExecutionInSweep(ctx context.Context, q Q, end ExecutionEnd) (bool, error) {
	if _, err := q.ExecContext(ctx, `SAVEPOINT wiki_execution_end`); err != nil {
		return false, err
	}
	_, endErr := EndExecution(ctx, q, end)
	if endErr != nil {
		if _, err := q.ExecContext(ctx, `ROLLBACK TO wiki_execution_end`); err != nil {
			return false, err
		}
	}
	if _, err := q.ExecContext(ctx, `RELEASE wiki_execution_end`); err != nil {
		return false, err
	}
	return endErr == nil, nil
}
