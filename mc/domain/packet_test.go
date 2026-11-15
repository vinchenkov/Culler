package domain_test

import (
	"context"
	"database/sql"
	"testing"

	"mc/domain"
)

func birthPacket(t *testing.T, db *sql.DB, taskID int64, render string) error {
	t.Helper()
	return tx(t, db, func(ctx context.Context, q domain.Q) error {
		return publishReviewFixture(ctx, q, taskID, render, "thesis")
	})
}

func TestPacketBirth(t *testing.T) {
	t.Run("born_from_live_packaged_task", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		if err := birthPacket(t, db, id, "packet/render.html"); err != nil {
			t.Fatalf("birth: %v", err)
		}
		if got := oneStr(t, db, `SELECT p.render_path FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, id); got != "packet/render.html" {
			t.Fatalf("render_path = %q", got)
		}
	})

	// The domain names the rejection. The trigger aborts the raw write.
	t.Run("non_packaged_rejected_both_layers", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "verified")
		wantCode(t, db, domain.CodeNotPackaged, func(ctx context.Context, q domain.Q) error {
			return publishReviewFixture(ctx, q, id, "r.html", "thesis")
		})
		wantAbort(t, db, `UPDATE tasks SET current_review_id=(SELECT id FROM review_versions WHERE task_id=tasks.id AND version_id=tasks.submitted_version),review_slot_held=1 WHERE id=?`, id)
	})

	// The domain names the cap. The trigger aborts the raw write.
	t.Run("wip_cap_rejected_both_layers", func(t *testing.T) {
		db := openSpine(t)
		mustExec(t, db, `UPDATE settings SET review_wip_cap = 2 WHERE id = 1`)
		for i := 0; i < 2; i++ {
			id := mkTask(t, db, "task", "packaged")
			if err := birthPacket(t, db, id, "r.html"); err != nil {
				t.Fatalf("packet %d: %v", i, err)
			}
		}
		third := mkTask(t, db, "task", "packaged")
		wantCode(t, db, domain.CodeWIPCap, func(ctx context.Context, q domain.Q) error {
			return publishReviewFixture(ctx, q, third, "r.html", "thesis")
		})
		seedReviewVersion(t, db, third)
		wantAbort(t, db, `UPDATE tasks SET current_review_id=(SELECT id FROM review_versions WHERE task_id=tasks.id AND version_id=tasks.submitted_version),review_slot_held=1 WHERE id=?`, third)
	})

	// A revision updates the existing packet.
	t.Run("repackaging_rerenders_in_place", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		if err := birthPacket(t, db, id, "v1.html"); err != nil {
			t.Fatalf("birth: %v", err)
		}

		// The revision moves the task from packaged to seeded. The task then
		// walks to packaged again with a new verified version.
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.RequestCorrection(ctx, q, id, "deepen")
		})
		mustExec(t, db, `UPDATE tasks SET status = 'worked' WHERE id = ?`, id)
		mustExec(t, db, `UPDATE tasks SET status = 'verified' WHERE id = ?`, id)
		seedVerifiedVersion(t, db, id)
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.AdvanceStage(ctx, q, id, "packaged")
		})

		if err := birthPacket(t, db, id, "v2.html"); err != nil {
			t.Fatalf("re-packaging: %v", err)
		}
		if got := oneInt(t, db, `SELECT COUNT(*) FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, id); got != 1 {
			t.Fatalf("packet rows = %d, want exactly one for the life of the task", got)
		}
		if got := oneStr(t, db, `SELECT p.render_path FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, id); got != "v2.html" {
			t.Fatalf("render_path = %q, want re-rendered in place", got)
		}
	})

	t.Run("missing_task", func(t *testing.T) {
		db := openSpine(t)
		wantCode(t, db, domain.CodeNotFound, func(ctx context.Context, q domain.Q) error {
			return publishReviewFixture(ctx, q, 999, "r.html", "thesis")
		})
	})

	t.Run("decided_task_cannot_birth_or_rerender_packet", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		if err := birthPacket(t, db, id, "v1.html"); err != nil {
			t.Fatalf("first birth: %v", err)
		}
		approveReviewed(t, db, id)
		if got := taskStr(t, db, id, "decision"); got != "approved" {
			t.Fatalf("decision = %q, want approved", got)
		}
		render := oneStr(t, db, `SELECT p.render_path FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, id)
		wantCode(t, db, domain.CodeAlreadyDecided, func(ctx context.Context, q domain.Q) error {
			return publishReviewFixture(ctx, q, id, "v2.html", "thesis v2")
		})
		if got := oneStr(t, db, `SELECT p.render_path FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, id); got != render {
			t.Fatalf("decided packet rerendered to %q", got)
		}
		wantAbort(t, db, `UPDATE review_versions SET render_path = 'v3.html' WHERE task_id = ?`, id)
	})
}

// An exact version preserves its original report and thesis.
func TestReviewPreservesTheReportForItsExactVersion(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "packaged")
	if err := tx(t, db, func(ctx context.Context, q domain.Q) error {
		return publishReviewFixture(ctx, q, id, "first.html", "the first claim")
	}); err != nil {
		t.Fatalf("birth: %v", err)
	}
	if got := oneStr(t, db, `SELECT p.thesis FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, id); got != "the first claim" {
		t.Fatalf("thesis = %q, want the birth thesis", got)
	}
	if err := tx(t, db, func(ctx context.Context, q domain.Q) error {
		return publishReviewFixture(ctx, q, id, "second.html", "the sharpened claim")
	}); err != nil {
		t.Fatalf("re-package: %v", err)
	}
	if got := oneStr(t, db, `SELECT p.thesis FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id = ?`, id); got != "the first claim" {
		t.Fatalf("thesis after re-packaging = %q, want the original claim", got)
	}
}

// An approval names the reviewed packet and version. A second approval and
// an approval of an unmatched pair are domain refusals, never raw errors.
func TestApproveReviewRefusesADecidedTaskAndAnObsoletePacket(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "packaged")
	if err := birthPacket(t, db, id, "v1.html"); err != nil {
		t.Fatalf("birth: %v", err)
	}
	seedMergedCandidate(t, db, id)
	version := deliveryInt(t, db, id, "submitted_version")
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		return domain.ApproveReview(ctx, q, id, 999, version)
	})
	if got := oneInt(t, db, `SELECT COUNT(*) FROM tasks WHERE id=? AND decision IS NOT NULL`, id); got != 0 {
		t.Fatal("an obsolete packet decided the task")
	}
	approveReviewed(t, db, id)
	packet := oneInt(t, db, `SELECT id FROM review_versions WHERE task_id=? ORDER BY id DESC LIMIT 1`, id)
	wantCode(t, db, domain.CodeAlreadyDecided, func(ctx context.Context, q domain.Q) error {
		return domain.ApproveReview(ctx, q, id, packet, version)
	})
	if got := oneInt(t, db, `SELECT COUNT(*) FROM review_decisions WHERE packet_id=?`, packet); got != 1 {
		t.Fatalf("review decisions = %d, want one", got)
	}
}

func TestReviewCapacitySurvivesCorrectionAndReleasesForRequirements(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `UPDATE settings SET review_wip_cap=1 WHERE id=1`)
	first := mkTask(t, db, "task", "packaged")
	second := mkTask(t, db, "task", "packaged")
	if err := birthPacket(t, db, first, "original.md"); err != nil {
		t.Fatal(err)
	}
	original := taskInt(t, db, first, "current_review_id")
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.RequestCorrection(ctx, q, first, "The result requires another check.")
	})
	if taskInt(t, db, first, "review_slot_held") != 1 || taskInt(t, db, first, "current_review_id") != original {
		t.Fatal("The correction lost its review or capacity reservation.")
	}
	wantCode(t, db, domain.CodeWIPCap, func(ctx context.Context, q domain.Q) error {
		return publishReviewFixture(ctx, q, second, "second.md", "The second result satisfies its requirements.")
	})
	version := taskInt(t, db, first, "record_version")
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.RequestRequirementsRevision(ctx, q, first, version, "replace-review-criteria", "The revised result includes the missing case.", "The operator revised the criteria.", "operator")
		return err
	})
	if taskInt(t, db, first, "review_slot_held") != 0 || taskInt(t, db, first, "current_review_id") != original {
		t.Fatal("The requirements revision lost history or retained capacity.")
	}
	if err := birthPacket(t, db, second, "second.md"); err != nil {
		t.Fatalf("The second review could not use the released capacity: %v", err)
	}
	if oneStr(t, db, `SELECT render_path FROM review_versions WHERE id=?`, original) != "original.md" {
		t.Fatal("The requirements revision changed the original report.")
	}
	if taskInt(t, db, first, "requirements_version") != 2 || taskInt(t, db, second, "review_slot_held") != 1 {
		t.Fatal("The current requirements and review capacity disagree.")
	}
}
