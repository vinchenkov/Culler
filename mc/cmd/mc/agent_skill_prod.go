//go:build !test_fake_routing

package main

import (
	"errors"
	"mc/deployment"
	"mc/onboarding"
	"os"
	"path/filepath"
)

func init() { deployment.AgentSkillReleaseID = releaseBuildID }

func (w *setupWizard) offerAgentSkillIntegration() error {
	skill := filepath.Join(w.home, "release", "host", "skills", "culler")
	if _, err := os.Stat(filepath.Join(skill, "SKILL.md")); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if w.session.AgentSkillOffered {
		return nil
	}
	w.ui.Panel("Use Culler with Claude Code", []string{
		"The Culler skill lets an external agent inspect Culler and request changes through the same commands as Collie.",
		"Your confirmation rules still apply.",
		"The skill is also available directly at:", skill,
	})
	choice, err := w.ui.Choose("Add the Culler skill to Claude Code?", []onboarding.Choice{
		setupChoice("add", "Add the skill", ""), setupChoice("skip", "Keep the direct path only", ""),
	}, 0)
	if err != nil {
		return err
	}
	if choice == "add" {
		status, err := deployment.InstallAgentSkillIntegration(w.home, w.operatorHome)
		if err != nil {
			return err
		}
		if status == "conflict" {
			w.ui.Panel("Your existing skill stays intact", []string{
				"Another skill uses the Claude Code path. Culler kept it intact.",
				"Give your agent the direct Culler skill path above.",
			})
		} else {
			w.ui.Panel("The Culler skill is available", []string{
				"Claude Code can discover the Culler skill in your personal skills directory.",
			})
		}
	}
	w.session.AgentSkillOffered = true
	return w.save()
}
