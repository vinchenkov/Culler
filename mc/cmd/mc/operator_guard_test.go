package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mc/deployment"
)

func TestOperatorTransportPreservesRefusalExitCodes(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		code   int
		output string
	}{
		{"usage envelope", 2, `{"error":{"code":"usage","message":"The request requires expected."}}`},
		{"absent usage envelope", 2, ""},
		{"domain envelope", 1, `{"error":{"code":"conflict","message":"The revision changed."}}`},
		{"absent domain envelope", 1, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var output, diagnostic bytes.Buffer
			err := operatorSpineError(fixture.code, []byte(fixture.output), "The command refused the request.")
			if got := writeVerbError(&output, &diagnostic, err); got != fixture.code {
				t.Fatalf("The transport changed exit %d to %d: %s", fixture.code, got, output.String())
			}
		})
	}
}

func TestOperatorGuardRejectsReleaseAndUpdateChanges(t *testing.T) {
	home := t.TempDir()
	home, err := deployment.CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "supervision"), 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"mcHome": home, "releaseBuildId": releaseBuildID})
	if err := os.WriteFile(filepath.Join(home, "supervision", "resident.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := operatorDeploymentGuard(true, releaseBuildID); err != nil {
		t.Fatal(err)
	}
	if err := operatorDeploymentGuard(true, "another-release"); err == nil {
		t.Fatal("A stale command changed the deployment.")
	}
	if err := os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "install-maintenance.json"), []byte(`{"id":"installation"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := operatorDeploymentGuard(true, releaseBuildID); err == nil {
		t.Fatal("An operator write passed install maintenance.")
	}
	if err := operatorDeploymentGuard(false, releaseBuildID); err != nil {
		t.Fatal("A read required a completed update.")
	}
	if err := os.Remove(filepath.Join(home, "state", "install-maintenance.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "updates"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "updates", "transaction.json"), []byte(`{"phase":"replace"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := operatorDeploymentGuard(true, releaseBuildID); err == nil {
		t.Fatal("An invalid update record admitted a change.")
	}
}

func TestOperatorGuardUsesSelectedHome(t *testing.T) {
	home := t.TempDir()
	home, err := deployment.CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "supervision"), 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"mcHome": filepath.Join(home, "other"), "releaseBuildId": releaseBuildID})
	if err := os.WriteFile(filepath.Join(home, "supervision", "resident.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := operatorDeploymentGuard(true, releaseBuildID); err == nil {
		t.Fatal("The selected deployment used another home.")
	}
}

func TestOperatorRequestFlagRetainsCommandArguments(t *testing.T) {
	args, id, err := operatorRequestFlag([]string{"project", "pause", "example", "--request-id=request-1"})
	if err != nil || id != "request-1" || len(args) != 3 {
		t.Fatalf("The request flag changed its command: %v %s %v", args, id, err)
	}
	if _, _, err := operatorRequestFlag([]string{"pause", "--request-id", "first", "--request-id", "second"}); err == nil {
		t.Fatal("Two request identifiers were accepted.")
	}
}
