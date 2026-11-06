package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/deployment"
)

// useAgentRunIdentity points the gate at path, so a check in an agent container
// tests the host gate the same way as a check on the Mac.
func useAgentRunIdentity(t *testing.T, path string) {
	t.Helper()
	previous := agentRunIdentity
	agentRunIdentity = path
	t.Cleanup(func() { agentRunIdentity = previous })
}

func writeUnreadableUpdateJournal(t *testing.T, home string) {
	t.Helper()
	path := deployment.UpdateJournalPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCommandGateRefusesWorkWhenJournalIsUnreadable(t *testing.T) {
	useAgentRunIdentity(t, filepath.Join(t.TempDir(), "absent-run.json"))
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	writeUnreadableUpdateJournal(t, home)
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
	useAgentRunIdentity(t, filepath.Join(t.TempDir(), "absent-run.json"))
	t.Setenv("MC_HOME", t.TempDir())
	if err := gateUpdateCommand([]string{"dispatch"}); err != nil {
		t.Fatal(err)
	}
}

// An agent container has no MC_HOME, and its HOME is a folder that the setuid
// mc cannot inspect. A file in place of a folder gives the same failure.
func TestUpdateCommandGateLeavesAgentContainerCommandsAvailable(t *testing.T) {
	identity := filepath.Join(t.TempDir(), "run.json")
	if err := os.WriteFile(identity, []byte(`{"tier":"homie","role":"homie","run_id":"h-probe"}`), 0644); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	journalHome := t.TempDir()
	writeUnreadableUpdateJournal(t, journalHome)
	for name, setup := range map[string]func(){
		"uninspectable home": func() { t.Setenv("MC_HOME", ""); t.Setenv("HOME", filepath.Join(blocked, "home")) },
		"unfinished update":  func() { t.Setenv("MC_HOME", journalHome) },
	} {
		setup()
		useAgentRunIdentity(t, filepath.Join(t.TempDir(), "absent-run.json"))
		if err := gateUpdateCommand([]string{"homie"}); err == nil {
			t.Fatalf("%s: the host gate accepted the command", name)
		}
		useAgentRunIdentity(t, identity)
		for _, command := range []string{"homie", "dispatch", "task"} {
			if err := gateUpdateCommand([]string{command}); err != nil {
				t.Fatalf("%s: the agent command %s was refused: %v", name, command, err)
			}
		}
	}
}
