package verbs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// srcGit runs a git command in dir and fails the test on error.
func srcGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// buildSourceRepo builds a Git repository with two commits. It returns the
// repository directory, the head commit, and the object format.
func buildSourceRepo(t *testing.T) (dir, headSHA, objectFormat string) {
	t.Helper()
	dir = t.TempDir()
	srcGit(t, dir, "init", "-q")
	srcGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "README.md"), "hello world\n")
	writeFile(t, filepath.Join(dir, "lib/core.go"), "package lib\n")
	srcGit(t, dir, "add", "-A")
	srcGit(t, dir, "commit", "-qm", "c1")
	writeFile(t, filepath.Join(dir, "README.md"), "hello world\nsecond\n")
	srcGit(t, dir, "add", "-A")
	srcGit(t, dir, "commit", "-qm", "c2")
	headSHA = srcGit(t, dir, "rev-parse", "HEAD")
	objectFormat = srcGit(t, dir, "rev-parse", "--show-object-format")
	return dir, headSHA, objectFormat
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
