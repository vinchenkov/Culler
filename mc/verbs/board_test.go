package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mc/repository"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

func boardText(value string) *string { return &value }
func boardVersion(t *testing.T, db *sql.DB, task int64) int64 {
	t.Helper()
	return dfInt(t, db, `SELECT record_version FROM tasks WHERE id=?`, task)
}
func boardRequest(t *testing.T, db *sql.DB, request BoardWriteRequest) map[string]any {
	t.Helper()
	value, err := BoardWrite(db, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)
}
func boardRevision(t *testing.T, db *sql.DB, task int64, requirements string) map[string]any {
	t.Helper()
	return boardRequest(t, db, BoardWriteRequest{OperationID: fmt.Sprintf("revise-%d", task), Action: "revise", TaskID: task,
		ExpectedVersion: boardVersion(t, db, task), Description: boardText(requirements), Reason: boardText("The operator clarified the required result.")})
}
func requireBoardCode(t *testing.T, err error, code string) {
	t.Helper()
	if typed, ok := err.(*domain.DomainError); !ok || typed.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestBoardCreationReceiptAndClosedFields(t *testing.T) {
	db := dvSpine(t)
	request := BoardWriteRequest{OperationID: "create-task", Action: "create", Scope: "task", Project: "ws-test", Title: boardText("Create the report")}
	first := boardRequest(t, db, request)
	task := first["task_id"].(int64)
	if got := dfStr(t, db, `SELECT t.status||':'||t.origin||':'||t.destination_branch FROM tasks t WHERE t.id=?`, task); got != "seeded:user:culler" {
		t.Fatal(got)
	}
	second := boardRequest(t, db, request)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) || dfInt(t, db, `SELECT count(*) FROM tasks`) != 1 {
		t.Fatalf("receipt changed: %s / %s", a, b)
	}
	request.Title = boardText("Different work")
	_, err := BoardWrite(db, nil, request)
	requireBoardCode(t, err, domain.CodeOperationConflict)
	for _, body := range []string{
		`{"operation_id":"x","action":"create","scope":"task","project":"ws-test","title":"Work","status":"seeded"}`,
		`{"operation_id":"x","action":"edit","task_id":1,"expected_version":1,"project":"","title":"Work"}`,
		`{"operation_id":"x","action":"revise","task_id":1,"expected_version":1,"priority":2,"reason":"A reason"}`,
		`{"operation_id":"x","action":"block","task_id":1,"expected_version":1,"reason":"   "}`,
	} {
		if _, err := DecodeBoardWriteRequest([]byte(body)); err == nil {
			t.Fatalf("accepted forbidden request %s", body)
		}
	}

}

func TestBoardMetadataVersionAndFinalDecisions(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	id := fhClaim(t, db, 1, "metadata-worker", "worker")
	version := boardVersion(t, db, 1)
	priority := -1
	boardRequest(t, db, BoardWriteRequest{OperationID: "edit-metadata", Action: "edit", TaskID: 1, ExpectedVersion: version, Title: boardText("Revised title"), Priority: &priority})
	if dfStr(t, db, `SELECT status FROM tasks WHERE id=1`) != "seeded" || dfStr(t, db, `SELECT run_id FROM leases WHERE subject=1`) != id.RunID {
		t.Fatal("metadata edit changed execution")
	}
	_, err := BoardWrite(db, nil, BoardWriteRequest{OperationID: "stale-edit", Action: "edit", TaskID: 1, ExpectedVersion: version, Title: boardText("Stale title")})
	requireBoardCode(t, err, domain.CodeRecordConflict)
	boardRequest(t, db, BoardWriteRequest{OperationID: "cancel-task", Action: "cancel", TaskID: 1, ExpectedVersion: boardVersion(t, db, 1), Reason: boardText("The operator cancelled the task.")})
	_, err = BoardWrite(db, nil, BoardWriteRequest{OperationID: "edit-final", Action: "edit", TaskID: 1, ExpectedVersion: boardVersion(t, db, 1), Title: boardText("After cancellation")})
	requireBoardCode(t, err, domain.CodeArchived)
	if dfStr(t, db, `SELECT run_id FROM leases WHERE subject=1`) != id.RunID {
		t.Fatal("cancellation released the active lease")
	}
}

func TestBoardCreationReceiptPreservesLargeIdentifiers(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1<<53, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	request := BoardWriteRequest{OperationID: "create-after-large-identifier", Action: "create", Scope: "task", Project: "ws-test", Title: boardText("Create the report")}
	first := boardRequest(t, db, request)
	second := boardRequest(t, db, request)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if first["task_id"].(int64) != (1<<53)+1 || string(a) != string(b) {
		t.Fatalf("the repeated receipt changed its identifier: %s / %s", a, b)
	}
	if dfInt(t, db, `SELECT count(*) FROM tasks`) != 2 {
		t.Fatal("the repeated request created another task")
	}
	task := first["task_id"].(int64)
	edit := BoardWriteRequest{OperationID: "edit-large-identifier", Action: "edit", TaskID: task, ExpectedVersion: boardVersion(t, db, task), Title: boardText("The updated title")}
	boardRequest(t, db, edit)
	value, err := TaskGet(db, task)
	if err != nil {
		t.Fatal(err)
	}
	history := value.(map[string]any)["board_history"].([]map[string]any)
	for _, event := range history {
		outcome, err := json.Marshal(event["outcome"])
		if err != nil || !strings.Contains(string(outcome), `"task_id":9007199254740993`) {
			t.Fatalf("the history changed its result identifier: %s, %v", outcome, err)
		}
		if event["action"] == "edit" {
			requested, err := json.Marshal(event["requested"])
			if err != nil || !strings.Contains(string(requested), `"task_id":9007199254740993`) {
				t.Fatalf("the history changed its request identifier: %s, %v", requested, err)
			}
		}
	}
}

func TestBoardTaskRevisionStageMatrixAndHistory(t *testing.T) {
	for _, stage := range []dispatch.Status{dispatch.StatusSeeded, dispatch.StatusWorked, dispatch.StatusVerified, dispatch.StatusPackaged} {
		t.Run(string(stage), func(t *testing.T) {
			var db *sql.DB
			var task int64 = 1
			evidence := "original-completion:"
			if stage == dispatch.StatusPackaged {
				f := newDeliveryFixture(t)
				db = f.db
				task = f.add("Deliver the original requirements")
				commit := f.advance(task, "feature.txt", "The original result.\n", "packaged")
				f.merge(task, "prepare")
				evidence = commit + ":" + commit
			} else {
				db = dvSpine(t)
				dvInsertTask(t, db, dvTask(task, dispatch.ScopeTask, stage, 2))
				if stage == dispatch.StatusVerified {
					dvCompleteDelivery(t, db, task)
					evidence += dfStr(t, db, `SELECT result_commit FROM task_versions WHERE id=(SELECT source_version FROM tasks WHERE id=?)`, task)
				}
				dvExec(t, db, `UPDATE tasks SET completion_sha='original-completion' WHERE id=?`, task)
			}
			dvExec(t, db, `UPDATE tasks SET description='Original requirements',correction_count=3,dispatch_retries=0,blocked=1,blocked_kind='operator',blocked_reason='An independent operator decision.' WHERE id=?`, task)
			result := boardRevision(t, db, task, "Revised requirements")
			want := string(dispatch.StatusSeeded)
			if stage == dispatch.StatusProposed {
				want = string(stage)
			}
			if dfStr(t, db, `SELECT status FROM tasks WHERE id=?`, task) != want || dfStr(t, db, `SELECT description FROM tasks WHERE id=?`, task) != "Revised requirements" || result["revision_state"] != "applied" {
				t.Fatal(result)
			}
			if dfInt(t, db, `SELECT correction_count FROM tasks WHERE id=?`, task) != 0 || dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=?`, task) != 0 || dfInt(t, db, `SELECT blocked FROM tasks WHERE id=?`, task) != 1 {
				t.Fatal("revision changed an independent block or infrastructure allowance")
			}
			if dfInt(t, db, `SELECT completion_sha IS NULL AND source_version IS NULL FROM tasks WHERE id=?`, task) != 1 {
				t.Fatal("old evidence remains current")
			}
			if dfStr(t, db, `SELECT completion_sha||':'||COALESCE(verified_sha,'') FROM requirements_evidence WHERE task_id=?`, task) != evidence {
				t.Fatal("old evidence disappeared")
			}
			if stage == dispatch.StatusPackaged {
				if dfInt(t, db, `SELECT (1-t.review_slot_held) FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id=?`, task) != 1 || dfInt(t, db, `SELECT count(*) FROM review_versions p WHERE EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.task_id=p.task_id AND r.previous_requirements_version=p.requirements_version) AND task_id=?`, task) != 1 {
					t.Fatal("the packet was not preserved as superseded")
				}
				if dfInt(t, db, `SELECT submitted_version IS NULL AND candidate_id IS NULL FROM tasks WHERE id=?`, task) != 1 {
					t.Fatal("the delivery kept the old version")
				}
				live, err := PacketList(db, PacketSelection{Live: true})
				if err != nil {
					t.Fatal(err)
				}
				if len(live.(map[string]any)["packets"].([]map[string]any)) != 0 {
					t.Fatal("superseded packet occupies the queue")
				}
			}
		})
	}
}

func TestBoardRevisionWaitsForActiveWorkerAndRecovery(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			db := dvSpine(t)
			dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
			dvExec(t, db, `UPDATE tasks SET description='Original requirements' WHERE id=1`)
			id := fhClaim(t, db, 1, "revision-worker", "worker")
			result := boardRevision(t, db, 1, "Revised requirements")
			if result["revision_state"] != "pending" || dfStr(t, db, `SELECT description FROM tasks WHERE id=1`) != "Original requirements" {
				t.Fatal(result)
			}
			if dfStr(t, db, `SELECT requirements FROM run_requirements WHERE run_id=?`, id.RunID) != "Original requirements" {
				t.Fatal("run requirements changed")
			}
			if failure {
				fhReport(t, db, id, domain.InfrastructureFailureReport{Condition: "The service failed.", Source: "agent"})
				fhReap(t, db, id.RunID)
			} else {
				if err := inTx(db, func(ctx context.Context, q Q) error {
					if err := domain.AdvanceStage(ctx, q, 1, "worked"); err != nil {
						return err
					}
					if err := endRun(ctx, q, id.RunID, "completed"); err != nil {
						return err
					}
					return releaseLease(ctx, q, id.RunID)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := inTx(db, domain.ApplyPendingTaskRevisions); err != nil {
				t.Fatal(err)
			}
			if dfStr(t, db, `SELECT status||':'||description FROM tasks WHERE id=1`) != "seeded:Revised requirements" {
				t.Fatal("revision did not use post-run state")
			}
			if failure && dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id=1`) != 2 {
				t.Fatal("revision restored the infrastructure allowance")
			}
		})
	}
}

func TestBoardLatePacketStaysOutsideQueueAndCannotApprove(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Deliver the original criteria")
	f.advance(task, "feature.txt", "The original result.\n", "verified")
	run, root, id := f.run(task, "packager")
	boardRevision(t, f.db, task, "New criteria")
	report := f.artifact(run, "The previous requirements passed.")
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "packaged", Outputs: report, Thesis: "The previous requirements passed."}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	f.merge(task, "prepare")
	var packet, version int64
	if err := f.db.QueryRow(`SELECT id,version_id FROM review_versions WHERE task_id=?`, task).Scan(&packet, &version); err != nil {
		t.Fatal(err)
	}
	_, err := PacketDecide(f.db, nil, task, "approve", "", packet, version)
	requireBoardCode(t, err, domain.CodeRevisionPending)
	if dfInt(t, f.db, `SELECT (1-t.review_slot_held) FROM tasks t JOIN review_versions p ON p.id=t.current_review_id WHERE task_id=?`, task) != 1 || dfInt(t, f.db, `SELECT count(*) FROM review_versions p WHERE EXISTS(SELECT 1 FROM requirements_revisions r WHERE r.task_id=p.task_id AND r.previous_requirements_version=p.requirements_version) AND task_id=?`, task) != 1 {
		t.Fatal("late packet did not remain historical")
	}
	if err := inTx(f.db, domain.ApplyPendingTaskRevisions); err != nil {
		t.Fatal(err)
	}
	if dfStr(t, f.db, `SELECT status FROM tasks WHERE id=?`, task) != "seeded" {
		t.Fatal("late package escaped the revised execution round")
	}
}

func TestBoardDeliveryRevisionWaitsForCaptureAndPreservesUsefulOutput(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Preserve the useful result")
	run, root, id := f.run(task, "worker")
	repo := filepath.Join(root, "repo")
	writeFile(t, filepath.Join(repo, "useful.txt"), "The prior run produced useful output.\n")
	srcGit(t, repo, "add", ".")
	srcGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Preserve the useful result")
	commit := srcGit(t, repo, "rev-parse", "HEAD")
	revision := boardRevision(t, f.db, task, "The revised task preserves the useful output and adds the required evidence.")
	if revision["revision_state"] != "pending" {
		t.Fatal(revision)
	}
	if _, err := Complete(f.db, id, CompleteArgs{Task: task, Run: run, Status: "worked", Commit: commit}); err != nil {
		t.Fatal(err)
	}
	f.transaction(domain.ApplyPendingTaskRevisions)
	if dfStr(t, f.db, `SELECT state FROM requirements_revisions WHERE task_id=?`, task) != "pending" {
		t.Fatal("revision passed uncaptured output")
	}
	var records dispatch.Records
	f.transaction(func(ctx context.Context, q Q) error {
		var err error
		records, err = loadVisionRecords(ctx, q, "ws-test")
		return err
	})
	action := dispatch.Decide(records, dispatch.Lease{}, dispatch.DefaultConfig(), dispatch.Clock{Now: time.Now()})
	if action.Kind != dispatch.KindIdle || action.Idle != dispatch.IdleRepositoryOperation {
		t.Fatalf("required capture lost precedence: %+v", action)
	}
	wantStart(t, records, time.Now(), dispatch.OperationCapture, task)
	f.captureSubmission(task, run)
	f.transaction(domain.ApplyPendingTaskRevisions)
	if dfStr(t, f.db, `SELECT state FROM requirements_revisions WHERE task_id=?`, task) != "applied" || dfStr(t, f.db, `SELECT status FROM tasks WHERE id=?`, task) != "seeded" {
		t.Fatal("the captured old result bypassed the revision")
	}
	if dfInt(t, f.db, `SELECT submitted_version IS NULL FROM tasks WHERE id=?`, task) != 1 {
		t.Fatal("the old submission remained current")
	}
	if dfStr(t, f.db, `SELECT status FROM requirements_evidence WHERE task_id=?`, task) != "worked" {
		t.Fatal("the previous result was not preserved")
	}
	f.cleanup(task, run, root)
	f.transaction(func(ctx context.Context, q Q) error {
		var err error
		records, err = loadVisionRecords(ctx, q, "ws-test")
		return err
	})
	action = dispatch.Decide(records, dispatch.Lease{}, dispatch.DefaultConfig(), dispatch.Clock{Now: time.Now()})
	if action.Spawn == nil || action.Spawn.Role != dispatch.RoleWorker {
		t.Fatalf("the revision did not select the Worker: %+v", action)
	}
	next, nextRoot, _ := f.run(task, "worker")
	if dfInt(t, f.db, `SELECT requirements_version FROM run_requirements WHERE run_id=?`, next) != 2 {
		t.Fatal("the next run received old requirements")
	}
	if got, err := (repository.Git{Root: filepath.Join(nextRoot, "repo")}).Commit(context.Background(), "HEAD"); err != nil || got != commit {
		t.Fatalf("the useful output disappeared: %s %v", got, err)
	}
}

func TestBoardFinalDecisionAndRevisionOrdering(t *testing.T) {
	for _, first := range []string{"approve", "revise"} {
		t.Run(first, func(t *testing.T) {
			f := newDeliveryFixture(t)
			db := f.db
			task := f.add("Deliver the reviewed result")
			f.advance(task, "feature.txt", "The result is ready.\n", "packaged")
			f.merge(task, "prepare")
			var packet, submitted int64
			if err := db.QueryRow(`SELECT id,version_id FROM review_versions WHERE task_id=?`, task).Scan(&packet, &submitted); err != nil {
				t.Fatal(err)
			}
			version := boardVersion(t, db, task)
			if first == "approve" {
				if _, err := PacketDecide(db, nil, task, "approve", "", packet, submitted); err != nil {
					t.Fatal(err)
				}
				_, err := BoardWrite(db, nil, BoardWriteRequest{OperationID: "late-revision", Action: "revise", TaskID: task, ExpectedVersion: version, Description: boardText("New criteria"), Reason: boardText("The requirements changed.")})
				requireBoardCode(t, err, domain.CodeRecordConflict)
				if dfInt(t, db, `SELECT count(*) FROM requirements_revisions`) != 0 {
					t.Fatal("a final decision accepted a later revision")
				}
			} else {
				boardRevision(t, db, task, "New criteria")
				if _, err := PacketDecide(db, nil, task, "approve", "", packet, submitted); err == nil {
					t.Fatal("old packet approved after revision")
				}
			}
		})
	}
}

func TestBoardActiveVerifierResultPreservesOriginalRequirements(t *testing.T) {
	f := newDeliveryFixture(t)
	db := f.db
	task := f.add("Deliver the original criteria")
	dvExec(t, db, `UPDATE tasks SET description='Original criteria' WHERE id=?`, task)
	commit := f.advance(task, "feature.txt", "The original result.\n", "worked")
	run, root, id := f.run(task, "verifier")
	boardRevision(t, db, task, "The new criteria require another Worker pass.")
	evidence := f.artifact(run, "The original criteria pass.")
	if _, err := VerifierVerdict(db, id, VerdictArgs{Task: task, Run: run, Outcome: "pass", SHA: commit, Evidence: evidence, StoreWorktree: filepath.Join(root, "repo")}); err != nil {
		t.Fatal(err)
	}
	f.cleanup(task, run, root)
	if err := inTx(db, domain.ApplyPendingTaskRevisions); err != nil {
		t.Fatal(err)
	}
	if dfStr(t, db, `SELECT verified_sha FROM requirements_evidence WHERE task_id=?`, task) != commit || dfStr(t, db, `SELECT COALESCE(evidence_path,'') FROM runs WHERE id=?`, run) == "" || dfStr(t, db, `SELECT requirements FROM run_requirements WHERE run_id=?`, run) != "Original criteria" {
		t.Fatal("the old verifier result lost its requirements or evidence")
	}
	if dfStr(t, db, `SELECT status FROM tasks WHERE id=?`, task) != "seeded" {
		t.Fatal("the old verdict authorized the new criteria")
	}
}

func TestBoardOldNeedsOperatorReportCannotReplaceRevision(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	id := fhClaim(t, db, 1, "old-worker-report", "worker")
	boardRevision(t, db, 1, "The operator resolved the original requirements.")
	value, err := Complete(db, id, CompleteArgs{Task: 1, Run: id.RunID, NeedsOperator: true, Reason: "The original requirements conflict."})
	if err != nil {
		t.Fatal(err)
	}
	if value.(map[string]any)["superseded"] != true || dfInt(t, db, `SELECT blocked FROM tasks WHERE id=1`) != 0 {
		t.Fatal("the old decision report replaced the operator revision")
	}
	if dfStr(t, db, `SELECT run_id FROM leases WHERE project='ws-test'`) != id.RunID {
		t.Fatal("The report released its lease before the stop acknowledgment.")
	}
	if _, err := RunStopped(db, nil, id.RunID); err != nil {
		t.Fatal(err)
	}
	if err := inTx(db, domain.ApplyPendingTaskRevisions); err != nil {
		t.Fatal(err)
	}
	if dfStr(t, db, `SELECT state FROM requirements_revisions WHERE task_id=1`) != "applied" || dfInt(t, db, `SELECT count(*) FROM activity WHERE kind='task.superseded_block'`) != 1 {
		t.Fatal("the old report did not remain historical")
	}
}

func TestBoardRequirementsSchemaRejectsAnApplicationWithoutEvidence(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusVerified, 2))
	dvExec(t, db, `INSERT INTO requirements_revisions(operation_id,task_id,expected_version,requirements,previous_requirements,reason,actor)
		VALUES('raw-revision',1,1,'Changed requirements','','The criteria changed.','operator')`)
	if _, err := db.Exec(`UPDATE tasks SET description='Changed requirements',requirements_version=2,status='seeded' WHERE id=1`); err == nil {
		t.Fatal("the schema accepted requirements without previous evidence")
	}
	if dfStr(t, db, `SELECT status FROM tasks WHERE id=1`) != "verified" {
		t.Fatal("the rejected application changed the stage")
	}
}
