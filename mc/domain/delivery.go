package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"mc/repository"
)

func ConcludeCorrection(ctx context.Context, q Q, task, version int64, outcome string) error {
	var open bool
	if err := q.QueryRowContext(ctx, `SELECT verifier_correction_open FROM tasks WHERE id=? AND submitted_version=?`, task, version).Scan(&open); err != nil {
		return err
	}
	if !open {
		if outcome == "pass" {
			_, err := q.ExecContext(ctx, `UPDATE tasks SET correction_count=0 WHERE id=? AND submitted_version=?`, task, version)
			return err
		}
		return nil
	}
	result, err := q.ExecContext(ctx, `INSERT INTO correction_results(version_id,task_id,outcome) VALUES(?,?,?) ON CONFLICT(version_id) DO NOTHING`, version, task, outcome)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 && outcome == "correct" {
		if _, err := q.ExecContext(ctx, `UPDATE tasks SET correction_count=correction_count+1 WHERE id=?`, task); err != nil {
			return err
		}
	}
	if outcome == "pass" {
		_, err = q.ExecContext(ctx, `UPDATE tasks SET verifier_correction_open=0,correction_count=0 WHERE id=?`, task)
	}
	return err
}

func RequestCorrection(ctx context.Context, q Q, task int64, reason string) error {
	if reason == "" {
		return Errf(CodeReasonRequired, "a correction requires its unresolved condition")
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT correction_count FROM tasks WHERE id=? AND archived=0`, task).Scan(&count); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET verifier_correction_open=1,correction_open=1,returned_kind='verifier',correction_reason=? WHERE id=?`, reason, task); err != nil {
		return err
	}
	if count >= 3 {
		var problem, evidence, resolution string
		if err := q.QueryRowContext(ctx, `SELECT problem,explanation_evidence,resolution FROM version_verdicts WHERE version_id=(SELECT submitted_version FROM tasks WHERE id=?)`, task).Scan(&problem, &evidence, &resolution); err != nil {
			return err
		}
		return blockAllowance(ctx, q, task, "verifier", "The Verifier rejected three corrections. "+problem+" Evidence: "+evidence+" Proposed resolution: "+resolution)
	}
	return returnToSeeded(ctx, q, task, "verifier", reason)
}

// ApplyVerdict records one Verifier verdict on the submitted version of the task.
func ApplyVerdict(ctx context.Context, q Q, a VerdictArgs) (VerdictResult, error) {
	var result VerdictResult
	if a.Outcome != "pass" && a.Outcome != "correct" {
		return result, Errf(CodeIllegalTransition, "the Verifier must pass the version or request a correction")
	}
	var version int64
	var commit, input, status string
	if err := q.QueryRowContext(ctx, `SELECT t.submitted_version,v.result_commit,r.input_commit,t.status
		FROM tasks t JOIN task_versions v ON v.id=t.submitted_version
		JOIN repository_runs r ON r.run_id=? AND r.task_id=t.id AND r.version_id=v.id
		JOIN runs run ON run.id=r.run_id AND run.role='verifier' WHERE t.id=?`, a.RunID, a.TaskID).
		Scan(&version, &commit, &input, &status); err != nil {
		if err == sql.ErrNoRows {
			return result, Errf(CodeStaleRun, "run %q is not the Verifier run of the submitted version of task %d", a.RunID, a.TaskID)
		}
		return result, err
	}
	if a.EvidencePath == "" {
		return result, Errf(CodeIllegalTransition, "the verdict requires an evidence path")
	}
	if status != "worked" || input != commit || (a.Outcome == "pass" && a.VerifiedSHA != commit) {
		return result, Errf(CodeStaleRun, "the verdict must assess the assigned submitted version")
	}
	if a.Outcome == "correct" && a.CorrectionPath == "" {
		return result, Errf(CodeCorrectionRequired, "the correction requires a durable report")
	}
	if a.Outcome == "correct" && (strings.TrimSpace(a.Problem) == "" || strings.TrimSpace(a.Evidence) == "" || strings.TrimSpace(a.Resolution) == "") {
		return result, Errf(CodeCorrectionRequired, "A correction requires the problem, evidence, and proposed resolution.")
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO version_verdicts(version_id,run_id,outcome,evidence_path,problem,explanation_evidence,resolution) VALUES(?,?,?,?,?,?,?)`, version, a.RunID, a.Outcome, a.EvidencePath, nullIfEmpty(a.Problem), nullIfEmpty(a.Evidence), nullIfEmpty(a.Resolution)); err != nil {
		return result, err
	}
	if _, err := q.ExecContext(ctx, `UPDATE runs SET verdict_outcome=?,evidence_path=?,correction_path=? WHERE id=?`, a.Outcome, a.EvidencePath, nullIfEmpty(a.CorrectionPath), a.RunID); err != nil {
		return result, err
	}
	if a.Outcome == "pass" {
		if err := ConcludeCorrection(ctx, q, a.TaskID, version, "pass"); err != nil {
			return result, err
		}
		_, err := q.ExecContext(ctx, `UPDATE tasks SET status='verified',source_version=? WHERE id=?`, version, a.TaskID)
		if err != nil {
			return result, err
		}
	} else {
		if pending, err := RunRequirementsSuperseded(ctx, q, a.RunID, a.TaskID); err != nil {
			return result, err
		} else if pending {
			err := q.QueryRowContext(ctx, `SELECT status,correction_count FROM tasks WHERE id=?`, a.TaskID).Scan(&result.Status, &result.CorrectionCount)
			return result, err
		}
		if err := ConcludeCorrection(ctx, q, a.TaskID, version, "correct"); err != nil {
			return result, err
		}
		if err := RequestCorrection(ctx, q, a.TaskID, a.CorrectionPath); err != nil {
			return result, err
		}
	}
	err := q.QueryRowContext(ctx, `SELECT status,correction_count FROM tasks WHERE id=?`, a.TaskID).Scan(&result.Status, &result.CorrectionCount)
	return result, err
}

func RecordCandidate(ctx context.Context, q Q, task int64, run string, version int64, c repository.Candidate, retained string) (int64, error) {
	var activeVersion int64
	var submitted, destination, status string
	if err := q.QueryRowContext(ctx, `SELECT t.submitted_version,v.result_commit,t.destination_branch,t.status
		FROM tasks t JOIN task_versions v ON v.id=t.submitted_version
		WHERE t.id=? AND t.archived=0 AND t.report_path IS NOT NULL`, task).Scan(&activeVersion, &submitted, &destination, &status); err != nil {
		return 0, err
	}
	if activeVersion != version || submitted != c.SubmittedCommit || destination != c.DestinationBranch || status != "packaged" {
		return 0, Errf(CodeStaleRun, "the merge result does not identify the active submitted version")
	}
	if c.Status == "merged" {
		if err := c.Validate(); err != nil {
			return 0, err
		}
		if retained == "" {
			return 0, Errf(CodeSHARequired, "the merged candidate requires a retained Git reference")
		}
	} else if c.Status != "conflict" {
		return 0, Errf(CodeIllegalTransition, "the merge result is invalid")
	}
	body, err := json.Marshal(c)
	if err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx, `INSERT INTO merge_candidates(task_id,run_id,version_id,destination_branch,destination_commit,candidate_commit,result,status,retained_ref)
		VALUES(?,?,?,?,?,?,?,?,?)`, task, run, version, destination, c.DestinationCommit, nullIfEmpty(c.Commit), string(body), c.Status, nullIfEmpty(retained))
	if err != nil {
		return 0, err
	}
	candidate, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := q.ExecContext(ctx, `UPDATE tasks SET candidate_id=? WHERE id=?`, candidate, task); err != nil {
		return 0, err
	}
	if c.Status == "conflict" {
		return candidate, RequestMergeRepair(ctx, q, task, candidate)
	}
	if err := CloseCorrection(ctx, q, task); err != nil {
		return 0, err
	}
	return candidate, PublishReview(ctx, q, task)
}

func PublishReview(ctx context.Context, q Q, task int64) error {
	var version, candidate int64
	var report, reportHash, thesis, criteria string
	if err := q.QueryRowContext(ctx, `SELECT t.submitted_version,t.candidate_id,t.report_path,t.report_sha256,t.thesis,COALESCE(t.description,'')
		FROM tasks t WHERE t.id=?`, task).Scan(&version, &candidate, &report, &reportHash, &thesis, &criteria); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO review_versions(task_id,version_id,candidate_id,render_path,report_sha256,thesis,criteria,requirements_version)
		VALUES(?,?,?,?,?,?,?,(SELECT requirements_version FROM tasks WHERE id=?)) ON CONFLICT(task_id,version_id) DO NOTHING`, task, version, candidate, report, reportHash, thesis, criteria, task); err != nil {
		return err
	}
	var decided sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT decision FROM tasks WHERE id=?`, task).Scan(&decided); err != nil {
		return err
	}
	if decided.Valid {
		return nil
	}
	var available bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND review_slot_held=1) OR
	 (SELECT count(*) FROM tasks WHERE archived=0 AND review_slot_held=1 AND project=(SELECT project FROM tasks WHERE id=?)) <
	 (SELECT review_wip_cap FROM settings WHERE id=1)`, task, task).Scan(&available); err != nil {
		return err
	}
	if !available {
		if err := RequireNoRequirementsHold(ctx, q, task); err == nil {
			return nil
		}
	}
	var review int64
	if err := q.QueryRowContext(ctx, `SELECT id FROM review_versions WHERE task_id=? AND version_id=?`, task, version).Scan(&review); err != nil {
		return err
	}
	return SelectReview(ctx, q, task, review)
}

func ApproveReview(ctx context.Context, q Q, task, packet, version int64) error {
	if err := RequireNoRequirementsHold(ctx, q, task); err != nil {
		return err
	}
	row, err := getTask(ctx, q, task)
	if err != nil {
		return err
	}
	if err := requireLive(row); err != nil {
		return err
	}
	var active int64
	var candidateStatus string
	if err := q.QueryRowContext(ctx, `SELECT t.submitted_version,c.status
		FROM tasks t JOIN review_versions p ON p.task_id=t.id AND p.id=? AND p.version_id=?
		JOIN merge_candidates c ON c.id=t.candidate_id AND c.version_id=p.version_id
		WHERE t.id=? AND t.status='packaged' AND t.archived=0 AND t.decision IS NULL AND t.blocked=0 AND t.stalled=0 AND t.current_review_id=p.id AND t.review_slot_held=1`, packet, version, task).
		Scan(&active, &candidateStatus); err != nil {
		if err == sql.ErrNoRows {
			return Errf(CodeStaleRun, "the reviewed packet is obsolete")
		}
		return err
	}
	if active != version || candidateStatus != "merged" {
		return Errf(CodeStaleRun, "the reviewed packet is obsolete")
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO review_decisions(packet_id,decision) VALUES(?,'approved')`, packet); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE tasks SET decision='approved',decided_at=datetime('now') WHERE id=?`, task)
	return err
}
