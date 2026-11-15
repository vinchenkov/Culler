// Dispatch-retry budget suite: decrement, the exhaustion → blocked write in
// the same transaction, the clamp edge, and the two-budgets separation.
package domain_test

import (
	"context"
	"testing"

	"mc/domain"
)

func TestChargeInfra(t *testing.T) {
	t.Run("decrements_toward_zero", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")

		for want := 2; want >= 1; want-- {
			var res domain.ChargeResult
			mustTx(t, db, func(ctx context.Context, q domain.Q) error {
				var err error
				res, err = domain.ChargeInfra(ctx, q, id, "spawn-watchdog", "")
				return err
			})
			if res.Remaining != want || res.Blocked {
				t.Fatalf("charge → %+v, want remaining %d unblocked", res, want)
			}
			if got := taskInt(t, db, id, "dispatch_retries"); got != int64(want) {
				t.Fatalf("dispatch_retries = %d", got)
			}
		}
		if got := taskInt(t, db, id, "blocked"); got != 0 {
			t.Fatalf("blocked early")
		}
	})

	t.Run("exhaustion_stalls_same_transaction", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		mustExec(t, db, `UPDATE tasks SET dispatch_retries = 1 WHERE id = ?`, id)

		var res domain.ChargeResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			res, err = domain.ChargeInfra(ctx, q, id, "lease-timeout", "")
			return err
		})
		if res.Remaining != 0 || !res.Stalled || res.Blocked {
			t.Fatalf("charge → %+v, want remaining 0 and stalled", res)
		}
		if got := taskInt(t, db, id, "blocked"); got != 0 {
			t.Fatal("infrastructure failure created an operator block")
		}
		if got := taskInt(t, db, id, "stalled"); got != 1 {
			t.Fatal("task did not stall")
		}
		if got := taskStr(t, db, id, "blocked_reason"); got != "<NULL>" {
			t.Fatalf("blocked_reason = %q", got)
		}
	})

	// The clamp edge, and never a silent loop: a charge against an
	// already-zero budget stays 0 — the CHECK (dispatch_retries >= 0)
	// backstop is never provoked — and still blocks.
	t.Run("charge_at_zero_clamps_and_stalls", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		mustExec(t, db, `UPDATE tasks SET dispatch_retries = 0 WHERE id = ?`, id)

		var res domain.ChargeResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			res, err = domain.ChargeInfra(ctx, q, id, "hard-deadline", "")
			return err
		})
		if res.Remaining != 0 || !res.Stalled || res.Blocked {
			t.Fatalf("charge at zero → %+v, want clamped 0 and stalled", res)
		}
		// Backstop agreement: the raw negative write aborts.
		wantAbort(t, db, `UPDATE tasks SET dispatch_retries = -1 WHERE id = ?`, id)
	})

	t.Run("already_blocked_keeps_original_reason", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "seeded")
		mustExec(t, db, `UPDATE tasks SET dispatch_retries = 1, blocked = 1, blocked_kind = 'operator', blocked_reason = 'operator question' WHERE id = ?`, id)

		var res domain.ChargeResult
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			var err error
			res, err = domain.ChargeInfra(ctx, q, id, "spawn-watchdog", "")
			return err
		})
		if res.Blocked {
			t.Fatalf("charge reported a fresh block on an already-blocked subject")
		}
		if got := taskStr(t, db, id, "blocked_reason"); got != "operator question" {
			t.Fatalf("blocked_reason = %q, want the original untouched", got)
		}
	})

	// Two budgets never blur: an infra charge leaves
	// correction_count untouched — including mid-rally.
	t.Run("never_touches_correction_count", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "worked")
		mustExec(t, db, `UPDATE tasks SET correction_count = 2 WHERE id = ?`, id)

		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			_, err := domain.ChargeInfra(ctx, q, id, "spawn-watchdog", "")
			return err
		})
		if got := taskInt(t, db, id, "correction_count"); got != 2 {
			t.Fatalf("correction_count = %d, want 2 untouched: the two budgets are separate", got)
		}
	})

	t.Run("missing_task", func(t *testing.T) {
		db := openSpine(t)
		wantCode(t, db, domain.CodeNotFound, func(ctx context.Context, q domain.Q) error {
			_, err := domain.ChargeInfra(ctx, q, 999, "r", "")
			return err
		})
	})
}
