package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"mc/dispatch"
	"mc/domain"
)

// approvedDelivery builds an approved task whose update to main is pending.
func approvedDelivery(t *testing.T) (*deliveryFixture, int64) {
	t.Helper()
	f := newDeliveryFixture(t)
	task := f.add("Deliver the approved change")
	f.pass(task, "feature.txt", "complete\n")
	f.merge(task, "prepare")
	f.approve(task)
	return f, task
}

// An approved task stalls when its update exhausts the allowance. The unblock
// restores the allowance, keeps the approved record, and the update resumes.
func TestBoardApprovedUpdateResume(t *testing.T) {
	for _, tc := range []struct {
		name  string
		board bool
	}{
		{name: "direct stall recovery"},
		{name: "board stall recovery", board: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, task := approvedDelivery(t)
			dvExec(t, f.db, `UPDATE tasks SET dispatch_retries=0 WHERE id=?`, task)
			const content = `SELECT json_object('title',title,'description',description,'priority',priority,
				'status',status,'decision',decision,'decided_at',decided_at,'archived',archived,
				'verified_sha',(SELECT result_commit FROM task_versions WHERE id=tasks.source_version),'completion_sha',completion_sha,
				'requirements_version',requirements_version) FROM tasks WHERE id=?`
			approved := dfStr(t, f.db, content, task)
			version := boardVersion(t, f.db, task)
			const reason = "The operator authorized another update attempt."
			request := BoardWriteRequest{OperationID: "recover-approved-update", Action: "resume", TaskID: task,
				ExpectedVersion: version, Reason: boardText(reason)}
			if tc.board {
				first := boardRequest(t, f.db, request)
				second := boardRequest(t, f.db, request)
				a, _ := json.Marshal(first)
				b, _ := json.Marshal(second)
				if string(a) != string(b) || dfInt(t, f.db, `SELECT count(*) FROM board_operations WHERE task_id=?`, task) != 1 {
					t.Fatalf("the repeated recovery changed its receipt: %s / %s", a, b)
				}
				if dfStr(t, f.db, `SELECT actor||':'||reason FROM board_operations WHERE task_id=?`, task) != "operator:"+reason {
					t.Fatal("the recovery lost its actor or reason")
				}
				request.OperationID = "stale-recovery"
				_, err := BoardWrite(f.db, nil, request)
				requireBoardCode(t, err, domain.CodeRecordConflict)
			} else if _, err := TaskResume(f.db, nil, task, reason); err != nil {
				t.Fatal(err)
			}
			if got := dfStr(t, f.db, content, task); got != approved {
				t.Fatalf("the recovery changed the approved record: %s / %s", approved, got)
			}
			if dfInt(t, f.db, `SELECT blocked=0 AND blocked_reason IS NULL AND stalled=0 FROM tasks WHERE id=?`, task) != 1 {
				t.Fatal("the recovery left a block or a stall")
			}
			if dfInt(t, f.db, `SELECT dispatch_retries FROM tasks WHERE id=?`, task) != domain.InfraBudget {
				t.Fatal("the recovery did not restore the infrastructure allowance")
			}
			if dfInt(t, f.db, `SELECT count(*) FROM activity WHERE kind='task.resumed' AND subject=? AND actor='operator' AND detail=?`, task, reason) != 1 {
				t.Fatal("the recovery reason must appear once")
			}
			records, err := loadVisionRecords(context.Background(), f.db, "ws-test")
			if err != nil {
				t.Fatal(err)
			}
			wantStart(t, records, dvNow(t, f.db), dispatch.OperationApply, task)
			_, err = TaskUnblock(f.db, nil, task, reason)
			requireBoardCode(t, err, domain.CodeNotBlocked)
			for _, action := range []string{"edit", "revise", "block"} {
				request := BoardWriteRequest{OperationID: "approved-" + action, Action: action, TaskID: task,
					ExpectedVersion: boardVersion(t, f.db, task)}
				if action == "edit" {
					request.Title = boardText("A different title")
				} else {
					request.Reason = boardText("The operator requested a different result.")
					if action == "revise" {
						request.Description = boardText("Different requirements")
					}
				}
				_, err := BoardWrite(f.db, nil, request)
				requireBoardCode(t, err, domain.CodeAlreadyDecided)
			}
			boardRequest(t, f.db, BoardWriteRequest{OperationID: "cancel-approved", Action: "cancel", TaskID: task,
				ExpectedVersion: boardVersion(t, f.db, task), Reason: boardText("The operator withdrew the task before application.")})
			if dfStr(t, f.db, `SELECT decision||'/'||archived FROM tasks WHERE id=?`, task) != "cancelled/1" {
				t.Fatal("the approved task retained its merge obligation after cancellation")
			}
		})
	}
}

func TestBoardUnblockRefusesOtherFinalRecords(t *testing.T) {
	for _, decision := range []string{"approved", "rejected", "cancelled"} {
		t.Run(decision, func(t *testing.T) {
			var db *sql.DB
			var task int64
			if decision == "approved" {
				f, approved := approvedDelivery(t)
				f.merge(approved, "apply")
				db, task = f.db, approved
				if dfInt(t, db, `SELECT archived FROM tasks WHERE id=?`, task) != 1 {
					t.Fatal("the applied update left the approved task open")
				}
			} else {
				db, task = dvSpine(t), 1
				dvInsertTask(t, db, dvTask(task, dispatch.ScopeTask, dispatch.StatusProposed, 2))
				dvExec(t, db, `UPDATE tasks SET decision=?,decided_at=datetime('now') WHERE id=?`, decision, task)
			}
			dvExec(t, db, `UPDATE tasks SET blocked=1,blocked_kind='operator',blocked_reason='The original block.',dispatch_retries=0 WHERE id=?`, task)
			version := boardVersion(t, db, task)
			const reason = "The operator requested another attempt."
			_, err := TaskUnblock(db, nil, task, reason)
			requireBoardCode(t, err, domain.CodeAlreadyDecided)
			_, err = BoardWrite(db, nil, BoardWriteRequest{OperationID: "recover-final", Action: "unblock", TaskID: task,
				ExpectedVersion: version, Reason: boardText(reason)})
			requireBoardCode(t, err, domain.CodeAlreadyDecided)
			if boardVersion(t, db, task) != version || dfInt(t, db, `SELECT blocked=1 AND stalled=1 FROM tasks WHERE id=?`, task) != 1 {
				t.Fatal("the refused recovery changed the final record")
			}
			if dfInt(t, db, `SELECT count(*) FROM activity WHERE kind='task.unblock_reason'`) != 0 || dfInt(t, db, `SELECT count(*) FROM board_operations`) != 0 {
				t.Fatal("the refused recovery recorded an accepted action")
			}
		})
	}
}
