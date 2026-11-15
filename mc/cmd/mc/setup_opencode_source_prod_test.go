//go:build !test_fake_routing

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func init() {
	for _, stage := range []string{"download", "preparation", "validation"} {
		setupPreviewCases["source-opencode-"+stage] = func(t *testing.T) setupPreviewCase {
			w, _ := setupTestWizard(t)
			return setupPreviewCase{wizard: w, steps: []setupPreviewStep{
				{Wait: "Preparing Culler", Name: "Source package in progress"},
				{Wait: "q exit", Send: "\x1b[B\r", Name: "Source package failure and retry"},
				{Wait: "q exit", Send: "\r", Name: "Source package error details"},
				{Wait: "Preparing Culler", Name: "Source package retry in progress"},
			}, run: func() {
				w.ui.Stage(2, setupStageTitles[1], []string{"welcome"})
				attempts := 0
				if err := w.operation("Prepare Culler", func() error {
					attempts++
					time.Sleep(500 * time.Millisecond)
					if attempts == 1 {
						return sourceOpenCodeSetupFailure(stage)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}}
		}
	}
}

func sourceOpenCodeSetupFailure(stage string) error {
	causes := map[string]string{
		"download":    "npm error code ENETUNREACH\nnpm error The package registry is unreachable.",
		"preparation": "/bin/sh: npm: not found",
		"validation":  "The installed OpenCode files differ from the archive.",
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]string{
		"message": "update OpenCode: The OpenCode " + stage + " from source failed: " + causes[stage]}})
	return setupSectionResult("home", 1, body, nil)
}

func TestOpenCodeSourceSetupFailurePreservesStageAndCause(t *testing.T) {
	for _, stage := range []string{"download", "preparation", "validation"} {
		err := sourceOpenCodeSetupFailure(stage)
		cause := setupCause(err)
		if !strings.Contains(cause, stage+" from source failed:") || strings.Contains(cause, "unknown") {
			t.Fatalf("The source failure lost its stage or cause: %v", err)
		}
	}
	if got := setupSectionCause("The OpenCode download failed: archive unavailable"); got != "The cause is unknown." {
		t.Fatalf("The published failure display changed: %s", got)
	}
}
