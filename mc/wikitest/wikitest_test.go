package wikitest_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"mc/domain"
	"mc/substrate"
	"mc/wikitest"
)

func openSpine(t *testing.T) *sql.DB {
	t.Helper()
	db, err := substrate.Open(filepath.Join(t.TempDir(), "spine.db"))
	if err != nil {
		t.Fatalf("open spine: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := substrate.Init(db); err != nil {
		t.Fatalf("init spine: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO sandbox_profiles (id, workspace_root) VALUES ('ws-profile', '/workspaces/ws')`,
		`INSERT INTO projects (id, title, sandbox_profile) VALUES ('ws', 'Project ws', 'ws-profile')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed Project: %v", err)
		}
	}
	return db
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}

func wikiState(t *testing.T, db *sql.DB) domain.WikiState {
	t.Helper()
	state, err := domain.WikiStateOf(context.Background(), db, "ws")
	if err != nil {
		t.Fatalf("wiki state: %v", err)
	}
	return state
}

func TestWikitestHelper(t *testing.T) {
	db := openSpine(t)
	ctx := context.Background()
	c1, c2, c3 := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	// The start time has an offset, so the stored text proves the conversion to UTC.
	started := time.Date(2026, 1, 1, 1, 0, 0, 0, time.FixedZone("offset", 3600))
	const stamp = "2026-01-01 00:00:00"

	first, ok, err := wikitest.RecordCurrentWiki(ctx, db, "ws", c1, started)
	if err != nil || !ok {
		t.Fatalf("first call = %q, %v, %v; want a recorded wiki update", first, ok, err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(first) {
		t.Fatalf("run identifier = %q, want 16 lowercase hexadecimal characters", first)
	}
	var start, wiki, startedAt, endedAt, result, stoppedAt, boot string
	var cause sql.NullString
	var counted, marker int
	if err := db.QueryRow(`SELECT start_commit, wiki_commit, started_at, ended_at, result, cause, counted, stopped_at,
		merge_marker, boot_id FROM wiki_updates WHERE run_id = ?`, first).
		Scan(&start, &wiki, &startedAt, &endedAt, &result, &cause, &counted, &stoppedAt, &marker, &boot); err != nil {
		t.Fatal(err)
	}
	if start != c1 || wiki != c1 || startedAt != stamp || endedAt != stamp || stoppedAt != stamp ||
		result != "succeeded" || cause.Valid || counted != 1 || marker != 0 || boot != "wikitest" {
		t.Fatalf("wiki update row = start %q, wiki %q, started %q, ended %q, stopped %q, result %q, cause %v, counted %d, marker %d, boot %q",
			start, wiki, startedAt, endedAt, stoppedAt, result, cause, counted, marker, boot)
	}
	var tier, role, session, binding, outcome, runEnded string
	var subject, endReason sql.NullString
	if err := db.QueryRow(`SELECT tier, role, subject, session_path, binding, outcome, end_reason, ended_at
		FROM runs WHERE id = ?`, first).
		Scan(&tier, &role, &subject, &session, &binding, &outcome, &endReason, &runEnded); err != nil {
		t.Fatal(err)
	}
	if tier != "pipeline" || role != "scribe" || subject.Valid || session != "sessions/"+first ||
		binding != "fake/fake" || outcome != "completed" || endReason.Valid || runEnded != stamp {
		t.Fatalf("run row = tier %q, role %q, subject %v, session %q, binding %q, outcome %q, end reason %v, ended %q",
			tier, role, subject, session, binding, outcome, endReason, runEnded)
	}
	state := wikiState(t, db)
	if state.Kind != "current" || state.AcceptedCommit != c1 || state.Active != nil || state.Unstopped != "" ||
		state.LastSucceeded == nil || state.LastSucceeded.RunID != first ||
		state.LastCounted == nil || state.LastCounted.StartedAt != stamp {
		t.Fatalf("wiki state after the first call = %+v", state)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM runs WHERE role IS NOT 'scribe'`); n != 0 {
		t.Fatalf("runs with another role = %d, want 0: a count excludes the helper row by its role", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM activity`); n != 0 {
		t.Fatalf("activity records = %d, want 0", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM leases WHERE run_id IS NOT NULL`); n != 0 {
		t.Fatalf("held leases = %d, want 0", n)
	}

	second, ok, err := wikitest.RecordCurrentWiki(ctx, db, "ws", c2, started.Add(2*time.Hour))
	if err != nil || !ok || second == first {
		t.Fatalf("second call = %q, %v, %v; want a second wiki update", second, ok, err)
	}
	state = wikiState(t, db)
	if state.Kind != "current" || state.AcceptedCommit != c2 || state.LastSucceeded.RunID != second ||
		state.LastSucceeded.StartCommit != c2 || state.LastCounted.StartedAt != "2026-01-01 02:00:00" {
		t.Fatalf("wiki state after the second call = %+v, last successful %+v", state, state.LastSucceeded)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project = 'ws'`); n != 2 {
		t.Fatalf("wiki updates = %d, want 2", n)
	}

	// An active wiki update stops statements 2 to 5. Statement 1 still writes.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.StartWikiUpdate(ctx, tx, started.Add(3*time.Hour), domain.WikiStart{
		Project: "ws", RunID: "active", StartCommit: c2, SessionPath: "sessions/active",
		Binding: "fake/fake", HardDeadlineMinutes: 240,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runs := count(t, db, `SELECT COUNT(*) FROM runs`)
	third, ok, err := wikitest.RecordCurrentWiki(ctx, db, "ws", c3, started.Add(4*time.Hour))
	if err != nil || ok || third != "" {
		t.Fatalf("call beside an active wiki update = %q, %v, %v; want no wiki update", third, ok, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM runs`); n != runs {
		t.Fatalf("runs = %d, want %d: the helper wrote a run", n, runs)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM wiki_updates WHERE project = 'ws'`); n != 3 {
		t.Fatalf("wiki updates = %d, want 3", n)
	}
	state = wikiState(t, db)
	if state.Kind != "update-active" || state.AcceptedCommit != c3 || state.Active == nil || state.Active.RunID != "active" {
		t.Fatalf("wiki state beside an active wiki update = %+v", state)
	}
}
