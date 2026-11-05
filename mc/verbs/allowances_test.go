package verbs

import (
	"context"
	"strings"
	"testing"

	"mc/dispatch"
	"mc/domain"
)

func TestVerifierAllowanceIsIndependentAndRetainsReport(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Correct the required result")
	for attempt := 0; attempt <= 3; attempt++ {
		f.advance(task, "answer.txt", "a rejected answer\n", "worked")
		submitVerdict(t, f, task, "correct")
		if got := dfInt(t, f.db, `SELECT correction_count FROM tasks WHERE id=?`, task); got != int64(attempt) {
			t.Fatalf("count=%d, want %d", got, attempt)
		}
	}
	if got := dfStr(t, f.db, `SELECT status||'/'||allowance_condition FROM tasks WHERE id=?`, task); got != "worked/verifier" {
		t.Fatal(got)
	}
	report := dfStr(t, f.db, `SELECT correction_reason FROM tasks WHERE id=?`, task)
	if err := inTx(f.db, func(ctx context.Context, q Q) error { return domain.Unblock(ctx, q, task) }); err == nil {
		t.Fatal("Ordinary resolution bypassed the allowance.")
	}
	// An independent decision survives the grant.
	f.transaction(func(ctx context.Context, q Q) error {
		return domain.Block(ctx, q, task, domain.BlockOperator, "Select the required behavior.")
	})
	version := int64(dfInt(t, f.db, `SELECT record_version FROM tasks WHERE id=?`, task))
	reason := "The evidence supports another correction."
	request := BoardWriteRequest{OperationID: "grant-verifier", Action: "allow-corrections", TaskID: task, ExpectedVersion: version, Reason: &reason}
	if _, err := BoardWrite(f.db, nil, request); err != nil {
		t.Fatal(err)
	}
	if _, err := BoardWrite(f.db, nil, request); err != nil {
		t.Fatal("The duplicate grant lost its receipt:", err)
	}
	if got := dfStr(t, f.db, `SELECT status||'/'||correction_count||'/'||blocked FROM tasks WHERE id=?`, task); got != "seeded/0/1" {
		t.Fatal(got)
	}
	if got := dfStr(t, f.db, `SELECT correction_reason FROM tasks WHERE id=?`, task); got != report {
		t.Fatal("The grant lost the report.")
	}
	request.OperationID = "stale-grant"
	if _, err := BoardWrite(f.db, nil, request); err == nil {
		t.Fatal("A stale grant succeeded.")
	}
	f.transaction(func(ctx context.Context, q Q) error { return domain.Unblock(ctx, q, task) })
	f.advance(task, "answer.txt", "another rejected answer\n", "worked")
	submitVerdict(t, f, task, "correct")
	if got := dfInt(t, f.db, `SELECT correction_count FROM tasks WHERE id=?`, task); got != 1 {
		t.Fatalf("The rejection after a grant counted %d", got)
	}
	f.advance(task, "answer.txt", "the corrected answer\n", "worked")
	submitVerdict(t, f, task, "pass")
	if got := dfStr(t, f.db, `SELECT correction_count||'/'||verifier_correction_open FROM tasks WHERE id=?`, task); got != "0/0" {
		t.Fatal(got)
	}
}

func TestMergeRepairRetainsEvidenceAndProjectPause(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Preserve both contributions")
	f.pass(task, "answer.txt", "worker 1\n")
	f.commitMain("answer.txt", "main 1\n")
	if first := f.merge(task, "prepare"); first.Status != "conflict" {
		t.Fatal(first)
	}
	f.pass(task, "answer.txt", "worker 2\n")
	f.commitMain("answer.txt", "main 2\n")
	dvExec(t, f.db, `UPDATE projects SET status='paused' WHERE id='ws-test'`)
	second := f.merge(task, "prepare")
	if second.Status != "conflict" {
		t.Fatal(second)
	}
	if got := dfStr(t, f.db, `SELECT status||'/'||blocked||'/'||correction_count||'/'||COALESCE(allowance_condition,'') FROM tasks WHERE id=?`, task); got != "seeded/0/0/" {
		t.Fatal(got)
	}
	if got := dfStr(t, f.db, `SELECT status FROM projects WHERE id='ws-test'`); got != "paused" {
		t.Fatal(got)
	}
	evidence := dfStr(t, f.db, `SELECT correction_reason FROM tasks WHERE id=?`, task)
	if !strings.Contains(evidence, second.DestinationCommit) || !strings.Contains(evidence, second.SubmittedCommit) || !strings.Contains(evidence, "answer.txt") {
		t.Fatal("The merge repair lost the latest conflict evidence.")
	}
	brief := briefFor(t, f.db, dispatch.RoleWorker, briefSubject(dispatch.RoleWorker, task), briefInputs{})
	if brief.part(partMergeCandidate) == nil || brief.part("Operator allowance explanation") != nil {
		t.Fatal("The Worker brief lost the conflict or received an operator explanation.")
	}
	reason := "Authorize another merge repair."
	request := BoardWriteRequest{OperationID: "grant-merge", Action: "allow-merge-repair", TaskID: task, ExpectedVersion: int64(dfInt(t, f.db, `SELECT record_version FROM tasks WHERE id=?`, task)), Reason: &reason}
	if _, err := BoardWrite(f.db, nil, request); err == nil {
		t.Fatal("The board accepted a merge repair authorization.")
	}
}

func TestMergeConflictAgainstSameDestinationStartsAnotherRepair(t *testing.T) {
	f := newDispatchLifecycle(t)
	task := f.add("Reconcile both contributions")
	first := f.candidate(task, func(run lifecycleRun) {
		f.submit(run, f.commit(run.root, "answer.txt", "worker one\n"))
		f.commitMain("answer.txt", "main one\n")
	})
	if first.Status != "conflict" {
		t.Fatal(first)
	}
	second := f.candidate(task, func(run lifecycleRun) { f.submit(run, f.commit(run.root, "answer.txt", "worker two\n")) })
	if second.Status != "conflict" || second.DestinationCommit != first.DestinationCommit {
		t.Fatal(second)
	}
	if got := dfStr(t, f.db, `SELECT status||'/'||blocked||'/'||correction_count FROM tasks WHERE id=?`, task); got != "seeded/0/0" {
		t.Fatal(got)
	}
	f.requireWorker(task)
}

func TestConflictAfterApprovalStartsAMergeRepair(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Preserve the approved result")
	f.pass(task, "answer.txt", "worker\n")
	f.commitMain("answer.txt", "main\n")
	f.merge(task, "prepare")
	f.pass(task, "answer.txt", "the combined answer\n")
	clean := f.merge(task, "prepare")
	if clean.Status != "merged" {
		t.Fatal(clean)
	}
	f.approve(task)
	f.commitMain("answer.txt", "a new main answer\n")
	conflict := f.merge(task, "prepare")
	if conflict.Status != "conflict" {
		t.Fatal(conflict)
	}
	if got := dfStr(t, f.db, `SELECT status||'/'||COALESCE(decision,'')||'/'||blocked||'/'||correction_open FROM tasks WHERE id=?`, task); got != "seeded//0/1" {
		t.Fatal(got)
	}
}
