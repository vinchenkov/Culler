package verbs

import (
	"database/sql"
	"strings"
	"testing"

	"mc/dispatch"
)

// waitRecords returns the Project activity of the given kinds, newest first.
func waitRecords(t *testing.T, db *sql.DB, kinds ...string) []map[string]any {
	t.Helper()
	result, err := ActivityList(db, "", 1000, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, row := range result.(map[string]any)["activity"].([]map[string]any) {
		for _, kind := range kinds {
			if row["kind"] == kind {
				rows = append(rows, row)
			}
		}
	}
	return rows
}

// taskConnectionWait reads the connection that the task list names for task 1.
func taskConnectionWait(t *testing.T, db *sql.DB) any {
	t.Helper()
	result, err := TaskList(db, false, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range result.(map[string]any)["tasks"].([]map[string]any) {
		if row["id"] == int64(1) {
			return row["connection_wait"]
		}
	}
	t.Fatal("The task list has no task 1.")
	return nil
}

// stopWorkerConnection stops the connection of the Worker and returns its identifier.
func stopWorkerConnection(t *testing.T, db *sql.DB) string {
	t.Helper()
	connection := testEndpointBinding().Credential
	dvExec(t, db, `UPDATE connection_health SET state = 'stopped' WHERE connection_id = ?`, connection)
	return connection
}

func TestConnectionWaitWritesOneStartAndOneEndRecord(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	connection := stopWorkerConnection(t, db)
	for tick := range 3 {
		effect, err := dvDispatchOne(db)
		if err != nil {
			t.Fatal(err)
		}
		if got := effect.(map[string]any); got["action"] != "idle" || got["reason"] != "connection-unavailable" {
			t.Fatalf("tick %d = %v, want a connection wait", tick, got)
		}
	}
	if spawns := waitRecords(t, db, "dispatch.spawn"); len(spawns) != 0 {
		t.Fatalf("A stopped spawn wrote spawn records: %v", spawns)
	}
	started := waitRecords(t, db, "dispatch.wait_started", "dispatch.wait_ended")
	if len(started) != 1 || started[0]["kind"] != "dispatch.wait_started" || started[0]["subject"] != connection ||
		started[0]["subject_kind"] != "connection" || started[0]["project"] != "ws-test" ||
		!strings.HasPrefix(started[0]["detail"].(string), "The Worker run for task #1 waits for this connection. ") {
		t.Fatalf("wait records = %v, want one start record for %s", started, connection)
	}
	if got := taskConnectionWait(t, db); got != connection {
		t.Fatalf("task connection wait = %v, want %s", got, connection)
	}

	dvExec(t, db, `UPDATE connection_health SET state = 'ready' WHERE connection_id = ?`, connection)
	effect, err := dvDispatchOne(db)
	if err != nil {
		t.Fatal(err)
	}
	spawn := effect.(map[string]any)
	if spawn["action"] != "spawn" {
		t.Fatalf("effect after recovery = %v, want a spawn", spawn)
	}
	waits := waitRecords(t, db, "dispatch.wait_started", "dispatch.wait_ended")
	if len(waits) != 2 || waits[0]["kind"] != "dispatch.wait_ended" || waits[0]["subject"] != connection ||
		waits[0]["subject_kind"] != "connection" || waits[0]["detail"] != "The wait for this connection ended." {
		t.Fatalf("wait records = %v, want one end record after the start record", waits)
	}
	spawns := waitRecords(t, db, "dispatch.spawn")
	if len(spawns) != 1 || spawns[0]["subject"] != spawn["run_id"] || spawns[0]["subject_kind"] != "run" ||
		spawns[0]["detail"] != "Worker run for task #1." {
		t.Fatalf("spawn records = %v, want one record for run %v", spawns, spawn["run_id"])
	}
	if got := taskConnectionWait(t, db); got != nil {
		t.Fatalf("task connection wait after the spawn = %v, want none", got)
	}
}

func TestProjectPauseEndsTheConnectionWait(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	stopWorkerConnection(t, db)
	if _, err := dvDispatchOne(db); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectSetStatus(db, nil, "ws-test", "paused"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := dvDispatchOne(db); err != nil {
			t.Fatal(err)
		}
	}
	waits := waitRecords(t, db, "dispatch.wait_started", "dispatch.wait_ended")
	if len(waits) != 2 || waits[0]["kind"] != "dispatch.wait_ended" || waits[1]["kind"] != "dispatch.wait_started" {
		t.Fatalf("wait records = %v, want one start record and one end record", waits)
	}
	if got := taskConnectionWait(t, db); got != nil {
		t.Fatalf("task connection wait in a paused Project = %v, want none", got)
	}
}

func TestActivitySubjectKindsNameTasksAndRuns(t *testing.T) {
	db := dvSpine(t)
	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusSeeded, 2))
	dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject) VALUES ('0123456789abcdef', 'pipeline', 'worker', 'ws-test', 1)`)
	dvExec(t, db, `INSERT INTO activity (actor, kind, subject, detail) VALUES
		('operator', 'task.block_reason', '1', 'A task record.'),
		('mc', 'dispatch.reap', '0123456789abcdef', 'A run record.')`)
	kinds := map[string]any{}
	for _, row := range waitRecords(t, db, "task.block_reason", "dispatch.reap") {
		kinds[row["kind"].(string)] = row["subject_kind"]
	}
	if kinds["task.block_reason"] != "task" || kinds["dispatch.reap"] != "run" {
		t.Fatalf("subject kinds = %v, want a task and a run", kinds)
	}
}
