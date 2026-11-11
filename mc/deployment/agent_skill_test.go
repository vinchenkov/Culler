package deployment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostSkillReleaseUsesAuthoritativeSources(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHostRelease(home, root); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(home, "release", "host", "skills", "culler")
	main, err := os.ReadFile(filepath.Join(installed, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"culler-model", "tasks", "packet-decisions", "project-configuration", "configuration", "diagnostics", "traces", "task-description", "writing"} {
		body, err := os.ReadFile(filepath.Join(installed, "references", name+".md"))
		if err != nil || len(body) == 0 {
			t.Fatalf("installed %s: %v", name, err)
		}
		if !strings.Contains(string(main), "references/"+name+".md") {
			t.Fatalf("the entry point omits %s", name)
		}
		if name == "task-description" || name == "writing" {
			expected, err := os.ReadFile(filepath.Join(root, "mc", "verbs", "skills", "shared", name, "SKILL.md"))
			if err != nil || string(body) != string(expected) {
				t.Fatalf("%s differs from its shared source", name)
			}
		}
	}
	var metadata map[string]any
	body, err := os.ReadFile(filepath.Join(installed, "installation.json"))
	if err != nil || json.Unmarshal(body, &metadata) != nil {
		t.Fatal("the installed skill has no path metadata")
	}
	if metadata["mc_home"] != home || metadata["release"] != AgentSkillReleaseID || metadata["trace_root"] != filepath.Join(home, "agentsview", "inspection") {
		t.Fatalf("metadata = %s", body)
	}
	prefix := metadata["command_prefix"].([]any)
	if prefix[1] != "MC_HOME="+home || !filepath.IsAbs(prefix[2].(string)) {
		t.Fatalf("prefix = %v", prefix)
	}
	if matches, err := HostReleaseMatches(home, root); err != nil || !matches {
		t.Fatalf("release match = %v, %v", matches, err)
	}
}

func agentSkillIntegrationFixture(t *testing.T) (string, string, string) {
	t.Helper()
	home, user := t.TempDir(), t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "release", "host", "skills", "culler")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("skill"), 0600); err != nil {
		t.Fatal(err)
	}
	return home, user, filepath.Join(user, ".claude", "skills", "culler")
}

func TestAgentSkillIntegrationPreservesOwnershipAcrossUpdateAndUninstall(t *testing.T) {
	home, user, link := agentSkillIntegrationFixture(t)
	for range 2 {
		status, err := InstallAgentSkillIntegration(home, user)
		if err != nil || status != "installed" {
			t.Fatalf("install = %q, %v", status, err)
		}
	}
	remove, err := PrepareCLIUninstall(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the owned link remains: %v", err)
	}
}

func TestAgentSkillIntegrationKeepsUnrelatedSkillAndReplacement(t *testing.T) {
	home, user, link := agentSkillIntegrationFixture(t)
	if err := os.MkdirAll(link, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(link, "SKILL.md"), []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if status, err := InstallAgentSkillIntegration(home, user); err != nil || status != "conflict" {
		t.Fatalf("conflict = %q, %v", status, err)
	}
	if body, _ := os.ReadFile(filepath.Join(link, "SKILL.md")); string(body) != "unrelated" {
		t.Fatal("the unrelated skill changed")
	}
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAgentSkillIntegration(home, user); err != nil {
		t.Fatal(err)
	}
	remove, err := PrepareAgentSkillUninstall(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	replacement := t.TempDir()
	if err := os.Symlink(replacement, link); err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != replacement {
		t.Fatal("uninstall removed the replacement")
	}
}
