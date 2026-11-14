package dispatch

// Decision-table coverage for the edge cases of each tick step.
// Fixture helpers live in dispatch_test.go.

import (
	"reflect"
	"testing"
	"time"
)

func project(name string) tmod { return func(t *Task) { t.Project = name } }

// ---------------------------------------------------------------------------
// Reap-reason precedence under simultaneous thresholds. dispatch.go
// fixes the check order watchdog → timeout → hard-deadline; the reap outcome
// is identical either way (assert the payload equal modulo Reason).

func TestStep0_SimultaneousThresholds_ReasonPrecedence_OutcomeIdentical(t *testing.T) {
	rec := recs([]Task{tk(1, StatusSeeded)}, nil)

	// (a) never-heartbeated + past hard deadline → spawn-watchdog wins.
	wd := Lease{
		Held: true, RunID: "run-x", Owner: "worker", SubjectID: pi64(1),
		AcquiredUptime: up(-2 * time.Hour), // far past spawn_grace_s
		DeadlineUptime: up(-time.Minute),   // hard deadline also past
	}
	aw := decide(t, rec, wd)
	if aw.Kind != KindReap || aw.Reap.Reason != ReapSpawnWatchdog {
		t.Fatalf("never-heartbeated + past deadline: want reap(spawn-watchdog), got %+v", aw)
	}

	// (b) heartbeated-stale + past hard deadline → lease-timeout wins.
	to := wd
	to.HeartbeatUptime = pd(up(-2 * time.Hour)) // silent far past timeout+grace
	al := decide(t, rec, to)
	if al.Kind != KindReap || al.Reap.Reason != ReapLeaseTimeout {
		t.Fatalf("stale-heartbeat + past deadline: want reap(lease-timeout), got %+v", al)
	}

	// The reap payload is identical modulo Reason: same run, same charge,
	// same block decision, same stop effect.
	nw, nt := *aw.Reap, *al.Reap
	nw.Reason, nt.Reason = "", ""
	if !reflect.DeepEqual(nw, nt) {
		t.Fatalf("reap outcome must be identical modulo Reason:\nwatchdog: %+v\ntimeout:  %+v", nw, nt)
	}
}

// ---------------------------------------------------------------------------
// Hard-deadline boundary not-taken: one second before
// the deadline uptime with a fresh heartbeat → idle lease-held.

func TestStep0_HardDeadline_OneSecondEarly_NotTaken(t *testing.T) {
	lock := Lease{
		Held: true, RunID: "run-x", SubjectID: pi64(1),
		AcquiredUptime:  up(-3 * time.Hour),
		HeartbeatUptime: pd(up(-30 * time.Second)), // fresh
		DeadlineUptime:  up(time.Second),           // now is 1s before it
	}
	a := decide(t, recs([]Task{tk(1, StatusSeeded)}, nil), lock)
	if a.Kind != KindIdle || a.Idle != IdleLeaseHeld {
		t.Fatalf("1s before the deadline uptime, heartbeat fresh: want idle(lease-held), got %+v", a)
	}
}

func TestStep0_ReapWithNoAllowanceStillStopsTheRun(t *testing.T) {
	lock := Lease{
		Held: true, RunID: "run-x", SubjectID: pi64(1),
		AcquiredUptime: up(-2 * time.Minute), // never heartbeated → watchdog
		DeadlineUptime: up(2 * time.Hour),
	}
	a := decide(t, recs([]Task{tk(1, StatusSeeded, retries(0))}, nil), lock)
	if a.Kind != KindReap {
		t.Fatalf("want reap, got %+v", a)
	}
	if !a.Reap.StopContainer || a.Reap.RunID != lock.RunID {
		t.Fatalf("An exhausted allowance prevented the required stop: %+v", a.Reap)
	}
}

// ---------------------------------------------------------------------------
// (2a) archived-packet link invisible: a task whose only packet link
// is an *archived* packet would win (2a) by rank — it must be invisible, and
// the lower-ranked row with a live link is selected instead.

func TestStep2a_ArchivedPacketLinkInvisible_LiveLinkWins(t *testing.T) {
	rec := recs([]Task{
		tk(1, StatusWorked), // rank 3 — would win via its (archived) packet link
		tk(2, StatusSeeded), // rank 2 — holds a live packet link
		tk(3, StatusPackaged), tk(4, StatusPackaged),
	}, []Packet{pk(1, pArchived()), pk(2), pk(3), pk(4)}) // occupancy 3 = at cap
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleWorker, pi64(2))
}

// ---------------------------------------------------------------------------
// (2a) orders the in-flight rows as (3) does: at cap, a P-1 low-rank
// in-flight row precedes a P2 higher-rank one.

func TestStep2a_PriorityPrecedesStageAtCap(t *testing.T) {
	rec := recs([]Task{
		tk(1, StatusSeeded, prio(-1)), // expedited, rank 2, live packet
		tk(2, StatusWorked, prio(2)),  // P2, rank 3, live packet
		tk(3, StatusPackaged),
	}, atCapPackets(1, 2, 3))
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleWorker, pi64(1))
}

// ---------------------------------------------------------------------------
// (3) Arbiter pool excludes archived proposals: a rejected+archived
// proposal must not appear in ProposedPool while a live proposal dispatches
// the Arbiter (snapshot honesty; the Arbiter's coverage check depends on it).
// The archived row sits between the live ids to pin ascending order too.

func TestStep3_ProposedPoolExcludesArchivedRejectedProposal(t *testing.T) {
	rec := recs([]Task{
		tk(8, StatusProposed),
		tk(2, StatusProposed, archived(), decided(DecisionRejected, at(-10))),
		tk(5, StatusProposed),
	}, nil)
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleArbiter, nil) // older created_at (id-correlated) wins (3)
	if want := []int64{5, 8}; !reflect.DeepEqual(a.Spawn.ProposedPool, want) {
		t.Fatalf("pool must exclude the archived proposal and stay ascending: want %v, got %v",
			want, a.Spawn.ProposedPool)
	}
}

// ---------------------------------------------------------------------------
// (4) dedupe tie order: equal decided_at → id DESC.

func TestStep4_DedupeTieOrder_EqualDecidedAt_IDDesc(t *testing.T) {
	rec := recs([]Task{
		tk(3, StatusProposed, archived(), decided(DecisionRejected, at(-10))),
		tk(7, StatusProposed, archived(), decided(DecisionRejected, at(-10))),
		tk(5, StatusProposed, archived(), decided(DecisionRejected, at(-10))),
	}, nil)
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleStrategist, nil)
	if want := []string{"task-7", "task-5", "task-3"}; !reflect.DeepEqual(a.Spawn.DedupeTitles, want) {
		t.Fatalf("decided_at tie → id DESC: want %v, got %v", want, a.Spawn.DedupeTitles)
	}
}

// ---------------------------------------------------------------------------
// The queue cap applies to each Project separately.
func TestOccupancy_PerProject(t *testing.T) {
	// The queue for ws-a is full.
	capped := recs([]Task{
		tk(1, StatusPackaged, project("ws-a")),
		tk(2, StatusPackaged, project("ws-a")),
		tk(3, StatusPackaged, project("ws-a")),
		tk(4, StatusVerified, project("ws-a")), // would win (3) with room
	}, atCapPackets(1, 2, 3))
	capped.Project = "ws-a"
	a := decide(t, capped, freeLock())
	wantQueueFull(t, a)
	// ws-d has room: its evaluation sees none of ws-a's packets and
	// dispatches its own verified task.
	room := recs([]Task{tk(5, StatusVerified, project("ws-d"))}, nil)
	room.Project = "ws-d"
	b := decide(t, room, freeLock())
	wantSpawn(t, b, RolePackager, pi64(5))
	if b.Spawn.Project != "ws-d" {
		t.Fatalf("spawn names %q, want ws-d", b.Spawn.Project)
	}
}

// ---------------------------------------------------------------------------
// Dispatch does not select a paused or archived Project: step (0) still
// reconciles its lease (a run that holds it ends on the ordinary lease
// rules), and then the evaluation stops — no dispatch and no propose for
// that Project.

func TestInactiveProject_LeaseStillReconciled_NothingElse(t *testing.T) {
	for _, status := range []string{"paused", "archived"} {
		t.Run(status, func(t *testing.T) {
			one := tk(1, StatusPackaged, decided(DecisionApproved, at(-10))) // the apply is pending
			one.ProjectStatus = status
			two := tk(2, StatusVerified)
			two.ProjectStatus = status
			rec := recs([]Task{one, two}, []Packet{pk(1)})
			rec.ProjectStatus = status

			// Free lease: idle(project-inactive) — no dispatch and no
			// propose. The start request starts no merge.
			a := decide(t, rec, freeLock())
			if a.Kind != KindIdle || a.Idle != IdleProjectInactive {
				t.Fatalf("%s Project with a free lease: got %+v, want idle(project-inactive)", status, a)
			}
			if starts := SelectStarts([]ProjectInput{{Records: rec}}, base, false); len(starts) != 0 {
				t.Fatalf("%s Project started %+v", status, starts)
			}

			// Fresh held lease: still idle(lease-held) — the run keeps going.
			if a := decide(t, rec, freshLock(pi64(2))); a.Kind != KindIdle || a.Idle != IdleLeaseHeld {
				t.Fatalf("%s Project with a fresh lease: got %+v, want idle(lease-held)", status, a)
			}

			// Reapable held lease: the reap still happens, on the ordinary rules.
			stale := Lease{
				Held: true, RunID: "run-stale", Owner: "packager", SubjectID: pi64(2),
				AcquiredUptime:  up(-3 * time.Hour),
				HeartbeatUptime: pd(up(-2 * time.Hour)),
				DeadlineUptime:  up(time.Hour),
			}
			a = decide(t, rec, stale)
			if a.Kind != KindReap || a.Reap.RunID != "run-stale" || a.Reap.Reason != ReapLeaseTimeout {
				t.Fatalf("%s Project with a dead run: got %+v, want the ordinary reap", status, a)
			}
			if a.Reap.Project != rec.Project {
				t.Fatalf("reap names %q, want the evaluated Project %q", a.Reap.Project, rec.Project)
			}
		})
	}
}

// Inactive Projects still require lease recovery.
func TestTickOrder(t *testing.T) {
	got := TickOrder([]ProjectState{
		{ID: "zeta", Status: "active"},
		{ID: "beta", Status: "paused", LeaseHeld: true},
		{ID: "alpha", Status: "active", LeaseHeld: true},
		{ID: "gamma", Status: "archived"},
		{ID: "delta", Status: "paused"},
		{ID: "eta", Status: "archived", LeaseHeld: true},
	})
	if want := []string{"alpha", "beta", "delta", "eta", "gamma", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("TickOrder = %v, want %v", got, want)
	}
	if got := TickOrder(nil); len(got) != 0 {
		t.Fatalf("TickOrder(nil) = %v, want empty", got)
	}
}
