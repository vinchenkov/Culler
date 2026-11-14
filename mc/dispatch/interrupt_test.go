package dispatch

import (
	"testing"
	"time"
)

// `mc task interrupt` promised the operator a stop it could not deliver.
// The verb returned an effect-shaped result —
// {"action":"interrupt","stop_container":true} — to its OWN caller's stdout,
// which for a Collie is inside a container; the resident applies effects at
// exactly one call site, the parsed stdout of `mc dispatch`, and `mc dispatch`
// had no interrupt action to emit. Worse, the same transaction released the
// lease, which removed the only remaining path to a stop: the reap selects on
// the lease. So the agent kept running and writing while the operator was told
// it had stopped.
//
// The repair is the architecture's own shape: the verb writes one durable
// fact — the subject's cancellation — and leaves the lease held; the next
// tick's Decide turns that fact into an ordinary reap, and the reap's existing
// stop machinery does the rest. These tests pin the pure half.

func interruptLock(subject int64) Lease {
	return Lease{
		Held:            true,
		RunID:           "ec62582c43ba415f",
		Owner:           "worker",
		SubjectID:       &subject,
		AcquiredUptime:  up(-2 * time.Minute),
		HeartbeatUptime: pd(up(-1 * time.Minute)),
		// Far enough out that no threshold can fire. Only the cancellation can
		// move this lock — which is the point: without it the board sits at
		// idle(lease-held) until the full lease expires.
		DeadlineUptime: up(4 * time.Hour),
	}
}

func liveWorkerRecords(now time.Time, subject int64, decision TaskDecision, archived bool) Records {
	return Records{Tasks: []Task{{
		ID:              subject,
		Title:           "interrupt me",
		Scope:           ScopeTask,
		Status:          StatusSeeded,
		CreatedAt:       now.Add(-time.Hour),
		DispatchRetries: 3,
		Decision:        decision,
		Archived:        archived,
		Project:         "ws",
	}}}
}

func TestCancellingTheLiveSubjectReapsOnTheVeryNextTick(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	subject := int64(7)
	lock := interruptLock(subject)

	// Before the interrupt the run is healthy and the tick must not touch it.
	if a := Decide(liveWorkerRecords(now, subject, DecisionNone, false), lock, cfg, Clock{Now: now, Uptime: baseUptime}); a.Kind != KindIdle {
		t.Fatalf("a healthy held lease must idle, got %v", a.Kind)
	}

	// `mc task interrupt` archived it with decision=cancelled.
	act := Decide(liveWorkerRecords(now, subject, DecisionCancelled, true), lock, cfg, Clock{Now: now, Uptime: baseUptime})
	if act.Kind != KindReap || act.Reap == nil {
		t.Fatalf("a cancelled live subject must be reaped, got %v", act.Kind)
	}
	if act.Reap.Reason != ReapInterrupted {
		t.Fatalf("reap reason = %q, want %q", act.Reap.Reason, ReapInterrupted)
	}
	if act.Reap.RunID != lock.RunID || !act.Reap.StopContainer {
		t.Fatalf("the reap must name the exact run and carry the stop: %+v", act.Reap)
	}
	if act.Reap.ChargeRetries {
		t.Fatalf("an interrupt must charge nothing: %+v", act.Reap)
	}
}

func TestInterruptOutranksEveryClockThresholdAndNeedsNone(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	subject := int64(7)
	rec := liveWorkerRecords(now, subject, DecisionCancelled, true)

	// A stamped-but-never-heartbeated slot inside its spawn grace: every
	// threshold says "not yet", and the interrupt still fires. This is the case
	// the operator actually hits — interrupting a run seconds after it started.
	lock := interruptLock(subject)
	lock.HeartbeatUptime = nil
	lock.AcquiredUptime = up(0)

	act := Decide(rec, lock, cfg, Clock{Now: now, Uptime: baseUptime})
	if act.Kind != KindReap || act.Reap == nil || act.Reap.Reason != ReapInterrupted {
		t.Fatalf("the interrupt must fire with no threshold elapsed, got %v %+v", act.Kind, act.Reap)
	}
}

func TestASubjectlessOrUncancelledLeaseIsNeverInterruptReaped(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	subject := int64(7)

	// Subjectless (Strategist): nothing to cancel, so this
	// condition must never claim it — its reaps stay the ordinary thresholds.
	subjectless := interruptLock(subject)
	subjectless.SubjectID = nil
	if act := Decide(liveWorkerRecords(now, subject, DecisionCancelled, true), subjectless, cfg, Clock{Now: now, Uptime: baseUptime}); act.Kind != KindIdle {
		t.Fatalf("a subjectless lease must not be interrupt-reaped, got %v", act.Kind)
	}

	// A subject decided any other way is not an interrupt. Approval archives a
	// packaged row, which never holds the lease — but the condition must be
	// keyed on cancellation, not on "archived", or an approval race would stop
	// a healthy run.
	approved := liveWorkerRecords(now, subject, DecisionApproved, true)
	if act := Decide(approved, interruptLock(subject), cfg, Clock{Now: now, Uptime: baseUptime}); act.Kind != KindIdle {
		t.Fatalf("an approved subject must not be interrupt-reaped, got %v", act.Kind)
	}

	// A subject the records do not carry at all is fail-closed: unknown is
	// never a reason to stop a live container.
	if act := Decide(Records{}, interruptLock(subject), cfg, Clock{Now: now, Uptime: baseUptime}); act.Kind != KindIdle {
		t.Fatalf("an unknown subject must not be interrupt-reaped, got %v", act.Kind)
	}
}
