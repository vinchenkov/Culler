//go:build linux && test_scratch_spine

package substrate

import (
	"path/filepath"
	"testing"
)

func TestScratchSpineOpensOnAnyFilesystem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spine.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	var projects int
	if err := db.QueryRow("SELECT count(*) FROM projects").Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 0 {
		t.Fatalf("The fresh test database contains %d Projects.", projects)
	}
}
