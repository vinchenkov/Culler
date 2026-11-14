package dispatch

import "testing"

func TestVisionRequiredBlocksAgentRuns(t *testing.T) {
	cases := map[string]Records{
		"proposal": recs(nil, nil),
		"worker":   recs([]Task{tk(1, StatusSeeded)}, nil),
		"verifier": recs([]Task{tk(1, StatusWorked)}, nil),
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			rec.VisionReady = false
			cfg := DefaultConfig()
			if got := Decide(rec, freeLock(), cfg, noonClock()); got.Kind != KindIdle || got.Idle != IdleVisionRequired {
				t.Fatalf("Missing vision allowed: %+v", got)
			}
			rec.VisionReady = true
			if got := Decide(rec, freeLock(), cfg, noonClock()); got.Kind == KindIdle {
				t.Fatalf("Valid vision blocked: %+v", got)
			}
		})
	}
}

func TestVisionRequiredBlocksMergeButNotCapture(t *testing.T) {
	approved := tk(1, StatusPackaged, decided(DecisionApproved, at(-30)))
	worked := tk(2, StatusWorked)
	worked.Delivery.CaptureRun = "verifier-run"
	rec := recs([]Task{approved, worked}, []Packet{pk(1)})
	rec.VisionReady = false
	starts := SelectStarts([]ProjectInput{{Records: rec}}, base, false)
	if len(starts) != 1 || starts[0].Operation != OperationCapture {
		t.Fatalf("Missing vision allowed a merge or prevented the capture: %+v", starts)
	}
	if state := MergeStateOf(rec, WikiMergeRecords{}, approved.ID, base); state.Kind != MergeStateVision {
		t.Fatalf("Missing vision did not name the merge state: %+v", state)
	}
	rec.VisionReady = true
	starts = SelectStarts([]ProjectInput{{Records: rec}}, base, false)
	if len(starts) != 2 || starts[0].Operation != OperationCapture || starts[1].Operation != OperationApply {
		t.Fatalf("Valid vision blocked the merge: %+v", starts)
	}
}

func TestVisionRequiredPreservesLeaseAndPause(t *testing.T) {
	rec := recs([]Task{tk(1, StatusSeeded)}, nil)
	rec.VisionReady = false
	cfg := DefaultConfig()
	lease := freshLock(pi64(1))
	if got := Decide(rec, lease, cfg, noonClock()); got.Idle != IdleLeaseHeld {
		t.Fatalf("Active run interrupted: %+v", got)
	}
	lease.FinalOutcome = "completed"
	if got := Decide(rec, lease, cfg, noonClock()); got.Kind != KindReap || got.Reap.Reason != ReapReported {
		t.Fatalf("Completion blocked: %+v", got)
	}
	rec.VisionReady = true
	rec.ProjectStatus = "paused"
	if got := Decide(rec, freeLock(), cfg, noonClock()); got.Idle != IdleProjectInactive {
		t.Fatalf("Vision cleared Project pause: %+v", got)
	}
}
