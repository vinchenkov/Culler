//go:build !test_fake_routing

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/deployment"
	"mc/onboarding"
)

func TestTerminalSetupAPIKeyReplacementPreservesSafeFailure(t *testing.T) {
	for _, failure := range []*deployment.APIKeyReplacementError{
		{Status: 401}, {Status: 403}, {Status: 429}, {Status: 503}, {}, {InvalidResponse: true},
	} {
		err := setupRuntimeFailure(fmt.Errorf("provider check: %w", failure), "The host could not save the API key.")
		var shown *setupFailure
		if !errors.As(err, &shown) || shown.cause != failure.Error() || len(shown.details) != 0 {
			t.Fatal("The terminal hid the safe cause or replaced it with a diagnostic record.")
		}
	}
}

func TestTerminalSetupAPIKeyResumesAndRetriesWithoutAnotherKey(t *testing.T) {
	for _, harness := range []string{"codex", "claude-sdk"} {
		t.Run(harness, func(t *testing.T) {
			const secret = "synthetic-private-api-key"
			w, ui := setupAccessFixture(t,
				setupChoiceAnswer{prompt: "API key", value: secret},
				setupChoiceAnswer{prompt: "Model", err: onboarding.ErrExit},
			)
			saved, saves := false, 0
			w.deps.identity = func(string, string) (string, error) {
				if saved {
					return strings.Repeat("a", 64), nil
				}
				return "", nil
			}
			w.deps.saveKey = func(_ string, request deployment.AgentConnectionRequest) (deployment.AgentProfileCatalog, error) {
				if request.APIKey != secret || request.AuthorizationID != "" {
					t.Fatal("The connection form changed the API key or its revision")
				}
				saved = true
				saves++
				return deployment.AgentProfileCatalog{}, nil
			}
			if err := w.apiKeyAccess(harness); !errors.Is(err, onboarding.ErrExit) {
				t.Fatalf("The model prompt did not preserve an interruption: %v", err)
			}
			if !saved || saves != 1 || len(ui.prompts) != 2 || !ui.prompts[0].Secret {
				t.Fatal("The key prompt did not save one masked key")
			}
			setupAccessReload(t, w)
			if w.session.Access == nil || w.session.Access.Step != "model" {
				t.Fatal("The saved session did not resume at the model prompt")
			}
			tests := 0
			w.deps.connect = func(_ *setupWizard, connection setupConnection) error {
				if connection.Mode != "api_key" {
					t.Fatal("The API connection selected native sign-in")
				}
				tests++
				if tests == 1 {
					return errors.New("The model test failed")
				}
				return nil
			}
			ui.answers = []setupChoiceAnswer{{prompt: "Model", value: "fixture-cheap-model"}, {prompt: "Test this connection?", value: "check"}}
			if err := w.resumeAccess(); err != nil {
				t.Fatal(err)
			}
			if w.session.Access != nil || len(w.session.RuntimeIncomplete) != 1 || saves != 1 {
				t.Fatal("The failed model test lost the saved key state")
			}
			ui.answers = []setupChoiceAnswer{{prompt: "Choose the API key", value: "saved-key"}, {prompt: "Model", value: "fixture-cheap-model"}, {prompt: "Test this connection?", value: "check"}}
			if err := w.apiKeyAccess(harness); err != nil {
				t.Fatal(err)
			}
			if saves != 1 || tests != 2 || len(w.session.RuntimeBindings) != 1 || len(w.session.RuntimeIncomplete) != 0 {
				t.Fatal("The retry did not reuse the saved key")
			}
			if err := filepath.WalkDir(w.home, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				body, err := os.ReadFile(path)
				if err == nil && strings.Contains(string(body), secret) {
					t.Fatal("The setup saved an API key in a deployment file")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
