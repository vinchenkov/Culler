package dispatch

import (
	"reflect"
	"testing"
)

func TestProjectPauseBlocksPipelineActions(t *testing.T) {
	cases := map[string]Records{
		"proposal":     recs([]Task{tk(1, StatusProposed)}, nil),
		"worker":       recs([]Task{tk(1, StatusSeeded)}, nil),
		"verifier":     recs([]Task{tk(1, StatusWorked)}, nil),
		"packager":     recs([]Task{tk(1, StatusVerified)}, nil),
		"new proposal": recs(nil, nil),
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			before := Decide(rec, freeLock(), cfg, noonClock())
			if before.Kind == KindIdle {
				t.Fatalf("The fixture selects no action: %+v", before)
			}
			rec.ProjectStatus = "paused"
			got := Decide(rec, freeLock(), cfg, noonClock())
			if got.Kind != KindIdle || got.Idle != IdleProjectInactive {
				t.Fatalf("The paused pipeline selected %+v", got)
			}
			rec.ProjectStatus = "active"
			if got := Decide(rec, freeLock(), cfg, noonClock()); got.Kind != before.Kind {
				t.Fatalf("Resume selected %+v", got)
			}
		})
	}
}

func TestProjectPauseBlocksMergeButNotCleanup(t *testing.T) {
	approved := tk(1, StatusPackaged, decided(DecisionApproved, at(-30)))
	rec := recs([]Task{approved, tk(2, StatusWorked)}, []Packet{pk(1)})
	rec.Cleanups = []Cleanup{{TaskID: 2, RunID: "worker-run"}}
	want := []RepositoryOperation{
		{Project: "ws", TaskID: 1, Operation: OperationApply},
		{Project: "ws", TaskID: 2, Operation: OperationCleanup, RunID: "worker-run"},
	}
	if got := SelectStarts([]ProjectInput{{Records: rec}}, base, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("The active Project started %+v", got)
	}
	rec.ProjectStatus = "paused"
	if got := SelectStarts([]ProjectInput{{Records: rec}}, base, false); !reflect.DeepEqual(got, want[1:]) {
		t.Fatalf("The paused Project started %+v", got)
	}
	if state := MergeStateOf(rec, WikiMergeRecords{}, approved.ID, base); state.Kind != MergeStatePaused {
		t.Fatalf("The pause did not name the merge state: %+v", state)
	}
}

func TestProjectPausePreservesActiveRunAndCompletion(t *testing.T) {
	rec := recs([]Task{tk(1, StatusSeeded)}, nil)
	rec.ProjectStatus = "paused"
	cfg := DefaultConfig()
	lease := freshLock(pi64(1))
	if got := Decide(rec, lease, cfg, noonClock()); got.Kind != KindIdle || got.Idle != IdleLeaseHeld {
		t.Fatalf("Pause interrupted an active run: %+v", got)
	}
	lease.FinalOutcome = "completed"
	if got := Decide(rec, lease, cfg, noonClock()); got.Kind != KindReap || got.Reap.Reason != ReapReported {
		t.Fatalf("Pause blocked run completion: %+v", got)
	}
}
