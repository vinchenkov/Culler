//go:build !test_fake_routing

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	operatorconfig "mc/config"
	"mc/onboarding"
	"mc/substrate"
	"mc/verbs"
)

var setupDiagnosticRoot = "/tmp"

func setupSafeDiagnostic(message string) string {
	home, _ := configuredCanonicalHome()
	return setupDiagnosticAt(home, message)
}

func setupDiagnosticAt(home, message string) string {
	secrets := []string{}
	if cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml")); err == nil {
		secrets = append(secrets, cfg.Surfaces.Dashboard.AuthToken, cfg.Surfaces.Discord.BotToken)
	}
	return onboarding.Diagnostic(message, secrets...)
}

func writeSetupDiagnostic(home, operation string, state *setupDeployment, failure error) (string, error) {
	record := struct {
		Operation       string                     `json:"operation"`
		ObservedAt      string                     `json:"observed_at"`
		ObservedRuntime string                     `json:"observed_runtime_identity"`
		Expected        verbs.DoctorRuntimeRequest `json:"expected_runtime_identity"`
		Deployment      *setupDeployment           `json:"deployment,omitempty"`
		Details         []string                   `json:"details,omitempty"`
	}{
		Operation: operation, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ObservedRuntime: "unavailable",
		Expected: verbs.DoctorRuntimeRequest{ProtocolVersion: 1, ReleaseBuildID: releaseBuildID, ControlVersion: gatewayControlVersion, SpineSchemaID: substrate.CurrentSchemaID, ConfigSchemaVersion: configSchemaVersion},
	}
	if state != nil {
		copy := *state
		copy.Checks = append([]verbs.DoctorFinding(nil), state.Checks...)
		for i := range copy.Checks {
			f := &copy.Checks[i]
			f.Detail = setupDiagnosticAt(home, f.Detail)
			if f.Check == "spine" {
				if _, observed, ok := strings.Cut(f.Detail, "; observed "); ok {
					record.ObservedRuntime = observed
				} else if f.Status == "ok" {
					record.ObservedRuntime = "matches expected_runtime_identity"
				}
			}
		}
		copy.Release.Components = append([]setupReleaseComponent(nil), state.Release.Components...)
		for i := range copy.Release.Components {
			c := &copy.Release.Components[i]
			if c.Detail != "" {
				c.Detail = setupDiagnosticAt(home, c.Detail)
			}
		}
		record.Deployment = &copy
	}
	if failure != nil {
		record.Details = setupDiagnosticLines(operation, failure)
		for i := range record.Details {
			record.Details[i] = setupDiagnosticAt(home, record.Details[i])
		}
	}
	// A private temporary directory avoids changes to the deployment during inspection.
	dir, err := os.MkdirTemp(setupDiagnosticRoot, "mc-setup-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "diagnostic.json")
	body, err := json.MarshalIndent(record, "", "  ")
	if err == nil {
		err = os.WriteFile(path, append(body, '\n'), 0600)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

func (w *setupWizard) failureLines(label string, err error) []string {
	lines := []string{setupCause(err)}
	var failure *setupFailure
	if errors.As(err, &failure) && failure.command != "" {
		lines = append(lines, "Resume setup for this deployment:", failure.command)
	}
	if errors.As(err, &failure) && failure.record != "" {
		lines = append(lines, failure.details...)
		return append(lines, "Share this diagnostic record with your coding harness:", failure.record)
	}
	path, writeErr := writeSetupDiagnostic(w.home, label, nil, err)
	if writeErr != nil {
		return append(lines, "The diagnostic record could not be saved. View error details for this operation.")
	}
	return append(lines, "Share this diagnostic record with your coding harness:", path)
}
