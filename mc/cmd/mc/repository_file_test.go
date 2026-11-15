package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The dashboard reads both versions of a changed file from the Project
// repository on the host. The command stays host-local when other verbs
// delegate to the helper.
func TestRepositoryFileCommandReadsAFileAtAnExactCommitOnTheHost(t *testing.T) {
	spine := initSpine(t)
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "--quiet", "-b", "culler")
	write("notes.md", "The first line.\nThe second line.\n")
	write("logo.bin", "\xff\xfe\x00binary")
	git("add", ".")
	git("commit", "--quiet", "-m", "Add the notes")
	before := git("rev-parse", "HEAD")
	write("notes.md", "The first line.\nThe changed line.\n")
	git("commit", "--quiet", "-am", "Change the notes")
	after := git("rev-parse", "HEAD")
	env := []string{"MC_HOME=" + filepath.Dir(spine), "MC_HELPER=mc-helper-that-must-never-be-reached"}

	for commit, want := range map[string]string{before: "The first line.\nThe second line.\n", after: "The first line.\nThe changed line.\n"} {
		res := runMC(t, env, "", "__repository-file", root, commit, "notes.md")
		if res.code != 0 || res.json["content"] != want || res.json["path"] != "notes.md" || res.json["commit"] != commit {
			t.Fatalf("__repository-file at %s gave %d %v (%s), want %q", commit[:7], res.code, res.json, res.stderr, want)
		}
	}
	for name, want := range map[string]string{
		"absent.md": "the commit holds no file at absent.md",
		"logo.bin":  "the repository file is not UTF-8 text: logo.bin",
	} {
		refused := runMC(t, env, "", "__repository-file", root, after, name)
		message, _ := refused.json["error"].(map[string]any)["message"].(string)
		if refused.code != 1 || message != want {
			t.Fatalf("__repository-file %s gave %d %v, want %q", name, refused.code, refused.json, want)
		}
	}
	for _, args := range [][]string{{root, after}, {"relative/root", after, "notes.md"}, {root, "culler", "notes.md"}, {root, after, "notes.md", "extra"}} {
		refused := runMC(t, env, "", append([]string{"__repository-file"}, args...)...)
		message, _ := refused.json["error"].(map[string]any)["message"].(string)
		if refused.code != 2 || message != "usage: mc __repository-file <workspace-root> <commit> <path>" {
			t.Fatalf("__repository-file %v gave %d %v", args, refused.code, refused.json)
		}
	}
	agent := runMC(t, collieJSONEnv(t, spine, "h-0123456789abcdef", []string{}), "", "__repository-file", root, after, "notes.md")
	if agent.code != 1 || !strings.Contains(agent.stderr, "host scope only") {
		t.Fatalf("a Collie caller gave %d %q", agent.code, agent.stderr)
	}
}
