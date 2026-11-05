//go:build !test_fake_routing

package main

import (
	"bytes"
	"strings"
	"testing"

	"mc/onboarding"
	"mc/verbs"
)

func init() {
	setupPreviewCases["dashboard-resume"] = setupPreviewDashboardResume
}

func setupPreviewDashboardResume(t *testing.T) setupPreviewCase {
	preview := setupPreviewProject(t, false)
	w := preview.wizard
	if err := w.resumeCheckedDeployment(setupDeployment{Exists: true}); err != nil {
		t.Fatal(err)
	}
	section := w.deps.section
	w.deps.section = func(name string, args verbs.OnboardArgs) error {
		if name == "surfaces" {
			parsed, err := parseOnboardArgs(wholeSectionArgs(name, args)[1:])
			if err != nil {
				return err
			}
			plan, err := verbs.PrepareOnboardSurfacesHost(parsed)
			if err != nil {
				return err
			}
			if _, _, err := verbs.ApplyOnboardSurfacesHost(plan); err != nil {
				return err
			}
		}
		return section(name, args)
	}
	return preview
}

func TestTerminalSetupResumeCompletesDashboardConfiguration(t *testing.T) {
	preview := setupPreviewDashboardResume(t)
	var input strings.Builder
	for _, step := range preview.steps {
		input.WriteString(step.Send)
	}
	var output bytes.Buffer
	terminal := onboarding.NewTerminal(strings.NewReader(input.String()), &output)
	terminal.SetStageTitles(setupStageTitles)
	preview.wizard.ui, preview.wizard.output = terminal, &output
	preview.run()
	text := output.String()
	if strings.Contains(text, "Failed") || !strings.Contains(text, "Set up your dashboard") ||
		!strings.Contains(text, "Open dashboard") {
		t.Fatal("The resumed setup did not reach the dashboard.")
	}
}
