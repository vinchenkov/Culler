//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mc/deployment"
)

// TestCullerTerminalPreview renders real commands with isolated service dependencies.
func TestCullerTerminalPreview(t *testing.T) {
	scenario := os.Getenv("CULLER_TERMINAL_PREVIEW")
	if scenario == "" {
		return
	}
	if strings.HasPrefix(scenario, "uninstall") {
		cullerUninstallPreview(t, scenario)
		return
	}
	home, observe := cullerObservationFixture(t)
	args, expected := []string{"status"}, 0
	deps := cullerDependencies{inspect: func(ctx context.Context, home string) cullerStatus { return inspectCullerWith(ctx, home, observe) }, manager: newCullerManager, release: readPublishedReleaseStatus}
	switch {
	case scenario == "help", scenario == "version":
		args = []string{scenario}
	case strings.HasPrefix(scenario, "help-"):
		args = []string{"help", strings.TrimPrefix(scenario, "help-")}
	case scenario == "usage":
		args, expected = []string{"logs", "--tail", "0"}, 2
	case scenario == "version-mismatch":
		args = []string{"version"}
		releaseBuildID = strings.Repeat("b", 40)
	case scenario == "version-files-mismatch":
		args, expected = []string{"version"}, 1
		path := filepath.Join(home, "culler")
		if err := os.WriteFile(path, []byte("verified command"), 0755); err != nil {
			t.Fatal(err)
		}
		digest, err := deployment.CLIFileDigest(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := writePublishedState(filepath.Join(home, deployment.CLIInstallRecord), deployment.CLIInstallation{Path: path, SHA256: digest, Release: releaseBuildID, Verified: true}); err != nil {
			t.Fatal(err)
		}
	case scenario == "status-absent":
		home, expected = filepath.Join(t.TempDir(), "absent"), 1
	case scenario == "status-stopped":
		observe.loaded = func(context.Context, string) (bool, error) { return false, nil }
		if err := deployment.WriteServiceStops(home, deployment.ServiceStops{"resident": true, "dashboard": true, "agentsview": true}); err != nil {
			t.Fatal(err)
		}
	case scenario == "status-partial":
		expected = 1
		observe.ready = func(_ context.Context, unit, _ string, _ cullerRuntime) error {
			if unit == "dashboard" {
				return errors.New("The dashboard did not answer its authenticated service check.")
			}
			return nil
		}
	case scenario == "status-unknown":
		expected = 1
		observe.loaded = func(_ context.Context, unit string) (bool, error) {
			if unit == "resident" {
				return false, errors.New("The macOS service state is unavailable.")
			}
			return true, nil
		}
	case scenario == "incomplete-setup":
		args, expected = []string{"start"}, 1
		if err := os.Remove(filepath.Join(home, "supervision", "resident.json")); err != nil {
			t.Fatal(err)
		}
	case scenario == "status-cancel":
		expected = 130
		var interrupt sync.Once
		observe.loaded = func(ctx context.Context, _ string) (bool, error) {
			interrupt.Do(func() { process, _ := os.FindProcess(os.Getpid()); _ = process.Signal(os.Interrupt) })
			<-ctx.Done()
			return false, ctx.Err()
		}
	case scenario == "status-ready":
	case scenario == "logs", scenario == "logs-empty":
		args = []string{"logs"}
		if scenario == "logs" {
			if err := os.MkdirAll(filepath.Join(home, "logs"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "logs", "resident.stderr.log"), []byte("2026-11-04T12:00:00Z INFO The resident started.\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	case scenario == "update-check", scenario == "update-unknown", scenario == "update-check-cancel", scenario == "update-check-wait":
		args = []string{"update", "--check"}
		deps.release = func(ctx context.Context, _ string) releaseStatus {
			if scenario == "update-check-wait" {
				time.Sleep(2200 * time.Millisecond)
			}
			if scenario == "update-check-cancel" {
				time.Sleep(1100 * time.Millisecond)
				process, _ := os.FindProcess(os.Getpid())
				_ = process.Signal(os.Interrupt)
				<-ctx.Done()
			}
			s := releaseStatus{Installed: releaseBuildID, Available: strings.Repeat("b", 40), State: "available", Detail: "A newer Culler release is available."}
			if scenario == "update-unknown" {
				s.State, s.Available, s.Detail = "unknown", "", "The release source is unavailable. Check your connection, then retry the release check."
			}
			return s
		}
		if scenario == "update-unknown" {
			expected = 1
		}
		if scenario == "update-check-cancel" {
			expected = 130
		}
	case scenario == "update-recovery":
		args, expected = []string{"update"}, 1
	default:
		m, control, database := cullerManagerFixture(t)
		m.home, m.maintenance.home = home, home
		control.loaded = map[string]bool{}
		for _, unit := range deployment.SupervisedUnits {
			control.loaded[cullerLabel(home, unit)] = true
		}
		deps.manager = func(string) (*cullerManager, error) { return m, nil }
		observe.loaded = func(_ context.Context, unit string) (bool, error) {
			return control.loaded[cullerLabel(home, unit)], nil
		}
		command, service := scenario, "dashboard"
		switch scenario {
		case "start":
			control.loaded[cullerLabel(home, service)] = false
		case "stop":
			service = ""
		case "wait-timeout":
			command, service, expected = "stop", "resident", 1
			database.Runs = 1
			prior := m.maintenance.sleep
			m.maintenance.sleep = func(d time.Duration) { time.Sleep(1100 * time.Millisecond); prior(d) }
		case "cancel":
			command, expected = "stop", 130
			control.onStop = func(string) {
				process, err := os.FindProcess(os.Getpid())
				if err != nil {
					t.Fatal(err)
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
				time.Sleep(50 * time.Millisecond)
			}
		case "service-timeout":
			command, expected = "restart", 1
			m.ready = func(context.Context, string, time.Time) error {
				return errors.New("The dashboard did not answer its service check.")
			}
			m.sleep = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
			observe.ready = func(_ context.Context, unit, _ string, _ cullerRuntime) error {
				if unit == "dashboard" {
					return errors.New("The dashboard did not answer its service check.")
				}
				return nil
			}
		case "restart-failure":
			command, expected = "restart", 1
			control.failStart = cullerLabel(home, service)
		}
		args = []string{command}
		if service != "" {
			args = append(args, service)
		}
	}
	if scenario != "help-uninstall" {
		args = append(args, "--mc-home", home)
	}
	code := runCullerWith(args, os.Stdin, os.Stdout, os.Stderr, deps)
	if path := os.Getenv("CULLER_PREVIEW_RESULT"); path != "" {
		body, _ := json.Marshal(map[string]any{"cli_arguments": args, "cli_exit_status": code, "expected_cli_exit_status": expected, "fixture_success": code == expected})
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if code != expected {
		t.Fatalf("The command returned %d; expected %d.", code, expected)
	}
}

func init() {
	for _, name := range []string{"cli-setup", "cli-setup-current"} {
		setupPreviewCases[name] = func(t *testing.T) setupPreviewCase {
			preview := setupPreviewProject(t, false)
			bin := filepath.Join(preview.wizard.home, "command bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			source, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if err := copyPublishedFile(source, filepath.Join(bin, "culler"), 0755); err != nil {
				t.Fatal(err)
			}
			userHome := t.TempDir()
			t.Setenv("HOME", userHome)
			t.Setenv("ZDOTDIR", userHome)
			t.Setenv("MC_BIN_DIR", bin)
			parentPath := "/usr/bin:/bin"
			if name == "cli-setup-current" {
				parentPath = bin + ":" + parentPath
			}
			t.Setenv("CULLER_PARENT_PATH", parentPath)
			if _, err := deployment.InstallCLIPath(preview.wizard.home, userHome, bin, "development", parentPath); err != nil {
				t.Fatal(err)
			}
			return preview
		}
	}
}
