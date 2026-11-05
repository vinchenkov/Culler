// Review-queue suite: the per-Project occupancy read, the cap at both layers,
// and the selection order at the cap. The suite drives mc/dispatch.Decide as
// a black-box oracle over states that domain operations built.
package domain_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"mc/dispatch"
	"mc/domain"
)

func occupancy(t *testing.T, db *sql.DB) int {
	t.Helper()
	return occupancyOf(t, db, "ws")
}

func occupancyOf(t *testing.T, db *sql.DB, project string) int {
	t.Helper()
	var n int
	mustTx(t, db, func(ctx context.Context, q domain.Q) error {
		var err error
		n, err = domain.Occupancy(ctx, q, project)
		return err
	})
	return n
}

func TestOccupancy(t *testing.T) {
	t.Run("counts_unarchived_only", func(t *testing.T) {
		db := openSpine(t)
		a := mkTask(t, db, "task", "packaged")
		b := mkTask(t, db, "task", "packaged")
		mkPacket(t, db, a)
		mkPacket(t, db, b)
		if got := occupancy(t, db); got != 2 {
			t.Fatalf("occupancy = %d, want 2", got)
		}
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Cancel(ctx, q, a, "drop", "operator") // the cascade archives the packet
		})
		if got := occupancy(t, db); got != 1 {
			t.Fatalf("occupancy = %d, want 1 after archive", got)
		}
	})

	// The cap applies to each Project. A Project with room keeps producing
	// packets while another Project sits at its cap.
	t.Run("per_project", func(t *testing.T) {
		db := openSpine(t)
		mkProject(t, db, "ws2")
		mkProject(t, db, "ws3")
		for _, ws := range []string{"ws", "ws2", "ws3"} {
			mkPacket(t, db, mkTaskIn(t, db, ws, "task", "packaged"))
		}
		for _, ws := range []string{"ws", "ws2", "ws3"} {
			if got := occupancyOf(t, db, ws); got != 1 {
				t.Fatalf("occupancy(%s) = %d, want 1 for each Project", ws, got)
			}
		}
		// Three packets across three Projects cap nobody. ws has room for two
		// more. Its fourth packet is refused at both layers, and the next ws2
		// birth commits.
		mkPacket(t, db, mkTask(t, db, "task", "packaged"))
		mkPacket(t, db, mkTask(t, db, "task", "packaged"))
		if got := occupancyOf(t, db, "ws"); got != 3 {
			t.Fatalf("occupancy(ws) = %d, want 3", got)
		}
		fourth := mkTask(t, db, "task", "packaged")
		wantCode(t, db, domain.CodeWIPCap, func(ctx context.Context, q domain.Q) error {
			return publishReviewFixture(ctx, q, fourth, "r.html", "thesis")
		})
		seedReviewVersion(t, db, fourth)
		wantAbort(t, db, `UPDATE tasks SET current_review_id=(SELECT id FROM review_versions WHERE task_id=tasks.id AND version_id=tasks.submitted_version),review_slot_held=1 WHERE id=?`, fourth)
		other := mkTaskIn(t, db, "ws2", "task", "packaged")
		if err := birthPacket(t, db, other, "r.html"); err != nil {
			t.Fatalf("ws2 birth while ws is at cap: %v", err)
		}
		if got := occupancyOf(t, db, "ws2"); got != 2 {
			t.Fatalf("occupancy(ws2) = %d, want 2", got)
		}
	})
}

// loadProjection reads the stored state of the fixture Project into the
// dispatch records. The oracle's input is the spine rows themselves.
func loadProjection(t *testing.T, db *sql.DB) dispatch.Records {
	t.Helper()
	rec := dispatch.Records{Project: "ws", VisionReady: true}
	rows, err := db.Query(`
		SELECT t.id, t.title, t.scope, t.priority, t.created_at, t.status,
		       t.blocked, t.dispatch_retries, t.decision, t.decided_at, t.archived,
		       t.project, w.status,
		       d.correction_open, COALESCE(d.candidate_id, 0),
		       COALESCE(c.status = 'merged', 0)
		FROM tasks t JOIN projects w ON w.id = t.project
		JOIN tasks d ON d.id = t.id
		LEFT JOIN merge_candidates c ON c.id = d.candidate_id
		WHERE t.project = 'ws'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			tk                       dispatch.Task
			scope, status, createdAt string
			blocked, archived        int
			decision, decidedAt      sql.NullString
		)
		if err := rows.Scan(&tk.ID, &tk.Title, &scope, &tk.Priority,
			&createdAt, &status, &blocked, &tk.DispatchRetries, &decision,
			&decidedAt, &archived, &tk.Project, &tk.ProjectStatus,
			&tk.Delivery.CorrectionOpen, &tk.Delivery.CandidateID,
			&tk.Delivery.CandidateMerged); err != nil {
			t.Fatal(err)
		}
		tk.Scope = dispatch.Scope(scope)
		tk.Status = dispatch.Status(status)
		tk.Blocked = blocked == 1
		tk.Stalled = tk.DispatchRetries == 0
		tk.Archived = archived == 1
		if tk.CreatedAt, err = domain.ParseSpineTime(createdAt); err != nil {
			t.Fatal(err)
		}
		if decision.Valid {
			tk.Decision = dispatch.TaskDecision(decision.String)
		}
		if decidedAt.Valid {
			d, err := domain.ParseSpineTime(decidedAt.String)
			if err != nil {
				t.Fatal(err)
			}
			tk.DecidedAt = &d
		}
		rec.Tasks = append(rec.Tasks, tk)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	prows, err := db.Query(`
		SELECT p.task_id, p.created_at, t.archived, (1-t.review_slot_held)
		FROM review_versions p JOIN tasks t ON t.current_review_id=p.id WHERE t.project = 'ws'`)
	if err != nil {
		t.Fatal(err)
	}
	defer prows.Close()
	for prows.Next() {
		var p dispatch.Packet
		var createdAt string
		var archived int
		if err := prows.Scan(&p.TaskID, &createdAt, &archived, &p.Superseded); err != nil {
			t.Fatal(err)
		}
		if p.CreatedAt, err = domain.ParseSpineTime(createdAt); err != nil {
			t.Fatal(err)
		}
		p.Archived = archived == 1
		rec.Packets = append(rec.Packets, p)
	}
	if err := prows.Err(); err != nil {
		t.Fatal(err)
	}
	return rec
}

// decide runs the oracle over the stored state with a free lease.
func decide(t *testing.T, db *sql.DB) dispatch.Action {
	t.Helper()
	cfg := dispatch.DefaultConfig()
	return dispatch.Decide(loadProjection(t, db), dispatch.Lease{}, cfg,
		dispatch.Clock{Now: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)})
}

// reviewedTask builds one packaged task with its merged candidate and its
// live packet: the state of a task that waits for the operator.
func reviewedTask(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	id := mkTask(t, db, "task", "packaged")
	seedMergedCandidate(t, db, id)
	if err := birthPacket(t, db, id, "r.html"); err != nil {
		t.Fatal(err)
	}
	return id
}

// atCap builds a full queue of three packets through domain operations and
// returns the packet-holding task ids in birth order.
func atCap(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	ids := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		ids = append(ids, reviewedTask(t, db))
	}
	return ids
}

func TestAtCapSelectionOrder(t *testing.T) {
	// A revision uses its existing packet slot. The tick advances it before
	// it starts new work.
	t.Run("advance_in_flight_first", func(t *testing.T) {
		db := openSpine(t)
		ids := atCap(t, db)
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.RequestCorrection(ctx, q, ids[1], "deepen")
		})
		a := decide(t, db)
		if a.Kind != dispatch.KindSpawn || a.Spawn.Role != dispatch.RoleWorker ||
			a.Spawn.SubjectID == nil || *a.Spawn.SubjectID != ids[1] {
			t.Fatalf("action = %+v, want worker spawn on the revised task %d", a, ids[1])
		}
	})

	t.Run("packaged_tasks_wait_for_review", func(t *testing.T) {
		db := openSpine(t)
		ids := atCap(t, db)
		mustExec(t, db, `UPDATE tasks SET priority = -1 WHERE id = ?`, ids[2])
		a := decide(t, db)
		if a.Kind != dispatch.KindIdle || a.Idle != dispatch.IdleQueueFull {
			t.Fatalf("The full queue must wait for review: %+v", a)
		}
	})

	// Below the cap the tick selects the next dispatch instead.
	t.Run("below_cap_selects_next_dispatch", func(t *testing.T) {
		db := openSpine(t)
		ids := atCap(t, db)
		mustTx(t, db, func(ctx context.Context, q domain.Q) error {
			return domain.Cancel(ctx, q, ids[0], "drop", "operator") // the slot frees
		})
		seeded := mkTask(t, db, "task", "seeded")
		a := decide(t, db)
		if a.Kind != dispatch.KindSpawn || a.Spawn.Role != dispatch.RoleWorker ||
			a.Spawn.SubjectID == nil || *a.Spawn.SubjectID != seeded {
			t.Fatalf("action = %+v, want the worker on %d with room", a, seeded)
		}
	})
}
