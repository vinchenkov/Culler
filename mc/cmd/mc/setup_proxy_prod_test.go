//go:build !test_fake_routing

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"mc/deployment"
)

func TestSetupProviderProxyFailureIdentifiesLocalRepair(t *testing.T) {
	w, _ := setupTestWizard(t)
	for _, message := range []string{
		"The host provider proxy release is unavailable.",
		"The host Bun executable is unavailable.",
		"The temporary host provider proxy could not start.",
		"The temporary host provider proxy did not become ready.",
		"fork/exec bun: permission denied",
	} {
		t.Run(message, func(t *testing.T) {
			original := fmt.Errorf("model test: %w", &deployment.ProviderProxyStartError{Err: errors.New(message)})
			if setupCause(original) != setupProviderProxyStartCause {
				t.Fatal("The direct failure does not identify the local repair.")
			}
			failure := setupRuntimeFailure(original, "The cause is unknown. Sign in again.")
			if setupCause(failure) != setupProviderProxyStartCause {
				t.Fatal("The model test blamed the connection or lost the repair action.")
			}
			record, err := writeSetupDiagnostic(w.home, "Connect Codex", nil, failure)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(strings.TrimSuffix(record, "/diagnostic.json")) })
			body, err := os.ReadFile(record)
			if err != nil || !strings.Contains(string(body), message) {
				t.Fatal("The diagnostic record lost the local startup failure.")
			}
		})
	}
}

func TestSetupProviderProxyFailureScrubsItsDiagnostic(t *testing.T) {
	setupTestWizard(t)
	failure := setupRuntimeFailure(&deployment.ProviderProxyStartError{
		Err: errors.New("The host provider proxy could not start: fixture-dashboard-access"),
	}, "The cause is unknown.")
	lines := strings.Join(setupDiagnosticLines("Test Codex", failure), "\n")
	if strings.Contains(lines, "fixture-dashboard-access") || !strings.Contains(lines, "The host provider proxy could not start") {
		t.Fatal("The proxy diagnostic exposed access or lost the startup failure.")
	}
	if strings.Contains(setupCause(failure), "credentials") || strings.Contains(setupCause(failure), "Sign in") {
		t.Fatal("The local startup failure blamed credentials.")
	}
}
