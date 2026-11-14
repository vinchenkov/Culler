//go:build !test_fake_routing

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mc/deployment"
	"mc/distribution"
	"mc/onboarding"
	"mc/verbs"
)

var setupAgentsViewExecutable = []byte("#!/bin/sh\necho synthetic agentsview\n")

func init() {
	setupPreviewCases["agentsview-diagnostic"] = setupPreviewAgentsViewDiagnostic
}

// setupPreviewAgentsViewDiagnostic shows the AgentsView digest failure and
// its details for the terminal review.
func setupPreviewAgentsViewDiagnostic(t *testing.T) setupPreviewCase {
	w, _ := setupTestWizard(t)
	pin, err := deployment.LoadAgentsViewPin()
	if err != nil {
		t.Fatal(err)
	}
	return setupPreviewCase{
		wizard: w,
		steps: []setupPreviewStep{
			{Wait: "q exit", Send: "\x1b[B\r", Name: "AgentsView digest failure"},
			{Wait: "q exit", Send: "q", Name: "AgentsView digest details"},
		},
		run: func() {
			err := w.operation("Install AgentsView", func() error {
				return agentsViewSetupFailure(&deployment.AgentsViewMismatchError{Item: "executable",
					ExpectedSize: pin.Executable.Size, ExpectedSHA256: pin.Executable.SHA256,
					ActualSize: 4096, ActualSHA256: strings.Repeat("0", 64)})
			})
			if !errors.Is(err, onboarding.ErrExit) {
				t.Fatal(err)
			}
		},
	}
}

func setupAgentsViewPin() deployment.AgentsViewPin {
	sum := sha256.Sum256(setupAgentsViewExecutable)
	return deployment.AgentsViewPin{
		Version:  "1.2.3",
		Platform: "darwin-arm64",
		Archive: deployment.AgentsViewArchive{
			Name: "agentsview_1.2.3_darwin_arm64.tar.gz", Size: 10, SHA256: strings.Repeat("a", 64),
			URL: "https://example.com/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz",
		},
		Executable:     deployment.AgentsViewExecutable{Name: "agentsview", Size: int64(len(setupAgentsViewExecutable)), SHA256: hex.EncodeToString(sum[:])},
		DisabledAgents: []string{"cursor"},
	}
}

func setupAgentsViewPackage(t *testing.T, body []byte) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "agentsview"), body, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPublishedSetupRequiresTheAgentsViewOfItsPin(t *testing.T) {
	pin := setupAgentsViewPin()
	record := publishedTestRecord(strings.Repeat("a", 40), 20)
	root := setupAgentsViewPackage(t, setupAgentsViewExecutable)
	if err := checkPublishedAgentsView(root, record, pin); err == nil ||
		err.Error() != "The release record has no AgentsView entry. Download install.sh again." {
		t.Fatalf("a record without AgentsView = %v", err)
	}
	record.AgentsView = &distribution.AgentsViewRelease{Version: pin.Version, ArchiveSHA256: pin.Archive.SHA256, ExecutableSHA256: pin.Executable.SHA256}
	if err := checkPublishedAgentsView(root, record, pin); err != nil {
		t.Fatal(err)
	}
	changed := *record.AgentsView
	changed.ExecutableSHA256 = strings.Repeat("b", 64)
	other := record
	other.AgentsView = &changed
	if err := checkPublishedAgentsView(root, other, pin); err == nil ||
		err.Error() != "The release record names a different AgentsView release. Download install.sh again." {
		t.Fatalf("a record of another AgentsView = %v", err)
	}
	edited := []byte("#!/bin/sh\necho changed\n")
	actual := sha256.Sum256(edited)
	err := checkPublishedAgentsView(setupAgentsViewPackage(t, edited), record, pin)
	if want := "AgentsView failed its digest check. Download install.sh again. Expected SHA-256: " + pin.Executable.SHA256 +
		". Found SHA-256: " + hex.EncodeToString(actual[:]) + "."; err == nil || err.Error() != want {
		t.Fatalf("a changed package executable = %v, want %q", err, want)
	}
}

func TestPublishedAgentsViewFailureStatesTheServicesAndTheDiagnostic(t *testing.T) {
	cause := errors.New("install the AgentsView executable: rename a b: file exists")
	for stopped, want := range map[bool]string{
		true:  "The AgentsView installation failed. The Culler services are stopped. Run install.sh again. Diagnostic: " + cause.Error(),
		false: "The AgentsView installation failed. Run install.sh again. Diagnostic: " + cause.Error(),
	} {
		if err := publishedAgentsViewFailure(cause, stopped); err.Error() != want || !errors.Is(err, cause) {
			t.Fatalf("services stopped %v = %v, want %q", stopped, err, want)
		}
	}
}

func TestPublishedSetupInstallsTheAgentsViewOfThePackage(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	p := &publishedSetup{home: home, root: setupAgentsViewPackage(t, setupAgentsViewExecutable), agentsView: setupAgentsViewPin()}
	deps := setupDependencies{}
	p.configure(&deps)
	if deps.agentsView == nil {
		t.Fatal("the published setup omits the AgentsView install")
	}
	if err := deps.agentsView(home); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, "bin", "agentsview"))
	info, statErr := os.Stat(filepath.Join(home, "bin", "agentsview"))
	if err != nil || statErr != nil || string(body) != string(setupAgentsViewExecutable) || info.Mode().Perm() != 0o755 {
		t.Fatalf("installed executable = %q, %v, %v, %v", body, info, err, statErr)
	}
}

func TestSetupStartInstallsAgentsViewBeforeTheServicesStart(t *testing.T) {
	w, u := setupTestWizard(t)
	calls := []string{}
	w.deps.publish = func() error { calls = append(calls, "publish"); return nil }
	w.deps.agentsView = func(home string) error {
		if home != w.home {
			t.Fatalf("AgentsView home = %s", home)
		}
		calls = append(calls, "agentsview")
		return nil
	}
	w.deps.section = func(section string, _ verbs.OnboardArgs) error { calls = append(calls, section); return nil }
	if err := w.start(); err != nil {
		t.Fatal(err)
	}
	if len(calls) < 3 || calls[0] != "publish" || calls[1] != "agentsview" || calls[len(calls)-1] != "supervision" {
		t.Fatalf("start order = %v", calls)
	}
	if !slices.Contains(u.activities, "Install AgentsView") {
		t.Fatalf("activities = %v", u.activities)
	}
}

func TestSetupAgentsViewFailureNamesTheCauseAndTheDigests(t *testing.T) {
	pin := setupAgentsViewPin()
	mismatch := &deployment.AgentsViewMismatchError{Item: "executable", ExpectedSize: pin.Executable.Size,
		ExpectedSHA256: pin.Executable.SHA256, ActualSize: 3, ActualSHA256: strings.Repeat("c", 64)}
	w, u := setupTestWizard(t, ":exit")
	w.deps.agentsView = func(string) error { return mismatch }
	w.deps.section = func(string, verbs.OnboardArgs) error {
		t.Fatal("a failed AgentsView install started a section")
		return nil
	}
	if err := w.start(); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("start = %v", err)
	}
	panels := strings.Join(u.panels, "\n")
	if !strings.Contains(panels, "AgentsView failed its digest check. Run install.sh again.") {
		t.Fatalf("panels = %s", panels)
	}
	details := strings.Join(setupDiagnosticLines("Install AgentsView", agentsViewSetupFailure(mismatch)), "\n")
	if !strings.Contains(details, pin.Executable.SHA256) || !strings.Contains(details, strings.Repeat("c", 64)) {
		t.Fatalf("details = %s", details)
	}
	other := agentsViewSetupFailure(errors.New("download the AgentsView release archive: connection refused"))
	if setupCause(other) != "The AgentsView installation failed. Open the error details, then select Retry." {
		t.Fatalf("cause = %q", setupCause(other))
	}
}
