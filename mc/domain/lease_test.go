// Lease suite: CAS claim, fenced heartbeat that
// never extends the hard deadline, fenced release, the reap write
// side, and zombie-vs-new-holder at the transaction grain.
package domain_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"mc/domain"
)

var testNow = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

func claim(t *testing.T, db *sql.DB, a domain.ClaimArgs) error {
	t.Helper()
	return tx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.Claim(ctx, q, testNow, a)
		return err
	})
}

func claimStarted(t *testing.T, db *sql.DB, a domain.ClaimArgs) error {
	if err := claim(t, db, a); err != nil {
		return err
	}
	_, err := db.Exec(`UPDATE runs SET agent_started_at=datetime('now') WHERE id=?`, a.RunID)
	return err
}

// lockStr reads one column of the fixture Project's lease row.
func lockStr(t *testing.T, db *sql.DB, col string) string {
	t.Helper()
	return oneStr(t, db, `SELECT `+col+` FROM leases WHERE project = 'ws'`)
}

func TestClaim(t *testing.T) {
	t.Run("stamps_lease_and_runs_row", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		var res domain.ClaimResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			res, err = domain.Claim(ctx, q, testNow, domain.ClaimArgs{
				Project: "ws", RunID: "r1", Owner: "worker", SubjectID: &id,
				SessionPath: "sessions/r1", Binding: "fake",
				HardDeadlineMinutes: 240,
			})
			return err
		})
		if res.Project != "ws" {
			t.Fatalf("project = %q, want the claimed Project", res.Project)
		}
		if got := lockStr(t, db, "run_id"); got != "r1" {
			t.Fatalf("run_id = %q", got)
		}
		if got := lockStr(t, db, "owner"); got != "worker" {
			t.Fatalf("owner = %q", got)
		}
		// acquired_at is the injected clock. The limits count VM uptime:
		// the deadline is the claim uptime plus hard_deadline_minutes.
		if got := lockStr(t, db, "acquired_at"); got != "2026-07-12 10:00:00" {
			t.Fatalf("acquired_at = %q", got)
		}
		vm, err := domain.ReadVMClock()
		if err != nil {
			t.Fatal(err)
		}
		if got := lockStr(t, db, "boot_id"); got != vm.BootID || got == "" {
			t.Fatalf("boot_id = %q, want %q", got, vm.BootID)
		}
		if got := oneStr(t, db, `SELECT deadline_uptime_ms - acquired_uptime_ms FROM leases WHERE project = 'ws'`); got != "14400000" {
			t.Fatalf("deadline - claim = %s ms, want 240m", got)
		}
		if got := oneStr(t, db, `SELECT acquired_uptime_ms <= ? FROM leases WHERE project = 'ws'`, vm.Uptime.Milliseconds()); got != "1" {
			t.Fatalf("the claim uptime is after the current uptime")
		}
		for _, col := range []string{"last_heartbeat_at", "heartbeat_uptime_ms"} {
			if got := lockStr(t, db, col); got != "<NULL>" {
				t.Fatalf("%s = %q, want NULL at claim (the watchdog's case)", col, got)
			}
		}
		// The runs row exists in the same transaction.
		if got := oneStr(t, db, `SELECT role || '/' || binding FROM runs WHERE id = 'r1'`); got != "worker/fake" {
			t.Fatalf("runs row = %q", got)
		}
	})

	t.Run("arbiter_pool_snapshot_stamped", func(t *testing.T) {
		db := openSpine(t)
		a := mkTask(t, db, "task", "proposed")
		b := mkTask(t, db, "task", "proposed")
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			_, err := domain.Claim(ctx, q, testNow, domain.ClaimArgs{
				Project: "ws", RunID: "e1", Owner: "arbiter", SubjectID: &a,
				PoolSnapshot: []int64{a, b}, HardDeadlineMinutes: 240,
			})
			return err
		})
		want := `[` + itoa(a) + `,` + itoa(b) + `]`
		if got := oneStr(t, db, `SELECT pool_snapshot FROM runs WHERE id = 'e1'`); got != want {
			t.Fatalf("pool_snapshot = %q, want %q", got, want)
		}
	})

	// CAS single-winner at the domain grain: a claim against a
	// held lock aborts before any write lands — no runs row for the loser.
	t.Run("claim_against_held_lock_aborts_before_writes", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		if err := claim(t, db, domain.ClaimArgs{
			Project: "ws", RunID: "winner", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
		}); err != nil {
			t.Fatalf("first claim: %v", err)
		}
		wantCode(t, db, domain.CodeLeaseHeld, func(ctx context.Context, q domain.Q) error {
			_, err := domain.Claim(ctx, q, testNow, domain.ClaimArgs{
				Project: "ws", RunID: "loser", Owner: "arbiter", HardDeadlineMinutes: 240,
			})
			return err
		})
		if got := oneInt(t, db, `SELECT COUNT(*) FROM runs`); got != 1 {
			t.Fatalf("runs rows = %d, want 1 (the loser must not open a run)", got)
		}
		if got := lockStr(t, db, "run_id"); got != "winner" {
			t.Fatalf("run_id = %q, want the winner untouched", got)
		}
	})

	t.Run("concurrent_transaction_claimants_have_one_cas_winner", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		const claimants = 4
		start := make(chan struct{})
		results := make(chan error, claimants)
		var ready sync.WaitGroup
		ready.Add(claimants)
		for i := 0; i < claimants; i++ {
			go func(i int) {
				ctx := context.Background()
				conn, err := db.Conn(ctx)
				if err != nil {
					ready.Done()
					results <- err
					return
				}
				defer conn.Close()
				ready.Done()
				<-start
				if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
					results <- err
					return
				}
				_, err = domain.Claim(ctx, conn, testNow, domain.ClaimArgs{
					Project: "ws", RunID: fmt.Sprintf("racer-%d", i), Owner: "worker",
					SubjectID: &id, HardDeadlineMinutes: 240,
				})
				if err != nil {
					_, _ = conn.ExecContext(ctx, "ROLLBACK")
					results <- err
					return
				}
				_, err = conn.ExecContext(ctx, "COMMIT")
				results <- err
			}(i)
		}
		ready.Wait()
		close(start)

		winners, losers := 0, 0
		for i := 0; i < claimants; i++ {
			err := <-results
			if err == nil {
				winners++
				continue
			}
			var de *domain.DomainError
			if errors.As(err, &de) && de.Code == domain.CodeLeaseHeld {
				losers++
				continue
			}
			t.Fatalf("claimant returned non-CAS error: %v", err)
		}
		if winners != 1 || losers != claimants-1 {
			t.Fatalf("winners/losers = %d/%d, want 1/%d", winners, losers, claimants-1)
		}
		if got := oneInt(t, db, `SELECT COUNT(*) FROM runs`); got != 1 {
			t.Fatalf("runs rows = %d, want exactly one committed claimant", got)
		}
		// The lease's holder and the single committed Run are the same racer —
		// a lease/runs mismatch would otherwise pass the counts above.
		if got := oneInt(t, db, `SELECT COUNT(*) FROM leases
			WHERE project = 'ws' AND run_id = (SELECT id FROM runs)`); got != 1 {
			t.Fatalf("leases.run_id does not match the committed runs row")
		}
	})

	// A subjectless run (propose) holds
	// the lease of its Project with no subject; runs.project is set.
	t.Run("subjectless_claim_binds_the_project", func(t *testing.T) {
		db := openSpine(t)
		var res domain.ClaimResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			res, err = domain.Claim(ctx, q, testNow, domain.ClaimArgs{
				Project: "ws", RunID: "s1", Owner: "strategist", HardDeadlineMinutes: 240,
			})
			return err
		})
		if res.Project != "ws" {
			t.Fatalf("project = %q, want the claimed Project", res.Project)
		}
		if got := lockStr(t, db, "subject"); got != "<NULL>" {
			t.Fatalf("subject = %q", got)
		}
		if got := lockStr(t, db, "run_id"); got != "s1" {
			t.Fatalf("run_id = %q, want the subjectless holder", got)
		}
		if got := oneStr(t, db, `SELECT project FROM runs WHERE id = 's1'`); got != "ws" {
			t.Fatalf("runs.project = %q, want ws", got)
		}
	})

	// The CAS is on the lease row of ONE
	// Project. Two Projects are held at once; a claim on a held
	// Project is refused while the other Project's claim succeeds.
	t.Run("claims_are_per_project", func(t *testing.T) {
		db := openSpine(t)
		mkProject(t, db, "ws2")
		id := mkTask(t, db, "task", "seeded")
		if err := claim(t, db, domain.ClaimArgs{
			Project: "ws", RunID: "a1", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
		}); err != nil {
			t.Fatalf("claim ws: %v", err)
		}
		// The other Project's claim does not wait for, and is not blocked
		// by, the held lease of ws.
		if err := claim(t, db, domain.ClaimArgs{
			Project: "ws2", RunID: "b1", Owner: "strategist", HardDeadlineMinutes: 240,
		}); err != nil {
			t.Fatalf("claim ws2 while ws is held: %v", err)
		}
		if got := oneInt(t, db, `SELECT COUNT(*) FROM leases WHERE run_id IS NOT NULL`); got != 2 {
			t.Fatalf("held leases = %d, want both Projects held at once", got)
		}
		// A second claim on either held Project loses the CAS.
		wantCode(t, db, domain.CodeLeaseHeld, func(ctx context.Context, q domain.Q) error {
			_, err := domain.Claim(ctx, q, testNow, domain.ClaimArgs{
				Project: "ws2", RunID: "b2", Owner: "arbiter", HardDeadlineMinutes: 240,
			})
			return err
		})
		wantCode(t, db, domain.CodeLeaseHeld, func(ctx context.Context, q domain.Q) error {
			_, err := domain.Claim(ctx, q, testNow, domain.ClaimArgs{
				Project: "ws", RunID: "a2", Owner: "arbiter", HardDeadlineMinutes: 240,
			})
			return err
		})
		if got := oneInt(t, db, `SELECT COUNT(*) FROM runs`); got != 2 {
			t.Fatalf("runs rows = %d, want one per winner", got)
		}
		// Releasing one Project's lease leaves the other held.
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Release(ctx, q, "a1")
		})
		if got := lockStr(t, db, "run_id"); got != "<NULL>" {
			t.Fatalf("ws lease after release = %q", got)
		}
		if got := oneStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws2'`); got != "b1" {
			t.Fatalf("ws2 lease after ws release = %q, want b1 untouched", got)
		}
	})

	// A claim must name a Project, and a subject task must belong to it.
	t.Run("claim_requires_the_subjects_project", func(t *testing.T) {
		db := openSpine(t)
		mkProject(t, db, "ws2")
		id := mkTask(t, db, "task", "seeded")
		wantCode(t, db, domain.CodeProjectMismatch, func(ctx context.Context, q domain.Q) error {
			_, err := domain.Claim(ctx, q, testNow, domain.ClaimArgs{
				Project: "ws2", RunID: "x1", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
			})
			return err
		})
		wantCode(t, db, domain.CodeProjectMismatch, func(ctx context.Context, q domain.Q) error {
			_, err := domain.Claim(ctx, q, testNow, domain.ClaimArgs{
				RunID: "x2", Owner: "strategist", HardDeadlineMinutes: 240,
			})
			return err
		})
		if got := oneInt(t, db, `SELECT COUNT(*) FROM leases WHERE run_id IS NOT NULL`); got != 0 {
			t.Fatalf("held leases after refused claims = %d, want 0", got)
		}
	})
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func TestHeartbeat(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	if err := claim(t, db, domain.ClaimArgs{
		Project: "ws", RunID: "r1", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
	}); err != nil {
		t.Fatal(err)
	}

	deadlineBefore := lockStr(t, db, "deadline_uptime_ms")
	var stamped string
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		stamped, err = domain.Heartbeat(ctx, q, "r1")
		return err
	})
	if stamped == "" {
		t.Fatalf("heartbeat returned no stamp")
	}
	if got := lockStr(t, db, "last_heartbeat_at"); got != stamped {
		t.Fatalf("last_heartbeat_at = %q, want %q", got, stamped)
	}
	if got := oneStr(t, db, `SELECT heartbeat_uptime_ms >= acquired_uptime_ms FROM leases WHERE project = 'ws'`); got != "1" {
		t.Fatalf("heartbeat uptime = %q, want a stamp at or after the claim", lockStr(t, db, "heartbeat_uptime_ms"))
	}
	// Asserted on the column: no heartbeat can extend the deadline.
	if got := lockStr(t, db, "deadline_uptime_ms"); got != deadlineBefore {
		t.Fatalf("heartbeat moved deadline_uptime_ms %q → %q", deadlineBefore, got)
	}

	// Fenced: a non-holder is rejected and stamps nothing.
	before := lockStr(t, db, "last_heartbeat_at")
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		_, err := domain.Heartbeat(ctx, q, "not-the-run")
		return err
	})
	if got := lockStr(t, db, "last_heartbeat_at"); got != before {
		t.Fatalf("stale heartbeat moved the stamp")
	}
}

func TestFenceAndRelease(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	if err := claim(t, db, domain.ClaimArgs{
		Project: "ws", RunID: "r1", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
	}); err != nil {
		t.Fatal(err)
	}

	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		subject, err := domain.Fence(ctx, q, "r1")
		if err != nil {
			return err
		}
		if subject == nil || *subject != id {
			t.Fatalf("fence subject = %v, want %d", subject, id)
		}
		return nil
	})
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		_, err := domain.Fence(ctx, q, "stale")
		return err
	})
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		return domain.Release(ctx, q, "stale")
	})

	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		return domain.Release(ctx, q, "r1")
	})
	for _, col := range []string{"run_id", "owner", "subject",
		"acquired_at", "last_heartbeat_at", "boot_id", "acquired_uptime_ms",
		"heartbeat_uptime_ms", "deadline_uptime_ms"} {
		if got := lockStr(t, db, col); got != "<NULL>" {
			t.Fatalf("released lease.%s = %q, want NULL (no run residue)", col, got)
		}
	}
	// Release is single-shot: the second release is fenced off.
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		return domain.Release(ctx, q, "r1")
	})
}

func TestApplyReap(t *testing.T) {
	t.Run("marks_charges_retains_until_stop", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		if err := claimStarted(t, db, domain.ClaimArgs{
			Project: "ws", RunID: "r1", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
		}); err != nil {
			t.Fatal(err)
		}
		var res domain.ReapResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			res, err = domain.ApplyReap(ctx, q, domain.ReapArgs{
				RunID: "r1", Reason: "spawn-watchdog",
			})
			return err
		})
		if !res.Charged || res.Blocked {
			t.Fatalf("reap = %+v, want charged unblocked", res)
		}
		if got := oneStr(t, db, `SELECT outcome FROM runs WHERE id = 'r1'`); got != "reaped" {
			t.Fatalf("outcome = %q", got)
		}
		if got := taskInt(t, db, id, "dispatch_retries"); got != 2 {
			t.Fatalf("dispatch_retries = %d, want charged once", got)
		}
		if got := lockStr(t, db, "run_id"); got != "r1" {
			t.Fatalf("reap released lease before the stop: %s", got)
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, "r1") })
		if got := lockStr(t, db, "run_id"); got != "<NULL>" {
			t.Fatalf("acknowledgment retained lease: %s", got)
		}
	})

	t.Run("exhaustion_stalls_in_same_transaction", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		mustExec(t, db, `UPDATE tasks SET dispatch_retries = 1 WHERE id = ?`, id)
		if err := claimStarted(t, db, domain.ClaimArgs{
			Project: "ws", RunID: "r1", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
		}); err != nil {
			t.Fatal(err)
		}
		var res domain.ReapResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			res, err = domain.ApplyReap(ctx, q, domain.ReapArgs{
				RunID: "r1", Reason: "lease-timeout",
			})
			return err
		})
		if !res.Stalled || res.Blocked {
			t.Fatalf("reap = %+v, want stalled", res)
		}
		if got := taskInt(t, db, id, "blocked"); got != 0 {
			t.Fatal("reap created an operator block")
		}
		if got := taskInt(t, db, id, "stalled"); got != 1 {
			t.Fatal("task did not stall")
		}
	})

	t.Run("exhaustion_preserves_the_failure_record", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		mustExec(t, db, `UPDATE tasks SET dispatch_retries = 1 WHERE id = ?`, id)
		if err := claimStarted(t, db, domain.ClaimArgs{Project: "ws", RunID: "lost", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240}); err != nil {
			t.Fatal(err)
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			_, err := domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "lost", Reason: "container-gone"})
			return err
		})
		if got := oneStr(t, db, `SELECT condition || ':' || source || ':' || cause FROM infrastructure_failures WHERE run_id = 'lost'`); got != "container-gone:resident:unknown" {
			t.Fatalf("record = %s", got)
		}
		if got := taskStr(t, db, id, "blocked_reason"); got != "<NULL>" {
			t.Fatalf("blocked reason = %s", got)
		}
	})

	// The branch a naive "append the run id" change breaks. Reaping a
	// subjectless run "marks it reaped, frees the lock, and emits the stop
	// effect while charging nothing" — so there is no task to block and no
	// reason to write.
	t.Run("subjectless_reap_preserves_task_state", func(t *testing.T) {
		db := openSpine(t)
		if err := claimStarted(t, db, domain.ClaimArgs{
			Project: "ws", RunID: "r-propose", Owner: "strategist", HardDeadlineMinutes: 240,
		}); err != nil {
			t.Fatal(err)
		}
		var res domain.ReapResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			res, err = domain.ApplyReap(ctx, q, domain.ReapArgs{
				RunID: "r-propose", Reason: "lease-timeout",
			})
			return err
		})
		if !res.Charged || res.Blocked {
			t.Fatalf("The proposal failure must charge only the Strategist allowance: %+v", res)
		}
		if n := oneStr(t, db, `SELECT COUNT(*) FROM tasks WHERE blocked = 1`); n != "0" {
			t.Fatalf("a subjectless reap blocked %s task(s)", n)
		}
	})

	t.Run("subjectless_reap_charges_only_strategist", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded") // an unrelated row that must stay uncharged
		if err := claimStarted(t, db, domain.ClaimArgs{
			Project: "ws", RunID: "s1", Owner: "strategist", HardDeadlineMinutes: 240,
		}); err != nil {
			t.Fatal(err)
		}
		var res domain.ReapResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			res, err = domain.ApplyReap(ctx, q, domain.ReapArgs{RunID: "s1", Reason: "spawn-watchdog"})
			return err
		})
		if !res.Charged {
			t.Fatalf("The proposal failure must charge the Strategist allowance.")
		}
		if got := taskInt(t, db, id, "dispatch_retries"); got != 3 {
			t.Fatalf("dispatch_retries = %d, want 3 untouched", got)
		}
	})

	t.Run("subject_charge_is_derived_from_fenced_lease", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		other := mkTask(t, db, "task", "seeded")
		if err := claimStarted(t, db, domain.ClaimArgs{
			Project: "ws", RunID: "authoritative", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
		}); err != nil {
			t.Fatal(err)
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			_, err := domain.ApplyReap(ctx, q, domain.ReapArgs{
				RunID: "authoritative", Reason: "lease-timeout",
			})
			return err
		})
		if got := taskInt(t, db, id, "dispatch_retries"); got != 2 {
			t.Fatalf("leased subject dispatch_retries = %d, want charged once", got)
		}
		if got := taskInt(t, db, other, "dispatch_retries"); got != 3 {
			t.Fatalf("unrelated subject dispatch_retries = %d, want untouched", got)
		}
	})

	// Two budgets: reaping a verifier lease
	// mid-rally charges dispatch_retries, never correction_count.
	t.Run("verifier_reap_leaves_rally_budget_untouched", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "worked")
		mustExec(t, db, `UPDATE tasks SET correction_count = 2 WHERE id = ?`, id)
		if err := claimStarted(t, db, domain.ClaimArgs{
			Project: "ws", RunID: "v1", Owner: "verifier", SubjectID: &id, HardDeadlineMinutes: 240,
		}); err != nil {
			t.Fatal(err)
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			_, err := domain.ApplyReap(ctx, q, domain.ReapArgs{
				RunID: "v1", Reason: "hard-deadline",
			})
			return err
		})
		if got := taskInt(t, db, id, "correction_count"); got != 2 {
			t.Fatalf("correction_count = %d, want 2 untouched (two budgets)", got)
		}
		if got := taskInt(t, db, id, "dispatch_retries"); got != 2 {
			t.Fatalf("dispatch_retries = %d, want charged once", got)
		}
	})
}

// Zombie-vs-new-holder: after reap +
// re-claim, the old run's writes are rejected and the new lease is
// bit-for-bit untouched.
func TestZombieVersusNewHolder(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	if err := claimStarted(t, db, domain.ClaimArgs{
		Project: "ws", RunID: "zombie", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
	}); err != nil {
		t.Fatal(err)
	}
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		_, err := domain.ApplyReap(ctx, q, domain.ReapArgs{
			RunID: "zombie", Reason: "lease-timeout",
		})
		return err
	})
	mustTx(t, db, func(ctx context.Context, q domain.Q) error { return domain.AcknowledgeRunStop(ctx, q, "zombie") })
	if err := claimStarted(t, db, domain.ClaimArgs{
		Project: "ws", RunID: "fresh", Owner: "worker", SubjectID: &id, HardDeadlineMinutes: 240,
	}); err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		return oneStr(t, db, `
			SELECT run_id || '|' || owner || '|' || COALESCE(subject, -1) || '|' ||
			       acquired_at || '|' || COALESCE(last_heartbeat_at, 'null') || '|' ||
			       boot_id || '|' || acquired_uptime_ms || '|' ||
			       COALESCE(heartbeat_uptime_ms, 'null') || '|' || deadline_uptime_ms
			FROM leases WHERE project = 'ws'`)
	}
	before := snapshot()

	// The zombie's heartbeat, release, and fence are all rejected…
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		_, err := domain.Heartbeat(ctx, q, "zombie")
		return err
	})
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		return domain.Release(ctx, q, "zombie")
	})
	wantCode(t, db, domain.CodeStaleRun, func(ctx context.Context, q domain.Q) error {
		_, err := domain.Fence(ctx, q, "zombie")
		return err
	})

	// …and the new holder's lease is bit-for-bit untouched.
	if after := snapshot(); after != before {
		t.Fatalf("zombie writes disturbed the new lease:\n  before %s\n  after  %s", before, after)
	}
}
