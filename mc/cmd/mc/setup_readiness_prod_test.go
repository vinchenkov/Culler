//go:build !test_fake_routing

package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/onboarding"
	"mc/verbs"
)

func TestTerminalSetupDashboardRequiresSuccessfulHealth(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantOK     bool
	}{
		{"ready", `{"ok":true,"findings":[{"status":"ok"}]}`, http.StatusOK, true},
		{"failed service", `{"ok":false,"findings":[{"status":"fail"}]}`, http.StatusOK, false},
		{"absent result", `{"findings":[{"status":"ok"}]}`, http.StatusOK, false},
		{"absent checks", `{"ok":true,"findings":[]}`, http.StatusOK, false},
		{"unreadable result", `<html>unavailable</html>`, http.StatusOK, false},
		{"access refused", `{"ok":true,"findings":[{"status":"ok"}]}`, http.StatusUnauthorized, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := setupTestWizard(t)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {
				requests++
				if req.URL.Path != "/api/health" || req.Header.Get("Authorization") != "Bearer fixture-dashboard-access" {
					t.Error("the readiness request lost its endpoint or authentication")
				}
				out.WriteHeader(tc.status)
				_, _ = io.WriteString(out, tc.body)
			}))
			defer server.Close()
			cfg, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Surfaces.Dashboard.Bind = strings.TrimPrefix(server.URL, "http://")
			if _, err := operatorconfig.WriteAtomic(filepath.Join(w.home, "config.toml"), cfg); err != nil {
				t.Fatal(err)
			}
			err = setupCheckDashboard(w.home)
			if (err == nil) != tc.wantOK || requests != 1 {
				t.Fatalf("check=%v requests=%d", err, requests)
			}
			if err != nil && strings.Contains(err.Error(), "fixture-dashboard-access") {
				t.Fatal("the dashboard token reached the failure result")
			}
		})
	}
}

type setupRecoveryTestUI struct {
	*setupTestUI
	failed bool
}

func (u *setupRecoveryTestUI) Choose(prompt string, choices []onboarding.Choice, selected int) (string, error) {
	if !u.failed {
		u.failed = true
		return "", errors.New("the prompt failed")
	}
	return u.setupTestUI.Choose(prompt, choices, selected)
}

func TestTerminalSetupStageFailureBackReturnsToSetup(t *testing.T) {
	w, ui := setupTestWizard(t, ":back", ":exit")
	w.ui = &setupRecoveryTestUI{setupTestUI: ui}
	if err := w.run(); !errors.Is(err, onboarding.ErrExit) {
		t.Fatalf("the recovery action left setup with %v", err)
	}
	if len(ui.answers) != 0 || w.session.Stage != 0 {
		t.Fatal("the recovery action did not return to the previous stage")
	}
}

type setupFlowTestUI struct {
	*setupTestUI
	workspace string
	inputs    int
}

func (u *setupFlowTestUI) Input(prompt, _ string, secret bool) (string, error) {
	if prompt != "Project folder path" || secret {
		u.t.Fatalf("unexpected input %s", prompt)
	}
	u.inputs++
	return u.workspace, nil
}

func TestTerminalSetupDefaultFlowReachesDashboardWithoutPipeline(t *testing.T) {
	w, ui := setupTestWizard(t, "continue", "provider:codex", "login", "check", "start", "open")
	workspace := t.TempDir()
	flow := &setupFlowTestUI{setupTestUI: ui, workspace: workspace}
	w.ui = flow
	sections := []string{}
	w.deps.section = func(section string, a verbs.OnboardArgs) error {
		sections = append(sections, section)
		if a.Smoke || a.SmokeApproveTask != 0 {
			t.Fatal("setup selected a pipeline task")
		}
		if a.ActivateSupervision != (section == "supervision") {
			t.Fatal("setup supplied activation consent to the wrong section")
		}
		if section == "supervision" && !slices.Contains(w.session.Completed, "review") {
			t.Fatal("service activation preceded the access review")
		}
		return nil
	}
	providerChecks, readinessChecks, browserLaunches := 0, 0, 0
	setupFakeSignIn(w, func(c setupConnection) error {
		providerChecks++
		if c.Mode != "login" {
			t.Fatal("the connection test lost the sign-in")
		}
		return nil
	})
	w.deps.ready = func(string) ([]string, error) {
		readinessChecks++
		return []string{"Resident service: ready", "Dashboard service: ready"}, nil
	}
	w.deps.open = func(address string) error {
		browserLaunches++
		if readinessChecks != 1 || !strings.Contains(address, "#token=fixture-dashboard-access") {
			t.Fatal("the dashboard handoff lost its readiness check or authentication")
		}
		return nil
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if len(ui.answers) != 0 || flow.inputs != 0 || providerChecks != 1 || browserLaunches != 1 {
		t.Fatalf("answers=%v inputs=%d provider=%d browser=%d", ui.answers, flow.inputs, providerChecks, browserLaunches)
	}
	if !slices.Equal(sections, []string{"preflight", "home", "routing", "container", "tunables", "surfaces", "supervision"}) {
		t.Fatalf("sections=%v", sections)
	}
	if !slices.Equal(w.session.Completed, setupStageIDs) {
		t.Fatal("setup lost its completed stages")
	}
	if _, err := os.Lstat(filepath.Join(workspace, "VISION.md")); !os.IsNotExist(err) {
		t.Fatal("setup wrote the deferred vision")
	}
}
