//go:build !test_fake_routing

package main

import (
	"errors"
	"io"
	operatorconfig "mc/config"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mc/onboarding"
	"mc/verbs"
)

func init() {
	for _, name := range []string{"existing-complete", "existing-partial", "existing-update", "existing-update-stopped", "existing-update-blocked", "existing-diagnostic", "incompatible-database", "existing-active-partial", "existing-requested", "existing-agent", "existing-install", "existing-exit", "saved-choices", "final-diagnostic", "image-diagnostic", "health-diagnostic", "existing-update-mixed", "existing-update-files", "final-check-failure", "existing-update-older", "existing-update-unknown", "existing-update-components", "existing-update-repair"} {
		setupPreviewCases[name] = func(t *testing.T) setupPreviewCase { return setupPreviewExisting(t, name) }
	}
}

func setupPreviewExisting(t *testing.T, name string) setupPreviewCase {
	w, _ := setupTestWizard(t)
	preview := setupPreviewCase{wizard: w}
	state := completeSetupDeployment()
	state.Release = setupRelease{Installed: "cb37a2d1c9016710aa5e3f8c7c4c6f43b173ab99", Proposed: "cb37a2d1c9016710aa5e3f8c7c4c6f43b173ab99", SourceState: "clean", InstalledDisplay: "0.1.0 (cb37a2d1c901)", ProposedDisplay: "0.1.0 (cb37a2d1c901)", Components: []setupReleaseComponent{
		{Name: "Service release", Status: "same"}, {Name: "Host files", Status: "same"}, {Name: "Runner files", Status: "same"}, {Name: "Agent image", Status: "same"},
	}}

	switch name {
	case "existing-complete":
		preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "\r", Name: "Complete deployment"}}
	case "existing-partial", "existing-active-partial":
		state.Active = name == "existing-active-partial"
		for i := range state.Checks {
			if state.Checks[i].Check == "supervision" || state.Checks[i].Check == "dashboard" {
				state.Checks[i].Status = "fail"
				state.Checks[i].Detail = "The services require activation."
			}
		}
		preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Partial deployment"}}
	case "health-diagnostic":
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {
			calls++
			body := `{"ok":false,"findings":[{"check":"resident","status":"fail","detail":"The resident heartbeat is absent."}]}`
			if calls > 1 {
				body = `{"ok":true,"findings":[]}`
			}
			_, _ = io.WriteString(out, body)
		}))
		t.Cleanup(server.Close)
		cfg, err := w.catalog()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Surfaces.Dashboard.Bind = strings.TrimPrefix(server.URL, "http://")
		if _, err := operatorconfig.WriteAtomic(filepath.Join(w.home, "config.toml"), cfg); err != nil {
			t.Fatal(err)
		}
		preview.steps = []setupPreviewStep{
			{Wait: "q exit", Send: "\x1b[B\r", Name: "Service health failure"},
			{Wait: "q exit", Send: "\r", Name: "Service health diagnostic"},
			{Wait: "q exit", Send: "\x1b[B\r", Name: "Incomplete health result"},
			{Wait: "q exit", Send: "q", Name: "Incomplete result diagnostic"},
		}
		preview.run = func() {
			if err := w.operation("Check dashboard access", func() error { return setupCheckDashboard(w.home) }); !errors.Is(err, onboarding.ErrExit) {
				t.Fatal(err)
			}
		}
	case "saved-choices":
		preview.steps = []setupPreviewStep{}
		preview.run = func() { setupSavedChoicesNotice(w.ui, errors.New("The saved setup stage is invalid.")) }
	case "final-diagnostic":
		preview.steps = []setupPreviewStep{}
		preview.run = func() {
			err := setupSectionResult("home", 1, []byte(`{"error":{"message":"validate deployment files: the directory /fixture/home/release has an invalid owner"}}`), nil)
			w.problem("Setup requires attention", w.failureLines("Setup", err))
		}
	case "image-diagnostic":
		preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "\x1b[B\r", Name: "Image build failure"}, {Wait: "q exit", Send: "q", Name: "Image build diagnostic"}}
		preview.run = func() {
			err := w.operation("Prepare the agent container", func() error {
				return &onboarding.PreparationError{
					Operation: "Agent image build", ExitCode: 9,
					Cause:      "The agent image build failed. Open the error details, then select Retry.",
					Diagnostic: "compiler failed: the source file contains an invalid declaration",
				}
			})
			if !errors.Is(err, onboarding.ErrExit) {
				t.Fatal(err)
			}
		}
	case "existing-requested":
		w.answers.DashboardBind = "127.0.0.1:7444"
		preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Requested changes"}}
	case "existing-agent":
		preview.steps = []setupPreviewStep{}
		preview.run = func() { w.configuredAgentNotice() }
	case "existing-install":
		preview.steps = []setupPreviewStep{}
		w.deps.publish = func() error { return nil }
		w.deps.agentsView = func(string) error { return nil }
		preview.run = func() {
			if err := w.start(); err != nil {
				t.Fatal(err)
			}
		}
	case "existing-exit":
		preview.steps = []setupPreviewStep{}
		preview.run = func() {
			w.servicesStopped = true
			w.serviceStates = map[string]bool{"resident": false, "dashboard": false}
			w.exitNotice()
		}
	case "existing-update", "existing-update-stopped", "existing-update-blocked", "existing-update-mixed", "existing-update-files", "existing-update-older", "existing-update-unknown", "existing-update-components", "existing-update-repair":
		state.Update = true
		state.Release.Installed, state.Release.InstalledDisplay = "a3def3e5dca098246219bc631f35fcd290158471", "a3def3e5dca0"
		for i := range state.Release.Components {
			state.Release.Components[i].Status = "different"
		}
		state.Release.Components[3].Installed = state.Release.Installed
		state.Active = name != "existing-update-stopped"
		state.Checks[0].Status = "fail"
		state.Checks[0].Detail = "The installed files differ from this installer."
		preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "\r", Name: "Release update"}}
		if name == "existing-update-blocked" {
			for i := range state.Checks {
				f := &state.Checks[i]
				if f.Check == "spine" || f.Check == "deployment-identity" || f.Check == "surfaces" || f.Check == "container-runtime" {
					f.Status, f.Detail = "fail", "not checked: helper capability crossing failed: private doctor-runtime build/schema identity mismatch"
				}
			}
			preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "\x1b[B\r", Name: "Blocked release checks"}}
		}
		if name == "existing-update-mixed" {
			state.Services = map[string]bool{"resident": true, "dashboard": false}
		}
		if name == "existing-update-files" {
			state.Release.Installed = state.Release.Proposed
			state.Release.InstalledDisplay = state.Release.ProposedDisplay
			state.Release.SourceState = "modified"
			state.Release.Components[0].Status = "same"
			state.Release.Components[3].Status = "same"
		}
		if name == "existing-update-older" {
			state.Release.Relation = "older"
			state.Release.InstalledDisplay = "0.1.0 (a3def3e5dca0)"
		}
		if name == "existing-update-unknown" {
			state.Release.Installed = "unknown"
			state.Release.InstalledDisplay = ""
			state.Release.Components[0].Status = "unknown"
		}
		if name == "existing-update-repair" {
			w.deps.assessCandidate = func() ([]string, error) {
				return []string{
					"Agent profile \"primary\": The candidate catalog omits this model. Work that uses this profile stays stopped until repair in the dashboard.",
					"Agent profile \"spare\": The candidate catalog omits this effort. The disabled profile keeps its saved choices.",
					"Connection \"local\" requires repair. The candidate catalog omits its provider. The saved connection remains intact.",
				}, nil
			}
		}
		if name == "existing-update-components" {
			state.Release.Components[3].Installed = "34b926d7ce43"
		}
		w.deps.stop = func(string) error { return nil }
	case "existing-diagnostic":
		preview.steps = []setupPreviewStep{
			{Wait: "q exit", Send: "\x1b[B\r", Name: "Preparation failure"},
			{Wait: "q exit", Send: "q", Name: "Diagnostic details"},
		}
		preview.run = func() {
			err := w.operation("Prepare Culler", func() error {
				return setupSectionResult("home", 1, []byte(`{"error":{"message":"database reset requires com.culler.fixture.resident to be unloaded"}}`), nil)
			})
			if !errors.Is(err, onboarding.ErrExit) {
				t.Fatalf("diagnostic preview: %v", err)
			}
		}
	case "final-check-failure":
		state.Services = map[string]bool{"resident": true, "dashboard": false}
		state.Checks[1].Status = "deferred"
		state.Checks[1].Detail = "not checked: fixture check unavailable"
		preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Final check failure"}}
		preview.run = func() {
			err := w.operation("Check Culler", func() error { _, err := setupReadinessResult(w.home, state); return err })
			if !errors.Is(err, onboarding.ErrExit) {
				t.Fatal(err)
			}
		}
	case "incompatible-database":
		preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Incompatible database"}}
		preview.run = func() {
			err := w.operation("Prepare Culler", func() error {
				return setupSectionResult("home", 1, []byte(`{"error":{"message":"database schema differs from this build"}}`), nil)
			})
			if !errors.Is(err, onboarding.ErrExit) {
				t.Fatal(err)
			}
		}

	}
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
	if preview.run == nil {
		preview.run = func() {
			_, err := w.inspectExistingDeployment()
			if err != nil && !errors.Is(err, onboarding.ErrExit) {
				t.Fatal(err)
			}
		}
	}
	return preview
}
