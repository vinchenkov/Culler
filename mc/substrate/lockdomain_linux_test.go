//go:build linux && !test_scratch_spine

package substrate

import (
	"io"
	"testing"
)

func TestLinuxRequiresMountTable(t *testing.T) {
	table, err := platformMountinfo()
	if err != nil {
		t.Fatal(err)
	}
	if table == nil {
		t.Fatal("The Linux database guard requires the mount table.")
	}
	defer table.Close()
	body, err := io.ReadAll(table)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Fatal("The Linux mount table is empty.")
	}
}
