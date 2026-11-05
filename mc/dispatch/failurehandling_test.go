package dispatch

import (
	"testing"
	"time"
)

func TestAttentionPausePreservesActiveRunsAndCleanup(t *testing.T) {
	rec := recs([]Task{tk(1, StatusSeeded)}, nil)
	rec.AttentionTaskIDs = []int64{10, 11, 12}
	cfg := DefaultConfig()
	if got := Decide(rec, Lease{}, cfg, noonClock()); got.Idle != IdleAttentionRequired {
		t.Fatalf("automatic dispatch continued during the attention pause: %+v", got)
	}
	lease := Lease{Held: true, RunID: "active", SubjectID: pi64(1), AcquiredUptime: up(0), DeadlineUptime: up(time.Hour)}
	if got := Decide(rec, lease, cfg, noonClock()); got.Idle != IdleLeaseHeld {
		t.Fatalf("the pause interrupted an active run: %+v", got)
	}
	lease.FinalOutcome = "infra-failed"
	got := Decide(rec, lease, cfg, noonClock())
	if got.Kind != KindReap || got.Reap.Reason != ReapReported || got.Reap.ChargeRetries || !got.Reap.StopContainer {
		t.Fatalf("the final report did not request container cleanup without another charge: %+v", got)
	}
	rec.AttentionTaskIDs = []int64{10, 11}
	if got := Decide(rec, Lease{}, cfg, noonClock()); got.Kind != KindSpawn {
		t.Fatalf("dispatch did not resume below the threshold: %+v", got)
	}
}

func TestStalledTaskIsExcludedAtEveryStage(t *testing.T) {
	for _, status := range []Status{StatusProposed, StatusSeeded, StatusWorked, StatusVerified, StatusPackaged} {
		t.Run(string(status), func(t *testing.T) {
			task := tk(1, status)
			task.Stalled = true
			task.DispatchRetries = 0
			rec := recs([]Task{task}, nil)
			got := Decide(rec, Lease{}, DefaultConfig(), noonClock())
			if got.Kind == KindSpawn && got.Spawn.SubjectID != nil {
				t.Fatalf("dispatch selected a stalled task: %+v", got)
			}
			task.Decision = DecisionApproved
			rec = recs([]Task{task}, nil)
			if starts := SelectStarts([]ProjectInput{{Records: rec}}, base, false); len(starts) != 0 {
				t.Fatalf("a stalled task remained eligible for a main update: %+v", starts)
			}
			if state := MergeStateOf(rec, WikiMergeRecords{}, task.ID, base); state.Kind != MergeStateBlocked {
				t.Fatalf("the stall did not name the merge state: %+v", state)
			}
		})
	}
}
