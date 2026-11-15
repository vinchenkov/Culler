package verbs

import (
	"mc/dispatch"
	"os"
	"path/filepath"
	"testing"
)

// The read verbs against a spine on a read-only mount. Inside an agent
// container, a read verb that opens the spine through the writer's path
// needs the WAL/SHM sidecars to exist or be creatable, so it fails with
// SQLITE_CANTOPEN exactly when no writer's sidecars happen to be present.
// The read verbs open through OpenSpineRead (substrate.OpenReadOnly), which
// reads BOTH states.
//
// The fixture is the worst of the two: a spine copy with NO -wal/-shm whose
// file and directory are both unwritable.
func TestReadVerbsReadAReadOnlySpine(t *testing.T) {
	t.Setenv("MC_HOME", t.TempDir())
	dir := t.TempDir()
	spine := filepath.Join(dir, "spine.db")
	if _, err := Init(InitArgs{Spine: spine, Project: "ws-ro", WorkspaceRoot: t.TempDir()}); err != nil {
		t.Fatalf("init spine: %v", err)
	}
	seed, err := OpenSpine(spine)
	if err != nil {
		t.Fatalf("open spine for seeding: %v", err)
	}
	if _, err := seed.Exec(`
		INSERT INTO tasks (title, scope, priority, created_at, status, origin, project)
		VALUES ('read me', 'task', 2, '2026-08-27 09:00:00', 'proposed', 'user', 'ws-ro')`); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := seed.Exec(`
		INSERT INTO activity (actor, kind, subject, detail)
		VALUES ('test', 'spine.readonly', 'ws-ro', 'seeded for the read-only harness')`); err != nil {
		t.Fatalf("seed activity: %v", err)
	}
	seed.Close()
	for _, sidecar := range []string{spine + "-wal", spine + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Fatalf("%s survived the seeding writer's close; the harness needs the sidecar-less state", sidecar)
		}
	}
	if err := os.Chmod(spine, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(dir, 0o755)
		os.Chmod(spine, 0o644)
	})

	db, err := OpenSpineRead(spine)
	if err != nil {
		t.Fatalf("OpenSpineRead(read-only spine) = %v, want a working read", err)
	}
	defer db.Close()
	if _, err := ProjectRoleShow(db, nil, "ws-ro", dispatch.RoleWorker); err != nil {
		t.Fatalf("Read-only customization read: %v", err)
	}

	rows := func(res any, key string) []map[string]any {
		t.Helper()
		list, ok := res.(map[string]any)[key].([]map[string]any)
		if !ok {
			t.Fatalf("%s is not a row list: %#v", key, res)
		}
		return list
	}

	// The verb that was dead in-container.
	res, err := ActivityList(db, "", 0, "")
	if err != nil {
		t.Fatalf("ActivityList against a read-only spine: %v", err)
	}
	activity := rows(res, "activity")
	if len(activity) == 0 || activity[0]["kind"] != "spine.readonly" {
		t.Fatalf("ActivityList = %#v, want the seeded spine.readonly row first", activity)
	}

	// A sibling read verb, proving the one shared path serves the family.
	res, err = TaskList(db, false, "")
	if err != nil {
		t.Fatalf("TaskList against a read-only spine: %v", err)
	}
	tasks := rows(res, "tasks")
	if len(tasks) != 1 || tasks[0]["title"] != "read me" {
		t.Fatalf("TaskList = %#v, want the one seeded task", tasks)
	}

	// The fixed path created no sidecar — no read verb may require one.
	for _, sidecar := range []string{spine + "-wal", spine + "-shm"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("the read verbs left %s behind (stat err %v)", sidecar, err)
		}
	}
}

// The read path must not become a write path: OpenSpineRead's connection
// carries query_only, so a mutation routed through it aborts instead of
// silently weakening the write discipline the read verbs were split from.
func TestOpenSpineReadRefusesWrites(t *testing.T) {
	t.Setenv("MC_HOME", t.TempDir())
	spine := filepath.Join(t.TempDir(), "spine.db")
	if _, err := Init(InitArgs{Spine: spine, Project: "ws-ro", WorkspaceRoot: t.TempDir()}); err != nil {
		t.Fatalf("init spine: %v", err)
	}
	db, err := OpenSpineRead(spine)
	if err != nil {
		t.Fatalf("OpenSpineRead: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO activity (actor, kind) VALUES ('test', 'forbidden.write')`); err == nil {
		t.Fatal("a write through the read-only spine handle committed")
	}
}
