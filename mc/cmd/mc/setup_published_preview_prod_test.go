//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/deployment"
	"mc/distribution"
	"mc/onboarding"
	"mc/verbs"
)

func init() {
	for _, name := range []string{"published-update", "published-schema-failure", "published-drain-failure", "published-newer", "published-recovery", "published-update-complete", "published-update-stops", "published-recovery-complete", "setup-dashboard-stopped", "existing-dashboard-stopped", "published-update-all-stopped", "setup-command-failure", "published-cancel"} {
		setupPreviewCases[name] = func(t *testing.T) setupPreviewCase { return setupPreviewPublished(t, name) }
	}
}

func setupPreviewPublished(t *testing.T, name string) setupPreviewCase {
	if name == "setup-command-failure" || name == "published-cancel" {
		w, _ := setupTestWizard(t)
		preview := setupPreviewCase{wizard: w, steps: []setupPreviewStep{}}
		if name == "setup-command-failure" {
			preview.run = func() {
				w.problem("Culler command requires attention", w.failureLines("Check the command", &setupFailure{cause: "A new zsh shell cannot resolve culler. Check its shell configuration.", command: cullerInstallCommand(w.home)}))
			}
		} else {
			preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "\x03", Name: "Cancel setup"}}
			preview.run = func() {
				if err := w.welcome(); !errors.Is(err, context.Canceled) || !errors.Is(err, onboarding.ErrExit) {
					t.Fatal(err)
				}
				p := &publishedSetup{}
				if err := w.ui.Activity("Restore the previous service state", p.cancelOperation); err != nil {
					t.Fatal(err)
				}
				w.exitNotice()
			}
		}
		return preview
	}
	if strings.HasSuffix(name, "-complete") || name == "published-update-stops" || name == "published-update-all-stopped" || strings.HasSuffix(name, "dashboard-stopped") {
		return setupPreviewPublishedComplete(t, name)
	}
	w, _ := setupTestWizard(t)
	state := completeSetupDeployment()
	state.Update = true
	state.Release = setupRelease{Installed: strings.Repeat("a", 40), Proposed: strings.Repeat("b", 40), InstalledDisplay: "0.1.0 (" + strings.Repeat("a", 12) + ")", ProposedDisplay: "0.2.0 (" + strings.Repeat("b", 12) + ")", SourceState: "published", Relation: "newer", Components: []setupReleaseComponent{{Name: "Service release", Status: "different"}, {Name: "Host files", Status: "different"}, {Name: "Runner files", Status: "different"}, {Name: "Agent image", Installed: "unknown", Status: "incomplete"}}}
	w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
	w.deps.stop = func(string) error { return nil }
	preview := setupPreviewCase{wizard: w, steps: []setupPreviewStep{{Wait: "q exit", Send: "\r", Name: "Published release update"}}}
	w.deps.beforeChange = func(_ string, _ verbs.OnboardArgs, report func(string)) error {
		report("Download the agent image")
		report("The agent image passed its identity check")
		report("Check database compatibility")
		switch name {
		case "published-schema-failure":
			return &setupFailure{cause: "This release cannot use the database. Setup left the installed release intact.", details: []string{"The database schema differs from this release."}}
		}
		report("Wait for active agents and repository work to finish")
		if name == "published-drain-failure" {
			return publishedSetupFailure(errors.New("Culler still has active work or cannot pause for this update. Close agent sessions through Culler, then retry install.sh."))
		}
		return nil
	}
	if name == "published-schema-failure" || name == "published-drain-failure" {
		preview.steps = append(preview.steps, setupPreviewStep{Wait: "q exit", Send: "q", Name: "Release refusal"})
	}
	if name == "published-newer" {
		state.Release.Relation = "older"
		state.Release.InstalledDisplay, state.Release.ProposedDisplay = "0.2.0 ("+strings.Repeat("a", 12)+")", "0.1.0 ("+strings.Repeat("b", 12)+")"
		preview.steps = []setupPreviewStep{}
	}
	if name == "published-recovery" {
		preview.steps = []setupPreviewStep{}
		prior := publishedTestRecord(strings.Repeat("a", 40), 20)
		pending := publishedTestRecord(strings.Repeat("b", 40), 21)
		prior.Version, prior.Tag, pending.Version, pending.Tag = "0.1.0", "v0.1.0", "0.2.0", "v0.2.0"
		if err := distribution.Save(filepath.Join(w.home, distribution.InstalledRecord), prior); err != nil {
			t.Fatal(err)
		}
		if err := distribution.Save(filepath.Join(w.home, "state", "pending-release.json"), pending); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(w.home, "supervision"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.home, "supervision", "resident.json"), []byte(`{"releaseBuildId":"`+pending.Commit+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CULLER_RELEASE_RECORD", filepath.Join(w.home, "state", "pending-release.json"))
		preview.run = func() {
			w.servicesTouched = true
			w.serviceStates = map[string]bool{"resident": false, "dashboard": false}
			w.problem("Setup requires attention", w.failureLines("Check Culler", &setupFailure{cause: "The dashboard did not answer its authenticated readiness check."}))
			w.serviceNotice()
		}
		return preview
	}
	preview.run = func() {
		_, err := w.inspectExistingDeployment()
		if err != nil && !errors.Is(err, onboarding.ErrExit) {
			t.Fatal(err)
		}
		if errors.Is(err, onboarding.ErrExit) {
			w.exitNotice()
		}
	}
	return preview
}

func setupPreviewPublishedComplete(t *testing.T, name string) setupPreviewCase {
	preview := setupPreviewProject(t, false)
	w := preview.wizard
	stopped := name == "published-update-stops" || name == "published-update-all-stopped" || strings.HasSuffix(name, "dashboard-stopped")
	state := completeSetupDeployment()
	state.Services = setupServiceMap(true)
	state.Release = setupRelease{Installed: strings.Repeat("a", 40), Proposed: strings.Repeat("a", 40), InstalledDisplay: "0.1.0 (" + strings.Repeat("a", 12) + ")", ProposedDisplay: "0.1.0 (" + strings.Repeat("a", 12) + ")", SourceState: "published", Components: []setupReleaseComponent{{Name: "Service release", Status: "same"}}}
	if stopped {
		state.Stops = deployment.ServiceStops{"dashboard": true}
		state.Services["dashboard"] = false
		if name == "published-update-all-stopped" {
			for _, unit := range deployment.SupervisedUnits {
				state.Stops[unit] = true
				state.Services[unit] = false
			}
			state.Active = false
		}
		if err := deployment.WriteServiceStops(w.home, state.Stops); err != nil {
			t.Fatal(err)
		}
	}
	w.deps.ready = func(home string) ([]string, error) { return setupReadinessResult(home, state) }
	if name == "existing-dashboard-stopped" {
		w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
		preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "\r", Name: "Preserved dashboard stop"}}
		preview.run = func() {
			if _, err := w.inspectExistingDeployment(); err != nil {
				t.Fatal(err)
			}
		}
		return preview
	}
	preview.steps = []setupPreviewStep{{Wait: "q exit", Send: "\r", Name: "Review service activation"}}
	if !stopped {
		preview.steps = append(preview.steps, setupPreviewStep{Wait: "q exit", Send: "\r", Name: "Open the authenticated dashboard"})
	}
	preview.run = func() {
		if name == "published-recovery-complete" {
			attempt := 0
			if err := w.operation("Check Culler", func() error {
				attempt++
				if attempt == 1 {
					return &setupFailure{cause: "The dashboard did not answer its authenticated readiness check."}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		w.session.Completed = append([]string(nil), setupStageIDs[:3]...)
		for i, stage := range []func() error{w.review, w.start, w.dashboard} {
			w.session.Stage = i + 3
			w.showStage(i + 3)
			if err := stage(); err != nil {
				t.Fatal(err)
			}
			if err := w.mark(setupStageIDs[i+3]); err != nil {
				t.Fatal(err)
			}
		}
		if terminal, ok := w.ui.(interface{ Complete() }); ok {
			terminal.Complete()
		}
	}
	if name == "published-recovery-complete" {
		preview.steps = append([]setupPreviewStep{{Wait: "q exit", Send: "\r", Name: "Retry the readiness check"}}, preview.steps...)
	}
	if strings.HasPrefix(name, "published-update") {
		state.Update = true
		state.Release = setupRelease{Installed: strings.Repeat("a", 40), Proposed: strings.Repeat("b", 40), InstalledDisplay: "0.1.0 (" + strings.Repeat("a", 12) + ")", ProposedDisplay: "0.2.0 (" + strings.Repeat("b", 12) + ")", SourceState: "published", Relation: "newer", Components: []setupReleaseComponent{{Name: "Service release", Status: "different"}}}
		w.deps.inspect = func(string, verbs.OnboardArgs) (setupDeployment, error) { return state, nil }
		w.deps.stop = func(string) error { return nil }
		w.deps.beforeChange = func(_ string, _ verbs.OnboardArgs, report func(string)) error {
			report("Check the image and database before service changes")
			return nil
		}
		w.deps.publish = func() error { return nil }
		complete := preview.run
		bindings, verified, identities, routing := w.session.RuntimeBindings, w.session.RuntimeVerified, w.session.RuntimeIdentities, w.session.RoutingFile
		preview.steps = append([]setupPreviewStep{{Wait: "q exit", Send: "\r", Name: "Review the published update"}}, preview.steps...)
		preview.run = func() {
			if _, err := w.inspectExistingDeployment(); err != nil {
				t.Fatal(err)
			}
			w.session.RuntimeBindings, w.session.RuntimeVerified, w.session.RuntimeIdentities, w.session.RoutingFile = bindings, verified, identities, routing
			state.Update = false
			state.Release.Installed = state.Release.Proposed
			state.Release.Components = nil
			complete()
		}
	}
	return preview
}
