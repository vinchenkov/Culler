//go:build !test_fake_routing

package main

import (
	"path/filepath"
	"slices"
	"testing"
)

// The AgentsView property list names MC_HOME only through its data folder.
func TestUninstallFindsTheHomeOfALoneAgentsViewUnit(t *testing.T) {
	f := newUninstallFixture(t)
	home := filepath.Join(f.root, "custom deployment")
	for _, file := range []string{"config.toml", "deployment.uuid"} {
		uninstallWrite(t, filepath.Join(home, file), "retained data", 0o600)
	}
	agentsViewUninstallFixture(t, f, home)
	code, report, diag := runUninstallFixture(t, "--dry-run", "--full-wipe")
	if code != 0 || !slices.Contains(report.Homes, home) {
		t.Fatalf("exit %d homes %v: %s", code, report.Homes, diag)
	}
}
