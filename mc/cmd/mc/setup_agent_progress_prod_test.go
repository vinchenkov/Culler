//go:build !test_fake_routing

package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"mc/deployment"
	"mc/onboarding"
)

func TestTerminalSetupManualConnectionReportsProgressAndResult(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			previous := setupDiagnosticRoot
			t.Cleanup(func() { setupDiagnosticRoot = previous })
			setupDiagnosticRoot = t.TempDir()
			w, ui := setupTestWizard(t, "check")
			cfg, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			binding := setupBindingForKind(cfg, setupHarnessKind("claude-sdk"))
			w.deps.connect = func(*setupWizard, setupConnection) error {
				if !slices.Contains(ui.activities, "Connect Claude Code") {
					t.Fatal("The connection started without a progress display.")
				}
				if w.session.RuntimeVerified[binding] {
					t.Fatal("Setup verified the account before the connection completed.")
				}
				if failed {
					return errors.New("fixture-provider-secret")
				}
				return nil
			}
			if err := w.checkConnection(setupConnection{Binding: binding, Mode: "login"}); err != nil {
				t.Fatal(err)
			}
			if len(ui.activityErrors) != 1 || (ui.activityErrors[0] != nil) != failed {
				t.Fatal("The progress display reported an incorrect connection result.")
			}
			if w.session.RuntimeVerified[binding] == failed {
				t.Fatal("Setup saved an incorrect connection result.")
			}
			if strings.Contains(strings.Join(ui.panels, "\n"), "fixture-provider-secret") {
				t.Fatal("The connection failure exposed a credential.")
			}
			// The activity line names the agent and the result. The failure panel adds the cause and the diagnostic record.
			if failed && (len(ui.panels) != 1 || !strings.HasPrefix(ui.panels[0], "\n") || strings.Contains(ui.panels[0], "connection failed") ||
				!strings.Contains(ui.panels[0], "\nShare this diagnostic record with your coding harness:\n"+setupDiagnosticRoot)) {
				t.Fatalf("The failure panel repeats the failure line or omits the diagnostic record: %q", ui.panels)
			}
		})
	}
}

func TestTerminalSetupAgentProgressPrecedesReview(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			w, ui := setupAgentFixture(t, "login:codex", "check", ":exit")
			w.session.ReleaseID = releaseBuildID
			w.session.Stage = 2
			w.session.Completed = []string{"welcome", "prepare"}
			setupFakeSignIn(w, func(setupConnection) error {
				if slices.Contains(ui.stages, 4) {
					t.Fatal("the review appeared before the connection completed")
				}
				if failed {
					return errors.New("the provider rejected the credential")
				}
				return nil
			})
			if err := w.run(); !errors.Is(err, onboarding.ErrExit) {
				t.Fatal(err)
			}
			want := []int{3, 4}
			if failed {
				want = []int{3}
			}
			if !slices.Equal(ui.stages, want) {
				t.Fatalf("stages = %v, want %v", ui.stages, want)
			}
			if !failed && !slices.Contains(ui.completed[1], "agents") {
				t.Fatal("the review shows an incomplete connection")
			}
			cfg, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(ui.activities, "Connect "+w.agentName(setupBindingForKind(cfg, setupHarnessKind("codex")))) {
				t.Fatal("the connection has no progress display")
			}
		})
	}
}

func init() {
	for _, failed := range []bool{false, true} {
		name := "agent-progress"
		if failed {
			name = "agent-progress-failure"
		}
		setupPreviewCases[name] = func(t *testing.T) setupPreviewCase {
			w, _ := setupAgentFixture(t)
			w.session.ReleaseID = releaseBuildID
			w.session.Stage = 2
			w.session.Completed = []string{"welcome", "prepare"}
			setupFakeSignIn(w, func(setupConnection) error {
				time.Sleep(2500 * time.Millisecond)
				if failed {
					return setupRuntimeFailure(&deployment.TokenRefreshError{Status: 401, Code: "refresh_token_reused"}, "")
				}
				return nil
			})
			return setupPreviewCase{
				wizard: w,
				steps: []setupPreviewStep{
					{Wait: "q exit", Send: "\r", Name: "Sign in to Codex"},
					{Wait: "q exit", Send: "\r", Name: "Sign in and connect"},
					{Wait: "q exit", Send: "q", Name: "Connection result"},
				},
				run: func() {
					if err := w.run(); !errors.Is(err, onboarding.ErrExit) {
						t.Fatal(err)
					}
				},
			}
		}
	}
}
