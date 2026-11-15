//go:build !test_fake_routing

package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/onboarding"
	"mc/verbs"
)

type setupPreviewStep struct {
	Wait string `json:"wait"`
	Send string `json:"send"`
	Name string `json:"name,omitempty"`
}

type setupPreviewCase struct {
	wizard *setupWizard
	steps  []setupPreviewStep
	run    func()
}

var setupPreviewCases = map[string]func(*testing.T) setupPreviewCase{
	"project":         func(t *testing.T) setupPreviewCase { return setupPreviewProject(t, false) },
	"project-details": func(t *testing.T) setupPreviewCase { return setupPreviewProject(t, true) },
	"resume":          setupPreviewResume,
	"unknown-config":  setupPreviewUnknownConfig,
	"advanced":        setupPreviewAdvanced,
}

func TestTerminalSetupPreview(t *testing.T) {
	scenario := os.Getenv("MC_SETUP_PREVIEW_SCENARIO")
	if scenario == "" {
		return
	}
	create, ok := setupPreviewCases[scenario]
	if !ok {
		t.Fatalf("unknown setup preview scenario %q", scenario)
	}
	preview := create(t)
	plan, err := os.OpenFile(os.Getenv("MC_SETUP_PREVIEW_PLAN"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(plan).Encode(preview.steps); err != nil {
		_ = plan.Close()
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	terminal := onboarding.NewTerminal(os.Stdin, os.Stdout)
	terminal.SetStageTitles(setupStageTitles)
	preview.wizard.ui, preview.wizard.input, preview.wizard.output = terminal, os.Stdin, terminal
	preview.run()
}

func setupPreviewAdvanced(t *testing.T) setupPreviewCase {
	w, _, _ := setupProjectFixture(t)
	w.session.RoutingFile = setupRoleFixture(t, w)
	return setupPreviewCase{
		wizard: w,
		steps:  []setupPreviewStep{{Wait: "q exit", Send: "\r", Name: "Agent assignments"}},
		run: func() {
			w.ui.Stage(4, setupStageTitles[3], setupStageIDs[:3])
			if err := w.advanced(); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func setupPreviewProject(t *testing.T, _ bool) setupPreviewCase {
	w, _ := setupTestWizard(t)
	w.deps.ready = func(home string) ([]string, error) { return setupReadinessResult(home, completeSetupDeployment()) }
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	binding := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	w.session.RuntimeBindings = []string{binding}
	w.session.RuntimeVerified[binding] = true
	w.session.RuntimeIdentities[binding] = setupAccessIdentity(cfg, binding, strings.Repeat("a", 64))
	activated, opened := false, false
	w.deps.section = func(section string, args verbs.OnboardArgs) error {
		if args.Project != "" || args.WorkspaceRoot != "" || section == "project" {
			t.Fatal("terminal setup requested a project")
		}
		if section == "supervision" {
			if !args.ActivateSupervision {
				t.Fatal("activation consent is absent")
			}
			activated = true
		}
		return nil
	}
	w.deps.open = func(address string) error {
		if !activated || !strings.Contains(address, "#token=fixture-dashboard-access") {
			t.Fatal("the dashboard handoff lost activation or authentication")
		}
		opened = true
		return nil
	}
	return setupPreviewCase{wizard: w, steps: []setupPreviewStep{
		{Wait: "q exit", Send: "\r", Name: "Welcome"},
		{Wait: "q exit", Send: "\r", Name: "Service activation"},
		{Wait: "q exit", Send: "\r", Name: "Dashboard handoff"},
	}, run: func() {
		w.showStage(0)
		if err := w.welcome(); err != nil {
			t.Fatal(err)
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
		if !opened {
			t.Fatal("the dashboard did not open")
		}
	}}
}

func setupPreviewResume(t *testing.T) setupPreviewCase {
	t.Helper()
	w, _, project := setupProjectFixture(t)
	cfg, err := w.catalog()
	if err != nil {
		t.Fatal(err)
	}
	binding := setupBindingForKind(cfg, operatorconfig.CredentialKindCodexOAuth)
	w.session.RuntimeBindings = []string{binding}
	w.session.RuntimeVerified[binding] = true
	w.session.RuntimeIdentities[binding] = setupAccessIdentity(cfg, binding, strings.Repeat("a", 64))
	w.session.WorkspaceRoot, w.session.Project = project, "example-project"
	w.session.Stage, w.session.ReleaseID = 3, releaseBuildID
	w.session.Completed = append([]string(nil), setupStageIDs[:4]...)
	w.session.ProjectConfirmed, w.session.SandboxAccepted = true, true
	if err := w.save(); err != nil {
		t.Fatal(err)
	}
	return setupPreviewCase{
		wizard: w,
		steps:  []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Resume the review"}},
		run: func() {
			if err := w.revalidate(); err != nil {
				t.Fatal(err)
			}
			if w.session.Stage != 3 {
				t.Fatal("the resume lost its saved review stage")
			}
			w.ui.Stage(w.session.Stage+1, setupStageTitles[w.session.Stage], w.session.Completed)
			if err := w.review(); !errors.Is(err, onboarding.ErrExit) {
				t.Fatalf("exit from the saved review = %v", err)
			}
		},
	}
}

func setupPreviewUnknownConfig(t *testing.T) setupPreviewCase {
	w, _ := setupTestWizard(t)
	return setupPreviewCase{
		wizard: w,
		steps: []setupPreviewStep{
			{Wait: "q exit", Send: "\x1b[B\r", Name: "View error details"},
			{Wait: "q exit", Send: "q", Name: "Exit to correct the configuration"},
		},
		run: func() {
			w.ui.Stage(2, setupStageTitles[1], setupStageIDs[:1])
			err := w.operation("Prepare Culler", func() error {
				return &setupFailure{cause: setupSectionCause(
					"scaffold config.toml: config.toml carries unknown key \"budgets.review_wip_caap\"")}
			})
			if !errors.Is(err, onboarding.ErrExit) {
				t.Fatalf("exit after the configuration error = %v", err)
			}
		},
	}
}
