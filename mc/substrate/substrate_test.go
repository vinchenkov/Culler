// Pure-SQL backstop tests for the spine substrate: no mc binary, no domain
// layer — raw SQL against a temp spine file. The tests assert commit and abort
// for every rule the trigger lattice and CHECK constraints must enforce
// standalone.
package substrate_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

// ---------------------------------------------------------------------------
// Schema smoke: WAL discipline, singletons.

func TestPragmasAndSingletons(t *testing.T) {
	db := openSpine(t)

	if jm := oneStr(t, db, `PRAGMA journal_mode`); jm != "wal" {
		t.Fatalf("journal_mode = %q, want wal", jm)
	}
	if fk := oneInt(t, db, `PRAGMA foreign_keys`); fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}

	t.Run("meta_singleton", func(t *testing.T) {
		mustExec(t, db, `INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (1, 'uuid-1', 'test')`)
		wantAbort(t, db, `INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (2, 'uuid-2', 'test')`)
	})

	t.Run("settings_singleton", func(t *testing.T) {
		// One deployment-wide settings row,
		// seeded with the defaults, never deleted, never a second one.
		if n := oneInt(t, db, `SELECT COUNT(*) FROM settings`); n != 1 {
			t.Fatalf("settings rows = %d, want the seeded singleton", n)
		}
		if cap := oneInt(t, db, `SELECT review_wip_cap FROM settings WHERE id = 1`); cap != 3 {
			t.Fatalf("seeded review_wip_cap = %d, want 3", cap)
		}
		wantAbort(t, db, `INSERT INTO settings (id) VALUES (2)`)
		wantAbort(t, db, `DELETE FROM settings`)
		wantAbort(t, db, `UPDATE settings SET review_wip_cap = 0 WHERE id = 1`)
		wantAbort(t, db, `UPDATE settings SET homie_idle_timeout_s = 0 WHERE id = 1`)
		wantAbort(t, db, `UPDATE settings SET wiki_update_interval_s = 0 WHERE id = 1`)
	})

	t.Run("leases_per_project", func(t *testing.T) {
		// The Project record inserts its
		// lease row, free; the row is never deleted; a claim and a release
		// both commit (CAS mechanics are mc's, shape is ours).
		if n := oneInt(t, db, `SELECT COUNT(*) FROM leases WHERE project = 'ws'`); n != 1 {
			t.Fatalf("lease rows for ws = %d, want the one the Project insert created", n)
		}
		if v := oneStr(t, db, `SELECT run_id FROM leases WHERE project = 'ws'`); v != "<NULL>" {
			t.Fatalf("seeded lease not free: run_id = %q", v)
		}
		mkProject(t, db, "ws2")
		if n := oneInt(t, db, `SELECT COUNT(*) FROM leases`); n != 2 {
			t.Fatalf("lease rows = %d, want one per Project", n)
		}
		wantAbort(t, db, `DELETE FROM leases WHERE project = 'ws2'`)
		wantAbort(t, db, `INSERT INTO leases (project) VALUES ('no-such-ws')`)
		mustExec(t, db, `UPDATE leases SET run_id = 'r1', owner = 'worker',
			acquired_at = datetime('now'), boot_id = 'boot', acquired_uptime_ms = 0,
			deadline_uptime_ms = 4500000 WHERE project = 'ws' AND run_id IS NULL`)
		// Two Projects hold leases at the same time; one run holds one.
		mustExec(t, db, `UPDATE leases SET run_id = 'r2', owner = 'worker',
			acquired_at = datetime('now'), boot_id = 'boot', acquired_uptime_ms = 0,
			deadline_uptime_ms = 4500000 WHERE project = 'ws2' AND run_id IS NULL`)
		wantAbort(t, db, `UPDATE leases SET run_id = 'r1' WHERE project = 'ws2'`)
		// A free lease carries no run residue.
		// A heartbeat stamps its time and its uptime together.
		wantAbort(t, db, `UPDATE leases SET last_heartbeat_at = datetime('now') WHERE project = 'ws'`)
		mustExec(t, db, `UPDATE leases SET last_heartbeat_at = datetime('now'), heartbeat_uptime_ms = 1 WHERE project = 'ws'`)
		wantAbort(t, db, `UPDATE leases SET run_id = NULL, owner = NULL, acquired_at = NULL, boot_id = NULL,
			acquired_uptime_ms = NULL, deadline_uptime_ms = NULL WHERE project = 'ws'`)
		mustExec(t, db, `UPDATE leases SET run_id = NULL, owner = NULL, subject = NULL,
			acquired_at = NULL, last_heartbeat_at = NULL, boot_id = NULL, acquired_uptime_ms = NULL,
			heartbeat_uptime_ms = NULL, deadline_uptime_ms = NULL WHERE project = 'ws'`)
		mustExec(t, db, `UPDATE leases SET run_id = NULL, owner = NULL, subject = NULL,
			acquired_at = NULL, boot_id = NULL, acquired_uptime_ms = NULL,
			deadline_uptime_ms = NULL WHERE project = 'ws2'`)
	})
}

func TestProjectArchiveIsHistorical(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "history")
	mustExec(t, db, `UPDATE projects SET status='paused' WHERE id='history'`)
	mustExec(t, db, `UPDATE projects SET status='archived' WHERE id='history'`)
	wantAbort(t, db, `UPDATE projects SET status='active' WHERE id='history'`)
	wantAbort(t, db, `DELETE FROM projects WHERE id='history'`)
}

func TestTransitionMatrix(t *testing.T) {
	legal := map[string]bool{
		"proposed>seeded":   true, // promote
		"seeded>worked":     true, // work
		"worked>verified":   true, // verify pass
		"worked>seeded":     true, // correction rally
		"verified>packaged": true, // package
		"packaged>seeded":   true, // revision / operator revise
	}

	// the same trigger must enforce the same matrix for all of them.
	for _, pop := range []string{"task"} {
		db := openSpine(t)
		for _, from := range statuses {
			for _, to := range statuses {
				t.Run(fmt.Sprintf("%s/%s->%s", pop, from, to), func(t *testing.T) {
					id := mkTask(t, db, pop, from)
					if from == "verified" {
						seedVerifiedVersion(t, db, id)
					}
					if from == "packaged" && to == "seeded" {
						mkPacket(t, db, id)
					}
					_, err := db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, to, id)
					switch {
					case from == to:
						// Self-assignment is a no-op, not a transition.
						if err != nil {
							t.Fatalf("no-op self-assignment aborted: %v", err)
						}
					case legal[from+">"+to]:
						if err != nil {
							t.Fatalf("legal edge %s->%s aborted: %v", from, to, err)
						}
						if got := taskStr(t, db, id, "status"); got != to {
							t.Fatalf("status = %q after legal edge, want %q", got, to)
						}
					default:
						if err == nil {
							t.Fatalf("illegal edge %s->%s committed", from, to)
						}
						if got := taskStr(t, db, id, "status"); got != from {
							t.Fatalf("status = %q after aborted edge, want unchanged %q", got, from)
						}
					}
				})
			}
		}
	}
}

// A verified task reaches the packaged stage only with the version that the
// Verifier passed as its submitted version.
func TestPackagedStageRequiresVerifiedVersion(t *testing.T) {
	t.Run("no_verified_source", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "verified")
		wantMessage(t, db, "the Packager requires the verified submitted version", `UPDATE tasks SET status = 'packaged' WHERE id = ?`, id)
		seedVerifiedVersion(t, db, id)
		mustExec(t, db, `UPDATE tasks SET status = 'packaged' WHERE id = ?`, id)
	})
	t.Run("another_submitted_version", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "verified")
		verified := seedVerifiedVersion(t, db, id)
		later := seedWorkerVersion(t, db, id)
		mustExec(t, db, `UPDATE tasks SET submitted_version = ? WHERE id = ?`, later, id)
		wantMessage(t, db, "the Packager requires the verified submitted version", `UPDATE tasks SET status = 'packaged' WHERE id = ?`, id)
		mustExec(t, db, `UPDATE tasks SET submitted_version = ? WHERE id = ?`, verified, id)
		mustExec(t, db, `UPDATE tasks SET status = 'packaged' WHERE id = ?`, id)
	})
	t.Run("source_version_requires_pass", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "worked")
		version := seedWorkerVersion(t, db, id)
		wantMessage(t, db, "the verified source requires a Verifier pass", `UPDATE tasks SET source_version = ? WHERE id = ?`, version, id)
		other := mkTask(t, db, "task", "worked")
		passed := seedVerifiedVersion(t, db, other)
		wantMessage(t, db, "the verified source requires a Verifier pass", `UPDATE tasks SET source_version = ? WHERE id = ?`, passed, id)
	})
}

// A Scribe run holds no task and no Project lease.
func TestScribeRunHasNoTask(t *testing.T) {
	db := openSpine(t)
	task := mkTask(t, db, "task", "verified")
	wantMessage(t, db, "a Scribe run has no task", `INSERT INTO runs(id,tier,role,project,subject,binding)
		VALUES('scribe-task','pipeline','scribe','ws',?,'fake')`, task)
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,binding) VALUES('scribe-wiki','pipeline','scribe','ws','fake')`)
	wantMessage(t, db, "a Scribe run has no task", `UPDATE runs SET subject=? WHERE id='scribe-wiki'`, task)
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,binding) VALUES('worker-task','pipeline','worker','ws',?,'fake')`, task)
	wantMessage(t, db, "a Scribe run has no task", `UPDATE runs SET role='scribe' WHERE id='worker-task'`)
	claim := `UPDATE leases SET run_id='worker-task',subject=?,owner=?,acquired_at=datetime('now'),
		boot_id='boot',acquired_uptime_ms=0,deadline_uptime_ms=1 WHERE project='ws'`
	wantAbort(t, db, claim, task, "scribe")
	mustExec(t, db, claim, task, "worker")
}

func TestBirthStatusMatrix(t *testing.T) {
	db := openSpine(t)
	for _, scope := range []string{"task"} {
		for _, st := range statuses {
			t.Run(fmt.Sprintf("%s/born-%s", scope, st), func(t *testing.T) {
				q := `INSERT INTO tasks (title, scope, status, project) VALUES ('b', ?, ?, 'ws')`
				if st == "proposed" {
					mustExec(t, db, q, scope, st)
				} else {
					wantAbort(t, db, q, scope, st)
				}
			})
		}
	}
	// Nothing is born decided or archived: these reach ONLY the
	// birth trigger's terminal-marks arm — every table CHECK is satisfied
	// (decision/decided_at paired, archive-carries-decision holds).
	t.Run("task/born-decided", func(t *testing.T) {
		wantAbort(t, db,
			`INSERT INTO tasks (title, project, decision, decided_at)
			 VALUES ('b', 'ws', 'rejected', datetime('now'))`)
	})
	t.Run("task/born-decided-archived", func(t *testing.T) {
		wantAbort(t, db,
			`INSERT INTO tasks (title, project, decision, decided_at, archived)
			 VALUES ('b', 'ws', 'cancelled', datetime('now'), 1)`)
	})

}

// Terminal cases: an archived (decided) row never transitions again.
func TestArchivedRowsAreTerminal(t *testing.T) {
	db := openSpine(t)

	t.Run("rejected_proposal", func(t *testing.T) {
		id := mkTask(t, db, "task", "proposed")
		mustExec(t, db, `UPDATE tasks SET decision = 'rejected', decided_at = datetime('now'), archived = 1 WHERE id = ?`, id)
		wantAbort(t, db, `UPDATE tasks SET status = 'seeded' WHERE id = ?`, id)
	})

	t.Run("cancelled_at_worked", func(t *testing.T) {
		id := mkTask(t, db, "task", "worked")
		cancelTask(t, db, id)
		wantAbort(t, db, `UPDATE tasks SET status = 'verified' WHERE id = ?`, id)
		wantAbort(t, db, `UPDATE tasks SET status = 'seeded' WHERE id = ?`, id)
	})

	t.Run("approved_and_merged", func(t *testing.T) {
		id := mkTask(t, db, "task", "packaged")
		mkPacket(t, db, id)
		approveTask(t, db, id)
		mustExec(t, db, `UPDATE tasks SET archived = 1 WHERE id = ?`, id)
		wantAbort(t, db, `UPDATE tasks SET status = 'seeded' WHERE id = ?`, id)
	})

	// Terminal bookkeeping is itself terminal: an archived row's decision
	// record never rewrites.
	t.Run("archived_decision_frozen", func(t *testing.T) {
		id := mkTask(t, db, "task", "proposed")
		mustExec(t, db, `UPDATE tasks SET decision = 'rejected', decided_at = datetime('now'), archived = 1 WHERE id = ?`, id)
		wantAbort(t, db, `UPDATE tasks SET decision = 'cancelled' WHERE id = ?`, id)
		wantAbort(t, db, `UPDATE tasks SET decided_at = datetime('now', '+1 hour') WHERE id = ?`, id)
		if got := taskStr(t, db, id, "decision"); got != "rejected" {
			t.Fatalf("archived decision rewritten to %q", got)
		}
	})

	// No resurrection: an archived row never returns to dispatch. A merged
	// task would otherwise wait for a second merge of the same version.
	t.Run("unarchive_refused", func(t *testing.T) {
		id := mkTask(t, db, "task", "proposed")
		mustExec(t, db, `UPDATE tasks SET decision = 'rejected', decided_at = datetime('now'), archived = 1 WHERE id = ?`, id)
		wantAbort(t, db, `UPDATE tasks SET archived = 0 WHERE id = ?`, id)
	})
	t.Run("merged_task_unarchive_refused", func(t *testing.T) {
		id := mkTask(t, db, "task", "packaged")
		mkPacket(t, db, id)
		approveTask(t, db, id)
		mustExec(t, db, `UPDATE tasks SET archived = 1 WHERE id = ?`, id) // the merge-success write
		wantAbort(t, db, `UPDATE tasks SET archived = 0 WHERE id = ?`, id)
	})

	// A decided-but-unarchived row (the transient two-statement window) never
	// transitions either: rejected/cancelled rows only archive.
	t.Run("decided_unarchived_never_moves", func(t *testing.T) {
		id := mkTask(t, db, "task", "proposed")
		mustExec(t, db, `UPDATE tasks SET decision = 'rejected', decided_at = datetime('now') WHERE id = ?`, id)
		wantAbort(t, db, `UPDATE tasks SET status = 'seeded' WHERE id = ?`, id)
		if got := taskStr(t, db, id, "status"); got != "proposed" {
			t.Fatalf("rejected row moved to %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// correction_count bounds: CHECK 0-3.

func TestCorrectionCountBounds(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	for v := 0; v <= 3; v++ {
		mustExec(t, db, `UPDATE tasks SET correction_count = ? WHERE id = ?`, v, id)
	}
	wantAbort(t, db, `UPDATE tasks SET correction_count = 4 WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET correction_count = -1 WHERE id = ?`, id)
	wantAbort(t, db, `INSERT INTO tasks (title, scope, project, correction_count) VALUES ('x', 'task', 'ws', 4)`)
}

// ---------------------------------------------------------------------------
// Blocked requires a reason and a condition type; unblocking clears both.

func TestBlockedNeedsReasonAndUnblockClears(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")

	wantAbort(t, db, `UPDATE tasks SET blocked = 1 WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET blocked = 1, blocked_reason = 'needs operator input' WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET blocked = 1, blocked_kind = 'question', blocked_reason = 'needs operator input' WHERE id = ?`, id)
	mustExec(t, db, `UPDATE tasks SET blocked = 1, blocked_kind = 'operator', blocked_reason = 'needs operator input' WHERE id = ?`, id)
	// Dropping the reason or the type while still blocked aborts.
	wantAbort(t, db, `UPDATE tasks SET blocked_reason = NULL WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET blocked_kind = NULL WHERE id = ?`, id)
	// Unblock clears the reason and the type.
	mustExec(t, db, `UPDATE tasks SET blocked = 0 WHERE id = ?`, id)
	if got := taskStr(t, db, id, "blocked_reason"); got != "<NULL>" {
		t.Fatalf("blocked_reason = %q after unblock, want cleared", got)
	}
	if got := taskStr(t, db, id, "blocked_kind"); got != "<NULL>" {
		t.Fatalf("blocked_kind = %q after unblock, want cleared", got)
	}
}

// The first value of the base commit stays fixed.
func TestTaskBaseCommitIsImmutable(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	base := strings.Repeat("a", 40)
	mustExec(t, db, `UPDATE tasks SET base_commit = ? WHERE id = ?`, base, id)
	mustExec(t, db, `UPDATE tasks SET base_commit = ? WHERE id = ?`, base, id)
	wantAbort(t, db, `UPDATE tasks SET base_commit = ? WHERE id = ?`, strings.Repeat("b", 40), id)
	wantAbort(t, db, `UPDATE tasks SET base_commit = NULL WHERE id = ?`, id)
	if got := taskStr(t, db, id, "base_commit"); got != base {
		t.Fatalf("base_commit = %q, want %q", got, base)
	}
}

// ---------------------------------------------------------------------------
// Archive needs a decision; decision and decided_at travel together.

func TestArchiveNeedsDecisionAndTimestampPairing(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "proposed")

	wantAbort(t, db, `UPDATE tasks SET archived = 1 WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET decision = 'cancelled' WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET decided_at = datetime('now') WHERE id = ?`, id)

	mustExec(t, db, `UPDATE tasks SET decision = 'cancelled', decided_at = datetime('now') WHERE id = ?`, id)
	// Un-pairing after the fact aborts too.
	wantAbort(t, db, `UPDATE tasks SET decided_at = NULL WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET decision = NULL WHERE id = ?`, id)
	mustExec(t, db, `UPDATE tasks SET archived = 1 WHERE id = ?`, id)
}

// ---------------------------------------------------------------------------
// Approve only from packaged, with a live packet, a merged candidate of the
// submitted version, and an approved review of that version.

func TestApproveOnlyFromPackaged(t *testing.T) {
	db := openSpine(t)
	const approve = `UPDATE tasks SET decision = 'approved', decided_at = datetime('now') WHERE id = ?`
	for _, scope := range []string{"task"} {
		for _, st := range statuses {
			t.Run(scope+"/"+st, func(t *testing.T) {
				id := mkTask(t, db, scope, st)
				if st == "packaged" {
					mkPacket(t, db, id)
					approveTask(t, db, id)
					// A task that waits for its merge (approved, unarchived, at
					// packaged) never returns to the pipeline: the revision edge
					// packaged -> seeded is refused.
					wantAbort(t, db, `UPDATE tasks SET status = 'seeded' WHERE id = ?`, id)
					if got := taskStr(t, db, id, "status"); got != "packaged" {
						t.Fatalf("approved row moved to %q", got)
					}
				} else {
					wantAbort(t, db, approve, id)
				}
			})
		}
	}

	t.Run("live_packet_required", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		seedMergedCandidate(t, db, id)
		recordReviewApproval(t, db, id)
		wantAbort(t, db, approve, id)
		mkPacket(t, db, id)
		mustExec(t, db, approve, id)
	})

	t.Run("merged_candidate_required", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		mkPacket(t, db, id)
		recordReviewApproval(t, db, id)
		wantAbort(t, db, approve, id)
		seedMergedCandidate(t, db, id)
		mustExec(t, db, approve, id)
	})

	t.Run("review_decision_required", func(t *testing.T) {
		db := openSpine(t)
		id := mkTask(t, db, "task", "packaged")
		mkPacket(t, db, id)
		seedMergedCandidate(t, db, id)
		wantAbort(t, db, approve, id)
		recordReviewApproval(t, db, id)
		mustExec(t, db, approve, id)
	})

}

func TestReviewWipCap(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `UPDATE settings SET review_wip_cap=2 WHERE id=1`)
	mkProject(t, db, "ws2")
	var first, second []int64
	for index := 0; index < 3; index++ {
		first = append(first, mkTask(t, db, "task", "packaged"))
		second = append(second, mkTaskIn(t, db, "ws2", "task", "packaged"))
	}
	for _, tasks := range [][]int64{first, second} {
		mkPacket(t, db, tasks[0])
		mkPacket(t, db, tasks[1])
		review := seedReviewVersion(t, db, tasks[2])
		wantAbort(t, db, `UPDATE tasks SET current_review_id=?,review_slot_held=1 WHERE id=?`, review, tasks[2])
	}
	if got := oneInt(t, db, `SELECT count(*) FROM tasks WHERE archived=0 AND review_slot_held=1`); got != 4 {
		t.Fatalf("the two Projects hold %d slots instead of four", got)
	}
	wantAbort(t, db, `UPDATE tasks SET review_slot_held=0 WHERE id=?`, first[1])
	review := taskInt(t, db, first[0], "current_review_id")
	cancelTask(t, db, first[0])
	if got := oneInt(t, db, `SELECT count(*) FROM review_versions WHERE id=?`, review); got != 1 {
		t.Fatal("cancellation removed the immutable review history")
	}
	mkPacket(t, db, first[2])
	otherReview := seedReviewVersion(t, db, second[2])
	wantAbort(t, db, `UPDATE tasks SET current_review_id=?,review_slot_held=1 WHERE id=?`, otherReview, second[2])
	for _, project := range []string{"ws", "ws2"} {
		if got := oneInt(t, db, `SELECT count(*) FROM tasks WHERE project=? AND archived=0 AND review_slot_held=1`, project); got != 2 {
			t.Fatalf("Project %s holds %d slots instead of two", project, got)
		}
	}
	wantAbort(t, db, `UPDATE review_versions SET render_path='another-report.html' WHERE id=?`, review)
	wantAbort(t, db, `DELETE FROM review_versions WHERE id=?`, review)
}

// ---------------------------------------------------------------------------
// Each Project starts with its repository limits and its lease.

func TestBirthRecords(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")

	t.Run("repository_limits_and_lease", func(t *testing.T) {
		if got := oneInt(t, db, `SELECT COUNT(*) FROM repository_limits WHERE project = 'ws2'`); got != 1 {
			t.Fatalf("repository_limits rows = %d, want 1", got)
		}
		if got := oneInt(t, db, `SELECT destination_quiet_seconds FROM repository_limits WHERE project = 'ws2'`); got != 30 {
			t.Fatalf("destination_quiet_seconds = %d, want 30", got)
		}
		if got := oneInt(t, db, `SELECT COUNT(*) FROM leases WHERE project = 'ws2'`); got != 1 {
			t.Fatalf("leases rows = %d, want 1", got)
		}
	})

	t.Run("task_merges_into_main", func(t *testing.T) {
		id := mkTask(t, db, "task", "proposed")
		if got := oneStr(t, db, `SELECT destination_branch FROM tasks WHERE id = ?`, id); got != "main" {
			t.Fatalf("destination_branch = %q, want main", got)
		}
	})

}

func TestCurrentReviewRequiresPackagedTask(t *testing.T) {
	for _, status := range []string{"proposed", "seeded", "worked", "verified"} {
		t.Run(status, func(t *testing.T) {
			db := openSpine(t)
			task := mkTask(t, db, "task", status)
			seedVerifiedVersion(t, db, task)
			review := seedReviewVersion(t, db, task)
			wantAbort(t, db, `UPDATE tasks SET current_review_id=?,review_slot_held=1 WHERE id=?`, review, task)
		})
	}
	t.Run("archived_packaged", func(t *testing.T) {
		db := openSpine(t)
		task := mkTask(t, db, "task", "packaged")
		review := seedReviewVersion(t, db, task)
		cancelTask(t, db, task)
		wantAbort(t, db, `UPDATE tasks SET current_review_id=?,review_slot_held=1 WHERE id=?`, review, task)
	})
	t.Run("review_requires_its_task", func(t *testing.T) {
		db := openSpine(t)
		task := mkTask(t, db, "task", "packaged")
		version := taskInt(t, db, task, "submitted_version")
		wantAbort(t, db, `INSERT INTO review_versions(task_id,version_id,render_path,report_sha256,thesis,criteria)
		 VALUES(999999,?,'report.html','hash','The result satisfies the task.','The task requires this result.')`, version)
	})
	t.Run("new_task_starts_without_a_review", func(t *testing.T) {
		db := openSpine(t)
		task := mkTask(t, db, "task", "packaged")
		review := seedReviewVersion(t, db, task)
		wantAbort(t, db, `INSERT INTO tasks(title,project,current_review_id,review_slot_held)
		 VALUES('The new task','ws',?,1)`, review)
	})
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Activity is append-only: UPDATE and DELETE both abort.

func TestActivityAppendOnly(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO activity (actor, kind, detail) VALUES ('mc', 'daily.briefing', 'x')`)
	wantAbort(t, db, `UPDATE activity SET detail = 'edited'`)
	wantAbort(t, db, `UPDATE activity SET detail = 'edited' WHERE kind = 'daily.briefing'`)
	wantAbort(t, db, `DELETE FROM activity`)
	wantAbort(t, db, `DELETE FROM activity WHERE kind = 'daily.briefing'`)
	if n := oneInt(t, db, `SELECT COUNT(*) FROM activity`); n != 1 {
		t.Fatalf("activity rows = %d, want the 1 appended row intact", n)
	}
}

// Every mutation-producing dispatch action has one
// database-backed replay fence. Commit-side actions use a 32-byte action hash;
// prepare-side actions pair a command-scoped request id with the exact final
// JSON result. The spine rejects malformed, half-paired, or duplicate receipts
// even if the Go layer regresses.
func TestDispatchActivityReceiptBackstops(t *testing.T) {
	assertActivityReceiptBackstops(t, openSpine(t))
}

// assertActivityReceiptBackstops checks the activity receipt constraints.
func assertActivityReceiptBackstops(t *testing.T, db *sql.DB) {
	t.Helper()
	dispatchKey := strings.Repeat("a", 64)
	requestID := strings.Repeat("b", 16)
	result := `{"action":"health","code":"health.runtime_unavailable"}`

	mustExec(t, db, `
		INSERT INTO activity (actor, kind, detail, dispatch_key)
		VALUES ('mc', 'dispatch.health', '{}', ?)`, dispatchKey)
	wantAbort(t, db, `
		INSERT INTO activity (actor, kind, detail, dispatch_key)
		VALUES ('mc', 'dispatch.health', '{}', ?)`, dispatchKey)

	// nulTail and nulPad are the two halves of the same hole: length() and
	// GLOB both stop at the first NUL, so a character test misses the trailing
	// bytes and a byte test never shows the NUL to GLOB. Either shape would
	// store a key whose own receipt lookup cannot find it, so a replayed commit
	// would duplicate the activity, its consequence, and its outbox fan-out.
	for name, key := range map[string]string{
		"short":     strings.Repeat("a", 63),
		"uppercase": strings.Repeat("A", 64),
		"non_hex":   strings.Repeat("a", 63) + "g",
		"nul_tail":  strings.Repeat("a", 64) + "\x00EVIL",
		"nul_pad":   strings.Repeat("a", 63) + "\x00",
	} {
		t.Run("dispatch_key_"+name, func(t *testing.T) {
			wantAbort(t, db, `
				INSERT INTO activity (actor, kind, detail, dispatch_key)
				VALUES ('mc', 'dispatch.health', '{}', ?)`, key)
		})
	}

	// The prepare-side receipt is all-or-nothing and stores an object-shaped,
	// bounded final result. A retry can therefore return the original bytes
	// without re-entering selection.
	wantAbort(t, db, `
		INSERT INTO activity (actor, kind, dispatch_request_id)
		VALUES ('mc', 'dispatch.reap', ?)`, requestID)
	wantAbort(t, db, `
		INSERT INTO activity (actor, kind, dispatch_result)
		VALUES ('mc', 'dispatch.reap', ?)`, result)
	mustExec(t, db, `
		INSERT INTO activity (actor, kind, dispatch_request_id, dispatch_result)
		VALUES ('mc', 'dispatch.reap', ?, ?)`, requestID, result)
	wantAbort(t, db, `
		INSERT INTO activity (actor, kind, dispatch_request_id, dispatch_result)
		VALUES ('mc', 'dispatch.reap', ?, ?)`, requestID, result)

	for name, args := range map[string][]any{
		"request_short":     {strings.Repeat("b", 15), result},
		"request_upper":     {strings.Repeat("B", 16), result},
		"request_nonhex":    {strings.Repeat("b", 15) + "z", result},
		"request_nul_tail":  {strings.Repeat("b", 16) + "\x00X", result},
		"request_nul_pad":   {strings.Repeat("b", 15) + "\x00", result},
		"result_not_json":   {strings.Repeat("c", 16), "not-json"},
		"result_not_object": {strings.Repeat("d", 16), `[]`},
		"result_oversize":   {strings.Repeat("e", 16), `{"x":"` + strings.Repeat("x", 65536) + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			wantAbort(t, db, `
				INSERT INTO activity (actor, kind, dispatch_request_id, dispatch_result)
				VALUES ('mc', 'dispatch.reap', ?, ?)`, args...)
		})
	}

	// TEXT affinity never converts a
	// BLOB, length(blob) counts bytes, and GLOB reads only to the first NUL —
	// so both a fully-hex BLOB (a UNIQUE value distinct from its TEXT twin,
	// which the replay lookup can never find) and a NUL-embedded one pass
	// every column CHECK. The trigger rejects these values.
	for name, args := range map[string][]any{
		"key_hex_blob":     {[]byte(strings.Repeat("a", 64)), nil, nil},
		"key_nul_blob":     {append([]byte(strings.Repeat("a", 63)), 0x00), nil, nil},
		"request_hex_blob": {nil, []byte(strings.Repeat("b", 16)), result},
		"request_nul_blob": {nil, append([]byte(strings.Repeat("b", 15)), 0x00), result},
		"result_blob":      {nil, strings.Repeat("f", 16), []byte(`{"forged":true}`)},
	} {
		t.Run("blob_"+name, func(t *testing.T) {
			wantAbort(t, db, `
				INSERT INTO activity (actor, kind, detail, dispatch_key, dispatch_request_id, dispatch_result)
				VALUES ('mc', 'dispatch.health', '{}', ?, ?, ?)`, args...)
		})
	}
}

// ---------------------------------------------------------------------------
// stage_rank generation: status → rank, packaged = 0, unwritable.

func TestStageRankGeneration(t *testing.T) {
	db := openSpine(t)
	want := map[string]int64{
		"packaged": 0,
		"proposed": 1,
		"seeded":   2,
		"worked":   3,
		"verified": 4,
	}
	for st, rank := range want {
		t.Run(st, func(t *testing.T) {
			id := mkTask(t, db, "task", st)
			if got := taskInt(t, db, id, "stage_rank"); got != rank {
				t.Fatalf("stage_rank(%s) = %d, want %d", st, got, rank)
			}
		})
	}
	t.Run("generated_never_written", func(t *testing.T) {
		id := mkTask(t, db, "task", "seeded")
		wantSQLiteError(t, db, sqlite3.SQLITE_ERROR, `cannot UPDATE generated column "stage_rank"`, `UPDATE tasks SET stage_rank = 9 WHERE id = ?`, id)
		wantSQLiteError(t, db, sqlite3.SQLITE_ERROR, `cannot INSERT into generated column "stage_rank"`, `INSERT INTO tasks (title, scope, project, stage_rank) VALUES ('x', 'task', 'ws', 4)`)
	})
}

// Fail-closed extras the trigger lattice adds.

func TestIdentityColumnsImmutable(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws2")
	id := mkTask(t, db, "task", "seeded")
	wantAbort(t, db, `UPDATE tasks SET scope = 'invalid' WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET project = 'ws2' WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET origin = 'user' WHERE id = ?`, id)
	wantAbort(t, db, `UPDATE tasks SET created_at = datetime('now', '-1 day') WHERE id = ?`, id)
}

func TestNoDeleteBackstops(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "packaged")
	mkPacket(t, db, id)
	wantAbort(t, db, `DELETE FROM tasks WHERE id = ?`, id)
	wantAbort(t, db, `DELETE FROM review_versions WHERE task_id = ?`, id)

	mustExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('r1', 'pipeline', 'worker', 'ws', ?)`, id)
	wantAbort(t, db, `DELETE FROM runs WHERE id = 'r1'`)

	mkHomieSession(t, db, "h1")
	wantAbort(t, db, `DELETE FROM homie_sessions WHERE id = 'h1'`)
}

func TestRunCreationSequenceIsMonotonicAndImmutable(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO runs (id, tier, created_at) VALUES
		('z-older', 'pipeline', '2026-08-06 12:00:00'),
		('a-newer', 'pipeline', '2026-08-06 12:00:00')`)
	if older := oneInt(t, db, `SELECT created_seq FROM runs WHERE id='z-older'`); older != 1 {
		t.Fatalf("older created_seq = %d, want 1", older)
	}
	if newer := oneInt(t, db, `SELECT created_seq FROM runs WHERE id='a-newer'`); newer != 2 {
		t.Fatalf("newer created_seq = %d, want 2", newer)
	}
	wantAbort(t, db, `UPDATE runs SET created_seq=9 WHERE id='z-older'`)
	wantAbort(t, db, `INSERT INTO runs (id, tier, created_seq) VALUES ('forged', 'pipeline', 9)`)
}

func TestHomieSessionRegistryInvariants(t *testing.T) {
	db := openSpine(t)

	// Start-time identity and resume locators are authoritative registry
	// state, never nullable placeholders that a later component guesses.
	wantAbort(t, db, `INSERT INTO homie_sessions (id) VALUES ('incomplete')`)
	wantAbort(t, db, `INSERT INTO homie_sessions
		(id, container_name, verb_allowlist, session_path, binding)
		VALUES ('bad-json', 'mc-homie-bad-json', 'not-json', 'sessions/bad-json', 'fake/fake')`)
	wantAbort(t, db, `INSERT INTO homie_sessions
		(id, container_name, verb_allowlist, session_path, binding)
		VALUES ('bad-shape', 'mc-homie-bad-shape', '{}', 'sessions/bad-shape', 'fake/fake')`)
	mkHomieSession(t, db, "h1")

	for name, update := range map[string]string{
		"id":             `id = 'renamed'`,
		"created_at":     `created_at = datetime('now', '+1 day')`,
		"container_name": `container_name = 'mc-homie-other'`,
		"verb_allowlist": `verb_allowlist = '["task.add"]'`,
		"session_path":   `session_path = 'sessions/other'`,
		"binding":        `binding = 'codex/chatgpt'`,
	} {
		t.Run(name+"_immutable", func(t *testing.T) {
			wantAbort(t, db, `UPDATE homie_sessions SET `+update+` WHERE id = 'h1'`)
		})
	}

	// The runner registers the remaining locator pair exactly once. A
	// half-registration is unstorable and conflicting retries fail closed.
	wantAbort(t, db, `UPDATE homie_sessions SET native_session_ref = 'native-1' WHERE id = 'h1'`)
	mustExec(t, db, `UPDATE homie_sessions
		SET native_session_ref = 'native-1', trace_filename = 'native.jsonl'
		WHERE id = 'h1'`)
	wantAbort(t, db, `UPDATE homie_sessions
		SET native_session_ref = 'native-2', trace_filename = 'native.jsonl'
		WHERE id = 'h1'`)
}

// Launch fencing is canonical liveness state on homie_sessions,
// with the pairing rules as storage CHECKs, not as Go politeness.
func TestHomieLaunchFenceBackstops(t *testing.T) {
	assertHomieLaunchFenceBackstops(t, openSpine(t))
}

// assertHomieLaunchFenceBackstops checks the Homie launch constraints.
func assertHomieLaunchFenceBackstops(t *testing.T, db *sql.DB) {
	t.Helper()
	mkHomieSession(t, db, "launch-fence")

	// `homie start` names only the five identity columns, so the schema's
	// defaults ARE its launch initialization: every launch/debt field must
	// start empty/zero without the Go layer spelling it.
	if got := oneInt(t, db, `
		SELECT count(*) FROM homie_sessions WHERE id = 'launch-fence'
		  AND current_launch_id IS NULL AND current_launch_mode IS NULL
		  AND current_prime_through_seq IS NULL AND current_prime_row_count IS NULL
		  AND current_container_id IS NULL
		  AND launch_bound_at IS NULL AND launch_started_at IS NULL
		  AND resume_owed = 0 AND resume_mode IS NULL
		  AND resume_prime_through_seq IS NULL AND resume_prime_row_count IS NULL`,
	); got != 1 {
		t.Fatal("a new session must start with every launch and debt field empty or zero")
	}

	launch := strings.Repeat("ab", 8)     // 16 lowercase hex
	container := strings.Repeat("cd", 32) // 64 lowercase hex

	// One full-width UPDATE per case: every launch/debt column is set at
	// once, so each shape is judged whole and no case leaks into the next.
	type launchRow struct {
		launchID, launchMode, primeSeq, primeCount any
		containerID, boundAt, startedAt            any
		resumeMode, resumeSeq, resumeCount         any
		resumeOwed                                 int
	}
	for name, c := range map[string]struct {
		row launchRow
		ok  bool
	}{
		// The legal states of the launch/debt lattice.
		"all_empty_zero":   {ok: true, row: launchRow{}},
		"fresh_launch":     {ok: true, row: launchRow{launchID: launch, launchMode: "fresh"}},
		"native_launch":    {ok: true, row: launchRow{launchID: launch, launchMode: "native"}},
		"rows_launch":      {ok: true, row: launchRow{launchID: launch, launchMode: "rows", primeSeq: 0, primeCount: 0}},
		"rows_launch_deep": {ok: true, row: launchRow{launchID: launch, launchMode: "rows", primeSeq: 9214, primeCount: 118}},
		"bound_launch": {ok: true, row: launchRow{launchID: launch, launchMode: "fresh",
			containerID: container, boundAt: "2026-07-16 03:00:00"}},
		"started_launch": {ok: true, row: launchRow{launchID: launch, launchMode: "fresh",
			containerID: container, boundAt: "2026-07-16 03:00:00", startedAt: "2026-07-16 03:00:05"}},
		"resume_debt_native": {ok: true, row: launchRow{resumeOwed: 1, resumeMode: "native"}},
		"resume_debt_rows":   {ok: true, row: launchRow{resumeOwed: 1, resumeMode: "rows", resumeSeq: 41, resumeCount: 2}},

		// Launch id and mode are both-null-or-both-present, and the id is
		// pinned by the dual-length hex fence: length() and GLOB both stop
		// at the first NUL, so a forged id could otherwise store as a value
		// its own generation lookup cannot find.
		"launch_id_without_mode": {row: launchRow{launchID: launch}},
		"mode_without_launch_id": {row: launchRow{launchMode: "fresh"}},
		"launch_mode_unknown":    {row: launchRow{launchID: launch, launchMode: "resume"}},
		"launch_id_short":        {row: launchRow{launchID: launch[:15], launchMode: "fresh"}},
		"launch_id_long":         {row: launchRow{launchID: launch + "a", launchMode: "fresh"}},
		"launch_id_uppercase":    {row: launchRow{launchID: strings.ToUpper(launch), launchMode: "fresh"}},
		"launch_id_nonhex":       {row: launchRow{launchID: launch[:15] + "g", launchMode: "fresh"}},
		"launch_id_nul_tail":     {row: launchRow{launchID: launch + "\x00EVIL", launchMode: "fresh"}},
		"launch_id_nul_pad":      {row: launchRow{launchID: launch[:15] + "\x00", launchMode: "fresh"}},
		// A BLOB bypasses TEXT affinity entirely: length() counts its bytes
		// (NULs included), and GLOB reads it converted, only to the first
		// NUL — so without a typeof fence a NUL-embedded 16-byte BLOB passes
		// every other conjunct, and even a hex-clean BLOB stores as a value
		// the Go layer's string comparisons can never match.
		"launch_id_blob_nul_pad":   {row: launchRow{launchID: append([]byte(launch[:15]), 0), launchMode: "fresh"}},
		"launch_id_blob_hex_bytes": {row: launchRow{launchID: []byte(launch), launchMode: "fresh"}},

		// Only `rows` mode carries the prime cutoff/count, always as a
		// non-negative pair.
		"fresh_launch_with_prime_pair": {row: launchRow{launchID: launch, launchMode: "fresh", primeSeq: 0, primeCount: 0}},
		"native_launch_with_prime_pair": {row: launchRow{launchID: launch, launchMode: "native",
			primeSeq: 0, primeCount: 0}},
		"rows_launch_missing_prime_pair": {row: launchRow{launchID: launch, launchMode: "rows"}},
		"rows_launch_half_pair_seq":      {row: launchRow{launchID: launch, launchMode: "rows", primeSeq: 0}},
		"rows_launch_half_pair_count":    {row: launchRow{launchID: launch, launchMode: "rows", primeCount: 0}},
		"rows_launch_negative_seq":       {row: launchRow{launchID: launch, launchMode: "rows", primeSeq: -1, primeCount: 0}},
		"rows_launch_negative_count":     {row: launchRow{launchID: launch, launchMode: "rows", primeSeq: 0, primeCount: -1}},
		"prime_pair_without_launch":      {row: launchRow{primeSeq: 0, primeCount: 0}},
		// INTEGER affinity stores non-numeric TEXT and any BLOB as-is, and
		// SQLite's cross-type ordering ranks both above every number — so
		// 'abc' >= 0 and X'FF' >= 0 are TRUE and only a typeof fence holds.
		// A non-integral REAL passes ">= 0" numerically the same way.
		"prime_seq_nonnumeric_text": {row: launchRow{launchID: launch, launchMode: "rows", primeSeq: "abc", primeCount: 41}},
		"prime_seq_noninteger_real": {row: launchRow{launchID: launch, launchMode: "rows", primeSeq: 2.5, primeCount: 3}},
		"prime_count_blob":          {row: launchRow{launchID: launch, launchMode: "rows", primeSeq: 7, primeCount: []byte{0xff}}},
		// An empty completed prefix has the single closed
		// encoding (0,0) — zero is a below-everything sentinel, not a row
		// identity. A zero cutoff claiming rows, or a positive cutoff
		// claiming none, is outside the closed grammar.
		"rows_launch_zero_cutoff_with_rows":     {row: launchRow{launchID: launch, launchMode: "rows", primeSeq: 0, primeCount: 41}},
		"rows_launch_positive_cutoff_zero_rows": {row: launchRow{launchID: launch, launchMode: "rows", primeSeq: 5, primeCount: 0}},

		// Container id and bound time are paired, require a current launch,
		// and the id is one 64-lowercase-hex Docker id.
		"container_without_bound":   {row: launchRow{launchID: launch, launchMode: "fresh", containerID: container}},
		"bound_without_container":   {row: launchRow{launchID: launch, launchMode: "fresh", boundAt: "2026-07-16 03:00:00"}},
		"bound_pair_without_launch": {row: launchRow{containerID: container, boundAt: "2026-07-16 03:00:00"}},
		"container_id_short":        {row: launchRow{launchID: launch, launchMode: "fresh", containerID: container[:63], boundAt: "t"}},
		"container_id_long":         {row: launchRow{launchID: launch, launchMode: "fresh", containerID: container + "c", boundAt: "t"}},
		"container_id_uppercase":    {row: launchRow{launchID: launch, launchMode: "fresh", containerID: strings.ToUpper(container), boundAt: "t"}},
		"container_id_nonhex":       {row: launchRow{launchID: launch, launchMode: "fresh", containerID: container[:63] + "x", boundAt: "t"}},
		"container_id_nul_tail":     {row: launchRow{launchID: launch, launchMode: "fresh", containerID: container + "\x00EVIL", boundAt: "t"}},
		"container_id_nul_pad":      {row: launchRow{launchID: launch, launchMode: "fresh", containerID: container[:63] + "\x00", boundAt: "t"}},
		"container_id_blob_nul_pad": {row: launchRow{launchID: launch, launchMode: "fresh", containerID: append([]byte(container[:63]), 0), boundAt: "t"}},

		// A start time requires the bound pair (transitively, a launch).
		"started_without_bound_pair": {row: launchRow{launchID: launch, launchMode: "fresh", startedAt: "t"}},
		"started_without_launch":     {row: launchRow{startedAt: "t"}},

		// Resume debt is resume_owed=1 plus a mode (`fresh` is not a resume
		// mode), carries the prime pair only for `rows`, and is mutually
		// exclusive with a current launch — the supersede-then-select order
		// is a storage rule, not scheduler politeness.
		"resume_owed_out_of_range": {row: launchRow{resumeOwed: 2, resumeMode: "native"}},
		// Out of range with NO mode: the (owed=1)=(mode present) pairing
		// CHECK is satisfied at owed=2/mode=NULL, so only the IN (0,1)
		// range fence rejects this shape — the case above alone would grade
		// a dropped range fence green.
		"resume_owed_out_of_range_alone": {row: launchRow{resumeOwed: 2}},
		"resume_owed_without_mode":       {row: launchRow{resumeOwed: 1}},
		"resume_mode_without_debt":       {row: launchRow{resumeMode: "native"}},
		"resume_mode_fresh_invalid":      {row: launchRow{resumeOwed: 1, resumeMode: "fresh"}},
		"resume_debt_with_current_launch": {row: launchRow{launchID: launch, launchMode: "fresh",
			resumeOwed: 1, resumeMode: "native"}},
		"resume_rows_missing_prime_pair": {row: launchRow{resumeOwed: 1, resumeMode: "rows"}},
		"resume_native_with_prime_pair":  {row: launchRow{resumeOwed: 1, resumeMode: "native", resumeSeq: 0, resumeCount: 0}},
		"resume_half_pair_seq":           {row: launchRow{resumeOwed: 1, resumeMode: "rows", resumeSeq: 0}},
		"resume_half_pair_count":         {row: launchRow{resumeOwed: 1, resumeMode: "rows", resumeCount: 0}},
		"resume_negative_seq":            {row: launchRow{resumeOwed: 1, resumeMode: "rows", resumeSeq: -1, resumeCount: 0}},
		"resume_negative_count":          {row: launchRow{resumeOwed: 1, resumeMode: "rows", resumeSeq: 0, resumeCount: -1}},
		"resume_prime_pair_without_debt": {row: launchRow{resumeSeq: 0, resumeCount: 0}},
		"resume_seq_nonnumeric_text":     {row: launchRow{resumeOwed: 1, resumeMode: "rows", resumeSeq: "abc", resumeCount: 41}},
		"resume_zero_cutoff_with_rows":   {row: launchRow{resumeOwed: 1, resumeMode: "rows", resumeSeq: 0, resumeCount: 41}},
		"resume_positive_cutoff_zero_rows": {row: launchRow{resumeOwed: 1, resumeMode: "rows",
			resumeSeq: 5, resumeCount: 0}},
	} {
		t.Run(name, func(t *testing.T) {
			const set = `
				UPDATE homie_sessions SET
					current_launch_id = ?, current_launch_mode = ?,
					current_prime_through_seq = ?, current_prime_row_count = ?,
					current_container_id = ?, launch_bound_at = ?, launch_started_at = ?,
					resume_owed = ?, resume_mode = ?,
					resume_prime_through_seq = ?, resume_prime_row_count = ?
				WHERE id = 'launch-fence'`
			args := []any{
				c.row.launchID, c.row.launchMode, c.row.primeSeq, c.row.primeCount,
				c.row.containerID, c.row.boundAt, c.row.startedAt,
				c.row.resumeOwed, c.row.resumeMode, c.row.resumeSeq, c.row.resumeCount,
			}
			if c.ok {
				mustExec(t, db, set, args...)
			} else {
				wantAbort(t, db, set, args...)
			}
		})
	}

	// resume_owed is NOT NULL, and NOT NULL is its only fence against NULL:
	// the launchRow fixture cannot express NULL (a bare int), and NULL slides
	// through IN (0,1) and every pairing CHECK because a NULL CHECK passes in
	// SQLite. Probed from the known all-empty state so nothing else can
	// reject vacuously.
	mustExec(t, db, `
		UPDATE homie_sessions SET
			current_launch_id = NULL, current_launch_mode = NULL,
			current_prime_through_seq = NULL, current_prime_row_count = NULL,
			current_container_id = NULL, launch_bound_at = NULL, launch_started_at = NULL,
			resume_owed = 0, resume_mode = NULL,
			resume_prime_through_seq = NULL, resume_prime_row_count = NULL
		WHERE id = 'launch-fence'`)
	wantAbort(t, db, `UPDATE homie_sessions SET resume_owed = NULL WHERE id = 'launch-fence'`)
}

// homie_bindings is bind-event history: end -> resume on the same
// surface/channel appends a fresh row; at most one ACTIVE row per place;
// the history persists indefinitely.
func TestHomieBindingsHistory(t *testing.T) {
	db := openSpine(t)
	mkHomieSession(t, db, "h1")
	mkHomieSession(t, db, "h2")
	mustExec(t, db, `INSERT INTO homie_bindings (session_id, surface, channel_ref) VALUES ('h1', 'discord', 'chan-1')`)

	// A second ACTIVE binding for the same place is refused.
	wantAbort(t, db, `INSERT INTO homie_bindings (session_id, surface, channel_ref) VALUES ('h1', 'discord', 'chan-1')`)
	// The place is globally unambiguous: a different active session cannot
	// claim the same Discord/dashboard/CLI destination either.
	wantAbort(t, db, `INSERT INTO homie_bindings (session_id, surface, channel_ref) VALUES ('h2', 'discord', 'chan-1')`)
	// Bind-event identity is immutable; only active -> inactive is legal.
	wantAbort(t, db, `UPDATE homie_bindings SET channel_ref = 'chan-2' WHERE session_id = 'h1'`)

	// Session ends: active bindings clear…
	mustExec(t, db, `UPDATE homie_sessions SET status = 'ended' WHERE id = 'h1'`)
	if active := oneInt(t, db, `SELECT active FROM homie_bindings WHERE session_id = 'h1'`); active != 0 {
		t.Fatalf("ended session retained active binding: %d", active)
	}
	wantAbort(t, db, `UPDATE homie_bindings SET active = 1 WHERE session_id = 'h1'`)
	wantAbort(t, db, `INSERT INTO homie_bindings (session_id, surface, channel_ref) VALUES ('h1', 'dashboard', 'ended-place')`)
	// …and a resume from the same surface creates a FRESH binding row — the
	// primary resume flow must be storable.
	mustExec(t, db, `UPDATE homie_sessions SET status = 'active' WHERE id = 'h1'`)
	mustExec(t, db, `INSERT INTO homie_bindings (session_id, surface, channel_ref) VALUES ('h1', 'discord', 'chan-1')`)
	if n := oneInt(t, db, `SELECT COUNT(*) FROM homie_bindings WHERE session_id = 'h1' AND surface = 'discord' AND channel_ref = 'chan-1'`); n != 2 {
		t.Fatalf("bind-event rows = %d, want 2 (history, one per bind)", n)
	}

	// Bindings history persists indefinitely.
	wantAbort(t, db, `DELETE FROM homie_bindings`)
	wantAbort(t, db, `DELETE FROM homie_bindings WHERE active = 0`)
}

func TestConversationRowsAppendOnly(t *testing.T) {
	db := openSpine(t)
	mkHomieSession(t, db, "h1")
	wantAbort(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body, attachments)
		 VALUES ('h1', 90, 'inbound', 'discord', 'bad', 'not-json')`)
	wantAbort(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body, attachments)
		 VALUES ('h1', 91, 'inbound', 'discord', 'bad', '{}')`)
	mustExec(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body)
		 VALUES ('h1', 1, 'inbound', 'discord', 'hello')`)

	// Content is immutable…
	wantAbort(t, db, `UPDATE conversation_messages SET body = 'edited' WHERE session_id = 'h1' AND seq = 1`)
	wantAbort(t, db, `UPDATE conversation_messages SET direction = 'reply' WHERE session_id = 'h1' AND seq = 1`)
	wantAbort(t, db, `DELETE FROM conversation_messages`)
	// …while the runner's fenced claim state may advance.
	mustExec(t, db,
		`UPDATE conversation_messages SET claimed_by = 'runner-1', claimed_at = datetime('now')
		 WHERE session_id = 'h1' AND seq = 1`)
	mustExec(t, db,
		`UPDATE conversation_messages SET completed_at = datetime('now')
		 WHERE session_id = 'h1' AND seq = 1`)
}

func TestOutboxPayloadIsJSONObject(t *testing.T) {
	db := openSpine(t)
	wantAbort(t, db, `INSERT INTO outbox (kind, surface, payload) VALUES ('health', 'email', '{"status":"ok"}')`)
	wantAbort(t, db, `INSERT INTO outbox (kind, surface, payload) VALUES ('health', 'dashboard', 'not-json')`)
	wantAbort(t, db, `INSERT INTO outbox (kind, surface, payload) VALUES ('health', 'dashboard', '[]')`)
	mustExec(t, db, `INSERT INTO outbox (kind, surface, payload) VALUES ('health', 'dashboard', '{"status":"ok"}')`)
}

// One activity fans out at most once to each tagged
// destination. Both columns permit NULL, but a
// keyed row must carry the pair and the event/destination key is globally
// unique.
func TestOutboxDispatchDestinationBackstops(t *testing.T) {
	assertOutboxDestinationBackstops(t, openSpine(t))
}

// assertOutboxDestinationBackstops checks the outbox destination constraints.
func assertOutboxDestinationBackstops(t *testing.T, db *sql.DB) {
	t.Helper()
	res := mustExec(t, db, `INSERT INTO activity (actor, kind, detail) VALUES ('mc', 'dispatch.health', '{}')`)
	activityID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	eventKey := strings.Repeat("f", 64)

	mustExec(t, db, `
		INSERT INTO outbox (kind, surface, payload, source_activity_id, event_destination_key)
		VALUES ('health', 'dashboard', '{"status":"unavailable"}', ?, ?)`, activityID, eventKey)
	wantAbort(t, db, `
		INSERT INTO outbox (kind, surface, payload, source_activity_id, event_destination_key)
		VALUES ('health', 'cli', '{"status":"unavailable"}', ?, ?)`, activityID, eventKey)
	wantAbort(t, db, `
		INSERT INTO outbox (kind, surface, payload, source_activity_id)
		VALUES ('health', 'dashboard', '{"status":"unavailable"}', ?)`, activityID)
	wantAbort(t, db, `
		INSERT INTO outbox (kind, surface, payload, event_destination_key)
		VALUES ('health', 'dashboard', '{"status":"unavailable"}', ?)`, strings.Repeat("1", 64))
	wantAbort(t, db, `
		INSERT INTO outbox (kind, surface, payload, source_activity_id, event_destination_key)
		VALUES ('health', 'dashboard', '{"status":"unavailable"}', 999999, ?)`, strings.Repeat("2", 64))
	wantAbort(t, db, `
		INSERT INTO outbox (kind, surface, payload, source_activity_id, event_destination_key)
		VALUES ('health', 'dashboard', '{"status":"unavailable"}', ?, ?)`, activityID, strings.Repeat("F", 64))

	// The destination key carries activity's NUL fence, for the same reason.
	for name, key := range map[string]string{
		"nul_tail": strings.Repeat("4", 64) + "\x00EVIL",
		"nul_pad":  strings.Repeat("4", 63) + "\x00",
	} {
		t.Run("event_destination_key_"+name, func(t *testing.T) {
			wantAbort(t, db, `
				INSERT INTO outbox (kind, surface, payload, source_activity_id, event_destination_key)
				VALUES ('health', 'dashboard', '{"status":"unavailable"}', ?, ?)`, activityID, key)
		})
	}

	// A BLOB key passes every CHECK and stores
	// as a UNIQUE value distinct from its TEXT twin.
	for name, key := range map[string][]byte{
		"hex_blob": []byte(strings.Repeat("5", 64)),
		"nul_blob": append([]byte(strings.Repeat("5", 63)), 0x00),
	} {
		t.Run("event_destination_key_"+name, func(t *testing.T) {
			wantAbort(t, db, `
				INSERT INTO outbox (kind, surface, payload, source_activity_id, event_destination_key)
				VALUES ('health', 'dashboard', '{"status":"unavailable"}', ?, ?)`, activityID, key)
		})
	}

	// The existing append-only content fence covers the new provenance too.
	wantAbort(t, db, `UPDATE outbox SET source_activity_id = NULL`)
	wantAbort(t, db, `UPDATE outbox SET event_destination_key = ?`, strings.Repeat("3", 64))
}

// Takeover finding — the outbox was the only communication table without a
// substrate fence: "nothing pushed is ever lost" and ack idempotency
// lived solely in verbs code. Content is immutable, rows never delete, and
// delivered_at moves NULL→value exactly once.
func TestOutboxDeliveryBookkeepingIsFenced(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO outbox (kind, surface, payload) VALUES ('health', 'dashboard', '{"status":"ok"}')`)

	wantAbort(t, db, `DELETE FROM outbox`)
	wantAbort(t, db, `UPDATE outbox SET kind = 'blocked_alert'`)
	wantAbort(t, db, `UPDATE outbox SET surface = 'cli'`)
	wantAbort(t, db, `UPDATE outbox SET channel_ref = 'c9'`)
	wantAbort(t, db, `UPDATE outbox SET payload = '{"forged":true}'`)
	wantAbort(t, db, `UPDATE outbox SET created_at = datetime('now', '+1 hour')`)

	// delivered_at is one-way and set-once (ack replay preserves the original).
	mustExec(t, db, `UPDATE outbox SET delivered_at = datetime('now')`)
	wantAbort(t, db, `UPDATE outbox SET delivered_at = NULL`)
	wantAbort(t, db, `UPDATE outbox SET delivered_at = datetime('now', '+1 hour')`)
	wantAbort(t, db, `DELETE FROM outbox`)
}

// Takeover finding — conversation rows had no active-session backstop: the
// domain layer refuses, but a resurrected runner replaying a reply append
// onto an ended/reaped session met no storage rejection (schema contract:
// "a bug in mc cannot write an illegal state").
func TestConversationRowsRequireActiveSession(t *testing.T) {
	db := openSpine(t)
	mkHomieSession(t, db, "h-conv")
	mustExec(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body)
		 VALUES ('h-conv', 1, 'inbound', 'discord', 'hello')`)

	mustExec(t, db, `UPDATE homie_sessions SET status = 'ended' WHERE id = 'h-conv'`)
	wantAbort(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body)
		 VALUES ('h-conv', 2, 'reply', 'homie', 'ghost turn')`)
	mustExec(t, db, `UPDATE homie_sessions SET status = 'reaped' WHERE id = 'h-conv'`)
	wantAbort(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body)
		 VALUES ('h-conv', 2, 'inbound', 'discord', 'ghost inbound')`)
	// The permanent transcript survives the session ending untouched.
	if n := oneInt(t, db, `SELECT COUNT(*) FROM conversation_messages WHERE session_id = 'h-conv'`); n != 1 {
		t.Fatalf("transcript rows = %d, want 1", n)
	}
}

// The claim/reply lattice — the storage law behind "runner death cannot
// create two logical replies to one inbound turn": a reply names the
// inbound turn it answers, exactly once, within its own conversation; claim
// and completion state are one-way and set-once.
func TestConversationClaimReplyLattice(t *testing.T) {
	db := openSpine(t)
	mkHomieSession(t, db, "h-cr")
	mkHomieSession(t, db, "h-cr2")
	mustExec(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body)
		 VALUES ('h-cr', 1, 'inbound', 'discord', 'question')`)
	turn := oneInt(t, db, `SELECT id FROM conversation_messages WHERE session_id = 'h-cr' AND seq = 1`)

	// A reply names its inbound turn; inbound rows never carry reply_to.
	wantAbort(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body)
		 VALUES ('h-cr', 2, 'reply', 'homie', 'orphan reply')`)
	wantAbort(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body, reply_to)
		 VALUES ('h-cr', 2, 'inbound', 'discord', 'inbound with reply_to', ?)`, turn)
	wantAbort(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body, reply_to)
		 VALUES ('h-cr', 2, 'reply', 'homie', 'dangling', 999)`)
	// …and only an inbound turn of its own conversation.
	wantAbort(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body, reply_to)
		 VALUES ('h-cr2', 1, 'reply', 'homie', 'cross-conversation', ?)`, turn)

	// Claim columns travel as a pair, and completion requires a claim.
	wantAbort(t, db, `UPDATE conversation_messages SET claimed_by = 'h-cr' WHERE id = ?`, turn)
	wantAbort(t, db, `UPDATE conversation_messages SET completed_at = datetime('now') WHERE id = ?`, turn)
	mustExec(t, db,
		`UPDATE conversation_messages SET claimed_by = 'h-cr', claimed_at = datetime('now') WHERE id = ?`, turn)

	// Claims are set-once: a fresh runner resumes the durable claim, never
	// rewrites or clears it.
	wantAbort(t, db, `UPDATE conversation_messages SET claimed_by = 'other' WHERE id = ?`, turn)
	wantAbort(t, db, `UPDATE conversation_messages SET claimed_at = datetime('now', '+1 hour') WHERE id = ?`, turn)
	wantAbort(t, db, `UPDATE conversation_messages SET claimed_by = NULL, claimed_at = NULL WHERE id = ?`, turn)

	mustExec(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body, reply_to)
		 VALUES ('h-cr', 2, 'reply', 'homie', 'answer', ?)`, turn)
	reply := oneInt(t, db, `SELECT id FROM conversation_messages WHERE session_id = 'h-cr' AND seq = 2`)

	// One logical reply per inbound turn, immutable linkage, and reply rows
	// are never themselves claimable.
	wantAbort(t, db,
		`INSERT INTO conversation_messages (session_id, seq, direction, surface, body, reply_to)
		 VALUES ('h-cr', 3, 'reply', 'homie', 'second reply', ?)`, turn)
	wantAbort(t, db, `UPDATE conversation_messages SET reply_to = NULL WHERE id = ?`, reply)
	wantAbort(t, db,
		`UPDATE conversation_messages SET claimed_by = 'h-cr', claimed_at = datetime('now') WHERE id = ?`, reply)

	// Completion is set-once and never clears.
	mustExec(t, db, `UPDATE conversation_messages SET completed_at = datetime('now') WHERE id = ?`, turn)
	wantAbort(t, db, `UPDATE conversation_messages SET completed_at = datetime('now', '+1 hour') WHERE id = ?`, turn)
	wantAbort(t, db, `UPDATE conversation_messages SET completed_at = NULL WHERE id = ?`, turn)
}

// ---------------------------------------------------------------------------
// The additive columns — new-column coverage only; every case above covers
// the core schema and stays untouched.
// ---------------------------------------------------------------------------

// The verdict record on the Verifier's own runs row — outcome
// vocabulary CHECK-pinned, evidence/correction paths free-form.
func TestRunsVerdictRecordColumns(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "worked")
	mustExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('v1', 'pipeline', 'verifier', 'ws', ?)`, id)
	mustExec(t, db, `
		UPDATE runs SET verdict_outcome = 'correct', evidence_path = 'e.md',
		       correction_path = 'corrections/mc-1-corrections1'
		WHERE id = 'v1'`)
	wantAbort(t, db, `UPDATE runs SET verdict_outcome = 'maybe' WHERE id = 'v1'`)
}

func TestRunSessionLocatorsRegisterOnce(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO runs (id, tier, role) VALUES ('r1', 'pipeline', 'worker')`)
	wantAbort(t, db, `UPDATE runs SET native_session_ref='session-only' WHERE id='r1'`)
	mustExec(t, db, `UPDATE runs SET native_session_ref='session-1', trace_filename='native.jsonl' WHERE id='r1'`)
	mustExec(t, db, `UPDATE runs SET native_session_ref='session-1', trace_filename='native.jsonl' WHERE id='r1'`)
	wantAbort(t, db, `UPDATE runs SET native_session_ref='session-2', trace_filename='other.jsonl' WHERE id='r1'`)
}

// The identity constraints permit updates to the revision notes.
func TestTasksRevisionNotesColumn(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "packaged")
	mustExec(t, db, `UPDATE tasks SET revision_notes = 'tighten the abstract' WHERE id = ?`, id)
	if got := taskStr(t, db, id, "revision_notes"); got != "tighten the abstract" {
		t.Fatalf("revision_notes = %q", got)
	}
	mustExec(t, db, `UPDATE tasks SET revision_notes = NULL WHERE id = ?`, id)
}

func TestVerifierAllowanceBoundsAndConditionOwnership(t *testing.T) {
	db := openSpine(t)
	id := mkTask(t, db, "task", "seeded")
	var corrections, open, owned int
	if err := db.QueryRow(`SELECT correction_count,verifier_correction_open,blocked_kind IS NOT NULL FROM tasks WHERE id=?`, id).Scan(&corrections, &open, &owned); err != nil {
		t.Fatal(err)
	}
	if corrections != 0 || open != 0 || owned != 0 {
		t.Fatal("A new task must start with an unused allowance.")
	}
	wantAbort(t, db, `UPDATE tasks SET correction_count=4 WHERE id=?`, id)
	wantAbort(t, db, `UPDATE tasks SET blocked=1,blocked_kind='allowance',blocked_reason='The allowance ended.' WHERE id=?`, id)
	wantAbort(t, db, `UPDATE tasks SET allowance_condition='merge' WHERE id=?`, id)
}

// wikiStateOf applies the wiki state rule to the stored rows: commit equality only.
func wikiStateOf(t *testing.T, db *sql.DB, project string) string {
	t.Helper()
	if oneInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project=? AND ended_at IS NULL`, project) > 0 {
		return "update-active"
	}
	var accepted, start, wiki string
	var destination, merged sql.NullString
	if err := db.QueryRow(`SELECT accepted_commit FROM accepted_commits WHERE project=?`, project).Scan(&accepted); err == sql.ErrNoRows {
		return "behind"
	} else if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT start_commit,wiki_commit,destination_commit,merged_commit FROM wiki_updates
		WHERE project=? AND result='succeeded' ORDER BY id DESC LIMIT 1`, project).Scan(&start, &wiki, &destination, &merged); err == sql.ErrNoRows {
		return "behind"
	} else if err != nil {
		t.Fatal(err)
	}
	if merged.Valid {
		if destination.String == start && accepted == merged.String {
			return "current"
		}
		return "behind"
	}
	if wiki == start && accepted == start {
		return "current"
	}
	return "behind"
}

// The accepted commit, the wiki update, and the two customization triggers
// that accept a wiki update run.
func TestWikiSchemaObjects(t *testing.T) {
	db := openSpine(t)
	for _, project := range []string{"p1", "p2", "p3", "p4"} {
		mkProject(t, db, project)
	}
	c1, c2, c3, c4, c5 := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40),
		strings.Repeat("d", 40), strings.Repeat("e", 40)
	const (
		check     = "CHECK constraint failed"
		requires  = "a wiki update requires its own active Scribe run with no task and no lease"
		afterRun  = "a wiki update ends after its run"
		preserved = "the wiki update preserves its assignment and its end"
	)
	scribeRun := func(run, project string) {
		t.Helper()
		mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
			VALUES(?,'pipeline','scribe',?,NULL,?,'fake/fake')`, run, project, "sessions/"+run)
	}
	wikiUpdate := func(run, project, start string) {
		t.Helper()
		scribeRun(run, project)
		mustExec(t, db, `INSERT INTO wiki_updates(project,run_id,start_commit,started_at,merge_marker,boot_id,acquired_uptime_ms,deadline_uptime_ms)
			VALUES(?,?,?,datetime('now'),0,'boot',1000,900000)`, project, run, start)
	}
	endRun := func(run, outcome string, reason any) {
		t.Helper()
		mustExec(t, db, `UPDATE runs SET ended_at=datetime('now'), outcome=?, end_reason=? WHERE id=?`, outcome, reason, run)
	}
	wantState := func(project, want, why string) {
		t.Helper()
		if got := wikiStateOf(t, db, project); got != want {
			t.Fatalf("wiki state of %s = %s, want %s: %s", project, got, want, why)
		}
	}
	wantRows := func(why, want, q string, args ...any) {
		t.Helper()
		rows, err := db.Query(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := []string{}
		for rows.Next() {
			var row sql.NullString
			if err := rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			got = append(got, row.String)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, ",") != want {
			t.Fatalf("%s: rows = %v, want %s", why, got, want)
		}
	}

	// The setting.
	if got := oneInt(t, db, `SELECT wiki_update_interval_s FROM settings WHERE id=1`); got != 3600 {
		t.Fatalf("default wiki update interval = %d, want 3600", got)
	}
	wantMessage(t, db, check, `UPDATE settings SET wiki_update_interval_s=0`)
	mustExec(t, db, `UPDATE settings SET wiki_update_interval_s=5`)

	// The accepted commit. An equal observation writes nothing.
	const observe = `INSERT INTO accepted_commits(project,accepted_commit,observed_at) VALUES(?,?,?)
		ON CONFLICT(project) DO UPDATE SET accepted_commit=excluded.accepted_commit, observed_at=excluded.observed_at
		WHERE accepted_commits.accepted_commit IS NOT excluded.accepted_commit`
	mustExec(t, db, observe, "p1", c1, "2026-01-01 00:00:00")
	mustExec(t, db, observe, "p1", c1, "2026-01-02 00:00:00")
	if got := oneStr(t, db, `SELECT observed_at FROM accepted_commits WHERE project='p1'`); got != "2026-01-01 00:00:00" {
		t.Fatalf("observation time after an equal observation = %s, want no change", got)
	}
	mustExec(t, db, observe, "p1", c2, "2026-01-03 00:00:00")
	if got := oneStr(t, db, `SELECT accepted_commit FROM accepted_commits WHERE project='p1'`); got != c2 {
		t.Fatalf("accepted commit = %s, want the new observation", got)
	}
	wantMessage(t, db, check, observe, "p2", strings.Repeat("A", 40), "t")
	wantMessage(t, db, check, observe, "p2", strings.Repeat("a", 39), "t")
	wantMessage(t, db, "FOREIGN KEY constraint failed", observe, "absent", c1, "t")
	wantState("p1", "behind", "a Project with no successful wiki update")

	// The insert order: the run, the wiki update, the customization record.
	wikiUpdate("run1", "p1", c1)
	const customization = `INSERT INTO run_customizations(run_id,project,role,revision,standard_directive,standard_digest)
		VALUES(?,?,?,?,'directive','digest')`
	mustExec(t, db, customization, "run1", "p1", "scribe", nil)
	if got := oneInt(t, db, `SELECT COUNT(*) FROM leases WHERE run_id IS NOT NULL`); got != 0 {
		t.Fatalf("held leases = %d, want 0: a wiki update run holds no lease", got)
	}
	wantState("p1", "update-active", "an active wiki update")

	// One active wiki update for each Project.
	scribeRun("run2", "p1")
	const insert = `INSERT INTO wiki_updates(project,run_id,start_commit,started_at,boot_id,acquired_uptime_ms,deadline_uptime_ms)
		VALUES(?,?,?,datetime('now'),'boot',1,2)`
	wantMessage(t, db, "UNIQUE constraint failed", insert, "p1", "run2", c1)
	wikiUpdate("run3", "p2", c1)

	// The run of a wiki update: its role, its binding, its Project, its task, its lease, and its end.
	task := mkTaskIn(t, db, "p2", "task", "proposed")
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,subject,binding) VALUES('worker1','pipeline','worker','p2',?,'fake/fake')`, task)
	const claim = `UPDATE leases SET run_id=?,subject=?,owner='worker',acquired_at=datetime('now'),boot_id='boot',
		acquired_uptime_ms=1,deadline_uptime_ms=2 WHERE project=?`
	mustExec(t, db, claim, "worker1", task, "p2")
	mustExec(t, db, `INSERT INTO role_customizations(project,role,revision,instructions,author) VALUES('p2','worker',1,'Write short code.','operator')`)
	mustExec(t, db, customization, "worker1", "p2", "worker", 1)
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,binding) VALUES('worker2','pipeline','worker','p3','fake/fake')`)
	wantMessage(t, db, requires, insert, "p3", "worker2", c1)
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,binding) VALUES('mech1','pipeline','scribe','p3','mechanical')`)
	wantMessage(t, db, requires, insert, "p3", "mech1", c1)
	scribeRun("run4", "p3")
	wantMessage(t, db, requires, insert, "p2", "run4", c1)
	wantMessage(t, db, requires, insert, "p4", "run4", c1)
	wantMessage(t, db, check, insert, "p3", "run4", "short")
	wantMessage(t, db, requires, `INSERT INTO wiki_updates(project,run_id,start_commit,started_at,boot_id,acquired_uptime_ms,deadline_uptime_ms,counted)
		VALUES('p3','run4',?,datetime('now'),'boot',1,2,0)`, c1)
	wantMessage(t, db, requires, `INSERT INTO wiki_updates(project,run_id,start_commit,started_at,boot_id,acquired_uptime_ms,deadline_uptime_ms,wiki_commit)
		VALUES('p3','run4',?,datetime('now'),'boot',1,2,?)`, c1, c2)
	mustExec(t, db, claim, "run4", nil, "p3")
	wantMessage(t, db, requires, insert, "p3", "run4", c1)
	mustExec(t, db, `UPDATE leases SET run_id=NULL,owner=NULL,subject=NULL,acquired_at=NULL,last_heartbeat_at=NULL,boot_id=NULL,
		acquired_uptime_ms=NULL,heartbeat_uptime_ms=NULL,deadline_uptime_ms=NULL WHERE project='p3'`)
	endRun("run4", "completed", nil)
	wantMessage(t, db, requires, insert, "p3", "run4", c1)
	wantMessage(t, db, "a Scribe run has no task", `INSERT INTO runs(id,tier,role,project,subject,binding)
		VALUES('bad','pipeline','scribe','p2',?,'fake/fake')`, task)
	wantMessage(t, db, check, `UPDATE leases SET owner='scribe' WHERE project='p2'`)

	// A customization record requires a lease or an active wiki update.
	mustExec(t, db, `INSERT INTO runs(id,tier,role,project,binding) VALUES('scribe9','pipeline','scribe','p3','fake/fake')`)
	wantMessage(t, db, "customization input requires its assigned active run", customization, "scribe9", "p3", "scribe", nil)

	// The heartbeat stamp holds the time and the uptime together.
	wantMessage(t, db, check, `UPDATE wiki_updates SET last_heartbeat_at=datetime('now') WHERE run_id='run1'`)
	mustExec(t, db, `UPDATE wiki_updates SET last_heartbeat_at=datetime('now'), heartbeat_uptime_ms=2000 WHERE run_id='run1'`)

	// A wiki update run gets no failure record: it holds no lease and it is no execution.
	wantMessage(t, db, "an infrastructure failure requires its assigned active run",
		`INSERT INTO infrastructure_failures(run_id,task_id,allowance,condition,source) VALUES('run1',NULL,'project','failed','resident')`)

	// The customization conflict: a wiki update run gets a cleared row, and a lease run gets an active row.
	mustExec(t, db, `INSERT INTO role_customizations(project,role,revision,instructions,author) VALUES('p2','scribe',1,'Write short pages.','operator')`)
	mustExec(t, db, customization, "run3", "p2", "scribe", 1)
	const conflict = `INSERT INTO customization_conflicts(run_id,project_title,passage,requirement,reason,cleared_at,cleared_by,resolution)
		VALUES(?,'p2',?,'the directive','conflict',?,?,?)`
	wantMessage(t, db, "the conflict of a wiki update run sets no role block", conflict, "run3", "short pages", nil, nil, nil)
	wantMessage(t, db, "a conflict requires the custom instructions of its active run", conflict, "run3", "long pages",
		"2026-01-01 00:00:00", "mc", "The wiki update ended as failed.")
	mustExec(t, db, conflict, "run3", "short pages", "2026-01-01 00:00:00", "mc", "The wiki update ended as failed.")
	mustExec(t, db, conflict, "worker1", "short code", nil, nil, nil)
	if got := oneInt(t, db, `SELECT COUNT(*) FROM customization_conflicts c JOIN run_customizations r ON r.run_id=c.run_id
		WHERE c.cleared_at IS NULL AND r.role='scribe'`); got != 0 {
		t.Fatalf("active conflicts of the Scribe role = %d, want 0", got)
	}
	if got := oneInt(t, db, `SELECT COUNT(*) FROM customization_conflicts WHERE cleared_at IS NULL`); got != 1 {
		t.Fatalf("active conflicts = %d, want the conflict of the Worker run", got)
	}

	// The end, the stop confirmation, and the wiki commit follow the end of the run.
	const end = `UPDATE wiki_updates SET ended_at=datetime('now'), result=?, cause=?, wiki_commit=?, counted=? WHERE run_id=?`
	wantMessage(t, db, afterRun, end, "succeeded", nil, c1, 1, "run1")
	wantMessage(t, db, afterRun, `UPDATE wiki_updates SET stopped_at=datetime('now') WHERE run_id='run1'`)
	wantMessage(t, db, afterRun, `UPDATE wiki_updates SET wiki_commit=? WHERE run_id='run1'`, c2)
	endRun("run1", "completed", nil)
	wantMessage(t, db, check, `UPDATE wiki_updates SET last_heartbeat_at=NULL WHERE run_id='run1'`)

	// The result, the cause, the count, and the commits of an end.
	wantMessage(t, db, check, end, "failed", nil, nil, 1, "run1")
	wantMessage(t, db, check, end, "succeeded", "a cause", c1, 1, "run1")
	wantMessage(t, db, check, end, "succeeded", nil, c1, 0, "run1")
	wantMessage(t, db, check, end, "succeeded", nil, nil, 1, "run1")
	wantMessage(t, db, check, end, "succeeded", nil, c2, 1, "run1")
	wantMessage(t, db, check, end, "failed", "  ", nil, 1, "run1")
	wantMessage(t, db, check, end, "failed", strings.Repeat("x", 2049), nil, 1, "run1")
	wantMessage(t, db, check, end, "stopped", nil, c1, 1, "run1")
	wantMessage(t, db, check, `UPDATE wiki_updates SET ended_at=datetime('now'), result='succeeded', wiki_commit=?, merged_commit=?,
		destination_commit=? WHERE run_id='run1'`, c1, c3, c1)
	wantMessage(t, db, check, `UPDATE wiki_updates SET ended_at=datetime('now'), result='succeeded', wiki_commit=?, merged_commit=?
		WHERE run_id='run1'`, c2, c3)
	wantMessage(t, db, check, `UPDATE wiki_updates SET merge_after=datetime('now') WHERE run_id='run1'`)
	// An active wiki update holds no wiki commit that equals its start commit.
	wantMessage(t, db, check, `UPDATE wiki_updates SET wiki_commit=? WHERE run_id='run1'`, c1)

	// A successful wiki update with no wiki change. Its end and its assignment stay.
	mustExec(t, db, end, "succeeded", nil, c1, 1, "run1")
	mustExec(t, db, `UPDATE wiki_updates SET stopped_at=datetime('now') WHERE run_id='run1'`)
	wantMessage(t, db, preserved, `UPDATE wiki_updates SET stopped_at=datetime('now','+1 day') WHERE run_id='run1'`)
	wantMessage(t, db, preserved, `UPDATE wiki_updates SET result='failed', cause='late' WHERE run_id='run1'`)
	wantMessage(t, db, preserved, `UPDATE wiki_updates SET last_heartbeat_at=datetime('now','+1 day') WHERE run_id='run1'`)
	wantMessage(t, db, preserved, `UPDATE wiki_updates SET start_commit=? WHERE run_id='run1'`, c2)
	wantMessage(t, db, preserved, `UPDATE wiki_updates SET merge_marker=4 WHERE run_id='run1'`)
	wantMessage(t, db, preserved, `UPDATE wiki_updates SET started_at='2020-01-01 00:00:00' WHERE run_id='run1'`)
	wantMessage(t, db, "the wiki update preserves its history", `DELETE FROM wiki_updates WHERE run_id='run1'`)

	wantState("p1", "behind", "the accepted commit differs from the start commit")
	mustExec(t, db, observe, "p1", c1, "t")
	wantState("p1", "current", "no wiki change and an equal accepted commit")

	// The next wiki update of the Project: the wiki commit waits for its merge, then the merge ends it.
	mustExec(t, db, observe, "p1", c2, "t")
	wikiUpdate("run5", "p1", c2)
	endRun("run5", "completed", nil)
	mustExec(t, db, `UPDATE wiki_updates SET wiki_commit=? WHERE run_id='run5'`, c3)
	wantMessage(t, db, preserved, `UPDATE wiki_updates SET wiki_commit=? WHERE run_id='run5'`, c4)
	mustExec(t, db, `UPDATE wiki_updates SET merge_after=datetime('now','+30 seconds') WHERE run_id='run5'`)
	mustExec(t, db, `UPDATE wiki_updates SET merge_after=NULL WHERE run_id='run5'`)
	wantMessage(t, db, check, `UPDATE wiki_updates SET destination_commit=? WHERE run_id='run5'`, c2)
	wantState("p1", "update-active", "a wiki update that waits for its merge")
	mustExec(t, db, `UPDATE wiki_updates SET ended_at=datetime('now'), result='succeeded', destination_commit=?, merged_commit=?
		WHERE run_id='run5'`, c2, c4)
	wantState("p1", "behind", "the accepted commit is the start commit, not the commit of the merge")
	mustExec(t, db, observe, "p1", c4, "t")
	wantState("p1", "current", "the commit of the merge onto the start commit is the accepted commit")
	mustExec(t, db, observe, "p1", c5, "t")
	wantState("p1", "behind", "a later accepted commit")

	// A failure that the interval rule does not count.
	endRun("run3", "infra-failed", "connection")
	mustExec(t, db, end, "failed", "The connection changed or became unavailable before launch.", nil, 0, "run3")

	// A refused candidate: the start and the end in one transaction, with the stop confirmation.
	refused, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Rollback()
	for _, step := range []string{
		`INSERT INTO runs(id,tier,role,project,subject,session_path,binding) VALUES('run6','pipeline','scribe','p2',NULL,'sessions/run6','fake/fake')`,
		`INSERT INTO wiki_updates(project,run_id,start_commit,started_at,merge_marker,boot_id,acquired_uptime_ms,deadline_uptime_ms)
			VALUES('p2','run6','` + c1 + `',datetime('now'),0,'boot',1000,900000)`,
		`UPDATE runs SET ended_at=datetime('now'), outcome='infra-failed', end_reason='launch' WHERE id='run6'`,
		`UPDATE wiki_updates SET ended_at=datetime('now'), result='failed', cause='Culler refused the start of the Scribe run.',
			stopped_at=datetime('now') WHERE run_id='run6'`,
	} {
		if _, err := refused.Exec(step); err != nil {
			t.Fatalf("refused candidate:\n  %s\n  %v", step, err)
		}
	}
	if err := refused.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := oneInt(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE ended_at IS NULL`); got != 0 {
		t.Fatalf("active wiki updates = %d, want 0", got)
	}

	// A failed check of the wiki commit: the wiki update fails and keeps the submitted commit.
	wikiUpdate("run8", "p3", c1)
	endRun("run8", "completed", nil)
	mustExec(t, db, `UPDATE wiki_updates SET ended_at=datetime('now'), result='failed',
		cause='The wiki commit changes a path outside openwiki/: a.txt.', wiki_commit=?, stopped_at=datetime('now') WHERE run_id='run8'`, c2)

	// The recovery read: an ended run, no end, no wiki commit.
	wikiUpdate("run9", "p3", c1)
	endRun("run9", "interrupted", "interrupted")
	const recovery = `SELECT w.run_id FROM wiki_updates w JOIN runs r ON r.id=w.run_id
		WHERE w.project=? AND w.ended_at IS NULL AND w.wiki_commit IS NULL AND r.ended_at IS NOT NULL`
	wantRows("the recovery read before the end", "run9", recovery, "p3")
	mustExec(t, db, end, "failed", "The Scribe run ended with no result.", nil, 1, "run9")
	wantRows("the recovery read after the end", "", recovery, "p3")
	wantRows("the open stop confirmations", "run9", `SELECT w.run_id FROM wiki_updates w JOIN runs r ON r.id=w.run_id
		WHERE w.project='p3' AND w.stopped_at IS NULL AND r.ended_at IS NOT NULL ORDER BY w.id`)

	// The reads of the due decision.
	wantRows("the last wiki update", "run6|failed|1",
		`SELECT run_id || '|' || result || '|' || counted FROM wiki_updates WHERE project='p2' ORDER BY id DESC LIMIT 1`)
	wantRows("the last successful wiki update", "run5|"+c2+"|"+c3+"|"+c2+"|"+c4,
		`SELECT run_id || '|' || start_commit || '|' || wiki_commit || '|' || destination_commit || '|' || merged_commit
		FROM wiki_updates WHERE project='p1' AND result='succeeded' ORDER BY id DESC LIMIT 1`)
	wantRows("the last counted wiki update", "run6", `SELECT run_id FROM wiki_updates WHERE project='p2' AND counted=1 ORDER BY id DESC LIMIT 1`)
	if got := oneInt(t, db, `SELECT COALESCE(MAX(r.created_seq),0) FROM merge_operations m JOIN runs r ON r.id=m.run_id
		WHERE m.project='p1' AND m.operation='apply' AND m.finished=1 AND m.destination_branch='main'
		  AND m.task_id IS NOT NULL AND json_extract(m.result,'$.applied.commit') IS NOT NULL`); got != 0 {
		t.Fatalf("merge marker of a Project with no merge = %d, want 0", got)
	}

	wantRows("the foreign key check", "", `SELECT "table" FROM pragma_foreign_key_check`)
	wantRows("the integrity check", "ok", `SELECT integrity_check FROM pragma_integrity_check`)
}
