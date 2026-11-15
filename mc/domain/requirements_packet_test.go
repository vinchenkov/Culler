package domain_test

import (
	"context"
	"testing"

	"mc/domain"
)

func TestRequirementsPacketUsesCurrentRequirementsAndPreservesHistory(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "packaged")
	mkPacket(t, db, task)
	priorReview := oneInt(t, db, `SELECT current_review_id FROM tasks WHERE id=?`, task)
	priorCreated := oneStr(t, db, `SELECT created_at FROM review_versions WHERE id=?`, priorReview)
	version := taskInt(t, db, task, "record_version")
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.RequestRequirementsRevision(ctx, q, task, version, "packet-revision", "The revised requirements include the complete evidence.", "The operator requests a new result.", "operator")
		return err
	})
	for _, stage := range []string{"worked", "verified", "packaged"} {
		if stage == "packaged" {
			seedVerifiedVersion(t, db, task)
		}
		mustExec(t, db, `UPDATE tasks SET status=? WHERE id=?`, stage, task)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return publishReviewFixture(ctx, q, task, "current-report.md", "The current result satisfies the revised requirements.")
	})
	if oneInt(t, db, `SELECT (1-t.review_slot_held) FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id=?`, task) != 0 {
		t.Fatal("The current packet remains superseded.")
	}
	if oneInt(t, db, `SELECT current_review_id FROM tasks WHERE id=?`, task) == priorReview || oneInt(t, db, `SELECT p.requirements_version FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id=?`, task) != 2 {
		t.Fatal("the new packet retained its previous identity")
	}
	if oneStr(t, db, `SELECT created_at FROM review_versions p WHERE EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.task_id=p.task_id AND r.previous_requirements_version=p.requirements_version) AND task_id=?`, task) != priorCreated {
		t.Fatal("The previous packet lost its creation time.")
	}
}
