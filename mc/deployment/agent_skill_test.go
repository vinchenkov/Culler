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
	return home, user, target
}

func skillLink(user, root string) string { return filepath.Join(user, root, "skills", "culler") }

func TestAgentSkillIntegrationPreservesOwnershipAcrossUpdateAndUninstall(t *testing.T) {
	home, user, target := agentSkillIntegrationFixture(t)
	for range 2 {
		if err := InstallAgentSkillIntegration(home, user, []string{".agents", ".claude", ".codex"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range AgentSkillRoots {
		if link, err := os.Readlink(skillLink(user, root)); err != nil || link != target {
			t.Fatalf("%s link = %q, %v", root, link, err)
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
	for _, root := range AgentSkillRoots {
		if _, err := os.Lstat(skillLink(user, root)); !os.IsNotExist(err) {
			t.Fatalf("the owned %s link remains: %v", root, err)
		}
	}
}

func TestAgentSkillIntegrationFollowsHarnessSelection(t *testing.T) {
	home, user, target := agentSkillIntegrationFixture(t)
	if err := InstallAgentSkillIntegration(home, user, []string{".agents", ".claude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(user, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("an unselected harness received a skill directory: %v", err)
	}
	if err := InstallAgentSkillIntegration(home, user, []string{".agents", ".codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(skillLink(user, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("the deselected harness keeps the owned link: %v", err)
	}
	for _, root := range []string{".agents", ".codex"} {
		if link, err := os.Readlink(skillLink(user, root)); err != nil || link != target {
			t.Fatalf("%s link = %q, %v", root, link, err)
		}
	}
}

func TestAgentSkillIntegrationReadsSingleLinkRecord(t *testing.T) {
	home, user, target := agentSkillIntegrationFixture(t)
	link := skillLink(user, ".claude")
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(AgentSkillIntegration{Path: link, Target: target})
	if err := os.WriteFile(filepath.Join(home, agentSkillIntegrationRecord), body, 0600); err != nil {
		t.Fatal(err)
	}
	remove, err := PrepareAgentSkillUninstall(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the recorded link remains: %v", err)
	}
}

func TestAgentSkillIntegrationKeepsUnrelatedSkillAndReplacement(t *testing.T) {
	home, user, _ := agentSkillIntegrationFixture(t)
	link := skillLink(user, ".claude")
	if err := os.MkdirAll(link, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(link, "SKILL.md"), []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallAgentSkillIntegration(home, user, []string{".agents", ".claude"}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(link, "SKILL.md")); string(body) != "unrelated" {
		t.Fatal("the unrelated skill changed")
	}
	remove, err := PrepareAgentSkillUninstall(home)
	if err != nil {
		t.Fatal(err)
	}
	agents := skillLink(user, ".agents")
	if err := os.Remove(agents); err != nil {
		t.Fatal(err)
	}
	replacement := t.TempDir()
	if err := os.Symlink(replacement, agents); err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(agents); err != nil || target != replacement {
		t.Fatal("uninstall removed the replacement")
	}
	if body, _ := os.ReadFile(filepath.Join(link, "SKILL.md")); string(body) != "unrelated" {
		t.Fatal("uninstall changed the unrelated skill")
	}
}
