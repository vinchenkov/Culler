package verbs

import (
	"database/sql"
	"strings"
	"testing"
)

const hbLaunch = "aaaaaaaaaaaaaaaa"

var hbContainer = strings.Repeat("c", 64)

func hbSpine(t *testing.T) *sql.DB {
	t.Helper()
	db := dvSpine(t)
	dvExec(t, db, `
		INSERT INTO homie_sessions (id, container_name, verb_allowlist, session_path, binding)
		VALUES ('sess-1', 'mc-homie-sess-1', '[]', 'sessions/sess-1', 'claude')`)
	dvExec(t, db, `UPDATE homie_sessions
		SET current_launch_id = ?, current_launch_mode = 'fresh' WHERE id = 'sess-1'`, hbLaunch)
	return db
}

func hbBind(t *testing.T, db *sql.DB, session, launch, container string) map[string]any {
	t.Helper()
	res, err := HomieLaunchBind(db, nil, session, launch, container)
	if err != nil {
		t.Fatalf("HomieLaunchBind: %v", err)
	}
	return res.(map[string]any)
}

func TestHomieLaunchBind(t *testing.T) {
	t.Run("binds the exact docker id to the current launch", func(t *testing.T) {
		db := hbSpine(t)
		got := hbBind(t, db, "sess-1", hbLaunch, hbContainer)
		if got["fenced"] != true {
			t.Fatalf("fenced = %v, want true", got["fenced"])
		}
		if got["bound_at"] == nil {
			t.Fatal("bound_at not returned")
		}
		var cid, boundAt sql.NullString
		if err := db.QueryRow(
			`SELECT current_container_id, launch_bound_at FROM homie_sessions WHERE id='sess-1'`,
		).Scan(&cid, &boundAt); err != nil {
			t.Fatal(err)
		}
		if cid.String != hbContainer || !boundAt.Valid {
			t.Fatalf("bind did not persist: container=%q bound_at_valid=%v", cid.String, boundAt.Valid)
		}
	})

	t.Run("idempotent: same launch + same container returns the original receipt", func(t *testing.T) {
		db := hbSpine(t)
		first := hbBind(t, db, "sess-1", hbLaunch, hbContainer)
		second := hbBind(t, db, "sess-1", hbLaunch, hbContainer)
		if second["fenced"] != true || second["bound_at"] != first["bound_at"] {
			t.Fatalf("re-bind not idempotent: %v vs %v", second, first)
		}
	})

	t.Run("a different container id is fenced and rebinds nothing", func(t *testing.T) {
		db := hbSpine(t)
		hbBind(t, db, "sess-1", hbLaunch, hbContainer)
		got := hbBind(t, db, "sess-1", hbLaunch, strings.Repeat("d", 64))
		if got["fenced"] != false {
			t.Fatalf("fenced = %v, want false for a different container", got["fenced"])
		}
		var cid string
		if err := db.QueryRow(`SELECT current_container_id FROM homie_sessions WHERE id='sess-1'`).Scan(&cid); err != nil {
			t.Fatal(err)
		}
		if cid != hbContainer {
			t.Fatalf("a fenced bind changed the container to %q", cid)
		}
	})

	t.Run("a stale launch id is fenced", func(t *testing.T) {
		db := hbSpine(t)
		got := hbBind(t, db, "sess-1", "bbbbbbbbbbbbbbbb", hbContainer)
		if got["fenced"] != false {
			t.Fatalf("fenced = %v, want false for a stale launch", got["fenced"])
		}
	})

	t.Run("an idled session refuses the bind", func(t *testing.T) {
		db := hbSpine(t)
		dvExec(t, db, `UPDATE settings SET homie_idle_timeout_s = 1 WHERE id = 1`)
		dvExec(t, db, `UPDATE homie_sessions SET last_activity_at = datetime('now', '-1 hour') WHERE id = 'sess-1'`)
		got := hbBind(t, db, "sess-1", hbLaunch, hbContainer)
		if got["fenced"] != false {
			t.Fatalf("fenced = %v, want false for an idled session", got["fenced"])
		}
		var cid sql.NullString
		if err := db.QueryRow(`SELECT current_container_id FROM homie_sessions WHERE id='sess-1'`).Scan(&cid); err != nil {
			t.Fatal(err)
		}
		if cid.Valid {
			t.Fatal("an idled session must not bind a container")
		}
	})

	t.Run("unknown session errors", func(t *testing.T) {
		db := hbSpine(t)
		if _, err := HomieLaunchBind(db, nil, "sess-nope", hbLaunch, hbContainer); err == nil {
			t.Fatal("want error for unknown session")
		}
	})

	t.Run("malformed ids refuse", func(t *testing.T) {
		db := hbSpine(t)
		if _, err := HomieLaunchBind(db, nil, "sess-1", "short", hbContainer); err == nil {
			t.Fatal("want usage error for a bad launch id")
		}
		if _, err := HomieLaunchBind(db, nil, "sess-1", hbLaunch, "short"); err == nil {
			t.Fatal("want usage error for a bad container id")
		}
	})
}

func homieRunnerID(session string) *RunIdentity {
	return &RunIdentity{Tier: "homie", RunID: session}
}

// hbBound seeds a session whose current launch is already bound to hbContainer
// (the state runner-started acts on).
func hbBound(t *testing.T, db *sql.DB) {
	t.Helper()
	dvExec(t, db, `UPDATE homie_sessions
		SET current_container_id = ?, launch_bound_at = datetime('now') WHERE id = 'sess-1'`, hbContainer)
}

func TestHomieRunnerStarted(t *testing.T) {
	rid := homieRunnerID("sess-1")

	t.Run("stamps launch_started_at on the bound current launch", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db)
		res, err := HomieRunnerStarted(db, rid, "sess-1", hbLaunch, hbContainer)
		if err != nil {
			t.Fatal(err)
		}
		got := res.(map[string]any)
		if got["fenced"] != true || got["started_at"] == nil {
			t.Fatalf("started = %v, want fenced+started_at", got)
		}
		var started sql.NullString
		if err := db.QueryRow(`SELECT launch_started_at FROM homie_sessions WHERE id='sess-1'`).Scan(&started); err != nil {
			t.Fatal(err)
		}
		if !started.Valid {
			t.Fatal("launch_started_at not persisted")
		}
	})

	t.Run("idempotent second start returns the original stamp", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db)
		first, _ := HomieRunnerStarted(db, rid, "sess-1", hbLaunch, hbContainer)
		second, _ := HomieRunnerStarted(db, rid, "sess-1", hbLaunch, hbContainer)
		if second.(map[string]any)["started_at"] != first.(map[string]any)["started_at"] {
			t.Fatal("runner-started not idempotent")
		}
	})

	t.Run("a start against an unbound container is fenced", func(t *testing.T) {
		db := hbSpine(t) // bound to nothing yet
		res, err := HomieRunnerStarted(db, rid, "sess-1", hbLaunch, hbContainer)
		if err != nil {
			t.Fatal(err)
		}
		if res.(map[string]any)["fenced"] != false {
			t.Fatal("a start before the bind must be fenced")
		}
	})

	t.Run("a stale launch is fenced", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db)
		res, _ := HomieRunnerStarted(db, rid, "sess-1", "bbbbbbbbbbbbbbbb", hbContainer)
		if res.(map[string]any)["fenced"] != false {
			t.Fatal("a stale launch must be fenced")
		}
	})

	t.Run("only the runner's own session tier may call it", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db)
		// Host scope (nil) is refused.
		if _, err := HomieRunnerStarted(db, nil, "sess-1", hbLaunch, hbContainer); err == nil {
			t.Fatal("host scope must not call runner-started")
		}
		// A runner for a different session is refused.
		if _, err := HomieRunnerStarted(db, homieRunnerID("other"), "sess-1", hbLaunch, hbContainer); err == nil {
			t.Fatal("a foreign-session runner must be refused")
		}
	})
}

func TestHomieExit(t *testing.T) {
	t.Run("a started launch ends the exact session", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db)
		dvExec(t, db, `UPDATE homie_sessions SET launch_started_at = datetime('now') WHERE id='sess-1'`)
		res, err := HomieExit(db, nil, "sess-1", hbLaunch, hbContainer, "exited")
		if err != nil {
			t.Fatal(err)
		}
		got := res.(map[string]any)
		if got["fenced"] != true || got["ended"] != true {
			t.Fatalf("exit = %v, want fenced+ended", got)
		}
		if s := hbStatus(t, db); s != "ended" {
			t.Fatalf("status = %q, want ended", s)
		}
	})

	t.Run("old native locators do not establish the current launch", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db)
		dvExec(t, db, `UPDATE homie_sessions SET native_session_ref = 'nat-1', trace_filename = 'native.jsonl' WHERE id='sess-1'`)
		res, err := HomieExit(db, nil, "sess-1", hbLaunch, hbContainer, "exited")
		if err != nil {
			t.Fatal(err)
		}
		got := res.(map[string]any)
		if got["fenced"] != true || got["ended"] != false || hbStatus(t, db) != "active" {
			t.Fatalf("old locator exit = %v status=%q, want fenced active launch debt", got, hbStatus(t, db))
		}
		var launch, container sql.NullString
		if err := db.QueryRow(`SELECT current_launch_id, current_container_id
			FROM homie_sessions WHERE id='sess-1'`).Scan(&launch, &container); err != nil {
			t.Fatal(err)
		}
		if launch.String != hbLaunch || container.Valid {
			t.Fatalf("old locator exit left launch=%q container=%q, want retained launch and cleared container",
				launch.String, container.String)
		}
	})

	t.Run("a pre-runner-start null-locator launch clears the bound pair and retains debt", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db) // bound, but never started and no locators
		res, err := HomieExit(db, nil, "sess-1", hbLaunch, hbContainer, "exited")
		if err != nil {
			t.Fatal(err)
		}
		got := res.(map[string]any)
		if got["fenced"] != true || got["ended"] != false {
			t.Fatalf("exit = %v, want fenced+not-ended", got)
		}
		if hbStatus(t, db) != "active" {
			t.Fatal("a pre-start exit must keep the session active")
		}
		var launch, container sql.NullString
		if err := db.QueryRow(`SELECT current_launch_id, current_container_id FROM homie_sessions WHERE id='sess-1'`).Scan(&launch, &container); err != nil {
			t.Fatal(err)
		}
		if launch.String != hbLaunch {
			t.Fatalf("the launch debt must be retained, got %q", launch.String)
		}
		if container.Valid {
			t.Fatal("the confirmed-exited container must be cleared")
		}
	})

	t.Run("idempotent: re-exit of an ended session is a fenced no-op", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db)
		dvExec(t, db, `UPDATE homie_sessions SET launch_started_at = datetime('now') WHERE id='sess-1'`)
		HomieExit(db, nil, "sess-1", hbLaunch, hbContainer, "exited")
		res, _ := HomieExit(db, nil, "sess-1", hbLaunch, hbContainer, "exited")
		if res.(map[string]any)["fenced"] != false {
			t.Fatal("re-exit of an ended session must be fenced")
		}
		if hbStatus(t, db) != "ended" {
			t.Fatal("re-exit must not change the ended state")
		}
	})

	t.Run("a stale launch or wrong container is fenced", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db)
		if HomieExit3(t, db, "bbbbbbbbbbbbbbbb", hbContainer)["fenced"] != false {
			t.Fatal("stale launch must be fenced")
		}
		if HomieExit3(t, db, hbLaunch, strings.Repeat("d", 64))["fenced"] != false {
			t.Fatal("wrong container must be fenced")
		}
	})

	t.Run("host scope only", func(t *testing.T) {
		db := hbSpine(t)
		hbBound(t, db)
		if _, err := HomieExit(db, homieRunnerID("sess-1"), "sess-1", hbLaunch, hbContainer, "exited"); err == nil {
			t.Fatal("a run.json caller must be refused (host scope)")
		}
	})
}

func hbStatus(t *testing.T, db *sql.DB) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM homie_sessions WHERE id='sess-1'`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func HomieExit3(t *testing.T, db *sql.DB, launch, container string) map[string]any {
	t.Helper()
	res, err := HomieExit(db, nil, "sess-1", launch, container, "exited")
	if err != nil {
		t.Fatal(err)
	}
	return res.(map[string]any)
}
