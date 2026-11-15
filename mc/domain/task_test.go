// Task state machine suite: the Arbiter arms, the forward advances, the
// Verifier verdicts, the correction requests, the blocks, and the decisions.
// Each rule class has one case where the domain names the rejection and the
// same raw write aborts on the substrate.
package domain_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"mc/domain"
)

func TestPromote(t *testing.T) {
	t.Run("proposed_to_seeded", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "proposed")
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Promote(ctx, q, id)
		})
		if got := taskStr(t, db, id, "status"); got != "seeded" {
			t.Fatalf("status = %q, want seeded", got)
		}
	})

	t.Run("non_proposed_rejected_both_layers", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "verified")
		wantCode(t, db, domain.CodeIllegalTransition, func(ctx context.Context, q domain.Q) error {
			return domain.Promote(ctx, q, id)
		})
		wantAbort(t, db, `UPDATE tasks SET status = 'seeded' WHERE id = ?`, id)
	})

	t.Run("decided_rejected", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "proposed")
		mustExec(t, db, `UPDATE tasks SET decision = 'rejected', decided_at = datetime('now'), archived = 1 WHERE id = ?`, id)
		wantCode(t, db, domain.CodeArchived, func(ctx context.Context, q domain.Q) error {
			return domain.Promote(ctx, q, id)
		})
	})

	t.Run("missing_task", func(t *testing.T) {
		db := openSpine(t)
		wantCode(t, db, domain.CodeNotFound, func(ctx context.Context, q domain.Q) error {
			return domain.Promote(ctx, q, 999)
		})
	})

	t.Run("blocked_proposal_refused", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "proposed")
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Block(ctx, q, id, domain.BlockOperator, "operator hold")
		})
		wantCode(t, db, domain.CodeBlocked, func(ctx context.Context, q domain.Q) error {
			return domain.Promote(ctx, q, id)
		})
		wantAbort(t, db, `UPDATE tasks SET status = 'seeded' WHERE id = ?`, id)
	})

}

func TestProposalTitlePrivateCarrierAdmission(t *testing.T) {
	db := openSpine(t)
	wantCode(t, db, domain.CodeCarrierForbidden, func(ctx context.Context, q domain.Q) error {
		_, err := domain.BirthProposal(ctx, q, domain.ProposalArgs{
			Title: strings.Repeat("x", 4097), Origin: "user", Project: "ws",
		})
		return err
	})
	if got := oneInt(t, db, `SELECT COUNT(*) FROM tasks`); got != 0 {
		t.Fatalf("overlong title admitted %d tasks", got)
	}
}

func TestRejectProposal(t *testing.T) {
	t.Run("rejects_archives_and_records_reason", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "proposed")
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.RejectProposal(ctx, q, id, "duplicate of existing work")
		})
		if got := taskStr(t, db, id, "decision"); got != "rejected" {
			t.Fatalf("decision = %q", got)
		}
		if got := taskInt(t, db, id, "archived"); got != 1 {
			t.Fatalf("not archived")
		}
		if got := taskStr(t, db, id, "decided_at"); got == "<NULL>" {
			t.Fatalf("decided_at not stamped")
		}
		if got := oneStr(t, db, `SELECT detail FROM activity WHERE kind = 'task.rejected' AND subject = ?`, id); got != "duplicate of existing work" {
			t.Fatalf("activity reason = %q", got)
		}
	})

	t.Run("reason_mandatory", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "proposed")
		wantCode(t, db, domain.CodeReasonRequired, func(ctx context.Context, q domain.Q) error {
			return domain.RejectProposal(ctx, q, id, "")
		})
	})

	t.Run("non_proposed_rejected", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		wantCode(t, db, domain.CodeIllegalTransition, func(ctx context.Context, q domain.Q) error {
			return domain.RejectProposal(ctx, q, id, "r")
		})
	})

	t.Run("blocked_proposal_refused", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "proposed")
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Block(ctx, q, id, domain.BlockOperator, "operator hold")
		})
		wantCode(t, db, domain.CodeBlocked, func(ctx context.Context, q domain.Q) error {
			return domain.RejectProposal(ctx, q, id, "stale Arbiter verdict")
		})
		wantAbort(t, db, `UPDATE tasks SET decision = 'rejected', decided_at = datetime('now'), archived = 1 WHERE id = ?`, id)
	})
}

func TestAdvanceStage(t *testing.T) {
	t.Run("seeded_to_worked", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.AdvanceStage(ctx, q, id, "worked")
		})
		if got := taskStr(t, db, id, "status"); got != "worked" {
			t.Fatalf("status = %q", got)
		}
	})

	t.Run("verified_to_packaged_clears_revision_notes", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "verified")
		seedVerifiedVersion(t, db, id)
		mustExec(t, db, `UPDATE tasks SET revision_notes = 'this round''s brief' WHERE id = ?`, id)
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.AdvanceStage(ctx, q, id, "packaged")
		})
		if got := taskStr(t, db, id, "status"); got != "packaged" {
			t.Fatalf("status = %q", got)
		}
		if got := taskStr(t, db, id, "revision_notes"); got != "<NULL>" {
			t.Fatalf("revision_notes = %q, want cleared after the packet completes", got)
		}
	})

	// The Packager requires the verified submitted version. The domain permits
	// the edge, and the substrate aborts it.
	t.Run("packaged_requires_verified_version", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "verified")
		wantReject(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.AdvanceStage(ctx, q, id, "packaged")
		})
		wantAbort(t, db, `UPDATE tasks SET status = 'packaged' WHERE id = ?`, id)
		if got := taskStr(t, db, id, "status"); got != "verified" {
			t.Fatalf("status = %q, want verified", got)
		}
	})

	t.Run("illegal_edges_rejected_both_layers", func(t *testing.T) {
		tests := []struct {
			name, from, to string
		}{
			{"proposed_to_worked", "proposed", "worked"},
			{"verified_to_worked", "verified", "worked"},
			{"seeded_to_packaged", "seeded", "packaged"},
			{"packaged_to_worked", "packaged", "worked"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				db := openSpine(t)
				id := mkTask(t, db, "task", tc.from)
				wantCode(t, db, domain.CodeIllegalTransition, func(ctx context.Context, q domain.Q) error {
					return domain.AdvanceStage(ctx, q, id, tc.to)
				})
				wantAbort(t, db, `UPDATE tasks SET status = ? WHERE id = ?`, tc.to, id)
			})
		}
	})

	t.Run("unknown_target_rejected", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		wantCode(t, db, domain.CodeIllegalTransition, func(ctx context.Context, q domain.Q) error {
			return domain.AdvanceStage(ctx, q, id, "verified") // only a verdict moves a task to verified
		})
	})
}

func applyVerdict(t *testing.T, db *sql.DB, a domain.VerdictArgs) (domain.VerdictResult, error) {
	t.Helper()
	var res domain.VerdictResult
	err := tx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		res, err = domain.ApplyVerdict(ctx, q, a)
		return err
	})
	return res, err
}

func TestApplyVerdictPass(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "worked")
	version := submitVersion(t, db, id, "w1", "v1", "abc123")

	res, err := applyVerdict(t, db, domain.VerdictArgs{
		TaskID: id, RunID: "v1", Outcome: "pass",
		EvidencePath: "e.md", VerifiedSHA: "abc123",
	})
	if err != nil {
		t.Fatalf("pass verdict: %v", err)
	}
	if res.Status != "verified" || res.CorrectionCount != 0 {
		t.Fatalf("result = %+v", res)
	}
	if got := taskStr(t, db, id, "status"); got != "verified" {
		t.Fatalf("status = %q", got)
	}
	if got := oneStr(t, db, `SELECT v.result_commit FROM tasks t JOIN task_versions v ON v.id=t.source_version WHERE t.id=?`, id); got != "abc123" {
		t.Fatalf("verified_sha = %q", got)
	}
	// The verdict record lands on the Verifier's own runs row and on the version.
	if got := oneStr(t, db, `SELECT verdict_outcome || '/' || evidence_path FROM runs WHERE id = 'v1'`); got != "pass/e.md" {
		t.Fatalf("verdict record = %q", got)
	}
	if got := oneStr(t, db, `SELECT outcome FROM version_verdicts WHERE version_id = ?`, version); got != "pass" {
		t.Fatalf("version verdict = %q", got)
	}
	if got := deliveryInt(t, db, id, "source_version"); got != version {
		t.Fatalf("source_version = %d, want the verified version %d", got, version)
	}
}

func TestApplyVerdictCorrect(t *testing.T) {
	// The first correction opens a correction round. The count grows when the
	// next version concludes the round.
	t.Run("correct_sends_back_and_opens_the_correction", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "worked")
		submitVersion(t, db, id, "w1", "v1", "c1")
		mustExec(t, db, `UPDATE tasks SET dispatch_retries = 3 WHERE id = ?`, id)

		res, err := applyVerdict(t, db, domain.VerdictArgs{
			TaskID: id, RunID: "v1", Outcome: "correct",
			EvidencePath: "e.md", CorrectionPath: "corrections/mc-1-corrections1", Problem: "The result misses a criterion.", Evidence: "The check failed.", Resolution: "Correct the result.",
		})
		if err != nil {
			t.Fatalf("correct verdict: %v", err)
		}
		if res.Status != "seeded" || res.CorrectionCount != 0 {
			t.Fatalf("result = %+v", res)
		}
		if got := deliveryInt(t, db, id, "correction_open"); got != 1 {
			t.Fatalf("correction_open = %d, want 1", got)
		}
		if got := deliveryStr(t, db, id, "correction_reason"); got != "corrections/mc-1-corrections1" {
			t.Fatalf("correction_reason = %q", got)
		}
		if got := taskStr(t, db, id, "status"); got != "seeded" {
			t.Fatalf("status = %q", got)
		}
		if got := oneStr(t, db, `SELECT correction_path FROM runs WHERE id = 'v1'`); got != "corrections/mc-1-corrections1" {
			t.Fatalf("correction_path = %q", got)
		}
		// A correction verdict leaves the infrastructure allowance untouched.
		if got := taskInt(t, db, id, "dispatch_retries"); got != 3 {
			t.Fatalf("dispatch_retries = %d, want 3 untouched", got)
		}

		// A pass restores the Verifier allowance. Project priority continues until the merge check.
		mustExec(t, db, `UPDATE tasks SET status = 'worked' WHERE id = ?`, id)
		version := submitVersion(t, db, id, "w2", "v2", "c2")
		res, err = applyVerdict(t, db, domain.VerdictArgs{
			TaskID: id, RunID: "v2", Outcome: "pass", EvidencePath: "e.md", VerifiedSHA: "c2",
		})
		if err != nil {
			t.Fatalf("pass after correction: %v", err)
		}
		if res.Status != "verified" || res.CorrectionCount != 0 {
			t.Fatalf("result = %+v, want verified with the round still open", res)
		}
		if got := deliveryInt(t, db, id, "correction_open"); got != 1 {
			t.Fatalf("correction_open = %d, want 1 until the candidate concludes the round", got)
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.ConcludeCorrection(ctx, q, id, version, "pass")
		})
		if got := oneStr(t, db, `SELECT outcome FROM correction_results WHERE version_id = ?`, version); got != "pass" {
			t.Fatalf("correction result = %q, want pass", got)
		}
		if got := taskInt(t, db, id, "correction_count"); got != 0 {
			t.Fatalf("correction_count = %d, want 0", got)
		}
		if got := deliveryInt(t, db, id, "verifier_correction_open"); got != 0 {
			t.Fatalf("correction_open = %d, want 0", got)
		}
		// A second conclusion of the same version counts nothing.
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.ConcludeCorrection(ctx, q, id, version, "pass")
		})
		if got := taskInt(t, db, id, "correction_count"); got != 0 {
			t.Fatalf("correction_count = %d after a repeat, want 0", got)
		}
	})

	t.Run("correction_path_required", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "worked")
		submitVersion(t, db, id, "w1", "v1", "c1")
		wantCode(t, db, domain.CodeCorrectionRequired, func(ctx context.Context, q domain.Q) error {
			_, err := domain.ApplyVerdict(ctx, q, domain.VerdictArgs{
				TaskID: id, RunID: "v1", Outcome: "correct", EvidencePath: "e",
			})
			return err
		})
	})

	// Three corrections finish through real verdicts. The fourth correction
	// request blocks the task. The count never exceeds three.
	t.Run("fourth_correction_blocks_the_task", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "worked")
		for round := 1; round <= 4; round++ {
			if round > 1 {
				mustExec(t, db, `UPDATE tasks SET status = 'worked' WHERE id = ?`, id)
			}
			worker := fmt.Sprintf("w%d", round)
			verifier := fmt.Sprintf("v%d", round)
			submitVersion(t, db, id, worker, verifier, "c"+worker)
			res, err := applyVerdict(t, db, domain.VerdictArgs{
				TaskID: id, RunID: verifier, Outcome: "correct",
				EvidencePath: "e", CorrectionPath: "c", Problem: "The result misses a criterion.", Evidence: "The check failed.", Resolution: "Correct the result.",
			})
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
			if res.CorrectionCount != round-1 {
				t.Fatalf("round %d count = %d, want %d", round, res.CorrectionCount, round-1)
			}
			if round < 4 && res.Status != "seeded" {
				t.Fatalf("round %d status = %q, want seeded", round, res.Status)
			}
		}
		if got := taskStr(t, db, id, "status"); got != "worked" {
			t.Fatalf("status = %q, want worked: a block keeps the pipeline position", got)
		}
		if got := taskInt(t, db, id, "blocked"); got != 1 {
			t.Fatalf("blocked = %d, want 1 after three corrections", got)
		}
		if got := taskStr(t, db, id, "blocked_reason"); !strings.HasPrefix(got, "The Verifier rejected three corrections.") {
			t.Fatalf("blocked_reason = %q", got)
		}
		if got := taskInt(t, db, id, "correction_count"); got != 3 {
			t.Fatalf("correction_count = %d, want 3", got)
		}
		wantAbort(t, db, `UPDATE tasks SET correction_count = 4 WHERE id = ?`, id)
	})
}

func TestApplyVerdictCarrierMatrix(t *testing.T) {
	tests := []struct {
		name string
		args domain.VerdictArgs
		code string
	}{
		{name: "pass_requires_evidence", args: domain.VerdictArgs{Outcome: "pass", VerifiedSHA: "c1"}, code: domain.CodeIllegalTransition},
		{name: "pass_requires_the_submitted_commit", args: domain.VerdictArgs{Outcome: "pass", EvidencePath: "e", VerifiedSHA: "other"}, code: domain.CodeStaleRun},
		{name: "correct_requires_evidence", args: domain.VerdictArgs{Outcome: "correct", CorrectionPath: "c"}, code: domain.CodeIllegalTransition},
		{name: "correct_requires_correction", args: domain.VerdictArgs{Outcome: "correct", EvidencePath: "e"}, code: domain.CodeCorrectionRequired},
		{name: "correct_requires_problem", args: domain.VerdictArgs{Outcome: "correct", EvidencePath: "e", CorrectionPath: "c", Evidence: "The check requires a correction.", Resolution: "The check requires a correction."}, code: domain.CodeCorrectionRequired},
		{name: "correct_requires_explanation_evidence", args: domain.VerdictArgs{Outcome: "correct", EvidencePath: "e", CorrectionPath: "c", Problem: "The check requires a correction.", Resolution: "The check requires a correction."}, code: domain.CodeCorrectionRequired},
		{name: "correct_requires_resolution", args: domain.VerdictArgs{Outcome: "correct", EvidencePath: "e", CorrectionPath: "c", Problem: "The check requires a correction.", Evidence: "The check requires a correction."}, code: domain.CodeCorrectionRequired},
		{name: "unknown_outcome", args: domain.VerdictArgs{Outcome: "budget-spent", EvidencePath: "e", VerifiedSHA: "c1"}, code: domain.CodeIllegalTransition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openSpine(t)
			id := mkTask(t, db, "task", "worked")
			version := submitVersion(t, db, id, "w1", "v1", "c1")
			a := tc.args
			a.TaskID, a.RunID = id, "v1"
			wantCode(t, db, tc.code, func(ctx context.Context, q domain.Q) error {
				_, err := domain.ApplyVerdict(ctx, q, a)
				return err
			})
			if got := taskStr(t, db, id, "status"); got != "worked" {
				t.Fatalf("invalid carrier moved task to %q", got)
			}
			if got := oneStr(t, db, `SELECT verdict_outcome FROM runs WHERE id = 'v1'`); got != "<NULL>" {
				t.Fatalf("invalid carrier wrote verdict %q", got)
			}
			if got := oneInt(t, db, `SELECT COUNT(*) FROM version_verdicts WHERE version_id = ?`, version); got != 0 {
				t.Fatalf("invalid carrier wrote %d version verdicts", got)
			}
		})
	}
}

// A verdict requires the Verifier run that the dispatch assigned to the
// submitted version. Another run cannot record it.
func TestApplyVerdictRequiresTheAssignedVerifierRun(t *testing.T) {
	for _, tc := range []struct {
		name    string
		first   string // the commit of the first submitted version
		makeRun func(t *testing.T, db *sql.DB, taskID int64)
	}{
		{name: "missing_run", first: "c1", makeRun: func(t *testing.T, db *sql.DB, taskID int64) {}},
		{name: "wrong_role", first: "c1", makeRun: func(t *testing.T, db *sql.DB, taskID int64) {
			mkRun(t, db, "v1", "worker", taskID)
			mustExec(t, db, `INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,version_id,temporary_bytes)
				SELECT 'v1',id,'c1','base',submitted_version,0 FROM tasks WHERE id=?`, taskID)
		}},
		{name: "wrong_subject", first: "c1", makeRun: func(t *testing.T, db *sql.DB, taskID int64) {
			other := mkTask(t, db, "task", "worked")
			submitVersion(t, db, other, "w-other", "v1", "c1")
		}},
		{name: "older_version", first: "c0", makeRun: func(t *testing.T, db *sql.DB, taskID int64) {
			mkRun(t, db, "v1", "verifier", taskID)
			mustExec(t, db, `INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,version_id,temporary_bytes)
				SELECT 'v1',id,'c0','base',submitted_version,0 FROM tasks WHERE id=?`, taskID)
			submitVersion(t, db, taskID, "w-next", "v-next", "c1")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openSpine(t)
			id := mkTask(t, db, "task", "worked")
			submitVersion(t, db, id, "w0", "v0", tc.first)
			tc.makeRun(t, db, id)
			wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
				_, err := domain.ApplyVerdict(ctx, q, domain.VerdictArgs{
					TaskID: id, RunID: "v1", Outcome: "pass",
					EvidencePath: "e", VerifiedSHA: "c1",
				})
				return err
			})
			if got := taskStr(t, db, id, "status"); got != "worked" {
				t.Fatalf("invalid run moved task to %q", got)
			}
			if got := oneInt(t, db, `SELECT COUNT(*) FROM version_verdicts`); got != 0 {
				t.Fatalf("invalid run wrote %d version verdicts", got)
			}
		})
	}
}

// An operator revision keeps the packet. The next verdict decides the new version.
func TestApplyVerdictAfterRevision(t *testing.T) {
	for _, outcome := range []string{"pass", "correct"} {
		t.Run(outcome, func(t *testing.T) {
			db := openSpine(t)
			id := mkTask(t, db, "task", "packaged")
			mkPacket(t, db, id)
			mustTx(t, db, func(ctx context.Context, q domain.Q) error {
				return domain.RequestCorrection(ctx, q, id, "Add the requested evidence.")
			})
			mustExec(t, db, `UPDATE tasks SET status = 'worked' WHERE id = ?`, id)
			submitVersion(t, db, id, "w1", "v1", "revised")
			args := domain.VerdictArgs{TaskID: id, RunID: "v1", Outcome: outcome, EvidencePath: "evidence.md"}
			wantStatus := "verified"
			if outcome == "correct" {
				args.CorrectionPath = "correction.md"
				args.Problem = "The result misses a criterion."
				args.Evidence = "The check failed."
				args.Resolution = "Correct the result."
				wantStatus = "seeded"
			} else {
				args.VerifiedSHA = "revised"
			}
			result, err := applyVerdict(t, db, args)
			if err != nil || result.Status != wantStatus {
				t.Fatalf("Revision verdict: result=%+v error=%v", result, err)
			}
			if oneInt(t, db, `SELECT COUNT(*) FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ? AND t.archived = 0`, id) != 1 {
				t.Fatal("The revision lost its packet.")
			}
		})
	}
}

func TestApplyVerdictOnNonWorked(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	submitVersion(t, db, id, "w1", "v1", "c1")
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		_, err := domain.ApplyVerdict(ctx, q, domain.VerdictArgs{
			TaskID: id, RunID: "v1", Outcome: "pass", EvidencePath: "e", VerifiedSHA: "c1",
		})
		return err
	})
	wantAbort(t, db, `UPDATE tasks SET status = 'verified' WHERE id = ?`, id)
}

func TestRequestCorrection(t *testing.T) {
	// The operator revision moves a packaged task to seeded. The packet stays
	// live, and the next Worker receives the reason.
	t.Run("packaged_to_seeded_with_reason", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		mkPacket(t, db, id)
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.RequestCorrection(ctx, q, id, "deepen the risk section")
		})
		if got := taskStr(t, db, id, "status"); got != "seeded" {
			t.Fatalf("status = %q", got)
		}
		if got := taskStr(t, db, id, "revision_notes"); got != "deepen the risk section" {
			t.Fatalf("revision_notes = %q", got)
		}
		if got := deliveryInt(t, db, id, "correction_open"); got != 1 {
			t.Fatalf("correction_open = %d, want 1", got)
		}
		if got := deliveryStr(t, db, id, "source_version"); got != "<NULL>" {
			t.Fatalf("source_version = %q, want no verified source for the new version", got)
		}
		if got := oneInt(t, db, `SELECT archived FROM tasks WHERE id = ?`, id); got != 0 {
			t.Fatalf("the revision archived the packet")
		}
	})

	t.Run("reason_mandatory", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		mkPacket(t, db, id)
		wantCode(t, db, domain.CodeReasonRequired, func(ctx context.Context, q domain.Q) error {
			return domain.RequestCorrection(ctx, q, id, "")
		})
		if got := taskStr(t, db, id, "status"); got != "packaged" {
			t.Fatalf("status = %q, want packaged", got)
		}
	})

	t.Run("packet_revision_preserves_exhausted_counts", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		mkPacket(t, db, id)
		mustExec(t, db, `UPDATE tasks SET correction_count=3 WHERE id=?`, id)
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.RequestPacketRevision(ctx, q, id, "Clarify the packet.")
		})
		if taskStr(t, db, id, "status") != "seeded" || taskInt(t, db, id, "correction_count") != 3 {
			t.Fatal("The packet revision changed an allowance.")
		}
	})

}

func TestBlockUnblock(t *testing.T) {
	t.Run("block_sets_flag_and_reason", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "worked")
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Block(ctx, q, id, domain.BlockOperator, "needs the prod credentials decision")
		})
		if got := taskInt(t, db, id, "blocked"); got != 1 {
			t.Fatalf("blocked = %d", got)
		}
		if got := taskStr(t, db, id, "blocked_reason"); got != "needs the prod credentials decision" {
			t.Fatalf("blocked_reason = %q", got)
		}
		// A block keeps the pipeline position.
		if got := taskStr(t, db, id, "status"); got != "worked" {
			t.Fatalf("status = %q, want worked untouched", got)
		}
	})

	t.Run("block_records_and_replaces_the_condition_type", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "worked")
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Block(ctx, q, id, domain.BlockReport, "The criteria contradict each other.")
		})
		if got := taskStr(t, db, id, "blocked_kind"); got != domain.BlockReport {
			t.Fatalf("blocked_kind = %q", got)
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Block(ctx, q, id, domain.BlockOperator, "The operator holds the task.")
		})
		if got := taskStr(t, db, id, "blocked_kind") + "/" + taskStr(t, db, id, "blocked_reason"); got != "operator/The operator holds the task." {
			t.Fatalf("the later block kept %q", got)
		}
		for _, kind := range []string{domain.BlockAllowance, domain.BlockProposal, ""} {
			wantCode(t, db, domain.CodeInvalidRequest, func(ctx context.Context, q domain.Q) error {
				return domain.Block(ctx, q, id, kind, "The condition requires the operator.")
			})
		}
	})

	t.Run("block_without_reason_rejected_both_layers", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		wantCode(t, db, domain.CodeReasonRequired, func(ctx context.Context, q domain.Q) error {
			return domain.Block(ctx, q, id, domain.BlockOperator, "")
		})
		wantAbort(t, db, `UPDATE tasks SET blocked = 1 WHERE id = ?`, id)
	})

	t.Run("unblock_clears_reason", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Block(ctx, q, id, domain.BlockOperator, "r")
		})
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Unblock(ctx, q, id)
		})
		if got := taskInt(t, db, id, "blocked"); got != 0 {
			t.Fatalf("blocked = %d", got)
		}
		if got := taskStr(t, db, id, "blocked_reason"); got != "<NULL>" {
			t.Fatalf("blocked_reason = %q, want cleared by the trigger", got)
		}
	})

	t.Run("unblock_unblocked_rejected", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		wantCode(t, db, domain.CodeNotBlocked, func(ctx context.Context, q domain.Q) error {
			return domain.Unblock(ctx, q, id)
		})
	})
}

func TestCancel(t *testing.T) {
	t.Run("cancels_archives_and_records_reason", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		mkPacket(t, db, id)
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Cancel(ctx, q, id, "direction changed", "operator")
		})
		if got := taskStr(t, db, id, "decision"); got != "cancelled" {
			t.Fatalf("decision = %q", got)
		}
		if got := taskInt(t, db, id, "archived"); got != 1 {
			t.Fatalf("not archived")
		}
		if got := oneInt(t, db, `SELECT archived FROM tasks WHERE id = ?`, id); got != 1 {
			t.Fatalf("packet not cascaded")
		}
		if got := oneStr(t, db, `SELECT detail FROM activity WHERE kind = 'task.cancelled' AND subject = ?`, id); got != "direction changed" {
			t.Fatalf("activity reason = %q", got)
		}
	})

	t.Run("reason_mandatory", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		wantCode(t, db, domain.CodeReasonRequired, func(ctx context.Context, q domain.Q) error {
			return domain.Cancel(ctx, q, id, "", "operator")
		})
	})

}

func TestBirthProposal(t *testing.T) {
	db := openSpine(t)
	var id int64
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		id, err = domain.BirthProposal(ctx, q, domain.ProposalArgs{
			Title: "an idea", Description: "criteria", StrategicRationale: "Observed: the export omits the date.\r\nAssumed: each client reads it.",
			Origin: "autonomous", Project: "ws",
		})
		return err
	})
	if got := taskStr(t, db, id, "status"); got != "proposed" {
		t.Fatalf("status = %q", got)
	}
	if got := taskStr(t, db, id, "origin"); got != "autonomous" {
		t.Fatalf("origin = %q", got)
	}
	if got := taskStr(t, db, id, "strategic_rationale"); got != "Observed: the export omits the date.\nAssumed: each client reads it." {
		t.Fatalf("strategic_rationale = %q", got)
	}
	if got := taskStr(t, db, id, "description"); got != "criteria" {
		t.Fatalf("description = %q", got)
	}
	// The schema trigger gives the task its delivery record.
	if got := deliveryStr(t, db, id, "destination_branch"); got != "culler" {
		t.Fatalf("destination_branch = %q, want culler", got)
	}

	wantCode(t, db, domain.CodeReasonRequired, func(ctx context.Context, q domain.Q) error {
		_, err := domain.BirthProposal(ctx, q, domain.ProposalArgs{Title: "", Project: "ws", Origin: "user"})
		return err
	})
	wantCode(t, db, domain.CodeIllegalTransition, func(ctx context.Context, q domain.Q) error {
		_, err := domain.BirthProposal(ctx, q, domain.ProposalArgs{
			Title: "Invalid scope", Scope: "other", Project: "ws", Origin: "user",
		})
		return err
	})
}

func TestBirthProposalStrategicRationale(t *testing.T) {
	db := openSpine(t)
	birth := func(origin, rationale string) func(context.Context, domain.Q) error {
		return func(ctx context.Context, q domain.Q) error {
			_, err := domain.BirthProposal(ctx, q, domain.ProposalArgs{
				Title: "an idea", Description: "criteria", StrategicRationale: rationale, Origin: origin, Project: "ws",
			})
			return err
		}
	}
	for _, rationale := range []string{"", "  ", "\n\t\r\n"} {
		wantCode(t, db, domain.CodeReasonRequired, birth("autonomous", rationale))
	}
	for _, rationale := range []string{
		strings.Repeat("x", domain.MaxStrategicRationaleBytes+1),
		"The rationale holds an escape \x1b[31m sequence.",
		"The rationale holds invalid text \xff.",
	} {
		wantCode(t, db, domain.CodeInvalidRequest, birth("autonomous", rationale))
	}
	wantCode(t, db, domain.CodeInvalidRequest, birth("user", "The operator needs no rationale."))
	if got := oneInt(t, db, `SELECT COUNT(*) FROM tasks`); got != 0 {
		t.Fatalf("refused proposals created %d tasks", got)
	}
	mustTx(t, db, birth("autonomous", strings.Repeat("x", domain.MaxStrategicRationaleBytes)))
	mustTx(t, db, birth("user", ""))
	if got := oneInt(t, db, `SELECT COUNT(*) FROM tasks WHERE (origin='autonomous') = (strategic_rationale IS NOT NULL)`); got != 2 {
		t.Fatalf("%d tasks hold the rationale of their origin, want 2", got)
	}
}
