package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

type RepositoryLimits struct {
	QuietSeconds   int
	TemporaryBytes int64
}

// ConfigureRepositoryLimits sets the destination quiet interval and the
// temporary repository limit of a Project. A zero value keeps the current value.
func ConfigureRepositoryLimits(db *sql.DB, id *RunIdentity, project string, limit RepositoryLimits) (any, error) {
	if err := RequireHostScope(id, "mc delivery limits"); err != nil {
		return nil, err
	}
	if limit.QuietSeconds < 0 || limit.QuietSeconds > 3600 || limit.TemporaryBytes < 0 {
		return nil, Usagef("the quiet interval must be at most 3600 seconds, and repository limits must be positive")
	}
	var limits RepositoryLimits
	err := inTx(db, func(ctx context.Context, q Q) error {
		if _, err := q.ExecContext(ctx, `UPDATE repository_limits SET destination_quiet_seconds=CASE WHEN ?=0 THEN destination_quiet_seconds ELSE ? END,
		 max_temporary_bytes=CASE WHEN ?=0 THEN max_temporary_bytes ELSE ? END WHERE project=?`, limit.QuietSeconds, limit.QuietSeconds, limit.TemporaryBytes, limit.TemporaryBytes, project); err != nil {
			return err
		}
		err := q.QueryRowContext(ctx, `SELECT destination_quiet_seconds,max_temporary_bytes FROM repository_limits WHERE project=?`, project).Scan(&limits.QuietSeconds, &limits.TemporaryBytes)
		if err == sql.ErrNoRows {
			return Domainf("no Project %q", project)
		}
		return err
	})
	return map[string]any{"project": project, "quiet_seconds": limits.QuietSeconds, "max_temporary_bytes": limits.TemporaryBytes}, err
}

func DeliveryAllowance(db *sql.DB, id *RunIdentity, task int64, reason string) (any, error) {
	if err := RequireHostScope(id, "mc delivery allow-corrections"); err != nil {
		return nil, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error { return domain.GrantCorrectionAllowance(ctx, q, task, reason) })
	return map[string]any{"task_id": task, "recorded": err == nil}, err
}

var deliveryTaskSQL = `(SELECT json_object(
 'input_commit',d.input_commit,
 'correction_count',d.correction_count,
 'verifier_correction_open',d.verifier_correction_open,
 'allowance_condition',d.allowance_condition,'allowance_reason',d.allowance_reason,
 'correction_operator_reason',d.correction_operator_reason,
 'latest_conflict',json((SELECT result FROM merge_candidates c WHERE c.task_id=d.id AND c.status='conflict' ORDER BY c.id DESC LIMIT 1)),
 'verifier_report',json((SELECT json_object('problem',v.problem,'evidence',v.explanation_evidence,'resolution',v.resolution,'report_path',run.correction_path)
 FROM version_verdicts v JOIN runs run ON run.id=v.run_id WHERE run.subject=d.id AND v.outcome='correct' ORDER BY v.version_id DESC LIMIT 1)),
 'project_priority',d.archived=0 AND d.decision IS 'approved',
 'application_pending',EXISTS(SELECT 1 FROM merge_operations m WHERE m.task_id=d.id AND m.operation='apply' AND m.finished=0),
 'source_verified_commit',source.result_commit,
 'checkpoint_version',d.checkpoint_version,'version_id',d.submitted_version,'submitted_commit',v.result_commit,'destination_branch',d.destination_branch,
 'artifact_retention',json((SELECT json_object('status',CASE WHEN f.expired_at IS NOT NULL THEN 'expired' WHEN a.token IS NOT NULL THEN 'expiry_pending' ELSE 'retained' END,'expires_at',a.expires_at,'expired_at',f.expired_at,'retention_days',` + strconv.Itoa(ArtifactRetentionDays) + `) FROM artifact_retention a LEFT JOIN task_artifacts f ON f.task_id=a.task_id AND f.path=d.report_path WHERE a.task_id=d.id)),
 'report_path',d.report_path,'report_sha256',d.report_sha256,'correction_reason',d.correction_reason,'merge_after',d.merge_after,
 'temporary_repositories',json((SELECT json_group_array(json_object('run_id',r.run_id,'bytes',r.temporary_bytes,'cleanup_error',r.cleanup_error)) FROM repository_runs r WHERE r.task_id=d.id AND r.cleaned=0)),
 'application_cleanup_errors',json((SELECT json_group_array(json_object('run_id',m.run_id,'condition',json_extract(m.result,'$.applied.cleanup_error'))) FROM merge_operations m WHERE m.task_id=d.id AND m.finished=1 AND COALESCE(json_extract(m.result,'$.applied.cleanup_error'),'')<>'')))
 FROM tasks d LEFT JOIN task_versions v ON v.id=d.submitted_version
 LEFT JOIN task_versions source ON source.id=d.source_version WHERE d.id=tasks.id) AS delivery`

func loadDeliveryRecords(ctx context.Context, q Q, rec *dispatch.Records) error {
	rows, err := q.QueryContext(ctx, `SELECT t.id,COALESCE(t.submitted_version,0),COALESCE(c.id,0),
		COALESCE(c.status='merged',0),t.merge_after,t.correction_open,
		COALESCE((SELECT s.run_id FROM version_submissions s JOIN runs r ON r.id=s.run_id WHERE s.task_id=t.id AND s.captured_version IS NULL ORDER BY r.created_seq,s.run_id LIMIT 1),''),
		COALESCE((SELECT m.run_id FROM merge_operations m JOIN runs r ON r.id=m.run_id WHERE m.task_id=t.id AND m.finished=0 AND r.ended_at IS NOT NULL ORDER BY (m.operation='apply') DESC,r.created_seq,m.run_id LIMIT 1),''),
		COALESCE((SELECT m.operation FROM merge_operations m JOIN runs r ON r.id=m.run_id WHERE m.task_id=t.id AND m.finished=0 AND r.ended_at IS NOT NULL ORDER BY (m.operation='apply') DESC,r.created_seq,m.run_id LIMIT 1),''),
		EXISTS(SELECT 1 FROM repository_executions x WHERE x.stopped_at IS NULL AND x.run_id=(SELECT m.run_id FROM merge_operations m JOIN runs r ON r.id=m.run_id WHERE m.task_id=t.id AND m.finished=0 AND r.ended_at IS NOT NULL ORDER BY (m.operation='apply') DESC,r.created_seq,m.run_id LIMIT 1)),
		EXISTS(SELECT 1 FROM merge_operations m WHERE m.task_id=t.id AND m.operation='apply' AND m.finished=0),
		t.decision IS NULL AND (t.current_review_id IS NULL OR t.review_slot_held=0)
		FROM tasks t LEFT JOIN merge_candidates c ON c.id=t.candidate_id WHERE t.project=?`, rec.Project)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var wait sql.NullString
		var d dispatch.Delivery
		if err := rows.Scan(&id, &d.VersionID, &d.CandidateID, &d.CandidateMerged, &wait, &d.CorrectionOpen, &d.CaptureRun, &d.RecoveryRun, &d.RecoveryOperation, &d.RecoveryStopPending, &d.ApplyPending, &d.ReviewPending); err != nil {
			return err
		}
		if wait.Valid {
			at, err := parseSpineTime(wait.String)
			if err != nil {
				return err
			}
			d.WaitUntil = &at
		}
		for index := range rec.Tasks {
			if rec.Tasks[index].ID == id {
				rec.Tasks[index].Delivery = d
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	return loadExecutionRecords(ctx, q, rec)
}

// loadExecutionRecords reads the cleanups, the active executions, the retry
// times, and the artifact expiry state of one Project. It also reads whether
// receipt recovery checks the merge of a wiki commit of the Project.
func loadExecutionRecords(ctx context.Context, q Q, rec *dispatch.Records) error {
	rec.Cleanups, rec.Executions, rec.Retries = nil, nil, nil
	cleanups, err := q.QueryContext(ctx, `SELECT r.task_id,r.run_id FROM repository_runs r JOIN runs run ON run.id=r.run_id JOIN tasks t ON t.id=r.task_id
		WHERE t.project=? AND (r.captured=1 OR NOT EXISTS(SELECT 1 FROM version_submissions v WHERE v.run_id=r.run_id)) AND r.cleaned=0
		AND NOT EXISTS(SELECT 1 FROM environment_proposals ep JOIN project_environments pe ON pe.draft_revision_id=ep.revision_id
		 WHERE ep.run_id=r.run_id AND NOT EXISTS(SELECT 1 FROM environment_candidates ec WHERE ec.revision_id=ep.revision_id))
		AND run.ended_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM merge_operations m WHERE m.run_id=r.run_id AND m.finished=0)
		ORDER BY run.created_seq`, rec.Project)
	if err != nil {
		return err
	}
	for cleanups.Next() {
		var c dispatch.Cleanup
		if err := cleanups.Scan(&c.TaskID, &c.RunID); err != nil {
			cleanups.Close()
			return err
		}
		rec.Cleanups = append(rec.Cleanups, c)
	}
	err = cleanups.Err()
	cleanups.Close()
	if err != nil {
		return err
	}
	executions, err := q.QueryContext(ctx, `SELECT run_id,operation,task_id,target FROM repository_executions WHERE project=? AND ended_at IS NULL AND wiki_update IS NULL ORDER BY id`, rec.Project)
	if err != nil {
		return err
	}
	for executions.Next() {
		var x dispatch.Execution
		var operation string
		if err := executions.Scan(&x.RunID, &operation, &x.TaskID, &x.Target); err != nil {
			executions.Close()
			return err
		}
		x.Operation = dispatch.Operation(operation)
		rec.Executions = append(rec.Executions, x)
	}
	err = executions.Err()
	executions.Close()
	if err != nil {
		return err
	}
	retries, err := q.QueryContext(ctx, `SELECT x.target,x.operation,x.task_id,x.retry_after FROM repository_executions x
		WHERE x.project=? AND x.retry_after IS NOT NULL AND x.wiki_update IS NULL
		AND x.id=(SELECT MAX(y.id) FROM repository_executions y WHERE y.target=x.target AND y.ended_at IS NOT NULL) ORDER BY x.id`, rec.Project)
	if err != nil {
		return err
	}
	for retries.Next() {
		var r dispatch.Retry
		var operation, until string
		if err := retries.Scan(&r.Target, &operation, &r.TaskID, &until); err != nil {
			retries.Close()
			return err
		}
		r.Operation = dispatch.Operation(operation)
		if r.Until, err = parseSpineTime(until); err != nil {
			retries.Close()
			return err
		}
		rec.Retries = append(rec.Retries, r)
	}
	err = retries.Err()
	retries.Close()
	if err != nil {
		return err
	}
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_retention a JOIN tasks t ON t.id=a.task_id
		WHERE a.token IS NOT NULL AND t.project=?)`, rec.Project).Scan(&rec.ExpiryActive); err != nil {
		return err
	}
	wiki, err := loadWikiDelivery(ctx, q, rec.Project)
	if err != nil {
		return err
	}
	rec.WikiReceiptRecovery = wikiReceiptRecovery(wiki)
	return nil
}

// wikiReceiptRecovery reports that the merge of the wiki commit ended and that
// its merge authorization is unfinished. Receipt recovery is then the next
// step of the wiki update, and each merge step of a task waits for its result.
func wikiReceiptRecovery(u *dispatch.WikiDelivery) bool {
	return u != nil && u.ApplyRun != "" && u.ApplyEnded
}

// wikiCheckText starts the repair text of a merge of a wiki commit whose last
// receipt recovery ended with no result. The condition of that end follows it.
const wikiCheckText = "The last check ended with no result. "

// loadWikiMergeRecords reads the repository facts of the wiki update of one
// Project: the capture and merge facts of the active wiki update, the active
// executions, the retry times, and the cleanups.
func loadWikiMergeRecords(ctx context.Context, q Q, project string) (dispatch.WikiMergeRecords, error) {
	var rec dispatch.WikiMergeRecords
	var err error
	if rec.Update, err = loadWikiDelivery(ctx, q, project); err != nil {
		return rec, err
	}
	executions, err := q.QueryContext(ctx, `SELECT run_id,operation,target FROM repository_executions
		WHERE project=? AND ended_at IS NULL AND wiki_update IS NOT NULL ORDER BY id`, project)
	if err != nil {
		return rec, err
	}
	for executions.Next() {
		var x dispatch.WikiExecution
		var operation string
		if err := executions.Scan(&x.RunID, &operation, &x.Target); err != nil {
			executions.Close()
			return rec, err
		}
		x.Operation = dispatch.Operation(operation)
		rec.Executions = append(rec.Executions, x)
	}
	err = executions.Err()
	executions.Close()
	if err != nil {
		return rec, err
	}
	retries, err := q.QueryContext(ctx, `SELECT x.target,x.operation,x.retry_after FROM repository_executions x
		WHERE x.project=? AND x.retry_after IS NOT NULL AND x.wiki_update IS NOT NULL
		AND x.id=(SELECT MAX(y.id) FROM repository_executions y WHERE y.target=x.target AND y.ended_at IS NOT NULL) ORDER BY x.id`, project)
	if err != nil {
		return rec, err
	}
	for retries.Next() {
		var r dispatch.WikiRetry
		var operation, until string
		if err := retries.Scan(&r.Target, &operation, &until); err != nil {
			retries.Close()
			return rec, err
		}
		r.Operation = dispatch.Operation(operation)
		if r.Until, err = parseSpineTime(until); err != nil {
			retries.Close()
			return rec, err
		}
		rec.Retries = append(rec.Retries, r)
	}
	err = retries.Err()
	retries.Close()
	if err != nil {
		return rec, err
	}
	// The repository of the Scribe run stays until the capture or the end of
	// the wiki update. The repository of an interrupted merge candidate step
	// stays for its recovery while the wiki update is active.
	cleanups, err := q.QueryContext(ctx, `SELECT r.wiki_update,r.run_id FROM repository_runs r JOIN runs run ON run.id=r.run_id JOIN wiki_updates w ON w.run_id=r.wiki_update
		WHERE w.project=? AND r.cleaned=0 AND run.ended_at IS NOT NULL
		AND (r.run_id<>r.wiki_update OR r.captured=1 OR w.ended_at IS NOT NULL)
		AND NOT EXISTS(SELECT 1 FROM merge_operations m WHERE m.run_id=r.run_id AND m.finished=0 AND w.ended_at IS NULL)
		ORDER BY run.created_seq`, project)
	if err != nil {
		return rec, err
	}
	defer cleanups.Close()
	for cleanups.Next() {
		var c dispatch.WikiCleanup
		if err := cleanups.Scan(&c.WikiUpdate, &c.RunID); err != nil {
			return rec, err
		}
		rec.Cleanups = append(rec.Cleanups, c)
	}
	return rec, cleanups.Err()
}

// loadWikiDelivery reads the capture and merge facts of the active wiki update
// of the Project. The result is nil with no active wiki update, and while the
// run of the wiki update is active.
func loadWikiDelivery(ctx context.Context, q Q, project string) (*dispatch.WikiDelivery, error) {
	var d dispatch.WikiDelivery
	var commit bool
	var captured sql.NullBool
	var wait sql.NullString
	err := q.QueryRowContext(ctx, `SELECT w.run_id,w.wiki_commit IS NOT NULL,w.merge_after,
		(SELECT r.captured FROM repository_runs r WHERE r.run_id=w.run_id AND r.wiki_update=w.run_id)
		FROM wiki_updates w JOIN runs run ON run.id=w.run_id
		WHERE w.project=? AND w.ended_at IS NULL AND run.ended_at IS NOT NULL`, project).Scan(&d.RunID, &commit, &wait, &captured)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.CaptureDue = commit && captured.Valid && !captured.Bool
	d.Captured = captured.Valid && captured.Bool
	if wait.Valid {
		at, err := parseSpineTime(wait.String)
		if err != nil {
			return nil, err
		}
		d.WaitUntil = &at
	}
	var candidate int64
	var body string
	switch err := q.QueryRowContext(ctx, currentWikiCandidate, d.RunID).Scan(&candidate, &body); err {
	case nil:
		d.Candidate = true
	case sql.ErrNoRows:
	default:
		return nil, err
	}
	err = q.QueryRowContext(ctx, `SELECT m.run_id,EXISTS(SELECT 1 FROM repository_executions x WHERE x.run_id=m.run_id AND x.stopped_at IS NULL)
		FROM merge_operations m JOIN runs r ON r.id=m.run_id
		WHERE m.wiki_update=? AND m.operation='prepare' AND m.finished=0 AND r.ended_at IS NOT NULL
		ORDER BY r.created_seq,m.run_id LIMIT 1`, d.RunID).Scan(&d.PrepareRun, &d.PrepareStopPending)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	// The stored result of the merge authorization names the repair of the
	// last Git refusal. With no such result, the condition of the last ended
	// receipt recovery is the repair text.
	var condition string
	err = q.QueryRowContext(ctx, `SELECT m.run_id,r.ended_at IS NOT NULL,
		EXISTS(SELECT 1 FROM repository_executions x WHERE x.run_id=m.run_id AND x.stopped_at IS NULL),
		EXISTS(SELECT 1 FROM repository_executions x WHERE x.operation='reconcile' AND x.target=m.run_id AND x.ended_at IS NOT NULL),
		COALESCE(json_extract(m.result,'$.operator_action'),''),
		COALESCE((SELECT x.end_condition FROM repository_executions x
			WHERE x.operation='reconcile' AND x.target=m.run_id AND x.ended_at IS NOT NULL ORDER BY x.id DESC LIMIT 1),'')
		FROM merge_operations m JOIN runs r ON r.id=m.run_id
		WHERE m.wiki_update=? AND m.operation='apply' AND m.finished=0`, d.RunID).Scan(&d.ApplyRun, &d.ApplyEnded, &d.ApplyStopPending, &d.RecoveryEnded, &d.Repair, &condition)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if d.ApplyRun != "" && d.Repair == "" && condition != "" {
		d.Repair = wikiCheckText + condition
	}
	return &d, nil
}

// executionStartRace marks a refused execution insert and a refused merge
// authorization. The refusal is a race with the start selection, so the start
// request records no failed execution.
type executionStartRace struct{ err error }

func (e *executionStartRace) Error() string { return e.err.Error() }
func (e *executionStartRace) Unwrap() error { return e.err }

// authorizationRace marks the refusal of a second unfinished merge
// authorization for one destination branch as a start race.
func authorizationRace(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: merge_operations.project, merge_operations.destination_branch") {
		return &executionStartRace{err: err}
	}
	return err
}

// startRepositoryOperation records the active execution of one operation and
// the operation rows. It returns the execution run. Only the execution insert
// and the insert of a merge authorization return an *executionStartRace. Each
// other error is a failed start.
func startRepositoryOperation(ctx context.Context, q Q, clock domain.VMClock, owner int64, op dispatch.RepositoryOperation) (string, error) {
	if op.Subject().Wiki() {
		return startWikiOperation(ctx, q, clock, owner, op)
	}
	var workspace, destination string
	var version, candidate sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT s.workspace_root,t.destination_branch,t.submitted_version,t.candidate_id
		FROM tasks t JOIN projects w ON w.id=t.project
		JOIN sandbox_profiles s ON s.id=w.sandbox_profile WHERE t.id=?`, op.TaskID).Scan(&workspace, &destination, &version, &candidate); err != nil {
		return "", err
	}
	run, err := newRunID()
	if err != nil {
		return "", err
	}
	effect := map[string]any{"run_id": run, "operation": string(op.Operation), "task_id": op.TaskID, "project": op.Project, "workspace_root": workspace, "destination_branch": destination}
	var write func() error
	switch op.Operation {
	case dispatch.OperationCapture:
		var input, commit, kind string
		if err := q.QueryRowContext(ctx, `SELECT input_commit,result_commit,kind FROM version_submissions WHERE run_id=? AND task_id=? AND captured_version IS NULL`, op.RunID, op.TaskID).Scan(&input, &commit, &kind); err != nil {
			return "", err
		}
		effect["input_commit"], effect["result_commit"] = input, commit
		effect["submission_kind"], effect["producer_run"] = kind, op.RunID
		write = func() error {
			_, err := q.ExecContext(ctx, `INSERT INTO repository_transfers(run_id,producer_run,task_id) VALUES(?,?,?)`, run, op.RunID, op.TaskID)
			return err
		}
	case dispatch.OperationCleanup:
		effect["producer_run"] = op.RunID
	case dispatch.OperationObserve:
		write = func() error {
			_, err := q.ExecContext(ctx, `INSERT INTO repository_observations(run_id,task_id,operation) VALUES(?,?,?)`, run, op.TaskID, string(op.Operation))
			return err
		}
	case dispatch.OperationReconcile:
		var body string
		var authorizedVersion int64
		if err := q.QueryRowContext(ctx, `SELECT c.result,m.version_id FROM merge_operations m
		 JOIN merge_candidates c ON c.id=m.candidate_id JOIN runs r ON r.id=m.run_id
		 WHERE m.run_id=? AND m.task_id=? AND m.operation='apply' AND m.finished=0 AND r.ended_at IS NOT NULL`, op.RunID, op.TaskID).Scan(&body, &authorizedVersion); err != nil {
			return "", err
		}
		var c repository.Candidate
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			return "", err
		}
		c.Changes = nil
		effect["submitted_commit"], effect["version_id"] = c.SubmittedCommit, authorizedVersion
		effect["application"] = repository.Application{Authorization: op.RunID, Candidate: c}
		effect["previous_run"] = op.RunID
	case dispatch.OperationPrepare, dispatch.OperationApply:
		write, err = mergeOperationStart(ctx, q, op, run, destination, version, candidate, effect)
		if err != nil {
			return "", err
		}
	default:
		return "", Domainf("the repository operation %q is invalid", op.Operation)
	}
	body, err := json.Marshal(effect)
	if err != nil {
		return "", err
	}
	if err := domain.StartExecution(ctx, q, domain.ExecutionStart{
		RunID: run, Project: op.Project, TaskID: op.TaskID, Operation: string(op.Operation),
		Target: op.Target(), Owner: owner, Effect: string(body), Deadline: op.Operation.Deadline(), Clock: clock,
	}); err != nil {
		return "", &executionStartRace{err: err}
	}
	if write != nil {
		if err := write(); err != nil {
			return "", err
		}
	}
	return run, nil
}

// mergeOperationStart adds the facts of a merge candidate step or a merge to
// the effect, and returns the writes that follow the execution insert.
func mergeOperationStart(ctx context.Context, q Q, op dispatch.RepositoryOperation, run, destination string, version, candidate sql.NullInt64, effect map[string]any) (func() error, error) {
	var maximum int64
	if err := q.QueryRowContext(ctx, `SELECT max_temporary_bytes FROM repository_limits WHERE project=?`, op.Project).Scan(&maximum); err != nil {
		return nil, err
	}
	effect["max_temporary_bytes"] = maximum
	if op.RunID != "" {
		if op.Operation != dispatch.OperationPrepare {
			return nil, Domainf("the application requires receipt reconciliation before another run")
		}
		// A prepare recovery completes the interrupted step for its own version.
		// A revision can replace the submitted version before the recovery starts.
		// The record then ends the recovery as obsolete.
		var previous string
		if err := q.QueryRowContext(ctx, `SELECT m.run_id,m.version_id,m.candidate_id FROM merge_operations m JOIN runs r ON r.id=m.run_id
		 WHERE m.run_id=? AND m.task_id=? AND m.operation='prepare' AND m.finished=0 AND r.ended_at IS NOT NULL`, op.RunID, op.TaskID).Scan(&previous, &version, &candidate); err != nil {
			return nil, err
		}
		effect["previous_run"] = previous
	}
	var submitted string
	if err := q.QueryRowContext(ctx, `SELECT result_commit FROM task_versions WHERE id=? AND task_id=?`, version, op.TaskID).Scan(&submitted); err != nil {
		return nil, err
	}
	effect["submitted_commit"], effect["version_id"] = submitted, version.Int64
	var packet sql.NullInt64
	if op.Operation == dispatch.OperationApply {
		var body string
		if err := q.QueryRowContext(ctx, `SELECT result FROM merge_candidates WHERE id=? AND task_id=? AND version_id=?`, candidate, op.TaskID, version).Scan(&body); err != nil {
			return nil, err
		}
		var c repository.Candidate
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			return nil, err
		}
		if err := c.Validate(); err != nil {
			return nil, err
		}
		c.Changes = nil
		if err := q.QueryRowContext(ctx, `SELECT p.id FROM review_versions p JOIN review_decisions d ON d.packet_id=p.id
            WHERE p.task_id=? AND p.version_id=? AND d.decision='approved'`, op.TaskID, version).Scan(&packet); err != nil {
			return nil, err
		}
		effect["application"] = repository.Application{Authorization: run, Candidate: c}
	}
	return func() error {
		if op.RunID != "" {
			if _, err := q.ExecContext(ctx, `UPDATE merge_operations SET finished=1,result='{"status":"interrupted"}' WHERE run_id=?`, op.RunID); err != nil {
				return err
			}
		}
		_, err := q.ExecContext(ctx, `INSERT INTO merge_operations(run_id,task_id,project,operation,version_id,candidate_id,destination_branch,packet_id) VALUES(?,?,?,?,?,?,?,?)`, run, op.TaskID, op.Project, string(op.Operation), version, candidate, destination, packet)
		return authorizationRace(err)
	}, nil
}

// wikiStartRefusal gives a missing row of the start of a wiki operation a
// text in plain words. That start error can become the cause of a failed
// wiki update.
func wikiStartRefusal(err error, text string) error {
	if err == sql.ErrNoRows {
		return &DomainError{Msg: text}
	}
	return err
}

// wikiCommits returns the start commit and the wiki commit of an active wiki update.
func wikiCommits(ctx context.Context, q Q, wiki string) (string, string, error) {
	var start string
	var commit sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT start_commit,wiki_commit FROM wiki_updates WHERE run_id=? AND ended_at IS NULL`, wiki).Scan(&start, &commit); err != nil {
		return "", "", wikiStartRefusal(err, "Culler found no active wiki update.")
	}
	if !commit.Valid {
		return "", "", &DomainError{Msg: "The wiki update has no wiki commit."}
	}
	return start, commit.String, nil
}

// startWikiOperation records the active execution of one operation of a wiki
// update and the operation rows. The destination branch is main.
func startWikiOperation(ctx context.Context, q Q, clock domain.VMClock, owner int64, op dispatch.RepositoryOperation) (string, error) {
	wiki := op.WikiUpdate
	var workspace string
	if err := q.QueryRowContext(ctx, `SELECT s.workspace_root FROM wiki_updates u JOIN projects w ON w.id=u.project
		JOIN sandbox_profiles s ON s.id=w.sandbox_profile WHERE u.run_id=?`, wiki).Scan(&workspace); err != nil {
		return "", wikiStartRefusal(err, "Culler found no Project repository for the wiki update.")
	}
	run, err := newRunID()
	if err != nil {
		return "", err
	}
	effect := map[string]any{"run_id": run, "operation": string(op.Operation), "wiki_update": wiki, "project": op.Project, "workspace_root": workspace, "destination_branch": "main"}
	target := op.Target()
	var write func() error
	switch op.Operation {
	case dispatch.OperationCapture:
		start, commit, err := wikiCommits(ctx, q, wiki)
		if err != nil {
			return "", err
		}
		effect["input_commit"], effect["result_commit"], effect["producer_run"] = start, commit, wiki
		target = wiki
	case dispatch.OperationCleanup:
		effect["producer_run"] = op.RunID
	case dispatch.OperationReconcile:
		var body string
		if err := q.QueryRowContext(ctx, `SELECT c.result FROM merge_operations m
		 JOIN merge_candidates c ON c.id=m.candidate_id JOIN runs r ON r.id=m.run_id
		 WHERE m.run_id=? AND m.wiki_update=? AND m.operation='apply' AND m.finished=0 AND r.ended_at IS NOT NULL`, op.RunID, wiki).Scan(&body); err != nil {
			return "", wikiStartRefusal(err, "The wiki update has no merge that waits for receipt recovery.")
		}
		var c repository.Candidate
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			return "", err
		}
		c.Changes = nil
		effect["submitted_commit"] = c.SubmittedCommit
		effect["application"] = repository.Application{Authorization: op.RunID, Candidate: c}
		effect["previous_run"] = op.RunID
	case dispatch.OperationPrepare, dispatch.OperationApply:
		write, err = wikiMergeStart(ctx, q, op, run, effect)
		if err != nil {
			return "", err
		}
	default:
		return "", Domainf("the repository operation is not valid for a wiki update")
	}
	body, err := json.Marshal(effect)
	if err != nil {
		return "", err
	}
	if err := domain.StartExecution(ctx, q, domain.ExecutionStart{
		RunID: run, Project: op.Project, WikiUpdate: wiki, Operation: string(op.Operation),
		Target: target, Owner: owner, Effect: string(body), Deadline: op.Operation.Deadline(), Clock: clock,
	}); err != nil {
		return "", &executionStartRace{err: err}
	}
	if write != nil {
		if err := write(); err != nil {
			return "", err
		}
	}
	return run, nil
}

// currentWikiCandidate selects the current candidate of a wiki update: the
// newest merged candidate that no finished merge names.
const currentWikiCandidate = `SELECT c.id,c.result FROM merge_candidates c WHERE c.wiki_update=? AND c.status='merged'
	AND NOT EXISTS(SELECT 1 FROM merge_operations m WHERE m.candidate_id=c.id AND m.operation='apply' AND m.finished=1)
	ORDER BY c.id DESC LIMIT 1`

// wikiMergeStart adds the facts of a merge candidate step or a merge of a
// wiki commit to the effect, and returns the writes that follow the execution
// insert.
func wikiMergeStart(ctx context.Context, q Q, op dispatch.RepositoryOperation, run string, effect map[string]any) (func() error, error) {
	wiki := op.WikiUpdate
	var maximum int64
	if err := q.QueryRowContext(ctx, `SELECT max_temporary_bytes FROM repository_limits WHERE project=?`, op.Project).Scan(&maximum); err != nil {
		return nil, err
	}
	effect["max_temporary_bytes"] = maximum
	start, commit, err := wikiCommits(ctx, q, wiki)
	if err != nil {
		return nil, err
	}
	effect["submitted_commit"] = commit
	if op.RunID != "" {
		if op.Operation != dispatch.OperationPrepare {
			return nil, Domainf("the application requires receipt reconciliation before another run")
		}
		var previous string
		if err := q.QueryRowContext(ctx, `SELECT m.run_id FROM merge_operations m JOIN runs r ON r.id=m.run_id
		 WHERE m.run_id=? AND m.wiki_update=? AND m.operation='prepare' AND m.finished=0 AND r.ended_at IS NOT NULL`, op.RunID, wiki).Scan(&previous); err != nil {
			return nil, wikiStartRefusal(err, "The wiki update has no interrupted merge candidate step.")
		}
		effect["previous_run"] = previous
	}
	var candidate sql.NullInt64
	if op.Operation == dispatch.OperationPrepare {
		effect["start_commit"] = start
	} else {
		var body string
		if err := q.QueryRowContext(ctx, currentWikiCandidate, wiki).Scan(&candidate, &body); err != nil {
			return nil, wikiStartRefusal(err, "The wiki update has no merge candidate.")
		}
		var c repository.Candidate
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			return nil, err
		}
		if err := c.Validate(); err != nil {
			return nil, err
		}
		c.Changes = nil
		effect["application"] = repository.Application{Authorization: run, Candidate: c}
	}
	return func() error {
		if op.RunID != "" {
			if _, err := q.ExecContext(ctx, `UPDATE merge_operations SET finished=1,result='{"status":"interrupted"}' WHERE run_id=?`, op.RunID); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO merge_operations(run_id,wiki_update,project,operation,candidate_id,destination_branch) VALUES(?,?,?,?,?,'main')`, run, wiki, op.Project, string(op.Operation), candidate); err != nil {
			return authorizationRace(err)
		}
		if op.Operation != dispatch.OperationPrepare {
			return nil
		}
		_, err := q.ExecContext(ctx, `UPDATE wiki_updates SET merge_after=NULL WHERE run_id=?`, wiki)
		return err
	}, nil
}

// RepositoryResult is the result of one repository operation. Its subject is
// one task or one wiki update.
type RepositoryResult struct {
	WikiUpdate        string                `json:"wiki_update,omitempty"`
	TaskID            int64                 `json:"task_id,omitempty"`
	InputCommit       string                `json:"input_commit"`
	DestinationCommit string                `json:"destination_commit"`
	ResultCommit      string                `json:"result_commit"`
	RetainedRef       string                `json:"retained_ref"`
	TemporaryBytes    int64                 `json:"temporary_bytes"`
	VersionID         *int64                `json:"version_id,omitempty"`
	Candidate         *repository.Candidate `json:"candidate,omitempty"`
	Applied           *repository.Applied   `json:"applied,omitempty"`
	Status            string                `json:"status"`
	EvaluationPending bool                  `json:"evaluation_pending,omitempty"`
	CleanupError      string                `json:"cleanup_error,omitempty"`
	OperatorAction    string                `json:"operator_action,omitempty"`
}

// Subject returns the subject of the result.
func (r RepositoryResult) Subject() dispatch.Subject {
	return dispatch.Subject{TaskID: r.TaskID, WikiUpdate: r.WikiUpdate}
}

// subjectField returns the JSON key and the value that name the subject.
func subjectField(s dispatch.Subject) (string, any) {
	if s.Wiki() {
		return "wiki_update", s.WikiUpdate
	}
	return "task_id", s.TaskID
}

func RepositoryRecord(db *sql.DB, id *RunIdentity, operation, run string, result RepositoryResult) (any, error) {
	if err := RequireHostScope(id, "mc delivery record"); err != nil {
		return nil, err
	}
	subject := result.Subject()
	if !subject.Valid() {
		return nil, Usagef("the repository result requires its task or its wiki update")
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		now, err := spineClock(ctx, q)
		if err != nil {
			return err
		}
		switch operation {
		case "observe":
			if subject.Wiki() {
				return Domainf("the repository operation is not valid for a wiki update")
			}
			return recordRepositoryObservation(ctx, q, now, operation, run, result)
		case "ready":
			return recordRepositoryReady(ctx, q, run, result)
		case "capture":
			if subject.Wiki() {
				return recordWikiCapture(ctx, q, now, run, result)
			}
			var producer string
			var finished bool
			err := q.QueryRowContext(ctx, `SELECT producer_run,finished FROM repository_transfers WHERE run_id=? AND task_id=?`, run, result.TaskID).Scan(&producer, &finished)
			if err == sql.ErrNoRows {
				return Domainf("the capture result requires its transfer record")
			}
			if err != nil {
				return err
			}
			if finished {
				return repeatedCapture(ctx, q, run, producer, result)
			}
			if _, err := domain.FenceExecution(ctx, q, run); err != nil {
				return err
			}
			if err := recordCapturedVersion(ctx, q, producer, result); err != nil {
				return err
			}
			return endExecutionResult(ctx, q, now, run, "")
		case "cleanup":
			return recordCleanup(ctx, q, now, run, result)
		case "prepare", "apply", "reconcile":
			return recordMergeOperation(ctx, q, now, operation, run, result)
		default:
			return Usagef("the repository result operation is invalid")
		}
	})
	key, value := subjectField(subject)
	return map[string]any{key: value, "recorded": err == nil}, err
}

// recordWikiCapture records the capture of the wiki commit of a wiki update.
// It accepts the record of an ended capture only as a repeat of the result
// that ended the capture.
func recordWikiCapture(ctx context.Context, q Q, now time.Time, run string, result RepositoryResult) error {
	var operation string
	var wiki, reason sql.NullString
	err := q.QueryRowContext(ctx, `SELECT operation,wiki_update,end_reason FROM repository_executions WHERE run_id=?`, run).Scan(&operation, &wiki, &reason)
	if err == sql.ErrNoRows {
		return &DomainError{Code: domain.CodeStaleRun, Msg: fmt.Sprintf("stale run: %q has no repository execution", run)}
	}
	if err != nil {
		return err
	}
	if operation != string(dispatch.OperationCapture) || wiki.String != result.WikiUpdate {
		return Domainf("the capture result does not identify its operation")
	}
	var start string
	var commit sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT start_commit,wiki_commit FROM wiki_updates WHERE run_id=?`, result.WikiUpdate).Scan(&start, &commit); err != nil {
		return err
	}
	same := commit.Valid && result.InputCommit == start && result.ResultCommit == commit.String &&
		result.RetainedRef == "refs/mc/wiki/commits/"+result.WikiUpdate
	if reason.Valid {
		if reason.String != domain.EndResult || !same {
			return &DomainError{Code: domain.CodeStaleRun, Msg: fmt.Sprintf("stale run: %q has no active repository execution", run)}
		}
		return nil
	}
	if _, err := domain.FenceExecution(ctx, q, run); err != nil {
		return err
	}
	if !same {
		return Domainf("the capture result changed the wiki commit")
	}
	if _, err := q.ExecContext(ctx, `UPDATE repository_runs SET captured=1 WHERE run_id=? AND wiki_update=?`, result.WikiUpdate, result.WikiUpdate); err != nil {
		return err
	}
	return endExecutionResult(ctx, q, now, run, "")
}

// repeatedCapture accepts the record of an ended capture only as a repeat of
// the result that ended the capture. Each other end leaves the late record stale.
func repeatedCapture(ctx context.Context, q Q, run, producer string, result RepositoryResult) error {
	var reason sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT end_reason FROM repository_executions WHERE run_id=?`, run).Scan(&reason); err != nil && err != sql.ErrNoRows {
		return err
	}
	var task int64
	var input, commit, ref string
	err := q.QueryRowContext(ctx, `SELECT s.task_id,s.input_commit,s.result_commit,v.retained_ref FROM version_submissions s
		JOIN task_versions v ON v.id=s.captured_version WHERE s.run_id=?`, producer).Scan(&task, &input, &commit, &ref)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if reason.String != domain.EndResult || err == sql.ErrNoRows {
		return &DomainError{Code: domain.CodeStaleRun, Msg: fmt.Sprintf("stale run: %q has no active repository execution", run)}
	}
	if task != result.TaskID || input != result.InputCommit || commit != result.ResultCommit || ref != result.RetainedRef {
		return Domainf("the capture result changed the submitted version")
	}
	return nil
}

// endExecutionResult ends the active execution of a recorded result. The
// resident records a result after the host work of the step stopped.
func endExecutionResult(ctx context.Context, q Q, now time.Time, run, outcome string) error {
	_, err := domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: run, Reason: domain.EndResult, Outcome: outcome, Stopped: true, Now: now})
	return err
}

// endActiveExecution ends the execution of a repeated result when the
// execution is still active.
func endActiveExecution(ctx context.Context, q Q, now time.Time, run, outcome string) error {
	var active bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM repository_executions WHERE run_id=? AND ended_at IS NULL)`, run).Scan(&active); err != nil {
		return err
	}
	if !active {
		return nil
	}
	return endExecutionResult(ctx, q, now, run, outcome)
}

// executionTarget returns the operation and the target of an execution in
// each state.
func executionTarget(ctx context.Context, q Q, run string) (string, string, error) {
	var operation, target string
	err := q.QueryRowContext(ctx, `SELECT operation,target FROM repository_executions WHERE run_id=?`, run).Scan(&operation, &target)
	if err == sql.ErrNoRows {
		return "", "", &DomainError{Code: domain.CodeStaleRun, Msg: fmt.Sprintf("stale run: %q has no repository execution", run)}
	}
	return operation, target, err
}

// recordRepositoryReady records the isolated repository of an agent run or of
// a merge candidate step.
func recordRepositoryReady(ctx context.Context, q Q, run string, result RepositoryResult) error {
	var execution bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM repository_executions WHERE run_id=?)`, run).Scan(&execution); err != nil {
		return err
	}
	var subject dispatch.Subject
	if execution {
		x, err := domain.FenceExecution(ctx, q, run)
		if err != nil {
			return err
		}
		if x.Operation != string(dispatch.OperationPrepare) {
			return Domainf("the repository requires its assigned run")
		}
		subject = x.Subject()
	} else {
		fence, err := domain.FenceAgentRun(ctx, q, run)
		if err != nil {
			return err
		}
		if fence.WikiUpdate {
			subject = dispatch.WikiSubject(run)
		} else if fence.TaskID != nil {
			subject = dispatch.TaskSubject(*fence.TaskID)
		}
	}
	if !subject.Valid() || subject != result.Subject() {
		return Domainf("the repository requires its assigned run")
	}
	if !repository.ValidCommit(result.InputCommit) || !repository.ValidCommit(result.DestinationCommit) {
		return Domainf("the repository requires exact input commits")
	}
	if subject.Wiki() {
		return recordWikiRepository(ctx, q, run, result, execution)
	}
	var priorInput, priorDestination string
	priorErr := q.QueryRowContext(ctx, `SELECT input_commit,destination_commit FROM repository_runs WHERE run_id=? AND task_id=?`, run, result.TaskID).Scan(&priorInput, &priorDestination)
	if priorErr == nil {
		if priorInput != result.InputCommit || priorDestination != result.DestinationCommit {
			return Domainf("the repository allocation identifies another input")
		}
		_, err := q.ExecContext(ctx, `UPDATE repository_runs SET temporary_bytes=? WHERE run_id=? AND cleaned=0`, result.TemporaryBytes, run)
		return err
	}
	if priorErr != sql.ErrNoRows {
		return priorErr
	}
	var expected sql.NullString
	var version sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(cp.result_commit,v.result_commit,d.input_commit),d.submitted_version FROM tasks d LEFT JOIN task_versions v ON v.id=d.submitted_version LEFT JOIN task_versions cp ON cp.id=d.checkpoint_version WHERE d.id=?`, result.TaskID).Scan(&expected, &version); err != nil {
		return err
	}
	var role string
	if err := q.QueryRowContext(ctx, `SELECT role FROM runs WHERE id=?`, run).Scan(&role); err != nil {
		return err
	}
	if expected.Valid && expected.String != result.InputCommit {
		return Domainf("the run repository changed its assigned input commit")
	}
	captured := role != "worker"
	if _, err := q.ExecContext(ctx, `INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,version_id,temporary_bytes,captured) VALUES(?,?,?,?,?,?,?)`, run, result.TaskID, result.InputCommit, result.DestinationCommit, version, result.TemporaryBytes, captured); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE tasks SET input_commit=COALESCE(input_commit,?),base_commit=COALESCE(base_commit,?) WHERE id=?`, result.InputCommit, result.InputCommit, result.TaskID)
	return err
}

// recordWikiRepository records the isolated repository of the Scribe run of a
// wiki update, or of a merge candidate step of its wiki commit. The first
// starts at the start commit and waits for the capture. The second starts at
// the wiki commit and needs no capture.
func recordWikiRepository(ctx context.Context, q Q, run string, result RepositoryResult, step bool) error {
	var start string
	var commit sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT start_commit,wiki_commit FROM wiki_updates WHERE run_id=? AND ended_at IS NULL`, result.WikiUpdate).Scan(&start, &commit); err != nil {
		if err == sql.ErrNoRows {
			return Domainf("the repository requires its assigned run")
		}
		return err
	}
	expected := start
	if step {
		expected = commit.String
	}
	if expected != result.InputCommit {
		return Domainf("the run repository changed its assigned input commit")
	}
	var priorInput, priorDestination string
	err := q.QueryRowContext(ctx, `SELECT input_commit,destination_commit FROM repository_runs WHERE run_id=? AND wiki_update=?`, run, result.WikiUpdate).Scan(&priorInput, &priorDestination)
	if err == nil {
		if priorInput != result.InputCommit || priorDestination != result.DestinationCommit {
			return Domainf("the repository allocation identifies another input")
		}
		_, err = q.ExecContext(ctx, `UPDATE repository_runs SET temporary_bytes=? WHERE run_id=? AND cleaned=0`, result.TemporaryBytes, run)
		return err
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO repository_runs(run_id,wiki_update,input_commit,destination_commit,temporary_bytes,captured) VALUES(?,?,?,?,?,?)`,
		run, result.WikiUpdate, result.InputCommit, result.DestinationCommit, result.TemporaryBytes, step)
	return err
}

// The statements that record a completed cleanup. Each one changes the row
// only while the repository meets the cleanup conditions of its subject.
const (
	taskCleanupFinish = `UPDATE repository_runs SET captured=1,cleaned=1,cleanup_error=NULL,temporary_bytes=? WHERE run_id=? AND task_id=? AND cleaned=0
		AND (captured=1 OR NOT EXISTS(SELECT 1 FROM version_submissions v WHERE v.run_id=repository_runs.run_id)) AND EXISTS(SELECT 1 FROM runs WHERE id=? AND ended_at IS NOT NULL)`
	wikiCleanupFinish = `UPDATE repository_runs SET captured=1,cleaned=1,cleanup_error=NULL,temporary_bytes=? WHERE run_id=? AND wiki_update=? AND cleaned=0
		AND EXISTS(SELECT 1 FROM runs WHERE id=? AND ended_at IS NOT NULL)
		AND EXISTS(SELECT 1 FROM wiki_updates w WHERE w.run_id=repository_runs.wiki_update
			AND (repository_runs.run_id<>repository_runs.wiki_update OR repository_runs.captured=1 OR w.ended_at IS NOT NULL)
			AND NOT EXISTS(SELECT 1 FROM merge_operations m WHERE m.run_id=repository_runs.run_id AND m.finished=0 AND w.ended_at IS NULL))`
)

// recordCleanup records the cleanup of the temporary repository that the
// execution targets. A cleanup error ends the execution as a failure.
func recordCleanup(ctx context.Context, q Q, now time.Time, run string, result RepositoryResult) error {
	operation, target, err := executionTarget(ctx, q, run)
	if err != nil {
		return err
	}
	if operation != string(dispatch.OperationCleanup) {
		return Domainf("the cleanup result does not identify its operation")
	}
	subject := result.Subject()
	row, finish := `SELECT cleaned,temporary_bytes FROM repository_runs WHERE run_id=? AND task_id=?`, taskCleanupFinish
	var key any = result.TaskID
	if subject.Wiki() {
		row, finish = `SELECT cleaned,temporary_bytes FROM repository_runs WHERE run_id=? AND wiki_update=?`, wikiCleanupFinish
		key = result.WikiUpdate
	}
	var cleaned bool
	var bytes int64
	if err := q.QueryRowContext(ctx, row, target, key).Scan(&cleaned, &bytes); err != nil {
		if err == sql.ErrNoRows {
			return Domainf("the cleanup result does not identify its repository")
		}
		return err
	}
	if cleaned && result.CleanupError == "" && bytes == result.TemporaryBytes {
		return endActiveExecution(ctx, q, now, run, "")
	}
	x, err := domain.FenceExecution(ctx, q, run)
	if err != nil {
		return err
	}
	if x.Subject() != subject {
		return Domainf("the cleanup result does not identify its repository")
	}
	if result.EvaluationPending {
		var authorized bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM evaluation_captures WHERE run_id=? AND task_id=? AND enabled=1 AND status<>'complete')`, target, result.TaskID).Scan(&authorized); err != nil {
			return err
		}
		if !authorized {
			return Domainf("The cleanup has no incomplete evaluation capture.")
		}
		if _, err := q.ExecContext(ctx, `UPDATE repository_runs SET cleanup_error=? WHERE run_id=?`, result.CleanupError, target); err != nil {
			return err
		}
		_, err := domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: run, Reason: domain.EndEvaluationPending, Stopped: true, Now: now})
		return err
	}
	if result.CleanupError != "" {
		if _, err := q.ExecContext(ctx, `UPDATE repository_runs SET cleanup_error=?,temporary_bytes=? WHERE run_id=?`, result.CleanupError, result.TemporaryBytes, target); err != nil {
			return err
		}
		_, err := domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: run, Reason: domain.EndFailure, Condition: result.CleanupError, Stopped: true, Now: now})
		return err
	}
	updated, err := q.ExecContext(ctx, finish, result.TemporaryBytes, target, key, target)
	if err != nil {
		return err
	}
	if count, err := updated.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return Domainf("the cleanup result does not identify a repository that meets the cleanup conditions")
	}
	return endExecutionResult(ctx, q, now, run, "")
}

func recordRepositoryObservation(ctx context.Context, q Q, now time.Time, operation, run string, result RepositoryResult) error {
	var recorded sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT result FROM repository_observations WHERE run_id=? AND task_id=? AND operation=?`, run, result.TaskID, operation).Scan(&recorded); err != nil {
		if err == sql.ErrNoRows {
			return Domainf("the repository observation requires its assigned execution")
		}
		return err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if recorded.Valid {
		if recorded.String != string(body) {
			return Domainf("the repository observation differs from its recorded result")
		}
		return endActiveExecution(ctx, q, now, run, "")
	}
	if _, err := domain.FenceExecution(ctx, q, run); err != nil {
		return err
	}
	{
		if !repository.ValidCommit(result.DestinationCommit) {
			return Domainf("the destination observation requires its exact commit")
		}
		if _, err := q.ExecContext(ctx, `UPDATE tasks SET merge_after=CASE WHEN observed_destination=? THEN NULL ELSE datetime(?,'+'||(SELECT destination_quiet_seconds FROM repository_limits p JOIN tasks t ON t.project=p.project WHERE t.id=?)||' seconds') END,observed_destination=?,observed_at=datetime('now') WHERE id=?`, result.DestinationCommit, now.UTC().Format(spineTime), result.TaskID, result.DestinationCommit, result.TaskID); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(ctx, `UPDATE repository_observations SET result=? WHERE run_id=?`, string(body), run); err != nil {
		return err
	}
	return endExecutionResult(ctx, q, now, run, "")
}

func recordCapturedVersion(ctx context.Context, q Q, run string, result RepositoryResult) error {
	var task int64
	var input, commit, kind string
	var captured sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT task_id,input_commit,result_commit,captured_version,kind FROM version_submissions WHERE run_id=?`, run).Scan(&task, &input, &commit, &captured, &kind); err != nil {
		return err
	}
	refs := "versions"
	if kind == "checkpoint" {
		refs = "checkpoints"
	}
	if task != result.TaskID || input != result.InputCommit || commit != result.ResultCommit || result.RetainedRef != fmt.Sprintf("refs/mc/tasks/%d/%s/%s", task, refs, run) {
		return Domainf("the capture result changed the submitted version")
	}
	if captured.Valid {
		return nil
	}
	var status, destination string
	var archived bool
	if err := q.QueryRowContext(ctx, `SELECT t.status,t.archived,t.destination_branch FROM tasks t WHERE t.id=?`, task).Scan(&status, &archived, &destination); err != nil {
		return err
	}
	var current bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM version_submissions s JOIN tasks d ON d.id=s.task_id WHERE s.run_id=?
	 AND d.submitted_version IS s.prior_version)`, run).Scan(&current); err != nil {
		return err
	}
	res, err := q.ExecContext(ctx, `INSERT INTO task_versions(task_id,run_id,input_commit,result_commit,destination_branch,retained_ref,evidence_path,kind)
		SELECT task_id,run_id,input_commit,result_commit,?,?,evidence_path,kind FROM version_submissions WHERE run_id=?`, destination, result.RetainedRef, run)
	if err != nil {
		return err
	}
	version, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE version_submissions SET captured_version=? WHERE run_id=?`, version, run); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE repository_runs SET captured=1 WHERE run_id=?`, run); err != nil {
		return err
	}
	if status != "seeded" || archived || !current {
		return nil
	}
	if kind == "checkpoint" {
		_, err := q.ExecContext(ctx, `UPDATE tasks SET checkpoint_version=? WHERE id=?`, version, task)
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET submitted_version=?,checkpoint_version=NULL,candidate_id=NULL WHERE id=?`, version, task); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE tasks SET status='worked',completion_sha=? WHERE id=?`, commit, task)
	return err
}

// The causes of a failed wiki update that a merge candidate step or a merge decides.
// The end condition of the merge execution follows wikiMergeCause.
const (
	wikiConflictCause = "The wiki commit conflicts with the main branch. Culler discarded the wiki commit."
	wikiMergeCause    = "Culler did not merge the wiki commit. "
)

// recordMergeOperation records the result of a merge candidate step, a merge,
// or a receipt recovery. A receipt recovery records the result of the merge
// that it checks.
func recordMergeOperation(ctx context.Context, q Q, now time.Time, operation, run string, result RepositoryResult) error {
	reconcile := operation == "reconcile"
	row := run
	if reconcile {
		executed, target, err := executionTarget(ctx, q, run)
		if err != nil {
			return err
		}
		if executed != string(dispatch.OperationReconcile) {
			return Domainf("the merge result does not identify its operation")
		}
		operation, row = "apply", target
	}
	var taskID, versionID sql.NullInt64
	var wikiUpdate sql.NullString
	var recordedOperation string
	var finished bool
	if err := q.QueryRowContext(ctx, `SELECT task_id,wiki_update,version_id,operation,finished FROM merge_operations WHERE run_id=?`, row).
		Scan(&taskID, &wikiUpdate, &versionID, &recordedOperation, &finished); err != nil {
		return err
	}
	subject, err := domain.SubjectOf(taskID, wikiUpdate)
	if err != nil {
		return err
	}
	if subject != result.Subject() || operation != recordedOperation {
		return Domainf("the merge result does not identify its operation")
	}
	task, version := subject.TaskID, versionID.Int64
	if operation == "apply" {
		validStatus := result.Status == "operator-attention" || result.Status == "destination-changed" || (reconcile && result.Status == "not-applied")
		if result.Candidate != nil || (result.Applied != nil && result.Status != "") ||
			(result.Applied == nil && !validStatus) {
			return Domainf("the application requires one exact receipt or one recognized refusal")
		}
	}
	if finished {
		var prior string
		if err := q.QueryRowContext(ctx, `SELECT result FROM merge_operations WHERE run_id=?`, row).Scan(&prior); err != nil {
			return err
		}
		var saved RepositoryResult
		if err := json.Unmarshal([]byte(prior), &saved); err != nil {
			return err
		}
		if saved.Applied != nil && result.Applied != nil {
			saved.Applied.Recovered = false
			result.Applied.Recovered = false
		}
		a, _ := json.Marshal(saved)
		b, _ := json.Marshal(result)
		if string(a) != string(b) {
			return Domainf("the repeated merge result identifies different facts")
		}
		return endActiveExecution(ctx, q, now, run, "")
	}
	if _, err := domain.FenceExecution(ctx, q, run); err != nil {
		return err
	}
	if reconcile && (result.Candidate != nil || (result.Applied != nil && result.Status != "") ||
		(result.Applied == nil && result.Status != "not-applied" && result.Status != "operator-attention")) {
		return Domainf("the application reconciliation requires a receipt or an unresolved authorization result")
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE merge_operations SET result=?,finished=? WHERE run_id=?`, string(body), !(operation == "apply" && result.Status == "operator-attention"), row); err != nil {
		return err
	}
	if subject.Wiki() {
		return recordWikiMerge(ctx, q, now, operation, reconcile, run, row, subject.WikiUpdate, result)
	}
	if reconcile && result.Status == "not-applied" {
		return endExecutionResult(ctx, q, now, run, "")
	}
	if operation == "prepare" && result.Candidate != nil {
		var current bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t
		 WHERE t.id=? AND t.submitted_version=? AND t.status='packaged' AND t.archived=0)`, task, version).Scan(&current); err != nil {
			return err
		}
		if !current {
			return endExecutionResult(ctx, q, now, run, "obsolete")
		}
		if result.Status != "" && result.Candidate.Status == "merged" {
			if err := result.Candidate.Validate(); err != nil {
				return err
			}
			if err := domain.CloseCorrection(ctx, q, task); err != nil {
				return err
			}
		}
	}
	if result.Status == "operator-attention" {
		// The refusal text names the repair of the operator checkout only.
		reason := result.OperatorAction
		if reason != "" {
			reason += " Then use Resolve decision with a reason."
		}
		if err := domain.Block(ctx, q, task, domain.BlockCheckout, reason); err != nil {
			return err
		}
	} else if result.Status == "destination-changed" {
		if !repository.ValidCommit(result.DestinationCommit) {
			return Domainf("the changed destination requires an exact commit")
		}
		if _, err := q.ExecContext(ctx, `UPDATE tasks SET candidate_id=NULL,observed_destination=?,observed_at=datetime('now'),
			merge_after=datetime(?,'+'||(SELECT destination_quiet_seconds FROM repository_limits p JOIN tasks t ON t.project=p.project WHERE t.id=?)||' seconds') WHERE id=?`, result.DestinationCommit, now.UTC().Format(spineTime), task, task); err != nil {
			return err
		}
	} else if operation == "prepare" {
		if result.Candidate == nil {
			return Domainf("the merge preparation requires its candidate result")
		}
		c := *result.Candidate
		if c.Status == "merged" {
			if err := validateChangeData(c.Changes, task, run, c); err != nil {
				return err
			}
		} else if c.Changes != nil {
			return Domainf("a merge conflict has no change data")
		}
		if _, err := domain.RecordCandidate(ctx, q, task, run, version, c, result.RetainedRef); err != nil {
			return err
		}
		if c.Changes != nil {
			if err := recordTaskArtifacts(ctx, q, task, c.Changes.Candidate.InventoryPath, c.Changes.Candidate.PatchPath); err != nil {
				return err
			}
		}
	} else {
		merge, err := authorizedMerge(ctx, q, row, result)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE tasks SET archived=1 WHERE id=?`, task); err != nil {
			return err
		}
		if merge.branch == "main" {
			if err := domain.RecordMergedCommit(ctx, q, merge.project, merge.destination, merge.commit); err != nil {
				return err
			}
		}
	}
	return endExecutionResult(ctx, q, now, run, "")
}

// appliedMerge holds the facts of a merge that its receipt proves.
type appliedMerge struct {
	project, branch string
	destination     string // the destination commit of the applied candidate
	commit          string // the commit that the destination branch holds after the merge
}

// authorizedMerge checks the receipt of a merge against the merge
// authorization of the row and returns the facts of the merge.
func authorizedMerge(ctx context.Context, q Q, row string, result RepositoryResult) (appliedMerge, error) {
	var merge appliedMerge
	if result.Applied == nil || result.Applied.Authorization != row {
		return merge, Domainf("the merge application requires its exact authorization receipt")
	}
	if err := q.QueryRowContext(ctx, `SELECT m.project,m.destination_branch,c.destination_commit,c.candidate_commit
		FROM merge_operations m JOIN merge_candidates c ON c.id=m.candidate_id WHERE m.run_id=?`, row).
		Scan(&merge.project, &merge.branch, &merge.destination, &merge.commit); err != nil {
		return merge, err
	}
	if result.Applied.Commit != merge.commit {
		return merge, Domainf("the applied commit differs from the authorized candidate")
	}
	return merge, nil
}

// recordWikiMerge applies the result of a merge candidate step, a merge, or a
// receipt recovery to its wiki update. The row of the operation holds the
// result at this point. Only a proven result of the merge ends a wiki update
// with a merge authorization: a result with no proof keeps the authorization
// unfinished, and receipt recovery repeats.
func recordWikiMerge(ctx context.Context, q Q, now time.Time, operation string, reconcile bool, run, row, wiki string, result RepositoryResult) error {
	// The schema refuses a failed end beside an active execution, so the execution ends first.
	fail := func(cause string) error {
		if err := endExecutionResult(ctx, q, now, run, ""); err != nil {
			return err
		}
		return domain.EndWikiUpdate(ctx, q, wiki, domain.WikiEnd{Result: "failed", Cause: cause})
	}
	// A new merge candidate step waits for one destination quiet interval.
	wait := func() error {
		if _, err := q.ExecContext(ctx, `UPDATE wiki_updates SET merge_after=datetime(?,'+'||(SELECT destination_quiet_seconds
			FROM repository_limits l WHERE l.project=wiki_updates.project)||' seconds') WHERE run_id=?`, now.UTC().Format(spineTime), wiki); err != nil {
			return err
		}
		return endExecutionResult(ctx, q, now, run, "")
	}
	switch {
	case result.Status == "operator-attention" && operation == "prepare":
		return fail(result.OperatorAction)
	case result.Status == "operator-attention":
		// The execution keeps the refusal text. Receipt recovery is due at once
		// after a refused merge, and after the retry time of a refused receipt recovery.
		reason := domain.EndResult
		if reconcile {
			reason = domain.EndFailure
		}
		_, err := domain.EndExecution(ctx, q, domain.ExecutionEnd{RunID: run, Reason: reason, Condition: result.OperatorAction, Stopped: true, Now: now})
		return err
	case result.Status == "destination-changed":
		return wait()
	case reconcile && result.Status == "not-applied":
		var reason, condition, destination string
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(x.end_reason,''),COALESCE(x.end_condition,''),c.destination_commit
			FROM merge_operations m JOIN repository_executions x ON x.run_id=m.run_id
			JOIN merge_candidates c ON c.id=m.candidate_id WHERE m.run_id=?`, row).Scan(&reason, &condition, &destination); err != nil {
			return err
		}
		switch {
		case reason == domain.EndResult:
			// The merge ended with a Git refusal, and its end condition is the refusal text.
			return fail(condition)
		case result.DestinationCommit != destination:
			return wait()
		}
		return fail(wikiMergeCause + condition)
	case operation == "prepare":
		if result.Candidate == nil || result.Status != "" {
			return Domainf("the merge preparation requires its candidate result")
		}
		c := *result.Candidate
		if c.Changes != nil {
			return Domainf("the merge candidate of a wiki commit has no change data")
		}
		if _, err := domain.RecordWikiCandidate(ctx, q, run, wiki, c, result.RetainedRef); err != nil {
			return err
		}
		if c.Status == "conflict" {
			return fail(wikiConflictCause)
		}
		return endExecutionResult(ctx, q, now, run, "")
	}
	merge, err := authorizedMerge(ctx, q, row, result)
	if err != nil {
		return err
	}
	if err := domain.EndWikiUpdate(ctx, q, wiki, domain.WikiEnd{Result: "succeeded", DestinationCommit: merge.destination, MergedCommit: merge.commit}); err != nil {
		return err
	}
	if err := domain.RecordMergedCommit(ctx, q, merge.project, merge.destination, merge.commit); err != nil {
		return err
	}
	return endExecutionResult(ctx, q, now, run, "")
}
