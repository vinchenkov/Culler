//go:build !test_fake_routing

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc/deployment"
	"mc/onboarding"
)

func init() {
	for _, variant := range []string{"", "failure", "failure-unsaved"} {
		name, failed := "login-progress", variant != ""
		if failed {
			name += "-" + variant
		}
		setupPreviewCases[name] = func(t *testing.T) setupPreviewCase {
			if variant == "failure-unsaved" {
				previous := setupDiagnosticRoot
				t.Cleanup(func() { setupDiagnosticRoot = previous })
				setupDiagnosticRoot = filepath.Join(t.TempDir(), "absent")
			}
			w, _ := setupTestWizard(t)
			cfg, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			binding := setupBindingForKind(cfg, setupHarnessKind("claude-sdk"))
			bin := t.TempDir()
			script := `#!/bin/sh
/bin/sleep 1.2
printf '%s\n' 'https://example.test/oauth/authorize?client_id=fixture&response_type=code&state=fixture'
/bin/sleep 1.5
umask 077
printf '%s\n' '{"claudeAiOauth":{"accessToken":"fixture-access","refreshToken":"fixture-refresh","expiresAt":9999999999999,"scopes":["user:inference"]}}' > "$CLAUDE_CONFIG_DIR/.credentials.json"
`
			if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			previousLogin, previousVerifier := productionRuntimeAuthLogin, productionRuntimeAuthVerifier
			t.Cleanup(func() {
				productionRuntimeAuthLogin, productionRuntimeAuthVerifier = previousLogin, previousVerifier
			})
			productionRuntimeAuthLogin = nil
			productionRuntimeAuthVerifier = deployment.RuntimeAuthVerifyFunc(func(string, string) error {
				time.Sleep(1200 * time.Millisecond)
				if failed {
					return errors.New("The fixture account failed its check.")
				}
				return nil
			})
			w.deps.connect = setupConnect
			return setupPreviewCase{
				wizard: w,
				steps: []setupPreviewStep{
					{Wait: "q exit", Send: "\r", Name: "Connect"},
					{Wait: "Connect Claude Code .", Name: "Start sign-in"},
					{Wait: "https://example.test", Name: "Browser sign-in"},
					{Wait: "q exit", Send: "q", Name: "Connection result"},
				},
				run: func() {
					w.ui.Stage(3, setupStageTitles[2], setupStageIDs[:2])
					if err := w.checkConnection(setupConnection{Binding: binding, Mode: "login"}); err != nil {
						t.Fatal(err)
					}
					if err := w.agents(); !errors.Is(err, onboarding.ErrExit) && err != nil {
						t.Fatal(err)
					}
					if !failed {
						w.ui.Stage(4, setupStageTitles[3], setupStageIDs[:3])
						if err := w.review(); !errors.Is(err, onboarding.ErrExit) {
							t.Fatal(err)
						}
					}
				},
			}
		}
	}
}
