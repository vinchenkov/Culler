package dispatch

// The exhaustive decision-table test.
//
// Coverage:
//   A. every tick-walk step taken AND not-taken
//      (reconcile, queue-at-cap, queue-with-room);
//   B. every invisibility rule exercised with a would-otherwise-win row;
//   C. deterministic tie-breaking asserted;
//   D. exactly zero-or-one action per evaluation, always (property suite);
//   E. the many-obligation precedence state asserted (the cascade test).

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Fixture helpers.
// ---------------------------------------------------------------------------

// base: a fixed instant.
var base = time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

// baseUptime: the VM uptime at base.
const baseUptime = 24 * time.Hour

func pi64(v int64) *int64               { return &v }
func pt(t time.Time) *time.Time         { return &t }
func pd(d time.Duration) *time.Duration { return &d }
func at(min int) time.Time              { return base.Add(time.Duration(min) * time.Minute) }
func up(d time.Duration) time.Duration  { return baseUptime + d }
func clock(t time.Time) Clock           { return Clock{Now: t, Uptime: up(t.Sub(base))} }
func noonClock() Clock                  { return clock(base) }

type tmod func(*Task)

func tk(id int64, st Status, mods ...tmod) Task {
	t := Task{
		ID:              id,
		Title:           fmt.Sprintf("task-%d", id),
		Scope:           ScopeTask,
		Priority:        1,
		CreatedAt:       base.Add(time.Duration(id) * time.Second), // deterministic, id-correlated default
		Status:          st,
		DispatchRetries: 3,
		Project:         "ws",
		// The delivery record is in the steady state for every stage: the
		// the fields that its case changes.
		Delivery: Delivery{CandidateID: id, CandidateMerged: true},
	}
	for _, m := range mods {
		m(&t)
	}
	return t
}

func prio(p int) tmod           { return func(t *Task) { t.Priority = p } }
func created(tm time.Time) tmod { return func(t *Task) { t.CreatedAt = tm } }
func blocked() tmod             { return func(t *Task) { t.Blocked = true } }
func archived() tmod            { return func(t *Task) { t.Archived = true } }
func retries(n int) tmod        { return func(t *Task) { t.DispatchRetries = n } }
func decided(d TaskDecision, when time.Time) tmod {
	return func(t *Task) { t.Decision = d; t.DecidedAt = pt(when) }
}

type pmod func(*Packet)

func pk(taskID int64, mods ...pmod) Packet {
	p := Packet{TaskID: taskID, CreatedAt: base.Add(time.Duration(taskID) * time.Second)}
	for _, m := range mods {
		m(&p)
	}
	return p
}

func pArchived() pmod            { return func(p *Packet) { p.Archived = true } }
func pCreated(tm time.Time) pmod { return func(p *Packet) { p.CreatedAt = tm } }

// recs builds one Project's Records ("ws", active, with a ready vision).
func recs(tasks []Task, pkts []Packet) Records {
	return Records{Project: "ws", VisionReady: true, Tasks: tasks, Packets: pkts}
}

func freeLock() Lease { return Lease{} }

// freshLock: held, heartbeated 30s ago, hard deadline 2h out — not reapable.
func freshLock(subject *int64) Lease {
	return Lease{
		Held:            true,
		RunID:           "run-live",
		Owner:           "worker",
		SubjectID:       subject,
		AcquiredUptime:  up(-10 * time.Minute),
		HeartbeatUptime: pd(up(-30 * time.Second)),
		DeadlineUptime:  up(2 * time.Hour),
	}
}

func decide(t *testing.T, rec Records, lock Lease) Action {
	t.Helper()
	a := Decide(rec, lock, DefaultConfig(), noonClock())
	assertWellFormed(t, a)
	assertNamesProject(t, a, rec.Project)
	return a
}

// assertNamesProject: every non-idle action names the Project it was
// evaluated for. The spawn effect carries the Project of its run, for every
// role; so does the reap.
func assertNamesProject(t *testing.T, a Action, project string) {
	t.Helper()
	var got string
	switch a.Kind {
	case KindIdle:
		return
	case KindReap:
		got = a.Reap.Project
	case KindSpawn:
		got = a.Spawn.Project
	}
	if got != project {
		t.Fatalf("%s action names Project %q, want %q", a.Kind, got, project)
	}
}

// assertWellFormed asserts requirement D locally: every evaluation yields
// exactly one agent-run action or idle — exactly one payload, matching the kind.
func assertWellFormed(t *testing.T, a Action) {
	t.Helper()
	n := 0
	if a.Reap != nil {
		n++
	}
	if a.Spawn != nil {
		n++
	}
	switch a.Kind {
	case KindIdle:
		if n != 0 || a.Idle == "" {
			t.Fatalf("malformed idle action: %+v", a)
		}
	case KindReap:
		if n != 1 || a.Reap == nil {
			t.Fatalf("malformed reap action: %+v", a)
		}
	case KindSpawn:
		if n != 1 || a.Spawn == nil {
			t.Fatalf("malformed spawn action: %+v", a)
		}
		// No task state selects the Scribe.
		if a.Spawn.Role == RoleScribe {
			t.Fatalf("the decision selected the Scribe: %+v", a)
		}
	default:
		t.Fatalf("unknown action kind %q", a.Kind)
	}
}

func wantSpawn(t *testing.T, a Action, role Role, subject *int64) {
	t.Helper()
	if a.Kind != KindSpawn {
		t.Fatalf("want spawn %s, got %+v", role, a)
	}
	if a.Spawn.Role != role {
		t.Fatalf("want role %s, got %s (%+v)", role, a.Spawn.Role, a)
	}
	switch {
	case subject == nil && a.Spawn.SubjectID != nil:
		t.Fatalf("want subjectless spawn, got subject %d", *a.Spawn.SubjectID)
	case subject != nil && (a.Spawn.SubjectID == nil || *a.Spawn.SubjectID != *subject):
		t.Fatalf("want subject %d, got %+v", *subject, a.Spawn)
	}
}

// ---------------------------------------------------------------------------
// A/0 — Step (0): reconcile the lease. Taken (three reap thresholds, budget
// outcomes, subjectless) and not-taken (fresh → idle; free → walk continues).

func TestStep0_FreshLeaseIdles_EvenWithEveryObligationWaiting(t *testing.T) {
	// Held-fresh lease returns idle even when an approved contribution
	// waits for its apply and dispatchable rows exist: dispatch runs only
	// when the lease is free.
	rec := Records{
		VisionReady: true,
		Tasks: []Task{
			tk(1, StatusSeeded),
			tk(2, StatusPackaged, decided(DecisionApproved, at(-30))),
		},
		Packets: []Packet{pk(2)},
	}
	a := decide(t, rec, freshLock(pi64(1)))
	if a.Kind != KindIdle || a.Idle != IdleLeaseHeld {
		t.Fatalf("want idle(lease-held), got %+v", a)
	}
}

func TestStep0_SpawnWatchdog_NeverHeartbeated(t *testing.T) {
	lock := Lease{
		Held: true, RunID: "run-x", SubjectID: pi64(1),
		AcquiredUptime: up(-61 * time.Second), // spawn_grace_s = 60 → past
		DeadlineUptime: up(2 * time.Hour),
	}
	a := decide(t, recs([]Task{tk(1, StatusSeeded)}, nil), lock)
	if a.Kind != KindReap || a.Reap.Reason != ReapSpawnWatchdog {
		t.Fatalf("want reap(spawn-watchdog), got %+v", a)
	}
	if !a.Reap.StopContainer || a.Reap.RunID != "run-x" {
		t.Fatalf("reap must return the stop effect for the reaped run: %+v", a.Reap)
	}
	if !a.Reap.ChargeRetries {
		t.Fatalf("reap must charge the subject's dispatch_retries: %+v", a.Reap)
	}
}

func TestStep0_SpawnWatchdog_BoundaryOneSecondEarly_NotTaken(t *testing.T) {
	lock := Lease{
		Held: true, RunID: "run-x", SubjectID: pi64(1),
		AcquiredUptime: up(-59 * time.Second),
		DeadlineUptime: up(2 * time.Hour),
	}
	a := decide(t, recs([]Task{tk(1, StatusSeeded)}, nil), lock)
	if a.Kind != KindIdle || a.Idle != IdleLeaseHeld {
		t.Fatalf("59s < spawn_grace_s: want idle(lease-held), got %+v", a)
	}
}

// The first-heartbeat grace of a lease is the configured grace for each lease owner.
func TestReapableHasNoScribeBranch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SpawnGraceS = 5
	lease := Lease{Held: true, RunID: "run-x", Owner: "scribe", AcquiredUptime: up(0), DeadlineUptime: up(2 * time.Hour)}
	after := func(d time.Duration) Clock { return Clock{Now: base.Add(d), Uptime: up(d)} }
	if reason, stop := reapable(Records{}, lease, cfg, after(4*time.Second)); stop {
		t.Fatalf("the lease stopped before the configured grace: %s", reason)
	}
	if reason, stop := reapable(Records{}, lease, cfg, after(5*time.Second)); !stop || reason != ReapSpawnWatchdog {
		t.Fatalf("the lease of the owner scribe got another grace than 5 seconds: %q, %v", reason, stop)
	}
}

func TestStep0_LeaseTimeout_HeartbeatedThenSilent(t *testing.T) {
	lock := Lease{
		Held: true, RunID: "run-x", SubjectID: pi64(1),
		AcquiredUptime:  up(-3 * time.Hour),
		HeartbeatUptime: pd(up(-(75*time.Minute + time.Second))), // timeout 60 + grace 15 → past
		DeadlineUptime:  up(2 * time.Hour),
	}
	a := decide(t, recs([]Task{tk(1, StatusSeeded)}, nil), lock)
	if a.Kind != KindReap || a.Reap.Reason != ReapLeaseTimeout {
		t.Fatalf("want reap(lease-timeout), got %+v", a)
	}
}

func TestStep0_LeaseTimeout_HeartbeatJustInsideFullLease_NotTaken(t *testing.T) {
	lock := Lease{
		Held: true, RunID: "run-x", SubjectID: pi64(1),
		AcquiredUptime:  up(-3 * time.Hour),
		HeartbeatUptime: pd(up(-74 * time.Minute)),
		DeadlineUptime:  up(2 * time.Hour),
	}
	a := decide(t, recs([]Task{tk(1, StatusSeeded)}, nil), lock)
	if a.Kind != KindIdle || a.Idle != IdleLeaseHeld {
		t.Fatalf("silent 74m < 75m full lease: want idle, got %+v", a)
	}
}

func TestStep0_HardDeadline_ReapedWhileStillHeartbeating(t *testing.T) {
	lock := Lease{
		Held: true, RunID: "run-x", SubjectID: pi64(1),
		AcquiredUptime:  up(-3 * time.Hour),
		HeartbeatUptime: pd(up(-5 * time.Second)), // alive!
		DeadlineUptime:  up(-time.Second),         // but past the hard deadline
	}
	a := decide(t, recs([]Task{tk(1, StatusSeeded)}, nil), lock)
	if a.Kind != KindReap || a.Reap.Reason != ReapHardDeadline {
		t.Fatalf("liveness is not progress: want reap(hard-deadline), got %+v", a)
	}
}

func TestStep0_VMRestart_StopsTheRunWithoutCharge(t *testing.T) {
	lock := freshLock(pi64(1))
	lock.BootID = "boot-before-restart"
	a := decide(t, recs([]Task{tk(1, StatusSeeded)}, nil), lock)
	if a.Kind != KindReap || a.Reap.Reason != ReapVMRestart {
		t.Fatalf("want reap(vm-restart), got %+v", a)
	}
	if a.Reap.ChargeRetries || !a.Reap.StopContainer {
		t.Fatalf("a VM restart stops the run and charges nothing: %+v", a.Reap)
	}
}

func TestStep0_HostSleepUsesNoLeaseTime(t *testing.T) {
	// The host slept for ten hours: the wall clock moved, and the VM uptime did not.
	clk := Clock{Now: base.Add(10 * time.Hour), Uptime: up(0)}
	for name, lock := range map[string]Lease{
		"heartbeat":    freshLock(pi64(1)),
		"no heartbeat": {Held: true, RunID: "run-x", SubjectID: pi64(1), AcquiredUptime: up(-30 * time.Second), DeadlineUptime: up(time.Minute)},
	} {
		a := Decide(recs([]Task{tk(1, StatusSeeded)}, nil), lock, DefaultConfig(), clk)
		if a.Kind != KindIdle || a.Idle != IdleLeaseHeld {
			t.Fatalf("%s: host sleep ended a live run: %+v", name, a)
		}
	}
}

func TestStep0_ReapSubjectlessRun_ChargesProjectAllowance(t *testing.T) {
	lock := Lease{
		Held: true, RunID: "run-propose", Owner: "strategist", SubjectID: nil,
		AcquiredUptime: up(-2 * time.Minute),
		DeadlineUptime: up(2 * time.Hour),
	}
	a := decide(t, recs([]Task{tk(1, StatusSeeded)}, nil), lock)
	if a.Kind != KindReap {
		t.Fatalf("want reap, got %+v", a)
	}
	if !a.Reap.ChargeRetries {
		t.Fatalf("The subjectless run must use its Project allowance: %+v", a.Reap)
	}
	if !a.Reap.StopContainer {
		t.Fatalf("subjectless reap still emits the stop effect: %+v", a.Reap)
	}
}

func TestStep0_NotTaken_FreeLockWalksOn(t *testing.T) {
	a := decide(t, recs([]Task{tk(1, StatusSeeded)}, nil), freeLock())
	wantSpawn(t, a, RoleWorker, pi64(1)) // reached step (3)
}

// ---------------------------------------------------------------------------
// A full review queue advances requested revisions and waits for operator decisions.
// ---------------------------------------------------------------------------

// atCapPackets: three unarchived packets on the given (packaged) task ids.
func atCapPackets(ids ...int64) []Packet {
	var ps []Packet
	for _, id := range ids {
		ps = append(ps, pk(id))
	}
	return ps
}

func TestAtCap_NoNewPipelineWorkEver(t *testing.T) {
	// The full review queue blocks new work, including expedited tasks.
	rec := recs([]Task{
		tk(1, StatusPackaged), tk(2, StatusPackaged), tk(3, StatusPackaged),
		tk(4, StatusProposed),           // would win nothing here; pipeline is gated
		tk(5, StatusVerified, prio(-1)), // even expedited pipeline work is gated at cap
	}, atCapPackets(1, 2, 3))
	a := decide(t, rec, freeLock())
	wantQueueFull(t, a)
}

func TestOccupancy_ArchivedPacketFreesTheQueue(t *testing.T) {
	// An archived packet frees a slot for the next task.
	pkts := []Packet{pk(1), pk(2), pk(3, pArchived())}
	rec := recs([]Task{
		tk(1, StatusPackaged), tk(2, StatusPackaged), tk(3, StatusPackaged),
		tk(4, StatusVerified),
	}, pkts)
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RolePackager, pi64(4))
}

func TestStep2a_AdvancesReenteredTask(t *testing.T) {
	// Task 1's packet holds its slot while the task rides the pipeline again;
	// at cap its next stage keeps dispatching.
	rec := recs([]Task{
		tk(1, StatusWorked), // re-entered, now owed verification
		tk(2, StatusPackaged), tk(3, StatusPackaged),
	}, atCapPackets(1, 2, 3))
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleVerifier, pi64(1))
}

func TestStep2a_RoleMapping_SameTableAsStep3(t *testing.T) {
	cases := []struct {
		name string
		task Task
		role Role
	}{
		{"seeded task → Worker", tk(1, StatusSeeded), RoleWorker},
		{"worked task → Verifier", tk(1, StatusWorked), RoleVerifier},
		{"verified task → Packager", tk(1, StatusVerified), RolePackager},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := recs([]Task{
				c.task,
				tk(2, StatusPackaged), tk(3, StatusPackaged),
			}, atCapPackets(1, 2, 3))
			a := decide(t, rec, freeLock())
			subject := pi64(1)
			if c.role == RoleArbiter {
				subject = nil
			}
			wantSpawn(t, a, c.role, subject)
		})
	}
}

func TestStep2a_BlockedInFlightRowInvisible(t *testing.T) {
	// A blocked revision waits for the operator.
	rec := recs([]Task{
		tk(1, StatusWorked, blocked()),
		tk(2, StatusPackaged), tk(3, StatusPackaged),
	}, atCapPackets(1, 2, 3))
	a := decide(t, rec, freeLock())
	wantQueueFull(t, a)
}

func TestStep2a_Ordering_FurthestFirstThenPriorityThenAgeThenID(t *testing.T) {
	mk := func(tasks ...Task) Records {
		all := append([]Task{}, tasks...)
		var pkts []Packet
		for _, x := range tasks {
			pkts = append(pkts, pk(x.ID))
		}
		for len(pkts) < 3 {
			filler := int64(90 + len(pkts))
			all = append(all, tk(filler, StatusPackaged, prio(3)))
			pkts = append(pkts, pk(filler))
		}
		return recs(all, pkts)
	}

	// furthest-first: worked (rank 3) beats seeded (rank 2), even at worse priority.
	a := decide(t, mk(tk(1, StatusSeeded, prio(0)), tk(2, StatusWorked, prio(3))), freeLock())
	wantSpawn(t, a, RoleVerifier, pi64(2))

	// same rank → priority.
	a = decide(t, mk(tk(1, StatusSeeded, prio(2)), tk(2, StatusSeeded, prio(0))), freeLock())
	wantSpawn(t, a, RoleWorker, pi64(2))

	// same rank+priority → older created_at.
	a = decide(t, mk(
		tk(1, StatusSeeded, created(at(-5))),
		tk(2, StatusSeeded, created(at(-50))),
	), freeLock())
	wantSpawn(t, a, RoleWorker, pi64(2))

	// full tie → lower id.
	a = decide(t, mk(
		tk(7, StatusSeeded, created(at(-5))),
		tk(4, StatusSeeded, created(at(-5))),
	), freeLock())
	wantSpawn(t, a, RoleWorker, pi64(4))
}

func TestStep2a_NotTaken_NothingInFlight(t *testing.T) {
	// Completed packets wait for review.
	rec := recs([]Task{
		tk(1, StatusPackaged), tk(2, StatusPackaged), tk(3, StatusPackaged),
	}, atCapPackets(1, 2, 3))
	a := decide(t, rec, freeLock())
	wantQueueFull(t, a)
}

func TestAtCapPackagedWorkWaitsForOperator(t *testing.T) {
	for _, scope := range []Scope{ScopeTask} {
		t.Run(string(scope), func(t *testing.T) {
			task := tk(1, StatusPackaged, prio(-1), created(at(-100)))
			task.Scope = scope
			rec := recs([]Task{task, tk(2, StatusPackaged), tk(3, StatusPackaged)},
				[]Packet{pk(1, pCreated(at(-100))), pk(2), pk(3)})
			before := rec
			before.Tasks = append([]Task(nil), rec.Tasks...)
			before.Packets = append([]Packet(nil), rec.Packets...)
			for day := 0; day < 7; day++ {
				clk := noonClock()
				clk.Now = clk.Now.Add(time.Duration(day) * 24 * time.Hour)
				wantQueueFull(t, Decide(rec, freeLock(), DefaultConfig(), clk))
			}
			if !reflect.DeepEqual(rec, before) {
				t.Fatal("Dispatch changed the packaged work.")
			}
		})
	}
}

func wantQueueFull(t *testing.T, a Action) {
	t.Helper()
	if a.Kind != KindIdle || a.Idle != IdleQueueFull {
		t.Fatalf("The full queue must wait for review: %+v", a)
	}
}

// ---------------------------------------------------------------------------
// A/3 — Queue with room: the single next dispatch. Role mapping, ordering,
// invisibility rules, each with a would-otherwise-win row.

func TestStep3_RoleMapping(t *testing.T) {
	cases := []struct {
		name string
		task Task
		role Role
	}{
		{"proposed task → Arbiter", tk(1, StatusProposed), RoleArbiter},

		{"seeded task → Worker", tk(1, StatusSeeded), RoleWorker},

		{"worked task → Verifier", tk(1, StatusWorked), RoleVerifier},

		{"verified task → Packager", tk(1, StatusVerified), RolePackager},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := decide(t, recs([]Task{c.task}, nil), freeLock())
			subject := pi64(1)
			if c.role == RoleArbiter {
				subject = nil
			}
			wantSpawn(t, a, c.role, subject)
		})
	}

}

func TestStep3_ArbiterBriefSnapshotsEntirePool(t *testing.T) {
	// Batch-wise: the Arbiter sees the whole proposed pool, never the subject
	// row alone; archived (already-rejected) proposals are excluded.
	rec := recs([]Task{
		tk(1, StatusProposed),

		tk(3, StatusProposed, archived(), decided(DecisionRejected, at(-10))),
		tk(4, StatusProposed),
		tk(5, StatusProposed, blocked()),
	}, nil)
	a := decide(t, rec, freeLock())
	if a.Kind != KindSpawn || a.Spawn.Role != RoleArbiter {
		t.Fatalf("want arbiter spawn, got %+v", a)
	}
	if want := []int64{1, 4}; !reflect.DeepEqual(a.Spawn.ProposedPool, want) {
		t.Fatalf("pool snapshot: want %v, got %v", want, a.Spawn.ProposedPool)
	}
}

// A paused Project is not evaluated past step (0): even an expedited
// proposal in it selects nothing. Its tasks are never in an active
// Project's records (the loader partitions by Project), so the
// Arbiter's pool is that Project's own.
func TestStep3_PausedProjectIsInvisible(t *testing.T) {
	paused := tk(1, StatusProposed)
	paused.Priority = -1
	paused.ProjectStatus = "paused"
	rec := recs([]Task{paused}, nil)
	rec.ProjectStatus = "paused"
	if a := decide(t, rec, freeLock()); a.Kind != KindIdle || a.Idle != IdleProjectInactive {
		t.Fatalf("paused Project selected %+v, want idle(project-inactive)", a)
	}
	active := tk(2, StatusProposed)
	active.ProjectStatus = "active"
	a := decide(t, recs([]Task{active}, nil), freeLock())
	wantSpawn(t, a, RoleArbiter, nil)
	if want := []int64{2}; !reflect.DeepEqual(a.Spawn.ProposedPool, want) {
		t.Fatalf("Arbiter pool: %v, want the Project's own proposals", a.Spawn.ProposedPool)
	}
}

func TestStep3_FurthestFirst(t *testing.T) {
	// verified (4) > worked (3) > seeded (2) > proposed (1).
	rec := recs([]Task{
		tk(1, StatusProposed), tk(2, StatusSeeded), tk(3, StatusWorked), tk(4, StatusVerified),
	}, nil)
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RolePackager, pi64(4))
}

func TestStep3_ExpediteLanePartitionsAheadOfStageOrder(t *testing.T) {
	// A P-1 proposal pulls the Arbiter ahead of a verified P0 row.
	rec := recs([]Task{
		tk(1, StatusVerified, prio(0)),
		tk(2, StatusProposed, prio(-1)),
	}, nil)
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleArbiter, nil)
}

func TestStep3_WithinExpeditePartition_FurthestFirstStillApplies(t *testing.T) {
	rec := recs([]Task{
		tk(1, StatusProposed, prio(-1)),
		tk(2, StatusWorked, prio(-1)),
	}, nil)
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleVerifier, pi64(2))
}

func TestStep3_PriorityThenAgeThenID(t *testing.T) {
	// same rank → priority.
	a := decide(t, recs([]Task{
		tk(1, StatusSeeded, prio(2)), tk(2, StatusSeeded, prio(0)),
	}, nil), freeLock())
	wantSpawn(t, a, RoleWorker, pi64(2))

	// same rank+priority → older created_at.
	a = decide(t, recs([]Task{
		tk(1, StatusSeeded, created(at(-1))), tk(2, StatusSeeded, created(at(-90))),
	}, nil), freeLock())
	wantSpawn(t, a, RoleWorker, pi64(2))

	// full tie → lower id.
	a = decide(t, recs([]Task{
		tk(9, StatusSeeded, created(at(-7))), tk(6, StatusSeeded, created(at(-7))),
	}, nil), freeLock())
	wantSpawn(t, a, RoleWorker, pi64(6))
}

func TestStep3_ArchivedRowInvisible(t *testing.T) {
	// Would-otherwise-win: the archived row out-ranks (verified vs seeded).
	rec := recs([]Task{
		tk(1, StatusVerified, archived(), decided(DecisionCancelled, at(-5))),
		tk(2, StatusSeeded),
	}, nil)
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleWorker, pi64(2))
}

func TestStep3_BlockedRowInvisible(t *testing.T) {
	// Would-otherwise-win: blocked verified row out-ranks the seeded row.
	rec := recs([]Task{
		tk(1, StatusVerified, blocked()),
		tk(2, StatusSeeded),
	}, nil)
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleWorker, pi64(2))
}

func TestStep3_PackagedRankZeroInvisible(t *testing.T) {
	// A packaged row is in the review queue, out of pipeline dispatch: with
	// only packaged rows (queue below cap), (3) finds nothing and the tick
	// falls through to the propose fallback.
	rec := recs([]Task{tk(1, StatusPackaged), tk(2, StatusPackaged)},
		[]Packet{pk(1), pk(2)})
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleStrategist, nil)
}

// ---------------------------------------------------------------------------
// A/4 — Step (4): nothing dispatchable → Strategist with dedupe memory.

func TestStep4_EmptySystem_SpawnsPropose(t *testing.T) {
	a := decide(t, recs(nil, nil), freeLock())
	wantSpawn(t, a, RoleStrategist, nil)
}

func TestStep4_OnlyBlockedPackagedParkedRows_SpawnsPropose(t *testing.T) {
	rec := recs([]Task{
		tk(1, StatusSeeded, blocked()),
		tk(2, StatusPackaged),

		tk(4, StatusSeeded, blocked()), // blocked task
	}, []Packet{pk(2)})
	a := decide(t, rec, freeLock())
	wantSpawn(t, a, RoleStrategist, nil)
}

func TestStep4_DedupeMemory_TwentyMostRecentRejectedTitles(t *testing.T) {
	var tasks []Task
	for i := int64(1); i <= 25; i++ {
		tasks = append(tasks, tk(i, StatusProposed, archived(),
			decided(DecisionRejected, at(int(-i))))) // task 1 newest, 25 oldest
	}
	// Cancelled/approved rows never feed the dedupe memory.
	tasks = append(tasks, tk(30, StatusSeeded, archived(), decided(DecisionCancelled, at(0))))
	a := decide(t, recs(tasks, nil), freeLock())
	wantSpawn(t, a, RoleStrategist, nil)
	if len(a.Spawn.DedupeTitles) != 20 {
		t.Fatalf("want 20 titles, got %d", len(a.Spawn.DedupeTitles))
	}
	if a.Spawn.DedupeTitles[0] != "task-1" || a.Spawn.DedupeTitles[19] != "task-20" {
		t.Fatalf("want newest-first task-1..task-20, got first=%s last=%s",
			a.Spawn.DedupeTitles[0], a.Spawn.DedupeTitles[19])
	}
}

// ---------------------------------------------------------------------------
// E — the many-obligation precedence cascade: a state owing everything at
// once yields exactly the top-most obligation; stripping obligations one at
// a time walks the whole precedence chain
// reap → approval wait → revision → idle → task → proposal.

func TestPrecedenceCascade_ManyObligationState(t *testing.T) {
	cfg := DefaultConfig()
	clk := noonClock()

	// The everything-owed state:
	tasks := []Task{
		tk(1, StatusSeeded), // the crashed run's subject
		tk(10, StatusPackaged, decided(DecisionApproved, at(-40))), // approved: the apply is pending
		tk(20, StatusWorked),   // in-flight revision (packet 20 below)
		tk(30, StatusPackaged), // queued task (packet 30 below)
		tk(40, StatusVerified), // pipeline work waiting
		tk(50, StatusProposed), // proposal waiting
	}
	pkts := []Packet{pk(10), pk(20), pk(30)} // occupancy 3 = at cap
	rec := Records{VisionReady: true, Tasks: tasks, Packets: pkts}
	reapableLock := Lease{
		Held: true, RunID: "run-dead", SubjectID: pi64(1),
		AcquiredUptime: up(-5 * time.Minute), // never heartbeated → watchdog
		DeadlineUptime: up(time.Hour),
	}

	step := func(name string, rec Records, lock Lease, check func(Action)) {
		a := Decide(rec, lock, cfg, clk)
		assertWellFormed(t, a)
		check(a)
		// Determinism under repetition at every cascade state.
		if b := Decide(rec, lock, cfg, clk); !reflect.DeepEqual(a, b) {
			t.Fatalf("%s: nondeterministic decision:\n%+v\n%+v", name, a, b)
		}
	}

	// 1. Everything owed at once → the reap, and only the reap.
	step("reap", rec, reapableLock, func(a Action) {
		if a.Kind != KindReap || a.Reap.RunID != "run-dead" {
			t.Fatalf("many-obligation state must reap first, got %+v", a)
		}
	})

	// 2. Lease freed → the approved task holds the Project priority, and the
	// start request starts its merge.
	step("apply", rec, freeLock(), func(a Action) {
		if a.Kind != KindIdle || a.Idle != "priority-task-wait" {
			t.Fatalf("want the approval to hold unrelated agent runs, got %+v", a)
		}
	})
	if got := SelectStarts([]ProjectInput{{Records: rec}}, clk.Now, false); len(got) != 1 || got[0].Operation != OperationApply || got[0].TaskID != 10 {
		t.Fatalf("want the merge of task 10, got %+v", got)
	}

	// 3. The apply succeeded (task+packet archived; occupancy back to cap via
	// a packet on task 60): (2a) advances the in-flight revision.
	rec.Tasks[1].Archived = true
	rec.Packets[0].Archived = true
	rec.Tasks = append(rec.Tasks, tk(60, StatusPackaged))
	rec.Packets = append(rec.Packets, pk(60))
	step("2a", rec, freeLock(), func(a Action) {
		if a.Kind != KindSpawn || a.Spawn.Role != RoleVerifier || *a.Spawn.SubjectID != 20 {
			t.Fatalf("want Verifier(20) advancing in-flight revision, got %+v", a)
		}
	})

	// 4. The completed revision waits for review at the queue cap.
	rec.Tasks[2].Status = StatusPackaged
	step("idle", rec, freeLock(), func(a Action) {
		if a.Kind != KindIdle || a.Idle != IdleQueueFull {
			t.Fatalf("want idle(queue-full), got %+v", a)
		}
	})

	// 5. A packet archives (operator decided task 60) → room → step (3):
	// furthest-first picks the verified row over the seeded and proposed ones.
	rec.Packets[3].Archived = true // packet 60
	rec.Tasks[6].Archived = true   // task 60
	step("3", rec, freeLock(), func(a Action) {
		if a.Kind != KindSpawn || a.Spawn.Role != RolePackager || *a.Spawn.SubjectID != 40 {
			t.Fatalf("want Packager(40), got %+v", a)
		}
	})

	// 6. Pipeline drained (40, 50, 20, 30, 1 all gone) → propose fallback.
	for i := range rec.Tasks {
		rec.Tasks[i].Archived = true
	}
	for i := range rec.Packets {
		rec.Packets[i].Archived = true
	}
	step("4", rec, freeLock(), func(a Action) {
		if a.Kind != KindSpawn || a.Spawn.Role != RoleStrategist {
			t.Fatalf("want Strategist fallback, got %+v", a)
		}
	})
}
