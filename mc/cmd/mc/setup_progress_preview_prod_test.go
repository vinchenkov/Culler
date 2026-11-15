//go:build !test_fake_routing

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc/deployment"
)

func init() {
	setupPreviewCases["setup-progress"] = setupPreviewProgress
}

func setupPreviewProgress(t *testing.T) setupPreviewCase {
	w, _ := setupTestWizard(t)
	command := filepath.Join(w.home, "culler")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	digest, err := deployment.CLIFileDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(deployment.CLIInstallation{Path: command, SHA256: digest, Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(w.home, deployment.CLIInstallRecord)
	if err := os.MkdirAll(filepath.Dir(recordPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, record, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_BIN_DIR", w.home)
	return setupPreviewCase{wizard: w, steps: []setupPreviewStep{
		{Wait: "Installing AgentsView", Name: "Automatic installation"},
		{Wait: "Downloading the agent image", Name: "Automatic download"},
	}, run: func() {
		if err := w.operation("Install AgentsView", func() error { time.Sleep(500 * time.Millisecond); return nil }); err != nil {
			t.Fatal(err)
		}
		if err := w.progressOperation("Prepare the agent container", func(report func(string)) error {
			report("Download the agent image")
			time.Sleep(500 * time.Millisecond)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := w.commandAvailability(); err != nil {
			t.Fatal(err)
		}
	}}
}

func TestSetupCommandAvailabilityShowsOneNotice(t *testing.T) {
	preview := setupPreviewProgress(t)
	w := preview.wizard
	ui := w.ui.(*setupTestUI)
	if err := w.commandAvailability(); err != nil {
		t.Fatal(err)
	}
	want := "\nThe culler cli is installed for lifecycle operations. Continue to the dashboard for the Culler experience."
	if len(ui.panels) != 1 || ui.panels[0] != want {
		t.Fatalf("The command notice differs: %q", ui.panels)
	}
	ui.panels = nil
	if err := os.WriteFile(filepath.Join(w.home, "culler"), []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := w.commandAvailability(); err == nil || len(ui.panels) != 0 {
		t.Fatal("The setup claimed success for an unverified command.")
	}
}
