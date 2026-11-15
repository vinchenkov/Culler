package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

func TestPacketListMergeStates(t *testing.T) {
	block := func(t *testing.T, db *sql.DB, task int64, reason string) {
		dvExec(t, db, `UPDATE tasks SET blocked=1,blocked_kind='operator',blocked_reason=? WHERE id=?`, reason, task)
	}
	approved := func(t *testing.T, db *sql.DB, ids ...int64) {
		for _, id := range ids {
			stMergeable(t, db, id)
		}
		for _, id := range ids {
			stApprove(t, db, id)
		}
	}
	failedMerge := func(t *testing.T, db *sql.DB, task int64) {
		resp := startRequest(t, db, false)
		stWantStarted(t, db, resp, fmt.Sprintf("apply/%d", task))
		stFail(t, db, resp.Started[0], false)
	}
	for _, test := range []struct {
		name    string
		setup   func(*testing.T, *sql.DB, *testClocks)
		state   string
		text    string
		related any
		task    int64 // the checked task; 0 checks task 1
	}{
		{"merge", func(t *testing.T, db *sql.DB, _ *testClocks) { approved(t, db, 1) },
			"merge", mergeTextMerge, nil, 0},
		{"merge that runs", func(t *testing.T, db *sql.DB, _ *testClocks) {
			approved(t, db, 1)
			stWantStarted(t, db, startRequest(t, db, false), "apply/1")
		}, "merge", mergeTextMerge, nil, 0},
		{"new candidate", func(t *testing.T, db *sql.DB, c *testClocks) {
			approved(t, db, 1)
			dvExec(t, db, `UPDATE tasks SET merge_after=? WHERE id=1`, c.now.Add(time.Minute).UTC().Format(spineTime))
		}, "new-candidate", "The culler branch changed after the review. Culler prepares a new merge candidate.", nil, 0},
		{"after merge step", func(t *testing.T, db *sql.DB, _ *testClocks) {
			stMergeable(t, db, 1)
			stTask(t, db, 2, "ws-test", dispatch.StatusPackaged)
			dvExec(t, db, `UPDATE tasks SET candidate_id=NULL WHERE id=2`)
			stWantStarted(t, db, startRequest(t, db, false), "prepare/2")
			stApprove(t, db, 1)
		}, "after-merge-step", mergeTextAfterMergeStep, int64(2), 0},
		{"after task", func(t *testing.T, db *sql.DB, _ *testClocks) {
			// Approved tasks merge in task id order, so task 1 holds the merge of task 5.
			approved(t, db, 5, 1)
		}, "after-task", "Culler merges task #1 first.", int64(1), 5},
		{"after task that needs attention", func(t *testing.T, db *sql.DB, _ *testClocks) {
			stMergeable(t, db, 1)
			approved(t, db, 2)
			resp := startRequest(t, db, false)
			stWantStarted(t, db, resp, "apply/2")
			if _, err := RepositoryRecord(db, nil, "apply", resp.Started[0], RepositoryResult{TaskID: 2, Status: "operator-attention",
				OperatorAction: "The operator checkout has uncommitted changes."}); err != nil {
				t.Fatal(err)
			}
			stApprove(t, db, 1)
		}, "after-task", "Culler merges task #2 first. Task #2 needs your attention.", int64(2), 0},
		{"a corrected packet that awaits a decision", func(t *testing.T, db *sql.DB, _ *testClocks) {
			// A correction cleared the approval of task 1, and its corrected packet awaits a decision.
			approved(t, db, 1)
			correctedPacket(t, db, 1)
			approved(t, db, 5)
		}, "merge", mergeTextMerge, nil, 5},
		{"an open correction of another task", func(t *testing.T, db *sql.DB, _ *testClocks) {
			approved(t, db, 1)
			stTask(t, db, 2, "ws-test", dispatch.StatusSeeded)
			dvExec(t, db, `UPDATE tasks SET correction_open=1 WHERE id=2`)
		}, "merge", mergeTextMerge, nil, 0},
		{"paused", func(t *testing.T, db *sql.DB, _ *testClocks) {
			approved(t, db, 1)
			dvExec(t, db, `UPDATE projects SET status='paused' WHERE id='ws-test'`)
		}, "paused", mergeTextPaused, nil, 0},
		{"attention", func(t *testing.T, db *sql.DB, _ *testClocks) {
			approved(t, db, 1)
			for id := int64(2); id <= 4; id++ {
				stTask(t, db, id, "ws-test", dispatch.StatusSeeded)
				block(t, db, id, "The task needs a decision.")
			}
		}, "attention", mergeTextAttention, nil, 0},
		{"attention above the threshold", func(t *testing.T, db *sql.DB, _ *testClocks) {
			approved(t, db, 1)
			for id := int64(2); id <= 5; id++ {
				stTask(t, db, id, "ws-test", dispatch.StatusSeeded)
				block(t, db, id, "The task needs a decision.")
			}
		}, "attention", "At least three tasks need your attention. Culler merges this change when less than three tasks need your attention.", nil, 0},
		{"vision", func(t *testing.T, db *sql.DB, _ *testClocks) {
			approved(t, db, 1)
			dvExec(t, db, `INSERT INTO vision_checks(project,ready,checked_at) VALUES('ws-test',0,datetime('now'))`)
		}, "vision", mergeTextVision, nil, 0},
		{"recovery", func(t *testing.T, db *sql.DB, _ *testClocks) {
			approved(t, db, 1)
			failedMerge(t, db, 1)
		}, "recovery", mergeTextRecovery, nil, 0},
		{"recovery of another task", func(t *testing.T, db *sql.DB, _ *testClocks) {
			stMergeable(t, db, 1)
			approved(t, db, 2)
			failedMerge(t, db, 2)
			stApprove(t, db, 1)
		}, "recovery", mergeTextRecovery, int64(2), 0},
		{"retry", func(t *testing.T, db *sql.DB, _ *testClocks) {
			// The fixture candidate has no exact commits, so the merge start fails.
			task := dvTask(1, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
			task.Decision, task.DecidedAt = dispatch.DecisionApproved, &dvOld
			dvInsertTask(t, db, task)
			if resp := startRequest(t, db, false); len(resp.Failed) != 1 {
				t.Fatalf("the merge start did not fail: %+v", resp)
			}
		}, "retry", mergeTextRetry, nil, 0},
		{"blocked", func(t *testing.T, db *sql.DB, _ *testClocks) {
			approved(t, db, 1)
			block(t, db, 1, "The operator must repair the checkout.")
		}, "blocked", "The operator must repair the checkout.", nil, 0},
		{"stalled", func(t *testing.T, db *sql.DB, _ *testClocks) {
			approved(t, db, 1)
			dvExec(t, db, `UPDATE tasks SET dispatch_retries=0 WHERE id=1`)
		}, "blocked", mergeTextStall, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := dvSpine(t)
			clocks := fixClocks(t, db)
			test.setup(t, db, clocks)
			task := test.task
			if task == 0 {
				task = 1
			}
			state, text, related := stMergeState(t, db, task)
			if state != test.state || text != test.text || related != test.related {
				t.Fatalf("the merge state is %s (%v): %s\nwant %s (%v): %s", state, related, text, test.state, test.related, test.text)
			}
		})
	}
}

func TestPacketListMergeStateOnlyForApprovedPackets(t *testing.T) {
	db := dvSpine(t)
	stMergeable(t, db, 1)
	dvPublishPacket(t, db, 1, "", dvOld)
	result, err := PacketList(db, PacketSelection{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	packets := result.(map[string]any)["packets"].([]map[string]any)
	if len(packets) != 1 || packets[0]["review_state"] != "awaits_decision" {
		t.Fatalf("the packets are %v", packets)
	}
	for _, key := range []string{"merge_state", "merge_state_text", "merge_task_id"} {
		if value, ok := packets[0][key]; !ok || value != nil {
			t.Fatalf("the packet that awaits a decision has %s %v", key, value)
		}
	}
}

// correctedPacket records a correction round of an approved task: the
// correction clears the approval, and the corrected version receives a
// merged candidate and a packet that awaits an operator decision.
func correctedPacket(t *testing.T, db *sql.DB, task int64) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := domain.RequestCorrection(context.Background(), tx, task, "The merge candidate has a conflict."); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `UPDATE tasks SET correction_open=0 WHERE id=?`, task)
	dvCompleteDeliveryRound(t, db, task, fmt.Sprintf("corrected-%d", task), strings.Repeat("e", 40))
	for _, status := range []dispatch.Status{dispatch.StatusWorked, dispatch.StatusVerified, dispatch.StatusPackaged} {
		dvExec(t, db, `UPDATE tasks SET status=? WHERE id=?`, string(status), task)
	}
	dvPublishPacket(t, db, task, "", dvOld)
}
