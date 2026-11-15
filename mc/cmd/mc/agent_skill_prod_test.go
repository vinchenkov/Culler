//go:build !test_fake_routing

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupInstallsAgentSkillForSelectedHarnesses(t *testing.T) {
	w, ui := setupTestWizard(t)
	skill := filepath.Join(w.home, "release", "host", "skills", "culler")
	if err := os.MkdirAll(skill, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("skill"), 0600); err != nil {
		t.Fatal(err)
	}
	w.session.RuntimeBindings = []string{"chatgpt"}
	if err := w.installAgentSkill(); err != nil {
		t.Fatal(err)
	}
	for root, want := range map[string]bool{".agents": true, ".codex": true, ".claude": false} {
		target, err := os.Readlink(filepath.Join(w.operatorHome, root, "skills", "culler"))
		if want && (err != nil || target != skill) {
			t.Fatalf("%s link = %q, %v", root, target, err)
		}
		if !want && !os.IsNotExist(err) {
			t.Fatalf("%s received a link: %v", root, err)
		}
	}
	w.session.RuntimeBindings = []string{"chatgpt", "claude"}
	if err := w.installAgentSkill(); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(w.operatorHome, ".claude", "skills", "culler")); err != nil || target != skill {
		t.Fatalf(".claude link = %q, %v", target, err)
	}
	if len(ui.panels) != 0 || len(ui.choices) != 0 {
		t.Fatalf("the skill installation showed output: %q %v", ui.panels, ui.choices)
	}
}
