//go:build !test_fake_routing

package main

import (
	"errors"
	"mc/deployment"
	"os"
	"path/filepath"
	"slices"
)

func init() { deployment.AgentSkillReleaseID = releaseBuildID }

// installAgentSkill links the Culler skill for each agent harness that setup selected.
func (w *setupWizard) installAgentSkill() error {
	skill := filepath.Join(w.home, "release", "host", "skills", "culler")
	if _, err := os.Stat(filepath.Join(skill, "SKILL.md")); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	cfg, err := w.catalog()
	if err != nil {
		return err
	}
	roots := []string{".agents"}
	harnessRoots := map[string]string{"claude-sdk": ".claude", "codex": ".codex"}
	for _, id := range w.session.RuntimeBindings {
		root := harnessRoots[cfg.Runtimes.Bindings[id].Harness]
		if root != "" && !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}
	return deployment.InstallAgentSkillIntegration(w.home, w.operatorHome, roots)
}
