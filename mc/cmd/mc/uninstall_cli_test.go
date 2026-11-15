package main_test

// `mc __uninstall` destroys the whole deployment, so the only thing standing
// between a prompt-injected Collie and an erased machine is the host-scope
// fence: scope follows from the read-only run.json a container carries, never
// from an argument. `onboard` and `reset` stay outside every container's reach
// for the same reason; uninstall belongs in that class and this pins it.
//
// The fence must refuse BEFORE the verb resolves routing, reads the spine, or
// touches Docker — which is also what keeps this test Docker-free.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallRefusesEveryContainerCaller(t *testing.T) {
	for _, identity := range []struct {
		name string
		body string
	}{
		{"pipeline worker", `{"run_id":"r","tier":"pipeline","role":"worker"}`},
		{"collie session", `{"run_id":"h","tier":"collie","role":"collie","verb_allowlist":["uninstall"]}`},
	} {
		t.Run(identity.name, func(t *testing.T) {
			runJSON := filepath.Join(t.TempDir(), "run.json")
			if err := os.WriteFile(runJSON, []byte(identity.body), 0o600); err != nil {
				t.Fatal(err)
			}
			denied := runMC(t, []string{
				"MC_HELPER=mc-helper-that-must-never-be-reached",
				"MC_RUN_JSON=" + runJSON,
			}, "", "__uninstall", "--confirm")
			if denied.code != 1 || !strings.Contains(denied.stderr, "host scope only") {
				t.Fatalf("container caller = code %d stderr %q", denied.code, denied.stderr)
			}
		})
	}
}

// A bare `mc __uninstall` must never remove anything: the destructive path is
// reachable only through an explicit flag.
func TestUninstallWithoutConfirmRemovesNothing(t *testing.T) {
	res := runMC(t, []string{"MC_HELPER=mc-helper-that-must-never-be-reached"}, "", "__uninstall")
	if res.code == 0 {
		t.Fatalf("bare `mc __uninstall` succeeded: %q", res.stdout)
	}
}
