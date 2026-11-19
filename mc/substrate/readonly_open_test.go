// The shared read-only open paths. The contract these tests pin: the ACTIVE
// entry (OpenReadOnly) reads a live spine through its -wal/-shm sidecars and,
// where sidecars cannot be created (read-only mount, none present), FAILS
// honestly — it never degrades to an immutable read, because the absence of
// a -wal at open time proves nothing about writers during the read. The
// explicit SNAPSHOT entry (OpenSnapshotReadOnly) is the only immutable path,
// for copies whose writer finished. Neither creates the database or a
// sidecar.
package substrate_test

import (
	"os"
	"path/filepath"
	"testing"

	"mc/substrate"

	sqlite3 "modernc.org/sqlite/lib"
)

// readOnlySpine builds a spine, seeds one project, closes the writer
// (which checkpoints and removes the WAL/SHM sidecars), and then locks the
// file AND its directory against writing — the agent container's view of a
// spine on a read-only mount, in the sidecar-less state that used to be
// unopenable.
func readOnlySpine(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "spine.db")
	db, err := substrate.Open(path)
	if err != nil {
		t.Fatalf("create spine: %v", err)
	}
	if err := substrate.Init(db); err != nil {
		t.Fatalf("init spine: %v", err)
	}
	mkProject(t, db, "ws")
	db.Close()
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Fatalf("%s survived the writer's close; the fixture needs the sidecar-less state", sidecar)
		}
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(dir, 0o755)
		os.Chmod(path, 0o644)
	})
	return dir, path
}

func TestOpenReadOnlyRefusesSidecarlessSpineWhileSnapshotPathReadsIt(t *testing.T) {
	_, path := readOnlySpine(t)

	// The write path CANNOT open this state — the fixture's premise.
	if db, err := substrate.Open(path); err == nil {
		db.Close()
		t.Fatal("substrate.Open opened a sidecar-less read-only spine; the fixture premise broke")
	}

	// C1: the ACTIVE entry must fail honestly, never silently become an
	// immutable read. This is the expected-failure half of the container
	// read-only-mount window that the mount layout (not a driver flag) owns.
	if db, err := substrate.OpenReadOnly(path); err == nil {
		db.Close()
		t.Fatal("the active read path degraded to a sidecar-less open; immutable must only come from an explicit snapshot call")
	}

	// The named snapshot path reads the same file, with the caller owning the
	// stability precondition.
	db, err := substrate.OpenSnapshotReadOnly(path)
	if err != nil {
		t.Fatalf("OpenSnapshotReadOnly(read-only snapshot) = %v, want a working read", err)
	}
	defer db.Close()
	if n := oneInt(t, db, `SELECT COUNT(*) FROM projects WHERE id = 'ws'`); n != 1 {
		t.Fatalf("read %d seeded projects through the snapshot path, want 1", n)
	}

	// Read-only means read-only on both entries: query_only is carried.
	wantSQLiteError(t, db, sqlite3.SQLITE_READONLY, "readonly database", `INSERT INTO projects (id, title) VALUES ('ws2', 'W2')`)

	// Neither open created a sidecar.
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("the read-only opens left %s behind (stat err %v)", sidecar, err)
		}
	}
}

// The other half of the intermittency: while a writer holds the spine, its
// -wal/-shm exist and carry committed rows the main file does not have yet.
// The read path must attach to them (never fall back to an immutable read,
// which would miss the WAL's commits) even when every file is unwritable.
func TestOpenReadOnlySeesALiveWritersWALCommits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spine.db")
	writer, err := substrate.Open(path)
	if err != nil {
		t.Fatalf("create spine: %v", err)
	}
	defer writer.Close()
	if err := substrate.Init(writer); err != nil {
		t.Fatalf("init spine: %v", err)
	}
	mkProject(t, writer, "ws")
	mkProject(t, writer, "in-wal")
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o444); err != nil {
			t.Fatalf("chmod %s: %v", p, err)
		}
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(dir, 0o755)
		for _, p := range []string{path, path + "-wal", path + "-shm"} {
			os.Chmod(p, 0o644)
		}
	})

	db, err := substrate.OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly(spine with a live writer) = %v, want a working read", err)
	}
	defer db.Close()
	if n := oneInt(t, db, `SELECT COUNT(*) FROM projects WHERE id = 'in-wal'`); n != 1 {
		t.Fatalf("the WAL-committed row is invisible to the read path (%d rows) — it read a stale snapshot", n)
	}
}

// A read verb must never provision. substrate.Open creates a missing spine;
// the read path reports it instead.
func TestOpenReadOnlyNeverCreatesTheSpine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	if db, err := substrate.OpenReadOnly(path); err == nil {
		db.Close()
		t.Fatal("OpenReadOnly opened a spine that does not exist")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the refused read-only open still created %s (stat err %v)", path, err)
	}
}
