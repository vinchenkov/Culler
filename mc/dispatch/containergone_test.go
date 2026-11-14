package dispatch

import (
	"testing"
	"time"
)

// The lease wedge.
//
// A container that dies without a terminal `mc` action held the global lease
// until `last_heartbeat_at + timeout + grace`, which stops the entire board.
// The runner must never convert a harness exit into spine state, and the lease
// rules recover such a run — but they must recover it PROMPTLY.
// They did not: a transient provider stream disconnect cost 75 minutes of
// budget, and on a sleeping laptop far more, because the thresholds are
// measured on the lock domain's clock and that clock stalls with the host.
//
// So the reap gains a fourth condition, and it is deliberately the only one
// that does not read a clock inside the lock domain at all: the resident
// observes the lock holder's container to be absent and reports how long its
// launch record has existed IN HOST TIME. Both operands of the guard are then
// host-measured, so no comparison ever crosses the two clock domains — the
// mistake that would early-reap a healthy run that had merely just claimed
// (the live skew was measured at 109s, 1.8x spawn_grace_s).
//
// Every unknown is fail-closed: no observation, no launch age, or a container
// that was actually seen all mean "not reapable on this ground", and the run
// falls back to the three ordinary thresholds.

func heldLock() Lease {
	return Lease{
		Held:            true,
		RunID:           "ec62582c43ba415f",
		Owner:           "arbiter",
		AcquiredUptime:  up(-3 * time.Minute),
		HeartbeatUptime: pd(up(-2 * time.Minute)),
		// Far enough out that neither the ordinary timeout nor the hard
		// deadline can fire; only the new condition can move this lock.
		DeadlineUptime: up(4 * time.Hour),
	}
}

func TestAbsentContainerIsReapedOnceItsHostMeasuredGraceElapses(t *testing.T) {
	now := time.Date(2026, 8, 5, 1, 55, 0, 0, time.UTC)
	cfg := DefaultConfig()
	lock := heldLock()

	clk := Clock{Now: now, Uptime: baseUptime, HostLiveness: &HostLiveness{
		RunID:          lock.RunID,
		Present:        false,
		LaunchAgeKnown: true,
		LaunchAgeS:     cfg.SpawnGraceS,
	}}

	reason, ok := reapable(Records{}, lock, cfg, clk)
	if !ok {
		t.Fatalf("a lock holder whose container is positively absent past its host-measured grace must be reapable")
	}
	if reason != ReapContainerGone {
		t.Fatalf("reason = %q, want %q", reason, ReapContainerGone)
	}
}

func TestAbsentContainerIsNotReapedInsideItsHostMeasuredGrace(t *testing.T) {
	now := time.Date(2026, 8, 5, 1, 55, 0, 0, time.UTC)
	cfg := DefaultConfig()
	lock := heldLock()

	clk := Clock{Now: now, Uptime: baseUptime, HostLiveness: &HostLiveness{
		RunID:          lock.RunID,
		Present:        false,
		LaunchAgeKnown: true,
		LaunchAgeS:     cfg.SpawnGraceS - 1,
	}}

	if reason, ok := reapable(Records{}, lock, cfg, clk); ok {
		t.Fatalf("a just-launched run whose container has not yet appeared must not be reaped, got %q", reason)
	}
}

// The four fail-closed shapes. Each is a way the host observation can be
// missing or inconclusive, and none of them may reap: an absent observation is
// not evidence of an absent container.
func TestInconclusiveHostObservationNeverReaps(t *testing.T) {
	now := time.Date(2026, 8, 5, 1, 55, 0, 0, time.UTC)
	cfg := DefaultConfig()
	lock := heldLock()

	cases := []struct {
		name string
		live *HostLiveness
	}{
		{
			name: "no observation at all — the in-process seam, or a resident that could not reach Docker",
			live: nil,
		},
		{
			name: "the container was seen, so the run is alive whatever else is true",
			live: &HostLiveness{RunID: lock.RunID, Present: true, LaunchAgeKnown: true, LaunchAgeS: 9999},
		},
		{
			name: "absent, but no launch record exists to age — nothing host-measured to compare",
			live: &HostLiveness{RunID: lock.RunID, Present: false, LaunchAgeKnown: false},
		},
		{
			name: "the observation names a different run than the one holding the lease",
			live: &HostLiveness{RunID: "0000000000000000", Present: false, LaunchAgeKnown: true, LaunchAgeS: 9999},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := Clock{Now: now, Uptime: baseUptime, HostLiveness: tc.live}
			if reason, ok := reapable(Records{}, lock, cfg, clk); ok {
				t.Fatalf("must not reap on an inconclusive observation, got %q", reason)
			}
		})
	}
}

// The new condition is additive: it may free a lease the ordinary thresholds
// would still be holding, and it must never suppress one they would fire. A
// present container past its lease timeout is still reaped, and on the
// ordinary reason.
func TestAPresentContainerStillReapsOnTheOrdinaryThresholds(t *testing.T) {
	now := time.Date(2026, 8, 5, 1, 55, 0, 0, time.UTC)
	cfg := DefaultConfig()
	lock := heldLock()
	lock.HeartbeatUptime = pd(up(-time.Duration(cfg.TimeoutMinutes+cfg.GraceMinutes) * time.Minute))

	clk := Clock{Now: now, Uptime: baseUptime, HostLiveness: &HostLiveness{
		RunID: lock.RunID, Present: true, LaunchAgeKnown: true, LaunchAgeS: 9999,
	}}

	reason, ok := reapable(Records{}, lock, cfg, clk)
	if !ok {
		t.Fatalf("a silent-but-present run past timeout+grace is still reapable")
	}
	if reason != ReapLeaseTimeout {
		t.Fatalf("reason = %q, want %q — the host observation must not rename an ordinary reap", reason, ReapLeaseTimeout)
	}
}
