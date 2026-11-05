package dispatch

import (
	"reflect"
	"testing"
	"time"
)

func TestApprovalReservesTheProjectAndCorrectionStartsFirst(t *testing.T) {
	approved := tk(10, StatusPackaged, decided(DecisionApproved, at(-10)))
	approved.Delivery.CandidateID = 1
	approved.Delivery.CandidateMerged = true
	other := tk(1, StatusVerified)
	other.Priority = -1
	for _, tc := range []struct {
		name      string
		change    func(*Task)
		kind      Kind
		operation Operation
		role      Role
		idle      IdleReason
	}{
		{name: "approval", kind: KindIdle, idle: "priority-task-wait", operation: OperationApply},
		{name: "new destination", change: func(task *Task) {
			task.Delivery.CandidateID = 0
			task.Delivery.CandidateMerged = false
		}, kind: KindIdle, idle: IdleRepositoryOperation, operation: OperationPrepare},
		{name: "destination delay", change: func(task *Task) {
			task.Delivery.WaitUntil = pt(base.Add(time.Minute))
		}, kind: KindIdle, idle: "priority-task-wait"},
		{name: "correction", change: func(task *Task) {
			task.Status, task.Decision = StatusSeeded, DecisionNone
			task.Delivery = Delivery{CorrectionOpen: true}
		}, kind: KindSpawn, role: RoleWorker},
		{name: "infrastructure recovery", change: func(task *Task) {
			task.Stalled = true
		}, kind: KindIdle, idle: IdleAttentionRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := approved
			if tc.change != nil {
				tc.change(&task)
			}
			rec := recs([]Task{other, task}, []Packet{pk(10)})
			got := Decide(rec, freeLock(), DefaultConfig(), noonClock())
			if got.Kind != tc.kind || got.Idle != tc.idle {
				t.Fatalf("The priority task selected %+v", got)
			}
			starts := SelectStarts([]ProjectInput{{Records: rec}}, base, false)
			if tc.operation == "" && len(starts) != 0 {
				t.Fatalf("The Project started repository work: %+v", starts)
			}
			if tc.operation != "" && (len(starts) != 1 || starts[0].TaskID != task.ID || starts[0].Operation != tc.operation) {
				t.Fatalf("The Project selected unrelated repository work: %+v", starts)
			}
			if tc.kind == KindSpawn && (got.Spawn.Role != tc.role || got.Spawn.SubjectID == nil || *got.Spawn.SubjectID != task.ID) {
				t.Fatalf("The Project selected unrelated agent work: %+v", got)
			}
		})
	}
}

func TestCorrectionPriorityEndsAtSuccessfulChecksBeforeFirstApproval(t *testing.T) {
	corrected := tk(10, StatusPackaged)
	corrected.Delivery = Delivery{CandidateID: 1, CandidateMerged: true}
	other := tk(1, StatusSeeded)
	for _, decision := range []TaskDecision{DecisionNone, DecisionCancelled} {
		corrected.Decision = decision
		corrected.Archived = decision == DecisionCancelled
		rec := recs([]Task{corrected, other}, []Packet{pk(10)})
		got := Decide(rec, freeLock(), DefaultConfig(), noonClock())
		if got.Spawn == nil || got.Spawn.SubjectID == nil || *got.Spawn.SubjectID != other.ID {
			t.Fatalf("The Project retained a completed correction: %+v", got)
		}
	}
}

func TestCorrectionPriorityPreservesTheActiveRunAndPause(t *testing.T) {
	correction := tk(10, StatusSeeded)
	correction.Delivery.CorrectionOpen = true
	rec := recs([]Task{correction, tk(1, StatusSeeded)}, nil)
	cfg := DefaultConfig()
	lease := freshLock(pi64(1))
	if got := Decide(rec, lease, cfg, noonClock()); got.Idle != IdleLeaseHeld {
		t.Fatalf("The correction interrupted an active run: %+v", got)
	}
	rec.ProjectStatus = "paused"
	if got := Decide(rec, freeLock(), cfg, noonClock()); got.Idle != IdleProjectInactive {
		t.Fatalf("The correction bypassed the operator pause: %+v", got)
	}
	rec.Tasks[1].Delivery.CaptureRun = "finished-worker"
	if got := Decide(rec, freeLock(), cfg, noonClock()); got.Idle != IdleProjectInactive {
		t.Fatalf("The capture bypassed the operator pause: %+v", got)
	}
	starts := SelectStarts([]ProjectInput{{Records: rec}}, base, false)
	if len(starts) != 1 || starts[0].Operation != OperationCapture || starts[0].RunID != "finished-worker" {
		t.Fatalf("The pause prevented permanent output capture: %+v", starts)
	}
}

func TestApprovalMergesBeforeACorrection(t *testing.T) {
	correction := tk(10, StatusSeeded)
	correction.Delivery.CorrectionOpen = true
	approved := tk(1, StatusPackaged, decided(DecisionApproved, at(-1)))
	approved.Delivery = Delivery{CandidateID: 1, CandidateMerged: true}
	rec := recs([]Task{approved, correction}, nil)
	if got := Decide(rec, freeLock(), DefaultConfig(), noonClock()); got.Idle != "priority-task-wait" {
		t.Fatalf("The correction displaced the approval: %+v", got)
	}
	if starts := selectOne(rec, freeLock()); !reflect.DeepEqual(starts, []string{"apply:1"}) {
		t.Fatalf("The correction held the approved merge: %+v", starts)
	}
	rec.Tasks[0].Archived = true
	wantSpawn(t, Decide(rec, freeLock(), DefaultConfig(), noonClock()), RoleWorker, pi64(10))

}

func TestApprovalAndUnrelatedCleanupStartTogether(t *testing.T) {
	approved := tk(10, StatusPackaged, decided(DecisionApproved, at(-1)))
	approved.Delivery = Delivery{CandidateID: 1, CandidateMerged: true}
	other := tk(1, StatusWorked)
	rec := recs([]Task{other, approved}, nil)
	rec.Cleanups = []Cleanup{{TaskID: other.ID, RunID: "finished-worker"}}
	got := SelectStarts([]ProjectInput{{Records: rec}}, base, false)
	want := []RepositoryOperation{
		{Project: "ws", TaskID: approved.ID, Operation: OperationApply},
		{Project: "ws", TaskID: other.ID, Operation: OperationCleanup, RunID: "finished-worker"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Unrelated cleanup delayed an approved merge: %+v", got)
	}
}

func TestReceiptRecoveryIgnoresPauseAndAttention(t *testing.T) {
	task := tk(10, StatusPackaged, decided(DecisionApproved, base))
	task.Delivery = Delivery{RecoveryRun: "authorized", RecoveryOperation: "apply", ApplyPending: true}
	rec := recs([]Task{task}, nil)
	rec.ProjectStatus = "paused"
	rec.AttentionTaskIDs = []int64{11, 12, 13}
	rec.VisionReady = false
	got := SelectStarts([]ProjectInput{{Records: rec}}, base, false)
	if len(got) != 1 || got[0].Operation != OperationReconcile || got[0].RunID != "authorized" || got[0].Target() != "authorized" {
		t.Fatalf("The hold prevented receipt recovery: %+v", got)
	}
	got = SelectStarts([]ProjectInput{{Records: rec, Lease: freshLock(pi64(10))}}, base, false)
	if len(got) != 0 {
		t.Fatalf("The recovery overlapped the agent run of its task: %+v", got)
	}
}

func TestOpenCorrectionsStartBeforeTheOrdinaryRules(t *testing.T) {
	later := tk(5, StatusSeeded)
	later.Delivery = Delivery{CorrectionOpen: true}
	earlier := tk(3, StatusWorked)
	earlier.Delivery = Delivery{CorrectionOpen: true}
	expedited := tk(1, StatusVerified, prio(-1))
	rec := recs([]Task{expedited, later, earlier}, []Packet{pk(5), pk(8), pk(9)})
	// expedited task, and the review capacity check.
	wantSpawn(t, decide(t, rec, freeLock()), RoleVerifier, pi64(3))
	rec.Tasks[2].Blocked = true
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(5))
	rec.Tasks[1].ExecutionHeld = true
	wantQueueFull(t, decide(t, rec, freeLock()))
}

// The acceptance scenarios of merge repairs and the Project priority. The
// review capacity is 3, and each packet holds one review slot.
func TestAConflictAfterAnApproval(t *testing.T) {
	x := tk(1, StatusSeeded)
	x.Delivery = Delivery{CorrectionOpen: true}
	y := tk(2, StatusPackaged)
	rec := recs([]Task{x, y}, []Packet{pk(1), pk(2)})
	wantSpawn(t, decide(t, rec, freeLock()), RoleWorker, pi64(x.ID))
	rec.Tasks[1].Decision = DecisionApproved
	if got := selectOne(rec, freshLock(pi64(x.ID))); !reflect.DeepEqual(got, []string{"apply:2"}) {
		t.Fatalf("The approval waited for the active agent run: %v", got)
	}
	rec.Tasks[0].Status = StatusWorked
	if got := decide(t, rec, freeLock()); got.Idle != "priority-task-wait" {
		t.Fatalf("The Verifier run started before the approved merge: %+v", got)
	}
	rec.Tasks[1].Archived = true
	rec.Packets = []Packet{pk(1)}
	wantSpawn(t, decide(t, rec, freeLock()), RoleVerifier, pi64(x.ID))
}

func TestAPacketThatAwaitsADecisionAfterAMergeRepair(t *testing.T) {
	x := tk(1, StatusPackaged)
	w := tk(2, StatusPackaged, decided(DecisionApproved, at(-1)))
	z := tk(3, StatusVerified)
	rec := recs([]Task{x, w, z}, []Packet{pk(1), pk(2)})
	if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{"apply:2"}) {
		t.Fatalf("The approved merge did not start: %v", got)
	}
	if got := decide(t, rec, freeLock()); got.Idle != "priority-task-wait" {
		t.Fatalf("An agent run started before the approved merge: %+v", got)
	}
	rec.Tasks[1].Archived = true
	rec.Packets = []Packet{pk(1)}
	wantSpawn(t, decide(t, rec, freeLock()), RolePackager, pi64(z.ID))
	rec.Tasks[2].Status = StatusPackaged
	rec.Tasks[2].Delivery = Delivery{}
	if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{"prepare:3"}) {
		t.Fatalf("The merge candidate step of task Z did not start: %v", got)
	}
	rec.Tasks[2].Delivery = Delivery{CandidateID: 9, CandidateMerged: true, ReviewPending: true}
	if got := ReviewPublications(rec, DefaultConfig()); !reflect.DeepEqual(got, []int64{3}) {
		t.Fatalf("The review packet of task Z was not published: %v", got)
	}
}

func TestABlockedMergeRepair(t *testing.T) {
	x := tk(1, StatusSeeded, blocked())
	x.Delivery = Delivery{CorrectionOpen: true}
	z := tk(3, StatusVerified)
	rec := recs([]Task{x, z}, []Packet{pk(1)})
	rec.AttentionTaskIDs = []int64{x.ID}
	wantSpawn(t, decide(t, rec, freeLock()), RolePackager, pi64(z.ID))
	rec.Tasks[1].Status = StatusPackaged
	rec.Tasks[1].Delivery = Delivery{CandidateID: 9, CandidateMerged: true, ReviewPending: true}
	if got := ReviewPublications(rec, DefaultConfig()); !reflect.DeepEqual(got, []int64{3}) {
		t.Fatalf("The review packet of task Z was not published: %v", got)
	}
}

func TestAnApprovedTaskThatWaitsForItsMerge(t *testing.T) {
	a := tk(1, StatusPackaged, decided(DecisionApproved, at(-1)))
	a.Delivery.WaitUntil = pt(at(5))
	b := prepareDue(2)
	other := tk(3, StatusSeeded)
	rec := recs([]Task{a, b, other}, []Packet{pk(1)})
	if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{"prepare:2"}) {
		t.Fatalf("The merge candidate step of task B waited: %v", got)
	}
	rec.Tasks[1].Delivery = Delivery{CandidateID: 9, CandidateMerged: true, ReviewPending: true}
	if got := ReviewPublications(rec, DefaultConfig()); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("The review packet of task B was not published: %v", got)
	}
	if got := decide(t, rec, freeLock()); got.Idle != "priority-task-wait" {
		t.Fatalf("Another agent run started before the merge of task A: %+v", got)
	}
	rec.Tasks[0].Delivery.WaitUntil = pt(at(-1))
	rec.Tasks[1].Delivery = Delivery{}
	if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{"observe:1"}) {
		t.Fatalf("The merge step of task A did not start first: %v", got)
	}
	rec.Tasks[0].Blocked = true
	if got := selectOne(rec, freeLock()); !reflect.DeepEqual(got, []string{"prepare:2"}) {
		t.Fatalf("The blocked priority task held the merge candidate step of task B: %v", got)
	}
	rec.Tasks[1].Delivery = Delivery{CandidateID: 9, CandidateMerged: true}
	if got := decide(t, rec, freeLock()); got.Idle != IdleAttentionRequired {
		t.Fatalf("The blocked priority task released agent runs: %+v", got)
	}
	rec.Tasks[1].Delivery = Delivery{}
	rec.Tasks[0].Delivery.ApplyPending = true
	if got := selectOne(rec, freeLock()); len(got) != 0 {
		t.Fatalf("An unfinished merge authorization of a blocked task released the merge candidate step: %v", got)
	}
}
