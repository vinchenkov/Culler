package domain

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"
)

const (
	CodeRecordConflict    = "record-conflict"
	CodeOperationConflict = "operation-conflict"
	CodeEditRestricted    = "edit-restricted"
	CodeRevisionPending   = "revision-pending"
	CodeInvalidRequest    = "invalid-request"
)

// BirthOperatorWork assigns explicit operator work before the transaction accepts it.
func BirthOperatorWork(ctx context.Context, q Q, a ProposalArgs) (int64, error) {
	a.Origin = "user"
	if strings.TrimSpace(a.Title) == "" {
		return 0, Errf(CodeReasonRequired, "The title requires text.")
	}
	if !utf8.ValidString(a.Description) || strings.ContainsRune(a.Description, 0) {
		return 0, Errf(CodeInvalidRequest, "The requirements must contain valid text.")
	}
	task, err := BirthProposal(ctx, q, a)
	if err != nil {
		return 0, err
	}
	if err := Promote(ctx, q, task); err != nil {
		return 0, err
	}
	return task, nil
}

// RequireEditableRecord checks identity and the final decision.
func RequireEditableRecord(ctx context.Context, q Q, task, expected int64) error {
	if err := RequireOperatorTaskControl(ctx, q, task); err != nil {
		return err
	}
	r, err := getTask(ctx, q, task)
	if err != nil {
		return err
	}
	if err := requireLive(r); err != nil {
		return err
	}
	var version int64
	if err := q.QueryRowContext(ctx, `SELECT record_version FROM tasks WHERE id=?`, task).Scan(&version); err != nil {
		return err
	}
	if expected > 0 && version != expected {
		return Errf(CodeRecordConflict, "The record changed. Reload the current record before another submission.")
	}
	return nil
}

func RequireRecordVersion(ctx context.Context, q Q, task, expected int64) error {
	var current int64
	if err := q.QueryRowContext(ctx, `SELECT record_version FROM tasks WHERE id=?`, task).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return Errf(CodeNotFound, "The record does not exist.")
		}
		return err
	}
	if expected < 1 || expected != current {
		return Errf(CodeRecordConflict, "The record changed. Reload the current record before another submission.")
	}
	return nil
}

func RequirementsRevisionPending(ctx context.Context, q Q, task int64) (bool, error) {
	var pending bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM requirements_revisions WHERE task_id=? AND state='pending')`, task).Scan(&pending)
	return pending, err
}

// RunRequirementsSuperseded identifies a result that predates an accepted revision request.
func RunRequirementsSuperseded(ctx context.Context, q Q, run string, task int64) (bool, error) {
	var superseded bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t WHERE t.id=? AND (
  EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.state='pending' AND r.task_id=t.id)
  OR EXISTS(SELECT 1 FROM run_requirements rr WHERE rr.run_id=? AND rr.task_id=t.id AND (rr.requirements_version<t.requirements_version OR rr.revision_id IS NOT (SELECT MAX(r.id) FROM requirements_revisions r WHERE r.task_id=t.id AND r.state='applied')))))`, task, run).Scan(&superseded)
	return superseded, err
}

// RequireNoRequirementsHold fences final decisions against superseded requirements.
func RequireNoRequirementsHold(ctx context.Context, q Q, task int64) error {
	var held bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t WHERE t.id=? AND (
		EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.state='pending' AND r.task_id=t.id)))`, task).Scan(&held)
	if err != nil {
		return err
	}
	if held {
		return Errf(CodeRevisionPending, "The task revision must complete before final approval.")
	}
	return nil
}

// RequestRequirementsRevision accepts the original command shape.
func RequestRequirementsRevision(ctx context.Context, q Q, task, expected int64, operation, requirements, reason, actor string) (int64, error) {
	return RequestTaskRevision(ctx, q, task, expected, operation, nil, &requirements, reason, actor)
}

// RequestTaskRevision preserves the request until each active operation ends.
func RequestTaskRevision(ctx context.Context, q Q, task, expected int64, operation string, title, description *string, instructions, actor string) (int64, error) {
	if err := RequireEditableRecord(ctx, q, task, expected); err != nil {
		return 0, err
	}
	var previous, previousTitle string
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(description,''),title FROM tasks WHERE id=?`, task).Scan(&previous, &previousTitle); err != nil {
		return 0, err
	}
	requirements, nextTitle := previous, previousTitle
	if description != nil {
		requirements = *description
	}
	if title != nil {
		nextTitle = *title
	}
	for _, value := range []string{requirements, nextTitle, instructions} {
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return 0, Errf(CodeInvalidRequest, "The revision must contain valid text.")
		}
	}
	if strings.TrimSpace(nextTitle) == "" {
		return 0, Errf(CodeInvalidRequest, "The title requires text.")
	}
	if strings.TrimSpace(requirements) == strings.TrimSpace(previous) {
		requirements = previous
	}
	if requirements == previous && nextTitle == previousTitle && strings.TrimSpace(instructions) == "" {
		return 0, Errf(CodeInvalidRequest, "Change the title or description, or write revision instructions.")
	}
	if pending, err := RequirementsRevisionPending(ctx, q, task); err != nil {
		return 0, err
	} else if pending {
		return 0, Errf(CodeRevisionPending, "The task already has a pending revision.")
	}
	res, err := q.ExecContext(ctx, `INSERT INTO requirements_revisions(operation_id,task_id,expected_version,requirements,previous_requirements,title,previous_title,reason,actor,previous_requirements_version)
		VALUES(?,?,?,?,?,?,?,?,?,(SELECT requirements_version FROM tasks WHERE id=?))`, operation, task, expected, requirements, previous, nextTitle, previousTitle, instructions, actor, task)
	if err != nil {
		return 0, err
	}
	revision, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := releaseReviewSlot(ctx, q, task); err != nil {
		return 0, err
	}
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET record_version=record_version+1 WHERE id=?`, task); err != nil {
		return 0, err
	}
	if err := ApplyTaskRequirementsRevision(ctx, q, revision); err != nil {
		return 0, err
	}
	return revision, nil
}

func releaseReviewSlot(ctx context.Context, q Q, task int64) error {
	_, err := q.ExecContext(ctx, `UPDATE tasks SET review_slot_held=0 WHERE id=? AND archived=0`, task)
	return err
}

// ApplyTaskRequirementsRevision uses the actual state after the active agent
// run and each active repository operation of the task end. A cleanup does not
// hold the revision. The applied revision ends an operator decision report and
// keeps each other Decision Pending condition. The base commit and the
// checkpoint remain.
func ApplyTaskRequirementsRevision(ctx context.Context, q Q, revision int64) error {
	var task int64
	var requirements, reason, state, status, previous string
	var title sql.NullString
	var archived bool
	var decision sql.NullString
	err := q.QueryRowContext(ctx, `SELECT r.task_id,r.requirements,r.reason,r.state,t.status,t.archived,t.decision,r.previous_requirements,r.title
		FROM requirements_revisions r JOIN tasks t ON t.id=r.task_id WHERE r.id=?`, revision).
		Scan(&task, &requirements, &reason, &state, &status, &archived, &decision, &previous, &title)
	if err != nil {
		return err
	}
	if state != "pending" {
		return nil
	}
	if archived || decision.Valid {
		_, err := q.ExecContext(ctx, `UPDATE requirements_revisions SET state='cancelled',failure_reason='The record reached a final decision.' WHERE id=?`, revision)
		return err
	}
	var active bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM leases l JOIN run_requirements rr ON rr.run_id=l.run_id
		WHERE rr.task_id=?)
		OR EXISTS(SELECT 1 FROM repository_executions x JOIN run_requirements rr ON rr.run_id=x.run_id
		WHERE x.ended_at IS NULL AND x.operation<>'cleanup'
		AND rr.task_id=?)
		OR EXISTS(SELECT 1 FROM version_submissions s WHERE s.captured_version IS NULL
		AND s.task_id=?)`,
		task, task, task).Scan(&active); err != nil {
		return err
	}
	if active {
		return nil
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO requirements_evidence(revision_id,task_id,requirements_version,status,completion_sha,verified_sha,correction_count,delivery)
		SELECT ?,id,requirements_version,status,completion_sha,(SELECT result_commit FROM task_versions WHERE id=t.source_version),correction_count,
		json_object('review_id',t.current_review_id,'submitted_version',t.submitted_version,'checkpoint_version',t.checkpoint_version,'candidate_id',t.candidate_id,
		'report_path',t.report_path,'report_sha256',t.report_sha256,'thesis',t.thesis,
		'input_commit',t.input_commit,'base_commit',t.base_commit,
		'verified_revision',COALESCE((SELECT rr.revision_id FROM version_verdicts v JOIN run_requirements rr ON rr.run_id=v.run_id
		WHERE rr.task_id=t.id AND v.outcome='pass' ORDER BY v.version_id DESC LIMIT 1),0),
		'worker_report',(SELECT output_path FROM runs WHERE subject=t.id AND role='worker' AND output_path IS NOT NULL ORDER BY created_at DESC,created_seq DESC LIMIT 1),
		'verifier_correction',(SELECT correction_path FROM runs WHERE subject=t.id AND role='verifier' AND verdict_outcome IS NOT NULL ORDER BY created_at DESC,created_seq DESC LIMIT 1),
		'verifier_evidence',(SELECT evidence_path FROM runs WHERE subject=t.id AND role='verifier' AND evidence_path IS NOT NULL ORDER BY created_at DESC,created_seq DESC LIMIT 1))
		FROM tasks t WHERE id=?`, revision, task); err != nil {
		return err
	}
	var verifierLimit bool
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(allowance_condition='verifier',0) FROM tasks WHERE id=?`, task).Scan(&verifierLimit); err != nil {
		return err
	}
	changed := requirements != previous
	if verifierLimit && changed {
		if err := clearAllowance(ctx, q, task, "verifier"); err != nil {
			return err
		}
	}
	if err := releaseReviewSlot(ctx, q, task); err != nil {
		return err
	}
	if status != "proposed" {
		status = "seeded"
	}
	// An open merge repair keeps its conflict: the revised requirements can
	// hold the operator decision about that conflict.
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET title=COALESCE(?,title),description=?,requirements_version=requirements_version+?,
		status=?,completion_sha=NULL,correction_count=CASE WHEN ? THEN 0 ELSE correction_count END,input_commit=COALESCE(
		(SELECT result_commit FROM task_versions WHERE id=tasks.submitted_version),input_commit),
		revision_notes=CASE WHEN returned_kind='merge' AND correction_open=1 THEN revision_notes END,
		correction_reason=CASE WHEN NOT ? OR (returned_kind='merge' AND correction_open=1) THEN correction_reason END,
		correction_open=CASE WHEN returned_kind='merge' AND correction_open=1 THEN 1 WHEN ? THEN 0 ELSE 1 END,
		returned_kind=CASE WHEN returned_kind='merge' AND correction_open=1 THEN 'merge' ELSE 'operator' END,
		submitted_version=CASE WHEN ? THEN NULL ELSE submitted_version END,source_version=NULL,
		candidate_id=NULL,report_path=NULL,report_sha256=NULL,thesis=NULL,
		verifier_correction_open=CASE WHEN ? THEN 0 ELSE verifier_correction_open END,correction_operator_reason=NULL,merge_after=NULL WHERE id=?`, title, nullIfEmpty(requirements), changed, status, changed, changed, changed, changed, changed, task); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET blocked=0 WHERE id=? AND blocked=1 AND blocked_kind='report'`, task); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE requirements_revisions SET state='applied',applied_at=datetime('now') WHERE id=?`, revision); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES('mc','requirements.applied',?,?)`, task, reason)
	return err
}

// ApplyPendingTaskRevisions runs before dispatch selects another task execution.
func ApplyPendingTaskRevisions(ctx context.Context, q Q) error {
	rows, err := q.QueryContext(ctx, `SELECT r.id FROM requirements_revisions r
		WHERE r.state='pending' ORDER BY r.requested_at,r.id`)
	if err != nil {
		return err
	}
	var revisions []int64
	for rows.Next() {
		var revision int64
		if err := rows.Scan(&revision); err != nil {
			rows.Close()
			return err
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, revision := range revisions {
		if err := ApplyTaskRequirementsRevision(ctx, q, revision); err != nil {
			return err
		}
	}
	return nil
}
