package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/deployment"
)

func TestUpdateCommandGateRefusesWorkWhenJournalIsUnreadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	path := deployment.UpdateJournalPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"dispatch", "project", "connection", "agent-profile", "__homie-start", "__dispatch-commit", "__wiki-export", "__reset"} {
		var output, diagnostic strings.Builder
		code := dispatchArgs([]string{command}, strings.NewReader(""), &output, &diagnostic)
		if code == 0 || !strings.Contains(output.String(), "update") {
			t.Fatalf("%s bypassed the update refusal: %d %s %s", command, code, output.String(), diagnostic.String())
		}
	}
	for _, command := range []string{"__setup", "__onboard", "__update-reconcile", "__release-database", "doctor", "health"} {
		if err := gateUpdateCommand([]string{command}); err != nil {
			t.Fatalf("recovery command %s refused: %v", command, err)
		}
	}
}

func TestUpdateCommandGateLeavesDeploymentsWithoutJournalAvailable(t *testing.T) {
	t.Setenv("MC_HOME", t.TempDir())
	if err := gateUpdateCommand([]string{"dispatch"}); err != nil {
		t.Fatal(err)
	}
}
