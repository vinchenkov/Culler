package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"mc/dispatch"
)

// dvOversizeTask makes one proposed row whose description alone cannot fit in a
// dispatch effect, which is what an accumulated board does to the Arbiter brief.
func dvOversizeTask(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	dvInsertTask(t, db, dvTask(id, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `UPDATE tasks SET description = ? WHERE id = ?`,
		strings.Repeat("overlap prose. ", maxEffectBytes/10), id)
}

func dvOversizeCommit(t *testing.T, db *sql.DB, requestID string) map[string]any {
	t.Helper()
	prepared := dfPrepare(t, db, requestID)
	if prepared.final != nil {
		return prepared.final
	}
	attested, err := dispatchAttest(os.Getenv("MC_HOME"), prepared)
	if err != nil {
		t.Fatal(err)
	}
	return dfCommit(t, db, prepared, attested)
}

func TestDispatchRefusesAnEffectTooLargeToRecordOrDeliver(t *testing.T) {
	db := dvSpine(t)
	dvOversizeTask(t, db, 1)

	effect := dvOversizeCommit(t, db, dfRequestID)

	if effect["action"] != "refused" {
		t.Fatalf("effect = %v, want a refusal rather than an uncommittable spawn", effect)
	}
	// A stale verdict means "read again and decide again". Nothing moved here,
	// so a stale verdict repeats the same decision every tick. Only the health
	// class carries the budget that ends the loop.
	if effect["class"] != "health" {
		t.Errorf("class = %v, want health: only health carries the budget that ends the loop", effect["class"])
	}
	if effect["code"] != "health.effect_oversize" {
		t.Errorf("code = %v, want health.effect_oversize", effect["code"])
	}
	if effect["consequence"] != "health" {
		t.Errorf("consequence = %v, want health", effect["consequence"])
	}

	// The refusal comes before the claim. A refusal after the claim leaves the
	// lease with a run whose container never starts, and the spawn watchdog
	// then charges the task for it.
	if n := dfInt(t, db, `SELECT COUNT(*) FROM runs`); n != 0 {
		t.Errorf("refusal opened %d runs, want none", n)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM leases WHERE run_id IS NOT NULL`); n != 0 {
		t.Errorf("refusal claimed the lease")
	}
	if r := dfInt(t, db, `SELECT dispatch_retries FROM tasks WHERE id = 1`); r != 3 {
		t.Errorf("dispatch_retries = %d, want 3 untouched: this is the deployment's fault, not the task's", r)
	}

	if n := dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind = 'dispatch.health'`); n != 1 {
		t.Errorf("dispatch.health rows = %d, want exactly one", n)
	}
	// The health budget bounds the repetition.
	if h := dfInt(t, db, `SELECT 3-infrastructure_failure_count FROM projects WHERE id='ws-test'`); h != 2 {
		t.Errorf("Project infrastructure remaining = %d, want 2 after one charge", h)
	}
}

func TestOversizeEffectsExhaustProjectInfrastructure(t *testing.T) {
	db := dvSpine(t)
	dvOversizeTask(t, db, 1)
	for _, requestID := range []string{"a1a1a1a1a1a1a1a1", "b2b2b2b2b2b2b2b2", "c3c3c3c3c3c3c3c3"} {
		dvExec(t, db, `UPDATE projects SET infrastructure_next_retry_at=NULL WHERE id='ws-test'`)
		if effect := dvOversizeCommit(t, db, requestID); effect["action"] != "refused" {
			t.Fatalf("effect: %v", effect)
		}
	}
	if dfInt(t, db, `SELECT infrastructure_failure_count FROM projects WHERE id='ws-test'`) != 3 {
		t.Fatal("The Project allowance did not stop repeated refusals")
	}
	if dfInt(t, db, `SELECT blocked OR stalled FROM tasks WHERE id=1`) != 0 {
		t.Fatal("The Project refusal changed task conditions")
	}
	if dfInt(t, db, `SELECT COUNT(*) FROM runs`) != 0 {
		t.Fatal("The refusals opened a run")
	}
	effect := dvOversizeCommit(t, db, "d4d4d4d4d4d4d4d4")
	if effect["action"] != "idle" {
		t.Fatalf("The exhausted Project still dispatches: %v", effect)
	}
}

func TestAnEffectThatFitsIsUnaffected(t *testing.T) {
	// The guard on the guard: an ordinary spawn must not pay for this.
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))

	effect := dvOversizeCommit(t, db, dfRequestID)

	if effect["action"] != "spawn" {
		t.Fatalf("effect = %v, want an ordinary spawn", effect)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM runs`); n != 1 {
		t.Errorf("runs = %d, want one", n)
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM activity WHERE kind = 'dispatch.health'`); n != 0 {
		t.Errorf("an ordinary spawn wrote %d health rows, want none", n)
	}
}

// The receipt column and the wire have separate limits. The frame bounds the
// wire. The column CHECK bounds the receipt. A receipt that cannot hold the
// whole effect drops the members that exist elsewhere instead of refusing the
// decision.
func TestAnEffectTooLargeToStoreWholeStillDispatches(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	// The description is too large for the receipt and small enough for the frame.
	dvExec(t, db, `UPDATE tasks SET description = ? WHERE id = ?`,
		strings.Repeat("overlap prose. ", maxReceiptBytes/10), int64(1))

	effect := dvOversizeCommit(t, db, dfRequestID)

	if effect["action"] != "spawn" {
		t.Fatalf("effect = %v, want a spawn: this size is deliverable", effect)
	}
	// The agent still gets its whole input. Nothing is truncated on the way out.
	if brief, ok := effect["brief"].(string); !ok || len(brief) < maxReceiptBytes {
		t.Fatalf("the delivered effect lost its brief; the resident would refuse this spawn")
	}
	if n := dfInt(t, db, `SELECT COUNT(*) FROM runs`); n != 1 {
		t.Errorf("runs = %d, want one", n)
	}

	var stored string
	if err := db.QueryRow(
		`SELECT dispatch_result FROM activity WHERE dispatch_request_id = ?`, dfRequestID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) > maxReceiptBytes {
		t.Fatalf("stored receipt is %d bytes, over the column's own CHECK", len(stored))
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(stored), &receipt); err != nil {
		t.Fatal(err)
	}
	// Only the bodies that the next tick can generate again go. Every decision
	// member stays, so the receipt still records what was decided.
	for _, gone := range []string{"brief", "skills"} {
		if _, present := receipt[gone]; present {
			t.Errorf("receipt kept %q; it did not need to", gone)
		}
	}
	for _, kept := range []string{"action", "run_id", "role", "mount_plan", "runtime_binding"} {
		if _, present := receipt[kept]; !present {
			t.Errorf("receipt dropped %q, which is decision state and not regenerable", kept)
		}
	}
	if _, marked := receipt["receipt_elided"]; !marked {
		t.Error("an elided receipt is not marked; the replay path cannot tell it is incomplete")
	}
}

func TestAnElidedReceiptRefusesToAnswerAReplay(t *testing.T) {
	// An incomplete stored result is a protocol error, never a permission to
	// execute again. A spawn without its brief launches an agent without
	// instructions, so the replay refuses and states the reason. The run holds
	// the lease, the spawn watchdog reaps it, and the next tick decides again.
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	dvExec(t, db, `UPDATE tasks SET description = ? WHERE id = ?`,
		strings.Repeat("overlap prose. ", maxReceiptBytes/10), int64(1))
	if effect := dvOversizeCommit(t, db, dfRequestID); effect["action"] != "spawn" {
		t.Fatalf("setup: effect = %v, want spawn", effect)
	}

	_, _, err := lookupDispatchReceipt(context.Background(), db, dfRequestID)
	if err == nil {
		t.Fatal("an elided receipt answered a replay; the resident would spawn without a brief")
	}
	if !strings.Contains(err.Error(), "without its brief") {
		t.Errorf("refusal = %v, want it to name the missing brief", err)
	}
}

func TestAnOrdinaryEffectStillReplaysByteForByte(t *testing.T) {
	// The degradation applies only to an effect that cannot fit. An ordinary
	// effect replays byte for byte.
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusProposed, 2))
	effect := dvOversizeCommit(t, db, dfRequestID)
	if effect["action"] != "spawn" {
		t.Fatalf("effect = %v, want spawn", effect)
	}
	dfAssertExactReplay(t, db, dfRequestID, effect)
}
