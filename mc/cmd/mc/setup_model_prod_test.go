//go:build !test_fake_routing

package main

import (
	"fmt"
	"strings"
	"testing"

	"mc/deployment"
)

func TestTerminalSetupExplainsAnUnverifiedCodexModel(t *testing.T) {
	w, ui := setupTestWizard(t)
	previous := setupDiagnosticRoot
	setupDiagnosticRoot = t.TempDir()
	t.Cleanup(func() { setupDiagnosticRoot = previous })
	err := setupRuntimeFailure(fmt.Errorf("connection check: %w", &deployment.CodexSetupModelError{}), "The cause is unknown.")
	if err := w.finishConnection(setupConnection{Binding: "chatgpt"}, false, err); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(ui.panels, "\n")
	if !strings.Contains(joined, "Setup cannot verify the model for this Codex connection.") || !strings.Contains(joined, "diagnostic.json") || w.session.RuntimeVerified["chatgpt"] {
		t.Fatalf("Setup lost the specific cause or marked the profile ready: %s", joined)
	}
}
